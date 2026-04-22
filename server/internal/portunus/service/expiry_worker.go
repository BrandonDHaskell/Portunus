package service

import (
	"context"
	"log"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

// ExpiryWorker periodically transitions member_access rows to 'expired' status
// based on two independent policy axes:
//   - Hard deadline: expires_at_ms has passed.
//   - Inactivity:    last_access_at_ms (or created_at_ms) + inactivity_limit_days has passed.
//
// Modeled after HeartbeatPruner.
type ExpiryWorker struct {
	store    store.MemberAccessStore
	interval time.Duration
	logger   *log.Logger
	cancel   context.CancelFunc
	done     chan struct{}
}

// ExpiryWorkerConfig holds configuration for NewExpiryWorker.
type ExpiryWorkerConfig struct {
	// IntervalMinutes is how often the worker runs. Defaults to 60.
	IntervalMinutes int
}

// NewExpiryWorker creates a worker but does not start it.
// Call Start to begin the background loop.
func NewExpiryWorker(s store.MemberAccessStore, cfg ExpiryWorkerConfig, logger *log.Logger) *ExpiryWorker {
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = 60 * time.Minute
	}
	return &ExpiryWorker{
		store:    s,
		interval: interval,
		logger:   logger,
		done:     make(chan struct{}),
	}
}

// Start begins the background expiry loop. Runs an immediate sweep on startup,
// then repeats on the configured interval. The loop exits when ctx is cancelled
// or Stop is called.
func (w *ExpiryWorker) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)
	go w.loop(ctx)
	w.logger.Printf("expiry worker started (interval=%dm)", int(w.interval.Minutes()))
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
}
