package service_test

// Integration tests for ProvisionService.
//
// Path 1 (capture): PEU + no operator UID → pending_authorization created.
// Path 2 (operator enrolment): PEU + operator UID + flag enabled → active member.
//
// Gate checks:
//   - ACU module → unauthorized (PEU gate)
//   - operator provisioning disabled → unauthorized even with operator UID
//   - unknown operator → hard-blocked, NO pending record created
//   - pending / inactive / disabled operator → unauthorized
//   - operator missing member.provision → unauthorized

import (
	"context"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

var testSecret = []byte("test-secret-key-32-bytes-padding!!")

const testModuleID = "prov-module-001"

// newProvisionSvc builds a ProvisionService backed by real SQLite.
// operatorProvisioningEnabled controls Path 2.
func newProvisionSvc(t *testing.T, operatorProvisioningEnabled bool) (
	*service.ProvisionService,
	store.MemberAccessStore,
	store.RoleStore,
	*memory.AccessEventStore,
	store.ModuleAuthorizationStore,
) {
	t.Helper()
	conn, writer := openSvcTestDB(t)
	seedModule(t, conn, testModuleID)

	maStore := sqlitestore.NewMemberAccessStore(conn, writer)
	mauthStore := sqlitestore.NewModuleAuthorizationStore(conn, writer)
	roleStore := sqlitestore.NewRoleStore(conn, writer)
	eventStore := memory.NewAccessEventStore()
	registry := service.NewDeviceRegistry(memory.NewDeviceStore([]string{testModuleID}))

	svc := service.NewProvisionService(
		registry, maStore, mauthStore, roleStore, eventStore,
		testSecret, operatorProvisioningEnabled, nil,
	)
	return svc, maStore, roleStore, eventStore, mauthStore
}

func seedOperatorMember(
	t *testing.T,
	maStore store.MemberAccessStore,
	rawUID []byte,
	roleID string,
	enabled bool,
	active bool,
) string {
	t.Helper()
	ctx := context.Background()
	memberUUID := "op-" + string(rawUID)
	provStatus := store.ProvisioningStatusActive
	if !active {
		provStatus = store.ProvisioningStatusPendingAuthorization
	}
	if err := maStore.CreateMember(ctx, memberUUID, roleID, "", provStatus, nil, nil); err != nil {
		t.Fatalf("seedOperatorMember CreateMember: %v", err)
	}
	if !enabled {
		if err := maStore.SetEnabled(ctx, memberUUID, false); err != nil {
			t.Fatalf("seedOperatorMember SetEnabled: %v", err)
		}
	}
	credHash := service.HashCredentialID(rawUID, testSecret)
	if err := maStore.AttachCredential(ctx, memberUUID, credHash); err != nil {
		t.Fatalf("seedOperatorMember AttachCredential: %v", err)
	}
	return memberUUID
}

func grantProvisionPermission(t *testing.T, roleStore store.RoleStore, roleID string) {
	t.Helper()
	if err := roleStore.SetRolePermissions(context.Background(), roleID, []string{permissions.MemberProvision}); err != nil {
		t.Fatalf("grantProvisionPermission: %v", err)
	}
}

func captureReq(credUID []byte) types.ProvisionCredentialRequest {
	return types.ProvisionCredentialRequest{
		ModuleID:      testModuleID,
		CredentialUID: credUID,
	}
}

func enrollReq(operatorUID, credUID []byte) types.ProvisionCredentialRequest {
	return types.ProvisionCredentialRequest{
		OperatorCredentialUID: operatorUID,
		ModuleID:              testModuleID,
		CredentialUID:         credUID,
		RoleID:                "guest",
	}
}

// ── PEU gate ──────────────────────────────────────────────────────────────────

// TestProvision_ACUModule_Blocked uses a real sqlite device store so that the
// module_type column is checked.  The module must be commissioned (enabled +
// commissioned_at_ms set) so that IsKnown returns true; only then does the PEU
// gate fire and return unauthorized.
func TestProvision_ACUModule_Blocked(t *testing.T) {
	conn, writer := openSvcTestDB(t)

	// Insert a commissioned ACU module (default module_type = 'access_control_unit').
	now := time.Now().UTC().UnixMilli()
	if _, err := conn.Exec(`
INSERT INTO modules(module_id, enabled, commissioned_at_ms, created_at_ms, updated_at_ms)
VALUES ('acu-module', 1, ?, ?, ?)`, now, now, now); err != nil {
		t.Fatalf("insert acu-module: %v", err)
	}

	maStore := sqlitestore.NewMemberAccessStore(conn, writer)
	roleStore := sqlitestore.NewRoleStore(conn, writer)
	eventStore := memory.NewAccessEventStore()
	deviceStore := sqlitestore.NewDeviceStore(conn, writer)
	registry := service.NewDeviceRegistry(deviceStore)

	svc := service.NewProvisionService(
		registry, maStore, nil, roleStore, eventStore,
		testSecret, false, nil,
	)

	req := types.ProvisionCredentialRequest{
		ModuleID:      "acu-module",
		CredentialUID: []byte{0x04, 0x01, 0x02, 0x03},
	}
	resp, err := svc.Provision(context.Background(), req)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized for ACU module, got status=%q detail=%q", resp.Status, resp.Detail)
	}
}

