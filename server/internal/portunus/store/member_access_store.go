package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrMemberCredentialConflict = errors.New("credential already assigned to another member")
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
	RoleID              string
	CredentialHash      []byte // nil until enrolled
	Status              MemberStatus
	Enabled             bool
	ExpiresAt           *time.Time
	InactivityLimitDays *int
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
	CreateMember(ctx context.Context, uuid, roleID, createdByUUID string,
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

	// AssignRole changes the role for an existing member.
	AssignRole(ctx context.Context, uuid, roleID string) error

	// UpdateLastAccess records the time of a granted access event.
	UpdateLastAccess(ctx context.Context, uuid string, t time.Time) error

	// ArchiveMember transitions a member to archived status and records who
	// performed the action and when.
	ArchiveMember(ctx context.Context, uuid, archivedByUUID string) error
}
