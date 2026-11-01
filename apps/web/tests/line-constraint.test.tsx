// @vitest-environment happy-dom
// H-073 (3): the line-constraint card, built only from the server's
// comparison rows. It names the station ELECTRONICS_LINE §5's rule picks
// (the highest work ratio on shift in the baseline, a tie to the smaller
// node ID: "a common reading, not a proof"), gives that station's figures in
// both runs and the largest blocked-ratio change the server computed, states
// its rule, claims no cause, and shows nothing for a pack without the
// rule's measure. The figures are real engine output (fixtures/electronics-line.ts).
import { describe, expect, it } from "vitest";
import type { Comparison, ComparisonMetric } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { lineConstraint, RULE_METRIC } from "../features/results/constraint";
import { LineConstraint } from "../features/results/line-constraint";
import { keyMetrics } from "../features/results/metrics";
import { boardComparison, boardVersion } from "../features/results/fixtures/board-line";
import { electronicsComparison, electronicsModel, electronicsResults } from "../features/results/fixtures/electronics-line";
import { mount, violations } from "./ui-harness";

const catalogs = { en, "zh-Hant": zhHant } as const;
const rowsOf = (c: Comparison = electronicsComparison) => keyMetrics(c, electronicsModel, electronicsResults);
const text = (el: Element) => (el.textContent ?? "").replace(/\s+/g, " ").trim();
const at = (path: string) => electronicsComparison.metrics.find((m) => m.metric_path === path)!;
/** The fixture's comparison with some of its rows replaced (a test's input; the rest stays the server's). */
const withRows = (over: Record<string, Partial<ComparisonMetric>>, base: Comparison = electronicsComparison): Comparison => ({
  ...base,
  metrics: base.metrics.map((m) => (Object.hasOwn(over, m.metric_path) ? { ...m, ...over[m.metric_path] } : m)),
});
const work = (station: string) => `/metrics/domain/work_ratio.${station}/mean`;

describe("the line's constraint by the stated rule (pure)", () => {
  it("is the baseline's station with the highest work ratio, with the server's figures for it", () => {
    expect(RULE_METRIC).toBe("work_ratio");
    const c = lineConstraint(rowsOf());
    expect(c.state).toBe("named");
    if (c.state !== "named") return;
    expect(c.at.station).toBe("fct");
    expect(c.at.row.label).toMatchObject({ name: { key: "domain.electronics_line.metric.work_ratio", station: "FCT" } });
    // Exactly the server's values, not recomputed.
    expect([c.at.row.baseline, c.at.row.scenario]).toEqual([at(work("fct")).baseline, at(work("fct")).scenario]);
    expect(c.scheduled?.path).toBe("/metrics/domain/scheduled_utilization.fct/mean");
    expect([c.scheduled?.baseline, c.scheduled?.scenario]).toEqual([0.896777356095679, 0.6734487930555556]);
    // The largest blocked-ratio change in either direction is AOI's (the server's Δ).
    expect(c.blocked.kind).toBe("changed");
    if (c.blocked.kind !== "changed") return;
    expect(c.blocked.at.station).toBe("aoi");
    expect(c.blocked.at.row.absolute).toBe(at("/metrics/domain/blocking_ratio.aoi/mean").absolute_delta);
  });

  it("is not moved by a station that is busy only because it is blocked (scheduled utilization counts blocked time)", () => {
    // AOI holds boards it cannot pass on 42% of the time: its utilization is high, its work ratio is not.
    const c = lineConstraint(rowsOf(withRows({ "/metrics/domain/scheduled_utilization.aoi/mean": { baseline: 0.99 } })));
    expect(c.state === "named" && c.at.station).toBe("fct");
  });

  it("gives a tie to the station whose ID sorts first, and reads the baseline only", () => {
    const fct = at(work("fct")).baseline;
    expect(lineConstraint(rowsOf(withRows({ [work("smt")]: { baseline: fct } })))).toMatchObject({ at: { station: "fct" } });
    expect(lineConstraint(rowsOf(withRows({ [work("aoi")]: { baseline: fct } })))).toMatchObject({ at: { station: "aoi" } });
    // A scenario value never names the constraint.
    expect(lineConstraint(rowsOf(withRows({ [work("ict")]: { scenario: 0.99 } })))).toMatchObject({ at: { station: "fct" } });
  });

  it("is unavailable when no station has a work ratio in the baseline, and absent for a pack without the measure", () => {
    const none = Object.fromEntries(
      electronicsComparison.metrics.filter((m) => m.metric_path.startsWith("/metrics/domain/work_ratio.")).map((m) => [m.metric_path, { baseline: null, absolute_delta: null, relative_delta: null, status: "unavailable" as const, reason: "BASELINE_UNAVAILABLE" as const }]),
    );
    expect(lineConstraint(rowsOf(withRows(none)))).toEqual({ state: "unavailable" });
    // The board line (the Software Delivery example pack) reports no work ratio.
    expect(lineConstraint(keyMetrics(boardComparison, boardVersion.ir, { baseline: null, scenario: null }))).toEqual({ state: "none" });
  });

  it("says when the blocked ratio did not change or the server gave no difference", () => {
    const blocked = electronicsComparison.metrics.filter((m) => m.metric_path.startsWith("/metrics/domain/blocking_ratio."));
    const zero = Object.fromEntries(blocked.map((m) => [m.metric_path, { absolute_delta: 0, relative_delta: null }]));
    expect(lineConstraint(rowsOf(withRows(zero)))).toMatchObject({ blocked: { kind: "unchanged" } });
    // Runs that cannot be compared have no Δ (D-088): no change is claimed.
    const incomparable: Comparison = {
      ...electronicsComparison,
      comparable: false,
      incomparable_reasons: ["SEED_MISMATCH"],
      metrics: electronicsComparison.metrics.map((m) => ({ ...m, absolute_delta: null, relative_delta: null, status: "incomparable" as const, reason: "RUNS_NOT_COMPARABLE" as const })),
    };
    expect(lineConstraint(rowsOf(incomparable))).toMatchObject({ state: "named", at: { station: "fct" }, blocked: { kind: "unavailable" } });
  });

  it("leaves out scheduled utilization for a station without a calendar (the pack does not list it)", () => {
    const burnIn = at(work("burn_in")).baseline!;
    const c = lineConstraint(rowsOf(withRows({ [work("burn_in")]: { baseline: burnIn + 1 } })));
    expect(c).toMatchObject({ state: "named", at: { station: "burn_in" }, scheduled: null });
  });
});

