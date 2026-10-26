// T-031 (hackathon slice, H-029): the client for the AI proposal endpoints
// T-025/T-026 serve (createProposal, getProposal, approveProposal,
// rejectProposal), at the contract's method and path, with the tenant
// selector and, for the three writes, an Idempotency-Key the caller keeps
// for retries (D-011). It refuses what the contract would refuse before
// sending anything, and hands the server's answers over as given: the
// approval's mode says which effect it names (T-028 contract test).
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { createApiClient } from "../lib/api/client";
import type { Approval, Proposal, ProposalRequest } from "../lib/contracts";

const fixture = (name: string): unknown => JSON.parse(readFileSync(new URL(`../../../testdata/contracts/${name}`, import.meta.url), "utf8"));
const operations = (JSON.parse(readFileSync(new URL("../../../planning/api-operations.json", import.meta.url), "utf8")) as { operations: { operation_id: string; method: string; path: string }[] }).operations;

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const MODEL = "mdl_01M4YDQK004AGVNEGJQJ1NMM0N";
const VERSION = "mv_01M4YDQK00FRYWV7GS7X1QCXZR";
const PROPOSAL = "aip_01M4YDQK00ADN261CME3WV5HZ6";

type Call = { url: URL; init: RequestInit };
function fakeFetch(...responses: Response[]) {
  const calls: Call[] = [];
  const fn = async (input: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: new URL(String(input), "https://app.test"), init: init ?? {} });
    const next = responses.shift();
    if (next === undefined) throw new Error("unexpected request");
    return next;
  };
  return { fetch: fn as typeof fetch, calls };
}
const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });
const api = (f: ReturnType<typeof fakeFetch>) => createApiClient({ session: () => ({ organizationId: ORG }), fetch: f.fetch });
const headers = (c: Call) => c.init.headers as Record<string, string>;

const request: ProposalRequest = { base_version_id: VERSION, mode: "edit", intent: "Burn-in batches of 48 boards", output_locale: "en" };

