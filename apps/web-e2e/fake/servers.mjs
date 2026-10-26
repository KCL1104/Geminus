// Test-only servers for the browser session E2E (T-043, TEST-032): a
// deterministic OIDC provider on 127.0.0.1:3101 and a stand-in for the Go API
// on 127.0.0.1:3102 that accepts only the provider's access tokens. Never a
// real provider (ADR-018); nothing here is part of apps/web.
//
// Control endpoints for the tests: POST /__subject (body: the subject the
// next sign-in gets), POST /__access-seconds (body: the lifetime of the next
// access tokens), POST /__api-cut (body "headers": the API's next answers
// send their headers and then drop the connection; empty: normal), GET
// /__state (counts and what the API saw).
import { createHash, generateKeyPairSync, randomBytes, sign } from "node:crypto";
import { readFileSync } from "node:fs";
import { createServer } from "node:http";

const APP = "http://127.0.0.1:3100";
const ISSUER = "http://127.0.0.1:3101/";
const CLIENT_ID = "e2e-client";
const CLIENT_SECRET = "e2e-client-secret";
const AUDIENCE = "https://api.e2e.invalid";

const keys = generateKeyPairSync("rsa", { modulusLength: 2048 });
const b64url = (b) => Buffer.from(b).toString("base64url");
const codes = new Map();
const refresh = new Map(); // token -> { state, family, sub }
const access = new Map(); // token -> { sub, exp }
const state = { subject: "auth0|e2e-user", accessSeconds: 600, revoked: [], refreshGrants: 0, codeGrants: 0, apiCalls: [], logouts: 0, apiCut: "" };

function jwt(claims) {
  const head = b64url(JSON.stringify({ alg: "RS256", typ: "JWT", kid: "e2e" }));
  const body = b64url(JSON.stringify(claims));
  return `${head}.${body}.${b64url(sign("sha256", Buffer.from(`${head}.${body}`), keys.privateKey))}`;
}

function issue(sub, nonce, family) {
  const now = Math.floor(Date.now() / 1000);
  const at = `at-${b64url(randomBytes(24))}`;
  const rt = `rt-${b64url(randomBytes(24))}`;
  access.set(at, { sub, exp: now + state.accessSeconds });
  refresh.set(rt, { state: "live", family: family ?? b64url(randomBytes(8)), sub });
  const out = { access_token: at, refresh_token: rt, token_type: "Bearer", expires_in: state.accessSeconds, scope: "openid offline_access" };
  if (nonce !== null) out.id_token = jwt({ iss: ISSUER, aud: CLIENT_ID, sub, nonce, iat: now, exp: now + 600 });
  return out;
}

const body = (req) =>
  new Promise((resolve) => {
    let data = "";
    req.on("data", (c) => (data += c));
    req.on("end", () => resolve(data));
  });

function json(res, status, value, headers = {}) {
  res.writeHead(status, { "content-type": "application/json", "cache-control": "no-store", ...headers });
  res.end(JSON.stringify(value));
}

