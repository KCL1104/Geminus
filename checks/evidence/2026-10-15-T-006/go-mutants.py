#!/usr/bin/env python3
"""Go mutation check for internal/storage/postgres (go test -overlay; repo untouched)."""
import json, os, pathlib, subprocess, sys
REPO = pathlib.Path("/home/user/Geminus"); HERE = pathlib.Path(__file__).parent
F = "internal/storage/postgres/postgres.go"
MUTANTS = [
    ("commit-on-error", "\tif err := fn(ctx, tx); err != nil {\n\t\trollback(ctx, tx)\n\t\treturn Classify(err)\n\t}",
     "\tif err := fn(ctx, tx); err != nil {\n\t\t_ = tx.Commit(ctx)\n\t\treturn Classify(err)\n\t}"),
    ("no-rollback-on-panic", "\t\tif p := recover(); p != nil {\n\t\t\trollback(ctx, tx)\n\t\t\tpanic(p)", "\t\tif p := recover(); p != nil {\n\t\t\tpanic(p)"),
    ("swallow-panic", "\t\t\trollback(ctx, tx)\n\t\t\tpanic(p)", "\t\t\trollback(ctx, tx)\n\t\t\terr = fmt.Errorf(\"panic: %v\", p)"),
    ("rollback-with-cancelled-ctx(equivalent)", "\trctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), RollbackTimeout)", "\trctx, cancel := context.WithTimeout(ctx, RollbackTimeout)"),
    ("commit-error-unclassified", "\t\treturn Classify(fmt.Errorf(\"postgres: commit: %w\", err))", "\t\treturn fmt.Errorf(\"postgres: commit: %v\", err)"),
    ("detail-in-message", "\tmsg := pg.Message\n", "\tmsg := pg.Message + \" \" + pg.Detail\n"),
    ("unclassed-keeps-message", "\tif !ok {\n\t\tmsg = \"\"\n\t}", "\tif !ok {\n\t\tmsg = pg.Message\n\t}"),
    ("immutable-as-constraint", "\"55000\": ErrImmutable,", "\"55000\": ErrConstraint,"),
    ("rls-as-reference", "\"42501\": ErrPermission,", "\"42501\": ErrReference,"),
    ("lose-cancellation", "\t\tcause = ctxErr", "\t\tcause = nil"),
]
def main():
    url = os.environ["GEMINUS_TEST_DATABASE_URL"]
    res = []
    for name, old, new in MUTANTS:
        src = (REPO / F).read_text()
        if src.count(old) != 1:
            res.append((name, "BAD-MUTANT")); continue
        m = HERE / f"gomut-{len(res)}.go"; m.write_text(src.replace(old, new))
        ov = HERE / f"gomut-{len(res)}.json"; ov.write_text(json.dumps({"Replace": {str(REPO / F): str(m)}}))
        env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1")
        p = subprocess.run(["go", "test", "-count=1", "-timeout", "60s", "-overlay", str(ov), "./internal/storage/postgres/"], cwd=REPO, capture_output=True, text=True, env=env)
        out = p.stdout + p.stderr
        st = "SURVIVED" if p.returncode == 0 else ("BUILD-FAILED" if "build failed" in out or "setup failed" in out else "KILLED")
        first = next((l.strip() for l in out.splitlines() if l.strip().startswith("--- FAIL")), "")
        res.append((name, st, first))
    for r in res: print("\t".join(r))
    eq = [r for r in res if r[0].endswith("(equivalent)")]
    killed = sum(1 for r in res if r[1] == "KILLED")
    print(f"{killed}/{len(res) - len(eq)} non-equivalent mutants killed; {len(eq)} documented equivalent")
    # Equivalent: with a cancelled context pgx closes the connection on rollback and
    # PostgreSQL ends the transaction (locks released); detaching only spares the connection.
    sys.exit(0 if killed == len(res) - len(eq) else 1)
main()
