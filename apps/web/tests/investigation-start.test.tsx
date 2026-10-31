// @vitest-environment happy-dom
// H-061 A8 (PROPOSED, behind NEXT_PUBLIC_GEMINUS_INVESTIGATIONS): starting
// an AI investigation. The client's four operations at the contract's
// method and path, refusing what the contract refuses before anything is
// sent; the build flags (off by default: nothing changes); the request
// bodies (run settings within H-060's caps, the planner only when Muse
// Wonder is chosen and offered); the model page's "Let the AI act on it"
// (entry A) and the explanation's "Let the AI test these" (entry B).
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act, useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { createProposalStandIn, type ProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { BASELINE_RUN, SCENARIO_RUN, boardBaselineRun, boardComparison, boardResults, boardScenario, boardScenarioRun, boardTraces, boardVersion } from "../features/results/fixtures/board-line";
import { investigationsOffered, offeredPlanners, plannerField } from "../features/investigations/flags";
import { baselineFits, fromAnalysis, fromRequest, OBJECTIVES, parseActRunForm } from "../features/investigations/request";
import { ModelView } from "../features/model-editor/model-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider, type Role } from "../lib/auth/session";
import type { AnalysisRecord, Investigation, InvestigationRequest, Run } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { mount, press, settle, violations } from "./ui-harness";

const nav = vi.hoisted(() => ({ push: vi.fn() }));
vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ push: nav.push, refresh: () => {} }) }));
vi.setConfig({ testTimeout: 60_000 });
beforeEach(() => nav.push.mockReset());
afterEach(() => vi.unstubAllEnvs());

const t = createTranslator("en");
const fixture = <T,>(name: string): T => JSON.parse(readFileSync(join(process.cwd(), `../../testdata/contracts/${name}`), "utf8")) as T;
const operations = (JSON.parse(readFileSync(join(process.cwd(), "../../planning/api-operations.json"), "utf8")) as { operations: { operation_id: string; method: string; path: string }[] }).operations;
const op = (id: string) => operations.find((o) => o.operation_id === id)!;

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const MODEL = "mdl_01M4YDQK004AGVNEGJQJ1NMM0N";
const INV = "inv_01M4YDQK00B8S5WQXJ3TQ7ZK2A";

type Call = { url: URL; init: RequestInit };
function fakeFetch(...responses: Response[]) {
  const calls: Call[] = [];
  const fn = async (input: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: new URL(String(input), "https://app.test"), init: init ?? {} });
    const next = responses.shift();
    if (next === undefined) throw new Error("unexpected request");
    return next;
  };
  return { fetch: fn as typeof fetch, calls };
}
const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQ, ...headers } });
const client = (f: ReturnType<typeof fakeFetch>) => createApiClient({ session: () => ({ organizationId: ORG }), fetch: f.fetch });
const headers = (c: Call) => c.init.headers as Record<string, string>;
/** The contract's path template with the call's IDs put back as {id}. */
const template = (c: Call, ...ids: string[]) => ids.reduce((p, id) => p.replace(id, "{id}"), c.url.pathname.replace(/^\/api/, ""));

