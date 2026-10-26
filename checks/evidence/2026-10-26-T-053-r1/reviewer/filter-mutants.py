#!/usr/bin/env python3
"""Reviewer (T-053 r1): the application organization filter mutants.

Runs on a `git archive` copy (argument 1), never the worktree. Each mutant
replaces text occurring exactly once in internal/storage/aispend/aispend.go,
then runs the named tests of internal/storage/aispend against the dt_test_*
database (argument 2) as GEMINUS_REQUIRE_DB_TESTS=1; KILLED when they fail.
The file is restored after each mutant. No network, no environment value
read besides PATH/HOME/GO*.

    filter-mutants.py <copy> <dt_test_db> <test regexp>
"""
import os
import subprocess
import sys

F = "internal/storage/aispend/aispend.go"
MUTANTS = [
    ("D9", "settle's SELECT without the organization filter",
     "FROM twin.ai_spend_reservations WHERE organization_id = $1 AND id = $2 FOR UPDATE`, org, r.ID)",
     "FROM twin.ai_spend_reservations WHERE id = $2 AND $1::text IS NOT NULL FOR UPDATE`, org, r.ID)"),
    ("F1", "reserve's organization-day UPDATE without the organization filter",
     "WHERE organization_id = $4 AND day = $2 AND committed_nano_usd + $1 <= $3`",
     "WHERE $4::text IS NOT NULL AND day = $2 AND committed_nano_usd + $1 <= $3`"),
    ("F2", "TotalsAt reads the day without the organization filter",
     "coalesce((SELECT committed_nano_usd FROM twin.ai_spend_days WHERE organization_id = $3 AND day = $2), 0)`",
     "coalesce((SELECT committed_nano_usd FROM twin.ai_spend_days WHERE $3::text IS NOT NULL AND day = $2), 0)`"),
    ("F3", "settle's reservation UPDATE without the organization filter",
     "settled_at = now() WHERE organization_id = $1 AND id = $2`",
     "settled_at = now() WHERE $1::text IS NOT NULL AND id = $2`"),
]


def main():
    root, db, run = sys.argv[1], sys.argv[2], sys.argv[3]
    if not db.startswith("dt_test_"):
        sys.exit("refusing: not a dt_test_* database")
    env = {k: v for k, v in os.environ.items() if k in ("PATH", "HOME", "GOPATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "GOFLAGS")}
    env["GEMINUS_TEST_DATABASE_URL"] = f"postgres://postgres@/{db}?host=/var/lib/postgresql/geminus-t009-fresh&port=55440"
    env["GEMINUS_REQUIRE_DB_TESTS"] = "1"
    path = os.path.join(root, F)
    original = open(path).read()
    survived = 0
    try:
        for mid, what, old, new in MUTANTS:
            if original.count(old) != 1:
                print(f"{mid} INVALID (text occurs {original.count(old)} times): {what}", flush=True)
                survived += 1
                continue
            open(path, "w").write(original.replace(old, new))
            vet = subprocess.run(["go", "vet", "./internal/storage/aispend/"], cwd=root, env=env, capture_output=True, text=True)
            if vet.returncode != 0:
                print(f"{mid} INVALID (does not build): {what}\n{vet.stderr}", flush=True)
                survived += 1
                continue
            res = subprocess.run(["go", "test", "-count=1", "-v", "-run", run, "./internal/storage/aispend/"], cwd=root, env=env,
                                 capture_output=True, text=True)
            failed = sorted({l.split()[2] for l in res.stdout.splitlines() if l.startswith("--- FAIL:")})
            if res.returncode != 0:
                print(f"{mid} KILLED by {', '.join(failed) or 'the build or a panic'}: {what}", flush=True)
                for l in res.stdout.splitlines():
                    if "probe_test.go" in l or "aispend_test.go" in l:
                        print("    " + l.strip(), flush=True)
            else:
                print(f"{mid} SURVIVED: {what}", flush=True)
                survived += 1
            open(path, "w").write(original)
    finally:
        open(path, "w").write(original)
    sys.exit(1 if survived else 0)


if __name__ == "__main__":
    main()
