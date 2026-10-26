// A synthetic board line for the results view's tests (T-032). Made-up
// model; the numbers are not invented: the Results, traces and comparison
// are the output of the real engine (des-go-0.5.0, internal/simulation/des)
// and comparison kernel (internal/results/compare) for this model, baseline
// and a scenario with a second ICT fixture: 600 minutes, no warm-up, 5
// replications, seed 42, sampled trace (checks/T-032_VALIDATION.md has how
// they were made). The runs, scenario and version around them are made up,
// as the worker and the API would have written them (board-line-objects.ts).
// Tests only: the files are read, never bundled (see board-line-objects.ts).
import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { Run } from "../../../lib/contracts";
import {
  type BoardLineData,
  boardBaselineRunOf,
  boardLineFiles,
  boardScenarioOf,
  boardScenarioRunOf,
  boardVersionOf,
} from "./board-line-objects";

export { BASELINE_RUN, SCENARIO_RUN } from "./board-line-objects";

const read = <T>(name: string): T => JSON.parse(readFileSync(join(import.meta.dirname, name), "utf8")) as T;

export const boardLine: BoardLineData = {
  model: read(boardLineFiles.model),
  results: { baseline: read(boardLineFiles.baselineResult), scenario: read(boardLineFiles.scenarioResult) },
  traces: { baseline: read(boardLineFiles.baselineTrace), scenario: read(boardLineFiles.scenarioTrace) },
  comparison: read(boardLineFiles.comparison),
};

export const boardVersion = boardVersionOf(boardLine.model);
export const boardScenario = boardScenarioOf(boardLine.model);
export const boardResults = boardLine.results;
export const boardTraces = boardLine.traces;
export const boardComparison = boardLine.comparison;
export const boardBaselineRun = (over: Partial<Run> = {}) => boardBaselineRunOf(boardLine.model, over);
export const boardScenarioRun = (over: Partial<Run> = {}) => boardScenarioRunOf(boardLine.model, over);
