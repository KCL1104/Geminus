#!/bin/bash
# The first run of the review-round-1 hand goldens on the unchanged kernel
# and collector (engine: observations, stalls, deadlocks, the setup stream
# vector; des: the Result members). The 0.5.0 locks are run separately.
d=$(dirname "$0")
"$d/gotest.sh" -v -run '^(TestBatchGoldens|TestBatchStall|TestBatchDeadlocks|TestSetupGoldens|TestSetupStreamVectors|TestSetupStreamsPerUnit)$' ./internal/simulation/engine/
a=$?
"$d/gotest.sh" -v -run '^(TestBatchGoldenResults|TestSetupGoldenResults)$' ./internal/simulation/des/
b=$?
echo "# engine exit=$a des exit=$b"
[ $a -eq 0 ] && [ $b -eq 0 ]
