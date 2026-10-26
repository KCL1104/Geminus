// T-032 (hackathon slice, H-029): the Marey chart's geometry is a pure
// function of the model's steps and the sampled trace (DESIGN.md Marey
// Strip; SIMULATION_SPEC §9). Time runs left to right, the steps top to
// bottom, one line per sampled work item; a flat stretch is waiting. Every
// case is computed by hand; the same input always gives the same chart, and
// nothing is drawn that the trace does not hold (no invented trajectory).
import { describe, expect, it } from "vitest";
import type { ModelIR, TraceArtifact, TraceEvent } from "../lib/contracts";
import {
  assignLanes,
  entityPaths,
  fitLabel,
  mareyLayout,
  rowResolver,
  stepRows,
  timeTicks,
  traceSummary,
  type EntityPath,
} from "../features/results/marey";
import boardModel from "../features/results/fixtures/board-line-model.json";
import boardBaselineTrace from "../features/results/fixtures/board-line-baseline-trace.json";
import boardScenarioTrace from "../features/results/fixtures/board-line-scenario-trace.json";

type N = ModelIR["nodes"][number];
const src = (id: string, name?: string): N => ({ id, type: "source", ...(name ? { name } : {}), entity_type: "item", arrival: { distribution: { type: "constant", value: 1 } } });
const proc = (id: string, name?: string): N => ({ id, type: "process", ...(name ? { name } : {}), service_time: { distribution: { type: "constant", value: 1 } }, requires: [] });
const queue = (id: string): N => ({ id, type: "queue" });
const sink = (id: string, name?: string): N => ({ id, type: "sink", ...(name ? { name } : {}) });
const decision = (id: string): N => ({ id, type: "decision" });
const rework = (id: string): N => ({ id, type: "rework" });
const delay = (id: string): N => ({ id, type: "delay", delay_time: { distribution: { type: "constant", value: 1 } } });

function model(nodes: N[], flows: [string, string][]): ModelIR {
  return {
    schema_version: "0.1", model_id: "mdl_01M4YDQK00TA00000000000001", domain: "test", name: "Test", time_unit: "minute",
    entity_types: [{ id: "item", name: "Item" }], resources: [], nodes,
    flows: flows.map(([from, to], i) => ({ id: `f${i}`, from, to, routing: { type: "probability", probability: 1 } })),
    constraints: [], metrics: [], views: { "2d": {}, "3d": {} },
  };
}

// s → p1 (entered directly) → q → p2 → k
const line = model([src("s", "Arrive"), proc("p1", "Cut"), queue("q"), proc("p2"), sink("k", "Done")], [["s", "p1"], ["p1", "q"], ["q", "p2"], ["p2", "k"]]);

let seq = 0;
const ev = (t: number, type: TraceEvent["type"], node: string, entity = "r0/s/1"): TraceEvent => ({ time_tick: t, sequence: ++seq, type, entity_id: entity, node_id: node });
const trace = (events: TraceEvent[], truncated = false): TraceArtifact => ({
  format: "geminus.trace.v1", engine_version: "des-go-0.5.0", replication_index: 0,
  sampling: { method: "entity_id_sha256", rate: 0.125, max_events: 100000 }, truncated, events,
});
const paths = (ir: ModelIR, events: TraceEvent[], end = 100, truncated = false) => entityPaths(trace(events, truncated), rowResolver(ir, stepRows(ir)), end);
const pts = (p: EntityPath | undefined) => (p?.points ?? []).map((x) => ["row" in x.place ? `r${x.place.row}` : `b${x.place.between.join("-")}`, x.t]);

describe("the steps (rows), top to bottom", () => {
  it("are the model's sources, processes, delays and sinks in flow order, without queues; a missing name is the ID", () => {
    expect(stepRows(line)).toEqual([
      { id: "s", name: "Arrive", sink: false },
      { id: "p1", name: "Cut", sink: false },
      { id: "p2", name: "p2", sink: false },
      { id: "k", name: "Done", sink: true },
    ]);
  });

  it("keep the forward order when rework loops back, and put the sinks last", () => {
    // The IR lists the sink and the rework first; the loop r → a is a back edge.
    const ir = model([sink("k"), rework("r"), proc("b"), src("s"), decision("d"), proc("a")], [["s", "a"], ["a", "b"], ["b", "d"], ["d", "k"], ["d", "r"], ["r", "a"]]);
    expect(stepRows(ir).map((r) => r.id)).toEqual(["s", "a", "b", "k"]);
  });

  it("order parallel branches by the model's own order, and every sink after every other step", () => {
    const ir = model([src("s"), proc("b"), proc("a"), sink("scrap"), proc("c"), sink("ok")], [["s", "a"], ["s", "b"], ["a", "c"], ["b", "c"], ["a", "scrap"], ["c", "ok"]]);
    expect(stepRows(ir).map((r) => r.id)).toEqual(["s", "b", "a", "c", "scrap", "ok"]);
  });

  it("puts a delay in its place in the flow", () => {
    const ir = model([src("s"), delay("d"), proc("p"), sink("k")], [["s", "d"], ["d", "p"], ["p", "k"]]);
    expect(stepRows(ir).map((r) => r.id)).toEqual(["s", "d", "p", "k"]);
  });
});

