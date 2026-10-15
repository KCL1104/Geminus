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
    ("no-resource-wise-fifo", K, "if k.blocked[q.resource] || k.inUse[q.resource]+q.units > p.resources[q.resource].capacity {", "if k.inUse[q.resource]+q.units > p.resources[q.resource].capacity {"),
    ("global-head-of-line", K, "\t\t\tkept = append(kept, req)\n\t\t\tcontinue\n", "\t\t\tkept = append(kept, req)\n\t\t\tfor i := range k.blocked {\n\t\t\t\tk.blocked[i] = true\n\t\t\t}\n\t\t\tcontinue\n"),
    ("partial-acquire", K, "\t\tif !free {\n\t\t\tfor _, q := range reqs {\n\t\t\t\tk.blocked[q.resource] = true\n\t\t\t}", "\t\tif !free {\n\t\t\tfor _, q := range reqs {\n\t\t\t\tk.blocked[q.resource] = true\n\t\t\t\tif k.inUse[q.resource]+q.units <= p.resources[q.resource].capacity {\n\t\t\t\t\tk.inUse[q.resource] += q.units\n\t\t\t\t\tk.observe(Observation{Kind: ObsResource, Resource: q.resource, Value: int64(k.inUse[q.resource])})\n\t\t\t\t}\n\t\t\t}"),
    ("no-dispatch-after-release", K, "\t\tif len(r.p.nodes[ev.Node].requires) > 0 {\n\t\t\tif err := k.needDispatch(); err != nil {", "\t\tif false {\n\t\t\tif err := k.needDispatch(); err != nil {"),
    ("dispatch-flag-stuck", K, "\tk.dispatchPending = false\n\tclear(k.blocked)", "\tclear(k.blocked)"),
    ("route-before-release", K, "\t\tk.observe(Observation{Kind: ObsServiceEnd, Entity: ev.Entity, Node: ev.Node})\n\t\tif len(r.p.nodes[ev.Node].requires) > 0 {", "\t\tif err := k.route(ev.Entity, ev.Node); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tk.observe(Observation{Kind: ObsServiceEnd, Entity: ev.Entity, Node: ev.Node})\n\t\tif len(r.p.nodes[ev.Node].requires) > 0 {"),
    ("decision-last-route", K, "\t\tif u < rt.cumulative {\n\t\t\treturn rt.to, nil\n\t\t}", "\t\tif u < rt.cumulative && false {\n\t\t\treturn rt.to, nil\n\t\t}"),
    ("decision-u-ge", K, "\t\tif u < rt.cumulative {", "\t\tif u >= 1-rt.cumulative {"),
    ("decision-no-fallback", K, "\treturn node.routes[len(node.routes)-1].to, nil\n}", "\treturn 0, fmt.Errorf(\"%w: no route for U\", ErrInternal)\n}"),
    ("delay-priority-0", K, "PriorityImmediate, EventDelayComplete, n, e)", "PriorityServiceComplete, EventDelayComplete, n, e)"),
    ("service-priority-10", K, "PriorityServiceComplete, EventServiceComplete, req.process, req.entity)", "PriorityImmediate, EventServiceComplete, req.process, req.entity)"),
    ("service-stream-arrival", K, "service.draw(k.r.stream(req.process, PurposeService))", "service.draw(k.r.stream(req.process, PurposeArrival))"),
    ("delay-stream-service", K, "node.delay.draw(k.r.stream(n, PurposeDelay))", "node.delay.draw(k.r.stream(n, PurposeService))"),
    ("routing-stream-delay", K, "u := k.r.stream(n, PurposeRouting).Uniform()", "u := k.r.stream(n, PurposeDelay).Uniform()"),
    ("lifo", K, "\tk.waiting = append(k.waiting, request{entity: e, process: process, enter: k.r.now, seq: k.reqSeq})", "\tk.waiting = append([]request{{entity: e, process: process, enter: k.r.now, seq: k.reqSeq}}, k.waiting...)"),
    ("no-visit-limit", K, "if lim := k.r.p.limits.MaxEntityVisits; en.visits > lim {", "if lim := k.r.p.limits.MaxEntityVisits; false && en.visits > lim {"),
    ("no-active-limit", K, "if lim := int64(r.p.limits.MaxActiveEntities); k.active > lim {", "if lim := int64(r.p.limits.MaxActiveEntities); false && k.active > lim {"),
    ("abandoned-as-completed", K, "\t\t\tif node.outcome == model.SinkAbandoned {\n\t\t\t\tcompleted = 0\n\t\t\t}", ""),
    ("no-failure-obs", K, "\t\t\tk.observe(Observation{Kind: ObsFailure, Entity: e, Node: n})\n", ""),
    ("no-rework-obs", K, "\t\t\tk.observe(Observation{Kind: ObsRework, Entity: e, Node: n})\n", ""),
    ("sink-not-counted", K, "\t\t\tk.active--\n\t\t\tk.exited++\n", "\t\t\tk.exited++\n"),
    ("granted-out-of-order", K, "\tfor _, req := range granted {", "\tfor _, req := range slices.Backward(granted) {"),
    ("release-skipped", K, "\t\t\tk.inUse[q.resource] -= q.units\n", ""),
    # T-020 review follow-ups in T-021
    ("entity-record-kept", K, "\t\t\tk.r.release(e)\n", ""),
    ("no-peak", K, "\tk.peak = max(k.peak, k.active)\n", ""),
    ("arrival-number-missing", K, "Node: en.source, Value: int64(en.n)})", "Node: en.source})"),
    ("visits-not-reset", K, "\ten.visits = 1 // the source is its first visit\n", ""),
    # Equivalent: the record invariant is a guard that only fires when another
    # bug keeps a record (entity-record-kept is killed through it).
    ("record-invariant-unchecked", K, "\tif err == nil && int64(len(r.entities)) != k.active {", "\tif false {", "equivalent"),
    ("handle-reused", R, "\tr.created++\n\tid := r.created\n", "\tid := r.created + 1\n"),
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
