package service

import (
	"context"
	"log"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

// HeartbeatPruner periodically deletes heartbeat records older than a
// configurable retention period.  It runs as a background goroutine and
// is safe to stop via its context or the Stop method.
//
// A retention of 0 disables pruning entirely.
type HeartbeatPruner struct {
	store     store.HeartbeatStore
	retention time.Duration
	interval  time.Duration
	logger    *log.Logger
	cancel    context.CancelFunc
	done      chan struct{}
}

// PrunerConfig holds the parameters for NewHeartbeatPruner.
type PrunerConfig struct {
	// RetentionDays is how many days of heartbeat history to keep.
	// 0 means keep everything (pruner will not start).
	RetentionDays int

	// IntervalHours is how often the pruner runs.  Defaults to 6.
	IntervalHours int
}

// NewHeartbeatPruner creates a pruner but does not start it.
// Call Start to begin the background loop.
func NewHeartbeatPruner(s store.HeartbeatStore, cfg PrunerConfig, logger *log.Logger) *HeartbeatPruner {
	interval := time.Duration(cfg.IntervalHours) * time.Hour
	if interval <= 0 {
		interval = 6 * time.Hour
	}

	return &HeartbeatPruner{
		store:     s,
		retention: time.Duration(cfg.RetentionDays) * 24 * time.Hour,
		interval:  interval,
		logger:    logger,
		done:      make(chan struct{}),
	}
}

// Start begins the background pruning loop.  It runs an immediate prune
// on startup, then repeats on the configured interval.  The loop exits
// when ctx is cancelled or Stop is called.
func (p *HeartbeatPruner) Start(ctx context.Context) {
	if p.retention <= 0 {
		p.logger.Printf("heartbeat pruner disabled (retention=0)")
		close(p.done)
		return
	}

	ctx, p.cancel = context.WithCancel(ctx)

	go p.loop(ctx)

	p.logger.Printf("heartbeat pruner started (retention=%dd, interval=%dh)",
		int(p.retention.Hours()/24), int(p.interval.Hours()))
}

// Stop signals the pruner to exit and waits for it to finish.
func (p *HeartbeatPruner) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	<-p.done
}

func (p *HeartbeatPruner) loop(ctx context.Context) {
	defer close(p.done)

	// Run immediately on startup to clean up any backlog.
	p.prune(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.prune(ctx)
		}
	}
}

func (p *HeartbeatPruner) prune(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-p.retention)
	deleted, err := p.store.PruneOlderThan(ctx, cutoff)
	if err != nil {
		p.logger.Printf("heartbeat prune error: %v", err)
		return
	}
	if deleted > 0 {
		p.logger.Printf("heartbeat prune: deleted %d rows older than %s",
			deleted, cutoff.Format(time.RFC3339))
	}
}
