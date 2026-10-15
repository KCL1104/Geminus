-- DESTRUCTIVE DEVELOPMENT-ONLY DOWN MIGRATION. Deletes all twin application data.
-- Requires explicit operator review and a disposable database. Not an automatic rollback.
BEGIN;
SET LOCAL ROLE twin_owner;
DROP SCHEMA twin CASCADE;
COMMIT;
