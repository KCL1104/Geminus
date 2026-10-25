"""Targeted mutants of T-054's mapping (publishVersion and createSimulation
answer a dataset binding stage that cannot read or verify the snapshots
with 503 DATASET_STORAGE_FAILED).

Each mutant replaces one exact snippet, runs the named tests, expects a
failure, and restores the file. The "unit" set must be killed by the tests
that need no database; the "db" set by the DB-backed tests, so it needs
GEMINUS_TEST_DATABASE_URL (a disposable dt_test_* database prepared by
`make db-test`) and is refused without it. "compile" applies each db
mutant, checks that the package still builds with its tests (go vet), and
restores it: a db mutant that does not compile would be "killed" for the
wrong reason.

Usage: mutate.py <repo root> <log path> [unit|db|compile]
"""
import os
import subprocess
import sys

root, log = sys.argv[1], sys.argv[2]
which = sys.argv[3] if len(sys.argv) > 3 else 'unit'
M, R = 'internal/models/', 'internal/runs/'
MP, RP, AP = './internal/models/', './internal/runs/', './internal/api/'

BF_IF = 'if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {'
STORAGE_DETAIL = 'Details: []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"}}}'

unit = [
    # models: the binding stage inside check.
    ("models-check-unwrapped", M + 'models.go',
     'return datasets.Resolution{}, nil, bindingFailure(ctx, err)', 'return datasets.Resolution{}, nil, err',
     MP, 'TestCheckStorageFailure'),
    # models: bindingFailure.
    ("models-binding-ignores-the-request", M + 'models.go',
     BF_IF, 'if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {',
     MP, 'TestBindingFailure|TestCheckStorageFailure'),
    ("models-binding-ignores-a-cancelled-read", M + 'models.go',
     BF_IF, 'if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {',
     MP, 'TestBindingFailure|TestCheckStorageFailure'),
    ("models-binding-ignores-a-read-past-its-deadline", M + 'models.go',
     BF_IF, 'if ctx.Err() != nil || errors.Is(err, context.Canceled) {',
     MP, 'TestBindingFailure|TestCheckStorageFailure'),
    ("models-binding-always-storage", M + 'models.go',
     BF_IF, 'if false {',
     MP, 'TestBindingFailure|TestCheckStorageFailure'),
    ("models-binding-never-storage", M + 'models.go',
     BF_IF, 'if true {',
     MP, 'TestBindingFailure|TestCheckStorageFailure'),
    # models: publishRefused.
    ("models-publish-not-mapped", M + 'http.go',
     '\tif errors.As(err, &storage) {\n\t\treturn &httpapi.Error{', '\tif false && errors.As(err, &storage) {\n\t\treturn &httpapi.Error{',
     MP, 'TestPublishRefused'),
    ("models-publish-cause-sent", M + 'http.go',
     STORAGE_DETAIL, 'Details: []httpapi.Detail{{Path: "", Reason: storage.Err.Error(), Code: "DATASET_STORAGE_FAILED"}}}',
     MP, 'TestPublishRefused'),
    ("models-publish-other-code", M + 'http.go',
     STORAGE_DETAIL, 'Details: []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "OBJECT_STORAGE_FAILED"}}}',
     MP, 'TestPublishRefused'),
    ("models-publish-no-details", M + 'http.go',
     ',\n\t\t\t' + STORAGE_DETAIL, '}',
     MP, 'TestPublishRefused'),
    ("models-publish-500", M + 'http.go',
     'return &httpapi.Error{Code: httpapi.ServiceUnavailable, Message: "the dataset snapshots could not be read; nothing was published (retry later)",',
     'return &httpapi.Error{Code: httpapi.InternalError, Message: "the dataset snapshots could not be read; nothing was published (retry later)",',
     MP, 'TestPublishRefused'),
    ("models-publish-says-saved", M + 'http.go',
     'nothing was published (retry later)', 'nothing was saved (retry later)',
     MP, 'TestPublishRefused'),
    # H-022 decided publishVersion only: refused (patchDraft, resetDraft)
    # must not map the storage failure.
    ("models-refused-maps-storage-for-every-route", M + 'http.go',
     '\tswitch {\n\tcase errors.Is(err, ErrNotFound):\n\t\treturn httpapi.Errorf(httpapi.NotFound, "not found")\n\tcase errors.Is(err, ErrConflict):',
     '\tswitch {\n\tcase errors.As(err, new(*DatasetStorageError)):\n\t\treturn httpapi.Errorf(httpapi.ServiceUnavailable, "storage")\n'
     '\tcase errors.Is(err, ErrNotFound):\n\t\treturn httpapi.Errorf(httpapi.NotFound, "not found")\n\tcase errors.Is(err, ErrConflict):',
     MP, 'TestPublishRefused'),
    # runs: bindingFailure.
    ("runs-binding-ignores-the-request", R + 'runs.go',
     BF_IF, 'if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {',
     RP, 'TestBindingFailure'),
    ("runs-binding-ignores-a-cancelled-read", R + 'runs.go',
     BF_IF, 'if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {',
     RP, 'TestBindingFailure'),
    ("runs-binding-ignores-a-read-past-its-deadline", R + 'runs.go',
     BF_IF, 'if ctx.Err() != nil || errors.Is(err, context.Canceled) {',
     RP, 'TestBindingFailure'),
    ("runs-binding-always-storage", R + 'runs.go',
     BF_IF, 'if false {',
     RP, 'TestBindingFailure'),
    ("runs-binding-never-storage", R + 'runs.go',
     BF_IF, 'if true {',
     RP, 'TestBindingFailure'),
    # runs: Refused.
    ("runs-not-mapped", R + 'http.go',
     '\tcase errors.As(err, &storage):\n', '\tcase false && errors.As(err, &storage):\n',
     RP, 'TestRefusedDatasetStorage'),
    ("runs-cause-sent", R + 'http.go',
     STORAGE_DETAIL, 'Details: []httpapi.Detail{{Path: "", Reason: storage.Err.Error(), Code: "DATASET_STORAGE_FAILED"}}}',
     RP, 'TestRefusedDatasetStorage'),
    ("runs-other-code", R + 'http.go',
     STORAGE_DETAIL, 'Details: []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "OBJECT_STORAGE_FAILED"}}}',
     RP, 'TestRefusedDatasetStorage'),
    ("runs-500", R + 'http.go',
     'return &httpapi.Error{Code: httpapi.ServiceUnavailable, Message: "the dataset snapshots could not be read; nothing was queued (retry later)",',
     'return &httpapi.Error{Code: httpapi.InternalError, Message: "the dataset snapshots could not be read; nothing was queued (retry later)",',
     RP, 'TestRefusedDatasetStorage'),
    ("runs-says-saved", R + 'http.go',
     'nothing was queued (retry later)', 'nothing was saved (retry later)',
     RP, 'TestRefusedDatasetStorage'),
    ("runs-no-storage-gets-details", R + 'http.go',
     'return httpapi.Errorf(httpapi.ServiceUnavailable, "object storage is not configured for this deployment")',
     'return &httpapi.Error{Code: httpapi.ServiceUnavailable, Message: "object storage is not configured for this deployment", ' + STORAGE_DETAIL,
     RP, 'TestRefusedDatasetStorage'),
]

