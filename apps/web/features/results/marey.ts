// The Marey chart's geometry (T-032, DESIGN.md Marey Strip): time runs left
// to right, the model's steps stand top to bottom, and every sampled work
// item is one line through them. A line goes flat while its item waits and
// runs to the next step while the item is served or delayed. Pure and
// deterministic: the same model, trace and width give the same chart, with
// no map order, clock or randomness involved (AGENTS.md §5).
//
// Only what the sampled trace holds is drawn (SIMULATION_SPEC §9): an item
// still waiting when the run ended runs flat to the end, but nothing is
// extended past a truncated trace, and an event that cannot be placed ends
// the line instead of being guessed.
import type { ModelIR, TraceArtifact, TraceEvent } from "../../lib/contracts";

export type StepRow = { id: string; name: string; sink: boolean };

type IRNode = ModelIR["nodes"][number];
const rowTypes: ReadonlySet<IRNode["type"]> = new Set(["source", "process", "delay", "sink"]);

/**
 * The rows, top to bottom: the model's sources, processes, delays and sinks
 * (queues and routing nodes are not steps) in flow order. Rank is the
 * longest path from a source, ignoring the edges that loop back (rework);
 * ties keep the model's own order; every sink comes after every other step.
 */
export function stepRows(ir: ModelIR): StepRow[] {
  const index = new Map(ir.nodes.map((n, i) => [n.id, i]));
  const out = new Map<string, string[]>(ir.nodes.map((n) => [n.id, []]));
  for (const f of ir.flows) if (index.has(f.from) && index.has(f.to)) out.get(f.from)!.push(f.to);

  // Back edges by depth-first search from the sources, then the rest, in model order.
  const colour = new Map<string, "grey" | "black">();
  const back = new Set<string>();
  const visit = (start: string) => {
    const stack: { id: string; next: number }[] = [{ id: start, next: 0 }];
    colour.set(start, "grey");
    while (stack.length > 0) {
      const top = stack[stack.length - 1]!;
      const targets = out.get(top.id)!;
      if (top.next >= targets.length) {
        colour.set(top.id, "black");
        stack.pop();
        continue;
      }
      const to = targets[top.next++]!;
      const c = colour.get(to);
      if (c === "grey") back.add(`${top.id}\u0000${to}`);
      else if (c === undefined) {
        colour.set(to, "grey");
        stack.push({ id: to, next: 0 });
      }
    }
  };
  for (const n of ir.nodes) if (n.type === "source" && !colour.has(n.id)) visit(n.id);
  for (const n of ir.nodes) if (!colour.has(n.id)) visit(n.id);

  // Longest path over the forward edges (Kahn, ready nodes in model order).
  const indegree = new Map(ir.nodes.map((n) => [n.id, 0]));
  const forward = new Map<string, string[]>();
  for (const [from, targets] of out) {
    const fw = targets.filter((to) => !back.has(`${from}\u0000${to}`));
    forward.set(from, fw);
    for (const to of fw) indegree.set(to, indegree.get(to)! + 1);
  }
  const rank = new Map(ir.nodes.map((n) => [n.id, 0]));
  const ready = ir.nodes.filter((n) => indegree.get(n.id) === 0).map((n) => n.id);
  while (ready.length > 0) {
    ready.sort((a, b) => index.get(a)! - index.get(b)!);
    const id = ready.shift()!;
    for (const to of forward.get(id)!) {
      rank.set(to, Math.max(rank.get(to)!, rank.get(id)! + 1));
      indegree.set(to, indegree.get(to)! - 1);
      if (indegree.get(to) === 0) ready.push(to);
    }
  }

  const order = (a: IRNode, b: IRNode) => rank.get(a.id)! - rank.get(b.id)! || index.get(a.id)! - index.get(b.id)!;
  const steps = ir.nodes.filter((n) => rowTypes.has(n.type));
  const sorted = [...steps.filter((n) => n.type !== "sink").sort(order), ...steps.filter((n) => n.type === "sink").sort(order)];
  return sorted.map((n) => ({ id: n.id, name: n.name ?? n.id, sink: n.type === "sink" }));
}

