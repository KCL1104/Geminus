"""Red-first runs of T-035's tests that were written alongside the code.

Each case puts back what the code was before T-035 (the file at the base
commit 38f0b7b, a new file removed, or, where the base cannot build with
the test, a one-line stand-in that is named in the log), runs the named
tests or `go vet` (DB-backed tests compile only: no database here), records
the exit code and output, and restores the tree with `git checkout HEAD`.
A case is RED when the command fails. The tree must be clean (committed)
before it runs and is checked clean after (tracked files).

Usage: redfirst.py <repo root> <out dir>
"""
import os
import subprocess
import sys

root, outdir = sys.argv[1], sys.argv[2]
BASE = '38f0b7b'
os.makedirs(outdir, exist_ok=True)


def git(*args):
    return subprocess.run(['git', *args], cwd=root, capture_output=True, text=True, check=True).stdout


def base(path):
    return ('base', path)


def hide(path):
    return ('hide', path)


def add(path, text):
    return ('add', path, text)


def sub(path, old, new):
    return ('sub', path, old, new)


def test(pkg, run):
    return ['go', 'test', '-count=1', '-run', '^(' + run + ')$', pkg]


def vet(*pkgs):
    return ['go', 'vet', *pkgs]


API_SHIM = '''package main

import (
	"io"
	"log/slog"
)

// redfirst stand-in: the logger cmd/api built before T-035 (base main.go,
// line 45), as the function the T-035 tests call.
func newLogger(w io.Writer) *slog.Logger { return slog.New(slog.NewJSONHandler(w, nil)).With("component", "api") }
'''

WORKER_SHIM = API_SHIM.replace('cmd/api', 'cmd/worker').replace('"component", "api"', '"component", "worker"').replace('line 45', 'line 198')

cases = [
    # internal/observability, internal/audit, the telemetry and export
    # packages are new: before T-035 they have no code at all.
    ('observability', [hide('internal/observability/redact.go'), hide('internal/observability/handler.go'),
                       hide('internal/observability/context.go')],
     test('./internal/observability/', 'Test.*')),
    ('telemetry', [hide('internal/observability/telemetry/telemetry.go')], test('./internal/observability/telemetry/', 'Test.*')),
    ('export', [hide('internal/observability/telemetry/export/export.go')], test('./internal/observability/telemetry/export/', 'Test.*')),
    ('config-telemetry', [hide('internal/config/telemetry.go')], test('./internal/config/', 'TestLoadTelemetry')),
    # httpapi: the access log kept, the request layer as it was: the panic
    # value is logged, and the access line has no error code, organization
    # or user, and the response's request ID is not the access line's.
    ('httpapi-request-layer', [base('internal/httpapi/request.go'), base('internal/httpapi/errors.go'), base('internal/httpapi/scope.go')],
     test('./internal/httpapi/', 'TestRecover|TestAccessLog|TestAccessLogTelemetry|TestAccessLogKeepsTheWriter|TestAccessLogAndAbort')),
    # Added after the first mutation pass (fc2b8b7): Self did not annotate
    # the user (TestPanicText and TestRootsAndFailedWork are in the
    # observability and telemetry cases above).
    ('httpapi-self-user', [base('internal/httpapi/scope.go')], test('./internal/httpapi/', 'TestSelfAnnotatesTheUser')),
    # The worker before T-035: a 200-character ID of any bytes was a worker
    # ID; the loop had no audit of its own (the internal test of it cannot
    # build, so it is a separate case).
    ('worker-id', [base('internal/worker/worker.go'), hide('internal/worker/audit_internal_test.go')],
     test('./internal/worker/', 'TestNewChecksTheConfig')),
    ('worker-attempt-event', [base('internal/worker/worker.go')], test('./internal/worker/', 'TestTheAttemptsEvent')),
    # The provider call's line and the engine outcome did not exist.
    ('generator-call-line', [base('internal/proposals/generator/generator.go')], test('./internal/proposals/generator/', 'TestTheCallsLine')),
    ('runner-engine-outcome', [base('internal/runs/runner/runner.go')], test('./internal/runs/runner/', 'TestEngineOutcome')),
    # cmd: the loggers and configuration as they were (cmd/api's logger as a
    # stand-in function with the base's body).
    ('cmd-api', [base('cmd/api/main.go'), add('cmd/api/zz_redfirst_shim.go', API_SHIM)],
     test('./cmd/api/', 'TestTheLogIsFiltered|TestRunRefusesABadTelemetryEndpoint')),
    ('cmd-worker', [base('cmd/worker/main.go'), add('cmd/worker/zz_redfirst_shim.go', WORKER_SHIM)],
     test('./cmd/worker/', 'TestTheWorkersLoggerIsFiltered|TestRunRefusesABadTelemetrySetting')),
    # Not red-first: TestTheWorkersLinesAreJSON held before T-035 as well
    # (JSON lines with the component); it guards that the filter keeps it.
    ('cmd-worker-json-lines', [base('cmd/worker/main.go'), add('cmd/worker/zz_redfirst_shim.go', WORKER_SHIM)],
     test('./cmd/worker/', 'TestTheWorkersLinesAreJSON')),
    # DB-backed tests (NOT RUN here): they do not build against the base.
    ('db-jobs-reapjobs', [base('internal/storage/jobs/jobs.go')], vet('./internal/storage/jobs/')),
    ('db-refuse-audit', [base('internal/storage/dbtest/dbtest.go')], vet('./internal/api/', './internal/worker/')),
    # Provisioning wrote its own audit SQL: the source test finds it.
    ('audit-provisioning', [base('internal/storage/provisioning/provisioning.go')], test('./internal/audit/', 'TestTheCatalogueMatchesTheCode')),
    # Architecture: the probes of the new rules, against the base's rules.
    ('architecture-new-rules', [base('scripts/check-architecture/rules.go')],
     ['go', 'test', '-count=1', '-run',
      'TestProbesAreCaught/(the-filter-reads-storage|the-filter-goes-online|the-filter-opens-files|the-filter-links-a-module|'
      'the-catalogue-reads-the-tenant|the-catalogue-imports-pgx|the-catalogue-opens-files|the-api-layer-exports|a-handler-links-the-sdk|'
      'the-instruments-link-the-sdk|the-instruments-go-online|the-instruments-read-storage)$', './scripts/check-architecture/']),
]

