// The BFF (ADR-018, T-043; TEST-032): login, callback, logout and the API
// proxy against an in-process provider (tests/fake-oidc.ts) and a recorded
// API. No network, no real provider.
import { beforeEach, describe, expect, it } from "vitest";
import { apiError, bff, type BffDeps, cookieValue, newRequestId, REFRESH_MARGIN_MS, REFRESH_REUSE_MS, RefreshCache, sameOrigin } from "../lib/session/bff";
import { BffConfigError, loadBffConfig } from "../lib/session/config";
import { newOidc } from "../lib/session/oidc";
import { parseSealKeys, seal } from "../lib/session/seal";
import { LOGIN_COOKIE, LOGIN_MS, MAX_TOKEN, readSession, SESSION_COOKIE, type Session, sessionCookie } from "../lib/session/session";
import { AUDIENCE, CLIENT_ID, CLIENT_SECRET, ENDPOINTS, FakeProvider, type IdTokenChange, ISSUER } from "./fake-oidc";

const APP = "https://app.test.invalid";
const API = "https://api.internal.test.invalid/base";
const KEY = Buffer.alloc(32, 7).toString("base64url");
const env: Record<string, string> = {
  BFF_APP_ORIGIN: APP,
  BFF_API_URL: API,
  BFF_OIDC_ISSUER: ISSUER,
  BFF_OIDC_AUTHORIZATION_URL: ENDPOINTS.authorization,
  BFF_OIDC_TOKEN_URL: ENDPOINTS.token,
  BFF_OIDC_REVOCATION_URL: ENDPOINTS.revocation,
  BFF_OIDC_END_SESSION_URL: ENDPOINTS.endSession,
  BFF_OIDC_JWKS_URL: ENDPOINTS.jwks,
  BFF_OIDC_CLIENT_ID: CLIENT_ID,
  BFF_OIDC_CLIENT_SECRET: CLIENT_SECRET,
  BFF_OIDC_AUDIENCE: AUDIENCE,
  SESSION_KEYS: `k1:${KEY}`,
};
const REQUEST_ID = /^req_[0-7][0-9A-HJKMNP-TV-Z]{25}$/;

type Env = {
  provider: FakeProvider;
  deps: BffDeps;
  h: ReturnType<typeof bff>;
  api: { requests: Request[]; inits: RequestInit[]; respond: (r: Request) => Response | Promise<Response>; fail: boolean };
  logs: string[];
  clock: { now: number };
};

let e: Env;

beforeEach(async () => {
  const provider = new FakeProvider();
  const cfg = loadBffConfig(env);
  const clock = { now: Date.now() };
  const logs: string[] = [];
  const api: Env["api"] = { requests: [], inits: [], respond: () => new Response("{}", { status: 200, headers: { "content-type": "application/json" } }), fail: false };
  const deps: BffDeps = {
    cfg,
    keys: await parseSealKeys(env.SESSION_KEYS),
    oidc: newOidc(cfg, provider.fetch),
    now: () => clock.now,
    fetch: async (input, init) => {
      const r = new Request(input as string, init);
      api.requests.push(r);
      api.inits.push(init as RequestInit);
      if (api.fail) throw new TypeError("connection refused");
      return api.respond(r);
    },
    log: (event, fields) => logs.push(fields === undefined ? event : `${event} ${JSON.stringify(fields)}`),
  };
  e = { provider, deps, h: bff(deps), api, logs, clock };
});

const valueOf = (setCookie: string) => setCookie.slice(setCookie.indexOf("=") + 1, setCookie.indexOf(";"));
const cookieNamed = (r: Response, name: string) => r.headers.getSetCookie().find((c) => c.startsWith(`${name}=`));
const allHeaders = (r: Response) => [...r.headers.entries()].map(([k, v]) => `${k}: ${v}`).join("\n");

async function startLogin(returnTo = "/projects") {
  const res = await e.h.login(new Request(`${APP}/auth/login?return_to=${encodeURIComponent(returnTo)}`));
  const login = valueOf(cookieNamed(res, LOGIN_COOKIE) as string);
  return { res, login, ...e.provider.authorize(res.headers.get("location") as string) };
}

async function callback(query: string, login: string | null) {
  return e.h.callback(new Request(`${APP}/auth/callback?${query}`, { headers: login === null ? {} : { cookie: `${LOGIN_COOKIE}=${login}` } }));
}

/** Signs in and returns the session cookie value. */
async function signIn(): Promise<string> {
  const { login, code, state } = await startLogin();
  const res = await callback(`code=${code}&state=${state}`, login);
  return valueOf(cookieNamed(res, SESSION_COOKIE) as string);
}

const sessionOf = (value: string) => readSession(e.deps.keys, value, e.clock.now);

