package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// linkFixture has members m1 and m2, a superuser "boss" (admin role), and a
// limited account "op" whose role holds only admin_user.edit.
type linkFixture struct {
	svc   *service.AdminUserService
	users *sqlitestore.AdminUserStore
	audit store.AuditStore
}

func newLinkFixture(t *testing.T) linkFixture {
	t.Helper()
	ctx := context.Background()
	conn, w := openSvcTestDB(t)
	f := linkFixture{users: sqlitestore.NewAdminUserStore(conn, w), audit: sqlitestore.NewAuditStore(conn, w)}
	roles := sqlitestore.NewRoleStore(conn, w)
	members := sqlitestore.NewMemberAccessStore(conn, w)
	f.svc = service.NewAdminUserService(f.users, roles, f.audit)

	for _, m := range []string{"m1", "m2"} {
		if err := members.CreateMember(ctx, m, "", store.ProvisioningStatusActive, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := roles.CreateRole(ctx, "editor", "editor", ""); err != nil {
		t.Fatal(err)
	}
	if err := roles.SetRolePermissions(ctx, "editor", limitedPermsList()); err != nil {
		t.Fatal(err)
	}
	seedAdminUser(t, f.users, "boss", "admin", true)
	seedAdminUser(t, f.users, "op", "editor", true)
	seedAdminUser(t, f.users, "op2", "editor", true)
	return f
}

func limitedPermsList() []string {
	var out []string
	for p := range limitedPerms() {
		out = append(out, p)
	}
	return out
}

func strp(s string) *string { return &s }

func TestMemberLink_LinkUnlinkAndDuplicate(t *testing.T) {
	f := newLinkFixture(t)
	ctx := context.Background()

	if err := f.svc.SetMemberLink(ctx, "boss", allPerms(), "op", strp("m1")); err != nil {
		t.Fatalf("link: %v", err)
	}
	rec, _ := f.users.GetAdminUserByUUID(ctx, "op")
	if rec.MemberUUID == nil || *rec.MemberUUID != "m1" {
		t.Fatalf("link not stored: %v", rec.MemberUUID)
	}
	requireAuditEntry(t, f.audit, "admin_user.member_linked", "op")

	// One badge, one console account.
	if err := f.svc.SetMemberLink(ctx, "boss", allPerms(), "op2", strp("m1")); !errors.Is(err, store.ErrMemberAlreadyLinked) {
		t.Fatalf("second account on the same member: got %v, want ErrMemberAlreadyLinked", err)
	}

	if err := f.svc.SetMemberLink(ctx, "boss", allPerms(), "op", nil); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	rec, _ = f.users.GetAdminUserByUUID(ctx, "op")
	if rec.MemberUUID != nil {
		t.Fatalf("unlink not stored: %v", *rec.MemberUUID)
	}

	if err := f.svc.SetMemberLink(ctx, "boss", allPerms(), "no-such-account", strp("m2")); !errors.Is(err, service.ErrAdminUserNotFound) {
		t.Errorf("unknown account: got %v", err)
	}
}

// The link defines held-scope grant rights, so nobody sets their own, and
// nobody changes the link of an account that outranks them.
func TestMemberLink_SelfAndOutrankingRefused(t *testing.T) {
	f := newLinkFixture(t)
	ctx := context.Background()

	if err := f.svc.SetMemberLink(ctx, "op", limitedPerms(), "op", strp("m1")); !errors.Is(err, service.ErrCannotSelfLink) {
		t.Errorf("self link (limited): got %v, want ErrCannotSelfLink", err)
	}
	if err := f.svc.SetMemberLink(ctx, "boss", allPerms(), "boss", strp("m1")); !errors.Is(err, service.ErrCannotSelfLink) {
		t.Errorf("self link (superuser): got %v, want ErrCannotSelfLink", err)
	}
	if err := f.svc.SetMemberLink(ctx, "op", limitedPerms(), "boss", strp("m1")); !errors.Is(err, service.ErrPermissionSubsetViolation) {
		t.Errorf("link on an outranking account: got %v, want ErrPermissionSubsetViolation", err)
	}
	for _, who := range []string{"op", "boss"} {
		rec, _ := f.users.GetAdminUserByUUID(ctx, who)
		if rec.MemberUUID != nil {
			t.Errorf("%s linked despite refusal", who)
		}
	}

	// A peer at the same level may set the link.
	if err := f.svc.SetMemberLink(ctx, "op", limitedPerms(), "op2", strp("m2")); err != nil {
		t.Errorf("peer link: %v", err)
	}
}
