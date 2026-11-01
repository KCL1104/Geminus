// H-072: the 2D canvas's trace overlay, its geometry and what it shows at
// fixed times of the engine's own traces of the electronics line
// (features/model-editor/trace-geometry.ts). A dot moves only on a hop and
// only along the links the canvas draws (through the decisions and failure
// steps between two stations, under their cards); a hop the links do not
// join is not drawn; at rest a board is only counted (the badge of the boards
// waiting) and a machine only lit (its state ring); without a run, chevrons
// on the links point the way the line flows.
import { getSmoothStepPath, Position } from "@xyflow/react";
import { describe, expect, it } from "vitest";
import type { ModelIR, TraceArtifact } from "../lib/contracts";
import { canvasLayout } from "../features/model-editor/layout";
import {
  chevronMarks,
  chevronPath,
  createRouter,
  type OverlayFrame,
  overlayFrame,
  pathPoints,
  placeHop,
  type Point,
  pointAt,
  polyline,
} from "../features/model-editor/trace-geometry";
import { type Motion, motion } from "../features/playback/motion";
import { readElectronicsLine } from "./fixtures/electronics-line";

const HOUR = 3_600_000;
const data = readElectronicsLine();
const ir: ModelIR = data.model;
const horizon = data.results.baseline.measurement_window.horizon_ticks;
const runs = { baseline: motion(data.traces.baseline, ir, horizon), scenario: motion(data.traces.scenario, ir, horizon) };
/** The run time a 300 ms slide covers at 1×: a whole run plays in 30 s (clock.ts). */
const HOP = (300 * horizon) / 30_000;

// The links as the canvas draws them: React Flow's smoothstep path between the
// source handle (centred on a card's right side, 8 px wide) and the target
// handle (on its left side), the cards 168 px wide where canvasLayout puts them.
const CARD = { width: 168, height: 64 };
const HANDLE = 4;
const at = canvasLayout(ir);
const right = (id: string): Point => ({ x: at[id]!.x + CARD.width + HANDLE, y: at[id]!.y + CARD.height / 2 });
const left = (id: string): Point => ({ x: at[id]!.x - HANDLE, y: at[id]!.y + CARD.height / 2 });
const linkPath = (from: Point, to: Point) =>
  getSmoothStepPath({ sourceX: from.x, sourceY: from.y, sourcePosition: Position.Right, targetX: to.x, targetY: to.y, targetPosition: Position.Left })[0];
const links = new Map(ir.flows.map((f) => [f.id, pathPoints(linkPath(right(f.from), left(f.to)))]));
const router = () => createRouter(ir.flows, (id) => links.get(id));
const flowId = (from: string, to: string) => ir.flows.find((f) => f.from === from && f.to === to)!.id;

const view = (side: keyof typeof runs, tick: number, hop = HOP, steps: readonly { id: string; type: string }[] = ir.nodes, m: Motion = runs[side]): OverlayFrame =>
  overlayFrame(m.at(tick, { hop, limit: 2000 }), steps, router().route);
const badge = (v: OverlayFrame, id: string) => v.steps.get(id)?.waiting ?? 0;
const near = (p: Point | null | undefined, q: Point) => {
  expect(p).not.toBeNull();
  expect(p!.x).toBeCloseTo(q.x, 6);
  expect(p!.y).toBeCloseTo(q.y, 6);
};

