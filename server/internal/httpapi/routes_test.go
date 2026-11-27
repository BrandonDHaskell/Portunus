package httpapi_test

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
)

// guardedRoute is one admin route and the permission(s) its guard requires.
// With two perms the guard accepts either one (requireEitherPermission).
type guardedRoute struct {
	pattern string // "METHOD /path/{param}" exactly as registered
	perms   []string
}

var restRoutes = []guardedRoute{
	{"GET /admin/v1/modules", []string{permissions.ModuleList}},
	{"GET /admin/v1/modules/{module_id}", []string{permissions.ModuleGet}},
	{"POST /admin/v1/modules", []string{permissions.ModuleRegister}},
	{"POST /admin/v1/modules/{module_id}/revoke", []string{permissions.ModuleRevoke}},
	{"DELETE /admin/v1/modules/{module_id}", []string{permissions.ModuleDelete}},
	{"GET /admin/v1/doors", []string{permissions.DoorList}},
	{"POST /admin/v1/doors", []string{permissions.DoorRegister}},
	{"DELETE /admin/v1/doors/{door_id}", []string{permissions.DoorDelete}},
	{"GET /admin/v1/members", []string{permissions.MemberList}},
	{"GET /admin/v1/members/pending", []string{permissions.MemberList}},
	{"GET /admin/v1/members/{member_uuid}", []string{permissions.MemberView}},
	{"POST /admin/v1/members", []string{permissions.MemberEnroll}},
	{"POST /admin/v1/members/{member_uuid}/credential", []string{permissions.MemberEnroll}},
	{"POST /admin/v1/members/{member_uuid}/approve", []string{permissions.MemberEnroll}},
	{"POST /admin/v1/members/{member_uuid}/disable", []string{permissions.MemberDisable}},
	{"POST /admin/v1/members/{member_uuid}/archive", []string{permissions.MemberArchive}},
	{"GET /admin/v1/modules/{module_id}/authorizations", []string{permissions.ModuleAuthList}},
	{"POST /admin/v1/modules/{module_id}/authorizations", []string{permissions.ModuleAuthGrantHeld, permissions.ModuleAuthGrantAny}},
	{"DELETE /admin/v1/modules/{module_id}/authorizations/{member_uuid}", []string{permissions.ModuleAuthRevokeHeld, permissions.ModuleAuthRevokeAny}},
	{"GET /admin/v1/members/{member_uuid}/authorizations", []string{permissions.ModuleAuthList}},
}

