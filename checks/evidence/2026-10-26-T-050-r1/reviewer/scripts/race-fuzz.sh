#!/bin/bash
# Targeted race run of T-050's tests and short count-based fuzz runs, in the reviewer worktree.
WT=/home/user/Geminus/.claude/worktrees/agent-abba1aa5d52ed7843
EV=$WT/checks/evidence/2026-10-26-T-050-r1/reviewer
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
cd "$WT" || exit 1
step() {
  log=$1; shift
  echo "# $(date -u +%Y-%m-%dT%H:%M:%SZ) cmd: $*" > "$EV/$log"
  s=$(date +%s); "$@" >> "$EV/$log" 2>&1; rc=$?; e=$(date +%s)
  echo "# exit=$rc elapsed=$((e-s))s" >> "$EV/$log"
  echo "$log exit=$rc elapsed=$((e-s))s"
}
step race-t050.log go test -race -count=1 -timeout 1800s \
  -run 'Batch|Setup|Neutral|Frozen040|KernelRefuses|PrimitiveGate|CompileChecksSetups|GoldenDigests050|SimPyReferenceExport' \
  ./internal/simulation/engine/ ./internal/simulation/des/ ./internal/simulation/trace/ ./internal/results/metrics/
step fuzz-batches-20000x.log go test -count=1 -run '^$' -fuzz '^FuzzBatches$' -fuzztime 20000x -parallel 2 ./internal/simulation/engine/
step fuzz-setups-20000x.log go test -count=1 -run '^$' -fuzz '^FuzzSetups$' -fuzztime 20000x -parallel 2 ./internal/simulation/engine/
git -C "$WT" status --short
