#!/usr/bin/env python3
"""Reviewer (T-053 r1): SQL mutants of migration 0016 (the author ran none).

Runs in a `git archive HEAD` copy (argument 1), never the worktree. For each
mutant: replace text occurring exactly once in migrations/0016_ai_spend.up.sql,
create a fresh dt_test_* database (prefix argument 2 + mutant id), run
`make db-test` on it (all migrations, the SQL suites, down/up/down/up), and
report KILLED when it fails. Then drop that database and restore the file.

    sql-mutants.py <copy> <dbprefix>
"""
import os
import subprocess
import sys

F = "migrations/0016_ai_spend.up.sql"
SOCK = "/var/lib/postgresql/geminus-t009-fresh"
MUTANTS = [
    ("S1", "reservations without FORCE RLS",
     "ALTER TABLE twin.ai_spend_reservations FORCE ROW LEVEL SECURITY;\n", ""),
    ("S2", "a settled reservation may be settled again",
     "  IF OLD.state <> 'reserved' THEN\n", "  IF false AND OLD.state <> 'reserved' THEN\n"),
    ("S3", "the worker may delete reservations",
     "GRANT SELECT, INSERT ON twin.ai_spend_periods, twin.ai_spend_days, twin.ai_spend_reservations TO twin_worker;",
     "GRANT SELECT, INSERT ON twin.ai_spend_periods, twin.ai_spend_days, twin.ai_spend_reservations TO twin_worker;\nGRANT DELETE ON twin.ai_spend_reservations TO twin_worker;"),
    ("S4", "the day check counts a settled reservation at its reserved amount",
     "CASE WHEN state = 'reserved' THEN reserved_nano_usd ELSE settled_nano_usd END",
     "reserved_nano_usd"),
    ("S5", "the deployment totals open to an unscoped transaction",
     "USING (twin.current_organization_id() IS NOT NULL) WITH CHECK (twin.current_organization_id() IS NOT NULL);",
     "USING (true) WITH CHECK (true);"),
    ("S6", "the day check is not deferred (immediate)",
     "CREATE CONSTRAINT TRIGGER check_ai_spend_day AFTER INSERT OR UPDATE ON twin.ai_spend_reservations\n  DEFERRABLE INITIALLY DEFERRED",
     "CREATE CONSTRAINT TRIGGER check_ai_spend_day AFTER INSERT OR UPDATE ON twin.ai_spend_reservations\n  DEFERRABLE INITIALLY IMMEDIATE"),
    ("S8", "a reservation may be inserted with any created_at",
     "    IF NEW.created_at IS DISTINCT FROM now() THEN\n", "    IF false AND NEW.created_at IS DISTINCT FROM now() THEN\n"),
]


def sh(args, **kw):
    return subprocess.run(args, capture_output=True, text=True, **kw)


def main():
    root, prefix = sys.argv[1], sys.argv[2]
    if not prefix.startswith("dt_test_"):
        sys.exit("refusing: not a dt_test_* prefix")
    path = os.path.join(root, F)
    original = open(path).read()
    env = {k: v for k, v in os.environ.items() if k in ("PATH", "HOME", "GOTOOLCHAIN")}
    survived = 0
    try:
        for mid, what, old, new in MUTANTS:
            if original.count(old) != 1:
                print(f"{mid} INVALID (text occurs {original.count(old)} times): {what}", flush=True)
                survived += 1
                continue
            if old == new:
                print(f"{mid} SKIPPED (no such rule to mutate): {what}", flush=True)
                continue
            db = f"{prefix}{mid.lower()}"
            sh(["su", "postgres", "-c", f"dropdb --if-exists -h {SOCK} -p 55440 {db}"])
            c = sh(["su", "postgres", "-c", f"createdb -h {SOCK} -p 55440 -E UTF8 -T template0 --locale=C.UTF-8 {db}"])
            if c.returncode != 0:
                print(f"{mid} ERROR creating {db}: {c.stderr.strip()}", flush=True)
                survived += 1
                continue
            open(path, "w").write(original.replace(old, new))
            e = dict(env, DATABASE_URL=f"postgres://postgres@/{db}?host={SOCK}&port=55440", DT_ALLOW_DB_TESTS="yes")
            r = sh(["make", "db-test"], cwd=root, env=e)
            out = r.stdout + r.stderr
            first = next((l for l in out.splitlines() if "FAILED" in l or "ERROR" in l), "")
            if r.returncode != 0:
                print(f"{mid} KILLED: {what}\n    {first.strip()[:300]}", flush=True)
            else:
                print(f"{mid} SURVIVED: {what}", flush=True)
                survived += 1
            open(path, "w").write(original)
            sh(["su", "postgres", "-c", f"dropdb --if-exists -h {SOCK} -p 55440 {db}"])
    finally:
        open(path, "w").write(original)
    sys.exit(1 if survived else 0)


if __name__ == "__main__":
    main()
