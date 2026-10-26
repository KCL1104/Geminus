#!/usr/bin/env bash
# T-052 review r1 (I7): regenerate checks/evidence/2026-10-26-T-052/comparison-values.txt
# (or the file given as $1) with comparison_values_test.go.txt, put into
# internal/domainpack/electronicsline through go test -overlay: the tree is
# never changed. Run from the repository root:
#   bash checks/evidence/2026-10-26-T-052-r1/fixes/comparison-values.sh [out]
set -eu
repo=$(pwd)
here="$repo/checks/evidence/2026-10-26-T-052-r1/fixes"
out=${1:-"$repo/checks/evidence/2026-10-26-T-052/comparison-values.txt"}
case "$out" in /*) ;; *) out="$repo/$out" ;; esac
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
printf '{"Replace":{"%s":"%s"}}\n' "$repo/internal/domainpack/electronicsline/zz_comparison_values_test.go" \
  "$here/comparison_values_test.go.txt" > "$tmp/overlay.json"
COMPARISON_VALUES_OUT="$out" go test -count=1 -overlay "$tmp/overlay.json" -run '^TestZZComparisonValues$' \
  ./internal/domainpack/electronicsline/
