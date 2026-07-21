package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LeoRBlume/go-libs/logger"
	kafka "github.com/segmentio/kafka-go"
	"github.com/seu-usuario/kafka-go/consumer/config"
	"github.com/seu-usuario/kafka-go/consumer/internal/model"
	"github.com/seu-usuario/kafka-go/consumer/internal/ports"
)

const readBackoff = 2 * time.Second

// emitFunc publica o resultado de uma janela fechada no destino (tópico de saída).
type emitFunc func(ctx context.Context, res model.WindowResult) error

// Metrics são os contadores observáveis do agregador (thread-safe via atomic).
type Metrics struct {
	Received   atomic.Int64
	Invalid    atomic.Int64
	Late       atomic.Int64
	Duplicates atomic.Int64
	Aggregated atomic.Int64
	Emitted    atomic.Int64
}

// MetricsSnapshot é uma leitura pontual dos contadores, exposta pelo controller.
type MetricsSnapshot struct {
	Received       int64 `json:"received"`
	Invalid        int64 `json:"invalid"`
	LateDropped    int64 `json:"late_events_dropped"`
	Duplicates     int64 `json:"duplicates"`
	Aggregated     int64 `json:"aggregated"`
	WindowsEmitted int64 `json:"windows_emitted"`
}

func (m *Metrics) snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Received:       m.Received.Load(),
		Invalid:        m.Invalid.Load(),
		LateDropped:    m.Late.Load(),
		Duplicates:     m.Duplicates.Load(),
		Aggregated:     m.Aggregated.Load(),
		WindowsEmitted: m.Emitted.Load(),
	}
}

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

		res := model.WindowResult{
			EntityKey:   e.EntityKey,
			WindowStart: e.WindowStart,
			WindowEnd:   windowEnd,
			Count:       e.State.Count,
			Sum:         e.State.Sum,
			Avg:         average(e.State.Sum, e.State.Count),
			EmittedAt:   a.clock.Now(),
		}

		if err := a.emit(ctx, res); err != nil {
			logger.Errorf(ctx, "aggregator.closeWindows",
				"failed to emit window entity_key=%s window_start=%s (kept for retry)",
				err, e.EntityKey, e.WindowStart.Format(time.RFC3339))
			continue
		}

		if err := a.store.Delete(e.EntityKey, e.WindowStart); err != nil {
			return err
		}
		a.metrics.Emitted.Add(1)

		logger.Infof(ctx, "aggregator.closeWindows",
			"window emitted: entity_key=%s window=[%s,%s) count=%d sum=%.4f avg=%.4f",
			res.EntityKey, res.WindowStart.Format(time.RFC3339), res.WindowEnd.Format(time.RFC3339),
			res.Count, res.Sum, res.Avg)
	}
	return nil
}

func average(sum float64, count int64) float64 {
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// aggregatorService liga o core ao Kafka num loop sequencial e single-threaded:
// lê uma mensagem, agrega, fecha janelas prontas, faz snapshot e comita o offset,
// então pega a próxima. Sem workers, canais ou tickers.
type aggregatorService struct {
	cfg   *config.Config
	core  *aggregator
	store ports.StateStorePort

	reader *kafka.Reader
	writer *kafka.Writer

	offsets map[int]int64 // próximo offset a comitar por partição (Offset+1)
}

// Aggregator é o serviço de agregação com observabilidade de métricas.
type Aggregator interface {
	ports.AggregatorPort
	MetricsSnapshot() MetricsSnapshot
}

// NewAggregatorService constrói o serviço de agregação janelada.
func NewAggregatorService(cfg *config.Config) (Aggregator, error) {
	store, err := NewDiskStateStore(cfg.StateDir)
	if err != nil {
		return nil, err
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{cfg.KafkaBroker},
		Topic:       cfg.KafkaTopic,
		GroupID:     cfg.KafkaGroupID,
		StartOffset: cfg.KafkaStartOffset,
	})

	writer := &kafka.Writer{
		Addr:         kafka.TCP(cfg.KafkaBroker),
		Topic:        cfg.OutputTopic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
	}

	s := &aggregatorService{
		cfg:     cfg,
		store:   store,
		reader:  reader,
		writer:  writer,
		offsets: make(map[int]int64),
	}
	s.core = newAggregator(store, ports.RealClock{}, cfg.WindowSize, cfg.GracePeriod, s.emit)
	return s, nil
}

