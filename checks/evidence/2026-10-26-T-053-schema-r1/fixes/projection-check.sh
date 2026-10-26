#!/usr/bin/env bash
# Prints proposal-output's projection from a git archive copy of the fix
# (scratch only) in the format of the schema follow-up's
# projected-proposal-output.txt and compares the two files' sha256.
set -u
S=/tmp/claude-0/-home-user-Geminus/b4164204-ed28-580b-a7b5-c02b59026d38/scratchpad
TREE=/home/user/Geminus/.claude/worktrees/agent-a0a11ec7ab4ef5df4
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local PROJ_OUT=$S/projected-now.txt
cd "$S/proj-copy" || exit 2
echo "projection of $(cat "$S/head.rev") (git archive copy; a scratch test prints it with encoding/json MarshalIndent, tab)"
go test -count=1 -run TestZZPrintProjection -v ./internal/ai/provider/anthropic/ 2>&1 | grep -v '^=== RUN'
a=$(sha256sum <"$S/projected-now.txt" | cut -d' ' -f1)
b=$(sha256sum <"$TREE/checks/evidence/2026-10-26-T-053-schema/projected-proposal-output.txt" | cut -d' ' -f1)
echo "sha256 now:                                                  $a"
echo "sha256 checks/evidence/2026-10-26-T-053-schema/projected-proposal-output.txt: $b"
if [ "$a" = "$b" ]; then echo "identical"; code=0; else echo "DIFFERENT"; code=1; fi
echo "exit $code"
exit $code
