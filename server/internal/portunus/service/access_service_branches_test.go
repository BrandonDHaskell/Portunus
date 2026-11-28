package service_test

// Decide branches not covered by the member-path tests: store failures,
// malformed input, remaining member statuses, and the availability-first audit
// contract. All run with allow-all OFF and a non-empty hash secret.

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

var errBoom = errors.New("boom")

type errDeviceStore struct{ store.DeviceStore }

func (errDeviceStore) IsKnown(context.Context, string) (bool, error) { return false, errBoom }

type errMemberStore struct{ store.MemberAccessStore }

func (errMemberStore) GetMemberByCredential(context.Context, []byte) (*store.MemberAccessRecord, error) {
	return nil, errBoom
}

type errAuthStore struct{ store.ModuleAuthorizationStore }

func (errAuthStore) GetAuthorization(context.Context, string, string) (*store.ModuleAuthorizationRecord, error) {
	return nil, errBoom
}

// switchableEventStore fails RecordEvent while fail is set.
type switchableEventStore struct {
	mu     sync.Mutex
	fail   bool
	events []store.AccessEventRecord
}

func (s *switchableEventStore) RecordEvent(_ context.Context, rec store.AccessEventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errBoom
	}
	s.events = append(s.events, rec)
	return nil
}

func (s *switchableEventStore) ListEventsByCredential(context.Context, []byte, int) ([]store.AccessEventRecord, error) {
	return nil, nil
}

func (s *switchableEventStore) setFail(v bool) {
	s.mu.Lock()
	s.fail = v
	s.mu.Unlock()
}

var branchSecret = []byte("branch-test-secret")

// branchFixture is a member-path AccessService over migrated SQLite with one
// active, enabled member enrolled as 04:AA:BB:CC and authorized on door-1.
type branchFixture struct {
	ma     store.MemberAccessStore
	mo     store.ModuleAuthorizationStore
	member string
}

func newBranchFixture(t *testing.T) branchFixture {
	t.Helper()
	ctx := context.Background()
	conn, w := openSvcTestDB(t)
	f := branchFixture{
		ma:     sqlitestore.NewMemberAccessStore(conn, w),
		mo:     sqlitestore.NewModuleAuthorizationStore(conn, w),
		member: "abababab-0000-4000-8000-000000000001",
	}
	seedModule(t, conn, "door-1")
	if err := f.ma.CreateMember(ctx, f.member, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	if err := f.ma.AttachCredential(ctx, f.member, service.HashCredentialID([]byte{0x04, 0xAA, 0xBB, 0xCC}, branchSecret)); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}
	if err := f.mo.GrantAuthorization(ctx, f.member, "door-1", "", nil, ""); err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}
	return f
}

func (f branchFixture) svc(devices store.DeviceStore, ma store.MemberAccessStore, mo store.ModuleAuthorizationStore, events store.AccessEventStore) *service.AccessService {
	if devices == nil {
		devices = memory.NewDeviceStore([]string{"door-1"})
	}
	if ma == nil {
		ma = f.ma
	}
	if mo == nil {
		mo = f.mo
	}
	s := service.NewAccessService(service.NewDeviceRegistry(devices), service.AccessPolicy{}, events)
	s.SetMemberAccessStore(ma)
	s.SetModuleAuthStore(mo)
	s.SetCredentialHashSecret(branchSecret)
	return s
}

var tapDoor1 = types.AccessRequest{ModuleID: "door-1", CredentialID: "04:AA:BB:CC"}

func TestDecide_InvalidCredentialFormat_DeniedAndRecorded(t *testing.T) {
	f := newBranchFixture(t)
	events := memory.NewAccessEventStore()
	s := f.svc(nil, nil, nil, events)

	for _, bad := range []string{"04-AA-BB-CC", "0x04AABBCC", "04::AA", "not a uid"} {
		resp, err := s.Decide(context.Background(), types.AccessRequest{ModuleID: "door-1", CredentialID: bad})
		if err != nil {
			t.Fatalf("%q: Decide error %v, want a recorded deny", bad, err)
		}
		if resp.Granted || resp.Reason != "invalid_credential_format" || !resp.Known {
			t.Errorf("%q: got granted=%v reason=%q known=%v", bad, resp.Granted, resp.Reason, resp.Known)
		}
	}
	got := events.Events()
	if len(got) != 4 {
		t.Fatalf("recorded %d events, want 4", len(got))
	}
	for _, e := range got {
		if e.Granted || e.Reason != "invalid_credential_format" || e.CredentialHash != nil {
			t.Errorf("event %+v: want a deny with no credential hash", e)
		}
	}
}

