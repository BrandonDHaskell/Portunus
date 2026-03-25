package store

import (
	"context"
	"errors"
	"time"
)

var ErrCardAlreadyExists = errors.New("card already registered")

// CardRecord represents a row in the cards table.
type CardRecord struct {
	CardIDHash []byte // SHA-256 of the raw card ID
	Tag        string
	Status     string // "active", "disabled", "lost"
	CreatedAt  time.Time
	LastSeenAt *time.Time
}

// CardStore manages RFID card registrations.
type CardStore interface {
	// RegisterCard inserts a new card. cardIDHash must be 32 bytes (SHA-256).
	// Returns ErrCardAlreadyExists if the hash is already present.
	RegisterCard(ctx context.Context, cardIDHash []byte, tag string) error

	// ListCards returns all registered cards.
	ListCards(ctx context.Context) ([]CardRecord, error)

	// SetCardStatus changes a card's status (active, disabled, lost).
	SetCardStatus(ctx context.Context, cardIDHash []byte, status string) error

	// DeleteCard removes a card registration entirely.
	DeleteCard(ctx context.Context, cardIDHash []byte) error

	// IsCardAllowed checks if a card hash exists and has status "active".
	// Also updates last_seen_at as a side effect.
	IsCardAllowed(ctx context.Context, cardIDHash []byte) (bool, error)
}
