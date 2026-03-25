package store

import (
	"context"
	"time"
)

// ModuleRecord represents a full row from the modules table.
type ModuleRecord struct {
	ModuleID       string
	DoorID         string
	DisplayName    string
	Enabled        bool
	CommissionedAt *time.Time
	RevokedAt      *time.Time
	LastSeenAt     *time.Time
	LastIP         string
	LastFWVersion  string
	LastWiFiRSSI   *int
	CreatedAt      time.Time
}

// DoorRecord represents a row in the doors table.
type DoorRecord struct {
	DoorID    string
	Name      string
	Location  string
	CreatedAt time.Time
}

// ModuleAdminStore extends DeviceStore with admin CRUD operations.
type ModuleAdminStore interface {
	// CommissionModule registers a module as enabled and commissioned.
	// If the module row already exists (from an auto-created ensureModule),
	// it is promoted to commissioned. doorID may be empty.
	CommissionModule(ctx context.Context, moduleID, doorID, displayName string) error

	// RevokeModule marks a module as revoked (sets revoked_at_ms, enabled=0).
	RevokeModule(ctx context.Context, moduleID string) error

	// DeleteModule removes a module row entirely.
	DeleteModule(ctx context.Context, moduleID string) error

	// GetModule returns a single module by ID. Returns nil if not found.
	GetModule(ctx context.Context, moduleID string) (*ModuleRecord, error)

	// ListModules returns all modules.
	ListModules(ctx context.Context) ([]ModuleRecord, error)

	// RegisterDoor creates a door entry.
	RegisterDoor(ctx context.Context, doorID, name, location string) error

	// ListDoors returns all doors.
	ListDoors(ctx context.Context) ([]DoorRecord, error)

	// DeleteDoor removes a door entry.
	DeleteDoor(ctx context.Context, doorID string) error
}
