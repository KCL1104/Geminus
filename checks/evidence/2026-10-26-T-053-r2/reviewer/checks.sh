#!/usr/bin/env bash
# Reviewer (T-053 r2): the repository checks on d150918 in the worktree, each
# to its own log with the exit code as the last line, then make
# integration-test on a fresh dt_test_* database. No AI variable is set or
# read; nothing calls an AI provider (govulncheck fetches the Go
# vulnerability database only).
# Usage: checks.sh <fresh dt_test_db>
set -u
db="$1"
case "$db" in dt_test_*) ;; *) echo "refusing: $db is not dt_test_*" >&2; exit 2;; esac
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
cd "$(dirname "$0")/../../../.." || exit 2
out=checks/evidence/2026-10-26-T-053-r2/reviewer
step() { name="$1"; shift; "$@" > "$out/$name.log" 2>&1; echo "exit $?" >> "$out/$name.log"; tail -1 "$out/$name.log" | sed "s/^/$name: /"; }
step ai-eval make ai-eval
step architecture-check make architecture-check
step race-t053 go test -race -count=1 ./internal/ai/... ./internal/config/... ./internal/aievals/... ./internal/storage/aispend/... ./internal/proposals/generator/... ./cmd/worker/... ./cmd/ai-eval-live/... ./internal/audit/... ./internal/observability/...
step govulncheck go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./cmd/... ./internal/... ./schemas/... ./scripts/...
step go-mod-verify go mod verify
url="postgres://postgres@/$db?host=/var/lib/postgresql/geminus-t009-fresh&port=55440"
step integration-test env DATABASE_URL="$url" GEMINUS_TEST_DATABASE_URL="$url" DT_ALLOW_DB_TESTS=yes make integration-test
git status --short > "$out/git-status-after-checks.txt"
