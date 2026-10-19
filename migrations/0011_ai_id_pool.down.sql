-- DEVELOPMENT-ONLY DOWN MIGRATION of 0011: drops the proposals' ID
-- allocation pools (with their values) and the rules that guard them.
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER protect_proposal_id_pool ON twin.ai_proposals;
DROP FUNCTION twin.protect_proposal_id_pool();
ALTER TABLE twin.ai_proposals DROP COLUMN id_pool;
DROP FUNCTION twin.id_pool_ok(jsonb);
COMMIT;
