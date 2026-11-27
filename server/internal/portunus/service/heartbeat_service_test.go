package service_test

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

func boolPtr(b bool) *bool { return &b }

// A reader or strike fault is logged once when it appears and once when it
// clears, not on every heartbeat; a healthy or unreported flag logs nothing.
func TestHeartbeatService_LogsHealthTransitions(t *testing.T) {
	type report struct{ reader, strike *bool }
	tests := []struct {
		name    string
		reports []report
		want    []string // substrings, in order; nil means no log lines
	}{
		{
			name:    "healthy first report is quiet",
			reports: []report{{boolPtr(true), boolPtr(true)}, {boolPtr(true), boolPtr(true)}},
		},
		{
			name:    "unreported flags are quiet",
			reports: []report{{nil, nil}, {nil, nil}},
		},
		{
			name: "strike fault logged once, then recovery",
			reports: []report{
				{boolPtr(true), boolPtr(true)},
				{boolPtr(true), boolPtr(false)},
				{boolPtr(true), boolPtr(false)},
				{boolPtr(true), boolPtr(true)},
			},
			want: []string{"WARN module door-001: strike did not lock", "module door-001: strike locking again"},
		},
		{
			name:    "fault in the first report is logged",
			reports: []report{{boolPtr(false), nil}},
			want:    []string{"WARN module door-001: card reader degraded"},
		},
		{
			name: "reader recovery",
			reports: []report{
				{boolPtr(false), boolPtr(true)},
				{boolPtr(true), boolPtr(true)},
			},
			want: []string{"WARN module door-001: card reader degraded", "module door-001: card reader recovered"},
		},
		{
			name: "a flag that stops being reported is forgotten",
			reports: []report{
				{nil, boolPtr(false)},
				{nil, nil},
				{nil, boolPtr(false)},
			},
			want: []string{"strike did not lock", "strike did not lock"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			svc := service.NewHeartbeatService(memory.New(),
				service.NewDeviceRegistry(memory.NewDeviceStore([]string{"door-001"})))
			svc.SetLogger(log.New(&buf, "", 0))

			for i, r := range tc.reports {
				_, err := svc.Record(context.Background(), types.HeartbeatRequest{
					ModuleID: "door-001", Sequence: uint32(i + 1),
					ReaderOK: r.reader, StrikeOK: r.strike,
				})
				if err != nil {
					t.Fatalf("Record #%d: %v", i, err)
				}
			}

			var lines []string
			for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
				if l != "" {
					lines = append(lines, l)
				}
			}
			if len(lines) != len(tc.want) {
				t.Fatalf("got %d log lines %q, want %d", len(lines), lines, len(tc.want))
			}
			for i, w := range tc.want {
				if !strings.Contains(lines[i], w) {
					t.Errorf("line %d = %q, want it to contain %q", i, lines[i], w)
				}
			}
		})
	}
}

// Modules are tracked separately: one module's fault does not suppress or
// clear another's.
func TestHeartbeatService_HealthIsPerModule(t *testing.T) {
	var buf bytes.Buffer
	svc := service.NewHeartbeatService(memory.New(),
		service.NewDeviceRegistry(memory.NewDeviceStore([]string{"door-001", "door-002"})))
	svc.SetLogger(log.New(&buf, "", 0))

	for _, id := range []string{"door-001", "door-002"} {
		if _, err := svc.Record(context.Background(), types.HeartbeatRequest{
			ModuleID: id, StrikeOK: boolPtr(false),
		}); err != nil {
			t.Fatalf("Record %s: %v", id, err)
		}
	}
	out := buf.String()
	for _, id := range []string{"door-001", "door-002"} {
		if !strings.Contains(out, "WARN module "+id+": strike did not lock") {
			t.Errorf("no strike fault logged for %s in %q", id, out)
		}
	}
}
