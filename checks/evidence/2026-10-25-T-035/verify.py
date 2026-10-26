"""Run T-035's verification commands in the worktree, one log each, and
record the exit codes (evidence/exit-codes.txt)."""
import os
import subprocess
import sys
import time

root, ev = sys.argv[1], sys.argv[2]
which = sys.argv[3:] or None
PY = '/home/user/Geminus/.venv/bin/python'
TOUCHED = ['./internal/observability/...', './internal/audit/', './internal/httpapi/', './internal/worker/', './internal/storage/auditlog/',
           './internal/storage/jobs/', './internal/storage/provisioning/', './internal/storage/dbtest/', './internal/config/', './cmd/...',
           './internal/runs/runner/', './internal/proposals/generator/', './internal/import/profiler/', './internal/import/validator/',
           './internal/api/']
steps = [
    ('clean-testcache', ['go', 'clean', '-testcache']),
    ('spec-check', ['make', 'spec-check', 'PYTHON=' + PY]),
    ('backend-test', ['make', 'backend-test']),
    ('architecture-check', ['make', 'architecture-check']),
    ('race-touched', ['go', 'test', '-race', '-count=1', *TOUCHED]),
    ('backend-race', ['make', 'backend-race']),
    ('ai-eval', ['make', 'ai-eval']),
    ('mod-verify', ['go', 'mod', 'verify']),
    ('govulncheck', ['env', 'GOTOOLCHAIN=go1.27.3', 'go', 'run', 'golang.org/x/vuln/cmd/govulncheck@v1.9.0', './cmd/...', './internal/...',
                     './schemas/...', './scripts/...']),
    ('mutants-db-build', ['python3', os.path.join(ev, 'mutate.py'), root, os.path.join(ev, 'mutants-db-build.log'), 'dbbuild']),
]
codes = os.path.join(ev, 'exit-codes.txt')
for name, cmd in steps:
    if which and name not in which:
        continue
    start = time.time()
    r = subprocess.run(cmd, cwd=root, capture_output=True, text=True)
    took = time.time() - start
    log = os.path.join(ev, name + '.log') if name != 'mutants-db-build' else os.path.join(ev, 'mutants-db-build.out')
    with open(log, 'w') as f:
        f.write('$ ' + ' '.join(cmd) + '\n')
        f.write(r.stdout)
        f.write(r.stderr)
        f.write('\nexit=%d (%.0fs)\n' % (r.returncode, took))
    with open(codes, 'a') as f:
        f.write('%-20s exit=%d  %5.0fs  %s\n' % (name, r.returncode, took, ' '.join(cmd)))
    print(name, r.returncode, '%.0fs' % took, flush=True)
