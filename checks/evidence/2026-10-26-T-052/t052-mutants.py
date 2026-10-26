"""T-052 mutation harness: each mutant changes one behaviour of the generator,
the pack's event rules, the import validation, the validator, the worker's
wiring or the D-060 comparison; the tests of the listed packages must fail on
it (go test -overlay: the tree itself is never changed).

  python3 t052-mutants.py <repo root> <scratch dir>   # every mutant but those needing PostgreSQL
  T052_DB=1 python3 t052-mutants.py ...                # also the validator's wiring, against the database
                                                        # (GEMINUS_TEST_DATABASE_URL, a migrated dt_test_* database)
"""
import json
import os
import pathlib
import subprocess
import sys

REPO, SCRATCH = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
SCRATCH.mkdir(parents=True, exist_ok=True)
GEN = "scripts/gen-electronics-line-data/"
GP = ["./" + GEN]
EL = "internal/domainpack/electronicsline/"
ELP = ["./" + EL]
VAL = "internal/import/validation/validation.go"
VP = ["./internal/import/validation/"]
VR = "internal/import/validator/validator.go"
VRP = ["./internal/import/validator/"]
OB = "internal/results/observed/observed.go"
OBP = ["./internal/results/observed/", "./" + EL]
M = [
    # The generator: what a row says must be what the engine did.
    ("gen-test-failure-ignored", GEN + "observer.go", '\t\t\te.op.result = "fail"\n', "", GP),
    ("gen-scrap-ignored", GEN + "observer.go", '\t\t\te.op.result = "fail" // scrapped at debug\n', "", GP),
    ("gen-changeover-in-the-step", GEN + "observer.go", "\tstart := ob.Tick + e.setup //", "\tstart := ob.Tick //", GP),
    ("gen-waiting-breakdown-dropped", GEN + "observer.go", "\t\to.goDown(r, u, d.clock, d.tick)\n", "\t\t_ = d\n", GP),
    ("gen-shift-boundary", GEN + "observer.go", "\t\tif m >= s.start && m < s.end {", "\t\tif m > s.start && m <= s.end {", GP),
    ("gen-batch-member-own-unit", GEN + "observer.go", "\t\t\tif u, ok = b.units[rq.Resource]; !ok {", "\t\t\tif u, ok = b.units[rq.Resource]; !ok || true {", GP),
    ("gen-highest-idle-unit", GEN + "observer.go", "\tfor u := range r.units {\n\t\tif !r.units[u].busy && !r.units[u].down {",
     "\tfor i := range r.units {\n\t\tu := len(r.units) - 1 - i\n\t\tif !r.units[u].busy && !r.units[u].down {", GP),
    ("gen-rounded-seconds", GEN + "generate.go", ".Truncate(time.Second).Format(time.RFC3339)", ".Round(time.Second).Format(time.RFC3339)", GP),
    # The pack's rules (D-059).
    ("rules-product-always-required", EL + "events.go", '\tcase e.Product == "" && op && v.multi:', '\tcase e.Product == "" && op:', ELP),
    ("rules-result-required-everywhere", EL + "events.go", '\t\tcase e.Outcome == "" && v.tests[e.Stage]:', '\t\tcase e.Outcome == "":', ELP),
    ("rules-step-ids-unknown", EL + "events.go", "\t\t\tv.stages[n.ID] = true\n", "\t\t\t_ = n\n", ELP),
    ("rules-downtime-reason-checked", EL + "events.go", "\tif op {\n\t\tswitch {\n\t\tcase e.Outcome", "\tif op || down {\n\t\tswitch {\n\t\tcase e.Outcome", ELP),
    ("rules-for-every-domain", EL + "events.go", "\tif ir == nil || ir.Domain != ID {", "\tif ir == nil {", ELP),
    # The import validation (D-072 (b)).
    ("validation-warnings-first", VAL, "\tout.Issues = append(out.Issues, warnings[:min(len(warnings), lim.MaxIssues-len(out.Issues))]...)",
     "\tout.Issues = append(warnings[:min(len(warnings), lim.MaxIssues)], out.Issues...)[:min(len(warnings)+len(out.Issues), lim.MaxIssues)]", VP),
    ("validation-warning-at-the-row", VAL, "\t\t\t\tcolumn = plan.Name(i)\n", "\t\t\t\t_ = i\n", VP),
    ("validation-any-issue-recorded", VAL, "\tif !issueCode.MatchString(e.Code) || n < 1", "\tif false && !issueCode.MatchString(e.Code) || n < 1", VP),
    ("validation-warnings-uncounted", VAL, "\t\t\t\tif out.Warnings++; len(warnings) < lim.MaxIssues {", "\t\t\t\tif len(warnings) < lim.MaxIssues {", VP),
    # The validator and the worker's wiring.
    ("validator-ignores-packs", VR, "\tif packs == nil {\n\t\treturn nil, nil", "\tif true {\n\t\treturn nil, nil", VRP),
    ("validator-unserved-domain-passes", VR, "\t\treturn nil, failure(store.InputInvalid, \"the import's model is of a domain this worker does not serve\", false)",
     "\t\treturn nil, nil", VRP),
    ("worker-without-packs", "cmd/worker/main.go", "Log: d.log, Packs: served})", "Log: d.log})", ["./cmd/worker/"]),
    # The comparison (D-060).
    ("compare-quantiles-from-21", OB, "\tif len(xs) >= QuantileReplications {", "\tif len(xs) > QuantileReplications {", OBP),
    ("compare-error-sign", OB, "\tabs := simulated - o", "\tabs := o - simulated", OBP),
    ("compare-range-open", OB, "\twithin := lo <= o && o <= hi", "\twithin := lo < o && o < hi", OBP),
    ("compare-zero-divides", OB, "\tif o == 0 {\n\t\tr := ReasonObservedZero", "\tif false {\n\t\tr := ReasonObservedZero", OBP),
    ("observed-phase-unchecked", EL + "observed.go", " || mod(from, unit) != mod(w0, unit)", "", ELP),
    ("observed-offsets-unchecked", EL + "observed.go", " || len(offsets) > 1:", ":", ELP),
    ("observed-fpy-last-test", EL + "observed.go", "!seen || ev.start < prev.start {", "!seen || ev.start >= prev.start {", ELP),
    ("observed-fpy-carry-over-boards", EL + "observed.go", " || !entered[ev.e.CaseID] {", " {", ELP),
    ("observed-triangular-mean", EL + "observed.go", "(*d.Min + *d.Mode + *d.Max) / 3", "(*d.Min + *d.Max) / 2", ELP),
]
DB = [
    # Only the closed loop through the job sees that the validator hands the check on.
    ("validator-drops-the-check", VR, "validation.ValidateWith(ctx, r, format, plan, lim, check)", "validation.ValidateWith(ctx, r, format, plan, lim, nil)",
     ["./internal/api/"], ["-run", "^TestSyntheticStationLogRules$"]),
]
res = []
cases = [(n, f, o, w, p, []) for n, f, o, w, p in M]
if os.environ.get("T052_DB") == "1":
    cases += DB
