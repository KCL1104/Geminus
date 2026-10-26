#!/usr/bin/env python3
"""Assembles testdata/simulation/batch-goldens.json (T-050, ADR-026 D-054).

This script only builds JSON. Every expected value below was derived by hand
from the case's derivation text (D-047, D-048, D-049, D-050, D-054 and the
T-050 interpretations D-096, PROPOSED), before the kernel ran the case; no
value is computed by a simulation. The case IDs are G-BT<n>: ADR-026 names
its batch goldens G-T1–G-T5, but G-T1, G-T2, G-T4 and G-T5 already name
D-048's same-tick goldens of T-048 and T-049 (breakdown-goldens.json,
calendar-goldens.json), so G-BT1–G-BT5 are ADR-026 D-054's G-T1–G-T5.

    python3 checks/evidence/2026-10-26-T-050/batch_goldens_gen.py > testdata/simulation/batch-goldens.json
"""
import json
import sys

MIN = 60_000  # ticks per minute


def const(v):
    return {"type": "constant", "value": v}


def res(rid, capacity=1, **extra):
    r = {"id": rid, "type": "machine", "name": rid, "capacity": capacity, "queue_discipline": "fifo"}
    r.update(extra)
    return r


def source(sid, every, max_entities=None, et="board"):
    a = {"distribution": every if isinstance(every, dict) else const(every)}
    if max_entities is not None:
        a["max_entities"] = max_entities
    return {"id": sid, "type": "source", "entity_type": et, "arrival": a}


def table_source(sid, period, rows, et="board"):
    return {"id": sid, "type": "source", "entity_type": et,
            "arrival": {"schedule": {"process": "table", "period": period,
                                     "releases": [{"at": at, "quantity": q} for at, q in rows]}}}


def process(pid, service, requires=(), batch=None):
    n = {"id": pid, "type": "process", "service_time": {"distribution": service if isinstance(service, dict) else const(service)},
         "requires": [{"resource_id": r, "units": u} for r, u in requires]}
    if batch is not None:
        b = {"min_size": batch[0], "max_size": batch[1]}
        if len(batch) > 2:
            b["max_wait"] = batch[2]
        n["batch"] = b
    return n


def queue(qid, capacity=None):
    q = {"id": qid, "type": "queue"}
    if capacity:
        q["capacity"] = capacity
    return q


def sink(sid, outcome="completed"):
    return {"id": sid, "type": "sink", "outcome": outcome}


def flow(a, b, p=1):
    return {"id": f"{a}__{b}", "from": a, "to": b, "routing": {"type": "probability", "probability": p}}


def model(name, resources, nodes, flows, calendars=None, entity_types=("board",)):
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


TOLERANCE = {"rule": "|mean_go - mean_simpy| <= z * sqrt(var_go/n + var_simpy/n) + rel * max(|mean_go|, |mean_simpy|) + abs",
             "z": 4.0, "rel": 0.005, "abs": 1e-09}

cases = []

# ---------------------------------------------------------------- G-BT1
src_work_done = [flow("src", "work"), flow("work", "done")]
bt1_model = model("G-BT1", [res("chamber")], [source("src", 10, 12), process("work", 30, [("chamber", 1)], (4, 4)), sink("done")],
                  src_work_done)
