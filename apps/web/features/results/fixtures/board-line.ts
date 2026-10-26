// A synthetic board line for the results view's review page (/ui-gallery/
// results) and its tests (T-032). Made-up model; the numbers are not
// invented: the Results, traces and comparison are the output of the real
// engine (des-go-0.5.0, internal/simulation/des) and comparison kernel
// (internal/results/compare) for this model, baseline and a scenario with a
// second ICT fixture: 600 minutes, no warm-up, 5 replications, seed 42,
// sampled trace (checks/T-032_VALIDATION.md has how they were made). The
// runs, scenario and version around them are made up, as the worker and
// the API would have written them.
import type { Comparison, ModelIR, ModelVersion, Result as SimulationResult, Run, Scenario, TraceArtifact } from "../../../lib/contracts";
import baselineResult from "./board-line-baseline-result.json";
import baselineTrace from "./board-line-baseline-trace.json";
import comparison from "./board-line-comparison.json";
import model from "./board-line-model.json";
import scenarioResult from "./board-line-scenario-result.json";
import scenarioTrace from "./board-line-scenario-trace.json";

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const USR = "usr_01M4YDQK00TA00000000000001";
const ir = model as unknown as ModelIR;
const config: Run["config"] = { horizon: { value: 600, unit: "minute" }, warmup: { value: 0, unit: "minute" }, replications: 5, seed: 42, trace_level: "sampled" };
const patch: Run["scenario_patch"] = [
  { op: "test", path: "/resources/2/id", value: "ict_fixture" },
  { op: "replace", path: "/resources/2/capacity", value: 2 },
];
const sha = (c: string) => `sha256:${c.repeat(64)}`;

export const boardVersion: ModelVersion = {
  id: "mv_01M4YDQK00TA0000000000T032", organization_id: ORG, model_id: ir.model_id, version: 3, schema_version: "0.1", parent_version_id: null,
  ir, hash: sha("1"), dataset_bindings: [], comment: null, created_by: USR, created_at: "2026-10-26T08:00:00Z",
};

export const boardScenario: Scenario = {
  id: "scn_01M4YDQK00TA0000000000T032", organization_id: ORG, model_id: ir.model_id, base_version_id: boardVersion.id,
  name: "Second ICT fixture", patch, patch_hash: sha("2"), source_proposal_id: null, created_by: USR, created_at: "2026-10-26T08:30:00Z",
};

function run(id: string, name: string, over: Partial<Run>): Run {
  return {
    id, organization_id: ORG, model_id: ir.model_id, model_version_id: boardVersion.id, name, scenario_patch: [], scenario_id: null, config,
    config_hash: sha("3"), model_hash: boardVersion.hash, effective_model_hash: sha("4"), engine_version: "des-go-0.5.0", dataset_bindings: [],
    execution_limits: { wall_timeout_seconds: 300, max_events: 10_000_000, max_active_entities: 100_000, max_entity_visits: 10_000, max_trace_events: 100_000 },
    status: "completed", progress: { completed_replications: 5, total_replications: 5, events_processed: 6402, revision: 9 },
    cancellation_requested: false, created_at: "2026-10-26T09:00:00Z", started_at: "2026-10-26T09:00:01Z", finished_at: "2026-10-26T09:00:02Z", error: null,
    ...over,
  };
}

export const BASELINE_RUN = "sim_01M4YDQK00TA00000000T032BA";
export const SCENARIO_RUN = "sim_01M4YDQK00TA00000000T032SC";

export function boardBaselineRun(over: Partial<Run> = {}): Run {
  return run(BASELINE_RUN, "Baseline for Second ICT fixture", over);
}

export function boardScenarioRun(over: Partial<Run> = {}): Run {
  return run(SCENARIO_RUN, boardScenario.name, { scenario_patch: patch, scenario_id: boardScenario.id, ...over });
}

export const boardResults = {
  baseline: baselineResult as unknown as SimulationResult,
  scenario: scenarioResult as unknown as SimulationResult,
};
export const boardTraces = {
  baseline: baselineTrace as unknown as TraceArtifact,
  scenario: scenarioTrace as unknown as TraceArtifact,
};
export const boardComparison = comparison as unknown as Comparison;
