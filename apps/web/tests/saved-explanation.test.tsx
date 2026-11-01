// @vitest-environment happy-dom
// H-073 slice 1: a finished comparison shows its saved explanation. The
// side panel's AI explanation first asks the server for the newest
// completed analysis of the pair (listAnalyses, one item); when there is
// one it shows it at once, labelled with the model that wrote it and its
// time, with no new provider call; an editor can still ask again. With
// none it is the form it always was (an editor) or nothing (a viewer).
// Every figure is the server's; no axe violation in any state.
import { act, useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider } from "../lib/auth/session";
import type { AnalysisRecord } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { i18nFor } from "../lib/i18n/provider";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { BASELINE_RUN, SCENARIO_RUN, boardBaselineRun, boardComparison, boardResults, boardScenarioRun, boardVersion } from "../features/results/fixtures/board-line";
import { keyMetrics } from "../features/results/metrics";
import { SavedExplanation } from "../features/results/saved-explanation";
import { mount, press, settle, unmountAll, violations } from "./ui-harness";

const nav = vi.hoisted(() => ({ push: vi.fn() }));
vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ push: nav.push, refresh: () => {} }) }));
vi.setConfig({ testTimeout: 30_000 });
beforeEach(() => nav.push.mockReset());
afterEach(() => vi.unstubAllEnvs());

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const ANA = "ana_01M4YDQK00TA0000000000H073";
const OLD = "ana_01M4YDQK00TA0000000000H07A";
const editor: Auth = { session: { organizationId: ORG }, role: "editor" };
const viewer: Auth = { session: { organizationId: ORG }, role: "viewer" };
const v1 = "/api/v1";
const listPath = `${v1}/simulations/${SCENARIO_RUN}/analyses`;

type Req = { method: string; path: string; query: Record<string, string>; body: unknown };
type Answer = Response | Promise<Response>;
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQ } });

/** The app's client over a fetch that answers by "METHOD path", queued answers in order, the last one kept. */
function fakeApi(table: Record<string, Answer | Answer[]>) {
  const calls: Req[] = [];
  const queues = new Map(Object.entries(table).map(([k, v]) => [k, Array.isArray(v) ? [...v] : [v]]));
  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://app.test");
    const method = init?.method ?? "GET";
    calls.push({ method, path: url.pathname, query: Object.fromEntries(url.searchParams), body: init?.body ? JSON.parse(String(init.body)) : undefined });
    const q = queues.get(`${method} ${url.pathname}`);
    if (!q || q.length === 0) throw new Error(`no fake answer for ${method} ${url.pathname}`);
    const next = q.length > 1 ? q.shift()! : q[0]!;
    return (await next).clone();
  }) as typeof globalThis.fetch;
  return { calls, client: createApiClient({ session: () => editor.session, fetch }) };
}

const lead = boardComparison.metrics.find((m) => m.metric_path === "/metrics/lead_time_minutes/p50")!;
const saved = (over: Partial<AnalysisRecord> = {}): AnalysisRecord => ({
  id: ANA, organization_id: ORG, run_id: SCENARIO_RUN, baseline_run_id: BASELINE_RUN, status: "completed",
  created_at: "2026-11-01T06:00:46Z", error: null, generated_by: { provider: "meta", model: "muse-spark-1.3" }, output_locale: "en",
  analysis: {
    baseline: { summary: "One ICT fixture held boards back." },
    scenario: { summary: "A second ICT fixture shortened the lead time." },
    delta: [{ metric_path: lead.metric_path, absolute_delta: lead.absolute_delta, relative_delta: lead.relative_delta, reason: "Less waiting before ICT." }],
    bottleneck: [{ statement: "ICT was the busiest station.", kind: "observation", evidence: [{ run: "baseline", metric_path: "/metrics/resources/ict_fixture" }] }],
    assumptions: ["Test times stay as modeled."],
    uncertainty: ["5 replications leave some spread."],
    suggested_next_experiments: [{ name: "Faster AOI", hypothesis: "AOI may become the next constraint.", requires_confirmation: true }],
  },
  ...over,
});
const page = (items: AnalysisRecord[], next: string | null = null) => json(200, { items, next_cursor: next });
/** The same explanation as written in Traditional Chinese (a request with output_locale zh-Hant), saved later. */
const inChinese = (over: Partial<AnalysisRecord> = {}): AnalysisRecord =>
  saved({
    id: OLD, output_locale: "zh-Hant", created_at: "2026-11-01T07:00:00Z",
    analysis: {
      baseline: { summary: "一台 ICT 治具讓板子等待。" },
      scenario: { summary: "第二台 ICT 治具縮短了前置時間。" },
      delta: [{ metric_path: lead.metric_path, absolute_delta: lead.absolute_delta, relative_delta: lead.relative_delta, reason: "ICT 前的等待變少。" }],
      bottleneck: [{ statement: "ICT 是最忙的工作站。", kind: "observation", evidence: [{ run: "baseline", metric_path: "/metrics/resources/ict_fixture" }] }],
      assumptions: ["測試時間維持模型設定。"],
      uncertainty: ["5 次重複仍有一些變異。"],
      suggested_next_experiments: [{ name: "更快的 AOI", hypothesis: "AOI 可能成為下一個限制。", requires_confirmation: true }],
    },
    ...over,
  });

