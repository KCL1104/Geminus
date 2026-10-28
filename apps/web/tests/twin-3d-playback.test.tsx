// @vitest-environment happy-dom
// T-034 (TEST-025, TEST-026): the results page's 3D playback. The view
// shows the run chosen (its colours and its sampled items), the slider and
// Play move through the run, the sampling is said, and a run without a
// trace has nothing to play: no control and no column.
import { act } from "react";
import { describe, expect, it } from "vitest";
import type { ModelIR, Result, TraceArtifact } from "../lib/contracts";
import { Playback, clock } from "../features/twin-3d/playback/playback";
import { timeline } from "../features/twin-3d/playback/occupancy";
import type { View } from "../features/twin-3d/viewer";
import { boardLine } from "../features/results/fixtures/board-line";
import { en } from "../messages/en";
import { mount, press, settle } from "./ui-harness";

const ir = boardLine.model as unknown as ModelIR;
const result = (s: "baseline" | "scenario") => boardLine.results[s] as unknown as Result;
const trace = (s: "baseline" | "scenario") => boardLine.traces[s] as unknown as TraceArtifact;
const loaded = (s: "baseline" | "scenario") => ({ result: result(s), trace: { state: "loaded" as const, trace: trace(s) } });

function fakeView() {
  const occupancy: string[] = [];
  const scenes: number[] = [];
  const view: View = {
    setScene: (sc) => scenes.push(sc.nodes.length),
    setPalette: () => {},
    setSelected: () => {},
    setOccupancy: (counts, run) => occupancy.push(`${run}:${JSON.stringify(counts)}`),
    stepAt: () => null,
    orbit: () => {},
    zoom: () => {},
    pan: () => {},
    reset: () => {},
    resize: () => {},
    destroy: () => {},
  };
  return { view, occupancy, scenes };
}

const valueText = (el: HTMLElement) => el.querySelector("input[type=range]")!.getAttribute("aria-valuetext");
const slide = async (el: HTMLElement, value: number) => {
  const input = el.querySelector<HTMLInputElement>("input[type=range]")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, String(value));
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
};

describe("the 3D playback of the sampled traces", () => {
  it("shows the scenario's items at the time on the slider, and the baseline's when it is chosen", async () => {
    const { view, occupancy } = fakeView();
    const el = await mount(<Playback ir={ir} runs={{ baseline: loaded("baseline"), scenario: loaded("scenario") }} loadView={async () => view} />);
    await settle();
    const nodes = new Set(ir.nodes.map((n) => n.id));
    const horizon = result("scenario").measurement_window.horizon_ticks;
    // At the start nothing has arrived.
    expect(occupancy.at(-1)).toBe("scenario:{}");
    await slide(el, 500); // half way
    const half = horizon / 2;
    expect(occupancy.at(-1)).toBe(`scenario:${JSON.stringify(timeline(trace("scenario"), nodes, horizon).at(half))}`);
    expect(valueText(el)).toBe("5 hours of 10 hours");
    // The same time in the baseline.
    await press([...el.querySelectorAll("button")].find((b) => b.textContent === en["compare.baseline"]));
    await settle();
    expect(occupancy.at(-1)).toBe(`baseline:${JSON.stringify(timeline(trace("baseline"), nodes, horizon).at(half))}`);
    expect(el.textContent).toContain("Sampled trace of replication 1: about 1 in 8 work items.");
  });

  it("plays forward with the clock and pauses", async () => {
    const { view } = fakeView();
    const el = await mount(<Playback ir={ir} runs={{ baseline: loaded("baseline"), scenario: loaded("scenario") }} loadView={async () => view} />);
    await settle();
    const play = [...el.querySelectorAll("button")].find((b) => b.textContent === en["playback.play"]);
    await press(play);
    await act(async () => void (await new Promise((r) => setTimeout(r, 700))));
    const pause = [...el.querySelectorAll("button")].find((b) => b.textContent === en["playback.pause"]);
    expect(pause).toBeDefined();
    await press(pause);
    const shown = valueText(el);
    // 30 s for the whole run: in 0.7 s about 14 of its 600 minutes.
    expect(shown).not.toBe("0 hours of 10 hours");
    await act(async () => void (await new Promise((r) => setTimeout(r, 300))));
    expect(valueText(el)).toBe(shown);
    // The time is said by the slider, not by a live region that would announce every update (review M1).
    expect(el.querySelector("output, [role=status], [aria-live]")).toBeNull();
  });

  it("offers nothing to play and draws no column without a trace", async () => {
    const { view, occupancy } = fakeView();
    const none = { result: result("scenario"), trace: { state: "none" as const } };
    const el = await mount(<Playback ir={ir} runs={{ baseline: { ...none, result: result("baseline") }, scenario: none }} loadView={async () => view} />);
    await settle();
    expect(el.textContent).toContain(en["playback.none"]);
    expect(el.querySelector("input[type=range]")).toBeNull();
    expect([...el.querySelectorAll("button")].some((b) => b.textContent === en["playback.play"])).toBe(false);
    expect(occupancy.length).toBeGreaterThan(0);
    expect(occupancy.every((o) => o.endsWith(":null"))).toBe(true);
  });

  it("plays the one run that has a trace, with no run to choose", async () => {
    const { view, occupancy } = fakeView();
    const el = await mount(
      <Playback ir={ir} runs={{ baseline: { result: result("baseline"), trace: { state: "none" } }, scenario: loaded("scenario") }} loadView={async () => view} />,
    );
    await settle();
    expect([...el.querySelectorAll("button")].some((b) => b.textContent === en["compare.baseline"])).toBe(false);
    expect(occupancy.at(-1)).toBe("scenario:{}");
    expect(el.textContent).toContain(en["playback.missing.baseline"]);
  });

  // Review M2: a trace that failed to load is not one that was never recorded.
  it("says which run's trace failed to load", async () => {
    const { view } = fakeView();
    const failed = { state: "failed" as const, problem: { kind: "internal" as const, code: null, status: 500, message: "Bad gateway", requestId: null, details: [], retryAfterSeconds: null } };
    const el = await mount(
      <Playback ir={ir} runs={{ baseline: { result: result("baseline"), trace: failed }, scenario: loaded("scenario") }} loadView={async () => view} />,
    );
    await settle();
    expect(el.textContent).toContain(en["marey.failed.baseline"]);
    expect(el.textContent).not.toContain(en["playback.none"]);
    expect(el.textContent).not.toContain(en["playback.missing.baseline"]);
  });
});

// Review M3: the time names its unit, the largest the run has at least 2 of, to a tenth.
describe("the time shown", () => {
  it("names its unit in the reader's language", () => {
    expect(clock("en", 18_000_000, 36_000_000)).toEqual({ time: "5 hours", end: "10 hours" });
    expect(clock("en", 45_000, 90_000)).toEqual({ time: "45 seconds", end: "90 seconds" });
    expect(clock("en", 30_000, 59_000)).toEqual({ time: "30 seconds", end: "59 seconds" });
    expect(clock("en", 2_700_000, 5_400_000)).toEqual({ time: "45 minutes", end: "90 minutes" });
    expect(clock("en", 3 * 86_400_000 + 43_200_000, 7 * 86_400_000)).toEqual({ time: "3.5 days", end: "7 days" });
    expect(clock("zh-Hant", 18_000_000, 36_000_000)).toEqual({ time: "5 小時", end: "10 小時" });
  });
});
