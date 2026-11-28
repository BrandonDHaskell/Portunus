package service_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
)

// FuzzParseCredentialUID checks the device-facing UID parser: it must never
// panic, accepted UIDs stay within the 1 to 10 byte bound, and re-formatting
// an accepted UID the way the firmware's credential_uid_to_hex does
// ("%02X" bytes joined by ':') parses back to the same bytes and so hashes
// identically (hazard #1: enrollment and lookup must agree byte for byte).
func FuzzParseCredentialUID(f *testing.F) {
	for _, s := range []string{
		"04:A3:2B:1C", "04:a3:2b:1c", " 04:A3 ", "00:11:22:33:44:55:66:77:88:99",
		"00:11:22:33:44:55:66:77:88:99:AA", "", ":", "0", "04:", "04::A3", "zz", "04|A3", "\x00",
	} {
		f.Add(s)
	}
	secret := []byte("fuzz-credential-hash-secret")
	f.Fuzz(func(t *testing.T, s string) {
		uid, err := service.ParseCredentialUID(s)
		if err != nil {
			if uid != nil {
				t.Fatalf("ParseCredentialUID(%q) returned bytes %x with error %v", s, uid, err)
			}
			return
		}
		if len(uid) < 1 || len(uid) > 10 {
			t.Fatalf("ParseCredentialUID(%q) accepted %d bytes", s, len(uid))
		}
		parts := make([]string, len(uid))
		for i, b := range uid {
			parts[i] = fmt.Sprintf("%02X", b)
		}
		canonical := strings.Join(parts, ":")
		again, err := service.ParseCredentialUID(canonical)
		if err != nil || !bytes.Equal(again, uid) {
			t.Fatalf("canonical form %q of %q re-parsed to %x, %v; want %x", canonical, s, again, err, uid)
		}
		if !bytes.Equal(service.HashCredentialID(uid, secret), service.HashCredentialID(again, secret)) {
			t.Fatalf("hash of %q differs from hash of its canonical form %q", s, canonical)
		}
	})
}
