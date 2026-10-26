#!/usr/bin/env python3
"""Independent SimPy reference for finite buffers and blocking (T-047),
resource breakdowns (T-048), and resource calendars and release schedules
(T-049), TEST-034.

AGENTS.md §7 and ADR-006: SimPy is only an independent reference. Nothing in
the Go engine imports, calls or embeds this script or SimPy; the script reads
the shared fixtures (testdata/simulation/blocking-goldens.json and
breakdown-goldens.json) and, for the stochastic cases, a JSON file of the Go
engine's per-replication results that `make simulation-reference-test`
writes first.

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

Limits of the interpretation, checked before use. The script accepts a
model only if every requirement is of 1 unit and at most one resource of
each bundle is shared with another process; if a bundle has a shared
resource, each of its own resources must have at least the shared one's
capacity. It refuses every other model, and the cases marked "refused"
must be refused (G-B13). A bundle is taken one resource at a time while
holding the earlier ones: the shared one first, then its own ones in
ascending capacity (ties by ID), all released together. An entity holding
a unit of a resource then holds a unit of every resource taken before it,
so when it is granted its first resource the next ones always have a free
unit (each has at least the capacity of the one before, of whose units the
entity holds one); the bundle is granted exactly when the engine's
all-or-nothing, resource-wise FIFO grant would grant it: when the first
resource (the shared one, or the smallest own one) has a free unit for the
request at the front of its FIFO. Own resources taken in ID order instead
could hold a larger own resource while waiting for a smaller one (G-B14;
T-047 review round 2, L1').

Breakdowns (T-048; ADR-026 D-051, D-074 APPROVED) with SimPy's own
primitives: a resource that breaks down is a simpy.PriorityResource; jobs
request it at priority 0 (FIFO among themselves), and each unit u has a
failure clock process that alternates TTF and TTR. When its TTF ends and a
unit is neither in use nor down (len(users) < capacity), the clock requests
a unit at priority -1, which SimPy grants at once (a request ahead of every
waiting job), and holds it for one TTR; otherwise the failure waits, in
FIFO order, for the next unit a job releases: the job hands its request
over to the failure process instead of releasing it (finish-first). A
repair does the same (D-074 (a) option B, T-048 review round 1): the
repaired unit goes to the first waiting failure, and only when none waits
is it released to SimPy's queue, where a waiting job gets it. Samples come
from Python's random: expovariate, triangular,
lognormvariate (sigma^2 = ln(1 + cv^2), mu = ln(mean) - sigma^2/2, D-056)
and weibullvariate(scale, shape), never the engine's streams or samplers.

Limits of the breakdown interpretation, checked before use. Besides the
rules above, a resource that breaks down must be the only resource of every
bundle that needs it: taken one resource at a time, a unit held while the
entity waits for the rest of its bundle could not fail, whereas the engine
grants all or nothing and the idle unit can (G-F8, refused). Every
distribution must be constant, exponential, triangular, lognormal or
Weibull (no empirical). Same-tick ties between a failure or a repair and
another event are the kernel's D-048 order, which SimPy (scheduling order)
does not reproduce: the deterministic cases with such ties are marked "not
compared"; in the stochastic ones, in continuous time, they have probability
0 in SimPy and are rare on the engine's 1 ms ticks, within the tolerance.

Calendars (T-049; ADR-026 D-051 (ii), D-052, D-081 APPROVED, H-024) with SimPy's
own primitives: a resource on a calendar is a simpy.PriorityResource whose
units off shift are held by "blocker" requests at priority -0.5 (behind a
failure at -1, ahead of every job at 0). The script computes S(t) itself
from the calendar (exceptions first, open = the capacity, closed = 0; else
the shift holding t mod period, at its label's shift_capacity, the
capacity when unlabelled or unlisted; 0 between shifts), never with the
engine's calendar package. At each change of S, in calendar ID order at
t=0 and resource ID order within a calendar, it asks for C - S blockers
when S falls (each takes a unit as soon as one is free: a job in service
finishes first, the overtime) and, when S rises, cancels blockers still
waiting and releases granted ones (the units go to the waiting jobs, in
FIFO order: the shift start wakes them without a job's release). The
calendar's events, and the failure clocks and repairs of a resource on a
calendar, are scheduled at SimPy's URGENT priority, before the jobs'
events at the same time, so that a request that arrives when S falls, or
when a failure takes the idle unit, waits as at the kernel's dispatch
(priority 20, after 10 and 15; D-048); among themselves they run in
scheduling order, as the kernel's priority-15 events run in sequence order.
A failure clock on a calendar runs only on shift (S > 0): its TTF is spent
in on-shift time (D-051 (ii)). The scheduled, available and idle members
are the integrals of S, min(U, S), max(U - S, 0), A = min(S, C - D),
max(0, A - U) and min(U - K, S), with U the units jobs hold, D the units
down and K the units held by blocked entities.

Release schedules (T-049; D-053): a table releases its rows' entities at
k·P + A_j ticks in order, P and A_j the period and the row's at each
quantized to ticks first (round half up, a positive value at least one
tick; D-082 (b) A, review round 1 M1: not k·period + at in model time),
computed by the script from the IR; a deterministic schedule releases the k-th entity
at Λ⁻¹(k), computed exactly (fractions.Fraction) and quantized as D-053
says (round half up to the tick, at least one tick after the last; with
every rate at most one a tick, the validator's cap, consecutive exact
releases are at least a tick apart, so that floor never binds here: G-R9),
not with the engine's floating-point profile; a poisson schedule is a
non-homogeneous Poisson process generated by thinning (Lewis-Shedler: a
homogeneous process at the largest rate, each point kept with
probability λ(t)/λ_max, Python's random), a different algorithm from the
engine's cumulative-rate inverse, so poisson agreement is statistical (the
arrivals in each segment).

Limits of the calendar interpretation, checked before use. A resource on
a calendar must be the only resource of every bundle that needs it (taken
one resource at a time, a unit held while the entity waits for one on
another calendar would be held off the shift; G-C4, G-D4C, G-D5 refused).
A resource on a calendar that breaks down must have S in {0, C} at every
time (its shifts' units 0 or the capacity): with 0 < S < C a blocker holds
an off-shift unit that the kernel counts as idle for a failure (D-051),
so a failure would wait where the kernel's takes that unit. The same holds
for a failure due exactly at a shift's end (G-T5, not compared). Every
requirement is still of 1 unit (G-D3C refused).

    python scripts/simpy_reference.py --go-results bin/simpy-reference-go.json
"""
import argparse
import json
import math
import random
import sys
from fractions import Fraction
from pathlib import Path