# Killed only with a database (the DB-backed tests through the handlers).
db = [
    ("db-models-handler-maps-like-every-route", M + 'http.go',
     '\t\treturn publishRefused(err)\n', '\t\treturn refused(err)\n',
     MP, 'TestPublishSnapshotStorageFailure'),
    ("db-models-cause-not-logged", M + 'http.go',
     '\t\t\th.log.WarnContext(r.Context(), "reading a publish\'s dataset snapshots failed"',
     '\t\t\t_ = storage\n\t\t\tif false {\n\t\t\t\th.log.WarnContext(r.Context(), "reading a publish\'s dataset snapshots failed"',
     MP, 'TestPublishSnapshotStorageFailure'),
    ("db-models-draft-check-ignored", M + 'models.go',
     '\tissues, err := s.issues(ctx, m.OrganizationID, res.IR, res.Issues, v.DatasetBindings)\n\tif err != nil {\n\t\treturn store.Draft{}, err\n\t}',
     '\tissues, err := s.issues(ctx, m.OrganizationID, res.IR, res.Issues, v.DatasetBindings)\n\tif err != nil {\n\t\tissues = []model.ValidationIssue{}\n\t}',
     MP, 'TestPublishSnapshotStorageFailure'),
    ("db-runs-freeze-unwrapped", R + 'runs.go',
     'return store.Run{}, bindingFailure(ctx, err)', 'return store.Run{}, err',
     AP, 'TestRunSnapshotStorageFailure'),
    ("db-runs-cause-not-logged", R + 'http.go',
     '\t\t\th.log.WarnContext(r.Context(), "reading a run\'s dataset snapshots failed"',
     '\t\t\t_ = storage\n\t\t\tif false {\n\t\t\t\th.log.WarnContext(r.Context(), "reading a run\'s dataset snapshots failed"',
     AP, 'TestRunSnapshotStorageFailure'),
]

