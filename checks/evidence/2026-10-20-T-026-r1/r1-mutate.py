"""Targeted mutants for T-026 review round 1 (M1, L1, I2, I3 and the DB-only L2-L4).

Each mutant replaces one exact snippet (it must occur once), runs `go vet`
on the mutated package (a compile failure is INVALID, never KILLED), then
runs the packages' tests without a database (DB-backed tests skip), records
KILLED or SURVIVED, and restores the file byte for byte. Mutants marked
db-only can only be killed by a DB-backed test: without a database they are
expected to survive, and the coordinator runs them with
GEMINUS_TEST_DATABASE_URL set (the DB-backed tests then run).
Usage: python3 -I r1-mutate.py <repo root> <log path>
"""
import os
import subprocess
import sys

root, log = sys.argv[1], sys.argv[2]
A = 'internal/proposals/approval/'
S = 'internal/storage/proposals/'
UNIT = ['./internal/proposals/approval/', './internal/proposals/', './internal/storage/proposals/', './internal/api/']

mutants = [
    # M1: the fingerprint holds the proposal (unit: TestFingerprint; DB: TestTheKeyNamesTheProposal, TestApprovalRefusals).
    ("fingerprint-ignores-proposal", "", A + 'approval.go',
     'map[string]any{"proposal_id": proposalID, "body": jsontext.Value(canonical)}',
     'map[string]any{"proposal_id": "", "body": jsontext.Value(canonical)}'),
    ("fingerprint-ignores-body", "", A + 'approval.go',
     'map[string]any{"proposal_id": proposalID, "body": jsontext.Value(canonical)}',
     'map[string]any{"proposal_id": proposalID, "body": jsontext.Value(append(canonical[:0:0], "{}"...))}'),
    # I3: the normalized parsed body.
    ("fingerprint-not-normalized", "", A + 'approval.go',
     'normalized, err := json.Marshal(parsed, json.Deterministic(true))',
     'normalized, err := append([]byte(nil), body...), error(nil)'),
    ("fingerprint-ignores-unknown-members", "", A + 'approval.go',
     'json.Unmarshal(body, &parsed, json.RejectUnknownMembers(true))',
     'json.Unmarshal(body, &parsed)'),
    # L1: a replayed error answer names its own request.
    ("replay-keeps-request-id", "", A + 'approval.go',
     'e.Error.RequestID = requestID', '_ = requestID'),
    ("replay-rewrites-nothing", "", A + 'approval.go',
     'if r.Status < 400 {', 'if r.Status < 600 {'),
    # I2: a storage failure is 503 DATASET_STORAGE_FAILED.
    ("storage-failure-is-500", "", A + 'approval.go',
     'return &DatasetStorageError{Err: err}', 'return err'),
    ("cancel-is-storage-failure", "", A + 'approval.go',
     'if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {', 'if false {'),
    ("storage-failure-unmapped", "", A + 'http.go',
     'case errors.As(err, &storage):', 'case false && errors.As(err, &storage):'),
    ("storage-detail-code-dropped", "", A + 'http.go',
     'Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"}', 'Reason: "the dataset storage failed"}'),
    ("apply-ignores-binding-failure", "db-only", A + 'approval.go',
     'return nil, bindingFailure(ctx, err)', 'return nil, err'),
    # L2-L4, the reviewer's surviving mutants, now with DB tests.
    ("manifest-equality-removed", "db-only", A + 'approval.go',
     'if manifest == nil || !slices.Equal(manifest, sorted(p.DatasetBindings)) {',
     'if manifest == nil || (false && !slices.Equal(manifest, sorted(p.DatasetBindings))) {'),
    ("lock-draft-before-model", "db-only", A + 'approval.go',
     '\tmdl, err := models.LockModel(ctx, tx, org, p.ModelID)\n\tif err != nil {\n\t\treturn locked{}, fmt.Errorf("approval: the model of proposal %s: %w", id, err)\n\t}\n\td, err := models.LockDraft(ctx, tx, org, p.ModelID)\n\tif err != nil {\n\t\treturn locked{}, fmt.Errorf("approval: the draft of proposal %s: %w", id, err)\n\t}',
     '\td, err := models.LockDraft(ctx, tx, org, p.ModelID)\n\tif err != nil {\n\t\treturn locked{}, fmt.Errorf("approval: the draft of proposal %s: %w", id, err)\n\t}\n\tmdl, err := models.LockModel(ctx, tx, org, p.ModelID)\n\tif err != nil {\n\t\treturn locked{}, fmt.Errorf("approval: the model of proposal %s: %w", id, err)\n\t}'),
    ("storage-reject-other-org", "db-only", S + 'proposals.go',
     "WHERE organization_id=$1 AND id=$2 AND status IN ('ready','needs_input')`, org, id, user, comment))",
     "WHERE id=$2 AND status IN ('ready','needs_input') AND $1::text IS NOT NULL`, org, id, user, comment))"),
    ("storage-approve-other-org", "db-only", S + 'proposals.go',
     "WHERE organization_id=$1 AND id=$2 AND status='ready'`, org, id, user, newVersionID, scenarioID, comment))",
     "WHERE id=$2 AND status='ready' AND $1::text IS NOT NULL`, org, id, user, newVersionID, scenarioID, comment))"),
]

db = 'with GEMINUS_TEST_DATABASE_URL' if os.environ.get('GEMINUS_TEST_DATABASE_URL') else 'without a database'
out = open(log, 'w')
out.write(f'T-026 review round 1 mutants, {db}\n')
counts = {}
for name, kind, path, old, new in mutants:
    full = root + '/' + path
    src = open(full).read()
    n = src.count(old)
    if n != 1:
        out.write(f'{name}: SNIPPET FOUND {n} TIMES, NOT RUN\n')
        out.flush()
        counts['NOT RUN'] = counts.get('NOT RUN', 0) + 1
        continue
    open(full, 'w').write(src.replace(old, new))
    try:
        pkg = './' + path.rsplit('/', 1)[0] + '/'
        v = subprocess.run(['go', 'vet', pkg], cwd=root, capture_output=True, text=True, timeout=900)
        if v.returncode != 0:
            status, detail = 'INVALID', ' | '.join((v.stdout + v.stderr).splitlines()[:3])
        else:
            r = subprocess.run(['go', 'test', '-count=1'] + UNIT, cwd=root, capture_output=True, text=True, timeout=1800)
            status = 'KILLED' if r.returncode != 0 else 'SURVIVED'
            detail = ' | '.join([l for l in (r.stdout + r.stderr).splitlines() if l.startswith('--- FAIL') or l.startswith('FAIL')][:4])
    finally:
        open(full, 'w').write(src)
    label = f'{name} ({kind})' if kind else name
    counts[status] = counts.get(status, 0) + 1
    out.write(f'{label}: {status} {detail}\n')
    out.flush()
out.write(f'TOTAL {len(mutants)}: {counts}\n')
out.close()
print(open(log).read())
