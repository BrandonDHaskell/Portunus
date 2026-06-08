package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

func newMemberSvc(t *testing.T) (*service.MemberAccessService, store.MemberAccessStore, store.RoleStore) {
	t.Helper()
	conn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(conn, writer)
	roleStore := sqlitestore.NewRoleStore(conn, writer)
	svc := service.NewMemberAccessService(maStore, roleStore)
	return svc, maStore, roleStore
}

// ── ApprovePending: happy path ────────────────────────────────────────────────

func TestApprovePending_HappyPath(t *testing.T) {
	svc, maStore, _ := newMemberSvc(t)
	ctx := context.Background()

	// Create a pending member.
	memberUUID := "pending-member-001"
	if err := maStore.CreateMember(ctx, memberUUID, "guest", "",
		store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	if err := svc.ApprovePending(ctx, memberUUID, "operator", "admin-001", nil, nil); err != nil {
		t.Fatalf("ApprovePending: %v", err)
	}

	rec, err := maStore.GetMember(ctx, memberUUID)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if rec.ProvisioningStatus != store.ProvisioningStatusActive {
		t.Errorf("provisioning_status = %q, want active", rec.ProvisioningStatus)
	}
	if rec.Status != store.MemberStatusActive {
		t.Errorf("status = %q, want active", rec.Status)
	}
	if rec.RoleID != "operator" {
		t.Errorf("role_id = %q, want operator", rec.RoleID)
	}
	if rec.CreatedByUUID != "admin-001" {
		t.Errorf("created_by_uuid = %q, want admin-001", rec.CreatedByUUID)
	}
}

// ── ApprovePending: role defaults applied ─────────────────────────────────────

func TestApprovePending_RoleDefaultsApplied(t *testing.T) {
	svc, maStore, roleStore := newMemberSvc(t)
	ctx := context.Background()

	// Create a role with default expiry.
	expiryDays := 14
	if err := roleStore.CreateRole(ctx, "short-lived", "Short Lived", "", &expiryDays, nil); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	memberUUID := "pending-member-002"
	if err := maStore.CreateMember(ctx, memberUUID, "guest", "",
		store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	if err := svc.ApprovePending(ctx, memberUUID, "short-lived", "admin-001", nil, nil); err != nil {
		t.Fatalf("ApprovePending: %v", err)
	}

	rec, err := maStore.GetMember(ctx, memberUUID)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if rec.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt from role default, got nil")
	}
	expected := time.Now().UTC().AddDate(0, 0, expiryDays)
	diff := rec.ExpiresAt.Sub(expected)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("ExpiresAt = %v, want ~%v (within 1s)", rec.ExpiresAt, expected)
	}
}

// ── ApprovePending: caller-supplied overrides role default ─────────────────────

func TestApprovePending_CallerOverridesRoleDefault(t *testing.T) {
	svc, maStore, roleStore := newMemberSvc(t)
	ctx := context.Background()

	expiryDays := 30
	if err := roleStore.CreateRole(ctx, "long-lived", "Long Lived", "", &expiryDays, nil); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	memberUUID := "pending-member-003"
	if err := maStore.CreateMember(ctx, memberUUID, "guest", "",
		store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	custom := time.Now().UTC().AddDate(0, 0, 7)
	if err := svc.ApprovePending(ctx, memberUUID, "long-lived", "", &custom, nil); err != nil {
		t.Fatalf("ApprovePending: %v", err)
	}

	rec, err := maStore.GetMember(ctx, memberUUID)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if rec.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt, got nil")
	}
	diff := rec.ExpiresAt.Sub(custom)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("ExpiresAt = %v, want ~%v", rec.ExpiresAt, custom)
	}
}

// ── ApprovePending: not pending ───────────────────────────────────────────────

func TestApprovePending_AlreadyActive_ReturnsNotPending(t *testing.T) {
	svc, maStore, _ := newMemberSvc(t)
	ctx := context.Background()

	// Create an active member (not pending).
	memberUUID := "active-member-001"
	if err := maStore.CreateMember(ctx, memberUUID, "guest", "",
		store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	err := svc.ApprovePending(ctx, memberUUID, "operator", "", nil, nil)
	if !errors.Is(err, service.ErrMemberNotPending) {
		t.Errorf("expected ErrMemberNotPending, got %v", err)
	}
}

// ── ApprovePending: not found ─────────────────────────────────────────────────

func TestApprovePending_NotFound_ReturnsNotFound(t *testing.T) {
	svc, _, _ := newMemberSvc(t)
	err := svc.ApprovePending(context.Background(), "nonexistent-uuid", "operator", "", nil, nil)
	if !errors.Is(err, service.ErrMemberNotFound) {
		t.Errorf("expected ErrMemberNotFound, got %v", err)
	}
}

// ── ApprovePending: validation ────────────────────────────────────────────────

func TestApprovePending_MissingUUID_ReturnsError(t *testing.T) {
	svc, _, _ := newMemberSvc(t)
	err := svc.ApprovePending(context.Background(), "", "operator", "", nil, nil)
	if !errors.Is(err, service.ErrMemberUUIDRequired) {
		t.Errorf("expected ErrMemberUUIDRequired, got %v", err)
	}
}

func TestApprovePending_MissingRoleID_ReturnsError(t *testing.T) {
	svc, _, _ := newMemberSvc(t)
	err := svc.ApprovePending(context.Background(), "some-uuid", "", "", nil, nil)
	if !errors.Is(err, service.ErrRoleIDRequired) {
		t.Errorf("expected ErrRoleIDRequired, got %v", err)
	}
}
