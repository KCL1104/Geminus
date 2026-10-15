-- Illustrative parameterized SQL for repositories; not a runnable migration.
-- PROPOSED D-008/D-011. Every block uses an explicit scoped transaction.

-- Set from verified auth context; true means transaction-local.
-- SELECT set_config('app.organization_id',$1,true), set_config('app.user_id',$2,true);
-- SELECT role FROM twin.memberships WHERE organization_id=$1 AND user_id=$2;

-- Claim exactly one job in an assigned tenant; RLS is still enforced.
-- Caller supplies trusted worker_id and approved lease duration.
WITH next_job AS (
  SELECT id FROM twin.jobs
  WHERE organization_id=$1 AND status='queued' AND available_at<=now()
    AND attempt<max_attempts
  ORDER BY priority DESC,created_at,id
  FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE twin.jobs j
SET status='running', attempt=attempt+1, locked_at=now(), locked_by=$2,
    lease_token=lease_token+1, lease_expires_at=now()+($3::integer*interval '1 second'),
    heartbeat_at=now()
FROM next_job n
WHERE j.id=n.id AND j.organization_id=$1
RETURNING j.*;

-- Heartbeat uses a fence; zero updated rows means ownership is lost.
UPDATE twin.jobs
SET heartbeat_at=now(), lease_expires_at=now()+($5::integer*interval '1 second')
WHERE organization_id=$1 AND id=$2 AND status='running'
  AND locked_by=$3 AND lease_token=$4 AND lease_expires_at>now();

-- Manual draft CAS; no row means distinguish not-found vs conflict in authorized scope.
UPDATE twin.model_drafts
SET ir=$4::jsonb,hash=$5,revision=revision+1,updated_by=$6,updated_at=now()
WHERE organization_id=$1 AND model_id=$2 AND revision=$3
RETURNING *;

-- Approval skeleton: role check and idempotency reservation occur before domain work.
-- 1 SELECT model FOR UPDATE; 2 SELECT draft FOR UPDATE; 3 SELECT proposal FOR UPDATE.
-- Compare model.latest_version_id, proposal.base_version_id and expected_base_version_id.
-- Revalidate frozen patch + dataset manifest, then INSERT one version OR one scenario.
-- UPDATE proposal to approved, update clean draft only, INSERT audit.
-- Store response in same transaction; COMMIT. Never call LLM while holding these locks.

-- Worker finalization skeleton (must be one transaction, not independent statements):
-- SELECT job FOR UPDATE WHERE org/id/lease_token/locked_by/status match AND lease not expired;
-- SELECT run FOR UPDATE; reject if cancellation_requested or terminal;
-- INSERT simulation_results (unique run_id);
-- UPDATE run SET status='completed', completed_replications=replications, finished_at=now();
-- UPDATE job SET status='completed', locked_at=NULL,locked_by=NULL,lease_expires_at=NULL;
-- INSERT audit; COMMIT. A stale attempt is never allowed to overwrite a completed result.
