-- D-042 (approved, H-009): provisioning acts as twin_provisioner, not as the
-- schema owner. It creates users, organizations, their first owner
-- membership and the system audit event, and reads users to find one;
-- nothing else (no UPDATE, DELETE, DDL or policy change). RLS applies to it
-- as to every role (FORCE ROW LEVEL SECURITY). Prerequisite:
-- db/bootstrap_roles.sql creates the role.
-- Found while doing D-042: guard_audit_actor checked only "user" events, so
-- the API could write an event as the worker or the system. Each role now
-- writes only as itself: the API as the scoped user, the worker as the
-- worker or the system (never as a person), provisioning as the system.
BEGIN;
SET LOCAL ROLE twin_owner;
GRANT USAGE ON SCHEMA twin TO twin_provisioner;
DO $$
DECLARE d text;
BEGIN
  FOR d IN SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
           WHERE n.nspname='twin' AND t.typtype='d' ORDER BY t.typname LOOP
    EXECUTE format('GRANT USAGE ON TYPE twin.%I TO twin_provisioner',d);
  END LOOP;
END $$;
GRANT EXECUTE ON FUNCTION twin.current_organization_id(),twin.current_actor_id() TO twin_provisioner;
GRANT SELECT, INSERT ON twin.users TO twin_provisioner;
GRANT INSERT ON twin.organizations,twin.memberships,twin.audit_events TO twin_provisioner;
CREATE OR REPLACE FUNCTION twin.guard_audit_actor() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  worker boolean := pg_catalog.pg_has_role(current_user,'twin_worker','USAGE');
  provisioner boolean := pg_catalog.pg_has_role(current_user,'twin_provisioner','USAGE');
BEGIN
  IF pg_catalog.pg_has_role(current_user,'twin_owner','USAGE') THEN
    RETURN NEW;
  END IF;
  IF NEW.actor_type='user' AND (worker OR provisioner OR NEW.actor_id::text IS DISTINCT FROM twin.current_actor_id()) THEN
    RAISE EXCEPTION 'a user audit event names the scoped user' USING ERRCODE='42501';
  END IF;
  IF (NEW.actor_type='worker' AND NOT worker) OR (NEW.actor_type='system' AND NOT (worker OR provisioner)) THEN
    RAISE EXCEPTION 'a role writes audit events only as itself' USING ERRCODE='42501';
  END IF;
  RETURN NEW;
END $$;
COMMIT;
