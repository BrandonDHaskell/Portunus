package grpcapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// hmacHeaderKey is the gRPC metadata key for the HMAC-SHA256 signature.
// This matches the PORTUNUS_HMAC_HEADER_NAME on the ESP32 side.
// gRPC metadata keys are lowercase by convention.
const hmacHeaderKey = "x-portunus-sig"

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

		// Re-marshal the request to get the exact protobuf bytes.
		// The ESP32 computes HMAC over the Nanopb-encoded body, which is
		// wire-compatible with the Go proto.Marshal output for the same message.
		msg, ok := req.(proto.Message)
		if !ok {
			return nil, status.Errorf(codes.Internal, "request is not a proto.Message")
		}

		body, err := proto.Marshal(msg)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to re-marshal request for HMAC verification")
		}

		// Compute expected HMAC-SHA256.
		mac := hmac.New(sha256.New, secretBytes)
		_, _ = io.WriteString(mac, "")
		mac.Write(body)
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
