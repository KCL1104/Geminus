// H-072: where the line in motion draws each sampled board, and what each
// station light shows, at fixed times of the electronics line's real runs
// (features/twin-3d/boards.ts on features/playback/motion.ts frames). A
// board is drawn at its step in the place its state has there: a queue's
// grid, a unit of its station, its batch's oven cell; only a hop slides,
// along the conveyors; nothing is drawn twice or somewhere else; a stack
// that is full, and the stage's cap, count what they do not draw; lights
// come from the sampled boards alone, never "failed"; the same time looks
// the same however it was reached.
import { describe, expect, it } from "vitest";
import type { ModelIR, TraceArtifact, TraceEvent } from "../lib/contracts";
import { type Frame, motion } from "../features/playback/motion";
import { BOARD_LIMIT, batchCells, type Placement, placeBoards, route, slotAt } from "../features/twin-3d/boards";
import { buildScene, type Scene, type SceneNode } from "../features/twin-3d/scene";
import { readElectronicsLine } from "./fixtures/electronics-line";

const data = readElectronicsLine();
const HOUR = 3_600_000;
const horizon = data.results.baseline.measurement_window.horizon_ticks;
const models = { baseline: data.model, scenario: { ...data.model, resources: data.model.resources.map((r) => (r.id === "fct_tester" ? { ...r, capacity: 4 } : r)) } as ModelIR };
const scenes = { baseline: buildScene(models.baseline), scenario: buildScene(models.scenario) };
type Side = keyof typeof models;

function stage(side: Side) {
  const m = motion(data.traces[side], models[side], horizon);
  const cells = batchCells(data.traces[side], scenes[side]);
  return { m, cells, at: (tick: number, hop = 0): Placement => placeBoards(scenes[side], m.at(tick, { hop, limit: BOARD_LIMIT }), cells) };
}

const near = (a: readonly number[], b: readonly number[], eps = 1e-9) => a.every((v, i) => Math.abs(v - b[i]!) <= eps);
const box = (scene: Scene, id: string) => scene.nodes.find((n) => n.id === id)!;
function within(n: SceneNode, p: readonly number[]) {
  const r = n.rotationY === 0 ? null : Math.hypot(n.size[0], n.size[2]) / 2;
  return Math.abs(p[0]! - n.position[0]) <= (r ?? n.size[0] / 2) && Math.abs(p[2]! - n.position[2]) <= (r ?? n.size[2] / 2) && p[1]! <= n.size[1];
}

