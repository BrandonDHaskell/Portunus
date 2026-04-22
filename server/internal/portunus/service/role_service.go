package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

var ErrRoleNotFound = errors.New("role not found")

// RoleInfo is the view type for role management pages.
type RoleInfo struct {
	RoleID                string
	Name                  string
	Description           string
	IsSystem              bool
	DefaultExpiryDays     *int
	DefaultInactivityDays *int
	CreatedAt             string
	Permissions           []string
}

// RoleService manages roles and their permission sets.
type RoleService struct {
	roles store.RoleStore
}

func NewRoleService(roles store.RoleStore) *RoleService {
	return &RoleService{roles: roles}
}

// CreateRole creates a new non-system role.  The role_id is derived from the
// name (lowercase, spaces → underscores, non-alnum stripped).
func (s *RoleService) CreateRole(ctx context.Context, name, description string, defaultExpiryDays, defaultInactivityDays *int) (*RoleInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	roleID := nameToRoleID(name)
	if roleID == "" {
		return nil, fmt.Errorf("name produces an empty role_id; use alphanumeric characters")
	}

	if err := s.roles.CreateRole(ctx, roleID, name, strings.TrimSpace(description), defaultExpiryDays, defaultInactivityDays); err != nil {
		return nil, fmt.Errorf("create role: %w", err)
	}

	return &RoleInfo{
		RoleID:                roleID,
		Name:                  name,
		Description:           strings.TrimSpace(description),
		IsSystem:              false,
		DefaultExpiryDays:     defaultExpiryDays,
		DefaultInactivityDays: defaultInactivityDays,
		CreatedAt:             time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// ListRoles returns all roles.
func (s *RoleService) ListRoles(ctx context.Context) ([]RoleInfo, error) {
	recs, err := s.roles.ListRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	infos := make([]RoleInfo, len(recs))
	for i := range recs {
		infos[i] = roleRecordToInfo(&recs[i])
	}
	return infos, nil
}

// GetRole returns a role with its current permissions.
func (s *RoleService) GetRole(ctx context.Context, roleID string) (*RoleInfo, error) {
	rec, err := s.roles.GetRole(ctx, roleID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrRoleNotFound
		}
		return nil, fmt.Errorf("get role: %w", err)
	}
	info := roleRecordToInfo(rec)

	perms, err := s.roles.GetRolePermissions(ctx, roleID)
	if err != nil {
		return nil, fmt.Errorf("get role permissions: %w", err)
	}
	info.Permissions = perms
	return &info, nil
}

// UpdateRole changes the mutable metadata of a non-system role.
func (s *RoleService) UpdateRole(ctx context.Context, roleID, name, description string, defaultExpiryDays, defaultInactivityDays *int) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if err := s.roles.UpdateRole(ctx, roleID, name, strings.TrimSpace(description), defaultExpiryDays, defaultInactivityDays); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrRoleNotFound
		}
		if errors.Is(err, store.ErrRoleIsSystem) {
			return err
		}
		return fmt.Errorf("update role: %w", err)
	}
	return nil
}

// DeleteRole removes a non-system role.
func (s *RoleService) DeleteRole(ctx context.Context, roleID string) error {
	if err := s.roles.DeleteRole(ctx, roleID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrRoleNotFound
		}
		return err
	}
	return nil
}

// SetPermissions replaces the full permission set for a role.
func (s *RoleService) SetPermissions(ctx context.Context, roleID string, permissions []string) error {
	if err := s.roles.SetRolePermissions(ctx, roleID, permissions); err != nil {
		return fmt.Errorf("set permissions: %w", err)
	}
	return nil
}

// GetPermissions returns the permissions for a role.
func (s *RoleService) GetPermissions(ctx context.Context, roleID string) ([]string, error) {
	return s.roles.GetRolePermissions(ctx, roleID)
}

func roleRecordToInfo(r *store.RoleRecord) RoleInfo {
	return RoleInfo{
		RoleID:                r.RoleID,
		Name:                  r.Name,
		Description:           r.Description,
		IsSystem:              r.IsSystem,
		DefaultExpiryDays:     r.DefaultExpiryDays,
		DefaultInactivityDays: r.DefaultInactivityDays,
		CreatedAt:             r.CreatedAt.Format(time.RFC3339),
	}
}

// nameToRoleID converts a human-readable role name to a snake_case identifier.
func nameToRoleID(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	prevWasUnderscore := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevWasUnderscore = false
		case unicode.IsSpace(r) || r == '-' || r == '_':
			if !prevWasUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevWasUnderscore = true
			}
		}
	}
	id := strings.TrimRight(b.String(), "_")
	return id
}
