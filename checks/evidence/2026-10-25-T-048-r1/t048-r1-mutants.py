#!/usr/bin/env python3
"""Mutants of T-048's code: resource breakdowns in the kernel
(internal/simulation/engine: failure clocks, finish-first, the repair's wake,
D-050 B1 and B2), the samplers' TTRmax, the validator's R3 and D-075 ranges,
the Result members, the runner's and the API's stall mapping, the D-057
risk, the engine version, and the SimPy reference's interpretation.

A Go mutant is one exact search/replace applied with `go test -overlay` (the
working tree is never modified); KILLED means the listed packages' tests
failed. A Python mutant is the same replace on a temporary copy of
scripts/simpy_reference.py, run against bin/simpy-reference-go.json (written
by `make simulation-reference-test` first); KILLED means it exited non-zero.
A mutant whose pattern is not found exactly once is BAD-MUTANT; one marked
equivalent must survive (EQUIVALENT), with the reason next to it.

    python3 checks/evidence/2026-10-25-T-048-r1/t048-r1-mutants.py [repo] [name...]
    PYTHON=/path/to/python-with-simpy python3 ... (for the Python mutants)

Review round 1 (T-048 r1): D-074 (a) is option B now (a repaired unit goes
to the waiting failures first), so the mutant that made a repair satisfy a
waiting failure became the code and its opposite, repair-ignores-waiting-
failures, is the mutant; the same for the SimPy reference. M2 adds the two
strict lower bounds of the window (a downtime starting exactly at W).
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
S = "internal/simulation/engine/sample.go"
V = "internal/model/validate/ir.go"
VP = "internal/model/validate/primitives.go"
M = "internal/results/metrics/aggregate.go"
T = "internal/simulation/trace/trace.go"
D = "internal/simulation/des/des.go"
RN = "internal/runs/runner/runner.go"
RS = "internal/runs/runs.go"
RK = "internal/ai/review/risk.go"
RV = "internal/ai/review/review.go"
SP = "scripts/simpy_reference.py"
ENG = ["./internal/simulation/engine/"]
SIM = ["./internal/simulation/...", "./internal/results/..."]
VAL = ["./internal/model/...", "./internal/simulation/engine/"]
RES = ["./internal/results/...", "./internal/simulation/des/"]
MUT = [
    # kernel: B1 tells capacity-bound from release-bound (T-047 review round 2, I1; G-K1)
    ("deadlock-bound-ge", K, "\t\t\tif k.inUse[q.resource]+q.units > p.resources[q.resource].capacity {\n\t\t\t\tbound = true",
     "\t\t\tif k.inUse[q.resource]+q.units >= p.resources[q.resource].capacity {\n\t\t\t\tbound = true", ENG),
    # T-047's equivalent mutant "deadlock-ignores-fitting-held": with
    # breakdowns a held candidate can fit C (capacity-bound), so it is no
    # longer equivalent (G-K1).
    ("deadlock-ignores-capacity-bound", K, "\t\tif !bound {\n\t\t\t// It fits the full capacity", "\t\tif false && !bound {\n\t\t\t// It fits the full capacity", ENG),
    ("deadlock-bound-uses-up-units", K, "\t\t\tif k.inUse[q.resource]+q.units > p.resources[q.resource].capacity {\n\t\t\t\tbound = true",
     "\t\t\tif k.inUse[q.resource]+q.units > p.resources[q.resource].capacity-k.down[q.resource] {\n\t\t\t\tbound = true", ENG),
    # kernel: dispatch with C - D (D-051)
    ("dispatch-ignores-down", K, "\t\t\tif k.inUse[q.resource]+q.units > p.resources[q.resource].capacity-k.down[q.resource] {\n\t\t\t\tfits = false",
     "\t\t\tif k.inUse[q.resource]+q.units > p.resources[q.resource].capacity {\n\t\t\t\tfits = false", ENG),
    # kernel: failures (finish-first, D-051)
    ("failure-preempts", K, "\tif k.r.p.resources[i].capacity-k.down[i]-k.inUse[i] > 0 {\n", "\tif k.r.p.resources[i].capacity-k.down[i] > 0 {\n", ENG),
    ("failure-never-waits", K, "\tif k.r.p.resources[i].capacity-k.down[i]-k.inUse[i] > 0 {\n", "\tif true {\n", ENG),
    ("release-ignores-waiting-failures", K, "\t\tif k.pending != nil {\n\t\t\tfor n := min(", "\t\tif false {\n\t\t\tfor n := min(", ENG),
    ("waiting-failures-lifo", K, "\t\t\t\tu := k.pending[q.resource][0]\n\t\t\t\tk.pending[q.resource] = k.pending[q.resource][1:]\n",
     "\t\t\t\tu := k.pending[q.resource][len(k.pending[q.resource])-1]\n\t\t\t\tk.pending[q.resource] = k.pending[q.resource][:len(k.pending[q.resource])-1]\n", ENG),
    ("release-satisfies-one-failure", K, "for n := min(q.units, len(k.pending[q.resource])); n > 0; n--", "for n := min(1, len(k.pending[q.resource])); n > 0; n--", ENG),
    # (named "release-before-failures-observed-after-wake" in run 1; it
    # removes the release's wake of the held fronts, T-047's mutant)
    ("release-wakes-no-held", K,
     "\t\tif f := k.queues[q.resource].front(); f != nil && f.held {\n\t\t\tf.held = false\n\t\t\tk.held--\n\t\t\theap.Push(&k.cands, f)\n\t\t}\n\t}\n\treturn nil\n}",
     "\t}\n\treturn nil\n}", SIM),
    # kernel: repairs (D-051, D-074 (a))
    ("repair-wakes-no-held", K, "\tif f := k.queues[i].front(); f != nil && f.held {\n\t\tf.held = false\n\t\tk.held--\n\t\theap.Push(&k.cands, f)\n\t\treturn k.needDispatch()\n\t}\n",
     "", ENG),
    ("repair-wakes-without-dispatch", K, "\t\theap.Push(&k.cands, f)\n\t\treturn k.needDispatch()\n", "\t\theap.Push(&k.cands, f)\n\t\treturn nil\n", ENG),
    # D-074 (a) option B since review round 1 (M1): the old mutant
    # repair-satisfies-waiting-failure is the code now; its opposite, option
    # A, must be killed (G-F3, G-F9).
    ("repair-ignores-waiting-failures", K, "\tif len(k.pending[i]) > 0 {\n\t\tw := k.pending[i][0]\n\t\tk.pending[i] = k.pending[i][1:]\n\t\treturn k.goDown(i, w)\n\t}\n", "", ENG),
    ("repair-gives-the-last-waiting-failure", K, "\t\tw := k.pending[i][0]\n\t\tk.pending[i] = k.pending[i][1:]\n\t\treturn k.goDown(i, w)\n",
     "\t\tw := k.pending[i][len(k.pending[i])-1]\n\t\tk.pending[i] = k.pending[i][:len(k.pending[i])-1]\n\t\treturn k.goDown(i, w)\n", ENG),
    ("repair-gives-the-unit-and-wakes-too", K, "\t\tk.pending[i] = k.pending[i][1:]\n\t\treturn k.goDown(i, w)\n",
     "\t\tk.pending[i] = k.pending[i][1:]\n\t\tif err := k.goDown(i, w); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif f := k.queues[i].front(); f != nil && f.held {\n\t\t\tf.held = false\n\t\t\tk.held--\n\t\t\theap.Push(&k.cands, f)\n\t\t\treturn k.needDispatch()\n\t\t}\n\t\treturn nil\n", ENG),
    ("repair-draws-no-next-failure", K, "\tif err := k.scheduleFailure(i, u); err != nil {\n\t\treturn err\n\t}\n\tif len(k.pending[i]) > 0 {", "\tif len(k.pending[i]) > 0 {", ENG),
    # kernel: streams (D-051) and the t=0 order (D-048)
    ("clocks-share-one-stream", K, "\t\tid, unit := k.r.p.resources[i].id, strconv.Itoa(u)\n", "\t\tid, unit := k.r.p.resources[i].id, strconv.Itoa(0*u)\n", ENG),
    ("ttf-and-ttr-one-stream", K, "Stream(k.r.seed, id, PurposeRepairPrefix+unit)", "Stream(k.r.seed, id, PurposeFailurePrefix+unit)", ENG),
    ("clocks-start-before-sources", R, "\tfor _, src := range r.p.sources {\n\t\tif err := r.scheduleArrival(src); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\tif r.onStart != nil {\n\t\tif err := r.onStart(r); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n",
     "\tif r.onStart != nil {\n\t\tif err := r.onStart(r); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\tfor _, src := range r.p.sources {\n\t\tif err := r.scheduleArrival(src); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n", ENG),
    ("clock-beyond-horizon-takes-a-seq", R, "\tif at > r.p.horizon {\n\t\treturn nil\n\t}\n\tr.q.schedule(event{Tick: at, Priority: PriorityResourceState",
     "\tif at > r.p.horizon {\n\t\tr.q.seq++\n\t\treturn nil\n\t}\n\tr.q.schedule(event{Tick: at, Priority: PriorityResourceState", ENG),
    ("failures-at-dispatch-priority", R, "Priority: PriorityResourceState, Kind: kind", "Priority: PriorityDispatch, Kind: kind", ENG),
    # kernel: D-050 B2 (D-074 (c))
    ("no-stall-check", K, "\t\treturn k.stall()\n", "\t\treturn nil\n", ENG),
    ("stall-check-after-every-event", K, "\tif p.breakdowns && k.held > 0 && k.tickEnds() {\n", "\tif p.breakdowns && k.held > 0 {\n", ENG),
    ("stall-deadline-strict", K, "m >= 0 && k.r.now >= f.stalled+m &&", "m >= 0 && k.r.now > f.stalled+m &&", ENG),
    ("stall-unbounded-checked", K, "m >= 0 && k.r.now >= f.stalled+m &&", "k.r.now >= f.stalled+m &&", ENG),
    ("stall-ignores-units-in-use", K, "\t\t\tif k.inUse[q.resource] > 0 {\n\t\t\t\tidle = false\n", "\t\t\tif false && k.inUse[q.resource] > 0 {\n\t\t\t\tidle = false\n", ENG),
    ("stall-since-reset-by-a-wake", K, "\t\tk.held--\n\t\theap.Push(&k.cands, f)\n\t\treturn k.needDispatch()\n", "\t\tk.held--\n\t\tf.stalled = -1\n\t\theap.Push(&k.cands, f)\n\t\treturn k.needDispatch()\n", ENG),
    ("stall-reports-the-latest", K, "(due == nil || f.seq < due.seq)", "(due == nil || f.seq > due.seq)", ENG),
    # compile: TTRmax and the program flags
    ("bundle-ttrmax-ignores-unbounded", C, "\t\t\t\t\tif r.ttrMax < 0 || c.ttrMax < 0 {\n", "\t\t\t\t\tif c.ttrMax < 0 {\n", ENG),
    ("bound-of-triangular-is-mode", S, "\t\treturn quantize(s.max, s.unitMs)\n", "\t\treturn quantize(s.mode, s.unitMs)\n", ENG),
    ("no-breakdowns-flag", C, "\t\t\tp.breakdowns = true\n", "", SIM),
    ("kernel-does-not-execute-breakdowns", C, "\tvalidate.Breakdowns: true, validate.Lognormal: true, validate.Weibull: true}", "\tvalidate.Lognormal: true, validate.Weibull: true}", ENG),
    ("kernel-does-not-execute-weibull", C, "\tvalidate.Breakdowns: true, validate.Lognormal: true, validate.Weibull: true}", "\tvalidate.Breakdowns: true, validate.Lognormal: true}", ENG),
    ("lognormal-mu-without-correction", S, "s.mu = math.Sqrt(s2), ln(mean)-float64(s2/2)", "s.mu = math.Sqrt(s2), ln(mean)", ENG),
    ("weibull-sample-without-shape", S, "\t\treturn s.scale * exp(ln(-ln(1-u))/s.shape)\n", "\t\treturn s.scale * exp(ln(-ln(1-u)))\n", ENG),
    # validator: R3, D-075 ranges, resource distributions
    ("r3-off", V, "\tif len(breaks) == 0 {\n\t\treturn nil\n\t}\n\tfor i, n := range ir.Nodes {\n\t\tvar found []string", "\tif true {\n\t\treturn nil\n\t}\n\tfor i, n := range ir.Nodes {\n\t\tvar found []string", VAL),
    ("r3-allows-two-units", V, "\t\t\tif q.Units != 1 {\n", "\t\t\tif q.Units > 2 {\n", VAL),
    ("r3-allows-two-resources", V, "\t\tif len(found) > 1 {\n\t\t\tslices.Sort(found)\n", "\t\tif len(found) > 2 {\n\t\t\tslices.Sort(found)\n", VAL),
    ("cv-range-100", V, "\tMaxLognormalCV  = 10.0\n", "\tMaxLognormalCV  = 100.0\n", VAL + ["./internal/ai/..."]),
    ("shape-min-0.01", V, "\tMinWeibullShape = 0.1\n", "\tMinWeibullShape = 0.01\n", VAL),
    ("ttf-may-be-zero", V, "d.Member == \"breakdowns/time_to_failure\" && canBeZero(d.Distribution)", "false && canBeZero(d.Distribution)", VAL),
    ("resource-limits-unchecked", V, "\t\t\tdurations(d.Distribution, Pointer(\"resources\", idx(i))+\"/\"+d.Member+\"/distribution\")\n", "\t\t\t_ = d\n", VAL),
    ("breakdowns-unchecked", VP, "var checked = map[Primitive]bool{FiniteBuffer: true, Breakdowns: true, Lognormal: true, Weibull: true}",
     "var checked = map[Primitive]bool{FiniteBuffer: true, Lognormal: true, Weibull: true}", VAL + ["./internal/ai/review/"]),
    # Result members (D-058, D-074 (d))
    ("down-not-integrated", M, "\t\tc.down[o.Resource].set(o.Value, o.Tick, c.w, c.h)\n\t\tc.downSince[o.Resource][o.At] = o.Tick\n", "\t\tc.downSince[o.Resource][o.At] = o.Tick\n", RES),
    ("downs-started-ignore-window", M, "\t\tif c.inWindow(o.Tick) {\n\t\t\tc.downsStarted[o.Resource]++\n\t\t}\n", "\t\tc.downsStarted[o.Resource]++\n", RES),
    # Review round 1, M2: a downtime that starts exactly at W counts (G-F1W10).
    ("downs-started-strict-warmup", M, "\t\tif c.inWindow(o.Tick) {\n\t\t\tc.downsStarted[o.Resource]++\n", "\t\tif c.w < o.Tick && o.Tick <= c.h {\n\t\t\tc.downsStarted[o.Resource]++\n", RES),
    ("repairs-strict-warmup", M, "\t\tif since := c.downSince[o.Resource][o.At]; since >= c.w && o.Tick <= c.h {\n", "\t\tif since := c.downSince[o.Resource][o.At]; since > c.w && o.Tick <= c.h {\n", RES),
    ("repairs-ignore-window", M, "\t\tif since := c.downSince[o.Resource][o.At]; since >= c.w && o.Tick <= c.h {\n", "\t\tif since := c.downSince[o.Resource][o.At]; o.Tick <= c.h {\n", RES),
    ("zero-repairs-not-counted", M, "\t\tif since := c.downSince[o.Resource][o.At]; since >= c.w && o.Tick <= c.h {\n", "\t\tif since := c.downSince[o.Resource][o.At]; since >= c.w && o.Tick <= c.h && o.Tick > since {\n", RES),
    ("available-ignores-down", M, "\t\t\tavailable := int64(p.ResourceCapacity(i))*(h-w) - down\n", "\t\t\tavailable := int64(p.ResourceCapacity(i)) * (h - w)\n", RES),
    ("every-resource-listed", M, "\t\tif !c.finite && !breaks {\n\t\t\tcontinue // no line-flow feature applies to it\n\t\t}\n", "", RES),
    # Equivalent: the trace skips every observation without an active
    # selected entity, and the breakdown observations have Entity 0, which
    # is never an entity handle; the explicit skip only documents it.
    ("trace-sees-breakdowns", T, "\tcase engine.ObsResource, engine.ObsResourceDown, engine.ObsResourceUp, engine.ObsBreakdownPending:\n", "\tcase engine.ObsResource:\n", ["./internal/simulation/..."], "equivalent"),
    # version (D-047)
    ("old-engine-version", D, 'const Version = "des-go-0.3.0"', 'const Version = "des-go-0.2.0"', ["./internal/simulation/des/", "./internal/runs/..."]),
    # runner and API mapping
    ("stall-retried", RN, "\t\treturn failure(store.SimulationStalled, se.Reason(), false)\n", "\t\treturn failure(store.SimulationStalled, se.Reason(), true)\n", ["./internal/runs/..."]),
    ("stall-details-dropped", RN, "\tcase errors.As(err, &se):\n", "\tcase errors.As(err, &se) && false:\n", ["./internal/runs/..."]),
    ("stall-internal-error", RS, "\tcase f.Code == store.SimulationDeadlock || f.Code == store.SimulationStalled:\n", "\tcase f.Code == store.SimulationDeadlock:\n", ["./internal/runs/..."]),
    # D-057 risk and the numbers an effect may quote
    ("risk-breakdowns-off", RK, "\t\tif ok && !sameBreakdowns(r.Breakdowns, c.Breakdowns) {\n", "\t\tif false {\n", ["./internal/ai/review/"]),
    ("risk-breakdowns-added-low", RK, "\tif a == nil || b == nil {\n\t\treturn a == b\n\t}\n", "\tif a == nil || b == nil {\n\t\treturn true\n\t}\n", ["./internal/ai/review/"]),
    ("risk-ignores-cv", RK, "same(a.CV, b.CV) && ", "", ["./internal/ai/review/"]),
    ("risk-ignores-repair", RK, " &&\n\t\tsameDistribution(a.TimeToRepair.Distribution, b.TimeToRepair.Distribution)\n", "\n", ["./internal/ai/review/"]),
    ("effects-without-resource-values", RV, "\t\tfor _, d := range ir.Resources[i].Distributions() {\n\t\t\tparams(d.Distribution)\n\t\t}\n", "", ["./internal/ai/review/"]),
    # SimPy reference: its interpretation must catch a wrong one
    # D-074 (a) option B since review round 1: the reference's repair gives
    # the unit to a waiting failure; option A (release it to the jobs) must
    # be caught (G-F3, G-F9).
    ("simpy-repair-releases-to-jobs", SP, "            if waiting_failures[r]:\n                # D-074 (a) B: the repaired unit goes to the first waiting\n",
     "            if False:\n                # D-074 (a) B: the repaired unit goes to the first waiting\n", "py"),
    ("simpy-no-finish-first", SP, "                if waiting_failures.get(r):\n", "                if False:\n", "py"),
    ("simpy-failure-preempts-nothing-waits", SP, "            if len(res.users) < res.capacity:\n", "            if True:\n", "py"),
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
