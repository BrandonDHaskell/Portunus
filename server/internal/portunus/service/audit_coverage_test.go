package service_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// auditTrail reads the audit log oldest first.
func auditTrail(t *testing.T, as store.AuditStore) []store.AuditEntry {
	t.Helper()
	entries, err := as.ListAuditEntries(context.Background(), 500)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries
}

// wantAudit asserts the next audit entry and returns its decoded details.
func wantAudit(t *testing.T, got []store.AuditEntry, i int, action, resourceID, actor, result string) map[string]any {
	t.Helper()
	if i >= len(got) {
		t.Fatalf("entry %d (%s): only %d audit entries", i, action, len(got))
	}
	e := got[i]
	if e.Action != action || e.ResourceID != resourceID || e.ActorUUID != actor || e.Result != result {
		t.Fatalf("entry %d = %s %s by %q -> %s; want %s %s by %q -> %s",
			i, e.Action, e.ResourceID, e.ActorUUID, e.Result, action, resourceID, actor, result)
	}
	var d map[string]any
	if e.Details != "" {
		if err := json.Unmarshal([]byte(e.Details), &d); err != nil {
			t.Fatalf("entry %d details are not JSON: %q", i, e.Details)
		}
	}
	return d
}

func TestAudit_MemberLifecycle(t *testing.T) {
	conn, w := openSvcTestDB(t)
	ma := sqlitestore.NewMemberAccessStore(conn, w)
	audit := sqlitestore.NewAuditStore(conn, w)
	svc := service.NewMemberAccessService(ma, audit)
	ctx := service.WithRequestIP(context.Background(), "10.1.2.3:5555")
	hash := service.HashCredentialID([]byte{0x04, 0x99}, []byte("s"))

	rec, err := svc.ProvisionMember(ctx, "adm", nil, intPtr(30))
	if err != nil {
		t.Fatal(err)
	}
	m := rec.UUID
	if err := svc.AttachCredential(ctx, m, hash, "adm"); err != nil {
		t.Fatal(err)
	}
	other, _ := svc.ProvisionMember(ctx, "adm", nil, nil)
	_ = svc.AttachCredential(ctx, other.UUID, hash, "adm") // duplicate: refused
	_ = svc.ApprovePending(ctx, m, "adm", nil, intPtr(30))
	_ = svc.ApprovePending(ctx, m, "adm", nil, intPtr(30)) // not pending any more
	exp := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = svc.UpdateMemberPolicy(ctx, m, &exp, intPtr(60), "adm")
	_ = svc.Disable(ctx, m, "adm")
	_ = svc.Enable(ctx, m, "adm")
	_ = svc.Archive(ctx, m, "adm")
	_ = svc.Disable(ctx, "no-such-member", "adm")

	got := auditTrail(t, audit)
	wantAudit(t, got, 0, "member.create", m, "adm", "success")
	wantAudit(t, got, 1, "member.credential_attach", m, "adm", "success")
	wantAudit(t, got, 2, "member.create", other.UUID, "adm", "success")
	if d := wantAudit(t, got, 3, "member.credential_attach", other.UUID, "adm", "failure"); d["reason"] != "duplicate_credential_pending" {
		t.Errorf("duplicate attach reason = %v", d["reason"])
	}
	if d := wantAudit(t, got, 4, "member.approve", m, "adm", "success"); d["inactivity_limit_days"] != float64(30) {
		t.Errorf("approve details = %v", d)
	}
	if d := wantAudit(t, got, 5, "member.approve", m, "adm", "failure"); d["reason"] != "not_pending" {
		t.Errorf("second approve reason = %v", d["reason"])
	}
	if d := wantAudit(t, got, 6, "member.policy_update", m, "adm", "success"); d["expires_at"] != "2030-01-01T00:00:00Z" {
		t.Errorf("policy details = %v", d)
	}
	wantAudit(t, got, 7, "member.disable", m, "adm", "success")
	wantAudit(t, got, 8, "member.enable", m, "adm", "success")
	wantAudit(t, got, 9, "member.archive", m, "adm", "success")
	if d := wantAudit(t, got, 10, "member.disable", "no-such-member", "adm", "failure"); d["reason"] != "member_not_found" {
		t.Errorf("unknown member reason = %v", d["reason"])
	}

	hashHex := hex.EncodeToString(hash)
	for _, e := range got {
		if e.IPAddress != "10.1.2.3:5555" || e.ActorType != store.ActorTypeAdmin {
			t.Errorf("%s: ip=%q actor_type=%q", e.Action, e.IPAddress, e.ActorType)
		}
		if strings.Contains(strings.ToLower(e.Details), hashHex[:16]) {
			t.Errorf("%s: credential hash written to the audit log", e.Action)
		}
	}
}

