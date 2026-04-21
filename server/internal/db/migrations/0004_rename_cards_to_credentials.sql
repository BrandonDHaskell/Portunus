-- PR 1: Terminology rename — cards → credentials
--
-- SQLite does not support ALTER TABLE ... RENAME COLUMN directly on older
-- versions, so we use the standard SQLite migration pattern:
--   1. Create the new table with correct names.
--   2. Copy data.
--   3. Drop the old table.
--   4. Rename the new table.
--
-- Foreign-key enforcement is disabled during the migration to avoid
-- constraint violations during the transition.

PRAGMA foreign_keys = OFF;

-- Rename cards → credentials (with renamed columns)
CREATE TABLE credentials_new (
  credential_hash BLOB PRIMARY KEY CHECK (length(credential_hash) = 32),
  credential_tag  TEXT,
  status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','lost')),
  created_at_ms   INTEGER NOT NULL,
  updated_at_ms   INTEGER NOT NULL,
  last_seen_at_ms INTEGER
);

INSERT INTO credentials_new
  SELECT card_id_hash, card_tag, status, created_at_ms, updated_at_ms, last_seen_at_ms
  FROM cards;

DROP TABLE cards;

ALTER TABLE credentials_new RENAME TO credentials;

-- Rename card_id_hash → credential_hash in access_events
CREATE TABLE access_events_new (
  access_event_id INTEGER PRIMARY KEY AUTOINCREMENT,

  module_id TEXT NOT NULL REFERENCES modules(module_id) ON DELETE CASCADE,
  door_id   TEXT REFERENCES doors(door_id) ON DELETE SET NULL,

  received_at_ms  INTEGER NOT NULL,
  requested_at_ms INTEGER,
  door_closed     INTEGER CHECK (door_closed IN (0,1)),

  credential_hash BLOB
    REFERENCES credentials(credential_hash) ON DELETE SET NULL
    CHECK (credential_hash IS NULL OR length(credential_hash) = 32),

  decision_granted INTEGER NOT NULL CHECK (decision_granted IN (0,1)),
  decision_reason  TEXT NOT NULL,

  policy_version INTEGER,
  decided_at_ms  INTEGER NOT NULL
);

INSERT INTO access_events_new
  SELECT access_event_id, module_id, door_id, received_at_ms, requested_at_ms,
         door_closed, card_id_hash, decision_granted, decision_reason,
         policy_version, decided_at_ms
  FROM access_events;

DROP TABLE access_events;

ALTER TABLE access_events_new RENAME TO access_events;

PRAGMA foreign_keys = ON;

-- Recreate indexes with updated names
CREATE INDEX IF NOT EXISTS idx_credentials_last_seen
  ON credentials(last_seen_at_ms);

CREATE INDEX IF NOT EXISTS idx_access_credential_time
  ON access_events(credential_hash, received_at_ms);
