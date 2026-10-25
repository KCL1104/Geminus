#!/usr/bin/env python3
"""Mutants of T-049's code: calendars and release schedules in the kernel
(internal/simulation/engine: CAPACITY_CHANGE, the shift start's wake, the
grant against min(S, C - D), D-050 B1's bound and B2's deadline, failure
clocks on shift, the rate profile and release tables), the calendar
arithmetic, the validator's R2, R4 and LCM, the Result members, the D-057
risk and D-083 pool rule, and the SimPy reference's interpretation.

A Go mutant is one exact search/replace applied with `go test -overlay` (the
working tree is never modified); KILLED means the listed packages' tests
failed. A Python mutant is the same replace on a temporary copy of
scripts/simpy_reference.py, run against bin/simpy-reference-go.json (written
by `make simulation-reference-test` first); KILLED means it exited non-zero.
A mutant whose pattern is not found exactly once is BAD-MUTANT; one marked
equivalent must survive (EQUIVALENT), with the reason next to it.

    python3 checks/evidence/2026-10-25-T-049/t049-mutants.py [repo] [name...]
    PYTHON=/path/to/python-with-simpy python3 ... (for the Python mutants)
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

REPO = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 and not sys.argv[1].startswith("-") and pathlib.Path(sys.argv[1]).is_dir() else pathlib.Path(__file__).resolve().parents[3]
ONLY = [a for a in sys.argv[1:] if not pathlib.Path(a).is_dir()]
PY = os.environ.get("PYTHON", "python3")
K = "internal/simulation/engine/kernel.go"
R = "internal/simulation/engine/replication.go"
C = "internal/simulation/engine/compile.go"
RL = "internal/simulation/engine/release.go"
CAL = "internal/model/calendar/calendar.go"
VC = "internal/model/validate/calendars.go"
M = "internal/results/metrics/aggregate.go"
RK = "internal/ai/review/risk.go"
RV = "internal/ai/review/review.go"
IP = "internal/ai/idpool/idpool.go"
SP = "scripts/simpy_reference.py"
ENG = ["./internal/simulation/engine/"]
SIM = ["./internal/simulation/...", "./internal/results/..."]
VAL = ["./internal/model/...", "./internal/simulation/engine/"]
AI = ["./internal/ai/..."]
MUT = [
    # B1 (D-050): the bound is the full capacity C, so a request held by the
    # units on shift is capacity-bound (G-K2; the task's required mutant).
    ("deadlock-bound-ge", K, "\t\t\tif k.inUse[q.resource]+q.units > p.resources[q.resource].capacity {\n\t\t\t\tbound = true",
     "\t\t\tif k.inUse[q.resource]+q.units >= p.resources[q.resource].capacity {\n\t\t\t\tbound = true", ENG),
    ("deadlock-bound-uses-shift", K, "\t\t\tif k.inUse[q.resource]+q.units > p.resources[q.resource].capacity {\n\t\t\t\tbound = true",
     "\t\t\tif k.inUse[q.resource]+q.units > k.capS[q.resource] {\n\t\t\t\tbound = true", ENG),
    # CAPACITY_CHANGE (D-052, D-081 (a)): the shift start wakes the held front
    # without a release (the task's required mutant), a dispatch follows,
    # a fall wakes nothing, only changed resources are observed, the next
    # change is scheduled first, resources in ID order, calendars in ID order.
    ("shift-start-no-wake", K, "if f := k.queues[i].front(); up && f != nil && f.held {", "if f := k.queues[i].front(); false && up && f != nil && f.held {", ENG),
    ("shift-start-wakes-without-dispatch", K, "\tif woke {\n\t\treturn k.needDispatch()\n\t}\n", "\tif false && woke {\n\t\treturn k.needDispatch()\n\t}\n", ENG),
    # (run 1: BUILD-FAILED, the mutant left `up` unused; it keeps it now.
    # Run 2: killed only by the 0.4.0 observation lock; the hand-derived
    # G-C9 kills it now: its extra DISPATCH makes 14 events, not 13)
    ("shift-end-wakes-too", K, "if f := k.queues[i].front(); up && f != nil && f.held {", "if f := k.queues[i].front(); (up || true) && f != nil && f.held {", ENG),
    ("dispatch-ignores-shift", K, "\t\t\tavail := min(k.capS[q.resource], p.resources[q.resource].capacity-k.down[q.resource])\n",
     "\t\t\tavail := p.resources[q.resource].capacity - k.down[q.resource]\n", ENG),
    ("capacity-observes-unchanged", K, "\t\tif s == k.capS[i] {\n\t\t\tcontinue\n\t\t}\n", "", ENG),
    ("capacity-next-change-last", K, "\tif err := k.scheduleChange(c); err != nil {\n\t\treturn err\n\t}\n\twoke := false\n",
     "\tdefer k.scheduleChange(c)\n\twoke := false\n", ENG),
    ("capacity-resources-reversed", C, "\t\t\tp.calendars[ci].resources = append(p.calendars[ci].resources, i)\n",
     "\t\t\tp.calendars[ci].resources = append([]int{i}, p.calendars[ci].resources...)\n", ENG),
    ("calendars-reverse-id-order", C, "\tslices.SortFunc(calendars, byID(func(c model.Calendar) string { return c.ID }))\n",
     "\tslices.SortFunc(calendars, func(a, b model.Calendar) int { return -cmp.Compare(a.ID, b.ID) })\n", ENG),
    ("clocks-before-calendars", K, "\tfor c := range r.p.calendars {\n\t\tif err := k.scheduleChange(c); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\tif r.p.breakdowns {\n\t\treturn k.startClocks(r)\n\t}\n\treturn nil\n",
     "\tif r.p.breakdowns {\n\t\tif err := k.startClocks(r); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\tfor c := range r.p.calendars {\n\t\tif err := k.scheduleChange(c); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\treturn nil\n", ENG),
    # D-051 (ii): the failure clock runs on shift only; a target reached at
    # a shift's end is that end's tick; a shift start gives a waiting
    # failure nothing (D-081 (c)).
    ("failure-clock-calendar-time", K, "\tif res.pattern == nil || res.pattern.AlwaysOn() {\n", "\tif true {\n", ENG),
    ("failure-at-end-is-next-on", CAL, "\t\tif p.OnTicks(mid) >= target {\n", "\t\tif p.OnTicks(mid) > target {\n", ["./internal/model/calendar/", "./internal/simulation/engine/"]),
    ("shift-start-satisfies-waiting-failure", K, "\t\tif f := k.queues[i].front(); up && f != nil && f.held {",
     "\t\tif up && k.pending != nil && len(k.pending[i]) > 0 {\n\t\t\tw := k.pending[i][0]\n\t\t\tk.pending[i] = k.pending[i][1:]\n\t\t\tif err := k.goDown(i, w); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n\t\tif f := k.queues[i].front(); up && f != nil && f.held {", ENG),
    # B2 (D-050): next_fit(max(since, X_end) + TTRmax); the check runs with
    # calendars too; since is not reset by a wake within the state.
    ("b2-deadline-ignores-xend", K, "\tfrom := max(k.r.now, node.xEnd) + node.ttrMax\n", "\tfrom := k.r.now + node.ttrMax\n", ENG),
    ("b2-deadline-no-ttrmax", K, "\tfrom := max(k.r.now, node.xEnd) + node.ttrMax\n", "\tfrom := max(k.r.now, node.xEnd)\n", ENG),
    ("b2-deadline-no-next-fit", K, "\tif node.need == nil {\n\t\treturn from // every resource on shift at every tick\n\t}\n", "\tif true {\n\t\treturn from\n\t}\n", ENG),
    # Equivalent: next_fit is only ever asked from a tick at or after X_end,
    # where no exception is left, so with or without them it is the same.
    ("b2-next-fit-ignores-exceptions", K, "\tat, ok := calendar.NextFit(node.need, from, k.r.p.horizon, true)\n", "\tat, ok := calendar.NextFit(node.need, from, k.r.p.horizon, false)\n", ENG, "equivalent"),
    # Equivalent (run 1: SURVIVED; D-081 (f)): without breakdowns B2 never
    # fires. A request in its state is held at the front of every FIFO it
    # is in with none of its bundle in use; its deadline next_fit(...) is
    # a tick at which a CAPACITY_CHANGE makes the bundle fit, and that event
    # wakes it and the tick's dispatch grants it (nothing else can take the
    # units: it is the front), so it never passes the deadline; the check
    # is a guard there.
    ("b2-off-with-calendars-only", K, "\tif (p.breakdowns || p.calendared) && k.held > 0 && k.tickEnds() {\n", "\tif p.breakdowns && k.held > 0 && k.tickEnds() {\n", ENG, "equivalent"),
    ("b2-since-reset-by-shift-wake", K, "\t\t\tf.held = false\n\t\t\tk.held--\n\t\t\theap.Push(&k.cands, f)\n\t\t\twoke = true\n",
     "\t\t\tf.held = false\n\t\t\tf.stalled = -1\n\t\t\tk.held--\n\t\t\theap.Push(&k.cands, f)\n\t\t\twoke = true\n", ENG),
    # D-053: the rate profile and release tables
    ("release-no-floor", R, "\t\tat := max(s.tick(r.acc[src]), r.last[src]+1)\n", "\t\tat := s.tick(r.acc[src])\n", ENG),
    # Equivalent (D-082 (a)): U is strictly inside (0, 1), so X_1 > 0 and
    # τ(X_1) > 0, which the quantizer makes at least one tick; the first
    # arrival's floor, tick 0 or 1, never applies.
    ("release-first-floor-one-tick", R, "\t\t\t\tr.last[i] = -1\n", "\t\t\t\tr.last[i] = 0\n", ENG, "equivalent"),
    ("release-no-correction", RL, "\tif r <= 0 {\n\t\tk, r = k-1, r+s.total\n\t} else if r > s.total {\n\t\tk, r = k+1, r-s.total\n\t}\n", "", ENG),
    # (run 1: killed by the random checker only; the `boundary` vectors
    # of engine-vectors.json, a at a segment's cumulative end, kill it now)
    ("release-segment-strictly-after", RL, "\t\tif s.rates[i] > 0 && s.cumEnd[i] >= r {\n", "\t\tif s.rates[i] > 0 && s.cumEnd[i] > r {\n", ENG),
    # (run 1: BUILD-FAILED, `hi` unused; it keeps it now, and the `clamp`
    # vectors kill it)
    ("release-no-clamp", RL, "\treturn min(max(lo+(r-s.cumStart[j])/s.rates[j], lo), hi)\n", "\treturn lo + (r-s.cumStart[j])/s.rates[j] + 0*hi\n", ENG),
    ("release-extra-draw", RL, "\treturn -ln(1 - float64(rng.Uniform()))\n", "\trng.Uniform()\n\treturn -ln(1 - float64(rng.Uniform()))\n", ENG),
    ("table-rows-after-entities", R, "\t\tif err := r.scheduleRow(src, quantity); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tfor range quantity {\n\t\t\tif err := r.arrive(src); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n\t\treturn nil\n",
     "\t\tfor range quantity {\n\t\t\tif err := r.arrive(src); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n\t\treturn r.scheduleRow(src, quantity)\n", ENG),
    ("table-schedules-an-empty-row", R, "\tif n.maxEntities > 0 && r.arrived[src]+uint64(quantity) >= uint64(n.maxEntities) {\n",
     "\tif n.maxEntities > 0 && r.arrived[src]+uint64(quantity) > uint64(n.maxEntities) {\n", ENG),
    ("table-quantity-not-clipped", R, "\t\t\tquantity = min(quantity, limit-int(r.arrived[src]))\n", "", ENG),
    # Result members (D-058)
    ("overtime-zero", M, "\tc.overtime[i].set(max(u-s, 0), t, c.w, c.h)\n", "\tc.overtime[i].set(0, t, c.w, c.h)\n", SIM),
    ("available-ignores-shift", M, "\ta := min(s, int64(c.p.ResourceCapacity(i))-d)\n", "\ta := int64(c.p.ResourceCapacity(i)) - d\n", SIM),
    # (run 1: BUILD-FAILED, `k` unused; it keeps it now)
    ("processing-ignores-blocked", M, "\tc.processing[i].set(min(u-k, s), t, c.w, c.h)\n", "\tc.processing[i].set(min(u+0*k, s), t, c.w, c.h)\n", SIM),
    ("down-does-not-update-shift-members", M, "\t\tc.down[o.Resource].set(o.Value, o.Tick, c.w, c.h)\n\t\tc.shift(o.Resource, o.Tick)\n\t\tc.downSince",
     "\t\tc.down[o.Resource].set(o.Value, o.Tick, c.w, c.h)\n\t\tc.downSince", SIM),
    ("calendar-resources-not-listed", M, "\t\tonCalendar := c.calendars && p.ResourceHasCalendar(i)\n", "\t\tonCalendar := false\n", SIM),
    # Validator: R2, R4, the LCM limit
    ("r2-off", VC, "\t\tif _, fit := calendar.NextFit(bundle, 0, period-1, false); !fit {\n", "\t\tif _, fit := calendar.NextFit(bundle, 0, period-1, false); false && !fit {\n", VAL),
    ("r4-off", VC, "\t\t\tif len(short) > 0 {\n", "\t\t\tif false && len(short) > 0 {\n", VAL),
    # (run 1: SURVIVED: the test's exception was past the periods' LCM, one
    # day; TestBundleFeasibility has one inside it now)
    ("r4-reads-exceptions", VC, "\t\t\t\t\tfunc(t int64) (int64, bool) { return partner.NextBelow(t, o.Units, false) }); bad {\n",
     "\t\t\t\t\tfunc(t int64) (int64, bool) { return partner.NextBelow(t, o.Units, true) }); bad {\n", VAL),
    ("lcm-unbounded", VC, "\t\tif lcm, ok = calendar.LCM(lcm, p, model.MaxDurationTicks); !ok {\n", "\t\tif lcm, ok = calendar.LCM(lcm, p, 1<<62); !ok {\n", VAL),
    # D-057 risk, D-083, the numbers an effect may quote
    ("risk-resource-calendar-off", RK, "\t\tif ok && (c.CalendarID != r.CalendarID || !sameShiftCapacity(r.ShiftCapacity, c.ShiftCapacity)) {\n", "\t\tif false {\n", AI),
    ("risk-ignores-shift-capacity", RK, "\t\tif ok && (c.CalendarID != r.CalendarID || !sameShiftCapacity(r.ShiftCapacity, c.ShiftCapacity)) {\n", "\t\tif ok && c.CalendarID != r.CalendarID {\n", AI),
    ("risk-calendars-added-low", RK, "\tif added(baseCals, candCals) || added(candCals, baseCals) {\n", "\tif false {\n", AI),
    ("risk-calendar-ignores-exceptions", RK, " && slices.Equal(a.Exceptions, b.Exceptions)", "", AI),
    ("risk-calendar-ignores-shifts", RK, "a.Period == b.Period && slices.Equal(a.Shifts, b.Shifts)", "a.Period == b.Period", AI),
    ("risk-schedule-off", RK, "\t\tif ok && c.Type == n.Type && !sameSchedule(arrivalSchedule(n), arrivalSchedule(c)) {\n", "\t\tif false {\n", AI),
    ("risk-schedule-ignores-releases", RK, " && slices.Equal(a.Releases, b.Releases)", "", AI),
    ("idpool-allows-new-calendars", IP, "\t\tif !baseCalendars[c.ID] {\n", "\t\tif false {\n", AI),
    ("effects-without-calendar-values", RV, "\tfor _, c := range ir.Calendars {\n\t\tout = append(out, c.Period)\n", "\tfor _, c := range ir.Calendars[:0] {\n\t\tout = append(out, c.Period)\n", AI),
    ("effects-without-schedule-values", RV, "\t\t\tfor _, seg := range s.Segments {\n\t\t\t\tout = append(out, seg.Start, seg.Rate)\n\t\t\t}\n", "", AI),
    # SimPy reference: its interpretation must catch a wrong one
    ("simpy-no-blockers", SP, "        while len(blockers[r]) < want:\n", "        while False and len(blockers[r]) < want:\n", "py"),
    ("simpy-no-initial-blockers", SP, "    for r in m.cal:\n        set_shift(r, shift[r], observed=False)\n", "", "py"),
    ("simpy-failure-clock-calendar-time", SP, "        if r not in m.cal:\n            yield env.timeout(d)\n            return\n", "        if True:\n            yield env.timeout(d)\n            return\n", "py"),
    ("simpy-calendar-events-normal", SP, "    env.schedule(ev, URGENT, delay)\n", "    env.schedule(ev, 1, delay)\n", "py"),
    ("simpy-thinning-keeps-all", SP, "            if rng.random() * rmax < seg[\"rate\"]:\n", "            if True:\n", "py"),
    ("simpy-shift-start-releases-nothing", SP, "        while len(blockers[r]) > want:\n", "        while False and len(blockers[r]) > want:\n", "py"),
]


def run_go(i, tmp, f, old, new, pkgs):
    src = (REPO / f).read_text()
    m = pathlib.Path(tmp) / f"m{i}.go"
    m.write_text(src.replace(old, new))
    ov = pathlib.Path(tmp) / f"m{i}.json"
    ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
    # -failfast: a package stops at its first failing test (a killed
    # mutant needs only one); a survivor runs every test.
    p = subprocess.run(["go", "test", "-count=1", "-failfast", "-timeout", "900s", "-overlay", str(ov), *pkgs],
                       cwd=REPO, capture_output=True, text=True, env=dict(os.environ))
    return p.returncode, p.stdout + p.stderr


def run_py(i, tmp, f, old, new):
    src = (REPO / f).read_text().replace(old, new)
    root = "ROOT = Path(__file__).resolve().parents[1]\n"
    if src.count(root) != 1:
        return 3, "the script's ROOT line moved"
    src = src.replace(root, f"ROOT = Path({str(REPO)!r})\n")
    m = pathlib.Path(tmp) / f"m{i}.py"
    m.write_text(src)
    p = subprocess.run([PY, "-I", str(m), "--go-results", str(REPO / "bin/simpy-reference-go.json")], cwd=tmp, capture_output=True, text=True)
    return p.returncode, p.stdout + p.stderr


def main() -> int:
    if "--patterns" in ONLY:  # only check that every pattern is found exactly once
        bad = [(name, (REPO / f).read_text().count(old)) for name, f, old, *_ in MUT if (REPO / f).read_text().count(old) != 1]
        print(f"{len(MUT)} mutants, bad patterns: {bad}")
        return 1 if bad else 0
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new, pkgs, *flags) in enumerate(MUT):
            if ONLY and name not in ONLY:
                continue
            src = (REPO / f).read_text()
            if src.count(old) != 1:
                results.append((name, f"BAD-MUTANT (pattern found {src.count(old)} times)"))
                print("\t".join(results[-1]), flush=True)
                continue
            if pkgs == "py":
                code, o = run_py(i, tmp, f, old, new)
                first = next((l.strip() for l in o.splitlines() if l.startswith(("FAIL", "Traceback")) or "!=" in l), "")
            else:
                code, o = run_go(i, tmp, f, old, new, pkgs)
                first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:"))), "")
            if code == 0:
                status = "SURVIVED"
            elif "build failed" in o or "setup failed" in o or "[build failed]" in o:
                status = "BUILD-FAILED"
            else:
                status = "KILLED"
            if "equivalent" in flags:
                status = "EQUIVALENT" if status == "SURVIVED" else f"UNEXPECTED-{status}"
            results.append((name, f"{status} {first[:110]}"))
            print("\t".join(results[-1]), flush=True)
    killed = sum(1 for r in results if r[1].startswith("KILLED"))
    equivalent = sum(1 for r in results if r[1].startswith("EQUIVALENT"))
    print(f"{killed}/{len(results) - equivalent} killed, {equivalent} equivalent")
    return 0 if killed + equivalent == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
