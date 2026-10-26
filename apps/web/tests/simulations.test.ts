// T-032 (hackathon slice): running a scenario and its baseline. The two
// runs share one explicit config (same horizon, warm-up, replications and
// seed: SIMULATION_SPEC §7's conditions for a comparison), the baseline
// runs the scenario's base version with the patch [] and the scenario by
// its saved ID. Keys are kept for retries of the same request (D-011). The
// pair's state only moves forward: an older answer never replaces a newer one.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { Run, Scenario } from "../lib/contracts";
import { defaultRunForm, keysFor, parseRunForm, runName, runRequests } from "../features/simulations/run-config";
import { checkPair, isActive, newer, pairPhase, reducePair, runProblem, type PairState } from "../features/simulations/run-pair";

const fixture = (name: string): unknown => JSON.parse(readFileSync(new URL(`../../../testdata/contracts/${name}`, import.meta.url), "utf8"));
const scenario = fixture("valid-manual-scenario.json") as Scenario;
const failed = fixture("valid-run-failed.json") as Run;

describe("the run settings", () => {
  it("start at one day of the model's time unit, no warm-up, 5 replications and seed 1", () => {
    expect(defaultRunForm("minute")).toEqual({ horizon: "1440", warmup: "0", replications: "5", seed: "1" });
    expect(defaultRunForm("second").horizon).toBe("86400");
    expect(defaultRunForm("hour").horizon).toBe("24");
    expect(defaultRunForm("day").horizon).toBe("1");
  });

  it("become the run config in the model's unit, with a sampled trace for the chart", () => {
    expect(parseRunForm({ horizon: " 600 ", warmup: "30.5", replications: "10", seed: "42" }, "minute")).toEqual({
      ok: true,
      config: { horizon: { value: 600, unit: "minute" }, warmup: { value: 30.5, unit: "minute" }, replications: 10, seed: 42, trace_level: "sampled" },
    });
    expect(parseRunForm({ horizon: "1", warmup: "0", replications: "1", seed: "9007199254740991" }, "day").ok).toBe(true);
  });

  it("name each field that is not acceptable, and send nothing", () => {
    const issues = (f: Partial<Record<"horizon" | "warmup" | "replications" | "seed", string>>) => {
      const r = parseRunForm({ horizon: "600", warmup: "0", replications: "5", seed: "1", ...f }, "minute");
      return r.ok ? {} : r.issues;
    };
    expect(issues({ horizon: "" })).toEqual({ horizon: "required" });
    expect(issues({ horizon: "1,5" })).toEqual({ horizon: "number" });
    expect(issues({ horizon: "1e3" })).toEqual({ horizon: "number" });
    expect(issues({ horizon: "-5" })).toEqual({ horizon: "number" });
    expect(issues({ horizon: "0" })).toEqual({ horizon: "positive" });
    expect(issues({ warmup: "600" })).toEqual({ warmup: "warmup" });
    expect(issues({ warmup: "x" })).toEqual({ warmup: "number" });
    expect(issues({ replications: "0" })).toEqual({ replications: "replications" });
    expect(issues({ replications: "1001" })).toEqual({ replications: "replications" });
    expect(issues({ replications: "2.5" })).toEqual({ replications: "replications" });
    expect(issues({ seed: "9007199254740992" })).toEqual({ seed: "seed" });
    expect(issues({ seed: "-1" })).toEqual({ seed: "seed" });
    expect(issues({ horizon: "", warmup: "", replications: "", seed: "" })).toEqual({ horizon: "required", warmup: "required", replications: "required", seed: "required" });
  });

  it("make two requests with one config: the base version unchanged, and the saved scenario", () => {
    const parsed = parseRunForm(defaultRunForm("minute"), "minute");
    if (!parsed.ok) throw new Error("defaults are valid");
    const { baseline, scenario: run } = runRequests(scenario, parsed.config, "Baseline for it");
    expect(baseline).toEqual({ model_version_id: scenario.base_version_id, name: "Baseline for it", scenario_patch: [], config: parsed.config });
    expect(run).toEqual({ model_version_id: scenario.base_version_id, name: scenario.name, scenario_id: scenario.id, config: parsed.config });
    expect(baseline.config).toBe(run.config);
  });

  it("keep run names within the contract's 200 characters", () => {
    expect(runName("x".repeat(250))).toHaveLength(200);
    expect([...runName("燒".repeat(201))]).toHaveLength(200);
    expect(runName("Line")).toBe("Line");
  });

  it("keep the same keys for a retry of the same request, and new ones for other settings", () => {
    let n = 0;
    const make = () => `key-test-${++n}`;
    const config = { horizon: { value: 1, unit: "day" as const }, warmup: { value: 0, unit: "day" as const }, replications: 5, seed: 1, trace_level: "sampled" as const };
    const first = keysFor(null, scenario.id, config, make);
    expect(first).toMatchObject({ baseline: "key-test-1", scenario: "key-test-2" });
    expect(keysFor(first, scenario.id, { ...config }, make)).toBe(first);
    const other = keysFor(first, scenario.id, { ...config, seed: 2 }, make);
    expect([other.baseline, other.scenario]).toEqual(["key-test-3", "key-test-4"]);
  });
});