describe("configuration", () => {
  it("takes the deployment's endpoints", () => {
    const cfg = loadBffConfig(env);
    expect(cfg.appOrigin).toBe(APP);
    expect(cfg.apiUrl).toBe(API);
    expect(cfg.oidc.issuer).toBe(ISSUER);
    expect(cfg.oidc.scopes).toBe("openid offline_access");
    expect(cfg.loopbackHttp).toBe(false);
  });

  it.each([
    ["BFF_APP_ORIGIN", "https://app.test.invalid/path"],
    ["BFF_APP_ORIGIN", "http://app.test.invalid"],
    ["BFF_API_URL", "https://api.test.invalid/?x=1"],
    ["BFF_API_URL", "https://user:pw@api.test.invalid"],
    ["BFF_OIDC_TOKEN_URL", "https://id.test.invalid/token#frag"],
    ["BFF_OIDC_TOKEN_URL", "ftp://id.test.invalid/token"],
    ["BFF_OIDC_JWKS_URL", "not a url"],
    ["BFF_OIDC_ISSUER", ""],
    ["BFF_OIDC_CLIENT_SECRET", ""],
    ["BFF_OIDC_CLIENT_ID", "with space"],
    ["BFF_OIDC_AUDIENCE", ""],
    ["BFF_OIDC_SCOPES", "profile email"],
    ["BFF_OIDC_SCOPES", "openid openid"],
    ["SESSION_KEYS", ""],
  ])("refuses %s=%s without quoting a value", (name, value) => {
    const err = (() => {
      try {
        loadBffConfig({ ...env, [name]: value });
      } catch (x) {
        return x;
      }
      return null;
    })();
    expect(err).toBeInstanceOf(BffConfigError);
    expect((err as BffConfigError).problems.join(" ")).toContain(name);
    expect(String(err)).not.toContain(CLIENT_SECRET);
    expect(String(err)).not.toContain(KEY);
  });

  it("allows plain http only when everything is on loopback", () => {
    const local = Object.fromEntries(Object.entries(env).map(([k, v]) => [k, v.replace(/^https:\/\/[^/]+/, "http://127.0.0.1:9")]));
    expect(loadBffConfig(local).loopbackHttp).toBe(true);
    expect(() => loadBffConfig({ ...env, BFF_API_URL: "http://127.0.0.1:8080" })).toThrow(BffConfigError);
  });
});

describe("login", () => {
  it("sends the browser to the provider with PKCE, state and nonce", async () => {
    const { res, params, login } = await startLogin("/models/x?tab=1");
    expect(res.status).toBe(303);
    expect(res.headers.get("cache-control")).toBe("no-store");
    const to = new URL(res.headers.get("location") as string);
    expect(`${to.origin}${to.pathname}`).toBe(ENDPOINTS.authorization);
    expect(params.get("response_type")).toBe("code");
    expect(params.get("client_id")).toBe(CLIENT_ID);
    expect(params.get("redirect_uri")).toBe(`${APP}/auth/callback`);
    expect(params.get("scope")).toBe("openid offline_access");
    expect(params.get("audience")).toBe(AUDIENCE);
    expect(params.get("code_challenge_method")).toBe("S256");
    expect(params.get("code_challenge")).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(params.get("state")).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(params.get("nonce")).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(params.get("client_secret")).toBeNull();
    expect(to.toString()).not.toContain(CLIENT_SECRET);
    // The verifier stays in the sealed login cookie.
    expect(cookieNamed(res, LOGIN_COOKIE)).toMatch(/; Path=\/; HttpOnly; Secure; SameSite=Lax; Max-Age=600$/);
    expect(login).toMatch(/^v1\.k1\./);
  });

  it("each login has its own state, nonce and verifier", async () => {
    const a = await startLogin();
    const b = await startLogin();
    for (const p of ["state", "nonce", "code_challenge"]) expect(a.params.get(p)).not.toBe(b.params.get(p));
  });

  it("returns to a percent-encoded path, and to / for a raw non-ASCII one", async () => {
    for (const [to, want] of [
      ["/projects/%E4%B8%AD", "/projects/%E4%B8%AD"],
      ["/projects/\u4e2d", "/"],
    ]) {
      const { login, code, state } = await startLogin(to);
      const res = await callback(`code=${code}&state=${state}`, login);
      expect(res.status).toBe(303);
      expect(res.headers.get("location")).toBe(`${APP}${want}`);
      expect(cookieNamed(res, SESSION_COOKIE)).toBeDefined();
    }
  });

  it.each(["//evil.example", "https://evil.example", "/\\evil.example"])("does not return to %s", async (to) => {
    const { login, code, state } = await startLogin(to);
    const res = await callback(`code=${code}&state=${state}`, login);
    expect(res.headers.get("location")).toBe(`${APP}/`);
  });
});

