package store

import (
	"context"
	"time"
)

// AccessEventRecord captures a single access decision for the audit log.
// CardIDHash is left nil until card hashing is implemented (item 3);
// the column is nullable so this is safe.
type AccessEventRecord struct {
	ModuleID    string
	ReceivedAt  time.Time
	RequestedAt *time.Time // optional device-reported timestamp
	DoorClosed  *bool
	CardIDHash  []byte // SHA-256; nil until card hashing is wired up
	Granted     bool
	Reason      string
	DecidedAt   time.Time
}

// AccessEventStore persists access decisions as an append-only audit log.
type AccessEventStore interface {
	RecordEvent(ctx context.Context, rec AccessEventRecord) error
}
