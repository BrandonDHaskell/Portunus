package httpapi

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter(threshold int) (*loginLimiter, *fakeClock) {
	c := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	l := newLoginLimiter(time.Minute, threshold)
	l.now = c.now
	return l, c
}

func TestLoginLimiter_LocksAtThreshold(t *testing.T) {
	l, _ := newTestLimiter(3)
	const user, addr = "alice", "10.0.0.5:4242"
	for i := 0; i < 3; i++ {
		if !l.Allow(user, addr) {
			t.Fatalf("locked after %d failures, threshold is 3", i)
		}
		l.RecordFailure(user, addr)
	}
	if l.Allow(user, addr) {
		t.Fatal("not locked after 3 failures")
	}
}

func TestLoginLimiter_WindowExpiry(t *testing.T) {
	l, clock := newTestLimiter(2)
	const user, addr = "alice", "10.0.0.5:4242"
	l.RecordFailure(user, addr)
	clock.advance(40 * time.Second)
	l.RecordFailure(user, addr)
	if l.Allow(user, addr) {
		t.Fatal("not locked with 2 failures inside the window")
	}
	clock.advance(21 * time.Second) // first failure is now older than the window
	if !l.Allow(user, addr) {
		t.Fatal("still locked after the oldest failure aged out")
	}
	clock.advance(time.Minute)
	if !l.Allow(user, addr) || len(l.entries) != 0 {
		t.Fatalf("expired entries not purged: %v", l.entries)
	}
}

func TestLoginLimiter_ResetClears(t *testing.T) {
	l, _ := newTestLimiter(1)
	l.RecordFailure("alice", "10.0.0.5:1")
	if l.Allow("alice", "10.0.0.5:1") {
		t.Fatal("not locked")
	}
	l.Reset("alice", "10.0.0.5:2") // different port, same host: same key
	if !l.Allow("alice", "10.0.0.5:1") {
		t.Fatal("Reset did not clear the user+host entry")
	}
}

func TestLoginLimiter_KeyedByUserAndHost(t *testing.T) {
	l, _ := newTestLimiter(1)
	l.RecordFailure("alice", "10.0.0.5:1111")

	if l.Allow("alice", "10.0.0.5:2222") {
		t.Error("a new source port bypassed the lock")
	}
	if !l.Allow("bob", "10.0.0.5:1111") {
		t.Error("another user from the same host was locked out")
	}
	if !l.Allow("alice", "10.0.0.6:1111") {
		t.Error("the same user from another host was locked out")
	}
}

func TestLoginKey(t *testing.T) {
	cases := map[string]string{
		"10.0.0.5:4242":       "u::10.0.0.5",
		"[2001:db8::1]:4242":  "u::2001:db8::1",
		"no-port-address":     "u::no-port-address",
		"[2001:db8::1]:65535": "u::2001:db8::1",
	}
	for addr, want := range cases {
		if got := loginKey("u", addr); got != want {
			t.Errorf("loginKey(%q) = %q, want %q", addr, got, want)
		}
	}
}
