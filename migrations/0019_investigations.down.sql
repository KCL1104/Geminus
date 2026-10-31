-- DEVELOPMENT-ONLY DOWN MIGRATION of 0019 (H-061 A3): AI investigations,
-- their ledger, the labels, the deny triggers and the investigation spend
-- ceiling are dropped; protect_job_state is 0007's again,
-- protect_proposal_content 0001's and check_proposal_effect 0013's, and the
-- replaced checks are restored as they were. It refuses, and changes
-- nothing, while any investigation (and so any labelled row) exists or an
-- audit event names one: an AI's labels and its applied proposals cannot be
-- unwritten. Never run automatically (db/README.md).
BEGIN;
SET LOCAL ROLE twin_owner;
-- FORCE ROW LEVEL SECURITY hides every row from the owner too; look
-- without it, inside this transaction only (as 0005, 0011 and 0018 do).
ALTER TABLE twin.investigations NO FORCE ROW LEVEL SECURITY;
ALTER TABLE twin.audit_events NO FORCE ROW LEVEL SECURITY;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM twin.investigations) OR EXISTS (SELECT 1 FROM twin.audit_events WHERE target_id::text LIKE 'inv\_%') THEN
    RAISE EXCEPTION 'migration 0019 down refuses: AI investigations exist, and their labelled rows and applied proposals cannot be unwritten'
      USING ERRCODE = '55000';
  END IF;
END $$;
ALTER TABLE twin.audit_events FORCE ROW LEVEL SECURITY;

DROP TRIGGER check_investigation_spend ON twin.ai_spend_reservations;
DROP FUNCTION twin.check_investigation_spend();
DROP TRIGGER guard_audit_investigation ON twin.audit_events;
DROP FUNCTION twin.guard_audit_investigation();
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['memberships', 'organizations', 'users', 'projects', 'models', 'model_versions', 'model_version_datasets',
                           'datasets', 'dataset_versions', 'imports', 'idempotency_keys', 'ai_proposal_images', 'investigations',
                           'simulation_runs', 'scenarios', 'ai_analyses', 'run_datasets', 'jobs', 'ai_proposals', 'model_drafts',
                           'investigation_tool_calls'] LOOP
    EXECUTE format('DROP TRIGGER deny_under_investigation ON twin.%I', t);
  END LOOP;
END $$;
DROP FUNCTION twin.deny_under_investigation();
DROP TRIGGER version_ai_edit_is_applied ON twin.model_versions;
DROP FUNCTION twin.check_version_ai_edit();
DROP TRIGGER draft_ai_edit_is_applied ON twin.model_drafts;
DROP FUNCTION twin.check_draft_ai_edit();
DROP TRIGGER guard_draft_ai_edit ON twin.model_drafts;
DROP FUNCTION twin.guard_draft_ai_edit();
DROP TRIGGER guard_investigation_proposal ON twin.ai_proposals;
DROP FUNCTION twin.guard_investigation_proposal();
DROP TRIGGER protect_investigation_label ON twin.ai_analyses;
DROP TRIGGER protect_investigation_label ON twin.simulation_runs;
DROP FUNCTION twin.protect_investigation_label();
DROP TRIGGER label_investigation ON twin.ai_analyses;
DROP FUNCTION twin.label_investigation_analysis();
DROP TRIGGER label_investigation ON twin.simulation_runs;
DROP FUNCTION twin.label_investigation_run();
DROP TRIGGER label_investigation ON twin.scenarios;
DROP FUNCTION twin.label_investigation_scenario();
DROP TRIGGER label_investigation ON twin.ai_proposals;
DROP FUNCTION twin.label_investigation_proposal();
DROP TRIGGER finish_ends_its_investigation ON twin.investigation_tool_calls;
DROP FUNCTION twin.check_finish_ends_its_investigation();
DROP TRIGGER protect_investigation_tool_call ON twin.investigation_tool_calls;
DROP FUNCTION twin.protect_investigation_tool_call();
DROP TRIGGER guard_investigation_job ON twin.jobs;
DROP FUNCTION twin.guard_investigation_job();
DROP TRIGGER investigation_job_completes_when_ended ON twin.jobs;
DROP FUNCTION twin.check_investigation_job_end();
DROP TRIGGER follow_investigation_job ON twin.jobs;
DROP FUNCTION twin.follow_investigation_job();
DROP TRIGGER investigation_created_with_its_job ON twin.investigations;
DROP FUNCTION twin.check_investigation_has_its_job();
DROP TRIGGER protect_investigation ON twin.investigations;
DROP FUNCTION twin.protect_investigation();

