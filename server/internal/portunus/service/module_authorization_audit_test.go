package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

type modAuthFixture struct {
	svc   *service.ModuleAuthorizationService
	ms    store.MemberAccessStore
	as    store.ModuleAuthorizationStore
	audit store.AuditStore
}

// newModAuthFixture seeds modules woodshop and laser plus an active member
// "target" with no authorizations.
func newModAuthFixture(t *testing.T) modAuthFixture {
	t.Helper()
	conn, w := openSvcTestDB(t)
	f := modAuthFixture{
		ms:    sqlitestore.NewMemberAccessStore(conn, w),
		as:    sqlitestore.NewModuleAuthorizationStore(conn, w),
		audit: sqlitestore.NewAuditStore(conn, w),
	}
	f.svc = service.NewModuleAuthorizationService(f.as, f.ms, f.audit)
	seedModule(t, conn, "woodshop")
	seedModule(t, conn, "laser")
	if err := f.ms.CreateMember(context.Background(), "target", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatal(err)
	}
	return f
}

// lastAudit returns the newest audit entry and its decoded details.
func (f modAuthFixture) lastAudit(t *testing.T) (store.AuditEntry, map[string]any) {
	t.Helper()
	entries, err := f.audit.ListAuditEntries(context.Background(), 1)
	if err != nil || len(entries) == 0 {
		t.Fatalf("ListAuditEntries: %v (n=%d)", err, len(entries))
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(entries[0].Details), &details); err != nil {
		t.Fatalf("audit details are not JSON: %q", entries[0].Details)
	}
	return entries[0], details
}

func TestModuleAuth_DuplicateRevokeRegrant(t *testing.T) {
	f := newModAuthFixture(t)
	ctx := context.Background()

	if err := f.svc.GrantAuthorization(ctx, anyActor(), "target", "woodshop", nil, ""); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := f.svc.GrantAuthorization(ctx, anyActor(), "target", "woodshop", nil, ""); !errors.Is(err, service.ErrAuthorizationAlreadyExists) {
		t.Fatalf("duplicate grant: got %v", err)
	}
	if err := f.svc.RevokeAuthorization(ctx, anyActor(), "target", "woodshop"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := f.svc.RevokeAuthorization(ctx, anyActor(), "target", "woodshop"); !errors.Is(err, service.ErrAuthorizationNotFound) {
		t.Fatalf("second revoke: got %v, want ErrAuthorizationNotFound", err)
	}
	if err := f.svc.GrantAuthorization(ctx, anyActor(), "target", "woodshop", nil, ""); err != nil {
		t.Fatalf("re-grant after revoke: %v", err)
	}
}

func TestModuleAuth_GrantStoresExpiryAndGrantor(t *testing.T) {
	f := newModAuthFixture(t)
	ctx := context.Background()
	exp := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Millisecond)

	if err := f.svc.GrantAuthorization(ctx, anyActor(), " target ", " laser ", &exp, `{"days":"mon-fri"}`); err != nil {
		t.Fatalf("grant: %v", err)
	}
	recs, err := f.svc.ListByMember(ctx, "target")
	if err != nil || len(recs) != 1 {
		t.Fatalf("ListByMember: %+v, %v", recs, err)
	}
	r := recs[0]
	if r.ModuleID != "laser" || r.GrantedByUUID != "admin-uuid" || r.ExpiresAt == nil || !r.ExpiresAt.Equal(exp) || r.TimeRestriction != `{"days":"mon-fri"}` {
		t.Fatalf("stored authorization = %+v", r)
	}
	byModule, err := f.svc.ListByModule(ctx, "laser")
	if err != nil || len(byModule) != 1 || byModule[0].MemberUUID != "target" {
		t.Fatalf("ListByModule: %+v, %v", byModule, err)
	}
	if none, err := f.svc.ListByModule(ctx, "woodshop"); err != nil || len(none) != 0 {
		t.Fatalf("ListByModule(woodshop) = %+v, %v; want empty", none, err)
	}
}

func TestModuleAuth_RequiredFields(t *testing.T) {
	f := newModAuthFixture(t)
	ctx := context.Background()
	if err := f.svc.GrantAuthorization(ctx, anyActor(), " ", "woodshop", nil, ""); !errors.Is(err, service.ErrMemberUUIDRequired) {
		t.Errorf("grant blank member: %v", err)
	}
	if err := f.svc.GrantAuthorization(ctx, anyActor(), "target", "", nil, ""); !errors.Is(err, service.ErrModuleIDRequired) {
		t.Errorf("grant blank module: %v", err)
	}
	if err := f.svc.RevokeAuthorization(ctx, anyActor(), "", "woodshop"); !errors.Is(err, service.ErrMemberUUIDRequired) {
		t.Errorf("revoke blank member: %v", err)
	}
	if err := f.svc.RevokeAuthorization(ctx, anyActor(), "target", " "); !errors.Is(err, service.ErrModuleIDRequired) {
		t.Errorf("revoke blank module: %v", err)
	}
}

