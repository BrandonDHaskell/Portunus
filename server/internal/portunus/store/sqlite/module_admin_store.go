package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	dbpkg "github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type ModuleAdminStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewModuleAdminStore(db *sql.DB, writer *dbpkg.Worker) *ModuleAdminStore {
	return &ModuleAdminStore{db: db, writer: writer}
}

func (s *ModuleAdminStore) CommissionModule(ctx context.Context, moduleID, doorID, displayName string) error {
	moduleID = strings.TrimSpace(moduleID)
	if moduleID == "" {
		return fmt.Errorf("module_id is required")
	}

	now := time.Now().UTC().UnixMilli()

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Upsert: if the module was auto-created by ensureModule (disabled,
		// uncommissioned), promote it. If new, create commissioned.
		var doorIDVal any
		if doorID != "" {
			doorIDVal = doorID
		}

		if _, err := tx.ExecContext(ctx, `
INSERT INTO modules(
  module_id, door_id, display_name,
  enabled, commissioned_at_ms,
  created_at_ms, updated_at_ms
) VALUES (?, ?, ?, 1, ?, ?, ?)
ON CONFLICT(module_id) DO UPDATE SET
  door_id              = excluded.door_id,
  display_name         = excluded.display_name,
  enabled              = 1,
  commissioned_at_ms   = COALESCE(modules.commissioned_at_ms, excluded.commissioned_at_ms),
  revoked_at_ms        = NULL,
  updated_at_ms        = excluded.updated_at_ms;
`, moduleID, doorIDVal, displayName, now, now, now); err != nil {
			return fmt.Errorf("CommissionModule %s: %w", moduleID, err)
		}

		return nil
	})
}

