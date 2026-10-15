-- Core schema. Source: report pp.36–37,42–43; D-001, D-013, D-027, D-034, D-035 (approved).
-- Still a draft until T-006 review: only disposable dt_test_* databases have run it.
-- From T-006 on, a migration applied to any persistent environment is never edited;
-- changes go into a new numbered migration.
-- Prerequisite: db/bootstrap_roles.sql; caller may SET ROLE twin_owner.
BEGIN;
CREATE SCHEMA twin AUTHORIZATION twin_owner;
SET LOCAL ROLE twin_owner;
REVOKE ALL ON SCHEMA twin FROM PUBLIC;
-- D-001: server IDs are <prefix>_<ULID>: 26 Crockford base32 characters, the
-- first 0-7 (a 128-bit value). One domain per kind, so a column cannot hold an
-- ID of another kind; public_id accepts any kind (audit targets).
CREATE DOMAIN twin.org_id AS text CHECK (VALUE ~ '^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.usr_id AS text CHECK (VALUE ~ '^usr_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.prj_id AS text CHECK (VALUE ~ '^prj_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.mdl_id AS text CHECK (VALUE ~ '^mdl_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.mv_id AS text CHECK (VALUE ~ '^mv_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.ds_id AS text CHECK (VALUE ~ '^ds_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.dsv_id AS text CHECK (VALUE ~ '^dsv_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.imp_id AS text CHECK (VALUE ~ '^imp_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.aip_id AS text CHECK (VALUE ~ '^aip_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.scn_id AS text CHECK (VALUE ~ '^scn_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.sim_id AS text CHECK (VALUE ~ '^sim_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.ana_id AS text CHECK (VALUE ~ '^ana_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.job_id AS text CHECK (VALUE ~ '^job_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.aud_id AS text CHECK (VALUE ~ '^aud_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.req_id AS text CHECK (VALUE ~ '^req_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.art_id AS text CHECK (VALUE ~ '^art_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
CREATE DOMAIN twin.public_id AS text CHECK (VALUE ~ '^(org|usr|prj|mdl|mv|ds|dsv|imp|aip|scn|sim|ana|job|aud|req|art)_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
-- Audit actors: user actors are usr_ IDs (checked on audit_events); the
-- worker/system identity format is open until T-035.
CREATE DOMAIN twin.actor_ref AS text CHECK (VALUE ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$');
CREATE DOMAIN twin.sha256 AS text
  CHECK (VALUE ~ '^sha256:[0-9a-f]{64}$');
-- D-035 / CB-14: object keys start with an exact prefix (starts_with; LIKE
-- would treat the '_' in organization IDs as a wildcard) and contain no
-- traversal, empty segment, backslash or control character.
CREATE FUNCTION twin.object_key_ok(object_key text, prefix text) RETURNS boolean
  LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
  -- After the tenant prefix only server-generated ASCII segments follow: no
  -- empty, "." or ".." segment and nothing a storage adapter could decode
  -- (%), normalize (Unicode look-alikes) or treat as a separator (\).
  RETURN starts_with(object_key, prefix) AND length(object_key) <= 1024 AND strpos(object_key, '..') = 0
    AND substr(object_key, length(prefix) + 1) ~ '^[0-9A-Za-z_-][0-9A-Za-z._-]*(/[0-9A-Za-z_-][0-9A-Za-z._-]*)*$';
-- D-034: logical refs are ASCII, so byte order and every collation agree on equality.
CREATE DOMAIN twin.logical_ref AS text
  CHECK (length(VALUE) <= 256 AND VALUE ~ '^dataset:[a-z0-9_.-]+$');
CREATE DOMAIN twin.time_unit AS text
  CHECK (VALUE IN ('millisecond','second','minute','hour','day'));

CREATE TABLE twin.users (
  id twin.usr_id PRIMARY KEY,
  oidc_issuer text NOT NULL,
  oidc_subject text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (oidc_issuer, oidc_subject)
);
CREATE TABLE twin.organizations (
  id twin.org_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL UNIQUE,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (id=organization_id)
);
CREATE TABLE twin.memberships (
  organization_id twin.org_id NOT NULL REFERENCES twin.organizations(id),
  user_id twin.usr_id NOT NULL REFERENCES twin.users(id),
  role text NOT NULL CHECK (role IN ('owner','editor','viewer')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id,user_id)
);
CREATE INDEX memberships_user_idx ON twin.memberships(user_id,organization_id);
CREATE TABLE twin.projects (
  id twin.prj_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL REFERENCES twin.organizations(id),
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  created_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id,id)
);
CREATE INDEX projects_org_created_idx ON twin.projects(organization_id,created_at,id);
CREATE TABLE twin.models (
  id twin.mdl_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL REFERENCES twin.organizations(id),
  project_id twin.prj_id NOT NULL,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  domain text NOT NULL,
  latest_version_id twin.mv_id,
  created_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id,id),
  UNIQUE (organization_id,project_id,id),
  FOREIGN KEY (organization_id,project_id) REFERENCES twin.projects(organization_id,id)
);
CREATE INDEX models_project_idx ON twin.models(organization_id,project_id,created_at,id);
CREATE TABLE twin.model_versions (
  id twin.mv_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL,
  model_id twin.mdl_id NOT NULL,
  version integer NOT NULL CHECK (version>0),
  schema_version text NOT NULL CHECK (schema_version='0.1'),
  parent_version_id twin.mv_id,
  source_proposal_id twin.aip_id,
  ir jsonb NOT NULL CHECK (jsonb_typeof(ir)='object'),
  hash twin.sha256 NOT NULL,
  dataset_bindings jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(dataset_bindings)='array'),
  created_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id,id),
  UNIQUE (organization_id,model_id,id),
  UNIQUE (organization_id,model_id,version),
  UNIQUE (organization_id,id,hash),
  UNIQUE (organization_id,source_proposal_id),
  FOREIGN KEY (organization_id,model_id) REFERENCES twin.models(organization_id,id),
  FOREIGN KEY (organization_id,model_id,parent_version_id)
    REFERENCES twin.model_versions(organization_id,model_id,id),
  CHECK (coalesce(ir->>'model_id'=model_id::text,false)),
  CHECK (coalesce(ir->>'schema_version'=schema_version,false))
);
ALTER TABLE twin.models ADD CONSTRAINT models_latest_version_fk
  FOREIGN KEY (organization_id,id,latest_version_id)
  REFERENCES twin.model_versions(organization_id,model_id,id)
  DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE twin.model_drafts (
  organization_id twin.org_id NOT NULL,
  model_id twin.mdl_id NOT NULL,
  base_version_id twin.mv_id NOT NULL,
  revision bigint NOT NULL CHECK (revision>0),
  ir jsonb NOT NULL CHECK (jsonb_typeof(ir)='object'),
  hash twin.sha256 NOT NULL,
  updated_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id,model_id),
  FOREIGN KEY (organization_id,model_id) REFERENCES twin.models(organization_id,id),
  FOREIGN KEY (organization_id,model_id,base_version_id)
    REFERENCES twin.model_versions(organization_id,model_id,id),
  CHECK (coalesce(ir->>'model_id'=model_id::text,false))
);
CREATE TABLE twin.datasets (
  id twin.ds_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL,
  project_id twin.prj_id NOT NULL,
  kind text NOT NULL CHECK (kind IN ('canonical_events','model_json','empirical_samples')),
  name text NOT NULL,
  created_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id,id),
  FOREIGN KEY (organization_id,project_id) REFERENCES twin.projects(organization_id,id)
);
CREATE TABLE twin.dataset_versions (
  id twin.dsv_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL,
  dataset_id twin.ds_id NOT NULL,
  version integer NOT NULL CHECK (version>0),
  parent_dataset_version_id twin.dsv_id,
  object_key text NOT NULL,
  content_hash twin.sha256 NOT NULL,
  content_type text NOT NULL,
  row_count bigint NOT NULL CHECK (row_count BETWEEN 0 AND 100000),
  unit twin.time_unit,
  created_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id,id),
  UNIQUE (organization_id,id,content_hash),
  UNIQUE (organization_id,dataset_id,version),
  FOREIGN KEY (organization_id,dataset_id) REFERENCES twin.datasets(organization_id,id),
  FOREIGN KEY (organization_id,parent_dataset_version_id)
    REFERENCES twin.dataset_versions(organization_id,id),
  CHECK (twin.object_key_ok(object_key, 'tenant/'||organization_id||'/'))
);
CREATE TABLE twin.model_version_datasets (
  organization_id twin.org_id NOT NULL,
  model_id twin.mdl_id NOT NULL,
  model_version_id twin.mv_id NOT NULL,
  logical_ref twin.logical_ref NOT NULL,
  dataset_version_id twin.dsv_id NOT NULL,
  content_hash twin.sha256 NOT NULL,
  unit twin.time_unit NOT NULL,
  sample_count integer NOT NULL CHECK (sample_count BETWEEN 1 AND 100000),
  PRIMARY KEY (organization_id,model_version_id,logical_ref),
  FOREIGN KEY (organization_id,model_id,model_version_id)
    REFERENCES twin.model_versions(organization_id,model_id,id),
  FOREIGN KEY (organization_id,dataset_version_id,content_hash)
    REFERENCES twin.dataset_versions(organization_id,id,content_hash)
);
CREATE TABLE twin.imports (
  id twin.imp_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL,
  project_id twin.prj_id NOT NULL,
  model_id twin.mdl_id NOT NULL,
  base_version_id twin.mv_id NOT NULL,
  kind text NOT NULL CHECK (kind IN ('csv_events','json_events','json_model')),
  filename text NOT NULL,
  upload_object_key text NOT NULL,
  upload_content_hash twin.sha256 NOT NULL,
  size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 50000000),
  status text NOT NULL CHECK (status IN ('UPLOADED','PROFILING','AWAITING_MAPPING','VALIDATED','COMMITTED','FAILED')),
  mapping_revision bigint NOT NULL DEFAULT 0 CHECK (mapping_revision>=0),
  mapping jsonb CHECK (mapping IS NULL OR jsonb_typeof(mapping)='object'),
  mapping_suggestion_status text NOT NULL DEFAULT 'none'
    CHECK (mapping_suggestion_status IN ('none','generating','ready','failed')),
  mapping_suggestion jsonb,
  total_rows integer NOT NULL DEFAULT 0 CHECK (total_rows BETWEEN 0 AND 100000),
  invalid_rows integer NOT NULL DEFAULT 0 CHECK (invalid_rows BETWEEN 0 AND 100000),
  preview jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(preview)='array'),
  validation jsonb,
  error jsonb,
  error_report_object_key text,
  dataset_version_id twin.dsv_id,
  proposal_id twin.aip_id,
  created_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id,id),
  UNIQUE (organization_id,model_id,id),
  FOREIGN KEY (organization_id,project_id,model_id)
    REFERENCES twin.models(organization_id,project_id,id),
  FOREIGN KEY (organization_id,model_id,base_version_id)
    REFERENCES twin.model_versions(organization_id,model_id,id),
  FOREIGN KEY (organization_id,dataset_version_id)
    REFERENCES twin.dataset_versions(organization_id,id),
  CHECK (twin.object_key_ok(upload_object_key, 'tenant/'||organization_id||'/imports/')),
  CHECK (error_report_object_key IS NULL OR twin.object_key_ok(error_report_object_key, 'tenant/'||organization_id||'/imports/')),
  CHECK (invalid_rows<=total_rows),
  CHECK (status<>'COMMITTED' OR (dataset_version_id IS NOT NULL AND proposal_id IS NOT NULL AND invalid_rows=0))
);
CREATE TABLE twin.ai_proposals (
  id twin.aip_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL,
  model_id twin.mdl_id NOT NULL,
  base_version_id twin.mv_id NOT NULL,
  source_import_id twin.imp_id,
  mode text NOT NULL CHECK (mode IN ('edit','scenario')),
  status text NOT NULL CHECK (status IN ('generating','needs_input','ready','invalid','failed','approved','rejected','conflicted')),
  intent text NOT NULL,
  summary text,
  assumptions jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(assumptions)='array'),
  patch jsonb CHECK (patch IS NULL OR jsonb_typeof(patch)='array'),
  patch_hash twin.sha256,
  candidate_dataset_bindings jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(candidate_dataset_bindings)='array'),
  expected_effects jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(expected_effects)='array'),
  questions jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(questions)='array'),
  -- D-027: the provider's risk is an untrusted claim; the effective level is
  -- max(server, claim), so a model can raise risk but never lower it.
  server_risk_level text CHECK (server_risk_level IN ('low','medium','high')),
  provider_risk_claim text CHECK (provider_risk_claim IN ('low','medium','high')),
  risk_level text CHECK (risk_level IN ('low','medium','high')),
  confidence double precision CHECK (confidence>=0 AND confidence<=1),
  requires_confirmation boolean NOT NULL DEFAULT true CHECK (requires_confirmation),
  validation jsonb,
  provider_metadata jsonb NOT NULL DEFAULT '{}',
  error jsonb,
  new_version_id twin.mv_id,
  scenario_id twin.scn_id,
  decided_by twin.usr_id REFERENCES twin.users(id),
  decided_at timestamptz,
  created_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id,id),
  UNIQUE (organization_id,model_id,id),
  FOREIGN KEY (organization_id,model_id,base_version_id)
    REFERENCES twin.model_versions(organization_id,model_id,id),
  FOREIGN KEY (organization_id,model_id,source_import_id)
    REFERENCES twin.imports(organization_id,model_id,id),
  FOREIGN KEY (organization_id,model_id,new_version_id)
    REFERENCES twin.model_versions(organization_id,model_id,id),
  CHECK (status NOT IN ('ready','approved') OR coalesce(
    jsonb_array_length(patch)>0 AND validation->>'valid'='true' AND
    risk_level IS NOT NULL AND confidence IS NOT NULL AND patch_hash IS NOT NULL,false)),
  CHECK (status<>'approved' OR
    (decided_by IS NOT NULL AND decided_at IS NOT NULL AND
     ((mode='edit' AND new_version_id IS NOT NULL AND scenario_id IS NULL) OR
      (mode='scenario' AND scenario_id IS NOT NULL AND new_version_id IS NULL)))),
  CHECK (status='approved' OR (new_version_id IS NULL AND scenario_id IS NULL)),
  CHECK (status<>'rejected' OR (decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  CHECK (risk_level IS NULL OR (server_risk_level IS NOT NULL AND provider_risk_claim IS NOT NULL AND
    risk_level = CASE WHEN 'high' IN (server_risk_level, provider_risk_claim) THEN 'high'
                      WHEN 'medium' IN (server_risk_level, provider_risk_claim) THEN 'medium'
                      ELSE 'low' END)),
  CHECK (server_risk_level IS NULL OR risk_level IS NOT NULL)
);
ALTER TABLE twin.model_versions ADD CONSTRAINT versions_source_proposal_fk
  FOREIGN KEY (organization_id,model_id,source_proposal_id)
  REFERENCES twin.ai_proposals(organization_id,model_id,id);
ALTER TABLE twin.imports ADD CONSTRAINT imports_proposal_fk
  FOREIGN KEY (organization_id,model_id,proposal_id)
  REFERENCES twin.ai_proposals(organization_id,model_id,id);
CREATE INDEX ai_proposals_model_idx ON twin.ai_proposals(organization_id,model_id,created_at,id);
CREATE TABLE twin.scenarios (
  id twin.scn_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL,
  model_id twin.mdl_id NOT NULL,
  base_version_id twin.mv_id NOT NULL,
  name text NOT NULL,
  patch jsonb NOT NULL CHECK (jsonb_typeof(patch)='array'),
  patch_hash twin.sha256 NOT NULL,
  source_proposal_id twin.aip_id,
  created_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id,id),
  UNIQUE (organization_id,model_id,id),
  UNIQUE (organization_id,model_id,base_version_id,id),
  UNIQUE (organization_id,source_proposal_id),
  FOREIGN KEY (organization_id,model_id,base_version_id)
    REFERENCES twin.model_versions(organization_id,model_id,id),
  FOREIGN KEY (organization_id,model_id,source_proposal_id)
    REFERENCES twin.ai_proposals(organization_id,model_id,id)
);
ALTER TABLE twin.ai_proposals ADD CONSTRAINT proposals_scenario_fk
  FOREIGN KEY (organization_id,model_id,base_version_id,scenario_id)
  REFERENCES twin.scenarios(organization_id,model_id,base_version_id,id);
