package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// allPerms returns a permission set containing every known permission,
// representing a superuser caller for tests that aren't exercising the subset guard.
func allPerms() map[string]struct{} {
	m := make(map[string]struct{})
	for _, p := range permissions.All() {
		m[p] = struct{}{}
	}
	return m
}

func TestRoleService_SetPermissions_AdminRoleImmutable(t *testing.T) {
	dbConn, writer := openSvcTestDB(t)
	rs := sqlitestore.NewRoleStore(dbConn, writer)
	svc := service.NewRoleService(rs)
	ctx := context.Background()

	err := svc.SetPermissions(ctx, allPerms(), "admin", []string{"door.unlock_remote"})
	if !errors.Is(err, service.ErrAdminRoleImmutable) {
		t.Fatalf("expected ErrAdminRoleImmutable, got: %v", err)
	}
}

func TestRoleService_SetPermissions_AdminRoleImmutable_Empty(t *testing.T) {
	dbConn, writer := openSvcTestDB(t)
	rs := sqlitestore.NewRoleStore(dbConn, writer)
	svc := service.NewRoleService(rs)
	ctx := context.Background()

	// Even clearing the admin role's permissions must be blocked.
	err := svc.SetPermissions(ctx, allPerms(), "admin", []string{})
	if !errors.Is(err, service.ErrAdminRoleImmutable) {
		t.Fatalf("expected ErrAdminRoleImmutable for empty perm list, got: %v", err)
	}
}

func TestRoleService_SetPermissions_OtherRolesEditable(t *testing.T) {
	dbConn, writer := openSvcTestDB(t)
	rs := sqlitestore.NewRoleStore(dbConn, writer)
	svc := service.NewRoleService(rs)
	ctx := context.Background()

	if err := svc.SetPermissions(ctx, allPerms(), "operator", []string{"module.list"}); err != nil {
		t.Fatalf("unexpected error setting permissions on operator role: %v", err)
	}
	if err := svc.SetPermissions(ctx, allPerms(), "viewer", []string{"audit_log.list"}); err != nil {
		t.Fatalf("unexpected error setting permissions on viewer role: %v", err)
	}
}

func TestRoleService_SetPermissions_SubsetViolation(t *testing.T) {
	dbConn, writer := openSvcTestDB(t)
	rs := sqlitestore.NewRoleStore(dbConn, writer)
	svc := service.NewRoleService(rs)
	ctx := context.Background()

	// Caller holds only "audit_log.list"; trying to grant "module.list" must fail.
	callerPerms := map[string]struct{}{"audit_log.list": {}}
	err := svc.SetPermissions(ctx, callerPerms, "operator", []string{"module.list"})
	if !errors.Is(err, service.ErrPermissionSubsetViolation) {
		t.Fatalf("expected ErrPermissionSubsetViolation, got: %v", err)
	}
}
