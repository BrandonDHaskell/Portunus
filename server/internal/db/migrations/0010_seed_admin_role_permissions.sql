-- PR 3: seed all permissions into the admin role
--
-- The admin role receives every permission defined in the permissions package.
-- INSERT OR IGNORE makes this idempotent on re-application.

INSERT OR IGNORE INTO role_permissions (role_id, permission, granted_at_ms)
VALUES
  ('admin', 'module.list',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module.get',               CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module.register',          CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module.revoke',            CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module.delete',            CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'credential.list',          CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'credential.register',      CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'credential.update_status', CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'credential.delete',        CAST(strftime('%s','now') AS INTEGER) * 1000),
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
  ('admin', 'member.provision',         CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'member.list',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'member.view',              CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'member.assign_role',       CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'member.disable',           CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'member.archive',           CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module_auth.grant',        CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module_auth.revoke',       CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('admin', 'module_auth.list',         CAST(strftime('%s','now') AS INTEGER) * 1000);
