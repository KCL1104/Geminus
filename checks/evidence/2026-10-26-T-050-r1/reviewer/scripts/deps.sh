#!/bin/bash
# List the module-internal and third-party dependencies of internal/simulation/... (non-test and test builds).
WT=/home/user/Geminus/.claude/worktrees/agent-abba1aa5d52ed7843
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
cd "$WT" || exit 1
echo "== non-test deps of ./internal/simulation/... that are not the standard library =="
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./internal/simulation/... | sort -u
echo "== test deps (go list -test -deps) of ./internal/simulation/engine/ that are not the standard library =="
go list -test -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./internal/simulation/engine/ | sort -u
