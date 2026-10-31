-- H-061 slice A3 (H-060, H-063; docs/design/H-061_BACKGROUND_INVESTIGATOR.md
-- §5, §6, §8, §11-§13; D-008 as amended; AGENTS.md §2 as amended by H-056;
-- REQ-018, REQ-019, REQ-021): an AI investigation, its tool-call ledger, the
-- labels on what it makes, and the database's own limits on it. Nothing
-- here is reachable while GEMINUS_INVESTIGATIONS is off: no route, job
-- handler or executor writes these tables until slices A6 and A7.
--
--   * twin.investigations is the stored acting-for scope: the organization,
--     the person who asked (created_by), the investigation, with its frozen
--     request, run settings, caps and deadline (created_at + 20 minutes). It
--     is written only at INSERT, by the API for the scoped user (an owner or
--     editor); its request never changes; it ends once, by the routes this
--     migration names for each role (protect_investigation), and an ended
--     investigation is final. One running per person per organization, at
--     most 10 per organization per UTC day.
--   * twin.investigation_tool_calls is the ledger where the worker and the
--     API meet: the worker appends a call (seq 1..14, strictly in order,
--     under the current lease of the investigation's one job, while it runs
--     and before its deadline); the API carries it out once, in the
--     investigation's acting-for transaction for its requester (pending ->
--     done, refused or failed, with the effect's references); the worker
--     writes what the AI is shown (the observation) once. The row is both
--     the idempotency record and the checkpoint.
--   * twin.jobs gains the type `investigation` (one job per investigation,
--     max attempts up to the table's 10). A job that fails while its
--     investigation runs stops it with ai_failed; a job completes or is
--     cancelled only with its investigation's end.
--   * Labels: ai_proposals, scenarios, simulation_runs and ai_analyses get
--     investigation_id, defaulting to the transaction's app.investigation_id;
--     a row is labelled exactly when it is written in that scope, for its
--     requester, while the investigation runs (the acting-for guard), and
--     the index or slot that caps it is assigned here. Labelled runs keep
--     D-007's limits, at most 10 replications, 28 days and a sampled trace.
--     A proposal the AI acts on becomes `applied` (never `approved`); a
--     person may still approve or reject it while it is ready.
--   * Deny triggers: while app.investigation_id is set, the database refuses
--     publishing, deciding, and every write outside the investigation's
--     tool effects, whatever the API role's grants allow.
--   * The spend ledger: planner turns (`investigator`) are reserved on the
--     investigation; an investigation spends at most USD 2.50 (settled plus
--     open reservations, its proposals' and analyses' included) and at most
--     20 planner turns.
--
-- Grants: the worker gets SELECT, INSERT and column UPDATEs on the two new
-- tables only (owner-approved, H-063); no grant on an existing table changes.
BEGIN;
SET LOCAL ROLE twin_owner;

-- D-001: an investigation's ID is "inv_" + ULID. The audit target domain
-- learns the kind; its check is found by its definition, never by a name.
CREATE DOMAIN twin.inv_id AS text CHECK (VALUE ~ '^inv_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
GRANT USAGE ON TYPE twin.inv_id TO twin_api, twin_worker, twin_provisioner;
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO STRICT c FROM pg_constraint
    WHERE contypid = 'twin.public_id'::regtype AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%(org|usr|prj|mdl|mv|ds|dsv|imp|aip|scn|sim|ana|job|aud|req|art)\_%';
  EXECUTE format('ALTER DOMAIN twin.public_id DROP CONSTRAINT %I', c);
END $$;
ALTER DOMAIN twin.public_id ADD CONSTRAINT public_id_kinds
  CHECK (VALUE ~ '^(org|usr|prj|mdl|mv|ds|dsv|imp|aip|scn|sim|ana|job|aud|req|art|inv)_[0-7][0-9A-HJKMNP-TV-Z]{25}$');

-- The investigation a transaction acts for (empty: none). Transaction-local,
-- like the tenant scope, and listed in tenant.ScopeSettings so a pooled
-- connection that still carries it is discarded.
CREATE FUNCTION twin.current_investigation_id() RETURNS text
  LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp
  AS $$ SELECT nullif(current_setting('app.investigation_id', true), '') $$;
-- A duration (common.schema.json Duration) in milliseconds; NULL for
-- anything else.
CREATE FUNCTION twin.duration_ms(d jsonb) RETURNS numeric
  LANGUAGE sql IMMUTABLE PARALLEL SAFE SET search_path = pg_catalog, pg_temp
  RETURN CASE WHEN jsonb_typeof(d) = 'object' AND jsonb_typeof(d -> 'value') = 'number'
    THEN (d ->> 'value')::numeric * CASE d ->> 'unit' WHEN 'millisecond' THEN 1 WHEN 'second' THEN 1000 WHEN 'minute' THEN 60000
      WHEN 'hour' THEN 3600000 WHEN 'day' THEN 86400000 END END;
REVOKE ALL ON FUNCTION twin.current_investigation_id(), twin.duration_ms(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION twin.current_investigation_id(), twin.duration_ms(jsonb) TO twin_api, twin_worker;

-- The stored acting-for scope (H-061 §8).
CREATE TABLE twin.investigations (
  id twin.inv_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL REFERENCES twin.organizations(id),
  model_id twin.mdl_id NOT NULL,
  base_version_id twin.mv_id NOT NULL,
  mode text NOT NULL CONSTRAINT investigations_mode CHECK (mode IN ('scenario', 'edit')),
  source text NOT NULL CONSTRAINT investigations_source CHECK (source IN ('request', 'analysis')),
  source_analysis_id twin.ana_id,
  -- the person's words (untrusted; never in audit): only from a request
  intent text CONSTRAINT investigations_intent CHECK (char_length(intent) BETWEEN 1 AND 20000),
  context jsonb NOT NULL DEFAULT '{}' CONSTRAINT investigations_context CHECK (jsonb_typeof(context) = 'object'),
  output_locale text NOT NULL CONSTRAINT investigations_output_locale CHECK (output_locale IN ('en', 'zh-Hant')),
  research boolean NOT NULL DEFAULT false,
  proposal_model text NOT NULL CONSTRAINT investigations_proposal_model CHECK (proposal_model IN ('muse-spark-1.3', 'muse-wonder-1.0')),
  planner_model text NOT NULL CONSTRAINT investigations_planner_model CHECK (planner_model IN ('muse-spark-1.3', 'muse-wonder-1.0')),
  prompt_template_version text NOT NULL
    CONSTRAINT investigations_prompt_template_version CHECK (prompt_template_version ~ '^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,127}$'),
  objective jsonb,
  run_config jsonb,
  run_config_hash twin.sha256,
  baseline_name text CONSTRAINT investigations_baseline_name CHECK (char_length(baseline_name) BETWEEN 1 AND 200),
  baseline_run_id twin.sim_id,
  caps jsonb NOT NULL,
  status text NOT NULL CONSTRAINT investigations_status CHECK (status IN ('running', 'finished', 'stopped')),
  reason text CONSTRAINT investigations_reason CHECK (reason IN ('finished_by_ai', 'draft_written', 'limit_reached', 'time_limit',
    'spend_cap', 'invalid_answer', 'ai_failed', 'role_lost', 'stopped_by_person')),
  ai_outcome text CONSTRAINT investigations_ai_outcome CHECK (ai_outcome IN ('improvement_found', 'no_improvement', 'out_of_ideas', 'needs_person')),
  stop_requested_by twin.usr_id REFERENCES twin.users(id),
  deadline_at timestamptz NOT NULL,
  created_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  UNIQUE (organization_id, id),
  UNIQUE (organization_id, model_id, id),
  FOREIGN KEY (organization_id, model_id) REFERENCES twin.models(organization_id, id),
  FOREIGN KEY (organization_id, model_id, base_version_id) REFERENCES twin.model_versions(organization_id, model_id, id),
  FOREIGN KEY (organization_id, source_analysis_id) REFERENCES twin.ai_analyses(organization_id, id),
  FOREIGN KEY (organization_id, baseline_run_id) REFERENCES twin.simulation_runs(organization_id, id),
  -- the wall time is 20 minutes from creation, queue time included
  CONSTRAINT investigations_deadline CHECK (deadline_at = created_at + interval '20 minutes'),
  -- running has no end; an end has its reason, its time and its status
  CONSTRAINT investigations_reason_iff_ended CHECK ((status = 'running') = (reason IS NULL)),
  CONSTRAINT investigations_finished_iff_ended CHECK ((status = 'running') = (finished_at IS NULL)),
  CONSTRAINT investigations_end_status CHECK (status = 'running'
    OR (status = 'finished') = (reason IN ('finished_by_ai', 'draft_written', 'limit_reached'))),
  CONSTRAINT investigations_ai_outcome_iff_finish CHECK ((reason IS NOT DISTINCT FROM 'finished_by_ai') = (ai_outcome IS NOT NULL)),
  CONSTRAINT investigations_stop_iff_person CHECK ((reason IS NOT DISTINCT FROM 'stopped_by_person') = (stop_requested_by IS NOT NULL)),
  -- where it came from: a request has the person's words, an analysis its ID
  -- and its reused baseline
  CONSTRAINT investigations_intent_iff_request CHECK ((source = 'request') = (intent IS NOT NULL)),
  CONSTRAINT investigations_analysis_iff_source CHECK ((source = 'analysis') = (source_analysis_id IS NOT NULL)),
  CONSTRAINT investigations_analysis_reuses_baseline CHECK (source <> 'analysis' OR (mode = 'scenario' AND baseline_run_id IS NOT NULL)),
  -- scenario mode has its objective and frozen run settings; edit mode none
  -- of what runs need
  CONSTRAINT investigations_scenario_settings CHECK ((mode = 'scenario') = (objective IS NOT NULL)
    AND (mode = 'scenario') = (run_config IS NOT NULL) AND (mode = 'scenario') = (run_config_hash IS NOT NULL)),
  CONSTRAINT investigations_edit_has_no_runs CHECK (mode = 'scenario' OR (baseline_run_id IS NULL AND baseline_name IS NULL)),
  CONSTRAINT investigations_baseline_named CHECK (mode <> 'scenario' OR (baseline_run_id IS NULL) = (baseline_name IS NOT NULL)),
  CONSTRAINT investigations_objective CHECK (objective IS NULL OR objective IN (
    '{"metric_path":"/metrics/lead_time_minutes/p50","better":"lower"}'::jsonb,
    '{"metric_path":"/metrics/lead_time_minutes/mean","better":"lower"}'::jsonb,
    '{"metric_path":"/metrics/lead_time_minutes/p95","better":"lower"}'::jsonb,
    '{"metric_path":"/metrics/throughput/mean","better":"higher"}'::jsonb)),
  -- H-060's run settings: RunConfig's members, at most 10 replications and
  -- 28 days, a warmup shorter than the horizon, no full trace
  CONSTRAINT investigations_run_config CHECK (run_config IS NULL OR coalesce(
    jsonb_typeof(run_config) = 'object'
    AND run_config - ARRAY['horizon', 'warmup', 'replications', 'seed', 'trace_level'] = '{}'::jsonb
    AND run_config ?& ARRAY['horizon', 'warmup', 'replications', 'seed', 'trace_level']
    AND CASE WHEN jsonb_typeof(run_config -> 'replications') = 'number'
      THEN (run_config ->> 'replications')::numeric IN (1, 2, 3, 4, 5, 6, 7, 8, 9, 10) ELSE false END
    AND CASE WHEN jsonb_typeof(run_config -> 'seed') = 'number'
      THEN (run_config ->> 'seed')::numeric BETWEEN 0 AND 9007199254740991 AND (run_config ->> 'seed')::numeric % 1 = 0 ELSE false END
    AND run_config ->> 'trace_level' IN ('none', 'sampled')
    AND twin.duration_ms(run_config -> 'horizon') > 0 AND twin.duration_ms(run_config -> 'horizon') <= 2419200000
    AND twin.duration_ms(run_config -> 'warmup') >= 0
    AND twin.duration_ms(run_config -> 'warmup') < twin.duration_ms(run_config -> 'horizon'), false)),
  -- H-060's caps, frozen: 3 what-ifs, 4 runs (3 with a reused baseline),
  -- 4 explanations, 14 steps, 20 minutes; edit mode 1 proposal, no runs
  CONSTRAINT investigations_caps CHECK (caps = CASE
    WHEN mode = 'edit' THEN '{"what_ifs":1,"runs":0,"explanations":0,"steps":14,"minutes":20}'::jsonb
    WHEN baseline_run_id IS NOT NULL THEN '{"what_ifs":3,"runs":3,"explanations":4,"steps":14,"minutes":20}'::jsonb
    ELSE '{"what_ifs":3,"runs":4,"explanations":4,"steps":14,"minutes":20}'::jsonb END)
);
-- One running investigation per person per organization (409).
CREATE UNIQUE INDEX investigations_one_running ON twin.investigations(organization_id, created_by) WHERE status = 'running';
-- listInvestigations, and the 10-per-day count.
CREATE INDEX investigations_model_idx ON twin.investigations(organization_id, model_id, created_at, id);
CREATE INDEX investigations_created_idx ON twin.investigations(organization_id, created_at);

-- The tool-call ledger (H-061 §5, §11). Identified by (investigation, seq).
CREATE TABLE twin.investigation_tool_calls (
  organization_id twin.org_id NOT NULL,
  investigation_id twin.inv_id NOT NULL,
  seq integer NOT NULL CONSTRAINT investigation_tool_calls_seq CHECK (seq BETWEEN 1 AND 14),
  tool text NOT NULL CONSTRAINT investigation_tool_calls_tool CHECK (tool IN ('propose_what_if', 'save_scenario', 'run_what_if',
    'explain_results', 'propose_model_edit', 'write_to_draft', 'finish', 'invalid_call')),
  -- the server's canonical form of the decoded arguments and its hash; none
  -- for invalid model output (its arguments are never stored). 8 KiB, plus
  -- room for jsonb's own spacing.
  arguments jsonb CONSTRAINT investigation_tool_calls_arguments
    CHECK (jsonb_typeof(arguments) = 'object' AND octet_length(arguments::text) <= 9216),
  arguments_hash twin.sha256,
  -- the AI's note on the call: sanitized AI text, shown labelled, never fed back
  note text CONSTRAINT investigation_tool_calls_note CHECK (char_length(note) BETWEEN 1 AND 500),
  job_id twin.job_id NOT NULL,
  lease_token bigint NOT NULL CONSTRAINT investigation_tool_calls_lease_token CHECK (lease_token > 0),
  status text NOT NULL CONSTRAINT investigation_tool_calls_status CHECK (status IN ('pending', 'done', 'refused', 'failed')),
  proposal_id twin.aip_id,
  scenario_id twin.scn_id,
  baseline_run_id twin.sim_id,
  run_id twin.sim_id,
  analysis_id twin.ana_id,
  draft_revision bigint CONSTRAINT investigation_tool_calls_draft_revision CHECK (draft_revision > 0),
  error jsonb CONSTRAINT investigation_tool_calls_error CHECK (jsonb_typeof(error) = 'object'
    AND error - ARRAY['code', 'path'] = '{}'::jsonb AND error ?& ARRAY['code', 'path'] AND jsonb_typeof(error -> 'path') = 'string'
    AND error ->> 'code' IN ('LIMIT_REACHED', 'BASE_MOVED', 'DRAFT_CHANGED', 'PROPOSAL_NOT_READY', 'PROPOSAL_DECIDED', 'RECHECK_FAILED',
      'RUN_NOT_COMPLETED', 'ALREADY_RUN', 'NOT_IN_INVESTIGATION', 'MODE_MISMATCH', 'UNKNOWN_TOOL', 'INVALID_ARGUMENTS',
      'SERVICE_UNAVAILABLE', 'DATASET_STORAGE_FAILED', 'INTERNAL')),
  -- the tool result the AI is shown (server-written, at most 6 KiB in the
  -- contract; 8 KiB here)
  observation jsonb CONSTRAINT investigation_tool_calls_observation
    CHECK (jsonb_typeof(observation) = 'object' AND octet_length(observation::text) <= 8192),
  observation_hash twin.sha256,
  requested_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,
  observed_at timestamptz,
  PRIMARY KEY (organization_id, investigation_id, seq),
  FOREIGN KEY (organization_id, investigation_id) REFERENCES twin.investigations(organization_id, id),
  FOREIGN KEY (organization_id, job_id) REFERENCES twin.jobs(organization_id, id),
  FOREIGN KEY (organization_id, proposal_id) REFERENCES twin.ai_proposals(organization_id, id),
  FOREIGN KEY (organization_id, scenario_id) REFERENCES twin.scenarios(organization_id, id),
  FOREIGN KEY (organization_id, baseline_run_id) REFERENCES twin.simulation_runs(organization_id, id),
  FOREIGN KEY (organization_id, run_id) REFERENCES twin.simulation_runs(organization_id, id),
  FOREIGN KEY (organization_id, analysis_id) REFERENCES twin.ai_analyses(organization_id, id),
  CONSTRAINT investigation_tool_calls_arguments_iff_call CHECK ((tool = 'invalid_call') = (arguments IS NULL)
    AND (arguments IS NULL) = (arguments_hash IS NULL)),
  CONSTRAINT investigation_tool_calls_invalid_is_refused CHECK (tool <> 'invalid_call'
    OR (status = 'refused' AND error ->> 'code' IN ('UNKNOWN_TOOL', 'INVALID_ARGUMENTS'))),
  CONSTRAINT investigation_tool_calls_resolved_iff_not_pending CHECK ((status = 'pending') = (resolved_at IS NULL)),
  CONSTRAINT investigation_tool_calls_error_iff_refused CHECK ((status IN ('refused', 'failed')) = (error IS NOT NULL)),
  -- a done call names what it made, by its tool; nothing else does
  CONSTRAINT investigation_tool_calls_effect CHECK (CASE WHEN status <> 'done'
      THEN num_nonnulls(proposal_id, scenario_id, baseline_run_id, run_id, analysis_id, draft_revision) = 0
    ELSE CASE tool
      WHEN 'propose_what_if' THEN proposal_id IS NOT NULL AND num_nonnulls(scenario_id, baseline_run_id, run_id, analysis_id, draft_revision) = 0
      WHEN 'propose_model_edit' THEN proposal_id IS NOT NULL AND num_nonnulls(scenario_id, baseline_run_id, run_id, analysis_id, draft_revision) = 0
      WHEN 'save_scenario' THEN scenario_id IS NOT NULL AND num_nonnulls(proposal_id, baseline_run_id, run_id, analysis_id, draft_revision) = 0
      WHEN 'run_what_if' THEN run_id IS NOT NULL AND baseline_run_id IS NOT NULL AND num_nonnulls(proposal_id, scenario_id, analysis_id, draft_revision) = 0
      WHEN 'explain_results' THEN analysis_id IS NOT NULL AND num_nonnulls(proposal_id, scenario_id, baseline_run_id, run_id, draft_revision) = 0
      WHEN 'write_to_draft' THEN draft_revision IS NOT NULL AND num_nonnulls(proposal_id, scenario_id, baseline_run_id, run_id, analysis_id) = 0
      WHEN 'finish' THEN num_nonnulls(proposal_id, scenario_id, baseline_run_id, run_id, analysis_id, draft_revision) = 0
      ELSE false END END),
  CONSTRAINT investigation_tool_calls_observed CHECK ((observation IS NULL) = (observation_hash IS NULL)
    AND (observation IS NULL) = (observed_at IS NULL) AND (observation IS NULL OR status <> 'pending'))
);
-- The executor's discovery: the pending calls of an organization.
CREATE INDEX investigation_tool_calls_pending ON twin.investigation_tool_calls(organization_id, requested_at)
  WHERE status = 'pending';

-- RLS as 0002: the organization's own rows only, for every role.
ALTER TABLE twin.investigations ENABLE ROW LEVEL SECURITY;
ALTER TABLE twin.investigations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON twin.investigations FOR ALL
  USING (organization_id::text = twin.current_organization_id()) WITH CHECK (organization_id::text = twin.current_organization_id());
ALTER TABLE twin.investigation_tool_calls ENABLE ROW LEVEL SECURITY;
ALTER TABLE twin.investigation_tool_calls FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON twin.investigation_tool_calls FOR ALL
  USING (organization_id::text = twin.current_organization_id()) WITH CHECK (organization_id::text = twin.current_organization_id());

-- Grants (H-063): the API starts and stops investigations and carries out
-- calls; the worker reads, appends calls, records what the AI was shown and
-- ends its own investigation. Nobody deletes either.
REVOKE ALL ON twin.investigations, twin.investigation_tool_calls FROM PUBLIC;
GRANT SELECT, INSERT ON twin.investigations TO twin_api;
GRANT UPDATE (status, reason, finished_at, stop_requested_by) ON twin.investigations TO twin_api;
GRANT SELECT ON twin.investigations TO twin_worker;
GRANT UPDATE (status, reason, ai_outcome, finished_at) ON twin.investigations TO twin_worker;
GRANT SELECT ON twin.investigation_tool_calls TO twin_api;
GRANT UPDATE (status, proposal_id, scenario_id, baseline_run_id, run_id, analysis_id, draft_revision, error, resolved_at)
  ON twin.investigation_tool_calls TO twin_api;
GRANT SELECT, INSERT ON twin.investigation_tool_calls TO twin_worker;
GRANT UPDATE (observation, observation_hash, observed_at) ON twin.investigation_tool_calls TO twin_worker;

-- Jobs: the investigation's one durable job (H-061 §5.1). The 0001 checks
-- on the type and the resource are replaced, each found by its definition.
ALTER TABLE twin.jobs ADD COLUMN investigation_id twin.inv_id,
  ADD CONSTRAINT jobs_investigation_fk FOREIGN KEY (organization_id, investigation_id) REFERENCES twin.investigations(organization_id, id);
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO STRICT c FROM pg_constraint WHERE conrelid = 'twin.jobs'::regclass AND contype = 'c'
    AND pg_get_constraintdef(oid) LIKE 'CHECK ((type = ANY (ARRAY[%';
  EXECUTE format('ALTER TABLE twin.jobs DROP CONSTRAINT %I', c);
  SELECT conname INTO STRICT c FROM pg_constraint WHERE conrelid = 'twin.jobs'::regclass AND contype = 'c'
    AND pg_get_constraintdef(oid) = 'CHECK ((num_nonnulls(simulation_run_id, ai_proposal_id, import_id, ai_analysis_id) = 1))';
  EXECUTE format('ALTER TABLE twin.jobs DROP CONSTRAINT %I', c);
  SELECT conname INTO STRICT c FROM pg_constraint WHERE conrelid = 'twin.jobs'::regclass AND contype = 'c'
    AND pg_get_constraintdef(oid) LIKE 'CHECK ((((type = ''simulation''::text) AND (simulation_run_id IS NOT NULL)) OR %';
  EXECUTE format('ALTER TABLE twin.jobs DROP CONSTRAINT %I', c);
END $$;
ALTER TABLE twin.jobs
  ADD CONSTRAINT jobs_type CHECK (type IN ('simulation', 'model_proposal', 'import_profile', 'import_validate', 'import_mapping',
    'analysis', 'investigation')),
  ADD CONSTRAINT jobs_one_resource CHECK (num_nonnulls(simulation_run_id, ai_proposal_id, import_id, ai_analysis_id, investigation_id) = 1),
  ADD CONSTRAINT jobs_resource_of_type CHECK ((type = 'simulation' AND simulation_run_id IS NOT NULL) OR
    (type = 'model_proposal' AND ai_proposal_id IS NOT NULL) OR
    (type IN ('import_profile', 'import_validate', 'import_mapping') AND import_id IS NOT NULL) OR
    (type = 'analysis' AND ai_analysis_id IS NOT NULL) OR
    (type = 'investigation' AND investigation_id IS NOT NULL));
CREATE UNIQUE INDEX jobs_one_investigation ON twin.jobs(organization_id, investigation_id) WHERE investigation_id IS NOT NULL;

-- 0007's lifecycle, with the investigation in a job's frozen identity.
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
         NEW.ai_analysis_id,NEW.investigation_id,NEW.max_attempts,NEW.created_at)
     IS DISTINCT FROM
     ROW(OLD.id,OLD.organization_id,OLD.type,OLD.payload,OLD.simulation_run_id,OLD.ai_proposal_id,OLD.import_id,
         OLD.ai_analysis_id,OLD.investigation_id,OLD.max_attempts,OLD.created_at) THEN
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

-- Labels (H-061 §10, §11): the investigation a row was written for. The
-- DEFAULT reads the transaction's scope, so the services' own INSERTs label
-- an investigation's rows without naming the column; the guards below
-- refuse a missing or forged label.
ALTER TABLE twin.ai_proposals
  ADD COLUMN investigation_id twin.inv_id DEFAULT (twin.current_investigation_id())::twin.inv_id,
  ADD COLUMN investigation_index integer CONSTRAINT ai_proposals_investigation_index CHECK (investigation_index BETWEEN 1 AND 3),
  ADD COLUMN applied_draft_revision bigint CONSTRAINT ai_proposals_applied_draft_revision CHECK (applied_draft_revision > 0),
  ADD CONSTRAINT ai_proposals_investigation_fk FOREIGN KEY (organization_id, model_id, investigation_id)
    REFERENCES twin.investigations(organization_id, model_id, id),
  ADD CONSTRAINT ai_proposals_investigation_slot UNIQUE (organization_id, investigation_id, investigation_index),
  ADD CONSTRAINT ai_proposals_investigation_labelled CHECK ((investigation_id IS NULL) = (investigation_index IS NULL));
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO STRICT c FROM pg_constraint WHERE conrelid = 'twin.ai_proposals'::regclass AND contype = 'c'
    AND pg_get_constraintdef(oid) LIKE 'CHECK ((status = ANY (ARRAY[''generating''::text, %';
  EXECUTE format('ALTER TABLE twin.ai_proposals DROP CONSTRAINT %I', c);
  SELECT conname INTO STRICT c FROM pg_constraint WHERE conrelid = 'twin.ai_proposals'::regclass AND contype = 'c'
    AND pg_get_constraintdef(oid) = 'CHECK (((status = ''approved''::text) OR ((new_version_id IS NULL) AND (scenario_id IS NULL))))';
  EXECUTE format('ALTER TABLE twin.ai_proposals DROP CONSTRAINT %I', c);
END $$;
ALTER TABLE twin.ai_proposals
  ADD CONSTRAINT ai_proposals_status CHECK (status IN ('generating', 'needs_input', 'ready', 'invalid', 'failed', 'approved', 'rejected',
    'conflicted', 'applied')),
  ADD CONSTRAINT ai_proposals_effect_when_decided CHECK (status IN ('approved', 'applied') OR (new_version_id IS NULL AND scenario_id IS NULL)),
  -- `applied` is the AI's action on its own ready proposal: the reviewed
  -- content kept, no decider, no version; a scenario in scenario mode, the
  -- draft revision it wrote in edit mode
  ADD CONSTRAINT ai_proposals_applied CHECK (status <> 'applied' OR coalesce(
    jsonb_array_length(patch) > 0 AND validation ->> 'valid' = 'true' AND risk_level IS NOT NULL AND confidence IS NOT NULL
    AND patch_hash IS NOT NULL AND decided_by IS NULL AND decided_at IS NULL AND investigation_id IS NOT NULL AND new_version_id IS NULL
    AND ((mode = 'scenario' AND scenario_id IS NOT NULL AND applied_draft_revision IS NULL)
      OR (mode = 'edit' AND scenario_id IS NULL AND applied_draft_revision IS NOT NULL)), false)),
  ADD CONSTRAINT ai_proposals_draft_revision_when_applied CHECK (applied_draft_revision IS NULL OR status = 'applied');

ALTER TABLE twin.scenarios
  ADD COLUMN investigation_id twin.inv_id DEFAULT (twin.current_investigation_id())::twin.inv_id,
  ADD CONSTRAINT scenarios_investigation_fk FOREIGN KEY (organization_id, model_id, investigation_id)
    REFERENCES twin.investigations(organization_id, model_id, id),
  -- the AI saves only a scenario from its own proposal
  ADD CONSTRAINT scenarios_investigation_from_proposal CHECK (investigation_id IS NULL OR source_proposal_id IS NOT NULL);

ALTER TABLE twin.simulation_runs
  ADD COLUMN investigation_id twin.inv_id DEFAULT (twin.current_investigation_id())::twin.inv_id,
  ADD COLUMN investigation_slot integer CONSTRAINT simulation_runs_investigation_slot CHECK (investigation_slot BETWEEN 0 AND 3),
  ADD CONSTRAINT simulation_runs_investigation_fk FOREIGN KEY (organization_id, model_id, investigation_id)
    REFERENCES twin.investigations(organization_id, model_id, id),
  ADD CONSTRAINT simulation_runs_investigation_runs UNIQUE (organization_id, investigation_id, investigation_slot),
  ADD CONSTRAINT simulation_runs_investigation_labelled CHECK ((investigation_id IS NULL) = (investigation_slot IS NULL)),
  -- a run an investigation starts: H-060's settings and D-007's limits
  -- exactly, whatever the service froze
  ADD CONSTRAINT simulation_runs_investigation_limits CHECK (investigation_id IS NULL OR coalesce(
    replications <= 10 AND config ->> 'trace_level' IN ('none', 'sampled') AND twin.duration_ms(config -> 'horizon') <= 2419200000
    AND execution_limits = '{"wall_timeout_seconds":300,"max_events":10000000,"max_active_entities":100000,"max_entity_visits":10000,"max_trace_events":100000}'::jsonb,
    false));

ALTER TABLE twin.ai_analyses
  ADD COLUMN investigation_id twin.inv_id DEFAULT (twin.current_investigation_id())::twin.inv_id,
  ADD COLUMN investigation_index integer CONSTRAINT ai_analyses_investigation_index CHECK (investigation_index BETWEEN 1 AND 4),
  ADD CONSTRAINT ai_analyses_investigation_fk FOREIGN KEY (organization_id, investigation_id) REFERENCES twin.investigations(organization_id, id),
  ADD CONSTRAINT ai_analyses_investigation_explanations UNIQUE (organization_id, investigation_id, investigation_index),
  ADD CONSTRAINT ai_analyses_investigation_labelled CHECK ((investigation_id IS NULL) = (investigation_index IS NULL));

-- The AI's edit in a draft, and in the version a person publishes from it.
ALTER TABLE twin.model_drafts
  ADD COLUMN ai_proposal_id twin.aip_id,
  ADD CONSTRAINT model_drafts_ai_proposal_fk FOREIGN KEY (organization_id, model_id, ai_proposal_id)
    REFERENCES twin.ai_proposals(organization_id, model_id, id);
ALTER TABLE twin.model_versions
  ADD COLUMN ai_edit_proposal_id twin.aip_id,
  ADD CONSTRAINT model_versions_ai_edit_fk FOREIGN KEY (organization_id, model_id, ai_edit_proposal_id)
    REFERENCES twin.ai_proposals(organization_id, model_id, id);

-- The spend ledger: planner turns are reserved on their investigation.
ALTER TABLE twin.ai_spend_reservations ADD COLUMN investigation_id twin.inv_id,
  ADD CONSTRAINT ai_spend_reservations_investigation_fk FOREIGN KEY (organization_id, investigation_id)
    REFERENCES twin.investigations(organization_id, id);
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO STRICT c FROM pg_constraint WHERE conrelid = 'twin.ai_spend_reservations'::regclass AND contype = 'c'
    AND pg_get_constraintdef(oid) LIKE 'CHECK ((role = ANY (ARRAY[%';
  EXECUTE format('ALTER TABLE twin.ai_spend_reservations DROP CONSTRAINT %I', c);
END $$;
ALTER TABLE twin.ai_spend_reservations DROP CONSTRAINT ai_spend_reservations_one_target,
  ADD CONSTRAINT ai_spend_reservations_role CHECK (role IN ('modeler', 'scenario', 'analyst', 'importer', 'investigator')),
  ADD CONSTRAINT ai_spend_reservations_one_target CHECK (num_nonnulls(ai_proposal_id, ai_analysis_id, import_id, investigation_id) = 1),
  ADD CONSTRAINT ai_spend_reservations_planner_turn CHECK ((role = 'investigator') = (investigation_id IS NOT NULL));
-- The ceiling's sums read an investigation's reservations by its target.
CREATE INDEX ai_spend_reservations_investigation_idx ON twin.ai_spend_reservations(organization_id, investigation_id)
  WHERE investigation_id IS NOT NULL;
CREATE INDEX ai_spend_reservations_proposal_idx ON twin.ai_spend_reservations(organization_id, ai_proposal_id)
  WHERE ai_proposal_id IS NOT NULL;
CREATE INDEX ai_spend_reservations_analysis_idx ON twin.ai_spend_reservations(organization_id, ai_analysis_id)
  WHERE ai_analysis_id IS NOT NULL;

-- ---------------------------------------------------------------------
-- The investigation's lifecycle (H-061 §11, §13).
--   INSERT: by the API only, for the scoped user, who is an owner or
--     editor; running with no end; at the transaction's time; never inside
--     another investigation's scope; a reused baseline is a run of the base
--     with no patch and these settings; at most 10 per organization per UTC
--     day (counted under an advisory lock of the organization).
--   UPDATE: the request never changes; running -> finished or stopped only,
--     once; for the API only stopped_by_person (by the requester or an
--     owner, as stop_requested_by = the scoped user), role_lost (only when
--     the requester holds no owner or editor membership) and time_limit
--     (only 2 minutes after the deadline, while its job is not running);
--     for the worker only its own end reasons, and only in the transaction
--     that ends the investigation's job (the job is no longer queued or
--     running), which follow_investigation_job's ai_failed is too.
-- The schema owner (operators, the SQL tests) is held to the lifecycle,
-- not to the per-role rules.
CREATE FUNCTION twin.protect_investigation() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  owner boolean := pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE');
  api boolean := pg_catalog.pg_has_role(current_user, 'twin_api', 'USAGE');
  worker boolean := pg_catalog.pg_has_role(current_user, 'twin_worker', 'USAGE');
  acting text := nullif(current_setting('app.investigation_id', true), '');
  usr text := nullif(current_setting('app.user_id', true), '');
  job text;
  today integer;
BEGIN
  IF NOT (owner OR api OR worker) THEN
    RAISE EXCEPTION 'only the API, the worker and the schema owner write investigations' USING ERRCODE = '55000';
  END IF;
  IF acting IS NOT NULL THEN
    RAISE EXCEPTION 'an investigation''s acting-for scope never writes an investigation' USING ERRCODE = '42501';
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NOT owner AND (worker OR NOT api) THEN
      RAISE EXCEPTION 'only the API starts an investigation' USING ERRCODE = '42501';
    END IF;
    IF NEW.status <> 'running' OR num_nonnulls(NEW.reason, NEW.ai_outcome, NEW.stop_requested_by, NEW.finished_at) <> 0 THEN
      RAISE EXCEPTION 'an investigation starts running, with no end' USING ERRCODE = '55000';
    END IF;
    IF NOT owner THEN
      IF NEW.created_by::text IS DISTINCT FROM usr THEN
        RAISE EXCEPTION 'the API starts an investigation for the scoped user' USING ERRCODE = '42501';
      END IF;
      IF NEW.created_at IS DISTINCT FROM now() THEN
        RAISE EXCEPTION 'an investigation starts at the time of its transaction' USING ERRCODE = '55000';
      END IF;
      IF NOT EXISTS (SELECT 1 FROM twin.memberships m WHERE m.organization_id = NEW.organization_id AND m.user_id = NEW.created_by
                     AND m.role IN ('owner', 'editor')) THEN
        RAISE EXCEPTION 'only an owner or an editor starts an investigation' USING ERRCODE = '42501';
      END IF;
    END IF;
    IF NEW.baseline_run_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM twin.simulation_runs r
         WHERE r.organization_id = NEW.organization_id AND r.id = NEW.baseline_run_id AND r.model_version_id = NEW.base_version_id
           AND r.scenario_id IS NULL AND r.scenario_patch = '[]'::jsonb AND r.config_hash::text = NEW.run_config_hash::text) THEN
      RAISE EXCEPTION 'a reused baseline is a run of the base with no patch and the investigation''s run settings' USING ERRCODE = '23514';
    END IF;
    IF NEW.source_analysis_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM twin.ai_analyses a
         WHERE a.organization_id = NEW.organization_id AND a.id = NEW.source_analysis_id AND a.status = 'completed'
           AND a.baseline_run_id::text = NEW.baseline_run_id::text) THEN
      RAISE EXCEPTION 'an investigation from an analysis starts from a completed analysis against the baseline it reuses'
        USING ERRCODE = '23514';
    END IF;
    PERFORM pg_catalog.pg_advisory_xact_lock(61019, pg_catalog.hashtext(NEW.organization_id::text));
    SELECT count(*) INTO today FROM twin.investigations i
      WHERE i.organization_id = NEW.organization_id
        AND i.created_at >= (date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC');
    IF today >= 10 THEN
      RAISE EXCEPTION 'an organization starts at most 10 investigations a UTC day' USING ERRCODE = '23514',
        CONSTRAINT = 'investigations_daily_limit';
    END IF;
    RETURN NEW;
  END IF;
  IF ROW(NEW.id, NEW.organization_id, NEW.model_id, NEW.base_version_id, NEW.mode, NEW.source, NEW.source_analysis_id, NEW.intent,
         NEW.context, NEW.output_locale, NEW.research, NEW.proposal_model, NEW.planner_model, NEW.prompt_template_version,
         NEW.objective, NEW.run_config, NEW.run_config_hash, NEW.baseline_name, NEW.baseline_run_id, NEW.caps, NEW.deadline_at,
         NEW.created_by, NEW.created_at)
     IS DISTINCT FROM
     ROW(OLD.id, OLD.organization_id, OLD.model_id, OLD.base_version_id, OLD.mode, OLD.source, OLD.source_analysis_id, OLD.intent,
         OLD.context, OLD.output_locale, OLD.research, OLD.proposal_model, OLD.planner_model, OLD.prompt_template_version,
         OLD.objective, OLD.run_config, OLD.run_config_hash, OLD.baseline_name, OLD.baseline_run_id, OLD.caps, OLD.deadline_at,
         OLD.created_by, OLD.created_at) THEN
    RAISE EXCEPTION 'an investigation''s request never changes' USING ERRCODE = '55000';
  END IF;
  IF NEW IS NOT DISTINCT FROM OLD THEN
    RETURN NEW;
  END IF;
  IF OLD.status <> 'running' THEN
    RAISE EXCEPTION 'an ended investigation is final' USING ERRCODE = '55000';
  END IF;
  IF NEW.status = 'running' THEN
    RAISE EXCEPTION 'a running investigation changes only by ending' USING ERRCODE = '55000';
  END IF;
  NEW.finished_at := now();
  IF owner THEN
    RETURN NEW;
  END IF;
  IF worker AND NOT api THEN
    IF NEW.reason NOT IN ('finished_by_ai', 'draft_written', 'limit_reached', 'time_limit', 'spend_cap', 'invalid_answer', 'ai_failed') THEN
      RAISE EXCEPTION 'the worker ends an investigation only for its own reasons' USING ERRCODE = '42501';
    END IF;
    SELECT j.status INTO job FROM twin.jobs j WHERE j.organization_id = NEW.organization_id AND j.investigation_id = NEW.id;
    IF job IS NULL OR job IN ('queued', 'running') THEN
      RAISE EXCEPTION 'the worker ends an investigation only in the transaction that ends its job' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
  END IF;
  IF NOT api OR worker THEN
    RAISE EXCEPTION 'only the API and the worker end investigations' USING ERRCODE = '42501';
  END IF;
  IF NEW.ai_outcome IS NOT NULL THEN
    RAISE EXCEPTION 'only the worker records the AI''s outcome' USING ERRCODE = '42501';
  END IF;
  IF NEW.reason = 'stopped_by_person' THEN
    IF NEW.stop_requested_by::text IS DISTINCT FROM usr OR NOT (usr = OLD.created_by::text OR EXISTS (SELECT 1 FROM twin.memberships m
         WHERE m.organization_id = NEW.organization_id AND m.user_id::text = usr AND m.role = 'owner')) THEN
      RAISE EXCEPTION 'a person stops an investigation: its requester or an owner, in their own name' USING ERRCODE = '42501';
    END IF;
  ELSIF NEW.reason = 'role_lost' THEN
    IF EXISTS (SELECT 1 FROM twin.memberships m WHERE m.organization_id = NEW.organization_id AND m.user_id = OLD.created_by
               AND m.role IN ('owner', 'editor')) THEN
      RAISE EXCEPTION 'an investigation ends with role_lost only once its requester holds no owner or editor membership' USING ERRCODE = '55000';
    END IF;
  ELSIF NEW.reason = 'time_limit' THEN
    IF now() <= OLD.deadline_at + interval '2 minutes' OR EXISTS (SELECT 1 FROM twin.jobs j
         WHERE j.organization_id = NEW.organization_id AND j.investigation_id = NEW.id AND j.status = 'running') THEN
      RAISE EXCEPTION 'the API ends an investigation at its time limit only 2 minutes after its deadline, while no worker runs it'
        USING ERRCODE = '55000';
    END IF;
  ELSE
    RAISE EXCEPTION 'the API ends an investigation only when a person stops it, its requester lost the role, or its time ran out'
      USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_investigation() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER protect_investigation BEFORE INSERT OR UPDATE ON twin.investigations
  FOR EACH ROW EXECUTE FUNCTION twin.protect_investigation();

-- An investigation is created with its one job, in one transaction, for
-- every role (createInvestigation: the row, its job and its audit event).
CREATE FUNCTION twin.check_investigation_has_its_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM twin.jobs j WHERE j.organization_id = NEW.organization_id AND j.investigation_id = NEW.id
                 AND j.type = 'investigation') THEN
    RAISE EXCEPTION 'an investigation is created with its job' USING ERRCODE = '23514';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_investigation_has_its_job() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER investigation_created_with_its_job AFTER INSERT ON twin.investigations
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION twin.check_investigation_has_its_job();

