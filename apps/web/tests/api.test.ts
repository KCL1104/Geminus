// T-014: the API boundary fails closed, sends the tenant selector and If-Match,
// and maps every error the contract defines (and nothing it does not).
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { createApiClient, newIdempotencyKey, type Session } from "../lib/api/client";
import { draftETag, parseDraftETag } from "../lib/api/etag";
import { isID } from "../lib/api/ids";
import { errorCodeStatus, problemFromResponse, validationIssues } from "../lib/api/problem";
import type { Comparison, CreateScenario, Draft, JsonPatch, ModelVersion, Scenario, Scenarios } from "../lib/contracts";

const commonSchema = JSON.parse(readFileSync(new URL("../../../schemas/common.schema.json", import.meta.url), "utf8")) as {
  $defs: { ErrorCode: { enum: string[]; "x-http-status": Record<string, number> } };
};
const fixture = (name: string): unknown =>
  JSON.parse(readFileSync(new URL(`../../../testdata/contracts/${name}`, import.meta.url), "utf8"));

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const MODEL = "mdl_01M4YDQK004AGVNEGJQJ1NMM0N";
const PROJECT = "prj_01M4YDQK00TA00000000000001";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const SCENARIO = "scn_01M4YDQK00J4MJS82YRG0PCEC1";
const BASELINE_RUN = "sim_01M4YDQK00FGZZWH0PP15XPNFA";
const SCENARIO_RUN = "sim_01M4YDQK00FGZZWH0PP15XPNFB";
const session: Session = { organizationId: ORG };

type Call = { url: URL; init: RequestInit };

/** A fetch test double that records calls and answers with queued responses. */
function fakeFetch(...responses: (Response | Error)[]) {
  const calls: Call[] = [];
  const fn = async (input: string | URL | Request, init?: RequestInit) => {
    // The client sends same-origin paths; resolve them as the browser would.
    calls.push({ url: new URL(String(input), "https://app.test"), init: init ?? {} });
    const next = responses.shift();
    if (next === undefined) throw new Error("unexpected request");
    if (next instanceof Error) throw next;
    return next;
  };
  return { fetch: fn as typeof fetch, calls };
}

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });

const errorBody = (code: string, extra: Record<string, unknown> = {}) => ({
  error: { code, message: `${code} message`, request_id: REQ, ...extra },
});

function draft(revision: number): Draft {
  const d = fixture("valid-draft-with-issues.json") as Draft;
  return { ...d, revision };
}

describe("error catalogue", () => {
  it("matches the schema's x-http-status exactly", () => {
    expect(errorCodeStatus).toEqual(commonSchema.$defs.ErrorCode["x-http-status"]);
    expect(Object.keys(errorCodeStatus).sort()).toEqual([...commonSchema.$defs.ErrorCode.enum].sort());
  });

  it("maps each status of the contract to its kind", () => {
    const kinds: Record<string, string> = {
      INVALID_REQUEST: "invalid", UNAUTHENTICATED: "unauthenticated", FORBIDDEN: "forbidden", NOT_FOUND: "not_found",
      MODEL_VERSION_CONFLICT: "conflict", PAYLOAD_TOO_LARGE: "invalid", UNSUPPORTED_MEDIA_TYPE: "invalid",
      MODEL_VALIDATION_FAILED: "invalid", PRECONDITION_REQUIRED: "precondition", RATE_LIMITED: "rate_limited",
      INTERNAL_ERROR: "internal", SERVICE_UNAVAILABLE: "unavailable", AI_POLICY_NOT_APPROVED: "unavailable",
    };
    for (const [code, kind] of Object.entries(kinds)) {
      const status = errorCodeStatus[code as keyof typeof errorCodeStatus];
      const p = problemFromResponse(status, new Headers(), errorBody(code));
      expect([code, p.kind, p.code, p.status, p.requestId]).toEqual([code, kind, code, status, REQ]);
    }
  });

  it("refuses to act on codes the server did not mean", () => {
    const h = new Headers({ "X-Request-ID": REQ });
    expect(problemFromResponse(409, h, errorBody("FORBIDDEN")).kind).toBe("protocol"); // code of another status
    expect(problemFromResponse(409, h, errorBody("SOMETHING_NEW")).kind).toBe("protocol");
    expect(problemFromResponse(502, h, undefined).kind).toBe("protocol"); // e.g. an HTML proxy page
    expect(problemFromResponse(418, h, errorBody("INVALID_REQUEST")).kind).toBe("protocol");
    const p = problemFromResponse(502, h, undefined);
    expect(p.requestId).toBe(REQ);
    expect(p.code).toBeNull();
  });

  it("never invents a request ID or details", () => {
    const p = problemFromResponse(403, new Headers({ "X-Request-ID": "not-a-request-id" }), {
      error: { code: "FORBIDDEN", message: "no", request_id: "<script>", details: [{ path: 1 }, "x"] },
    });
    expect(p.requestId).toBeNull();
    expect(p.details).toEqual([]);
  });

  it("keeps validation details for the editor and Retry-After only where it applies", () => {
    const p = problemFromResponse(422, new Headers({ "Retry-After": "5" }), errorBody("MODEL_VALIDATION_FAILED", {
      details: [{ path: "/nodes/1/service_time", reason: "must be positive", code: "DISTRIBUTION_INVALID", severity: "error" }],
    }));
    expect(p.retryAfterSeconds).toBeNull();
    expect(validationIssues(p)).toEqual([{ code: "DISTRIBUTION_INVALID", path: "/nodes/1/service_time", message: "must be positive", severity: "error" }]);
    expect(problemFromResponse(429, new Headers({ "Retry-After": "7" }), errorBody("RATE_LIMITED")).retryAfterSeconds).toBe(7);
    expect(problemFromResponse(429, new Headers({ "Retry-After": "Wed, 21 Oct" }), errorBody("RATE_LIMITED")).retryAfterSeconds).toBeNull();
  });
});

