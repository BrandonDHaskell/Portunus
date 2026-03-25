package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	dbpkg "github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type CardStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewCardStore(db *sql.DB, writer *dbpkg.Worker) *CardStore {
	return &CardStore{db: db, writer: writer}
}

func (s *CardStore) RegisterCard(ctx context.Context, cardIDHash []byte, tag string) error {
	if len(cardIDHash) != 32 {
		return fmt.Errorf("card_id_hash must be 32 bytes, got %d", len(cardIDHash))
	}

	now := time.Now().UTC().UnixMilli()

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Check if card already exists.
		var exists int
		err := tx.QueryRowContext(ctx, `
SELECT 1 FROM cards WHERE card_id_hash = ?;
`, cardIDHash).Scan(&exists)
		if err == nil {
			return store.ErrCardAlreadyExists
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("RegisterCard check: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
INSERT INTO cards(card_id_hash, card_tag, status, created_at_ms, updated_at_ms)
VALUES (?, ?, 'active', ?, ?);
`, cardIDHash, tag, now, now); err != nil {
			return fmt.Errorf("RegisterCard insert: %w", err)
		}

		return nil
	})
}

func (s *CardStore) ListCards(ctx context.Context) ([]store.CardRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT card_id_hash, card_tag, status, created_at_ms, last_seen_at_ms
FROM cards
ORDER BY created_at_ms DESC;
`)
	if err != nil {
		return nil, fmt.Errorf("ListCards query: %w", err)
	}
	defer rows.Close()

	var cards []store.CardRecord
	for rows.Next() {
		var (
			hash      []byte
			tag       sql.NullString
			status    string
			createdMs int64
			seenMs    sql.NullInt64
		)
		if err := rows.Scan(&hash, &tag, &status, &createdMs, &seenMs); err != nil {
			return nil, fmt.Errorf("ListCards scan: %w", err)
		}

		rec := store.CardRecord{
			CardIDHash: hash,
			Tag:        tag.String,
			Status:     status,
			CreatedAt:  time.UnixMilli(createdMs).UTC(),
		}
		if seenMs.Valid {
			t := time.UnixMilli(seenMs.Int64).UTC()
			rec.LastSeenAt = &t
		}
		cards = append(cards, rec)
	}
	return cards, rows.Err()
}

func (s *CardStore) SetCardStatus(ctx context.Context, cardIDHash []byte, status string) error {
	now := time.Now().UTC().UnixMilli()

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
UPDATE cards SET status = ?, updated_at_ms = ? WHERE card_id_hash = ?;
`, status, now, cardIDHash)
		if err != nil {
			return fmt.Errorf("SetCardStatus: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

func (s *CardStore) DeleteCard(ctx context.Context, cardIDHash []byte) error {
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
DELETE FROM cards WHERE card_id_hash = ?;
`, cardIDHash)
		if err != nil {
			return fmt.Errorf("DeleteCard: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

func (s *CardStore) IsCardAllowed(ctx context.Context, cardIDHash []byte) (bool, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `
SELECT status FROM cards WHERE card_id_hash = ?;
`, cardIDHash).Scan(&status)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("IsCardAllowed: %w", err)
	}

	if status == "active" {
		// Update last_seen_at as a background side effect.
		now := time.Now().UTC().UnixMilli()
		_, _ = s.db.ExecContext(ctx, `
UPDATE cards SET last_seen_at_ms = ?, updated_at_ms = ? WHERE card_id_hash = ?;
`, now, now, cardIDHash)
	}

	return status == "active", nil
}
