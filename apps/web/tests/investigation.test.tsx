// @vitest-environment happy-dom
// H-061 A8 (PROPOSED, behind NEXT_PUBLIC_GEMINUS_INVESTIGATIONS): the
// investigation page at /investigations/<id>. It reads getInvestigation
// every 2 s while the investigation runs and drives nothing; its header
// names the planner, the version, the objective, the budget and the clock
// (H-063: it keeps going after the page closes); the steps are the ledger's
// rows in words (a step still pending when it ended was not carried out),
// the AI's text shown as text, labelled and isolated; the candidates carry
// only the server's figures; the outcome gives the reason in words, the
// AI's claim as a claim and the server's closing line; Stop is for the
// requester or an owner, asked first. Then the labels on what the AI made
// (scenarios, runs, an applied proposal, the draft, the version), the rail,
// the review page's states and both catalogs' words for every code.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthBootstrap } from "../app/_components/auth-bootstrap";
import { AppRail, RailModel, RailProvider } from "../app/_components/rail";
import { SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { InvestigationPreview } from "../app/ui-gallery/investigation/preview";
import { HOSTILE_IDEA, HOSTILE_NOTE, IDEA, NOTE, OTHER_USER, sampleInvestigation, sampleRuns, type SampleState, states } from "../app/ui-gallery/investigation/samples";
import { createInvestigationStandIn, type InvestigationStandIn, type InvestigationStandInOptions } from "../app/ui-gallery/investigation/stand-in";
import { USER } from "../app/ui-gallery/proposal/samples";
import { createProposalStandIn } from "../app/ui-gallery/proposal/stand-in";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { InvestigationRoute } from "../features/investigations/investigation-view";
import { ModelView } from "../features/model-editor/model-view";
import { RunsPage } from "../features/models/model-lists";
import { ScenarioList } from "../features/simulations/scenario-list";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider, useAuth } from "../lib/auth/session";
import type { Investigation, Run, Scenario } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator, formatTime } from "../lib/i18n/translate";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { mount, press, settle, violations } from "./ui-harness";

const nav = vi.hoisted(() => ({ pathname: "/projects", push: vi.fn() }));
vi.mock("next/navigation", async (orig) => ({
  ...(await orig<typeof import("next/navigation")>()),
  usePathname: () => nav.pathname,
  useSearchParams: () => new URLSearchParams(),
  useRouter: () => ({ refresh: () => {}, push: nav.push }),
}));
vi.setConfig({ testTimeout: 60_000 });
afterEach(() => vi.unstubAllEnvs());

const t = createTranslator("en");
const fixture = <T,>(name: string): T => JSON.parse(readFileSync(join(process.cwd(), `../../testdata/contracts/${name}`), "utf8")) as T;
const schema = (name: string) => JSON.parse(readFileSync(join(process.cwd(), `../../schemas/${name}`), "utf8")) as { $defs: Record<string, { enum?: string[]; properties?: Record<string, { enum?: string[]; properties?: Record<string, { enum?: string[] }> }> }> };

// A fixed clock: the samples were made at 06:00:00 on 2026-11-01, with a deadline 20 minutes later.
const NOW = Date.parse("2026-11-01T06:02:00Z");

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
const buttons = (el: ParentNode) => [...el.querySelectorAll<HTMLButtonElement>("button")];
const findButton = (el: ParentNode, name: string) => buttons(el).find((x) => (x.getAttribute("aria-label") ?? text(x)) === name);
const button = (el: ParentNode, name: string) => {
  const b = findButton(el, name);
  if (!b) throw new Error(`no button "${name}" among ${buttons(el).map((x) => text(x)).join(" | ")}`);
  return b;
};
/** The header band's facts as [label, value] pairs. */
const facts = (el: Element) => [...el.querySelectorAll("header dl > div")].map((d) => [text(d.querySelector("dt")), text(d.querySelector("dd"))]);
const fact = (el: Element, label: string) => facts(el).find(([l]) => l === label)?.[1];
const steps = (el: Element) => [...el.querySelectorAll<HTMLElement>("[data-steps] > li")];
const gets = (api: InvestigationStandIn) => api.calls.filter((c) => c.method === "GET" && c.path === `/api/v1/investigations/${api.investigationId}`);

type PageOpts = Omit<InvestigationStandInOptions, "caller"> & { caller?: InvestigationStandInOptions["caller"]; locale?: Locale; pollMs?: number };

