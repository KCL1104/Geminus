// T-014: the API boundary fails closed, sends the tenant selector and If-Match,
// and maps every error the contract defines (and nothing it does not).
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { createApiClient, newIdempotencyKey, type Session } from "../lib/api/client";
import { draftETag, parseDraftETag } from "../lib/api/etag";
import { isID } from "../lib/api/ids";
import { errorCodeStatus, problemFromResponse, validationIssues } from "../lib/api/problem";
import type { Draft, JsonPatch, ModelVersion } from "../lib/contracts";

const commonSchema = JSON.parse(readFileSync(new URL("../../../schemas/common.schema.json", import.meta.url), "utf8")) as {
  $defs: { ErrorCode: { enum: string[]; "x-http-status": Record<string, number> } };
};
const fixture = (name: string): unknown =>
  JSON.parse(readFileSync(new URL(`../../../testdata/contracts/${name}`, import.meta.url), "utf8"));

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const MODEL = "mdl_01M4YDQK004AGVNEGJQJ1NMM0N";
const PROJECT = "prj_01M4YDQK00TA00000000000001";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const session: Session = { accessToken: "test-token", organizationId: ORG };

type Call = { url: URL; init: RequestInit };

/** A fetch test double that records calls and answers with queued responses. */
function fakeFetch(...responses: (Response | Error)[]) {
  const calls: Call[] = [];
  const fn = async (input: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: new URL(String(input)), init: init ?? {} });
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
  it("sends nothing without an API URL or a session", async () => {
    const f = fakeFetch();
    const noURL = await createApiClient({ baseUrl: null, session: () => session, fetch: f.fetch }).listProjects();
    const badURL = await createApiClient({ baseUrl: "javascript:alert(1)", session: () => session, fetch: f.fetch }).listProjects();
    const noSession = await createApiClient({ baseUrl: "https://api.test", session: () => null, fetch: f.fetch }).listProjects();
    expect([noURL.ok, badURL.ok, noSession.ok]).toEqual([false, false, false]);
    expect(!noURL.ok && noURL.problem.kind).toBe("unavailable");
    expect(!badURL.ok && badURL.problem.kind).toBe("unavailable");
    expect(!noSession.ok && noSession.problem.kind).toBe("unauthenticated");
    expect(f.calls).toHaveLength(0);
  });

  it("sends the bearer token and tenant selector, never cookies", async () => {
    const f = fakeFetch(json(200, { items: [], next_cursor: null }, { "X-Request-ID": REQ }));
    const api = createApiClient({ baseUrl: "https://api.test/v0/", session: () => session, fetch: f.fetch });
    const r = await api.listProjects({ cursor: "c1" });
    expect(r).toEqual({ ok: true, data: { items: [], next_cursor: null }, requestId: REQ, etag: null });
    const call = f.calls[0]!;
    expect(call.url.toString()).toBe("https://api.test/v0/projects?cursor=c1");
    const headers = call.init.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer test-token");
    expect(headers["X-Organization-ID"]).toBe(ORG);
    expect(call.init.credentials).toBe("omit");
  });

  it("rejects malformed route IDs locally as not found", async () => {
    const f = fakeFetch();
    const api = createApiClient({ baseUrl: "https://api.test", session: () => session, fetch: f.fetch });
    for (const r of [await api.getModel("../projects"), await api.getDraft("mdl_bad"), await api.listModels("mdl_01M4YDQK004AGVNEGJQJ1NMM0N")]) {
      expect(!r.ok && r.problem.kind).toBe("not_found");
    }
    expect(f.calls).toHaveLength(0);
  });

  it("saves a draft with If-Match and the JSON Patch media type", async () => {
    const saved = draft(4);
    const f = fakeFetch(json(200, saved, { ETag: draftETag(4) }));
    const api = createApiClient({ baseUrl: "https://api.test", session: () => session, fetch: f.fetch });
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
    const r = await createApiClient({ baseUrl: "https://api.test", session: () => session, fetch: f.fetch }).patchDraft(MODEL, 3, [{ op: "test", path: "/name", value: "x" }]);
    expect(!r.ok && [r.problem.kind, r.problem.code]).toEqual(["conflict", "MODEL_VERSION_CONFLICT"]);
  });

  it("treats a draft without a matching ETag as a protocol error", async () => {
    for (const etag of [undefined, draftETag(5), 'W/"draft-revision-4"']) {
      const f = fakeFetch(json(200, draft(4), etag === undefined ? {} : { ETag: etag }));
      const r = await createApiClient({ baseUrl: "https://api.test", session: () => session, fetch: f.fetch }).getDraft(MODEL);
      expect(!r.ok && r.problem.kind).toBe("protocol");
    }
  });

  it("maps transport failures and non-JSON successes", async () => {
    const down = fakeFetch(new TypeError("fetch failed"));
    const r1 = await createApiClient({ baseUrl: "https://api.test", session: () => session, fetch: down.fetch }).listProjects();
    expect(!r1.ok && r1.problem.kind).toBe("network");
    const html = fakeFetch(new Response("<html>", { status: 200 }));
    const r2 = await createApiClient({ baseUrl: "https://api.test", session: () => session, fetch: html.fetch }).listProjects();
    expect(!r2.ok && r2.problem.kind).toBe("protocol");
    const ctl = new AbortController();
    ctl.abort();
    const aborted = fakeFetch(new DOMException("aborted", "AbortError"));
    const r3 = await createApiClient({ baseUrl: "https://api.test", session: () => session, fetch: aborted.fetch }).listProjects({ signal: ctl.signal });
    expect(!r3.ok && r3.problem.message).toBe("Request aborted.");
    expect(aborted.calls[0]!.init.signal).toBe(ctl.signal);
  });

  it("reads the session per request, so a sign-out takes effect immediately", async () => {
    let current: Session | null = session;
    const f = fakeFetch(json(200, { items: [], next_cursor: null }));
    const api = createApiClient({ baseUrl: "https://api.test", session: () => current, fetch: f.fetch });
    expect((await api.listProjects()).ok).toBe(true);
    current = null;
    const r = await api.listProjects();
    expect(!r.ok && r.problem.kind).toBe("unauthenticated");
    expect(f.calls).toHaveLength(1);
  });

  it("keeps a valid project ID path intact", async () => {
    const f = fakeFetch(json(200, { items: [], next_cursor: null }));
    await createApiClient({ baseUrl: "https://api.test", session: () => session, fetch: f.fetch }).listModels(PROJECT);
    expect(f.calls[0]!.url.pathname).toBe(`/projects/${PROJECT}/models`);
  });
});

