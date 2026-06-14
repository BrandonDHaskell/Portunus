-- 0028: seed member.edit permission for admin and operator roles.
--
-- PR #61 (feat/refactor-admin-member-separation) dropped MemberAssignRole but
-- never added a replacement edit permission.  This seeds member.edit so that
-- admins and operators can update a member's policy fields (expires_at,
-- inactivity_limit_days) after activation.
--
-- INSERT OR IGNORE is idempotent: safe to re-apply.

INSERT OR IGNORE INTO role_permissions (role_id, permission, granted_at_ms) VALUES
  ('admin',    'member.edit', CAST(strftime('%s','now') AS INTEGER) * 1000),
  ('operator', 'member.edit', CAST(strftime('%s','now') AS INTEGER) * 1000);
