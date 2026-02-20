package memory

import (
	"context"
	"sync"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

// AccessEventStore is an in-memory append-only log of access decisions.
// It is intended for use in tests and dev environments.
type AccessEventStore struct {
	mu     sync.Mutex
	events []store.AccessEventRecord
}

func NewAccessEventStore() *AccessEventStore {
	return &AccessEventStore{}
}

func (s *AccessEventStore) RecordEvent(_ context.Context, rec store.AccessEventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, rec)
	return nil
}

// Events returns a copy of all recorded events.  Test-only helper.
func (s *AccessEventStore) Events() []store.AccessEventRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.AccessEventRecord, len(s.events))
	copy(out, s.events)
	return out
}
