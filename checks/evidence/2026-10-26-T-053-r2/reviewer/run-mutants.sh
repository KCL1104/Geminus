#!/usr/bin/env bash
# Reviewer (T-053 r2): the round-1 mutation harness (98 mutants) and then the
# reviewer's N-series, one after the other, in a `git archive` copy of
# d150918, DB mutants on a disposable dt_test_* database prepared by
# make db-test (mutants-db-prepare.log). Each log ends with its exit code.
# Usage: run-mutants.sh <copy> <dt_test_db>
set -u
copy="$1"; db="$2"
case "$db" in dt_test_*) ;; *) echo "refusing: $db is not dt_test_*" >&2; exit 2;; esac
out="$(cd "$(dirname "$0")" && pwd)"
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
export GEMINUS_TEST_DATABASE_URL="postgres://postgres@/$db?host=/var/lib/postgresql/geminus-t009-fresh&port=55440" GEMINUS_REQUIRE_DB_TESTS=1
python3 "$out/../../2026-10-26-T-053-r1/mutants.py" "$copy" > "$out/mutants-r1-harness.log" 2>&1
echo "exit $?" >> "$out/mutants-r1-harness.log"
python3 "$out/reviewer-mutants.py" "$copy" > "$out/mutants-reviewer.log" 2>&1
echo "exit $?" >> "$out/mutants-reviewer.log"
