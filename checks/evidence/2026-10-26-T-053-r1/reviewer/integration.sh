#!/usr/bin/env bash
# Reviewer (T-053 r1): make integration-test on a fresh disposable
# dt_test_* database created for it (dropped first if it exists).
# Usage: integration.sh <dbname> <log>
set -u
db="$1"; log="$2"
case "$db" in dt_test_*) ;; *) echo "refusing: $db is not dt_test_*" >&2; exit 2;; esac
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
sock=/var/lib/postgresql/geminus-t009-fresh
su postgres -c "dropdb --if-exists -h $sock -p 55440 $db"
su postgres -c "createdb -h $sock -p 55440 -E UTF8 -T template0 --locale=C.UTF-8 $db" || exit 2
url="postgres://postgres@/$db?host=$sock&port=55440"
cd "$(dirname "$0")/../../../.."
DATABASE_URL="$url" GEMINUS_TEST_DATABASE_URL="$url" DT_ALLOW_DB_TESTS=yes make integration-test > "$log" 2>&1
echo "exit $?" >> "$log"
