#!/usr/bin/env python3
"""Assembles testdata/simulation/calendar-goldens.json (T-049).

It only builds JSON: every expected value below is typed in by hand from
the derivation written next to it (each case's "derivation"), following
ADR-026 D-047, D-048, D-050, D-051, D-052, D-053 and the T-049
interpretations (D-081, D-082, PROPOSED). No engine, SimPy run or other
program produced any of them. Loops only spell out a pattern the
derivation states (G-D4C's 100 exception days).

    python checks/evidence/2026-10-25-T-049/calendar_goldens_gen.py
"""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / "testdata/simulation/calendar-goldens.json"
MIN = 60_000  # ticks per model minute


def const(v):
    return {"type": "constant", "value": v}


def resource(rid, capacity=1, calendar=None, shift_capacity=None, ttf=None, ttr=None, typ="machine"):
    r = {"id": rid, "type": typ, "name": rid, "capacity": capacity, "queue_discipline": "fifo"}
    if calendar:
        r["calendar_id"] = calendar
    if shift_capacity:
        r["shift_capacity"] = shift_capacity
    if ttf is not None:
        r["breakdowns"] = {"time_to_failure": {"distribution": ttf}, "time_to_repair": {"distribution": ttr}}
    return r


def calendar(cid, period, shifts, exceptions=None):
    c = {"id": cid, "period": period,
         "shifts": [dict(start=s[0], end=s[1], **({"label": s[2]} if len(s) > 2 else {})) for s in shifts]}
    if exceptions:
        c["exceptions"] = [{"start": a, "end": b, "open": o} for a, b, o in exceptions]
    return c


def source(nid, dist=None, max_entities=None, schedule=None):
    arrival = {"distribution": dist} if dist else {"schedule": schedule}
    if max_entities:
        arrival["max_entities"] = max_entities
    return {"id": nid, "type": "source", "entity_type": "board", "arrival": arrival}


def process(nid, dist, *requires):
    return {"id": nid, "type": "process", "service_time": {"distribution": dist},
            "requires": [{"resource_id": r, "units": u} for r, u in requires]}


def queue(nid, capacity=None):
    n = {"id": nid, "type": "queue"}
    if capacity:
        n["capacity"] = capacity
    return n


def sink(nid="done"):
    return {"id": nid, "type": "sink", "outcome": "completed"}


def flows(*pairs):
    return [{"id": f"{a}__{b}", "from": a, "to": b, "routing": {"type": "probability", "probability": 1}} for a, b in pairs]


def model(name, resources, nodes, edges, calendars=None, unit="minute"):
    m = {"schema_version": "0.1", "model_id": "mdl_01M4YDQK00R942EDGA45T538HS", "domain": "synthetic_line", "name": name,
         "time_unit": unit, "entity_types": [{"id": "board", "name": "Board"}], "resources": resources, "nodes": nodes,
         "flows": flows(*edges), "constraints": [], "metrics": [], "views": {"2d": {}, "3d": {}}, "metadata": {"synthetic": True}}
    if calendars:
        m["calendars"] = calendars
    return m


def config(horizon, warmup=0, replications=1, seed=7, unit="minute"):
    return {"horizon": {"value": horizon, "unit": unit}, "warmup": {"value": warmup, "unit": unit},
            "replications": replications, "seed": seed, "trace_level": "none"}


def state(in_use, processing, *, scheduled=None, scheduled_in_use=None, overtime=None, available=None, idle=None,
          blocked=None, down=None, started=None, repairs=None, repair_ticks=None):
    """A resource_states entry in ticks; arguments in minutes."""
    s = {"in_use_ticks": round(in_use * MIN)}
    if blocked is not None:
        s["blocked_ticks"] = round(blocked * MIN)
    if down is not None:
        s.update({"down_ticks": round(down * MIN), "breakdowns_started": started, "repairs_completed": repairs,
                  "completed_repair_ticks": round(repair_ticks * MIN)})
    if scheduled is not None:
        s.update({"scheduled_ticks": round(scheduled * MIN), "scheduled_in_use_ticks": round(scheduled_in_use * MIN),
                  "overtime_ticks": round(overtime * MIN)})
    if available is not None:
        s.update({"available_ticks": round(available * MIN), "idle_ticks": round(idle * MIN)})
    s["scheduled_processing_ticks"] = round(processing * MIN)
    return s


def ids(src, times):
    return [[f"r0/{src}/{k}", t] for k, t in enumerate(times, 1)]


cases = []

# ---------------------------------------------------------------- G-C1
gc1_model = model("G-C1", [resource("m", 1, "c")], [source("src", const(2)), process("work", const(3), ("m", 1)), sink()],
                  [("src", "work"), ("work", "done")], [calendar("c", 10, [(0, 6)])])
