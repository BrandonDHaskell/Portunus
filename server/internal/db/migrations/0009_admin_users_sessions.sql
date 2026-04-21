-- PR 3: admin_users and sessions tables
--
-- admin_users holds server operator accounts. Passwords are bcrypt-hashed.
-- must_change_pw forces a password reset on first login (bootstrap account).
--
-- sessions is the server-side session store for cookie-based auth.
-- HttpOnly + Secure + SameSite=Strict cookies carry only the session_id.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS admin_users (
  uuid             TEXT    PRIMARY KEY,
  username         TEXT    NOT NULL UNIQUE,
  password_hash    TEXT    NOT NULL,
  must_change_pw   INTEGER NOT NULL DEFAULT 1 CHECK (must_change_pw IN (0,1)),
  created_at_ms    INTEGER NOT NULL,
  updated_at_ms    INTEGER NOT NULL,
  last_login_at_ms INTEGER
);

CREATE TABLE IF NOT EXISTS sessions (
  session_id    TEXT    PRIMARY KEY,
  admin_uuid    TEXT    NOT NULL REFERENCES admin_users(uuid) ON DELETE CASCADE,
  created_at_ms INTEGER NOT NULL,
  expires_at_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_admin_uuid ON sessions(admin_uuid);
CREATE INDEX IF NOT EXISTS idx_sessions_expires    ON sessions(expires_at_ms);
