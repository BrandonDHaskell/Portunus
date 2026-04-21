package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	dbpkg "github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type SessionStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewSessionStore(db *sql.DB, writer *dbpkg.Worker) *SessionStore {
	return &SessionStore{db: db, writer: writer}
}

func (s *SessionStore) CreateSession(ctx context.Context, sessionID, adminUUID string, expiresAt time.Time) error {
	now := time.Now().UTC().UnixMilli()
	expiresMs := expiresAt.UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO sessions(session_id, admin_uuid, created_at_ms, expires_at_ms)
VALUES (?, ?, ?, ?);
`, sessionID, adminUUID, now, expiresMs)
		if err != nil {
			return fmt.Errorf("CreateSession: %w", err)
		}
		return nil
	})
}

func (s *SessionStore) GetSession(ctx context.Context, sessionID string) (*store.SessionRecord, error) {
	var (
		id, adminUUID        string
		createdMs, expiresMs int64
	)
	err := s.db.QueryRowContext(ctx, `
SELECT session_id, admin_uuid, created_at_ms, expires_at_ms
FROM sessions WHERE session_id = ?;
`, sessionID).Scan(&id, &adminUUID, &createdMs, &expiresMs)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetSession: %w", err)
	}
	return &store.SessionRecord{
		SessionID: id,
		AdminUUID: adminUUID,
		CreatedAt: time.UnixMilli(createdMs).UTC(),
		ExpiresAt: time.UnixMilli(expiresMs).UTC(),
	}, nil
}

func (s *SessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE session_id = ?;`, sessionID)
		if err != nil {
			return fmt.Errorf("DeleteSession: %w", err)
		}
		return nil
	})
}

func (s *SessionStore) DeleteExpiredSessions(ctx context.Context) error {
	now := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at_ms <= ?;`, now)
		if err != nil {
			return fmt.Errorf("DeleteExpiredSessions: %w", err)
		}
		return nil
	})
}
