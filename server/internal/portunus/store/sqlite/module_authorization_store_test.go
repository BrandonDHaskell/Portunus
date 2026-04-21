package sqlite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// ── Migration: module_authorizations schema ───────────────────────────────────

func TestMigration_ModuleAuthorizationsTableExists(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	var name string
	err := conn.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='module_authorizations'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("module_authorizations table not found: %v", err)
	}
}

func TestMigration_ModuleAuthorizationsIndexes(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	for _, idx := range []string{
		"uidx_module_auth_active_grant",
		"idx_module_auth_active",
		"idx_module_auth_expires",
	} {
		var n string
		err := conn.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx,
		).Scan(&n)
		if err != nil {
			t.Errorf("expected index %q to exist: %v", idx, err)
		}
	}
}

func TestMigration_ModuleAuthUniqueMemberModule(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC().UnixMilli()
	seedModule(t, conn, "mod-001")
	seedMember(t, conn, "ma-uuid-001")

	_, err := conn.ExecContext(ctx, `
INSERT INTO module_authorizations(member_uuid, module_id, granted_at_ms)
VALUES ('ma-uuid-001', 'mod-001', ?)`, now)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err = conn.ExecContext(ctx, `
INSERT INTO module_authorizations(member_uuid, module_id, granted_at_ms)
VALUES ('ma-uuid-001', 'mod-001', ?)`, now)
	if err == nil {
		t.Error("expected UNIQUE constraint violation on duplicate (member_uuid, module_id)")
	}
}

// ── GrantAuthorization ────────────────────────────────────────────────────────