async function page(opts: PageOpts) {
  const caller = opts.caller ?? { userId: USER, role: "editor" as const };
  const api = createInvestigationStandIn({ ...opts, caller });
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const auth: Auth = { session: { organizationId: api.organizationId }, role: caller.role, userId: caller.userId };
  const el = await mount(
    <AuthProvider value={auth}>
      <ApiProvider client={client}>
        <InvestigationRoute investigationId={api.investigationId} pollMs={opts.pollMs ?? 20} now={() => NOW} />
      </ApiProvider>
    </AuthProvider>,
    opts.locale ?? "en",
  );
  await settle();
  await waitFor(() => el.querySelector("h1, [role=alert]") !== null, "the investigation page");
  await settle();
  return { el, api };
}

describe("the investigation page: what the AI is doing (H-061 A8)", () => {
  it("names the planner, the version, the objective, the budget and the clock while it runs (the contract's running example)", async () => {
    const running = { ...fixture<Investigation>("valid-investigation-running.json"), model_id: SAMPLE_MODEL_ID };
    const { el } = await page({ investigation: running, caller: { userId: running.created_by, role: "editor" } });
    expect(text(el.querySelector("h1"))).toContain("Board assembly and test line (sample)");
    expect(fact(el, t("band.status"))).toBe(`${t("investigation.status.running")} · ${t("investigation.activity.waiting")}`);
    // H-061 §3 step 4: "Done by Muse Spark 1.3 (Meta) for you".
    expect(fact(el, t("investigation.doneBy"))).toBe(t("investigation.for.you", { model: "Muse Spark 1.3 (Meta)" }));
    expect(fact(el, t("investigation.version"))).toBe("1");
    expect(fact(el, t("investigation.objective"))).toBe(t("investigation.objective./metrics/lead_time_minutes/p50"));
    const budget = text(el.querySelector("[data-budget]"));
    expect(budget).toBe([
      t("investigation.used.what_ifs", { used: "1", cap: "3" }), t("investigation.used.runs", { used: "2", cap: "4" }), t("investigation.used.steps", { used: "3", cap: "14" }),
      t("investigation.left", { count: 18 }),
    ].join(" · "));
    // H-063: the background clock, in the reader's time.
    expect(text(el.querySelector("[data-clock]"))).toBe(t("investigation.clock", { time: formatTime("en", running.deadline_at) }));
    expect(await violations()).toEqual([]);
  });

  it("says who it is done for when the reader is not the requester, and the proposal model when it differs", async () => {
    const inv = { ...sampleInvestigation("running"), proposal_model: "muse-wonder-1.0" as const };
    const { el } = await page({ investigation: inv, caller: { userId: OTHER_USER, role: "editor" } });
    expect(fact(el, t("investigation.doneBy"))).toBe(t("investigation.for.other", { model: "Muse Spark 1.3 (Meta)" }));
    expect(fact(el, t("investigation.proposalsBy"))).toBe("Muse Wonder 1.0");
  });

  it("reads it again every poll while it runs, and stops reading once it ended; the outcome takes focus once and is announced", async () => {
    const running = sampleInvestigation("running");
    const finished = sampleInvestigation("finished");
    const { el, api } = await page({ investigation: running, next: [running, running, finished] });
    await waitFor(() => el.querySelector("[data-outcome]") !== null, "the outcome");
    const reads = gets(api).length;
    expect(reads).toBe(4);
    await act(async () => new Promise((r) => setTimeout(r, 120)));
    expect(gets(api).length, "no read after it ended").toBe(reads);
    const outcome = el.querySelector<HTMLElement>("[data-outcome]")!;
    expect(document.activeElement).toBe(outcome);
    expect([...el.querySelectorAll('p[role="status"]')].map((p) => text(p))).toContain(t("investigation.ended"));
    // Nothing is written by the page: every request was a read.
    expect(api.calls.every((c) => c.method === "GET")).toBe(true);
  });

  it("never reads again for an investigation that had already ended when the page opened", async () => {
    const { el, api } = await page({ investigation: sampleInvestigation("finished") });
    await act(async () => new Promise((r) => setTimeout(r, 120)));
    expect(gets(api)).toHaveLength(1);
    // Opened on an ended one, the outcome does not steal the focus.
    expect(document.activeElement).not.toBe(el.querySelector("[data-outcome]"));
    expect(el.querySelector("[data-clock]")).toBeNull();
  });

  it("lists the steps in words: what each did, its state, the AI's note and idea labelled, and links to what it made", async () => {
    const { el } = await page({ investigation: sampleInvestigation("running") });
    const [first, second, third] = steps(el) as [HTMLElement, HTMLElement, HTMLElement];
    expect(steps(el)).toHaveLength(3);
    expect(text(first.querySelector("h3"))).toBe(t("investigation.step", { n: "1", tool: t("investigation.tool.propose_what_if") }));
    expect(first.getAttribute("data-status")).toBe("done");
    expect(text(first)).toContain(t("investigation.stepStatus.done"));
    expect(text(first)).toContain(t("investigation.aiIdea"));
    expect(text(first)).toContain(IDEA);
    expect(text(first)).toContain(t("investigation.aiNote"));
    expect(text(first)).toContain(NOTE);
    // The AI's words are data: each in its own <bdi>.
    expect([...first.querySelectorAll("bdi[data-ai-text]")].map((b) => b.textContent)).toEqual([IDEA, NOTE]);
    expect(first.querySelector('a[href^="/proposals/aip_"]')).not.toBeNull();
    expect(second.querySelector('a[href^="/scenarios/scn_"]')).not.toBeNull();
    // Step 3 is carried out and its runs are going: the page says so and shows them.
    expect(third.getAttribute("data-status")).toBe("waiting");
    expect(text(third)).toContain(t("investigation.waiting.run_what_if"));
    // Its results are linked once, from the candidate table that lists the run (H-027: one link per place).
    expect(third.querySelector('a[href*="/results?baseline=sim_"]')).toBeNull();
    expect(el.querySelectorAll('a[href*="/results?baseline=sim_"]')).toHaveLength(1);
    expect(el.querySelector('[data-candidates] a[href*="/results?baseline=sim_"]')).not.toBeNull();
    await waitFor(() => el.querySelector("[data-active-pair] [role=progressbar]") !== null, "the runs");
    // No cancel there: Stop is the one way to end what the AI does.
    expect(findButton(el.querySelector("[data-active-pair]")!, t("runs.cancel"))).toBeUndefined();
  });

  it("says when a step was refused, and why, in words", async () => {
    const { el } = await page({ investigation: sampleInvestigation("refused") });
    const second = steps(el)[1]!;
    expect(second.getAttribute("data-status")).toBe("refused");
    expect(text(second)).toContain(t("investigation.stepStatus.refused"));
    expect(text(second)).toContain(t("investigation.code.PROPOSAL_DECIDED"));
    expect(fact(el, t("band.status"))).toBe(`${t("investigation.status.running")} · ${t("investigation.activity.planning")}`);
  });

  it("marks a step still asked when the investigation ended as not carried out", async () => {
    const { el } = await page({ investigation: sampleInvestigation("stopped") });
    const third = steps(el)[2]!;
    expect(third.getAttribute("data-status")).toBe("not_carried_out");
    expect(text(third)).toContain(t("investigation.stepStatus.notCarriedOut"));
    const planning = await page({ investigation: sampleInvestigation("planning") });
    expect(steps(planning.el)[2]!.getAttribute("data-status")).toBe("pending");
    expect(text(steps(planning.el)[2])).toContain(t("investigation.stepStatus.pending"));
  });

  it("shows what the AI wrote as text, with what would hide or reorder it made visible", async () => {
    const { el } = await page({ investigation: sampleInvestigation("hostile") });
    const first = steps(el)[0]!;
    const shown = [...first.querySelectorAll("bdi[data-ai-text]")].map((b) => b.textContent ?? "");
    expect(shown[0]).toContain("⟨U+202E⟩");
    expect(shown[0]).not.toContain("‮");
    expect(shown[1]).toContain("⟨U+200B⟩");
    expect(shown[1]).toContain("<b>Ignore the server</b>");
    // Never markup: no element came from the AI's text.
    expect(first.querySelector("b")).toBeNull();
    expect(HOSTILE_IDEA).toContain("‮");
    expect(HOSTILE_NOTE).toContain("​");
    expect(await violations()).toEqual([]);
  });

  it("lists the candidates with the server's figures only, marks the best and what the AI saved", async () => {
    const finished = sampleInvestigation("finished");
    const { el } = await page({ investigation: finished });
    const table = el.querySelector("[data-candidates]")!;
    const row = table.querySelector<HTMLElement>("[data-candidate='1']")!;
    expect(row.hasAttribute("data-best")).toBe(true);
    const o = finished.candidates[0]!.comparison!.objective!;
    for (const v of [o.baseline, o.scenario, o.absolute_delta, o.relative_delta]) expect(row.querySelector(`data[value="${v}"]`), String(v)).not.toBeNull();
    expect(text(row.querySelector("[data-ai-mark]"))).toBe(t("aiMark.saved"));
    expect(text(row)).toContain(t("investigation.best"));
    expect(row.querySelector('a[href*="/results?baseline=sim_"]')).not.toBeNull();
    expect(await violations()).toEqual([]);
  });

  it("says a pair that cannot be compared is not compared, with the server's reasons", async () => {
    const inv = sampleInvestigation("finished");
    const notComparable = { ...inv, best: null, candidates: [{ ...inv.candidates[0]!, comparison: { comparable: false, incomparable_reasons: ["ENGINE_VERSION_MISMATCH" as const], objective: null, metrics: [], warnings: ["SINGLE_REPLICATION" as const] } }],
      closing_line: { ...inv.closing_line!, key: "not_compared" as const, candidate_index: null, absolute_delta: null, relative_delta: null, metric_path: null, compared: 0 } };
    const { el } = await page({ investigation: notComparable });
    const row = el.querySelector("[data-candidate='1']")!;
    expect(text(row)).toContain(t("investigation.notCompared"));
    expect(text(row)).toContain(t("compare.incomparable.ENGINE_VERSION_MISMATCH"));
    expect(text(row)).toContain(t("compare.warning.SINGLE_REPLICATION"));
    expect(row.querySelector("data")).toBeNull();
    expect(text(el.querySelector("[data-outcome]"))).toContain(t("investigation.closing.not_compared"));
  });

  it("ends with the reason in words, the AI's claim as its claim, and the server's closing line", async () => {
    const finished = sampleInvestigation("finished");
    const { el } = await page({ investigation: finished });
    const outcome = el.querySelector<HTMLElement>("[data-outcome]")!;
    expect(outcome.getAttribute("data-reason")).toBe("finished_by_ai");
    expect(text(outcome)).toContain(t("investigation.reason.finished_by_ai"));
    const claim = outcome.querySelector("[data-claim]")!;
    expect(text(claim)).toContain(t("investigation.claim"));
    expect(text(claim)).toContain(t("investigation.outcome.improvement_found"));
    const line = text(outcome.querySelector("[data-closing]"));
    expect(line).toContain(t("investigation.closing.improved", { index: "1", metric: t("metric.leadTimeMedian"), delta: "−2.6 min", percent: "−6.1%" }));
    expect(text(outcome)).toContain(t("investigation.noSignificance"));
    expect(fact(el, t("band.status"))).toBe(t("investigation.status.finished"));
  });

  it("in edit mode, says the change is in the draft for a person to check and publish", async () => {
    const { el } = await page({ investigation: sampleInvestigation("edit") });
    const outcome = el.querySelector("[data-outcome]")!;
    expect(text(outcome)).toContain(t("investigation.reason.draft_written"));
    expect(text(outcome)).toContain(t("investigation.closing.draft_written", { revision: "4" }));
    // The draft is the rail's Canvas: the page does not link the model a second time (H-027).
    expect(el.querySelector('a[href^="/models/"]')).toBeNull();
    expect(text(el.querySelector("[data-budget]"))).toBe([t("investigation.used.changes", { used: "1", cap: "1" }), t("investigation.used.steps", { used: "2", cap: "14" })].join(" · "));
    expect(el.querySelector("[data-candidates]")).toBeNull();
  });

  // H-027 (simplicity.spec.ts in the browser): no two links to the same place, and none to the model (the rail's Canvas).
  it.each(states)("links no place twice in the %s sample", async (state) => {
    const { el } = await page({ investigation: sampleInvestigation(state), caller: { userId: USER, role: "editor" }, pollMs: 60_000 });
    const hrefs = [...el.querySelectorAll("a[href]")].map((a) => a.getAttribute("href")!);
    expect(hrefs.filter((h, i) => hrefs.indexOf(h) !== i)).toEqual([]);
    expect(hrefs.filter((h) => h.startsWith("/models/"))).toEqual([]);
  });

  it("is not found for an ID that does not exist or is another organization's, and refuses another investigation's answer", async () => {
    const missing = await page({ investigation: sampleInvestigation("running"), missing: true });
    expect(missing.el.querySelector('[data-problem="not_found"]')).not.toBeNull();
    const api = createInvestigationStandIn({ investigation: sampleInvestigation("running"), caller: { userId: USER, role: "editor" } });
    const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
    const el = await mount(
      <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
        <ApiProvider client={client}>
          <InvestigationRoute investigationId="inv_01M4YDQK00E1ECTR0N1C5V0099" now={() => NOW} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => el.querySelector("[role=alert]") !== null, "the problem");
    expect(el.querySelector("h1")).toBeNull();
  });
});

