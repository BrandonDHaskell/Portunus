package service

import (
	"context"
	"strings"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type DeviceRegistry struct {
	store store.DeviceStore
}

func NewDeviceRegistry(st store.DeviceStore) *DeviceRegistry {
	return &DeviceRegistry{store: st}
}

func (r *DeviceRegistry) IsKnown(ctx context.Context, moduleID string) (bool, error) {
	moduleID = strings.TrimSpace(moduleID)
	if moduleID == "" {
		return false, nil
	}
	return r.store.IsKnown(ctx, moduleID)
}

func (r *DeviceRegistry) NoteSeen(ctx context.Context, moduleID string, known bool) error {
	moduleID = strings.TrimSpace(moduleID)
	if moduleID == "" {
		return nil
	}
	return r.store.MarkSeen(ctx, moduleID, known, time.Now().UTC())
}
