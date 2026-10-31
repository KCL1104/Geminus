// H-061 A8 (PROPOSED): what an investigation is started with. The run
// settings of every run the AI starts are the person's, frozen at creation
// and within H-060's caps (at most 10 replications and 28 days, in the
// model's time unit; trace sampled for the Marey chart); the form says so at
// the field before anything is sent, and the server checks it again (422 at
// /run_config/…). The bodies carry only what was chosen: research, a model
// or a planner other than the default each add their member, so a request
// on the defaults is the contract's minimal one. Pure, so every rule is
// testable without React.
import type {
  Duration,
  InvestigationFromAnalysis,
  InvestigationFromRequest,
  InvestigationObjectiveMetric,
  InvestigationRunConfig,
  Locale,
  ModelTimeUnit,
  PlannerModel,
  ProposalModel,
  Run,
} from "../../lib/contracts";
import { DEFAULT_MODEL } from "../ai-proposals/proposal-models";
import { type FormIssue, parseRunForm, type RunField, type RunForm } from "../simulations/run-config";
import { plannerField } from "./flags";

/** The proposal model's field (H-049): none for the default, as model-choice.tsx's modelField. */
const modelField = (model: ProposalModel): { model?: ProposalModel } => (model === DEFAULT_MODEL ? {} : { model });

/** H-060's caps on the AI's runs. */
export const MAX_REPLICATIONS = 10;
export const MAX_DAYS = 28;

/** 28 days in each time unit. */
const maxHorizon: Readonly<Record<ModelTimeUnit, number>> = { day: MAX_DAYS, hour: MAX_DAYS * 24, minute: MAX_DAYS * 1440, second: MAX_DAYS * 86400 };

export type ActIssue = FormIssue | "aiReplications" | "aiHorizon";

/** The run form's settings, refused at the field when they are not values or are over the caps. */
export function parseActRunForm(form: RunForm, unit: ModelTimeUnit): { ok: true; config: InvestigationRunConfig } | { ok: false; issues: Partial<Record<RunField, ActIssue>> } {
  const parsed = parseRunForm(form, unit);
  const issues: Partial<Record<RunField, ActIssue>> = parsed.ok ? {} : { ...parsed.issues };
  const replications = Number(form.replications.trim());
  if (issues.replications === undefined && replications > MAX_REPLICATIONS) issues.replications = "aiReplications";
  // parseRunForm allows up to 1000 replications; over 10 is the AI's cap, said as such.
  if (issues.replications === "replications" && /^\d+$/.test(form.replications.trim()) && replications > MAX_REPLICATIONS) issues.replications = "aiReplications";
  const horizon = Number(form.horizon.trim());
  if (issues.horizon === undefined && horizon > maxHorizon[unit]) issues.horizon = "aiHorizon";
  if (Object.keys(issues).length > 0 || !parsed.ok) return { ok: false, issues };
  return { ok: true, config: { ...parsed.config, trace_level: "sampled" } };
}

/** The metrics an investigation may aim at, the default first (lower is better for lead time, higher for throughput: the server's). */
export const OBJECTIVES: readonly InvestigationObjectiveMetric[] = ["/metrics/lead_time_minutes/p50", "/metrics/lead_time_minutes/mean", "/metrics/lead_time_minutes/p95", "/metrics/throughput/mean"];

/** Entry A: from the person's words on the model's latest version. Edit mode carries no run settings, baseline or objective. */
export function fromRequest(a: {
  base: string;
  mode: "scenario" | "edit";
  intent: string;
  locale: Locale;
  research: boolean;
  model: ProposalModel;
  planner: PlannerModel;
  /** The parsed run settings; required in scenario mode, ignored in edit mode. */
  config: InvestigationRunConfig | null;
  baselineName: string;
  objective: InvestigationObjectiveMetric;
}): InvestigationFromRequest {
  const common = { base_version_id: a.base, mode: a.mode, intent: a.intent, output_locale: a.locale, ...(a.research ? { research: true } : {}), ...modelField(a.model), ...plannerField(a.planner) };
  if (a.mode === "edit") return common;
  if (a.config === null) throw new Error("a scenario investigation needs its run settings");
  return { ...common, run_config: a.config, baseline_name: a.baselineName, objective: { metric_path: a.objective } };
}

/** Entry B: from a completed analysis, whose baseline is reused and whose suggestions seed the AI. */
export function fromAnalysis(a: { analysis: string; locale: Locale; model: ProposalModel; planner: PlannerModel }): InvestigationFromAnalysis {
  return { from_analysis_id: a.analysis, output_locale: a.locale, ...modelField(a.model), ...plannerField(a.planner) };
}

/** A run's duration in days (a run's config may name any TimeUnit, milliseconds included). */
const perDay: Readonly<Record<Duration["unit"], number>> = { day: 1, hour: 24, minute: 1440, second: 86400, millisecond: 86_400_000 };
const days = (d: Duration) => d.value / perDay[d.unit];

/**
 * Whether an analysis's baseline run can be the investigation's baseline
 * (entry B): it changes nothing (no scenario, patch []) and its settings
 * fit the caps. The server decides (422 at /from_analysis_id); this only
 * says why the page cannot offer the start.
 */
export function baselineFits(baseline: Run): "fits" | "changed" | "limits" {
  if (baseline.scenario_id !== null || baseline.scenario_patch.length !== 0) return "changed";
  const c = baseline.config;
  if (c.replications > MAX_REPLICATIONS || days(c.horizon) > MAX_DAYS || (c.trace_level !== "none" && c.trace_level !== "sampled")) return "limits";
  return "fits";
}
