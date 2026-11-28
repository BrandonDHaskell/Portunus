package httpapi_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/grpcapi"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

const deviceSecret = "httpapi-test-hmac-secret-not-real"

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// device posts body to a device endpoint. sig == "" sends no signature header.
func (h *harness) device(path, contentType string, body []byte, sig string) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.TS.URL+path, bytes.NewReader(body))
	if err != nil {
		h.t.Fatalf("device request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if sig != "" {
		req.Header.Set("X-Portunus-Sig", sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("device do: %v", err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func errCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &body)
	return body.Error
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ── HMAC middleware ──────────────────────────────────────────────────────────

func TestHMACMiddleware(t *testing.T) {
	h := newHarness(t, harnessOptions{HMACSecret: deviceSecret})
	h.seedKnownModule("door-001")
	body := []byte(`{"module_id":"door-001","uptime_s":7}`)

	t.Run("missing signature is 401", func(t *testing.T) {
		resp := h.device("/v1/heartbeat", "application/json", body, "")
		if resp.StatusCode != http.StatusUnauthorized || errCode(t, resp) != "missing_signature" {
			t.Fatalf("got %d, want 401 missing_signature", resp.StatusCode)
		}
	})
	t.Run("wrong secret is 401", func(t *testing.T) {
		resp := h.device("/v1/heartbeat", "application/json", body, signBody("other-secret", body))
		if resp.StatusCode != http.StatusUnauthorized || errCode(t, resp) != "invalid_signature" {
			t.Fatalf("got %d, want 401 invalid_signature", resp.StatusCode)
		}
	})
	t.Run("tampered body is 401", func(t *testing.T) {
		tampered := []byte(`{"module_id":"door-002","uptime_s":7}`)
		resp := h.device("/v1/heartbeat", "application/json", tampered, signBody(deviceSecret, body))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", resp.StatusCode)
		}
	})
	t.Run("non-hex garbage is 401", func(t *testing.T) {
		resp := h.device("/v1/heartbeat", "application/json", body, "zz-not-hex")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", resp.StatusCode)
		}
	})
	t.Run("valid signature passes and body reaches handler", func(t *testing.T) {
		resp := h.device("/v1/heartbeat", "application/json", body, signBody(deviceSecret, body))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("got %d, want 200", resp.StatusCode)
		}
		var hb types.HeartbeatResponse
		if err := json.NewDecoder(resp.Body).Decode(&hb); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if hb.ModuleID != "door-001" || !hb.Known {
			t.Fatalf("handler did not see the signed body: %+v", hb)
		}
	})
	t.Run("GET is not signature-checked", func(t *testing.T) {
		resp, err := http.Get(h.TS.URL + "/v1/heartbeat")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			t.Fatal("GET was rejected by the HMAC middleware")
		}
	})
	t.Run("admin POST bypasses HMAC", func(t *testing.T) {
		resp := h.device("/admin/v1/login", "application/json", []byte(`not json`), "")
		if resp.StatusCode != http.StatusBadRequest || errCode(t, resp) != "bad_json" {
			t.Fatalf("got %d, want 400 bad_json from the login handler", resp.StatusCode)
		}
	})
}

// ── Access request nonce + replay (HTTP path) ────────────────────────────────

func signedAccess(t *testing.T, req types.AccessRequest) ([]byte, string) {
	b := mustJSON(t, req)
	return b, signBody(deviceSecret, b)
}

func TestHTTPAccess_ReplayProtection(t *testing.T) {
	h := newHarness(t, harnessOptions{HMACSecret: deviceSecret, ReplayWindow: time.Minute, AllowAll: true})
	h.seedKnownModule("door-001")
	now := time.Now().UTC().Format(time.RFC3339)
	nonce := bytes.Repeat([]byte{0xAB}, 16)

	t.Run("missing nonce is 401", func(t *testing.T) {
		b, sig := signedAccess(t, types.AccessRequest{ModuleID: "door-001", CredentialID: "04:A3:2B:1C", RequestedAt: now})
		resp := h.device("/v1/access_request", "application/json", b, sig)
		if resp.StatusCode != http.StatusUnauthorized || errCode(t, resp) != "missing_nonce" {
			t.Fatalf("got %d, want 401 missing_nonce", resp.StatusCode)
		}
	})
	t.Run("fresh nonce accepted, replay rejected", func(t *testing.T) {
		b, sig := signedAccess(t, types.AccessRequest{ModuleID: "door-001", CredentialID: "04:A3:2B:1C", RequestedAt: now, Nonce: nonce})
		if resp := h.device("/v1/access_request", "application/json", b, sig); resp.StatusCode != http.StatusOK {
			t.Fatalf("first request: got %d, want 200", resp.StatusCode)
		}
		resp := h.device("/v1/access_request", "application/json", b, sig)
		if resp.StatusCode != http.StatusUnauthorized || errCode(t, resp) != "replay" {
			t.Fatalf("replay: got %d, want 401 replay", resp.StatusCode)
		}
	})
	t.Run("stale timestamp rejected", func(t *testing.T) {
		stale := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
		b, sig := signedAccess(t, types.AccessRequest{ModuleID: "door-001", CredentialID: "04:A3:2B:1C", RequestedAt: stale, Nonce: bytes.Repeat([]byte{0x01}, 16)})
		if resp := h.device("/v1/access_request", "application/json", b, sig); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", resp.StatusCode)
		}
	})
	t.Run("missing timestamp rejected", func(t *testing.T) {
		b, sig := signedAccess(t, types.AccessRequest{ModuleID: "door-001", CredentialID: "04:A3:2B:1C", Nonce: bytes.Repeat([]byte{0x02}, 16)})
		if resp := h.device("/v1/access_request", "application/json", b, sig); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", resp.StatusCode)
		}
	})
}