describe("ids and ETags", () => {
  it("accepts only D-001 IDs of the right kind", () => {
    expect(isID("mdl", MODEL)).toBe(true);
    for (const bad of ["mdl_x", MODEL.toLowerCase(), MODEL.replace("mdl", "prj"), `${MODEL}/../x`, "mdl_81M4YDQK004AGVNEGJQJ1NMM0N", ""]) {
      expect(isID("mdl", bad)).toBe(false);
    }
  });

  it("round-trips draft revisions and rejects weak or foreign ETags", () => {
    expect(parseDraftETag(draftETag(12))).toBe(12);
    for (const bad of ['W/"draft-revision-12"', '"draft-revision-0"', "draft-revision-12", '"draft-revision-012"', '"v-12"']) {
      expect(parseDraftETag(bad)).toBeNull();
    }
    expect(() => draftETag(0)).toThrow(RangeError);
    expect(() => draftETag(1.5)).toThrow(RangeError);
  });
});

describe("client", () => {
  it("sends nothing without a session", async () => {
    const f = fakeFetch();
    const noSession = await createApiClient({ session: () => null, fetch: f.fetch }).listProjects();
    expect(!noSession.ok && noSession.problem.kind).toBe("unauthenticated");
    expect(f.calls).toHaveLength(0);
  });

  it("goes through the same-origin BFF with the tenant selector and the CSRF header, never a token (ADR-018)", async () => {
    const f = fakeFetch(json(200, { items: [], next_cursor: null }, { "X-Request-ID": REQ }));
    const api = createApiClient({ session: () => session, fetch: f.fetch });
    const r = await api.listProjects({ cursor: "c1" });
    expect(r).toEqual({ ok: true, data: { items: [], next_cursor: null }, requestId: REQ, etag: null });
    const call = f.calls[0]!;
    expect(call.url.toString()).toBe("https://app.test/api/v1/projects?cursor=c1");
    const headers = call.init.headers as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
    expect(headers["X-Organization-ID"]).toBe(ORG);
    expect(headers["X-Geminus-Request"]).toBe("1");
    expect(call.init.credentials).toBe("same-origin");
    expect(call.init.redirect).toBe("error");
    expect(call.init.cache).toBe("no-store");
  });

  it("reads the user's own memberships (D-045) with no session and never the tenant selector", async () => {
    const me = fixture("valid-me.json");
    for (const current of [null, session]) {
      const f = fakeFetch(json(200, me, { "X-Request-ID": REQ, "Cache-Control": "no-store" }));
      const r = await createApiClient({ session: () => current, fetch: f.fetch }).getMe();
      expect(r).toEqual({ ok: true, data: me, requestId: REQ, etag: null });
      const call = f.calls[0]!;
      expect([call.init.method, call.url.toString()]).toEqual(["GET", "https://app.test/api/v1/me"]);
      const headers = call.init.headers as Record<string, string>;
      expect(Object.keys(headers).map((h) => h.toLowerCase())).not.toContain("x-organization-id");
      expect(headers.Authorization).toBeUndefined();
      expect([headers.Accept, headers["X-Geminus-Request"]]).toEqual(["application/json", "1"]);
      expect([call.init.credentials, call.init.redirect, call.init.cache, call.init.body]).toEqual(["same-origin", "error", "no-store", undefined]);
    }
  });

  it("ends the /me request with the caller's signal (D-045 review L1)", async () => {
    const ctl = new AbortController();
    const f = fakeFetch(json(200, fixture("valid-me.json"), { "X-Request-ID": REQ }));
    await createApiClient({ session: () => null, fetch: f.fetch }).getMe({ signal: ctl.signal });
    expect(f.calls[0]!.init.signal).toBe(ctl.signal);
  });

  it("maps the answers /me can give like any other", async () => {
    const cases: [Response | Error, string][] = [
      [json(401, errorBody("UNAUTHENTICATED")), "unauthenticated"], // signed out (the BFF)
      [json(403, errorBody("FORBIDDEN")), "forbidden"], // an identity without a user
      [json(400, errorBody("INVALID_REQUEST")), "invalid"],
      [json(503, errorBody("SERVICE_UNAVAILABLE"), { "Retry-After": "5" }), "unavailable"],
      [new TypeError("fetch failed"), "network"],
      [new Response("<html>", { status: 200 }), "protocol"],
    ];
    for (const [answer, kind] of cases) {
      const r = await createApiClient({ session: () => null, fetch: fakeFetch(answer).fetch }).getMe();
      expect(!r.ok && r.problem.kind).toBe(kind);
    }
  });

  it("rejects malformed route IDs locally as not found", async () => {
    const f = fakeFetch();
    const api = createApiClient({ session: () => session, fetch: f.fetch });
    for (const r of [await api.getModel("../projects"), await api.getDraft("mdl_bad"), await api.listModels("mdl_01M4YDQK004AGVNEGJQJ1NMM0N")]) {
      expect(!r.ok && r.problem.kind).toBe("not_found");
    }
    expect(f.calls).toHaveLength(0);
  });

  it("saves a draft with If-Match and the JSON Patch media type", async () => {
    const saved = draft(4);
    const f = fakeFetch(json(200, saved, { ETag: draftETag(4) }));
    const api = createApiClient({ session: () => session, fetch: f.fetch });
    const patch: JsonPatch = [{ op: "test", path: "/resources/0/id", value: "reviewer" }, { op: "replace", path: "/resources/0/capacity", value: 3 }];
    const r = await api.patchDraft(MODEL, 3, patch);
    expect(r.ok && r.data.revision).toBe(4);
    const call = f.calls[0]!;
    expect(call.init.method).toBe("PATCH");
    const headers = call.init.headers as Record<string, string>;
    expect(headers["If-Match"]).toBe('"draft-revision-3"');
    expect(headers["Content-Type"]).toBe("application/json-patch+json");
    expect(JSON.parse(String(call.init.body))).toEqual(patch);
  });

  it("reports a stale revision as a conflict", async () => {
    const f = fakeFetch(json(409, errorBody("MODEL_VERSION_CONFLICT")));
    const r = await createApiClient({ session: () => session, fetch: f.fetch }).patchDraft(MODEL, 3, [{ op: "test", path: "/name", value: "x" }]);
    expect(!r.ok && [r.problem.kind, r.problem.code]).toEqual(["conflict", "MODEL_VERSION_CONFLICT"]);
  });

  it("treats a draft without a matching ETag as a protocol error", async () => {
    for (const etag of [undefined, draftETag(5), 'W/"draft-revision-4"']) {
      const f = fakeFetch(json(200, draft(4), etag === undefined ? {} : { ETag: etag }));
      const r = await createApiClient({ session: () => session, fetch: f.fetch }).getDraft(MODEL);
      expect(!r.ok && r.problem.kind).toBe("protocol");
    }
  });

  it("maps transport failures and non-JSON successes", async () => {
    const down = fakeFetch(new TypeError("fetch failed"));
    const r1 = await createApiClient({ session: () => session, fetch: down.fetch }).listProjects();
    expect(!r1.ok && r1.problem.kind).toBe("network");
    const html = fakeFetch(new Response("<html>", { status: 200 }));
    const r2 = await createApiClient({ session: () => session, fetch: html.fetch }).listProjects();
    expect(!r2.ok && r2.problem.kind).toBe("protocol");
    const ctl = new AbortController();
    ctl.abort();
    const aborted = fakeFetch(new DOMException("aborted", "AbortError"));
    const r3 = await createApiClient({ session: () => session, fetch: aborted.fetch }).listProjects({ signal: ctl.signal });
    expect(!r3.ok && r3.problem.message).toBe("Request aborted.");
    expect(aborted.calls[0]!.init.signal).toBe(ctl.signal);
  });

  it("reads the session per request, so a sign-out takes effect immediately", async () => {
    let current: Session | null = session;
    const f = fakeFetch(json(200, { items: [], next_cursor: null }));
    const api = createApiClient({ session: () => current, fetch: f.fetch });
    expect((await api.listProjects()).ok).toBe(true);
    current = null;
    const r = await api.listProjects();
    expect(!r.ok && r.problem.kind).toBe("unauthenticated");
    expect(f.calls).toHaveLength(1);
  });

  it("keeps a valid project ID path intact", async () => {
    const f = fakeFetch(json(200, { items: [], next_cursor: null }));
    await createApiClient({ session: () => session, fetch: f.fetch }).listModels(PROJECT);
    expect(f.calls[0]!.url.pathname).toBe(`/api/v1/projects/${PROJECT}/models`);
  });
});

