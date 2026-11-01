// Where the line in motion draws each sampled board, and what each station
// light shows, at a time of a run (H-072; docs/design/H-072_TRACE_ANIMATION.md).
// Pure: it reads a frame of the motion model (features/playback/motion.ts)
// and the scene's stations (scene.ts), so a test can check any time.
//
// - A board stands where its last trace event put it (motion.ts), in the
//   place its state has at that step: queued in the step's stack (a queue's
//   grid, front first), in service or held (blocked) on a unit of the
//   station (one cabinet, seat or lane per unit, the first free first), in a
//   batch in the oven's cell, between two events of one tick at the step's
//   port. Nothing between events moves it.
// - The one motion is a hop's slide (Board.hop, over the clock's hopTicks):
//   along the conveyors from the step it left, through the steps a board
//   passes without an event (decisions, failures, rework, and a queue it
//   passed within one tick), to its place at the new step (route). A hop
//   that began before the last ended is folded as motion.ts's placeOnHop
//   folds it, each leg along its conveyors.
// - An oven's batch keeps one cell from its start to its end (batchCells):
//   the cell is given when the batch starts, from the trace alone, so the
//   same time always looks the same, whichever way the reader got there.
// - A stack holds what its grid has room for (kits.ts `layers`); boards past
//   it, and past the stage's cap (motion.at's limit), are counted in
//   notDrawn, never drawn somewhere else.
// - A light is a station's state from its sampled boards only (motion.ts):
//   per unit, the first units busy, then changing over, then held; an oven
//   or a large bank has one light for the station. Breakdowns are not in
//   geminus.trace.v1, so no light is ever "failed" from it.
import type { TraceArtifact } from "../../lib/contracts";
import type { Board, BoardState, Frame, NodeFrame, StationState } from "../playback/motion";
import type { Grid, Scene, Station, Vec3 } from "./scene";
import { LAYER, PITCH } from "./kits";

/** The most boards one stage draws (brief §5); the rest are counted, not drawn. */
export const BOARD_LIMIT = 2000;

export type PlacedBoard = {
  readonly id: string;
  readonly node: string;
  readonly state: BoardState;
  /** The tile's centre. */
  readonly position: Vec3;
  /** Degrees about y. */
  readonly yaw: number;
  /** On a hop's slide. */
  readonly sliding: boolean;
};

export type Light = { readonly node: string; readonly unit: number; readonly position: Vec3; readonly state: StationState };

export type Placement = {
  readonly tick: number;
  readonly boards: readonly PlacedBoard[];
  readonly lights: readonly Light[];
  /** Boards in the line not drawn: past the stage's cap, or past a stack's height. */
  readonly notDrawn: number;
  /** Boards at a step the drawn model does not have (motion.ts `hidden`, and a step a draft removed). */
  readonly hidden: number;
  /** The motion's frame it was placed from (the counts the captions read). */
  readonly frame: Frame;
};

/** Slot k of a stack, or null past its last layer. */
export function slotAt(g: Grid, k: number): Vec3 | null {
  const per = g.cols * g.rows;
  const layer = Math.floor(k / per);
  if (layer >= g.layers) return null;
  const r = k % per;
  const col = Math.floor(r / g.rows);
  const row = r % g.rows;
  const a = col * PITCH[0];
  const c = (row - (g.rows - 1) / 2) * PITCH[1];
  return [g.at[0] + g.along[0] * a + g.across[0] * c, g.at[1] + layer * LAYER, g.at[2] + g.along[1] * a + g.across[1] * c];
}

/** Steps a board passes without an event of its own (motion.ts: decisions route, failures and rework are passes). */
const PASS: ReadonlySet<string> = new Set(["decision", "failure", "rework"]);
/** Then a queue too: a board that finds its station free passes its queue within one tick, so its hop runs from station to station. */
const THROUGH: ReadonlySet<string> = new Set([...PASS, "queue"]);
const routes = new WeakMap<Scene, Map<string, readonly string[]>>();

/** The fewest flows from `from` to `to` through steps of the types `via` (the IR's flow order breaking ties), or null. */
function shortest(scene: Scene, from: string, to: string, via: ReadonlySet<string>): string[] | null {
  const back = new Map<string, string>([[from, ""]]);
  const queue = [from];
  for (let i = 0; i < queue.length; i++) {
    const at = queue[i]!;
    for (const next of scene.links[at] ?? []) {
      if (back.has(next)) continue;
      back.set(next, at);
      if (next === to) {
        const path = [to];
        for (let p = at; p !== ""; p = back.get(p)!) path.unshift(p);
        return path;
      }
      if (via.has(scene.stations[next]?.type ?? "")) queue.push(next);
    }
  }
  return null;
}

