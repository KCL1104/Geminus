#!/usr/bin/env bash
# Numerical-profile guard for the simulation engine and result statistics
# (docs/SIMULATION_SPEC.md §4, T-020 review): the Go compiler may fuse a*b+c
# into one FMA instruction on some targets, which rounds once instead of twice
# and makes a result depend on GOARCH. The engine code must round explicitly
# (float64(...) conversions), so the assembly listings of these packages must
# contain no fused multiply-add on any target that has one.
#
# Then, where the CPU allows, the numeric tests also run as a GOAMD64=v3
# (FMA-capable) build. Set REQUIRE_GOAMD64_V3=1 to fail instead of reporting
# NOT RUN when the CPU cannot run such a build.
set -euo pipefail
PKGS=()
for d in internal/simulation internal/results; do
  [ -d "$d" ] && PKGS+=("./$d/...")
done
TARGETS=("amd64 v3" "arm64" "ppc64le" "s390x" "riscv64" "loong64")
FUSED='\b(VFN?M(ADD|SUB)[0-9]+S[SD]|FN?M(ADD|SUB)[SD]?)\b'
# Calls into package math's transcendental functions, whose results differ
# between GOARCHs (assembly on some, pure Go elsewhere); ln is pinned instead.
PLATFORM='CALL[[:space:]]+math\.(arch)?([Ll]og|[Ll]og1p|[Ll]og2|[Ll]og10|[Ee]xp|[Ee]xp2|[Ee]xpm1|[Pp]ow|[Ss]in|[Cc]os|[Tt]an|[Ss]incos|[Aa]sin|[Aa]cos|[Aa]tan|[Aa]tan2|[Ss]inh|[Cc]osh|[Tt]anh|[Gg]amma|[Ll]gamma|[Ee]rf|[Ee]rfc|[Cc]brt|[Hh]ypot)(\(|<|\.)'
status=0
for spec in "${TARGETS[@]}"; do
  read -r arch level <<<"$spec"
  listing=$(GOARCH="$arch" GOAMD64="${level:-v1}" go build -gcflags=-S "${PKGS[@]}" 2>&1) || { echo "$listing" >&2; exit 1; }
  # A listing without the pinned logarithm would make this check vacuous.
  grep -q 'engine\.ln STEXT' <<<"$listing" || { echo "FAIL the $arch listing has no engine.ln" >&2; exit 1; }
  if fused=$(grep -E "$FUSED" <<<"$listing"); then
    echo "FAIL fused multiply-add in the $arch${level:+ $level} build:" >&2
    echo "$fused" >&2
    status=1
  elif calls=$(grep -E "$PLATFORM" <<<"$listing"); then
    echo "FAIL platform-dependent math call in the $arch${level:+ $level} build:" >&2
    echo "$calls" >&2
    status=1
  else
    echo "PASS no fused multiply-add or platform math call: $arch${level:+ $level}"
  fi
done
[ "$status" -eq 0 ] || exit "$status"
if grep -qw fma /proc/cpuinfo 2>/dev/null && grep -qw avx2 /proc/cpuinfo && grep -qw bmi2 /proc/cpuinfo && grep -qw movbe /proc/cpuinfo; then
  GOAMD64=v3 go test -count=1 "${PKGS[@]}"
  echo "PASS numeric tests as a GOAMD64=v3 build"
elif [ "${REQUIRE_GOAMD64_V3:-0}" = 1 ]; then
  echo "FAIL this CPU cannot run a GOAMD64=v3 build" >&2
  exit 1
else
  echo "NOT RUN numeric tests as a GOAMD64=v3 build (CPU lacks FMA/AVX2/BMI2/MOVBE)"
fi
