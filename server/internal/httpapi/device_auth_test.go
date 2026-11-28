package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
)

// A module flashed with a different HMAC secret must be identifiable: each
// failed request is attributed to the module id it claims, over JSON and
// protobuf, and shown on the health endpoint and the module's page.
func TestDeviceAuthFailures_HTTP(t *testing.T) {
	h := newHarness(t, harnessOptions{HMACSecret: deviceSecret})
	if _, err := h.Conn.Exec(`INSERT INTO doors(door_id, name, created_at_ms, updated_at_ms) VALUES ('front','Front',0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Conn.Exec(`INSERT INTO modules(module_id, door_id, enabled, commissioned_at_ms, created_at_ms, updated_at_ms) VALUES ('door-stale','front',1,1,0,0)`); err != nil {
		t.Fatal(err)
	}

	jsonBody := []byte(`{"module_id":"door-stale","uptime_s":1}`)
	h.device("/v1/heartbeat", "application/json", jsonBody, signBody("old-secret", jsonBody))
	protoBody := mustProto(t, &pb.AccessRequest{ModuleId: "door-stale", CredentialId: "04:A3:2B:1C"})
	h.device("/v1/access_request", protoCT, protoBody, signBody("old-secret", protoBody))
	h.device("/v1/heartbeat", "application/json", []byte(`{"module_id":"door-nosig"}`), "")
	h.device("/v1/heartbeat", "application/json", []byte(`not json`), "bad")
	good := []byte(`{"module_id":"door-good"}`)
	h.device("/v1/heartbeat", "application/json", good, signBody(deviceSecret, good))

	f, ok := h.DeviceAuth.ForModule("door-stale")
	if !ok || f.Count != 2 || f.LastTransport != "http" || f.LastReason != service.ReasonBadSignature || !strings.HasPrefix(f.LastPeer, "127.0.0.1:") {
		t.Fatalf("door-stale = %+v, %v; want 2 bad-signature failures from 127.0.0.1", f, ok)
	}
	if f, ok := h.DeviceAuth.ForModule("door-nosig"); !ok || f.LastReason != service.ReasonMissingSignature {
		t.Errorf("missing signature not attributed: %+v %v", f, ok)
	}
	if _, ok := h.DeviceAuth.ForModule("unknown"); !ok {
		t.Error("unparseable body not recorded as unknown")
	}
	if _, ok := h.DeviceAuth.ForModule("door-good"); ok {
		t.Error("a correctly signed request was recorded as a failure")
	}

	var health struct {
		DeviceAuthFailures []service.DeviceAuthFailure `json:"device_auth_failures"`
	}
	decodeJSON(t, h.rest(http.MethodGet, "/admin/v1/health", nil, h.Admin), &health)
	found := false
	for _, x := range health.DeviceAuthFailures {
		if x.ModuleID == "door-stale" && x.Count == 2 {
			found = true
		}
	}
	if !found {
		t.Errorf("health does not list door-stale: %+v", health.DeviceAuthFailures)
	}

	_, page := h.getPage("/admin/ui/modules/door-stale", h.Admin)
	if !strings.Contains(page, `id="device-auth-failure"`) || !strings.Contains(page, "2 request(s)") || !strings.Contains(page, "</html>") {
		t.Errorf("module page lacks the signature-failure banner")
	}
	_, other := h.getPage("/admin/ui/modules/door-good", h.Admin)
	if strings.Contains(other, `id="device-auth-failure"`) {
		t.Error("banner shown for a module with no failures")
	}
}

func TestDeviceAuthFailures_HealthEmptyList(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	var health map[string]any
	decodeJSON(t, h.rest(http.MethodGet, "/admin/v1/health", nil, h.Admin), &health)
	if l, ok := health["device_auth_failures"].([]any); !ok || len(l) != 0 {
		t.Fatalf("device_auth_failures = %v, want an empty list", health["device_auth_failures"])
	}
}
