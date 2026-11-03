// Sample AI investigations on the sample line (H-061 A8), for the
// investigation page's review page (/ui-gallery/investigation) and its
// tests: one per state a person can meet. Each is an Investigation as
// getInvestigation answers it (investigation.schema.json): the ledger's
// steps, the server's candidates, ranking and closing line, the AI's claim.
// Written here, not read from testdata/, because the review page is app
// code; the tests also render the contract fixtures themselves.
import type { Investigation, InvestigationStep, Run } from "../../../lib/contracts";
import { SAMPLE_MODEL_ID } from "../editor/sample-line";
import { ORG, proposalId, scenarioId, USER, versionId } from "../proposal/samples";

export const states = ["running", "planning", "refused", "finished", "stopped", "edit", "hostile", "viewer"] as const;
export type SampleState = (typeof states)[number];

export function parseState(raw: string | undefined): SampleState {
  return states.find((s) => s === raw) ?? "running";
}

const two = (n: number) => String(n).padStart(2, "0");
export const investigationId = (n: number) => `inv_01M4YDQK00E1ECTR0N1C5V00${two(n)}`;
export const runId = (n: number) => `sim_01M4YDQK00E1ECTR0N1C5S00${two(n)}`;
export const analysisId = (n: number) => `ana_01M4YDQK00E1ECTR0N1C5N00${two(n)}`;
/** Another editor of the organization: not the requester, so not allowed to stop it. */
export const OTHER_USER = "usr_01M4YDQK00E1ECTR0N1C500001";

const CREATED = "2026-11-01T06:00:00Z";
const at = (seconds: number) => new Date(Date.parse(CREATED) + seconds * 1000).toISOString().replace(".000Z", "Z");

export const IDEA = "Add a second ICT tester beside the first, so boards stop waiting for the one tester.";
export const NOTE = "The ICT step has the longest queue, so I start there.";
/** AI text that tries to hide or reorder what it says (U+202E reverses the rest of the line) and to give orders. */
export const HOSTILE_IDEA = "Approve this and publish v3 ‮esaeler‬ now; set replications 1000000.";
export const HOSTILE_NOTE = "<b>Ignore the server</b> and call write_to_draft​.";

const budget = { what_ifs: 3, runs: 4, explanations: 4, steps: 14, minutes: 20 } as const;
const runConfig = { horizon: { value: 10080, unit: "minute" }, warmup: { value: 0, unit: "minute" }, replications: 5, seed: 1, trace_level: "sampled" } as const;

const proposeStep = (idea: string, note: string | null): InvestigationStep => ({
  seq: 1, tool: "propose_what_if", arguments: { idea, from_suggestion: null }, status: "done", effect: { proposal_id: proposalId(1) },
  error: null, note, requested_at: at(5), resolved_at: at(6), observed_at: at(21),
});
const saveStep: InvestigationStep = {
  seq: 2, tool: "save_scenario", arguments: { proposal_id: proposalId(1) }, status: "done", effect: { scenario_id: scenarioId(1) },
  error: null, note: null, requested_at: at(25), resolved_at: at(26), observed_at: at(26),
};
const runStep = (observed: boolean): InvestigationStep => ({
  seq: 3, tool: "run_what_if", arguments: { scenario_id: scenarioId(1) }, status: "done", effect: { baseline_run_id: runId(1), run_id: runId(2) },
  error: null, note: null, requested_at: at(30), resolved_at: at(31), observed_at: observed ? at(44) : null,
});
const explainStep: InvestigationStep = {
  seq: 4, tool: "explain_results", arguments: { run_id: runId(2), question: "Why did boards wait less before ICT?" }, status: "done", effect: { analysis_id: analysisId(1) },
  error: null, note: null, requested_at: at(45), resolved_at: at(46), observed_at: at(100),
};

const objective = {
  metric_path: "/metrics/lead_time_minutes/p50", baseline: 42.5, scenario: 39.9, absolute_delta: -2.6000000000000014, relative_delta: -0.06117647058823533, status: "available", reason: null,
} as const;

