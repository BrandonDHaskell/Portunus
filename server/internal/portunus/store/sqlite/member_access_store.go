package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	dbpkg "github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type MemberAccessStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewMemberAccessStore(db *sql.DB, writer *dbpkg.Worker) *MemberAccessStore {
	return &MemberAccessStore{db: db, writer: writer}
}

func (s *MemberAccessStore) CreateMember(ctx context.Context,
	uuid, createdByUUID string,
	provisioningStatus store.ProvisioningStatus,
	expiresAt *time.Time, inactivityLimitDays *int,
) error {
	now := time.Now().UTC().UnixMilli()
	var expiresAtMs *int64
	if expiresAt != nil {
		ms := expiresAt.UTC().UnixMilli()
		expiresAtMs = &ms
	}
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO member_access(
  uuid, status, enabled,
  expires_at_ms, inactivity_limit_days,
  created_at_ms, created_by_uuid,
  provisioning_status)
VALUES (?, 'active', 1, ?, ?, ?, ?, ?);
`, uuid, expiresAtMs, inactivityLimitDays, now, createdByUUID, string(provisioningStatus))
		if err != nil {
			return fmt.Errorf("CreateMember: %w", err)
		}
		return nil
	})
}

func (s *MemberAccessStore) GetMember(ctx context.Context, uuid string) (*store.MemberAccessRecord, error) {
	row := s.db.QueryRowContext(ctx, memberAccessSelectSQL+` WHERE uuid = ?;`, uuid)
	rec, err := scanMemberAccessRow(row)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	return rec, err
}

func (s *MemberAccessStore) GetMemberByCredential(ctx context.Context, credentialHash []byte) (*store.MemberAccessRecord, error) {
	row := s.db.QueryRowContext(ctx, memberAccessSelectSQL+` WHERE credential_hash = ?;`, credentialHash)
	rec, err := scanMemberAccessRow(row)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	return rec, err
}

func (s *MemberAccessStore) ListMembers(ctx context.Context) ([]store.MemberAccessRecord, error) {
	rows, err := s.db.QueryContext(ctx, memberAccessSelectSQL+` ORDER BY created_at_ms DESC;`)
	if err != nil {
		return nil, fmt.Errorf("ListMembers: %w", err)
	}
	return scanMemberAccessRows(rows)
}

func (s *MemberAccessStore) ListPendingAuthorizations(ctx context.Context) ([]store.MemberAccessRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		memberAccessSelectSQL+` WHERE provisioning_status = 'pending_authorization' ORDER BY created_at_ms ASC;`)
	if err != nil {
		return nil, fmt.Errorf("ListPendingAuthorizations: %w", err)
	}
	return scanMemberAccessRows(rows)
}

func (s *MemberAccessStore) SetStatus(ctx context.Context, uuid string, status store.MemberStatus) error {
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE member_access SET status = ? WHERE uuid = ?;`, string(status), uuid)
		if err != nil {
			return fmt.Errorf("SetStatus: %w", err)
		}
		return requireOneRow(res, "SetStatus")
	})
}

func (s *MemberAccessStore) SetEnabled(ctx context.Context, uuid string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE member_access SET enabled = ? WHERE uuid = ?;`, v, uuid)
		if err != nil {
			return fmt.Errorf("SetEnabled: %w", err)
		}
		return requireOneRow(res, "SetEnabled")
	})
}

func (s *MemberAccessStore) AttachCredential(ctx context.Context, uuid string, credentialHash []byte) error {
	if len(credentialHash) != 32 {
		return fmt.Errorf("credential_hash must be 32 bytes, got %d", len(credentialHash))
	}
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Pre-check for a meaningful duplicate error before hitting the UNIQUE constraint.
		var existingUUID string
		err := tx.QueryRowContext(ctx,
			`SELECT uuid FROM member_access WHERE credential_hash = ?;`, credentialHash,
		).Scan(&existingUUID)
		if err == nil {
			return store.ErrMemberCredentialConflict
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("AttachCredential conflict check: %w", err)
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE member_access SET credential_hash = ? WHERE uuid = ?;`, credentialHash, uuid)
		if err != nil {
			return fmt.Errorf("AttachCredential: %w", err)
		}
		return requireOneRow(res, "AttachCredential")
	})
}

func (s *MemberAccessStore) SetProvisioningStatus(ctx context.Context, uuid string, status store.ProvisioningStatus) error {
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE member_access SET provisioning_status = ? WHERE uuid = ?;`, string(status), uuid)
		if err != nil {
			return fmt.Errorf("SetProvisioningStatus: %w", err)
		}
		return requireOneRow(res, "SetProvisioningStatus")
	})
}

func (s *MemberAccessStore) UpdateLastAccess(ctx context.Context, uuid string, t time.Time) error {
	ms := t.UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE member_access SET last_access_at_ms = ? WHERE uuid = ?;`, ms, uuid)
		if err != nil {
			return fmt.Errorf("UpdateLastAccess: %w", err)
		}
		return requireOneRow(res, "UpdateLastAccess")
	})
}

