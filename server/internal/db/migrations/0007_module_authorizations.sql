-- PR 2: module_authorizations join table
--
-- Default-deny: no row = no access, regardless of member status.
-- One active authorization per member per module (UNIQUE constraint).
-- Revocation is a soft-delete: revoked_at_ms is set, the row is kept for audit.
-- time_restriction is an optional JSON column for per-authorization policy
-- (e.g. business-hours-only); NULL means unrestricted.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS module_authorizations (
  authorization_id INTEGER PRIMARY KEY AUTOINCREMENT,
  member_uuid      TEXT    NOT NULL REFERENCES member_access(uuid) ON DELETE CASCADE,
  module_id        TEXT    NOT NULL REFERENCES modules(module_id)  ON DELETE CASCADE,
  granted_at_ms    INTEGER NOT NULL,
  granted_by_uuid  TEXT,
  expires_at_ms    INTEGER,
  revoked_at_ms    INTEGER,
  revoked_by_uuid  TEXT,
  time_restriction TEXT
);

-- Enforces one active (non-revoked) grant per member per module.
-- Revoked rows are kept for audit and do not conflict with new grants.
CREATE UNIQUE INDEX IF NOT EXISTS uidx_module_auth_active_grant
  ON module_authorizations(member_uuid, module_id)
  WHERE revoked_at_ms IS NULL;

-- Fast path for the access-check query: module + member, non-revoked only.
CREATE INDEX IF NOT EXISTS idx_module_auth_active
  ON module_authorizations(module_id, member_uuid)
  WHERE revoked_at_ms IS NULL;

-- Authorization expiry sweep.
CREATE INDEX IF NOT EXISTS idx_module_auth_expires
  ON module_authorizations(expires_at_ms)
  WHERE revoked_at_ms IS NULL AND expires_at_ms IS NOT NULL;
