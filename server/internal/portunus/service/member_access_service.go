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

// memberAuditReasons maps member errors to the reason recorded on a failed
// audit entry.
var memberAuditReasons = []auditReason{
	{ErrMemberUUIDRequired, "member_uuid_required"},
	{ErrMemberNotFound, "member_not_found"},
	{ErrMemberNotPending, "not_pending"},
	{ErrInactivityLimitRequired, "inactivity_limit_required"},
	{ErrCredentialHashRequired, "credential_required"},
	{ErrDuplicateCredentialActive, "duplicate_credential_active"},
	{ErrDuplicateCredentialPending, "duplicate_credential_pending"},
	{ErrDuplicateCredentialInactive, "duplicate_credential_inactive"},
}

// MemberAccessService manages member records. Every state change is written
// to the audit log with the acting admin, whichever transport invoked it.
type MemberAccessService struct {
	memberStore store.MemberAccessStore
	auditStore  store.AuditStore // nil disables auditing (tests only)
}

func NewMemberAccessService(ms store.MemberAccessStore, audit store.AuditStore) *MemberAccessService {
	return &MemberAccessService{memberStore: ms, auditStore: audit}
}

// audit records a member action and its outcome.
func (s *MemberAccessService) audit(ctx context.Context, actorUUID, action, memberUUID string, err error, details map[string]any) {
	result, reason := resultOf(err, memberAuditReasons...)
	if reason != "" {
		if details == nil {
			details = map[string]any{}
		}
		details["reason"] = reason
	}
	writeAudit(ctx, s.auditStore, store.AuditEntry{
		ActorUUID:    actorUUID,
		Action:       action,
		ResourceType: "member",
		ResourceID:   strings.TrimSpace(memberUUID),
		Result:       result,
		Details:      auditDetails(details),
	})
}

func policyDetails(expiresAt *time.Time, inactivityLimitDays *int) map[string]any {
	d := map[string]any{"expires_at": nil, "inactivity_limit_days": nil}
	if expiresAt != nil {
		d["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	}
	if inactivityLimitDays != nil {
		d["inactivity_limit_days"] = *inactivityLimitDays
	}
	return d
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

	err := s.memberStore.CreateMember(
		ctx, memberUUID, createdByUUID,
		store.ProvisioningStatusPendingAuthorization,
		expiresAt, inactivityLimitDays,
	)
	s.audit(ctx, createdByUUID, "member.create", memberUUID, err, policyDetails(expiresAt, inactivityLimitDays))
	if err != nil {
		return nil, fmt.Errorf("create member: %w", err)
	}

	rec, err := s.memberStore.GetMember(ctx, memberUUID)
	if err != nil {
		return nil, fmt.Errorf("get member after provision: %w", err)
	}
	return rec, nil
}

// AttachCredential assigns a credential hash to a member. Returns a specific
// error indicating why a duplicate is rejected. The hash itself is never
// written to the audit log.
func (s *MemberAccessService) AttachCredential(ctx context.Context, memberUUID string, credentialHash []byte, actorUUID string) error {
	err := s.attachCredential(ctx, memberUUID, credentialHash)
	s.audit(ctx, actorUUID, "member.credential_attach", memberUUID, err, nil)
	return err
}

func (s *MemberAccessService) attachCredential(ctx context.Context, memberUUID string, credentialHash []byte) error {
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
func (s *MemberAccessService) Disable(ctx context.Context, memberUUID, actorUUID string) error {
	err := s.setEnabled(ctx, memberUUID, false)
	s.audit(ctx, actorUUID, "member.disable", memberUUID, err, nil)
	return err
}

// Enable sets enabled = true on the member.
func (s *MemberAccessService) Enable(ctx context.Context, memberUUID, actorUUID string) error {
	err := s.setEnabled(ctx, memberUUID, true)
	s.audit(ctx, actorUUID, "member.enable", memberUUID, err, nil)
	return err
}

func (s *MemberAccessService) setEnabled(ctx context.Context, memberUUID string, enabled bool) error {
	if strings.TrimSpace(memberUUID) == "" {
		return ErrMemberUUIDRequired
	}
	if err := s.memberStore.SetEnabled(ctx, memberUUID, enabled); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrMemberNotFound
		}
		return fmt.Errorf("set member enabled=%v: %w", enabled, err)
	}
	return nil
}

// Archive transitions a member to archived status.
func (s *MemberAccessService) Archive(ctx context.Context, memberUUID, archivedByUUID string) error {
	err := s.archive(ctx, memberUUID, archivedByUUID)
	s.audit(ctx, archivedByUUID, "member.archive", memberUUID, err, nil)
	return err
}

func (s *MemberAccessService) archive(ctx context.Context, memberUUID, archivedByUUID string) error {
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

// ListPendingAuthorizations returns live captures awaiting approval.
func (s *MemberAccessService) ListPendingAuthorizations(ctx context.Context) ([]store.MemberAccessRecord, error) {
	return s.memberStore.ListPendingAuthorizations(ctx)
}

// UpdateMemberPolicy updates expires_at and inactivity_limit_days. Pass nil
// to clear a field. Editing an archived member's policy is allowed but has no
// effect at the door: archived members are always denied.
func (s *MemberAccessService) UpdateMemberPolicy(ctx context.Context, memberUUID string, expiresAt *time.Time, inactivityLimitDays *int, actorUUID string) error {
	err := s.updateMemberPolicy(ctx, memberUUID, expiresAt, inactivityLimitDays)
	s.audit(ctx, actorUUID, "member.policy_update", memberUUID, err, policyDetails(expiresAt, inactivityLimitDays))
	return err
}

func (s *MemberAccessService) updateMemberPolicy(ctx context.Context, memberUUID string, expiresAt *time.Time, inactivityLimitDays *int) error {
	if strings.TrimSpace(memberUUID) == "" {
		return ErrMemberUUIDRequired
	}
	if err := s.memberStore.UpdateMemberPolicy(ctx, memberUUID, expiresAt, inactivityLimitDays); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrMemberNotFound
		}
		return fmt.Errorf("update member policy: %w", err)
	}
	return nil
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
	err := s.approvePending(ctx, memberUUID, approvedByUUID, expiresAt, inactivityLimitDays)
	s.audit(ctx, approvedByUUID, "member.approve", memberUUID, err, policyDetails(expiresAt, inactivityLimitDays))
	return err
}

func (s *MemberAccessService) approvePending(
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
