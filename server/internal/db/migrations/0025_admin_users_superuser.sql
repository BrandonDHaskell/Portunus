-- 0025: admin_users gains time-boxed accounts and a badge-identity link.
--
-- expires_at_ms: account expiry, enforced at login and per-request session
-- resolution. NULL = no expiry.
-- member_uuid: links the console account to the same human's member_access
-- row. Granting scope (0026) resolves through this link. ON DELETE SET NULL
-- so archiving/deleting a member fails the admin's scope closed rather than
-- blocking member lifecycle operations.

PRAGMA foreign_keys = ON;

ALTER TABLE admin_users ADD COLUMN expires_at_ms INTEGER;
ALTER TABLE admin_users ADD COLUMN member_uuid TEXT
  REFERENCES member_access(uuid) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uidx_admin_users_member_uuid
  ON admin_users(member_uuid) WHERE member_uuid IS NOT NULL;