describe("the links as the canvas draws them", () => {
  it("reads React Flow's smoothstep path into a polyline from handle to handle", () => {
    // Two steps side by side (one layer apart): a straight link 64 units long.
    const straight = pathPoints(linkPath({ x: 172, y: 32 }, { x: 236, y: 32 }));
    expect(straight[0]).toEqual({ x: 172, y: 32 });
    expect(straight.at(-1)).toEqual({ x: 236, y: 32 });
    expect(polyline(straight).length).toBeCloseTo(64, 6);

    // One row down: right, down and right again, its two corners rounded (radius 5) and flattened.
    const bent = pathPoints(linkPath({ x: 172, y: 32 }, { x: 236, y: 152 }));
    expect(bent[0]).toEqual({ x: 172, y: 32 });
    expect(bent.at(-1)).toEqual({ x: 236, y: 152 });
    expect(bent.length).toBeGreaterThan(6);
    for (const p of bent) {
      expect(p.x).toBeGreaterThanOrEqual(172);
      expect(p.x).toBeLessThanOrEqual(236);
      expect(p.y).toBeGreaterThanOrEqual(32);
      expect(p.y).toBeLessThanOrEqual(152);
    }
    // Shorter than the square corners' 64 + 120, by a little.
    const length = polyline(bent).length;
    expect(length).toBeLessThan(184);
    expect(length).toBeGreaterThan(180);
  });

  it("stops at anything but M, L and Q, and never invents a point", () => {
    expect(pathPoints("")).toEqual([]);
    expect(pathPoints("M0 0L10 0C 1 2 3 4 5 6L20 0")).toEqual([{ x: 0, y: 0 }, { x: 10, y: 0 }]);
    expect(pathPoints("M0 0L10")).toEqual([{ x: 0, y: 0 }]);
    expect(pathPoints("Q 1,1 2,2")).toEqual([]);
  });

  it("measures along a polyline, and cuts it where a dot is", () => {
    const line = polyline([{ x: 0, y: 0 }, { x: 10, y: 0 }, { x: 10, y: 10 }]);
    expect(line.length).toBe(20);
    expect(pointAt(line, 0)).toEqual({ x: 0, y: 0 });
    expect(pointAt(line, 0.25)).toEqual({ x: 5, y: 0 });
    expect(pointAt(line, 0.75)).toEqual({ x: 10, y: 5 });
    expect(pointAt(line, 1)).toEqual({ x: 10, y: 10 });
    expect(pointAt(line, 7)).toEqual({ x: 10, y: 10 });
    expect(pointAt(line, -1)).toEqual({ x: 0, y: 0 });
  });
});

describe("without a run: chevrons show the way the line flows", () => {
  it("puts one chevron in the middle of a short straight link, pointing downstream like its arrow", () => {
    expect(chevronMarks([{ x: 172, y: 32 }, { x: 236, y: 32 }])).toEqual([{ x: 204, y: 32, dx: 1, dy: 0 }]);
    // An open ">" 5 units long and 8 wide, its tip downstream: the shape of the link's end arrow.
    expect(chevronPath([{ x: 172, y: 32 }, { x: 236, y: 32 }])).toBe("M201.5 36L206.5 32L201.5 28");
  });

  it("spaces chevrons along long runs, follows a run that goes back or up, and leaves short runs bare", () => {
    expect(chevronMarks([{ x: 0, y: 0 }, { x: 360, y: 0 }]).map((m) => m.x)).toEqual([60, 180, 300]);
    expect(chevronMarks([{ x: 0, y: 120 }, { x: 0, y: 0 }])).toEqual([{ x: 0, y: 60, dx: 0, dy: -1 }]);
    // A run React Flow writes as several collinear pieces (its gaps and centre) is one run.
    expect(chevronMarks([{ x: 172, y: 32 }, { x: 192, y: 32 }, { x: 204, y: 32 }, { x: 204, y: 32 }, { x: 216, y: 32 }, { x: 236, y: 32 }])).toEqual([{ x: 204, y: 32, dx: 1, dy: 0 }]);
    expect(chevronMarks([{ x: 300, y: 0 }, { x: 0, y: 0 }]).every((m) => m.dx === -1)).toBe(true);
    expect(chevronMarks([{ x: 0, y: 0 }, { x: 39, y: 0 }])).toEqual([]);
    expect(chevronPath([{ x: 0, y: 0 }, { x: 39, y: 0 }])).toBe("");
  });

  it("gives every link of the electronics line at least one chevron, each on the link", () => {
    for (const f of ir.flows) {
      const points = links.get(f.id)!;
      const marks = chevronMarks(points);
      expect(marks.length, f.id).toBeGreaterThan(0);
      const line = polyline(points);
      // Each mark lies on one of the link's segments.
      for (const m of marks) {
        const on = points.some((a, i) => {
          const b = points[i + 1];
          if (b === undefined) return false;
          const cross = (b.x - a.x) * (m.y - a.y) - (b.y - a.y) * (m.x - a.x);
          const dot = (m.x - a.x) * (b.x - a.x) + (m.y - a.y) * (b.y - a.y);
          return Math.abs(cross) < 1e-6 && dot >= 0 && dot <= (b.x - a.x) ** 2 + (b.y - a.y) ** 2;
        });
        expect(on, `${f.id} at ${m.x},${m.y}`).toBe(true);
      }
      expect(line.length).toBeGreaterThan(0);
    }
  });
});

