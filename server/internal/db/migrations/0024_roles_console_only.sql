-- 0024: roles is console RBAC only.
--
-- member/guest rows are deleted (member_access no longer references roles as
-- of 0023; a guest is now just a member with a short expiry and narrow
-- grants). default_expiry_days / default_inactivity_days were member-only
-- semantics and are dropped. 12-step rebuild because the columns carry CHECKs
-- in the proposed-but-rejected schema and modernc DROP COLUMN behavior should
-- not be assumed across versions.

PRAGMA foreign_keys = OFF;

DELETE FROM roles WHERE role_id IN ('member', 'guest');

CREATE TABLE roles_new (
  role_id       TEXT    PRIMARY KEY,
  name          TEXT    NOT NULL UNIQUE,
  description   TEXT,
  is_system     INTEGER NOT NULL DEFAULT 0 CHECK (is_system IN (0,1)),
  created_at_ms INTEGER NOT NULL,
  updated_at_ms INTEGER NOT NULL
);

INSERT INTO roles_new
  SELECT role_id, name, description, is_system, created_at_ms, updated_at_ms
  FROM roles;

DROP TABLE roles;
ALTER TABLE roles_new RENAME TO roles;

PRAGMA foreign_keys = ON;
