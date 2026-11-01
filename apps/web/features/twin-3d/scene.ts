// The read-only 3D projection of a model (T-033, H-072; ADR-008,
// docs/ARCHITECTURE.md §7): computed from the IR on every render, never
// stored, never edited. Each step is a station with the step's IR ID,
// placed where the 2D canvas places it (canvasLayout: views.2d.positions,
// layout_mode derived_from_2d), so there is no second topology; each flow is
// a conveyor between its steps, with an arrow for its direction.
//
// A step is drawn as its equipment (equipment.ts: the pack's own for the
// electronics line, else the generic shape of its node type), built from
// primitives by kits.ts and turned to its flow: a queue, a delay and a
// return lane along the flow out of them, a station square to the flow
// into it. The stations also say where their boards go (Station): the
// motion (boards.ts) only reads them.
//
// A run's results, when given, colour each process by the utilization of
// the resources it requires and raise a column on each queue by its mean
// length (T-033); the line in motion (H-072) is drawn without them. Pure:
// PlayCanvas only draws what this returns (engine.ts).
import type { ModelIR, Result } from "../../lib/contracts";
import { canvasLayout } from "../model-editor/layout";
import { type EquipmentKind, equipmentOf } from "./equipment";
import { BELT, kit, type LocalGrid, type Shape, type Tone, type Vec3 } from "./kits";

export type { Shape, Tone, Vec3 } from "./kits";
type NodeType = ModelIR["nodes"][number]["type"];

export type SceneNode = {
  id: string;
  type: NodeType;
  label: string;
  kind: EquipmentKind;
  /** The station's box: its centre and full size, in world units (y is up); picking and the first view use it. */
  position: Vec3;
  size: Vec3;
  /** Degrees about y the box is turned (0 when the station is square to the axes). */
  rotationY: number;
  /** The run's mean utilization of the resources the step requires (the highest), 0–1; null without results. */
  utilization: number | null;
  /** The run's mean queue length at the step, null without results. */
  queueLength: number | null;
};

export type SceneEdge = { id: string; from: string; to: string; start: Vec3; end: Vec3 };

/** One primitive of the scene: a step's (`node`) or a flow's conveyor (null). */
export type Part = { readonly node: string | null; readonly shape: Shape; readonly tone: Tone; readonly position: Vec3; readonly size: Vec3; readonly yaw: number };

/** A stack of boards in world space (kits.ts LocalGrid, placed): slot 0's centre, the way the slots run and the way across. */
export type Grid = {
  readonly at: Vec3;
  readonly along: readonly [number, number];
  readonly across: readonly [number, number];
  readonly cols: number;
  readonly rows: number;
  readonly layers: number;
  /** The boards' turn, degrees about y. */
  readonly yaw: number;
};

/** Where a step's boards go, in world space (boards.ts places them). */
export type Station = {
  readonly id: string;
  readonly kind: EquipmentKind;
  readonly type: NodeType;
  /** Degrees about y: the kit's +x points along the flow. */
  readonly yaw: number;
  /** Where a slide starts, passes and ends: the step's centre at belt height. */
  readonly hub: Vec3;
  readonly wait: Grid;
  readonly port: Grid;
  readonly units: readonly Grid[];
  readonly cells: readonly Grid[];
  /** Each light's centre, the first units first. */
  readonly lamps: readonly Vec3[];
  /** One light for the station's state (an oven, a bank too large to draw unit by unit). */
  readonly shared: boolean;
};

/**
 * columns: a run's results are shown, so playback may raise a column on any
 * step (T-034, engine.ts setOccupancy). links: each step's next steps, in the
 * IR's flow order (a slide follows them, boards.ts).
 */
export type Scene = {
  nodes: SceneNode[];
  edges: SceneEdge[];
  center: Vec3;
  radius: number;
  columns: boolean;
  parts: Part[];
  stations: Record<string, Station>;
  links: Record<string, readonly string[]>;
};

// One 2D layer (240 px) is 4 units, one row (120 px) is 3.
const PER_X = 60;
const PER_Z = 40;

/** The top of a flow's conveyor: just under a station's belt, and a hair apart from the next flow's, so crossings never flicker. */
export const FLOW_TOP = BELT - 0.01;

/** How much a queue's column grows per entity waiting, and its cap (T-033). */
const QUEUE_RISE = 0.15;
const QUEUE_MAX = 3;

