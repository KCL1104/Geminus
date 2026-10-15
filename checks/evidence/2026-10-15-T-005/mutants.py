#!/usr/bin/env python3
"""Mutation check for T-005: each mutant must make its package's tests fail.

Uses `go test -overlay` so the repository files are never modified.
"""
import json, os, subprocess, sys, pathlib

REPO = pathlib.Path("/home/user/Geminus")
OUT = pathlib.Path(__file__).parent

H = "internal/model/hashing/hashing.go"
T = "internal/model/ticks.go"
V = "internal/model/validate/ir.go"
D = "internal/storage/datasets/datasets.go"

MUTANTS = [
    # hashing
    ("no-ijson-profile", H, "\tif err := profile(data); err != nil {\n\t\treturn nil, err\n\t}\n\tout, err := jcs.Transform(data)", "\tout, err := jcs.Transform(data)", "./internal/model/hashing/"),
    ("identity-canonical", H, "\treturn out, nil\n}\n\n// profile", "\t_ = out\n\treturn data, nil\n}\n\n// profile", "./internal/model/hashing/"),
    ("sum-no-prefix", H, "\treturn Prefix + hex.EncodeToString(h[:])", "\treturn hex.EncodeToString(h[:])", "./internal/model/hashing/"),
    ("patch-any-json", H, "\tif c[0] != '[' {", "\tif false && c[0] != '[' {", "./internal/model/hashing/"),
    ("manifest-unsorted", H, "\tslices.SortStableFunc(out, func(a, b model.DatasetBinding) int { return strings.Compare(a.LogicalRef, b.LogicalRef) })", "\tslices.SortStableFunc(out, func(a, b model.DatasetBinding) int { return 0 * strings.Compare(a.LogicalRef, b.LogicalRef) })", "./internal/model/hashing/"),
    # Equivalent within the 365-day domain: floor(x+0.5) differs from math.Round only at
    # x = 0.49999999999999994, where the positive-sub-tick clamp yields 1 either way.

    ("manifest-dups", H, "\t\tif out[i].LogicalRef == out[i-1].LogicalRef {", "\t\tif false && out[i].LogicalRef == out[i-1].LogicalRef {", "./internal/model/hashing/"),
    ("config-raw-durations", H, "\t\tHorizonTicks: horizon, WarmupTicks: warmup,", "\t\tHorizonTicks: int64(cfg.Horizon.Value), WarmupTicks: int64(cfg.Warmup.Value) + 0*warmup + 0*horizon,", "./internal/model/hashing/"),
    ("config-no-seed-check", H, "\tif cfg.Seed < 0 || cfg.Seed > MaxSafeInteger {", "\tif false {", "./internal/model/hashing/"),
    ("result-keeps-digest", H, "\tdelete(repro, \"result_digest\")", "", "./internal/model/hashing/"),
    ("result-includes-trace", H, "var resultMembers = []string{\"engine_version\",", "var resultMembers = []string{\"trace\", \"engine_version\",", "./internal/model/hashing/"),
    ("content-hash-canonical", H, "func ContentHash(raw []byte) string { return Sum(raw) }", "func ContentHash(raw []byte) string { h, _ := Of(raw); return h }", "./internal/model/hashing/"),
    ("leak-syntax-error", H, "\treturn fmt.Errorf(\"%w: %s at byte %d\", ErrInvalid, what, se.ByteOffset)", "\treturn fmt.Errorf(\"%w: %s %v\", ErrInvalid, what, err)", "./internal/model/hashing/"),
    ("leak-pointer", H, "\treturn fmt.Errorf(\"%w: %s at byte %d\", ErrInvalid, what, se.ByteOffset)", "\treturn fmt.Errorf(\"%w: %s at byte %d (%q)\", ErrInvalid, what, se.ByteOffset, string(se.JSONPointer))", "./internal/model/hashing/"),
    ("leak-jcs-error", H, "\t\treturn nil, fmt.Errorf(\"%w: a number or string has no RFC 8785 form\", ErrInvalid)", "\t\treturn nil, fmt.Errorf(\"%w: %v\", ErrInvalid, err)", "./internal/model/hashing/"),
    # ticks
    ("ticks-half-even", T, "\tt := math.Round(value * float64(ms))", "\tt := math.RoundToEven(value * float64(ms))", "./internal/model/"),
    ("ticks-no-clamp", T, "\tif t == 0 && value > 0 {", "\tif false {", "./internal/model/"),
    ("ticks-cap-ge", T, "\tif t > float64(MaxDurationTicks) {", "\tif t >= float64(MaxDurationTicks) {", "./internal/model/"),
    ("ticks-truncate", T, "\tt := math.Round(value * float64(ms))", "\tt := math.Trunc(value * float64(ms))", "./internal/model/"),
    ("ticks-floor-plus-half(equivalent)", T, "\tt := math.Round(value * float64(ms))", "\tt := math.Floor(value*float64(ms) + 0.5)", "./internal/model/"),
    # validate binding stage
    ("empirical-never-zero", V, "\t\treturn zeroRefs[d.SamplesRef]", "\t\treturn false", "./internal/model/validate/"),
    ("bindings-no-arrival", V, "\t\tif n.Arrival != nil && n.Arrival.Distribution.Type == model.DistEmpirical && zeroRefs[n.Arrival.Distribution.SamplesRef] {", "\t\tif false {", "./internal/model/validate/"),
    # datasets resolver
    ("no-hash-check", D, "\tif got := hashing.ContentHash(raw); got != b.ContentHash {", "\tif got := hashing.ContentHash(raw); false && got != b.ContentHash {", "./internal/storage/datasets/"),
    ("no-tenant-check", D, "\tcase snap.OrganizationID != org:", "\tcase false:", "./internal/storage/datasets/"),
    ("no-version-check", D, "\tcase snap.DatasetVersionID != b.DatasetVersionID:", "\tcase false:", "./internal/storage/datasets/"),
    ("no-ref-check", D, "\tcase snap.LogicalRef != b.LogicalRef:", "\tcase false:", "./internal/storage/datasets/"),
    ("no-unit-check", D, "\tcase snap.Unit != b.Unit:", "\tcase false:", "./internal/storage/datasets/"),
    ("no-count-check", D, "\tcase len(snap.Samples) != b.SampleCount:", "\tcase false:", "./internal/storage/datasets/"),
    ("no-sample-cap", D, "\t\tif err != nil {\n\t\t\treturn nil, []model.ValidationIssue{issue(validate.LimitExceeded,", "\t\tif false && err != nil {\n\t\t\treturn nil, []model.ValidationIssue{issue(validate.LimitExceeded,", "./internal/storage/datasets/"),
    ("never-zero", D, "\t\tout.zero = out.zero || t == 0", "\t\tout.zero = out.zero && t == 0", "./internal/storage/datasets/"),
    ("no-schema-shape", D, "\t\tfor _, f := range found {\n\t\t\tf.Path = bindingPath(i, \"\") + f.Path", "\t\tfor _, f := range found[:0] {\n\t\t\tf.Path = bindingPath(i, \"\") + f.Path", "./internal/storage/datasets/"),
    ("no-dup-binding", D, "\t\tif bound[b.LogicalRef] {", "\t\tif false {", "./internal/storage/datasets/"),
    ("no-coverage", D, "\t\t\tif d.Distribution.Type == model.DistEmpirical && !bound[d.Distribution.SamplesRef] {", "\t\t\tif false {", "./internal/storage/datasets/"),
    ("datasets-with-issues", D, "\tif len(issues) > 0 {\n\t\treturn Resolution{Issues: validate.Sort(issues)}, nil\n\t}\n\tfor _, i := range validate.Bindings", "\tif len(issues) > 0 {\n\t\treturn Resolution{Issues: validate.Sort(issues), Datasets: datasets}, nil\n\t}\n\tfor _, i := range validate.Bindings", "./internal/storage/datasets/"),
    ("manifest-not-sorted", D, "\treturn Resolution{Manifest: sorted, ManifestHash: digest, Datasets: datasets}, nil", "\treturn Resolution{Manifest: manifest, ManifestHash: digest + string(rune(len(sorted)*0)), Datasets: datasets}, nil", "./internal/storage/datasets/"),
    ("ignore-ctx", D, "\tif err := ctx.Err(); err != nil {\n\t\treturn Resolution{}, err\n\t}\n\tif issues, err := shape", "\tif issues, err := shape", "./internal/storage/datasets/"),
    ("store-error-swallowed", D, "\tcase err != nil:\n\t\treturn nil, nil, fmt.Errorf(\"reading dataset version %s: %w\", b.DatasetVersionID, err)", "\tcase err != nil:\n\t\treturn bad(\"\", \"read failed\")", "./internal/storage/datasets/"),
    ("no-size-limit", D, "\tcase len(raw) > MaxSnapshotBytes:", "\tcase false:", "./internal/storage/datasets/"),
    ("bindings-stage-skipped", D, "\tfor _, i := range validate.Bindings(ir, zeroRefs) {", "\tfor _, i := range validate.Bindings(ir, nil) {", "./internal/storage/datasets/"),
]

