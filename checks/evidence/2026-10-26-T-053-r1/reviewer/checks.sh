#!/usr/bin/env bash
# Reviewer (T-053 r1): the repository checks on HEAD, each to its own log
# with the exit code as the last line. No AI environment value is set or
# read; no network call to any AI provider (govulncheck fetches the Go
# vulnerability database only).
set -u
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
cd "$(dirname "$0")/../../../.."
out=checks/evidence/2026-10-26-T-053-r1/reviewer
step() { name="$1"; shift; "$@" > "$out/$name.log" 2>&1; echo "exit $?" >> "$out/$name.log"; tail -1 "$out/$name.log" | sed "s/^/$name: /"; }
step spec-check make spec-check PYTHON=/home/user/Geminus/.venv/bin/python
step ai-eval make ai-eval
step architecture-check make architecture-check
step race-t053 go test -race -count=1 ./internal/ai/... ./internal/config/... ./internal/aievals/... ./internal/storage/aispend/... ./internal/proposals/generator/... ./cmd/worker/... ./cmd/ai-eval-live/... ./internal/audit/... ./internal/observability/...
step govulncheck go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./cmd/... ./internal/... ./schemas/... ./scripts/...
step go-mod-verify go mod verify
git status --short > "$out/git-status-after-checks.txt"
