package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

var ErrProvisionCredentialHashRequired = errors.New("credential_hash is required")

// ProvisionService handles device-initiated member provisioning from the
// PROVISIONING_CONSOLE firmware variant.  It creates a fully active member
// record in a single flow — no pending_authorization step.
type ProvisionService struct {
	registry    *DeviceRegistry
	memberStore store.MemberAccessStore
	roleStore   store.RoleStore
	adminUsers  store.AdminUserStore
}

func NewProvisionService(
	registry *DeviceRegistry,
	memberStore store.MemberAccessStore,
	roleStore store.RoleStore,
	adminUsers store.AdminUserStore,
) *ProvisionService {
	return &ProvisionService{
		registry:    registry,
		memberStore: memberStore,
		roleStore:   roleStore,
		adminUsers:  adminUsers,
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
	operatorUUID := strings.TrimSpace(req.OperatorUUID)
	roleID := strings.TrimSpace(req.RoleID)

	if moduleID == "" {
		return types.ProvisionCredentialResponse{}, ErrInvalidModuleID
	}
	if len(req.CredentialHash) == 0 {
		return types.ProvisionCredentialResponse{}, ErrProvisionCredentialHashRequired
	}

	// Check module is known.
	known, err := s.registry.IsKnown(ctx, moduleID)
	if err != nil {
		return types.ProvisionCredentialResponse{}, err
	}
	_ = s.registry.NoteSeen(ctx, moduleID, known)

	if !known {
		return types.ProvisionCredentialResponse{OK: false, Known: false}, nil
	}

	// Verify operator: must be an enabled admin user.
	if operatorUUID == "" {
		return types.ProvisionCredentialResponse{
			OK:     true,
			Known:  true,
			Status: types.ProvisionStatusUnauthorized,
			Detail: "operator_uuid is required",
		}, nil
	}
	operator, err := s.adminUsers.GetAdminUserByUUID(ctx, operatorUUID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return types.ProvisionCredentialResponse{
				OK:     true,
				Known:  true,
				Status: types.ProvisionStatusUnauthorized,
				Detail: "operator not found",
			}, nil
		}
		return types.ProvisionCredentialResponse{}, fmt.Errorf("operator lookup: %w", err)
	}
	if !operator.Enabled {
		return types.ProvisionCredentialResponse{
			OK:     true,
			Known:  true,
			Status: types.ProvisionStatusUnauthorized,
			Detail: "operator account is disabled",
		}, nil
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
	existing, err := s.memberStore.GetMemberByCredential(ctx, req.CredentialHash)
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

	// Create member and attach credential. Using ProvisioningStatusActive skips
	// the pending_authorization workflow used in admin-initiated flows.
	memberUUID := uuid.New().String()
	if err := s.memberStore.CreateMember(
		ctx, memberUUID, roleID, operatorUUID,
		store.ProvisioningStatusActive, nil, nil,
	); err != nil {
		return types.ProvisionCredentialResponse{}, fmt.Errorf("create member: %w", err)
	}
	if err := s.memberStore.AttachCredential(ctx, memberUUID, req.CredentialHash); err != nil {
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
