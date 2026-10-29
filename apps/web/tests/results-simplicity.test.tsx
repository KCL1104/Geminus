// @vitest-environment happy-dom
// T-032 / H-027 ("簡潔"; "只保留所有應該要包含的按鈕功能"): the scenario page and
// the runs-and-results page keep only the controls each state needs, the
// same rules as tests/simplicity.test.tsx: at most one primary action, no
// disabled-forever control, no two buttons (or links) with one name and no
// two links to one place, each view together with the app shell. The app
// mounts no session yet (app/layout.tsx), so these signed-in views are
// checked here, with the app's client over a fake API.
import { act, type ReactNode, Suspense } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider } from "../lib/auth/session";
import type { Comparison, Run } from "../lib/contracts";
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
import { mount, press, settle } from "./ui-harness";

const jar = vi.hoisted(() => ({ locale: undefined as string | undefined }));
vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ push: () => {}, refresh: () => {} }) }));
vi.mock("next/headers", () => ({
  cookies: async () => ({ get: (name: string) => (name === "geminus_locale" && jar.locale !== undefined ? { name, value: jar.locale } : undefined) }),
  // The root layout reads the request's nonce (T-057); none here.
  headers: async () => new Headers(),
}));
vi.setConfig({ testTimeout: 120_000 });
beforeEach(() => {
  jar.locale = undefined;
});

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const editor: Auth = { session: { organizationId: ORG }, role: "editor" };
const viewer: Auth = { session: { organizationId: ORG }, role: "viewer" };
const catalogs = { en, "zh-Hant": zhHant } as const;
const locales = Object.keys(catalogs) as Locale[];
const primaries = (el: ParentNode) => el.querySelectorAll('[data-variant="primary"]').length;
const disabled = (el: ParentNode) => el.querySelectorAll("[disabled], [aria-disabled=true], [data-disabled]").length;

type Control = { role: "button" | "link"; name: string; href: string | null };

function accessibleName(el: Element): string {
  const ids = el.getAttribute("aria-labelledby");
  const name = ids ? ids.split(/\s+/).map((id) => el.ownerDocument.getElementById(id)?.textContent ?? "").join(" ") : (el.getAttribute("aria-label") ?? el.textContent ?? "");
  return name.replace(/\s+/g, " ").trim();
}

function controls(root: ParentNode): Control[] {
  const out: Control[] = [];
  for (const el of root.querySelectorAll("a[href], [role=link], button, [role=button]")) {
    if (el.closest("[hidden], [aria-hidden=true], [inert]")) continue;
    const role = el.getAttribute("role") ?? (el.tagName === "A" ? "link" : "button");
    if (role !== "button" && role !== "link") continue;
    const href = el.getAttribute("href");
    out.push({ role, name: accessibleName(el), href: href === null || href.startsWith("#") ? null : href });
  }
  return out;
}

function duplicates(cs: Control[]): string[] {
  const keys = [...cs.map((c) => `${c.role} "${c.name}"`), ...cs.flatMap((c) => (c.href === null ? [] : [`link to ${c.href}`]))];
  return [...new Set(keys.filter((k, i) => keys.indexOf(k) !== i))].sort();
}

async function shellControls(locale: Locale): Promise<Control[]> {
  jar.locale = locale;
  const { default: RootLayout } = await import("../app/layout");
  const html = renderToStaticMarkup(await RootLayout({ children: null }));
  const c = controls(new DOMParser().parseFromString(`<!doctype html>${html}`, "text/html"));
  expect(c.map((x) => x.role), "the shell's controls were found").toContain("link");
  return c;
}

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const unavailable = () => json(503, { error: { code: "SERVICE_UNAVAILABLE", message: "down", request_id: REQ } });

function client(table: Record<string, () => Response>) {
  return createApiClient({
    session: () => editor.session,
    fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://app.test").pathname;
      const answer = table[`${init?.method ?? "GET"} ${path}`];
      if (!answer) throw new Error(`no fake answer for ${init?.method ?? "GET"} ${path}`);
      return answer();
    }) as typeof fetch,
  });
}

