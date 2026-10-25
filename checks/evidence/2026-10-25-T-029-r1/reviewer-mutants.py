"""Reviewer (H-003) mutants for T-029, applied only through `go test -overlay`
(the worktree is never written). Each mutant replaces one exact snippet in a
copy of the source file under the scratchpad, then runs the whole test
package (not only a named test). Usage: mutants.py <worktree> <scratch> <log>
"""
import json
import os
import subprocess
import sys

root, scratch, log = sys.argv[1], sys.argv[2], sys.argv[3]
GO = '/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin/go'
C = 'internal/results/compare/compare.go'
CP = './internal/results/compare/'
SH = 'internal/comparisons/http.go'
SS = 'internal/comparisons/comparisons.go'
SP = './internal/comparisons/'

mutants = [
    # Warning order (D-088 (d) and the schema: "in this order").
    ("R01-single-replication-before-patch-empty", C,
     '\twarnings := []string{}\n\tif scenEmpty {\n\t\twarnings = append(warnings, WarningScenarioPatchEmpty)\n\t}\n\tif baseline.Config.Replications < 2 || scenario.Config.Replications < 2 {\n\t\twarnings = append(warnings, WarningSingleReplication)\n\t}\n',
     '\twarnings := []string{}\n\tif baseline.Config.Replications < 2 || scenario.Config.Replications < 2 {\n\t\twarnings = append(warnings, WarningSingleReplication)\n\t}\n\tif scenEmpty {\n\t\twarnings = append(warnings, WarningScenarioPatchEmpty)\n\t}\n', CP),
    ("R02-truncated-warning-first", C,
     '\t\tmetrics = metrics[:MaxMetrics]\n\t\twarnings = append(warnings, WarningMetricsTruncated)\n',
     '\t\tmetrics = metrics[:MaxMetrics]\n\t\twarnings = append([]string{WarningMetricsTruncated}, warnings...)\n', CP),
    ("R03-single-replication-baseline-only", C,
     'if baseline.Config.Replications < 2 || scenario.Config.Replications < 2 {',
     'if baseline.Config.Replications < 2 {', CP),
    # RFC 6901 decoding in the wrong order (~01 must be "~1", not "/").
    ("R04-pointer-decoded-sequentially", C,
     '\t\tparts[i] = strings.NewReplacer("~1", "/", "~0", "~").Replace(p)\n',
     '\t\tparts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~0", "~"), "~1", "/")\n', CP),
    ("R05-pointer-encoded-sequentially", C,
     '\t\tb.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(t))\n',
     '\t\tb.WriteString(strings.ReplaceAll(strings.ReplaceAll(t, "/", "~1"), "~", "~0"))\n', CP),
    # The contract's inclusive bound ±1e15.
    ("R06-range-exclusive", C,
     'math.Abs(v) <= MaxValue }', 'math.Abs(v) < MaxValue }', CP),
    ("R07-range-doubled", C,
     'math.Abs(v) <= MaxValue }', 'math.Abs(v) <= 2*MaxValue }', CP),
    # Fail closed (D-089 (b)), checks the named tests may or may not reach.
    ("R08-no-replication-summary-count", C,
     ' || len(res.ReplicationSummaries) != r.Config.Replications ||', ' ||', CP),
    ("R09-no-result-manifest-check", C,
     ' ||\n\t\trep.DatasetManifestHash != r.DatasetManifestHash {', ' {', CP),
    ("R10-no-status-check", C, ' || res.Status != "completed" ||', ' ||', CP),
    ("R11-no-result-engine-check", C, ' || res.EngineVersion != r.EngineVersion {', ' {', CP),
    ("R12-no-warmup-window-check", C, ' || res.MeasurementWindow.WarmupTicks != w {', ' {', CP),
    ("R13-available-not-finite-ok", C,
     '\t\tif v == nil || !finite(*v) {\n\t\t\treturn nil, inconsistent("%s: an available summary without a finite %s", at, stat)',
     '\t\tif v == nil {\n\t\t\treturn nil, inconsistent("%s: an available summary without a finite %s", at, stat)', CP),
    ("R14-queue-length-unavailable-with-waits", C,
     '\tif stat == "queue_length_mean" {\n',
     '\tif stat == "queue_length_mean" && q.Status == simulation.Available {\n', CP),
    ("R15-missing-after-unavailable", C,
     '\tcase !bok:\n\t\tm.Status, m.Reason = Unavailable, reason(ReasonMissingInBaseline)\n\tcase !sok:\n\t\tm.Status, m.Reason = Unavailable, reason(ReasonMissingInScenario)\n\tcase b == nil && s == nil:\n\t\tm.Status, m.Reason = Unavailable, reason(ReasonBothUnavailable)\n',
     '\tcase b == nil && s == nil:\n\t\tm.Status, m.Reason = Unavailable, reason(ReasonBothUnavailable)\n\tcase !bok:\n\t\tm.Status, m.Reason = Unavailable, reason(ReasonMissingInBaseline)\n\tcase !sok:\n\t\tm.Status, m.Reason = Unavailable, reason(ReasonMissingInScenario)\n', CP),
    ("R16-union-baseline-keys-only", C, '\t\t\tks = append(keys(b, g), keys(s, g)...)\n', '\t\t\tks = keys(b, g)\n', CP),
    ("R17-truncate-keeps-last", C, '\t\tmetrics = metrics[:MaxMetrics]\n', '\t\tmetrics = metrics[len(metrics)-MaxMetrics:]\n', CP),
    ("R18-reasons-order-swapped", C,
     '\t\t{baseline.Config.Replications != scenario.Config.Replications, ReplicationsMismatch},\n\t\t{baseline.Config.Seed != scenario.Config.Seed, SeedMismatch},\n',
     '\t\t{baseline.Config.Seed != scenario.Config.Seed, SeedMismatch},\n\t\t{baseline.Config.Replications != scenario.Config.Replications, ReplicationsMismatch},\n', CP),
    ("R19-domain-keys-from-dora", C, '\t\tfor k := range m.Domain {\n', '\t\tfor k := range m.DORA {\n', CP),
    ("R20-resolve-nonkeyed-length-unchecked", C, '\t} else if len(t) != 3 {\n', '\t} else if false {\n', CP),
    ("R21-relative-negative-zero-kept", C, '\t\tm.RelativeDelta = value(&rel)\n', '\t\tm.RelativeDelta = &rel\n', CP),
    ("R22-censored-threshold", C, 's.CensoredEntities > 0 })', 's.CensoredEntities > 1 })', CP),
    ("R23-empty-patch-accepts-one-op", C, '\treturn len(ops) == 0, nil\n', '\treturn len(ops) <= 1, nil\n', CP),
    ("R24-delta-out-of-range-keeps-relative", C,
     '\t\tif !finite(abs) {\n\t\t\tm.Reason = reason(ReasonDeltaOutOfRange)\n\t\t\treturn m\n\t\t}\n',
     '\t\tif !finite(abs) {\n\t\t\tm.Reason = reason(ReasonDeltaOutOfRange)\n\t\t\tr := abs / *b\n\t\t\tm.RelativeDelta = &r\n\t\t\treturn m\n\t\t}\n', CP),
    ("R25-incomparable-hides-values", C,
     '\tcase !comparable:\n\t\tm.Status, m.Reason = Incomparable, reason(ReasonRunsNotComparable)\n',
     '\tcase !comparable:\n\t\tm.Status, m.Reason = Incomparable, reason(ReasonRunsNotComparable)\n\t\tm.Baseline, m.Scenario = nil, nil\n', CP),
    ("R26-statistics-skip-unavailable", C,
     '\t\t\t\tout = append(out, Statistic{Path: statPath(g, k, stat), Value: v})\n',
     '\t\t\t\tif v == nil {\n\t\t\t\t\tcontinue\n\t\t\t\t}\n\t\t\t\tout = append(out, Statistic{Path: statPath(g, k, stat), Value: v})\n', CP),
    ("R27-resource-stat-name-unchecked", C, '\t{"resources", true, []string{"utilization_mean"}},\n', '\t{"resources", true, []string{"utilization_mean", "capacity"}},\n', CP),
    # The adapter and the service.
    ("R28-run-id-unanchored", SH, 'regexp.MustCompile(`^sim_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)', 'regexp.MustCompile(`^sim_[0-7][0-9A-HJKMNP-TV-Z]{25}`)', SP),
    ("R29-same-run-names-baseline", SH, '\t\treturn "", "", badParam(ScenarioParam, "baseline_run_id and scenario_run_id must name two different runs")\n',
     '\t\treturn "", "", badParam(BaselineParam, "baseline_run_id and scenario_run_id must name two different runs")\n', SP),
    ("R30-read-write-transaction", SS, 'tenant.ReadSnapshot', 'tenant.ReadWrite', SP),
    ("R31-not-found-message-names-org", SH, 'Message: "not found", Details', 'Message: "not found in this organization", Details', SP),
    ("R32-result-missing-is-not-ready", SS,
     '\t\t\t\treturn fmt.Errorf("%w: completed run %s has no result", compare.ErrInconsistent, r.ID)\n',
     '\t\t\t\treturn &RunError{Param: params[i], Err: ErrNotReady}\n', SP),
]