describe("where an event puts a work item", () => {
  it("is its step's row; a queue's is the step it feeds; a routing node or an unknown node has none", () => {
    const ir = model([src("s"), queue("q1"), queue("q2"), proc("p"), decision("d"), queue("q3"), sink("k"), sink("k2")], [["s", "q1"], ["q1", "q2"], ["q2", "p"], ["p", "d"], ["d", "q3"], ["q3", "k"], ["q3", "k2"]]);
    const row = rowResolver(ir, stepRows(ir));
    expect(["s", "q1", "q2", "p", "d", "q3", "k", "k2", "nope"].map(row)).toEqual([0, 1, 1, 1, null, null, 2, 3, null]);
  });
});

describe("a work item's line (SIMULATION_SPEC §9: only what the trace holds)", () => {
  it("is flat while it waits and runs to the next step while it is served", () => {
    const [p, ...rest] = paths(line, [
      ev(0, "arrival", "s"), ev(0, "queue_enter", "p1"), ev(0, "service_start", "p1"), ev(10, "service_end", "p1"),
      ev(10, "queue_enter", "q"), ev(25, "service_start", "p2"), ev(30, "service_end", "p2"), ev(30, "complete", "k"),
    ]);
    expect(rest).toEqual([]);
    // Into Cut at once; served 0–10 on the way to p2; waits 10–25 at p2; served 25–30 into Done.
    expect(pts(p)).toEqual([["r0", 0], ["r1", 0], ["r2", 10], ["r2", 25], ["r3", 30]]);
    expect(p?.release).toEqual({ t: 30, row: 3, outcome: "complete" });
    expect(p?.waitingAtEnd).toBe(false);
  });

  it("holds between two steps while it is blocked after its service, then enters the next step", () => {
    const [p] = paths(line, [
      ev(0, "arrival", "s"), ev(0, "queue_enter", "p1"), ev(0, "service_start", "p1"), ev(10, "service_end", "p1"),
      ev(10, "blocked", "p1"), ev(14, "unblocked", "p1"), ev(14, "queue_enter", "q"), ev(14, "service_start", "p2"),
    ]);
    expect(pts(p)).toEqual([["r0", 0], ["r1", 0], ["b1-2", 10], ["b1-2", 14], ["r2", 14]]);
  });

  it("stays flat at the source while the source is blocked", () => {
    const [p] = paths(line, [ev(5, "arrival", "s"), ev(5, "blocked", "s"), ev(9, "unblocked", "s"), ev(9, "queue_enter", "p1")]);
    expect(pts(p).slice(0, 3)).toEqual([["r0", 5], ["r0", 9], ["r1", 9]]);
  });

  it("runs flat to the end of the run when the item is still waiting there, and says so", () => {
    const [p] = paths(line, [ev(50, "arrival", "s"), ev(50, "queue_enter", "p1")], 100);
    expect(pts(p)).toEqual([["r0", 50], ["r1", 50], ["r1", 100]]);
    expect(p?.waitingAtEnd).toBe(true);
    expect(p?.release).toBeNull();
  });

  it("ends where recording stopped when the trace is truncated: nothing is extended", () => {
    const [p] = paths(line, [ev(50, "arrival", "s"), ev(50, "queue_enter", "p1")], 100, true);
    expect(pts(p)).toEqual([["r0", 50], ["r1", 50]]);
    expect(p?.waitingAtEnd).toBe(false);
  });

  it("ends at its service start when it is being served at the end (where it goes next is unknown)", () => {
    const [p] = paths(line, [ev(90, "arrival", "s"), ev(90, "queue_enter", "p1"), ev(90, "service_start", "p1")], 100);
    expect(pts(p)).toEqual([["r0", 90], ["r1", 90]]);
    expect(p?.waitingAtEnd).toBe(false);
  });

  it("goes back up for rework and ignores the routing and changeover events", () => {
    const ir = model([src("s"), proc("a"), proc("b"), decision("d"), rework("r"), sink("k")], [["s", "a"], ["a", "b"], ["b", "d"], ["d", "r"], ["d", "k"], ["r", "a"]]);
    const [p] = paths(ir, [
      ev(0, "arrival", "s"), ev(0, "queue_enter", "a"), { ...ev(0, "setup_start", "a"), resource_id: "m" }, ev(0, "service_start", "a"), ev(4, "service_end", "a"),
      ev(4, "queue_enter", "b"), ev(4, "service_start", "b"), ev(6, "service_end", "b"), ev(6, "rework", "r"), ev(6, "queue_enter", "a"),
      ev(8, "service_start", "a"), ev(9, "service_end", "a"), ev(9, "queue_enter", "b"), ev(9, "service_start", "b"), ev(10, "service_end", "b"), ev(10, "failure", "d"), ev(10, "complete", "k"),
    ]);
    expect(pts(p)).toEqual([["r0", 0], ["r1", 0], ["r2", 4], ["r1", 6], ["r1", 8], ["r2", 9], ["r3", 10]]);
  });

  it("takes the row of the step it is served at when its queue feeds more than one step", () => {
    const ir = model([src("s"), queue("q"), proc("a"), proc("b"), sink("k")], [["s", "q"], ["q", "a"], ["q", "b"], ["a", "k"], ["b", "k"]]);
    const [p] = paths(ir, [ev(0, "arrival", "s"), ev(0, "queue_enter", "q"), ev(3, "service_start", "b")]);
    expect(pts(p)).toEqual([["r0", 0], ["r2", 0], ["r2", 3]]);
  });

  it("drops a queue entry it cannot place rather than guessing, and then draws nothing after it", () => {
    const ir = model([src("s"), queue("q"), proc("a"), proc("b"), sink("k")], [["s", "q"], ["q", "a"], ["q", "b"], ["a", "k"], ["b", "k"]]);
    const [p] = paths(ir, [ev(0, "arrival", "s"), ev(0, "queue_enter", "q")], 100);
    // Not extended along the source row: the item is no longer there.
    expect(pts(p)).toEqual([["r0", 0]]);
    expect(p?.waitingAtEnd).toBe(false);
  });

  it("is served by a delay like by a process, and an abandoned item's release says so", () => {
    const ir = model([src("s"), delay("d"), sink("k")], [["s", "d"], ["d", "k"]]);
    const [p] = paths(ir, [ev(0, "arrival", "s"), ev(2, "delay_start", "d"), ev(7, "delay_end", "d"), ev(7, "abandon", "k")]);
    expect(pts(p)).toEqual([["r0", 0], ["r0", 2], ["r1", 2], ["r2", 7]]);
    expect(p?.release).toEqual({ t: 7, row: 2, outcome: "abandon" });
  });

  it("keeps one line per item, in the order the items first appear, whatever the interleaving", () => {
    const ps = paths(line, [ev(0, "arrival", "s", "r0/s/2"), ev(1, "arrival", "s", "r0/s/1"), ev(1, "queue_enter", "p1", "r0/s/2"), ev(1, "queue_enter", "p1", "r0/s/1")]);
    expect(ps.map((p) => p.entity)).toEqual(["r0/s/2", "r0/s/1"]);
  });

  it("has no line for an empty trace", () => {
    expect(paths(line, [])).toEqual([]);
  });
});

