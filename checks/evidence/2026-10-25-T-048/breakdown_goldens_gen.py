#!/usr/bin/env python3
"""Assembles testdata/simulation/breakdown-goldens.json (T-048).

It only builds JSON: every expected value below is typed in by hand from
the derivation written next to it (each case's "derivation"), following
ADR-026 D-048, D-050, D-051 and the T-048 interpretations (D-074, D-075,
PROPOSED). No engine, SimPy run or other program produced any of them.
Review round 1: D-074 (a) is option B (a repaired unit goes to the waiting
failures first); G-F3 was re-derived under it, and G-F9 (a repair satisfies
a waiting failure) and G-F1W10 (a downtime starting exactly at W) added.

    python checks/evidence/2026-10-25-T-048/breakdown_goldens_gen.py
"""
import copy
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / "testdata/simulation/breakdown-goldens.json"
BLOCKING = ROOT / "testdata/simulation/blocking-goldens.json"
MIN = 60_000  # ticks per model minute


def const(v):
    return {"type": "constant", "value": v}


def resource(rid, capacity=1, ttf=None, ttr=None):
    r = {"id": rid, "type": "machine", "name": rid, "capacity": capacity, "queue_discipline": "fifo"}
    if ttf is not None:
        r["breakdowns"] = {"time_to_failure": {"distribution": ttf}, "time_to_repair": {"distribution": ttr}}
    return r


def source(nid, dist, max_entities=None):
    n = {"id": nid, "type": "source", "entity_type": "board", "arrival": {"distribution": dist}}
    if max_entities:
        n["arrival"]["max_entities"] = max_entities
    return n


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


def model(name, resources, nodes, edges):
    return {"schema_version": "0.1", "model_id": "mdl_01M4YDQK00R942EDGA45T538HS", "domain": "synthetic_line", "name": name,
            "time_unit": "minute", "entity_types": [{"id": "board", "name": "Board"}], "resources": resources, "nodes": nodes,
            "flows": flows(*edges), "constraints": [], "metrics": [], "views": {"2d": {}, "3d": {}}, "metadata": {"synthetic": True}}


def config(horizon, warmup=0, replications=1, seed=7):
    return {"horizon": {"value": horizon, "unit": "minute"}, "warmup": {"value": warmup, "unit": "minute"},
            "replications": replications, "seed": seed, "trace_level": "none"}


def state(in_use, down, started, repairs, repair_ticks, available, idle, blocked=None, processing=None):
    """A resource_states entry in ticks; arguments in minutes."""
    s = {"in_use_ticks": round(in_use * MIN)}
    if blocked is not None:
        s["blocked_ticks"] = round(blocked * MIN)
    s.update({"down_ticks": round(down * MIN), "breakdowns_started": started, "repairs_completed": repairs,
              "completed_repair_ticks": round(repair_ticks * MIN), "available_ticks": round(available * MIN),
              "idle_ticks": round(idle * MIN)})
    s["scheduled_processing_ticks"] = round((processing if processing is not None else in_use - (blocked or 0)) * MIN)
    return s


cases = []

