-- DEVELOPMENT-ONLY DOWN MIGRATION of 0008: with no organization selected the
-- API sees no membership or organization again (GET /v1/me then answers
-- with an empty list for everyone).
BEGIN;
SET LOCAL ROLE twin_owner;
DROP POLICY own_organizations ON twin.organizations;
DROP POLICY own_memberships ON twin.memberships;
COMMIT;
