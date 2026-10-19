-- DEVELOPMENT-ONLY DOWN MIGRATION of 0010: imports lose their upload and
-- profile guard and the profile and error report hash and size columns
-- (dropped with their values, and with the worker's grants on them).
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER protect_import ON twin.imports;
DROP FUNCTION twin.protect_import();
ALTER TABLE twin.imports
  DROP CONSTRAINT imports_failed_with_error,
  DROP CONSTRAINT imports_profiled_past_profiling,
  DROP CONSTRAINT imports_no_profile_while_profiling,
  DROP CONSTRAINT imports_error_report_all_or_none,
  DROP COLUMN error_report_size_bytes,
  DROP COLUMN error_report_content_hash,
  DROP COLUMN profile;
COMMIT;
