package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	dbpkg "github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type AuditStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewAuditStore(db *sql.DB, writer *dbpkg.Worker) *AuditStore {
	return &AuditStore{db: db, writer: writer}
}

func (s *AuditStore) RecordAuditEntry(ctx context.Context, e store.AuditEntry) error {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if e.Result == "" {
		e.Result = "success"
	}
	if e.ActorType == "" {
		e.ActorType = store.ActorTypeSystem
	}

	occurredMs := e.OccurredAt.UTC().UnixMilli()

	var actorUUID any
	if e.ActorUUID != "" {
		actorUUID = e.ActorUUID
	}

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO audit_log(
  id, occurred_at_ms,
  actor_uuid, actor_type,
  action, resource_type, resource_id,
  details, ip_address, result
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
`,
			e.ID, occurredMs,
			actorUUID, string(e.ActorType),
			e.Action, nullStr(e.ResourceType), nullStr(e.ResourceID),
			nullStr(e.Details), nullStr(e.IPAddress), e.Result,
		)
		if err != nil {
			return fmt.Errorf("RecordAuditEntry: %w", err)
		}
		return nil
	})
}

func (s *AuditStore) ListAuditEntries(ctx context.Context, limit int) ([]store.AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, occurred_at_ms,
       COALESCE(actor_uuid, ''), actor_type,
       action,
       COALESCE(resource_type, ''), COALESCE(resource_id, ''),
       COALESCE(details, ''), COALESCE(ip_address, ''),
       result
FROM audit_log
ORDER BY occurred_at_ms DESC
LIMIT ?;
`, limit)
	if err != nil {
		return nil, fmt.Errorf("ListAuditEntries: %w", err)
	}
	defer rows.Close()

	var entries []store.AuditEntry
	for rows.Next() {
		var (
			e         store.AuditEntry
			occurredMs int64
			actorType  string
		)
		if err := rows.Scan(
			&e.ID, &occurredMs,
			&e.ActorUUID, &actorType,
			&e.Action, &e.ResourceType, &e.ResourceID,
			&e.Details, &e.IPAddress, &e.Result,
		); err != nil {
			return nil, fmt.Errorf("ListAuditEntries scan: %w", err)
		}
		e.OccurredAt = time.UnixMilli(occurredMs).UTC()
		e.ActorType = store.ActorType(actorType)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// nullStr converts an empty string to nil for nullable TEXT columns.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