/**
 * The row an event's node puts its item in: the node's own row; for a
 * queue, the step it feeds (following queues), when there is exactly one;
 * otherwise null (a routing node, a queue with several exits, an unknown node).
 */
export function rowResolver(ir: ModelIR, rows: StepRow[]): (nodeId: string) => number | null {
  const rowIndex = new Map(rows.map((r, i) => [r.id, i]));
  const types = new Map(ir.nodes.map((n) => [n.id, n.type]));
  const exits = new Map<string, string[]>();
  for (const f of ir.flows) exits.set(f.from, [...(exits.get(f.from) ?? []), f.to]);
  const resolve = (id: string, seen: Set<string>): number | null => {
    const own = rowIndex.get(id);
    if (own !== undefined) return own;
    if (types.get(id) !== "queue" || seen.has(id)) return null;
    const next = exits.get(id) ?? [];
    if (next.length !== 1) return null;
    seen.add(id);
    return resolve(next[0]!, seen);
  };
  const cache = new Map<string, number | null>();
  return (id) => {
    if (!cache.has(id)) cache.set(id, resolve(id, new Set()));
    return cache.get(id)!;
  };
}

/** Where a point of a line is: in a step's row, or held between two steps (blocked after service). */
export type Place = { row: number } | { between: [number, number] };
export type PathPoint = { t: number; place: Place };
export type EntityPath = {
  entity: string;
  points: PathPoint[];
  /** Where the item left the line (a sink), if it did. */
  release: { t: number; row: number; outcome: "complete" | "abandon" } | null;
  /** Still waiting (or held) when the run ended: the line was drawn flat to the end. */
  waitingAtEnd: boolean;
};

const samePlace = (a: Place, b: Place) =>
  "row" in a ? "row" in b && a.row === b.row : "between" in b && a.between[0] === b.between[0] && a.between[1] === b.between[1];

const locating: ReadonlySet<TraceEvent["type"]> = new Set(["queue_enter", "service_start", "delay_start", "complete", "abandon"]);

/** One line per sampled item, in the order the items first appear in the trace (the engine's order). */
export function entityPaths(trace: TraceArtifact, rowOf: (nodeId: string) => number | null, endTick: number): EntityPath[] {
  const byEntity = new Map<string, TraceEvent[]>();
  for (const e of trace.events) {
    const list = byEntity.get(e.entity_id);
    if (list) list.push(e);
    else byEntity.set(e.entity_id, [e]);
  }
  const lines: EntityPath[] = [];
  for (const [entity, events] of byEntity) {
    const points: PathPoint[] = [];
    let row: number | null = null;
    let transit: { from: number; end: number | null } | null = null;
    let held: { to: number | null } | null = null;
    let release: EntityPath["release"] = null;
    let lost = false;
    let left = false;
    const push = (t: number, place: Place) => {
      const last = points[points.length - 1];
      if (last && last.t === t && samePlace(last.place, place)) return;
      points.push({ t, place });
    };
    const enter = (t: number, r: number) => {
      if (transit) {
        if (held) {
          const between: Place = { between: [transit.from, r] };
          push(transit.end ?? t, between);
          push(held.to ?? t, between);
        }
        push(t, { row: r });
        transit = null;
        held = null;
      } else {
        if (row !== null && row !== r) push(t, { row });
        push(t, { row: r });
      }
      row = r;
    };
    const ahead = (from: number): number | null => {
      for (let j = from; j < events.length; j++) {
        const e = events[j]!;
        if (!locating.has(e.type)) continue;
        const r = rowOf(e.node_id);
        if (r !== null) return r;
      }
      return null;
    };
    for (let i = 0; i < events.length && !lost && !left; i++) {
      const e = events[i]!;
      const t = e.time_tick;
      switch (e.type) {
        case "arrival": {
          const r = rowOf(e.node_id);
          if (r === null) lost = true;
          else enter(t, r);
          break;
        }
        case "queue_enter": {
          const r = rowOf(e.node_id) ?? ahead(i + 1);
          if (r === null) lost = true;
          else enter(t, r);
          break;
        }
        case "service_start":
        case "delay_start": {
          const r = rowOf(e.node_id);
          if (r === null) {
            lost = true;
            break;
          }
          if (transit || row !== r) enter(t, r);
          push(t, { row: r });
          transit = { from: r, end: null };
          break;
        }
        case "service_end":
        case "delay_end":
          if (transit) transit.end = t;
          break;
        case "blocked":
          if (transit && transit.end !== null) held = { to: null };
          break;
        case "unblocked":
          if (held) held.to = t;
          break;
        case "complete":
        case "abandon": {
          const r = rowOf(e.node_id);
          if (r === null) {
            lost = true;
            break;
          }
          enter(t, r);
          release = { t, row: r, outcome: e.type };
          left = true;
          break;
        }
        default:
          // failure, rework and setup_start happen in place: the next located event says where the item went.
          break;
      }
    }
    let waitingAtEnd = false;
    const last = points[points.length - 1];
    if (!trace.truncated && !lost && !left && last && last.t <= endTick) {
      const current = transit as { from: number; end: number | null } | null;
      if (current === null && row !== null) {
        push(endTick, { row });
        waitingAtEnd = true;
      } else if (current !== null && held !== null && current.end !== null) {
        const between: Place = { between: [current.from, current.from + 1] };
        push(current.end, between);
        push(endTick, between);
        waitingAtEnd = true;
      }
    }
    lines.push({ entity, points, release, waitingAtEnd });
  }
  return lines;
}

