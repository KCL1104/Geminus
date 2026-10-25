#!/usr/bin/env python3
"""Independent Python implementation of ADR-026 D-050's static rules R2, R3
and R4, and of B2's next_fit, X_end and TTRmax (D-050, D-081 (f), D-085;
T-049 review round 1 L2), compared with the Go validator and compiler on
random models.

The Go side (internal/simulation/engine TestCalendarRulesExport, run by
`make simulation-reference-test`) writes random models, each with the
validator's issues and, when valid, each process's X_end, TTRmax and
next_fit from several ticks. This script reads only the models' IR and
recomputes everything from D-050's text, without the engine, the
validator, the calendar package or their algorithms:

- S(t) of a resource: inside an exception its capacity C (open) or 0
  (closed); else the shift holding t mod period, at its label's
  shift_capacity (C when unlabelled or unlisted); 0 between shifts. A
  resource without a calendar has S = C. Times are in 1 ms ticks, each
  model value rounded half up, a positive value at least one tick.
- R2: the least common multiple L of every calendar's period is at most
  365 days (inclusive), else CALENDAR_INVALID at /calendars and no bundle
  rule is checked; a process whose bundle has a resource on a calendar has
  a tick in the LCM of its calendars' periods at which every resource has
  its units on shift (the periodic part, no exceptions), else
  CALENDAR_INVALID at /nodes/<i>/requires.
- R3: a requirement of a resource that breaks down asks for 1 unit
  (RESOURCE_REQUIREMENT_INVALID at /nodes/<i>/requires/<k>/units), and a
  bundle has at most one such resource (the same code at
  /nodes/<i>/requires).
- R4, for a bundle R2 and R3 accept: while a resource of it that breaks
  down is on shift (S >= 1), each other resource has the units the bundle
  needs, over the LCM of the two periods (the periodic part), else
  STALL_RISK at /nodes/<i>/requires, once per such resource.
- next_fit(from) of a bundle with a resource on a calendar: the first tick
  t >= from, t <= the horizon, at which every resource has its units on
  shift, exceptions included; X_end: the end of the last exception of the
  bundle's calendars (0 without one); TTRmax: the longest repair bound of
  the bundle's resources that break down (constant: its value, triangular:
  its max), -1 when one has no bound, 0 without one.

The search is brute force over the change points of S (every shift and
exception boundary in the range), not a leapfrog. It checks that the
random models exercise each rule (minimum counts) and exits non-zero on any
difference.

    python scripts/calendar_reference.py --go-results bin/calendar-reference-go.json
"""
import argparse
import json
import math
import sys
from fractions import Fraction

UNIT_MS = {"millisecond": 1, "second": 1000, "minute": 60_000, "hour": 3_600_000, "day": 86_400_000}
MAX_TICKS = 365 * 86_400_000


def ticks(value, ms):
    t = math.floor(Fraction(value) * ms + Fraction(1, 2))
    return 1 if t == 0 and value > 0 else t


