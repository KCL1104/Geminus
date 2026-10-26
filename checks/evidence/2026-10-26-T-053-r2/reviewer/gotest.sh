#!/usr/bin/env bash
# Reviewer (T-053 r2): go test with Go 1.27.3 in a tree; when the second
# argument names a dt_test_* database, the DB-test variables for it. The log
# is the third argument; the exit code is its last line. No AI variable is
# set; nothing calls an AI provider.
# Usage: gotest.sh <tree> <dbname|-> <log> <go test args...>
set -u
tree="$1"; db="$2"; log="$3"; shift 3
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
if [ "$db" != "-" ]; then
  case "$db" in dt_test_*) ;; *) echo "refusing: $db is not dt_test_*" >&2; exit 2;; esac
  export GEMINUS_TEST_DATABASE_URL="postgres://postgres@/$db?host=/var/lib/postgresql/geminus-t009-fresh&port=55440" GEMINUS_REQUIRE_DB_TESTS=1
fi
cd "$tree" || exit 2
go test "$@" > "$log" 2>&1
code=$?
echo "exit $code" >> "$log"
exit $code