out = open(log, 'w')
counts = {}
for name, path, old, new, pkg in mutants:
    src = open(os.path.join(root, path)).read()
    if src.count(old) != 1:
        out.write(f'{name}: SNIPPET FOUND {src.count(old)} TIMES (not run)\n')
        out.flush()
        counts['NOT RUN'] = counts.get('NOT RUN', 0) + 1
        continue
    mdir = os.path.join(scratch, 'mut', name)
    os.makedirs(mdir, exist_ok=True)
    mfile = os.path.join(mdir, os.path.basename(path))
    open(mfile, 'w').write(src.replace(old, new))
    ov = os.path.join(mdir, 'overlay.json')
    json.dump({'Replace': {os.path.join(root, path): mfile}}, open(ov, 'w'))
    env = dict(os.environ, GOTOOLCHAIN='local')
    vet = subprocess.run([GO, 'vet', '-overlay', ov, pkg], cwd=root, capture_output=True, text=True, timeout=900, env=env)
    if vet.returncode != 0:
        status = 'BUILD'
        detail = vet.stderr.strip().splitlines()[:2]
    else:
        r = subprocess.run([GO, 'test', '-count=1', '-overlay', ov, pkg], cwd=root, capture_output=True, text=True, timeout=1800, env=env)
        text = r.stdout + r.stderr
        status = 'KILLED' if r.returncode != 0 else 'SURVIVED'
        detail = [l.strip() for l in text.splitlines() if l.startswith('--- FAIL') or 'panic:' in l][:4]
    counts[status] = counts.get(status, 0) + 1
    out.write(f'{name}: {status} {" | ".join(detail)}\n')
    out.flush()
out.write(f'TOTAL {len(mutants)}: {counts}\n')
out.close()
print(open(log).read())
