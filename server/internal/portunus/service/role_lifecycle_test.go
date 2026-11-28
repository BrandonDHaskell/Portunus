package service_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

func newRoleFixture(t *testing.T) (*service.RoleService, *sqlitestore.RoleStore, *sqlitestore.AdminUserStore) {
	t.Helper()
	conn, w := openSvcTestDB(t)
	rs := sqlitestore.NewRoleStore(conn, w)
	return service.NewRoleService(rs), rs, sqlitestore.NewAdminUserStore(conn, w)
}

func TestRoles_CreateGetUpdateDelete(t *testing.T) {
	svc, _, _ := newRoleFixture(t)
	ctx := context.Background()

	info, err := svc.CreateRole(ctx, "  Front Desk - Weekends ", " weekend staff ")
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if info.RoleID != "front_desk_weekends" || info.Name != "Front Desk - Weekends" || info.Description != "weekend staff" || info.IsSystem {
		t.Fatalf("created = %+v", info)
	}
	if _, err := svc.CreateRole(ctx, "front desk weekends", ""); err == nil {
		t.Error("duplicate role id accepted")
	}

	perms := []string{permissions.MemberList, permissions.MemberView}
	if err := svc.SetPermissions(ctx, allPerms(), info.RoleID, perms); err != nil {
		t.Fatalf("SetPermissions: %v", err)
	}
	got, err := svc.GetRole(ctx, info.RoleID)
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	sort.Strings(got.Permissions)
	sort.Strings(perms)
	if strings.Join(got.Permissions, ",") != strings.Join(perms, ",") {
		t.Errorf("permissions = %v, want %v", got.Permissions, perms)
	}

	if err := svc.UpdateRole(ctx, info.RoleID, "Front Desk", "renamed"); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	got, _ = svc.GetRole(ctx, info.RoleID)
	if got.Name != "Front Desk" || got.Description != "renamed" || got.RoleID != info.RoleID {
		t.Errorf("after update = %+v (role id must not change)", got)
	}

	if err := svc.DeleteRole(ctx, info.RoleID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if _, err := svc.GetRole(ctx, info.RoleID); !errors.Is(err, service.ErrRoleNotFound) {
		t.Errorf("deleted role still readable: %v", err)
	}
}

func TestRoles_CreateRejectsUnusableNames(t *testing.T) {
	svc, _, _ := newRoleFixture(t)
	for _, name := range []string{"", "   ", "!!!", "---"} {
		if _, err := svc.CreateRole(context.Background(), name, ""); err == nil {
			t.Errorf("CreateRole(%q) accepted", name)
		}
	}
}

func TestRoles_SystemRolesProtected(t *testing.T) {
	svc, _, _ := newRoleFixture(t)
	ctx := context.Background()
	roles, err := svc.ListRoles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var system []string
	for _, r := range roles {
		if r.IsSystem {
			system = append(system, r.RoleID)
		}
	}
	if len(system) == 0 {
		t.Fatal("no system roles seeded")
	}
	for _, id := range system {
		if err := svc.DeleteRole(ctx, id); !errors.Is(err, store.ErrRoleIsSystem) {
			t.Errorf("DeleteRole(%s): got %v, want ErrRoleIsSystem", id, err)
		}
		if err := svc.UpdateRole(ctx, id, "renamed", ""); !errors.Is(err, store.ErrRoleIsSystem) {
			t.Errorf("UpdateRole(%s): got %v, want ErrRoleIsSystem", id, err)
		}
	}
}

// A role still assigned to an account cannot be deleted out from under it.
func TestRoles_DeleteInUseRefused(t *testing.T) {
	svc, _, users := newRoleFixture(t)
	ctx := context.Background()
	info, err := svc.CreateRole(ctx, "contractors", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := users.CreateAdminUser(ctx, "uuid-c", "carl", "x", info.RoleID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteRole(ctx, info.RoleID); err == nil {
		t.Fatal("deleted a role that is still assigned")
	}
	rec, err := users.GetAdminUserByUUID(ctx, "uuid-c")
	if err != nil || rec.RoleID != info.RoleID {
		t.Fatalf("account's role changed: %+v, %v", rec, err)
	}
}

func TestRoles_NotFound(t *testing.T) {
	svc, _, _ := newRoleFixture(t)
	ctx := context.Background()
	if _, err := svc.GetRole(ctx, "nope"); !errors.Is(err, service.ErrRoleNotFound) {
		t.Errorf("GetRole: %v", err)
	}
	if err := svc.UpdateRole(ctx, "nope", "x", ""); !errors.Is(err, service.ErrRoleNotFound) {
		t.Errorf("UpdateRole: %v", err)
	}
	if err := svc.DeleteRole(ctx, "nope"); !errors.Is(err, service.ErrRoleNotFound) {
		t.Errorf("DeleteRole: %v", err)
	}
	if err := svc.UpdateRole(ctx, "nope", "  ", ""); err == nil {
		t.Error("UpdateRole with blank name accepted")
	}
	if err := svc.SetPermissions(ctx, allPerms(), "nope", []string{permissions.MemberList}); err == nil {
		t.Error("SetPermissions on a missing role accepted")
	}
}

func TestRoles_SetPermissionsReplacesAndClears(t *testing.T) {
	svc, _, _ := newRoleFixture(t)
	ctx := context.Background()
	info, _ := svc.CreateRole(ctx, "shift lead", "")

	if err := svc.SetPermissions(ctx, allPerms(), info.RoleID, []string{permissions.MemberList, permissions.DoorList}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPermissions(ctx, allPerms(), info.RoleID, []string{permissions.ModuleList}); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetPermissions(ctx, info.RoleID)
	if len(got) != 1 || got[0] != permissions.ModuleList {
		t.Fatalf("after replace = %v, want only %s", got, permissions.ModuleList)
	}
	if err := svc.SetPermissions(ctx, allPerms(), info.RoleID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetPermissions(ctx, info.RoleID); len(got) != 0 {
		t.Fatalf("after clear = %v, want none", got)
	}
}

// Submitting the same permission twice (a doubled form field) must not fail
// the whole update.
func TestRoles_SetPermissionsToleratesDuplicates(t *testing.T) {
	svc, _, _ := newRoleFixture(t)
	ctx := context.Background()
	info, _ := svc.CreateRole(ctx, "dupes", "")
	if err := svc.SetPermissions(ctx, allPerms(), info.RoleID, []string{permissions.MemberList, permissions.MemberList}); err != nil {
		t.Fatalf("duplicate permission in request: %v", err)
	}
	if got, _ := svc.GetPermissions(ctx, info.RoleID); len(got) != 1 {
		t.Fatalf("permissions = %v, want one entry", got)
	}
}