describe("the investigation operations through the client (H-061 A8)", () => {
  it("starts one with its Idempotency-Key and the body as given, at createInvestigation's method and path", async () => {
    const accepted = fixture("valid-investigation-accepted.json");
    const body = fixture<InvestigationRequest>("valid-investigation-request.json");
    const f = fakeFetch(json(202, accepted));
    const r = await client(f).createInvestigation(MODEL, body, "key-inv-00001");
    expect(r.ok && r.data).toEqual(accepted);
    const c = f.calls[0]!;
    expect([c.init.method, template(c, MODEL)]).toEqual([op("createInvestigation").method, op("createInvestigation").path]);
    expect([headers(c)["Idempotency-Key"], headers(c)["Content-Type"], headers(c)["X-Organization-ID"]]).toEqual(["key-inv-00001", "application/json", ORG]);
    expect(JSON.parse(String(c.init.body))).toEqual(body);
  });

  it("starts entry B (from an analysis) the same way", async () => {
    const body = fixture<InvestigationRequest>("valid-investigation-request-from-analysis.json");
    const f = fakeFetch(json(202, { investigation_id: INV, status: "running" }));
    expect((await client(f).createInvestigation(MODEL, body, "key-inv-00002")).ok).toBe(true);
    expect(JSON.parse(String(f.calls[0]!.init.body))).toEqual(body);
  });

  it("lists, reads and stops at the contract's methods and paths; a stop sends {} with its key", async () => {
    const running = fixture<Investigation>("valid-investigation-running.json");
    const stopped = fixture<Investigation>("valid-investigation-stopped.json");
    const page = fixture("valid-investigations-page.json");
    const f = fakeFetch(json(200, page), json(200, running), json(200, stopped));
    const api = client(f);
    expect((await api.listInvestigations(MODEL)).ok).toBe(true);
    expect((await api.getInvestigation(INV)).ok && running.id).toBe(INV);
    const s = await api.stopInvestigation(INV, "key-stop-00001");
    expect(s.ok && s.data.status).toBe("stopped");
    const [list, get, stop] = f.calls as [Call, Call, Call];
    expect([list.init.method, template(list, MODEL)]).toEqual([op("listInvestigations").method, op("listInvestigations").path]);
    expect(list.url.searchParams.get("limit")).toBe("100");
    expect([get.init.method, template(get, INV)]).toEqual([op("getInvestigation").method, op("getInvestigation").path]);
    expect([stop.init.method, template(stop, INV)]).toEqual([op("stopInvestigation").method, op("stopInvestigation").path]);
    expect([headers(stop)["Idempotency-Key"], headers(stop)["Content-Type"], String(stop.init.body)]).toEqual(["key-stop-00001", "application/json", "{}"]);
  });

  it("refuses what the contract refuses before sending anything", async () => {
    const f = fakeFetch();
    const api = client(f);
    const entryA = fixture<InvestigationRequest>("valid-investigation-request.json");
    const notFound = async (p: Promise<{ ok: boolean; problem?: { kind: string } }>) => expect(((await p) as { problem: { kind: string } }).problem.kind).toBe("not_found");
    await notFound(api.createInvestigation("mdl_bad", entryA, "key-inv-00003"));
    await notFound(api.createInvestigation(MODEL, { ...entryA, base_version_id: "mv_bad" } as InvestigationRequest, "key-inv-00004"));
    await notFound(api.createInvestigation(MODEL, { from_analysis_id: "aip_01M4YDQK00B8S5WQXJ3TQ7ZK30", output_locale: "en" }, "key-inv-00005"));
    const bad = await api.createInvestigation(MODEL, entryA, "short");
    expect(!bad.ok && bad.problem.kind).toBe("invalid");
    await notFound(api.listInvestigations("prj_01M4YDQK00TA00000000000001"));
    await notFound(api.getInvestigation("aip_01M4YDQK00B8S5WQXJ3TQ7ZK30"));
    await notFound(api.getInvestigation("inv_not-a-ulid"));
    await notFound(api.stopInvestigation("inv_01m4ydqk00b8s5wqxj3tq7zk2a", "key-stop-00002"));
    const noKey = await api.stopInvestigation(INV, "");
    expect(!noKey.ok && noKey.problem.kind).toBe("invalid");
    expect(f.calls).toHaveLength(0);
  });

  it("hands the server's refusals over as given: 409 already running, 429 with Retry-After, 503 off", async () => {
    const running = { error: { code: "LIFECYCLE_CONFLICT", message: "x", details: [{ path: "", reason: "y", code: "INVESTIGATION_RUNNING" }], request_id: REQ } };
    const daily = { error: { code: "RATE_LIMITED", message: "x", details: [{ path: "", reason: "y", code: "INVESTIGATION_DAILY_LIMIT" }], request_id: REQ } };
    const off = { error: { code: "SERVICE_UNAVAILABLE", message: "x", details: [{ path: "", reason: "y", code: "INVESTIGATIONS_OFF" }], request_id: REQ } };
    const f = fakeFetch(json(409, running), json(429, daily, { "Retry-After": "3600" }), json(503, off));
    const api = client(f);
    const body = fixture<InvestigationRequest>("valid-investigation-request-minimal.json");
    const a = await api.createInvestigation(MODEL, body, "key-inv-00006");
    const b = await api.createInvestigation(MODEL, body, "key-inv-00007");
    const c = await api.createInvestigation(MODEL, body, "key-inv-00008");
    expect(!a.ok && [a.problem.code, a.problem.details[0]?.code]).toEqual(["LIFECYCLE_CONFLICT", "INVESTIGATION_RUNNING"]);
    expect(!b.ok && [b.problem.kind, b.problem.retryAfterSeconds]).toEqual(["rate_limited", 3600]);
    expect(!c.ok && [c.problem.kind, c.problem.details[0]?.code]).toEqual(["unavailable", "INVESTIGATIONS_OFF"]);
  });
});

