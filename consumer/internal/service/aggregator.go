package service

import (
	"context"
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
		"aggregated: entity_key=%s window_start=%s partition=%d value=%.4f count=%d sum=%.4f",
		ev.EntityKey, windowStart.Format(time.RFC3339), partition, ev.Value, next.Count, next.Sum)
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
		if !wm.After(windowEnd.Add(a.grace)) {
			continue
		}

		// trace_id por janela: entity_key|window_start, para correlacionar a
		// emissão de uma janela específica nos logs.
		wCtx := logger.WithTraceID(ctx, e.EntityKey+"|"+e.WindowStart.Format(time.RFC3339))

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
			"window emitted: entity_key=%s window=[%s,%s) count=%d sum=%.4f avg=%.4f watermark=%s",
			res.EntityKey, res.WindowStart.Format(time.RFC3339), res.WindowEnd.Format(time.RFC3339),
			res.Count, res.Sum, res.Avg, wm.Format(time.RFC3339))
	}
	return nil
}

// openWindows devolve o número de janelas vivas no estado (gauge de backlog).
func (a *aggregator) openWindows() int {
	entries, _ := a.store.All()
	return len(entries)
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
