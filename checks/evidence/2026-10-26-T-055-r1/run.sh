#!/usr/bin/env bash
# T-055 review round 1 fixes: run a command at the repository root with Go
# 1.27.3, its output to the log named by the first argument (relative to
# this directory), the exit code as the log's last line. A second form
# names a dt_test_* database first and sets the DB-test variables for it
# (on the coordinator's throwaway PostgreSQL 16 cluster, socket
# /var/lib/postgresql/geminus-throwaway-hkb6, port 55450, while the shared
# cluster on 55440 is down; neither is ever started or stopped here):
#   run.sh <log> <command...>
#   run.sh -db <dt_test_name> <log> <command...>
# Every AI and deployment variable is removed from the command's
# environment by name (no value is read or printed); no network call to any
# provider: the tests talk to local httptest servers only.
set -u
url=""
if [ "$1" = "-db" ]; then
  db="$2"; shift 2
  case "$db" in dt_test_*) ;; *) echo "refusing: $db is not dt_test_*" >&2; exit 2;; esac
  url="postgres://postgres@/$db?host=/var/lib/postgresql/geminus-throwaway-hkb6&port=55450"
fi
here="$(cd "$(dirname "$0")" && pwd)"
log="$here/$1"; shift
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
root="$(cd "$here/../../.." && pwd)"
envs=()
if [ -n "$url" ]; then
  envs=(GEMINUS_TEST_DATABASE_URL="$url" GEMINUS_REQUIRE_DB_TESTS=1 DATABASE_URL="$url" DT_ALLOW_DB_TESTS=yes)
fi
unset_names=(GEMINUS_MUSE_API_KEY GEMINUS_META_API_KEY MODEL_API_KEY GEMINUS_ANTHROPIC_API_KEY_A GEMINUS_ANTHROPIC_API_KEY_B
  ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN AI_API_KEY GEMINUS_AI_LIVE AI_PROVIDER AI_POLICY_APPROVAL AI_MODEL
  AI_VISION_PROVIDER AI_VISION_POLICY_APPROVAL AI_VISION_MODEL RAILWAY_TOKEN RAILWAY_API_TOKEN)
uargs=()
for n in "${unset_names[@]}"; do uargs+=(-u "$n"); done
env -C "$root" "${uargs[@]}" "${envs[@]}" "$@" > "$log" 2>&1
code=$?
echo "exit $code" >> "$log"
grep -E '^(--- FAIL|--- SKIP|ok |FAIL|PASS|panic|exit )' "$log" | head -60
exit $code
