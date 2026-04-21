package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

const (
	sessionDuration = 8 * time.Hour
	bcryptCost      = 12
)

var (
	ErrInvalidCredentials   = errors.New("invalid username or password")
	ErrPasswordChangeFailed = errors.New("password change failed")
)

// AdminSession is the resolved identity injected into request context after
// session validation.
type AdminSession struct {
	AdminUUID    string
	Username     string
	MustChangePW bool
	Permissions  map[string]struct{}
}

// HasPermission reports whether the session carries the given permission.
func (s *AdminSession) HasPermission(p string) bool {
	_, ok := s.Permissions[p]
	return ok
}

// AuthService handles admin authentication: bootstrap, login, logout, session
// resolution, and password changes.
type AuthService struct {
	users    store.AdminUserStore
	sessions store.SessionStore
	roles    store.RoleStore
	logger   *log.Logger
}

func NewAuthService(
	users store.AdminUserStore,
	sessions store.SessionStore,
	roles store.RoleStore,
	logger *log.Logger,
) *AuthService {
	return &AuthService{users: users, sessions: sessions, roles: roles, logger: logger}
}

// Bootstrap checks whether any admin user exists. If none does, it creates the
// initial "admin" account, generates a random password, prints it to the
// logger (stdout), and sets must_change_pw = 1.
func (s *AuthService) Bootstrap(ctx context.Context) error {
	exists, err := s.users.AnyAdminExists(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	if exists {
		return nil
	}

	password, err := generateRandomPassword()
	if err != nil {
		return fmt.Errorf("bootstrap: generate password: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("bootstrap: hash password: %w", err)
	}

	uuid, err := generateUUID()
	if err != nil {
		return fmt.Errorf("bootstrap: generate uuid: %w", err)
	}

	if err := s.users.CreateAdminUser(ctx, uuid, "admin", string(hash)); err != nil {
		return fmt.Errorf("bootstrap: create admin user: %w", err)
	}

	s.logger.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	s.logger.Printf("FIRST RUN — initial admin account created")
	s.logger.Printf("  username: admin")
	s.logger.Printf("  password: %s", password)
	s.logger.Printf("  You will be required to change this password on first login.")
	s.logger.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	return nil
}

// Login validates credentials and returns a new session ID on success.
func (s *AuthService) Login(ctx context.Context, username, password string) (string, error) {
	user, err := s.users.GetAdminUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", ErrInvalidCredentials
		}
		return "", fmt.Errorf("login: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", ErrInvalidCredentials
	}

	sessionID, err := generateSessionID()
	if err != nil {
		return "", fmt.Errorf("login: generate session: %w", err)
	}

	expiresAt := time.Now().UTC().Add(sessionDuration)
	if err := s.sessions.CreateSession(ctx, sessionID, user.UUID, expiresAt); err != nil {
		return "", fmt.Errorf("login: create session: %w", err)
	}

	_ = s.users.UpdateLastLogin(ctx, user.UUID, time.Now().UTC())
	return sessionID, nil
}

// Logout deletes the session.
func (s *AuthService) Logout(ctx context.Context, sessionID string) error {
	return s.sessions.DeleteSession(ctx, sessionID)
}

// ResolveSession looks up a session by ID, verifies it is not expired, loads
// the associated admin user, and returns an AdminSession with the user's role
// permissions resolved.
func (s *AuthService) ResolveSession(ctx context.Context, sessionID string) (*AdminSession, error) {
	sess, err := s.sessions.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	if time.Now().UTC().After(sess.ExpiresAt) {
		_ = s.sessions.DeleteSession(ctx, sessionID)
		return nil, store.ErrNotFound
	}

	user, err := s.users.GetAdminUserByUUID(ctx, sess.AdminUUID)
	if err != nil {
		return nil, fmt.Errorf("resolve session: load user: %w", err)
	}

	perms, err := s.roles.GetRolePermissions(ctx, "admin")
	if err != nil {
		return nil, fmt.Errorf("resolve session: load permissions: %w", err)
	}

	permSet := make(map[string]struct{}, len(perms))
	for _, p := range perms {
		permSet[p] = struct{}{}
	}

	return &AdminSession{
		AdminUUID:    user.UUID,
		Username:     user.Username,
		MustChangePW: user.MustChangePW,
		Permissions:  permSet,
	}, nil
}

// ChangePassword verifies the current password and replaces it with the new one.
// Clears must_change_pw on success.
func (s *AuthService) ChangePassword(ctx context.Context, adminUUID, currentPassword, newPassword string) error {
	user, err := s.users.GetAdminUserByUUID(ctx, adminUUID)
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
		return ErrInvalidCredentials
	}

	if len(newPassword) < 12 {
		return fmt.Errorf("%w: password must be at least 12 characters", ErrPasswordChangeFailed)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("change password: hash: %w", err)
	}

	return s.users.UpdatePasswordHash(ctx, adminUUID, string(hash))
}

func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func generateRandomPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateUUID produces a v4 UUID using crypto/rand.
func generateUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