const run = (over: Partial<Run>): Run => ({ ...failed, status: "queued", error: null, finished_at: null, started_at: null, cancellation_requested: false, ...over });
const progress = (revision: number, completed = 0, total = 5) => ({ revision, completed_replications: completed, total_replications: total, events_processed: 0 });
const B = "sim_01M4YDQK00FGZZWH0PP15XPNFA";
const S = "sim_01M4YDQK00FGZZWH0PP15XPNFB";

describe("a pair of runs", () => {
  it("is active while a run is queued or running, completed when both are, stopped otherwise", () => {
    const b = run({ id: B, status: "completed" });
    expect([isActive(run({ status: "queued" })), isActive(run({ status: "running" })), isActive(b)]).toEqual([true, true, false]);
    expect(pairPhase(null, null)).toBe("loading");
    expect(pairPhase(b, run({ id: S, status: "running" }))).toBe("active");
    expect(pairPhase(b, run({ id: S, status: "completed" }))).toBe("completed");
    expect(pairPhase(b, run({ id: S, status: "failed" }))).toBe("stopped");
    expect(pairPhase(run({ id: B, status: "cancelled" }), run({ id: S, status: "queued" }))).toBe("active");
  });

  it("keeps the newer of two answers about a run (its progress revision), so a late poll never undoes a cancel", () => {
    const old = run({ id: B, status: "running", progress: progress(4, 2) });
    const cancelled = run({ id: B, status: "running", cancellation_requested: true, progress: progress(5, 2) });
    expect(newer(cancelled, old)).toBe(cancelled);
    expect(newer(old, cancelled)).toBe(cancelled);
    expect(newer(null, old)).toBe(old);
  });

  it("follows polls and cancel answers, and keeps each problem until the next try", () => {
    const empty: PairState = { baseline: null, scenario: null, problem: null, cancelProblem: null, cancelling: false };
    const b1 = run({ id: B, status: "running", progress: progress(3, 1) });
    const s1 = run({ id: S, status: "queued", progress: progress(1) });
    let s = reducePair(empty, { type: "polled", baseline: b1, scenario: s1 });
    expect([s.baseline, s.scenario]).toEqual([b1, s1]);
    s = reducePair(s, { type: "cancelStarted" });
    expect(s.cancelling).toBe(true);
    const sCancelled = run({ id: S, status: "cancelled", progress: progress(2) });
    s = reducePair(s, { type: "cancelAnswered", run: sCancelled });
    expect(s.scenario).toBe(sCancelled);
    // A poll that left before the cancel was answered changes nothing.
    s = reducePair(s, { type: "polled", baseline: b1, scenario: s1 });
    expect(s.scenario).toBe(sCancelled);
    const problem = { kind: "network" as const, code: null, status: null, message: "x", requestId: null, details: [], retryAfterSeconds: null };
    s = reducePair(s, { type: "cancelFailed", problem });
    expect([s.cancelling, s.cancelProblem]).toEqual([false, problem]);
    s = reducePair(s, { type: "cancelStarted" });
    expect(s.cancelProblem).toBeNull();
    s = reducePair(s, { type: "pollFailed", problem });
    expect(s.problem).toBe(problem);
    s = reducePair(s, { type: "retried" });
    expect(s.problem).toBeNull();
  });

  it("shows a failed run's error as the problem the API would have sent", () => {
    const p = runProblem(failed);
    expect(p && [p.kind, p.code, p.status, p.requestId, p.details[0]?.code]).toEqual(["invalid", "MODEL_VALIDATION_FAILED", 422, failed.error!.error.request_id, failed.error!.error.details?.[0]?.code]);
    expect(runProblem(run({}))).toBeNull();
    // A code outside the contract is a protocol problem, never acted on.
    const odd = { ...failed, error: { error: { ...failed.error!.error, code: "NO_SUCH_CODE" as never } } };
    expect(runProblem(odd)?.kind).toBe("protocol");
  });

  it("belongs to the scenario in the address: its scenario run, and a baseline with no patch", () => {
    const b = run({ id: B, scenario_id: null, scenario_patch: [] });
    const s = run({ id: S, scenario_id: scenario.id });
    expect(checkPair(scenario.id, b, s)).toBeNull();
    expect(checkPair(scenario.id, b, run({ id: S, scenario_id: "scn_01M4YDQK00J4MJS82YRG0PCEC2" }))?.kind).toBe("not_found");
    expect(checkPair(scenario.id, run({ id: B, scenario_id: scenario.id }), s)?.kind).toBe("not_found");
    expect(checkPair(scenario.id, run({ id: B, scenario_id: null, scenario_patch: [{ op: "test", path: "/name", value: "x" }] }), s)?.kind).toBe("not_found");
  });
});