for name, rel, old, new, pkgs, extra in cases:
    src = (REPO / rel).read_text(encoding="utf-8")
    if src.count(old) != 1:
        res.append((name, "BAD-MUTANT"))
        continue
    mutant = SCRATCH / f"{name}.go"
    mutant.write_text(src.replace(old, new), encoding="utf-8")
    overlay = SCRATCH / f"{name}.json"
    overlay.write_text(json.dumps({"Replace": {str(REPO / rel): str(mutant)}}))
    env = dict(os.environ)
    if extra:
        env["GEMINUS_REQUIRE_DB_TESTS"] = "1"
    p = subprocess.run(["go", "test", "-count=1", "-overlay", str(overlay), *extra, *pkgs], cwd=REPO, capture_output=True, text=True, env=env)
    out = p.stdout + p.stderr
    # A mutant that does not compile proves nothing about the tests.
    if "[build failed]" in out or "[setup failed]" in out:
        res.append((name, "BUILD-ERROR"))
    else:
        res.append((name, "SURVIVED" if p.returncode == 0 else ("KILLED" if "--- FAIL" in out else f"ERROR {p.returncode}")))
for r in res:
    print("\t".join(r))
killed = sum(1 for r in res if r[1] == "KILLED")
print(f"{killed}/{len(res)} killed")
sys.exit(0 if killed == len(res) else 1)
