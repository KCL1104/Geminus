-- T-019 (ADR-024; API_DATABASE §6–§7, TEST-020, TEST-021): a job moves only
-- along its lifecycle and a finished job never changes. internal/storage/jobs
-- fences each write by the lease; this holds for every write, including the
-- API role's table-wide UPDATE and the owner's.
--   queued  -> running (a claim: next attempt, new lease token), cancelled
--   running -> running (heartbeat, lease taken over), queued (retry, reaper),
--              completed, failed, cancelled
--   completed, failed, cancelled: final.
-- A job starts queued with no attempt, lease or error; its identity and
-- input never change; attempts and lease tokens never go back.
BEGIN;
SET LOCAL ROLE twin_owner;
CREATE FUNCTION twin.protect_job_state() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF TG_OP='INSERT' THEN
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
  IF OLD.status='queued' AND NEW.status='running' AND (NEW.attempt<>OLD.attempt+1 OR NEW.lease_token<=OLD.lease_token) THEN
    RAISE EXCEPTION 'a claim starts the next attempt under a new lease token' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_job_state() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER protect_job_state BEFORE INSERT OR UPDATE ON twin.jobs
  FOR EACH ROW EXECUTE FUNCTION twin.protect_job_state();
COMMIT;
