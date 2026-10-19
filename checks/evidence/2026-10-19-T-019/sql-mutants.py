#!/usr/bin/env python3
"""T-019 SQL mutation check: each mutant weakens one rule of
migrations/0007_job_states.up.sql (a job's lifecycle) and must make the SQL
suites fail.

Each mutant gets a fresh database (dropped and created as DB_NAME, which
must start with dt_test_) on the disposable cluster that ADMIN_URL points
at; bootstrap, 0001-0006 and the mutated 0007 are applied, then the three
SQL suites run. The baseline also runs the queue's and the loop's Go tests
on that database, so the guard is shown not to refuse what they write. A
mutant that does not install is a harness error, never a kill. The
repository is never modified.

Usage: ADMIN_URL='postgres://postgres@/postgres?host=...&port=...' DB_NAME=dt_test_sqlmut \\
       python3 checks/evidence/2026-10-19-T-019/sql-mutants.py [repo-root]
"""
import os
import pathlib
import subprocess
import sys
import tempfile
import urllib.parse

REPO = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[3])
M7 = "migrations/0007_job_states.up.sql"
BASE = ["db/bootstrap_roles.sql", "migrations/0001_core.up.sql", "migrations/0002_rls.up.sql", "migrations/0003_draft_issues.up.sql",
        "migrations/0004_provisioner.up.sql", "migrations/0005_ir_text.up.sql", "migrations/0006_version_comment.up.sql", M7]
SUITES = ["db/tests/invariants.sql", "db/tests/constraints.sql", "db/tests/roles.sql"]
GO = ["./internal/storage/jobs/", "./internal/worker/", "./cmd/worker/"]

