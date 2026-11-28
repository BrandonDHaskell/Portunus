package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// rest issues an admin JSON request and registers the body for closing.
func (h *harness) rest(method, path string, body any, c *http.Cookie) *http.Response {
	h.t.Helper()
	resp := do(h.t, adminReq(h.t, method, h.TS.URL+path, body, c))
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func expectStatus(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status %d, want %d", resp.StatusCode, status)
	}
	if code != "" {
		if got := errCode(t, resp); got != code {
			t.Fatalf("error code %q, want %q", got, code)
		}
	}
}

// requireAudit asserts an audit_log row exists for action+resource, made by
// the given actor.
func (h *harness) requireAudit(action, resourceID, actorUUID string) {
	h.t.Helper()
	entries, err := h.Audit.ListAuditEntries(context.Background(), 100)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, e := range entries {
		if e.Action == action && e.ResourceID == resourceID {
			if e.ActorUUID != actorUUID || e.ActorType != store.ActorTypeAdmin {
				h.t.Errorf("%s audit actor = %q/%q, want admin %q", action, e.ActorUUID, e.ActorType, actorUUID)
			}
			return
		}
	}
	h.t.Errorf("no %s audit entry for %s", action, resourceID)
}

func (h *harness) adminUUID() string {
	h.t.Helper()
	u, err := h.AdminUsers.GetAdminUserByUsername(context.Background(), "admin")
	if err != nil {
		h.t.Fatal(err)
	}
	return u.UUID
}

func TestAdminREST_MemberProvisionApproveFlow(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	expectStatus(t, h.rest(http.MethodPost, "/admin/v1/members", "not-an-object", h.Admin), http.StatusBadRequest, "bad_json")
	expectStatus(t, h.rest(http.MethodPost, "/admin/v1/members", map[string]any{"expires_at": "tomorrow"}, h.Admin), http.StatusBadRequest, "invalid_expires_at")

	resp := h.rest(http.MethodPost, "/admin/v1/members", map[string]any{"inactivity_limit_days": 30}, h.Admin)
	expectStatus(t, resp, http.StatusCreated, "")
	var created struct {
		Member types.MemberInfo `json:"member"`
	}
	decodeJSON(t, resp, &created)
	m := created.Member
	if m.ProvisioningStatus != "pending_authorization" || m.CreatedByUUID != h.adminUUID() {
		t.Fatalf("created member = %+v", m)
	}
	h.requireAudit("member.create", m.UUID, h.adminUUID())

	var pending types.ListPendingAuthorizationsResponse
	decodeJSON(t, h.rest(http.MethodGet, "/admin/v1/members/pending", nil, h.Admin), &pending)
	if len(pending.Members) != 1 || pending.Members[0].UUID != m.UUID {
		t.Fatalf("pending list = %+v", pending.Members)
	}

	approve := "/admin/v1/members/" + m.UUID + "/approve"
	expectStatus(t, h.rest(http.MethodPost, approve, map[string]any{}, h.Admin), http.StatusBadRequest, "missing_inactivity_limit")
	expectStatus(t, h.rest(http.MethodPost, approve, map[string]any{"inactivity_limit_days": 30, "expires_at": "soon"}, h.Admin), http.StatusBadRequest, "invalid_expires_at")
	expectStatus(t, h.rest(http.MethodPost, "/admin/v1/members/no-such/approve", map[string]any{"inactivity_limit_days": 30}, h.Admin), http.StatusNotFound, "not_found")
	expectStatus(t, h.rest(http.MethodPost, approve, map[string]any{"inactivity_limit_days": 30, "expires_at": "2030-01-01T00:00:00Z"}, h.Admin), http.StatusOK, "")
	h.requireAudit("member.approve", m.UUID, h.adminUUID())
	expectStatus(t, h.rest(http.MethodPost, approve, map[string]any{"inactivity_limit_days": 30}, h.Admin), http.StatusConflict, "not_pending")

	var got struct {
		Member types.MemberInfo `json:"member"`
	}
	decodeJSON(t, h.rest(http.MethodGet, "/admin/v1/members/"+m.UUID, nil, h.Admin), &got)
	if got.Member.ProvisioningStatus != "active" || got.Member.ExpiresAt != "2030-01-01T00:00:00Z" || got.Member.ActivatedAt == "" {
		t.Fatalf("approved member = %+v", got.Member)
	}
	expectStatus(t, h.rest(http.MethodGet, "/admin/v1/members/no-such", nil, h.Admin), http.StatusNotFound, "not_found")
}

