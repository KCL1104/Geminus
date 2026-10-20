#!/usr/bin/env python3
"""Independent SimPy reference for finite buffers and blocking (T-047, TEST-034).

AGENTS.md §7 and ADR-006: SimPy is only an independent reference. Nothing in
the Go engine imports, calls or embeds this script or SimPy; the script reads
the shared fixture (testdata/simulation/blocking-goldens.json) and, for the
stochastic cases, a JSON file of the Go engine's per-replication results that
`make simulation-reference-test` writes first.

The models are interpreted with SimPy's own primitives, as ADR-026 D-049 plans
it ("SimPy"): every resource is a simpy.Resource; a finite buffer of capacity
B is a simpy.Resource(capacity=B) of slots; an entity finishing at a process
requests a slot of the next buffer while it still holds its machines (it is
blocked when the request is not granted at once), releases its machines when
it has the slot, then requests the next machines and gives the slot back
once it has them. A source's entity asks for a slot when it arrives (the
release backlog). Routing draws use Python's own random.Random, never the
engine's streams, so stochastic agreement is statistical: per-replication
means within the tolerance fixed in the fixture before either side was run.

Checks:
  * deterministic cases marked "simpy": "compare": service starts,
    completions, every block interval and the Result members of the case
    (completed count, censored entities, blocking, buffers, resource states,
    utilization, lead times, queue waits) equal the hand-derived values;
  * G-D2 (illustrative, D-050): SimPy does not detect deadlocks; it shows the
    last service start and that from the engine's detection tick on both
    machines are held by entities waiting for a slot and nothing starts again;
  * stochastic cases (reference.cases): per metric, |mean_go - mean_simpy| <=
    z*sqrt(var_go/n + var_simpy/n) + rel*max(|means|) + abs.

Limits of the interpretation, checked before use: units are 1; a bundle is
taken one resource at a time while holding the earlier ones, the one it
shares with other processes first, which equals the engine's all-or-nothing
grant only when at most one resource of the bundle is shared (its own units
are free whenever that one is); the script refuses any other model.

    python scripts/simpy_reference.py --go-results bin/simpy-reference-go.json
"""
import argparse
import json
import math
import random
import sys
from pathlib import Path

import simpy

ROOT = Path(__file__).resolve().parents[1]
FIXTURE = ROOT / "testdata/simulation/blocking-goldens.json"
SIMPY_VERSION = "4.1.2"
TICK = 60_000  # ticks per model minute (the fixtures are in minutes)


class Step:
    """A step function of time integrated over the window [w, h]."""

    def __init__(self, w, h):
        self.w, self.h = w, h
        self.level, self.since, self.area = 0, 0.0, 0.0

    def set(self, level, t):
        a, b = max(self.since, self.w), min(t, self.h)
        if b > a:
            self.area += self.level * (b - a)
        self.level, self.since = level, t

    def add(self, delta, t):
        self.set(self.level + delta, t)

    def total(self):
        a, b = max(self.since, self.w), self.h
        return self.area + (self.level * (b - a) if b > a else 0.0)


class Model:
    def __init__(self, ir):
        self.ir = ir
        self.nodes = {n["id"]: n for n in ir["nodes"]}
        self.capacity = {r["id"]: r["capacity"] for r in ir["resources"]}
        self.out = {}
        for f in sorted(ir["flows"], key=lambda f: f["id"]):
            if f["routing"]["probability"] > 0:
                self.out.setdefault(f["from"], []).append((f["to"], f["routing"]["probability"]))
        # a queue names the buffer of the process it feeds
        self.buffer_of = {}
        self.finite = {}  # process -> capacity
        self.label = {}  # process -> queue-metric key
        for n in ir["nodes"]:
            if n["type"] == "process":
                self.buffer_of[n["id"]] = n["id"]
                self.label.setdefault(n["id"], n["id"])
        for n in ir["nodes"]:
            if n["type"] == "queue":
                (p, _), = self.out[n["id"]]
                self.buffer_of[n["id"]] = p
                self.label[p] = n["id"]
                if n.get("capacity"):
                    self.finite[p] = n["capacity"]
        users = {}
        for n in ir["nodes"]:
            for r in n.get("requires", []):
                if r["units"] != 1:
                    raise SystemExit(f"unsupported: {n['id']} needs {r['units']} units")
                users.setdefault(r["resource_id"], set()).add(n["id"])
        # A bundle is taken one resource at a time: the one it shares with
        # other processes first, then its own (free whenever the shared one
        # is: a unit of its own is only ever held together with it).
        self.order = {}
        for n in ir["nodes"]:
            req = sorted(r["resource_id"] for r in n.get("requires", []))
            shared = [r for r in req if users[r] != {n["id"]}]
            if len(shared) > 1:
                raise SystemExit(f"unsupported: {n['id']}'s bundle {req} shares {shared}: one at a time would not be all-or-nothing")
            self.order[n["id"]] = shared + [r for r in req if r not in shared]

    def requires(self, node):
        return self.order[node]