-- 0013's check, as it was.
CREATE OR REPLACE FUNCTION twin.check_proposal_effect() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  p record;
  base_number integer;
BEGIN
  SELECT status, mode, base_version_id, new_version_id, scenario_id, patch, patch_hash INTO p FROM twin.ai_proposals
    WHERE organization_id = NEW.organization_id AND model_id = NEW.model_id AND id = NEW.source_proposal_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'the source proposal is not visible to its organization' USING ERRCODE = '23514';
  END IF;
  IF TG_TABLE_NAME = 'model_versions' THEN
    SELECT version INTO base_number FROM twin.model_versions
      WHERE organization_id = NEW.organization_id AND model_id = NEW.model_id AND id = p.base_version_id;
    IF NOT coalesce(p.status = 'approved' AND p.mode = 'edit' AND p.new_version_id::text = NEW.id::text
                    AND NEW.parent_version_id::text = p.base_version_id::text AND NEW.version = base_number + 1, false) THEN
      RAISE EXCEPTION 'a version from a proposal is the approved edit proposal''s one effect: the next version after its base'
        USING ERRCODE = '23514';
    END IF;
  ELSE
    IF NOT coalesce(p.status = 'approved' AND p.mode = 'scenario' AND p.scenario_id::text = NEW.id::text
                    AND NEW.base_version_id::text = p.base_version_id::text AND NEW.patch = p.patch
                    AND NEW.patch_hash::text = p.patch_hash::text, false) THEN
      RAISE EXCEPTION 'a scenario from a proposal is the approved scenario proposal''s one effect: its base, patch and patch hash'
        USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NULL;
END $$;

