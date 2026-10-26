// @vitest-environment happy-dom
// T-032 (hackathon slice, H-029): run a scenario and its baseline, follow
// them (queued, running, completed, failed, cancelled, with a cancel), then
// the results: the server's key metrics side by side and the Marey chart
// of the sampled trace. Every screen through the app's own API client, with
// a fake behind its fetch; no value the API did not send is shown; no axe
// violation in any state (WCAG 2.2 A/AA without layout: contrast and target
// size are the browser checks').
import { act, Suspense } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider } from "../lib/auth/session";
import type { Comparison, Run, TraceArtifact } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import {
  BASELINE_RUN,
  SCENARIO_RUN,
  boardBaselineRun,
  boardComparison,
  boardResults,
  boardScenario,
  boardScenarioRun,
  boardTraces,
  boardVersion,
} from "../features/results/fixtures/board-line";
import { mount, press, settle, violations } from "./ui-harness";

const nav = vi.hoisted(() => ({ push: vi.fn() }));
vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ push: nav.push, refresh: () => {} }) }));
vi.setConfig({ testTimeout: 30_000 });
beforeEach(() => nav.push.mockReset());
afterEach(() => vi.useRealTimers());

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const editor: Auth = { session: { organizationId: ORG }, role: "editor" };
const viewer: Auth = { session: { organizationId: ORG }, role: "viewer" };
const catalogs = { en, "zh-Hant": zhHant } as const;

type Req = { method: string; path: string; body: unknown; key: string | undefined };
type Answer = Response | ((r: Req) => Response);
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQ } });
const problem = (status: number, code: string, message = "x") => json(status, { error: { code, message, request_id: REQ } });

/** The app's client over a fetch that answers by "METHOD path", queued answers in order, the last one kept. */
function fakeApi(table: Record<string, Answer | Answer[]>) {
  const calls: Req[] = [];
  const queues = new Map(Object.entries(table).map(([k, v]) => [k, Array.isArray(v) ? [...v] : [v]]));
  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://app.test");
    const method = init?.method ?? "GET";
    const req: Req = { method, path: url.pathname + url.search, body: init?.body ? JSON.parse(String(init.body)) : undefined, key: (init?.headers as Record<string, string>)["Idempotency-Key"] };
    calls.push(req);
    const q = queues.get(`${method} ${url.pathname}`);
    if (!q || q.length === 0) throw new Error(`no fake answer for ${method} ${url.pathname}`);
    const next = q.length > 1 ? q.shift()! : q[0]!;
    const res = typeof next === "function" ? next(req) : next.clone();
    return res;
  }) as typeof globalThis.fetch;
  return { calls, client: createApiClient({ session: () => editor.session, fetch }) };
}

const v1 = "/api/v1";
const scenarioAnswers = {
  [`GET ${v1}/scenarios/${boardScenario.id}`]: json(200, boardScenario),
  [`GET ${v1}/model-versions/${boardVersion.id}`]: json(200, boardVersion),
};
const resultAnswers = (over: { comparison?: Comparison; traces?: { baseline: TraceArtifact | null; scenario: TraceArtifact | null } } = {}) => {
  const traces = over.traces ?? boardTraces;
  const result = (r: typeof boardResults.baseline, t: TraceArtifact | null) => ({ ...r, trace: { ...r.trace, available: t !== null } });
  return {
    [`GET ${v1}/simulation-comparisons`]: json(200, over.comparison ?? boardComparison),
    [`GET ${v1}/simulations/${BASELINE_RUN}/results`]: json(200, result(boardResults.baseline, traces.baseline)),
    [`GET ${v1}/simulations/${SCENARIO_RUN}/results`]: json(200, result(boardResults.scenario, traces.scenario)),
    ...(traces.baseline ? { [`GET ${v1}/simulations/${BASELINE_RUN}/trace`]: json(200, traces.baseline) } : {}),
    ...(traces.scenario ? { [`GET ${v1}/simulations/${SCENARIO_RUN}/trace`]: json(200, traces.scenario) } : {}),
  };
};

