package httpapi_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
)

const fuzzHMACSecret = "device-fuzz-hmac-secret"

// FuzzDeviceEndpoints sends arbitrary bodies to the device endpoints of a
// server wired like production (SQLite, HMAC on, replay store). When sign is
// set the body carries a valid signature, modelling a module that holds the
// shared secret (gap analysis G1), so decoding and the services are reached
// and not just the signature check. Every request must get a response below
// 500; a handler panic surfaces as a transport error.
func FuzzDeviceEndpoints(f *testing.F) {
	paths := []string{"/v1/heartbeat", "/v1/access_request", "/v1/provision_credential"}
	seed := func(m proto.Message) []byte {
		b, err := proto.Marshal(m)
		if err != nil {
			f.Fatal(err)
		}
		return b
	}
	now := time.Now().UTC().Format(time.RFC3339)
	f.Add(uint8(0), seed(&pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}), true, true, "")
	f.Add(uint8(1), seed(&pb.AccessRequest{ModuleId: "door-001", CredentialId: "04:A3:2B:1C",
		Nonce: bytes.Repeat([]byte{0xAB}, 16), RequestedAt: now}), true, true, "")
	f.Add(uint8(2), seed(&pb.ProvisionCredentialRequest{ModuleId: "peu-001", CredentialUid: []byte{4, 0xA3}}), true, true, "")
	f.Add(uint8(1), []byte(`{"module_id":"door-001","credential_id":"04:A3","nonce":"q80=","requested_at":"`+now+`"}`), false, true, "")
	f.Add(uint8(0), []byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0x0f}, true, false, "00")

	h := newHarness(f, harnessOptions{HMACSecret: fuzzHMACSecret, ReplayWindow: time.Minute})
	h.seedKnownModule("door-001")
	h.seedPEU("peu-001")
	client := &http.Client{Timeout: 10 * time.Second}

	f.Fuzz(func(t *testing.T, pathIdx uint8, body []byte, protobuf, sign bool, sig string) {
		path := paths[int(pathIdx)%len(paths)]
		req, err := http.NewRequest(http.MethodPost, h.TS.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if protobuf {
			req.Header.Set("Content-Type", "application/x-protobuf")
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
		if sign {
			mac := hmac.New(sha256.New, []byte(fuzzHMACSecret))
			mac.Write(body)
			sig = hex.EncodeToString(mac.Sum(nil))
		}
		if !sendableHeaderValue(sig) {
			t.Skip("net/http refuses to send this header value")
		}
		req.Header.Set("X-Portunus-Sig", sig)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s (%d bytes): %v", path, len(body), err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			t.Fatalf("POST %s (%d bytes, protobuf=%v, signed=%v) returned %d", path, len(body), protobuf, sign, resp.StatusCode)
		}
	})
}

// sendableHeaderValue mirrors the client-side check net/http applies before
// sending a header: no control characters other than tab.
func sendableHeaderValue(v string) bool {
	for i := 0; i < len(v); i++ {
		if c := v[i]; (c < 0x20 && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
}
