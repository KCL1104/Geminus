#!/usr/bin/env python3
"""Go mutants for T-022: metrics aggregation and statistics
(internal/results/metrics), trace sampling (internal/simulation/trace) and
the engine Run (internal/simulation/des).

Each mutant is one or more exact search/replace edits applied with
`go test -overlay` (the working tree is never modified); KILLED means
`go test ./internal/results/... ./internal/simulation/...
./internal/domainpack/...` failed, or, for the mutants marked fma,
scripts/check_no_fma.sh failed. The T-022 review's surviving mutants are
included under their review names.

    python3 checks/evidence/2026-10-15-T-022/results-mutants.py [repo]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

REPO = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[3])
A = "internal/results/metrics/aggregate.go"
S = "internal/results/metrics/stats.go"
T = "internal/simulation/trace/trace.go"
D = "internal/simulation/des/des.go"
K = "internal/simulation/engine/kernel.go"
MUT = [
    # window and populations (SIMULATION_SPEC §5)
    ("window-excludes-warmup", A, "func (c *Collector) inWindow(t int64) bool { return c.w <= t && t <= c.h }", "func (c *Collector) inWindow(t int64) bool { return c.w < t && t <= c.h }"),
    ("window-excludes-horizon", A, "func (c *Collector) inWindow(t int64) bool { return c.w <= t && t <= c.h }", "func (c *Collector) inWindow(t int64) bool { return c.w <= t && t < c.h }"),
    ("wait-enter-after-warmup", A, "if enter := c.entered[o.Entity]; enter >= c.w && o.Tick <= c.h {", "if enter := c.entered[o.Entity]; enter > c.w && o.Tick <= c.h {"),
    ("cohort-after-warmup", A, "\t\tif arrived >= c.w {", "\t\tif arrived > c.w {"),
    ("cohort-everyone", A, "\t\tif arrived >= c.w {", "\t\tif true {"),
    ("abandoned-counted", A, "\t\tif o.Value != 1 || !c.inWindow(o.Tick) {", "\t\tif !c.inWindow(o.Tick) {"),
    ("failures-unwindowed", A, "\t\tif c.inWindow(o.Tick) {\n\t\t\tc.failures++", "\t\tif true {\n\t\t\tc.failures++"),
    ("reworks-unwindowed", A, "\t\tif c.inWindow(o.Tick) {\n\t\t\tc.reworks++", "\t\tif true {\n\t\t\tc.reworks++"),
    ("overlap-ignores-warmup", A, "\treturn max(min(b, h)-max(a, w), 0)", "\treturn max(min(b, h)-a, 0)"),
    # Equivalent: every caller passes b <= h (events never pass the horizon,
    # and total closes at h), so min(b, h) is b; kept for the general contract.
    ("overlap-ignores-horizon", A, "\treturn max(min(b, h)-max(a, w), 0)", "\treturn max(b-max(a, w), 0)", "equivalent"),
    ("integral-not-closed", A, "\treturn g.area + g.level*overlap(g.since, h, w, h)", "\treturn g.area"),
    ("utilization-ignores-capacity", A, "u := float64(c.busy[i].total(w, h)) / (float64(p.ResourceCapacity(i)) * span)", "u := float64(c.busy[i].total(w, h)) / span"),
    ("queue-never-shrinks", A, "\t\tc.queueLen[o.Node]--\n", ""),
    ("replication-mean-with-gaps", A, "\tcase !allCohorts:", "\tcase !allCohorts && false:"),
    ("pooled-lead-gets-ci", A, "LeadTimeMinutes:   summary(pooledLeads, simulation.PopulationEntity, \"minute\", ReasonNoCompletedEntity),", "LeadTimeMinutes:   summary(pooledLeads, simulation.PopulationReplication, \"minute\", ReasonNoCompletedEntity),"),
    ("queue-keyed-by-process", A, "\t\tm.Queues[p.QueueLabel(i)] = q", "\t\tm.Queues[p.NodeID(i)] = q"),
    ("build-order-unchecked", A, "r.Collector == nil || r.Collector.p != p || int(r.Outcome.Index) != i", "r.Collector == nil || r.Collector.p != p"),
    ("censored-from-exited", A, "CensoredEntities: r.Outcome.Censored,", "CensoredEntities: r.Outcome.Exited,"),
    # statistics
    ("type7-floor-only", S, "\treturn s[int(lo)] + float64((h-lo)*(s[int(hi)]-s[int(lo)]))", "\treturn s[int(lo)] + 0*s[int(hi)]"),
    ("type7-unsorted", S, "\tslices.Sort(s)\n", ""),
    ("type6-index", S, "\th := float64(float64(len(s)-1) * p)", "\th := float64(float64(len(s)+1)*p) - 1"),
    ("ci-df-n", S, "\thalf := studentT975[n-1] *", "\thalf := studentT975[min(n, len(studentT975)-1)] *"),
    ("ci-population-sd", S, "math.Sqrt(ss/float64(n-1))", "math.Sqrt(ss/float64(n))"),
    ("ci-from-one", S, "\tif n < 2 || n > len(studentT975) {", "\tif n < 1 || n > len(studentT975) {"),
    ("ci-on-every-population", S, "\tif pop == simulation.PopulationReplication {", "\tif true {"),
    ("empty-is-zero", S, "\t\treturn simulation.MetricSummary{Status: simulation.Unavailable, Population: pop, Unit: unit, Reason: ptr(reasonIfEmpty)}", "\t\treturn simulation.MetricSummary{Status: simulation.Available, Population: pop, Unit: unit, Mean: ptr(0.0), P50: ptr(0.0), P95: ptr(0.0)}"),
    # trace
    ("trace-rate-quarter", T, "\tthreshold = uint64(1) << 61", "\tthreshold = uint64(1) << 62"),
    ("trace-prefix", T, "\tprefix    = \"dt-trace-v1\\x00\"", "\tprefix    = \"dt-trace-v1\""),
    ("trace-big-endian", T, "binary.LittleEndian.Uint64(h[:8])", "binary.BigEndian.Uint64(h[:8])"),
    ("trace-cap-plus-one", T, "\tif len(r.events) >= r.max {", "\tif len(r.events) > r.max {"),
    ("trace-not-marked-truncated", T, "\t\tr.truncated = true\n", ""),
    ("trace-queue-at-process", T, "\t\tnode = r.p.NodeID(o.At)\n", ""),
    ("trace-abandon-as-complete", T, "\t\tif o.Value != 1 {\n\t\t\ttyp = simulation.TraceAbandon\n\t\t}\n", ""),
    ("trace-unselected-too", T, "\t\tif !Selected(id) {\n\t\t\treturn\n\t\t}\n", ""),
    # engine Run
    ("trace-every-replication", D, "\t\t\t\tif i == 0 && rec != nil {", "\t\t\t\tif rec != nil {"),
    ("dora-dropped", D, "\t\t\tres.Metrics.DORA = dm\n", ""),
    ("effective-hash-trusted", D, "\tif in.EffectiveModelHash != effective {", "\tif false {"),
    ("model-hash-unchecked", D, "\tif !hashPattern.MatchString(in.ModelHash) {", "\tif false {"),
    ("failures-ignored", D, "\tif finished != n {", "\tif false {"),
    ("no-wall-timeout", D, "\trunCtx, cancelTimeout := context.WithTimeoutCause(ctx, timeout, ErrWallTimeout)", "\trunCtx, cancelTimeout := context.WithCancel(ctx)\n\t_ = timeout"),
    ("empty-trace-available", D, "Available: len(out.Trace.Events) > 0", "Available: true"),
    ("trace-ignores-level", D, "\tif p.TraceLevel() == simulation.TraceSampled && in.Limits.MaxTraceEvents > 0 {", "\tif in.Limits.MaxTraceEvents > 0 {"),
    ("digest-unset", D, "\tres.Reproducibility.ResultDigest = digest\n", "\t_ = digest\n"),
    ("engine-version-ignored", D, "Status: \"completed\", EngineVersion: e.version(),", "Status: \"completed\", EngineVersion: Version,"),
    # the T-022 review's survivors (review-t022/py/mutants.py)
    ("wait-first-visit-only", A, [("\tfinished  bool\n}", "\tfinished  bool\n\tseen      map[uint64]bool\n}"),
                                  ("\t\tif enter := c.entered[o.Entity]; enter >= c.w && o.Tick <= c.h {", "\t\tif c.seen == nil {\n\t\t\tc.seen = map[uint64]bool{}\n\t\t}\n\t\tif enter := c.entered[o.Entity]; enter >= c.w && o.Tick <= c.h && !c.seen[o.Entity] {\n\t\t\tc.seen[o.Entity] = true")], None),
    ("busy-boolean", A, "c.busy[o.Resource].set(o.Value, o.Tick, c.w, c.h)", "c.busy[o.Resource].set(min(o.Value, 1), o.Tick, c.w, c.h)"),
    ("failure-rework-swap-metric", A, "\t\tfailures = append(failures, float64(c.failures))\n\t\treworks = append(reworks, float64(c.reworks))", "\t\tfailures = append(failures, float64(c.reworks))\n\t\treworks = append(reworks, float64(c.failures))"),
    ("failure-rework-swap-summary", A, "Failures: c.failures, Reworks: c.reworks,", "Failures: c.reworks, Reworks: c.failures,"),
    ("queue-unavailable-no-reason", A, "q.Status, q.Reason = simulation.Unavailable, ptr(ReasonNoQueueVisit)", "q.Status, q.Reason = simulation.Unavailable, nil"),
    ("queue-unavailable-zero-wait", A, "q.Status, q.Reason = simulation.Unavailable, ptr(ReasonNoQueueVisit)", "q.Status, q.Reason = simulation.Unavailable, ptr(ReasonNoQueueVisit)\n\t\t\tq.WaitMeanMinutes, q.WaitP50Minutes, q.WaitP95Minutes = ptr(0.0), ptr(0.0), ptr(0.0)"),
    ("queue-unavailable-as-available-zero", A, "q.Status, q.Reason = simulation.Unavailable, ptr(ReasonNoQueueVisit)", "q.Status = simulation.Available\n\t\t\tq.WaitMeanMinutes, q.WaitP50Minutes, q.WaitP95Minutes = ptr(0.0), ptr(0.0), ptr(0.0)"),
    ("queue-length-last-replication", A, "QueueLengthMean: mean(queueLen[i])", "QueueLengthMean: queueLen[i][len(queueLen[i])-1]"),
    ("utilization-first-replication", A, "UtilizationMean: ptr(mean(util[i]))", "UtilizationMean: ptr(util[i][0])"),
    ("replication-mean-lead-is-running-pooled", A, "\t\t\tm := mean(c.leads)\n", "\t\t\tm := mean(pooledLeads)\n"),
    ("queue-wait-pooled-first-rep-only", A, "\t\t\t\twaits[i] = append(waits[i], c.waits[i]...)", "\t\t\t\tif r.Outcome.Index == 0 {\n\t\t\t\t\twaits[i] = append(waits[i], c.waits[i]...)\n\t\t\t\t}"),
    ("summary-utilization-capacity1", A, "\t\t\ts.ResourceUtilization[p.ResourceID(i)] = u", "\t\t\ts.ResourceUtilization[p.ResourceID(i)] = min(u*float64(p.ResourceCapacity(i)), 1)"),
    ("ci-unrounded-square", S, "ss += float64((v - m) * (v - m))", "ss += (v - m) * (v - m)", "fma"),
    ("budget-per-replication", D, "engine.RunReplication(poolCtx, p, uint32(i), budget, obs)", "engine.RunReplication(poolCtx, p, uint32(i), engine.NewBudget(in.Limits.MaxEvents), obs)"),
    ("completion-does-not-decide", D, "\tif finished != n {", "\tif finished != n || poolCtx.Err() != nil {"),
    ("trace-last-replication", D, "\t\t\t\tif i == 0 && rec != nil {", "\t\t\t\tif i == n-1 && rec != nil {"),
    ("trace-replication-1-if-any", D, "\t\t\t\tif i == 0 && rec != nil {", "\t\t\t\tif i == min(1, n-1) && rec != nil {"),
    # the review fixes
    ("progress-reported-synchronously", D, "\t\t\tcase latest <- pr:\n\t\t\t\treturn", "\t\t\tcase latest <- pr:\n\t\t\t\tsink.Report(pr)\n\t\t\t\treturn"),
    ("progress-never-delivered", D, "\t\tfor pr := range latest {\n\t\t\tsink.Report(pr)\n\t\t}", "\t\tfor range latest {\n\t\t}"),
    ("stale-snapshot-kept", D, "\t\t\tselect { // replace the undelivered, older snapshot\n\t\t\tcase <-latest:\n\t\t\tdefault:\n\t\t\t}", "\t\t\treturn"),
    ("no-panic-recovery", D, "\t\t\t\tif v := recover(); v != nil {", "\t\t\t\tif v := recover(); false && v != nil {"),
    ("collector-kept", D, "\t\t\t\tc.Finish()\n", ""),
    ("finish-keeps-maps", A, "func (c *Collector) Finish() { c.arrived, c.entered, c.finished = nil, nil, true }", "func (c *Collector) Finish() { c.finished = true }"),
    ("trace-at-explicit-queue", T, "\t\tnode = r.p.NodeID(o.At)", "\t\tnode = r.p.QueueLabel(o.Node)"),
    ("entered-node-is-buffer", K, "\t\t\treturn k.enqueue(e, node.buffer, n)", "\t\t\treturn k.enqueue(e, node.buffer, node.buffer)"),
]


def main() -> int:
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new, *flags) in enumerate(MUT):
            src = (REPO / f).read_text()
            edits = old if isinstance(old, list) else [(old, new)]
            bad = [o for o, _ in edits if src.count(o) != 1]
            if bad:
                results.append((name, f"BAD-MUTANT (pattern found {src.count(bad[0])} times)"))
                print("\t".join(results[-1]), flush=True)
                continue
            for o, n in edits:
                src = src.replace(o, n)
            m = pathlib.Path(tmp) / f"m{i}.go"
            m.write_text(src)
            ov = pathlib.Path(tmp) / f"m{i}.json"
            ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
            if "fma" in flags:
                p = subprocess.run(["bash", "scripts/check_no_fma.sh"], cwd=REPO, capture_output=True, text=True,
                                   env=dict(os.environ, GOFLAGS=f"-overlay={ov}"))
            else:
                p = subprocess.run(["go", "test", "-count=1", "-timeout", "300s", "-overlay", str(ov), "./internal/results/...", "./internal/simulation/...", "./internal/domainpack/..."],
                                   cwd=REPO, capture_output=True, text=True, env=dict(os.environ))
            o = p.stdout + p.stderr
            if p.returncode == 0:
                status = "SURVIVED"
            elif "build failed" in o or "setup failed" in o:
                status = "BUILD-FAILED"
            else:
                status = "KILLED"
            first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:", "FAIL fused", "FAIL platform"))), "")
            if "equivalent" in flags:
                status = "EQUIVALENT" if status == "SURVIVED" else f"UNEXPECTED-{status}"
            results.append((name, f"{status} {first[:90]}"))
            print("\t".join(results[-1]), flush=True)
    killed = sum(1 for r in results if r[1].startswith("KILLED"))
    equivalent = sum(1 for r in results if r[1].startswith("EQUIVALENT"))
    print(f"{killed}/{len(results) - equivalent} killed, {equivalent} equivalent (documented above)")
    return 0 if killed + equivalent == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