def sample(dist, rng):
    if dist["type"] == "constant":
        return float(dist["value"])
    if dist["type"] == "exponential":
        return rng.expovariate(1.0 / dist["mean"])
    raise SystemExit(f"unsupported distribution {dist['type']}")


def simulate(ir, config, rep, seed, record=False, until=None):
    """One replication; returns the per-replication statistics (minutes)."""
    m = Model(ir)
    env = simpy.Environment()
    rng = random.Random(seed)
    w, h = config["warmup"]["value"], config["horizon"]["value"]
    if until is None:
        until = h
    machines = {r: simpy.Resource(env, capacity=c) for r, c in m.capacity.items()}
    slots = {p: simpy.Resource(env, capacity=c) for p, c in m.finite.items()}
    in_use = {r: Step(w, h) for r in m.capacity}
    held = {r: Step(w, h) for r in m.capacity}
    full = {p: Step(w, h) for p in m.finite}
    blocked_at = {}
    stats = {"completed": 0, "leads": [], "reworks": 0, "failures": 0, "blocks_started": {}, "blocks_done": {}, "block_minutes": {},
             "waits": {}, "starts": {}, "completions": [], "blocks": [], "last_start": None}

    def resolve(node):
        """Follows routes through decisions, failure and rework nodes."""
        passed = []
        while True:
            outs = m.out[node]
            if m.nodes[node]["type"] == "decision":
                u, acc, to = rng.random(), 0.0, outs[-1][0]
                for t, p in outs:
                    acc += p
                    if u < acc:
                        to = t
                        break
            else:
                (to, _), = outs
            node = to
            typ = m.nodes[node]["type"]
            if typ in ("failure", "rework"):
                passed.append(typ)
                continue
            if typ == "decision":
                continue
            return node, passed

    def mark_full(p):
        if p in full:
            full[p].set(1 if slots[p].count == slots[p].capacity else 0, env.now)

    def entity(eid, source, arrived):
        here = source
        holding = []  # (resource, request) of the machines this entity holds
        while True:
            to, passed = resolve(here)
            for kind in passed:
                if w <= env.now <= h:
                    stats["reworks" if kind == "rework" else "failures"] += 1
            typ = m.nodes[to]["type"]
            slot = None
            if typ in ("queue", "process") and m.buffer_of[to] in slots:
                p = m.buffer_of[to]
                slot = slots[p].request()
                if not slot.triggered:
                    since = env.now
                    blocked_at.setdefault(here, Step(w, h)).add(1, since)
                    for r, _ in holding:
                        held[r].add(1, since)
                    if w <= since <= h:
                        stats["blocks_started"][here] = stats["blocks_started"].get(here, 0) + 1
                    entry = [eid, here, since, None]
                    if record:
                        stats["blocks"].append(entry)
                    yield slot
                    entry[3] = env.now
                    blocked_at[here].add(-1, env.now)
                    for r, _ in holding:
                        held[r].add(-1, env.now)
                    if since >= w and env.now <= h:
                        stats["blocks_done"][here] = stats["blocks_done"].get(here, 0) + 1
                        stats["block_minutes"][here] = stats["block_minutes"].get(here, 0.0) + (env.now - since)
                mark_full(p)
            for r, req in holding:
                machines[r].release(req)
                in_use[r].add(-1, env.now)
            holding.clear()
            if typ == "sink":
                if m.nodes[to].get("outcome", "completed") == "completed":
                    if w <= env.now <= h:
                        stats["completed"] += 1
                        if arrived >= w:
                            stats["leads"].append(env.now - arrived)
                    if record:
                        stats["completions"].append([eid, env.now])
                return
            if typ == "delay":
                yield env.timeout(sample(m.nodes[to]["delay_time"]["distribution"], rng))
                here = to
                continue
            p = m.buffer_of[to]
            entered = env.now
            for r in m.requires(p):
                req = machines[r].request()
                yield req
                holding.append((r, req))
                in_use[r].add(1, env.now)
            if slot is not None:
                slots[p].release(slot)
                mark_full(p)
            if entered >= w and env.now <= h:
                stats["waits"].setdefault(m.label[p], []).append(env.now - entered)
            if record:
                stats["starts"].setdefault(p, []).append([eid, env.now])
            stats["last_start"] = env.now
            yield env.timeout(sample(m.nodes[p]["service_time"]["distribution"], rng))
            here = p

    created = {"n": 0}

    def source(sid):
        node = m.nodes[sid]
        n = 0
        while True:
            yield env.timeout(sample(node["arrival"]["distribution"], rng))
            n += 1
            created["n"] += 1
            eid = f"r{rep}/{sid}/{n}"
            env.process(entity(eid, sid, env.now))
            if node["arrival"].get("max_entities") and n >= node["arrival"]["max_entities"]:
                return

    for sid in sorted(n["id"] for n in ir["nodes"] if n["type"] == "source"):
        env.process(source(sid))
    env.run(until=math.nextafter(until, math.inf))
    T = h - w
    out = {
        "completed_count": stats["completed"],
        "mean_lead_time_minutes": (sum(stats["leads"]) / len(stats["leads"])) if stats["leads"] else None,
        "leads": stats["leads"],
        "reworks": stats["reworks"],
        "utilization": {r: in_use[r].total() / (m.capacity[r] * T) for r in m.capacity},
        "in_use": {r: in_use[r].total() for r in m.capacity},
        "blocked": {r: held[r].total() for r in m.capacity},
        "full": {m.label[p]: full[p].total() for p in full},
        "blocked_entities": {n: s.total() for n, s in blocked_at.items()},
        "blocks_started": stats["blocks_started"],
        "blocks_done": stats["blocks_done"],
        "block_minutes": stats["block_minutes"],
        "waits": stats["waits"],
        "starts": stats["starts"],
        "completions": stats["completions"],
        "blocks": stats["blocks"],
        "last_start": stats["last_start"],
        "created": created["n"],
        "machines_held_by_blocked": {r: held[r].level for r in m.capacity},
        "machines_in_use": {r: in_use[r].level for r in m.capacity},
        "finite": sorted(m.finite),
        "labels": m.label,
    }
    return out