func TestAudit_ModulesAndDoors(t *testing.T) {
	conn, w := openSvcTestDB(t)
	audit := sqlitestore.NewAuditStore(conn, w)
	svc := service.NewAdminService(sqlitestore.NewModuleAdminStore(conn, w), nil, audit)
	ctx := context.Background()

	if _, err := svc.RegisterDoor(ctx, types.RegisterDoorRequest{DoorID: "front", Name: "Front"}, "adm"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterModule(ctx, types.RegisterModuleRequest{ModuleID: "m1", DoorID: "front"}, "adm"); err != nil {
		t.Fatal(err)
	}
	_, _ = svc.RegisterModule(ctx, types.RegisterModuleRequest{ModuleID: "m2", DoorID: "nowhere"}, "adm")
	_ = svc.RevokeModule(ctx, "m1", "adm")
	_ = svc.RevokeModule(ctx, "ghost", "adm")
	_ = svc.DeleteModule(ctx, "m1", "adm")
	_ = svc.DeleteDoor(ctx, "front", "adm")

	got := auditTrail(t, audit)
	wantAudit(t, got, 0, "door.register", "front", "adm", "success")
	if d := wantAudit(t, got, 1, "module.register", "m1", "adm", "success"); d["door_id"] != "front" {
		t.Errorf("register details = %v", d)
	}
	if d := wantAudit(t, got, 2, "module.register", "m2", "adm", "failure"); d["reason"] != "door_not_found" {
		t.Errorf("bad register reason = %v", d["reason"])
	}
	wantAudit(t, got, 3, "module.revoke", "m1", "adm", "success")
	if d := wantAudit(t, got, 4, "module.revoke", "ghost", "adm", "failure"); d["reason"] != "module_not_found" {
		t.Errorf("unknown revoke reason = %v", d["reason"])
	}
	wantAudit(t, got, 5, "module.delete", "m1", "adm", "success")
	wantAudit(t, got, 6, "door.delete", "front", "adm", "success")
}

func TestAudit_SessionRevocations(t *testing.T) {
	conn, w := openSvcTestDB(t)
	users := sqlitestore.NewAdminUserStore(conn, w)
	sessions := sqlitestore.NewSessionStore(conn, w)
	audit := sqlitestore.NewAuditStore(conn, w)
	auth := service.NewAuthService(users, sessions, sqlitestore.NewRoleStore(conn, w), log.New(&syncBuffer{}, "", 0), audit)
	ctx := context.Background()
	const pw = "correct-horse-battery"
	if err := users.CreateAdminUser(ctx, "u1", "alice", hashPW(t, pw), "admin"); err != nil {
		t.Fatal(err)
	}

	sid, err := auth.Login(ctx, "alice", pw)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Logout(ctx, sid); err != nil {
		t.Fatal(err)
	}
	if err := auth.ChangePassword(ctx, "u1", pw, "a-new-valid-password"); err != nil {
		t.Fatal(err)
	}

	got := auditTrail(t, audit)
	wantAudit(t, got, 0, "admin_user.logout", "u1", "u1", "success")
	if d := wantAudit(t, got, 1, "admin_user.password_change", "u1", "u1", "success"); d["all_sessions_revoked"] != true {
		t.Errorf("password change details = %v", d)
	}
}

func TestAudit_AutomaticExpiry(t *testing.T) {
	conn, w := openSvcTestDB(t)
	ma := sqlitestore.NewMemberAccessStore(conn, w)
	audit := sqlitestore.NewAuditStore(conn, w)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)
	if err := ma.CreateMember(ctx, "m-exp", "", store.ProvisioningStatusActive, &past, nil); err != nil {
		t.Fatal(err)
	}

	logs := &syncBuffer{}
	ew := service.NewExpiryWorker(ma, audit, service.ExpiryWorkerConfig{PendingTTLDays: 0}, log.New(logs, "", 0))
	ew.Start(ctx)
	eventually(t, "expiry recorded", func() bool { return len(auditTrail(t, audit)) > 0 })
	ew.Stop()

	got := auditTrail(t, audit)
	e := got[0]
	var d map[string]any
	_ = json.Unmarshal([]byte(e.Details), &d)
	if e.Action != "member.expire" || e.ActorType != store.ActorTypeSystem || d["reason"] != "hard_deadline" || d["count"] != float64(1) {
		t.Fatalf("expiry audit = %+v details %v", e, d)
	}
}
