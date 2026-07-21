package ports

import "context"

// AggregatorPort é o contrato do serviço de agregação janelada.
type AggregatorPort interface {
	// Start restaura o estado do último snapshot e consome o tópico de entrada
	// até o ctx ser cancelado.
	Start(ctx context.Context) error
	// Close executa um flush final de snapshot e libera recursos.
	// NÃO emite janelas ainda abertas.
	Close() error
}
