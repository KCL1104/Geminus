// The 2D canvas's trace overlay, its pure part (H-072;
// docs/design/H-072_TRACE_ANIMATION.md §4–6; DESIGN.md, The Trace Motion
// Rule). No React and no DOM: trace-overlay.tsx paints what this computes.
//
// - Links are the canvas's own: each flow's polyline is read from the path
//   React Flow draws for it (getSmoothStepPath, flattened by pathPoints), so
//   a dot travels exactly on the link the reader sees.
// - A hop goes from one step to the next along the links between them (the
//   shortest way through the flows, in the IR's order, so through a decision
//   or a failure step the trace does not stop at), and under the cards it
//   passes. A hop the links do not join is not drawn: nothing moves where
//   the canvas has no link.
// - Between its events a board is not drawn at all on the 2D canvas: what
//   waits at a step is its count badge, what a machine does is its state
//   ring. Only a hop's 300 ms slide (motion.ts `Hop`) draws a dot.
// - Without a run, chevrons on each link's straight runs show the way the
//   line flows, and nothing moves.
import type { Frame, Hop, StationState } from "../playback/motion";

export type Point = { readonly x: number; readonly y: number };

/** A polyline with its cumulative lengths: lengths[i] is the distance from points[0] to points[i]. */
export type Polyline = { readonly points: readonly Point[]; readonly lengths: readonly number[]; readonly length: number };

/** Each rounded corner of a smoothstep path is flattened into this many lines. */
const CURVE_STEPS = 4;

/**
 * The points of an SVG path made of M, L and Q commands, as React Flow's
 * smoothstep edge writes it ("M172 20L 187,20Q 192,20 192,25L192 115…"):
 * every quadratic corner flattened into CURVE_STEPS lines. Anything else in
 * the path ends the reading there.
 */
export function pathPoints(d: string): Point[] {
  const tokens = d.match(/[A-Za-z]|-?(?:\d+\.?\d*|\.\d+)(?:e[-+]?\d+)?/g) ?? [];
  const out: Point[] = [];
  let i = 0;
  const num = () => (i < tokens.length && !/^[A-Za-z]$/.test(tokens[i]!) ? Number(tokens[i++]) : Number.NaN);
  const point = (): Point | null => {
    const x = num();
    const y = num();
    return Number.isFinite(x) && Number.isFinite(y) ? { x, y } : null;
  };
  while (i < tokens.length) {
    const command = tokens[i++]!;
    if (command === "M" || command === "L") {
      const p = point();
      if (p === null) break;
      out.push(p);
    } else if (command === "Q") {
      const c = point();
      const e = point();
      const s = out.at(-1);
      if (c === null || e === null || s === undefined) break;
      for (let k = 1; k <= CURVE_STEPS; k++) {
        const t = k / CURVE_STEPS;
        const a = (1 - t) * (1 - t);
        const b = 2 * (1 - t) * t;
        const q = t * t;
        out.push({ x: a * s.x + b * c.x + q * e.x, y: a * s.y + b * c.y + q * e.y });
      }
    } else {
      break;
    }
  }
  return out;
}

export function polyline(points: readonly Point[]): Polyline {
  const lengths: number[] = [];
  let total = 0;
  points.forEach((p, i) => {
    if (i > 0) total += Math.hypot(p.x - points[i - 1]!.x, p.y - points[i - 1]!.y);
    lengths.push(total);
  });
  return { points, lengths, length: total };
}

/** The point s of the way along a polyline (0 its start, 1 its end), and the rest of it from there. */
export function cut(line: Polyline, s: number): { readonly at: Point; readonly rest: readonly Point[] } {
  const { points, lengths, length } = line;
  if (points.length === 0) return { at: { x: 0, y: 0 }, rest: [] };
  const d = Math.min(Math.max(s, 0), 1) * length;
  let i = 1;
  while (i < points.length - 1 && lengths[i]! < d) i++;
  if (points.length === 1) return { at: points[0]!, rest: [points[0]!] };
  const a = points[i - 1]!;
  const b = points[i]!;
  const span = lengths[i]! - lengths[i - 1]!;
  const f = span > 0 ? (d - lengths[i - 1]!) / span : 1;
  const at = { x: a.x + (b.x - a.x) * f, y: a.y + (b.y - a.y) * f };
  return { at, rest: [at, ...points.slice(i)] };
}

export const pointAt = (line: Polyline, s: number): Point => cut(line, s).at;

