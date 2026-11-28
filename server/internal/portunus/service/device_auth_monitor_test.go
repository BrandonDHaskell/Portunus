package service_test

import (
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
)

func TestDeviceAuthMonitor_CountsPerModule(t *testing.T) {
	logs := &syncBuffer{}
	m := service.NewDeviceAuthMonitor(log.New(logs, "", 0))

	m.RecordFailure("door-1", "10.0.0.7:4000", "grpc", service.ReasonBadSignature)
	m.RecordFailure("door-1", "10.0.0.7:4001", "http", service.ReasonBadSignature)
	m.RecordFailure("door-2", "10.0.0.8:4000", "grpc", service.ReasonMissingSignature)

	f, ok := m.ForModule("door-1")
	if !ok || f.Count != 2 || f.LastPeer != "10.0.0.7:4001" || f.LastTransport != "http" || f.LastReason != service.ReasonBadSignature {
		t.Fatalf("door-1 = %+v, %v", f, ok)
	}
	if f.FirstAt.IsZero() || f.LastAt.Before(f.FirstAt) {
		t.Errorf("timestamps first=%v last=%v", f.FirstAt, f.LastAt)
	}
	if _, ok := m.ForModule("door-3"); ok {
		t.Error("module with no failures reported")
	}

	out := logs.String()
	for _, want := range []string{`module="door-1"`, "peer=10.0.0.7:4000", "transport=grpc", "reason=bad_signature", `module="door-2"`, "reason=missing_signature"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
}

// Module ids come from unauthenticated requests, so they are bounded in
// length and count: a flood of forged ids cannot grow memory or the logs.
func TestDeviceAuthMonitor_BoundedAgainstForgedIDs(t *testing.T) {
	m := service.NewDeviceAuthMonitor(log.New(&syncBuffer{}, "", 0))
	for i := 0; i < 1000; i++ {
		m.RecordFailure(fmt.Sprintf("forged-%d", i), "1.2.3.4:1", "http", service.ReasonBadSignature)
	}
	if n := len(m.Snapshot()); n != service.MaxTrackedDevices {
		t.Fatalf("tracking %d modules, want the cap of %d", n, service.MaxTrackedDevices)
	}
	if _, ok := m.ForModule("forged-999"); !ok {
		t.Error("newest failure was evicted instead of the oldest")
	}
	if _, ok := m.ForModule("forged-0"); ok {
		t.Error("oldest failure was not evicted")
	}

	m.RecordFailure("", "1.2.3.4:1", "http", service.ReasonBadSignature)
	if _, ok := m.ForModule("unknown"); !ok {
		t.Error(`empty module id not recorded as "unknown"`)
	}
	long := strings.Repeat("x", 500)
	m.RecordFailure(long, "1.2.3.4:1", "http", service.ReasonBadSignature)
	for _, f := range m.Snapshot() {
		if len(f.ModuleID) > 64 {
			t.Fatalf("module id of %d bytes stored", len(f.ModuleID))
		}
	}
}

func TestDeviceAuthMonitor_SnapshotNewestFirst(t *testing.T) {
	m := service.NewDeviceAuthMonitor(log.New(&syncBuffer{}, "", 0))
	m.RecordFailure("a", "p", "grpc", service.ReasonBadSignature)
	time.Sleep(2 * time.Millisecond)
	m.RecordFailure("b", "p", "grpc", service.ReasonBadSignature)
	s := m.Snapshot()
	if len(s) != 2 || s[0].ModuleID != "b" || s[1].ModuleID != "a" {
		t.Fatalf("snapshot order = %+v", s)
	}
}

func TestDeviceAuthMonitor_NilIsSafe(t *testing.T) {
	var m *service.DeviceAuthMonitor
	m.RecordFailure("door-1", "p", "grpc", service.ReasonBadSignature)
	if s := m.Snapshot(); s != nil {
		t.Fatalf("nil monitor snapshot = %v", s)
	}
	if _, ok := m.ForModule("door-1"); ok {
		t.Fatal("nil monitor reported a failure")
	}
}
