// T-003: the generated contract types (lib/contracts, from schemas/) must
// describe the shared fixtures, and must reject shapes the schemas reject.
// Positive cases are typed literals that `tsc` checks and that must equal the
// fixture at runtime; negative cases use @ts-expect-error, so `npm run
// typecheck` fails if a type ever becomes too permissive.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type {
  AnalysisOutput,
  AnalysisRequest,
  CancelSimulation,
  Error,
  IdAllocationPool,
  Import,
  JsonPatch,
  MappingSuggestionRequest,
  ModelIR,
  ProfileColumn,
  Proposal,
  ProposalAccepted,
  ProposalOutput,
  ProposalRequest,
  ValidationCode,
} from "../lib/contracts";

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

// An import after profiling (D-043, T-016): raw preview values keyed by the
// source column names (CSV values are strings), and the file's profile.
const profiledImport: Import = {
  id: "imp_01M4YDQK0001HSSAXJ76PRHJ58",
  organization_id: "org_01M4YDQK00Y5H2929M9WWZWTVH",
  project_id: "prj_01M4YDQK00K9HVKK0GQKY0MHSA",
  model_id: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N",
  base_version_id: "mv_01M4YDQK00FRYWV7GS7X1QCXZR",
  kind: "csv_events",
  filename: "synthetic-steps.csv",
  status: "AWAITING_MAPPING",
  mapping_revision: 0,
  total_rows: 2,
  invalid_rows: 0,
  profile: {
    format: "csv",
    encoding: "utf-8-bom",
    rows: 2,
    columns: [
      { name: "案件編號", index: 0, inferred: "string", non_empty: 2, max_length: 7 },
      { name: "階段", index: 1, inferred: "string", non_empty: 2, max_length: 2 },
      { name: "開始時間", index: 2, inferred: "timestamp", non_empty: 2, max_length: 25 },
      { name: "耗時毫秒", index: 3, inferred: "integer", non_empty: 1, max_length: 7 },
      { name: "備註", index: 4, inferred: "empty", non_empty: 0, max_length: 0 },
    ],
  },
  preview: [
    { 案件編號: "CASE-01", 階段: "檢驗", 開始時間: "2026-10-15T08:30:00+08:00", 耗時毫秒: "7200000", 備註: "" },
    { 案件編號: "CASE-02", 階段: "組裝", 開始時間: "2026-10-15T08:31:00+08:00", 耗時毫秒: "", 備註: "" },
  ],
  mapping: null,
  validation: null,
  dataset_version_id: null,
  proposal_id: null,
  created_at: "2026-10-15T08:30:00Z",
  error: null,
  mapping_suggestion_status: "ready",
  mapping_suggestion: {
    mappings: [
      {
        source_column: "案件編號",
        target_field: "case_id",
        confidence: 0.96,
        evidence: "Sample values identify the same case across rows.",
        transform: "identity",
        requires_confirmation: false,
      },
      {
        source_column: "開始時間",
        target_field: "timestamp",
        confidence: 0.7,
        evidence: "RFC 3339 values with an offset; confirm the time zone.",
        transform: "rfc3339",
        requires_confirmation: true,
      },
    ],
  },
};
// T-024: AI requests carry the prose language, which the server validates (REQ-022).
const proposalRequest: ProposalRequest = {
  base_version_id: "mv_01M4YDQK00TA00000000000001",
  mode: "edit",
  intent: "把 ICT 測試機從 2 台增加為 3 台，其他不要動。",
  context: { selected_entity_ids: ["ict_tester"] },
  output_locale: "zh-Hant",
};
// T-025: what createProposal answers, and a proposal the server's review
// refused (its validation only, nothing the provider wrote).
const proposalAccepted: ProposalAccepted = { proposal_id: "aip_01M4YDQK00ADN261CME3WV5HZ7", status: "generating" };
const invalidProposal: Proposal = {
  proposal_id: "aip_01M4YDQK00ADN261CME3WV5HZ8",
  organization_id: "org_01M4YDQK00Y5H2929M9WWZWTVH",
  model_id: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N",
  base_version_id: "mv_01M4YDQK00FRYWV7GS7X1QCXZR",
  mode: "edit",
  status: "invalid",
  created_by: "usr_01M4YDQK0016KTK4JM6KAHZ6BR",
  created_at: "2026-10-20T08:30:00Z",
  requires_confirmation: true,
  validation: {
    valid: false,
    issues: [
      {
        code: "ID_POOL_VIOLATION" satisfies ValidationCode,
        path: "/nodes/9/id",
        message: "a new entity's ID is not one of this proposal's allocated IDs (D-046)",
        severity: "error",
      },
    ],
  },
};
const analysisRequest: AnalysisRequest = {
  baseline_run_id: "sim_01M4YDQK00TA00000000000001",
  question: "Which station is the bottleneck?",
  output_locale: "en",
};
const mappingRequest: MappingSuggestionRequest = { output_locale: "zh-Hant" };
const poolIDs = (kind: string): string[] =>
  Array.from({ length: 16 }, (_, i) => `${kind}_01m4ydqk00ta${String(i + 1).padStart(14, "0")}`);
// D-046: the server's ID allocation pool, 16 IDs of each kind.
const idPool: IdAllocationPool = { node: poolIDs("node"), flow: poolIDs("flow"), resource: poolIDs("resource") };

describe("generated contract types match the shared fixtures", () => {
  it.each([
    ["valid-minimal-ir.json", minimalIR],
    ["valid-patch.json", scenarioPatch],
    ["valid-error.json", errorBody],
    ["valid-cancel-simulation.json", cancelBody],
    ["valid-import.json", profiledImport],
    ["valid-proposal-request-zh-hant.json", proposalRequest],
    ["valid-analysis-request-locale.json", analysisRequest],
    ["valid-mapping-suggestion-request.json", mappingRequest],
    ["valid-id-allocation-pool.json", idPool],
    ["valid-proposal-accepted.json", proposalAccepted],
    ["valid-proposal-invalid.json", invalidProposal],
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
  // @ts-expect-error a profile column type outside D-043's list
  profileType: { name: "a", index: 0, inferred: "date", non_empty: 0, max_length: 0 } satisfies ProfileColumn,
  // @ts-expect-error an import always has a profile member, null until profiling completes (D-043)
  noProfile: { ...profiledImport, profile: undefined } satisfies Import,
  // @ts-expect-error output_locale is one of the two UI languages (REQ-022)
  proposalLocale: { ...proposalRequest, output_locale: "zh-TW" } satisfies ProposalRequest,
  // @ts-expect-error a mapping suggestion request takes no client-supplied columns (T-024)
  mappingColumns: { columns: ["case_id"] } satisfies MappingSuggestionRequest,
  // @ts-expect-error the pool has exactly the node, flow and resource kinds (D-046)
  poolExtraKind: { ...idPool, entity_type: [] } satisfies IdAllocationPool,
  // @ts-expect-error createProposal answers generating, never a later state (T-025)
  acceptedReady: { ...proposalAccepted, status: "ready" } satisfies ProposalAccepted,
  // @ts-expect-error a proposal's status is one of the contract's (T-025)
  proposalStatus: { ...invalidProposal, status: "publishing" } satisfies Proposal,
  // @ts-expect-error an issue code outside the ValidationCode catalogue is not one (T-025)
  unknownIssueCode: "ID_POOL_OVERFLOW" satisfies ValidationCode,
};