describe("lanes inside a row", () => {
  it("give overlapping waits their own lane, lowest free first, and reuse a lane once it is free", () => {
    const ps = paths(line, [
      ev(0, "arrival", "s", "a"), ev(0, "queue_enter", "p1", "a"), ev(0, "service_start", "p1", "a"),
      ev(1, "arrival", "s", "b"), ev(1, "queue_enter", "p1", "b"),
      ev(2, "arrival", "s", "c"), ev(2, "queue_enter", "p1", "c"),
      ev(4, "service_start", "p1", "b"), ev(8, "service_start", "p1", "c"),
      ev(9, "arrival", "s", "d"), ev(9, "queue_enter", "p1", "d"), ev(9, "service_start", "p1", "d"),
    ], 20);
    const lanes = assignLanes(ps);
    // In row 1: a stays [0, 0], b [1, 4], c [2, 8], d [9, 9].
    const row1 = ps.map((p, i) => p.points.flatMap((x, j) => ("row" in x.place && x.place.row === 1 ? [lanes[i]![j]] : [])));
    expect(row1).toEqual([[0], [0, 0], [1, 1], [0]]);
  });
});

describe("the time axis", () => {
  it("chooses the smallest readable unit and a round step that fits the width", () => {
    const minute = 60_000;
    const hour = 60 * minute;
    expect(timeTicks(600 * minute, 640)).toEqual({ unit: "hour", step: 1, values: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10] });
    expect(timeTicks(24 * hour, 640)).toEqual({ unit: "hour", step: 3, values: [0, 3, 6, 9, 12, 15, 18, 21, 24] });
    expect(timeTicks(45 * minute, 640)).toEqual({ unit: "minute", step: 5, values: [0, 5, 10, 15, 20, 25, 30, 35, 40, 45] });
    expect(timeTicks(365 * 24 * hour, 640)).toEqual({ unit: "day", step: 60, values: [0, 60, 120, 180, 240, 300, 360] });
    expect(timeTicks(500, 640)).toEqual({ unit: "second", step: 1, values: [0] });
    expect(timeTicks(2000 * 24 * hour, 640)).toEqual({ unit: "day", step: 365, values: [0, 365, 730, 1095, 1460, 1825] });
    expect(timeTicks(20000 * 24 * hour, 640)).toEqual({ unit: "day", step: 2000, values: [0, 2000, 4000, 6000, 8000, 10000, 12000, 14000, 16000, 18000, 20000] });
    // A narrow chart keeps at least two ticks' room.
    expect(timeTicks(10 * hour, 100).values.length).toBeLessThanOrEqual(3);
  });
});