-- A job that fails while its investigation runs stops it (ai_failed), in
-- the same statement: the worker loop's final failure, the reaper's after
-- the last attempt, an operator's. Runs as whoever ends the job (the
-- worker's column UPDATE covers status, reason and finished_at).
CREATE FUNCTION twin.follow_investigation_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  UPDATE twin.investigations SET status = 'stopped', reason = 'ai_failed'
    WHERE organization_id = NEW.organization_id AND id = NEW.investigation_id AND status = 'running';
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.follow_investigation_job() FROM PUBLIC;
CREATE TRIGGER follow_investigation_job AFTER UPDATE OF status ON twin.jobs
  FOR EACH ROW WHEN (NEW.type = 'investigation' AND NEW.status = 'failed' AND NEW.status IS DISTINCT FROM OLD.status)
  EXECUTE FUNCTION twin.follow_investigation_job();

-- An investigation's job completes, or is cancelled, only together with
-- its investigation's end (the worker's Finish; the API's stop of a queued
-- job), for every role.
CREATE FUNCTION twin.check_investigation_job_end() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM twin.investigations i WHERE i.organization_id = NEW.organization_id AND i.id = NEW.investigation_id
             AND i.status = 'running') THEN
    RAISE EXCEPTION 'an investigation''s job completes or is cancelled only with its investigation''s end' USING ERRCODE = '55000';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_investigation_job_end() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER investigation_job_completes_when_ended AFTER UPDATE OF status ON twin.jobs
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW WHEN (NEW.type = 'investigation' AND NEW.status IN ('completed', 'cancelled') AND NEW.status IS DISTINCT FROM OLD.status)
  EXECUTE FUNCTION twin.check_investigation_job_end();

