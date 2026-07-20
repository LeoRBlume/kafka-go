package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/seu-usuario/kafka-go/consumer/internal/model"
	"github.com/seu-usuario/kafka-go/consumer/internal/ports"
)

const snapshotFileName = "snapshot.json"

// stateKeyLayout é ordenável e sempre em UTC.
const stateKeyLayout = "2006-01-02T15:04:05Z"

// record é uma janela viva materializada no store.
type record struct {
	EntityKey   string             `json:"entity_key"`
	WindowStart time.Time          `json:"window_start"`
	State       *model.WindowState `json:"state"`
}

// snapshotFile é o formato persistido em disco: estado + offsets por partição.
type snapshotFile struct {
	Offsets map[int]int64 `json:"offsets"`
	Records []record      `json:"records"`
}

// diskStateStore persiste o estado do agregador em disco local (v1).
// O mapa é protegido por RWMutex: cada WindowState tem um único escritor
// (a goroutine da partição dona da entity_key, por co-particionamento), mas
// o mapa em si e o snapshot concorrente exigem o lock para segurança de corrida.
type diskStateStore struct {
	dir  string
	mu   sync.RWMutex
	data map[string]*record
}

// NewDiskStateStore cria o store e garante que o diretório de estado existe.
func NewDiskStateStore(dir string) (ports.StateStorePort, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("statestore: create dir %q: %w", dir, err)
	}
	return &diskStateStore{
		dir:  dir,
		data: make(map[string]*record),
	}, nil
}

func stateKey(entityKey string, windowStart time.Time) string {
	return entityKey + "|" + windowStart.UTC().Format(stateKeyLayout)
}

func (s *diskStateStore) Get(entityKey string, windowStart time.Time) (*model.WindowState, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, ok := s.data[stateKey(entityKey, windowStart)]
	if !ok {
		return nil, false, nil
	}
	return rec.State, true, nil
}

func (s *diskStateStore) Set(entityKey string, windowStart time.Time, state *model.WindowState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[stateKey(entityKey, windowStart)] = &record{
		EntityKey:   entityKey,
		WindowStart: windowStart.UTC(),
		State:       state,
	}
	return nil
}

func (s *diskStateStore) Delete(entityKey string, windowStart time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, stateKey(entityKey, windowStart))
	return nil
}

func (s *diskStateStore) All() ([]ports.StateEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]ports.StateEntry, 0, len(s.data))
	for _, rec := range s.data {
		entries = append(entries, ports.StateEntry{
			EntityKey:   rec.EntityKey,
			WindowStart: rec.WindowStart,
			State:       rec.State,
		})
	}
	return entries, nil
}

// Snapshot persiste estado + offsets atomicamente: escreve num arquivo temporário
// e faz rename para o destino final (nunca deixa arquivo meio-escrito).
func (s *diskStateStore) Snapshot(offsets map[int]int64) error {
	s.mu.RLock()
	snap := snapshotFile{
		Offsets: make(map[int]int64, len(offsets)),
		Records: make([]record, 0, len(s.data)),
	}
	for p, o := range offsets {
		snap.Offsets[p] = o
	}
	for _, rec := range s.data {
		snap.Records = append(snap.Records, *rec)
	}
	s.mu.RUnlock()

	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("statestore: marshal snapshot: %w", err)
	}

	final := filepath.Join(s.dir, snapshotFileName)
	tmp, err := os.CreateTemp(s.dir, snapshotFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("statestore: create temp: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("statestore: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("statestore: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("statestore: close temp: %w", err)
	}

	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("statestore: rename snapshot: %w", err)
	}
	return nil
}

// Restore recarrega o estado do último snapshot e devolve os offsets por partição.
// Sem snapshot prévio, devolve um mapa vazio (sem erro).
func (s *diskStateStore) Restore() (map[int]int64, error) {
	final := filepath.Join(s.dir, snapshotFileName)

	data, err := os.ReadFile(final)
	if err != nil {
		if os.IsNotExist(err) {
			return map[int]int64{}, nil
		}
		return nil, fmt.Errorf("statestore: read snapshot: %w", err)
	}

	var snap snapshotFile
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("statestore: unmarshal snapshot: %w", err)
	}

	s.mu.Lock()
	s.data = make(map[string]*record, len(snap.Records))
	for i := range snap.Records {
		rec := snap.Records[i]
		if rec.State != nil && rec.State.SeenIDs == nil {
			rec.State.SeenIDs = make(map[string]struct{})
		}
		s.data[stateKey(rec.EntityKey, rec.WindowStart)] = &rec
	}
	s.mu.Unlock()

	if snap.Offsets == nil {
		return map[int]int64{}, nil
	}
	return snap.Offsets, nil
}
