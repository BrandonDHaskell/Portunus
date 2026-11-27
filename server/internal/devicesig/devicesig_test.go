package devicesig_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/devicesig"
	"github.com/BrandonDHaskell/Portunus/server/internal/hmacvectors"
)

const (
	goldenModule = "door-001"
	goldenCred   = "04:A3:2B:1C"
	testSecret   = "test-secret-not-a-deployment-value"
)

var goldenNonce = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

func hmacHex(s string) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(s))
	return hex.EncodeToString(mac.Sum(nil))
}

// PROTO-18 / PROTO-20 (D1): the access-decision projection and signature are
// byte-identical to the shared golden vectors (proto/testdata/hmac_vectors.txt)
// that the firmware's test_access_protocol also reads.
func TestAccessResponse_SharedGoldenVectors(t *testing.T) {
	key, vectors, err := hmacvectors.Load()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, v := range vectors {
		if v.Kind != "access_resp" {
			continue
		}
		nonce, err := hex.DecodeString(v.Fields[2])
		if err != nil {
			t.Fatalf("%s: %v", v.Projection, err)
		}
		granted := v.Fields[3] == "1"
		got := string(devicesig.AccessResponseProjection(v.Fields[0], v.Fields[1], nonce, granted))
		if got != v.Projection {
			t.Errorf("projection = %q, want %q", got, v.Projection)
		}
		if sig := devicesig.AccessResponseSig(key, v.Fields[0], v.Fields[1], nonce, granted); sig != v.HMAC {
			t.Errorf("%s: sig = %s, want %s", v.Projection, sig, v.HMAC)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no access_resp vectors checked")
	}
}

func TestAccessResponseSig_IsHMACOfProjection(t *testing.T) {
	got := devicesig.AccessResponseSig(testSecret, goldenModule, goldenCred, goldenNonce, true)
	proj := string(devicesig.AccessResponseProjection(goldenModule, goldenCred, goldenNonce, true))
	if want := hmacHex(proj); got != want {
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