describe("stopping the AI (H-061 §13)", () => {
  it("lets the requester stop it, asked first; the page then shows it stopped, and what the AI made stays", async () => {
    const { el, api } = await page({ investigation: sampleInvestigation("running"), pollMs: 60_000 });
    const stop = button(el, t("investigation.stop"));
    expect(stop.getAttribute("data-variant")).toBe("secondary");
    await press(stop);
    await settle();
    const dialog = document.querySelector("[role=alertdialog]")!;
    expect(text(dialog)).toContain(t("investigation.stop.body"));
    expect(api.calls.some((c) => c.method === "POST")).toBe(false);
    await press(button(dialog, t("investigation.stop.confirm")));
    await waitFor(() => el.querySelector("[data-outcome]") !== null, "the stopped investigation");
    const post = api.calls.find((c) => c.method === "POST")!;
    expect(post.path).toBe(`/api/v1/investigations/${api.investigationId}/stop`);
    expect(post.body).toEqual({});
    expect(post.key).toMatch(/^[\x21-\x7e]{8,128}$/);
    expect(el.querySelector("[data-outcome]")!.getAttribute("data-reason")).toBe("stopped_by_person");
    expect(text(el.querySelector("[data-outcome]"))).toContain(t("investigation.reason.stopped_by_person"));
    expect(findButton(el, t("investigation.stop"))).toBeUndefined();
    expect(steps(el)).toHaveLength(3);
    expect(await violations()).toEqual([]);
  });

  it("is not stopped when the person cancels the question", async () => {
    const { el, api } = await page({ investigation: sampleInvestigation("running"), pollMs: 60_000 });
    await press(button(el, t("investigation.stop")));
    await settle();
    await press(button(document.querySelector("[role=alertdialog]")!, t("investigation.stop.cancel")));
    await settle();
    expect(api.calls.some((c) => c.method === "POST")).toBe(false);
    expect(findButton(el, t("investigation.stop"))).toBeDefined();
  });

  it("lets an owner who did not start it stop it, but not another editor or a viewer", async () => {
    const owner = await page({ investigation: sampleInvestigation("running"), caller: { userId: OTHER_USER, role: "owner" } });
    expect(findButton(owner.el, t("investigation.stop"))).toBeDefined();
    const editor = await page({ investigation: sampleInvestigation("running"), caller: { userId: OTHER_USER, role: "editor" } });
    expect(findButton(editor.el, t("investigation.stop"))).toBeUndefined();
    expect(text(editor.el)).toContain(t("investigation.watchOnly"));
    const viewer = await page({ investigation: sampleInvestigation("running"), caller: { userId: OTHER_USER, role: "viewer" } });
    expect(findButton(viewer.el, t("investigation.stop"))).toBeUndefined();
    expect(text(viewer.el)).toContain(t("investigation.watchOnly"));
    // An unknown user (the bootstrap has not said who) is not offered a stop either, unless an owner.
    const unknown = await mount(
      <AuthProvider value={{ session: { organizationId: owner.api.organizationId }, role: "editor" }}>
        <ApiProvider client={createApiClient({ session: () => ({ organizationId: owner.api.organizationId }), fetch: createInvestigationStandIn({ investigation: sampleInvestigation("running"), caller: { userId: USER, role: "editor" } }).fetch })}>
          <InvestigationRoute investigationId={sampleInvestigation("running").id} now={() => NOW} pollMs={60_000} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => unknown.querySelector("h1") !== null, "the page");
    expect(findButton(unknown, t("investigation.stop"))).toBeUndefined();
  });

  it("shows a refused stop, keeps the button, and a retry reuses the key", async () => {
    const { el, api } = await page({ investigation: sampleInvestigation("running"), pollMs: 60_000, refuseStop: { status: 503, code: "SERVICE_UNAVAILABLE", message: "try again" } });
    await press(button(el, t("investigation.stop")));
    await settle();
    await press(button(document.querySelector("[role=alertdialog]")!, t("investigation.stop.confirm")));
    await waitFor(() => el.querySelector("[role=alert]") !== null, "the refusal");
    await press(button(el, t("investigation.stop")));
    await settle();
    await press(button(document.querySelector("[role=alertdialog]")!, t("investigation.stop.confirm")));
    await waitFor(() => el.querySelector("[data-outcome]") !== null, "the stopped investigation");
    const posts = api.calls.filter((c) => c.method === "POST");
    expect(posts).toHaveLength(2);
    expect(posts[1]!.key).toBe(posts[0]!.key);
  });

  it("reads it again when it had already ended (409), and shows how it ended without an alarm", async () => {
    const running = sampleInvestigation("running");
    const finished = sampleInvestigation("finished");
    // The page opened while it ran; it ended before the stop arrived.
    const { el, api } = await page({ investigation: running, next: [finished], pollMs: 60_000 });
    await press(button(el, t("investigation.stop")));
    await settle();
    await press(button(document.querySelector("[role=alertdialog]")!, t("investigation.stop.confirm")));
    await waitFor(() => el.querySelector("[data-outcome]") !== null, "how it ended");
    expect(el.querySelector("[data-outcome]")!.getAttribute("data-reason")).toBe("finished_by_ai");
    expect(el.querySelector("[role=alert]")).toBeNull();
    expect(api.calls.filter((c) => c.method === "POST")).toHaveLength(1);
  });
});

