-- T-030 (AI_SPEC §9, API_DATABASE §4, D-010, D-016, D-026): an analysis's
-- request, its life while it is generated, and its one job, kept
-- consistent by the database for every role, as 0012 does for proposals.
--
--   * An analysis starts generating with nothing generated: no analysis,
--     provider metadata or error. The API creates it for the scoped user.
--     Its request never changes, for any role: the analyzed run, the
--     baseline run, the question, the output_locale (added here), the ID,
--     the creator and the creation time. The baseline is another run.
--   * While it is generated, only its job writes it: the API changes
--     nothing of an analysis (0002 grants it no UPDATE; the worker's
--     column UPDATE covers status, analysis, provider metadata and error).
--     It leaves generating for completed only in the transaction that
--     completes its job (analysis_completed_with_its_job), and for failed
--     only once its job has failed or been cancelled: whoever ends the job,
--     the analysis is failed in the same statement with the job's error
--     (follow_analysis_job), as proposals (0012) follow theirs. A job
--     completes only once its analysis is completed. An analysis has at
--     most one job (0001's jobs_one_analysis).
--   * A completed or failed analysis never changes (AGENTS.md §2: a
--     completed result is not modified in place).
--   * What each state holds: completed has its analysis (0001) and no
--     error; failed has its error and nothing generated; generating has
--     neither; the provider metadata is an object.
--   * Only the API, the worker and the schema owner write analyses.
--
-- No analysis exists before T-030 stores them (nothing created one), so the
-- new checks hold for every row; the migration stops on a database that has
-- one rather than guess the language it was asked in.
BEGIN;
SET LOCAL ROLE twin_owner;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM twin.ai_analyses) THEN
    RAISE EXCEPTION 'migration 0018 records the language each analysis is asked in, which cannot be made up for existing analyses'
      USING ERRCODE = '55000';
  END IF;
END $$;

ALTER TABLE twin.ai_analyses
  ADD COLUMN output_locale text NOT NULL DEFAULT 'en' CONSTRAINT ai_analyses_output_locale CHECK (output_locale IN ('en', 'zh-Hant')),
  ADD CONSTRAINT ai_analyses_question_length CHECK (question IS NULL OR char_length(question) BETWEEN 1 AND 10000),
  ADD CONSTRAINT ai_analyses_baseline_is_another_run CHECK (baseline_run_id IS DISTINCT FROM run_id),
  ADD CONSTRAINT ai_analyses_error_iff_failed CHECK ((status = 'failed') = (error IS NOT NULL)),
  ADD CONSTRAINT ai_analyses_analysis_iff_completed CHECK ((status = 'completed') = (analysis IS NOT NULL)),
  ADD CONSTRAINT ai_analyses_analysis_object CHECK (analysis IS NULL OR jsonb_typeof(analysis) = 'object'),
  ADD CONSTRAINT ai_analyses_failed_content CHECK (status <> 'failed' OR provider_metadata = '{}'),
  ADD CONSTRAINT ai_analyses_provider_metadata_object CHECK (jsonb_typeof(provider_metadata) = 'object');

CREATE FUNCTION twin.protect_analysis_lifecycle() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  owner boolean := pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE');
  api boolean := pg_catalog.pg_has_role(current_user, 'twin_api', 'USAGE');
  worker boolean := pg_catalog.pg_has_role(current_user, 'twin_worker', 'USAGE');