describe("the build flags (H-061 A8, H-061 §14)", () => {
  it("offers nothing unless NEXT_PUBLIC_GEMINUS_INVESTIGATIONS is 1", () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", undefined);
    expect(investigationsOffered()).toBe(false);
    for (const v of ["", "0", "true", "yes", " 1"]) {
      vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", v);
      expect(investigationsOffered(), v).toBe(false);
    }
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    expect(investigationsOffered()).toBe(true);
  });

  it("offers Muse Wonder 1.0 as planner only with NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER=1, apart from H-051's flag", () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER", undefined);
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_OFFER_WONDER", "1");
    expect(offeredPlanners()).toEqual(["muse-spark-1.3"]);
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER", "1");
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_OFFER_WONDER", undefined);
    expect(offeredPlanners()).toEqual(["muse-spark-1.3", "muse-wonder-1.0"]);
    // Only Muse Wonder adds planner_model, so a request on the default is the minimal one.
    expect(plannerField("muse-spark-1.3")).toEqual({});
    expect(plannerField("muse-wonder-1.0")).toEqual({ planner_model: "muse-wonder-1.0" });
  });

  it("are fixed at build time even when unset, like H-051's", () => {
    const config = readFileSync(join(process.cwd(), "next.config.ts"), "utf8");
    expect(config).toContain('NEXT_PUBLIC_GEMINUS_INVESTIGATIONS: process.env.NEXT_PUBLIC_GEMINUS_INVESTIGATIONS ?? ""');
    expect(config).toContain('NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER: process.env.NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER ?? ""');
  });
});