const v1 = "/api/v1";
const scenarioAnswers = {
  [`GET ${v1}/scenarios/${boardScenario.id}`]: () => json(200, boardScenario),
  [`GET ${v1}/model-versions/${boardVersion.id}`]: () => json(200, boardVersion),
  [`GET ${v1}/models/${boardScenario.model_id}/simulations`]: () => json(200, { items: [boardBaselineRun(), boardScenarioRun()], next_cursor: null }),
};
const results = (comparison: Comparison = boardComparison) => ({
  [`GET ${v1}/simulation-comparisons`]: () => json(200, comparison),
  [`GET ${v1}/simulations/${BASELINE_RUN}/results`]: () => json(200, boardResults.baseline),
  [`GET ${v1}/simulations/${SCENARIO_RUN}/results`]: () => json(200, boardResults.scenario),
  [`GET ${v1}/simulations/${BASELINE_RUN}/trace`]: () => json(200, boardTraces.baseline),
  [`GET ${v1}/simulations/${SCENARIO_RUN}/trace`]: () => json(200, boardTraces.scenario),
});
const runs = (b: Run, s: Run) => ({ [`GET ${v1}/simulations/${BASELINE_RUN}`]: () => json(200, b), [`GET ${v1}/simulations/${SCENARIO_RUN}`]: () => json(200, s) });
const progress = (revision: number, done: number) => ({ completed_replications: done, total_replications: 5, events_processed: 0, revision });
const failedRun = boardScenarioRun({ status: "failed", progress: progress(4, 2), error: { error: { code: "INTERNAL_ERROR", message: "x", request_id: REQ } } });
const incomparable: Comparison = {
  ...boardComparison, comparable: false, incomparable_reasons: ["ENGINE_VERSION_MISMATCH", "SEED_MISMATCH"],
  metrics: boardComparison.metrics.map((m) => ({ ...m, absolute_delta: null, relative_delta: null, status: "incomparable", reason: "RUNS_NOT_COMPARABLE" })),
};

