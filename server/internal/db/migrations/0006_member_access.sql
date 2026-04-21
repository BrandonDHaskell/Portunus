-- PR 2: member_access table
--
-- Unified identity model: members and guests share one table, distinguished
-- by role_id.  Guest-to-member promotion is a role reassignment on the
-- existing row — the UUID and credential_hash never change.
--
-- credential_hash is nullable until physical enrollment.  The UNIQUE
-- constraint covers all non-NULL values across all statuses.  The partial
-- index on (credential_hash) WHERE status = 'active' keeps the hot
-- access-check path fast regardless of expired row accumulation.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS member_access (
  uuid                  TEXT    PRIMARY KEY,
  role_id               TEXT    NOT NULL REFERENCES roles(role_id),
  credential_hash       BLOB    UNIQUE
                                CHECK (credential_hash IS NULL OR length(credential_hash) = 32),
  status                TEXT    NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active','suspended','expired','archived')),
  enabled               INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
  expires_at_ms         INTEGER,
  inactivity_limit_days INTEGER,
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

-- Fast path for the access-check query: credential → active member.
CREATE INDEX IF NOT EXISTS idx_member_access_credential_active
  ON member_access(credential_hash) WHERE status = 'active';

-- Hard-expiry sweep: find active rows whose deadline has passed.
CREATE INDEX IF NOT EXISTS idx_member_access_expires
  ON member_access(expires_at_ms)
  WHERE status = 'active' AND expires_at_ms IS NOT NULL;

-- Inactivity sweep: find active rows with a last-access timestamp.
CREATE INDEX IF NOT EXISTS idx_member_access_last_access
  ON member_access(last_access_at_ms) WHERE status = 'active';

-- Pending-authorization queue ordered by arrival time.
CREATE INDEX IF NOT EXISTS idx_member_access_pending
  ON member_access(created_at_ms)
  WHERE provisioning_status = 'pending_authorization';
