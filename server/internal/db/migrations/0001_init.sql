PRAGMA foreign_keys = ON;

-- 1) Doors
CREATE TABLE IF NOT EXISTS doors (
  door_id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  location TEXT,
  created_at_ms INTEGER NOT NULL,
  updated_at_ms INTEGER NOT NULL
);

-- 2) Modules
CREATE TABLE IF NOT EXISTS modules (
  module_id TEXT PRIMARY KEY,
  door_id TEXT REFERENCES doors(door_id) ON DELETE SET NULL,

  display_name TEXT,
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
  commissioned_at_ms INTEGER,
  revoked_at_ms INTEGER,

  last_seen_at_ms INTEGER,
  last_ip TEXT,
  last_fw_version TEXT,
  last_wifi_rssi INTEGER,
  last_strike_unlocked INTEGER CHECK (last_strike_unlocked IN (0,1)),

  created_at_ms INTEGER NOT NULL,
  updated_at_ms INTEGER NOT NULL
);

-- 3) Heartbeats
CREATE TABLE IF NOT EXISTS module_heartbeats (
  heartbeat_id INTEGER PRIMARY KEY AUTOINCREMENT,
  module_id TEXT NOT NULL REFERENCES modules(module_id) ON DELETE CASCADE,

  received_at_ms INTEGER NOT NULL,

  seq INTEGER,
  uptime_ms INTEGER,
  fw_version TEXT,
  wifi_rssi INTEGER,
  strike_unlocked INTEGER CHECK (strike_unlocked IN (0,1)),
  ip TEXT
);

-- 4) Cards (hashed IDs, not raw)
CREATE TABLE IF NOT EXISTS cards (
  card_id_hash BLOB PRIMARY KEY CHECK (length(card_id_hash) = 32),
  card_tag TEXT,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','lost')),
  created_at_ms INTEGER NOT NULL,
  updated_at_ms INTEGER NOT NULL,
  last_seen_at_ms INTEGER
);

-- 5) Access events
CREATE TABLE IF NOT EXISTS access_events (
  access_event_id INTEGER PRIMARY KEY AUTOINCREMENT,

  module_id TEXT NOT NULL REFERENCES modules(module_id) ON DELETE CASCADE,
  door_id TEXT REFERENCES doors(door_id) ON DELETE SET NULL,

  received_at_ms INTEGER NOT NULL,
  requested_at_ms INTEGER,
  door_closed INTEGER CHECK (door_closed IN (0,1)),

  card_id_hash BLOB
    REFERENCES cards(card_id_hash) ON DELETE SET NULL
    CHECK (card_id_hash IS NULL OR length(card_id_hash) = 32),

  decision_granted INTEGER NOT NULL CHECK (decision_granted IN (0,1)),
  decision_reason TEXT NOT NULL,

  policy_version INTEGER,
  decided_at_ms INTEGER NOT NULL
);