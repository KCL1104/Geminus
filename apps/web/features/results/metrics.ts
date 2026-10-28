// The key metrics of a baseline–scenario comparison (T-032, D-088–D-090):
// rows taken from the server's Comparison as given. Nothing is recomputed:
// the deltas, the status and the reason are the server's. A run-level
// metric the comparison does not have is shown as missing (no value),
// never filled in; resources and queues are the ones the comparison lists.
import type { MessageKey } from "../../lib/i18n/translate";
import { en } from "../../messages/en";
import type { Comparison, ComparisonMetric, ComparisonReason, MetricSummary, ModelIR, Result as SimulationResult } from "../../lib/contracts";
import type { MetricUnit } from "./format";

export type MetricGroup = "run" | "resources" | "queues";
/** A pack's own metric is its key (data); `name` is the catalog's name for it, at its station when it has one (D-117 (h)). */
export type MetricLabel =
  | { key: MessageKey; params?: Record<string, string> }
  | { data: string; unit: string | null; name?: { key: MessageKey; station: string | null } };
export type Interval = { lower: number; upper: number };
export type MetricRow = {
  path: string;
  group: MetricGroup;
  label: MetricLabel;
  unit: MetricUnit;
  baseline: number | null;
  scenario: number | null;
  absolute: number | null;
  relative: number | null;
  status: ComparisonMetric["status"] | "missing";
  reason: ComparisonReason | null;
  /** Each run's own 95% interval of the mean, from its Result (no paired interval, SIMULATION_SPEC §7). */
  ci: { baseline: Interval | null; scenario: Interval | null };
};

type Results = { baseline: SimulationResult | null; scenario: SimulationResult | null };

/** The reference tokens of an RFC 6901 pointer (D-090), or null for one that is not a pointer. */
export function metricKeys(path: string): string[] | null {
  if (!path.startsWith("/")) return null;
  const tokens = path.slice(1).split("/");
  if (tokens.some((t) => /~[^01]|~$/.test(t))) return null;
  return tokens.map((t) => t.replace(/~1/g, "/").replace(/~0/g, "~"));
}

