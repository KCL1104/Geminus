#!/bin/bash
# Batch C: the setup-stream purpose rename (with the locks), then the mutants a lock killed first, without the locks.
S=/tmp/claude-0/-home-user-Geminus/b4164204-ed28-580b-a7b5-c02b59026d38/scratchpad
echo "== with the regression locks =="
python3 "$S/review-mutants.py" "$S/m" r-setup-purpose-renamed
echo "== WITHOUT_LOCKS=1 (go test -skip Digests) =="
WITHOUT_LOCKS=1 python3 "$S/review-mutants.py" "$S/m" r-setup-purpose-renamed r-setup-stream-by-process r-setup-from-service-stream