func TestAdminREST_MemberDisableArchive(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	m := h.seedActiveMember("dddddddd-0000-4000-8000-0000000000c1")

	resp := h.rest(http.MethodPost, "/admin/v1/members/"+m+"/disable", nil, h.Admin)
	expectStatus(t, resp, http.StatusOK, "")
	rec, _ := h.Members.GetMember(context.Background(), m)
	if rec.Enabled {
		t.Fatal("member still enabled")
	}
	expectStatus(t, h.rest(http.MethodPost, "/admin/v1/members/no-such/disable", nil, h.Admin), http.StatusNotFound, "not_found")

	expectStatus(t, h.rest(http.MethodPost, "/admin/v1/members/"+m+"/archive", nil, h.Admin), http.StatusOK, "")
	rec, _ = h.Members.GetMember(context.Background(), m)
	if rec.Status != store.MemberStatusArchived || rec.ArchivedByUUID != h.adminUUID() {
		t.Fatalf("archive = status %q by %q", rec.Status, rec.ArchivedByUUID)
	}
	expectStatus(t, h.rest(http.MethodPost, "/admin/v1/members/no-such/archive", nil, h.Admin), http.StatusNotFound, "not_found")
}

// The member listing must never expose a full credential hash.
func TestAdminREST_MemberListShowsOnlyHashPrefix(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	m := h.seedActiveMember("dddddddd-0000-4000-8000-0000000000c2")
	h.enroll(m, "04:A3:2B:1C")

	var list types.ListMembersResponse
	decodeJSON(t, h.rest(http.MethodGet, "/admin/v1/members", nil, h.Admin), &list)
	if len(list.Members) != 1 {
		t.Fatalf("%d members", len(list.Members))
	}
	h0 := list.Members[0].CredentialHash
	if !strings.HasSuffix(h0, "…") || len(strings.TrimSuffix(h0, "…")) != 16 {
		t.Fatalf("credential_hash = %q, want a 16-hex-char prefix plus an ellipsis", h0)
	}
}

func TestAdminREST_AttachCredentialConflicts(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	ctx := context.Background()
	target := h.seedActiveMember("dddddddd-0000-4000-8000-0000000000d0")

	active := h.seedActiveMember("dddddddd-0000-4000-8000-0000000000d1")
	h.enroll(active, "04:00:00:01")
	if err := h.Members.CreateMember(ctx, "dddddddd-0000-4000-8000-0000000000d2", "", store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatal(err)
	}
	h.enroll("dddddddd-0000-4000-8000-0000000000d2", "04:00:00:02")
	archived := h.seedActiveMember("dddddddd-0000-4000-8000-0000000000d3")
	h.enroll(archived, "04:00:00:03")
	if err := h.Members.ArchiveMember(ctx, archived, ""); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ uid, code string }{
		{"04:00:00:01", "duplicate_credential_active"},
		{"04:00:00:02", "duplicate_credential_pending"},
		{"04:00:00:03", "duplicate_credential_inactive"},
	}
	for _, tc := range cases {
		resp := h.rest(http.MethodPost, "/admin/v1/members/"+target+"/credential", map[string]string{"credential_id": tc.uid}, h.Admin)
		expectStatus(t, resp, http.StatusConflict, tc.code)
	}
	expectStatus(t, h.rest(http.MethodPost, "/admin/v1/members/no-such/credential", map[string]string{"credential_id": "04:00:00:09"}, h.Admin), http.StatusNotFound, "not_found")
}

