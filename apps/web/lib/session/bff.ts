// The BFF's request handlers (ADR-018, T-043): login, callback, logout and the
// same-origin API proxy. They take and return standard Request / Response, so
// the Next.js route files are one line each and the tests need no server.
// Server-side only.
import type { BffConfig } from "./config";
import { endSessionUrl, finishLogin, type Oidc, OidcFailure, refresh, revoke, startLogin } from "./oidc";
import type { SealKeys } from "./seal";
import { clearCookie, LOGIN_COOKIE, loginCookie, readLogin, readSession, SESSION_COOKIE, type Session, safeReturnTo, sessionCookie } from "./session";

export type BffDeps = {
  cfg: BffConfig;
  keys: SealKeys;
  oidc: Oidc;
  now: () => number;
  /** Reaches the Go API (the OIDC client has its own). */
  fetch: typeof fetch;
  /** Structured log of what happened; never tokens, cookies or bodies. */
  log: (event: string, fields?: Record<string, string | number>) => void;
};

/** Refresh when the access token has less than this left (ADR-018). */
export const REFRESH_MARGIN_MS = 60_000;
/** Write lastSeenAt back at most this often (the idle limit is 12 hours). */
const TOUCH_MS = 5 * 60_000;
/**
 * How long a refresh's result answers for the refresh token it spent.
 * Requests the browser sent before it stored the new cookie still carry the
 * old token; refreshing again would be a reuse, which rotation punishes by
 * revoking the session (ADR-018).
 */
export const REFRESH_REUSE_MS = 30_000;

const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";

/** A D-001 request ID for the BFF's own error responses. */
export function newRequestId(now: number): string {
  let time = "";
  for (let t = now, i = 0; i < 10; i++, t = Math.floor(t / 32)) time = CROCKFORD[t % 32] + time;
  const rand = crypto.getRandomValues(new Uint8Array(16));
  let r = "";
  for (const b of rand) r += CROCKFORD[b % 32];
  return `req_${time}${r}`;
}

type ErrorCode = "UNAUTHENTICATED" | "FORBIDDEN" | "INVALID_REQUEST" | "SERVICE_UNAVAILABLE";
const statusOf: Record<ErrorCode, number> = { UNAUTHENTICATED: 401, FORBIDDEN: 403, INVALID_REQUEST: 400, SERVICE_UNAVAILABLE: 503 };

/** An error in the API's envelope, so the client handles it like the API's own. */
export function apiError(code: ErrorCode, message: string, now: number, headers: [string, string][] = []): Response {
  const requestId = newRequestId(now);
  return new Response(JSON.stringify({ error: { code, message, request_id: requestId } }), {
    status: statusOf[code],
    headers: [["Content-Type", "application/json"], ["Cache-Control", "no-store"], ["X-Request-ID", requestId], ...headers],
  });
}

/** The value of the cookie named name, or undefined if absent or sent more than once. */
export function cookieValue(request: Request, name: string): string | undefined {
  const found = (request.headers.get("cookie") ?? "")
    .split(";")
    .map((c) => c.trim())
    .filter((c) => c.startsWith(`${name}=`))
    .map((c) => c.slice(name.length + 1));
  return found.length === 1 ? found[0] : undefined;
}

/**
 * CSRF (ADR-018): a state-changing request must come from the app's own
 * origin (Origin equal to it, and Sec-Fetch-Site same-origin when the
 * browser sends it) and carry X-Geminus-Request: 1, which a cross-site form
 * cannot set and a cross-site fetch cannot send without a preflight the BFF
 * never answers (no CORS).
 */
export function sameOrigin(request: Request, appOrigin: string): boolean {
  const site = request.headers.get("sec-fetch-site");
  return request.headers.get("origin") === appOrigin && (site === null || site === "same-origin") && request.headers.get("x-geminus-request") === "1";
}

const redirect = (location: string, cookies: string[] = []): Response =>
  new Response(null, { status: 303, headers: [["Location", location], ["Cache-Control", "no-store"], ...cookies.map((c): [string, string] => ["Set-Cookie", c])] });

/** One refresh per refresh token: concurrent and late requests share its result. */
type Refreshes = Map<string, { at: number; result: Promise<Session> }>;

async function digest(token: string): Promise<string> {
  return Buffer.from(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(token))).toString("hex");
}

async function refreshOnce(deps: BffDeps, refreshes: Refreshes, s: Session, now: number): Promise<Session> {
  for (const [k, v] of refreshes) if (now - v.at >= REFRESH_REUSE_MS) refreshes.delete(k);
  const key = s.refreshToken === null ? null : await digest(s.refreshToken);
  const known = key === null ? undefined : refreshes.get(key);
  if (known !== undefined) {
    // The session itself (its creation and idle times) is the caller's.
    const fresh = await known.result;
    return { ...s, accessToken: fresh.accessToken, refreshToken: fresh.refreshToken, accessExpiresAt: fresh.accessExpiresAt };
  }
  const result = refresh(deps.oidc, s, now);
  if (key !== null && refreshes.size < 10_000) {
    refreshes.set(key, { at: now, result });
    // A failed refresh is not remembered: the next request may try again.
    result.catch(() => refreshes.delete(key));
  }
  return result;
}