// MetricsSnapshot expõe os contadores para o controller.
func (s *aggregatorService) MetricsSnapshot() MetricsSnapshot {
	return s.core.metrics.snapshot()
}

// emit publica um WindowResult no tópico de saída. Chamado apenas pelo loop
// sequencial, então não precisa de lock.
func (s *aggregatorService) emit(ctx context.Context, res model.WindowResult) error {
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return s.writer.WriteMessages(ctx, kafka.Message{Key: []byte(res.EntityKey), Value: data})
}

// Start restaura o estado e processa o tópico de entrada num loop sequencial até
// ctx ser cancelado, com um snapshot final antes de retornar.
func (s *aggregatorService) Start(ctx context.Context) error {
	restored, err := s.store.Restore()
	if err != nil {
		return err
	}
	for p, o := range restored {
		s.offsets[p] = o
	}
	logger.Infof(ctx, "aggregatorService.Start",
		"state restored: %d partition offsets snapshotted; group resumes from committed offsets",
		len(restored))

	for {
		m, err := s.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				break
			}
			logger.Error(ctx, "aggregatorService.Start", "failed to fetch message", err)
			select {
			case <-time.After(readBackoff):
			case <-ctx.Done():
			}
			if ctx.Err() != nil {
				break
			}
			continue
		}

		s.handleMessage(ctx, m)
	}

	logger.Info(ctx, "aggregatorService.Start", "context cancelled, taking final snapshot")
	if err := s.store.Snapshot(s.offsets); err != nil {
		logger.Error(ctx, "aggregatorService.Start", "final snapshot failed", err)
	}
	return nil
}

// handleMessage processa uma mensagem de ponta a ponta: agrega, fecha janelas
// prontas, faz snapshot do estado+offset e SÓ ENTÃO comita o offset no Kafka
// (regra de ouro: nunca comita além do que foi snapshotado).
func (s *aggregatorService) handleMessage(ctx context.Context, m kafka.Message) {
	var ev model.Event
	if err := json.Unmarshal(m.Value, &ev); err != nil {
		s.core.metrics.Received.Add(1)
		s.core.metrics.Invalid.Add(1)
		logger.Errorf(ctx, "aggregatorService.handleMessage",
			"failed to unmarshal message at partition %d offset %d", err, m.Partition, m.Offset)
	} else if err := s.core.processEvent(ctx, m.Partition, ev); err != nil {
		logger.Error(ctx, "aggregatorService.handleMessage", "failed to process event", err)
	}

	if err := s.core.closeWindows(ctx); err != nil {
		logger.Error(ctx, "aggregatorService.handleMessage", "closeWindows failed", err)
	}

	s.offsets[m.Partition] = m.Offset + 1
	if err := s.store.Snapshot(s.offsets); err != nil {
		logger.Error(ctx, "aggregatorService.handleMessage", "snapshot failed, offset not committed", err)
		return
	}
	if err := s.reader.CommitMessages(ctx, m); err != nil {
		logger.Error(ctx, "aggregatorService.handleMessage", "commit failed", err)
	}
}

// Close libera os recursos de rede. O snapshot final acontece em Start (ctx.Done).
func (s *aggregatorService) Close() error {
	var err error
	if e := s.reader.Close(); e != nil {
		err = e
	}
	if e := s.writer.Close(); e != nil && err == nil {
		err = e
	}
	return err
}
