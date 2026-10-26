#!/usr/bin/env python3
"""Independent SimPy reference for batch processes (ADR-026 D-054) and
changeovers (D-055), T-050, TEST-034.

AGENTS.md §7 and ADR-006: SimPy is only an independent reference. Nothing in
the Go engine imports, calls or embeds this script or SimPy; the script reads
the shared fixtures (testdata/simulation/batch-goldens.json and
setup-goldens.json) and, for the stochastic cases, a JSON file of the Go
engine's per-replication results that `make simulation-reference-test`
writes first (TestSimPyReferenceExport).

The models are run on SimPy's event queue (simpy.Environment, its time and
its heap of events), with the stations written here in Python from the
approved text, never from the Go code:

  * D-055's SimPy plan ("unit 選擇是自訂規則 ... 以 Python 自寫的 dispatcher
    管理 unit 狀態，搭配 SimPy 的時間推進"): each resource keeps its units
    (in use, down, the entity type it is set up for); a grant takes, of the
    units neither in use nor down, the one of least index set up for the
    entity's type, else the one of least index; a unit set up for another
    type changes over first (the transition's time, else the default's,
    else 0); the service follows; one completion.
  * D-054: a batch process's buffer holds its entities in entry order; the
    batch is ready when max_size wait, or min_size do and the oldest has
    waited max_wait (checked at every entry, after every grant and at the
    oldest member's wait limit, a timeout that an older member's leaving
    makes stale); a ready batch waits for a free unit, then takes
    min(waiting, max_size) members in FIFO order, one service, and at its
    end its members move on in FIFO order.
  * Breakdowns (D-051, D-074 (a) B, D-097 (c)): per unit a failure clock in
    calendar time; at its failure, the idle unit of least index goes down
    for one repair time, or, with none idle, the failure waits (FIFO) for
    the next unit a job releases or a repair frees.

Deviation from ADR-026 D-054's SimPy plan (a simpy.Store and a batcher
process requesting the chamber): SimPy's Store and Resource grant at its
NORMAL priority in creation order, which cannot express D-048's order of
one instant (service completions, then arrivals and delay ends, then
failures, repairs and wait limits, then the dispatch), on which the
deterministic cases depend. So every event here is a simpy Event scheduled
on the environment with an explicit priority (0 completions, 1 arrivals and
delay ends, 2 failures, repairs and wait limits, 3 the dispatch), ties in
scheduling order, and the stations above decide; the batcher's rule is the
same.

Time is in ticks (1 ms, D-005): constant durations are exact integers
(round half up of value x ticks per unit); random samples from Python's own
random.Random (expovariate, triangular, lognormvariate with sigma^2 = ln(1 +
cv^2), weibullvariate), continuous, never the engine's streams or
samplers, so stochastic agreement is statistical.

Limits, checked before use (a model outside them is refused, never run as
if the member were absent): no finite buffer (queue capacity), no
calendar, no release schedule but a table, every process needs at most one
resource, of one unit, needed by no other process; no empirical
distribution. The cases outside them are marked "refused" in the fixtures
and must be refused here.

Checks:
  * deterministic cases marked "simpy": "compare": service starts,
    completions, every batch with its members, every changeover with its
    unit and length, every downtime and waiting failure, and the Result
    members of the case equal the hand-derived values;
  * stochastic cases (reference.cases): per metric, |mean_go - mean_simpy| <=
    z*sqrt(var_go/n + var_simpy/n) + rel*max(|means|) + abs, the tolerance
    fixed in the fixture before either side was run.

    python scripts/simpy_batch_reference.py --go-results bin/simpy-reference-go.json
"""
import argparse
import json
import math
import random
import sys
from fractions import Fraction
from pathlib import Path

import simpy
from simpy.events import Event

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = [ROOT / "testdata/simulation/batch-goldens.json", ROOT / "testdata/simulation/setup-goldens.json"]
SIMPY_VERSION = "4.1.2"
DISTRIBUTIONS = ("constant", "exponential", "triangular", "lognormal", "weibull")
UNIT_MS = {"millisecond": 1, "second": 1000, "minute": 60_000, "hour": 3_600_000, "day": 86_400_000}
COMPLETE, ARRIVE, STATE, DISPATCH = 0, 1, 2, 3  # D-048's order in one instant


