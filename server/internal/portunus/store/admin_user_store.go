package store

import (
	"context"
	"errors"
	"time"
)

var ErrUsernameAlreadyExists = errors.New("username already exists")

// AdminUserRecord represents a row in the admin_users table.
type AdminUserRecord struct {
	UUID         string
	Username     string
	PasswordHash string
	RoleID       string
	Enabled      bool
	MustChangePW bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastLoginAt  *time.Time
}

// AdminUserStore manages server operator accounts.
type AdminUserStore interface {
	// CreateAdminUser inserts a new admin user with the given role.
	// Returns ErrUsernameAlreadyExists if the username is taken.
	CreateAdminUser(ctx context.Context, uuid, username, passwordHash, roleID string) error

	// GetAdminUserByUsername returns the user with the given username, or ErrNotFound.
	GetAdminUserByUsername(ctx context.Context, username string) (*AdminUserRecord, error)

	// GetAdminUserByUUID returns the user with the given UUID, or ErrNotFound.
	GetAdminUserByUUID(ctx context.Context, uuid string) (*AdminUserRecord, error)

	// ListAdminUsers returns all admin users ordered by username.
	ListAdminUsers(ctx context.Context) ([]AdminUserRecord, error)

	// SetMustChangePW updates the must_change_pw flag.
	SetMustChangePW(ctx context.Context, uuid string, mustChange bool) error

	// UpdatePasswordHash replaces the password_hash and clears must_change_pw.
	UpdatePasswordHash(ctx context.Context, uuid, passwordHash string) error

	// UpdateLastLogin sets last_login_at_ms to the provided time.
	UpdateLastLogin(ctx context.Context, uuid string, t time.Time) error

	// SetAdminUserEnabled enables or disables an admin account.
	// Disabled accounts cannot log in.
	SetAdminUserEnabled(ctx context.Context, uuid string, enabled bool) error

	// SetAdminUserRole assigns a role to an admin user.
	SetAdminUserRole(ctx context.Context, uuid, roleID string) error

	// AnyAdminExists returns true if at least one admin_user row exists.
	AnyAdminExists(ctx context.Context) (bool, error)
}
