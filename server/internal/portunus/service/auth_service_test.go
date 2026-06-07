package service_test

import (
	"context"
	"errors"
	"log"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// newAuthSvcWithStores wires AuthService and returns the underlying stores so
// tests can seed rows directly without going through the service.
func newAuthSvcWithStores(t *testing.T) (*service.AuthService, *sqlitestore.AdminUserStore, *sqlitestore.SessionStore) {
	t.Helper()
	dbConn, writer := openSvcTestDB(t)
	us := sqlitestore.NewAdminUserStore(dbConn, writer)
	ss := sqlitestore.NewSessionStore(dbConn, writer)
	rs := sqlitestore.NewRoleStore(dbConn, writer)
	svc := service.NewAuthService(us, ss, rs, log.Default())
	return svc, us, ss
}

// hashPW produces a bcrypt hash at minimum cost so tests run fast.
func hashPW(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hashPW: %v", err)
	}
	return string(h)
}

// ── ChangePassword: sessions revoked ─────────────────────────────────────────

func TestAuthService_ChangePassword_RevokesAllSessions(t *testing.T) {
	svc, us, ss := newAuthSvcWithStores(t)
	ctx := context.Background()

	const (
		uuid    = "uuid-chpw-1"
		oldPass = "correct-horse-battery"
		newPass = "correct-horse-battery-staple"
	)

	if err := us.CreateAdminUser(ctx, uuid, "user1", hashPW(t, oldPass), "admin"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	expires := time.Now().UTC().Add(8 * time.Hour)
	if err := ss.CreateSession(ctx, "sess-A", uuid, expires); err != nil {
		t.Fatalf("create session A: %v", err)
	}
	if err := ss.CreateSession(ctx, "sess-B", uuid, expires); err != nil {
		t.Fatalf("create session B: %v", err)
	}

	if err := svc.ChangePassword(ctx, uuid, oldPass, newPass); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	for _, sid := range []string{"sess-A", "sess-B"} {
		_, err := ss.GetSession(ctx, sid)
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("session %q still exists after password change (err=%v)", sid, err)
		}
	}
}

// ── ChangePassword: wrong current password ────────────────────────────────────

func TestAuthService_ChangePassword_WrongCurrentPassword(t *testing.T) {
	svc, us, _ := newAuthSvcWithStores(t)
	ctx := context.Background()

	if err := us.CreateAdminUser(ctx, "uuid-chpw-2", "user2", hashPW(t, "correct-pass-long"), "admin"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	err := svc.ChangePassword(ctx, "uuid-chpw-2", "wrong-password-here", "new-valid-password-123")
	if !errors.Is(err, service.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got: %v", err)
	}
}

// ── ChangePassword: new password too short ────────────────────────────────────

func TestAuthService_ChangePassword_ShortNewPasswordRejected(t *testing.T) {
	svc, us, _ := newAuthSvcWithStores(t)
	ctx := context.Background()

	const oldPass = "correct-horse-battery"
	if err := us.CreateAdminUser(ctx, "uuid-chpw-3", "user3", hashPW(t, oldPass), "admin"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	err := svc.ChangePassword(ctx, "uuid-chpw-3", oldPass, "tooshort")
	if !errors.Is(err, service.ErrPasswordChangeFailed) {
		t.Fatalf("expected ErrPasswordChangeFailed, got: %v", err)
	}
}
