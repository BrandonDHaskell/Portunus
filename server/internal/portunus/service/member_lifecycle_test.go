package service_test

// MemberAccessService lifecycle operations, each checked at the door: the
// point of disabling or archiving a member is that the card stops working.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
)

func (f branchFixture) door(t *testing.T) doorDecision {
	t.Helper()
	resp, err := f.svc(nil, nil, nil, memory.NewAccessEventStore()).Decide(context.Background(), tapDoor1)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return doorDecision{resp.Granted, resp.Reason}
}

type doorDecision struct {
	granted bool
	reason  string
}

func TestMemberLifecycle_DisableEnable(t *testing.T) {
	f := newBranchFixture(t)
	ms := service.NewMemberAccessService(f.ma, nil)
	ctx := context.Background()

	if err := ms.Disable(ctx, f.member, "test-admin"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if d := f.door(t); d.granted || d.reason != "member_disabled" {
		t.Fatalf("disabled member at door: %+v", d)
	}
	if err := ms.Enable(ctx, f.member, "test-admin"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if d := f.door(t); !d.granted {
		t.Fatalf("re-enabled member denied: %+v", d)
	}
}

func TestMemberLifecycle_ArchiveIsPermanentAtTheDoor(t *testing.T) {
	f := newBranchFixture(t)
	ms := service.NewMemberAccessService(f.ma, nil)
	ctx := context.Background()

	if err := ms.Archive(ctx, f.member, "admin-1"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	rec, err := ms.GetMember(ctx, f.member)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != store.MemberStatusArchived || rec.ArchivedAt == nil || rec.ArchivedByUUID != "admin-1" {
		t.Fatalf("archive not recorded: %+v", rec)
	}
	if d := f.door(t); d.granted || d.reason != "member_archived" {
		t.Fatalf("archived member at door: %+v", d)
	}

	// Neither enabling nor a policy edit brings an archived member back.
	if err := ms.Enable(ctx, f.member, "test-admin"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := ms.UpdateMemberPolicy(ctx, f.member, nil, intPtr(365), "test-admin"); err != nil {
		t.Fatalf("UpdateMemberPolicy: %v", err)
	}
	if d := f.door(t); d.granted {
		t.Fatalf("archived member granted after enable/policy edit: %+v", d)
	}
}

func TestMemberLifecycle_UpdateMemberPolicy(t *testing.T) {
	f := newBranchFixture(t)
	ms := service.NewMemberAccessService(f.ma, nil)
	ctx := context.Background()

	exp := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Millisecond)
	if err := ms.UpdateMemberPolicy(ctx, f.member, &exp, intPtr(14), "test-admin"); err != nil {
		t.Fatalf("UpdateMemberPolicy: %v", err)
	}
	rec, _ := ms.GetMember(ctx, f.member)
	if rec.ExpiresAt == nil || !rec.ExpiresAt.Equal(exp) || rec.InactivityLimitDays == nil || *rec.InactivityLimitDays != 14 {
		t.Fatalf("policy not stored: expires=%v inactivity=%v", rec.ExpiresAt, rec.InactivityLimitDays)
	}

	if err := ms.UpdateMemberPolicy(ctx, f.member, nil, nil, "test-admin"); err != nil {
		t.Fatalf("clear policy: %v", err)
	}
	rec, _ = ms.GetMember(ctx, f.member)
	if rec.ExpiresAt != nil || rec.InactivityLimitDays != nil {
		t.Fatalf("policy not cleared: expires=%v inactivity=%v", rec.ExpiresAt, rec.InactivityLimitDays)
	}
}

func TestMemberLifecycle_ErrorMapping(t *testing.T) {
	f := newBranchFixture(t)
	ms := service.NewMemberAccessService(f.ma, nil)
	ctx := context.Background()

	calls := map[string]func(uuid string) error{
		"Disable": func(u string) error { return ms.Disable(ctx, u, "test-admin") },
		"Enable":  func(u string) error { return ms.Enable(ctx, u, "test-admin") },
		"Archive": func(u string) error { return ms.Archive(ctx, u, "") },
		"UpdateMemberPolicy": func(u string) error {
			return ms.UpdateMemberPolicy(ctx, u, nil, nil, "test-admin")
		},
		"AttachCredential": func(u string) error {
			return ms.AttachCredential(ctx, u, service.HashCredentialID([]byte{0x09, 0x09}, branchSecret), "test-admin")
		},
	}
	for name, call := range calls {
		if err := call("  "); !errors.Is(err, service.ErrMemberUUIDRequired) {
			t.Errorf("%s(blank): got %v, want ErrMemberUUIDRequired", name, err)
		}
		if err := call("no-such-member"); !errors.Is(err, service.ErrMemberNotFound) {
			t.Errorf("%s(unknown): got %v, want ErrMemberNotFound", name, err)
		}
	}

	if _, err := ms.GetMember(ctx, "no-such-member"); !errors.Is(err, service.ErrMemberNotFound) {
		t.Errorf("GetMember(unknown): got %v", err)
	}
	if err := ms.AttachCredential(ctx, f.member, nil, "test-admin"); !errors.Is(err, service.ErrCredentialHashRequired) {
		t.Errorf("AttachCredential(nil hash): got %v", err)
	}
}

func TestMemberLifecycle_ProvisionAndLists(t *testing.T) {
	f := newBranchFixture(t)
	ms := service.NewMemberAccessService(f.ma, nil)
	ctx := context.Background()

	rec, err := ms.ProvisionMember(ctx, "admin-1", nil, intPtr(30))
	if err != nil {
		t.Fatalf("ProvisionMember: %v", err)
	}
	if rec.ProvisioningStatus != store.ProvisioningStatusPendingAuthorization || rec.CreatedByUUID != "admin-1" || len(rec.UUID) != 36 {
		t.Fatalf("provisioned member = %+v", rec)
	}

	all, err := ms.ListMembers(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListMembers: %d members, err %v; want 2", len(all), err)
	}
	pending, err := ms.ListPendingAuthorizations(ctx)
	if err != nil || len(pending) != 1 || pending[0].UUID != rec.UUID {
		t.Fatalf("ListPendingAuthorizations = %+v, err %v; want only the new member", pending, err)
	}
}

// Approval must never revive an archived member, even one archived while
// still pending (a stale capture, or one an admin rejected).
func TestMemberLifecycle_ApproveRefusesArchived(t *testing.T) {
	f := newBranchFixture(t)
	ms := service.NewMemberAccessService(f.ma, nil)
	ctx := context.Background()

	const pend = "abababab-0000-4000-8000-0000000000a2"
	if err := f.ma.CreateMember(ctx, pend, "", store.ProvisioningStatusPendingAuthorization, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := ms.Archive(ctx, pend, "admin-1"); err != nil {
		t.Fatal(err)
	}
	if err := ms.ApprovePending(ctx, pend, "admin-2", nil, intPtr(30)); !errors.Is(err, service.ErrMemberNotPending) {
		t.Fatalf("approving an archived capture: got %v, want ErrMemberNotPending", err)
	}
	rec, _ := ms.GetMember(ctx, pend)
	if rec.Status != store.MemberStatusArchived {
		t.Fatalf("archived member's status became %q", rec.Status)
	}
}
