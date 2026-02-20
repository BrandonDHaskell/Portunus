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

type HeartbeatStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewHeartbeatStore(db *sql.DB, writer *dbpkg.Worker) *HeartbeatStore {
	return &HeartbeatStore{db: db, writer: writer}
}

func (s *HeartbeatStore) UpsertHeartbeat(ctx context.Context, moduleID string, rec store.HeartbeatRecord) error {
	moduleID = strings.TrimSpace(moduleID)
	if moduleID == "" {
		return nil
	}

	if rec.ReceivedAt.IsZero() {
		rec.ReceivedAt = time.Now().UTC()
	}
	recvMs := rec.ReceivedAt.UTC().UnixMilli()

	// Map request -> DB columns
	fw := strings.TrimSpace(rec.Request.FirmwareVersion)
	if fw == "" {
		fw = "" // keep empty; you can switch to NULL if you prefer
	}

	var rssi any
	if rec.Request.RSSIDbm != nil {
		rssi = *rec.Request.RSSIDbm
	} else {
		rssi = nil
	}

	ip := strings.TrimSpace(rec.Request.IP)
	if ip == "" {
		ip = "" // keep empty; you can switch to NULL if you prefer
	}

	// UptimeSeconds -> uptime_ms
	uptimeMs := any(nil)
	if rec.Request.UptimeSeconds != 0 {
		// safe conversion (realistic uptimes won’t overflow int64)
		uptimeMs = int64(rec.Request.UptimeSeconds) * 1000
	}

	var seq any
	if rec.Request.Sequence != 0 {
		seq = rec.Request.Sequence
	}

	var freeHeap any
	if rec.Request.FreeHeapBytes != 0 {
		freeHeap = rec.Request.FreeHeapBytes
	}

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := ensureModule(ctx, tx, moduleID, recvMs); err != nil {
			return err
		}

		// Insert heartbeat event (append-only)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO module_heartbeats(
  module_id, received_at_ms, seq, uptime_ms, fw_version, wifi_rssi, ip, free_heap_bytes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);
`, moduleID, recvMs, seq, uptimeMs, fw, rssi, ip, freeHeap); err != nil {
			return fmt.Errorf("UpsertHeartbeat insert heartbeat: %w", err)
		}

		// Update module snapshot (fast “current status” queries)
		if _, err := tx.ExecContext(ctx, `
UPDATE modules
SET last_seen_at_ms = ?,
    last_ip = ?,
    last_fw_version = ?,
    last_wifi_rssi = ?,
    updated_at_ms = ?
WHERE module_id = ?;
`, recvMs, ip, fw, rssi, recvMs, moduleID); err != nil {
			return fmt.Errorf("UpsertHeartbeat update module snapshot: %w", err)
		}

		return nil
	})
}

// PruneOlderThan deletes heartbeat rows with received_at_ms before the given
// cutoff time.  Returns the number of rows deleted.
//
// Uses the idx_heartbeats_time index for an efficient range scan.
func (s *HeartbeatStore) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoffMs := cutoff.UTC().UnixMilli()

	var deleted int64
	err := s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
DELETE FROM module_heartbeats
WHERE received_at_ms < ?;
`, cutoffMs)
		if err != nil {
			return fmt.Errorf("PruneOlderThan: %w", err)
		}
		deleted, _ = res.RowsAffected()
		return nil
	})
	return deleted, err
}
