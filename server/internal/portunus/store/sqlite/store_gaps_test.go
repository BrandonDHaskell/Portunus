package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// A tap or heartbeat from a module nobody commissioned creates a placeholder
// row (for foreign keys) that must never count as known: only an admin
// commissioning step may turn a device into one the door trusts.
func TestDeviceStore_UnknownDeviceNeverBecomesKnown(t *testing.T) {
	conn := openTestDB(t)
	ds := sqlitestore.NewDeviceStore(conn, newTestWriter(t, conn))
	ctx := context.Background()

	if known, err := ds.IsKnown(ctx, "rogue"); err != nil || known {
		t.Fatalf("before any traffic: known=%v err=%v", known, err)
	}
	seen := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	if err := ds.MarkSeen(ctx, "rogue", false, seen); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if known, err := ds.IsKnown(ctx, "rogue"); err != nil || known {
		t.Fatalf("after MarkSeen: known=%v err=%v (placeholder row must not be trusted)", known, err)
	}
	var enabled int
	var lastSeen int64
	if err := conn.QueryRow(`SELECT enabled, last_seen_at_ms FROM modules WHERE module_id='rogue'`).Scan(&enabled, &lastSeen); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 || lastSeen != seen.UnixMilli() {
		t.Fatalf("placeholder row enabled=%d last_seen=%d", enabled, lastSeen)
	}
	if mt, err := ds.GetModuleType(ctx, "rogue"); err != nil || mt != store.ModuleTypeACU {
		t.Fatalf("placeholder type = %q, %v; want the default access control unit", mt, err)
	}

	if err := ds.MarkSeen(ctx, "  ", false, seen); err != nil {
		t.Errorf("blank module id: %v", err)
	}
	if _, err := ds.GetModuleType(ctx, "never-seen"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetModuleType(never-seen): %v", err)
	}
}

func TestDeviceStore_IsKnownRules(t *testing.T) {
	conn := openTestDB(t)
	ds := sqlitestore.NewDeviceStore(conn, newTestWriter(t, conn))
	rows := []struct {
		id                    string
		enabled               int
		commissioned, revoked any
		want                  bool
	}{
		{"ok", 1, 1, nil, true},
		{"disabled", 0, 1, nil, false},
		{"uncommissioned", 1, nil, nil, false},
		{"revoked", 1, 1, 2, false},
	}
	for _, r := range rows {
		if _, err := conn.Exec(`INSERT INTO modules(module_id, enabled, commissioned_at_ms, revoked_at_ms, created_at_ms, updated_at_ms) VALUES (?,?,?,?,0,0)`,
			r.id, r.enabled, r.commissioned, r.revoked); err != nil {
			t.Fatal(err)
		}
		if got, err := ds.IsKnown(context.Background(), r.id); err != nil || got != r.want {
			t.Errorf("%s: known=%v err=%v, want %v", r.id, got, err, r.want)
		}
	}
	// A revoked module that keeps heartbeating stays revoked.
	if err := ds.MarkSeen(context.Background(), "revoked", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := ds.IsKnown(context.Background(), "revoked"); got {
		t.Error("revoked module became known after MarkSeen")
	}
}

// UpdateLastLogin is left out on purpose: it is a best-effort write whose
// error Login ignores.
func TestAdminUserStore_WritesReportNotFound(t *testing.T) {
	conn := openTestDB(t)
	us := sqlitestore.NewAdminUserStore(conn, newTestWriter(t, conn))
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)
	link := "member-x"
	writes := map[string]error{
		"UpdatePasswordHash":     us.UpdatePasswordHash(ctx, "ghost", "h"),
		"SetAdminUserEnabled":    us.SetAdminUserEnabled(ctx, "ghost", false),
		"SetAdminUserRole":       us.SetAdminUserRole(ctx, "ghost", "admin"),
		"SetAdminUserExpiry":     us.SetAdminUserExpiry(ctx, "ghost", &exp),
		"SetMustChangePW":        us.SetMustChangePW(ctx, "ghost", true),
		"SetAdminUserMemberLink": us.SetAdminUserMemberLink(ctx, "ghost", &link),
	}
	for name, err := range writes {
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s(ghost): got %v, want ErrNotFound", name, err)
		}
	}
}

func TestAdminUserStore_DuplicateUsernameAndRole(t *testing.T) {
	conn := openTestDB(t)
	us := sqlitestore.NewAdminUserStore(conn, newTestWriter(t, conn))
	ctx := context.Background()
	if err := us.CreateAdminUser(ctx, "u1", "dup", "h", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := us.CreateAdminUser(ctx, "u2", "dup", "h", "admin"); !errors.Is(err, store.ErrUsernameAlreadyExists) {
		t.Errorf("duplicate username: %v", err)
	}
	if err := us.SetAdminUserRole(ctx, "u1", "no-such-role"); err == nil {
		t.Error("assigning a role that does not exist was accepted")
	}
	if err := us.SetAdminUserMemberLink(ctx, "u1", strPtr("no-such-member")); err == nil {
		t.Error("linking a member that does not exist was accepted")
	}
	if ok, err := us.AnyAdminExists(ctx); err != nil || !ok {
		t.Errorf("AnyAdminExists = %v, %v", ok, err)
	}
}

func strPtr(s string) *string { return &s }
