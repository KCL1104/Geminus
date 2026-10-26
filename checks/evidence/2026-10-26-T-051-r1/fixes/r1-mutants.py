"""Review r1 fixes (T-051 M1, L4): mutants run in a `git archive` copy of the
fix commit under the scratch directory, never in the worktree. Each mutant
replaces one text of one file (it must match exactly once) through
`go test -overlay` in the copy, then runs the listed packages.

The reviewer's 13 mutants (checks/T-051_REVIEW_r1.md, rv_mutants.py; the
starvation one in the reviewer's rewritten, buildable form) and the M1
mutants of this fix. A build failure is not a kill (run 1 had three M1 mutants that did not
build, an unused import; run 2 keeps validate used).

usage: python3 mutants.py <archive copy> <work dir> <log>"""
import json
import pathlib
import subprocess
import sys

REPO, SCRATCH, LOG = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), pathlib.Path(sys.argv[3])
SCRATCH.mkdir(parents=True, exist_ok=True)
EL = "internal/domainpack/electronicsline/"
PKG = ["./internal/domainpack/electronicsline/", "./internal/domainpack/conformance/"]

REVIEWER = [
    ("rv-featureless-idle-wrong", "L1", EL + "metrics.go",
     "return states{CT: ct, U: in, S: ct, A: ct, I: ct - in, US: in, PS: in}, \"\"",
     "return states{CT: ct, U: in, S: ct, A: ct, I: ct, US: in, PS: in}, \"\""),
    ("rv-featureless-processing-zero", "L1", EL + "metrics.go",
     "return states{CT: ct, U: in, S: ct, A: ct, I: ct - in, US: in, PS: in}, \"\"",
     "return states{CT: ct, U: in, S: ct, A: ct, I: ct - in, US: in, PS: 0}, \"\""),
    ("rv-line-balance-no-dedupe", "L4", EL + "metrics.go", "\t\tif !seen {\n", "\t\tif true {\n"),
    ("rv-blocking-first-test-only", "-", EL + "metrics.go", "\t\tfor _, i := range s.processes() {\n\t\t\tb := rep.Blocking",
     "\t\tfor _, i := range s.processes()[:1] {\n\t\t\tb := rep.Blocking"),
    ("rv-retest-any-primary", "L4", EL + "stations.go", "\t\t\tif st.first.kind == s.kind && st.first.primary == s.primary {",
     "\t\t\tif st.first.kind == s.kind {"),
    ("rv-missing-member-accepted", "L2", EL + "metrics.go", "\t\tcase p == nil:\n\t\t\treturn 0, false", "\t\tcase p == nil:\n\t\t\treturn 0, true"),
    ("rv-rty-without-tests-is-1", "L3", EL + "metrics.go", "\tif tests == 0 {\n\t\treturn missing(reasonTestResultNotIdentified)\n\t}",
     "\tif tests == 0 {\n\t\treturn ok(1)\n\t}"),
    ("rv-overtime-missing-accepted", "L2", EL + "metrics.go", "\t\tif s.OT, has = get(st.OvertimeTicks, true, 0); !has {",
     "\t\tif s.OT, has = get(st.OvertimeTicks, true, 0); false && !has {"),
    ("rv-fpy-last-pass-flow-only", "L3", EL + "stations.go", "\t\t\t\tr.pass += f.Routing.Probability", "\t\t\t\tr.pass = f.Routing.Probability"),
    ("rv-uph-scheduled-calendar-any-resource", "-", EL + "metrics.go",
     "\t\tif cal := c.l.ir.Resources[s.primary].CalendarID; cal != \"\" && !ids[cal] {",
     "\t\tif cal := c.l.ir.Resources[s.primary].CalendarID; cal != \"\" && !ids[cal] && false {"),
    ("rv-starved-own-ignored (rewritten)", "-", EL + "metrics.go", "\t\tcase !own[i] || len(n.Requires) != 1:",
     "\t\tcase (!own[i] && false) || len(n.Requires) != 1:"),
    ("rv-rule3-skip-retest-without-primary", "-", EL + "rules.go", "\t\tif !s.retest || s.primary < 0 {\n\t\t\tcontinue\n\t\t}\n\t\tfirsts",
     "\t\tif !s.retest {\n\t\t\tcontinue\n\t\t}\n\t\tfirsts"),
    ("rv-batch-fill-over-min", "-", EL + "metrics.go", "\t\treturn ok(m.v / float64(batch.MaxSize))", "\t\treturn ok(m.v / float64(batch.MinSize))"),
]

# The old rolledYield (one factor per first test, node order), as in d229225.
OLD_ROLLED = """func (c *computer) rolledYield() value {
	product, tests := 1.0, 0
	for _, s := range c.l.stations {
		if !s.first.kind.test {
			continue
		}
		tests++
		y := c.fpy(s)
		if y.reason != "" {
			return y
		}
		product *= y.v
	}
	if tests == 0 {
		return missing(reasonTestResultNotIdentified)
	}
	return ok(product)
}"""
FIXED_ROLLED_HEAD = "func (c *computer) rolledYield() value {\n\tproduct, tests := 1.0, 0\n\tfor i := range stationKinds {"


