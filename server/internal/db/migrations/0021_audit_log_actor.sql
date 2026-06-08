-- Rebuild audit_log to support both admin and member actors.
-- The original table FK-constrained actor_uuid to admin_users only, which
-- prevents recording member actors (operator enrolment) and system actions
-- (device capture). The table is empty and unwired so a DROP + recreate is safe.

DROP TABLE IF EXISTS audit_log;

CREATE TABLE audit_log (
  id             TEXT    PRIMARY KEY,
  occurred_at_ms INTEGER NOT NULL,
  actor_uuid     TEXT,
  actor_type     TEXT    NOT NULL DEFAULT 'system'
                         CHECK (actor_type IN ('admin', 'member', 'system')),
  action         TEXT    NOT NULL,
  resource_type  TEXT,
  resource_id    TEXT,
  details        TEXT,
  ip_address     TEXT,
  result         TEXT    NOT NULL DEFAULT 'success' CHECK (result IN ('success', 'failure'))
);

CREATE INDEX IF NOT EXISTS idx_audit_log_occurred ON audit_log(occurred_at_ms);
CREATE INDEX IF NOT EXISTS idx_audit_log_actor    ON audit_log(actor_uuid);
CREATE INDEX IF NOT EXISTS idx_audit_log_action   ON audit_log(action);
CREATE INDEX IF NOT EXISTS idx_audit_log_resource ON audit_log(resource_type, resource_id);