/** The kinds that run with their flow out (conveyor-like), not square to their flow in. */
const ALONG: ReadonlySet<EquipmentKind> = new Set(["buffer", "conveyor", "return"]);

const rad = (deg: number) => (deg * Math.PI) / 180;
/** The turn about y that points a kit's +x along (dx, dz). */
const heading = (dx: number, dz: number) => (Math.atan2(-dz, dx) * 180) / Math.PI;
const tidy = (deg: number) => {
  const d = Math.round(deg * 1e6) / 1e6;
  return d === 0 ? 0 : d; // no -0
};

/** Turns a kit's point (x, z) by yaw degrees and places it at (cx, cz). */
function turn(yaw: number, cx: number, cz: number, x: number, z: number): [number, number] {
  const a = rad(yaw);
  const c = Math.cos(a);
  const s = Math.sin(a);
  return [cx + x * c + z * s, cz - x * s + z * c];
}
const turnDir = (yaw: number, [x, z]: readonly [number, number]): [number, number] => {
  const [a, b] = turn(yaw, 0, 0, x, z);
  return [Math.round(a * 1e9) / 1e9 || 0, Math.round(b * 1e9) / 1e9 || 0];
};

function place(g: LocalGrid, yaw: number, cx: number, cz: number): Grid {
  const [x, z] = turn(yaw, cx, cz, g.at[0], g.at[2]);
  return { at: [x, g.at[1], z], along: turnDir(yaw, g.along), across: turnDir(yaw, [-g.along[1], g.along[0]]), cols: g.cols, rows: g.rows, layers: g.layers, yaw: tidy(yaw) };
}

