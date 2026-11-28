package service_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// staleLookupStore never finds a credential, the view a capture racing a
// concurrent capture of the same card would get.
type staleLookupStore struct{ store.MemberAccessStore }

func (staleLookupStore) GetMemberByCredential(context.Context, []byte) (*store.MemberAccessRecord, error) {
	return nil, store.ErrNotFound
}

type failingAttachStore struct{ store.MemberAccessStore }

func (failingAttachStore) AttachCredential(context.Context, string, []byte) error { return errBoom }

type provFixture struct {
	ms    store.MemberAccessStore
	audit store.AuditStore
}

func newProvFixture(t *testing.T) provFixture {
	t.Helper()
	conn, w := openSvcTestDB(t)
	seedModule(t, conn, testModuleID)
	return provFixture{ms: sqlitestore.NewMemberAccessStore(conn, w), audit: sqlitestore.NewAuditStore(conn, w)}
}

func (f provFixture) svc(ms store.MemberAccessStore) *service.ProvisionService {
	if ms == nil {
		ms = f.ms
	}
	reg := service.NewDeviceRegistry(memory.NewDeviceStore([]string{testModuleID}))
	return service.NewProvisionService(reg, ms, memory.NewAccessEventStore(), testSecret, f.audit)
}

func (f provFixture) pending(t *testing.T) []store.MemberAccessRecord {
	t.Helper()
	p, err := f.ms.ListPendingAuthorizations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var provUID = []byte{0x04, 0x55, 0x66, 0x77}

func TestProvision_Validation(t *testing.T) {
	f := newProvFixture(t)
	ctx := context.Background()
	if _, err := f.svc(nil).Provision(ctx, types.ProvisionCredentialRequest{ModuleID: " ", CredentialUID: provUID}); !errors.Is(err, service.ErrInvalidModuleID) {
		t.Errorf("blank module: %v", err)
	}
	if _, err := f.svc(nil).Provision(ctx, types.ProvisionCredentialRequest{ModuleID: testModuleID}); !errors.Is(err, service.ErrProvisionCredentialUIDRequired) {
		t.Errorf("empty uid: %v", err)
	}
}

func TestProvision_UnknownModule_CreatesNothing(t *testing.T) {
	f := newProvFixture(t)
	resp, err := f.svc(nil).Provision(context.Background(), types.ProvisionCredentialRequest{ModuleID: "ghost", CredentialUID: provUID})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if resp.Known || resp.OK || resp.MemberUUID != "" {
		t.Fatalf("unknown module response = %+v", resp)
	}
	if p := f.pending(t); len(p) != 0 {
		t.Fatalf("pending members created for an unknown module: %d", len(p))
	}
}

// The captured hash must be the canonical HashCredentialID of the raw UID, so
// the card works at the door after approval (hazard #1). The capture is
// audited as a system action.
func TestProvision_CaptureStoresCanonicalHashAndAudits(t *testing.T) {
	f := newProvFixture(t)
	resp, err := f.svc(nil).Provision(context.Background(), captureReq(provUID))
	if err != nil || resp.Status != types.ProvisionStatusPendingCreated {
		t.Fatalf("capture: %+v, %v", resp, err)
	}
	rec, err := f.ms.GetMember(context.Background(), resp.MemberUUID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rec.CredentialHash, service.HashCredentialID(provUID, testSecret)) {
		t.Fatal("stored hash is not HashCredentialID(rawUID, secret)")
	}
	entries, err := f.audit.ListAuditEntries(context.Background(), 5)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit entries: %d, %v", len(entries), err)
	}
	if e := entries[0]; e.Action != "member.capture" || e.ResourceID != resp.MemberUUID || e.ActorType != store.ActorTypeSystem {
		t.Fatalf("audit entry = %+v", e)
	}
}

func TestProvision_DuplicateStatuses(t *testing.T) {
	cases := []struct {
		name   string
		status store.MemberStatus
		want   types.ProvisionStatus
	}{
		{"active member", store.MemberStatusActive, types.ProvisionStatusDuplicateActive},
		{"expired member", store.MemberStatusExpired, types.ProvisionStatusDuplicateInactive},
		{"archived member", store.MemberStatusArchived, types.ProvisionStatusDuplicateInactive},
		{"suspended member", store.MemberStatusSuspended, types.ProvisionStatusDuplicateActive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newProvFixture(t)
			ctx := context.Background()
			if err := f.ms.CreateMember(ctx, "existing", "", store.ProvisioningStatusActive, nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := f.ms.AttachCredential(ctx, "existing", service.HashCredentialID(provUID, testSecret)); err != nil {
				t.Fatal(err)
			}
			if err := f.ms.SetStatus(ctx, "existing", tc.status); err != nil {
				t.Fatal(err)
			}
			resp, err := f.svc(nil).Provision(ctx, captureReq(provUID))
			if err != nil {
				t.Fatalf("Provision: %v", err)
			}
			if resp.Status != tc.want || resp.MemberUUID != "" {
				t.Fatalf("got %+v, want status %s and no new member", resp, tc.want)
			}
		})
	}
}

// Two captures of one card racing each other: the loser must report a
// duplicate and must not leave a credential-less pending member behind.
func TestProvision_ConcurrentCapture_NoOrphan(t *testing.T) {
	f := newProvFixture(t)
	ctx := context.Background()
	first, err := f.svc(nil).Provision(ctx, captureReq(provUID))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := f.svc(staleLookupStore{f.ms}).Provision(ctx, captureReq(provUID))
	if err != nil {
		t.Fatalf("racing capture: %v", err)
	}
	if resp.Status != types.ProvisionStatusDuplicatePending {
		t.Fatalf("racing capture status = %s, want duplicate_pending", resp.Status)
	}
	if p := f.pending(t); len(p) != 1 || p[0].UUID != first.MemberUUID {
		t.Fatalf("pending queue has %d entries, want only the first capture", len(p))
	}
}

func TestProvision_AttachFailure_NoOrphan(t *testing.T) {
	f := newProvFixture(t)
	if _, err := f.svc(failingAttachStore{f.ms}).Provision(context.Background(), captureReq(provUID)); !errors.Is(err, errBoom) {
		t.Fatalf("got %v, want the attach error", err)
	}
	if p := f.pending(t); len(p) != 0 {
		t.Fatalf("attach failure left %d pending member(s)", len(p))
	}
}
