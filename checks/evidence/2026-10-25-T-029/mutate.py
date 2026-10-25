"""Targeted mutants of T-029's code (internal/results/compare, internal/comparisons).

Each mutant replaces one exact snippet, runs the named tests, expects a
failure, and restores the file. A mutant that does not compile is reported
as BUILD (not counted as killed): every mutant here must build, so a test,
not the compiler, has to catch it. The "unit" set must be killed by the
tests that need no database; the "db" set by the DB-backed tests of
internal/api, so it needs GEMINUS_TEST_DATABASE_URL (a disposable dt_test_*
database prepared by `make db-test`) and is refused without it.

Usage: mutate.py <repo root> <log path> [unit|db]
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
    # Baseline zero (TEST-022): the relative delta must be null with BASELINE_ZERO.
    ("baseline-zero-not-special", C, '\t\tif *b == 0 {\n', '\t\tif false {\n', CP, 'TestBaselineZero|TestGolden'),
    ("baseline-zero-drops-absolute", C, '\t\tif *b == 0 {\n\t\t\tm.Reason = reason(ReasonBaselineZero)\n',
     '\t\tif *b == 0 {\n\t\t\tm.AbsoluteDelta = nil\n\t\t\tm.Reason = reason(ReasonBaselineZero)\n', CP, 'TestBaselineZero|TestGolden'),
    # The formulas (§7).
    ("absolute-reversed", C, 'abs := *s - *b', 'abs := *b - *s', CP, 'TestDeltas'),
    ("relative-over-scenario", C, 'rel := abs / *b', 'rel := abs / *s', CP, 'TestDeltas'),
    ("out-of-range-delta-kept", C, '\t\tif !finite(abs) {\n', '\t\tif math.IsNaN(abs) {\n', CP, 'TestOutOfRange'),
    ("out-of-range-relative-kept", C, '\t\tif !finite(rel) {\n', '\t\tif math.IsNaN(rel) {\n', CP, 'TestOutOfRange'),
    # Null propagation: unavailable is null, never 0, and has no delta.
    ("unavailable-summary-as-zero", C,
     '\t\t\treturn nil, inconsistent("%s: an unavailable summary with a %s", at, stat)\n\t\t}\n\t\treturn nil, nil\n',
     '\t\t\treturn nil, inconsistent("%s: an unavailable summary with a %s", at, stat)\n\t\t}\n\t\tzero := 0.0\n\t\treturn &zero, nil\n',
     CP, 'TestUnavailable|TestDORAStaysUnavailable'),
    ("unavailable-wait-as-zero", C,
     '\t\t\treturn nil, inconsistent("%s: an unavailable queue with a %s", at, stat)\n\t\t}\n\t\treturn nil, nil\n',
     '\t\t\treturn nil, inconsistent("%s: an unavailable queue with a %s", at, stat)\n\t\t}\n\t\tzero := 0.0\n\t\treturn &zero, nil\n',
     CP, 'TestUnavailable'),
    ("missing-as-both-unavailable", C, 'm.Status, m.Reason = Unavailable, reason(ReasonMissingInBaseline)',
     'm.Status, m.Reason = Unavailable, reason(ReasonBothUnavailable)', CP, 'TestMissing|TestDORAStaysUnavailable'),
    ("missing-scenario-ignored", C, '\tcase !sok:\n', '\tcase !sok && false:\n', CP, 'TestMissing'),
    ("negative-zero-kept", C, '\tif x == 0 {\n\t\tx = 0\n\t}\n', '', CP, 'TestNoNegativeZero|TestBaselineZero'),
    # Incomparable detection (§7, D-088 (a)).
    ("seed-not-compared", C, '{baseline.Config.Seed != scenario.Config.Seed, SeedMismatch}', '{false, SeedMismatch}', CP, 'TestIncomparable'),
    ("engine-not-compared", C, '{baseline.EngineVersion != scenario.EngineVersion, EngineVersionMismatch}', '{false, EngineVersionMismatch}',
     CP, 'TestIncomparable'),
    ("version-not-compared", C, '{baseline.ModelVersionID != scenario.ModelVersionID, ModelVersionMismatch}', '{false, ModelVersionMismatch}',
     CP, 'TestIncomparable'),
    ("manifest-not-compared", C, '{baseline.DatasetManifestHash != scenario.DatasetManifestHash, DatasetManifestMismatch}',
     '{false, DatasetManifestMismatch}', CP, 'TestIncomparable'),
    ("replications-not-compared", C, '{baseline.Config.Replications != scenario.Config.Replications, ReplicationsMismatch}',
     '{false, ReplicationsMismatch}', CP, 'TestIncomparable'),
    ("warmup-not-compared", C, '{bw != sw, WarmupMismatch}', '{bw != sw && false, WarmupMismatch}', CP, 'TestIncomparable'),
    ("baseline-patch-not-checked", C, '{!baseEmpty, BaselinePatchNotEmpty}', '{!baseEmpty && false, BaselinePatchNotEmpty}', CP, 'TestIncomparable'),
    ("horizon-by-spelling", C, '{bh != sh, HorizonMismatch}', '{bh != sh && false || baseline.Config.Horizon != scenario.Config.Horizon, HorizonMismatch}',
     CP, 'TestComparableAcrossSpellings'),
    ("incomparable-keeps-deltas", C, '\tcase !comparable:\n', '\tcase !comparable && false:\n', CP, 'TestIncomparable'),
    # Ordering never depends on map iteration.
    ("keys-in-map-order", C, '\tslices.Sort(out) // byte order, whatever the map\'s\n', '', CP, 'TestOrder'),
    ("union-not-sorted", C, '\t\t\tslices.Sort(ks)\n\t\t\tks = slices.Compact(ks)\n', '\t\t\tks = slices.Compact(ks)\n', CP, 'TestOrder'),
    # Warnings and truncation.
    ("single-replication-off-by-one", C, 'baseline.Config.Replications < 2 || scenario.Config.Replications < 2',
     'baseline.Config.Replications < 1 || scenario.Config.Replications < 1', CP, 'TestWarnings'),
    ("censoring-of-the-baseline-only", C, 'if censored(baseline.Result) || censored(scenario.Result) {', 'if censored(baseline.Result) {',
     CP, 'TestWarnings'),
    ("truncation-off-by-one", C, '\tif len(metrics) > MaxMetrics {\n', '\tif len(metrics) >= MaxMetrics {\n', CP, 'TestTruncation'),
    # Fail closed on a result that is not the run's.
    ("result-of-another-run", C, 'if res.RunID != r.ID || res.Status', 'if res.Status', CP, 'TestInconsistent'),
    ("same-run-compared", C, '\tif baseline.ID == scenario.ID {\n\t\treturn Comparison{}, ErrSameRun\n',
     '\tif false {\n\t\treturn Comparison{}, ErrSameRun\n', CP, 'TestSameRun'),
    # Evidence paths (D-090).
    ("non-statistic-resolves", C, '\tif !slices.Contains(g.stats, stat) {\n', '\tif false {\n', CP, 'TestResolve'),
    ("absent-key-resolves", C, '\tif !present {\n\t\treturn nil, fmt.Errorf("%w: %q", ErrUnknownPath, path)\n',
     '\tif !present && false {\n\t\treturn nil, fmt.Errorf("%w: %q", ErrUnknownPath, path)\n', CP, 'TestResolve'),
    ("bad-escape-accepted", C, "if p[j] == '~' && (j+1 == len(p) || (p[j+1] != '0' && p[j+1] != '1')) {", "if false {", CP, 'TestPathEscaping'),
    # The service and its adapter.
    ("viewer-cannot-compare", S + 'comparisons.go', 'var readRoles = tenant.Members', 'var readRoles = tenant.Writers', SP, 'TestRoutesMatchContract'),
    ("any-id-kind", S + 'http.go', 'regexp.MustCompile(`^sim_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)', 'regexp.MustCompile(`^[a-z]+_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)',
     SP, 'TestRunQuery'),
    ("same-run-not-refused", S + 'http.go', '\tif got[0] == got[1] {\n', '\tif false {\n', SP, 'TestRunQuery'),
    ("repeated-parameter-accepted", S + 'http.go', 'if len(vs) != 1 || !runIDs.MatchString(vs[0]) {', 'if len(vs) == 0 || !runIDs.MatchString(vs[0]) {',
     SP, 'TestRunQuery'),
    ("not-ready-as-not-found", S + 'http.go', 'return &httpapi.Error{Code: httpapi.ResultNotReady,', 'return &httpapi.Error{Code: httpapi.NotFound,',
     SP, 'TestRefused'),
    ("not-found-names-no-parameter", S + 'http.go',
     'return &httpapi.Error{Code: httpapi.NotFound, Message: "not found", Details: []httpapi.Detail{{Path: "", Reason: re.Param}}}',
     'return &httpapi.Error{Code: httpapi.NotFound, Message: "not found"}', SP, 'TestRefused'),
    ("inconsistent-result-shown-as-404", S + 'http.go', '\tcase errors.Is(err, compare.ErrSameRun):\n',
     '\tcase errors.Is(err, compare.ErrInconsistent):\n\t\treturn httpapi.Errorf(httpapi.NotFound, "not found")\n\tcase errors.Is(err, compare.ErrSameRun):\n',
     SP, 'TestRefused'),
    ("lenient-result-decoding", S + 'comparisons.go', '\tif err := model.Decode(data, &res); err != nil {\n',
     '\tif err := model.Decode(data, &res); err != nil && false {\n', SP, 'TestDecodeResult'),
    ("patch-not-passed", S + 'comparisons.go', 'ScenarioPatch: r.ScenarioPatch, Config: r.Config, Result: res}',
     'Config: r.Config, Result: res}', SP, 'TestInput'),
]

# Killed only with a database (internal/api's DB-backed comparison tests).
API = './internal/api/'
db = [
    ("db-status-not-checked", S + 'comparisons.go', '\t\t\tif r.Status != store.Completed {\n', '\t\t\tif r.Status != store.Completed && false {\n', API,
     'TestComparisonRefusals|TestComparisonClosedLoop'),
    ("db-scenario-named-as-baseline", S + 'comparisons.go', '\t\t\t\treturn &RunError{Param: params[i], Err: ErrNotFound}\n',
     '\t\t\t\treturn &RunError{Param: params[0], Err: ErrNotFound}\n', API, 'TestComparisonRoleMatrix|TestComparisonRefusals'),
    ("db-readiness-before-existence", S + 'comparisons.go',
     '\t\t\tif err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\trows[i] = r\n',
     '\t\t\tif err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tif r.Status != store.Completed {\n\t\t\t\treturn &RunError{Param: params[i], Err: ErrNotReady}\n\t\t\t}\n\t\t\trows[i] = r\n',
     API, 'TestComparisonRefusals'),
    # Not a mutant here: removing m.Require(readRoles...) is equivalent for an
    # operation every role may perform (a non-member never reaches it:
    # tenant.DB.Run refuses with ErrNoMembership); the unit mutant
    # viewer-cannot-compare covers the role list instead.
    ("db-results-swapped", S + 'comparisons.go', 'c, err := compare.Compare(inputs[0], inputs[1])', 'c, err := compare.Compare(inputs[1], inputs[0])',
     API, 'TestComparisonClosedLoop'),
    ("db-route-not-registered", 'internal/api/api.go', '\tcomparisons.Register(mux, log, v, db, &comparisons.Service{DB: db})\n',
     '\t_ = comparisons.Register\n', API, 'TestComparisonClosedLoop'),
]

if which == 'dbbuild':
    # Without a database: only that each DB mutant applies once and builds
    # (go vet of its package and of internal/api); its tests are NOT RUN.
    out = open(log, 'w')
    for name, path, old, new, pkg, run in db:
        full = root + '/' + path
        src = open(full).read()
        if src.count(old) != 1:
            out.write(f'{name}: SNIPPET NOT FOUND ONCE ({src.count(old)})\n')
            continue
        open(full, 'w').write(src.replace(old, new))
        try:
            vet = subprocess.run(['go', 'vet', './internal/comparisons/', API], cwd=root, capture_output=True, text=True, timeout=600)
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
        # The mutant must build (with its tests): otherwise the compiler,
        # not a test, would be what catches it.
        vet = subprocess.run(['go', 'vet', pkg], cwd=root, capture_output=True, text=True, timeout=600, env=env)
        r = None
        if vet.returncode == 0:
            r = subprocess.run(['go', 'test', '-count=1', '-run', '^(' + run + ')$', pkg], cwd=root, capture_output=True, text=True, timeout=900, env=env)
    finally:
        open(full, 'w').write(src)
    text = (r.stdout + r.stderr) if r else vet.stderr
    if r is None or '[build failed]' in text or 'setup failed' in text:
        build += 1
        out.write(f'{name}: BUILD ({run}) {" | ".join(text.splitlines()[:2])}\n')
        continue
    status = 'KILLED' if r.returncode != 0 else 'SURVIVED'
    if r.returncode != 0:
        killed += 1
    else:
        survived += 1
    first = [l for l in text.splitlines() if l.startswith('--- FAIL') or 'panic' in l][:3]
    out.write(f'{name}: {status} ({run}) {" | ".join(first)}\n')
out.write(f'TOTAL {len(mutants)} ({which}): {killed} killed, {survived} survived, {build} did not build\n')
out.close()
print(open(log).read())
