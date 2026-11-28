package replay

import (
	"errors"
	"testing"
	"time"
)

func newTestStore(window time.Duration) (*Store, *time.Time) {
	clock := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s := NewStore(window)
	s.now = func() time.Time { return clock }
	return s, &clock
}

func ts(t time.Time) string { return t.Format(time.RFC3339Nano) }

func TestCheck_Timestamps(t *testing.T) {
	s, clock := newTestStore(time.Minute)
	cases := []struct {
		name string
		at   string
		want error
	}{
		{"missing", "", ErrTimestampRequired},
		{"not RFC 3339", "yesterday", ErrTimestampInvalid},
		{"unix seconds", "1767268800", ErrTimestampInvalid},
		{"too old", ts(clock.Add(-61 * time.Second)), ErrTimestampWindow},
		{"too far in the future", ts(clock.Add(61 * time.Second)), ErrTimestampWindow},
		{"edge of the past window", ts(clock.Add(-60 * time.Second)), nil},
		{"edge of the future window", ts(clock.Add(60 * time.Second)), nil},
		{"other time zone", clock.In(time.FixedZone("x", -5*3600)).Format(time.RFC3339), nil},
	}
	for i, tc := range cases {
		nonce := string(rune('a' + i))
		if err := s.Check("m1", nonce, tc.at); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestCheck_NonceSingleUsePerModule(t *testing.T) {
	s, clock := newTestStore(time.Minute)
	now := ts(*clock)
	if err := s.Check("m1", "abcd", now); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := s.Check("m1", "abcd", now); !errors.Is(err, ErrNonceSeen) {
		t.Fatalf("replay: got %v, want ErrNonceSeen", err)
	}
	// Nonces are namespaced by module: another device may draw the same bytes.
	if err := s.Check("m2", "abcd", now); err != nil {
		t.Fatalf("same nonce from another module: %v", err)
	}
	// A rejected request (bad timestamp) must not burn the nonce.
	if err := s.Check("m1", "fresh", "garbage"); !errors.Is(err, ErrTimestampInvalid) {
		t.Fatal(err)
	}
	if err := s.Check("m1", "fresh", now); err != nil {
		t.Fatalf("nonce burned by a rejected request: %v", err)
	}
}

// Entries are kept for one window after first use and then dropped, so the
// map stays bounded. A replay of the same signed request after that is still
// refused by the timestamp check.
func TestCheck_ExpiryAndBoundedMemory(t *testing.T) {
	s, clock := newTestStore(time.Minute)
	sentAt := ts(*clock)
	for i := 0; i < 100; i++ {
		if err := s.Check("m1", string(rune(1000+i)), sentAt); err != nil {
			t.Fatal(err)
		}
	}
	*clock = clock.Add(2 * time.Minute)
	if err := s.Check("m1", string(rune(1000)), sentAt); !errors.Is(err, ErrTimestampWindow) {
		t.Fatalf("old request replayed after expiry: got %v, want ErrTimestampWindow", err)
	}
	if err := s.Check("m1", "new", ts(*clock)); err != nil {
		t.Fatal(err)
	}
	if len(s.entries) != 1 {
		t.Fatalf("%d entries after the window passed, want 1", len(s.entries))
	}
}

func TestCheck_EmptyNonceNotRecorded(t *testing.T) {
	s, clock := newTestStore(time.Minute)
	for i := 0; i < 2; i++ {
		if err := s.Check("m1", "", ts(*clock)); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if len(s.entries) != 0 {
		t.Fatalf("empty nonce recorded: %v", s.entries)
	}
}
