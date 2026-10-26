#!/usr/bin/env python3
"""T-053 schema follow-up, review round 1 fixes: mutants of the new code.

The reviewer's R14 and F1 named the line M1 replaced, and S1 the condition
the fix split in two, so they cannot apply any more (ERROR in the
reviewer's harness). These are the same questions on the new code, plus
one per new rule. Each runs through the reviewer's own `one()`
(../reviewer/reviewer_mutants.py): one exact replacement, `go vet`, then
`go test -count=1 -p 1`, the file restored and its hash compared. Only in a
`git archive` copy; no network call.

    python3 fix_mutants.py <copy> <worktree> [ID ...]   (no ID: all of them)
"""
import importlib.util
import os
import sys

A = "./internal/ai/provider/anthropic/"
E = "./internal/aievals/"
ENC = "internal/ai/provider/anthropic/encoded.go"

CANNOT = ('\t\t\tcannot := model.NewIssue(provider.OutputInvalid, "", "the answer cannot be decoded", model.SeverityError)\n'
          '\t\t\treturn nil, append(issues[:min(len(issues), maxIssues-1)], cannot)')
NOT_STRICT = 'return nil, []model.ValidationIssue{model.NewIssue(provider.OutputInvalid, "", "the answer is not one strict JSON document", model.SeverityError)}'

# (id, what, file, [(old, new)], packages, expect)
MUTANTS = [
    ("M1a", "M1 put back: an answer Decode cannot rewrite goes on undecoded (R14 on the new code)", ENC,
     [(CANNOT, "\t\t\treturn text, nil")], [A, E], "kill"),
    ("M1b", "a rewrite error drops the issues found before (F1 as the reviewer wrote it)", ENC,
     [(CANNOT, '\t\t\treturn nil, []model.ValidationIssue{model.NewIssue(provider.OutputInvalid, "", "the answer cannot be decoded", model.SeverityError)}')], [A], "kill"),
    ("M1c", "a rewrite error's root issue may be cut by the cap", ENC,
     [("append(issues[:min(len(issues), maxIssues-1)], cannot)", "append(issues, cannot)[:min(len(issues)+1, maxIssues)]")], [A], "kill"),
    ("M1d", "an answer that is not one strict JSON document goes on undecoded again", ENC,
     [(NOT_STRICT, "return text, nil")], [A, E], "kill"),
    ("M1e", "no nesting bound", ENC,
     [("if deeper(inner, MaxEncodedValueDepth) {", "if false {")], [A, E], "kill"),
    ("M1f", "the nesting bound is off by one (exactly 64 levels refused)", ENC,
     [("if dec.StackDepth() > limit {", "if dec.StackDepth() >= limit {")], [A], "kill"),
    ("M1g", "the nesting bound is the decoder's own (10000)", ENC,
     [("const MaxEncodedValueDepth = 64", "const MaxEncodedValueDepth = 10000")], [A, E], "kill"),
    ("M1h", "deeper fails open on a read error other than the end (cannot happen for a valid text)", ENC,
     [("return !errors.Is(err, io.EOF)", "return false && !errors.Is(err, io.EOF)")], [A], "equivalent"),
    ("S1a", "skip the decode step (S1 on the new code: Decode returns every answer as it came)", ENC,
     [("if len(p.Encoded) == 0 {", "if true || len(p.Encoded) == 0 {")], [A, E], "kill"),
]


def main():
    root, tree = os.path.abspath(sys.argv[1]), os.path.abspath(sys.argv[2])
    if os.path.exists(os.path.join(root, ".git")):
        sys.exit("refusing: run it in a `git archive` copy, not a checkout")
    spec = importlib.util.spec_from_file_location(
        "reviewer_mutants", os.path.join(tree, "checks/evidence/2026-10-26-T-053-schema-r1/reviewer/reviewer_mutants.py"))
    rm = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(rm)
    env = {k: v for k, v in os.environ.items() if k in ("PATH", "HOME", "GOPATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "GOFLAGS")}
    counts = {}
    only = sys.argv[3:]
    for mid, what, rel, edits, pkgs, expect in MUTANTS:
        if only and mid not in only:
            continue
        r = rm.one(root, env, mid, what, rel, edits, pkgs, expect)
        counts[r] = counts.get(r, 0) + 1
    print("summary\t" + " ".join(f"{k}={v}" for k, v in sorted(counts.items())))
    bad = sum(counts.get(k, 0) for k in ("SURVIVED", "ERROR", "INVALID", "FIXFAILS"))
    return 0 if bad == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
