-- T-016 (API_DATABASE §5, D-009, D-035, D-037, D-043, D-044; TEST-012,
-- TEST-029): an uploaded import, its profile and its error report, kept
-- consistent by the database for every role.
--
--   * An import starts UPLOADED. Its upload (ID, tenant, project, model,
--     base version, kind, file name, object key, SHA-256, size, creator,
--     creation time) never changes, for any role: the bytes a later commit
--     freezes are the bytes that were uploaded (D-009).
--   * The profile (D-043) is there exactly when the import is past
--     profiling: null while UPLOADED or PROFILING, present for
--     AWAITING_MAPPING, VALIDATED and COMMITTED (a FAILED import may have
--     none). Once written it never changes, and a profiled import is never
--     profiled again.
--   * Profiling is the job's: the API never writes an import's profile,
--     preview or error report, and does not move an import while it is
--     UPLOADED or PROFILING. The worker writes them through its column
--     grants (D-037; 0002's guard keeps it off the human-confirmed columns).
--   * An error report is named by its object key, SHA-256 and size
--     together, at most 16 MiB (D-044: the API serves it only if the
--     stored bytes match); the key stays under the tenant's imports prefix
--     (0001, D-035).
--   * A FAILED import has its error, and FAILED and COMMITTED imports are
--     final, for every role.
--   * Only the API, the worker and the schema owner write imports (the
--     owner is otherwise held only to the rules above).
-- Whether a profile job failed is the job's (0007): the API reports an
-- import whose profile job failed as FAILED, with the job's failure; the
-- row keeps the status the job left it in (T-016).
BEGIN;
SET LOCAL ROLE twin_owner;

ALTER TABLE twin.imports
  ADD COLUMN profile jsonb CHECK (profile IS NULL OR jsonb_typeof(profile) = 'object'),
  ADD COLUMN error_report_content_hash twin.sha256,
  ADD COLUMN error_report_size_bytes bigint CHECK (error_report_size_bytes BETWEEN 1 AND 16777216),
  ADD CONSTRAINT imports_error_report_all_or_none
    CHECK (num_nonnulls(error_report_object_key, error_report_content_hash, error_report_size_bytes) IN (0, 3)),
  ADD CONSTRAINT imports_no_profile_while_profiling
    CHECK (status NOT IN ('UPLOADED', 'PROFILING') OR profile IS NULL),
  ADD CONSTRAINT imports_profiled_past_profiling
    CHECK (status NOT IN ('AWAITING_MAPPING', 'VALIDATED', 'COMMITTED') OR profile IS NOT NULL),
  ADD CONSTRAINT imports_failed_with_error
    CHECK (status <> 'FAILED' OR error IS NOT NULL);

GRANT UPDATE (profile, error_report_content_hash, error_report_size_bytes) ON twin.imports TO twin_worker;

CREATE FUNCTION twin.protect_import() RETURNS trigger LANGUAGE plpgsql
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
    IF NEW.status <> 'UPLOADED' THEN
      RAISE EXCEPTION 'an import starts UPLOADED' USING ERRCODE = '55000';
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
  IF api AND NOT owner THEN
    IF ROW(NEW.profile, NEW.preview, NEW.error_report_object_key, NEW.error_report_content_hash, NEW.error_report_size_bytes)
       IS DISTINCT FROM
       ROW(OLD.profile, OLD.preview, OLD.error_report_object_key, OLD.error_report_content_hash, OLD.error_report_size_bytes) THEN
      RAISE EXCEPTION 'the API never writes an import''s profile, preview or error report; its jobs do' USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('UPLOADED', 'PROFILING') AND NEW.status IS DISTINCT FROM OLD.status THEN
      RAISE EXCEPTION 'the API does not move an import while it is profiled; its job does' USING ERRCODE = '55000';
    END IF;
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_import() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER protect_import BEFORE INSERT OR UPDATE ON twin.imports
  FOR EACH ROW EXECUTE FUNCTION twin.protect_import();
COMMIT;
