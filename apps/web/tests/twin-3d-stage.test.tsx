// @vitest-environment happy-dom
// H-072: the 3D view as a stage of the line in motion (features/twin-3d/viewer.tsx).
// Given a run's trace, its motion and the page's clock, the view hands the
// clock's time to the drawing on every change and React does not re-render
// for it; the boards are the run's (its side's ink), placed by boards.ts at
// the clock's time, with no slide under reduced motion; the notes under the
// stage (the selected step's state, "+N not drawn") follow the drawing
// without a live region; the station lights have a key. Without a stage the
// line stands still. PlayCanvas is replaced by a view that records what it
// is asked and draws as the engine would (one placement per time).
import { act, Profiler } from "react";
import { describe, expect, it } from "vitest";
import type { ModelIR, TraceArtifact, TraceEvent } from "../lib/contracts";
import { createClock, type PlaybackClock } from "../features/playback/clock";
import { motion } from "../features/playback/motion";
import type { Placement } from "../features/twin-3d/boards";
import { type Playback, type Stage, Twin3D, type View } from "../features/twin-3d/viewer";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { fakeScheduler } from "./fake-scheduler";
import { readElectronicsLine } from "./fixtures/electronics-line";
import { mount, rerender, settle, violations } from "./ui-harness";

const data = readElectronicsLine();
const horizon = data.results.baseline.measurement_window.horizon_ticks;
const HOUR = 3_600_000;

/** A view that draws as the engine does: each time it is given, it asks the playback once and tells it what it drew. */
function drawingView() {
  const calls: string[] = [];
  const drawn: Placement[] = [];
  let playback: Playback | null = null;
  const view: View = {
    setScene: (sc) => calls.push(`scene:${sc.nodes.length}`),
    setPalette: () => calls.push("palette"),
    setSelected: (id) => calls.push(`selected:${id}`),
    setOccupancy: () => {},
    setPlayback: (p) => {
      playback = p;
      calls.push(`playback:${p?.side ?? null}`);
    },
    setTime: (tick) => {
      calls.push(`time:${tick}`);
      if (!playback) return;
      const p = playback.at(tick);
      drawn.push(p);
      playback.onDraw?.(p);
    },
    stepAt: () => null,
    orbit: () => {},
    zoom: () => {},
    pan: () => {},
    reset: () => {},
    resize: () => {},
    destroy: () => calls.push("destroy"),
  };
  return { view, calls, drawn };
}

function setup(side: "baseline" | "scenario" = "baseline", options: { reduced?: boolean; trace?: TraceArtifact } = {}) {
  const trace = options.trace ?? data.traces[side];
  const stage: Stage = { side, trace, motion: motion(trace, data.model, horizon) };
  const fake = fakeScheduler();
  const clock = createClock(horizon, { playing: false, reduced: options.reduced ?? false, scheduler: fake.scheduler });
  return { stage, clock, fake, ...drawingView() };
}

async function open(node: (props: { selected: string | null }) => React.ReactNode, selected: string | null = null) {
  let commits = 0;
  const el = await mount(<Profiler id="stage" onRender={() => commits++}>{node({ selected })}</Profiler>);
  await settle();
  return { el, commits: () => commits };
}

const stageOf = (s: ReturnType<typeof setup>, extra: { selected?: string | null; legend?: boolean; clock?: PlaybackClock | null } = {}) => (
  <Twin3D ir={data.model} selected={extra.selected ?? null} onSelect={() => {}} stage={s.stage} clock={extra.clock === undefined ? s.clock : extra.clock} legend={extra.legend} loadView={async () => s.view} />
);