type Stay = { line: number; first: number; last: number; start: number; end: number };

/** The stays of each line in each row: consecutive points in the same row. */
function stays(lines: EntityPath[]): Map<number, Stay[]> {
  const byRow = new Map<number, Stay[]>();
  lines.forEach((line, li) => {
    let i = 0;
    while (i < line.points.length) {
      const p = line.points[i]!;
      if (!("row" in p.place)) {
        i++;
        continue;
      }
      let j = i;
      while (j + 1 < line.points.length) {
        const q = line.points[j + 1]!.place;
        if (!("row" in q) || q.row !== p.place.row) break;
        j++;
      }
      const list = byRow.get(p.place.row) ?? [];
      list.push({ line: li, first: i, last: j, start: p.t, end: line.points[j]!.t });
      byRow.set(p.place.row, list);
      i = j + 1;
    }
  });
  return byRow;
}

/**
 * A lane per point (DESIGN.md: each waiting stretch takes the lowest free
 * lane): within a row, a stay that starts before another one ends takes
 * the next lane up. Points between rows have lane -1.
 */
export function assignLanes(lines: EntityPath[]): number[][] {
  const lanes = lines.map((l) => l.points.map(() => -1));
  for (const [, list] of [...stays(lines)].sort((a, b) => a[0] - b[0])) {
    const sorted = [...list].sort((a, b) => a.start - b.start || a.line - b.line || a.first - b.first);
    const ends: number[] = [];
    for (const s of sorted) {
      let lane = ends.findIndex((e) => e <= s.start);
      if (lane < 0) {
        lane = ends.length;
        ends.push(s.end);
      } else ends[lane] = s.end;
      for (let k = s.first; k <= s.last; k++) lanes[s.line]![k] = lane;
    }
  }
  return lanes;
}

export type TimeUnit = "second" | "minute" | "hour" | "day";
export const tickMs: Readonly<Record<TimeUnit, number>> = { second: 1_000, minute: 60_000, hour: 3_600_000, day: 86_400_000 };
const steps: Readonly<Record<TimeUnit, readonly number[]>> = {
  second: [1, 2, 5, 10, 15, 30],
  minute: [1, 2, 5, 10, 15, 30],
  hour: [1, 2, 3, 6, 12],
  day: [1, 2, 5, 7, 10, 14, 30, 60, 90, 180, 365],
};
const units: readonly TimeUnit[] = ["second", "minute", "hour", "day"];
/** At least this many pixels between two labelled times. */
const TICK_SPACING = 64;

