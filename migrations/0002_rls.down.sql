-- DEVELOPMENT-ONLY SECURITY-REMOVING DOWN MIGRATION.
-- Never run while API/worker are live. Prefer forward fixes in production.
BEGIN;
SET LOCAL ROLE twin_owner;
REVOKE ALL ON ALL TABLES IN SCHEMA twin FROM twin_api,twin_worker;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA twin FROM twin_api,twin_worker;
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
DROP TRIGGER IF EXISTS guard_worker_import ON twin.imports;
DROP FUNCTION IF EXISTS twin.guard_worker_import();
DROP FUNCTION twin.current_actor_id();
DROP FUNCTION twin.current_organization_id();
COMMIT;
