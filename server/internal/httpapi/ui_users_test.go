package httpapi_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
)

func TestUIUsersCreate_CannotMintAdmin(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	limited := h.sessionWith([]string{permissions.AdminUserCreate}, false)

	resp := h.postForm("/admin/ui/users", url.Values{
		"username": {"mallory"}, "password": {"a-valid-password-1"}, "role_id": {"admin"},
	}, limited)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "/admin/ui/users/new?flash=") {
		t.Fatalf("status %d location %q, want 303 back to the form with a flash", resp.StatusCode, resp.Header.Get("Location"))
	}
	if _, err := h.AdminUsers.GetAdminUserByUsername(context.Background(), "mallory"); err == nil {
		t.Fatal("limited caller created an admin account")
	}
}

func TestUIUsersSetMemberLink_SelfRefused(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	ctx := context.Background()
	me, err := h.AdminUsers.GetAdminUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	member := h.seedActiveMember("dddddddd-0000-4000-8000-0000000000bb")

	resp := h.postForm("/admin/ui/users/"+me.UUID+"/member", url.Values{"member_uuid": {member}}, h.Admin)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "flash=") || !strings.Contains(resp.Header.Get("Location"), "ft=error") {
		t.Fatalf("status %d location %q, want an error flash", resp.StatusCode, resp.Header.Get("Location"))
	}
	if rec, _ := h.AdminUsers.GetAdminUserByUUID(ctx, me.UUID); rec.MemberUUID != nil {
		t.Fatal("admin linked their own account")
	}
}
