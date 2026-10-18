#!/usr/bin/env python3
"""T-010 mutation run: each mutant removes or weakens one rule of membership
management (internal/tenant/memberships, internal/tenant/owners.go,
internal/storage/memberships) or of provisioning (internal/storage/
provisioning, cmd/provision-tenant) and must make a test fail.

Applied with `go test -overlay` (the working tree is never modified) to the
packages below, one at a time (`-p 1`, as in `make db-go-test`), against the
disposable database of `make db-test` (GEMINUS_TEST_DATABASE_URL;
GEMINUS_REQUIRE_DB_TESTS=1 makes a skip a failure, so a run without the
database cannot pass for a kill).

KILLED means a test failed. A mutant that does not build or a run that
timed out is a harness error, never a kill. EQUIVALENT lists mutants that
cannot change behaviour, with the reason; they must survive.

Usage: GEMINUS_TEST_DATABASE_URL=... python3 checks/evidence/2026-10-18-T-010/mutants.py [--check] [--only=NAME ...] [repo-root]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

ONLY = {a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--only=")}
ARGS = [a for a in sys.argv[1:] if a != "--check" and not a.startswith("--only=")]
CHECK_ONLY = "--check" in sys.argv[1:]
REPO = pathlib.Path(ARGS[0] if ARGS else pathlib.Path(__file__).resolve().parents[3])
PKGS = ["./internal/tenant/...", "./internal/storage/memberships/...", "./internal/storage/provisioning/...", "./internal/api/...", "./cmd/provision-tenant/..."]
OWN = "internal/tenant/owners.go"
SVC = "internal/tenant/memberships/memberships.go"
HTTP = "internal/tenant/memberships/http.go"
ST = "internal/storage/memberships/memberships.go"
PROV = "internal/storage/provisioning/provisioning.go"
CMD = "cmd/provision-tenant/main.go"

MUTANTS = [
    # RunOwners: the D-036 lock before the caller's role (ADR-023)
    ("owners-scope-unchecked", OWN, "\tif !orgID.MatchString(s.OrganizationID) || !userID.MatchString(s.UserID) {\n\t\treturn ErrInvalidScope\n\t}\n\treturn d.db.Tx(ctx, pgx.TxOptions{}", "\treturn d.db.Tx(ctx, pgx.TxOptions{}"),
    ("owners-unordered", OWN, "\t\t\t\tORDER BY user_id FOR UPDATE`, s.OrganizationID)", "\t\t\t\tFOR UPDATE`, s.OrganizationID)"),
    ("owners-not-locked", OWN, "\t\t\t\tORDER BY user_id FOR UPDATE`, s.OrganizationID)", "\t\t\t\tORDER BY user_id`, s.OrganizationID)"),
    ("owners-any-role", OWN, "WHERE organization_id=$1 AND role='owner'\n", "WHERE organization_id=$1 AND role IS NOT NULL\n"),
    ("owners-non-member-allowed", OWN, "\t\t\tif errors.Is(err, pgx.ErrNoRows) {\n\t\t\t\treturn ErrNoMembership\n\t\t\t}", "\t\t\tif errors.Is(err, pgx.ErrNoRows) {\n\t\t\t\trole = Owner\n\t\t\t\terr = nil\n\t\t\t}"),
    ("owners-no-relock", OWN, "\t\t\t\tif attempt == 0 {\n\t\t\t\t\tcontinue\n\t\t\t\t}", "\t\t\t\tif attempt == 0 {\n\t\t\t\t\treturn fn(ctx, tx, Membership{OrganizationID: s.OrganizationID, UserID: s.UserID, Role: role}, owners)\n\t\t\t\t}"),
    ("owners-no-scope", OWN, "\t\tif err := setScope(ctx, tx, s.OrganizationID, s.UserID); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tfor attempt", "\t\tfor attempt"),
    # roles
    ("manage-roles-members", SVC, "var manageRoles = []tenant.Role{tenant.Owner}", "var manageRoles = tenant.Members"),
    ("list-no-role", SVC, "\t\tif err := m.Require(manageRoles...); err != nil {\n\t\t\treturn err\n\t\t}\n\t\trows, err := store.List", "\t\trows, err := store.List"),
    ("create-no-role", SVC, "\t\tif err := m.Require(manageRoles...); err != nil {\n\t\t\treturn err\n\t\t}\n\t\treplay, err", "\t\treplay, err"),
    # create
    ("create-input-unchecked", SVC, "\tif !ValidUserID(req.UserID) || !validRole(req.Role) {\n\t\treturn 0, nil, ErrInvalidRequest\n\t}", "\tif false {\n\t\treturn 0, nil, ErrInvalidRequest\n\t}"),
    ("create-no-replay", SVC, "\t\tif replay != nil {\n\t\t\tstatus = replay.Status", "\t\tif false && replay != nil {\n\t\t\tstatus = replay.Status"),
    ("create-exists-as-other", SVC, "\t\tcase errors.Is(err, store.ErrExists):\n\t\t\treturn ErrExists", "\t\tcase errors.Is(err, store.ErrExists):\n\t\t\treturn ErrUnknownUser"),
    ("create-no-audit", SVC, "\t\t\tAction: \"membership.create\",", "\t\t\tAction: \"membership.created\","),
    ("create-status-200", SVC, "\t\tstatus = 201\n", "\t\tstatus = 200\n"),
    # update and delete: D-036
    ("update-role-unchecked", SVC, "\tif !validRole(req.Role) {\n\t\treturn Membership{}, ErrInvalidRequest\n\t}", "\tif false {\n\t\treturn Membership{}, ErrInvalidRequest\n\t}"),
    ("update-last-owner-allowed", SVC, "\t\tif cur.Role == string(tenant.Owner) && len(owners) == 1 {\n\t\t\treturn ErrLastOwner\n\t\t}\n\t\tnext, err", "\t\tnext, err"),
    ("update-no-op-written", SVC, "\t\tif cur.Role == string(req.Role) {\n\t\t\tout = public(cur)\n\t\t\treturn nil\n\t\t}", ""),
    ("update-no-audit", SVC, "\t\t\tAction: \"membership.update\",", "\t\t\tAction: \"membership.updated\","),
    ("update-target-unlocked", SVC, "\t\tcur, err := store.Get(ctx, tx, m.OrganizationID, user, true)\n\t\tif errors.Is(err, store.ErrNotFound) {\n\t\t\treturn ErrNotFound\n\t\t}\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif cur.Role == string(req.Role) {", "\t\tcur, err := store.Get(ctx, tx, m.OrganizationID, user, false)\n\t\tif errors.Is(err, store.ErrNotFound) {\n\t\t\treturn ErrNotFound\n\t\t}\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif cur.Role == string(req.Role) {"),
    ("delete-last-owner-allowed", SVC, "\t\tif cur.Role == string(tenant.Owner) && len(owners) == 1 {\n\t\t\treturn ErrLastOwner\n\t\t}\n\t\tif err := store.Delete", "\t\tif err := store.Delete"),
    ("delete-no-audit", SVC, "\t\t\tAction: \"membership.delete\",", "\t\t\tAction: \"membership.deleted\","),
    ("delete-not-found-hidden", SVC, "\t\tcur, err := store.Get(ctx, tx, m.OrganizationID, user, true)\n\t\tif errors.Is(err, store.ErrNotFound) {\n\t\t\treturn ErrNotFound\n\t\t}\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif cur.Role == string(tenant.Owner) && len(owners) == 1 {\n\t\t\treturn ErrLastOwner\n\t\t}\n\t\tif err := store.Delete", "\t\tcur, err := store.Get(ctx, tx, m.OrganizationID, user, true)\n\t\tif errors.Is(err, store.ErrNotFound) {\n\t\t\treturn nil\n\t\t}\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif cur.Role == string(tenant.Owner) && len(owners) == 1 {\n\t\t\treturn ErrLastOwner\n\t\t}\n\t\tif err := store.Delete"),
    # list and cursor
    ("list-limit-unchecked", SVC, "\tif limit < 1 || limit > maxLimit {\n\t\treturn Page{}, ErrInvalidLimit", "\tif false {\n\t\treturn Page{}, ErrInvalidLimit"),
    ("list-page-short", SVC, "\t\trows, err := store.List(ctx, tx, m.OrganizationID, after, limit+1)", "\t\trows, err := store.List(ctx, tx, m.OrganizationID, after, limit)"),
    ("cursor-any-org", SVC, "\tif c.Version != 1 || c.Org != org ||", "\tif c.Version != 1 ||"),
    ("cursor-not-canonical", SVC, " || encodeCursor(c.Org, c.After) != s {", " {"),
    # SQL
    ("sql-get-any-org", ST, "\tq := `SELECT role, created_at FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`", "\tq := `SELECT role, created_at FROM twin.memberships WHERE $1::text IS NOT NULL AND user_id=$2`"),
    ("sql-list-any-org", ST, "FROM twin.memberships WHERE organization_id=$1\n\t\t\tORDER BY user_id LIMIT $2`", "FROM twin.memberships WHERE $1::text IS NOT NULL\n\t\t\tORDER BY user_id LIMIT $2`"),
    ("sql-list-after-inclusive", ST, "\t\t\tAND user_id > $2 ORDER BY user_id LIMIT $3`", "\t\t\tAND user_id >= $2 ORDER BY user_id LIMIT $3`"),
    ("sql-setrole-any-org", ST, "`UPDATE twin.memberships SET role=$3 WHERE organization_id=$1 AND user_id=$2 RETURNING created_at`", "`UPDATE twin.memberships SET role=$3 WHERE $1::text IS NOT NULL AND user_id=$2 RETURNING created_at`"),
    ("sql-delete-any-org", ST, "`DELETE FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`", "`DELETE FROM twin.memberships WHERE $1::text IS NOT NULL AND user_id=$2`"),
    ("sql-delete-rows-unchecked", ST, "\tif err == nil && tag.RowsAffected() != 1 {\n\t\treturn ErrNotFound\n\t}", "\t_ = tag"),
    ("sql-unknown-user-unmapped", ST, "ok && pg.Code == \"23503\" && pg.ConstraintName == \"memberships_user_id_fkey\" {", "ok && pg.Code == \"23503\" && pg.ConstraintName == \"memberships_organization_id_fkey\" {"),
    # HTTP
    ("http-path-org-unchecked", HTTP, "\t\tif r.PathValue(\"organization_id\") != sc.OrganizationID {", "\t\tif false && r.PathValue(\"organization_id\") != sc.OrganizationID {"),
    ("http-path-user-unchecked", HTTP, "\tif !ValidUserID(user) {\n\t\treturn \"\", &httpapi.Error", "\tif false {\n\t\treturn \"\", &httpapi.Error"),
    ("http-last-owner-500", HTTP, "\tcase errors.Is(err, ErrLastOwner):\n\t\treturn httpapi.Errorf(httpapi.LifecycleConflict", "\tcase errors.Is(err, ErrLastOwner) && false:\n\t\treturn httpapi.Errorf(httpapi.LifecycleConflict"),
    ("http-unknown-user-404", HTTP, "\t\treturn &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: \"no user has this ID\",", "\t\treturn &httpapi.Error{Code: httpapi.NotFound, Message: \"no user has this ID\","),
    ("http-delete-200", HTTP, "\tw.WriteHeader(http.StatusNoContent)", "\tw.WriteHeader(http.StatusOK)"),
    # provisioning (D-008, TEST-011)
    ("prov-owner-unchecked", PROV, "\t\tif !owner || runtime {", "\t\tif runtime {"),
    ("prov-runtime-allowed", PROV, "\t\tif !owner || runtime {", "\t\tif !owner {"),
    ("prov-no-set-role", PROV, "\t_, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`)\n\treturn err", "\treturn nil"),
    ("prov-identity-unchecked", PROV, "\tfor _, s := range []string{issuer, subject} {\n\t\tif s == \"\" ||", "\tfor _, s := range []string{} {\n\t\tif s == \"\" ||"),
    ("prov-name-unchecked", PROV, "\tif name == \"\" || utf8.RuneCountInString(name) > 200", "\tif false && utf8.RuneCountInString(name) > 200"),
    ("prov-user-duplicated", PROV, "\tif err == nil {\n\t\treturn id, false, nil\n\t}", "\tif false {\n\t\treturn id, false, nil\n\t}"),
    ("prov-no-owner-membership", PROV, "VALUES ($1,$2,'owner')`, orgID, userID)", "VALUES ($1,$2,'viewer')`, orgID, userID)"),
    ("prov-no-audit", PROV, "'organization.provision','organization'", "'organization.created','organization'"),
    ("cmd-operator-unchecked", CMD, "\tif err := provisioning.CheckOperator(ctx, db); errors.Is(err, provisioning.ErrNotOperator) {", "\tif err := provisioning.CheckOperator(ctx, db); false && errors.Is(err, provisioning.ErrNotOperator) {"),
]

EQUIVALENT: dict[str, str] = {}

HARNESS_ERRORS = {
    "BUILD-FAILED": ("build failed", "setup failed"),
    "TIMEOUT": ("panic: test timed out",),
}


def verdict(p):
    o = p.stdout + p.stderr
    if p.returncode == 0:
        return "SURVIVED", ""
    for kind, marks in HARNESS_ERRORS.items():
        if any(m in o for m in marks):
            return kind, o[-300:].replace("\n", " ")
    first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:"))), "")
    return "KILLED", first[:110]


def main() -> int:
    bad = []
    for name, f, old, new in MUTANTS:
        n = (REPO / f).read_text().count(old)
        if n != 1:
            bad.append(f"{name}: pattern occurs {n} times in {f}")
    if bad:
        print("\n".join(bad))
        return 2
    if CHECK_ONLY:
        print(f"ok: {len(MUTANTS)} mutants")
        return 0
    if not os.environ.get("GEMINUS_TEST_DATABASE_URL"):
        print("set GEMINUS_TEST_DATABASE_URL to the disposable database of `make db-test`", file=sys.stderr)
        return 2
    env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1", GOTOOLCHAIN=os.environ.get("GOTOOLCHAIN", "go1.27.2"))
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new) in enumerate(MUTANTS):
            if ONLY and name not in ONLY:
                continue
            src = (REPO / f).read_text().replace(old, new)
            m = pathlib.Path(tmp) / f"m{i}.go"
            m.write_text(src)
            ov = pathlib.Path(tmp) / f"m{i}.json"
            ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
            p = subprocess.run(["go", "test", "-p", "1", "-count=1", "-timeout", "600s", "-overlay", str(ov), *PKGS],
                               cwd=REPO, capture_output=True, text=True, env=env)
            results.append((name, *verdict(p)))
            print("\t".join(results[-1]), flush=True)
    wrong = [r for r in results if r[1] in HARNESS_ERRORS or (r[1] == "SURVIVED") != (r[0] in EQUIVALENT)]
    print(f"total={len(results)} killed={sum(r[1] == 'KILLED' for r in results)} equivalent={sum(r[0] in EQUIVALENT for r in results)} unexpected={len(wrong)}")
    for r in wrong:
        print("UNEXPECTED\t" + "\t".join(r))
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
