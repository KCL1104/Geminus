// @vitest-environment happy-dom
// H-072 (docs/design/H-072_TRACE_ANIMATION.md §5, §6): the model page's line
// in motion, with the real model page, API client and draft session against
// the editor's stand-in API (app/ui-gallery/editor/stand-in.ts) serving the
// electronics line template and its baseline run as the engine wrote them
// (tests/fixtures/electronics-line.ts). The page plays the latest completed
// run of the version shown on the canvas, names it over the canvas beside
// the playback bar (Pause first), pauses for editing until Play, starts
// paused under reduced motion, says why the line stands still (no run, no
// trace, a trace or the runs that could not be read), plays on a read-only
// canvas on a phone, and holds still at a review page's time. And
// loadLatestRun picks the run: completed, of the version, the latest.
import { act, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { standInFor } from "../app/ui-gallery/editor/sample-states";
import { createStandIn, type StandInOptions } from "../app/ui-gallery/editor/stand-in";
import { ModelView } from "../features/model-editor/model-view";
import { loadLatestRun } from "../features/playback/run";
import { StillPlayback } from "../features/playback/still";
import { lineBaselineRunOf, lineScenarioRunOf, STILL_AT } from "../features/results/fixtures/electronics-line-objects";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { Run } from "../lib/contracts";
import { i18nFor } from "../lib/i18n/provider";
import type { Locale } from "../lib/i18n/locale";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { readElectronicsLine } from "./fixtures/electronics-line";
import { key, mount, press, settle, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });

const line = readElectronicsLine();
const motionState = () => standInFor("motion", line);
const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";

/** The browser's media: a wide window (the editor) or a phone, with or without reduced motion. */
function media({ wide = true, reduced = false } = {}) {
  vi.spyOn(window, "matchMedia").mockImplementation(
    (q: string) =>
      ({ matches: q.includes("reduce") ? reduced : q.includes("min-width") ? wide : false, media: q, addEventListener: () => {}, removeEventListener: () => {} }) as unknown as MediaQueryList,
  );
}
afterEach(() => vi.restoreAllMocks());

type Opts = Partial<Omit<StandInOptions, "modelId">> & {
  locale?: Locale;
  still?: number;
  fetch?: (base: typeof globalThis.fetch) => typeof globalThis.fetch;
  /** When the page is ready to look at (default: the strip over the canvas is there). */
  ready?: (el: HTMLElement) => boolean;
};

async function open(opts: Opts = {}): Promise<HTMLElement> {
  const { locale, still, fetch: wrap, ready = (e) => e.querySelector("[data-motion]") !== null, ...stand } = opts;
  const api = createStandIn({ modelId: SAMPLE_MODEL_ID, ...motionState(), ...stand });
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: wrap ? wrap(api.fetch) : api.fetch });
  const page = <ModelView modelId={SAMPLE_MODEL_ID} />;
  const node: ReactNode = (
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
      <ApiProvider client={client}>{still === undefined ? page : <StillPlayback at={still}>{page}</StillPlayback>}</ApiProvider>
    </AuthProvider>
  );
  const el = await mount(node, locale ?? "en");
  const until = Date.now() + 10_000;
  while (!ready(el)) {
    if (Date.now() > until) throw new Error("the page did not get ready");
    await settle();
  }
  await settle();
  return el;
}

const strip = (el: HTMLElement) => el.querySelector<HTMLElement>("[data-motion]");
const caption = (el: HTMLElement) => strip(el)?.querySelector("p")?.textContent;
const toggle = (el: HTMLElement) => strip(el)?.querySelector<HTMLButtonElement>("[role=group] button") ?? null;
const slider = (el: HTMLElement) => strip(el)?.querySelector<HTMLInputElement>("input[type=range]") ?? null;
const card = (el: HTMLElement, id: string) => el.querySelector<HTMLElement>(`.react-flow__node[data-id="${id}"] [data-kind]`);
const button = (name: string) => [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => (b.getAttribute("aria-label") ?? b.textContent?.trim()) === name);
const wait = (ms: number) => act(async () => void (await new Promise((r) => setTimeout(r, ms))));
const runDate = (locale: Locale) => i18nFor(locale).dateTime(lineBaselineRunOf(line.model).created_at);

