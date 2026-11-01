// Where each sampled work item is at a time of a run, for the line in motion
// (H-072; docs/design/H-072_TRACE_ANIMATION.md). Pure and shared by the 3D
// stage and the 2D canvas overlay: it reads only a run's sampled trace
// (geminus.trace.v1, schemas/simulation.schema.json#/$defs/TraceArtifact),
// the model's nodes and resources, and the run's horizon.
//
//   const m = motion(trace, ir, result.measurement_window.horizon_ticks);
//   const f = m.at(tick, { hop: clock.hopTicks(), limit: 2000 });
//   f.boards  // each sampled item in the line: its node, state, slot, hop
//   f.nodes   // per node: waiting, in service, blocked, ..., its state
//   f.resources // per resource: the units the sampled items hold, its state
//
// The honesty of features/twin-3d/playback/occupancy.ts holds here too:
// - Only the trace's own events count. An item the trace lacks is never
//   drawn, an item at a node the model does not have is not drawn (counted
//   in `hidden`), and between two of its events an item stays where the
//   last one put it (SIMULATION_SPEC §9: nothing is filled in).
// - The one drawn motion is the slide of a hop: when an event puts an item
//   at another node, it slides there over `hop` ticks (the clock gives the
//   run time 300 ms of playback covers; 0 draws no slide). Its counts and
//   state are the new node's from the event on; only its drawn place lags,
//   by at most `hop`. A hop that starts before the last one ended starts
//   from where the item was drawn (`Hop`, `placeOnHop`), so nothing jumps
//   back. A completed or abandoned item slides into its sink and is gone.
// - A truncated trace holds after its last event (`end`); a whole one ends
//   at the horizon. Asked for a later time (the page's clock runs to the
//   horizon, or to the longer of two runs), the line stays as it was at
//   `end`, and only the slides its last events began finish, so every item
//   then stands at its node.
// - Station states come from the sampled items only: a station serving
//   items the sample left out looks idle. Breakdowns are not in the trace
//   (SIMULATION_SPEC §9), so no station is ever "failed" from a
//   geminus.trace.v1 trace; a `failure` event is a work item routed through
//   a failure node (a failed test), counted in that node's `through`.
//
// Event semantics (SIMULATION_SPEC §9, the schema's TraceEvent):
//   arrival       → "arrived" at the source
//   queue_enter   → "queued" at the node entered (a queue, or a process entered directly)
//   setup_start   → "setup" at the process, changing over `resource_id`; the
//                   service it begins (same tick) stays "setup" until its
//                   service_end, since the trace does not say when the changeover ended
//   service_start → "service", or "batch" with a batch_id (D-054)
//   service_end, delay_end, unblocked → "done" at that node (the next event,
//                   usually in the same tick, says where it went)
//   blocked       → "blocked" at the node it is held at (D-049; it keeps its resources)
//   delay_start   → "delay"
//   failure, rework → a pass through that node (`through`); the item's place does not change
//   complete, abandon → "left": out of the line at the sink (`through`)
//
// Fast for the engine's cap of 100,000 events: the trace is read once into
// per-item stays and one list of changes sorted by time; at(t) applies only
// the changes between the last time asked and t (forward, or undone
// backward, or from the start if that is shorter) and then looks at the
// items in the line, never at every event. The last frame is kept, so two
// views of one run at one time share it.
import type { TraceArtifact } from "../../lib/contracts";

/** What an item is doing at its node. */
export type BoardState = "arrived" | "queued" | "service" | "setup" | "batch" | "blocked" | "delay" | "done" | "left";

/** A station's (or a resource's) state, from what its sampled items do. "failed" is never produced from geminus.trace.v1 (no breakdowns in it). */
export type StationState = "busy" | "idle" | "blocked" | "setup" | "failed";

/**
 * A slide in progress: the item is drawn at P(path[0]) moved toward each
 * later node in turn, steps[i] of the way to path[i + 1] (0..1). The common
 * case is one hop: path [A, B], steps [s]. `placeOnHop` folds it.
 */
export type Hop = { readonly path: readonly string[]; readonly steps: readonly number[] };

