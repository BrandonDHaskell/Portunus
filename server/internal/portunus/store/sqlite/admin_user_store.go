package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	dbpkg "github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type AdminUserStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewAdminUserStore(db *sql.DB, writer *dbpkg.Worker) *AdminUserStore {
	return &AdminUserStore{db: db, writer: writer}
}

func (s *AdminUserStore) CreateAdminUser(ctx context.Context, uuid, username, passwordHash, roleID string) error {
	now := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO admin_users(uuid, username, password_hash, role_id, enabled, must_change_pw, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, 1, 1, ?, ?);
`, uuid, username, passwordHash, roleID, now, now)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return store.ErrUsernameAlreadyExists
			}
			return fmt.Errorf("CreateAdminUser: %w", err)
		}
		return nil
	})
}

func (s *AdminUserStore) GetAdminUserByUsername(ctx context.Context, username string) (*store.AdminUserRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT uuid, username, password_hash, COALESCE(role_id,'admin'), COALESCE(enabled,1),
       must_change_pw, created_at_ms, updated_at_ms, last_login_at_ms
FROM admin_users WHERE username = ?;
`, username)
	return scanAdminUser(row)
}

func (s *AdminUserStore) GetAdminUserByUUID(ctx context.Context, uuid string) (*store.AdminUserRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT uuid, username, password_hash, COALESCE(role_id,'admin'), COALESCE(enabled,1),
       must_change_pw, created_at_ms, updated_at_ms, last_login_at_ms
FROM admin_users WHERE uuid = ?;
`, uuid)
	return scanAdminUser(row)
}

func (s *AdminUserStore) ListAdminUsers(ctx context.Context) ([]store.AdminUserRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT uuid, username, password_hash, COALESCE(role_id,'admin'), COALESCE(enabled,1),
       must_change_pw, created_at_ms, updated_at_ms, last_login_at_ms
FROM admin_users ORDER BY username;
`)
	if err != nil {
		return nil, fmt.Errorf("ListAdminUsers: %w", err)
	}
	defer rows.Close()

	var users []store.AdminUserRecord
	for rows.Next() {
		rec, err := scanAdminUserRow(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *rec)
	}
	return users, rows.Err()
}

func (s *AdminUserStore) SetMustChangePW(ctx context.Context, uuid string, mustChange bool) error {
	flag := 0
	if mustChange {
		flag = 1
	}
	now := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE admin_users SET must_change_pw = ?, updated_at_ms = ? WHERE uuid = ?;`,
			flag, now, uuid)
		if err != nil {
			return fmt.Errorf("SetMustChangePW: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return store.ErrNotFound
		}
		return nil
	})
}

func (s *AdminUserStore) UpdatePasswordHash(ctx context.Context, uuid, passwordHash string) error {
	now := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE admin_users SET password_hash = ?, must_change_pw = 0, updated_at_ms = ? WHERE uuid = ?;`,
			passwordHash, now, uuid)
		if err != nil {
			return fmt.Errorf("UpdatePasswordHash: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return store.ErrNotFound
		}
		return nil
	})
}

func (s *AdminUserStore) UpdateLastLogin(ctx context.Context, uuid string, t time.Time) error {
	ms := t.UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE admin_users SET last_login_at_ms = ? WHERE uuid = ?;`, ms, uuid)
		if err != nil {
			return fmt.Errorf("UpdateLastLogin: %w", err)
		}
		return nil
	})
}

func (s *AdminUserStore) SetAdminUserEnabled(ctx context.Context, uuid string, enabled bool) error {
	flag := 0
	if enabled {
		flag = 1
	}
	now := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE admin_users SET enabled = ?, updated_at_ms = ? WHERE uuid = ?;`,
			flag, now, uuid)
		if err != nil {
			return fmt.Errorf("SetAdminUserEnabled: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return store.ErrNotFound
		}
		return nil
	})
}

func (s *AdminUserStore) SetAdminUserRole(ctx context.Context, uuid, roleID string) error {
	now := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE admin_users SET role_id = ?, updated_at_ms = ? WHERE uuid = ?;`,
			roleID, now, uuid)
		if err != nil {
			return fmt.Errorf("SetAdminUserRole: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return store.ErrNotFound
		}
		return nil
	})
}

func (s *AdminUserStore) AnyAdminExists(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users LIMIT 1;`).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("AnyAdminExists: %w", err)
	}
	return n > 0, nil
}

// scanAdminUser scans a single *sql.Row.
func scanAdminUser(row *sql.Row) (*store.AdminUserRecord, error) {
	var (
		uuid, username, hash, roleID string
		enabled, mustChange          int
		createdMs, updatedMs         int64
		lastLoginMs                  sql.NullInt64
	)
	err := row.Scan(&uuid, &username, &hash, &roleID, &enabled, &mustChange, &createdMs, &updatedMs, &lastLoginMs)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanAdminUser: %w", err)
	}
	return buildAdminUserRecord(uuid, username, hash, roleID, enabled, mustChange, createdMs, updatedMs, lastLoginMs), nil
}

// scanAdminUserRow scans a row from *sql.Rows.
func scanAdminUserRow(rows *sql.Rows) (*store.AdminUserRecord, error) {
	var (
		uuid, username, hash, roleID string
		enabled, mustChange          int
		createdMs, updatedMs         int64
		lastLoginMs                  sql.NullInt64
	)
	if err := rows.Scan(&uuid, &username, &hash, &roleID, &enabled, &mustChange, &createdMs, &updatedMs, &lastLoginMs); err != nil {
		return nil, fmt.Errorf("scanAdminUserRow: %w", err)
	}
	return buildAdminUserRecord(uuid, username, hash, roleID, enabled, mustChange, createdMs, updatedMs, lastLoginMs), nil
}

func buildAdminUserRecord(uuid, username, hash, roleID string, enabled, mustChange int, createdMs, updatedMs int64, lastLoginMs sql.NullInt64) *store.AdminUserRecord {
	rec := &store.AdminUserRecord{
		UUID:         uuid,
		Username:     username,
		PasswordHash: hash,
		RoleID:       roleID,
		Enabled:      enabled == 1,
		MustChangePW: mustChange == 1,
		CreatedAt:    time.UnixMilli(createdMs).UTC(),
		UpdatedAt:    time.UnixMilli(updatedMs).UTC(),
	}
	if lastLoginMs.Valid {
		t := time.UnixMilli(lastLoginMs.Int64).UTC()
		rec.LastLoginAt = &t
	}
	return rec
}
