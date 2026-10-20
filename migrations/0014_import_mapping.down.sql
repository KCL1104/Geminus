-- DEVELOPMENT-ONLY DOWN MIGRATION of 0014: imports lose the rules of their
-- mapping revisions and the stale-job fence, no longer follow their
-- validation jobs, and lose the canonical preview and dataset columns
-- (dropped with their values, and with the worker's grants on them);
-- protect_import is 0010's again. It fails while an import is VALIDATING
-- (0001's status check has no such state). It runs before 0013's down
-- (T-026), which it does not depend on.
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER follow_import_validate_job ON twin.jobs;
DROP FUNCTION twin.follow_import_validate_job();
DROP TRIGGER import_validated_with_its_job ON twin.imports;
DROP FUNCTION twin.check_import_validated_with_its_job();
DROP TRIGGER import_validating_with_its_job ON twin.imports;
DROP FUNCTION twin.check_import_validating_with_its_job();
-- 0010's function, as it was.
CREATE OR REPLACE FUNCTION twin.protect_import() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  owner boolean := pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE');
  api boolean := pg_catalog.pg_has_role(current_user, 'twin_api', 'USAGE');
  worker boolean := pg_catalog.pg_has_role(current_user, 'twin_worker', 'USAGE');
  -- follow_import_profile_job is the only trigger that writes imports.
  followed boolean := pg_catalog.pg_trigger_depth() > 1;
BEGIN
  IF NOT (owner OR api OR worker) THEN
    RAISE EXCEPTION 'only the API, the worker and the schema owner write imports' USING ERRCODE = '55000';
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NEW.status <> 'UPLOADED' OR NEW.profile IS NOT NULL OR NEW.preview IS DISTINCT FROM '[]'::jsonb
       OR NEW.total_rows <> 0 OR NEW.invalid_rows <> 0 OR NEW.validation IS NOT NULL OR NEW.error IS NOT NULL
       OR num_nonnulls(NEW.error_report_object_key, NEW.error_report_content_hash, NEW.error_report_size_bytes) <> 0
       OR NEW.mapping IS NOT NULL OR NEW.mapping_revision <> 0 OR NEW.mapping_suggestion_status <> 'none'
       OR NEW.mapping_suggestion IS NOT NULL OR NEW.dataset_version_id IS NOT NULL OR NEW.proposal_id IS NOT NULL THEN
      RAISE EXCEPTION 'an import starts UPLOADED with nothing derived from its file' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
  END IF;
  IF ROW(NEW.id, NEW.organization_id, NEW.project_id, NEW.model_id, NEW.base_version_id, NEW.kind, NEW.filename,
         NEW.upload_object_key, NEW.upload_content_hash, NEW.size_bytes, NEW.created_by, NEW.created_at)
     IS DISTINCT FROM
     ROW(OLD.id, OLD.organization_id, OLD.project_id, OLD.model_id, OLD.base_version_id, OLD.kind, OLD.filename,
         OLD.upload_object_key, OLD.upload_content_hash, OLD.size_bytes, OLD.created_by, OLD.created_at) THEN
    RAISE EXCEPTION 'an import''s upload (its identity, file, object, hash and size) never changes' USING ERRCODE = '55000';
  END IF;
  IF NEW IS NOT DISTINCT FROM OLD THEN
    RETURN NEW;
  END IF;
  IF OLD.status IN ('FAILED', 'COMMITTED') THEN
    RAISE EXCEPTION 'a % import is final', OLD.status USING ERRCODE = '55000';
  END IF;
  IF OLD.profile IS NOT NULL AND NEW.profile IS DISTINCT FROM OLD.profile THEN
    RAISE EXCEPTION 'an import''s profile never changes once written' USING ERRCODE = '55000';
  END IF;
  IF NEW.status IN ('UPLOADED', 'PROFILING') AND OLD.status NOT IN ('UPLOADED', 'PROFILING') THEN
    RAISE EXCEPTION 'a profiled import is never profiled again' USING ERRCODE = '55000';
  END IF;
  IF followed THEN
    IF NOT (OLD.status IN ('UPLOADED', 'PROFILING') AND NEW.status = 'FAILED') THEN
      RAISE EXCEPTION 'an import follows its profile job only to FAILED' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
  END IF;
  IF api AND NOT owner THEN
    IF ROW(NEW.profile, NEW.preview, NEW.error_report_object_key, NEW.error_report_content_hash, NEW.error_report_size_bytes)
       IS DISTINCT FROM
       ROW(OLD.profile, OLD.preview, OLD.error_report_object_key, OLD.error_report_content_hash, OLD.error_report_size_bytes) THEN
      RAISE EXCEPTION 'the API never writes an import''s profile, preview or error report; its jobs do' USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('UPLOADED', 'PROFILING') THEN
      RAISE EXCEPTION 'the API changes nothing of an import while it is profiled; its job does' USING ERRCODE = '55000';
    END IF;
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_import() FROM PUBLIC; -- runs as a trigger, never called directly
DROP INDEX twin.jobs_one_import_validate_per_revision;
ALTER TABLE twin.imports
  DROP CONSTRAINT imports_validated_complete,
  DROP CONSTRAINT imports_validating_clean,
  DROP CONSTRAINT imports_mapping_with_revision,
  DROP CONSTRAINT imports_canonical_when_validated,
  DROP CONSTRAINT imports_canonical_own_key,
  DROP CONSTRAINT imports_canonical_all_or_none,
  DROP COLUMN canonical_size_bytes,
  DROP COLUMN canonical_content_hash,
  DROP COLUMN canonical_object_key,
  DROP COLUMN canonical_preview,
  DROP CONSTRAINT imports_status_check,
  ADD CONSTRAINT imports_status_check
    CHECK (status IN ('UPLOADED', 'PROFILING', 'AWAITING_MAPPING', 'VALIDATED', 'COMMITTED', 'FAILED'));
COMMIT;
