package store

import (
	"context"
	"errors"
	"time"
)

var ErrAuthorizationAlreadyExists = errors.New("authorization already exists for this member and module")

// ModuleAuthorizationRecord represents a row in the module_authorizations table.
type ModuleAuthorizationRecord struct {
	AuthorizationID int64
	MemberUUID      string
	ModuleID        string
	GrantedAt       time.Time
	GrantedByUUID   string
	ExpiresAt       *time.Time
	RevokedAt       *time.Time
	RevokedByUUID   string
	TimeRestriction string // JSON; empty string means no restriction
}

// ModuleAuthorizationStore manages per-module access grants.
// Default-deny: no row means no access regardless of member status.
type ModuleAuthorizationStore interface {
	// GrantAuthorization creates a new authorization. Returns
	// ErrAuthorizationAlreadyExists if an active (non-revoked) authorization
	// already exists for the (memberUUID, moduleID) pair.
	// grantedByUUID may be empty for automated grants.
	// timeRestriction is an optional JSON policy string; pass "" for none.
	GrantAuthorization(ctx context.Context, memberUUID, moduleID, grantedByUUID string,
		expiresAt *time.Time, timeRestriction string) error

	// RevokeAuthorization soft-deletes the authorization by setting
	// revoked_at_ms. Returns ErrNotFound if no active authorization exists.
	RevokeAuthorization(ctx context.Context, memberUUID, moduleID, revokedByUUID string) error

	// GetAuthorization returns the most recent authorization for the
	// (memberUUID, moduleID) pair, or ErrNotFound.
	GetAuthorization(ctx context.Context, memberUUID, moduleID string) (*ModuleAuthorizationRecord, error)

	// ListByMember returns all authorizations for a member, ordered by
	// granted_at_ms DESC.
	ListByMember(ctx context.Context, memberUUID string) ([]ModuleAuthorizationRecord, error)

	// ListByModule returns all authorizations for a module, ordered by
	// granted_at_ms DESC.
	ListByModule(ctx context.Context, moduleID string) ([]ModuleAuthorizationRecord, error)
}
