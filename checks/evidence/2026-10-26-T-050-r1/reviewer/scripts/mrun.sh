#!/bin/bash
# usage: mrun.sh <logname> <command...>   (runs in the scratch copy, logs to the reviewer evidence dir)
M=/tmp/claude-0/-home-user-Geminus/b4164204-ed28-580b-a7b5-c02b59026d38/scratchpad/m
EV=/home/user/Geminus/.claude/worktrees/agent-abba1aa5d52ed7843/checks/evidence/2026-10-26-T-050-r1/reviewer
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH
export GOTOOLCHAIN=local
cd "$M" || exit 99
log="$EV/$1"; shift
{
  echo "# $(date -u +%Y-%m-%dT%H:%M:%SZ) cwd=scratch copy of a0fb365 (git archive HEAD) plus the reviewer's probe file"
  echo "# cmd: $*"
} > "$log"
start=$(date +%s)
"$@" >> "$log" 2>&1
rc=$?
end=$(date +%s)
echo "# exit=$rc elapsed=$((end-start))s" >> "$log"
echo "exit=$rc elapsed=$((end-start))s log=$log"
tail -30 "$log"
exit $rc
