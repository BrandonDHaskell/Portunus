package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// ensureModule guarantees a modules row exists for the given moduleID so that
// foreign-key constraints from heartbeats and access_events are satisfied.
//
// New rows start disabled and uncommissioned — only an admin action (or the
// dev seeder) should set enabled=1 and commissioned_at_ms.
//
// Must be called inside an existing transaction.
func ensureModule(ctx context.Context, tx *sql.Tx, moduleID string, nowMs int64) error {
	if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO modules(
  module_id, enabled, created_at_ms, updated_at_ms
) VALUES (?, 0, ?, ?);
`, moduleID, nowMs, nowMs); err != nil {
		return fmt.Errorf("ensureModule %s: %w", moduleID, err)
	}
	return nil
}