export type TimeTicks = { unit: TimeUnit; step: number; values: number[] };

/** The labelled times: the smallest unit and round step whose ticks fit the plot's width. */
export function timeTicks(endTick: number, plotWidth: number): TimeTicks {
  const max = Math.max(2, Math.floor(plotWidth / TICK_SPACING));
  const make = (unit: TimeUnit, step: number): TimeTicks => {
    const n = Math.floor(endTick / (step * tickMs[unit]) + 1e-9);
    return { unit, step, values: Array.from({ length: n + 1 }, (_, k) => k * step) };
  };
  for (const unit of units) for (const step of steps[unit]) if (endTick / (step * tickMs[unit]) <= max) return make(unit, step);
  return make("day", Math.ceil(endTick / tickMs.day / max));
}

// Width estimates for row labels at 13.5 px Archivo (about 0.55 em) and a
// CJK face (1 em). An estimate, so the layout stays a pure function.
const wide = /[ᄀ-ᅟ⺀-꓏가-힣豈-﫿︰-﹏＀-｠￠-￦]/u;
const charWidth = (c: string) => (wide.test(c) ? 13.5 : 7.4);
const textWidth = (s: string) => [...s].reduce((w, c) => w + charWidth(c), 0);
const ELLIPSIS = "…";

/** The name if it fits in maxPx, else as much of it as fits, then an ellipsis. */
export function fitLabel(name: string, maxPx: number): string {
  if (textWidth(name) <= maxPx) return name;
  let out = "";
  let w = charWidth(ELLIPSIS);
  for (const c of name) {
    if (w + charWidth(c) > maxPx) break;
    out += c;
    w += charWidth(c);
  }
  return out.trimEnd() + ELLIPSIS;
}

export const ROW_HEIGHT = 28;
const AXIS_BAND = 24;
const BOTTOM_PAD = 4;
const RIGHT_PAD = 16;
const LABEL_PAD = 24;
const LANE_INSET = 3;
const LANE_PITCH = 2.5;
const MAX_LANE = Math.floor((ROW_HEIGHT - 2 * LANE_INSET) / 2 / LANE_PITCH);
/** Lines drawn per run at most; the chart says when it draws fewer than the trace has. */
export const MAX_LINES = 400;

export type RunLines = {
  lines: { entity: string; points: [number, number][] }[];
  /** Release dots, where items reached a sink. */
  releases: [number, number][];
  drawn: number;
  total: number;
};

export type MareyLayout = {
  width: number;
  height: number;
  x0: number;
  x1: number;
  top: number;
  bottom: number;
  rows: { id: string; label: string; name: string; y: number; height: number; sink: boolean }[];
  unit: TimeUnit;
  ticks: { x: number; value: number }[];
  minor: number[];
  /** The y of each sink row's station line. */
  stations: number[];
  baseline: RunLines | null;
  scenario: RunLines | null;
};

const r1 = (v: number) => Math.round(v * 10) / 10;

/**
 * Pixels for a chart width wide. The baseline's lanes stack down from each
 * row's top and the scenario's up from its bottom, so the two runs never
 * share a lane; points held between two steps sit halfway between them.
 */