-- 0001's protect_proposal, as it was (0001 fixed its search_path afterwards).
CREATE OR REPLACE FUNCTION twin.protect_proposal_content() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF ROW(NEW.organization_id,NEW.model_id,NEW.base_version_id,NEW.mode,NEW.source_import_id,NEW.intent)
     IS DISTINCT FROM ROW(OLD.organization_id,OLD.model_id,OLD.base_version_id,OLD.mode,OLD.source_import_id,OLD.intent) THEN
    RAISE EXCEPTION 'proposal identity/base is immutable' USING ERRCODE='55000';
  END IF;
  -- A decided or finished proposal never changes (API_DATABASE §8).
  IF OLD.status IN ('approved','rejected','conflicted','invalid','failed') AND NEW IS DISTINCT FROM OLD THEN
    RAISE EXCEPTION 'a % proposal is immutable', OLD.status USING ERRCODE='55000';
  END IF;
  -- Once generated, the review payload is exactly what the reviewer saw.
  IF OLD.status<>'generating' AND
     ROW(NEW.patch,NEW.patch_hash,NEW.assumptions,NEW.summary,NEW.risk_level,NEW.server_risk_level,
         NEW.provider_risk_claim,NEW.confidence,NEW.candidate_dataset_bindings,NEW.validation,
         NEW.expected_effects,NEW.questions,NEW.provider_metadata)
     IS DISTINCT FROM
     ROW(OLD.patch,OLD.patch_hash,OLD.assumptions,OLD.summary,OLD.risk_level,OLD.server_risk_level,
         OLD.provider_risk_claim,OLD.confidence,OLD.candidate_dataset_bindings,OLD.validation,
         OLD.expected_effects,OLD.questions,OLD.provider_metadata) THEN
    RAISE EXCEPTION 'reviewed proposal content is immutable; regenerate proposal' USING ERRCODE='55000';
  END IF;
  IF NEW.status<>OLD.status AND NOT (
       (OLD.status='generating' AND NEW.status IN ('ready','needs_input','invalid','failed')) OR
       (OLD.status='ready' AND NEW.status IN ('approved','rejected','conflicted')) OR
       (OLD.status='needs_input' AND NEW.status='rejected')) THEN
    RAISE EXCEPTION 'invalid proposal transition % -> %',OLD.status,NEW.status USING ERRCODE='55000';
  END IF;
  -- The decision record is written by the decision itself, for the scoped
  -- user, at the database's time; an approval names the effect created from
  -- this very proposal.
  IF NEW.status IN ('approved','rejected','conflicted') AND NEW.status<>OLD.status THEN
    IF NEW.decided_by IS NOT NULL AND NEW.decided_by::text IS DISTINCT FROM nullif(current_setting('app.user_id',true),'') THEN
      RAISE EXCEPTION 'a decision is recorded for the user who makes it' USING ERRCODE='42501';
    END IF;
    NEW.decided_at := now();
    IF NEW.status='approved' AND NOT (
         (NEW.mode='edit' AND EXISTS (SELECT 1 FROM twin.model_versions v
            WHERE v.organization_id=NEW.organization_id AND v.id=NEW.new_version_id AND v.source_proposal_id=NEW.id)) OR
         (NEW.mode='scenario' AND EXISTS (SELECT 1 FROM twin.scenarios s
            WHERE s.organization_id=NEW.organization_id AND s.id=NEW.scenario_id AND s.source_proposal_id=NEW.id))) THEN
      RAISE EXCEPTION 'an approval names the version or scenario created from this proposal' USING ERRCODE='23514';
    END IF;
  ELSIF ROW(NEW.decided_by,NEW.decided_at,NEW.new_version_id,NEW.scenario_id)
        IS DISTINCT FROM ROW(OLD.decided_by,OLD.decided_at,OLD.new_version_id,OLD.scenario_id) THEN
    RAISE EXCEPTION 'the decision record is written only by the decision' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END $$;

-- 0007's lifecycle, as it was.
CREATE OR REPLACE FUNCTION twin.protect_job_state() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  owner boolean := pg_catalog.pg_has_role(current_user,'twin_owner','USAGE');
  api boolean := pg_catalog.pg_has_role(current_user,'twin_api','USAGE');
  worker boolean := pg_catalog.pg_has_role(current_user,'twin_worker','USAGE');
