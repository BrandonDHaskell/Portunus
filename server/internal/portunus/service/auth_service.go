package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

const (
	sessionDuration = 8 * time.Hour
	bcryptCost      = 12
	bcryptMaxLen    = 72
)

// dummyHash is a bcrypt hash of a fixed string computed once at first use.
// It is compared against the supplied password on the "user not found" path so
// that both paths spend the same bcrypt work before returning ErrInvalidCredentials,
// preventing username-enumeration via response timing (F-2).
var (
	dummyHashOnce sync.Once
	dummyHash     []byte
)

func getDummyHash() []byte {
	dummyHashOnce.Do(func() {
		h, _ := bcrypt.GenerateFromPassword([]byte("portunus-timing-dummy-constant"), bcryptCost)
		dummyHash = h
	})
	return dummyHash
}

var (
	ErrInvalidCredentials   = errors.New("invalid username or password")
	ErrAccountDisabled      = errors.New("account is disabled")
	ErrPasswordChangeFailed = errors.New("password change failed")
)

// AdminSession is the resolved identity injected into request context after
// session validation.
type AdminSession struct {
	AdminUUID    string
	Username     string
	RoleID       string
	MustChangePW bool
	Permissions  map[string]struct{}
	MemberUUID   string // admin_users.member_uuid; empty if no linked member
}

// HasPermission reports whether the session carries the given permission.
func (s *AdminSession) HasPermission(p string) bool {
	_, ok := s.Permissions[p]
	return ok
}

// AuthService handles admin authentication: bootstrap, login, logout, session
// resolution, and password changes.
type AuthService struct {
	users           store.AdminUserStore
	sessions        store.SessionStore
	roles           store.RoleStore
	logger          *log.Logger
	bootstrapPWFile string // path to write the initial admin password; empty → log it
}

func NewAuthService(
	users store.AdminUserStore,
	sessions store.SessionStore,
	roles store.RoleStore,
	logger *log.Logger,
) *AuthService {
	return &AuthService{users: users, sessions: sessions, roles: roles, logger: logger}
}

// SetBootstrapPasswordFile configures the path where the initial admin password
// is written on first boot instead of to the log stream (F-9).  The file is
// created with mode 0600 so only the service user can read it.
// Call before Bootstrap.  An empty path reverts to logging (test/dev use only).
func (s *AuthService) SetBootstrapPasswordFile(path string) {
	s.bootstrapPWFile = path
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

	if err := s.users.CreateAdminUser(ctx, uuid, "admin", string(hash), "admin"); err != nil {
		return fmt.Errorf("bootstrap: create admin user: %w", err)
	}

	if s.bootstrapPWFile != "" {
		content := fmt.Sprintf("username: admin\npassword: %s\n", password)
		if err := os.WriteFile(s.bootstrapPWFile, []byte(content), 0o600); err != nil {
			// Fall back to logging if we can't write the file.
			s.logger.Printf("WARNING: could not write bootstrap password file %q: %v — printing to log instead", s.bootstrapPWFile, err)
			s.logger.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			s.logger.Printf("FIRST RUN — initial admin account created")
			s.logger.Printf("  username: admin")
			s.logger.Printf("  password: %s", password)
			s.logger.Printf("  You will be required to change this password on first login.")
			s.logger.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		} else {
			s.logger.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			s.logger.Printf("FIRST RUN — initial admin account created")
			s.logger.Printf("  username: admin")
			s.logger.Printf("  password written to: %s  (delete after first login)", s.bootstrapPWFile)
			s.logger.Printf("  You will be required to change this password on first login.")
			s.logger.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		}
	} else {
		s.logger.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		s.logger.Printf("FIRST RUN — initial admin account created")
		s.logger.Printf("  username: admin")
		s.logger.Printf("  password: %s", password)
		s.logger.Printf("  You will be required to change this password on first login.")
		s.logger.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	}

	return nil
}

// Login validates credentials and returns a new session ID on success.
func (s *AuthService) Login(ctx context.Context, username, password string) (string, error) {
	user, err := s.users.GetAdminUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Run a dummy bcrypt compare so that a missing username takes the
			// same wall-clock time as a present-but-wrong-password attempt,
			// preventing username enumeration via response timing (F-2).
			_ = bcrypt.CompareHashAndPassword(getDummyHash(), []byte(password))
			return "", ErrInvalidCredentials
		}
		return "", fmt.Errorf("login: %w", err)
	}

	if !user.Enabled {
		return "", ErrAccountDisabled
	}

	if user.ExpiresAt != nil && time.Now().UTC().After(*user.ExpiresAt) {
		return "", ErrInvalidCredentials
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

	// Reject sessions for disabled or expired accounts without invalidating the
	// session token (the user should see "invalid credentials" rather than a hint).
	if !user.Enabled {
		return nil, store.ErrNotFound
	}

	if user.ExpiresAt != nil && time.Now().UTC().After(*user.ExpiresAt) {
		return nil, store.ErrNotFound
	}

	roleID := user.RoleID
	if roleID == "" {
		roleID = "admin"
	}

	perms, err := s.roles.GetRolePermissions(ctx, roleID)
	if err != nil {
		return nil, fmt.Errorf("resolve session: load permissions: %w", err)
	}

	permSet := make(map[string]struct{}, len(perms))
	for _, p := range perms {
		permSet[p] = struct{}{}
	}

	memberUUID := ""
	if user.MemberUUID != nil {
		memberUUID = *user.MemberUUID
	}

	return &AdminSession{
		AdminUUID:    user.UUID,
		Username:     user.Username,
		RoleID:       roleID,
		MustChangePW: user.MustChangePW,
		Permissions:  permSet,
		MemberUUID:   memberUUID,
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
	if len(newPassword) > bcryptMaxLen {
		return fmt.Errorf("%w: password must not exceed %d characters", ErrPasswordChangeFailed, bcryptMaxLen)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("change password: hash: %w", err)
	}

	if err := s.users.UpdatePasswordHash(ctx, adminUUID, string(hash)); err != nil {
		return err
	}

	// Revoke all sessions so the new password takes effect everywhere and any
	// previously issued (or stolen) token is invalidated. The user must log in again.
	if err := s.sessions.DeleteSessionsForAdmin(ctx, adminUUID); err != nil {
		return fmt.Errorf("change password: revoke sessions: %w", err)
	}
	return nil
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
