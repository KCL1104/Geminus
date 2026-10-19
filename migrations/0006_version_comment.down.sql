-- DEVELOPMENT-ONLY DOWN MIGRATION of 0006: drops the versions' comments.
BEGIN;
SET LOCAL ROLE twin_owner;
ALTER TABLE twin.model_versions DROP COLUMN comment;
COMMIT;
