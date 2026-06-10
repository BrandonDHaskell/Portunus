package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

// ExpiryWorker periodically transitions member_access rows based on three
// independent policy axes:
//   - Hard deadline: expires_at_ms has passed.
//   - Inactivity:    last_access_at_ms (or created_at_ms) + inactivity_limit_days has passed.
//   - Stale pending: pending_authorization rows older than pendingTTLDays are archived.
//
// Modeled after HeartbeatPruner.
type ExpiryWorker struct {
	store          store.MemberAccessStore
	auditStore     store.AuditStore // may be nil; audit writes are best-effort
	interval       time.Duration
	pendingTTLDays int
	logger         *log.Logger
	cancel         context.CancelFunc
	done           chan struct{}
}

// ExpiryWorkerConfig holds configuration for NewExpiryWorker.
type ExpiryWorkerConfig struct {
	// IntervalMinutes is how often the worker runs. Defaults to 60.
	IntervalMinutes int
	// PendingTTLDays is how many days before stale pending_authorization rows
	// are archived. 0 disables the sweep. Defaults to 7.
	PendingTTLDays int
}

// NewExpiryWorker creates a worker but does not start it.
// Call Start to begin the background loop.
func NewExpiryWorker(s store.MemberAccessStore, audit store.AuditStore, cfg ExpiryWorkerConfig, logger *log.Logger) *ExpiryWorker {
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = 60 * time.Minute
	}
	ttl := cfg.PendingTTLDays
	if ttl < 0 {
		ttl = 0
	}
	return &ExpiryWorker{
		store:          s,
		auditStore:     audit,
		interval:       interval,
		pendingTTLDays: ttl,
		logger:         logger,
		done:           make(chan struct{}),
	}
}

// Start begins the background expiry loop. Runs an immediate sweep on startup,
// then repeats on the configured interval. The loop exits when ctx is cancelled
// or Stop is called.
func (w *ExpiryWorker) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)
	go w.loop(ctx)
	w.logger.Printf("expiry worker started (interval=%dm, pendingTTL=%dd)", int(w.interval.Minutes()), w.pendingTTLDays)
}

// Stop signals the worker to exit and waits for it to finish.
func (w *ExpiryWorker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	<-w.done
}

func (w *ExpiryWorker) loop(ctx context.Context) {
	defer close(w.done)

	w.sweep(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

func (w *ExpiryWorker) sweep(ctx context.Context) {
	now := time.Now().UTC()

	n, err := w.store.ExpireByHardDeadline(ctx, now)
	if err != nil {
		w.logger.Printf("expiry worker: hard-deadline sweep error: %v", err)
	} else if n > 0 {
		w.logger.Printf("expiry worker: expired %d member(s) by hard deadline", n)
	}

	n, err = w.store.ExpireByInactivity(ctx, now)
	if err != nil {
		w.logger.Printf("expiry worker: inactivity sweep error: %v", err)
	} else if n > 0 {
		w.logger.Printf("expiry worker: expired %d member(s) by inactivity", n)
	}

	if w.pendingTTLDays == 0 {
		return
	}

	archived, err := w.store.ArchiveStalePending(ctx, now, w.pendingTTLDays)
	if err != nil {
		w.logger.Printf("expiry worker: pending-TTL sweep error: %v", err)
		return
	}
	if archived == 0 {
		return
	}
	w.logger.Printf("expiry worker: archived %d stale pending member(s)", archived)
	if w.auditStore != nil {
		entry := store.AuditEntry{
			ActorType:    store.ActorTypeSystem,
			Action:       "archive_stale_pending",
			ResourceType: "member_access",
			Details:      fmt.Sprintf(`{"count":%d,"ttl_days":%d}`, archived, w.pendingTTLDays),
			Result:       "success",
		}
		if err := w.auditStore.RecordAuditEntry(ctx, entry); err != nil {
			w.logger.Printf("expiry worker: audit write error: %v", err)
		}
	}
}
