package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// newAdminUserSvc wires AdminUserService against in-memory SQLite with real migrations.
func newAdminUserSvc(t *testing.T) (*service.AdminUserService, store.AdminUserStore, store.RoleStore) {
	t.Helper()
	dbConn, writer := openSvcTestDB(t)
	us := sqlitestore.NewAdminUserStore(dbConn, writer)
	rs := sqlitestore.NewRoleStore(dbConn, writer)
	return service.NewAdminUserService(us, rs), us, rs
}

// seedAdminUser inserts a minimal admin_users row via the store, bypassing bcrypt.
func seedAdminUser(t *testing.T, us store.AdminUserStore, uuid, roleID string, enabled bool) {
	t.Helper()
	ctx := context.Background()
	if err := us.CreateAdminUser(ctx, uuid, uuid, "$2a$12$placeholder", roleID); err != nil {
		t.Fatalf("seedAdminUser %q: %v", uuid, err)
	}
	if !enabled {
		if err := us.SetAdminUserEnabled(ctx, uuid, false); err != nil {
			t.Fatalf("seedAdminUser disable %q: %v", uuid, err)
		}
	}
}

// ── SetEnabled ────────────────────────────────────────────────────────────────

func TestAdminUserService_SetEnabled_SelfDisableBlocked(t *testing.T) {
	svc, us, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-self", "admin", true)

	err := svc.SetEnabled(ctx, "uuid-self", "uuid-self", false)
	if !errors.Is(err, service.ErrCannotSelfDisable) {
		t.Fatalf("expected ErrCannotSelfDisable, got: %v", err)
	}
}

func TestAdminUserService_SetEnabled_LastAdminDisableBlocked(t *testing.T) {
	svc, us, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-only", "admin", true)

	// Different caller UUID so self-disable guard does not fire.
	err := svc.SetEnabled(ctx, "uuid-only", "uuid-caller", false)
	if !errors.Is(err, service.ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got: %v", err)
	}
}

func TestAdminUserService_SetEnabled_NonLastAdminCanBeDisabled(t *testing.T) {
	svc, us, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-a", "admin", true)
	seedAdminUser(t, us, "uuid-b", "admin", true)

	// Disabling uuid-a is fine because uuid-b still holds the admin role.
	if err := svc.SetEnabled(ctx, "uuid-a", "uuid-b", false); err != nil {
		t.Fatalf("unexpected error disabling non-last admin: %v", err)
	}
}

func TestAdminUserService_SetEnabled_NonAdminRoleUserCanBeDisabled(t *testing.T) {
	svc, us, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-admin", "admin", true)
	seedAdminUser(t, us, "uuid-op", "operator", true)

	// Disabling an operator user is always allowed (they don't hold the admin role).
	if err := svc.SetEnabled(ctx, "uuid-op", "uuid-admin", false); err != nil {
		t.Fatalf("unexpected error disabling operator user: %v", err)
	}
}

// ── AssignRole ────────────────────────────────────────────────────────────────

func TestAdminUserService_AssignRole_LastAdminRoleMoveBlocked(t *testing.T) {
	svc, us, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-only", "admin", true)

	err := svc.AssignRole(ctx, "uuid-only", "operator")
	if !errors.Is(err, service.ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got: %v", err)
	}
}

func TestAdminUserService_AssignRole_NonLastAdminRoleMoveAllowed(t *testing.T) {
	svc, us, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-a", "admin", true)
	seedAdminUser(t, us, "uuid-b", "admin", true)

	// Moving uuid-a to operator is fine because uuid-b still holds admin.
	if err := svc.AssignRole(ctx, "uuid-a", "operator"); err != nil {
		t.Fatalf("unexpected error moving non-last admin to operator: %v", err)
	}
}

func TestAdminUserService_AssignRole_ToAdminAlwaysAllowed(t *testing.T) {
	svc, us, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-admin", "admin", true)
	seedAdminUser(t, us, "uuid-op", "operator", true)

	// Elevating an operator to admin should never be blocked by the last-admin guard.
	if err := svc.AssignRole(ctx, "uuid-op", "admin"); err != nil {
		t.Fatalf("unexpected error promoting operator to admin: %v", err)
	}
}
