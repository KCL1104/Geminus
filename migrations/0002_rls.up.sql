-- RLS + least-privilege grants (D-013; tenant selection rules D-008; both approved).
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
-- The guards below hold even if a worker login were given broader UPDATE
-- (e.g. pg_write_all_data): members of twin_owner (and superusers, who are
-- members of every role) are not runtime workers; every other role with
-- twin_worker's privileges is.
CREATE FUNCTION twin.guard_worker_import() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF pg_catalog.pg_has_role(current_user,'twin_worker','USAGE') AND NOT pg_catalog.pg_has_role(current_user,'twin_owner','USAGE')
     AND (OLD.status='COMMITTED' OR NEW.status='COMMITTED' OR
          ROW(NEW.id,NEW.organization_id,NEW.project_id,NEW.model_id,NEW.base_version_id,NEW.kind,NEW.filename,
              NEW.upload_object_key,NEW.upload_content_hash,NEW.size_bytes,NEW.mapping_revision,NEW.mapping,
              NEW.dataset_version_id,NEW.proposal_id,NEW.created_by,NEW.created_at)
          IS DISTINCT FROM
          ROW(OLD.id,OLD.organization_id,OLD.project_id,OLD.model_id,OLD.base_version_id,OLD.kind,OLD.filename,
              OLD.upload_object_key,OLD.upload_content_hash,OLD.size_bytes,OLD.mapping_revision,OLD.mapping,
              OLD.dataset_version_id,OLD.proposal_id,OLD.created_by,OLD.created_at)) THEN
    RAISE EXCEPTION 'the worker may not commit, map or change a committed import' USING ERRCODE='42501';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.guard_worker_import() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER guard_worker_import BEFORE UPDATE ON twin.imports
  FOR EACH ROW EXECUTE FUNCTION twin.guard_worker_import();
-- The worker fills generating proposals; deciding, or touching a proposal
-- that waits for a person, is a human Editor/Owner action (SECURITY §2).
-- Fires before protect_proposal (trigger names sort alphabetically).
CREATE FUNCTION twin.guard_worker_proposal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status<>'generating'
     AND pg_catalog.pg_has_role(current_user,'twin_worker','USAGE') AND NOT pg_catalog.pg_has_role(current_user,'twin_owner','USAGE') THEN
    RAISE EXCEPTION 'the worker only fills generating proposals' USING ERRCODE='42501';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.guard_worker_proposal() FROM PUBLIC;
CREATE TRIGGER guard_worker_proposal BEFORE UPDATE ON twin.ai_proposals
  FOR EACH ROW EXECUTE FUNCTION twin.guard_worker_proposal();
-- A "user" audit event always names the scoped user, so neither the worker
-- nor a bug can record an action in a person's name.
CREATE FUNCTION twin.guard_audit_actor() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.actor_type='user' AND NOT pg_catalog.pg_has_role(current_user,'twin_owner','USAGE')
     AND (pg_catalog.pg_has_role(current_user,'twin_worker','USAGE')
          OR NEW.actor_id::text IS DISTINCT FROM twin.current_actor_id()) THEN
    RAISE EXCEPTION 'a user audit event names the scoped user' USING ERRCODE='42501';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.guard_audit_actor() FROM PUBLIC;
CREATE TRIGGER guard_audit_actor BEFORE INSERT ON twin.audit_events
  FOR EACH ROW EXECUTE FUNCTION twin.guard_audit_actor();
-- No runtime UPDATE/DELETE/TRUNCATE on immutable versions/results/audit.
-- No worker idempotency/member/auth access. No PUBLIC EXECUTE on authority functions.
ALTER DEFAULT PRIVILEGES FOR ROLE twin_owner IN SCHEMA twin REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE twin_owner IN SCHEMA twin REVOKE ALL ON FUNCTIONS FROM PUBLIC;
-- Like 0001's functions, these never search another schema, so an object
-- created in, say, public (which clusters upgraded from PG14 still let PUBLIC
-- create in) cannot shadow what they call. Runtime logins that can create
-- objects at all are refused at startup (tenant.CheckRuntimeRole).
DO $$
DECLARE f regprocedure;
BEGIN
  FOR f IN SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
           WHERE n.nspname='twin' AND p.proname IN ('current_organization_id','current_actor_id',
             'guard_worker_import','guard_worker_proposal','guard_audit_actor') LOOP
    EXECUTE format('ALTER FUNCTION %s SET search_path = pg_catalog, pg_temp', f);
  END LOOP;
END $$;
COMMIT;