cases.append({
    "id": "G-BT1",
    "title": "full batches only (min = max = 4): a batch starts when its fourth member enters (ADR-026 D-054 G-T1)",
    "model": bt1_model,
    "config": config(160),
    "derivation": [
        "One chamber; src every 10 minutes, 12 entities (10, 20, ..., 120); work is a batch process with min_size = max_size = 4 and no max_wait, service 30 (constant, one draw per batch, which a constant does not consume).",
        "With min = max a wait limit could never start a batch, so no BATCH_TIMEOUT is ever scheduled (D-096 (b)); an entry that does not make the batch ready asks for no dispatch (D-096 (e)).",
        "t=0: src's first arrival at 10 (seq 1).",
        "t=10, 20, 30: e1, e2, e3 arrive (seq 1, 2, 3; each schedules the next arrival: seq 2, 3, 4) and enter work's buffer (1, 2, 3 waiting): not ready (fewer than max_size), no dispatch.",
        "t=40: e4 arrives (seq 4; next at 50, seq 5) and enters: 4 waiting = max_size, ready: the batch request takes its FIFO place (request sequence 1) and asks for a dispatch (seq 6). DISPATCH (seq 6): the chamber fits (0 + 1 <= 1): ObsResource, BatchStart b1 (4 members), ServiceStart e1, e2, e3, e4 at 40 (D-054's order); one SERVICE_COMPLETE at 70 (seq 7). The buffer is empty again.",
        "t=50, 60: e5, e6 (seq 5, 8; next arrivals seq 8, 9).",
        "t=70: SERVICE_COMPLETE (seq 7, priority 0): every member's route resolves to done, none is blocked: the chamber's release (ObsResource), the dispatch (seq 10), then per member ServiceEnd and Complete (e1-e4 complete at 70). ARRIVAL e7 (seq 9; next at 80, seq 11): 3 waiting. DISPATCH (seq 10): no candidate.",
        "t=80: e8 (seq 11; next at 90, seq 12): 4 waiting, ready (request sequence 2), DISPATCH (seq 13): BatchStart b2 (e5-e8) at 80, SERVICE_COMPLETE at 110 (seq 14).",
        "t=90, 100: e9, e10 (seq 12, 15; next seq 15, 16). t=110: SERVICE_COMPLETE (seq 14): release, DISPATCH (seq 17), e5-e8 complete; ARRIVAL e11 (seq 16; next at 120, seq 18); DISPATCH (seq 17): nothing.",
        "t=120: e12 (seq 18), the 12th: no next arrival (max_entities), no sequence taken. 4 waiting: ready, DISPATCH (seq 19): b3 (e9-e12) at 120, SERVICE_COMPLETE at 150 (seq 20).",
        "t=150: SERVICE_COMPLETE (seq 20): release, DISPATCH (seq 21), e9-e12 complete; DISPATCH (seq 21): nothing.",
        "21 events: 12 arrivals, 3 SERVICE_COMPLETEs, 6 DISPATCHes (40, 70, 80, 110, 120, 150). The batches start at 40, 80, 120 (D-054 G-T1), their BatchStarts in the DISPATCHes seq 6, 13, 19.",
        "Result over [0,160] (T = 160 min): 12 completed, none censored. Lead times per member (D-054: lead time per entity, from its arrival): 60, 50, 40, 30 in each batch; queue waits (from entering the buffer to the batch's start, the forming wait included): 30, 20, 10, 0 in each batch.",
        "Chamber in use [40,70), [80,110), [120,150): 90 of 160 minutes, utilization 0.5625 (busy chamber time, not slots). batches[work]: 3 started, 12 members. No resource feature applies to the chamber: no resource_states; no finite buffer: no blocking, no buffers.",
    ],
    "expected": {
        "starts": {"work": [[e("src", n), t] for n, t in [(1, 40), (2, 40), (3, 40), (4, 40), (5, 80), (6, 80), (7, 80), (8, 80),
                                                           (9, 120), (10, 120), (11, 120), (12, 120)]]},
        "completions": [[e("src", n), t] for n, t in [(1, 70), (2, 70), (3, 70), (4, 70), (5, 110), (6, 110), (7, 110), (8, 110),
                                                      (9, 150), (10, 150), (11, 150), (12, 150)]],
        "batches": [["work", 1, 40, [e("src", n) for n in (1, 2, 3, 4)]], ["work", 2, 80, [e("src", n) for n in (5, 6, 7, 8)]],
                    ["work", 3, 120, [e("src", n) for n in (9, 10, 11, 12)]]],
        "batch_start_seqs": [["work", 1, 6], ["work", 2, 13], ["work", 3, 19]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 21,
        "result": {"completed_count": 12, "censored_entities": 0,
                   "lead_times_minutes": [60, 50, 40, 30] * 3,
                   "queue_waits_minutes": {"work": [30, 20, 10, 0] * 3},
                   "resource_utilization": {"chamber": 0.5625},
                   "resource_states": None, "blocking": None, "buffers": None,
                   "batches": {"work": {"batches_started": 3, "members_started": 12}}},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-BT2
cases.append({
    "id": "G-BT2",
    "title": "the wait limit: a batch below max_size starts when its oldest has waited max_wait and min_size wait (ADR-026 D-054 G-T2)",
    "model": model("G-BT2", [res("chamber")], [source("src", 30, 4), process("work", 30, [("chamber", 1)], (2, 4, 25)), sink("done")],
                   src_work_done),
    "config": config(200),
    "derivation": [
        "One chamber; src every 30 minutes, 4 entities (30, 60, 90, 120); work: min_size 2, max_size 4, max_wait 25, service 30.",
        "t=0: the first arrival at 30 (seq 1).",
        "t=30: e1 (seq 1; next at 60, seq 2) enters the empty buffer: it is the oldest, its wait limit is at 30 + 25 = 55 > 30, so a BATCH_TIMEOUT at 55 (seq 3, priority 15; D-096 (c)). Not ready (1 < min_size).",
        "t=55: BATCH_TIMEOUT (seq 3), not stale (e1 is still the oldest): the oldest has waited 25 >= 25, but only 1 waits (< min_size 2): not ready (ADR-026 G-T2: at 55 one is not enough).",
        "t=60: e2 (seq 2; next at 90, seq 4) enters: 2 >= min_size and the oldest has waited 30 >= 25: ready (request sequence 1), DISPATCH (seq 5): b1 = (e1, e2), 60-90, SERVICE_COMPLETE at 90 (seq 6). The buffer is empty (a new timeout generation, no event).",
        "t=90: SERVICE_COMPLETE (seq 6, priority 0): release, DISPATCH (seq 7), e1, e2 complete. ARRIVAL e3 (seq 4, priority 10; next at 120, seq 8): the new oldest, BATCH_TIMEOUT at 115 (seq 9). DISPATCH (seq 7): nothing.",
        "t=115: BATCH_TIMEOUT (seq 9): 1 waits, not ready.",
        "t=120: e4 (seq 8), the last (no next arrival): 2 wait, the oldest since 90 (30 >= 25): ready, DISPATCH (seq 10): b2 = (e3, e4) 120-150, SERVICE_COMPLETE (seq 11).",
        "t=150: SERVICE_COMPLETE (seq 11): release, DISPATCH (seq 12), e3, e4 complete; DISPATCH: nothing.",
        "12 events: 4 arrivals, 2 BATCH_TIMEOUTs (55, 115), 4 DISPATCHes (60, 90, 120, 150), 2 SERVICE_COMPLETEs.",
        "Result over [0,200]: 4 completed; lead times e1 60, e2 30, e3 60, e4 30; queue waits 30, 0, 30, 0; chamber in use [60,90) and [120,150): 60/200 = 0.3; batches[work]: 2, 4 members.",
    ],
    "expected": {
        "starts": {"work": [[e("src", 1), 60], [e("src", 2), 60], [e("src", 3), 120], [e("src", 4), 120]]},
        "completions": [[e("src", 1), 90], [e("src", 2), 90], [e("src", 3), 150], [e("src", 4), 150]],
        "batches": [["work", 1, 60, [e("src", 1), e("src", 2)]], ["work", 2, 120, [e("src", 3), e("src", 4)]]],
        "batch_start_seqs": [["work", 1, 5], ["work", 2, 10]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 12,
        "result": {"completed_count": 4, "censored_entities": 0, "lead_times_minutes": [60, 30, 60, 30],
                   "queue_waits_minutes": {"work": [30, 0, 30, 0]}, "resource_utilization": {"chamber": 0.3},
                   "resource_states": None, "blocking": None, "buffers": None,
                   "batches": {"work": {"batches_started": 2, "members_started": 4}}},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-BT3
cases.append({
    "id": "G-BT3",
    "title": "two chambers, nine waiting, max_size 4: one dispatch starts two batches and leaves one (ADR-026 D-054 G-T3)",
    "model": model("G-BT3", [res("chamber", 2)], [table_source("src", 1000, [(10, 9)]), process("work", 30, [("chamber", 1)], (4, 4)),
                                                  sink("done")], src_work_done),
    "config": config(100),
    "derivation": [
        "Chamber capacity 2 (each batch needs 1 unit); src is a release table: 9 entities at 10 (period 1000, so the next row, at 1010, is after the horizon and is not scheduled); work: min = max = 4, service 30.",
        "t=0: the table's first row at 10 (seq 1).",
        "t=10: ARRIVAL (seq 1): the next row is after the horizon (no sequence); e1..e9 are created one after another (D-053), each routed at once into work's buffer. e4's entry makes the batch ready (4 = max_size): the batch request (request sequence 1) asks for a dispatch (seq 2); e5..e9 find a request already waiting (D-054: at most one ungranted batch request) and do nothing more.",
        "DISPATCH (seq 2): the request fits (0 + 1 <= 2): ObsResource (1), BatchStart b1 (e1-e4), SERVICE_COMPLETE at 40 (seq 3). Right after the grant the buffer is checked again (D-054): 5 wait, ready: a second request (request sequence 2) is at the front of the chamber's FIFO and a candidate in this dispatch (no new dispatch is asked during one). It fits (1 + 1 <= 2): ObsResource (2), BatchStart b2 (e5-e8), SERVICE_COMPLETE at 40 (seq 4). Checked again: e9 alone, not ready.",
        "t=40: SERVICE_COMPLETE b1 (seq 3): release (1), DISPATCH (seq 5), e1-e4 complete; SERVICE_COMPLETE b2 (seq 4): release (0), e5-e8 complete; DISPATCH (seq 5): no candidate.",
        "5 events. Both BatchStarts are in DISPATCH seq 2 (one dispatch, two batches).",
        "Result over [0,100]: 8 completed, e9 censored (waiting); lead times 30 (x8); queue waits 0 (x8; e9 never starts, so it is no sample); utilization (2 units x 30 min) / (2 x 100) = 0.3; batches[work]: 2, 8 members.",
    ],
    "expected": {
        "starts": {"work": [[e("src", n), 10] for n in range(1, 9)]},
        "completions": [[e("src", n), 40] for n in range(1, 9)],
        "batches": [["work", 1, 10, [e("src", n) for n in (1, 2, 3, 4)]], ["work", 2, 10, [e("src", n) for n in (5, 6, 7, 8)]]],
        "batch_start_seqs": [["work", 1, 2], ["work", 2, 2]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 5,
        "result": {"completed_count": 8, "censored_entities": 1, "lead_times_minutes": [30] * 8,
                   "queue_waits_minutes": {"work": [0] * 8}, "resource_utilization": {"chamber": 0.3},
                   "resource_states": None, "blocking": None, "buffers": None,
                   "batches": {"work": {"batches_started": 2, "members_started": 8}}},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-BT4
cases.append({
    "id": "G-BT4",
    "title": "a batch ends before a full buffer: the chamber is held until its last blocked member moves on (ADR-026 D-054 G-T4)",
    "model": model("G-BT4", [res("chamber"), res("packer")],
                   [source("src", 1, 3), process("oven", 10, [("chamber", 1)], (3, 3)), queue("q_pack", 1),
                    process("pack", 4, [("packer", 1)]), sink("done")],
                   [flow("src", "oven"), flow("oven", "q_pack"), flow("q_pack", "pack"), flow("pack", "done")]),
    "config": config(30),
    "derivation": [
        "src every minute, 3 entities (1, 2, 3) -> oven (batch min = max = 3, chamber, 10 min) -> q_pack (capacity 1) -> pack (packer, 4 min) -> done.",
        "t=0: the first arrival at 1 (seq 1). t=1, 2: e1, e2 enter oven's buffer (next arrivals seq 2, 3). t=3: e3 (seq 3, the last): 3 wait, ready, DISPATCH (seq 4): BatchStart b1 (e1, e2, e3) at 3, SERVICE_COMPLETE at 13 (seq 5).",
        "t=13: SERVICE_COMPLETE (seq 5). Every member's route is resolved first, in FIFO order (D-054): e1 -> q_pack, which holds 0 of 1: e1 takes the place; e2 -> q_pack: 0 + 1 (e1's) of 1, full: blocked; e3: blocked. A member is blocked, so nothing is released and no dispatch is asked for the release (review L3). Per member: ServiceEnd e1, e1 queues at pack (QueueEnter, at q_pack; its request asks for a dispatch, seq 6); ServiceEnd e2, Blocked e2 (at oven, by q_pack); ServiceEnd e3, Blocked e3.",
        "DISPATCH (seq 6): e1 is granted the packer (ObsResource), ServiceStart e1 at pack, SERVICE_COMPLETE at 17 (seq 7). The grant made room: e2, the first blocked, moves in; it is not the batch's last blocked member, so the chamber is not released: Unblocked e2, QueueEnter e2 (a 0-minute block, D-064 (e)). e2's request is then a candidate: the packer is busy, held.",
        "t=17: SERVICE_COMPLETE e1 at pack (seq 7): release (wakes e2), ServiceEnd, DISPATCH (seq 8), Complete e1. DISPATCH (seq 8): e2 granted (ServiceStart at 17, SERVICE_COMPLETE at 21, seq 9); room: e3 moves in, the batch's last blocked member: first the chamber's release (ObsResource 0), then Unblocked e3, QueueEnter e3; e3 held.",
        "t=21: SERVICE_COMPLETE e2 (seq 9): release, DISPATCH (seq 10), Complete e2; DISPATCH: e3 starts (21-25, seq 11). t=25: SERVICE_COMPLETE e3 (seq 11): release, DISPATCH (seq 12), Complete; DISPATCH: nothing.",
        "12 events: 3 arrivals, 5 DISPATCHes (3, 13, 17, 21, 25), 1 SERVICE_COMPLETE at oven, 3 at pack.",
        "Result over [0,30]: 3 completed; lead times e1 16, e2 19, e3 22; queue waits at oven (no explicit queue: key oven) 2, 1, 0 and at pack (key q_pack) 0, 4, 4 (from the entry after the unblock, D-064 (f)).",
        "chamber in use [3,17): 14 min (utilization 14/30), held by blocked members [13,17): blocked_ticks 4 min (the bundle once, not per member), scheduled processing 14 - 4 = 10 min; packer in use [13,25): 12 min (0.4), never blocked.",
        "blocking[oven] (the only node whose route reaches a finite buffer): 2 blocks started (e2, e3 at 13), both complete (13-13 and 13-17): 0 + 4 = 4 min; blocked entities over time: 4 min (e3; e2's block has length 0). buffers[q_pack]: full from 13 to 21 (an entry right after each grant): 8 min. batches[oven]: 1, 3 members.",
    ],
    "expected": {
        "starts": {"oven": [[e("src", 1), 3], [e("src", 2), 3], [e("src", 3), 3]], "pack": [[e("src", 1), 13], [e("src", 2), 17], [e("src", 3), 21]]},
        "completions": [[e("src", 1), 17], [e("src", 2), 21], [e("src", 3), 25]],
        "batches": [["oven", 1, 3, [e("src", 1), e("src", 2), e("src", 3)]]],
        "batch_start_seqs": [["oven", 1, 4]],
        "blocks": [[e("src", 2), "oven", 13, 13], [e("src", 3), "oven", 13, 17]],
        "downs": [], "pending": [], "capacities": [],
        "events": 12,
        "result": {"completed_count": 3, "censored_entities": 0, "lead_times_minutes": [16, 19, 22],
                   "queue_waits_minutes": {"oven": [2, 1, 0], "q_pack": [0, 4, 4]},
                   "resource_utilization": {"chamber": 14 / 30, "packer": 0.4},
                   "resource_states": {"chamber": {"in_use_ticks": ticks(14), "blocked_ticks": ticks(4), "scheduled_processing_ticks": ticks(10)},
                                       "packer": {"in_use_ticks": ticks(12), "blocked_ticks": 0, "scheduled_processing_ticks": ticks(12)}},
                   "blocking": {"oven": {"blocks_started": 2, "blocks_completed": 2, "completed_block_ticks": ticks(4), "blocked_entity_ticks": ticks(4)}},
                   "buffers": {"q_pack": {"full_ticks": ticks(8)}},
                   "batches": {"oven": {"batches_started": 1, "members_started": 3}}},
    },
    "simpy": "refused: a finite buffer (the batch and changeover reference has no blocking; T-047's reference has no batches)",
})

# ---------------------------------------------------------------- G-BT5
cases.append({
    "id": "G-BT5",
    "title": "the members' routing draws are in FIFO order (ADR-026 D-054 G-T5)",
    "model": model("G-BT5", [res("chamber")],
                   [source("src", 1, 4), process("oven", 5, [("chamber", 1)], (4, 4)), {"id": "test", "type": "decision"},
                    sink("done"), sink("scrap", "abandoned")],
                   [flow("src", "oven"), flow("oven", "test"), flow("test", "done", 0.5), flow("test", "scrap", 0.5)]),
    "config": config(20, seed=4242),
    "derivation": [
        "4 entities at 1..4 form one batch at 4 (min = max = 4), served 4-9. At 9 the four members' routes are resolved in FIFO order (D-054): member i takes the i-th draw of test's routing stream; the decision's flows in ID order are test__done (0.5) then test__scrap (0.5), so a draw U < 0.5 completes the member and U >= 0.5 scraps it (SIMULATION_SPEC §3).",
        "The test computes the four draws itself from the stream test/routing of replication 0 (D-006's SHA-256 seed derivation, SplitMix64), and expects the members, in FIFO order e1..e4, to complete (Value 1) or be abandoned (Value 0) as those draws say, all at 9.",
    ],
    "expected": {"routing_draws_in_member_order": {"decision": "test", "members": [e("src", n) for n in (1, 2, 3, 4)], "at": 9,
                                                  "threshold": 0.5}},
    "simpy": "not compared: the routing draws are the engine's stream (SimPy draws from Python's random)",
})

# ---------------------------------------------------------------- G-BT6
cases.append({
    "id": "G-BT6",
    "title": "min_size reached by an arrival exactly at the wait limit; the limit's BATCH_TIMEOUT in the same tick finds the request already there",
    "model": model("G-BT6", [res("chamber")], [source("src", 20, 2), process("work", 5, [("chamber", 1)], (2, 4, 20)), sink("done")],
                   src_work_done),
    "config": config(60),
    "derivation": [
        "src every 20 minutes, 2 entities (20, 40); work: min 2, max 4, max_wait 20, service 5.",
        "t=0: the first arrival at 20 (seq 1). t=20: e1 (seq 1; next at 40, seq 2) enters the empty buffer: BATCH_TIMEOUT at 20 + 20 = 40 (seq 3).",
        "t=40: ARRIVAL e2 (seq 2, priority 10) comes before the BATCH_TIMEOUT (seq 3, priority 15; D-048). e2 enters: 2 >= min_size and the oldest has waited 40 - 20 = 20 >= 20 (the limit counts at equality): ready at the entry (request sequence 1, DISPATCH seq 4). BATCH_TIMEOUT (seq 3): not stale (e1, its oldest, is not granted yet: that is the dispatch's), but a batch request is already waiting: nothing.",
        "DISPATCH (seq 4): b1 = (e1, e2) at 40, SERVICE_COMPLETE at 45 (seq 5). t=45: release, DISPATCH (seq 6), e1, e2 complete; DISPATCH: nothing.",
        "6 events: 2 arrivals, 1 BATCH_TIMEOUT, 2 DISPATCHes, 1 SERVICE_COMPLETE.",
        "Result over [0,60]: 2 completed; lead times 25, 5; queue waits 20, 0; utilization 5/60; batches[work]: 1, 2.",
    ],
    "expected": {
        "starts": {"work": [[e("src", 1), 40], [e("src", 2), 40]]},
        "completions": [[e("src", 1), 45], [e("src", 2), 45]],
        "batches": [["work", 1, 40, [e("src", 1), e("src", 2)]]],
        "batch_start_seqs": [["work", 1, 4]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 6,
        "result": {"completed_count": 2, "censored_entities": 0, "lead_times_minutes": [25, 5],
                   "queue_waits_minutes": {"work": [20, 0]}, "resource_utilization": {"chamber": 5 / 60},
                   "resource_states": None, "blocking": None, "buffers": None,
                   "batches": {"work": {"batches_started": 1, "members_started": 2}}},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-BT7
cases.append({
    "id": "G-BT7",
    "title": "the wait limit's BATCH_TIMEOUT makes a batch of min_size ready",
    "model": model("G-BT7", [res("chamber")], [source("src", 10, 2), process("work", 5, [("chamber", 1)], (2, 4, 20)), sink("done")],
                   src_work_done),
    "config": config(60),
    "derivation": [
        "src every 10 minutes, 2 entities (10, 20); work: min 2, max 4, max_wait 20, service 5.",
        "t=0: the first arrival at 10 (seq 1). t=10: e1 (seq 1; next at 20, seq 2): BATCH_TIMEOUT at 30 (seq 3). t=20: e2 (seq 2, the last): 2 wait, but the oldest has waited 10 < 20 and 2 < max_size: not ready.",
        "t=30: BATCH_TIMEOUT (seq 3), not stale: 2 >= min_size and 30 - 10 = 20 >= 20: ready (request sequence 1), DISPATCH (seq 4): b1 = (e1, e2) at 30, SERVICE_COMPLETE at 35 (seq 5). t=35: release, DISPATCH (seq 6), e1, e2 complete; DISPATCH: nothing.",
        "6 events: 2 arrivals, 1 BATCH_TIMEOUT, 2 DISPATCHes, 1 SERVICE_COMPLETE.",
        "Result over [0,60]: 2 completed; lead times 25, 15; queue waits 20, 10; utilization 5/60; batches[work]: 1, 2.",
    ],
    "expected": {
        "starts": {"work": [[e("src", 1), 30], [e("src", 2), 30]]},
        "completions": [[e("src", 1), 35], [e("src", 2), 35]],
        "batches": [["work", 1, 30, [e("src", 1), e("src", 2)]]],
        "batch_start_seqs": [["work", 1, 4]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 6,
        "result": {"completed_count": 2, "censored_entities": 0, "lead_times_minutes": [25, 15],
                   "queue_waits_minutes": {"work": [20, 10]}, "resource_utilization": {"chamber": 5 / 60},
                   "resource_states": None, "blocking": None, "buffers": None,
                   "batches": {"work": {"batches_started": 1, "members_started": 2}}},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-BT8
cases.append({
    "id": "G-BT8",
    "title": "more arrivals than max_size: a ready request waits for the busy chamber, takes max_size at its grant, and the re-check finds the next oldest's limit already passed",
    "model": model("G-BT8", [res("chamber")], [source("src", 1, 9), process("work", 10, [("chamber", 1)], (2, 3, 5)), sink("done")],
                   src_work_done),
    "config": config(40),
    "derivation": [
        "src every minute, 9 entities (1..9); work: min 2, max 3, max_wait 5, service 10.",
        "t=0: the first arrival at 1 (seq 1). t=1: e1 (seq 1; next at 2, seq 2): BATCH_TIMEOUT at 6 (seq 3). t=2: e2 (seq 2; next seq 4): 2 wait, the oldest 1 minute: not ready. t=3: e3 (seq 4; next at 4, seq 5): 3 = max_size: ready (request sequence 1), DISPATCH (seq 6): b1 = (e1, e2, e3) 3-13, SERVICE_COMPLETE (seq 7); the buffer is empty (new generation).",
        "t=4: e4 (seq 5; next at 5, seq 8), the new oldest: BATCH_TIMEOUT at 9 (seq 9). t=5: e5 (seq 8; next seq 10).",
        "t=6: ARRIVAL e6 (seq 10, priority 10; next at 7, seq 11): 3 wait = max_size: ready (request sequence 2), DISPATCH (seq 12). BATCH_TIMEOUT (seq 3, priority 15): stale (e1's, an earlier oldest): ignored. DISPATCH (seq 12): the chamber is busy: held.",
        "t=7, 8: e7, e8 (seq 11, 13; next seq 13, 14): 4, 5 wait (a request is waiting already). t=9: ARRIVAL e9 (seq 14, the last): 6 wait. BATCH_TIMEOUT (seq 9): e4's, not stale, but a request is waiting: nothing.",
        "t=13: SERVICE_COMPLETE b1 (seq 7): release (wakes the held request), DISPATCH (seq 15), e1-e3 complete. DISPATCH (seq 15): b2 takes min(6, 3) = 3: e4, e5, e6, 13-23 (SERVICE_COMPLETE seq 16). Re-check right after the grant: the new oldest is e7 (entered 7); its limit 7 + 5 = 12 <= 13 has passed, so no BATCH_TIMEOUT: the check is now (D-096 (c)): 3 wait = max_size anyway: ready (request sequence 3), a candidate in this dispatch: the chamber is busy: held.",
        "t=23: SERVICE_COMPLETE b2 (seq 16): release (wakes it), DISPATCH (seq 17), e4-e6 complete; DISPATCH: b3 = (e7, e8, e9) 23-33 (seq 18). t=33: SERVICE_COMPLETE (seq 18): release, DISPATCH (seq 19), e7-e9 complete; DISPATCH: nothing.",
        "19 events: 9 arrivals, 2 BATCH_TIMEOUTs (6, 9), 5 DISPATCHes (3, 6, 13, 23, 33), 3 SERVICE_COMPLETEs.",
        "Result over [0,40]: 9 completed; lead times (completion order) 12, 11, 10, 19, 18, 17, 26, 25, 24; queue waits (start order) 2, 1, 0, 9, 8, 7, 16, 15, 14; utilization 30/40 = 0.75; batches[work]: 3, 9.",
    ],
    "expected": {
        "starts": {"work": [[e("src", n), t] for n, t in [(1, 3), (2, 3), (3, 3), (4, 13), (5, 13), (6, 13), (7, 23), (8, 23), (9, 23)]]},
        "completions": [[e("src", n), t] for n, t in [(1, 13), (2, 13), (3, 13), (4, 23), (5, 23), (6, 23), (7, 33), (8, 33), (9, 33)]],
        "batches": [["work", 1, 3, [e("src", n) for n in (1, 2, 3)]], ["work", 2, 13, [e("src", n) for n in (4, 5, 6)]],
                    ["work", 3, 23, [e("src", n) for n in (7, 8, 9)]]],
        "batch_start_seqs": [["work", 1, 6], ["work", 2, 15], ["work", 3, 17]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 19,
        "result": {"completed_count": 9, "censored_entities": 0, "lead_times_minutes": [12, 11, 10, 19, 18, 17, 26, 25, 24],
                   "queue_waits_minutes": {"work": [2, 1, 0, 9, 8, 7, 16, 15, 14]}, "resource_utilization": {"chamber": 0.75},
                   "resource_states": None, "blocking": None, "buffers": None,
                   "batches": {"work": {"batches_started": 3, "members_started": 9}}},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-BT9
cases.append({
    "id": "G-BT9",
    "title": "a batch spans its chamber's shift end (finish-first, overtime); the next waits off shift in B2's state and starts at the next shift",
    "model": model("G-BT9", [res("chamber", calendar_id="c")], [source("src", 5, 4), process("work", 15, [("chamber", 1)], (2, 2)), sink("done")],
                   src_work_done, calendars=[{"id": "c", "period": 40, "shifts": [{"start": 0, "end": 20}]}]),
    "config": config(60),
    "derivation": [
        "The chamber is on calendar c: S = 1 on [0,20), 0 on [20,40), 1 on [40,60), 0 from 60. src every 5 minutes, 4 entities (5, 10, 15, 20); work: min = max = 2, service 15.",
        "t=0: the first arrival at 5 (seq 1); c's first change at 20 (seq 2; D-048: sources, then calendars). S(0) = 1 is the program's (no observation).",
        "t=5: e1 (seq 1; next at 10, seq 3). t=10: e2 (seq 3; next seq 4): ready, DISPATCH (seq 5): 0 + 1 <= min(S 1, C - D 1): b1 = (e1, e2) 10-25 (SERVICE_COMPLETE seq 6). t=15: e3 (seq 4; next at 20, seq 7).",
        "t=20: ARRIVAL e4 (seq 7, the last): ready (request sequence 2), DISPATCH (seq 8). CAPACITY_CHANGE (seq 2): first the calendar's next change, at 40 (seq 9; D-081 (a)), then S 1 -> 0 (Capacity chamber 0); a fall wakes nothing and b1 goes on (finish-first, overtime [20,25)). DISPATCH (seq 8): 1 + 1 > min(0, 1): held. At the tick's end the chamber is in use: not B2's state.",
        "t=25: SERVICE_COMPLETE b1 (seq 6): release (wakes the request), DISPATCH (seq 10), e1, e2 complete; DISPATCH: 0 + 1 > min(0, 1): held. At the tick's end the request is in B2's state (held, none of its bundle in use): since 25, deadline next_fit(max(25, X_end 0) + TTRmax 0) = next_fit(25) = 40 (D-081 (f)); a batch request is a request like any other (D-050).",
        "t=40: CAPACITY_CHANGE (seq 9): next at 60 (seq 11); S 0 -> 1 wakes the held front, DISPATCH (seq 12): b2 = (e3, e4) 40-55 (SERVICE_COMPLETE seq 13): no stall at the deadline (it is granted in that tick).",
        "t=55: SERVICE_COMPLETE (seq 13): release, DISPATCH (seq 14), e3, e4 complete; DISPATCH: nothing. t=60: CAPACITY_CHANGE (seq 11): its next, at 80, is after the horizon; S 1 -> 0.",
        "14 events: 4 arrivals, 3 CAPACITY_CHANGEs (20, 40, 60), 5 DISPATCHes (10, 20, 25, 40, 55), 2 SERVICE_COMPLETEs.",
        "Result over [0,60]: 4 completed; lead times e1 20, e2 15, e3 40, e4 35; queue waits 5, 0, 25, 20; chamber in use [10,25) and [40,55): 30/60 = 0.5.",
        "resource_states[chamber]: in use 30 min; scheduled (S) 20 + 20 = 40 min; min(U, S): [10,20) and [40,55) = 25 min; overtime [20,25) = 5 min (25 + 5 = 30); available = S = 40 min; idle [0,10) and [55,60) = 15 min; scheduled processing (P = U) = 25 min. batches[work]: 2, 4.",
    ],
    "expected": {
        "starts": {"work": [[e("src", 1), 10], [e("src", 2), 10], [e("src", 3), 40], [e("src", 4), 40]]},
        "completions": [[e("src", 1), 25], [e("src", 2), 25], [e("src", 3), 55], [e("src", 4), 55]],
        "batches": [["work", 1, 10, [e("src", 1), e("src", 2)]], ["work", 2, 40, [e("src", 3), e("src", 4)]]],
        "batch_start_seqs": [["work", 1, 5], ["work", 2, 12]],
        "blocks": [], "downs": [], "pending": [],
        "capacities": [["chamber", 20, 0], ["chamber", 40, 1], ["chamber", 60, 0]],
        "capacity_seqs": [2, 9, 11],
        "events": 14,
        "result": {"completed_count": 4, "censored_entities": 0, "lead_times_minutes": [20, 15, 40, 35],
                   "queue_waits_minutes": {"work": [5, 0, 25, 20]}, "resource_utilization": {"chamber": 0.5},
                   "resource_states": {"chamber": {"in_use_ticks": ticks(30), "scheduled_ticks": ticks(40), "scheduled_in_use_ticks": ticks(25),
                                                   "overtime_ticks": ticks(5), "available_ticks": ticks(40), "idle_ticks": ticks(15),
                                                   "scheduled_processing_ticks": ticks(25)}},
                   "blocking": None, "buffers": None,
                   "batches": {"work": {"batches_started": 2, "members_started": 4}}},
    },
    "simpy": "refused: a calendar (the batch and changeover reference has none; T-049's reference has no batches)",
})

# ---------------------------------------------------------------- G-BT10
cases.append({
    "id": "G-BT10",
    "title": "a breakdown during a batch: the failure waits for the whole batch (finish-first) and takes the chamber it releases",
    "model": model("G-BT10", [res("chamber", breakdowns={"time_to_failure": {"distribution": const(12)}, "time_to_repair": {"distribution": const(3)}})],
                   [source("src", 5, 4), process("work", 15, [("chamber", 1)], (2, 2)), sink("done")], src_work_done),
    "config": config(50),
    "derivation": [
        "The chamber breaks down (TTF 12, TTR 3, constant, calendar time: no calendar). src every 5 minutes, 4 entities; work: min = max = 2, service 15.",
        "t=0: the first arrival at 5 (seq 1); the chamber's clock 0: FAILURE at 12 (seq 2).",
        "t=5: e1 (seq 1; next seq 3). t=10: e2 (seq 3; next at 15, seq 4): ready, DISPATCH (seq 5): b1 = (e1, e2) 10-25 (SERVICE_COMPLETE seq 6).",
        "t=12: FAILURE (seq 2): no unit is idle (in use 1): the failure waits (BreakdownPending; finish-first, D-051).",
        "t=15: e3 (seq 4; next at 20, seq 7). t=20: e4 (seq 7, the last): ready, DISPATCH (seq 8): the chamber is busy: held.",
        "t=25: SERVICE_COMPLETE b1 (seq 6): the release (in use 0); the released unit goes down for the waiting failure (ResourceDown; TTR 3: REPAIR at 28, seq 9; D-074 (b)), then the held front is woken; DISPATCH (seq 10); e1, e2 complete. DISPATCH: 0 + 1 > C - D = 0: held. At the tick's end it is in B2's state: since 25, deadline 25 + TTRmax 3 = 28 (no calendar).",
        "t=28: REPAIR (seq 9): up; the clock's next failure 12 later, at 40 (seq 11); no failure waits: the held front is woken, DISPATCH (seq 12): b2 = (e3, e4) 28-43 (SERVICE_COMPLETE seq 13).",
        "t=40: FAILURE (seq 11): busy, waits. t=43: SERVICE_COMPLETE b2 (seq 13): release, down for the failure (REPAIR at 46, seq 14), no held front; DISPATCH (seq 15); e3, e4 complete; DISPATCH: nothing. t=46: REPAIR (seq 14): the next failure at 58 is after the horizon (not scheduled); nothing waits.",
        "15 events: 4 arrivals, 2 FAILUREs, 2 REPAIRs, 5 DISPATCHes (10, 20, 25, 28, 43), 2 SERVICE_COMPLETEs.",
        "Result over [0,50]: 4 completed; lead times e1 20, e2 15, e3 28, e4 23; queue waits 5, 0, 13, 8; chamber in use [10,25) and [28,43): 30/50 = 0.6. resource_states[chamber]: in use 30 min; down [25,28) and [43,46) = 6 min, 2 breakdowns started, 2 complete repairs of 6 min in all; available = C*T - down = 44 min; idle = 44 - 30 = 14 min; scheduled processing 30 min (no finite buffer). batches[work]: 2, 4.",
    ],
    "expected": {
        "starts": {"work": [[e("src", 1), 10], [e("src", 2), 10], [e("src", 3), 28], [e("src", 4), 28]]},
        "completions": [[e("src", 1), 25], [e("src", 2), 25], [e("src", 3), 43], [e("src", 4), 43]],
        "batches": [["work", 1, 10, [e("src", 1), e("src", 2)]], ["work", 2, 28, [e("src", 3), e("src", 4)]]],
        "batch_start_seqs": [["work", 1, 5], ["work", 2, 12]],
        "blocks": [], "downs": [["chamber", 0, 25, 28], ["chamber", 0, 43, 46]], "pending": [["chamber", 0, 12], ["chamber", 0, 40]],
        "capacities": [],
        "events": 15,
        "result": {"completed_count": 4, "censored_entities": 0, "lead_times_minutes": [20, 15, 28, 23],
                   "queue_waits_minutes": {"work": [5, 0, 13, 8]}, "resource_utilization": {"chamber": 0.6},
                   "resource_states": {"chamber": {"in_use_ticks": ticks(30), "down_ticks": ticks(6), "breakdowns_started": 2, "repairs_completed": 2,
                                                   "completed_repair_ticks": ticks(6), "available_ticks": ticks(44), "idle_ticks": ticks(14),
                                                   "scheduled_processing_ticks": ticks(30)}},
                   "blocking": None, "buffers": None,
                   "batches": {"work": {"batches_started": 2, "members_started": 4}}},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-BT11
cases.append({
    "id": "G-BT11",
    "title": "the measurement window: batches started at W and at H count, a batch started before W does not (G-BT1 over [80, 120])",
    "model": dict(bt1_model, name="G-BT11"),
    "config": config(120, warmup=80),
    "derivation": [
        "G-BT1's model and run up to 120 (the same events up to there): b1 40-70, b2 80-110, b3 from 120 (its SERVICE_COMPLETE at 150 is after the horizon: not scheduled).",
        "19 events: 12 arrivals, SERVICE_COMPLETEs at 70 and 110, DISPATCHes at 40, 70, 80, 110, 120.",
        "Window [80,120] (T = 40 min; SIMULATION_SPEC §5: events at W <= t <= H count). batches_started counts a BatchStart in the window: b2 at 80 (= W) and b3 at 120 (= H): 2 batches, 8 members; b1 (40) does not count.",
        "Completed: e5-e8 at 110 (4); e1-e4 at 70 are before W. Censored at H: e9-e12 in service (4). Lead-time cohort (arrival >= 80, completion <= 120): only e8 (80 -> 110): 30. Queue waits (entry >= 80, start <= 120): e8 0 (80, 80), e9 30, e10 20, e11 10, e12 0 (all started at 120); e5-e7 entered before W.",
        "Chamber in use over [80,120]: [80,110) and from 120 (b3: no time inside the window): 30/40 = 0.75.",
    ],
    "expected": {
        "starts": {"work": [[e("src", n), t] for n, t in [(1, 40), (2, 40), (3, 40), (4, 40), (5, 80), (6, 80), (7, 80), (8, 80),
                                                           (9, 120), (10, 120), (11, 120), (12, 120)]]},
        "completions": [[e("src", n), t] for n, t in [(1, 70), (2, 70), (3, 70), (4, 70), (5, 110), (6, 110), (7, 110), (8, 110)]],
        "batches": [["work", 1, 40, [e("src", n) for n in (1, 2, 3, 4)]], ["work", 2, 80, [e("src", n) for n in (5, 6, 7, 8)]],
                    ["work", 3, 120, [e("src", n) for n in (9, 10, 11, 12)]]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 19,
        "result": {"completed_count": 4, "censored_entities": 4, "lead_times_minutes": [30],
                   "queue_waits_minutes": {"work": [0, 30, 20, 10, 0]}, "resource_utilization": {"chamber": 0.75},
                   "resource_states": None, "blocking": None, "buffers": None,
                   "batches": {"work": {"batches_started": 2, "members_started": 8}}},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-BT12
cases.append({
    "id": "G-BT12",
    "title": "max_entities mid-batch: the last two never form a batch and stay censored; a forming batch is no deadlock (B1 runs, finite buffer)",
    "model": model("G-BT12", [res("chamber")],
                   [source("src", 10, 6), queue("q_oven", 4), process("oven", 30, [("chamber", 1)], (4, 4)), sink("done")],
                   [flow("src", "q_oven"), flow("q_oven", "oven"), flow("oven", "done")]),
    "config": config(200),
    "derivation": [
        "src every 10 minutes, 6 entities -> q_oven (capacity 4 = max_size, valid: D-054) -> oven (min = max = 4, chamber, 30 min) -> done.",
        "t=10..40: e1..e4 enter (next arrivals seq 2..5); e4 makes the batch ready, DISPATCH (seq 6): b1 = (e1..e4) 40-70 (SERVICE_COMPLETE seq 7).",
        "t=50, 60: e5, e6 (seq 5, 8; e6 is the 6th: no next arrival). 2 wait: never 4 (no wait limit), so no batch request: they are not a request in any FIFO, so D-050 B1 (which runs after every event here, the model having a finite buffer) sees no blocked entity and no held candidate, and B2 does not run (no breakdowns, no calendar): the run goes on to the horizon (D-050: a batch waiting to form is not a stall).",
        "t=70: SERVICE_COMPLETE (seq 7): release, DISPATCH (seq 9), e1-e4 complete; DISPATCH: nothing.",
        "9 events: 6 arrivals, 2 DISPATCHes, 1 SERVICE_COMPLETE.",
        "Result over [0,200]: 4 completed, 2 censored (e5, e6 waiting); lead times 60, 50, 40, 30; queue waits (key q_oven) 30, 20, 10, 0; chamber in use 30/200 = 0.15; resource_states[chamber] (a finite buffer: every resource): in use 30 min, blocked 0, processing 30 min; blocking[src] (its route reaches q_oven): no block; buffers[q_oven]: full only at the instant of tick 40 (4 waiting, granted in the same tick): 0; batches[oven]: 1, 4.",
    ],
    "expected": {
        "starts": {"oven": [[e("src", n), 40] for n in (1, 2, 3, 4)]},
        "completions": [[e("src", n), 70] for n in (1, 2, 3, 4)],
        "batches": [["oven", 1, 40, [e("src", n) for n in (1, 2, 3, 4)]]],
        "batch_start_seqs": [["oven", 1, 6]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 9,
        "result": {"completed_count": 4, "censored_entities": 2, "lead_times_minutes": [60, 50, 40, 30],
                   "queue_waits_minutes": {"q_oven": [30, 20, 10, 0]}, "resource_utilization": {"chamber": 0.15},
                   "resource_states": {"chamber": {"in_use_ticks": ticks(30), "blocked_ticks": 0, "scheduled_processing_ticks": ticks(30)}},
                   "blocking": {"src": {"blocks_started": 0, "blocks_completed": 0, "completed_block_ticks": 0, "blocked_entity_ticks": 0}},
                   "buffers": {"q_oven": {"full_ticks": 0}},
                   "batches": {"oven": {"batches_started": 1, "members_started": 4}}},
    },
    "simpy": "refused: a finite buffer (the batch and changeover reference has no blocking)",
})

# ---------------------------------------------------------------- G-BT13
gd1_resources = [res("machine", breakdowns={"time_to_failure": {"distribution": const(960)}, "time_to_repair": {"distribution": const(480)}}),
                 dict(res("operator", calendar_id="op"), type="human")]
cases.append({
    "id": "G-BT13",
    "title": "D-050 B2 on a batch request (R4 bypassed, G-D1's bundle): ErrStalled names the batch's oldest member, since the end of the tick the batch became ready",
    "model": model("G-BT13", gd1_resources,
                   [source("src", 10, 2), process("work", 30, [("machine", 1), ("operator", 1)], (2, 2)), sink("done")], src_work_done,
                   calendars=[{"id": "op", "period": 1440, "shifts": [{"start": 960, "end": 1440}]}]),
    "config": config(1500),
    "validator": {"codes_at": [["STALL_RISK", "/nodes/1/requires"]]},
    "derivation": [
        "G-D1's resources (calendar-goldens.json): the machine breaks down (TTF 960, TTR 480, constant, calendar time) and has no calendar; the operator is on shift [960,1440) of each day. The validator refuses the bundle (D-050 R4, STALL_RISK); the test runs it past the validator. work is a batch process (min = max = 2) needing both; src: 2 entities, at 10 and 20.",
        "t=0: the first arrival at 10 (seq 1); the operator's calendar: first change at 960 (seq 2); the machine's clock: FAILURE at 960 (seq 3).",
        "t=10: e1 (seq 1; next at 20, seq 4): 1 waits. t=20: e2 (seq 4, the last): ready: the batch request (request sequence 1) is at the front of both FIFOs, DISPATCH (seq 5): the operator is off shift (0 + 1 > min(0, 1)): held. At the tick's end it is in B2's state (held, none of its bundle in use): since 20; TTRmax 480, X_end 0: deadline next_fit(20 + 480) = next_fit(500) = 960.",
        "t=960: CAPACITY_CHANGE (seq 2): next at 1440 (seq 6); operator 0 -> 1 wakes the request, DISPATCH (seq 7). FAILURE (seq 3): the machine is idle: down (REPAIR at 1440, seq 8). DISPATCH (seq 7): 0 + 1 > C - D = 0: held. At the tick's end it is still in the state (since 20 is not reset) and 960 >= 960: ErrStalled.",
        "The batch request has no entity of its own: the error names the batch's oldest member, e1 (the one a grant would take first), and since = 20, the end of the tick its request entered the state (D-096 (g)).",
    ],
    "expected": {"starts": {}, "completions": [], "batches": [], "blocks": [], "downs": [["machine", 0, 960, None]], "pending": [],
                 "capacities": [["operator", 960, 1]],
                 "stalled": {"tick": ticks(960), "entity": e("src", 1), "node": "work", "since": ticks(20), "deadline": ticks(960),
                             "reason": f"the replication stalled at tick {ticks(960)}: {e('src', 1)} waits for node work since tick {ticks(20)} with none of its bundle in use; it was due by tick {ticks(960)}"}},
    "simpy": "not compared: SimPy does not detect stalls",
})

# ---------------------------------------------------------------- G-BT14
cases.append({
    "id": "G-BT14",
    "title": "a stale BATCH_TIMEOUT: the batch filled before its oldest's limit; the old limit fires later and is ignored",
    "model": model("G-BT14", [res("chamber")], [source("src", 10, 3), process("work", 5, [("chamber", 1)], (1, 2, 50)), sink("done")],
                   src_work_done),
    "config": config(100),
    "derivation": [
        "src every 10 minutes, 3 entities (10, 20, 30); work: min 1, max 2, max_wait 50, service 5.",
        "t=0: the first arrival at 10 (seq 1). t=10: e1 (seq 1; next at 20, seq 2): the oldest, BATCH_TIMEOUT at 60 (seq 3, generation 1). 1 waits: not max_size, and it has waited 0 < 50: not ready.",
        "t=20: e2 (seq 2; next at 30, seq 4): 2 = max_size: ready, DISPATCH (seq 5): b1 = (e1, e2) 20-25 (SERVICE_COMPLETE seq 6); the buffer is empty: generation 2 (e1's timeout is stale now).",
        "t=25: SERVICE_COMPLETE (seq 6): release, DISPATCH (seq 7), e1, e2 complete; DISPATCH: nothing.",
        "t=30: e3 (seq 4, the last): the new oldest (generation 3), BATCH_TIMEOUT at 80 (seq 8). Not ready.",
        "t=60: BATCH_TIMEOUT (seq 3): generation 1 is stale: ignored (D-054: a stale timeout is ignored when it fires).",
        "t=80: BATCH_TIMEOUT (seq 8): generation 3, current: 1 >= min_size and 80 - 30 = 50 >= 50: ready, DISPATCH (seq 9): b2 = (e3) 80-85 (SERVICE_COMPLETE seq 10). t=85: release, DISPATCH (seq 11), e3 completes; DISPATCH: nothing.",
        "11 events: 3 arrivals, 2 BATCH_TIMEOUTs (60, 80), 4 DISPATCHes (20, 25, 80, 85), 2 SERVICE_COMPLETEs.",
        "Result over [0,100]: 3 completed; lead times 15, 5, 55; queue waits 10, 0, 50; utilization 10/100 = 0.1; batches[work]: 2, 3.",
    ],
    "expected": {
        "starts": {"work": [[e("src", 1), 20], [e("src", 2), 20], [e("src", 3), 80]]},
        "completions": [[e("src", 1), 25], [e("src", 2), 25], [e("src", 3), 85]],
        "batches": [["work", 1, 20, [e("src", 1), e("src", 2)]], ["work", 2, 80, [e("src", 3)]]],
        "batch_start_seqs": [["work", 1, 5], ["work", 2, 9]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 11,
        "result": {"completed_count": 3, "censored_entities": 0, "lead_times_minutes": [15, 5, 55],
                   "queue_waits_minutes": {"work": [10, 0, 50]}, "resource_utilization": {"chamber": 0.1},
                   "resource_states": None, "blocking": None, "buffers": None,
                   "batches": {"work": {"batches_started": 2, "members_started": 3}}},
    },
    "simpy": "compare",
})


# ---------------------------------------------------------------- G-BT15
# Added after mutation run 1 (mutant expired-limit-scheduled survived): an
# oldest member whose wait limit is already reached when it becomes the
# oldest gets no BATCH_TIMEOUT, the batch is checked at once (D-096 (c)).
cases.append({
    "id": "G-BT15",
    "title": "max_wait 0 (min 1, max 2): every entry into an empty buffer readies a batch at once, with no BATCH_TIMEOUT (D-096 (b), (c))",
    "model": model("G-BT15", [res("chamber")], [source("src", 10, 4), process("work", 25, [("chamber", 1)], (1, 2, 0)), sink("done")],
                   src_work_done),
    "config": config(100),
    "derivation": [
        "src every 10 minutes, 4 entities (10, 20, 30, 40); work: min 1, max 2, max_wait 0, service 25, one chamber.",
        "With max_wait 0 an entity's limit is reached when it enters: when it becomes the oldest at its entry, enter + max_wait = now is not after now, so no BATCH_TIMEOUT is scheduled and the batch is checked at once (D-096 (c)): 1 >= min_size and it has waited 0 >= 0: ready.",
        "t=0: the first arrival at 10 (seq 1).",
        "t=10: e1 (seq 1; next at 20, seq 2) enters the empty buffer: ready at once, the batch request, DISPATCH (seq 3): b1 = (e1) 10-35 (SERVICE_COMPLETE seq 4); the buffer is empty again.",
        "t=20: e2 (seq 2; next at 30, seq 5): ready at once, DISPATCH (seq 6): the chamber is busy: held. t=30: e3 (seq 5; next at 40, seq 7) enters behind e2: a request is waiting already (at most one ungranted), no dispatch.",
        "t=35: SERVICE_COMPLETE (seq 4): release, DISPATCH (seq 8), e1 completes; the held batch request wakes: b2 = (e2, e3), min(2 waiting, max_size 2), 35-60 (seq 9).",
        "t=40: e4 (seq 7, the last): ready at once, DISPATCH (seq 10): held. t=60: SERVICE_COMPLETE (seq 9): release, DISPATCH (seq 11), e2, e3 complete; b3 = (e4) 60-85 (seq 12). t=85: release, DISPATCH (seq 13), e4 completes; nothing.",
        "13 events: 4 arrivals, 3 SERVICE_COMPLETEs, 6 DISPATCHes, no BATCH_TIMEOUT.",
        "Result over [0,100]: 4 completed; lead times e1 25, e2 40, e3 30, e4 45; queue waits 0, 15, 5, 20; the chamber in use 75 of 100 minutes (0.75); batches[work]: 3 started, 4 members.",
    ],
    "expected": {
        "starts": {"work": [[e("src", 1), 10], [e("src", 2), 35], [e("src", 3), 35], [e("src", 4), 60]]},
        "completions": [[e("src", 1), 35], [e("src", 2), 60], [e("src", 3), 60], [e("src", 4), 85]],
        "batches": [["work", 1, 10, [e("src", 1)]], ["work", 2, 35, [e("src", 2), e("src", 3)]], ["work", 3, 60, [e("src", 4)]]],
        "batch_start_seqs": [["work", 1, 3], ["work", 2, 8], ["work", 3, 11]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 13,
        "result": {"completed_count": 4, "censored_entities": 0, "lead_times_minutes": [25, 40, 30, 45],
                   "queue_waits_minutes": {"work": [0, 15, 5, 20]}, "resource_utilization": {"chamber": 0.75},
                   "resource_states": None, "blocking": None, "buffers": None,
                   "batches": {"work": {"batches_started": 3, "members_started": 4}}},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-BT16
# Added after mutation run 1 (mutant stale-timeout-not-ignored survived):
# a stale BATCH_TIMEOUT due at the same tick as the live ones of its own and
# of another batch process must not make its batch's request earlier.
cases.append({
    "id": "G-BT16",
    "title": "a stale BATCH_TIMEOUT at the tick of two live ones: the requests take their FIFO places in the live timeouts' order (D-096 (c))",
    "model": model("G-BT16", [res("r")], [table_source("src_a", 1000, [(10, 3)]), table_source("src_b", 1000, [(10, 1)]),
                                          process("pa", 15, [("r", 1)], (1, 2, 10)), process("pb", 5, [("r", 1)], (1, 2, 10)), sink("done")],
                   [flow("src_a", "pa"), flow("src_b", "pb"), flow("pa", "done"), flow("pb", "done")]),
    "config": config(60),
    "derivation": [
        "One resource r (1 unit) needed by two batch processes: pa (min 1, max 2, max_wait 10, service 15) and pb (min 1, max 2, max_wait 10, service 5). src_a releases 3 at 10, src_b 1 at 10 (tables, period 1000).",
        "t=0: the first rows in source ID order: src_a at 10 (seq 1), src_b at 10 (seq 2).",
        "t=10: ARRIVAL (seq 1; the next row is after the horizon): a1 enters pa's empty buffer: the oldest (generation 1), BATCH_TIMEOUT at 20 (seq 3); 1 waits, waited 0: not ready. a2: 2 = max_size: ready, the batch request (request sequence 1), DISPATCH (seq 4). a3 enters (a request waits already). ARRIVAL (seq 2): b1 enters pb: the oldest, BATCH_TIMEOUT at 20 (seq 5); not ready.",
        "DISPATCH (seq 4): r is free: pa's batch b1 = (a1, a2) 10-25 (SERVICE_COMPLETE seq 6); pa's oldest is now a3 (generation 2): BATCH_TIMEOUT at 10 + 10 = 20 (seq 7), seq 3 is stale; 1 waits, waited 0: not ready.",
        "t=20, the three timeouts in sequence order: seq 3 (pa, generation 1): stale, ignored. seq 5 (pb): b1 has waited 10 >= 10: ready, pb's request (request sequence 2), DISPATCH (seq 8). seq 7 (pa, generation 2): a3 has waited 10: ready, pa's request (request sequence 3), behind pb's in r's FIFO. Had the stale one been taken for a check, pa's request would have come first. DISPATCH (seq 8): r is in use: pb's request is held at the front.",
        "t=25: SERVICE_COMPLETE (seq 6): release, DISPATCH (seq 9), a1, a2 complete; pb's request wakes: pb's b1 = (b1) 25-30 (seq 10); pa's request is now the front: held. t=30: SERVICE_COMPLETE (seq 10): release, DISPATCH (seq 11), b1 completes; pa's b2 = (a3) 30-45 (seq 12). t=45: release, DISPATCH (seq 13), a3 completes.",
        "13 events: 2 arrival events, 3 BATCH_TIMEOUTs, 3 SERVICE_COMPLETEs, 5 DISPATCHes.",
        "Result over [0,60]: 4 completed; lead times a1 15, a2 15, b1 20, a3 35; queue waits pa: 0, 0, 20, pb: 15; r in use 15 + 5 + 15 = 35 of 60 minutes; batches[pa]: 2 started, 3 members; batches[pb]: 1, 1.",
    ],
    "expected": {
        "starts": {"pa": [[e("src_a", 1), 10], [e("src_a", 2), 10], [e("src_a", 3), 30]], "pb": [[e("src_b", 1), 25]]},
        "completions": [[e("src_a", 1), 25], [e("src_a", 2), 25], [e("src_b", 1), 30], [e("src_a", 3), 45]],
        "batches": [["pa", 1, 10, [e("src_a", 1), e("src_a", 2)]], ["pb", 1, 25, [e("src_b", 1)]], ["pa", 2, 30, [e("src_a", 3)]]],
        "batch_start_seqs": [["pa", 1, 4], ["pb", 1, 9], ["pa", 2, 11]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 13,
        "result": {"completed_count": 4, "censored_entities": 0, "lead_times_minutes": [15, 15, 20, 35],
                   "queue_waits_minutes": {"pa": [0, 0, 20], "pb": [15]}, "resource_utilization": {"r": 35 / 60},
                   "resource_states": None, "blocking": None, "buffers": None,
                   "batches": {"pa": {"batches_started": 2, "members_started": 3}, "pb": {"batches_started": 1, "members_started": 1}}},
    },
    "simpy": "refused: a resource needed by two processes (the batch and changeover reference grants each process's own resource)",
})

# ---------------------------------------------------------------- G-BT17
# Added after mutation run 2: batch-bundle-per-member (each blocked member
# counting the bundle in K) survived, since G-BT4's second blocked member
# moves on in the tick it is blocked, so no golden had two members of one
# batch blocked together for a positive time.
cases.append({
    "id": "G-BT17",
    "title": "two members of one batch blocked together: the batch's bundle counts once in blocked_ticks (D-096 (j))",
    "model": model("G-BT17", [res("chamber"), res("packer")],
                   [source("src", 1, 3), source("src_b", 12, 1), process("oven", 10, [("chamber", 1)], (3, 3)), queue("q_pack", 1),
                    process("pack", 4, [("packer", 1)]), sink("done")],
                   [flow("src", "oven"), flow("src_b", "q_pack"), flow("oven", "q_pack"), flow("q_pack", "pack"), flow("pack", "done")]),
    "config": config(30),
    "derivation": [
        "src every minute, 3 entities (e1, e2, e3 at 1, 2, 3) -> oven (batch min = max = 3, chamber, 10 min) -> q_pack (capacity 1) -> pack (packer, 4 min) -> done; src_b one entity f1 at 12 -> q_pack. G-BT4 with the packer busy when the batch ends.",
        "t=0: the first arrivals in source order: src at 1 (seq 1), src_b at 12 (seq 2). t=1: e1 enters oven's buffer (next src arrival at 2, seq 3); 1 of 3, min = max: no BATCH_TIMEOUT, not ready, no dispatch (D-096 (b), (e)). t=2: e2 (next at 3, seq 4). t=3: e3 (the last): 3 wait = max_size: ready, the batch request, DISPATCH (seq 5): BatchStart b1 (e1, e2, e3) at 3, SERVICE_COMPLETE at 13 (seq 6).",
        "t=12: ARRIVAL f1 (seq 2, src_b's only one): q_pack holds 0 of 1: f1 enters (QueueEnter at pack), its request asks for a dispatch (seq 7). DISPATCH (seq 7): the packer is free: f1 12-16 (SERVICE_COMPLETE seq 8); the grant leaves q_pack empty.",
        "t=13: SERVICE_COMPLETE at oven (seq 6). The members' routes in FIFO order (D-054): e1 -> q_pack (0 of 1): takes the place; e2 -> q_pack (1 of 1, e1's): full, blocked; e3: blocked. A member is blocked: nothing released. ServiceEnd e1, e1 queues at pack (DISPATCH seq 9); ServiceEnd e2, Blocked e2; ServiceEnd e3, Blocked e3. DISPATCH (seq 9): the packer is busy with f1: e1 is held; no grant, so no place frees: e2 and e3 stay blocked together, the chamber held by the batch.",
        "t=16: SERVICE_COMPLETE f1 at pack (seq 8): release (wakes e1), ServiceEnd, DISPATCH (seq 10), Complete f1. DISPATCH (seq 10): e1 granted (16-20, SERVICE_COMPLETE seq 11); its place lets e2, the first blocked, move in: not the batch's last blocked member, so the chamber stays held: Unblocked e2, QueueEnter e2; e2 held (packer busy).",
        "t=20: SERVICE_COMPLETE e1 (seq 11): release (wakes e2), ServiceEnd, DISPATCH (seq 12), Complete e1. DISPATCH (seq 12): e2 granted (20-24, seq 13); e3, the batch's last blocked member, moves in: first the chamber's release (ObsResource 0), then Unblocked e3, QueueEnter e3; e3 held.",
        "t=24: SERVICE_COMPLETE e2 (seq 13): release, DISPATCH (seq 14), Complete e2; DISPATCH: e3 24-28 (seq 15). t=28: SERVICE_COMPLETE e3 (seq 15): release, DISPATCH (seq 16), Complete e3; DISPATCH: nothing.",
        "16 events: 4 arrivals (1, 2, 3, 12), 7 DISPATCHes (3, 12, 13, 16, 20, 24, 28), 1 SERVICE_COMPLETE at oven, 4 at pack.",
        "Result over [0,30]: 4 completed (f1, e1, e2, e3); lead times f1 16 - 12 = 4, e1 20 - 1 = 19, e2 24 - 2 = 22, e3 28 - 3 = 25; queue waits at oven (key oven) 2, 1, 0; at q_pack f1 0, e1 16 - 13 = 3, e2 20 - 16 = 4 (from its entry after the unblock, D-064 (f)), e3 24 - 20 = 4.",
        "chamber in use [3, 20): 17 min (utilization 17/30); held by blocked members [13, 20): blocked_ticks 7 min, the bundle once although e2 and e3 are both blocked in [13, 16) (counting it per member would give 2 x 3 + 4 = 10); scheduled processing 17 - 7 = 10 min. packer in use [12, 28): 16 min (16/30), never blocked.",
        "blocking[oven]: 2 blocks started (e2, e3 at 13), both complete (13-16, 13-20): 3 + 7 = 10 min; blocked entities over time: 2 in [13, 16), 1 in [16, 20): 6 + 4 = 10 min. blocking[src_b] (its route reaches q_pack): nothing. buffers[q_pack]: f1's entry at 12 is granted in that tick; full from e1's entry at 13 to e3's grant at 24 (each grant followed by an entry in its tick): 11 min. batches[oven]: 1, 3 members.",
    ],
    "expected": {
        "starts": {"oven": [[e("src", 1), 3], [e("src", 2), 3], [e("src", 3), 3]],
                   "pack": [[e("src_b", 1), 12], [e("src", 1), 16], [e("src", 2), 20], [e("src", 3), 24]]},
        "completions": [[e("src_b", 1), 16], [e("src", 1), 20], [e("src", 2), 24], [e("src", 3), 28]],
        "batches": [["oven", 1, 3, [e("src", 1), e("src", 2), e("src", 3)]]],
        "batch_start_seqs": [["oven", 1, 5]],
        "blocks": [[e("src", 2), "oven", 13, 16], [e("src", 3), "oven", 13, 20]],
        "downs": [], "pending": [], "capacities": [],
        "events": 16,
        "result": {"completed_count": 4, "censored_entities": 0, "lead_times_minutes": [4, 19, 22, 25],
                   "queue_waits_minutes": {"oven": [2, 1, 0], "q_pack": [0, 3, 4, 4]},
                   "resource_utilization": {"chamber": 17 / 30, "packer": 16 / 30},
                   "resource_states": {"chamber": {"in_use_ticks": ticks(17), "blocked_ticks": ticks(7), "scheduled_processing_ticks": ticks(10)},
                                       "packer": {"in_use_ticks": ticks(16), "blocked_ticks": 0, "scheduled_processing_ticks": ticks(16)}},
                   "blocking": {"oven": {"blocks_started": 2, "blocks_completed": 2, "completed_block_ticks": ticks(10), "blocked_entity_ticks": ticks(10)},
                                "src_b": {"blocks_started": 0, "blocks_completed": 0, "completed_block_ticks": 0, "blocked_entity_ticks": 0}},
                   "buffers": {"q_pack": {"full_ticks": ticks(11)}},
                   "batches": {"oven": {"batches_started": 1, "members_started": 3}}},
    },
    "simpy": "refused: a finite buffer (the batch and changeover reference has no blocking; T-047's reference has no batches)",
})

# ================================================================ review round 1 (H-003, 2026-10-26)
# Added for the findings of T-050 review round 1 (checks/evidence/2026-10-26-T-050-r1/reviewer/REVIEW.md).
# Each case was derived by hand below before the kernel ran it; the cases
# above are unchanged.

# ---------------------------------------------------------------- G-BT18
# Review round 1 M1 (the reviewer's probe P1): B2's since of a batch
# request is, as for any request, the first tick at whose end it is in
# B2's state (D-074 (c)), not the tick the batch became ready; in G-BT13
# the two are the same tick (20), here they differ (20 and 100).
cases.append({
    "id": "G-BT18",
    "title": "D-050 B2 on a batch request that became ready while its bundle was in use: since is the end of the first tick in B2's state (D-074 (c)), not the ready tick",
    "model": model("G-BT18", gd1_resources,
                   [source("srcp", 10, 1), source("srcw", 20, 1), process("pre", 90, [("machine", 1)]),
                    process("work", 30, [("machine", 1), ("operator", 1)], (1, 1)), sink("done")],
                   [flow("srcp", "pre"), flow("pre", "done"), flow("srcw", "work"), flow("work", "done")],
                   calendars=[{"id": "op", "period": 1440, "shifts": [{"start": 960, "end": 1440}]}]),
    "config": config(1500),
    "validator": {"codes_at": [["STALL_RISK", "/nodes/3/requires"]]},
    "derivation": [
        "G-BT13's resources (G-D1's bundle): the machine breaks down (TTF 960, TTR 480, constant, calendar time) and has no calendar; the operator (human) is on shift [960,1440) of each day. work is a batch process (min = max = 1) needing both; the validator refuses its bundle (D-050 R4, STALL_RISK at /nodes/3/requires: nodes in document order srcp, srcw, pre, work, done) and the test runs it past the validator. pre needs the machine alone (no partner: R4 does not apply). srcp makes one board at 10 into pre (service 90); srcw one board at 20 into work.",
        "t=0 (sources in ID order, then the calendar, then the failure clock; D-048): srcp's arrival at 10 (seq 1), srcw's at 20 (seq 2), the operator's calendar: first change at 960 (seq 3), the machine's clock: FAILURE at 960 (seq 4).",
        "t=10: P1 (r0/srcp/1, seq 1; max_entities 1: no next arrival) enters pre, DISPATCH (seq 5): the machine fits (0 + 1 <= min(S = C = 1, C - D = 1)): P1 at pre 10-100 (SERVICE_COMPLETE seq 6).",
        "t=20: W1 (r0/srcw/1, seq 2; no next) enters work's buffer: 1 = max_size: ready (min = max: no BATCH_TIMEOUT): the batch request (made now, at 20) is at the front of the machine's and the operator's FIFOs (P1's request left the machine's at its grant), DISPATCH (seq 7): the machine is in use (1 + 1 > 1) and the operator off shift (0 + 1 > min(0, 1)): held. At the tick's end the request is held but the machine is in use (by P1): not B2's state (D-050 B2: none of its bundle in use).",
        "t=100: SERVICE_COMPLETE (seq 6): the machine's release (in use 0) wakes the front held request, DISPATCH (seq 8), P1 completes at 100. DISPATCH (seq 8): the machine fits now, the operator does not (S = 0): held again. At the tick's end it is held and neither the machine nor the operator is in use: B2's state from the end of this tick, since = 100 (D-074 (c): the first tick at whose end it is in the state); TTRmax 480 (the machine's constant TTR), X_end 0 (no exceptions): deadline next_fit(max(100, 0) + 480) = next_fit(580): the first tick from 580 at which the machine (always on shift) and the operator are on shift together: 960.",
        "t=960: CAPACITY_CHANGE (seq 3): the next change at 1440 (seq 9); the operator 0 -> 1 wakes the request, DISPATCH (seq 10). FAILURE (seq 4): the machine is idle: down at once (REPAIR at 1440, seq 11). DISPATCH (seq 10): the machine has 0 + 1 > C - D = 0: held. At the tick's end it is still in B2's state (woken and held again within a tick does not reset since, D-074 (c)) and 960 >= 960: ErrStalled.",
        "The batch request has no entity of its own: the error names the batch's oldest member, W1 (D-096 (g)), at work, since 100 (6,000,000 ticks), deadline 960 (57,600,000), at tick 960. The batch became ready at 20 (1,200,000): a reading of since as the ready tick would report 20.",
    ],
    "expected": {"starts": {"pre": [[e("srcp", 1), 10]]}, "completions": [[e("srcp", 1), 100]], "batches": [], "blocks": [],
                 "downs": [["machine", 0, 960, None]], "pending": [], "capacities": [["operator", 960, 1]],
                 "stalled": {"tick": ticks(960), "entity": e("srcw", 1), "node": "work", "since": ticks(100), "deadline": ticks(960),
                             "reason": f"the replication stalled at tick {ticks(960)}: {e('srcw', 1)} waits for node work since tick {ticks(100)} with none of its bundle in use; it was due by tick {ticks(960)}"}},
    "simpy": "not compared: SimPy does not detect stalls",
})

# ---------------------------------------------------------------- G-BT19, G-BT20
# Review round 1 M2 (the reviewer's probe P2): G-D2 (blocking-goldens.json,
# R1 bypassed) plus an unrelated resource-free batch process. ADR-026 D-050
# B1 (ACCEPTED, H-018) reports a circular wait only when no SERVICE_COMPLETE,
# DELAY_COMPLETE or BATCH_TIMEOUT is in the heap nor was discarded for being
# after the horizon (option A of D-096 (g), implemented). G-BT19: the batch's
# wait limit is after the horizon, so B1 stays off for the rest of the run;
# under D-096 (g) B (not counting a limit after the horizon) the same run is
# ErrDeadlock at 7, G-D2's. G-BT20: the limit is within the horizon, so B1
# waits for it under A and B alike and reports once the batch has ended.
gd2_resources = [res("r1"), res("r2")]
gd2_nodes = [source("src", 1), queue("q1", 1), process("s1", 2, [("r1", 1)]), queue("q2", 1), process("s2", 3, [("r2", 1)]),
             source("z", 1), sink("done")]
gd2_flows = [flow("src", "q1"), flow("q1", "s1"), flow("s1", "q2"), flow("q2", "s2"), flow("s2", "q1"), flow("z", "done")]
gd2_codes = [["BLOCKING_CYCLE", "/nodes/1/capacity"], ["BLOCKING_CYCLE", "/nodes/3/capacity"]] + \
            [["NO_REACHABLE_SINK", f"/nodes/{i}"] for i in range(5)]


def gd2_with_batch(name, max_wait):
    return model(name, gd2_resources,
                 gd2_nodes + [source("bsrc", 1, 1), process("bp", 5, (), (1, 2, max_wait))],
                 gd2_flows + [flow("bsrc", "bp"), flow("bp", "done")])


gd2_derivation = [
    "G-D2's model (blocking-goldens.json): src makes a board every minute into q1 (capacity 1) -> s1 (r1, 2 min) -> q2 (capacity 1) -> s2 (r2, 3 min) -> back to q1; z makes a board every minute into done. The validator refuses it (D-050 R1: BLOCKING_CYCLE at q1 and q2; the cycle's nodes reach no sink); the test runs it past the validator. Added: bsrc makes one board b1 at 1 into bp, a batch process with no resource (min 1, max 2, service 5) -> done; bsrc and bp are valid and reach done, so the validator's issues are G-D2's.",
    "t=0: the first arrivals in source ID order (the Program's nodes are in ID order): bsrc at 1, src at 1, z at 1. Every minute src's arrival comes before z's (each schedules its next one in that order).",
    "G-D2's run, by hand: t=1: e1 (r0/src/1) enters q1, DISPATCH: e1 at s1 1-3; z/1 completes. t=2: e2 enters q1, DISPATCH: r1 busy, e2 held. t=3: e1's s1 end: q2 holds 0 of 1, e1 enters it, r1 released (wakes e2); e3 arrives: q1 is full (e2): blocked at src; DISPATCH: e2 granted r1 (3-5), the place it frees in q1 lets e3 in (blocked 3-3, a 0-minute block), e3 held; e1 granted r2 (3-6). t=4: e4 blocked at src. t=5: e2's s1 end: q2 holds 0 (e1 left it at its grant), e2 enters it, r1 released; e5 blocked at src behind e4; DISPATCH: e3 granted r1 (5-7), e4 moves into q1 (blocked 4-5), held; e2 held at s2 (r2 busy). t=6: e1's s2 end: q1 is full (e4): e1 blocked at s2, holding r2; e6 blocked at src. t=7: e3's s1 end (priority 0, before the tick's arrivals): q2 is full (e2): e3 blocked at s1, holding r1. Now nothing is in service or in a delay, e2 (q2, needs r2) and e4 (q1, needs r1) are held and release-bound: the circular wait; G-D2 reports ErrDeadlock at 7 naming the earliest blocked entity still blocked, r0/src/5 at src since 5.",
]

cases.append({
    "id": "G-BT19",
    "title": "a BATCH_TIMEOUT due after the horizon keeps D-050 B1 off for the rest of the run (ADR-026 D-050 B1; D-096 (g) A): G-D2's circular wait runs to the horizon with no error",
    "model": gd2_with_batch("G-BT19", 100),
    "config": config(60),
    "validator": {"codes_at": gd2_codes},
    "derivation": gd2_derivation + [
        "t=1 (bp): b1 enters bp's empty buffer: the oldest; min 1 < max 2, so its wait limit 1 + 100 = 101 is due, after the horizon (60): the BATCH_TIMEOUT is not scheduled (no sequence) but counts as pending for B1 for the rest of the run (ADR-026 D-050 B1: no BATCH_TIMEOUT in the heap nor discarded for being after the horizon; D-096 (g) A). 1 >= min_size, but it has waited 0 < 100: not ready, no dispatch.",
        "t=7: after e3's SERVICE_COMPLETE nothing is in service or in a delay and the held requests are release-bound, but a BATCH_TIMEOUT is pending (b1's, after the horizon): B1 does not report. Under D-096 (g) B (a limit after the horizon is not counted) B1 would report here as in G-D2: ErrDeadlock at 7 (420,000), r0/src/5 at src since 5 (300,000).",
        "t=7..60: every minute src's arrival is blocked at src (release backlog, src/7 ... src/60; none moves on: q1 stays full) and z's completes; nothing else happens: no dispatch, no BATCH_TIMEOUT, B1 stays off. At 60 the next arrivals (61) are after the horizon. b1 never starts.",
        "129 events: 121 arrivals (src 60, z 60, bsrc 1), 4 SERVICE_COMPLETEs (s1 at 3, 5, 7; s2 at 6), 4 DISPATCHes (1, 2, 3, 5). The run ends at the horizon with no error: 121 created, 60 exited (z/1 ... z/60), 61 censored (the 60 of src and b1).",
    ],
    "expected": {
        "starts": {"s1": [[e("src", 1), 1], [e("src", 2), 3], [e("src", 3), 5]], "s2": [[e("src", 1), 3]]},
        "completions": [[e("z", n), n] for n in range(1, 61)],
        "batches": [],
        "blocks": [[e("src", 3), "src", 3, 3], [e("src", 4), "src", 4, 5], [e("src", 5), "src", 5, None], [e("src", 1), "s2", 6, None],
                   [e("src", 6), "src", 6, None], [e("src", 3), "s1", 7, None]] + [[e("src", n), "src", n, None] for n in range(7, 61)],
        "downs": [], "pending": [], "capacities": [],
        "events": 129,
    },
    "simpy": "not compared: SimPy does not detect deadlocks",
})

cases.append({
    "id": "G-BT20",
    "title": "a BATCH_TIMEOUT due within the horizon holds D-050 B1 back until it fires; once the batch it starts has ended, B1 reports the circular wait (D-096 (g), A and B alike)",
    "model": gd2_with_batch("G-BT20", 30),
    "config": config(60),
    "validator": {"codes_at": gd2_codes},
    "derivation": gd2_derivation + [
        "t=1 (bp): b1 enters bp's empty buffer: the oldest; its wait limit 1 + 30 = 31 is within the horizon: BATCH_TIMEOUT at 31 (priority 15), pending for B1. Not ready (waited 0 < 30), no dispatch.",
        "t=7: after e3's SERVICE_COMPLETE the circular wait has formed, but b1's BATCH_TIMEOUT (31) is pending: B1 does not report (under D-096 (g) A and B alike: it is within the horizon).",
        "t=7..30: every minute src's arrival is blocked at src (src/7 ... src/30) and z's completes; B1 stays off (the timeout is pending).",
        "t=31: src/31 blocked, z/31 completes (priority 10); then the BATCH_TIMEOUT (priority 15), live (b1 is still the oldest): 1 >= min_size and 31 - 1 = 30 >= 30: ready: the batch request (no resource: a candidate at once), DISPATCH: bp's batch 1 = (b1) at 31, SERVICE_COMPLETE at 36. The buffer is empty: no new timeout. B1: the batch is in service.",
        "t=32..35: src/32 ... src/35 blocked, z/32 ... z/35 complete; the batch is in service.",
        "t=36: bp's SERVICE_COMPLETE (priority 0, before the tick's arrivals): no bundle to release and no dispatch (a batch process without resources, G-BT21); b1 completes at done. After the event no dispatch is due: nothing in service or in a delay, no BATCH_TIMEOUT pending, entities blocked, e2 and e4 held and release-bound: ErrDeadlock at 36 (2,160,000 ticks) naming the earliest entity still blocked, r0/src/5 at src since 5 (300,000), as G-D2. src's and z's arrivals at 36 are not processed.",
    ],
    "expected": {
        "starts": {"s1": [[e("src", 1), 1], [e("src", 2), 3], [e("src", 3), 5]], "s2": [[e("src", 1), 3]], "bp": [[e("bsrc", 1), 31]]},
        "completions": [[e("z", n), n] for n in range(1, 36)] + [[e("bsrc", 1), 36]],
        "batches": [["bp", 1, 31, [e("bsrc", 1)]]],
        "blocks": [[e("src", 3), "src", 3, 3], [e("src", 4), "src", 4, 5], [e("src", 5), "src", 5, None], [e("src", 1), "s2", 6, None],
                   [e("src", 6), "src", 6, None], [e("src", 3), "s1", 7, None]] + [[e("src", n), "src", n, None] for n in range(7, 36)],
        "downs": [], "pending": [], "capacities": [],
        "deadlock": {"tick": ticks(36), "entity": e("src", 5), "node": "src", "since": ticks(5), "queued": False,
                     "reason": f"the replication deadlocked at tick {ticks(36)}: {e('src', 5)} is blocked at node src since tick {ticks(5)}"},
    },
    "simpy": "not compared: SimPy does not detect deadlocks",
})

# ---------------------------------------------------------------- G-BT21
# Review round 1 L1 (the reviewer's probe P4): a batch process with no
# resource (valid) releases no bundle at its end, so, like any service end
# that releases nothing, it asks for no dispatch.
cases.append({
    "id": "G-BT21",
    "title": "a batch process with no resource: its batch's end releases no bundle and asks for no dispatch (the event count and the sequence numbers)",
    "model": model("G-BT21", [res("m")], [source("src", 10, 4), process("oven", 5, (), (2, 2)), sink("done")],
                   [flow("src", "oven"), flow("oven", "done")]),
    "config": config(100),
    "derivation": [
        "src every 10 minutes, 4 entities (10, 20, 30, 40) -> oven, a batch process with no resource (min = max = 2, service 5) -> done. The resource m is needed by no process (valid; it is only in use 0).",
        "With min = max no BATCH_TIMEOUT is scheduled (D-096 (b)); an entry that does not make the batch ready asks for no dispatch (D-096 (e)).",
        "t=0: the first arrival at 10 (seq 1). t=10: e1 (seq 1; next at 20, seq 2) enters oven's buffer: 1 of 2: not ready, no dispatch.",
        "t=20: e2 (seq 2; next at 30, seq 3) enters: 2 = max_size: ready: the batch request needs no resource (a candidate at once), DISPATCH (seq 4): no bundle (no ObsResource), BatchStart b1 (e1, e2), ServiceStart e1, e2 at 20; SERVICE_COMPLETE at 25 (seq 5). The buffer is empty again.",
        "t=25: SERVICE_COMPLETE (seq 5): no member is blocked and there is no bundle to release, so no dispatch is asked (as a service end that releases nothing); ServiceEnd and Complete e1, then e2, at 25.",
        "t=30: e3 (seq 3; next at 40, seq 6): 1 waits. t=40: e4 (seq 6, the 4th: no next arrival, no sequence): 2 wait: ready, DISPATCH (seq 7): b2 (e3, e4) at 40, SERVICE_COMPLETE at 45 (seq 8). t=45: SERVICE_COMPLETE (seq 8): no dispatch; e3, e4 complete.",
        "8 events: 4 arrivals, 2 DISPATCHes (20, 40), 2 SERVICE_COMPLETEs (25, 45); the BatchStarts in the DISPATCHes seq 4 and 7. A dispatch asked at each batch's end would make 10 events and put b2 in DISPATCH seq 8.",
        "Result over [0,100]: 4 completed, none censored; lead times e1 15, e2 5, e3 15, e4 5; queue waits at oven (no explicit queue: key oven) 10, 0, 10, 0 (from the entry to the batch's start); m in use 0 of 100 minutes (utilization 0); no resource feature applies: no resource_states, no blocking, no buffers; batches[oven]: 2 started, 4 members.",
    ],
    "expected": {
        "starts": {"oven": [[e("src", 1), 20], [e("src", 2), 20], [e("src", 3), 40], [e("src", 4), 40]]},
        "completions": [[e("src", 1), 25], [e("src", 2), 25], [e("src", 3), 45], [e("src", 4), 45]],
        "batches": [["oven", 1, 20, [e("src", 1), e("src", 2)]], ["oven", 2, 40, [e("src", 3), e("src", 4)]]],
        "batch_start_seqs": [["oven", 1, 4], ["oven", 2, 7]],
        "blocks": [], "downs": [], "pending": [], "capacities": [],
        "events": 8,
        "result": {"completed_count": 4, "censored_entities": 0, "lead_times_minutes": [15, 5, 15, 5],
                   "queue_waits_minutes": {"oven": [10, 0, 10, 0]}, "resource_utilization": {"m": 0},
                   "resource_states": None, "blocking": None, "buffers": None,
                   "batches": {"oven": {"batches_started": 2, "members_started": 4}}},
    },
    "simpy": "compare",
})

# ---------------------------------------------------------------- G-BT22
# Review round 1 L2: D-050 B1's report without a blocked entity (Queued)
# for a batch request names its oldest member and the tick the request was
# made (the batch became ready), not the member's entry. Reachable only past
# the validator: with no entity blocked and none in service no unit is in
# use, so a release-bound held request needs more units than the capacity.
cases.append({
    "id": "G-BT22",
    "title": "D-050 B1 with no blocked entity on a batch request (validator bypassed: 2 units of a capacity-1 resource): ErrDeadlock names its oldest member, since the tick the batch became ready",
    "model": model("G-BT22", [res("r")], [source("src", 1, 2), queue("q", 2), process("bp", 5, [("r", 2)], (2, 2)), sink("done")],
                   [flow("src", "q"), flow("q", "bp"), flow("bp", "done")]),
    "config": config(60),
    "validator": {"codes_at": [["RESOURCE_REQUIREMENT_INVALID", "/nodes/2/requires/0/units"]]},
    "derivation": [
        "src makes 2 boards (at 1 and 2) into q (capacity 2 = max_size: valid for BATCH_INVALID) -> bp, a batch process (min = max = 2, service 5) needing 2 units of r, whose capacity is 1 -> done. The validator refuses the requirement (RESOURCE_REQUIREMENT_INVALID at /nodes/2/requires/0/units); the test runs it past the validator. The model has a finite buffer, so B1 runs after every event with no dispatch due (D-064 (a)).",
        "t=0: the first arrival at 1 (seq 1). t=1: e1 (seq 1; next at 2, seq 2) enters q (bp's buffer, 1 of 2): min = max: no BATCH_TIMEOUT, not ready, no dispatch. B1: no entity blocked, no held candidate: nothing.",
        "t=2: e2 (seq 2, the last) enters: 2 = max_size: ready: the batch request is made now (2 minutes), at the front of r's FIFO, DISPATCH (seq 3). DISPATCH: r has 0 + 2 > min(S = 1, C - D = 1): held (it can never fit).",
        "After the DISPATCH no dispatch is due: B1: nothing in service or in a delay, no BATCH_TIMEOUT, no entity blocked, one held candidate, release-bound (0 + 2 > C = 1): ErrDeadlock at 2 (120,000 ticks). With no entity blocked the report names the earliest held request (Queued: 'waits for'): the batch request, by its oldest member e1 (r0/src/1), at bp, since the tick the request was made, 2 (120,000; D-096 (g)). e1 entered the buffer at 1 (60,000): since is not the member's entry.",
    ],
    "expected": {"starts": {}, "completions": [], "batches": [], "blocks": [], "downs": [], "pending": [], "capacities": [],
                 "deadlock": {"tick": ticks(2), "entity": e("src", 1), "node": "bp", "since": ticks(2), "queued": True,
                              "reason": f"the replication deadlocked at tick {ticks(2)}: {e('src', 1)} waits for node bp since tick {ticks(2)}"}},
    "simpy": "not compared: SimPy does not detect deadlocks",
})

# ---------------------------------------------------------------- the SimPy reference (stochastic)
# Fixed before either side ran (T-050): 30 replications of 3000 minutes,
# warmup 300, the tolerance of blocking-goldens.json (T-047). Compared by
# scripts/simpy_batch_reference.py against TestSimPyReferenceExport.
def ref_config(seed):
    return config(3000, warmup=300, replications=30, seed=seed)


def dist(kind, **kw):
    d = {"type": kind}
    d.update(kw)
    return d


reference = {
    "tolerance": TOLERANCE,
    "metrics": ["completed_count", "mean_lead_time_minutes", "utilization/<resource>", "batches_started/<batch process>",
                "members_started/<batch process>", "down_fraction/<resource with breakdowns>"],
    "cases": [
        {"id": "BT-S1", "title": "one chamber, min 2, max 4, max_wait 20; exponential arrivals (mean 15), triangular service (20, 25, 35)",
         "model": model("BT-S1", [res("chamber")], [source("src", dist("exponential", mean=15)),
                                                    process("work", dist("triangular", min=20, mode=25, max=35), [("chamber", 1)], (2, 4, 20)), sink("done")],
                        src_work_done),
         "config": ref_config(101)},
        {"id": "BT-S2", "title": "two chambers, full batches of 3 (no wait limit); exponential arrivals (mean 8) and service (mean 30)",
         "model": model("BT-S2", [res("chamber", 2)], [source("src", dist("exponential", mean=8)),
                                                       process("work", dist("exponential", mean=30), [("chamber", 1)], (3, 3)), sink("done")],
                        src_work_done),
         "config": ref_config(102)},
        {"id": "BT-S3", "title": "a line: prep (exponential 6), an oven batch (min 1, max 5, max_wait 15, service 20), test (triangular 3, 5, 8), 10% scrapped",
         "model": model("BT-S3", [res("prep_r"), res("oven_r"), res("tester")],
                        [source("src", dist("exponential", mean=10)), process("prep", dist("exponential", mean=6), [("prep_r", 1)]),
                         process("oven", 20, [("oven_r", 1)], (1, 5, 15)), process("test", dist("triangular", min=3, mode=5, max=8), [("tester", 1)]),
                         {"id": "d", "type": "decision"}, sink("done"), sink("scrap", "abandoned")],
                        [flow("src", "prep"), flow("prep", "oven"), flow("oven", "test"), flow("test", "d"), flow("d", "done", 0.9), flow("d", "scrap", 0.1)]),
         "config": ref_config(103)},
        {"id": "BT-S4", "title": "a chamber that breaks down (TTF exponential 300, TTR triangular 10, 20, 40), min 3, max 6, max_wait 30",
         "model": model("BT-S4", [res("chamber", breakdowns={"time_to_failure": {"distribution": dist("exponential", mean=300)},
                                                             "time_to_repair": {"distribution": dist("triangular", min=10, mode=20, max=40)}})],
                        [source("src", dist("exponential", mean=12)),
                         process("work", dist("triangular", min=30, mode=35, max=45), [("chamber", 1)], (3, 6, 30)), sink("done")],
                        src_work_done),
         "config": ref_config(104)},
    ],
}

doc = {
    "status": "HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN",
    "semantics_decisions": ["D-047", "D-048", "D-049", "D-050", "D-051", "D-052", "D-054", "D-064", "D-074", "D-081", "D-096"],
    "units": "starts, completions, batches, blocks, downs, capacities and lead times in minutes; Result members in ticks (1 ms)",
    "notes": ("batches are [process, batch number, start time, [members in FIFO order]] of every BatchStart (D-054); batch_start_seqs, where given, "
              "the sequence number of the DISPATCH that started each; starts are each member's service start; blocks [entity, node, from, to]; "
              "downs, pending and capacities as in calendar-goldens.json; events the replication's event count. Each case's derivation is the "
              "hand derivation of its expected values (assembled by checks/evidence/2026-10-26-T-050/batch_goldens_gen.py, which only builds the JSON). "
              "G-BT1-G-BT5 are ADR-026 D-054's G-T1-G-T5, renamed because G-T1, G-T2, G-T4 and G-T5 name D-048's same-tick goldens of T-048 and T-049."),
    "cases": cases,
    "reference": reference,
}
json.dump(doc, sys.stdout, indent=1, ensure_ascii=False)
sys.stdout.write("\n")
