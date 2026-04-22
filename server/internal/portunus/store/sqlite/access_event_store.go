package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	dbpkg "github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type AccessEventStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewAccessEventStore(db *sql.DB, writer *dbpkg.Worker) *AccessEventStore {
	return &AccessEventStore{db: db, writer: writer}
}

func (s *AccessEventStore) ListEventsByCredential(ctx context.Context, credentialHash []byte, limit int) ([]store.AccessEventRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT module_id, received_at_ms, requested_at_ms, door_closed,
       credential_hash, decision_granted, decision_reason, decided_at_ms
FROM access_events
WHERE credential_hash = ?
ORDER BY received_at_ms DESC
LIMIT ?;
`, credentialHash, limit)
	if err != nil {
		return nil, fmt.Errorf("ListEventsByCredential query: %w", err)
	}
	defer rows.Close()

	var out []store.AccessEventRecord
	for rows.Next() {
		var rec store.AccessEventRecord
		var receivedMs, decidedMs int64
		var requestedMs sql.NullInt64
		var doorClosed sql.NullInt64
		var granted int
		var credHash []byte

		if err := rows.Scan(
			&rec.ModuleID, &receivedMs, &requestedMs, &doorClosed,
			&credHash, &granted, &rec.Reason, &decidedMs,
		); err != nil {
			return nil, fmt.Errorf("ListEventsByCredential scan: %w", err)
		}

		rec.ReceivedAt = time.UnixMilli(receivedMs).UTC()
		rec.DecidedAt = time.UnixMilli(decidedMs).UTC()
		if requestedMs.Valid {
			t := time.UnixMilli(requestedMs.Int64).UTC()
			rec.RequestedAt = &t
		}
		if doorClosed.Valid {
			b := doorClosed.Int64 != 0
			rec.DoorClosed = &b
		}
		rec.Granted = granted != 0
		rec.CredentialHash = credHash
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *AccessEventStore) RecordEvent(ctx context.Context, rec store.AccessEventRecord) error {
	if rec.ReceivedAt.IsZero() {
		rec.ReceivedAt = time.Now().UTC()
	}
	if rec.DecidedAt.IsZero() {
		rec.DecidedAt = time.Now().UTC()
	}

	receivedMs := rec.ReceivedAt.UTC().UnixMilli()
	decidedMs := rec.DecidedAt.UTC().UnixMilli()

	var requestedMs any
	if rec.RequestedAt != nil {
		requestedMs = rec.RequestedAt.UTC().UnixMilli()
	}

	var doorClosed any
	if rec.DoorClosed != nil {
		if *rec.DoorClosed {
			doorClosed = 1
		} else {
			doorClosed = 0
		}
	}

	var granted int
	if rec.Granted {
		granted = 1
	}

	var credentialHash any
	if len(rec.CredentialHash) == 32 {
		credentialHash = rec.CredentialHash
	}

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Resolve door_id from the module's current assignment.
		// Returns NULL if the module has no door, which is fine — the column is nullable.
		var doorID any
		err := tx.QueryRowContext(ctx, `
SELECT door_id FROM modules WHERE module_id = ?;
`, rec.ModuleID).Scan(&doorID)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("RecordEvent resolve door_id: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
INSERT INTO access_events(
  module_id, door_id, received_at_ms, requested_at_ms, door_closed,
  credential_hash, decision_granted, decision_reason, decided_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);
`,
			rec.ModuleID, doorID, receivedMs, requestedMs, doorClosed,
			credentialHash, granted, rec.Reason, decidedMs,
		); err != nil {
			return fmt.Errorf("RecordEvent insert: %w", err)
		}

		return nil
	})
}
