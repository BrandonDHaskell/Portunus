package db

import (
	"context"
	"database/sql"
)

type TxFn func(ctx context.Context, tx *sql.Tx) error

type job struct {
	ctx context.Context
	fn  TxFn
	ch  chan error
}

type Worker struct {
	db   *sql.DB
	jobs chan job
	done chan struct{}
}

func NewWorker(db *sql.DB) *Worker {
	w := &Worker{
		db:   db,
		jobs: make(chan job, 256),
		done: make(chan struct{}),
	}
	go w.loop()
	return w
}

func (w *Worker) Close() {
	close(w.jobs)
	<-w.done
}

func (w *Worker) Do(ctx context.Context, fn TxFn) error {
	ch := make(chan error, 1)
	w.jobs <- job{ctx: ctx, fn: fn, ch: ch}
	return <-ch
}

func (w *Worker) loop() {
	defer close(w.done)

	for j := range w.jobs {
		tx, err := w.db.BeginTx(j.ctx, nil)
		if err != nil {
			j.ch <- err
			continue
		}

		if err := j.fn(j.ctx, tx); err != nil {
			_ = tx.Rollback()
			j.ch <- err
			continue
		}

		j.ch <- tx.Commit()
	}
}
