-- Modules
CREATE INDEX IF NOT EXISTS idx_modules_last_seen
  ON modules(last_seen_at_ms);

-- Heartbeats
CREATE INDEX IF NOT EXISTS idx_heartbeats_module_time
  ON module_heartbeats(module_id, received_at_ms);

-- Helpful for retention pruning:
CREATE INDEX IF NOT EXISTS idx_heartbeats_time
  ON module_heartbeats(received_at_ms);

-- Cards
CREATE INDEX IF NOT EXISTS idx_cards_last_seen
  ON cards(last_seen_at_ms);

-- Access events
CREATE INDEX IF NOT EXISTS idx_access_module_time
  ON access_events(module_id, received_at_ms);

CREATE INDEX IF NOT EXISTS idx_access_card_time
  ON access_events(card_id_hash, received_at_ms);

-- Helpful for retention / reporting:
CREATE INDEX IF NOT EXISTS idx_access_time
  ON access_events(received_at_ms);