class Pattern:
    """S(t) of one resource on one calendar, in ticks."""

    def __init__(self, cal, capacity, shift_capacity, ms):
        self.capacity = capacity
        self.period = ticks(cal["period"], ms)
        self.shifts = []
        for s in cal["shifts"]:
            units = capacity
            if s.get("label") and s["label"] in (shift_capacity or {}):
                units = shift_capacity[s["label"]]
            self.shifts.append((ticks(s["start"], ms), ticks(s["end"], ms), units))
        self.exceptions = [(ticks(e["start"], ms), ticks(e["end"], ms), capacity if e["open"] else 0)
                           for e in cal.get("exceptions", [])]

    def periodic(self, t):
        x = t % self.period
        for a, b, u in self.shifts:
            if a <= x < b:
                return u
        return 0

    def at(self, t):
        for a, b, u in self.exceptions:
            if a <= t < b:
                return u
        return self.periodic(t)

    def points(self, lo, hi, exceptions):
        """The change points of S in (lo, hi]."""
        out = set()
        for k in range(lo // self.period, hi // self.period + 1):
            for a, b, _ in self.shifts:
                out.update((k * self.period + a, k * self.period + b))
            out.add((k + 1) * self.period)
        if exceptions:
            for a, b, _ in self.exceptions:
                out.update((a, b))
        return {p for p in out if lo < p <= hi}


class Always:
    def __init__(self, capacity):
        self.capacity, self.period, self.exceptions = capacity, None, []

    def periodic(self, t):
        return self.capacity

    def at(self, t):
        return self.capacity

    def points(self, lo, hi, exceptions):
        return set()


def first(bundle, lo, hi, ok, exceptions):
    """The first tick in [lo, hi] at which ok(S of each resource) holds."""
    candidates = {lo}
    for p, _ in bundle:
        candidates |= p.points(lo, hi, exceptions)
    for t in sorted(candidates):
        if t > hi:
            break
        if ok(t):
            return t
    return -1


def lcm(values):
    out = 1
    for v in values:
        out = out * v // math.gcd(out, v)
    return out


def repair_bound(dist, ms):
    if dist["type"] == "constant":
        return ticks(dist["value"], ms)
    if dist["type"] == "triangular":
        return ticks(dist["max"], ms)
    return -1


def evaluate(ir, horizon, stats):
    """The issues (code, path) of R2, R3, R4, and each process's X_end,
    TTRmax and next_fit function."""
    ms = UNIT_MS[ir["time_unit"]]
    cals = {c["id"]: c for c in ir.get("calendars", [])}
    res = {r["id"]: r for r in ir["resources"]}
    pats = {}
    for r in ir["resources"]:
        c = cals.get(r.get("calendar_id"))
        pats[r["id"]] = Pattern(c, r["capacity"], r.get("shift_capacity"), ms) if c else Always(r["capacity"])
    issues = []
    r3 = set()
    for i, n in enumerate(ir["nodes"]):
        broken = [q["resource_id"] for q in n.get("requires", []) if res[q["resource_id"]].get("breakdowns")]
        for k, q in enumerate(n.get("requires", [])):
            if res[q["resource_id"]].get("breakdowns") and q["units"] != 1:
                issues.append(("RESOURCE_REQUIREMENT_INVALID", f"/nodes/{i}/requires/{k}/units"))
                r3.add(i)
        if len(broken) > 1:
            issues.append(("RESOURCE_REQUIREMENT_INVALID", f"/nodes/{i}/requires"))
            r3.add(i)
    stats["r3"] += len(r3)
    L = lcm([ticks(c["period"], ms) for c in cals.values()]) if cals else 1
    if L > MAX_TICKS:
        stats["lcm_over"] += 1
        issues.append(("CALENDAR_INVALID", "/calendars"))
        return issues, {}
    if cals and L == MAX_TICKS:
        stats["lcm_exactly_365_days"] += 1
    nodes = {}
    for i, n in enumerate(ir["nodes"]):
        if n["type"] != "process":
            continue
        reqs = n.get("requires", [])
        bundle = [(pats[q["resource_id"]], q["units"]) for q in reqs]
        calendared = [p for p, _ in bundle if p.period is not None]
        ttr = [repair_bound(res[q["resource_id"]]["breakdowns"]["time_to_repair"]["distribution"], ms)
               for q in reqs if res[q["resource_id"]].get("breakdowns")]
        ttr_max = -1 if -1 in ttr else max(ttr, default=0)
        x_end = max((max((b for _, b, _ in p.exceptions), default=0) for p in calendared), default=0)
        nodes[n["id"]] = {"x_end": x_end, "ttr_max": ttr_max, "bundle": bundle if calendared else None}
        if not calendared:
            continue
        span = lcm([p.period for p in calendared])
        if first(bundle, 0, span - 1, lambda t: all(p.periodic(t) >= u for p, u in bundle), False) < 0:
            stats["r2"] += 1
            issues.append(("CALENDAR_INVALID", f"/nodes/{i}/requires"))
            continue
        if i in r3:
            continue
        for q in sorted(reqs, key=lambda q: q["resource_id"]):
            if not res[q["resource_id"]].get("breakdowns"):
                continue
            m = pats[q["resource_id"]]
            short = False
            for o in reqs:
                if o["resource_id"] == q["resource_id"]:
                    continue
                po = pats[o["resource_id"]]
                periods = [p.period for p in (m, po) if p.period is not None]
                span = lcm(periods) if periods else 1
                pair = [(m, 1), (po, o["units"])]
                bad = first(pair, 0, span - 1, lambda t: m.periodic(t) >= 1 and po.periodic(t) < o["units"], False)
                if bad >= 0:
                    short = True
                    if bad >= max(periods, default=1):
                        stats["r4_past_larger_period"] += 1
            if short:
                stats["r4"] += 1
                issues.append(("STALL_RISK", f"/nodes/{i}/requires"))
    return issues, nodes


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--go-results", required=True)
    args = ap.parse_args()
    doc = json.loads(open(args.go_results).read())
    stats = {k: 0 for k in ("models", "valid", "r2", "r3", "r4", "r4_past_larger_period", "lcm_over", "lcm_exactly_365_days",
                            "next_fit", "next_fit_found", "next_fit_none", "x_end_positive", "ttr_bounded", "ttr_unbounded")}
    diffs = []
    for c in doc["cases"]:
        stats["models"] += 1
        ir = c["model"]
        issues, nodes = evaluate(ir, c["horizon_ticks"], stats)
        go = sorted(tuple(x) for x in c["issues"])
        if sorted(issues) != go:
            diffs.append(f"seed {c['seed']}: issues {sorted(issues)} != Go {go}")
            continue
        if any(code for code, _ in issues):
            if c.get("nodes"):
                diffs.append(f"seed {c['seed']}: Go compiled an invalid model")
            continue
        stats["valid"] += 1
        got = c.get("nodes") or {}
        if set(got) != set(nodes):
            diffs.append(f"seed {c['seed']}: processes {sorted(got)} != {sorted(nodes)}")
            continue
        for nid, want in nodes.items():
            g = got[nid]
            if (g["x_end"], g["ttr_max"]) != (want["x_end"], want["ttr_max"]):
                diffs.append(f"seed {c['seed']} {nid}: X_end, TTRmax Go {g['x_end']}, {g['ttr_max']} != {want['x_end']}, {want['ttr_max']}")
            stats["x_end_positive"] += want["x_end"] > 0
            stats["ttr_bounded"] += want["ttr_max"] > 0
            stats["ttr_unbounded"] += want["ttr_max"] < 0
            if want["bundle"] is None:
                if g["next_fit"]:
                    diffs.append(f"seed {c['seed']} {nid}: next_fit without a calendar")
                continue
            if not g["next_fit"]:
                diffs.append(f"seed {c['seed']} {nid}: no next_fit from Go")
            bundle = want["bundle"]
            for frm, at in g["next_fit"]:
                mine = first(bundle, frm, c["horizon_ticks"], lambda t: all(p.at(t) >= u for p, u in bundle), True)
                stats["next_fit"] += 1
                stats["next_fit_found" if mine >= 0 else "next_fit_none"] += 1
                if mine != at:
                    diffs.append(f"seed {c['seed']} {nid}: next_fit({frm}) Go {at} != {mine}")
    # Each rule must be exercised: floors well below the counts of the
    # committed generator (5000 models), so a weakened generator fails.
    floors = {"models": 4000, "valid": 1000, "r2": 200, "r3": 200, "r4": 200, "r4_past_larger_period": 15, "lcm_over": 100,
              "lcm_exactly_365_days": 50, "next_fit": 10000, "next_fit_found": 5000, "next_fit_none": 1000, "x_end_positive": 1000,
              "ttr_bounded": 300, "ttr_unbounded": 150}
    weak = [f"{k} {stats[k]} < {v}" for k, v in floors.items() if stats[k] < v]
    print(json.dumps({"stats": stats, "differences": len(diffs), "too_weak": weak}, indent=1))
    for d in diffs[:20]:
        print(d)
    if diffs or weak:
        print(f"FAIL calendar rules reference: {len(diffs)} difference(s), {len(weak)} weak count(s)")
        return 1
    print("PASS calendar rules reference (R2, R3, R4, next_fit, X_end, TTRmax)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
