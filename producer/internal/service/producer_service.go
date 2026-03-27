package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LeoRBlume/go-libs/logger"
	"github.com/google/uuid"
	kafka "github.com/segmentio/kafka-go"
	"github.com/seu-usuario/kafka-go/producer/config"
	"github.com/seu-usuario/kafka-go/producer/internal/model"
	"github.com/seu-usuario/kafka-go/producer/internal/ports"
)

type job struct {
	msg  kafka.Message
	done func(err error)
}

type producerService struct {
	writer *kafka.Writer
	jobs   chan job
}

// NewProducerService creates a kafka writer, starts a persistent worker pool and returns a ProducerPort implementation.
func NewProducerService(cfg *config.Config) ports.ProducerPort {
	writer := &kafka.Writer{
		Addr:         kafka.TCP(cfg.KafkaBroker),
		Topic:        cfg.KafkaTopic,
		Async:        false,
		RequiredAcks: kafka.RequireOne,
		Compression:  kafka.Snappy,
	}

	svc := &producerService{
		writer: writer,
		jobs:   make(chan job, cfg.ChannelSize),
	}

	for i := 0; i < cfg.WorkerCount; i++ {
		go svc.worker()
	}

	return svc
}

func (s *producerService) worker() {
	for j := range s.jobs {
		j.done(s.writer.WriteMessages(context.Background(), j.msg))
	}
}

// Produce dispatches `count` messages into the persistent worker pool and waits for all to complete.
func (s *producerService) Produce(ctx context.Context, count int) (ports.ProduceResult, error) {
	start := time.Now()

	logger.Infof(ctx, "producerService.Produce", "starting to produce %d messages", count)

	var sent atomic.Int64
	var errs atomic.Int64
	var wg sync.WaitGroup

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
			logger.Error(ctx, "producerService.Produce", "failed to marshal message", err)
			errs.Add(1)
			continue
		}

		wg.Add(1)
		select {
		case s.jobs <- job{
			msg: kafka.Message{Key: []byte(msg.ID), Value: data},
			done: func(err error) {
				defer wg.Done()
				if err != nil {
					logger.Error(ctx, "producerService.Produce", "failed to write message", err)
					errs.Add(1)
				} else {
					sent.Add(1)
				}
			},
		}:
		case <-ctx.Done():
			wg.Done()
			logger.Warn(ctx, "producerService.Produce", "dispatcher cancelled, stopping message generation")
			goto wait
		}
	}

wait:
	wg.Wait()

	result := ports.ProduceResult{
		TotalSent:   int(sent.Load()),
		TotalErrors: int(errs.Load()),
		DurationMs:  time.Since(start).Milliseconds(),
	}

	logger.Infof(ctx, "producerService.Produce", "produce complete: sent=%d errors=%d duration=%dms",
		result.TotalSent, result.TotalErrors, result.DurationMs)

	return result, nil
}

func (s *producerService) Close() error {
	close(s.jobs)
	return s.writer.Close()
}
