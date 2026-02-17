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

// PruneOlderThan removes entries older than cutoff.  The in-memory store
// only keeps the latest heartbeat per module, so this is a simple scan.
func (s *Store) PruneOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var deleted int64
	for id, rec := range s.data {
		if rec.ReceivedAt.Before(cutoff) {
			delete(s.data, id)
			deleted++
		}
	}
	return deleted, nil
}