export type Board = {
  /** The trace's entity ID ("r0/release_a/4"). */
  readonly id: string;
  /** Where its last event put it (the sink, for "left"). */
  readonly node: string;
  readonly state: BoardState;
  /** Its batch (D-054) while in a batch's service. */
  readonly batch: string | null;
  /** The tick it reached `node`. */
  readonly arrived: number;
  /** The tick its state began. */
  readonly since: number;
  /** The tick of its next event, if the trace has one (the end of a service, a wait, a block). */
  readonly until: number | null;
  /** Its place among the items in the same state at the same node, in the order they got there (0 first): a queue's stack. */
  readonly slot: number;
  /** The slide toward `node`, or null when it is drawn at `node`. */
  readonly hop: Hop | null;
};

export type NodeFrame = {
  readonly id: string;
  /** Items queued here (queue_enter), waiting for service. */
  readonly waiting: number;
  /** Items in service here, a setup's and a batch's included. */
  readonly inService: number;
  /** Items in a service that began with a changeover. */
  readonly setup: number;
  /** Items held here by a full buffer downstream. */
  readonly blocked: number;
  readonly delayed: number;
  /** Items here between two events of the same tick, or after a truncated trace's last (arrived, done). */
  readonly other: number;
  /** Items that passed this node so far: failed or reworked at a failure or rework node, completed or abandoned at a sink. */
  readonly through: number;
  /** The batches in service here, with their sampled members, in the order they started. */
  readonly batches: readonly { readonly id: string; readonly members: number }[];
  readonly state: StationState;
};

export type ResourceFrame = {
  readonly id: string;
  /** The model's capacity (its units). */
  readonly capacity: number;
  /** Units the sampled items hold (in service or blocked; a batch holds one unit for all its members). A lower bound: the trace samples about 1 in 8. */
  readonly inUse: number;
  readonly state: StationState;
};

export type Frame = {
  /** The tick shown: the time asked, within 0..end. */
  readonly tick: number;
  /** The items to draw, by node (the model's order), state, then slot. */
  readonly boards: readonly Board[];
  /** Every node of the model. */
  readonly nodes: Readonly<Record<string, NodeFrame>>;
  /** Every resource of the model. */
  readonly resources: Readonly<Record<string, ResourceFrame>>;
  /** Sampled items in the line at this time, the hidden ones included (not those sliding into a sink). */
  readonly inLine: number;
  /** Items in the line beyond the limit, not drawn. */
  readonly notDrawn: number;
  /** Items at a node the model does not have: where they are is unknown, so they are not drawn. */
  readonly hidden: number;
};

export type Motion = {
  /** The run's horizon in ticks. */
  readonly horizon: number;
  /** The last tick the trace knows: the horizon, or its last event's when it was cut short. */
  readonly end: number;
  readonly truncated: boolean;
  /** Distinct sampled items in the trace. */
  readonly items: number;
  /**
   * The line at tick t (clamped to 0..end; a slide begun by then goes on
   * at t itself, so past `end` it finishes). hop: the ticks a slide takes
   * (0, the default: none). limit: at most this many boards (default all;
   * the rest are counted in notDrawn, the deepest in a stack first).
   */
  at(t: number, options?: { readonly hop?: number; readonly limit?: number }): Frame;
};

/** The model's parts the motion reads (a ModelIR has them). */
export type MotionModel = {
  readonly nodes: readonly { readonly id: string; readonly type: string; readonly requires?: readonly { readonly resource_id: string; readonly units: number }[]; readonly batch?: unknown }[];
  readonly resources: readonly { readonly id: string; readonly capacity: number }[];
};

/** Where a board is drawn: P(path[0]), moved toward each next node by its step. */
export function placeOnHop<P>(hop: Hop, at: (node: string) => P, mix: (from: P, to: P, s: number) => P): P {
  let p = at(hop.path[0]!);
  for (let i = 1; i < hop.path.length; i++) p = mix(p, at(hop.path[i]!), hop.steps[i - 1] ?? 1);
  return p;
}

const STATES: readonly BoardState[] = ["arrived", "queued", "service", "setup", "batch", "blocked", "delay", "done", "left"];
const S = Object.fromEntries(STATES.map((s, i) => [s, i])) as Record<BoardState, number>;
const NS = STATES.length;
/** A slide that starts before this many earlier ones ended starts from the oldest's node (a short forward snap; rare). */
const MAX_CHAIN = 8;

