#!/usr/bin/env python3
"""T-053 schema follow-up: mutation pass.

Each mutant replaces text that occurs exactly once in one file (all its
edits must apply), is built with `go vet` on the packages it names (a mutant
that does not build is INVALID, never killed), then those packages' tests
run with -count=1: KILLED when they fail, SURVIVED when they pass. Every
file is restored in a finally block, and the last line checks that
internal/ and cmd/ are unchanged against the last commit. Run it from the
repository root with Go 1.27.3 first on PATH:

    python3 checks/evidence/2026-10-26-T-053-schema/mutants.py [--only S1,S2] > log

No network call; no environment value is read besides what go itself uses.
"""
import subprocess
import sys

A = "./internal/ai/provider/anthropic/"
E = "./internal/aievals/"
G = "./internal/proposals/generator/"
AP = "./internal/config/aiapproval/"
ENC = "internal/ai/provider/anthropic/encoded.go"
PRJ = "internal/ai/provider/anthropic/projection.go"
ADP = "internal/ai/provider/anthropic/anthropic.go"

# (id, what it breaks, file, [(old, new)], packages)
MUTANTS = [
    # the coordinator's four
    ("S1", "skip the decode step (Decode returns every answer as it came)", ENC,
     [("if len(p.Encoded) == 0 || !jsontext.Value(text).IsValid(strict) {", "if true || len(p.Encoded) == 0 || !jsontext.Value(text).IsValid(strict) {")], [A, E]),
    ("S1b", "skip the decode step in the adapter (answer decodes with no projection)", ADP,
     [("decoded, issues := proj.Decode([]byte(text.String()))", "decoded, issues := Projection{}.Decode([]byte(text.String()))")], [A]),
    ("S2", "decode at the wrong position: the alternatives' const members are ignored", ENC,
     [("for _, c := range st.When {", "for _, c := range st.When[:0] {")], [A]),
    ("S2b", "decode at the wrong position: the place recorded is the patch item, not its value", PRJ,
     [("loc := Location{Steps: at.steps}", "loc := Location{Steps: at.steps[:len(at.steps)-1]}")], [A]),
    ("S2c", "decode at the wrong position: only the first change of a patch", ENC,
     [("for i := range items {", "for i := range items[:min(1, len(items))] {")], [A]),
    ("S2d", "decode at the wrong position: the issue names the next item", ENC,
     [('ptr+"/"+strconv.Itoa(i), issues)', 'ptr+"/"+strconv.Itoa(i+1), issues)')], [A]),
    ("S3", "accept trailing data (read the first JSON value of the text only)", ENC,
     [('\t"strconv"\n', '\t"strconv"\n\t"strings"\n'),
      ("inner := jsontext.Value(s)\n\tif !inner.IsValid(strict) {",
       "inner, verr := jsontext.NewDecoder(strings.NewReader(s), strict).ReadValue()\n\tinner = inner.Clone()\n\tif verr != nil {")], [A]),
    ("S4", "drop the guard's empty-schema check", "internal/ai/provider/anthropic/guard_test.go",
     [("if open {\n\t\tg.add(path, \"an empty schema accepts any JSON value\")", "if false && open {\n\t\tg.add(path, \"an empty schema accepts any JSON value\")")], [A]),
    # more of the decoding
    ("S5", "no size limit on a JSON text", ENC,
     [("if len(s) > MaxEncodedValueBytes {", "if false {")], [A]),
    ("S6", "a value that is not a string is taken as it is", ENC,
     [('return nil, "the projection asks for a string holding a JSON text here"', 'return v, ""')], [A]),
    ("S7", "duplicate member names are accepted", ENC,
     [("var strict = json.JoinOptions(jsontext.AllowDuplicateNames(false), jsontext.AllowInvalidUTF8(false))",
       "var strict = json.JoinOptions(jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(false))")], [A]),
    ("S8", "the alternatives' condition is inverted", ENC,
     [("!reflect.DeepEqual(got, c.Const)", "reflect.DeepEqual(got, c.Const)")], [A]),
    # the projection
    ("S9", "an open value projects to a plain string, its place not listed (nothing decodes it)", PRJ,
     [("\t\treturn p.encode(v, file, ptr, at)\n", "\t\treturn map[string]any{\"type\": \"string\"}, nil\n")], [A]),
    ("S9b", "an open value projects to {} again, as before the fix", PRJ,
     [("\t\treturn p.encode(v, file, ptr, at)\n", "\t\treturn map[string]any{}, nil\n")], [A]),
    ("S9c", "an open value projects to {} again and nothing refuses a schema without a type", PRJ,
     [("\t\treturn p.encode(v, file, ptr, at)\n", "\t\treturn map[string]any{}, nil\n"), ("if !typed(out) {", "if false {")], [A]),
    ("S10", "a schema without a concrete type is sent", PRJ,
     [("if !typed(out) {", "if false {")], [A]),
    ("S11", "an open allOf member is encoded instead of dropped", PRJ,
     [("if p.openRef(sub, file) {", "if false && p.openRef(sub, file) {")], [A]),
    # provider errors
    ("S12", "the error type is not checked against the API's list", ADP,
     [("if t := string(e.Type()); slices.Contains(errorTypes, t) {", "if t := string(e.Type()); true {")], [A]),
    ("S13", "the error type is not recorded", ADP,
     [("e.ErrorType = errorType(apiErr)", 'e.ErrorType = ""')], [A]),
    ("S14", "the report does not name the provider status and type", "internal/aievals/live.go",
     [("o.ProviderStatus, o.ProviderErrorType = pe.Status, pe.ErrorType", "_ = pe")], [E, A]),
    ("S15", "ai-eval-live prints no warning for a provider error", "internal/aievals/main.go",
     [("if o.ProviderStatus != 0 {", "if false {")], [AP]),
    ("S16", "the worker logs no warning for a provider error", "internal/proposals/generator/generator.go",
     [("if pe != nil && pe.Status != 0 {", "if false {")], [G]),
    ("S17", "the log key provider_error_type is not registered (redacted)", "internal/observability/redact.go",
     [('\t"provider_error_type": KindToken,\n', "")], [G]),
]


