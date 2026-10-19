// The BFF's OIDC client (ADR-018, T-043): Authorization Code with PKCE (S256),
// state and nonce, through openid-client with the deployment's endpoints (no
// discovery). The ID token is checked (signature, iss, aud, exp, nonce, sub)
// and dropped; only the access and refresh tokens are kept, sealed in the
// session cookie. Server-side only.
import * as client from "openid-client";
import type { BffConfig } from "./config";
import { type Login, type Session, sessionToken } from "./session";

export type Oidc = {
  config: client.Configuration;
  redirectUri: string;
  postLogoutRedirectUri: string;
  audience: string;
  scopes: string;
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
 * How a failed refresh grant ends (ADR-018). The session ends when the
 * provider refused the token (invalid_grant, and any other OAuth error but
 * UNAVAILABLE's) or answered 2xx with something unusable (it may have spent
 * the token by then). It stays when the provider did not answer, timed out,
 * or answered 5xx, 429, UNAVAILABLE's codes or no OAuth error at all: the
 * token was not spent, and a later request tries again.
 */
function refusal(e: unknown): { refused: boolean; error?: string; status?: number } {
  if (e instanceof TypeError) return { refused: false };
  if (e instanceof client.ResponseBodyError) {
    return { refused: !(UNAVAILABLE.has(e.error) || e.status === 429 || e.status >= 500), error: e.error, status: e.status };
  }
  if (e instanceof client.WWWAuthenticateChallengeError) {
    const error = e.cause.map((c) => c.parameters.error).find((x) => typeof x === "string");
    return { refused: error !== undefined && !UNAVAILABLE.has(error) && e.status !== 429 && e.status < 500, error, status: e.status };
  }
  if (e instanceof client.ClientError) {
    if (e.code === "OAUTH_TIMEOUT" || e.code === "OAUTH_ABORT") return { refused: false };
    if ((e.code === "OAUTH_RESPONSE_IS_NOT_CONFORM" || e.code === "OAUTH_RESPONSE_IS_NOT_JSON") && e.cause instanceof Response) {
      return { refused: e.cause.status >= 200 && e.cause.status < 300, status: e.cause.status };
    }
  }
  return { refused: true };
}

/** fetchImpl replaces the network for the provider's endpoints (tests). */
export function newOidc(cfg: BffConfig, fetchImpl?: (url: string, init: RequestInit) => Promise<Response>): Oidc {
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
  if (fetchImpl !== undefined) config[client.customFetch] = (url, options) => fetchImpl(url, options as RequestInit);
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
    // Not kept, so not left live at the provider either.
    if (typeof tokens.refresh_token === "string" && tokens.refresh_token !== "") await discard(o, tokens.refresh_token);
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
    return { ...(await fields(o, tokens, null, now)), createdAt: now, lastSeenAt: now };
  } catch (e) {
    throw new OidcFailure("login", e instanceof Error ? e.message : "unknown error");
  }
}

/**
 * New tokens for s. With rotation the provider returns a new refresh token
 * and the old one is spent; a reused or revoked one is refused
 * (invalid_grant), which ends the session, as does an answer the session
 * cannot use. An outage is not a refusal: the session stays and a later
 * request tries again (refusal, OidcFailure.refused).
 */
export async function refresh(o: Oidc, s: Session, now: number): Promise<Session> {
  if (s.refreshToken === null) throw new OidcFailure("refresh", "the session has no refresh token");
  let tokens: client.TokenEndpointResponse;
  try {
    tokens = await client.refreshTokenGrant(o.config, s.refreshToken);
  } catch (e) {
    const { refused, ...answer } = refusal(e);
    throw new OidcFailure("refresh", e instanceof Error ? e.message : "unknown error", refused, answer);
  }
  try {
    return { ...s, ...(await fields(o, tokens, s.refreshToken, now)) };
  } catch (e) {
    // The old refresh token may be spent already: the session cannot go on.
    throw new OidcFailure("refresh", e instanceof Error ? e.message : "unknown error");
  }
}

/** Revokes a refresh token the session cannot keep; failures are ignored (it expires on its own). */
export async function discard(o: Oidc, refreshToken: string): Promise<void> {
  try {
    await client.tokenRevocation(o.config, refreshToken, { token_type_hint: "refresh_token" });
  } catch {
    // Best effort.
  }
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
