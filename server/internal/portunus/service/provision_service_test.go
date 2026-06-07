package service_test

// Integration tests for ProvisionService operator-resolution logic.
//
// Operator (scan-1) resolution now goes through member_access, not admin_users:
//   - operator credential not in DB         → UNAUTHORIZED + pending_authorization created
//   - operator credential found, no perms   → UNAUTHORIZED + event recorded
//   - operator credential found, inactive   → UNAUTHORIZED + event recorded
//   - operator credential found, has perms  → provisioning succeeds

import (
	"context"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// testSecret is a fixed HMAC key used throughout these tests.
var testSecret = []byte("test-secret-key-32-bytes-padding!!")

// newProvisionSvc builds a ProvisionService backed by real migrated SQLite.
func newProvisionSvc(t *testing.T) (
	*service.ProvisionService,
	store.MemberAccessStore,
	store.RoleStore,
	*memory.AccessEventStore,
) {
	t.Helper()
	conn, writer := openSvcTestDB(t)
	seedModule(t, conn, "prov-module-001")

	maStore := sqlitestore.NewMemberAccessStore(conn, writer)
	roleStore := sqlitestore.NewRoleStore(conn, writer)
	eventStore := memory.NewAccessEventStore()
	registry := service.NewDeviceRegistry(memory.NewDeviceStore([]string{"prov-module-001"}))

	svc := service.NewProvisionService(registry, maStore, roleStore, eventStore, testSecret)
	return svc, maStore, roleStore, eventStore
}

// seedOperatorMember creates a member_access record with an attached credential
// and returns the member UUID. The role's permissions are set by the caller.
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

// grantProvisionPermission assigns member.provision to the given role.
func grantProvisionPermission(t *testing.T, roleStore store.RoleStore, roleID string) {
	t.Helper()
	if err := roleStore.SetRolePermissions(context.Background(), roleID, []string{permissions.MemberProvision}); err != nil {
		t.Fatalf("grantProvisionPermission: %v", err)
	}
}

func provisionReq(operatorUID, credUID []byte) types.ProvisionCredentialRequest {
	return types.ProvisionCredentialRequest{
		OperatorCredentialUID: operatorUID,
		ModuleID:              "prov-module-001",
		CredentialUID:         credUID,
		RoleID:                "guest",
	}
}

// ── scan-1 not found → pending_authorization created ─────────────────────────

func TestProvision_OperatorNotFound_CreatesPlaceholderAndReturnsUnauthorized(t *testing.T) {
	svc, maStore, _, _ := newProvisionSvc(t)
	operatorUID := []byte{0x04, 0xFF, 0xFF, 0xFF}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}

	resp, err := svc.Provision(context.Background(), provisionReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized, got status=%q detail=%q", resp.Status, resp.Detail)
	}

	// A pending_authorization record must have been created for the unknown credential.
	pending, err := maStore.ListPendingAuthorizations(context.Background())
	if err != nil {
		t.Fatalf("ListPendingAuthorizations: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending record, got %d", len(pending))
	}
	opHash := service.HashCredentialID(operatorUID, testSecret)
	rec, err := maStore.GetMemberByCredential(context.Background(), opHash)
	if err != nil {
		t.Fatalf("GetMemberByCredential for pending op: %v", err)
	}
	if rec.ProvisioningStatus != store.ProvisioningStatusPendingAuthorization {
		t.Errorf("pending record provisioning_status = %q, want pending_authorization", rec.ProvisioningStatus)
	}
}

// ── scan-1 found but role lacks member.provision ──────────────────────────────

func TestProvision_OperatorNoProvisionPermission_Unauthorized(t *testing.T) {
	svc, maStore, _, eventStore := newProvisionSvc(t)
	operatorUID := []byte{0x04, 0xAA, 0xBB, 0xCC}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}
	// guest role has no permissions by default
	seedOperatorMember(t, maStore, operatorUID, "guest", true, true)

	resp, err := svc.Provision(context.Background(), provisionReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized, got status=%q detail=%q", resp.Status, resp.Detail)
	}

	// Failed attempt must be recorded in the event log.
	events := eventStore.Events()
	if len(events) != 1 || events[0].Granted {
		t.Errorf("expected 1 denied event, got %d events", len(events))
	}
}

// ── scan-1 found but member is inactive ───────────────────────────────────────