describe("a hop's way along the links", () => {
  it("is the link itself between two linked steps, from the right side of one to the left side of the next", () => {
    const r = router().route("release_a", "q_smt")!;
    expect(r.points).toEqual(links.get(flowId("release_a", "q_smt")));
    expect(r.points[0]).toEqual(right("release_a"));
    expect(r.points.at(-1)).toEqual(left("q_smt"));
  });

  it("runs through the steps the trace does not stop at (a decision, a failure), under their cards", () => {
    // AOI to the ICT queue: through the AOI result (a decision).
    const aoi = router().route("aoi", "q_ict")!;
    expect(aoi.points).toEqual([...links.get(flowId("aoi", "aoi_result"))!, ...links.get(flowId("aoi_result", "q_ict"))!]);
    // Across the decision's card: from its left side to its right side.
    const into = links.get(flowId("aoi", "aoi_result"))!.length;
    expect(aoi.points[into - 1]).toEqual(left("aoi_result"));
    expect(aoi.points[into]).toEqual(right("aoi_result"));
    // A failed functional test: FCT, its result, the failure, the debug queue.
    const fail = router().route("fct", "q_fct_debug")!;
    expect(fail.points).toEqual([
      ...links.get(flowId("fct", "fct_result"))!,
      ...links.get(flowId("fct_result", "fct_fail"))!,
      ...links.get(flowId("fct_fail", "q_fct_debug"))!,
    ]);
  });

  it("has no way where no link leads, and none until every link on it is drawn", () => {
    expect(router().route("q_ict", "release_a")).toBeNull();
    expect(router().route("smt", "smt")).toBeNull();
    expect(router().route("smt", "not_a_step")).toBeNull();
    // A link React Flow has not drawn yet (its steps not measured): no way, and asked again later.
    const drawn = new Map(links);
    drawn.delete(flowId("q_smt", "smt"));
    const r = createRouter(ir.flows, (id) => drawn.get(id));
    expect(r.route("q_smt", "smt")).toBeNull();
    drawn.set(flowId("q_smt", "smt"), links.get(flowId("q_smt", "smt"))!);
    expect(r.route("q_smt", "smt")?.points).toEqual(links.get(flowId("q_smt", "smt")));
    // A step moved: its links are drawn again, and clear() forgets the old ones.
    const moved = links.get(flowId("q_smt", "smt"))!.map((p) => ({ x: p.x, y: p.y + 120 }));
    drawn.set(flowId("q_smt", "smt"), moved);
    expect(r.route("q_smt", "smt")?.points).toEqual(links.get(flowId("q_smt", "smt")));
    r.clear();
    expect(r.route("q_smt", "smt")?.points).toEqual(moved);
  });

  it("places a board steps of the way along it, and a hop that began before the last ended from where that one was", () => {
    const route = router().route;
    const line = route("q_smt", "smt")!;
    near(placeHop({ path: ["q_smt", "smt"], steps: [0] }, route), line.points[0]!);
    near(placeHop({ path: ["q_smt", "smt"], steps: [0.5] }, route), pointAt(line, 0.5));
    near(placeHop({ path: ["q_smt", "smt"], steps: [1] }, route), line.points.at(-1)!);
    // q_smt → smt finished (1), then 0.3 of the way from there to the AOI queue: across SMT's card, then its link.
    const on = polyline([line.points.at(-1)!, ...route("smt", "q_aoi")!.points]);
    near(placeHop({ path: ["q_smt", "smt", "q_aoi"], steps: [1, 0.3] }, route), pointAt(on, 0.3));
    // Half way to SMT when the next hop began: from that point on to the AOI queue.
    const half = polyline([pointAt(line, 0.5), ...line.points.slice(-1), ...route("smt", "q_aoi")!.points]);
    near(placeHop({ path: ["q_smt", "smt", "q_aoi"], steps: [0.5, 0.5] }, route), pointAt(half, 0.5));
    // Nothing where the links do not join two of its steps.
    expect(placeHop({ path: ["q_ict", "release_a"], steps: [0.5] }, route)).toBeNull();
    expect(placeHop({ path: ["q_smt", "smt", "release_b"], steps: [1, 0.5] }, route)).toBeNull();
  });
});

