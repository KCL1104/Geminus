-- DEVELOPMENT-ONLY DOWN MIGRATION of 0017: the records of the photos photo
-- proposals were asked for with are dropped (the objects stay in storage,
-- named by nothing). Never on a deployment with photo proposals: their
-- proposals would then look as if asked for with an empty intent.
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER proposal_source_at_commit ON twin.ai_proposals;
DROP FUNCTION twin.check_proposal_source();
DROP TRIGGER protect_ai_proposal_image ON twin.ai_proposal_images;
DROP FUNCTION twin.protect_ai_proposal_image();
DROP TABLE twin.ai_proposal_images;
COMMIT;
