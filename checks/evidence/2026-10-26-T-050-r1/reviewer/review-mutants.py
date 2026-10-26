#!/usr/bin/env python3
"""T-050 review (H-003): the reviewer's own mutants, on rules the author's
harness (checks/evidence/2026-10-26-T-050/t050-mutants.py) does not mutate.

Same mechanics as the author's harness: one exact search/replace per mutant,
applied with `go test -overlay` to a scratch copy of the tree (git archive
of a0fb365), never to the worktree; KILLED = the listed packages' tests
failed; a pattern not found exactly once = BAD-MUTANT. With WITHOUT_LOCKS=1
the regression locks (tests named *Digests*) are skipped, to see whether a
mutant is caught by anything but the engine's own recorded output.

    python3 review-mutants.py <repo> [name...]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

REPO = pathlib.Path(sys.argv[1])
ONLY = sys.argv[2:]
K = "internal/simulation/engine/kernel.go"
C = "internal/simulation/engine/compile.go"
M = "internal/results/metrics/aggregate.go"
VB = "internal/model/validate/batches.go"
RK = "internal/ai/review/risk.go"
RV = "internal/ai/review/review.go"
ENG = ["./internal/simulation/engine/"]
SIM = ["./internal/simulation/des/", "./internal/simulation/trace/", "./internal/results/..."]
VAL = ["./internal/model/..."]  # engine added only for a survivor
AI = ["./internal/ai/..."]

RNG = "internal/simulation/engine/rng.go"
DES_AND_ENG = ["./internal/simulation/engine/", "./internal/simulation/des/"]

MUT = [
    # R0. D-055's approved stream name (resource ID, "setup/<u>") renamed:
    # TestSetupStreamsPerUnit derives its expectation from the same Go
    # constant, so only an independent vector or a lock can tell.
    ("r-setup-purpose-renamed", RNG, 'const PurposeSetupPrefix = "setup/"', 'const PurposeSetupPrefix = "setups/"', DES_AND_ENG),
    # --- kernel: batches (D-054, D-096) ---
    # R1. The oldest member identified by its entry tick, not its entry
    # serial (D-096 (c): "以進入的序號識別，同一 tick 回到同一 buffer 的也分得開").
    ("r-oldest-by-entry-tick", K, "batchWaiter{e, k.r.now, k.serial}", "batchWaiter{e, k.r.now, uint64(k.r.now) + 1}", ENG),
    # R2. D-096 (g) option B: a BATCH_TIMEOUT after the horizon does not
    # hold B1 back.
    ("r-b1-ignores-timeouts-after-horizon", K, "\t\t\tk.timeouts++\n\t\t\tif err := k.r.schedule(front.enter+b.wait-k.r.now",
     "\t\t\tif front.enter+b.wait <= k.r.p.horizon {\n\t\t\t\tk.timeouts++\n\t\t\t}\n\t\t\tif err := k.r.schedule(front.enter+b.wait-k.r.now", ENG),
    # R3. B1's since of a batch request: the oldest member's entry, not the
    # ready tick (D-096 (g)).
    ("r-b1-since-oldest-entry", K, "req := &request{process: n, seq: k.reqSeq, entered: k.r.now, stalled: -1, deadline: -1, batch: true}",
     "req := &request{process: n, seq: k.reqSeq, entered: front.enter, stalled: -1, deadline: -1, batch: true}", ENG),
    # R4. A batch's end asks for a dispatch even when its process needs no
    # resource (serviceComplete asks only when it released a bundle).
    ("r-batch-end-dispatch-without-resources", K, "\t\tif err := k.release(reqs, 0); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif len(reqs) > 0 {\n",
     "\t\tif err := k.release(reqs, 0); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif true {\n", ENG),
    # R5. Members bound for one finite buffer do not see the places the
    # earlier members took (D-054: "後面的成員看到前面成員已占的位置").
    ("r-members-ignore-taken-places", K, "if k.waiting[b]+taken[b] >= k.r.p.nodes[b].capacity {", "if k.waiting[b] >= k.r.p.nodes[b].capacity {", ENG),
    # R6. A processed BATCH_TIMEOUT is never uncounted (B1 then never fires
    # after the first timeout).
    ("r-timeout-never-uncounted", K, "\tk.timeouts--\n\tif gen != k.tgen[n] {", "\tif gen != k.tgen[n] {", ENG),
    # R7. A batch in service does not count as in service for B1.
    ("r-batch-not-in-service", K, "\tk.waiting[n] -= size\n\tk.inService++\n", "\tk.waiting[n] -= size\n", ENG),
    # R8. Readiness at max_size + 1 (the other side of ready-below-max).
    ("r-ready-above-max", K, "if !ok || !(count >= b.max ||", "if !ok || !(count > b.max ||", ENG),
    # --- kernel: changeovers (D-055, D-097) ---
    # R9. The changeover stream keyed by the process, not the resource.
    ("r-setup-stream-by-process", K, "k.setupStreams[r][u] = Stream(k.r.seed, k.r.p.resources[r].id, PurposeSetupPrefix+strconv.Itoa(u))",
     "k.setupStreams[r][u] = Stream(k.r.seed, k.r.p.nodes[n].id, PurposeSetupPrefix+strconv.Itoa(u))", ENG),
    # R10. The changeover drawn from the process's service stream.
    ("r-setup-from-service-stream", K, "\t\tticks = s.draw(k.setupStreams[r][u])\n", "\t\tticks = s.draw(k.r.stream(n, PurposeService))\n", ENG),
    # R11. A unit set up for the type is taken even when it is down.
    ("r-matching-unit-may-be-down", K, "return !x.busy && !x.down && x.state == product", "return !x.busy && x.state == product", ENG),
    # R12. The matching unit of greatest index instead of least.
    ("r-matching-unit-greatest-index", K, "\tu := slices.IndexFunc(units, func(x unit) bool { return !x.busy && !x.down && x.state == product })\n",
     "\tu := -1\n\tfor i := len(units) - 1; i >= 0; i-- {\n\t\tif !units[i].busy && !units[i].down && units[i].state == product {\n\t\t\tu = i\n\t\t\tbreak\n\t\t}\n\t}\n", ENG),
    # --- Result (D-096 (j), D-097 (f)) ---
    # R13. A changeover's end applied only after a later tick's observation.
    ("r-setup-end-applied-late", M, "for len(c.setupEnds) > 0 && c.setupEnds[0].tick <= t {", "for len(c.setupEnds) > 0 && c.setupEnds[0].tick < t {", SIM),
    # R14. The ends after the last observation (before H) never applied.
    ("r-setup-ends-not-applied-at-finish", M, "\t\tc.endSetups(c.h) // a later end is cut at H by the integrals\n", "", SIM),
    # R15. A complete changeover must start strictly after W.
    ("r-setup-complete-strict-w", M, "if o.Tick >= c.w && o.Tick+o.Value <= c.h {", "if o.Tick > c.w && o.Tick+o.Value <= c.h {", SIM),
    # R16. A batch's bundle released from K at its first blocked member's leaving.
    ("r-batch-bundle-released-early", M, "(sign < 0 && left != 0)", "(sign < 0 && left > 1)", SIM),
    # --- validator (BATCH_INVALID, SETUP_INVALID, UNKNOWN_REFERENCE) ---
    ("r-max-wait-unbounded", VB, "if _, err := model.Ticks(*b.MaxWait, unit); err != nil {", "if _, err := model.Ticks(*b.MaxWait, unit); false && err != nil {", VAL),
    ("r-buffer-equal-to-max-refused", VB, "c > 0 && c < b.MaxSize {", "c > 0 && c <= b.MaxSize {", VAL),
    ("r-min-equal-max-refused", VB, "case b.MinSize > b.MaxSize:", "case b.MinSize >= b.MaxSize:", VAL),
    ("r-duplicate-pair-allowed", VB, "\t\t\tcase seen[pair]:\n", "\t\t\tcase false && seen[pair]:\n", VAL),
    ("r-second-setup-resource-allowed", VB, "\t\t\tcase first != \"\":\n", "\t\t\tcase false && first != \"\":\n", VAL),
    ("r-batch-needing-setups-allowed", VB, "\t\t\tcase n.Batch != nil:\n", "\t\t\tcase false && n.Batch != nil:\n", VAL),
    ("r-unknown-initial-allowed", VB, "if s.Initial != \"\" && !entity[s.Initial] {", "if false && s.Initial != \"\" && !entity[s.Initial] {", VAL),
    # --- D-057 risk and the review's values ---
    ("r-risk-ignores-max-wait", RK, "a.MaxSize == b.MaxSize && same(a.MaxWait, b.MaxWait)", "a.MaxSize == b.MaxSize && (same(a.MaxWait, b.MaxWait) || true)", AI),
    ("r-risk-ignores-transition-distribution", RK, "if t.From != u.From || t.To != u.To || !sameDistribution(t.Distribution, u.Distribution) {",
     "if t.From != u.From || t.To != u.To {", AI),
    ("r-risk-ignores-default", RK, " || (a.Default != nil && !sameDistribution(*a.Default, *b.Default))", "", AI),
    ("r-max-wait-not-a-value", RV, "\t\t\tadd(b.MaxWait)\n", "", AI),
]


def run_go(i, tmp, f, old, new, pkgs):
    src = (REPO / f).read_text().replace(old, new)
    m = pathlib.Path(tmp) / f"m{i}.go"
    m.write_text(src)
    ov = pathlib.Path(tmp) / f"m{i}.json"
    ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
    skip = ["-skip", "Digests"] if os.environ.get("WITHOUT_LOCKS") == "1" else []
    p = subprocess.run(["go", "test", "-count=1", "-failfast", "-timeout", "1200s", *skip, "-overlay", str(ov), *pkgs],
                       cwd=REPO, capture_output=True, text=True, env=dict(os.environ))
    return p.returncode, p.stdout + p.stderr


def main() -> int:
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new, pkgs) in enumerate(MUT):
            if ONLY and name not in ONLY:
                continue
            n = (REPO / f).read_text().count(old)
            if n != 1:
                results.append((name, f"BAD-MUTANT (pattern found {n} times)"))
                print("\t".join(results[-1]), flush=True)
                continue
            code, o = run_go(i, tmp, f, old, new, pkgs)
            fails = [l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:"))]
            first = fails[0] if fails else ""
            if code == 0:
                status = "SURVIVED"
            elif "build failed" in o or "setup failed" in o:
                status = "BUILD-FAILED"
                first = next((l for l in o.splitlines() if ".go:" in l), "")
            else:
                status = "KILLED"
            results.append((name, f"{status} {first[:150]}"))
            print("\t".join(results[-1]), flush=True)
    killed = sum(1 for r in results if r[1].startswith("KILLED"))
    print(f"{killed}/{len(results)} killed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
