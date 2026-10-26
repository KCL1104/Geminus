#!/usr/bin/env bash
# T-053 schema follow-up, review round 1 fixes: the new and changed tests,
# before the fix (red: tests.sh red) and after it (green: tests.sh green).
# Run from the repository root. Go 1.27.3 from the local toolchain, no
# network, no AI environment value is read or set; every HTTP test uses the
# package's local httptest server. Each log ends with the go test exit
# code.
set -u
phase=${1:?red or green}
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
E=checks/evidence/2026-10-26-T-053-schema-r1/fixes
pattern='^(TestDecodeBoundsTheNesting|TestDecodeFailsClosed|TestADeepValueMakesTheAnswerInvalid|FuzzDecode|TestDecodeOnlyTheEncodedPlaces|TestDecodeRefuses|TestDecodeCapsTheIssues|TestProjectionRefuses|TestTheSubsetGuard)$'
{
	date -u
	echo "commit: $(git rev-parse --short HEAD) ($phase; working tree: $(git status --porcelain -- internal | wc -l) changed file(s) under internal/)"
	echo "go test -count=1 -v -run '$pattern' ./internal/ai/provider/anthropic/"
	go test -count=1 -v -run "$pattern" ./internal/ai/provider/anthropic/ 2>&1 | grep -v '^=== RUN' | cut -c1-400
	echo "exit ${PIPESTATUS[0]}"
} >"$E/$phase-anthropic.log" 2>&1
grep -E '^(--- FAIL|--- PASS|ok|FAIL|exit)' "$E/$phase-anthropic.log"
