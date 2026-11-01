// @vitest-environment happy-dom
// H-072 (replacing T-034's column playback; TEST-025, TEST-026): the results
// page's line in motion (features/twin-3d/playback/playback.tsx). The
// baseline and the scenario play one above the other, Baseline first, each
// titled with its run, under one playback bar and on one clock, the controls
// said once under the last; the scenario's
// stage is its version with the scenario's patch; a step selected in one is
// selected in both; it plays once the traces are there and starts paused
// under reduced motion; a run without a trace stands still and says why;
// with none there is no bar; a review page can hold it still at a time.
// PlayCanvas is replaced by a view per stage that records what it is asked.
import { act } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ModelIR, Result, TraceArtifact } from "../lib/contracts";
import { StillPlayback } from "../features/playback/still";
import { Playback, type PlaybackRun } from "../features/twin-3d/playback/playback";
import type { Scene } from "../features/twin-3d/scene";
import type { View } from "../features/twin-3d/viewer";
import type { TraceState } from "../features/results/load";
import { boardBaselineRun, boardLine, boardScenarioRun } from "../features/results/fixtures/board-line";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { mount, press, settle, violations } from "./ui-harness";

const ir = boardLine.model as unknown as ModelIR;
const HOUR = 3_600_000;
const result = (s: "baseline" | "scenario") => boardLine.results[s] as unknown as Result;
const trace = (s: "baseline" | "scenario") => boardLine.traces[s] as unknown as TraceArtifact;
const loaded = (s: "baseline" | "scenario"): TraceState => ({ state: "loaded", trace: trace(s) });
const failed: TraceState = { state: "failed", problem: { kind: "internal", code: null, status: 500, message: "Bad gateway", requestId: null, details: [], retryAfterSeconds: null } };
const runs = (b: TraceState = loaded("baseline"), s: TraceState = loaded("scenario")): Record<"baseline" | "scenario", PlaybackRun> => ({
  baseline: { run: boardBaselineRun(), result: result("baseline"), trace: b },
  scenario: { run: boardScenarioRun(), result: result("scenario"), trace: s },
});

/** A recording view for each stage, named by the canvas it was made on. */
function views() {
  const made = new Map<string, { calls: string[]; scenes: Scene[] }>();
  const loadView = async (canvas: HTMLCanvasElement): Promise<View> => {
    const rec = { calls: [] as string[], scenes: [] as Scene[] };
    made.set(canvas.getAttribute("aria-label") ?? "", rec);
    return {
      setScene: (sc) => void rec.scenes.push(sc),
      setPalette: () => {},
      setSelected: (id) => void rec.calls.push(`selected:${id}`),
      setOccupancy: () => {},
      setPlayback: (p) => void rec.calls.push(`playback:${p?.side ?? null}`),
      setTime: (tick) => void rec.calls.push(`time:${tick}`),
      stepAt: () => "ict",
      orbit: () => {},
      zoom: () => {},
      pan: () => {},
      reset: () => {},
      resize: () => {},
      destroy: () => {},
    };
  };
  return { made, loadView };
}
const lastTime = (calls: string[]) => calls.filter((c) => c.startsWith("time:")).at(-1);

function reducedMotion(on: boolean) {
  vi.spyOn(window, "matchMedia").mockImplementation(
    (q: string) => ({ matches: on && q.includes("reduce"), media: q, addEventListener: () => {}, removeEventListener: () => {} }) as unknown as MediaQueryList,
  );
}
afterEach(() => vi.restoreAllMocks());

const toggle = (el: HTMLElement) => el.querySelector<HTMLButtonElement>("[role=group] button");
const slider = (el: HTMLElement) => el.querySelector<HTMLInputElement>("input[type=range]");
const stages = (el: HTMLElement) => [...el.querySelectorAll<HTMLElement>("section[data-stage]")];
const wait = (ms: number) => act(async () => void (await new Promise((r) => setTimeout(r, ms))));
async function slide(el: HTMLElement, value: number) {
  const input = slider(el)!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, String(value));
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
}