func TestProvision_OperatorInactive_Unauthorized(t *testing.T) {
	svc, maStore, roleStore, eventStore := newProvisionSvc(t)
	operatorUID := []byte{0x04, 0x11, 0x22, 0x33}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}
	grantProvisionPermission(t, roleStore, "operator")
	// active=false → provisioning_status = pending_authorization
	seedOperatorMember(t, maStore, operatorUID, "operator", true, false)

	resp, err := svc.Provision(context.Background(), provisionReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized for inactive operator, got status=%q detail=%q", resp.Status, resp.Detail)
	}

	events := eventStore.Events()
	if len(events) != 1 || events[0].Granted {
		t.Errorf("expected 1 denied event, got %d events", len(events))
	}
}

func TestProvision_OperatorDisabled_Unauthorized(t *testing.T) {
	svc, maStore, roleStore, eventStore := newProvisionSvc(t)
	operatorUID := []byte{0x04, 0x44, 0x55, 0x66}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}
	grantProvisionPermission(t, roleStore, "operator")
	// enabled=false
	seedOperatorMember(t, maStore, operatorUID, "operator", false, true)

	resp, err := svc.Provision(context.Background(), provisionReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized for disabled operator, got status=%q detail=%q", resp.Status, resp.Detail)
	}

	events := eventStore.Events()
	if len(events) != 1 || events[0].Granted {
		t.Errorf("expected 1 denied event, got %d events", len(events))
	}
}

// ── scan-1 valid → provisioning succeeds ─────────────────────────────────────

func TestProvision_ValidOperator_Success(t *testing.T) {
	svc, maStore, roleStore, _ := newProvisionSvc(t)
	operatorUID := []byte{0x04, 0xDE, 0xAD, 0xBE}
	memberUID := []byte{0x04, 0x99, 0x88, 0x77}
	grantProvisionPermission(t, roleStore, "operator")
	opMemberUUID := seedOperatorMember(t, maStore, operatorUID, "operator", true, true)

	resp, err := svc.Provision(context.Background(), provisionReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusSuccess {
		t.Errorf("expected success, got status=%q detail=%q", resp.Status, resp.Detail)
	}
	if resp.MemberUUID == "" {
		t.Error("expected non-empty MemberUUID on success")
	}

	// Verify created_by_uuid is the operator's member UUID.
	credHash := service.HashCredentialID(memberUID, testSecret)
	newMember, err := maStore.GetMemberByCredential(context.Background(), credHash)
	if err != nil {
		t.Fatalf("GetMemberByCredential for new member: %v", err)
	}
	if newMember.CreatedByUUID != opMemberUUID {
		t.Errorf("created_by_uuid = %q, want %q", newMember.CreatedByUUID, opMemberUUID)
	}
}

// ── no operator credential provided ──────────────────────────────────────────

func TestProvision_NoOperatorCredential_Unauthorized(t *testing.T) {
	svc, _, _, _ := newProvisionSvc(t)
	req := types.ProvisionCredentialRequest{
		ModuleID:      "prov-module-001",
		CredentialUID: []byte{0x04, 0x01, 0x02, 0x03},
		RoleID:        "guest",
	}
	resp, err := svc.Provision(context.Background(), req)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized when no operator provided, got status=%q", resp.Status)
	}
}

// ── second scan with already-pending operator credential ─────────────────────
// Ensures scanning an already-pending credential again returns a duplicate
// response (not a second pending record).

func TestProvision_OperatorAlreadyPending_ReturnsDuplicate(t *testing.T) {
	svc, _, _, _ := newProvisionSvc(t)
	operatorUID := []byte{0x04, 0xCC, 0xDD, 0xEE}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}

	// First scan: creates pending record, returns UNAUTHORIZED.
	resp1, err := svc.Provision(context.Background(), provisionReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	if resp1.Status != types.ProvisionStatusUnauthorized {
		t.Fatalf("first call: expected unauthorized, got %q", resp1.Status)
	}

	// Second scan with the same credential: member exists as pending, so the
	// pending record is found and returned (no second pending created).
	resp2, err := svc.Provision(context.Background(), provisionReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("second Provision: %v", err)
	}
	if resp2.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("second call: expected unauthorized (still no permission), got %q", resp2.Status)
	}
}
