-- DEVELOPMENT-ONLY DOWN MIGRATION of 0009: runs lose their lifecycle guard
-- and stop following their jobs; results lose the trace's hash and size
-- (dropped with their values) and the checks that bind them to their run.
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER follow_simulation_job ON twin.jobs;
DROP FUNCTION twin.follow_simulation_job();
DROP TRIGGER check_simulation_result ON twin.simulation_results;
DROP FUNCTION twin.check_simulation_result();
DROP TRIGGER protect_run_lifecycle ON twin.simulation_runs;
DROP FUNCTION twin.protect_run_lifecycle();
ALTER TABLE twin.simulation_results
  DROP CONSTRAINT simulation_results_digest,
  DROP CONSTRAINT simulation_results_trace_named,
  DROP CONSTRAINT simulation_results_trace_of_the_run,
  DROP CONSTRAINT simulation_results_trace_all_or_none,
  DROP COLUMN trace_size_bytes,
  DROP COLUMN trace_content_hash;
ALTER TABLE twin.simulation_runs DROP CONSTRAINT simulation_runs_error_iff_failed;
COMMIT;
