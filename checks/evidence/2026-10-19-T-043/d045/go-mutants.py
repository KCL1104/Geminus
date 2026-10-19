#!/usr/bin/env python3
"""T-043 / D-045 Go mutation check: each mutant weakens one rule of GET
/v1/me (the user-only scope tenant.DB.RunSelf, the query
internal/storage/memberships.OfUser, the list and its truncation in
internal/tenant/me, the authentication httpapi.Self, the unknown-user
mapping, the response headers and the route's wiring) and must make the
listed packages' tests fail. Mutants are applied with `go test -overlay`;
the repository is never modified. A mutant that does not build is a harness
error, never a kill; a crashed test binary counts as a kill (H-011).

Run it on a migrated dt_test_* database whose default collation is not
byte order (an ICU en-US one, as db/README.md describes), or
ofuser-database-collation survives.

Usage: GEMINUS_TEST_DATABASE_URL=... python3 checks/evidence/2026-10-19-T-043/d045/go-mutants.py [--only=NAME ...] [repo-root]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

ARGS = [a for a in sys.argv[1:] if not a.startswith("--only=")]
ONLY = {a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--only=")}
REPO = pathlib.Path(ARGS[0] if ARGS else pathlib.Path(__file__).resolve().parents[4]).resolve()
SELF = "internal/tenant/self.go"
STORE = "internal/storage/memberships/memberships.go"
ME = "internal/tenant/me/me.go"
SCOPE = "internal/httpapi/scope.go"
ERRORS = "internal/httpapi/errors.go"
API = "internal/api/api.go"
TENANT, STORAGE, MEPKG, HTTPAPI, APIPKG, CMD = ("./internal/tenant/", "./internal/storage/memberships/", "./internal/tenant/me/",
                                                "./internal/httpapi/", "./internal/api/", "./cmd/api/")

CHECK = '''		if len(r.Header.Values("X-Organization-ID")) > 0 {
			return &Error{Code: InvalidRequest, Message: "this operation selects no organization: send no X-Organization-ID header",
				Details: []Detail{{Path: "", Reason: "X-Organization-ID"}}}
		}
'''
RESOLVE = '''		user, err := users.ResolveUser(r.Context(), id.Issuer, id.Subject)
		if err != nil {
			return err
		}
		return next(w, r.WithContext(auth.WithIdentity(r.Context(), id)), user)'''

MUTANTS = [
    # The user-only scope (tenant.DB.RunSelf)
    ("self-no-user-check", SELF, "\tif !userID.MatchString(user) {\n\t\treturn ErrInvalidScope\n\t}\n\treturn d.db.Tx(", "\treturn d.db.Tx(", [TENANT]),
    ("self-read-write", SELF, "d.db.Tx(ctx, ReadOnly,", "d.db.Tx(ctx, ReadWrite,", [TENANT]),
    ("self-scope-without-the-user", SELF, 'scope.Set(ctx, tx, "", user)', 'scope.Set(ctx, tx, "", "")', [TENANT]),
    # The query (OfUser)
    ("ofuser-no-user-filter", STORE, "\t\tWHERE m.user_id = $1\n", "\t\tWHERE $1::text IS NOT NULL\n", [STORAGE]),
    ("ofuser-order-by-id", STORE, 'ORDER BY o.name COLLATE "C", m.organization_id', "ORDER BY m.organization_id", [STORAGE]),
    ("ofuser-database-collation", STORE, 'ORDER BY o.name COLLATE "C",', "ORDER BY o.name,", [STORAGE, APIPKG]),
    ("ofuser-no-tie-break", STORE, ', m.organization_id COLLATE "C" LIMIT', " LIMIT", [STORAGE]),
    ("ofuser-names-descending", STORE, 'ORDER BY o.name COLLATE "C",', 'ORDER BY o.name COLLATE "C" DESC,', [STORAGE, APIPKG]),
    # The list and its truncation (D-045: at most 100, never silently cut)
    ("truncated-at-100", ME, "truncated := len(rows) > MaxMemberships", "truncated := len(rows) >= MaxMemberships", [MEPKG, APIPKG]),
    ("asks-for-exactly-100", ME, "store.OfUser(ctx, tx, user, MaxMemberships+1)", "store.OfUser(ctx, tx, user, MaxMemberships)", [MEPKG, APIPKG]),
    ("not-cut", ME, "\t\trows = rows[:MaxMemberships]\n", "", [MEPKG, APIPKG]),
    ("limit-101", ME, "const MaxMemberships = 100", "const MaxMemberships = 101", [MEPKG, APIPKG]),
    ("empty-list-is-null", ME, "out := make([]Membership, 0, len(rows))", "var out []Membership", [MEPKG, APIPKG]),
    # Authentication (httpapi.Self)
    ("selector-accepted", SCOPE, CHECK, "", [HTTPAPI, MEPKG, APIPKG]),
    ("one-selector-accepted", SCOPE, 'if len(r.Header.Values("X-Organization-ID")) > 0 {', 'if len(r.Header.Values("X-Organization-ID")) > 1 {', [HTTPAPI, MEPKG, APIPKG]),
    ("selector-checked-after-resolving", SCOPE, CHECK + RESOLVE, RESOLVE.replace("\t\treturn next(", CHECK + "\t\treturn next("), [HTTPAPI]),
    ("token-error-ignored", SCOPE, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n" + CHECK, "\t\t_ = err\n" + CHECK, [HTTPAPI, MEPKG]),
    ("unknown-user-is-404", ERRORS, 'return Errorf(Forbidden, "the authenticated identity has no user in this deployment"), true',
     'return Errorf(NotFound, "the authenticated identity has no user in this deployment"), true', [HTTPAPI, MEPKG, APIPKG]),
    # Personal data is never cached
    ("no-store-removed", ERRORS, '\tw.Header().Set("Cache-Control", "no-store")\n', "", [MEPKG, APIPKG]),
    # The route
    ("me-not-wired", API, "\tme.Register(mux, log, v, db, &me.Service{DB: db})\n", "\t_ = me.Register\n", [APIPKG, CMD]),
]
EQUIVALENT: dict[str, str] = {}


def run(overlay: str | None, pkgs: list[str]) -> str:
    env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1")
    cmd = ["go", "test", "-count=1", "-p", "1", "-timeout=300s", *pkgs]
    if overlay:
        cmd.insert(2, "-overlay=" + overlay)
    r = subprocess.run(cmd, cwd=REPO, env=env, capture_output=True, text=True, timeout=1200)
    if r.returncode:
        if "--- FAIL" in r.stdout:
            return "KILLED\t" + next(l.strip() for l in r.stdout.splitlines() if l.startswith("--- FAIL"))
        if "panic: test timed out" in r.stdout:
            return "KILLED\ttest timed out (a hang the tests do not bound)"
        if "\npanic: " in "\n" + r.stdout + r.stderr:
            first = next(l for l in (r.stdout + r.stderr).splitlines() if l.startswith("panic: "))
            return "KILLED\tthe test binary panicked: " + first
        return "BUILD-FAILED\t" + (r.stdout + r.stderr).strip().splitlines()[-1]
    return "SURVIVED"


def main() -> int:
    if not os.environ.get("GEMINUS_TEST_DATABASE_URL"):
        print("set GEMINUS_TEST_DATABASE_URL to a migrated dt_test_* database", file=sys.stderr)
        return 2
    files = sorted({m[1] for m in MUTANTS})
    sources = {f: (REPO / f).read_text() for f in files}
    bad = [n for n, f, old, _, _ in MUTANTS if sources[f].count(old) != 1]
    if bad:
        print("patterns not found exactly once:", bad)
        return 2
    pkgs = sorted({p for m in MUTANTS for p in m[4]})
    if (b := run(None, pkgs)) != "SURVIVED":
        print("baseline fails:", b)
        return 2
    print("baseline\t" + " ".join(pkgs) + " pass", flush=True)
    wrong = total = 0
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new, tests) in enumerate(MUTANTS):
            if ONLY and name not in ONLY:
                continue
            total += 1
            mutated = pathlib.Path(tmp) / f"m{i}.go"
            mutated.write_text(sources[f].replace(old, new))
            overlay = pathlib.Path(tmp) / f"o{i}.json"
            overlay.write_text(json.dumps({"Replace": {str(REPO / f): str(mutated)}}))
            v = run(str(overlay), tests)
            ok = not v.startswith("BUILD-FAILED") and (v == "SURVIVED") == (name in EQUIVALENT)
            wrong += not ok
            print(f"{name}\t{v}" + ("" if ok else "\tUNEXPECTED"), flush=True)
    print(f"total={total} unexpected={wrong}")
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
