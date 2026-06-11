package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// newModuleAuthSvc builds a ModuleAuthorizationService against an in-memory SQLite DB.
func newModuleAuthSvc(t *testing.T) (
	*service.ModuleAuthorizationService,
	store.MemberAccessStore,
	store.ModuleAuthorizationStore,
) {
	t.Helper()
	dbConn, writer := openSvcTestDB(t)
	ms := sqlitestore.NewMemberAccessStore(dbConn, writer)
	as := sqlitestore.NewModuleAuthorizationStore(dbConn, writer)
	svc := service.NewModuleAuthorizationService(as, ms, nil)

	// seed FK parent so module_authorizations.module_id can reference it
	_, err := dbConn.Exec(
		`INSERT OR IGNORE INTO modules(module_id, enabled, created_at_ms, updated_at_ms) VALUES (?,1,0,0)`,
		"woodshop")
	if err != nil {
		t.Fatalf("seed module woodshop: %v", err)
	}
	_, err = dbConn.Exec(
		`INSERT OR IGNORE INTO modules(module_id, enabled, created_at_ms, updated_at_ms) VALUES (?,1,0,0)`,
		"laser")
	if err != nil {
		t.Fatalf("seed module laser: %v", err)
	}
	return svc, ms, as
}

// anyActor returns a GrantActor with grant_any / revoke_any permissions set.
func anyActor() service.GrantActor {
	return service.GrantActor{
		AdminUUID: "admin-uuid",
		Perms: map[string]struct{}{
			permissions.ModuleAuthGrantAny:  {},
			permissions.ModuleAuthRevokeAny: {},
		},
	}
}

// heldActor returns a GrantActor with only _held permissions and the given linked memberUUID.
func heldActor(linkedMemberUUID string) service.GrantActor {
	return service.GrantActor{
		AdminUUID:  "operator-uuid",
		MemberUUID: linkedMemberUUID,
		Perms: map[string]struct{}{
			permissions.ModuleAuthGrantHeld:  {},
			permissions.ModuleAuthRevokeHeld: {},
		},
	}
}

// seedActiveMemberWithAuth creates an active member that holds an authorization
// for the given moduleID. Returns the member UUID and the authorization_id.
func seedActiveMemberWithAuth(
	t *testing.T,
	ctx context.Context,
	ms store.MemberAccessStore,
	as store.ModuleAuthorizationStore,
	memberUUID, moduleID string,
) {
	t.Helper()
	if err := ms.CreateMember(ctx, memberUUID, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember %s: %v", memberUUID, err)
	}
	if err := ms.ApprovePending(ctx, memberUUID, "", nil, intPtr(30)); err != nil {
		// member was created as Active directly — ApprovePending would fail; ignore
		_ = err
	}
	if err := as.GrantAuthorization(ctx, memberUUID, moduleID, "", nil, ""); err != nil {
		t.Fatalf("GrantAuthorization seed %s→%s: %v", memberUUID, moduleID, err)
	}
}

func intPtr(n int) *int { return &n }

// ── _any actor ────────────────────────────────────────────────────────────────

