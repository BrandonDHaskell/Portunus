package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	dbpkg "github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type RoleStore struct {
	db     *sql.DB
	writer *dbpkg.Worker
}

func NewRoleStore(db *sql.DB, writer *dbpkg.Worker) *RoleStore {
	return &RoleStore{db: db, writer: writer}
}

func (s *RoleStore) CreateRole(ctx context.Context, roleID, name, description string) error {
	now := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO roles(role_id, name, description, is_system, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, 0, ?, ?);
`, roleID, name, description, now, now)
		if err != nil {
			return fmt.Errorf("CreateRole: %w", err)
		}
		return nil
	})
}

func (s *RoleStore) GetRole(ctx context.Context, roleID string) (*store.RoleRecord, error) {
	var (
		id, name, desc string
		isSystem       int
		createdMs      int64
	)
	err := s.db.QueryRowContext(ctx, `
SELECT role_id, name, COALESCE(description,''), is_system, created_at_ms
FROM roles WHERE role_id = ?;
`, roleID).Scan(&id, &name, &desc, &isSystem, &createdMs)
	if err == sql.ErrNoRows {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetRole: %w", err)
	}
	return scanRoleRecord(id, name, desc, isSystem, createdMs), nil
}

func (s *RoleStore) ListRoles(ctx context.Context) ([]store.RoleRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT role_id, name, COALESCE(description,''), is_system, created_at_ms
FROM roles ORDER BY name;
`)
	if err != nil {
		return nil, fmt.Errorf("ListRoles: %w", err)
	}
	defer rows.Close()

	var roles []store.RoleRecord
	for rows.Next() {
		var (
			id, name, desc string
			isSystem       int
			createdMs      int64
		)
		if err := rows.Scan(&id, &name, &desc, &isSystem, &createdMs); err != nil {
			return nil, fmt.Errorf("ListRoles scan: %w", err)
		}
		roles = append(roles, *scanRoleRecord(id, name, desc, isSystem, createdMs))
	}
	return roles, rows.Err()
}

func (s *RoleStore) UpdateRole(ctx context.Context, roleID, name, description string) error {
	now := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var isSystem int
		err := tx.QueryRowContext(ctx, `SELECT is_system FROM roles WHERE role_id = ?;`, roleID).Scan(&isSystem)
		if err == sql.ErrNoRows {
			return store.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("UpdateRole check: %w", err)
		}
		if isSystem == 1 {
			return store.ErrRoleIsSystem
		}
		res, err := tx.ExecContext(ctx, `
UPDATE roles SET name = ?, description = ?, updated_at_ms = ?
WHERE role_id = ?;
`, name, description, now, roleID)
		if err != nil {
			return fmt.Errorf("UpdateRole: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return store.ErrNotFound
		}
		return nil
	})
}

func (s *RoleStore) DeleteRole(ctx context.Context, roleID string) error {
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var isSystem int
		err := tx.QueryRowContext(ctx, `SELECT is_system FROM roles WHERE role_id = ?;`, roleID).Scan(&isSystem)
		if err == sql.ErrNoRows {
			return store.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("DeleteRole check: %w", err)
		}
		if isSystem == 1 {
			return store.ErrRoleIsSystem
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM roles WHERE role_id = ?;`, roleID)
		if err != nil {
			return fmt.Errorf("DeleteRole: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return store.ErrNotFound
		}
		return nil
	})
}

func (s *RoleStore) SetRolePermissions(ctx context.Context, roleID string, permissions []string) error {
	now := time.Now().UTC().UnixMilli()
	return s.writer.Do(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?;`, roleID); err != nil {
			return fmt.Errorf("SetRolePermissions delete: %w", err)
		}
		for _, p := range permissions {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO role_permissions(role_id, permission, granted_at_ms) VALUES (?, ?, ?);
`, roleID, p, now); err != nil {
				return fmt.Errorf("SetRolePermissions insert %q: %w", p, err)
			}
		}
		return nil
	})
}

func (s *RoleStore) GetRolePermissions(ctx context.Context, roleID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT permission FROM role_permissions WHERE role_id = ? ORDER BY permission;
`, roleID)
	if err != nil {
		return nil, fmt.Errorf("GetRolePermissions: %w", err)
	}
	defer rows.Close()

	var perms []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("GetRolePermissions scan: %w", err)
		}
		perms = append(perms, p)
	}
	return perms, rows.Err()
}

func scanRoleRecord(id, name, desc string, isSystem int, createdMs int64) *store.RoleRecord {
	return &store.RoleRecord{
		RoleID:      id,
		Name:        name,
		Description: desc,
		IsSystem:    isSystem == 1,
		CreatedAt:   time.UnixMilli(createdMs).UTC(),
	}
}