CREATE TABLE twin.simulation_runs (
  id twin.sim_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL,
  model_id twin.mdl_id NOT NULL,
  model_version_id twin.mv_id NOT NULL,
  scenario_id twin.scn_id,
  name text NOT NULL,
  scenario_patch jsonb NOT NULL CHECK (jsonb_typeof(scenario_patch)='array'),
  config jsonb NOT NULL CHECK (jsonb_typeof(config)='object'),
  seed bigint NOT NULL CHECK (seed BETWEEN 0 AND 9007199254740991),
  replications integer NOT NULL CHECK (replications BETWEEN 1 AND 1000),
  engine_version text NOT NULL,
  model_hash twin.sha256 NOT NULL,
  effective_model_hash twin.sha256 NOT NULL,
  config_hash twin.sha256 NOT NULL,
  dataset_manifest_hash twin.sha256 NOT NULL,
  dataset_bindings jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(dataset_bindings)='array'),
  event_budget_reserved bigint NOT NULL DEFAULT 0 CHECK (event_budget_reserved>=0),
  execution_deadline_at timestamptz,
  execution_limits jsonb NOT NULL CHECK (jsonb_typeof(execution_limits)='object'),
  status text NOT NULL CHECK (status IN ('queued','running','completed','failed','cancelled')),
  completed_replications integer NOT NULL DEFAULT 0,
  events_processed bigint NOT NULL DEFAULT 0 CHECK (events_processed>=0),
  progress_revision bigint NOT NULL DEFAULT 0 CHECK (progress_revision>=0),
  cancellation_requested boolean NOT NULL DEFAULT false,
  error jsonb,
  created_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz,
  finished_at timestamptz,
  UNIQUE (organization_id,id),
  FOREIGN KEY (organization_id,model_id,model_version_id)
    REFERENCES twin.model_versions(organization_id,model_id,id),
  FOREIGN KEY (organization_id,model_version_id,model_hash)
    REFERENCES twin.model_versions(organization_id,id,hash),
  FOREIGN KEY (organization_id,model_id,model_version_id,scenario_id)
    REFERENCES twin.scenarios(organization_id,model_id,base_version_id,id),
  CHECK (completed_replications BETWEEN 0 AND replications),
  CHECK (event_budget_reserved<=coalesce((execution_limits->>'max_events')::bigint,0)),
  CHECK (status<>'running' OR (started_at IS NOT NULL AND execution_deadline_at IS NOT NULL)),
  CHECK (status NOT IN ('completed','failed','cancelled') OR finished_at IS NOT NULL),
  CHECK (status<>'completed' OR completed_replications=replications)
);
CREATE INDEX simulation_runs_model_idx ON twin.simulation_runs(organization_id,model_id,created_at,id);
CREATE TABLE twin.run_datasets (
  organization_id twin.org_id NOT NULL,
  run_id twin.sim_id NOT NULL,
  logical_ref twin.logical_ref NOT NULL,
  dataset_version_id twin.dsv_id NOT NULL,
  content_hash twin.sha256 NOT NULL,
  unit twin.time_unit NOT NULL,
  sample_count integer NOT NULL CHECK (sample_count BETWEEN 1 AND 100000),
  PRIMARY KEY (organization_id,run_id,logical_ref),
  FOREIGN KEY (organization_id,run_id) REFERENCES twin.simulation_runs(organization_id,id),
  FOREIGN KEY (organization_id,dataset_version_id,content_hash)
    REFERENCES twin.dataset_versions(organization_id,id,content_hash)
);
CREATE TABLE twin.simulation_results (
  run_id twin.sim_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL,
  result jsonb NOT NULL CHECK (jsonb_typeof(result)='object'),
  result_digest twin.sha256 NOT NULL,
  trace_artifact_id twin.art_id,
  trace_object_key text,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id,run_id),
  FOREIGN KEY (organization_id,run_id) REFERENCES twin.simulation_runs(organization_id,id),
  CHECK (coalesce(result->>'run_id'=run_id::text,false)),
  CHECK (coalesce(result->>'status'='completed',false)),
  CHECK (trace_object_key IS NULL OR twin.object_key_ok(trace_object_key, 'tenant/'||organization_id||'/simulation-results/'))
);
CREATE TABLE twin.ai_analyses (
  id twin.ana_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL,
  run_id twin.sim_id NOT NULL,
  baseline_run_id twin.sim_id,
  status text NOT NULL CHECK (status IN ('generating','completed','failed')),
  question text,
  analysis jsonb,
  provider_metadata jsonb NOT NULL DEFAULT '{}',
  error jsonb,
  created_by twin.usr_id NOT NULL REFERENCES twin.users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id,id),
  FOREIGN KEY (organization_id,run_id) REFERENCES twin.simulation_runs(organization_id,id),
  FOREIGN KEY (organization_id,baseline_run_id) REFERENCES twin.simulation_runs(organization_id,id),
  CHECK (status<>'completed' OR analysis IS NOT NULL)
);
CREATE TABLE twin.jobs (
  id twin.job_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL REFERENCES twin.organizations(id),
  type text NOT NULL CHECK (type IN ('simulation','model_proposal','import_profile','import_validate','import_mapping','analysis')),
  status text NOT NULL CHECK (status IN ('queued','running','completed','failed','cancelled')),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object'),
  simulation_run_id twin.sim_id,
  ai_proposal_id twin.aip_id,
  import_id twin.imp_id,
  ai_analysis_id twin.ana_id,
  priority integer NOT NULL DEFAULT 0,
  attempt integer NOT NULL DEFAULT 0 CHECK (attempt>=0),
  max_attempts integer NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 10),
  available_at timestamptz NOT NULL DEFAULT now(),
  locked_at timestamptz,
  locked_by text,
  lease_token bigint NOT NULL DEFAULT 0 CHECK (lease_token>=0),
  lease_expires_at timestamptz,
  heartbeat_at timestamptz,
  error jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id,id),
  FOREIGN KEY (organization_id,simulation_run_id) REFERENCES twin.simulation_runs(organization_id,id),
  FOREIGN KEY (organization_id,ai_proposal_id) REFERENCES twin.ai_proposals(organization_id,id),
  FOREIGN KEY (organization_id,import_id) REFERENCES twin.imports(organization_id,id),
  FOREIGN KEY (organization_id,ai_analysis_id) REFERENCES twin.ai_analyses(organization_id,id),
  CHECK (num_nonnulls(simulation_run_id,ai_proposal_id,import_id,ai_analysis_id)=1),
  CHECK ((type='simulation' AND simulation_run_id IS NOT NULL) OR
         (type='model_proposal' AND ai_proposal_id IS NOT NULL) OR
         (type IN ('import_profile','import_validate','import_mapping') AND import_id IS NOT NULL) OR
         (type='analysis' AND ai_analysis_id IS NOT NULL)),
  CHECK (status<>'running' OR (locked_at IS NOT NULL AND locked_by IS NOT NULL AND lease_expires_at IS NOT NULL))
);
CREATE INDEX jobs_claim_idx ON twin.jobs(organization_id,priority DESC,created_at,id)
  WHERE status='queued';
