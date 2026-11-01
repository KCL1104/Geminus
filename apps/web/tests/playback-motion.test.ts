// H-072: the motion model of the line in motion (features/playback/motion.ts).
// Every item is where its last trace event put it, in the state that event
// says; only a hop slides, over the ticks asked; a truncated trace holds;
// nothing the trace lacks is drawn; station states come from the sampled
// items alone (no breakdowns in geminus.trace.v1, so never "failed"). The
// incremental cursor is checked against a plain scan of the events on the
// engine's own traces of the electronics line, going forward, back and
// jumping, and it is fast enough for the engine's cap of 100,000 events.
import { describe, expect, it } from "vitest";
import type { TraceArtifact, TraceEvent } from "../lib/contracts";
import { type BoardState, type Frame, type MotionModel, motion, placeOnHop } from "../features/playback/motion";
import { readElectronicsLine } from "./fixtures/electronics-line";

let seq = 1;
const ev = (time_tick: number, type: TraceEvent["type"], entity_id: string, node_id: string, extra: Partial<TraceEvent> = {}): TraceEvent => ({
  time_tick, sequence: seq++, type, entity_id, node_id, ...extra,
});
const trace = (events: TraceEvent[], truncated = false): TraceArtifact => ({
  format: "geminus.trace.v1",
  engine_version: "des-go-0.5.0",
  replication_index: 0,
  sampling: { method: "entity_id_sha256", rate: 0.125, max_events: 100_000 },
  truncated,
  events,
});

// src → q → p (machine m, 2 units) → q2 → p2 (machine m2) → sink; oven: a batch process on chamber c; fail: a failure node.
const model: MotionModel = {
  nodes: [
    { id: "src", type: "source" },
    { id: "q", type: "queue" },
    { id: "p", type: "process", requires: [{ resource_id: "m", units: 1 }] },
    { id: "q2", type: "queue" },
    { id: "p2", type: "process", requires: [{ resource_id: "m2", units: 1 }, { resource_id: "op", units: 1 }] },
    { id: "oven", type: "process", requires: [{ resource_id: "c", units: 1 }], batch: { min_size: 2, max_size: 4 } },
    { id: "fail", type: "failure" },
    { id: "d", type: "delay" },
    { id: "sink", type: "sink" },
  ],
  resources: [
    { id: "m", capacity: 2 },
    { id: "m2", capacity: 1 },
    { id: "op", capacity: 3 },
    { id: "c", capacity: 9 },
  ],
};

// e1 waits at q, is served at p, is held there (blocked) until q2 has room, then leaves through p2.
const e1 = () => [
  ev(0, "arrival", "e1", "src"), ev(0, "queue_enter", "e1", "q"), ev(5, "service_start", "e1", "p"), ev(8, "service_end", "e1", "p"),
  ev(8, "blocked", "e1", "p"), ev(12, "unblocked", "e1", "p"), ev(12, "queue_enter", "e1", "q2"), ev(12, "service_start", "e1", "p2"),
  ev(15, "service_end", "e1", "p2"), ev(15, "complete", "e1", "sink"),
];
const e2 = () => [ev(3, "arrival", "e2", "src"), ev(3, "queue_enter", "e2", "q"), ev(9, "service_start", "e2", "p")];

const where = (f: Frame) => Object.fromEntries(f.boards.map((b) => [b.id, `${b.state}@${b.node}`]));

