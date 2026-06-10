package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

func newAuditStore(t *testing.T) *sqlitestore.AuditStore {
	t.Helper()
	conn := openTestDB(t)
	w := newTestWriter(t, conn)
	return sqlitestore.NewAuditStore(conn, w)
}

// ── RecordAuditEntry ──────────────────────────────────────────────────────────

func TestAuditStore_RecordAuditEntry_FullEntry(t *testing.T) {
	s := newAuditStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Millisecond)
	entry := store.AuditEntry{
		ID:           "test-audit-id-001",
		OccurredAt:   now,
		ActorUUID:    "admin-uuid-001",
		ActorType:    store.ActorTypeAdmin,
		Action:       "member.approve",
		ResourceType: "member",
		ResourceID:   "member-uuid-001",
		Details:      "approved via UI",
		IPAddress:    "192.168.1.10",
		Result:       "success",
	}

	if err := s.RecordAuditEntry(ctx, entry); err != nil {
		t.Fatalf("RecordAuditEntry: %v", err)
	}

	entries, err := s.ListAuditEntries(ctx, 10)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	got := entries[0]
	if got.ID != entry.ID {
		t.Errorf("ID = %q, want %q", got.ID, entry.ID)
	}
	if got.ActorUUID != entry.ActorUUID {
		t.Errorf("ActorUUID = %q, want %q", got.ActorUUID, entry.ActorUUID)
	}
	if got.ActorType != entry.ActorType {
		t.Errorf("ActorType = %q, want %q", got.ActorType, entry.ActorType)
	}
	if got.Action != entry.Action {
		t.Errorf("Action = %q, want %q", got.Action, entry.Action)
	}
	if got.ResourceType != entry.ResourceType {
		t.Errorf("ResourceType = %q, want %q", got.ResourceType, entry.ResourceType)
	}
	if got.ResourceID != entry.ResourceID {
		t.Errorf("ResourceID = %q, want %q", got.ResourceID, entry.ResourceID)
	}
	if got.Details != entry.Details {
		t.Errorf("Details = %q, want %q", got.Details, entry.Details)
	}
	if got.IPAddress != entry.IPAddress {
		t.Errorf("IPAddress = %q, want %q", got.IPAddress, entry.IPAddress)
	}
	if got.Result != entry.Result {
		t.Errorf("Result = %q, want %q", got.Result, entry.Result)
	}
	diff := got.OccurredAt.Sub(now)
	if diff < -time.Millisecond || diff > time.Millisecond {
		t.Errorf("OccurredAt = %v, want ~%v", got.OccurredAt, now)
	}
}

func TestAuditStore_RecordAuditEntry_DefaultsFilled(t *testing.T) {
	s := newAuditStore(t)
	ctx := context.Background()

	before := time.Now().UTC()
	entry := store.AuditEntry{
		Action: "member.enroll",
	}
	if err := s.RecordAuditEntry(ctx, entry); err != nil {
		t.Fatalf("RecordAuditEntry: %v", err)
	}
	after := time.Now().UTC()

	entries, err := s.ListAuditEntries(ctx, 1)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected 1 entry")
	}

	got := entries[0]
	if got.ID == "" {
		t.Error("expected auto-generated ID, got empty string")
	}
	if got.ActorType != store.ActorTypeSystem {
		t.Errorf("ActorType = %q, want system", got.ActorType)
	}
	if got.Result != "success" {
		t.Errorf("Result = %q, want success", got.Result)
	}
	if got.OccurredAt.Before(before.Truncate(time.Millisecond)) || got.OccurredAt.After(after.Add(time.Second)) {
		t.Errorf("OccurredAt = %v, want between ~%v and ~%v", got.OccurredAt, before, after)
	}
}

func TestAuditStore_RecordAuditEntry_NullableFieldsEmpty(t *testing.T) {
	s := newAuditStore(t)
	ctx := context.Background()

	entry := store.AuditEntry{
		Action:    "module.commission",
		ActorType: store.ActorTypeAdmin,
	}
	if err := s.RecordAuditEntry(ctx, entry); err != nil {
		t.Fatalf("RecordAuditEntry: %v", err)
	}

	entries, err := s.ListAuditEntries(ctx, 1)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected 1 entry")
	}
	// Nullable fields that were empty should come back as empty strings (via COALESCE).
	got := entries[0]
	if got.ActorUUID != "" {
		t.Errorf("ActorUUID = %q, want empty", got.ActorUUID)
	}
	if got.ResourceType != "" {
		t.Errorf("ResourceType = %q, want empty", got.ResourceType)
	}
	if got.IPAddress != "" {
		t.Errorf("IPAddress = %q, want empty", got.IPAddress)
	}
}

// ── ListAuditEntries ──────────────────────────────────────────────────────────

func TestAuditStore_ListAuditEntries_NewestFirst(t *testing.T) {
	s := newAuditStore(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 3; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		e := store.AuditEntry{
			ID:         "ordered-" + string(rune('A'+i)),
			OccurredAt: ts,
			Action:     "test.action",
			ActorType:  store.ActorTypeSystem,
			Result:     "success",
		}
		if err := s.RecordAuditEntry(ctx, e); err != nil {
			t.Fatalf("RecordAuditEntry[%d]: %v", i, err)
		}
	}

	entries, err := s.ListAuditEntries(ctx, 10)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// Newest first: entries[0] should have the latest OccurredAt.
	if !entries[0].OccurredAt.After(entries[1].OccurredAt) {
		t.Errorf("entries not in newest-first order: [0]=%v [1]=%v", entries[0].OccurredAt, entries[1].OccurredAt)
	}
	if !entries[1].OccurredAt.After(entries[2].OccurredAt) {
		t.Errorf("entries not in newest-first order: [1]=%v [2]=%v", entries[1].OccurredAt, entries[2].OccurredAt)
	}
}

func TestAuditStore_ListAuditEntries_LimitRespected(t *testing.T) {
	s := newAuditStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := s.RecordAuditEntry(ctx, store.AuditEntry{Action: "test.action"}); err != nil {
			t.Fatalf("RecordAuditEntry[%d]: %v", i, err)
		}
	}

	entries, err := s.ListAuditEntries(ctx, 2)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries with limit=2, got %d", len(entries))
	}
}

func TestAuditStore_ListAuditEntries_DefaultLimit(t *testing.T) {
	s := newAuditStore(t)
	ctx := context.Background()

	// limit <= 0 should default to 100 without error.
	entries, err := s.ListAuditEntries(ctx, 0)
	if err != nil {
		t.Fatalf("ListAuditEntries with limit=0: %v", err)
	}
	if entries == nil {
		entries = []store.AuditEntry{}
	}
	// Nothing inserted, so empty is fine.
	_ = entries
}

func TestAuditStore_ListAuditEntries_Empty(t *testing.T) {
	s := newAuditStore(t)
	entries, err := s.ListAuditEntries(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListAuditEntries on empty table: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}
