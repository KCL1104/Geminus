// Review of T-014 (2026-10-15): the client fails closed on every path,
// including a body that breaks off, and request IDs on success responses.
// Since T-043 it holds no token and calls only the same-origin BFF.
import { describe, expect, it } from "vitest";
import { createApiClient } from "../lib/api/client";
import { draftETag } from "../lib/api/etag";

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const MDL = "mdl_01M4YDQK00TA00000000000001";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const session = () => ({ organizationId: ORG });

type Call = { url: string; init: RequestInit | undefined };
function recorder(respond: () => Response | Promise<Response>) {
  const calls: Call[] = [];
  const fetch = (async (url: URL | string, init?: RequestInit) => {
    calls.push({ url: String(url), init });
    return respond();
  }) as typeof globalThis.fetch;
  return { calls, fetch };
}

const breaking = (error: Error) =>
  new Response(new ReadableStream({ start: (c) => c.error(error) }), { status: 200, headers: { "Content-Type": "application/json" } });

describe("body read failures", () => {
  it("returns a network problem when the body breaks off, never a rejection", async () => {
    const { fetch } = recorder(() => breaking(new TypeError("terminated")));
    const r = await createApiClient({ session, fetch }).listProjects();
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.problem.kind).toBe("network");
  });

  it("reports an abort during the body as an aborted request", async () => {
    const ctl = new AbortController();
    const { fetch } = recorder(() => {
      ctl.abort();
      return breaking(new DOMException("aborted", "AbortError"));
    });
    const r = await createApiClient({ session, fetch }).listProjects({ signal: ctl.signal });
    expect(r.ok).toBe(false);
    if (!r.ok) expect([r.problem.kind, r.problem.message]).toEqual(["network", "Request aborted."]);
  });
});

describe("same origin", () => {
  it("only ever requests a path of the BFF's API proxy", async () => {
    const { calls, fetch } = recorder(() => new Response(JSON.stringify({ items: [], next_cursor: null })));
    const api = createApiClient({ session, fetch });
    await api.listProjects({ cursor: "//evil.example/x" });
    await api.listModels("prj_01M4YDQK00TA00000000000001", { cursor: "a&b=c" });
    for (const c of calls) {
      expect(c.url.startsWith("/api/v1/")).toBe(true);
      expect(new URL(c.url, "https://app.test").origin).toBe("https://app.test");
    }
    expect(calls.map((c) => c.url)).toEqual(["/api/v1/projects?cursor=%2F%2Fevil.example%2Fx", "/api/v1/projects/prj_01M4YDQK00TA00000000000001/models?cursor=a%26b%3Dc"]);
  });
});

describe("request IDs and patches", () => {
  it("keeps only a valid request ID on every path", async () => {
    const bad = "attacker-chosen <b>id</b>";
    for (const [body, headers] of [
      ["not json", { "X-Request-ID": bad }],
      [JSON.stringify({ items: [], next_cursor: null }), { "X-Request-ID": bad }],
    ] as const) {
      const { fetch } = recorder(() => new Response(body, { status: 200, headers }));
      const r = await createApiClient({ session, fetch }).listProjects();
      expect(r.ok ? r.requestId : r.problem.requestId).toBeNull();
    }
    const { fetch } = recorder(() => new Response(JSON.stringify({ items: [], next_cursor: null }), { headers: { "X-Request-ID": REQ } }));
    const ok = await createApiClient({ session, fetch }).listProjects();
    expect(ok.ok && ok.requestId).toBe(REQ);
  });

  it("refuses an empty patch without sending it (the contract needs at least one op)", async () => {
    const { calls, fetch } = recorder(() => new Response("{}", { headers: { ETag: draftETag(4) } }));
    const r = await createApiClient({ session, fetch }).patchDraft(MDL, 3, []);
    expect([r.ok, calls.length]).toEqual([false, 0]);
    if (!r.ok) expect(r.problem.kind).toBe("invalid");
  });
});
