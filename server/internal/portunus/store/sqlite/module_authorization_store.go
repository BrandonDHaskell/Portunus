package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	dbpkg "github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type ModuleAuthorizationStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewModuleAuthorizationStore(db *sql.DB, writer *dbpkg.Worker) *ModuleAuthorizationStore {
	return &ModuleAuthorizationStore{db: db, writer: writer}
}

func (s *ModuleAuthorizationStore) GrantAuthorization(ctx context.Context,
	memberUUID, moduleID, grantedByUUID string,
	expiresAt *time.Time, timeRestriction string,
) error {
	now := time.Now().UTC().UnixMilli()
	var expiresAtMs *int64
	if expiresAt != nil {
		ms := expiresAt.UTC().UnixMilli()
		expiresAtMs = &ms
	}
	var restrictionVal *string
	if timeRestriction != "" {
		restrictionVal = &timeRestriction
	}

	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Check for an existing non-revoked authorization.
		var existing int
		err := tx.QueryRowContext(ctx, `
SELECT 1 FROM module_authorizations
 WHERE member_uuid = ? AND module_id = ? AND revoked_at_ms IS NULL;
`, memberUUID, moduleID).Scan(&existing)
		if err == nil {
			return store.ErrAuthorizationAlreadyExists
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("GrantAuthorization conflict check: %w", err)
		}

		_, err = tx.ExecContext(ctx, `
INSERT INTO module_authorizations(
  member_uuid, module_id, granted_at_ms, granted_by_uuid,
  expires_at_ms, time_restriction)
VALUES (?, ?, ?, ?, ?, ?);
`, memberUUID, moduleID, now, grantedByUUID, expiresAtMs, restrictionVal)
		if err != nil {
			return fmt.Errorf("GrantAuthorization insert: %w", err)
		}
		return nil
	})
}

func (s *ModuleAuthorizationStore) RevokeAuthorization(ctx context.Context,
	memberUUID, moduleID, revokedByUUID string,
) error {
	now := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
UPDATE module_authorizations
   SET revoked_at_ms = ?, revoked_by_uuid = ?
 WHERE member_uuid = ? AND module_id = ? AND revoked_at_ms IS NULL;
`, now, revokedByUUID, memberUUID, moduleID)
		if err != nil {
			return fmt.Errorf("RevokeAuthorization: %w", err)
		}
		return requireOneRow(res, "RevokeAuthorization")
	})
}

func (s *ModuleAuthorizationStore) GetAuthorization(ctx context.Context,
	memberUUID, moduleID string,
) (*store.ModuleAuthorizationRecord, error) {
	row := s.db.QueryRowContext(ctx,
		moduleAuthSelectSQL+` WHERE member_uuid = ? AND module_id = ? ORDER BY granted_at_ms DESC LIMIT 1;`,
		memberUUID, moduleID)
	rec, err := scanModuleAuthRow(row)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	return rec, err
}

func (s *ModuleAuthorizationStore) ListByMember(ctx context.Context, memberUUID string) ([]store.ModuleAuthorizationRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		moduleAuthSelectSQL+` WHERE member_uuid = ? ORDER BY granted_at_ms DESC;`, memberUUID)
	if err != nil {
		return nil, fmt.Errorf("ListByMember: %w", err)
	}
	return scanModuleAuthRows(rows)
}

func (s *ModuleAuthorizationStore) ListByModule(ctx context.Context, moduleID string) ([]store.ModuleAuthorizationRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		moduleAuthSelectSQL+` WHERE module_id = ? ORDER BY granted_at_ms DESC;`, moduleID)
	if err != nil {
		return nil, fmt.Errorf("ListByModule: %w", err)
	}
	return scanModuleAuthRows(rows)
}

// ── query helpers ─────────────────────────────────────────────────────────────

const moduleAuthSelectSQL = `
SELECT authorization_id, member_uuid, module_id,
       granted_at_ms, COALESCE(granted_by_uuid,''),
       expires_at_ms, revoked_at_ms, COALESCE(revoked_by_uuid,''),
       COALESCE(time_restriction,'')
FROM module_authorizations`

func scanModuleAuthRow(row rowScanner) (*store.ModuleAuthorizationRecord, error) {
	var (
		id                                int64
		memberUUID, moduleID              string
		grantedMs                         int64
		grantedBy                         string
		expiresMs, revokedMs              sql.NullInt64
		revokedBy, timeRestriction        string
	)
	err := row.Scan(
		&id, &memberUUID, &moduleID,
		&grantedMs, &grantedBy,
		&expiresMs, &revokedMs, &revokedBy,
		&timeRestriction,
	)
	if err != nil {
		return nil, err
	}

	rec := &store.ModuleAuthorizationRecord{
		AuthorizationID: id,
		MemberUUID:      memberUUID,
		ModuleID:        moduleID,
		GrantedAt:       time.UnixMilli(grantedMs).UTC(),
		GrantedByUUID:   grantedBy,
		RevokedByUUID:   revokedBy,
		TimeRestriction: timeRestriction,
	}
	if expiresMs.Valid {
		t := time.UnixMilli(expiresMs.Int64).UTC()
		rec.ExpiresAt = &t
	}
	if revokedMs.Valid {
		t := time.UnixMilli(revokedMs.Int64).UTC()
		rec.RevokedAt = &t
	}
	return rec, nil
}

func scanModuleAuthRows(rows *sql.Rows) ([]store.ModuleAuthorizationRecord, error) {
	defer rows.Close()
	var auths []store.ModuleAuthorizationRecord
	for rows.Next() {
		rec, err := scanModuleAuthRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan module_authorizations: %w", err)
		}
		auths = append(auths, *rec)
	}
	return auths, rows.Err()
}
