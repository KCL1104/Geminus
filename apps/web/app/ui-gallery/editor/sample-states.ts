// The editor preview's sample states (for review and the browser checks).
// "problems": the sample line with the burn-in's largest batch raised above
// its buffer and an X-ray step nothing feeds; its issues are what the
// server's validator (internal/model/validate) answered for exactly this IR
// on 2026-10-26 (checks/evidence/2026-10-26-T-015/preview-problems-ir.json).
// "clean": the sample with a change since version 1, so it can be
// published. "conflict": someone else renames SMT just before the first
// save, which then gets a 409. "viewer": the clean sample, read-only.
// H-072: "motion" is the electronics line template (not the sample line)
// with its baseline run through the engine (one day, seed 1, sampled
// trace), which the model page plays; "still" is the same paused at Day 1,
// 22:00 (StillPlayback, preview.tsx). The other states have no run.
// "requests": the clean sample with the model's earlier requests to the AI
// above Ask AI's composer (2026-11-02; samples.ts earlierRequests).
import type { ModelIR } from "../../../lib/contracts";
import { type ElectronicsLineData, lineBaselineRunOf } from "../../../features/results/fixtures/electronics-line-objects";
import { earlierRequests } from "../proposal/samples";
import { SAMPLE_MODEL_ID, sampleLine } from "./sample-line";
import type { StandInOptions } from "./stand-in";

export const states = ["problems", "clean", "conflict", "viewer", "motion", "still", "requests"] as const;
export type SampleState = (typeof states)[number];

export function parseState(v: string | undefined): SampleState {
  return (states as readonly string[]).includes(v ?? "") ? (v as SampleState) : "problems";
}

function withProblems(): ModelIR {
  const ir = JSON.parse(JSON.stringify(sampleLine)) as ModelIR;
  for (const n of ir.nodes) if (n.id === "burn_in" && n.type === "process" && n.batch) n.batch.max_size = 200;
  ir.nodes.push({ id: "xray", type: "process", name: "X-ray inspection", service_time: { distribution: { type: "constant", value: 2 } }, requires: [] });
  ir.flows.push({ id: "xray_shipped", from: "xray", to: "shipped", routing: { type: "probability", probability: 1 } });
  return ir;
}

/** line: the electronics line's engine output, which "motion" and "still" need (without it they show the sample line, with no run). */
export function standInFor(state: SampleState, line?: ElectronicsLineData): Omit<StandInOptions, "modelId"> {
  switch (state) {
    case "motion":
    case "still": {
      if (!line) return { ir: sampleLine, changed: true };
      const ir: ModelIR = { ...line.model, model_id: SAMPLE_MODEL_ID };
      return { ir, runs: [{ run: lineBaselineRunOf(ir), result: line.results.baseline, trace: line.traces.baseline }] };
    }
    case "problems":
      return {
        ir: withProblems(),
        changed: true,
        issues: [
          {
            node: "burn_in", suffix: "/batch/max_size", code: "BATCH_INVALID", severity: "error",
            message: "the batch's max_size (200) is larger than its buffer's capacity (120): a full batch could never form; raise the capacity or lower max_size",
          },
          { node: "xray", suffix: "", code: "UNREACHABLE_NODE", severity: "warning", message: "no source reaches this node" },
        ],
      };
    case "conflict":
      return { ir: sampleLine, changed: true, otherSave: [{ op: "test", path: "/nodes/3/id", value: "smt" }, { op: "replace", path: "/nodes/3/name", value: "SMT (line 2)" }] };
    case "clean":
    case "viewer":
      return { ir: sampleLine, changed: true };
    case "requests":
      return { ir: sampleLine, changed: true, requests: earlierRequests() };
  }
}
