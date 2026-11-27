package service_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

type authFixture struct {
	svc     *service.AuthService
	users   *sqlitestore.AdminUserStore
	sess    *sqlitestore.SessionStore
	roles   *sqlitestore.RoleStore
	members *sqlitestore.MemberAccessStore
	log     *bytes.Buffer
}

func newAuthFixture(t *testing.T) authFixture {
	t.Helper()
	conn, w := openSvcTestDB(t)
	f := authFixture{
		users:   sqlitestore.NewAdminUserStore(conn, w),
		sess:    sqlitestore.NewSessionStore(conn, w),
		roles:   sqlitestore.NewRoleStore(conn, w),
		members: sqlitestore.NewMemberAccessStore(conn, w),
		log:     &bytes.Buffer{},
	}
	f.svc = service.NewAuthService(f.users, f.sess, f.roles, log.New(f.log, "", 0))
	return f
}

const lifecyclePass = "correct-horse-battery"

func (f authFixture) user(t *testing.T, uuid, name, role string) {
	t.Helper()
	if err := f.users.CreateAdminUser(context.Background(), uuid, name, hashPW(t, lifecyclePass), role); err != nil {
		t.Fatalf("CreateAdminUser: %v", err)
	}
}

func TestAuth_LoginResolveLogout(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	f.user(t, "uuid-l1", "alice", "operator")

	before := time.Now().UTC().Add(-time.Second)
	sid, err := f.svc.Login(ctx, "alice", lifecyclePass)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if len(sid) != 64 {
		t.Errorf("session id length %d, want 64 hex chars", len(sid))
	}

	rec, err := f.users.GetAdminUserByUUID(ctx, "uuid-l1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.LastLoginAt == nil || rec.LastLoginAt.Before(before) {
		t.Errorf("LastLoginAt not updated: %v", rec.LastLoginAt)
	}

	sess, err := f.svc.ResolveSession(ctx, sid)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	want, err := f.roles.GetRolePermissions(ctx, "operator")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for p := range sess.Permissions {
		got = append(got, p)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("session permissions = %v, want operator's %v", got, want)
	}
	if sess.AdminUUID != "uuid-l1" || sess.Username != "alice" || sess.RoleID != "operator" || !sess.MustChangePW {
		t.Errorf("session identity = %+v", sess)
	}

	if err := f.svc.Logout(ctx, sid); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := f.svc.ResolveSession(ctx, sid); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session usable after logout: %v", err)
	}
}

func TestAuth_LoginRejections(t *testing.T) {
	f := newAuthFixture(t)
	f.user(t, "uuid-l2", "bob", "admin")
	ctx := context.Background()
	for name, tc := range map[string][2]string{
		"unknown user":   {"nobody", lifecyclePass},
		"wrong password": {"bob", "not-the-password"},
		"empty password": {"bob", ""},
		"username case":  {"BOB", lifecyclePass},
	} {
		if _, err := f.svc.Login(ctx, tc[0], tc[1]); !errors.Is(err, service.ErrInvalidCredentials) {
			t.Errorf("%s: got %v, want ErrInvalidCredentials", name, err)
		}
	}
}

