package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type SeedDevOptions struct {
	// Optional: you can pass your config-known modules here to pre-create them in dev.
	KnownModules []string
}

func SeedDev(ctx context.Context, db *sql.DB, opt SeedDevOptions) error {
	now := time.Now().UTC().UnixMilli()

	// Minimal “starter door”
	if _, err := db.ExecContext(ctx, `
INSERT OR IGNORE INTO doors(door_id, name, location, created_at_ms, updated_at_ms)
VALUES ('door_main', 'Main Door', 'Dev', ?, ?);`, now, now); err != nil {
		return fmt.Errorf("seed doors: %w", err)
	}

	// Seed known modules (optional).
	// for _, mid := range opt.KnownModules {
	// 	mid = strings.TrimSpace(mid)
	// 	if mid == "" {
	// 		continue
	// 	}

	if _, err := db.ExecContext(ctx, `
INSERT INTO modules(
  module_id, door_id, display_name,
  enabled, commissioned_at_ms,
  created_at_ms, updated_at_ms
) VALUES ('door-001', 'door_main', 'Main Entrance', 1, ?, ?, ?)
ON CONFLICT(module_id) DO UPDATE SET
  door_id = excluded.door_id,
  display_name = excluded.display_name,
  enabled = 1,
  commissioned_at_ms = COALESCE(modules.commissioned_at_ms, excluded.commissioned_at_ms),
  updated_at_ms = excluded.updated_at_ms;
`, now, now, now); err != nil {
		return fmt.Errorf("seed module door-001: %w", err)
	}

	return nil
}
