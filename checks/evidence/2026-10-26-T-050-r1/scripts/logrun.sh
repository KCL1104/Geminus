#!/bin/bash
# T-050 review round 1 (author's fixes): run a command, log its output with
# a header (time, cwd, command) and a footer (exit code, elapsed seconds).
#   logrun.sh <log file> <command...>
log=$1; shift
start=$(date +%s)
{ echo "# $(date -u +%FT%TZ) cwd=$(pwd)"; echo "# cmd: $*"; } > "$log"
"$@" >> "$log" 2>&1
code=$?
echo "# exit=$code elapsed=$(( $(date +%s) - start ))s" >> "$log"
exit $code
