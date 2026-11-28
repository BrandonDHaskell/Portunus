package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

// uiFixture seeds one of everything the UI pages display: a door with a
// module, an active member with a credential and an authorization (so detail
// pages render their history and grant tables), a pending capture, a custom
// role and a second console account.
type uiFixture struct {
	*harness
	member, pending, operatorUUID string
}

func newUIFixture(t *testing.T) uiFixture {
	t.Helper()
	h := newHarness(t, harnessOptions{})
	ctx := context.Background()
	if _, err := h.Conn.Exec(`INSERT INTO doors(door_id, name, created_at_ms, updated_at_ms) VALUES ('front','Front Door',0,0)`); err != nil {
		t.Fatalf("seed door: %v", err)
	}
	if _, err := h.Conn.Exec(`INSERT INTO modules(module_id, door_id, display_name, enabled, commissioned_at_ms, created_at_ms, updated_at_ms) VALUES ('m-1','front','Front reader',1,1,0,0)`); err != nil {
		t.Fatalf("seed module: %v", err)
	}
	f := uiFixture{harness: h, member: "eeeeeeee-0000-4000-8000-0000000000f1", pending: "eeeeeeee-0000-4000-8000-0000000000f2"}
	h.seedActiveMember(f.member)
	h.enroll(f.member, "04:A3:2B:1C")
	h.grant(f.member, "m-1")
	h.decide("m-1", "04:A3:2B:1C") // one access event for the history table
	if err := h.Members.CreateMember(ctx, f.pending, "", store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.Roles.CreateRole(ctx, "crew", "Crew", "test role"); err != nil {
		t.Fatal(err)
	}
	f.operatorUUID = "uuid-ui-operator"
	if err := h.AdminUsers.CreateAdminUser(ctx, f.operatorUUID, "olivia", "x", "operator"); err != nil {
		t.Fatal(err)
	}
	return f
}

func (h *harness) getPage(path string, c *http.Cookie) (*http.Response, string) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.TS.URL+path, nil)
	if c != nil {
		req.AddCookie(c)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// Every page must render completely. Template execution errors are dropped
// after the 200 header is sent, so a broken page shows up as a document that
// never reaches </html>.
func TestUI_EveryPageRenders(t *testing.T) {
	f := newUIFixture(t)
	pages := []string{
		"/admin/ui/",
		"/admin/ui/change-password",
		"/admin/ui/users",
		"/admin/ui/users/new",
		"/admin/ui/users/" + f.operatorUUID,
		"/admin/ui/roles",
		"/admin/ui/roles/new",
		"/admin/ui/roles/crew",
		"/admin/ui/roles/admin",
		"/admin/ui/members",
		"/admin/ui/members/pending",
		"/admin/ui/members/new",
		"/admin/ui/members/" + f.member,
		"/admin/ui/members/" + f.pending,
		"/admin/ui/members/" + f.member + "/edit",
		"/admin/ui/modules",
		"/admin/ui/modules/new",
		"/admin/ui/modules/m-1",
		"/admin/ui/doors",
		"/admin/ui/doors/new",
	}
	for _, p := range pages {
		t.Run(p, func(t *testing.T) {
			resp, body := f.getPage(p, f.Admin)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %.200s", resp.StatusCode, body)
			}
			if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
				t.Errorf("Content-Type %q", resp.Header.Get("Content-Type"))
			}
			if !strings.Contains(body, "</html>") {
				t.Fatalf("page truncated (template execution error?): ...%s", tail(body, 300))
			}
		})
	}

	t.Run("login page (anonymous)", func(t *testing.T) {
		resp, body := f.getPage("/admin/ui/login?flash=Session+expired", nil)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, "</html>") || !strings.Contains(body, "Session expired") {
			t.Fatalf("login page: status %d, flash shown=%v", resp.StatusCode, strings.Contains(body, "Session expired"))
		}
	})
}

