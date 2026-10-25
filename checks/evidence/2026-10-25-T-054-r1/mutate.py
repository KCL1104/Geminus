"""Targeted mutants of T-054 after review round 1: the shared classification
of the dataset binding stage's errors (internal/storage/datasets: ReadError,
Failure, StorageFailure), the shared 503 (httpapi.DatasetStorageFailed) and
each route's use of them (models, runs, approval, scenarios).

The harness of rounds 1 and 2 is checks/evidence/2026-10-25-T-054/mutate.py;
this one targets the code as it is after review round 1.

Each mutant replaces one exact snippet, runs the named tests, expects a
failure, and restores the file. "unit" must be killed by the tests that need
no database; "db" by the DB-backed tests, so it needs GEMINUS_TEST_DATABASE_URL
(a disposable dt_test_* database prepared by `make db-test`) and is refused
without it; "compile" applies each db mutant, checks that the package still
builds with its tests (go vet), and restores it.

Usage: mutate.py <repo root> <log path> [unit|db|compile]
"""
import os
import subprocess
import sys

root, log = sys.argv[1], sys.argv[2]
which = sys.argv[3] if len(sys.argv) > 3 else 'unit'
D, H = 'internal/storage/datasets/', 'internal/httpapi/'
M, R, A, S = 'internal/models/', 'internal/runs/', 'internal/proposals/approval/', 'internal/scenarios/'
DP, HP = './internal/storage/datasets/', './internal/httpapi/'
MP, RP, AP, PP, SP = './internal/models/', './internal/runs/', './internal/api/', './internal/proposals/approval/', './internal/scenarios/'

READ_ERR = 'return nil, nil, &ReadError{DatasetVersionID: b.DatasetVersionID, Err: err}'
JOIN = 'return errors.Join(cerr, err)'
WHEN = 'if cerr := ctx.Err(); cerr != nil && !errors.Is(err, cerr) {'
STORAGE = 'if !errors.As(err, &re) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {'
CHANGED = 'httpapi.DatasetStorageFailed("the dataset snapshots could not be read; nothing was changed (retry later)")'
QUEUED = 'httpapi.DatasetStorageFailed("the dataset snapshots could not be read; nothing was queued (retry later)")'
SAVED = 'httpapi.DatasetStorageFailed("the dataset snapshots could not be read; nothing was saved (retry later)")'
ROUTE_TESTS = 'TestRefused|TestRefusedDatasetStorage|TestBindingFailure|TestRefusedDraftSyncStorage'

