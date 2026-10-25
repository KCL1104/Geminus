-- T-028 (REQ-010, AI_SPEC §7, API_DATABASE §4, §8; D-004, D-021, D-067
-- APPROVED; D-080 PROPOSED; TEST-008, TEST-010): people save scenarios
-- through the API, and every scenario stays one a person, or a person's
-- approval, created, for every role.
--
--   * A scenario's name is 1 to 200 characters, as scenario.schema.json
--     says: a person's name as given, or the proposal summary an approval
--     names it after (D-067).
--   * The API creates a scenario for the scoped user (created_by is
--     app.user_id, 42501, as 0012 holds a proposal's creator) and at the
--     time of its transaction (created_at is now(), 55000), so the list's
--     order and its cursor (created_at, then ID) are the server's, never a
--     request's. Every role but the schema owner is held to this (the API
--     is the only other role with INSERT, 0002); the owner (operators, the
--     SQL tests) is not.
--   * listScenarios reads a version's scenarios in (created_at, id) order;
--     an index serves it.
--
-- Unchanged: 0001 makes a scenario immutable for every role (no UPDATE,
-- DELETE or TRUNCATE) and its keys composite (the base is a version of the
-- same organization and model; the source proposal is one of the same
-- model); 0002 enables and forces RLS and grants the API SELECT and INSERT
-- only and the worker SELECT only; 0013 makes a scenario from a proposal
-- that approved proposal's own effect. What SQL cannot check stays the API
-- transaction's (API_DATABASE §8): that the patch is a scenario patch
-- (D-021) that applies to the base and leaves a valid model, and that
-- patch_hash is its D-019 hash.
BEGIN;
SET LOCAL ROLE twin_owner;

ALTER TABLE twin.scenarios
  ADD CONSTRAINT scenarios_name_length CHECK (char_length(name) BETWEEN 1 AND 200);

CREATE INDEX scenarios_base_idx ON twin.scenarios(organization_id, base_version_id, created_at, id);

CREATE FUNCTION twin.protect_scenario_creation() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE') THEN
    RETURN NEW;
  END IF;
  IF NEW.created_by::text IS DISTINCT FROM nullif(current_setting('app.user_id', true), '') THEN
    RAISE EXCEPTION 'a scenario is created for the scoped user' USING ERRCODE = '42501';
  END IF;
  IF NEW.created_at IS DISTINCT FROM now() THEN
    RAISE EXCEPTION 'a scenario is created at the time of its transaction' USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_scenario_creation() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER protect_scenario_creation BEFORE INSERT ON twin.scenarios
  FOR EACH ROW EXECUTE FUNCTION twin.protect_scenario_creation();

COMMIT;