var uiRoutes = []guardedRoute{
	{"GET /admin/ui/users", []string{permissions.AdminUserList}},
	{"GET /admin/ui/users/new", []string{permissions.AdminUserCreate}},
	{"POST /admin/ui/users", []string{permissions.AdminUserCreate}},
	{"GET /admin/ui/users/{uuid}", []string{permissions.AdminUserList}},
	{"POST /admin/ui/users/{uuid}/role", []string{permissions.AdminUserEdit}},
	{"POST /admin/ui/users/{uuid}/disable", []string{permissions.AdminUserDisable}},
	{"POST /admin/ui/users/{uuid}/enable", []string{permissions.AdminUserDisable}},
	{"POST /admin/ui/users/{uuid}/expiry", []string{permissions.AdminUserEdit}},
	{"POST /admin/ui/users/{uuid}/member", []string{permissions.AdminUserEdit}},
	{"GET /admin/ui/roles", []string{permissions.RoleList}},
	{"GET /admin/ui/roles/new", []string{permissions.RoleCreate}},
	{"POST /admin/ui/roles", []string{permissions.RoleCreate}},
	{"GET /admin/ui/roles/{role_id}", []string{permissions.RoleList}},
	{"POST /admin/ui/roles/{role_id}", []string{permissions.RoleEdit}},
	{"POST /admin/ui/roles/{role_id}/permissions", []string{permissions.RoleAssignPermission}},
	{"POST /admin/ui/roles/{role_id}/delete", []string{permissions.RoleDelete}},
	{"GET /admin/ui/members", []string{permissions.MemberList}},
	{"GET /admin/ui/members/pending", []string{permissions.MemberList}},
	{"GET /admin/ui/members/new", []string{permissions.MemberEnroll}},
	{"POST /admin/ui/members", []string{permissions.MemberEnroll}},
	{"GET /admin/ui/members/{member_uuid}", []string{permissions.MemberView}},
	{"GET /admin/ui/members/{member_uuid}/edit", []string{permissions.MemberEdit}},
	{"POST /admin/ui/members/{member_uuid}/edit", []string{permissions.MemberEdit}},
	{"POST /admin/ui/members/{member_uuid}/approve", []string{permissions.MemberEnroll}},
	{"POST /admin/ui/members/{member_uuid}/credential", []string{permissions.MemberEnroll}},
	{"POST /admin/ui/members/{member_uuid}/disable", []string{permissions.MemberDisable}},
	{"POST /admin/ui/members/{member_uuid}/enable", []string{permissions.MemberDisable}},
	{"POST /admin/ui/members/{member_uuid}/archive", []string{permissions.MemberArchive}},
	{"POST /admin/ui/members/{member_uuid}/authorizations", []string{permissions.ModuleAuthGrantHeld, permissions.ModuleAuthGrantAny}},
	{"POST /admin/ui/members/{member_uuid}/authorizations/{module_id}/revoke", []string{permissions.ModuleAuthRevokeHeld, permissions.ModuleAuthRevokeAny}},
	{"GET /admin/ui/modules", []string{permissions.ModuleList}},
	{"GET /admin/ui/modules/new", []string{permissions.ModuleRegister}},
	{"POST /admin/ui/modules", []string{permissions.ModuleRegister}},
	{"GET /admin/ui/modules/{module_id}", []string{permissions.ModuleGet}},
	{"POST /admin/ui/modules/{module_id}/revoke", []string{permissions.ModuleRevoke}},
	{"POST /admin/ui/modules/{module_id}/delete", []string{permissions.ModuleDelete}},
	{"POST /admin/ui/modules/{module_id}/authorizations", []string{permissions.ModuleAuthGrantHeld, permissions.ModuleAuthGrantAny}},
	{"POST /admin/ui/modules/{module_id}/authorizations/{member_uuid}/revoke", []string{permissions.ModuleAuthRevokeHeld, permissions.ModuleAuthRevokeAny}},
	{"GET /admin/ui/doors", []string{permissions.DoorList}},
	{"GET /admin/ui/doors/new", []string{permissions.DoorRegister}},
	{"POST /admin/ui/doors", []string{permissions.DoorRegister}},
	{"POST /admin/ui/doors/{door_id}/delete", []string{permissions.DoorDelete}},
}

// sessionOnlyRoutes need a session but no specific permission.
var sessionOnlyRoutes = []string{
	"POST /admin/v1/logout",
	"POST /admin/v1/change-password",
	"GET /admin/v1/health",
	"POST /admin/ui/logout",
	"GET /admin/ui/change-password",
	"POST /admin/ui/change-password",
	"GET /admin/ui/",
}

// publicRoutes are reachable without a session by design.
var publicRoutes = []string{
	"POST /admin/v1/login",
	"GET /admin/ui/login",
	"POST /admin/ui/login",
	"GET /admin/ui/static/",
}

var pathParam = regexp.MustCompile(`\{[a-z_]+\}`)