func TestUI_DetailPagesForMissingRecords(t *testing.T) {
	f := newUIFixture(t)
	for _, p := range []string{
		"/admin/ui/users/no-such-user",
		"/admin/ui/roles/no-such-role",
		"/admin/ui/members/no-such-member",
		"/admin/ui/modules/no-such-module",
	} {
		if resp, _ := f.getPage(p, f.Admin); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, resp.StatusCode)
		}
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// flashOf extracts the flash message and type from a redirect.
func flashOf(t testing.TB, resp *http.Response) (dest, msg, kind string) {
	t.Helper()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	u, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return u.Path, u.Query().Get("flash"), u.Query().Get("ft")
}

// Flash messages containing non-ASCII text must survive the redirect intact.
func TestUI_FlashPreservesUnicode(t *testing.T) {
	f := newUIFixture(t)
	resp := f.postForm("/admin/ui/members/"+f.member+"/credential", url.Values{"credential_id": {"bad"}}, f.Admin)
	resp.Body.Close()
	_, msg, kind := flashOf(t, resp)
	if kind != "error" || !strings.Contains(msg, "Invalid credential UID — use colon-separated hex") {
		t.Fatalf("flash = %q (%s)", msg, kind)
	}
}

// The authorization tables must offer Revoke on active grants and the module
// page must offer its grant form. Both were silently cut off by template
// errors before.
func TestUI_DetailPagesOfferRevokeAndGrant(t *testing.T) {
	f := newUIFixture(t)
	_, member := f.getPage("/admin/ui/members/"+f.member, f.Admin)
	if !strings.Contains(member, `action="/admin/ui/members/`+f.member+`/authorizations/m-1/revoke"`) {
		t.Error("member page has no Revoke form for its active grant")
	}
	_, module := f.getPage("/admin/ui/modules/m-1", f.Admin)
	if !strings.Contains(module, `action="/admin/ui/modules/m-1/authorizations/`+f.member+`/revoke"`) {
		t.Error("module page has no Revoke form for its active grant")
	}
	if !strings.Contains(module, `<option value="`+f.member+`">`) {
		t.Error("module page grant form does not list the approved member")
	}
	if strings.Contains(module, `<option value="`+f.pending+`">`) {
		t.Error("module page grant form lists a pending (unapproved) member")
	}
}

// Render the pages again in the other states their templates branch on.
func TestUI_PagesRenderInEveryState(t *testing.T) {
	f := newUIFixture(t)
	ctx := context.Background()

	if err := f.ModuleAuths.RevokeAuthorization(ctx, f.member, "m-1", ""); err != nil {
		t.Fatal(err)
	}
	link := f.member
	if err := f.AdminUsers.SetAdminUserMemberLink(ctx, f.operatorUUID, &link); err != nil {
		t.Fatal(err)
	}
	exp := mustTime(t, "2030-01-01T00:00:00Z")
	if err := f.AdminUsers.SetAdminUserExpiry(ctx, f.operatorUUID, &exp); err != nil {
		t.Fatal(err)
	}
	if err := f.Members.UpdateMemberPolicy(ctx, f.member, &exp, intp(30)); err != nil {
		t.Fatal(err)
	}
	pages := []string{"/admin/ui/members/" + f.member, "/admin/ui/modules/m-1", "/admin/ui/users/" + f.operatorUUID, "/admin/ui/users", "/admin/ui/members/" + f.member + "/edit"}

	render := func(state string) {
		for _, p := range pages {
			resp, body := f.getPage(p, f.Admin)
			if resp.StatusCode != http.StatusOK || !strings.Contains(body, "</html>") {
				t.Errorf("%s, %s: status %d, complete=%v", state, p, resp.StatusCode, strings.Contains(body, "</html>"))
			}
		}
	}
	render("revoked grant, linked and expiring account")

	if err := f.Members.ArchiveMember(ctx, f.member, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Conn.Exec(`UPDATE modules SET revoked_at_ms = 1 WHERE module_id = 'm-1'`); err != nil {
		t.Fatal(err)
	}
	render("archived member, revoked module")
}

func intp(n int) *int { return &n }

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
