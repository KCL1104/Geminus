#!/usr/bin/env bash
# T-053 schema follow-up, review round 1 fixes: the verification commands,
# each through run.sh (its log in this directory, the exit code as the
# log's last line). Run from the repository root:
#   verify.sh go           gofmt + go vet, go test, go test -race
#   verify.sh make         backend-test, architecture-check, ai-eval, spec-check
#   verify.sh integration  make integration-test on a fresh dt_test_t053m1it,
#                          created empty here and dropped afterwards
#   verify.sh final        on the report's commit (a comment-only change of
#                          encoded.go): gofmt, go vet and go test of the
#                          adapter, then spec-check again
# No AI environment value is read or set; no network call to any provider.
set -u
here=$(cd "$(dirname "$0")" && pwd)
run="$here/run.sh"
pkgs="./internal/ai/... ./internal/aievals/... ./cmd/..."
summary() { echo "$1: $(tail -1 "$here/$1")"; }
case "${1:?go, make or integration}" in
go)
	bash "$run" vet-fmt.log bash "$here/verify.sh" vet-fmt; summary vet-fmt.log
	# shellcheck disable=SC2086
	bash "$run" test.log go test -count=1 $pkgs; summary test.log
	# shellcheck disable=SC2086
	bash "$run" race.log go test -race -count=1 $pkgs; summary race.log
	;;
vet-fmt)
	out=$(gofmt -l cmd internal schemas scripts prompts)
	echo "gofmt -l cmd internal schemas scripts prompts: ${out:-(nothing)}"
	[ -z "$out" ] || exit 1
	echo "go vet $pkgs"
	# shellcheck disable=SC2086
	go vet $pkgs
	;;
make)
	bash "$run" backend-test.log make backend-test GOFLAGS=-count=1; summary backend-test.log
	bash "$run" architecture-check.log make architecture-check; summary architecture-check.log
	bash "$run" ai-eval.log make ai-eval; summary ai-eval.log
	bash "$run" spec-check.log make spec-check PYTHON=/home/user/Geminus/.venv/bin/python; summary spec-check.log
	;;
final)
	bash "$run" final-anthropic.log bash "$here/verify.sh" final-anthropic; summary final-anthropic.log
	bash "$run" spec-check-final.log make spec-check PYTHON=/home/user/Geminus/.venv/bin/python; summary spec-check-final.log
	;;
final-anthropic)
	out=$(gofmt -l internal/ai/provider/anthropic)
	echo "gofmt -l internal/ai/provider/anthropic: ${out:-(nothing)}"
	[ -z "$out" ] || exit 1
	go vet ./internal/ai/provider/anthropic/ && go test -count=1 ./internal/ai/provider/anthropic/
	;;
integration)
	db=dt_test_t053m1it
	admin="postgres://postgres@/postgres?host=/var/lib/postgresql/geminus-t009-fresh&port=55440"
	psql -X "$admin" -v ON_ERROR_STOP=1 -qc "CREATE DATABASE $db" || exit 2
	bash "$run" -db "$db" integration-test.log make integration-test; summary integration-test.log
	psql -X "$admin" -v ON_ERROR_STOP=1 -qc "DROP DATABASE $db" && echo "dropped $db"
	;;
esac
