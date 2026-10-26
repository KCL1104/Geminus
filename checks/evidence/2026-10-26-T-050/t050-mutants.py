#!/usr/bin/env python3
"""Mutants of T-050's code: batch processes and changeovers in the kernel
(internal/simulation/engine: readiness, BATCH_TIMEOUT and its staleness,
the batch request, the grant and its unblocks, the end of a batch, D-050
B1 and the names in B1/B2; the unit choice, the changeover time and its
stream, the completion after changeover and service, the units of
failures), Compile's own check of D-055's limits, the validator's
BATCH_INVALID and SETUP_INVALID, the Result members, the trace, the dataset
binding's path of a changeover, the D-057 risk, and the SimPy batch and changeover reference's interpretation.

A Go mutant is one exact search/replace applied with `go test -overlay` (the
working tree is never modified); KILLED means the listed packages' tests
failed. A Python mutant is the same replace on a temporary copy of
scripts/simpy_batch_reference.py, run against bin/simpy-reference-go.json
(written by `make simulation-reference-test` first); KILLED means it exited
non-zero. A mutant whose pattern is not found exactly once is BAD-MUTANT;
one marked equivalent must survive (EQUIVALENT), with the reason next to
it.

    python3 checks/evidence/2026-10-26-T-050/t050-mutants.py [repo] [name...]
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
RNG = "internal/simulation/engine/rng.go"
C = "internal/simulation/engine/compile.go"
M = "internal/results/metrics/aggregate.go"
T = "internal/simulation/trace/trace.go"
VB = "internal/model/validate/batches.go"
RK = "internal/ai/review/risk.go"
DS = "internal/storage/datasets/datasets.go"
SP = "scripts/simpy_batch_reference.py"
ENG = ["./internal/simulation/engine/"]
# Every test package that builds internal/results/metrics or
# internal/simulation/trace (go list -test -deps): the engine's tests never
# build them, so running them for a Result or trace mutant only takes time.
SIM = ["./internal/simulation/des/", "./internal/simulation/trace/", "./internal/results/..."]
VAL = ["./internal/model/...", "./internal/simulation/engine/"]
AI = ["./internal/ai/..."]
MUT = [
    # D-054 readiness: max_size waiting, or min_size once the oldest has
    # waited max_wait (at the limit's tick, D-096 (c)); min = max never
    # starts on the limit (D-096 (b)).
    ("ready-below-max", K, "count >= b.max || (b.timed", "count >= b.max-1 || (b.timed", ENG),
    ("ready-ignores-min", K, "(b.timed && count >= b.min && k.r.now-front.enter >= b.wait)", "(b.timed && k.r.now-front.enter >= b.wait)", ENG),
    ("ready-strictly-after-limit", K, "(b.timed && count >= b.min && k.r.now-front.enter >= b.wait)", "(b.timed && count >= b.min && k.r.now-front.enter > b.wait)", ENG),
    # Equivalent (run 1: SURVIVED): untimed is min = max, where count >=
    # min_size is count >= max_size, which the first clause takes already;
    # D-096 (b) is about scheduling timeouts, not readiness.
    ("ready-untimed-on-limit", K, "(b.timed && count >= b.min && k.r.now-front.enter >= b.wait)", "((b.timed || true) && count >= b.min && k.r.now-front.enter >= b.wait)", ENG, "equivalent"),
    ("two-ungranted-requests", K, "\tif k.pend[n] != nil {\n\t\treturn nil\n\t}\n", "", ENG),
    # BATCH_TIMEOUT: only when min < max, not when already expired, stale
    # ones ignored (D-096 (b), (c)).
    ("timeouts-for-min-equals-max", K, "\t\tif ok && b.timed && front.enter+b.wait > k.r.now {\n", "\t\tif ok && front.enter+b.wait > k.r.now {\n", ENG),
    # (run 1: SURVIVED; the hand-derived G-BT15, max_wait 0, kills it now:
    # a 0-delay BATCH_TIMEOUT at each entry adds events and sequences)
    ("expired-limit-scheduled", K, "\t\tif ok && b.timed && front.enter+b.wait > k.r.now {\n", "\t\tif ok && b.timed && front.enter+b.wait >= k.r.now {\n", ENG),
    # (run 1: SURVIVED: a stale timeout's check is a no-op but for the
    # order of two batch processes' requests at one tick; the hand-derived
    # G-BT16 kills it now)
    ("stale-timeout-not-ignored", K, "\tif gen != k.tgen[n] {\n\t\treturn nil\n\t}\n", "", ENG),
    # Only a ready batch asks for a dispatch (D-096 (e)).
    ("dispatch-on-every-batch-entry", K, "\t\treturn k.batchCheck(process)\n", "\t\tif err := k.batchCheck(process); err != nil {\n\t\t\treturn err\n\t\t}\n\t\treturn k.needDispatch()\n", ENG),
    # The grant: min(waiting, max_size) members, BatchStart and the members'
    # ServiceStarts carry the number, one draw, as many unblocks as places,
    # then a check again (several units, one dispatch: G-BT3).
    ("batch-one-short", K, "\tsize := min(q.len(), p.nodes[n].batch.max)\n", "\tsize := min(q.len(), max(p.nodes[n].batch.max-1, 1))\n", ENG),
    ("member-start-no-number", K, "\t\tk.observe(Observation{Kind: ObsServiceStart, Entity: m.entity, Node: n, Batch: run.number})\n",
     "\t\tk.observe(Observation{Kind: ObsServiceStart, Entity: m.entity, Node: n})\n", ENG),
    ("grant-unblocks-one", K, "\t\tfor range size {\n\t\t\tif k.blocked[n].len() == 0 {", "\t\tfor range 1 {\n\t\t\tif k.blocked[n].len() == 0 {", ENG),
    ("grant-no-recheck", K, "\t\t}\n\t}\n\treturn k.batchCheck(n)\n}", "\t\t}\n\t}\n\treturn nil\n}", ENG),
    # (expected to be killed only by the 0.5.0 locks: a second draw from the
    # same stream has the same distribution, so the hand-derived goldens,
    # all constant, and the SimPy reference cannot tell)
    ("batch-second-draw", K, "\td := p.nodes[n].service.draw(k.r.stream(n, PurposeService))\n\tif err := k.r.schedule(d, PriorityServiceComplete, EventServiceComplete, n, k.batchHandle); err != nil {",
     "\td := p.nodes[n].service.draw(k.r.stream(n, PurposeService))\n\td = p.nodes[n].service.draw(k.r.stream(n, PurposeService))\n\tif err := k.r.schedule(d, PriorityServiceComplete, EventServiceComplete, n, k.batchHandle); err != nil {", ENG),
    # The end of a batch: routes in member order, the bundle kept while a
    # member is blocked and released with the last one (D-054).
    # (run 2: SURVIVED: the destinations follow the draws whatever the
    # resolution order, only the visits went to the wrong member; the
    # visit limit of TestBatchVisitsChargedToTheirMember kills it now)
    ("routes-in-reverse", K, "\tfor _, e := range run.members {\n\t\tto, err := k.resolve(e, n)\n", "\tfor _, e := range slices.Backward(run.members) {\n\t\tto, err := k.resolve(e, n)\n", ENG),
    ("members-take-routes-in-reverse", K, "\tfor i, mr := range routes {\n\t\te := run.members[i]\n", "\tfor i, mr := range routes {\n\t\te := run.members[len(routes)-1-i]\n", ENG),
    ("release-with-members-blocked", K, "\treqs := k.r.p.nodes[n].requires\n\tif !blocked {\n", "\treqs := k.r.p.nodes[n].requires\n\tif true || !blocked {\n", ENG),
    ("release-at-first-unblock", K, "\t\tif run.blocked--; run.blocked == 0 {\n", "\t\tif run.blocked--; run.blocked <= 1 {\n", ENG),
    # D-050 B1 waits for the BATCH_TIMEOUTs to come; B1 and B2 name a batch
    # request by its oldest member (D-096 (g)).
    ("deadlock-ignores-timeouts", K, "\tif k.inService > 0 || k.inDelay > 0 || k.timeouts > 0 || (k.nBlocked == 0 && k.held == 0) {\n",
     "\tif k.inService > 0 || k.inDelay > 0 || (k.nBlocked == 0 && k.held == 0) {\n", ENG),
    ("failure-names-no-member", K, "func (k *kernel) entityOf(req *request) uint64 {\n\tif req.batch {\n", "func (k *kernel) entityOf(req *request) uint64 {\n\tif false && req.batch {\n", ENG),
    # D-055 changeovers: the unit set up for the type first, else the first
    # free; the transition, else the default, else 0 (observed, D-097 (a));
    # the first piece without initial changes nothing; the state follows;
    # the unit's own stream; completion after changeover and service.
    ("unit-choice-ignores-state", K, "\tu := slices.IndexFunc(units, func(x unit) bool { return !x.busy && !x.down && x.state == product })\n", "\tu := -1\n", ENG),
    ("unit-choice-ignores-down", K, "\t\tu = slices.IndexFunc(units, func(x unit) bool { return !x.busy && !x.down })\n", "\t\tu = slices.IndexFunc(units, func(x unit) bool { return !x.busy })\n", ENG),
    ("transition-ignored", K, "\ts := cs.trans[[2]int{from, product}]\n", "\ts := cs.trans[[2]int{product, from}]\n", ENG),
    ("default-ignored", K, "\tif s == nil {\n\t\ts = cs.def\n\t}\n", "", ENG),
    ("zero-changeover-not-observed", K, "\tk.observe(Observation{Kind: ObsSetup, Entity: e, Node: n, Resource: r, At: u, Value: ticks})\n",
     "\tif ticks > 0 {\n\t\tk.observe(Observation{Kind: ObsSetup, Entity: e, Node: n, Resource: r, At: u, Value: ticks})\n\t}\n", ENG),
    ("first-piece-changes-over", K, "\tif from < 0 || from == product {\n", "\tif from == product {\n", ENG),
    ("state-not-updated", K, "\tunits[u].state = product\n", "", ENG),
    ("one-stream-for-all-units", K, "PurposeSetupPrefix+strconv.Itoa(u))", "PurposeSetupPrefix+strconv.Itoa(0*u))", ENG),
    ("completion-ignores-changeover", K, "min(setup+d, beyondCap)", "min(0*setup+d, beyondCap)", ENG),
    # Units of failures (D-097 (c)): at once the idle unit of least index;
    # a release frees the entity's unit; a downtime keeps the state.
    ("failure-takes-the-clock-unit", K, "\t\t\twhich = slices.IndexFunc(k.units[i], func(x unit) bool { return !x.busy && !x.down })\n", "\t\t\twhich = u\n", ENG),
    ("release-frees-unit-0", K, "\t\t\tfreed = u\n", "\t\t\tfreed = 0 * u\n", ENG),
    ("downtime-resets-the-state", K, "\t\tk.units[i][which].down = true\n", "\t\tk.units[i][which].down, k.units[i][which].state = true, -1\n", ENG),
    # Equivalent: a failure only waits when no unit is idle (D-074 (a) B),
    # and every unit that becomes available goes to the waiting failures
    # first, so at a release or a repair with a waiting failure the unit
    # just freed is the only idle one: the idle unit of least index is it.
    ("release-hands-over-least-idle", K, "\t\t\t\tif err := k.goDown(q.resource, u, freed); err != nil {\n", "\t\t\t\tif err := k.goDown(q.resource, u, -1+0*freed); err != nil {\n", ENG, "equivalent"),
    ("repair-hands-over-least-idle", K, "\t\treturn k.goDown(i, w, repaired)\n", "\t\treturn k.goDown(i, w, -1+0*repaired)\n", ENG, "equivalent"),
    # Compile checks D-055's limits again (TestCompileChecksSetups).
    ("compile-skips-setup-limits", C, "\t\t\t\tif q.units != 1 || c.setupRes >= 0 || c.batch != nil {\n", "\t\t\t\tif false && (q.units != 1 || c.setupRes >= 0 || c.batch != nil) {\n", ENG),
    # Result members (D-058, D-096 (j), D-097 (f))
    ("batches-outside-window", M, "\t\tif c.inWindow(o.Tick) {\n\t\t\tc.batchesStarted[o.Node]++\n", "\t\tif true {\n\t\t\tc.batchesStarted[o.Node]++\n", SIM),
    # (run 2: SURVIVED: G-BT4's second blocked member moves on in the tick
    # it is blocked; G-BT17, two members blocked together for 3 minutes,
    # kills it now)
    ("batch-bundle-per-member", M, "\t\tif (sign > 0 && left != 1) || (sign < 0 && left != 0) {\n", "\t\tif false {\n", SIM),
    ("setups-started-outside-window", M, "\t\tif c.inWindow(o.Tick) {\n\t\t\tc.setupsStarted[r]++\n", "\t\tif true {\n\t\t\tc.setupsStarted[r]++\n", SIM),
    ("setup-complete-ignores-end", M, "\t\tif o.Tick >= c.w && o.Tick+o.Value <= c.h {\n", "\t\tif o.Tick >= c.w {\n", SIM),
    ("setup-end-not-applied", M, "\t\tc.changing[e.r]--\n", "", SIM),
    ("processing-ignores-changeover-on-shift", M, "\tc.processing[i].set(min(u-k-z, s), t, c.w, c.h)", "\tc.processing[i].set(min(u-k-0*z, s), t, c.w, c.h)", SIM),
    ("processing-ignores-changeover", M, "\t\t\tst.ScheduledProcessingTicks -= z\n", "", SIM),
    # Trace (SIMULATION_SPEC §9)
    ("trace-no-batch-id", T, "\t\tev.BatchID = engine.FormatBatchID(r.index, node, o.Batch)\n", "\t\t_ = node\n", SIM),
    ("trace-no-resource-id", T, "\t\tev.ResourceID = r.p.ResourceID(o.Resource)\n", "", SIM),
    # Validator (BATCH_INVALID, SETUP_INVALID)
    ("batch-capacity-unchecked", VB, "\t\tif c := capacity[n.ID]; c > 0 && c < b.MaxSize {\n", "\t\tif c := capacity[n.ID]; false && c < b.MaxSize {\n", VAL),
    ("batch-wait-required-at-equal-sizes", VB, "\t\tcase b.MinSize < b.MaxSize && b.MaxWait == nil:\n", "\t\tcase b.MinSize <= b.MaxSize && b.MaxWait == nil:\n", VAL),
    ("setup-self-transition-allowed", VB, "\t\t\tcase tr.From == tr.To:\n", "\t\t\tcase false && tr.From == tr.To:\n", VAL),
    ("setup-two-units-allowed", VB, "\t\t\tcase q.Units != 1:\n", "\t\t\tcase false && q.Units != 1:\n", VAL),
    # The binding stage reports an unbound empirical changeover at its own
    # path (setups/default is the distribution itself; TestSetupDatasets).
    ("dataset-path-of-a-member", DS, '"/"+d.Path+"/samples_ref"', '"/"+d.Member+"/distribution/samples_ref"', ["./internal/storage/datasets/"]),
    # D-057 risk
    ("batch-risk-ignored", RK, "\t\tif ok && c.Type == n.Type && !sameBatch(n.Batch, c.Batch) {\n", "\t\tif false && ok && c.Type == n.Type && !sameBatch(n.Batch, c.Batch) {\n", AI),
    ("setup-risk-ignored", RK, "\t\tif ok && !sameSetups(r.Setups, c.Setups) {\n", "\t\tif false && ok && !sameSetups(r.Setups, c.Setups) {\n", AI),
    ("setup-risk-ignores-initial", RK, "\tif a.Initial != b.Initial || (a.Default == nil)", "\tif (a.Default == nil)", AI),
    # --- Review round 1 (H-003, checks/evidence/2026-10-26-T-050-r1/): the
    # reviewer's mutants that no test, or only a 0.5.0 lock, killed
    # (reviewer/review-mutants.py), the reading of D-096 (g)'s old text
    # (M1), and the reviewer's equivalent one (I2). Each is killed now by a
    # hand golden or vector added in round 1, not by a lock.
    # M1: B2's since of a batch request taken as the ready tick (the old
    # D-096 (g) text) instead of the first tick in B2's state (D-074 (c));
    # G-BT13 cannot tell (both 20), G-BT18 can (20 vs 100).
    ("m1-b2-since-ready-tick", K, "\t\tif f.stalled < 0 {\n\t\t\tf.stalled, f.deadline = k.r.now, k.deadlineOf(f.process)\n\t\t}\n",
     "\t\tif f.stalled < 0 {\n\t\t\tf.stalled, f.deadline = k.r.now, k.deadlineOf(f.process)\n\t\t\tif f.batch {\n\t\t\t\tf.stalled = f.entered\n\t\t\t}\n\t\t}\n", ENG),
    # M2: D-096 (g) B, a BATCH_TIMEOUT after the horizon not counted for
    # B1. ADR-026 D-050 B1 (ACCEPTED) counts it, as the kernel does (A);
    # G-BT19 runs to the horizon under A and is ErrDeadlock at 7 under B.
    ("r-b1-ignores-timeouts-after-horizon", K, "\t\t\tk.timeouts++\n\t\t\tif err := k.r.schedule(front.enter+b.wait-k.r.now",
     "\t\t\tif front.enter+b.wait <= k.r.p.horizon {\n\t\t\t\tk.timeouts++\n\t\t\t}\n\t\t\tif err := k.r.schedule(front.enter+b.wait-k.r.now", ENG),
    # M3: D-055's stream name renamed (TestSetupStreamsPerUnit derives its
    # expectation from the constant; TestSetupStreamVectors, against the
    # Python port, does not).
    ("r-setup-purpose-renamed", RNG, 'const PurposeSetupPrefix = "setup/"', 'const PurposeSetupPrefix = "setups/"', ENG),
    # M3: the changeover ends after the last observation never applied at
    # the run's end (G-SU12: setup_ticks 13 instead of 12 minutes).
    ("r-setup-ends-not-applied-at-finish", M, "\t\tc.endSetups(c.h) // a later end is cut at H by the integrals\n", "", SIM),
    # M4: a complete changeover must start strictly after W (G-SU11: B1's at
    # W = 20 is complete).
    ("r-setup-complete-strict-w", M, "if o.Tick >= c.w && o.Tick+o.Value <= c.h {", "if o.Tick > c.w && o.Tick+o.Value <= c.h {", SIM),
    # L1: a batch's end asks for a dispatch with no bundle released
    # (G-BT21: 10 events instead of 8, b2 in DISPATCH seq 8 instead of 7).
    ("r-batch-end-dispatch-without-resources", K, "\t\tif err := k.release(reqs, 0); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif len(reqs) > 0 {\n",
     "\t\tif err := k.release(reqs, 0); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif true {\n", ENG),
    # L2: B1's since of a batch request (Queued) taken as its oldest
    # member's entry instead of the tick the request was made (G-BT22: 1
    # minute instead of 2).
    ("r-b1-since-oldest-entry", K, "req := &request{process: n, seq: k.reqSeq, entered: k.r.now, stalled: -1, deadline: -1, batch: true}",
     "req := &request{process: n, seq: k.reqSeq, entered: front.enter, stalled: -1, deadline: -1, batch: true}", ENG),
    # Equivalent (the reviewer's I2): a changeover's end applied only after
    # an observation of a later tick. integral.set adds level x (t - since)
    # and moves since, and the late end is applied at its own tick before
    # any observation of a later tick, so the only reordering is between
    # zero-length steps at that tick; at the run's end an end exactly at H
    # left unapplied covers [H, H], zero length.
    ("r-setup-end-applied-late", M, "for len(c.setupEnds) > 0 && c.setupEnds[0].tick <= t {", "for len(c.setupEnds) > 0 && c.setupEnds[0].tick < t {", SIM, "equivalent"),
    # The SimPy reference's interpretation
    ("py-unit-choice-ignores-state", SP, "        return match[0] if match else free[0]\n", "        return free[0]\n", "py"),
    ("py-ready-strictly-after-limit", SP, "env.now - bbuf[p][0][1] >= wait):", "env.now - bbuf[p][0][1] > wait):", "py"),
    ("py-no-changeover-time", SP, "                    setup = m.draw(d, rng) if d else 0\n", "                    setup = 0\n", "py"),
]
EXTRA = {}


def run_go(i, tmp, f, old, new, pkgs, extra=()):
    edits = {}
    for path, o, n in [(f, old, new), *extra]:
        edits[path] = edits.get(path, (REPO / path).read_text()).replace(o, n)
    replace = {}
    for k, (path, src) in enumerate(edits.items()):
        m = pathlib.Path(tmp) / f"m{i}-{k}.go"
        m.write_text(src)
        replace[str(REPO / path)] = str(m)
    ov = pathlib.Path(tmp) / f"m{i}.json"
    ov.write_text(json.dumps({"Replace": replace}))
    # -failfast: a package stops at its first failing test (a killed
    # mutant needs only one); a survivor runs every test.
    # WITHOUT_LOCKS=1 leaves out the regression locks (the digests of the
    # engine's own output, not oracles), to show which other test kills a
    # mutant the locks would catch first.
    skip = ["-skip", "Digests"] if os.environ.get("WITHOUT_LOCKS") == "1" else []
    p = subprocess.run(["go", "test", "-count=1", "-failfast", "-timeout", "900s", *skip, "-overlay", str(ov), *pkgs],
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
            counts = [src.count(old)] + [(REPO / p).read_text().count(o) for p, o, _ in EXTRA.get(name, [])]
            if counts != [1] * len(counts):
                results.append((name, f"BAD-MUTANT (patterns found {counts} times)"))
                print("\t".join(results[-1]), flush=True)
                continue
            if pkgs == "py":
                code, o = run_py(i, tmp, f, old, new)
                first = next((l.strip() for l in o.splitlines() if l.startswith(("FAIL", "Traceback")) or "!=" in l), "")
            else:
                code, o = run_go(i, tmp, f, old, new, pkgs, EXTRA.get(name, []))
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
