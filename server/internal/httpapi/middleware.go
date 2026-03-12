package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"time"
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
// Only POST requests are checked; other methods pass through unchanged.
func hmacAuthMiddleware(logger *log.Logger, secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only enforce on POST (the only method ESP32 modules use).
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}

		sig := r.Header.Get("X-Portunus-Sig")
		if sig == "" {
			logger.Printf("HMAC: missing X-Portunus-Sig from %s %s", r.RemoteAddr, r.URL.Path)
			writeError(w, http.StatusUnauthorized, "missing_signature",
				"X-Portunus-Sig header is required")
			return
		}

		// Read the body so we can compute the HMAC, then replace it so
		// downstream handlers can read it normally.
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
		if err != nil {
			writeError(w, http.StatusBadRequest, "read_error", "could not read request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		// Compute expected HMAC-SHA256.
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))

		// Constant-time compare to prevent timing side-channels.
		if !hmac.Equal([]byte(sig), []byte(expected)) {
			logger.Printf("HMAC: invalid signature from %s %s (got %.8s…)", r.RemoteAddr, r.URL.Path, sig)
			writeError(w, http.StatusUnauthorized, "invalid_signature",
				"X-Portunus-Sig does not match")
			return
		}

		next.ServeHTTP(w, r)
	})
}
