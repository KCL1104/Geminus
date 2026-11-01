// @vitest-environment happy-dom
// H-073 slice 2, "Which what-if wins?": the model's Scenarios page ranks
// each scenario by the server's comparison of its latest completed run with
// its baseline (the median lead time's change), best first, every figure
// the server's and each name one click from its results; the Runs page
// opens a what-if run's results instead of its run form. The reads go out
// together and are capped, and the page says so when a cap cut them.
import { act, type ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { ScenariosPage, RunsPage } from "../features/models/model-lists";
import { MAX_RUN_PAGES } from "../features/simulations/load";
import { MAX_COMPARISONS, rankScenarios, sharedWarnings, standingOf, warningsOf } from "../features/simulations/ranking";
import { baselinesOf, earlierPairs, latestCompleted } from "../features/simulations/run-pair";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { Comparison, ComparisonMetric, ComparisonWarning, Run, Scenario } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { boardBaselineRun, boardComparison, boardScenario, boardScenarioRun, boardVersion } from "../features/results/fixtures/board-line";
import { mount, press, settle, violations } from "./ui-harness";

vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), usePathname: () => "/models/x/scenarios", useRouter: () => ({ refresh: () => {}, push: () => {} }) }));
vi.setConfig({ testTimeout: 30_000 });

const ORG = boardScenario.organization_id;
const MODEL = boardScenario.model_id;
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const P50 = "/metrics/lead_time_minutes/p50";
const v1 = "/api/v1";
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQ } });
const problem = (status: number, code: string) => json(status, { error: { code, message: "x", request_id: REQ } });
const page = <T,>(items: T[], next: string | null = null) => json(200, { items, next_cursor: next });

// IDs the client accepts: a prefix and 26 Crockford base32 characters.
const id = (prefix: string, n: number) => `${prefix}_01M4YDQK00TA0000000H073${String(n).padStart(3, "0")}`;
const at = (minute: number) => new Date(Date.UTC(2026, 10, 1, 8, minute)).toISOString();
const scenario = (n: number, name: string, over: Partial<Scenario> = {}): Scenario => ({ ...boardScenario, id: id("scn", n), name, ...over });
const baseline = (n: number, minute: number, over: Partial<Run> = {}) => boardBaselineRun({ id: id("sim", n), created_at: at(minute), ...over });
const whatIf = (n: number, s: Scenario, minute: number, over: Partial<Run> = {}) => boardScenarioRun({ id: id("sim", n), scenario_id: s.id, name: s.name, created_at: at(minute), ...over });
const p50 = (over: Partial<ComparisonMetric>): ComparisonMetric => ({ metric_path: P50, baseline: 30, scenario: 27, absolute_delta: -3, relative_delta: -0.1, status: "available", reason: null, ...over });
const comparison = (b: Run, s: Run, metric: ComparisonMetric | null, over: Partial<Comparison> = {}): Comparison => ({
  ...boardComparison, baseline_run_id: b.id, scenario_run_id: s.id, metrics: metric === null ? [] : [metric], ...over,
});
const model = { id: MODEL, organization_id: ORG, project_id: "prj_01M4YDQK00TA00000000000001", domain: "electronics_line", name: "Board line",
  draft: { revision: 1, base_version_id: boardVersion.id, out_of_date: false }, latest_version: { id: boardVersion.id, version: boardVersion.version }, created_at: "2026-10-29T08:00:00Z" };

type Answer = Response | Promise<Response> | ((url: URL) => Response | Promise<Response>);
/** The app's client over a fetch that answers by "METHOD path" (queued answers in order, the last one kept), recording every request. */
function fakeApi(table: Record<string, Answer | Answer[]>) {
  const calls: URL[] = [];
  const queues = new Map(Object.entries(table).map(([k, v]) => [k, Array.isArray(v) ? [...v] : [v]]));
  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://app.test");
    calls.push(url);
    const q = queues.get(`${init?.method ?? "GET"} ${url.pathname}`);
    if (!q || q.length === 0) throw new Error(`no fake answer for ${url.pathname}`);
    const next = q.length > 1 ? q.shift()! : q[0]!;
    const res = await (typeof next === "function" ? next(url) : next);
    return res.clone();
  }) as typeof globalThis.fetch;
  return { calls, client: createApiClient({ session: () => ({ organizationId: ORG }), fetch }) };
}

