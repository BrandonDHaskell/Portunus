-- Migration 0015: clear pre-existing credential hashes that were computed under
-- the broken scheme (SHA-256 of raw bytes, no HMAC; or HMAC of the formatted
-- colon-hex string).  Any hash stored before this migration is unmatchable under
-- the canonical scheme (HMAC-SHA256(secret, raw UID bytes)) and would silently
-- deny the card holder forever.  Affected members must re-enroll.
--
-- NOTE: This migration only NULLs the hash; the member record (UUID, role,
-- authorizations) is preserved.  The member regains access after re-enrollment
-- via the provisioning console or the admin UI.

UPDATE member_access SET credential_hash = NULL WHERE credential_hash IS NOT NULL;