describe("callback", () => {
  it("keeps the access and refresh tokens sealed and drops the ID token", async () => {
    const { login, code, state } = await startLogin("/projects?x=1");
    const res = await callback(`code=${code}&state=${state}`, login);
    expect(res.status).toBe(303);
    expect(res.headers.get("location")).toBe(`${APP}/projects?x=1`);
    expect(await res.text()).toBe("");
    const set = cookieNamed(res, SESSION_COOKIE) as string;
    expect(set).toMatch(/; Path=\/; HttpOnly; Secure; SameSite=Lax; Max-Age=\d+$/);
    expect(set).not.toMatch(/Domain=/i);
    expect(cookieNamed(res, LOGIN_COOKIE)).toMatch(/=; .*Max-Age=0$/);
    const s = (await sessionOf(valueOf(set))) as Session;
    expect(s.accessToken).toMatch(/^at-/);
    expect(s.refreshToken).not.toBeNull();
    expect(s.accessExpiresAt).toBe(e.clock.now + 600_000);
    // Nothing readable leaves the server: no token in any header but sealed.
    expect(allHeaders(res)).not.toContain(s.accessToken);
    expect(allHeaders(res)).not.toContain(s.refreshToken as string);
    expect(Object.keys(s).sort()).toEqual(["accessExpiresAt", "accessToken", "createdAt", "lastSeenAt", "refreshToken"]);
    expect(e.logs).toContain("login.succeeded");
  });

  const refused = async (res: Response, reason: string) => {
    expect(res.status).toBe(303);
    expect(res.headers.get("location")).toBe(`${APP}/auth/error?reason=${reason}`);
    expect(cookieNamed(res, SESSION_COOKIE)).toBeUndefined();
    expect(cookieNamed(res, LOGIN_COOKIE)).toMatch(/Max-Age=0$/);
  };

  it("sends the configured redirect URI, whatever host the request came in on", async () => {
    const { login, code, state } = await startLogin();
    // Behind a proxy the request may name an internal host.
    const res = await e.h.callback(new Request(`http://10.0.0.7:3000/auth/callback?code=${code}&state=${state}`, { headers: { cookie: `${LOGIN_COOKIE}=${login}` } }));
    expect(res.headers.get("location")).toBe(`${APP}/projects`);
  });

  it("refuses a callback without its login, or after ten minutes", async () => {
    const { login, code, state } = await startLogin();
    await refused(await callback(`code=${code}&state=${state}`, null), "expired");
    e.clock.now += LOGIN_MS;
    await refused(await callback(`code=${code}&state=${state}`, login), "expired");
    expect(e.provider.tokenCalls("authorization_code")).toBe(0);
  });

  it("refuses another login's state", async () => {
    const a = await startLogin();
    const b = await startLogin();
    await refused(await callback(`code=${b.code}&state=${b.state}`, a.login), "failed");
    expect(e.provider.tokenCalls("authorization_code")).toBe(0);
  });

  it("refuses a code issued for another login's PKCE challenge", async () => {
    const a = await startLogin();
    const b = await startLogin();
    // b's code and challenge with a's state and verifier: the provider's PKCE check fails.
    await refused(await callback(`code=${b.code}&state=${a.state}`, a.login), "failed");
    expect(e.provider.tokenCalls("authorization_code")).toBe(1);
  });

  it("refuses a code used twice", async () => {
    const { login, code, state } = await startLogin();
    expect((await callback(`code=${code}&state=${state}`, login)).headers.get("location")).toBe(`${APP}/projects`);
    await refused(await callback(`code=${code}&state=${state}`, login), "failed");
  });

  it("refuses the provider's error", async () => {
    const { login, state } = await startLogin();
    await refused(await callback(`error=access_denied&state=${state}`, login), "refused");
  });

  it.each<[string, IdTokenChange]>([
    ["another nonce", { nonce: "other-nonce" }],
    ["another issuer", { iss: "https://evil.test.invalid/" }],
    ["another audience", { aud: "other-client" }],
    ["no subject", { sub: null }],
    // oauth4webapi only checks that sub is a string; the BFF refuses "".
    ["an empty subject", { sub: "" }],
    ["an expired ID token", { exp: Math.floor(Date.now() / 1000) - 3600, iat: Math.floor(Date.now() / 1000) - 7200 }],
    ["a signature by another key", { badSignature: true }],
  ])("refuses an ID token with %s", async (_, change) => {
    e.provider.idToken = change;
    const { login, code, state } = await startLogin();
    await refused(await callback(`code=${code}&state=${state}`, login), "failed");
  });

  it("refuses a token response without expires_in", async () => {
    e.provider.expiresIn = undefined;
    const { login, code, state } = await startLogin();
    await refused(await callback(`code=${code}&state=${state}`, login), "failed");
  });

  it.each<[string, number, number | undefined]>([
    ["an access token over the session's limit", MAX_TOKEN + 1, undefined],
    ["a refresh token over the session's limit", 40, MAX_TOKEN + 1],
    ["tokens too large together for the cookie", MAX_TOKEN, MAX_TOKEN],
  ])("refuses %s, and says so in the log", async (_, access, refreshLength) => {
    e.provider.accessTokenLength = access;
    e.provider.refreshTokenLength = refreshLength;
    const { login, code, state } = await startLogin();
    await refused(await callback(`code=${code}&state=${state}`, login), "failed");
    expect(e.logs.some((l) => l.startsWith("login.failed"))).toBe(true);
    expect(e.logs).not.toContain("login.succeeded");
  });

  it("logs why without tokens", async () => {
    e.provider.idToken = { nonce: "other-nonce" };
    const { login, code, state } = await startLogin();
    await callback(`code=${code}&state=${state}`, login);
    expect(e.logs.some((l) => l.startsWith("login.failed"))).toBe(true);
    expect(e.logs.join("\n")).not.toMatch(/at-|eyJ/);
  });
});

