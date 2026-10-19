-- DEVELOPMENT-ONLY DOWN MIGRATION of 0005: the IR back to jsonb with 0001's
-- checks. The hash then no longer covers the stored bytes.
BEGIN;
SET LOCAL ROLE twin_owner;
ALTER TABLE twin.model_versions
  DROP CONSTRAINT model_versions_ir_object,
  DROP CONSTRAINT model_versions_ir_model,
  DROP CONSTRAINT model_versions_ir_schema_version,
  DROP CONSTRAINT model_versions_ir_hash;
ALTER TABLE twin.model_drafts
  DROP CONSTRAINT model_drafts_ir_object,
  DROP CONSTRAINT model_drafts_ir_model,
  DROP CONSTRAINT model_drafts_ir_hash;
ALTER TABLE twin.model_versions ALTER COLUMN ir TYPE jsonb USING ir::jsonb;
ALTER TABLE twin.model_drafts ALTER COLUMN ir TYPE jsonb USING ir::jsonb;
ALTER TABLE twin.model_versions
  ADD CHECK (jsonb_typeof(ir) = 'object'),
  ADD CHECK (coalesce(ir->>'model_id' = model_id::text, false)),
  ADD CHECK (coalesce(ir->>'schema_version' = schema_version, false));
ALTER TABLE twin.model_drafts
  ADD CHECK (jsonb_typeof(ir) = 'object'),
  ADD CHECK (coalesce(ir->>'model_id' = model_id::text, false));
COMMIT;
