// The read-only 3D projection of a model (T-033; ADR-008, docs/ARCHITECTURE.md
// §7): computed from the IR on every render, never stored, never edited.
// Each step is one object with the step's IR ID, placed where the 2D canvas
// places it (canvasLayout: views.2d.positions, layout_mode derived_from_2d),
// so there is no second topology; each flow is one line between its steps.
// A run's results, when given, colour each process by the utilization of the
// resources it requires and raise each queue by its mean length. Pure:
// PlayCanvas only draws what this returns (viewer.tsx).
import type { ModelIR, Result } from "../../lib/contracts";
import { canvasLayout } from "../model-editor/layout";

export type Vec3 = [number, number, number];
type NodeType = ModelIR["nodes"][number]["type"];

export type Shape = "box" | "cylinder";

export type SceneNode = {
  id: string;
  type: NodeType;
  label: string;
  shape: Shape;
  /** The object's centre and full size, in world units (y is up). */
  position: Vec3;
  size: Vec3;
  /** Degrees about y (a decision is a box turned 45°). */
  rotationY: number;
  /** The run's mean utilization of the resources the step requires (the highest), 0–1; null without results. */
  utilization: number | null;
  /** The run's mean queue length at the step, null without results. */
  queueLength: number | null;
};

export type SceneEdge = { id: string; from: string; to: string; start: Vec3; end: Vec3 };

export type Scene = { nodes: SceneNode[]; edges: SceneEdge[]; center: Vec3; radius: number };

// One 2D layer (240 px) is 4 units, one row (120 px) is 3.
const PER_X = 60;
const PER_Z = 40;

const shapes: Record<NodeType, { shape: Shape; size: Vec3; rotationY?: number }> = {
  source: { shape: "cylinder", size: [1.2, 0.5, 1.2] },
  sink: { shape: "cylinder", size: [1.2, 0.5, 1.2] },
  queue: { shape: "box", size: [1.8, 0.3, 1] },
  process: { shape: "box", size: [1.6, 1.2, 1.6] },
  delay: { shape: "box", size: [1.4, 0.8, 1.4] },
  decision: { shape: "box", size: [1, 0.8, 1], rotationY: 45 },
  failure: { shape: "box", size: [1.2, 0.8, 1.2] },
  rework: { shape: "box", size: [1.2, 0.8, 1.2] },
};

/** How much a queue's height grows per entity waiting, and its cap. */
const QUEUE_RISE = 0.15;
const QUEUE_MAX = 3;

export function buildScene(ir: ModelIR, results?: Result | null): Scene {
  const positions = canvasLayout(ir);
  const metrics = results?.metrics;
  const nodes: SceneNode[] = ir.nodes.map((n) => {
    const p = positions[n.id] ?? { x: 0, y: 0 };
    const spec = shapes[n.type];
    let utilization: number | null = null;
    let queueLength: number | null = null;
    if (metrics && n.type === "process") {
      for (const r of n.requires) {
        const u = metrics.resources[r.resource_id]?.utilization_mean;
        if (u !== null && u !== undefined && Number.isFinite(u)) utilization = Math.max(utilization ?? 0, Math.min(1, Math.max(0, u)));
      }
    }
    if (metrics && n.type === "queue") {
      const q = metrics.queues[n.id];
      if (q && q.status === "available" && Number.isFinite(q.queue_length_mean)) queueLength = Math.max(0, q.queue_length_mean);
    }
    const size: Vec3 = [...spec.size];
    if (queueLength !== null) size[1] = Math.min(QUEUE_MAX, spec.size[1] + queueLength * QUEUE_RISE);
    return {
      id: n.id, type: n.type, label: n.name ?? n.id, shape: spec.shape, size, rotationY: spec.rotationY ?? 0, utilization, queueLength,
      position: [p.x / PER_X, size[1] / 2, p.y / PER_Z],
    };
  });
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const edges: SceneEdge[] = [];
  for (const f of ir.flows) {
    const a = byId.get(f.from);
    const b = byId.get(f.to);
    if (a && b) edges.push({ id: f.id, from: a.id, to: b.id, start: [a.position[0], 0.05, a.position[2]], end: [b.position[0], 0.05, b.position[2]] });
  }
  if (nodes.length === 0) return { nodes, edges, center: [0, 0, 0], radius: 5 };
  const xs = nodes.map((n) => n.position[0]);
  const zs = nodes.map((n) => n.position[2]);
  const [minX, maxX, minZ, maxZ] = [Math.min(...xs), Math.max(...xs), Math.min(...zs), Math.max(...zs)];
  const center: Vec3 = [(minX + maxX) / 2, 0, (minZ + maxZ) / 2];
  const radius = Math.max(5, Math.hypot(maxX - minX, maxZ - minZ) / 2 + 2);
  return { nodes, edges, center, radius };
}

/**
 * The step a ray hits first (origin, direction in world space): each step
 * is tested as its axis-aligned box (a turned decision by the box around
 * it), null when the ray misses every step.
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
