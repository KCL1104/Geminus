#!/usr/bin/env bash
# Reviewer (T-053 r1): re-run the author's unit mutants (U1..U46) with the
# author's harness, in the `git archive HEAD` copy (never the worktree),
# with no database (the DB mutants are the coordinator's run). The harness's
# final git-status line is meaningless in a copy without .git.
# Usage: unit-mutants.sh <copy> <log>
set -u
copy="$1"; log="$2"
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
unset GEMINUS_TEST_DATABASE_URL
ids=$(seq -s, -f 'U%g' 1 46)
cd "$copy" || exit 2
python3 checks/evidence/2026-10-26-T-053/mutants.py --only "$ids" > "$log" 2>&1
echo "exit $?" >> "$log"
