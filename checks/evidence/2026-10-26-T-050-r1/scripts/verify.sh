#!/bin/bash
# T-050 review round 1: the verification commands on this round's tree, each
# logged with its exit code (logrun.sh), in this order; exit codes collected
# in exit-codes.txt. The integration test runs on the fresh database $1
# (created for this run: createdb ... -T template0 --locale=C.UTF-8).
s=$(cd "$(dirname "$0")" && pwd)
e=$(cd "$s/.." && pwd)
db=$1
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
run() { # <log> <command...>
  "$s/logrun.sh" "$e/$1" "${@:2}"
  echo "$1: exit $?" >> "$e/exit-codes.txt"
}
run backend-test.log "$s/make.sh" backend-test GOFLAGS=-count=1
run architecture-check.log "$s/make.sh" architecture-check
run backend-numeric.log "$s/make.sh" backend-numeric
run ai-eval.log "$s/make.sh" ai-eval
run simulation-reference-test.log "$s/make.sh" simulation-reference-test
run race-simulation-results.log "$s/gotest.sh" -race -timeout 60m ./internal/simulation/... ./internal/results/...
run fuzz-batches-20000x.log "$s/gotest.sh" -run '^$' -fuzz '^FuzzBatches$' -fuzztime 20000x -parallel 2 ./internal/simulation/engine/
run fuzz-setups-20000x.log "$s/gotest.sh" -run '^$' -fuzz '^FuzzSetups$' -fuzztime 20000x -parallel 2 ./internal/simulation/engine/
export DATABASE_URL="postgres://postgres@/$db?host=/var/lib/postgresql/geminus-t009-fresh&port=55440"
export GEMINUS_TEST_DATABASE_URL=$DATABASE_URL DT_ALLOW_DB_TESTS=yes
run integration-test.log "$s/make.sh" integration-test
echo "verify done" >> "$e/exit-codes.txt"