describe("the electronics line at fixed times of its sampled traces", () => {
  it("shows nothing before the first board is released (05:00): no dot, no count, every machine idle", () => {
    for (const side of ["baseline", "scenario"] as const) {
      const v = view(side, 5 * HOUR);
      expect(v.dots).toEqual([]);
      expect([...v.steps.values()].every((s) => s.waiting === 0)).toBe(true);
      const machines = ir.nodes.filter((n) => n.type === "process");
      expect(machines.length).toBe(11);
      for (const n of machines) expect(v.steps.get(n.id)?.state, n.id).toBe("idle");
      // Rings are for machines only.
      for (const n of ir.nodes.filter((x) => x.type !== "process")) expect(v.steps.get(n.id)?.state, n.id).toBeNull();
      expect(v.notDrawn).toBe(0);
      expect(v.hidden).toBe(0);
    }
  });

  it("at 06:00 the release: 47 sampled boards wait before SMT, each dot at the start of its link, then half way along it 150 ms later", () => {
    const start = view("baseline", 6 * HOUR);
    expect(badge(start, "q_smt")).toBe(47);
    const frame = runs.baseline.at(6 * HOUR, { hop: HOP, limit: 2000 });
    const hopping = frame.boards.filter((b) => b.hop !== null);
    expect(hopping.length).toBeGreaterThan(0);
    expect(start.dots.length).toBe(hopping.length);
    const a = router().route("release_a", "q_smt")!;
    for (const d of start.dots.filter((x) => x.id.startsWith("r0/release_a/"))) near(d, a.points[0]!);
    // Half a slide later (150 ms of playback), the boards still on that one hop are half way along the release link.
    const half = 6 * HOUR + HOP / 2;
    const still = runs.baseline.at(half, { hop: HOP, limit: 2000 }).boards.filter((b) => b.hop?.path.join(">") === "release_a>q_smt").map((b) => b.id);
    expect(still.length).toBeGreaterThan(20);
    const later = view("baseline", half);
    for (const id of still) near(later.dots.find((d) => d.id === id), pointAt(a, 0.5));
    // Once the slide is over (300 ms), a board stands where its event put it until its next one: counted, not drawn.
    const settled = runs.baseline.at(6 * HOUR + HOP, { hop: HOP, limit: 2000 }).boards.filter((b) => b.node === "q_smt" && b.state === "queued");
    expect(settled.length).toBeGreaterThan(20);
    expect(settled.every((b) => b.hop === null)).toBe(true);
    const drawn = new Set(view("baseline", 6 * HOUR + HOP).dots.map((d) => d.id));
    expect(settled.filter((b) => drawn.has(b.id))).toEqual([]);
  });

  it("at 16:00 a board slides from the SMT queue into SMT, and three leave the burn-in oven together for packing", () => {
    const v = view("baseline", 16 * HOUR);
    const dot = (id: string) => v.dots.find((d) => d.id === id);
    const frame = runs.baseline.at(16 * HOUR, { hop: HOP, limit: 2000 });
    const hop = (id: string) => frame.boards.find((b) => b.id === id)!.hop!;
    expect(hop("r0/release_b/71").path).toEqual(["q_smt", "smt"]);
    near(dot("r0/release_b/71"), pointAt(router().route("q_smt", "smt")!, hop("r0/release_b/71").steps[0]!));
    // One batch out of the oven: its boards share the slide to the packing queue.
    const oven = ["r0/release_a/56", "r0/release_a/58", "r0/release_a/11"];
    for (const id of oven) expect(hop(id)).toEqual({ path: ["burn_in", "q_pack"], steps: [hop(oven[0]!).steps[0]] });
    for (const id of oven) near(dot(id), pointAt(router().route("burn_in", "q_pack")!, hop(id).steps[0]!));
    // Every dot is on a link of the canvas (here, the ends of the two ways).
    expect(v.dots.length).toBe(frame.boards.filter((b) => b.hop !== null).length);
  });

  it("at 22:00 two boards wait before FCT in the baseline, and none with a fourth tester (the scenario)", () => {
    expect(badge(view("baseline", 22 * HOUR), "q_fct")).toBe(2);
    expect(badge(view("scenario", 22 * HOUR), "q_fct")).toBe(0);
    expect(badge(view("baseline", 17 * HOUR), "q_fct")).toBe(2);
    expect(badge(view("scenario", 17 * HOUR), "q_fct")).toBe(0);
    // Between events, nothing slides (23:00): only the counts and the rings show.
    expect(view("baseline", 23 * HOUR).dots).toEqual([]);
  });

  it("lights each machine in the state its sampled boards put it: busy, blocked, a batch in the oven", () => {
    const events = data.traces.baseline.events;
    const fct = events.find((e) => e.type === "service_start" && e.node_id === "fct")!;
    expect(view("baseline", fct.time_tick).steps.get("fct")).toEqual({ waiting: 0, state: "busy", batch: null });
    // ICT held (blocked) by a full FCT queue: its ring is the warning.
    const blocked = events.find((e) => e.type === "blocked")!;
    expect(blocked.node_id).toBe("ict");
    expect(view("baseline", blocked.time_tick).steps.get("ict")?.state).toBe("blocked");
    // The first burn-in batch: five sampled boards in one batch.
    const batch = events.find((e) => e.batch_id !== undefined)!;
    expect(view("baseline", batch.time_tick).steps.get("burn_in")).toEqual({ waiting: 0, state: "busy", batch: 5 });
    // Two batches in at 08:00: busy, and no single batch to name.
    const eight = view("baseline", 8 * HOUR).steps.get("burn_in")!;
    expect(eight.state).toBe("busy");
    expect(eight.batch).toBeNull();
    // A queue has a count, never a ring.
    expect(view("baseline", 22 * HOUR).steps.get("q_fct")).toEqual({ waiting: 2, state: null, batch: null });
  });

  it("draws no slide under reduced motion (hop 0): the counts and rings jump with each step", () => {
    const v = view("baseline", 6 * HOUR, 0);
    expect(v.dots).toEqual([]);
    expect(badge(v, "q_smt")).toBe(47);
    expect(view("baseline", 16 * HOUR, 0).dots).toEqual([]);
  });

  it("counts what it cannot draw: boards at a step the canvas lacks, and boards past the limit", () => {
    // The draft lost the FCT queue: its two boards at 22:00 are not drawn, and said to be so.
    const without = ir.nodes.filter((n) => n.id !== "q_fct");
    const v = view("baseline", 22 * HOUR, HOP, without);
    expect(v.steps.has("q_fct")).toBe(false);
    expect(v.hidden).toBe(2);
    // A step the draft added has no state from the run.
    const added = view("baseline", 22 * HOUR, HOP, [...ir.nodes, { id: "new_step", type: "process" }]);
    expect(added.steps.has("new_step")).toBe(false);
    expect(added.hidden).toBe(0);
    // A hop through a step the canvas lacks is not drawn.
    const noRelease = view("baseline", 6 * HOUR, HOP, ir.nodes.filter((n) => n.id !== "release_a"));
    expect(noRelease.dots.filter((d) => d.id.startsWith("r0/release_a/"))).toEqual([]);
    // Over the limit, the motion keeps the front of each stack and counts the rest.
    const frame = runs.baseline.at(6 * HOUR, { hop: HOP, limit: 10 });
    const limited = overlayFrame(frame, ir.nodes, router().route);
    expect(limited.notDrawn).toBe(frame.notDrawn);
    expect(limited.notDrawn).toBeGreaterThan(0);
    expect(limited.dots.length).toBeLessThanOrEqual(10);
  });

  it("holds a cut-short trace at its last event: the counts and rings stay, and the boards that had just moved finish their slide (H-072 review)", () => {
    const cut = 12 * HOUR;
    const short: TraceArtifact = { ...data.traces.baseline, truncated: true, events: data.traces.baseline.events.filter((e) => e.time_tick <= cut) };
    const m = motion(short, ir, horizon);
    expect(m.end).toBeLessThanOrEqual(cut);
    const atEnd = view("baseline", m.end, HOP, ir.nodes, m);
    // Boards moved on the last events: at the end they have only begun their slide.
    expect(atEnd.dots.length).toBeGreaterThan(0);
    const midway = view("baseline", m.end + HOP / 2, HOP, ir.nodes, m);
    expect(midway.tick).toBe(m.end);
    expect([...midway.steps]).toEqual([...atEnd.steps]);
    for (const t of [m.end + HOP, 20 * HOUR, horizon]) {
      const after = view("baseline", t, HOP, ir.nodes, m);
      expect(after.tick).toBe(m.end);
      expect([...after.steps]).toEqual([...atEnd.steps]);
      // Every board then stands at its step: no dot is left on a link (a dot means a board in transit).
      expect(after.dots).toEqual([]);
    }
  });
});
