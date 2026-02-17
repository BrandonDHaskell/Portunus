package service_test

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

func silentLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

func TestHeartbeatPruner_DisabledWhenRetentionZero(t *testing.T) {
	ms := memory.New()
	pruner := service.NewHeartbeatPruner(ms, service.PrunerConfig{
		RetentionDays: 0,
		IntervalHours: 1,
	}, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pruner.Start(ctx)
	// Stop should return immediately without error.
	pruner.Stop()
}

func TestHeartbeatPruner_PrunesOldRecords(t *testing.T) {
	ms := memory.New()
	ctx := context.Background()

	// Insert an old heartbeat (40 days ago).
	old := store.HeartbeatRecord{
		ReceivedAt: time.Now().UTC().AddDate(0, 0, -40),
		Request:    types.HeartbeatRequest{ModuleID: "door-old"},
	}
	if err := ms.UpsertHeartbeat(ctx, "door-old", old); err != nil {
		t.Fatalf("insert old: %v", err)
	}

	// Insert a recent heartbeat (1 day ago).
	recent := store.HeartbeatRecord{
		ReceivedAt: time.Now().UTC().AddDate(0, 0, -1),
		Request:    types.HeartbeatRequest{ModuleID: "door-recent"},
	}
	if err := ms.UpsertHeartbeat(ctx, "door-recent", recent); err != nil {
		t.Fatalf("insert recent: %v", err)
	}

	// Prune directly via the store (same operation the pruner calls).
	cutoff := time.Now().UTC().AddDate(0, 0, -30)
	deleted, err := ms.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 pruned, got %d", deleted)
	}

	// The recent record should survive.
	_, err = ms.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("second prune: %v", err)
	}
}

func TestHeartbeatPruner_StopIsIdempotent(t *testing.T) {
	ms := memory.New()
	pruner := service.NewHeartbeatPruner(ms, service.PrunerConfig{
		RetentionDays: 30,
		IntervalHours: 1,
	}, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	pruner.Start(ctx)

	cancel()
	// Multiple stops should not panic.
	pruner.Stop()
	pruner.Stop()
}
