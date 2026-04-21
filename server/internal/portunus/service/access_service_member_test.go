package service_test

// Integration tests for the AccessService member_access + module_authorizations
// decision path introduced in PR 4.  These tests use an in-memory SQLite
// database with the production schema applied via the migration runner.

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// ── test helpers ──────────────────────────────────────────────────────────────

// seedModule inserts a minimal modules row to satisfy the FK constraint on
// module_authorizations.module_id.
func seedModule(t *testing.T, dbConn *sql.DB, moduleID string) {
	t.Helper()
	_, err := dbConn.Exec(`INSERT OR IGNORE INTO modules(module_id, enabled, created_at_ms, updated_at_ms) VALUES (?,1,0,0)`, moduleID)
	if err != nil {
		t.Fatalf("seedModule %q: %v", moduleID, err)
	}
}

func openSvcTestDB(t *testing.T) (*sql.DB, *db.Worker) {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:svc_test_%s?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)",
		t.Name(),
	)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("openSvcTestDB: %v", err)
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if err := db.Migrate(context.Background(), conn); err != nil {
		conn.Close()
		t.Fatalf("openSvcTestDB migrate: %v", err)
	}
	w := db.NewWorker(conn)
	t.Cleanup(func() {
		w.Close()
		conn.Close()
	})
	return conn, w
}

// newMemberAccessSvc builds an AccessService wired with the member_access path.
func newMemberAccessSvc(t *testing.T) (
	*service.AccessService,
	store.MemberAccessStore,
	store.ModuleAuthorizationStore,
) {
	t.Helper()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	moStore := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)

	deviceStore := memory.NewDeviceStore([]string{"module-a"})
	registry := service.NewDeviceRegistry(deviceStore)
	eventStore := memory.NewAccessEventStore()

	svc := service.NewAccessService(registry, service.AccessPolicy{}, eventStore)
	svc.SetMemberAccessStore(maStore)
	svc.SetModuleAuthStore(moStore)
	return svc, maStore, moStore
}

// seedActiveMember creates a member with an attached credential and an active
// module authorization. Returns the credential hash used.
func seedActiveMember(
	t *testing.T,
	ctx context.Context,
	maStore store.MemberAccessStore,
	moStore store.ModuleAuthorizationStore,
	memberUUID, moduleID string,
) []byte {
	t.Helper()
	credHash := make([]byte, 32)
	credHash[0] = 0xDE
	credHash[1] = 0xAD

	if err := maStore.CreateMember(ctx, memberUUID, "member", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if err := maStore.AttachCredential(ctx, memberUUID, credHash); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}
	if err := moStore.GrantAuthorization(ctx, memberUUID, moduleID, "", nil, ""); err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}
	return credHash
}

// credID encodes the raw credential identifier for HashCredentialID with an
// empty secret, matching the test service configuration.
func credIDFor(hash []byte) string {
	// The AccessService calls HashCredentialID(credentialID, secret) → hash.
	// With an empty secret, HashCredentialID does SHA-256(credentialID).
	// To produce the exact hash we inserted we need to produce the pre-image.
	// For tests, we bypass HashCredentialID by inserting the raw hash directly
	// and using a special helper; here we just return a placeholder and instead
	// inject the hash via the store — so this function is unused in the helpers.
	// The actual tests pass a credential_id and expect the access service to
	// hash it. Use seedActiveMemberByID below for a test that owns the full chain.
	_ = hash
	return ""
}

// ── access decision tests ─────────────────────────────────────────────────────

