-- D-040 (approved, H-009): model_versions.ir and model_drafts.ir hold the
-- canonical JSON text (RFC 8785) the hash covers, not jsonb. jsonb keeps
-- values, not bytes: it printed a 1.9 MB canonical draft as 83.6 MB (T-012
-- review M4). Stored as text, what is read back is byte for byte what the
-- service checked and hashed, and the database itself checks that the hash
-- is the stored text's.
--
-- SQL cannot produce the canonical form, so existing rows are not
-- converted: the migration stops if there are any. Nothing is deployed yet;
-- a development database is re-created (db/README.md).
BEGIN;
SET LOCAL ROLE twin_owner;
-- FORCE ROW LEVEL SECURITY hides every row from the owner too; look
-- without it, inside this transaction only.
ALTER TABLE twin.model_versions NO FORCE ROW LEVEL SECURITY;
ALTER TABLE twin.model_drafts NO FORCE ROW LEVEL SECURITY;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM twin.model_versions) OR EXISTS (SELECT 1 FROM twin.model_drafts) THEN
    RAISE EXCEPTION 'migration 0005 stores the canonical IR text, which SQL cannot produce from jsonb: run it on a database without models'
      USING ERRCODE = '55000';
  END IF;
END $$;
-- The checks that read the IR as jsonb.
DO $$
DECLARE c record;
BEGIN
  FOR c IN SELECT conrelid::regclass AS tbl, conname FROM pg_constraint
           WHERE conrelid IN ('twin.model_versions'::regclass, 'twin.model_drafts'::regclass)
             AND contype = 'c' AND pg_get_constraintdef(oid) ~ '\mir\M' LOOP
    EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', c.tbl, c.conname);
  END LOOP;
END $$;
ALTER TABLE twin.model_versions ALTER COLUMN ir TYPE text USING ir::text;
ALTER TABLE twin.model_drafts ALTER COLUMN ir TYPE text USING ir::text;
ALTER TABLE twin.model_versions
  ADD CONSTRAINT model_versions_ir_object CHECK (jsonb_typeof(ir::jsonb) = 'object'),
  ADD CONSTRAINT model_versions_ir_model CHECK (coalesce(ir::jsonb->>'model_id' = model_id::text, false)),
  ADD CONSTRAINT model_versions_ir_schema_version CHECK (coalesce(ir::jsonb->>'schema_version' = schema_version, false)),
  ADD CONSTRAINT model_versions_ir_hash CHECK (hash = 'sha256:' || encode(sha256(convert_to(ir, 'UTF8')), 'hex'));
ALTER TABLE twin.model_drafts
  ADD CONSTRAINT model_drafts_ir_object CHECK (jsonb_typeof(ir::jsonb) = 'object'),
  ADD CONSTRAINT model_drafts_ir_model CHECK (coalesce(ir::jsonb->>'model_id' = model_id::text, false)),
  ADD CONSTRAINT model_drafts_ir_hash CHECK (hash = 'sha256:' || encode(sha256(convert_to(ir, 'UTF8')), 'hex'));
ALTER TABLE twin.model_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE twin.model_drafts FORCE ROW LEVEL SECURITY;
COMMIT;
