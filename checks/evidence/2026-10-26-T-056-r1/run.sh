#!/usr/bin/env bash
# T-056 review round 1 fixes: run a command at the repository root with Go
# 1.27.3, its output to the log named by the first argument (relative to
# this directory), the exit code as the log's last line. A second form names
# a dt_test_t056r1* database first and sets the DB-test variables for it
# (the throwaway PostgreSQL cluster, socket
# /var/lib/postgresql/geminus-throwaway-hkb6, port 55450; no cluster is ever
# started or stopped here):
#   run.sh <log> <command...>
#   run.sh -db <dt_test_t056r1...> <log> <command...>
# Every AI variable is unset (names only; no value is read or printed); no
# network call to any provider.
set -u
url=""
if [ "$1" = "-db" ]; then
  db="$2"; shift 2
  case "$db" in dt_test_t056r1*) ;; *) echo "refusing: $db is not dt_test_t056r1*" >&2; exit 2;; esac
  url="postgres://postgres@/$db?host=/var/lib/postgresql/geminus-throwaway-hkb6&port=55450"
fi
here="$(cd "$(dirname "$0")" && pwd)"
log="$here/$1"; shift
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
for v in AI_PROVIDER AI_POLICY_APPROVAL AI_MODEL GEMINUS_AI_LIVE AI_VISION_PROVIDER AI_VISION_POLICY_APPROVAL AI_VISION_MODEL \
  GEMINUS_MUSE_API_KEY GEMINUS_ANTHROPIC_API_KEY_A GEMINUS_ANTHROPIC_API_KEY_B MODEL_API_KEY ANTHROPIC_API_KEY AI_EVAL_LIVE_MAX_USD; do
  unset "$v"
done
cd "$here/../../.." || exit 2
if [ -n "$url" ]; then
  export GEMINUS_TEST_DATABASE_URL="$url" GEMINUS_REQUIRE_DB_TESTS=1 DATABASE_URL="$url" DT_ALLOW_DB_TESTS=yes
fi
"$@" > "$log" 2>&1
code=$?
echo "exit $code" >> "$log"
grep -E '^(--- FAIL|--- SKIP|ok |FAIL|PASS|panic|exit )' "$log" | head -80
exit $code
