package httpapi_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/httpapi"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
	"github.com/BrandonDHaskell/Portunus/server/internal/replay"
)

// testCredentialHashSecret is non-empty so credential tests exercise the keyed
// HMAC path that production uses (hazard #1).
var testCredentialHashSecret = []byte("httpapi-test-credential-hash-secret")

type harnessOptions struct {
	HMACSecret   string
	ReplayWindow time.Duration // >0 wires a replay store
	TLSEnabled   bool
	AllowAll     bool
}

// harness is a full HTTP server wired the same way cmd/portunus-server/main.go
// wires it, backed by in-memory SQLite with the real migrations applied.
type harness struct {
	t      *testing.T
	TS     *httptest.Server
	Conn   *sql.DB
	Admin  *http.Cookie // bootstrapped "admin" account, must_change_pw cleared
	Access *service.AccessService

	AdminUsers  *sqlitestore.AdminUserStore
	Roles       *sqlitestore.RoleStore
	Members     *sqlitestore.MemberAccessStore
	ModuleAuths *sqlitestore.ModuleAuthorizationStore
	Audit       *sqlitestore.AuditStore
	Auth        *service.AuthService
	Heartbeat   *service.HeartbeatService
	Provision   *service.ProvisionService
}

func newHarness(t *testing.T, opts harnessOptions) *harness {
	t.Helper()
	ctx := context.Background()

	conn := openAdminDB(t)
	writer := db.NewWorker(conn)
	t.Cleanup(func() { writer.Close() })
	logger := log.New(io.Discard, "", 0)

	deviceStore := sqlitestore.NewDeviceStore(conn, writer)
	heartbeatStore := sqlitestore.NewHeartbeatStore(conn, writer)
	accessEventStore := sqlitestore.NewAccessEventStore(conn, writer)
	moduleAdminStore := sqlitestore.NewModuleAdminStore(conn, writer)
	adminUserStore := sqlitestore.NewAdminUserStore(conn, writer)
	sessionStore := sqlitestore.NewSessionStore(conn, writer)
	roleStore := sqlitestore.NewRoleStore(conn, writer)
	memberStore := sqlitestore.NewMemberAccessStore(conn, writer)
	moduleAuthStore := sqlitestore.NewModuleAuthorizationStore(conn, writer)
	auditStore := sqlitestore.NewAuditStore(conn, writer)

	registry := service.NewDeviceRegistry(deviceStore)
	accessSvc := service.NewAccessService(registry, service.AccessPolicy{AllowAll: opts.AllowAll}, accessEventStore)
	accessSvc.SetCredentialHashSecret(testCredentialHashSecret)
	accessSvc.SetMemberAccessStore(memberStore)
	accessSvc.SetModuleAuthStore(moduleAuthStore)
	if err := accessSvc.Validate(); err != nil {
		t.Fatalf("access Validate: %v", err)
	}

	authSvc := service.NewAuthService(adminUserStore, sessionStore, roleStore, logger)
	if err := authSvc.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	var replayStore *replay.Store
	if opts.ReplayWindow > 0 {
		replayStore = replay.NewStore(opts.ReplayWindow)
	}

	heartbeatSvc := service.NewHeartbeatService(heartbeatStore, registry)
	provisionSvc := service.NewProvisionService(registry, memberStore, accessEventStore, testCredentialHashSecret, auditStore)

	srv := httpapi.NewServer(httpapi.Dependencies{
		Logger:               logger,
		Addr:                 ":0",
		HeartbeatService:     heartbeatSvc,
		AccessService:        accessSvc,
		ProvisionService:     provisionSvc,
		AdminService:         service.NewAdminService(moduleAdminStore, testCredentialHashSecret),
		AuthService:          authSvc,
		AdminUserService:     service.NewAdminUserService(adminUserStore, roleStore, auditStore),
		RoleService:          service.NewRoleService(roleStore),
		MemberAccessService:  service.NewMemberAccessService(memberStore),
		ModuleAuthService:    service.NewModuleAuthorizationService(moduleAuthStore, memberStore, auditStore),
		AuditStore:           auditStore,
		HMACSecret:           opts.HMACSecret,
		ReplayStore:          replayStore,
		CredentialHashSecret: testCredentialHashSecret,
		TLSEnabled:           opts.TLSEnabled,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	h := &harness{
		t: t, TS: ts, Conn: conn, Access: accessSvc,
		AdminUsers: adminUserStore, Roles: roleStore, Members: memberStore,
		ModuleAuths: moduleAuthStore, Audit: auditStore, Auth: authSvc,
		Heartbeat: heartbeatSvc, Provision: provisionSvc,
	}
	h.Admin = h.loginAs("admin")
	return h
}

// loginAs resets username's password to a known value (which also clears
// must_change_pw) and returns a session cookie.
func (h *harness) loginAs(username string) *http.Cookie {
	h.t.Helper()
	ctx := context.Background()
	user, err := h.AdminUsers.GetAdminUserByUsername(ctx, username)
	if err != nil {
		h.t.Fatalf("loginAs %q: %v", username, err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(testAdminPassword), bcrypt.MinCost)
	if err != nil {
		h.t.Fatalf("bcrypt: %v", err)
	}
	if err := h.AdminUsers.UpdatePasswordHash(ctx, user.UUID, string(hash)); err != nil {
		h.t.Fatalf("set password: %v", err)
	}
	sid, err := h.Auth.Login(ctx, username, testAdminPassword)
	if err != nil {
		h.t.Fatalf("login %q: %v", username, err)
	}
	return &http.Cookie{Name: "portunus_session", Value: sid}
}

// sessionWith creates a fresh role holding exactly perms, an admin user in that
// role, and returns a logged-in session cookie for it.
func (h *harness) sessionWith(perms []string, mustChangePW bool) *http.Cookie {
	h.t.Helper()
	ctx := context.Background()
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(h.t.Name())
	roleID := fmt.Sprintf("r_%s_%d", safe, roleSeq.Add(1))
	if err := h.Roles.CreateRole(ctx, roleID, roleID, ""); err != nil {
		h.t.Fatalf("CreateRole: %v", err)
	}
	if err := h.Roles.SetRolePermissions(ctx, roleID, perms); err != nil {
		h.t.Fatalf("SetRolePermissions: %v", err)
	}
	username := "u_" + roleID
	if err := h.AdminUsers.CreateAdminUser(ctx, "uuid-"+roleID, username, "x", roleID); err != nil {
		h.t.Fatalf("CreateAdminUser: %v", err)
	}
	c := h.loginAs(username)
	if mustChangePW {
		if err := h.AdminUsers.SetMustChangePW(ctx, "uuid-"+roleID, true); err != nil {
			h.t.Fatalf("SetMustChangePW: %v", err)
		}
	}
	return c
}

var roleSeq atomic.Int64

// seedKnownModule inserts a commissioned, enabled module so DeviceRegistry
// reports it as known.
func (h *harness) seedKnownModule(moduleID string) {
	h.t.Helper()
	_, err := h.Conn.Exec(`INSERT INTO modules(module_id, enabled, commissioned_at_ms, created_at_ms, updated_at_ms) VALUES (?,1,1,0,0)`, moduleID)
	if err != nil {
		h.t.Fatalf("seedKnownModule %q: %v", moduleID, err)
	}
}

// seedActiveMember creates an active, enabled member with no credential and
// returns its UUID.
func (h *harness) seedActiveMember(memberUUID string) string {
	h.t.Helper()
	if err := h.Members.CreateMember(context.Background(), memberUUID, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		h.t.Fatalf("CreateMember: %v", err)
	}
	return memberUUID
}

func (h *harness) grant(memberUUID, moduleID string) {
	h.t.Helper()
	if err := h.ModuleAuths.GrantAuthorization(context.Background(), memberUUID, moduleID, "", nil, ""); err != nil {
		h.t.Fatalf("GrantAuthorization: %v", err)
	}
}

// decide runs the door-side access decision exactly as a device request would
// reach the service: the colon-formatted UID string the firmware sends.
func (h *harness) decide(moduleID, credentialID string) types.AccessResponse {
	h.t.Helper()
	resp, err := h.Access.Decide(context.Background(), types.AccessRequest{ModuleID: moduleID, CredentialID: credentialID})
	if err != nil {
		h.t.Fatalf("Decide: %v", err)
	}
	return resp
}

// noRedirectClient returns a client that surfaces 3xx responses instead of
// following them, for UI handler assertions.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// postForm submits an HTML form with the given session cookie and returns the
// unfollowed response.
func (h *harness) postForm(path string, form url.Values, cookie *http.Cookie) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.TS.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		h.t.Fatalf("postForm: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		h.t.Fatalf("postForm do: %v", err)
	}
	return resp
}

// seedPEU inserts a commissioned provisioning & enrollment unit.
func (h *harness) seedPEU(moduleID string) {
	h.t.Helper()
	_, err := h.Conn.Exec(`INSERT INTO modules(module_id, enabled, commissioned_at_ms, module_type, created_at_ms, updated_at_ms) VALUES (?,1,1,'provisioning_enrollment_unit',0,0)`, moduleID)
	if err != nil {
		h.t.Fatalf("seedPEU %q: %v", moduleID, err)
	}
}

// enroll attaches the canonical hash of a colon UID to memberUUID, the same
// hash every enrollment path produces.
func (h *harness) enroll(memberUUID, uid string) {
	h.t.Helper()
	raw, err := service.ParseCredentialUID(uid)
	if err != nil {
		h.t.Fatalf("ParseCredentialUID(%q): %v", uid, err)
	}
	if err := h.Members.AttachCredential(context.Background(), memberUUID, service.HashCredentialID(raw, testCredentialHashSecret)); err != nil {
		h.t.Fatalf("AttachCredential: %v", err)
	}
}
