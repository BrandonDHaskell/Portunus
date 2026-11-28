package grpcapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/replay"
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
//
// Projection formats (must match proto_*_projection() in the firmware's
// services/server_comm/src/access_protocol.cpp; both sides test
// proto/testdata/hmac_vectors.txt):
//
//	Heartbeat:  "heartbeat|{module_id}|{sequence}"
//	Access:     "access|{module_id}|{credential_id}|{nonce_hex}|{requested_at}"
//	Provision:  "provision|{module_id}|{hex(credential_uid)}"
//
// The access projection includes the nonce (hex-encoded) and requested_at so
// that every request has a unique signed payload — a captured access request
// cannot be replayed because the nonce is single-use.  requested_at must be
// present when HMAC is enabled; the server rejects requests with empty timestamps.
func hmacProjection(req interface{}) ([]byte, error) {
	switch m := req.(type) {
	case *pb.HeartbeatRequest:
		return []byte(fmt.Sprintf("heartbeat|%s|%d", m.ModuleId, m.Sequence)), nil
	case *pb.AccessRequest:
		return []byte(fmt.Sprintf("access|%s|%s|%s|%s",
			m.ModuleId,
			m.CredentialId,
			hex.EncodeToString(m.Nonce),
			m.RequestedAt,
		)), nil
	case *pb.ProvisionCredentialRequest:
		// credential_uid (raw RFID bytes of the new member's card) is the
		// key field for the capture-only path. Matches the firmware projection.
		return []byte(fmt.Sprintf("provision|%s|%x", m.ModuleId, m.CredentialUid)), nil
	default:
		return nil, fmt.Errorf("unsupported request type %T", req)
	}
}

// HMACInterceptor returns a gRPC unary server interceptor that verifies
// the HMAC-SHA256 signature attached as custom metadata by the ESP32, then
// — for AccessRequests — checks the request against store to reject replays.
//
// The signature is computed over a canonical projection of key request fields
// (see hmacProjection), not the raw protobuf bytes. This avoids spurious
// mismatches caused by wire-format differences between Nanopb and the Go
// protobuf library. The HTTP path signs the raw body; the gRPC path signs
// the projection — both use the same pre-shared secret.
//
// When secret is empty, the interceptor is a no-op pass-through.
// replayStore may be nil to disable replay protection (not recommended for production).
func HMACInterceptor(secret string, replayStore *replay.Store) grpc.UnaryServerInterceptor {
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

		if ar, ok := req.(*pb.AccessRequest); ok {
			// A nonce is mandatory, as on the HTTP path: without one the
			// replay store has nothing to record and the signed request
			// could be replayed inside the timestamp window.
			if len(ar.Nonce) == 0 {
				return nil, status.Errorf(codes.Unauthenticated, "nonce is required for access requests")
			}
			// Replay protection, when a store is wired.
			if replayStore != nil {
				if err := replayStore.Check(ar.ModuleId, hex.EncodeToString(ar.Nonce), ar.RequestedAt); err != nil {
					return nil, replayErrToStatus(err)
				}
			}
		}

		return handler(ctx, req)
	}
}

// replayErrToStatus converts a replay sentinel error into a gRPC status error.
func replayErrToStatus(err error) error {
	switch {
	case errors.Is(err, replay.ErrNonceSeen):
		return status.Errorf(codes.Unauthenticated, "replay: nonce already seen")
	case errors.Is(err, replay.ErrTimestampWindow):
		return status.Errorf(codes.Unauthenticated, "replay: request timestamp out of window")
	case errors.Is(err, replay.ErrTimestampRequired):
		return status.Errorf(codes.Unauthenticated, "replay: requested_at is required")
	case errors.Is(err, replay.ErrTimestampInvalid):
		return status.Errorf(codes.InvalidArgument, "replay: invalid requested_at timestamp")
	default:
		return status.Errorf(codes.Unauthenticated, "replay: %v", err)
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
