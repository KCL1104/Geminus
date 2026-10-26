#!/bin/bash
# usage: run.sh <logname> <command...>   (runs in the reviewer worktree, logs to evidence dir)
WT=/home/user/Geminus/.claude/worktrees/agent-abba1aa5d52ed7843
EV=$WT/checks/evidence/2026-10-26-T-050-r1/reviewer
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH
export GOTOOLCHAIN=local
cd "$WT" || exit 99
log="$EV/$1"; shift
{
  echo "# $(date -u +%Y-%m-%dT%H:%M:%SZ) cwd=$WT"
  echo "# cmd: $*"
} > "$log"
start=$(date +%s)
"$@" >> "$log" 2>&1
rc=$?
end=$(date +%s)
echo "# exit=$rc elapsed=$((end-start))s" >> "$log"
echo "$1 ... exit=$rc elapsed=$((end-start))s log=$log"
exit $rc