describe("the boards at a time of the run", () => {
  it("draws every sampled board in the line once, at its step, in the place its state has there", () => {
    for (const side of ["baseline", "scenario"] as const) {
      const s = stage(side);
      const scene = scenes[side];
      for (let t = 0; t <= horizon; t += HOUR / 2) {
        const p = s.at(t);
        const f = p.frame;
        expect(p.boards.map((b) => b.id), `${side} ${t}`).toEqual(f.boards.map((b) => b.id)); // all drawn, in order
        expect([p.notDrawn, p.hidden]).toEqual([0, 0]);
        const spots = new Set<string>();
        for (const b of p.boards) {
          expect(b.sliding).toBe(false);
          const key = b.position.map((v) => v.toFixed(6)).join(",");
          expect(spots.has(key), `${side} ${t} ${b.id} shares ${key}`).toBe(false);
          spots.add(key);
          // Waiting in a queue, in service, held, in a batch: on its station.
          if (["queued", "service", "setup", "blocked", "batch"].includes(b.state) && b.node !== "burn_in") expect(within(box(scene, b.node), b.position), `${side} ${t} ${b.id} ${b.state}@${b.node}`).toBe(true);
        }
        for (const [id, n] of Object.entries(f.nodes)) {
          const here = p.boards.filter((b) => b.node === id);
          expect(here.filter((b) => b.state === "queued").length, `${side} ${t} ${id}`).toBe(n.waiting);
          expect(here.filter((b) => ["service", "setup", "batch"].includes(b.state)).length, `${side} ${t} ${id}`).toBe(n.inService);
        }
      }
    }
  });

  it("shows the pile before FCT: at 22:00 two boards wait in the baseline, none with the fourth tester", () => {
    const t = 22 * HOUR;
    const base = stage("baseline").at(t);
    const scen = stage("scenario").at(t);
    const waiting = (p: Placement) => p.boards.filter((b) => b.node === "q_fct" && b.state === "queued");
    expect([waiting(base).length, waiting(scen).length]).toEqual([2, 0]);
    // On q_fct's grid, front first: slot 0 and slot 1.
    const grid = scenes.baseline.stations.q_fct!.wait;
    expect(waiting(base).map((b) => b.position)).toEqual([slotAt(grid, 0), slotAt(grid, 1)]);
  });

  it("puts a board in service on a unit of its station, and one held after service on the next", () => {
    const s = stage("baseline");
    const p = s.at(41_760_000); // ICT: one in service, one held (q_fct full)
    const ict = scenes.baseline.stations.ict!;
    const at = (state: string) => p.boards.filter((b) => b.node === "ict" && b.state === state).map((b) => b.position);
    expect(at("service")).toEqual([ict.units[0]!.at]);
    expect(at("blocked")).toEqual([ict.units[1]!.at]);
    expect(p.lights.filter((l) => l.node === "ict").map((l) => l.state)).toEqual(["busy", "blocked"]);
  });

  it("loads a batch into one oven cell and keeps it there until it leaves", () => {
    const s = stage("baseline");
    const oven = scenes.baseline.stations.burn_in!;
    const seen = new Map<string, number>();
    for (let t = 0; t <= horizon; t += 5 * 60_000) {
      const p = s.at(t);
      const batches = p.frame.nodes.burn_in!.batches;
      const cells = batches.map((b) => s.cells.get(b.id)!);
      expect(new Set(cells).size, `${t}`).toBe(batches.length); // each batch its own cell
      for (const b of batches) {
        const cell = s.cells.get(b.id)!;
        if (seen.has(b.id)) expect(cell).toBe(seen.get(b.id));
        seen.set(b.id, cell);
        // Its members stacked on that cell's grid, from slot 0.
        const members = p.boards.filter((x) => x.node === "burn_in" && x.state === "batch" && p.frame.boards.find((y) => y.id === x.id)!.batch === b.id);
        expect(members.length).toBe(b.members);
        members.forEach((m, k) => expect(near(m.position, slotAt(oven.cells[cell]!, k)!), `${t} ${b.id} ${k}`).toBe(true));
      }
    }
    expect(seen.size).toBeGreaterThan(5);
  });

  it("gives the oven cells of a trace at the engine's cap in time linear in its batches (H-072 review)", () => {
    // A batch step that makes most of a capped trace: 20,000 batches of one sampled board each,
    // four at a time in the oven's cells, then the next four.
    const oven = scenes.baseline.stations.burn_in!;
    const n = oven.cells.length;
    const events: TraceEvent[] = [];
    for (let i = 0; i < 20_000; i++) {
      const start = Math.floor(i / n) * 10;
      events.push({ time_tick: start, sequence: 2 * i + 1, type: "service_start", entity_id: `r0/release_a/${i}`, node_id: "burn_in", batch_id: `r0/burn_in/${i}` });
    }
    for (let i = 0; i < 20_000; i++) events.push({ time_tick: Math.floor(i / n) * 10 + 9, sequence: 2 * i + 2, type: "service_end", entity_id: `r0/release_a/${i}`, node_id: "burn_in", batch_id: `r0/burn_in/${i}` });
    events.sort((a, b) => a.time_tick - b.time_tick || a.sequence - b.sequence);
    const trace: TraceArtifact = { ...data.traces.baseline, truncated: true, events };
    const t0 = performance.now();
    const cells = batchCells(trace, scenes.baseline);
    // Grouping by copying each step's list per batch took about 2 s here; pushing onto it, about 10 ms.
    expect(performance.now() - t0).toBeLessThan(400);
    expect(cells.size).toBe(20_000);
    // Each round of n batches fills the cells in turn.
    expect([0, 1, n - 1, n, 19_999].map((i) => cells.get(`r0/burn_in/${i}`))).toEqual([0, 1 % n, (n - 1) % n, 0, 19_999 % n]);
  });
});