/** The answers for a model whose one version holds scenarios and whose runs are runs (oldest first, one page). */
function modelAnswers(scenarios: Scenario[], runs: Run[], comparisons: Comparison[]) {
  return {
    [`GET ${v1}/models/${MODEL}`]: json(200, model),
    [`GET ${v1}/models/${MODEL}/versions`]: page([boardVersion]),
    [`GET ${v1}/model-versions/${boardVersion.id}/scenarios`]: page(scenarios),
    [`GET ${v1}/models/${MODEL}/simulations`]: page(runs),
    [`GET ${v1}/simulation-comparisons`]: (url: URL) => {
      const c = comparisons.find((x) => x.baseline_run_id === url.searchParams.get("baseline_run_id") && x.scenario_run_id === url.searchParams.get("scenario_run_id"));
      return c ? json(200, c) : problem(404, "NOT_FOUND");
    },
  };
}

async function waitFor(check: () => boolean, what: string, ms = 10_000): Promise<void> {
  const until = Date.now() + ms;
  while (!check()) {
    if (Date.now() > until) throw new Error(`timed out waiting for ${what}`);
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
}

const text = (el: Element | null | undefined) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();
const rows = (el: Element) => [...el.querySelectorAll("tbody tr")];
const loaded = (el: Element) => el.querySelector("table, [role=alert]") !== null || text(el).includes(en["scenarios.empty"]) || text(el).includes(zhHant["scenarios.empty"]);

async function show(api: ReturnType<typeof fakeApi>, node: ReactNode, locale: Locale = "en") {
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: ORG }, role: "viewer" }}>
      <ApiProvider client={api.client}>{node}</ApiProvider>
    </AuthProvider>,
    locale,
  );
  await settle();
  return el;
}

async function scenariosPage(api: ReturnType<typeof fakeApi>, locale: Locale = "en") {
  const el = await show(api, <ScenariosPage modelId={MODEL} />, locale);
  await waitFor(() => loaded(el), "the ranking");
  return el;
}

const notes = (el: Element) => [...el.querySelectorAll("[role=note]")].map(text);
const shared = (locale: Locale, w: ComparisonWarning) => {
  const c = locale === "en" ? en : zhHant;
  return c["scenarios.ranking.shared"].replace("{warning}", c[`compare.warning.${w}`]);
};
const results = (s: Scenario, b: Run, r: Run) => `/scenarios/${s.id}/results?baseline=${b.id}&scenario=${r.id}`;
/** A row as [rank, the name's link, the figures' exact server values (data value), the row's words]. */
const shape = (tr: Element) => [
  text(tr.querySelector("td[data-rank]")),
  tr.querySelector("th a")?.getAttribute("href"),
  [...tr.querySelectorAll("data")].map((d) => d.getAttribute("value")),
];

describe("the pairs a ranking reads (run-pair.ts)", () => {
  const s = scenario(1, "Faster AOI");
  it("takes a scenario's newest pair whose two runs both completed", () => {
    const runs = [baseline(1, 0), whatIf(2, s, 1), baseline(3, 2), whatIf(4, s, 3), baseline(5, 4, { status: "running" }), whatIf(6, s, 5, { status: "queued" })];
    expect(latestCompleted(earlierPairs(runs, s))).toEqual({ baseline: runs[2], scenario: runs[3] });
    expect(latestCompleted(earlierPairs(runs.slice(4), s))).toBeNull();
    expect(latestCompleted([])).toBeNull();
  });

  it("finds each what-if run's baseline among a page of runs; a run without one has none", () => {
    const other = boardBaselineRun({ id: id("sim", 9), created_at: at(0), config_hash: `sha256:${"9".repeat(64)}` });
    const runs = [baseline(1, 0), whatIf(2, s, 1), other, whatIf(3, s, 2, { config_hash: other.config_hash, model_version_id: "mv_01M4YDQK00TA0000000H073999" })];
    const found = baselinesOf(runs);
    expect(found.get(runs[1]!.id)?.id).toBe(runs[0]!.id);
    expect(found.has(runs[3]!.id)).toBe(false);
    expect(found.has(runs[0]!.id)).toBe(false);
  });
});

