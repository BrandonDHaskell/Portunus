-- Audit log table.
--
-- Records every state-changing action performed through the admin API.
-- actor_uuid is nullable to accommodate future system-generated events.
-- details holds a JSON blob with action-specific context (e.g. which fields
-- changed, previous values).
-- result distinguishes actions that were attempted but rejected (e.g. denied
-- by RBAC) from those that succeeded.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS audit_log (
  id             TEXT    PRIMARY KEY,
  occurred_at_ms INTEGER NOT NULL,
  actor_uuid     TEXT    REFERENCES admin_users(uuid) ON DELETE SET NULL,
  action         TEXT    NOT NULL,
  resource_type  TEXT,
  resource_id    TEXT,
  details        TEXT,
  ip_address     TEXT,
  result         TEXT    NOT NULL DEFAULT 'success' CHECK (result IN ('success', 'failure'))
);

CREATE INDEX IF NOT EXISTS idx_audit_log_occurred   ON audit_log(occurred_at_ms);
CREATE INDEX IF NOT EXISTS idx_audit_log_actor      ON audit_log(actor_uuid);
CREATE INDEX IF NOT EXISTS idx_audit_log_action     ON audit_log(action);
CREATE INDEX IF NOT EXISTS idx_audit_log_resource   ON audit_log(resource_type, resource_id);
