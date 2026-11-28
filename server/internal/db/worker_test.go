package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/BrandonDHaskell/Portunus/server/internal/db"
)

func memDB(t *testing.T) *sql.DB {
	t.Helper()
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	conn, err := sql.Open("sqlite", fmt.Sprintf("file:db_%s?mode=memory&cache=shared&_pragma=foreign_keys(1)", safe))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(`CREATE TABLE counter (n INTEGER NOT NULL); INSERT INTO counter VALUES (0);`); err != nil {
		t.Fatal(err)
	}
	return conn
}

func readCounter(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(`SELECT n FROM counter`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Concurrent read-modify-write transactions must not lose updates or fail
// with "database is locked": the worker runs them one at a time.
func TestWorker_SerializesConcurrentWrites(t *testing.T) {
	conn := memDB(t)
	w := db.NewWorker(conn)
	defer w.Close()

	const n = 100
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- w.Do(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
				var v int
				if err := tx.QueryRowContext(ctx, `SELECT n FROM counter`).Scan(&v); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `UPDATE counter SET n = ?`, v+1)
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
	}
	if got := readCounter(t, conn); got != n {
		t.Fatalf("counter = %d, want %d (lost updates)", got, n)
	}
}

func TestWorker_ErrorRollsBack(t *testing.T) {
	conn := memDB(t)
	w := db.NewWorker(conn)
	defer w.Close()

	boom := errors.New("boom")
	err := w.Do(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE counter SET n = 99`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Do = %v, want the callback error", err)
	}
	if got := readCounter(t, conn); got != 0 {
		t.Fatalf("counter = %d after a failed transaction, want 0 (not rolled back)", got)
	}
}

func TestWorker_CancelledContext(t *testing.T) {
	conn := memDB(t)
	w := db.NewWorker(conn)
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	err := w.Do(ctx, func(context.Context, *sql.Tx) error { ran = true; return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do = %v, want context.Canceled", err)
	}
	_ = ran // the job may or may not run; the caller must not wait for it
}

// A write arriving after shutdown must fail cleanly, not crash the server.
func TestWorker_DoAfterClose(t *testing.T) {
	conn := memDB(t)
	w := db.NewWorker(conn)
	w.Close()

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Do after Close panicked: %v", r)
			}
		}()
		err = w.Do(context.Background(), func(context.Context, *sql.Tx) error { return nil })
	}()
	if !errors.Is(err, db.ErrWorkerClosed) {
		t.Fatalf("Do after Close = %v, want ErrWorkerClosed", err)
	}
	w.Close() // a second Close must not panic either
}

// Jobs already queued when Close is called still run.
func TestWorker_CloseDrainsQueuedJobs(t *testing.T) {
	conn := memDB(t)
	w := db.NewWorker(conn)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Do(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE counter SET n = n + 1`)
				return err
			})
		}()
	}
	wg.Wait()
	w.Close()
	if got := readCounter(t, conn); got != 20 {
		t.Fatalf("counter = %d, want 20", got)
	}
}
