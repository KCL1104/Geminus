// Test-only servers for the browser session E2E (T-043, TEST-032): a
// deterministic OIDC provider on 127.0.0.1:3101 and a stand-in for the Go API
// on 127.0.0.1:3102 that accepts only the provider's access tokens. Never a
// real provider (ADR-018); nothing here is part of apps/web.
//
// Control endpoints for the tests: POST /__subject (body: the subject the
// next sign-in gets), POST /__access-seconds (body: the lifetime of the next
// access tokens), POST /__api-cut (body "headers": the API's next answers
// send their headers and then drop the connection; empty: normal), POST
// /__reset (the tenant data as it starts: version 1, its draft, no AI
// proposals, no created models), GET /__state (counts and what the API saw).
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
  if (req.method === "POST" && url.pathname === "/__reset") {
    live = fresh();
    return json(res, 200, { reset: true });
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

// The model's AI proposals (T-031 review round 1, L5), in Line Works like
// the rest of its data: createProposal, getProposal, approveProposal and
// rejectProposal as openapi has them. A request on the latest version is
// answered 202 generating; the proposal's first read is generating and the
// next ones are ready with the fixed answer in proposal-answer.json, its
// words in the request's output_locale. That answer is written for the
// test: no AI is called, and nothing here is the server's review (nothing
// is validated, the risk is given). Approving an edit makes the next
// version from the latest one and the patch, and moves the draft (clean
// here) onto it; approving a scenario gives a scenario ID and no version;
// rejecting keeps the model. A key used again with the same request gets
// the first answer (D-011), with another request 409. A viewer is 403.
// H-046 (2): a request with research: true (a boolean when present, as the
// schema has it) is answered with the answer's "research" part too: which
// model wrote it, how many searches it made, and two fixed test pages as its
// sources (example.com and example.org, reserved for tests); nothing
// searched anything, and a request without it gets what it got before.
const answer = JSON.parse(readFileSync(new URL("./proposal-answer.json", import.meta.url), "utf8"));
const jcs = (v) =>
  Array.isArray(v) ? `[${v.map(jcs).join(",")}]` : v !== null && typeof v === "object" ? `{${Object.keys(v).sort().map((k) => `${JSON.stringify(k)}:${jcs(v[k])}`).join(",")}}` : JSON.stringify(v);
const sha = (v) => `sha256:${createHash("sha256").update(jcs(v)).digest("hex")}`;
const serial = (prefix, tag, n) => `${prefix}_01M4YDQK00TA0000000000${tag}${String(n).padStart(3, "0")}`;
const ASKED = "2026-10-26T00:05:00Z";
const DECIDED = "2026-10-26T00:10:00Z";
/** The answer's test and replace ops on a copy of ir (all the fixed answer has). */
function applied(ir, ops) {
  const out = structuredClone(ir);
  for (const op of ops) {
    const tokens = op.path.split("/").slice(1).map((t) => t.replace(/~1/g, "/").replace(/~0/g, "~"));
    const last = tokens.pop();
    let at = out;
    for (const t of tokens) at = at[t];
    if (op.op === "test" && JSON.stringify(at[last]) !== JSON.stringify(op.value)) throw new Error(`test failed at ${op.path}`);
    else if (op.op === "replace") at[last] = op.value;
    else if (op.op !== "test") throw new Error(`the fake applies test and replace only, not ${op.op}`);
  }
  return out;
}
// createModel (H-045) in Line Works' project, as openapi has it: the
// contract's domain and template pairs; the model is the pack's template
// file as it is (its zh-Hant names from the pack's names file when asked),
// with the new model's ID and the name given. Nothing is validated or
// built as the server does; a viewer is 403, and keys work as for the
// proposals. A created model has its draft and version 1 and empty lists.
const pack = (dir, name) => JSON.parse(readFileSync(new URL(`../../../internal/domainpack/${dir}/templates/${name}.json`, import.meta.url), "utf8"));
const templateDirs = {
  electronics_line: ["electronicsline", ["electronics_assembly_test_line"]],
  production_logistics: ["productionlogistics", ["shoe_factory", "food_plant", "carton_plant", "warehouse"]],
  software_delivery: ["softwaredelivery", ["default_delivery_pipeline"]],
};
function templateIR(domain, template, locale) {
  const [dir] = templateDirs[domain];
  const ir = pack(dir, template);
  if (locale === "zh-Hant") {
    const names = pack(dir, `${template}.zh-Hant.names`);
    for (const [list, from] of [[ir.entity_types, names.entity_types], [ir.resources, names.resources], [ir.nodes, names.nodes]]) {
      for (const x of list) if (x.name && from[x.id]) x.name = from[x.id];
    }
  }
  return ir;
}
/** Line Works' model as it starts: version 1 and its draft, no proposals, no keys, no models created. */
const fresh = () => ({ versions: [version], draft: structuredClone(fixture), proposals: new Map(), keys: new Map(), scenarios: 0, created: new Map() });
let live = fresh();
const latest = () => live.versions[live.versions.length - 1];
const modelNow = () => ({
  ...model,
  draft: { revision: live.draft.revision, base_version_id: live.draft.base_version_id, out_of_date: live.draft.base_version_id !== latest().id },
  latest_version: { id: latest().id, version: latest().version },
});

/** What a member of org may GET: path → [body, extra headers]. */
function tenantGet(org, path) {
  if (path === "/v1/projects") return [{ items: projects[org] ?? [], next_cursor: null }, org === ORG ? { "set-cookie": "api_cookie=1" } : {}];
  if (org !== LINE) return null;
  if (path === `/v1/projects/${PRJ_LINE}/models`) return [{ items: [modelNow(), ...[...live.created.values()].map((c) => c.model)], next_cursor: null }, {}];
  for (const c of live.created.values()) {
    if (path === `/v1/models/${c.model.id}`) return [c.model, {}];
    if (path === `/v1/models/${c.model.id}/draft`) return [c.draft, { etag: `"draft-revision-${c.draft.revision}"` }];
    if (path === `/v1/model-versions/${c.version.id}`) return [c.version, {}];
    if (path === `/v1/models/${c.model.id}/versions`) return [{ items: [c.version], next_cursor: null }, {}];
    if ([`/v1/model-versions/${c.version.id}/scenarios`, `/v1/models/${c.model.id}/ai-proposals`, `/v1/models/${c.model.id}/simulations`].includes(path)) return [{ items: [], next_cursor: null }, {}];
  }
  if (path === `/v1/models/${model.id}`) return [modelNow(), {}];
  if (path === `/v1/models/${model.id}/draft`) return [live.draft, { etag: `"draft-revision-${live.draft.revision}"` }];
  const v = live.versions.find((x) => path === `/v1/model-versions/${x.id}`);
  if (v) return [v, {}];
  // The model's lists (H-042: the rail's Scenarios, AI proposals and Runs pages) in the server's order: versions newest first, proposals and runs oldest first.
  if (path === `/v1/models/${model.id}/versions`) return [{ items: [...live.versions].reverse(), next_cursor: null }, {}];
  if (live.versions.some((x) => path === `/v1/model-versions/${x.id}/scenarios`)) return [{ items: [], next_cursor: null }, {}];
  if (path === `/v1/models/${model.id}/ai-proposals`) return [{ items: [...live.proposals.values()].map((x) => x.proposal), next_cursor: null }, {}];
  if (path === `/v1/models/${model.id}/simulations`) return [{ items: [], next_cursor: null }, {}];
  const s = path.startsWith("/v1/ai-proposals/") ? live.proposals.get(path.slice("/v1/ai-proposals/".length)) : undefined;
  if (s) {
    if (s.proposal.status === "generating" && s.reads++ >= 1) s.proposal = s.answer;
    return [s.proposal, {}];
  }
  return null;
}

const refusal = (status, code, message, details) => ({ status, body: { error: { code, message, request_id: "req_01M4YDQK00V6H8S0ST4F7510KR", ...(details ? { details } : {}) } } });

/** A member of org with role POSTs body to path: [status, body], or null (not found). */
function tenantPost(org, role, path, key, payload) {
  if (org !== LINE) return null;
  const create = path === `/v1/models/${model.id}/ai-proposals`;
  const newModel = path === `/v1/projects/${PRJ_LINE}/models`;
  const decision = path.match(/^\/v1\/ai-proposals\/([^/]+)\/(approve|reject)$/);
  const s = decision ? live.proposals.get(decision[1]) : undefined;
  if (!create && !newModel && !s) return null;
  if (role !== "editor" && role !== "owner") return refusal(403, "FORBIDDEN", newModel ? "this role cannot create a model" : "this role cannot ask for or decide a proposal");
  if (!key) return refusal(400, "IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required");
  if (typeof payload !== "object" || payload === null || Array.isArray(payload)) return refusal(422, "REQUEST_VALIDATION_FAILED", "the request body does not match the schema");
  const fingerprint = `${path} ${jcs(payload)}`;
  const seen = live.keys.get(key);
  if (seen) return seen.fingerprint === fingerprint ? seen.answer : refusal(409, "IDEMPOTENCY_KEY_CONFLICT", "this Idempotency-Key was used for a different request");
  const kept = (a) => (live.keys.set(key, { fingerprint, answer: a }), a);
  if (newModel) {
    const known = templateDirs[payload.domain];
    const fields = Object.keys(payload).every((k) => ["name", "domain", "template", "locale"].includes(k));
    if (!fields || !known || !known[1].includes(payload.template) || typeof payload.name !== "string" || payload.name.length < 1 || payload.name.length > 200 ||
      (payload.locale !== undefined && !["en", "zh-Hant"].includes(payload.locale))) {
      return refusal(422, "REQUEST_VALIDATION_FAILED", "the request body does not match the schema", [{ path: "/template", reason: "not a template of this domain" }]);
    }
    const n = live.created.size + 1;
    const id = serial("mdl", "M", n);
    const ir = { ...templateIR(payload.domain, payload.template, payload.locale), model_id: id, name: payload.name };
    const v = { ...version, id: serial("mv", "N", n), model_id: id, version: 1, ir, hash: sha(ir) };
    const m = { id, organization_id: LINE, project_id: PRJ_LINE, domain: payload.domain, name: payload.name,
      draft: { revision: 1, base_version_id: v.id, out_of_date: false }, latest_version: { id: v.id, version: 1 }, created_at: ASKED };
    live.created.set(id, { model: m, version: v, draft: { model_id: id, base_version_id: v.id, revision: 1, ir, hash: v.hash, out_of_date: false, issues: [] } });
    return kept({ status: 201, body: m });
  }
  if (create) {
    if (!["edit", "scenario"].includes(payload.mode) || typeof payload.intent !== "string" || payload.intent.trim() === "") return refusal(422, "REQUEST_VALIDATION_FAILED", "the request body does not match the schema");
    if ("research" in payload && typeof payload.research !== "boolean") return refusal(422, "REQUEST_VALIDATION_FAILED", "the request body does not match the schema", [{ path: "/research", reason: "research is true or false" }]);
    if (payload.base_version_id !== latest().id) return refusal(409, "MODEL_VERSION_CONFLICT", "base_version_id is not the model's latest version", [{ path: "/base_version_id", reason: "not the model's latest version" }]);
    const id = serial("aip", "P", live.proposals.size + 1);
    const server = { proposal_id: id, organization_id: LINE, model_id: model.id, base_version_id: payload.base_version_id, mode: payload.mode, created_by: USER, created_at: ASKED, requires_confirmation: true };
    const words = answer[payload.output_locale === "zh-Hant" ? "zh-Hant" : "en"];
    const ready = {
      ...server, status: "ready", ...words, changes: answer.changes, patch_hash: sha(answer.changes),
      risk_level: answer.risk_level, server_risk_level: answer.server_risk_level, provider_risk_claim: answer.provider_risk_claim, confidence: answer.confidence,
      validation: { valid: true, issues: [] }, dataset_bindings: [],
      ...(payload.research === true ? { generated_by: answer.research.generated_by, sources: answer.research.sources } : {}),
    };
    live.proposals.set(id, { proposal: { ...server, status: "generating" }, answer: ready, reads: 0, request: payload });
    return kept({ status: 202, body: { proposal_id: id, status: "generating" } });
  }
  const id = s.proposal.proposal_id;
  const decide = (change) => (s.proposal = { ...s.proposal, ...change, decided_by: USER, decided_at: DECIDED });
  if (["approved", "rejected", "conflicted"].includes(s.proposal.status)) return kept(refusal(409, "PROPOSAL_ALREADY_DECIDED", `the proposal is already ${s.proposal.status}; nothing was changed`));
  if (decision[2] === "reject") {
    if (!["ready", "needs_input"].includes(s.proposal.status)) return kept(refusal(409, "LIFECYCLE_CONFLICT", "only a ready or needs_input proposal can be rejected; nothing was changed"));
    decide({ status: "rejected", ...(typeof payload.comment === "string" && payload.comment !== "" ? { decision_comment: payload.comment } : {}) });
    return kept({ status: 200, body: s.proposal });
  }
  if (s.proposal.status !== "ready") return kept(refusal(409, "LIFECYCLE_CONFLICT", "only a ready proposal can be approved; nothing was changed"));
  if (s.proposal.base_version_id !== latest().id) {
    decide({ status: "conflicted" });
    return kept(refusal(409, "MODEL_VERSION_CONFLICT", "the model's latest version is no longer this proposal's base; nothing was applied", [{ path: "/expected_base_version_id", reason: "the proposal's base is not the model's latest version" }]));
  }
  if (payload.expected_base_version_id !== s.proposal.base_version_id) return kept(refusal(409, "MODEL_VERSION_CONFLICT", "expected_base_version_id is not this proposal's base; nothing was changed", [{ path: "/expected_base_version_id", reason: "not this proposal's base" }]));
  if (s.proposal.mode === "scenario") {
    const scenario = serial("scn", "S", ++live.scenarios);
    decide({ status: "approved", scenario_id: scenario });
    return kept({ status: 201, body: { proposal_id: id, status: "approved", mode: "scenario", scenario_id: scenario, patch_hash: s.proposal.patch_hash } });
  }
  const ir = applied(latest().ir, s.proposal.changes);
  const n = live.versions.length + 1;
  const v = { ...version, id: serial("mv", "V", n), version: n, parent_version_id: latest().id, ir, hash: sha(ir), created_at: DECIDED };
  live.versions.push(v);
  live.draft = { ...live.draft, base_version_id: v.id, revision: live.draft.revision + 1, ir, hash: v.hash, out_of_date: false };
  decide({ status: "approved", new_version_id: v.id });
  return kept({ status: 201, body: { proposal_id: id, status: "approved", mode: "edit", new_version_id: v.id, version: n, patch_hash: s.proposal.patch_hash, draft_out_of_date: false } });
}

// The API stand-in: tokens from the provider only; an unknown subject is 403
// (as the Go API answers an identity provisioning does not know, D-008).
createServer(async (req, res) => {
  const url = new URL(req.url, "http://127.0.0.1:3102");
  const auth = req.headers.authorization ?? "";
  const token = auth.startsWith("Bearer ") ? auth.slice(7) : "";
  const known = access.get(token);
  const org = req.headers["x-organization-id"] ?? null;
  const raw = await body(req);
  const key = req.headers["idempotency-key"] ?? null;
  let payload;
  try {
    payload = raw === "" ? undefined : JSON.parse(raw);
  } catch {
    payload = null;
  }
  state.apiCalls.push({ method: req.method, path: url.pathname, query: url.search, cookie: req.headers.cookie ?? null, org, hasAuth: auth !== "", ...(req.method === "POST" ? { key, body: payload ?? null } : {}) });
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
  const membership = typeof org === "string" ? memberships.find((m) => m.organization_id === org) : undefined;
  const found = req.method === "GET" && membership ? tenantGet(org, url.pathname) : null;
  if (found !== null) return json(res, 200, found[0], { "x-request-id": rid, ...found[1] });
  const answered = req.method === "POST" && membership ? tenantPost(org, membership.role, url.pathname, key, payload) : null;
  if (answered !== null) return json(res, answered.status, answered.body, { "x-request-id": rid });
  fail(404, "NOT_FOUND", "not found");
}).listen(3102, "127.0.0.1");

process.stdout.write("fake provider on :3101, fake API on :3102\n");
