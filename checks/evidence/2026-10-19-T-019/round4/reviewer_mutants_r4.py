#!/usr/bin/env python3
"""Round-4 reviewer mutants for T-019 (Go), beside the author's
checks/evidence/2026-10-19-T-019/mutants.py: round-2/3 reviewer mutants
still applicable, re-targeted to 750b729, and new ones for the round-3
fixes. Same method (`go test -overlay`; the export is never modified; a
mutant that does not build is a harness error); a mutant may make several
replacements. A test binary that panics counts as killed (as the author's
harness at 8eb6647).

Usage: GEMINUS_TEST_DATABASE_URL=... python3 -I reviewer_mutants_r4.py <export-root> [--only=NAME ...]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

ARGS = [a for a in sys.argv[1:] if not a.startswith("--only=")]
ONLY = {a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--only=")}
REPO = pathlib.Path(ARGS[0]).resolve()
LOOP = "internal/worker/worker.go"
CMD = "cmd/worker/main.go"
TESTS = {LOOP: "./internal/worker/", CMD: "./cmd/worker/"}

STOPPED_FN = "\t\tif a.alive.Err() != nil {\n\t\t\treturn fmt.Errorf(\"%w: %w\", a.stopped(), err)\n\t\t}\n\t\treturn err\n"
STOPPED_OTHER = "\tcase a.alive.Err() != nil:\n\t\treturn fmt.Errorf(\"%w: %w\", a.stopped(), err)\n\t}\n"

MUTANTS = [
    # Round 2/3 reviewer mutants still applicable.
    ("rv2-claimed-after-the-claim", LOOP, [
        ("\t\tclaimed := time.Now()\n", "\t\tvar claimed time.Time\n"),
        ("\t\tif ok {\n\t\t\tw.run(ctx, job, lease, claimed,", "\t\tif ok {\n\t\t\tclaimed = time.Now()\n\t\t\tw.run(ctx, job, lease, claimed,"),
    ]),
    ("rv2-deadline-from-the-heartbeat-start", LOOP, [("\tdeadline := start.Add(keep)\n", "\tdeadline := time.Now().Add(keep)\n")]),
    ("rv2-cmd-stop-after-cancel", CMD, [("\t\tsignal.Stop(signals)\n\t\tcancel()\n\t}()", "\t\tcancel()\n\t\tsignal.Stop(signals)\n\t}()")]),
    ("rv3-no-width-check", LOOP, [("if seen == maxErrors || len(queue) > maxErrors {", "if seen == maxErrors {")]),
    ("rv3-text-read-outside-recover", LOOP, [
        ("\tv.text = logText(err)\n\treturn v\n}", "\treturn v\n}"),
        ("\tv := read(err)\n", "\tv := read(err)\n\tif err != nil && !v.broken && !v.large {\n\t\tv.text = logText(err)\n\t}\n"),
    ]),
    ("rv3-new-deadline-dropped-when-busy", LOOP, [("\t\t\tcase deadlines <- deadline:\n\t\t\tcase <-ctx.Done():\n\t\t\t\treturn\n\t\t\t}", "\t\t\tcase deadlines <- deadline:\n\t\t\tdefault:\n\t\t\t}")]),
    ("rv3-renewal-bound-two-heartbeats", LOOP, [("earliest(deadline, started.Add(w.cfg.Heartbeat))", "earliest(deadline, started.Add(2*w.cfg.Heartbeat))")]),
    ("rv3-broken-before-failure", LOOP, [
        ("\tcase v.failure != nil:\n\t\tfailure = checked(*v.failure, log)\n", "\tcase v.broken:\n\t\tfailure = jobs.Failure{Code: Internal, Message: \"the job's handler failed\"}\n\tcase v.failure != nil:\n\t\tfailure = checked(*v.failure, log)\n"),
    ]),
    # Round 4: what locked() returns.
    ("rv4-fn-failure-stop-is-always-ErrStopped", LOOP, [(STOPPED_FN, "\t\tif a.alive.Err() != nil {\n\t\t\treturn fmt.Errorf(\"%w: %w\", handler.ErrStopped, err)\n\t\t}\n\t\treturn err\n")]),
    ("rv4-other-stop-is-always-ErrStopped", LOOP, [(STOPPED_OTHER, "\tcase a.alive.Err() != nil:\n\t\treturn fmt.Errorf(\"%w: %w\", handler.ErrStopped, err)\n\t}\n")]),
    ("rv4-other-stop-drops-the-cause", LOOP, [(STOPPED_OTHER, "\tcase a.alive.Err() != nil:\n\t\treturn a.stopped()\n\t}\n")]),
    ("rv4-outcome-unknown-wrapped-in-the-stop", LOOP, [("\tcase errors.Is(err, tenant.ErrOutcomeUnknown):\n\t\treturn err\n", "\tcase errors.Is(err, tenant.ErrOutcomeUnknown):\n\t\tif a.alive.Err() != nil {\n\t\t\treturn fmt.Errorf(\"%w: %w\", a.stopped(), err)\n\t\t}\n\t\treturn err\n")]),
    ("rv4-outcome-unknown-marks-the-lease-lost", LOOP, [("\tcase errors.Is(err, tenant.ErrOutcomeUnknown):\n\t\treturn err\n", "\tcase errors.Is(err, tenant.ErrOutcomeUnknown):\n\t\ta.lost()\n\t\treturn err\n")]),
    # fn's error read with errors.Is like the transaction's own errors (the
    # `failed` flag never set).
    ("rv4-fn-error-read-like-the-transactions", LOOP, [("\t\t\tfailed = true\n", "\t\t\t_ = failed\n")]),
    ("rv4-lost-record-goes-on", LOOP, [("its end is not recorded\", \"code\", failure.Code)\n\t\treturn\n", "its end is not recorded\", \"code\", failure.Code)\n")]),
    # Round 4: readable().
    ("rv4-readable-panic-is-large", LOOP, [("\t\t\tout = unreadable{typ: typ}\n", "\t\t\tout = unreadable{typ: typ, large: true}\n")]),
    ("rv4-readable-large-is-broken", LOOP, [("\t\treturn unreadable{typ: typ, large: true}\n", "\t\treturn unreadable{typ: typ}\n")]),
    ("rv4-max-errors-63", LOOP, [("const maxErrors = 64", "const maxErrors = 63")]),
    ("rv4-max-errors-65", LOOP, [("const maxErrors = 64", "const maxErrors = 65")]),
    ("rv4-log-text-counts-bytes", LOOP, [("\t\tb.WriteRune(r)\n\t\tn++\n", "\t\tb.WriteRune(r)\n\t\tn += utf8.RuneLen(r)\n")]),
    ("rv4-log-text-ellipsis-always", LOOP, [("\t\tb.WriteRune(r)\n\t\tn++\n\t}\n\treturn b.String()\n", "\t\tb.WriteRune(r)\n\t\tn++\n\t}\n\treturn b.String() + \"…\"\n")]),
    # Round 4: the renewal grid.
    ("rv4-due-not-advanced", LOOP, [("\t\tdue = due.Add(w.cfg.Heartbeat)\n", "")]),
    ("rv4-due-from-the-renewals-end", LOOP, [("\t\tdue = due.Add(w.cfg.Heartbeat)\n", "\t\tdue = time.Now().Add(w.cfg.Heartbeat)\n")]),
    ("rv4-due-from-the-renewals-start", LOOP, [("\t\tdue = due.Add(w.cfg.Heartbeat)\n", "\t\tdue = started.Add(w.cfg.Heartbeat)\n")]),
    ("rv4-first-renewal-at-the-claim", LOOP, [("\tdue := start.Add(w.cfg.Heartbeat)\n", "\tdue := start\n")]),
    ("rv4-due-two-heartbeats", LOOP, [("\t\tdue = due.Add(w.cfg.Heartbeat)\n", "\t\tdue = due.Add(2 * w.cfg.Heartbeat)\n")]),
]


def run(overlay, pkg):
    env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1")
    cmd = ["go", "test", "-count=1", "-timeout=300s", pkg]
    if overlay:
        cmd.insert(2, "-overlay=" + overlay)
    r = subprocess.run(cmd, cwd=REPO, env=env, capture_output=True, text=True, timeout=900)
    if r.returncode:
        if "--- FAIL" in r.stdout:
            fails = [l.strip() for l in r.stdout.splitlines() if l.startswith("--- FAIL")]
            return "KILLED\t" + "; ".join(fails[:4])
        if "panic: test timed out" in r.stdout:
            return "KILLED\ttest timed out"
        if "\npanic: " in "\n" + r.stdout + r.stderr:
            first = next(l for l in (r.stdout + r.stderr).splitlines() if l.startswith("panic: "))
            return "KILLED\tthe test binary panicked: " + first
        return "BUILD-FAILED\t" + (r.stdout + r.stderr).strip().splitlines()[-1]
    return "SURVIVED"


def main():
    if not os.environ.get("GEMINUS_TEST_DATABASE_URL"):
        print("set GEMINUS_TEST_DATABASE_URL", file=sys.stderr)
        return 2
    src = {f: (REPO / f).read_text() for f in TESTS}
    bad = [n for n, f, reps in MUTANTS for old, _ in reps if src[f].count(old) != 1]
    if bad:
        print("patterns not found exactly once:", bad)
        return 2
    survived = total = 0
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, reps) in enumerate(MUTANTS):
            if ONLY and name not in ONLY:
                continue
            total += 1
            text = src[f]
            for old, new in reps:
                text = text.replace(old, new)
            m = pathlib.Path(tmp) / f"m{i}.go"
            m.write_text(text)
            o = pathlib.Path(tmp) / f"o{i}.json"
            o.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
            v = run(str(o), TESTS[f])
            survived += v == "SURVIVED"
            print(f"{name}\t{v}", flush=True)
    print(f"total={total} survived={survived}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