def fixed_rolled(src):
    start = src.index(FIXED_ROLLED_HEAD)
    end = src.index("\n}\n", start) + 2
    return src[start:end]


M1 = [
    # The bug itself: one factor per first test.
    ("m1-one-factor-per-first-test", "M1", EL + "metrics.go", None, OLD_ROLLED + "\n\nvar _ = validate.ProbabilityEpsilon"),
    # Exact comparison: 0.6 + 0.33 differs from 0.93 by one ulp.
    ("m1-exact-comparison", "M1", EL + "metrics.go", "\t\tcase hi-lo > validate.ProbabilityEpsilon:", "\t\tcase hi-lo > 0*validate.ProbabilityEpsilon:"),
    # A tolerance wider than the IR's: 1e-6 apart would pass.
    ("m1-wide-tolerance", "M1", EL + "metrics.go", "\t\tcase hi-lo > validate.ProbabilityEpsilon:", "\t\tcase hi-lo > 1e4*validate.ProbabilityEpsilon:"),
    # Different yields multiplied anyway (no unavailable).
    ("m1-differing-yields-accepted", "M1", EL + "metrics.go",
     "\t\tcase hi-lo > validate.ProbabilityEpsilon:\n\t\t\treturn missing(reasonParallelYieldsDiffer)",
     "\t\tcase hi-lo > validate.ProbabilityEpsilon:\n\t\t\t_ = reasonParallelYieldsDiffer"),
    # The type's yield from its last cell instead of its first.
    ("m1-type-yield-last-cell", "M1", EL + "metrics.go", "\t\t\tif cells == 0 {\n\t\t\t\tfirst, lo, hi = y.v, y.v, y.v\n\t\t\t}",
     "\t\t\tfirst = y.v\n\t\t\tif cells == 0 {\n\t\t\t\tlo, hi = y.v, y.v\n\t\t\t}"),
    # A cell without a yield skipped instead of leaving its type without one.
    ("m1-missing-cell-skipped", "M1", EL + "metrics.go", "\t\t\ty := c.fpy(s)\n\t\t\tif y.reason != \"\" {\n\t\t\t\treturn y\n\t\t\t}",
     "\t\t\ty := c.fpy(s)\n\t\t\tif y.reason != \"\" {\n\t\t\t\tcontinue\n\t\t\t}"),
    # A type without first tests counted as a factor of 1 (harmless for the
    # product, but then a line without tests would have 1): tests++ first.
    ("m1-empty-type-counted", "M1", EL + "metrics.go", "\t\tcase cells == 0:\n\t\t\tcontinue\n", "\t\tcase cells == 0:\n\t\t\ttests++\n\t\t\tcontinue\n"),
]

rows = []
for name, finding, rel, old, new in REVIEWER + M1:
    src = (REPO / rel).read_text(encoding="utf-8")
    if old is None:
        old = fixed_rolled(src)
    if src.count(old) != 1:
        rows.append((name, finding, rel, f"BAD-MUTANT ({src.count(old)} matches)", ""))
        print("\t".join(rows[-1]), flush=True)
        continue
    safe = name.split(" ")[0]
    mutant = SCRATCH / f"{safe}.go"
    mutant.write_text(src.replace(old, new), encoding="utf-8")
    overlay = SCRATCH / f"{safe}.json"
    overlay.write_text(json.dumps({"Replace": {str(REPO / rel): str(mutant)}}))
    p = subprocess.run(["go", "test", "-count=1", "-overlay", str(overlay), *PKG], cwd=REPO, capture_output=True, text=True)
    out = p.stdout + p.stderr
    (SCRATCH / f"{safe}.log").write_text(out, encoding="utf-8")
    if "[build failed]" in out or "[setup failed]" in out:
        result = "BUILD-ERROR"
    elif p.returncode == 0:
        result = "SURVIVED"
    elif "--- FAIL" in out:
        result = "KILLED"
    else:
        result = f"ERROR {p.returncode}"
    failing = sorted({line.split()[2] for line in out.splitlines() if line.startswith("--- FAIL: ") and "/" not in line.split()[2]})
    rows.append((name, finding, rel, result, ", ".join(failing)))
    print("\t".join(rows[-1]), flush=True)

with LOG.open("w", encoding="utf-8") as f:
    f.write("# mutants in a git archive copy of the fix commit (go test -count=1 -overlay ... " + " ".join(PKG) + ")\n")
    f.write("| mutant | finding | file | result | failing tests |\n|---|---|---|---|---|\n")
    for r in rows:
        f.write("| " + " | ".join(r) + " |\n")
    killed = sum(1 for r in rows if r[3] == "KILLED")
    f.write(f"\n{killed}/{len(rows)} killed\n")
print(f"{sum(1 for r in rows if r[3] == 'KILLED')}/{len(rows)} killed")