// The kinds of change: an item moves from its previous stay (or from outside
// the line) to a stay; it leaves the line from its stay; it passes a node
// (failure, rework).
const STAY = 0;
const LEAVE = 1;
const PASS = 2;

export function motion(trace: TraceArtifact, model: MotionModel, horizon: number): Motion {
  const nodeIds = model.nodes.map((n) => n.id);
  const nodeOf = new Map(nodeIds.map((id, i) => [id, i]));
  const resourceOf = new Map(model.resources.map((r, i) => [r.id, i]));
  const nn = nodeIds.length;

  // Stays: one per state an item takes, in the trace's order.
  const sNode: number[] = []; // node index, -1 for a node the model does not have
  const sState: number[] = [];
  const sBatch: number[] = []; // index into batchIds, -1
  const sSetup: number[] = []; // resource index changing over, -1
  const sStart: number[] = [];
  const sItem: number[] = [];
  const sVisit: number[] = []; // the tick the item reached this stay's node
  const sVisitPrev: number[] = []; // the last stay of the item's previous node, -1
  const sNext: number[] = []; // the tick of the item's next change, NaN while unknown
  const batchIds: string[] = [];
  const batchOf = new Map<string, number>();
  const items: string[] = [];
  const itemOf = new Map<string, number>();
  const last: number[] = []; // each item's current stay, -1 outside the line

  // Changes, in the trace's order (its ticks never go back: engine order).
  const cTick: number[] = [];
  const cKind: number[] = [];
  const cItem: number[] = [];
  const cFrom: number[] = []; // stay, -1
  const cTo: number[] = []; // stay, -1 (Leave: the "left" stay, for the slide into the sink)
  const cNode: number[] = []; // Pass and Leave: the node passed, -1

  const stay = (item: number, tick: number, node: number, state: number, batch: number, setup: number) => {
    const prev = last[item]!;
    const s = sNode.length;
    sNode.push(node);
    sState.push(state);
    sBatch.push(batch);
    sSetup.push(setup);
    sStart.push(tick);
    sItem.push(item);
    const sameNode = prev >= 0 && sNode[prev] === node;
    sVisit.push(sameNode ? sVisit[prev]! : tick);
    sVisitPrev.push(sameNode ? sVisitPrev[prev]! : prev);
    sNext.push(Number.NaN);
    if (prev >= 0) sNext[prev] = tick;
    return s;
  };

  const change = (tick: number, kind: number, item: number, from: number, to: number, node: number) => {
    cTick.push(tick);
    cKind.push(kind);
    cItem.push(item);
    cFrom.push(from);
    cTo.push(to);
    cNode.push(node);
  };
  const enter = (item: number, tick: number, node: number, state: number, batch = -1, setup = -1) => {
    const prev = last[item]!;
    const s = stay(item, tick, node, state, batch, setup);
    change(tick, STAY, item, prev, s, -1);
    last[item] = s;
  };

  let lastTick = 0;
  for (const e of trace.events) {
    const tick = e.time_tick;
    lastTick = Math.max(lastTick, tick);
    let item = itemOf.get(e.entity_id);
    if (item === undefined) {
      item = items.length;
      items.push(e.entity_id);
      itemOf.set(e.entity_id, item);
      last.push(-1);
    }
    const node = nodeOf.get(e.node_id) ?? -1;
    const prev = last[item]!;
    switch (e.type) {
      case "arrival":
        enter(item, tick, node, S.arrived);
        break;
      case "queue_enter":
        enter(item, tick, node, S.queued);
        break;
      case "setup_start":
        enter(item, tick, node, S.setup, -1, e.resource_id === undefined ? -1 : (resourceOf.get(e.resource_id) ?? -1));
        break;
      case "service_start": {
        let batch = -1;
        if (e.batch_id !== undefined) {
          batch = batchOf.get(e.batch_id) ?? -1;
          if (batch < 0) {
            batch = batchIds.length;
            batchIds.push(e.batch_id);
            batchOf.set(e.batch_id, batch);
          }
        }
        // The service a changeover began (setup_start at this node, this tick) keeps it.
        const changed = prev >= 0 && sState[prev] === S.setup && sNode[prev] === node && sStart[prev] === tick;
        enter(item, tick, node, batch >= 0 ? S.batch : changed ? S.setup : S.service, batch, changed ? sSetup[prev]! : -1);
        break;
      }
      case "blocked":
        enter(item, tick, node, S.blocked);
        break;
      case "delay_start":
        enter(item, tick, node, S.delay);
        break;
      case "service_end":
      case "delay_end":
      case "unblocked":
        enter(item, tick, node, S.done);
        break;
      case "failure":
      case "rework":
        change(tick, PASS, item, -1, -1, node);
        break;
      case "complete":
      case "abandon": {
        // A "left" stay at the sink, outside the line: drawn only while it slides in.
        const s = stay(item, tick, node, S.left, -1, -1);
        change(tick, LEAVE, item, prev, s, node);
        last[item] = -1;
        break;
      }
    }
  }

  const n = cTick.length;
  // geminus.trace.v1 keeps engine order, so its ticks never go back; a list
  // that does is sorted by tick, stably, so each item's changes keep their order.
  if (cTick.some((t, i) => i > 0 && t < cTick[i - 1]!)) {
    const order = cTick.map((_, i) => i).sort((a, b) => cTick[a]! - cTick[b]! || a - b);
    for (const a of [cTick, cKind, cItem, cFrom, cTo, cNode]) {
      const copy = a.slice();
      order.forEach((from, to) => (a[to] = copy[from]!));
    }
  }
  const ticks = Float64Array.from(cTick);
  const leaves: number[] = [];
  for (let i = 0; i < n; i++) if (cKind[i] === LEAVE) leaves.push(i);
  const leaveTicks = Float64Array.from(leaves.map((i) => cTick[i]!));

  const truncated = trace.truncated;
  const end = truncated ? Math.min(lastTick, horizon) : horizon;

  // Which processes hold which resources, and how many units.
  const holds = model.nodes.map((node) =>
    node.type === "process" ? (node.requires ?? []).flatMap((r) => (resourceOf.has(r.resource_id) ? [{ r: resourceOf.get(r.resource_id)!, units: r.units }] : [])) : [],
  );
  const batchNode = model.nodes.map((node) => node.type === "process" && node.batch !== undefined);

  // The state at the cursor: the changes before it applied.
  const counts = new Int32Array(nn * NS);
  const through = new Int32Array(nn);
  const resSetup = new Int32Array(model.resources.length);
  const nodeSetup = new Int32Array(nn);
  const cur = new Int32Array(items.length).fill(-1);
  const alive = new Int32Array(items.length);
  const alivePos = new Int32Array(items.length).fill(-1);
  let aliveN = 0;
  let hidden = 0;
  let cursor = 0;

  const count = (s: number, d: number) => {
    const node = sNode[s]!;
    if (node < 0) hidden += d;
    else counts[node * NS + sState[s]!]! += d;
    const r = sSetup[s]!;
    if (r >= 0) {
      resSetup[r]! += d;
      if (node >= 0) nodeSetup[node]! += d;
    }
  };
  const enterLine = (item: number) => {
    if (alivePos[item]! >= 0) return;
    alivePos[item] = aliveN;
    alive[aliveN++] = item;
  };
  const leaveLine = (item: number) => {
    const p = alivePos[item]!;
    if (p < 0) return;
    const moved = alive[--aliveN]!;
    alive[p] = moved;
    alivePos[moved] = p;
    alivePos[item] = -1;
  };
  const apply = (i: number) => {
    const item = cItem[i]!;
    switch (cKind[i]) {
      case STAY:
        if (cFrom[i]! >= 0) count(cFrom[i]!, -1);
        else enterLine(item);
        count(cTo[i]!, 1);
        cur[item] = cTo[i]!;
        break;
      case LEAVE:
        if (cFrom[i]! >= 0) count(cFrom[i]!, -1);
        leaveLine(item);
        cur[item] = -1;
        if (cNode[i]! >= 0) through[cNode[i]!]! += 1;
        break;
      case PASS:
        if (cNode[i]! >= 0) through[cNode[i]!]! += 1;
        break;
    }
  };
  const undo = (i: number) => {
    const item = cItem[i]!;
    switch (cKind[i]) {
      case STAY:
        count(cTo[i]!, -1);
        if (cFrom[i]! >= 0) {
          count(cFrom[i]!, 1);
          cur[item] = cFrom[i]!;
        } else {
          leaveLine(item);
          cur[item] = -1;
        }
        break;
      case LEAVE:
        if (cFrom[i]! >= 0) {
          count(cFrom[i]!, 1);
          enterLine(item);
          cur[item] = cFrom[i]!;
        }
        if (cNode[i]! >= 0) through[cNode[i]!]! -= 1;
        break;
      case PASS:
        if (cNode[i]! >= 0) through[cNode[i]!]! -= 1;
        break;
    }
  };
  const reset = () => {
    counts.fill(0);
    through.fill(0);
    resSetup.fill(0);
    nodeSetup.fill(0);
    cur.fill(-1);
    alivePos.fill(-1);
    aliveN = 0;
    hidden = 0;
    cursor = 0;
  };
  // The number of entries of a sorted array that are at most x.
  const upTo = (a: Float64Array, x: number) => {
    let lo = 0;
    let hi = a.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (a[mid]! <= x) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  };
  const moveTo = (target: number) => {
    if (target < cursor && cursor - target > target) reset();
    for (; cursor < target; cursor++) apply(cursor);
    for (; cursor > target; ) undo(--cursor);
  };

  // The slide into the node of stay s at tick `tick` (see Hop).
  const hopOf = (s: number, tick: number, h: number): Hop | null => {
    if (!(h > 0)) return null;
    const path: number[] = [];
    const steps: number[] = [];
    let v = s;
    let endAt = tick;
    for (;;) {
      const p = sVisitPrev[v]!;
      if (p < 0 || sNode[p]! < 0 || path.length >= MAX_CHAIN) {
        path.unshift(sNode[v]!); // appeared here (or the chain is long enough): drawn from its node
        break;
      }
      const step = Math.min(1, (endAt - sVisit[v]!) / h);
      if (step >= 1) {
        path.unshift(sNode[v]!);
        break;
      }
      if (path.length === 0 || step > 0) {
        path.unshift(sNode[v]!);
        steps.unshift(Math.max(0, step));
      }
      endAt = sVisit[v]!;
      v = p;
    }
    return steps.length === 0 ? null : { path: path.map((i) => nodeIds[i]!), steps };
  };

  const STATE_ORDER = [S.service, S.setup, S.batch, S.blocked, S.delay, S.queued, S.arrived, S.done, S.left];
  const rank = new Int32Array(NS);
  STATE_ORDER.forEach((st, i) => (rank[st] = i));

  let memo: { tick: number; slide: number; hop: number; limit: number; frame: Frame } | null = null;

  const at: Motion["at"] = (t, options = {}) => {
    const asked = Math.max(Number.isFinite(t) ? t : 0, 0);
    const tick = Math.min(asked, end);
    const h = Math.max(0, options.hop ?? 0);
    const limit = Math.max(0, Math.floor(options.limit ?? Number.POSITIVE_INFINITY));
    // The slides are worked out at the time asked: past a cut-short trace's end the clock
    // runs on, and the hops its last events began finish on it (H-072 review), while the
    // line itself holds at `end`. From end + hop on every slide is over, so every later
    // time is the same frame.
    const slide = h > 0 ? Math.min(asked, end + h) : tick;
    if (memo && memo.tick === tick && memo.slide === slide && memo.hop === h && memo.limit === limit) return memo.frame;
    moveTo(upTo(ticks, tick));

    // The items to draw: those in the line at a node the model has, and those sliding into a sink.
    const drawn: number[] = [];
    for (let i = 0; i < aliveN; i++) {
      const s = cur[alive[i]!]!;
      if (s >= 0 && sNode[s]! >= 0) drawn.push(s);
    }
    if (h > 0) {
      for (let j = upTo(leaveTicks, slide - h); j < leaves.length && leaveTicks[j]! <= tick; j++) {
        const s = cTo[leaves[j]!]!;
        if (sNode[s]! >= 0) drawn.push(s);
      }
    }
    drawn.sort((a, b) => sNode[a]! - sNode[b]! || rank[sState[a]!]! - rank[sState[b]!]! || a - b);
    const slots = new Int32Array(drawn.length);
    for (let i = 1; i < drawn.length; i++) {
      const a = drawn[i - 1]!;
      const b = drawn[i]!;
      slots[i] = sNode[a] === sNode[b] && sState[a] === sState[b] ? slots[i - 1]! + 1 : 0;
    }
    // Over the limit: the shallowest slots of every stack first, so each node keeps its front.
    let keep: (i: number) => boolean = () => true;
    if (drawn.length > limit) {
      const bySlot = new Int32Array(drawn.length + 1);
      for (const sl of slots) bySlot[sl]! += 1;
      let k = 0;
      let taken = 0;
      while (taken + bySlot[k]! <= limit) taken += bySlot[k++]!;
      let room = limit - taken;
      keep = (i) => slots[i]! < k || (slots[i] === k && room-- > 0);
    }
    const boards: Board[] = [];
    let notDrawn = 0;
    const members = new Map<number, { node: number; members: number }>(); // batch → its node and sampled members in service
    for (let i = 0; i < drawn.length; i++) {
      const s = drawn[i]!;
      if (sState[s] === S.batch) {
        const m = members.get(sBatch[s]!);
        if (m) m.members++;
        else members.set(sBatch[s]!, { node: sNode[s]!, members: 1 });
      }
      if (!keep(i)) {
        if (sState[s] !== S.left) notDrawn++;
        continue;
      }
      const next = sNext[s]!;
      boards.push({
        id: items[sItem[s]!]!,
        node: nodeIds[sNode[s]!]!,
        state: STATES[sState[s]!]!,
        batch: sBatch[s]! >= 0 ? batchIds[sBatch[s]!]! : null,
        arrived: sVisit[s]!,
        since: sStart[s]!,
        until: Number.isNaN(next) ? null : next,
        slot: slots[i]!,
        hop: hopOf(s, slide, h),
      });
    }

    // Each node's batches in the order they first appear in the trace (a batch is one process's, D-054).
    const batchesAt = new Map<number, { id: string; members: number }[]>();
    for (const [b, m] of [...members].sort((x, y) => x[0] - y[0])) {
      const list = batchesAt.get(m.node) ?? [];
      list.push({ id: batchIds[b]!, members: m.members });
      batchesAt.set(m.node, list);
    }

    const c = (node: number, st: number) => counts[node * NS + st]!;
    const stateOf = (blocked: number, setup: number, busy: number): StationState => (blocked > 0 ? "blocked" : setup > 0 ? "setup" : busy > 0 ? "busy" : "idle");
    const nodes: Record<string, NodeFrame> = {};
    for (let i = 0; i < nn; i++) {
      const inService = c(i, S.service) + c(i, S.setup) + c(i, S.batch);
      const blocked = c(i, S.blocked);
      const delayed = c(i, S.delay);
      nodes[nodeIds[i]!] = {
        id: nodeIds[i]!,
        waiting: c(i, S.queued),
        inService,
        setup: nodeSetup[i]!,
        blocked,
        delayed,
        other: c(i, S.arrived) + c(i, S.done),
        through: through[i]!,
        batches: batchesAt.get(i) ?? [],
        state: stateOf(blocked, nodeSetup[i]!, inService + delayed),
      };
    }
    const inUse = new Float64Array(model.resources.length);
    const resBlocked = new Int32Array(model.resources.length);
    for (let i = 0; i < nn; i++) {
      // A batch holds one unit for all its members (D-054); its blocked members are not told apart, so they add none.
      const holding = batchNode[i] ? (batchesAt.get(i)?.length ?? 0) : c(i, S.service) + c(i, S.setup) + c(i, S.batch) + c(i, S.blocked);
      for (const { r, units } of holds[i]!) {
        inUse[r]! += holding * units;
        resBlocked[r]! += c(i, S.blocked);
      }
    }
    const resources: Record<string, ResourceFrame> = {};
    model.resources.forEach((r, i) => {
      resources[r.id] = { id: r.id, capacity: r.capacity, inUse: inUse[i]!, state: stateOf(resBlocked[i]!, resSetup[i]!, inUse[i]!) };
    });

    const frame: Frame = { tick, boards, nodes, resources, inLine: aliveN, notDrawn, hidden };
    memo = { tick, slide, hop: h, limit, frame };
    return frame;
  };

  return { horizon, end, truncated, items: items.length, at };
}
