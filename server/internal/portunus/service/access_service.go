package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

var (
	ErrInvalidCardID = errors.New("card_id is required")
)

type AccessPolicy struct {
	// AllowAll grants access to any card (dev/testing only).
	AllowAll bool
	// AllowedCardIDs is the legacy env-var allowlist (deprecated).
	// When CardStore is set, this is ignored.
	AllowedCardIDs map[string]struct{}
}

type AccessService struct {
	registry       *DeviceRegistry
	policy         AccessPolicy
	eventStore     store.AccessEventStore
	cardStore      store.CardStore // nil = use legacy AllowedCardIDs map
	cardHashSecret []byte
}

func NewAccessService(reg *DeviceRegistry, policy AccessPolicy, es store.AccessEventStore) *AccessService {
	return &AccessService{registry: reg, policy: policy, eventStore: es}
}

// SetCardStore enables DB-backed card lookups, replacing the legacy
// AllowedCardIDs map.  Call after NewAccessService, before serving traffic.
func (s *AccessService) SetCardStore(cs store.CardStore) {
	s.cardStore = cs
}

// SetCardHashSecret sets the HMAC key used to hash card IDs.  Must match the
// key used at registration time.  Call after NewAccessService, before serving traffic.
func (s *AccessService) SetCardHashSecret(secret []byte) {
	s.cardHashSecret = secret
}

func (s *AccessService) Decide(ctx context.Context, req types.AccessRequest) (types.AccessResponse, error) {
	now := time.Now().UTC()

	moduleID := strings.TrimSpace(req.ModuleID)
	cardID := strings.TrimSpace(req.CardID)

	if moduleID == "" {
		return types.AccessResponse{}, ErrInvalidModuleID
	}
	if cardID == "" {
		return types.AccessResponse{}, ErrInvalidCardID
	}

	known, err := s.registry.IsKnown(ctx, moduleID)
	if err != nil {
		return types.AccessResponse{}, err
	}
	_ = s.registry.NoteSeen(ctx, moduleID, known)

	if !known {
		resp := types.AccessResponse{
			OK:         false,
			Known:      false,
			Granted:    false,
			Reason:     "unknown_module",
			ModuleID:   moduleID,
			ServerTime: now.Format(time.RFC3339Nano),
		}

		s.recordEvent(ctx, req, false, "unknown_module", now)

		return resp, nil
	}

	// Decision logic
	granted := false
	reason := "denied"

	if s.policy.AllowAll {
		granted = true
		reason = "allow_all"
	} else if s.cardStore != nil {
		// DB-backed card lookup (production path).
		allowed, err := s.cardStore.IsCardAllowed(ctx, HashCardID(cardID, s.cardHashSecret))
		if err != nil {
			s.recordEvent(ctx, req, false, "card_lookup_error", now)
			return types.AccessResponse{}, err
		}
		if allowed {
			granted = true
			reason = "card_allowed"
		} else {
			reason = "card_not_allowed"
		}
	} else {
		// Legacy env-var allowlist fallback.
		if _, ok := s.policy.AllowedCardIDs[cardID]; ok {
			granted = true
			reason = "card_allowed"
		} else {
			reason = "card_not_allowed"
		}
	}

	s.recordEvent(ctx, req, granted, reason, now)

	return types.AccessResponse{
		OK:         true,
		Known:      true,
		Granted:    granted,
		Reason:     reason,
		ModuleID:   moduleID,
		ServerTime: now.Format(time.RFC3339Nano),
	}, nil
}

// recordEvent persists the access decision to the audit log.  Errors are
// intentionally not returned to the caller — a failed audit write should
// not prevent the device from receiving its access decision.  In a future
// iteration this could be promoted to a hard failure if audit completeness
// is a strict requirement.
func (s *AccessService) recordEvent(
	ctx context.Context,
	req types.AccessRequest,
	granted bool,
	reason string,
	decidedAt time.Time,
) {
	rec := store.AccessEventRecord{
		ModuleID:   strings.TrimSpace(req.ModuleID),
		ReceivedAt: decidedAt, // close enough for v1; refine if needed
		DoorClosed: req.DoorClosed,
		Granted:    granted,
		Reason:     reason,
		DecidedAt:  decidedAt,
	}

	if t := parseOptionalTimestamp(req.RequestedAt); t != nil {
		rec.RequestedAt = t
	}

	// Hash the card ID for the audit trail (same algorithm as card registration).
	cardID := strings.TrimSpace(req.CardID)
	if cardID != "" {
		rec.CardIDHash = HashCardID(cardID, s.cardHashSecret)
	}

	_ = s.eventStore.RecordEvent(ctx, rec)
}

// parseOptionalTimestamp attempts to parse a device-reported timestamp.
// Returns nil if the string is empty or unparseable.
func parseOptionalTimestamp(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Try RFC3339 first (most likely from a well-behaved device).
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		u := t.UTC()
		return &u
	}
	// Try RFC3339Nano as a fallback.
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		u := t.UTC()
		return &u
	}
	return nil
}
