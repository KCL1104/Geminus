"""Mutants of the T-015 / T-032 review-round-1 fixes: one change at a time,
run the tests named, restore the file from the text read before. Writes a
JSON summary and one log per mutant into the evidence directory given."""
import json
import os
import subprocess
import sys

root, out = sys.argv[1], sys.argv[2]
web = os.path.join(root, "apps/web")
go_env = dict(os.environ, PATH="/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:" + os.environ["PATH"], GOTOOLCHAIN="local")

E = "apps/web/features/model-editor/"
S = "apps/web/features/simulations/"
mutants = [
    ("T015-A", "edits.ts setAt: no test of the old value before replace/remove", E + "edits.ts",
     'const checked: Op[] = [...tests, { op: "test", path, value: snapshot(old) }];', "const checked: Op[] = [...tests];",
     ["tests/editor-adapter.test.ts", "tests/editor.test.tsx"]),
    ("T015-B", "model-editor.tsx: reapply decides with applyPatch (RFC add overwrites)", E + "model-editor.tsx",
     "if (reapplyPatch(r.data.ir, pending).ok) {", "if (applyPatch(r.data.ir, pending).ok) {",
     ["tests/editor.test.tsx"]),
    ("T015-C", "patch.ts reapplyPatch: an add may overwrite a member that is there", E + "patch.ts",
     '  if (exists && op.op === "add" && addsAbsent) return "test_failed";\n', "",
     ["tests/editor-patch.test.ts", "tests/editor-adapter.test.ts", "tests/editor.test.tsx"]),
    ("T015-D", "patch.ts reapplyPatch: an append may repeat an ID the array has", E + "patch.ts",
     '      if (addsAbsent && id !== undefined && parent.some((x) => idOf(x) === id)) return "test_failed";\n', "",
     ["tests/editor-patch.test.ts", "tests/editor-adapter.test.ts"]),
    ("T015-E", "model-editor.tsx: in a conflict the canvas applies the edits with applyPatch", E + "model-editor.tsx",
     '(session.status === "conflict" ? reapplyPatch : applyPatch)(session.remote.ir, pendingOps(session))', "applyPatch(session.remote.ir, pendingOps(session))",
     ["tests/editor.test.tsx"]),
    ("T015-F", "edits.ts removeWhere: no test of the whole element before its removal", E + "edits.ts",
     '    out.push({ op: "test", path: pointer(catalog, i), value: snapshot(x) });\n', "",
     ["tests/editor-adapter.test.ts"]),
    ("T015-G", "edits.ts deleteStep: no test of the whole node before its removal", E + "edits.ts",
     '{ op: "test", path: pointer("nodes", i), value: snapshot(ir.nodes[i]!) }, testNode(i, id)', "testNode(i, id)",
     ["tests/editor-adapter.test.ts"]),
    ("T015-H", "edits.ts deleteStep: the position is removed without a test of it", E + "edits.ts",
     'ops.push(...setAt([], pointer("views", "2d", "positions", id), positions[id], undefined));', 'ops.push({ op: "remove", path: pointer("views", "2d", "positions", id) });',
     ["tests/editor-adapter.test.ts"]),
    ("T032-A", "use-run-pair.ts: a 409 LIFECYCLE_CONFLICT counts as a failed cancel", S + "use-run-pair.ts",
     'else if (answer.problem.code !== "LIFECYCLE_CONFLICT") dispatch', "else dispatch",
     ["tests/results-view.test.tsx"]),
    ("T032-B", "use-run-pair.ts: no read after the cancels", S + "use-run-pair.ts",
     "    // Follow the cancel to its end, also when polling had stopped.\n    nextAttempt();\n", "",
     ["tests/results-view.test.tsx"]),
    ("T032-C", "use-run-pair.ts: only the first run still going is cancelled", S + "use-run-pair.ts",
     "      runs.map(async (run) => {", "      runs.slice(0, 1).map(async (run) => {",
     ["tests/results-view.test.tsx"]),
    ("T032-D", "run-pair.ts: a second failure replaces the first instead of naming both", S + "run-pair.ts",
     '{ side: "both", problem: first.problem }', "{ side, problem: e.problem }",
     ["tests/simulations.test.ts", "tests/results-view.test.tsx"]),
    ("T032-E", "run-status.tsx: the failure does not name whose cancel failed", S + "run-status.tsx",
     "t(`runs.cancelFailed.${cancelProblem.side}`)", 't("runs.cancelFailed.both")',
     ["tests/results-view.test.tsx"]),
    ("T032-F", "run-status.tsx: the cancel's problem is not shown", S + "run-status.tsx",
     "          <ProblemView problem={cancelProblem.problem} headingLevel={3} />\n", "",
     ["tests/results-view.test.tsx"]),
    ("T032-G", "use-run-pair.ts: a failed cancel is not reported (review r1 L1 R2 at the new place)", S + "use-run-pair.ts",
     'else if (answer.problem.code !== "LIFECYCLE_CONFLICT") dispatch({ type: "cancelFailed", runId: run.id, problem: answer.problem });', "else void 0;",
     ["tests/results-view.test.tsx"]),
    ("T032-L4", "metrics.ts unitOf: ratio is a plain number again", "apps/web/features/results/metrics.ts",
     '  if (summary?.unit === "ratio") return "ratio";\n', "",
     ["tests/results-metrics.test.ts", "tests/results-view.test.tsx"]),
]

