package ports

import "context"

type ConsumerPort interface {
	Start(ctx context.Context) error
	Close() error
}
