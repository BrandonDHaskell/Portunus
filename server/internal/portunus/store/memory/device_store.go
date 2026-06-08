package memory

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
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

// GetModuleType returns PEU for all known modules — the memory store is only
// used in provision-service tests, which always need PEU modules.
func (s *DeviceStore) GetModuleType(_ context.Context, moduleID string) (store.ModuleType, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.known[moduleID]; ok {
		return store.ModuleTypePEU, nil
	}
	return "", store.ErrNotFound
}
