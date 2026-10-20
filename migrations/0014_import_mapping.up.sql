-- T-017 (API_DATABASE §5, APPROVED D-009, D-037, D-043 (c); D-071, D-072
-- PROPOSED; TEST-012, TEST-013): a person's mapping of an import's columns,
-- saved revision by revision, and the full-rows validation of each
-- revision by its own job, kept consistent by the database for every role.
--
-- Applies after 0013 (T-026, developed in parallel) and depends on nothing
-- of it: it reads and changes only twin.imports, a new index and a new
-- trigger on twin.jobs, and functions of its own; it replaces 0010's
-- protect_import.
--
--   * Mapping revisions: a mapping is saved as the next mapping_revision
--     (never another number, never back), only on a profiled import that
--     is not final (AWAITING_MAPPING, VALIDATING or VALIDATED), and makes
--     it VALIDATING with the earlier validation cleared (validation,
--     invalid rows, canonical preview, canonical dataset, error report,
--     error) and nothing else changed; the transaction that saves it
--     queues the import_validate job of that revision (deferred
--     import_validating_with_its_job). A mapping exists exactly when a
--     revision does. The worker never maps (0002's guard_worker_import).
--   * The stale-job fence: an import leaves VALIDATING for VALIDATED or
--     AWAITING_MAPPING only in the transaction that completes the
--     import_validate job of its current mapping_revision, and for FAILED
--     only with that job's end (deferred import_validated_with_its_job). A
--     job of an older revision (or of none) therefore never writes a
--     validation: whoever ends it, it changes nothing of the import
--     (follow_import_validate_job). Only one job per revision.
--   * The import follows the job of its current revision: when it fails
--     or is cancelled, the import is FAILED with the job's error (one
--     saying why when the job has none), whoever ends the job; that job
--     completes only once its result is written.
--   * Results: VALIDATED has a mapping, valid validation, no invalid row,
--     its canonical dataset (object key under the import's own prefix,
--     SHA-256 and size, at most 256 MiB, all or none) and no error report;
--     AWAITING_MAPPING after a validation has validation.valid false.
--     VALIDATED is reached only from VALIDATING, and a validated import is
--     mapped again only through a new revision. While VALIDATING nothing of
--     a validation is there. The canonical dataset is there only for
--     VALIDATED (and COMMITTED, T-018's).
--   * Who writes what: the API saves mappings and never writes a
--     validation (nor a profile, preview or error report, 0010); the
--     worker writes a validation only on the way out of VALIDATING, and
--     otherwise only the mapping suggestion of a profiled import (T-027).
--   * 0010's protect_import told "following a job" by pg_trigger_depth(),
--     which any other trigger passes (T-016 re-review, as T-025 review I1
--     for 0012): here an import moves to FAILED from UPLOADED or PROFILING
--     as the API only once its profile job has failed or been cancelled,
--     and from VALIDATING only once its current revision's job has ended,
--     from a trigger or not.
--   * What leads to COMMITTED (D-009's commit) is T-018's; 0001's check
--     still holds it.
BEGIN;
SET LOCAL ROLE twin_owner;

ALTER TABLE twin.imports DROP CONSTRAINT imports_status_check;
ALTER TABLE twin.imports
  ADD CONSTRAINT imports_status_check
    CHECK (status IN ('UPLOADED', 'PROFILING', 'AWAITING_MAPPING', 'VALIDATING', 'VALIDATED', 'COMMITTED', 'FAILED')),
  ADD COLUMN canonical_preview jsonb NOT NULL DEFAULT '[]'
    CONSTRAINT imports_canonical_preview_array
    CHECK (jsonb_typeof(canonical_preview) = 'array' AND jsonb_array_length(canonical_preview) <= 100),
  ADD COLUMN canonical_object_key text,
  ADD COLUMN canonical_content_hash twin.sha256,
  ADD COLUMN canonical_size_bytes bigint CONSTRAINT imports_canonical_size CHECK (canonical_size_bytes BETWEEN 2 AND 268435456),
  ADD CONSTRAINT imports_canonical_all_or_none
    CHECK (num_nonnulls(canonical_object_key, canonical_content_hash, canonical_size_bytes) IN (0, 3)),
  ADD CONSTRAINT imports_canonical_own_key CHECK (canonical_object_key IS NULL OR (
    twin.object_key_ok(canonical_object_key, 'tenant/' || organization_id || '/imports/' || id || '/')
    AND canonical_object_key IS DISTINCT FROM upload_object_key AND canonical_object_key IS DISTINCT FROM error_report_object_key)),
  ADD CONSTRAINT imports_canonical_when_validated CHECK (canonical_object_key IS NULL OR status IN ('VALIDATED', 'COMMITTED')),
  ADD CONSTRAINT imports_mapping_with_revision CHECK ((mapping IS NULL) = (mapping_revision = 0)),
  ADD CONSTRAINT imports_validating_clean CHECK (status <> 'VALIDATING' OR (
    mapping IS NOT NULL AND validation IS NULL AND invalid_rows = 0 AND canonical_preview = '[]'::jsonb
    AND num_nonnulls(canonical_object_key, error_report_object_key, error) = 0)),
  ADD CONSTRAINT imports_validated_complete CHECK (status <> 'VALIDATED' OR (
    mapping IS NOT NULL AND invalid_rows = 0 AND coalesce(validation->>'valid' = 'true', false)
    AND canonical_object_key IS NOT NULL AND error_report_object_key IS NULL));

-- The worker records a validation's canonical preview and dataset (D-037:
-- column grants; mapping and revision stay the API's).
GRANT UPDATE (canonical_preview, canonical_object_key, canonical_content_hash, canonical_size_bytes) ON twin.imports TO twin_worker;

-- One validation job per mapping revision: a second could validate a
-- revision again, or end after the import followed the first.
CREATE UNIQUE INDEX jobs_one_import_validate_per_revision ON twin.jobs(organization_id, import_id, (payload->>'mapping_revision'))
  WHERE type = 'import_validate';

CREATE OR REPLACE FUNCTION twin.protect_import() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  owner boolean := pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE');
  api boolean := pg_catalog.pg_has_role(current_user, 'twin_api', 'USAGE');
  worker boolean := pg_catalog.pg_has_role(current_user, 'twin_worker', 'USAGE');
BEGIN
  IF NOT (owner OR api OR worker) THEN
    RAISE EXCEPTION 'only the API, the worker and the schema owner write imports' USING ERRCODE = '55000';
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NEW.status <> 'UPLOADED' OR NEW.profile IS NOT NULL OR NEW.preview IS DISTINCT FROM '[]'::jsonb
       OR NEW.total_rows <> 0 OR NEW.invalid_rows <> 0 OR NEW.validation IS NOT NULL OR NEW.error IS NOT NULL
       OR num_nonnulls(NEW.error_report_object_key, NEW.error_report_content_hash, NEW.error_report_size_bytes) <> 0
       OR NEW.mapping IS NOT NULL OR NEW.mapping_revision <> 0 OR NEW.mapping_suggestion_status <> 'none'
       OR NEW.mapping_suggestion IS NOT NULL OR NEW.dataset_version_id IS NOT NULL OR NEW.proposal_id IS NOT NULL
       OR NEW.canonical_preview IS DISTINCT FROM '[]'::jsonb
       OR num_nonnulls(NEW.canonical_object_key, NEW.canonical_content_hash, NEW.canonical_size_bytes) <> 0 THEN
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

  -- A mapping is saved as the next revision of a profiled import, which
  -- starts VALIDATING with the earlier validation cleared and nothing else
  -- changed (the API's; 0002 keeps the worker off these columns).
  IF NEW.mapping_revision IS DISTINCT FROM OLD.mapping_revision OR NEW.mapping IS DISTINCT FROM OLD.mapping THEN
    IF NEW.mapping_revision IS DISTINCT FROM OLD.mapping_revision + 1 OR NEW.mapping IS NULL THEN
      RAISE EXCEPTION 'a mapping is saved as the next mapping revision' USING ERRCODE = '55000';
    END IF;
    IF OLD.status NOT IN ('AWAITING_MAPPING', 'VALIDATING', 'VALIDATED') THEN
      RAISE EXCEPTION 'only a profiled import is mapped' USING ERRCODE = '55000';
    END IF;
    IF NEW.status <> 'VALIDATING' OR NEW.validation IS NOT NULL OR NEW.invalid_rows <> 0 OR NEW.canonical_preview IS DISTINCT FROM '[]'::jsonb
       OR num_nonnulls(NEW.canonical_object_key, NEW.canonical_content_hash, NEW.canonical_size_bytes, NEW.error_report_object_key,
                       NEW.error_report_content_hash, NEW.error_report_size_bytes, NEW.error) <> 0
       OR ROW(NEW.profile, NEW.preview, NEW.total_rows, NEW.mapping_suggestion_status, NEW.mapping_suggestion, NEW.dataset_version_id, NEW.proposal_id)
          IS DISTINCT FROM
          ROW(OLD.profile, OLD.preview, OLD.total_rows, OLD.mapping_suggestion_status, OLD.mapping_suggestion, OLD.dataset_version_id, OLD.proposal_id) THEN
      RAISE EXCEPTION 'a new mapping revision starts VALIDATING with the earlier validation cleared and nothing else changed' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
  END IF;

  -- Leaving VALIDATING (but for T-018's commit): the result of the current
  -- revision's validation job, or following it to FAILED. Which job, and
  -- that it ends in the same transaction, is import_validated_with_its_job's.
  IF OLD.status = 'VALIDATING' AND NEW.status NOT IN ('VALIDATING', 'COMMITTED') THEN
    IF NEW.status = 'FAILED' THEN
      IF api AND NOT owner AND NOT EXISTS (SELECT 1 FROM twin.jobs j
           WHERE j.organization_id = NEW.organization_id AND j.import_id = NEW.id AND j.type = 'import_validate'
             AND j.payload->>'mapping_revision' = OLD.mapping_revision::text AND j.status IN ('failed', 'cancelled')) THEN
        RAISE EXCEPTION 'an import fails only with its validation job: end the job, and the import follows' USING ERRCODE = '55000';
      END IF;
      IF ROW(NEW.validation, NEW.invalid_rows, NEW.canonical_preview, NEW.canonical_object_key, NEW.error_report_object_key,
             NEW.preview, NEW.total_rows, NEW.mapping_suggestion_status, NEW.mapping_suggestion, NEW.dataset_version_id, NEW.proposal_id)
         IS DISTINCT FROM
         ROW(OLD.validation, OLD.invalid_rows, OLD.canonical_preview, OLD.canonical_object_key, OLD.error_report_object_key,
             OLD.preview, OLD.total_rows, OLD.mapping_suggestion_status, OLD.mapping_suggestion, OLD.dataset_version_id, OLD.proposal_id) THEN
        RAISE EXCEPTION 'an import failing its validation keeps all but its status and error' USING ERRCODE = '55000';
      END IF;
      RETURN NEW;
    END IF;
    IF NEW.status NOT IN ('VALIDATED', 'AWAITING_MAPPING') THEN
      RAISE EXCEPTION 'a validated import is VALIDATED, AWAITING_MAPPING or FAILED, not %', NEW.status USING ERRCODE = '55000';
    END IF;
    IF api AND NOT owner THEN
      RAISE EXCEPTION 'the API never writes a validation; its job does' USING ERRCODE = '55000';
    END IF;
    IF NEW.status = 'AWAITING_MAPPING' AND coalesce(NEW.validation->>'valid', '') <> 'false' THEN
      RAISE EXCEPTION 'an import left to be mapped again has a validation that is not valid' USING ERRCODE = '55000';
    END IF;
    IF ROW(NEW.preview, NEW.total_rows, NEW.dataset_version_id, NEW.proposal_id)
       IS DISTINCT FROM ROW(OLD.preview, OLD.total_rows, OLD.dataset_version_id, OLD.proposal_id) THEN
      RAISE EXCEPTION 'a validation writes only its own results' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.status = 'VALIDATED' AND OLD.status <> 'VALIDATED' THEN
    RAISE EXCEPTION 'an import is VALIDATED only by the validation of its mapping revision' USING ERRCODE = '55000';
  END IF;
  IF NEW.status = 'VALIDATING' AND OLD.status <> 'VALIDATING' THEN
    RAISE EXCEPTION 'an import is VALIDATING only with a new mapping revision' USING ERRCODE = '55000';
  END IF;
  IF OLD.status = 'VALIDATED' AND NEW.status = 'AWAITING_MAPPING' THEN
    RAISE EXCEPTION 'a validated import is mapped again through a new mapping revision' USING ERRCODE = '55000';
  END IF;

  -- The worker writes a profiled import only as the job above, or its
  -- mapping suggestion (T-027).
  IF worker AND NOT owner AND OLD.status IN ('AWAITING_MAPPING', 'VALIDATING', 'VALIDATED')
     AND ROW(NEW.status, NEW.preview, NEW.total_rows, NEW.invalid_rows, NEW.validation, NEW.error, NEW.canonical_preview,
             NEW.canonical_object_key, NEW.canonical_content_hash, NEW.canonical_size_bytes,
             NEW.error_report_object_key, NEW.error_report_content_hash, NEW.error_report_size_bytes)
         IS DISTINCT FROM
         ROW(OLD.status, OLD.preview, OLD.total_rows, OLD.invalid_rows, OLD.validation, OLD.error, OLD.canonical_preview,
             OLD.canonical_object_key, OLD.canonical_content_hash, OLD.canonical_size_bytes,
             OLD.error_report_object_key, OLD.error_report_content_hash, OLD.error_report_size_bytes) THEN
    RAISE EXCEPTION 'the worker writes a profiled import only as its validation job, or its mapping suggestion' USING ERRCODE = '55000';
  END IF;

  IF api AND NOT owner THEN
    IF ROW(NEW.profile, NEW.preview, NEW.error_report_object_key, NEW.error_report_content_hash, NEW.error_report_size_bytes)
       IS DISTINCT FROM
       ROW(OLD.profile, OLD.preview, OLD.error_report_object_key, OLD.error_report_content_hash, OLD.error_report_size_bytes) THEN
      RAISE EXCEPTION 'the API never writes an import''s profile, preview or error report; its jobs do' USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('UPLOADED', 'PROFILING') THEN
      -- Following its profile job (0010's follow_import_profile_job runs
      -- with the privileges of whoever ends the job): FAILED with an error,
      -- nothing else, once the job has failed or been cancelled.
      IF NEW.status = 'FAILED'
         AND ROW(NEW.validation, NEW.invalid_rows, NEW.total_rows, NEW.canonical_preview, NEW.canonical_object_key,
                 NEW.mapping_suggestion_status, NEW.mapping_suggestion, NEW.dataset_version_id, NEW.proposal_id)
             IS NOT DISTINCT FROM
             ROW(OLD.validation, OLD.invalid_rows, OLD.total_rows, OLD.canonical_preview, OLD.canonical_object_key,
                 OLD.mapping_suggestion_status, OLD.mapping_suggestion, OLD.dataset_version_id, OLD.proposal_id)
         AND EXISTS (SELECT 1 FROM twin.jobs j WHERE j.organization_id = NEW.organization_id AND j.import_id = NEW.id
                       AND j.type = 'import_profile' AND j.status IN ('failed', 'cancelled')) THEN
        RETURN NEW;
      END IF;
      RAISE EXCEPTION 'the API changes nothing of an import while it is profiled; its job does' USING ERRCODE = '55000';
    END IF;
    IF ROW(NEW.validation, NEW.invalid_rows, NEW.total_rows, NEW.error, NEW.canonical_preview, NEW.canonical_object_key,
           NEW.canonical_content_hash, NEW.canonical_size_bytes)
       IS DISTINCT FROM
       ROW(OLD.validation, OLD.invalid_rows, OLD.total_rows, OLD.error, OLD.canonical_preview, OLD.canonical_object_key,
           OLD.canonical_content_hash, OLD.canonical_size_bytes) THEN
      RAISE EXCEPTION 'the API never writes a validation; its job does' USING ERRCODE = '55000';
    END IF;
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_import() FROM PUBLIC; -- runs as a trigger, never called directly

-- A mapping revision is saved with its validation job, for every role:
-- otherwise the import would stay VALIDATING with no job to validate it.
CREATE FUNCTION twin.check_import_validating_with_its_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM twin.jobs j
                 WHERE j.organization_id = NEW.organization_id AND j.import_id = NEW.id AND j.type = 'import_validate'
                   AND j.payload->>'mapping_revision' = NEW.mapping_revision::text) THEN
    RAISE EXCEPTION 'a mapping revision is saved with its validation job' USING ERRCODE = '55000';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_import_validating_with_its_job() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER import_validating_with_its_job AFTER UPDATE OF mapping_revision ON twin.imports
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW WHEN (NEW.mapping_revision IS DISTINCT FROM OLD.mapping_revision)
  EXECUTE FUNCTION twin.check_import_validating_with_its_job();

-- The stale-job fence, for every role: an import leaves VALIDATING with a
-- result only in the transaction that completes the validation job of its
-- current mapping revision, and fails only with that job's end.
CREATE FUNCTION twin.check_import_validated_with_its_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM twin.jobs j
                 WHERE j.organization_id = NEW.organization_id AND j.import_id = NEW.id AND j.type = 'import_validate'
                   AND j.payload->>'mapping_revision' = NEW.mapping_revision::text
                   AND (j.status = 'completed' OR (NEW.status = 'FAILED' AND j.status IN ('failed', 'cancelled')))) THEN
    RAISE EXCEPTION 'an import leaves VALIDATING only with the end of the validation job of its mapping revision' USING ERRCODE = '55000';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_import_validated_with_its_job() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER import_validated_with_its_job AFTER UPDATE OF status ON twin.imports
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW WHEN (OLD.status = 'VALIDATING' AND NEW.status IN ('VALIDATED', 'AWAITING_MAPPING', 'FAILED'))
  EXECUTE FUNCTION twin.check_import_validated_with_its_job();

-- Runs with the privileges of whoever ends the job: the worker (its column
-- UPDATE on imports covers status and error), the API or an operator.
CREATE FUNCTION twin.follow_import_validate_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE s text; r bigint;
BEGIN
  SELECT status, mapping_revision INTO s, r FROM twin.imports
    WHERE organization_id = NEW.organization_id AND id = NEW.import_id FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'an import validation job''s import is not visible to its organization' USING ERRCODE = '55000';
  END IF;
  -- The stale-job fence: a job of another revision (or of none), however
  -- it ends, changes nothing of the import.
  IF s <> 'VALIDATING' OR NEW.payload->>'mapping_revision' IS DISTINCT FROM r::text THEN
    RETURN NULL;
  END IF;
  IF NEW.status = 'completed' THEN
    RAISE EXCEPTION 'an import validation job completes only with its result' USING ERRCODE = '55000';
  END IF;
  UPDATE twin.imports SET status = 'FAILED',
      error = coalesce(NEW.error, CASE WHEN NEW.status = 'cancelled'
        THEN '{"code":"JOB_CANCELLED","message":"the validation job was ended before it validated the file","retryable":false}'::jsonb
        ELSE '{"code":"INTERNAL","message":"the validation job failed","retryable":false}'::jsonb END)
    WHERE organization_id = NEW.organization_id AND id = NEW.import_id;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.follow_import_validate_job() FROM PUBLIC;
CREATE TRIGGER follow_import_validate_job AFTER UPDATE OF status ON twin.jobs
  FOR EACH ROW WHEN (NEW.type = 'import_validate' AND NEW.status IN ('completed', 'failed', 'cancelled')
                     AND NEW.status IS DISTINCT FROM OLD.status)
  EXECUTE FUNCTION twin.follow_import_validate_job();
COMMIT;
