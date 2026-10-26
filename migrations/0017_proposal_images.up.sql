-- T-056 (H-029, H-033; REQ-008, REQ-009, REQ-019; AGENTS.md §2): the photo
-- or floor plan an edit proposal is asked for with. The API decodes the
-- upload and stores its pixels encoded again (no EXIF or other metadata,
-- internal/proposals/photo) in private object storage; this table holds
-- what the server records of that object, one row per photo proposal:
--
--   * the object key (tenant/<organization>/assets/proposal-images/
--     <proposal>/image, made by the server; twin.object_key_ok as 0001),
--     the SHA-256 and size of the stored bytes (at most 5,242,880: 5 MiB), the
--     media type (image/png or image/jpeg) and the size in pixels (32 to
--     4096 on each side), and when it was recorded;
--   * the composite foreign key to the proposal of the same organization,
--     and RLS as 0002: no row of another tenant is ever visible or written;
--   * it is the proposal's request, so it never changes and is never
--     removed by a runtime role: the API may only insert it, the worker
--     only read it (the job forwards the image to the AI after checking
--     the bytes against this record), and an UPDATE is refused for every
--     role, the schema owner included;
--   * it is written with its proposal, in the transaction that creates it
--     (generating, mode edit, created at the transaction's time, by the
--     scoped user, without an intent), and a proposal created without an
--     intent has exactly one: checked at commit, so "asked for with a photo"
--     is a fact of the database, not of the client (proposal.schema.json
--     source_image).
--
-- No proposal before this migration lacks an intent (the API refuses an
-- intent of only white space, T-025), so the commit check holds for every
-- existing row; it runs only for proposals created from now on.
BEGIN;
SET LOCAL ROLE twin_owner;

CREATE TABLE twin.ai_proposal_images (
  organization_id twin.org_id NOT NULL,
  proposal_id twin.aip_id NOT NULL,
  object_key text NOT NULL,
  content_hash twin.sha256 NOT NULL,
  size_bytes integer NOT NULL CONSTRAINT ai_proposal_images_size CHECK (size_bytes BETWEEN 1 AND 5242880),
  media_type text NOT NULL CONSTRAINT ai_proposal_images_media_type CHECK (media_type IN ('image/png', 'image/jpeg')),
  width integer NOT NULL CONSTRAINT ai_proposal_images_width CHECK (width BETWEEN 32 AND 4096),
  height integer NOT NULL CONSTRAINT ai_proposal_images_height CHECK (height BETWEEN 32 AND 4096),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id, proposal_id),
  UNIQUE (object_key),
  FOREIGN KEY (organization_id, proposal_id) REFERENCES twin.ai_proposals(organization_id, id),
  CONSTRAINT ai_proposal_images_key CHECK (
    twin.object_key_ok(object_key, 'tenant/' || organization_id || '/assets/proposal-images/' || proposal_id || '/'))
);

ALTER TABLE twin.ai_proposal_images ENABLE ROW LEVEL SECURITY;
ALTER TABLE twin.ai_proposal_images FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON twin.ai_proposal_images FOR ALL
  USING (organization_id::text = twin.current_organization_id()) WITH CHECK (organization_id::text = twin.current_organization_id());

REVOKE ALL ON twin.ai_proposal_images FROM PUBLIC;
GRANT SELECT, INSERT ON twin.ai_proposal_images TO twin_api;
GRANT SELECT ON twin.ai_proposal_images TO twin_worker;

-- An image is recorded with its proposal and never changes. The schema
-- owner may insert one for the SQL tests and operators, under the same
-- rules; nobody updates one.
CREATE FUNCTION twin.protect_ai_proposal_image() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE p record;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    RAISE EXCEPTION 'a proposal''s image is its request: it never changes' USING ERRCODE = '55000';
  END IF;
  SELECT status, mode, intent, created_by, created_at INTO p FROM twin.ai_proposals
    WHERE organization_id = NEW.organization_id AND id = NEW.proposal_id;
  IF NOT FOUND THEN
    RETURN NEW; -- the foreign key refuses it
  END IF;
  IF p.status <> 'generating' OR p.mode <> 'edit' OR p.intent <> '' OR p.created_at IS DISTINCT FROM now() THEN
    RAISE EXCEPTION 'an image is recorded with its proposal: generating, an edit, without an intent, in the transaction that creates it'
      USING ERRCODE = '55000';
  END IF;
  IF pg_catalog.pg_has_role(current_user, 'twin_api', 'USAGE') AND NOT pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE')
     AND p.created_by::text IS DISTINCT FROM nullif(current_setting('app.user_id', true), '') THEN
    RAISE EXCEPTION 'the API records an image for the scoped user''s own proposal' USING ERRCODE = '42501';
  END IF;
  IF NEW.created_at IS DISTINCT FROM now() THEN
    RAISE EXCEPTION 'an image is recorded at the time of its transaction' USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_ai_proposal_image() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER protect_ai_proposal_image BEFORE INSERT OR UPDATE ON twin.ai_proposal_images
  FOR EACH ROW EXECUTE FUNCTION twin.protect_ai_proposal_image();

-- A proposal without an intent was asked for with a photo, and one with
-- an intent was not: at commit, for every role. Runs with the privileges of
-- whoever inserts the proposal (the API, the owner), which read the images
-- of their own organization.
CREATE FUNCTION twin.check_proposal_source() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE has_image boolean;
BEGIN
  SELECT EXISTS (SELECT 1 FROM twin.ai_proposal_images i
                 WHERE i.organization_id = NEW.organization_id AND i.proposal_id = NEW.id) INTO has_image;
  IF (NEW.intent = '') IS DISTINCT FROM has_image THEN
    RAISE EXCEPTION 'a proposal is asked for with an intent or with a photo: exactly one' USING ERRCODE = '23514';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_proposal_source() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER proposal_source_at_commit AFTER INSERT ON twin.ai_proposals
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION twin.check_proposal_source();
COMMIT;