unit = [
    # datasets: only the read failure is a ReadError (L1).
    ("datasets-read-failure-untyped", D + 'datasets.go', READ_ERR,
     'return nil, nil, fmt.Errorf("reading dataset version %s: %w", b.DatasetVersionID, err)', DP, 'TestReadError'),
    ("datasets-read-failure-unnamed", D + 'datasets.go', READ_ERR,
     'return nil, nil, &ReadError{Err: err}', DP, 'TestReadError'),
    ("datasets-schema-fault-is-a-read-failure", D + 'failure.go', STORAGE,
     'if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {', DP, 'TestFailure'),
    ("datasets-storage-ignores-cancellation", D + 'failure.go', STORAGE,
     'if !errors.As(err, &re) || errors.Is(err, context.DeadlineExceeded) {', DP, 'TestFailure'),
    ("datasets-storage-ignores-deadlines", D + 'failure.go', STORAGE,
     'if !errors.As(err, &re) || errors.Is(err, context.Canceled) {', DP, 'TestFailure|TestReadError'),
    # datasets: an ended request keeps its context's error (L2).
    ("datasets-ended-request-keeps-the-store-error", D + 'failure.go', JOIN, 'return err', DP, 'TestFailure'),
    ("datasets-ended-request-loses-the-store-error", D + 'failure.go', JOIN, 'return cerr', DP, 'TestFailure'),
    ("datasets-joins-twice", D + 'failure.go', WHEN, 'if cerr := ctx.Err(); cerr != nil {', DP, 'TestFailure'),
    ("datasets-live-request-joined", D + 'failure.go', WHEN, 'if cerr := context.Cause(ctx); true {', DP, 'TestFailure'),
    ("datasets-nil-error-joined", D + 'failure.go', '\tif err == nil {\n\t\treturn nil\n\t}\n', '', DP, 'TestFailure'),
    # httpapi: the one 503 (L3).
    ("httpapi-other-code", H + 'errors.go', 'Code: "DATASET_STORAGE_FAILED"}}}', 'Code: "OBJECT_STORAGE_FAILED"}}}', HP, 'TestDatasetStorageFailed'),
    ("httpapi-other-reason", H + 'errors.go', 'Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"',
     'Reason: "storage", Code: "DATASET_STORAGE_FAILED"', HP, 'TestDatasetStorageFailed'),
    ("httpapi-500", H + 'errors.go', 'return &Error{Code: ServiceUnavailable, Message: message,',
     'return &Error{Code: InternalError, Message: message,', HP, 'TestDatasetStorageFailed'),
    ("httpapi-fixed-message", H + 'errors.go', 'return &Error{Code: ServiceUnavailable, Message: message,',
     'return &Error{Code: ServiceUnavailable, Message: "retry later",', HP, 'TestDatasetStorageFailed'),
    # models.
    ("models-check-ignores-the-request", M + 'models.go', 'return datasets.Resolution{}, nil, datasets.Failure(ctx, err)',
     'return datasets.Resolution{}, nil, err', MP, 'TestCheckStorageFailure'),
    ("models-not-mapped", M + 'http.go', '\tcase storage:\n\t\treturn ' + CHANGED, '\tcase storage && false:\n\t\treturn ' + CHANGED, MP, ROUTE_TESTS),
    ("models-every-error-is-storage", M + 'http.go', '\t_, storage := datasets.StorageFailure(err)\n',
     '\tstorage := err != nil\n', MP, ROUTE_TESTS),
    ("models-says-published", M + 'http.go', CHANGED, CHANGED.replace('changed', 'published'), MP, ROUTE_TESTS),
    # runs.
    ("runs-not-mapped", R + 'http.go', '\tcase storage:\n', '\tcase storage && false:\n', RP, ROUTE_TESTS),
    ("runs-every-error-is-storage", R + 'http.go', '\t_, storage := datasets.StorageFailure(err)\n', '\tstorage := err != nil\n', RP, ROUTE_TESTS),
    ("runs-says-saved", R + 'http.go', QUEUED, QUEUED.replace('queued', 'saved'), RP, ROUTE_TESTS),
    # approval.
    ("approval-not-mapped", A + 'http.go', '\tcase storage:\n', '\tcase storage && false:\n', PP, ROUTE_TESTS),
    ("approval-every-error-is-storage", A + 'http.go', '\t_, storage := datasets.StorageFailure(err)\n', '\tstorage := err != nil\n', PP, ROUTE_TESTS),
    ("approval-says-saved", A + 'http.go', CHANGED, CHANGED.replace('changed', 'saved'), PP, ROUTE_TESTS),
    # scenarios.
    ("scenarios-not-mapped", S + 'http.go', '\tcase storage:\n', '\tcase storage && false:\n', SP, ROUTE_TESTS),
    ("scenarios-every-error-is-storage", S + 'http.go', '\t_, storage := datasets.StorageFailure(err)\n', '\tstorage := err != nil\n', SP, ROUTE_TESTS),
    ("scenarios-says-changed", S + 'http.go', SAVED, SAVED.replace('saved', 'changed'), SP, ROUTE_TESTS),
]