def quantile7(x, p):
    s = sorted(x)
    hh = (len(s) - 1) * p
    lo, hi = math.floor(hh), math.ceil(hh)
    return s[lo] + (hh - lo) * (s[hi] - s[lo])


failures = []


def check(cond, what):
    if not cond:
        failures.append(what)
    return cond


def possible_blockers(ir):
    """Nodes whose route can reach a finite buffer: the Result's blocking keys."""
    m = Model(ir)
    keys = set()
    for n in ir["nodes"]:
        if n["type"] not in ("source", "process", "delay"):
            continue
        seen, stack = set(), [t for t, _ in m.out.get(n["id"], [])]
        while stack:
            x = stack.pop()
            if x in seen:
                continue
            seen.add(x)
            t = m.nodes[x]["type"]
            if t in ("queue", "process") and m.buffer_of[x] in m.finite:
                keys.add(n["id"])
                break
            if t in ("decision", "failure", "rework"):
                stack.extend(to for to, _ in m.out.get(x, []))
    return keys


def deterministic(case):
    ir, cfg, exp = case["model"], case["config"], case["expected"]
    r = simulate(ir, cfg, 0, 1, record=True)
    cid = case["id"]
    want_starts = {n: [list(x) for x in v] for n, v in exp["starts"].items()}
    got_starts = {n: [[e, t] for e, t in v] for n, v in r["starts"].items()}
    check(got_starts == want_starts, f"{cid}: starts {got_starts} != {want_starts}")
    check(r["completions"] == [list(x) for x in exp["completions"]], f"{cid}: completions {r['completions']} != {exp['completions']}")
    want_blocks = [list(b) for b in exp["blocks"]]
    check(r["blocks"] == want_blocks, f"{cid}: blocks {r['blocks']} != {want_blocks}")
    res = exp.get("result")
    if not res:
        return
    check(r["completed_count"] == res["completed_count"], f"{cid}: completed {r['completed_count']} != {res['completed_count']}")
    censored = r["created"] - len(r["completions"])
    check(censored == res["censored_entities"], f"{cid}: censored {censored} != {res['censored_entities']}")
    for k, v in (res.get("resource_utilization") or {}).items():
        check(abs(r["utilization"][k] - v) < 1e-12, f"{cid}: utilization {k} {r['utilization'][k]} != {v}")
    if res.get("lead_times_minutes"):
        check(sorted(r["leads"]) == sorted(res["lead_times_minutes"]), f"{cid}: leads {r['leads']} != {res['lead_times_minutes']}")
    for q, waits in (res.get("queue_waits_minutes") or {}).items():
        check(sorted(r["waits"].get(q, [])) == sorted(waits), f"{cid}: waits at {q} {r['waits'].get(q)} != {waits}")
    if res.get("blocking") is None:
        check(not r["finite"], f"{cid}: finite buffers in a case without blocking members")
        return
    for node in possible_blockers(ir) | set(res["blocking"]):
        want = res["blocking"].get(node)
        got = {"blocks_started": r["blocks_started"].get(node, 0), "blocks_completed": r["blocks_done"].get(node, 0),
               "completed_block_ticks": round(r["block_minutes"].get(node, 0.0) * TICK),
               "blocked_entity_ticks": round(r["blocked_entities"].get(node, 0.0) * TICK)}
        check(got == want, f"{cid}: blocking[{node}] {got} != {want}")
    for q, want in res["buffers"].items():
        got = round(r["full"].get(q, 0.0) * TICK)
        check(got == want["full_ticks"], f"{cid}: buffers[{q}] {got} != {want['full_ticks']}")
    for rid, want in res["resource_states"].items():
        used, blocked = round(r["in_use"][rid] * TICK), round(r["blocked"][rid] * TICK)
        got = {"in_use_ticks": used, "blocked_ticks": blocked, "scheduled_processing_ticks": used - blocked}
        check(got == want, f"{cid}: resource_states[{rid}] {got} != {want}")