export function bff(deps: BffDeps) {
  const { cfg, keys, oidc } = deps;
  const refreshes: Refreshes = new Map();
  const failed = (reason: string) => redirect(`${cfg.appOrigin}/auth/error?reason=${reason}`, [clearCookie(LOGIN_COOKIE)]);

  return {
    /** GET /auth/login?return_to=/path: starts a login at the provider. */
    async login(request: Request): Promise<Response> {
      const returnTo = safeReturnTo(new URL(request.url).searchParams.get("return_to"));
      const { url, login } = await startLogin(oidc, returnTo, deps.now());
      return redirect(url, [await loginCookie(keys, login)]);
    },

    /** GET /auth/callback: the provider's redirect back. */
    async callback(request: Request): Promise<Response> {
      const now = deps.now();
      const login = await readLogin(keys, cookieValue(request, LOGIN_COOKIE), now);
      if (login === null) {
        deps.log("login.expired");
        return failed("expired");
      }
      const query = new URL(request.url).search;
      if (new URLSearchParams(query).has("error")) {
        deps.log("login.refused_by_provider");
        return failed("refused");
      }
      // The redirect URI is the configured one, never the request's host.
      let session: Session;
      try {
        session = await finishLogin(oidc, new URL(`${oidc.redirectUri}${query}`), login, now);
      } catch (e) {
        deps.log("login.failed", { reason: e instanceof OidcFailure ? e.reason : "unknown" });
        return failed("failed");
      }
      deps.log("login.succeeded");
      return redirect(`${cfg.appOrigin}${login.returnTo}`, [await sessionCookie(keys, session, now), clearCookie(LOGIN_COOKIE)]);
    },

    /**
     * POST /auth/logout: ends the session here, revokes the refresh token and
     * answers with the provider's logout URL for the page to go to.
     */
    async logout(request: Request): Promise<Response> {
      const now = deps.now();
      if (!sameOrigin(request, cfg.appOrigin)) return apiError("FORBIDDEN", "cross-site request refused", now);
      const session = await readSession(keys, cookieValue(request, SESSION_COOKIE), now);
      if (session?.refreshToken) {
        try {
          await revoke(oidc, session.refreshToken);
        } catch (e) {
          // The cookie is cleared anyway; the token expires on its own.
          deps.log("logout.revoke_failed", { reason: e instanceof OidcFailure ? e.reason : "unknown" });
        }
      }
      return new Response(JSON.stringify({ redirect: endSessionUrl(oidc) }), {
        status: 200,
        headers: [["Content-Type", "application/json"], ["Cache-Control", "no-store"], ["Set-Cookie", clearCookie(SESSION_COOKIE)]],
      });
    },

    /** Any method on /api/v1/<path>: forwarded to the Go API with the session's token. */
    async proxy(request: Request, path: string[]): Promise<Response> {
      return proxy(deps, refreshes, request, path);
    },
  };
}

// What the browser may send to the API, and what of the API's answer it gets.
const requestHeaders = ["accept", "content-type", "if-match", "idempotency-key", "x-organization-id", "last-event-id"];
// Not Location: a redirect from the API would name its internal address.
const responseHeaders = ["content-type", "etag", "x-request-id", "retry-after", "content-disposition", "cache-control"];
// A segment cannot start with ".", so "." and ".." are not segments.
const SEGMENT = /^[A-Za-z0-9_~-][A-Za-z0-9._~-]*$/;
const methods = new Set(["GET", "HEAD", "POST", "PATCH", "PUT", "DELETE"]);

async function proxy(deps: BffDeps, refreshes: Refreshes, request: Request, path: string[]): Promise<Response> {
  const { cfg, keys } = deps;
  const now = deps.now();
  if (!methods.has(request.method)) return apiError("INVALID_REQUEST", "method not allowed", now);
  if (request.method !== "GET" && request.method !== "HEAD" && !sameOrigin(request, cfg.appOrigin)) {
    return apiError("FORBIDDEN", "cross-site request refused", now);
  }
  // Each segment is one path segment of the API: no "." or "..", no
  // separators smuggled in encoded, nothing to reach outside /v1.
  if (path.length === 0 || path.some((s) => !SEGMENT.test(s))) {
    return apiError("INVALID_REQUEST", "invalid API path", now);
  }
  let session = await readSession(keys, cookieValue(request, SESSION_COOKIE), now);
  if (session === null) return apiError("UNAUTHENTICATED", "not signed in", now, [["Set-Cookie", clearCookie(SESSION_COOKIE)]]);
  let write = now - session.lastSeenAt >= TOUCH_MS;
  if (session.accessExpiresAt - now < REFRESH_MARGIN_MS) {
    try {
      session = await refreshOnce(deps, refreshes, session, now);
      write = true;
    } catch (e) {
      deps.log("session.refresh_failed", { reason: e instanceof OidcFailure ? e.reason : "unknown" });
      return apiError("UNAUTHENTICATED", "the session has ended", now, [["Set-Cookie", clearCookie(SESSION_COOKIE)]]);
    }
  }
  session = { ...session, lastSeenAt: write ? now : session.lastSeenAt };

  const target = `${cfg.apiUrl}/v1/${path.join("/")}${new URL(request.url).search}`;
  const headers = new Headers();
  for (const h of requestHeaders) {
    const v = request.headers.get(h);
    if (v !== null) headers.set(h, v);
  }
  headers.set("authorization", `Bearer ${session.accessToken}`);
  const hasBody = request.method !== "GET" && request.method !== "HEAD" && request.body !== null;
  let upstream: Response;
  try {
    upstream = await deps.fetch(target, {
      method: request.method,
      headers,
      body: hasBody ? request.body : undefined,
      // Streams the upload instead of buffering it (node fetch needs this).
      ...(hasBody ? { duplex: "half" } : {}),
      redirect: "manual",
      signal: request.signal,
    } as RequestInit);
  } catch {
    deps.log("proxy.unavailable");
    return apiError("SERVICE_UNAVAILABLE", "the API is unavailable", now);
  }
  const out = new Headers();
  for (const h of responseHeaders) {
    const v = upstream.headers.get(h);
    if (v !== null) out.set(h, v);
  }
  if (write) out.append("set-cookie", await sessionCookie(keys, session, now));
  return new Response(upstream.body, { status: upstream.status, headers: out });
}
