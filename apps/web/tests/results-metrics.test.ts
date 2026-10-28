// T-032 (hackathon slice): the key metrics come from the server's
// comparison (T-029, D-088) as given, never recomputed or invented: a fixed
// set of run-level rows (shown as unavailable when the comparison does not
// have them), then each resource and queue the comparison lists, labelled
// with the model's names. Formatting follows the locale; the exact server
// value stays with every figure.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { Comparison, ModelIR, Result as SimulationResult } from "../lib/contracts";
import { i18nFor } from "../lib/i18n/provider";
import { formatAbsolute, formatRelative, formatValue } from "../features/results/format";
import { keyMetrics, leadTimeSummary, metricKeys } from "../features/results/metrics";
import boardModel from "../features/results/fixtures/board-line-model.json";
import boardComparison from "../features/results/fixtures/board-line-comparison.json";
import boardBaseline from "../features/results/fixtures/board-line-baseline-result.json";
import boardScenario from "../features/results/fixtures/board-line-scenario-result.json";

const fixture = (name: string): unknown => JSON.parse(readFileSync(new URL(`../../../testdata/contracts/${name}`, import.meta.url), "utf8"));
const ir = boardModel as unknown as ModelIR;
const comparison = boardComparison as unknown as Comparison;
const results = { baseline: boardBaseline as unknown as SimulationResult, scenario: boardScenario as unknown as SimulationResult };

