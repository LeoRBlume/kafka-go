package ports

import (
	"context"
	"time"

	"github.com/seu-usuario/kafka-go/consumer/internal/model"
)

// AggregatorPort é o contrato do serviço de agregação janelada.
type AggregatorPort interface {
	// Start restaura o estado do último snapshot e consome o tópico de entrada
	// até o ctx ser cancelado.
	Start(ctx context.Context) error
	// Close executa um flush final de snapshot e libera recursos.
	// NÃO emite janelas ainda abertas.
	Close() error
}

// StateStorePort abstrai a persistência do estado do agregador.
// v1: disco local; a interface permite trocar por um changelog topic depois.
type StateStorePort interface {
	Get(entityKey string, windowStart time.Time) (*model.WindowState, bool, error)
	Set(entityKey string, windowStart time.Time, state *model.WindowState) error
	Delete(entityKey string, windowStart time.Time) error
	// All devolve uma cópia de todo o estado vivo, indexado por (entityKey, windowStart).
	All() ([]StateEntry, error)
	// Snapshot persiste todo o estado + offsets por partição, atomicamente.
	Snapshot(offsets map[int]int64) error
	// Restore recarrega o estado e devolve os offsets do último snapshot.
	Restore() (map[int]int64, error)
}

// StateEntry é uma janela viva materializada, usada na varredura de fechamento.
type StateEntry struct {
	EntityKey   string
	WindowStart time.Time
	State       *model.WindowState
}

// Clock abstrai o relógio para permitir avançar o watermark de forma
// determinística nos testes, sem time.Sleep.
type Clock interface {
	Now() time.Time
}

// RealClock é a implementação de produção do Clock (UTC).
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }
