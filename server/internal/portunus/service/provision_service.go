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

// ProvisionService handles device-initiated member provisioning from the
// PROVISIONING_CONSOLE firmware variant.
//
// Operator resolution (scan-1):
//  1. Hash the raw UID from OperatorCredentialUID.
//  2. Look up the credential in member_access. If not found, create a
//     pending_authorization record so an admin can resolve it via the UI,
//     then return UNAUTHORIZED.
//  3. If found: the member must be active, enabled, and their role must carry
//     the member.provision permission. Anything else returns UNAUTHORIZED and
//     records the attempt in the access event log.
type ProvisionService struct {
	registry             *DeviceRegistry
	memberStore          store.MemberAccessStore
	roleStore            store.RoleStore
	accessEvents         store.AccessEventStore
	credentialHashSecret []byte
}

func NewProvisionService(
	registry *DeviceRegistry,
	memberStore store.MemberAccessStore,
	roleStore store.RoleStore,
	accessEvents store.AccessEventStore,
	credentialHashSecret []byte,
) *ProvisionService {
	return &ProvisionService{
		registry:             registry,
		memberStore:          memberStore,
		roleStore:            roleStore,
		accessEvents:         accessEvents,
		credentialHashSecret: credentialHashSecret,
	}
}

// Provision handles a ProvisionCredentialRequest from a PROVISIONING_CONSOLE module.
// It returns a domain error only for internal failures; all provisioning outcomes
// (duplicate, unauthorized, invalid_role) are encoded in the response Status.
func (s *ProvisionService) Provision(
	ctx context.Context,
	req types.ProvisionCredentialRequest,
) (types.ProvisionCredentialResponse, error) {
	moduleID := strings.TrimSpace(req.ModuleID)
	roleID := strings.TrimSpace(req.RoleID)

	if moduleID == "" {
		return types.ProvisionCredentialResponse{}, ErrInvalidModuleID
	}
	if len(req.CredentialUID) == 0 {
		return types.ProvisionCredentialResponse{}, ErrProvisionCredentialUIDRequired
	}

	// Hash the scan-2 raw UID bytes server-side.
	credHash := HashCredentialID(req.CredentialUID, s.credentialHashSecret)

	// Check module is known.
	known, err := s.registry.IsKnown(ctx, moduleID)
	if err != nil {
		return types.ProvisionCredentialResponse{}, err
	}
	_ = s.registry.NoteSeen(ctx, moduleID, known)

	if !known {
		return types.ProvisionCredentialResponse{OK: false, Known: false}, nil
	}

	// Validate operator (scan-1).
	operatorUUID, resp, err := s.resolveOperator(ctx, req)
	if err != nil {
		return types.ProvisionCredentialResponse{}, err
	}
	if resp != nil {
		return *resp, nil
	}

	// Validate role.
	if roleID == "" {
		return types.ProvisionCredentialResponse{
			OK:     true,
			Known:  true,
			Status: types.ProvisionStatusInvalidRole,
			Detail: "role_id is required",
		}, nil
	}
	if _, err := s.roleStore.GetRole(ctx, roleID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return types.ProvisionCredentialResponse{
				OK:     true,
				Known:  true,
				Status: types.ProvisionStatusInvalidRole,
				Detail: fmt.Sprintf("role %q not found", roleID),
			}, nil
		}
		return types.ProvisionCredentialResponse{}, fmt.Errorf("role lookup: %w", err)
	}

	// Check credential uniqueness before attempting to create the member.
	existing, err := s.memberStore.GetMemberByCredential(ctx, credHash)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return types.ProvisionCredentialResponse{}, fmt.Errorf("credential lookup: %w", err)
	}
	if err == nil {
		dupStatus, dupDetail := provisionDuplicateStatus(existing)
		return types.ProvisionCredentialResponse{
			OK:     true,
			Known:  true,
			Status: dupStatus,
			Detail: dupDetail,
		}, nil
	}

	// Create member and attach credential.
	memberUUID := uuid.New().String()
	if err := s.memberStore.CreateMember(
		ctx, memberUUID, roleID, operatorUUID,
		store.ProvisioningStatusActive, nil, nil,
	); err != nil {
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

	return types.ProvisionCredentialResponse{
		OK:         true,
		Known:      true,
		MemberUUID: memberUUID,
		Status:     types.ProvisionStatusSuccess,
	}, nil
}

// resolveOperator validates the scan-1 operator credential.
// Returns (uuid, nil, nil) on success.
// Returns ("", &unauthorizedResponse, nil) when the operator is not authorized.
// Returns ("", nil, err) on an internal error.
func (s *ProvisionService) resolveOperator(
	ctx context.Context,
	req types.ProvisionCredentialRequest,
) (string, *types.ProvisionCredentialResponse, error) {
	if len(req.OperatorCredentialUID) == 0 {
		return "", unauthorizedResponse("operator credential is required"), nil
	}

	opHash := HashCredentialID(req.OperatorCredentialUID, s.credentialHashSecret)

	member, err := s.memberStore.GetMemberByCredential(ctx, opHash)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return "", nil, fmt.Errorf("operator credential lookup: %w", err)
		}
		// Unknown credential: create a pending_authorization record so an admin
		// can resolve it via the UI, then reject this request.
		if createErr := s.createPendingOperator(ctx, opHash); createErr != nil {
			return "", nil, fmt.Errorf("create pending operator: %w", createErr)
		}
		return "", unauthorizedResponse("operator credential not found — pending authorization created"), nil
	}

	// Member must be active and enabled.
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

// createPendingOperator inserts a pending_authorization member record for an
// unknown scan-1 credential so it surfaces in the admin UI pending queue.
func (s *ProvisionService) createPendingOperator(ctx context.Context, credHash []byte) error {
	memberUUID := uuid.New().String()
	if err := s.memberStore.CreateMember(
		ctx, memberUUID, "guest", "",
		store.ProvisioningStatusPendingAuthorization, nil, nil,
	); err != nil {
		return err
	}
	return s.memberStore.AttachCredential(ctx, memberUUID, credHash)
}

// recordProvisionAttempt writes a denied access event to the audit log.
// Errors are swallowed — the primary response path must not fail because of logging.
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

func unauthorizedResponse(detail string) *types.ProvisionCredentialResponse {
	return &types.ProvisionCredentialResponse{
		OK:     true,
		Known:  true,
		Status: types.ProvisionStatusUnauthorized,
		Detail: detail,
	}
}

// hasPermission returns true if perm is present in the sorted permissions slice.
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