describe("a hop's slide", () => {
  it("follows the conveyors through the steps a board passes without an event", () => {
    const scene = scenes.baseline;
    expect(route(scene, "fct", "q_burnin")).toEqual(["fct", "fct_result", "q_burnin"]);
    expect(route(scene, "ict", "q_ict_debug")).toEqual(["ict", "ict_result", "ict_fail", "q_ict_debug"]);
    // A board that finds its station free passes the queue in the same tick: its slide runs through it.
    expect(route(scene, "ict", "ict_debug")).toEqual(["ict", "ict_result", "ict_fail", "q_ict_debug", "ict_debug"]);
    expect(route(scene, "release_a", "q_smt")).toEqual(["release_a", "q_smt"]);
  });

  it("starts at the step left, passes the decision's hub, and ends where the board then stands", () => {
    // r0/release_a/4 leaves FCT for the burn-in queue at 22,129,070 ms.
    const s = stage("baseline");
    const scene = scenes.baseline;
    const t0 = 22_129_070;
    const hop = 60_000; // shorter than its FCT test, so one hop (a longer one would fold into the slide toward FCT)
    const board = (p: Placement) => p.boards.find((b) => b.id === "r0/release_a/4")!;
    const end = board(s.at(t0 + hop, hop));
    expect([end.node, end.sliding]).toEqual(["q_burnin", false]);
    const start = board(s.at(t0, hop));
    expect(start.sliding).toBe(true);
    expect(near(start.position, scene.stations.fct!.hub)).toBe(true);
    // Along FCT → decision → its place in the queue, by length.
    const pts = [scene.stations.fct!.hub, scene.stations.fct_result!.hub, end.position];
    const legs = [Math.hypot(pts[1]![0] - pts[0]![0], pts[1]![2] - pts[0]![2]), Math.hypot(pts[2]![0] - pts[1]![0], pts[2]![1] - pts[1]![1], pts[2]![2] - pts[1]![2])];
    const s1 = legs[0]! / (legs[0]! + legs[1]!);
    const atHub = board(s.at(t0 + s1 * hop, hop));
    expect(near(atHub.position, scene.stations.fct_result!.hub, 1e-6)).toBe(true);
    const half = board(s.at(t0 + (s1 / 2) * hop, hop));
    expect(near(half.position, [(pts[0]![0] + pts[1]![0]) / 2, pts[0]![1], (pts[0]![2] + pts[1]![2]) / 2], 1e-6)).toBe(true);
  });

  it("folds a slide that begins before the last ended, each leg along its conveyors, and ends at the board's place", () => {
    // At 1× a 1-day run's 300 ms slide is 14.4 minutes of the run: longer than some tests, so hops chain.
    const hop = 864_000;
    const s = stage("baseline");
    const scene = scenes.baseline;
    let chains = 0;
    for (let t = 6 * HOUR; t <= 20 * HOUR; t += 7 * 60_000) {
      const f = s.m.at(t, { hop, limit: BOARD_LIMIT });
      const p = placeBoards(scene, f, s.cells);
      for (const b of f.boards) {
        if (!b.hop || b.hop.path.length < 3) continue;
        chains++;
        const placed = p.boards.find((x) => x.id === b.id)!;
        expect(placed.sliding).toBe(true);
        // On the conveyors between the steps of the chain's routes (and the board's place): never off them, never below the floor.
        const steps = b.hop.path.slice(1).flatMap((to, i) => route(scene, b.hop!.path[i]!, to));
        const xs = steps.map((id) => scene.stations[id]!.hub[0]);
        expect(placed.position[0]).toBeGreaterThanOrEqual(Math.min(...xs) - 2.5);
        expect(placed.position[0]).toBeLessThanOrEqual(Math.max(...xs) + 2.5);
        expect(placed.position[1]).toBeGreaterThan(0);
      }
    }
    expect(chains).toBeGreaterThan(0);
  });

  it("is not drawn under reduced motion (no slide: the board is at its new place at once)", () => {
    const s = stage("baseline");
    for (let t = 0; t <= horizon; t += HOUR) expect(s.at(t, 0).boards.some((b) => b.sliding)).toBe(false);
    expect(stage("baseline").at(22_129_070, 0).boards.find((b) => b.id === "r0/release_a/4")!.node).toBe("q_burnin");
  });
});

describe("the same time, however it was reached", () => {
  it("places the same boards and lights going forward, back or jumping", () => {
    const fresh = (t: number) => stage("scenario").at(t, 300_000);
    const walked = stage("scenario");
    for (const t of [18 * HOUR, 7 * HOUR, 23.5 * HOUR, 12 * HOUR + 1234, 0, 18 * HOUR]) {
      walked.at(t + 7 * HOUR, 300_000);
      walked.at(t / 3, 300_000);
      const a = walked.at(t, 300_000);
      const b = fresh(t);
      expect([a.boards, a.lights, a.notDrawn]).toEqual([b.boards, b.lights, b.notDrawn]);
    }
  });
});

