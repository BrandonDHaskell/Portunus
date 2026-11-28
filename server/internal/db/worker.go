package db

import (
	"context"
	"database/sql"
	"errors"
	"sync"
)

// ErrWorkerClosed is returned by Do once Close has been called.
var ErrWorkerClosed = errors.New("db worker is closed")

type TxFn func(ctx context.Context, tx *sql.Tx) error

type job struct {
	ctx context.Context
	fn  TxFn
	ch  chan error
}

// Worker serializes all writes through one goroutine so SQLite never sees two
// concurrent writers ("database is locked").
type Worker struct {
	db        *sql.DB
	jobs      chan job
	quit      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func NewWorker(db *sql.DB) *Worker {
	w := &Worker{
		db:   db,
		jobs: make(chan job, 256),
		quit: make(chan struct{}),
		done: make(chan struct{}),
	}
	go w.loop()
	return w
}

// Close stops accepting writes, runs the jobs already queued, and waits for
// the worker to exit. It is safe to call more than once. The jobs channel is
// never closed, so a Do racing Close gets ErrWorkerClosed instead of a panic.
func (w *Worker) Close() {
	w.closeOnce.Do(func() { close(w.quit) })
	<-w.done
}

func (w *Worker) Do(ctx context.Context, fn TxFn) error {
	select {
	case <-w.quit:
		return ErrWorkerClosed
	default:
	}

	ch := make(chan error, 1)
	j := job{ctx: ctx, fn: fn, ch: ch}

	// Enqueue — bail out if the caller's context expires while the buffer is
	// full, or if the worker shuts down.
	select {
	case w.jobs <- j:
	case <-ctx.Done():
		return ctx.Err()
	case <-w.quit:
		return ErrWorkerClosed
	}

	// Wait for the result — bail out if the caller's context expires while the
	// job is queued or executing. The worker still completes the transaction;
	// the result lands in the buffered ch and is discarded.
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-w.done:
		// The worker exited. The job either ran before it did (result is
		// waiting) or was enqueued too late and never will.
		select {
		case err := <-ch:
			return err
		default:
			return ErrWorkerClosed
		}
	}
}

func (w *Worker) loop() {
	defer close(w.done)
	for {
		select {
		case j := <-w.jobs:
			w.run(j)
		case <-w.quit:
			// Drain what was queued before Close, then exit.
			for {
				select {
				case j := <-w.jobs:
					w.run(j)
				default:
					return
				}
			}
		}
	}
}

func (w *Worker) run(j job) {
	tx, err := w.db.BeginTx(j.ctx, nil)
	if err != nil {
		j.ch <- err
		return
	}
	if err := j.fn(j.ctx, tx); err != nil {
		_ = tx.Rollback()
		j.ch <- err
		return
	}
	j.ch <- tx.Commit()
}
