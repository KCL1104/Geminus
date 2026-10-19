-- D-046 (approved, H-010; T-024): every AI proposal keeps the ID
-- allocation pool the server generated for it when it was created
-- (common.schema.json#/$defs/IdAllocationPool): 16 IDs for each of node,
-- flow and resource, written "<kind>_<lowercase ULID>", all 48 distinct.
-- The provider context lists the pool, and validation and approval (T-025,
-- T-026) accept a new node, flow or resource ID only from the list of its
-- own kind. The pool is written once, with the proposal, and never changes,
-- for any role, the schema owner included: a reviewer approves a patch that
-- was validated against exactly this pool. The worker has no UPDATE grant on
-- the column (0002's column grants do not name it), the API's table-level
-- INSERT and UPDATE grants cover it and the trigger below refuses the change.
--
-- A proposal created before this migration has no pool, and the server
-- cannot make one up for it afterwards (its patch may already use IDs no
-- pool holds). The migration therefore stops on a database that has
-- proposals and changes nothing; none exist before T-025 stores them.
-- 0010 (T-016, imports) does not touch ai_proposals, so the check is the
-- same whether this runs right after it or after 0009.
BEGIN;
SET LOCAL ROLE twin_owner;
-- FORCE ROW LEVEL SECURITY hides every row from the owner too; look
-- without it, inside this transaction only (as 0005 does).
ALTER TABLE twin.ai_proposals NO FORCE ROW LEVEL SECURITY;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM twin.ai_proposals) THEN
    RAISE EXCEPTION 'migration 0011 stores the ID allocation pool every proposal is created with (D-046): run it on a database without proposals'
      USING ERRCODE = '55000';
  END IF;
END $$;
ALTER TABLE twin.ai_proposals FORCE ROW LEVEL SECURITY;

-- The shape of common.schema.json#/$defs/IdAllocationPool, checked
-- without raising for any input: an object with exactly the members node,
-- flow and resource, each an array of 16 strings "<kind>_" + a lowercase
-- ULID (first character 0-7, Crockford base32), no string twice.
CREATE FUNCTION twin.id_pool_ok(pool jsonb) RETURNS boolean
  LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  k text;
  e jsonb;
  seen text[] := '{}';
BEGIN
  IF jsonb_typeof(pool) <> 'object' THEN
    RETURN false;
  END IF;
  IF (SELECT array_agg(x ORDER BY x COLLATE "C") FROM jsonb_object_keys(pool) AS x)
     IS DISTINCT FROM ARRAY['flow','node','resource'] THEN
    RETURN false;
  END IF;
  FOREACH k IN ARRAY ARRAY['node','flow','resource'] LOOP
    IF jsonb_typeof(pool->k) <> 'array' OR jsonb_array_length(pool->k) <> 16 THEN
      RETURN false;
    END IF;
    FOR e IN SELECT v FROM jsonb_array_elements(pool->k) AS v LOOP
      IF jsonb_typeof(e) <> 'string'
         OR (e #>> '{}') !~ ('^' || k || '_[0-7][0-9a-hjkmnp-tv-z]{25}$')
         OR (e #>> '{}') = ANY (seen) THEN
        RETURN false;
      END IF;
      seen := seen || (e #>> '{}');
    END LOOP;
  END LOOP;
  RETURN true;
END $$;
-- A CHECK runs its function as the role writing the row (as 0002 does for
-- object_key_ok): the API inserts proposals, the worker updates them.
REVOKE ALL ON FUNCTION twin.id_pool_ok(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION twin.id_pool_ok(jsonb) TO twin_api, twin_worker;

ALTER TABLE twin.ai_proposals ADD COLUMN id_pool jsonb NOT NULL
  CONSTRAINT ai_proposals_id_pool_shape CHECK (twin.id_pool_ok(id_pool));

CREATE FUNCTION twin.protect_proposal_id_pool() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF NEW.id_pool IS DISTINCT FROM OLD.id_pool THEN
    RAISE EXCEPTION 'a proposal''s ID allocation pool is written once, with the proposal (D-046)' USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_proposal_id_pool() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER protect_proposal_id_pool BEFORE UPDATE ON twin.ai_proposals
  FOR EACH ROW EXECUTE FUNCTION twin.protect_proposal_id_pool();
COMMIT;
