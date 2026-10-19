-- D-045 (approved, H-010; CB-17): GET /v1/me lists the caller's own
-- memberships with each organization's ID and name. It runs in a read-only
-- transaction with no organization selected and the verified user as
-- app.user_id (tenant.DB.RunSelf). Two read-only policies let that
-- transaction see the user's own memberships and the organizations they
-- belong to, and nothing else:
--   * they apply only when no organization is selected, so a transaction
--     that selects one sees exactly what tenant_isolation (0002) shows it;
--   * FOR SELECT: no row is inserted, changed, removed or locked through
--     them (UPDATE, DELETE and SELECT ... FOR UPDATE / FOR SHARE also need
--     an UPDATE or DELETE policy, and tenant_isolation is the only one);
--   * TO twin_api: the worker and provisioning roles get nothing (neither
--     has SELECT on these tables).
-- Permissive policies are OR-ed with tenant_isolation, which is false when
-- no organization is selected. The organizations policy reads memberships
-- as the invoking role, so memberships' policies filter that subquery as
-- well; no policy on memberships reads organizations, so there is no
-- recursion. The organization row is one its member can already read with
-- the organization selected; the API reads only its id and name.
BEGIN;
SET LOCAL ROLE twin_owner;
CREATE POLICY own_memberships ON twin.memberships FOR SELECT TO twin_api
  USING (twin.current_organization_id() IS NULL AND user_id::text = twin.current_actor_id());
CREATE POLICY own_organizations ON twin.organizations FOR SELECT TO twin_api
  USING (twin.current_organization_id() IS NULL AND EXISTS (
    SELECT 1 FROM twin.memberships m
    WHERE m.organization_id = organizations.id AND m.user_id::text = twin.current_actor_id()));
COMMIT;
