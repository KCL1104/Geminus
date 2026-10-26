#!/usr/bin/env bash
# verify.sh <worktree> <log> <command...>: runs the command in the worktree
# with Go 1.27.3 first on PATH and the venv's Python, writes its output and
# its exit code to the log.
set -u
cd "$1"
log="$2"
shift 2
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH
export PYTHON=/home/user/Geminus/.venv/bin/python
{
  echo "# $(date -u +%Y-%m-%dT%H:%M:%SZ) $*"
  echo "# go: $(go version)"
} > "$log"
"$@" >> "$log" 2>&1
code=$?
echo "exit=$code" >> "$log"
exit $code