func TestHTTPAccess_NonceRequiredWithoutReplayStore(t *testing.T) {
	h := newHarness(t, harnessOptions{HMACSecret: deviceSecret, AllowAll: true})
	h.seedKnownModule("door-001")
	b, sig := signedAccess(t, types.AccessRequest{ModuleID: "door-001", CredentialID: "04:A3:2B:1C"})
	resp := h.device("/v1/access_request", "application/json", b, sig)
	if resp.StatusCode != http.StatusUnauthorized || errCode(t, resp) != "missing_nonce" {
		t.Fatalf("got %d, want 401 missing_nonce", resp.StatusCode)
	}
}

// ── Protobuf device path ─────────────────────────────────────────────────────

const protoCT = "application/x-protobuf"

func mustProto(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readProtoResp(t *testing.T, resp *http.Response, m proto.Message) {
	t.Helper()
	if ct := resp.Header.Get("Content-Type"); ct != protoCT {
		t.Fatalf("response Content-Type = %q, want %q", ct, protoCT)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(b, m); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
}

func TestProtobuf_Heartbeat(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.seedKnownModule("door-001")
	resp := h.device("/v1/heartbeat", protoCT, mustProto(t, &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 3}), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got pb.HeartbeatResponse
	readProtoResp(t, resp, &got)
	if !got.GetOk() || !got.GetKnown() || got.GetModuleId() != "door-001" || got.GetServerTime() == "" {
		t.Fatalf("unexpected response %+v", &got)
	}
}

func TestProtobuf_BadBody_400(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	for _, path := range []string{"/v1/heartbeat", "/v1/access_request", "/v1/provision_credential"} {
		resp := h.device(path, protoCT, []byte{0xFF, 0xFF, 0xFF}, "")
		if resp.StatusCode != http.StatusBadRequest || errCode(t, resp) != "bad_proto" {
			t.Errorf("%s: got %d, want 400 bad_proto", path, resp.StatusCode)
		}
	}
}

func TestProtobuf_AccessGrantedAndDoorClosedRecorded(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.seedKnownModule("door-001")
	m := h.seedActiveMember("eeeeeeee-0000-4000-8000-000000000001")
	h.enroll(m, "04:A3:2B:1C")
	h.grant(m, "door-001")

	closed := true
	resp := h.device("/v1/access_request", protoCT, mustProto(t, &pb.AccessRequest{
		ModuleId: "door-001", CredentialId: "04:A3:2B:1C", DoorClosed: &closed,
	}), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got pb.AccessResponse
	readProtoResp(t, resp, &got)
	if !got.GetGranted() || got.GetReason() != "credential_allowed" {
		t.Fatalf("got granted=%v reason=%q", got.GetGranted(), got.GetReason())
	}

	var dc *int
	if err := h.Conn.QueryRow(`SELECT door_closed FROM access_events ORDER BY rowid DESC LIMIT 1`).Scan(&dc); err != nil {
		t.Fatalf("read access event: %v", err)
	}
	if dc == nil || *dc != 1 {
		t.Fatalf("door_closed not mapped from proto: %v", dc)
	}
}

func TestProtobuf_AccessUnknownModule_403WithProtoBody(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	resp := h.device("/v1/access_request", protoCT, mustProto(t, &pb.AccessRequest{ModuleId: "ghost", CredentialId: "04:A3:2B:1C"}), "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}
	var got pb.AccessResponse
	readProtoResp(t, resp, &got)
	if got.GetKnown() || got.GetGranted() || got.GetReason() != "unknown_module" {
		t.Fatalf("unexpected %+v", &got)
	}
}

func TestProtobuf_ProvisionCapture(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.seedPEU("peu-001")
	resp := h.device("/v1/provision_credential", protoCT, mustProto(t, &pb.ProvisionCredentialRequest{
		ModuleId: "peu-001", CredentialUid: []byte{0x04, 0xA3, 0x2B, 0x1C},
	}), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got pb.ProvisionCredentialResponse
	readProtoResp(t, resp, &got)
	if got.GetMemberUuid() == "" || got.GetStatus() != pb.ProvisionStatus_PROVISION_STATUS_PENDING_CREATED {
		t.Fatalf("unexpected %+v", &got)
	}
}

func TestHTTPProvision_StatusMapping(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.seedKnownModule("acu-001")
	cases := []struct {
		name string
		body string
		want int
		code string
	}{
		{"missing module", `{"credential_uid":"BKMrHA=="}`, http.StatusBadRequest, "invalid_module_id"},
		{"missing uid", `{"module_id":"acu-001"}`, http.StatusBadRequest, "invalid_credential_uid"},
		{"unknown module", `{"module_id":"ghost","credential_uid":"BKMrHA=="}`, http.StatusForbidden, "unknown_module"},
		{"unknown field", `{"module_id":"acu-001","credential_uid":"BKMrHA==","x":1}`, http.StatusBadRequest, "bad_json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := h.device("/v1/provision_credential", "application/json", []byte(tc.body), "")
			if resp.StatusCode != tc.want || errCode(t, resp) != tc.code {
				t.Fatalf("got %d, want %d %s", resp.StatusCode, tc.want, tc.code)
			}
		})
	}
	t.Run("ACU module is refused provisioning", func(t *testing.T) {
		resp := h.device("/v1/provision_credential", "application/json", []byte(`{"module_id":"acu-001","credential_uid":"BKMrHA=="}`), "")
		var got types.ProvisionCredentialResponse
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || got.Status != types.ProvisionStatusUnauthorized || got.MemberUUID != "" {
			t.Fatalf("got %d %+v, want 200 unauthorized with no member", resp.StatusCode, got)
		}
	})
}

// ── Transport parity: two transports, one brain ──────────────────────────────

// The same door tap must reach the same decision over HTTP JSON, HTTP
// protobuf and gRPC. A divergence means business logic leaked into a handler.
func TestTransportParity_AccessDecision(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.seedKnownModule("door-001")
	h.seedKnownModule("door-002")
	m := h.seedActiveMember("eeeeeeee-0000-4000-8000-000000000002")
	h.enroll(m, "04:A3:2B:1C")
	h.grant(m, "door-001")

	gs := grpcapi.NewServer(grpcapi.Dependencies{
		Logger:           log.New(io.Discard, "", 0),
		HeartbeatService: h.Heartbeat,
		AccessService:    h.Access,
		ProvisionService: h.Provision,
	})

	cases := []struct {
		name, module, cred, reason string
		granted                    bool
	}{
		{"granted", "door-001", "04:A3:2B:1C", "credential_allowed", true},
		{"lowercase uid granted", "door-001", "04:a3:2b:1c", "credential_allowed", true},
		{"not authorized for module", "door-002", "04:A3:2B:1C", "credential_not_authorized", false},
		{"unknown credential", "door-001", "04:00:00:01", "credential_not_found", false},
		{"bad credential format", "door-001", "not-a-uid", "invalid_credential_format", false},
		{"unknown module", "ghost", "04:A3:2B:1C", "unknown_module", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// HTTP JSON
			resp := h.device("/v1/access_request", "application/json",
				mustJSON(t, types.AccessRequest{ModuleID: tc.module, CredentialID: tc.cred}), "")
			var js types.AccessResponse
			if err := json.NewDecoder(resp.Body).Decode(&js); err != nil {
				t.Fatalf("json decode: %v", err)
			}
			// HTTP protobuf
			resp = h.device("/v1/access_request", protoCT,
				mustProto(t, &pb.AccessRequest{ModuleId: tc.module, CredentialId: tc.cred}), "")
			var pr pb.AccessResponse
			readProtoResp(t, resp, &pr)
			// gRPC
			gr, err := gs.RequestAccess(context.Background(), &pb.AccessRequest{ModuleId: tc.module, CredentialId: tc.cred})
			if err != nil {
				t.Fatalf("gRPC: %v", err)
			}

			for transport, got := range map[string]struct {
				granted bool
				reason  string
			}{
				"http-json":  {js.Granted, js.Reason},
				"http-proto": {pr.GetGranted(), pr.GetReason()},
				"grpc":       {gr.GetGranted(), gr.GetReason()},
			} {
				if got.granted != tc.granted || got.reason != tc.reason {
					t.Errorf("%s: granted=%v reason=%q, want granted=%v reason=%q",
						transport, got.granted, got.reason, tc.granted, tc.reason)
				}
			}
		})
	}
}