func (s *MemberAccessStore) ArchiveMember(ctx context.Context, uuid, archivedByUUID string) error {
	now := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
UPDATE member_access
   SET status = 'archived', archived_at_ms = ?, archived_by_uuid = ?
 WHERE uuid = ?;
`, now, archivedByUUID, uuid)
		if err != nil {
			return fmt.Errorf("ArchiveMember: %w", err)
		}
		return requireOneRow(res, "ArchiveMember")
	})
}

func (s *MemberAccessStore) ExpireByHardDeadline(ctx context.Context, cutoff time.Time) (int, error) {
	cutoffMs := cutoff.UTC().UnixMilli()
	var count int
	err := s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
UPDATE member_access
   SET status = 'expired'
 WHERE status = 'active'
   AND expires_at_ms IS NOT NULL
   AND expires_at_ms <= ?;
`, cutoffMs)
		if err != nil {
			return fmt.Errorf("ExpireByHardDeadline: %w", err)
		}
		n, _ := res.RowsAffected()
		count = int(n)
		return nil
	})
	return count, err
}

func (s *MemberAccessStore) ExpireByInactivity(ctx context.Context, now time.Time) (int, error) {
	nowMs := now.UTC().UnixMilli()
	var count int
	err := s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Falls back through activated_at_ms then created_at_ms so pending
		// dwell time does not consume the inactivity window.
		res, err := tx.ExecContext(ctx, `
UPDATE member_access
   SET status = 'expired'
 WHERE status = 'active'
   AND inactivity_limit_days IS NOT NULL
   AND (
     COALESCE(last_access_at_ms, activated_at_ms, created_at_ms) + (inactivity_limit_days * 86400000)
   ) <= ?;
`, nowMs)
		if err != nil {
			return fmt.Errorf("ExpireByInactivity: %w", err)
		}
		n, _ := res.RowsAffected()
		count = int(n)
		return nil
	})
	return count, err
}

func (s *MemberAccessStore) ApprovePending(ctx context.Context, uuid, approvedByUUID string,
	expiresAt *time.Time, inactivityLimitDays *int,
) error {
	var expiresAtMs *int64
	if expiresAt != nil {
		ms := expiresAt.UTC().UnixMilli()
		expiresAtMs = &ms
	}
	activatedMs := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var provStatus string
		err := tx.QueryRowContext(ctx,
			`SELECT provisioning_status FROM member_access WHERE uuid = ?;`, uuid).Scan(&provStatus)
		if err == sql.ErrNoRows {
			return store.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("ApprovePending lookup: %w", err)
		}
		if provStatus != string(store.ProvisioningStatusPendingAuthorization) {
			return store.ErrMemberNotPending
		}
		_, err = tx.ExecContext(ctx, `
UPDATE member_access
SET status = 'active', provisioning_status = 'active',
    activated_at_ms = ?, created_by_uuid = ?,
    expires_at_ms = ?, inactivity_limit_days = ?
WHERE uuid = ?;
`, activatedMs, approvedByUUID, expiresAtMs, inactivityLimitDays, uuid)
		if err != nil {
			return fmt.Errorf("ApprovePending update: %w", err)
		}
		return nil
	})
}

// ── query helpers ─────────────────────────────────────────────────────────────

const memberAccessSelectSQL = `
SELECT uuid, credential_hash, status, enabled,
       expires_at_ms, inactivity_limit_days, activated_at_ms, last_access_at_ms,
       created_at_ms, COALESCE(created_by_uuid,''),
       COALESCE(promoted_from_uuid,''), provisioning_status,
       archived_at_ms, COALESCE(archived_by_uuid,'')
FROM member_access`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMemberAccessRow(row rowScanner) (*store.MemberAccessRecord, error) {
	var (
		uuid, statusStr, provStatus string
		credHash                    []byte
		enabled                     int
		expiresMs, activatedMs      sql.NullInt64
		lastAccessMs                sql.NullInt64
		inactivityDays              sql.NullInt64
		createdMs                   int64
		createdBy, promotedFrom     string
		archivedMs                  sql.NullInt64
		archivedBy                  string
	)
	err := row.Scan(
		&uuid, &credHash, &statusStr, &enabled,
		&expiresMs, &inactivityDays, &activatedMs, &lastAccessMs,
		&createdMs, &createdBy, &promotedFrom, &provStatus,
		&archivedMs, &archivedBy,
	)
	if err != nil {
		return nil, err
	}

	rec := &store.MemberAccessRecord{
		UUID:               uuid,
		CredentialHash:     credHash,
		Status:             store.MemberStatus(statusStr),
		Enabled:            enabled == 1,
		CreatedAt:          time.UnixMilli(createdMs).UTC(),
		CreatedByUUID:      createdBy,
		PromotedFromUUID:   promotedFrom,
		ProvisioningStatus: store.ProvisioningStatus(provStatus),
		ArchivedByUUID:     archivedBy,
	}
	if expiresMs.Valid {
		t := time.UnixMilli(expiresMs.Int64).UTC()
		rec.ExpiresAt = &t
	}
	if inactivityDays.Valid {
		v := int(inactivityDays.Int64)
		rec.InactivityLimitDays = &v
	}
	if activatedMs.Valid {
		t := time.UnixMilli(activatedMs.Int64).UTC()
		rec.ActivatedAt = &t
	}
	if lastAccessMs.Valid {
		t := time.UnixMilli(lastAccessMs.Int64).UTC()
		rec.LastAccessAt = &t
	}
	if archivedMs.Valid {
		t := time.UnixMilli(archivedMs.Int64).UTC()
		rec.ArchivedAt = &t
	}
	return rec, nil
}

func scanMemberAccessRows(rows *sql.Rows) ([]store.MemberAccessRecord, error) {
	defer rows.Close()
	var members []store.MemberAccessRecord
	for rows.Next() {
		rec, err := scanMemberAccessRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan member_access: %w", err)
		}
		members = append(members, *rec)
	}
	return members, rows.Err()
}

func requireOneRow(res sql.Result, op string) error {
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
