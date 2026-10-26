// The synthetic board line's runs, scenario and version (T-032), around
// the engine output in the board-line-*.json files. No JSON is imported
// here: a bundler's minifier may rewrite a number literal to a different
// double (Next.js 16 turned 29.138691666666666 into 29.138691666666663), so
// code that ships to a browser takes the engine output as data parsed at
// run time (app/ui-gallery/results/page.tsx reads the files), never as a
// bundled module. Tests read the files directly (board-line.ts).
import type { Comparison, ModelIR, ModelVersion, Result as SimulationResult, Run, Scenario, TraceArtifact } from "../../../lib/contracts";

/** The engine output for the board line: the files' contents, parsed. */
export type BoardLineData = {
  model: ModelIR;
  results: { baseline: SimulationResult; scenario: SimulationResult };
  traces: { baseline: TraceArtifact; scenario: TraceArtifact };
  comparison: Comparison;
};

/** The files that hold it, in apps/web/features/results/fixtures/. */
export const boardLineFiles = {
  model: "board-line-model.json",
  baselineResult: "board-line-baseline-result.json",
  scenarioResult: "board-line-scenario-result.json",
  baselineTrace: "board-line-baseline-trace.json",
  scenarioTrace: "board-line-scenario-trace.json",
  comparison: "board-line-comparison.json",
} as const;

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const USR = "usr_01M4YDQK00TA00000000000001";
const sha = (c: string) => `sha256:${c.repeat(64)}`;
const config: Run["config"] = { horizon: { value: 600, unit: "minute" }, warmup: { value: 0, unit: "minute" }, replications: 5, seed: 42, trace_level: "sampled" };
const patch: Run["scenario_patch"] = [
  { op: "test", path: "/resources/2/id", value: "ict_fixture" },
  { op: "replace", path: "/resources/2/capacity", value: 2 },
];

export const BASELINE_RUN = "sim_01M4YDQK00TA00000000T032BA";
export const SCENARIO_RUN = "sim_01M4YDQK00TA00000000T032SC";
export const BOARD_VERSION = "mv_01M4YDQK00TA0000000000T032";
export const BOARD_SCENARIO = "scn_01M4YDQK00TA0000000000T032";

export function boardVersionOf(ir: ModelIR): ModelVersion {
  return {
    id: BOARD_VERSION, organization_id: ORG, model_id: ir.model_id, version: 3, schema_version: "0.1", parent_version_id: null,
    ir, hash: sha("1"), dataset_bindings: [], comment: null, created_by: USR, created_at: "2026-10-26T08:00:00Z",
  };
}

export function boardScenarioOf(ir: ModelIR): Scenario {
  return {
    id: BOARD_SCENARIO, organization_id: ORG, model_id: ir.model_id, base_version_id: BOARD_VERSION,
    name: "Second ICT fixture", patch, patch_hash: sha("2"), source_proposal_id: null, created_by: USR, created_at: "2026-10-26T08:30:00Z",
  };
}

function run(ir: ModelIR, id: string, name: string, over: Partial<Run>): Run {
  return {
    id, organization_id: ORG, model_id: ir.model_id, model_version_id: BOARD_VERSION, name, scenario_patch: [], scenario_id: null, config,
    config_hash: sha("3"), model_hash: sha("1"), effective_model_hash: sha("4"), engine_version: "des-go-0.5.0", dataset_bindings: [],
    execution_limits: { wall_timeout_seconds: 300, max_events: 10_000_000, max_active_entities: 100_000, max_entity_visits: 10_000, max_trace_events: 100_000 },
    status: "completed", progress: { completed_replications: 5, total_replications: 5, events_processed: 6402, revision: 9 },
    cancellation_requested: false, created_at: "2026-10-26T09:00:00Z", started_at: "2026-10-26T09:00:01Z", finished_at: "2026-10-26T09:00:02Z", error: null,
    ...over,
  };
}

export function boardBaselineRunOf(ir: ModelIR, over: Partial<Run> = {}): Run {
  return run(ir, BASELINE_RUN, "Baseline for Second ICT fixture", over);
}

export function boardScenarioRunOf(ir: ModelIR, over: Partial<Run> = {}): Run {
  return run(ir, SCENARIO_RUN, "Second ICT fixture", { scenario_patch: patch, scenario_id: BOARD_SCENARIO, ...over });
}
