-- D-041 (approved, H-009): a version keeps the comment it was published
-- with (http.schema.json Publish.comment): 1 to 2000 characters as given,
-- null when there was none. Versions are immutable (TEST-004), so the
-- comment is written once, with the version. Table-level grants (0002)
-- cover the new column.
BEGIN;
SET LOCAL ROLE twin_owner;
ALTER TABLE twin.model_versions ADD COLUMN comment text
  CONSTRAINT model_versions_comment_length CHECK (char_length(comment) BETWEEN 1 AND 2000);
COMMIT;
