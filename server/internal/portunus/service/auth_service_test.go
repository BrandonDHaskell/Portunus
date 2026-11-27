package service_test

import (
	"context"
	"database/sql"
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

// ── Login: expired account rejected ──────────────────────────────────────────

func TestAuthService_Login_ExpiredAccountRejected(t *testing.T) {
	svc, us, _ := newAuthSvcWithStores(t)
	ctx := context.Background()

	const pass = "correct-horse-battery"
	if err := us.CreateAdminUser(ctx, "uuid-exp-1", "expired-user", hashPW(t, pass), "admin"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	past := time.Now().Add(-time.Hour)
	if err := us.SetAdminUserExpiry(ctx, "uuid-exp-1", &past); err != nil {
		t.Fatalf("set expiry: %v", err)
	}

	_, err := svc.Login(ctx, "expired-user", pass)
	if !errors.Is(err, service.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials for expired account, got: %v", err)
	}
}

// ── ResolveSession: expired account kills session ─────────────────────────────

func TestAuthService_ResolveSession_ExpiredAccountRejected(t *testing.T) {
	svc, us, ss := newAuthSvcWithStores(t)
	ctx := context.Background()

	const pass = "correct-horse-battery"
	if err := us.CreateAdminUser(ctx, "uuid-exp-2", "expired-sess", hashPW(t, pass), "admin"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Create a valid session before expiry.
	sessExpiry := time.Now().Add(8 * time.Hour)
	if err := ss.CreateSession(ctx, "sess-exp", "uuid-exp-2", sessExpiry); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Now expire the account.
	past := time.Now().Add(-time.Hour)
	if err := us.SetAdminUserExpiry(ctx, "uuid-exp-2", &past); err != nil {
		t.Fatalf("set expiry: %v", err)
	}

	_, err := svc.ResolveSession(ctx, "sess-exp")
	if err == nil {
		t.Fatal("expected error for expired account session, got nil")
	}
}

// ── NULL role fails closed ────────────────────────────────────────────────────

// nullRoleUser creates a user and then clears role_id directly, the state a
// hand-edited or partially migrated row could be in.
func nullRoleUser(t *testing.T, conn *sql.DB, us *sqlitestore.AdminUserStore, uuid, username, pass string) {
	t.Helper()
	if err := us.CreateAdminUser(context.Background(), uuid, username, hashPW(t, pass), "admin"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := conn.Exec(`UPDATE admin_users SET role_id = NULL WHERE uuid = ?`, uuid); err != nil {
		t.Fatalf("null role: %v", err)
	}
}

func TestAuthService_NullRole_LoginRejected(t *testing.T) {
	conn, writer := openSvcTestDB(t)
	us := sqlitestore.NewAdminUserStore(conn, writer)
	svc := service.NewAuthService(us, sqlitestore.NewSessionStore(conn, writer), sqlitestore.NewRoleStore(conn, writer), log.Default())

	const pass = "correct-horse-battery"
	nullRoleUser(t, conn, us, "uuid-norole-1", "norole1", pass)

	if _, err := svc.Login(context.Background(), "norole1", pass); !errors.Is(err, service.ErrInvalidCredentials) {
		t.Fatalf("Login with NULL role: got %v, want ErrInvalidCredentials", err)
	}
}

func TestAuthService_NullRole_SessionNotResolved(t *testing.T) {
	conn, writer := openSvcTestDB(t)
	us := sqlitestore.NewAdminUserStore(conn, writer)
	ss := sqlitestore.NewSessionStore(conn, writer)
	svc := service.NewAuthService(us, ss, sqlitestore.NewRoleStore(conn, writer), log.Default())
	ctx := context.Background()

	nullRoleUser(t, conn, us, "uuid-norole-2", "norole2", "correct-horse-battery")
	if err := ss.CreateSession(ctx, "sess-norole", "uuid-norole-2", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}

	sess, err := svc.ResolveSession(ctx, "sess-norole")
	if err == nil {
		t.Fatalf("NULL-role session resolved with %d permissions; must fail closed", len(sess.Permissions))
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want store.ErrNotFound", err)
	}
}