func TestModuleAuthService_Grant_AnyActor_GenesisPath(t *testing.T) {
	svc, ms, _ := newModuleAuthSvc(t)
	ctx := context.Background()

	target := "member-target"
	if err := ms.CreateMember(ctx, target, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	if err := svc.GrantAuthorization(ctx, anyActor(), target, "woodshop", nil, ""); err != nil {
		t.Fatalf("expected success for _any actor, got: %v", err)
	}
}

// ── _held actor: success ──────────────────────────────────────────────────────

func TestModuleAuthService_Grant_HeldActor_CanGrantHeldModule(t *testing.T) {
	svc, ms, as := newModuleAuthSvc(t)
	ctx := context.Background()

	// operator's linked member holds woodshop
	linkedMember := "op-member"
	seedActiveMemberWithAuth(t, ctx, ms, as, linkedMember, "woodshop")

	// target member receiving the grant
	target := "target-member"
	if err := ms.CreateMember(ctx, target, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember target: %v", err)
	}

	actor := heldActor(linkedMember)
	if err := svc.GrantAuthorization(ctx, actor, target, "woodshop", nil, ""); err != nil {
		t.Fatalf("expected success granting held module, got: %v", err)
	}
}

func TestModuleAuthService_Grant_HeldActor_CannotGrantUnheldModule(t *testing.T) {
	svc, ms, as := newModuleAuthSvc(t)
	ctx := context.Background()

	// operator's linked member holds woodshop only
	linkedMember := "op-member"
	seedActiveMemberWithAuth(t, ctx, ms, as, linkedMember, "woodshop")

	target := "target-member"
	if err := ms.CreateMember(ctx, target, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember target: %v", err)
	}

	actor := heldActor(linkedMember)
	err := svc.GrantAuthorization(ctx, actor, target, "laser", nil, "")
	if !errors.Is(err, service.ErrGrantOutOfScope) {
		t.Fatalf("expected ErrGrantOutOfScope, got: %v", err)
	}
}

// ── _held actor: failure cases ────────────────────────────────────────────────

func TestModuleAuthService_Grant_HeldActor_NoLinkedMember(t *testing.T) {
	svc, ms, _ := newModuleAuthSvc(t)
	ctx := context.Background()

	target := "target-member"
	if err := ms.CreateMember(ctx, target, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	actor := heldActor("") // no linked member
	err := svc.GrantAuthorization(ctx, actor, target, "woodshop", nil, "")
	if !errors.Is(err, service.ErrGrantOutOfScope) {
		t.Fatalf("expected ErrGrantOutOfScope for unlinked actor, got: %v", err)
	}
}

func TestModuleAuthService_Grant_HeldActor_InactiveMember(t *testing.T) {
	svc, ms, _ := newModuleAuthSvc(t)
	ctx := context.Background()

	// linked member exists but is disabled
	linkedMember := "op-member-disabled"
	if err := ms.CreateMember(ctx, linkedMember, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember linked: %v", err)
	}
	if err := ms.SetEnabled(ctx, linkedMember, false); err != nil {
		t.Fatalf("SetEnabled false: %v", err)
	}

	target := "target-member"
	if err := ms.CreateMember(ctx, target, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember target: %v", err)
	}

	actor := heldActor(linkedMember)
	err := svc.GrantAuthorization(ctx, actor, target, "woodshop", nil, "")
	if !errors.Is(err, service.ErrGrantOutOfScope) {
		t.Fatalf("expected ErrGrantOutOfScope for inactive linked member, got: %v", err)
	}
}

func TestModuleAuthService_Grant_HeldActor_ExpiredAuthorization(t *testing.T) {
	svc, ms, as := newModuleAuthSvc(t)
	ctx := context.Background()

	linkedMember := "op-member-expired-auth"
	if err := ms.CreateMember(ctx, linkedMember, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember linked: %v", err)
	}

	// grant an authorization that has already expired
	past := time.Now().UTC().Add(-time.Hour)
	if err := as.GrantAuthorization(ctx, linkedMember, "woodshop", "", &past, ""); err != nil {
		t.Fatalf("GrantAuthorization (expired): %v", err)
	}

	target := "target-member"
	if err := ms.CreateMember(ctx, target, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember target: %v", err)
	}

	actor := heldActor(linkedMember)
	err := svc.GrantAuthorization(ctx, actor, target, "woodshop", nil, "")
	if !errors.Is(err, service.ErrGrantOutOfScope) {
		t.Fatalf("expected ErrGrantOutOfScope for expired authorization, got: %v", err)
	}
}

func TestModuleAuthService_Grant_HeldActor_RevokedAuthorization(t *testing.T) {
	svc, ms, as := newModuleAuthSvc(t)
	ctx := context.Background()

	linkedMember := "op-member-revoked-auth"
	if err := ms.CreateMember(ctx, linkedMember, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember linked: %v", err)
	}
	if err := as.GrantAuthorization(ctx, linkedMember, "woodshop", "", nil, ""); err != nil {
		t.Fatalf("GrantAuthorization: %v", err)
	}
	if err := as.RevokeAuthorization(ctx, linkedMember, "woodshop", ""); err != nil {
		t.Fatalf("RevokeAuthorization: %v", err)
	}

	target := "target-member"
	if err := ms.CreateMember(ctx, target, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember target: %v", err)
	}

	actor := heldActor(linkedMember)
	err := svc.GrantAuthorization(ctx, actor, target, "woodshop", nil, "")
	if !errors.Is(err, service.ErrGrantOutOfScope) {
		t.Fatalf("expected ErrGrantOutOfScope for revoked authorization, got: %v", err)
	}
}

// ── revoke: symmetric ─────────────────────────────────────────────────────────

func TestModuleAuthService_Revoke_AnyActor(t *testing.T) {
	svc, ms, as := newModuleAuthSvc(t)
	ctx := context.Background()

	target := "target-member"
	seedActiveMemberWithAuth(t, ctx, ms, as, target, "woodshop")

	if err := svc.RevokeAuthorization(ctx, anyActor(), target, "woodshop"); err != nil {
		t.Fatalf("expected success for _any revoke, got: %v", err)
	}
}

func TestModuleAuthService_Revoke_HeldActor_CanRevokeHeldModule(t *testing.T) {
	svc, ms, as := newModuleAuthSvc(t)
	ctx := context.Background()

	linkedMember := "op-member"
	seedActiveMemberWithAuth(t, ctx, ms, as, linkedMember, "woodshop")

	target := "target-member"
	seedActiveMemberWithAuth(t, ctx, ms, as, target, "woodshop")

	actor := heldActor(linkedMember)
	if err := svc.RevokeAuthorization(ctx, actor, target, "woodshop"); err != nil {
		t.Fatalf("expected success revoking held module, got: %v", err)
	}
}

func TestModuleAuthService_Revoke_HeldActor_CannotRevokeUnheldModule(t *testing.T) {
	svc, ms, as := newModuleAuthSvc(t)
	ctx := context.Background()

	// operator holds woodshop, not laser
	linkedMember := "op-member"
	seedActiveMemberWithAuth(t, ctx, ms, as, linkedMember, "woodshop")

	target := "target-member"
	seedActiveMemberWithAuth(t, ctx, ms, as, target, "laser")

	actor := heldActor(linkedMember)
	err := svc.RevokeAuthorization(ctx, actor, target, "laser")
	if !errors.Is(err, service.ErrGrantOutOfScope) {
		t.Fatalf("expected ErrGrantOutOfScope revoking unheld module, got: %v", err)
	}
}

func TestModuleAuthService_Revoke_HeldActor_NoLinkedMember(t *testing.T) {
	svc, ms, as := newModuleAuthSvc(t)
	ctx := context.Background()

	target := "target-member"
	seedActiveMemberWithAuth(t, ctx, ms, as, target, "woodshop")

	actor := heldActor("") // no linked member
	err := svc.RevokeAuthorization(ctx, actor, target, "woodshop")
	if !errors.Is(err, service.ErrGrantOutOfScope) {
		t.Fatalf("expected ErrGrantOutOfScope for unlinked actor on revoke, got: %v", err)
	}
}

// ── no permission ─────────────────────────────────────────────────────────────

func TestModuleAuthService_Grant_NoPermission(t *testing.T) {
	svc, ms, _ := newModuleAuthSvc(t)
	ctx := context.Background()

	target := "target-member"
	if err := ms.CreateMember(ctx, target, "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	actor := service.GrantActor{AdminUUID: "nobody", Perms: map[string]struct{}{}}
	err := svc.GrantAuthorization(ctx, actor, target, "woodshop", nil, "")
	if !errors.Is(err, service.ErrGrantOutOfScope) {
		t.Fatalf("expected ErrGrantOutOfScope for actor with no permissions, got: %v", err)
	}
}

func TestModuleAuthService_Revoke_NoPermission(t *testing.T) {
	svc, ms, as := newModuleAuthSvc(t)
	ctx := context.Background()

	target := "target-member"
	seedActiveMemberWithAuth(t, ctx, ms, as, target, "woodshop")

	actor := service.GrantActor{AdminUUID: "nobody", Perms: map[string]struct{}{}}
	err := svc.RevokeAuthorization(ctx, actor, target, "woodshop")
	if !errors.Is(err, service.ErrGrantOutOfScope) {
		t.Fatalf("expected ErrGrantOutOfScope for actor with no permissions, got: %v", err)
	}
}