BEGIN
  IF NOT (owner OR api OR worker) THEN
    RAISE EXCEPTION 'only the API, the worker and the schema owner write jobs' USING ERRCODE='55000';
  END IF;
  IF TG_OP='INSERT' THEN
    IF NOT (owner OR api) THEN
      RAISE EXCEPTION 'only the API enqueues a job' USING ERRCODE='55000';
    END IF;
    IF NEW.status<>'queued' OR NEW.attempt<>0 OR NEW.lease_token<>0 OR NEW.locked_at IS NOT NULL OR NEW.locked_by IS NOT NULL
       OR NEW.lease_expires_at IS NOT NULL OR NEW.heartbeat_at IS NOT NULL OR NEW.error IS NOT NULL THEN
      RAISE EXCEPTION 'a job starts queued, with no attempt, lease or error' USING ERRCODE='55000';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW IS NOT DISTINCT FROM OLD THEN
    RETURN NEW;
  END IF;
  IF OLD.status IN ('completed','failed','cancelled') THEN
    RAISE EXCEPTION 'a % job is final', OLD.status USING ERRCODE='55000';
  END IF;
  -- A running job may take any state; a queued one is claimed or cancelled.
  IF OLD.status='queued' AND NEW.status NOT IN ('queued','running','cancelled') THEN
    RAISE EXCEPTION 'a queued job cannot become %', NEW.status USING ERRCODE='55000';
  END IF;
  IF ROW(NEW.id,NEW.organization_id,NEW.type,NEW.payload,NEW.simulation_run_id,NEW.ai_proposal_id,NEW.import_id,
         NEW.ai_analysis_id,NEW.max_attempts,NEW.created_at)
     IS DISTINCT FROM
     ROW(OLD.id,OLD.organization_id,OLD.type,OLD.payload,OLD.simulation_run_id,OLD.ai_proposal_id,OLD.import_id,
         OLD.ai_analysis_id,OLD.max_attempts,OLD.created_at) THEN
    RAISE EXCEPTION 'a job''s identity and input are immutable' USING ERRCODE='55000';
  END IF;
  IF NEW.attempt<OLD.attempt OR NEW.lease_token<OLD.lease_token THEN
    RAISE EXCEPTION 'a job''s attempts and lease tokens never go back' USING ERRCODE='55000';
  END IF;
  IF NEW.status='queued' AND NEW.attempt>=NEW.max_attempts THEN
    RAISE EXCEPTION 'a queued job has an attempt left' USING ERRCODE='55000';
  END IF;
  IF NOT owner THEN
    IF api
       AND NOT (OLD.status='queued' AND NEW.status IN ('queued','cancelled')
                AND ROW(NEW.attempt,NEW.lease_token,NEW.locked_at,NEW.locked_by,NEW.lease_expires_at,NEW.heartbeat_at,NEW.error)
                    IS NOT DISTINCT FROM
                    ROW(OLD.attempt,OLD.lease_token,OLD.locked_at,OLD.locked_by,OLD.lease_expires_at,OLD.heartbeat_at,OLD.error)) THEN
      RAISE EXCEPTION 'the API only cancels or reschedules a queued job' USING ERRCODE='55000';
    END IF;
    IF worker AND OLD.status='queued' AND NEW.status<>'running' THEN
      RAISE EXCEPTION 'the worker only claims a queued job' USING ERRCODE='55000';
    END IF;
    -- A running job's lease is its holder's: renewed (heartbeat_at,
    -- lease_expires_at), never handed to another holder in place, and
    -- cleared when the job leaves running; its attempt and token change only
    -- on a claim. Workers share one role, so the database cannot tell
    -- holders apart: a worker login may still end any running job, set any
    -- lease length, or let a lease expire and claim the job again through
    -- the reaper; the lease token fences the old holder's writes.
    IF worker AND OLD.status='running'
       AND (ROW(NEW.attempt,NEW.lease_token) IS DISTINCT FROM ROW(OLD.attempt,OLD.lease_token)
            OR (NEW.status='running' AND ROW(NEW.locked_by,NEW.locked_at,NEW.error) IS DISTINCT FROM ROW(OLD.locked_by,OLD.locked_at,OLD.error))
            OR (NEW.status<>'running' AND (NEW.locked_by IS NOT NULL OR NEW.locked_at IS NOT NULL OR NEW.lease_expires_at IS NOT NULL))) THEN
      RAISE EXCEPTION 'the worker only renews, ends or retries a running job, under its lease' USING ERRCODE='55000';
    END IF;
  END IF;
  IF OLD.status='queued' AND NEW.status='running' AND (NEW.attempt<>OLD.attempt+1 OR NEW.lease_token<=OLD.lease_token) THEN
    RAISE EXCEPTION 'a claim starts the next attempt under a new lease token' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END $$;

-- The spend ledger's 0016 checks.
DROP INDEX twin.ai_spend_reservations_analysis_idx;
DROP INDEX twin.ai_spend_reservations_proposal_idx;
DROP INDEX twin.ai_spend_reservations_investigation_idx;
ALTER TABLE twin.ai_spend_reservations
  DROP CONSTRAINT ai_spend_reservations_planner_turn,
  DROP CONSTRAINT ai_spend_reservations_one_target,
  DROP CONSTRAINT ai_spend_reservations_role,
  DROP CONSTRAINT ai_spend_reservations_investigation_fk,
  DROP COLUMN investigation_id,
  ADD CONSTRAINT ai_spend_reservations_role_check CHECK (role IN ('modeler','scenario','analyst','importer')),
  ADD CONSTRAINT ai_spend_reservations_one_target CHECK (num_nonnulls(ai_proposal_id, ai_analysis_id, import_id) = 1);