const csrf = { origin: APP, "x-geminus-request": "1", "sec-fetch-site": "same-origin" };

describe("CSRF", () => {
  it.each([
    [{}, false],
    [{ origin: APP }, false],
    [{ "x-geminus-request": "1" }, false],
    [{ origin: "https://evil.example", "x-geminus-request": "1" }, false],
    [{ origin: `${APP}/`, "x-geminus-request": "1" }, false],
    [{ origin: "null", "x-geminus-request": "1" }, false],
    [{ origin: APP, "x-geminus-request": "true" }, false],
    [{ origin: APP, "x-geminus-request": "1", "sec-fetch-site": "cross-site" }, false],
    [{ origin: APP, "x-geminus-request": "1", "sec-fetch-site": "same-site" }, false],
    [{ origin: APP, "x-geminus-request": "1" }, true],
    [csrf, true],
  ])("%j → %s", (headers, ok) => {
    expect(sameOrigin(new Request(`${APP}/x`, { method: "POST", headers }), APP)).toBe(ok);
  });
});

describe("logout", () => {
  it("revokes the refresh token, clears the cookie and returns the provider's logout URL", async () => {
    const value = await signIn();
    const s = (await sessionOf(value)) as Session;
    const res = await e.h.logout(new Request(`${APP}/auth/logout`, { method: "POST", headers: { ...csrf, cookie: `${SESSION_COOKIE}=${value}` } }));
    expect(res.status).toBe(200);
    expect(cookieNamed(res, SESSION_COOKIE)).toBe(`${SESSION_COOKIE}=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=0`);
    expect(e.provider.revoked).toEqual([s.refreshToken]);
    expect(e.provider.refreshState(s.refreshToken as string)).toBe("revoked");
    const to = new URL(((await res.json()) as { redirect: string }).redirect);
    expect(`${to.origin}${to.pathname}`).toBe(ENDPOINTS.endSession);
    expect(to.searchParams.get("post_logout_redirect_uri")).toBe(`${APP}/`);
    expect(to.searchParams.get("client_id")).toBe(CLIENT_ID);
  });

  it("is refused cross-site and then revokes nothing", async () => {
    const value = await signIn();
    const tries: Record<string, string>[] = [{ cookie: `${SESSION_COOKIE}=${value}` }, { origin: "https://evil.example", "x-geminus-request": "1", cookie: `${SESSION_COOKIE}=${value}` }];
    for (const headers of tries) {
      const res = await e.h.logout(new Request(`${APP}/auth/logout`, { method: "POST", headers }));
      expect(res.status).toBe(403);
      expect(cookieNamed(res, SESSION_COOKIE)).toBeUndefined();
    }
    expect(e.provider.revoked).toEqual([]);
  });

  it("still ends the session when revocation fails", async () => {
    const value = await signIn();
    e.provider.failRevocation = true;
    const res = await e.h.logout(new Request(`${APP}/auth/logout`, { method: "POST", headers: { ...csrf, cookie: `${SESSION_COOKIE}=${value}` } }));
    expect(res.status).toBe(200);
    expect(cookieNamed(res, SESSION_COOKIE)).toMatch(/Max-Age=0$/);
    expect(e.logs.some((l) => l.startsWith("logout.revoke_failed"))).toBe(true);
  });
});

