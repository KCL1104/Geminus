-- T-025 (AI_SPEC §4, API_DATABASE §4, D-002, D-011, D-016, D-027, D-046;
-- TEST-006, TEST-007): a proposal's request, its life while it is
-- generated, and its one job, kept consistent by the database for every
-- role.
--
--   * A proposal starts generating with nothing generated and no decision,
--     for every role: no summary, assumptions, patch or patch hash,
--     expected effects, questions, dataset bindings, risk, confidence,
--     validation, provider metadata, error, decider or decision time. The
--     API creates it for the scoped user. Its request never changes, for
--     any role: 0001 freezes the base, mode, source import and intent; the
--     output_locale and the selected entity IDs (added here), the ID, the
--     creator and the creation time are frozen here.
--   * While it is generated, only its job writes it: the API changes
--     nothing of a generating proposal (the worker fills it; 0002 keeps the
--     worker off every other state and column). It leaves generating for
--     ready, needs_input or invalid only in the transaction that completes
--     its job (proposal_generated_with_its_job), and for failed only once
--     its job has failed or been cancelled: when the job fails or is
--     cancelled, whoever ends it (the worker loop, the reaper, an operator;
--     the API has no route that cancels one), the proposal is failed in the
--     same statement with the job's error and anything written while it was
--     generated cleared (follow_proposal_job), as runs (0009) and imports
--     (0010) follow theirs. A job completes only once its proposal is
--     generated. A proposal has at most one job (0001's jobs_one_proposal).
--   * What each state holds: needs_input has its summary and questions and
--     no patch or risk; invalid has its failed validation and nothing else
--     generated (no summary, assumptions, patch or patch hash, expected
--     effects, questions, dataset bindings, risk levels or claim,
--     confidence: nothing the provider wrote, AI_SPEC §4, D-062 (c)); failed
--     has its error and nothing generated (nor provider metadata), and no
--     other state has an error; the provider metadata is an object. 0001
--     already holds ready to its patch, valid validation, risk, confidence
--     and patch hash.
--   * Only the API, the worker and the schema owner write proposals; the
--     owner is otherwise held only to the rules above.
--
-- No proposal exists before T-025 stores them (0011 refuses a database that
-- has one), so the new checks hold for every row.
BEGIN;
SET LOCAL ROLE twin_owner;

ALTER TABLE twin.ai_proposals
  ADD COLUMN output_locale text NOT NULL DEFAULT 'en' CONSTRAINT ai_proposals_output_locale CHECK (output_locale IN ('en', 'zh-Hant')),
  ADD COLUMN selected_entity_ids jsonb NOT NULL DEFAULT '[]' CONSTRAINT ai_proposals_selected_entity_ids
    CHECK (jsonb_typeof(selected_entity_ids) = 'array' AND jsonb_array_length(selected_entity_ids) <= 500),
  ADD CONSTRAINT ai_proposals_error_iff_failed CHECK ((status = 'failed') = (error IS NOT NULL)),
  ADD CONSTRAINT ai_proposals_needs_input_content CHECK (status <> 'needs_input' OR coalesce(
    summary IS NOT NULL AND jsonb_array_length(questions) > 0 AND patch IS NULL AND patch_hash IS NULL AND risk_level IS NULL, false)),
  ADD CONSTRAINT ai_proposals_invalid_content CHECK (status <> 'invalid' OR coalesce(
    validation->>'valid' = 'false' AND summary IS NULL AND patch IS NULL AND patch_hash IS NULL
    AND num_nonnulls(server_risk_level, provider_risk_claim, risk_level, confidence) = 0
    AND assumptions = '[]' AND expected_effects = '[]' AND questions = '[]' AND candidate_dataset_bindings = '[]', false)),
  ADD CONSTRAINT ai_proposals_failed_content CHECK (status <> 'failed' OR coalesce(
    summary IS NULL AND patch IS NULL AND patch_hash IS NULL
    AND num_nonnulls(server_risk_level, provider_risk_claim, risk_level, confidence, validation) = 0
    AND assumptions = '[]' AND expected_effects = '[]' AND questions = '[]' AND candidate_dataset_bindings = '[]'
    AND provider_metadata = '{}', false)),
  ADD CONSTRAINT ai_proposals_provider_metadata_object CHECK (jsonb_typeof(provider_metadata) = 'object');

CREATE FUNCTION twin.protect_proposal_lifecycle() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  owner boolean := pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE');
  api boolean := pg_catalog.pg_has_role(current_user, 'twin_api', 'USAGE');
  worker boolean := pg_catalog.pg_has_role(current_user, 'twin_worker', 'USAGE');
BEGIN
  IF NOT (owner OR api OR worker) THEN
    RAISE EXCEPTION 'only the API, the worker and the schema owner write proposals' USING ERRCODE = '55000';
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NEW.status <> 'generating' OR NEW.summary IS NOT NULL OR NEW.patch IS NOT NULL OR NEW.patch_hash IS NOT NULL
       OR NEW.assumptions IS DISTINCT FROM '[]'::jsonb OR NEW.expected_effects IS DISTINCT FROM '[]'::jsonb
       OR NEW.questions IS DISTINCT FROM '[]'::jsonb OR NEW.candidate_dataset_bindings IS DISTINCT FROM '[]'::jsonb
       OR NEW.provider_metadata IS DISTINCT FROM '{}'::jsonb
       OR num_nonnulls(NEW.server_risk_level, NEW.provider_risk_claim, NEW.risk_level, NEW.confidence, NEW.validation, NEW.error,
                       NEW.decided_by, NEW.decided_at) <> 0 THEN
      RAISE EXCEPTION 'a proposal starts generating with nothing generated and no decision' USING ERRCODE = '55000';
    END IF;
    IF api AND NOT owner AND NEW.created_by::text IS DISTINCT FROM nullif(current_setting('app.user_id', true), '') THEN
      RAISE EXCEPTION 'the API creates a proposal for the scoped user' USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
  END IF;
  IF ROW(NEW.id, NEW.output_locale, NEW.selected_entity_ids, NEW.created_by, NEW.created_at)
     IS DISTINCT FROM ROW(OLD.id, OLD.output_locale, OLD.selected_entity_ids, OLD.created_by, OLD.created_at) THEN
    RAISE EXCEPTION 'a proposal''s request (output locale, selected entities), ID, creator and creation time never change' USING ERRCODE = '55000';
  END IF;
  IF NEW IS NOT DISTINCT FROM OLD OR OLD.status <> 'generating' THEN
    RETURN NEW; -- a generated proposal is 0001's protect_proposal's
  END IF;
  -- Only the job fails a proposal, whoever asks, the owner included, from a
  -- trigger or not: a proposal fails only once its job has failed or been
  -- cancelled, which follow_proposal_job, an AFTER trigger of the job's
  -- update, sees (H-003 review I1: not the trigger depth, which any other
  -- trigger would pass). A proposal failed while its job still runs would
  -- make the job's end fail its follow (an operator ends the job; the
  -- proposal follows). A job is created queued (0007), so outside that
  -- statement no generating proposal has an ended job.
  IF NEW.status = 'failed' THEN
    IF NOT EXISTS (SELECT 1 FROM twin.jobs j
                   WHERE j.organization_id = NEW.organization_id AND j.ai_proposal_id = NEW.id
                     AND j.status IN ('failed', 'cancelled')) THEN
      RAISE EXCEPTION 'a proposal fails only with its job: end the job, and the proposal follows' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
  END IF;
  IF api AND NOT owner THEN
    RAISE EXCEPTION 'the API changes nothing of a proposal while it is generated; its job does' USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_proposal_lifecycle() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER protect_proposal_lifecycle BEFORE INSERT OR UPDATE ON twin.ai_proposals
  FOR EACH ROW EXECUTE FUNCTION twin.protect_proposal_lifecycle();