def deadlock(case):
    """G-D2, illustrative: no service starts after the last one, and from the
    engine's detection tick both machines are held by blocked entities."""
    exp = case["expected"]["deadlock"]
    tick = exp["tick"] / TICK
    r = simulate(case["model"], case["config"], 0, 1, record=True, until=tick)
    last = max(t for v in r["starts"].values() for _, t in v)
    check(r["machines_held_by_blocked"] == {"r1": 1, "r2": 1}, f"G-D2: at {tick} the machines held by blocked entities are {r['machines_held_by_blocked']}")
    long = simulate(case["model"], case["config"], 0, 1, record=True, until=case["config"]["horizon"]["value"])
    later = [t for v in long["starts"].values() for _, t in v if t > last]
    check(not later, f"G-D2: services start after {last}: {later}")
    return last, tick


def stochastic(ref, go):
    tol = ref["tolerance"]
    report = []
    for case in ref["cases"]:
        cid, cfg = case["id"], case["config"]
        n = cfg["replications"]
        sims = [simulate(case["model"], cfg, i, 10_007 * cfg["seed"] + i) for i in range(n)]
        g = go[cid]
        if len(g) != n:
            check(False, f"{cid}: {len(g)} Go replications, {n} expected")
            continue
        T = cfg["horizon"]["value"] - cfg["warmup"]["value"]
        m = Model(case["model"])
        metrics = {"completed_count": ([x["completed_count"] for x in g], [s["completed_count"] for s in sims]),
                   "mean_lead_time_minutes": ([x["mean_lead_time_minutes"] for x in g], [s["mean_lead_time_minutes"] for s in sims]),
                   "reworks": ([x["reworks"] for x in g], [s["reworks"] for s in sims])}
        for rid, c in m.capacity.items():
            metrics[f"utilization/{rid}"] = ([x["utilization"][rid] for x in g], [s["utilization"][rid] for s in sims])
            metrics[f"blocked_fraction/{rid}"] = ([x["blocked_ticks"][rid] / TICK / (c * T) for x in g], [s["blocked"][rid] / (c * T) for s in sims])
        for p in m.finite:
            q = m.label[p]
            metrics[f"full_fraction/{q}"] = ([x["full_ticks"][q] / TICK / T for x in g], [s["full"].get(q, 0.0) / T for s in sims])
        for node in sorted(possible_blockers(case["model"])):
            metrics[f"blocks_started/{node}"] = ([x["blocks_started"][node] for x in g], [s["blocks_started"].get(node, 0) for s in sims])
            metrics[f"blocked_entities_mean/{node}"] = ([x["blocked_entity_ticks"][node] / TICK / T for x in g],
                                                        [s["blocked_entities"].get(node, 0.0) / T for s in sims])
        for name, (a, b) in metrics.items():
            if any(v is None for v in a + b):
                check(False, f"{cid} {name}: a replication without a value")
                continue
            ma, mb = sum(a) / n, sum(b) / n
            va = sum((v - ma) ** 2 for v in a) / (n - 1)
            vb = sum((v - mb) ** 2 for v in b) / (n - 1)
            bound = tol["z"] * math.sqrt(va / n + vb / n) + tol["rel"] * max(abs(ma), abs(mb)) + tol["abs"]
            ok = abs(ma - mb) <= bound
            report.append({"case": cid, "metric": name, "go": ma, "simpy": mb, "difference": ma - mb, "bound": bound, "pass": ok})
            check(ok, f"{cid} {name}: Go {ma:.6g}, SimPy {mb:.6g}, |difference| {abs(ma - mb):.3g} > {bound:.3g}")
    return report


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--go-results", required=True)
    ap.add_argument("--report")
    args = ap.parse_args()
    if simpy.__version__ != SIMPY_VERSION:
        print(f"SimPy {simpy.__version__}, the reference is pinned to {SIMPY_VERSION} (requirements-spec.txt)", file=sys.stderr)
        return 2
    doc = json.loads(FIXTURE.read_text())
    compared, skipped = [], []
    last = None
    for case in doc["cases"]:
        mode = case.get("simpy", "")
        if mode == "compare":
            deterministic(case)
            compared.append(case["id"])
        elif mode.startswith("illustrative"):
            last = deadlock(case)
            compared.append(case["id"] + " (illustrative)")
        else:
            skipped.append(f"{case['id']}: {mode}")
    go = json.loads(Path(args.go_results).read_text())
    report = stochastic(doc["reference"], go)
    summary = {"simpy": simpy.__version__, "deterministic_cases": compared, "not_compared": skipped,
               "g_d2_last_service_start_minutes": last[0] if last else None, "g_d2_deadlock_minutes": last[1] if last else None,
               "stochastic_comparisons": len(report), "stochastic_passed": sum(1 for x in report if x["pass"]),
               "tolerance": doc["reference"]["tolerance"], "failures": failures}
    if args.report:
        Path(args.report).write_text(json.dumps({"summary": summary, "comparisons": report}, indent=2) + "\n")
    print(json.dumps(summary, indent=2))
    for x in report:
        print(f"{x['case']:>3} {x['metric']:<34} go {x['go']:<12.6g} simpy {x['simpy']:<12.6g} bound {x['bound']:<10.3g} {'ok' if x['pass'] else 'FAIL'}")
    if failures:
        print(f"FAIL simulation reference: {len(failures)} difference(s)", file=sys.stderr)
        return 1
    print("PASS simulation reference")
    return 0


if __name__ == "__main__":
    sys.exit(main())
