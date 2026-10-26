"""T-051 mutation harness: each mutant changes one behaviour of the pack, the
engine hook or the conformance suite; the tests of the listed packages must
fail on it (go test -overlay, the tree itself is never changed).

  python3 t051-mutants.py <repo root> <scratch dir>   # every mutant but those needing PostgreSQL
  T051_DB=1 python3 t051-mutants.py ...                # also the runner wiring, against the database
                                                        # (GEMINUS_TEST_DATABASE_URL, a migrated dt_test_* database)
"""
import json
import os
import pathlib
import subprocess
import sys

REPO, SCRATCH = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
SCRATCH.mkdir(parents=True, exist_ok=True)
EL = "internal/domainpack/electronicsline/"
PKG = ["./internal/domainpack/..."]
M = [
    # rules.go: ELECTRONICS_LINE §3.4
    ("rule1-two-of-the-role-pass", EL + "rules.go", "\t\tif s.matches != 1 {", "\t\tif s.matches == 0 {", PKG),
    ("rule3-cascades", EL + "rules.go", "\t\tcase withPrimary > 0 && !shared:", "\t\tcase !shared:", PKG),
    ("rule3-no-first-test-accepted", EL + "rules.go", "\t\tcase firsts == 0:", "\t\tcase false:", PKG),
    ("rule2-two-fail-flows-accepted", EL + "rules.go", "\t\tif r.fails != 1 {", "\t\tif r.fails == 0 {", PKG),
    # stations.go: §3.3 attributes
    ("result-of-need-not-route", EL + "stations.go", "\t\tif l.station(p.of) == nil || !l.routes(p.of, p.index) {", "\t\tif l.station(p.of) == nil {", PKG),
    ("missing-pass-accepted", EL + "stations.go", "\t\t\t\tif !present || !ok || (pass != passFirst && pass != passRetest) {",
     "\t\t\t\tif present && (!ok || (pass != passFirst && pass != passRetest)) {", PKG),
    ("failure-flow-counted-as-pass", EL + "stations.go", "\t\t\tif to, ok := l.nodes[f.To]; ok && ir.Nodes[to].Type == model.NodeFailure {",
     "\t\t\tif to, ok := l.nodes[f.To]; ok && ir.Nodes[to].Type == model.NodeSink {", PKG),
    # metrics.go: §5
    ("uph-over-the-horizon", EL + "metrics.go", "\t\treturn ok(float64(rep.CompletedCount*ticksPerHour) / float64(c.t))",
     "\t\treturn ok(float64(rep.CompletedCount*ticksPerHour) / float64(c.h))", PKG),
    ("uph-scheduled-over-the-window", EL + "metrics.go", "\t\treturn ok(float64(rep.CompletedCount*ticksPerHour) / float64(onShift))",
     "\t\t_ = onShift\n\t\treturn ok(float64(rep.CompletedCount*ticksPerHour) / float64(c.t))", PKG),
    ("line-calendar-from-zero", EL + "metrics.go", "\t\ton := p.OnTicks(c.h) - p.OnTicks(c.w)", "\t\ton := p.OnTicks(c.h)", PKG),
    ("processing-keeps-changeovers", EL + "metrics.go", "ok((st.U - st.K - st.Z) / st.CT)", "ok((st.U - st.K) / st.CT)", PKG),
    ("off-shift-keeps-down-time", EL + "metrics.go", "ok((st.CT - st.U - st.D - st.I) / st.CT)", "ok((st.CT - st.U - st.I) / st.CT)", PKG),
    ("starved-with-a-bundle", EL + "metrics.go", "\t\tcase !own[i] || len(n.Requires) != 1:", "\t\tcase !own[i]:", PKG),
    ("starved-in-a-batch", EL + "metrics.go", "\t\tcase n.Batch != nil && reason == \"\":", "\t\tcase false:", PKG),
    ("availability-over-capacity", EL + "metrics.go", "\t\treturn ok(st.A / st.S)", "\t\treturn ok(st.A / st.CT)", PKG),
    ("scheduled-utilization-without-calendar", EL + "metrics.go", "\tif res == nil || res.CalendarID != \"\" {", "\tif true {", PKG),
    ("line-balance-sum-not-mean", EL + "metrics.go", "\treturn ok(sum / float64(len(resources)) / most)", "\treturn ok(sum / most)", PKG),
    ("a-replication-without-a-value-counts-0", EL + "metrics.go", "\t\t\tif v.reason != \"\" {", "\t\t\tif v.reason != \"\" && i == 0 {", PKG),
    ("fpy-with-two-fail-flows", EL + "metrics.go", "\tif s.result == nil || s.result.fails != 1 {", "\tif s.result == nil {", PKG),
    ("blocking-in-ticks", EL + "metrics.go", "\t\treturn ok(float64(ticks) / float64(blocks) / ticksPerMinute)", "\t\treturn ok(float64(ticks) / float64(blocks))", PKG),
    ("declared-metric-invented", EL + "metrics.go", "\tput(\"line_wip\", \"line_wip\", static(missing(reasonWIPNotDeclared)))",
     "\tput(\"line_wip\", \"line_wip\", static(ok(0)))", PKG),
    # template.go and the template's labelling (rule 4)
    ("created-model-not-synthetic", EL + "template.go", "\t\t\"synthetic\":   true,\n", "", PKG),
    ("template-unvalidated", EL + "template.go", "\tif !res.Valid() {", "\tif false {", PKG),
    # the engine hook (D-058)
    ("domain-written-when-empty", "internal/simulation/des/des.go", "\t\tif len(mm) > 0 {", "\t\tif mm != nil {", ["./internal/simulation/des/"]),
    ("hook-gets-the-callers-ir", "internal/simulation/des/des.go", "\treturn f(&cp, res), nil", "\treturn f(&ir, res), nil", ["./internal/simulation/des/"]),
    ("hook-after-the-digest", "internal/simulation/des/des.go",
     "\tif e.ModelMetrics != nil {\n\t\tmm, err := modelMetrics(e.ModelMetrics, in.Model, res)",
     "\tif e.ModelMetrics != nil && false {\n\t\tmm, err := modelMetrics(e.ModelMetrics, in.Model, res)", ["./internal/simulation/des/", "./internal/domainpack/electronicsline/"]),
    # the conformance suite's model-metrics step
    ("conformance-skips-engine-agreement", "internal/domainpack/conformance/conformance.go",
     "\tif encoded(got.Metrics.Domain) != encoded(direct) {", "\tif false {", ["./internal/domainpack/conformance/"]),
    ("conformance-allows-ir-changes", "internal/domainpack/conformance/conformance.go",
     "\tif encoded(&ir) != irBefore {", "\tif false && encoded(&ir) != irBefore {", ["./internal/domainpack/conformance/"]),
    # the deployment's packs and the contract
    ("pack-not-served", "internal/packs/packs.go", "\treturn domainpack.NewRegistry(electronicsline.Pack{}, softwaredelivery.Pack{})",
     "\t_ = electronicsline.Pack{}\n\treturn domainpack.NewRegistry(softwaredelivery.Pack{})", ["./internal/packs/"]),
    ("review-without-a-sentence", "internal/ai/review/clean.go",
     "\t\"STATION_RESOURCE_INVALID\":   {\"a station process needs exactly one resource of its station's role\"},\n", "", ["./internal/ai/review/"]),
]
DB = [
    # Only the closed loop through the worker sees the runner's wiring.
    ("runner-without-model-metrics", "internal/runs/runner/runner.go", "\t\te.ModelMetrics = mp.ComputeModelMetrics // metrics.domain (D-058)",
     "\t\t_ = mp", ["./internal/api/"], ["-run", "^TestElectronicsLineTemplateClosedLoop$"]),
]
res = []
cases = [(n, f, o, w, p, []) for n, f, o, w, p in M]
if os.environ.get("T051_DB") == "1":
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
