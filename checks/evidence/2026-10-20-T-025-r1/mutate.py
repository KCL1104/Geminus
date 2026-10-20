#!/usr/bin/env python3
"""T-025 review round 1: targeted mutants of the server's review, risk and
generator (equivalent to the reviewer's mutate.py, retargeted at this
worktree's code). Applies one mutant at a time, runs the packages' tests,
restores the file, and checks the restore byte for byte.

usage: mutate.py OUT.log [MUTANT_ID ...]
"""
import os
import shutil
import subprocess
import sys

REPO = "/home/user/Geminus/.claude/worktrees/agent-a7aaf00e92c7f69f8"
S = "/tmp/claude-0/-home-user-Geminus/b4164204-ed28-580b-a7b5-c02b59026d38/scratchpad/t025-r1"

REVIEW = ["./internal/ai/review/", "./internal/ai/corpus/"]
GEN = ["./internal/proposals/...", "./internal/storage/proposals/"]
RISK = "internal/ai/review/risk.go"
REV = "internal/ai/review/review.go"
CLEAN = "internal/ai/review/clean.go"
GENF = "internal/proposals/generator/generator.go"

M = [
    ("M01", "claim ignored (risk = server)", REV,
     "ServerRisk: server, ProviderRisk: claim, Risk: Max(server, claim),",
     "ServerRisk: server, ProviderRisk: claim, Risk: server,", REVIEW),
    ("M02", "node type change not high", RISK,
     "\t\tcase c.Type != n.Type:\n\t\t\traise(High)\n",
     "\t\tcase c.Type != n.Type:\n\t\t\traise(Low)\n", REVIEW),
    ("M03", "new cycle not medium", RISK,
     "\t\tif !baseCycles[e] {\n\t\t\traise(Medium)",
     "\t\tif !baseCycles[e] {\n\t\t\traise(Low)", REVIEW),
    ("M04b", "probability rise on a cycle ignored", RISK,
     "t.Type == model.NodeRework) || onCycle[id] {",
     "t.Type == model.NodeRework) || (onCycle[id] && false) {", REVIEW),
    ("M04c", "probability rise into failure/rework ignored", RISK,
     "if t, ok := candNodes[c.To]; ok && (t.Type == model.NodeFailure || t.Type == model.NodeRework) || onCycle[id] {",
     "if onCycle[id] {", REVIEW),
    ("M05", "constraint change ignored", RISK,
     "\t\t!sameConstraints(base.Constraints, candidate.Constraints) {",
     "\t\tfalse {", REVIEW),
    ("M06", "entity type add/remove ignored", RISK,
     "\tif added(byID(base.EntityTypes, func(e model.EntityType) string { return e.ID }), byID(candidate.EntityTypes, func(e model.EntityType) string { return e.ID })) ||\n\t\tadded(byID(candidate.EntityTypes, func(e model.EntityType) string { return e.ID }), byID(base.EntityTypes, func(e model.EntityType) string { return e.ID })) ||\n",
     "\tif ", REVIEW),
    ("M06b", "metric add/remove ignored", RISK,
     "\t\tadded(byID(base.Metrics, func(m model.Metric) string { return m.ID }), byID(candidate.Metrics, func(m model.Metric) string { return m.ID })) ||\n\t\tadded(byID(candidate.Metrics, func(m model.Metric) string { return m.ID }), byID(base.Metrics, func(m model.Metric) string { return m.ID })) ||\n",
     "", REVIEW),
    ("M07", "capacity decrease not medium", RISK,
     "\t\tcase c.Capacity < r.Capacity:\n\t\t\traise(Medium)",
     "\t\tcase c.Capacity < r.Capacity:\n\t\t\traise(Low)", REVIEW),
    ("M08", "flow re-target not medium", RISK,
     "\t\tif c.From != f.From || c.To != f.To {\n\t\t\traise(Medium)",
     "\t\tif c.From != f.From || c.To != f.To {\n\t\t\traise(Low)", REVIEW),
    ("M10", "domain pack errors ignored", REV,
     "\tif slices.ContainsFunc(packIssues, isError) {\n\t\treturn invalid(packIssues), nil\n\t}",
     "\tif false {\n\t\treturn invalid(packIssues), nil\n\t}", REVIEW),
    ("M16", "requirement change not medium", RISK,
     "\t\tcase !sameRequirements(n.Requires, c.Requires):\n\t\t\traise(Medium)",
     "\t\tcase !sameRequirements(n.Requires, c.Requires):\n\t\t\traise(Low)", REVIEW),
    ("M31", "added node ignored", RISK,
     "\tif added(baseNodes, candNodes) || added(baseRes, candRes) {",
     "\tif added(baseRes, candRes) {", REVIEW),
    ("M32", "added resource ignored", RISK,
     "\tif added(baseNodes, candNodes) || added(baseRes, candRes) {",
     "\tif added(baseNodes, candNodes) {", REVIEW),
    ("M33", "added flow ignored", RISK,
     "\tif added(baseFlows, candFlows) || added(candFlows, baseFlows) {",
     "\tif added(candFlows, baseFlows) {", REVIEW),
    ("M34", "removed flow ignored", RISK,
     "\tif added(baseFlows, candFlows) || added(candFlows, baseFlows) {",
     "\tif added(baseFlows, candFlows) {", REVIEW),
    ("M35", "removed node not high", RISK,
     "\t\tc, ok := candNodes[id]\n\t\tswitch {\n\t\tcase !ok:\n\t\t\traise(High)",
     "\t\tc, ok := candNodes[id]\n\t\tswitch {\n\t\tcase !ok:\n\t\t\traise(Medium)", REVIEW),
    ("M36", "removed resource not high", RISK,
     "\t\tc, ok := candRes[id]\n\t\tswitch {\n\t\tcase !ok:\n\t\t\traise(High)",
     "\t\tc, ok := candRes[id]\n\t\tswitch {\n\t\tcase !ok:\n\t\t\traise(Medium)", REVIEW),
    ("L1", "fewer units treated as no change (the old n > b)", RISK,
     "\t\tif !ok || n != b {",
     "\t\tif !ok || n > b {", REVIEW),
    ("M1a", "invalid issues not cleaned", REV,
     "\tissues = cleaned(issues)\n\tif !slices.ContainsFunc(issues, isError) {",
     "\tif !slices.ContainsFunc(issues, isError) {", REVIEW),
    ("M1b", "ready warnings not cleaned", REV,
     "Issues: nonNilIssues(capped(cleaned(issues)))}}, nil",
     "Issues: nonNilIssues(capped(issues))}}, nil", REVIEW),
    ("M1c", "unknown path tokens kept", CLEAN,
     "\t\tif !arrayIndex.MatchString(tok) && !members[tok] {\n\t\t\tbreak\n\t\t}",
     "\t\t_ = tok", REVIEW),
    ("M2a", "NUL in text accepted", REV,
     "\t\tcase strings.ContainsRune(s, 0):\n\t\t\tbad(path, sentNUL)\n",
     "\t\tcase strings.ContainsRune(s, 0) && false:\n\t\t\tbad(path, sentNUL)\n", REVIEW + GEN),
    ("M2b", "NUL in changes accepted", REV,
     "\t\tif at, ok := nulAt(encoded); ok {",
     "\t\tif at, ok := nulAt(encoded); ok && false {", REVIEW + GEN),
    ("M2c", "empty text accepted", REV,
     "\t\tcase s == \"\":\n\t\t\tbad(path, sentEmpty)\n",
     "", REVIEW + GEN),
    ("M2d", "unstorable Finish retried", GENF,
     "\tif errors.Is(err, store.ErrUnstorable) {",
     "\tif false && errors.Is(err, store.ErrUnstorable) {", GEN),
    ("M3", "candidate names supply numbers again", REV,
     "slices.Concat(values(baseIR), nameNumbers(baseIR), values(candidate))",
     "slices.Concat(values(baseIR), nameNumbers(baseIR), values(candidate), nameNumbers(candidate))", REVIEW),
    ("L3a", "snapshots not read before the provider call", GENF,
     "\tsnaps, err := h.preload(ctx, org, st)",
     "\tsnaps, err := copies{}, error(nil)", GEN),
    ("L3b", "base snapshots not verified before the provider call", GENF,
     "\tmanifest, err := h.verify(ctx, org, pr.ID, snaps, &ir, v.DatasetBindings)",
     "\tmanifest, err := v.DatasetBindings, error(nil)", GEN),
    ("L3c", "a storage failure before the call not retried", "internal/proposals/generator/snapshots.go",
     "failure(DatasetStorageFailed, \"the dataset snapshots could not be read\", true)",
     "failure(DatasetStorageFailed, \"the dataset snapshots could not be read\", false)", GEN),
    ("L5", "ready stores no dataset manifest", GENF,
     "\t\tg.DatasetBindings = manifest\n",
     "", GEN),
]


