#!/usr/bin/env bash
# T-056 review round 1: the verification, in order, each through run.sh
# (Go 1.27.3, every AI variable unset, the exit code as each log's last
# line); the exit codes also go to exit-codes.txt. The DB step takes a
# fresh dt_test_t056r1* database the caller created on the throwaway
# cluster; nothing here starts or stops a cluster, calls a provider or
# touches Railway.
#   verify.sh <dt_test_t056r1...>
set -u
here="$(cd "$(dirname "$0")" && pwd)"
db="$1"
codes="$here/exit-codes.txt"
: > "$codes"
step() {
  name="$1"; shift
  "$here/run.sh" "$@" > /dev/null
  echo "$name $?" >> "$codes"
}
CHANGED="./internal/proposals/... ./internal/ai/... ./internal/aievals/... ./internal/config/... ./cmd/ai-eval-live/ ./cmd/worker/ ./cmd/api/ ./internal/api/"
step gofmt gofmt.log bash -c 'unformatted=$(gofmt -l cmd internal schemas scripts prompts); echo "$unformatted"; [ -z "$unformatted" ]'
step vet vet.log go vet $CHANGED
step test-changed test-changed.log go test -count=1 $CHANGED
step race-changed race-changed.log go test -race -count=1 -timeout 30m $CHANGED
step backend-test backend-test.log make backend-test "GOFLAGS=-count=1 -timeout=30m"
step architecture-check architecture-check.log make architecture-check
step ai-eval ai-eval.log make ai-eval
step integration-test -db "$db" integration-test.log make integration-test PYTHON=/home/user/Geminus/.venv/bin/python
step frontend-test frontend-test.log make frontend-test
cat "$codes"