describe("the labels on what the AI made (H-056 §13)", () => {
  const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
  const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQ } });
  const made = { investigation_id: sampleInvestigation("running").id, requested_by: USER, generated_by: { provider: "meta", model: "muse-spark-1.3" } };
  const answering = (table: Record<string, unknown>) =>
    createApiClient({
      session: () => ({ organizationId: ORG }),
      fetch: (async (input: RequestInfo | URL) => {
        const path = new URL(String(input), "http://app.test").pathname;
        if (!(path in table)) throw new Error(`no fake answer for ${path}`);
        return json(200, table[path]);
      }) as typeof fetch,
    });

  it("marks a scenario the AI saved in the scenarios list, and a person's not at all", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const version = { id: "mv_01M4YDQK00E1ECTR0N1C500001", version: 1 };
    const scenario = (id: string, over: Partial<Scenario>): Scenario => ({ id, organization_id: ORG, model_id: SAMPLE_MODEL_ID, base_version_id: version.id, name: "S", patch: [], patch_hash: `sha256:${"0".repeat(64)}`, source_proposal_id: null, created_by: USER, created_at: "2026-11-01T06:00:26Z", ...over });
    const ai = scenario("scn_01M4YDQK00E1ECTR0N1C550001", { name: "Second ICT tester", source_proposal_id: "aip_01M4YDQK00E1ECTR0N1C5A0001", made_by_ai: made });
    const person = scenario("scn_01M4YDQK00E1ECTR0N1C550002", { name: "Faster AOI" });
    const client = answering({ [`/api/v1/models/${SAMPLE_MODEL_ID}/versions`]: { items: [version], next_cursor: null }, [`/api/v1/model-versions/${version.id}/scenarios`]: { items: [ai, person], next_cursor: null } });
    const el = await mount(<ApiProvider client={client}><ScenarioList modelId={SAMPLE_MODEL_ID} latestVersionId={version.id} /></ApiProvider>);
    await waitFor(() => el.querySelectorAll("li").length === 2, "the list");
    const [first, second] = [...el.querySelectorAll("li")];
    expect(text(first!.querySelector("[data-ai-mark]"))).toBe(t("aiMark.saved"));
    expect(second!.querySelector("[data-ai-mark]")).toBeNull();
    expect(await violations()).toEqual([]);
  });

  it("marks a run the AI started in the runs list", async () => {
    const run = (over: Partial<Run>): Run => ({ ...(sampleInvestigationRuns()[0]!), ...over });
    const ai = run({});
    const person = run({ id: "sim_01M4YDQK00E1ECTR0N1C5S0099", made_by_ai: undefined, name: "Person's run" });
    const model = { id: SAMPLE_MODEL_ID, organization_id: ORG, project_id: "prj_01M4YDQK00E1ECTR0N1C500000", domain: "electronics_line", name: "Line",
      draft: { revision: 1, base_version_id: "mv_01M4YDQK00E1ECTR0N1C500001", out_of_date: false }, latest_version: { id: "mv_01M4YDQK00E1ECTR0N1C500001", version: 1 }, created_at: "2026-10-26T00:00:00Z" };
    const client = answering({ [`/api/v1/models/${SAMPLE_MODEL_ID}`]: model, [`/api/v1/models/${SAMPLE_MODEL_ID}/simulations`]: { items: [ai, person], next_cursor: null } });
    const el = await mount(<ApiProvider client={client}><RunsPage modelId={SAMPLE_MODEL_ID} /></ApiProvider>);
    await waitFor(() => el.querySelectorAll("li").length === 2, "the list");
    const [first, second] = [...el.querySelectorAll("li")];
    expect(text(first!.querySelector("[data-ai-mark]"))).toBe(t("aiMark.started"));
    expect(second!.querySelector("[data-ai-mark]")).toBeNull();
  });

  it("says on a proposal the AI acted on what it did, and on a ready AI proposal that a person may still decide first", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const INV = sampleInvestigation("running").id;
    const applied = createProposalStandIn({ sample: "scenario", proposal: { status: "applied", investigation: { id: INV, index: 1 }, scenario_id: "scn_01M4YDQK00E1ECTR0N1C550001" } });
    const el = await mount(
      <AuthProvider value={{ session: { organizationId: applied.organizationId }, role: "editor" }}>
        <ApiProvider client={createApiClient({ session: () => ({ organizationId: applied.organizationId }), fetch: applied.fetch })}>
          <ProposalRoute proposalId={applied.proposalId!} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => el.querySelector("[data-decided='applied']") !== null, "the applied outcome");
    const section = el.querySelector("[data-decided='applied']")!;
    expect(text(section)).toContain(t("proposal.applied.scenario"));
    expect(section.querySelector('a[href="/scenarios/scn_01M4YDQK00E1ECTR0N1C550001"]')).not.toBeNull();
    expect(section.querySelector(`a[href="/investigations/${INV}"]`)).not.toBeNull();
    // Applied is the AI's own status: no Approve or Reject.
    expect(findButton(el, t("proposal.reject"))).toBeUndefined();
    expect(await violations()).toEqual([]);

    const ready = createProposalStandIn({ sample: "scenario", proposal: { investigation: { id: INV, index: 1 } } });
    const el2 = await mount(
      <AuthProvider value={{ session: { organizationId: ready.organizationId }, role: "editor" }}>
        <ApiProvider client={createApiClient({ session: () => ({ organizationId: ready.organizationId }), fetch: ready.fetch })}>
          <ProposalRoute proposalId={ready.proposalId!} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => el2.querySelector("[data-by-ai]") !== null, "the AI note");
    expect(text(el2.querySelector("[data-by-ai]"))).toContain(t("proposal.byAi.ready"));
    expect(findButton(el2, t("proposal.reject"))).toBeDefined();
  });

  it("calls out a draft change the AI wrote, repeats it in the publish dialog, and marks a version that includes one", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const api = createProposalStandIn({ sample: null, aiEdit: { investigation: sampleInvestigation("edit").id, proposal: "aip_01M4YDQK00E1ECTR0N1C5A0002" } });
    const el = await mount(
      <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
        <ApiProvider client={createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch })}>
          <ModelView modelId={SAMPLE_MODEL_ID} onProposal={() => {}} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => el.querySelector("[data-ai-edit]") !== null, "the draft callout");
    expect(text(el.querySelector("[data-ai-edit]"))).toContain(t("aiEdit.draft"));
    await waitFor(() => fact(el, t("aiEdit.versionLabel")) !== undefined, "the version fact");
    expect(fact(el, t("aiEdit.versionLabel"))).toBe(t("aiEdit.version", { version: "1" }));
    await press(button(el, t("editor.publish")));
    await settle();
    const dialog = document.querySelector("[role=alertdialog]");
    expect(text(dialog)).toContain(t("aiEdit.publish"));
    expect(await violations()).toEqual([]);
  });

  it("puts no label on a person's draft, version or scenario: their pages are as before", async () => {
    const api = createProposalStandIn({ sample: null });
    const el = await mount(
      <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
        <ApiProvider client={createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch })}>
          <ModelView modelId={SAMPLE_MODEL_ID} onProposal={() => {}} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => el.querySelector("h1") !== null, "the model page");
    await settle();
    expect(el.querySelector("[data-ai-edit]")).toBeNull();
    expect(fact(el, t("aiEdit.versionLabel"))).toBeUndefined();
  });
});

