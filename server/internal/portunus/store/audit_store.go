package store

import (
	"context"
	"time"
)

// ActorType identifies what kind of principal performed an audited action.
type ActorType string

const (
	ActorTypeAdmin  ActorType = "admin"
	ActorTypeMember ActorType = "member"
	ActorTypeSystem ActorType = "system"
)

// AuditEntry is one row in audit_log. ID and OccurredAt are filled by the
// store if left zero. ActorUUID is empty for system actions. Details is a
// free-form JSON string with action-specific context.
type AuditEntry struct {
	ID           string
	OccurredAt   time.Time
	ActorUUID    string
	ActorType    ActorType
	Action       string
	ResourceType string
	ResourceID   string
	Details      string
	IPAddress    string
	Result       string // "success" or "failure"; defaults to "success"
}

// AuditStore writes and reads the audit_log table.
type AuditStore interface {
	// RecordAuditEntry appends one audit row. Best-effort: callers must not
	// fail the primary operation when this returns an error.
	RecordAuditEntry(ctx context.Context, e AuditEntry) error

	// ListAuditEntries returns recent entries, newest first, capped by limit.
	ListAuditEntries(ctx context.Context, limit int) ([]AuditEntry, error)
}
