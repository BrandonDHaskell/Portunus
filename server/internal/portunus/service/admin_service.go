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
	ErrModuleIDRequired     = errors.New("module_id is required")
	ErrCredentialIDRequired = errors.New("credential_id is required")
	ErrDoorIDRequired       = errors.New("door_id is required")
	ErrDoorNameRequired     = errors.New("door name is required")
	ErrInvalidStatus        = errors.New("status must be active, disabled, or lost")
	ErrCredentialNotFound   = errors.New("credential not found")
	ErrModuleNotFound       = errors.New("module not found")
	ErrDoorNotFound         = errors.New("door not found")
)

type AdminService struct {
	moduleStore          store.ModuleAdminStore
	credentialStore      store.CredentialStore
	credentialHashSecret []byte
}

func NewAdminService(ms store.ModuleAdminStore, cs store.CredentialStore, credentialHashSecret []byte) *AdminService {
	return &AdminService{moduleStore: ms, credentialStore: cs, credentialHashSecret: credentialHashSecret}
}

// ── Modules ─────────────────────────────────────────────────────────────────

func (s *AdminService) RegisterModule(ctx context.Context, req types.RegisterModuleRequest) (*types.ModuleInfo, error) {
	moduleID := strings.TrimSpace(req.ModuleID)
	if moduleID == "" {
		return nil, ErrModuleIDRequired
	}

	if err := s.moduleStore.CommissionModule(ctx, moduleID, req.DoorID, req.DisplayName); err != nil {
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

func (s *AdminService) RevokeModule(ctx context.Context, moduleID string) error {
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

func (s *AdminService) DeleteModule(ctx context.Context, moduleID string) error {
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

// ── Credentials ──────────────────────────────────────────────────────────────

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

func (s *AdminService) RegisterCredential(ctx context.Context, req types.RegisterCredentialRequest) (*types.CredentialInfo, error) {
	credentialID := strings.TrimSpace(req.CredentialID)
	if credentialID == "" {
		return nil, ErrCredentialIDRequired
	}

	rawUID, err := ParseCredentialUID(credentialID)
	if err != nil {
		return nil, fmt.Errorf("invalid credential_id format: %w", err)
	}
	hash := HashCredentialID(rawUID, s.credentialHashSecret)
	if err := s.credentialStore.RegisterCredential(ctx, hash, req.Tag); err != nil {
		return nil, err
	}

	return &types.CredentialInfo{
		CredentialHash: hex.EncodeToString(hash),
		Tag:            req.Tag,
		Status:         "active",
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *AdminService) ListCredentials(ctx context.Context) ([]types.CredentialInfo, error) {
	recs, err := s.credentialStore.ListCredentials(ctx)
	if err != nil {
		return nil, err
	}

	infos := make([]types.CredentialInfo, len(recs))
	for i := range recs {
		infos[i] = credentialRecordToInfo(&recs[i])
	}
	return infos, nil
}

func (s *AdminService) SetCredentialStatus(ctx context.Context, credentialHashHex string, status string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "active", "disabled", "lost":
		// valid
	default:
		return ErrInvalidStatus
	}

	hash, err := hex.DecodeString(credentialHashHex)
	if err != nil {
		return fmt.Errorf("invalid credential_hash hex: %w", err)
	}
	if err := s.credentialStore.SetCredentialStatus(ctx, hash, status); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrCredentialNotFound
		}
		return fmt.Errorf("set credential status: %w", err)
	}
	return nil
}

func (s *AdminService) DeleteCredential(ctx context.Context, credentialHashHex string) error {
	hash, err := hex.DecodeString(credentialHashHex)
	if err != nil {
		return fmt.Errorf("invalid credential_hash hex: %w", err)
	}
	if err := s.credentialStore.DeleteCredential(ctx, hash); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrCredentialNotFound
		}
		return fmt.Errorf("delete credential: %w", err)
	}
	return nil
}

// ── Doors ───────────────────────────────────────────────────────────────────

func (s *AdminService) RegisterDoor(ctx context.Context, req types.RegisterDoorRequest) (*types.DoorInfo, error) {
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

func (s *AdminService) DeleteDoor(ctx context.Context, doorID string) error {
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

func credentialRecordToInfo(rec *store.CredentialRecord) types.CredentialInfo {
	info := types.CredentialInfo{
		CredentialHash: hex.EncodeToString(rec.CredentialHash),
		Tag:            rec.Tag,
		Status:         rec.Status,
		CreatedAt:      rec.CreatedAt.Format(time.RFC3339),
	}
	if rec.LastSeenAt != nil {
		info.LastSeenAt = rec.LastSeenAt.Format(time.RFC3339)
	}
	return info
}