describe("proxy", () => {
  const get = (path: string, value: string | null, headers: Record<string, string> = {}) =>
    new Request(`${APP}/api/v1/${path}`, { headers: { ...headers, ...(value === null ? {} : { cookie: `${SESSION_COOKIE}=${value}` }) } });

  it("answers 401 in the API's envelope without a session", async () => {
    for (const value of [null, "garbage", await seal(e.deps.keys, LOGIN_COOKIE, {})]) {
      const res = await e.h.proxy(get("projects", value), ["projects"]);
      expect(res.status).toBe(401);
      expect(cookieNamed(res, SESSION_COOKIE)).toMatch(/Max-Age=0$/);
      const body = (await res.json()) as { error: { code: string; request_id: string } };
      expect(body.error.code).toBe("UNAUTHENTICATED");
      expect(body.error.request_id).toMatch(REQUEST_ID);
      expect(res.headers.get("x-request-id")).toBe(body.error.request_id);
    }
    expect(e.api.requests).toHaveLength(0);
  });

  it("forwards with the session's token and nothing of the browser's credentials", async () => {
    const value = await signIn();
    const s = (await sessionOf(value)) as Session;
    e.api.respond = () =>
      new Response('{"items":[]}', {
        status: 200,
        headers: { "content-type": "application/json", etag: '"draft-revision-3"', "x-request-id": "req_01M4YDQK00TA00000000000001", "set-cookie": "api=1", location: "http://10.0.0.5/v1/x", "x-internal": "1" },
      });
    const res = await e.h.proxy(
      get("projects?limit=5&cursor=abc", value, { "x-organization-id": "org_01M4YDQK00TA00000000000000", authorization: "Bearer stolen", "x-forwarded-for": "1.2.3.4", accept: "application/json" }),
      ["projects"],
    );
    expect(res.status).toBe(200);
    expect(await res.text()).toBe('{"items":[]}');
    const sent = e.api.requests[0] as Request;
    expect(sent.url).toBe(`${API}/v1/projects?limit=5&cursor=abc`);
    expect(sent.method).toBe("GET");
    expect(sent.headers.get("authorization")).toBe(`Bearer ${s.accessToken}`);
    expect(sent.headers.get("x-organization-id")).toBe("org_01M4YDQK00TA00000000000000");
    expect(sent.headers.get("cookie")).toBeNull();
    expect(sent.headers.get("x-forwarded-for")).toBeNull();
    expect(res.headers.get("etag")).toBe('"draft-revision-3"');
    expect(res.headers.get("x-request-id")).toBe("req_01M4YDQK00TA00000000000001");
    expect(res.headers.get("location")).toBeNull();
    expect(res.headers.get("x-internal")).toBeNull();
    expect(res.headers.getSetCookie()).toEqual([]);
    expect(allHeaders(res)).not.toContain(s.accessToken);
  });

  it("refuses a state-changing request from anywhere but the app", async () => {
    const value = await signIn();
    const tries: Record<string, string>[] = [{}, { origin: APP }, { origin: "https://evil.example", "x-geminus-request": "1" }, { ...csrf, "sec-fetch-site": "cross-site" }];
    for (const headers of tries) {
      const res = await e.h.proxy(new Request(`${APP}/api/v1/projects`, { method: "POST", body: "{}", headers: { ...headers, cookie: `${SESSION_COOKIE}=${value}` } }), ["projects"]);
      expect(res.status).toBe(403);
    }
    expect(e.api.requests).toHaveLength(0);
  });

  it("forwards a same-origin write with its body and preconditions", async () => {
    const value = await signIn();
    e.api.respond = async (r) => new Response(await r.text(), { status: 201, headers: { "content-type": "application/json" } });
    const res = await e.h.proxy(
      new Request(`${APP}/api/v1/models/mdl_x/draft`, {
        method: "PATCH",
        body: '[{"op":"test","path":"/name","value":"M"}]',
        headers: { ...csrf, cookie: `${SESSION_COOKIE}=${value}`, "content-type": "application/json-patch+json", "if-match": '"draft-revision-1"', "idempotency-key": "key-12345678" },
      }),
      ["models", "mdl_x", "draft"],
    );
    expect(res.status).toBe(201);
    expect(await res.text()).toBe('[{"op":"test","path":"/name","value":"M"}]');
    const sent = e.api.requests[0] as Request;
    expect(sent.method).toBe("PATCH");
    expect(sent.headers.get("content-type")).toBe("application/json-patch+json");
    expect(sent.headers.get("if-match")).toBe('"draft-revision-1"');
    expect(sent.headers.get("idempotency-key")).toBe("key-12345678");
    expect(sent.headers.get("x-geminus-request")).toBeNull();
    expect(sent.headers.get("origin")).toBeNull();
  });

  it.each([[[]], [[".."]], [["projects", ".."]], [["."]], [["a/b"]], [["a\\b"]], [["%2e%2e"]], [[""]], [["projects", "x?y"]]])("refuses the path %j", async (path) => {
    const value = await signIn();
    const res = await e.h.proxy(get("x", value), path);
    expect(res.status).toBe(400);
    expect(e.api.requests).toHaveLength(0);
  });

  it("answers 503 when the API does not answer", async () => {
    const value = await signIn();
    e.api.fail = true;
    const res = await e.h.proxy(get("projects", value), ["projects"]);
    expect(res.status).toBe(503);
    expect(((await res.json()) as { error: { code: string } }).error.code).toBe("SERVICE_UNAVAILABLE");
  });

  it("answers with nosniff, and never lets a cache store a response that sets the session", async () => {
    const value = await signIn();
    e.api.respond = () => new Response("{}", { status: 200, headers: { "content-type": "application/json", "cache-control": "public, max-age=60" } });
    const plain = await e.h.proxy(get("projects", value), ["projects"]);
    expect(plain.headers.get("x-content-type-options")).toBe("nosniff");
    expect(plain.headers.get("cache-control")).toBe("public, max-age=60");
    e.clock.now += 5 * 60_000;
    const touched = await e.h.proxy(get("projects", value), ["projects"]);
    expect(cookieNamed(touched, SESSION_COOKIE)).toBeDefined();
    expect(touched.headers.get("cache-control")).toBe("no-store");
    expect(touched.headers.get("x-content-type-options")).toBe("nosniff");
  });

  // TRACE and CONNECT cannot even be built as a Request (forbidden methods).
  it.each(["OPTIONS", "PROPFIND", "PURGE"])("refuses the method %s without calling the API", async (method) => {
    const value = await signIn();
    const res = await e.h.proxy(new Request(`${APP}/api/v1/projects`, { method, headers: { ...csrf, cookie: `${SESSION_COOKIE}=${value}` } }), ["projects"]);
    expect(res.status).toBe(400);
    expect(e.api.requests).toHaveLength(0);
  });

  it("streams an upload to the API without buffering it", async () => {
    const value = await signIn();
    const bytes = new Uint8Array(256 * 1024).map((_, i) => i % 251);
    e.api.respond = async (r) => new Response(new Uint8Array(await r.arrayBuffer()), { status: 201 });
    const res = await e.h.proxy(
      new Request(`${APP}/api/v1/imports`, { method: "POST", body: new Blob([bytes]).stream(), duplex: "half", headers: { ...csrf, cookie: `${SESSION_COOKIE}=${value}`, "content-type": "text/csv" } } as RequestInit),
      ["imports"],
    );
    expect(res.status).toBe(201);
    expect(new Uint8Array(await res.arrayBuffer())).toEqual(bytes);
    const init = e.api.inits[0] as RequestInit & { duplex?: string };
    expect(init.body).toBeInstanceOf(ReadableStream);
    expect(init.duplex).toBe("half");
  });

  it("streams the API's answer: an event arrives before the stream ends", async () => {
    const value = await signIn();
    let upstream: ReadableStreamDefaultController<Uint8Array> | undefined;
    const events = new ReadableStream<Uint8Array>({
      start(c) {
        upstream = c;
      },
    });
    const push = (s: string) => upstream?.enqueue(new TextEncoder().encode(s));
    const end = () => upstream?.close();
    e.api.respond = () => new Response(events, { status: 200, headers: { "content-type": "text/event-stream" } });
    const pending = e.h.proxy(get("runs/run_x/events", value, { "last-event-id": "41" }), ["runs", "run_x", "events"]);
    push("id: 42\ndata: {}\n\n");
    const res = await Promise.race([pending, new Promise<never>((_, no) => setTimeout(() => no(new Error("the proxy waited for the stream to end")), 2000))]);
    expect(res.headers.get("content-type")).toBe("text/event-stream");
    expect((e.api.requests[0] as Request).headers.get("last-event-id")).toBe("41");
    const reader = (res.body as ReadableStream<Uint8Array>).getReader();
    expect(new TextDecoder().decode((await reader.read()).value)).toBe("id: 42\ndata: {}\n\n");
    end();
    expect((await reader.read()).done).toBe(true);
  });

  it("passes the browser's cancellation on to the API", async () => {
    const value = await signIn();
    const browser = new AbortController();
    await e.h.proxy(new Request(`${APP}/api/v1/projects`, { signal: browser.signal, headers: { cookie: `${SESSION_COOKIE}=${value}` } }), ["projects"]);
    const sent = (e.api.inits[0] as RequestInit).signal as AbortSignal;
    expect(sent.aborted).toBe(false);
    browser.abort();
    expect(sent.aborted).toBe(true);
  });

  it("does not follow the API's redirects", async () => {
    const value = await signIn();
    e.api.respond = () => new Response(null, { status: 302, headers: { location: "http://10.0.0.5/v1/elsewhere" } });
    const res = await e.h.proxy(get("projects", value), ["projects"]);
    expect((e.api.inits[0] as RequestInit).redirect).toBe("manual");
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBeNull();
    expect(e.api.requests).toHaveLength(1);
  });

  it("writes the idle time back only every five minutes", async () => {
    const value = await signIn();
    expect((await e.h.proxy(get("projects", value), ["projects"])).headers.getSetCookie()).toEqual([]);
    e.clock.now += 5 * 60_000;
    const res = await e.h.proxy(get("projects", value), ["projects"]);
    const fresh = (await sessionOf(valueOf(cookieNamed(res, SESSION_COOKIE) as string))) as Session;
    expect(fresh.lastSeenAt).toBe(e.clock.now);
  });

  describe("refresh", () => {
    /** A signed-in session whose access token has `left` ms to live. */
    async function expiring(left: number): Promise<{ value: string; s: Session }> {
      const value = await signIn();
      const s = { ...((await sessionOf(value)) as Session), accessExpiresAt: e.clock.now + left };
      return { value: valueOf(await sessionCookie(e.deps.keys, s, e.clock.now)), s };
    }

    it("refreshes under a minute left and keeps the rotated token", async () => {
      const { value, s } = await expiring(REFRESH_MARGIN_MS - 1);
      const res = await e.h.proxy(get("projects", value), ["projects"]);
      expect(res.status).toBe(200);
      const fresh = (await sessionOf(valueOf(cookieNamed(res, SESSION_COOKIE) as string))) as Session;
      expect(fresh.accessToken).not.toBe(s.accessToken);
      expect(fresh.refreshToken).not.toBe(s.refreshToken);
      expect(fresh.createdAt).toBe(s.createdAt);
      expect((e.api.requests[0] as Request).headers.get("authorization")).toBe(`Bearer ${fresh.accessToken}`);
      expect(e.provider.refreshState(s.refreshToken as string)).toBe("spent");
    });

    it("does not refresh with a minute or more left", async () => {
      const { value } = await expiring(REFRESH_MARGIN_MS);
      await e.h.proxy(get("projects", value), ["projects"]);
      expect(e.provider.tokenCalls("refresh_token")).toBe(0);
    });

    it("refreshes once for concurrent and late requests with the old token", async () => {
      const { value } = await expiring(1000);
      const responses = await Promise.all([1, 2, 3].map(() => e.h.proxy(get("projects", value), ["projects"])));
      expect(responses.map((r) => r.status)).toEqual([200, 200, 200]);
      e.clock.now += REFRESH_REUSE_MS - 1;
      const late = await e.h.proxy(get("projects", value), ["projects"]);
      expect(late.status).toBe(200);
      expect(e.provider.tokenCalls("refresh_token")).toBe(1);
      const tokens = new Set(e.api.requests.map((r) => r.headers.get("authorization")));
      expect(tokens.size).toBe(1);
    });

    it("ends the session when the provider refuses the refresh (reuse or revocation)", async () => {
      const { value, s } = await expiring(1000);
      // Another BFF instance already spent this token: a reuse.
      await e.provider.fetch(ENDPOINTS.token, {
        method: "POST",
        body: new URLSearchParams({ grant_type: "refresh_token", refresh_token: s.refreshToken as string, client_id: CLIENT_ID, client_secret: CLIENT_SECRET }).toString(),
      });
      const res = await e.h.proxy(get("projects", value), ["projects"]);
      expect(res.status).toBe(401);
      expect(cookieNamed(res, SESSION_COOKIE)).toMatch(/Max-Age=0$/);
      expect(e.api.requests).toHaveLength(0);
      // Reuse revoked the whole family at the provider.
      expect(e.provider.refreshState(s.refreshToken as string)).toBe("revoked");
    });

    it("refreshes again after the reuse window, which the provider refuses as a reuse", async () => {
      const { value } = await expiring(1000);
      expect((await e.h.proxy(get("projects", value), ["projects"])).status).toBe(200);
      e.clock.now += REFRESH_REUSE_MS;
      // The browser kept the old cookie past the window: its token is spent.
      const res = await e.h.proxy(get("projects", value), ["projects"]);
      expect(res.status).toBe(401);
      expect(e.provider.tokenCalls("refresh_token")).toBe(2);
    });

    it.each<[string, (p: FakeProvider) => void]>([
      ["answers 503", (p) => (p.failRefresh = true)],
      ["cannot be reached", (p) => (p.unreachableRefresh = true)],
      // An OAuth error that is not a refusal of the token: Auth0's rate limit.
      ["answers 429 too_many_requests", (p) => (p.refreshError = [429, "too_many_requests"])],
    ])("keeps the session when the provider %s, and the next request tries again", async (_, down) => {
      const { value } = await expiring(1000);
      down(e.provider);
      const res = await e.h.proxy(get("projects", value), ["projects"]);
      expect(res.status).toBe(503);
      expect(((await res.json()) as { error: { code: string } }).error.code).toBe("SERVICE_UNAVAILABLE");
      // No Set-Cookie: the browser keeps the cookie it sent.
      expect(res.headers.getSetCookie()).toEqual([]);
      expect(e.api.requests).toHaveLength(0);
      expect(e.logs.some((l) => l.startsWith("session.refresh_unavailable"))).toBe(true);
      e.provider.failRefresh = e.provider.unreachableRefresh = false;
      e.provider.refreshError = undefined;
      expect((await e.h.proxy(get("projects", value), ["projects"])).status).toBe(200);
    });

    it("gives two sessions refreshing at once their own tokens", async () => {
      const a = await expiring(1000);
      const b = await expiring(1000);
      const [ra, rb] = await Promise.all([e.h.proxy(get("projects", a.value), ["projects"]), e.h.proxy(get("projects", b.value), ["projects"])]);
      const fa = (await sessionOf(valueOf(cookieNamed(ra, SESSION_COOKIE) as string))) as Session;
      const fb = (await sessionOf(valueOf(cookieNamed(rb, SESSION_COOKIE) as string))) as Session;
      expect(e.provider.tokenCalls("refresh_token")).toBe(2);
      expect(fa.accessToken).not.toBe(fb.accessToken);
      expect(fa.refreshToken).not.toBe(fb.refreshToken);
      expect(fa.createdAt).toBe(a.s.createdAt);
      expect(fb.createdAt).toBe(b.s.createdAt);
      const sent = e.api.requests.map((r) => r.headers.get("authorization"));
      expect(sent.sort()).toEqual([`Bearer ${fa.accessToken}`, `Bearer ${fb.accessToken}`].sort());
    });

    it("ends the session when the refreshed tokens do not fit it (the old refresh token is spent)", async () => {
      const { value } = await expiring(1000);
      e.provider.accessTokenLength = MAX_TOKEN;
      e.provider.refreshTokenLength = MAX_TOKEN;
      const res = await e.h.proxy(get("projects", value), ["projects"]);
      expect(res.status).toBe(401);
      expect(cookieNamed(res, SESSION_COOKIE)).toMatch(/Max-Age=0$/);
      expect(e.api.requests).toHaveLength(0);
      expect(e.logs.some((l) => l.startsWith("session.refresh_failed"))).toBe(true);
    });

    it("after logout, an older cookie cannot get the session's tokens from the shared refresh", async () => {
      const { value: old } = await expiring(1000);
      const refreshed = await e.h.proxy(get("projects", old), ["projects"]);
      const current = valueOf(cookieNamed(refreshed, SESSION_COOKIE) as string);
      const out = await e.h.logout(new Request(`${APP}/auth/logout`, { method: "POST", headers: { ...csrf, cookie: `${SESSION_COOKIE}=${current}` } }));
      expect(out.status).toBe(200);
      e.api.requests.length = 0;
      // Within the reuse window the old cookie would have shared the refresh;
      // now it refreshes with its spent token, which the provider refuses.
      const res = await e.h.proxy(get("projects", old), ["projects"]);
      expect(res.status).toBe(401);
      expect(e.api.requests).toHaveLength(0);
    });

    it("ends a session without a refresh token when its access token expires", async () => {
      e.provider.issueRefreshToken = false;
      const { value } = await expiring(1000);
      const res = await e.h.proxy(get("projects", value), ["projects"]);
      expect(res.status).toBe(401);
    });
  });
});