-- An AI investigation's job: only the worker's job types may be queued
-- inside an acting-for transaction (proposals, runs, explanations).
CREATE FUNCTION twin.guard_investigation_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF nullif(current_setting('app.investigation_id', true), '') IS NOT NULL
     AND NEW.type NOT IN ('model_proposal', 'simulation', 'analysis') THEN
    RAISE EXCEPTION 'an AI investigation queues only proposals, runs and explanations' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.guard_investigation_job() FROM PUBLIC;
CREATE TRIGGER guard_investigation_job BEFORE INSERT ON twin.jobs
  FOR EACH ROW EXECUTE FUNCTION twin.guard_investigation_job();

-- ---------------------------------------------------------------------
-- The ledger (H-061 §5.5, §5.6, §6.2, §11).
--   INSERT (the worker): while the investigation runs, before its
--     deadline (read FOR SHARE, so a stop that commits first is waited
--     for, then refuses it); under the current lease of the
--     investigation's own job; seq = the last + 1, once every earlier call
--     is carried out and observed, and never after finish; a tool of the
--     investigation's mode; recorded pending, except invalid model output
--     (invalid_call, refused at once) and finish (done, the last row).
--   UPDATE (the API): pending -> done, refused or failed, once, in the
--     investigation's acting-for transaction for its requester, while it
--     runs; what it made is the investigation's own.
--   UPDATE (the worker): what the AI was shown, once, on a carried-out
--     call, while the investigation's job holds an unexpired lease.
-- Everything else of a call never changes. The schema owner is held to
-- the sequence and the lifecycle, not to the per-role rules.
CREATE FUNCTION twin.protect_investigation_tool_call() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  owner boolean := pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE');
  api boolean := pg_catalog.pg_has_role(current_user, 'twin_api', 'USAGE');
  worker boolean := pg_catalog.pg_has_role(current_user, 'twin_worker', 'USAGE');
  acting text := nullif(current_setting('app.investigation_id', true), '');
  usr text := nullif(current_setting('app.user_id', true), '');
  inv record;
  prior record;
