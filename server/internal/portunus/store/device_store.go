package store

import (
	"context"
	"time"
)

type DeviceRecord struct {
	ModuleID string
	Known    bool
	LastSeen time.Time
}

type DeviceStore interface {
	IsKnown(ctx context.Context, moduleID string) (bool, error)
	MarkSeen(ctx context.Context, moduleID string, known bool, t time.Time) error
	// GetModuleType returns the module_type for a commissioned module.
	// Returns ErrNotFound if the module row does not exist.
	GetModuleType(ctx context.Context, moduleID string) (ModuleType, error)
}
