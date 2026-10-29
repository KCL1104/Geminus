# ADR-018 — Browser session through a BFF, and Auth0 as the OIDC provider

Status: ACCEPTED. Decided on 2026-10-18 by the agent under the owner's authorization: the owner replied "ok, you can do it right now" in the session (H-004); the owner did not review it item by item. The owner can reopen it at any time; any provider account, plan and cost is confirmed by the owner.

The original report lists the Auth provider as TBD ("managed OIDC", DECISIONS_AND_TBD.md). SECURITY §3 requires a separate auth ADR for browser-side token storage, refresh and the BFF pattern. ADR-017 only fixes how the API verifies the access token and does not choose a provider. This ADR adds these two items; the frontend sign-in flow (T-043) is implemented from it.

## Decision

### 1. Browser session: BFF (Backend-for-Frontend)

- **The OIDC client is on the server.** The Next.js server side (Node runtime, not a static export) is a confidential OIDC client.
  - Flow: Authorization Code + PKCE (S256) + `state` + `nonce`.
  - Client authentication: a client secret or `private_key_jwt`, kept only in the web server's secret store.
  - Endpoints: authorization, token, revocation and end-session come only from the deployment settings; there is no discovery at runtime. This matches SECURITY §3 "provider endpoints only from the deployment allowlist".
- **The browser never gets any token.**
  - Access, refresh and ID tokens are never put anywhere JS can read: not localStorage, sessionStorage, IndexedDB, a JS-readable cookie or a response body.
  - The ID token is used only after it is verified in the callback (`iss`, `aud`, `nonce`, `sub`) and is discarded right after.