func TestAdminREST_ModuleAuthorizations(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.seedKnownModule("door-a")
	m := h.seedActiveMember("dddddddd-0000-4000-8000-0000000000e1")
	grant := "/admin/v1/modules/door-a/authorizations"

	expectStatus(t, h.rest(http.MethodPost, grant, map[string]string{}, h.Admin), http.StatusBadRequest, "missing_member_uuid")
	expectStatus(t, h.rest(http.MethodPost, grant, map[string]string{"member_uuid": m, "expires_at": "later"}, h.Admin), http.StatusBadRequest, "invalid_expires_at")
	expectStatus(t, h.rest(http.MethodPost, grant, map[string]string{"member_uuid": "no-such"}, h.Admin), http.StatusNotFound, "not_found")
	expectStatus(t, h.rest(http.MethodPost, grant, map[string]string{"member_uuid": m, "expires_at": "2030-06-01T00:00:00Z"}, h.Admin), http.StatusCreated, "")
	expectStatus(t, h.rest(http.MethodPost, grant, map[string]string{"member_uuid": m}, h.Admin), http.StatusConflict, "authorization_exists")

	// A held-scope operator with no linked member is out of scope.
	held := h.sessionWith([]string{permissions.ModuleAuthGrantHeld, permissions.ModuleAuthRevokeHeld}, false)
	expectStatus(t, h.rest(http.MethodDelete, grant+"/"+m, nil, held), http.StatusForbidden, "grant_out_of_scope")

	var byModule, byMember types.ListModuleAuthorizationsResponse
	decodeJSON(t, h.rest(http.MethodGet, grant, nil, h.Admin), &byModule)
	decodeJSON(t, h.rest(http.MethodGet, "/admin/v1/members/"+m+"/authorizations", nil, h.Admin), &byMember)
	for name, l := range map[string]types.ListModuleAuthorizationsResponse{"by module": byModule, "by member": byMember} {
		if len(l.Authorizations) != 1 {
			t.Fatalf("%s: %d authorizations", name, len(l.Authorizations))
		}
		a := l.Authorizations[0]
		if a.MemberUUID != m || a.ModuleID != "door-a" || a.ExpiresAt != "2030-06-01T00:00:00Z" || a.GrantedByUUID != h.adminUUID() || a.RevokedAt != "" {
			t.Fatalf("%s: %+v", name, a)
		}
	}

	expectStatus(t, h.rest(http.MethodDelete, grant+"/"+m, nil, h.Admin), http.StatusOK, "")
	expectStatus(t, h.rest(http.MethodDelete, grant+"/"+m, nil, h.Admin), http.StatusNotFound, "not_found")
	decodeJSON(t, h.rest(http.MethodGet, grant, nil, h.Admin), &byModule)
	if len(byModule.Authorizations) != 1 || byModule.Authorizations[0].RevokedAt == "" || byModule.Authorizations[0].RevokedByUUID != h.adminUUID() {
		t.Fatalf("after revoke: %+v", byModule.Authorizations)
	}
}

func TestAdminREST_ModulesAndDoors(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	expectStatus(t, h.rest(http.MethodPost, "/admin/v1/doors", "x", h.Admin), http.StatusBadRequest, "bad_json")
	expectStatus(t, h.rest(http.MethodPost, "/admin/v1/doors", map[string]string{"door_id": "front"}, h.Admin), http.StatusBadRequest, "validation_error")
	expectStatus(t, h.rest(http.MethodPost, "/admin/v1/doors", map[string]string{"door_id": "front", "name": "Front Door"}, h.Admin), http.StatusCreated, "")
	var doors types.ListDoorsResponse
	decodeJSON(t, h.rest(http.MethodGet, "/admin/v1/doors", nil, h.Admin), &doors)
	if len(doors.Doors) != 1 || doors.Doors[0].Name != "Front Door" {
		t.Fatalf("doors = %+v", doors.Doors)
	}

	reg := "/admin/v1/modules"
	expectStatus(t, h.rest(http.MethodPost, reg, "x", h.Admin), http.StatusBadRequest, "bad_json")
	expectStatus(t, h.rest(http.MethodPost, reg, map[string]string{"door_id": "front"}, h.Admin), http.StatusBadRequest, "missing_module_id")
	expectStatus(t, h.rest(http.MethodPost, reg, map[string]string{"module_id": "m-1"}, h.Admin), http.StatusBadRequest, "module_door_required")
	expectStatus(t, h.rest(http.MethodPost, reg, map[string]string{"module_id": "m-1", "door_id": "nowhere"}, h.Admin), http.StatusBadRequest, "door_not_found")
	expectStatus(t, h.rest(http.MethodPost, reg, map[string]string{"module_id": "m-1", "door_id": "front", "display_name": "Front reader"}, h.Admin), http.StatusCreated, "")

	var got struct {
		Module types.ModuleInfo `json:"module"`
	}
	decodeJSON(t, h.rest(http.MethodGet, reg+"/m-1", nil, h.Admin), &got)
	if got.Module.ModuleID != "m-1" || got.Module.DoorID != "front" {
		t.Fatalf("module = %+v", got.Module)
	}
	expectStatus(t, h.rest(http.MethodGet, reg+"/ghost", nil, h.Admin), http.StatusNotFound, "not_found")
	var list types.ListModulesResponse
	decodeJSON(t, h.rest(http.MethodGet, reg, nil, h.Admin), &list)
	if len(list.Modules) != 1 {
		t.Fatalf("modules = %+v", list.Modules)
	}
}

func TestAdminREST_HealthReportsAuditState(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	var body map[string]any
	resp := h.rest(http.MethodGet, "/admin/v1/health", nil, h.Admin)
	expectStatus(t, resp, http.StatusOK, "")
	decodeJSON(t, resp, &body)
	audit, ok := body["audit"].(map[string]any)
	if !ok {
		t.Fatalf("health body = %v, want an audit section", body)
	}
	if audit["consecutive_failures"] != float64(0) {
		t.Fatalf("audit health = %v", audit)
	}
}
