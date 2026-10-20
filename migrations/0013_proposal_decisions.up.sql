-- T-026 (AI_SPEC §6, API_DATABASE §4, D-003, D-004, D-030; D-068, D-069
-- PROPOSED; TEST-006, TEST-007): a person decides a proposal, and an
-- approval has exactly one effect, the approved proposal's own, for every
-- role.
--
--   * A decision keeps the person's comment (Approve.comment,
--     Reject.comment): decision_comment, 1 to 2000 characters as given,
--     null when there was none. Only an approved or rejected proposal has
--     one; the decision writes it, since a decided proposal never changes
--     (0001's protect_proposal). The worker cannot name the column (its
--     column grants, 0002); the API's table-level UPDATE covers it.
--   * A conflicted proposal names the person whose approval found that its
--     base had moved, and when, as approved and rejected ones name who
--     decided them (0001 holds decided_by to the scoped user and sets
--     decided_at to the database's time).
--   * A version or a scenario that names a source proposal is that
--     proposal's approved effect, checked when the transaction commits:
--     the proposal is approved, its mode is edit (for a version) or
--     scenario (for a scenario), and it names this version or scenario.
--     (0001 checks the other way: an approved proposal names an effect that
--     names it; 0001's unique source_proposal_id leaves one of each kind at
--     most.) A version from a proposal is the next version after the
--     proposal's base: its parent is the base and its number the base's
--     plus one, so with 0001's unique (model, version) an approval whose base
--     is no longer the model's latest version cannot commit. A scenario from
--     a proposal is on the proposal's base with the proposal's patch and
--     patch hash.
--
-- What SQL cannot check stays the API transaction's (API_DATABASE §8):
-- that the version's IR is the base with the patch applied and valid, the
-- risk, and that a person asked (the role is the API's to check; the worker
-- has no INSERT on versions or scenarios and touches no ready proposal,
-- 0002). No table is added, so RLS, tenant keys and grants are 0002's.
BEGIN;
SET LOCAL ROLE twin_owner;

ALTER TABLE twin.ai_proposals
  ADD COLUMN decision_comment text,
  ADD CONSTRAINT ai_proposals_decision_comment CHECK (
    decision_comment IS NULL OR (char_length(decision_comment) BETWEEN 1 AND 2000 AND status IN ('approved', 'rejected'))),
  ADD CONSTRAINT ai_proposals_conflicted_decider CHECK (status <> 'conflicted' OR (decided_by IS NOT NULL AND decided_at IS NOT NULL));

-- Runs with the privileges of whoever inserts the version or scenario (the
-- API, or the schema owner): both read proposals and versions.
CREATE FUNCTION twin.check_proposal_effect() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  p record;
  base_number integer;
BEGIN
  SELECT status, mode, base_version_id, new_version_id, scenario_id, patch, patch_hash INTO p FROM twin.ai_proposals
    WHERE organization_id = NEW.organization_id AND model_id = NEW.model_id AND id = NEW.source_proposal_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'the source proposal is not visible to its organization' USING ERRCODE = '23514';
  END IF;
  IF TG_TABLE_NAME = 'model_versions' THEN
    SELECT version INTO base_number FROM twin.model_versions
      WHERE organization_id = NEW.organization_id AND model_id = NEW.model_id AND id = p.base_version_id;
    IF NOT coalesce(p.status = 'approved' AND p.mode = 'edit' AND p.new_version_id::text = NEW.id::text
                    AND NEW.parent_version_id::text = p.base_version_id::text AND NEW.version = base_number + 1, false) THEN
      RAISE EXCEPTION 'a version from a proposal is the approved edit proposal''s one effect: the next version after its base'
        USING ERRCODE = '23514';
    END IF;
  ELSE
    IF NOT coalesce(p.status = 'approved' AND p.mode = 'scenario' AND p.scenario_id::text = NEW.id::text
                    AND NEW.base_version_id::text = p.base_version_id::text AND NEW.patch = p.patch
                    AND NEW.patch_hash::text = p.patch_hash::text, false) THEN
      RAISE EXCEPTION 'a scenario from a proposal is the approved scenario proposal''s one effect: its base, patch and patch hash'
        USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_proposal_effect() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE CONSTRAINT TRIGGER version_is_its_proposals_effect AFTER INSERT ON twin.model_versions
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW WHEN (NEW.source_proposal_id IS NOT NULL)
  EXECUTE FUNCTION twin.check_proposal_effect();
CREATE CONSTRAINT TRIGGER scenario_is_its_proposals_effect AFTER INSERT ON twin.scenarios
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW WHEN (NEW.source_proposal_id IS NOT NULL)
  EXECUTE FUNCTION twin.check_proposal_effect();
COMMIT;