def ticks(value, per):
    """A model-time value in ticks: round half up, at least one tick for a
    positive value (D-005, D-082 (b))."""
    t = math.floor(Fraction(value) * per + Fraction(1, 2))
    return 1 if t == 0 and value > 0 else t


class Line:
    """A model the reference accepts (see the module docstring)."""

    def __init__(self, ir):
        self.ir = ir
        self.per = UNIT_MS[ir.get("time_unit", "minute")]
        self.nodes = {n["id"]: n for n in ir["nodes"]}
        if ir.get("calendars") or any(r.get("calendar_id") for r in ir["resources"]):
            raise SystemExit("unsupported: a calendar")
        self.out = {}
        for f in sorted(ir["flows"], key=lambda f: f["id"]):
            if f["routing"]["probability"] > 0:
                self.out.setdefault(f["from"], []).append((f["to"], f["routing"]["probability"]))
        self.buffer_of, self.label = {}, {}
        for n in ir["nodes"]:
            if n["type"] == "process":
                self.buffer_of[n["id"]] = n["id"]
                self.label.setdefault(n["id"], n["id"])
        for n in ir["nodes"]:
            if n["type"] == "queue":
                if n.get("capacity"):
                    raise SystemExit(f"unsupported: {n['id']} is a finite buffer")
                (p, _), = self.out[n["id"]]
                self.buffer_of[n["id"]] = p
                self.label[p] = n["id"]
            if n["type"] == "source":
                s = n["arrival"].get("schedule")
                if s and s["process"] != "table":
                    raise SystemExit(f"unsupported: {n['id']}'s {s['process']} schedule")
        self.resource = {}  # process -> its one resource, or None
        users = {}
        for n in ir["nodes"]:
            if n["type"] != "process":
                continue
            req = n.get("requires", [])
            if len(req) > 1 or any(r["units"] != 1 for r in req):
                raise SystemExit(f"unsupported: {n['id']}'s bundle {req}")
            self.resource[n["id"]] = req[0]["resource_id"] if req else None
            for r in req:
                users.setdefault(r["resource_id"], []).append(n["id"])
        for r, ps in users.items():
            if len(ps) > 1:
                raise SystemExit(f"unsupported: {r} is needed by {ps}")
        self.res = {r["id"]: r for r in ir["resources"]}
        dists = []
        for n in ir["nodes"]:
            for member in ("arrival", "service_time", "delay_time"):
                if n.get(member) and "distribution" in n[member]:
                    dists.append(n[member]["distribution"])
        for r in ir["resources"]:
            if r.get("breakdowns"):
                dists += [r["breakdowns"]["time_to_failure"]["distribution"], r["breakdowns"]["time_to_repair"]["distribution"]]
            s = r.get("setups") or {}
            if s.get("default"):
                dists.append(s["default"])
            dists += [t["distribution"] for t in s.get("transitions", [])]
        for d in dists:
            if d["type"] not in DISTRIBUTIONS:
                raise SystemExit(f"unsupported distribution {d['type']}")

    def draw(self, dist, rng):
        """A duration in ticks: exact for a constant, else continuous."""
        if dist["type"] == "constant":
            return ticks(dist["value"], self.per)
        if dist["type"] == "exponential":
            v = rng.expovariate(1.0 / dist["mean"])
        elif dist["type"] == "triangular":
            v = rng.triangular(dist["min"], dist["max"], dist["mode"])
        elif dist["type"] == "lognormal":
            s2 = math.log(1 + dist["cv"] ** 2)
            v = rng.lognormvariate(math.log(dist["mean"]) - s2 / 2, math.sqrt(s2))
        else:
            v = rng.weibullvariate(dist["scale"], dist["shape"])
        return v * self.per


class Step:
    """A step function of time integrated over [w, h]."""

    def __init__(self, w, h):
        self.w, self.h, self.level, self.since, self.area = w, h, 0, 0.0, 0.0

    def set(self, level, t):
        a, b = max(self.since, self.w), min(t, self.h)
        if b > a:
            self.area += self.level * (b - a)
        self.level, self.since = level, t

    def total(self):
        a, b = max(self.since, self.w), self.h
        return self.area + (self.level * (b - a) if b > a else 0.0)