describe("the results page's line in motion", () => {
  it("plays both runs on one clock, Baseline first, each titled with its run, under one bar", async () => {
    reducedMotion(false);
    const v = views();
    const el = await mount(<Playback ir={ir} runs={runs()} loadView={v.loadView} />);
    await settle();
    expect(stages(el).map((s) => s.getAttribute("data-stage"))).toEqual(["baseline", "scenario"]);
    expect(stages(el).map((s) => s.querySelector("h3")?.textContent)).toEqual([en["runs.baseline"], en["runs.scenario"]]);
    expect(stages(el).map((s) => s.getAttribute("aria-labelledby") === s.querySelector("h3")?.id)).toEqual([true, true]);
    expect([...el.querySelectorAll("canvas")].map((c) => c.getAttribute("aria-label"))).toEqual([en["playback.stage.baseline"], en["playback.stage.scenario"]]);
    expect(el.querySelectorAll("[role=group]")).toHaveLength(1);
    // The bar comes before the stages, Pause first (WCAG 2.2.2).
    expect(el.firstElementChild?.firstElementChild?.getAttribute("role")).toBe("group");
    expect(el.textContent).toContain("Sampled: 1 work item in 8, replication 1.");
    const b = v.made.get(en["playback.stage.baseline"])!;
    const s = v.made.get(en["playback.stage.scenario"])!;
    expect(b.calls).toContain("playback:baseline");
    expect(s.calls).toContain("playback:scenario");
    // It plays once the traces are here (autoplay): Pause is offered, and time goes on in both.
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.pause"]);
    await wait(300);
    expect(lastTime(b.calls)).not.toBe("time:0");
    await press(toggle(el));
    await settle();
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.play"]);
    // One clock: a time on the slider is both stages' time.
    await slide(el, 5 * HOUR);
    expect([lastTime(b.calls), lastTime(s.calls)]).toEqual([`time:${5 * HOUR}`, `time:${5 * HOUR}`]);
    expect(slider(el)?.getAttribute("aria-valuetext")).toBe("Day 1, 05:00 of 10 hours");
    // Paused, it stays.
    const held = [b.calls.length, s.calls.length];
    await wait(200);
    expect([b.calls.length, s.calls.length]).toEqual(held);
    // Nothing is announced while it plays: the time is the slider's value.
    expect(el.querySelector("output, [role=status], [aria-live]")).toBeNull();
    expect(await violations()).toEqual([]);
  });

  it("draws each run on the model it ran: the scenario's second ICT fixture is a second unit", async () => {
    reducedMotion(false);
    const v = views();
    await mount(<Playback ir={ir} runs={runs()} loadView={v.loadView} />);
    await settle();
    const units = (label: string) => v.made.get(label)!.scenes.at(-1)!.stations.ict!.units.length;
    expect([units(en["playback.stage.baseline"]), units(en["playback.stage.scenario"])]).toEqual([1, 2]);
  });

  it("selects a step in both stages when it is picked in either", async () => {
    reducedMotion(false);
    const v = views();
    const el = await mount(<Playback ir={ir} runs={runs()} loadView={v.loadView} />);
    await settle();
    const canvas = el.querySelectorAll("canvas")[1]!;
    await act(async () => {
      canvas.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0, clientX: 10, clientY: 10 }));
      canvas.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, button: 0, clientX: 10, clientY: 10 }));
    });
    await settle();
    for (const label of [en["playback.stage.baseline"], en["playback.stage.scenario"]]) expect(v.made.get(label)!.calls).toContain("selected:ict");
  });

  it("starts paused under reduced motion, at the run's start", async () => {
    reducedMotion(true);
    const v = views();
    const el = await mount(<Playback ir={ir} runs={runs()} loadView={v.loadView} />);
    await settle();
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.play"]);
    await wait(300);
    expect(lastTime(v.made.get(en["playback.stage.scenario"])!.calls)).toBe("time:0");
  });

  it("holds still at the review page's time until Play", async () => {
    reducedMotion(false);
    const v = views();
    const el = await mount(
      <StillPlayback at={5 * HOUR}>
        <Playback ir={ir} runs={runs()} loadView={v.loadView} />
      </StillPlayback>,
    );
    await settle();
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.play"]);
    expect(slider(el)?.getAttribute("aria-valuetext")).toBe("Day 1, 05:00 of 10 hours");
    // The thumb is there too: the slider's end is the run's, though the paused clock rendered nothing new.
    expect([slider(el)?.max, slider(el)?.value]).toEqual([String(10 * HOUR), String(5 * HOUR)]);
    await wait(200);
    expect(lastTime(v.made.get(en["playback.stage.baseline"])!.calls)).toBe(`time:${5 * HOUR}`);
    await press(toggle(el));
    await settle();
    expect(toggle(el)?.getAttribute("aria-label")).toBe(en["playback.pause"]);
  });

  it("stands still without a trace: no bar, each run says why, its results' colours keyed once", async () => {
    reducedMotion(false);
    const v = views();
    const el = await mount(<Playback ir={ir} runs={runs({ state: "none" }, { state: "loaded", trace: { ...trace("scenario"), events: [] } })} loadView={v.loadView} />);
    await settle();
    expect(el.querySelector("[role=group], input[type=range]")).toBeNull();
    expect(stages(el).map((s) => s.querySelector("h3 + p")?.textContent)).toEqual([en["playback.noTrace"], en["playback.noTrace"]]);
    expect(el.textContent?.split(en["twin3d.legend"]).length).toBe(2);
    for (const rec of v.made.values()) expect(rec.calls.filter((c) => c.startsWith("playback:") && c !== "playback:null")).toEqual([]);
    expect(await violations()).toEqual([]);
  });

  it("plays the run that has a trace, and says the other's could not be loaded", async () => {
    reducedMotion(false);
    const v = views();
    const el = await mount(<Playback ir={ir} runs={runs(failed, loaded("scenario"))} loadView={v.loadView} />);
    await settle();
    expect(stages(el)[0]!.querySelector("h3 + p")?.textContent).toBe(en["playback.traceFailed"]);
    expect(stages(el)[1]!.querySelector("h3 + p")).toBeNull();
    expect(el.querySelectorAll("[role=group]")).toHaveLength(1);
    expect(v.made.get(en["playback.stage.scenario"])!.calls).toContain("playback:scenario");
    // The still stage keeps its default name; the lights' key is under the playing one, the colours' under the still one.
    expect(el.querySelector("canvas")?.getAttribute("aria-label")).toMatch(/^3D view of the line: \d+ steps$/);
    expect(stages(el)[1]!.textContent).toContain(en["twin3d.lights"]);
    expect(stages(el)[0]!.textContent).toContain(en["twin3d.legend"]);
  });

  // H-039 "say each thing once" (H-072 review): the same three lines of controls were under both stages.
  it("shows the stage's controls once, under the last stage; each canvas is still described by them", async () => {
    reducedMotion(false);
    const v = views();
    const el = await mount(<Playback ir={ir} runs={runs()} loadView={v.loadView} />);
    await settle();
    const described = [...el.querySelectorAll("canvas")].map((c) => el.querySelector(`[id="${c.getAttribute("aria-describedby")}"]`)?.textContent ?? "");
    expect(described.map((d) => d.includes(en["twin3d.hint"]))).toEqual([true, true]);
    const said = stages(el).map((s) => s.querySelector("[data-controls]")?.getAttribute("data-controls"));
    expect(said).toEqual(["unseen", "shown"]);
    // A step selected is named under each stage, with its own run's state.
    const canvas = el.querySelectorAll("canvas")[0]!;
    await act(async () => {
      canvas.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0, clientX: 10, clientY: 10 }));
      canvas.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, button: 0, clientX: 10, clientY: 10 }));
    });
    await settle();
    for (const s of stages(el)) expect(s.querySelector<HTMLElement>(`[id="${s.querySelector("canvas")!.getAttribute("aria-describedby")}"]`)!.hidden).toBe(false);
    expect(await violations()).toEqual([]);
  });

  it("reads in Traditional Chinese", async () => {
    reducedMotion(false);
    const v = views();
    const el = await mount(<Playback ir={ir} runs={runs()} loadView={v.loadView} />, "zh-Hant");
    await settle();
    expect(stages(el).map((s) => s.querySelector("h3")?.textContent)).toEqual([zhHant["runs.baseline"], zhHant["runs.scenario"]]);
    expect([...el.querySelectorAll("canvas")].map((c) => c.getAttribute("aria-label"))).toEqual([zhHant["playback.stage.baseline"], zhHant["playback.stage.scenario"]]);
    expect(toggle(el)?.getAttribute("aria-label")).toBe(zhHant["playback.pause"]);
    await press(toggle(el));
    await slide(el, 5 * HOUR);
    expect(slider(el)?.getAttribute("aria-valuetext")).toBe("第 1 天 05:00／10 小時");
    expect(await violations()).toEqual([]);
  });
});
