package service

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

var (
	ErrInvalidModuleID = errors.New("module_id is required")
)

type HeartbeatService struct {
	heartbeatStore store.HeartbeatStore
	registry       *DeviceRegistry
	logger         *log.Logger

	// Last reported health per module, so a fault is logged when it starts
	// and when it clears rather than on every heartbeat. In memory only: a
	// restart logs a still-present fault again, which is what an operator
	// wants.
	healthMu sync.Mutex
	health   map[string]moduleHealth
}

// moduleHealth holds the optional health flags last reported by a module;
// nil means not reported.
type moduleHealth struct {
	readerOK *bool
	strikeOK *bool
}

func NewHeartbeatService(hs store.HeartbeatStore, reg *DeviceRegistry) *HeartbeatService {
	return &HeartbeatService{heartbeatStore: hs, registry: reg, health: map[string]moduleHealth{}}
}

// SetLogger attaches a logger for module health changes. When nil (the
// default), they are not logged.
func (s *HeartbeatService) SetLogger(l *log.Logger) { s.logger = l }

func (s *HeartbeatService) Record(ctx context.Context, req types.HeartbeatRequest) (types.HeartbeatResponse, error) {
	moduleID := strings.TrimSpace(req.ModuleID)
	if moduleID == "" {
		return types.HeartbeatResponse{}, ErrInvalidModuleID
	}

	known, err := s.registry.IsKnown(ctx, moduleID)
	if err != nil {
		return types.HeartbeatResponse{}, err
	}
	_ = s.registry.NoteSeen(ctx, moduleID, known)

	rec := store.HeartbeatRecord{
		ReceivedAt: time.Now().UTC(),
		Request:    req,
	}

	if err := s.heartbeatStore.UpsertHeartbeat(ctx, moduleID, rec); err != nil {
		return types.HeartbeatResponse{}, err
	}
	s.noteHealth(moduleID, req)

	return types.HeartbeatResponse{
		OK:         true,
		Known:      known,
		ModuleID:   moduleID,
		ServerTime: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

// noteHealth logs a module's reader or strike fault when it first appears and
// when it clears. A flag the module stops reporting is forgotten, not logged.
func (s *HeartbeatService) noteHealth(moduleID string, req types.HeartbeatRequest) {
	s.healthMu.Lock()
	prev := s.health[moduleID]
	s.health[moduleID] = moduleHealth{readerOK: req.ReaderOK, strikeOK: req.StrikeOK}
	s.healthMu.Unlock()

	if s.logger == nil {
		return
	}
	switch healthChange(prev.strikeOK, req.StrikeOK) {
	case healthFailed:
		s.logger.Printf("WARN module %s: strike did not lock, door may be unlocked", moduleID)
	case healthRecovered:
		s.logger.Printf("module %s: strike locking again", moduleID)
	}
	switch healthChange(prev.readerOK, req.ReaderOK) {
	case healthFailed:
		s.logger.Printf("WARN module %s: card reader degraded, cards cannot be read", moduleID)
	case healthRecovered:
		s.logger.Printf("module %s: card reader recovered", moduleID)
	}
}

type healthTransition int

const (
	healthUnchanged healthTransition = iota
	healthFailed
	healthRecovered
)

// healthChange compares two reports of one flag. An unknown previous value
// counts as healthy, so a fault present in the first report is logged and a
// healthy first report is not.
func healthChange(prev, cur *bool) healthTransition {
	if cur == nil {
		return healthUnchanged
	}
	wasOK := prev == nil || *prev
	switch {
	case wasOK && !*cur:
		return healthFailed
	case !wasOK && *cur:
		return healthRecovered
	default:
		return healthUnchanged
	}
}
