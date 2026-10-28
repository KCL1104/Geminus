// The state of a scenario's run and its baseline's while they are queued,
// running and done (T-032, T-023): what the server last said about each,
// never edited locally, and only forward. Each answer carries the run's
// progress revision, which the server raises on every visible change, so a
// late poll cannot undo a cancel the server already answered.
import { errorCodeStatus, type Problem, problemFromResponse } from "../../lib/api/problem";
import type { ErrorCode, Run, Scenario } from "../../lib/contracts";

export const isActive = (r: Run): boolean => r.status === "queued" || r.status === "running";

/** The later of two answers about one run. */
export function newer(current: Run | null, next: Run): Run {
  if (current === null || current.id !== next.id) return next;
  return next.progress.revision >= current.progress.revision ? next : current;
}

export type PairPhase = "loading" | "active" | "completed" | "stopped";

export function pairPhase(baseline: Run | null, scenario: Run | null): PairPhase {
  if (baseline === null || scenario === null) return "loading";
  if (isActive(baseline) || isActive(scenario)) return "active";
  return baseline.status === "completed" && scenario.status === "completed" ? "completed" : "stopped";
}

/** A failed run's error, as the problem the API would have answered with (CB-38 envelope). */
export function runProblem(run: Run): Problem | null {
  if (run.error === null) return null;
  const code = run.error.error.code as string;
  const status = Object.hasOwn(errorCodeStatus, code) ? errorCodeStatus[code as ErrorCode] : 500;
  return problemFromResponse(status, new Headers(), run.error);
}

const notThisScenario = (): Problem => ({
  kind: "not_found", code: null, status: null, message: "These runs are not this scenario's run and baseline.", requestId: null, details: [], retryAfterSeconds: null,
});

/** The two runs in the address are this scenario's run and a baseline (patch []), or the page shows nothing of them. */
export function checkPair(scenarioId: string, baseline: Run, scenario: Run): Problem | null {
  if (scenario.scenario_id !== scenarioId) return notThisScenario();
  if (baseline.scenario_id !== null || baseline.scenario_patch.length !== 0) return notThisScenario();
  return null;
}

/** A cancel that did not go through: whose (one run's, or both), and the first failure's problem. */
export type CancelProblem = { side: "baseline" | "scenario" | "both"; problem: Problem };

export type PairState = { baseline: Run | null; scenario: Run | null; problem: Problem | null; cancelProblem: CancelProblem | null; cancelling: boolean };

export type PairEvent =
  | { type: "polled"; baseline: Run; scenario: Run }
  | { type: "pollFailed"; problem: Problem }
  | { type: "retried" }
  | { type: "cancelStarted" }
  | { type: "cancelAnswered"; run: Run }
  /** One run's cancel failed; the other run's goes on (T-032 review r1 M1). */
  | { type: "cancelFailed"; runId: string; problem: Problem }
  | { type: "cancelDone" };

export const initialPair: PairState = { baseline: null, scenario: null, problem: null, cancelProblem: null, cancelling: false };

export function reducePair(s: PairState, e: PairEvent): PairState {
  switch (e.type) {
    case "polled":
      return { ...s, baseline: newer(s.baseline, e.baseline), scenario: newer(s.scenario, e.scenario), problem: null };
    case "pollFailed":
      return { ...s, problem: e.problem };
    case "retried":
      return { ...s, problem: null };
    case "cancelStarted":
      return { ...s, cancelling: true, cancelProblem: null };
    case "cancelAnswered":
      if (s.baseline?.id === e.run.id) return { ...s, baseline: newer(s.baseline, e.run) };
      if (s.scenario?.id === e.run.id) return { ...s, scenario: newer(s.scenario, e.run) };
      return s;
    case "cancelFailed": {
      const side = s.baseline?.id === e.runId ? "baseline" : s.scenario?.id === e.runId ? "scenario" : null;
      if (side === null) return s;
      const first = s.cancelProblem;
      return { ...s, cancelProblem: first === null || first.side === side ? { side, problem: e.problem } : { side: "both", problem: first.problem } };
    }
    case "cancelDone":
      return { ...s, cancelling: false };
  }
}

export type RunPair = { baseline: Run; scenario: Run };

/**
 * The scenario's runs among runs (oldest first), newest first, each with the
 * baseline started beside it (SimulateView queues the baseline first): the
 * latest run created at or before it of the same version, with no scenario,
 * an empty patch and the same config. A run without one is left out.
 */
export function earlierPairs(runs: readonly Run[], scenario: Scenario): RunPair[] {
  const baselines = runs.filter((r) => r.scenario_id === null && r.scenario_patch.length === 0 && r.model_version_id === scenario.base_version_id);
  return runs
    .filter((r) => r.scenario_id === scenario.id)
    .flatMap((s) => {
      const b = baselines.filter((r) => r.config_hash === s.config_hash && Date.parse(r.created_at) <= Date.parse(s.created_at)).at(-1);
      return b === undefined ? [] : [{ baseline: b, scenario: s }];
    })
    .reverse();
}