/** A straight run shorter than this gets no chevron (the link's arrow is enough). */
export const CHEVRON_MIN = 40;
/** On a long straight run, about one chevron per this many canvas units. */
export const CHEVRON_GAP = 120;
/** A chevron's length along the link and half its width: the end arrow's shape (React Flow's arrow marker, "-5,-4 0,0 -5,4"). */
const CHEVRON_LENGTH = 5;
const CHEVRON_HALF = 4;

/** A link's straight runs: its segments, those that go on in the same direction joined (React Flow writes a straight link as several pieces). */
function runs(points: readonly Point[]): { a: Point; b: Point; dx: number; dy: number; len: number }[] {
  const out: { a: Point; b: Point; dx: number; dy: number; len: number }[] = [];
  for (let i = 1; i < points.length; i++) {
    const a = points[i - 1]!;
    const b = points[i]!;
    const len = Math.hypot(b.x - a.x, b.y - a.y);
    if (len === 0) continue;
    const dx = (b.x - a.x) / len + 0;
    const dy = (b.y - a.y) / len + 0;
    const last = out.at(-1);
    if (last && last.b.x === a.x && last.b.y === a.y && Math.abs(last.dx - dx) < 1e-9 && Math.abs(last.dy - dy) < 1e-9) {
      last.b = b;
      last.len += len;
    } else {
      out.push({ a, b, dx, dy, len });
    }
  }
  return out;
}

/** Where a link's chevrons go: on each straight run of at least CHEVRON_MIN, evenly spaced, each pointing the way the link runs. */
export function chevronMarks(points: readonly Point[]): { readonly x: number; readonly y: number; readonly dx: number; readonly dy: number }[] {
  const marks: { x: number; y: number; dx: number; dy: number }[] = [];
  for (const { a, b, dx, dy, len } of runs(points)) {
    if (len < CHEVRON_MIN) continue;
    const count = Math.max(1, Math.round(len / CHEVRON_GAP));
    for (let k = 0; k < count; k++) {
      const f = (k + 0.5) / count;
      marks.push({ x: a.x + (b.x - a.x) * f, y: a.y + (b.y - a.y) * f, dx, dy });
    }
  }
  return marks;
}

const r1 = (v: number) => Math.round(v * 10) / 10;

/** The chevrons of a link as one SVG path: open ">" marks centred on their places, pointing along the link. */
export function chevronPath(points: readonly Point[]): string {
  return chevronMarks(points)
    .map(({ x, y, dx, dy }) => {
      const tip = { x: x + (dx * CHEVRON_LENGTH) / 2, y: y + (dy * CHEVRON_LENGTH) / 2 };
      const back = { x: x - (dx * CHEVRON_LENGTH) / 2, y: y - (dy * CHEVRON_LENGTH) / 2 };
      // The normal (-dy, dx) spreads the two arms.
      const nx = -dy * CHEVRON_HALF;
      const ny = dx * CHEVRON_HALF;
      return `M${r1(back.x + nx)} ${r1(back.y + ny)}L${r1(tip.x)} ${r1(tip.y)}L${r1(back.x - nx)} ${r1(back.y - ny)}`;
    })
    .join("");
}

/** A flow of the model (an IR flow's ID and ends). */
export type FlowLink = { readonly id: string; readonly from: string; readonly to: string };

export type Router = {
  /** The polyline from step `from` to step `to` along the links between them, or null when no link joins them or a link on the way is not drawn yet. */
  route(from: string, to: string): Polyline | null;
  /** Forgets the polylines (a link was drawn again: a step moved); the ways through the flows stay. */
  clear(): void;
};

/**
 * Routes over the model's flows, with each link's polyline from `points`
 * (the canvas's drawn links). The way from one step to another is the one
 * with the fewest links, the flows taken in the IR's order (deterministic,
 * like the engine's routing order), so a hop through a decision, a failure
 * or a rework step follows its links.
 */
