package grpcapi

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
)

// FuzzHMACProjection checks that the signed request projections never panic
// and, for field values without the '|' separator, encode every field so it
// can be recovered: two different requests then never share a signed payload.
// '|' inside a field is not rejected by the projection itself (module IDs are
// only accepted if commissioned, and a credential_id containing '|' fails
// ParseCredentialUID), so that case is only checked for panics.
func FuzzHMACProjection(f *testing.F) {
	f.Add("door-001", "04:A3:2B:1C", []byte("0123456789abcdef"), "2026-11-27T12:00:00Z", uint32(7), []byte{0x04, 0xA3})
	f.Add("", "", []byte{}, "", uint32(0), []byte{})
	f.Add("a|b", "c|d", []byte{0xff}, "|", uint32(4294967295), []byte{0x00})
	f.Fuzz(func(t *testing.T, moduleID, credentialID string, nonce []byte, requestedAt string, seq uint32, uid []byte) {
		access, err := hmacProjection(&pb.AccessRequest{
			ModuleId: moduleID, CredentialId: credentialID, Nonce: nonce, RequestedAt: requestedAt,
		})
		if err != nil {
			t.Fatalf("access projection: %v", err)
		}
		heartbeat, err := hmacProjection(&pb.HeartbeatRequest{ModuleId: moduleID, Sequence: seq})
		if err != nil {
			t.Fatalf("heartbeat projection: %v", err)
		}
		provision, err := hmacProjection(&pb.ProvisionCredentialRequest{ModuleId: moduleID, CredentialUid: uid})
		if err != nil {
			t.Fatalf("provision projection: %v", err)
		}

		if strings.ContainsRune(moduleID, '|') || strings.ContainsRune(credentialID, '|') ||
			strings.ContainsRune(requestedAt, '|') {
			return
		}
		want := []string{"access", moduleID, credentialID, hex.EncodeToString(nonce), requestedAt}
		if got := strings.Split(string(access), "|"); !slices.Equal(got, want) {
			t.Fatalf("access projection %q splits to %q, want %q", access, got, want)
		}
		want = []string{"heartbeat", moduleID, fmt.Sprint(seq)}
		if got := strings.Split(string(heartbeat), "|"); !slices.Equal(got, want) {
			t.Fatalf("heartbeat projection %q splits to %q, want %q", heartbeat, got, want)
		}
		want = []string{"provision", moduleID, hex.EncodeToString(uid)}
		if got := strings.Split(string(provision), "|"); !slices.Equal(got, want) {
			t.Fatalf("provision projection %q splits to %q, want %q", provision, got, want)
		}
	})
}
