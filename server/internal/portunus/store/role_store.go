package store

import (
	"context"
	"errors"
	"time"
)

var ErrRoleIsSystem = errors.New("cannot modify or delete a system role")

// RoleRecord represents a row in the roles table.
type RoleRecord struct {
	RoleID                string
	Name                  string
	Description           string
	IsSystem              bool
	DefaultExpiryDays     *int
	DefaultInactivityDays *int
	CreatedAt             time.Time
}

// RoleStore manages runtime-configurable roles and their permission sets.
type RoleStore interface {
	// CreateRole inserts a new non-system role.
	CreateRole(ctx context.Context, roleID, name, description string, defaultExpiryDays, defaultInactivityDays *int) error

	// GetRole returns the role with the given ID, or ErrNotFound.
	GetRole(ctx context.Context, roleID string) (*RoleRecord, error)

	// ListRoles returns all roles ordered by name.
	ListRoles(ctx context.Context) ([]RoleRecord, error)

	// UpdateRole changes the mutable fields of a non-system role.
	// Returns ErrRoleIsSystem if is_system = 1, ErrNotFound if the role does not exist.
	UpdateRole(ctx context.Context, roleID, name, description string, defaultExpiryDays, defaultInactivityDays *int) error

	// DeleteRole removes a role. Returns ErrRoleIsSystem if is_system = 1,
	// ErrNotFound if the role does not exist.
	DeleteRole(ctx context.Context, roleID string) error

	// SetRolePermissions replaces the full permission set for a role in a
	// single transaction. Passing an empty slice clears all permissions.
	SetRolePermissions(ctx context.Context, roleID string, permissions []string) error

	// GetRolePermissions returns all permission strings assigned to a role,
	// sorted lexicographically. Returns an empty slice for roles with no
	// permissions.
	GetRolePermissions(ctx context.Context, roleID string) ([]string, error)
}
