// T-034 (TEST-025, TEST-026): the 3D playback places each sampled item at
// the node of its last located event, from the trace alone: no item the
// trace lacks, nothing interpolated, nothing after a truncated trace ends.
import { describe, expect, it } from "vitest";
import type { TraceArtifact, TraceEvent } from "../lib/contracts";
import { timeline } from "../features/twin-3d/playback/occupancy";

let seq = 0;
const ev = (time_tick: number, type: TraceEvent["type"], entity_id: string, node_id: string): TraceEvent => ({ time_tick, sequence: seq++, type, entity_id, node_id });
const trace = (events: TraceEvent[], truncated = false): TraceArtifact => ({
  format: "geminus.trace.v1",
  engine_version: "des-go-0.5.0",
  replication_index: 0,
  sampling: { method: "entity_id_sha256", rate: 0.5, max_events: 100_000 },
  truncated,
  events,
});
const nodes = new Set(["src", "q", "p", "q2", "p2", "sink"]);

// e1 waits at q, is served at p, is held there (blocked) until q2 has room, then leaves through p2.
const e1 = [
  ev(0, "arrival", "e1", "src"), ev(0, "queue_enter", "e1", "q"), ev(5, "service_start", "e1", "p"), ev(8, "service_end", "e1", "p"),
  ev(8, "blocked", "e1", "p"), ev(12, "unblocked", "e1", "p"), ev(12, "queue_enter", "e1", "q2"), ev(12, "service_start", "e1", "p2"),
  ev(15, "service_end", "e1", "p2"), ev(15, "complete", "e1", "sink"),
];
const e2 = [ev(3, "arrival", "e2", "src"), ev(3, "queue_enter", "e2", "q"), ev(9, "service_start", "e2", "p")];

describe("the items at each node over a run", () => {
  it("follow each item's located events, held items staying at their station, finished ones gone", () => {
    const tl = timeline(trace([...e1, ...e2]), nodes, 20);
    expect(tl.end).toBe(20);
    expect([0, 3, 5, 8, 9, 11, 12, 15, 20].map((t) => tl.at(t))).toEqual([
      { q: 1 },
      { q: 2 },
      { p: 1, q: 1 },
      { p: 1, q: 1 }, // service ended, but blocked: still at p
      { p: 2 }, // e2 starts at p while e1 is held there
      { p: 2 },
      { p: 1, p2: 1 },
      { p: 1 },
      { p: 1 }, // e2 was still being served when the run ended
    ]);
  });

  it("give the same counts going back as from the start (the cursor starts over)", () => {
    const tl = timeline(trace([...e1, ...e2]), nodes, 20);
    tl.at(20);
    expect(tl.at(5)).toEqual({ p: 1, q: 1 });
    expect(tl.at(5)).toEqual(timeline(trace([...e1, ...e2]), nodes, 20).at(5));
  });

  it("end at a truncated trace's last event, never drawing past what was recorded", () => {
    const tl = timeline(trace(e2, true), nodes, 20);
    expect(tl.end).toBe(9);
    expect(tl.at(20)).toEqual({ p: 1 });
  });

  it("leave out an item at a node the model does not have, rather than guess where it is", () => {
    const tl = timeline(trace([ev(0, "arrival", "e3", "src"), ev(1, "queue_enter", "e3", "elsewhere"), ev(4, "service_start", "e3", "p")]), nodes, 10);
    expect([tl.at(0), tl.at(2), tl.at(4)]).toEqual([{ src: 1 }, {}, { p: 1 }]);
  });

  it("place items in a delay and in a batch, and take away one that abandons", () => {
    const tl = timeline(
      trace([
        ev(0, "arrival", "a", "src"), ev(0, "delay_start", "a", "q2"), ev(6, "delay_end", "a", "q2"), ev(6, "abandon", "a", "sink"),
        { ...ev(2, "service_start", "b1", "p"), batch_id: "x" }, { ...ev(2, "service_start", "b2", "p"), batch_id: "x" },
        ev(4, "service_end", "b1", "p"), ev(4, "queue_enter", "b1", "q"), ev(4, "service_end", "b2", "p"), ev(4, "rework", "b2", "p2"),
      ]),
      nodes,
      10,
    );
    // rework is a pass-through: b2 stays where its last located event put it.
    expect([tl.at(1), tl.at(2), tl.at(4), tl.at(6)]).toEqual([{ q2: 1 }, { q2: 1, p: 2 }, { q2: 1, p: 1, q: 1 }, { p: 1, q: 1 }]);
  });

  it("draw nothing for a trace with no events", () => {
    expect(timeline(trace([]), nodes, 10).at(5)).toEqual({});
  });
});
