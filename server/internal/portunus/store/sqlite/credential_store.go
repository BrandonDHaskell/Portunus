package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	dbpkg "github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type CredentialStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewCredentialStore(db *sql.DB, writer *dbpkg.Worker) *CredentialStore {
	return &CredentialStore{db: db, writer: writer}
}

func (s *CredentialStore) RegisterCredential(ctx context.Context, credentialHash []byte, tag string) error {
	if len(credentialHash) != 32 {
		return fmt.Errorf("credential_hash must be 32 bytes, got %d", len(credentialHash))
	}

	now := time.Now().UTC().UnixMilli()

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var exists int
		err := tx.QueryRowContext(ctx, `
SELECT 1 FROM credentials WHERE credential_hash = ?;
`, credentialHash).Scan(&exists)
		if err == nil {
			return store.ErrCredentialAlreadyExists
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("RegisterCredential check: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
INSERT INTO credentials(credential_hash, credential_tag, status, created_at_ms, updated_at_ms)
VALUES (?, ?, 'active', ?, ?);
`, credentialHash, tag, now, now); err != nil {
			return fmt.Errorf("RegisterCredential insert: %w", err)
		}

		return nil
	})
}

func (s *CredentialStore) ListCredentials(ctx context.Context) ([]store.CredentialRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT credential_hash, credential_tag, status, created_at_ms, last_seen_at_ms
FROM credentials
ORDER BY created_at_ms DESC;
`)
	if err != nil {
		return nil, fmt.Errorf("ListCredentials query: %w", err)
	}
	defer rows.Close()

	var credentials []store.CredentialRecord
	for rows.Next() {
		var (
			hash      []byte
			tag       sql.NullString
			status    string
			createdMs int64
			seenMs    sql.NullInt64
		)
		if err := rows.Scan(&hash, &tag, &status, &createdMs, &seenMs); err != nil {
			return nil, fmt.Errorf("ListCredentials scan: %w", err)
		}

		rec := store.CredentialRecord{
			CredentialHash: hash,
			Tag:            tag.String,
			Status:         status,
			CreatedAt:      time.UnixMilli(createdMs).UTC(),
		}
		if seenMs.Valid {
			t := time.UnixMilli(seenMs.Int64).UTC()
			rec.LastSeenAt = &t
		}
		credentials = append(credentials, rec)
	}
	return credentials, rows.Err()
}

func (s *CredentialStore) SetCredentialStatus(ctx context.Context, credentialHash []byte, status string) error {
	now := time.Now().UTC().UnixMilli()

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
UPDATE credentials SET status = ?, updated_at_ms = ? WHERE credential_hash = ?;
`, status, now, credentialHash)
		if err != nil {
			return fmt.Errorf("SetCredentialStatus: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return store.ErrNotFound
		}
		return nil
	})
}

func (s *CredentialStore) DeleteCredential(ctx context.Context, credentialHash []byte) error {
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
DELETE FROM credentials WHERE credential_hash = ?;
`, credentialHash)
		if err != nil {
			return fmt.Errorf("DeleteCredential: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return store.ErrNotFound
		}
		return nil
	})
}

func (s *CredentialStore) IsCredentialAllowed(ctx context.Context, credentialHash []byte) (bool, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `
SELECT status FROM credentials WHERE credential_hash = ?;
`, credentialHash).Scan(&status)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("IsCredentialAllowed: %w", err)
	}

	if status == "active" {
		now := time.Now().UTC().UnixMilli()
		go func() {
			wCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.writer.Do(wCtx, func(wCtx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(wCtx, `
UPDATE credentials SET last_seen_at_ms = ?, updated_at_ms = ? WHERE credential_hash = ?;
`, now, now, credentialHash)
				return err
			})
		}()
	}

	return status == "active", nil
}
