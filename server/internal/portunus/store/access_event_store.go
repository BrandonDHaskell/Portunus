package store

import (
	"context"
	"time"
)

// AccessEventRecord captures a single access decision for the audit log.
type AccessEventRecord struct {
	ModuleID       string
	ReceivedAt     time.Time
	RequestedAt    *time.Time // optional device-reported timestamp
	DoorClosed     *bool
	CredentialHash []byte // SHA-256 of the credential
	Granted        bool
	Reason         string
	DecidedAt      time.Time
}

// AccessEventStore persists access decisions as an append-only audit log.
type AccessEventStore interface {
	RecordEvent(ctx context.Context, rec AccessEventRecord) error
}
