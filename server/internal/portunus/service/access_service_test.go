package service_test

import (
	"context"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// newTestAccessService builds an AccessService backed by in-memory stores,
// returning the service and the event store so tests can inspect recorded events.
func newTestAccessService(
	knownModules []string,
	policy service.AccessPolicy,
) (*service.AccessService, *memory.AccessEventStore) {
	deviceStore := memory.NewDeviceStore(knownModules)
	registry := service.NewDeviceRegistry(deviceStore)
	eventStore := memory.NewAccessEventStore()
	svc := service.NewAccessService(registry, policy, eventStore)
	return svc, eventStore
}

// ── Event recording ──────────────────────────────────────────────────────────

func TestDecide_AllowAll_RecordsGrantEvent(t *testing.T) {
	svc, es := newTestAccessService(
		[]string{"door-001"},
		service.AccessPolicy{AllowAll: true},
	)

	_, err := svc.Decide(context.Background(), types.AccessRequest{
		ModuleID: "door-001",
		CardID:   "AABBCCDD",
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	events := es.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	ev := events[0]
	if ev.ModuleID != "door-001" {
		t.Errorf("expected module_id=door-001, got %q", ev.ModuleID)
	}
	if !ev.Granted {
		t.Error("expected granted=true")
	}
	if ev.Reason != "allow_all" {
		t.Errorf("expected reason=allow_all, got %q", ev.Reason)
	}
	if ev.DecidedAt.IsZero() {
		t.Error("expected decided_at to be set")
	}
}

func TestDecide_CardAllowed_RecordsGrantEvent(t *testing.T) {
	allowed := map[string]struct{}{"AABBCCDD": {}}
	svc, es := newTestAccessService(
		[]string{"door-001"},
		service.AccessPolicy{AllowedCardIDs: allowed},
	)

	_, err := svc.Decide(context.Background(), types.AccessRequest{
		ModuleID: "door-001",
		CardID:   "AABBCCDD",
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	events := es.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if !events[0].Granted {
		t.Error("expected granted=true for allowed card")
	}
	if events[0].Reason != "card_allowed" {
		t.Errorf("expected reason=card_allowed, got %q", events[0].Reason)
	}
}

func TestDecide_CardDenied_RecordsDenyEvent(t *testing.T) {
	allowed := map[string]struct{}{"AABBCCDD": {}}
	svc, es := newTestAccessService(
		[]string{"door-001"},
		service.AccessPolicy{AllowedCardIDs: allowed},
	)

	_, err := svc.Decide(context.Background(), types.AccessRequest{
		ModuleID: "door-001",
		CardID:   "UNKNOWN_CARD",
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	events := es.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Granted {
		t.Error("expected granted=false for denied card")
	}
	if events[0].Reason != "card_not_allowed" {
		t.Errorf("expected reason=card_not_allowed, got %q", events[0].Reason)
	}
}

func TestDecide_UnknownModule_RecordsDenyEvent(t *testing.T) {
	svc, es := newTestAccessService(
		[]string{"door-001"},
		service.AccessPolicy{AllowAll: true},
	)

	resp, err := svc.Decide(context.Background(), types.AccessRequest{
		ModuleID: "rogue-device",
		CardID:   "AABBCCDD",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Known {
		t.Error("expected known=false for unknown module")
	}
	if resp.Granted {
		t.Error("expected granted=false for unknown module")
	}

	events := es.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event even for unknown module, got %d", len(events))
	}
	if events[0].Granted {
		t.Error("expected granted=false for unknown module")
	}
	if events[0].Reason != "unknown_module" {
		t.Errorf("expected reason=unknown_module, got %q", events[0].Reason)
	}
}

func TestDecide_DoorClosedPassedThrough(t *testing.T) {
	svc, es := newTestAccessService(
		[]string{"door-001"},
		service.AccessPolicy{AllowAll: true},
	)

	closed := true
	_, err := svc.Decide(context.Background(), types.AccessRequest{
		ModuleID:   "door-001",
		CardID:     "AABBCCDD",
		DoorClosed: &closed,
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	events := es.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].DoorClosed == nil || !*events[0].DoorClosed {
		t.Error("expected door_closed=true in recorded event")
	}
}

func TestDecide_RequestedAtParsed(t *testing.T) {
	svc, es := newTestAccessService(
		[]string{"door-001"},
		service.AccessPolicy{AllowAll: true},
	)

	_, err := svc.Decide(context.Background(), types.AccessRequest{
		ModuleID:    "door-001",
		CardID:      "AABBCCDD",
		RequestedAt: "2026-02-15T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	events := es.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].RequestedAt == nil {
		t.Fatal("expected requested_at to be parsed")
	}
	if events[0].RequestedAt.Year() != 2026 || events[0].RequestedAt.Month() != 2 {
		t.Errorf("unexpected parsed time: %v", events[0].RequestedAt)
	}
}

func TestDecide_MultipleDecisions_AllRecorded(t *testing.T) {
	svc, es := newTestAccessService(
		[]string{"door-001"},
		service.AccessPolicy{AllowAll: true},
	)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, _ = svc.Decide(ctx, types.AccessRequest{
			ModuleID: "door-001",
			CardID:   "AABBCCDD",
		})
	}

	events := es.Events()
	if len(events) != 5 {
		t.Errorf("expected 5 events, got %d", len(events))
	}
}

// ── Validation (no event should be recorded) ─────────────────────────────────

func TestDecide_MissingModuleID_NoEventRecorded(t *testing.T) {
	svc, es := newTestAccessService(nil, service.AccessPolicy{})

	_, err := svc.Decide(context.Background(), types.AccessRequest{
		CardID: "AABBCCDD",
	})
	if err == nil {
		t.Fatal("expected error for missing module_id")
	}

	if len(es.Events()) != 0 {
		t.Error("expected no event for validation failure")
	}
}

func TestDecide_MissingCardID_NoEventRecorded(t *testing.T) {
	svc, es := newTestAccessService([]string{"door-001"}, service.AccessPolicy{})

	_, err := svc.Decide(context.Background(), types.AccessRequest{
		ModuleID: "door-001",
	})
	if err == nil {
		t.Fatal("expected error for missing card_id")
	}

	if len(es.Events()) != 0 {
		t.Error("expected no event for validation failure")
	}
}
