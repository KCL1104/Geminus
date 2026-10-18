#!/usr/bin/env python3
"""T-010 / D-042 SQL mutation check: each mutant weakens one rule of
migrations/0004_provisioner.up.sql and must make db/tests/roles.sql fail.

Each mutant gets a fresh database (dropped and created as DB_NAME, which
must start with dt_test_) on the disposable cluster that ADMIN_URL points
at; bootstrap, 0001-0003 and the mutated 0004 are applied, then the three
SQL suites run. A mutant that does not install is a harness error, never a
kill. The repository is never modified.

Usage: ADMIN_URL='postgres://postgres@/postgres?host=...&port=...' DB_NAME=dt_test_sqlmut \\
       python3 checks/evidence/2026-10-18-T-010/sql-mutants.py [repo-root]
"""
import os
import pathlib
import subprocess
import sys
import tempfile
import urllib.parse

REPO = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[3])
M4 = "migrations/0004_provisioner.up.sql"
BASE = ["db/bootstrap_roles.sql", "migrations/0001_core.up.sql", "migrations/0002_rls.up.sql", "migrations/0003_draft_issues.up.sql", M4]
SUITES = ["db/tests/invariants.sql", "db/tests/constraints.sql", "db/tests/roles.sql"]

MUTANTS = [
    ("users-not-readable", "GRANT SELECT, INSERT ON twin.users TO twin_provisioner;", "GRANT INSERT ON twin.users TO twin_provisioner;"),
    ("memberships-not-insertable", "GRANT INSERT ON twin.organizations,twin.memberships,twin.audit_events TO twin_provisioner;", "GRANT INSERT ON twin.organizations,twin.audit_events TO twin_provisioner;"),
    ("extra-update", "GRANT INSERT ON twin.organizations,twin.memberships,twin.audit_events TO twin_provisioner;", "GRANT INSERT ON twin.organizations,twin.memberships,twin.audit_events TO twin_provisioner;\nGRANT UPDATE ON twin.memberships TO twin_provisioner;"),
    ("extra-select", "GRANT SELECT, INSERT ON twin.users TO twin_provisioner;", "GRANT SELECT, INSERT ON twin.users TO twin_provisioner;\nGRANT SELECT ON twin.memberships TO twin_provisioner;"),
    ("schema-create", "GRANT USAGE ON SCHEMA twin TO twin_provisioner;", "GRANT USAGE, CREATE ON SCHEMA twin TO twin_provisioner;"),
    ("api-writes-worker", "IF (NEW.actor_type='worker' AND NOT worker) OR (NEW.actor_type='system'", "IF (NEW.actor_type='system'"),
    ("api-writes-system", " OR (NEW.actor_type='system' AND NOT (worker OR provisioner)) THEN", " THEN"),
    ("provisioner-writes-worker", "(NEW.actor_type='worker' AND NOT worker)", "(NEW.actor_type='worker' AND NOT (worker OR provisioner))"),
    ("provisioner-writes-user", "IF NEW.actor_type='user' AND (worker OR provisioner OR NEW.actor_id", "IF NEW.actor_type='user' AND (worker OR NEW.actor_id"),
    ("worker-writes-user", "IF NEW.actor_type='user' AND (worker OR provisioner OR NEW.actor_id", "IF NEW.actor_type='user' AND (provisioner OR NEW.actor_id"),
    ("user-any-actor", " OR NEW.actor_id::text IS DISTINCT FROM twin.current_actor_id()) THEN", ") THEN"),
    ("no-search-path", "  SET search_path = pg_catalog, pg_temp AS $$", "  AS $$"),
]
EQUIVALENT: dict[str, str] = {}


def psql(url, *args):
    return subprocess.run(["psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", url, *args], capture_output=True, text=True)


def fresh(admin, name):
    for sql in (f"DROP DATABASE IF EXISTS {name}", f"CREATE DATABASE {name}"):
        r = psql(admin, "-c", sql)
        if r.returncode:
            raise SystemExit(r.stderr)
    u = urllib.parse.urlsplit(admin)
    return urllib.parse.urlunsplit((u.scheme, u.netloc, "/" + name, u.query, u.fragment))


def run(admin, name, files):
    url = fresh(admin, name)
    for f in BASE:
        if psql(url, "-f", files.get(f, str(REPO / f))).returncode:
            return "INSTALL-FAILED"
    for t in SUITES:
        if psql(url, "-f", str(REPO / t)).returncode:
            return "KILLED"
    return "SURVIVED"


def main() -> int:
    admin, name = os.environ.get("ADMIN_URL"), os.environ.get("DB_NAME", "")
    if not admin or not name.startswith("dt_test_"):
        print("set ADMIN_URL and DB_NAME=dt_test_*", file=sys.stderr)
        return 2
    src = (REPO / M4).read_text()
    bad = [n for n, old, _ in MUTANTS if src.count(old) != 1]
    if bad:
        print("patterns not found exactly once:", bad)
        return 2
    if run(admin, name, {}) != "SURVIVED":
        print("baseline fails")
        return 2
    wrong = 0
    with tempfile.TemporaryDirectory() as tmp:
        for i, (n, old, new) in enumerate(MUTANTS):
            m = pathlib.Path(tmp) / f"m{i}.sql"
            m.write_text(src.replace(old, new))
            v = run(admin, name, {M4: str(m)})
            ok = (v == "SURVIVED") == (n in EQUIVALENT)
            wrong += not ok
            print(f"{n}\t{v}" + ("" if ok else "\tUNEXPECTED"), flush=True)
    psql(admin, "-c", f"DROP DATABASE IF EXISTS {name}")
    print(f"total={len(MUTANTS)} unexpected={wrong}")
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