describe("the station lights", () => {
  it("are one per unit for a bank (busy first, then held, then idle) and one for the oven, never failed", () => {
    const s = stage("baseline");
    const scene = scenes.baseline;
    let busyOven = false;
    for (let t = 0; t <= horizon; t += 15 * 60_000) {
      const p = s.at(t);
      expect(p.lights.length).toBe(Object.values(scene.stations).reduce((a, st) => a + st.lamps.length, 0));
      for (const l of p.lights) expect(l.state).not.toBe("failed");
      const fct = p.lights.filter((l) => l.node === "fct").map((l) => l.state);
      const n = p.frame.nodes.fct!;
      expect(fct.filter((x) => x === "busy").length).toBe(Math.min(3, n.inService));
      expect(fct.indexOf("idle") === -1 || fct.slice(fct.indexOf("idle")).every((x) => x === "idle")).toBe(true);
      const oven = p.lights.filter((l) => l.node === "burn_in");
      expect(oven).toHaveLength(1);
      expect(oven[0]!.state).toBe(p.frame.nodes.burn_in!.state);
      busyOven ||= oven[0]!.state === "busy";
    }
    expect(busyOven).toBe(true);
  });

  it("show a changeover in amber's state (setup), from the trace's setup_start", () => {
    let seq = 1;
    const ev = (time_tick: number, type: TraceEvent["type"], node_id: string, extra: Partial<TraceEvent> = {}): TraceEvent => ({ time_tick, sequence: seq++, type, entity_id: "e1", node_id, ...extra });
    const trace = synthetic([ev(0, "arrival", "release_a"), ev(0, "queue_enter", "q_smt"), ev(10, "setup_start", "smt", { resource_id: "smt_line" }), ev(10, "service_start", "smt")]);
    const p = placeBoards(scenes.baseline, motion(trace, models.baseline, 100).at(20));
    expect(p.lights.filter((l) => l.node === "smt").map((l) => l.state)).toEqual(["setup"]);
    expect(p.boards.map((b) => [b.node, b.state])).toEqual([["smt", "setup"]]);
    expect(p.boards[0]!.position).toEqual(scenes.baseline.stations.smt!.units[0]!.at);
  });
});

describe("what is not drawn", () => {
  it("counts the boards past a stack's height, and past the stage's cap, and draws none of them elsewhere", () => {
    let seq = 1;
    const events: TraceEvent[] = [];
    for (let i = 0; i < 200; i++) events.push({ time_tick: 0, sequence: seq++, type: "queue_enter", entity_id: `e${i}`, node_id: "q_fct" });
    const m = motion(synthetic(events), models.baseline, 100);
    const all = placeBoards(scenes.baseline, m.at(1, { limit: BOARD_LIMIT }));
    const grid = scenes.baseline.stations.q_fct!.wait;
    const room = grid.cols * grid.rows * grid.layers;
    expect(room).toBe(144);
    expect([all.boards.length, all.notDrawn]).toEqual([144, 56]);
    const capped = placeBoards(scenes.baseline, m.at(1, { limit: 100 }));
    expect([capped.boards.length, capped.notDrawn]).toEqual([100, 100]);
    expect(capped.boards.map((b) => b.position)).toEqual(all.boards.slice(0, 100).map((b) => b.position));
  });

  it("counts the boards at a step the drawn model does not have as hidden", () => {
    const cut: ModelIR = { ...models.baseline, nodes: models.baseline.nodes.filter((n) => n.id !== "q_smt"), flows: models.baseline.flows.filter((f) => f.from !== "q_smt" && f.to !== "q_smt") };
    const f: Frame = stage("baseline").m.at(6 * HOUR);
    const p = placeBoards(buildScene(cut), f);
    expect(p.hidden).toBe(f.nodes.q_smt!.waiting);
    expect(p.boards.some((b) => b.node === "q_smt")).toBe(false);
  });
});

function synthetic(events: TraceEvent[]): TraceArtifact {
  return { format: "geminus.trace.v1", engine_version: "des-go-0.5.0", replication_index: 0, sampling: { method: "entity_id_sha256", rate: 0.125, max_events: 100_000 }, truncated: false, events };
}
