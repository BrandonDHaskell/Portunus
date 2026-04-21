package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrCredentialAlreadyExists = errors.New("credential already registered")
	ErrNotFound                = errors.New("record not found")
)

// CredentialRecord represents a row in the credentials table.
type CredentialRecord struct {
	CredentialHash []byte // SHA-256 of the raw credential
	Tag            string
	Status         string // "active", "disabled", "lost"
	CreatedAt      time.Time
	LastSeenAt     *time.Time
}

// CredentialStore manages credential registrations.
type CredentialStore interface {
	// RegisterCredential inserts a new credential. credentialHash must be 32 bytes (SHA-256).
	// Returns ErrCredentialAlreadyExists if the hash is already present.
	RegisterCredential(ctx context.Context, credentialHash []byte, tag string) error

	// ListCredentials returns all registered credentials.
	ListCredentials(ctx context.Context) ([]CredentialRecord, error)

	// SetCredentialStatus changes a credential's status (active, disabled, lost).
	SetCredentialStatus(ctx context.Context, credentialHash []byte, status string) error

	// DeleteCredential removes a credential registration entirely.
	DeleteCredential(ctx context.Context, credentialHash []byte) error

	// IsCredentialAllowed checks if a credential hash exists and has status "active".
	// Also updates last_seen_at as a side effect.
	IsCredentialAllowed(ctx context.Context, credentialHash []byte) (bool, error)
}
