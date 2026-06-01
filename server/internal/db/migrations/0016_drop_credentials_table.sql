-- 0016: remove the orphaned credentials table and its role_permissions rows
--
-- The credentials table was never read by the live access path (member_access
-- is the live table). Retaining it created an operator trap: cards registered
-- there never granted door access. All credential management now goes through
-- member_access. Existing data is dropped; affected members must re-enroll.
--
-- access_events.credential_hash is kept as an audit column but its FK to
-- credentials is dropped (no longer meaningful once the table is gone). The
-- 12-step rebuild is required because SQLite does not support DROP CONSTRAINT.

PRAGMA foreign_keys = OFF;

-- Rebuild access_events without the FK to credentials.
CREATE TABLE access_events_new (
  access_event_id INTEGER PRIMARY KEY AUTOINCREMENT,

  module_id TEXT NOT NULL REFERENCES modules(module_id) ON DELETE CASCADE,
  door_id   TEXT REFERENCES doors(door_id) ON DELETE SET NULL,

  received_at_ms  INTEGER NOT NULL,
  requested_at_ms INTEGER,
  door_closed     INTEGER CHECK (door_closed IN (0,1)),

  credential_hash BLOB
    CHECK (credential_hash IS NULL OR length(credential_hash) = 32),

  decision_granted INTEGER NOT NULL CHECK (decision_granted IN (0,1)),
  decision_reason  TEXT NOT NULL,

  policy_version INTEGER,
  decided_at_ms  INTEGER NOT NULL
);

INSERT INTO access_events_new
  SELECT access_event_id, module_id, door_id, received_at_ms, requested_at_ms,
         door_closed, credential_hash, decision_granted, decision_reason,
         policy_version, decided_at_ms
  FROM access_events;

DROP TABLE access_events;

ALTER TABLE access_events_new RENAME TO access_events;

CREATE INDEX IF NOT EXISTS idx_access_credential_time
  ON access_events(credential_hash, received_at_ms);

-- Now safe to drop credentials (no FK pointing at it remains).
DROP TABLE IF EXISTS credentials;

DELETE FROM role_permissions
WHERE permission IN (
  'credential.list',
  'credential.register',
  'credential.update_status',
  'credential.delete'
);

PRAGMA foreign_keys = ON;
