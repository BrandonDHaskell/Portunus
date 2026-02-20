package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/BrandonDHaskell/Portunus/server/internal/db"
)

// openTestDB returns an in-memory SQLite connection with the same PRAGMAs
// and schema as production.  The connection is closed automatically when the
// test finishes.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	// Each call gets a unique in-memory database.  The shared-cache URI
	// keeps the database alive for the lifetime of the connection pool
	// (important because sql.DB may close/reopen the underlying conn).
	dsn := fmt.Sprintf(
		"file:test_%s?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)",
		t.Name(),
	)

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("openTestDB: sql.Open: %v", err)
	}

	// Match production: single connection for SQLite safety.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(0)

	if err := conn.Ping(); err != nil {
		conn.Close()
		t.Fatalf("openTestDB: ping: %v", err)
	}

	// Apply the same migrations as production.
	if err := db.Migrate(context.Background(), conn); err != nil {
		conn.Close()
		t.Fatalf("openTestDB: migrate: %v", err)
	}

	t.Cleanup(func() { conn.Close() })
	return conn
}

// newTestWriter returns a db.Worker backed by conn.  The worker is closed
// automatically when the test finishes.
func newTestWriter(t *testing.T, conn *sql.DB) *db.Worker {
	t.Helper()

	w := db.NewWorker(conn)
	t.Cleanup(func() { w.Close() })
	return w
}