BEGIN
  IF NOT (owner OR api OR worker) THEN
    RAISE EXCEPTION 'only the API, the worker and the schema owner write the ledger' USING ERRCODE = '55000';
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NOT owner AND (api OR NOT worker) THEN
      RAISE EXCEPTION 'only the worker records the AI''s calls' USING ERRCODE = '42501';
    END IF;
    SELECT i.status, i.mode, i.deadline_at INTO inv FROM twin.investigations i
      WHERE i.organization_id = NEW.organization_id AND i.id = NEW.investigation_id FOR SHARE;
    IF NOT FOUND THEN
      RETURN NEW; -- the foreign key refuses it
    END IF;
    IF inv.status <> 'running' OR now() >= inv.deadline_at THEN
      RAISE EXCEPTION 'a call is recorded only while its investigation runs, before its deadline' USING ERRCODE = '55000';
    END IF;
    IF NEW.tool <> 'invalid_call' AND NEW.tool <> ALL (CASE inv.mode
         WHEN 'scenario' THEN ARRAY['propose_what_if', 'save_scenario', 'run_what_if', 'explain_results', 'finish']
         ELSE ARRAY['propose_model_edit', 'write_to_draft', 'finish'] END) THEN
      RAISE EXCEPTION 'a call is one of its investigation''s mode''s tools' USING ERRCODE = '23514';
    END IF;
    IF NEW.status IS DISTINCT FROM (CASE NEW.tool WHEN 'invalid_call' THEN 'refused' WHEN 'finish' THEN 'done' ELSE 'pending' END)
       OR NEW.observation IS NOT NULL OR NEW.observation_hash IS NOT NULL OR NEW.observed_at IS NOT NULL
       OR num_nonnulls(NEW.proposal_id, NEW.scenario_id, NEW.baseline_run_id, NEW.run_id, NEW.analysis_id, NEW.draft_revision) <> 0 THEN
      RAISE EXCEPTION 'a call is recorded before anything is carried out (invalid output refused, finish done)' USING ERRCODE = '55000';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM twin.jobs j WHERE j.organization_id = NEW.organization_id AND j.id = NEW.job_id
                   AND j.investigation_id = NEW.investigation_id
                   AND (owner OR (j.status = 'running' AND j.lease_token = NEW.lease_token AND j.lease_expires_at > now()))) THEN
      RAISE EXCEPTION 'a call is recorded under the current lease of its investigation''s job' USING ERRCODE = '55000';
    END IF;
    SELECT coalesce(max(c.seq), 0) AS last,
           coalesce(bool_and(c.status <> 'pending' AND c.observed_at IS NOT NULL), true) AS settled,
           coalesce(bool_or(c.tool = 'finish'), false) AS finished
      INTO prior FROM twin.investigation_tool_calls c
      WHERE c.organization_id = NEW.organization_id AND c.investigation_id = NEW.investigation_id;
    IF NEW.seq <> prior.last + 1 OR prior.finished THEN
      RAISE EXCEPTION 'calls are recorded one after the other, and none after finish' USING ERRCODE = '55000';
    END IF;
    IF NOT prior.settled THEN
      RAISE EXCEPTION 'a call is recorded once every earlier call was carried out and observed' USING ERRCODE = '55000';
    END IF;
    NEW.requested_at := now();
    NEW.resolved_at := CASE WHEN NEW.status = 'pending' THEN NULL ELSE now() END;
    RETURN NEW;
  END IF;
  IF ROW(NEW.organization_id, NEW.investigation_id, NEW.seq, NEW.tool, NEW.arguments, NEW.arguments_hash, NEW.note, NEW.job_id,
         NEW.lease_token, NEW.requested_at)
     IS DISTINCT FROM
     ROW(OLD.organization_id, OLD.investigation_id, OLD.seq, OLD.tool, OLD.arguments, OLD.arguments_hash, OLD.note, OLD.job_id,
         OLD.lease_token, OLD.requested_at) THEN
    RAISE EXCEPTION 'a call''s request never changes' USING ERRCODE = '55000';
  END IF;
  IF NEW IS NOT DISTINCT FROM OLD THEN
    RETURN NEW;
  END IF;
  IF OLD.status = 'pending' THEN
    IF NEW.status = 'pending' OR ROW(NEW.observation, NEW.observation_hash, NEW.observed_at) IS DISTINCT FROM ROW(OLD.observation,
         OLD.observation_hash, OLD.observed_at) THEN
      RAISE EXCEPTION 'a pending call changes only by being carried out' USING ERRCODE = '55000';
    END IF;
    IF NOT owner THEN
      IF NOT api OR worker THEN
        RAISE EXCEPTION 'only the API carries out a call' USING ERRCODE = '42501';
      END IF;
      IF acting IS DISTINCT FROM NEW.investigation_id::text OR usr IS DISTINCT FROM (SELECT i.created_by::text FROM twin.investigations i
           WHERE i.organization_id = NEW.organization_id AND i.id = NEW.investigation_id) THEN
        RAISE EXCEPTION 'a call is carried out only in its investigation''s acting-for scope, for its requester' USING ERRCODE = '42501';
      END IF;
    END IF;
    SELECT i.status, i.baseline_run_id INTO inv FROM twin.investigations i
      WHERE i.organization_id = NEW.organization_id AND i.id = NEW.investigation_id FOR SHARE;
    IF inv.status <> 'running' THEN
      RAISE EXCEPTION 'a call is carried out only while its investigation runs' USING ERRCODE = '55000';
    END IF;
    IF (NEW.proposal_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM twin.ai_proposals p
          WHERE p.organization_id = NEW.organization_id AND p.id = NEW.proposal_id AND p.investigation_id = NEW.investigation_id))
       OR (NEW.scenario_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM twin.scenarios s
          WHERE s.organization_id = NEW.organization_id AND s.id = NEW.scenario_id AND s.investigation_id = NEW.investigation_id))
       OR (NEW.run_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM twin.simulation_runs r
          WHERE r.organization_id = NEW.organization_id AND r.id = NEW.run_id AND r.investigation_id = NEW.investigation_id
            AND r.investigation_slot >= 1))
       OR (NEW.baseline_run_id IS NOT NULL AND NEW.baseline_run_id::text IS DISTINCT FROM inv.baseline_run_id::text
           AND NOT EXISTS (SELECT 1 FROM twin.simulation_runs r WHERE r.organization_id = NEW.organization_id AND r.id = NEW.baseline_run_id
             AND r.investigation_id = NEW.investigation_id AND r.investigation_slot = 0))
       OR (NEW.analysis_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM twin.ai_analyses a
          WHERE a.organization_id = NEW.organization_id AND a.id = NEW.analysis_id AND a.investigation_id = NEW.investigation_id)) THEN
      RAISE EXCEPTION 'a carried-out call names only what its investigation made' USING ERRCODE = '23514';
    END IF;
    NEW.resolved_at := now();
    RETURN NEW;
  END IF;
  IF ROW(NEW.status, NEW.proposal_id, NEW.scenario_id, NEW.baseline_run_id, NEW.run_id, NEW.analysis_id, NEW.draft_revision, NEW.error,
         NEW.resolved_at)
     IS DISTINCT FROM
     ROW(OLD.status, OLD.proposal_id, OLD.scenario_id, OLD.baseline_run_id, OLD.run_id, OLD.analysis_id, OLD.draft_revision, OLD.error,
         OLD.resolved_at) THEN
    RAISE EXCEPTION 'a carried-out call never changes' USING ERRCODE = '55000';
  END IF;
  IF OLD.observed_at IS NOT NULL THEN
    RAISE EXCEPTION 'what the AI was shown is written once' USING ERRCODE = '55000';
  END IF;
  IF NOT owner THEN
    IF NOT worker OR api THEN
      RAISE EXCEPTION 'only the worker records what the AI was shown' USING ERRCODE = '42501';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM twin.jobs j WHERE j.organization_id = NEW.organization_id AND j.investigation_id = NEW.investigation_id
                   AND j.status = 'running' AND j.lease_expires_at > now()) THEN
      RAISE EXCEPTION 'what the AI was shown is recorded under its investigation''s job''s lease' USING ERRCODE = '55000';
    END IF;
  END IF;
  NEW.observed_at := CASE WHEN NEW.observation IS NULL THEN NULL ELSE now() END;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_investigation_tool_call() FROM PUBLIC;
