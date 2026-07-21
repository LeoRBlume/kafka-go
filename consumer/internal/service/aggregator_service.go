package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/LeoRBlume/go-libs/logger"
	kafka "github.com/segmentio/kafka-go"
	"github.com/seu-usuario/kafka-go/consumer/config"
	"github.com/seu-usuario/kafka-go/consumer/internal/model"
	"github.com/seu-usuario/kafka-go/consumer/internal/ports"
)

const readBackoff = 2 * time.Second

// Aggregator é o serviço de agregação com observabilidade de métricas.
type Aggregator interface {
	ports.AggregatorPort
	Observe() Observation
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

// Observe expõe contadores + gauges de estado para o controller.
func (s *aggregatorService) Observe() Observation {
	return s.core.observe()
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