const operations = (
  JSON.parse(readFileSync(new URL("../../../planning/api-operations.json", import.meta.url), "utf8")) as {
    operations: { method: string; path: string; operation_id: string }[];
  }
).operations;

describe("model writes and versions (T-013)", () => {
  const VERSION = "mv_01M4YDQK00FRYWV7GS7X1QCXZR";
  const version = fixture("valid-model-version.json") as ModelVersion;
  const api = (f: ReturnType<typeof fakeFetch>) => createApiClient({ baseUrl: "https://api.test/v1", session: () => session, fetch: f.fetch });

  it("creates a model with its Idempotency-Key and a JSON body", async () => {
    const f = fakeFetch(json(201, { id: MODEL }));
    const r = await api(f).createModel(PROJECT, { name: "Pipeline", domain: "software_delivery", template: "default_delivery_pipeline" }, "key-create-1");
    expect(r.ok).toBe(true);
    const call = f.calls[0]!;
    const headers = call.init.headers as Record<string, string>;
    expect([call.init.method, call.url.pathname]).toEqual(["POST", `/v1/projects/${PROJECT}/models`]);
    expect([headers["Idempotency-Key"], headers["Content-Type"], headers["If-Match"]]).toEqual(["key-create-1", "application/json", undefined]);
    expect(JSON.parse(String(call.init.body))).toEqual({ name: "Pipeline", domain: "software_delivery", template: "default_delivery_pipeline" });
  });

  it("publishes with If-Match and the key, and reads the version back", async () => {
    const f = fakeFetch(json(201, version), json(200, version));
    const client = api(f);
    const r = await client.publishVersion(MODEL, 3, { expected_base_version_id: VERSION, comment: "Ready" }, "key-publish-1");
    expect(r.ok && r.data.comment).toBe(null);
    const headers = f.calls[0]!.init.headers as Record<string, string>;
    expect([f.calls[0]!.init.method, f.calls[0]!.url.pathname]).toEqual(["POST", `/v1/models/${MODEL}/versions`]);
    expect([headers["If-Match"], headers["Idempotency-Key"]]).toEqual(['"draft-revision-3"', "key-publish-1"]);
    expect(JSON.parse(String(f.calls[0]!.init.body))).toEqual({ expected_base_version_id: VERSION, comment: "Ready" });
    expect((await client.getModelVersion(VERSION)).ok).toBe(true);
    expect(f.calls[1]!.url.pathname).toBe(`/v1/model-versions/${VERSION}`);
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

  it("makes keys the server accepts, a new one each time", () => {
    const a = newIdempotencyKey();
    expect(a).toMatch(/^[\x21-\x7e]{8,128}$/);
    expect(newIdempotencyKey()).not.toBe(a);
  });

  it("calls each operation at the contract's method and path", async () => {
    const ok = () => json(200, { items: [], next_cursor: null });
    const f = fakeFetch(...Array.from({ length: 10 }, ok));
    const client = api(f);
    const seen: Record<string, number> = {};
    const record = (id: string) => (seen[id] = f.calls.length - 1);
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
    for (const [id, i] of Object.entries(seen)) {
      const op = operations.find((o) => o.operation_id === id);
      const call = f.calls[i]!;
      const template = call.url.pathname.replace(/\/(prj|mdl|mv)_[0-9A-Z]{26}/g, "/{id}");
      expect([id, call.init.method, template]).toEqual([id, op?.method, op?.path]);
    }
  });
});