-- A proposal is generated in the transaction that completes its job (the
-- worker's Finish), for every role: a generated proposal whose job could
-- still fail would be left generated by a failed job.
CREATE FUNCTION twin.check_proposal_generated_with_its_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM twin.jobs j
                 WHERE j.organization_id = NEW.organization_id AND j.ai_proposal_id = NEW.id AND j.status = 'completed') THEN
    RAISE EXCEPTION 'a proposal is generated in the transaction that completes its job' USING ERRCODE = '55000';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_proposal_generated_with_its_job() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER proposal_generated_with_its_job AFTER UPDATE OF status ON twin.ai_proposals
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW WHEN (OLD.status = 'generating' AND NEW.status IN ('ready', 'needs_input', 'invalid'))
  EXECUTE FUNCTION twin.check_proposal_generated_with_its_job();

-- Runs with the privileges of whoever ends the job: the worker (its column
-- UPDATE on ai_proposals covers status, error and every generated column it
-- clears), the API or an operator.
CREATE FUNCTION twin.follow_proposal_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM twin.ai_proposals
    WHERE organization_id = NEW.organization_id AND id = NEW.ai_proposal_id FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'a proposal job''s proposal is not visible to its organization' USING ERRCODE = '55000';
  END IF;
  IF NEW.status = 'completed' THEN
    IF s = 'generating' THEN
      RAISE EXCEPTION 'a proposal job completes only once its proposal is generated' USING ERRCODE = '55000';
    END IF;
  ELSIF s = 'generating' THEN
    -- A proposal past generating stays as it is, so that ending its job
    -- never fails. A failed proposal holds only its error
    -- (ai_proposals_failed_content): whatever an attempt wrote while it was
    -- generated is cleared, so that the check never stops a job's end.
    UPDATE twin.ai_proposals SET status = 'failed',
        error = coalesce(NEW.error, CASE WHEN NEW.status = 'cancelled'
          THEN '{"code":"JOB_CANCELLED","message":"the proposal job was ended before the proposal was generated","retryable":false}'::jsonb
          ELSE '{"code":"INTERNAL","message":"the proposal job failed","retryable":false}'::jsonb END),
        summary = NULL, assumptions = '[]', patch = NULL, patch_hash = NULL, candidate_dataset_bindings = '[]',
        expected_effects = '[]', questions = '[]', server_risk_level = NULL, provider_risk_claim = NULL, risk_level = NULL,
        confidence = NULL, validation = NULL, provider_metadata = '{}'
      WHERE organization_id = NEW.organization_id AND id = NEW.ai_proposal_id;
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.follow_proposal_job() FROM PUBLIC;
CREATE TRIGGER follow_proposal_job AFTER UPDATE OF status ON twin.jobs
  FOR EACH ROW WHEN (NEW.type = 'model_proposal' AND NEW.status IS DISTINCT FROM OLD.status
                     AND NEW.status IN ('completed', 'failed', 'cancelled'))
  EXECUTE FUNCTION twin.follow_proposal_job();
COMMIT;
