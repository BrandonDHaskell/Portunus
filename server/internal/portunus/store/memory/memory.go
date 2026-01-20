package memory

import (
	"context"
	"sync"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type Store struct {
	mu   sync.RWMutex
	data map[string]store.HeartbeatRecord
}

func New() *Store {
	return &Store{
		data: make(map[string]store.HeartbeatRecord),
	}
}

func (s *Store) UpsertHeartbeat(_ context.Context, moduleID string, rec store.HeartbeatRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec.ReceivedAt.IsZero() {
		rec.ReceivedAt = time.Now().UTC()
	}
	s.data[moduleID] = rec
	return nil
}