func (s *ModuleAdminStore) RevokeModule(ctx context.Context, moduleID string) error {
	moduleID = strings.TrimSpace(moduleID)
	now := time.Now().UTC().UnixMilli()

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
UPDATE modules
SET enabled       = 0,
    revoked_at_ms = ?,
    updated_at_ms = ?
WHERE module_id = ?;
`, now, now, moduleID)
		if err != nil {
			return fmt.Errorf("RevokeModule: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

func (s *ModuleAdminStore) DeleteModule(ctx context.Context, moduleID string) error {
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
DELETE FROM modules WHERE module_id = ?;
`, moduleID)
		if err != nil {
			return fmt.Errorf("DeleteModule: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

func (s *ModuleAdminStore) GetModule(ctx context.Context, moduleID string) (*store.ModuleRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT module_id, door_id, display_name, enabled,
       commissioned_at_ms, revoked_at_ms, last_seen_at_ms,
       last_ip, last_fw_version, last_wifi_rssi, created_at_ms
FROM modules
WHERE module_id = ?;
`, moduleID)

	return scanModuleRow(row)
}

func (s *ModuleAdminStore) ListModules(ctx context.Context) ([]store.ModuleRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT module_id, door_id, display_name, enabled,
       commissioned_at_ms, revoked_at_ms, last_seen_at_ms,
       last_ip, last_fw_version, last_wifi_rssi, created_at_ms
FROM modules
ORDER BY created_at_ms DESC;
`)
	if err != nil {
		return nil, fmt.Errorf("ListModules: %w", err)
	}
	defer rows.Close()

	var modules []store.ModuleRecord
	for rows.Next() {
		rec, err := scanModuleRows(rows)
		if err != nil {
			return nil, err
		}
		modules = append(modules, *rec)
	}
	return modules, rows.Err()
}

func (s *ModuleAdminStore) RegisterDoor(ctx context.Context, doorID, name, location string) error {
	now := time.Now().UTC().UnixMilli()

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO doors(door_id, name, location, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(door_id) DO UPDATE SET
  name          = excluded.name,
  location      = excluded.location,
  updated_at_ms = excluded.updated_at_ms;
`, doorID, name, location, now, now); err != nil {
			return fmt.Errorf("RegisterDoor: %w", err)
		}
		return nil
	})
}

func (s *ModuleAdminStore) ListDoors(ctx context.Context) ([]store.DoorRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT door_id, name, location, created_at_ms
FROM doors
ORDER BY created_at_ms DESC;
`)
	if err != nil {
		return nil, fmt.Errorf("ListDoors: %w", err)
	}
	defer rows.Close()

	var doors []store.DoorRecord
	for rows.Next() {
		var (
			doorID    string
			name      string
			location  sql.NullString
			createdMs int64
		)
		if err := rows.Scan(&doorID, &name, &location, &createdMs); err != nil {
			return nil, fmt.Errorf("ListDoors scan: %w", err)
		}
		doors = append(doors, store.DoorRecord{
			DoorID:    doorID,
			Name:      name,
			Location:  location.String,
			CreatedAt: time.UnixMilli(createdMs).UTC(),
		})
	}
	return doors, rows.Err()
}

func (s *ModuleAdminStore) DeleteDoor(ctx context.Context, doorID string) error {
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
DELETE FROM doors WHERE door_id = ?;
`, doorID)
		if err != nil {
			return fmt.Errorf("DeleteDoor: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// ── scan helpers ─────────────────────────────────────────────────────────────

// scanModuleRow scans a single *sql.Row into a ModuleRecord.
func scanModuleRow(row *sql.Row) (*store.ModuleRecord, error) {
	var (
		moduleID       string
		doorID         sql.NullString
		displayName    sql.NullString
		enabled        int
		commissionedMs sql.NullInt64
		revokedMs      sql.NullInt64
		seenMs         sql.NullInt64
		lastIP         sql.NullString
		lastFW         sql.NullString
		lastRSSI       sql.NullInt64
		createdMs      int64
	)

	err := row.Scan(
		&moduleID, &doorID, &displayName, &enabled,
		&commissionedMs, &revokedMs, &seenMs,
		&lastIP, &lastFW, &lastRSSI, &createdMs,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanModuleRow: %w", err)
	}

	return buildModuleRecord(
		moduleID, doorID, displayName, enabled,
		commissionedMs, revokedMs, seenMs,
		lastIP, lastFW, lastRSSI, createdMs,
	), nil
}

// scanModuleRows scans one row from a *sql.Rows cursor.
func scanModuleRows(rows *sql.Rows) (*store.ModuleRecord, error) {
	var (
		moduleID       string
		doorID         sql.NullString
		displayName    sql.NullString
		enabled        int
		commissionedMs sql.NullInt64
		revokedMs      sql.NullInt64
		seenMs         sql.NullInt64
		lastIP         sql.NullString
		lastFW         sql.NullString
		lastRSSI       sql.NullInt64
		createdMs      int64
	)

	if err := rows.Scan(
		&moduleID, &doorID, &displayName, &enabled,
		&commissionedMs, &revokedMs, &seenMs,
		&lastIP, &lastFW, &lastRSSI, &createdMs,
	); err != nil {
		return nil, fmt.Errorf("scanModuleRows: %w", err)
	}

	return buildModuleRecord(
		moduleID, doorID, displayName, enabled,
		commissionedMs, revokedMs, seenMs,
		lastIP, lastFW, lastRSSI, createdMs,
	), nil
}

func buildModuleRecord(
	moduleID string,
	doorID sql.NullString,
	displayName sql.NullString,
	enabled int,
	commissionedMs, revokedMs, seenMs sql.NullInt64,
	lastIP, lastFW sql.NullString,
	lastRSSI sql.NullInt64,
	createdMs int64,
) *store.ModuleRecord {
	rec := &store.ModuleRecord{
		ModuleID:      moduleID,
		DoorID:        doorID.String,
		DisplayName:   displayName.String,
		Enabled:       enabled == 1,
		CreatedAt:     time.UnixMilli(createdMs).UTC(),
		LastIP:        lastIP.String,
		LastFWVersion: lastFW.String,
	}
	if commissionedMs.Valid {
		t := time.UnixMilli(commissionedMs.Int64).UTC()
		rec.CommissionedAt = &t
	}
	if revokedMs.Valid {
		t := time.UnixMilli(revokedMs.Int64).UTC()
		rec.RevokedAt = &t
	}
	if seenMs.Valid {
		t := time.UnixMilli(seenMs.Int64).UTC()
		rec.LastSeenAt = &t
	}
	if lastRSSI.Valid {
		v := int(lastRSSI.Int64)
		rec.LastWiFiRSSI = &v
	}
	return rec
}
