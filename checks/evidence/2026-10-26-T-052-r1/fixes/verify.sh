#!/usr/bin/env bash
# T-052 review round 1: the verification of the fixes, in order, each with its
# log and exit code (exit-codes.txt). Run from the repository root with go
# on PATH (GOTOOLCHAIN=local) and DB=<a fresh dt_test_* database name> on the
# shared cluster; the database is created here and dropped at the end. The
# cluster itself is never started or stopped.
set -u
EV=checks/evidence/2026-10-26-T-052-r1/fixes
SCRATCH=${SCRATCH:?set SCRATCH to a scratch directory}
: "${DB:?set DB to a fresh dt_test_* database name}"
case "$DB" in dt_test_*) ;; *) echo "refusing $DB" >&2; exit 2 ;; esac
HOST='host=/var/lib/postgresql/geminus-t009-fresh&port=55440'
ADMIN="postgresql://postgres@/postgres?$HOST"
URL="postgresql://postgres@/$DB?$HOST"
: > "$EV/exit-codes.txt"
step() { # step <name> <log> <command...>
  local name=$1 log=$2 start rc
  shift 2
  start=$(date -u +%s)
  { echo "\$ $*"; "$@"; } > "$EV/$log" 2>&1
  rc=$?
  echo "exit $rc" >> "$EV/$log"
  printf '%s %s exit %d (%d s) -> %s\n' "$(date -u +%TZ)" "$name" "$rc" "$(( $(date -u +%s) - start ))" "$log" >> "$EV/exit-codes.txt"
}
CHANGED=(./scripts/gen-electronics-line-data/ ./internal/domainpack/... ./internal/results/... ./internal/import/... ./internal/audit/ ./cmd/worker/)

go version > "$EV/environment.txt"; nproc >> "$EV/environment.txt"; uptime >> "$EV/environment.txt"
psql -X "$ADMIN" -Atc 'SELECT version()' >> "$EV/environment.txt"
git rev-parse HEAD >> "$EV/environment.txt"

step gofmt gofmt.log gofmt -l cmd internal schemas scripts prompts
step "go vet (changed packages)" vet.log go vet "${CHANGED[@]}" ./internal/api/
step "generator -check" generator-check.log go run ./scripts/gen-electronics-line-data -check
step "generator -check TZ=Asia/Taipei" generator-check-taipei.log env TZ=Asia/Taipei go run ./scripts/gen-electronics-line-data -check
step "generator -check TZ=America/New_York GOMAXPROCS=1" generator-check-newyork.log env TZ=America/New_York GOMAXPROCS=1 go run ./scripts/gen-electronics-line-data -check
step "go test -count=1 (changed packages)" unit-changed.log go test -count=1 "${CHANGED[@]}"
step "go test -race -count=1 -timeout 30m (changed packages)" race-changed.log go test -race -count=1 -timeout 30m "${CHANGED[@]}"
step "make architecture-check" architecture-check.log make architecture-check
step "make backend-test" backend-test.log make backend-test GOFLAGS=-count=1

step "createdb $DB" createdb.log psql -X "$ADMIN" -v ON_ERROR_STOP=1 -c "CREATE DATABASE $DB"
export DATABASE_URL="$URL" GEMINUS_TEST_DATABASE_URL="$URL" DT_ALLOW_DB_TESTS=yes
step "make integration-test ($DB)" integration-test.log make integration-test
step "go test -race -v synthetic line tests ($DB)" api-synthetic-line.log env GEMINUS_REQUIRE_DB_TESTS=1 go test -count=1 -race -v \
  -run '^(TestSyntheticStationLogImport|TestSyntheticStationLogRules|TestSyntheticStationLogIsTheTemplatesRun)$' ./internal/api/
step "author's t052-mutants.py with T052_DB=1 ($DB)" t052-mutants-rerun.log env T052_DB=1 python3 checks/evidence/2026-10-26-T-052/t052-mutants.py "$PWD" "$SCRATCH/author-mutants"
unset DATABASE_URL GEMINUS_TEST_DATABASE_URL DT_ALLOW_DB_TESTS
step "dropdb $DB" dropdb.log psql -X "$ADMIN" -v ON_ERROR_STOP=1 -c "DROP DATABASE $DB"
step "databases left" databases-left.log psql -X "$ADMIN" -Atc "SELECT count(*) FROM pg_database WHERE datname = '$DB'"
step "git status --short" git-status.log git status --short
cat "$EV/exit-codes.txt"
