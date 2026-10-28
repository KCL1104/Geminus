// Where the sampled work items are at a time of a run (T-034, REQ-017):
// each item is at the node of its last located event (arrival, queue_enter,
// service_start, delay_start) until it completes or abandons; an item held
// after service (blocked) stays at its station. Only the trace's own events
// count: nothing is interpolated, an item the trace does not have is never
// drawn, and after a truncated trace's last event nothing is known.
import type { TraceArtifact, TraceEvent } from "../../../lib/contracts";

type Move = { t: number; from: string | null; to: string | null };

export type Timeline = {
  /** The last tick the trace knows: the horizon, or its last event's when it was cut short. */
  end: number;
  /** The items at each node at tick t (nodes with none are left out). */
  at(t: number): Record<string, number>;
};

const locating: ReadonlySet<TraceEvent["type"]> = new Set(["arrival", "queue_enter", "service_start", "delay_start"]);
const leaving: ReadonlySet<TraceEvent["type"]> = new Set(["complete", "abandon"]);

export function timeline(trace: TraceArtifact, nodes: ReadonlySet<string>, horizon: number): Timeline {
  const where = new Map<string, string>();
  const moves: Move[] = [];
  for (const e of trace.events) {
    if (!locating.has(e.type) && !leaving.has(e.type)) continue;
    // A node the model does not have: where the item is is unknown, so it is not drawn.
    const to = leaving.has(e.type) || !nodes.has(e.node_id) ? null : e.node_id;
    const from = where.get(e.entity_id) ?? null;
    if (from === to) continue;
    if (to === null) where.delete(e.entity_id);
    else where.set(e.entity_id, to);
    moves.push({ t: e.time_tick, from, to });
  }
  moves.sort((a, b) => a.t - b.t); // stable: an item's moves keep their order
  const last = trace.events.reduce((m, e) => Math.max(m, e.time_tick), 0);
  const end = trace.truncated ? Math.min(last, horizon) : horizon;

  // A cursor: playing forward applies only the moves since the last call; going back starts over.
  const counts = new Map<string, number>();
  let next = 0;
  let cursor = -Infinity;
  const step = (node: string | null, d: number) => node !== null && counts.set(node, (counts.get(node) ?? 0) + d);
  return {
    end,
    at(t) {
      const tick = Math.min(t, end);
      if (tick < cursor) {
        counts.clear();
        next = 0;
      }
      cursor = tick;
      for (; next < moves.length && moves[next]!.t <= tick; next++) {
        step(moves[next]!.from, -1);
        step(moves[next]!.to, 1);
      }
      const out: Record<string, number> = {};
      for (const [node, n] of counts) if (n > 0) out[node] = n;
      return out;
    },
  };
}