# Killed only with a database (the DB-backed tests through the handlers or
# the services).
db = [
    ("db-runs-freeze-ignores-the-request", R + 'runs.go', 'return store.Run{}, datasets.Failure(ctx, err)', 'return store.Run{}, err',
     AP, 'TestRunSnapshotStorageFailure'),
    ("db-approval-revalidation-ignores-the-request", A + 'approval.go', 'return nil, datasets.Failure(ctx, err)', 'return nil, err',
     PP, 'TestApprovalDraftSyncStorageFailure'),
    ("db-scenarios-ignore-the-request", S + 'scenarios.go', 'return datasets.Failure(ctx, err)', 'return err',
     SP, 'TestScenarioDatasets'),
    ("db-models-not-logged", M + 'http.go', 'if read, ok := datasets.StorageFailure(err); ok {', 'if read, ok := datasets.StorageFailure(err); ok && false {',
     MP, 'TestPublishSnapshotStorageFailure|TestDraftSnapshotStorageFailure'),
    ("db-runs-not-logged", R + 'http.go', 'if read, ok := datasets.StorageFailure(err); ok {', 'if read, ok := datasets.StorageFailure(err); ok && false {',
     AP, 'TestRunSnapshotStorageFailure'),
    ("db-approval-not-logged", A + 'http.go', 'if read, ok := datasets.StorageFailure(err); ok {', 'if read, ok := datasets.StorageFailure(err); ok && false {',
     PP, 'TestApprovalDraftSyncStorageFailure'),
    ("db-models-draft-check-ignored", M + 'models.go',
     '\tissues, err := s.issues(ctx, m.OrganizationID, res.IR, res.Issues, v.DatasetBindings)\n\tif err != nil {\n\t\treturn store.Draft{}, err\n\t}',
     '\tissues, err := s.issues(ctx, m.OrganizationID, res.IR, res.Issues, v.DatasetBindings)\n\tif err != nil {\n\t\tissues = []model.ValidationIssue{}\n\t}',
     MP, 'TestPublishSnapshotStorageFailure|TestDraftSnapshotStorageFailure'),
    ("db-approval-draft-sync-ignored", A + 'approval.go',
     '\t\td, err := svc.Advance(ctx, tx, m, l.model, l.draft, base, v, r.ir.Name)\n\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}',
     '\t\td, err := svc.Advance(ctx, tx, m, l.model, l.draft, base, v, r.ir.Name)\n\t\tif err != nil {\n\t\t\td = l.draft\n\t\t}',
     PP, 'TestApprovalDraftSyncStorageFailure'),
]

mutants = {'unit': unit, 'db': db, 'compile': db}[which]
if which == 'db' and not os.environ.get('GEMINUS_TEST_DATABASE_URL'):
    sys.exit('db mutants need GEMINUS_TEST_DATABASE_URL (the DB-backed tests skip without it, so every mutant would survive)')

out = open(log, 'w')
killed = survived = 0
for name, path, old, new, pkg, run in mutants:
    full = root + '/' + path
    src = open(full).read()
    if src.count(old) != 1:
        out.write(f'{name}: SNIPPET NOT FOUND ONCE ({src.count(old)})\n')
        survived += 1
        continue
    open(full, 'w').write(src.replace(old, new))
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
        status, ok = ('COMPILES', True) if r.returncode == 0 else ('DOES NOT COMPILE', False)
    else:
        status, ok = ('KILLED', True) if r.returncode != 0 else ('SURVIVED', False)
        if 'build failed' in r.stdout + r.stderr or '[setup failed]' in r.stdout + r.stderr:
            status, ok = 'COMPILE ERROR (not a kill)', False
    killed += ok
    survived += not ok
    first = [l.strip() for l in (r.stdout + r.stderr).splitlines() if l.startswith('--- FAIL') or 'FAIL\t' in l or 'error' in l.lower()][:3]
    out.write(f'{name}: {status} ({run}) {" | ".join(first)}\n')
label = 'compile' if which == 'compile' else 'killed'
out.write(f'TOTAL {len(mutants)} ({which}): {killed} {label}, {survived} not\n')
out.close()
print(open(log).read())
