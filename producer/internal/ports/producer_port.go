package ports

import "context"

type ProducerPort interface {
	Produce(ctx context.Context, count int) (ProduceResult, error)
	Close() error
}

type ProduceResult struct {
	TotalSent   int   `json:"total_sent"`
	TotalErrors int   `json:"total_errors"`
	DurationMs  int64 `json:"duration_ms"`
}
