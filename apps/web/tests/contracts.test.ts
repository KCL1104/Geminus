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
  Approval,
  Approve,
  Calendar,
  CancelSimulation,
  CanonicalEvent,
  ColumnMapping,
  Distribution,
  Error,
  IdAllocationPool,
  Import,
  ImportMapping,
  JsonPatch,
  MappingSuggestionRequest,
  ModelIR,
  ProfileColumn,
  Proposal,
  ProposalAccepted,
  ProposalOutput,
  ProposalRequest,
  Reject,
  ReleaseSchedule,
  SaveMapping,
  TargetField,
  Transform,
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
  canonical_preview: [],
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
// T-026: what approveProposal answers for an edit proposal (the one new
// version, discriminated by mode), its request, a rejection's request, and
// an approved proposal with its decision record (D-068 APPROVED).
const zeroHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000";
const approvalEdit: Approval = {
  proposal_id: "aip_01M4YDQK00ADN261CME3WV5HZ6",
  status: "approved",
  mode: "edit",
  new_version_id: "mv_01M4YDQK00FRYWV7GS7X1QCXZS",
  version: 2,
  patch_hash: zeroHash,
  draft_out_of_date: false,
};
const approveBody: Approve = {
  expected_base_version_id: "mv_01M4YDQK00FRYWV7GS7X1QCXZR",
  comment: "Reviewed the diff, the assumptions and the server's risk.",
};
const rejectBody: Reject = { comment: "Not now: the line is being re-planned." };
const approvedProposal: Proposal = {
  summary: "將 CI runner capacity 從 3 增至 6，其他參數不變。",
  assumptions: ["服務時間分布不變。"],
  risk_level: "low",
  confidence: 0.96,
  changes: [
    { op: "test", path: "/resources/1/id", value: "ci_runner" },
    { op: "replace", path: "/resources/1/capacity", value: 6 },
  ],
  expected_effects: ["CI queue waiting 可能下降；仍需模擬。"],
  requires_confirmation: true,
  proposal_id: "aip_01M4YDQK00ADN261CME3WV5HZ6",
  organization_id: "org_01M4YDQK00Y5H2929M9WWZWTVH",
  model_id: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N",
  base_version_id: "mv_01M4YDQK00FRYWV7GS7X1QCXZR",
  mode: "edit",
  status: "approved",
  created_by: "usr_01M4YDQK0016KTK4JM6KAHZ6BR",
  created_at: "2026-10-15T08:30:00Z",
  validation: { valid: true, issues: [] },
  patch_hash: zeroHash,
  server_risk_level: "low",
  provider_risk_claim: "low",
  dataset_bindings: [],
  new_version_id: "mv_01M4YDQK00FRYWV7GS7X1QCXZS",
  decided_by: "usr_01M4YDQK0016KTK4JM6KAHZ6BS",
  decided_at: "2026-10-20T09:15:00Z",
  decision_comment: "檢閱過差異與風險後批准。",
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
// T-046 phase 2 (D-059): a station operation record with its end, shift and product.
const stationEvent: CanonicalEvent = {
  case_id: "SN-000123",
  event_type: "operation",
  timestamp: "2026-10-13T06:12:00+08:00",
  end_timestamp: "2026-10-13T06:15:10+08:00",
  stage: "fct",
  resource_id: "fct-02",
  outcome: "pass",
  shift: "day",
  product: "board_a",
  duration_ms: 190000,
  metadata: { synthetic: true },
};
// T-017 (D-043 (c), D-070): the mapping a person confirms holds only the
// source column, the target field and one of the four transforms; the D-059
// station fields are target fields; the operator column is not imported.
const stationMapping: ImportMapping = {
  mappings: [
    { source_column: "序號", target_field: "case_id", transform: "identity" },
    { source_column: "紀錄類型", target_field: "event_type", transform: "identity" },
    { source_column: "站別", target_field: "stage", transform: "identity" },
    { source_column: "開始", target_field: "timestamp", transform: "rfc3339" },
    { source_column: "結束", target_field: "end_timestamp", transform: "rfc3339" },
    { source_column: "結果", target_field: "outcome", transform: "identity" },
    { source_column: "班別", target_field: "shift", transform: "identity" },
    { source_column: "機種", target_field: "product", transform: "identity" },
    { source_column: "耗時毫秒", target_field: "duration_ms", transform: "integer_ms" },
    { source_column: "重測", target_field: "is_rework", transform: "boolean_literal" },
    { source_column: "批號", target_field: "metadata", transform: "identity" },
    { source_column: "作業員", target_field: null, transform: "identity" },
  ],
};
// saveImportMapping (D-071): a compare-and-set on the revision, the person's
// confirmation, and the low-confidence suggestions confirmed by name.
const saveMapping: SaveMapping = {
  expected_mapping_revision: 0,
  mapping: stationMapping,
  confirmed: true,
  confirmed_low_confidence: ["開始"],
};
const stationEvents: CanonicalEvent[] = [
  {
    case_id: "SN-000123",
    event_type: "operation",
    timestamp: "2026-10-13T06:12:00+08:00",
    end_timestamp: "2026-10-13T06:15:10+08:00",
    stage: "fct",
  },
  {
    case_id: "SN-000124",
    event_type: "operation",
    timestamp: "2026-10-13T06:16:00+08:00",
    end_timestamp: "2026-10-13T06:19:02+08:00",
    stage: "fct",
  },
];
// A validated import (D-071, D-072): every row maps, and the canonical
// preview holds the preview rows' events.
const validatedImport: Import = {
  ...profiledImport,
  id: "imp_01M4YDQK0001HSSAXJ76PRHJ5A",
  filename: "synthetic-station-log.csv",
  status: "VALIDATED",
  mapping_revision: 2,
  profile: {
    format: "csv",
    encoding: "utf-8",
    rows: 2,
    columns: [
      { name: "序號", index: 0, inferred: "string", non_empty: 2, max_length: 9 },
      { name: "紀錄類型", index: 1, inferred: "string", non_empty: 2, max_length: 9 },
      { name: "站別", index: 2, inferred: "string", non_empty: 2, max_length: 3 },
      { name: "開始", index: 3, inferred: "timestamp", non_empty: 2, max_length: 25 },
      { name: "結束", index: 4, inferred: "timestamp", non_empty: 2, max_length: 25 },
      { name: "作業員", index: 5, inferred: "string", non_empty: 2, max_length: 4 },
    ],
  },
  preview: [
    {
      序號: "SN-000123",
      紀錄類型: "operation",
      站別: "fct",
      開始: "2026-10-13T06:12:00+08:00",
      結束: "2026-10-13T06:15:10+08:00",
      作業員: "op-7",
    },
    {
      序號: "SN-000124",
      紀錄類型: "operation",
      站別: "fct",
      開始: "2026-10-13T06:16:00+08:00",
      結束: "2026-10-13T06:19:02+08:00",
      作業員: "op-7",
    },
  ],
  canonical_preview: stationEvents,
  mapping: {
    mappings: [
      ...stationMapping.mappings.slice(0, 5),
      { source_column: "作業員", target_field: null, transform: "identity" },
    ],
  },
  validation: { valid: true, issues: [] },
  mapping_suggestion_status: "none",
  mapping_suggestion: null,
};
// ADR-026 members are optional parts of the IR types (D-047).
const lineFlowParts = {
  calendar: {
    id: "two_shift",
    period: 1440,
    shifts: [{ start: 360, end: 840, label: "day" }],
    exceptions: [{ start: 7200, end: 8640, open: false }],
  } satisfies Calendar,
  lognormal: { type: "lognormal", mean: 25, cv: 0.8 } satisfies Distribution,
  table: { process: "table", period: 1440, releases: [{ at: 360, quantity: 20 }] } satisfies ReleaseSchedule,
  gated: "PRIMITIVE_NOT_SUPPORTED" satisfies ValidationCode,
};

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
    ["valid-canonical-event-station.json", stationEvent],
    ["valid-approval-edit.json", approvalEdit],
    ["valid-approve.json", approveBody],
    ["valid-reject.json", rejectBody],
    ["valid-proposal-approved.json", approvedProposal],
    ["valid-import-mapping.json", stationMapping],
    ["valid-save-mapping.json", saveMapping],
    ["valid-import-validated.json", validatedImport],
  ])("%s", (name, typed) => {
    expect(typed).toEqual(fixture(name));
  });

  it("valid-line-flow-ir.json carries the ADR-026 members", () => {
    const ir = fixture("valid-line-flow-ir.json") as ModelIR;
    expect(ir.calendars?.[0]?.shifts[0]).toEqual({ start: 360, end: 840, label: "day" });
    expect(ir.resources[1]?.breakdowns?.time_to_repair.distribution).toEqual(lineFlowParts.lognormal);
    expect(lineFlowParts.gated).toBe("PRIMITIVE_NOT_SUPPORTED");
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
  // @ts-expect-error an approval's answer is approved, never another state (T-026)
  approvalStatus: { ...approvalEdit, status: "rejected" } satisfies Approval,
  // @ts-expect-error the approver is the verified caller, never a member of the request (T-026)
  approveApprover: { ...approveBody, approved_by: "usr_01M4YDQK0016KTK4JM6KAHZ6BS" } satisfies Approve,
  // @ts-expect-error a rejection names no base and creates nothing (T-026)
  rejectBase: { expected_base_version_id: "mv_01M4YDQK00FRYWV7GS7X1QCXZR" } satisfies Reject,
  // @ts-expect-error mixtures are still P1 (TWIN_IR_SPEC §3); lognormal and Weibull are P0 (D-056)
  p1Distribution: { type: "mixture", mean: 3 } satisfies Distribution,
  // @ts-expect-error a lognormal is parameterized by mean and cv, not sd (D-056)
  lognormalSd: { type: "lognormal", mean: 3, sd: 1 } satisfies Distribution,
  // @ts-expect-error a release table has releases, a rate schedule has segments (D-053)
  tableSegments: { process: "table", period: 60, segments: [{ start: 0, rate: 1 }] } satisfies ReleaseSchedule,
  // @ts-expect-error a calendar exception says whether the line is open (D-052)
  exceptionOpen: { id: "c", period: 1440, shifts: [{ start: 0, end: 60 }], exceptions: [{ start: 0, end: 60 }] } satisfies Calendar,
  // @ts-expect-error a confirmed mapping carries no AI confidence (D-043 (c))
  mappingConfidence: { source_column: "a", target_field: "case_id", transform: "identity", confidence: 0.9 } satisfies ColumnMapping,
  // @ts-expect-error a transform is one of the four, never an expression (D-009, D-070)
  expressionTransform: "split(',')[0]" satisfies Transform,
  // @ts-expect-error the operator ID is not a canonical field (ELECTRONICS_LINE §6)
  operatorTarget: "operator_id" satisfies TargetField,
  // @ts-expect-error saving a mapping is the person's confirmation (D-071)
  unconfirmedSave: { ...saveMapping, confirmed: false } satisfies SaveMapping,
  // @ts-expect-error an import's status is one of the contract's (T-017 adds VALIDATING only)
  importStatus: { ...validatedImport, status: "MAPPING" } satisfies Import,
};