def overlap(a, b, w, h):
    return max(min(b, h) - max(a, w), 0)


def simulate(ir, config, rep, seed):
    """One replication; returns its facts and statistics (ticks)."""
    m = Line(ir)
    per = m.per
    env = simpy.Environment()
    rng = random.Random(seed)
    w, h = ticks(config["warmup"]["value"], UNIT_MS[config["warmup"]["unit"]]), ticks(config["horizon"]["value"], UNIT_MS[config["horizon"]["unit"]])
    st = {"starts": {}, "completions": [], "batches": [], "setups": [], "downs": [], "pending": [], "leads": [], "waits": {},
          "completed": 0, "created": 0, "exited": 0, "failures": 0, "reworks": 0}
    entities = {}
    in_use = {r: Step(w, h) for r in m.res}
    down_level = {r: Step(w, h) for r in m.res}
    changeovers = {r: [] for r in m.res}  # (start, end)
    downs_open = {}
    units = {r: [{"busy": False, "down": False, "state": (x.get("setups") or {}).get("initial")} for _ in range(x["capacity"])]
             for r, x in m.res.items()}
    pending = {r: [] for r in m.res}
    clock_unit = {r: [None] * x["capacity"] for r, x in m.res.items()}
    queue = {p: [] for p in m.resource}
    holding = {}  # entity in service -> its unit
    bbuf = {p: [] for p in m.resource}
    pend, gen, oldest, number = {p: False for p in m.resource}, {p: 0 for p in m.resource}, {p: 0 for p in m.resource}, {p: 0 for p in m.resource}
    serial = [0]
    arrived = {n: 0 for n in m.nodes}
    dispatching = [False]

    def at(delay, prio, fn):
        ev = Event(env)
        ev._ok, ev._value = True, None
        ev.callbacks.append(lambda _e: fn())
        env.schedule(ev, prio, delay)

    def inwin(t):
        return w <= t <= h

    def need_dispatch():
        if not dispatching[0]:
            dispatching[0] = True
            at(0, DISPATCH, dispatch)

    def used(r):
        return sum(1 for u in units[r] if u["busy"])

    def ndown(r):
        return sum(1 for u in units[r] if u["down"])

    def set_levels(r):
        in_use[r].set(used(r), env.now)
        down_level[r].set(ndown(r), env.now)

    # Arrivals (D-053 tables; a constant or random interarrival).
    def start_source(src):
        n = m.nodes[src]
        s = n["arrival"].get("schedule")
        if s:
            rows = [(ticks(x["at"], per), x["quantity"]) for x in s["releases"]]
            schedule_row(src, ticks(s["period"], per), rows, 0, 0)
        else:
            schedule_arrival(src)

    def schedule_arrival(src):
        n = m.nodes[src]
        lim = n["arrival"].get("max_entities")
        if lim and arrived[src] >= lim:
            return
        at(m.draw(n["arrival"]["distribution"], rng), ARRIVE, lambda: arrive(src))

    def arrive(src):
        # The arrival counts before the next is drawn: max_entities stops
        # the source at its last one (the kernel's order).
        arrived[src] += 1
        schedule_arrival(src)
        create(src, counted=True)

    def schedule_row(src, period, rows, cycle, row):
        t = cycle * period + rows[row][0]
        at(t - env.now, ARRIVE, lambda: release_row(src, period, rows, cycle, row))

    def release_row(src, period, rows, cycle, row):
        lim = m.nodes[src]["arrival"].get("max_entities")
        nxt, ncycle = row + 1, cycle
        if nxt == len(rows):
            nxt, ncycle = 0, cycle + 1
        if not (lim and arrived[src] + rows[row][1] >= lim):
            schedule_row(src, period, rows, ncycle, nxt)
        for _ in range(rows[row][1]):
            if lim and arrived[src] >= lim:
                break
            create(src)

    def create(src, counted=False):
        if not counted:
            arrived[src] += 1
        e = f"r0/{src}/{arrived[src]}"
        entities[e] = {"created": env.now, "product": m.nodes[src]["entity_type"]}
        st["created"] += 1
        route(e, src)

    def route(e, frm):
        n = frm
        while True:
            outs = m.out[n]
            if len(outs) == 1:
                n = outs[0][0]
            else:
                x, acc = rng.random(), 0.0
                for to, pr in outs:
                    acc += pr
                    if x < acc:
                        n = to
                        break
                else:
                    n = outs[-1][0]
            t = m.nodes[n]["type"]
            if t in ("failure", "rework"):
                if inwin(env.now):
                    st["failures" if t == "failure" else "reworks"] += 1
                continue
            if t == "decision":
                continue
            break
        t = m.nodes[n]["type"]
        if t in ("queue", "process"):
            enqueue(e, m.buffer_of[n])
        elif t == "delay":
            at(m.draw(m.nodes[n]["delay_time"]["distribution"], rng), ARRIVE, lambda: route(e, n))
        elif t == "sink":
            st["exited"] += 1
            if m.nodes[n].get("outcome", "completed") == "completed":
                st["completions"].append([e, env.now / per])  # every one: the observation-level facts
                if inwin(env.now):
                    st["completed"] += 1
                    if entities[e]["created"] >= w:
                        st["leads"].append((env.now - entities[e]["created"]) / per)
            del entities[e]
        else:
            raise SystemExit(f"routed into {t}")

    def enqueue(e, p):
        entities[e]["enter"] = env.now
        if m.nodes[p].get("batch"):
            serial[0] += 1
            bbuf[p].append((e, env.now, serial[0]))
            batch_check(p)
        else:
            queue[p].append(e)
            need_dispatch()

    # D-054: the batch's readiness.
    def batch_params(p):
        b = m.nodes[p]["batch"]
        wait = ticks(b["max_wait"], per) if b.get("max_wait") is not None else 0
        return b["min_size"], b["max_size"], wait, b["min_size"] < b["max_size"]

    def batch_check(p):
        lo, hi, wait, timed = batch_params(p)
        front = bbuf[p][0] if bbuf[p] else None
        s = front[2] if front else 0
        if s != oldest[p]:
            oldest[p] = s
            gen[p] += 1
            if front and timed and front[1] + wait > env.now:
                g = gen[p]
                at(front[1] + wait - env.now, STATE, lambda: timeout(p, g))
        ready(p)

    def ready(p):
        lo, hi, wait, timed = batch_params(p)
        n = len(bbuf[p])
        if pend[p] or n == 0:
            return
        if n >= hi or (timed and n >= lo and env.now - bbuf[p][0][1] >= wait):
            pend[p] = True
            need_dispatch()

    def timeout(p, g):
        if g == gen[p]:
            ready(p)

    def free_unit(r, e):
        """D-055: the unit set up for e's type of least index, else the
        first free one; None when none is free."""
        free = [i for i, u in enumerate(units[r]) if not u["busy"] and not u["down"]]
        if not free:
            return None
        prod = entities[e]["product"] if e else None
        match = [i for i in free if units[r][i]["state"] == prod]
        return match[0] if match else free[0]

    def dispatch():
        dispatching[0] = False
        granted = True
        while granted:
            granted = False
            for p in sorted(m.resource):
                r = m.resource[p]
                if m.nodes[p].get("batch"):
                    while pend[p] and (r is None or free_unit(r, None) is not None):
                        grant_batch(p)
                        granted = True
                    continue
                while queue[p] and (r is None or free_unit(r, queue[p][0]) is not None):
                    grant(p, queue[p].pop(0))
                    granted = True

    def service(p):
        return m.draw(m.nodes[p]["service_time"]["distribution"], rng)

    def started(p, e):
        st["starts"].setdefault(p, []).append([e, env.now / per])
        enter = entities[e]["enter"]
        if enter >= w and env.now <= h:
            st["waits"].setdefault(m.label[p], []).append((env.now - enter) / per)

    def grant(p, e):
        r = m.resource[p]
        setup = 0
        if r is not None:
            u = free_unit(r, e)
            unit = units[r][u]
            unit["busy"] = True
            holding[e] = u
            set_levels(r)
            s = m.res[r].get("setups")
            prod = entities[e]["product"]
            if s is not None:
                frm = unit["state"]
                unit["state"] = prod
                if frm is not None and frm != prod:
                    d = next((t["distribution"] for t in s.get("transitions", []) if t["from"] == frm and t["to"] == prod), s.get("default"))
                    setup = m.draw(d, rng) if d else 0
                    st["setups"].append([e, p, r, u, env.now / per, setup / per])
                    changeovers[r].append((env.now, env.now + setup))
        started(p, e)
        at(setup + service(p), COMPLETE, lambda: complete(p, [e]))

    def grant_batch(p):
        lo, hi, wait, timed = batch_params(p)
        r = m.resource[p]
        size = min(len(bbuf[p]), hi)
        members = [x[0] for x in bbuf[p][:size]]
        del bbuf[p][:size]
        pend[p] = False
        number[p] += 1
        if r is not None:
            u = free_unit(r, None)
            units[r][u]["busy"] = True
            holding[(p, number[p])] = u
            set_levels(r)
        st["batches"].append([p, number[p], env.now / per, members, size])
        for e in members:
            started(p, e)
        n = number[p]
        at(service(p), COMPLETE, lambda: complete(p, members, (p, n)))
        batch_check(p)

    def complete(p, members, key=None):
        r = m.resource[p]
        if r is not None:
            release(r, holding.pop(key if key else members[0]))
        need_dispatch()
        for e in members:
            route(e, p)

    def release(r, u):
        units[r][u]["busy"] = False
        if pending[r]:
            go_down(r, pending[r].pop(0), u)
        set_levels(r)

    # Breakdowns (D-051): failure clocks in calendar time, finish-first.
    def start_clocks():
        for r in sorted(m.res):
            if m.res[r].get("breakdowns"):
                for c in range(m.res[r]["capacity"]):
                    schedule_failure(r, c)

    def schedule_failure(r, c):
        at(m.draw(m.res[r]["breakdowns"]["time_to_failure"]["distribution"], rng), STATE, lambda: failure(r, c))

    def failure(r, c):
        idle = [i for i, u in enumerate(units[r]) if not u["busy"] and not u["down"]]
        if idle:
            go_down(r, c, idle[0])
        else:
            pending[r].append(c)
            st["pending"].append([r, c, env.now / per])

    def go_down(r, c, u):
        units[r][u]["down"] = True
        clock_unit[r][c] = u
        set_levels(r)
        downs_open[(r, c)] = len(st["downs"])
        st["downs"].append([r, c, env.now / per, None, env.now])
        at(m.draw(m.res[r]["breakdowns"]["time_to_repair"]["distribution"], rng), STATE, lambda: repair(r, c))

    def repair(r, c):
        u = clock_unit[r][c]
        units[r][u]["down"] = False
        clock_unit[r][c] = None
        st["downs"][downs_open.pop((r, c))][3] = env.now / per
        set_levels(r)
        schedule_failure(r, c)
        if pending[r]:
            go_down(r, pending[r].pop(0), u)
        else:
            need_dispatch()

    for src in sorted(x for x in m.nodes if m.nodes[x]["type"] == "source"):
        start_source(src)
    start_clocks()
    env.run(until=math.nextafter(h, math.inf))

    # The Result members over [W, H].
    T = h - w
    out = {"facts": st, "T": T, "w": w, "h": h, "per": per}
    states = {}
    for r, x in m.res.items():
        used_t = in_use[r].total()
        down_t = down_level[r].total()
        out.setdefault("utilization", {})[r] = used_t / (x["capacity"] * T)
        out.setdefault("in_use", {})[r] = used_t
        out.setdefault("down", {})[r] = down_t
        z = sum(overlap(a, b, w, h) for a, b in changeovers[r])
        out.setdefault("setup", {})[r] = z
        out.setdefault("setups_started", {})[r] = sum(1 for a, _ in changeovers[r] if inwin(a))
        if not (x.get("breakdowns") or x.get("setups")):
            continue
        s = {"in_use_ticks": round(used_t)}
        if x.get("setups") is not None:
            done = [(a, b) for a, b in changeovers[r] if a >= w and b <= h]
            s.update({"setup_ticks": round(z), "setups_started": out["setups_started"][r], "setups_completed": len(done),
                      "completed_setup_ticks": round(sum(b - a for a, b in done))})
        if x.get("breakdowns"):
            ds = [d for d in st["downs"] if d[0] == r]
            complete_repairs = [d for d in ds if d[4] >= w and d[3] is not None and d[3] * per <= h]
            available = x["capacity"] * T - round(down_t)
            s.update({"down_ticks": round(down_t), "breakdowns_started": sum(1 for d in ds if inwin(d[4])),
                      "repairs_completed": len(complete_repairs),
                      "completed_repair_ticks": round(sum(d[3] * per - d[4] for d in complete_repairs)),
                      "available_ticks": available, "idle_ticks": available - round(used_t)})
        s["scheduled_processing_ticks"] = round(used_t) - round(z)
        states[r] = s
    out["resource_states"] = states
    batches = {}
    for p in m.resource:
        if m.nodes[p].get("batch"):
            mine = [b for b in st["batches"] if b[0] == p and inwin(b[2] * per)]
            batches[p] = {"batches_started": len(mine), "members_started": sum(b[4] for b in mine)}
    out["batches"] = batches
    out["mean_lead_time_minutes"] = sum(st["leads"]) / len(st["leads"]) if st["leads"] else None
    return out