CREATE TRIGGER protect_investigation_tool_call BEFORE INSERT OR UPDATE ON twin.investigation_tool_calls
  FOR EACH ROW EXECUTE FUNCTION twin.protect_investigation_tool_call();

-- finish is the AI's last call: its row commits only with its
-- investigation's end (the worker's Finish).
CREATE FUNCTION twin.check_finish_ends_its_investigation() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM twin.investigations i WHERE i.organization_id = NEW.organization_id AND i.id = NEW.investigation_id
             AND i.status = 'running') THEN
    RAISE EXCEPTION 'a finish call commits only with its investigation''s end' USING ERRCODE = '55000';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_finish_ends_its_investigation() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER finish_ends_its_investigation AFTER INSERT ON twin.investigation_tool_calls
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.tool = 'finish')
  EXECUTE FUNCTION twin.check_finish_ends_its_investigation();

-- ---------------------------------------------------------------------
-- The acting-for guard (H-061 §6.2 step 7, §11): a proposal, scenario, run
-- or analysis is labelled with an investigation exactly when it is written
-- in that investigation's acting-for transaction (app.investigation_id),
-- both ways, for every role; then the investigation runs (read FOR SHARE),
-- before its deadline, and the row is its requester's, written for them.
-- Each guard then binds the row to the investigation and assigns the index
-- or slot its cap counts (a unique index refuses a race; a number past the
-- cap is refused here, by the cap's name).
CREATE FUNCTION twin.label_investigation_proposal() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  acting text := nullif(current_setting('app.investigation_id', true), '');
  inv record;
  n integer;
BEGIN
  IF NEW.investigation_id::text IS DISTINCT FROM acting THEN
    RAISE EXCEPTION 'a row is labelled with an investigation exactly when it is written in that investigation''s scope' USING ERRCODE = '42501';
  END IF;
  IF acting IS NULL THEN
    IF NEW.investigation_index IS NOT NULL THEN
      RAISE EXCEPTION 'only an investigation''s proposal has an index' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
  END IF;
  SELECT * INTO inv FROM twin.investigations i WHERE i.organization_id = NEW.organization_id AND i.id = NEW.investigation_id FOR SHARE;
  IF NOT FOUND OR inv.status <> 'running' OR now() >= inv.deadline_at THEN
    RAISE EXCEPTION 'an investigation writes only while it runs, before its deadline' USING ERRCODE = '55000';
  END IF;
  IF NEW.created_by::text IS DISTINCT FROM inv.created_by::text
     OR nullif(current_setting('app.user_id', true), '') IS DISTINCT FROM inv.created_by::text THEN
    RAISE EXCEPTION 'an investigation writes only for the person who started it' USING ERRCODE = '42501';
  END IF;
  IF NEW.model_id::text <> inv.model_id::text OR NEW.base_version_id::text <> inv.base_version_id::text OR NEW.mode <> inv.mode
     OR NEW.source_import_id IS NOT NULL THEN
    RAISE EXCEPTION 'an investigation''s proposal is on its base, in its mode' USING ERRCODE = '23514';
  END IF;
  SELECT coalesce(max(p.investigation_index), 0) + 1 INTO n FROM twin.ai_proposals p
    WHERE p.organization_id = NEW.organization_id AND p.investigation_id = NEW.investigation_id;
  IF n > (inv.caps ->> 'what_ifs')::integer THEN
    RAISE EXCEPTION 'the investigation has used its what-ifs' USING ERRCODE = '23514', CONSTRAINT = 'ai_proposals_investigation_cap';
  END IF;
  NEW.investigation_index := n;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.label_investigation_proposal() FROM PUBLIC;
CREATE TRIGGER label_investigation BEFORE INSERT ON twin.ai_proposals
  FOR EACH ROW EXECUTE FUNCTION twin.label_investigation_proposal();

CREATE FUNCTION twin.label_investigation_scenario() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  acting text := nullif(current_setting('app.investigation_id', true), '');
  inv record;
BEGIN
  IF NEW.investigation_id::text IS DISTINCT FROM acting THEN
    RAISE EXCEPTION 'a row is labelled with an investigation exactly when it is written in that investigation''s scope' USING ERRCODE = '42501';
  END IF;
  IF acting IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT * INTO inv FROM twin.investigations i WHERE i.organization_id = NEW.organization_id AND i.id = NEW.investigation_id FOR SHARE;
  IF NOT FOUND OR inv.status <> 'running' OR now() >= inv.deadline_at THEN
    RAISE EXCEPTION 'an investigation writes only while it runs, before its deadline' USING ERRCODE = '55000';
  END IF;
  IF NEW.created_by::text IS DISTINCT FROM inv.created_by::text
     OR nullif(current_setting('app.user_id', true), '') IS DISTINCT FROM inv.created_by::text THEN
    RAISE EXCEPTION 'an investigation writes only for the person who started it' USING ERRCODE = '42501';
  END IF;
  IF inv.mode <> 'scenario' OR NEW.base_version_id::text <> inv.base_version_id::text OR NOT EXISTS (SELECT 1 FROM twin.ai_proposals p
       WHERE p.organization_id = NEW.organization_id AND p.id = NEW.source_proposal_id AND p.investigation_id = NEW.investigation_id) THEN
    RAISE EXCEPTION 'an investigation saves a scenario only from its own what-if, on its base' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.label_investigation_scenario() FROM PUBLIC;
CREATE TRIGGER label_investigation BEFORE INSERT ON twin.scenarios
  FOR EACH ROW EXECUTE FUNCTION twin.label_investigation_scenario();

CREATE FUNCTION twin.label_investigation_run() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  acting text := nullif(current_setting('app.investigation_id', true), '');
  inv record;
  slot integer;
BEGIN
  IF NEW.investigation_id::text IS DISTINCT FROM acting THEN
    RAISE EXCEPTION 'a row is labelled with an investigation exactly when it is written in that investigation''s scope' USING ERRCODE = '42501';
  END IF;
  IF acting IS NULL THEN
    IF NEW.investigation_slot IS NOT NULL THEN
      RAISE EXCEPTION 'only an investigation''s run has a slot' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
  END IF;
  SELECT * INTO inv FROM twin.investigations i WHERE i.organization_id = NEW.organization_id AND i.id = NEW.investigation_id FOR SHARE;
  IF NOT FOUND OR inv.status <> 'running' OR now() >= inv.deadline_at THEN
    RAISE EXCEPTION 'an investigation writes only while it runs, before its deadline' USING ERRCODE = '55000';
  END IF;
  IF NEW.created_by::text IS DISTINCT FROM inv.created_by::text
     OR nullif(current_setting('app.user_id', true), '') IS DISTINCT FROM inv.created_by::text THEN
    RAISE EXCEPTION 'an investigation writes only for the person who started it' USING ERRCODE = '42501';
  END IF;
  IF inv.mode <> 'scenario' THEN
    RAISE EXCEPTION 'an edit investigation starts no runs' USING ERRCODE = '23514';
  END IF;
  IF NEW.model_version_id::text <> inv.base_version_id::text OR NEW.config_hash::text <> inv.run_config_hash::text THEN
    RAISE EXCEPTION 'an investigation runs its base with its frozen run settings' USING ERRCODE = '23514';
  END IF;
  IF NEW.scenario_id IS NULL THEN
    -- slot 0: the baseline, once, unless a person's baseline is reused
    IF NEW.scenario_patch <> '[]'::jsonb OR inv.baseline_run_id IS NOT NULL THEN
      RAISE EXCEPTION 'an investigation''s baseline is the base with no patch, only when it reuses none' USING ERRCODE = '23514';
    END IF;
    slot := 0;
  ELSE
    -- slot k: the scenario the investigation saved from its what-if k
    SELECT p.investigation_index INTO slot FROM twin.scenarios s
      JOIN twin.ai_proposals p ON p.organization_id = s.organization_id AND p.id = s.source_proposal_id
      WHERE s.organization_id = NEW.organization_id AND s.id = NEW.scenario_id
        AND s.investigation_id = NEW.investigation_id AND p.investigation_id = NEW.investigation_id;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'an investigation runs only the scenarios it saved' USING ERRCODE = '23514';
    END IF;
  END IF;
  NEW.investigation_slot := slot;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.label_investigation_run() FROM PUBLIC;
CREATE TRIGGER label_investigation BEFORE INSERT ON twin.simulation_runs
  FOR EACH ROW EXECUTE FUNCTION twin.label_investigation_run();

CREATE FUNCTION twin.label_investigation_analysis() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  acting text := nullif(current_setting('app.investigation_id', true), '');
  inv record;
  n integer;
BEGIN
  IF NEW.investigation_id::text IS DISTINCT FROM acting THEN
    RAISE EXCEPTION 'a row is labelled with an investigation exactly when it is written in that investigation''s scope' USING ERRCODE = '42501';
  END IF;
  IF acting IS NULL THEN
    IF NEW.investigation_index IS NOT NULL THEN
      RAISE EXCEPTION 'only an investigation''s explanation has an index' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
  END IF;
  SELECT * INTO inv FROM twin.investigations i WHERE i.organization_id = NEW.organization_id AND i.id = NEW.investigation_id FOR SHARE;
  IF NOT FOUND OR inv.status <> 'running' OR now() >= inv.deadline_at THEN
    RAISE EXCEPTION 'an investigation writes only while it runs, before its deadline' USING ERRCODE = '55000';
  END IF;
  IF NEW.created_by::text IS DISTINCT FROM inv.created_by::text
     OR nullif(current_setting('app.user_id', true), '') IS DISTINCT FROM inv.created_by::text THEN
    RAISE EXCEPTION 'an investigation writes only for the person who started it' USING ERRCODE = '42501';
  END IF;
  -- a what-if run of the investigation against its baseline (slot 0, or
  -- the person's baseline it reuses)
  IF NOT EXISTS (SELECT 1 FROM twin.simulation_runs r WHERE r.organization_id = NEW.organization_id AND r.id = NEW.run_id
                 AND r.investigation_id = NEW.investigation_id AND r.investigation_slot >= 1)
     OR NOT (NEW.baseline_run_id::text IS NOT DISTINCT FROM inv.baseline_run_id::text AND inv.baseline_run_id IS NOT NULL
             OR EXISTS (SELECT 1 FROM twin.simulation_runs r WHERE r.organization_id = NEW.organization_id AND r.id = NEW.baseline_run_id
                        AND r.investigation_id = NEW.investigation_id AND r.investigation_slot = 0)) THEN
    RAISE EXCEPTION 'an investigation explains only its what-if runs against its baseline' USING ERRCODE = '23514';
  END IF;
  SELECT coalesce(max(a.investigation_index), 0) + 1 INTO n FROM twin.ai_analyses a
    WHERE a.organization_id = NEW.organization_id AND a.investigation_id = NEW.investigation_id;
  IF n > (inv.caps ->> 'explanations')::integer THEN
    RAISE EXCEPTION 'the investigation has used its explanations' USING ERRCODE = '23514', CONSTRAINT = 'ai_analyses_investigation_cap';
  END IF;
  NEW.investigation_index := n;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.label_investigation_analysis() FROM PUBLIC;
CREATE TRIGGER label_investigation BEFORE INSERT ON twin.ai_analyses
  FOR EACH ROW EXECUTE FUNCTION twin.label_investigation_analysis();

-- A label is written once, with its row (scenarios and versions never
-- change at all, 0001).
CREATE FUNCTION twin.protect_investigation_label() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF TG_TABLE_NAME = 'simulation_runs' THEN
    IF ROW(NEW.investigation_id, NEW.investigation_slot) IS DISTINCT FROM ROW(OLD.investigation_id, OLD.investigation_slot) THEN
      RAISE EXCEPTION 'a run''s investigation label never changes' USING ERRCODE = '55000';
    END IF;
  ELSIF ROW(NEW.investigation_id, NEW.investigation_index) IS DISTINCT FROM ROW(OLD.investigation_id, OLD.investigation_index) THEN
    RAISE EXCEPTION 'an analysis''s investigation label never changes' USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_investigation_label() FROM PUBLIC;
CREATE TRIGGER protect_investigation_label BEFORE UPDATE ON twin.simulation_runs
  FOR EACH ROW EXECUTE FUNCTION twin.protect_investigation_label();
CREATE TRIGGER protect_investigation_label BEFORE UPDATE ON twin.ai_analyses
  FOR EACH ROW EXECUTE FUNCTION twin.protect_investigation_label();

-- ---------------------------------------------------------------------
-- 0001's protect_proposal, plus one transition: ready -> applied, the AI's
-- action on its own ready proposal, only in its investigation's acting-for
-- transaction for its requester, with its effect already written (the
-- scenario it saved, or the draft revision it wrote). applied is final, so
-- it is never approved or rejected; a person may still decide a ready one.
-- The investigation label is part of a proposal's identity.
CREATE OR REPLACE FUNCTION twin.protect_proposal_content() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF ROW(NEW.organization_id,NEW.model_id,NEW.base_version_id,NEW.mode,NEW.source_import_id,NEW.intent,NEW.investigation_id,NEW.investigation_index)
     IS DISTINCT FROM ROW(OLD.organization_id,OLD.model_id,OLD.base_version_id,OLD.mode,OLD.source_import_id,OLD.intent,OLD.investigation_id,OLD.investigation_index) THEN
    RAISE EXCEPTION 'proposal identity/base is immutable' USING ERRCODE='55000';
  END IF;
  -- A decided, applied or finished proposal never changes (API_DATABASE §8).
  IF OLD.status IN ('approved','rejected','conflicted','invalid','failed','applied') AND NEW IS DISTINCT FROM OLD THEN
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
       (OLD.status='ready' AND NEW.status IN ('approved','rejected','conflicted','applied')) OR
       (OLD.status='needs_input' AND NEW.status='rejected')) THEN
    RAISE EXCEPTION 'invalid proposal transition % -> %',OLD.status,NEW.status USING ERRCODE='55000';
  END IF;
  IF NEW.status='applied' AND NEW.status<>OLD.status THEN
    IF NEW.investigation_id IS NULL
       OR NEW.investigation_id::text IS DISTINCT FROM nullif(current_setting('app.investigation_id',true),'')
       OR NEW.created_by::text IS DISTINCT FROM nullif(current_setting('app.user_id',true),'') THEN
      RAISE EXCEPTION 'only its own investigation applies an AI proposal, for the person who started it' USING ERRCODE='42501';
    END IF;
    IF NOT coalesce((NEW.mode='scenario' AND EXISTS (SELECT 1 FROM twin.scenarios s
            WHERE s.organization_id=NEW.organization_id AND s.id=NEW.scenario_id AND s.source_proposal_id=NEW.id
              AND s.investigation_id=NEW.investigation_id)) OR
         (NEW.mode='edit' AND EXISTS (SELECT 1 FROM twin.model_drafts d
            WHERE d.organization_id=NEW.organization_id AND d.model_id=NEW.model_id AND d.ai_proposal_id=NEW.id
              AND d.revision=NEW.applied_draft_revision)), false) THEN
      RAISE EXCEPTION 'an applied proposal names the scenario or the draft revision its investigation wrote from it' USING ERRCODE='23514';
    END IF;
  -- The decision record is written by the decision itself, for the scoped
  -- user, at the database's time; an approval names the effect created from
  -- this very proposal.
  ELSIF NEW.status IN ('approved','rejected','conflicted') AND NEW.status<>OLD.status THEN
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
    IF NEW.applied_draft_revision IS NOT NULL THEN
      RAISE EXCEPTION 'the decision record is written only by the decision' USING ERRCODE='55000';
    END IF;
  ELSIF ROW(NEW.decided_by,NEW.decided_at,NEW.new_version_id,NEW.scenario_id,NEW.applied_draft_revision)
        IS DISTINCT FROM ROW(OLD.decided_by,OLD.decided_at,OLD.new_version_id,OLD.scenario_id,OLD.applied_draft_revision) THEN
    RAISE EXCEPTION 'the decision record is written only by the decision' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END $$;

-- 0013's check, with the AI's effect: a scenario that names a source
-- proposal is that proposal's one effect, approved by a person (the
-- scenario then carries no investigation label, whoever's proposal it
-- was: TEST-008) or applied by its own investigation (the scenario then
-- carries that investigation's label).
CREATE OR REPLACE FUNCTION twin.check_proposal_effect() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  p record;
  base_number integer;
BEGIN
  SELECT status, mode, base_version_id, new_version_id, scenario_id, patch, patch_hash, investigation_id INTO p FROM twin.ai_proposals
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
    IF NOT coalesce(((p.status = 'approved' AND NEW.investigation_id IS NULL)
                     OR (p.status = 'applied' AND NEW.investigation_id IS NOT NULL AND NEW.investigation_id::text = p.investigation_id::text))
                    AND p.mode = 'scenario' AND p.scenario_id::text = NEW.id::text
                    AND NEW.base_version_id::text = p.base_version_id::text AND NEW.patch = p.patch
                    AND NEW.patch_hash::text = p.patch_hash::text, false) THEN
      RAISE EXCEPTION 'a scenario from a proposal is the approved or applied scenario proposal''s one effect: its base, patch and patch hash'
        USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NULL;
END $$;

-- An AI proposal is applied only by its own investigation: while
-- app.investigation_id is set, a proposal changes only from ready to
-- applied, and only one of that investigation (the AI never decides).
CREATE FUNCTION twin.guard_investigation_proposal() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE acting text := nullif(current_setting('app.investigation_id', true), '');
BEGIN
  IF acting IS NOT NULL AND NOT (OLD.status = 'ready' AND NEW.status = 'applied' AND OLD.investigation_id::text = acting) THEN
    RAISE EXCEPTION 'an AI investigation changes a proposal only by applying its own ready one' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.guard_investigation_proposal() FROM PUBLIC;
CREATE TRIGGER guard_investigation_proposal BEFORE UPDATE ON twin.ai_proposals
  FOR EACH ROW EXECUTE FUNCTION twin.guard_investigation_proposal();

-- The AI's edit in a draft: only its own investigation names it (while the
-- scope is set, a draft changes only by gaining its edit proposal); a
-- person's write keeps it or clears it, never sets one.
CREATE FUNCTION twin.guard_draft_ai_edit() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE acting text := nullif(current_setting('app.investigation_id', true), '');
BEGIN
  IF acting IS NULL THEN
    IF NOT pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE') AND NEW.ai_proposal_id IS NOT NULL
       AND (TG_OP = 'INSERT' OR NEW.ai_proposal_id IS DISTINCT FROM OLD.ai_proposal_id) THEN
      RAISE EXCEPTION 'only an AI investigation writes its edit into a draft' USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
  END IF;
  IF TG_OP <> 'UPDATE' OR NEW.ai_proposal_id IS NULL OR NEW.ai_proposal_id IS NOT DISTINCT FROM OLD.ai_proposal_id
     OR NEW.updated_by::text IS DISTINCT FROM nullif(current_setting('app.user_id', true), '')
     OR NOT EXISTS (SELECT 1 FROM twin.ai_proposals p WHERE p.organization_id = NEW.organization_id AND p.model_id = NEW.model_id
                    AND p.id = NEW.ai_proposal_id AND p.mode = 'edit' AND p.investigation_id::text = acting) THEN
    RAISE EXCEPTION 'an AI investigation changes a draft only by writing its own edit, for its requester' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.guard_draft_ai_edit() FROM PUBLIC;
