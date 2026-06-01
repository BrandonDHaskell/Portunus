-- 0017: admin_user_credentials — maps RFID badge hashes to admin users
--
-- An admin user may register one or more RFID badges. The credential_hash is
-- the canonical HMAC-SHA256(secret, raw_UID_bytes) value — same algorithm as
-- member_access.credential_hash, so the same HashCredentialID function is used.
--
-- The provisioning FSM resolves scan-1 against this table to attribute
-- provisioning events to the actual operator tapping their badge, replacing
-- the prior compile-time CONFIG_PORTUNUS_OPERATOR_UUID constant.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS admin_user_credentials (
  credential_hash BLOB    NOT NULL PRIMARY KEY CHECK (length(credential_hash) = 32),
  admin_user_uuid TEXT    NOT NULL REFERENCES admin_users(uuid) ON DELETE CASCADE,
  created_at_ms   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_admin_user_credentials_uuid
  ON admin_user_credentials(admin_user_uuid);