CREATE INDEX jobs_lease_idx ON twin.jobs(organization_id,lease_expires_at) WHERE status='running';
CREATE UNIQUE INDEX jobs_one_simulation ON twin.jobs(organization_id,simulation_run_id) WHERE simulation_run_id IS NOT NULL;
CREATE UNIQUE INDEX jobs_one_proposal ON twin.jobs(organization_id,ai_proposal_id) WHERE ai_proposal_id IS NOT NULL;
CREATE UNIQUE INDEX jobs_one_analysis ON twin.jobs(organization_id,ai_analysis_id) WHERE ai_analysis_id IS NOT NULL;
CREATE TABLE twin.idempotency_keys (
  organization_id twin.org_id NOT NULL REFERENCES twin.organizations(id),
  actor_id twin.usr_id NOT NULL REFERENCES twin.users(id),
  operation_scope text NOT NULL,
  key text NOT NULL CHECK (length(key) BETWEEN 8 AND 128),
  request_hash twin.sha256 NOT NULL,
  response_status integer CHECK (response_status BETWEEN 200 AND 599),
  response_body jsonb,
  completed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (organization_id,actor_id,operation_scope,key),
  CHECK (expires_at>created_at),
  CHECK ((completed_at IS NULL AND response_status IS NULL AND response_body IS NULL) OR
         (completed_at IS NOT NULL AND response_status IS NOT NULL AND response_body IS NOT NULL))
);
CREATE TABLE twin.audit_events (
  id twin.aud_id PRIMARY KEY,
  organization_id twin.org_id NOT NULL REFERENCES twin.organizations(id),
  actor_type text NOT NULL CHECK (actor_type IN ('user','worker','system')),
  actor_id twin.actor_ref NOT NULL,
  action text NOT NULL,
  target_type text NOT NULL,
  target_id twin.public_id NOT NULL,
  request_id twin.req_id NOT NULL,
  metadata jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(metadata)='object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (actor_type<>'user' OR actor_id ~ '^usr_[0-7][0-9A-HJKMNP-TV-Z]{25}$')
);
CREATE INDEX audit_tenant_created_idx ON twin.audit_events(organization_id,created_at DESC,id);
CREATE INDEX audit_request_idx ON twin.audit_events(organization_id,request_id);