// Every refused grant or revoke leaves an audit row naming why, attributed to
// the acting admin. The caller only ever sees ErrGrantOutOfScope.
func TestModuleAuth_RefusalsAreAudited(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, f modAuthFixture)
		call   func(f modAuthFixture) error
		action string
		reason string
	}{
		{
			name: "grant without permission",
			call: func(f modAuthFixture) error {
				return f.svc.GrantAuthorization(context.Background(), service.GrantActor{AdminUUID: "nobody"}, "target", "woodshop", nil, "")
			},
			action: "module_auth.granted", reason: "no_grant_permission",
		},
		{
			name: "revoke without permission",
			call: func(f modAuthFixture) error {
				return f.svc.RevokeAuthorization(context.Background(), service.GrantActor{AdminUUID: "nobody"}, "target", "woodshop")
			},
			action: "module_auth.revoked", reason: "no_revoke_permission",
		},
		{
			name: "held actor not linked",
			call: func(f modAuthFixture) error {
				return f.svc.GrantAuthorization(context.Background(), heldActor(""), "target", "woodshop", nil, "")
			},
			action: "module_auth.granted", reason: "actor_not_linked",
		},
		{
			name: "held actor linked to missing member",
			call: func(f modAuthFixture) error {
				return f.svc.GrantAuthorization(context.Background(), heldActor("ghost"), "target", "woodshop", nil, "")
			},
			action: "module_auth.granted", reason: "actor_member_not_found",
		},
		{
			name: "held actor's member disabled",
			setup: func(t *testing.T, f modAuthFixture) {
				seedActiveMemberWithAuth(t, context.Background(), f.ms, f.as, "op", "woodshop")
				if err := f.ms.SetEnabled(context.Background(), "op", false); err != nil {
					t.Fatal(err)
				}
			},
			call: func(f modAuthFixture) error {
				return f.svc.GrantAuthorization(context.Background(), heldActor("op"), "target", "woodshop", nil, "")
			},
			action: "module_auth.granted", reason: "actor_member_inactive",
		},
		{
			name: "held actor lacks the module (revoke)",
			setup: func(t *testing.T, f modAuthFixture) {
				seedActiveMemberWithAuth(t, context.Background(), f.ms, f.as, "op", "woodshop")
			},
			call: func(f modAuthFixture) error {
				return f.svc.RevokeAuthorization(context.Background(), heldActor("op"), "target", "laser")
			},
			action: "module_auth.revoked", reason: "actor_lacks_module_access",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newModAuthFixture(t)
			if tc.setup != nil {
				tc.setup(t, f)
			}
			if err := tc.call(f); !errors.Is(err, service.ErrGrantOutOfScope) {
				t.Fatalf("got %v, want ErrGrantOutOfScope", err)
			}
			e, details := f.lastAudit(t)
			if e.Action != tc.action || e.Result != "failure" || details["reason"] != tc.reason {
				t.Fatalf("audit = action %q result %q details %v; want %s failure reason %s", e.Action, e.Result, details, tc.action, tc.reason)
			}
			if e.ActorUUID == "" || e.ActorType != store.ActorTypeAdmin {
				t.Errorf("audit actor = %q/%q, want the acting admin", e.ActorUUID, e.ActorType)
			}
		})
	}
}

// A held-scope grant records which of the actor's own authorizations
// justified it, so a grant can be traced back to its qualifying access.
func TestModuleAuth_HeldGrantRecordsQualifyingAuthorization(t *testing.T) {
	f := newModAuthFixture(t)
	ctx := context.Background()
	seedActiveMemberWithAuth(t, ctx, f.ms, f.as, "op", "woodshop")
	opAuth, err := f.as.GetAuthorization(ctx, "op", "woodshop")
	if err != nil {
		t.Fatal(err)
	}

	if err := f.svc.GrantAuthorization(ctx, heldActor("op"), "target", "woodshop", nil, ""); err != nil {
		t.Fatalf("held grant: %v", err)
	}
	e, details := f.lastAudit(t)
	if e.Result != "success" || details["qualifying_authorization_id"] != float64(opAuth.AuthorizationID) {
		t.Fatalf("audit = %q %v, want success with qualifying_authorization_id %d", e.Result, details, opAuth.AuthorizationID)
	}
}