describe("the request an investigation is started with (H-060's caps)", () => {
  const form = (over: Partial<Record<"horizon" | "warmup" | "replications" | "seed", string>> = {}) => ({ horizon: "10080", warmup: "0", replications: "5", seed: "1", ...over });

  it("takes run settings within the caps: at most 10 replications and 28 days in the model's unit", () => {
    const ok = parseActRunForm(form(), "minute");
    expect(ok).toEqual({ ok: true, config: { horizon: { value: 10080, unit: "minute" }, warmup: { value: 0, unit: "minute" }, replications: 5, seed: 1, trace_level: "sampled" } });
    expect(parseActRunForm(form({ replications: "10" }), "minute").ok).toBe(true);
    expect(parseActRunForm(form({ replications: "11" }), "minute")).toEqual({ ok: false, issues: { replications: "aiReplications" } });
    expect(parseActRunForm(form({ replications: "0" }), "minute")).toEqual({ ok: false, issues: { replications: "replications" } });
    for (const [unit, max] of [["day", 28], ["hour", 672], ["minute", 40320], ["second", 2419200]] as const) {
      expect(parseActRunForm(form({ horizon: String(max) }), unit).ok, `${max} ${unit}`).toBe(true);
      expect(parseActRunForm(form({ horizon: String(max + 1) }), unit), `${max + 1} ${unit}`).toEqual({ ok: false, issues: { horizon: "aiHorizon" } });
    }
    expect(parseActRunForm(form({ horizon: "x" }), "minute")).toEqual({ ok: false, issues: { horizon: "number" } });
    expect(parseActRunForm(form({ warmup: "10080" }), "minute")).toEqual({ ok: false, issues: { warmup: "warmup" } });
  });

  it("builds entry A as the contract's examples are, adding only what was chosen", () => {
    const parsed = parseActRunForm(form(), "minute");
    if (!parsed.ok) throw new Error("the default settings fit the caps");
    const config = parsed.config;
    const base = { base: "mv_01M4YDQK00FRYWV7GS7X1QCXZR", intent: "Find a change that lowers the median lead time without a new line.", locale: "en" as const };
    expect(fromRequest({ ...base, mode: "scenario", research: false, model: "muse-spark-1.3", planner: "muse-spark-1.3", config, baselineName: "Baseline for the AI", objective: OBJECTIVES[0]! })).toEqual({
      base_version_id: base.base, mode: "scenario", intent: base.intent, output_locale: "en", run_config: config, baseline_name: "Baseline for the AI", objective: { metric_path: "/metrics/lead_time_minutes/p50" },
    });
    // Research for the first what-if, Muse Wonder for proposals and as planner: each adds its member.
    expect(fromRequest({ ...base, mode: "scenario", research: true, model: "muse-wonder-1.0", planner: "muse-wonder-1.0", config, baselineName: "B", objective: "/metrics/throughput/mean" })).toEqual({
      base_version_id: base.base, mode: "scenario", intent: base.intent, output_locale: "en", research: true, model: "muse-wonder-1.0", planner_model: "muse-wonder-1.0", run_config: config, baseline_name: "B", objective: { metric_path: "/metrics/throughput/mean" },
    });
    // Edit mode carries no run settings, baseline or objective (the contract refuses them): the contract's own edit example.
    const edit = fixture<Extract<InvestigationRequest, { mode: string }>>("valid-investigation-request-edit.json");
    expect(fromRequest({ base: edit.base_version_id, intent: edit.intent, locale: "en", mode: "edit", research: false, model: "muse-spark-1.3", planner: "muse-spark-1.3", config, baselineName: "B", objective: OBJECTIVES[0]! })).toEqual(edit);
    expect(OBJECTIVES).toEqual(["/metrics/lead_time_minutes/p50", "/metrics/lead_time_minutes/mean", "/metrics/lead_time_minutes/p95", "/metrics/throughput/mean"]);
  });

  it("builds entry B from the analysis, its locale and only what was chosen", () => {
    expect(fromAnalysis({ analysis: "ana_01M4YDQK00B8S5WQXJ3TQ7ZK61", locale: "zh-Hant", model: "muse-spark-1.3", planner: "muse-spark-1.3" })).toEqual({ from_analysis_id: "ana_01M4YDQK00B8S5WQXJ3TQ7ZK61", output_locale: "zh-Hant" });
    expect(fromAnalysis({ analysis: "ana_01M4YDQK00B8S5WQXJ3TQ7ZK61", locale: "en", model: "muse-wonder-1.0", planner: "muse-wonder-1.0" })).toEqual({ from_analysis_id: "ana_01M4YDQK00B8S5WQXJ3TQ7ZK61", output_locale: "en", model: "muse-wonder-1.0", planner_model: "muse-wonder-1.0" });
  });

  it("tells whether an analysis's baseline can be reused: patch [] and settings within the caps", () => {
    const run = (over: Partial<Run>) => boardBaselineRun(over);
    expect(baselineFits(run({}))).toBe("fits");
    expect(baselineFits(run({ scenario_patch: [{ op: "replace", path: "/name", value: "x" }] }))).toBe("changed");
    expect(baselineFits(run({ scenario_id: boardScenario.id }))).toBe("changed");
    expect(baselineFits(run({ config: { ...run({}).config, replications: 11 } }))).toBe("limits");
    expect(baselineFits(run({ config: { ...run({}).config, horizon: { value: 29, unit: "day" } } }))).toBe("limits");
    expect(baselineFits(run({ config: { ...run({}).config, horizon: { value: 40321, unit: "minute" } } }))).toBe("limits");
  });
});

// The model page (entry A): Ask AI's "Let the AI act on it".
type Opts = Omit<ProposalStandInOptions, "sample"> & { role?: Role | null; locale?: Locale; api?: ProposalStandIn; userId?: string };

