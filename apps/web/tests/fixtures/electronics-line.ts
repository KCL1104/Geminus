// The electronics template run through the real engine (H-072), for the
// motion model's tests and, later, the ui-gallery's results and editor
// samples. Nothing in these files is hand-written:
//
// - electronics-line-model.json: electronicsline.Template("electronics_assembly_test_line",
//   "mdl_01M4YDQK00E1ECTR0N1CS00000", "Synthetic electronics assembly and test line", en),
//   i.e. internal/domainpack/electronicsline/templates/electronics_assembly_test_line.json.
// - The baseline is that model; the scenario is it with fct_tester's capacity
//   4 instead of 3 (patch: test /resources/3/id "fct_tester", replace
//   /resources/3/capacity 4).
// - Both ran in a throwaway Go test (deleted, never committed) in
//   internal/domainpack/electronicsline: des.Engine{DomainMetrics:
//   Pack.ComputeMetrics, ModelMetrics: Pack.ComputeModelMetrics}.RunWithTrace
//   (des-go-0.5.0), with horizon 1440 minutes (one day), warm-up 0, 5
//   replications, seed 1, trace_level sampled, and the API's default
//   limits (max_trace_events 100,000; internal/runs/runs.go); then
//   compare.Compare (internal/results/compare) of the two. Run IDs
//   sim_01M4YDQK00TA00000000H072BA / …SC and version mv_01M4YDQK00TA0000000000H072
//   are made up, as in the T-032 fixtures.
// - The Results and the Comparison are the bytes Go wrote; each trace is
//   Go's JSON re-written with one event per line (same values, checked equal).
//   No trimming was needed: every file is under 300 KB with every sampled
//   entity whole (baseline 1,845 events of 87 boards, scenario 1,729 of 87;
//   neither truncated). All six validate against schemas/ (Result,
//   TraceArtifact, Comparison, the IR).
//
// The traces have blocking (aoi, ict, smt), burn-in batches (batch_id) and
// test failures with rework, but no setup_start (no sampled board got the
// SMT changeover in this run): the motion tests cover setups with their own
// events. Tests read the files; a page that ships to the browser must read
// them at run time too, never import the JSON (board-line-objects.ts says why).
import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { Comparison, ModelIR, Result, TraceArtifact } from "../../lib/contracts";

export const electronicsLineFiles = {
  model: "electronics-line-model.json",
  baselineResult: "electronics-line-baseline-result.json",
  scenarioResult: "electronics-line-scenario-result.json",
  baselineTrace: "electronics-line-baseline-trace.json",
  scenarioTrace: "electronics-line-scenario-trace.json",
  comparison: "electronics-line-comparison.json",
} as const;

const read = <T>(name: string): T => JSON.parse(readFileSync(join(import.meta.dirname, name), "utf8")) as T;

export type ElectronicsLineData = {
  model: ModelIR;
  results: { baseline: Result; scenario: Result };
  traces: { baseline: TraceArtifact; scenario: TraceArtifact };
  comparison: Comparison;
};

export function readElectronicsLine(): ElectronicsLineData {
  const f = electronicsLineFiles;
  return {
    model: read(f.model),
    results: { baseline: read(f.baselineResult), scenario: read(f.scenarioResult) },
    traces: { baseline: read(f.baselineTrace), scenario: read(f.scenarioTrace) },
    comparison: read(f.comparison),
  };
}
