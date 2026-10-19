-- DEVELOPMENT-ONLY DOWN MIGRATION of 0004: removes the provisioning guard,
-- restores 0002's audit guard and removes twin_provisioner's privileges (the role itself is cluster-wide and
-- stays; db/bootstrap_roles.sql owns it).
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER guard_provisioner_target ON twin.memberships;
DROP TRIGGER guard_provisioner_target ON twin.audit_events;
DROP FUNCTION twin.guard_provisioner_target();
DROP FUNCTION twin.created_in_this_transaction(text);
CREATE OR REPLACE FUNCTION twin.guard_audit_actor() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF NEW.actor_type='user' AND NOT pg_catalog.pg_has_role(current_user,'twin_owner','USAGE')
     AND (pg_catalog.pg_has_role(current_user,'twin_worker','USAGE')
          OR NEW.actor_id::text IS DISTINCT FROM twin.current_actor_id()) THEN
    RAISE EXCEPTION 'a user audit event names the scoped user' USING ERRCODE='42501';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON ALL TABLES IN SCHEMA twin FROM twin_provisioner;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA twin FROM twin_provisioner;
DO $$
DECLARE d text;
BEGIN
  FOR d IN SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
           WHERE n.nspname='twin' AND t.typtype='d' ORDER BY t.typname LOOP
    EXECUTE format('REVOKE USAGE ON DOMAIN twin.%I FROM twin_provisioner', d);
  END LOOP;
END $$;
REVOKE USAGE ON SCHEMA twin FROM twin_provisioner;
COMMIT;
