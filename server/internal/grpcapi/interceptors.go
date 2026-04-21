package grpcapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// hmacHeaderKey is the gRPC metadata key for the HMAC-SHA256 signature.
// This matches the PORTUNUS_HMAC_HEADER_NAME on the ESP32 side.
// gRPC metadata keys are lowercase by convention.
const hmacHeaderKey = "x-portunus-sig"

// hmacProjection builds the canonical byte string that the firmware signs.
// Format: "{type}|{module_id}|{key_field}"
// Using parsed fields instead of re-marshalled bytes makes verification
// independent of protobuf wire-format differences between Nanopb and Go.
func hmacProjection(req interface{}) ([]byte, error) {
	switch m := req.(type) {
	case *pb.HeartbeatRequest:
		return []byte(fmt.Sprintf("heartbeat|%s|%d", m.ModuleId, m.Sequence)), nil
	case *pb.AccessRequest:
		return []byte(fmt.Sprintf("access|%s|%s", m.ModuleId, m.CredentialId)), nil
	default:
		return nil, fmt.Errorf("unsupported request type %T", req)
	}
}

// HMACInterceptor returns a gRPC unary server interceptor that verifies
// the HMAC-SHA256 signature attached as custom metadata by the ESP32.
//
// The signature is computed over the raw protobuf request body using
// the pre-shared secret, matching the HTTP middleware behaviour.
//
// When secret is empty, the interceptor is a no-op pass-through.
func HMACInterceptor(secret string) grpc.UnaryServerInterceptor {
	if secret == "" {
		// No secret configured — pass everything through.
		return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			return handler(ctx, req)
		}
	}

	secretBytes := []byte(secret)

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract the signature from metadata.
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Errorf(codes.Unauthenticated, "missing metadata")
		}

		sigs := md.Get(hmacHeaderKey)
		if len(sigs) == 0 {
			return nil, status.Errorf(codes.Unauthenticated, "missing %s header", hmacHeaderKey)
		}
		receivedHex := sigs[0]

		// Decode the hex-encoded signature.
		receivedSig, err := hex.DecodeString(receivedHex)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "invalid %s: bad hex", hmacHeaderKey)
		}

		// Build the canonical projection the firmware signed.
		projection, projErr := hmacProjection(req)
		if projErr != nil {
			return nil, status.Errorf(codes.Internal, "HMAC projection: %v", projErr)
		}

		// Compute expected HMAC-SHA256.
		mac := hmac.New(sha256.New, secretBytes)
		mac.Write(projection)
		expectedSig := mac.Sum(nil)

		if !hmac.Equal(receivedSig, expectedSig) {
			return nil, status.Errorf(codes.Unauthenticated, "invalid %s signature", hmacHeaderKey)
		}

		return handler(ctx, req)
	}
}

// LoggingInterceptor returns a gRPC unary server interceptor that logs
// each RPC call with its method, duration, peer address, and status.
func LoggingInterceptor(logger *log.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()

		// Extract peer address for logging.
		peerAddr := "unknown"
		if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
			peerAddr = p.Addr.String()
		}

		resp, err := handler(ctx, req)

		duration := time.Since(start)
		st, _ := status.FromError(err)

		logger.Printf("grpc %s from=%s status=%s dur=%s",
			info.FullMethod, peerAddr, st.Code(), duration.Round(time.Microsecond))

		return resp, err
	}
}
