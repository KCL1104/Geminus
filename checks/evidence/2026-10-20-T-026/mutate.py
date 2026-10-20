"""Targeted mutants of T-026's code that the tests without a database must kill.

Each mutant replaces one exact snippet, runs the named tests, expects a
failure, and restores the file. Usage: mutate.py <repo root> <log path>.
"""
import subprocess
import sys

root, log = sys.argv[1], sys.argv[2]
A = 'internal/proposals/approval/'
P = 'internal/proposals/'
mutants = [
    ("risk-not-compared", A + 'recheck.go',
     'case string(o.Risk) != *p.Risk:', 'case false:',
     './internal/proposals/approval/', 'TestRecheckTrustsNoStoredResult'),
    ("server-risk-not-compared", A + 'recheck.go',
     'case string(o.ServerRisk) != *p.ServerRisk:', 'case false:',
     './internal/proposals/approval/', 'TestRecheckTrustsNoStoredResult'),
    ("stored-patch-trusted", A + 'recheck.go',
     'if !bytes.Equal(stored, o.Changes) || o.PatchHash != *p.PatchHash {', 'if false && !bytes.Equal(stored, o.Changes) {',
     './internal/proposals/approval/', 'TestRecheckTrustsNoStoredResult'),
    ("stored-hash-trusted", A + 'recheck.go',
     'if !bytes.Equal(stored, o.Changes) || o.PatchHash != *p.PatchHash {', 'if !bytes.Equal(stored, o.Changes) {',
     './internal/proposals/approval/', 'TestRecheckTrustsNoStoredResult'),
    ("review-outcome-ignored", A + 'recheck.go',
     'if o.Status != review.Ready {', 'if o.Status != review.Ready && o.Status == review.Ready {',
     './internal/proposals/approval/', 'TestRecheckTrustsNoStoredResult|TestRecheckAppliesTodaysRules'),
    ("scenario-reviewed-as-edit", A + 'recheck.go',
     'o, err := review.Review(review.Input{Mode: mode,', 'o, err := review.Review(review.Input{Mode: provider.ModeEdit,',
     './internal/proposals/approval/', 'TestRecheckTrustsNoStoredResult'),
    ("pack-not-asked", A + 'recheck.go',
     'Base: base, Pool: pool, Pack: pack,', 'Base: base, Pool: pool, Pack: unwrap(pack),',
     './internal/proposals/approval/', 'TestRecheckAppliesTodaysRules'),
    ("effects-not-rechecked", A + 'recheck.go',
     'Changes: changes, ExpectedEffects: p.ExpectedEffects,', 'Changes: changes, ExpectedEffects: nil,',
     './internal/proposals/approval/', 'TestRecheckTrustsNoStoredResult'),
    ("candidate-is-the-base", A + 'recheck.go',
     'candidate, err := hashing.Canonical(res.Candidate)', 'candidate, err := hashing.Canonical(base)',
     './internal/proposals/approval/', 'TestRecheckAppliesTheReviewedChanges'),
    ("name-not-cut", A + 'approval.go',
     'if r := []rune(name); len(r) > maxName {', 'if r := []rune(name); len(r) > 10*maxName {',
     './internal/proposals/approval/', 'TestScenarioName'),
    ("name-not-one-line", A + 'approval.go',
     'name := strings.Join(strings.Fields(summary), " ")', 'name := strings.TrimSpace(summary)',
     './internal/proposals/approval/', 'TestScenarioName'),
    ("blank-name", A + 'approval.go',
     '\tif name == "" {\n\t\treturn id\n\t}', '\tif name == "x" {\n\t\treturn id\n\t}',
     './internal/proposals/approval/', 'TestScenarioName'),
    ("comment-2001", A + 'approval.go',
     'utf8.RuneCountInString(c) > 2000', 'utf8.RuneCountInString(c) > 2001',
     './internal/proposals/approval/', 'TestDecisionComment'),
    ("comment-nul", A + 'approval.go',
     '|| strings.ContainsRune(c, 0) {\n\t\treturn nil, ErrInvalidComment', '{\n\t\treturn nil, ErrInvalidComment',
     './internal/proposals/approval/', 'TestDecisionComment'),
    ("other-base-is-lifecycle", A + 'http.go',
     'return &httpapi.Error{Code: httpapi.ModelVersionConflict, Message: "expected_base_version_id is not',
     'return &httpapi.Error{Code: httpapi.LifecycleConflict, Message: "expected_base_version_id is not',
     './internal/proposals/approval/', 'TestRefused'),
    ("decided-is-lifecycle", A + 'http.go',
     'return &httpapi.Error{Code: httpapi.ProposalAlreadyDecided,', 'return &httpapi.Error{Code: httpapi.LifecycleConflict,',
     './internal/proposals/approval/', 'TestRefused'),
    ("risk-path-lost", A + 'http.go',
     'Details: []httpapi.Detail{{Path: risk.Path,', 'Details: []httpapi.Detail{{Path: "",',
     './internal/proposals/approval/', 'TestRefused'),
    ("viewer-decides", A + 'approval.go',
     'var writeRoles = tenant.Writers', 'var writeRoles = tenant.Members',
     './internal/proposals/approval/', 'TestRoutesMatchContract'),
    ("conflict-shows-comment", P + 'public.go',
     'if err := decision(false); err != nil {', 'if err := decision(true); err != nil {',
     './internal/proposals/', 'TestPublicMatchesTheContract'),
    ("ready-shows-decision", P + 'public.go',
     '\tcase store.Ready:\n\t\tif err := reviewed(); err != nil {\n\t\t\treturn Proposal{}, err\n\t\t}\n',
     '\tcase store.Ready:\n\t\tif err := reviewed(); err != nil {\n\t\t\treturn Proposal{}, err\n\t\t}\n\t\t_ = decision(true)\n',
     './internal/proposals/', 'TestPublicMatchesTheContract'),
    ("decision-record-optional", P + 'public.go',
     'if p.DecidedBy == nil || p.DecidedAt == nil {\n\t\t\treturn fmt.Errorf', 'if p.DecidedBy == nil && p.DecidedAt == nil {\n\t\t\treturn fmt.Errorf',
     './internal/proposals/', 'TestPublicMatchesTheContract'),
    ("decided-at-not-utc", P + 'public.go',
     'at := timestamp(*p.DecidedAt)', 'at := p.DecidedAt.Format(time.RFC3339Nano)',
     './internal/proposals/', 'TestPublicMatchesTheContract'),
]