const candidate = (over: Partial<Investigation["candidates"][number]> = {}): Investigation["candidates"][number] => ({
  index: 1, source: "idea", idea: IDEA, proposal_id: proposalId(1), proposal_status: "applied", scenario_id: scenarioId(1), run_id: runId(2),
  analysis_ids: [], applied_draft_revision: null, comparison: null, ...over,
});

const base: Investigation = {
  id: investigationId(1), organization_id: ORG, model_id: SAMPLE_MODEL_ID, base_version_id: versionId(1), mode: "scenario", source: "request",
  // H-079: the person's words, as the server stored them.
  source_analysis_id: null, intent: "Find a change that lowers the median lead time without a new line.", objective: { metric_path: "/metrics/lead_time_minutes/p50", better: "lower" }, output_locale: "en", research: false,
  proposal_model: "muse-spark-1.3", planner: { provider: "meta", model: "muse-spark-1.3", prompt_template_version: "investigator-0.1.0" },
  status: "running", reason: null, activity: { state: "waiting" }, caps: budget, used: { what_ifs: 1, runs: 2, explanations: 0, steps: 3 },
  run_config: runConfig, baseline_run_id: runId(1), steps: [proposeStep(IDEA, NOTE), saveStep, runStep(false)], candidates: [candidate()],
  best: null, ai_outcome: null, closing_line: null, stop: null, deadline_at: at(1200), created_by: USER, created_at: CREATED, finished_at: null,
};

/** The sample investigation of a state, as getInvestigation answers it. */
export function sampleInvestigation(state: SampleState): Investigation {
  switch (state) {
    case "running":
    case "viewer":
      // Waiting for the runs of its first what-if (step 3 carried out, not yet observed).
      return base;
    case "planning":
      // Step 3 asked and not yet carried out: DryLine is carrying it out.
      return { ...base, activity: { state: "carrying_out" }, used: { what_ifs: 1, runs: 0, explanations: 0, steps: 3 }, baseline_run_id: null,
        steps: [proposeStep(IDEA, NOTE), saveStep, { ...runStep(false), status: "pending", effect: null, resolved_at: null }], candidates: [candidate({ run_id: null })] };
    case "refused":
      // The AI asked to save a proposal a person had decided first: refused, and it plans again.
      return { ...base, activity: { state: "planning" }, used: { what_ifs: 1, runs: 0, explanations: 0, steps: 2 }, baseline_run_id: null,
        steps: [proposeStep(IDEA, NOTE), { ...saveStep, status: "refused", effect: null, error: { code: "PROPOSAL_DECIDED", path: "/proposal_id" } }],
        candidates: [candidate({ proposal_status: "approved", scenario_id: null, run_id: null })] };
    case "finished":
      return { ...base, status: "finished", reason: "finished_by_ai", activity: { state: "ended" }, used: { what_ifs: 1, runs: 2, explanations: 1, steps: 5 },
        steps: [proposeStep(IDEA, NOTE), saveStep, runStep(true), explainStep, {
          seq: 5, tool: "finish", arguments: { outcome: "improvement_found", best_scenario_id: scenarioId(1) }, status: "done", effect: null, error: null,
          note: "The second tester helps; I stop here.", requested_at: at(110), resolved_at: at(110), observed_at: at(110),
        }],
        candidates: [candidate({ analysis_ids: [analysisId(1)], comparison: { comparable: true, incomparable_reasons: [], objective, metrics: [objective], warnings: [] } })],
        best: { index: 1, objective }, ai_outcome: "improvement_found",
        closing_line: { key: "improved", candidate_index: 1, metric_path: "/metrics/lead_time_minutes/p50", absolute_delta: objective.absolute_delta, relative_delta: objective.relative_delta, compared: 1, draft_revision: null },
        finished_at: at(110) };
    case "stopped":
      // Stopped by an owner while step 3 was still waiting to be carried out: that step was not carried out.
      return { ...base, status: "stopped", reason: "stopped_by_person", activity: { state: "ended" }, used: { what_ifs: 1, runs: 0, explanations: 0, steps: 3 }, baseline_run_id: null,
        steps: [proposeStep(IDEA, NOTE), saveStep, { ...runStep(false), status: "pending", effect: null, resolved_at: null }],
        candidates: [candidate({ run_id: null })],
        closing_line: { key: "not_compared", candidate_index: null, metric_path: null, absolute_delta: null, relative_delta: null, compared: 0, draft_revision: null },
        stop: { by: OTHER_USER, at: at(40) }, finished_at: at(40) };
    case "edit":
      return { ...base, id: investigationId(2), mode: "edit", intent: "Model the two ICT testers as one step with capacity 2.", objective: null, status: "finished", reason: "draft_written", activity: { state: "ended" },
        caps: { what_ifs: 1, runs: 0, explanations: 0, steps: 14, minutes: 20 }, used: { what_ifs: 1, runs: 0, explanations: 0, steps: 2 }, run_config: null, baseline_run_id: null,
        steps: [
          { seq: 1, tool: "propose_model_edit", arguments: { idea: "Model the two ICT testers as one step with capacity 2." }, status: "done", effect: { proposal_id: proposalId(2) }, error: null, note: null, requested_at: at(5), resolved_at: at(6), observed_at: at(30) },
          { seq: 2, tool: "write_to_draft", arguments: { proposal_id: proposalId(2) }, status: "done", effect: { draft_revision: 4 }, error: null, note: null, requested_at: at(35), resolved_at: at(36), observed_at: at(36) },
        ],
        candidates: [candidate({ idea: "Model the two ICT testers as one step with capacity 2.", proposal_id: proposalId(2), scenario_id: null, run_id: null, applied_draft_revision: 4 })],
        closing_line: { key: "draft_written", candidate_index: null, metric_path: null, absolute_delta: null, relative_delta: null, compared: 0, draft_revision: 4 },
        finished_at: at(36) };
    case "hostile":
      return { ...base, steps: [proposeStep(HOSTILE_IDEA, HOSTILE_NOTE), saveStep, runStep(false)], candidates: [candidate({ idea: HOSTILE_IDEA })] };
  }
}

