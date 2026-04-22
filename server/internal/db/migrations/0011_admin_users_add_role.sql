-- PR 5: add role_id and enabled to admin_users
--
-- role_id links an admin user to their RBAC role.  Existing users default to
-- the built-in 'admin' role so no access is lost on upgrade.
--
-- enabled allows an admin to be disabled without deletion, preserving audit
-- continuity.  Disabled accounts cannot log in.

PRAGMA foreign_keys = ON;

ALTER TABLE admin_users ADD COLUMN role_id TEXT REFERENCES roles(role_id);
ALTER TABLE admin_users ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1));

-- Assign every existing admin user to the admin role.
UPDATE admin_users SET role_id = 'admin';
