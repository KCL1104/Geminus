"""Mutants of the T-029 review round 1 fixes (same rules as
checks/evidence/2026-10-25-T-029/mutate.py: one exact snippet, must build,
the named tests must fail, the file is restored).

Usage: mutate-r1.py <repo root> <log path> [unit|db]
"""
import os
import subprocess
import sys

root, log = sys.argv[1], sys.argv[2]
which = sys.argv[3] if len(sys.argv) > 3 else 'unit'
C = 'internal/results/compare/compare.go'
CP = './internal/results/compare/'
S = 'internal/comparisons/'
SP = './internal/comparisons/'

unit = [
    # L5: a JSON null is not a patch.
    ("null-patch-is-a-baseline", C, "\tif t := bytes.TrimLeft(patch, \" \\t\\r\\n\"); len(t) == 0 || t[0] != '[' {\n",
     "\tif t := bytes.TrimLeft(patch, \" \\t\\r\\n\"); len(t) == 0 && t == nil {\n", CP, 'TestInconsistent'),
    ("padded-empty-array-refused", C, 'bytes.TrimLeft(patch, " \\t\\r\\n")', 'bytes.TrimLeft(patch, "")', CP, 'TestComparableAcrossSpellings'),
    # I1: the other hashes and the window's unit and boundary.
    ("model-hash-not-checked", C, 'if rep.ModelHash != r.ModelHash || rep.EffectiveModelHash', 'if rep.EffectiveModelHash', CP, 'TestInconsistent'),
    ("effective-hash-not-checked", C, ' || rep.EffectiveModelHash != r.EffectiveModelHash || rep.ConfigHash', ' || rep.ConfigHash', CP, 'TestInconsistent'),
    ("config-hash-not-checked", C, ' || rep.ConfigHash != r.ConfigHash {', ' {', CP, 'TestInconsistent'),
    ("tick-unit-not-checked", C, ' || mw.TickUnit != "millisecond" ||', ' ||', CP, 'TestInconsistent'),
    ("time-boundary-not-checked", C, ' || mw.TimeBoundary != "warmup <= t <= horizon" ||', ' ||', CP, 'TestInconsistent'),
    ("warmup-window-not-checked", C, '\t\tmw.WarmupTicks != w {\n', '\t\tfalse {\n', CP, 'TestInconsistent'),
    # R13 of the review, which survived once Compare validated the metrics:
    # Statistics and Resolve must still refuse a value out of range.
    ("summary-not-finite-ok", C,
     '\t\tif v == nil || !finite(*v) {\n\t\t\treturn nil, inconsistent("%s: an available summary without a finite %s", at, stat)',
     '\t\tif v == nil {\n\t\t\treturn nil, inconsistent("%s: an available summary without a finite %s", at, stat)', CP, 'TestInconsistent'),
    ("wait-not-finite-ok", C,
     '\t\tif v == nil || !finite(*v) {\n\t\t\treturn nil, inconsistent("%s: an available queue without a finite %s", at, stat)',
     '\t\tif v == nil {\n\t\t\treturn nil, inconsistent("%s: an available queue without a finite %s", at, stat)', CP, 'TestInconsistent'),
    ("hashes-not-passed", S + 'comparisons.go', '\t\tModelHash: r.ModelHash, EffectiveModelHash: r.EffectiveModelHash, ConfigHash: r.ConfigHash,\n', '', SP, 'TestInput'),
    # I2: the metrics against the contract.
    ("metrics-not-validated", C, '\tif len(issues) > 0 {\n\t\t// The path only', '\tif len(issues) > 0 && false {\n\t\t// The path only', CP, 'TestInconsistent'),
    ("metrics-validated-against-nothing", C, 'const metricsRef = "simulation.schema.json#/$defs/Result/properties/metrics"',
     'const metricsRef = "simulation.schema.json#/$defs/Result/properties/trace"', CP, 'TestInconsistent|TestGolden'),
]

db = [
    # L6: the transaction's mode (R30 of the review) and the seam itself.
    ("db-read-write-transaction", S + 'comparisons.go', 'tenant.ReadSnapshot', 'tenant.ReadWrite', SP, 'TestCompareReadsOneReadOnlySnapshot'),
    ("db-read-committed-transaction", S + 'comparisons.go', 'tenant.ReadSnapshot', 'tenant.ReadOnly', SP, 'TestCompareReadsOneReadOnlySnapshot'),
    ("db-hook-not-called", S + 'comparisons.go', '\t\tif inTransaction != nil {\n', '\t\tif inTransaction != nil && false {\n', SP,
     'TestCompareReadsOneReadOnlySnapshot'),
]

if which == 'dbbuild':
    out = open(log, 'w')
    for name, path, old, new, pkg, run in db:
        full = root + '/' + path
        src = open(full).read()
        if src.count(old) != 1:
            out.write(f'{name}: SNIPPET NOT FOUND ONCE ({src.count(old)})\n')
            continue
        open(full, 'w').write(src.replace(old, new))
        try:
            vet = subprocess.run(['go', 'vet', pkg], cwd=root, capture_output=True, text=True, timeout=600)
        finally:
            open(full, 'w').write(src)
        out.write(f'{name}: {"BUILDS" if vet.returncode == 0 else "DOES NOT BUILD"}; tests NOT RUN ({run}) {vet.stderr.strip()[:200]}\n')
    out.close()
    print(open(log).read())
    sys.exit(0)

mutants = {'unit': unit, 'db': db}[which]
if which == 'db' and not os.environ.get('GEMINUS_TEST_DATABASE_URL'):
    sys.exit('db mutants need GEMINUS_TEST_DATABASE_URL (the DB-backed tests skip without it, so every mutant would survive)')

out = open(log, 'w')
killed = survived = build = 0
for name, path, old, new, pkg, run in mutants:
    full = root + '/' + path
    src = open(full).read()
    if src.count(old) != 1:
        out.write(f'{name}: SNIPPET NOT FOUND ONCE ({src.count(old)})\n')
        survived += 1
        continue
    open(full, 'w').write(src.replace(old, new))
    env = dict(os.environ)
    if which == 'db':
        env['GEMINUS_REQUIRE_DB_TESTS'] = '1'
    try:
        vet = subprocess.run(['go', 'vet', pkg], cwd=root, capture_output=True, text=True, timeout=600, env=env)
        r = None
        if vet.returncode == 0:
            r = subprocess.run(['go', 'test', '-count=1', '-run', '^(' + run + ')$', pkg], cwd=root, capture_output=True, text=True, timeout=900, env=env)
    finally:
        open(full, 'w').write(src)
    text = (r.stdout + r.stderr) if r else vet.stderr
    if r is None or '[build failed]' in text:
        build += 1
        out.write(f'{name}: BUILD ({run}) {" | ".join(text.splitlines()[:2])}\n')
        continue
    status = 'KILLED' if r.returncode != 0 else 'SURVIVED'
    killed += r.returncode != 0
    survived += r.returncode == 0
    first = [l for l in text.splitlines() if l.startswith('--- FAIL') or 'panic' in l][:3]
    out.write(f'{name}: {status} ({run}) {" | ".join(first)}\n')
out.write(f'TOTAL {len(mutants)} ({which}): {killed} killed, {survived} survived, {build} did not build\n')
out.close()
print(open(log).read())