async function modelPage(opts: Opts = {}) {
  const api = opts.api ?? createProposalStandIn({ sample: null, ...opts });
  const c = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const started: string[] = [];
  const proposals: string[] = [];
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: opts.role === undefined ? "editor" : opts.role, userId: opts.userId ?? null }}>
      <ApiProvider client={c}>
        <ModelView modelId={SAMPLE_MODEL_ID} onProposal={(id) => proposals.push(id)} onInvestigation={(id) => started.push(id)} />
      </ApiProvider>
    </AuthProvider>,
    opts.locale ?? "en",
  );
  await settle();
  await waitFor(() => el.querySelector("h1, [role=alert]") !== null, "the model page");
  await settle();
  return { el, api, started, proposals };
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
const buttons = (el: ParentNode) => [...el.querySelectorAll<HTMLButtonElement>("button")];
const button = (el: ParentNode, name: string) => {
  const b = buttons(el).find((x) => (x.getAttribute("aria-label") ?? text(x)) === name);
  if (!b) throw new Error(`no button "${name}" among ${buttons(el).map((x) => text(x)).join(" | ")}`);
  return b;
};
const ask = (el: Element) => el.querySelector<HTMLElement>("[data-ask]")!;
const checkbox = (el: Element, label: string) => {
  const l = [...el.querySelectorAll("label")].find((x) => text(x) === label);
  return l?.querySelector<HTMLInputElement>("input[type=checkbox]") ?? null;
};
const fieldOf = (el: Element, label: string) => {
  const l = [...el.querySelectorAll("label")].find((x) => text(x) === label);
  if (!l) throw new Error(`no field ${label}`);
  return document.getElementById(l.htmlFor) as HTMLInputElement | HTMLTextAreaElement;
};
async function type(input: HTMLInputElement | HTMLTextAreaElement, value: string) {
  await act(async () => {
    input.focus();
    const proto = input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
}
async function tick(box: HTMLInputElement | null) {
  if (!box) throw new Error("no checkbox");
  await act(async () => box.click());
  await settle();
}
async function choose(el: Element, label: string, option: string) {
  const trigger = [...el.querySelectorAll("button")].find((b) => b.getAttribute("aria-labelledby")?.split(" ").some((id) => text(document.getElementById(id)) === label));
  if (!trigger) throw new Error(`no select ${label}`);
  await press(trigger);
  await settle();
  const o = [...document.querySelectorAll("[role=option]")].find((x) => text(x) === option);
  if (!o) throw new Error(`no option ${option}`);
  await press(o);
  await settle();
}
const starts = (api: ProposalStandIn) => api.calls.filter((c) => c.method === "POST" && c.path.endsWith("/investigations"));

describe("Ask AI on the model page: Let the AI act on it (entry A)", () => {
  it("is not there unless the build turns investigations on: the form sends a proposal as before", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", undefined);
    const { el, api, proposals } = await modelPage();
    const form = ask(el);
    expect(checkbox(form, t("investigation.act"))).toBeNull();
    expect(text(form)).not.toContain(t("investigation.act"));
    await type(fieldOf(form, t("ask.intent")), "Add a third ICT tester");
    await press(button(form, t("ask.send")));
    await waitFor(() => proposals.length > 0, "the proposal");
    expect(starts(api)).toHaveLength(0);
  });

  it("with the flag on, is a box off by default; unticked, the form still sends a proposal", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const { el, api, proposals, started } = await modelPage();
    const form = ask(el);
    const box = checkbox(form, t("investigation.act"));
    expect(box?.checked).toBe(false);
    // Off, nothing of the investigation shows: no run settings, no objective.
    expect(text(form)).not.toContain(t("investigation.objective"));
    await type(fieldOf(form, t("ask.intent")), "Add a third ICT tester");
    await press(button(form, t("ask.send")));
    await waitFor(() => proposals.length > 0, "the proposal");
    expect(starts(api)).toHaveLength(0);
    expect(started).toEqual([]);
    expect(await violations()).toEqual([]);
  });

  it("ticked in scenario mode: says the caps, takes run settings and an objective, and starts the investigation", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER", undefined);
    const { el, api, started } = await modelPage();
    const form = ask(el);
    await choose(form, t("ask.mode"), t("ask.mode.scenario"));
    await tick(checkbox(form, t("investigation.act")));
    expect(text(form)).toContain(t("investigation.actHint.scenario"));
    expect(text(form)).toContain(t("investigation.runs.hint"));
    // The photo button is not offered while the AI acts (H-056 §17: no photo inside an investigation).
    expect(form.querySelector('input[type="file"]')).toBeNull();
    // No planner choice unless Muse Wonder is offered as planner (H-061 §14).
    expect(text(form)).not.toContain(t("investigation.planner"));
    const send = button(form, t("investigation.start"));
    expect(send.getAttribute("data-variant")).toBe("ai");
    expect(el.querySelectorAll('[data-variant="primary"]')).toHaveLength(1);
    expect(await violations()).toEqual([]);

    await type(fieldOf(form, t("ask.intent")), "  Find a change that lowers the median lead time.  ");
    await choose(form, t("investigation.objective"), t("investigation.objective./metrics/throughput/mean"));
    await type(fieldOf(form, t("run.replications")), "11");
    await press(send);
    await settle();
    // Over the caps: said at the field, nothing sent.
    expect(text(form)).toContain(t("investigation.issue.aiReplications", { max: 10 }));
    expect(starts(api)).toHaveLength(0);
    await type(fieldOf(form, t("run.replications")), "10");
    await press(button(form, t("investigation.start")));
    await waitFor(() => started.length > 0, "the investigation");
    const [post] = starts(api);
    expect(post!.body).toEqual({
      base_version_id: api.baseVersionId, mode: "scenario", intent: "Find a change that lowers the median lead time.", output_locale: "en",
      run_config: { horizon: { value: 10080, unit: "minute" }, warmup: { value: 0, unit: "minute" }, replications: 10, seed: 1, trace_level: "sampled" },
      baseline_name: t("investigation.baselineName"), objective: { metric_path: "/metrics/throughput/mean" },
    });
    expect(post!.key).toMatch(/^[\x21-\x7e]{8,128}$/);
    expect(started).toEqual(["inv_01M4YDQK00E1ECTR0N1C5V0001"]);
  });

  it("ticked in edit mode: one change into a clean draft, no run settings or objective in the request", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const { el, api, started } = await modelPage({ locale: "zh-Hant" });
    const zh = createTranslator("zh-Hant");
    const form = ask(el);
    await tick(checkbox(form, zh("investigation.act")));
    expect(text(form)).toContain(zh("investigation.actHint.edit"));
    expect(text(form)).not.toContain(zh("investigation.objective"));
    expect(text(form)).not.toContain(zh("run.replications"));
    await type(fieldOf(form, zh("ask.intent")), "把兩台 ICT 測試機合成一站");
    await press(button(form, zh("investigation.start")));
    await waitFor(() => started.length > 0, "the investigation");
    expect(starts(api)[0]!.body).toEqual({ base_version_id: api.baseVersionId, mode: "edit", intent: "把兩台 ICT 測試機合成一站", output_locale: "zh-Hant" });
  });

  it("offers the planner choice only in a build that offers Muse Wonder as planner, and sends it only when chosen", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER", "1");
    const { el, api, started } = await modelPage();
    const form = ask(el);
    await tick(checkbox(form, t("investigation.act")));
    expect(text(form)).toContain(t("investigation.plannerHint.muse-spark-1.3"));
    await choose(form, t("investigation.planner"), t("investigation.planner.muse-wonder-1.0"));
    expect(text(form)).toContain(t("investigation.plannerHint.muse-wonder-1.0"));
    expect(await violations()).toEqual([]);
    await type(fieldOf(form, t("ask.intent")), "Merge the ICT testers");
    await press(button(form, t("investigation.start")));
    await waitFor(() => started.length > 0, "the investigation");
    expect(starts(api)[0]!.body).toEqual({ base_version_id: api.baseVersionId, mode: "edit", intent: "Merge the ICT testers", output_locale: "en", planner_model: "muse-wonder-1.0" });
  });

  it("says what to do when the server refuses, and a retry of the same request reuses its key", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const { el, api, started } = await modelPage({
      refuseNext: { status: 409, code: "LIFECYCLE_CONFLICT", message: "you already have an AI investigation running", details: [{ path: "", reason: "one running investigation per person per organization", code: "INVESTIGATION_RUNNING" }] },
    });
    const form = ask(el);
    await tick(checkbox(form, t("investigation.act")));
    await type(fieldOf(form, t("ask.intent")), "Merge the ICT testers");
    await press(button(form, t("investigation.start")));
    await waitFor(() => form.querySelector("[role=alert]") !== null, "the refusal");
    expect(text(form.querySelector("[role=alert]"))).toContain(t("investigation.refused.running"));
    expect(started).toEqual([]);
    await press(button(form, t("investigation.start")));
    await waitFor(() => started.length > 0, "the investigation");
    const [first, second] = starts(api);
    expect(second!.key).toBe(first!.key);
  });

  it.each([
    [429, "RATE_LIMITED", "INVESTIGATION_DAILY_LIMIT", "investigation.refused.daily"],
    [503, "SERVICE_UNAVAILABLE", "INVESTIGATIONS_OFF", "investigation.refused.off"],
    [503, "AI_POLICY_NOT_APPROVED", "AI_ROLE_NOT_APPROVED", "investigation.refused.notApproved"],
    [422, "REQUEST_VALIDATION_FAILED", "INVESTIGATION_PLANNER_NOT_OFFERED", "investigation.refused.planner"],
    [422, "REQUEST_VALIDATION_FAILED", "INVESTIGATION_RESEARCH_OVER_CEILING", "investigation.refused.research"],
  ] as const)("a %i %s (%s) says %s", async (status, code, detail, guidance) => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const { el } = await modelPage({ refuseNext: { status, code, message: "x", details: [{ path: "", reason: "y", code: detail }] } });
    const form = ask(el);
    await tick(checkbox(form, t("investigation.act")));
    await type(fieldOf(form, t("ask.intent")), "Merge the ICT testers");
    await press(button(form, t("investigation.start")));
    await waitFor(() => form.querySelector("[role=alert]") !== null, "the refusal");
    expect(text(form.querySelector("[role=alert]"))).toContain(t(guidance));
  });

  it("is not offered to a viewer, who sees no request at all", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const viewer = await modelPage({ role: "viewer" });
    expect(viewer.el.querySelector("[data-ask]")).toBeNull();
  });
});

