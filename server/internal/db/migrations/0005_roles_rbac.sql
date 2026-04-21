-- PR 2: RBAC tables — roles and role_permissions
--
-- Permissions are atomic named constants defined in code (PR 3).
-- Roles are runtime data: admin-configurable, with is_system protecting
-- built-in rows from deletion.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS roles (
  role_id                 TEXT    PRIMARY KEY,
  name                    TEXT    NOT NULL UNIQUE,
  description             TEXT,
  is_system               INTEGER NOT NULL DEFAULT 0 CHECK (is_system IN (0,1)),
  default_expiry_days     INTEGER,
  default_inactivity_days INTEGER,
  created_at_ms           INTEGER NOT NULL,
  updated_at_ms           INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS role_permissions (
  role_id       TEXT    NOT NULL REFERENCES roles(role_id) ON DELETE CASCADE,
  permission    TEXT    NOT NULL,
  granted_at_ms INTEGER NOT NULL,
  PRIMARY KEY (role_id, permission)
);

CREATE INDEX IF NOT EXISTS idx_role_permissions_role
  ON role_permissions(role_id);
