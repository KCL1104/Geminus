#!/usr/bin/env python3
"""T-053 schema follow-up, review round 1 (reviewer): mutation pass.

Runs in a `git archive HEAD` copy (never a checkout), three sets one after
the other:

  r1  the round-1 harness's mutants (checks/evidence/2026-10-26-T-053-r1/
      mutants.py) that need no database: the spend, gating, record and key
      mutants, on the code after the schema fix (regression of T-053's
      spend behaviour);
  s   the fix's own harness (checks/evidence/2026-10-26-T-053-schema/
      mutants.py, S1-S17), with restoration checked by hash instead of git;
  R   the reviewer's mutants (below), and F1, a candidate fix for M1 that
      is expected to pass every test (reported FIXPASS, not a failure);
  K   r1 mutants whose text the fix changed, brought to the new code (K11).

Each mutant replaces text occurring exactly once in one file, `go vet` on
its packages (INVALID if it does not build), then `go test -count=1 -p 1`:
KILLED when the tests fail, SURVIVED when they pass (EQUIVALENT when marked
so). Every file is restored and its hash compared. No network call.

    python3 reviewer_mutants.py <copy> <worktree> [r1|s|R|K ...]
"""
import hashlib
import importlib.util
import os
import subprocess
import sys

A = "./internal/ai/provider/anthropic/"
E = "./internal/aievals/"
G = "./internal/proposals/generator/"
ENC = "internal/ai/provider/anthropic/encoded.go"
PRJ = "internal/ai/provider/anthropic/projection.go"
GRD = "internal/ai/provider/anthropic/guard_test.go"
ADP = "internal/ai/provider/anthropic/anthropic.go"

# (id, what, file, [(old, new)], packages, expect) expect: "kill", "equivalent", "pass" (a candidate fix)
REVIEWER = [
    ("R1", "the issues of one answer are not capped at 20", ENC,
     [("return nil, issues[:min(len(issues), maxIssues)]", "return nil, issues")], [A], "kill"),
    ("R2", "the discriminator need not be required in every alternative", PRJ,
     [("if !has || !slices.Contains(a.required, any(name)) {", "if !has {")], [A], "kill"),
    ("R3", "a JSON text is not compacted", ENC,
     [("if err := inner.Compact(); err != nil {", "if err := error(nil); err != nil {")], [A], "kill"),
    ("R4", "a rewritten object's strings are re-escaped (asWritten dropped)", ENC,
     [("data, err := json.Marshal(obj, asWritten, json.Deterministic(true))", "data, err := json.Marshal(obj, json.Deterministic(true))")], [A], "kill"),
    ("R5", "the size bound is off by one (exactly 64 KiB refused)", ENC,
     [("if len(s) > MaxEncodedValueBytes {", "if len(s) >= MaxEncodedValueBytes {")], [A], "kill"),
    ("R6", "the model is not told how to encode (description)", PRJ,
     [("desc := EncodedDescription\n", "desc := \"a string\"\n")], [A], "kill"),
    ("R7", "an open value inside untold alternatives is encoded", PRJ,
     [("case at.untold || len(at.when) > 0:", "case false:")], [A], "kill"),
    ("R8", "a description-only projection counts as typed", PRJ,
     [('for _, k := range []string{"type", "enum", "const", "anyOf", "allOf"} {', 'for _, k := range []string{"type", "enum", "const", "anyOf", "allOf", "description"} {')], [A], "kill"),
    ("R9", "the guard does not count optional members", GRD,
     [("if !required[name] {\n\t\t\tg.optional++", "if false && !required[name] {\n\t\t\tg.optional++")], [A], "kill"),
    ("R10", "the guard does not count type arrays as unions", GRD,
     [("case []any:\n\t\t\t\tg.unions++", "case []any:\n\t\t\t\t_ = g.unions")], [A], "kill"),
    ("R11", "the worker warns of every provider error, with or without a status", "internal/proposals/generator/generator.go",
     [("if pe != nil && pe.Status != 0 {", "if pe != nil {")], [G], "kill"),
    ("R12", "the usage is not kept when the answer does not decode", ADP,
     [("\t\te := provider.NewError(provider.CodeInvalidOutput, 0, resp.Credential)\n\t\te.Issues = issues\n\t\treturn resp, e",
       "\t\te := provider.NewError(provider.CodeInvalidOutput, 0, resp.Credential)\n\t\te.Issues = issues\n\t\treturn provider.Response{Model: resp.Model, Credential: resp.Credential}, e")], [A], "kill"),
    ("R13", "an error status's type is recorded only for 4xx (5xx and 429 none)", ADP,
     [("\t\tcase status == http.StatusTooManyRequests:\n\t\t\treturn resp, statusErr(provider.CodeRateLimited)",
       "\t\tcase status == http.StatusTooManyRequests:\n\t\t\treturn resp, provider.NewError(provider.CodeRateLimited, status, c.label)")], [A], "kill"),
    ("R14", "M1 as the code is: an encoder error makes the answer pass undecoded (equivalent to HEAD, survives)", ENC,
     [("return text, nil // cannot happen for a valid document; the client's check decides",
       "return text, nil // cannot happen for a valid document; the client's check decides (R14)")], [A], "equivalent"),
    ("F1", "candidate fix for M1: an answer the adapter cannot rewrite is invalid_output (expected to pass every test)", ENC,
     [("return text, nil // cannot happen for a valid document; the client's check decides",
       'return nil, []model.ValidationIssue{model.NewIssue(provider.OutputInvalid, "", "the answer cannot be decoded", model.SeverityError)}')], [A, E], "pass"),
]