describe("AI proposals (T-025, T-026) through the client", () => {
  it("asks for a proposal with its Idempotency-Key and the request as given, and reads the 202", async () => {
    const accepted = fixture("valid-proposal-accepted.json");
    const f = fakeFetch(json(202, accepted, { "X-Request-ID": REQ }));
    const r = await api(f).createProposal(MODEL, request, "key-ask-00001");
    expect(r.ok && [r.data, r.requestId]).toEqual([accepted, REQ]);
    const c = f.calls[0]!;
    expect([c.init.method, c.url.pathname]).toEqual(["POST", `/api/v1/models/${MODEL}/ai-proposals`]);
    expect([headers(c)["Idempotency-Key"], headers(c)["Content-Type"], headers(c)["X-Organization-ID"]]).toEqual(["key-ask-00001", "application/json", ORG]);
    expect(JSON.parse(String(c.init.body))).toEqual(request);
  });

  it("reads a proposal as the server sent it", async () => {
    const ready = fixture("valid-ready-proposal.json") as Proposal;
    const f = fakeFetch(json(200, ready));
    const r = await api(f).getProposal(PROPOSAL);
    expect(r.ok && r.data).toEqual(ready);
    expect([f.calls[0]!.init.method, f.calls[0]!.url.pathname]).toEqual(["GET", `/api/v1/ai-proposals/${PROPOSAL}`]);
  });

  it("approves with the base the person reviewed and its key; the mode tells the effect apart", async () => {
    const edit = fixture("valid-approval-edit.json") as Approval;
    const scenario = fixture("valid-scenario-approval.json") as Approval;
    const f = fakeFetch(json(201, edit), json(201, scenario));
    const client = api(f);
    const a = await client.approveProposal(PROPOSAL, { expected_base_version_id: VERSION }, "key-approve-01");
    const b = await client.approveProposal("aip_01M4YDQK00Q10EM95Z6H4XK0Y8", { expected_base_version_id: VERSION }, "key-approve-02");
    expect(a.ok && a.data).toEqual(edit);
    expect(b.ok && b.data.mode === "scenario" && b.data.scenario_id).toBe("scn_01M4YDQK00J4MJS82YRG0PCEC0");
    const c = f.calls[0]!;
    expect([c.init.method, c.url.pathname]).toEqual(["POST", `/api/v1/ai-proposals/${PROPOSAL}/approve`]);
    expect([headers(c)["Idempotency-Key"], headers(c)["Content-Type"]]).toEqual(["key-approve-01", "application/json"]);
    expect(JSON.parse(String(c.init.body))).toEqual({ expected_base_version_id: VERSION });
  });

  it("rejects with an optional comment and its key, and reads the proposal it answers with", async () => {
    const rejected = fixture("valid-proposal-rejected.json") as Proposal;
    const f = fakeFetch(json(200, rejected), json(200, rejected));
    const client = api(f);
    const r = await client.rejectProposal(PROPOSAL, { comment: "Asked again with the step named." }, "key-reject-01");
    await client.rejectProposal(PROPOSAL, {}, "key-reject-02");
    expect(r.ok && r.data).toEqual(rejected);
    expect(f.calls.map((c) => [c.init.method, c.url.pathname, JSON.parse(String(c.init.body)), headers(c)["Idempotency-Key"]])).toEqual([
      ["POST", `/api/v1/ai-proposals/${PROPOSAL}/reject`, { comment: "Asked again with the step named." }, "key-reject-01"],
      ["POST", `/api/v1/ai-proposals/${PROPOSAL}/reject`, {}, "key-reject-02"],
    ]);
  });

  it("calls each operation at the contract's method and path, with the tenant selector", async () => {
    const f = fakeFetch(json(202, {}), json(200, {}), json(201, {}), json(200, {}));
    const client = api(f);
    await client.createProposal(MODEL, request, "key-ask-00002");
    await client.getProposal(PROPOSAL);
    await client.approveProposal(PROPOSAL, { expected_base_version_id: VERSION }, "key-approve-03");
    await client.rejectProposal(PROPOSAL, {}, "key-reject-03");
    const ids = ["createProposal", "getProposal", "approveProposal", "rejectProposal"];
    ids.forEach((id, i) => {
      const op = operations.find((o) => o.operation_id === id);
      const c = f.calls[i]!;
      const template = c.url.pathname.slice("/api".length).replace(/\/(mdl|aip)_[0-9A-Z]{26}/g, "/{id}");
      expect([id, c.init.method, template, headers(c)["X-Organization-ID"]]).toEqual([id, op?.method, op?.path, ORG]);
    });
  });

  it("maps the refusals the review UI handles to the contract's problems", async () => {
    const err = (code: string, details?: unknown[]) => ({ error: { code, message: "x", request_id: REQ, ...(details ? { details } : {}) } });
    const f = fakeFetch(
      json(409, err("MODEL_VERSION_CONFLICT", [{ path: "/expected_base_version_id", reason: "the proposal's base is not the model's latest version" }])),
      json(409, err("PROPOSAL_ALREADY_DECIDED")),
      json(422, err("MODEL_VALIDATION_FAILED", [{ path: "/dataset_bindings/0/content_hash", reason: "x", code: "DATASET_BINDING_INVALID", severity: "error" }])),
      json(503, err("AI_PROVIDER_NOT_CONFIGURED")),
    );
    const client = api(f);
    const stale = await client.approveProposal(PROPOSAL, { expected_base_version_id: VERSION }, "key-approve-04");
    const decided = await client.rejectProposal(PROPOSAL, {}, "key-reject-04");
    const invalid = await client.approveProposal(PROPOSAL, { expected_base_version_id: VERSION }, "key-approve-05");
    const off = await client.createProposal(MODEL, request, "key-ask-00003");
    expect([stale, decided, invalid, off].map((r) => !r.ok && [r.problem.kind, r.problem.code, r.problem.requestId])).toEqual([
      ["conflict", "MODEL_VERSION_CONFLICT", REQ],
      ["conflict", "PROPOSAL_ALREADY_DECIDED", REQ],
      ["invalid", "MODEL_VALIDATION_FAILED", REQ],
      ["unavailable", "AI_PROVIDER_NOT_CONFIGURED", REQ],
    ]);
    expect(!invalid.ok && invalid.problem.details[0]?.code).toBe("DATASET_BINDING_INVALID");
  });

  it("refuses bad IDs and keys before sending anything", async () => {
    const f = fakeFetch();
    const client = api(f);
    const refused = [
      await client.createProposal(VERSION, request, "key-ask-00004"),
      await client.createProposal(`${MODEL}/../x`, request, "key-ask-00005"),
      await client.createProposal(MODEL, request, "short"),
      await client.createProposal(MODEL, { ...request, base_version_id: MODEL }, "key-ask-00006"),
      await client.getProposal(MODEL),
      await client.getProposal(`${PROPOSAL}?x=1`),
      await client.approveProposal("aip_bad", { expected_base_version_id: VERSION }, "key-approve-06"),
      await client.approveProposal(PROPOSAL, { expected_base_version_id: VERSION }, "short"),
      await client.approveProposal(PROPOSAL, { expected_base_version_id: "mv_bad" }, "key-approve-07"),
      await client.rejectProposal(VERSION, {}, "key-reject-05"),
      await client.rejectProposal(PROPOSAL, {}, "x".repeat(129)),
    ];
    expect(refused.map((r) => !r.ok && r.problem.kind)).toEqual(["not_found", "not_found", "invalid", "not_found", "not_found", "not_found", "not_found", "invalid", "not_found", "not_found", "invalid"]);
    expect(f.calls).toHaveLength(0);
  });
});
