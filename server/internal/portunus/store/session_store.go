package store

import (
	"context"
	"time"
)

// SessionRecord represents a row in the sessions table.
type SessionRecord struct {
	SessionID string
	AdminUUID string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionStore manages server-side session tokens for cookie-based auth.
type SessionStore interface {
	// CreateSession inserts a new session row.
	CreateSession(ctx context.Context, sessionID, adminUUID string, expiresAt time.Time) error

	// GetSession returns the session for the given ID, or ErrNotFound.
	// Returns ErrNotFound for expired sessions too — callers should check
	// ExpiresAt when deciding whether to honour the session.
	GetSession(ctx context.Context, sessionID string) (*SessionRecord, error)

	// DeleteSession removes a session (logout).
	DeleteSession(ctx context.Context, sessionID string) error

	// DeleteExpiredSessions removes all rows whose expires_at_ms is in the past.
	DeleteExpiredSessions(ctx context.Context) error
}