# r1 mutants whose text the schema fix changed, brought to the new code
ADAPTED = [
    ("r1:K11a", "an error after paid calls prints no summary or cost (K11 on the new main.go)", "internal/aievals/main.go",
     [("\t\tif report != nil {\n\t\t\tfmt.Fprintln(stdout, summary())\n\t\t\tproviderErrors()\n\t\t} else {",
       "\t\tif report != nil {\n\t\t\t_ = summary\n\t\t\tproviderErrors()\n\t\t} else {")], [E, "./internal/config/aiapproval/"], "kill"),
]


def load(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod.MUTANTS


def run(args, cwd, env):
    p = subprocess.run(args, capture_output=True, text=True, cwd=cwd, env=env)
    return p.returncode, p.stdout + p.stderr


def failing(out):
    return sorted({l.split()[2] for l in out.splitlines() if l.startswith("--- FAIL: ")})[:6]


def digest(path):
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


def one(root, env, mid, what, rel, edits, pkgs, expect):
    path = os.path.join(root, rel)
    before = digest(path)
    with open(path, encoding="utf-8") as f:
        orig = f.read()
    mutated, bad = orig, None
    for old, new in edits:
        n = mutated.count(old)
        if n != 1:
            bad = f"pattern occurs {n} times"
            break
        mutated = mutated.replace(old, new)
    if bad:
        result, detail = "ERROR", bad
    else:
        try:
            with open(path, "w", encoding="utf-8") as f:
                f.write(mutated)
            code, out = run(["go", "vet", *pkgs], root, env)
            if code != 0:
                result, detail = "INVALID", "does not build: " + out.strip().splitlines()[-1][:160]
            else:
                code, out = run(["go", "test", "-count=1", "-p", "1", *pkgs], root, env)
                if code != 0:
                    result = "KILLED" if expect != "pass" else "FIXFAILS"
                    detail = ", ".join(failing(out)) or out.strip().splitlines()[-1][:160]
                elif expect == "equivalent":
                    result, detail = "EQUIVALENT", "tests pass, as expected"
                elif expect == "pass":
                    result, detail = "FIXPASS", "every test passes"
                else:
                    result, detail = "SURVIVED", "tests pass"
        finally:
            with open(path, "w", encoding="utf-8") as f:
                f.write(orig)
    if digest(path) != before:
        result, detail = "ERROR", "the file was not restored"
    print(f"{mid}\t{result}\t{what}\t{rel}\t{' '.join(pkgs)}\t{detail}", flush=True)
    return result


def main():
    root, tree = os.path.abspath(sys.argv[1]), os.path.abspath(sys.argv[2])
    sets = sys.argv[3:] or ["r1", "s", "R"]
    if os.path.exists(os.path.join(root, ".git")):
        sys.exit("refusing: run it in a `git archive` copy, not a checkout")
    env = {k: v for k, v in os.environ.items() if k in ("PATH", "HOME", "GOPATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "GOFLAGS")}
    counts = {}
    todo = []
    if "r1" in sets:
        for mid, what, rel, edits, pkgs, db, equivalent in load(os.path.join(tree, "checks/evidence/2026-10-26-T-053-r1/mutants.py"), "r1"):
            if db:
                continue
            todo.append(("r1:" + mid, what, rel, edits, pkgs, "equivalent" if equivalent else "kill"))
    if "s" in sets:
        for mid, what, rel, edits, pkgs in load(os.path.join(tree, "checks/evidence/2026-10-26-T-053-schema/mutants.py"), "s"):
            todo.append(("s:" + mid, what, rel, edits, pkgs, "kill"))
    if "R" in sets:
        todo.extend(REVIEWER)
    if "K" in sets:
        todo.extend(ADAPTED)
    if "R9" in sets:  # the second run of R9, after its first form did not build
        todo.extend(m for m in REVIEWER if m[0] == "R9")
    for mid, what, rel, edits, pkgs, expect in todo:
        r = one(root, env, mid, what, rel, edits, pkgs, expect)
        counts[r] = counts.get(r, 0) + 1
    print("summary\t" + " ".join(f"{k}={v}" for k, v in sorted(counts.items())))
    bad = sum(counts.get(k, 0) for k in ("SURVIVED", "ERROR", "INVALID", "FIXFAILS"))
    return 0 if bad == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
