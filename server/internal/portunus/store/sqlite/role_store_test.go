package sqlite_test

import (
	"context"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// ── Migration: default roles seeded ──────────────────────────────────────────

func TestMigration_DefaultRolesExist(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	expected := []string{"admin", "operator", "viewer", "member", "guest"}
	for _, id := range expected {
		var count int
		err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM roles WHERE role_id = ?`, id).Scan(&count)
		if err != nil {
			t.Fatalf("query role %q: %v", id, err)
		}
		if count != 1 {
			t.Errorf("expected role %q to exist after migration, count=%d", id, count)
		}
	}
}

func TestMigration_SystemRolesFlagged(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	systemRoles := []string{"admin", "operator", "viewer"}
	for _, id := range systemRoles {
		var isSystem int
		err := conn.QueryRowContext(ctx, `SELECT is_system FROM roles WHERE role_id = ?`, id).Scan(&isSystem)
		if err != nil {
			t.Fatalf("query is_system for %q: %v", id, err)
		}
		if isSystem != 1 {
			t.Errorf("expected role %q to have is_system=1, got %d", id, isSystem)
		}
	}
}

func TestMigration_NonSystemRoles(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	for _, id := range []string{"member", "guest"} {
		var isSystem int
		err := conn.QueryRowContext(ctx, `SELECT is_system FROM roles WHERE role_id = ?`, id).Scan(&isSystem)
		if err != nil {
			t.Fatalf("query is_system for %q: %v", id, err)
		}
		if isSystem != 0 {
			t.Errorf("expected role %q to have is_system=0, got %d", id, isSystem)
		}
	}
}

// ── CreateRole ────────────────────────────────────────────────────────────────

func TestRoleStore_CreateRole_InsertsRow(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)
	ctx := context.Background()

	if err := rs.CreateRole(ctx, "custom", "Custom Role", "A test role", nil, nil); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM roles WHERE role_id = 'custom'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

func TestRoleStore_CreateRole_SetsIsSystemFalse(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)
	ctx := context.Background()

	_ = rs.CreateRole(ctx, "custom2", "Another Role", "", nil, nil)

	var isSystem int
	conn.QueryRowContext(ctx, `SELECT is_system FROM roles WHERE role_id = 'custom2'`).Scan(&isSystem)
	if isSystem != 0 {
		t.Errorf("expected is_system=0 for user-created role, got %d", isSystem)
	}
}

func TestRoleStore_CreateRole_WithDefaultPolicies(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)
	ctx := context.Background()

	expiry := 30
	inactivity := 90
	_ = rs.CreateRole(ctx, "policy_role", "Policy Role", "", &expiry, &inactivity)

	rec, err := rs.GetRole(ctx, "policy_role")
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	if rec.DefaultExpiryDays == nil || *rec.DefaultExpiryDays != 30 {
		t.Errorf("unexpected DefaultExpiryDays: %v", rec.DefaultExpiryDays)
	}
	if rec.DefaultInactivityDays == nil || *rec.DefaultInactivityDays != 90 {
		t.Errorf("unexpected DefaultInactivityDays: %v", rec.DefaultInactivityDays)
	}
}

// ── GetRole ───────────────────────────────────────────────────────────────────

func TestRoleStore_GetRole_NotFound(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)

	_, err := rs.GetRole(context.Background(), "nonexistent")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRoleStore_GetRole_SystemRole(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)

	rec, err := rs.GetRole(context.Background(), "admin")
	if err != nil {
		t.Fatalf("GetRole(admin): %v", err)
	}
	if !rec.IsSystem {
		t.Error("expected admin role to be marked is_system")
	}
}

// ── ListRoles ─────────────────────────────────────────────────────────────────

func TestRoleStore_ListRoles_IncludesSeeded(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)

	roles, err := rs.ListRoles(context.Background())
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(roles) < 5 {
		t.Errorf("expected at least 5 roles (seeded), got %d", len(roles))
	}
}

// ── DeleteRole ────────────────────────────────────────────────────────────────

func TestRoleStore_DeleteRole_SystemRoleBlocked(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)

	err := rs.DeleteRole(context.Background(), "admin")
	if err != store.ErrRoleIsSystem {
		t.Errorf("expected ErrRoleIsSystem deleting admin, got %v", err)
	}
}

func TestRoleStore_DeleteRole_NonSystemDeleted(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)
	ctx := context.Background()

	_ = rs.CreateRole(ctx, "temp_role", "Temp", "", nil, nil)
	if err := rs.DeleteRole(ctx, "temp_role"); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if _, err := rs.GetRole(ctx, "temp_role"); err != store.ErrNotFound {
		t.Errorf("expected role to be gone, GetRole returned %v", err)
	}
}

func TestRoleStore_DeleteRole_NotFound(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)

	err := rs.DeleteRole(context.Background(), "ghost")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ── SetRolePermissions / GetRolePermissions ───────────────────────────────────

func TestRoleStore_Permissions_RoundTrip(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)
	ctx := context.Background()

	perms := []string{"door.unlock_remote", "member.view"}
	if err := rs.SetRolePermissions(ctx, "admin", perms); err != nil {
		t.Fatalf("SetRolePermissions: %v", err)
	}

	got, err := rs.GetRolePermissions(ctx, "admin")
	if err != nil {
		t.Fatalf("GetRolePermissions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 permissions, got %d: %v", len(got), got)
	}
	// Results are sorted lexicographically.
	if got[0] != "door.unlock_remote" || got[1] != "member.view" {
		t.Errorf("unexpected permissions: %v", got)
	}
}

func TestRoleStore_Permissions_ReplaceIsFull(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)
	ctx := context.Background()

	_ = rs.SetRolePermissions(ctx, "operator", []string{"door.unlock_remote", "member.view"})
	_ = rs.SetRolePermissions(ctx, "operator", []string{"member.view"})

	got, _ := rs.GetRolePermissions(ctx, "operator")
	if len(got) != 1 || got[0] != "member.view" {
		t.Errorf("expected only member.view after replace, got %v", got)
	}
}

func TestRoleStore_Permissions_ClearAll(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)
	ctx := context.Background()

	_ = rs.SetRolePermissions(ctx, "viewer", []string{"audit.view"})
	_ = rs.SetRolePermissions(ctx, "viewer", nil)

	got, _ := rs.GetRolePermissions(ctx, "viewer")
	if len(got) != 0 {
		t.Errorf("expected empty permission list, got %v", got)
	}
}

func TestRoleStore_Permissions_DeleteCascades(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	rs := sqlitestore.NewRoleStore(conn, w)
	ctx := context.Background()

	_ = rs.CreateRole(ctx, "cascade_role", "Cascade", "", nil, nil)
	_ = rs.SetRolePermissions(ctx, "cascade_role", []string{"door.unlock_remote"})
	_ = rs.DeleteRole(ctx, "cascade_role")

	var count int
	conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM role_permissions WHERE role_id = 'cascade_role'`).Scan(&count)
	if count != 0 {
		t.Errorf("expected role_permissions to cascade-delete, got %d rows", count)
	}
}
