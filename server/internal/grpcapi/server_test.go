package grpcapi_test

import (
	"bytes"
	"context"
	"io"
	"log"
	"testing"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/devicesig"
	"github.com/BrandonDHaskell/Portunus/server/internal/grpcapi"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const accessSigSecret = "test-secret-not-a-deployment-value"

// trailerStream captures what a handler sets with grpc.SetTrailer, without a
// network server.
type trailerStream struct{ trailer metadata.MD }

func (s *trailerStream) Method() string               { return "/portunus.v1.PortunusService/RequestAccess" }
func (s *trailerStream) SetHeader(metadata.MD) error  { return nil }
func (s *trailerStream) SendHeader(metadata.MD) error { return nil }
func (s *trailerStream) SetTrailer(md metadata.MD) error {
	s.trailer = metadata.Join(s.trailer, md)
	return nil
}

func newAccessServer(t *testing.T, secret string) *grpcapi.Server {
	t.Helper()
	registry := service.NewDeviceRegistry(memory.NewDeviceStore([]string{"door-001"}))
	access := service.NewAccessService(registry, service.AccessPolicy{AllowAll: true},
		memory.NewAccessEventStore())
	return grpcapi.NewServer(grpcapi.Dependencies{
		Logger:        log.New(io.Discard, "", 0),
		AccessService: access,
		HMACSecret:    secret,
	})
}

// requestAccess calls RequestAccess directly and returns the response trailer
// signature ("" if none).
func requestAccess(t *testing.T, srv *grpcapi.Server, nonce []byte) string {
	t.Helper()
	st := &trailerStream{}
	ctx := grpc.NewContextWithServerTransportStream(context.Background(), st)
	resp, err := srv.RequestAccess(ctx, &pb.AccessRequest{
		ModuleId: "door-001", CredentialId: "04:A3:2B:1C", Nonce: nonce,
	})
	if err != nil {
		t.Fatalf("RequestAccess: %v", err)
	}
	if !resp.GetGranted() {
		t.Fatalf("expected a grant under AllowAll, got %+v", resp)
	}
	if v := st.trailer.Get("x-portunus-sig"); len(v) == 1 {
		return v[0]
	}
	return ""
}

// D1: the gRPC trailer signs this request's nonce, so the signature for one
// request never verifies for another.
func TestRequestAccess_TrailerSignsRequestNonce(t *testing.T) {
	srv := newAccessServer(t, accessSigSecret)
	n1 := bytes.Repeat([]byte{0x11}, 16)
	n2 := bytes.Repeat([]byte{0x22}, 16)

	s1 := requestAccess(t, srv, n1)
	s2 := requestAccess(t, srv, n2)
	if want := devicesig.AccessResponseSig(accessSigSecret, "door-001", "04:A3:2B:1C", n1, true); s1 != want {
		t.Errorf("trailer for n1 = %q, want %q", s1, want)
	}
	if want := devicesig.AccessResponseSig(accessSigSecret, "door-001", "04:A3:2B:1C", n2, true); s2 != want {
		t.Errorf("trailer for n2 = %q, want %q", s2, want)
	}
	if s1 == s2 {
		t.Error("requests with different nonces got the same signature")
	}
}

// No nonce, or HMAC disabled: no trailer at all, which the firmware denies.
func TestRequestAccess_NoTrailerWithoutNonceOrSecret(t *testing.T) {
	if sig := requestAccess(t, newAccessServer(t, accessSigSecret), nil); sig != "" {
		t.Errorf("no nonce: trailer = %q, want none", sig)
	}
	if sig := requestAccess(t, newAccessServer(t, ""), bytes.Repeat([]byte{1}, 16)); sig != "" {
		t.Errorf("HMAC disabled: trailer = %q, want none", sig)
	}
}
