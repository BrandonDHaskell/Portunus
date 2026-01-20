package memory

import (
	"context"
	"strings"
	"sync"
	"time"
)

type DeviceStore struct {
	mu    sync.RWMutex
	known map[string]struct{}
	seen  map[string]time.Time
}

func NewDeviceStore(knownModules []string) *DeviceStore {
	k := make(map[string]struct{}, len(knownModules))
	for _, m := range knownModules {
		m = strings.TrimSpace(m)
		if m != "" {
			k[m] = struct{}{}
		}
	}
	return &DeviceStore{
		known: k,
		seen:  make(map[string]time.Time),
	}
}

func (s *DeviceStore) IsKnown(_ context.Context, moduleID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.known[moduleID]
	return ok, nil
}

func (s *DeviceStore) MarkSeen(_ context.Context, moduleID string, _ bool, t time.Time) error {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[moduleID] = t
	return nil
}
