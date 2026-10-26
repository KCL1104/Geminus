#!/usr/bin/env bash
# final.sh <worktree>: the final verification of T-053, one command after
# the other, each into its own log with its exit code; a summary of the
# exit codes in exit-codes.txt. No live call; no key is read.
set -u
wt="$1"
ev="$wt/checks/evidence/2026-10-26-T-053"
v=/tmp/claude-0/-home-user-Geminus/b4164204-ed28-580b-a7b5-c02b59026d38/scratchpad/verify.sh
: > "$ev/exit-codes.txt"
run() {
  local log="$1"; shift
  bash "$v" "$wt" "$ev/$log" "$@"
  echo "$log: exit $? ($*)" >> "$ev/exit-codes.txt"
}
run backend-test.log make backend-test GOFLAGS=-count=1
run backend-race.log make backend-race GOFLAGS=-count=1
run architecture-check.log make architecture-check
run ai-eval.log make ai-eval
run go-mod-verify.log go mod verify
run govulncheck.log env GOTOOLCHAIN=go1.27.3 go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./cmd/... ./internal/... ./schemas/... ./scripts/...
