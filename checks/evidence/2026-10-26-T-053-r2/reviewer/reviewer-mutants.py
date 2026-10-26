#!/usr/bin/env python3
"""T-053 review round 2 (H-003): the reviewer's own mutants (N-series).

Runs the round-1 harness's engine (checks/evidence/2026-10-26-T-053-r1/mutants.py:
one exact replacement per mutant, go vet, go test -count=1 -p 1 on the named
packages, every file restored and its hash compared) with mutants that the
round-1 list does not have: edges of the spend record (H1), of the overrun
flags (M3, L1) and of the M3 failover rule. Run it in a `git archive` copy,
never a checkout:

    python3 reviewer-mutants.py <copy> [--only N1,N2] > log
"""
import importlib.util
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location(
    "r1mutants", os.path.join(HERE, "..", "..", "2026-10-26-T-053-r1", "mutants.py"))
r1 = importlib.util.module_from_spec(spec)
spec.loader.exec_module(r1)

A = "./internal/ai/provider/anthropic/"
M = "./internal/ai/provider/"
E = "./internal/aievals/"
AP = "./internal/config/aiapproval/"
L = "./internal/storage/aispend/"
REC = "internal/aievals/record.go"
MAIN = "internal/aievals/main.go"

r1.MUTANTS = [
    ("N1", "a close line may be dated before its open", REC,
     [("\tcase end.Before(start):\n", "\tcase false && end.Before(start):\n")], [E, AP], False, False),
    ("N2", "a negative settled amount is taken", REC,
     [("case c.SettledNanoUSD < 0 || c.CommittedNanoUSD < c.SettledNanoUSD:", "case c.CommittedNanoUSD < c.SettledNanoUSD:")], [E, AP], False, False),
    ("N3", "the day's sum is not saturating (wraps on huge lines)", REC,
     [("day = addSat(day, amount)", "day = day + amount")], [E, AP], False, False),
    ("N4", "a run may open twice", REC,
     [("if _, dup := r.runs[o.RunID]; dup {", "if _, dup := r.runs[o.RunID]; dup && false {")], [E, AP], False, False),
    ("N5", "a zero cap is taken", REC,
     [("case o.CapNanoUSD <= 0:", "case o.CapNanoUSD < 0:")], [E, AP], False, False),
    ("N6", "a last line without its newline is taken", REC,
     [("if len(data) > 0 && data[len(data)-1] != '\\n' {", "if false {")], [E, AP], False, False),
    ("N7", "Main sums the record for tomorrow, not the run's day", MAIN,
     [("day, month := rec.Spent(start)", "day, month := rec.Spent(start.Add(24 * time.Hour))")], [E, AP], False, False),
    ("N8", "the report's existence is not checked before the backend is built", MAIN,
     [("if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {", "if _, err := os.Lstat(path); false && (err == nil || !errors.Is(err, os.ErrNotExist)) {")],
     [E, AP], False, False),
    ("N9", "a call at exactly its reservation is flagged as over it", "internal/ai/provider/meter.go",
     [("s.NanoUSD > m.res.Charge.NanoUSD\n", "s.NanoUSD >= m.res.Charge.NanoUSD\n")], [M, A], False, False),
    ("N10", "the ledger flags a settlement at exactly its reservation", "internal/storage/aispend/aispend.go",
     [("if over = s.NanoUSD > reserved; over {", "if over = s.NanoUSD >= reserved; over {")], [L], True, False),
    ("N11", "a refusal after a possibly billed attempt does not mark the credential", "internal/ai/provider/anthropic/anthropic.go",
     [("\t\t\tc.refuse(status, b.now())\n\t\t\tif t.unknown.Load() > unknownBefore {\n",
       "\t\t\tif t.unknown.Load() > unknownBefore {\n"),
      ("\t\t\tfailed = append(failed, c.label)\n\t\t\tcontinue\n\t\tcase status == http.StatusTooManyRequests:",
       "\t\t\tc.refuse(status, b.now())\n\t\t\tfailed = append(failed, c.label)\n\t\t\tcontinue\n\t\tcase status == http.StatusTooManyRequests:")],
     [A], False, False),
]

if __name__ == "__main__":
    sys.exit(r1.main())