def run(pkgs):
    p = subprocess.run(["go", "test", "-count=1"] + pkgs, cwd=REPO, capture_output=True, text=True)
    return p.returncode, p.stdout + p.stderr


def main():
    out = sys.argv[1]
    only = set(sys.argv[2:])
    lines = []
    for mid, name, rel, old, new, pkgs in M:
        if only and mid not in only:
            continue
        path = os.path.join(REPO, rel)
        src = open(path, encoding="utf-8").read()
        n = src.count(old)
        if n != 1:
            verdict = f"NOT APPLIED (the original text occurs {n} times)"
        else:
            backup = os.path.join(S, "backup-" + os.path.basename(rel))
            shutil.copyfile(path, backup)
            try:
                open(path, "w", encoding="utf-8").write(src.replace(old, new, 1))
                code, text = run(pkgs)
            finally:
                shutil.copyfile(backup, path)
            assert open(path, encoding="utf-8").read() == src, "restore failed: " + rel
            if code == 0:
                verdict = "SURVIVED (tests pass)"
            elif "build failed" in text or "[setup failed]" in text:
                verdict = "BUILD FAILED"
            else:
                fails = [l.strip() for l in text.splitlines() if l.lstrip().startswith("--- FAIL")]
                verdict = "KILLED: " + "; ".join(fails[:4])
        line = f"{mid} {name} [{' '.join(pkgs)}] => {verdict}"
        print(line, flush=True)
        lines.append(line)
    with open(out, "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")


main()
