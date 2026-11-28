package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

// submit posts a UI form and returns the redirect destination, flash text and
// flash type.
func (f uiFixture) submit(path string, form url.Values) (dest, msg, kind string) {
	f.t.Helper()
	resp := f.postForm(path, form, f.Admin)
	resp.Body.Close()
	return flashOf(f.t, resp)
}

func (f uiFixture) expectFlash(path string, form url.Values, wantDest, wantKind, wantMsg string) {
	f.t.Helper()
	dest, msg, kind := f.submit(path, form)
	if dest != wantDest || kind != wantKind || !strings.Contains(msg, wantMsg) {
		f.t.Errorf("POST %s: -> %s [%s] %q; want %s [%s] containing %q", path, dest, kind, msg, wantDest, wantKind, wantMsg)
	}
}

func (f uiFixture) expect404(path string, form url.Values) {
	f.t.Helper()
	resp := f.postForm(path, form, f.Admin)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		f.t.Errorf("POST %s: status %d, want 404", path, resp.StatusCode)
	}
}

func (f uiFixture) memberRec(uuid string) *store.MemberAccessRecord {
	f.t.Helper()
	rec, err := f.Members.GetMember(context.Background(), uuid)
	if err != nil {
		f.t.Fatal(err)
	}
	return rec
}

// ── members ──────────────────────────────────────────────────────────────────

func TestUIMembers_ProvisionApproveEdit(t *testing.T) {
	f := newUIFixture(t)

	f.expectFlash("/admin/ui/members", url.Values{"expires_at": {"31/12/2030"}}, "/admin/ui/members/new", "error", "Invalid expiry date")
	f.expectFlash("/admin/ui/members", url.Values{"inactivity_limit_days": {"0"}}, "/admin/ui/members/new", "error", "positive number")

	dest, _, kind := f.submit("/admin/ui/members", url.Values{"inactivity_limit_days": {"30"}})
	newUUID := strings.TrimPrefix(dest, "/admin/ui/members/")
	if kind != "success" || newUUID == dest || f.memberRec(newUUID).ProvisioningStatus != store.ProvisioningStatusPendingAuthorization {
		t.Fatalf("provision -> %s [%s]", dest, kind)
	}

	approve := "/admin/ui/members/" + newUUID + "/approve"
	f.expectFlash(approve, url.Values{}, "/admin/ui/members/"+newUUID, "error", "Inactivity limit is required")
	f.expectFlash(approve, url.Values{"inactivity_limit_days": {"-3"}}, "/admin/ui/members/"+newUUID, "error", "positive number")
	f.expectFlash(approve, url.Values{"inactivity_limit_days": {"30"}, "expires_at": {"tomorrow"}}, "/admin/ui/members/"+newUUID, "error", "Invalid expiry date")
	f.expectFlash(approve, url.Values{"inactivity_limit_days": {"30"}}, "/admin/ui/members/"+newUUID, "success", "approved")
	if rec := f.memberRec(newUUID); rec.ProvisioningStatus != store.ProvisioningStatusActive || rec.ActivatedAt == nil {
		t.Fatalf("approve not stored: %+v", rec)
	}
	f.expectFlash(approve, url.Values{"inactivity_limit_days": {"30"}}, "/admin/ui/members/"+newUUID, "error", "not pending")
	f.expect404("/admin/ui/members/no-such/approve", url.Values{"inactivity_limit_days": {"30"}})

	edit := "/admin/ui/members/" + newUUID + "/edit"
	f.expectFlash(edit, url.Values{"expires_at": {"2030-13-01"}}, edit, "error", "Invalid expiry date")
	f.expectFlash(edit, url.Values{"expires_at": {"2030-06-01"}, "inactivity_limit_days": {"60"}}, "/admin/ui/members/"+newUUID, "success", "policy updated")
	rec := f.memberRec(newUUID)
	if rec.ExpiresAt == nil || rec.ExpiresAt.Format("2006-01-02") != "2030-06-01" || rec.InactivityLimitDays == nil || *rec.InactivityLimitDays != 60 {
		t.Fatalf("policy not stored: expires=%v inactivity=%v", rec.ExpiresAt, rec.InactivityLimitDays)
	}
}

func TestUIMembers_DisableEnableArchiveAtTheDoor(t *testing.T) {
	f := newUIFixture(t)
	base := "/admin/ui/members/" + f.member

	f.expectFlash(base+"/disable", nil, base, "success", "disabled")
	if d := f.decide("m-1", "04:A3:2B:1C"); d.Granted || d.Reason != "member_disabled" {
		t.Fatalf("after UI disable: %+v", d)
	}
	f.expectFlash(base+"/enable", nil, base, "success", "enabled")
	if d := f.decide("m-1", "04:A3:2B:1C"); !d.Granted {
		t.Fatalf("after UI enable: %+v", d)
	}
	f.expectFlash(base+"/archive", nil, "/admin/ui/members", "success", "archived")
	if d := f.decide("m-1", "04:A3:2B:1C"); d.Granted || d.Reason != "member_archived" {
		t.Fatalf("after UI archive: %+v", d)
	}
	for _, action := range []string{"disable", "enable", "archive"} {
		f.expect404("/admin/ui/members/no-such/"+action, nil)
	}
}

