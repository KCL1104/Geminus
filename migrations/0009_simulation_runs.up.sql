-- T-023 (API_DATABASE §6–§8, D-007, D-011, D-044; TEST-019, TEST-020,
-- TEST-021): a simulation run's lifecycle, its result and its sampled
-- trace, kept consistent by the database for every role.
--
--   * A run starts queued with nothing done. It moves queued -> running
--     (the worker starts an attempt), running -> queued (the attempt is
--     retried), queued|running -> failed|cancelled, running -> completed
--     (only with its result); completed, failed and cancelled are final
--     (0001's protect_run_inputs). A cancellation request is never
--     withdrawn, every change a client can see (status, progress, the
--     cancellation request) advances progress_revision, which the SSE
--     stream follows, and error is set exactly when the run failed.
--   * The run follows its job (follow_simulation_job): when the worker
--     loop retries or fails an attempt, the reaper takes an expired lease
--     back, the API cancels a queued job or the worker ends a cancelled
--     one, the run moves with it in the same transaction; a running run is
--     cancelled only after its cancellation was requested. A job completes
--     only once its run has completed with its result.
--   * A run's identity (ID, name, creator, creation time) never changes,
--     for any role (0001 freezes its inputs).
--   * Only the job moves a run to queued, failed or cancelled, for every
--     role, the owner included, and a run completes only in the
--     transaction that completes its job: a run final while its job still
--     runs would stall the reaper (T-023 review L1).
--   * Each runtime role moves a run only its own way, as 0007 does for
--     jobs: the API creates runs and requests cancellation; the worker
--     starts an attempt (queued -> running), writes its progress and event
--     budget, and completes the run; every other move comes from the job.
--     Only the schema owner is otherwise unrestricted.
--   * A result is stored only for its running run, not while its
--     cancellation is requested, and only if it reproduces the run's frozen
--     input (engine version, seed, replications, every hash); its digest
--     is the one inside it. The sampled trace is named by artifact ID,
--     object key (under the run's own prefix), SHA-256 and size, all or
--     none, and the result names the same artifact (D-044).
BEGIN;
SET LOCAL ROLE twin_owner;

ALTER TABLE twin.simulation_runs
  ADD CONSTRAINT simulation_runs_error_iff_failed CHECK ((status = 'failed') = (error IS NOT NULL));

-- 64 MiB bounds one trace (100,000 events of at most a few hundred bytes).
ALTER TABLE twin.simulation_results
  ADD COLUMN trace_content_hash twin.sha256,
  ADD COLUMN trace_size_bytes bigint CHECK (trace_size_bytes BETWEEN 1 AND 67108864),
  ADD CONSTRAINT simulation_results_trace_all_or_none
    CHECK (num_nonnulls(trace_artifact_id, trace_object_key, trace_content_hash, trace_size_bytes) IN (0, 4)),
  ADD CONSTRAINT simulation_results_trace_of_the_run
    CHECK (trace_object_key IS NULL OR starts_with(trace_object_key, 'tenant/' || organization_id || '/simulation-results/' || run_id || '/')),
  ADD CONSTRAINT simulation_results_trace_named
    CHECK ((result -> 'trace' ->> 'artifact_id') IS NOT DISTINCT FROM trace_artifact_id::text),
  ADD CONSTRAINT simulation_results_digest
    CHECK (result_digest::text = result -> 'reproducibility' ->> 'result_digest');

CREATE FUNCTION twin.protect_run_lifecycle() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  owner boolean := pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE');
  api boolean := pg_catalog.pg_has_role(current_user, 'twin_api', 'USAGE');
  worker boolean := pg_catalog.pg_has_role(current_user, 'twin_worker', 'USAGE');
  -- follow_simulation_job is the only trigger that writes runs.
  followed boolean := pg_catalog.pg_trigger_depth() > 1;
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.status <> 'queued' OR NEW.cancellation_requested OR NEW.completed_replications <> 0 OR NEW.events_processed <> 0
       OR NEW.progress_revision <> 0 OR NEW.event_budget_reserved <> 0 OR NEW.started_at IS NOT NULL
       OR NEW.execution_deadline_at IS NOT NULL OR NEW.finished_at IS NOT NULL OR NEW.error IS NOT NULL THEN
      RAISE EXCEPTION 'a run starts queued with nothing done' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
  END IF;
  -- 0001 freezes the inputs; the identity is frozen here, for every role.
  IF ROW(NEW.id, NEW.name, NEW.created_by, NEW.created_at) IS DISTINCT FROM ROW(OLD.id, OLD.name, OLD.created_by, OLD.created_at) THEN
    RAISE EXCEPTION 'a run''s identity (ID, name, creator, creation time) never changes' USING ERRCODE = '55000';
  END IF;
  IF NEW IS NOT DISTINCT FROM OLD OR OLD.status IN ('completed', 'failed', 'cancelled') THEN
    RETURN NEW; -- a final run is protect_run_inputs' to refuse
  END IF;
  IF NEW.status <> OLD.status AND NOT (
       (OLD.status = 'queued' AND NEW.status IN ('running', 'failed', 'cancelled')) OR
       (OLD.status = 'running' AND NEW.status IN ('queued', 'completed', 'failed', 'cancelled'))) THEN
    RAISE EXCEPTION 'a % run cannot become %', OLD.status, NEW.status USING ERRCODE = '55000';
  END IF;
  IF NEW.status = 'completed' AND NOT EXISTS (
       SELECT 1 FROM twin.simulation_results r WHERE r.organization_id = NEW.organization_id AND r.run_id = NEW.id) THEN
    RAISE EXCEPTION 'a run completes with its result' USING ERRCODE = '55000';
  END IF;
  IF OLD.cancellation_requested AND NOT NEW.cancellation_requested THEN
    RAISE EXCEPTION 'a cancellation request is never withdrawn' USING ERRCODE = '55000';
  END IF;
  IF NEW.progress_revision < OLD.progress_revision OR
     (ROW(NEW.status, NEW.completed_replications, NEW.events_processed, NEW.cancellation_requested)
        IS DISTINCT FROM ROW(OLD.status, OLD.completed_replications, OLD.events_processed, OLD.cancellation_requested)
      AND NEW.progress_revision <= OLD.progress_revision) THEN
    RAISE EXCEPTION 'every visible change of a run advances its progress revision' USING ERRCODE = '55000';
  END IF;
  -- Only the job moves a run to queued, failed or cancelled, whoever asks,
  -- the owner included: a run final while its job still runs would make
  -- the reaper's UPDATE fail for the whole organization once the lease
  -- expires (T-023 review L1). An operator ends the job; the run follows.
  IF NEW.status <> OLD.status AND NEW.status IN ('queued', 'failed', 'cancelled') AND NOT followed THEN
    RAISE EXCEPTION 'a run becomes % only with its job: end or retry the job, and the run follows', NEW.status USING ERRCODE = '55000';
  END IF;
  IF owner THEN
    RETURN NEW;
  END IF;
  IF followed THEN
    IF NEW.status NOT IN ('queued', 'failed', 'cancelled') THEN
      RAISE EXCEPTION 'a run follows its job only to queued, failed or cancelled' USING ERRCODE = '55000';
    END IF;
  ELSIF worker THEN
    IF NOT (NEW.status = 'running' OR (OLD.status = 'running' AND NEW.status = 'completed')) THEN
      RAISE EXCEPTION 'the worker starts, advances and completes a run; its job moves it otherwise' USING ERRCODE = '55000';
    END IF;
  ELSIF api THEN
    IF ROW(NEW.status, NEW.completed_replications, NEW.events_processed, NEW.error, NEW.started_at, NEW.finished_at,
           NEW.execution_deadline_at, NEW.event_budget_reserved)
       IS DISTINCT FROM
       ROW(OLD.status, OLD.completed_replications, OLD.events_processed, OLD.error, OLD.started_at, OLD.finished_at,
           OLD.execution_deadline_at, OLD.event_budget_reserved) THEN
      RAISE EXCEPTION 'the API only requests a run''s cancellation; its job moves it' USING ERRCODE = '55000';
    END IF;
  ELSE
    RAISE EXCEPTION 'only the API, the worker and the schema owner write runs' USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_run_lifecycle() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER protect_run_lifecycle BEFORE INSERT OR UPDATE ON twin.simulation_runs
  FOR EACH ROW EXECUTE FUNCTION twin.protect_run_lifecycle();

CREATE FUNCTION twin.check_simulation_result() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE r record;
BEGIN
  SELECT status, cancellation_requested, engine_version, seed, replications, model_hash, effective_model_hash,
         config_hash, dataset_manifest_hash
    INTO r FROM twin.simulation_runs WHERE organization_id = NEW.organization_id AND id = NEW.run_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'a result belongs to a run of its organization' USING ERRCODE = '23503';
  END IF;
  IF r.status <> 'running' OR r.cancellation_requested THEN
    RAISE EXCEPTION 'a result is stored only for a running run whose cancellation is not requested' USING ERRCODE = '55000';
  END IF;
  IF ROW(NEW.result -> 'engine_version', NEW.result -> 'reproducibility' -> 'seed', NEW.result -> 'reproducibility' -> 'replications',
         NEW.result -> 'reproducibility' -> 'model_hash', NEW.result -> 'reproducibility' -> 'effective_model_hash',
         NEW.result -> 'reproducibility' -> 'config_hash', NEW.result -> 'reproducibility' -> 'dataset_manifest_hash')
     IS DISTINCT FROM
     ROW(to_jsonb(r.engine_version), to_jsonb(r.seed), to_jsonb(r.replications), to_jsonb(r.model_hash::text),
         to_jsonb(r.effective_model_hash::text), to_jsonb(r.config_hash::text), to_jsonb(r.dataset_manifest_hash::text)) THEN
    RAISE EXCEPTION 'a result reproduces its run''s frozen input' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.check_simulation_result() FROM PUBLIC;
CREATE TRIGGER check_simulation_result BEFORE INSERT ON twin.simulation_results
  FOR EACH ROW EXECUTE FUNCTION twin.check_simulation_result();

-- A run completes only in the transaction that completes its job (the
-- worker's Finish), for every role: a completed run whose job still runs
-- would stall the reaper as above (T-023 review L1).
CREATE FUNCTION twin.check_run_completes_with_its_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM twin.jobs j
                 WHERE j.organization_id = NEW.organization_id AND j.simulation_run_id = NEW.id AND j.status = 'completed') THEN
    RAISE EXCEPTION 'a run completes in the transaction that completes its job' USING ERRCODE = '55000';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_run_completes_with_its_job() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER run_completes_with_its_job AFTER UPDATE OF status ON twin.simulation_runs
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW WHEN (NEW.status = 'completed' AND OLD.status IS DISTINCT FROM NEW.status)
  EXECUTE FUNCTION twin.check_run_completes_with_its_job();

-- Runs with the privileges of whoever moves the job: the worker (its
-- column UPDATE on simulation_runs covers what this writes) or the API.
CREATE FUNCTION twin.follow_simulation_job() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE s text; requested boolean;
BEGIN
  SELECT status, cancellation_requested INTO s, requested FROM twin.simulation_runs
    WHERE organization_id = NEW.organization_id AND id = NEW.simulation_run_id FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'a simulation job''s run is not visible to its organization' USING ERRCODE = '55000';
  END IF;
  IF NEW.status = 'running' THEN
    RETURN NULL; -- a claim: the worker marks the run running when the attempt starts
  ELSIF NEW.status = 'completed' THEN
    IF s <> 'completed' THEN
      RAISE EXCEPTION 'a simulation job completes only with its run''s result' USING ERRCODE = '55000';
    END IF;
    RETURN NULL;
  ELSIF s NOT IN ('queued', 'running') THEN
    RAISE EXCEPTION 'a % run cannot follow its job to %', s, NEW.status USING ERRCODE = '55000';
  ELSIF NEW.status = 'cancelled' AND OLD.status = 'running' AND NOT requested THEN
    RAISE EXCEPTION 'a running simulation is cancelled only after its cancellation was requested' USING ERRCODE = '55000';
  END IF;
  -- A retry starts the replications again; a failure or cancellation ends
  -- the run now. Either way the progress revision moves.
  UPDATE twin.simulation_runs SET
      status = NEW.status,
      completed_replications = CASE WHEN NEW.status = 'queued' THEN 0 ELSE completed_replications END,
      events_processed = CASE WHEN NEW.status = 'queued' THEN 0 ELSE events_processed END,
      error = CASE WHEN NEW.status = 'failed'
                   THEN coalesce(NEW.error, '{"code":"INTERNAL","message":"the job failed","retryable":false}'::jsonb) END,
      finished_at = CASE WHEN NEW.status IN ('failed', 'cancelled') THEN now() END,
      progress_revision = progress_revision + 1
    WHERE organization_id = NEW.organization_id AND id = NEW.simulation_run_id;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.follow_simulation_job() FROM PUBLIC;
CREATE TRIGGER follow_simulation_job AFTER UPDATE OF status ON twin.jobs
  FOR EACH ROW WHEN (NEW.type = 'simulation' AND NEW.status IS DISTINCT FROM OLD.status)
  EXECUTE FUNCTION twin.follow_simulation_job();
COMMIT;
