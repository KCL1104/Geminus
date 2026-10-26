"""T-052 review round 1: the reviewer's mutants (scratchpad rv/mut.py, the same
changes) rerun on the fixed code, and the mutants of this round's fixes. Each
changes one behaviour through go test -overlay (the tree is never changed)
and runs the named tests of the listed packages; the failing tests are
printed, so it shows which test kills each mutant.

  python3 r1-mutants.py <repo root> <scratch dir>

The generator's two golden tests (the pinned SHA-256 and the committed
files being the output) catch every change of a byte; the "non-golden"
runs leave them out, to show what the other tests catch (review I3).
"""
import json
import pathlib
import subprocess
import sys

REPO, SCRATCH = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
SCRATCH.mkdir(parents=True, exist_ok=True)
GEN = "scripts/gen-electronics-line-data/"
EL = "internal/domainpack/electronicsline/"
OB = "internal/results/observed/"
# The reviewer's non-golden tests of the generator, and the same with this
# round's two new ones.
NONGOLDEN_RV = ("^(TestLogShape|TestEquipmentIsNeverDoubleBooked|TestRoutesFollowTheLine|TestChangeoversShowAsGaps|"
                "TestTheLogIsTheEngineReplication|TestNegativeFixtures|TestGroundTruth|TestMoreWeeksExtendTheSameLog)$")
NONGOLDEN_R1 = NONGOLDEN_RV[:-2] + "|TestObserverFailsWhenItsBookingIsNotTheEngines|TestRecordOrderAcrossWeeks)$"
ALL = "."

HIGHEST = (GEN + "observer.go", "\tfor u := range r.units {\n\t\tif !r.units[u].busy && !r.units[u].down {",
           "\tfor i := range r.units {\n\t\tu := len(r.units) - 1 - i\n\t\tif !r.units[u].busy && !r.units[u].down {")
