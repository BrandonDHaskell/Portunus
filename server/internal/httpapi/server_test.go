package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/httpapi"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// newTestServer wires up the full dependency graph using in-memory stores
// and returns an httptest.Server whose URL can be hit with a plain http.Client.
func newTestServer(t *testing.T, knownModules []string, policy service.AccessPolicy) *httptest.Server {
	t.Helper()

	deviceStore := memory.NewDeviceStore(knownModules)
	heartbeatStore := memory.New()
	accessEventStore := memory.NewAccessEventStore()
	registry := service.NewDeviceRegistry(deviceStore)
	heartbeatSvc := service.NewHeartbeatService(heartbeatStore, registry)
	accessSvc := service.NewAccessService(registry, policy, accessEventStore)

	srv := httpapi.NewServer(httpapi.Dependencies{
		Logger:           log.New(io.Discard, "", 0),
		Addr:             ":0",
		HeartbeatService: heartbeatSvc,
		AccessService:    accessSvc,
	})

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// ── Heartbeat ────────────────────────────────────────────────────────────────

func TestHeartbeat_KnownModule_OK(t *testing.T) {
	ts := newTestServer(t, []string{"door-001"}, service.AccessPolicy{})

	body := []byte(`{"module_id":"door-001","uptime_s":42}`)
	resp, err := http.Post(ts.URL+"/v1/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var hbResp types.HeartbeatResponse
	if err := json.NewDecoder(resp.Body).Decode(&hbResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !hbResp.OK {
		t.Error("expected ok=true")
	}
	if !hbResp.Known {
		t.Error("expected known=true for a configured module")
	}
	if hbResp.ModuleID != "door-001" {
		t.Errorf("expected module_id=door-001, got %q", hbResp.ModuleID)
	}
}

func TestHeartbeat_UnknownModule_StillAccepted(t *testing.T) {
	ts := newTestServer(t, []string{"door-001"}, service.AccessPolicy{})

	body := []byte(`{"module_id":"unknown-device","uptime_s":1}`)
	resp, err := http.Post(ts.URL+"/v1/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var hbResp types.HeartbeatResponse
	if err := json.NewDecoder(resp.Body).Decode(&hbResp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !hbResp.OK {
		t.Error("expected ok=true (heartbeats are accepted from unknown modules)")
	}
	if hbResp.Known {
		t.Error("expected known=false for an unknown module")
	}
}

func TestHeartbeat_MissingModuleID_400(t *testing.T) {
	ts := newTestServer(t, nil, service.AccessPolicy{})

	body := []byte(`{"uptime_s":42}`)
	resp, err := http.Post(ts.URL+"/v1/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHeartbeat_InvalidJSON_400(t *testing.T) {
	ts := newTestServer(t, nil, service.AccessPolicy{})

	body := []byte(`not json at all`)
	resp, err := http.Post(ts.URL+"/v1/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// ── Access Request ───────────────────────────────────────────────────────────

func TestAccessRequest_AllowAll_Granted(t *testing.T) {
	ts := newTestServer(t, []string{"door-001"}, service.AccessPolicy{AllowAll: true})

	body := []byte(`{"module_id":"door-001","card_id":"AABBCCDD"}`)
	resp, err := http.Post(ts.URL+"/v1/access_request", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var ar types.AccessResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !ar.Granted {
		t.Error("expected granted=true with AllowAll policy")
	}
	if ar.Reason != "allow_all" {
		t.Errorf("expected reason=allow_all, got %q", ar.Reason)
	}
}

func TestAccessRequest_CardNotAllowed_Denied(t *testing.T) {
	allowed := map[string]struct{}{"AABBCCDD": {}}
	ts := newTestServer(t, []string{"door-001"}, service.AccessPolicy{
		AllowedCardIDs: allowed,
	})

	body := []byte(`{"module_id":"door-001","card_id":"UNKNOWN_CARD"}`)
	resp, err := http.Post(ts.URL+"/v1/access_request", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var ar types.AccessResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if ar.Granted {
		t.Error("expected granted=false for unknown card")
	}
	if ar.Reason != "card_not_allowed" {
		t.Errorf("expected reason=card_not_allowed, got %q", ar.Reason)
	}
}

func TestAccessRequest_UnknownModule_403(t *testing.T) {
	ts := newTestServer(t, []string{"door-001"}, service.AccessPolicy{AllowAll: true})

	body := []byte(`{"module_id":"rogue-device","card_id":"AABBCCDD"}`)
	resp, err := http.Post(ts.URL+"/v1/access_request", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestAccessRequest_MissingCardID_400(t *testing.T) {
	ts := newTestServer(t, []string{"door-001"}, service.AccessPolicy{})

	body := []byte(`{"module_id":"door-001"}`)
	resp, err := http.Post(ts.URL+"/v1/access_request", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
