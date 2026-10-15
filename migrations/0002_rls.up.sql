-- PROPOSED D-008/D-013. RLS + least-privilege draft.
-- Application RBAC, membership authorization and SQL-injection prevention remain required.
BEGIN;
SET LOCAL ROLE twin_owner;
CREATE FUNCTION twin.current_organization_id() RETURNS text
  LANGUAGE sql STABLE AS $$ SELECT nullif(current_setting('app.organization_id',true),'') $$;
CREATE FUNCTION twin.current_actor_id() RETURNS text
  LANGUAGE sql STABLE AS $$ SELECT nullif(current_setting('app.user_id',true),'') $$;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA twin FROM PUBLIC;
GRANT USAGE ON SCHEMA twin TO twin_api,twin_worker;
GRANT USAGE ON TYPE twin.public_id,twin.sha256 TO twin_api,twin_worker;
GRANT EXECUTE ON FUNCTION twin.current_organization_id(),twin.current_actor_id() TO twin_api,twin_worker;
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
GRANT UPDATE ON twin.imports,twin.jobs TO twin_worker;
GRANT UPDATE (status,summary,assumptions,patch,patch_hash,candidate_dataset_bindings,
  expected_effects,questions,risk_level,confidence,validation,provider_metadata,error)
  ON twin.ai_proposals TO twin_worker;
GRANT UPDATE (status,completed_replications,events_processed,progress_revision,
  error,started_at,finished_at,execution_deadline_at,event_budget_reserved) ON twin.simulation_runs TO twin_worker;
GRANT UPDATE (status,analysis,provider_metadata,error) ON twin.ai_analyses TO twin_worker;
-- No runtime UPDATE/DELETE/TRUNCATE on immutable versions/results/audit.
-- No worker idempotency/member/auth access. No PUBLIC EXECUTE on authority functions.
ALTER DEFAULT PRIVILEGES FOR ROLE twin_owner IN SCHEMA twin REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE twin_owner IN SCHEMA twin REVOKE ALL ON FUNCTIONS FROM PUBLIC;
COMMIT;