def main():
    only = set(sys.argv[1:])
    results = []
    for name, rel, old, new, pkg in MUTANTS:
        if only and name not in only:
            continue
        src = (REPO / rel).read_text()
        if src.count(old) != 1:
            results.append((name, "BAD-MUTANT (anchor not unique/found)", ""))
            continue
        mpath = OUT / f"{name.replace('(equivalent)', '')}.go"
        mpath.write_text(src.replace(old, new))
        overlay = OUT / f"{name.replace('(equivalent)', '')}.json"
        overlay.write_text(json.dumps({"Replace": {str(REPO / rel): str(mpath)}}))
        p = subprocess.run(["go", "test", "-count=1", "-overlay", str(overlay), pkg], cwd=REPO, capture_output=True, text=True)
        out = p.stdout + p.stderr
        if p.returncode == 0:
            status = "SURVIVED"
        elif "[build failed]" in out or "setup failed" in out:
            status = "BUILD-FAILED"
        else:
            status = "KILLED"
        first_fail = next((l.strip() for l in out.splitlines() if l.strip().startswith("--- FAIL")), "")
        results.append((name, status, first_fail))
    for r in results:
        print("\t".join(r))
    bad = [r for r in results if r[1] != "KILLED" and not r[0].endswith("(equivalent)")]
    eq = sum(1 for r in results if r[0].endswith("(equivalent)"))
    print(f"{sum(1 for r in results if r[1] == 'KILLED')}/{len(results) - eq} non-equivalent mutants killed; {eq} documented equivalent")
    return 1 if bad else 0

if __name__ == "__main__":
    sys.exit(main())
