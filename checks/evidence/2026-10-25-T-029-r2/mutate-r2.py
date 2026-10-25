"""Mutants of the T-029 review round 2 fix (R2-L1), same rules as
checks/evidence/2026-10-25-T-029/mutate.py: one exact snippet, must build,
the named tests must fail, the file is restored.

Modes:
  unit      every mutant against its named tests (all must be KILLED)
  blackbox  the range mutants against the whole package but the white-box
            TestLookupKeepsTheComparisonRange: they survive, because since
            R2-L1 every exported function validates the metrics first (the
            red evidence for that test)

Usage: mutate-r2.py <repo root> <log path> [unit|blackbox]
"""
import subprocess
import sys

root, log = sys.argv[1], sys.argv[2]
which = sys.argv[3] if len(sys.argv) > 3 else 'unit'
C = 'internal/results/compare/compare.go'
CP = './internal/results/compare/'
CHECK = '\tif err := validMetrics(r.RunID, r.Metrics); err != nil {\n\t\treturn nil, err\n\t}\n'
RANGE = 'TestInconsistent|TestLookupKeepsTheComparisonRange'

validation = [
    # R2-L1: Statistics and Resolve check the metrics against the contract
    # themselves, each one (the reviewer's four probes).
    ("statistics-metrics-not-validated", C, CHECK + '\tvar out []Statistic\n', '\tvar out []Statistic\n', CP, 'TestInconsistent'),
    ("resolve-metrics-not-validated", C, CHECK + '\tt, ok := tokens(path)\n', '\tt, ok := tokens(path)\n', CP, 'TestInconsistent'),
]

range_ = [
    # The comparison's own range behind the contract: round 1's two mutants
    # (and the reviewer's R13), now unreachable from the exported functions,
    # and the two range checks no harness had.
    ("summary-not-finite-ok", C,
     '\t\tif v == nil || !finite(*v) {\n\t\t\treturn nil, inconsistent("%s: an available summary without a finite %s", at, stat)',
     '\t\tif v == nil {\n\t\t\treturn nil, inconsistent("%s: an available summary without a finite %s", at, stat)', CP, RANGE),
    ("wait-not-finite-ok", C,
     '\t\tif v == nil || !finite(*v) {\n\t\t\treturn nil, inconsistent("%s: an available queue without a finite %s", at, stat)',
     '\t\tif v == nil {\n\t\t\treturn nil, inconsistent("%s: an available queue without a finite %s", at, stat)', CP, RANGE),
    ("queue-length-not-finite-ok", C, '\t\tif !finite(q.QueueLengthMean) {\n', '\t\tif !finite(q.QueueLengthMean) && false {\n', CP, RANGE),
    ("utilization-not-finite-ok", C, 'if r.UtilizationMean != nil && !finite(*r.UtilizationMean) {', 'if r.UtilizationMean != nil && false {', CP, RANGE),
]

mutants = {'unit': validation + range_, 'blackbox': range_}[which]
out = open(log, 'w')
killed = survived = build = 0
for name, path, old, new, pkg, run in mutants:
    full = root + '/' + path
    src = open(full).read()
    if src.count(old) != 1:
        out.write(f'{name}: SNIPPET NOT FOUND ONCE ({src.count(old)})\n')
        survived += 1
        continue
    if which == 'blackbox':
        args, run = ['-run', '.', '-skip', '^TestLookupKeepsTheComparisonRange$'], 'all but TestLookupKeepsTheComparisonRange'
    else:
        args = ['-run', '^(' + run + ')$']
    open(full, 'w').write(src.replace(old, new))
    try:
        vet = subprocess.run(['go', 'vet', pkg], cwd=root, capture_output=True, text=True, timeout=600)
        r = None
        if vet.returncode == 0:
            r = subprocess.run(['go', 'test', '-count=1'] + args + [pkg], cwd=root, capture_output=True, text=True, timeout=900)
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
    first = [l.strip() for l in text.splitlines() if l.startswith('--- FAIL') or '_test.go' in l][:3]
    out.write(f'{name}: {status} ({run}) {" | ".join(first)}\n')
out.write(f'TOTAL {len(mutants)} ({which}): {killed} killed, {survived} survived, {build} did not build\n')
out.close()
print(open(log).read())
