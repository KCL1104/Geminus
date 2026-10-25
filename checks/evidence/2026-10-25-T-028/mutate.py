"""Targeted mutants of T-028's code.

Each mutant replaces one exact snippet, runs the named tests, expects a
failure, and restores the file. The "unit" set must be killed by the tests
that need no database; the "db" set by the DB-backed tests, so it needs
GEMINUS_TEST_DATABASE_URL (a disposable dt_test_* database prepared by
`make db-test`) and is refused without it.

Usage: mutate.py <repo root> <log path> [unit|db]
"""
import os
import subprocess
import sys

root, log = sys.argv[1], sys.argv[2]
which = sys.argv[3] if len(sys.argv) > 3 else 'unit'
S = 'internal/scenarios/'
PKG = './internal/scenarios/'

unit = [
    # The allowlist (D-021): a person's scenario under another mode's.
    ("manual-draft-allowlist", S + 'check.go',
     'res, err := patch.Apply(base, doc, patch.Scenario)', 'res, err := patch.Apply(base, doc, patch.ManualDraft)',
     PKG, 'TestCheck|TestTheAIScenarioModeTakesTheSameRules'),
    ("ai-edit-allowlist", S + 'check.go',
     'res, err := patch.Apply(base, doc, patch.Scenario)', 'res, err := patch.Apply(base, doc, patch.AIEdit)',
     PKG, 'TestCheck|TestTheAIScenarioModeTakesTheSameRules'),
    ("patch-issues-not-in-the-request", S + 'check.go',
     'capped(prefixed("/patch", res.PatchIssues))', 'capped(prefixed("", res.PatchIssues))',
     PKG, 'TestCheck|TestCheckRefusals'),
    ("invalid-candidate-accepted", S + 'check.go',
     '\tif !res.Valid() {\n', '\tif false && !res.Valid() {\n',
     PKG, 'TestCheck|TestCheckRefusals'),
    ("pack-not-asked", S + 'check.go',
     'if issues := pack.Validate(ir); slices.ContainsFunc(issues, isError) {',
     'if issues := pack.Validate(ir); false && slices.ContainsFunc(issues, isError) {',
     PKG, 'TestCheckRefusals'),
    ("raw-patch-stored", S + 'check.go',
     'return checked{patch: canonical, hash: hash, ir: ir}, nil', 'return checked{patch: doc, hash: hash, ir: ir}, nil',
     PKG, 'TestCheckStoresTheCanonicalPatch'),
    ("unknown-domain-is-invalid", S + 'check.go',
     'return checked{}, fmt.Errorf("scenarios: the domain of the base: %w", err)',
     'return checked{}, &InvalidError{Issues: []model.ValidationIssue{model.NewIssue("DOMAIN_MISMATCH", "/domain", err.Error(), model.SeverityError)}}',
     PKG, 'TestCheckRefusals'),
    ("blank-name-accepted", S + 'check.go',
     'if strings.TrimFunc(name, unicode.IsSpace) == "" {', 'if strings.TrimFunc(name, unicode.IsSpace) == "" && name == "" {',
     PKG, 'TestNameIssues'),
    ("hash-not-checked-on-read", S + 'public.go',
     '\tif hash != r.PatchHash {\n', '\tif hash != r.PatchHash && false {\n',
     PKG, 'TestPublic'),
    ("jsonb-spelling-shown", S + 'public.go',
     'Patch: jsontext.Value(canonical),', 'Patch: jsontext.Value(r.Patch),',
     PKG, 'TestPublic'),
    ("created-at-not-utc", S + 'public.go',
     'CreatedAt: timestamp(r.CreatedAt)}, nil', 'CreatedAt: r.CreatedAt.Format(time.RFC3339Nano)}, nil',
     PKG, 'TestPublic'),
    ("source-not-checked", S + 'public.go',
     'r.SourceProposalID != nil && !proposalIDs.MatchString(*r.SourceProposalID)', 'false',
     PKG, 'TestPublic'),
    ("fingerprint-ignores-version", S + 'scenarios.go',
     'map[string]any{"model_version_id": versionID, "body"', 'map[string]any{"model_version_id": "", "body"',
     PKG, 'TestFingerprint'),
    # Not "the fingerprint of the spelling": hashing.Of canonicalizes the
    # whole fingerprint document again, so leaving out the body's own
    # canonicalization is an equivalent mutant (run 1 of this harness:
    # SURVIVED, mutants-unit-r1.log); the spelling case of TestFingerprint
    # holds either way.
    ("fingerprint-ignores-the-body", S + 'scenarios.go',
     '"body": jsontext.Value(canonical)}, json.Deterministic(true))',
     '"body": jsontext.Value(append(canonical[:0:0], "{}"...))}, json.Deterministic(true))',
     PKG, 'TestFingerprint'),
    ("cursor-of-any-version", S + 'scenarios.go',
     '|| c.Base != versionID ||', '||',
     PKG, 'TestCursor'),
    ("cursor-of-any-organization", S + 'scenarios.go',
     '|| c.Org != org ||', '||',
     PKG, 'TestCursor'),
    ("viewer-creates", S + 'scenarios.go',
     '\twriteRoles = tenant.Writers\n', '\twriteRoles = tenant.Members\n',
     PKG, 'TestRoutesMatchContract'),
    ("invalid-patch-is-a-request-error", S + 'http.go',
     'return &httpapi.Error{Code: httpapi.ModelValidationFailed,', 'return &httpapi.Error{Code: httpapi.RequestValidationFailed,',
     PKG, 'TestRefused'),
    ("storage-cause-sent", S + 'http.go',
     'Details: []httpapi.Detail{{Path: "", Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"}}}',
     'Details: []httpapi.Detail{{Path: "", Reason: storage.Err.Error(), Code: "DATASET_STORAGE_FAILED"}}}',
     PKG, 'TestRefused'),
    ("no-storage-is-not-found", S + 'http.go',
     'return httpapi.Errorf(httpapi.ServiceUnavailable, "the version binds datasets', 'return httpapi.Errorf(httpapi.NotFound, "the version binds datasets',
     PKG, 'TestRefused'),
]