mutants = {'unit': unit, 'db': db, 'compile': db}[which]
if which == 'db' and not os.environ.get('GEMINUS_TEST_DATABASE_URL'):
    sys.exit('db mutants need GEMINUS_TEST_DATABASE_URL (the DB-backed tests skip without it, so every mutant would survive)')


def close_blocks(name, text):
    # The two "cause-not-logged" mutants open an `if false {` around the log
    # call; close it after the call's last line.
    if 'cause-not-logged' not in name:
        return text
    marker = '"error", storage.Err.Error())\n'
    return text.replace(marker, marker + '\t\t\t}\n', 1)


out = open(log, 'w')
killed = survived = 0
for name, path, old, new, pkg, run in mutants:
    full = root + '/' + path
    src = open(full).read()
    if src.count(old) != 1:
        out.write(f'{name}: SNIPPET NOT FOUND ONCE ({src.count(old)})\n')
        survived += 1
        continue
    mutated = close_blocks(name, src.replace(old, new))
    open(full, 'w').write(mutated)
    env = dict(os.environ)
    try:
        if which == 'compile':
            r = subprocess.run(['go', 'vet', pkg], cwd=root, capture_output=True, text=True, timeout=900, env=env)
        else:
            if which == 'db':
                env['GEMINUS_REQUIRE_DB_TESTS'] = '1'
            r = subprocess.run(['go', 'test', '-count=1', '-run', '^(' + run + ')$', pkg], cwd=root, capture_output=True, text=True, timeout=900, env=env)
    finally:
        open(full, 'w').write(src)
    if which == 'compile':
        status = 'COMPILES' if r.returncode == 0 else 'DOES NOT COMPILE'
        ok = r.returncode == 0
    else:
        status = 'KILLED' if r.returncode != 0 else 'SURVIVED'
        ok = r.returncode != 0
    if ok:
        killed += 1
    else:
        survived += 1
    first = [l for l in (r.stdout + r.stderr).splitlines() if 'FAIL' in l or 'error' in l.lower() or 'storage_failure_test' in l][:3]
    out.write(f'{name}: {status} ({run}) {" | ".join(first)}\n')
if which == 'compile':
    out.write(f'TOTAL {len(mutants)} (db mutants, compile only): {killed} compile, {survived} do not\n')
else:
    out.write(f'TOTAL {len(mutants)} ({which}): {killed} killed, {survived} survived\n')
out.close()
print(open(log).read())
