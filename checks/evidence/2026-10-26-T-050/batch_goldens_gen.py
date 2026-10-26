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
    a = {"distribution": const(every)}
    if max_entities is not None:
        a["max_entities"] = max_entities
    return {"id": sid, "type": "source", "entity_type": et, "arrival": a}


def table_source(sid, period, rows, et="board"):
    return {"id": sid, "type": "source", "entity_type": et,
            "arrival": {"schedule": {"process": "table", "period": period,
                                     "releases": [{"at": at, "quantity": q} for at, q in rows]}}}


def process(pid, service, requires=(), batch=None):
    n = {"id": pid, "type": "process", "service_time": {"distribution": const(service)},
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
    "simpy": "compare",
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
    "simpy": "not compared: the arrival and the wait limit fall on the same tick (D-048's order, which SimPy's scheduling order does not reproduce)",
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
    "simpy": "compare",
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
    "simpy": "compare",
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
}
json.dump(doc, sys.stdout, indent=1, ensure_ascii=False)
sys.stdout.write("\n")