describe("the line at a time, from the trace alone", () => {
  it("puts each item where its last event put it, in the state it says", () => {
    const m = motion(trace([...e1(), ...e2()]), model, 20);
    expect(m.end).toBe(20);
    expect(m.items).toBe(2);
    expect([0, 3, 5, 8, 9, 12, 15, 20].map((t) => where(m.at(t)))).toEqual([
      { e1: "queued@q" },
      { e1: "queued@q", e2: "queued@q" },
      { e1: "service@p", e2: "queued@q" },
      { e1: "blocked@p", e2: "queued@q" }, // service ended, held: still at p
      { e1: "blocked@p", e2: "service@p" },
      { e1: "service@p2", e2: "service@p" },
      { e2: "service@p" }, // e1 completed
      { e2: "service@p" }, // e2 was still in service when the run ended
    ]);
  });

  it("counts each node's items and gives its station state", () => {
    const m = motion(trace([...e1(), ...e2()]), model, 20);
    const f = m.at(9);
    expect(f.nodes.p).toMatchObject({ waiting: 0, inService: 1, blocked: 1, setup: 0, through: 0, state: "blocked" });
    expect(f.nodes.q).toMatchObject({ waiting: 0, state: "idle" });
    expect(m.at(3).nodes.q).toMatchObject({ waiting: 2, state: "idle" });
    expect(m.at(5).nodes.p!.state).toBe("busy");
    // A blocked item keeps its resources (D-049): two units of m held at 9.
    expect(f.resources.m).toEqual({ id: "m", capacity: 2, inUse: 2, state: "blocked" });
    expect(m.at(12).resources).toMatchObject({ m2: { inUse: 1, state: "busy" }, op: { inUse: 1, state: "busy" }, c: { inUse: 0, state: "idle" } });
    expect(f.inLine).toBe(2);
    // The sink counts what completed there.
    expect(m.at(15).nodes.sink!.through).toBe(1);
    expect(m.at(14).nodes.sink!.through).toBe(0);
  });

  it("knows when each state ends, from the next event", () => {
    const f = motion(trace([...e1(), ...e2()]), model, 20).at(6);
    expect(f.boards.find((b) => b.id === "e1")).toMatchObject({ arrived: 5, since: 5, until: 8 });
    expect(motion(trace(e2()), model, 20).at(10).boards[0]).toMatchObject({ state: "service", until: null });
  });

  it("stacks the items of a node in the order they got there", () => {
    const events = ["a", "b", "c"].flatMap((id, i) => [ev(i, "arrival", id, "src"), ev(i, "queue_enter", id, "q")]);
    const f = motion(trace(events), model, 10).at(5);
    expect(f.boards.map((b) => [b.id, b.slot])).toEqual([["a", 0], ["b", 1], ["c", 2]]);
  });

  it("holds a truncated trace after its last event, and never draws past it", () => {
    const m = motion(trace(e2(), true), model, 20);
    expect(m.truncated).toBe(true);
    expect(m.end).toBe(9);
    expect(m.at(20).tick).toBe(9);
    expect(where(m.at(20))).toEqual({ e2: "service@p" });
  });

  it("leaves out an item at a node the model does not have, and counts it as hidden", () => {
    const m = motion(trace([ev(0, "arrival", "x", "src"), ev(1, "queue_enter", "x", "elsewhere"), ev(4, "service_start", "x", "p")]), model, 10);
    expect([where(m.at(0)), where(m.at(2)), where(m.at(4))]).toEqual([{ x: "arrived@src" }, {}, { x: "service@p" }]);
    expect(m.at(2)).toMatchObject({ hidden: 1, inLine: 1 });
    expect(m.at(4).hidden).toBe(0);
  });

  it("draws nothing for a trace without events", () => {
    const f = motion(trace([]), model, 10).at(5);
    expect(f.boards).toEqual([]);
    expect(f.nodes.p).toMatchObject({ waiting: 0, inService: 0, state: "idle" });
  });

  it("shows a changeover's service as setup, on the resource changing over", () => {
    const m = motion(
      trace([ev(0, "arrival", "s", "src"), ev(0, "queue_enter", "s", "q"), ev(2, "setup_start", "s", "p2", { resource_id: "m2" }), ev(2, "service_start", "s", "p2"), ev(7, "service_end", "s", "p2"), ev(7, "queue_enter", "s", "q2")]),
      model,
      10,
    );
    const f = m.at(3);
    expect(where(f)).toEqual({ s: "setup@p2" });
    expect(f.nodes.p2).toMatchObject({ inService: 1, setup: 1, state: "setup" });
    expect(f.resources.m2!.state).toBe("setup");
    expect(f.resources.op).toMatchObject({ inUse: 1, state: "busy" }); // the operator does not change over
    expect(m.at(7).nodes.p2).toMatchObject({ setup: 0, state: "idle" });
    expect(m.at(7).resources.m2!.state).toBe("idle");
  });

  it("loads a batch together and holds one unit for it (D-054)", () => {
    const b = { batch_id: "r0/oven/b1" };
    const m = motion(
      trace([
        ev(0, "arrival", "a", "src"), ev(0, "queue_enter", "a", "oven"), ev(1, "arrival", "b", "src"), ev(1, "queue_enter", "b", "oven"),
        ev(2, "service_start", "a", "oven", b), ev(2, "service_start", "b", "oven", b),
        ev(9, "service_end", "a", "oven", b), ev(9, "queue_enter", "a", "q2"), ev(9, "service_end", "b", "oven", b), ev(9, "queue_enter", "b", "q2"),
      ]),
      model,
      10,
    );
    const f = m.at(5);
    expect(f.boards.map((x) => [x.id, x.state, x.batch])).toEqual([["a", "batch", "r0/oven/b1"], ["b", "batch", "r0/oven/b1"]]);
    expect(f.nodes.oven).toMatchObject({ inService: 2, batches: [{ id: "r0/oven/b1", members: 2 }], state: "busy" });
    expect(f.resources.c).toMatchObject({ inUse: 1, state: "busy" });
    expect(m.at(9).nodes.q2!.waiting).toBe(2);
    expect(m.at(9).nodes.oven!.batches).toEqual([]);
  });

  it("counts a failed test at its failure node, never as a machine down", () => {
    const m = motion(
      trace([ev(0, "arrival", "f", "src"), ev(0, "queue_enter", "f", "q"), ev(1, "service_start", "f", "p"), ev(4, "service_end", "f", "p"), ev(4, "failure", "f", "fail"), ev(4, "queue_enter", "f", "q2")]),
      model,
      10,
    );
    const f = m.at(5);
    expect(where(f)).toEqual({ f: "queued@q2" });
    expect(f.nodes.fail!.through).toBe(1);
    expect(m.at(3).nodes.fail!.through).toBe(0);
    const states = [0, 1, 2, 3, 4, 5, 10].flatMap((t) => [...Object.values(m.at(t).nodes), ...Object.values(m.at(t).resources)].map((n) => n.state));
    expect(states).not.toContain("failed");
  });

  it("puts a delayed item in its delay until it ends", () => {
    const m = motion(trace([ev(0, "arrival", "d1", "src"), ev(0, "delay_start", "d1", "d"), ev(6, "delay_end", "d1", "d"), ev(6, "abandon", "d1", "sink")]), model, 10);
    expect(m.at(3).nodes.d).toMatchObject({ delayed: 1, state: "busy" });
    expect(where(m.at(3))).toEqual({ d1: "delay@d" });
    expect(where(m.at(6))).toEqual({});
    expect(m.at(6).nodes.sink!.through).toBe(1);
  });
});

