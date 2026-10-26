#!/usr/bin/env bash
# Reviewer (T-053 r1): prepare the disposable review database and run the
# T-053 DB-backed Go tests on it. Disposable dt_test_* databases only.
# Usage: run-db.sh <dbname> <logdir>
set -u
db="$1"; out="$2"
case "$db" in dt_test_*) ;; *) echo "refusing: $db is not dt_test_*" >&2; exit 2;; esac
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
url="postgres://postgres@/$db?host=/var/lib/postgresql/geminus-t009-fresh&port=55440"
cd "$(dirname "$0")/../../../.."
DATABASE_URL="$url" DT_ALLOW_DB_TESTS=yes make db-test > "$out/db-test.log" 2>&1
echo "exit $?" >> "$out/db-test.log"
GEMINUS_TEST_DATABASE_URL="$url" GEMINUS_REQUIRE_DB_TESTS=1 go test -count=1 -race -v \
  -run '^(TestReserveAndSettle|TestLimitsRefuse|TestConcurrentReservationsNeverOvershoot|TestSettleKeepsItsDayAndOrganization|TestFailClosed|TestAMeteredProposal|TestASpendLimitRefusesTheProposal)$' \
  ./internal/storage/aispend/ ./internal/proposals/generator/ > "$out/t053-db-tests.log" 2>&1
echo "exit $?" >> "$out/t053-db-tests.log"