// The explanation (entry B): "Let the AI test these".
describe("Let the AI test these, under an explanation's suggestions (entry B)", () => {
  const v1 = "/api/v1";
  const ANA = "ana_01M4YDQK00TA0000000000T030";
  const ME = "usr_01M4YDQK00TA0000000000ME01";
  const explained: AnalysisRecord = {
    id: ANA, organization_id: ORG, run_id: SCENARIO_RUN, baseline_run_id: BASELINE_RUN, status: "completed", created_at: "2026-10-28T09:00:00Z", error: null,
    analysis: {
      baseline: { summary: "One ICT fixture held boards back." }, scenario: { summary: "A second ICT fixture shortened the lead time." }, delta: [], bottleneck: [],
      assumptions: [], uncertainty: [], suggested_next_experiments: [{ name: "Faster AOI", hypothesis: "AOI may become the next constraint.", requires_confirmation: true }],
    },
  };
  const model = { id: boardScenario.model_id, organization_id: ORG, project_id: "prj_01M4YDQK00TA00000000000001", domain: "electronics_line", name: "Board line",
    draft: { revision: 2, base_version_id: boardVersion.id, out_of_date: false }, latest_version: { id: boardVersion.id, version: 3 }, created_at: "2026-10-28T08:00:00Z" };
  const result = (r: typeof boardResults.baseline) => r;
  type Req = { method: string; path: string; body: unknown; key: string | undefined };
  function fakeApi(table: Record<string, Response | Response[]>, auth: Auth) {
    const calls: Req[] = [];
    const queues = new Map(Object.entries(table).map(([k, v]) => [k, Array.isArray(v) ? [...v] : [v]]));
    const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://app.test");
      const method = init?.method ?? "GET";
      calls.push({ method, path: url.pathname, body: init?.body ? JSON.parse(String(init.body)) : undefined, key: (init?.headers as Record<string, string>)["Idempotency-Key"] });
      const q = queues.get(`${method} ${url.pathname}`);
      if (!q || q.length === 0) throw new Error(`no fake answer for ${method} ${url.pathname}`);
      return (q.length > 1 ? q.shift()! : q[0]!).clone();
    }) as typeof globalThis.fetch;
    return { calls, client: createApiClient({ session: () => auth.session, fetch }) };
  }
  const answers = (over: Record<string, Response | Response[]> = {}) => ({
    [`GET ${v1}/scenarios/${boardScenario.id}`]: json(200, boardScenario),
    [`GET ${v1}/model-versions/${boardVersion.id}`]: json(200, boardVersion),
    [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, boardBaselineRun()),
    [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, boardScenarioRun()),
    [`GET ${v1}/simulation-comparisons`]: json(200, boardComparison),
    [`GET ${v1}/simulations/${BASELINE_RUN}/results`]: json(200, result(boardResults.baseline)),
    [`GET ${v1}/simulations/${SCENARIO_RUN}/results`]: json(200, result(boardResults.scenario)),
    [`GET ${v1}/simulations/${BASELINE_RUN}/trace`]: json(200, boardTraces.baseline),
    [`GET ${v1}/simulations/${SCENARIO_RUN}/trace`]: json(200, boardTraces.scenario),
    [`GET ${v1}/analyses/${ANA}`]: json(200, explained),
    [`GET ${v1}/models/${model.id}`]: json(200, model),
    [`GET ${v1}/models/${model.id}/investigations`]: json(200, { items: [], next_cursor: null }),
    ...over,
  });
  const editor: Auth = { session: { organizationId: ORG }, role: "editor", userId: ME };

  async function page(api: ReturnType<typeof fakeApi>, auth: Auth = editor) {
    const { RunResults } = await import("../features/results/run-results");
    function WithAnalysis() {
      const [id, setId] = useState<string | null>(ANA);
      return <RunResults scenarioId={boardScenario.id} baselineId={BASELINE_RUN} scenarioRunId={SCENARIO_RUN} pollMs={25} analysis={{ id, onChange: setId }} />;
    }
    const el = await mount(
      <AuthProvider value={auth}>
        <ApiProvider client={api.client}>
          <WithAnalysis />
        </ApiProvider>
      </AuthProvider>,
    );
    await settle();
    await waitFor(() => text(el).includes("A second ICT fixture"), "the explanation");
    await settle();
    return el;
  }
  const section = (el: Element) => el.querySelector<HTMLElement>('section[aria-labelledby="analysis-heading"]')!;

  it("is not there unless the build turns investigations on", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", undefined);
    const api = fakeApi(answers(), editor);
    const el = await page(api);
    expect(text(section(el))).not.toContain(t("investigation.testThese"));
    expect(api.calls.some((c) => c.path.endsWith("/investigations"))).toBe(false);
  });

  it("starts an investigation from the analysis, reusing its baseline, and follows it", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const api = fakeApi(answers({ [`POST ${v1}/models/${model.id}/investigations`]: json(202, { investigation_id: INV, status: "running" }) }), editor);
    const el = await page(api);
    const s = section(el);
    await waitFor(() => text(s).includes(t("investigation.testThese")), "the button");
    expect(text(s)).toContain(t("investigation.testTheseHint"));
    const go = button(s, t("investigation.testThese"));
    expect(go.getAttribute("data-variant")).toBe("ai");
    expect(await violations()).toEqual([]);
    await press(go);
    await waitFor(() => nav.push.mock.calls.length > 0, "the investigation page");
    const post = api.calls.find((c) => c.method === "POST" && c.path.endsWith("/investigations"))!;
    expect(post.body).toEqual({ from_analysis_id: ANA, output_locale: "en" });
    expect(post.key).toMatch(/^[\x21-\x7e]{8,128}$/);
    expect(nav.push.mock.calls).toEqual([[`/investigations/${INV}`]]);
  });

  it("is a link to watch it when the caller's investigation is already running", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const running = { ...fixture<Investigation>("valid-investigation-running.json"), model_id: model.id, created_by: ME };
    const someoneElse = { ...running, id: "inv_01M4YDQK00B8S5WQXJ3TQ7ZK2C", created_by: "usr_01M4YDQK00TA0000000000ME02" };
    // Another person's running one is not the caller's: the button stays.
    const other = await page(fakeApi(answers({ [`GET ${v1}/models/${model.id}/investigations`]: json(200, { items: [someoneElse], next_cursor: null }) }), editor));
    await waitFor(() => text(section(other)).includes(t("investigation.testThese")), "the button");
    expect(section(other).querySelector(`a[href="/investigations/${someoneElse.id}"]`)).toBeNull();
    const mine = await page(fakeApi(answers({ [`GET ${v1}/models/${model.id}/investigations`]: json(200, { items: [running], next_cursor: null }) }), editor));
    await waitFor(() => section(mine).querySelector(`a[href="/investigations/${running.id}"]`) !== null, "the link");
    expect(text(section(mine).querySelector(`a[href="/investigations/${running.id}"]`))).toBe(t("investigation.watch"));
    expect(buttons(section(mine)).some((b) => text(b) === t("investigation.testThese"))).toBe(false);
  });

  it("says why it cannot start when the baseline changes the model or is over the caps", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const big = boardBaselineRun({ config: { ...boardBaselineRun().config, replications: 20 } });
    const el = await page(fakeApi(answers({ [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, big) }), editor));
    await waitFor(() => text(section(el)).includes(t("investigation.testThese.limits")), "the reason");
    expect(buttons(section(el)).some((b) => text(b) === t("investigation.testThese"))).toBe(false);
  });

  it("offers a viewer nothing to start", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const viewer: Auth = { session: { organizationId: ORG }, role: "viewer", userId: ME };
    const el = await page(fakeApi(answers(), viewer), viewer);
    expect(text(section(el))).not.toContain(t("investigation.testThese"));
  });
});
