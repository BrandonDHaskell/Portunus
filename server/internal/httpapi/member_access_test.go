package httpapi_test

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

// Hazard #1 cross-path tests for the two admin enrollment paths. Each side
// starts from the input a human or the firmware actually supplies: the admin
// types the colon-formatted UID printed by a reader, and the door later sends
// the same colon-formatted string (server_comm.cpp credential_uid_to_hex).

func TestAdminREST_AttachCredentialByUID_IsGrantedAtDoor(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.seedKnownModule("door-rest")
	member := h.seedActiveMember("dddddddd-0000-4000-8000-000000000001")
	h.grant(member, "door-rest")

	resp := do(t, adminReq(t, http.MethodPost, h.TS.URL+"/admin/v1/members/"+member+"/credential",
		map[string]string{"credential_id": "04:A3:2B:1C"}, h.Admin))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("attach: status %d, want 200", resp.StatusCode)
	}

	got := h.decide("door-rest", "04:A3:2B:1C")
	if !got.Granted {
		t.Fatalf("REST-enrolled credential denied at door: reason=%q", got.Reason)
	}
}

func TestAdminUI_AttachCredentialByUID_IsGrantedAtDoor(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.seedKnownModule("door-ui")
	member := h.seedActiveMember("dddddddd-0000-4000-8000-000000000002")
	h.grant(member, "door-ui")

	resp := h.postForm("/admin/ui/members/"+member+"/credential",
		url.Values{"credential_id": {"04:A3:2B:1C"}}, h.Admin)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("attach: status %d, want 303", resp.StatusCode)
	}

	got := h.decide("door-ui", "04:A3:2B:1C")
	if !got.Granted {
		t.Fatalf("UI-enrolled credential denied at door: reason=%q", got.Reason)
	}
}

func TestAdminREST_AttachCredential_RejectsBadInput(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	member := h.seedActiveMember("dddddddd-0000-4000-8000-000000000003")

	// A precomputed hash is no longer accepted: only the server may hash.
	precomputed := hex.EncodeToString(service.HashCredentialID([]byte{0x04, 0xA3, 0x2B, 0x1C}, testCredentialHashSecret))

	cases := []struct {
		name string
		body any
	}{
		{"malformed uid", map[string]string{"credential_id": "04-A3-2B-1C"}},
		{"empty uid", map[string]string{"credential_id": ""}},
		{"legacy precomputed hash field", map[string]string{"credential_hash": precomputed}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(t, adminReq(t, http.MethodPost, h.TS.URL+"/admin/v1/members/"+member+"/credential", tc.body, h.Admin))
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestGrantToPendingMember_RefusedOverRESTAndUI(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.seedKnownModule("door-p")
	const member = "dddddddd-0000-4000-8000-0000000000aa"
	if err := h.Members.CreateMember(context.Background(), member, "", store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	resp := do(t, adminReq(t, http.MethodPost, h.TS.URL+"/admin/v1/modules/door-p/authorizations",
		map[string]string{"member_uuid": member}, h.Admin))
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("REST grant: status %d, want 409", resp.StatusCode)
	}

	for _, path := range []string{
		"/admin/ui/members/" + member + "/authorizations",
		"/admin/ui/modules/door-p/authorizations",
	} {
		resp := h.postForm(path, url.Values{"module_id": {"door-p"}, "member_uuid": {member}}, h.Admin)
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "approve") {
			t.Errorf("UI grant %s: status %d location %q, want 303 with an approval flash", path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}

	if _, err := h.ModuleAuths.GetAuthorization(context.Background(), member, "door-p"); err == nil {
		t.Fatal("an authorization was written for a pending member")
	}
}
