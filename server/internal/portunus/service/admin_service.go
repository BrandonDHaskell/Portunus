package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// ParseCredentialUID parses a colon-separated uppercase hex UID string
// (e.g. "04:A3:2B:1C") into raw bytes.  This is the format the access module
// puts in AccessRequest.credential_id via credential_uid_to_hex().
func ParseCredentialUID(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty credential UID")
	}
	parts := strings.Split(s, ":")
	if len(parts) < 1 || len(parts) > 10 {
		return nil, fmt.Errorf("credential UID must be 1–10 colon-separated bytes, got %d", len(parts))
	}
	uid := make([]byte, len(parts))
	for i, p := range parts {
		b, err := hex.DecodeString(p)
		if err != nil || len(b) != 1 {
			return nil, fmt.Errorf("invalid byte at position %d: %q", i, p)
		}
		uid[i] = b[0]
	}
	return uid, nil
}

var (
	ErrModuleIDRequired   = errors.New("module_id is required")
	ErrModuleDoorRequired = errors.New("a door must be assigned to commission a module")
	ErrDoorIDRequired     = errors.New("door_id is required")
	ErrDoorNameRequired   = errors.New("door name is required")
	ErrModuleNotFound     = errors.New("module not found")
	ErrDoorNotFound       = errors.New("door not found")
	ErrInvalidModuleType  = errors.New("module_type must be 'access_control_unit' or 'provisioning_enrollment_unit'")
)

// AdminService manages modules and doors. Every change is written to the
// audit log with the acting admin, whichever transport invoked it.
type AdminService struct {
	moduleStore          store.ModuleAdminStore
	credentialHashSecret []byte
	auditStore           store.AuditStore // nil disables auditing (tests only)
}

func NewAdminService(ms store.ModuleAdminStore, credentialHashSecret []byte, audit store.AuditStore) *AdminService {
	return &AdminService{moduleStore: ms, credentialHashSecret: credentialHashSecret, auditStore: audit}
}

var adminAuditReasons = []auditReason{
	{ErrModuleIDRequired, "module_id_required"},
	{ErrModuleDoorRequired, "door_required"},
	{ErrInvalidModuleType, "invalid_module_type"},
	{ErrModuleNotFound, "module_not_found"},
	{ErrDoorIDRequired, "door_id_required"},
	{ErrDoorNameRequired, "door_name_required"},
	{ErrDoorNotFound, "door_not_found"},
}

func (s *AdminService) audit(ctx context.Context, actorUUID, action, resourceType, resourceID string, err error, details map[string]any) {
	result, reason := resultOf(err, adminAuditReasons...)
	if reason != "" {
		if details == nil {
			details = map[string]any{}
		}
		details["reason"] = reason
	}
	writeAudit(ctx, s.auditStore, store.AuditEntry{
		ActorUUID:    actorUUID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   strings.TrimSpace(resourceID),
		Result:       result,
		Details:      auditDetails(details),
	})
}

// ── Modules ─────────────────────────────────────────────────────────────────

// RegisterModule commissions a module onto a door.
func (s *AdminService) RegisterModule(ctx context.Context, req types.RegisterModuleRequest, actorUUID string) (*types.ModuleInfo, error) {
	info, err := s.registerModule(ctx, req)
	s.audit(ctx, actorUUID, "module.register", "module", req.ModuleID, err,
		map[string]any{"door_id": strings.TrimSpace(req.DoorID), "module_type": strings.TrimSpace(req.ModuleType)})
	return info, err
}

func (s *AdminService) registerModule(ctx context.Context, req types.RegisterModuleRequest) (*types.ModuleInfo, error) {
	moduleID := strings.TrimSpace(req.ModuleID)
	if moduleID == "" {
		return nil, ErrModuleIDRequired
	}

	doorID := strings.TrimSpace(req.DoorID)
	if doorID == "" {
		return nil, ErrModuleDoorRequired
	}

	moduleType := store.ModuleType(strings.TrimSpace(req.ModuleType))
	if moduleType == "" {
		moduleType = store.ModuleTypeACU
	}
	if moduleType != store.ModuleTypeACU && moduleType != store.ModuleTypePEU {
		return nil, ErrInvalidModuleType
	}

	if _, err := s.moduleStore.GetDoor(ctx, doorID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrDoorNotFound
		}
		return nil, fmt.Errorf("commission module: verify door: %w", err)
	}

	if err := s.moduleStore.CommissionModule(ctx, moduleID, doorID, req.DisplayName, moduleType); err != nil {
		return nil, fmt.Errorf("commission module: %w", err)
	}

	rec, err := s.moduleStore.GetModule(ctx, moduleID)
	if err != nil {
		return nil, fmt.Errorf("get module after commission: %w", err)
	}

	return moduleRecordToInfo(rec), nil
}

func (s *AdminService) ListModules(ctx context.Context) ([]types.ModuleInfo, error) {
	recs, err := s.moduleStore.ListModules(ctx)
	if err != nil {
		return nil, err
	}

	infos := make([]types.ModuleInfo, len(recs))
	for i := range recs {
		infos[i] = *moduleRecordToInfo(&recs[i])
	}
	return infos, nil
}

func (s *AdminService) GetModule(ctx context.Context, moduleID string) (*types.ModuleInfo, error) {
	rec, err := s.moduleStore.GetModule(ctx, moduleID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, ErrModuleNotFound
	}
	return moduleRecordToInfo(rec), nil
}

// RevokeModule takes a module out of service; its taps become unknown_module.
func (s *AdminService) RevokeModule(ctx context.Context, moduleID, actorUUID string) error {
	err := s.revokeModule(ctx, moduleID)
	s.audit(ctx, actorUUID, "module.revoke", "module", moduleID, err, nil)
	return err
}