CREATE FUNCTION twin.reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% is immutable; create a new record', TG_TABLE_NAME USING ERRCODE='55000';
END $$;
-- TRUNCATE skips row triggers and RLS, so it gets its own statement trigger.
CREATE FUNCTION twin.reject_truncate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% is immutable; TRUNCATE is not allowed', TG_TABLE_NAME USING ERRCODE='55000';
END $$;
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['model_versions','scenarios','dataset_versions','model_version_datasets','run_datasets','simulation_results','audit_events'] LOOP
    EXECUTE format('CREATE TRIGGER immutable_record BEFORE UPDATE OR DELETE ON twin.%I FOR EACH ROW EXECUTE FUNCTION twin.reject_mutation()',t);
    EXECUTE format('CREATE TRIGGER immutable_table BEFORE TRUNCATE ON twin.%I FOR EACH STATEMENT EXECUTE FUNCTION twin.reject_truncate()',t);
  END LOOP;
END $$;
CREATE FUNCTION twin.protect_run_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF ROW(NEW.organization_id,NEW.model_id,NEW.model_version_id,NEW.scenario_id,
         NEW.scenario_patch,NEW.config,NEW.seed,NEW.replications,NEW.engine_version,
         NEW.model_hash,NEW.effective_model_hash,NEW.config_hash,NEW.dataset_manifest_hash,NEW.dataset_bindings,NEW.execution_limits)
     IS DISTINCT FROM
     ROW(OLD.organization_id,OLD.model_id,OLD.model_version_id,OLD.scenario_id,
         OLD.scenario_patch,OLD.config,OLD.seed,OLD.replications,OLD.engine_version,
         OLD.model_hash,OLD.effective_model_hash,OLD.config_hash,OLD.dataset_manifest_hash,OLD.dataset_bindings,OLD.execution_limits) THEN
    RAISE EXCEPTION 'simulation inputs are immutable' USING ERRCODE='55000';
  END IF;
  IF OLD.execution_deadline_at IS NOT NULL AND NEW.execution_deadline_at IS DISTINCT FROM OLD.execution_deadline_at THEN
    RAISE EXCEPTION 'execution deadline cannot be reset by a retry' USING ERRCODE='55000';
  END IF;
  IF OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at THEN
    RAISE EXCEPTION 'first execution start cannot be reset' USING ERRCODE='55000';
  END IF;
  IF NEW.event_budget_reserved<OLD.event_budget_reserved THEN
    RAISE EXCEPTION 'reserved event budget cannot decrease' USING ERRCODE='55000';
  END IF;
  IF OLD.status IN ('completed','failed','cancelled') AND NEW IS DISTINCT FROM OLD THEN
    RAISE EXCEPTION 'terminal run is immutable' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER immutable_run_input BEFORE UPDATE ON twin.simulation_runs
  FOR EACH ROW EXECUTE FUNCTION twin.protect_run_inputs();