createServer(async (req, res) => {
  const url = new URL(req.url, ISSUER);
  if (req.method === "GET" && url.pathname === "/authorize") {
    const p = url.searchParams;
    if (p.get("client_id") !== CLIENT_ID || p.get("redirect_uri") !== `${APP}/auth/callback` || p.get("response_type") !== "code" ||
        p.get("code_challenge_method") !== "S256" || !p.get("code_challenge") || !p.get("state") || !p.get("nonce") || p.get("audience") !== AUDIENCE) {
      return json(res, 400, { error: "invalid_request" });
    }
    const code = b64url(randomBytes(16));
    codes.set(code, { challenge: p.get("code_challenge"), nonce: p.get("nonce"), sub: state.subject, used: false });
    res.writeHead(302, { location: `${APP}/auth/callback?code=${code}&state=${encodeURIComponent(p.get("state"))}` });
    return res.end();
  }
  if (req.method === "POST" && url.pathname === "/oauth/token") {
    const p = new URLSearchParams(await body(req));
    if (p.get("client_id") !== CLIENT_ID || p.get("client_secret") !== CLIENT_SECRET) return json(res, 401, { error: "invalid_client" });
    if (p.get("grant_type") === "authorization_code") {
      state.codeGrants++;
      const c = codes.get(p.get("code") ?? "");
      if (!c || c.used || p.get("redirect_uri") !== `${APP}/auth/callback`) return json(res, 400, { error: "invalid_grant" });
      c.used = true;
      if (b64url(createHash("sha256").update(p.get("code_verifier") ?? "").digest()) !== c.challenge) return json(res, 400, { error: "invalid_grant" });
      return json(res, 200, issue(c.sub, c.nonce));
    }
    if (p.get("grant_type") === "refresh_token") {
      state.refreshGrants++;
      const t = refresh.get(p.get("refresh_token") ?? "");
      if (!t || t.state !== "live") {
        if (t?.state === "spent") for (const v of refresh.values()) if (v.family === t.family) v.state = "revoked";
        return json(res, 400, { error: "invalid_grant" });
      }
      t.state = "spent";
      return json(res, 200, issue(t.sub, null, t.family));
    }
    return json(res, 400, { error: "unsupported_grant_type" });
  }
  if (req.method === "POST" && url.pathname === "/oauth/revoke") {
    const token = new URLSearchParams(await body(req)).get("token") ?? "";
    state.revoked.push(token);
    const t = refresh.get(token);
    if (t) for (const v of refresh.values()) if (v.family === t.family) v.state = "revoked";
    res.writeHead(200);
    return res.end();
  }
  if (req.method === "GET" && url.pathname === "/v2/logout") {
    state.logouts++;
    const back = url.searchParams.get("post_logout_redirect_uri");
    res.writeHead(302, { location: back === `${APP}/` ? back : APP });
    return res.end();
  }
  if (req.method === "GET" && url.pathname === "/.well-known/jwks.json") {
    return json(res, 200, { keys: [{ ...keys.publicKey.export({ format: "jwk" }), kid: "e2e", alg: "RS256", use: "sig" }] });
  }
  if (req.method === "POST" && url.pathname === "/__subject") {
    state.subject = (await body(req)).trim();
    return json(res, 200, { subject: state.subject });
  }
  if (req.method === "POST" && url.pathname === "/__api-cut") {
    state.apiCut = (await body(req)).trim();
    return json(res, 200, { apiCut: state.apiCut });
  }
  if (req.method === "POST" && url.pathname === "/__access-seconds") {
    state.accessSeconds = Number((await body(req)).trim());
    return json(res, 200, { accessSeconds: state.accessSeconds });
  }
  if (req.method === "GET" && url.pathname === "/__state") {
    return json(res, 200, { ...state, refreshStates: Object.fromEntries([...refresh].map(([k, v]) => [k, v.state])) });
  }
  json(res, 404, { error: "not_found" });
}).listen(3101, "127.0.0.1");

