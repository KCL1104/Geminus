-- Disposable-database smoke suite. NOT executed during spec generation.
-- Role switching checks policies/privileges, not actual production login configuration.
-- All fixtures are synthetic; hashes below are shape placeholders.
BEGIN;
SET LOCAL ROLE twin_owner;
SELECT set_config('app.oidc_issuer','https://test.invalid',true),
       set_config('app.oidc_subject','subject_a',true),
       set_config('app.organization_id','org_test_a',true),
       set_config('app.user_id','usr_test_a',true);
INSERT INTO twin.users(id,oidc_issuer,oidc_subject) VALUES ('usr_test_a','https://test.invalid','subject_a');
INSERT INTO twin.organizations(id,organization_id,name) VALUES ('org_test_a','org_test_a','Tenant A');
INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ('org_test_a','usr_test_a','owner');
INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ('prj_test_a','org_test_a','Project A','usr_test_a');
SELECT set_config('app.oidc_subject','subject_b',true),
       set_config('app.organization_id','org_test_b',true),
       set_config('app.user_id','usr_test_b',true);
INSERT INTO twin.users(id,oidc_issuer,oidc_subject) VALUES ('usr_test_b','https://test.invalid','subject_b');
INSERT INTO twin.organizations(id,organization_id,name) VALUES ('org_test_b','org_test_b','Tenant B');
INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ('org_test_b','usr_test_b','owner');
INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ('prj_test_b','org_test_b','Project B','usr_test_b');

SET LOCAL ROLE twin_api;
SELECT set_config('app.oidc_subject','subject_a',true),
       set_config('app.organization_id','org_test_a',true),
       set_config('app.user_id','usr_test_a',true);
DO $$
BEGIN
  IF (SELECT count(*) FROM twin.projects)<>1 THEN RAISE EXCEPTION 'unscoped SELECT leaked projects'; END IF;
  IF EXISTS(SELECT 1 FROM twin.projects WHERE id='prj_test_b') THEN RAISE EXCEPTION 'cross-tenant read'; END IF;
  IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname=current_user AND (rolsuper OR rolbypassrls)) THEN
    RAISE EXCEPTION 'unsafe runtime role';
  END IF;
  IF pg_has_role('twin_api','twin_owner','MEMBER') OR pg_has_role('twin_worker','twin_owner','MEMBER') THEN
    RAISE EXCEPTION 'runtime may inherit owner';
  END IF;
  BEGIN
    INSERT INTO twin.projects(id,organization_id,name,created_by)
      VALUES ('prj_illegal','org_test_b','Illegal','usr_test_a');
    RAISE EXCEPTION 'cross-tenant INSERT unexpectedly succeeded';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  BEGIN
    INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by)
      VALUES ('mdl_illegal','org_test_a','prj_test_b','Illegal FK','software_delivery','usr_test_a');
    RAISE EXCEPTION 'cross-tenant parent FK unexpectedly succeeded';
  EXCEPTION WHEN foreign_key_violation THEN NULL; END;
END $$;
INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by)
 VALUES ('mdl_test_a','org_test_a','prj_test_a','Test Model','software_delivery','usr_test_a');
INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,ir,hash,created_by)
 VALUES ('mv_test_a','org_test_a','mdl_test_a',1,'0.1',
 '{"schema_version":"0.1","model_id":"mdl_test_a","domain":"software_delivery","name":"Test Model","time_unit":"minute","entity_types":[{"id":"change","name":"Change"}],"nodes":[{"id":"arrival","type":"source","entity_type":"change","arrival":{"distribution":{"type":"constant","value":10}}},{"id":"done","type":"sink"}],"resources":[],"flows":[{"id":"flow","from":"arrival","to":"done","routing":{"type":"probability","probability":1}}],"constraints":[],"metrics":[],"views":{"2d":{},"3d":{}}}',
 'sha256:0000000000000000000000000000000000000000000000000000000000000000','usr_test_a');
UPDATE twin.models SET latest_version_id='mv_test_a' WHERE organization_id='org_test_a' AND id='mdl_test_a';
-- Force deferred FK/manifest checks while still in the matching tenant context.
SET CONSTRAINTS ALL IMMEDIATE;
SET CONSTRAINTS ALL DEFERRED;
DO $$
BEGIN
  IF has_table_privilege('twin_api','twin.model_versions','UPDATE') THEN
    RAISE EXCEPTION 'API may update immutable version';
  END IF;
  IF has_table_privilege('twin_worker','twin.model_versions','INSERT') OR
     has_table_privilege('twin_worker','twin.scenarios','INSERT') THEN
    RAISE EXCEPTION 'worker may publish canonical effect';
  END IF;
END $$;
SET LOCAL ROLE twin_owner;
DO $$
BEGIN
  BEGIN
    UPDATE twin.model_versions SET ir=ir||'{"name":"tampered"}'::jsonb
      WHERE organization_id='org_test_a' AND id='mv_test_a';
    RAISE EXCEPTION 'immutable trigger missing';
  EXCEPTION WHEN SQLSTATE '55000' THEN NULL; END;
END $$;
SET LOCAL ROLE twin_api;
SELECT set_config('app.organization_id','',true);
DO $$
BEGIN
  IF EXISTS(SELECT 1 FROM twin.projects) THEN RAISE EXCEPTION 'no-context read should return no rows'; END IF;
END $$;
ROLLBACK;
-- Pool reuse, true runtime-login SET ROLE rejection, concurrent claim/approval,
-- object storage and API role tests still require the application integration suite.