describe("the ranking's order (ranking.ts)", () => {
  const s = (n: number) => scenario(n, `S${n}`);
  const pair = { baseline: baseline(1, 0), scenario: whatIf(2, s(1), 1) };
  const row = (n: number, relative: number | null, over: Partial<ComparisonMetric> = {}) => ({
    scenario: s(n), version: 3, standing: standingOf([pair], comparison(pair.baseline, pair.scenario, p50({ relative_delta: relative, ...over }))),
  });

  it("puts the largest cut first, gives ties one rank, and lists what has no rank after, each kind in its own place", () => {
    const ranked = rankScenarios([
      { scenario: s(1), version: 3, standing: standingOf([], undefined) },
      row(2, -0.1),
      { scenario: s(3), version: 3, standing: standingOf([{ ...pair, scenario: { ...pair.scenario, status: "running" } }], undefined) },
      row(4, 0.05),
      row(5, -0.3),
      row(6, null, { reason: "BASELINE_ZERO" }),
      row(7, -0.1),
      { scenario: s(8), version: 3, standing: standingOf([pair], undefined) },
      { scenario: s(9), version: 3, standing: standingOf([{ ...pair, scenario: { ...pair.scenario, status: "failed" } }], undefined) },
    ]);
    expect(ranked.map((r) => [r.scenario.name, r.standing.kind, r.standing.kind === "ranked" ? r.standing.rank : null])).toEqual([
      ["S5", "ranked", 1],
      ["S2", "ranked", 2],
      ["S7", "ranked", 2],
      ["S4", "ranked", 4],
      ["S6", "compared", null],
      ["S8", "unread", null],
      ["S3", "running", null],
      ["S9", "stopped", null],
      ["S1", "none", null],
    ]);
  });

  it("ranks only a comparable, available figure with a percentage the server gave", () => {
    const c = (metric: ComparisonMetric | null, over: Partial<Comparison> = {}) => standingOf([pair], comparison(pair.baseline, pair.scenario, metric, over)).kind;
    expect(c(p50({}))).toBe("ranked");
    expect(c(p50({}), { comparable: false, incomparable_reasons: ["SEED_MISMATCH"] })).toBe("compared");
    expect(c(p50({ status: "unavailable", scenario: null, absolute_delta: null, relative_delta: null, reason: "SCENARIO_UNAVAILABLE" }))).toBe("compared");
    expect(c(null)).toBe("compared");
  });

  // Review of slice 2: a ranked row kept only its figures, so a single-replication result could stand first with nothing beside it.
  it("keeps the server's warnings with every compared row, and finds the ones every compared row shares", () => {
    const warned = (n: number, relative: number | null, warnings: ComparisonWarning[]) => ({
      scenario: s(n), version: 3, standing: standingOf([pair], comparison(pair.baseline, pair.scenario, p50({ relative_delta: relative, ...(relative === null ? { reason: "BASELINE_ZERO" as const } : {}) }), { warnings })),
    });
    const quick = warned(1, -0.25, ["SINGLE_REPLICATION", "CENSORED_ENTITIES"]);
    const careful = warned(2, -0.2, ["CENSORED_ENTITIES"]);
    const same = warned(3, null, ["SCENARIO_PATCH_EMPTY", "CENSORED_ENTITIES"]);
    const none = { scenario: s(4), version: 3, standing: standingOf([], undefined) };
    const unread = { scenario: s(5), version: 3, standing: standingOf([pair], undefined) };
    const ranked = rankScenarios([careful, same, none, quick, unread]);
    expect(ranked.map((r) => [r.scenario.name, r.standing.kind, warningsOf(r.standing)])).toEqual([
      ["S1", "ranked", ["SINGLE_REPLICATION", "CENSORED_ENTITIES"]],
      ["S2", "ranked", ["CENSORED_ENTITIES"]],
      ["S3", "compared", ["SCENARIO_PATCH_EMPTY", "CENSORED_ENTITIES"]],
      ["S5", "unread", []],
      ["S4", "none", []],
    ]);
    // In the server's order; a row without a comparison has no say.
    expect(sharedWarnings(ranked)).toEqual(["CENSORED_ENTITIES"]);
    expect(sharedWarnings([quick, same])).toEqual(["CENSORED_ENTITIES"]);
    expect(sharedWarnings([quick, warned(6, -0.1, [])])).toEqual([]);
    // With one comparison there is nothing to share: its warnings stay on its row.
    expect(sharedWarnings([quick, none, unread])).toEqual([]);
    expect(sharedWarnings([])).toEqual([]);
  });
});

