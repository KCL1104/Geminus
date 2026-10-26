#!/usr/bin/env bash
# T-053 schema follow-up: the new tests against the stubs (types and fields
# only, no behaviour), before the fix. Run from the repository root with Go
# 1.27.3 first on PATH and GOTOOLCHAIN=local. No network, no key is read.
# Each log ends with the go test exit code.
set -u
E=checks/evidence/2026-10-26-T-053-schema
run() {
	local log=$1 pattern=$2
	shift 2
	{
		date -u
		echo "commit: $(git rev-parse --short HEAD) (working tree: the stubs and the new tests, not committed yet)"
		echo "go test -count=1 -v -run '$pattern' $*"
		go test -count=1 -v -run "$pattern" "$@" 2>&1 | grep -v '^=== RUN' | cut -c1-600
		echo "exit ${PIPESTATUS[0]}"
	} >"$E/$log" 2>&1
	grep -E '^(--- FAIL|--- PASS|ok|FAIL|exit)' "$E/$log" | grep -v '^    ' | sed "s|^|$log: |"
}
run red-anthropic.log '^(TestTheSubsetGuard|TestEverySchemaTheAdapterSendsIsInsideTheLimits|TestTheOpenValuesAreEncoded|TestDecodeOnlyTheEncodedPlaces|TestDecodeRefuses|TestProjectionRefuses|TestProjection|TestTheRequestSchemaIsInsideTheLimits|TestEncodedValuesDecode|TestEncodedValuesThatDoNotDecode|TestDecodedAnswersMeetTheFullSchema|TestTheProviderErrorType|TestTheLiveEvalDecodesTheEncodedValues|TestTheLiveEvalNamesTheProviderError)$' ./internal/ai/provider/anthropic/
run red-aievals.log '^TestAFailedCaseNamesTheProviderError$' ./internal/aievals/
run red-generator.log '^TestTheProviderErrorLine$' ./internal/proposals/generator/
run red-aiapproval.log '^TestTheLiveEvalWarnsOfAProviderError$' ./internal/config/aiapproval/
