package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

var (
	ErrAdminUserNotFound = errors.New("admin user not found")
	ErrCannotSelfDisable = errors.New("cannot disable your own account")
)

// AdminUserInfo is the view type for admin user management pages.
type AdminUserInfo struct {
	UUID         string
	Username     string
	RoleID       string
	Enabled      bool
	MustChangePW bool
	CreatedAt    string
	LastLoginAt  string
}

// AdminUserService manages server operator accounts (create, list, edit,
// disable, assign role).  Auth operations (login/session) stay in AuthService.
type AdminUserService struct {
	users store.AdminUserStore
	roles store.RoleStore
}

func NewAdminUserService(users store.AdminUserStore, roles store.RoleStore) *AdminUserService {
	return &AdminUserService{users: users, roles: roles}
}

// CreateUser creates a new admin user with the given role.
// The new account starts with must_change_pw = 1 so the operator must set a
// personal password on first login.
func (s *AdminUserService) CreateUser(ctx context.Context, username, password, roleID string) (*AdminUserInfo, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if len(password) < 12 {
		return nil, fmt.Errorf("%w: password must be at least 12 characters", ErrPasswordChangeFailed)
	}
	roleID = strings.TrimSpace(roleID)
	if roleID == "" {
		return nil, fmt.Errorf("role_id is required")
	}

	// Verify the role exists.
	if _, err := s.roles.GetRole(ctx, roleID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("role %q not found", roleID)
		}
		return nil, fmt.Errorf("create user: verify role: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("create user: hash password: %w", err)
	}

	uuid, err := generateUUID()
	if err != nil {
		return nil, fmt.Errorf("create user: generate uuid: %w", err)
	}

	if err := s.users.CreateAdminUser(ctx, uuid, username, string(hash), roleID); err != nil {
		return nil, err
	}

	return &AdminUserInfo{
		UUID:         uuid,
		Username:     username,
		RoleID:       roleID,
		Enabled:      true,
		MustChangePW: true,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// ListUsers returns all admin users.
func (s *AdminUserService) ListUsers(ctx context.Context) ([]AdminUserInfo, error) {
	recs, err := s.users.ListAdminUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	infos := make([]AdminUserInfo, len(recs))
	for i, r := range recs {
		infos[i] = adminUserRecordToInfo(&r)
	}
	return infos, nil
}

// GetUser returns a single admin user by UUID.
func (s *AdminUserService) GetUser(ctx context.Context, uuid string) (*AdminUserInfo, error) {
	rec, err := s.users.GetAdminUserByUUID(ctx, uuid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrAdminUserNotFound
		}
		return nil, fmt.Errorf("get user: %w", err)
	}
	info := adminUserRecordToInfo(rec)
	return &info, nil
}

// SetEnabled enables or disables an admin account.
// A user may not disable their own account (callerUUID guard).
func (s *AdminUserService) SetEnabled(ctx context.Context, uuid, callerUUID string, enabled bool) error {
	if !enabled && uuid == callerUUID {
		return ErrCannotSelfDisable
	}
	if err := s.users.SetAdminUserEnabled(ctx, uuid, enabled); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrAdminUserNotFound
		}
		return fmt.Errorf("set enabled: %w", err)
	}
	return nil
}

// AssignRole assigns a role to an admin user.
func (s *AdminUserService) AssignRole(ctx context.Context, uuid, roleID string) error {
	roleID = strings.TrimSpace(roleID)
	if roleID == "" {
		return fmt.Errorf("role_id is required")
	}
	if _, err := s.roles.GetRole(ctx, roleID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("role %q not found", roleID)
		}
		return fmt.Errorf("assign role: verify role: %w", err)
	}
	if err := s.users.SetAdminUserRole(ctx, uuid, roleID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrAdminUserNotFound
		}
		return fmt.Errorf("assign role: %w", err)
	}
	return nil
}

func adminUserRecordToInfo(r *store.AdminUserRecord) AdminUserInfo {
	info := AdminUserInfo{
		UUID:         r.UUID,
		Username:     r.Username,
		RoleID:       r.RoleID,
		Enabled:      r.Enabled,
		MustChangePW: r.MustChangePW,
		CreatedAt:    r.CreatedAt.Format(time.RFC3339),
	}
	if r.LastLoginAt != nil {
		info.LastLoginAt = r.LastLoginAt.Format(time.RFC3339)
	}
	return info
}
