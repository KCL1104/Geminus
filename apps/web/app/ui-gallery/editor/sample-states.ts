// The editor preview's sample states (for review and the browser checks).
// "problems": the sample line with the burn-in's largest batch raised above
// its buffer and an X-ray step nothing feeds; its issues are what the
// server's validator (internal/model/validate) answered for exactly this IR
// on 2026-10-26 (checks/evidence/2026-10-26-T-015/preview-problems-ir.json).
// "clean": the sample with a change since version 1, so it can be
// published. "conflict": someone else renames SMT just before the first
// save, which then gets a 409. "viewer": the clean sample, read-only.
import type { ModelIR } from "../../../lib/contracts";
import { sampleLine } from "./sample-line";
import type { StandInOptions } from "./stand-in";

export const states = ["problems", "clean", "conflict", "viewer"] as const;
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

export function standInFor(state: SampleState): Omit<StandInOptions, "modelId"> {
  switch (state) {
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
  }
}
