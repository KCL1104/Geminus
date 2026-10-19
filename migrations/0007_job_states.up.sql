-- T-019 (ADR-024; API_DATABASE §6–§7, TEST-020, TEST-021): a job moves only
-- along its lifecycle and a finished job never changes. internal/storage/jobs
-- fences each write by the lease; this holds for every write, including the
-- API role's table-wide UPDATE and the owner's.
--   queued  -> running (a claim: next attempt, new lease token), cancelled
--   running -> running (heartbeat, lease taken over), queued (retry, reaper),
--              completed, failed, cancelled
--   completed, failed, cancelled: final.
-- A job starts queued with no attempt, lease or error; a queued job always
-- has an attempt left; its identity and input never change; attempts and
-- lease tokens never go back. The runtime roles each move a job only their
-- own way (T-019 review L3, round 2 I1): the API enqueues, cancels a queued
-- job or reschedules it (priority, available_at); the worker claims a queued
-- job, renews its own lease, and ends, retries or reaps a running one,
-- clearing the lease. Only the schema owner (operators, migrations, and any
-- login that is a member of it) is otherwise unrestricted; every other role
-- is refused whatever privileges it has. This guards INSERT and UPDATE;
-- removing rows is a retention matter, not a lifecycle one (the runtime
-- roles have no DELETE on jobs).
BEGIN;
SET LOCAL ROLE twin_owner;
CREATE FUNCTION twin.protect_job_state() RETURNS trigger LANGUAGE plpgsql
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
    -- lease_expires_at), never taken over, and cleared when the job leaves
    -- running; its attempt and token change only on a claim.
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
REVOKE ALL ON FUNCTION twin.protect_job_state() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER protect_job_state BEFORE INSERT OR UPDATE ON twin.jobs
  FOR EACH ROW EXECUTE FUNCTION twin.protect_job_state();
COMMIT;
