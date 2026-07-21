package ports

import (
	"time"

	"github.com/seu-usuario/kafka-go/consumer/internal/model"
)

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
