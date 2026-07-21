package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LeoRBlume/go-libs/logger"
	"github.com/seu-usuario/kafka-go/consumer/internal/model"
	"github.com/seu-usuario/kafka-go/consumer/internal/ports"
)

// emitFunc publica o resultado de uma janela fechada no destino (tópico de saída).
type emitFunc func(ctx context.Context, res model.WindowResult) error

// aggregator é o núcleo puro da agregação janelada: sem Kafka, testável de forma
// determinística com Clock injetável e um emitFunc arbitrário.
type aggregator struct {
	store      ports.StateStorePort
	clock      ports.Clock
	windowSize time.Duration
	grace      time.Duration
	emit       emitFunc
	metrics    *Metrics

	wmMu       sync.RWMutex
	watermarks map[int]time.Time // event-time watermark por partição

	lastOpenCount int // usado para logar a lista completa só quando o conjunto muda
}

func newAggregator(store ports.StateStorePort, clock ports.Clock, windowSize, grace time.Duration, emit emitFunc) *aggregator {
	return &aggregator{
		store:      store,
		clock:      clock,
		windowSize: windowSize,
		grace:      grace,
		emit:       emit,
		metrics:    &Metrics{},
		watermarks: make(map[int]time.Time),
	}
}

// advanceWatermark eleva o watermark da partição para ts (nunca retrocede).
func (a *aggregator) advanceWatermark(partition int, ts time.Time) {
	a.wmMu.Lock()
	if cur, ok := a.watermarks[partition]; !ok || ts.After(cur) {
		a.watermarks[partition] = ts
	}
	a.wmMu.Unlock()
}

// watermark devolve o menor watermark entre as partições ativas (escolha
// conservadora: uma janela só fecha quando TODAS as partições ativas passaram
// dela, evitando fechar cedo por conta de uma partição atrasada). Zero se
// nenhuma partição recebeu evento ainda.
func (a *aggregator) watermark() time.Time {
	a.wmMu.RLock()
	defer a.wmMu.RUnlock()

	var min time.Time
	first := true
	for _, w := range a.watermarks {
		if first || w.Before(min) {
			min = w
			first = false
		}
	}
	return min
}

// processEvent aplica um evento ao estado: valida, calcula a janela, descarta
// late events, deduplica por ID e atualiza o agregado.
//
// Concorrência: cada entity_key pertence a exatamente uma partição
// (co-particionamento), logo cada WindowState tem um único escritor. Os campos
// escalares (Count/Sum) são publicados via uma NOVA instância a cada evento, de
// modo que o ticker de fechamento (outra goroutine) só observa snapshots
// imutáveis via store.All(). O mapa SeenIDs é reusado e tocado apenas por este
// escritor — o leitor de fechamento nunca o acessa.
func (a *aggregator) processEvent(ctx context.Context, partition int, ev model.Event) error {
	a.metrics.Received.Add(1)

	if ev.ID == "" || ev.EntityKey == "" || ev.Timestamp.IsZero() {
		a.metrics.Invalid.Add(1)
		logger.Warnf(ctx, "aggregator.processEvent",
			"invalid event skipped: id=%q entity_key=%q ts_zero=%t",
			ev.ID, ev.EntityKey, ev.Timestamp.IsZero())
		return nil
	}

	ts := ev.Timestamp.UTC()
	windowStart := ts.Truncate(a.windowSize)
	windowEnd := windowStart.Add(a.windowSize)

	if wm := a.watermark(); !wm.IsZero() && wm.After(windowEnd.Add(a.grace)) {
		a.metrics.Late.Add(1)
		logger.Warnf(ctx, "aggregator.processEvent",
			"late event dropped: id=%s entity_key=%s window_end=%s delay=%s",
			ev.ID, ev.EntityKey, windowEnd.Format(time.RFC3339), wm.Sub(windowEnd))
		return nil
	}

	cur, found, err := a.store.Get(ev.EntityKey, windowStart)
	if err != nil {
		return err
	}

	var (
		count int64
		sum   float64
		seen  map[string]struct{}
	)
	if found {
		count = cur.Count
		sum = cur.Sum
		seen = cur.SeenIDs
	}
	if seen == nil {
		seen = make(map[string]struct{})
	}

	if _, dup := seen[ev.ID]; dup {
		a.metrics.Duplicates.Add(1)
		a.advanceWatermark(partition, ts)
		logger.Debugf(ctx, "aggregator.processEvent",
			"duplicate skipped: entity_key=%s window_start=%s partition=%d",
			ev.EntityKey, windowStart.Format(time.RFC3339), partition)
		return nil
	}

	seen[ev.ID] = struct{}{}
	next := &model.WindowState{
		Count:   count + 1,
		Sum:     sum + ev.Value,
		SeenIDs: seen,
	}
	if err := a.store.Set(ev.EntityKey, windowStart, next); err != nil {
		return err
	}

	a.metrics.Aggregated.Add(1)
	a.advanceWatermark(partition, ts)
	logger.Debugf(ctx, "aggregator.processEvent",
		"bucket %s [%s → %s) += %.2f ⇒ count=%d sum=%.2f avg=%.2f",
		ev.EntityKey,
		windowStart.Format("15:04:05"), windowEnd.Format("15:04:05"),
		ev.Value, next.Count, next.Sum, average(next.Sum, next.Count))
	return nil
}

// closeWindows emite e remove todas as janelas cujo watermark já ultrapassou
// windowEnd + grace.
func (a *aggregator) closeWindows(ctx context.Context) error {
	wm := a.watermark()
	if wm.IsZero() {
		return nil
	}

	entries, err := a.store.All()
	if err != nil {
		return err
	}

	for _, e := range entries {
		windowEnd := e.WindowStart.Add(a.windowSize)
		deadline := windowEnd.Add(a.grace)
		if !wm.After(deadline) {
			continue
		}

		// trace_id por janela: entity_key|window_start, para correlacionar a
		// emissão de uma janela específica nos logs.
		wCtx := logger.WithTraceID(ctx, e.EntityKey+"|"+e.WindowStart.Format(time.RFC3339))

		logger.Infof(wCtx, "aggregator.closeWindows",
			"⏰ closing %s [%s → %s): watermark %s passed deadline %s (windowEnd+grace)",
			e.EntityKey, e.WindowStart.Format("15:04:05"), windowEnd.Format("15:04:05"),
			wm.Format("15:04:05"), deadline.Format("15:04:05"))

		res := model.WindowResult{
			EntityKey:   e.EntityKey,
			WindowStart: e.WindowStart,
			WindowEnd:   windowEnd,
			Count:       e.State.Count,
			Sum:         e.State.Sum,
			Avg:         average(e.State.Sum, e.State.Count),
			EmittedAt:   a.clock.Now(),
		}

		if err := a.emit(wCtx, res); err != nil {
			logger.Errorf(wCtx, "aggregator.closeWindows",
				"failed to emit window entity_key=%s window_start=%s (kept for retry)",
				err, e.EntityKey, e.WindowStart.Format(time.RFC3339))
			continue
		}

		if err := a.store.Delete(e.EntityKey, e.WindowStart); err != nil {
			return err
		}
		a.metrics.Emitted.Add(1)

		logger.Infof(wCtx, "aggregator.closeWindows",
			"✅ CLOSED %s [%s → %s) FINAL count=%d sum=%.2f avg=%.2f → emitido em windowed-results",
			res.EntityKey, res.WindowStart.Format("15:04:05"), res.WindowEnd.Format("15:04:05"),
			res.Count, res.Sum, res.Avg)
	}
	return nil
}

// logOpenWindows registra o estado das janelas abertas de forma focada:
//   - a lista completa (com count/sum de cada bucket) sempre que o conjunto muda;
//   - um resumo por chamada apontando a PRÓXIMA a fechar e quanto falta de
//     event-time para o watermark cruzar o deadline (windowEnd + grace).
func (a *aggregator) logOpenWindows(ctx context.Context) {
	wm := a.watermark()
	entries, err := a.store.All()
	if err != nil {
		return
	}

	// ordena por deadline (windowEnd+grace) e entity_key → saída estável, e o
	// primeiro elemento é sempre a próxima janela a fechar.
	sort.Slice(entries, func(i, j int) bool {
		di := entries[i].WindowStart.Add(a.windowSize + a.grace)
		dj := entries[j].WindowStart.Add(a.windowSize + a.grace)
		if di.Equal(dj) {
			return entries[i].EntityKey < entries[j].EntityKey
		}
		return di.Before(dj)
	})

	// lista completa só quando o número de janelas abertas muda (evita flood).
	if len(entries) != a.lastOpenCount {
		a.lastOpenCount = len(entries)
		if len(entries) > 0 {
			var b strings.Builder
			for i, e := range entries {
				if i > 0 {
					b.WriteString("  |  ")
				}
				fmt.Fprintf(&b, "%s[%s) count=%d sum=%.2f",
					e.EntityKey, e.WindowStart.Format("15:04:05"), e.State.Count, e.State.Sum)
			}
			logger.Debugf(ctx, "aggregator.openWindows", "📋 %d janelas abertas: %s", len(entries), b.String())
		} else {
			logger.Debug(ctx, "aggregator.openWindows", "📋 0 janelas abertas")
		}
	}

	if len(entries) == 0 {
		return
	}

	next := entries[0]
	deadline := next.WindowStart.Add(a.windowSize + a.grace)
	remaining := deadline.Sub(wm).Truncate(time.Second)
	logger.Debugf(ctx, "aggregator.openWindows",
		"⏳ próxima a fechar: %s [%s) count=%d sum=%.2f — fecha quando watermark>%s (faltam +%s de event-time; watermark=%s)",
		next.EntityKey, next.WindowStart.Format("15:04:05"), next.State.Count, next.State.Sum,
		deadline.Format("15:04:05"), remaining, wm.Format("15:04:05"))
}

func average(sum float64, count int64) float64 {
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// observe devolve uma leitura pontual dos contadores + gauges de estado
// (janelas abertas e watermark por partição) para o endpoint de métricas.
func (a *aggregator) observe() Observation {
	entries, _ := a.store.All()

	a.wmMu.RLock()
	watermarks := make(map[int]time.Time, len(a.watermarks))
	for p, t := range a.watermarks {
		watermarks[p] = t
	}
	a.wmMu.RUnlock()

	return Observation{
		Metrics:     a.metrics.snapshot(),
		OpenWindows: len(entries),
		Watermarks:  watermarks,
	}
}
