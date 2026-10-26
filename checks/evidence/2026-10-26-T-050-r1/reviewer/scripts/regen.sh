#!/bin/bash
# Re-run the author's freeze tool on the 4584ba6 kernel sources and diff the result with the committed frozen040_test.go.
set -u
D=/tmp/claude-0/-home-user-Geminus/b4164204-ed28-580b-a7b5-c02b59026d38/scratchpad/freeze
WT=/home/user/Geminus/.claude/worktrees/agent-abba1aa5d52ed7843
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
cp "$WT/checks/evidence/2026-10-26-T-050/freeze040.go.txt" "$D/tool/main.go"
printf 'module freeze\ngo 1.24\n' > "$D/tool/go.mod"
cd "$D/tool" || exit 1
go run . ../src ../frozen040_regen.go || exit 2
if diff "$D/frozen040_regen.go" "$WT/internal/simulation/engine/frozen040_test.go" > "$D/frozen.diff"; then
  echo "IDENTICAL: regenerated frozen040 equals committed frozen040_test.go"
else
  echo "DIFFERENT:"; head -50 "$D/frozen.diff"
fi
