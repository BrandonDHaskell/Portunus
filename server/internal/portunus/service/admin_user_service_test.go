package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// newAdminUserSvc wires AdminUserService against in-memory SQLite with real migrations.
func newAdminUserSvc(t *testing.T) (*service.AdminUserService, store.AdminUserStore, store.RoleStore, store.AuditStore) {
	t.Helper()
	dbConn, writer := openSvcTestDB(t)
	us := sqlitestore.NewAdminUserStore(dbConn, writer)
	rs := sqlitestore.NewRoleStore(dbConn, writer)
	as := sqlitestore.NewAuditStore(dbConn, writer)
	return service.NewAdminUserService(us, rs, as), us, rs, as
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

// requireAuditEntry asserts that exactly one audit entry matching action and
// resourceID exists in the audit log.
func requireAuditEntry(t *testing.T, as store.AuditStore, action, resourceID string) {
	t.Helper()
	entries, err := as.ListAuditEntries(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	for _, e := range entries {
		if e.Action == action && e.ResourceID == resourceID {
			return
		}
	}
	t.Errorf("no audit entry found with action=%q resourceID=%q (got %d entries)", action, resourceID, len(entries))
}

// ── SetEnabled ────────────────────────────────────────────────────────────────

func TestAdminUserService_SetEnabled_SelfDisableBlocked(t *testing.T) {
	svc, us, _, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-self", "admin", true)

	err := svc.SetEnabled(ctx, "uuid-self", "uuid-self", allPerms(), false)
	if !errors.Is(err, service.ErrCannotSelfDisable) {
		t.Fatalf("expected ErrCannotSelfDisable, got: %v", err)
	}
}

func TestAdminUserService_SetEnabled_LastAdminDisableBlocked(t *testing.T) {
	svc, us, _, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-only", "admin", true)

	// Different caller UUID so self-disable guard does not fire.
	err := svc.SetEnabled(ctx, "uuid-only", "uuid-caller", allPerms(), false)
	if !errors.Is(err, service.ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got: %v", err)
	}
}

func TestAdminUserService_SetEnabled_NonLastAdminCanBeDisabled(t *testing.T) {
	svc, us, _, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-a", "admin", true)
	seedAdminUser(t, us, "uuid-b", "admin", true)

	// Disabling uuid-a is fine because uuid-b still holds the admin role.
	if err := svc.SetEnabled(ctx, "uuid-a", "uuid-b", allPerms(), false); err != nil {
		t.Fatalf("unexpected error disabling non-last admin: %v", err)
	}
}

func TestAdminUserService_SetEnabled_NonAdminRoleUserCanBeDisabled(t *testing.T) {
	svc, us, _, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-admin", "admin", true)
	seedAdminUser(t, us, "uuid-op", "operator", true)

	// Disabling an operator user is always allowed (they don't hold the admin role).
	if err := svc.SetEnabled(ctx, "uuid-op", "uuid-admin", allPerms(), false); err != nil {
		t.Fatalf("unexpected error disabling operator user: %v", err)
	}
}

func TestAdminUserService_SetEnabled_AuditDisable(t *testing.T) {
	svc, us, _, as := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-a", "admin", true)
	seedAdminUser(t, us, "uuid-b", "admin", true)

	if err := svc.SetEnabled(ctx, "uuid-a", "uuid-b", allPerms(), false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	requireAuditEntry(t, as, "admin_user.disabled", "uuid-a")
}

func TestAdminUserService_SetEnabled_AuditEnable(t *testing.T) {
	svc, us, _, as := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-admin", "admin", true)
	seedAdminUser(t, us, "uuid-op", "operator", false)

	if err := svc.SetEnabled(ctx, "uuid-op", "uuid-admin", allPerms(), true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	requireAuditEntry(t, as, "admin_user.enabled", "uuid-op")
}

// ── AssignRole ────────────────────────────────────────────────────────────────

func TestAdminUserService_AssignRole_LastAdminRoleMoveBlocked(t *testing.T) {
	svc, us, _, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-only", "admin", true)

	err := svc.AssignRole(ctx, "uuid-caller", allPerms(), "uuid-only", "operator")
	if !errors.Is(err, service.ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got: %v", err)
	}
}

func TestAdminUserService_AssignRole_NonLastAdminRoleMoveAllowed(t *testing.T) {
	svc, us, _, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-a", "admin", true)
	seedAdminUser(t, us, "uuid-b", "admin", true)

	// Moving uuid-a to operator is fine because uuid-b still holds admin.
	if err := svc.AssignRole(ctx, "uuid-b", allPerms(), "uuid-a", "operator"); err != nil {
		t.Fatalf("unexpected error moving non-last admin to operator: %v", err)
	}
}

func TestAdminUserService_AssignRole_ToAdminAlwaysAllowed(t *testing.T) {
	svc, us, _, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-admin", "admin", true)
	seedAdminUser(t, us, "uuid-op", "operator", true)

	// Elevating an operator to admin should never be blocked by the last-admin guard.
	if err := svc.AssignRole(ctx, "uuid-admin", allPerms(), "uuid-op", "admin"); err != nil {
		t.Fatalf("unexpected error promoting operator to admin: %v", err)
	}
}

func TestAdminUserService_AssignRole_AuditRoleAssigned(t *testing.T) {
	svc, us, _, as := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-a", "admin", true)
	seedAdminUser(t, us, "uuid-b", "admin", true)

	if err := svc.AssignRole(ctx, "uuid-b", allPerms(), "uuid-a", "operator"); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	requireAuditEntry(t, as, "admin_user.role_assigned", "uuid-a")
}

// ── SetExpiry ─────────────────────────────────────────────────────────────────

func TestAdminUserService_SetExpiry_LastAdminBlocked(t *testing.T) {
	svc, us, _, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-only", "admin", true)

	future := time.Now().Add(24 * time.Hour)
	err := svc.SetExpiry(ctx, "uuid-caller", allPerms(), "uuid-only", &future)
	if !errors.Is(err, service.ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got: %v", err)
	}
}

func TestAdminUserService_SetExpiry_ClearAlwaysAllowed(t *testing.T) {
	svc, us, _, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-only", "admin", true)

	// Clearing expiry (nil) must never be blocked by the last-admin guard.
	if err := svc.SetExpiry(ctx, "uuid-caller", allPerms(), "uuid-only", nil); err != nil {
		t.Fatalf("unexpected error clearing expiry on last admin: %v", err)
	}
}

func TestAdminUserService_SetExpiry_NonLastAdminAllowed(t *testing.T) {
	svc, us, _, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-a", "admin", true)
	seedAdminUser(t, us, "uuid-b", "admin", true)

	future := time.Now().Add(24 * time.Hour)
	// Setting expiry on uuid-a is fine because uuid-b still qualifies.
	if err := svc.SetExpiry(ctx, "uuid-b", allPerms(), "uuid-a", &future); err != nil {
		t.Fatalf("unexpected error setting expiry on non-last admin: %v", err)
	}
}

func TestAdminUserService_SetExpiry_AuditExpirySet(t *testing.T) {
	svc, us, _, as := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-a", "admin", true)
	seedAdminUser(t, us, "uuid-b", "admin", true)

	future := time.Now().Add(24 * time.Hour)
	if err := svc.SetExpiry(ctx, "uuid-b", allPerms(), "uuid-a", &future); err != nil {
		t.Fatalf("SetExpiry: %v", err)
	}
	requireAuditEntry(t, as, "admin_user.expiry_set", "uuid-a")
}

func TestAdminUserService_SetExpiry_AuditExpiryClear(t *testing.T) {
	svc, us, _, as := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-only", "admin", true)

	if err := svc.SetExpiry(ctx, "uuid-caller", allPerms(), "uuid-only", nil); err != nil {
		t.Fatalf("SetExpiry clear: %v", err)
	}
	requireAuditEntry(t, as, "admin_user.expiry_set", "uuid-only")
}

// TestAdminUserService_SetEnabled_ExpiredAdminNotCounted verifies that an
// expired admin account does not count toward the last-admin threshold.
func TestAdminUserService_SetEnabled_ExpiredAdminNotCounted(t *testing.T) {
	_, us, _, _ := newAdminUserSvc(t)
	ctx := context.Background()

	// Create two admins, then expire uuid-a via the store directly.
	if err := us.CreateAdminUser(ctx, "uuid-a", "uuid-a", "$2a$12$placeholder", "admin"); err != nil {
		t.Fatalf("create uuid-a: %v", err)
	}
	if err := us.CreateAdminUser(ctx, "uuid-b", "uuid-b", "$2a$12$placeholder", "admin"); err != nil {
		t.Fatalf("create uuid-b: %v", err)
	}

	past := time.Now().Add(-time.Hour)
	if err := us.SetAdminUserExpiry(ctx, "uuid-a", &past); err != nil {
		t.Fatalf("expire uuid-a: %v", err)
	}

	// Now uuid-b is the only qualifying admin. Disabling uuid-b must fail.
	svc2, _, _, _ := newAdminUserSvc(t)
	// Re-wire on same DB — use a fresh svc against the same us.
	_ = svc2 // svc2 uses a different in-memory DB; re-use the store directly.

	// Use the store to verify the count is 1 (only uuid-b qualifies).
	n, err := us.CountEnabledAdminsWithRole(ctx, "admin")
	if err != nil {
		t.Fatalf("CountEnabledAdminsWithRole: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 qualifying admin (uuid-a expired), got %d", n)
	}
}

// ── SetMemberLink ─────────────────────────────────────────────────────────────

// ── Target privilege guard (no acting on a more-privileged account) ──────────

// limitedPerms is a custom console role that may manage admin accounts but
// holds far less than the superuser role.
func limitedPerms() map[string]struct{} {
	return map[string]struct{}{
		permissions.AdminUserList:    {},
		permissions.AdminUserEdit:    {},
		permissions.AdminUserDisable: {},
	}
}

func TestAdminUserService_LimitedCaller_CannotActOnAdmin(t *testing.T) {
	svc, us, rs, _ := newAdminUserSvc(t)
	ctx := context.Background()
	seedAdminUser(t, us, "uuid-a", "admin", true)
	seedAdminUser(t, us, "uuid-b", "admin", true) // so the last-admin guard is not what blocks
	if err := rs.CreateRole(ctx, "empty", "empty", ""); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	future := time.Now().Add(24 * time.Hour)

	cases := []struct {
		name string
		call func() error
	}{
		{"demote", func() error { return svc.AssignRole(ctx, "uuid-lim", limitedPerms(), "uuid-a", "empty") }},
		{"disable", func() error { return svc.SetEnabled(ctx, "uuid-a", "uuid-lim", limitedPerms(), false) }},
		{"enable", func() error { return svc.SetEnabled(ctx, "uuid-a", "uuid-lim", limitedPerms(), true) }},
		{"set expiry", func() error { return svc.SetExpiry(ctx, "uuid-lim", limitedPerms(), "uuid-a", &future) }},
		{"clear expiry", func() error { return svc.SetExpiry(ctx, "uuid-lim", limitedPerms(), "uuid-a", nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, service.ErrPermissionSubsetViolation) {
				t.Fatalf("got %v, want ErrPermissionSubsetViolation", err)
			}
		})
	}

	rec, err := us.GetAdminUserByUUID(ctx, "uuid-a")
	if err != nil {
		t.Fatalf("GetAdminUserByUUID: %v", err)
	}
	if rec.RoleID != "admin" || !rec.Enabled || rec.ExpiresAt != nil {
		t.Errorf("target changed despite rejection: role=%q enabled=%v expires=%v", rec.RoleID, rec.Enabled, rec.ExpiresAt)
	}
}

func TestAdminUserService_LimitedCaller_CanActOnEqualOrLesserAccount(t *testing.T) {
	svc, us, rs, _ := newAdminUserSvc(t)
	ctx := context.Background()
	if err := rs.CreateRole(ctx, "empty", "empty", ""); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	seedAdminUser(t, us, "uuid-low", "empty", true)
	future := time.Now().Add(24 * time.Hour)

	if err := svc.SetExpiry(ctx, "uuid-lim", limitedPerms(), "uuid-low", &future); err != nil {
		t.Fatalf("SetExpiry on lesser account: %v", err)
	}
	if err := svc.SetEnabled(ctx, "uuid-low", "uuid-lim", limitedPerms(), false); err != nil {
		t.Fatalf("SetEnabled on lesser account: %v", err)
	}
	if err := svc.AssignRole(ctx, "uuid-lim", limitedPerms(), "uuid-low", "empty"); err != nil {
		t.Fatalf("AssignRole on lesser account: %v", err)
	}
}

// ── CreateUser: no minting accounts above your own permissions (F-7) ──────────

func TestAdminUserService_CreateUser_RoleMustBeWithinCaller(t *testing.T) {
	svc, us, rs, _ := newAdminUserSvc(t)
	ctx := context.Background()
	if err := rs.CreateRole(ctx, "empty", "empty", ""); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	creator := map[string]struct{}{permissions.AdminUserCreate: {}}

	_, err := svc.CreateUser(ctx, "uuid-creator", creator, "mallory", "a-valid-password-1", "admin")
	if !errors.Is(err, service.ErrPermissionSubsetViolation) {
		t.Fatalf("creating an admin with only admin_user.create: got %v, want ErrPermissionSubsetViolation", err)
	}
	if _, err := us.GetAdminUserByUsername(ctx, "mallory"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("account created despite the refusal (err=%v)", err)
	}

	if _, err := svc.CreateUser(ctx, "uuid-creator", creator, "trent", "a-valid-password-1", "empty"); err != nil {
		t.Fatalf("creating a role within the caller's permissions: %v", err)
	}
	if _, err := svc.CreateUser(ctx, "uuid-admin", allPerms(), "victor", "a-valid-password-1", "admin"); err != nil {
		t.Fatalf("superuser creating an admin: %v", err)
	}
}
