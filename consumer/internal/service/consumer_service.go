package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

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
			slog.Error("failed to read message", "error", err)
			continue
		}

		var msg model.Message
		if err := json.Unmarshal(m.Value, &msg); err != nil {
			slog.Error("failed to unmarshal message", "error", err, "offset", m.Offset)
			continue
		}

		latency := time.Since(msg.Timestamp)

		slog.Info("message received",
			"offset", m.Offset,
			"partition", m.Partition,
			"topic", m.Topic,
			"key", string(m.Key),
			"message_id", msg.ID,
			"seq_number", msg.SeqNumber,
			"payload", msg.Payload,
			"timestamp", msg.Timestamp,
			"latency", latency,
		)
	}
}

func (s *consumerService) Close() error {
	return s.reader.Close()
}
