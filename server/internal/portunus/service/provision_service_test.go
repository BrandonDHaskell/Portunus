package service_test

// Integration tests for ProvisionService operator-resolution logic (P1-3).
//
// These tests verify that:
//   - scan-1 UID (OperatorCredentialUID) resolves to an admin user (preferred path)
//   - an unregistered scan-1 UID returns UNAUTHORIZED
//   - a disabled admin user's badge returns UNAUTHORIZED
//   - empty OperatorCredentialUID falls back to OperatorUUID (legacy Kconfig path)
//   - empty both fields returns UNAUTHORIZED

import (
	"context"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// testSecret is a fixed HMAC key used throughout these tests.
var testSecret = []byte("test-secret-key-32-bytes-padding!!")

// seedAdminUser creates an admin user and returns their UUID.
func seedAdminUser(t *testing.T, adminStore *sqlitestore.AdminUserStore, username string, enabled bool) string {
	t.Helper()
	ctx := context.Background()
	adminUUID := "admin-" + username
	if err := adminStore.CreateAdminUser(ctx, adminUUID, username, "$2a$04$irrelevant", "admin"); err != nil {
		t.Fatalf("seedAdminUser %q: CreateAdminUser: %v", username, err)
	}
	if !enabled {
		if err := adminStore.SetAdminUserEnabled(ctx, adminUUID, false); err != nil {
			t.Fatalf("seedAdminUser %q: SetAdminUserEnabled: %v", username, err)
		}
	}
	return adminUUID
}

// registerAdminBadge registers rawUID as the badge for adminUUID.
func registerAdminBadge(t *testing.T, adminStore *sqlitestore.AdminUserStore, adminUUID string, rawUID []byte) {
	t.Helper()
	hash := service.HashCredentialID(rawUID, testSecret)
	if err := adminStore.RegisterAdminCredential(context.Background(), adminUUID, hash); err != nil {
		t.Fatalf("registerAdminBadge: %v", err)
	}
}

// newProvisionSvc builds a ProvisionService backed by real migrated SQLite.
func newProvisionSvc(t *testing.T) (
	*service.ProvisionService,
	*sqlitestore.AdminUserStore,
	store.MemberAccessStore,
) {
	t.Helper()
	conn, writer := openSvcTestDB(t)
	seedModule(t, conn, "prov-module-001")

	adminStore := sqlitestore.NewAdminUserStore(conn, writer)
	maStore := sqlitestore.NewMemberAccessStore(conn, writer)
	roleStore := sqlitestore.NewRoleStore(conn, writer)

	registry := service.NewDeviceRegistry(memory.NewDeviceStore([]string{"prov-module-001"}))

	svc := service.NewProvisionService(registry, maStore, roleStore, adminStore, testSecret)
	return svc, adminStore, maStore
}

func provisionReq(operatorUID, credUID []byte) types.ProvisionCredentialRequest {
	return types.ProvisionCredentialRequest{
		OperatorCredentialUID: operatorUID,
		ModuleID:              "prov-module-001",
		CredentialUID:         credUID,
		RoleID:                "guest",
	}
}

// ── scan-1 credential path ────────────────────────────────────────────────────

func TestProvision_Scan1CredentialResolvesToAdmin_Success(t *testing.T) {
	svc, adminStore, _ := newProvisionSvc(t)
	adminUUID := seedAdminUser(t, adminStore, "alice", true)
	operatorUID := []byte{0x04, 0xAA, 0xBB, 0xCC}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}
	registerAdminBadge(t, adminStore, adminUUID, operatorUID)

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
}

func TestProvision_Scan1CredentialUnregistered_Unauthorized(t *testing.T) {
	svc, _, _ := newProvisionSvc(t)
	// No badge registered — any scan-1 UID should be rejected.
	operatorUID := []byte{0x04, 0xFF, 0xFF, 0xFF}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}

	resp, err := svc.Provision(context.Background(), provisionReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized, got status=%q detail=%q", resp.Status, resp.Detail)
	}
}

func TestProvision_Scan1CredentialDisabledAdmin_Unauthorized(t *testing.T) {
	svc, adminStore, _ := newProvisionSvc(t)
	adminUUID := seedAdminUser(t, adminStore, "bob", false) // disabled
	operatorUID := []byte{0x04, 0x11, 0x22, 0x33}
	memberUID := []byte{0x04, 0x01, 0x02, 0x03}
	registerAdminBadge(t, adminStore, adminUUID, operatorUID)

	resp, err := svc.Provision(context.Background(), provisionReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusUnauthorized {
		t.Errorf("expected unauthorized for disabled admin, got status=%q detail=%q", resp.Status, resp.Detail)
	}
}

// ── legacy operator_uuid fallback path ───────────────────────────────────────

func TestProvision_LegacyOperatorUUID_Success(t *testing.T) {
	svc, adminStore, _ := newProvisionSvc(t)
	adminUUID := seedAdminUser(t, adminStore, "carol", true)
	memberUID := []byte{0x04, 0x0A, 0x0B, 0x0C}

	// No operator_credential_uid — use legacy operator_uuid directly.
	req := types.ProvisionCredentialRequest{
		OperatorUUID:  adminUUID,
		ModuleID:      "prov-module-001",
		CredentialUID: memberUID,
		RoleID:        "guest",
	}
	resp, err := svc.Provision(context.Background(), req)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusSuccess {
		t.Errorf("expected success on legacy path, got status=%q detail=%q", resp.Status, resp.Detail)
	}
}

func TestProvision_BothFieldsEmpty_Unauthorized(t *testing.T) {
	svc, _, _ := newProvisionSvc(t)
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

// ── operator_uuid recorded in member_access.created_by_uuid ─────────────────

func TestProvision_Scan1Credential_RecordsOperatorUUID(t *testing.T) {
	conn, writer := openSvcTestDB(t)
	seedModule(t, conn, "prov-module-001")

	adminStore := sqlitestore.NewAdminUserStore(conn, writer)
	maStore := sqlitestore.NewMemberAccessStore(conn, writer)
	roleStore := sqlitestore.NewRoleStore(conn, writer)
	registry := service.NewDeviceRegistry(memory.NewDeviceStore([]string{"prov-module-001"}))
	svc := service.NewProvisionService(registry, maStore, roleStore, adminStore, testSecret)

	adminUUID := seedAdminUser(t, adminStore, "dave", true)
	operatorUID := []byte{0x04, 0xDE, 0xAD, 0xBE}
	memberUID := []byte{0x04, 0x99, 0x88, 0x77}
	registerAdminBadge(t, adminStore, adminUUID, operatorUID)

	resp, err := svc.Provision(context.Background(), provisionReq(operatorUID, memberUID))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Status != types.ProvisionStatusSuccess {
		t.Fatalf("expected success, got %q / %q", resp.Status, resp.Detail)
	}

	// Verify the member was created with the resolved admin UUID.
	credHash := service.HashCredentialID(memberUID, testSecret)
	member, err := maStore.GetMemberByCredential(context.Background(), credHash)
	if err != nil {
		t.Fatalf("GetMemberByCredential: %v", err)
	}
	if member.CreatedByUUID != adminUUID {
		t.Errorf("created_by_uuid = %q, want %q", member.CreatedByUUID, adminUUID)
	}
}