describe("RefreshCache", () => {
  const session = (token: string): Session => ({ accessToken: `at-${token}`, refreshToken: `rt-${token}`, accessExpiresAt: 2, createdAt: 1, lastSeenAt: 1 });

  it("keeps at most its limit; past it a refresh is not shared", async () => {
    const cache = new RefreshCache(2);
    let runs = 0;
    const run = (t: string) => async () => (runs++, session(t));
    for (const t of ["a", "b", "c"]) await cache.refresh(t, 0, run(t));
    expect(cache.size).toBe(2);
    await cache.refresh("a", 1, run("a"));
    expect(runs).toBe(3);
    await cache.refresh("c", 1, run("c"));
    expect(runs).toBe(4);
  });

  it("shares by token, forgets failures, expires after the reuse window", async () => {
    const cache = new RefreshCache();
    let runs = 0;
    const ok = async () => (runs++, session("x"));
    await cache.refresh("a", 0, ok);
    await cache.refresh("b", 0, ok);
    expect(await cache.refresh("a", REFRESH_REUSE_MS - 1, ok)).toEqual(session("x"));
    expect(runs).toBe(2);
    await expect(cache.refresh("f", 0, async () => Promise.reject(new Error("down")))).rejects.toThrow("down");
    await Promise.resolve();
    expect(cache.size).toBe(2);
    await cache.refresh("a", REFRESH_REUSE_MS, ok);
    expect(runs).toBe(3);
  });

  it("forgets the refresh that spent or issued a token", async () => {
    const cache = new RefreshCache();
    await cache.refresh("old", 0, async () => session("new"));
    await cache.forget("rt-new");
    expect(cache.size).toBe(0);
    await cache.refresh("old", 0, async () => session("new"));
    await cache.forget("old");
    expect(cache.size).toBe(0);
  });
});

