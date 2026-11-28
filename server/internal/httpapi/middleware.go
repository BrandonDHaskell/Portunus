package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
)

func loggingMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now().UTC()
		next.ServeHTTP(w, r)
		logger.Printf("%s %s from=%s dur=%s", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}

// hmacAuthMiddleware verifies the X-Portunus-Sig header on every POST request.
//
// The ESP32 firmware computes HMAC-SHA256(secret, request_body) and sends the
// hex-encoded result as the X-Portunus-Sig header.  This middleware:
//  1. Reads the full request body (re-instating it for downstream handlers).
//  2. Computes the expected HMAC.
//  3. Does a constant-time comparison to prevent timing attacks.
//  4. Rejects requests with missing or invalid signatures with HTTP 401.
//
// Only POST requests to device endpoints are checked; other methods and
// admin paths pass through unchanged.
func hmacAuthMiddleware(logger *log.Logger, secret string, monitor *service.DeviceAuthMonitor, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only enforce on POST (the only method ESP32 modules use).
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}

		// Admin endpoints are protected by session cookie auth, not HMAC.
		// They originate from curl/browser, not from ESP32 firmware.
		if strings.HasPrefix(r.URL.Path, "/admin/") {
			next.ServeHTTP(w, r)
			return
		}

		// Read the body so we can compute the HMAC (and label a failure with
		// the module id it claims), then replace it so downstream handlers can
		// read it normally.
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
		if err != nil {
			writeError(w, http.StatusBadRequest, "read_error", "could not read request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		sig := r.Header.Get("X-Portunus-Sig")
		if sig == "" {
			logger.Printf("HMAC: missing X-Portunus-Sig from %s %s", r.RemoteAddr, r.URL.Path)
			monitor.RecordFailure(claimedModuleID(r, body), r.RemoteAddr, "http", service.ReasonMissingSignature)
			writeError(w, http.StatusUnauthorized, "missing_signature",
				"X-Portunus-Sig header is required")
			return
		}

		// Compute expected HMAC-SHA256.
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))

		// Constant-time compare to prevent timing side-channels.
		if !hmac.Equal([]byte(sig), []byte(expected)) {
			logger.Printf("HMAC: invalid signature from %s %s (got %.8s…)", r.RemoteAddr, r.URL.Path, sig)
			monitor.RecordFailure(claimedModuleID(r, body), r.RemoteAddr, "http", service.ReasonBadSignature)
			writeError(w, http.StatusUnauthorized, "invalid_signature",
				"X-Portunus-Sig does not match")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// claimedModuleID extracts the module id a device request names, from either
// a JSON or a protobuf body. It is unauthenticated and only labels a
// signature failure; anything unparseable yields "".
func claimedModuleID(r *http.Request, body []byte) string {
	if isProtobuf(r) {
		var m interface {
			proto.Message
			GetModuleId() string
		}
		switch r.URL.Path {
		case "/v1/heartbeat":
			m = &pb.HeartbeatRequest{}
		case "/v1/access_request":
			m = &pb.AccessRequest{}
		case "/v1/provision_credential":
			m = &pb.ProvisionCredentialRequest{}
		default:
			return ""
		}
		if proto.Unmarshal(body, m) != nil {
			return ""
		}
		return m.GetModuleId()
	}
	var j struct {
		ModuleID string `json:"module_id"`
	}
	if json.Unmarshal(body, &j) != nil {
		return ""
	}
	return j.ModuleID
}

// requireEitherPermission wraps a handler and enforces that the session holds
// at least one of perm1 or perm2. Used for routes where _held and _any
// variants are both acceptable; the service layer performs the scope check.
func requireEitherPermission(perm1, perm2 string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFromContext(r.Context())
		if sess == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "valid session required")
			return
		}
		if sess.MustChangePW {
			writeError(w, http.StatusForbidden, "password_change_required",
				"you must change your password before performing other actions")
			return
		}
		if !sess.HasPermission(perm1) && !sess.HasPermission(perm2) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient permissions")
			return
		}
		next(w, r)
	}
}