import simpy
from simpy.events import URGENT, Event

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = [ROOT / "testdata/simulation/blocking-goldens.json", ROOT / "testdata/simulation/breakdown-goldens.json",
            ROOT / "testdata/simulation/calendar-goldens.json"]
SIMPY_VERSION = "4.1.2"
DISTRIBUTIONS = ("constant", "exponential", "triangular", "lognormal", "weibull")
UNIT_MS = {"millisecond": 1, "second": 1000, "minute": 60_000, "hour": 3_600_000, "day": 86_400_000}
TICK = 60_000  # ticks per model minute (T-047, T-048 fixtures); tick_of(ir) in general


def tick_of(ir):
    """Ticks per model time unit (1 ms ticks, D-005)."""
    return UNIT_MS[ir.get("time_unit", "minute")]


class Step:
    """A step function of time integrated over the window [w, h]; hook,
    if set, runs after every change (the calendar members, T-049)."""

    def __init__(self, w, h, hook=None):
        self.w, self.h, self.hook = w, h, hook
        self.level, self.since, self.area = 0, 0.0, 0.0

    def set(self, level, t):
        a, b = max(self.since, self.w), min(t, self.h)
        if b > a:
            self.area += self.level * (b - a)
        self.level, self.since = level, t
        if self.hook:
            self.hook(t)

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
        # other processes first, then its own in ascending capacity (ties
        # by ID). That is the engine's all-or-nothing grant only if, once
        # the first is granted, the next ones always have a free unit. A
        # unit of a resource is only held together with a unit of every
        # resource taken before it, by this process's entities; while one
        # entity holding the previous resource R asks for the next one N,
        # at most capacity(R) - 1 others hold N, so with capacity(N) >=
        # capacity(R) a unit of N is free. Hence the refusal of an own
        # resource smaller than the shared one (an entity holding S would
        # wait for O and let later requests take S's other units, which the
        # engine's resource-wise FIFO does not: G-B13, review round 1, L1)
        # and the ascending order of the own ones (in ID order a larger own
        # resource could be held while waiting for a smaller: G-B14, review
        # round 2, L1').
        self.order = {}
        for n in ir["nodes"]:
            req = sorted(r["resource_id"] for r in n.get("requires", []))
            shared = [r for r in req if users[r] != {n["id"]}]
            if len(shared) > 1:
                raise SystemExit(f"unsupported: {n['id']}'s bundle {req} shares {shared}: one at a time would not be all-or-nothing")
            own = [r for r in req if r not in shared]
            for r in own:
                if shared and self.capacity[r] < self.capacity[shared[0]]:
                    raise SystemExit(f"unsupported: {n['id']}'s own {r} (capacity {self.capacity[r]}) is smaller than its shared "
                                     f"{shared[0]} (capacity {self.capacity[shared[0]]}): one at a time would not be all-or-nothing")
            self.order[n["id"]] = shared + sorted(own, key=lambda r: (self.capacity[r], r))
        # Breakdowns (T-048): a resource that breaks down is the whole
        # bundle of every process that needs it (see the module docstring).
        self.breaks = {}
        for r in sorted(ir["resources"], key=lambda r: r["id"]):
            if r.get("breakdowns"):
                self.breaks[r["id"]] = (r["breakdowns"]["time_to_failure"]["distribution"], r["breakdowns"]["time_to_repair"]["distribution"])
        for n in ir["nodes"]:
            req = [r["resource_id"] for r in n.get("requires", [])]
            for r in req:
                if r in self.breaks and len(req) > 1:
                    raise SystemExit(f"unsupported: {n['id']}'s bundle {sorted(req)} has {r}, which breaks down, and other resources: "
                                     "taken one at a time, a unit of it held while waiting for the rest could not fail")
        dists = [d for ttf, ttr in self.breaks.values() for d in (ttf, ttr)]
        for n in ir["nodes"]:
            for member in ("arrival", "service_time", "delay_time"):
                if n.get(member) and "distribution" in n[member]:  # an arrival may have a schedule instead (T-049)
                    dists.append(n[member]["distribution"])
        for d in dists:
            if d["type"] not in DISTRIBUTIONS:
                raise SystemExit(f"unsupported distribution {d['type']}")

        # Calendars (T-049; see the module docstring for the limits).
        cals = {c["id"]: c for c in ir.get("calendars", [])}
        self.cal = {}  # resource -> (calendar, shift_capacity)
        for r in sorted(ir["resources"], key=lambda r: r["id"]):
            if r.get("calendar_id"):
                self.cal[r["id"]] = (cals[r["calendar_id"]], r.get("shift_capacity", {}))
        for n in ir["nodes"]:
            req = [r["resource_id"] for r in n.get("requires", [])]
            for r in req:
                if r in self.cal and len(req) > 1:
                    raise SystemExit(f"unsupported: {n['id']}'s bundle {sorted(req)} has {r}, on a calendar, and other resources: "
                                     "taken one at a time, a unit held while waiting for the rest would be held off its shift")
        for r, (c, sc) in self.cal.items():
            units = {sc.get(s.get("label"), self.capacity[r]) if s.get("label") else self.capacity[r] for s in c["shifts"]}
            if r in self.breaks and not units <= {0, self.capacity[r]}:
                raise SystemExit(f"unsupported: {r} breaks down and has {sorted(units)} units on shift: a blocker would hold an "
                                 "off-shift unit the kernel counts as idle for a failure (D-051)")
        for n in ir["nodes"]:
            s = (n.get("arrival") or {}).get("schedule")
            if s and s["process"] not in ("poisson", "deterministic", "table"):
                raise SystemExit(f"unsupported release process {s['process']}")

    def requires(self, node):
        return self.order[node]

    def units(self, r, t):
        """S(t) of resource r, from its calendar (C without one)."""
        if r not in self.cal:
            return self.capacity[r]
        c, sc = self.cal[r]
        for e in c.get("exceptions", []):
            if e["start"] <= t < e["end"]:
                return self.capacity[r] if e["open"] else 0
        off = math.fmod(t, c["period"])
        for s in c["shifts"]:
            if s["start"] <= off < s["end"]:
                return sc.get(s["label"], self.capacity[r]) if s.get("label") else self.capacity[r]
        return 0

    def changes(self, cid, horizon):
        """The times in (0, horizon] at which S of some resource on
        calendar cid changes, in order."""
        cache = self.__dict__.setdefault("_changes", {})
        if (cid, horizon) in cache:
            return cache[(cid, horizon)]
        c = next(x for x in self.ir.get("calendars", []) if x["id"] == cid)
        rs = [r for r, (x, _) in self.cal.items() if x["id"] == cid]
        points = set()
        k = 0
        while k * c["period"] <= horizon:
            for s in c["shifts"]:
                points.update((k * c["period"] + s["start"], k * c["period"] + s["end"]))
            points.add((k + 1) * c["period"])
            k += 1
        for e in c.get("exceptions", []):
            points.update((e["start"], e["end"]))
        out, level = [], {r: self.units(r, 0) for r in rs}
        for p in sorted(x for x in points if 0 < x <= horizon):
            now = {r: self.units(r, p) for r in rs}
            if now != level:
                out.append(p)
                level = now
        cache[(cid, horizon)] = out
        return out

    def on_shift_until(self, r, t, horizon):
        """(on, until): whether r is on shift (S > 0) at t, and the next time
        after t at which that changes (inf if not before the horizon)."""
        on = self.units(r, t) > 0
        c = self.cal[r][0]
        for p in self.changes(c["id"], horizon):
            if p > t and (self.units(r, p) > 0) != on:
                return on, p
        return on, math.inf