const operations = (
  JSON.parse(readFileSync(new URL("../../../planning/api-operations.json", import.meta.url), "utf8")) as {
    operations: { method: string; path: string; operation_id: string; scope?: "user" }[];
  }
).operations;

describe("model writes and versions (T-013)", () => {
  const VERSION = "mv_01M4YDQK00FRYWV7GS7X1QCXZR";
  const version = fixture("valid-model-version.json") as ModelVersion;
  const api = (f: ReturnType<typeof fakeFetch>) => createApiClient({ session: () => session, fetch: f.fetch });

  it("creates a model with its Idempotency-Key and a JSON body", async () => {
    const f = fakeFetch(json(201, { id: MODEL }));
    const r = await api(f).createModel(PROJECT, { name: "Pipeline", domain: "software_delivery", template: "default_delivery_pipeline" }, "key-create-1");
    expect(r.ok).toBe(true);
    const call = f.calls[0]!;
    const headers = call.init.headers as Record<string, string>;
    expect([call.init.method, call.url.pathname]).toEqual(["POST", `/api/v1/projects/${PROJECT}/models`]);
    expect([headers["Idempotency-Key"], headers["Content-Type"], headers["If-Match"]]).toEqual(["key-create-1", "application/json", undefined]);
    expect(JSON.parse(String(call.init.body))).toEqual({ name: "Pipeline", domain: "software_delivery", template: "default_delivery_pipeline" });
  });

  it("creates a model from the electronics line template with its locale (T-051)", async () => {
    const f = fakeFetch(json(201, { id: MODEL }));
    const body = { name: "電子產線", domain: "electronics_line", template: "electronics_assembly_test_line", locale: "zh-Hant" } as const;
    const r = await api(f).createModel(PROJECT, body, "key-create-2");
    expect(r.ok).toBe(true);
    expect(JSON.parse(String(f.calls[0]!.init.body))).toEqual(body);
  });

  it("publishes with If-Match and the key, and reads the version back", async () => {
    const f = fakeFetch(json(201, version), json(200, version));
    const client = api(f);
    const r = await client.publishVersion(MODEL, 3, { expected_base_version_id: VERSION, comment: "Ready" }, "key-publish-1");
    expect(r.ok && r.data.comment).toBe(null);
    const headers = f.calls[0]!.init.headers as Record<string, string>;
    expect([f.calls[0]!.init.method, f.calls[0]!.url.pathname]).toEqual(["POST", `/api/v1/models/${MODEL}/versions`]);
    expect([headers["If-Match"], headers["Idempotency-Key"]]).toEqual(['"draft-revision-3"', "key-publish-1"]);
    expect(JSON.parse(String(f.calls[0]!.init.body))).toEqual({ expected_base_version_id: VERSION, comment: "Ready" });
    expect((await client.getModelVersion(VERSION)).ok).toBe(true);
    expect(f.calls[1]!.url.pathname).toBe(`/api/v1/model-versions/${VERSION}`);
  });

  it("keeps why a publish was unavailable: snapshots that could not be read (H-022, T-054)", async () => {
    // publishVersion's 503 storage_failed example (openapi.yaml), as the API sends it.
    const storageFailed = {
      error: {
        code: "SERVICE_UNAVAILABLE",
        message: "the dataset snapshots could not be read; nothing was changed (retry later)",
        details: [{ path: "", reason: "the dataset storage failed", code: "DATASET_STORAGE_FAILED" }],
        request_id: REQ,
      },
    };
    const f = fakeFetch(json(503, storageFailed, { "X-Request-ID": REQ }));
    const r = await api(f).publishVersion(MODEL, 3, { expected_base_version_id: VERSION }, "key-publish-3");
    expect(!r.ok && r.problem).toEqual({
      kind: "unavailable",
      code: "SERVICE_UNAVAILABLE",
      status: 503,
      message: "the dataset snapshots could not be read; nothing was changed (retry later)",
      requestId: REQ,
      details: [{ path: "", reason: "the dataset storage failed", code: "DATASET_STORAGE_FAILED" }],
      retryAfterSeconds: null,
    });
  });

  it("resets with If-Match and requires the matching ETag back", async () => {
    const good = fakeFetch(json(200, draft(5), { ETag: draftETag(5) }));
    const r = await api(good).resetDraft(MODEL, 4, { expected_latest_version_id: VERSION, confirm_discard: true });
    expect(r.ok && r.data.revision).toBe(5);
    expect((good.calls[0]!.init.headers as Record<string, string>)["If-Match"]).toBe('"draft-revision-4"');
    const stale = fakeFetch(json(200, draft(5), { ETag: draftETag(6) }));
    const s2 = await api(stale).resetDraft(MODEL, 4, { expected_latest_version_id: VERSION, confirm_discard: true });
    expect(!s2.ok && s2.problem.kind).toBe("protocol");
  });

  it("refuses bad IDs and keys before sending anything", async () => {
    const f = fakeFetch();
    const client = api(f);
    const body = { name: "P", domain: "software_delivery", template: "default_delivery_pipeline" } as const;
    const refused = [
      await client.createModel(MODEL, body, "key-create-2"),
      await client.createModel(PROJECT, body, "short"),
      await client.createModel(PROJECT, body, "key with space"),
      await client.publishVersion(MODEL, 2, { expected_base_version_id: VERSION }, "k".repeat(129)),
      await client.publishVersion(VERSION, 2, { expected_base_version_id: VERSION }, "key-publish-2"),
      await client.getModelVersion(MODEL),
      await client.listVersions(VERSION),
    ];
    expect(refused.map((r) => !r.ok && r.problem.kind)).toEqual(["not_found", "invalid", "invalid", "invalid", "not_found", "not_found", "not_found"]);
    expect(f.calls).toHaveLength(0);
  });

  it("refuses a revision the server cannot have issued instead of throwing", async () => {
    const f = fakeFetch();
    const client = api(f);
    const reset = { expected_latest_version_id: VERSION, confirm_discard: true } as const;
    for (const revision of [0, -1, 1.5, Number.NaN, 2 ** 53]) {
      const results = [
        await client.patchDraft(MODEL, revision, [{ op: "test", path: "/name", value: "x" }]),
        await client.resetDraft(MODEL, revision, reset),
        await client.publishVersion(MODEL, revision, { expected_base_version_id: VERSION }, "key-revision-1"),
      ];
      expect(results.map((r) => !r.ok && r.problem.kind)).toEqual(["invalid", "invalid", "invalid"]);
    }
    expect(f.calls).toHaveLength(0);
  });

  it("makes keys the server accepts, a new one each time", () => {
    const a = newIdempotencyKey();
    expect(a).toMatch(/^[\x21-\x7e]{8,128}$/);
    expect(newIdempotencyKey()).not.toBe(a);
  });

  it("calls each operation at the contract's method and path", async () => {
    const ok = () => json(200, { items: [], next_cursor: null });
    const f = fakeFetch(...Array.from({ length: 18 }, ok));
    const client = api(f);
    const seen: Record<string, number> = {};
    const record = (id: string) => (seen[id] = f.calls.length - 1);
    await client.getMe();
    record("getMe");
    await client.listProjects();
    record("listProjects");
    await client.listModels(PROJECT);
    record("listModels");
    await client.createModel(PROJECT, { name: "P", domain: "software_delivery", template: "default_delivery_pipeline" }, "key-contract-1");
    record("createModel");
    await client.getModel(MODEL);
    record("getModel");
    await client.getDraft(MODEL);
    record("getDraft");
    await client.patchDraft(MODEL, 1, [{ op: "test", path: "/name", value: "x" }]);
    record("patchDraft");
    await client.resetDraft(MODEL, 1, { expected_latest_version_id: VERSION, confirm_discard: true });
    record("resetDraft");
    await client.publishVersion(MODEL, 1, { expected_base_version_id: VERSION }, "key-contract-2");
    record("publishVersion");
    await client.listVersions(MODEL);
    record("listVersions");
    await client.getModelVersion(VERSION);
    record("getModelVersion");
    await client.createScenario(VERSION, { name: "Baseline", patch: [] }, "key-contract-3");
    record("createManualScenario");
    await client.listScenarios(VERSION);
    record("listScenarios");
    await client.getScenario(SCENARIO);
    record("getScenario");
    await client.compareSimulations(BASELINE_RUN, SCENARIO_RUN);
    record("compareSimulations");
    await client.listSimulations(MODEL, { cursor: "next" });
    record("listSimulations");
    // A page of up to 100 runs after the cursor (T-057 review r1 L6).
    expect(Object.fromEntries(f.calls[seen.listSimulations!]!.url.searchParams)).toEqual({ cursor: "next", limit: "100" });
    await client.createAnalysis(SCENARIO_RUN, { baseline_run_id: BASELINE_RUN, output_locale: "en" }, "key-contract-4");
    record("createAnalysis");
    await client.getAnalysis("ana_01M4YDQK00TA0000000000T030");
    record("getAnalysis");
    // An ID of another kind is refused before any request.
    const before = f.calls.length;
    expect((await client.listSimulations(PROJECT)).ok).toBe(false);
    expect(f.calls).toHaveLength(before);
    for (const [id, i] of Object.entries(seen)) {
      const op = operations.find((o) => o.operation_id === id);
      const call = f.calls[i]!;
      // The BFF's /api prefix in front of the contract's /v1 path (ADR-018).
      expect(call.url.pathname.startsWith("/api/v1/")).toBe(true);
      const template = call.url.pathname.slice("/api".length).replace(/\/(prj|mdl|mv|scn|sim|ana)_[0-9A-Z]{26}/g, "/{id}");
      expect([id, call.init.method, template]).toEqual([id, op?.method, op?.path]);
      // The tenant selector goes with every operation in an organization, and only with those (D-045).
      const selector = (call.init.headers as Record<string, string>)["X-Organization-ID"];
      expect([id, selector]).toEqual([id, op?.scope === "user" ? undefined : ORG]);
    }
  });
});