describe("the model page's line in motion", () => {
  it("plays the latest completed run of the version shown on the canvas, named over it beside the bar, Pause first", async () => {
    media();
    const el = await open();
    expect(caption(el)).toBe(`In motion: the baseline run of ${runDate("en")}, seed 1.`);
    expect(strip(el)?.getAttribute("data-motion")).toBe("baseline");
    expect(el.querySelector("[data-trace]")?.getAttribute("data-trace")).toBe("baseline");
    // It plays once the trace is here, and Pause comes before the canvas's steps in the page's order.
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.pause"]);
    const firstStep = el.querySelector(".react-flow__node")!;
    expect(toggle(el)!.compareDocumentPosition(firstStep) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(strip(el)?.textContent).toContain("Sampled: 1 board in 8, replication 1.");
    // The playhead's time goes on (its spoken value follows at most once a second).
    await wait(400);
    expect(slider(el)?.previousElementSibling?.textContent).not.toBe("Day 1, 00:00");
    // The machines are painted from the trace; nothing is announced while it plays.
    expect(el.querySelectorAll("[data-station]").length).toBeGreaterThan(0);
    expect(strip(el)?.querySelector("[role=status], [aria-live]")).toBeNull();
    expect(await violations()).toEqual([]);
  });

  it("pauses for editing (selecting a step to edit it, a change in its properties), and only Play resumes", async () => {
    media();
    const el = await open();
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.pause"]);
    const smt = el.querySelector<HTMLElement>('.react-flow__node[data-id="smt"]')!;
    await act(async () => smt.focus());
    await key(smt, "Enter");
    await settle();
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.play"]);
    await press(toggle(el));
    await settle();
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.pause"]);
    // An edit in the properties pauses it again.
    const name = [...document.querySelectorAll("label")].find((l) => l.textContent === en["editor.field.name"])!;
    const input = document.getElementById(name.htmlFor) as HTMLInputElement;
    await act(async () => {
      input.focus();
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "SMT line 1");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await key(input, "Enter");
    await settle();
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.play"]);
    // The pause holds for the visit: the 3D view keeps the same clock, paused.
    await press(button("3D"));
    await settle();
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.play"]);
  });

  it("starts paused under reduced motion", async () => {
    media({ reduced: true });
    const el = await open();
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.play"]);
    await wait(300);
    expect(slider(el)?.getAttribute("aria-valuetext")).toBe("Day 1, 00:00 of 1 day");
  });

  it("holds still at a review page's time: at 22:00 two boards wait before FCT", async () => {
    media();
    const el = await open({ still: STILL_AT });
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.play"]);
    expect(slider(el)?.getAttribute("aria-valuetext")).toBe("Day 1, 22:00 of 1 day");
    expect(card(el, "q_fct")?.querySelector("[data-trace-badge]")?.textContent).toBe("2");
  });

  it("says why the line stands still: no completed run, no trace, a trace or the runs that could not be read", async () => {
    media();
    const unavailable = (part: RegExp) => (base: typeof globalThis.fetch) =>
      (async (input: RequestInfo | URL, init?: RequestInit) =>
        part.test(new URL(String(input), "http://stand-in.invalid").pathname)
          ? new Response(JSON.stringify({ error: { code: "SERVICE_UNAVAILABLE", message: "down", request_id: "req_01M4YDQK00V6H8S0ST4F7510KR" } }), { status: 503 })
          : base(input, init)) as typeof globalThis.fetch;
    const cases: [string, Opts, string][] = [
      ["no run", { runs: [] }, en["playback.noRun"]],
      ["no trace", { runs: [{ run: lineBaselineRunOf(line.model), result: line.results.baseline, trace: null }] }, en["playback.noTrace"]],
      ["the trace failed", { fetch: unavailable(/\/trace$/) }, en["playback.traceFailed"]],
      ["the runs failed", { fetch: unavailable(/\/simulations$/) }, en["playback.runsFailed"]],
    ];
    for (const [what, opts, said] of cases) {
      const el = await open(opts);
      expect(caption(el), what).toBe(said);
      expect(strip(el)?.getAttribute("data-motion"), what).toBe("still");
      expect(strip(el)?.querySelector("[role=group]"), what).toBeNull();
      expect(el.querySelector("[data-trace]"), what).toBeNull();
      expect(el.querySelector(".react-flow__node"), what).not.toBeNull();
      expect(await violations(), what).toEqual([]);
      document.body.innerHTML = "";
    }
  });

  it("on a phone plays on a read-only canvas under the note, with no 3D; without a run there is no canvas", async () => {
    media({ wide: false });
    const el = await open();
    expect(el.textContent).toContain(en["editor.narrow"]);
    expect(el.querySelector("[data-trace]")?.getAttribute("data-trace")).toBe("baseline");
    expect(el.querySelector(".react-flow__node.draggable")).toBeNull();
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.pause"]);
    expect(button("3D")).toBeUndefined();
    expect(button(en["editor.save"])).toBeUndefined();
    expect(await violations()).toEqual([]);
    document.body.innerHTML = "";
    const none = await open({ runs: [], ready: (e) => (e.textContent ?? "").includes(en["editor.narrow"]) });
    await settle();
    expect(none.querySelector(".react-flow, [data-motion]")).toBeNull();
  });

  it("reads in Traditional Chinese", async () => {
    media();
    const el = await open({ locale: "zh-Hant" });
    expect(caption(el)).toBe(`播放中：${runDate("zh-Hant")} 的基準執行，種子 1。`);
    expect(toggle(el)?.getAttribute("aria-label")).toBe(zhHant["playback.pause"]);
    expect(strip(el)?.textContent).toContain("抽樣：每 8 片電路板取 1 片，第 1 次重複。");
    expect(strip(el)?.textContent).toContain(zhHant["pack.electronics_line.playback.ringsSampled"]);
    expect(await violations()).toEqual([]);
  });

  // H-072 review: a ring's colour is the only sign of a machine's state on the 2D canvas, and it
  // comes from the sampled boards alone (FCT can look idle while boards wait before it).
  it("keys the 2D canvas's state rings, and says they show only the sampled boards; in 3D the stage keys its lights", async () => {
    const rings = (el: HTMLElement) => strip(el)?.querySelector<HTMLElement>("[data-rings]") ?? null;
    const key = (el: HTMLElement) => {
      const list = rings(el)?.querySelector("ul");
      const name = list && document.getElementById(list.getAttribute("aria-labelledby") ?? "")?.textContent;
      return { name, items: [...(list?.querySelectorAll("li") ?? [])].map((li) => [li.querySelector("[data-ring]")?.getAttribute("data-ring"), li.textContent]) };
    };
    media();
    const el = await open();
    expect(key(el)).toEqual({
      name: en["playback.rings"],
      items: [["busy", en["playback.state.busy"]], ["idle", en["playback.state.idle"]], ["held", en["twin3d.lights.held"]]],
    });
    expect(rings(el)?.textContent).toContain(en["pack.electronics_line.playback.ringsSampled"]);
    // Read with the bar before the steps, never announced.
    expect(rings(el)!.compareDocumentPosition(el.querySelector(".react-flow__node")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(rings(el)?.closest("[aria-live], [role=status]")).toBeNull();
    expect(await violations()).toEqual([]);
    // The 3D view draws no rings: the strip drops their key (the stage has its own, of the lights).
    await press(button("3D"));
    await settle();
    expect(rings(el)).toBeNull();
    await press(button("2D"));
    await settle();
    expect(rings(el)).not.toBeNull();
    document.body.innerHTML = "";
    // A phone plays on the 2D canvas, so it has the key too; a line standing still has none.
    media({ wide: false });
    expect(rings(await open())).not.toBeNull();
    document.body.innerHTML = "";
    media();
    expect(rings(await open({ runs: [] }))).toBeNull();
  });
});

describe("the run the model page plays", () => {
  const ir = line.model;
  const V1 = "mv_01M4YDQK00TA0000000000H072";
  const V2 = "mv_01M4YDQK00TA0000000000H073";
  const at = (finished: string, over: Partial<Run> = {}) => lineBaselineRunOf(ir, { finished_at: finished, ...over });
  function api(pages: Run[][]) {
    const calls: string[] = [];
    const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    const client = createApiClient({
      session: () => ({ organizationId: ORG }),
      fetch: (async (input: RequestInfo | URL) => {
        const url = new URL(String(input), "http://app.test");
        calls.push(url.pathname + url.search);
        if (url.pathname.endsWith("/simulations")) {
          const n = Number(url.searchParams.get("cursor") ?? "0");
          return json({ items: pages[n] ?? [], next_cursor: n + 1 < pages.length ? String(n + 1) : null });
        }
        if (url.pathname.endsWith("/results")) return json(line.results.baseline);
        if (url.pathname.endsWith("/trace")) return json(line.traces.baseline);
        if (url.pathname.startsWith("/api/v1/model-versions/")) return json({ id: V1, ir });
        throw new Error(`no fake answer for ${url.pathname}`);
      }) as typeof fetch,
    });
    return { client, calls };
  }

  it("is the latest completed run of the version, over every page; a newer run still going, failed or of another version is not", async () => {
    const latest = at("2026-11-01T07:00:00Z", { id: "sim_01M4YDQK00TA00000000H072C3" });
    const a = api([
      [at("2026-11-01T06:00:00Z"), at("2026-11-01T09:00:00Z", { id: "sim_01M4YDQK00TA00000000H072C4", model_version_id: V2 })],
      [latest, at("2026-11-01T08:00:00Z", { id: "sim_01M4YDQK00TA00000000H072C5", status: "failed" }), lineBaselineRunOf(ir, { id: "sim_01M4YDQK00TA00000000H072C6", status: "running", finished_at: null })],
    ]);
    const r = await loadLatestRun(a.client, ir.model_id, V1);
    expect(r.ok && r.data?.run.id).toBe(latest.id);
    expect(r.ok && r.data?.side).toBe("baseline");
    expect(r.ok && r.data?.trace.state).toBe("loaded");
    expect(a.calls.filter((c) => c.includes("/simulations?"))).toHaveLength(2);
  });

  it("is a scenario's run on its version with the scenario's patch; and none when nothing completed", async () => {
    const scenario = lineScenarioRunOf(ir);
    const r = await loadLatestRun(api([[scenario]]).client, ir.model_id, V1);
    expect(r.ok && r.data?.side).toBe("scenario");
    expect(r.ok && r.data?.model.resources.find((x) => x.id === "fct_tester")?.capacity).toBe(4);
    const none = await loadLatestRun(api([[at("2026-11-01T06:00:00Z", { status: "cancelled" })]]).client, ir.model_id, V1);
    expect(none).toMatchObject({ ok: true, data: null });
  });
});