def urgent(env, delay):
    """A timeout at SimPy's URGENT priority: before every NORMAL event (a
    job's timeout, a request's grant) at the same time (the kernel's
    priority 15 before its dispatch at 20, D-048)."""
    ev = Event(env)
    ev._ok, ev._value = True, None
    env.schedule(ev, URGENT, delay)
    return ev


def quantize(value, tick):
    """A model-time value in ticks, as D-082 (b) quantizes a table's period
    and release times: round half up (on the value's exact binary64
    fraction), a positive value at least one tick."""
    t = math.floor(Fraction(value) * tick + Fraction(1, 2))
    return 1 if t == 0 and value > 0 else t


def exact_release(sched, n, tick):
    """Λ⁻¹(n) of a deterministic schedule exactly (Fraction), in ticks,
    rounded half up, a positive τ at least one tick (D-053)."""
    period = Fraction(sched["period"])
    segs = sched["segments"]
    total, starts, rates, cum_start, cum_end = Fraction(0), [], [], [], []
    for j, s in enumerate(segs):
        end = Fraction(segs[j + 1]["start"]) if j + 1 < len(segs) else period
        starts.append(Fraction(s["start"]))
        rates.append(Fraction(s["rate"]))
        cum_start.append(total)
        total += rates[-1] * (end - starts[-1])
        cum_end.append(total)
    a = Fraction(n)
    k = math.ceil(a / total) - 1
    r = a - k * total
    j = next(i for i in range(len(rates)) if rates[i] > 0 and cum_end[i] >= r)
    tau = k * period + starts[j] + (r - cum_start[j]) / rates[j]
    t = math.floor(tau * tick + Fraction(1, 2))
    return 1 if t == 0 and tau > 0 else t