describe("scenarios (T-028)", () => {
  const VERSION = "mv_01M4YDQK00FRYWV7GS7X1QCXZR";
  const manual = fixture("valid-manual-scenario.json") as Scenario;
  const api = (f: ReturnType<typeof fakeFetch>) => createApiClient({ session: () => session, fetch: f.fetch });

  it("saves a scenario on a version with its Idempotency-Key and the body as given", async () => {
    const body = fixture("valid-create-scenario.json") as CreateScenario;
    const f = fakeFetch(json(201, manual));
    const r = await api(f).createScenario(VERSION, body, "key-scenario-1");
    expect(r.ok && r.data.source_proposal_id).toBe(null);
    const call = f.calls[0]!;
    const headers = call.init.headers as Record<string, string>;
    expect([call.init.method, call.url.pathname]).toEqual(["POST", `/api/v1/model-versions/${VERSION}/scenarios`]);
    expect([headers["Idempotency-Key"], headers["Content-Type"], headers["If-Match"]]).toEqual(["key-scenario-1", "application/json", undefined]);
    expect(JSON.parse(String(call.init.body))).toEqual(body);
  });

  it("lists a version's scenarios with the cursor and reads one", async () => {
    const page = fixture("valid-scenarios-page.json") as Scenarios;
    const f = fakeFetch(json(200, page), json(200, manual));
    const client = api(f);
    const listed = await client.listScenarios(VERSION, { cursor: "next-page" });
    expect(listed.ok && listed.data.items.map((s) => s.source_proposal_id)).toEqual(["aip_01M4YDQK00Q10EM95Z6H4XK0Y8", null]);
    expect([f.calls[0]!.url.pathname, f.calls[0]!.url.searchParams.get("cursor")]).toEqual([`/api/v1/model-versions/${VERSION}/scenarios`, "next-page"]);
    expect((await client.getScenario(SCENARIO)).ok).toBe(true);
    expect([f.calls[1]!.init.method, f.calls[1]!.url.pathname]).toEqual(["GET", `/api/v1/scenarios/${SCENARIO}`]);
  });

  it("maps a refused patch to its issues, nothing else", async () => {
    const f = fakeFetch(json(422, errorBody("MODEL_VALIDATION_FAILED", {
      details: [{ path: "/patch/1/path", reason: "the scenario allowlist does not include /nodes/5", code: "PATCH_PATH_FORBIDDEN", severity: "error" }],
    })));
    const r = await api(f).createScenario(VERSION, { name: "Remove a step", patch: [] }, "key-scenario-2");
    expect(!r.ok && validationIssues(r.problem)).toEqual([
      { code: "PATCH_PATH_FORBIDDEN", path: "/patch/1/path", message: "the scenario allowlist does not include /nodes/5", severity: "error" },
    ]);
  });

  it("refuses bad IDs and keys before sending anything", async () => {
    const f = fakeFetch();
    const client = api(f);
    const body = { name: "Baseline", patch: [] };
    const refused = [
      await client.createScenario(MODEL, body, "key-scenario-3"),
      await client.createScenario(VERSION, body, "short"),
      await client.listScenarios(MODEL),
      await client.getScenario(VERSION),
      await client.getScenario(`${SCENARIO}/../x`),
    ];
    expect(refused.map((r) => !r.ok && r.problem.kind)).toEqual(["not_found", "invalid", "not_found", "not_found", "not_found"]);
    expect(f.calls).toHaveLength(0);
  });
});

