#!/bin/bash
# The des-go-0.5.0 regression locks (engine output, not an oracle) and the
# older locks, verbose: the observation lock (engine) and the Result lock
# (des).
d=$(dirname "$0")
"$d/gotest.sh" -v -run 'Digests' ./internal/simulation/engine/
a=$?
"$d/gotest.sh" -v -run 'Digests' ./internal/simulation/des/
b=$?
echo "# engine exit=$a des exit=$b"
[ $a -eq 0 ] && [ $b -eq 0 ]
