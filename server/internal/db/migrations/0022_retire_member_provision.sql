-- 0022: Path 2 (two-scan operator enrollment) hard cut.
--
-- member.provision had two meanings: badge-resolved operator enrollment
-- (removed) and console-side member enrollment/approval (kept). The kept
-- meaning is renamed member.enroll. UPDATE OR IGNORE tolerates a role that
-- already holds both rows.

UPDATE OR IGNORE role_permissions
SET permission = 'member.enroll'
WHERE permission = 'member.provision';

DELETE FROM role_permissions WHERE permission = 'member.provision';