describe("comparisons (T-029)", () => {
  const api = (f: ReturnType<typeof fakeFetch>) => createApiClient({ session: () => session, fetch: f.fetch });

  it("asks for the server's comparison of two runs, by query, and shows it as given", async () => {
    const golden = fixture("valid-comparison.json") as Comparison;
    const f = fakeFetch(json(200, golden));
    const r = await api(f).compareSimulations(BASELINE_RUN, SCENARIO_RUN);
    expect(r.ok && r.data).toEqual(golden);
    const call = f.calls[0]!;
    expect([call.init.method, call.url.pathname, call.init.body]).toEqual(["GET", "/api/v1/simulation-comparisons", undefined]);
    expect([call.url.searchParams.getAll("baseline_run_id"), call.url.searchParams.getAll("scenario_run_id")]).toEqual([[BASELINE_RUN], [SCENARIO_RUN]]);
    expect((call.init.headers as Record<string, string>)["X-Organization-ID"]).toBe(ORG);
  });

  it("maps a run that has no result yet to the contract's conflict, naming the run", async () => {
    const f = fakeFetch(json(409, errorBody("RESULT_NOT_READY", { details: [{ path: "", reason: "scenario_run_id" }] })));
    const r = await api(f).compareSimulations(BASELINE_RUN, SCENARIO_RUN);
    expect(!r.ok && [r.problem.kind, r.problem.code, r.problem.details[0]?.reason]).toEqual(["conflict", "RESULT_NOT_READY", "scenario_run_id"]);
  });

  it("refuses run IDs the server would refuse before sending anything", async () => {
    const f = fakeFetch();
    const client = api(f);
    const refused = [
      await client.compareSimulations(SCENARIO, SCENARIO_RUN),
      await client.compareSimulations(BASELINE_RUN, `${SCENARIO_RUN}&baseline_run_id=${BASELINE_RUN}`),
      await client.compareSimulations(BASELINE_RUN, BASELINE_RUN),
    ];
    expect(refused.map((r) => !r.ok && r.problem.kind)).toEqual(["not_found", "not_found", "invalid"]);
    expect(f.calls).toHaveLength(0);
  });
});
