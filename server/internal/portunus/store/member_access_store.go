package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrMemberCredentialConflict = errors.New("credential already assigned to another member")
	ErrMemberNotPending         = errors.New("member is not pending authorization")
)

// MemberStatus enumerates the lifecycle states of a member_access row.
type MemberStatus string

const (
	MemberStatusActive    MemberStatus = "active"
	MemberStatusSuspended MemberStatus = "suspended"
	MemberStatusExpired   MemberStatus = "expired"
	MemberStatusArchived  MemberStatus = "archived"
)

// ProvisioningStatus enumerates the provisioning workflow states.
type ProvisioningStatus string

const (
	ProvisioningStatusPendingAuthorization ProvisioningStatus = "pending_authorization"
	ProvisioningStatusActive               ProvisioningStatus = "active"
	ProvisioningStatusIncomplete           ProvisioningStatus = "incomplete"
)

// MemberAccessRecord represents a row in the member_access table.
type MemberAccessRecord struct {
	UUID                string
	CredentialHash      []byte // nil until enrolled
	Status              MemberStatus
	Enabled             bool
	ExpiresAt           *time.Time
	InactivityLimitDays *int
	ActivatedAt         *time.Time // set at ApprovePending; nil while pending
	LastAccessAt        *time.Time
	CreatedAt           time.Time
	CreatedByUUID       string
	PromotedFromUUID    string
	ProvisioningStatus  ProvisioningStatus
	ArchivedAt          *time.Time
	ArchivedByUUID      string
}

// MemberAccessStore manages the member_access lifecycle.
type MemberAccessStore interface {
	// CreateMember inserts a new member_access row with the given identity and
	// policy. uuid must be a v4 UUID string. createdByUUID may be empty for
	// bootstrap scenarios.
	CreateMember(ctx context.Context, uuid, createdByUUID string,
		provisioningStatus ProvisioningStatus,
		expiresAt *time.Time, inactivityLimitDays *int) error

	// GetMember returns the member with the given UUID, or ErrNotFound.
	GetMember(ctx context.Context, uuid string) (*MemberAccessRecord, error)

	// GetMemberByCredential returns the member whose credential_hash matches,
	// or ErrNotFound.
	GetMemberByCredential(ctx context.Context, credentialHash []byte) (*MemberAccessRecord, error)

	// ListMembers returns all member_access rows ordered by created_at_ms DESC.
	ListMembers(ctx context.Context) ([]MemberAccessRecord, error)

	// ListPendingAuthorizations returns rows with provisioning_status =
	// 'pending_authorization', ordered by created_at_ms ASC (FIFO queue).
	ListPendingAuthorizations(ctx context.Context) ([]MemberAccessRecord, error)

	// SetStatus updates the lifecycle status of a member.
	SetStatus(ctx context.Context, uuid string, status MemberStatus) error

	// SetEnabled updates the enabled flag.
	SetEnabled(ctx context.Context, uuid string, enabled bool) error

	// AttachCredential sets credential_hash on a member that does not yet have
	// one. Returns ErrMemberCredentialConflict if the hash is already assigned
	// to any other member. Returns ErrNotFound if uuid does not exist.
	AttachCredential(ctx context.Context, uuid string, credentialHash []byte) error

	// SetProvisioningStatus updates provisioning_status.
	SetProvisioningStatus(ctx context.Context, uuid string, status ProvisioningStatus) error

	// UpdateLastAccess records the time of a granted access event.
	UpdateLastAccess(ctx context.Context, uuid string, t time.Time) error

	// ArchiveMember transitions a member to archived status and records who
	// performed the action and when.
	ArchiveMember(ctx context.Context, uuid, archivedByUUID string) error

	// ExpireByHardDeadline sets status = 'expired' for all active records
	// where expires_at_ms is non-null and <= cutoff. Returns the number of rows
	// transitioned.
	ExpireByHardDeadline(ctx context.Context, cutoff time.Time) (int, error)

	// ExpireByInactivity sets status = 'expired' for active records where
	// inactivity_limit_days is non-null and
	// COALESCE(last_access_at_ms, activated_at_ms, created_at_ms) +
	// inactivity_limit_days*86400000 <= now. Returns the number of rows
	// transitioned.
	ExpireByInactivity(ctx context.Context, now time.Time) (int, error)

	// ApprovePending promotes a pending_authorization member to active: sets
	// status and provisioning_status to active, records activated_at_ms and the
	// approver, applies policy fields. Returns ErrNotFound if the member does
	// not exist, ErrMemberNotPending if it is not pending_authorization.
	ApprovePending(ctx context.Context, uuid, approvedByUUID string,
		expiresAt *time.Time, inactivityLimitDays *int) error

	// UpdateMemberPolicy updates the policy fields of a member: expires_at and
	// inactivity_limit_days. Pass nil to clear a field. Returns ErrNotFound if
	// the member does not exist.
	UpdateMemberPolicy(ctx context.Context, uuid string, expiresAt *time.Time, inactivityLimitDays *int) error

	// ArchiveStalePending transitions pending_authorization rows whose
	// created_at_ms + ttlDays*86400000 < now to status='archived'. Returns the
	// number of rows affected.
	ArchiveStalePending(ctx context.Context, now time.Time, ttlDays int) (int64, error)
}
