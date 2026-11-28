package httpapi_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Every revocation, archival and disable, and every other change to a
// member, module, door or authorization, must leave an audit row naming the
// admin who did it and where from, whether it came through the UI or REST.
func TestAuditCoverage_EveryMutationIsRecorded(t *testing.T) {
	type step struct {
		name     string
		do       func(f uiFixture)
		action   string
		resource func(f uiFixture) string
	}
	form := func(path string, v url.Values) func(f uiFixture) {
		return func(f uiFixture) {
			r := f.postForm(path, v, f.Admin)
			r.Body.Close()
		}
	}
	rest := func(method, path string, body any) func(f uiFixture) {
		return func(f uiFixture) { f.rest(method, path, body, f.Admin) }
	}
	member := func(f uiFixture) string { return f.member }
	fixed := func(id string) func(uiFixture) string { return func(uiFixture) string { return id } }

	steps := []step{
		// UI
		{"ui member disable", func(f uiFixture) { form("/admin/ui/members/"+f.member+"/disable", nil)(f) }, "member.disable", member},
		{"ui member enable", func(f uiFixture) { form("/admin/ui/members/"+f.member+"/enable", nil)(f) }, "member.enable", member},
		{"ui member policy", func(f uiFixture) {
			form("/admin/ui/members/"+f.member+"/edit", url.Values{"inactivity_limit_days": {"45"}})(f)
		}, "member.policy_update", member},
		{"ui attach credential", func(f uiFixture) {
			form("/admin/ui/members/"+f.pending+"/credential", url.Values{"credential_id": {"04:11:22:33"}})(f)
		}, "member.credential_attach", func(f uiFixture) string { return f.pending }},
		{"ui approve", func(f uiFixture) {
			form("/admin/ui/members/"+f.pending+"/approve", url.Values{"inactivity_limit_days": {"30"}})(f)
		}, "member.approve", func(f uiFixture) string { return f.pending }},
		{"ui revoke grant (member page)", func(f uiFixture) {
			form("/admin/ui/members/"+f.member+"/authorizations/m-1/revoke", nil)(f)
		}, "module_auth.revoked", fixed("m-1")},
		{"ui grant (module page)", func(f uiFixture) {
			form("/admin/ui/modules/m-1/authorizations", url.Values{"member_uuid": {f.member}})(f)
		}, "module_auth.granted", fixed("m-1")},
		{"ui revoke grant (module page)", func(f uiFixture) {
			form("/admin/ui/modules/m-1/authorizations/"+f.member+"/revoke", nil)(f)
		}, "module_auth.revoked", fixed("m-1")},
		{"ui door create", form("/admin/ui/doors", url.Values{"door_id": {"side"}, "name": {"Side"}}), "door.register", fixed("side")},
		{"ui module register", form("/admin/ui/modules", url.Values{"module_id": {"m-ui"}, "door_id": {"side"}}), "module.register", fixed("m-ui")},
		{"ui module revoke", form("/admin/ui/modules/m-ui/revoke", nil), "module.revoke", fixed("m-ui")},
		{"ui module delete", form("/admin/ui/modules/m-ui/delete", nil), "module.delete", fixed("m-ui")},
		{"ui door delete", form("/admin/ui/doors/side/delete", nil), "door.delete", fixed("side")},
		{"ui member archive", func(f uiFixture) { form("/admin/ui/members/"+f.member+"/archive", nil)(f) }, "member.archive", member},

		// REST
		{"rest door register", rest(http.MethodPost, "/admin/v1/doors", map[string]string{"door_id": "back", "name": "Back"}), "door.register", fixed("back")},
		{"rest module register", rest(http.MethodPost, "/admin/v1/modules", map[string]string{"module_id": "m-rest", "door_id": "back"}), "module.register", fixed("m-rest")},
		{"rest module revoke", rest(http.MethodPost, "/admin/v1/modules/m-rest/revoke", nil), "module.revoke", fixed("m-rest")},
		{"rest module delete", rest(http.MethodDelete, "/admin/v1/modules/m-rest", nil), "module.delete", fixed("m-rest")},
		{"rest door delete", rest(http.MethodDelete, "/admin/v1/doors/back", nil), "door.delete", fixed("back")},
	}

	f := newUIFixture(t)
	admin := f.adminUUID()
	for _, s := range steps {
		before := len(f.auditFor(s.action, s.resource(f)))
		s.do(f)
		after := f.auditFor(s.action, s.resource(f))
		if len(after) <= before {
			t.Errorf("%s: no new %s audit entry for %s", s.name, s.action, s.resource(f))
			continue
		}
		e := after[0] // newest first
		if e.ActorUUID != admin || !strings.HasPrefix(e.IPAddress, "127.0.0.1:") {
			t.Errorf("%s: audit actor=%q ip=%q, want admin %q from 127.0.0.1", s.name, e.ActorUUID, e.IPAddress, admin)
		}
	}

	// REST member operations on a fresh member.
	var created struct {
		Member struct {
			UUID string `json:"uuid"`
		} `json:"member"`
	}
	decodeJSON(t, f.rest(http.MethodPost, "/admin/v1/members", map[string]any{}, f.Admin), &created)
	m := created.Member.UUID
	f.rest(http.MethodPost, "/admin/v1/members/"+m+"/credential", map[string]string{"credential_id": "04:44:55:66"}, f.Admin)
	f.rest(http.MethodPost, "/admin/v1/members/"+m+"/approve", map[string]any{"inactivity_limit_days": 30}, f.Admin)
	f.rest(http.MethodPost, "/admin/v1/members/"+m+"/disable", nil, f.Admin)
	f.rest(http.MethodPost, "/admin/v1/members/"+m+"/archive", nil, f.Admin)
	for _, action := range []string{"member.create", "member.credential_attach", "member.approve", "member.disable", "member.archive"} {
		got := f.auditFor(action, m)
		if len(got) != 1 || got[0].ActorUUID != admin || got[0].Result != "success" {
			t.Errorf("REST %s: %d entries %+v", action, len(got), got)
		}
	}

	// Session revocations.
	c := f.loginAs("admin")
	f.rest(http.MethodPost, "/admin/v1/logout", nil, c)
	if got := f.auditFor("admin_user.logout", admin); len(got) == 0 {
		t.Error("logout not audited")
	}
}

// auditFor returns audit entries for one action and resource, newest first.
func (h *harness) auditFor(action, resourceID string) []auditRow {
	h.t.Helper()
	entries, err := h.Audit.ListAuditEntries(context.Background(), 1000)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []auditRow
	for _, e := range entries {
		if e.Action == action && e.ResourceID == resourceID {
			out = append(out, auditRow{ActorUUID: e.ActorUUID, IPAddress: e.IPAddress, Result: e.Result})
		}
	}
	return out
}

type auditRow struct{ ActorUUID, IPAddress, Result string }