// ── Path 1: capture (no operator UID) ─────────────────────────────────────────

func TestProvision_Capture_CreatesNewPendingMember(t *testing.T) {
	svc, maStore, _, _, _ := newProvisionSvc(t, false)
	credUID := []byte{0x04, 0xAA, 0xBB, 0xCC}

	resp, err := svc.Provision(context.Background(), captureReq(credUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusPendingCreated {
		t.Errorf("expected pending_created, got status=%q detail=%q", resp.Status, resp.Detail)
	}
	if resp.MemberUUID == "" {
		t.Error("expected non-empty MemberUUID")
	}

	rec, err := maStore.GetMember(context.Background(), resp.MemberUUID)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if rec.ProvisioningStatus != store.ProvisioningStatusPendingAuthorization {
		t.Errorf("provisioning_status = %q, want pending_authorization", rec.ProvisioningStatus)
	}
}

func TestProvision_Capture_DuplicateScan_ReturnsDuplicatePending(t *testing.T) {
	svc, _, _, _, _ := newProvisionSvc(t, false)
	credUID := []byte{0x04, 0x11, 0x22, 0x33}

	resp1, err := svc.Provision(context.Background(), captureReq(credUID))
	if err != nil || resp1.Status != types.ProvisionStatusPendingCreated {
		t.Fatalf("first capture failed: err=%v status=%q", err, resp1.Status)
	}

	resp2, err := svc.Provision(context.Background(), captureReq(credUID))
	if err != nil {
		t.Fatalf("second capture: %v", err)
	}
	if resp2.Status != types.ProvisionStatusDuplicatePending {
		t.Errorf("expected duplicate_pending on second scan, got %q", resp2.Status)
	}
}

// ── Path 2: operator enrolment disabled ───────────────────────────────────────

func TestProvision_OperatorEnroll_DisabledByFlag_Unauthorized(t *testing.T) {
	svc, _, _, _, _ := newProvisionSvc(t, false /* disabled */)
	operatorUID := []byte{0x04, 0xDE, 0xAD, 0xBE}
	memberUID := []byte{0x04, 0x99, 0x88, 0x77}

	resp, err := svc.Provision(context.Background(), enrollReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized when flag off, got status=%q detail=%q", resp.Status, resp.Detail)
	}
}

// ── Path 2: operator not found — hard block, no record created ─────────────────

func TestProvision_OperatorNotFound_HardBlocked_NoPendingCreated(t *testing.T) {
	svc, maStore, _, _, _ := newProvisionSvc(t, true)
	operatorUID := []byte{0x04, 0xFF, 0xFF, 0xFF}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}

	resp, err := svc.Provision(context.Background(), enrollReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized, got %q", resp.Status)
	}

	// No pending record must have been created for the unknown operator.
	pending, err := maStore.ListPendingAuthorizations(context.Background())
	if err != nil {
		t.Fatalf("ListPendingAuthorizations: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending records after hard block, got %d", len(pending))
	}
}

// ── Path 2: operator found but inactive/no-permission ─────────────────────────

func TestProvision_OperatorPending_Unauthorized(t *testing.T) {
	svc, maStore, _, eventStore, _ := newProvisionSvc(t, true)
	operatorUID := []byte{0x04, 0x11, 0x22, 0x33}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}
	// active=false → provisioning_status = pending_authorization; inactive check fires before perm check
	seedOperatorMember(t, maStore, operatorUID, "operator", true, false)

	resp, err := svc.Provision(context.Background(), enrollReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized for pending operator, got %q", resp.Status)
	}
	if n := len(eventStore.Events()); n != 1 {
		t.Errorf("expected 1 denied event, got %d", n)
	}
}

