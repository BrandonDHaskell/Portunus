-- 0023: Roleless members — drop role_id, add activated_at_ms.
--
-- Console privilege (admin_users + roles) and physical access policy
-- (member_access + module_authorizations) are permanently separated.
-- Members no longer carry a role_id FK; the roles table becomes
-- admin-console-only in subsequent migrations.
--
-- activated_at_ms is set by ApprovePending; the inactivity sweep uses
-- COALESCE(last_access_at_ms, activated_at_ms, created_at_ms) so pending
-- dwell time does not consume the inactivity window.

CREATE TABLE member_access_new (
  uuid                  TEXT    PRIMARY KEY,
  credential_hash       BLOB    UNIQUE
                                CHECK (credential_hash IS NULL OR length(credential_hash) = 32),
  status                TEXT    NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active','suspended','expired','archived')),
  enabled               INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
  expires_at_ms         INTEGER,
  inactivity_limit_days INTEGER,
  activated_at_ms       INTEGER,
  last_access_at_ms     INTEGER,
  created_at_ms         INTEGER NOT NULL,
  created_by_uuid       TEXT,
  promoted_from_uuid    TEXT,
  provisioning_status   TEXT    NOT NULL DEFAULT 'active'
                                CHECK (provisioning_status IN
                                  ('pending_authorization','active','incomplete')),
  archived_at_ms        INTEGER,
  archived_by_uuid      TEXT
);

INSERT INTO member_access_new
  SELECT uuid, credential_hash, status, enabled,
         expires_at_ms, inactivity_limit_days,
         NULL,
         last_access_at_ms, created_at_ms, created_by_uuid, promoted_from_uuid,
         provisioning_status, archived_at_ms, archived_by_uuid
  FROM member_access;

DROP TABLE member_access;
ALTER TABLE member_access_new RENAME TO member_access;

CREATE INDEX IF NOT EXISTS idx_member_access_credential_active
  ON member_access(credential_hash) WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_member_access_expires
  ON member_access(expires_at_ms)
  WHERE status = 'active' AND expires_at_ms IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_member_access_last_access
  ON member_access(last_access_at_ms) WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_member_access_pending
  ON member_access(created_at_ms)
  WHERE provisioning_status = 'pending_authorization';

DELETE FROM role_permissions WHERE permission = 'member.assign_role';
