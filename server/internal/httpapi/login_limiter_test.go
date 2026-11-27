package httpapi

import (
	"fmt"
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
	const addr = "10.0.0.5:4242"
	for i := 0; i < 3; i++ {
		if !l.Allow(addr) {
			t.Fatalf("locked after %d failures, threshold is 3", i)
		}
		l.RecordFailure(addr)
	}
	if l.Allow(addr) {
		t.Fatal("not locked after 3 failures")
	}
}

func TestLoginLimiter_WindowExpiry(t *testing.T) {
	l, clock := newTestLimiter(2)
	const addr = "10.0.0.5:4242"
	l.RecordFailure(addr)
	clock.advance(40 * time.Second)
	l.RecordFailure(addr)
	if l.Allow(addr) {
		t.Fatal("not locked with 2 failures inside the window")
	}
	clock.advance(21 * time.Second) // the first failure is now older than the window
	if !l.Allow(addr) {
		t.Fatal("still locked after the oldest failure aged out")
	}
}

func TestLoginLimiter_ResetClears(t *testing.T) {
	l, _ := newTestLimiter(1)
	l.RecordFailure("10.0.0.5:1")
	if l.Allow("10.0.0.5:1") {
		t.Fatal("not locked")
	}
	l.Reset("10.0.0.5:2") // different port, same host: same key
	if !l.Allow("10.0.0.5:1") {
		t.Fatal("Reset did not clear the host entry")
	}
}

// One source is one key, whatever port it reconnects from; other sources are
// unaffected.
func TestLoginLimiter_KeyedByHostOnly(t *testing.T) {
	l, _ := newTestLimiter(1)
	l.RecordFailure("10.0.0.5:1111")
	if l.Allow("10.0.0.5:2222") {
		t.Error("a new source port bypassed the lock")
	}
	if !l.Allow("10.0.0.6:1111") {
		t.Error("a different host was locked out")
	}
}

// Failures from many hosts must not accumulate once they age out.
func TestLoginLimiter_MemoryBounded(t *testing.T) {
	l, clock := newTestLimiter(5)
	for i := 0; i < 500; i++ {
		l.RecordFailure(fmt.Sprintf("10.0.%d.%d:1", i/250, i%250))
	}
	clock.advance(2 * time.Minute)
	l.RecordFailure("10.9.9.9:1")
	if len(l.entries) != 1 {
		t.Fatalf("%d entries after expiry, want 1", len(l.entries))
	}
}

func TestLoginKey(t *testing.T) {
	cases := map[string]string{
		"10.0.0.5:4242":      "10.0.0.5",
		"[2001:db8::1]:4242": "2001:db8::1",
		"no-port-address":    "no-port-address",
	}
	for addr, want := range cases {
		if got := loginKey(addr); got != want {
			t.Errorf("loginKey(%q) = %q, want %q", addr, got, want)
		}
	}
}