MUTANTS = [
    ("insert-any-status", "IF NEW.status<>'queued' OR NEW.attempt<>0", "IF NEW.attempt<>0"),
    ("insert-with-an-attempt", "OR NEW.attempt<>0 OR", "OR"),
    ("insert-with-a-token", " OR NEW.lease_token<>0 OR", " OR"),
    ("insert-with-an-error", " OR NEW.error IS NOT NULL THEN", " THEN"),
    ("no-insert-trigger", "BEFORE INSERT OR UPDATE ON twin.jobs", "BEFORE UPDATE ON twin.jobs"),
    ("no-update-trigger", "BEFORE INSERT OR UPDATE ON twin.jobs", "BEFORE INSERT ON twin.jobs"),
    ("unchanged-row-refused", "  IF NEW IS NOT DISTINCT FROM OLD THEN\n    RETURN NEW;\n  END IF;\n", ""),
    ("finished-not-final", "  IF OLD.status IN ('completed','failed','cancelled') THEN", "  IF false THEN"),
    ("queued-straight-to-an-end", "NEW.status NOT IN ('queued','running','cancelled') THEN", "NEW.status NOT IN ('queued','running','cancelled','completed','failed') THEN"),
    ("payload-mutable", "NEW.type,NEW.payload,", "NEW.type,OLD.payload,"),
    ("max-attempts-mutable", "NEW.max_attempts,NEW.created_at)", "OLD.max_attempts,NEW.created_at)"),
    ("created-at-mutable", "NEW.max_attempts,NEW.created_at)", "NEW.max_attempts,OLD.created_at)"),
    ("attempt-goes-back", "IF NEW.attempt<OLD.attempt OR NEW.lease_token<OLD.lease_token THEN", "IF NEW.lease_token<OLD.lease_token THEN"),
    ("token-goes-back", "IF NEW.attempt<OLD.attempt OR NEW.lease_token<OLD.lease_token THEN", "IF NEW.attempt<OLD.attempt THEN"),
    ("claim-any-attempt", "(NEW.attempt<>OLD.attempt+1 OR NEW.lease_token<=OLD.lease_token)", "(NEW.lease_token<=OLD.lease_token)"),
    ("claim-same-token", "(NEW.attempt<>OLD.attempt+1 OR NEW.lease_token<=OLD.lease_token)", "(NEW.attempt<>OLD.attempt+1)"),
    ("no-search-path", "LANGUAGE plpgsql\n  SET search_path = pg_catalog, pg_temp AS $$", "LANGUAGE plpgsql\n  AS $$"),
    # Identity (review: rv-*-mutable), one column at a time
    ("rv-id-mutable", "ROW(NEW.id,NEW.organization_id,NEW.type,", "ROW(OLD.id,NEW.organization_id,NEW.type,"),
    ("rv-org-mutable", "ROW(NEW.id,NEW.organization_id,NEW.type,", "ROW(NEW.id,OLD.organization_id,NEW.type,"),
    ("rv-type-mutable", "ROW(NEW.id,NEW.organization_id,NEW.type,", "ROW(NEW.id,NEW.organization_id,OLD.type,"),
    ("rv-run-id-mutable", "NEW.payload,NEW.simulation_run_id,", "NEW.payload,OLD.simulation_run_id,"),
    ("proposal-id-mutable", "NEW.simulation_run_id,NEW.ai_proposal_id,", "NEW.simulation_run_id,OLD.ai_proposal_id,"),
    ("rv-import-id-mutable", "NEW.ai_proposal_id,NEW.import_id,", "NEW.ai_proposal_id,OLD.import_id,"),
    ("analysis-id-mutable", "\n         NEW.ai_analysis_id,", "\n         OLD.ai_analysis_id,"),
    # A queued job has an attempt left
    ("queued-without-attempts", "  IF NEW.status='queued' AND NEW.attempt>=NEW.max_attempts THEN", "  IF false THEN"),
    # Each runtime role only its own way (review L3)
    ("owner-restricted-as-api", "  IF NOT pg_catalog.pg_has_role(current_user,'twin_owner','USAGE') THEN", "  IF true THEN"),
    ("roles-unrestricted", "  IF NOT pg_catalog.pg_has_role(current_user,'twin_owner','USAGE') THEN", "  IF false THEN"),
    ("api-any-move", "    IF pg_catalog.pg_has_role(current_user,'twin_api','USAGE')\n       AND NOT", "    IF false\n       AND NOT"),
    ("api-moves-a-running-job", "       AND NOT (OLD.status='queued' AND NEW.status IN ('queued','cancelled')", "       AND NOT (NEW.status IN ('queued','cancelled')"),
    ("api-claims", "NEW.status IN ('queued','cancelled')\n", "NEW.status IN ('queued','running','cancelled')\n"),
    ("api-touches-the-lease", "AND ROW(NEW.attempt,NEW.lease_token,NEW.locked_at,NEW.locked_by,NEW.lease_expires_at,NEW.heartbeat_at,NEW.error)", "AND ROW(OLD.attempt,OLD.lease_token,OLD.locked_at,OLD.locked_by,OLD.lease_expires_at,OLD.heartbeat_at,OLD.error)"),
    ("worker-any-move", "    IF pg_catalog.pg_has_role(current_user,'twin_worker','USAGE') AND OLD.status='queued' AND NEW.status<>'running' THEN", "    IF false THEN"),
]
EQUIVALENT: dict[str, str] = {
    "api-claims": "a claim changes attempt and lease_token (the claim rule requires attempt+1 and a new token), which the API's own check keeps unchanged; leaving running out of the API's statuses only states the rule twice",
}


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
        if psql(url, "-f", str(REPO / t)).returncode:
            return "KILLED"
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
    src = (REPO / M7).read_text()
    bad = [n for n, old, _ in MUTANTS if src.count(old) != 1]
    if bad:
        print("patterns not found exactly once:", bad)
        return 2
    if (b := run(admin, name, {}, go=True)) != "SURVIVED":
        print("baseline fails:", b)
        return 2
    print("baseline\tSQL suites and Go tests pass", flush=True)
    wrong = 0
    with tempfile.TemporaryDirectory() as tmp:
        for i, (n, old, new) in enumerate(MUTANTS):
            m = pathlib.Path(tmp) / f"m{i}.sql"
            m.write_text(src.replace(old, new))
            v = run(admin, name, {M7: str(m)})
            ok = v != "INSTALL-FAILED" and (v == "SURVIVED") == (n in EQUIVALENT)
            wrong += not ok
            print(f"{n}\t{v}" + ("" if ok else "\tUNEXPECTED"), flush=True)
    psql(admin, "-c", f"DROP DATABASE IF EXISTS {name}")
    print(f"total={len(MUTANTS)} unexpected={wrong}")
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
