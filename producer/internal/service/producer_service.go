package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	kafka "github.com/segmentio/kafka-go"
	"github.com/seu-usuario/kafka-go/producer/config"
	"github.com/seu-usuario/kafka-go/producer/internal/model"
	"github.com/seu-usuario/kafka-go/producer/internal/ports"
)

type producerService struct {
	writer *kafka.Writer
	cfg    *config.Config
}

// NewProducerService creates a kafka writer and returns a ProducerPort implementation.
func NewProducerService(cfg *config.Config) ports.ProducerPort {
	writer := &kafka.Writer{
		Addr:         kafka.TCP(cfg.KafkaBroker),
		Topic:        cfg.KafkaTopic,
		Async:        false,
		BatchSize:    cfg.BatchSize,
		BatchTimeout: 100 * time.Millisecond,
		RequiredAcks: kafka.RequireOne,
		Compression:  kafka.Snappy,
	}

	return &producerService{
		writer: writer,
		cfg:    cfg,
	}
}

// Produce dispatches `count` messages to Kafka using a worker pool.
// Channel buffer = WorkerCount * BatchSize (both from config).
// Workers = WorkerCount (from config).
func (s *producerService) Produce(ctx context.Context, count int) (ports.ProduceResult, error) {
	start := time.Now()

	// Buffer sized by config so the dispatcher rarely blocks.
	jobs := make(chan kafka.Message, s.cfg.WorkerCount*s.cfg.BatchSize)

	var sent atomic.Int64
	var errs atomic.Int64
	var wg sync.WaitGroup

	// Start WorkerCount goroutines, each draining the jobs channel.
	for i := 0; i < s.cfg.WorkerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for msg := range jobs {
				if err := s.writer.WriteMessages(ctx, msg); err != nil {
					errs.Add(1)
				} else {
					sent.Add(1)
				}
			}
		}()
	}

	// Dispatcher: generates all messages and pushes them into the channel.
	go func() {
		for i := 0; i < count; i++ {
			msg := model.Message{
				ID:        uuid.New().String(),
				Payload:   fmt.Sprintf("message payload %d", i),
				Timestamp: time.Now(),
				Source:    "producer",
				SeqNumber: i,
			}

			data, err := json.Marshal(msg)
			if err != nil {
				errs.Add(1)
				continue
			}

			jobs <- kafka.Message{
				Key:   []byte(msg.ID),
				Value: data,
			}
		}
		close(jobs)
	}()

	wg.Wait()

	return ports.ProduceResult{
		TotalSent:   int(sent.Load()),
		TotalErrors: int(errs.Load()),
		DurationMs:  time.Since(start).Milliseconds(),
	}, nil
}

func (s *producerService) Close() error {
	return s.writer.Close()
}