describe("the key metrics of a comparison", () => {
  const rows = keyMetrics(comparison, ir, results);

  it("are the run-level rows first, then each resource's utilization, then each queue's mean wait, in the server's order", () => {
    expect(rows.map((r) => [r.group, r.path])).toEqual([
      ["run", "/metrics/throughput/mean"],
      ["run", "/metrics/lead_time_minutes/p50"],
      ["run", "/metrics/lead_time_minutes/p95"],
      ["run", "/metrics/lead_time_minutes/mean"],
      ["run", "/metrics/additional_metrics/replication_mean_lead_time_minutes/mean"],
      ["run", "/metrics/failure_count/mean"],
      ["run", "/metrics/rework_count/mean"],
      ["resources", "/metrics/resources/aoi_station/utilization_mean"],
      ["resources", "/metrics/resources/ict_fixture/utilization_mean"],
      ["resources", "/metrics/resources/packer/utilization_mean"],
      ["resources", "/metrics/resources/smt_line/utilization_mean"],
      ["queues", "/metrics/queues/aoi/wait_mean_minutes"],
      ["queues", "/metrics/queues/pack/wait_mean_minutes"],
      ["queues", "/metrics/queues/q_ict/wait_mean_minutes"],
      ["queues", "/metrics/queues/smt/wait_mean_minutes"],
    ]);
  });

  it("carry the server's values, deltas, status and reason exactly", () => {
    for (const row of rows) {
      const m = comparison.metrics.find((x) => x.metric_path === row.path)!;
      expect([row.baseline, row.scenario, row.absolute, row.relative, row.status, row.reason]).toEqual([m.baseline, m.scenario, m.absolute_delta, m.relative_delta, m.status, m.reason]);
    }
  });

  it("are labelled from the catalog, with the model's names for resources and steps", () => {
    expect(rows.map((r) => r.label)).toEqual([
      { key: "metric.throughput" },
      { key: "metric.leadTimeMedian" },
      { key: "metric.leadTimeP95" },
      { key: "metric.leadTimeMean" },
      { key: "metric.leadTimeReplicationMean" },
      { key: "metric.failures" },
      { key: "metric.rework" },
      { key: "metric.utilization", params: { name: "AOI station" } },
      { key: "metric.utilization", params: { name: "ICT fixture" } },
      { key: "metric.utilization", params: { name: "Packer" } },
      { key: "metric.utilization", params: { name: "SMT line" } },
      { key: "metric.wait", params: { name: "AOI" } },
      { key: "metric.wait", params: { name: "Pack" } },
      { key: "metric.wait", params: { name: "ICT buffer" } },
      { key: "metric.wait", params: { name: "SMT" } },
    ]);
    expect(rows.map((r) => r.unit)).toEqual(["count", "minute", "minute", "minute", "minute", "count", "count", "ratio", "ratio", "ratio", "ratio", "minute", "minute", "minute", "minute"]);
  });

  it("show each run's own 95% interval of a mean where its Result has one (no paired CI, SIMULATION_SPEC §7)", () => {
    const ci = (path: string) => rows.find((r) => r.path === path)!.ci;
    const t = results.baseline.metrics.throughput.confidence_interval_mean!;
    const s = results.scenario.metrics.throughput.confidence_interval_mean!;
    expect(ci("/metrics/throughput/mean")).toEqual({ baseline: { lower: t.lower, upper: t.upper }, scenario: { lower: s.lower, upper: s.upper } });
    expect(ci("/metrics/additional_metrics/replication_mean_lead_time_minutes/mean").baseline).not.toBeNull();
    // A median and a pooled entity mean have no interval.
    expect(ci("/metrics/lead_time_minutes/p50")).toEqual({ baseline: null, scenario: null });
    // Without the Results there is nothing to show, not an invented one.
    expect(keyMetrics(comparison, ir, { baseline: null, scenario: null })[0]!.ci).toEqual({ baseline: null, scenario: null });
  });

  it("show a run-level metric the comparison does not have as missing, with no values", () => {
    const without = { ...comparison, metrics: comparison.metrics.filter((m) => !m.metric_path.startsWith("/metrics/throughput/")) };
    const row = keyMetrics(without, ir, results)[0]!;
    expect([row.path, row.status, row.baseline, row.scenario, row.absolute, row.relative, row.reason]).toEqual(["/metrics/throughput/mean", "missing", null, null, null, null, null]);
    // The optional replication-mean row is left out rather than shown as missing.
    const noAdditional = { ...comparison, metrics: comparison.metrics.filter((m) => !m.metric_path.includes("additional_metrics")) };
    expect(keyMetrics(noAdditional, ir, results).map((r) => r.path)).not.toContain("/metrics/additional_metrics/replication_mean_lead_time_minutes/mean");
  });

  it("list a pack's or DORA metric under its own key, as data, with the server's status", () => {
    const golden = fixture("valid-comparison.json") as Comparison;
    const rs = keyMetrics(golden, null, { baseline: fixture("valid-result.json") as SimulationResult, scenario: fixture("valid-result-scenario.json") as SimulationResult });
    const dora = rs.find((r) => r.path === "/metrics/dora/change_lead_time/mean")!;
    // Its unit is one the UI writes itself (minutes), so the label needs none.
    expect([dora.group, dora.label, dora.unit, dora.status, dora.reason, dora.baseline, dora.absolute]).toEqual(["run", { data: "change_lead_time", unit: null }, "minute", "unavailable", "BOTH_UNAVAILABLE", null, null]);
    // A unit the UI does not know is shown as the pack gave it, next to the name.
    const odd = { ...(fixture("valid-result.json") as SimulationResult) };
    odd.metrics = { ...odd.metrics, domain: { first_pass_yield: { ...odd.metrics.throughput, unit: "ratio_of_boards" } } };
    const withDomain: Comparison = { ...golden, metrics: [...golden.metrics, { metric_path: "/metrics/domain/first_pass_yield/mean", baseline: 0.9, scenario: 0.95, absolute_delta: 0.05, relative_delta: 0.05555555555555555, status: "available", reason: null }] };
    const fpy = keyMetrics(withDomain, null, { baseline: odd, scenario: null }).find((r) => r.path === "/metrics/domain/first_pass_yield/mean")!;
    expect([fpy.label, fpy.unit]).toEqual([{ data: "first_pass_yield", unit: "ratio_of_boards" }, "number"]);
    const failures = rs.find((r) => r.path === "/metrics/failure_count/mean")!;
    expect([failures.absolute, failures.relative, failures.reason]).toEqual([2, null, "BASELINE_ZERO"]);
    // No model: a resource is named by its ID.
    expect(rs.find((r) => r.group === "resources")!.label).toEqual({ key: "metric.utilization", params: { name: "server" } });
  });

  // T-032 review round 1, L4: the electronics line pack's yields and rates
  // (internal/domainpack/electronicsline metricDefs) have the unit "ratio".
  it("write a pack metric whose unit is ratio as a ratio (percent, difference in points), with no unit beside its name", () => {
    const golden = fixture("valid-comparison.json") as Comparison;
    const base = fixture("valid-result.json") as SimulationResult;
    const withScrap = { ...base, metrics: { ...base.metrics, domain: { scrap_rate: { ...base.metrics.throughput, unit: "ratio" }, "fpy_input.ict": { ...base.metrics.throughput, unit: "ratio" } } } };
    const metrics = [
      ...golden.metrics,
      { metric_path: "/metrics/domain/scrap_rate/mean", baseline: 0.04, scenario: 0.025, absolute_delta: -0.015, relative_delta: -0.375, status: "available" as const, reason: null },
      { metric_path: "/metrics/domain/fpy_input.ict/mean", baseline: 0.9, scenario: 0.95, absolute_delta: 0.05, relative_delta: 0.05555555555555555, status: "available" as const, reason: null },
    ];
    const rs = keyMetrics({ ...golden, metrics }, null, { baseline: withScrap, scenario: null });
    const scrap = rs.find((r) => r.path === "/metrics/domain/scrap_rate/mean")!;
    expect([scrap.group, scrap.label, scrap.unit]).toEqual(["run", { data: "scrap_rate", unit: null }, "ratio"]);
    // The server's values, unchanged.
    expect([scrap.baseline, scrap.scenario, scrap.absolute, scrap.relative]).toEqual([0.04, 0.025, -0.015, -0.375]);
    const fpy = rs.find((r) => r.path === "/metrics/domain/fpy_input.ict/mean")!;
    expect([fpy.label, fpy.unit]).toEqual([{ data: "fpy_input.ict", unit: null }, "ratio"]);
  });

  // Live on 2026-10-28 the AI explanation's "What changed" and the step
  // measures showed keys such as scheduled_utilization.ict: a pack metric
  // the catalog names (D-117 (h)) is named, at its station's name.
  it("name a pack metric from the catalog, at the station's name, and keep the key of one the catalog does not have", () => {
    const golden = fixture("valid-comparison.json") as Comparison;
    const base = fixture("valid-result.json") as SimulationResult;
    const at = (path: string) => ({ metric_path: path, baseline: 0.9, scenario: 0.95, absolute_delta: 0.05, relative_delta: 0.05555555555555555, status: "available" as const, reason: null });
    const metrics = [...golden.metrics, at("/metrics/domain/scrap_rate/mean"), at("/metrics/domain/blocking_ratio.ict/mean"), at("/metrics/domain/blocking_ratio.gone/mean"), at("/metrics/domain/made_up.ict/mean"), at("/metrics/additional_metrics/scrap_rate/mean")];
    const line = { domain: "electronics_line", nodes: [{ id: "ict", name: "ICT station 1" }], resources: [] } as unknown as ModelIR;
    const label = (m: ModelIR | null, path: string) => keyMetrics({ ...golden, metrics }, m, { baseline: base, scenario: null }).find((r) => r.path === path)!.label;
    expect(label(line, "/metrics/domain/scrap_rate/mean")).toEqual({ data: "scrap_rate", unit: null, name: { key: "domain.electronics_line.metric.scrap_rate", station: null } });
    expect(label(line, "/metrics/domain/blocking_ratio.ict/mean")).toEqual({ data: "blocking_ratio.ict", unit: null, name: { key: "domain.electronics_line.metric.blocking_ratio", station: "ICT station 1" } });
    // A station the model does not name keeps its node ID.
    expect(label(line, "/metrics/domain/blocking_ratio.gone/mean")).toMatchObject({ name: { station: "gone" } });
    expect(label(line, "/metrics/domain/made_up.ict/mean")).toEqual({ data: "made_up.ict", unit: null });
    expect(label({ ...line, domain: "software_delivery" }, "/metrics/domain/scrap_rate/mean")).toEqual({ data: "scrap_rate", unit: null });
    // A generic group's metric is not the pack's, whatever its key (review L5).
    expect(label(line, "/metrics/additional_metrics/scrap_rate/mean")).toEqual({ data: "scrap_rate", unit: null });
    const t = i18nFor("zh-Hant").t;
    expect(t("metric.atStation", { metric: t("domain.electronics_line.metric.blocking_ratio"), name: "ICT station 1" })).toBe("阻塞比例：ICT station 1");
  });

  it("keep the side-by-side values of runs that are not comparable, with no delta", () => {
    const rs = keyMetrics(fixture("valid-comparison-incomparable.json") as Comparison, null, { baseline: null, scenario: null });
    expect([rs[0]!.status, rs[0]!.reason, rs[0]!.baseline, rs[0]!.scenario, rs[0]!.absolute, rs[0]!.relative]).toEqual(["incomparable", "RUNS_NOT_COMPARABLE", 100, 104.5, null, null]);
  });

  it("read keys as RFC 6901 pointers, so a key with / or ~ keeps its name", () => {
    expect(metricKeys("/metrics/queues/a~1b~0c/wait_mean_minutes")).toEqual(["metrics", "queues", "a/b~c", "wait_mean_minutes"]);
    expect(metricKeys("/metrics/queues/bad~2/wait_mean_minutes")).toBeNull();
    expect(metricKeys("metrics/throughput")).toBeNull();
    const odd: Comparison = { ...comparison, metrics: [{ metric_path: "/metrics/queues/a~1b/wait_mean_minutes", baseline: 1, scenario: 2, absolute_delta: 1, relative_delta: 1, status: "available", reason: null }] };
    expect(keyMetrics(odd, ir, results).find((r) => r.group === "queues")!.label).toEqual({ key: "metric.wait", params: { name: "a/b" } });
  });
});

