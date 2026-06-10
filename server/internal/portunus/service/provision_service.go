package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

var ErrProvisionCredentialUIDRequired = errors.New("credential_uid is required")

// ProvisionService handles device-initiated member provisioning.
//
// Capture path: a PEU scan creates one pending_authorization member.
// An admin approves it later via the console.
//
// Only PEU (provisioning_enrollment_unit) modules may call Provision. ACU
// (access_control_unit) modules are blocked at the module-type gate.
type ProvisionService struct {
	registry             *DeviceRegistry
	memberStore          store.MemberAccessStore
	accessEvents         store.AccessEventStore
	auditStore           store.AuditStore // may be nil; writes are best-effort
	credentialHashSecret []byte
}

func NewProvisionService(
	registry *DeviceRegistry,
	memberStore store.MemberAccessStore,
	accessEvents store.AccessEventStore,
	credentialHashSecret []byte,
	auditStore store.AuditStore,
) *ProvisionService {
	return &ProvisionService{
		registry:             registry,
		memberStore:          memberStore,
		accessEvents:         accessEvents,
		auditStore:           auditStore,
		credentialHashSecret: credentialHashSecret,
	}
}

// Provision validates the module and routes every valid PEU request to capture.
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

	return s.capture(ctx, moduleID, credHash)
}

// capture parks a single credential as pending_authorization.
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
	if err := s.memberStore.CreateMember(ctx, memberUUID, "",
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
		_ = err
	}
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
