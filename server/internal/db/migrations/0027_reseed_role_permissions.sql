-- 0027: reseed role permissions after the 0024 bug.
--
-- Migration 0024 used DROP TABLE roles inside a transaction where PRAGMA
-- foreign_keys = OFF is silently ignored (SQLite disallows changing FK
-- enforcement mid-transaction). Because FK was ON when roles was dropped,
-- SQLite cascade-deleted all role_permissions rows via the ON DELETE CASCADE
-- FK on role_permissions.role_id.
--
-- This migration re-seeds the complete, current permission set for the three
-- system roles: admin (all 28 permissions), operator (day-to-day, scoped
-- grant/revoke), and viewer (read-only).
--
-- INSERT OR IGNORE is idempotent: harmless if applied to a DB where
-- permissions were not wiped.

INSERT OR IGNORE INTO role_permissions (role_id, permission, granted_at_ms) VALUES
  -- admin: every permission defined in the permissions package (28 total)
  ('admin', 'module.list',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module.get',               CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module.register',          CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module.revoke',            CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module.delete',            CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'door.list',                CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'door.register',            CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'door.delete',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'admin_user.create',        CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'admin_user.list',          CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'admin_user.edit',          CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'admin_user.disable',       CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'role.list',                CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'role.create',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'role.edit',                CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'role.delete',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'role.assign_permissions',  CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'member.enroll',            CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'member.list',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'member.view',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'member.disable',           CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'member.archive',           CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module_auth.grant_held',   CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module_auth.grant_any',    CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module_auth.revoke_held',  CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module_auth.revoke_any',   CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module_auth.list',         CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'audit_log.list',           CAST(strftime('%s','now') AS INTEGER) * 1000),

  -- operator: day-to-day operations with scoped grant/revoke
  ('operator', 'module.list',             CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'module.get',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'door.list',               CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'member.enroll',           CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'member.list',             CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'member.view',             CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'member.disable',          CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'module_auth.grant_held',  CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'module_auth.revoke_held', CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'module_auth.list',        CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'audit_log.list',          CAST(strftime('%s','now') AS INTEGER) * 1000),

  -- viewer: read-only
  ('viewer', 'module.list',    CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'module.get',     CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'door.list',      CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'member.list',    CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'member.view',    CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'module_auth.list', CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'role.list',      CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'audit_log.list', CAST(strftime('%s','now') AS INTEGER) * 1000);
