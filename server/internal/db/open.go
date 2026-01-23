package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Path string // e.g. "./data/portunus.db"
	Env  string // "dev" | "prod"
}

func Open(ctx context.Context, cfg Config) (*sql.DB, error) {
	if cfg.Path == "" {
		cfg.Path = "./data/portunus.db"
	}
	if cfg.Env == "" {
		cfg.Env = "dev"
	}

	// Ensure DB parent directory exists.
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir db dir: %w", err)
	}

	// modernc.org/sqlite DSN with per-connection PRAGMAs.
	// These are good defaults for a single-process server:
	// - foreign_keys ON
	// - WAL for better concurrency
	// - synchronous NORMAL for performance with good safety
	// - busy_timeout to reduce SQLITE_BUSY under load
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)",
		cfg.Path,
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}

	// Strong safety for SQLite in servers: single connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	// Validate connection early.
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}

	// Apply migrations.
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}
