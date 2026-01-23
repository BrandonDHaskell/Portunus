package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

func Migrate(ctx context.Context, db *sql.DB) error {
	// Ensure migration tracking exists (outside versioned migrations so it's always available).
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at_ms INTEGER NOT NULL
);`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var ms []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		v, err := parseVersion(e.Name()) // e.g. 0001_init.sql -> 1
		if err != nil {
			return err
		}
		b, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		ms = append(ms, migration{
			version: v,
			name:    e.Name(),
			sql:     string(b),
		})
	}

	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })

	for _, m := range ms {
		applied, err := isApplied(ctx, db, m.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}

		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}

		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, applied_at_ms) VALUES(?, ?);",
			m.version, time.Now().UTC().UnixMilli(),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", m.name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", m.name, err)
		}
	}

	return nil
}

func isApplied(ctx context.Context, db *sql.DB, version int) (bool, error) {
	var v int
	err := db.QueryRowContext(ctx, "SELECT version FROM schema_migrations WHERE version = ?;", version).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check migration %d: %w", version, err)
	}
	return true, nil
}

func parseVersion(filename string) (int, error) {
	parts := strings.SplitN(filename, "_", 2)
	if len(parts) < 1 {
		return 0, fmt.Errorf("bad migration filename: %s", filename)
	}
	s := strings.TrimLeft(parts[0], "0")
	if s == "" {
		s = "0"
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad migration version %s: %w", filename, err)
	}
	return v, nil
}
