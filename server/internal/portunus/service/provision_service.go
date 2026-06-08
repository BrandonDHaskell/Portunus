package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

var ErrProvisionCredentialUIDRequired = errors.New("credential_uid is required")

// capturePlaceholderRole is assigned to device-captured pending members (Path 1).
// An admin reassigns it at approval time.
const capturePlaceholderRole = "guest"

// ProvisionService handles device-initiated member provisioning.
//
// Path 1 (capture): PEU scan with no operator badge → creates one
// pending_authorization member with role "guest". An admin approves it later.
//
// Path 2 (operator enrolment): PEU scan-1 (operator) + scan-2 (new card) →
// creates an active member directly. Gated by operatorProvisioningEnabled.
//
// Only PEU (provisioning_enrollment_unit) modules may call Provision. ACU
// (access_control_unit) modules are blocked at the module-type gate.
type ProvisionService struct {
	registry                    *DeviceRegistry
	memberStore                 store.MemberAccessStore
	roleStore                   store.RoleStore
	accessEvents                store.AccessEventStore
	auditStore                  store.AuditStore // may be nil; writes are best-effort
	credentialHashSecret        []byte
	operatorProvisioningEnabled bool
}

func NewProvisionService(
	registry *DeviceRegistry,
	memberStore store.MemberAccessStore,
	roleStore store.RoleStore,
	accessEvents store.AccessEventStore,
	credentialHashSecret []byte,
	operatorProvisioningEnabled bool,
	auditStore store.AuditStore,
) *ProvisionService {
	return &ProvisionService{
		registry:                    registry,
		memberStore:                 memberStore,
		roleStore:                   roleStore,
		accessEvents:                accessEvents,
		auditStore:                  auditStore,
		credentialHashSecret:        credentialHashSecret,
		operatorProvisioningEnabled: operatorProvisioningEnabled,
	}
}

// Provision routes a device provisioning request to the capture or operator-
// enrolment path after validating the module and applying the PEU gate.
func (s *ProvisionService) Provision(
	ctx context.Context,
	req types.ProvisionCredentialRequest,
) (types.ProvisionCredentialResponse, error) {
	moduleID := strings.TrimSpace(req.ModuleID)
	if moduleID == "" {
		return types.ProvisionCredentialResponse{}, ErrInvalidModuleID
	}
	if len(req.CredentialUID) == 0 {
		return types.ProvisionCredentialResponse{}, ErrProvisionCredentialUIDRequired
	}

	credHash := HashCredentialID(req.CredentialUID, s.credentialHashSecret)

	known, err := s.registry.IsKnown(ctx, moduleID)
	if err != nil {
		return types.ProvisionCredentialResponse{}, err
	}
	_ = s.registry.NoteSeen(ctx, moduleID, known)
	if !known {
		return types.ProvisionCredentialResponse{OK: false, Known: false}, nil
	}

	// Gate: only PEU modules may provision.
	mt, err := s.registry.ModuleType(ctx, moduleID)
	if err != nil {
		return types.ProvisionCredentialResponse{}, fmt.Errorf("module type: %w", err)
	}
	if mt != store.ModuleTypePEU {
		s.recordProvisionAttempt(ctx, moduleID, credHash, "provision_blocked:not_peu")
		return types.ProvisionCredentialResponse{
			OK:     true,
			Known:  true,
			Status: types.ProvisionStatusUnauthorized,
			Detail: "module is not a Provisioning & Enrollment Unit (PEU)",
		}, nil
	}

	// Path 1: no operator badge — capture the credential as pending.
	if len(req.OperatorCredentialUID) == 0 {
		return s.capture(ctx, moduleID, credHash)
	}

	// Path 2: operator badge present — gated by deployment flag.
	if !s.operatorProvisioningEnabled {
		s.recordProvisionAttempt(ctx, moduleID, credHash, "provision_blocked:operator_provisioning_disabled")
		return types.ProvisionCredentialResponse{
			OK:     true,
			Known:  true,
			Status: types.ProvisionStatusUnauthorized,
			Detail: "operator provisioning is disabled for this deployment",
		}, nil
	}
	return s.operatorEnroll(ctx, req, credHash)
}