describe("the slide of a hop", () => {
  const two = () => motion(trace([...e1(), ...e2()]), model, 20);

  it("slides an item from its last node over the hop's ticks, then draws it at its node", () => {
    const m = two();
    // e1 entered q at 0 from src (same tick): sliding src → q.
    expect(m.at(0, { hop: 2 }).boards.find((b) => b.id === "e1")!.hop).toEqual({ path: ["src", "q"], steps: [0] });
    expect(m.at(1, { hop: 2 }).boards.find((b) => b.id === "e1")!.hop).toEqual({ path: ["src", "q"], steps: [0.5] });
    expect(m.at(2, { hop: 2 }).boards.find((b) => b.id === "e1")!.hop).toBeNull();
    // q → p at 5.
    expect(m.at(6, { hop: 4 }).boards.find((b) => b.id === "e1")!.hop).toEqual({ path: ["q", "p"], steps: [0.25] });
  });

  it("draws no slide with no hop ticks (reduced motion), and never moves the counts", () => {
    const m = two();
    expect(m.at(6).boards.every((b) => b.hop === null)).toBe(true);
    expect(m.at(6, { hop: 0 }).boards.every((b) => b.hop === null)).toBe(true);
    // e1 is at p from 5 on, sliding or not.
    expect(m.at(5, { hop: 4 }).nodes.p!.inService).toBe(1);
  });

  it("starts a hop from where the last one had got to, so nothing jumps back", () => {
    // e1: q2 and p2 at 12 (same tick, from p), then the sink at 15. Hop 5: at 15 the slide into p2 is 3/5 done.
    const m = two();
    const left = m.at(16, { hop: 5 }).boards.find((b) => b.id === "e1")!;
    expect(left).toMatchObject({ state: "left", node: "sink" });
    expect(left.hop).toEqual({ path: ["p", "p2", "sink"], steps: [0.6, 0.2] });
    // A line of positions: p 0, p2 10, sink 20. Drawn at 0 + (10 − 0) · 0.6 = 6, then 6 + (20 − 6) · 0.2 = 8.8.
    const x = placeOnHop(left.hop!, (n) => ({ p: 0, p2: 10, sink: 20 })[n]!, (a, b, s) => a + (b - a) * s);
    expect(x).toBeCloseTo(8.8, 10);
    // Continuous: just before 15 it was at 0 + 10 · (2.999/5).
    const before = m.at(14.999, { hop: 5 }).boards.find((b) => b.id === "e1")!;
    expect(placeOnHop(before.hop!, (n) => ({ p: 0, p2: 10, sink: 20 })[n]!, (a, b, s) => a + (b - a) * s)).toBeCloseTo(5.998, 3);
  });

  it("slides a completed item into its sink, then it is gone", () => {
    const m = two();
    expect(where(m.at(15, { hop: 2 }))).toMatchObject({ e1: "left@sink" });
    expect(where(m.at(16.9, { hop: 2 }))).toMatchObject({ e1: "left@sink" });
    expect(where(m.at(17, { hop: 2 }))).not.toHaveProperty("e1");
    expect(m.at(15, { hop: 2 }).inLine).toBe(1);
  });

  it("finishes the hops a cut-short trace's last events began while it holds, then draws every item at its node (H-072 review)", () => {
    // e2 reaches p at 9, the trace's last event: the clock runs on past the end, and so does that slide.
    const m = motion(trace(e2(), true), model, 20);
    expect(m.end).toBe(9);
    expect(m.at(9, { hop: 4 }).boards[0]!.hop).toEqual({ path: ["q", "p"], steps: [0] });
    const half = m.at(11, { hop: 4 });
    expect(half.tick).toBe(9); // the line itself holds at the end
    expect(half.boards[0]!.hop).toEqual({ path: ["q", "p"], steps: [0.5] });
    for (const t of [13, 15, 20]) {
      const f = m.at(t, { hop: 4 });
      expect(f.tick).toBe(9);
      expect(f.boards.map((b) => [b.id, b.state, b.node, b.hop])).toEqual([["e2", "service", "p", null]]);
    }
    // An item that left at the end slides into its sink, then is gone; nothing else changes.
    const left = motion(trace(e1().slice(0, 10), true), model, 20);
    expect(left.end).toBe(15);
    expect(where(left.at(16, { hop: 2 }))).toEqual({ e1: "left@sink" });
    expect(left.at(17, { hop: 2 }).boards).toEqual([]);
    expect(left.at(20, { hop: 2 }).nodes.sink!.through).toBe(1);
  });
});