/** The running sample's pair, the what-if run first (each labelled made_by_ai). */
const sampleInvestigationRuns = (): Run[] => sampleRuns(sampleInvestigation("running")).reverse();

describe("the rail and who is signed in (H-061 A8)", () => {
  it("files the investigation page under the model's AI proposals", async () => {
    nav.pathname = `/investigations/${sampleInvestigation("running").id}`;
    const el = await mount(
      <RailProvider>
        <AppRail initialTheme="system" profile={null} />
        <RailModel id={SAMPLE_MODEL_ID} />
      </RailProvider>,
    );
    await settle();
    const current = [...el.querySelectorAll("nav a")].find((a) => a.getAttribute("aria-current") === "page");
    expect(current?.getAttribute("href")).toBe(`/models/${SAMPLE_MODEL_ID}/proposals`);
  });

  it("knows the signed-in user's ID from /me, so the page can tell the requester", async () => {
    const ORG = "org_01M4YDQK004J6EG1BW3SRYPDGD";
    globalThis.fetch = (async () =>
      new Response(JSON.stringify({ user_id: USER, memberships: [{ organization_id: ORG, organization_name: "Line A", role: "editor" }], truncated: false }), { status: 200, headers: { "Content-Type": "application/json" } })) as typeof fetch;
    let seen: string | null | undefined;
    function Probe() {
      seen = useAuth().userId;
      return null;
    }
    await mount(
      <AuthBootstrap signedIn organization={null} navigate={() => {}}>
        <Probe />
      </AuthBootstrap>,
    );
    await waitFor(() => seen === USER, "the user ID");
  });
});

