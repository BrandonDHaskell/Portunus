-- Seed permissions for the operator and viewer system roles.
--
-- operator: day-to-day operations — provision members, manage credentials,
--           grant and revoke module access. No destructive or administrative
--           actions (no delete, no role/admin-user management).
--
-- viewer:   read-only access to members, modules, doors, credentials,
--           module authorizations, and roles.
--
-- INSERT OR IGNORE makes this migration idempotent on re-application.

INSERT OR IGNORE INTO role_permissions (role_id, permission, granted_at_ms)
VALUES
  -- operator
  ('operator', 'module.list',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'module.get',               CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'credential.list',          CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'credential.register',      CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'credential.update_status', CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'door.list',               CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'member.provision',         CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'member.list',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'member.view',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'member.assign_role',       CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'member.disable',           CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'module_auth.grant',        CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'module_auth.revoke',       CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'module_auth.list',         CAST(strftime('%s','now') AS INTEGER) * 1000),

  -- viewer
  ('viewer', 'module.list',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'module.get',               CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'credential.list',          CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'door.list',               CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'member.list',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'member.view',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'module_auth.list',         CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer', 'role.list',                CAST(strftime('%s','now') AS INTEGER) * 1000);