// The tenant data the API stand-in serves (T-043 follow-up, the signed-in
// bootstrap): who each subject is (GET /v1/me, D-045), and per organization
// its projects, a project's models and a model with its draft and version.
// The draft is the contract fixture testdata/contracts/valid-draft-with-issues.json.
// An organization the subject is not a member of, and anything of another
// organization, is 404 (as the Go API answers, D-008, TEST-009).
const fixture = JSON.parse(readFileSync(new URL("../../../testdata/contracts/valid-draft-with-issues.json", import.meta.url), "utf8"));
const ORG = "org_01M4YDQK00TA00000000000000";
const LINE = "org_01M4YDQK00TA00000000000010";
const BAY = "org_01M4YDQK00TA00000000000020";
const PRJ_LINE = "prj_01M4YDQK00TA00000000000011";
const PRJ_BAY = "prj_01M4YDQK00TA00000000000021";
const USER = "usr_01M4YDQK00TA00000000000001";
const users = {
  "auth0|e2e-user": [{ organization_id: ORG, organization_name: "E2E Org", role: "editor" }],
  "auth0|e2e-editor": [{ organization_id: LINE, organization_name: "Line Works", role: "editor" }],
  "auth0|e2e-viewer": [{ organization_id: LINE, organization_name: "Line Works", role: "viewer" }],
  "auth0|e2e-multi": [
    { organization_id: LINE, organization_name: "Line Works", role: "owner" },
    { organization_id: BAY, organization_name: "Test Bay", role: "viewer" },
  ],
  "auth0|e2e-nomember": [],
};
const created = "2026-10-26T00:00:00Z";
const projects = {
  [ORG]: [],
  [LINE]: [{ id: PRJ_LINE, organization_id: LINE, name: "SMT line 1", created_at: created, created_by: USER }],
  [BAY]: [{ id: PRJ_BAY, organization_id: BAY, name: "Burn-in bay", created_at: created, created_by: USER }],
};
const model = {
  id: fixture.model_id, organization_id: LINE, project_id: PRJ_LINE, domain: fixture.ir.domain, name: fixture.ir.name,
  draft: { revision: fixture.revision, base_version_id: fixture.base_version_id, out_of_date: false },
  latest_version: { id: fixture.base_version_id, version: 1 }, created_at: created,
};
const version = {
  id: fixture.base_version_id, organization_id: LINE, model_id: fixture.model_id, version: 1, schema_version: fixture.ir.schema_version,
  parent_version_id: null, ir: fixture.ir, hash: fixture.hash, dataset_bindings: [], comment: null, created_by: USER, created_at: created,
};
/** What a member of org may GET: path → [body, extra headers]. */
function tenantGet(org, path) {
  if (path === "/v1/projects") return [{ items: projects[org] ?? [], next_cursor: null }, org === ORG ? { "set-cookie": "api_cookie=1" } : {}];
  if (org !== LINE) return null;
  if (path === `/v1/projects/${PRJ_LINE}/models`) return [{ items: [model], next_cursor: null }, {}];
  if (path === `/v1/models/${model.id}`) return [model, {}];
  if (path === `/v1/models/${model.id}/draft`) return [fixture, { etag: `"draft-revision-${fixture.revision}"` }];
  if (path === `/v1/model-versions/${version.id}`) return [version, {}];
  return null;
}

// The API stand-in: tokens from the provider only; an unknown subject is 403
// (as the Go API answers an identity provisioning does not know, D-008).
createServer(async (req, res) => {
  const url = new URL(req.url, "http://127.0.0.1:3102");
  const auth = req.headers.authorization ?? "";
  const token = auth.startsWith("Bearer ") ? auth.slice(7) : "";
  const known = access.get(token);
  const org = req.headers["x-organization-id"] ?? null;
  state.apiCalls.push({ method: req.method, path: url.pathname, query: url.search, cookie: req.headers.cookie ?? null, org, hasAuth: auth !== "" });
  const rid = "req_01M4YDQK00V6H8S0ST4F7510KR";
  const fail = (status, code, message) => json(res, status, { error: { code, message, request_id: rid } }, { "x-request-id": rid });
  if (!known || known.exp <= Math.floor(Date.now() / 1000)) return fail(401, "UNAUTHENTICATED", "invalid token");
  const memberships = users[known.sub];
  if (memberships === undefined) return fail(403, "FORBIDDEN", "unknown identity");
  if (req.method === "GET" && url.pathname === "/v1/me") {
    // GET /me selects no organization: the header is refused, never ignored (openapi NoOrganizationContext).
    if (org !== null) return fail(400, "INVALID_REQUEST", "this operation selects no organization: send no X-Organization-ID header");
    return json(res, 200, { user_id: USER, memberships, truncated: false }, { "x-request-id": rid });
  }
  if (req.method === "GET" && url.pathname === "/v1/projects" && state.apiCut === "headers") {
    // Headers, then the connection drops before any body byte.
    res.writeHead(200, { "content-type": "application/json", "x-request-id": rid });
    res.flushHeaders();
    setTimeout(() => res.destroy(), 50);
    return;
  }
  const member = typeof org === "string" && memberships.some((m) => m.organization_id === org);
  const found = req.method === "GET" && member ? tenantGet(org, url.pathname) : null;
  if (found !== null) return json(res, 200, found[0], { "x-request-id": rid, ...found[1] });
  await body(req);
  fail(404, "NOT_FOUND", "not found");
}).listen(3102, "127.0.0.1");

process.stdout.write("fake provider on :3101, fake API on :3102\n");
