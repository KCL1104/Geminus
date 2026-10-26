#!/usr/bin/env bash
# T-055: run a command at the repository root with Go 1.27.3, its output
# to the log named by the first argument (relative to this directory), the
# exit code as the log's last line. A second form names a dt_test_*
# database first and sets the DB-test variables for it (the shared
# PostgreSQL 16 cluster's socket; it is never started or stopped here):
#   run.sh <log> <command...>
#   run.sh -db <dt_test_name> <log> <command...>
# No AI environment value is read or set; no network call to any provider.
set -u
url=""
if [ "$1" = "-db" ]; then
  db="$2"; shift 2
  case "$db" in dt_test_*) ;; *) echo "refusing: $db is not dt_test_*" >&2; exit 2;; esac
  url="postgres://postgres@/$db?host=/var/lib/postgresql/geminus-t009-fresh&port=55440"
fi
here="$(cd "$(dirname "$0")" && pwd)"
log="$here/$1"; shift
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
cd "$here/../../.." || exit 2
if [ -n "$url" ]; then
  export GEMINUS_TEST_DATABASE_URL="$url" GEMINUS_REQUIRE_DB_TESTS=1 DATABASE_URL="$url" DT_ALLOW_DB_TESTS=yes
fi
"$@" > "$log" 2>&1
code=$?
echo "exit $code" >> "$log"
grep -E '^(--- FAIL|--- SKIP|ok |FAIL|PASS|panic|exit )' "$log" | head -60
exit $code
