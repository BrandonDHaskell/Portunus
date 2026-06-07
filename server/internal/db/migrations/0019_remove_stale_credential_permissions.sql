-- Remove permissions for the credentials feature removed in migrations
-- 0015–0016. These strings are no longer defined in the permissions package
-- (permissions.All()), so they can never be granted or rendered again, but
-- they linger in role_permissions for the admin role from the 0010 seed.
-- This deletes them. Idempotent: re-running affects zero rows.

DELETE FROM role_permissions
WHERE permission IN (
  'credential.list',
  'credential.register',
  'credential.update_status',
  'credential.delete'
);