const escape = (key: string) => key.replace(/~/g, "~0").replace(/\//g, "~1");

type Fixed = { path: string; key: MessageKey; optional?: boolean };
const fixed: readonly Fixed[] = [
  { path: "/metrics/throughput/mean", key: "metric.throughput" },
  { path: "/metrics/lead_time_minutes/p50", key: "metric.leadTimeMedian" },
  { path: "/metrics/lead_time_minutes/p95", key: "metric.leadTimeP95" },
  { path: "/metrics/lead_time_minutes/mean", key: "metric.leadTimeMean", optional: true },
  { path: "/metrics/additional_metrics/replication_mean_lead_time_minutes/mean", key: "metric.leadTimeReplicationMean", optional: true },
  { path: "/metrics/failure_count/mean", key: "metric.failures" },
  { path: "/metrics/rework_count/mean", key: "metric.rework" },
];
const fixedPaths = new Set(fixed.map((f) => f.path));

/** The MetricSummary a statistic's path belongs to, in one run's Result. */
function summaryOf(result: SimulationResult | null, keys: string[]): MetricSummary | null {
  if (result === null || keys[0] !== "metrics") return null;
  const m = result.metrics as unknown as Record<string, unknown>;
  const at = (obj: unknown, k: string | undefined): unknown =>
    k !== undefined && typeof obj === "object" && obj !== null && Object.hasOwn(obj, k) ? (obj as Record<string, unknown>)[k] : undefined;
  const group = keys[1];
  const s = group === "dora" || group === "domain" || group === "additional_metrics" ? at(at(m, group), keys[2]) : at(m, group);
  return typeof s === "object" && s !== null && "population" in s ? (s as MetricSummary) : null;
}

// The units the UI writes itself; a pack's other units are shown beside the
// metric's name as given. "ratio" is the electronics line pack's yields and
// rates (T-032 review r1 L4): a percentage, its difference in points.
function unitOf(summary: MetricSummary | null): MetricUnit {
  if (summary?.unit === "minute") return "minute";
  if (summary?.unit === "count") return "count";
  if (summary?.unit === "ratio") return "ratio";
  return "number";
}

function interval(summary: MetricSummary | null, stat: string | undefined): Interval | null {
  const ci = stat === "mean" ? summary?.confidence_interval_mean : null;
  return ci ? { lower: ci.lower, upper: ci.upper } : null;
}

function row(metric: ComparisonMetric | undefined, path: string, group: MetricGroup, label: MetricLabel, unit: MetricUnit, results: Results): MetricRow {
  const keys = metricKeys(path) ?? [];
  const stat = keys[keys.length - 1];
  const ci = { baseline: interval(summaryOf(results.baseline, keys), stat), scenario: interval(summaryOf(results.scenario, keys), stat) };
  if (!metric) return { path, group, label, unit, baseline: null, scenario: null, absolute: null, relative: null, status: "missing", reason: null, ci };
  return {
    path, group, label, unit,
    baseline: metric.baseline, scenario: metric.scenario, absolute: metric.absolute_delta, relative: metric.relative_delta,
    status: metric.status, reason: metric.reason, ci,
  };
}

/** A pack metric's catalog name: a line metric's key is its name, a station's `<name>.<first test's node ID>` (D-117 (d)). */
function packName(ir: ModelIR | null, key: string, nodeName: Map<string, string>): { key: MessageKey; station: string | null } | undefined {
  const dot = key.indexOf(".");
  const name = dot < 0 ? key : key.slice(0, dot);
  const k = `domain.${ir?.domain}.metric.${name}`;
  if (ir === null || !Object.hasOwn(en, k)) return undefined;
  const station = dot < 0 ? null : key.slice(dot + 1);
  return { key: k as MessageKey, station: station === null ? null : (nodeName.get(station) ?? station) };
}

/** The rows to show, in order: the run level, then resources, then queues (each in the server's order). */
export function keyMetrics(comparison: Comparison, ir: ModelIR | null, results: Results): MetricRow[] {
  const byPath = new Map(comparison.metrics.map((m) => [m.metric_path, m]));
  const nodeName = new Map((ir?.nodes ?? []).map((n) => [n.id, n.name ?? n.id]));
  const resourceName = new Map((ir?.resources ?? []).map((r) => [r.id, r.name]));
  const summary = (path: string) => summaryOf(results.baseline, metricKeys(path) ?? []) ?? summaryOf(results.scenario, metricKeys(path) ?? []);

  const run: MetricRow[] = [];
  for (const f of fixed) {
    const m = byPath.get(f.path);
    if (!m && f.optional) continue;
    const unit = f.path.startsWith("/metrics/lead_time_minutes/") || f.path.includes("lead_time_minutes") ? "minute" : "count";
    run.push(row(m, f.path, "run", { key: f.key }, unit, results));
  }
  const resources: MetricRow[] = [];
  const queues: MetricRow[] = [];
  for (const m of comparison.metrics) {
    const keys = metricKeys(m.metric_path);
    if (keys === null || keys[0] !== "metrics" || fixedPaths.has(m.metric_path)) continue;
    const [, group, key, stat] = keys;
    if (key === undefined) continue;
    if (group === "resources" && stat === "utilization_mean") {
      resources.push(row(m, m.metric_path, "resources", { key: "metric.utilization", params: { name: resourceName.get(key) ?? key } }, "ratio", results));
    } else if (group === "queues" && stat === "wait_mean_minutes") {
      queues.push(row(m, m.metric_path, "queues", { key: "metric.wait", params: { name: nodeName.get(key) ?? key } }, "minute", results));
    } else if ((group === "dora" || group === "domain" || group === "additional_metrics") && stat === "mean" && keys.length === 4) {
      const s = summary(m.metric_path);
      const unit = unitOf(s);
      run.push(row(m, m.metric_path, "run", { data: key, unit: unit === "number" ? (s?.unit ?? null) : null, name: group === "domain" ? packName(ir, key, nodeName) : undefined }, unit, results));
    }
  }
  return [...run, ...resources, ...queues];
}

/** The median lead time, both runs and the server's relative change, when the runs compare and all three exist. */
export function leadTimeSummary(comparison: Comparison, rows: MetricRow[]): { baseline: number; scenario: number; relative: number } | null {
  if (!comparison.comparable) return null;
  const r = rows.find((x) => x.path === "/metrics/lead_time_minutes/p50");
  if (!r || r.status !== "available" || r.baseline === null || r.scenario === null || r.relative === null) return null;
  return { baseline: r.baseline, scenario: r.scenario, relative: r.relative };
}

export { escape as escapeMetricKey };
