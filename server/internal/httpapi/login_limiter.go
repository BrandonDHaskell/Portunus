package httpapi

import (
	"net"
	"sync"
	"time"
)

// loginLimiter enforces a sliding-window failed-attempt limit on login routes.
// It is keyed on "username::clientIP" so each (user, source) pair has its own
// independent counter.  The store is bounded: entries older than the window are
// purged lazily on each call.
//
// Safe for concurrent use.
type loginLimiter struct {
	mu        sync.Mutex
	window    time.Duration
	threshold int
	entries   map[string][]time.Time // key → slice of recent failure timestamps
}

func newLoginLimiter(window time.Duration, threshold int) *loginLimiter {
	return &loginLimiter{
		window:    window,
		threshold: threshold,
		entries:   make(map[string][]time.Time),
	}
}

// key returns the rate-limit key for a username + remote address pair.
// It strips the port from remoteAddr so IPv4 and IPv6 both work.
func loginKey(username, remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr // fall back to raw string if no port
	}
	return username + "::" + host
}

// Allow returns true if the caller is under the failure threshold.
// It does NOT record a new attempt; call RecordFailure after a confirmed
// authentication failure and Reset after a success.
func (l *loginLimiter) Allow(username, remoteAddr string) bool {
	now := time.Now().UTC()
	key := loginKey(username, remoteAddr)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.purge(key, now)
	return len(l.entries[key]) < l.threshold
}

// RecordFailure records a failed attempt for the given username + IP.
func (l *loginLimiter) RecordFailure(username, remoteAddr string) {
	now := time.Now().UTC()
	key := loginKey(username, remoteAddr)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.purge(key, now)
	l.entries[key] = append(l.entries[key], now)
}

// Reset clears the failure record for the given username + IP after a
// successful login.
func (l *loginLimiter) Reset(username, remoteAddr string) {
	key := loginKey(username, remoteAddr)

	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.entries, key)
}

// purge removes timestamps older than the window from a single entry.
// Must be called with l.mu held.
func (l *loginLimiter) purge(key string, now time.Time) {
	times := l.entries[key]
	cutoff := now.Add(-l.window)
	i := 0
	for i < len(times) && times[i].Before(cutoff) {
		i++
	}
	if i > 0 {
		l.entries[key] = times[i:]
	}
	if len(l.entries[key]) == 0 {
		delete(l.entries, key)
	}
}