export function buildScene(ir: ModelIR, results?: Result | null): Scene {
  const positions = canvasLayout(ir);
  const metrics = results?.metrics;
  const kinds = equipmentOf(ir);
  const first = new Map<string, ModelIR["nodes"][number]>();
  for (const n of ir.nodes) if (!first.has(n.id)) first.set(n.id, n);
  const at = (id: string) => {
    const p = positions[id] ?? { x: 0, y: 0 };
    return { x: p.x / PER_X, z: p.y / PER_Z };
  };
  const links: Record<string, string[]> = {};
  const into = new Map<string, string>();
  for (const f of ir.flows) {
    if (!first.has(f.from) || !first.has(f.to)) continue;
    (links[f.from] ??= []).push(f.to);
    if (!into.has(f.to)) into.set(f.to, f.from);
  }
  // A step's turn: a conveyor-like step along its first flow out, a station square to its first flow in (or out).
  const dir = (from: { x: number; z: number }, to: { x: number; z: number }) => (from.x === to.x && from.z === to.z ? null : heading(to.x - from.x, to.z - from.z));
  const yawOf = (id: string, kind: EquipmentKind): number => {
    if (kind === "junction" || kind === "reject") return 0;
    const p = at(id);
    const out = links[id]?.[0];
    const inn = into.get(id);
    const forward = out !== undefined ? dir(p, at(out)) : null;
    const backward = inn !== undefined ? dir(at(inn), p) : null;
    if (ALONG.has(kind)) return tidy(forward ?? backward ?? 0);
    return tidy(Math.round((backward ?? forward ?? 0) / 90) * 90);
  };

  const parts: Part[] = [];
  const stations: Record<string, Station> = {};
  const nodes: SceneNode[] = ir.nodes.map((n) => {
    const { x, z } = at(n.id);
    const equipment = kinds.get(n.id) ?? { kind: "machine" as const, resource: null, units: 1 };
    const k = kit(equipment.kind, equipment.units);
    const yaw = yawOf(n.id, equipment.kind);
    let utilization: number | null = null;
    let queueLength: number | null = null;
    if (metrics && n.type === "process") {
      for (const r of n.requires ?? []) {
        const u = metrics.resources[r.resource_id]?.utilization_mean;
        if (u !== null && u !== undefined && Number.isFinite(u)) utilization = Math.max(utilization ?? 0, Math.min(1, Math.max(0, u)));
      }
    }
    if (metrics && n.type === "queue") {
      const q = metrics.queues[n.id];
      if (q && q.status === "available" && Number.isFinite(q.queue_length_mean)) queueLength = Math.max(0, q.queue_length_mean);
    }
    const rise = queueLength === null ? 0 : Math.min(QUEUE_MAX, queueLength * QUEUE_RISE);
    const height = Math.max(k.height, BELT + rise);
    // A quarter turn swaps the box's sides; any other turn keeps it turned (picking and framing take its circle).
    const quarter = Math.abs(Math.round(yaw / 90) * 90 - yaw) < 1e-6;
    const odd = quarter && Math.abs(Math.round(yaw / 90)) % 2 === 1;
    const size: Vec3 = odd ? [2 * k.half[1], height, 2 * k.half[0]] : [2 * k.half[0], height, 2 * k.half[1]];
    // A draft may repeat an ID; the first stands, as in the layout (review r1).
    if (first.get(n.id) === n) {
      for (const p of k.parts) {
        const [px, pz] = turn(yaw, x, z, p.at[0], p.at[2]);
        parts.push({ node: n.id, shape: p.shape, tone: p.tone, position: [px, p.at[1], pz], size: p.size, yaw: tidy(yaw) });
      }
      if (rise > 0) parts.push({ node: n.id, shape: "box", tone: "queue", position: [x, BELT + rise / 2, z], size: [1.3, rise, 0.66], yaw: tidy(yaw) });
      stations[n.id] = {
        id: n.id, kind: equipment.kind, type: n.type, yaw: tidy(yaw), hub: [x, BELT, z],
        wait: place(k.wait, yaw, x, z), port: place(k.port, yaw, x, z),
        units: k.units.map((g) => place(g, yaw, x, z)), cells: k.cells.map((g) => place(g, yaw, x, z)),
        lamps: k.lamps.map((l) => {
          const [lx, lz] = turn(yaw, x, z, l[0], l[2]);
          return [lx, l[1], lz] as Vec3;
        }),
        shared: k.shared,
      };
    }
    return {
      id: n.id, type: n.type, label: n.name ?? n.id, kind: equipment.kind, size, rotationY: quarter ? 0 : tidy(yaw), utilization, queueLength,
      position: [x, height / 2, z],
    };
  });

  const byId = new Map<string, SceneNode>();
  for (const n of nodes) if (!byId.has(n.id)) byId.set(n.id, n);
  const edges: SceneEdge[] = [];
  ir.flows.forEach((f, i) => {
    const a = byId.get(f.from);
    const b = byId.get(f.to);
    if (!a || !b) return;
    const y = FLOW_TOP - (i % 4) * 0.003;
    edges.push({ id: f.id, from: a.id, to: b.id, start: [a.position[0], y, a.position[2]], end: [b.position[0], y, b.position[2]] });
  });
  const unique = [...byId.values()];
  for (const e of edges) conveyor(parts, e, unique);

  const columns = results != null;
  if (nodes.length === 0) return { nodes, edges, center: [0, 0, 0], radius: 5, columns, parts, stations, links };
  const xs = nodes.map((n) => n.position[0]);
  const zs = nodes.map((n) => n.position[2]);
  const [minX, maxX, minZ, maxZ] = [Math.min(...xs), Math.max(...xs), Math.min(...zs), Math.max(...zs)];
  // With results the view looks halfway up the tallest column, which only rises: the line and the columns share the view.
  const center: Vec3 = [(minX + maxX) / 2, columns ? COLUMN_MAX / 2 : 0, (minZ + maxZ) / 2];
  const radius = Math.max(5, Math.hypot(maxX - minX, maxZ - minZ) / 2 + 2);
  return { nodes, edges, center, radius, columns, parts, stations, links };
}

/** A step's footprint half-widths in x and z (a turned box by its circle). */
function footprint(n: SceneNode): [number, number] {
  if (n.rotationY === 0) return [n.size[0] / 2, n.size[2] / 2];
  const r = Math.hypot(n.size[0], n.size[2]) / 2;
  return [r, r];
}

/** How far from a step's centre a line leaves its footprint, going along (dx, dz) (a unit vector). */
function exit(n: SceneNode, dx: number, dz: number): number {
  const [hx, hz] = footprint(n);
  const tx = Math.abs(dx) < 1e-9 ? Infinity : hx / Math.abs(dx);
  const tz = Math.abs(dz) < 1e-9 ? Infinity : hz / Math.abs(dz);
  return Math.min(tx, tz);
}