failures = []


def check(cond, what):
    if not cond:
        failures.append(what)
    return cond


def deterministic(case):
    cid, exp = case["id"], case["expected"]
    r = simulate(case["model"], case["config"], 0, 1)
    f = r["facts"]
    want_starts = {n: [list(x) for x in v] for n, v in exp["starts"].items()}
    check(f["starts"] == want_starts, f"{cid}: starts {f['starts']} != {want_starts}")
    check(f["completions"] == [list(x) for x in exp["completions"]], f"{cid}: completions {f['completions']} != {exp['completions']}")
    check(not exp.get("blocks"), f"{cid}: a case with blocks")
    if "batches" in exp:
        got = [[p, n, t, ms] for p, n, t, ms, _ in f["batches"]]
        check(got == [list(b) for b in exp["batches"]], f"{cid}: batches {got} != {exp['batches']}")
    if "setups" in exp:
        check(f["setups"] == [list(s) for s in exp["setups"]], f"{cid}: changeovers {f['setups']} != {exp['setups']}")
    if "downs" in exp:
        got = [d[:4] for d in f["downs"]]
        check(got == [list(d) for d in exp["downs"]], f"{cid}: downs {got} != {exp['downs']}")
        check(f["pending"] == [list(x) for x in exp["pending"]], f"{cid}: waiting failures {f['pending']} != {exp['pending']}")
    res = exp.get("result")
    if not res:
        return
    check(f["completed"] == res["completed_count"], f"{cid}: completed {f['completed']} != {res['completed_count']}")
    censored = f["created"] - f["exited"]
    check(censored == res["censored_entities"], f"{cid}: censored {censored} != {res['censored_entities']}")
    for k, v in (res.get("resource_utilization") or {}).items():
        check(abs(r["utilization"][k] - v) < 1e-12, f"{cid}: utilization {k} {r['utilization'][k]} != {v}")
    check(sorted(f["leads"]) == sorted(res["lead_times_minutes"]), f"{cid}: leads {f['leads']} != {res['lead_times_minutes']}")
    for q, waits in (res.get("queue_waits_minutes") or {}).items():
        check(sorted(f["waits"].get(q, [])) == sorted(waits), f"{cid}: waits at {q} {f['waits'].get(q)} != {waits}")
    check(res.get("blocking") is None and res.get("buffers") is None, f"{cid}: blocking members in a case without finite buffers")
    check(r["resource_states"] == (res.get("resource_states") or {}), f"{cid}: resource_states {r['resource_states']} != {res.get('resource_states')}")
    check(r["batches"] == (res.get("batches") or {}), f"{cid}: batches {r['batches']} != {res.get('batches')}")