describe("row labels", () => {
  it("keep a name that fits and shorten one that does not, with an ellipsis", () => {
    expect(fitLabel("SMT", 100)).toBe("SMT");
    expect(fitLabel("A very long step name that never fits", 100)).toBe("A very long…");
    expect(fitLabel("燒機測試站第一區第二段", 80)).toBe("燒機測試站…");
  });
});

describe("the chart layout", () => {
  const events = [
    ev(0, "arrival", "s", "a"), ev(0, "queue_enter", "p1", "a"), ev(0, "service_start", "p1", "a"), ev(10, "service_end", "p1", "a"),
    ev(10, "queue_enter", "q", "a"), ev(25, "service_start", "p2", "a"), ev(30, "service_end", "p2", "a"), ev(30, "complete", "k", "a"),
  ];
  const rows = stepRows(line);
  const row = rowResolver(line, rows);
  const one = entityPaths(trace(events), row, 100);

  it("maps time to x across the plot and puts baseline lanes at a row's top, scenario lanes at its bottom", () => {
    const l = mareyLayout({ rows, baseline: one, scenario: one, endTick: 100, width: 600 });
    expect([l.width, l.x1]).toEqual([600, 584]);
    expect(l.rows.map((r) => [r.label, r.y, r.height, r.sink])).toEqual([["Arrive", 24, 28, false], ["Cut", 52, 28, false], ["p2", 80, 28, false], ["Done", 108, 28, true]]);
    expect(l.height).toBe(140);
    const x = (t: number) => Math.round((l.x0 + (t / 100) * (l.x1 - l.x0)) * 10) / 10;
    // Every release lands on the sink row's one station line, its centre (DESIGN.md).
    expect(l.baseline?.lines[0]?.points).toEqual([[x(0), 27], [x(0), 55], [x(10), 83], [x(25), 83], [x(30), 122]]);
    expect(l.scenario?.lines[0]?.points).toEqual([[x(0), 49], [x(0), 77], [x(10), 105], [x(25), 105], [x(30), 122]]);
    expect(l.baseline?.releases).toEqual([[x(30), 122]]);
    expect(l.stations).toEqual([122]);
    expect([l.baseline?.drawn, l.baseline?.total]).toEqual([1, 1]);
  });

  it("draws only the runs it has", () => {
    const l = mareyLayout({ rows, baseline: null, scenario: one, endTick: 100, width: 600 });
    expect(l.baseline).toBeNull();
    expect(l.scenario?.lines).toHaveLength(1);
  });

  it("caps the lines it draws per run, keeping the first items, and says how many", () => {
    const many = Array.from({ length: 5 }, (_, i) => [ev(i, "arrival", "s", `e${i}`), ev(i, "queue_enter", "p1", `e${i}`)]).flat();
    const l = mareyLayout({ rows, baseline: entityPaths(trace(many), row, 100), scenario: null, endTick: 100, width: 600, maxLines: 3 });
    expect(l.baseline?.lines.map((x) => x.entity)).toEqual(["e0", "e1", "e2"]);
    expect([l.baseline?.drawn, l.baseline?.total]).toEqual([3, 5]);
  });

  it("puts a major line on every axis tick and a minor line every quarter step (DESIGN.md Minor and Major Time Grid)", () => {
    const wide = mareyLayout({ rows, baseline: one, scenario: null, endTick: 600 * 60_000, width: 1100 });
    expect(wide.unit).toBe("hour");
    expect(wide.ticks.map((t) => t.value)).toEqual([0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10]);
    expect(wide.minor.length).toBe(30);
    // On a phone the step grows (every 3 hours) and the quarters follow it.
    const narrow = mareyLayout({ rows, baseline: one, scenario: null, endTick: 600 * 60_000, width: 360 });
    expect(narrow.ticks.map((t) => t.value)).toEqual([0, 3, 6, 9]);
    expect(narrow.minor.length).toBe(10);
  });
});