describe("a stage of the line in motion", () => {
  it("draws the run's boards at the clock's time, every time it changes, without re-rendering React", async () => {
    const s = setup("scenario");
    const { commits } = await open(() => stageOf(s));
    expect(s.calls).toContain("playback:scenario");
    expect(s.calls).toContain("time:0");
    const before = commits();
    const times = s.calls.filter((c) => c.startsWith("time:")).length;
    await act(async () => {
      s.clock.seek(18 * HOUR);
      s.clock.play();
      s.fake.run(1000); // about 60 frames
      s.clock.pause();
    });
    expect(s.calls.filter((c) => c.startsWith("time:")).length - times).toBeGreaterThan(50);
    expect(commits()).toBe(before); // the clock's time never renders the component
    // What it drew at a time is the motion's frame there, placed.
    const last = s.drawn.at(-1)!;
    expect(last.tick).toBe(s.clock.now());
    expect(last.boards.map((b) => b.id)).toEqual(s.stage.motion.at(s.clock.now(), { hop: s.clock.hopTicks(), limit: 2000 }).boards.map((b) => b.id));
  });

  it("draws no slide under reduced motion: each board is at its new place at once", async () => {
    const s = setup("baseline", { reduced: true });
    await open(() => stageOf(s));
    for (let t = 6 * HOUR; t <= 23 * HOUR; t += HOUR / 4) await act(async () => s.clock.seek(t));
    expect(s.drawn.length).toBeGreaterThan(60);
    expect(s.drawn.every((p) => p.boards.every((b) => !b.sliding))).toBe(true);
  });

  it("says what the selected step is doing as it plays, in the line the canvas is described by, never announced", async () => {
    const s = setup("baseline");
    const { el } = await open(() => stageOf(s, { selected: "ict" }));
    await act(async () => s.clock.seek(41_760_000)); // ICT: one in service, one held
    const canvas = el.querySelector("canvas")!;
    const hint = el.querySelector(`[id="${canvas.getAttribute("aria-describedby")}"]`)!;
    expect(hint.textContent).toContain("Selected: ICT, blocked, 1 in service.");
    await act(async () => s.clock.seek(18 * HOUR + 30 * 60_000));
    expect(hint.textContent).toContain("Selected: ICT, ");
    expect(el.querySelector("[aria-live], [role=status], [role=alert], output")).toBeNull();
  });

  it("names a queue's waiting boards when a queue is selected", async () => {
    const s = setup("baseline");
    const { el } = await open(() => stageOf(s, { selected: "q_fct" }));
    await act(async () => s.clock.seek(22 * HOUR));
    expect(el.textContent).toContain("Selected: Buffer before FCT, 2 waiting.");
  });

  it("notes the boards it does not draw in the stage's corner, only when there are some", async () => {
    let seq = 1;
    const events: TraceEvent[] = Array.from({ length: 200 }, (_, i) => ({ time_tick: 0, sequence: seq++, type: "queue_enter" as const, entity_id: `e${i}`, node_id: "q_fct" }));
    const full: TraceArtifact = { ...data.traces.baseline, events };
    const s = setup("baseline", { trace: full });
    const { el } = await open(() => stageOf(s));
    await act(async () => s.clock.seek(HOUR));
    const cap = [...el.querySelectorAll("span")].find((x) => x.textContent === "+56 not drawn");
    expect(cap).toBeDefined();
    expect(cap!.hidden).toBe(false);
    const quiet = setup("baseline");
    const { el: other } = await open(() => stageOf(quiet));
    await act(async () => quiet.clock.seek(HOUR));
    expect(other.textContent).not.toContain("not drawn");
  });

  it("keys its station lights once, with what they can and cannot show, and passes axe", async () => {
    const s = setup("scenario");
    const { el } = await open(() => stageOf(s));
    const list = el.querySelector("ul")!;
    expect(list.getAttribute("aria-labelledby")).toBeTruthy();
    expect(el.querySelector(`[id="${list.getAttribute("aria-labelledby")}"]`)!.textContent).toBe(en["twin3d.lights"]);
    expect([...list.querySelectorAll("li")].map((li) => li.textContent)).toEqual([en["playback.state.busy"], en["playback.state.idle"], en["twin3d.lights.held"]]);
    expect(el.textContent).toContain(en["pack.electronics_line.playback.statesSampled"]);
    expect(await violations()).toEqual([]);
    // A page with two stages keys them under one.
    await rerender(stageOf(s, { legend: false }));
    expect(document.querySelector("ul")).toBeNull();
  });

  it("speaks Traditional Chinese", async () => {
    const s = setup("baseline");
    const el = await mount(stageOf(s, { selected: "q_fct" }), "zh-Hant");
    await settle();
    await act(async () => s.clock.seek(22 * HOUR));
    expect(el.textContent).toContain("已選取：Buffer before FCT，2 個等待中。");
    expect(el.textContent).toContain(zhHant["twin3d.lights"]);
    expect(el.textContent).toContain(zhHant["twin3d.lights.held"]);
  });
});

describe("a line that stands still", () => {
  it("plays nothing without a clock or a stage, and has no key to lights it does not show", async () => {
    const s = setup("baseline");
    const { el } = await open(() => stageOf(s, { clock: null }));
    expect(s.calls.filter((c) => c.startsWith("playback:"))).toEqual(["playback:null"]);
    expect(s.calls.some((c) => c.startsWith("time:"))).toBe(false);
    expect(el.querySelector("ul")).toBeNull();
    expect(el.textContent).not.toContain(en["twin3d.lights"]);
    expect(el.textContent).toContain(en["twin3d.hint"]);
  });

  it("shows the results' tint and its legend only when no run plays", async () => {
    const ir: ModelIR = data.model;
    const s = setup("baseline");
    const still = await mount(<Twin3D ir={ir} results={data.results.baseline} selected={null} onSelect={() => {}} loadView={async () => s.view} />);
    await settle();
    expect(still.textContent).toContain(en["twin3d.legend"]);
    const moving = setup("baseline");
    const playing = await mount(<Twin3D ir={ir} results={data.results.baseline} selected={null} onSelect={() => {}} stage={moving.stage} clock={moving.clock} loadView={async () => moving.view} />);
    await settle();
    expect(playing.textContent).not.toContain(en["twin3d.legend"]);
  });
});