/**
 * The steps a slide from `from` to `to` runs through, both included: the
 * fewest flows through steps a board passes without an event, else also
 * through queues; [from, to] when no such way exists.
 */
export function route(scene: Scene, from: string, to: string): readonly string[] {
  let cache = routes.get(scene);
  if (!cache) routes.set(scene, (cache = new Map()));
  const key = `${from}\u0000${to}`;
  const known = cache.get(key);
  if (known) return known;
  const path = shortest(scene, from, to, PASS) ?? shortest(scene, from, to, THROUGH) ?? [from, to];
  cache.set(key, path);
  return path;
}

const lerp = (a: Vec3, b: Vec3, s: number): Vec3 => [a[0] + (b[0] - a[0]) * s, a[1] + (b[1] - a[1]) * s, a[2] + (b[2] - a[2]) * s];
const headingOf = (a: Vec3, b: Vec3) => (Math.atan2(-(b[2] - a[2]), b[0] - a[0]) * 180) / Math.PI;

/** The point a share s (0..1) of the way along a polyline, by length, and the heading of its segment there. */
function along(points: readonly Vec3[], s: number): { p: Vec3; yaw: number | null } {
  const lengths = points.slice(1).map((p, i) => Math.hypot(p[0] - points[i]![0], p[1] - points[i]![1], p[2] - points[i]![2]));
  const total = lengths.reduce((a, b) => a + b, 0);
  if (total === 0) return { p: points.at(-1)!, yaw: null };
  let left = Math.min(1, Math.max(0, s)) * total;
  for (let i = 0; i < lengths.length; i++) {
    const l = lengths[i]!;
    if (left <= l || i === lengths.length - 1) {
      const a = points[i]!;
      const b = points[i + 1]!;
      const flat = Math.hypot(b[0] - a[0], b[2] - a[2]) > 1e-9;
      return { p: lerp(a, b, l === 0 ? 1 : left / l), yaw: flat ? headingOf(a, b) : null };
    }
    left -= l;
  }
  return { p: points.at(-1)!, yaw: null };
}

/**
 * Where a sliding board is: motion.ts's fold (placeOnHop: from path[0],
 * steps[i] of the way toward path[i + 1], in turn), each leg along the
 * conveyors of its route rather than straight, the last ending at the
 * board's place `end`. A leg's start is where the fold has got to.
 */
function slide(scene: Scene, b: Board, end: Vec3, endYaw: number): { p: Vec3; yaw: number } {
  const hop = b.hop!;
  const hub = (id: string) => scene.stations[id]?.hub ?? end;
  const at = (id: string) => (id === b.node ? end : hub(id));
  let p = at(hop.path[0]!);
  let yaw = endYaw;
  for (let i = 0; i + 1 < hop.path.length; i++) {
    const from = hop.path[i]!;
    const to = hop.path[i + 1]!;
    const s = hop.steps[i] ?? 1;
    const leg = along([p, ...route(scene, from, to).slice(1, -1).map(hub), at(to)], s);
    p = leg.p;
    if (s > 0 && s < 1 && leg.yaw !== null) yaw = leg.yaw;
  }
  return { p, yaw };
}


/** Unit u of a station: its cabinet (or cell), the u-th, stacked when there are more boards than units. */
function unitSlot(st: Station, u: number): { at: Vec3 | null; yaw: number } {
  const units = st.units.length > 0 ? st.units : st.cells;
  if (units.length === 0) return { at: slotAt(st.port, u), yaw: st.port.yaw };
  const g = units[u % units.length]!;
  return { at: slotAt(g, Math.floor(u / units.length)), yaw: g.yaw };
}

/**
 * Each batch's cell at its step, from the trace alone: a batch takes the
 * first cell free when it starts (its first member's service_start) and
 * frees it when its last member's next event comes (or never, if the trace
 * has none). More batches at once than cells (not from a valid run) share
 * the cell freed soonest.
 */
export function batchCells(trace: TraceArtifact, scene: Scene): ReadonlyMap<string, number> {
  type Batch = { id: string; node: string; start: number; end: number; order: number };
  const batches = new Map<string, Batch>();
  const open = new Map<string, Batch>();
  for (const e of trace.events) {
    const was = open.get(e.entity_id);
    if (was) {
      was.end = Math.max(was.end, e.time_tick);
      open.delete(e.entity_id);
    }
    if (e.type === "service_start" && e.batch_id !== undefined) {
      let b = batches.get(e.batch_id);
      if (!b) batches.set(e.batch_id, (b = { id: e.batch_id, node: e.node_id, start: e.time_tick, end: e.time_tick, order: batches.size }));
      b.start = Math.min(b.start, e.time_tick);
      open.set(e.entity_id, b);
    }
  }
  for (const b of open.values()) b.end = Number.POSITIVE_INFINITY;
  const out = new Map<string, number>();
  const byNode = new Map<string, Batch[]>();
  for (const b of batches.values()) byNode.set(b.node, [...(byNode.get(b.node) ?? []), b]);
  for (const [node, list] of byNode) {
    const st = scene.stations[node];
    const n = Math.max(1, st ? st.cells.length || st.units.length : 1);
    const until = new Array<number>(n).fill(Number.NEGATIVE_INFINITY);
    for (const b of list.sort((x, y) => x.start - y.start || x.order - y.order)) {
      let cell = until.findIndex((t) => t <= b.start);
      if (cell < 0) cell = until.indexOf(Math.min(...until));
      until[cell] = b.end;
      out.set(b.id, cell);
    }
  }
  return out;
}