describe("a limit on the boards drawn", () => {
  it("draws the front of every stack first and counts the rest", () => {
    const events = [
      ...["a", "b", "c", "d"].flatMap((id, i) => [ev(i, "arrival", id, "src"), ev(i, "queue_enter", id, "q")]),
      ...["x", "y"].flatMap((id, i) => [ev(i, "arrival", id, "src"), ev(i, "queue_enter", id, "q2")]),
    ];
    const f = motion(trace(events), model, 10).at(5, { limit: 3 });
    expect(f.boards.map((b) => b.id)).toEqual(["a", "b", "x"]);
    expect(f.notDrawn).toBe(3);
    expect(f.nodes.q!.waiting).toBe(4); // the counts are whole
  });
});

// A plain scan: each item's last event at or before t, read with the same semantics.
function scan(t: TraceArtifact, nodes: ReadonlySet<string>, tick: number) {
  const last = new Map<string, { state: BoardState | "gone"; node: string }>();
  const prevEvent = new Map<string, TraceEvent>();
  for (const e of t.events) {
    if (e.time_tick > tick) break;
    const prev = prevEvent.get(e.entity_id);
    prevEvent.set(e.entity_id, e);
    const set = (state: BoardState | "gone") => last.set(e.entity_id, { state, node: e.node_id });
    switch (e.type) {
      case "arrival": set("arrived"); break;
      case "queue_enter": set("queued"); break;
      case "setup_start": set("setup"); break;
      case "service_start":
        set(e.batch_id ? "batch" : prev?.type === "setup_start" && prev.node_id === e.node_id && prev.time_tick === e.time_tick ? "setup" : "service");
        break;
      case "blocked": set("blocked"); break;
      case "delay_start": set("delay"); break;
      case "service_end": case "delay_end": case "unblocked": set("done"); break;
      case "complete": case "abandon": set("gone"); break;
      default: break;
    }
  }
  const out: Record<string, string> = {};
  const waiting: Record<string, number> = {};
  for (const [id, s] of last) {
    if (s.state === "gone" || !nodes.has(s.node)) continue;
    out[id] = `${s.state}@${s.node}`;
    if (s.state === "queued") waiting[s.node] = (waiting[s.node] ?? 0) + 1;
  }
  return { out, waiting };
}

