// T-032 (hackathon slice): the client for the run endpoints T-023 serves
// (createSimulation, getSimulation, cancelSimulation, getSimulationResults,
// downloadSimulationTrace), at the contract's method and path, with the
// tenant selector and, for the two writes, an Idempotency-Key the caller
// keeps for retries (D-011). It refuses what the contract would refuse
// before sending anything, and shows the server's answers as given.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { createApiClient } from "../lib/api/client";
import type { Result as SimulationResult, Run, RunRequest, TraceArtifact } from "../lib/contracts";

const fixture = (name: string): unknown => JSON.parse(readFileSync(new URL(`../../../testdata/contracts/${name}`, import.meta.url), "utf8"));
const operations = (JSON.parse(readFileSync(new URL("../../../planning/api-operations.json", import.meta.url), "utf8")) as { operations: { operation_id: string; method: string; path: string }[] }).operations;

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const VERSION = "mv_01M4YDQK00FRYWV7GS7X1QCXZR";
const SCENARIO = "scn_01M4YDQK00J4MJS82YRG0PCEC1";
const RUN = "sim_01M4YDQK00FGZZWH0PP15XPNFA";

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

const request: RunRequest = {
  model_version_id: VERSION, name: "Baseline", scenario_patch: [],
  config: { horizon: { value: 600, unit: "minute" }, warmup: { value: 0, unit: "minute" }, replications: 5, seed: 42, trace_level: "sampled" },
};
const accepted = { id: RUN, status: "queued", model_version_id: VERSION, created_at: "2026-10-26T09:00:00Z" };

describe("runs (T-023) through the client", () => {
  it("queues a run with its Idempotency-Key and the request as given, and reads the 202", async () => {
    const f = fakeFetch(json(202, accepted, { "X-Request-ID": REQ }));
    const r = await api(f).createSimulation(request, "key-run-0001");
    expect(r.ok && [r.data, r.requestId]).toEqual([accepted, REQ]);
    const c = f.calls[0]!;
    expect([c.init.method, c.url.pathname]).toEqual(["POST", "/api/v1/simulations"]);
    expect([headers(c)["Idempotency-Key"], headers(c)["Content-Type"], headers(c)["X-Organization-ID"]]).toEqual(["key-run-0001", "application/json", ORG]);
    expect(JSON.parse(String(c.init.body))).toEqual(request);
  });

  it("queues a saved scenario by its ID", async () => {
    const f = fakeFetch(json(202, accepted));
    const body: RunRequest = { model_version_id: VERSION, name: "Two fixtures", scenario_id: SCENARIO, config: request.config };
    await api(f).createSimulation(body, "key-run-0002");
    expect(JSON.parse(String(f.calls[0]!.init.body))).toEqual(body);
  });

  it("reads a run, its result and its trace, and cancels it with an empty body and its key", async () => {
    const run = fixture("valid-run-failed.json") as Run;
    const result = fixture("valid-result.json") as SimulationResult;
    const trace = fixture("valid-trace-artifact.json") as TraceArtifact;
    const f = fakeFetch(json(200, run), json(200, result), json(200, trace, { "Content-Disposition": `attachment; filename="${RUN}-trace.json"` }), json(200, run));
    const client = api(f);
    expect((await client.getSimulation(RUN)).ok && true).toBe(true);
    const got = await client.getSimulationResults(RUN);
    expect(got.ok && got.data).toEqual(result);
    const t = await client.downloadSimulationTrace(RUN);
    expect(t.ok && t.data).toEqual(trace);
    await client.cancelSimulation(RUN, "key-cancel-0001");
    expect(f.calls.map((c) => [c.init.method, c.url.pathname])).toEqual([
      ["GET", `/api/v1/simulations/${RUN}`],
      ["GET", `/api/v1/simulations/${RUN}/results`],
      ["GET", `/api/v1/simulations/${RUN}/trace`],
      ["POST", `/api/v1/simulations/${RUN}/cancel`],
    ]);
    const cancel = f.calls[3]!;
    expect([cancel.init.body, headers(cancel)["Idempotency-Key"], headers(cancel)["Content-Type"]]).toEqual(["{}", "key-cancel-0001", "application/json"]);
  });

  it("calls each operation at the contract's method and path, with the tenant selector", async () => {
    const f = fakeFetch(json(202, accepted), json(200, {}), json(200, {}), json(200, {}), json(200, {}));
    const client = api(f);
    await client.createSimulation(request, "key-run-0003");
    await client.getSimulation(RUN);
    await client.cancelSimulation(RUN, "key-cancel-0002");
    await client.getSimulationResults(RUN);
    await client.downloadSimulationTrace(RUN);
    const ids = ["createSimulation", "getSimulation", "cancelSimulation", "getSimulationResults", "downloadSimulationTrace"];
    ids.forEach((id, i) => {
      const op = operations.find((o) => o.operation_id === id);
      const c = f.calls[i]!;
      const template = c.url.pathname.slice("/api".length).replace(/\/sim_[0-9A-Z]{26}/g, "/{id}");
      expect([id, c.init.method, template, headers(c)["X-Organization-ID"]]).toEqual([id, op?.method, op?.path, ORG]);
    });
  });

  it("maps a run that has no result yet, and a run without a trace, to the contract's problems", async () => {
    const notReady = { error: { code: "RESULT_NOT_READY", message: "not completed", request_id: REQ } };
    const noTrace = { error: { code: "NOT_FOUND", message: "no trace", request_id: REQ } };
    const f = fakeFetch(json(409, notReady), json(404, noTrace));
    const client = api(f);
    const r = await client.getSimulationResults(RUN);
    expect(!r.ok && [r.problem.kind, r.problem.code]).toEqual(["conflict", "RESULT_NOT_READY"]);
    const t = await client.downloadSimulationTrace(RUN);
    expect(!t.ok && [t.problem.kind, t.problem.code]).toEqual(["not_found", "NOT_FOUND"]);
  });

  it("refuses bad IDs and keys before sending anything", async () => {
    const f = fakeFetch();
    const client = api(f);
    const refused = [
      await client.createSimulation({ ...request, model_version_id: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N" }, "key-run-0004"),
      await client.createSimulation({ model_version_id: VERSION, name: "x", scenario_id: "scn_bad", config: request.config }, "key-run-0005"),
      await client.createSimulation(request, "short"),
      await client.getSimulation(SCENARIO),
      await client.getSimulation(`${RUN}/../x`),
      await client.cancelSimulation(RUN, "short"),
      await client.cancelSimulation("sim_bad", "key-cancel-0003"),
      await client.getSimulationResults(VERSION),
      await client.downloadSimulationTrace(`${RUN}?x=1`),
    ];
    expect(refused.map((r) => !r.ok && r.problem.kind)).toEqual(["not_found", "not_found", "invalid", "not_found", "not_found", "invalid", "not_found", "not_found", "not_found"]);
    expect(f.calls).toHaveLength(0);
  });
});
