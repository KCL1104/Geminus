-- DEVELOPMENT-ONLY DOWN MIGRATION of 0012: proposals no longer follow their
-- job, lose the lifecycle guard and the checks of each state's content,
-- and lose their output locale and selected entity IDs (dropped with their
-- values).
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER follow_proposal_job ON twin.jobs;
DROP FUNCTION twin.follow_proposal_job();
DROP TRIGGER proposal_generated_with_its_job ON twin.ai_proposals;
DROP FUNCTION twin.check_proposal_generated_with_its_job();
DROP TRIGGER protect_proposal_lifecycle ON twin.ai_proposals;
DROP FUNCTION twin.protect_proposal_lifecycle();
ALTER TABLE twin.ai_proposals
  DROP CONSTRAINT ai_proposals_provider_metadata_object,
  DROP CONSTRAINT ai_proposals_invalid_content,
  DROP CONSTRAINT ai_proposals_needs_input_content,
  DROP CONSTRAINT ai_proposals_error_iff_failed,
  DROP COLUMN selected_entity_ids,
  DROP COLUMN output_locale;
COMMIT;