describe("the spoken summary (DESIGN.md: the median lead time leads)", () => {
  it("is the median lead time, both values and the server's relative change, only when comparable", () => {
    const rows = keyMetrics(comparison, ir, results);
    expect(leadTimeSummary(comparison, rows)).toEqual({ baseline: 29.138691666666666, scenario: 19.766575, relative: -0.3216382112786464 });
    expect(leadTimeSummary({ ...comparison, comparable: false }, rows)).toBeNull();
    const zero = rows.map((r) => (r.path === "/metrics/lead_time_minutes/p50" ? { ...r, relative: null, reason: "BASELINE_ZERO" as const } : r));
    expect(leadTimeSummary(comparison, zero)).toBeNull();
  });
});

describe("numbers in the reader's language (TEST-031), the value itself unchanged", () => {
  const en = i18nFor("en");
  const zh = i18nFor("zh-Hant");

  it("write values with their unit, up to three significant digits or two decimals", () => {
    expect(formatValue(en, 87.2, "count")).toBe("87.2");
    expect(formatValue(en, 29.138691666666666, "minute")).toBe("29.14 min");
    expect(formatValue(zh, 29.138691666666666, "minute")).toBe("29.14 分鐘");
    expect(formatValue(en, 0.49504950495049505, "ratio")).toBe("49.5%");
    expect(formatValue(en, 12345.678, "number")).toBe("12,345.68");
    // A small value is not rounded to zero.
    expect(formatValue(en, 0.0003094456601021152, "minute")).toBe("0.000309 min");
  });

  it("sign every difference, and give a ratio's difference in percentage points", () => {
    expect(formatAbsolute(en, -9.372116666666667, "minute")).toBe("-9.37 min");
    expect(formatAbsolute(en, 2, "count")).toBe("+2");
    expect(formatAbsolute(en, 0, "count")).toBe("0");
    expect(formatAbsolute(en, -0.24752475247524752, "ratio")).toBe("-24.8 pp");
    expect(formatAbsolute(zh, -0.24752475247524752, "ratio")).toBe("-24.8 個百分點");
  });

  it("write the relative change as the server's ratio × 100, signed (SIMULATION_SPEC §7: -0.11 is -11%, never -0.11%)", () => {
    expect(formatRelative(en, 0.1)).toBe("+10%");
    expect(formatRelative(en, -0.3216382112786464)).toBe("-32.2%");
    expect(formatRelative(en, -0.11)).toBe("-11%");
    expect(formatRelative(zh, 0)).toBe("0%");
  });
});
