package sqlite_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// ── Migration: member_access schema ──────────────────────────────────────────

func TestMigration_MemberAccessTableExists(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	var name string
	err := conn.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='member_access'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("member_access table not found: %v", err)
	}
}

func TestMigration_MemberAccessIndexes(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	indexes := []string{
		"idx_member_access_credential_active",
		"idx_member_access_expires",
		"idx_member_access_last_access",
		"idx_member_access_pending",
	}
	for _, idx := range indexes {
		var name string
		err := conn.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx,
		).Scan(&name)
		if err != nil {
			t.Errorf("expected index %q to exist: %v", idx, err)
		}
	}
}

// ── CreateMember ──────────────────────────────────────────────────────────────

func TestMemberAccessStore_CreateMember_InsertsRow(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "11111111-1111-4111-8111-111111111111"
	if err := ms.CreateMember(ctx, uuid, "member", "", store.ProvisioningStatusActive, nil, nil); err != nil {
		t.Fatalf("CreateMember: %v", err)
	}

	var count int
	conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM member_access WHERE uuid = ?`, uuid).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

func TestMemberAccessStore_CreateMember_DefaultsCorrect(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "22222222-2222-4222-8222-222222222222"
	_ = ms.CreateMember(ctx, uuid, "member", "operator-uuid", store.ProvisioningStatusActive, nil, nil)

	var status string
	var enabled int
	var provStatus string
	conn.QueryRowContext(ctx,
		`SELECT status, enabled, provisioning_status FROM member_access WHERE uuid = ?`, uuid,
	).Scan(&status, &enabled, &provStatus)

	if status != "active" {
		t.Errorf("expected status=active, got %q", status)
	}
	if enabled != 1 {
		t.Errorf("expected enabled=1, got %d", enabled)
	}
	if provStatus != "active" {
		t.Errorf("expected provisioning_status=active, got %q", provStatus)
	}
}

func TestMemberAccessStore_CreateMember_PendingAuthorization(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "33333333-3333-4333-8333-333333333333"
	_ = ms.CreateMember(ctx, uuid, "guest", "", store.ProvisioningStatusPendingAuthorization, nil, nil)

	var provStatus string
	conn.QueryRowContext(ctx, `SELECT provisioning_status FROM member_access WHERE uuid = ?`, uuid).Scan(&provStatus)
	if provStatus != "pending_authorization" {
		t.Errorf("expected pending_authorization, got %q", provStatus)
	}
}

func TestMemberAccessStore_CreateMember_WithExpiry(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "44444444-4444-4444-8444-444444444444"
	exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = ms.CreateMember(ctx, uuid, "guest", "", store.ProvisioningStatusActive, &exp, nil)

	var expiresMs sql.NullInt64
	conn.QueryRowContext(ctx, `SELECT expires_at_ms FROM member_access WHERE uuid = ?`, uuid).Scan(&expiresMs)
	if !expiresMs.Valid || expiresMs.Int64 != exp.UnixMilli() {
		t.Errorf("unexpected expires_at_ms: %v", expiresMs)
	}
}

// ── GetMember / GetMemberByCredential ─────────────────────────────────────────

func TestMemberAccessStore_GetMember_NotFound(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)

	_, err := ms.GetMember(context.Background(), "nonexistent-uuid")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMemberAccessStore_GetMember_RoundTrip(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "55555555-5555-4555-8555-555555555555"
	_ = ms.CreateMember(ctx, uuid, "member", "op-uuid", store.ProvisioningStatusActive, nil, nil)

	rec, err := ms.GetMember(ctx, uuid)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if rec.UUID != uuid {
		t.Errorf("UUID mismatch: %q", rec.UUID)
	}
	if rec.RoleID != "member" {
		t.Errorf("RoleID mismatch: %q", rec.RoleID)
	}
	if rec.CreatedByUUID != "op-uuid" {
		t.Errorf("CreatedByUUID mismatch: %q", rec.CreatedByUUID)
	}
}

func TestMemberAccessStore_GetMemberByCredential_Found(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "66666666-6666-4666-8666-666666666666"
	hash := randomHash(t)
	_ = ms.CreateMember(ctx, uuid, "member", "", store.ProvisioningStatusActive, nil, nil)
	_ = ms.AttachCredential(ctx, uuid, hash)

	rec, err := ms.GetMemberByCredential(ctx, hash)
	if err != nil {
		t.Fatalf("GetMemberByCredential: %v", err)
	}
	if rec.UUID != uuid {
		t.Errorf("unexpected UUID: %q", rec.UUID)
	}
}

// ── ListMembers / ListPendingAuthorizations ───────────────────────────────────

func TestMemberAccessStore_ListMembers_ReturnsAll(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuids := []string{
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
	}
	for _, id := range uuids {
		_ = ms.CreateMember(ctx, id, "member", "", store.ProvisioningStatusActive, nil, nil)
	}

	members, err := ms.ListMembers(ctx)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) < 2 {
		t.Errorf("expected at least 2 members, got %d", len(members))
	}
}

func TestMemberAccessStore_ListPendingAuthorizations_FiltersCorrectly(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	pendingID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	activeID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	_ = ms.CreateMember(ctx, pendingID, "guest", "", store.ProvisioningStatusPendingAuthorization, nil, nil)
	_ = ms.CreateMember(ctx, activeID, "member", "", store.ProvisioningStatusActive, nil, nil)

	pending, err := ms.ListPendingAuthorizations(ctx)
	if err != nil {
		t.Fatalf("ListPendingAuthorizations: %v", err)
	}
	for _, m := range pending {
		if m.ProvisioningStatus != store.ProvisioningStatusPendingAuthorization {
			t.Errorf("non-pending record in list: %q", m.UUID)
		}
	}
	found := false
	for _, m := range pending {
		if m.UUID == pendingID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected pending member %q in list", pendingID)
	}
}

// ── Status / Enabled mutations ────────────────────────────────────────────────

func TestMemberAccessStore_SetStatus(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	_ = ms.CreateMember(ctx, uuid, "member", "", store.ProvisioningStatusActive, nil, nil)
	_ = ms.SetStatus(ctx, uuid, store.MemberStatusSuspended)

	rec, _ := ms.GetMember(ctx, uuid)
	if rec.Status != store.MemberStatusSuspended {
		t.Errorf("expected suspended, got %q", rec.Status)
	}
}

func TestMemberAccessStore_SetStatus_NotFound(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)

	err := ms.SetStatus(context.Background(), "ghost-uuid", store.MemberStatusSuspended)
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMemberAccessStore_SetEnabled_False(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	_ = ms.CreateMember(ctx, uuid, "member", "", store.ProvisioningStatusActive, nil, nil)
	_ = ms.SetEnabled(ctx, uuid, false)

	rec, _ := ms.GetMember(ctx, uuid)
	if rec.Enabled {
		t.Error("expected enabled=false after SetEnabled(false)")
	}
}

// ── AttachCredential ──────────────────────────────────────────────────────────

func TestMemberAccessStore_AttachCredential_Success(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "11111111-aaaa-4aaa-8aaa-111111111111"
	hash := randomHash(t)
	_ = ms.CreateMember(ctx, uuid, "member", "", store.ProvisioningStatusActive, nil, nil)

	if err := ms.AttachCredential(ctx, uuid, hash); err != nil {
		t.Fatalf("AttachCredential: %v", err)
	}

	rec, _ := ms.GetMember(ctx, uuid)
	if string(rec.CredentialHash) != string(hash) {
		t.Error("credential_hash not stored correctly")
	}
}

func TestMemberAccessStore_AttachCredential_DuplicateConflict(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid1 := "22222222-aaaa-4aaa-8aaa-222222222222"
	uuid2 := "33333333-aaaa-4aaa-8aaa-333333333333"
	hash := randomHash(t)

	_ = ms.CreateMember(ctx, uuid1, "member", "", store.ProvisioningStatusActive, nil, nil)
	_ = ms.CreateMember(ctx, uuid2, "member", "", store.ProvisioningStatusActive, nil, nil)
	_ = ms.AttachCredential(ctx, uuid1, hash)

	err := ms.AttachCredential(ctx, uuid2, hash)
	if err != store.ErrMemberCredentialConflict {
		t.Errorf("expected ErrMemberCredentialConflict, got %v", err)
	}
}

func TestMemberAccessStore_AttachCredential_InvalidHashLength(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)

	err := ms.AttachCredential(context.Background(), "any-uuid", []byte{0x01, 0x02})
	if err == nil {
		t.Error("expected error for short hash, got nil")
	}
}

// ── AssignRole ────────────────────────────────────────────────────────────────

func TestMemberAccessStore_AssignRole(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "44444444-aaaa-4aaa-8aaa-444444444444"
	_ = ms.CreateMember(ctx, uuid, "guest", "", store.ProvisioningStatusActive, nil, nil)
	_ = ms.AssignRole(ctx, uuid, "member")

	rec, _ := ms.GetMember(ctx, uuid)
	if rec.RoleID != "member" {
		t.Errorf("expected role=member after AssignRole, got %q", rec.RoleID)
	}
}

// ── UpdateLastAccess ──────────────────────────────────────────────────────────

func TestMemberAccessStore_UpdateLastAccess(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "55555555-aaaa-4aaa-8aaa-555555555555"
	_ = ms.CreateMember(ctx, uuid, "member", "", store.ProvisioningStatusActive, nil, nil)

	accessTime := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	_ = ms.UpdateLastAccess(ctx, uuid, accessTime)

	rec, _ := ms.GetMember(ctx, uuid)
	if rec.LastAccessAt == nil {
		t.Fatal("expected last_access_at to be set")
	}
	if !rec.LastAccessAt.Equal(accessTime) {
		t.Errorf("unexpected last_access_at: %v", rec.LastAccessAt)
	}
}

// ── ArchiveMember ─────────────────────────────────────────────────────────────

func TestMemberAccessStore_ArchiveMember(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	uuid := "66666666-aaaa-4aaa-8aaa-666666666666"
	_ = ms.CreateMember(ctx, uuid, "member", "", store.ProvisioningStatusActive, nil, nil)
	_ = ms.ArchiveMember(ctx, uuid, "admin-uuid")

	rec, _ := ms.GetMember(ctx, uuid)
	if rec.Status != store.MemberStatusArchived {
		t.Errorf("expected archived status, got %q", rec.Status)
	}
	if rec.ArchivedAt == nil {
		t.Error("expected archived_at_ms to be set")
	}
	if rec.ArchivedByUUID != "admin-uuid" {
		t.Errorf("expected archived_by_uuid=admin-uuid, got %q", rec.ArchivedByUUID)
	}
}

// ── credential_hash UNIQUE constraint ────────────────────────────────────────

func TestMigration_CredentialHashUniqueConstraint(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	ms := sqlitestore.NewMemberAccessStore(conn, w)
	ctx := context.Background()

	hash := randomHash(t)
	uuid1 := "77777777-aaaa-4aaa-8aaa-777777777777"
	uuid2 := "88888888-aaaa-4aaa-8aaa-888888888888"

	_ = ms.CreateMember(ctx, uuid1, "member", "", store.ProvisioningStatusActive, nil, nil)
	_ = ms.CreateMember(ctx, uuid2, "member", "", store.ProvisioningStatusActive, nil, nil)
	_ = ms.AttachCredential(ctx, uuid1, hash)

	// Direct SQL to bypass the pre-check: the DB constraint must still fire.
	_, err := conn.ExecContext(ctx,
		`UPDATE member_access SET credential_hash = ? WHERE uuid = ?`, hash, uuid2)
	if err == nil {
		t.Error("expected UNIQUE constraint violation when assigning same hash to second member")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func randomHash(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("randomHash: %v", err)
	}
	return b
}