CREATE TRIGGER guard_draft_ai_edit BEFORE INSERT OR UPDATE ON twin.model_drafts
  FOR EACH ROW EXECUTE FUNCTION twin.guard_draft_ai_edit();
-- At commit, a draft that gains an edit proposal holds that proposal's
-- applied revision (a person's later writes carry it forward unchecked).
CREATE FUNCTION twin.check_draft_ai_edit() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.ai_proposal_id IS NOT DISTINCT FROM OLD.ai_proposal_id THEN
    RETURN NULL;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM twin.ai_proposals p WHERE p.organization_id = NEW.organization_id AND p.model_id = NEW.model_id
                 AND p.id = NEW.ai_proposal_id AND p.mode = 'edit' AND p.status = 'applied' AND p.applied_draft_revision = NEW.revision) THEN
    RAISE EXCEPTION 'a draft names an edit proposal only with that proposal''s applied revision' USING ERRCODE = '23514';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_draft_ai_edit() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER draft_ai_edit_is_applied AFTER INSERT OR UPDATE ON twin.model_drafts
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.ai_proposal_id IS NOT NULL)
  EXECUTE FUNCTION twin.check_draft_ai_edit();
-- A version a person publishes from a draft with the AI's edit names that
-- applied edit proposal of its model.
CREATE FUNCTION twin.check_version_ai_edit() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM twin.ai_proposals p WHERE p.organization_id = NEW.organization_id AND p.model_id = NEW.model_id
                 AND p.id = NEW.ai_edit_proposal_id AND p.mode = 'edit' AND p.status = 'applied') THEN
    RAISE EXCEPTION 'a version names an AI edit only of an applied edit proposal of its model' USING ERRCODE = '23514';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_version_ai_edit() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER version_ai_edit_is_applied AFTER INSERT ON twin.model_versions
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.ai_edit_proposal_id IS NOT NULL)
  EXECUTE FUNCTION twin.check_version_ai_edit();

