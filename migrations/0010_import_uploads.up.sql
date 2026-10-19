-- T-016 (API_DATABASE §5, D-009, D-035, D-037, D-043, D-044; TEST-012,
-- TEST-029): an uploaded import, its profile and its error report, kept
-- consistent by the database for every role.
--
--   * An import starts UPLOADED with nothing derived from its file, for
--     every role: no profile, preview, row counts, validation, error, error
--     report, mapping, mapping suggestion, dataset or proposal (T-016
--     review M3). Its upload (ID, tenant, project, model, base version,
--     kind, file name, object key, SHA-256, size, creator, creation time)
--     never changes, for any role: the bytes a later commit freezes are the
--     bytes that were uploaded (D-009).
--   * The profile (D-043) is there exactly when the import is past
--     profiling: null while UPLOADED or PROFILING, present for
--     AWAITING_MAPPING, VALIDATED and COMMITTED (a FAILED import may have
--     none). Once written it never changes, and a profiled import is never
--     profiled again.
--   * Profiling is the job's: the API never writes an import's profile,
--     preview or error report, and changes nothing of an import while it
--     is UPLOADED or PROFILING. The worker writes them through its column
--     grants (D-037; 0002's guard keeps it off the human-confirmed columns).
--   * An import has one profile job, and follows it (T-016 review M1): when
--     the job fails or is cancelled before the import is profiled, the
--     import is FAILED in the same transaction, with the job's error (one
--     saying why when the job has none), whoever ends the job: the worker
--     loop, the reaper, the API cancelling a queued job or an operator.
--   * An error report is named by its object key, SHA-256 and size
--     together, at most 16 MiB (D-044: the API serves it only if the
--     stored bytes match); the key stays under the tenant's imports prefix
--     (0001, D-035) and is never the upload's own.
--   * A FAILED import has its error, and FAILED and COMMITTED imports are
--     final, for every role.
--   * Only the API, the worker and the schema owner write imports (the
--     owner is otherwise held only to the rules above).
BEGIN;
SET LOCAL ROLE twin_owner;

ALTER TABLE twin.imports
  ADD COLUMN profile jsonb CHECK (profile IS NULL OR jsonb_typeof(profile) = 'object'),
  ADD COLUMN error_report_content_hash twin.sha256,
  ADD COLUMN error_report_size_bytes bigint CHECK (error_report_size_bytes BETWEEN 1 AND 16777216),
  ADD CONSTRAINT imports_error_report_all_or_none
    CHECK (num_nonnulls(error_report_object_key, error_report_content_hash, error_report_size_bytes) IN (0, 3)),
  ADD CONSTRAINT imports_error_report_not_the_upload
    CHECK (error_report_object_key IS DISTINCT FROM upload_object_key),
  ADD CONSTRAINT imports_no_profile_while_profiling
    CHECK (status NOT IN ('UPLOADED', 'PROFILING') OR profile IS NULL),
  ADD CONSTRAINT imports_profiled_past_profiling
    CHECK (status NOT IN ('AWAITING_MAPPING', 'VALIDATED', 'COMMITTED') OR profile IS NOT NULL),
  ADD CONSTRAINT imports_failed_with_error
    CHECK (status <> 'FAILED' OR error IS NOT NULL);

GRANT UPDATE (profile, error_report_content_hash, error_report_size_bytes) ON twin.imports TO twin_worker;

-- One profile job per import: a second one could profile an import again,
-- or end after the import followed the first.
CREATE UNIQUE INDEX jobs_one_import_profile ON twin.jobs(organization_id, import_id) WHERE type = 'import_profile';

CREATE FUNCTION twin.protect_import() RETURNS trigger LANGUAGE plpgsql
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
CREATE TRIGGER protect_import BEFORE INSERT OR UPDATE ON twin.imports
  FOR EACH ROW EXECUTE FUNCTION twin.protect_import();

-- Runs with the privileges of whoever ends the job: the worker (its column
-- UPDATE on imports covers status and error), the API or an operator.
CREATE FUNCTION twin.follow_import_profile_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM twin.imports WHERE organization_id = NEW.organization_id AND id = NEW.import_id FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'an import profile job''s import is not visible to its organization' USING ERRCODE = '55000';
  END IF;
  -- An import past profiling (an operator moved it) stays as it is, so
  -- that ending its job never fails.
  IF s IN ('UPLOADED', 'PROFILING') THEN
    UPDATE twin.imports SET status = 'FAILED',
        error = coalesce(NEW.error, CASE WHEN NEW.status = 'cancelled'
          THEN '{"code":"JOB_CANCELLED","message":"the profile job was ended before it profiled the file","retryable":false}'::jsonb
          ELSE '{"code":"INTERNAL","message":"the profile job failed","retryable":false}'::jsonb END)
      WHERE organization_id = NEW.organization_id AND id = NEW.import_id;
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.follow_import_profile_job() FROM PUBLIC;
CREATE TRIGGER follow_import_profile_job AFTER UPDATE OF status ON twin.jobs
  FOR EACH ROW WHEN (NEW.type = 'import_profile' AND NEW.status IN ('failed', 'cancelled') AND NEW.status IS DISTINCT FROM OLD.status)
  EXECUTE FUNCTION twin.follow_import_profile_job();
COMMIT;
