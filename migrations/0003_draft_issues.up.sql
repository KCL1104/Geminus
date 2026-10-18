-- T-012 / D-028: a draft keeps the semantic issues of its IR, computed when
-- the draft is written (ADR-022). A PATCH only has to be schema-valid; the
-- issues are what publish and run later refuse. At most 1000, as
-- http.schema.json Draft.issues; each one's shape is the application's
-- (common.schema.json ValidationIssue).
ALTER TABLE twin.model_drafts
  ADD COLUMN issues jsonb NOT NULL DEFAULT '[]'
  CONSTRAINT model_drafts_issues_array CHECK (jsonb_typeof(issues)='array' AND jsonb_array_length(issues)<=1000);
