#!/usr/bin/env python3
"""T-007 mutation check: Go mutants on internal/tenant (go test -overlay) and SQL
mutants on 0002 (fresh ICU database each). The repository is never modified."""
import json, os, pathlib, subprocess, sys
REPO = pathlib.Path("/home/user/Geminus"); HERE = pathlib.Path(__file__).parent
TX = "internal/tenant/transaction.go"; RT = "internal/tenant/runtime.go"; RLS = "migrations/0002_rls.up.sql"
GO = [
    ("no-membership-check", TX, "\t\tif errors.Is(err, pgx.ErrNoRows) {\n\t\t\treturn ErrNoMembership\n\t\t}\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}",
     "\t\tif errors.Is(err, pgx.ErrNoRows) {\n\t\t\trole, err = Viewer, nil\n\t\t}\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}"),
    ("session-level-scope", TX, "set_config('app.organization_id', $1, true), set_config('app.user_id', $2, true)", "set_config('app.organization_id', $1, false), set_config('app.user_id', $2, false)"),
    ("guard-always-reuses", TX, "\treturn err == nil && org == \"\" && user == \"\" && sameRole", "\treturn true || (err == nil && org == \"\" && user == \"\" && sameRole)"),
    ("guard-ignores-role", TX, "\treturn err == nil && org == \"\" && user == \"\" && sameRole", "\treturn err == nil && org == \"\" && user == \"\" && (sameRole || true)"),
    ("worker-ignores-assignment", TX, "\tif !slices.Contains(w.assigned, org) {", "\tif false {"),
    ("no-id-validation", TX, "\tif !orgID.MatchString(s.OrganizationID) || !userID.MatchString(s.UserID) {", "\tif s.OrganizationID == \"\" && s.UserID == \"\" {"),
    ("role-check-ignores-owner", RT, '{owner, "is a member of " + ownerRole},', '{false, "is a member of " + ownerRole},'),
    ("role-check-ignores-other", RT, '{hasOther, "is also a member of " + other},', '{false, "is also a member of " + other},'),
    ("role-check-ignores-superuser", RT, '{super, "is a superuser"},', '{false, "is a superuser"},'),
]
SQL = [
    ("worker-updates-all-imports", RLS, "GRANT UPDATE (status,total_rows,invalid_rows,preview,validation,mapping_suggestion_status,\n  mapping_suggestion,error,error_report_object_key) ON twin.imports TO twin_worker;", "GRANT UPDATE ON twin.imports TO twin_worker;"),
    ("worker-updates-all-jobs", RLS, "GRANT UPDATE (status,attempt,available_at,locked_at,locked_by,lease_token,lease_expires_at,\n  heartbeat_at,error) ON twin.jobs TO twin_worker;", "GRANT UPDATE ON twin.jobs TO twin_worker;"),
    ("no-commit-guard", RLS, "CREATE TRIGGER guard_worker_import BEFORE UPDATE ON twin.imports\n  FOR EACH ROW EXECUTE FUNCTION twin.guard_worker_import();", ""),
    ("guard-public-execute", RLS, "REVOKE ALL ON FUNCTION twin.guard_worker_import() FROM PUBLIC; -- runs as a trigger, never called directly\n", ""),
    ("api-may-lock-organizations", RLS, "GRANT DELETE ON twin.memberships TO twin_api;", "GRANT DELETE ON twin.memberships TO twin_api;\nGRANT UPDATE ON twin.organizations TO twin_api;"),
    ("no-force-rls-on-jobs", RLS, "    EXECUTE format('ALTER TABLE twin.%I FORCE ROW LEVEL SECURITY',t);", "    IF t <> 'jobs' THEN EXECUTE format('ALTER TABLE twin.%I FORCE ROW LEVEL SECURITY',t); END IF;"),
]
def fresh():
    return subprocess.run([str(HERE / "freshdb.sh"), "dt_test_t007mut", "icu"], capture_output=True, text=True, check=True).stdout.strip().splitlines()[-1]
def install(url, override):
    for f in ["db/bootstrap_roles.sql", "migrations/0001_core.up.sql", RLS]:
        p = override.get(f, str(REPO / f))
        if subprocess.run(["psql", "-X", "-q", url, "-v", "ON_ERROR_STOP=1", "-f", p], capture_output=True).returncode:
            return False
    return True
def main():
    res = []
    url = fresh(); assert install(url, {})
    for name, rel, old, new in GO:
        src = (REPO / rel).read_text()
        if src.count(old) != 1: res.append((name, "BAD-MUTANT")); continue
        m = HERE / f"go-{name}.go"; m.write_text(src.replace(old, new))
        ov = HERE / f"go-{name}.json"; ov.write_text(json.dumps({"Replace": {str(REPO / rel): str(m)}}))
        env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1", GEMINUS_TEST_DATABASE_URL=url)
        p = subprocess.run(["go", "test", "-count=1", "-timeout", "120s", "-overlay", str(ov), "./internal/tenant/"], cwd=REPO, capture_output=True, text=True, env=env)
        res.append((name, "SURVIVED" if p.returncode == 0 else "KILLED"))
    for name, rel, old, new in SQL:
        src = (REPO / rel).read_text()
        if src.count(old) != 1: res.append((name, "BAD-MUTANT")); continue
        m = HERE / f"sql-{name}.sql"; m.write_text(src.replace(old, new))
        url = fresh()
        if not install(url, {rel: str(m)}): res.append((name, "NO-INSTALL")); continue
        rc = 0
        for t in ["db/tests/invariants.sql", "db/tests/constraints.sql", "db/tests/roles.sql"]:
            rc |= subprocess.run(["psql", "-X", "-q", url, "-v", "ON_ERROR_STOP=1", "-f", str(REPO / t)], capture_output=True).returncode
        res.append((name, "SURVIVED" if rc == 0 else "KILLED"))
    for r in res: print("\t".join(r))
    k = sum(1 for r in res if r[1] == "KILLED")
    print(f"{k}/{len(res)} killed")
    sys.exit(0 if k == len(res) else 1)
main()