describe("the review page's states (/ui-gallery/investigation)", () => {
  it.each(states.flatMap((s) => (["en", "zh-Hant"] as const).map((l) => [s, l] as [SampleState, Locale])))("%s in %s renders with no axe violation", async (state, locale) => {
    const el = await mount(<InvestigationPreview state={state} />, locale);
    await waitFor(() => el.querySelector("h1") !== null, `the ${state} sample`);
    await settle();
    expect(el.querySelector("[role=alert]")).toBeNull();
    expect(await violations()).toEqual([]);
  });
});

describe("the catalogs (REQ-022): every code, reason, tool and outcome in both languages", () => {
  const common = schema("common.schema.json");
  const investigator = schema("investigator.schema.json");
  const investigation = schema("investigation.schema.json");
  const groups: [string, string[]][] = [
    ["investigation.reason", common.$defs.InvestigationReason!.enum!],
    ["investigation.code", investigator.$defs.InvestigationToolCode!.enum!],
    ["investigation.outcome", investigator.$defs.AiOutcome!.enum!],
    ["investigation.tool", [...investigator.$defs.InvestigatorTool!.enum!, "invalid_call"]],
    ["investigation.activity", investigation.$defs.InvestigationActivity!.properties!.state!.enum!],
    ["investigation.status", investigation.$defs.Investigation!.properties!.status!.enum!],
    ["investigation.closing", investigation.$defs.InvestigationClosingLine!.properties!.key!.enum!],
    ["investigation.objective", investigation.$defs.InvestigationObjectiveMetric!.enum!],
    ["investigation.source", investigation.$defs.InvestigationCandidate!.properties!.source!.enum!],
  ];

  it.each(groups)("%s has a message for each value", (prefix, values) => {
    expect(values.length).toBeGreaterThan(1);
    for (const v of values) {
      expect(Object.hasOwn(en, `${prefix}.${v}`), `${prefix}.${v}`).toBe(true);
      expect(Object.hasOwn(zhHant, `${prefix}.${v}`), `${prefix}.${v}`).toBe(true);
    }
  });

  it("has no message for a value the contract does not have", () => {
    for (const [prefix, values] of groups) {
      const keys = Object.keys(en).filter((k) => k.startsWith(`${prefix}.`) && !k.slice(prefix.length + 1).includes("."));
      const extra = keys.map((k) => k.slice(prefix.length + 1)).filter((v) => !values.includes(v) && !v.startsWith("/"));
      expect(extra, prefix).toEqual([]);
    }
  });

  it("words the H-063 clock as the owner approved it", () => {
    expect(en["investigation.clock"]).toBe("It keeps going if you close this page, until {time}. You can stop it at any time.");
    expect(zhHant["investigation.clock"]).toBe("關掉這個頁面它也會繼續，直到 {time}。你隨時可以停止它。");
  });
});
