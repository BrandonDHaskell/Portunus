package grpcapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/grpcapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const testHMACSecret = "test-hmac-secret"

// hmacSigHeader matches the unexported hmacHeaderKey in interceptors.go.
const hmacSigHeader = "x-portunus-sig"

// sign computes the HMAC signature for req using the same projection as the interceptor.
func sign(req interface{}, secret string) string {
	var proj string
	switch m := req.(type) {
	case *pb.HeartbeatRequest:
		proj = fmt.Sprintf("heartbeat|%s|%d", m.ModuleId, m.Sequence)
	case *pb.AccessRequest:
		proj = fmt.Sprintf("access|%s|%s|%s|%s",
			m.ModuleId,
			m.CredentialId,
			hex.EncodeToString(m.Nonce),
			m.RequestedAt,
		)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(proj))
	return hex.EncodeToString(mac.Sum(nil))
}

func invoke(interceptor grpc.UnaryServerInterceptor, ctx context.Context, req interface{}) (interface{}, error) {
	info := &grpc.UnaryServerInfo{FullMethod: "/portunus.v1.PornusService/Test"}
	handler := func(_ context.Context, r interface{}) (interface{}, error) { return r, nil }
	return interceptor(ctx, req, info, handler)
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error, got nil", want)
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != want {
		t.Errorf("expected %s, got %s: %s", want, st.Code(), st.Message())
	}
}

// freshAccessRequest returns an AccessRequest with a unique nonce and a
// current timestamp — the minimum valid payload for replay-protected requests.
func freshAccessRequest(moduleID, credentialID string) *pb.AccessRequest {
	nonce := make([]byte, 16)
	for i := range nonce {
		nonce[i] = byte(i + 1) // deterministic but unique per test call if args differ
	}
	return &pb.AccessRequest{
		ModuleId:     moduleID,
		CredentialId: credentialID,
		Nonce:        nonce,
		RequestedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// ── empty secret ─────────────────────────────────────────────────────────────

func TestHMACInterceptor_EmptySecret_Passthrough(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor("", nil)
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}

	_, err := invoke(interceptor, context.Background(), req)
	if err != nil {
		t.Fatalf("empty secret should be a no-op passthrough, got: %v", err)
	}
}

// ── valid signatures ──────────────────────────────────────────────────────────

func TestHMACInterceptor_ValidHeartbeat_Passes(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, nil)
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 42}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req, testHMACSecret)))

	_, err := invoke(interceptor, ctx, req)
	if err != nil {
		t.Fatalf("valid heartbeat HMAC should pass, got: %v", err)
	}
}

func TestHMACInterceptor_ValidAccessRequest_Passes(t *testing.T) {
	store := grpcapi.NewReplayStore(60 * time.Second)
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, store)
	req := freshAccessRequest("door-001", "AABBCCDD")

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req, testHMACSecret)))

	_, err := invoke(interceptor, ctx, req)
	if err != nil {
		t.Fatalf("valid access-request HMAC should pass, got: %v", err)
	}
}

// ── rejection cases ───────────────────────────────────────────────────────────

func TestHMACInterceptor_MissingMetadata_Unauthenticated(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, nil)
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}

	_, err := invoke(interceptor, context.Background(), req)
	assertCode(t, err, codes.Unauthenticated)
}

func TestHMACInterceptor_MissingSignatureHeader_Unauthenticated(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, nil)
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("some-other-header", "value"))
	_, err := invoke(interceptor, ctx, req)
	assertCode(t, err, codes.Unauthenticated)
}

func TestHMACInterceptor_WrongSecret_Unauthenticated(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, nil)
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req, "wrong-secret")))
	_, err := invoke(interceptor, ctx, req)
	assertCode(t, err, codes.Unauthenticated)
}