describe("helpers", () => {
  it("makes D-001 request IDs that sort by time", () => {
    const a = newRequestId(Date.UTC(2026, 0, 1));
    const b = newRequestId(Date.UTC(2026, 0, 2));
    expect(a).toMatch(REQUEST_ID);
    expect(b > a).toBe(true);
    expect(newRequestId(0)).toMatch(/^req_0000000000/);
  });

  it("reads one cookie, not a duplicated one", () => {
    const r = (cookie: string) => new Request(APP, { headers: { cookie } });
    expect(cookieValue(r(`a=1; ${SESSION_COOKIE}=v; b=2`), SESSION_COOKIE)).toBe("v");
    expect(cookieValue(r(`${SESSION_COOKIE}=v; ${SESSION_COOKIE}=w`), SESSION_COOKIE)).toBeUndefined();
    expect(cookieValue(r(`x${SESSION_COOKIE}=v`), SESSION_COOKIE)).toBeUndefined();
    expect(cookieValue(new Request(APP), SESSION_COOKIE)).toBeUndefined();
  });

  it("answers errors the client reads as the API's", async () => {
    const res = apiError("FORBIDDEN", "cross-site request refused", Date.now());
    expect(res.status).toBe(403);
    expect(res.headers.get("cache-control")).toBe("no-store");
    const body = (await res.json()) as { error: Record<string, unknown> };
    expect(Object.keys(body.error).sort()).toEqual(["code", "message", "request_id"]);
  });
});
