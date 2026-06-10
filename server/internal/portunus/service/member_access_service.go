package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

var (
	ErrMemberNotFound              = errors.New("member not found")
	ErrMemberUUIDRequired          = errors.New("member_uuid is required")
	ErrInactivityLimitRequired     = errors.New("inactivity_limit_days is required when approving a member")
	ErrCredentialHashRequired      = errors.New("credential_hash is required")
	ErrDuplicateCredentialActive   = errors.New("credential already assigned to an active member")
	ErrDuplicateCredentialPending  = errors.New("credential already attached to a pending_authorization member")
	ErrDuplicateCredentialInactive = errors.New("credential already assigned to an expired or archived member")
	ErrMemberNotPending            = errors.New("member is not pending authorization")
)

type MemberAccessService struct {
	memberStore store.MemberAccessStore
}

func NewMemberAccessService(ms store.MemberAccessStore) *MemberAccessService {
	return &MemberAccessService{memberStore: ms}
}

// ProvisionMember creates a new member_access record with a fresh v4 UUID.
// createdByUUID may be empty for system-initiated provisioning.
func (s *MemberAccessService) ProvisionMember(
	ctx context.Context,
	createdByUUID string,
	expiresAt *time.Time,
	inactivityLimitDays *int,
) (*store.MemberAccessRecord, error) {
	memberUUID := uuid.New().String()

	if err := s.memberStore.CreateMember(
		ctx, memberUUID, createdByUUID,
		store.ProvisioningStatusPendingAuthorization,
		expiresAt, inactivityLimitDays,
	); err != nil {
		return nil, fmt.Errorf("create member: %w", err)
	}

	rec, err := s.memberStore.GetMember(ctx, memberUUID)
	if err != nil {
		return nil, fmt.Errorf("get member after provision: %w", err)
	}
	return rec, nil
}

// AttachCredential assigns a credential hash to a member.
// Returns a specific error indicating why a duplicate is rejected.
func (s *MemberAccessService) AttachCredential(ctx context.Context, memberUUID string, credentialHash []byte) error {
	memberUUID = strings.TrimSpace(memberUUID)
	if memberUUID == "" {
		return ErrMemberUUIDRequired
	}
	if len(credentialHash) == 0 {
		return ErrCredentialHashRequired
	}

	// Pre-check: distinguish the type of duplicate before hitting the UNIQUE
	// constraint so callers get an actionable error.
	if err := s.checkCredentialUniqueness(ctx, credentialHash); err != nil {
		return err
	}

	if err := s.memberStore.AttachCredential(ctx, memberUUID, credentialHash); err != nil {
		if errors.Is(err, store.ErrMemberCredentialConflict) {
			// Raced with another request between check and insert — still a conflict.
			return ErrDuplicateCredentialActive
		}
		if errors.Is(err, store.ErrNotFound) {
			return ErrMemberNotFound
		}
		return fmt.Errorf("attach credential: %w", err)
	}
	return nil
}

// checkCredentialUniqueness performs a diagnostic lookup and returns a
// typed error that distinguishes active / pending / inactive duplicates.
func (s *MemberAccessService) checkCredentialUniqueness(ctx context.Context, credentialHash []byte) error {
	existing, err := s.memberStore.GetMemberByCredential(ctx, credentialHash)
	if errors.Is(err, store.ErrNotFound) {
		return nil // hash not yet assigned — safe to proceed
	}
	if err != nil {
		return fmt.Errorf("credential uniqueness check: %w", err)
	}
	switch existing.Status {
	case store.MemberStatusActive:
		if existing.ProvisioningStatus == store.ProvisioningStatusPendingAuthorization {
			return ErrDuplicateCredentialPending
		}
		return ErrDuplicateCredentialActive
	case store.MemberStatusExpired, store.MemberStatusArchived:
		return ErrDuplicateCredentialInactive
	default:
		return ErrDuplicateCredentialActive
	}
}

// Disable sets enabled = false on the member.
func (s *MemberAccessService) Disable(ctx context.Context, memberUUID string) error {
	if strings.TrimSpace(memberUUID) == "" {
		return ErrMemberUUIDRequired
	}
	if err := s.memberStore.SetEnabled(ctx, memberUUID, false); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrMemberNotFound
		}
		return fmt.Errorf("disable member: %w", err)
	}
	return nil
}

// Enable sets enabled = true on the member.
func (s *MemberAccessService) Enable(ctx context.Context, memberUUID string) error {
	if strings.TrimSpace(memberUUID) == "" {
		return ErrMemberUUIDRequired
	}
	if err := s.memberStore.SetEnabled(ctx, memberUUID, true); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrMemberNotFound
		}
		return fmt.Errorf("enable member: %w", err)
	}
	return nil
}

// Archive transitions a member to archived status.
func (s *MemberAccessService) Archive(ctx context.Context, memberUUID, archivedByUUID string) error {
	if strings.TrimSpace(memberUUID) == "" {
		return ErrMemberUUIDRequired
	}
	if err := s.memberStore.ArchiveMember(ctx, memberUUID, archivedByUUID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrMemberNotFound
		}
		return fmt.Errorf("archive member: %w", err)
	}
	return nil
}

// SetProvisioningStatus updates the provisioning workflow state.
func (s *MemberAccessService) SetProvisioningStatus(ctx context.Context, memberUUID string, status store.ProvisioningStatus) error {
	if strings.TrimSpace(memberUUID) == "" {
		return ErrMemberUUIDRequired
	}
	if err := s.memberStore.SetProvisioningStatus(ctx, memberUUID, status); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrMemberNotFound
		}
		return fmt.Errorf("set provisioning status: %w", err)
	}
	return nil
}

// GetMember returns a member by UUID.
func (s *MemberAccessService) GetMember(ctx context.Context, memberUUID string) (*store.MemberAccessRecord, error) {
	rec, err := s.memberStore.GetMember(ctx, strings.TrimSpace(memberUUID))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrMemberNotFound
	}
	return rec, err
}

// ListMembers returns all members ordered by created_at_ms DESC.
func (s *MemberAccessService) ListMembers(ctx context.Context) ([]store.MemberAccessRecord, error) {
	return s.memberStore.ListMembers(ctx)
}

// ListPendingAuthorizations returns members with provisioning_status = 'pending_authorization'.
func (s *MemberAccessService) ListPendingAuthorizations(ctx context.Context) ([]store.MemberAccessRecord, error) {
	return s.memberStore.ListPendingAuthorizations(ctx)
}

// ApprovePending activates a pending_authorization member.
// inactivityLimitDays is required — the admin must explicitly choose a window.
// expiresAt is optional; nil means no hard deadline.
// The approver UUID comes from the authenticated admin session, never the client.
func (s *MemberAccessService) ApprovePending(
	ctx context.Context,
	memberUUID, approvedByUUID string,
	expiresAt *time.Time,
	inactivityLimitDays *int,
) error {
	memberUUID = strings.TrimSpace(memberUUID)
	if memberUUID == "" {
		return ErrMemberUUIDRequired
	}
	if inactivityLimitDays == nil {
		return ErrInactivityLimitRequired
	}

	if err := s.memberStore.ApprovePending(ctx, memberUUID, approvedByUUID, expiresAt, inactivityLimitDays); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrMemberNotFound
		}
		if errors.Is(err, store.ErrMemberNotPending) {
			return ErrMemberNotPending
		}
		return fmt.Errorf("approve pending: %w", err)
	}
	return nil
}