# The pack-not-asked mutant needs a helper that drops the test's wrapper.
helper = '\n\n// unwrap is a mutant helper.\nfunc unwrap(p domainpack.DomainPack) domainpack.DomainPack {\n\ttype inner interface{ Inner() domainpack.DomainPack }\n\tif w, ok := p.(interface{ Unwrap() domainpack.DomainPack }); ok {\n\t\treturn w.Unwrap()\n\t}\n\treturn p\n}\n'

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
    if name == 'pack-not-asked':
        mutated += helper
    if name == 'decided-at-not-utc' and '"time"' not in mutated:
        mutated = mutated.replace('"regexp"\n', '"regexp"\n\t"time"\n', 1)
    open(full, 'w').write(mutated)
    test_extra = None
    if name == 'pack-not-asked':
        tf = root + '/' + A + 'approval_test.go'
        test_extra = open(tf).read()
        open(tf, 'w').write(test_extra + '\nfunc (p strictPack) Unwrap() domainpack.DomainPack { return p.DomainPack }\n')
    try:
        r = subprocess.run(['go', 'test', '-count=1', '-run', '^(' + run + ')$', pkg], cwd=root, capture_output=True, text=True, timeout=600)
    finally:
        open(full, 'w').write(src)
        if test_extra is not None:
            open(root + '/' + A + 'approval_test.go', 'w').write(test_extra)
    status = 'KILLED' if r.returncode != 0 else 'SURVIVED'
    if r.returncode != 0:
        killed += 1
    else:
        survived += 1
    first = [l for l in (r.stdout + r.stderr).splitlines() if 'FAIL' in l or 'error' in l.lower()][:3]
    out.write(f'{name}: {status} ({run}) {" | ".join(first)}\n')
out.write(f'TOTAL {len(mutants)}: {killed} killed, {survived} survived\n')
out.close()
print(open(log).read())