func TestDecide_StoreErrors_NeverGrant(t *testing.T) {
	f := newBranchFixture(t)
	cases := []struct {
		name        string
		devices     store.DeviceStore
		ma          store.MemberAccessStore
		mo          store.ModuleAuthorizationStore
		wantEventRs string
	}{
		{"device registry error", errDeviceStore{}, nil, nil, "module_lookup_error"},
		{"member lookup error", nil, errMemberStore{f.ma}, nil, "member_lookup_error"},
		{"authorization lookup error", nil, nil, errAuthStore{f.mo}, "member_lookup_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := memory.NewAccessEventStore()
			resp, err := f.svc(tc.devices, tc.ma, tc.mo, events).Decide(context.Background(), tapDoor1)
			if !errors.Is(err, errBoom) {
				t.Fatalf("Decide error = %v, want it to wrap the store error", err)
			}
			if resp.Granted {
				t.Fatal("granted despite a store error")
			}
			got := events.Events()
			if len(got) != 1 || got[0].Granted || got[0].Reason != tc.wantEventRs {
				t.Fatalf("events = %+v, want one %s deny", got, tc.wantEventRs)
			}
		})
	}
}

func TestDecide_MemberStatuses_Denied(t *testing.T) {
	for _, st := range []store.MemberStatus{store.MemberStatusSuspended, store.MemberStatusArchived, store.MemberStatusExpired} {
		t.Run(string(st), func(t *testing.T) {
			f := newBranchFixture(t)
			if err := f.ma.SetStatus(context.Background(), f.member, st); err != nil {
				t.Fatalf("SetStatus: %v", err)
			}
			resp, err := f.svc(nil, nil, nil, memory.NewAccessEventStore()).Decide(context.Background(), tapDoor1)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if resp.Granted || resp.Reason != "member_"+string(st) {
				t.Fatalf("got granted=%v reason=%q, want member_%s", resp.Granted, resp.Reason, st)
			}
		})
	}
}

func TestDecide_TrimsWhitespace(t *testing.T) {
	f := newBranchFixture(t)
	resp, err := f.svc(nil, nil, nil, memory.NewAccessEventStore()).Decide(context.Background(),
		types.AccessRequest{ModuleID: "  door-1 ", CredentialID: " 04:aa:bb:cc\t"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !resp.Granted || resp.ModuleID != "door-1" {
		t.Fatalf("got granted=%v module=%q reason=%q", resp.Granted, resp.ModuleID, resp.Reason)
	}
}

// A failed audit write must not change or block the decision, and must be
// counted so GET /admin/v1/health can report a degraded audit state.
func TestDecide_AuditFailure_DecisionStandsAndIsCounted(t *testing.T) {
	f := newBranchFixture(t)
	events := &switchableEventStore{fail: true}
	var logBuf bytes.Buffer
	s := f.svc(nil, nil, nil, events)
	s.SetLogger(log.New(&logBuf, "", 0))

	if h := s.AuditHealth(); h.ConsecutiveFailures != 0 || h.LastFailure != nil || h.LastSuccess != nil {
		t.Fatalf("fresh health = %+v, want zero", h)
	}

	for i := 0; i < 2; i++ {
		resp, err := s.Decide(context.Background(), tapDoor1)
		if err != nil || !resp.Granted {
			t.Fatalf("decision affected by audit failure: granted=%v err=%v", resp.Granted, err)
		}
	}
	h := s.AuditHealth()
	if h.ConsecutiveFailures != 2 || h.TotalFailures != 2 || h.LastFailure == nil || h.LastSuccess != nil {
		t.Fatalf("after 2 failures health = %+v", h)
	}
	if !strings.Contains(logBuf.String(), "consecutive=2 total=2") {
		t.Errorf("audit failure not logged: %q", logBuf.String())
	}

	events.setFail(false)
	before := time.Now().UTC()
	if _, err := s.Decide(context.Background(), tapDoor1); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	h = s.AuditHealth()
	if h.ConsecutiveFailures != 0 || h.TotalFailures != 2 || h.LastSuccess == nil || h.LastSuccess.Before(before) {
		t.Fatalf("after recovery health = %+v, want consecutive reset, total kept", h)
	}
}

func TestParseCredentialUID(t *testing.T) {
	cases := []struct {
		in   string
		want []byte // nil = expect an error
	}{
		{"04:A3:2B:1C", []byte{0x04, 0xA3, 0x2B, 0x1C}},
		{"04:a3:2b:1c", []byte{0x04, 0xA3, 0x2B, 0x1C}},
		{"  04:A3:2B:1C\n", []byte{0x04, 0xA3, 0x2B, 0x1C}},
		{"FF", []byte{0xFF}},
		{"01:02:03:04:05:06:07:08:09:0A", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"", nil},
		{"   ", nil},
		{"01:02:03:04:05:06:07:08:09:0A:0B", nil}, // 11 bytes
		{"04::A3", nil},
		{"04:A3:", nil},
		{"4:A3", nil},
		{"004:A3", nil},
		{"0x04", nil},
		{"04-A3-2B-1C", nil},
		{"04A32B1C", nil},
		{"GG:A3", nil},
	}
	for _, tc := range cases {
		got, err := service.ParseCredentialUID(tc.in)
		if tc.want == nil {
			if err == nil {
				t.Errorf("ParseCredentialUID(%q) = %X, want error", tc.in, got)
			}
			continue
		}
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Errorf("ParseCredentialUID(%q) = %X, %v; want %X", tc.in, got, err, tc.want)
		}
	}
}
