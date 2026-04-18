package grpcapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

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
		proj = fmt.Sprintf("access|%s|%s", m.ModuleId, m.CardId)
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

// ── empty secret ─────────────────────────────────────────────────────────────

func TestHMACInterceptor_EmptySecret_Passthrough(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor("")
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}

	_, err := invoke(interceptor, context.Background(), req)
	if err != nil {
		t.Fatalf("empty secret should be a no-op passthrough, got: %v", err)
	}
}

// ── valid signatures ──────────────────────────────────────────────────────────

func TestHMACInterceptor_ValidHeartbeat_Passes(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret)
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 42}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req, testHMACSecret)))

	_, err := invoke(interceptor, ctx, req)
	if err != nil {
		t.Fatalf("valid heartbeat HMAC should pass, got: %v", err)
	}
}

func TestHMACInterceptor_ValidAccessRequest_Passes(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret)
	req := &pb.AccessRequest{ModuleId: "door-001", CardId: "AABBCCDD"}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req, testHMACSecret)))

	_, err := invoke(interceptor, ctx, req)
	if err != nil {
		t.Fatalf("valid access-request HMAC should pass, got: %v", err)
	}
}

// ── rejection cases ───────────────────────────────────────────────────────────

func TestHMACInterceptor_MissingMetadata_Unauthenticated(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret)
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}

	_, err := invoke(interceptor, context.Background(), req)
	assertCode(t, err, codes.Unauthenticated)
}

func TestHMACInterceptor_MissingSignatureHeader_Unauthenticated(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret)
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("some-other-header", "value"))
	_, err := invoke(interceptor, ctx, req)
	assertCode(t, err, codes.Unauthenticated)
}

func TestHMACInterceptor_WrongSecret_Unauthenticated(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret)
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(req, "wrong-secret")))
	_, err := invoke(interceptor, ctx, req)
	assertCode(t, err, codes.Unauthenticated)
}

func TestHMACInterceptor_TamperedField_Unauthenticated(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret)

	// Signature computed for door-001, but request claims door-002.
	sigReq := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}
	sendReq := &pb.HeartbeatRequest{ModuleId: "door-002", Sequence: 1}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, sign(sigReq, testHMACSecret)))
	_, err := invoke(interceptor, ctx, sendReq)
	assertCode(t, err, codes.Unauthenticated)
}

func TestHMACInterceptor_BadHexSignature_Unauthenticated(t *testing.T) {
	interceptor := grpcapi.HMACInterceptor(testHMACSecret)
	req := &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(hmacSigHeader, "not-valid-hex!!"))
	_, err := invoke(interceptor, ctx, req)
	assertCode(t, err, codes.Unauthenticated)
}