describe("the engine's own traces of the electronics line (1 day, seed 1)", () => {
  const data = readElectronicsLine();
  const nodes = new Set(data.model.nodes.map((n) => n.id));
  const horizon = data.results.baseline.measurement_window.horizon_ticks;

  it("agree with a plain scan of the events at any time, in any order of asking", () => {
    for (const side of ["baseline", "scenario"] as const) {
      const m = motion(data.traces[side], data.model, horizon);
      let x = 7;
      const rnd = () => ((x = (x * 1103515245 + 12345) % 2147483648) / 2147483648);
      const times = [0, horizon, horizon / 2, ...Array.from({ length: 150 }, () => Math.floor(rnd() * horizon)), 0, horizon];
      for (const t of times) {
        const f = m.at(t);
        const { out, waiting } = scan(data.traces[side], nodes, t);
        expect(where(f), `${side} at ${t}`).toEqual(out);
        for (const id of nodes) expect(f.nodes[id]!.waiting, `${side} ${id} at ${t}`).toBe(waiting[id] ?? 0);
      }
    }
  });

  it("are whole: they play to the horizon", () => {
    const m = motion(data.traces.baseline, data.model, horizon);
    expect({ truncated: m.truncated, end: m.end, items: m.items }).toEqual({ truncated: false, end: 86_400_000, items: 87 });
  });

  it("show the pile before FCT draining with a fourth tester", () => {
    const pile = (side: "baseline" | "scenario") => {
      const m = motion(data.traces[side], data.model, horizon);
      let sum = 0;
      for (let i = 0; i <= 1000; i++) sum += m.at((i / 1000) * horizon).nodes.q_fct!.waiting;
      return sum / 1001;
    };
    // The run's own figure: q_fct's mean length falls from 5.5 to 0.75 (all boards); the sample shows the same fall.
    expect(data.results.baseline.metrics.queues.q_fct!.queue_length_mean).toBeGreaterThan(5);
    expect(data.results.scenario.metrics.queues.q_fct!.queue_length_mean).toBeLessThan(1);
    expect(pile("baseline")).toBeGreaterThan(3 * pile("scenario"));
  });

  it("draw burn-in batches and blocked boards from the trace", () => {
    const m = motion(data.traces.baseline, data.model, horizon);
    const seen = { batch: 0, blocked: 0 };
    for (let i = 0; i <= 500; i++) {
      const f = m.at((i / 500) * horizon);
      seen.batch = Math.max(seen.batch, f.nodes.burn_in!.batches.length);
      seen.blocked += f.boards.filter((b) => b.state === "blocked").length;
      for (const b of f.boards) expect(nodes.has(b.node)).toBe(true);
    }
    expect(seen.batch).toBeGreaterThan(0);
    expect(seen.blocked).toBeGreaterThan(0);
  });
});

describe("speed for the engine's cap of 100,000 events", () => {
  it("builds once and then plays frame by frame without scanning every event", () => {
    // 5,000 items of 20 events each, through src → q → p → q2 → p2 → sink, staggered over the run.
    const events: TraceEvent[] = [];
    const per = 20;
    const items = 100_000 / per;
    const horizon = items * 100;
    const steps: [TraceEvent["type"], string][] = [["arrival", "src"], ["queue_enter", "q"], ["service_start", "p"], ["service_end", "p"], ["queue_enter", "q2"], ["service_start", "p2"], ["service_end", "p2"]];
    const all: { t: number; e: TraceEvent }[] = [];
    for (let i = 0; i < items; i++) {
      let t = i * 100;
      let k = 0;
      for (let j = 0; j < per - 1; j++) {
        const [type, node] = steps[k++ % steps.length]!;
        all.push({ t, e: { time_tick: t, sequence: 1, type, entity_id: `r0/src/${i + 1}`, node_id: node } });
        t += 7;
      }
      all.push({ t, e: { time_tick: t, sequence: 1, type: "complete", entity_id: `r0/src/${i + 1}`, node_id: "sink" } });
    }
    all.sort((a, b) => a.t - b.t);
    for (const { e } of all) events.push(e);
    expect(events.length).toBe(100_000);

    const built = performance.now();
    const m = motion(trace(events), model, horizon);
    expect(performance.now() - built).toBeLessThan(2000);
    // Playing: 600 frames of a 30 s playback at 60 frames a second (each a 1,800th of the run).
    let t0 = performance.now();
    for (let i = 0; i < 600; i++) m.at(horizon / 3 + (i * horizon) / 1800, { hop: horizon / 100 });
    const frame = (performance.now() - t0) / 600;
    // Jumping from the start to the end: each jump applies 98% of the 100,000 changes.
    let jumps = 0;
    for (let i = 0; i < 20; i++) {
      m.at(0.01 * horizon);
      t0 = performance.now();
      m.at(0.99 * horizon, { hop: horizon / 100 });
      jumps += performance.now() - t0;
    }
    const jump = jumps / 20;
    // A frame costs the changes since the last one and the items in the line, not the whole trace (measured about 20 times less).
    expect(frame * 5).toBeLessThan(jump);
    // A jump back and forth stays correct.
    expect(where(m.at(horizon / 3))).toEqual(where(motion(trace(events), model, horizon).at(horizon / 3)));
  });
});
