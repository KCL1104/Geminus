# Runbook: Auth0 tenant and browser session (BFF) setup

Basis: [ADR-018](../adr/ADR-018-browser-session-and-oidc-provider.md) (BFF, Auth0), [ADR-017](../adr/ADR-017-oidc-token-verification.md) (the API verifies tokens), T-043.

**The owner runs these steps.** Agents do not create accounts, do not accept terms and do not hold any credential. Neither the repo nor CI has Auth0 settings or secrets; tests use only the test provider in the repo (`apps/web/tests/fake-oidc.ts`, `apps/web-e2e/fake/servers.mjs`). After H-034 (T-057): the owner still creates the tenant and the owner's own user. The settings of §2 and §3, turning off sign-up, and the judge account (H-035 (2)) are set by the deploy session's `deploy/railway/deploy.sh --apply` with the Auth0 CLI (the owner approves the CLI's login in the owner's own browser, [deploy runbook](deploy.md) §7.8). §6 is the manual fallback for the same settings.

## 1. What to decide before creating it

- **Region**: decided by data residency and retention (still TBD). Until it is decided, only test users are allowed; no real customer user data (ADR-018 §3).
- **Plan and cost**: the owner confirms them at sign-up.

## 2. API (the audience of the access token)

Create it in Auth0 Dashboard → Applications → APIs:

| Setting | Value |
|---|---|
| Identifier | The API's audience, for example `https://api.<your domain>`. It cannot be changed later |
| Signing algorithm | RS256 |
| Access token profile | RFC 9068 (header `typ: at+jwt`); on the API side set `AUTH_OIDC_REQUIRE_AT_JWT=true` |
| Token expiration | 600 seconds (10 minutes) |
| Allow Offline Access | On (otherwise no refresh token is issued) |

## 3. Application (the BFF is a confidential client)

Applications → Applications → Create → **Regular Web Application**:

| Setting | Value |
|---|---|
| Token endpoint authentication method | Client Secret (Post) |
| Allowed Callback URLs | `https://<app domain>/auth/callback` (this one only) |
| Allowed Logout URLs | `https://<app domain>/` |
| Allowed Web Origins, Allowed Origins (CORS) | Empty (the BFF does not open CORS; the browser does not connect to Auth0's API directly) |
| Grant types | Tick only Authorization Code and Refresh Token |
| ID token signature | RS256 (the BFF always verifies the ID token signature with RS256) |
| Refresh token rotation | On, with reuse detection on |
| Rotation reuse interval | 30 seconds, the same as the time the BFF shares a refresh result within the process (`REFRESH_REUSE_MS`). Do not set 0 even with a single web instance: a request the browser cancelled, or a lost response, does not get the cookie after rotation; a resend within 30 seconds still gets the new token instead of being treated as reuse that revokes the whole session |
| Refresh token expiration | Inactivity lifetime 43200 seconds (12 hours), Absolute lifetime 604800 seconds (7 days) |

Users: turn off sign-up on the database connection. Users and organizations are created only by an operator with `cmd/provision-tenant`; the subject is Auth0's `user_id` (for example `auth0|…`). An identity that was not provisioned gets 403 from the API after sign-in; it is not created automatically (D-008).

## 4. Deployment settings

Web (the Next.js server side, the BFF):

| Variable | Value |
|---|---|
| `BFF_APP_ORIGIN` | `https://<app domain>` |
| `BFF_API_URL` | The Go API's base URL (https; http only when every URL is on loopback) |
| `BFF_OIDC_ISSUER` | `https://<tenant>.<region>.auth0.com/` (with the trailing `/`, exactly the ID token's `iss`) |
| `BFF_OIDC_AUTHORIZATION_URL` | `<issuer>authorize` |
| `BFF_OIDC_TOKEN_URL` | `<issuer>oauth/token` |
| `BFF_OIDC_REVOCATION_URL` | `<issuer>oauth/revoke` |
| `BFF_OIDC_END_SESSION_URL` | `<issuer>oidc/logout` |
| `BFF_OIDC_JWKS_URL` | `<issuer>.well-known/jwks.json` |
| `BFF_OIDC_CLIENT_ID` | The Application's Client ID |
| `BFF_OIDC_CLIENT_SECRET` | The Application's Client Secret (only in the secret manager) |
| `BFF_OIDC_AUDIENCE` | The API Identifier of section 2 |
| `BFF_OIDC_SCOPES` | Not set (default `openid profile email offline_access`; H-042: `profile email` give the account menu its name and email) |
| `SESSION_KEYS` | `<id>:<base64url of 32 bytes>`, see below (only in the secret manager) |

Go API: `AUTH_OIDC_ISSUER` the same as above, `AUTH_OIDC_AUDIENCE` the API Identifier, `AUTH_OIDC_JWKS_URL` as above, `AUTH_OIDC_REQUIRE_AT_JWT=true` (ADR-017).

When the settings are incomplete or invalid, the BFF's `/auth/login`, `/auth/callback`, `/auth/logout` and `/api/v1/*` always return 503 (`/auth/error` is an ordinary page and shows as usual). The log writes only variable names, never values.

The BFF classifies refresh failures by the token endpoint's own answer (SECURITY §3). When Auth0 temporarily cannot refresh (no answer: unreachable, timeout; 5xx, 429; error codes `temporarily_unavailable`, `server_error`, `too_many_requests`; or a non-2xx without an OAuth error, for example an error page from an edge in front) or the client is misconfigured (`invalid_client`), `/api/v1/*` returns 503 (`Retry-After: 5`) and the session cookie is kept. Requests to the same instance within 5 seconds with the same refresh token share this failure; later requests try again. The log's `session.refresh_unavailable` carries the token endpoint's status and the OAuth error code in the body (only the status when there is no error code), so rate limiting, edge error pages and a wrong client secret can be told apart. The session ends (401 and the cookie is cleared, log `session.refresh_failed`) only when Auth0 rejects the refresh token (4xx with an error code other than those above, for example `invalid_grant`: reuse, revoked, expired; `access_denied`), when the session has no refresh token, or when Auth0 returns 2xx but the answer cannot be used (including when the JWKS needed to verify the ID token cannot be fetched). On a 2xx, Auth0 may already have rotated; the BFF does its best to revoke the new refresh token in the answer, and a failed revocation is logged as `session.discard_failed`.

### Session keys and rotation

```sh
# Generate one key (32 random bytes, base64url, no padding)
openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
```

- `SESSION_KEYS="k2:<new key>,k1:<old key>"`: the first key seals; the second only opens old cookies; at most two keys.
- Rotation: first deploy "new,old", wait at least 7 days (the session's absolute lifetime), then deploy "new" only. If the old key leaked, deploy only the new key right away: every session becomes invalid and users sign in again.

## 5. Pre-launch checks (staging)

1. Sign in as a test user: the browser's developer tools show only `__Host-geminus_session`, with attributes HttpOnly, Secure, SameSite=Lax, Path=/, no Domain; there is no token in `document.cookie`, localStorage or sessionStorage.
2. Call the API from any page: requests go only to the same-origin `/api/v1/…`, with no `Authorization` header.
3. Sign out: Auth0's log has the refresh token revocation and the logout; after that `/api/v1/…` returns 401.
4. Sign in as a user that was not provisioned: the API returns 403.
5. Block a test user, and let a refresh token expire; confirm that the error code Auth0 returns (expected `invalid_grant`) ends the session with 401, not 503.
   Also confirm the status and error code Auth0 actually returns in two cases (see the `session.refresh_unavailable`/`session.refresh_failed` logs): (a) the BFF refreshes with a wrong client secret (set the wrong value only on a separate staging deployment; do not touch the real settings). A 4xx answer keeps the session only when the error code is `invalid_client` or there is no error code. If Auth0 returns another error code (for example 401 `access_denied`), a wrong secret rotation order ends every session; before a rotation, first add the new secret in Auth0, deploy, then remove the old one, and record the result back in this runbook. (b) Make an Auth0 Action that runs during the refresh flow fail on purpose; record the status and error code Auth0 returns, and whether the BFF therefore ends (401) or keeps (503) the session, and confirm this matches the expectation.
6. At sign-out the BFF sends no `id_token_hint` (the ID token is discarded once verified): confirm whether Auth0 therefore shows a logout confirmation screen; if it does, evaluate Auth0's logout settings or a way other than keeping the ID token's hash (needs a separate decision).

The matching automated tests are `apps/web/tests/bff.test.ts` (unit) and `apps/web-e2e/tests/session.spec.ts` (browser, TEST-032); both connect only to the test provider. Checks against Auth0 can only be run by a person on staging (NOT RUN in CI).

## 6. Manual fallback: the Auth0 step's settings in the dashboard (H-034)

The owner runs this when the Auth0 step of `deploy.sh` ([deploy runbook](deploy.md) §7.8) cannot be used (the pinned CLI cannot be obtained, the device login fails, or a step stops and cannot be fixed). The settings are exactly those of the §7.8 table. The dashboard names follow Auth0's documentation (fetched 2026-10-26: [API settings](https://auth0.com/docs/get-started/apis/api-settings), [application settings](https://auth0.com/docs/get-started/applications/application-settings), [credential settings](https://auth0.com/docs/get-started/applications/credentials), [refresh token rotation](https://auth0.com/docs/secure/tokens/refresh-tokens/configure-refresh-token-rotation), [refresh token expiration](https://auth0.com/docs/secure/tokens/refresh-tokens/configure-refresh-token-expiration), [application connections](https://auth0.com/docs/get-started/applications/update-application-connections), [access token profiles](https://auth0.com/docs/secure/tokens/access-tokens/access-token-profiles)). The documentation page for "Disable Sign Ups" was not fetched; its name follows the dashboard (the API field is the connection's `options.disable_signup`).

First the web needs its public domain (Railway → web → Settings → Networking; or first run `--apply` once with `GEMINUS_AUTH0_SETUP=manual`: step 2 creates it and the verification prints the callback and logout URLs).

1. **API**: Applications → APIs → **+ Create API**: Name `Geminus demo API`; Identifier is `GEMINUS_AUTH0_AUDIENCE` (for example `https://api.geminus-demo.invalid`; it cannot be changed later); **JSON Web Token (JWT) Profile** choose **RFC 9068**; **JSON Web Token (JWT) Signing Algorithm** choose **RS256** → Create. In that API's **Settings**: Token Settings, **Maximum Access Token Lifetime** enter `600`; Access Settings, **Allow Skipping User Consent** on, **Allow Offline Access** on → Save.
2. **Application**: Applications → Applications → **+ Create Application**: Name `Geminus demo web`, choose **Regular Web Applications** → Create.
   - **Settings** → Application URIs: **Allowed Callback URLs** only `https://<web domain>/auth/callback`; **Allowed Logout URLs** only `https://<web domain>/`; **Allowed Web Origins** and **Allowed Origins (CORS)** empty.
   - **Settings** → **Refresh Token Rotation**: **Allow Refresh Token Rotation** on, **Rotation Overlap Period** enter `30`. **Refresh Token Expiration**: **Set Idle Refresh Token Lifetime** on, **Idle Refresh Token Lifetime** enter `43200`; **Set Maximum Refresh Token Lifetime** on, **Maximum Refresh Token Lifetime** enter `604800`.
   - **Settings** → **Advanced Settings** → **OAuth**: **JSON Web Token (JWT) Signature Algorithm** RS256, **OIDC Conformant** on; **Grant Types**: tick only **Authorization Code** and **Refresh Token** (untick all others) → **Save Changes**.
   - **Credentials**: **Authentication Method** choose **Client Secret (Post)** → Save. The **Client ID** goes to `GEMINUS_AUTH0_CLIENT_ID`; paste the **Client Secret** only into Railway → web → Variables as `BFF_OIDC_CLIENT_SECRET`, Seal it right away (the 3-dot menu on the right of the variable), and store it nowhere else.
   - **Connections**: enable only `Username-Password-Authentication` (the database connection); turn off all others (for example Google).
3. **Turn off sign-up**: Authentication → Database → `Username-Password-Authentication` → **Disable Sign Ups** on → Save.
4. **Judge account** (H-035 (2)): User Management → Users → **+ Create User**: Email `judge@geminus-demo.invalid` (or the address agreed with the coordinator), Password generated with a password manager (20 characters or more, with upper and lower case, digits and symbols), Connection `Username-Password-Authentication` → Create. On that user's page copy the **user_id** (`auth0|…`) for `GEMINUS_JUDGE_SUBJECT`. Write the password directly into the submission notes and store it nowhere else.
5. Run `GEMINUS_AUTH0_SETUP=manual GEMINUS_AUTH0_CLIENT_ID='<Client ID>' GEMINUS_JUDGE_SUBJECT='auth0|<the judge's user_id>' deploy/railway/deploy.sh --apply` (other inputs as in deploy runbook §7.7): the seed makes the judge account an editor of the demo organization and checks that it is not a member of any other organization. If `BFF_OIDC_CLIENT_SECRET` is not filled in, it stops at the verification; fill it in, Seal it and run again. If the demo organization was already seeded, see "The judge account's membership" in deploy runbook §7.8.
6. Check each item against the table in deploy runbook §7.8 (for example the API's Settings and the application's Settings, Credentials and Connections pages), then do the checks of §5.
