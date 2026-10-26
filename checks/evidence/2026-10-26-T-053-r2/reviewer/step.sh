#!/usr/bin/env bash
# Reviewer (T-053 r2): run one command in a tree with Go 1.27.3, its output
# to a log whose last line is the exit code. No AI variable is set or read.
# Usage: step.sh <tree> <log> <command...>
set -u
tree="$1"; log="$2"; shift 2
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
cd "$tree" || exit 2
"$@" > "$log" 2>&1
code=$?
echo "exit $code" >> "$log"
exit $code