gc1_derivation = [
    "S(t) of m: 1 on [0,6), 0 on [6,10), 1 on [10,16), 0 on [16,20), 1 from 20 (period 10, shift [0,6)); one unit.",
    "t=0: src schedules its first arrival at 2 (seq 1); calendar c's first change of S is at 6 (1 -> 0): CAPACITY_CHANGE at 6 (seq 2, priority 15; D-048: sources, then calendars). S(0) = 1 is the program's initial value, not an observation (D-047).",
    "t=2: ARRIVAL e1 (seq 1): next arrival at 4 (seq 3); e1 queues; DISPATCH at 2 (seq 4) grants it (0+1 <= min(S=1, C-D=1)): service [2,5], completion seq 5.",
    "t=4: ARRIVAL e2 (seq 3): next at 6 (seq 6); queues; DISPATCH (seq 7): 1+1 > 1, held.",
    "t=5: SERVICE_COMPLETE e1 (seq 5): release wakes e2; DISPATCH (seq 8) grants e2: service [5,8] (completion seq 9).",
    "t=6: ARRIVAL e3 (seq 6, priority 10): next at 8 (seq 10); queues; DISPATCH asked (seq 11). CAPACITY_CHANGE (seq 2, priority 15): first the calendar's next change, at 10 (seq 12; D-081 (a)), then S 1 -> 0 (Capacity m 0); a decrease wakes nothing. e2 keeps its unit (finish-first, D-052 (a)): overtime [6,8). DISPATCH (seq 11): e3 held (1+1 > min(0, 1)).",
    "t=8: SERVICE_COMPLETE e2 (seq 9): release wakes e3; DISPATCH (seq 13). ARRIVAL e4 (seq 10): next at 10 (seq 14). DISPATCH (seq 13): 0+1 > min(S=0, 1): e3 held again.",
    "t=10: ARRIVAL e5 (seq 14): next at 12 (seq 15); DISPATCH (seq 16). CAPACITY_CHANGE (seq 12): next change at 16 (seq 17); S 0 -> 1 (Capacity m 1); the increase wakes the held front e3 (D-052; the shift start raises the capacity without a release). DISPATCH (seq 16) grants e3: [10,13] (seq 18); e4 is the next candidate, held.",
    "t=12: e6 arrives (seq 15; next at 14, seq 19); DISPATCH (seq 20) grants nothing. t=13: e3 completes, wakes e4; DISPATCH (seq 21) grants e4: [13,16] (seq 22); e5 held. t=14: e7 arrives (next at 16, seq 23); DISPATCH (seq 24).",
    "t=16: SERVICE_COMPLETE e4 (seq 22): release wakes e5; DISPATCH (seq 25). ARRIVAL e8 (seq 23): next at 18 (seq 26). CAPACITY_CHANGE (seq 17): next at 20 (seq 27); S 1 -> 0. DISPATCH (seq 25): e5 held.",
    "t=18: e9 arrives (next at 20, seq 28); DISPATCH (seq 29). t=20: ARRIVAL e10 (seq 28): the next, at 22, is after the horizon (not scheduled); DISPATCH (seq 30). CAPACITY_CHANGE (seq 27): its next, at 26, is after the horizon; S 0 -> 1 wakes e5; DISPATCH (seq 30) grants e5 at 20 (its completion at 23 is after the horizon).",
    "30 events: 2 at each of 2, 4, 5, 12, 13, 14, 18, 3 at each of 6, 8, 10, 20 and 4 at 16 (e4's completion, e8's arrival, the change, the dispatch); the last sequence number, the dispatch at 20, is 30. (Correction, before any commit: the first version of this line said 29, counting tick 16 as 3 events; the per-tick derivation above and its own sequence numbers give 30. The first engine run showed the mismatch; the derivation, not the engine, settles it.)",
]
gc1_expected = {
    "starts": {"work": ids("src", [2, 5, 10, 13, 20])},
    "completions": ids("src", [5, 8, 13, 16]),
    "blocks": [], "downs": [], "pending": [],
    "capacities": [["m", 6, 0], ["m", 10, 1], ["m", 16, 0], ["m", 20, 1]],
    "service_start_seqs": [["r0/src/1", 2, 4], ["r0/src/2", 5, 8], ["r0/src/3", 10, 16], ["r0/src/4", 13, 21], ["r0/src/5", 20, 30]],
    "events": 30,
}
cases.append({
    "id": "G-C1",
    "title": "a shift end with a service in progress (finish-first, overtime) and requests that wait for the next shift (ADR-026 D-052 G-C1)",
    "model": gc1_model, "config": config(20), "derivation": gc1_derivation + [
        "Result over [0,20] (T = 20 min): completed e1-e4 (4), censored e5 (in service) and e6-e10 (6); lead times 3, 4, 7, 8; waits 0, 1, 4, 5 and e5's 10 (it starts at 20 <= H).",
        "in use: [2,8) and [10,16) = 12 min, utilization 12/20 (D-052 (i): utilization_mean keeps C*T as its denominator).",
        "S on [0,6) and [10,16): scheduled 12 min; min(U,S): [2,6) and [10,16) = 10 min; overtime max(U-S,0): [6,8) = 2 min (10 + 2 = 12 in use); available min(S, C-D) = S = 12 min; idle max(0, A-U): [0,2) = 2 min (12 = 10 + 2); scheduled processing min(P,S) with P = U (no finite buffer) = 10 min.",
        "The first period's scheduled utilization is 4/6 (ADR-026's G-C1): min(U,S) over [0,10) is 4, S 6.",
    ],
    "expected": {**gc1_expected, "result": {
        "completed_count": 4, "censored_entities": 6, "lead_times_minutes": [3, 4, 7, 8],
        "queue_waits_minutes": {"work": [0, 1, 4, 5, 10]}, "resource_utilization": {"m": 0.6},
        "resource_states": {"m": state(12, 10, scheduled=12, scheduled_in_use=10, overtime=2, available=12, idle=2)},
        "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-C1W, G-C1W10
cases.append({
    "id": "G-C1W",
    "title": "G-C1 over [6, 20]: a shift end at W, a shift start at H",
    "model": gc1_model, "config": config(20, warmup=6), "derivation": gc1_derivation + [
        "The same run; the window is [6,20] (T = 14 min). S drops to 0 exactly at W = 6 and rises at H = 20: the integrals over [W,H] see S = 0 on [6,10), 1 on [10,16), 0 on [16,20) and a zero-length [20,20].",
        "in use over [6,20]: [6,8) (e2's overtime), [10,13), [13,16) = 8 min, utilization 8/14; scheduled 6 min; min(U,S) 6 min ([10,16)); overtime 2 min ([6,8)); available 6; idle 0; scheduled processing 6.",
        "Completions in [6,20]: e2 (8), e3 (13), e4 (16): 3. The lead-time cohort (arrived >= 6): e3 7, e4 8. Waits that queued at or after 6 and started by 20: e3 4, e4 5, e5 10. Censored 6.",
    ],
    "expected": {**gc1_expected, "result": {
        "completed_count": 3, "censored_entities": 6, "lead_times_minutes": [7, 8],
        "queue_waits_minutes": {"work": [4, 5, 10]}, "resource_utilization": {"m": 8 / 14},
        "resource_states": {"m": state(8, 6, scheduled=6, scheduled_in_use=6, overtime=2, available=6, idle=0)},
        "blocking": None, "buffers": None}},
    "simpy": "compare",
})
gc1w10_expected = {
    "starts": {"work": ids("src", [2, 5, 10, 13])},
    "completions": ids("src", [5, 8, 13, 16]),
    "blocks": [], "downs": [], "pending": [],
    "capacities": [["m", 6, 0], ["m", 10, 1], ["m", 16, 0]],
}
cases.append({
    "id": "G-C1W10",
    "title": "G-C1 over [10, 16]: a shift start at W, a shift end at H",
    "model": gc1_model, "config": config(16, warmup=10), "derivation": gc1_derivation[:9] + [
        "With the horizon at 16 the run stops after tick 16: there e4 completes (priority 0), e8 arrives (10; the next arrival, 18, is not scheduled), the shift ends (15; the next change, 20, is not scheduled), and the dispatch leaves e5 held.",
        "Window [10,16] (T = 6 min): S 1 on all of it (6 min); in use e3 [10,13) and e4 [13,16) = 6 min, utilization 1; min(U,S) 6; overtime 0; available 6; idle 0; scheduled processing 6.",
        "Completions in [10,16]: e3 (13) and e4 (16, at H: counted): 2. No completed entity arrived at or after 10: lead time unavailable. No visit queued at or after 10 and started by 16 (e5 waits): the queue has no wait sample. Censored: e5-e8 (4).",
    ],
    "expected": {**gc1w10_expected, "result": {
        "completed_count": 2, "censored_entities": 4, "lead_times_minutes": [],
        "queue_waits_minutes": {}, "resource_utilization": {"m": 1.0},
        "resource_states": {"m": state(6, 6, scheduled=6, scheduled_in_use=6, overtime=0, available=6, idle=0)},
        "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-C2
cases.append({
    "id": "G-C2",
    "title": "an arrival in the tick a shift starts is granted in it; one in the tick a shift ends waits (ADR-026 D-052 G-C2)",
    "model": model("G-C2", [resource("m", 1, "c")], [source("src", const(5)), process("work", const(2), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")], [calendar("c", 10, [(5, 10)])]),
    "config": config(20),
    "derivation": [
        "S(t) of m: 0 on [0,5), 1 on [5,10), 0 on [10,15), 1 on [15,20), 0 at 20 (period 10, shift [5,10)). S(0) = 0, not observed.",
        "t=0: arrival at 5 (seq 1); calendar c's first change at 5 (seq 2).",
        "t=5: ARRIVAL e1 (priority 10, seq 1): next at 10 (seq 3); e1 queues, DISPATCH asked (seq 4). CAPACITY_CHANGE (15, seq 2): next change at 10 (seq 5); S 0 -> 1; e1 is a candidate, not held. DISPATCH (20): e1 granted at 5 (D-048: the dispatch sees the tick's last capacity): [5,7].",
        "t=7: e1 completes; DISPATCH grants nothing.",
        "t=10: ARRIVAL e2 (seq 3): next at 15 (seq 8); queues (DISPATCH seq 9). CAPACITY_CHANGE (seq 5): next at 15 (seq 10); S 1 -> 0. DISPATCH: e2 held (0+1 > min(0,1)).",
        "t=15: ARRIVAL e3 (seq 8): next at 20 (seq 11); queues behind e2. CAPACITY_CHANGE (seq 10): next at 20 (seq 13); S 0 -> 1 wakes e2. DISPATCH: e2 [15,17]; e3 held. t=17: e2 completes; e3 [17,19]. t=19: e3 completes.",
        "t=20: ARRIVAL e4: the next, 25, is after the horizon; CAPACITY_CHANGE: its next, 25, too; S 1 -> 0; DISPATCH: e4 held.",
        "Result over [0,20]: S on [5,10) and [15,20): 10 min; in use 6 min, all on shift: min(U,S) 6, overtime 0; available 10, idle 4 ([7,10) and [19,20)); utilization 6/20. Completed 3, censored 1 (e4); lead times 2, 7, 4; waits 0, 5, 2 (e4 has not started).",
    ],
    "expected": {
        "starts": {"work": ids("src", [5, 15, 17])},
        "completions": ids("src", [7, 17, 19]),
        "blocks": [], "downs": [], "pending": [],
        "capacities": [["m", 5, 1], ["m", 10, 0], ["m", 15, 1], ["m", 20, 0]],
        "result": {
            "completed_count": 3, "censored_entities": 1, "lead_times_minutes": [2, 7, 4],
            "queue_waits_minutes": {"work": [0, 5, 2]}, "resource_utilization": {"m": 0.3},
            "resource_states": {"m": state(6, 6, scheduled=10, scheduled_in_use=6, overtime=0, available=10, idle=4)},
            "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-C4
cases.append({
    "id": "G-C4",
    "title": "a bundle of two resources with different, overlapping calendars is granted only when both are on shift (ADR-026 D-052 G-C4)",
    "model": model("G-C4", [resource("r1", 1, "a"), resource("r2", 1, "b")],
                   [source("src", const(4)), process("work", const(3), ("r1", 1), ("r2", 1)), sink()],
                   [("src", "work"), ("work", "done")], [calendar("a", 20, [(0, 12)]), calendar("b", 20, [(6, 20)])]),
    "config": config(30),
    "derivation": [
        "S of r1 (calendar a): 1 on [0,12), 0 on [12,20), 1 on [20,32); S of r2 (calendar b): 0 on [0,6), 1 on [6,20), 0 on [20,26), 1 on [26,40). Both on: [6,12) and [26,32).",
        "t=0: arrival at 4 (seq 1); the calendars in ID order: a's first change at 12 (seq 2), b's at 6 (seq 3).",
        "t=4: e1 arrives (next at 8, seq 4) and is held: r2 has 0+1 > min(S=0, 1).",
        "t=6: b's change (seq 3): next at 20 (seq 6); S of r2 0 -> 1 wakes e1 (the front of r2's FIFO); DISPATCH (seq 7) grants e1: [6,9].",
        "t=8: e2 arrives (next at 12, seq 9); held (r1 in use). t=9: e1 completes, releasing r1 and r2 and waking e2: [9,12].",
        "t=12: SERVICE_COMPLETE e2 (0) releases both; ARRIVAL e3 (10): next at 16 (seq 14); a's change (15, seq 2): next at 20 (seq 15); S of r1 1 -> 0; DISPATCH: e3 held (r1 off shift).",
        "t=16: e4 arrives (next at 20) and queues behind e3.",
        "t=20: ARRIVAL e5 (next at 24); two changes at priority 15 in sequence order: b's (seq 6, scheduled at 6) first: next at 26 (seq 20); S of r2 1 -> 0; then a's (seq 15): its next, 32, is after the horizon; S of r1 0 -> 1 wakes e3 (front of r1). DISPATCH: e3 held again (r2 off shift).",
        "t=24: e6 arrives (next at 28). t=26: b's change (seq 20): its next, 40, is after the horizon; S of r2 0 -> 1 wakes e3; DISPATCH grants e3: [26,29]; e4 held (in use).",
        "t=28: e7 arrives (the next, 32, is after the horizon). t=29: e3 completes, waking e4: [29,32], whose completion is after the horizon; e5 held.",
        "Result over [0,30]: r1: S on [0,12) and [20,30] = 22 min, in use [6,12) and [26,30] = 10 min, all on shift (min(U,S) 10, overtime 0), idle 12; r2: S on [6,20) and [26,30] = 18 min, in use 10 min on shift, idle 8; utilization 10/30 each.",
        "Completed e1-e3 (3), censored 4 (e4 in service, e5-e7 waiting); lead times 5, 4, 17; waits 2, 1, 14, 13.",
    ],
    "expected": {
        "starts": {"work": ids("src", [6, 9, 26, 29])},
        "completions": ids("src", [9, 12, 29]),
        "blocks": [], "downs": [], "pending": [],
        "capacities": [["r2", 6, 1], ["r1", 12, 0], ["r2", 20, 0], ["r1", 20, 1], ["r2", 26, 1]],
        "result": {
            "completed_count": 3, "censored_entities": 4, "lead_times_minutes": [5, 4, 17],
            "queue_waits_minutes": {"work": [2, 1, 14, 13]}, "resource_utilization": {"r1": 10 / 30, "r2": 10 / 30},
            "resource_states": {"r1": state(10, 10, scheduled=22, scheduled_in_use=10, overtime=0, available=22, idle=12),
                                "r2": state(10, 10, scheduled=18, scheduled_in_use=10, overtime=0, available=18, idle=8)},
            "blocking": None, "buffers": None}},
    "simpy": "refused: work's bundle has two resources with calendars; taken one resource at a time it would not be all or nothing",
})

# ---------------------------------------------------------------- G-C5
cases.append({
    "id": "G-C5",
    "title": "units on shift: day 2, night 1; at the change the second service in progress finishes (overtime) (ADR-026 D-052 G-C5)",
    "model": model("G-C5", [resource("m", 2, "c", {"day": 2, "night": 1})],
                   [source("src", const(2)), process("work", const(5), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")], [calendar("c", 20, [(0, 10, "day"), (10, 20, "night")])]),
    "config": config(24),
    "derivation": [
        "S of m (capacity 2): 2 on [0,10) (day), 1 on [10,20) (night), 2 from 20. Arrivals every 2 min (e_k at 2k), services of 5.",
        "t=0: arrival at 2; the calendar's first change at 10 (2 -> 1).",
        "e1 [2,7], e2 [4,9] (two units); e3 (6) waits for e1's release: [7,12]; e4 (8) for e2's: [9,14].",
        "t=10: ARRIVAL e5 (10) queues; CAPACITY_CHANGE (15): next at 20; S 2 -> 1 (two units stay in use: overtime); DISPATCH: e5 held (2+1 > min(1,2)).",
        "t=12: e3 completes (in use 1), waking e5; e6 arrives; DISPATCH: 1+1 > 1, e5 held. t=14: e4 completes (in use 0); e7 arrives; DISPATCH grants e5: [14,19]; e6 held.",
        "t=16, 18: e8, e9 arrive behind. t=19: e5 completes; e6 [19,24]; e7 held.",
        "t=20: ARRIVAL e10 queues; CAPACITY_CHANGE: its next, 30, is after the horizon; S 1 -> 2 wakes e7; DISPATCH grants e7 [20,25] (1+1 <= 2); e8 held.",
        "t=22: e11 arrives. t=24: e6 completes (0), waking e8; e12 arrives (10; the next, 26, is not scheduled); DISPATCH grants e8 [24,29]; e9 held.",
        "Result over [0,24] (T = 24 min, C*T = 48): in use 0 [0,2), 1 [2,4), 2 [4,12), 1 [12,20), 2 [20,24): 34 min, utilization 34/48.",
        "S: 2x10 + 1x10 + 2x4 = 38 min; min(U,S) = 14 (on [0,10)) + 10 ([10,20): 1 a tick) + 8 = 32; overtime: [10,12) at 2 - 1 = 2 min (32 + 2 = 34); available = S = 38; idle max(0,S-U): [0,2) 2x2 + [2,4) 1x2 = 6 (38 = 32 + 6); scheduled processing = 32.",
        "Completed e1-e6 (6; e6 at H = 24 counts), censored 6; lead times 5, 5, 6, 6, 9, 12; waits 0, 0, 1, 1, 4, 7 and e7 6, e8 8.",
    ],
    "expected": {
        "starts": {"work": ids("src", [2, 4, 7, 9, 14, 19, 20, 24])},
        "completions": ids("src", [7, 9, 12, 14, 19, 24]),
        "blocks": [], "downs": [], "pending": [],
        "capacities": [["m", 10, 1], ["m", 20, 2]],
        "result": {
            "completed_count": 6, "censored_entities": 6, "lead_times_minutes": [5, 5, 6, 6, 9, 12],
            "queue_waits_minutes": {"work": [0, 0, 1, 1, 4, 7, 6, 8]}, "resource_utilization": {"m": 34 / 48},
            "resource_states": {"m": state(34, 32, scheduled=38, scheduled_in_use=32, overtime=2, available=38, idle=6)},
            "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-C6
cases.append({
    "id": "G-C6",
    "title": "a closed exception: no grant during it, and the scheduled failure clock is postponed by its length (ADR-026 D-052 G-C6)",
    "model": model("G-C6", [resource("m", 1, "c", ttf=const(12), ttr=const(2))],
                   [source("src", const(4)), process("work", const(1), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")], [calendar("c", 10, [(0, 10)], [(10, 15, False)])]),
    "config": config(30),
    "derivation": [
        "S of m: the period [0,10) is always on; the exception closes [10,15): S = 1 except 0 on [10,15). Θ(t) (the on-shift ticks of [0,t)) is t up to 10, 10 on [10,15], t - 5 after.",
        "t=0: arrival at 4 (seq 1); the calendar's first change at 10 (seq 2); m's clock (TTF 12): the least t with Θ(t) >= 12 is 17 (without the exception it would be 12; D-051 (ii)): FAILURE at 17 (seq 3).",
        "e1 [4,5], e2 [8,9]. t=10: CAPACITY_CHANGE: next at 15; S 1 -> 0. t=12: e3 arrives (next at 16) and is held (S = 0): no grant during the exception.",
        "t=15: CAPACITY_CHANGE: no later change (the period is constant); S 0 -> 1 wakes e3: [15,16].",
        "t=16: e3 completes (0); e4 arrives (10; next at 20); DISPATCH grants e4: [16,17].",
        "t=17: e4 completes (0, releases the unit); FAILURE (15): idle = C - D - in use = 1, the unit goes down at once, TTR 2: REPAIR at 19.",
        "t=19: REPAIR: the next failure needs Θ >= Θ(19) + 12 = 14 + 12 = 26: at 31, after the horizon (not scheduled). No failure waits, no request is held.",
        "e5 [20,21], e6 [24,25], e7 [28,29] (the arrival after 28, 32, is not scheduled).",
        "Result over [0,30]: S 25 min ([10,15) closed); in use 7 min, all on shift; down [17,19) 2 min; available min(S, C-D) = 25 - 2 = 23; idle = 23 - 7 = 16; 1 breakdown, 1 complete repair of 2 min; utilization 7/30. Completed 7, censored 0; lead times 1, 1, 4, 1, 1, 1, 1; waits 0, 0, 3, 0, 0, 0, 0.",
    ],
    "expected": {
        "starts": {"work": ids("src", [4, 8, 15, 16, 20, 24, 28])},
        "completions": ids("src", [5, 9, 16, 17, 21, 25, 29]),
        "blocks": [], "downs": [["m", 0, 17, 19]], "pending": [],
        "capacities": [["m", 10, 0], ["m", 15, 1]],
        "result": {
            "completed_count": 7, "censored_entities": 0, "lead_times_minutes": [1, 1, 4, 1, 1, 1, 1],
            "queue_waits_minutes": {"work": [0, 0, 3, 0, 0, 0, 0]}, "resource_utilization": {"m": 7 / 30},
            "resource_states": {"m": state(7, 7, scheduled=25, scheduled_in_use=7, overtime=0, available=23, idle=16,
                                           down=2, started=1, repairs=1, repair_ticks=2)},
            "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-C7
gc7_caps = [["r_a", 5, 1], ["r_a", 8, 0], ["r_b", 8, 0], ["r_a", 10, 2], ["r_b", 10, 1], ["r_a", 15, 1],
            ["r_a", 18, 0], ["r_b", 18, 0], ["r_a", 20, 2], ["r_b", 20, 1]]
cases.append({
    "id": "G-C7",
    "title": "two resources on one calendar: one CAPACITY_CHANGE, Capacity in resource ID order, only for the resources whose S changes; a calendar whose S never changes schedules nothing (ADR-026 D-052 G-C7)",
    "model": model("G-C7", [resource("r_a", 2, "c", {"x": 2, "y": 1}), resource("r_b", 1, "c"), resource("r_c", 1, "d", {"p": 1, "q": 1})],
                   [source("src", const(100), 1), process("work", const(1), ("r_a", 1)), sink()],
                   [("src", "work"), ("work", "done")],
                   [calendar("c", 10, [(0, 5, "x"), (5, 8, "y")]), calendar("d", 10, [(0, 5, "p"), (5, 10, "q")])]),
    "config": config(20),
    "derivation": [
        "Calendar c (period 10): shift x [0,5), y [5,8), off [8,10). r_a (capacity 2, x 2, y 1): 2, 1, 0; r_b (capacity 1, both labels at the whole capacity): 1, 1, 0. Calendar d: p [0,5), q [5,10), r_c 1 on both: S of r_c is always 1.",
        "t=0: the first arrival, at 100, is after the horizon (never scheduled, no sequence number). Calendar c's first change is at 5 (seq 1: r_a changes there); calendar d never changes S of any resource: no event at all (D-048: only a calendar whose S changes schedules one; D-047's neutral calendar).",
        "t=5 (seq 1): next change at 8 (seq 2); r_a 2 -> 1 (Capacity); r_b stays 1: no observation.",
        "t=8 (seq 2): next at 10 (seq 3); r_a 1 -> 0 and r_b 1 -> 0, in resource ID order, one event (both observations carry seq 2).",
        "t=10 (seq 3): next at 15 (seq 4); r_a 0 -> 2, r_b 0 -> 1. The increases wake no held request (there is none) and schedule no dispatch.",
        "t=15 (seq 4): next at 18 (seq 5); r_a 2 -> 1. t=18 (seq 5): next at 20 (seq 6); r_a 1 -> 0, r_b 1 -> 0. t=20 (seq 6): its next, 25, is after the horizon; r_a 0 -> 2, r_b 0 -> 1. 6 events.",
        "Result over [0,20], no finite buffer: every resource with a calendar is listed (r_c too); nothing is in use. r_a: S 10 + 3 + 0 + 10 + 3 + 0 = 26 min; r_b: 8 + 8 = 16 min; r_c: 20 min; available = S, idle = S, the rest 0; utilization 0.",
    ],
    "expected": {
        "starts": {}, "completions": [], "blocks": [], "downs": [], "pending": [],
        "capacities": gc7_caps,
        "capacity_seqs": [1, 2, 2, 3, 3, 4, 5, 5, 6, 6],
        "events": 6,
        "result": {
            "completed_count": 0, "censored_entities": 0, "lead_times_minutes": [],
            "queue_waits_minutes": {}, "resource_utilization": {"r_a": 0, "r_b": 0, "r_c": 0},
            "resource_states": {"r_a": state(0, 0, scheduled=26, scheduled_in_use=0, overtime=0, available=26, idle=26),
                                "r_b": state(0, 0, scheduled=16, scheduled_in_use=0, overtime=0, available=16, idle=16),
                                "r_c": state(0, 0, scheduled=20, scheduled_in_use=0, overtime=0, available=20, idle=20)},
            "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-C8
cases.append({
    "id": "G-C8",
    "title": "an open exception extends a shift: no change where the periodic shift ends inside it, one at its end",
    "model": model("G-C8", [resource("m", 1, "c")], [source("src", const(3)), process("work", const(2), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")], [calendar("c", 10, [(0, 5)], [(5, 8, True)])]),
    "config": config(16),
    "derivation": [
        "S of m: the period is on [0,5) and off [5,10); the open exception [5,8) sets S = C = 1 there. So S = 1 on [0,8), 0 on [8,10), 1 on [10,15), 0 on [15,20). The periodic change at 5 is inside the exception: S does not change there (no event).",
        "t=0: arrival at 3; the calendar's first change at 8 (the exception's end).",
        "e1 [3,5], e2 [6,8] (granted at 6: S is 1 in the exception). t=8: e2 completes (0); CAPACITY_CHANGE (15): next at 10; S 1 -> 0.",
        "t=9: e3 arrives (next at 12), held. t=10: CAPACITY_CHANGE: next at 15; S 0 -> 1 wakes e3: [10,12]. t=12: e3 completes (0); e4 arrives (10; next at 15): [12,14].",
        "t=15: ARRIVAL e5 (10; the next, 18, is after the horizon); CAPACITY_CHANGE (15): its next, 20, is after the horizon; S 1 -> 0; DISPATCH: e5 held.",
        "Result over [0,16]: S 8 + 5 = 13 min; in use 8 min, all on shift; idle 5; utilization 8/16. Completed 4, censored 1; lead times 2, 2, 3, 2; waits 0, 0, 1, 0.",
    ],
    "expected": {
        "starts": {"work": ids("src", [3, 6, 10, 12])},
        "completions": ids("src", [5, 8, 12, 14]),
        "blocks": [], "downs": [], "pending": [],
        "capacities": [["m", 8, 0], ["m", 10, 1], ["m", 15, 0]],
        "result": {
            "completed_count": 4, "censored_entities": 1, "lead_times_minutes": [2, 2, 3, 2],
            "queue_waits_minutes": {"work": [0, 0, 1, 0]}, "resource_utilization": {"m": 0.5},
            "resource_states": {"m": state(8, 8, scheduled=13, scheduled_in_use=8, overtime=0, available=13, idle=5)},
            "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-C9
# (Added after mutation run 2 began: the mutant shift-end-wakes-too, a fall
# of S that also wakes the held front, was killed only by the 0.4.0
# observation lock; no golden had a request held at the front when S falls
# with nothing else waking it in that tick. Hand-derived like the others,
# before the engine ran it.)
cases.append({
    "id": "G-C9",
    "title": "a shift end while a request is held at the front wakes nothing and asks for no dispatch (D-052 (a), D-081 (b))",
    "model": model("G-C9", [resource("m", 1, "c")], [source("src", const(1), max_entities=2), process("work", const(6), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")], [calendar("c", 10, [(0, 5)])]),
    "config": config(20),
    "derivation": [
        "S(t) of m: 1 on [0,5), 0 on [5,10), 1 on [10,15), 0 on [15,20), 1 at 20 (period 10, shift [0,5)); one unit. The source stops after 2 entities.",
        "t=0: src's first arrival at 1 (seq 1); calendar c's first change at 5 (seq 2; D-048: sources, then calendars).",
        "t=1: ARRIVAL e1 (seq 1): the next arrival at 2 (seq 3); e1 queues, DISPATCH asked (seq 4). DISPATCH (seq 4): 0+1 <= min(S=1, C-D=1): e1 granted, service [1,7], completion seq 5.",
        "t=2: ARRIVAL e2 (seq 3): the source has created 2 = max_entities, no next arrival; e2 queues at the front of m's FIFO, DISPATCH (seq 6): 1+1 > 1, e2 held.",
        "t=5: CAPACITY_CHANGE (seq 2): first the next change, at 10 (seq 7); S 1 -> 0 (Capacity m 0). A decrease wakes nothing: e2 stays held and no DISPATCH is asked (D-052 (a), D-081 (b)); the tick has this one event. e1 keeps its unit (finish-first): overtime [5,7).",
        "t=7: SERVICE_COMPLETE e1 (seq 5): the release wakes the held front e2; DISPATCH asked (seq 8); e1 completes. DISPATCH (seq 8): 0+1 > min(S=0, 1): e2 held again.",
        "t=10: CAPACITY_CHANGE (seq 7): next change at 15 (seq 9); S 0 -> 1 (Capacity m 1) wakes the held front e2; DISPATCH (seq 10) grants e2: service [10,16], completion seq 11.",
        "t=15: CAPACITY_CHANGE (seq 9): next at 20 (seq 12); S 1 -> 0; m's FIFO is empty (e2 in service): nothing to wake. e2 keeps its unit: overtime [15,16).",
        "t=16: SERVICE_COMPLETE e2 (seq 11): release, nothing held; DISPATCH (seq 13) grants nothing; e2 completes.",
        "t=20: CAPACITY_CHANGE (seq 12): its next, 25, is after the horizon (not scheduled); S 0 -> 1; nothing to wake.",
        "13 events: 2 at each of 1, 2, 7, 10, 16 and 1 at each of 5, 15, 20; the last sequence number, the dispatch at 16, is 13. A shift end that woke the held front (the mutant) would add a DISPATCH at 5 (seq 8) and shift every later sequence number by one: 14 events, e2's service started by the dispatch of seq 11.",
        "Result over [0,20] (T = 20 min): completed e1, e2 (2), censored 0; lead times 6 (1 -> 7), 14 (2 -> 16); waits 0, 8 (e2 queued at 2, started at 10).",
        "in use [1,7) and [10,16) = 12 min, utilization 12/20 = 0.6; S on [0,5) and [10,15) = 10 min scheduled (and a zero-length [20,20]); min(U,S): [1,5) and [10,15) = 9 min; overtime [5,7) and [15,16) = 3 min (9 + 3 = 12 in use); available min(S, C-D) = S = 10 min; idle max(0, A-U): [0,1) = 1 min (10 - 9); scheduled processing min(P,S), P = U (no finite buffer) = 9 min.",
    ],
    "expected": {
        "arrivals": ids("src", [1, 2]),
        "starts": {"work": ids("src", [1, 10])},
        "completions": ids("src", [7, 16]),
        "blocks": [], "downs": [], "pending": [],
        "capacities": [["m", 5, 0], ["m", 10, 1], ["m", 15, 0], ["m", 20, 1]],
        "capacity_seqs": [2, 7, 9, 12],
        "service_start_seqs": [["r0/src/1", 1, 4], ["r0/src/2", 10, 10]],
        "events": 13,
        "result": {
            "completed_count": 2, "censored_entities": 0, "lead_times_minutes": [6, 14],
            "queue_waits_minutes": {"work": [0, 8]}, "resource_utilization": {"m": 0.6},
            "resource_states": {"m": state(12, 9, scheduled=10, scheduled_in_use=9, overtime=3, available=10, idle=1)},
            "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-C10
# (Added after mutation run 3: processing-ignores-blocked, ∫min(U, S) in
# place of ∫min(U - K, S), was killed only by the 0.4.0 Result lock; no
# golden had a resource on a calendar holding a blocked entity. Hand-derived
# before the engine ran it.)
cases.append({
    "id": "G-C10",
    "title": "a blocked entity holds a unit on a calendar across the shift end: blocked on shift is not processing, overtime counts the held unit (D-049, D-052, D-058)",
    "model": model("G-C10", [resource("m", 1, "c"), resource("n", 1)],
                   [source("src", const(1), max_entities=3), process("work", const(2), ("m", 1)), queue("q", 1),
                    process("pack", const(10), ("n", 1)), sink()],
                   [("src", "work"), ("work", "q"), ("q", "pack"), ("pack", "done")], [calendar("c", 20, [(0, 10)])]),
    "config": config(25),
    "derivation": [
        "S(t) of m: 1 on [0,10), 0 on [10,20), 1 on [20,25] (period 20, shift [0,10)); n has no calendar (S = C = 1). pack's buffer q holds 1 waiting entity. The source stops after 3 entities (arrivals at 1, 2, 3).",
        "t=0: src's first arrival at 1; calendar c's first change at 10.",
        "t=1: e1 arrives (next at 2) and is granted m: work [1,3].",
        "t=2: e2 arrives (next at 3); m in use: e2 held at work.",
        "t=3: SERVICE_COMPLETE e1 (priority 0): q is empty (0 waiting < 1), so e1 moves on: the release of m wakes e2; e1 enters q (pack's buffer). ARRIVAL e3 (10): the source has its 3, no next; e3 queues at work behind e2. DISPATCH (20): e2 granted m: work [3,5]; e1 granted n: pack [3,13]; e3, now the front of m, held.",
        "t=5: SERVICE_COMPLETE e2: q has 0 waiting (e1 is in service), so e2 moves on: the release of m wakes e3; e2 enters q and is held (n in use). DISPATCH: e3 granted m: work [5,7]. q is full (1 waiting) from 5.",
        "t=7: SERVICE_COMPLETE e3: q is full, so e3 is blocked at work (D-049) and keeps its unit of m: no release, no dispatch. K of m (units held by a blocked entity) 1 from 7.",
        "t=10: CAPACITY_CHANGE: next change at 20; S of m 1 -> 0. A decrease wakes nothing and interrupts nothing: the blocked e3 keeps m (finish-first, D-052 (a)); U - S = 1 from 10: overtime.",
        "t=13: SERVICE_COMPLETE e1 at pack: e1 completes (sink); the release of n wakes e2. DISPATCH: e2 granted n: pack [13,23]; its grant frees a place in q, so the blocked e3 moves in (unblocked at 13, block [7,13]), releasing m (U of m 0 from 13), and queues for n, held (n in use). q stays full.",
        "t=20: CAPACITY_CHANGE: its next, 30, is after the horizon; S of m 0 -> 1; nothing waits at m.",
        "t=23: SERVICE_COMPLETE e2 at pack: e2 completes; the release of n wakes e3; DISPATCH: e3 granted n: pack [23,33] (its completion is after the horizon). q is empty from 23.",
        "Result over [0,25] (T = 25 min): completed e1, e2 (2), censored e3 (in service); lead times 12 (1 -> 13), 21 (2 -> 23); waits at work 0, 1, 2 (e3 queued at 3, started at 5); waits at q 0 (e1 entered at 3, started at 3), 8 (e2: 5 -> 13), 10 (e3 entered q at 13 when unblocked, started at 23).",
        "m: U = 1 on [1,13) (e1 [1,3), e2 [3,5), e3 [5,7) in service and [7,13) blocked) = 12 min, utilization 12/25; K = 1 on [7,13) = 6 min blocked; S on [0,10) and [20,25] = 15 min scheduled; min(U,S): [1,10) = 9 min; overtime max(U-S,0): [10,13) = 3 min (9 + 3 = 12); available min(S, C-D) = S = 15 min; idle max(0, A-U): [0,1) and [20,25) = 6 min (15 - 9); scheduled processing min(U - K, S): U - K = 1 on [1,7) only, all on shift: 6 min (the blocked [7,10) on shift is not processing: min(U, S) would give 9).",
        "n: in use [3,25] (e1 [3,13), e2 [13,23), e3 [23,25]) = 22 min, utilization 22/25; blocked 0; processing 22 min (no calendar). Blocking at work: 1 block started and completed, 6 min, 6 entity-minutes. q full on [5,23) = 18 min (at 13 e2 leaves and e3 enters in the same tick).",
    ],
    "expected": {
        "arrivals": ids("src", [1, 2, 3]),
        "starts": {"work": ids("src", [1, 3, 5]), "pack": ids("src", [3, 13, 23])},
        "completions": ids("src", [13, 23]),
        "blocks": [["r0/src/3", "work", 7, 13]], "downs": [], "pending": [],
        "capacities": [["m", 10, 0], ["m", 20, 1]],
        "result": {
            "completed_count": 2, "censored_entities": 1, "lead_times_minutes": [12, 21],
            "queue_waits_minutes": {"work": [0, 1, 2], "q": [0, 8, 10]},
            "resource_utilization": {"m": 12 / 25, "n": 22 / 25},
            "resource_states": {"m": state(12, 6, blocked=6, scheduled=15, scheduled_in_use=9, overtime=3, available=15, idle=6),
                                "n": state(22, 22, blocked=0)},
            "blocking": {"work": {"blocks_started": 1, "blocks_completed": 1, "completed_block_ticks": 6 * MIN, "blocked_entity_ticks": 6 * MIN}},
            "buffers": {"q": {"full_ticks": 18 * MIN}}}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-F6
cases.append({
    "id": "G-F6",
    "title": "the scheduled failure clock pauses across the off-shift hours (ADR-026 D-051 G-F6)",
    "model": model("G-F6", [resource("m", 1, "c", ttf=const(8), ttr=const(1))],
                   [source("src", const(5)), process("work", const(1), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")], [calendar("c", 10, [(0, 6)])]),
    "config": config(30),
    "derivation": [
        "S of m: 1 on [0,6), 0 on [6,10), periodically. Θ(t) counts the on-shift ticks of [0,t) (D-051 (ii)).",
        "t=0: arrival at 5 (seq 1); the calendar's first change at 6 (seq 2); the clock's TTF 8: Θ reaches 8 at 12 (6 on [0,6), 2 on [10,12)): FAILURE at 12 (seq 3). (The calendar-time clock D-051 rejected would fail at 8, off shift.)",
        "e1 [5,6]. t=6: e1 completes (0); CAPACITY_CHANGE: next at 10; S 1 -> 0. t=10: e2 arrives (next at 15); CAPACITY_CHANGE: next at 16; S 0 -> 1 (e2 is a candidate, not held); DISPATCH: e2 [10,11].",
        "t=12: FAILURE: the unit is idle, down at once; TTR 1: REPAIR at 13. t=13: REPAIR; Θ(13) = 9, the next failure at Θ = 17: 3 more on [13,16), 5 on [20,25): at 25.",
        "e3 [15,16]. t=16: e3 completes (0); CAPACITY_CHANGE: next at 20; S 1 -> 0. t=20: e4 arrives; CAPACITY_CHANGE: next at 26; S 0 -> 1; e4 [20,21].",
        "t=25: ARRIVAL e5 (10; next at 30) queues; FAILURE (15): the unit is idle, down, REPAIR at 26; DISPATCH: e5 held (0+1 > min(S=1, C-D=0)).",
        "t=26: two priority-15 events: the CAPACITY_CHANGE (scheduled at 20) before the REPAIR (scheduled at 25). The change: next at 30; S 1 -> 0. The repair: Θ(26) = 18 (6 on each of [0,6), [10,16), [20,26)), the next failure at Θ = 26 is at 42 ([30,36) brings Θ to 24, [40,42) to 26), after the horizon; no failure waits, so it wakes e5; DISPATCH: e5 held (S = 0). (Review round 1 I1: this line said 38; the arithmetic gives 42. Text only: 38 and 42 are both after the horizon, nothing expected changes.)",
        "t=30: ARRIVAL e6 (the next, 35, is not scheduled); CAPACITY_CHANGE (its next, 36, is not): S 0 -> 1 wakes e5; DISPATCH grants e5 at 30; e6 held.",
        "Result over [0,30]: S 18 min; in use 4 min, all on shift; down [12,13) and [25,26) inside shifts: 2 min; available = 18 - 2 = 16; idle = 16 - 4 = 12; 2 breakdowns, 2 complete repairs (2 min); utilization 4/30. Completed 4, censored 2; lead times 1, 1, 1, 1; waits 0, 0, 0, 0 and e5's 5.",
    ],
    "expected": {
        "starts": {"work": ids("src", [5, 10, 15, 20, 30])},
        "completions": ids("src", [6, 11, 16, 21]),
        "blocks": [], "downs": [["m", 0, 12, 13], ["m", 0, 25, 26]], "pending": [],
        "capacities": [["m", 6, 0], ["m", 10, 1], ["m", 16, 0], ["m", 20, 1], ["m", 26, 0], ["m", 30, 1]],
        "result": {
            "completed_count": 4, "censored_entities": 2, "lead_times_minutes": [1, 1, 1, 1],
            "queue_waits_minutes": {"work": [0, 0, 0, 0, 5]}, "resource_utilization": {"m": 4 / 30},
            "resource_states": {"m": state(4, 4, scheduled=18, scheduled_in_use=4, overtime=0, available=16, idle=12,
                                           down=2, started=2, repairs=2, repair_ticks=2)},
            "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-CF1
cases.append({
    "id": "G-CF1",
    "title": "a failure that waits across a shift start: the start raises the capacity but frees no unit, the release does (D-074 (a) B with a calendar)",
    "model": model("G-CF1", [resource("m", 1, "c", ttf=const(3), ttr=const(2))],
                   [source("src", const(1), 2), process("work", const(12), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")], [calendar("c", 10, [(0, 5)])]),
    "config": config(22),
    "derivation": [
        "S of m: 1 on [0,5), 0 on [5,10), 1 on [10,15), 0 on [15,20), 1 on [20,25). Services of 12 min; two arrivals, at 1 and 2.",
        "t=0: arrival at 1 (seq 1); the first change at 5 (seq 2); the clock's TTF 3: FAILURE at 3 (seq 3).",
        "t=1: e1 granted: [1,13]. t=2: e2 arrives (the last) and is held (in use).",
        "t=3: FAILURE: idle = C - D - in use = 0: it waits (finish-first, D-051 A; ObsBreakdownPending 1).",
        "t=5: CAPACITY_CHANGE: next at 10; S 1 -> 0; e1 keeps its unit (overtime from 5).",
        "t=10: CAPACITY_CHANGE: next at 15; S 0 -> 1. Off-shift units were idle for failures too (D-051: 含下班的), so a failure only waits when every unit not down is in use; a shift start changes neither D nor in use and gives the waiting failure nothing (D-081 (c)). It wakes the held front e2; DISPATCH: 1+1 > min(1, 1), e2 held again.",
        "t=13: e1 completes: the released unit goes to the waiting failure first (D-074 (a) B, before any grant): down at 13, TTR 2: REPAIR at 15; then the release wakes e2; DISPATCH: 0+1 > min(1, C-D=0), held.",
        "t=15: CAPACITY_CHANGE (scheduled at 10) before the REPAIR (scheduled at 13): next at 20; S 1 -> 0. REPAIR: the next failure needs Θ >= Θ(15) + 3 = 13 (10 on-shift ticks in [0,15)): at 23, after the horizon; no failure waits: it wakes e2; DISPATCH: held (S = 0).",
        "t=20: CAPACITY_CHANGE: its next, 25, is after the horizon; S 0 -> 1 wakes e2; DISPATCH grants e2 [20,32].",
        "Result over [0,22]: S on [0,5), [10,15), [20,22]: 12 min; in use [1,13) and [20,22]: 14 min; min(U,S) [1,5) 4 + [10,13) 3 + [20,22) 2 = 9; overtime [5,10) 5 (9 + 5 = 14); down [13,15) 2 min; available min(S, C-D): 5 + 3 + 0 + 2 = 10; idle max(0, A-U): [0,1) 1; 1 breakdown, 1 complete repair (2 min); utilization 14/22. Completed 1, censored 1; lead time 12; waits 0 and 18.",
    ],
    "expected": {
        "starts": {"work": ids("src", [1, 20])},
        "completions": ids("src", [13]),
        "blocks": [], "downs": [["m", 0, 13, 15]], "pending": [["m", 0, 3]],
        "capacities": [["m", 5, 0], ["m", 10, 1], ["m", 15, 0], ["m", 20, 1]],
        "result": {
            "completed_count": 1, "censored_entities": 1, "lead_times_minutes": [12],
            "queue_waits_minutes": {"work": [0, 18]}, "resource_utilization": {"m": 14 / 22},
            "resource_states": {"m": state(14, 9, scheduled=12, scheduled_in_use=9, overtime=5, available=10, idle=1,
                                           down=2, started=1, repairs=1, repair_ticks=2)},
            "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-T5
cases.append({
    "id": "G-T5",
    "title": "t=0 order (sources, calendars, failure clocks) and a failure whose target is reached at a shift's end: at that tick, on the off-shift idle unit",
    "model": model("G-T5", [resource("m", 1, "c", ttf=const(6), ttr=const(1))],
                   [source("src", const(3), 1), process("work", const(1), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")], [calendar("c", 10, [(0, 6)])]),
    "config": config(12),
    "derivation": [
        "S of m: 1 on [0,6), 0 on [6,10), 1 on [10,16). TTF 6 of on-shift time from t=0: Θ reaches 6 at 6, the end of the shift: the failure is at that end's tick (D-051: 目標恰在一段上班的結束時達到時，故障時刻即該段的結束 tick).",
        "t=0 (D-048): the source first (arrival at 3, seq 1), then the calendar (its first change at 6, seq 2), then the failure clock (FAILURE at 6, seq 3).",
        "t=3: e1 [3,4]. t=4: e1 completes.",
        "t=6: CAPACITY_CHANGE (seq 2) first: its next change at 10 (seq 7); S 1 -> 0. Then FAILURE (seq 3): idle = C - D - in use = 1 (the off-shift unit counts, D-051), down at once; TTR 1: REPAIR at 7.",
        "t=7: REPAIR: Θ(7) = 6, the next failure at Θ = 12 is at 16, after the horizon. t=10: CAPACITY_CHANGE: its next, 16, is after the horizon; S 0 -> 1.",
        "At tick 6 the observations are, in this order: Capacity m 0, then ResourceDown m/0.",
        "Result over [0,12]: S 6 + 2 = 8 min; in use 1 min on shift; down [6,7) off shift: 1 min; available min(S, C-D) = 8 (the downtime is off shift); idle 7; 1 breakdown, 1 complete repair (1 min); utilization 1/12. Completed 1; lead time 1; wait 0.",
    ],
    "expected": {
        "starts": {"work": ids("src", [3])},
        "completions": ids("src", [4]),
        "blocks": [], "downs": [["m", 0, 6, 7]], "pending": [],
        "capacities": [["m", 6, 0], ["m", 10, 1]],
        "at_tick_6": ["capacity m 0", "down m 0"],
        "result": {
            "completed_count": 1, "censored_entities": 0, "lead_times_minutes": [1],
            "queue_waits_minutes": {"work": [0]}, "resource_utilization": {"m": 1 / 12},
            "resource_states": {"m": state(1, 1, scheduled=8, scheduled_in_use=1, overtime=0, available=8, idle=7,
                                           down=1, started=1, repairs=1, repair_ticks=1)},
            "blocking": None, "buffers": None}},
    "simpy": "not compared: the failure is due exactly at the shift's end; in the script the change's blocker takes the off-shift unit first, which the kernel counts as idle for a failure (D-051), so the script's failure would wait",
})

# ---------------------------------------------------------------- G-K2
cases.append({
    "id": "G-K2",
    "title": "a held request that fits the capacity but not the units on shift is capacity-bound: no ErrDeadlock, and the shift start wakes it (T-047 review round 2, I1; T-049)",
    "model": model("G-K2", [resource("m", 1, "c")],
                   [source("src", const(1), 2), queue("q", 1), process("work", const(1), ("m", 1)), sink()],
                   [("src", "q"), ("q", "work"), ("work", "done")], [calendar("c", 10, [(5, 10)])]),
    "config": config(12),
    "derivation": [
        "S of m: 0 on [0,5), 1 on [5,10), 0 on [10,15). q holds one waiting entity (a finite buffer: the program runs D-050 B1).",
        "t=0: arrival at 1 (seq 1); the first change at 5 (seq 2).",
        "t=1: ARRIVAL e1 (seq 1): next at 2 (seq 3); e1 enters q; DISPATCH (seq 4): 0+1 > min(S=0, 1): held. After the dispatch, B1: a held candidate and nothing in service, but in_use + units = 0 + 1 is not above C = 1: capacity-bound, not a circular wait (the bound is C, not min(S, C-D); with >= instead of > it would be reported here).",
        "t=2: ARRIVAL e2 (seq 3, the last): q is full: e2 is blocked at src (a release backlog). B1 again: still capacity-bound, no ErrDeadlock.",
        "t=5: CAPACITY_CHANGE (seq 2): first the next change, at 10 (seq 5); S 0 -> 1 wakes the held e1, and with no dispatch pending asks for one (seq 6). DISPATCH (seq 6) grants e1 [5,6] (completion seq 7), which makes room in q: e2 moves in (Unblocked, QueueEnter) and, as the new candidate, is held (1+1 > 1).",
        "t=6: e1 completes (seq 7), waking e2; DISPATCH (seq 8) grants e2 [6,7] (seq 9). t=7: e2 completes; DISPATCH (seq 10). t=10: CAPACITY_CHANGE: its next, 15, is after the horizon; S 1 -> 0. 10 events.",
        "Result over [0,12]: m in use 2 min, blocked 0 (src holds no unit); S on [5,10): 5 min; min(U,S) 2; overtime 0; available 5; idle 3; scheduled processing 2; utilization 2/12. Blocking at src: 1 started, 1 complete of 3 min, 3 min of blocked entity time; q full on [1,6): 5 min. Completed 2; lead times 5, 5; waits 4 and 1 (e2 queues from its unblocking at 5, D-064 (f)).",
    ],
    "expected": {
        "starts": {"work": ids("src", [5, 6])},
        "completions": ids("src", [6, 7]),
        "blocks": [["r0/src/2", "src", 2, 5]], "downs": [], "pending": [],
        "capacities": [["m", 5, 1], ["m", 10, 0]],
        "service_start_seqs": [["r0/src/1", 5, 6], ["r0/src/2", 6, 8]],
        "events": 10,
        "result": {
            "completed_count": 2, "censored_entities": 0, "lead_times_minutes": [5, 5],
            "queue_waits_minutes": {"q": [4, 1]}, "resource_utilization": {"m": 2 / 12},
            "resource_states": {"m": state(2, 2, blocked=0, scheduled=5, scheduled_in_use=2, overtime=0, available=5, idle=3)},
            "blocking": {"src": {"blocks_started": 1, "blocks_completed": 1, "completed_block_ticks": 3 * MIN, "blocked_entity_ticks": 3 * MIN}},
            "buffers": {"q": {"full_ticks": 5 * MIN}}}},
    "simpy": "not compared: the point is the kernel's B1 check",
})

# ---------------------------------------------------------------- G-D1
cases.append({
    "id": "G-D1",
    "title": "D-050 B2 with a calendar: R4 bypassed, the machine fails as its partner's shift starts; ErrStalled at next_fit(10 + TTRmax) (ADR-026 D-050 G-D1)",
    "model": model("G-D1", [resource("machine", 1, ttf=const(960), ttr=const(480)), resource("operator", 1, "op", typ="human")],
                   [source("src", const(10), 1), process("work", const(30), ("machine", 1), ("operator", 1)), sink()],
                   [("src", "work"), ("work", "done")], [calendar("op", 1440, [(960, 1440)])]),
    "config": config(1500),
    "validator": {"codes_at": [["STALL_RISK", "/nodes/1/requires"]]},
    "derivation": [
        "The validator refuses the model (D-050 R4: the machine, which breaks down and has no calendar, is on shift at every tick, the operator only on [960,1440)); the test runs it past the validator.",
        "S of operator: 0 on [0,960), 1 on [960,1440). The machine has no calendar: its clock is calendar time, TTF 960: FAILURE at 960.",
        "t=0: arrival at 10 (seq 1); the operator's calendar: first change at 960 (seq 2); the machine's clock: FAILURE at 960 (seq 3).",
        "t=10: e1 arrives (the only one) and is held (operator off shift). At the tick's end it is in B2's state (held, none of its bundle in use): since 10; TTRmax = 480 (the machine's constant TTR), X_end = 0 (no exception): deadline = next_fit(max(10, 0) + 480) = next_fit(490) = 960, the first tick at which the operator is on shift (the machine is always).",
        "t=960: CAPACITY_CHANGE (seq 2): next at 1440; S of operator 0 -> 1 wakes e1; DISPATCH asked. FAILURE (seq 3): the machine's unit is idle: down, REPAIR at 1440. DISPATCH: the machine has 0+1 > min(1, C-D=0): held. At the tick's end e1 is still in the state (woken and held within the tick: since is not reset, D-074 (c)) and 960 >= 960: ErrStalled.",
    ],
    "expected": {
        "starts": {}, "completions": [], "blocks": [], "downs": [["machine", 0, 960, None]], "pending": [],
        "capacities": [["operator", 960, 1]],
        "stalled": {"tick": 960 * MIN, "entity": "r0/src/1", "node": "work", "since": 10 * MIN, "deadline": 960 * MIN,
                    "reason": f"the replication stalled at tick {960 * MIN}: r0/src/1 waits for node work since tick {10 * MIN} with none of its bundle in use; it was due by tick {960 * MIN}"}},
    "simpy": "not compared: SimPy does not detect stalls",
})

# ---------------------------------------------------------------- G-D3C
cases.append({
    "id": "G-D3C",
    "title": "B2 must not report a request that waits for the units of the next day shift (ADR-026 D-050 G-D3, review N1 (a); named G-D3C here, G-D3 being T-047's)",
    "model": model("G-D3C", [resource("R", 2, "c", {"day": 2, "evening": 1})],
                   [source("src", const(900), 1), process("work", const(60), ("R", 2)), sink()],
                   [("src", "work"), ("work", "done")], [calendar("c", 1440, [(360, 840, "day"), (840, 1320, "evening")])]),
    "config": config(2000),
    "derivation": [
        "S of R: 0 on [0,360), 2 on [360,840) (day), 1 on [840,1320) (evening), 0 on [1320,1800) (the next day's [0,360) included: no change at 1440), 2 from 1800. X (work) needs 2 units.",
        "t=0: arrival at 900; the calendar's first change at 360 (0 -> 2). t=360: next at 840; S 0 -> 2. t=840: next at 1320; S 2 -> 1.",
        "t=900: e1 arrives, held (0+2 > min(1, 2)); none of its bundle is in use: B2's state since 900; TTRmax = 0 (nothing breaks down), X_end = 0: deadline = next_fit(900) = 1800 (the next tick with S >= 2). No report at 900.",
        "t=1320: next at 1800 (S stays 0 across 1440); S 1 -> 0. At the tick's end: 1320 < 1800.",
        "t=1800: CAPACITY_CHANGE: its next, 2280, is after the horizon; S 0 -> 2 wakes e1; DISPATCH grants it [1800,1860]. It never stalls.",
        "Result over [0,2000]: S 2x480 + 1x480 + 2x200 = 1840 min of unit time; in use 2x60 = 120, on shift; idle 1720; utilization 120/4000. Completed 1; lead time 960; wait 900.",
    ],
    "expected": {
        "starts": {"work": ids("src", [1800])},
        "completions": ids("src", [1860]),
        "blocks": [], "downs": [], "pending": [],
        "capacities": [["R", 360, 2], ["R", 840, 1], ["R", 1320, 0], ["R", 1800, 2]],
        "result": {
            "completed_count": 1, "censored_entities": 0, "lead_times_minutes": [960],
            "queue_waits_minutes": {"work": [900]}, "resource_utilization": {"R": 0.03},
            "resource_states": {"R": state(120, 120, scheduled=1840, scheduled_in_use=120, overtime=0, available=1840, idle=1720)},
            "blocking": None, "buffers": None}},
    "simpy": "refused: work needs 2 units of R",
})

# ---------------------------------------------------------------- G-D4C
d4_exc = [(540 + 1440 * k, 600 + 1440 * k, False) for k in range(100)]
d4_caps = []
for k in range(100):
    d4_caps += [["r1", 1440 * k + 540, 0], ["r2", 1440 * k + 540, 1], ["r1", 1440 * k + 1440, 1], ["r2", 1440 * k + 1440, 0]]
d4_caps += [["r2", 144540, 1], ["r1", 144600, 0]]
cases.append({
    "id": "G-D4C",
    "title": "B2 must not report before the last exception day: X_end (ADR-026 D-050 G-D4, review N1 (b); named G-D4C here, G-D4 being T-048's)",
    "model": model("G-D4C", [resource("r1", 1, "a"), resource("r2", 1, "b")],
                   [source("src", const(10), 1), process("work", const(30), ("r1", 1), ("r2", 1)), sink()],
                   [("src", "work"), ("work", "done")],
                   [calendar("a", 1440, [(0, 600)], d4_exc), calendar("b", 1440, [(540, 1440)])]),
    "config": config(144600),
    "derivation": [
        "r1 (calendar a): shift [0,600) daily, closed on [540+1440k, 600+1440k) for k = 0..99 (100 exceptions): on [1440k, 1440k+540) for k <= 99, and on all of day 100's [144000,144600). r2 (calendar b): on [1440k+540, 1440k+1440). Periodically they meet on [540,600) (R2 holds), but every such hour of days 0-99 is closed: the first tick both are on is 100x1440 + 540 = 144540.",
        "t=0: arrival at 10; calendars in ID order: a's first change at 540 (seq 2), b's at 540 (seq 3). Every later change of a is scheduled before b's of the same tick (a's event comes first), so at each shared tick r1's Capacity precedes r2's.",
        "t=10: e1 held (r2 off). B2: since 10; X_end = 99x1440 + 600 = 143160 (a's last exception); deadline = next_fit(max(10, 143160)) = 144540. Before it, no report.",
        "Each day k <= 99: at 1440k+540 r1 1 -> 0 (the exception; at 600 it ends where the periodic shift ends: no change) and r2 0 -> 1 (waking e1, held again); at 1440k+1440 r1 0 -> 1 (waking e1, held: r2 off) and r2 1 -> 0.",
        "Day 100: at 144000 r1 0 -> 1, r2 1 -> 0; at 144540 r2 0 -> 1 (a has no change there) wakes e1: granted [144540,144570]; at 144600 r1 1 -> 0.",
        "Result over [0,144600]: r1 S 100x540 + 600 = 54600 min, r2 S 100x900 + 60 = 90060 min; each in use 30 min, on shift; idle 54570 and 90030. Completed 1; lead time 144560; wait 144530.",
    ],
    "expected": {
        "starts": {"work": ids("src", [144540])},
        "completions": ids("src", [144570]),
        "blocks": [], "downs": [], "pending": [],
        "capacities": d4_caps,
        "result": {
            "completed_count": 1, "censored_entities": 0, "lead_times_minutes": [144560],
            "queue_waits_minutes": {"work": [144530]}, "resource_utilization": {"r1": 30 / 144600, "r2": 30 / 144600},
            "resource_states": {"r1": state(30, 30, scheduled=54600, scheduled_in_use=30, overtime=0, available=54600, idle=54570),
                                "r2": state(30, 30, scheduled=90060, scheduled_in_use=30, overtime=0, available=90060, idle=90030)},
            "blocking": None, "buffers": None}},
    "simpy": "refused: work's bundle has two resources with calendars",
})

# ---------------------------------------------------------------- G-D5
cases.append({
    "id": "G-D5",
    "title": "B2 must not report while a failure in an exception keeps the unit down: deadline next_fit(max(t_q, X_end) + TTRmax) (ADR-026 D-050 G-D5, review N6 (b))",
    "model": model("G-D5", [resource("m", 1, "cm", ttf=const(960), ttr=const(1500)), resource("o", 1, "co", typ="human")],
                   [source("src", const(1450), 1), process("work", const(60), ("m", 1), ("o", 1)), sink()],
                   [("src", "work"), ("work", "done")],
                   [calendar("cm", 1440, [(0, 480)]), calendar("co", 1440, [(0, 480)], [(1440, 1920, False)])]),
    "config": config(4400),
    "derivation": [
        "m and o are both on [0,480) daily; o's exception closes [1440,1920) (day 1's shift). The model is valid: R2 and R4 read only the periods, where m on shift implies o on shift.",
        "t=0: arrival at 1450 (seq 1); calendars in ID order: cm's first change at 480 (seq 2), co's at 480 (seq 3); m's clock (TTF 960 of on-shift time): Θ = 480 at 480, then 480 more on [1440,1920): FAILURE at 1920, the shift's end (seq 4).",
        "t=480: cm: next at 1440; m 1 -> 0. co: its next change is at 2880 (at 1440 the exception keeps S at 0, at 1920 the period is off); o 1 -> 0. t=1440: cm: next at 1920; m 0 -> 1.",
        "t=1450: e1 arrives, held (o is closed). B2: since 1450; TTRmax 1500; X_end = 1920 (co's exception); deadline = next_fit(max(1450, 1920) + 1500) = next_fit(3420) = 4320 (day 3's shift start).",
        "t=1920: FAILURE (seq 4, scheduled at 0) before cm's change (scheduled at 1440): the idle unit goes down, REPAIR at 3420; then m 1 -> 0.",
        "t=2880: co's change (scheduled at 480) before cm's (scheduled at 1920): o 0 -> 1 wakes e1; m 0 -> 1. DISPATCH: m's unit is down: held. 2880 < 4320.",
        "t=3360: o 1 -> 0, m 1 -> 0. t=3420: REPAIR: the next failure (Θ(3420) = 1440, at Θ = 2400) is at 6240, after the horizon; it wakes e1; DISPATCH: m off shift, held. 3420 < 4320.",
        "t=4320: o 0 -> 1 (wakes e1), m 0 -> 1 (no later change before the horizon); DISPATCH grants e1 [4320,4380]. No stall. (Without X_end the deadline would be next_fit(2950) = 2950, reported at the end of tick 3360; without TTRmax, next_fit(1920) = 2880, reported at 2880.)",
        "Result over [0,4400]: m: S 480 + 480 + 480 + 80 = 1520 min; down [1920,3420) 1500 min, 480 of it on shift; available 1520 - 480 = 1040; in use 60 (on shift); idle 980; 1 breakdown, 1 complete repair of 1500 min. o: S 480 + 0 + 480 + 80 = 1040; in use 60; idle 980. Utilization 60/4400 each. Completed 1; lead time 2930; wait 2870.",
    ],
    "expected": {
        "starts": {"work": ids("src", [4320])},
        "completions": ids("src", [4380]),
        "blocks": [], "downs": [["m", 0, 1920, 3420]], "pending": [],
        "capacities": [["m", 480, 0], ["o", 480, 0], ["m", 1440, 1], ["m", 1920, 0], ["o", 2880, 1], ["m", 2880, 1],
                       ["o", 3360, 0], ["m", 3360, 0], ["o", 4320, 1], ["m", 4320, 1]],
        "result": {
            "completed_count": 1, "censored_entities": 0, "lead_times_minutes": [2930],
            "queue_waits_minutes": {"work": [2870]}, "resource_utilization": {"m": 60 / 4400, "o": 60 / 4400},
            "resource_states": {"m": state(60, 60, scheduled=1520, scheduled_in_use=60, overtime=0, available=1040, idle=980,
                                           down=1500, started=1, repairs=1, repair_ticks=1500),
                                "o": state(60, 60, scheduled=1040, scheduled_in_use=60, overtime=0, available=1040, idle=980)},
            "blocking": None, "buffers": None}},
    "simpy": "refused: work's bundle has m, which breaks down, and o",
})

# ---------------------------------------------------------------- G-R1
gr1 = [1, 2, 3, 7, 8, 9, 13, 14, 15]
cases.append({
    "id": "G-R1",
    "title": "a deterministic release schedule: a = 3 falls at 3, the end of the first segment, not at 6 (ADR-026 D-053 G-R1, a shorter period)",
    "model": model("G-R1", [], [source("src", schedule={"process": "deterministic", "period": 6,
                                                         "segments": [{"start": 0, "rate": 1}, {"start": 3, "rate": 0}]}), sink()],
                   [("src", "done")]),
    "config": config(16),
    "derivation": [
        "Λ(t) over one period: rate 1 on [0,3), 0 on [3,6): P = Λ(6) = 3 (cumStart 0, 3; cumEnd 3, 3). Deterministic: a_k = k (no draw).",
        "a = 1, 2, 3: k = ceil(a/3) - 1 = 0, r = a; the first segment with rate > 0 and cumEnd >= r is [0,3): τ = 0 + 0 + r/1 = 1, 2, 3 (a = 3 at 3, the segment's end, clamped to [0,3]: the ceiling, not 6).",
        "a = 4..6: k = 1, r = 1..3: τ = 6 + r = 7, 8, 9. a = 7..9: k = 2: 13, 14, 15. a = 10: 19 > 16, never scheduled.",
        "Each τ is quantized (minutes x 60000, round half up) and is at least one tick after the last; every value here is a whole minute. Each arrival goes straight to the sink: completions at the arrival times, lead time 0. 9 events.",
    ],
    "expected": {
        "arrivals": ids("src", gr1), "starts": {}, "completions": ids("src", gr1),
        "blocks": [], "downs": [], "pending": [], "capacities": [], "events": 9,
        "result": {"completed_count": 9, "censored_entities": 0, "lead_times_minutes": [0] * 9, "queue_waits_minutes": {},
                   "resource_utilization": {}, "resource_states": None, "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-R5
gr5 = [0.004, 0.008, 0.010, 0.012, 0.014, 0.016, 0.020, 0.023, 0.025, 0.027, 0.029, 0.031, 0.035, 0.039]
cases.append({
    "id": "G-R5",
    "title": "a rate change inside a tick (at 7.8125 ms) and the quantization of each arrival (dyadic values, exact in binary64)",
    "model": model("G-R5", [], [source("src", schedule={"process": "deterministic", "period": 0.015625,
                                                         "segments": [{"start": 0, "rate": 256}, {"start": 0.0078125, "rate": 512}]}), sink()],
                   [("src", "done")], unit="second"),
    "config": config(0.04, unit="second"),
    "derivation": [
        "Seconds, 1000 ticks a second. Period 2^-6 s = 15.625 ms; rate 256/s on [0, 2^-7 s = 7.8125 ms), 512/s on [7.8125, 15.625) ms: the change is inside tick 7. P = 256 x 2^-7 + 512 x 2^-7 = 2 + 4 = 6 (cumStart 0, 2; cumEnd 2, 6). Every value below is a dyadic rational, so binary64 is exact.",
        "a = 1: k = 0, r = 1, segment 0: τ = 1/256 = 3.90625 ms -> 4. a = 2: r = 2 = cumEnd_0, segment 0: 7.8125 ms -> 8 (round half up of 7.8125).",
        "a = 3..6: segment 1: τ = 7.8125 + (r - 2) x 1.953125 ms = 9.765625, 11.71875, 13.671875, 15.625 -> 10, 12, 14, 16.",
        "a = 7..12: k = 1 (15.625 ms later): 19.53125, 23.4375, 25.390625, 27.34375, 29.296875, 31.25 -> 20, 23, 25, 27, 29, 31.",
        "a = 13, 14: k = 2: 35.15625, 39.0625 -> 35, 39; a = 15: 41.015625 -> 41 > 40 (the horizon, 0.04 s), never scheduled.",
        "Each tick is above the last, so the one-tick floor never applies. Straight to the sink: 14 completions at the arrival ticks, lead time 0. 14 events.",
    ],
    "expected": {
        "arrivals": ids("src", gr5), "starts": {}, "completions": ids("src", gr5),
        "blocks": [], "downs": [], "pending": [], "capacities": [], "events": 14,
        "result": {"completed_count": 14, "censored_entities": 0, "lead_times_minutes": [0] * 14, "queue_waits_minutes": {},
                   "resource_utilization": {}, "resource_states": None, "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-R7
gr7_arr = [0, 0, 30, 30, 30, 60, 60, 90, 90]
cases.append({
    "id": "G-R7",
    "title": "a release table: whole batches at fixed times, consecutive entity numbers, max_entities mid-batch (ADR-026 D-053 G-R7, a shorter table)",
    "model": model("G-R7", [resource("m", 1)],
                   [source("src", max_entities=9, schedule={"process": "table", "period": 60,
                                                            "releases": [{"at": 0, "quantity": 2}, {"at": 30, "quantity": 3}]}),
                    process("work", const(10), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")]),
    "config": config(150),
    "derivation": [
        "Releases at k x 60 + 0 (2 entities) and k x 60 + 30 (3), in ticks (D-082 (b)); max_entities 9. One ARRIVAL event a row: it first schedules the next row, then creates its entities one after another, each routed at once (D-053).",
        "t=0: the first row is due at 0 (seq 1). t=0 (seq 1): the next row at 30 (seq 2; 0 + 2 < 9); e1 queues (DISPATCH seq 3), e2 queues; DISPATCH grants e1 [0,10] (seq 4), e2 held.",
        "e2 [10,20]. t=30 (seq 2): the next row at 60 (seq 8; 2 + 3 < 9); e3, e4, e5; e3 [30,40], e4 [40,50], e5 [50,60].",
        "t=60: e5 completes (priority 0, DISPATCH seq 15); the row (seq 8): next at 90 (seq 16; 5 + 2 < 9); e6, e7; e6 [60,70], e7 [70,80].",
        "t=90 (seq 16): the next row would bring 7 + 3 = 10 >= 9 entities: not scheduled (no empty event later); e8 and e9 are created, and the source reaches max_entities in the middle of the row (the third is not). e8 [90,100], e9 [100,110].",
        "25 events. Completed 9; lead times 10, 20, 10, 20, 30, 10, 20, 10, 20; waits 0, 10, 0, 10, 20, 0, 10, 0, 10; m busy 90 of 150 min.",
    ],
    "expected": {
        "arrivals": ids("src", gr7_arr),
        "starts": {"work": ids("src", [0, 10, 30, 40, 50, 60, 70, 90, 100])},
        "completions": ids("src", [10, 20, 40, 50, 60, 70, 80, 100, 110]),
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "service_start_seqs": [["r0/src/1", 0, 3], ["r0/src/2", 10, 5], ["r0/src/3", 30, 9], ["r0/src/4", 40, 11], ["r0/src/5", 50, 13],
                               ["r0/src/6", 60, 15], ["r0/src/7", 70, 18], ["r0/src/8", 90, 21], ["r0/src/9", 100, 23]],
        "events": 25,
        "result": {"completed_count": 9, "censored_entities": 0, "lead_times_minutes": [10, 20, 10, 20, 30, 10, 20, 10, 20],
                   "queue_waits_minutes": {"work": [0, 10, 0, 10, 20, 0, 10, 0, 10]}, "resource_utilization": {"m": 0.6},
                   "resource_states": None, "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-R8
# (Review round 1 M1: G-R7's period is a whole number of ticks, so it cannot
# tell D-082 (b) A from B. Hand-derived before the engine and SimPy ran it.)
gr8 = [0, 0.001, 0.003, 0.004, 0.006, 0.007, 0.009, 0.01, 0.012, 0.013]
cases.append({
    "id": "G-R8",
    "title": "a release table whose period and release time are not whole ticks: each is quantized first, then k x period + at in ticks (D-082 (b) A)",
    "model": model("G-R8", [], [source("src", schedule={"process": "table", "period": 0.0026,
                                                         "releases": [{"at": 0, "quantity": 1}, {"at": 0.0012, "quantity": 1}]}), sink()],
                   [("src", "done")], unit="second"),
    "config": config(0.013, unit="second"),
    "derivation": [
        "Seconds, 1000 ticks a second (model.Ticks: round half up, a positive value at least 1 tick). The period 0.0026 s is 2.6 ms -> 3 ticks; the rows' at 0 -> 0 ticks and 0.0012 s = 1.2 ms -> 1 tick (neither is near a half, so binary64 cannot move them).",
        "D-082 (b) A: row j of period k is released at k x 3 + at_j ticks: 0, 1; 3, 4; 6, 7; 9, 10; 12, 13. The next, period 5's first row at 15, is after the horizon (13 ticks) and never scheduled.",
        "One ARRIVAL a row (D-082 (c)), each creating one entity that goes straight to the sink in the same event: 10 events, 10 completions at the arrival ticks, lead time 0.",
        "Option B (k x 2.6 ms + at in model time, then quantized) would release at round(0, 1.2, 2.6, 3.8, 5.2, 6.4, 7.8, 9.0, 10.4, 11.6, 13.0) = 0, 1, 3, 4, 5, 6, 8, 9, 10, 12, 13: 11 arrivals, apart from A from the third period on (the drift of 0.4 ms a period).",
    ],
    "expected": {
        "arrivals": ids("src", gr8), "starts": {}, "completions": ids("src", gr8),
        "blocks": [], "downs": [], "pending": [], "capacities": [], "events": 10,
        "result": {"completed_count": 10, "censored_entities": 0, "lead_times_minutes": [0] * 10, "queue_waits_minutes": {},
                   "resource_utilization": {}, "resource_states": None, "blocking": None, "buffers": None}},
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-R9
# (Review round 1 I2: ADR-026's planned G-R5 was "high rate: ticks strictly
# increase"; the fixture's G-R5 is the rate change inside a tick. This is the
# ADR's case under a new ID: the rate at the validator's cap.)
gr9 = [0.001, 0.002, 0.003, 0.004, 0.005, 0.006, 0.007, 0.008, 0.009, 0.01, 0.011, 0.012]
cases.append({
    "id": "G-R9",
    "title": "a deterministic schedule at the rate cap, one release a tick: the ticks strictly increase (ADR-026 D-053's planned G-R5, high rate)",
    "model": model("G-R9", [], [source("src", schedule={"process": "deterministic", "period": 0.01,
                                                         "segments": [{"start": 0, "rate": 1000}]}), sink()],
                   [("src", "done")], unit="second"),
    "config": config(0.012, unit="second"),
    "derivation": [
        "Seconds, 1000 ticks a second. Rate 1000/s, the most the validator accepts (one release a tick, D-085 (d)); period 0.01 s, P = 1000 x 0.01 = 10.",
        "a = n: k = ceil(n/10) - 1, r = n - 10k, τ = k x 0.01 s + r/1000 s = n ms exactly; every τ is a whole tick, so binary64's error (well under half a tick) cannot move the rounding: arrivals at 1, 2, ..., 12 ms; a = 13 (13 ms) is after the horizon (12) and never scheduled.",
        "Each arrival is exactly one tick after the last: the one-tick floor (D-053) is met but never raises a tick. With every rate at most one a tick, Λ⁻¹(n+1) - Λ⁻¹(n) >= 1 tick exactly, so round half up gives strictly increasing ticks and the floor cannot bind for a deterministic schedule in exact arithmetic; it can only correct binary64's rounding (and bind for poisson draws, TestReleaseRunVectors).",
        "Straight to the sink: 12 events, 12 completions at the arrival ticks, lead time 0.",
    ],
    "expected": {
        "arrivals": ids("src", gr9), "starts": {}, "completions": ids("src", gr9),
        "blocks": [], "downs": [], "pending": [], "capacities": [], "events": 12,
        "result": {"completed_count": 12, "censored_entities": 0, "lead_times_minutes": [0] * 12, "queue_waits_minutes": {},
                   "resource_utilization": {}, "resource_states": None, "blocking": None, "buffers": None}},
    "simpy": "compare",
})

REFERENCE = {
    "tolerance": {"rule": "|mean_go - mean_simpy| <= z * sqrt(var_go/n + var_simpy/n) + rel * max(|mean_go|, |mean_simpy|) + abs",
                  "z": 4.0, "rel": 0.005, "abs": 1e-09},
    "metrics": ["completed_count", "mean_lead_time_minutes", "utilization/<resource>", "scheduled_utilization/<resource with a calendar>",
                "overtime_fraction/<resource with a calendar>", "idle_fraction/<resource with a calendar>",
                "available_fraction/<resource with a calendar>", "down_fraction/<resource with breakdowns>",
                "breakdowns_started/<resource with breakdowns>", "blocked_fraction/<resource> (finite buffers)",
                "full_fraction/<finite queue>", "blocks_started/<node>", "arrivals/<source with a schedule>/<segment or release>"],
    "cases": [],
}


def ref_case(cid, title, mdl, horizon=3000, warmup=300, seed=4901):
    REFERENCE["cases"].append({"id": cid, "title": title, "model": mdl,
                               "config": {"horizon": {"value": horizon, "unit": "minute"}, "warmup": {"value": warmup, "unit": "minute"},
                                          "replications": 30, "seed": seed, "trace_level": "none"}})


expo = lambda m: {"type": "exponential", "mean": m}  # noqa: E731
ref_case("C-S1", "one station, two shifts and a closed day shift, exponential arrivals and service",
         model("C-S1", [resource("m", 2, "two")], [source("src", expo(4)), process("work", expo(3), ("m", 1)), sink()],
               [("src", "work"), ("work", "done")], [calendar("two", 1440, [(360, 840), (840, 1320)], [(1800, 2280, False)])]))
ref_case("C-S2", "units on shift 3, 2, 1, a lognormal service and a three-segment poisson release schedule",
         model("C-S2", [resource("m", 3, "c", {"day": 3, "swing": 2, "night": 1})],
               [source("src", schedule={"process": "poisson", "period": 1440,
                                        "segments": [{"start": 0, "rate": 0.5}, {"start": 480, "rate": 0.4}, {"start": 960, "rate": 0.2}]}),
                process("work", {"type": "lognormal", "mean": 2.5, "cv": 0.6}, ("m", 1)), sink()],
               [("src", "work"), ("work", "done")], [calendar("c", 1440, [(0, 480, "day"), (480, 960, "swing"), (960, 1440, "night")])]),
         seed=4902)
ref_case("C-S3", "a station on a two-shift calendar that breaks down (Weibull TTF of on-shift time, lognormal TTR) behind a finite buffer",
         model("C-S3", [resource("u", 1), resource("m", 2, "c", ttf={"type": "weibull", "shape": 1.5, "scale": 300},
                                                   ttr={"type": "lognormal", "mean": 20, "cv": 0.5})],
               [source("src", expo(3)), process("prep", expo(2), ("u", 1)), queue("q", 5),
                process("work", {"type": "triangular", "min": 1, "mode": 2, "max": 4}, ("m", 1)), sink()],
               [("src", "prep"), ("prep", "q"), ("q", "work"), ("work", "done")], [calendar("c", 1440, [(0, 600), (720, 1320)])]),
         seed=4903)
ref_case("C-S4", "a deterministic schedule and a release table into two stations on different calendars with a finite buffer between",
         model("C-S4", [resource("r1", 2, "a"), resource("r2", 1, "b")],
               [source("src_a", schedule={"process": "deterministic", "period": 1440,
                                          "segments": [{"start": 0, "rate": 0.3}, {"start": 600, "rate": 0.1}]}),
                source("src_b", schedule={"process": "table", "period": 720, "releases": [{"at": 0, "quantity": 20}, {"at": 360, "quantity": 15}]}),
                process("s1", expo(4), ("r1", 1)), queue("q2", 10), process("s2", expo(1.5), ("r2", 1)), sink()],
               [("src_a", "s1"), ("src_b", "s1"), ("s1", "q2"), ("q2", "s2"), ("s2", "done")],
               [calendar("a", 1440, [(0, 1200)]), calendar("b", 1440, [(100, 1300)])]),
         seed=4904)

doc = {
    "status": "HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN",
    "semantics_decisions": ["D-047", "D-048", "D-050", "D-051", "D-052", "D-053", "D-064", "D-074", "D-081", "D-082"],
    "units": "arrivals, starts, completions, blocks, downs, capacities and lead times in the model's time unit (minutes, G-R5 seconds); Result members in ticks (1 ms)",
    "notes": "capacities are [resource, time, units on shift] of every Capacity observation in observation order (only resources whose S changes, D-052); capacity_seqs, where given, the sequence number of each; downs are [resource, unit (failure clock), from, to] in start order, to null when still down at the horizon or the stall; pending are [resource, unit, time] of the failures that waited (D-074 (a) B); service_start_seqs, where given, are [entity, time, sequence number of the dispatch that started the service] and events the replication's event count. Each case's derivation is the hand derivation of its expected values (assembled by checks/evidence/2026-10-25-T-049/calendar_goldens_gen.py, which only builds the JSON).",
    "cases": cases,
    "reference": REFERENCE,
}
OUT.write_text(json.dumps(doc, indent=1, ensure_ascii=False) + "\n")
print(f"wrote {OUT.relative_to(ROOT)}: {len(cases)} cases, {len(REFERENCE['cases'])} reference cases")
