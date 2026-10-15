#!/usr/bin/env python3
"""Go mutants for the T-020 engine foundation, after its review fixes.

Each mutant is one exact search/replace in one source file, applied through
`go test -overlay` (the working tree is never modified). A mutant is KILLED
when `go test ./internal/simulation/...` fails, or, for the mutants marked
fma, when scripts/check_no_fma.sh fails with the overlay in GOFLAGS.

    python3 checks/evidence/2026-10-15-T-020-review/engine-mutants.py [repo]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

REPO = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[3])
E = "internal/simulation/engine/"
S, EV, C, R, RNG = E + "sample.go", E + "events.go", E + "compile.go", E + "replication.go", E + "rng.go"

# (name, file, old, new[, "fma"])
MUT = [
    # --- the original 30 (checks/evidence/2026-10-15-T-020/mutation.log), on the current code
    ("seq-before-priority", EV, "\tif a.Priority != b.Priority {\n\t\treturn a.Priority < b.Priority\n\t}\n\treturn a.Seq < b.Seq", "\tif a.Seq != b.Seq {\n\t\treturn a.Seq < b.Seq\n\t}\n\treturn a.Priority < b.Priority"),
    ("tick-ignored", EV, "\tif a.Tick != b.Tick {\n\t\treturn a.Tick < b.Tick\n\t}\n", ""),
    ("uniform-no-half", RNG, "(float64(r.Next()>>12) + 0.5) / (1 << 52)", "float64(r.Next()>>12) / (1 << 52)"),
    ("exp-log-u", S, "-s.mean * ln(1-u)", "-s.mean * ln(u)"),
    ("exp-allows-zero", S, "\tif s.kind == model.DistExponential {\n\t\treturn max(t, 1)\n\t}", ""),
    ("no-subtick-clamp", S, "\tcase t == 0 && d > 0:\n\t\treturn 1\n", ""),
    ("no-saturation", S, "\tcase t > float64(model.MaxDurationTicks):\n\t\treturn beyondCap\n", ""),
    ("tri-swapped", S, "math.Sqrt(u*width*(s.mode-s.min))", "math.Sqrt(u*width*(s.max-s.mode))"),
    ("tri-wrong-branch", S, "if u < (s.mode-s.min)/width {", "if u > (s.mode-s.min)/width {"),
    ("constant-draws", S, "\t\ts.fixed = true\n\tcase model.DistExponential:", "\tcase model.DistExponential:"),
    ("degenerate-tri-draws", S, "\t\tif s.min == s.max {\n\t\t\ts.fixed = true", "\t\tif false && s.min == s.max {\n\t\t\ts.fixed = true"),
    ("empirical-ceil", S, "i := int(u * float64(len(s.values)))", "i := int(u*float64(len(s.values)) + 0.999999)"),
    ("nodes-unsorted", C, "\tslices.SortFunc(nodes, byID(func(n model.Node) string { return n.ID }))\n", ""),
    ("flows-unsorted", C, "\tslices.SortFunc(flows, byID(func(f model.Flow) string { return f.ID }))\n", ""),
    ("zero-flow-kept", C, "if f.From != n.ID || f.Routing.Probability <= 0 {", "if f.From != n.ID {"),
    ("queue-no-buffer", C, "\t\t\tp.nodes[i].buffer = p.nodes[i].routes[0].to\n", ""),
    ("no-bindings-check", C, "\tif issues := validate.Bindings(ir, zeroRefs); hasError(issues) {", "\tif issues := validate.Bindings(ir, zeroRefs); false && hasError(issues) {"),
    ("no-revalidation", C, "\tif !res.Valid() {\n\t\treturn nil, &InputError{Issues: prefix(\"/model\", res.Issues)}\n\t}", "\tif !res.Valid() && false {\n\t\treturn nil, &InputError{Issues: prefix(\"/model\", res.Issues)}\n\t}"),
    ("warmup-equal-ok", C, "if !(norm.WarmupTicks < norm.HorizonTicks) {", "if !(norm.WarmupTicks <= norm.HorizonTicks) {"),
    # Equivalent since the review fixes: checkManifest already requires
    # Datasets[ref].Binding to equal the manifest binding for ref, so this
    # guard in resolveDatasets is defence in depth that no input reaches.
    ("other-ref-ok", C, "\t\t\tcase ds.Binding.LogicalRef != ref:", "\t\t\tcase false:", "equivalent"),
    ("count-mismatch-ok", C, "len(ds.Samples) == 0 || len(ds.Samples) != ds.Binding.SampleCount", "len(ds.Samples) == 0"),
    ("limits-unchecked", C, "\t\t{\"/limits\", \"simulation.schema.json#/$defs/ExecutionLimits\", limits},\n", ""),
    ("no-max-entities", R, "\tif n.maxEntities > 0 && r.arrived[src] >= uint64(n.maxEntities) {", "\tif false {"),
    ("horizon-exclusive", R, "\tif at > r.p.horizon {", "\tif at >= r.p.horizon {"),
    ("no-cancel-check", R, "\t\tcase <-done:\n\t\t\treturn ctx.Err()\n", "\t\tcase <-done:\n"),
    ("no-budget", R, "\t\tif err := b.spend(); err != nil {\n\t\t\treturn err\n\t\t}\n", ""),
    ("sources-reversed", R, "\tfor _, src := range r.p.sources {", "\tfor _, src := range slices.Backward(r.p.sources) {"),
    ("stream-ignores-purpose", R, "\tk := streamKey{node, purpose}", "\tk := streamKey{node, PurposeArrival}"),
    ("unknown-event-skipped", R, "\t\tif r.onEvent == nil {\n\t\t\treturn fmt.Errorf(\"%w: no handler for event kind %d\", ErrInternal, e.Kind)\n\t\t}", "\t\tif r.onEvent == nil {\n\t\t\treturn nil\n\t\t}"),
    ("first-arrival-at-zero", R, "\treturn r.schedule(n.arrival.draw(r.stream(src, PurposeArrival)), PriorityImmediate, EventArrival, src, 0)", "\tdelay := n.arrival.draw(r.stream(src, PurposeArrival))\n\tif r.arrived[src] == 0 {\n\t\tdelay = 0\n\t}\n\treturn r.schedule(delay, PriorityImmediate, EventArrival, src, 0)"),
    # --- the review's surviving mutants
    ("budget-allows-max-1", R, "\tif b.used.Add(1) > b.max {", "\tif b.used.Add(1) >= b.max {"),
    ("dataset-in-model-unit", C, "\t\t\t\tt, err := model.Ticks(s, ds.Binding.Unit)", "\t\t\t\tt, err := model.Ticks(s, model.TimeUnit(ir.TimeUnit))"),
    ("route-before-next-arrival", R, "\tif err := r.scheduleArrival(src); err != nil {\n\t\treturn err\n\t}\n\tif r.onArrival != nil {\n\t\treturn r.onArrival(r, id)\n\t}\n\treturn nil", "\tif r.onArrival != nil {\n\t\tif err := r.onArrival(r, id); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\treturn r.scheduleArrival(src)"),
    ("requires-unsorted", C, "\t\t\tslices.SortFunc(c.requires, func(a, b requirement) int { return cmp.Compare(a.resource, b.resource) })\n", ""),
    ("extra-draw-after-max", R, "\tif n.maxEntities > 0 && r.arrived[src] >= uint64(n.maxEntities) {\n\t\treturn nil\n\t}\n\treturn r.schedule(n.arrival.draw(r.stream(src, PurposeArrival)), PriorityImmediate, EventArrival, src, 0)", "\td := n.arrival.draw(r.stream(src, PurposeArrival))\n\tif n.maxEntities > 0 && r.arrived[src] >= uint64(n.maxEntities) {\n\t\treturn nil\n\t}\n\treturn r.schedule(d, PriorityImmediate, EventArrival, src, 0)"),
    ("log1p", S, "-s.mean * ln(1-u)", "-s.mean * math.Log1p(-u)"),
    ("split-sqrt-low", S, "math.Sqrt(u*width*(s.mode-s.min))", "math.Sqrt(u*width)*math.Sqrt(s.mode-s.min)"),
    ("split-sqrt-high", S, "math.Sqrt((1-u)*width*(s.max-s.mode))", "math.Sqrt((1-u)*width)*math.Sqrt(s.max-s.mode)"),
    # --- the review fixes
    # On amd64 math.Log equals the pinned ln, so only the listing check sees it
    # (TestNoPlatformTranscendentals reads the files on disk, not the overlay).
    ("ln-is-math-Log", S, "func ln(x float64) float64 {\n", "func ln(x float64) float64 { return math.Log(x) }\n\nfunc lnPinned(x float64) float64 {\n", "fma"),
    ("ln-fusable", S, "\tr := t1 + t2\n", "\tr := t1 + s2*t2\n", "fma"),
    ("ln-constant", S, "lnL4     = math.Float64frombits(0x3fcc71c51d8e78af)", "lnL4     = math.Float64frombits(0x3fcc71c51d8e78b0)"),
    ("ln-boundary", S, "\tif f1 < sqrtHalf {", "\tif f1 <= sqrtHalf {"),
    ("triangular-unclamped", S, "\treturn min(max(v, s.min), s.max)", "\treturn v"),
    ("dataset-issues-unsorted", C, "\tempirical, zeroRefs, issues := resolveDatasets(ir, in.Datasets)\n\tif len(issues) > 0 {\n\t\treturn nil, &InputError{Issues: validate.Sort(issues)}", "\tempirical, zeroRefs, issues := resolveDatasets(ir, in.Datasets)\n\tif len(issues) > 0 {\n\t\treturn nil, &InputError{Issues: issues}"),
    ("manifest-unchecked", C, "\tif issues, err := checkManifest(ir, in.Manifest, in.Datasets); err != nil || len(issues) > 0 {", "\tif issues, err := checkManifest(ir, in.Manifest, in.Datasets); false && (err != nil || len(issues) > 0) {"),
    ("manifest-schema-skipped", C, "\t\tissues = append(issues, prefix(at, found)...)\n", "\t\t_ = found\n"),
    ("manifest-order-unchecked", C, "\t\tif i > 0 && b.LogicalRef <= manifest[i-1].LogicalRef {", "\t\tif false {"),
    ("manifest-binding-trusted", C, "\t\tcase ds.Binding != b:", "\t\tcase ds.Binding != b && false:"),
    ("manifest-extra-dataset-ok", C, "\t\tif !bound[ref] {", "\t\tif false {"),
    ("manifest-unbound-ref-ok", C, "\t\t\tif d.Distribution.Type == model.DistEmpirical && !bound[d.Distribution.SamplesRef] {", "\t\t\tif false {"),
    ("config-cap-generic-path", C, "\t\t\tissues = append(issues, model.NewIssue(validate.LimitExceeded, d.path, err.Error(), model.SeverityError))", "\t\t\tissues = append(issues, model.NewIssue(validate.LimitExceeded, \"/config\", err.Error(), model.SeverityError))"),
    ("config-nan-generic-path", C, "\t\t\tissues = append(issues, model.NewIssue(validate.IRSchemaInvalid, d.path, \"not a finite number\", model.SeverityError))", "\t\t\tissues = append(issues, model.NewIssue(validate.IRSchemaInvalid, \"/config\", \"not a finite number\", model.SeverityError))"),
]


def run(cmd, overlay):
    env = dict(os.environ, GOFLAGS=f"-overlay={overlay}")
    p = subprocess.run(cmd, cwd=REPO, capture_output=True, text=True, env=env)
    return p.returncode, p.stdout + p.stderr


def main() -> int:
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new, *flags) in enumerate(MUT):
            src = (REPO / f).read_text()
            if src.count(old) != 1:
                results.append((name, f"BAD-MUTANT (pattern found {src.count(old)} times)"))
                continue
            out = src.replace(old, new)
            if "slices.Backward" in new and '"slices"' not in out:
                out = out.replace("import (\n", 'import (\n\t"slices"\n', 1)
            m = pathlib.Path(tmp) / f"m{i}.go"
            m.write_text(out)
            ov = pathlib.Path(tmp) / f"m{i}.json"
            ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
            if "fma" in flags:
                code, o = run(["bash", "scripts/check_no_fma.sh"], ov)
                first = next((l.strip() for l in o.splitlines() if l.startswith("FAIL")), "")
            else:
                code, o = run(["go", "test", "-count=1", "-timeout", "300s", "./internal/simulation/..."], ov)
                first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:"))), "")
            if code == 0:
                status = "SURVIVED"
            elif "build failed" in o or "setup failed" in o or "[build failed]" in o:
                status = "BUILD-FAILED"
            else:
                status = "KILLED"
            if "equivalent" in flags:
                status = "EQUIVALENT" if status == "SURVIVED" else f"UNEXPECTED-{status}"
            results.append((name, f"{status} {first[:90]}"))
            print("\t".join(results[-1]), flush=True)
    killed = sum(1 for r in results if r[1].startswith("KILLED"))
    equivalent = sum(1 for r in results if r[1].startswith("EQUIVALENT"))
    print(f"{killed}/{len(results) - equivalent} killed, {equivalent} equivalent (documented above)")
    return 0 if killed + equivalent == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
