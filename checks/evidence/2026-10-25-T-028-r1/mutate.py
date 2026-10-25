"""Targeted mutants for T-028 review round 1.

Each mutant replaces one exact snippet and runs two sets of tests:
"before", the tests that existed before this round (the new ones skipped),
which is the reviewer's finding when it SURVIVES; and "after", the new test
written for the finding, which must KILL it. The file is restored after
each mutant. The "unit" set needs no database; the "db" set needs
GEMINUS_TEST_DATABASE_URL (a disposable dt_test_* database prepared by
`make db-test`) and is refused without it, since the DB-backed tests skip
and every mutant would survive.

`db-compile` only checks, without a database, that each db mutant applies
once and builds.

Usage: mutate.py <repo root> <log path> [unit|db|db-compile]
"""
import os
import subprocess
import sys

root, log = sys.argv[1], sys.argv[2]
which = sys.argv[3] if len(sys.argv) > 3 else 'unit'
S = 'internal/scenarios/'
PKG = './internal/scenarios/'
API = './internal/api/'
NEW_UNIT = 'TestCheckAcceptsWarnings|TestBindingFailure|TestCapped'
NEW_DB = 'TestCorruptScenarioIsNotShown'

# (name, file, old, new, after-test regexp[, an existing test this round
# changed, which "before" skips as well])
unit = [
    # L1: warnings refuse a scenario (the reviewer's warnings-refuse).
    ("warnings-refuse", S + 'check.go',
     'func isError(i model.ValidationIssue) bool { return i.Severity == model.SeverityError }',
     'func isError(i model.ValidationIssue) bool { return true }',
     'TestCheckAcceptsWarnings'),
    ("pack-warnings-refuse", S + 'check.go',
     'if issues := pack.Validate(ir); slices.ContainsFunc(issues, isError) {',
     'if issues := pack.Validate(ir); len(issues) > 0 {',
     'TestCheckAcceptsWarnings'),
    ("model-warnings-refuse", S + 'check.go',
     '\tif !res.Valid() {\n', '\tif !res.Valid() || len(res.Validation.Issues) > 0 {\n',
     'TestCheckAcceptsWarnings'),
    # L2: a request that ended answers 503 DATASET_STORAGE_FAILED (the
    # reviewer's binding-cancel-is-503), and two halves of the rule.
    ("binding-cancel-is-503", S + 'scenarios.go',
     'if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {',
     'if false {',
     'TestBindingFailure'),
    ("binding-only-the-context", S + 'scenarios.go',
     'if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {',
     'if ctx.Err() != nil {',
     'TestBindingFailure'),
    ("binding-only-the-error", S + 'scenarios.go',
     'if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {',
     'if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {',
     'TestBindingFailure'),
    ("binding-wraps-everything", S + 'scenarios.go',
     '\treturn &DatasetStorageError{Err: err}\n}', '\treturn err\n}',
     'TestBindingFailure'),
    # I1: the cap (the reviewer's capped-off-by-one and capped-disabled),
    # and its count and severity.
    ("capped-off-by-one", S + 'check.go',
     'if len(issues) <= maxIssues {', 'if len(issues) < maxIssues {',
     'TestCapped'),
    ("capped-disabled", S + 'check.go',
     'if len(issues) <= maxIssues {', 'if true {',
     'TestCapped'),
    ("capped-miscounts", S + 'check.go',
     '"%d more issues are not listed", len(rest))', '"%d more issues are not listed", len(rest)-1)',
     'TestCapped'),
    ("capped-severity-of-the-kept", S + 'check.go',
     'if slices.ContainsFunc(rest, isError) {', 'if slices.ContainsFunc(kept, isError) {',
     'TestCapped'),
    ("capped-keeps-the-last", S + 'check.go',
     'kept, rest := issues[:maxIssues-1], issues[maxIssues-1:]',
     'kept, rest := issues[len(issues)-maxIssues+1:], issues[:len(issues)-maxIssues+1]',
     'TestCapped'),
    # I3: a patch that is not empty but changes nothing is stored as the
    # baseline's [] instead of as given. The assertion was added to an
    # existing test, TestCheckKeepsABaseline, so "before" skips that test
    # too (its earlier version, 19f676b, checked only the candidate).
    ("no-op-stored-as-baseline", S + 'check.go',
     '\tcanonical, err := hashing.Canonical(doc)\n',
     '\tcanonical, err := hashing.Canonical(doc)\n'
     '\tif enc, encErr := model.Encode(ir); encErr == nil {\n'
     '\t\tif again, cErr := hashing.Canonical(enc); cErr == nil && string(again) == string(base) {\n'
     '\t\t\tcanonical = []byte("[]")\n\t\t}\n\t}\n',
     'TestCheckKeepsABaseline', 'TestCheckKeepsABaseline'),
]

