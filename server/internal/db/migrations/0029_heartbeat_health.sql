-- 0029: module health reported in heartbeats.
--
-- door_closed has been in HeartbeatRequest since the start but was never
-- stored; reader_ok, strike_ok and dropped_events are new proto fields 9-11.
-- The booleans are NULL when the module did not report them (no reed switch,
-- not an access point, or older firmware), so "unknown" stays distinct from
-- "false". The unused strike_unlocked column from 0001 is left alone.
PRAGMA foreign_keys = ON;

ALTER TABLE module_heartbeats ADD COLUMN door_closed INTEGER CHECK (door_closed IN (0,1));
ALTER TABLE module_heartbeats ADD COLUMN reader_ok INTEGER CHECK (reader_ok IN (0,1));
ALTER TABLE module_heartbeats ADD COLUMN strike_ok INTEGER CHECK (strike_ok IN (0,1));
ALTER TABLE module_heartbeats ADD COLUMN dropped_events INTEGER;
