-- 0026: split module_auth.grant/revoke into scoped (_held) and unscoped
-- (_any) variants. admin keeps unscoped (genesis: first grants into an empty
-- database). operator becomes scoped.

UPDATE OR IGNORE role_permissions SET permission = 'module_auth.grant_any'
  WHERE role_id = 'admin' AND permission = 'module_auth.grant';
UPDATE OR IGNORE role_permissions SET permission = 'module_auth.revoke_any'
  WHERE role_id = 'admin' AND permission = 'module_auth.revoke';

UPDATE OR IGNORE role_permissions SET permission = 'module_auth.grant_held'
  WHERE permission = 'module_auth.grant';
UPDATE OR IGNORE role_permissions SET permission = 'module_auth.revoke_held'
  WHERE permission = 'module_auth.revoke';

DELETE FROM role_permissions
  WHERE permission IN ('module_auth.grant', 'module_auth.revoke');