// capture parks a single credential as pending_authorization (Path 1).
func (s *ProvisionService) capture(
	ctx context.Context,
	moduleID string,
	credHash []byte,
) (types.ProvisionCredentialResponse, error) {
	existing, err := s.memberStore.GetMemberByCredential(ctx, credHash)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return types.ProvisionCredentialResponse{}, fmt.Errorf("credential lookup: %w", err)
	}
	if err == nil {
		st, detail := provisionDuplicateStatus(existing)
		return types.ProvisionCredentialResponse{OK: true, Known: true, Status: st, Detail: detail}, nil
	}

	memberUUID := uuid.New().String()
	if err := s.memberStore.CreateMember(ctx, memberUUID, capturePlaceholderRole, "",
		store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		return types.ProvisionCredentialResponse{}, fmt.Errorf("create pending member: %w", err)
	}
	if err := s.memberStore.AttachCredential(ctx, memberUUID, credHash); err != nil {
		if errors.Is(err, store.ErrMemberCredentialConflict) {
			return types.ProvisionCredentialResponse{
				OK:     true,
				Known:  true,
				Status: types.ProvisionStatusDuplicatePending,
				Detail: "credential captured concurrently",
			}, nil
		}
		return types.ProvisionCredentialResponse{}, fmt.Errorf("attach credential: %w", err)
	}

	s.recordAudit(ctx, store.AuditEntry{
		ActorType:    store.ActorTypeSystem,
		Action:       "member.capture",
		ResourceType: "member",
		ResourceID:   memberUUID,
		Details:      fmt.Sprintf(`{"module_id":%q}`, moduleID),
		Result:       "success",
	})
	return types.ProvisionCredentialResponse{
		OK:         true,
		Known:      true,
		MemberUUID: memberUUID,
		Status:     types.ProvisionStatusPendingCreated,
	}, nil
}

// operatorEnroll runs the two-scan enrolment flow (Path 2).
func (s *ProvisionService) operatorEnroll(
	ctx context.Context,
	req types.ProvisionCredentialRequest,
	credHash []byte,
) (types.ProvisionCredentialResponse, error) {
	operatorUUID, blocked, err := s.resolveOperator(ctx, req)
	if err != nil {
		return types.ProvisionCredentialResponse{}, err
	}
	if blocked != nil {
		return *blocked, nil
	}

	roleID := strings.TrimSpace(req.RoleID)
	if roleID == "" {
		return invalidRoleResponse("role_id is required"), nil
	}
	role, err := s.roleStore.GetRole(ctx, roleID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return invalidRoleResponse(fmt.Sprintf("role %q not found", roleID)), nil
		}
		return types.ProvisionCredentialResponse{}, fmt.Errorf("role lookup: %w", err)
	}

	existing, err := s.memberStore.GetMemberByCredential(ctx, credHash)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return types.ProvisionCredentialResponse{}, fmt.Errorf("credential lookup: %w", err)
	}
	if err == nil {
		st, detail := provisionDuplicateStatus(existing)
		return types.ProvisionCredentialResponse{OK: true, Known: true, Status: st, Detail: detail}, nil
	}

	expiresAt, inactivityLimitDays := applyRoleDefaults(role, nil, nil)

	memberUUID := uuid.New().String()
	if err := s.memberStore.CreateMember(ctx, memberUUID, roleID, operatorUUID,
		store.ProvisioningStatusActive, expiresAt, inactivityLimitDays); err != nil {
		return types.ProvisionCredentialResponse{}, fmt.Errorf("create member: %w", err)
	}
	if err := s.memberStore.AttachCredential(ctx, memberUUID, credHash); err != nil {
		if errors.Is(err, store.ErrMemberCredentialConflict) {
			return types.ProvisionCredentialResponse{
				OK:     true,
				Known:  true,
				Status: types.ProvisionStatusDuplicateActive,
				Detail: "credential assigned between check and insert",
			}, nil
		}
		return types.ProvisionCredentialResponse{}, fmt.Errorf("attach credential: %w", err)
	}

	s.recordAudit(ctx, store.AuditEntry{
		ActorUUID:    operatorUUID,
		ActorType:    store.ActorTypeMember,
		Action:       "member.provision.enroll",
		ResourceType: "member",
		ResourceID:   memberUUID,
		Details:      fmt.Sprintf(`{"module_id":%q,"role_id":%q}`, req.ModuleID, roleID),
		Result:       "success",
	})
	return types.ProvisionCredentialResponse{
		OK:         true,
		Known:      true,
		MemberUUID: memberUUID,
		Status:     types.ProvisionStatusSuccess,
	}, nil
}

