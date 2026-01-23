package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	dbpkg "github.com/BrandonDHaskell/Portunus/server/internal/db"
)

type DeviceStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewDeviceStore(db *sql.DB, writer *dbpkg.Worker) *DeviceStore {
	return &DeviceStore{db: db, writer: writer}
}

// IsKnown: treat “known” as “commissioned + enabled + not revoked”.
// This aligns with your “prod: admin seeds/commissions modules” requirement.
func (s *DeviceStore) IsKnown(ctx context.Context, moduleID string) (bool, error) {
	moduleID = strings.TrimSpace(moduleID)
	if moduleID == "" {
		return false, nil
	}

	var enabled int
	var commissioned sql.NullInt64
	var revoked sql.NullInt64

	err := s.db.QueryRowContext(ctx, `
SELECT enabled, commissioned_at_ms, revoked_at_ms
FROM modules
WHERE module_id = ?;
`, moduleID).Scan(&enabled, &commissioned, &revoked)

	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("IsKnown query: %w", err)
	}

	known := enabled == 1 && commissioned.Valid && !revoked.Valid
	return known, nil
}

// MarkSeen: ensure module row exists (even if unknown) and update last_seen.
func (s *DeviceStore) MarkSeen(ctx context.Context, moduleID string, _ bool, t time.Time) error {
	moduleID = strings.TrimSpace(moduleID)
	if moduleID == "" {
		return nil
	}
	if t.IsZero() {
		t = time.Now().UTC()
	}
	ms := t.UTC().UnixMilli()

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Ensure the module exists so FKs from heartbeats can work.
		// Unknown modules start disabled/uncommissioned.
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO modules(
  module_id, enabled, created_at_ms, updated_at_ms
) VALUES (?, 0, ?, ?);
`, moduleID, ms, ms); err != nil {
			return fmt.Errorf("MarkSeen insert module: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
UPDATE modules
SET last_seen_at_ms = ?,
    updated_at_ms   = ?
WHERE module_id = ?;
`, ms, ms, moduleID); err != nil {
			return fmt.Errorf("MarkSeen update module: %w", err)
		}

		return nil
	})
}
