package store

import (
	"context"
	"time"
)

type DeviceRecord struct {
	ModuleID  string
	Known     bool
	LastSeen  time.Time
}

type DeviceStore interface {
	IsKnown(ctx context.Context, moduleID string) (bool, error)
	MarkSeen(ctx context.Context, moduleID string, known bool, t time.Time) error
}