-- ---------------------------------------------------------------------
-- Deny triggers (H-061 §11): while app.investigation_id is set, the API
-- role acts for the AI and the database refuses everything but the tool
-- effects, whatever its grants allow: no publish, no decision, no write to
-- memberships, identities, projects, models, versions, datasets, imports,
-- idempotency keys, images or investigations; no change to or removal of
-- runs, scenarios, analyses, run manifests or jobs.
CREATE FUNCTION twin.deny_under_investigation() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF nullif(current_setting('app.investigation_id', true), '') IS NOT NULL THEN
    RAISE EXCEPTION 'an AI investigation may not % %', lower(TG_OP), TG_TABLE_NAME USING ERRCODE = '42501';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.deny_under_investigation() FROM PUBLIC;
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['memberships', 'organizations', 'users', 'projects', 'models', 'model_versions', 'model_version_datasets',
                           'datasets', 'dataset_versions', 'imports', 'idempotency_keys', 'ai_proposal_images', 'investigations'] LOOP
    EXECUTE format('CREATE TRIGGER deny_under_investigation BEFORE INSERT OR UPDATE OR DELETE ON twin.%I '
                   'FOR EACH STATEMENT EXECUTE FUNCTION twin.deny_under_investigation()', t);
  END LOOP;
  FOREACH t IN ARRAY ARRAY['simulation_runs', 'scenarios', 'ai_analyses', 'run_datasets', 'jobs'] LOOP
    EXECUTE format('CREATE TRIGGER deny_under_investigation BEFORE UPDATE OR DELETE ON twin.%I '
                   'FOR EACH STATEMENT EXECUTE FUNCTION twin.deny_under_investigation()', t);
  END LOOP;