def run(args):
    p = subprocess.run(args, capture_output=True, text=True)
    return p.returncode, p.stdout + p.stderr


def failing(out):
    return sorted({l.split()[2] for l in out.splitlines() if l.startswith("--- FAIL: ")})


def main():
    only = None
    if len(sys.argv) > 2 and sys.argv[1] == "--only":
        only = set(sys.argv[2].split(","))
    counts = {}
    for mid, what, path, edits, pkgs in MUTANTS:
        if only and mid not in only:
            continue
        with open(path, encoding="utf-8") as f:
            orig = f.read()
        mutated = orig
        bad = None
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
                code, out = run(["go", "vet", *pkgs])
                if code != 0:
                    result, detail = "INVALID", "does not build: " + out.strip().splitlines()[-1][:160]
                else:
                    code, out = run(["go", "test", "-count=1", *pkgs])
                    if code != 0:
                        result, detail = "KILLED", ", ".join(failing(out)) or out.strip().splitlines()[-1][:160]
                    else:
                        result, detail = "SURVIVED", "tests pass"
            finally:
                with open(path, "w", encoding="utf-8") as f:
                    f.write(orig)
        counts[result] = counts.get(result, 0) + 1
        print(f"{mid}\t{result}\t{what}\t{path}\t{' '.join(pkgs)}\t{detail}", flush=True)
    code, out = run(["git", "status", "--porcelain", "--", "internal", "cmd"])
    print("summary\t" + " ".join(f"{k}={v}" for k, v in sorted(counts.items())))
    print("sources restored (git status of internal/ and cmd/ against the last commit): " + ("clean" if not out.strip() else "CHANGED:\n" + out))
    return 0 if counts.get("SURVIVED", 0) == 0 and counts.get("ERROR", 0) == 0 and counts.get("INVALID", 0) == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
