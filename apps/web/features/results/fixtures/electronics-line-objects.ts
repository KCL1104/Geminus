// The electronics line template's runs, scenario and version (H-072), around
// the engine output in apps/web/tests/fixtures/electronics-line-*.json (how
// they were made: tests/fixtures/electronics-line.ts): the baseline, and a
// scenario with a fourth FCT tester, one day each, seed 1, sampled traces.
// The review pages show them so the line in motion plays on the real
// engine's traces (app/ui-gallery/results and editor). As for the board
// line (board-line-objects.ts), no JSON is imported here: the pages read the
// files at request time and hand them over as data, so a minifier cannot
// rewrite a number. The run, scenario and version objects are made up, as
// the worker and the API would have written them; their IDs are the ones
// the files carry.
import type { Comparison, ModelIR, ModelVersion, Result as SimulationResult, Run, Scenario, TraceArtifact } from "../../../lib/contracts";

/** The engine output for the electronics line: the files' contents, parsed. */
export type ElectronicsLineData = {
  model: ModelIR;
  results: { baseline: SimulationResult; scenario: SimulationResult };
  traces: { baseline: TraceArtifact; scenario: TraceArtifact };
  comparison: Comparison;
};

/** The directory that holds them, from apps/web, and their names. */
export const ELECTRONICS_LINE_DIR = "tests/fixtures";
export const electronicsLineFiles = {
  model: "electronics-line-model.json",
  baselineResult: "electronics-line-baseline-result.json",
  scenarioResult: "electronics-line-scenario-result.json",
  baselineTrace: "electronics-line-baseline-trace.json",
  scenarioTrace: "electronics-line-scenario-trace.json",
  comparison: "electronics-line-comparison.json",
} as const;

/** Day 1, 22:00 of the run: the baseline has boards waiting before FCT, the scenario has none (the still review state). */
export const STILL_AT = 22 * 3_600_000;

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const USR = "usr_01M4YDQK00TA00000000000001";
const sha = (c: string) => `sha256:${c.repeat(64)}`;
const config: Run["config"] = { horizon: { value: 1440, unit: "minute" }, warmup: { value: 0, unit: "minute" }, replications: 5, seed: 1, trace_level: "sampled" };
const patch: Run["scenario_patch"] = [
  { op: "test", path: "/resources/3/id", value: "fct_tester" },
  { op: "replace", path: "/resources/3/capacity", value: 4 },
];

export const LINE_BASELINE_RUN = "sim_01M4YDQK00TA00000000H072BA";
export const LINE_SCENARIO_RUN = "sim_01M4YDQK00TA00000000H072SC";
export const LINE_VERSION = "mv_01M4YDQK00TA0000000000H072";
export const LINE_SCENARIO = "scn_01M4YDQK00TA0000000000H072";

export function lineVersionOf(ir: ModelIR): ModelVersion {
  return {
    id: LINE_VERSION, organization_id: ORG, model_id: ir.model_id, version: 1, schema_version: "0.1", parent_version_id: null,
    ir, hash: sha("5"), dataset_bindings: [], comment: null, created_by: USR, created_at: "2026-11-01T05:00:00Z",
  };
}

export function lineScenarioOf(ir: ModelIR): Scenario {
  return {
    id: LINE_SCENARIO, organization_id: ORG, model_id: ir.model_id, base_version_id: LINE_VERSION,
    name: "Fourth FCT tester", patch, patch_hash: sha("6"), source_proposal_id: null, created_by: USR, created_at: "2026-11-01T05:30:00Z",
  };
}

function run(ir: ModelIR, id: string, name: string, over: Partial<Run>): Run {
  return {
    id, organization_id: ORG, model_id: ir.model_id, model_version_id: LINE_VERSION, name, scenario_patch: [], scenario_id: null, config,
    config_hash: sha("7"), model_hash: sha("5"), effective_model_hash: sha("8"), engine_version: "des-go-0.5.0", dataset_bindings: [],
    execution_limits: { wall_timeout_seconds: 300, max_events: 10_000_000, max_active_entities: 100_000, max_entity_visits: 10_000, max_trace_events: 100_000 },
    status: "completed", progress: { completed_replications: 5, total_replications: 5, events_processed: 31_000, revision: 9 },
    cancellation_requested: false, created_at: "2026-11-01T06:00:00Z", started_at: "2026-11-01T06:00:01Z", finished_at: "2026-11-01T06:00:04Z", error: null,
    ...over,
  };
}

export function lineBaselineRunOf(ir: ModelIR, over: Partial<Run> = {}): Run {
  return run(ir, LINE_BASELINE_RUN, "Baseline for Fourth FCT tester", over);
}

export function lineScenarioRunOf(ir: ModelIR, over: Partial<Run> = {}): Run {
  return run(ir, LINE_SCENARIO_RUN, "Fourth FCT tester", { scenario_patch: patch, scenario_id: LINE_SCENARIO, ...over });
}
