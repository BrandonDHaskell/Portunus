package service_test

// Integration tests for ProvisionService.
//
// Capture path: PEU + no operator UID → pending_authorization created.
//
// Gate checks:
//   - ACU module → unauthorized (PEU gate)

import (
	"context"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

var testSecret = []byte("test-secret-key-32-bytes-padding!!")

const testModuleID = "prov-module-001"

func newProvisionSvc(t *testing.T) (
	*service.ProvisionService,
	store.MemberAccessStore,
	store.RoleStore,
	*memory.AccessEventStore,
) {
	t.Helper()
	conn, writer := openSvcTestDB(t)
	seedModule(t, conn, testModuleID)

	maStore := sqlitestore.NewMemberAccessStore(conn, writer)
	roleStore := sqlitestore.NewRoleStore(conn, writer)
	eventStore := memory.NewAccessEventStore()
	registry := service.NewDeviceRegistry(memory.NewDeviceStore([]string{testModuleID}))

	svc := service.NewProvisionService(
		registry, maStore, roleStore, eventStore,
		testSecret, nil,
	)
	return svc, maStore, roleStore, eventStore
}

func captureReq(credUID []byte) types.ProvisionCredentialRequest {
	return types.ProvisionCredentialRequest{
		ModuleID:      testModuleID,
		CredentialUID: credUID,
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
		registry, maStore, roleStore, eventStore,
		testSecret, nil,
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

// ── Capture path ──────────────────────────────────────────────────────────────

func TestProvision_Capture_CreatesNewPendingMember(t *testing.T) {
	svc, maStore, _, _ := newProvisionSvc(t)
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
	svc, _, _, _ := newProvisionSvc(t)
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