END $$;
CREATE TRIGGER deny_under_investigation BEFORE DELETE ON twin.ai_proposals
  FOR EACH STATEMENT EXECUTE FUNCTION twin.deny_under_investigation();
CREATE TRIGGER deny_under_investigation BEFORE INSERT OR DELETE ON twin.model_drafts
  FOR EACH STATEMENT EXECUTE FUNCTION twin.deny_under_investigation();
CREATE TRIGGER deny_under_investigation BEFORE INSERT OR DELETE ON twin.investigation_tool_calls
  FOR EACH STATEMENT EXECUTE FUNCTION twin.deny_under_investigation();

-- Audit (H-061 §6.2 step 8, §10): inside an acting-for transaction every
-- event is the requester's own (guard_audit_actor, unchanged), of the
-- investigation catalogue, and marked as the AI's (investigation_id = the
-- scope, by_ai true); outside one, a person's event never carries the mark.
CREATE FUNCTION twin.guard_audit_investigation() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE acting text := nullif(current_setting('app.investigation_id', true), '');
BEGIN
  IF acting IS NOT NULL THEN
    IF NEW.actor_type <> 'user' OR NEW.action NOT IN ('proposal.create', 'proposal.apply', 'simulation.create', 'analysis.create',
         'model.draft.update', 'investigation.tool_call')
       OR NEW.metadata ->> 'investigation_id' IS DISTINCT FROM acting OR NEW.metadata -> 'by_ai' IS DISTINCT FROM 'true'::jsonb THEN
      RAISE EXCEPTION 'an AI investigation records only its own actions, marked as the AI''s' USING ERRCODE = '42501';
    END IF;
  ELSIF NEW.actor_type = 'user' AND (NEW.metadata ? 'investigation_id' OR NEW.metadata ? 'by_ai')
        AND NOT pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE') THEN
    RAISE EXCEPTION 'a person''s event carries the AI''s mark only inside its investigation' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.guard_audit_investigation() FROM PUBLIC;
CREATE TRIGGER guard_audit_investigation BEFORE INSERT ON twin.audit_events
  FOR EACH ROW EXECUTE FUNCTION twin.guard_audit_investigation();

-- ---------------------------------------------------------------------
-- The spend ceiling (H-060, H-061 §12): an investigation's settled and
-- open reservations, its planner turns' and its proposals' and analyses'
-- together, plus the new one, stay within USD 2.50 (2.5e9 nano-USD), and it
-- has at most 20 planner turns. Under an advisory lock of the
-- investigation (reservations of one organization day already serialize on
-- its row; this covers an investigation across midnight UTC). Settlement is
-- never refused (an INSERT trigger only).
CREATE FUNCTION twin.check_investigation_spend() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  inv text := NEW.investigation_id;
  held numeric;
  turns integer;
BEGIN
  IF inv IS NULL AND NEW.ai_proposal_id IS NOT NULL THEN
    SELECT p.investigation_id INTO inv FROM twin.ai_proposals p WHERE p.organization_id = NEW.organization_id AND p.id = NEW.ai_proposal_id;
  ELSIF inv IS NULL AND NEW.ai_analysis_id IS NOT NULL THEN
    SELECT a.investigation_id INTO inv FROM twin.ai_analyses a WHERE a.organization_id = NEW.organization_id AND a.id = NEW.ai_analysis_id;
  END IF;
  IF inv IS NULL THEN
    RETURN NEW;
  END IF;
  PERFORM pg_catalog.pg_advisory_xact_lock(61020, pg_catalog.hashtext(inv));
  SELECT coalesce(sum(CASE WHEN r.state = 'reserved' THEN r.reserved_nano_usd ELSE r.settled_nano_usd END), 0),
         count(*) FILTER (WHERE r.role = 'investigator')
    INTO held, turns FROM twin.ai_spend_reservations r
    WHERE r.organization_id = NEW.organization_id AND (r.investigation_id::text = inv
      OR r.ai_proposal_id IN (SELECT p.id FROM twin.ai_proposals p WHERE p.organization_id = NEW.organization_id AND p.investigation_id::text = inv)
      OR r.ai_analysis_id IN (SELECT a.id FROM twin.ai_analyses a WHERE a.organization_id = NEW.organization_id AND a.investigation_id::text = inv));
  IF held + NEW.reserved_nano_usd > 2500000000 THEN
    RAISE EXCEPTION 'an investigation spends at most USD 2.50' USING ERRCODE = '23514', CONSTRAINT = 'ai_spend_reservations_investigation_ceiling';
  END IF;
  IF NEW.role = 'investigator' AND turns >= 20 THEN
    RAISE EXCEPTION 'an investigation has at most 20 planner turns' USING ERRCODE = '23514', CONSTRAINT = 'ai_spend_reservations_investigation_turns';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.check_investigation_spend() FROM PUBLIC;
CREATE TRIGGER check_investigation_spend BEFORE INSERT ON twin.ai_spend_reservations
  FOR EACH ROW EXECUTE FUNCTION twin.check_investigation_spend();
COMMIT;