describe("the line-constraint card", () => {
  it.each(["en", "zh-Hant"] as Locale[])("names the station, its figures, the largest blocked-ratio change and its rule (%s)", async (locale) => {
    const m = catalogs[locale];
    const el = await mount(<LineConstraint rows={rowsOf()} />, locale);
    const card = el.querySelector("section")!;
    expect(card.querySelector("h2")?.textContent).toBe(m["constraint.title"]);
    const named = m["constraint.named"].replace("{station}", "FCT");
    expect(text(card)).toContain(named);
    expect(card.querySelector("bdi")?.textContent).toBe("FCT");
    // Each figure is the server's exact value in <data value>, baseline before scenario.
    const values = [...card.querySelectorAll("data")].map((d) => d.getAttribute("value"));
    const figures = (path: string) => { const r = at(path); return [r.baseline, r.scenario, r.absolute_delta].map(String); };
    expect(values).toEqual([...figures(work("fct")), ...figures("/metrics/domain/scheduled_utilization.fct/mean"), ...figures("/metrics/domain/blocking_ratio.aoi/mean")]);
    const pp = (v: string) => (locale === "en" ? `\u2212${v} pp` : `\u2212${v} 個百分點`);
    const pair = (b: string, s: string, d: string) => m["constraint.pair"].replace("{baseline}", b).replace("{scenario}", s).replace("{delta}", d);
    expect(text(card)).toContain(pair("89.7%", "67.3%", pp("22.3")));
    expect(text(card)).toContain(m["constraint.blockedAt"].replace("{station}", "AOI").replace("{baseline}", "41.8%").replace("{scenario}", "10.6%").replace("{delta}", pp("31.2")));
    // The scenario figure is the scenario's (green at 600, after the baseline and the arrow: Two Inks).
    expect(card.querySelectorAll("[data-run=scenario]").length).toBe(3);
    // The utilization line says what it counts; the rule is stated, and no cause is claimed.
    expect(text(card)).toContain(m["constraint.counts"].replace("{metric}", m["domain.electronics_line.metric.scheduled_utilization"]));
    expect(text(card.querySelector("p:last-child")!)).toBe(m["constraint.rule"]);
    expect(text(card)).not.toMatch(/because|cause[sd]? by|due to|因為|導致/i);
    expect(await violations()).toEqual([]);
  });

  it("shows nothing for a pack without the rule's measure", async () => {
    const el = await mount(<LineConstraint rows={keyMetrics(boardComparison, boardVersion.ir, { baseline: null, scenario: null })} />);
    expect(el.innerHTML).toBe("");
  });

  it("says unavailable, with the rule, when no station has a work ratio in the baseline", async () => {
    const none = Object.fromEntries(
      electronicsComparison.metrics.filter((m) => m.metric_path.startsWith("/metrics/domain/work_ratio.")).map((m) => [m.metric_path, { baseline: null }]),
    );
    const el = await mount(<LineConstraint rows={rowsOf(withRows(none))} />);
    expect(text(el)).toContain(en["constraint.unavailable"]);
    expect(text(el)).toContain(en["constraint.rule"]);
    expect(el.querySelectorAll("data").length).toBe(0);
    expect(await violations()).toEqual([]);
  });

  it("says when the server gave no blocked-ratio difference, and when none changed", async () => {
    const blocked = electronicsComparison.metrics.filter((m) => m.metric_path.startsWith("/metrics/domain/blocking_ratio."));
    const el = await mount(<LineConstraint rows={rowsOf(withRows(Object.fromEntries(blocked.map((m) => [m.metric_path, { absolute_delta: null }]))))} />);
    expect(text(el)).toContain(en["constraint.blockedUnavailable"]);
    const zero = await mount(<LineConstraint rows={rowsOf(withRows(Object.fromEntries(blocked.map((m) => [m.metric_path, { absolute_delta: 0 }]))))} />);
    expect(text(zero)).toContain(en["constraint.blockedUnchanged"]);
  });
});

describe("the utilization label (H-073)", () => {
  it("says it counts blocked and changeover time, in both languages", () => {
    expect(en["metric.utilization"]).toBe("Utilization (counts blocked and changeover time): {name}");
    expect(zhHant["metric.utilization"]).toBe("使用率（含阻塞與換線時間）：{name}");
  });
});
