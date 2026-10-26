#!/usr/bin/env python3
"""Assembles testdata/simulation/setup-goldens.json (T-050, ADR-026 D-055).

This script only builds JSON. Every expected value below was derived by hand
from the case's derivation text (D-047, D-048, D-049, D-051, D-052, D-055,
D-074 and the T-050 interpretations D-097, PROPOSED), before the kernel ran
the case; no value is computed by a simulation. The case IDs are G-SU<n>:
G-SU1-G-SU6 are ADR-026 D-055's G-S1-G-S6 (G-SU4, the neutral setting, and
G-SU5, the per-unit streams, are checked by tests that compute their
expectation from the model itself: the same run without setups, and the
setup/<u> streams); G-SU7-G-SU10 add unit identity under breakdowns, the
measurement window, a calendar and blocking.

    python3 checks/evidence/2026-10-26-T-050/setup_goldens_gen.py > testdata/simulation/setup-goldens.json
"""
import json
import sys

MIN = 60_000  # ticks per minute
A, B, C = "board_a", "board_b", "board_c"


def const(v):
    return {"type": "constant", "value": v}


def res(rid, capacity=1, **extra):
    r = {"id": rid, "type": "machine", "name": rid, "capacity": capacity, "queue_discipline": "fifo"}
    r.update(extra)
    return r


def setups(initial=None, default=None, transitions=()):
    s = {}
    if initial is not None:
        s["initial"] = initial
    if default is not None:
        s["default"] = default
    if transitions:
        s["transitions"] = [{"from": f, "to": t, "distribution": d} for f, t, d in transitions]
    return s


def source(sid, every, et, max_entities=None):
    a = {"distribution": every if isinstance(every, dict) else const(every)}
    if max_entities is not None:
        a["max_entities"] = max_entities
    return {"id": sid, "type": "source", "entity_type": et, "arrival": a}


def table_source(sid, rows, et, period=100):
    return {"id": sid, "type": "source", "entity_type": et,
            "arrival": {"schedule": {"process": "table", "period": period,
                                     "releases": [{"at": at, "quantity": q} for at, q in rows]}}}


def process(pid, service, requires=()):
    return {"id": pid, "type": "process", "service_time": {"distribution": service if isinstance(service, dict) else const(service)},
            "requires": [{"resource_id": r, "units": u} for r, u in requires]}


def queue(qid, capacity=None):
    q = {"id": qid, "type": "queue"}
    if capacity:
        q["capacity"] = capacity
    return q


def sink(sid):
    return {"id": sid, "type": "sink", "outcome": "completed"}


def flow(a, b, p=1):
    return {"id": f"{a}__{b}", "from": a, "to": b, "routing": {"type": "probability", "probability": p}}


def model(name, resources, nodes, flows, entity_types=(A, B), calendars=None):
    m = {"schema_version": "0.1", "model_id": "mdl_01M4YDQK00R942EDGA45T538HS", "domain": "synthetic_line",
         "name": name, "time_unit": "minute",
         "entity_types": [{"id": e, "name": e} for e in entity_types],
         "resources": resources, "nodes": nodes, "flows": flows, "constraints": [], "metrics": [],
         "views": {"2d": {}, "3d": {}}, "metadata": {"synthetic": True}}
    if calendars:
        m["calendars"] = calendars
    return m


def config(horizon, warmup=0, replications=1, seed=7, trace="none"):
    return {"horizon": {"value": horizon, "unit": "minute"}, "warmup": {"value": warmup, "unit": "minute"},
            "replications": replications, "seed": seed, "trace_level": trace}


def e(source_id, n):
    return f"r0/{source_id}/{n}"


def ticks(minutes):
    return int(round(minutes * MIN))


def setup_state(in_use, setup, started, done, done_ticks, processing, **more):
    st = {"in_use_ticks": ticks(in_use), "setup_ticks": ticks(setup), "setups_started": started, "setups_completed": done,
          "completed_setup_ticks": ticks(done_ticks), "scheduled_processing_ticks": ticks(processing)}
    st.update(more)
    return st


cases = []
both = [flow("src_a", "place"), flow("src_b", "place"), flow("place", "done")]

# ---------------------------------------------------------------- G-SU1
su1_model = model("G-SU1", [res("smt", setups=setups(transitions=[(A, B, const(3)), (B, A, const(3))]))],
                  [source("src_a", 10, A), source("src_b", 20, B), process("place", 2, [("smt", 1)]), sink("done")], both)
