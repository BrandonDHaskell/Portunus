package db

import "testing"

func TestParseVersion(t *testing.T) {
	good := map[string]int{"0001_init.sql": 1, "0029_heartbeat_health.sql": 29, "0000_zero.sql": 0, "0100_x.sql": 100}
	for name, want := range good {
		if got, err := parseVersion(name); err != nil || got != want {
			t.Errorf("parseVersion(%q) = %d, %v; want %d", name, got, err, want)
		}
	}
	// A name with no number at all ("_x.sql") parses as version 0; the
	// NNNN_name.sql filename check in migrate_test.go rejects such files.
	for _, bad := range []string{"init.sql", "00a1_x.sql"} {
		if _, err := parseVersion(bad); err == nil {
			t.Errorf("parseVersion(%q) accepted", bad)
		}
	}
}
