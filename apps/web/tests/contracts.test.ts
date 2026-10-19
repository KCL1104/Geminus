// T-003: the generated contract types (lib/contracts, from schemas/) must
// describe the shared fixtures, and must reject shapes the schemas reject.
// Positive cases are typed literals that `tsc` checks and that must equal the
// fixture at runtime; negative cases use @ts-expect-error, so `npm run
// typecheck` fails if a type ever becomes too permissive.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { AnalysisOutput, CancelSimulation, Error, JsonPatch, ModelIR, ProposalOutput } from "../lib/contracts";

const fixture = (name: string): unknown =>
  JSON.parse(readFileSync(new URL(`../../../testdata/contracts/${name}`, import.meta.url), "utf8"));

const minimalIR: ModelIR = {
  schema_version: "0.1",
  model_id: "mdl_01M4YDQK00R942EDGA45T538HS",
  domain: "software_delivery",
  name: "Minimal valid model",
  time_unit: "minute",
  entity_types: [{ id: "change", name: "Change" }],
  resources: [{ id: "worker", type: "human", name: "Worker", capacity: 1, queue_discipline: "fifo" }],
  nodes: [
    {
      id: "start",
      type: "source",
      entity_type: "change",
      arrival: { distribution: { type: "constant", value: 10 }, max_entities: 1 },
    },
    {
      id: "work",
      type: "process",
      service_time: { distribution: { type: "constant", value: 1 } },
      requires: [{ resource_id: "worker", units: 1 }],
    },
    { id: "end", type: "sink" },
  ],
  flows: [
    { id: "start_work", from: "start", to: "work", routing: { type: "probability", probability: 1 } },
    { id: "work_end", from: "work", to: "end", routing: { type: "probability", probability: 1 } },
  ],
  constraints: [],
  metrics: [],
  views: { "2d": {}, "3d": {} },
};

const scenarioPatch: JsonPatch = [
  { op: "test", path: "/resources/1/id", value: "ci_runner" },
  { op: "replace", path: "/resources/1/capacity", value: 6 },
];

const errorBody: Error = {
  error: {
    code: "MODEL_VALIDATION_FAILED",
    message: "Unknown reference",
    details: [{ path: "/flows/0/to", reason: "Unknown node" }],
    request_id: "req_01M4YDQK00V6H8S0ST4F7510KR",
  },
};

// cancelSimulation's body is a closed empty object (T-023).
const cancelBody: CancelSimulation = {};

describe("generated contract types match the shared fixtures", () => {
  it.each([
    ["valid-minimal-ir.json", minimalIR],
    ["valid-patch.json", scenarioPatch],
    ["valid-error.json", errorBody],
    ["valid-cancel-simulation.json", cancelBody],
  ])("%s", (name, typed) => {
    expect(typed).toEqual(fixture(name));
  });
});

// Each line below must be a type error; remove the directive and tsc fails.
export const rejected = {
  // @ts-expect-error unknown member on a closed object
  unknownMember: { ...minimalIR, script: "rm -rf /" } satisfies ModelIR,
  // @ts-expect-error node type outside the schema enum
  badNodeType: { id: "x", type: "lambda" } satisfies ModelIR["nodes"][number],
  // @ts-expect-error JSON Patch move is outside the P0 profile (D-014)
  movePatch: [{ op: "move", from: "/a", path: "/b" }] satisfies JsonPatch,
  // @ts-expect-error error code outside the D-025 catalogue
  unknownCode: { error: { ...errorBody.error, code: "OOPS" } } satisfies Error,
  // @ts-expect-error provider analysis output carries no run IDs (D-026)
  analysisRunId: { run_id: "sim_x", summary: "s" } satisfies NonNullable<AnalysisOutput["baseline"]>,
  // @ts-expect-error provider output never claims approval (D-002)
  providerApproval: { status: "approved" } satisfies Pick<ProposalOutput, "status">,
  // @ts-expect-error a closed empty object takes no member: a cancellation has no options (T-023)
  cancelOption: { force: true } satisfies CancelSimulation,
};