func (h *harness) hit(pattern string, cookie *http.Cookie) *http.Response {
	h.t.Helper()
	method, path, _ := strings.Cut(pattern, " ")
	path = pathParam.ReplaceAllString(path, "x-test-id")
	req, err := http.NewRequest(method, h.TS.URL+path, strings.NewReader("{}"))
	if err != nil {
		h.t.Fatal(err)
	}
	if strings.HasPrefix(path, "/admin/ui/") {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// restGuardRejection returns the guard's error code, or "" when the request
// reached the handler. Handlers may return 403 themselves (for example
// grant_out_of_scope from the service scope check); that is not the guard.
func restGuardRejection(t *testing.T, resp *http.Response) string {
	t.Helper()
	switch code := errCode(t, resp); code {
	case "unauthenticated", "forbidden", "password_change_required":
		return code
	}
	return ""
}

func allPermsExcept(excluded ...string) []string {
	skip := map[string]bool{}
	for _, p := range excluded {
		skip[p] = true
	}
	var out []string
	for _, p := range permissions.All() {
		if !skip[p] {
			out = append(out, p)
		}
	}
	return out
}

// uiGuardRejection classifies a UI response: "" when the guard let it through.
func uiGuardRejection(resp *http.Response) string {
	if resp.StatusCode != http.StatusSeeOther {
		return ""
	}
	loc := resp.Header.Get("Location")
	switch {
	case loc == "/admin/ui/login":
		return "login"
	case loc == "/admin/ui/change-password":
		return "change-password"
	case strings.Contains(loc, "Insufficient+permissions"):
		return "forbidden"
	}
	return ""
}

func TestRoutePermissionMatrix_REST(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	for _, rt := range restRoutes {
		t.Run(rt.pattern, func(t *testing.T) {
			if resp := h.hit(rt.pattern, nil); resp.StatusCode != http.StatusUnauthorized || restGuardRejection(t, resp) != "unauthenticated" {
				t.Errorf("no session: %d, want 401 unauthenticated", resp.StatusCode)
			}
			if resp := h.hit(rt.pattern, h.sessionWith(permissions.All(), true)); resp.StatusCode != http.StatusForbidden || restGuardRejection(t, resp) != "password_change_required" {
				t.Errorf("must-change-pw: %d, want 403 password_change_required", resp.StatusCode)
			}
			if resp := h.hit(rt.pattern, h.sessionWith(allPermsExcept(rt.perms...), false)); resp.StatusCode != http.StatusForbidden || restGuardRejection(t, resp) != "forbidden" {
				t.Errorf("missing %v: %d, want 403 forbidden", rt.perms, resp.StatusCode)
			}
			for _, p := range rt.perms {
				if got := restGuardRejection(t, h.hit(rt.pattern, h.sessionWith([]string{p}, false))); got != "" {
					t.Errorf("only %s: guard rejected (%s), should admit it", p, got)
				}
			}
		})
	}
}

func TestRoutePermissionMatrix_UI(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	for _, rt := range uiRoutes {
		t.Run(rt.pattern, func(t *testing.T) {
			if got := uiGuardRejection(h.hit(rt.pattern, nil)); got != "login" {
				t.Errorf("no session: rejection %q, want login", got)
			}
			if got := uiGuardRejection(h.hit(rt.pattern, h.sessionWith(permissions.All(), true))); got != "change-password" {
				t.Errorf("must-change-pw: rejection %q, want change-password", got)
			}
			if got := uiGuardRejection(h.hit(rt.pattern, h.sessionWith(allPermsExcept(rt.perms...), false))); got != "forbidden" {
				t.Errorf("missing %v: rejection %q, want forbidden", rt.perms, got)
			}
			for _, p := range rt.perms {
				if got := uiGuardRejection(h.hit(rt.pattern, h.sessionWith([]string{p}, false))); got != "" {
					t.Errorf("only %s: rejected (%s), guard should admit it", p, got)
				}
			}
		})
	}
}

func TestRoutePermissionMatrix_SessionOnly(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	for _, pattern := range sessionOnlyRoutes {
		t.Run(pattern, func(t *testing.T) {
			resp := h.hit(pattern, nil)
			if strings.Contains(pattern, "/admin/ui/") {
				if got := uiGuardRejection(resp); got != "login" {
					t.Errorf("no session: rejection %q, want login", got)
				}
			} else if resp.StatusCode != http.StatusUnauthorized || restGuardRejection(t, resp) != "unauthenticated" {
				t.Errorf("no session: %d, want 401 unauthenticated", resp.StatusCode)
			}
		})
	}
}

// Every /admin/ route registered in the package must appear in one of the
// tables above. A route added without a guard entry fails here, before it can
// ship unreviewed.
func TestRoutePermissionMatrix_CoversEveryAdminRoute(t *testing.T) {
	known := map[string]bool{}
	for _, rt := range append(append([]guardedRoute{}, restRoutes...), uiRoutes...) {
		known[rt.pattern] = true
	}
	for _, p := range append(append([]string{}, sessionOnlyRoutes...), publicRoutes...) {
		known[p] = true
	}

	reg := regexp.MustCompile(`\.Handle(?:Func)?\(\s*"([A-Z]+ /admin/[^"]*)"`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range reg.FindAllStringSubmatch(string(src), -1) {
			if !known[m[1]] {
				missing = append(missing, m[1]+" ("+f+")")
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("admin route not in the permission matrix: %s", m)
	}
}