describe("the chart of a real run (des-go-0.5.0, a synthetic board line)", () => {
  const ir = boardModel as unknown as ModelIR;
  const base = boardBaselineTrace as unknown as TraceArtifact;
  const scen = boardScenarioTrace as unknown as TraceArtifact;
  const end = 600 * 60_000;
  const build = () => {
    const rows = stepRows(ir);
    const row = rowResolver(ir, rows);
    return mareyLayout({ rows, baseline: entityPaths(base, row, end), scenario: entityPaths(scen, row, end), endTick: end, width: 1040 });
  };

  it("is the same every time", () => {
    expect(build()).toEqual(build());
  });

  it("has one line per sampled board, inside the plot, and a release dot per shipped board", () => {
    const l = build();
    expect(l.rows.map((r) => r.label)).toEqual(["Boards in", "SMT", "AOI", "ICT", "Pack", "Shipped"]);
    for (const [run, t] of [[l.baseline, base], [l.scenario, scen]] as const) {
      const entities = new Set(t.events.map((e) => e.entity_id));
      expect(run?.lines.length).toBe(entities.size);
      expect(run?.releases.length).toBe(t.events.filter((e) => e.type === "complete").length);
      for (const line of run?.lines ?? []) {
        for (const [x, y] of line.points) {
          expect(x).toBeGreaterThanOrEqual(l.x0);
          expect(x).toBeLessThanOrEqual(l.x1);
          expect(y).toBeGreaterThanOrEqual(l.top);
          expect(y).toBeLessThanOrEqual(l.bottom);
        }
      }
    }
  });

  it("shows the ICT queue that the scenario's second fixture removes", () => {
    const rows = stepRows(ir);
    const row = rowResolver(ir, rows);
    const ict = rows.findIndex((r) => r.id === "ict");
    const b = traceSummary(entityPaths(base, row, end), rows)[ict]!;
    const s = traceSummary(entityPaths(scen, row, end), rows)[ict]!;
    expect(b.longestWait).toBeGreaterThan(s.longestWait ?? 0);
    expect(s.longestWait).toBe(0);
  });
});

describe("the chart as a table (the text alternative)", () => {
  it("counts the sampled items per step, their longest finished wait and the items still waiting at the end", () => {
    const rows = stepRows(line);
    const ps = paths(line, [
      ev(0, "arrival", "s", "a"), ev(0, "queue_enter", "p1", "a"), ev(0, "service_start", "p1", "a"), ev(10, "service_end", "p1", "a"),
      ev(10, "queue_enter", "q", "a"), ev(25, "service_start", "p2", "a"), ev(30, "service_end", "p2", "a"), ev(30, "complete", "k", "a"),
      ev(40, "arrival", "s", "b"), ev(40, "queue_enter", "p1", "b"),
    ], 100);
    expect(traceSummary(ps, rows)).toEqual([
      { items: 2, longestWait: 0, waitingAtEnd: 0 },
      { items: 2, longestWait: 0, waitingAtEnd: 1 },
      { items: 1, longestWait: 15, waitingAtEnd: 0 },
      { items: 1, longestWait: null, waitingAtEnd: 0 },
    ]);
  });
});
