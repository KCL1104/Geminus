-- D-042 (approved, H-009): provisioning acts as twin_provisioner, not as the
-- schema owner. It creates users, organizations, their first owner
-- membership and the system audit event, and reads users to find one;
-- nothing else (no UPDATE, DELETE, DDL or policy change). RLS applies to it
-- as to every role (FORCE ROW LEVEL SECURITY), but RLS reads a scope the
-- role sets itself: a guard lets it add members and audit events only to
-- the organization its own transaction created (T-010 review 2, L1).
-- Prerequisite: db/bootstrap_roles.sql creates the role.
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
-- Without this, the grants above would let provisioning set another
-- organization's scope and make anyone its owner, or write system events
-- into its log. The organization's row has this transaction's ID as its
-- xmin only if this transaction inserted it (provisioning has no UPDATE,
-- so it cannot rewrite one; a row inserted under a savepoint has the
-- subtransaction's ID and is refused, and provisioning uses none).
CREATE FUNCTION twin.created_in_this_transaction(org text) RETURNS boolean LANGUAGE sql
  SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM twin.organizations o
                 WHERE o.id::text = org AND o.xmin = pg_catalog.pg_current_xact_id()::xid)
$$;
REVOKE ALL ON FUNCTION twin.created_in_this_transaction(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION twin.created_in_this_transaction(text) TO twin_provisioner;
CREATE FUNCTION twin.guard_provisioner_target() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  -- Nested: EXECUTE is checked when an expression is prepared, and only
  -- provisioning may call the function.
  IF pg_catalog.pg_has_role(current_user,'twin_provisioner','USAGE')
     AND NOT pg_catalog.pg_has_role(current_user,'twin_owner','USAGE') THEN
    IF NOT twin.created_in_this_transaction(NEW.organization_id::text) THEN
      RAISE EXCEPTION 'provisioning adds members and audit events only to the organization it creates'
        USING ERRCODE='42501';
    END IF;
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.guard_provisioner_target() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER guard_provisioner_target BEFORE INSERT ON twin.memberships
  FOR EACH ROW EXECUTE FUNCTION twin.guard_provisioner_target();
CREATE TRIGGER guard_provisioner_target BEFORE INSERT ON twin.audit_events
  FOR EACH ROW EXECUTE FUNCTION twin.guard_provisioner_target();
COMMIT;
