// Review of T-014 (2026-10-15): the client fails closed on every path,
// including a body that breaks off, a base URL that would move the token to
// another host, and request IDs on success responses.
import { describe, expect, it } from "vitest";
import { createApiClient } from "../lib/api/client";
import { draftETag } from "../lib/api/etag";

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const MDL = "mdl_01M4YDQK00TA00000000000001";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const session = () => ({ accessToken: "secret-token", organizationId: ORG });

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
    const r = await createApiClient({ baseUrl: "https://api.test", session, fetch }).listProjects();
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.problem.kind).toBe("network");
  });

  it("reports an abort during the body as an aborted request", async () => {
    const ctl = new AbortController();
    const { fetch } = recorder(() => {
      ctl.abort();
      return breaking(new DOMException("aborted", "AbortError"));
    });
    const r = await createApiClient({ baseUrl: "https://api.test", session, fetch }).listProjects({ signal: ctl.signal });
    expect(r.ok).toBe(false);
    if (!r.ok) expect([r.problem.kind, r.problem.message]).toEqual(["network", "Request aborted."]);
  });
});

describe("base URL", () => {
  it("never sends the token to a host other than the configured one", async () => {
    for (const baseUrl of ["https://api.example.com//v1", "https://api.example.com/v1//x", "http://api.example.com",
      "https://user:pw@api.example.com", "https://api.example.com/?x=1", "https://api.example.com/#f", "ftp://api.example.com"]) {
      const { calls, fetch } = recorder(() => new Response("{}"));
      const r = await createApiClient({ baseUrl, session, fetch }).listProjects();
      expect([baseUrl, r.ok, calls.length]).toEqual([baseUrl, false, 0]);
      if (!r.ok) expect(r.problem.kind).toBe("unavailable");
    }
  });

  it("joins paths under the base path and allows plain http only on loopback", async () => {
    for (const [baseUrl, want] of [
      ["https://api.example.com/v1/", "https://api.example.com/v1/projects"],
      ["https://api.example.com/v1", "https://api.example.com/v1/projects"],
      ["https://api.example.com", "https://api.example.com/projects"],
      ["http://localhost:8080", "http://localhost:8080/projects"],
      ["http://127.0.0.1:8080/api", "http://127.0.0.1:8080/api/projects"],
      ["http://[::1]:8080", "http://[::1]:8080/projects"],
    ] as const) {
      const { calls, fetch } = recorder(() => new Response(JSON.stringify({ items: [], next_cursor: null })));
      await createApiClient({ baseUrl, session, fetch }).listProjects();
      expect(calls.map((c) => c.url)).toEqual([want]);
    }
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
      const r = await createApiClient({ baseUrl: "https://api.test", session, fetch }).listProjects();
      expect(r.ok ? r.requestId : r.problem.requestId).toBeNull();
    }
    const { fetch } = recorder(() => new Response(JSON.stringify({ items: [], next_cursor: null }), { headers: { "X-Request-ID": REQ } }));
    const ok = await createApiClient({ baseUrl: "https://api.test", session, fetch }).listProjects();
    expect(ok.ok && ok.requestId).toBe(REQ);
  });

  it("refuses an empty patch without sending it (the contract needs at least one op)", async () => {
    const { calls, fetch } = recorder(() => new Response("{}", { headers: { ETag: draftETag(4) } }));
    const r = await createApiClient({ baseUrl: "https://api.test", session, fetch }).patchDraft(MDL, 3, []);
    expect([r.ok, calls.length]).toEqual([false, 0]);
    if (!r.ok) expect(r.problem.kind).toBe("invalid");
  });
});