# ---------------------------------------------------------------- G-F1
cases.append({
    "id": "G-F1",
    "title": "a failure while the unit is idle (ADR-026 D-051 G-F1)",
    "model": model("G-F1", [resource("m", 1, const(10), const(3))],
                   [source("src", const(2)), process("work", const(1), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")]),
    "config": config(30),
    "derivation": [
        "t=0: src schedules its first arrival at 2; then m's unit 0 draws TTF 10: FAILURE at 10 (D-048: sources, then failure clocks).",
        "Arrivals every 2 min (e_k at 2k), each served 1 min: e1–e4 run [2,3], [4,5], [6,7], [8,9].",
        "t=10: ARRIVAL e5 (priority 10) queues and asks for a dispatch; FAILURE (15): idle = C - D - in_use = 1 - 0 - 0 = 1, so the unit goes down at once (D=1), TTR 3: REPAIR at 13; DISPATCH (20): e5 needs 1 > C - D = 0, held.",
        "t=12: e6 queues behind e5 (not a candidate); the dispatch grants nothing.",
        "t=13: REPAIR: D=0, next TTF 10: FAILURE at 23; the held front e5 wakes and the dispatch grants it: e5 [13,14]; e6 is the next candidate and is held (in use).",
        "t=14: e5 completes (0), releasing m and waking e6; ARRIVAL e7 (10) queues; DISPATCH: e6 [14,15], e7 held. t=15: e7 [15,16]. t=16: e7 done (0), e8 arrives (10): e8 [16,17].",
        "e9–e11 run [18,19], [20,21], [22,23].",
        "t=23: e11 completes (0); FAILURE (15): the unit is idle, down at once, REPAIR at 26.",
        "t=24: e12 held. t=26: ARRIVAL e13 (10) queues behind e12; REPAIR (15): D=0, FAILURE at 36 (> horizon, never scheduled); DISPATCH: e12 [26,27], e13 held. t=27: e13 [27,28]. t=28: e13 done, e14 [28,29]. t=30: e15 starts; its completion at 31 is after the horizon.",
        "Result over [0, 30] (T = 30 min): completed 14, censored 1 (e15); lead times 1,1,1,1,4,3,2,1,1,1,1,3,2,1; waits 0,0,0,0,3,2,1,0,0,0,0,2,1,0,0 (e15's 0 counts: it starts at 30);",
        "in use 14 min (e15's service has no length inside [0,30]): 840000 ticks, utilization 14/30; down [10,13] and [23,26]: 6 min; 2 breakdowns started, 2 complete repairs of 6 min; available = C*T - down = 24 min; idle = available - in use = 10 min (A - U >= 0 at every tick, U + D <= C).",
        "No finite buffer: m is the only resource and has breakdowns, so it is the only resource_states entry; no blocking or buffers members.",
    ],
    "expected": {
        "starts": {"work": [[f"r0/src/{k}", t] for k, t in enumerate([2, 4, 6, 8, 13, 14, 15, 16, 18, 20, 22, 26, 27, 28, 30], 1)]},
        "completions": [[f"r0/src/{k}", t] for k, t in enumerate([3, 5, 7, 9, 14, 15, 16, 17, 19, 21, 23, 27, 28, 29], 1)],
        "blocks": [],
        "downs": [["m", 0, 10, 13], ["m", 0, 23, 26]],
        "pending": [],
        "result": {
            "completed_count": 14, "censored_entities": 1,
            "lead_times_minutes": [1, 1, 1, 1, 4, 3, 2, 1, 1, 1, 1, 3, 2, 1],
            "queue_waits_minutes": {"work": [0, 0, 0, 0, 3, 2, 1, 0, 0, 0, 0, 2, 1, 0, 0]},
            "resource_utilization": {"m": 14 / 30},
            "resource_states": {"m": state(14, 6, 2, 2, 6, 24, 10)},
            "blocking": None, "buffers": None,
        },
    },
    "simpy": "not compared: same-tick arrival and failure at 10 and 26 (D-048 tie-break; SimPy orders same-time events by scheduling)",
})

# ---------------------------------------------------------------- G-F1W
gf1 = cases[0]
cases.append({
    "id": "G-F1W",
    "title": "G-F1 measured over [12, 30]: the downtime members count in the window (D-074 (e))",
    "model": copy.deepcopy(gf1["model"]),
    "config": config(30, warmup=12),
    "derivation": [
        "G-F1's run (same model, horizon and seed): the warm-up changes no event, only the window [W, H] = [12, 30], T = 18 min.",
        "Downtimes [10,13] and [23,26]: down_ticks = (13 - 12) + 3 = 4 min; breakdowns_started counts the downtimes that start in [12, 30]: only the one at 23, so 1;",
        "a complete repair starts at or after W and ends by H: [23,26] only (the one at 10 started before W), 1 repair of 3 min.",
        "In use in [12, 30]: e5 [13,14], e6 [14,15], e7 [15,16], e8 [16,17], e9 [18,19], e10 [20,21], e11 [22,23], e12 [26,27], e13 [27,28], e14 [28,29] (e15 starts at 30): 10 min; utilization 10/18.",
        "available = C*T - down = 18 - 4 = 14 min; idle = 14 - 10 = 4 min.",
        "Completed in [12, 30]: e5–e14 (14, 15, 16, 17, 19, 21, 23, 27, 28, 29), 10; censored e15.",
        "Lead-time cohort (arrival >= 12, completion <= 30): e6 3, e7 2, e8 1, e9 1, e10 1, e11 1, e12 3, e13 2, e14 1 (e5 arrived at 10).",
        "Queue waits (queue enter >= 12, start <= 30): e6 2 (12 to 14), e7 1, e8 0, e9 0, e10 0, e11 0, e12 2 (24 to 26), e13 1, e14 0, e15 0.",
    ],
    "expected": {
        "starts": gf1["expected"]["starts"],
        "completions": gf1["expected"]["completions"],
        "blocks": [],
        "downs": gf1["expected"]["downs"],
        "pending": [],
        "result": {
            "completed_count": 10, "censored_entities": 1,
            "lead_times_minutes": [3, 2, 1, 1, 1, 1, 3, 2, 1],
            "queue_waits_minutes": {"work": [2, 1, 0, 0, 0, 0, 2, 1, 0, 0]},
            "resource_utilization": {"m": 10 / 18},
            "resource_states": {"m": state(10, 4, 1, 1, 3, 14, 4)},
            "blocking": None, "buffers": None,
        },
    },
    "simpy": "not compared: G-F1's same-tick arrival and failure (D-048 tie-break)",
})

# ---------------------------------------------------------------- G-F1W10 (review round 1, M2)
cases.append({
    "id": "G-F1W10",
    "title": "G-F1 measured over [10, 30]: a downtime that starts exactly at W counts (D-074 (e); T-048 review round 1, M2)",
    "model": copy.deepcopy(gf1["model"]),
    "config": config(30, warmup=10),
    "derivation": [
        "G-F1's run (same model, horizon and seed): the warm-up changes no event, only the window [W, H] = [10, 30], T = 20 min. The first downtime starts at 10 = W.",
        "Downtimes [10,13] and [23,26]: down_ticks = 3 + 3 = 6 min; breakdowns_started counts the downtimes that start in [W, H] (W included): 10 and 23, so 2;",
        "a complete repair starts at or after W and ends by H: both, 2 repairs of 6 min. A strict lower bound (> W) would count 1 of each.",
        "In use in [10, 30]: e5 [13,14], e6 [14,15], e7 [15,16], e8 [16,17], e9 [18,19], e10 [20,21], e11 [22,23], e12 [26,27], e13 [27,28], e14 [28,29] (e4 ended at 9, e15 starts at 30): 10 min; utilization 10/20.",
        "available = C*T - down = 20 - 6 = 14 min; idle = 14 - 10 = 4 min.",
        "Completed in [10, 30]: e5–e14 (14, 15, 16, 17, 19, 21, 23, 27, 28, 29), 10; censored e15.",
        "Lead-time cohort (arrival >= 10, completion <= 30): e5 4 (10 to 14), e6 3, e7 2, e8 1, e9 1, e10 1, e11 1, e12 3, e13 2, e14 1.",
        "Queue waits (queue enter >= 10, start <= 30): e5 3 (10 to 13), e6 2, e7 1, e8 0, e9 0, e10 0, e11 0, e12 2, e13 1, e14 0, e15 0.",
    ],
    "expected": {
        "starts": gf1["expected"]["starts"],
        "completions": gf1["expected"]["completions"],
        "blocks": [],
        "downs": gf1["expected"]["downs"],
        "pending": [],
        "result": {
            "completed_count": 10, "censored_entities": 1,
            "lead_times_minutes": [4, 3, 2, 1, 1, 1, 1, 3, 2, 1],
            "queue_waits_minutes": {"work": [3, 2, 1, 0, 0, 0, 0, 2, 1, 0, 0]},
            "resource_utilization": {"m": 10 / 20},
            "resource_states": {"m": state(10, 6, 2, 2, 6, 14, 4)},
            "blocking": None, "buffers": None,
        },
    },
    "simpy": "not compared: G-F1's same-tick arrival and failure (D-048 tie-break)",
})

# ---------------------------------------------------------------- G-F2
cases.append({
    "id": "G-F2",
    "title": "a failure during service waits for the release (finish-first, ADR-026 D-051 G-F2)",
    "model": model("G-F2", [resource("m", 1, const(10), const(3))],
                   [source("src", const(3)), process("work", const(2), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")]),
    "config": config(40),
    "derivation": [
        "t=0: src's first arrival at 3; m unit 0: FAILURE at 10. Arrivals every 3 min, service 2: e1 [3,5], e2 [6,8], e3 [9,11].",
        "t=10: FAILURE: idle = 1 - 0 - 1 = 0, every unit in use: the failure waits (BreakdownPending), nothing goes down.",
        "t=11: e3 completes: the released unit satisfies the waiting failure first and goes down (D=1), TTR 3 from now: REPAIR at 14.",
        "t=12: e4 held (C - D = 0). t=14: REPAIR: D=0, FAILURE at 24; e4 wakes: e4 [14,16]. t=15: e5 held (in use). t=16: e5 [16,18]. t=18: e5 done (0), e6 arrives (10): e6 [18,20]. t=21: e7 [21,23].",
        "t=24: ARRIVAL e8 (10) queues; FAILURE (15): the unit is idle, down at once, REPAIR at 27; DISPATCH: e8 held.",
        "t=27: ARRIVAL e9 (10) queues; REPAIR (15): D=0, FAILURE at 37; DISPATCH: e8 [27,29], e9 held. t=29: e9 [29,31]. t=30: e10 held. t=31: e10 [31,33]. t=33: e10 done, e11 [33,35]. t=36: e12 [36,38].",
        "t=37: FAILURE: busy, waits. t=38: e12 completes, the unit goes down until 41 (REPAIR at 41 > horizon 40, never scheduled). t=39: e13 held to the horizon.",
        "Result over [0, 40]: 13 created, 12 completed, 1 censored (e13); lead times 2,2,2,4,3,2,2,5,4,3,2,2; waits 0,0,0,2,1,0,0,3,2,1,0,0;",
        "in use 12 x 2 = 24 min, utilization 0.6; down [11,14] 3 + [24,27] 3 + [38,40] 2 = 8 min; 3 breakdowns started, 2 complete repairs of 6 min (the third has not ended); available 32 min; idle 8 min.",
    ],
    "expected": {
        "starts": {"work": [[f"r0/src/{k}", t] for k, t in enumerate([3, 6, 9, 14, 16, 18, 21, 27, 29, 31, 33, 36], 1)]},
        "completions": [[f"r0/src/{k}", t] for k, t in enumerate([5, 8, 11, 16, 18, 20, 23, 29, 31, 33, 35, 38], 1)],
        "blocks": [],
        "downs": [["m", 0, 11, 14], ["m", 0, 24, 27], ["m", 0, 38, None]],
        "pending": [["m", 0, 10], ["m", 0, 37]],
        "result": {
            "completed_count": 12, "censored_entities": 1,
            "lead_times_minutes": [2, 2, 2, 4, 3, 2, 2, 5, 4, 3, 2, 2],
            "queue_waits_minutes": {"work": [0, 0, 0, 2, 1, 0, 0, 3, 2, 1, 0, 0]},
            "resource_utilization": {"m": 0.6},
            "resource_states": {"m": state(24, 8, 3, 2, 6, 32, 8)},
            "blocking": None, "buffers": None,
        },
    },
    "simpy": "not compared: same-tick arrival and failure at 24 (D-048 tie-break)",
})

# ---------------------------------------------------------------- G-F3
g_f3_sources = [("s1", 8), ("s2", 18), ("s3", 19), ("s4", 23), ("s5", 28)]
cases.append({
    "id": "G-F3",
    "title": "two units, waiting failures, releases and repairs; a unit that becomes available goes to the waiting failures first (ADR-026 D-051 G-F3, D-074 (a) B; review round 1, M1)",
    "model": model("G-F3", [resource("m", 2, const(10), const(2))],
                   [source(s, const(t), 1) for s, t in g_f3_sources] + [process("work", const(7), ("m", 1)), sink()],
                   [(s, "work") for s, _ in g_f3_sources] + [("work", "done")]),
    "config": config(40),
    "derivation": [
        "Re-derived in T-048 review round 1 under D-074 (a) option B: a unit that becomes available, released by a job or repaired, goes to the waiting failures first, in the order they fired; only a unit no failure waits for wakes the held front.",
        "m has capacity 2: two failure clocks, units 0 and 1, both TTF 10 and TTR 2. Five one-off sources: s1 at 8, s2 at 18, s3 at 19, s4 at 23, s5 at 28; service 7.",
        "t=0: the five first arrivals (source ID order), then FAILURE(0) at 10 and FAILURE(1) at 10 (unit order, so 0's has the smaller sequence).",
        "t=8: s1/1 [8,15]. t=10: FAILURE(0): idle = 2 - 0 - 1 = 1: unit down (D=1), REPAIR(0) at 12; FAILURE(1): idle = 2 - 1 - 1 = 0: waits.",
        "t=12: REPAIR(0): D=0, FAILURE(0) at 22; the repaired unit goes to the waiting failure 1: down (D=1), REPAIR(1) at 14. (Under option A it would wait for s1/1's release at 15.)",
        "t=14: REPAIR(1): D=0, FAILURE(1) at 24; no failure waits, no request is held. t=15: s1/1 completes; nothing waits.",
        "t=18: s2/1 [18,25]. t=19: s3/1 [19,26] (both units in use). t=22: FAILURE(0): idle 0, waits. t=23: s4/1 held (in use 2). t=24: FAILURE(1): idle 0, waits behind failure 0.",
        "t=25: s2/1 completes (0): its unit goes down for the first waiting failure, 0 (D=1), REPAIR(0) at 27; s4/1 wakes, but in use 1 (s3/1) + 1 > C - D = 2 - 1: held.",
        "t=26: s3/1 completes: its unit goes down for failure 1 (D=2), REPAIR(1) at 28; s4/1 wakes, in use 0 + 1 > 2 - 2: held.",
        "t=27: REPAIR(0): D=1, FAILURE(0) at 37; no failure waits, so the unit wakes s4/1: 0 + 1 <= 2 - 1: s4/1 [27,34].",
        "t=28: ARRIVAL s5/1 (10) queues and is a candidate; REPAIR(1) (15): D=0, FAILURE(1) at 38, wakes nothing (s5/1 is not held); DISPATCH (20): s5/1 [28,35].",
        "t=34: s4/1 completes; t=35: s5/1 completes; nothing waits. t=37: FAILURE(0): idle 2, down, REPAIR(0) at 39. t=38: FAILURE(1): idle 1, down, REPAIR(1) at 40. t=39: REPAIR(0), FAILURE(0) at 49 (> horizon). t=40: REPAIR(1) (at the horizon), FAILURE(1) at 50 (> horizon).",
        "Result over [0, 40]: 5 completed, lead times 7,7,7,11,7, waits 0,0,0,4,0; in use 5 x 7 = 35 min, utilization 35/80; down [10,12], [12,14], [25,27], [26,28], [37,39], [38,40]: 12 min (both units down on [26,27]), 6 started, 6 complete repairs of 12 min; available = 2*40 - 12 = 68 min; idle = 68 - 35 = 33 min.",
    ],
    "expected": {
        "starts": {"work": [["r0/s1/1", 8], ["r0/s2/1", 18], ["r0/s3/1", 19], ["r0/s4/1", 27], ["r0/s5/1", 28]]},
        "completions": [["r0/s1/1", 15], ["r0/s2/1", 25], ["r0/s3/1", 26], ["r0/s4/1", 34], ["r0/s5/1", 35]],
        "blocks": [],
        "downs": [["m", 0, 10, 12], ["m", 1, 12, 14], ["m", 0, 25, 27], ["m", 1, 26, 28], ["m", 0, 37, 39], ["m", 1, 38, 40]],
        "pending": [["m", 1, 10], ["m", 0, 22], ["m", 1, 24]],
        "result": {
            "completed_count": 5, "censored_entities": 0,
            "lead_times_minutes": [7, 7, 7, 11, 7],
            "queue_waits_minutes": {"work": [0, 0, 0, 4, 0]},
            "resource_utilization": {"m": 35 / 80},
            "resource_states": {"m": state(35, 12, 6, 6, 12, 68, 33)},
            "blocking": None, "buffers": None,
        },
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-F9 (review round 1, N8)
cases.append({
    "id": "G-F9",
    "title": "a repair satisfies a waiting failure (D-074 (a) B; the reviewer's N8, T-048 review round 1, M1)",
    "model": model("G-F9", [resource("m", 2, const(10), const(5))],
                   [source("s1", const(1), 1), process("work", const(100), ("m", 1)), sink()],
                   [("s1", "work"), ("work", "done")]),
    "config": config(60),
    "derivation": [
        "m has capacity 2, both clocks TTF 10 and TTR 5; one entity arrives at 1 and is served for 100 min, so it holds one unit to the horizon (its completion at 101 is not scheduled). Option B: a repaired unit goes to the waiting failures first.",
        "t=0: s1's arrival at 1, then FAILURE(0) and FAILURE(1) at 10. t=1: s1/1 queues, the dispatch grants it: in use 1.",
        "t=10: FAILURE(0): idle = 2 - 0 - 1 = 1: down (D=1), REPAIR(0) at 15; FAILURE(1): idle = 2 - 1 - 1 = 0: waits.",
        "t=15: REPAIR(0): D=0, FAILURE(0) at 25; the repaired unit goes to the waiting failure 1: down (D=1), REPAIR(1) at 20.",
        "t=20: REPAIR(1): D=0, FAILURE(1) at 30. t=25: FAILURE(0): idle 1: down, REPAIR(0) at 30.",
        "t=30: FAILURE(1) (scheduled at 20) runs before REPAIR(0) (scheduled at 25): idle = 2 - 1 - 1 = 0, waits; REPAIR(0): D=0, FAILURE(0) at 40, the unit goes to failure 1: down, REPAIR(1) at 35.",
        "The pattern repeats every 15 min: t=35 REPAIR(1), FAILURE(1) at 45; t=40 FAILURE(0) down, REPAIR(0) at 45; t=45 FAILURE(1) waits, REPAIR(0) gives it the unit (REPAIR(1) at 50), FAILURE(0) at 55; t=50 REPAIR(1), FAILURE(1) at 60; t=55 FAILURE(0) down, REPAIR(0) at 60; t=60 FAILURE(1) waits, REPAIR(0) (FAILURE(0) at 70, not scheduled) gives it the unit, whose REPAIR(1) at 65 is after the horizon.",
        "Downs: [0,10,15], [1,15,20], [0,25,30], [1,30,35], [0,40,45], [1,45,50], [0,55,60], [1,60,-]; waiting failures: clock 1 at 10, 30, 45, 60 (the last three wait 0 minutes: the repair comes later in the same tick).",
        "Result over [0, 60] (T = 60, C = 2): in use 59 min (s1/1 from 1); D = 1 on [10,20], [25,35], [40,50], [55,60]: down 35 min; 8 downtimes started (the one at 60 too); 7 complete repairs of 35 min (the one from 60 is not repaired by H); available = 120 - 35 = 85 min; idle = 85 - 59 = 26 min; utilization 59/120. Nothing completes: no lead time; s1/1's queue wait 0.",
        "Under option A (the repaired unit does not go to the waiting failure, review N8): failure 1 waits from 10 to the horizon while clock 0 fails at 10, 25, 40, 55: down 20 min, 4 started, idle 41 min.",
    ],
    "expected": {
        "starts": {"work": [["r0/s1/1", 1]]},
        "completions": [],
        "blocks": [],
        "downs": [["m", 0, 10, 15], ["m", 1, 15, 20], ["m", 0, 25, 30], ["m", 1, 30, 35], ["m", 0, 40, 45], ["m", 1, 45, 50], ["m", 0, 55, 60], ["m", 1, 60, None]],
        "pending": [["m", 1, 10], ["m", 1, 30], ["m", 1, 45], ["m", 1, 60]],
        "result": {
            "completed_count": 0, "censored_entities": 1,
            "lead_times_minutes": [],
            "queue_waits_minutes": {"work": [0]},
            "resource_utilization": {"m": 59 / 120},
            "resource_states": {"m": state(59, 35, 8, 7, 35, 85, 26)},
            "blocking": None, "buffers": None,
        },
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-F7
cases.append({
    "id": "G-F7",
    "title": "finish-first and idle failures without a same-tick arrival (for SimPy)",
    "model": model("G-F7", [resource("m", 1, const(9.5), const(2.5))],
                   [source("src", const(4)), process("work", const(3), ("m", 1)), sink()],
                   [("src", "work"), ("work", "done")]),
    "config": config(40),
    "derivation": [
        "Arrivals every 4 min (e_k at 4k), service 3, m: TTF 9.5, TTR 2.5. t=0: first arrival at 4, FAILURE at 9.5.",
        "e1 [4,7], e2 [8,11]. t=9.5: FAILURE: busy, waits. t=11: e2's release: down until 13.5. t=12: e3 held. t=13.5: REPAIR, FAILURE at 23; e3 [13.5,16.5].",
        "t=16: e4 held (busy). t=16.5: e4 [16.5,19.5]. t=20: e5 [20,23].",
        "t=23: e5 completes (0); FAILURE (15): the unit is idle, down until 25.5. t=24: e6 held. t=25.5: REPAIR, FAILURE at 35; e6 [25.5,28.5]. t=28: e7 held; t=28.5: e7 [28.5,31.5]. t=32: e8 [32,35].",
        "t=35: e8 completes; FAILURE: idle, down until 37.5. t=36: e9 held. t=37.5: REPAIR, FAILURE at 47 (> horizon); e9 [37.5,40.5]. t=40: e10 held.",
        "Result over [0, 40]: 10 created, 8 completed, 2 censored; lead times 3,3,4.5,3.5,3,4.5,3.5,3; waits 0,0,1.5,0.5,0,1.5,0.5,0,1.5 (e9 starts by 40);",
        "in use 8 x 3 + 2.5 (e9's part) = 26.5 min, utilization 26.5/40; down 3 x 2.5 = 7.5 min, 3 started, 3 complete; available 32.5 min; idle 6 min.",
        "The only same-tick pairs are a release and a failure (23, 35), which SimPy and the kernel resolve alike (the failure takes the released unit either way).",
    ],
    "expected": {
        "starts": {"work": [[f"r0/src/{k}", t] for k, t in enumerate([4, 8, 13.5, 16.5, 20, 25.5, 28.5, 32, 37.5], 1)]},
        "completions": [[f"r0/src/{k}", t] for k, t in enumerate([7, 11, 16.5, 19.5, 23, 28.5, 31.5, 35], 1)],
        "blocks": [],
        "downs": [["m", 0, 11, 13.5], ["m", 0, 23, 25.5], ["m", 0, 35, 37.5]],
        "pending": [["m", 0, 9.5]],
        "result": {
            "completed_count": 8, "censored_entities": 2,
            "lead_times_minutes": [3, 3, 4.5, 3.5, 3, 4.5, 3.5, 3],
            "queue_waits_minutes": {"work": [0, 0, 1.5, 0.5, 0, 1.5, 0.5, 0, 1.5]},
            "resource_utilization": {"m": 26.5 / 40},
            "resource_states": {"m": state(26.5, 7.5, 3, 3, 7.5, 32.5, 6)},
            "blocking": None, "buffers": None,
        },
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-F8
cases.append({
    "id": "G-F8",
    "title": "a bundle with a resource that breaks down and one that does not; only the first is in resource_states",
    "model": model("G-F8", [resource("m", 1, const(5), const(2)), resource("o", 1)],
                   [source("src", const(3)), process("work", const(2), ("m", 1), ("o", 1)), sink()],
                   [("src", "work"), ("work", "done")]),
    "config": config(15),
    "derivation": [
        "work needs m (breaks down: TTF 5, TTR 2) and o; arrivals every 3, service 2. t=0: first arrival at 3, FAILURE(m) at 5.",
        "t=3: e1 [3,5]. t=5: e1 completes (0), releasing m and o; FAILURE (15): m is idle, down until 7.",
        "t=6: e2 is the candidate at the front of m and o; m has C - D = 0: held (all or nothing: o is not taken). t=7: REPAIR: FAILURE at 12; e2 [7,9].",
        "t=9: e2 completes (0), e3 arrives (10): e3 [9,11]. t=12: ARRIVAL e4 (10) queues, FAILURE (15) takes the idle m until 14, the dispatch holds e4. t=14: REPAIR, FAILURE at 19; e4 [14,16]. t=15: e5 held.",
        "Result over [0, 15]: 5 created, 3 completed, 2 censored; lead times 2, 3, 2; waits 0, 1, 0, 2; in use of m and of o 2+2+2+1 = 7 min each, utilization 7/15;",
        "m: down 2 + 2 = 4 min, 2 started, 2 complete; available 11 min; idle 4 min. o has no line-flow feature (no breakdowns, no finite buffer in the model): no resource_states entry (D-058).",
    ],
    "expected": {
        "starts": {"work": [["r0/src/1", 3], ["r0/src/2", 7], ["r0/src/3", 9], ["r0/src/4", 14]]},
        "completions": [["r0/src/1", 5], ["r0/src/2", 9], ["r0/src/3", 11]],
        "blocks": [],
        "downs": [["m", 0, 5, 7], ["m", 0, 12, 14]],
        "pending": [],
        "result": {
            "completed_count": 3, "censored_entities": 2,
            "lead_times_minutes": [2, 3, 2],
            "queue_waits_minutes": {"work": [0, 1, 0, 2]},
            "resource_utilization": {"m": 7 / 15, "o": 7 / 15},
            "resource_states": {"m": state(7, 4, 2, 2, 4, 11, 4)},
            "blocking": None, "buffers": None,
        },
    },
    "simpy": "refused: work's bundle has m, which breaks down, and o; taken one resource at a time it would not be all or nothing",
})

# ---------------------------------------------------------------- G-T1
cases.append({
    "id": "G-T1",
    "title": "a unit released at t is taken by a failure at t; the waiting request is not granted at t (D-048 golden 1)",
    "model": model("G-T1", [resource("m", 1, const(10), const(2))],
                   [source("s1", const(5), 1), source("s2", const(7), 1), process("work", const(5), ("m", 1)), sink()],
                   [("s1", "work"), ("s2", "work"), ("work", "done")]),
    "config": config(20),
    "derivation": [
        "s1/1 arrives at 5 and runs [5,10]; s2/1 arrives at 7 and is held. m: FAILURE at 10, TTR 2.",
        "t=10: SERVICE_COMPLETE (0) releases m (no waiting failure yet) and wakes s2/1; FAILURE (15) finds the unit idle and takes it (down until 12); DISPATCH (20): s2/1 needs 1 > C - D = 0: not granted at 10.",
        "t=12: REPAIR, FAILURE at 22 (> horizon); s2/1 [12,17].",
        "Result over [0, 20]: 2 completed, lead times 5, 10, waits 0, 5; in use 10 min, utilization 0.5; down 2 min, 1 started, 1 complete; available 18 min; idle 8 min.",
    ],
    "expected": {
        "starts": {"work": [["r0/s1/1", 5], ["r0/s2/1", 12]]},
        "completions": [["r0/s1/1", 10], ["r0/s2/1", 17]],
        "blocks": [],
        "downs": [["m", 0, 10, 12]],
        "pending": [],
        "result": {
            "completed_count": 2, "censored_entities": 0,
            "lead_times_minutes": [5, 10],
            "queue_waits_minutes": {"work": [0, 5]},
            "resource_utilization": {"m": 0.5},
            "resource_states": {"m": state(10, 2, 1, 1, 2, 18, 8)},
            "blocking": None, "buffers": None,
        },
    },
    "simpy": "not compared: D-048 tie-break (release, failure and dispatch in one tick)",
})

# ---------------------------------------------------------------- G-T2
cases.append({
    "id": "G-T2",
    "title": "an arrival and a repair in the same tick: the dispatch grants the arrival (D-048 golden 2)",
    "model": model("G-T2", [resource("m", 1, const(3), const(2))],
                   [source("s1", const(5), 1), process("work", const(1), ("m", 1)), sink()],
                   [("s1", "work"), ("work", "done")]),
    "config": config(10),
    "derivation": [
        "m: FAILURE at 3 (idle: down until 5). s1/1 arrives at 5.",
        "t=5: ARRIVAL (10) queues and asks for a dispatch; REPAIR (15): D=0, FAILURE at 8; s1/1 is a candidate, not held, so the repair wakes nothing; DISPATCH (20): s1/1 [5,6].",
        "t=8: FAILURE: idle, down until 10. t=10: REPAIR (at the horizon, processed), FAILURE at 13 (> horizon).",
        "Result over [0, 10]: 1 completed, lead time 1, wait 0; in use 1 min, utilization 0.1; down [3,5] and [8,10]: 4 min, 2 started, 2 complete; available 6 min; idle 5 min.",
    ],
    "expected": {
        "starts": {"work": [["r0/s1/1", 5]]},
        "completions": [["r0/s1/1", 6]],
        "blocks": [],
        "downs": [["m", 0, 3, 5], ["m", 0, 8, 10]],
        "pending": [],
        "result": {
            "completed_count": 1, "censored_entities": 0,
            "lead_times_minutes": [1],
            "queue_waits_minutes": {"work": [0]},
            "resource_utilization": {"m": 0.1},
            "resource_states": {"m": state(1, 4, 2, 2, 4, 6, 5)},
            "blocking": None, "buffers": None,
        },
    },
    "simpy": "not compared: D-048 tie-break",
})

# ---------------------------------------------------------------- G-T4
blocking = json.loads(BLOCKING.read_text())
gb1 = next(c for c in blocking["cases"] if c["id"] == "G-B1")
gt4_model = model("G-T4", [resource("ma", 1, const(3), const(0)), resource("rb", 1)],
                  [source("src", const(1)), process("a", const(1), ("ma", 1)), queue("qb", 1),
                   process("b", const(3), ("rb", 1)), sink()],
                  [("src", "a"), ("a", "qb"), ("qb", "b"), ("b", "done")])
gb1_result = copy.deepcopy(gb1["expected"]["result"])
gt4_result = copy.deepcopy(gb1_result)
gt4_result["queue_waits_minutes"] = gb1_result["queue_waits_minutes"]
gt4_result["resource_utilization"] = {"ma": gb1_result["resource_utilization"]["ra"], "rb": gb1_result["resource_utilization"]["rb"]}
ra = gb1_result["resource_states"]["ra"]
gt4_result["resource_states"] = {
    "ma": state(ra["in_use_ticks"] / MIN, 0, 6, 6, 0, 20, 20 - ra["in_use_ticks"] / MIN, blocked=ra["blocked_ticks"] / MIN),
    "rb": gb1_result["resource_states"]["rb"],
}
cases.append({
    "id": "G-T4",
    "title": "a 0-tick repair scheduled during a dispatch runs after it and dispatches again in the same tick (D-048 golden 4)",
    "model": gt4_model,
    "config": gb1["config"],
    "derivation": [
        "G-B1 (src every 1, a 1 min, qb capacity 1, b 3 min) with breakdowns on a's resource ma: TTF 3, TTR 0. Without breakdowns G-B1's facts hold: a starts 1,2,3,5,8,11,14,17,20; b starts 2,5,8,11,14,17,20; blocks at a [4,5], [6,8], [9,11], [12,14], [15,17], [18,20].",
        "t=3: e2 leaves a (0) for qb, e3 arrives (10); FAILURE (15): ma is idle, down with TTR 0: REPAIR at 3 (15, later sequence), which runs before the dispatch (20): ma is up again, e3 [3,4]. A 0-length downtime [3,3].",
        "t=4: e3 is blocked at a (qb full), keeping ma; e4 held. t=5: b grants e2, unblocking e3 (ma released, nothing waiting) and e4 [5,6].",
        "t=6: e4 is blocked at a holding ma; FAILURE (15): idle = 1 - 0 - 1 = 0: waits (pending).",
        "t=8: e2 leaves b (0); e8 arrives (10); DISPATCH (20): b grants e3 [8,11]; qb has room: e4 is unblocked and releases ma, whose unit goes down for the waiting failure (D=1) with TTR 0: REPAIR at 8, priority 15, scheduled during the dispatch; e5, the front of ma, wakes and is held (C - D = 0); the dispatch ends.",
        "t=8, after it: REPAIR (15): D=0, FAILURE at 11; it wakes e5 and schedules a second DISPATCH at 8 (20), which grants e5 [8,9]: the tick does not go back, and e5 starts at 8 as in G-B1.",
        "The same at 11, 14, 17 and 20: the failure is due while ma is held by the blocked entity, waits, takes ma when b's grant unblocks it, and is repaired in the same tick, followed by a second dispatch. The 20 one is at the horizon (its next FAILURE at 23 is not scheduled).",
        "So starts, completions, blocks and every G-B1 Result member are G-B1's; ma adds 6 downtimes of 0 ticks (3, 8, 11, 14, 17, 20), 6 started and 6 complete (D-074 (d)), down 0, available C*T = 20 min, idle 20 - 19 = 1 min; rb has no breakdowns: only its blocked group, as in G-B1.",
    ],
    "expected": {
        "starts": gb1["expected"]["starts"],
        "completions": gb1["expected"]["completions"],
        "blocks": gb1["expected"]["blocks"],
        "downs": [["ma", 0, t, t] for t in (3, 8, 11, 14, 17, 20)],
        "pending": [["ma", 0, t] for t in (6, 11, 14, 17, 20)],
        "at_tick_8": [
            ["resource", "rb", 0], ["service_end", "r0/src/2", "b"], ["complete", "r0/src/2", "done"],
            ["arrival", "r0/src/8", "src"], ["queue_enter", "r0/src/8", "a"],
            ["resource", "rb", 1], ["service_start", "r0/src/3", "b"], ["resource", "ma", 0], ["down", "ma", 0],
            ["unblocked", "r0/src/4", "a"], ["queue_enter", "r0/src/4", "b"],
            "next event",
            ["up", "ma", 0],
            "next event",
            ["resource", "ma", 1], ["service_start", "r0/src/5", "a"],
        ],
        "result": gt4_result,
    },
    "simpy": "not compared: D-048 tie-break (a 0-tick repair inside a tick)",
})

# ---------------------------------------------------------------- G-K1
cases.append({
    "id": "G-K1",
    "title": "a held request that only a repair can let in is not a deadlock (B1 tells capacity-bound from release-bound; T-047 review round 2, I1)",
    "model": model("G-K1", [resource("m", 1, const(1), const(10))],
                   [source("src", const(2), 1), queue("q", 1), process("work", const(1), ("m", 1)), sink()],
                   [("src", "q"), ("q", "work"), ("work", "done")]),
    "config": config(15),
    "derivation": [
        "The finite buffer q makes the kernel run the D-050 B1 check; m breaks down at 1 (idle: down until 11).",
        "t=2: the only entity arrives and queues; the dispatch holds it (1 > C - D = 0). After the dispatch: nothing in service or in a delay, one held candidate: B1 asks whether it is release-bound, in_use + units > C, with the full capacity C: 0 + 1 > 1 is false, so it is capacity-bound (only a repair can let it in) and this is no deadlock. (With >= it would be reported as one: the mutant this golden kills.)",
        "B2 at the end of tick 2: held, none of its bundle in use: in that state since 2, TTRmax 10 (constant): due by 12.",
        "t=11: REPAIR: D=0, FAILURE at 12; the repair wakes the held request (capacity rose without a release) and dispatches: work [11,12].",
        "t=12: it completes (0); FAILURE (15): idle, down until 22 (REPAIR > horizon, not scheduled).",
        "Result over [0, 15]: 1 completed, lead time 10, q's wait 9; in use 1 min, utilization 1/15; m (the model has a finite buffer, so it lists blocked_ticks too): blocked 0, down 10 + 3 = 13 min, 2 started, 1 complete of 10 min, available 2 min, idle 1 min;",
        "blocking: src can reach the finite buffer q: 0 blocks; q full (its one place taken) [2,11]: 9 min.",
    ],
    "expected": {
        "starts": {"work": [["r0/src/1", 11]]},
        "completions": [["r0/src/1", 12]],
        "blocks": [],
        "downs": [["m", 0, 1, 11], ["m", 0, 12, None]],
        "pending": [],
        "result": {
            "completed_count": 1, "censored_entities": 0,
            "lead_times_minutes": [10],
            "queue_waits_minutes": {"q": [9]},
            "resource_utilization": {"m": 1 / 15},
            "resource_states": {"m": state(1, 13, 2, 1, 10, 2, 1, blocked=0)},
            "blocking": {"src": {"blocks_started": 0, "blocks_completed": 0, "completed_block_ticks": 0, "blocked_entity_ticks": 0}},
            "buffers": {"q": {"full_ticks": 9 * MIN}},
        },
    },
    "simpy": "not compared: the point is the kernel's B1 check",
})

# ---------------------------------------------------------------- G-D4
gd2 = next(c for c in blocking["cases"] if c["id"] == "G-D2")
gd4_model = copy.deepcopy(gd2["model"])
gd4_model["name"] = "G-D4"
gd4_model["resources"].append(resource("x", 1, const(2), const(1)))
cases.append({
    "id": "G-D4",
    "title": "G-D2 with a resource that breaks down beside the cycle: B1 still finds the circular wait at 7 (R1 bypassed, test-only)",
    "model": gd4_model,
    "config": gd2["config"],
    "validator": gd2["validator"],
    "derivation": [
        "G-D2's model and timeline (R1 bypassed in the test only), plus resource x (TTF 2, TTR 1) that no process needs: x's FAILURE and REPAIR events (2, 3, 5, 6; next failure at 8) touch no queue and wake nothing.",
        "x's events at 2 and 5 come after the same tick's arrivals, which have a dispatch pending, so no check runs after them; at 3 and 6 the check runs and finds entities in service.",
        "t=7: the SERVICE_COMPLETE of r0/src/3 at s1 blocks it; nothing is in service or in a delay; B1 reports the same deadlock as G-D2: tick 420000, r0/src/5 blocked at src since 300000.",
    ],
    "expected": {
        "starts": gd2["expected"]["starts"],
        "completions": gd2["expected"]["completions"],
        "blocks": gd2["expected"]["blocks"],
        "downs": [["x", 0, 2, 3], ["x", 0, 5, 6]],
        "pending": [],
        "deadlock": gd2["expected"]["deadlock"],
    },
    "simpy": "not compared: SimPy does not detect deadlocks (G-D2)",
})

# ---------------------------------------------------------------- G-S1
stall_reason = "the replication stalled at tick 1800000: r0/sx/1 waits for node x since tick 1200000 with none of its bundle in use; it was due by tick 1800000"
cases.append({
    "id": "G-S1",
    "title": "B2: a bundle that can never be filled (R3 bypassed, test-only: 2 units of a resource that breaks down; ADR-026 D-050's second counterexample)",
    "model": model("G-S1", [resource("r", 2, const(10), const(10))],
                   [source("sx", const(6), 1), process("x", const(1), ("r", 2)),
                    source("sy", const(5), 1), process("y", const(15), ("r", 1)), sink()],
                   [("sx", "x"), ("sy", "y"), ("x", "done"), ("y", "done")]),
    "config": config(60),
    "validator": {"codes_at": [["RESOURCE_REQUIREMENT_INVALID", "/nodes/1/requires/0/units"]]},
    "derivation": [
        "r has capacity 2, TTF 10 and TTR 10 per unit; x needs both units (R3 refuses it; the test bypasses the validator), y one. sx/1 arrives at 6, sy/1 at 5. t=0: arrivals of sx (6) and sy (5) in ID order, then FAILURE(0) and FAILURE(1) at 10.",
        "t=5: sy/1 [5,20]. t=6: sx/1 is the front of r's FIFO and is held (1 + 2 > 2).",
        "t=10: FAILURE(0): idle = 2 - 0 - 1 = 1: down until 20; FAILURE(1): idle 0: waits.",
        "t=20: sy/1 completes (0): its unit goes down for failure 1 (D=2), REPAIR(1) at 30; sx/1 wakes. REPAIR(0) (15): D=1, FAILURE(0) at 30. DISPATCH: 0 + 2 > 2 - 1: held. End of tick 20: sx/1 is held with no unit of its bundle in use: the B2 state since 20; TTRmax = 10: due by 30.",
        "t=30: REPAIR(1) (scheduled at 20 before FAILURE(0)) runs first: D=0, FAILURE(1) at 40, sx/1 wakes; FAILURE(0): idle 2: down (D=1) until 40; DISPATCH: held again. The two clocks are now 10 apart: one unit is always down, for ever.",
        "End of tick 30 (no other event at 30): sx/1 is still in the state and 30 >= 30: ErrStalled at 30 min (1800000), r0/sx/1 at x, since 1200000, due by 1800000.",
    ],
    "expected": {
        "starts": {"y": [["r0/sy/1", 5]]},
        "completions": [["r0/sy/1", 20]],
        "blocks": [],
        "downs": [["r", 0, 10, 20], ["r", 1, 20, 30], ["r", 0, 30, None]],
        "pending": [["r", 1, 10]],
        "stalled": {"tick": 30 * MIN, "entity": "r0/sx/1", "node": "x", "since": 20 * MIN, "deadline": 30 * MIN, "reason": stall_reason},
    },
    "simpy": "not compared: SimPy does not detect stalls",
})

# ---------------------------------------------------------------- G-S2
gs2 = copy.deepcopy(cases[0]["model"])
gs2["name"] = "G-S2"
gs2["nodes"].insert(0, source("sd", const(13), 1))
gs2["flows"].append({"id": "sd__done", "from": "sd", "to": "done", "routing": {"type": "probability", "probability": 1}})
gf1 = cases[0]["expected"]
cases.append({
    "id": "G-S2",
    "title": "no stall at the deadline tick when the repair comes later in that tick (B2 at the end of a tick, D-074 (c))",
    "model": gs2,
    "config": config(30),
    "derivation": [
        "G-F1 plus a one-off source sd at 13 that goes straight to done (no queue, no dispatch). sd sorts before src, so the first arrivals are scheduled sd, src, then m's FAILURE at 10; G-F1's timeline is unchanged.",
        "e5 (r0/src/5) is held from 10 with nothing of its bundle in use: the B2 state since 10, TTRmax 3, due by 13.",
        "t=13: ARRIVAL sd/1 (10) completes at once, without a dispatch. A B2 check right after it would see 13 >= 13 and report a stall; but REPAIR (15) is still due in tick 13: it wakes e5 and the dispatch grants it [13,14]. B2 checks only at the end of a tick, so nothing is reported.",
        "Facts and Result: G-F1's, plus sd/1 completed at 13 with lead time 0 (15 completed, 16 created, 1 censored).",
    ],
    "expected": {
        "starts": gf1["starts"],
        "completions": gf1["completions"][:4] + [["r0/sd/1", 13]] + gf1["completions"][4:],
        "blocks": [],
        "downs": gf1["downs"],
        "pending": [],
        "result": {**gf1["result"], "completed_count": 15, "lead_times_minutes": gf1["result"]["lead_times_minutes"][:4] + [0] + gf1["result"]["lead_times_minutes"][4:]},
    },
    "simpy": "not compared: the point is the kernel's B2 check",
})

# ---------------------------------------------------------------- reference (SimPy, stochastic)
def exp_(m):
    return {"type": "exponential", "mean": m}


reference = {
    "tolerance": blocking["reference"]["tolerance"],
    "metrics": ["completed_count", "mean_lead_time_minutes", "utilization/<resource>", "down_fraction/<resource with breakdowns>",
                "available_fraction/<resource with breakdowns>", "idle_fraction/<resource with breakdowns>",
                "breakdowns_started/<resource with breakdowns>", "blocked_fraction/<resource> (finite buffers)",
                "full_fraction/<finite queue>", "blocks_started/<node>"],
    "cases": [
        {"id": "B-S1", "title": "one station, exponential arrivals, service, TTF and TTR",
         "model": model("B-S1", [resource("m", 1, exp_(60), exp_(6))],
                        [source("src", exp_(5)), process("work", exp_(3.5), ("m", 1)), sink()],
                        [("src", "work"), ("work", "done")]),
         "config": config(3000, 300, 30, 4801)},
        {"id": "B-S2", "title": "three units, Weibull TTF, lognormal TTR and lognormal service",
         "model": model("B-S2", [resource("m", 3, {"type": "weibull", "shape": 1.5, "scale": 200}, {"type": "lognormal", "mean": 15, "cv": 0.5})],
                        [source("src", exp_(3)), process("work", {"type": "lognormal", "mean": 6, "cv": 0.8}, ("m", 1)), sink()],
                        [("src", "work"), ("work", "done")]),
         "config": config(3000, 300, 30, 4802)},
        {"id": "B-S3", "title": "breakdowns before a finite buffer: failures wait while a blocked entity holds the unit",
         "model": model("B-S3", [resource("ma", 1, exp_(100), const(10)), resource("rb", 1)],
                        [source("src", exp_(3)), process("a", exp_(2), ("ma", 1)), queue("qb", 2), process("b", exp_(2.5), ("rb", 1)), sink()],
                        [("src", "a"), ("a", "qb"), ("qb", "b"), ("b", "done")]),
         "config": config(3000, 300, 30, 4803)},
        {"id": "B-S4", "title": "a resource that breaks down, shared by two processes; lognormal TTF, Weibull TTR and service",
         "model": model("B-S4", [resource("m", 2, {"type": "lognormal", "mean": 120, "cv": 1}, {"type": "weibull", "shape": 2, "scale": 8})],
                        [source("src", exp_(2)), process("p1", {"type": "weibull", "shape": 2, "scale": 1.5}, ("m", 1)),
                         process("p2", {"type": "triangular", "min": 0.5, "mode": 1, "max": 2}, ("m", 1)), sink()],
                        [("src", "p1"), ("p1", "p2"), ("p2", "done")]),
         "config": config(3000, 300, 30, 4804)},
    ],
}

doc = {
    "status": "HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN",
    "semantics_decisions": ["D-047", "D-048", "D-050", "D-051", "D-056", "D-074", "D-075"],
    "units": "starts, completions, blocks, downs and lead times in model minutes; Result members in ticks (1 ms)",
    "notes": "downs are [resource, unit (failure clock), from, to] in the order the downtimes started, to null when still down at the horizon (or at the stall); pending are [resource, unit, tick] of the failures that found every unit in use and waited for a release (finish-first, D-051). Each case's derivation is the hand derivation of its expected values (assembled by checks/evidence/2026-10-25-T-048/breakdown_goldens_gen.py, which only builds the JSON).",
    "cases": cases,
    "reference": reference,
}
OUT.write_text(json.dumps(doc, indent=2, ensure_ascii=False) + "\n")
print(f"wrote {OUT.relative_to(ROOT)}: {len(cases)} cases, {len(reference['cases'])} reference cases")
