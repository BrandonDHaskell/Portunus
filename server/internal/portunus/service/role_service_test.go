package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

func TestRoleService_SetPermissions_AdminRoleImmutable(t *testing.T) {
	dbConn, writer := openSvcTestDB(t)
	rs := sqlitestore.NewRoleStore(dbConn, writer)
	svc := service.NewRoleService(rs)
	ctx := context.Background()

	err := svc.SetPermissions(ctx, "admin", []string{"door.unlock_remote"})
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
	err := svc.SetPermissions(ctx, "admin", []string{})
	if !errors.Is(err, service.ErrAdminRoleImmutable) {
		t.Fatalf("expected ErrAdminRoleImmutable for empty perm list, got: %v", err)
	}
}

func TestRoleService_SetPermissions_OtherRolesEditable(t *testing.T) {
	dbConn, writer := openSvcTestDB(t)
	rs := sqlitestore.NewRoleStore(dbConn, writer)
	svc := service.NewRoleService(rs)
	ctx := context.Background()

	if err := svc.SetPermissions(ctx, "operator", []string{"door.unlock_remote"}); err != nil {
		t.Fatalf("unexpected error setting permissions on operator role: %v", err)
	}
	if err := svc.SetPermissions(ctx, "viewer", []string{"audit.view"}); err != nil {
		t.Fatalf("unexpected error setting permissions on viewer role: %v", err)
	}
}