func TestProvision_OperatorDisabled_Unauthorized(t *testing.T) {
	svc, maStore, roleStore, eventStore, _ := newProvisionSvc(t, true)
	operatorUID := []byte{0x04, 0x44, 0x55, 0x66}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}
	grantProvisionPermission(t, roleStore, "operator")
	seedOperatorMember(t, maStore, operatorUID, "operator", false /* disabled */, true)

	resp, err := svc.Provision(context.Background(), enrollReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized for disabled operator, got %q", resp.Status)
	}
	if n := len(eventStore.Events()); n != 1 {
		t.Errorf("expected 1 denied event, got %d", n)
	}
}

func TestProvision_OperatorNoProvisionPermission_Unauthorized(t *testing.T) {
	svc, maStore, _, eventStore, _ := newProvisionSvc(t, true)
	operatorUID := []byte{0x04, 0xAA, 0xBB, 0xCC}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}
	// guest role has no permissions by default
	seedOperatorMember(t, maStore, operatorUID, "guest", true, true)

	resp, err := svc.Provision(context.Background(), enrollReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized, got %q detail=%q", resp.Status, resp.Detail)
	}
	if n := len(eventStore.Events()); n != 1 {
		t.Errorf("expected 1 denied event, got %d", n)
	}
}

// ── Path 2: success ───────────────────────────────────────────────────────────