export function createRouter(flows: readonly FlowLink[], points: (flowId: string) => readonly Point[] | undefined): Router {
  const out = new Map<string, FlowLink[]>();
  for (const f of flows) out.set(f.from, [...(out.get(f.from) ?? []), f]);
  const ways = new Map<string, readonly string[] | null>();
  const lines = new Map<string, Polyline | null>();
  const key = (from: string, to: string) => `${from}\u0000${to}`;

  const way = (from: string, to: string): readonly string[] | null => {
    const k = key(from, to);
    const known = ways.get(k);
    if (known !== undefined) return known;
    // Breadth first from `from`: the first time `to` is reached is a fewest-links way.
    const via = new Map<string, FlowLink>();
    const queue = [from];
    const seen = new Set([from]);
    let found = false;
    for (let head = 0; head < queue.length && !found; head++) {
      for (const f of out.get(queue[head]!) ?? []) {
        if (seen.has(f.to)) continue;
        seen.add(f.to);
        via.set(f.to, f);
        if (f.to === to) {
          found = true;
          break;
        }
        queue.push(f.to);
      }
    }
    let result: string[] | null = null;
    if (found) {
      result = [];
      for (let at = to; at !== from; ) {
        const f = via.get(at)!;
        result.unshift(f.id);
        at = f.from;
      }
    }
    ways.set(k, result);
    return result;
  };

  return {
    route(from, to) {
      const k = key(from, to);
      const known = lines.get(k);
      if (known !== undefined) return known;
      const ids = from === to ? null : way(from, to);
      let line: Polyline | null = null;
      if (ids !== null) {
        const all: Point[] = [];
        for (const id of ids) {
          const p = points(id);
          if (p === undefined || p.length === 0) {
            all.length = 0;
            break;
          }
          // From one link's end (a step's left side) to the next one's start (its right side): under the card.
          all.push(...p);
        }
        line = all.length > 0 ? polyline(all) : null;
      }
      // A link that is not drawn yet is asked again next time.
      if (line !== null || ids === null) lines.set(k, line);
      return line;
    },
    clear() {
      lines.clear();
    },
  };
}

/**
 * Where a board on a hop is drawn (motion.ts `Hop`): steps[0] of the way
 * along the links from path[0] to path[1], then, for a hop that began
 * before the last ended, steps[1] of the way from there on to path[2], and
 * so on. Null when links do not join two steps of the hop.
 */
export function placeHop(hop: Hop, route: (from: string, to: string) => Polyline | null): Point | null {
  let rest: readonly Point[] = [];
  let at: Point | null = null;
  for (let i = 1; i < hop.path.length; i++) {
    const leg = route(hop.path[i - 1]!, hop.path[i]!);
    if (leg === null) return null;
    const way = rest.length > 0 ? polyline([...rest, ...leg.points]) : leg;
    const c = cut(way, hop.steps[i - 1] ?? 1);
    at = c.at;
    rest = c.rest;
  }
  return at;
}

/** What the overlay shows on a step card at a time. */
export type StepMark = {
  /** Sampled boards queued here: the count badge (none at 0). */
  readonly waiting: number;
  /** A machine's (process step's) state: its 2 px ring. Null for every other kind of step. */
  readonly state: StationState | null;
  /** The sampled members of the one batch in service here (D-054), or null when none is, or several are (the state says busy). */
  readonly batch: number | null;
};

export type OverlayFrame = {
  readonly tick: number;
  /** A dot per board on a hop, where the links put it. */
  readonly dots: readonly ({ readonly id: string } & Point)[];
  /** Per step of the canvas that the run knows. */
  readonly steps: ReadonlyMap<string, StepMark>;
  /** Boards beyond the drawing limit (motion.ts `notDrawn`). */
  readonly notDrawn: number;
  /** Sampled boards at a step this canvas does not have (the run's model, or the draft, differs): not drawn. */
  readonly hidden: number;
};

/** The most boards a stage draws (H-072 §5). */
export const DRAW_LIMIT = 2000;

/** The overlay at a frame of the motion: the canvas's steps (in the IR's order) and its routes. */
export function overlayFrame(frame: Frame, steps: readonly { readonly id: string; readonly type: string }[], route: (from: string, to: string) => Polyline | null): OverlayFrame {
  const onCanvas = new Set(steps.map((s) => s.id));
  const dots: ({ id: string } & Point)[] = [];
  for (const b of frame.boards) {
    if (b.hop === null || !b.hop.path.every((n) => onCanvas.has(n))) continue;
    const p = placeHop(b.hop, route);
    if (p !== null) dots.push({ id: b.id, x: p.x, y: p.y });
  }
  const marks = new Map<string, StepMark>();
  for (const s of steps) {
    const n = Object.hasOwn(frame.nodes, s.id) ? frame.nodes[s.id] : undefined;
    if (n === undefined) continue;
    const batch = n.batches.length === 1 ? n.batches[0]!.members : null;
    marks.set(s.id, { waiting: n.waiting, state: s.type === "process" ? n.state : null, batch: s.type === "process" ? batch : null });
  }
  // Boards at a step the run knows and the canvas does not are not drawn either.
  let hidden = frame.hidden;
  for (const n of Object.values(frame.nodes)) {
    if (!onCanvas.has(n.id)) hidden += n.waiting + n.inService + n.blocked + n.delayed + n.other;
  }
  return { tick: frame.tick, dots, steps: marks, notDrawn: frame.notDrawn, hidden };
}
