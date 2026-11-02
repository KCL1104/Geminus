// @vitest-environment happy-dom
// H-072: the 2D canvas with a run playing (features/model-editor/canvas.tsx,
// trace-overlay.tsx), on the engine's own baseline trace of the electronics
// line. The overlay paints the cards from the clock's time without a React
// render; each card's state is in its description, never announced; the
// dots and the counts are hidden from assistive tech; editing pauses the
// playback (only Play resumes it); without a run nothing of it stays.
import { Profiler, type ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { getSmoothStepPath, type EdgeProps, Position, ReactFlow } from "@xyflow/react";
import { Canvas, type CanvasPlayback } from "../features/model-editor/canvas";
import { pathPoints } from "../features/model-editor/trace-geometry";
import { createRegistry, FlowEdge, OverlayContext } from "../features/model-editor/trace-overlay";
import { createClock, type PlaybackClock } from "../features/playback/clock";
import { motion } from "../features/playback/motion";
import type { ModelIR } from "../lib/contracts";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { fakeScheduler } from "./fake-scheduler";
import { readElectronicsLine } from "./fixtures/electronics-line";
import { mount, rerender, settle, violations } from "./ui-harness";

const HOUR = 3_600_000;
const data = readElectronicsLine();
const ir: ModelIR = data.model;
const horizon = data.results.baseline.measurement_window.horizon_ticks;
const baseline = motion(data.traces.baseline, ir, horizon);
const scenario = motion(data.traces.scenario, ir, horizon);

function setup() {
  const fake = fakeScheduler();
  const clock = createClock(horizon, { playing: false, scheduler: fake.scheduler });
  return { fake, clock };
}

let commits = 0;
const view = (props: { ir?: ModelIR; selected?: string | null; editable?: boolean; playback?: CanvasPlayback | null }): ReactNode => (
  <Profiler id="canvas" onRender={() => void commits++}>
    <Canvas
      ir={props.ir ?? ir}
      selected={props.selected ?? null}
      severity={() => null}
      editable={props.editable ?? true}
      onSelect={() => {}}
      onMove={() => {}}
      onConnect={() => {}}
      onDelete={() => {}}
      playback={props.playback ?? null}
    />
  </Profiler>
);

const wrapper = (id: string) => document.querySelector<HTMLElement>(`.react-flow__node[data-id="${id}"]`)!;
const card = (id: string) => wrapper(id).querySelector<HTMLElement>("[data-kind]")!;
const badge = (id: string) => card(id).querySelector("[data-trace-badge]")?.textContent ?? null;
const described = (id: string) =>
  (wrapper(id).getAttribute("aria-describedby") ?? "")
    .split(" ")
    .filter((x) => x !== "")
    .map((x) => document.getElementById(x)?.textContent ?? `(missing ${x})`);
const seek = async (clock: PlaybackClock, tick: number) => {
  clock.seek(tick);
  await settle();
};

describe("the 2D canvas with a run playing", () => {
  it("without a run carries none of the overlay", async () => {
    await mount(view({}));
    await settle();
    expect(document.querySelectorAll(".react-flow__node").length).toBe(ir.nodes.length);
    expect(document.querySelector("[data-trace]")).toBeNull();
    expect(document.querySelector("[data-trace-badge], [data-trace-state], [data-trace-dots], [data-station]")).toBeNull();
    // React Flow's own description of a step, alone.
    expect(described("fct")).toEqual([en["editor.canvas.help"]]);
  });

  it("paints the cards at the clock's time, in the run's ink, without a React render", async () => {
    const { fake, clock } = setup();
    await mount(view({ playback: { clock, motion: baseline, side: "baseline" } }));
    await settle();
    expect(document.querySelector("[data-trace]")?.getAttribute("data-trace")).toBe("baseline");
    // 05:00: nothing released yet; every machine lit idle, no count.
    await seek(clock, 5 * HOUR);
    const before = commits;
    expect(badge("q_smt")).toBe("");
    expect(card("fct").getAttribute("data-station")).toBe("idle");
    // A ring is a machine's: a queue has none.
    expect(card("q_fct").hasAttribute("data-station")).toBe(false);
    // 06:00: the release, 47 sampled boards before SMT.
    clock.seek(6 * HOUR);
    expect(badge("q_smt")).toBe("47");
    // 22:00: two before FCT.
    clock.seek(22 * HOUR);
    expect(badge("q_fct")).toBe("2");
    expect(badge("q_smt")).toBe("");
    // The first burn-in batch: the oven busy with five sampled boards.
    const batch = data.traces.baseline.events.find((e) => e.batch_id !== undefined)!;
    clock.seek(batch.time_tick);
    expect(card("burn_in").getAttribute("data-station")).toBe("busy");
    // ICT held by a full FCT queue: the warning ring.
    const blocked = data.traces.baseline.events.find((e) => e.type === "blocked")!;
    clock.seek(blocked.time_tick);
    expect(card("ict").getAttribute("data-station")).toBe("blocked");
    // Playing: two seconds of frames move the line on, and React renders nothing.
    clock.seek(6 * HOUR);
    clock.play();
    fake.run(2000);
    expect(clock.now()).toBeGreaterThan(6 * HOUR);
    expect(badge("q_smt")).not.toBe("47");
    clock.pause();
    expect(commits).toBe(before);
  });

  it("names a step's state in its description, never in a live region, and hides the dots and counts", async () => {
    const { clock } = setup();
    const lives = async () => {
      await settle();
      return document.querySelectorAll("[aria-live]").length;
    };
    await mount(view({}));
    const withoutRun = await lives();
    await rerender(view({ playback: { clock, motion: baseline, side: "baseline" } }));
    expect(await lives()).toBe(withoutRun);
    await seek(clock, 22 * HOUR);
    const help = en["editor.canvas.help"];
    // The step's state first, then the canvas's help (React Flow's description of a step).
    expect(described("fct")).toEqual(["FCT: idle, 0 waiting", help]);
    expect(described("q_fct")).toEqual(["Buffer before FCT: 2 waiting", help]);
    // A queue with nothing waiting adds nothing.
    expect(described("q_smt")).toEqual([help]);
    const batch = data.traces.baseline.events.find((e) => e.batch_id !== undefined)!;
    await seek(clock, batch.time_tick);
    expect(described("burn_in")[0]).toBe("Burn-in: a batch in service, 5 of it sampled");
    // The count is shown, not spoken (the description says it); the dots are drawing only.
    expect(card("q_fct").querySelector("[data-trace-badge]")?.getAttribute("aria-hidden")).toBe("true");
    expect(document.querySelector("[data-trace-dots]")?.getAttribute("aria-hidden")).toBe("true");
    expect(await violations()).toEqual([]);
  });

  it("says the state in Traditional Chinese", async () => {
    const { clock } = setup();
    await mount(view({ playback: { clock, motion: baseline, side: "baseline" } }), "zh-Hant");
    await seek(clock, 22 * HOUR);
    expect(described("fct")).toEqual(["FCT：閒置，0 個等待中", zhHant["editor.canvas.help"]]);
    expect(described("q_fct")).toEqual(["Buffer before FCT：2 個等待中", zhHant["editor.canvas.help"]]);
  });

  it("shows the scenario's run in its ink, and follows another run at once", async () => {
    const { clock } = setup();
    await mount(view({ playback: { clock, motion: baseline, side: "baseline" } }));
    await seek(clock, 22 * HOUR);
    expect(badge("q_fct")).toBe("2");
    await rerender(view({ playback: { clock, motion: scenario, side: "scenario" } }));
    await settle();
    expect(document.querySelector("[data-trace]")?.getAttribute("data-trace")).toBe("scenario");
    // With a fourth FCT tester nothing waits there at 22:00.
    expect(badge("q_fct")).toBe("");
  });

  it("pauses for editing: a step selected to edit it, or any change to the draft; Play resumes", async () => {
    const { clock } = setup();
    const playback: CanvasPlayback = { clock, motion: baseline, side: "baseline" };
    await mount(view({ playback }));
    clock.play();
    expect(clock.state().playing).toBe(true);
    // Selecting a step to edit it.
    await rerender(view({ playback, selected: "fct" }));
    expect(clock.state().playing).toBe(false);
    // The pause holds: a reload of the run does not start it again.
    clock.autoplay();
    expect(clock.state().playing).toBe(false);
    clock.play();
    // A change to the draft (here a rename in the properties).
    const renamed: ModelIR = { ...ir, nodes: ir.nodes.map((n) => (n.id === "fct" ? { ...n, name: "Functional test" } : n)) };
    await rerender(view({ playback, selected: "fct", ir: renamed }));
    expect(clock.state().playing).toBe(false);
    clock.play();
    // The same draft again (a reload that changed nothing) and deselecting go on playing.
    await rerender(view({ playback, selected: null, ir: JSON.parse(JSON.stringify(renamed)) as ModelIR }));
    expect(clock.state().playing).toBe(true);
    // Someone who cannot edit selects a step to look at it: it plays on.
    await rerender(view({ playback, selected: "smt", ir: renamed, editable: false }));
    expect(clock.state().playing).toBe(true);
    clock.halt();
  });

  it("says what it cannot draw: boards at a step the draft no longer has", async () => {
    const { clock } = setup();
    // The draft lost the FCT queue since the run: the run's two boards there at 22:00 have no card.
    const draft: ModelIR = { ...ir, nodes: ir.nodes.filter((n) => n.id !== "q_fct"), flows: ir.flows.filter((f) => f.from !== "q_fct" && f.to !== "q_fct") };
    await mount(view({ ir: draft, playback: { clock, motion: baseline, side: "baseline" } }));
    await seek(clock, 22 * HOUR);
    expect(document.querySelector("[data-trace-note]")?.textContent).toBe(en["playback.hidden"].other.replace("{count}", "2"));
    // At 05:00 there is nothing to say, and the note is empty (and not shown).
    await seek(clock, 5 * HOUR);
    expect(document.querySelector("[data-trace-note]")?.textContent).toBe("");
  });

  it("leaves nothing behind when the run goes", async () => {
    const { clock } = setup();
    await mount(view({}));
    await settle();
    const own = wrapper("fct").getAttribute("aria-describedby");
    await rerender(view({ playback: { clock, motion: baseline, side: "baseline" } }));
    await seek(clock, 22 * HOUR);
    expect(card("fct").getAttribute("data-station")).toBe("idle");
    expect(wrapper("fct").getAttribute("aria-describedby")).not.toBe(own);
    await rerender(view({}));
    await settle();
    expect(document.querySelector("[data-trace], [data-trace-badge], [data-trace-state], [data-trace-dots], [data-station]")).toBeNull();
    expect(wrapper("fct").getAttribute("aria-describedby")).toBe(own);
    // The clock no longer paints anything.
    clock.seek(6 * HOUR);
    expect(document.querySelector("[data-station]")).toBeNull();
  });
});

describe("a link on the canvas", () => {
  const ends = { sourceX: 172, sourceY: 32, sourcePosition: Position.Right, targetX: 236, targetY: 152, targetPosition: Position.Left };
  const link = (active: boolean, registry = createRegistry()) => (
    <OverlayContext.Provider value={{ active, registry }}>
      <svg>
        <FlowEdge {...({ id: "q_smt__smt", source: "q_smt", target: "smt", markerEnd: "url(#arrow)", ...ends } as unknown as EdgeProps)} />
      </svg>
    </OverlayContext.Provider>
  );

  it("is React Flow's smoothstep link, with chevrons while no run plays", async () => {
    const [path] = getSmoothStepPath(ends);
    await mount(link(false));
    const drawn = document.querySelector(".react-flow__edge-path")!;
    expect(drawn.getAttribute("d")).toBe(path);
    expect(drawn.getAttribute("marker-end")).toBe("url(#arrow)");
    // Its own id stays off the page (the built-in edge renders none): an IR flow ID is not a DOM ID.
    expect(drawn.hasAttribute("id")).toBe(false);
    const chevrons = document.querySelector("[data-chevrons]")!;
    expect(chevrons.getAttribute("d")).toMatch(/^M[\d.]+ [\d.]+L/);
  });

  // 2026-11-02 (canvas layout): a decision's probability sits above every link, never inside its
  // own link's group (where a link drawn later would cross it): in React
  // Flow's edge-label layer, over the links and under the cards, on a
  // backing (canvas.module.css .edgeLabel), hidden from assistive tech as
  // it was inside the link's role="img" group.
  it("draws a decision's probability in React Flow's edge-label layer, above every link, not in its own link", async () => {
    const [, labelX, labelY] = getSmoothStepPath(ends);
    await mount(
      <div style={{ width: 800, height: 600 }}>
        <ReactFlow nodes={[]} edges={[]}>
          <svg>
            <FlowEdge {...({ id: "ict_result__q_fct", source: "ict_result", target: "q_fct", label: "95%", markerEnd: "url(#arrow)", ...ends } as unknown as EdgeProps)} />
          </svg>
        </ReactFlow>
      </div>,
    );
    await settle();
    const layer = document.querySelector(".react-flow__edgelabel-renderer")!;
    const label = layer.querySelector<HTMLElement>("[data-edge-label]");
    expect(label?.textContent).toBe("95%");
    expect(label?.getAttribute("aria-hidden")).toBe("true");
    // Centred on the point React Flow gives the label, in canvas units.
    expect(label?.style.transform).toBe(`translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`);
    // Nothing of it in the link itself: no SVG text, no SVG backing.
    expect(document.querySelector(".react-flow__edge-path")?.getAttribute("d")).toBe(getSmoothStepPath(ends)[0]);
    expect(document.querySelector(".react-flow__edge-text, .react-flow__edge-textbg, svg text")).toBeNull();
  });

  it("reports its polyline to the overlay while a run plays, and drops its chevrons", async () => {
    const registry = createRegistry();
    await mount(link(true, registry));
    await settle();
    expect(document.querySelector("[data-chevrons]")).toBeNull();
    expect(registry.route("q_smt__smt")).toEqual(pathPoints(getSmoothStepPath(ends)[0]));
  });
});
