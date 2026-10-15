#!/usr/bin/env python3
"""Go mutants for the T-021 DES kernel (internal/simulation/engine/kernel.go
and the replication hooks it uses).

Each mutant is one exact search/replace, applied with `go test -overlay`
(the working tree is never modified); KILLED means `go test
./internal/simulation/...` failed.

    python3 checks/evidence/2026-10-15-T-021/kernel-mutants.py [repo]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

REPO = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[3])
K = "internal/simulation/engine/kernel.go"
R = "internal/simulation/engine/replication.go"
MUT = [
    # resource-wise FIFO dispatcher (candidates = fronts of all their queues)
    ("every-request-a-candidate", K, "\tif req.heads == len(reqs) {\n\t\theap.Push(&k.cands, req)", "\tif true {\n\t\theap.Push(&k.cands, req)"),
    ("heads-not-counted", K, "\t\tif k.queues[q.resource].push(req) {\n\t\t\treq.heads++\n\t\t}", "\t\tk.queues[q.resource].push(req)"),
    ("next-front-not-promoted", K, "\t\t\t\tif next.heads++; next.heads == len(p.nodes[next.process].requires) {", "\t\t\t\tif next.heads++; false {"),
    ("held-candidates-dropped", K, "\tfor _, req := range held {\n\t\theap.Push(&k.cands, req)\n\t}\n", ""),
    ("global-head-of-line", K, "\t\t\theld = append(held, req)\n\t\t\tcontinue", "\t\t\theld = append(held, req)\n\t\t\tbreak"),
    ("partial-acquire", K, "\t\tif !fits {\n\t\t\theld = append(held, req)", "\t\tif !fits {\n\t\t\tfor _, q := range reqs {\n\t\t\t\tif k.inUse[q.resource]+q.units <= p.resources[q.resource].capacity {\n\t\t\t\t\tk.inUse[q.resource] += q.units\n\t\t\t\t}\n\t\t\t}\n\t\t\theld = append(held, req)"),
    ("candidates-latest-first", K, "func (c candidates) Less(i, j int) bool { return c[i].seq < c[j].seq }", "func (c candidates) Less(i, j int) bool { return c[i].seq > c[j].seq }"),
    ("capacity-check-ignores-units", K, "\t\t\tif k.inUse[q.resource]+q.units > p.resources[q.resource].capacity {\n\t\t\t\tfits = false", "\t\t\tif k.inUse[q.resource]+1 > p.resources[q.resource].capacity {\n\t\t\t\tfits = false"),
    ("grant-takes-one-unit", K, "\t\t\tk.inUse[q.resource] += q.units\n\t\t\tk.observe(", "\t\t\tk.inUse[q.resource]++\n\t\t\tk.observe("),
    ("release-skipped", K, "\t\t\tk.inUse[q.resource] -= q.units\n", ""),
    ("release-one-unit", K, "\t\t\tk.inUse[q.resource] -= q.units\n", "\t\t\tk.inUse[q.resource]--\n"),
    ("no-dispatch-after-release", K, "\t\tif len(r.p.nodes[ev.Node].requires) > 0 {\n\t\t\tif err := k.needDispatch(); err != nil {", "\t\tif false {\n\t\t\tif err := k.needDispatch(); err != nil {"),
    ("dispatch-flag-stuck", K, "\tk.dispatchPending = false\n\tp := k.r.p\n", "\tp := k.r.p\n"),
    ("route-before-release", K, "\t\tk.observe(Observation{Kind: ObsServiceEnd, Entity: ev.Entity, Node: ev.Node})\n\t\tif len(r.p.nodes[ev.Node].requires) > 0 {", "\t\tif err := k.route(ev.Entity, ev.Node); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tk.observe(Observation{Kind: ObsServiceEnd, Entity: ev.Entity, Node: ev.Node})\n\t\tif len(r.p.nodes[ev.Node].requires) > 0 {"),
    # event priorities (D-032), including zero-time steps
    ("dispatch-priority-10", K, "\treturn k.r.schedule(0, PriorityDispatch, EventDispatch, -1, 0)", "\treturn k.r.schedule(0, PriorityImmediate, EventDispatch, -1, 0)"),
    ("delay-priority-0", K, "PriorityImmediate, EventDelayComplete, n, e)", "PriorityServiceComplete, EventDelayComplete, n, e)"),
    ("delay-priority-20", K, "PriorityImmediate, EventDelayComplete, n, e)", "PriorityDispatch, EventDelayComplete, n, e)"),
    ("service-priority-10", K, "PriorityServiceComplete, EventServiceComplete, req.process, req.entity)", "PriorityImmediate, EventServiceComplete, req.process, req.entity)"),
    # routing
    ("decision-last-route", K, "\t\tif u < rt.cumulative {\n\t\t\treturn rt.to, nil\n\t\t}", "\t\tif u < rt.cumulative && false {\n\t\t\treturn rt.to, nil\n\t\t}"),
    ("decision-u-ge", K, "\t\tif u < rt.cumulative {", "\t\tif u >= 1-rt.cumulative {"),
    ("decision-no-fallback", K, "\treturn node.routes[len(node.routes)-1].to, nil\n}", "\treturn 0, fmt.Errorf(\"%w: no route for U\", ErrInternal)\n}"),
    ("queue-enqueues-at-queue", K, "\t\t\treturn k.enqueue(e, node.buffer)", "\t\t\treturn k.enqueue(e, n)"),
    ("service-stream-arrival", K, "service.draw(k.r.stream(req.process, PurposeService))", "service.draw(k.r.stream(req.process, PurposeArrival))"),
    ("delay-stream-service", K, "node.delay.draw(k.r.stream(n, PurposeDelay))", "node.delay.draw(k.r.stream(n, PurposeService))"),
    ("routing-stream-delay", K, "u := k.r.stream(n, PurposeRouting).Uniform()", "u := k.r.stream(n, PurposeDelay).Uniform()"),
    ("abandoned-as-completed", K, "\t\t\tif node.outcome == model.SinkAbandoned {\n\t\t\t\tcompleted = 0\n\t\t\t}", ""),
    ("no-failure-obs", K, "\t\t\tk.observe(Observation{Kind: ObsFailure, Entity: e, Node: n})\n", ""),
    ("no-rework-obs", K, "\t\t\tk.observe(Observation{Kind: ObsRework, Entity: e, Node: n})\n", ""),
    # limits and accounting
    ("no-visit-limit", K, "if lim := k.r.p.limits.MaxEntityVisits; en.visits > lim {", "if lim := k.r.p.limits.MaxEntityVisits; false && en.visits > lim {"),
    ("queue-counts-once", K, "\t\t\tvisits = 2 // the queue, and the process whose buffer the entity waits in", "\t\t\tvisits = 1"),
    ("failure-not-a-visit", K, "\t\tvisits := 1\n", "\t\tvisits := 1\n\t\tif node.typ == model.NodeFailure {\n\t\t\tvisits = 0\n\t\t}\n"),
    ("visits-not-reset", K, "\ten.visits = 1 // the source is its first visit\n", ""),
    ("no-active-limit", K, "if lim := int64(r.p.limits.MaxActiveEntities); k.active > lim {", "if lim := int64(r.p.limits.MaxActiveEntities); false && k.active > lim {"),
    ("limit-error-without-context", K, "\t\t\treturn fmt.Errorf(\"%w: %s entering %s at tick %d\", ErrEntityVisits, k.r.EntityID(e), node.id, k.r.now)", "\t\t\treturn ErrEntityVisits"),
    ("sink-not-counted", K, "\t\t\tk.active--\n\t\t\tk.exited++\n", "\t\t\tk.exited++\n"),
    ("entity-record-kept", K, "\t\t\tk.r.release(e)\n", ""),
    ("no-peak", K, "\tk.peak = max(k.peak, k.active)\n", ""),
    ("arrival-number-missing", K, "Node: en.source, Value: int64(en.n)})", "Node: en.source})"),
    ("events-not-counted", K, "\tr.onProcessed = func() { k.events++ }", "\tr.onProcessed = func() {}"),
    ("ctx-ignored", K, "\terr := r.run(ctx, b)", "\terr := r.run(context.Background(), b)"),
    ("budget-ignored", K, "\terr := r.run(ctx, b)", "\terr := r.run(ctx, NewBudget(1<<62))"),
    ("no-index-check", K, "\tif int(index) >= p.replications {", "\tif false {"),
    ("handle-reused", R, "\tr.created++\n\tid := r.created\n", "\tid := r.created + 1\n"),
    # Equivalent: invariant guards that no input reaches; their failure cases
    # are produced by other mutants (entity-record-kept, candidates order).
    ("record-invariant-unchecked", K, "\tif err == nil && int64(len(r.entities)) != k.active {", "\tif false {", "equivalent"),
    ("fifo-pop-unchecked", K, "\t\t\tif queue.pop() != req {", "\t\t\tif queue.pop() != req && false {", "equivalent"),
]


def main() -> int:
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new, *flags) in enumerate(MUT):
            src = (REPO / f).read_text()
            if src.count(old) != 1:
                results.append((name, f"BAD-MUTANT (pattern found {src.count(old)} times)"))
                print("\t".join(results[-1]), flush=True)
                continue
            out = src.replace(old, new)
            if "slices." in new and '"slices"' not in out:
                out = out.replace("import (\n", 'import (\n\t"slices"\n', 1)
            m = pathlib.Path(tmp) / f"m{i}.go"
            m.write_text(out)
            ov = pathlib.Path(tmp) / f"m{i}.json"
            ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
            p = subprocess.run(["go", "test", "-count=1", "-timeout", "300s", "-overlay", str(ov), "./internal/simulation/..."],
                               cwd=REPO, capture_output=True, text=True, env=dict(os.environ))
            o = p.stdout + p.stderr
            if p.returncode == 0:
                status = "SURVIVED"
            elif "build failed" in o or "setup failed" in o:
                status = "BUILD-FAILED"
            else:
                status = "KILLED"
            first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:"))), "")
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
