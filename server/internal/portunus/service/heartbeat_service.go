package service

import (
	"context"
	"errors"
	"strings"
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
}

func NewHeartbeatService(hs store.HeartbeatStore, reg *DeviceRegistry) *HeartbeatService {
	return &HeartbeatService{heartbeatStore: hs, registry: reg}
}

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

	return types.HeartbeatResponse{
		OK:         true,
		Known:      known,
		ModuleID:   moduleID,
		ServerTime: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}