const text = (el: Element) => (el.textContent ?? "").replace(/\s+/g, " ");
const button = (el: Element, name: string) => [...el.querySelectorAll("button")].find((b) => text(b).trim() === name);
const section = (el: Element) => el.querySelector('section[aria-labelledby="analysis-heading"]');

async function waitFor(check: () => boolean, what: string, ms = 10_000): Promise<void> {
  const until = Date.now() + ms;
  while (!check()) {
    if (Date.now() > until) throw new Error(`timed out waiting for ${what}`);
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
}

async function panel(api: ReturnType<typeof fakeApi>, opts: { auth?: Auth; locale?: Locale; initial?: string | null } = {}) {
  const seen: (string | null)[] = [];
  const baseline = boardBaselineRun();
  const scenario = boardScenarioRun();
  const rows = keyMetrics(boardComparison, boardVersion.ir, { baseline: boardResults.baseline, scenario: boardResults.scenario });
  function Host() {
    const [id, setId] = useState<string | null>(opts.initial ?? null);
    return <SavedExplanation baseline={baseline} scenario={scenario} rows={rows} pollMs={25} analysisId={id} onAnalysis={(next) => { seen.push(next); setId(next); }} />;
  }
  const el = await mount(
    <AuthProvider value={opts.auth ?? editor}>
      <ApiProvider client={api.client}>
        <Host />
      </ApiProvider>
    </AuthProvider>,
    opts.locale ?? "en",
  );
  await settle();
  return { el, seen };
}

describe("a finished comparison's saved explanation (H-073 slice 1)", () => {
  it("shows the pair's newest saved explanation at once, with its model and time, and asks no AI", async () => {
    const api = fakeApi({ [`GET ${listPath}`]: page([saved()]) });
    const { el } = await panel(api);
    await waitFor(() => text(section(el) ?? el).includes("A second ICT fixture"), "the saved explanation");
    const s = section(el)!;
    expect(s.querySelector("h2")?.textContent).toBe(en["analysis.title"]);
    // One read of the pair's newest few (to find one in the reader's language): no POST, no poll of the analysis.
    expect(api.calls).toEqual([{ method: "GET", path: listPath, query: { baseline_run_id: BASELINE_RUN, limit: "10" }, body: undefined }]);
    // Labelled with the model that wrote it and when it was saved, the exact instant in <time>.
    expect(text(s)).toContain(`Written by Muse Spark 1.3 (Meta) from these results. ${en["analysis.checked"]}`);
    const when = s.querySelector("time")!;
    expect(when.getAttribute("datetime")).toBe("2026-11-01T06:00:46Z");
    expect(text(when.parentElement!)).toBe(en["analysis.saved.when"].replace("{time}", i18nFor("en").dateTime("2026-11-01T06:00:46Z")));
    // The figure beside the AI's words is the server's comparison's.
    expect(s.querySelector(`data[value="${lead.absolute_delta}"]`)).not.toBeNull();
    expect(text(s)).toContain("Less waiting before ICT.");
    // An editor can ask again, and propose a suggestion as before; nothing says it is being written.
    expect(button(s, en["analysis.again"])).toBeDefined();
    expect([...s.querySelectorAll("button")].some((b) => b.getAttribute("aria-label") === en["analysis.proposeNamed"].replace("{name}", "Faster AOI"))).toBe(true);
    expect(text(s)).not.toContain(en["analysis.generating"]);
    expect(button(s, en["analysis.ask"])).toBeUndefined();
    expect(await violations()).toEqual([]);
  });

  it("lets an editor ask again: the form, then the new analysis in the address, followed as before", async () => {
    const api = fakeApi({
      [`GET ${listPath}`]: page([saved()]),
      [`POST ${listPath}`]: json(202, { id: OLD, status: "generating" }),
      [`GET ${v1}/analyses/${OLD}`]: [json(200, saved({ id: OLD, status: "generating", analysis: null, generated_by: undefined })), json(200, saved({ id: OLD }))],
    });
    const { el, seen } = await panel(api);
    await waitFor(() => button(section(el) ?? el, en["analysis.again"]) !== undefined, "the saved explanation");
    await press(button(section(el)!, en["analysis.again"]));
    // The form it always was: a question and the AI button.
    const ask = button(section(el)!, en["analysis.ask"])!;
    expect(ask.getAttribute("data-variant")).toBe("ai");
    expect(text(section(el)!)).not.toContain("A second ICT fixture");
    expect(await violations()).toEqual([]);
    await press(ask);
    await waitFor(() => seen.length > 0, "the analysis in the address");
    expect(seen).toEqual([OLD]);
    expect(api.calls.find((c) => c.method === "POST")!.body).toEqual({ baseline_run_id: BASELINE_RUN, output_locale: "en" });
    await waitFor(() => text(section(el)!).includes("A second ICT fixture"), "the new explanation");
    // Leaving it ("Ask again") opens the form, not the saved one again, and reads the list no more.
    const reads = api.calls.filter((c) => c.method === "GET" && c.path === listPath).length;
    await press(button(section(el)!, en["analysis.again"]));
    expect(seen).toEqual([OLD, null]);
    await waitFor(() => button(section(el)!, en["analysis.ask"]) !== undefined, "the form again");
    expect(api.calls.filter((c) => c.method === "GET" && c.path === listPath)).toHaveLength(reads);
  });

  it("shows a viewer the saved explanation and nothing to ask with; in Traditional Chinese, its own words", async () => {
    const api = fakeApi({ [`GET ${listPath}`]: page([saved()]) });
    const { el } = await panel(api, { auth: viewer, locale: "zh-Hant" });
    await waitFor(() => text(section(el) ?? el).includes("A second ICT fixture"), "the saved explanation");
    const s = section(el)!;
    expect(s.querySelector("h2")?.textContent).toBe(zhHant["analysis.title"]);
    expect(text(s.querySelector("time")!.parentElement!)).toBe(zhHant["analysis.saved.when"].replace("{time}", i18nFor("zh-Hant").dateTime("2026-11-01T06:00:46Z")));
    expect(button(s, zhHant["analysis.again"])).toBeUndefined();
    expect(button(s, zhHant["analysis.ask"])).toBeUndefined();
    expect([...s.querySelectorAll("button")].map((b) => b.getAttribute("aria-label"))).not.toContain(zhHant["analysis.proposeNamed"].replace("{name}", "Faster AOI"));
    expect(await violations()).toEqual([]);
  });

  it("is the form for an editor and nothing for a viewer when the pair has none", async () => {
    const asked = await panel(fakeApi({ [`GET ${listPath}`]: page([]) }));
    await waitFor(() => button(section(asked.el) ?? asked.el, en["analysis.ask"]) !== undefined, "the form");
    expect(text(section(asked.el)!)).toContain(en["analysis.intro"]);
    expect(await violations()).toEqual([]);
    const none = await panel(fakeApi({ [`GET ${listPath}`]: page([]) }), { auth: viewer });
    await settle();
    expect(section(none.el)).toBeNull();
  });

  it("says it is looking while the list is on its way", async () => {
    let answer!: (r: Response) => void;
    const api = fakeApi({ [`GET ${listPath}`]: new Promise<Response>((r) => (answer = r)) });
    const { el } = await panel(api);
    const s = section(el)!;
    expect(s.querySelector('[role="status"]')?.textContent).toBe(en["analysis.saved.loading"]);
    expect(button(s, en["analysis.ask"])).toBeUndefined();
    expect(await violations()).toEqual([]);
    await act(async () => answer(page([saved()])));
    await waitFor(() => text(section(el)!).includes("A second ICT fixture"), "the saved explanation");
    expect(section(el)!.querySelector('[role="status"]')?.textContent).toBe("");
  });

  it("does not show an analysis of other runs as this pair's (as the address's analysis is not)", async () => {
    const other = saved({ baseline_run_id: "sim_01M4YDQK00FGZZWH0PP15XPN99" });
    const { el } = await panel(fakeApi({ [`GET ${listPath}`]: page([other]) }));
    await waitFor(() => button(section(el) ?? el, en["analysis.ask"]) !== undefined, "the form");
    expect(text(section(el)!)).not.toContain("A second ICT fixture");
  });

  it("says when the list fails and finds the saved explanation on a retry", async () => {
    const api = fakeApi({ [`GET ${listPath}`]: [json(503, { error: { code: "SERVICE_UNAVAILABLE", message: "x", request_id: REQ } }), page([saved()])] });
    const { el } = await panel(api);
    await waitFor(() => section(el)?.querySelector('[role="alert"]') != null, "the problem");
    expect(text(section(el)!)).not.toContain("A second ICT fixture");
    expect(await violations()).toEqual([]);
    await press(button(section(el)!, en.retry));
    await waitFor(() => text(section(el)!).includes("A second ICT fixture"), "the saved explanation");
    expect(section(el)!.querySelector('[role="alert"]')).toBeNull();
  });

  // H-073 review (WCAG 3.1.2): an explanation is written in the language it was asked in.
  it("prefers the newest one in the reader's language, else shows the newest", async () => {
    // Newest first: the Chinese one, then the English one.
    const both = () => fakeApi({ [`GET ${listPath}`]: page([inChinese(), saved()]) });
    const english = await panel(both());
    await waitFor(() => text(section(english.el) ?? english.el).includes("A second ICT fixture"), "the English explanation");
    expect(text(section(english.el)!)).not.toContain("第二台");
    unmountAll();
    const chinese = await panel(both(), { locale: "zh-Hant" });
    await waitFor(() => text(section(chinese.el) ?? chinese.el).includes("第二台 ICT 治具"), "the Chinese explanation");
    expect(text(section(chinese.el)!)).not.toContain("A second ICT fixture");
    expect(section(chinese.el)!.querySelector("[lang]")).toBeNull();
    unmountAll();
    // None in the reader's language: the newest, whatever its language.
    const only = await panel(fakeApi({ [`GET ${listPath}`]: page([inChinese()]) }));
    await waitFor(() => text(section(only.el) ?? only.el).includes("第二台 ICT 治具"), "the newest explanation");
  });

  it("marks an explanation in another language than the page's with its language, and only the AI's text", async () => {
    const { el } = await panel(fakeApi({ [`GET ${listPath}`]: page([inChinese()]) }));
    await waitFor(() => text(section(el) ?? el).includes("第二台 ICT 治具"), "the explanation");
    const s = section(el)!;
    const marked = [...s.querySelectorAll("[lang]")];
    expect(new Set(marked.map((m) => m.getAttribute("lang")))).toEqual(new Set(["zh-Hant"]));
    // Each piece the AI wrote is inside an element marked zh-Hant.
    const written = ["第二台 ICT 治具縮短了前置時間。", "一台 ICT 治具讓板子等待。", "ICT 前的等待變少。", "ICT 是最忙的工作站。", "測試時間維持模型設定。", "5 次重複仍有一些變異。", "更快的 AOI", "AOI 可能成為下一個限制。"];
    for (const w of written) expect(marked.some((m) => text(m).trim() === w), w).toBe(true);
    // The page's own words stay in the page's language: no heading, label or note is marked.
    for (const m of marked) expect(m.querySelector("h2, h3, summary, [data-kind], button")).toBeNull();
    expect([...s.querySelectorAll("h2, h3, summary, [data-kind]")].some((h) => h.closest('[lang="zh-Hant"]'))).toBe(false);
    expect(text(s)).toContain(en["analysis.baseline"].replace("{summary}", "一台 ICT 治具讓板子等待。"));
    expect(await violations()).toEqual([]);
  });

  it("marks nothing when the explanation is in the page's language", async () => {
    const { el } = await panel(fakeApi({ [`GET ${listPath}`]: page([saved()]) }));
    await waitFor(() => text(section(el) ?? el).includes("A second ICT fixture"), "the explanation");
    expect(section(el)!.querySelector("[lang]")).toBeNull();
  });

  it("follows the analysis in the address instead, without reading the list", async () => {
    const api = fakeApi({ [`GET ${v1}/analyses/${ANA}`]: json(200, saved()) });
    const { el } = await panel(api, { initial: ANA });
    await waitFor(() => text(section(el) ?? el).includes("A second ICT fixture"), "the explanation");
    expect(api.calls.map((c) => c.path)).toEqual([`${v1}/analyses/${ANA}`]);
    expect(section(el)!.querySelector("time")).toBeNull();
  });
});