func TestAuth_ResolveSession_Rejections(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	f.user(t, "uuid-r1", "carol", "admin")

	if _, err := f.svc.ResolveSession(ctx, "no-such-session"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown session: %v", err)
	}

	// An expired session is rejected and removed.
	if err := f.sess.CreateSession(ctx, "sess-old", "uuid-r1", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ResolveSession(ctx, "sess-old"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expired session: %v", err)
	}
	if _, err := f.sess.GetSession(ctx, "sess-old"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expired session row not deleted: %v", err)
	}

	// A disabled account's session is refused but left in place.
	if err := f.sess.CreateSession(ctx, "sess-dis", "uuid-r1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.users.SetAdminUserEnabled(ctx, "uuid-r1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ResolveSession(ctx, "sess-dis"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("disabled account session: %v", err)
	}
	if _, err := f.sess.GetSession(ctx, "sess-dis"); err != nil {
		t.Errorf("disabled account session row removed: %v", err)
	}
}

func TestAuth_ResolveSession_CarriesMemberLink(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	f.user(t, "uuid-r2", "dave", "admin")
	if err := f.members.CreateMember(ctx, "mem-link-1", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatal(err)
	}
	link := "mem-link-1"
	if err := f.users.SetAdminUserMemberLink(ctx, "uuid-r2", &link); err != nil {
		t.Fatalf("SetAdminUserMemberLink: %v", err)
	}
	sid, err := f.svc.Login(ctx, "dave", lifecyclePass)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := f.svc.ResolveSession(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	if sess.MemberUUID != link {
		t.Errorf("MemberUUID = %q, want %q", sess.MemberUUID, link)
	}
}

var bootstrapPassword = regexp.MustCompile(`password: ([0-9a-f]{32})`)

func TestAuth_Bootstrap_LogMode(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	if err := f.svc.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	rec, err := f.users.GetAdminUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("admin not created: %v", err)
	}
	if rec.RoleID != "admin" || !rec.MustChangePW || !rec.Enabled {
		t.Errorf("bootstrap admin = %+v, want enabled admin role with must_change_pw", rec)
	}
	m := bootstrapPassword.FindStringSubmatch(f.log.String())
	if m == nil {
		t.Fatalf("no password in log: %q", f.log.String())
	}
	if _, err := f.svc.Login(ctx, "admin", m[1]); err != nil {
		t.Fatalf("logged password does not log in: %v", err)
	}

	// Second boot is a no-op: no new account, no new password printed.
	f.log.Reset()
	if err := f.svc.Bootstrap(ctx); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	users, err := f.users.ListAdminUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || f.log.Len() != 0 {
		t.Errorf("second Bootstrap created %d users, logged %q", len(users), f.log.String())
	}
}

func TestAuth_Bootstrap_FileMode(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "initial-admin-password.txt")
	f.svc.SetBootstrapPasswordFile(path)
	if err := f.svc.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("password file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("password file mode %o, want 600", perm)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := bootstrapPassword.FindStringSubmatch(string(content))
	if m == nil {
		t.Fatalf("no password in file")
	}
	if strings.Contains(f.log.String(), m[1]) {
		t.Error("password written to the log despite a password file being configured")
	}
	if _, err := f.svc.Login(ctx, "admin", m[1]); err != nil {
		t.Fatalf("file password does not log in: %v", err)
	}
}

func TestAuth_Bootstrap_UnwritableFileFallsBackToLog(t *testing.T) {
	f := newAuthFixture(t)
	f.svc.SetBootstrapPasswordFile(filepath.Join(t.TempDir(), "missing-dir", "pw.txt"))
	if err := f.svc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if !strings.Contains(f.log.String(), "WARNING: could not write bootstrap password file") ||
		bootstrapPassword.FindStringSubmatch(f.log.String()) == nil {
		t.Fatalf("expected a warning and the password in the log, got %q", f.log.String())
	}
}

func TestAuth_ChangePassword_SuccessAndLimits(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	f.user(t, "uuid-c1", "erin", "admin")

	tooLong := strings.Repeat("x", 73)
	if err := f.svc.ChangePassword(ctx, "uuid-c1", lifecyclePass, tooLong); !errors.Is(err, service.ErrPasswordChangeFailed) {
		t.Errorf("73-char password: got %v, want ErrPasswordChangeFailed", err)
	}
	if err := f.svc.ChangePassword(ctx, "no-such-uuid", lifecyclePass, "a-valid-new-password"); err == nil {
		t.Error("unknown user: want an error")
	}

	const newPass = "a-valid-new-password"
	if err := f.svc.ChangePassword(ctx, "uuid-c1", lifecyclePass, newPass); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	rec, _ := f.users.GetAdminUserByUUID(ctx, "uuid-c1")
	if rec.MustChangePW {
		t.Error("must_change_pw not cleared")
	}
	if _, err := f.svc.Login(ctx, "erin", lifecyclePass); !errors.Is(err, service.ErrInvalidCredentials) {
		t.Errorf("old password still works: %v", err)
	}
	if _, err := f.svc.Login(ctx, "erin", newPass); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
}