/** Whether (x, z) is on a step's footprint, or within `margin` of it. */
export function covers(n: SceneNode, x: number, z: number, margin = 0): boolean {
  const [hx, hz] = footprint(n);
  return Math.abs(x - n.position[0]) <= hx + margin && Math.abs(z - n.position[2]) <= hz + margin;
}

/** The arrow's tip on a flow: within the stretch between its steps, the middle unless another step covers it. */
export function arrowAt(e: SceneEdge, nodes: readonly SceneNode[]): Vec3 | null {
  const a = nodes.find((n) => n.id === e.from);
  const b = nodes.find((n) => n.id === e.to);
  const dx = e.end[0] - e.start[0];
  const dz = e.end[2] - e.start[2];
  const length = Math.hypot(dx, dz);
  if (!a || !b || length < 1e-6) return null;
  const ux = dx / length;
  const uz = dz / length;
  const t0 = exit(a, ux, uz);
  const t1 = length - exit(b, -ux, -uz);
  const others = nodes.filter((n) => n.id !== a.id && n.id !== b.id);
  const at = (f: number): Vec3 => {
    const t = t1 > t0 ? t0 + (t1 - t0) * f : length / 2;
    return [e.start[0] + ux * t, e.start[1], e.start[2] + uz * t];
  };
  for (const f of [0.5, 0.35, 0.65, 0.2, 0.8, 0.1, 0.9]) {
    const p = at(f);
    if (!others.some((n) => covers(n, p[0], p[2], 0.2))) return p;
  }
  return at(0.5);
}

/** The length of a chevron's arm, and its turn either side of the flow. */
const ARM = 0.26;
const SPLAY = 40;

/** A flow's conveyor: frame and belt from step to step, and a chevron pointing downstream. */
function conveyor(parts: Part[], e: SceneEdge, nodes: readonly SceneNode[]) {
  const dx = e.end[0] - e.start[0];
  const dz = e.end[2] - e.start[2];
  const length = Math.hypot(dx, dz);
  if (length < 1e-6) return;
  const yaw = tidy(heading(dx, dz));
  const mx = (e.start[0] + e.end[0]) / 2;
  const mz = (e.start[2] + e.end[2]) / 2;
  const top = e.start[1];
  parts.push({ node: null, shape: "box", tone: "trim", position: [mx, (top - 0.04) / 2, mz], size: [length, top - 0.04, 0.48], yaw });
  parts.push({ node: null, shape: "box", tone: "belt", position: [mx, top - 0.02, mz], size: [length, 0.04, 0.38], yaw });
  const tip = arrowAt(e, nodes);
  if (!tip) return;
  // Two arms back from the tip, SPLAY degrees either side of the flow: a ">" lying on the belt.
  for (const s of [-1, 1]) {
    const arm = yaw + 180 + s * SPLAY;
    const ax = Math.cos(rad(arm));
    const az = -Math.sin(rad(arm));
    parts.push({ node: null, shape: "box", tone: "trim", position: [tip[0] + (ax * ARM) / 2, top + 0.006, tip[2] + (az * ARM) / 2], size: [ARM, 0.012, 0.045], yaw: tidy(arm) });
  }
}

/** The selection frame's bar width and its gap from the step's footprint. */
const FRAME = 0.07;
const FRAME_GAP = 0.12;

/**
 * The frame on the floor around a selected step (DESIGN.md: the selected
 * step in Graphite Ink, as the 2D card's border): four bars just outside its
 * footprint, turned with it, under its boards and lights, so nothing of the
 * station is hidden.
 */
export function selectionFrame(n: SceneNode): Part[] {
  const hx = n.size[0] / 2 + FRAME_GAP;
  const hz = n.size[2] / 2 + FRAME_GAP;
  const bar = (x: number, z: number, sx: number, sz: number): Part => {
    const [px, pz] = turn(n.rotationY, n.position[0], n.position[2], x, z);
    return { node: n.id, shape: "box", tone: "trim", position: [px, 0.01, pz], size: [sx, 0.02, sz], yaw: n.rotationY };
  };
  return [bar(0, -hz, 2 * hx + FRAME, FRAME), bar(0, hz, 2 * hx + FRAME, FRAME), bar(-hx, 0, FRAME, 2 * hz - FRAME), bar(hx, 0, FRAME, 2 * hz - FRAME)];
}

