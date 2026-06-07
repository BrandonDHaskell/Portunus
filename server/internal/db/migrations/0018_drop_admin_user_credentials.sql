-- 0018: drop admin_user_credentials
--
-- admin_user_credentials mapped RFID badges to admin_users so the provisioning
-- scan-1 operator could be resolved to an admin account. That path was replaced
-- in P1-3: scan-1 now resolves against member_access and checks the
-- member.provision role permission.  The table has no remaining callers.

PRAGMA foreign_keys = OFF;
DROP TABLE IF EXISTS admin_user_credentials;
PRAGMA foreign_keys = ON;
