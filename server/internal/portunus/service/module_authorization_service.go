package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

var (
	ErrAuthorizationNotFound      = errors.New("authorization not found")
	ErrAuthorizationAlreadyExists = errors.New("authorization already exists for this member and module")
)

type ModuleAuthorizationService struct {
	authStore store.ModuleAuthorizationStore
}

func NewModuleAuthorizationService(as store.ModuleAuthorizationStore) *ModuleAuthorizationService {
	return &ModuleAuthorizationService{authStore: as}
}

// GrantAuthorization creates a new active module authorization.
// grantedByUUID may be empty for automated/system grants.
// timeRestriction is an optional JSON policy string; pass "" for none.
func (s *ModuleAuthorizationService) GrantAuthorization(
	ctx context.Context,
	memberUUID, moduleID, grantedByUUID string,
	expiresAt *time.Time,
	timeRestriction string,
) error {
	memberUUID = strings.TrimSpace(memberUUID)
	moduleID = strings.TrimSpace(moduleID)
	if memberUUID == "" {
		return ErrMemberUUIDRequired
	}
	if moduleID == "" {
		return ErrModuleIDRequired
	}

	if err := s.authStore.GrantAuthorization(ctx, memberUUID, moduleID, grantedByUUID, expiresAt, timeRestriction); err != nil {
		if errors.Is(err, store.ErrAuthorizationAlreadyExists) {
			return ErrAuthorizationAlreadyExists
		}
		return fmt.Errorf("grant authorization: %w", err)
	}
	return nil
}

// RevokeAuthorization soft-deletes an active authorization.
func (s *ModuleAuthorizationService) RevokeAuthorization(ctx context.Context, memberUUID, moduleID, revokedByUUID string) error {
	memberUUID = strings.TrimSpace(memberUUID)
	moduleID = strings.TrimSpace(moduleID)
	if memberUUID == "" {
		return ErrMemberUUIDRequired
	}
	if moduleID == "" {
		return ErrModuleIDRequired
	}

	if err := s.authStore.RevokeAuthorization(ctx, memberUUID, moduleID, revokedByUUID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrAuthorizationNotFound
		}
		return fmt.Errorf("revoke authorization: %w", err)
	}
	return nil
}

// ListByMember returns all authorization records for a member.
func (s *ModuleAuthorizationService) ListByMember(ctx context.Context, memberUUID string) ([]store.ModuleAuthorizationRecord, error) {
	recs, err := s.authStore.ListByMember(ctx, strings.TrimSpace(memberUUID))
	if err != nil {
		return nil, fmt.Errorf("list authorizations by member: %w", err)
	}
	return recs, nil
}

// ListByModule returns all authorization records for a module.
func (s *ModuleAuthorizationService) ListByModule(ctx context.Context, moduleID string) ([]store.ModuleAuthorizationRecord, error) {
	recs, err := s.authStore.ListByModule(ctx, strings.TrimSpace(moduleID))
	if err != nil {
		return nil, fmt.Errorf("list authorizations by module: %w", err)
	}
	return recs, nil
}
