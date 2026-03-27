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

type consumerService struct {
	reader *kafka.Reader
}

// NewConsumerService creates a kafka reader and returns a ConsumerPort implementation.
func NewConsumerService(cfg *config.Config) ports.ConsumerPort {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{cfg.KafkaBroker},
		Topic:       cfg.KafkaTopic,
		GroupID:     cfg.KafkaGroupID,
		StartOffset: cfg.KafkaStartOffset,
	})

	return &consumerService{reader: reader}
}

// Start reads messages from Kafka in a loop until ctx is cancelled.
func (s *consumerService) Start(ctx context.Context) error {
	for {
		m, err := s.reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			logger.Error(ctx, "consumerService.Start", "failed to read message", err)
			continue
		}

		var msg model.Message
		if err := json.Unmarshal(m.Value, &msg); err != nil {
			logger.Errorf(ctx, "consumerService.Start", "failed to unmarshal message at offset %d", err, m.Offset)
			continue
		}

		latency := time.Since(msg.Timestamp)

		logger.Infof(ctx, "consumerService.Start",
			"message received: id=%s seq=%d partition=%d offset=%d latency=%s",
			msg.ID, msg.SeqNumber, m.Partition, m.Offset, latency)
	}
}

func (s *consumerService) Close() error {
	return s.reader.Close()
}