func TestProvision_OperatorEnroll_Success(t *testing.T) {
	svc, maStore, roleStore, _, _ := newProvisionSvc(t, true)
	operatorUID := []byte{0x04, 0xDE, 0xAD, 0xBE}
	memberUID := []byte{0x04, 0x99, 0x88, 0x77}
	grantProvisionPermission(t, roleStore, "operator")
	opMemberUUID := seedOperatorMember(t, maStore, operatorUID, "operator", true, true)

	resp, err := svc.Provision(context.Background(), enrollReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusSuccess {
		t.Errorf("expected success, got status=%q detail=%q", resp.Status, resp.Detail)
	}
	if resp.MemberUUID == "" {
		t.Error("expected non-empty MemberUUID on success")
	}

	credHash := service.HashCredentialID(memberUID, testSecret)
	newMember, err := maStore.GetMemberByCredential(context.Background(), credHash)
	if err != nil {
		t.Fatalf("GetMemberByCredential: %v", err)
	}
	if newMember.CreatedByUUID != opMemberUUID {
		t.Errorf("created_by_uuid = %q, want %q", newMember.CreatedByUUID, opMemberUUID)
	}
	if newMember.ProvisioningStatus != store.ProvisioningStatusActive {
		t.Errorf("provisioning_status = %q, want active", newMember.ProvisioningStatus)
	}
}

// ── role defaults applied on enrolment ────────────────────────────────────────

func TestProvision_OperatorEnroll_RoleDefaultsApplied(t *testing.T) {
	conn, writer := openSvcTestDB(t)
	seedModule(t, conn, testModuleID)

	maStore := sqlitestore.NewMemberAccessStore(conn, writer)
	roleStore := sqlitestore.NewRoleStore(conn, writer)
	registry := service.NewDeviceRegistry(memory.NewDeviceStore([]string{testModuleID}))

	// Create a role with a default expiry of 30 days.
	expiryDays := 30
	ctx := context.Background()
	if err := roleStore.CreateRole(ctx, "enrolled", "Enrolled", "", &expiryDays, nil); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if err := roleStore.SetRolePermissions(ctx, "operator", []string{permissions.MemberProvision}); err != nil {
		t.Fatalf("SetRolePermissions: %v", err)
	}

	mauthStore := sqlitestore.NewModuleAuthorizationStore(conn, writer)
	svc := service.NewProvisionService(
		registry, maStore, mauthStore, roleStore, memory.NewAccessEventStore(),
		testSecret, true, nil,
	)

	operatorUID := []byte{0x04, 0xDE, 0xAD, 0xFF}
	memberUID := []byte{0x04, 0x12, 0x34, 0x56}
	opUUID := seedOperatorMember(t, maStore, operatorUID, "operator", true, true)
	_ = opUUID

	req := types.ProvisionCredentialRequest{
		OperatorCredentialUID: operatorUID,
		ModuleID:              testModuleID,
		CredentialUID:         memberUID,
		RoleID:                "enrolled",
	}
	resp, err := svc.Provision(ctx, req)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusSuccess {
		t.Fatalf("expected success, got %q: %s", resp.Status, resp.Detail)
	}

	credHash := service.HashCredentialID(memberUID, testSecret)
	rec, err := maStore.GetMemberByCredential(ctx, credHash)
	if err != nil {
		t.Fatalf("GetMemberByCredential: %v", err)
	}
	if rec.ExpiresAt == nil {
		t.Error("expected ExpiresAt to be set from role default, got nil")
	}
}

// ── authorization copy ────────────────────────────────────────────────────────

// TestProvision_OperatorEnroll_CopiesModuleAuthorizations verifies that the
// new member is granted access to every module the operator currently has an
// active (non-revoked) authorization on.
func TestProvision_OperatorEnroll_CopiesModuleAuthorizations(t *testing.T) {
	ctx := context.Background()

	conn, writer := openSvcTestDB(t)
	doorA, doorB := "door-alpha", "door-beta"
	seedModule(t, conn, testModuleID)
	seedModule(t, conn, doorA)
	seedModule(t, conn, doorB)

	maStore := sqlitestore.NewMemberAccessStore(conn, writer)
	mauthStore := sqlitestore.NewModuleAuthorizationStore(conn, writer)
	roleStore := sqlitestore.NewRoleStore(conn, writer)
	registry := service.NewDeviceRegistry(memory.NewDeviceStore([]string{testModuleID}))
	svc := service.NewProvisionService(
		registry, maStore, mauthStore, roleStore, memory.NewAccessEventStore(),
		testSecret, true, nil,
	)

	if err := roleStore.SetRolePermissions(ctx, "operator", []string{permissions.MemberProvision}); err != nil {
		t.Fatalf("SetRolePermissions: %v", err)
	}

	operatorUID := []byte{0x04, 0xDE, 0xAD, 0x01}
	memberUID := []byte{0x04, 0xDE, 0xAD, 0x02}
	opUUID := seedOperatorMember(t, maStore, operatorUID, "operator", true, true)

	if err := mauthStore.GrantAuthorization(ctx, opUUID, doorA, "", nil, ""); err != nil {
		t.Fatalf("grant %s to operator: %v", doorA, err)
	}
	if err := mauthStore.GrantAuthorization(ctx, opUUID, doorB, "", nil, ""); err != nil {
		t.Fatalf("grant %s to operator: %v", doorB, err)
	}

	resp, err := svc.Provision(ctx, enrollReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusSuccess {
		t.Fatalf("expected success, got status=%q detail=%q", resp.Status, resp.Detail)
	}

	auths, err := mauthStore.ListByMember(ctx, resp.MemberUUID)
	if err != nil {
		t.Fatalf("ListByMember new member: %v", err)
	}
	got := make(map[string]bool)
	for _, a := range auths {
		if a.RevokedAt == nil {
			got[a.ModuleID] = true
		}
	}
	for _, want := range []string{doorA, doorB} {
		if !got[want] {
			t.Errorf("new member missing authorization for module %q", want)
		}
	}
}