const progress = (revision: number, completed: number, total = 5) => ({ completed_replications: completed, total_replications: total, events_processed: completed * 1000, revision });
const queuedB = boardBaselineRun({ status: "queued", progress: progress(1, 0), started_at: null, finished_at: null });
const queuedS = boardScenarioRun({ status: "queued", progress: progress(1, 0), started_at: null, finished_at: null });
const runningB = boardBaselineRun({ status: "running", progress: progress(3, 2), finished_at: null });
const runningS = boardScenarioRun({ status: "running", progress: progress(2, 1), finished_at: null });

// Generous: these run beside other suites on a shared machine.
async function waitFor(check: () => boolean, what: string, ms = 10_000): Promise<void> {
  const until = Date.now() + ms;
  while (!check()) {
    if (Date.now() > until) throw new Error(`timed out waiting for ${what}`);
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
}

const text = (el: Element) => (el.textContent ?? "").replace(/\s+/g, " ");
const button = (el: Element, name: string) => [...el.querySelectorAll("button")].find((b) => text(b).trim() === name);

async function scenarioPage(api: ReturnType<typeof fakeApi>, auth: Auth = editor, locale: Locale = "en") {
  const { default: ScenarioPage } = await import("../app/scenarios/[scenarioId]/page");
  const el = await mount(
    <AuthProvider value={auth}>
      <ApiProvider client={api.client}>
        <Suspense fallback={null}>
          <ScenarioPage params={Promise.resolve({ scenarioId: boardScenario.id })} />
        </Suspense>
      </ApiProvider>
    </AuthProvider>,
    locale,
  );
  await settle();
  await waitFor(() => el.querySelector("h1, [role=alert]") !== null, "the scenario page");
  return el;
}

async function runResults(api: ReturnType<typeof fakeApi>, auth: Auth = editor, locale: Locale = "en", ids = { baseline: BASELINE_RUN, scenario: SCENARIO_RUN }) {
  const { RunResults } = await import("../features/results/run-results");
  const el = await mount(
    <AuthProvider value={auth}>
      <ApiProvider client={api.client}>
        {/* Fast enough for the test, slow enough that act() can settle between polls. */}
        <RunResults scenarioId={boardScenario.id} baselineId={ids.baseline} scenarioRunId={ids.scenario} pollMs={25} />
      </ApiProvider>
    </AuthProvider>,
    locale,
  );
  await settle();
  return el;
}

describe("the scenario page: run the scenario and its baseline", () => {
  it("shows the scenario, its base version and the run settings in the model's time unit, with no violation", async () => {
    const api = fakeApi(scenarioAnswers);
    const el = await scenarioPage(api);
    expect(el.querySelector("h1")?.textContent).toBe(boardScenario.name);
    expect(text(el)).toContain("A what-if on version 3 of Board line (synthetic), with 1 change.");
    const field = (label: string) => ([...el.querySelectorAll("label")].find((l) => l.textContent === label) as HTMLLabelElement | undefined)?.control as HTMLInputElement | undefined;
    expect([field("Run length (minutes)")?.value, field("Warm-up (minutes)")?.value, field("Replications")?.value, field("Seed")?.value]).toEqual(["1440", "0", "5", "1"]);
    expect(el.querySelector('a[href="/models/' + boardScenario.model_id + '"]')?.textContent).toBe(en["scenario.back"]);
    expect(button(el, en["run.start"])?.getAttribute("data-variant")).toBe("run");
    expect(await violations()).toEqual([]);
  });

  it("queues the baseline (base version, patch []) and the scenario (its ID) with one config and their own keys, then opens their results", async () => {
    const api = fakeApi({
      ...scenarioAnswers,
      [`POST ${v1}/simulations`]: (r) => json(202, { id: (r.body as { scenario_id?: string }).scenario_id ? SCENARIO_RUN : BASELINE_RUN, status: "queued", model_version_id: boardVersion.id, created_at: "2026-10-26T09:00:00Z" }),
    });
    const el = await scenarioPage(api);
    await press(button(el, en["run.start"]));
    await waitFor(() => nav.push.mock.calls.length > 0, "navigation");
    const posts = api.calls.filter((c) => c.method === "POST");
    const config = { horizon: { value: 1440, unit: "minute" }, warmup: { value: 0, unit: "minute" }, replications: 5, seed: 1, trace_level: "sampled" };
    expect(posts.map((p) => p.body)).toEqual([
      { model_version_id: boardVersion.id, name: "Baseline for Second ICT fixture", scenario_patch: [], config },
      { model_version_id: boardVersion.id, name: "Second ICT fixture", scenario_id: boardScenario.id, config },
    ]);
    expect(posts[0]?.key).toMatch(/^[\x21-\x7e]{8,128}$/);
    expect(posts[0]?.key).not.toBe(posts[1]?.key);
    expect(nav.push.mock.calls).toEqual([[`/scenarios/${boardScenario.id}/results?baseline=${BASELINE_RUN}&scenario=${SCENARIO_RUN}`]]);
  });

  it("names a value it cannot accept and sends nothing", async () => {
    const api = fakeApi(scenarioAnswers);
    const el = await scenarioPage(api);
    const input = ([...el.querySelectorAll("label")].find((l) => l.textContent === "Replications") as HTMLLabelElement).control as HTMLInputElement;
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
      set.call(input, "0");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await press(button(el, en["run.start"]));
    await settle();
    expect(text(el)).toContain(en["run.issue.replications"]);
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(api.calls.filter((c) => c.method === "POST")).toEqual([]);
    expect(await violations()).toEqual([]);
  });

  it("says which run could not start and retries it with the same key, so nothing is queued twice", async () => {
    const accepted = (id: string) => json(202, { id, status: "queued", model_version_id: boardVersion.id, created_at: "2026-10-26T09:00:00Z" });
    const api = fakeApi({ ...scenarioAnswers, [`POST ${v1}/simulations`]: [problem(503, "SERVICE_UNAVAILABLE"), accepted(BASELINE_RUN), accepted(SCENARIO_RUN)] });
    const el = await scenarioPage(api);
    await press(button(el, en["run.start"]));
    await waitFor(() => text(el).includes(en["run.startFailed.baseline"]), "the failure");
    expect(el.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("unavailable");
    expect(nav.push).not.toHaveBeenCalled();
    await press(button(el, en["run.start"]));
    await waitFor(() => nav.push.mock.calls.length > 0, "navigation");
    const posts = api.calls.filter((c) => c.method === "POST");
    expect(posts).toHaveLength(3);
    expect(posts[1]?.key).toBe(posts[0]?.key);
    expect(posts[2]?.key).not.toBe(posts[0]?.key);
  });

  it("offers no run to a viewer, and says why", async () => {
    const api = fakeApi(scenarioAnswers);
    const el = await scenarioPage(api, viewer);
    expect(text(el)).toContain(en["run.readOnly.viewer"]);
    expect(button(el, en["run.start"])).toBeUndefined();
    expect(el.querySelectorAll("input")).toHaveLength(0);
  });

  it("shows a scenario that does not exist as not found", async () => {
    const api = fakeApi({ [`GET ${v1}/scenarios/${boardScenario.id}`]: problem(404, "NOT_FOUND") });
    const el = await scenarioPage(api);
    expect(el.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("not_found");
  });
});

describe("the runs, from queued to the results", () => {
  it("follows both runs to completion, then shows the server's metrics and the chart, with no violation on the way", async () => {
    // What the server says about each run; the test moves it on.
    const now = { b: queuedB, s: queuedS };
    const api = fakeApi({
      ...scenarioAnswers,
      [`GET ${v1}/simulations/${BASELINE_RUN}`]: () => json(200, now.b),
      [`GET ${v1}/simulations/${SCENARIO_RUN}`]: () => json(200, now.s),
      ...resultAnswers(),
    });
    const el = await runResults(api);
    await waitFor(() => text(el).includes(en["runs.status.queued"]), "queued");
    expect(text(el).match(new RegExp(en["runs.status.queued"], "g"))).toHaveLength(2);
    expect(button(el, en["runs.cancel"])).toBeDefined();
    expect(await violations()).toEqual([]);
    now.b = runningB;
    now.s = runningS;
    await waitFor(() => el.querySelectorAll('[role="progressbar"]').length === 2, "running");
    const bars = [...el.querySelectorAll('[role="progressbar"]')];
    expect(bars.map((b) => [b.getAttribute("aria-label"), b.getAttribute("aria-valuetext")])).toEqual([
      [en["runs.progress.baseline"], "2 of 5 replications"],
      [en["runs.progress.scenario"], "1 of 5 replications"],
    ]);
    expect(await violations()).toEqual([]);
    now.b = boardBaselineRun();
    now.s = boardScenarioRun();
    await waitFor(() => el.querySelector("table") !== null, "the results");
    expect(button(el, en["runs.cancel"])).toBeUndefined();
    // The server's values, exactly, beside their locale text.
    const lead = boardComparison.metrics.find((m) => m.metric_path === "/metrics/lead_time_minutes/p50")!;
    const row = [...el.querySelectorAll("tbody tr")].find((r) => r.querySelector("th")?.textContent === en["metric.leadTimeMedian"])!;
    expect([...row.querySelectorAll("data")].map((d) => d.getAttribute("value"))).toEqual([String(lead.baseline), String(lead.scenario), String(lead.absolute_delta), String(lead.relative_delta)]);
    expect([...row.querySelectorAll("data")].map((d) => d.textContent)).toEqual(["29.14 min", "19.77 min", "-9.37 min", "-32.2%"]);
    expect(text(el)).toContain("Median lead time 29.14 min → 19.77 min: 32.2% shorter.");
    // Each run's own interval of a mean, from its Result.
    expect(text(el)).toContain("95% CI 78.14 to 96.26");
    // The chart: an image with a title and a description, two line keys, and its table.
    const chart = el.querySelector('svg[role="img"]')!;
    const name = chart.getAttribute("aria-labelledby")!.split(" ").map((id) => el.ownerDocument.getElementById(id)?.textContent);
    expect(name[0]).toBe(en["marey.title"]);
    expect(name[1]).toContain("Dashed lines: the baseline, 19 work items.");
    expect(chart.querySelectorAll("polyline[data-run=baseline]")).toHaveLength(19);
    expect(chart.querySelectorAll("polyline[data-run=scenario]")).toHaveLength(19);
    // Dashed versus solid carries the difference, not only the hue (DESIGN.md The Two Inks Rule).
    expect(text(el)).toContain(en["marey.baseline"]);
    expect(text(el)).toContain(en["marey.scenario"]);
    expect(text(el)).toContain("Sampled trace of replication 1: about 1 in 8 work items.");
    expect(el.querySelector("details summary")?.textContent).toBe(en["marey.table"]);
    expect(text(el)).toContain(en["compare.warning.CENSORED_ENTITIES"]);
    // Run settings, as frozen in the runs.
    expect(text(el)).toContain("Version 3");
    expect(await violations()).toEqual([]);
    // The comparison was asked once, for this pair, in this order.
    expect(api.calls.filter((c) => c.path.startsWith(`${v1}/simulation-comparisons`)).map((c) => c.path)).toEqual([`${v1}/simulation-comparisons?baseline_run_id=${BASELINE_RUN}&scenario_run_id=${SCENARIO_RUN}`]);
  });

  it("cancels the runs that are still going, each with an empty body and its own key, and then has nothing to compare", async () => {
    const stopping = boardScenarioRun({ status: "running", cancellation_requested: true, progress: progress(3, 1), finished_at: null });
    const queuedCancelled = boardBaselineRun({ status: "cancelled", progress: progress(2, 0), finished_at: "2026-10-26T09:00:03Z" });
    const now = { b: queuedB, s: runningS };
    const api = fakeApi({
      ...scenarioAnswers,
      [`GET ${v1}/simulations/${BASELINE_RUN}`]: () => json(200, now.b),
      [`GET ${v1}/simulations/${SCENARIO_RUN}`]: () => json(200, now.s),
      [`POST ${v1}/simulations/${BASELINE_RUN}/cancel`]: json(200, queuedCancelled),
      [`POST ${v1}/simulations/${SCENARIO_RUN}/cancel`]: () => {
        now.s = stopping;
        return json(200, stopping);
      },
    });
    const el = await runResults(api);
    await waitFor(() => button(el, en["runs.cancel"]) !== undefined, "the cancel");
    await press(button(el, en["runs.cancel"]));
    await waitFor(() => text(el).includes(en["runs.status.stopping"]), "the scenario stopping");
    // The worker stops the scenario at its next checkpoint.
    now.b = queuedCancelled;
    now.s = boardScenarioRun({ status: "cancelled", progress: progress(4, 1) });
    await waitFor(() => text(el).includes(en["runs.noComparison"]), "both cancelled");
    const cancels = api.calls.filter((c) => c.method === "POST");
    expect(cancels.map((c) => [c.path, c.body])).toEqual([[`${v1}/simulations/${BASELINE_RUN}/cancel`, {}], [`${v1}/simulations/${SCENARIO_RUN}/cancel`, {}]]);
    expect(cancels[0]?.key).not.toBe(cancels[1]?.key);
    expect(text(el).match(new RegExp(en["runs.status.cancelled"], "g"))).toHaveLength(2);
    expect(button(el, en["runs.cancel"])).toBeUndefined();
    expect(api.calls.some((c) => c.path.includes("comparisons") || c.path.endsWith("/results"))).toBe(false);
    expect(el.querySelector(`a[href="/scenarios/${boardScenario.id}"]`)?.textContent).toBe(en["runs.back"]);
    expect(await violations()).toEqual([]);
  });

  it("shows that a run is stopping once the server has the request", async () => {
    const stopping = boardScenarioRun({ status: "running", cancellation_requested: true, progress: progress(3, 1), finished_at: null });
    const api = fakeApi({
      ...scenarioAnswers,
      [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, boardBaselineRun()),
      // The poll keeps answering what it knew before the cancel: the cancel's own answer is newer.
      [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, runningS),
      [`POST ${v1}/simulations/${SCENARIO_RUN}/cancel`]: json(200, stopping),
    });
    const el = await runResults(api);
    await waitFor(() => button(el, en["runs.cancel"]) !== undefined, "the cancel");
    await press(button(el, en["runs.cancel"]));
    await waitFor(() => text(el).includes(en["runs.status.stopping"]), "stopping");
    // The completed baseline is not cancelled.
    expect(api.calls.filter((c) => c.method === "POST").map((c) => c.path)).toEqual([`${v1}/simulations/${SCENARIO_RUN}/cancel`]);
  });

  it("shows a failed run's error as the API's problem, and compares nothing", async () => {
    const failed = boardScenarioRun({
      status: "failed", progress: progress(4, 2),
      error: { error: { code: "MODEL_VALIDATION_FAILED", message: "the run exceeded its event budget (max_events)", details: [{ path: "/execution_limits/max_events", reason: "the run exceeded its event budget", code: "EVENT_BUDGET_EXCEEDED" }], request_id: REQ } },
    });
    const api = fakeApi({ ...scenarioAnswers, [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, boardBaselineRun()), [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, failed) });
    const el = await runResults(api);
    await waitFor(() => text(el).includes(en["runs.failed.scenario"]), "the failure");
    const alert = el.querySelector('[role="alert"]')!;
    expect([alert.getAttribute("data-problem"), alert.querySelector("h3")?.textContent]).toEqual(["invalid", en["problem.heading.invalid"]]);
    expect(text(alert)).toContain(en["error.MODEL_VALIDATION_FAILED"]);
    expect(text(alert)).toContain("/execution_limits/max_events");
    expect(text(el)).toContain(en["runs.noComparison"]);
    expect(api.calls.some((c) => c.path.includes("comparisons"))).toBe(false);
    expect(await violations()).toEqual([]);
  });

  it("says when the runs cannot be compared (another engine version, D-088) and shows their values without a difference", async () => {
    const incomparable: Comparison = {
      ...boardComparison, comparable: false, incomparable_reasons: ["ENGINE_VERSION_MISMATCH"], warnings: [],
      metrics: boardComparison.metrics.map((m) => ({ ...m, absolute_delta: null, relative_delta: null, status: "incomparable", reason: "RUNS_NOT_COMPARABLE" })),
    };
    const api = fakeApi({
      ...scenarioAnswers,
      [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, boardBaselineRun({ engine_version: "des-go-0.4.0" })),
      [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, boardScenarioRun()),
      ...resultAnswers({ comparison: incomparable }),
    });
    const el = await runResults(api);
    await waitFor(() => el.querySelector("table") !== null, "the results");
    const note = [...el.querySelectorAll('[role="note"]')].find((n) => n.querySelector("h2, h3")?.textContent === en["compare.incomparable.heading"])!;
    expect(text(note)).toContain(en["compare.incomparable.ENGINE_VERSION_MISMATCH"]);
    const row = [...el.querySelectorAll("tbody tr")].find((r) => r.querySelector("th")?.textContent === en["metric.leadTimeMedian"])!;
    expect([...row.querySelectorAll("data")].map((d) => d.getAttribute("value"))).toEqual(["29.138691666666666", "19.766575"]);
    expect(text(row)).toContain(en["compare.reason.RUNS_NOT_COMPARABLE"]);
    // No summary sentence claims a change.
    expect(text(el)).not.toContain("shorter");
    // Both engines are named in the settings.
    expect(text(el)).toContain("des-go-0.4.0");
    expect(text(el)).toContain("des-go-0.5.0");
    expect(await violations()).toEqual([]);
  });

  it("draws no chart without a sampled trace and says so; a trace with no events is no trace", async () => {
    for (const traces of [{ baseline: null, scenario: null }, { baseline: { ...boardTraces.baseline, events: [] }, scenario: { ...boardTraces.scenario, events: [] } }]) {
      const api = fakeApi({ ...scenarioAnswers, [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, boardBaselineRun()), [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, boardScenarioRun()), ...resultAnswers({ traces }) });
      const el = await runResults(api);
      await waitFor(() => el.querySelector("table") !== null, "the results");
      expect(el.querySelector('svg[role="img"]')).toBeNull();
      expect(text(el)).toContain(en["marey.none"]);
      expect(await violations()).toEqual([]);
      document.body.innerHTML = "";
    }
  });

  it("draws the one run that has a trace, and says the other has none", async () => {
    const api = fakeApi({ ...scenarioAnswers, [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, boardBaselineRun()), [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, boardScenarioRun()), ...resultAnswers({ traces: { baseline: boardTraces.baseline, scenario: null } }) });
    const el = await runResults(api);
    await waitFor(() => el.querySelector('svg[role="img"]') !== null, "the chart");
    expect(el.querySelectorAll("polyline[data-run=scenario]")).toHaveLength(0);
    expect(el.querySelectorAll("polyline[data-run=baseline]")).toHaveLength(19);
    expect(text(el)).toContain(en["marey.missing.scenario"]);
  });

  it("says when a trace stopped at its limit", async () => {
    const api = fakeApi({ ...scenarioAnswers, [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, boardBaselineRun()), [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, boardScenarioRun()), ...resultAnswers({ traces: { baseline: { ...boardTraces.baseline, truncated: true }, scenario: boardTraces.scenario } }) });
    const el = await runResults(api);
    await waitFor(() => el.querySelector('svg[role="img"]') !== null, "the chart");
    expect(text(el)).toContain(en["marey.truncated.baseline"]);
  });

  it("offers no cancel to a viewer", async () => {
    const api = fakeApi({ ...scenarioAnswers, [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, runningB), [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, runningS) });
    const el = await runResults(api, viewer);
    await waitFor(() => el.querySelector('[role="progressbar"]') !== null, "running");
    expect(button(el, en["runs.cancel"])).toBeUndefined();
  });

  it("shows a failed poll with a retry, and goes on after it", async () => {
    const api = fakeApi({
      ...scenarioAnswers,
      [`GET ${v1}/simulations/${BASELINE_RUN}`]: [problem(503, "SERVICE_UNAVAILABLE"), json(200, boardBaselineRun())],
      [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, boardScenarioRun()),
      ...resultAnswers(),
    });
    const el = await runResults(api);
    await waitFor(() => button(el, en.retry) !== undefined, "the retry");
    expect(el.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("unavailable");
    await press(button(el, en.retry));
    await waitFor(() => el.querySelector("table") !== null, "the results");
  });

  it("shows runs that are not this scenario's as not found, and asks for nothing more", async () => {
    const other = boardScenarioRun({ scenario_id: "scn_01M4YDQK00TA0000000000T033" });
    const api = fakeApi({ ...scenarioAnswers, [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, boardBaselineRun()), [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, other) });
    const el = await runResults(api);
    await waitFor(() => el.querySelector('[role="alert"]') !== null, "the problem");
    expect(el.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("not_found");
    await settle();
    expect(api.calls.filter((c) => c.path.includes("/simulations/")).length).toBe(2);
  });

  it("reads in Traditional Chinese, numbers the locale's way", async () => {
    const api = fakeApi({ ...scenarioAnswers, [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, boardBaselineRun()), [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, boardScenarioRun()), ...resultAnswers() });
    const el = await runResults(api, editor, "zh-Hant");
    await waitFor(() => el.querySelector("table") !== null, "the results");
    const zh = catalogs["zh-Hant"];
    expect([...el.querySelectorAll("h2")].map((h) => h.textContent)).toEqual(expect.arrayContaining([zh["runs.title"], zh["compare.title"], zh["marey.title"], zh["facts.title"]]));
    expect(text(el)).toContain("前置時間中位數 29.14 分鐘 → 19.77 分鐘：縮短 32.2%。");
    expect(await violations()).toEqual([]);
  });
});

describe("the results route", () => {
  it("reads the two runs from the address and shows them", async () => {
    const api = fakeApi({ ...scenarioAnswers, [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, boardBaselineRun()), [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, boardScenarioRun()), ...resultAnswers() });
    const { default: ResultsPage } = await import("../app/scenarios/[scenarioId]/results/page");
    const el = await mount(
      <AuthProvider value={editor}>
        <ApiProvider client={api.client}>
          <Suspense fallback={null}>
            <ResultsPage params={Promise.resolve({ scenarioId: boardScenario.id })} searchParams={Promise.resolve({ baseline: BASELINE_RUN, scenario: SCENARIO_RUN })} />
          </Suspense>
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => el.querySelector("table") !== null, "the results");
    expect(el.querySelector("h1")?.textContent).toBe(boardScenario.name);
  });

  it("shows a missing or repeated run in the address as not found, without calling the API for it", async () => {
    const api = fakeApi(scenarioAnswers);
    const { default: ResultsPage } = await import("../app/scenarios/[scenarioId]/results/page");
    for (const searchParams of [{}, { baseline: [BASELINE_RUN, BASELINE_RUN], scenario: SCENARIO_RUN }]) {
      const el = await mount(
        <AuthProvider value={editor}>
          <ApiProvider client={api.client}>
            <Suspense fallback={null}>
              <ResultsPage params={Promise.resolve({ scenarioId: boardScenario.id })} searchParams={Promise.resolve(searchParams)} />
            </Suspense>
          </ApiProvider>
        </AuthProvider>,
      );
      await waitFor(() => el.querySelector('[role="alert"]') !== null, "the problem");
      expect(el.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("not_found");
      document.body.innerHTML = "";
    }
    expect(api.calls.filter((c) => c.path.includes("/simulations"))).toEqual([]);
  });

  it("asks for a sign-in without a session, and calls nothing", async () => {
    const api = fakeApi({});
    const { default: ResultsPage } = await import("../app/scenarios/[scenarioId]/results/page");
    const el = await mount(
      <AuthProvider value={{ session: null, role: null }}>
        <ApiProvider client={api.client}>
          <Suspense fallback={null}>
            <ResultsPage params={Promise.resolve({ scenarioId: boardScenario.id })} searchParams={Promise.resolve({ baseline: BASELINE_RUN, scenario: SCENARIO_RUN })} />
          </Suspense>
        </ApiProvider>
      </AuthProvider>,
    );
    await settle();
    expect(el.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("unauthenticated");
    expect(api.calls).toEqual([]);
  });
});

export type { Run };