async function render(node: ReactNode, auth: Auth, table: Record<string, () => Response>, locale: Locale, ready: (el: HTMLElement) => boolean) {
  const el = await mount(
    <AuthProvider value={auth}>
      <ApiProvider client={client(table)}>
        <Suspense fallback={null}>{node}</Suspense>
      </ApiProvider>
    </AuthProvider>,
    locale,
  );
  const until = Date.now() + 5000;
  while (!ready(el)) {
    if (Date.now() > until) throw new Error("the view did not render");
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
  await settle();
  return el;
}

async function scenarioPage() {
  const { default: ScenarioPage } = await import("../app/scenarios/[scenarioId]/page");
  return <ScenarioPage params={Promise.resolve({ scenarioId: boardScenario.id })} />;
}

async function resultsView() {
  const { RunResults } = await import("../features/results/run-results");
  return <RunResults scenarioId={boardScenario.id} baselineId={BASELINE_RUN} scenarioRunId={SCENARIO_RUN} pollMs={60_000} />;
}

const has = (sel: string) => (el: HTMLElement) => el.querySelector(sel) !== null;
const says = (text: string) => (el: HTMLElement) => (el.textContent ?? "").includes(text);

for (const locale of locales) {
  const t = catalogs[locale];
  describe(`${locale}: the scenario page and the results keep only the controls they need (H-027)`, () => {
    const states: [string, () => Promise<ReactNode>, Auth, Record<string, () => Response>, (el: HTMLElement) => boolean, string[]][] = [
      // H-042: the way back to the model is the rail; the shell's Projects link is always there.
      ["the scenario, for an editor", scenarioPage, editor, scenarioAnswers, has("form"), [t["run.start"], t["nav.projects"]]],
      ["the scenario, for a viewer", scenarioPage, viewer, scenarioAnswers, says(t["run.readOnly.viewer"]), [t["nav.projects"]]],
      ["the scenario, failed to load", scenarioPage, editor, { [`GET ${v1}/scenarios/${boardScenario.id}`]: unavailable }, has("[role=alert]"), [t.retry]],
      ["both runs queued", resultsView, editor, { ...scenarioAnswers, ...runs(boardBaselineRun({ status: "queued", progress: progress(1, 0) }), boardScenarioRun({ status: "queued", progress: progress(1, 0) })) }, says(t["runs.status.queued"]), [t["runs.cancel"], t["runs.back"]]],
      ["both running", resultsView, editor, { ...scenarioAnswers, ...runs(boardBaselineRun({ status: "running", progress: progress(2, 1) }), boardScenarioRun({ status: "running", progress: progress(2, 3) })) }, has("[role=progressbar]"), [t["runs.cancel"]]],
      ["running, for a viewer", resultsView, viewer, { ...scenarioAnswers, ...runs(boardBaselineRun({ status: "running", progress: progress(2, 1) }), boardScenarioRun({ status: "queued", progress: progress(1, 0) })) }, has("[role=progressbar]"), [t["runs.back"]]],
      ["one run failed", resultsView, editor, { ...scenarioAnswers, ...runs(boardBaselineRun(), failedRun) }, says(t["runs.failed.scenario"]), [t["runs.back"]]],
      ["both cancelled", resultsView, editor, { ...scenarioAnswers, ...runs(boardBaselineRun({ status: "cancelled" }), boardScenarioRun({ status: "cancelled" })) }, says(t["runs.noComparison"]), [t["runs.back"]]],
      ["the results", resultsView, editor, { ...scenarioAnswers, ...runs(boardBaselineRun(), boardScenarioRun()), ...results() }, has("svg[role=img]"), [t["runs.back"]]],
      ["results that cannot be compared", resultsView, editor, { ...scenarioAnswers, ...runs(boardBaselineRun(), boardScenarioRun()), ...results(incomparable) }, has("table"), [t["runs.back"]]],
      ["a failed poll", resultsView, editor, { ...scenarioAnswers, [`GET ${v1}/simulations/${BASELINE_RUN}`]: unavailable, [`GET ${v1}/simulations/${SCENARIO_RUN}`]: unavailable }, has("[role=alert]"), [t.retry]],
    ];

    it.each(states)("%s, inside the app shell", async (_, page, auth, table, ready, offered) => {
      const el = await render(await page(), auth, table, locale, ready);
      const c = [...(await shellControls(locale)), ...controls(el)];
      // What the state offers is really there, so the rule is not vacuous.
      expect(c.map((x) => x.name)).toEqual(expect.arrayContaining(offered));
      expect(duplicates(c)).toEqual([]);
      expect(primaries(el), "primary actions").toBeLessThanOrEqual(1);
      expect(disabled(el), "disabled controls").toBe(0);
    });
  });
}

describe("a cancel pressed twice is sent once per run (no duplicate request from one control)", () => {
  it("ignores a second press while the first is being answered", async () => {
    const posts: string[] = [];
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const table: Record<string, () => Response | Promise<Response>> = {
      ...scenarioAnswers,
      ...runs(boardBaselineRun(), boardScenarioRun({ status: "running", progress: progress(2, 1) })),
    };
    const api = createApiClient({
      session: () => editor.session,
      fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = new URL(String(input), "http://app.test").pathname;
        if (init?.method === "POST") {
          posts.push(path);
          await gate;
          return json(200, boardScenarioRun({ status: "running", cancellation_requested: true, progress: progress(3, 1) }));
        }
        return table[`GET ${path}`]!();
      }) as typeof fetch,
    });
    const el = await mount(
      <AuthProvider value={editor}>
        <ApiProvider client={api}>{await resultsView()}</ApiProvider>
      </AuthProvider>,
    );
    const cancel = () => [...el.querySelectorAll("button")].find((b) => b.textContent === en["runs.cancel"]);
    const until = Date.now() + 5000;
    while (!cancel() && Date.now() < until) await act(async () => new Promise((r) => setTimeout(r, 5)));
    await press(cancel());
    await press(cancel());
    release();
    await settle();
    expect(posts).toEqual([`${v1}/simulations/${SCENARIO_RUN}/cancel`]);
  });
});