func (s *AdminService) revokeModule(ctx context.Context, moduleID string) error {
	if strings.TrimSpace(moduleID) == "" {
		return ErrModuleIDRequired
	}
	if err := s.moduleStore.RevokeModule(ctx, moduleID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrModuleNotFound
		}
		return fmt.Errorf("revoke module: %w", err)
	}
	return nil
}

// DeleteModule removes a module record.
func (s *AdminService) DeleteModule(ctx context.Context, moduleID, actorUUID string) error {
	err := s.deleteModule(ctx, moduleID)
	s.audit(ctx, actorUUID, "module.delete", "module", moduleID, err, nil)
	return err
}

func (s *AdminService) deleteModule(ctx context.Context, moduleID string) error {
	if strings.TrimSpace(moduleID) == "" {
		return ErrModuleIDRequired
	}
	if err := s.moduleStore.DeleteModule(ctx, moduleID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrModuleNotFound
		}
		return fmt.Errorf("delete module: %w", err)
	}
	return nil
}

// ── Credential hashing ───────────────────────────────────────────────────────

// HashCredentialID computes the canonical credential hash for storage and lookup.
// Algorithm: HMAC-SHA256(secret, rawUID) when secret is non-empty; SHA-256(rawUID) otherwise.
// Input: rawUID must be the raw RFID UID bytes (e.g. {0x04, 0xA3, 0x2B, 0x1C}) — not a
// formatted string.  Use ParseCredentialUID to convert a colon-hex string first.
// This is the sole server-side hashing function; every enrollment and lookup path calls it.
func HashCredentialID(rawUID []byte, secret []byte) []byte {
	if len(secret) > 0 {
		mac := hmac.New(sha256.New, secret)
		mac.Write(rawUID)
		return mac.Sum(nil)
	}
	h := sha256.Sum256(rawUID)
	return h[:]
}

// ── Doors ───────────────────────────────────────────────────────────────────

// RegisterDoor creates a door.
func (s *AdminService) RegisterDoor(ctx context.Context, req types.RegisterDoorRequest, actorUUID string) (*types.DoorInfo, error) {
	info, err := s.registerDoor(ctx, req)
	s.audit(ctx, actorUUID, "door.register", "door", req.DoorID, err, map[string]any{"name": strings.TrimSpace(req.Name)})
	return info, err
}

func (s *AdminService) registerDoor(ctx context.Context, req types.RegisterDoorRequest) (*types.DoorInfo, error) {
	doorID := strings.TrimSpace(req.DoorID)
	if doorID == "" {
		return nil, ErrDoorIDRequired
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, ErrDoorNameRequired
	}

	if err := s.moduleStore.RegisterDoor(ctx, doorID, name, req.Location); err != nil {
		return nil, err
	}

	return &types.DoorInfo{
		DoorID:    doorID,
		Name:      name,
		Location:  req.Location,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *AdminService) ListDoors(ctx context.Context) ([]types.DoorInfo, error) {
	recs, err := s.moduleStore.ListDoors(ctx)
	if err != nil {
		return nil, err
	}

	infos := make([]types.DoorInfo, len(recs))
	for i := range recs {
		infos[i] = types.DoorInfo{
			DoorID:    recs[i].DoorID,
			Name:      recs[i].Name,
			Location:  recs[i].Location,
			CreatedAt: recs[i].CreatedAt.Format(time.RFC3339),
		}
	}
	return infos, nil
}

// DeleteDoor removes a door.
func (s *AdminService) DeleteDoor(ctx context.Context, doorID, actorUUID string) error {
	err := s.deleteDoor(ctx, doorID)
	s.audit(ctx, actorUUID, "door.delete", "door", doorID, err, nil)
	return err
}

func (s *AdminService) deleteDoor(ctx context.Context, doorID string) error {
	if strings.TrimSpace(doorID) == "" {
		return ErrDoorIDRequired
	}
	if err := s.moduleStore.DeleteDoor(ctx, doorID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrDoorNotFound
		}
		return fmt.Errorf("delete door: %w", err)
	}
	return nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func deriveModuleStatus(rec *store.ModuleRecord) types.ModuleStatus {
	if rec.RevokedAt != nil {
		return types.ModuleStatusRevoked
	}
	if rec.CommissionedAt == nil {
		return types.ModuleStatusDiscovered
	}
	return types.ModuleStatusActive
}

func moduleRecordToInfo(rec *store.ModuleRecord) *types.ModuleInfo {
	if rec == nil {
		return nil
	}
	info := &types.ModuleInfo{
		ModuleID:      rec.ModuleID,
		DoorID:        rec.DoorID,
		DisplayName:   rec.DisplayName,
		ModuleType:    string(rec.ModuleType),
		Status:        deriveModuleStatus(rec),
		Enabled:       rec.Enabled,
		Commissioned:  rec.CommissionedAt != nil,
		LastIP:        rec.LastIP,
		LastFWVersion: rec.LastFWVersion,
		LastWiFiRSSI:  rec.LastWiFiRSSI,
		CreatedAt:     rec.CreatedAt.Format(time.RFC3339),
	}
	if rec.CommissionedAt != nil {
		info.CommissionedAt = rec.CommissionedAt.Format(time.RFC3339)
	}
	if rec.RevokedAt != nil {
		info.RevokedAt = rec.RevokedAt.Format(time.RFC3339)
	}
	if rec.LastSeenAt != nil {
		info.LastSeenAt = rec.LastSeenAt.Format(time.RFC3339)
	}
	return info
}
