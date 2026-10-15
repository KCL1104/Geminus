-- DEVELOPMENT-ONLY SECURITY-REMOVING DOWN MIGRATION.
-- Never run while API/worker are live. Prefer forward fixes in production.
BEGIN;
SET LOCAL ROLE twin_owner;
REVOKE ALL ON ALL TABLES IN SCHEMA twin FROM twin_api,twin_worker;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA twin FROM twin_api,twin_worker;
DO $$
DECLARE d text;
BEGIN
  FOR d IN SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
           WHERE n.nspname='twin' AND t.typtype='d' ORDER BY t.typname LOOP
    EXECUTE format('REVOKE USAGE ON DOMAIN twin.%I FROM twin_api,twin_worker', d);
  END LOOP;
END $$;
REVOKE USAGE ON SCHEMA twin FROM twin_api,twin_worker;
DO $$
DECLARE t text;
BEGIN
  FOR t IN SELECT tablename FROM pg_tables WHERE schemaname='twin' LOOP
    EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON twin.%I',t);
    EXECUTE format('DROP POLICY IF EXISTS identity_self ON twin.%I',t);
    EXECUTE format('ALTER TABLE twin.%I NO FORCE ROW LEVEL SECURITY',t);
    EXECUTE format('ALTER TABLE twin.%I DISABLE ROW LEVEL SECURITY',t);
  END LOOP;
END $$;
DROP TRIGGER IF EXISTS guard_audit_actor ON twin.audit_events;
DROP FUNCTION IF EXISTS twin.guard_audit_actor();
DROP TRIGGER IF EXISTS guard_worker_proposal ON twin.ai_proposals;
DROP FUNCTION IF EXISTS twin.guard_worker_proposal();
DROP TRIGGER IF EXISTS guard_worker_import ON twin.imports;
DROP FUNCTION IF EXISTS twin.guard_worker_import();
DROP FUNCTION twin.current_actor_id();
DROP FUNCTION twin.current_organization_id();
COMMIT;
