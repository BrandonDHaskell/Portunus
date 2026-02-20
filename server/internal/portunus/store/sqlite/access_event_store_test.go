package sqlite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// ═══════════════════════════════════════════════════════════════════════════
// RecordEvent — basic insert
// ═══════════════════════════════════════════════════════════════════════════

func TestAccessEventStore_RecordEvent_InsertsRow(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	seedModule(t, conn, "door-001")
	as := sqlitestore.NewAccessEventStore(conn, w)

	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	closed := true

	err := as.RecordEvent(context.Background(), store.AccessEventRecord{
		ModuleID:   "door-001",
		ReceivedAt: now,
		DoorClosed: &closed,
		Granted:    true,
		Reason:     "allow_all",
		DecidedAt:  now.Add(5 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	var count int
	err = conn.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM access_events WHERE module_id = ?`, "door-001",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 access_event row, got %d", count)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// RecordEvent — column values
// ═══════════════════════════════════════════════════════════════════════════

func TestAccessEventStore_RecordEvent_ColumnsCorrect(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	seedModule(t, conn, "door-001")
	as := sqlitestore.NewAccessEventStore(conn, w)

	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	reqAt := now.Add(-100 * time.Millisecond)
	closed := false

	err := as.RecordEvent(context.Background(), store.AccessEventRecord{
		ModuleID:    "door-001",
		ReceivedAt:  now,
		RequestedAt: &reqAt,
		DoorClosed:  &closed,
		Granted:     false,
		Reason:      "card_not_allowed",
		DecidedAt:   now.Add(2 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	var (
		granted     int
		reason      string
		receivedMs  int64
		requestedMs sql.NullInt64
		doorClosed  sql.NullInt64
		decidedMs   int64
	)
	err = conn.QueryRowContext(context.Background(), `
SELECT decision_granted, decision_reason, received_at_ms,
       requested_at_ms, door_closed, decided_at_ms
FROM access_events WHERE module_id = ?`, "door-001",
	).Scan(&granted, &reason, &receivedMs, &requestedMs, &doorClosed, &decidedMs)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if granted != 0 {
		t.Errorf("expected decision_granted=0, got %d", granted)
	}
	if reason != "card_not_allowed" {
		t.Errorf("expected decision_reason=card_not_allowed, got %q", reason)
	}
	if receivedMs != now.UnixMilli() {
		t.Errorf("expected received_at_ms=%d, got %d", now.UnixMilli(), receivedMs)
	}
	if !requestedMs.Valid || requestedMs.Int64 != reqAt.UnixMilli() {
		t.Errorf("expected requested_at_ms=%d, got %v", reqAt.UnixMilli(), requestedMs)
	}
	if !doorClosed.Valid || doorClosed.Int64 != 0 {
		t.Errorf("expected door_closed=0, got %v", doorClosed)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// RecordEvent — door_id resolved from module
// ═══════════════════════════════════════════════════════════════════════════

func TestAccessEventStore_RecordEvent_ResolvesDoorID(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ctx := context.Background()

	// Seed a door and a module linked to it.
	nowMs := time.Now().UTC().UnixMilli()
	_, err := conn.ExecContext(ctx, `
INSERT INTO doors(door_id, name, location, created_at_ms, updated_at_ms)
VALUES ('door_main', 'Main Door', 'Lobby', ?, ?);`, nowMs, nowMs)
	if err != nil {
		t.Fatalf("seed door: %v", err)
	}
	_, err = conn.ExecContext(ctx, `
INSERT INTO modules(module_id, door_id, enabled, commissioned_at_ms, created_at_ms, updated_at_ms)
VALUES ('door-001', 'door_main', 1, ?, ?, ?);`, nowMs, nowMs, nowMs)
	if err != nil {
		t.Fatalf("seed module: %v", err)
	}

	as := sqlitestore.NewAccessEventStore(conn, w)
	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)

	err = as.RecordEvent(ctx, store.AccessEventRecord{
		ModuleID:   "door-001",
		ReceivedAt: now,
		Granted:    true,
		Reason:     "allow_all",
		DecidedAt:  now,
	})
	if err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	var doorID sql.NullString
	err = conn.QueryRowContext(ctx,
		`SELECT door_id FROM access_events WHERE module_id = ?`, "door-001",
	).Scan(&doorID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !doorID.Valid || doorID.String != "door_main" {
		t.Errorf("expected door_id=door_main, got %v", doorID)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// RecordEvent — nullable fields
// ═══════════════════════════════════════════════════════════════════════════

func TestAccessEventStore_RecordEvent_NullOptionalFields(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	seedModule(t, conn, "door-001")
	as := sqlitestore.NewAccessEventStore(conn, w)

	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)

	// No RequestedAt, no DoorClosed, no CardIDHash.
	err := as.RecordEvent(context.Background(), store.AccessEventRecord{
		ModuleID:   "door-001",
		ReceivedAt: now,
		Granted:    true,
		Reason:     "allow_all",
		DecidedAt:  now,
	})
	if err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	var (
		requestedMs sql.NullInt64
		doorClosed  sql.NullInt64
		cardHash    []byte
	)
	err = conn.QueryRowContext(context.Background(), `
SELECT requested_at_ms, door_closed, card_id_hash
FROM access_events WHERE module_id = ?`, "door-001",
	).Scan(&requestedMs, &doorClosed, &cardHash)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if requestedMs.Valid {
		t.Error("expected requested_at_ms to be NULL")
	}
	if doorClosed.Valid {
		t.Error("expected door_closed to be NULL")
	}
	if cardHash != nil {
		t.Error("expected card_id_hash to be NULL")
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// RecordEvent — append-only
// ═══════════════════════════════════════════════════════════════════════════

func TestAccessEventStore_RecordEvent_AppendOnly(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	seedModule(t, conn, "door-001")
	as := sqlitestore.NewAccessEventStore(conn, w)
	ctx := context.Background()

	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		err := as.RecordEvent(ctx, store.AccessEventRecord{
			ModuleID:   "door-001",
			ReceivedAt: now.Add(time.Duration(i) * time.Second),
			Granted:    true,
			Reason:     "allow_all",
			DecidedAt:  now.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("RecordEvent %d: %v", i, err)
		}
	}

	var count int
	err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM access_events WHERE module_id = ?`, "door-001",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 rows (append-only), got %d", count)
	}
}

// ── Test helpers ─────────────────────────────────────────────────────────────

// seedModule ensures a module row exists so FK constraints on access_events
// are satisfied.  The module is created as disabled/uncommissioned.
func seedModule(t *testing.T, conn *sql.DB, moduleID string) {
	t.Helper()
	nowMs := time.Now().UTC().UnixMilli()
	_, err := conn.ExecContext(context.Background(), `
INSERT OR IGNORE INTO modules(module_id, enabled, created_at_ms, updated_at_ms)
VALUES (?, 0, ?, ?);`, moduleID, nowMs, nowMs)
	if err != nil {
		t.Fatalf("seedModule(%s): %v", moduleID, err)
	}
}
