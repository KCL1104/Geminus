#!/usr/bin/env python3
"""T-043 / D-045 SQL mutation check: each mutant weakens one rule of
migrations/0008_me.up.sql (the API's read of the user's own memberships)
and must make the SQL suites fail.

Each mutant gets a fresh database (dropped and created as DB_NAME, which
must start with dt_test_) on the disposable cluster that ADMIN_URL points
at; bootstrap, 0001-0007 and the mutated 0008 are applied, then the four
SQL suites run. The baseline also runs the Go tests of GET /v1/me on that
database, so the policies are shown to serve them. A mutant that does not
install is a harness error, never a kill. The repository is never modified.

Usage: ADMIN_URL='postgres://postgres@/postgres?host=...&port=...' DB_NAME=dt_test_d045_sqlmut \\
       python3 checks/evidence/2026-10-19-T-043/d045/sql-mutants.py [repo-root]
"""
import os
import pathlib
import subprocess
import sys
import tempfile
import urllib.parse

REPO = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[4])
M8 = "migrations/0008_me.up.sql"
BASE = ["db/bootstrap_roles.sql", "migrations/0001_core.up.sql", "migrations/0002_rls.up.sql", "migrations/0003_draft_issues.up.sql",
        "migrations/0004_provisioner.up.sql", "migrations/0005_ir_text.up.sql", "migrations/0006_version_comment.up.sql",
        "migrations/0007_job_states.up.sql", M8]
SUITES = ["db/tests/invariants.sql", "db/tests/constraints.sql", "db/tests/roles.sql", "db/tests/self.sql"]
GO = ["./internal/tenant/", "./internal/storage/memberships/", "./internal/tenant/me/", "./internal/api/"]

MEMBERSHIPS = "USING (twin.current_organization_id() IS NULL AND user_id::text = twin.current_actor_id());"
MUTANTS = [
    # The memberships policy
    ("memberships-any-user", MEMBERSHIPS, "USING (twin.current_organization_id() IS NULL);"),
    ("memberships-with-an-organization-selected", MEMBERSHIPS, "USING (user_id::text = twin.current_actor_id());"),
    ("memberships-for-all-commands", "ON twin.memberships FOR SELECT TO twin_api", "ON twin.memberships FOR ALL TO twin_api"),
    ("memberships-for-every-role", "ON twin.memberships FOR SELECT TO twin_api", "ON twin.memberships FOR SELECT"),
    ("memberships-restrictive", "POLICY own_memberships ON twin.memberships FOR", "POLICY own_memberships ON twin.memberships AS RESTRICTIVE FOR"),
    # The organizations policy
    ("organizations-any-member", "WHERE m.organization_id = organizations.id AND m.user_id::text = twin.current_actor_id()));",
     "WHERE m.organization_id = organizations.id));"),
    ("organizations-any-organization-of-the-user", "WHERE m.organization_id = organizations.id AND m.user_id", "WHERE m.user_id"),
    ("organizations-with-an-organization-selected", "USING (twin.current_organization_id() IS NULL AND EXISTS (", "USING (EXISTS ("),
    ("organizations-for-all-commands", "ON twin.organizations FOR SELECT TO twin_api", "ON twin.organizations FOR ALL TO twin_api"),
    ("organizations-for-the-worker", "ON twin.organizations FOR SELECT TO twin_api", "ON twin.organizations FOR SELECT TO twin_api, twin_worker"),
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


def run(admin, name, files, go=False):
    url = fresh(admin, name)
    for f in BASE:
        if psql(url, "-f", files.get(f, str(REPO / f))).returncode:
            return "INSTALL-FAILED"
    for t in SUITES:
        r = psql(url, "-f", str(REPO / t))
        if r.returncode:
            failed = [l for l in r.stderr.splitlines() if "FAILED" in l or "ERROR" in l]
            return "KILLED\t" + t + ": " + (failed[0].split("FAILED: ", 1)[-1][:160] if failed else "?")
    if go:
        env = dict(os.environ, GEMINUS_TEST_DATABASE_URL=url, GEMINUS_REQUIRE_DB_TESTS="1")
        r = subprocess.run(["go", "test", "-count=1", "-p", "1", *GO], cwd=REPO, env=env, capture_output=True, text=True)
        if r.returncode:
            print(r.stdout[-3000:], r.stderr[-2000:])
            return "GO-FAILED"
    return "SURVIVED"


def main() -> int:
    admin, name = os.environ.get("ADMIN_URL"), os.environ.get("DB_NAME", "")
    if not admin or not name.startswith("dt_test_"):
        print("set ADMIN_URL and DB_NAME=dt_test_*", file=sys.stderr)
        return 2
    src = (REPO / M8).read_text()
    bad = [n for n, old, _ in MUTANTS if src.count(old) != 1]
    if bad:
        print("patterns not found exactly once:", bad)
        return 2
    if (b := run(admin, name, {}, go=True)) != "SURVIVED":
        print("baseline fails:", b)
        return 2
    print("baseline\tSQL suites and the /me Go tests pass", flush=True)
    wrong = 0
    with tempfile.TemporaryDirectory() as tmp:
        for i, (n, old, new) in enumerate(MUTANTS):
            m = pathlib.Path(tmp) / f"m{i}.sql"
            m.write_text(src.replace(old, new))
            v = run(admin, name, {M8: str(m)})
            ok = v != "INSTALL-FAILED" and (v == "SURVIVED") == (n in EQUIVALENT)
            wrong += not ok
            print(f"{n}\t{v}" + ("" if ok else "\tUNEXPECTED"), flush=True)
    psql(admin, "-c", f"DROP DATABASE IF EXISTS {name}")
    print(f"total={len(MUTANTS)} unexpected={wrong}")
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