func TestModuleAuthStore_Grant_InsertsRow(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	as := sqlitestore.NewModuleAuthorizationStore(conn, w)
	ctx := context.Background()

	seedModule(t, conn, "mod-002")
	seedMember(t, conn, "ma-uuid-002")

	if err := as.GrantAuthorization(ctx, "ma-uuid-002", "mod-002", "admin-uuid", nil, ""); err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}

	var count int
	conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM module_authorizations WHERE member_uuid='ma-uuid-002' AND module_id='mod-002'`,
	).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

func TestModuleAuthStore_Grant_DuplicateConflict(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	as := sqlitestore.NewModuleAuthorizationStore(conn, w)
	ctx := context.Background()

	seedModule(t, conn, "mod-003")
	seedMember(t, conn, "ma-uuid-003")

	_ = as.GrantAuthorization(ctx, "ma-uuid-003", "mod-003", "", nil, "")
	err := as.GrantAuthorization(ctx, "ma-uuid-003", "mod-003", "", nil, "")
	if err != store.ErrAuthorizationAlreadyExists {
		t.Errorf("expected ErrAuthorizationAlreadyExists, got %v", err)
	}
}

func TestModuleAuthStore_Grant_AllowsAfterRevoke(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	as := sqlitestore.NewModuleAuthorizationStore(conn, w)
	ctx := context.Background()

	seedModule(t, conn, "mod-004")
	seedMember(t, conn, "ma-uuid-004")

	_ = as.GrantAuthorization(ctx, "ma-uuid-004", "mod-004", "", nil, "")
	_ = as.RevokeAuthorization(ctx, "ma-uuid-004", "mod-004", "admin")
	// Re-granting after revoke should succeed (inserts a new row).
	if err := as.GrantAuthorization(ctx, "ma-uuid-004", "mod-004", "", nil, ""); err != nil {
		t.Fatalf("re-grant after revoke: %v", err)
	}
}

func TestModuleAuthStore_Grant_WithExpiry(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	as := sqlitestore.NewModuleAuthorizationStore(conn, w)
	ctx := context.Background()

	seedModule(t, conn, "mod-005")
	seedMember(t, conn, "ma-uuid-005")

	exp := time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)
	_ = as.GrantAuthorization(ctx, "ma-uuid-005", "mod-005", "", &exp, "")

	rec, err := as.GetAuthorization(ctx, "ma-uuid-005", "mod-005")
	if err != nil {
		t.Fatalf("GetAuthorization: %v", err)
	}
	if rec.ExpiresAt == nil || !rec.ExpiresAt.Equal(exp) {
		t.Errorf("unexpected ExpiresAt: %v", rec.ExpiresAt)
	}
}

func TestModuleAuthStore_Grant_WithTimeRestriction(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	as := sqlitestore.NewModuleAuthorizationStore(conn, w)
	ctx := context.Background()

	seedModule(t, conn, "mod-006")
	seedMember(t, conn, "ma-uuid-006")

	policy := `{"days":["mon","tue","wed","thu","fri"],"start":"08:00","end":"18:00"}`
	_ = as.GrantAuthorization(ctx, "ma-uuid-006", "mod-006", "", nil, policy)

	rec, _ := as.GetAuthorization(ctx, "ma-uuid-006", "mod-006")
	if rec.TimeRestriction != policy {
		t.Errorf("unexpected TimeRestriction: %q", rec.TimeRestriction)
	}
}

// ── RevokeAuthorization ───────────────────────────────────────────────────────

func TestModuleAuthStore_Revoke_SetsTimestamp(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	as := sqlitestore.NewModuleAuthorizationStore(conn, w)
	ctx := context.Background()

	seedModule(t, conn, "mod-007")
	seedMember(t, conn, "ma-uuid-007")

	_ = as.GrantAuthorization(ctx, "ma-uuid-007", "mod-007", "", nil, "")
	if err := as.RevokeAuthorization(ctx, "ma-uuid-007", "mod-007", "admin-uuid"); err != nil {
		t.Fatalf("RevokeAuthorization: %v", err)
	}

	rec, _ := as.GetAuthorization(ctx, "ma-uuid-007", "mod-007")
	if rec.RevokedAt == nil {
		t.Error("expected revoked_at_ms to be set")
	}
	if rec.RevokedByUUID != "admin-uuid" {
		t.Errorf("unexpected revoked_by_uuid: %q", rec.RevokedByUUID)
	}
}

func TestModuleAuthStore_Revoke_NotFound(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	as := sqlitestore.NewModuleAuthorizationStore(conn, w)

	err := as.RevokeAuthorization(context.Background(), "ghost", "ghost-mod", "")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ── GetAuthorization ──────────────────────────────────────────────────────────

func TestModuleAuthStore_GetAuthorization_NotFound(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	as := sqlitestore.NewModuleAuthorizationStore(conn, w)

	_, err := as.GetAuthorization(context.Background(), "nobody", "nomod")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ── ListByMember / ListByModule ───────────────────────────────────────────────

func TestModuleAuthStore_ListByMember(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	as := sqlitestore.NewModuleAuthorizationStore(conn, w)
	ctx := context.Background()

	seedModule(t, conn, "mod-010")
	seedModule(t, conn, "mod-011")
	seedMember(t, conn, "ma-uuid-010")

	_ = as.GrantAuthorization(ctx, "ma-uuid-010", "mod-010", "", nil, "")
	_ = as.GrantAuthorization(ctx, "ma-uuid-010", "mod-011", "", nil, "")

	list, err := as.ListByMember(ctx, "ma-uuid-010")
	if err != nil {
		t.Fatalf("ListByMember: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 authorizations, got %d", len(list))
	}
}

func TestModuleAuthStore_ListByModule(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	as := sqlitestore.NewModuleAuthorizationStore(conn, w)
	ctx := context.Background()

	seedModule(t, conn, "mod-020")
	seedMember(t, conn, "ma-uuid-020")
	seedMember(t, conn, "ma-uuid-021")

	_ = as.GrantAuthorization(ctx, "ma-uuid-020", "mod-020", "", nil, "")
	_ = as.GrantAuthorization(ctx, "ma-uuid-021", "mod-020", "", nil, "")

	list, err := as.ListByModule(ctx, "mod-020")
	if err != nil {
		t.Fatalf("ListByModule: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 authorizations, got %d", len(list))
	}
}

// ── cascade delete ────────────────────────────────────────────────────────────

func TestModuleAuthStore_CascadeOnMemberDelete(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	as := sqlitestore.NewModuleAuthorizationStore(conn, w)
	ctx := context.Background()

	seedModule(t, conn, "mod-030")
	seedMember(t, conn, "ma-uuid-030")
	_ = as.GrantAuthorization(ctx, "ma-uuid-030", "mod-030", "", nil, "")

	_, err := conn.ExecContext(ctx, `DELETE FROM member_access WHERE uuid = 'ma-uuid-030'`)
	if err != nil {
		t.Fatalf("delete member: %v", err)
	}

	var count int
	conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM module_authorizations WHERE member_uuid = 'ma-uuid-030'`,
	).Scan(&count)
	if count != 0 {
		t.Errorf("expected cascade delete of authorizations, got %d rows", count)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// seedMember inserts a bare member_access row to satisfy FK constraints on
// module_authorizations.
func seedMember(t *testing.T, conn *sql.DB, uuid string) {
	t.Helper()
	nowMs := time.Now().UTC().UnixMilli()
	_, err := conn.ExecContext(context.Background(), `
INSERT OR IGNORE INTO member_access(uuid, role_id, status, enabled, provisioning_status, created_at_ms)
VALUES (?, 'member', 'active', 1, 'active', ?);`, uuid, nowMs)
	if err != nil {
		t.Fatalf("seedMember(%s): %v", uuid, err)
	}
}