func TestUIMembers_GrantRevokeFromMemberPage(t *testing.T) {
	f := newUIFixture(t)
	if _, err := f.Conn.Exec(`INSERT INTO modules(module_id, enabled, commissioned_at_ms, created_at_ms, updated_at_ms) VALUES ('m-2',1,1,0,0)`); err != nil {
		t.Fatal(err)
	}
	base := "/admin/ui/members/" + f.member

	f.expectFlash(base+"/authorizations", url.Values{"module_id": {"m-2"}, "expires_at": {"soon"}}, base, "error", "Invalid expiry date")
	f.expectFlash(base+"/authorizations", url.Values{"module_id": {"m-2"}}, base, "success", "granted")
	if d := f.decide("m-2", "04:A3:2B:1C"); !d.Granted {
		t.Fatalf("UI grant not effective at the door: %+v", d)
	}
	f.expectFlash(base+"/authorizations", url.Values{"module_id": {"m-2"}}, base, "error", "already exists")

	f.expectFlash(base+"/authorizations/m-2/revoke", nil, base, "success", "revoked")
	if d := f.decide("m-2", "04:A3:2B:1C"); d.Granted {
		t.Fatalf("UI revoke not effective at the door: %+v", d)
	}
	f.expectFlash(base+"/authorizations/m-2/revoke", nil, base, "error", "No active authorization")
}

// ── modules and doors ────────────────────────────────────────────────────────

func TestUIModules_RegisterRevokeDelete(t *testing.T) {
	f := newUIFixture(t)

	f.expectFlash("/admin/ui/modules", url.Values{"module_id": {"m-9"}}, "/admin/ui/modules/new", "error", "")
	f.expectFlash("/admin/ui/modules", url.Values{"module_id": {"m-9"}, "door_id": {"front"}, "display_name": {"Side"}}, "/admin/ui/modules/m-9", "success", "registered")

	// Revoking a module takes it off the air: its taps are unknown_module.
	f.expectFlash("/admin/ui/modules/m-1/revoke", nil, "/admin/ui/modules/m-1", "success", "revoked")
	if d := f.decide("m-1", "04:A3:2B:1C"); d.Granted || d.Reason != "unknown_module" {
		t.Fatalf("revoked module still serving: %+v", d)
	}
	f.expect404("/admin/ui/modules/ghost/revoke", nil)

	f.expectFlash("/admin/ui/modules/m-9/delete", nil, "/admin/ui/modules", "success", "deleted")
	f.expect404("/admin/ui/modules/m-9/delete", nil)
}

func TestUIModules_GrantRevokeFromModulePage(t *testing.T) {
	f := newUIFixture(t)
	other := f.seedActiveMember("eeeeeeee-0000-4000-8000-0000000000f9")
	f.enroll(other, "04:00:00:99")
	base := "/admin/ui/modules/m-1"

	f.expectFlash(base+"/authorizations", url.Values{"member_uuid": {other}, "expires_at": {"x"}}, base, "error", "Invalid expiry date")
	f.expectFlash(base+"/authorizations", url.Values{"member_uuid": {other}}, base, "success", "granted")
	if d := f.decide("m-1", "04:00:00:99"); !d.Granted {
		t.Fatalf("module-page grant not effective: %+v", d)
	}
	f.expectFlash(base+"/authorizations/"+other+"/revoke", nil, base, "success", "revoked")
	f.expectFlash(base+"/authorizations/"+other+"/revoke", nil, base, "error", "No active authorization")
}

func TestUIDoors_CreateDelete(t *testing.T) {
	f := newUIFixture(t)
	f.expectFlash("/admin/ui/doors", url.Values{"door_id": {"back"}}, "/admin/ui/doors/new", "error", "")
	f.expectFlash("/admin/ui/doors", url.Values{"door_id": {"back"}, "name": {"Back Door"}, "location": {"alley"}}, "/admin/ui/doors", "success", "registered")
	f.expectFlash("/admin/ui/doors/back/delete", nil, "/admin/ui/doors", "success", "deleted")
	f.expect404("/admin/ui/doors/back/delete", nil)
}

// ── roles ────────────────────────────────────────────────────────────────────

