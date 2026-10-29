// The BFF's OIDC client (ADR-018, T-043): Authorization Code with PKCE (S256),
// state and nonce, through openid-client with the deployment's endpoints (no
// discovery). The ID token is checked (signature, iss, aud, exp, nonce, sub)
// and dropped; only the access and refresh tokens and, from its claims, a
// cleaned name and email (H-042, the account menu) are kept, sealed in the
// session cookie. Server-side only.
import { AsyncLocalStorage } from "node:async_hooks";
import * as client from "openid-client";
import type { BffConfig } from "./config";
import { type Login, profileFromClaims, type Session, sessionToken } from "./session";

export type Oidc = {
  config: client.Configuration;
  redirectUri: string;
  postLogoutRedirectUri: string;
  audience: string;
  scopes: string;
  /** The BFF's structured log (bff.ts BffDeps.log); never tokens. */
  log: (event: string, fields?: Record<string, string | number>) => void;
};

/**
 * Why a login, refresh or logout step failed; the cause never reaches the
 * browser. refused: the provider refused (or answered what the session cannot
 * hold), so the session cannot go on; otherwise the provider was unavailable
 * and the same step may succeed later.
 */
export class OidcFailure extends Error {
  constructor(
    readonly step: "login" | "refresh" | "revoke",
    readonly reason: string,
    readonly refused = true,
    /** The provider's OAuth error code and HTTP status, for the log (never its description). */
    readonly answer: { error?: string; status?: number } = {},
  ) {
    super(`${step}: ${reason}`);
  }
}

/**
 * OAuth error codes after which the same refresh may succeed later: the
 * provider is unavailable or the deployment is misconfigured
 * (invalid_client: a client secret rotated at the provider first), not the
 * token refused.
 */
const UNAVAILABLE = new Set(["temporarily_unavailable", "server_error", "too_many_requests", "invalid_client"]);

/**
 * What the token endpoint answered during one refresh grant, recorded by the
 * client's fetch (tokenAnswers): openid-client's error alone cannot tell the
 * token endpoint's answer from, say, a JWKS fetch made after it.
 */
type TokenAnswer = { status?: number; error?: string; refreshToken?: string };
const tokenAnswers = new AsyncLocalStorage<TokenAnswer>();

/**
 * Whether a failed refresh grant ends the session (ADR-018), from what the
 * token endpoint answered. It ends when the endpoint answered 2xx and the
 * answer could not be used (an ID token that does not verify or whose keys
 * cannot be fetched, an unknown token type: the provider may have spent the
 * token, so the new refresh token is discarded), or answered an OAuth error
 * other than UNAVAILABLE's. It stays when the endpoint did not answer
 * (unreachable, timed out), or answered 5xx, 429, UNAVAILABLE's codes, or a
 * non-2xx without an OAuth error (an edge's error page): the provider did not
 * take the token, and a later request tries again.
 */
function refused(a: TokenAnswer): boolean {
  if (a.status === undefined) return false;
  if (a.status >= 200 && a.status < 300) return true;
  if (a.status === 429 || a.status >= 500) return false;
  return a.error !== undefined && !UNAVAILABLE.has(a.error);
}

/** Records the token endpoint's answer for the refresh grant in progress. */
async function record(url: string, tokenEndpoint: string, res: Response): Promise<void> {
  const a = tokenAnswers.getStore();
  if (a === undefined || url !== tokenEndpoint) return;
  a.status = res.status;
  try {
    const body = (await res.clone().json()) as Record<string, unknown> | null;
    if (typeof body?.error === "string" && body.error !== "") a.error = body.error;
    if (res.ok && typeof body?.refresh_token === "string" && body.refresh_token !== "") a.refreshToken = body.refresh_token;
  } catch {
    // Not JSON: an answer without an OAuth error.
  }
}

/** fetchImpl replaces the network for the provider's endpoints (tests). */
export function newOidc(
  cfg: BffConfig,
  fetchImpl?: (url: string, init: RequestInit) => Promise<Response>,
  log: Oidc["log"] = () => {},
): Oidc {
  const o = cfg.oidc;
  const config = new client.Configuration(
    {
      issuer: o.issuer,
      authorization_endpoint: o.authorizationEndpoint,
      token_endpoint: o.tokenEndpoint,
      revocation_endpoint: o.revocationEndpoint,
      end_session_endpoint: o.endSessionEndpoint,
      jwks_uri: o.jwksUri,
    },
    o.clientId,
    { id_token_signed_response_alg: "RS256" },
    client.ClientSecretPost(o.clientSecret),
  );
  const send = fetchImpl ?? ((url: string, init: RequestInit) => fetch(url, init));
  config[client.customFetch] = async (url, options) => {
    const res = await send(url, options as RequestInit);
    await record(url, o.tokenEndpoint, res);
    return res;
  };
  if (cfg.loopbackHttp) client.allowInsecureRequests(config);
  // openid-client trusts TLS for an ID token from the token endpoint (OIDC
  // Core 3.1.3.7) and skips its signature; check it against the deployment's
  // JWKS as well, so a token endpoint answer alone cannot sign a user in.
  client.enableNonRepudiationChecks(config);
  return {
    config,
    redirectUri: `${cfg.appOrigin}/auth/callback`,
    postLogoutRedirectUri: `${cfg.appOrigin}/`,
    audience: o.audience,
    scopes: o.scopes,
    log,
  };
}

