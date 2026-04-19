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

var (
	ErrModuleIDRequired = errors.New("module_id is required")
	ErrCardIDRequired   = errors.New("card_id is required")
	ErrDoorIDRequired   = errors.New("door_id is required")
	ErrDoorNameRequired = errors.New("door name is required")
	ErrInvalidStatus    = errors.New("status must be active, disabled, or lost")
	ErrCardNotFound     = errors.New("card not found")
	ErrModuleNotFound   = errors.New("module not found")
	ErrDoorNotFound     = errors.New("door not found")
)

type AdminService struct {
	moduleStore    store.ModuleAdminStore
	cardStore      store.CardStore
	cardHashSecret []byte
}

func NewAdminService(ms store.ModuleAdminStore, cs store.CardStore, cardHashSecret []byte) *AdminService {
	return &AdminService{moduleStore: ms, cardStore: cs, cardHashSecret: cardHashSecret}
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

// ── Cards ───────────────────────────────────────────────────────────────────

// HashCardID computes the card hash used for storage and lookups.
// When secret is non-empty, uses HMAC-SHA256(secret, cardID).
// Falls back to bare SHA-256 when secret is nil (dev/migration only).
func HashCardID(cardID string, secret []byte) []byte {
	id := []byte(strings.TrimSpace(cardID))
	if len(secret) > 0 {
		mac := hmac.New(sha256.New, secret)
		mac.Write(id)
		return mac.Sum(nil)
	}
	h := sha256.Sum256(id)
	return h[:]
}

func (s *AdminService) RegisterCard(ctx context.Context, req types.RegisterCardRequest) (*types.CardInfo, error) {
	cardID := strings.TrimSpace(req.CardID)
	if cardID == "" {
		return nil, ErrCardIDRequired
	}

	hash := HashCardID(cardID, s.cardHashSecret)
	if err := s.cardStore.RegisterCard(ctx, hash, req.Tag); err != nil {
		return nil, err
	}

	return &types.CardInfo{
		CardIDHash: hex.EncodeToString(hash),
		Tag:        req.Tag,
		Status:     "active",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *AdminService) ListCards(ctx context.Context) ([]types.CardInfo, error) {
	recs, err := s.cardStore.ListCards(ctx)
	if err != nil {
		return nil, err
	}

	infos := make([]types.CardInfo, len(recs))
	for i := range recs {
		infos[i] = cardRecordToInfo(&recs[i])
	}
	return infos, nil
}

func (s *AdminService) SetCardStatus(ctx context.Context, cardIDHashHex string, status string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "active", "disabled", "lost":
		// valid
	default:
		return ErrInvalidStatus
	}

	hash, err := hex.DecodeString(cardIDHashHex)
	if err != nil {
		return fmt.Errorf("invalid card_id_hash hex: %w", err)
	}
	if err := s.cardStore.SetCardStatus(ctx, hash, status); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrCardNotFound
		}
		return fmt.Errorf("set card status: %w", err)
	}
	return nil
}

func (s *AdminService) DeleteCard(ctx context.Context, cardIDHashHex string) error {
	hash, err := hex.DecodeString(cardIDHashHex)
	if err != nil {
		return fmt.Errorf("invalid card_id_hash hex: %w", err)
	}
	if err := s.cardStore.DeleteCard(ctx, hash); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrCardNotFound
		}
		return fmt.Errorf("delete card: %w", err)
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

func moduleRecordToInfo(rec *store.ModuleRecord) *types.ModuleInfo {
	if rec == nil {
		return nil
	}
	info := &types.ModuleInfo{
		ModuleID:      rec.ModuleID,
		DoorID:        rec.DoorID,
		DisplayName:   rec.DisplayName,
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

func cardRecordToInfo(rec *store.CardRecord) types.CardInfo {
	info := types.CardInfo{
		CardIDHash: hex.EncodeToString(rec.CardIDHash),
		Tag:        rec.Tag,
		Status:     rec.Status,
		CreatedAt:  rec.CreatedAt.Format(time.RFC3339),
	}
	if rec.LastSeenAt != nil {
		info.LastSeenAt = rec.LastSeenAt.Format(time.RFC3339)
	}
	return info
}
