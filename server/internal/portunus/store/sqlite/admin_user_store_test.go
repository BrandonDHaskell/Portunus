package sqlite_test

import (
	"context"
	"testing"

	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

func TestAdminUserStore_CountEnabledAdminsWithRole_Empty(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	us := sqlitestore.NewAdminUserStore(conn, w)
	ctx := context.Background()

	n, err := us.CountEnabledAdminsWithRole(ctx, "admin")
	if err != nil {
		t.Fatalf("CountEnabledAdminsWithRole: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 before any users, got %d", n)
	}
}

func TestAdminUserStore_CountEnabledAdminsWithRole_OneEnabled(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	us := sqlitestore.NewAdminUserStore(conn, w)
	ctx := context.Background()

	if err := us.CreateAdminUser(ctx, "u1", "alice", "hash", "admin"); err != nil {
		t.Fatalf("CreateAdminUser: %v", err)
	}

	n, err := us.CountEnabledAdminsWithRole(ctx, "admin")
	if err != nil {
		t.Fatalf("CountEnabledAdminsWithRole: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

func TestAdminUserStore_CountEnabledAdminsWithRole_DisabledNotCounted(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	us := sqlitestore.NewAdminUserStore(conn, w)
	ctx := context.Background()

	if err := us.CreateAdminUser(ctx, "u1", "alice", "hash", "admin"); err != nil {
		t.Fatalf("CreateAdminUser u1: %v", err)
	}
	if err := us.CreateAdminUser(ctx, "u2", "bob", "hash", "admin"); err != nil {
		t.Fatalf("CreateAdminUser u2: %v", err)
	}
	if err := us.SetAdminUserEnabled(ctx, "u1", false); err != nil {
		t.Fatalf("SetAdminUserEnabled: %v", err)
	}

	n, err := us.CountEnabledAdminsWithRole(ctx, "admin")
	if err != nil {
		t.Fatalf("CountEnabledAdminsWithRole: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 after disabling u1, got %d", n)
	}
}

func TestAdminUserStore_CountEnabledAdminsWithRole_DifferentRolesIsolated(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	us := sqlitestore.NewAdminUserStore(conn, w)
	ctx := context.Background()

	if err := us.CreateAdminUser(ctx, "u1", "alice", "hash", "admin"); err != nil {
		t.Fatalf("CreateAdminUser u1: %v", err)
	}
	if err := us.CreateAdminUser(ctx, "u2", "carol", "hash", "operator"); err != nil {
		t.Fatalf("CreateAdminUser u2: %v", err)
	}

	adminCount, err := us.CountEnabledAdminsWithRole(ctx, "admin")
	if err != nil {
		t.Fatalf("CountEnabledAdminsWithRole admin: %v", err)
	}
	if adminCount != 1 {
		t.Errorf("expected 1 admin, got %d", adminCount)
	}

	opCount, err := us.CountEnabledAdminsWithRole(ctx, "operator")
	if err != nil {
		t.Fatalf("CountEnabledAdminsWithRole operator: %v", err)
	}
	if opCount != 1 {
		t.Errorf("expected 1 operator, got %d", opCount)
	}
}
