-- Seed audit_log.list permission for admin, operator, and viewer roles.
--
-- All three staff roles can read the audit log; write access is implicit
-- (the server writes entries directly, not through RBAC-checked API calls).
--
-- INSERT OR IGNORE makes this migration idempotent on re-application.

INSERT OR IGNORE INTO role_permissions (role_id, permission, granted_at_ms)
VALUES
  ('admin',    'audit_log.list', CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'audit_log.list', CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('viewer',   'audit_log.list', CAST(strftime('%s','now') AS INTEGER) * 1000);