only = set(sys.argv[3:])
summary = []
for mid, what, rel, old, new, tests in mutants:
    if only and mid not in only:
        continue
    path = os.path.join(root, rel)
    text = open(path).read()
    assert text.count(old) == 1, (mid, "pattern not found once")
    open(path, "w").write(text.replace(old, new))
    try:
        r = subprocess.run(["npx", "vitest", "run", *tests], cwd=web, capture_output=True, text=True, timeout=1500)
        log = r.stdout + r.stderr
    finally:
        open(path, "w").write(text)
    open(os.path.join(out, f"mutant-{mid}.log"), "w").write(log)
    failed = [l.strip() for l in log.splitlines() if l.strip().startswith("×")]
    result = "killed" if r.returncode != 0 else "survived"
    summary.append({"id": mid, "mutation": what, "file": rel, "tests": tests, "exit": r.returncode, "result": result, "failing_tests": failed})
    print(mid, result, r.returncode, len(failed), flush=True)

# Go: the server evaluates the new tests: a wrong old value in one vector must be refused.
if not only or "GO-A" in only:
    vec = os.path.join(root, "testdata/editor/patch-vectors.json")
    text = open(vec).read()
    old = '"op": "test",\n          "path": "/nodes/4/capacity",\n          "value": 20'
    assert text.count(old) >= 1, "GO-A pattern"
    open(vec, "w").write(text.replace(old, old[:-2] + "21", 1))
    try:
        r = subprocess.run(["go", "test", "-count=1", "-run", "TestEditorVectors", "./internal/model/patch/"], cwd=root, capture_output=True, text=True, env=go_env, timeout=900)
        log = r.stdout + r.stderr
    finally:
        open(vec, "w").write(text)
    open(os.path.join(out, "mutant-GO-A.log"), "w").write(log)
    result = "killed" if r.returncode != 0 else "survived"
    summary.append({"id": "GO-A", "mutation": "patch-vectors.json: one vector tests a wrong old capacity (21, not 20)", "file": "testdata/editor/patch-vectors.json", "tests": ["go test -run TestEditorVectors ./internal/model/patch/"], "exit": r.returncode, "result": result, "failing_tests": [l.strip() for l in log.splitlines() if "--- FAIL" in l or "PATCH_TEST_FAILED" in l][:5]})
    print("GO-A", result, r.returncode, flush=True)

name = "mutants.json" if not only else "mutants-" + "-".join(sorted(only)) + ".json"
json.dump(summary, open(os.path.join(out, name), "w"), indent=2, ensure_ascii=False)