BEGIN
  IF NOT (owner OR api OR worker) THEN
    RAISE EXCEPTION 'only the API, the worker and the schema owner write analyses' USING ERRCODE = '55000';
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NEW.status <> 'generating' OR NEW.analysis IS NOT NULL OR NEW.error IS NOT NULL
       OR NEW.provider_metadata IS DISTINCT FROM '{}'::jsonb THEN
      RAISE EXCEPTION 'an analysis starts generating with nothing generated' USING ERRCODE = '55000';
    END IF;
    IF api AND NOT owner AND NEW.created_by::text IS DISTINCT FROM nullif(current_setting('app.user_id', true), '') THEN
      RAISE EXCEPTION 'the API creates an analysis for the scoped user' USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
  END IF;
  IF ROW(NEW.id, NEW.organization_id, NEW.run_id, NEW.baseline_run_id, NEW.question, NEW.output_locale, NEW.created_by, NEW.created_at)
     IS DISTINCT FROM ROW(OLD.id, OLD.organization_id, OLD.run_id, OLD.baseline_run_id, OLD.question, OLD.output_locale, OLD.created_by, OLD.created_at) THEN
    RAISE EXCEPTION 'an analysis''s request (runs, question, output locale), ID, creator and creation time never change' USING ERRCODE = '55000';
  END IF;
  IF NEW IS NOT DISTINCT FROM OLD THEN
    RETURN NEW;
  END IF;
  IF OLD.status <> 'generating' THEN
    RAISE EXCEPTION 'a completed or failed analysis never changes' USING ERRCODE = '55000';
  END IF;
  -- Only the job fails an analysis (0012's reasoning): follow_analysis_job,
  -- an AFTER trigger of the job's update, sees the job already ended.
  IF NEW.status = 'failed' AND NOT EXISTS (SELECT 1 FROM twin.jobs j
       WHERE j.organization_id = NEW.organization_id AND j.ai_analysis_id = NEW.id AND j.status IN ('failed', 'cancelled')) THEN
    RAISE EXCEPTION 'an analysis fails only with its job: end the job, and the analysis follows' USING ERRCODE = '55000';
  END IF;
  IF api AND NOT owner THEN
    RAISE EXCEPTION 'the API changes nothing of an analysis; its job does' USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_analysis_lifecycle() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER protect_analysis_lifecycle BEFORE INSERT OR UPDATE ON twin.ai_analyses
  FOR EACH ROW EXECUTE FUNCTION twin.protect_analysis_lifecycle();

-- An analysis is completed in the transaction that completes its job (the
-- worker's Finish), for every role.
CREATE FUNCTION twin.check_analysis_completed_with_its_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM twin.jobs j
                 WHERE j.organization_id = NEW.organization_id AND j.ai_analysis_id = NEW.id AND j.status = 'completed') THEN
    RAISE EXCEPTION 'an analysis is completed in the transaction that completes its job' USING ERRCODE = '55000';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_analysis_completed_with_its_job() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER analysis_completed_with_its_job AFTER UPDATE OF status ON twin.ai_analyses
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW WHEN (OLD.status = 'generating' AND NEW.status = 'completed')
  EXECUTE FUNCTION twin.check_analysis_completed_with_its_job();

-- Runs with the privileges of whoever ends the job: the worker (its column
-- UPDATE on ai_analyses covers status, error and the provider metadata it
-- clears), the API or an operator.
CREATE FUNCTION twin.follow_analysis_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM twin.ai_analyses
    WHERE organization_id = NEW.organization_id AND id = NEW.ai_analysis_id FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'an analysis job''s analysis is not visible to its organization' USING ERRCODE = '55000';
  END IF;
  IF NEW.status = 'completed' THEN
    IF s <> 'completed' THEN
      RAISE EXCEPTION 'an analysis job completes only once its analysis is completed' USING ERRCODE = '55000';
    END IF;
  ELSIF s = 'generating' THEN
    UPDATE twin.ai_analyses SET status = 'failed',
        error = coalesce(NEW.error, CASE WHEN NEW.status = 'cancelled'
          THEN '{"code":"JOB_CANCELLED","message":"the analysis job was ended before the analysis was written","retryable":false}'::jsonb
          ELSE '{"code":"INTERNAL","message":"the analysis job failed","retryable":false}'::jsonb END),
        analysis = NULL, provider_metadata = '{}'
      WHERE organization_id = NEW.organization_id AND id = NEW.ai_analysis_id;
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.follow_analysis_job() FROM PUBLIC;
CREATE TRIGGER follow_analysis_job AFTER UPDATE OF status ON twin.jobs
  FOR EACH ROW WHEN (NEW.type = 'analysis' AND NEW.status IS DISTINCT FROM OLD.status
                     AND NEW.status IN ('completed', 'failed', 'cancelled'))
  EXECUTE FUNCTION twin.follow_analysis_job();
COMMIT;
