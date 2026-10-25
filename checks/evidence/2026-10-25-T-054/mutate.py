"""Targeted mutants of T-054's mapping: a dataset binding stage that cannot
read or verify the snapshots is 503 DATASET_STORAGE_FAILED on
publishVersion and createSimulation (round 1), and on patchDraft,
resetDraft and the draft sync of an edit approval (round 2, the
coordinator's extension of H-022 Q4).

Round 2 (this version) targets the code as it is after round 2: models
has one mapping (refused) and one logging helper (refuse); approval's
storageCause recognizes the draft sync's models.DatasetStorageError. The
round 1 harness, with its mutants of the then publishRefused, is in git at
3093ebb (logs: mutants-unit.log, mutants-db-compile.log); round 2's logs
are r2-mutants-unit.log and r2-mutants-db-compile.log.

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
M, R, A = 'internal/models/', 'internal/runs/', 'internal/proposals/approval/'
MP, RP, AP, PP = './internal/models/', './internal/runs/', './internal/api/', './internal/proposals/approval/'

BF_IF = 'if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {'
STORAGE_DETAIL = 'Details: []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"}}}'
MODELS_503 = 'return &httpapi.Error{Code: httpapi.ServiceUnavailable, Message: "the dataset snapshots could not be read; nothing was changed (retry later)",'
RUNS_503 = 'return &httpapi.Error{Code: httpapi.ServiceUnavailable, Message: "the dataset snapshots could not be read; nothing was queued (retry later)",'

unit = [
    # models: the binding stage inside check (publish, patch, reset, the draft sync).
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
    # models: refused, the one mapping of every model route.
    ("models-not-mapped", M + 'http.go',
     '\tcase errors.As(err, &storage):\n', '\tcase false && errors.As(err, &storage):\n',
     MP, 'TestRefusedDatasetStorage'),
    ("models-cause-sent", M + 'http.go',
     STORAGE_DETAIL, 'Details: []httpapi.Detail{{Path: "", Reason: storage.Err.Error(), Code: "DATASET_STORAGE_FAILED"}}}',
     MP, 'TestRefusedDatasetStorage'),
    ("models-other-code", M + 'http.go',
     STORAGE_DETAIL, 'Details: []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "OBJECT_STORAGE_FAILED"}}}',
     MP, 'TestRefusedDatasetStorage'),
    ("models-no-details", M + 'http.go',
     ',\n\t\t\t' + STORAGE_DETAIL, '}',
     MP, 'TestRefusedDatasetStorage'),
    ("models-500", M + 'http.go',
     MODELS_503, MODELS_503.replace('httpapi.ServiceUnavailable', 'httpapi.InternalError'),
     MP, 'TestRefusedDatasetStorage'),
    ("models-says-published", M + 'http.go',
     'nothing was changed (retry later)', 'nothing was published (retry later)',
     MP, 'TestRefusedDatasetStorage'),
    ("models-no-storage-gets-details", M + 'http.go',
     'return httpapi.Errorf(httpapi.ServiceUnavailable, "the version binds datasets and dataset storage is not available")',
     'return &httpapi.Error{Code: httpapi.ServiceUnavailable, Message: "the version binds datasets and dataset storage is not available", ' + STORAGE_DETAIL,
     MP, 'TestRefusedDatasetStorage'),
    # approval: the draft sync's storage failure is the revalidation's 503.
    ("approval-draft-sync-not-recognized", A + 'approval.go',
     '\tif errors.As(err, &draft) {\n', '\tif false && errors.As(err, &draft) {\n',
     PP, 'TestRefusedDraftSyncStorage'),
    ("approval-draft-sync-logs-the-wrapper", A + 'approval.go',
     '\t\treturn draft.Err, true\n', '\t\treturn err, true\n',
     PP, 'TestRefusedDraftSyncStorage'),
    ("approval-revalidation-not-recognized", A + 'approval.go',
     '\tif errors.As(err, &own) {\n', '\tif false && errors.As(err, &own) {\n',
     PP, 'TestRefused|TestRefusedDraftSyncStorage'),
    ("approval-not-mapped", A + 'http.go',
     '\tcase storage:\n', '\tcase storage && false:\n',
     PP, 'TestRefused|TestRefusedDraftSyncStorage'),
    ("approval-everything-is-storage", A + 'http.go',
     '\t_, storage := storageCause(err)\n', '\t_, storage := storageCause(err)\n\tstorage = storage || err != nil\n',
     PP, 'TestRefused|TestRefusedDraftSyncStorage'),
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
     RUNS_503, RUNS_503.replace('httpapi.ServiceUnavailable', 'httpapi.InternalError'),
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
    # models: each write that reads snapshots logs the cause (refuse).
    ("db-models-publish-not-logged", M + 'http.go',
     'return h.refuse(r, sc, "publishVersion", id, err)', 'return refused(err)',
     MP, 'TestPublishSnapshotStorageFailure'),
    ("db-models-patch-not-logged", M + 'http.go',
     'return h.refuse(r, sc, "patchDraft", id, err)', 'return refused(err)',
     MP, 'TestDraftSnapshotStorageFailure'),
    ("db-models-reset-not-logged", M + 'http.go',
     'return h.refuse(r, sc, "resetDraft", id, err)', 'return refused(err)',
     MP, 'TestDraftSnapshotStorageFailure'),
    ("db-models-cause-not-logged", M + 'http.go',
     '\t\th.log.WarnContext(r.Context(), "reading a model write\'s dataset snapshots failed"',
     '\t\t_ = storage\n\t\tif false {\n\t\t\th.log.WarnContext(r.Context(), "reading a model write\'s dataset snapshots failed"',
     MP, 'TestPublishSnapshotStorageFailure|TestDraftSnapshotStorageFailure'),
    # models: the draft that follows a new version swallows its failure.
    ("db-models-draft-check-ignored", M + 'models.go',
     '\tissues, err := s.issues(ctx, m.OrganizationID, res.IR, res.Issues, v.DatasetBindings)\n\tif err != nil {\n\t\treturn store.Draft{}, err\n\t}',
     '\tissues, err := s.issues(ctx, m.OrganizationID, res.IR, res.Issues, v.DatasetBindings)\n\tif err != nil {\n\t\tissues = []model.ValidationIssue{}\n\t}',
     MP, 'TestPublishSnapshotStorageFailure|TestDraftSnapshotStorageFailure'),
    # models: a patched draft whose binding stage failed is saved anyway.
    ("db-models-patch-check-ignored", M + 'models.go',
     '\t\tissues, err := s.issues(ctx, m.OrganizationID, res.Validation.IR, res.Validation.Issues, base.DatasetBindings)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}',
     '\t\tissues, err := s.issues(ctx, m.OrganizationID, res.Validation.IR, res.Validation.Issues, base.DatasetBindings)\n\t\tif err != nil {\n\t\t\tissues = []model.ValidationIssue{}\n\t\t}',
     MP, 'TestDraftSnapshotStorageFailure'),
    # approval: the draft sync's failure is swallowed, or not logged.
    ("db-approval-draft-sync-ignored", A + 'approval.go',
     '\t\td, err := svc.Advance(ctx, tx, m, l.model, l.draft, base, v, r.ir.Name)\n\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}',
     '\t\td, err := svc.Advance(ctx, tx, m, l.model, l.draft, base, v, r.ir.Name)\n\t\tif err != nil {\n\t\t\td = l.draft\n\t\t}',
     PP, 'TestApprovalDraftSyncStorageFailure'),
    ("db-approval-log-skipped", A + 'http.go',
     'if cause, ok := storageCause(err); ok {', 'if cause, ok := storageCause(err); ok && false {',
     PP, 'TestApprovalDraftSyncStorageFailure'),
    # runs.
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
    # The "cause-not-logged" mutants open an `if false {` around the log
    # call; close it after the call's last line.
    if 'cause-not-logged' not in name:
        return text
    marker = '"error", storage.Err.Error())\n'
    return text.replace(marker, marker + '\t\t}\n', 1)


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
    first = [l for l in (r.stdout + r.stderr).splitlines() if 'FAIL' in l or 'error' in l.lower() or '_test.go' in l][:3]
    out.write(f'{name}: {status} ({run}) {" | ".join(first)}\n')
if which == 'compile':
    out.write(f'TOTAL {len(mutants)} (db mutants, compile only): {killed} compile, {survived} do not\n')
else:
    out.write(f'TOTAL {len(mutants)} ({which}): {killed} killed, {survived} survived\n')
out.close()
print(open(log).read())