describe("the Scenarios page ranks what the server compared (H-073)", () => {
  const aoi = scenario(1, "Faster AOI");
  const ict = scenario(2, "Second ICT fixture", { made_by_ai: { investigation_id: "inv_01M4YDQK00TA0000000H073001", requested_by: "usr_01M4YDQK00TA00000000000001", generated_by: { provider: "meta", model: "muse-spark-1.3" } } });
  const buffer = scenario(3, "Bigger buffer");
  const untried = scenario(4, "Night shift");
  // Faster AOI: an older pair that did better and a newer one, which is the one ranked; the newest is still running.
  const runs = [
    baseline(10, 0), whatIf(11, aoi, 1),
    baseline(12, 10), whatIf(13, ict, 11),
    baseline(14, 20), whatIf(15, aoi, 21),
    baseline(16, 30, { status: "running" }), whatIf(17, buffer, 31, { status: "queued" }),
    baseline(18, 40, { status: "running" }), whatIf(19, aoi, 41, { status: "running" }),
  ];
  const [b10, s11, b12, s13, b14, s15, b16, s17] = runs as [Run, Run, Run, Run, Run, Run, Run, Run];
  const lead = boardComparison.metrics.find((m) => m.metric_path === P50)!;
  const comparisons = [
    comparison(b10, s11, p50({ relative_delta: -0.5 })),
    comparison(b14, s15, p50({ baseline: 29.5, scenario: 26.55, absolute_delta: -2.950000000000001, relative_delta: -0.1000000000000001 })),
    comparison(b12, s13, lead),
  ];

  it("ranks each scenario by its latest completed comparison, best first, with the server's figures only, each a link to its results", async () => {
    const api = fakeApi(modelAnswers([aoi, ict, buffer, untried], runs, comparisons));
    const el = await scenariosPage(api);
    const table = el.querySelector("table")!;
    expect(text(el.querySelector("section h2"))).toBe(en["scenarios.ranking.title"]);
    expect(text(table.querySelector("caption"))).toBe(en["scenarios.ranking.caption"]);
    expect([...table.querySelectorAll("thead th")].map(text)).toEqual([en["scenarios.ranking.rank"], en["scenarios.ranking.whatIf"], en["compare.baseline"], en["compare.scenario"], en["compare.absolute"], en["compare.relative"]]);
    // The Two Inks Rule: each run's head carries its line key.
    expect([...table.querySelectorAll("thead line")].map((l) => l.getAttribute("data-run"))).toEqual(["baseline", "scenario"]);
    expect(rows(el).map(shape)).toEqual([
      ["1", results(ict, b12, s13), [lead.baseline, lead.scenario, lead.absolute_delta, lead.relative_delta].map(String)],
      ["2", results(aoi, b14, s15), ["29.5", "26.55", "-2.950000000000001", "-0.1000000000000001"]],
      ["", results(buffer, b16, s17), []],
      ["", `/scenarios/${untried.id}`, []],
    ]);
    const [first, second, third, fourth] = rows(el);
    expect(text(first)).toContain("29.14 min");
    expect(text(first)).toContain("19.77 min");
    expect(text(first)).toContain("−9.37 min");
    expect(text(first)).toContain("−32.2%");
    expect(first!.querySelector("[data-ai-mark]")).not.toBeNull();
    expect(second!.querySelector("[data-ai-mark]")).toBeNull();
    expect(text(second!.querySelector("th"))).toBe(`${aoi.name}${en["model.scenarios.version"].replace("{version}", "3")}`);
    expect(text(third)).toContain(en["scenarios.ranking.running"]);
    expect(text(fourth)).toContain(en["scenarios.ranking.notRun"]);
    // Only the latest completed pairs were compared: never the older, better one, never one still running.
    const asked = api.calls.filter((u) => u.pathname === `${v1}/simulation-comparisons`).map((u) => u.searchParams.get("scenario_run_id")).sort();
    expect(asked).toEqual([s13.id, s15.id].sort());
    // H-027: each place is linked once.
    const hrefs = [...el.querySelectorAll("a")].map((a) => a.getAttribute("href"));
    expect(new Set(hrefs).size).toBe(hrefs.length);
    // Both comparisons carry the board line's CENSORED_ENTITIES: said once, above the table, and on no row; no cap note.
    expect(notes(el)).toEqual([shared("en", "CENSORED_ENTITIES")]);
    expect(rows(el).flatMap((r) => [...r.querySelectorAll("th li")])).toEqual([]);
    expect(await violations()).toEqual([]);
  });

  it("writes the server's warnings on a ranked row beside its figures, and says once a warning every comparison shares (review of slice 2)", async () => {
    const quick = scenario(8, "Quick try");
    const careful = scenario(9, "Careful try");
    const same = scenario(10, "Same line");
    const one = { config: { ...boardBaselineRun().config, replications: 1 } };
    const theRuns = [baseline(60, 0, one), whatIf(61, quick, 1, one), baseline(62, 2), whatIf(63, careful, 3), baseline(64, 4), whatIf(65, same, 5)];
    const comps = [
      comparison(theRuns[0]!, theRuns[1]!, p50({ baseline: 30, scenario: 22.5, absolute_delta: -7.5, relative_delta: -0.25 }), { warnings: ["SINGLE_REPLICATION", "CENSORED_ENTITIES"] }),
      comparison(theRuns[2]!, theRuns[3]!, p50({ baseline: 30, scenario: 24, absolute_delta: -6, relative_delta: -0.2 }), { warnings: ["CENSORED_ENTITIES"] }),
      comparison(theRuns[4]!, theRuns[5]!, p50({ baseline: 0, scenario: 0, absolute_delta: 0, relative_delta: null, reason: "BASELINE_ZERO" }), { warnings: ["SCENARIO_PATCH_EMPTY", "CENSORED_ENTITIES"] }),
    ];
    const el = await scenariosPage(fakeApi(modelAnswers([careful, same, quick], theRuns, comps)));
    // The order is still the server's figures' (the qualifier does not move a row), each row with its own warnings in the server's words.
    expect(rows(el).map((r) => [text(r.querySelector("th a")), shape(r)[0], [...r.querySelectorAll("th li")].map(text)])).toEqual([
      [quick.name, "1", [en["compare.warning.SINGLE_REPLICATION"]]],
      [careful.name, "2", []],
      [same.name, "", [en["compare.warning.SCENARIO_PATCH_EMPTY"]]],
    ]);
    // The first row's warning is in the row that holds its figures.
    const first = rows(el)[0]!;
    expect([...first.querySelectorAll("data")].map((d) => d.getAttribute("value"))).toEqual(["30", "22.5", "-7.5", "-0.25"]);
    // The warning all three share is said once, before the table.
    expect(notes(el)).toEqual([shared("en", "CENSORED_ENTITIES")]);
    const note = el.querySelector("[role=note]")!;
    expect(note.compareDocumentPosition(el.querySelector("table")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(text(el.querySelector("tbody"))).not.toContain(en["compare.warning.CENSORED_ENTITIES"]);
    expect(await violations()).toEqual([]);
  });

  it("keeps a lone comparison's warnings on its row, with nothing said above the table", async () => {
    const quick = scenario(8, "Quick try");
    const theRuns = [baseline(70, 0), whatIf(71, quick, 1)];
    const comps = [comparison(theRuns[0]!, theRuns[1]!, p50({}), { warnings: ["SINGLE_REPLICATION", "CENSORED_ENTITIES"] })];
    const el = await scenariosPage(fakeApi(modelAnswers([quick, untried], theRuns, comps)));
    expect(rows(el).map((r) => [shape(r)[0], [...r.querySelectorAll("th li")].map(text)])).toEqual([
      ["1", [en["compare.warning.SINGLE_REPLICATION"], en["compare.warning.CENSORED_ENTITIES"]]],
      ["", []],
    ]);
    expect(notes(el)).toEqual([]);
  });

  it("sends the scenario and run reads together, and every comparison at once", async () => {
    const many = [1, 2, 3].map((n) => scenario(n, `S${n}`));
    const theRuns = many.flatMap((s, i) => [baseline(30 + 2 * i, 2 * i), whatIf(31 + 2 * i, s, 2 * i + 1)]);
    const comps = many.map((_, i) => comparison(theRuns[2 * i]!, theRuns[2 * i + 1]!, p50({ relative_delta: -0.1 * (i + 1) })));
    const answers = modelAnswers(many, theRuns, comps);
    // Each kind of read is held until every read of its kind has been sent: read one after another, they would never be answered.
    const gate = (n: number, answer: (url: URL) => Response) => {
      const held: (() => void)[] = [];
      return (url: URL) => new Promise<Response>((resolve) => {
        held.push(() => resolve(answer(url)));
        if (held.length === n) held.forEach((r) => r());
      });
    };
    const both = gate(2, (url) => (url.pathname.endsWith("/versions") ? page([boardVersion]) : page(theRuns)));
    const api = fakeApi({
      ...answers,
      [`GET ${v1}/models/${MODEL}/versions`]: both,
      [`GET ${v1}/models/${MODEL}/simulations`]: both,
      [`GET ${v1}/simulation-comparisons`]: gate(3, answers[`GET ${v1}/simulation-comparisons`] as (url: URL) => Response),
    });
    const el = await scenariosPage(api);
    expect(rows(el).map((r) => text(r.querySelector("th a")))).toEqual(["S3", "S2", "S1"]);
  });

  it(`reads at most ${MAX_COMPARISONS} comparisons, the newest results first, and says so`, async () => {
    const n = MAX_COMPARISONS + 2;
    const many = Array.from({ length: n }, (_, i) => scenario(100 + i, `S${i}`));
    const theRuns = many.flatMap((s, i) => [baseline(200 + 2 * i, 2 * i), whatIf(201 + 2 * i, s, 2 * i + 1)]);
    const comps = many.map((_, i) => comparison(theRuns[2 * i]!, theRuns[2 * i + 1]!, p50({ relative_delta: -0.01 * (i + 1) })));
    const api = fakeApi(modelAnswers(many, theRuns, comps));
    const el = await scenariosPage(api);
    expect(api.calls.filter((u) => u.pathname === `${v1}/simulation-comparisons`)).toHaveLength(MAX_COMPARISONS);
    // The two oldest results were not read: listed after the ranked ones, without figures, still a link to their results.
    const unread = rows(el).filter((r) => r.getAttribute("data-standing") === "unread");
    expect(unread.map((r) => text(r.querySelector("th a")))).toEqual(["S0", "S1"]);
    expect(unread.every((r) => r.querySelectorAll("data").length === 0 && text(r).includes(en["scenarios.ranking.unread"]))).toBe(true);
    expect(unread[0]!.querySelector("th a")?.getAttribute("href")).toBe(results(many[0]!, theRuns[0]!, theRuns[1]!));
    expect(text(el)).toContain(en["scenarios.ranking.capped"].replace("{count}", String(MAX_COMPARISONS)));
    expect(text(rows(el)[0]!.querySelector("th a"))).toBe(`S${n - 1}`);
  });

  it(`says when the model has more runs than the ${MAX_RUN_PAGES} pages it reads`, async () => {
    const api = fakeApi({ ...modelAnswers([aoi], runs, comparisons), [`GET ${v1}/models/${MODEL}/simulations`]: page(runs, "more") });
    const el = await scenariosPage(api);
    expect(api.calls.filter((u) => u.pathname === `${v1}/models/${MODEL}/simulations`)).toHaveLength(MAX_RUN_PAGES);
    expect(text(el)).toContain(en["scenario.earlier.cut"].replace("{count}", (MAX_RUN_PAGES * 100).toLocaleString("en")));
  });

  it("lists a comparison it cannot rank after the ranked ones, with the server's values and its reason", async () => {
    const zero = scenario(5, "Zero baseline");
    const apart = scenario(6, "Other seed");
    const missing = scenario(7, "No median");
    const theRuns = [baseline(40, 0), whatIf(41, zero, 1), baseline(42, 2), whatIf(43, apart, 3), baseline(44, 4), whatIf(45, missing, 5), baseline(46, 6), whatIf(47, aoi, 7)];
    const comps = [
      comparison(theRuns[0]!, theRuns[1]!, p50({ baseline: 0, scenario: 4, absolute_delta: 4, relative_delta: null, reason: "BASELINE_ZERO" })),
      comparison(theRuns[2]!, theRuns[3]!, p50({ absolute_delta: null, relative_delta: null, status: "incomparable", reason: "RUNS_NOT_COMPARABLE" }), { comparable: false, incomparable_reasons: ["SEED_MISMATCH"] }),
      comparison(theRuns[4]!, theRuns[5]!, null),
      comparison(theRuns[6]!, theRuns[7]!, p50({ relative_delta: 0.2 })),
    ];
    const el = await scenariosPage(fakeApi(modelAnswers([zero, apart, missing, aoi], theRuns, comps)));
    expect(rows(el).map((r) => [text(r.querySelector("th a")), ...shape(r).slice(0, 1), r.getAttribute("data-standing")])).toEqual([
      [aoi.name, "1", "ranked"],
      [zero.name, "", "compared"],
      [apart.name, "", "compared"],
      [missing.name, "", "compared"],
    ]);
    const [, z, a, m] = rows(el);
    expect([...z!.querySelectorAll("data")].map((d) => d.getAttribute("value"))).toEqual(["0", "4", "4"]);
    expect(text(z)).toContain(en["compare.reason.BASELINE_ZERO"]);
    expect([...a!.querySelectorAll("data")].map((d) => d.getAttribute("value"))).toEqual(["30", "27"]);
    expect(text(a)).toContain(en["compare.reason.RUNS_NOT_COMPARABLE"]);
    expect(m!.querySelectorAll("data")).toHaveLength(0);
    expect(text(m)).toContain(en["compare.reason.missing"]);
    expect(await violations()).toEqual([]);
  });

  it("fails as a whole when a read fails, with a retry, and shows the ranking once it works", async () => {
    const answers = modelAnswers([ict], runs, comparisons);
    const api = fakeApi({ ...answers, [`GET ${v1}/simulation-comparisons`]: [problem(503, "SERVICE_UNAVAILABLE"), answers[`GET ${v1}/simulation-comparisons`]] });
    const el = await scenariosPage(api);
    expect(el.querySelector("[role=alert]")).not.toBeNull();
    expect(el.querySelector("table")).toBeNull();
    await press([...el.querySelectorAll("button")].find((b) => text(b) === en.retry));
    await waitFor(() => el.querySelector("table") !== null, "the retried ranking");
    expect(shape(rows(el)[0]!)[0]).toBe("1");
  });

  it("says there are none whatever the runs read answers, since it needs no runs then", async () => {
    const api = fakeApi({ ...modelAnswers([], [], []), [`GET ${v1}/models/${MODEL}/simulations`]: problem(503, "SERVICE_UNAVAILABLE") });
    const el = await scenariosPage(api);
    expect(text(el)).toContain(en["scenarios.empty"]);
    expect(el.querySelector("[role=alert]")).toBeNull();
  });

  it("reads in Traditional Chinese", async () => {
    const el = await scenariosPage(fakeApi(modelAnswers([aoi, ict, untried], runs, comparisons)), "zh-Hant");
    expect(text(el.querySelector("section h2"))).toBe(zhHant["scenarios.ranking.title"]);
    expect(text(el.querySelector("caption"))).toBe(zhHant["scenarios.ranking.caption"]);
    expect([...el.querySelectorAll("thead th")].map(text)).toEqual([zhHant["scenarios.ranking.rank"], zhHant["scenarios.ranking.whatIf"], zhHant["compare.baseline"], zhHant["compare.scenario"], zhHant["compare.absolute"], zhHant["compare.relative"]]);
    expect(text(rows(el).at(-1))).toContain(zhHant["scenarios.ranking.notRun"]);
    expect(notes(el)).toEqual([shared("zh-Hant", "CENSORED_ENTITIES")]);
    expect(await violations()).toEqual([]);
  });
});

describe("the Runs page opens a what-if run's results (H-073)", () => {
  it("links a what-if run to its results and one without a baseline to its scenario; its baseline is not a second link there (H-027)", async () => {
    const s = scenario(1, "Faster AOI");
    const lone = whatIf(52, s, 5, { config_hash: `sha256:${"8".repeat(64)}` });
    const theRuns = [baseline(50, 0), whatIf(51, s, 1), lone];
    const el = await show(fakeApi({ [`GET ${v1}/models/${MODEL}`]: json(200, model), [`GET ${v1}/models/${MODEL}/simulations`]: page(theRuns) }), <RunsPage modelId={MODEL} />);
    await waitFor(() => el.querySelectorAll("li").length === 3, "the runs");
    expect([...el.querySelectorAll("li")].map((li) => li.querySelector("a")?.getAttribute("href") ?? null)).toEqual([null, results(s, theRuns[0]!, theRuns[1]!), `/scenarios/${s.id}`]);
    expect(await violations()).toEqual([]);
  });
});
