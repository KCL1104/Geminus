import json, pathlib, subprocess, sys
REPO = pathlib.Path("/home/user/Geminus"); HERE = pathlib.Path(__file__).parent
P = "internal/domainpack/softwaredelivery/pack.go"; T = "internal/domainpack/softwaredelivery/template.go"
D = "internal/domainpack/domainpack.go"; L = "internal/domainpack/layout.go"
M = [
 ("validate-accepts-any-domain", P, "\tif ir.Domain != ID {", "\tif false {"),
 ("dora-from-failure-count", P, "out[m.key] = simulation.MetricSummary{Status: simulation.Unavailable,", "out[m.key] = simulation.MetricSummary{Status: simulation.Available,"),
 ("dora-without-reason", P, "Unit: m.unit, Reason: &reason}", "Unit: m.unit, Reason: nil}"),
 ("template-keeps-model-id", T, "\tir.ModelID, ir.Name = newModelID, name", "\tir.Name = name"),
 ("template-unvalidated", T, "\tif !res.Valid() {", "\tif false {"),
 ("catalog-shared", P, "\t\tn.ResourceTypes = append([]string(nil), n.ResourceTypes...)\n", ""),
 ("registry-duplicates", D, "\t\tif _, dup := r.packs[id]; dup {", "\t\tif false {"),
 ("layout-overwrites-existing", L, "\t\tif p, ok := existing[n.ID]; ok {", "\t\tif p, ok := existing[n.ID]; ok && false {"),
 ("layout-orphans-at-origin", L, "\t\t\tl = deepest + 1", "\t\t\tl = 0"),
 ("inference-fake-success", P, "\treturn nil, fmt.Errorf(\"%w: event inference for %s arrives with T-027\", domainpack.ErrUnavailable, ID)", "\treturn &domainpack.InferenceResult{}, nil"),
]
res = []
for name, rel, old, new in M:
    src = (REPO / rel).read_text()
    if src.count(old) != 1: res.append((name, "BAD-MUTANT")); continue
    m = HERE / f"{name}.go"; m.write_text(src.replace(old, new))
    ov = HERE / f"{name}.json"; ov.write_text(json.dumps({"Replace": {str(REPO / rel): str(m)}}))
    p = subprocess.run(["go", "test", "-count=1", "-overlay", str(ov), "./internal/domainpack/..."], cwd=REPO, capture_output=True, text=True)
    res.append((name, "SURVIVED" if p.returncode == 0 else "KILLED"))
for r in res: print("\t".join(r))
k = sum(1 for r in res if r[1] == "KILLED"); print(f"{k}/{len(res)} killed"); sys.exit(0 if k == len(res) else 1)
