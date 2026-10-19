// Test-only servers for the browser session E2E (T-043, TEST-032): a
// deterministic OIDC provider on 127.0.0.1:3101 and a stand-in for the Go API
// on 127.0.0.1:3102 that accepts only the provider's access tokens. Never a
// real provider (ADR-018); nothing here is part of apps/web.
//
// Control endpoints for the tests: POST /__subject (body: the subject the
// next sign-in gets), POST /__access-seconds (body: the lifetime of the next
// access tokens), GET /__state (counts and what the API saw).
import { createHash, generateKeyPairSync, randomBytes, sign } from "node:crypto";
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
const state = { subject: "auth0|e2e-user", accessSeconds: 600, revoked: [], refreshGrants: 0, codeGrants: 0, apiCalls: [], logouts: 0 };

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
  if (req.method === "POST" && url.pathname === "/__access-seconds") {
    state.accessSeconds = Number((await body(req)).trim());
    return json(res, 200, { accessSeconds: state.accessSeconds });
  }
  if (req.method === "GET" && url.pathname === "/__state") {
    return json(res, 200, { ...state, refreshStates: Object.fromEntries([...refresh].map(([k, v]) => [k, v.state])) });
  }
  json(res, 404, { error: "not_found" });
}).listen(3101, "127.0.0.1");

// The API stand-in: tokens from the provider only; an unknown subject is 403
// (as the Go API answers an identity provisioning does not know, D-008).
createServer(async (req, res) => {
  const url = new URL(req.url, "http://127.0.0.1:3102");
  const auth = req.headers.authorization ?? "";
  const token = auth.startsWith("Bearer ") ? auth.slice(7) : "";
  const known = access.get(token);
  state.apiCalls.push({ method: req.method, path: url.pathname, query: url.search, cookie: req.headers.cookie ?? null, org: req.headers["x-organization-id"] ?? null, hasAuth: auth !== "" });
  const rid = "req_01M4YDQK00V6H8S0ST4F7510KR";
  if (!known || known.exp <= Math.floor(Date.now() / 1000)) {
    return json(res, 401, { error: { code: "UNAUTHENTICATED", message: "invalid token", request_id: rid } }, { "x-request-id": rid });
  }
  if (known.sub === "auth0|unknown") {
    return json(res, 403, { error: { code: "FORBIDDEN", message: "unknown identity", request_id: rid } }, { "x-request-id": rid });
  }
  if (req.method === "GET" && url.pathname === "/v1/projects") {
    return json(res, 200, { items: [], next_cursor: null }, { "x-request-id": rid, "set-cookie": "api_cookie=1" });
  }
  await body(req);
  json(res, 404, { error: { code: "NOT_FOUND", message: "not found", request_id: rid } }, { "x-request-id": rid });
}).listen(3102, "127.0.0.1");

process.stdout.write("fake provider on :3101, fake API on :3102\n");