CREATE FUNCTION twin.protect_proposal_content() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER protect_proposal BEFORE UPDATE ON twin.ai_proposals
  FOR EACH ROW EXECUTE FUNCTION twin.protect_proposal_content();
-- Whole-manifest immutability: immutable children alone would still allow late INSERTs.
-- Parent frozen JSON is authoritative; deferred checks require exact relational bindings
-- at COMMIT and reject later additions. Manifests are sorted by the bytes of
-- logical_ref (D-034): COLLATE "C", whatever the database default collation is.
CREATE FUNCTION twin.verify_version_dataset_manifest() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE oid text; vid text; expected jsonb; actual jsonb;
BEGIN
  oid:=NEW.organization_id::text;
  IF TG_TABLE_NAME='model_versions' THEN vid:=NEW.id::text;
  ELSE vid:=NEW.model_version_id::text; END IF;
  SELECT dataset_bindings INTO expected FROM twin.model_versions
    WHERE organization_id::text=oid AND id::text=vid;
  SELECT coalesce(jsonb_agg(jsonb_build_object(
    'logical_ref',logical_ref,'dataset_version_id',dataset_version_id,
    'content_hash',content_hash,'unit',unit,'sample_count',sample_count
  ) ORDER BY logical_ref COLLATE "C"),'[]'::jsonb) INTO actual FROM twin.model_version_datasets
    WHERE organization_id::text=oid AND model_version_id::text=vid;
  IF expected IS NULL OR actual IS DISTINCT FROM expected THEN
    RAISE EXCEPTION 'model version dataset manifest mismatch' USING ERRCODE='23514';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER version_dataset_manifest_at_commit
  AFTER INSERT ON twin.model_versions DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION twin.verify_version_dataset_manifest();
