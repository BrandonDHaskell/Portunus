package service

import (
	"context"
	"log"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

// SessionSweeper periodically deletes expired session rows.  It is safe to
// stop via its context or the Stop method.
//
// Expired sessions are already rejected at resolve time; this sweeper only
// prevents unbounded table growth for sessions that are never resolved again.
type SessionSweeper struct {
	store    store.SessionStore
	interval time.Duration
	logger   *log.Logger
	bg       bgLoop
}

// NewSessionSweeper creates a sweeper that runs on the given interval.
// If interval ≤ 0, it defaults to 1 hour.
func NewSessionSweeper(s store.SessionStore, interval time.Duration, logger *log.Logger) *SessionSweeper {
	if interval <= 0 {
		interval = time.Hour
	}
	return &SessionSweeper{
		store:    s,
		interval: interval,
		logger:   logger,
	}
}

// Start begins the background sweep loop.
func (sw *SessionSweeper) Start(ctx context.Context) {
	if !sw.bg.start(ctx, sw.loop) {
		return
	}
	sw.logger.Printf("session sweeper started (interval=%s)", sw.interval)
}

// Stop signals the sweeper to exit and waits for it to finish. It is a no-op
// if Start was never called, and safe to call more than once.
func (sw *SessionSweeper) Stop() {
	sw.bg.stop()
}

func (sw *SessionSweeper) loop(ctx context.Context) {
	sw.sweep(ctx)

	ticker := time.NewTicker(sw.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sw.sweep(ctx)
		}
	}
}

func (sw *SessionSweeper) sweep(ctx context.Context) {
	if err := sw.store.DeleteExpiredSessions(ctx); err != nil {
		sw.logger.Printf("session sweep error: %v", err)
	}
}
