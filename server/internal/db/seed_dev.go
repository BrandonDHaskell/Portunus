package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type SeedDevOptions struct {
	// Optional: you can pass your config-known modules here to pre-create them in dev.
	KnownModules []string
}

func SeedDev(ctx context.Context, db *sql.DB, opt SeedDevOptions) error {
	now := time.Now().UTC().UnixMilli()

	// Minimal starter door -- always created so modules have something to attach to.
	if _, err := db.ExecContext(ctx, `
INSERT OR IGNORE INTO doors(door_id, name, location, created_at_ms, updated_at_ms)
VALUES ('door_main', 'Main Door', 'Dev', ?, ?);`, now, now); err != nil {
		return fmt.Errorf("seed doors: %w", err)
	}

	// Default to "door-001" if no modules are configured, so dev mode always
	// has at least one commissioned module out of the box.
	modules := opt.KnownModules
	if len(modules) == 0 {
		modules = []string{"door-001"}
	}

	for _, mid := range modules {
		mid = strings.TrimSpace(mid)
		if mid == "" {
			continue
		}

		if _, err := db.ExecContext(ctx, `
INSERT INTO modules(
  module_id, door_id, display_name,
  enabled, commissioned_at_ms,
  created_at_ms, updated_at_ms
) VALUES (?, 'door_main', ?, 1, ?, ?, ?)
ON CONFLICT(module_id) DO UPDATE SET
  door_id = excluded.door_id,
  display_name = excluded.display_name,
  enabled = 1,
  commissioned_at_ms = COALESCE(modules.commissioned_at_ms, excluded.commissioned_at_ms),
  updated_at_ms = excluded.updated_at_ms;
`, mid, mid, now, now, now); err != nil {
			return fmt.Errorf("seed module %s: %w", mid, err)
		}
	}

	return nil
}
