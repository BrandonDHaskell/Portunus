package httpapi

import (
	"net"
	"sync"
	"time"
)

// loginLimiter enforces a sliding-window failed-attempt limit on login routes.
//
// It is keyed on the client host only, never on the submitted username: one
// source is locked out after threshold failures no matter how many usernames
// or passwords it cycles through, and the lockout response is identical for
// real and made-up usernames, so it reveals nothing about which accounts
// exist. The key is the TCP peer address (r.RemoteAddr); X-Forwarded-For is
// deliberately not trusted because a client can set it to dodge the limit.
//
// Expired entries for every host are purged whenever a failure is recorded,
// so memory stays bounded by the number of hosts that failed recently.
//
// Safe for concurrent use.
type loginLimiter struct {
	mu        sync.Mutex
	window    time.Duration
	threshold int
	entries   map[string][]time.Time // host → recent failure timestamps, oldest first
	now       func() time.Time       // injectable so tests can move time
}

func newLoginLimiter(window time.Duration, threshold int) *loginLimiter {
	return &loginLimiter{
		window:    window,
		threshold: threshold,
		entries:   make(map[string][]time.Time),
		now:       time.Now,
	}
}

// loginKey returns the rate-limit key for a remote address: its host, with
// the port stripped so a client cannot reset its count by reconnecting.
func loginKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr // no port: use the raw string
	}
	return host
}

// Allow reports whether the source is under the failure threshold. It does
// not record an attempt; call RecordFailure after a failed authentication
// and Reset after a success.
func (l *loginLimiter) Allow(remoteAddr string) bool {
	now := l.now().UTC()
	key := loginKey(remoteAddr)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.purge(key, now)
	return len(l.entries[key]) < l.threshold
}

// RecordFailure records a failed attempt from the source and drops expired
// entries for all sources.
func (l *loginLimiter) RecordFailure(remoteAddr string) {
	now := l.now().UTC()
	key := loginKey(remoteAddr)

	l.mu.Lock()
	defer l.mu.Unlock()

	for k := range l.entries {
		l.purge(k, now)
	}
	l.entries[key] = append(l.entries[key], now)
}

// Reset clears the source's failure record after a successful login.
func (l *loginLimiter) Reset(remoteAddr string) {
	key := loginKey(remoteAddr)

	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.entries, key)
}

// purge removes timestamps older than the window from one entry.
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
