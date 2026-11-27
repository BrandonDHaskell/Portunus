package devicesig_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/devicesig"
)

// Golden vector shared with access_module/test/unit/test_access_projection.cpp.
// Change one, change both (CLAUDE.md hazard #5).
const (
	goldenModule = "door-001"
	goldenCred   = "04:A3:2B:1C"
	goldenGrant  = "access_resp|door-001|04:A3:2B:1C|000102030405060708090a0b0c0d0e0f|1"
	goldenDeny   = "access_resp|door-001|04:A3:2B:1C|000102030405060708090a0b0c0d0e0f|0"
	testSecret   = "test-secret-not-a-deployment-value"
)

var goldenNonce = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

func hmacHex(s string) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(s))
	return hex.EncodeToString(mac.Sum(nil))
}

// PROTO-20 (D1): the response projection carries the request nonce and the
// decision, under a prefix distinct from the request projection.
func TestAccessResponseProjection_GoldenVector(t *testing.T) {
	tests := []struct {
		granted bool
		want    string
	}{
		{true, goldenGrant},
		{false, goldenDeny},
	}
	for _, tc := range tests {
		got := string(devicesig.AccessResponseProjection(goldenModule, goldenCred, goldenNonce, tc.granted))
		if got != tc.want {
			t.Errorf("granted=%v: projection = %q, want %q", tc.granted, got, tc.want)
		}
	}
}

func TestAccessResponseSig_IsHMACOfProjection(t *testing.T) {
	got := devicesig.AccessResponseSig(testSecret, goldenModule, goldenCred, goldenNonce, true)
	if want := hmacHex(goldenGrant); got != want {
		t.Fatalf("sig = %s, want HMAC of the golden projection %s", got, want)
	}
}

// A signed grant for one request does not verify for another request that
// differs only in its nonce: the replay D1 closes.
func TestAccessResponseSig_BoundToNonce(t *testing.T) {
	other := make([]byte, 16)
	for i := range other {
		other[i] = 0xff
	}
	a := devicesig.AccessResponseSig(testSecret, goldenModule, goldenCred, goldenNonce, true)
	b := devicesig.AccessResponseSig(testSecret, goldenModule, goldenCred, other, true)
	if a == b {
		t.Fatal("signatures for different nonces must differ")
	}
}

// The pre-D1 projection ("access|{module}|{credential}|{0|1}") must not
// verify: firmware on the old format rejects new responses, and vice versa.
func TestAccessResponseSig_OldProjectionDoesNotVerify(t *testing.T) {
	sig := devicesig.AccessResponseSig(testSecret, goldenModule, goldenCred, goldenNonce, true)
	if sig == hmacHex("access|door-001|04:A3:2B:1C|1") {
		t.Fatal("new signature matches the old nonce-free projection")
	}
}

func TestAccessResponseSig_EmptySecretOrNonce(t *testing.T) {
	if got := devicesig.AccessResponseSig("", goldenModule, goldenCred, goldenNonce, true); got != "" {
		t.Errorf("empty secret: sig = %q, want empty", got)
	}
	if got := devicesig.AccessResponseSig(testSecret, goldenModule, goldenCred, nil, true); got != "" {
		t.Errorf("no nonce: sig = %q, want empty", got)
	}
}