def refused(case):
    """A case outside the limits (its "simpy" says why) must be refused
    before it is run, never compared with a wrong reading."""
    try:
        Line(case["model"])
    except SystemExit as e:
        return str(e)
    check(False, f"{case['id']}: the script accepted a model it cannot interpret ({case['simpy']})")
    return None


def stochastic(ref, go):
    tol = ref["tolerance"]
    report = []
    for case in ref["cases"]:
        cid, cfg = case["id"], case["config"]
        n = cfg["replications"]
        sims = [simulate(case["model"], cfg, i, 20_011 * cfg["seed"] + i) for i in range(n)]
        g = go.get(cid, [])
        if not check(len(g) == n, f"{cid}: {len(g)} Go replications, {n} expected"):
            continue
        m = Line(case["model"])
        T = sims[0]["T"]
        per = sims[0]["per"]
        metrics = {"completed_count": ([x["completed_count"] for x in g], [s["facts"]["completed"] for s in sims]),
                   "mean_lead_time_minutes": ([x["mean_lead_time_minutes"] for x in g], [s["mean_lead_time_minutes"] for s in sims])}
        for rid, x in m.res.items():
            c = x["capacity"]
            metrics[f"utilization/{rid}"] = ([v["utilization"][rid] for v in g], [s["utilization"][rid] for s in sims])
            if x.get("setups") is not None:
                metrics[f"setup_fraction/{rid}"] = ([v["setup_ticks"][rid] / (c * T) for v in g], [s["setup"][rid] / (c * T) for s in sims])
                metrics[f"setups_started/{rid}"] = ([v["setups_started"][rid] for v in g], [s["setups_started"][rid] for s in sims])
            if x.get("breakdowns"):
                metrics[f"down_fraction/{rid}"] = ([v["down_ticks"][rid] / (c * T) for v in g], [s["down"][rid] / (c * T) for s in sims])
        for p in m.resource:
            if m.nodes[p].get("batch"):
                metrics[f"batches_started/{p}"] = ([v["batches_started"][p] for v in g], [s["batches"][p]["batches_started"] for s in sims])
                metrics[f"members_started/{p}"] = ([v["members_started"][p] for v in g], [s["batches"][p]["members_started"] for s in sims])
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
        del per
    return report


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--go-results", required=True)
    ap.add_argument("--report")
    args = ap.parse_args()
    if simpy.__version__ != SIMPY_VERSION:
        print(f"SimPy {simpy.__version__}, the reference is pinned to {SIMPY_VERSION} (requirements-spec.txt)", file=sys.stderr)
        return 2
    go = json.loads(Path(args.go_results).read_text())
    compared, skipped, report, tolerances = [], [], [], {}
    for fixture in FIXTURES:
        doc = json.loads(fixture.read_text())
        for case in doc["cases"]:
            mode = case.get("simpy", "")
            if mode == "compare":
                deterministic(case)
                compared.append(case["id"])
            elif mode.startswith("refused"):
                why = refused(case)
                skipped.append(f"{case['id']}: refused by the script: {why}")
            else:
                skipped.append(f"{case['id']}: {mode}")
        report += stochastic(doc["reference"], go)
        tolerances[fixture.name] = doc["reference"]["tolerance"]
    summary = {"simpy": simpy.__version__, "deterministic_cases": compared, "not_compared": skipped,
               "stochastic_comparisons": len(report), "stochastic_passed": sum(1 for x in report if x["pass"]),
               "tolerance": tolerances, "failures": failures}
    if args.report:
        Path(args.report).write_text(json.dumps({"summary": summary, "comparisons": report}, indent=2) + "\n")
    print(json.dumps(summary, indent=2))
    for x in report:
        print(f"{x['case']:>6} {x['metric']:<34} go {x['go']:<12.6g} simpy {x['simpy']:<12.6g} bound {x['bound']:<10.3g} {'ok' if x['pass'] else 'FAIL'}")
    if failures:
        print(f"FAIL batch and changeover reference: {len(failures)} difference(s)", file=sys.stderr)
        return 1
    print("PASS batch and changeover reference")
    return 0


if __name__ == "__main__":
    sys.exit(main())
