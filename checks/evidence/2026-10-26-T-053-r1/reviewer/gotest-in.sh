#!/usr/bin/env bash
# Reviewer (T-053 r1): go test in another tree (the `git archive HEAD`
# copy the probes and mutants run in), log to the second argument.
# Usage: gotest-in.sh <tree> <log> <go test args...>
set -u
tree="$1"; log="$2"; shift 2
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
cd "$tree" || exit 2
go test "$@" > "$log" 2>&1
code=$?
echo "exit $code" >> "$log"
exit $code
