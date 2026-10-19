-- DEVELOPMENT-ONLY DOWN MIGRATION of 0007: jobs lose their lifecycle guard.
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER protect_job_state ON twin.jobs;
DROP FUNCTION twin.protect_job_state();
COMMIT;