CREATE CONSTRAINT TRIGGER version_dataset_binding_at_commit
  AFTER INSERT ON twin.model_version_datasets DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION twin.verify_version_dataset_manifest();
CREATE FUNCTION twin.verify_run_dataset_manifest() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE oid text; rid text; expected jsonb; actual jsonb;
BEGIN
  oid:=NEW.organization_id::text;
  IF TG_TABLE_NAME='simulation_runs' THEN rid:=NEW.id::text;
  ELSE rid:=NEW.run_id::text; END IF;
  SELECT dataset_bindings INTO expected FROM twin.simulation_runs
    WHERE organization_id::text=oid AND id::text=rid;
  SELECT coalesce(jsonb_agg(jsonb_build_object(
    'logical_ref',logical_ref,'dataset_version_id',dataset_version_id,
    'content_hash',content_hash,'unit',unit,'sample_count',sample_count
  ) ORDER BY logical_ref COLLATE "C"),'[]'::jsonb) INTO actual FROM twin.run_datasets
    WHERE organization_id::text=oid AND run_id::text=rid;
  IF expected IS NULL OR actual IS DISTINCT FROM expected THEN
    RAISE EXCEPTION 'run dataset manifest mismatch' USING ERRCODE='23514';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER run_dataset_manifest_at_commit
  AFTER INSERT ON twin.simulation_runs DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION twin.verify_run_dataset_manifest();
CREATE CONSTRAINT TRIGGER run_dataset_binding_at_commit
  AFTER INSERT ON twin.run_datasets DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION twin.verify_run_dataset_manifest();

-- Every function resolves names in pg_catalog only (pg_temp last), so an
-- object created in another schema can never shadow what it calls; twin
-- objects are always schema-qualified.
DO $$
DECLARE f regprocedure;
BEGIN
  FOR f IN SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='twin' LOOP
    EXECUTE format('ALTER FUNCTION %s SET search_path = pg_catalog, pg_temp', f);
  END LOOP;
END $$;
COMMIT;