def segment_of(sched, t):
    """The index of the segment (rate schedule) or row (table) of an
    arrival at time t (model units), by t mod period."""
    off = math.fmod(t, sched["period"])
    if sched["process"] == "table":
        return min(range(len(sched["releases"])), key=lambda i: abs(sched["releases"][i]["at"] - off))
    return max(i for i, s in enumerate(sched["segments"]) if s["start"] <= off + 1e-9)


def sample(dist, rng):
    if dist["type"] == "constant":
        return float(dist["value"])
    if dist["type"] == "exponential":
        return rng.expovariate(1.0 / dist["mean"])
    if dist["type"] == "triangular":
        return rng.triangular(dist["min"], dist["max"], dist["mode"])
    if dist["type"] == "lognormal":
        s2 = math.log(1 + dist["cv"] ** 2)
        return rng.lognormvariate(math.log(dist["mean"]) - s2 / 2, math.sqrt(s2))
    if dist["type"] == "weibull":
        return rng.weibullvariate(dist["scale"], dist["shape"])
    raise SystemExit(f"unsupported distribution {dist['type']}")


def simulate(ir, config, rep, seed, record=False, until=None):
    """One replication; returns the per-replication statistics (minutes)."""
    m = Model(ir)
    env = simpy.Environment()
    rng = random.Random(seed)
    w, h = config["warmup"]["value"], config["horizon"]["value"]
    if until is None:
        until = h
    machines = {r: (simpy.PriorityResource if r in m.breaks or r in m.cal else simpy.Resource)(env, capacity=c) for r, c in m.capacity.items()}
    slots = {p: simpy.Resource(env, capacity=c) for p, c in m.finite.items()}
    # Calendars (T-049): S per resource, its blocker requests, and the
    # members of D-058 kept from U (in_use), S, D (down) and K (held).
    shift = {r: m.units(r, 0) for r in m.cal}
    blockers = {r: [] for r in m.cal}
    cstats = {r: {k: Step(w, h) for k in ("scheduled", "sched_in_use", "overtime", "available", "idle", "processing")} for r in m.cal}
    capacities = []

    def derive(r, t):
        if r not in m.cal:
            return
        u, s, d, k = in_use[r].level, shift[r], down[r].level if r in m.breaks else 0, held[r].level
        a = min(s, m.capacity[r] - d)
        st = cstats[r]
        st["scheduled"].set(s, t)
        st["sched_in_use"].set(min(u, s), t)
        st["overtime"].set(max(u - s, 0), t)
        st["available"].set(a, t)
        st["idle"].set(max(0, a - u), t)
        st["processing"].set(min(u - k, s), t)

    def hook(r):
        return (lambda t: derive(r, t)) if r in m.cal else None

    in_use = {r: Step(w, h, hook(r)) for r in m.capacity}
    held = {r: Step(w, h, hook(r)) for r in m.capacity}
    full = {p: Step(w, h) for p in m.finite}
    blocked_at = {}
    stats = {"completed": 0, "leads": [], "reworks": 0, "failures": 0, "blocks_started": {}, "blocks_done": {}, "block_minutes": {},
             "waits": {}, "starts": {}, "completions": [], "blocks": [], "last_start": None, "arrivals": [], "sink_at": []}
    # Breakdowns (T-048): units down, the failures waiting for a unit that
    # a job releases or a repair frees (FIFO; D-074 (a) B), downtimes
    # started in the window, complete repairs (started at or after W, ended
    # by H) and their minutes.
    down = {r: Step(w, h, hook(r)) for r in m.breaks}
    waiting_failures = {r: [] for r in m.breaks}
    bstats = {"started": {r: 0 for r in m.breaks}, "repairs": {r: 0 for r in m.breaks}, "repair_minutes": {r: 0.0 for r in m.breaks},
              "downs": [], "pending": []}
    def set_shift(r, s, observed=True):
        """S of r changes to s: blockers for the units off shift (D-052)."""
        shift[r] = s
        if record and observed:
            capacities.append([r, env.now, s])
        want = m.capacity[r] - s
        while len(blockers[r]) < want:
            blockers[r].append(machines[r].request(priority=-0.5))
        while len(blockers[r]) > want:
            pending = [b for b in blockers[r] if not b.triggered]
            b = pending[-1] if pending else blockers[r][-1]
            blockers[r].remove(b)
            if b.triggered:
                machines[r].release(b)
            else:
                b.cancel()
        derive(r, env.now)

    # S(0): the units off shift at t=0 are blocked before anything runs; it
    # is the program's initial value, not a change (D-047).
    for r in m.cal:
        set_shift(r, shift[r], observed=False)

    def calendar(cid):
        """Calendar cid's changes of S, its resources in ID order (D-052),
        at URGENT priority."""
        rs = sorted(r for r, (c, _) in m.cal.items() if c["id"] == cid)
        for t in m.changes(cid, h):
            yield urgent(env, t - env.now)
            for r in rs:
                if m.units(r, t) != shift[r]:
                    set_shift(r, m.units(r, t))

    def wait_on_shift(r, d):
        """Spends d of on-shift time of r (D-051 (ii)); calendar time
        without a calendar."""
        if r not in m.cal:
            yield env.timeout(d)
            return
        while True:
            on, until = m.on_shift_until(r, env.now, h)
            if not on:
                if until == math.inf:
                    yield env.timeout(math.inf)
                yield urgent(env, until - env.now)
                continue
            if env.now + d <= until:
                yield urgent(env, d)
                return
            d -= until - env.now
            yield urgent(env, until - env.now)

    def clock(r, u):
        """Failure clock u of resource r: TTF (of on-shift time on a
        calendar), then down (at once on an idle unit, else on the next
        unit released or repaired) for TTR, then up."""
        ttf, ttr = m.breaks[r]
        while True:
            yield from wait_on_shift(r, sample(ttf, rng))
            res = machines[r]
            if len(res.users) < res.capacity:
                req = res.request(priority=-1)
                if not req.triggered:
                    raise RuntimeError(f"{r}/{u}: an idle unit was not granted at once")
            else:
                ev = env.event()
                waiting_failures[r].append(ev)
                fired = env.now
                req = yield ev
                # A failure that waited 0 minutes is SimPy's order of a
                # failure and a release at the same time (scheduling order);
                # the kernel releases first (priority 0 < 15) and the failure
                # finds the unit idle. Both put the unit down at that time,
                # so only a positive wait is a waiting failure.
                if record and env.now > fired:
                    bstats["pending"].append([r, u, fired])
            since = env.now
            down[r].add(1, since)
            if w <= since <= h:
                bstats["started"][r] += 1
            entry = [r, u, since, None]
            if record:
                bstats["downs"].append(entry)
            yield urgent(env, sample(ttr, rng)) if r in m.cal else env.timeout(sample(ttr, rng))
            if waiting_failures[r]:
                # D-074 (a) B: the repaired unit goes to the first waiting
                # failure, as a released one does; only otherwise does it
                # return to SimPy's queue (a waiting job).
                waiting_failures[r].pop(0).succeed(req)
            else:
                res.release(req)
            down[r].add(-1, env.now)
            entry[3] = env.now
            if since >= w and env.now <= h:
                bstats["repairs"][r] += 1
                bstats["repair_minutes"][r] += env.now - since

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
                in_use[r].add(-1, env.now)
                if waiting_failures.get(r):
                    # Finish-first (D-051): the released unit goes down for
                    # the first waiting failure; the job hands it over.
                    waiting_failures[r].pop(0).succeed(req)
                else:
                    machines[r].release(req)
            holding.clear()
            if typ == "sink":
                if record:
                    stats["sink_at"].append([eid, env.now])
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
    tick = tick_of(ir)
    arrivals_in = {}  # "<source>/<segment or row>" -> arrivals in [W, H]

    def source(sid):
        node = m.nodes[sid]
        limit = node["arrival"].get("max_entities")
        sched = node["arrival"].get("schedule")
        n = 0

        def create(row=None):
            nonlocal n
            n += 1
            created["n"] += 1
            eid = f"r{rep}/{sid}/{n}"
            if record:
                stats["arrivals"].append([eid, env.now])
            if sched and w <= env.now <= h:
                key = f"{sid}/{segment_of(sched, env.now) if row is None else row}"
                arrivals_in[key] = arrivals_in.get(key, 0) + 1
            env.process(entity(eid, sid, env.now))
            return bool(limit) and n >= limit

        if sched is None:
            while True:
                yield env.timeout(sample(node["arrival"]["distribution"], rng))
                if create():
                    return
        if sched["process"] == "table":
            # D-082 (b) A: the period and each release time in ticks first.
            period = quantize(sched["period"], tick)
            ats = [quantize(row["at"], tick) for row in sched["releases"]]
            k = 0
            while True:
                for j, row in enumerate(sched["releases"]):
                    at = k * period + ats[j]
                    if at > h * tick:
                        return
                    yield env.timeout(at / tick - env.now)
                    for _ in range(row["quantity"]):
                        if create(j):
                            return
                k += 1
        if sched["process"] == "deterministic":
            last = -1
            while True:
                at = max(exact_release(sched, n + 1, tick), last + 1)
                if at > h * tick:
                    return
                last = at
                yield env.timeout(at / tick - env.now)
                if create():
                    return
        # poisson: Lewis-Shedler thinning at the largest rate.
        rmax = max(s["rate"] for s in sched["segments"])
        while True:
            yield env.timeout(rng.expovariate(rmax))
            seg = sched["segments"][segment_of(sched, env.now)]
            if rng.random() * rmax < seg["rate"]:
                if create():
                    return

    for sid in sorted(n["id"] for n in ir["nodes"] if n["type"] == "source"):
        env.process(source(sid))
    # Then the calendars in ID order and the failure clocks, resources in
    # ID order, units in index order (the kernel's t=0 order, D-048).
    for cid in sorted({c["id"] for c, _ in m.cal.values()}):
        env.process(calendar(cid))
    for r in m.breaks:
        for u in range(m.capacity[r]):
            env.process(clock(r, u))
    env.run(until=math.nextafter(until, math.inf))
    T = h - w
    out = {
        "breaks": sorted(m.breaks),
        "down": {r: down[r].total() for r in m.breaks},
        "breakdowns_started": bstats["started"],
        "repairs_completed": bstats["repairs"],
        "repair_minutes": bstats["repair_minutes"],
        "downs": bstats["downs"],
        "pending": bstats["pending"],
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
        # T-049
        "calendars": sorted(m.cal),
        "calendar_states": {r: {k: s.total() for k, s in st.items()} for r, st in cstats.items()},
        "capacities": capacities,
        "arrivals": stats["arrivals"],
        "arrivals_in": arrivals_in,
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
    TICK = tick_of(ir)  # noqa: N806 (the model's own unit, T-049's G-R5 is in seconds)
    if "arrivals" in exp:
        check(r["arrivals"] == [list(x) for x in exp["arrivals"]], f"{cid}: arrivals {r['arrivals']} != {exp['arrivals']}")
    if "capacities" in exp:
        # [resource, time, S] of every change of the units on shift (D-052).
        check(r["capacities"] == [list(x) for x in exp["capacities"]], f"{cid}: units on shift {r['capacities']} != {exp['capacities']}")
    want_starts = {n: [list(x) for x in v] for n, v in exp["starts"].items()}
    got_starts = {n: [[e, t] for e, t in v] for n, v in r["starts"].items()}
    check(got_starts == want_starts, f"{cid}: starts {got_starts} != {want_starts}")
    check(r["completions"] == [list(x) for x in exp["completions"]], f"{cid}: completions {r['completions']} != {exp['completions']}")
    want_blocks = [list(b) for b in exp["blocks"]]
    check(r["blocks"] == want_blocks, f"{cid}: blocks {r['blocks']} != {want_blocks}")
    if "downs" in exp:
        # [resource, unit, from, to or None] in start order, exactly; the
        # failures that waited, in the order they fired. The script records
        # only a positive wait (see clock): a 0-minute wait is the order of
        # two events at one time, which SimPy and the kernel resolve
        # differently but with the same downtime (compared above). So the
        # hand-derived waiting failures are compared without their 0-minute
        # waits (a failure that goes down in the tick it fired; G-F9 has
        # three); one with no later downtime still waits at the horizon,
        # which the script does not record, so such a case cannot be
        # compared and fails here.
        check(r["downs"] == [list(d) for d in exp["downs"]], f"{cid}: downs {r['downs']} != {exp['downs']}")
        want = []
        for res_id, unit, t in exp["pending"]:
            nxt = [d[2] for d in exp["downs"] if d[0] == res_id and d[1] == unit and d[2] >= t]
            check(bool(nxt), f"{cid}: the waiting failure {res_id}/{unit} at {t} still waits at the horizon; the case cannot be compared")
            if nxt and nxt[0] > t:
                want.append([res_id, unit, t])
        got = sorted(r["pending"], key=lambda p: p[2])
        check(got == want, f"{cid}: waiting failures with a positive wait {got} != {want}")
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
    else:
        for node in possible_blockers(ir) | set(res["blocking"]):
            want = res["blocking"].get(node)
            got = {"blocks_started": r["blocks_started"].get(node, 0), "blocks_completed": r["blocks_done"].get(node, 0),
                   "completed_block_ticks": round(r["block_minutes"].get(node, 0.0) * TICK),
                   "blocked_entity_ticks": round(r["blocked_entities"].get(node, 0.0) * TICK)}
            check(got == want, f"{cid}: blocking[{node}] {got} != {want}")
        for q, want in res["buffers"].items():
            got = round(r["full"].get(q, 0.0) * TICK)
            check(got == want["full_ticks"], f"{cid}: buffers[{q}] {got} != {want['full_ticks']}")
    # resource_states (D-058): every resource with a finite buffer in the
    # model, else only the resources that break down or have a calendar.
    capacity = {x["id"]: x["capacity"] for x in ir["resources"]}
    listed = sorted(capacity) if r["finite"] else sorted(set(r["breaks"]) | set(r["calendars"]))
    check(sorted(res.get("resource_states") or {}) == listed, f"{cid}: resource_states lists {sorted(res.get('resource_states') or {})}, SimPy {listed}")
    T = cfg["horizon"]["value"] - cfg["warmup"]["value"]
    for rid, want in (res.get("resource_states") or {}).items():
        used = round(r["in_use"][rid] * TICK)
        got = {"in_use_ticks": used}
        processing = used
        if r["finite"]:
            got["blocked_ticks"] = round(r["blocked"][rid] * TICK)
            processing = used - got["blocked_ticks"]
        if rid in r["breaks"]:
            down = round(r["down"][rid] * TICK)
            available = capacity[rid] * round(T * TICK) - down
            got.update({"down_ticks": down, "breakdowns_started": r["breakdowns_started"][rid], "repairs_completed": r["repairs_completed"][rid],
                        "completed_repair_ticks": round(r["repair_minutes"][rid] * TICK), "available_ticks": available, "idle_ticks": available - used})
        if rid in r["calendars"]:
            st = {k: round(v * TICK) for k, v in r["calendar_states"][rid].items()}
            got.update({"scheduled_ticks": st["scheduled"], "scheduled_in_use_ticks": st["sched_in_use"], "overtime_ticks": st["overtime"],
                        "available_ticks": st["available"], "idle_ticks": st["idle"]})
            processing = st["processing"]
        got["scheduled_processing_ticks"] = processing
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


def refused(case):
    """A case the interpretation cannot reproduce (its "simpy" says why) must
    be refused before it is run, never compared with a wrong reading."""
    try:
        Model(case["model"])
    except SystemExit as e:
        return str(e)
    check(False, f"{case['id']}: the script accepted a model it cannot interpret ({case['simpy']})")
    return None


def stochastic(ref, go):
    tol = ref["tolerance"]
    report = []
    for case in ref["cases"]:
        cid, cfg = case["id"], case["config"]
        TICK = tick_of(case["model"])  # noqa: N806
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
            if m.finite:
                metrics[f"blocked_fraction/{rid}"] = ([x["blocked_ticks"][rid] / TICK / (c * T) for x in g], [s["blocked"][rid] / (c * T) for s in sims])
        for rid in m.breaks:
            c = m.capacity[rid]
            metrics[f"down_fraction/{rid}"] = ([x["down_ticks"][rid] / TICK / (c * T) for x in g], [s["down"][rid] / (c * T) for s in sims])
            if rid not in m.cal:  # on a calendar: below
                metrics[f"available_fraction/{rid}"] = ([x["available_ticks"][rid] / TICK / (c * T) for x in g], [1 - s["down"][rid] / (c * T) for s in sims])
                metrics[f"idle_fraction/{rid}"] = ([x["idle_ticks"][rid] / TICK / (c * T) for x in g],
                                                   [(c * T - s["down"][rid] - s["in_use"][rid]) / (c * T) for s in sims])
            metrics[f"breakdowns_started/{rid}"] = ([x["breakdowns_started"][rid] for x in g], [s["breakdowns_started"][rid] for s in sims])
        for rid in m.cal:
            # T-049 (D-052, D-058): the scheduled utilization and the shares
            # of C·T in overtime, available and idle.
            c = m.capacity[rid]
            cs = [s["calendar_states"][rid] for s in sims]
            metrics[f"scheduled_utilization/{rid}"] = ([x["scheduled_in_use_ticks"][rid] / x["scheduled_ticks"][rid] for x in g],
                                                       [s["sched_in_use"] / s["scheduled"] for s in cs])
            for name, gm, sm in (("overtime_fraction", "overtime_ticks", "overtime"), ("available_fraction", "available_ticks", "available"),
                                 ("idle_fraction", "idle_ticks", "idle")):
                metrics[f"{name}/{rid}"] = ([x[gm][rid] / TICK / (c * T) for x in g], [s[sm] / (c * T) for s in cs])
        for node in case["model"]["nodes"]:
            sched = (node.get("arrival") or {}).get("schedule")
            if not sched:
                continue
            parts = sched["releases"] if sched["process"] == "table" else sched["segments"]
            for i in range(len(parts)):
                key = f"{node['id']}/{i}"
                metrics[f"arrivals/{key}"] = ([x["arrivals"].get(key, 0) for x in g], [s["arrivals_in"].get(key, 0) for s in sims])
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
    go = json.loads(Path(args.go_results).read_text())
    compared, skipped, report, tolerances = [], [], [], {}
    last = None
    for fixture in FIXTURES:
        doc = json.loads(fixture.read_text())
        for case in doc["cases"]:
            mode = case.get("simpy", "")
            if mode == "compare":
                deterministic(case)
                compared.append(case["id"])
            elif mode.startswith("illustrative"):
                last = deadlock(case)
                compared.append(case["id"] + " (illustrative)")
            elif mode.startswith("refused"):
                why = refused(case)
                skipped.append(f"{case['id']}: refused by the script: {why}")
            else:
                skipped.append(f"{case['id']}: {mode}")
        report += stochastic(doc["reference"], go)
        tolerances[fixture.name] = doc["reference"]["tolerance"]
    summary = {"simpy": simpy.__version__, "deterministic_cases": compared, "not_compared": skipped,
               "g_d2_last_service_start_minutes": last[0] if last else None, "g_d2_deadlock_minutes": last[1] if last else None,
               "stochastic_comparisons": len(report), "stochastic_passed": sum(1 for x in report if x["pass"]),
               "tolerance": tolerances, "failures": failures}
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