/** A new login transaction and the provider URL to send the browser to. */
export async function startLogin(o: Oidc, returnTo: string, now: number): Promise<{ url: string; login: Login }> {
  const login: Login = {
    state: client.randomState(),
    nonce: client.randomNonce(),
    codeVerifier: client.randomPKCECodeVerifier(),
    returnTo,
    createdAt: now,
  };
  const url = client.buildAuthorizationUrl(o.config, {
    redirect_uri: o.redirectUri,
    scope: o.scopes,
    audience: o.audience,
    code_challenge: await client.calculatePKCECodeChallenge(login.codeVerifier),
    code_challenge_method: "S256",
    state: login.state,
    nonce: login.nonce,
  });
  return { url: url.toString(), login };
}

/**
 * The session fields of a token response at now: tokens the session can
 * hold (sessionToken) and the access token's expiry from the response's own
 * expires_in (not openid-client's expiresIn(), which counts from Date.now()).
 */
async function fields(o: Oidc, tokens: client.TokenEndpointResponse, keepRefresh: string | null, now: number): Promise<Pick<Session, "accessToken" | "refreshToken" | "accessExpiresAt">> {
  const s = tokens.expires_in;
  if (s === undefined || !Number.isSafeInteger(s) || s <= 0) throw new Error("the token response has no positive expires_in");
  const refreshToken = tokens.refresh_token ?? keepRefresh;
  if (!sessionToken(tokens.access_token) || (refreshToken !== null && !sessionToken(refreshToken))) {
    // The session ends without it, so it is not left live at the provider
    // either (the new token, or the one a provider that does not rotate
    // would have let it keep).
    if (refreshToken !== null && refreshToken !== "") await discard(o, refreshToken);
    throw new Error("the provider's tokens do not fit the session");
  }
  return { accessToken: tokens.access_token, refreshToken, accessExpiresAt: now + s * 1000 };
}

/**
 * Exchanges the code in callback (the provider's redirect to redirectUri)
 * for tokens, checking state, PKCE and the ID token against login.
 */
export async function finishLogin(o: Oidc, callback: URL, login: Login, now: number): Promise<Session> {
  try {
    const tokens = await client.authorizationCodeGrant(o.config, callback, {
      expectedState: login.state,
      expectedNonce: login.nonce,
      pkceCodeVerifier: login.codeVerifier,
      idTokenExpected: true,
    });
    const claims = tokens.claims();
    if (claims === undefined || typeof claims.sub !== "string" || claims.sub === "") throw new Error("the ID token has no subject");
    // The person's name and email for the account menu (H-042); never the subject or any other claim.
    return { ...(await fields(o, tokens, null, now)), createdAt: now, lastSeenAt: now, profile: profileFromClaims(claims) };
  } catch (e) {
    throw new OidcFailure("login", e instanceof Error ? e.message : "unknown error");
  }
}

/**
 * New tokens for s. With rotation the provider returns a new refresh token
 * and the old one is spent; a reused or revoked one is refused
 * (invalid_grant), which ends the session, as does an answer the session
 * cannot use. An outage is not a refusal: the session stays and a later
 * request tries again (refused, OidcFailure.refused).
 */
export async function refresh(o: Oidc, s: Session, now: number): Promise<Session> {
  const token = s.refreshToken;
  if (token === null) throw new OidcFailure("refresh", "the session has no refresh token");
  const answer: TokenAnswer = {};
  let tokens: client.TokenEndpointResponse;
  try {
    tokens = await tokenAnswers.run(answer, () => client.refreshTokenGrant(o.config, token));
  } catch (e) {
    const ended = refused(answer);
    // A 2xx that could not be used: the token it issued is not kept.
    if (ended && answer.refreshToken !== undefined) await discard(o, answer.refreshToken);
    throw new OidcFailure("refresh", e instanceof Error ? e.message : "unknown error", ended, { error: answer.error, status: answer.status });
  }
  try {
    return { ...s, ...(await fields(o, tokens, s.refreshToken, now)) };
  } catch (e) {
    // The old refresh token may be spent already: the session cannot go on.
    throw new OidcFailure("refresh", e instanceof Error ? e.message : "unknown error");
  }
}

/** How long an answer waits for a discard; the revocation goes on after it. */
export const DISCARD_WAIT_MS = 2000;

/**
 * Revokes a refresh token the session will not keep, best effort: it never
 * throws, a failure is logged (the token then expires on its own), and the
 * caller waits at most DISCARD_WAIT_MS.
 */
export async function discard(o: Oidc, refreshToken: string): Promise<void> {
  const revocation = client.tokenRevocation(o.config, refreshToken, { token_type_hint: "refresh_token" }).catch((e: unknown) => {
    o.log("session.discard_failed", { reason: e instanceof Error ? e.message : "unknown error" });
  });
  let timer: ReturnType<typeof setTimeout> | undefined;
  await Promise.race([revocation, new Promise<void>((done) => (timer = setTimeout(done, DISCARD_WAIT_MS)))]);
  clearTimeout(timer);
}

export async function revoke(o: Oidc, refreshToken: string): Promise<void> {
  try {
    await client.tokenRevocation(o.config, refreshToken, { token_type_hint: "refresh_token" });
  } catch (e) {
    throw new OidcFailure("revoke", e instanceof Error ? e.message : "unknown error");
  }
}

/** The provider's RP-initiated logout URL, returning to the app. */
export function endSessionUrl(o: Oidc): string {
  return client.buildEndSessionUrl(o.config, { client_id: o.config.clientMetadata().client_id, post_logout_redirect_uri: o.postLogoutRedirectUri }).toString();
}