- **Session cookie.**
  - Attributes: `__Host-geminus_session`, `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, no `Domain`.
  - Content: the access token, refresh token, expiry time and the session's absolute deadline, encrypted with AES-256-GCM; the AAD is the cookie name and the format version.
  - Keys: a current key and the previous key are kept at the same time, to support rotation; the keys come from the secret store.
  - Size: a cookie over 3.5 KB is an error; it is not split into chunks. If that happens, switch to a server-side session store, under a separate ADR.
- **CSRF protection.**
  - Besides `SameSite=Lax`, the BFF requires two things on every request that is not GET/HEAD: `Origin` is identical to the configured app origin (when the browser sends `Sec-Fetch-Site`, it must be `same-origin`), and the custom header `X-Geminus-Request: 1` is present, which forces a cross-site request to do a preflight first.
  - The BFF does not enable CORS.
- **API proxy.**
  - The browser calls only the same-origin `/api/v1/...`. The BFF swaps in `Authorization: Bearer <access token>` and forwards to the Go API; `X-Organization-ID` is forwarded as is; it is only a selector (D-008).
  - When the access token has less than 60 seconds left, the BFF refreshes it on the server.
  - The BFF does not forward the cookie to the Go API. The Go API still reads only Bearer and no cookie (ADR-017 unchanged).
  - SSE (TEST-026) and uploads are forwarded as streams; size limits are still enforced by the API (TEST-012).
- **Starting token lifetimes** (set at the provider, recorded in the deployment runbook; a change needs a security review):
  - Access token: 10 minutes.
  - Refresh token: rotation, with reuse detection turned on.
  - Session: 12 hours idle, 7 days absolute.
- **Logout.** In order: clear the cookie; revoke the refresh token at the revocation endpoint; then redirect to the configured end-session URL (RP-initiated logout).
- **Library.** A generic OIDC/OAuth client is used, not a provider-specific SDK, so that the provider can be replaced. The candidate is `openid-client` (MIT). The version is chosen, pinned and recorded in T-043 under AGENTS.md: released at least two weeks ago, with a lockfile and a license record.

### 2. OIDC provider: Auth0

Auth0 is chosen as the managed OIDC provider for alpha and production, because:

- **The access token is bound to the API audience.** Auth0 can issue JWT access tokens for an API identifier (`aud`), and each API can choose the RFC 9068 profile (header `typ: at+jwt`). So `AUTH_OIDC_REQUIRE_AT_JWT` (ADR-017) can be turned on, and an ID token cannot be used as an access token.
- **Not tied to a cloud.** The cloud is still TBD (GCP reference/AWS equivalent); Auth0 works with both.
- **Standard interface.** It provides the standard OIDC endpoints and a JWKS, RS256 by default, which is in ADR-017's default allowlist.
- **Refresh token.** It supports rotation and reuse detection.
- **B2B SSO later.** If needed, organizations or enterprise connections can be used. Tenancy and role still always come from the Geminus DB (D-008) and are never taken from IdP claims.

Alternatives compared:

| Option | Why not chosen |
|---|---|
| Amazon Cognito | The access token carries `aud` only with a resource binding; and it is tied to AWS |
| Google Identity Platform | Centred on ID tokens; not an authorization server that issues access tokens for a custom API audience |
| Microsoft Entra External ID | A workable alternative; `typ` is `JWT`, not `at+jwt` |
| Self-hosted Keycloak | Not managed; adds operating burden |

Changing the provider only needs a settings change: the API's `AUTH_OIDC_*` and the BFF's client settings, then re-running TEST-009/TEST-032.

### 3. Conditions and still open

- **The account belongs to the owner.** The Auth0 tenant is created and held by the owner; the plan and cost are confirmed by the owner at sign-up. The agent does not create accounts, does not accept terms and does not hold credentials.
- **Region and data.** The tenant region is decided by retention/residency (TBD). Until then there may be only test users; no real customer user data.
- **Controlled provisioning.** Follows D-008/T-010: when an unknown identity signs in, the answer is 403 (`tenant.ErrUnknownUser`); no user is created automatically from the token.
- **Local and CI.** E2E uses a deterministic test OIDC provider in the repository: add authorize and token endpoints to `internal/auth/authtest`, or use equivalent test-only code instead. It never connects to Auth0, and CI has no Auth0 credential.
- **Rate limiting.** Rate limiting and alerting for login, callback and refresh belong to T-036/T-035.

## Consequences

- The web needs a server-side runtime: T-039's web container is a Node server, not static files.
- ADR-016's `lib/api/client.ts` originally "sends `Authorization`, sends no cookie"; in T-043 it changes to:
  - call the same-origin BFF, `credentials: 'same-origin'`;
  - send `X-Geminus-Request`;
  - the browser no longer holds a token and does not set `Authorization`.

  The rest of ADR-016 is unchanged.
- The Go API is unchanged: it still accepts only Bearer and adds no cookie write path. The cookie protections SECURITY §3 requires (CSRF, SameSite, Secure, HttpOnly) are enforced by the BFF and verified by TEST-032.
- 2026-10-26 addendum (T-043 follow-up: the UI bootstrap after sign-in): the root layout reads the session cookie on the server only to know whether it can be opened and has not expired (the BFF's `hasSession`: unseal, check the deadline; no refresh, no call to the provider or the API). The page receives only this boolean and the organization ID chosen earlier; the cookie's value, tokens or sealed contents never reach the HTML. `/me` and the API calls after it all go through the same-origin proxy, and refresh still happens only in the proxy (the rotated token can only go in the response's Set-Cookie). The sign-in link is `/auth/login?return_to=<the current page>`; the return path is checked with the same rules as the BFF (`lib/return-to.ts`). The sign-out button calls the existing `POST /auth/logout` with `X-Geminus-Request`. The CSRF rules are unchanged.
- 2026-10-26 addendum (T-057: security headers before the public demo; details and reasons in SECURITY §3): the Next.js server that hosts the BFF adds to every response (pages, `/auth/*`, `/api/v1/*`, static files), through `apps/web/proxy.ts`, a CSP (one nonce per request; scripts rely only on the nonce and `'strict-dynamic'`, with no `'unsafe-inline'`/`'unsafe-eval'`; the only relaxation is for style attributes), `X-Content-Type-Options`, `Referrer-Policy: no-referrer`, `Permissions-Policy`, `X-Frame-Options`, and HSTS when the app origin is https. Impact on this ADR:
  - The sign-in flow needs no CSP exception: `/auth/login` is a link and a 303, the provider returns to the callback with a GET 302, and `form-action 'self'` is enough; `connect-src 'self'` is also enough, because the token, revocation and JWKS calls are server-side, and the browser only touches the same-origin BFF. Changing the provider does not need a CSP change.
  - The CSRF rules are unchanged: `Referrer-Policy: no-referrer` does not affect the `Origin` that `fetch` sends (the e2e sign-out and writes pass, and a cross-site write is still 403).
  - HSTS and `upgrade-insecure-requests` are decided by the scheme of `BFF_APP_ORIGIN` (a deployment setting, not the request), so local and e2e http (loopback) is not affected.
  - The proxy does not read or change the session cookie and does not block any request; it only adds headers and the nonce.
