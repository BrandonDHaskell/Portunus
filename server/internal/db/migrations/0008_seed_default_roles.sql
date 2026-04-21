-- PR 2: built-in default roles
--
-- is_system = 1 rows cannot be deleted through the admin API.
-- Permissions are assigned at runtime via role_permissions; this migration
-- only establishes the role identities.
--
-- INSERT OR IGNORE makes this migration idempotent on re-application.

INSERT OR IGNORE INTO roles
  (role_id, name, description, is_system, created_at_ms, updated_at_ms)
VALUES
  ('admin',
   'Administrator',
   'Full system access. All permissions granted.',
   1,
   CAST(strftime('%s','now') AS INTEGER) * 1000,
   CAST(strftime('%s','now') AS INTEGER) * 1000),

  ('operator',
   'Operator',
   'Day-to-day operations: provision members, grant and revoke module access.',
   1,
   CAST(strftime('%s','now') AS INTEGER) * 1000,
   CAST(strftime('%s','now') AS INTEGER) * 1000),

  ('viewer',
   'Viewer',
   'Read-only access to members, modules, and audit log.',
   1,
   CAST(strftime('%s','now') AS INTEGER) * 1000,
   CAST(strftime('%s','now') AS INTEGER) * 1000),

  ('member',
   'Member',
   'Standard member. Inherits default expiry policy from role.',
   0,
   CAST(strftime('%s','now') AS INTEGER) * 1000,
   CAST(strftime('%s','now') AS INTEGER) * 1000),

  ('guest',
   'Guest',
   'Temporary access with short default expiry.',
   0,
   CAST(strftime('%s','now') AS INTEGER) * 1000,
   CAST(strftime('%s','now') AS INTEGER) * 1000);