func TestHMACInterceptor_TamperedField_Unauthenticated(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, nil)

	// Signature computed for door-001, but request claims door-002.
	sigReq := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}
	sendReq := &pb.HeartbeatRequest{ModuleId: "door-002", Sequence: 1}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(sigReq, testHMACSecret)))
	_, err := invoke(interceptor, ctx, sendReq)
	assertCode(t, err, codes.Unauthenticated)
}

func TestHMACInterceptor_BadHexSignature_Unauthenticated(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, nil)
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, "not-valid-hex!!"))
	_, err := invoke(interceptor, ctx, req)
	assertCode(t, err, codes.Unauthenticated)
}

// ── replay protection ─────────────────────────────────────────────────────────

func TestHMACInterceptor_ReplayedNonce_Unauthenticated(t *testing.T) {
	store := grpcapi.NewReplayStore(60 * time.Second)
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, store)
	req := freshAccessRequest("door-001", "AABBCCDD")

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req, testHMACSecret)))

	// First request succeeds.
	if _, err := invoke(interceptor, ctx, req); err != nil {
		t.Fatalf("first request should pass, got: %v", err)
	}

	// Identical request (same nonce) is a replay — must be rejected.
	_, err := invoke(interceptor, ctx, req)
	assertCode(t, err, codes.Unauthenticated)
}

func TestHMACInterceptor_FreshNonce_Accepted(t *testing.T) {
	store := grpcapi.NewReplayStore(60 * time.Second)
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, store)

	// Two requests with different nonces from the same module should both pass.
	req1 := freshAccessRequest("door-001", "AABBCCDD")
	req2 := freshAccessRequest("door-001", "AABBCCDD")
	req2.Nonce = make([]byte, 16)
	for i := range req2.Nonce {
		req2.Nonce[i] = byte(i + 100)
	}

	ctx1 := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req1, testHMACSecret)))
	ctx2 := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req2, testHMACSecret)))

	if _, err := invoke(interceptor, ctx1, req1); err != nil {
		t.Fatalf("first request should pass, got: %v", err)
	}
	if _, err := invoke(interceptor, ctx2, req2); err != nil {
		t.Fatalf("second request with fresh nonce should pass, got: %v", err)
	}
}

func TestHMACInterceptor_StaleTimestamp_Unauthenticated(t *testing.T) {
	store := grpcapi.NewReplayStore(60 * time.Second)
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, store)

	req := freshAccessRequest("door-001", "AABBCCDD")
	// Timestamp 90 seconds in the past — outside the 60s window.
	req.RequestedAt = time.Now().UTC().Add(-90 * time.Second).Format(time.RFC3339Nano)

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req, testHMACSecret)))

	_, err := invoke(interceptor, ctx, req)
	assertCode(t, err, codes.Unauthenticated)
}

func TestHMACInterceptor_EmptyTimestamp_Accepted(t *testing.T) {
	// Empty requested_at means the device clock is not yet synced.
	// The server skips the timestamp check but still enforces nonce uniqueness.
	store := grpcapi.NewReplayStore(60 * time.Second)
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, store)

	req := freshAccessRequest("door-001", "AABBCCDD")
	req.RequestedAt = "" // clock not synced

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req, testHMACSecret)))

	if _, err := invoke(interceptor, ctx, req); err != nil {
		t.Fatalf("empty requested_at should be accepted, got: %v", err)
	}
}

func TestHMACInterceptor_NoStore_NoReplayCheck(t *testing.T) {
	// When store is nil, replay protection is disabled — same nonce can repeat.
	interceptor := grpcapi.HMACInterceptor(testHMACSecret, nil)
	req := freshAccessRequest("door-001", "AABBCCDD")

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req, testHMACSecret)))

	if _, err := invoke(interceptor, ctx, req); err != nil {
		t.Fatalf("first request should pass, got: %v", err)
	}
	if _, err := invoke(interceptor, ctx, req); err != nil {
		t.Fatalf("repeated request with no store should pass, got: %v", err)
	}
}
