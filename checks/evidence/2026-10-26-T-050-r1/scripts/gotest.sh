#!/bin/bash
# T-050 review round 1: the Go toolchain of the evidence (go1.27.3, local),
# then `go test -count=1` with the arguments given, from the repository root
# (or the directory in REPO, e.g. a scratch copy for a mutant).
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
cd "${REPO:-$(dirname "$0")/../../../..}" || exit 2
exec go test -count=1 "$@"
