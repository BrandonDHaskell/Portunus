package grpcapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
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
//
// Projection formats (must match grpc_post_proto() in services/server_comm):
//
//	Heartbeat:  "heartbeat|{module_id}|{sequence}"
//	Access:     "access|{module_id}|{credential_id}|{nonce_hex}|{requested_at}"
//	Provision:  "provision|{module_id}|{hex(credential_uid)}"
//
// The access projection includes the nonce (hex-encoded) and requested_at so
// that every request has a unique signed payload — a captured access request
// cannot be replayed because the nonce is single-use.  requested_at may be
// empty when the device clock is not yet synced; the server skips the timestamp
// window check in that case but still enforces nonce uniqueness.
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

// accessResponseProjection returns the canonical string the server signs for an
// AccessResponse and the device verifies before publishing EVENT_ACCESS_GRANTED.
// Format: "access|{module_id}|{credential_id}|{1 or 0}"
// Must match the snprintf in handle_credential() in server_comm.cpp.
func accessResponseProjection(moduleID, credentialID string, granted bool) []byte {
	v := 0
	if granted {
		v = 1
	}
	return []byte(fmt.Sprintf("access|%s|%s|%d", moduleID, credentialID, v))
}

// AccessResponseSig computes the HMAC-SHA256 signature the server attaches to
// an AccessResponse.  Returns an empty string when secret is empty (HMAC
// disabled), so callers can gate on a non-empty result.
func AccessResponseSig(secret, moduleID, credentialID string, granted bool) string {
	if secret == "" {
		return ""
	}
	projection := accessResponseProjection(moduleID, credentialID, granted)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(projection)
	return hex.EncodeToString(mac.Sum(nil))
}

// ── Replay protection ─────────────────────────────────────────────────────────

// ReplayStore tracks recently-seen access-request nonces to detect replayed
// messages.  It pairs with the timestamp window: a request is rejected if its
// nonce was seen within the window, or if its timestamp is older than the window.
//
// The store is safe for concurrent use.  Expired entries are purged lazily on
// each Check call so memory stays bounded to (window / fastest scan rate) entries.
type ReplayStore struct {
	mu      sync.Mutex
	window  time.Duration
	entries map[string]time.Time // key: moduleID+":"+nonceHex, value: expiry
}

// NewReplayStore creates a ReplayStore with the given sliding window duration.
// A window of 60 seconds is appropriate for most door deployments: it exceeds
// any reasonable network round-trip while being short enough that the nonce map
// stays tiny even under sustained load.
func NewReplayStore(window time.Duration) *ReplayStore {
	return &ReplayStore{
		window:  window,
		entries: make(map[string]time.Time),
	}
}

// Check validates that a nonce has not been seen before within the window, and
// optionally that the timestamp is fresh.  On success it records the nonce so
// subsequent calls with the same nonce are rejected.
//
// moduleID namespaces nonces so different devices cannot collide.
// nonceHex is the hex-encoded nonce from the request (may be empty for old firmware).
// requestedAt is the RFC 3339 device timestamp (empty when clock not synced).
//
// Returns a non-nil error (with a gRPC status code embedded) on rejection.
func (r *ReplayStore) Check(moduleID, nonceHex, requestedAt string) error {
	now := time.Now().UTC()

	r.mu.Lock()
	defer r.mu.Unlock()

	// Purge expired entries to keep the map bounded.
	for k, expiry := range r.entries {
		if now.After(expiry) {
			delete(r.entries, k)
		}
	}

	// Timestamp window check — only when the device provides a timestamp.
	if requestedAt != "" {
		ts, err := time.Parse(time.RFC3339Nano, requestedAt)
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "replay: unparseable requested_at")
		}
		age := now.Sub(ts.UTC())
		if age > r.window || age < -r.window {
			return status.Errorf(codes.Unauthenticated, "replay: request timestamp out of window")
		}
	}

	// Nonce uniqueness check — skip only when nonce is completely absent
	// (e.g. old firmware that predates this field).
	if nonceHex != "" {
		key := moduleID + ":" + nonceHex
		if _, seen := r.entries[key]; seen {
			return status.Errorf(codes.Unauthenticated, "replay: nonce already seen")
		}
		r.entries[key] = now.Add(r.window)
	}

	return nil
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
// store may be nil to disable replay protection (not recommended for production).
func HMACInterceptor(secret string, store *ReplayStore) grpc.UnaryServerInterceptor {
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

		// Replay protection — only for access requests, only when a store is wired.
		if store != nil {
			if ar, ok := req.(*pb.AccessRequest); ok {
				if err := store.Check(ar.ModuleId, hex.EncodeToString(ar.Nonce), ar.RequestedAt); err != nil {
					return nil, err
				}
			}
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
