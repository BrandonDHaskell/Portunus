package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

var (
	ErrInvalidCardID = errors.New("card_id is required")
	ErrUnknownModule = errors.New("unknown module")
)

type AccessPolicy struct {
	AllowAll       bool
	AllowedCardIDs map[string]struct{}
}

type AccessService struct {
	registry *DeviceRegistry
	policy   AccessPolicy
}

func NewAccessService(reg *DeviceRegistry, policy AccessPolicy) *AccessService {
	return &AccessService{registry: reg, policy: policy}
}

func (s *AccessService) Decide(ctx context.Context, req types.AccessRequest) (types.AccessResponse, error) {
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
		// Block unknown modules from access flow
		return types.AccessResponse{
			OK:         false,
			Known:      false,
			Granted:    false,
			Reason:     "unknown_module",
			ModuleID:   moduleID,
			ServerTime: time.Now().UTC().Format(time.RFC3339Nano),
		}, ErrUnknownModule
	}

	// Decision logic (v1 testing-friendly)
	granted := false
	reason := "denied"

	if s.policy.AllowAll {
		granted = true
		reason = "allow_all"
	} else {
		if _, ok := s.policy.AllowedCardIDs[cardID]; ok {
			granted = true
			reason = "card_allowed"
		} else {
			reason = "card_not_allowed"
		}
	}

	return types.AccessResponse{
		OK:         true,
		Known:      true,
		Granted:    granted,
		Reason:     reason,
		ModuleID:   moduleID,
		ServerTime: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}