-- The labels.
ALTER TABLE twin.model_versions DROP CONSTRAINT model_versions_ai_edit_fk, DROP COLUMN ai_edit_proposal_id;
ALTER TABLE twin.model_drafts DROP CONSTRAINT model_drafts_ai_proposal_fk, DROP COLUMN ai_proposal_id;
ALTER TABLE twin.ai_analyses
  DROP CONSTRAINT ai_analyses_investigation_labelled,
  DROP CONSTRAINT ai_analyses_investigation_explanations,
  DROP CONSTRAINT ai_analyses_investigation_fk,
  DROP COLUMN investigation_index,
  DROP COLUMN investigation_id;
ALTER TABLE twin.simulation_runs
  DROP CONSTRAINT simulation_runs_investigation_limits,
  DROP CONSTRAINT simulation_runs_investigation_labelled,
  DROP CONSTRAINT simulation_runs_investigation_runs,
  DROP CONSTRAINT simulation_runs_investigation_fk,
  DROP COLUMN investigation_slot,
  DROP COLUMN investigation_id;
ALTER TABLE twin.scenarios
  DROP CONSTRAINT scenarios_investigation_from_proposal,
  DROP CONSTRAINT scenarios_investigation_fk,
  DROP COLUMN investigation_id;
ALTER TABLE twin.ai_proposals
  DROP CONSTRAINT ai_proposals_draft_revision_when_applied,
  DROP CONSTRAINT ai_proposals_applied,
  DROP CONSTRAINT ai_proposals_effect_when_decided,
  DROP CONSTRAINT ai_proposals_status,
  DROP CONSTRAINT ai_proposals_investigation_labelled,
  DROP CONSTRAINT ai_proposals_investigation_slot,
  DROP CONSTRAINT ai_proposals_investigation_fk,
  DROP COLUMN applied_draft_revision,
  DROP COLUMN investigation_index,
  DROP COLUMN investigation_id,
  ADD CONSTRAINT ai_proposals_status_check CHECK (status IN ('generating','needs_input','ready','invalid','failed','approved','rejected','conflicted')),
  ADD CONSTRAINT ai_proposals_check2 CHECK (status='approved' OR (new_version_id IS NULL AND scenario_id IS NULL));

-- Jobs: 0001's checks.
DROP INDEX twin.jobs_one_investigation;
ALTER TABLE twin.jobs
  DROP CONSTRAINT jobs_resource_of_type,
  DROP CONSTRAINT jobs_one_resource,
  DROP CONSTRAINT jobs_type,
  DROP CONSTRAINT jobs_investigation_fk,
  DROP COLUMN investigation_id,
  ADD CONSTRAINT jobs_type_check CHECK (type IN ('simulation','model_proposal','import_profile','import_validate','import_mapping','analysis')),
  ADD CONSTRAINT jobs_check CHECK (num_nonnulls(simulation_run_id,ai_proposal_id,import_id,ai_analysis_id)=1),
  ADD CONSTRAINT jobs_check1 CHECK ((type='simulation' AND simulation_run_id IS NOT NULL) OR
         (type='model_proposal' AND ai_proposal_id IS NOT NULL) OR
         (type IN ('import_profile','import_validate','import_mapping') AND import_id IS NOT NULL) OR
         (type='analysis' AND ai_analysis_id IS NOT NULL));

DROP TABLE twin.investigation_tool_calls;
DROP TABLE twin.investigations;
DROP FUNCTION twin.duration_ms(jsonb);
DROP FUNCTION twin.current_investigation_id();
ALTER DOMAIN twin.public_id DROP CONSTRAINT public_id_kinds;
ALTER DOMAIN twin.public_id ADD CONSTRAINT public_id_check
  CHECK (VALUE ~ '^(org|usr|prj|mdl|mv|ds|dsv|imp|aip|scn|sim|ana|job|aud|req|art)_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
DROP DOMAIN twin.inv_id;
COMMIT;