export function mareyLayout(input: {
  rows: StepRow[];
  baseline: EntityPath[] | null;
  scenario: EntityPath[] | null;
  endTick: number;
  width: number;
  maxLines?: number;
}): MareyLayout {
  const { rows, width } = input;
  const end = input.endTick > 0 ? input.endTick : 1;
  const labelWidth = Math.min(Math.round(width * 0.3), Math.max(72, Math.ceil(Math.max(0, ...rows.map((r) => textWidth(r.name))) + LABEL_PAD)));
  const x0 = labelWidth;
  const x1 = width - RIGHT_PAD;
  const top = AXIS_BAND;
  const bottom = top + rows.length * ROW_HEIGHT;
  const x = (t: number) => r1(x0 + (Math.min(Math.max(t, 0), end) / end) * (x1 - x0));
  const rowTop = (r: number) => top + r * ROW_HEIGHT;
  const centre = (r: number) => rowTop(r) + ROW_HEIGHT / 2;

  const ticks = timeTicks(end, x1 - x0);
  const unitMs = tickMs[ticks.unit];
  const quarter = (ticks.step * unitMs) / 4;
  const minor: number[] = [];
  for (let k = 1; k * quarter <= end + 1e-6; k++) if (k % 4 !== 0) minor.push(x(k * quarter));

  const runLines = (lines: EntityPath[] | null, fromTop: boolean): RunLines | null => {
    if (lines === null) return null;
    const shown = lines.slice(0, input.maxLines ?? MAX_LINES);
    const lanes = assignLanes(shown);
    const y = (p: PathPoint, lane: number) => {
      if ("between" in p.place) {
        const [a, b] = p.place.between;
        return r1((centre(a) + centre(Math.min(b, rows.length - 1))) / 2);
      }
      // Releases land on the sink row's one station line, its centre.
      if (rows[p.place.row]?.sink) return r1(centre(p.place.row));
      const l = Math.min(Math.max(lane, 0), MAX_LANE);
      return r1(fromTop ? rowTop(p.place.row) + LANE_INSET + l * LANE_PITCH : rowTop(p.place.row) + ROW_HEIGHT - LANE_INSET - l * LANE_PITCH);
    };
    const releases: [number, number][] = [];
    const out = shown.map((line, li) => {
      const points = line.points.map((p, pi): [number, number] => [x(p.t), y(p, lanes[li]![pi]!)]);
      if (line.release) {
        const at = line.points.findLastIndex((p) => "row" in p.place && p.place.row === line.release!.row && p.t === line.release!.t);
        if (at >= 0) releases.push(points[at]!);
      }
      return { entity: line.entity, points };
    });
    return { lines: out, releases, drawn: shown.length, total: lines.length };
  };

  return {
    width,
    height: bottom + BOTTOM_PAD,
    x0,
    x1,
    top,
    bottom,
    rows: rows.map((r, i) => ({ id: r.id, name: r.name, label: fitLabel(r.name, labelWidth - 16), y: rowTop(i), height: ROW_HEIGHT, sink: r.sink })),
    unit: ticks.unit,
    ticks: ticks.values.map((v) => ({ x: x(v * unitMs), value: v })),
    minor,
    stations: rows.flatMap((r, i) => (r.sink ? [r1(centre(i))] : [])),
    baseline: runLines(input.baseline, true),
    scenario: runLines(input.scenario, false),
  };
}

export type StepSummary = {
  /** Sampled items with a stay at this step. */
  items: number;
  /** The longest finished stay (waiting) at the step, in ticks; null at a sink or with no finished stay. */
  longestWait: number | null;
  /** Items still waiting at the step when the run ended. */
  waitingAtEnd: number;
};

/** What the chart shows per step, as numbers: the chart's text alternative (WCAG 1.1.1). */
export function traceSummary(lines: EntityPath[], rows: StepRow[]): StepSummary[] {
  const byRow = stays(lines);
  return rows.map((row, r) => {
    const list = byRow.get(r) ?? [];
    const items = new Set(list.map((s) => s.line)).size;
    let longest: number | null = null;
    let atEnd = 0;
    for (const s of list) {
      const line = lines[s.line]!;
      if (line.waitingAtEnd && s.last === line.points.length - 1) {
        atEnd++;
        continue;
      }
      if (!row.sink) longest = Math.max(longest ?? 0, s.end - s.start);
    }
    return { items, longestWait: longest, waitingAtEnd: atEnd };
  });
}