// TestAccessService_MemberPath_Granted verifies end-to-end grant when member is
// active, enabled, and has a non-revoked module authorization.
func TestAccessService_MemberPath_Granted(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	moStore := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)

	memberUUID := "aaaaaaaa-0000-4000-8000-000000000001"
	moduleID := "module-a"

	// Hash we'll store — must match what AccessService will compute.
	// Use credentialID "testcred" with empty secret → SHA-256("testcred").
	credHash := service.HashCredentialID("testcred", nil)

	seedModule(t, dbConn, moduleID)
	if err := maStore.CreateMember(ctx, memberUUID, "member", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if err := maStore.AttachCredential(ctx, memberUUID, credHash); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}
	if err := moStore.GrantAuthorization(ctx, memberUUID, moduleID, "", nil, ""); err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}

	deviceStore := memory.NewDeviceStore([]string{moduleID})
	registry := service.NewDeviceRegistry(deviceStore)
	eventStore := memory.NewAccessEventStore()
	svc := service.NewAccessService(registry, service.AccessPolicy{}, eventStore)
	svc.SetMemberAccessStore(maStore)
	svc.SetModuleAuthStore(moStore)

	resp, err := svc.Decide(ctx, types.AccessRequest{
		ModuleID:     moduleID,
		CredentialID: "testcred",
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !resp.Granted {
		t.Errorf("expected granted=true, got reason=%q", resp.Reason)
	}
	if resp.Reason != "credential_allowed" {
		t.Errorf("expected reason=credential_allowed, got %q", resp.Reason)
	}
}

func TestAccessService_MemberPath_NoAuthorization(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	moStore := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)

	memberUUID := "aaaaaaaa-0000-4000-8000-000000000002"
	credHash := service.HashCredentialID("testcred2", nil)

	if err := maStore.CreateMember(ctx, memberUUID, "member", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if err := maStore.AttachCredential(ctx, memberUUID, credHash); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}
	// No module authorization granted.

	deviceStore := memory.NewDeviceStore([]string{"module-a"})
	svc := service.NewAccessService(service.NewDeviceRegistry(deviceStore), service.AccessPolicy{}, memory.NewAccessEventStore())
	svc.SetMemberAccessStore(maStore)
	svc.SetModuleAuthStore(moStore)

	resp, err := svc.Decide(ctx, types.AccessRequest{ModuleID: "module-a", CredentialID: "testcred2"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Granted {
		t.Error("expected granted=false when no authorization exists")
	}
	if resp.Reason != "module_not_authorized" {
		t.Errorf("expected reason=module_not_authorized, got %q", resp.Reason)
	}
}

func TestAccessService_MemberPath_RevokedAuthorization(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	moStore := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)

	memberUUID := "aaaaaaaa-0000-4000-8000-000000000003"
	moduleID := "module-a"
	credHash := service.HashCredentialID("testcred3", nil)

	seedModule(t, dbConn, moduleID)
	if err := maStore.CreateMember(ctx, memberUUID, "member", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if err := maStore.AttachCredential(ctx, memberUUID, credHash); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}
	if err := moStore.GrantAuthorization(ctx, memberUUID, moduleID, "", nil, ""); err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}
	if err := moStore.RevokeAuthorization(ctx, memberUUID, moduleID, ""); err != nil {
		t.Fatalf("RevokeAuthorization: %v", err)
	}

	deviceStore := memory.NewDeviceStore([]string{moduleID})
	svc := service.NewAccessService(service.NewDeviceRegistry(deviceStore), service.AccessPolicy{}, memory.NewAccessEventStore())
	svc.SetMemberAccessStore(maStore)
	svc.SetModuleAuthStore(moStore)

	resp, err := svc.Decide(ctx, types.AccessRequest{ModuleID: moduleID, CredentialID: "testcred3"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Granted {
		t.Error("expected granted=false for revoked authorization")
	}
	// After revoke the row exists but revoked_at_ms is set; GetAuthorization
	// returns the most recent row. The decideMemberAccess path checks RevokedAt.
	if resp.Reason != "authorization_revoked" && resp.Reason != "module_not_authorized" {
		t.Errorf("unexpected reason %q", resp.Reason)
	}
}

func TestAccessService_MemberPath_ExpiredAuthorization(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	moStore := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)

	memberUUID := "aaaaaaaa-0000-4000-8000-000000000004"
	moduleID := "module-a"
	credHash := service.HashCredentialID("testcred4", nil)

	pastExpiry := time.Now().UTC().Add(-24 * time.Hour)

	seedModule(t, dbConn, moduleID)
	if err := maStore.CreateMember(ctx, memberUUID, "member", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if err := maStore.AttachCredential(ctx, memberUUID, credHash); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}
	if err := moStore.GrantAuthorization(ctx, memberUUID, moduleID, "", &pastExpiry, ""); err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}

	deviceStore := memory.NewDeviceStore([]string{moduleID})
	svc := service.NewAccessService(service.NewDeviceRegistry(deviceStore), service.AccessPolicy{}, memory.NewAccessEventStore())
	svc.SetMemberAccessStore(maStore)
	svc.SetModuleAuthStore(moStore)

	resp, err := svc.Decide(ctx, types.AccessRequest{ModuleID: moduleID, CredentialID: "testcred4"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Granted {
		t.Error("expected granted=false for expired authorization")
	}
	if resp.Reason != "authorization_expired" {
		t.Errorf("expected reason=authorization_expired, got %q", resp.Reason)
	}
}

func TestAccessService_MemberPath_ExpiredMember(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	moStore := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)

	memberUUID := "aaaaaaaa-0000-4000-8000-000000000005"
	moduleID := "module-a"
	credHash := service.HashCredentialID("testcred5", nil)

	seedModule(t, dbConn, moduleID)
	if err := maStore.CreateMember(ctx, memberUUID, "member", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if err := maStore.AttachCredential(ctx, memberUUID, credHash); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}
	if err := moStore.GrantAuthorization(ctx, memberUUID, moduleID, "", nil, ""); err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}
	// Manually transition member to expired.
	if err := maStore.SetStatus(ctx, memberUUID, store.MemberStatusExpired); err != nil {
		t.Fatalf("SetStatus expired: %v", err)
	}

	deviceStore := memory.NewDeviceStore([]string{moduleID})
	svc := service.NewAccessService(service.NewDeviceRegistry(deviceStore), service.AccessPolicy{}, memory.NewAccessEventStore())
	svc.SetMemberAccessStore(maStore)
	svc.SetModuleAuthStore(moStore)

	resp, err := svc.Decide(ctx, types.AccessRequest{ModuleID: moduleID, CredentialID: "testcred5"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Granted {
		t.Error("expected granted=false for expired member")
	}
	if resp.Reason != "member_expired" {
		t.Errorf("expected reason=member_expired, got %q", resp.Reason)
	}
}

func TestAccessService_MemberPath_DisabledMember(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	moStore := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)

	memberUUID := "aaaaaaaa-0000-4000-8000-000000000006"
	moduleID := "module-a"
	credHash := service.HashCredentialID("testcred6", nil)

	seedModule(t, dbConn, moduleID)
	if err := maStore.CreateMember(ctx, memberUUID, "member", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if err := maStore.AttachCredential(ctx, memberUUID, credHash); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}
	if err := moStore.GrantAuthorization(ctx, memberUUID, moduleID, "", nil, ""); err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}
	if err := maStore.SetEnabled(ctx, memberUUID, false); err != nil {
		t.Fatalf("SetEnabled false: %v", err)
	}

	deviceStore := memory.NewDeviceStore([]string{moduleID})
	svc := service.NewAccessService(service.NewDeviceRegistry(deviceStore), service.AccessPolicy{}, memory.NewAccessEventStore())
	svc.SetMemberAccessStore(maStore)
	svc.SetModuleAuthStore(moStore)

	resp, err := svc.Decide(ctx, types.AccessRequest{ModuleID: moduleID, CredentialID: "testcred6"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Granted {
		t.Error("expected granted=false for disabled member")
	}
	if resp.Reason != "member_disabled" {
		t.Errorf("expected reason=member_disabled, got %q", resp.Reason)
	}
}

func TestAccessService_MemberPath_UnknownCredential(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	moStore := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)

	deviceStore := memory.NewDeviceStore([]string{"module-a"})
	svc := service.NewAccessService(service.NewDeviceRegistry(deviceStore), service.AccessPolicy{}, memory.NewAccessEventStore())
	svc.SetMemberAccessStore(maStore)
	svc.SetModuleAuthStore(moStore)

	resp, err := svc.Decide(ctx, types.AccessRequest{ModuleID: "module-a", CredentialID: "nobody"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Granted {
		t.Error("expected granted=false for unknown credential")
	}
	if resp.Reason != "credential_not_found" {
		t.Errorf("expected reason=credential_not_found, got %q", resp.Reason)
	}
}

// TestAccessService_MemberPath_UpdatesLastAccess verifies that a granted
// decision updates last_access_at_ms on the member record.
func TestAccessService_MemberPath_UpdatesLastAccess(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	moStore := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)

	memberUUID := "aaaaaaaa-0000-4000-8000-000000000007"
	moduleID := "module-a"
	credHash := service.HashCredentialID("testcred7", nil)

	seedModule(t, dbConn, moduleID)
	if err := maStore.CreateMember(ctx, memberUUID, "member", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if err := maStore.AttachCredential(ctx, memberUUID, credHash); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}
	if err := moStore.GrantAuthorization(ctx, memberUUID, moduleID, "", nil, ""); err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}

	before, _ := maStore.GetMember(ctx, memberUUID)
	if before.LastAccessAt != nil {
		t.Error("expected last_access_at to be nil before first access")
	}

	deviceStore := memory.NewDeviceStore([]string{moduleID})
	svc := service.NewAccessService(service.NewDeviceRegistry(deviceStore), service.AccessPolicy{}, memory.NewAccessEventStore())
	svc.SetMemberAccessStore(maStore)
	svc.SetModuleAuthStore(moStore)

	resp, err := svc.Decide(ctx, types.AccessRequest{ModuleID: moduleID, CredentialID: "testcred7"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !resp.Granted {
		t.Fatalf("expected granted=true")
	}

	after, err := maStore.GetMember(ctx, memberUUID)
	if err != nil {
		t.Fatalf("GetMember after access: %v", err)
	}
	if after.LastAccessAt == nil {
		t.Error("expected last_access_at to be set after granted access")
	}
}

// ── expiry worker tests ───────────────────────────────────────────────────────

func TestExpiryWorker_HardDeadlineSweep(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)

	past := time.Now().UTC().Add(-1 * time.Hour)
	future := time.Now().UTC().Add(24 * time.Hour)

	// Expired by hard deadline.
	if err := maStore.CreateMember(ctx, "exp-001", "member", "", store.ProvisioningStatusActive, &past, nil); err != nil {
		t.Fatalf("CreateMember exp-001: %v", err)
	}
	// Not yet expired.
	if err := maStore.CreateMember(ctx, "exp-002", "member", "", store.ProvisioningStatusActive, &future, nil); err != nil {
		t.Fatalf("CreateMember exp-002: %v", err)
	}
	// No deadline — never expires via hard sweep.
	if err := maStore.CreateMember(ctx, "exp-003", "member", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember exp-003: %v", err)
	}

	n, err := maStore.ExpireByHardDeadline(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("ExpireByHardDeadline: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row expired, got %d", n)
	}

	rec, _ := maStore.GetMember(ctx, "exp-001")
	if rec.Status != store.MemberStatusExpired {
		t.Errorf("exp-001 status: want expired, got %s", rec.Status)
	}
	rec, _ = maStore.GetMember(ctx, "exp-002")
	if rec.Status != store.MemberStatusActive {
		t.Errorf("exp-002 status: want active, got %s", rec.Status)
	}
}

func TestExpiryWorker_InactivitySweep(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)

	// Member with 1-day inactivity limit, last seen 2 days ago → should expire.
	inact1 := 1
	if err := maStore.CreateMember(ctx, "inact-001", "member", "", store.ProvisioningStatusActive, nil, &inact1); err != nil {
		t.Fatalf("CreateMember inact-001: %v", err)
	}
	twoDaysAgo := time.Now().UTC().Add(-48 * time.Hour)
	if err := maStore.UpdateLastAccess(ctx, "inact-001", twoDaysAgo); err != nil {
		t.Fatalf("UpdateLastAccess: %v", err)
	}

	// Member with 30-day inactivity limit, last seen yesterday → should NOT expire.
	inact30 := 30
	if err := maStore.CreateMember(ctx, "inact-002", "member", "", store.ProvisioningStatusActive, nil, &inact30); err != nil {
		t.Fatalf("CreateMember inact-002: %v", err)
	}
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	if err := maStore.UpdateLastAccess(ctx, "inact-002", yesterday); err != nil {
		t.Fatalf("UpdateLastAccess: %v", err)
	}

	n, err := maStore.ExpireByInactivity(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("ExpireByInactivity: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row expired by inactivity, got %d", n)
	}

	rec, _ := maStore.GetMember(ctx, "inact-001")
	if rec.Status != store.MemberStatusExpired {
		t.Errorf("inact-001 status: want expired, got %s", rec.Status)
	}
	rec, _ = maStore.GetMember(ctx, "inact-002")
	if rec.Status != store.MemberStatusActive {
		t.Errorf("inact-002 status: want active, got %s", rec.Status)
	}
}

func TestExpiryWorker_RunsOnStart(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)

	past := time.Now().UTC().Add(-1 * time.Hour)
	if err := maStore.CreateMember(ctx, "worker-001", "member", "", store.ProvisioningStatusActive, &past, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	worker := service.NewExpiryWorker(maStore, service.ExpiryWorkerConfig{IntervalMinutes: 60}, silentLogger())
	workerCtx, cancel := context.WithCancel(ctx)
	worker.Start(workerCtx)
	// Give the goroutine a moment to run the initial sweep.
	time.Sleep(50 * time.Millisecond)
	cancel()
	worker.Stop()

	rec, err := maStore.GetMember(ctx, "worker-001")
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if rec.Status != store.MemberStatusExpired {
		t.Errorf("expected worker-001 to be expired by worker sweep, got %s", rec.Status)
	}
}

// ── credential uniqueness tests ───────────────────────────────────────────────

func TestMemberAccessService_AttachCredential_DuplicateActive(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	roleStore := sqlitestore.NewRoleStore(dbConn, writer)

	svc := service.NewMemberAccessService(maStore, roleStore)

	// Provision first member and attach a credential.
	m1, err := svc.ProvisionMember(ctx, "member", "", nil, nil)
	if err != nil {
		t.Fatalf("ProvisionMember m1: %v", err)
	}
	credHash := service.HashCredentialID("sharedcred", nil)
	if err := svc.AttachCredential(ctx, m1.UUID, credHash); err != nil {
		t.Fatalf("AttachCredential m1: %v", err)
	}
	// Mark as active provisioning status to pass the active check.
	if err := maStore.SetProvisioningStatus(ctx, m1.UUID, store.ProvisioningStatusActive); err != nil {
		t.Fatalf("SetProvisioningStatus: %v", err)
	}

	// Provision second member and try to attach the same credential.
	m2, err := svc.ProvisionMember(ctx, "member", "", nil, nil)
	if err != nil {
		t.Fatalf("ProvisionMember m2: %v", err)
	}
	err = svc.AttachCredential(ctx, m2.UUID, credHash)
	if err == nil {
		t.Fatal("expected duplicate credential error, got nil")
	}
	if err != service.ErrDuplicateCredentialActive && err != service.ErrDuplicateCredentialPending {
		t.Errorf("expected ErrDuplicateCredentialActive or ErrDuplicateCredentialPending, got %v", err)
	}
}

func TestMemberAccessService_AttachCredential_DuplicateInactive(t *testing.T) {
	ctx := context.Background()
	dbConn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(dbConn, writer)
	roleStore := sqlitestore.NewRoleStore(dbConn, writer)

	svc := service.NewMemberAccessService(maStore, roleStore)

	m1, err := svc.ProvisionMember(ctx, "member", "", nil, nil)
	if err != nil {
		t.Fatalf("ProvisionMember: %v", err)
	}
	credHash := service.HashCredentialID("expiredcred", nil)
	if err := svc.AttachCredential(ctx, m1.UUID, credHash); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}
	// Expire the first member.
	if err := maStore.SetStatus(ctx, m1.UUID, store.MemberStatusExpired); err != nil {
		t.Fatalf("SetStatus expired: %v", err)
	}

	m2, err := svc.ProvisionMember(ctx, "member", "", nil, nil)
	if err != nil {
		t.Fatalf("ProvisionMember m2: %v", err)
	}
	err = svc.AttachCredential(ctx, m2.UUID, credHash)
	if err != service.ErrDuplicateCredentialInactive {
		t.Errorf("expected ErrDuplicateCredentialInactive, got %v", err)
	}
}
