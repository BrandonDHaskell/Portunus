package service_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// newExpiryWorkerFixture wires a worker against a real in-memory SQLite DB.
// auditStore may be nil.
func newExpiryWorkerFixture(t *testing.T, ttlDays int) (
	*service.ExpiryWorker,
	store.MemberAccessStore,
	*sql.DB,
) {
	t.Helper()
	conn, writer := openSvcTestDB(t)
	maStore := sqlitestore.NewMemberAccessStore(conn, writer)
	auditConn, auditWriter := openSvcTestDB(t)
	auditSt := sqlitestore.NewAuditStore(auditConn, auditWriter)
	worker := service.NewExpiryWorker(maStore, auditSt, service.ExpiryWorkerConfig{
		IntervalMinutes: 9999, // large — we call sweep manually via Start+Stop
		PendingTTLDays:  ttlDays,
	}, silentLogger())
	return worker, maStore, conn
}

// sweepOnce starts the worker (which runs an immediate sweep), then stops it.
// A brief sleep lets the initial sweep goroutine complete its DB writes before
// the context is cancelled — same pattern used in access_service_member_test.go.
func sweepOnce(t *testing.T, w *service.ExpiryWorker) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	w.Stop()
}

// ── pending TTL sweep ─────────────────────────────────────────────────────────

func TestExpiryWorker_ArchivesStalePending(t *testing.T) {
	worker, maStore, conn := newExpiryWorkerFixture(t, 7)
	ctx := context.Background()

	uuid := "ew-stale-001"
	if err := maStore.CreateMember(ctx, uuid, "", store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	// Back-date to 8 days ago.
	past := time.Now().UTC().Add(-8 * 24 * time.Hour).UnixMilli()
	if _, err := conn.ExecContext(ctx, `UPDATE member_access SET created_at_ms = ? WHERE uuid = ?`, past, uuid); err != nil {
		t.Fatalf("back-date: %v", err)
	}

	sweepOnce(t, worker)

	rec, err := maStore.GetMember(ctx, uuid)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if rec.Status != store.MemberStatusArchived {
		t.Errorf("status = %q, want archived", rec.Status)
	}
}

func TestExpiryWorker_KeepsFreshPending(t *testing.T) {
	worker, maStore, _ := newExpiryWorkerFixture(t, 7)
	ctx := context.Background()

	uuid := "ew-fresh-001"
	if err := maStore.CreateMember(ctx, uuid, "", store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	sweepOnce(t, worker)

	rec, err := maStore.GetMember(ctx, uuid)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if rec.Status != store.MemberStatusActive {
		t.Errorf("status = %q, want active (untouched)", rec.Status)
	}
}

func TestExpiryWorker_ZeroTTLDisablesPendingSweep(t *testing.T) {
	worker, maStore, conn := newExpiryWorkerFixture(t, 0)
	ctx := context.Background()

	uuid := "ew-disabled-001"
	if err := maStore.CreateMember(ctx, uuid, "", store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	// Very old: would be archived if TTL were active.
	past := time.Now().UTC().Add(-365 * 24 * time.Hour).UnixMilli()
	if _, err := conn.ExecContext(ctx, `UPDATE member_access SET created_at_ms = ? WHERE uuid = ?`, past, uuid); err != nil {
		t.Fatalf("back-date: %v", err)
	}

	sweepOnce(t, worker)

	rec, err := maStore.GetMember(ctx, uuid)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if rec.Status == store.MemberStatusArchived {
		t.Error("worker with TTL=0 must not archive rows")
	}
}

func TestExpiryWorker_PendingSweepSkipsActiveRows(t *testing.T) {
	worker, maStore, conn := newExpiryWorkerFixture(t, 7)
	ctx := context.Background()

	uuid := "ew-active-old-001"
	if err := maStore.CreateMember(ctx, uuid, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	past := time.Now().UTC().Add(-30 * 24 * time.Hour).UnixMilli()
	if _, err := conn.ExecContext(ctx, `UPDATE member_access SET created_at_ms = ? WHERE uuid = ?`, past, uuid); err != nil {
		t.Fatalf("back-date: %v", err)
	}

	sweepOnce(t, worker)

	rec, err := maStore.GetMember(ctx, uuid)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if rec.Status != store.MemberStatusActive {
		t.Errorf("status = %q, want active (active rows must not be archived by pending sweep)", rec.Status)
	}
}