cases.append({
    "id": "G-SU1",
    "title": "one unit, two products, changeovers both ways (ADR-026 D-055 G-S1): the first piece needs none, FIFO interleaving does",
    "model": su1_model,
    "config": config(50),
    "derivation": [
        "smt: 1 unit with setups: board_a -> board_b and board_b -> board_a 3 minutes (constant), no initial, no default. src_a makes board_a every 10 minutes, src_b board_b every 20 (constant); place: service 2, needs one smt.",
        "t=0: the sources' first arrivals in source ID order: src_a at 10 (seq 1), src_b at 20 (seq 2). No calendar, no breakdowns.",
        "t=10: A1 (seq 1; next src_a at 20, seq 3) enters, DISPATCH (seq 4): smt's only unit has no setup state yet (no initial): it takes board_a without a changeover (no Setup observation); service 10-12 (SERVICE_COMPLETE seq 5).",
        "t=12: SERVICE_COMPLETE (seq 5): release, DISPATCH (seq 6), A1 completes at 12; the DISPATCH finds nothing.",
        "t=20: src_b's ARRIVAL has the smaller seq (2, scheduled at t=0) and goes first: B1 (next src_b at 40, seq 7) enters, DISPATCH (seq 8); then src_a's (seq 3): A2 (next src_a at 30, seq 9) enters behind B1. DISPATCH (seq 8): B1 is first in FIFO; the unit is set up for board_a: changeover board_a -> board_b, 3 minutes: ObsResource, Setup (B1, unit 0, 3), ServiceStart B1 at 20; one SERVICE_COMPLETE at 20 + 3 + 2 = 25 (seq 10). A2 is held.",
        "t=25: SERVICE_COMPLETE (seq 10): release, DISPATCH (seq 11), B1 completes. DISPATCH (seq 11): A2: the unit is set up for board_b: changeover 3: Setup (A2, unit 0, 3) at 25, service until 30 (seq 12).",
        "t=30: SERVICE_COMPLETE (seq 12, priority 0) before src_a's ARRIVAL (seq 9, priority 10): release, DISPATCH (seq 13), A2 completes at 30; A3 (next src_a at 40, seq 14) enters. DISPATCH (seq 13): A3: the unit is set up for board_a: no changeover; service 30-32 (seq 15).",
        "t=32: SERVICE_COMPLETE (seq 15): release, DISPATCH (seq 16), A3 completes; the DISPATCH finds nothing.",
        "t=40: src_b's ARRIVAL (seq 7) first: B2 (next src_b at 60 > 50: not scheduled) enters, DISPATCH (seq 17); src_a's (seq 14): A4 (next at 50, seq 18). DISPATCH (seq 17): B2 first (D-055 G-S1: B2 before A4): changeover 3 at 40, SERVICE_COMPLETE at 45 (seq 19); A4 held.",
        "t=45: SERVICE_COMPLETE (seq 19): release, DISPATCH (seq 20), B2 completes. DISPATCH (seq 20): A4: changeover board_b -> board_a 3 at 45, until 50 (seq 21).",
        "t=50: SERVICE_COMPLETE (seq 21) first: release, DISPATCH (seq 22), A4 completes at 50; then src_a's ARRIVAL (seq 18): A5 (next at 60 > 50: not scheduled) enters. DISPATCH (seq 22): A5: set up for board_a, no changeover; service 50-52 (after the horizon: never scheduled).",
        "22 events: 7 arrivals, 6 SERVICE_COMPLETEs, 9 DISPATCHes. Changeovers: B1 at 20, A2 at 25, B2 at 40, A4 at 45, 3 minutes each, all on unit 0.",
        "Result over [0,50]: 6 completed, A5 censored. Lead times (completion order) A1 2, B1 5, A2 10, A3 2, B2 5, A4 10. Queue waits at place (service-start order, up to the grant, D-055): A1 0, B1 0, A2 5, A3 0, B2 0, A4 5, A5 0.",
        "smt in use [10,12), [20,25), [25,30), [30,32), [40,45), [45,50): 24 of 50 minutes, utilization 0.48. resource_states[smt] (setups apply; no finite buffer: no blocked_ticks): in use 24 min, changing over [20,23), [25,28), [40,43), [45,48) = 12 min, 4 changeovers started, 4 complete of 12 min, scheduled processing (no calendar: U - Z) 12 min.",
    ],
    "expected": {
        "starts": {"place": [[e("src_a", 1), 10], [e("src_b", 1), 20], [e("src_a", 2), 25], [e("src_a", 3), 30], [e("src_b", 2), 40],
                             [e("src_a", 4), 45], [e("src_a", 5), 50]]},
        "completions": [[e("src_a", 1), 12], [e("src_b", 1), 25], [e("src_a", 2), 30], [e("src_a", 3), 32], [e("src_b", 2), 45],
                        [e("src_a", 4), 50]],
        "setups": [[e("src_b", 1), "place", "smt", 0, 20, 3], [e("src_a", 2), "place", "smt", 0, 25, 3],
                   [e("src_b", 2), "place", "smt", 0, 40, 3], [e("src_a", 4), "place", "smt", 0, 45, 3]],
        "setup_seqs": [8, 11, 17, 20],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 22,
        "result": {"completed_count": 6, "censored_entities": 1, "lead_times_minutes": [2, 5, 10, 2, 5, 10],
                   "queue_waits_minutes": {"place": [0, 0, 5, 0, 0, 5, 0]}, "resource_utilization": {"smt": 0.48},
                   "resource_states": {"smt": setup_state(24, 12, 4, 4, 12, 12)}, "blocking": None, "buffers": None, "batches": None},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-SU2
cases.append({
    "id": "G-SU2",
    "title": "two units set up for different products (ADR-026 D-055 G-S2): a board_b piece takes unit 1, set up for it, not unit 0; the default changeover",
    "model": model("G-SU2", [res("smt", 2, setups=setups(default=const(4)))],
                   [table_source("src_a", [(10, 1), (20, 1), (30, 2)], A), source("src_b", 12, B, 2), process("place", 4, [("smt", 1)]),
                    sink("done")], both),
    "config": config(40),
    "derivation": [
        "smt: 2 units, setups with only a default (4 minutes, constant), no initial. src_a is a release table (period 100: 1 at 10, 1 at 20, 2 at 30), src_b makes board_b every 12 minutes, 2 in all; place: service 4.",
        "t=0: src_a's first row at 10 (seq 1), src_b's first arrival at 12 (seq 2).",
        "t=10: ARRIVAL (seq 1): the next row at 20 (seq 3) first, then A1 enters, DISPATCH (seq 4): of the units neither in use nor down (0, 1), none is set up for board_a: the one of least index, unit 0, which has no state yet: board_a, no changeover; service 10-14 (seq 5).",
        "t=12: B1 (seq 2; next at 24, seq 6), DISPATCH (seq 7): only unit 1 is free, no state: board_b, no changeover; 12-16 (seq 8).",
        "t=14: SERVICE_COMPLETE (seq 5): release, DISPATCH (seq 9), A1 completes; nothing to grant. t=16: SERVICE_COMPLETE (seq 8): release, DISPATCH (seq 10), B1 completes; nothing.",
        "t=20: ARRIVAL (seq 3): the next row at 30 (seq 11), A2 enters, DISPATCH (seq 12): unit 0 (board_a) and unit 1 (board_b) are free; unit 0 matches: no changeover; 20-24 (seq 13).",
        "t=24: SERVICE_COMPLETE (seq 13, priority 0): release, DISPATCH (seq 14), A2 completes; then B2 (seq 6, the second and last of src_b) enters. DISPATCH (seq 14): both units free; unit 1 is set up for board_b: B2 takes unit 1, not unit 0 of the smaller index (D-055 G-S2), no changeover; 24-28 (seq 15).",
        "t=28: SERVICE_COMPLETE (seq 15): release, DISPATCH (seq 16), B2 completes; nothing.",
        "t=30: ARRIVAL (seq 11), the row of 2: the next row would be in the next period (110 > 40): not scheduled; A3 then A4 enter, DISPATCH (seq 17): A3 takes unit 0 (board_a matches), 30-34 (seq 18); A4: only unit 1 (board_b) is free: changeover board_b -> board_a, no transition, the default 4 minutes: Setup (A4, unit 1, 4) at 30, SERVICE_COMPLETE at 30 + 4 + 4 = 38 (seq 19).",
        "t=34: SERVICE_COMPLETE (seq 18): release, DISPATCH (seq 20), A3 completes. t=38: SERVICE_COMPLETE (seq 19): release, DISPATCH (seq 21), A4 completes.",
        "21 events: 5 arrival events (6 entities), 6 SERVICE_COMPLETEs, 10 DISPATCHes. One changeover: A4 at 30 on unit 1, 4 minutes.",
        "Result over [0,40] (C = 2): 6 completed; lead times A1 4, B1 4, A2 4, B2 4, A3 4, A4 8; queue waits all 0. In use: 4 + 4 + 4 + 4 + 4 + 8 = 28 unit-minutes, utilization 28 / 80 = 0.35. resource_states[smt]: in use 28 min, changing over 4 min (1 started, 1 complete of 4 min), scheduled processing 24 min.",
    ],
    "expected": {
        "starts": {"place": [[e("src_a", 1), 10], [e("src_b", 1), 12], [e("src_a", 2), 20], [e("src_b", 2), 24], [e("src_a", 3), 30],
                             [e("src_a", 4), 30]]},
        "completions": [[e("src_a", 1), 14], [e("src_b", 1), 16], [e("src_a", 2), 24], [e("src_b", 2), 28], [e("src_a", 3), 34],
                        [e("src_a", 4), 38]],
        "setups": [[e("src_a", 4), "place", "smt", 1, 30, 4]],
        "setup_seqs": [17],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 21,
        "result": {"completed_count": 6, "censored_entities": 0, "lead_times_minutes": [4, 4, 4, 4, 4, 8],
                   "queue_waits_minutes": {"place": [0, 0, 0, 0, 0, 0]}, "resource_utilization": {"smt": 0.35},
                   "resource_states": {"smt": setup_state(28, 4, 1, 1, 4, 24)}, "blocking": None, "buffers": None, "batches": None},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-SU3
line3 = [flow("src_a", "p1"), flow("src_b", "p1"), flow("src_c", "p1"), flow("p1", "p2"), flow("p2", "done")]
cases.append({
    "id": "G-SU3",
    "title": "a transition before the default, the default, 0 without either, with and without initial (ADR-026 D-055 G-S3); a 0-minute changeover is one (D-097 (a))",
    "model": model("G-SU3", [res("m1", setups=setups(initial=A, default=const(5), transitions=[(A, B, const(2))])),
                             res("m2", setups=setups(transitions=[(A, B, const(3))]))],
                   [source("src_a", 55, A, 1), source("src_b", 35, B, 2), source("src_c", 45, C, 1),
                    process("p1", 1, [("m1", 1)]), process("p2", 1, [("m2", 1)]), sink("done")], line3, entity_types=(A, B, C)),
    "config": config(80),
    "derivation": [
        "m1: initial board_a, default 5, board_a -> board_b 2. m2: no initial, no default, board_a -> board_b 3. src_b: board_b at 35 and 70; src_c: board_c at 45; src_a: board_a at 55. Every piece goes p1 (m1, service 1) then p2 (m2, service 1).",
        "t=0: first arrivals in source ID order: src_a at 55 (seq 1), src_b at 35 (seq 2), src_c at 45 (seq 3).",
        "t=35: B1 (seq 2; next src_b at 70, seq 4), DISPATCH (seq 5): m1's unit starts in board_a (initial): board_a -> board_b has a transition, 2 (not the default 5): Setup (B1, p1, m1, 0, 2); SERVICE_COMPLETE at 35 + 2 + 1 = 38 (seq 6).",
        "t=38: SERVICE_COMPLETE (seq 6): release m1, DISPATCH (seq 7), B1 enters p2. DISPATCH (seq 7): m2's unit has no state (no initial): board_b, no changeover; 38-39 (seq 8).",
        "t=39: SERVICE_COMPLETE (seq 8): release, DISPATCH (seq 9), B1 completes; nothing.",
        "t=45: C1 (seq 3, the only one), DISPATCH (seq 10): m1 board_b -> board_c: no transition, the default 5: Setup (C1, p1, m1, 0, 5); until 51 (seq 11).",
        "t=51: SERVICE_COMPLETE (seq 11): release, DISPATCH (seq 12), C1 enters p2; DISPATCH: m2 board_b -> board_c: no transition, no default: a changeover of 0 minutes, observed (D-097 (a)): Setup (C1, p2, m2, 0, 0); until 52 (seq 13). t=52: SERVICE_COMPLETE (seq 13): release, DISPATCH (seq 14), C1 completes.",
        "t=55: A1 (seq 1, the only one), DISPATCH (seq 15): m1 board_c -> board_a: the default 5: Setup (A1, p1, m1, 0, 5); until 61 (seq 16). t=61: SERVICE_COMPLETE: release, DISPATCH (seq 17), A1 enters p2; DISPATCH: m2 board_c -> board_a: 0: Setup (A1, p2, m2, 0, 0); until 62 (seq 18). t=62: SERVICE_COMPLETE: release, DISPATCH (seq 19), A1 completes.",
        "t=70: B2 (seq 4, the second and last), DISPATCH (seq 20): m1 board_a -> board_b: the transition, 2: Setup (B2, p1, m1, 0, 2); until 73 (seq 21). t=73: SERVICE_COMPLETE: release, DISPATCH (seq 22), B2 enters p2; DISPATCH: m2 board_a -> board_b: the transition, 3: Setup (B2, p2, m2, 0, 3); until 77 (seq 23). t=77: SERVICE_COMPLETE: release, DISPATCH (seq 24), B2 completes.",
        "24 events: 4 arrivals, 8 SERVICE_COMPLETEs, 12 DISPATCHes.",
        "Result over [0,80]: 4 completed; lead times B1 4, C1 7, A1 7, B2 7; queue waits all 0 at p1 and p2. m1 in use 3 + 6 + 6 + 3 = 18 min (0.225), changing over 2 + 5 + 5 + 2 = 14 min (4 started, 4 complete), processing 4 min. m2 in use 1 + 1 + 1 + 4 = 7 min (0.0875), changing over 0 + 0 + 3 = 3 min (3 started, the two 0-minute ones included; 3 complete), processing 4 min.",
    ],
    "expected": {
        "starts": {"p1": [[e("src_b", 1), 35], [e("src_c", 1), 45], [e("src_a", 1), 55], [e("src_b", 2), 70]],
                   "p2": [[e("src_b", 1), 38], [e("src_c", 1), 51], [e("src_a", 1), 61], [e("src_b", 2), 73]]},
        "completions": [[e("src_b", 1), 39], [e("src_c", 1), 52], [e("src_a", 1), 62], [e("src_b", 2), 77]],
        "setups": [[e("src_b", 1), "p1", "m1", 0, 35, 2], [e("src_c", 1), "p1", "m1", 0, 45, 5], [e("src_c", 1), "p2", "m2", 0, 51, 0],
                   [e("src_a", 1), "p1", "m1", 0, 55, 5], [e("src_a", 1), "p2", "m2", 0, 61, 0], [e("src_b", 2), "p1", "m1", 0, 70, 2],
                   [e("src_b", 2), "p2", "m2", 0, 73, 3]],
        "setup_seqs": [5, 10, 12, 15, 17, 20, 22],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 24,
        "result": {"completed_count": 4, "censored_entities": 0, "lead_times_minutes": [4, 7, 7, 7],
                   "queue_waits_minutes": {"p1": [0, 0, 0, 0], "p2": [0, 0, 0, 0]}, "resource_utilization": {"m1": 0.225, "m2": 0.0875},
                   "resource_states": {"m1": setup_state(18, 14, 4, 4, 14, 4), "m2": setup_state(7, 3, 3, 3, 3, 4)},
                   "blocking": None, "buffers": None, "batches": None},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-SU5
cases.append({
    "id": "G-SU5",
    "title": "each unit draws its changeovers from (resource ID, setup/<u>) (ADR-026 D-055 G-S5): one more unit of capacity does not shift a unit's draws",
    "model": model("G-SU5", [res("smt", 2, setups=setups(default={"type": "exponential", "mean": 3}))],
                   [source("src_a", {"type": "exponential", "mean": 2}, A), source("src_b", {"type": "exponential", "mean": 3}, B),
                    process("place", 3, [("smt", 1)]), sink("done")], both),
    "config": config(300, seed=4242),
    "derivation": [
        "smt: 2 units, an exponential default changeover (mean 3); board_a and board_b with exponential interarrivals (means 2 and 3 minutes), service 3, over 300 minutes: the units are overloaded, so a free unit takes the next piece in FIFO whatever its type, and the products interleave on every unit (the case is run with capacity 3 too).",
        "Not hand-derived: the test takes, for capacity 2 and again for capacity 3, every Setup observation of unit u in order and checks that the k-th has the k-th draw of the stream (seed's replication 0, smt, setup/<u>) through the default's sampler (D-055: draws from (resource ID, setup/<u>)), so a unit's sequence does not depend on the capacity or on the other units.",
    ],
    "expected": {"setup_streams": {"resource": "smt", "capacities": [2, 3], "min_changeovers_per_unit": 20}},
    "simpy": "not compared: a stream identity test",
})

# ---------------------------------------------------------------- G-SU6
su6_res = res("smt", setups=setups(transitions=[(A, B, const(4)), (B, A, const(4))]),
              breakdowns={"time_to_failure": {"distribution": const(20)}, "time_to_repair": {"distribution": const(3)}})
cases.append({
    "id": "G-SU6",
    "title": "a failure that fires during a changeover waits (ADR-026 D-055 G-S6): the unit goes down only after its changeover and service (finish-first)",
    "model": model("G-SU6", [su6_res], [source("src_a", 12, A, 2), source("src_b", 18, B, 1), process("place", 2, [("smt", 1)]), sink("done")],
                   both),
    "config": config(40),
    "derivation": [
        "smt: 1 unit; changeovers board_a <-> board_b 4 minutes; breakdowns TTF 20, TTR 3 (constant, calendar time). src_a: board_a at 12 and 24; src_b: board_b at 18; place: service 2.",
        "t=0: src_a at 12 (seq 1), src_b at 18 (seq 2), then the failure clock: FAILURE of clock 0 at 20 (seq 3).",
        "t=12: A1 (seq 1; next at 24, seq 4), DISPATCH (seq 5): the unit has no state: board_a, no changeover; 12-14 (seq 6). t=14: SERVICE_COMPLETE (seq 6): release, DISPATCH (seq 7), A1 completes.",
        "t=18: B1 (seq 2, the only one), DISPATCH (seq 8): board_a -> board_b 4: Setup (B1, unit 0, 4) at 18: changing over [18,22), service [22,24): SERVICE_COMPLETE at 24 (seq 9).",
        "t=20: FAILURE (seq 3): no unit is idle (the only one is changing over, in use): the failure waits (ObsBreakdownPending, 1 waiting; D-051 finish-first, D-074 (a)).",
        "t=24: SERVICE_COMPLETE (seq 9): release (ObsResource 0); the released unit goes down for the waiting failure (ObsResourceDown, clock 0, 1 down; REPAIR at 27, seq 10); DISPATCH (seq 11); B1 completes. Then A2 (seq 4, the last of src_a) enters. DISPATCH (seq 11): 0 + 1 > min(S, C - D) = 0: held.",
        "t=27: REPAIR (seq 10): up (0 down); the next failure would be at 47 > 40: not scheduled; the held front A2 wakes, DISPATCH (seq 12): A2: the unit is still set up for board_b (a downtime keeps the setup state, D-055): changeover board_b -> board_a 4: Setup (A2, unit 0, 4) at 27; until 33 (seq 13).",
        "t=33: SERVICE_COMPLETE (seq 13): release, DISPATCH (seq 14), A2 completes.",
        "14 events: 3 arrivals, 1 FAILURE, 1 REPAIR, 3 SERVICE_COMPLETEs, 6 DISPATCHes.",
        "Result over [0,40]: 3 completed; lead times A1 2, B1 6, A2 9; queue waits 0, 0, 3. smt in use [12,14), [18,24), [27,33) = 14 min (0.35); changing over [18,22), [27,31) = 8 min (2 started, 2 complete); down [24,27) = 3 min (1 started, 1 complete repair of 3 min); available C*T - down = 37 min; idle 37 - 14 = 23 min; scheduled processing U - Z = 6 min.",
    ],
    "expected": {
        "starts": {"place": [[e("src_a", 1), 12], [e("src_b", 1), 18], [e("src_a", 2), 27]]},
        "completions": [[e("src_a", 1), 14], [e("src_b", 1), 24], [e("src_a", 2), 33]],
        "setups": [[e("src_b", 1), "place", "smt", 0, 18, 4], [e("src_a", 2), "place", "smt", 0, 27, 4]],
        "setup_seqs": [8, 12],
        "blocks": [], "downs": [["smt", 0, 24, 27]], "pending": [["smt", 0, 20]], "capacities": [],
        "events": 14,
        "result": {"completed_count": 3, "censored_entities": 0, "lead_times_minutes": [2, 6, 9],
                   "queue_waits_minutes": {"place": [0, 0, 3]}, "resource_utilization": {"smt": 0.35},
                   "resource_states": {"smt": setup_state(14, 8, 2, 2, 8, 6, down_ticks=ticks(3), breakdowns_started=1, repairs_completed=1,
                                                          completed_repair_ticks=ticks(3), available_ticks=ticks(37), idle_ticks=ticks(23))},
                   "blocking": None, "buffers": None, "batches": None},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-SU7
su7_res = res("smt", 2, setups=setups(transitions=[(A, B, const(2)), (B, A, const(2))]),
              breakdowns={"time_to_failure": {"distribution": const(15)}, "time_to_repair": {"distribution": const(10)}})
cases.append({
    "id": "G-SU7",
    "title": "unit identity under breakdowns (D-055, D-097 (c)): a failure at once takes the idle unit of least index, a waiting one the released unit, a repair frees the clock's unit, and a downtime keeps the setup state",
    "model": model("G-SU7", [su7_res], [table_source("src_a", [(10, 1), (32, 1), (36, 1)], A), table_source("src_b", [(22, 1), (46, 1)], B),
                                        process("place", 10, [("smt", 1)]), sink("done")], both),
    "config": config(60),
    "derivation": [
        "smt: 2 units; changeovers board_a <-> board_b 2; breakdowns TTF 15, TTR 10 (constant): both clocks fail at 15. Release tables (period 100): board_a at 10, 32, 36; board_b at 22, 46. place: service 10. The clock fires on a definite unit (D-097 (c)): at once, the idle unit of least index; when waiting, the unit released (or repaired) that takes it over; ResourceDown/Up's At stays the clock.",
        "t=0: src_a's row at 10 (seq 1), src_b's at 22 (seq 2), FAILURE of clock 0 at 15 (seq 3), of clock 1 at 15 (seq 4).",
        "t=10: ARRIVAL (seq 1; next row at 32, seq 5): A1, DISPATCH (seq 6): unit 0 (no state): board_a, no changeover; 10-20 (seq 7).",
        "t=15: FAILURE clock 0 (seq 3): one unit idle: the idle unit of least index is unit 1 (unit 0 serves A1): unit 1 down for clock 0 (1 down; REPAIR at 25, seq 8). FAILURE clock 1 (seq 4): no unit idle: waits (1 waiting).",
        "t=20: SERVICE_COMPLETE (seq 7): release; the released unit 0 goes down for clock 1 (2 down; REPAIR at 30, seq 9); DISPATCH (seq 10); A1 completes; nothing to grant.",
        "t=22: ARRIVAL (seq 2; next row at 46, seq 11): B1, DISPATCH (seq 12): 0 + 1 > min(2, 2 - 2): held.",
        "t=25: REPAIR clock 0 (seq 8): its unit, unit 1, is up (1 down); clock 0's next FAILURE at 40 (seq 13); B1 wakes, DISPATCH (seq 14): only unit 1 is free (unit 0 is down), no state: board_b, no changeover; 25-35 (seq 15).",
        "t=30: REPAIR clock 1 (seq 9): unit 0 is up (0 down); next FAILURE at 45 (seq 16).",
        "t=32: ARRIVAL (seq 5; next row at 36, seq 17): A2, DISPATCH (seq 18): unit 0 is free and still set up for board_a (its downtime [20,30) kept the state): no changeover; 32-42 (seq 19).",
        "t=35: SERVICE_COMPLETE (seq 15): release, DISPATCH (seq 20), B1 completes; nothing.",
        "t=36: ARRIVAL (seq 17; the next row is in the next period: not scheduled): A3, DISPATCH (seq 21): only unit 1 (board_b) is free: changeover board_b -> board_a 2: Setup (A3, unit 1, 2) at 36; until 36 + 2 + 10 = 48 (seq 22).",
        "t=40: FAILURE clock 0 (seq 13): both units serve: waits (1 waiting). t=42: SERVICE_COMPLETE (seq 19): release; unit 0 goes down for clock 0 (1 down; REPAIR at 52, seq 23); DISPATCH (seq 24); A2 completes.",
        "t=45: FAILURE clock 1 (seq 16): unit 1 serves A3, unit 0 is down: no idle unit: waits. t=46: ARRIVAL (seq 11; no next row): B2, DISPATCH (seq 25): 1 + 1 > min(2, 2 - 1): held.",
        "t=48: SERVICE_COMPLETE (seq 22): release; unit 1 goes down for clock 1 (2 down; REPAIR at 58, seq 26); B2 is woken by the release, DISPATCH (seq 27): 0 + 1 > 0: held again; A3 completes.",
        "t=52: REPAIR clock 0 (seq 23): unit 0 up (1 down); next FAILURE at 67 > 60: not scheduled; B2 wakes, DISPATCH (seq 28): unit 0 (board_a) is the only free unit: changeover board_a -> board_b 2: Setup (B2, unit 0, 2) at 52; its SERVICE_COMPLETE at 64 > 60 is never scheduled.",
        "t=58: REPAIR clock 1 (seq 26): unit 1 up (0 down); next FAILURE at 73: not scheduled.",
        "28 events: 5 arrival events, 4 FAILUREs, 4 REPAIRs, 4 SERVICE_COMPLETEs, 11 DISPATCHes.",
        "Result over [0,60] (C = 2, T = 60): 4 completed, B2 censored; lead times A1 10, B1 13, A2 10, A3 12; queue waits A1 0, B1 3, A2 0, A3 0, B2 6. In use 10 + 10 + 10 + 12 + 8 = 50 unit-minutes (50/120); down 4 x 10 = 40 min (4 started, 4 complete repairs of 40 min); available 120 - 40 = 80; idle 80 - 50 = 30; changing over [36,38), [52,54) = 4 min (2 started, 2 complete); scheduled processing 50 - 4 = 46 min.",
    ],
    "expected": {
        "starts": {"place": [[e("src_a", 1), 10], [e("src_b", 1), 25], [e("src_a", 2), 32], [e("src_a", 3), 36], [e("src_b", 2), 52]]},
        "completions": [[e("src_a", 1), 20], [e("src_b", 1), 35], [e("src_a", 2), 42], [e("src_a", 3), 48]],
        "setups": [[e("src_a", 3), "place", "smt", 1, 36, 2], [e("src_b", 2), "place", "smt", 0, 52, 2]],
        "setup_seqs": [21, 28],
        "blocks": [],
        "downs": [["smt", 0, 15, 25], ["smt", 1, 20, 30], ["smt", 0, 42, 52], ["smt", 1, 48, 58]],
        "pending": [["smt", 1, 15], ["smt", 0, 40], ["smt", 1, 45]],
        "capacities": [],
        "events": 28,
        "result": {"completed_count": 4, "censored_entities": 1, "lead_times_minutes": [10, 13, 10, 12],
                   "queue_waits_minutes": {"place": [0, 3, 0, 0, 6]}, "resource_utilization": {"smt": 50 / 120},
                   "resource_states": {"smt": setup_state(50, 4, 2, 2, 4, 46, down_ticks=ticks(40), breakdowns_started=4, repairs_completed=4,
                                                          completed_repair_ticks=ticks(40), available_ticks=ticks(80), idle_ticks=ticks(30))},
                   "blocking": None, "buffers": None, "batches": None},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-SU8
cases.append({
    "id": "G-SU8",
    "title": "the measurement window [22, 41] (D-058, D-097 (f)): a changeover started before W counts in setup_ticks only from W, one running at H counts as started but not complete",
    "model": model("G-SU8", [res("smt", setups=setups(transitions=[(A, B, const(3)), (B, A, const(3))]))],
                   [source("src_a", 10, A), source("src_b", 20, B), process("place", 2, [("smt", 1)]), sink("done")], both),
    "config": config(41, warmup=22),
    "derivation": [
        "G-SU1's model with warmup 22 and horizon 41. Up to t=32 the run is G-SU1's (every event it schedules falls by 40 <= 41), the same sequence numbers: B1 changes over [20,23) and serves [23,25); A2 changes over [25,28) and serves [28,30); A3 serves [30,32) without one.",
        "t=40: src_b's ARRIVAL (seq 7): B2 (next at 60 > 41: not scheduled), DISPATCH (seq 17); src_a's (seq 14): A4 (next at 50 > 41: not scheduled). DISPATCH (seq 17): B2 changes over board_a -> board_b at 40 (3 minutes, until 43) and would end at 45: never scheduled; A4 is held.",
        "17 events: 6 arrivals, 4 SERVICE_COMPLETEs, 7 DISPATCHes. The window [22,41], T = 19 minutes.",
        "In use in the window: B1 [22,25) 3, A2 [25,30) 5, A3 [30,32) 2, B2 [40,41] 1: 11 min, utilization 11/19. Changing over: B1 [22,23) 1 (started at 20 < W: not counted as started), A2 [25,28) 3 (started and complete), B2 [40,41] 1 (started at 40, ends at 43 > H: not complete): setup_ticks 5 min, setups_started 2, setups_completed 1 of 3 min; scheduled processing 11 - 5 = 6 min.",
        "Completions in the window: B1 at 25, A2 at 30, A3 at 32: 3. The lead-time cohort (arrived at or after W, completed by H): A3 only, 2 minutes (B1 and A2 arrived at 20). Queue waits (joined at or after W, started by H): A3 0, B2 0. Censored: B2 (in service) and A4 (waiting): 2.",
    ],
    "expected": {
        "starts": {"place": [[e("src_a", 1), 10], [e("src_b", 1), 20], [e("src_a", 2), 25], [e("src_a", 3), 30], [e("src_b", 2), 40]]},
        "completions": [[e("src_a", 1), 12], [e("src_b", 1), 25], [e("src_a", 2), 30], [e("src_a", 3), 32]],
        "setups": [[e("src_b", 1), "place", "smt", 0, 20, 3], [e("src_a", 2), "place", "smt", 0, 25, 3], [e("src_b", 2), "place", "smt", 0, 40, 3]],
        "setup_seqs": [8, 11, 17],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 17,
        "result": {"completed_count": 3, "censored_entities": 2, "lead_times_minutes": [2],
                   "queue_waits_minutes": {"place": [0, 0]}, "resource_utilization": {"smt": 11 / 19},
                   "resource_states": {"smt": setup_state(11, 5, 2, 1, 3, 6)}, "blocking": None, "buffers": None, "batches": None},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-SU9
su9_res = res("smt", 2, calendar_id="c", shift_capacity={"night": 1},
              setups=setups(initial=A, transitions=[(A, B, const(4)), (B, A, const(4))]))
cases.append({
    "id": "G-SU9",
    "title": "a calendar (D-052, D-058): scheduled processing is ∫min(P, S) with P = U − K − Z, Z changing over (ELECTRONICS_LINE §5); a changeover's end has no observation",
    "model": model("G-SU9", [su9_res], [table_source("src_a", [(30, 1)], A), table_source("src_b", [(14, 1), (16, 1)], B),
                                        process("place", 10, [("smt", 1)]), sink("done")], both,
                   calendars=[{"id": "c", "period": 40, "shifts": [{"start": 0, "end": 20, "label": "day"}, {"start": 20, "end": 40, "label": "night"}]}]),
    "config": config(60),
    "derivation": [
        "smt: 2 units on calendar c (period 40: day [0,20) with S = C = 2, night [20,40) with shift_capacity night = 1); initial board_a, changeovers board_a <-> board_b 4. board_b at 14 and 16, board_a at 30 (release tables, period 100); place: service 10.",
        "t=0: src_a's row at 30 (seq 1), src_b's at 14 (seq 2), then c's first change at 20 (seq 3).",
        "t=14: ARRIVAL (seq 2; next row at 16, seq 4): B1, DISPATCH (seq 5): both units are set up for board_a (initial): unit 0: changeover 4: Setup (B1, unit 0, 4) [14,18), service [18,28) (seq 6).",
        "t=16: ARRIVAL (seq 4; no next row in this period): B2, DISPATCH (seq 7): 1 + 1 <= min(S = 2, 2): unit 1: changeover 4: Setup (B2, unit 1, 4) [16,20), service [20,30) (seq 8).",
        "t=20: CAPACITY_CHANGE (seq 3): the next at 40 (seq 9); S 2 -> 1 (Capacity smt 1); a fall wakes nothing: both units stay in use (finish-first; U = 2 > S = 1 is overtime).",
        "t=28: SERVICE_COMPLETE (seq 6): release, DISPATCH (seq 10), B1 completes. t=30: SERVICE_COMPLETE (seq 8, priority 0): release, DISPATCH (seq 11), B2 completes; then ARRIVAL (seq 1): A1. DISPATCH (seq 11): 0 + 1 <= min(1, 2): no unit is set up for board_a: unit 0 (board_b) changes over 4: Setup (A1, unit 0, 4) [30,34), service [34,44) (seq 12).",
        "t=40: CAPACITY_CHANGE (seq 9): the next at 60 (seq 13); S 1 -> 2. t=44: SERVICE_COMPLETE (seq 12): release, DISPATCH (seq 14), A1 completes. t=60: CAPACITY_CHANGE (seq 13): S 2 -> 1; the next (80) is after the horizon.",
        "14 events: 3 arrival events, 3 CAPACITY_CHANGEs, 3 SERVICE_COMPLETEs, 5 DISPATCHes. The changeover ends at 18, 20 and 34 have no observation of their own.",
        "Integrals over [0,60] (T = 60, C = 2): U = 1 on [14,16), 2 on [16,28), 1 on [28,30), 1 on [30,44): 42 unit-min (0.35). S: 2 on [0,20), 1 on [20,40), 2 on [40,60): 100. Z: [14,18), [16,20), [30,34): 12. P = U - Z: 0 on [14,18), 1 on [18,20), 2 on [20,28), 1 on [28,30), 0 on [30,34), 1 on [34,44). min(P, S): 2 + 8 + 2 + 6 + 4 = 22 (P = 2 > S = 1 on [20,28)). min(U, S): 2 + 8 + 8 + 2 + 10 + 4 = 34; overtime max(U - S, 0) = 8 on [20,28); A = S (no breakdowns): 100; idle max(0, A - U): 28 + 2 + 4 + 32 = 66.",
        "Result: 3 completed; lead times 14, 14, 14; queue waits 0, 0, 0.",
    ],
    "expected": {
        "starts": {"place": [[e("src_b", 1), 14], [e("src_b", 2), 16], [e("src_a", 1), 30]]},
        "completions": [[e("src_b", 1), 28], [e("src_b", 2), 30], [e("src_a", 1), 44]],
        "setups": [[e("src_b", 1), "place", "smt", 0, 14, 4], [e("src_b", 2), "place", "smt", 1, 16, 4], [e("src_a", 1), "place", "smt", 0, 30, 4]],
        "setup_seqs": [5, 7, 11],
        "blocks": [], "downs": [], "pending": [], "capacities": [["smt", 20, 1], ["smt", 40, 2], ["smt", 60, 1]],
        "events": 14,
        "result": {"completed_count": 3, "censored_entities": 0, "lead_times_minutes": [14, 14, 14],
                   "queue_waits_minutes": {"place": [0, 0, 0]}, "resource_utilization": {"smt": 0.35},
                   "resource_states": {"smt": setup_state(42, 12, 3, 3, 12, 22, scheduled_ticks=ticks(100), scheduled_in_use_ticks=ticks(34),
                                                          overtime_ticks=ticks(8), available_ticks=ticks(100), idle_ticks=ticks(66))},
                   "blocking": None, "buffers": None, "batches": None},
    },
    "simpy": "not compared: the SimPy reference has no calendars",
})

# ---------------------------------------------------------------- G-SU10
su10_flows = [flow("src_a", "place"), flow("src_b", "place"), flow("place", "q_test"), flow("q_test", "test"), flow("test", "done")]
cases.append({
    "id": "G-SU10",
    "title": "blocking (D-049, D-055): an entity blocked after its service keeps its unit; the unit is freed when it moves on",
    "model": model("G-SU10", [res("smt", setups=setups(transitions=[(A, B, const(2)), (B, A, const(2))])), res("tester")],
                   [table_source("src_a", [(10, 1), (12, 1)], A), table_source("src_b", [(11, 1)], B), process("place", 3, [("smt", 1)]),
                    queue("q_test", 1), process("test", 10, [("tester", 1)]), sink("done")], su10_flows),
    "config": config(50),
    "derivation": [
        "smt: 1 unit, changeovers board_a <-> board_b 2; place: service 3 on smt, then q_test (capacity 1) in front of test (service 10 on tester). board_a at 10 and 12, board_b at 11 (release tables, period 100).",
        "t=0: src_a's row at 10 (seq 1), src_b's at 11 (seq 2). t=10: ARRIVAL (seq 1; next row at 12, seq 3): A1, DISPATCH (seq 4): unit 0, no state: board_a; 10-13 (seq 5).",
        "t=11: ARRIVAL (seq 2; no next row): B1, DISPATCH (seq 6): held. t=12: ARRIVAL (seq 3; no next row): A2 behind B1, DISPATCH (seq 7): nothing (B1 held, A2 not at the front).",
        "t=13: SERVICE_COMPLETE (seq 5): A1's route reaches q_test with 0 of 1 waiting: release smt, DISPATCH (seq 8), A1 enters test's buffer. DISPATCH (seq 8): B1 (request 2): board_a -> board_b 2: Setup (B1, unit 0, 2), 13-18 (seq 9); A2 (request 3) is now at smt's front: held; A1 at test (request 4): tester free: 13-23 (seq 10).",
        "t=18: SERVICE_COMPLETE (seq 9): B1's route: test's buffer holds 0 of 1: release smt, DISPATCH (seq 11), B1 enters (1 of 1: full). DISPATCH (seq 11): A2: board_b -> board_a 2: Setup (A2, unit 0, 2), 18-23 (seq 12); B1 at test: held.",
        "t=23: SERVICE_COMPLETE (seq 10, A1 at test) first: release tester, DISPATCH (seq 13), A1 completes. SERVICE_COMPLETE (seq 12, A2 at place): test's buffer is full (B1 waits): A2 is blocked at place and keeps smt's unit 0: no release (ServiceEnd, Blocked). DISPATCH (seq 13): B1 at test: 23-33 (seq 14); the grant frees a place: A2 moves in: place's bundle is released (ObsResource smt 0, unit 0 free), Unblocked, QueueEnter at test; held (tester in use).",
        "t=33: SERVICE_COMPLETE (seq 14): release, DISPATCH (seq 15), B1 completes. DISPATCH (seq 15): A2 at test: 33-43 (seq 16). t=43: SERVICE_COMPLETE (seq 16): release, DISPATCH (seq 17), A2 completes.",
        "17 events: 3 arrivals, 6 SERVICE_COMPLETEs, 8 DISPATCHes.",
        "Result over [0,50]: 3 completed; lead times 13, 22, 31; queue waits at place 0, 2, 6, at q_test 0, 5, 10. smt in use [10,13), [13,18), [18,23): 13 min (0.26); blocked: A2 [23,23], 0 min; changing over [13,15), [18,20): 4 min (2 started, 2 complete); scheduled processing U - K - Z = 9 min. tester in use 30 min (0.6), blocked 0, scheduled processing 30. blocking[place]: 1 started, 1 complete of 0 min, 0 entity-minutes. buffers[q_test]: full [18,33) = 15 min.",
    ],
    "expected": {
        "starts": {"place": [[e("src_a", 1), 10], [e("src_b", 1), 13], [e("src_a", 2), 18]],
                   "test": [[e("src_a", 1), 13], [e("src_b", 1), 23], [e("src_a", 2), 33]]},
        "completions": [[e("src_a", 1), 23], [e("src_b", 1), 33], [e("src_a", 2), 43]],
        "setups": [[e("src_b", 1), "place", "smt", 0, 13, 2], [e("src_a", 2), "place", "smt", 0, 18, 2]],
        "setup_seqs": [8, 11],
        "blocks": [[e("src_a", 2), "place", 23, 23]], "downs": [], "pending": [], "capacities": [],
        "events": 17,
        "result": {"completed_count": 3, "censored_entities": 0, "lead_times_minutes": [13, 22, 31],
                   "queue_waits_minutes": {"place": [0, 2, 6], "q_test": [0, 5, 10]}, "resource_utilization": {"smt": 0.26, "tester": 0.6},
                   "resource_states": {"smt": setup_state(13, 4, 2, 2, 4, 9, blocked_ticks=0),
                                       "tester": {"in_use_ticks": ticks(30), "blocked_ticks": 0, "scheduled_processing_ticks": ticks(30)}},
                   "blocking": {"place": {"blocks_started": 1, "blocks_completed": 1, "completed_block_ticks": 0, "blocked_entity_ticks": 0}},
                   "buffers": {"q_test": {"full_ticks": ticks(15)}}, "batches": None},
    },
    "simpy": "not compared: the SimPy reference has no finite buffers with setups",
})

doc = {
    "status": "HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN",
    "semantics_decisions": ["D-047", "D-048", "D-049", "D-051", "D-052", "D-055", "D-058", "D-074", "D-081", "D-097"],
    "units": "starts, completions, setups, blocks, downs, capacities and lead times in minutes; Result members in ticks (1 ms)",
    "notes": ("setups are [entity, process, resource, unit, start time, changeover minutes] of every Setup observation (D-055, in "
              "observation order); setup_seqs the sequence number of the DISPATCH each was made in; starts are service starts (a changeover is part of the grant: the "
              "service starts with it); blocks [entity, node, from, to]; downs, pending and capacities as in calendar-goldens.json; events "
              "the replication's event count. Each case's derivation is the hand derivation of its expected values (assembled by "
              "checks/evidence/2026-10-26-T-050/setup_goldens_gen.py, which only builds the JSON). G-SU1-G-SU6 are ADR-026 D-055's "
              "G-S1-G-S6; G-S4 (neutral setups) is the D-047 differential's neutral variant (differential040_test.go, neutral_setups_test.go)."),
    "cases": cases,
}
json.dump(doc, sys.stdout, indent=1, ensure_ascii=False)
sys.stdout.write("\n")