/**
 * The step a ray hits first (origin, direction in world space): each step
 * is tested as its axis-aligned box (a turned one by the box around it),
 * null when the ray misses every step.
 */
export function pick(scene: Scene, origin: Vec3, direction: Vec3): string | null {
  let best: { id: string; t: number } | null = null;
  for (const n of scene.nodes) {
    const half = n.rotationY === 0 ? n.size.map((s) => s / 2) : [Math.hypot(n.size[0], n.size[2]) / 2, n.size[1] / 2, Math.hypot(n.size[0], n.size[2]) / 2];
    let tMin = -Infinity;
    let tMax = Infinity;
    let hit = true;
    for (let i = 0; i < 3 && hit; i++) {
      const lo = n.position[i]! - half[i]!;
      const hi = n.position[i]! + half[i]!;
      const o = origin[i]!;
      const d = direction[i]!;
      if (Math.abs(d) < 1e-12) {
        if (o < lo || o > hi) hit = false;
        continue;
      }
      const t1 = (lo - o) / d;
      const t2 = (hi - o) / d;
      tMin = Math.max(tMin, Math.min(t1, t2));
      tMax = Math.min(tMax, Math.max(t1, t2));
      if (tMin > tMax) hit = false;
    }
    if (hit && tMax >= 0) {
      const t = Math.max(tMin, 0);
      if (best === null || t < best.t) best = { id: n.id, t };
    }
  }
  return best?.id ?? null;
}

/** The camera's vertical field of view and the first view's pitch, in degrees. */
export const FOV = 40;
export const START_PITCH = 52;
/** A playback column's height per item at its step, and its cap (engine.ts draws them). */
export const ITEM_RISE = 0.4;
export const COLUMN_MAX = 5;
/** The share of the view the line may take in the first view: the rest is margin. */
const FILL = 0.9;

/**
 * Every corner the first view must hold: each step's box (a turned one by
 * its circle) and, with results, the tallest column playback can raise on it.
 * In the first view's camera space (yaw 0, START_PITCH, looking at the
 * scene's centre): across, up, and the depth towards the camera.
 */
function corners(scene: Scene): { x: number; y: number; depth: number }[] {
  const pitch = (START_PITCH * Math.PI) / 180;
  const out: { x: number; y: number; depth: number }[] = [];
  for (const n of scene.nodes) {
    const r = n.rotationY === 0 ? null : Math.hypot(n.size[0], n.size[2]) / 2;
    const hx = r ?? n.size[0] / 2;
    const hz = r ?? n.size[2] / 2;
    const bottom = n.position[1] - n.size[1] / 2;
    const top = n.position[1] + n.size[1] / 2 + (scene.columns ? COLUMN_MAX : 0);
    for (const dx of [-hx, hx])
      for (const dz of [-hz, hz])
        for (const y of [bottom, top]) {
          const v = [n.position[0] + dx - scene.center[0], y - scene.center[1], n.position[2] + dz - scene.center[2]] as const;
          out.push({ x: v[0], y: v[1] * Math.cos(pitch) - v[2] * Math.sin(pitch), depth: v[1] * Math.sin(pitch) + v[2] * Math.cos(pitch) });
        }
  }
  return out;
}

/**
 * The camera distance at which the first view holds every corner within
 * FILL of a view of this width-to-height, in perspective: a corner nearer
 * the camera needs more room (review of H-039, M1).
 */
export function fitDistance(scene: Scene, aspect: number): number {
  const t = Math.tan(((FOV / 2) * Math.PI) / 180) * FILL;
  let d = 4;
  for (const c of corners(scene)) d = Math.max(d, c.depth + Math.abs(c.x) / (t * aspect), c.depth + Math.abs(c.y) / t);
  return d;
}

/** The width-to-height of the view the line fills best: its corners' spread across and up, seen from where a square view would hold them. */
export function stageAspect(scene: Scene): number {
  const d = fitDistance(scene, 1);
  let across = 0;
  let up = 0;
  for (const c of corners(scene)) {
    across = Math.max(across, Math.abs(c.x) / (d - c.depth));
    up = Math.max(up, Math.abs(c.y) / (d - c.depth));
  }
  return up === 0 ? 1 : across / up;
}
