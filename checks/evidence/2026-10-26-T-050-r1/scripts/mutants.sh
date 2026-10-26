#!/bin/bash
# T-050 review round 1: the whole mutation harness (checks/evidence/
# 2026-10-26-T-050/t050-mutants.py, 63 mutants) on a scratch copy of HEAD
# ($1, a git archive; bin/simpy-reference-go.json copied in for the Python
# mutants), first with the regression locks, then with WITHOUT_LOCKS=1
# (go test -skip Digests). Logs in $2.
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local PYTHON=/home/user/Geminus/.venv/bin/python
r=$1
e=$2
l=$(dirname "$0")/logrun.sh
cd "$r" || exit 2
"$l" "$e/mutants-run4.log" python3 checks/evidence/2026-10-26-T-050/t050-mutants.py "$r"
a=$?
WITHOUT_LOCKS=1 "$l" "$e/mutants-run4-without-locks.log" python3 checks/evidence/2026-10-26-T-050/t050-mutants.py "$r"
b=$?
echo "with locks exit=$a, without locks exit=$b" > "$e/mutants-run4-exit.txt"
