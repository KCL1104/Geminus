#!/usr/bin/env python3
"""Go mutants of T-047's code: finite buffers and blocking in the kernel
(internal/simulation/engine), R1 in the validator, the Result members, the
trace, the runner's and the API's deadlock mapping, and the D-057 risk.

Each mutant is one exact search/replace applied with `go test -overlay` (the
working tree is never modified); KILLED means the listed packages' tests
failed. A mutant whose pattern is not found exactly once is BAD-MUTANT.

    python3 checks/evidence/2026-10-20-T-047/t047-mutants.py [repo] [name...]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

REPO = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 and not sys.argv[1].startswith("-") and pathlib.Path(sys.argv[1]).is_dir() else pathlib.Path(__file__).resolve().parents[3]
ONLY = [a for a in sys.argv[1:] if not pathlib.Path(a).is_dir()]
K = "internal/simulation/engine/kernel.go"
C = "internal/simulation/engine/compile.go"
V = "internal/model/validate/blocking.go"
VP = "internal/model/validate/primitives.go"
M = "internal/results/metrics/aggregate.go"
T = "internal/simulation/trace/trace.go"
D = "internal/simulation/des/des.go"
RN = "internal/runs/runner/runner.go"
RS = "internal/runs/runs.go"
RK = "internal/ai/review/risk.go"
SIM = ["./internal/simulation/...", "./internal/results/..."]
VAL = ["./internal/model/...", "./internal/simulation/engine/"]
MUT = [
    # kernel: blocking after service (D-049)
    ("never-blocks", K, "\tblock := err == nil && k.full(to)\n", "\tblock := false\n", SIM),
    ("full-off-by-one", K, "\treturn c > 0 && k.waiting[b] >= c\n", "\treturn c > 0 && k.waiting[b] > c\n", SIM),
    ("source-never-blocks", K, "\tif k.full(to) {\n\t\tk.block(e, from, to)\n\t\treturn nil\n\t}\n\treturn k.enter(e, to)\n}", "\treturn k.enter(e, to)\n}", SIM),
    ("blocked-releases-bundle", K, "\tif !block {\n\t\tk.release(reqs)\n\t}\n", "\tk.release(reqs)\n", SIM),
    ("blocked-schedules-dispatch", K, "\tk.nBlocked++\n\tk.observe(Observation{Kind: ObsBlocked", "\tk.nBlocked++\n\t_ = k.needDispatch()\n\tk.observe(Observation{Kind: ObsBlocked", SIM),
    ("route-observed-before-release", K, "\tif !block {\n\t\tk.release(reqs)\n\t}\n", "\tk.passed(e)\n\tif !block {\n\t\tk.release(reqs)\n\t}\n", SIM),
    ("waiting-not-decremented", K, "\t\tk.waiting[req.process]--\n", "", SIM),
    ("unblock-skipped", K, "\t\tif k.blocked != nil && k.blocked[req.process].len() > 0 {\n\t\t\tif err := k.unblock(req.process); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n", "", SIM),
    ("unblock-lifo", K, "\tb := q.items[q.head]\n\tq.head++", "\tb := q.items[len(q.items)-1]\n\tq.items[len(q.items)-1] = q.items[q.head]\n\tq.head++", SIM),
    ("unblock-keeps-bundle", K, "\tif from := &k.r.p.nodes[b.from]; from.typ == model.NodeProcess {\n\t\tk.release(from.requires)\n\t}\n", "", SIM),
    ("unblocked-before-release", K, "\tif from := &k.r.p.nodes[b.from]; from.typ == model.NodeProcess {\n\t\tk.release(from.requires)\n\t}\n\tk.observe(Observation{Kind: ObsUnblocked, Entity: b.entity, Node: b.from, At: b.at})\n",
     "\tk.observe(Observation{Kind: ObsUnblocked, Entity: b.entity, Node: b.from, At: b.at})\n\tif from := &k.r.p.nodes[b.from]; from.typ == model.NodeProcess {\n\t\tk.release(from.requires)\n\t}\n", SIM),
    ("unblock-enters-the-process", K, "\treturn k.enqueue(b.entity, process, b.at)\n}", "\treturn k.enqueue(b.entity, process, process)\n}", SIM),
    ("release-wakes-no-held", K, "\t\tif f := k.queues[q.resource].front(); f != nil && f.held {\n\t\t\tf.held = false\n\t\t\tk.held--\n\t\t\theap.Push(&k.cands, f)\n\t\t}\n", "", SIM),
    ("dispatch-redispatches", K, "\tif k.dispatchPending || k.inDispatch {\n", "\tif k.dispatchPending {\n", SIM),
    ("unblock-in-a-later-dispatch", K, "\t\tif k.blocked != nil && k.blocked[req.process].len() > 0 {\n\t\t\tif err := k.unblock(req.process); err != nil {", "\t\tif k.blocked != nil && k.blocked[req.process].len() > 0 {\n\t\t\tk.inDispatch = false\n\t\t\tif err := k.unblock(req.process); err != nil {", SIM),
    # Equivalent: an invariant check (ErrInternal). Every caller decides
    # first: a route into a full buffer blocks instead (resolve, full), and
    # an unblock enters only after a grant made room in the same dispatch.
    ("enqueue-capacity-unchecked", K, "\tif c := k.r.p.nodes[process].capacity; c > 0 && k.waiting[process] >= c {\n", "\tif c := k.r.p.nodes[process].capacity; false && c > 0 && k.waiting[process] >= c {\n", SIM, "equivalent"),
    # kernel: D-050 B1
    ("no-deadlock-check", K, "\treturn k.deadlock()\n}", "\treturn nil\n}", SIM),
    ("deadlock-check-only-after-dispatch", K, "\tif err != nil || !k.r.p.finite || k.dispatchPending {\n", "\tif err != nil || !k.r.p.finite || k.dispatchPending || k.r.cur.Kind != EventDispatch {\n", SIM),
    ("deadlock-check-while-dispatch-due", K, "\tif err != nil || !k.r.p.finite || k.dispatchPending {\n", "\tif err != nil || !k.r.p.finite {\n", SIM),
    ("deadlock-ignores-delays", K, "\tif k.inService > 0 || k.inDelay > 0 || (k.nBlocked == 0 && k.held == 0) {\n", "\tif k.inService > 0 || (k.nBlocked == 0 && k.held == 0) {\n", SIM),
    ("deadlock-ignores-service", K, "\tif k.inService > 0 || k.inDelay > 0 || (k.nBlocked == 0 && k.held == 0) {\n", "\tif k.inDelay > 0 || (k.nBlocked == 0 && k.held == 0) {\n", SIM),
    ("service-not-counted", K, "\t\tk.inService++\n", "", SIM),
    ("delay-not-counted", K, "\t\tk.inDelay++\n", "", SIM),
    ("deadlock-reports-the-latest", K, "\t\t\tif q.len() > 0 && (earliest == nil || q.items[q.head].order < earliest.order) {\n", "\t\t\tif q.len() > 0 && (earliest == nil || q.items[q.head].order > earliest.order) {\n", SIM),
    # Equivalent: release() wakes the front of every queue it frees and
    # schedules a dispatch, so when after() runs (no dispatch pending) every
    # held candidate is bound; the branch only keeps B1 from a false report
    # if that invariant ever broke.
    ("deadlock-ignores-fitting-held", K, "\t\tif !bound {\n\t\t\treturn nil // it fits: a dispatch will grant it\n\t\t}\n", "\t\t_ = bound\n", SIM, "equivalent"),
    ("resolve-counts-no-visits", K, "\t\ten.visits += visits\n\t\tif lim := k.r.p.limits.MaxEntityVisits; en.visits > lim {\n\t\t\treturn -1,", "\t\t_ = visits\n\t\tif lim := k.r.p.limits.MaxEntityVisits; en.visits > lim {\n\t\t\treturn -1,", SIM),
    ("resolve-drops-rework", K, "\t\tcase model.NodeFailure, model.NodeRework:\n\t\t\tk.path = append(k.path, n)\n", "\t\tcase model.NodeFailure, model.NodeRework:\n\t\t\tif k.r.p.nodes[n].typ == model.NodeFailure {\n\t\t\t\tk.path = append(k.path, n)\n\t\t\t}\n", SIM),
    # compile
    ("capacity-ignored", C, "\t\t\t\tp.nodes[p.nodes[i].buffer].capacity = c\n", "", SIM),
    ("no-finite-flag", C, "\t\t\t\tp.finite = true\n", "", SIM),
    ("every-node-can-block", C, "\t\t\t\tp.nodes[i].canBlock = p.reachesFinite(i)\n", "\t\t\t\tp.nodes[i].canBlock = true\n", SIM),
    ("can-block-not-through-decisions", C, "\t\tcase model.NodeDecision, model.NodeFailure, model.NodeRework:\n\t\t\tfor _, rt := range c.routes {", "\t\tcase model.NodeFailure, model.NodeRework:\n\t\t\tfor _, rt := range c.routes {", SIM),
    ("kernel-does-not-execute", C, "var kernelExecutes = map[validate.Primitive]bool{validate.FiniteBuffer: true}", "var kernelExecutes = map[validate.Primitive]bool{}", SIM),
    # validator: R1
    ("r1-off", V, "\tif !any {\n\t\treturn nil // no finite buffer, nothing to check (D-047)\n\t}\n", "\tif true {\n\t\treturn nil\n\t}\n\t_ = any\n", VAL),
    ("r1-no-components", V, "\t\t\tif j, ok := firstUser[r.ResourceID]; ok {\n\t\t\t\tunion(i, j)\n", "\t\t\tif j, ok := firstUser[r.ResourceID]; ok {\n\t\t\t\t_, _, _ = union, i, j\n", VAL),
    ("r1-not-through-decisions", V, "\t\tcase model.NodeDecision, model.NodeFailure, model.NodeRework:\n\t\t\treturn true\n", "\t\tcase model.NodeFailure, model.NodeRework:\n\t\t\treturn true\n", VAL),
    ("r1-through-delays", V, "\t\tcase model.NodeDecision, model.NodeFailure, model.NodeRework:\n\t\t\treturn true\n", "\t\tcase model.NodeDecision, model.NodeFailure, model.NodeRework, model.NodeDelay:\n\t\t\treturn true\n", VAL),
    ("r1-zero-probability-routes", V, "\t\t\tif !ok || fl.Routing.Probability <= 0 {\n", "\t\t\tif !ok {\n", VAL),
    ("r1-every-process-holds", V, "\t\tcase node.Type == model.NodeProcess && len(node.Requires) > 0:\n", "\t\tcase node.Type == model.NodeProcess:\n", VAL),
    ("r1-misses-self-edges", V, "\t\t\tif e.label >= 0 && scc[v] == scc[e.to] {\n", "\t\t\tif e.label >= 0 && v != e.to && scc[v] == scc[e.to] {\n", VAL),
    ("r1-unlimited-queue-blocks", V, "\t\tcase ir.Nodes[j].Type == model.NodeProcess && finite[j] >= 0:\n", "\t\tcase ir.Nodes[j].Type == model.NodeProcess:\n", VAL),
    ("r1-message-unsorted", V, "\t\tslices.Sort(procs)\n", "", VAL),
    ("finite-buffer-unchecked", VP, "var checked = map[Primitive]bool{FiniteBuffer: true}", "var checked = map[Primitive]bool{}", VAL + ["./internal/ai/review/"]),
    # Result members
    ("held-not-integrated", M, "\t\tc.held[q.Resource].set(c.heldUnits[q.Resource], t, c.w, c.h)\n", "", ["./internal/results/...", "./internal/simulation/des/"]),
    ("blocks-started-ignore-window", M, "\t\tif c.inWindow(o.Tick) {\n\t\t\tc.blocksStarted[o.Node]++\n\t\t}\n", "\t\tc.blocksStarted[o.Node]++\n", ["./internal/results/...", "./internal/simulation/des/"]),
    ("blocks-completed-ignore-window", M, "\t\tif since := c.since[o.Entity]; since >= c.w && o.Tick <= c.h {\n", "\t\tif since := c.since[o.Entity]; o.Tick <= c.h {\n", ["./internal/results/...", "./internal/simulation/des/"]),
    ("full-when-over", M, "\t\tif c.queueLen[i] >= int64(cap) {\n", "\t\tif c.queueLen[i] > int64(cap) {\n", ["./internal/results/...", "./internal/simulation/des/"]),
    ("members-always", M, "\tif !c.finite {\n\t\treturn\n\t}\n\tp, w, h := c.p, c.w, c.h\n", "\tif c.held == nil {\n\t\tc.held, c.blockedAt, c.full = make([]integral, c.p.ResourceCount()), make([]integral, c.p.NodeCount()), make([]integral, c.p.NodeCount())\n\t\tc.blocksStarted, c.blocksDone, c.blockTicks = make([]int64, c.p.NodeCount()), make([]int64, c.p.NodeCount()), make([]int64, c.p.NodeCount())\n\t}\n\tp, w, h := c.p, c.w, c.h\n", ["./internal/results/...", "./internal/simulation/des/"]),
    ("processing-is-in-use", M, "ScheduledProcessingTicks: used - held}", "ScheduledProcessingTicks: used}", ["./internal/results/...", "./internal/simulation/des/"]),
    # Equivalent: only a process has requirements (Requires of any other
    # node is empty), so the node-type test only saves the loop.
    ("delay-blocks-hold-units", M, "\tif c.p.NodeType(n) != model.NodeProcess {\n\t\treturn\n\t}\n\tfor _, q := range c.p.Requires(n) {", "\tfor _, q := range c.p.Requires(n) {", ["./internal/results/...", "./internal/simulation/des/"], "equivalent"),
    # trace
    ("no-blocked-trace", T, "\tengine.ObsBlocked: simulation.TraceBlocked, engine.ObsUnblocked: simulation.TraceUnblocked,\n", "", ["./internal/simulation/..."]),
    # version (D-047)
    ("old-engine-version", D, 'const Version = "des-go-0.2.0"', 'const Version = "des-go-0.1.0"', ["./internal/simulation/des/", "./internal/runs/..."]),
    # runner and API mapping
    ("deadlock-retried", RN, "\t\treturn failure(store.SimulationDeadlock, de.Reason(), false)\n", "\t\treturn failure(store.SimulationDeadlock, de.Reason(), true)\n", ["./internal/runs/..."]),
    ("deadlock-unmapped", RN, "\tcase errors.As(err, &de):\n\t\treturn failure(store.SimulationDeadlock, de.Reason(), false)\n\tcase errors.Is(err, engine.ErrDeadlock):\n\t\treturn failure(store.SimulationDeadlock, \"the replication deadlocked\", false)\n", "\tcase de != nil && false:\n", ["./internal/runs/..."]),
    ("deadlock-details-dropped", RN, "\tcase errors.As(err, &de):\n", "\tcase errors.As(err, &de) && false:\n", ["./internal/runs/..."]),
    ("deadlock-internal-error", RS, "\tcase f.Code == store.SimulationDeadlock:\n\t\tbody.Code = \"MODEL_VALIDATION_FAILED\"\n", "\tcase f.Code == store.SimulationDeadlock:\n", ["./internal/runs/..."]),
    ("deadlock-without-severity", RS, "\t\tbody.Details[0].Severity = string(model.SeverityError)\n\tcase f.Code == jobs.LeaseExpired", "\tcase f.Code == jobs.LeaseExpired", ["./internal/runs/..."]),
    # architecture checker: no production file may replace Compile's validator
    ("guard-misses-assignments", "scripts/check-architecture/source.go", '\t\t\t\t\t\t\tbad = "assigns " + gv.name\n', "", ["./scripts/check-architecture/"]),
    ("guard-misses-addresses", "scripts/check-architecture/source.go", '\t\t\t\t\t\tbad = "takes the address of " + gv.name\n', "", ["./scripts/check-architecture/"]),
    ("guard-without-declaration", "scripts/check-architecture/source.go", "\t\tif declared != 1 {\n", "\t\tif false {\n", ["./scripts/check-architecture/"]),
    # D-057 risk
    ("risk-misses-new-finite", RK, "\t\tif ok && c.Type == n.Type && c.Capacity != 0 && (n.Capacity == 0 || c.Capacity < n.Capacity) {\n", "\t\tif ok && c.Type == n.Type && c.Capacity != 0 && n.Capacity != 0 && c.Capacity < n.Capacity {\n", ["./internal/ai/review/"]),
    ("risk-any-change-medium", RK, "\t\tif ok && c.Type == n.Type && c.Capacity != 0 && (n.Capacity == 0 || c.Capacity < n.Capacity) {\n", "\t\tif ok && c.Type == n.Type && c.Capacity != n.Capacity {\n", ["./internal/ai/review/"]),
    ("risk-off", RK, "\t\tif ok && c.Type == n.Type && c.Capacity != 0 && (n.Capacity == 0 || c.Capacity < n.Capacity) {\n", "\t\tif false {\n", ["./internal/ai/review/"]),
]


def main() -> int:
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new, pkgs, *flags) in enumerate(MUT):
            if ONLY and name not in ONLY:
                continue
            src = (REPO / f).read_text()
            if src.count(old) != 1:
                results.append((name, f"BAD-MUTANT (pattern found {src.count(old)} times)"))
                print("\t".join(results[-1]), flush=True)
                continue
            m = pathlib.Path(tmp) / f"m{i}.go"
            m.write_text(src.replace(old, new))
            ov = pathlib.Path(tmp) / f"m{i}.json"
            ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
            # -failfast: a package stops at its first failing test (a
            # killed mutant needs only one); a survivor runs every test.
            p = subprocess.run(["go", "test", "-count=1", "-failfast", "-timeout", "900s", "-overlay", str(ov), *pkgs],
                               cwd=REPO, capture_output=True, text=True, env=dict(os.environ))
            o = p.stdout + p.stderr
            if p.returncode == 0:
                status = "SURVIVED"
            elif "build failed" in o or "setup failed" in o or "[build failed]" in o:
                status = "BUILD-FAILED"
            else:
                status = "KILLED"
            first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:"))), "")
            if "equivalent" in flags:
                status = "EQUIVALENT" if status == "SURVIVED" else f"UNEXPECTED-{status}"
            results.append((name, f"{status} {first[:100]}"))
            print("\t".join(results[-1]), flush=True)
    killed = sum(1 for r in results if r[1].startswith("KILLED"))
    equivalent = sum(1 for r in results if r[1].startswith("EQUIVALENT"))
    print(f"{killed}/{len(results) - equivalent} killed, {equivalent} equivalent")
    return 0 if killed + equivalent == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
