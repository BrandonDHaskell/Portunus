// Package replay provides a nonce+timestamp replay-protection store shared by
// the HTTP and gRPC device transports.  Both transports must share one instance
// so that a nonce replayed across transports is also rejected.
package replay

import (
	"errors"
	"sync"
	"time"
)

// Sentinel errors returned by Store.Check.  Callers translate these into their
// own transport-layer error codes (HTTP 401, gRPC Unauthenticated, etc.).
var (
	ErrNonceSeen         = errors.New("replay: nonce already seen")
	ErrTimestampWindow   = errors.New("replay: request timestamp out of window")
	ErrTimestampRequired = errors.New("replay: requested_at is required")
	ErrTimestampInvalid  = errors.New("replay: requested_at is not a valid RFC 3339 timestamp")
)

// Store tracks recently-seen access-request nonces to reject replayed messages.
// It pairs nonce uniqueness with a timestamp window: a request is rejected if
// its nonce was seen within the window OR if its timestamp is outside the window.
//
// A missing timestamp is always rejected — when HMAC is enabled, the firmware
// must provide a synchronised clock timestamp so the window check is binding.
// (Old firmware that predates the timestamp field must be updated.)
//
// Store is safe for concurrent use.  Expired entries are purged lazily on each
// Check call so memory stays bounded to roughly (window / fastest scan rate).
type Store struct {
	mu      sync.Mutex
	window  time.Duration
	entries map[string]time.Time // key: moduleID+":"+nonceHex  value: expiry
}

// NewStore creates a Store with the given sliding-window duration.
// A window of 60 seconds suits most door deployments.
func NewStore(window time.Duration) *Store {
	return &Store{
		window:  window,
		entries: make(map[string]time.Time),
	}
}

// Check validates that:
//  1. requestedAt is present and parses as RFC 3339.
//  2. The timestamp is within ±window of now.
//  3. nonceHex has not been seen before within the window.
//
// On success it records the nonce; subsequent calls with the same
// moduleID+nonceHex are rejected until the entry expires.
//
// moduleID namespaces nonces so different devices cannot collide.
func (s *Store) Check(moduleID, nonceHex, requestedAt string) error {
	now := time.Now().UTC()

	if requestedAt == "" {
		return ErrTimestampRequired
	}

	ts, err := time.Parse(time.RFC3339Nano, requestedAt)
	if err != nil {
		return ErrTimestampInvalid
	}
	age := now.Sub(ts.UTC())
	if age > s.window || age < -s.window {
		return ErrTimestampWindow
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Purge expired entries to keep the map bounded.
	for k, expiry := range s.entries {
		if now.After(expiry) {
			delete(s.entries, k)
		}
	}

	if nonceHex != "" {
		key := moduleID + ":" + nonceHex
		if _, seen := s.entries[key]; seen {
			return ErrNonceSeen
		}
		s.entries[key] = now.Add(s.window)
	}

	return nil
}
