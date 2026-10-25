#!/usr/bin/env python3
"""T-048 review round 2 (L2, I1): mutants of kernel.repair run against the
hand-derived golden tests alone, without the des-go-0.3.0 regression locks,
and against the locks alone.

Round 2 found two kernel.repair mutants that only the observation lock
(kernel-observation-digests-0.3.0.json) caught:

  repair-gives-the-unit-and-wakes-too  after handing the repaired unit to a
      waiting failure, also wakes the held front: a needless DISPATCH (one
      more sequence number, one more event against the budget).
  repair-pending-before-clock          serves the waiting failure before
      drawing the repaired clock's next TTF and scheduling FAILURE(u)
      (SIMULATION_SPEC §3 says the other way round).

G-F10 and G-F9R10 (breakdown-goldens.json, round 2) are meant to catch them
in TestBreakdownGoldens. The third mutant is review I1, kept as a residual
risk: the TTR of the failure a repair serves drawn from the repaired
clock's stream; every golden has a constant TTR, so no golden can see it
and only the locks (the stochastic B-S cases) do.

A mutant is one exact search/replace applied with `go test -overlay` (the
working tree is never modified). "golden" runs only the hand-derived
golden tests (engine TestBreakdownGoldens, des TestBreakdownGoldenResults);
"locks" runs only the two des-go-0.3.0 locks (engine
TestBreakdownObservationDigests, des TestGoldenDigests030). KILLED means a
test of that set failed. The unmutated code must pass both sets (baseline).

"result" runs des TestBreakdownGoldenResults alone (part of "golden"):
the derivations of G-F9R10 and G-F10 say the two L2 mutants change no
Result member, only the waiting failures, the sequence numbers and the
event count, so both must survive it.

    python3 checks/evidence/2026-10-25-T-048-r2/t048-r2-mutants.py [repo] [golden|locks|result]...
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

args = sys.argv[1:]
REPO = pathlib.Path(args.pop(0)) if args and pathlib.Path(args[0]).is_dir() else pathlib.Path(__file__).resolve().parents[3]
SETS = args or ["golden", "locks"]
K = "internal/simulation/engine/kernel.go"
ENG, DES = "./internal/simulation/engine/", "./internal/simulation/des/"
TESTS = {
    "golden": [(ENG, "^TestBreakdownGoldens$"), (DES, "^TestBreakdownGoldenResults$")],
    "locks": [(ENG, "^TestBreakdownObservationDigests$"), (DES, "^TestGoldenDigests030$")],
    # the Result layer alone: the derivations say both L2 mutants leave
    # every Result member as it was, so they must survive this set
    "result": [(DES, "^TestBreakdownGoldenResults$")],
}
MUT = [
    ("repair-gives-the-unit-and-wakes-too",
     "\t\tk.pending[i] = k.pending[i][1:]\n\t\treturn k.goDown(i, w)\n",
     "\t\tk.pending[i] = k.pending[i][1:]\n\t\tif err := k.goDown(i, w); err != nil {\n\t\t\treturn err\n\t\t}\n"
     "\t\tif f := k.queues[i].front(); f != nil && f.held {\n\t\t\tf.held = false\n\t\t\tk.held--\n\t\t\theap.Push(&k.cands, f)\n"
     "\t\t\treturn k.needDispatch()\n\t\t}\n\t\treturn nil\n"),
    ("repair-pending-before-clock",
     "\tif err := k.scheduleFailure(i, u); err != nil {\n\t\treturn err\n\t}\n\tif len(k.pending[i]) > 0 {\n"
     "\t\tw := k.pending[i][0]\n\t\tk.pending[i] = k.pending[i][1:]\n\t\treturn k.goDown(i, w)\n\t}\n",
     "\tif len(k.pending[i]) > 0 {\n\t\tw := k.pending[i][0]\n\t\tk.pending[i] = k.pending[i][1:]\n"
     "\t\tif err := k.goDown(i, w); err != nil {\n\t\t\treturn err\n\t\t}\n\t\treturn k.scheduleFailure(i, u)\n\t}\n"
     "\tif err := k.scheduleFailure(i, u); err != nil {\n\t\treturn err\n\t}\n"),
    ("repair-ttr-from-repaired-clock-stream (I1, residual)",
     "\t\tk.pending[i] = k.pending[i][1:]\n\t\treturn k.goDown(i, w)\n\t}\n",
     "\t\tk.pending[i] = k.pending[i][1:]\n\t\tk.down[i]++\n"
     "\t\tk.observe(Observation{Kind: ObsResourceDown, Resource: i, At: w, Value: int64(k.down[i])})\n"
     "\t\treturn k.r.scheduleClock(k.r.p.resources[i].ttr.draw(k.streams(i, u).ttr), EventRepair, i, w)\n\t}\n"),
]


def run(overlay, tests):
    """Runs each (package, -run) of tests; returns (killed, first failing line)."""
    for pkg, pattern in tests:
        cmd = ["go", "test", "-count=1", "-run", pattern, pkg]
        if overlay:
            cmd[2:2] = ["-overlay", str(overlay)]
        p = subprocess.run(cmd, cwd=REPO, capture_output=True, text=True, env=dict(os.environ))
        out = p.stdout + p.stderr
        if "build failed" in out or "setup failed" in out:
            return None, "BUILD-FAILED " + out.strip().splitlines()[-1][:110]
        if p.returncode != 0:
            # every failing (sub)test and assertion, so the log shows which
            # golden caught the mutant
            lines = [l.strip() for l in out.splitlines()
                     if l.strip().startswith(("--- FAIL", "panic:")) or "breakdown_test.go" in l]
            return True, f"{pkg} {pattern}: " + " | ".join(lines)
        if "no tests to run" in out:
            return None, f"{pkg} {pattern}: no tests to run"
    return False, ""


def main() -> int:
    bad = 0
    with tempfile.TemporaryDirectory() as tmp:
        for s in SETS:
            killed, why = run(None, TESTS[s])
            print(f"baseline\t{s}\t{'PASS' if killed is False else 'FAIL ' + why}", flush=True)
            bad += killed is not False
        src = (REPO / K).read_text()
        for i, (name, old, new) in enumerate(MUT):
            if src.count(old) != 1:
                print(f"{name}\tBAD-MUTANT (pattern found {src.count(old)} times)", flush=True)
                bad += 1
                continue
            m = pathlib.Path(tmp) / f"m{i}.go"
            m.write_text(src.replace(old, new))
            ov = pathlib.Path(tmp) / f"m{i}.json"
            ov.write_text(json.dumps({"Replace": {str(REPO / K): str(m)}}))
            for s in SETS:
                killed, why = run(ov, TESTS[s])
                status = {True: "KILLED", False: "SURVIVED", None: "ERROR"}[killed]
                print(f"{name}\t{s}\t{status} {why}", flush=True)
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