func TestUIRoles_Lifecycle(t *testing.T) {
	f := newUIFixture(t)
	ctx := context.Background()

	f.expectFlash("/admin/ui/roles", url.Values{"name": {"Night Shift"}, "description": {"after hours"}}, "/admin/ui/roles/night_shift", "success", "Role created")

	f.expectFlash("/admin/ui/roles/night_shift", url.Values{"name": {"Night Crew"}}, "/admin/ui/roles/night_shift", "success", "updated")
	f.expectFlash("/admin/ui/roles/operator", url.Values{"name": {"x"}}, "/admin/ui/roles/operator", "error", "System roles cannot be modified")

	perms := url.Values{"permission": {permissions.MemberList, permissions.MemberView}}
	f.expectFlash("/admin/ui/roles/night_shift/permissions", perms, "/admin/ui/roles/night_shift", "success", "saved")
	if got, _ := f.Roles.GetRolePermissions(ctx, "night_shift"); len(got) != 2 {
		t.Fatalf("permissions stored = %v", got)
	}
	f.expectFlash("/admin/ui/roles/admin/permissions", perms, "/admin/ui/roles/admin", "error", "cannot be modified")

	limited := f.sessionWith([]string{permissions.RoleAssignPermission}, false)
	resp := f.postForm("/admin/ui/roles/night_shift/permissions", url.Values{"permission": {permissions.AdminUserCreate}}, limited)
	resp.Body.Close()
	if _, msg, kind := flashOf(t, resp); kind != "error" || !strings.Contains(msg, "do not hold") {
		t.Errorf("limited caller granting an unheld permission: [%s] %q", kind, msg)
	}

	f.expectFlash("/admin/ui/roles/operator/delete", nil, "/admin/ui/roles", "error", "System roles cannot be deleted")
	f.expectFlash("/admin/ui/roles/crew/delete", nil, "/admin/ui/roles", "success", "deleted")
	f.expect404("/admin/ui/roles/crew/delete", nil)
}

// ── console accounts ─────────────────────────────────────────────────────────

func TestUIUsers_Lifecycle(t *testing.T) {
	f := newUIFixture(t)
	ctx := context.Background()
	op := "/admin/ui/users/" + f.operatorUUID

	f.expectFlash("/admin/ui/users", url.Values{"username": {"ivan"}, "password": {"a-valid-password-1"}, "role_id": {"crew"}}, "/admin/ui/users", "success", "created")
	if rec, err := f.AdminUsers.GetAdminUserByUsername(ctx, "ivan"); err != nil || rec.RoleID != "crew" || !rec.MustChangePW {
		t.Fatalf("created account = %+v, %v", rec, err)
	}

	f.expectFlash(op+"/role", url.Values{"role_id": {"crew"}}, op, "success", "Role updated")
	f.expectFlash(op+"/expiry", url.Values{"expires_at": {"01/02/2030"}}, op, "error", "Invalid date")
	f.expectFlash(op+"/expiry", url.Values{"expires_at": {"2030-02-01"}}, op, "success", "expiry updated")
	if rec, _ := f.AdminUsers.GetAdminUserByUUID(ctx, f.operatorUUID); rec.ExpiresAt == nil || rec.ExpiresAt.Format("2006-01-02 15:04") != "2030-02-01 23:59" {
		t.Fatalf("expiry stored = %v, want end of 2030-02-01", rec.ExpiresAt)
	}
	f.expectFlash(op+"/expiry", url.Values{"expires_at": {""}}, op, "success", "expiry cleared")
	f.expectFlash(op+"/member", url.Values{"member_uuid": {f.member}}, op, "success", "linked")

	f.expectFlash(op+"/disable", nil, "/admin/ui/users", "success", "disabled")
	f.expectFlash(op+"/enable", nil, "/admin/ui/users", "success", "enabled")
	f.expectFlash("/admin/ui/users/"+f.adminUUID()+"/disable", nil, "/admin/ui/users", "error", "your own account")
	f.expect404("/admin/ui/users/no-such/disable", nil)
}

// ── own password + logout ────────────────────────────────────────────────────

func TestUIAuth_ChangePasswordAndLogout(t *testing.T) {
	f := newUIFixture(t)
	page := func(form url.Values) (*http.Response, string) {
		resp := f.postForm("/admin/ui/change-password", form, f.Admin)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	if _, body := page(url.Values{"current_password": {testAdminPassword}, "new_password": {"a-new-valid-password"}, "confirm_password": {"different"}}); !strings.Contains(body, "do not match") {
		t.Error("mismatched confirmation not reported")
	}
	if _, body := page(url.Values{"current_password": {"wrong-password-x"}, "new_password": {"a-new-valid-password"}, "confirm_password": {"a-new-valid-password"}}); !strings.Contains(body, "Current password is incorrect") {
		t.Error("wrong current password not reported")
	}
	if _, body := page(url.Values{"current_password": {testAdminPassword}, "new_password": {"short"}, "confirm_password": {"short"}}); !strings.Contains(body, "at least 12") {
		t.Error("short password not reported")
	}
	resp, _ := page(url.Values{"current_password": {testAdminPassword}, "new_password": {"a-new-valid-password"}, "confirm_password": {"a-new-valid-password"}})
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/admin/ui/login") {
		t.Fatalf("change: status %d -> %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if c := sessionCookie(resp); c == nil || c.MaxAge >= 0 {
		t.Error("change did not clear the cookie")
	}

	fresh := f.loginAs("admin")
	resp = f.postForm("/admin/ui/logout", nil, fresh)
	resp.Body.Close()
	if c := sessionCookie(resp); resp.StatusCode != http.StatusSeeOther || c == nil || c.MaxAge >= 0 {
		t.Fatalf("logout: status %d cookie %+v", resp.StatusCode, c)
	}
	if r, _ := f.getPage("/admin/ui/", fresh); r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/admin/ui/login" {
		t.Fatalf("session usable after UI logout: %d -> %s", r.StatusCode, r.Header.Get("Location"))
	}
}