ROUNDED = (GEN + "generate.go", ".Truncate(time.Second).Format(time.RFC3339)", ".Round(time.Second).Format(time.RFC3339)")
M = [
    # The reviewer's five survivors.
    ("gen-highest-idle-unit (reviewer's non-golden tests)", *HIGHEST, ["./" + GEN], NONGOLDEN_RV),
    ("gen-highest-idle-unit (non-golden tests with r1's)", *HIGHEST, ["./" + GEN], NONGOLDEN_R1),
    ("gen-highest-idle-unit (all tests)", *HIGHEST, ["./" + GEN], ALL),
    ("gen-rounded-seconds (reviewer's non-golden tests)", *ROUNDED, ["./" + GEN], NONGOLDEN_RV),
    ("gen-rounded-seconds (non-golden tests with r1's)", *ROUNDED, ["./" + GEN], NONGOLDEN_R1),
    ("gen-rounded-seconds (all tests)", *ROUNDED, ["./" + GEN], ALL),
    ("gen-no-self-check", GEN + "observer.go", "\t\to.check(r)\n", "\t\t_ = r\n", ["./" + GEN], ALL),
    ("observed-output-by-start", EL + "observed.go", "\t\t\tcase ev.end >= from && ev.end < to:", "\t\t\tcase ev.start >= from && ev.start < to:",
     ["./" + EL], "Observed"),
    ("observed-fpy-zero-boards", EL + "observed.go", "\t\t\tcase len(events) == 0 || len(order) == 0:", "\t\t\tcase len(events) == 0:",
     ["./" + EL], "Observed"),
    # The reviewer's seven killed mutants, rerun as the reviewer ran them.
    ("gen-batch-member-own-unit", GEN + "observer.go", "\t\t\tif u, ok = b.units[rq.Resource]; !ok {", "\t\t\tif u, ok = b.units[rq.Resource]; !ok || true {",
     ["./" + GEN], NONGOLDEN_RV),
    ("gen-waiting-breakdown-dropped", GEN + "observer.go", "\t\to.goDown(r, u, d.clock, d.tick)\n", "\t\t_ = d\n", ["./" + GEN], NONGOLDEN_RV),
    ("gen-downtime-shift-empty", GEN + "observer.go", "\t\tshift: o.shifts.at(tick), seq: len(o.records), node: -1}",
     "\t\tshift: \"\", seq: len(o.records), node: -1}", ["./" + GEN], NONGOLDEN_RV),
    ("gen-downtime-station-is-resource", GEN + "observer.go", "\td := &record{serial: id, recordType: \"downtime\", station: r.station,",
     "\td := &record{serial: id, recordType: \"downtime\", station: r.id,", ["./" + GEN], NONGOLDEN_RV),
    ("observed-cycle-unknown-mean", EL + "observed.go", "\tcase d.Type == model.DistConstant && d.Value != nil:\n\t\tv = *d.Value\n",
     "\tcase d.Type == model.DistConstant && d.Value != nil:\n\t\tv = *d.Value + 1\n", ["./" + EL], "Observed"),
    ("rules-end-only-for-operations", EL + "events.go", "\tif e.EndTimestamp == \"\" && (op || down) {", "\tif e.EndTimestamp == \"\" && op {",
     ["./" + EL], "EventRules"),
    ("validation-check-before-canonical", "internal/import/validation/validation.go", "\t\tif check != nil {\n\t\t\tfor _, w := range check(e.CanonicalEvent) {",
     "\t\tif check != nil && row.Index > 0 {\n\t\t\tfor _, w := range check(e.CanonicalEvent) {", ["./internal/import/validation/"], ALL),
    # This round's fixes.
    ("r1-uncovered-unit-counted (M1)", EL + "observed.go", "\t\tcase !covered(starts, from, to, unit):", "\t\tcase false && !covered(starts, from, to, unit):",
     ["./" + EL], ALL),
    ("r1-one-covered-unit-enough (M1)", EL + "observed.go", "\treturn int64(len(seen)) == (to-from)/unit", "\treturn len(seen) > 0",
     ["./" + EL], ALL),
    ("r1-coverage-by-operations-only (M1)", EL + "observed.go", "\t\tstarts = append(starts, ev.start)\n",
     "\t\tif e.EventType == EventOperation {\n\t\t\tstarts = append(starts, ev.start)\n\t\t}\n", ["./" + EL], ALL),
    ("r1-label-is-hold-out (label)", OB + "observed.go", "\tModelGenerated Sample = \"model_generated\"", "\tModelGenerated Sample = \"hold_out\"",
     ["./" + OB, "./" + EL], ALL),
    ("r1-record-order-32-bit (I5)", GEN + "generate.go", "\t\treturn cmp.Compare(a.start, b.start)", "\t\treturn int(int32(a.start - b.start))",
     ["./" + GEN], ALL),
]
res = []
for name, rel, old, new, pkgs, run in M:
    src = (REPO / rel).read_text(encoding="utf-8")
    if src.count(old) != 1:
        res.append((name, f"BAD-MUTANT ({src.count(old)})", ""))
        print("\t".join(res[-1]), flush=True)
        continue
    slug = name.split(" ")[0]
    mutant = SCRATCH / f"{slug}.go"
    mutant.write_text(src.replace(old, new), encoding="utf-8")
    overlay = SCRATCH / f"{slug}.json"
    overlay.write_text(json.dumps({"Replace": {str(REPO / rel): str(mutant)}}))
    p = subprocess.run(["go", "test", "-count=1", "-overlay", str(overlay), "-run", run, *pkgs], cwd=REPO, capture_output=True, text=True)
    out = p.stdout + p.stderr
    if "[build failed]" in out or "[setup failed]" in out:
        res.append((name, "BUILD-ERROR", out[-400:].replace("\n", " ")))
    else:
        fails = sorted({l.split()[2] for l in out.splitlines() if l.startswith("--- FAIL")})
        res.append((name, "SURVIVED" if p.returncode == 0 else ("KILLED" if fails else f"ERROR {p.returncode}"), " ".join(fails)))
    print("\t".join(res[-1]), flush=True)
killed = sum(1 for r in res if r[1] == "KILLED")
print(f"{killed}/{len(res)} killed")
