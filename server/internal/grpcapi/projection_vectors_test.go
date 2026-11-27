package grpcapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/hmacvectors"
)

// PROTO-18: the request projections the interceptor verifies are
// byte-identical to the shared golden vectors the firmware test also reads.
func TestHMACProjection_SharedGoldenVectors(t *testing.T) {
	key, vectors, err := hmacvectors.Load()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, v := range vectors {
		var req interface{}
		switch v.Kind {
		case "heartbeat":
			seq, err := strconv.ParseUint(v.Fields[1], 10, 32)
			if err != nil {
				t.Fatalf("%s: %v", v.Projection, err)
			}
			req = &pb.HeartbeatRequest{ModuleId: v.Fields[0], Sequence: uint32(seq)}
		case "access":
			nonce, err := hex.DecodeString(v.Fields[2])
			if err != nil {
				t.Fatalf("%s: %v", v.Projection, err)
			}
			req = &pb.AccessRequest{ModuleId: v.Fields[0], CredentialId: v.Fields[1],
				Nonce: nonce, RequestedAt: v.Fields[3]}
		case "provision":
			uid, err := hex.DecodeString(v.Fields[1])
			if err != nil {
				t.Fatalf("%s: %v", v.Projection, err)
			}
			req = &pb.ProvisionCredentialRequest{ModuleId: v.Fields[0], CredentialUid: uid}
		default:
			continue // access_resp: devicesig's test
		}
		got, err := hmacProjection(req)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != v.Projection {
			t.Errorf("%s projection = %q, want %q", v.Kind, got, v.Projection)
		}
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write(got)
		if sig := hex.EncodeToString(mac.Sum(nil)); sig != v.HMAC {
			t.Errorf("%s HMAC = %s, want %s", v.Projection, sig, v.HMAC)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no request vectors checked")
	}
}