// resolveOperator validates the scan-1 operator credential.
// Returns (uuid, nil, nil) on success.
// Returns ("", &blockedResponse, nil) when the operator is not authorized.
// Returns ("", nil, err) on an internal error.
// Unknown operators are hard-blocked with no record created.
func (s *ProvisionService) resolveOperator(
	ctx context.Context,
	req types.ProvisionCredentialRequest,
) (string, *types.ProvisionCredentialResponse, error) {
	opHash := HashCredentialID(req.OperatorCredentialUID, s.credentialHashSecret)

	member, err := s.memberStore.GetMemberByCredential(ctx, opHash)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return "", nil, fmt.Errorf("operator credential lookup: %w", err)
		}
		s.recordProvisionAttempt(ctx, req.ModuleID, opHash, "provision_unauthorized:operator_not_found")
		return "", unauthorizedResponse("operator credential not recognized"), nil
	}

	// Operator must be active and enabled.
	if !member.Enabled || member.Status != store.MemberStatusActive ||
		member.ProvisioningStatus != store.ProvisioningStatusActive {
		s.recordProvisionAttempt(ctx, req.ModuleID, opHash, "provision_unauthorized:operator_inactive")
		return "", unauthorizedResponse("operator account is not active"), nil
	}

	// Role must carry member.provision.
	perms, err := s.roleStore.GetRolePermissions(ctx, member.RoleID)
	if err != nil {
		return "", nil, fmt.Errorf("get operator role permissions: %w", err)
	}
	if !hasPermission(perms, permissions.MemberProvision) {
		s.recordProvisionAttempt(ctx, req.ModuleID, opHash, "provision_unauthorized:no_member_provision_permission")
		return "", unauthorizedResponse("operator does not have member.provision permission"), nil
	}

	return member.UUID, nil, nil
}

// recordProvisionAttempt writes a denied access event. Errors are swallowed.
func (s *ProvisionService) recordProvisionAttempt(ctx context.Context, moduleID string, credHash []byte, reason string) {
	now := time.Now().UTC()
	_ = s.accessEvents.RecordEvent(ctx, store.AccessEventRecord{
		ModuleID:       moduleID,
		ReceivedAt:     now,
		CredentialHash: credHash,
		Granted:        false,
		Reason:         reason,
		DecidedAt:      now,
	})
}

// recordAudit writes an audit entry. Errors are swallowed — the primary
// operation must not fail because of audit logging.
func (s *ProvisionService) recordAudit(ctx context.Context, e store.AuditEntry) {
	if s.auditStore == nil {
		return
	}
	if err := s.auditStore.RecordAuditEntry(ctx, e); err != nil {
		// Intentionally not fatal; logged by the store itself if needed.
		_ = err
	}
}

func unauthorizedResponse(detail string) *types.ProvisionCredentialResponse {
	return &types.ProvisionCredentialResponse{
		OK:     true,
		Known:  true,
		Status: types.ProvisionStatusUnauthorized,
		Detail: detail,
	}
}

func invalidRoleResponse(detail string) types.ProvisionCredentialResponse {
	return types.ProvisionCredentialResponse{
		OK:     true,
		Known:  true,
		Status: types.ProvisionStatusInvalidRole,
		Detail: detail,
	}
}

// hasPermission returns true if perm is present in the permissions slice.
func hasPermission(perms []string, perm string) bool {
	for _, p := range perms {
		if p == perm {
			return true
		}
	}
	return false
}

// provisionDuplicateStatus maps an existing member record to the appropriate
// ProvisionStatus and a human-readable detail string.
func provisionDuplicateStatus(m *store.MemberAccessRecord) (types.ProvisionStatus, string) {
	switch m.Status {
	case store.MemberStatusActive:
		if m.ProvisioningStatus == store.ProvisioningStatusPendingAuthorization {
			return types.ProvisionStatusDuplicatePending, "credential already attached to a pending member"
		}
		return types.ProvisionStatusDuplicateActive, "credential already assigned to an active member"
	case store.MemberStatusExpired, store.MemberStatusArchived:
		return types.ProvisionStatusDuplicateInactive, "credential already assigned to an expired or archived member"
	default:
		return types.ProvisionStatusDuplicateActive, "credential already assigned to another member"
	}
}
