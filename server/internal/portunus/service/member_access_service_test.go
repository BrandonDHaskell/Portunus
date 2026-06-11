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

func newMemberSvc(t *testing.T) (*service.MemberAccessService, store.MemberAccessStore) {
	t.Helper()
	conn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(conn, writer)
	svc := service.NewMemberAccessService(maStore)
	return svc, maStore
}

// ── ApprovePending: happy path ────────────────────────────────────────────────

func TestApprovePending_HappyPath(t *testing.T) {
	svc, maStore := newMemberSvc(t)
	ctx := context.Background()

	memberUUID := "pending-member-001"
	if err := maStore.CreateMember(ctx, memberUUID, "",
		store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	inactivity := 30
	if err := svc.ApprovePending(ctx, memberUUID, "admin-001", nil, &inactivity); err != nil {
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
	if rec.ActivatedAt == nil {
		t.Error("expected ActivatedAt to be set after approval")
	}
	if rec.CreatedByUUID != "admin-001" {
		t.Errorf("created_by_uuid = %q, want admin-001", rec.CreatedByUUID)
	}
}

// ── ApprovePending: explicit expiry ──────────────────────────────────────────

func TestApprovePending_ExplicitExpiry(t *testing.T) {
	svc, maStore := newMemberSvc(t)
	ctx := context.Background()

	memberUUID := "pending-member-002"
	if err := maStore.CreateMember(ctx, memberUUID, "",
		store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	custom := time.Now().UTC().AddDate(0, 0, 7)
	inactivity := 14
	if err := svc.ApprovePending(ctx, memberUUID, "", &custom, &inactivity); err != nil {
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

// ── ApprovePending: inactivity required ──────────────────────────────────────

func TestApprovePending_MissingInactivity_ReturnsError(t *testing.T) {
	svc, _ := newMemberSvc(t)
	err := svc.ApprovePending(context.Background(), "some-uuid", "", nil, nil)
	if !errors.Is(err, service.ErrInactivityLimitRequired) {
		t.Errorf("expected ErrInactivityLimitRequired, got %v", err)
	}
}

// ── ApprovePending: not pending ───────────────────────────────────────────────

func TestApprovePending_AlreadyActive_ReturnsNotPending(t *testing.T) {
	svc, maStore := newMemberSvc(t)
	ctx := context.Background()

	memberUUID := "active-member-001"
	if err := maStore.CreateMember(ctx, memberUUID, "",
		store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	inactivity := 30
	err := svc.ApprovePending(ctx, memberUUID, "", nil, &inactivity)
	if !errors.Is(err, service.ErrMemberNotPending) {
		t.Errorf("expected ErrMemberNotPending, got %v", err)
	}
}

// ── ApprovePending: not found ─────────────────────────────────────────────────

func TestApprovePending_NotFound_ReturnsNotFound(t *testing.T) {
	svc, _ := newMemberSvc(t)
	inactivity := 30
	err := svc.ApprovePending(context.Background(), "nonexistent-uuid", "", nil, &inactivity)
	if !errors.Is(err, service.ErrMemberNotFound) {
		t.Errorf("expected ErrMemberNotFound, got %v", err)
	}
}

// ── ApprovePending: validation ────────────────────────────────────────────────

func TestApprovePending_MissingUUID_ReturnsError(t *testing.T) {
	svc, _ := newMemberSvc(t)
	inactivity := 30
	err := svc.ApprovePending(context.Background(), "", "", nil, &inactivity)
	if !errors.Is(err, service.ErrMemberUUIDRequired) {
		t.Errorf("expected ErrMemberUUIDRequired, got %v", err)
	}
}
