-- DEVELOPMENT-ONLY DOWN MIGRATION of 0013: versions and scenarios that
-- name a source proposal are no longer checked against it at commit, a
-- conflicted proposal need not name who found the conflict, and decisions
-- lose their comments (dropped with their values).
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER scenario_is_its_proposals_effect ON twin.scenarios;
DROP TRIGGER version_is_its_proposals_effect ON twin.model_versions;
DROP FUNCTION twin.check_proposal_effect();
ALTER TABLE twin.ai_proposals
  DROP CONSTRAINT ai_proposals_conflicted_decider,
  DROP CONSTRAINT ai_proposals_decision_comment,
  DROP COLUMN decision_comment;
COMMIT;
