// The electronics line template beside the demo's "Fourth FCT tester"
// what-if, as the server compares them (H-073 slice 3: the line-constraint
// card's tests). Not invented: the Results are two real engine runs
// (des-go-0.5.0 with the electronics line pack's metrics) of the template,
// 28 days with 7 of warm-up, 5 replications, seed 2026, and the comparison is
// the comparison kernel's answer for them (internal/results/compare); the
// baseline's result digest is the template's regression lock (internal/domainpack/electronicsline/testdata/
// template-run-lock.json). The generator and the schema check are in
// checks/evidence/2026-11-01-H-073-s3/; it was run again on 2026-11-02 when
// the template's 2D positions moved (the model and the two Results' hashes
// changed, the comparison did not). Synthetic model, English names.
// Tests only: the files are read, never bundled (see board-line-objects.ts).
import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { Comparison, ModelIR, Result as SimulationResult } from "../../../lib/contracts";

const read = <T>(name: string): T => JSON.parse(readFileSync(join(import.meta.dirname, name), "utf8")) as T;

export const electronicsComparison: Comparison = read("electronics-line-comparison.json");
export const electronicsModel: ModelIR = read("electronics-line-model.json");
export const electronicsResults: { baseline: SimulationResult; scenario: SimulationResult } = {
  baseline: read("electronics-line-baseline-result.json"),
  scenario: read("electronics-line-scenario-result.json"),
};