/** A station's lights from its counts: per unit, busy, then changing over, then held, then idle; one light for a shared station. */
function lights(st: Station, n: NodeFrame | undefined): Light[] {
  if (st.lamps.length === 0) return [];
  const state = n?.state ?? "idle";
  if (st.shared) return [{ node: st.id, unit: 0, position: st.lamps[0]!, state }];
  const batched = (n?.batches.length ?? 0) > 0;
  const setup = n?.setup ?? 0;
  const busy = batched ? n!.batches.length : Math.max(0, (n?.inService ?? 0) - setup);
  const blocked = n?.blocked ?? 0;
  return st.lamps.map((position, unit) => ({
    node: st.id, unit, position,
    state: unit < busy ? "busy" : unit < busy + setup ? "setup" : unit < busy + setup + blocked ? "blocked" : "idle",
  }));
}

/** Where every board of a frame is drawn, and what every light shows. */
export function placeBoards(scene: Scene, frame: Frame, cells?: ReadonlyMap<string, number>): Placement {
  const boards: PlacedBoard[] = [];
  let over = 0;
  let missing = 0;
  // The boards drawn so far at a step in service and changing over (they come first: motion.ts orders by state).
  const running = new Map<string, { service: number; setup: number; batches: string[] }>();
  const filled = new Map<string, number>(); // `${node} ${cell}` → members placed
  for (const b of frame.boards) {
    const st = scene.stations[b.node];
    if (!st) {
      if (b.state !== "left") missing++;
      continue;
    }
    let spot: Vec3 | null;
    let yaw: number;
    const run = running.get(b.node) ?? { service: 0, setup: 0, batches: [] };
    running.set(b.node, run);
    switch (b.state) {
      case "queued":
      case "delay":
        spot = slotAt(st.wait, b.slot);
        yaw = st.wait.yaw;
        break;
      case "service":
      case "setup":
      case "blocked": {
        // An oven's held board waits at its way out; a station's keeps its unit (it holds the resource).
        if (b.state === "blocked" && st.cells.length > 0) {
          spot = slotAt(st.port, b.slot);
          yaw = st.port.yaw;
          break;
        }
        const u = b.state === "service" ? b.slot : b.state === "setup" ? run.service + b.slot : run.service + run.setup + b.slot;
        if (b.state === "service") run.service++;
        if (b.state === "setup") run.setup++;
        ({ at: spot, yaw } = unitSlot(st, u));
        break;
      }
      case "batch": {
        let cell = b.batch === null ? undefined : cells?.get(b.batch);
        if (cell === undefined) {
          // Without the trace's cells: the batch's place among those drawn here.
          const id = b.batch ?? "";
          if (!run.batches.includes(id)) run.batches.push(id);
          cell = run.batches.indexOf(id);
        }
        const grids = st.cells.length > 0 ? st.cells : st.units;
        if (grids.length === 0) {
          spot = slotAt(st.port, b.slot);
          yaw = st.port.yaw;
          break;
        }
        const key = `${b.node} ${cell % grids.length}`;
        const k = filled.get(key) ?? 0;
        filled.set(key, k + 1);
        const g = grids[cell % grids.length]!;
        spot = slotAt(g, k);
        yaw = g.yaw;
        break;
      }
      default:
        spot = slotAt(st.port, b.slot);
        yaw = st.port.yaw;
    }
    if (spot === null) {
      if (b.state !== "left") over++;
      continue;
    }
    if (b.hop) {
      const s = slide(scene, b, spot, yaw);
      boards.push({ id: b.id, node: b.node, state: b.state, position: s.p, yaw: s.yaw, sliding: true });
    } else boards.push({ id: b.id, node: b.node, state: b.state, position: spot, yaw, sliding: false });
  }
  const out: Light[] = [];
  for (const st of Object.values(scene.stations)) out.push(...lights(st, frame.nodes[st.id]));
  return { tick: frame.tick, boards, lights: out, notDrawn: frame.notDrawn + over, hidden: frame.hidden + missing, frame };
}
