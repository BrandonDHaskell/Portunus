package sqlite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// ═══════════════════════════════════════════════════════════════════════════
// UpsertHeartbeat — basic insert
// ═══════════════════════════════════════════════════════════════════════════

func TestHeartbeatStore_UpsertHeartbeat_InsertsRow(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	hs := sqlitestore.NewHeartbeatStore(conn, w)

	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	rssi := -55

	err := hs.UpsertHeartbeat(context.Background(), "door-001", store.HeartbeatRecord{
		ReceivedAt: now,
		Request: types.HeartbeatRequest{
			ModuleID:        "door-001",
			FirmwareVersion: "0.1.0-test",
			UptimeSeconds:   300,
			RSSIDbm:         &rssi,
			IP:              "192.168.1.50",
		},
	})
	if err != nil {
		t.Fatalf("UpsertHeartbeat: %v", err)
	}

	// Verify heartbeat row was inserted.
	var count int
	err = conn.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM module_heartbeats WHERE module_id = ?`, "door-001",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count heartbeats: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 heartbeat row, got %d", count)
	}

	// Verify the heartbeat data.
	var fw string
	var ip string
	var wifiRSSI sql.NullInt64
	err = conn.QueryRowContext(context.Background(),
		`SELECT fw_version, ip, wifi_rssi FROM module_heartbeats WHERE module_id = ?`, "door-001",
	).Scan(&fw, &ip, &wifiRSSI)
	if err != nil {
		t.Fatalf("query heartbeat: %v", err)
	}
	if fw != "0.1.0-test" {
		t.Errorf("expected fw_version=0.1.0-test, got %q", fw)
	}
	if ip != "192.168.1.50" {
		t.Errorf("expected ip=192.168.1.50, got %q", ip)
	}
	if !wifiRSSI.Valid || wifiRSSI.Int64 != -55 {
		t.Errorf("expected wifi_rssi=-55, got %v", wifiRSSI)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// UpsertHeartbeat — append-only (each heartbeat gets its own row)
// ═══════════════════════════════════════════════════════════════════════════

func TestHeartbeatStore_UpsertHeartbeat_AppendOnly(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	hs := sqlitestore.NewHeartbeatStore(conn, w)
	ctx := context.Background()

	base := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)

	// Insert 3 heartbeats from the same module.
	for i := 0; i < 3; i++ {
		rec := store.HeartbeatRecord{
			ReceivedAt: base.Add(time.Duration(i) * 10 * time.Second),
			Request: types.HeartbeatRequest{
				ModuleID:      "door-001",
				UptimeSeconds: uint64(i * 10),
			},
		}
		if err := hs.UpsertHeartbeat(ctx, "door-001", rec); err != nil {
			t.Fatalf("heartbeat %d: %v", i, err)
		}
	}

	// Should have 3 separate rows — not 1 updated row.
	var count int
	err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM module_heartbeats WHERE module_id = ?`, "door-001",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 heartbeat rows (append-only), got %d", count)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// UpsertHeartbeat — module snapshot update
// ═══════════════════════════════════════════════════════════════════════════

func TestHeartbeatStore_UpsertHeartbeat_UpdatesModuleSnapshot(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	hs := sqlitestore.NewHeartbeatStore(conn, w)

	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	rssi := -40

	err := hs.UpsertHeartbeat(context.Background(), "door-001", store.HeartbeatRecord{
		ReceivedAt: now,
		Request: types.HeartbeatRequest{
			ModuleID:        "door-001",
			FirmwareVersion: "0.2.0",
			RSSIDbm:         &rssi,
			IP:              "10.0.0.5",
		},
	})
	if err != nil {
		t.Fatalf("UpsertHeartbeat: %v", err)
	}

	// The modules table should reflect the latest snapshot values.
	var lastIP, lastFW string
	var lastRSSI sql.NullInt64
	var lastSeen sql.NullInt64
	err = conn.QueryRowContext(context.Background(), `
SELECT last_ip, last_fw_version, last_wifi_rssi, last_seen_at_ms
FROM modules WHERE module_id = ?`, "door-001",
	).Scan(&lastIP, &lastFW, &lastRSSI, &lastSeen)
	if err != nil {
		t.Fatalf("query module snapshot: %v", err)
	}

	if lastIP != "10.0.0.5" {
		t.Errorf("expected last_ip=10.0.0.5, got %q", lastIP)
	}
	if lastFW != "0.2.0" {
		t.Errorf("expected last_fw_version=0.2.0, got %q", lastFW)
	}
	if !lastRSSI.Valid || lastRSSI.Int64 != -40 {
		t.Errorf("expected last_wifi_rssi=-40, got %v", lastRSSI)
	}
	if !lastSeen.Valid {
		t.Error("expected last_seen_at_ms to be set")
	}
}

func TestHeartbeatStore_UpsertHeartbeat_SnapshotReflectsLatest(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	hs := sqlitestore.NewHeartbeatStore(conn, w)
	ctx := context.Background()

	base := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)

	// First heartbeat.
	rssi1 := -70
	err := hs.UpsertHeartbeat(ctx, "door-001", store.HeartbeatRecord{
		ReceivedAt: base,
		Request: types.HeartbeatRequest{
			ModuleID:        "door-001",
			FirmwareVersion: "0.1.0",
			RSSIDbm:         &rssi1,
			IP:              "10.0.0.1",
		},
	})
	if err != nil {
		t.Fatalf("heartbeat 1: %v", err)
	}

	// Second heartbeat with updated values.
	rssi2 := -45
	err = hs.UpsertHeartbeat(ctx, "door-001", store.HeartbeatRecord{
		ReceivedAt: base.Add(10 * time.Second),
		Request: types.HeartbeatRequest{
			ModuleID:        "door-001",
			FirmwareVersion: "0.2.0",
			RSSIDbm:         &rssi2,
			IP:              "10.0.0.2",
		},
	})
	if err != nil {
		t.Fatalf("heartbeat 2: %v", err)
	}

	// The snapshot should reflect the second heartbeat, not the first.
	var lastFW, lastIP string
	var lastRSSI sql.NullInt64
	err = conn.QueryRowContext(ctx, `
SELECT last_fw_version, last_ip, last_wifi_rssi
FROM modules WHERE module_id = ?`, "door-001",
	).Scan(&lastFW, &lastIP, &lastRSSI)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if lastFW != "0.2.0" {
		t.Errorf("expected snapshot fw=0.2.0, got %q", lastFW)
	}
	if lastIP != "10.0.0.2" {
		t.Errorf("expected snapshot ip=10.0.0.2, got %q", lastIP)
	}
	if !lastRSSI.Valid || lastRSSI.Int64 != -45 {
		t.Errorf("expected snapshot rssi=-45, got %v", lastRSSI)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// UpsertHeartbeat — auto-creates module row
// ═══════════════════════════════════════════════════════════════════════════

func TestHeartbeatStore_UpsertHeartbeat_CreatesModuleIfMissing(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	hs := sqlitestore.NewHeartbeatStore(conn, w)

	// No module seeded — UpsertHeartbeat should create one.
	err := hs.UpsertHeartbeat(context.Background(), "new-module", store.HeartbeatRecord{
		ReceivedAt: time.Now().UTC(),
		Request: types.HeartbeatRequest{
			ModuleID: "new-module",
		},
	})
	if err != nil {
		t.Fatalf("UpsertHeartbeat: %v", err)
	}

	// Module should exist but be disabled (not commissioned).
	var enabled int
	var commissioned sql.NullInt64
	err = conn.QueryRowContext(context.Background(),
		`SELECT enabled, commissioned_at_ms FROM modules WHERE module_id = ?`, "new-module",
	).Scan(&enabled, &commissioned)
	if err != nil {
		t.Fatalf("query module: %v", err)
	}
	if enabled != 0 {
		t.Error("expected auto-created module to be disabled")
	}
	if commissioned.Valid {
		t.Error("expected auto-created module to be uncommissioned")
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// UpsertHeartbeat — optional fields
// ═══════════════════════════════════════════════════════════════════════════

func TestHeartbeatStore_UpsertHeartbeat_NilRSSI(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	hs := sqlitestore.NewHeartbeatStore(conn, w)

	// RSSI not provided — should be stored as NULL.
	err := hs.UpsertHeartbeat(context.Background(), "door-001", store.HeartbeatRecord{
		ReceivedAt: time.Now().UTC(),
		Request: types.HeartbeatRequest{
			ModuleID: "door-001",
			RSSIDbm:  nil,
		},
	})
	if err != nil {
		t.Fatalf("UpsertHeartbeat: %v", err)
	}

	var rssi sql.NullInt64
	err = conn.QueryRowContext(context.Background(),
		`SELECT wifi_rssi FROM module_heartbeats WHERE module_id = ?`, "door-001",
	).Scan(&rssi)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if rssi.Valid {
		t.Errorf("expected NULL rssi, got %d", rssi.Int64)
	}
}

func TestHeartbeatStore_UpsertHeartbeat_EmptyModuleID_NoOp(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	hs := sqlitestore.NewHeartbeatStore(conn, w)

	err := hs.UpsertHeartbeat(context.Background(), "", store.HeartbeatRecord{
		ReceivedAt: time.Now().UTC(),
		Request:    types.HeartbeatRequest{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Nothing should have been inserted.
	var count int
	err = conn.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM module_heartbeats`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows for empty module_id, got %d", count)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// PruneOlderThan
// ═══════════════════════════════════════════════════════════════════════════

func TestHeartbeatStore_PruneOlderThan_DeletesOldRows(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	hs := sqlitestore.NewHeartbeatStore(conn, w)
	ctx := context.Background()

	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)

	// Insert heartbeats at day 1, day 15, and day 30.
	for _, daysAgo := range []int{30, 15, 1} {
		ts := now.AddDate(0, 0, -daysAgo)
		rec := store.HeartbeatRecord{
			ReceivedAt: ts,
			Request: types.HeartbeatRequest{
				ModuleID:      "door-001",
				UptimeSeconds: uint64(daysAgo * 86400),
			},
		}
		if err := hs.UpsertHeartbeat(ctx, "door-001", rec); err != nil {
			t.Fatalf("insert heartbeat (-%dd): %v", daysAgo, err)
		}
	}

	// Prune anything older than 20 days from "now".
	cutoff := now.AddDate(0, 0, -20)
	deleted, err := hs.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 row deleted (the 30-day-old one), got %d", deleted)
	}

	// Should have 2 rows remaining.
	var count int
	err = conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM module_heartbeats WHERE module_id = ?`, "door-001",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 remaining rows, got %d", count)
	}
}

func TestHeartbeatStore_PruneOlderThan_NothingToDelete(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	hs := sqlitestore.NewHeartbeatStore(conn, w)
	ctx := context.Background()

	// Insert a recent heartbeat.
	rec := store.HeartbeatRecord{
		ReceivedAt: time.Now().UTC(),
		Request: types.HeartbeatRequest{
			ModuleID: "door-001",
		},
	}
	if err := hs.UpsertHeartbeat(ctx, "door-001", rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Cutoff is a year ago — nothing should be deleted.
	cutoff := time.Now().UTC().AddDate(-1, 0, 0)
	deleted, err := hs.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 rows deleted, got %d", deleted)
	}
}

func TestHeartbeatStore_PruneOlderThan_EmptyTable(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	hs := sqlitestore.NewHeartbeatStore(conn, w)

	deleted, err := hs.PruneOlderThan(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 on empty table, got %d", deleted)
	}
}

func TestHeartbeatStore_PruneOlderThan_PreservesModuleSnapshot(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	hs := sqlitestore.NewHeartbeatStore(conn, w)
	ctx := context.Background()

	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)

	// Insert an old heartbeat that set the module snapshot.
	rssi := -50
	rec := store.HeartbeatRecord{
		ReceivedAt: now.AddDate(0, 0, -60),
		Request: types.HeartbeatRequest{
			ModuleID:        "door-001",
			FirmwareVersion: "0.1.0",
			RSSIDbm:         &rssi,
			IP:              "10.0.0.1",
		},
	}
	if err := hs.UpsertHeartbeat(ctx, "door-001", rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Prune the heartbeat row.
	deleted, err := hs.PruneOlderThan(ctx, now)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	// The module row and its snapshot columns should still exist.
	var lastIP string
	err = conn.QueryRowContext(ctx,
		`SELECT last_ip FROM modules WHERE module_id = ?`, "door-001",
	).Scan(&lastIP)
	if err != nil {
		t.Fatalf("query module: %v", err)
	}
	if lastIP != "10.0.0.1" {
		t.Errorf("expected module snapshot preserved, got last_ip=%q", lastIP)
	}
}