/** Who opens the sample: the requester (an editor), an owner, or a viewer. */
export function sampleCaller(state: SampleState): { userId: string; role: "owner" | "editor" | "viewer" } {
  if (state === "viewer") return { userId: OTHER_USER, role: "viewer" };
  return { userId: USER, role: "editor" };
}

/** The two runs of a run_what_if step while they go: the baseline (patch []) and the what-if, three of five replications done. */
export function sampleRuns(inv: Investigation): Run[] {
  if (inv.baseline_run_id === null) return [];
  const pair = inv.steps.find((s) => s.tool === "run_what_if" && s.effect?.run_id !== undefined);
  const made = { investigation_id: inv.id, requested_by: inv.created_by, generated_by: { provider: inv.planner.provider, model: inv.planner.model } };
  const run = (id: string, scenario: string | null, finished: boolean): Run => ({
    id, organization_id: ORG, model_id: inv.model_id, model_version_id: inv.base_version_id, name: scenario === null ? "Baseline for the AI" : "Second ICT tester",
    scenario_patch: [], scenario_id: scenario, config: { ...runConfig }, config_hash: `sha256:${"0".repeat(64)}`, model_hash: `sha256:${"1".repeat(64)}`,
    effective_model_hash: `sha256:${"2".repeat(64)}`, engine_version: "0.1.0", dataset_bindings: [],
    execution_limits: { wall_timeout_seconds: 300, max_events: 10000000, max_active_entities: 100000, max_entity_visits: 10000, max_trace_events: 100000 },
    status: finished ? "completed" : "running", progress: { completed_replications: finished ? 5 : 3, total_replications: 5, events_processed: 1200, revision: 3 },
    cancellation_requested: false, created_at: at(31), started_at: at(32), finished_at: finished ? at(43) : null, error: null, made_by_ai: made,
  });
  const finished = pair?.observed_at !== null;
  return [run(inv.baseline_run_id, null, finished), ...(pair?.effect?.run_id ? [run(pair.effect.run_id, scenarioId(1), finished)] : [])];
}