def changed():
    """Tracked files that differ from HEAD, outside the output directory
    (its logs are this script's own output)."""
    out = os.path.relpath(os.path.abspath(outdir), os.path.abspath(root)) + '/'
    return [l for l in git('status', '--porcelain', '--untracked-files=no').splitlines() if not l[3:].startswith(out)]


if changed():
    sys.exit('the tree is not clean: commit first')
summary = []
for name, ops, cmd in cases:
    touched, added = [], []
    try:
        for op in ops:
            full = os.path.join(root, op[1])
            if op[0] == 'base':
                open(full, 'w').write(git('show', BASE + ':' + op[1]))
                touched.append(op[1])
            elif op[0] == 'hide':
                os.remove(full)
                touched.append(op[1])
            elif op[0] == 'add':
                open(full, 'w').write(op[2])
                added.append(full)
            elif op[0] == 'sub':
                src = open(full).read()
                assert src.count(op[2]) == 1, (name, op[1])
                open(full, 'w').write(src.replace(op[2], op[3]))
                touched.append(op[1])
        r = subprocess.run(cmd, cwd=root, capture_output=True, text=True, timeout=900)
    finally:
        for f in added:
            if os.path.exists(f):
                os.remove(f)
        if touched:
            git('checkout', 'HEAD', '--', *touched)
    text = (r.stdout + r.stderr).strip()
    lines = text.splitlines()
    if len(lines) > 80:
        lines = lines[:40] + ['... (%d lines cut) ...' % (len(lines) - 80)] + lines[-40:]
    verdict = 'RED' if r.returncode != 0 else 'NOT RED'
    with open(os.path.join(outdir, name + '.log'), 'w') as f:
        f.write('# case %s: %s\n' % (name, verdict))
        for op in ops:
            f.write('# %s %s\n' % (op[0], op[1]))
        f.write('# $ %s\n# exit %d\n' % (' '.join(cmd), r.returncode))
        f.write('\n'.join(lines) + '\n')
    summary.append('%-24s %-8s exit=%d  %s' % (name, verdict, r.returncode, ' '.join(cmd)))
left = changed()
summary.append('tree after the runs (outside the logs): ' + ('clean' if not left else 'NOT CLEAN\n' + '\n'.join(left)))
open(os.path.join(outdir, 'summary.txt'), 'w').write('\n'.join(summary) + '\n')
print('\n'.join(summary))
