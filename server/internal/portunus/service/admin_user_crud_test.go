package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

func TestAdminUsers_CreateValidation(t *testing.T) {
	svc, _, _, _ := newAdminUserSvc(t)
	ctx := context.Background()
	cases := []struct {
		name, user, pw, role string
		wantPWErr            bool
	}{
		{"blank username", "  ", "a-valid-password-1", "operator", false},
		{"short password", "u1", "elevenchars", "operator", true},
		{"73-char password", "u2", strings.Repeat("p", 73), "operator", true},
		{"blank role", "u3", "a-valid-password-1", " ", false},
		{"unknown role", "u4", "a-valid-password-1", "no-such-role", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateUser(ctx, "creator", allPerms(), tc.user, tc.pw, tc.role)
			if err == nil {
				t.Fatal("accepted")
			}
			if tc.wantPWErr && !errors.Is(err, service.ErrPasswordChangeFailed) {
				t.Errorf("got %v, want ErrPasswordChangeFailed", err)
			}
		})
	}
}

func TestAdminUsers_CreateListGet(t *testing.T) {
	svc, us, _, audit := newAdminUserSvc(t)
	ctx := context.Background()

	info, err := svc.CreateUser(ctx, "creator", allPerms(), "  heidi ", "a-valid-password-1", "operator")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if info.Username != "heidi" || info.RoleID != "operator" || !info.MustChangePW || !info.Enabled || len(info.UUID) != 36 {
		t.Fatalf("created = %+v", info)
	}
	rec, err := us.GetAdminUserByUsername(ctx, "heidi")
	if err != nil || !rec.MustChangePW {
		t.Fatalf("stored = %+v, %v; want must_change_pw", rec, err)
	}
	requireAuditEntry(t, audit, "admin_user.created", info.UUID)

	if _, err := svc.CreateUser(ctx, "creator", allPerms(), "heidi", "a-valid-password-1", "operator"); !errors.Is(err, store.ErrUsernameAlreadyExists) {
		t.Errorf("duplicate username: got %v", err)
	}

	got, err := svc.GetUser(ctx, info.UUID)
	if err != nil || got.Username != "heidi" {
		t.Fatalf("GetUser = %+v, %v", got, err)
	}
	if _, err := svc.GetUser(ctx, "no-such-uuid"); !errors.Is(err, service.ErrAdminUserNotFound) {
		t.Errorf("GetUser(unknown): %v", err)
	}

	seedAdminUser(t, us, "uuid-zed", "viewer", true)
	past := time.Now().Add(-time.Hour)
	if err := us.SetAdminUserExpiry(ctx, "uuid-zed", &past); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListUsers(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListUsers: %d users, %v", len(list), err)
	}
	for _, u := range list {
		if u.UUID == "uuid-zed" && (!u.IsExpired || u.ExpiresAt == "") {
			t.Errorf("expired account not flagged: %+v", u)
		}
		if u.UUID == info.UUID && u.IsExpired {
			t.Errorf("active account flagged expired: %+v", u)
		}
	}
}
