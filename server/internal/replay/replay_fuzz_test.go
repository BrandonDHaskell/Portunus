package replay

import (
	"errors"
	"testing"
	"time"
)

// FuzzCheck feeds arbitrary module IDs, nonces and timestamps to the replay
// store. It must never panic or return an undeclared error, and a fresh
// in-window request must be accepted once and then refused as a replay.
func FuzzCheck(f *testing.F) {
	f.Add("door-001", "00112233445566778899aabbccddeeff", "2026-03-01T12:00:00Z", int64(0))
	f.Add("", "", "", int64(-60))
	f.Add("door:1", "zz", "9999-12-31T23:59:59.999999999Z", int64(60))
	f.Add("door-001", "00", "0001-01-01T00:00:00Z", int64(-1<<40))
	f.Fuzz(func(t *testing.T, moduleID, nonceHex, rawAt string, offsetSec int64) {
		s, clock := newTestStore(time.Minute)

		switch err := s.Check(moduleID, nonceHex, rawAt); {
		case err == nil, errors.Is(err, ErrTimestampRequired), errors.Is(err, ErrTimestampInvalid),
			errors.Is(err, ErrTimestampWindow), errors.Is(err, ErrNonceSeen):
		default:
			t.Fatalf("Check(%q, %q, %q) returned undeclared error %v", moduleID, nonceHex, rawAt, err)
		}

		// A request inside the window, on a store that has not seen it.
		s, clock = newTestStore(time.Minute)
		offset := time.Duration(offsetSec%61) * time.Second
		at := clock.Add(offset).Format(time.RFC3339Nano)
		if err := s.Check(moduleID, nonceHex, at); err != nil {
			t.Fatalf("fresh in-window request (offset %v) refused: %v", offset, err)
		}
		err := s.Check(moduleID, nonceHex, at)
		if nonceHex != "" && !errors.Is(err, ErrNonceSeen) {
			t.Fatalf("replayed nonce %q accepted: %v", nonceHex, err)
		}
		if nonceHex == "" && err != nil {
			t.Fatalf("second nonce-less request refused: %v", err)
		}
	})
}
