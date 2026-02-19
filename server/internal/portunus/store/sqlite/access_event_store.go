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

	// card_id_hash is nil until card hashing is implemented (item 3).
	var cardIDHash any
	if len(rec.CardIDHash) == 32 {
		cardIDHash = rec.CardIDHash
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
  card_id_hash, decision_granted, decision_reason, decided_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);
`,
			rec.ModuleID, doorID, receivedMs, requestedMs, doorClosed,
			cardIDHash, granted, rec.Reason, decidedMs,
		); err != nil {
			return fmt.Errorf("RecordEvent insert: %w", err)
		}

		return nil
	})
}