# Killed only with a database (the DB-backed tests of internal/scenarios).
db = [
    ("db-no-role-check", S + 'scenarios.go',
     '\t\tif err := m.Require(writeRoles...); err != nil {\n\t\t\treturn err\n\t\t}\n\t\treplay',
     '\t\treplay',
     PKG, 'TestScenarioRoles'),
    ("db-no-binding-stage", S + 'scenarios.go',
     '\t\tif !resolution.Valid() {\n\t\t\treturn &InvalidError', '\t\tif false && !resolution.Valid() {\n\t\t\treturn &InvalidError',
     PKG, 'TestScenarioDatasets'),
    ("db-storage-not-required", S + 'scenarios.go',
     'if len(v.DatasetBindings) > 0 && s.Objects == nil {', 'if false {',
     PKG, 'TestScenarioDatasets'),
    ("db-storage-failure-unwrapped", S + 'scenarios.go',
     '\t\t\treturn bindingFailure(ctx, err)\n', '\t\t\treturn err\n',
     PKG, 'TestScenarioDatasets'),
    ("db-name-in-audit", S + 'scenarios.go',
     '"operations": len(ops)}}); err != nil {', '"operations": len(ops), "name": row.Name}}); err != nil {',
     PKG, 'TestCreateManualScenario'),
    ("db-no-audit", S + 'scenarios.go',
     'Action: "scenario.create", TargetType: "scenario"', 'Action: "scenario.made", TargetType: "scenario"',
     PKG, 'TestCreateManualScenario'),
    ("db-list-of-the-model", 'internal/storage/scenarios/scenarios.go',
     'WHERE organization_id=$1 AND base_version_id=$2\n\t\t\tORDER BY created_at, id LIMIT $3',
     'WHERE organization_id=$1 AND (base_version_id=$2 OR true)\n\t\t\tORDER BY created_at, id LIMIT $3',
     PKG, 'TestCreateManualScenario'),
    ("db-list-without-the-organization", 'internal/storage/scenarios/scenarios.go',
     'WHERE organization_id=$1 AND base_version_id=$2\n\t\t\tORDER BY created_at, id LIMIT $3',
     'WHERE ($1::text IS NOT NULL) AND base_version_id=$2\n\t\t\tORDER BY created_at, id LIMIT $3',
     './internal/storage/scenarios/', 'TestListAndGet'),
    ("db-next-cursor-lost", S + 'scenarios.go',
     '\t\t\tpage.NextCursor = &c\n', '\t\t\t_ = c\n',
     PKG, 'TestListPages'),
]

mutants = {'unit': unit, 'db': db}[which]
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
    mutated = src.replace(old, new)
    open(full, 'w').write(mutated)
    env = dict(os.environ)
    if which == 'db':
        env['GEMINUS_REQUIRE_DB_TESTS'] = '1'
    try:
        r = subprocess.run(['go', 'test', '-count=1', '-run', '^(' + run + ')$', pkg], cwd=root, capture_output=True, text=True, timeout=900, env=env)
    finally:
        open(full, 'w').write(src)
    status = 'KILLED' if r.returncode != 0 else 'SURVIVED'
    if r.returncode != 0:
        killed += 1
    else:
        survived += 1
    first = [l for l in (r.stdout + r.stderr).splitlines() if 'FAIL' in l or 'error' in l.lower()][:3]
    out.write(f'{name}: {status} ({run}) {" | ".join(first)}\n')
out.write(f'TOTAL {len(mutants)} ({which}): {killed} killed, {survived} survived\n')
out.close()
print(open(log).read())