# L3: a stored scenario whose patch is not its patch_hash's is shown, or
# not refused as a server fault. Killed only with a database.
db = [
    ("db-hash-not-checked-on-read", S + 'public.go',
     '\tif hash != r.PatchHash {\n', '\tif hash != r.PatchHash && false {\n',
     NEW_DB),
    ("db-list-skips-the-corrupt-row", S + 'scenarios.go',
     '\t\t\tp, err := Public(r)\n\t\t\tif err != nil {\n\t\t\t\treturn err\n\t\t\t}',
     '\t\t\tp, err := Public(r)\n\t\t\tif err != nil {\n\t\t\t\tcontinue\n\t\t\t}',
     NEW_DB),
    ("db-get-corrupt-is-not-found", S + 'scenarios.go',
     '\t\tout, err = Public(r)\n\t\treturn err',
     '\t\tout, err = Public(r)\n\t\tif errors.Is(err, ErrCorrupt) {\n\t\t\treturn ErrNotFound\n\t\t}\n\t\treturn err',
     NEW_DB),
    ("db-corrupt-cause-sent", S + 'http.go',
     '\tcase errors.Is(err, ErrNotFound):\n',
     '\tcase errors.Is(err, ErrCorrupt):\n\t\treturn httpapi.Errorf(httpapi.InternalError, err.Error())\n\tcase errors.Is(err, ErrNotFound):\n',
     NEW_DB),
]

if which == 'unit':
    mutants, after_pkg = unit, PKG
    # Every test of the package that existed before this round.
    before = [(PKG, '.', '^(' + NEW_UNIT + ')$')]
elif which == 'db-compile':
    # Without a database: each db mutant's snippet occurs once and the
    # mutated packages build with their tests (go vet), so a kill under
    # `db` is the test's and never a compile error's. Runs no test.
    out = open(log, 'w')
    ok = 0
    for name, path, old, new, run, *changed in db:
        full = root + '/' + path
        src = open(full).read()
        if src.count(old) != 1:
            out.write(f'{name}: SNIPPET NOT FOUND ONCE ({src.count(old)})\n')
            continue
        open(full, 'w').write(src.replace(old, new))
        try:
            r = subprocess.run(['go', 'vet', PKG, API], cwd=root, capture_output=True, text=True, timeout=900)
        finally:
            open(full, 'w').write(src)
        ok += r.returncode == 0
        out.write(f'{name}: {"BUILDS" if r.returncode == 0 else "DOES NOT BUILD " + r.stderr.strip()[:300]}\n')
    out.write(f'TOTAL {len(db)} (db-compile): {ok} build; the tests were NOT RUN (no database)\n')
    out.close()
    print(open(log).read())
    sys.exit(0 if ok == len(db) else 1)
elif which == 'db':
    if not os.environ.get('GEMINUS_TEST_DATABASE_URL'):
        sys.exit('db mutants need GEMINUS_TEST_DATABASE_URL (the DB-backed tests skip without it, so every mutant would survive)')
    mutants, after_pkg = db, API
    before = [(PKG, '.', ''), ('./internal/storage/scenarios/', '.', ''), (API, '^TestScenario', '^(' + NEW_DB + ')$')]
else:
    sys.exit('unit or db')


def go_test(pkg, run, skip, env):
    args = ['go', 'test', '-count=1', '-run', run]
    if skip:
        args += ['-skip', skip]
    r = subprocess.run(args + [pkg], cwd=root, capture_output=True, text=True, timeout=900, env=env)
    first = [l for l in (r.stdout + r.stderr).splitlines() if 'FAIL' in l or 'error' in l.lower() or 'want' in l][:3]
    return r.returncode != 0, ' | '.join(first)


env = dict(os.environ)
if which == 'db':
    env['GEMINUS_REQUIRE_DB_TESTS'] = '1'
out = open(log, 'w')
out.write(f'set {which}; before = the earlier tests ({before}); after = the new test\n')
killed = gaps = 0
for name, path, old, new, run, *changed in mutants:
    full = root + '/' + path
    src = open(full).read()
    if src.count(old) != 1:
        out.write(f'{name}: SNIPPET NOT FOUND ONCE ({src.count(old)})\n')
        continue
    open(full, 'w').write(src.replace(old, new))
    try:
        b_killed, b_why = False, ''
        for pkg, brun, bskip in before:
            if changed and bskip:
                bskip = bskip[:-2] + '|' + changed[0] + ')$'
            k, why = go_test(pkg, brun, bskip, env)
            if k:
                b_killed, b_why = True, why
                break
        a_killed, a_why = go_test(after_pkg, '^(' + run + ')$', '', env)
    finally:
        open(full, 'w').write(src)
    if not b_killed:
        gaps += 1
    if a_killed:
        killed += 1
    out.write(f'{name}: before {"KILLED" if b_killed else "SURVIVED"}{" (" + b_why + ")" if b_why else ""}; '
              f'after ({run}) {"KILLED" if a_killed else "SURVIVED"} {a_why}\n')
out.write(f'TOTAL {len(mutants)} ({which}): {killed} killed by the new tests, {len(mutants) - killed} survived; '
          f'{gaps} survived the earlier tests\n')
out.close()
print(open(log).read())
