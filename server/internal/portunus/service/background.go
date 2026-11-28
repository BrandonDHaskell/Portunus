package service

import (
	"context"
	"sync"
)

// bgLoop runs one background goroutine with a safe lifecycle: Stop before
// Start is a no-op, Stop is idempotent, and a second Start does nothing.
// Shutdown code can therefore call Stop unconditionally without hanging.
type bgLoop struct {
	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// start launches run in a goroutine unless the loop was already started.
// It reports whether it started the loop.
func (b *bgLoop) start(ctx context.Context, run func(context.Context)) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return false
	}
	b.started = true
	ctx, b.cancel = context.WithCancel(ctx)
	b.done = make(chan struct{})
	go func() {
		defer close(b.done)
		run(ctx)
	}()
	return true
}

// stop cancels the loop and waits for it to exit. It returns at once if the
// loop never started.
func (b *bgLoop) stop() {
	b.mu.Lock()
	cancel, done := b.cancel, b.done
	b.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}
