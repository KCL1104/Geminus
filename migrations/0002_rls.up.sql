-- RLS + least-privilege grants (D-013 approved; tenant selection rules are D-008, still PROPOSED).
-- Application RBAC, membership authorization and SQL-injection prevention remain required.
BEGIN;
SET LOCAL ROLE twin_owner;
CREATE FUNCTION twin.current_organization_id() RETURNS text
  LANGUAGE sql STABLE AS $$ SELECT nullif(current_setting('app.organization_id',true),'') $$;
CREATE FUNCTION twin.current_actor_id() RETURNS text
  LANGUAGE sql STABLE AS $$ SELECT nullif(current_setting('app.user_id',true),'') $$;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA twin FROM PUBLIC;
GRANT USAGE ON SCHEMA twin TO twin_api,twin_worker;
DO $$
DECLARE d text;
BEGIN
  FOR d IN SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
           WHERE n.nspname='twin' AND t.typtype='d' ORDER BY t.typname LOOP
    EXECUTE format('GRANT USAGE ON TYPE twin.%I TO twin_api,twin_worker',d);
  END LOOP;
END $$;
GRANT EXECUTE ON FUNCTION twin.current_organization_id(),twin.current_actor_id(),
  twin.object_key_ok(text,text) TO twin_api,twin_worker;
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'organizations','memberships','projects','models','model_versions','model_drafts',
    'datasets','dataset_versions','model_version_datasets','imports','ai_proposals',
    'scenarios','simulation_runs','run_datasets','simulation_results','ai_analyses',
    'jobs','idempotency_keys','audit_events'
  ] LOOP
    EXECUTE format('ALTER TABLE twin.%I ENABLE ROW LEVEL SECURITY',t);
    EXECUTE format('ALTER TABLE twin.%I FORCE ROW LEVEL SECURITY',t);
    EXECUTE format('CREATE POLICY tenant_isolation ON twin.%I FOR ALL USING (organization_id::text=twin.current_organization_id()) WITH CHECK (organization_id::text=twin.current_organization_id())',t);
  END LOOP;
END $$;
-- Global identity table: only the verified issuer/subject may be read by API.
-- Auth middleware sets these from the validated token, never raw client headers.
ALTER TABLE twin.users ENABLE ROW LEVEL SECURITY;
ALTER TABLE twin.users FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_self ON twin.users FOR ALL
  USING (oidc_issuer=nullif(current_setting('app.oidc_issuer',true),'') AND
         oidc_subject=nullif(current_setting('app.oidc_subject',true),''))
  WITH CHECK (oidc_issuer=nullif(current_setting('app.oidc_issuer',true),'') AND
              oidc_subject=nullif(current_setting('app.oidc_subject',true),''));
REVOKE ALL ON ALL TABLES IN SCHEMA twin FROM PUBLIC;
GRANT SELECT ON twin.users TO twin_api;
GRANT SELECT ON twin.organizations,twin.memberships,twin.projects,twin.models,twin.model_versions,
  twin.model_drafts,twin.datasets,twin.dataset_versions,twin.model_version_datasets,twin.imports,
  twin.ai_proposals,twin.scenarios,twin.simulation_runs,twin.run_datasets,twin.simulation_results,
  twin.ai_analyses,twin.jobs,twin.idempotency_keys,twin.audit_events TO twin_api;
GRANT INSERT ON twin.memberships,twin.projects,twin.models,twin.model_versions,twin.model_drafts,
  twin.datasets,twin.dataset_versions,twin.model_version_datasets,twin.imports,twin.ai_proposals,
  twin.scenarios,twin.simulation_runs,twin.run_datasets,twin.ai_analyses,twin.jobs,twin.idempotency_keys,
  twin.audit_events TO twin_api;
GRANT UPDATE ON twin.memberships,twin.projects,twin.models,twin.model_drafts,twin.imports,
  twin.ai_proposals,twin.simulation_runs,twin.jobs,twin.idempotency_keys TO twin_api;
GRANT DELETE ON twin.memberships TO twin_api;
-- Worker can never INSERT/PUBLISH canonical model_versions or scenarios.
GRANT SELECT ON twin.projects,twin.models,twin.model_versions,twin.model_version_datasets,
  twin.datasets,twin.dataset_versions,twin.imports,twin.ai_proposals,twin.scenarios,
  twin.simulation_runs,twin.run_datasets,twin.simulation_results,twin.ai_analyses,twin.jobs TO twin_worker;
GRANT INSERT ON twin.datasets,twin.dataset_versions,twin.simulation_results,twin.audit_events TO twin_worker;
-- D-037 / CB-16: column-level UPDATE only. The worker records profiling,
-- validation, mapping suggestions and errors on imports, and lease state on
-- jobs; mapping, mapping_revision, dataset_version_id and proposal_id belong
-- to the human-confirmed API path, and committing an import is never a worker
-- action (guard trigger below).
GRANT UPDATE (status,total_rows,invalid_rows,preview,validation,mapping_suggestion_status,
  mapping_suggestion,error,error_report_object_key) ON twin.imports TO twin_worker;
GRANT UPDATE (status,attempt,available_at,locked_at,locked_by,lease_token,lease_expires_at,
  heartbeat_at,error) ON twin.jobs TO twin_worker;
GRANT UPDATE (status,summary,assumptions,patch,patch_hash,candidate_dataset_bindings,
  expected_effects,questions,risk_level,server_risk_level,provider_risk_claim,confidence,
  validation,provider_metadata,error)
  ON twin.ai_proposals TO twin_worker;
GRANT UPDATE (status,completed_replications,events_processed,progress_revision,
  error,started_at,finished_at,execution_deadline_at,event_budget_reserved) ON twin.simulation_runs TO twin_worker;
GRANT UPDATE (status,analysis,provider_metadata,error) ON twin.ai_analyses TO twin_worker;
CREATE FUNCTION twin.guard_worker_import() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  -- Members of twin_owner (and superusers, who are members of every role) are
  -- not runtime workers; every other role with twin_worker's privileges is.
  IF NEW.status='COMMITTED' AND OLD.status IS DISTINCT FROM 'COMMITTED'
     AND pg_has_role(current_user,'twin_worker','USAGE') AND NOT pg_has_role(current_user,'twin_owner','USAGE') THEN
    RAISE EXCEPTION 'only the API may commit an import' USING ERRCODE='42501';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.guard_worker_import() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER guard_worker_import BEFORE UPDATE ON twin.imports
  FOR EACH ROW EXECUTE FUNCTION twin.guard_worker_import();
-- No runtime UPDATE/DELETE/TRUNCATE on immutable versions/results/audit.
-- No worker idempotency/member/auth access. No PUBLIC EXECUTE on authority functions.
ALTER DEFAULT PRIVILEGES FOR ROLE twin_owner IN SCHEMA twin REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE twin_owner IN SCHEMA twin REVOKE ALL ON FUNCTIONS FROM PUBLIC;
COMMIT;
