#!/bin/bash
# T-050 review round 1: the red of every test added for a finding, under the
# mutant it is for (red_under_mutant.py; scratch copy in $1, logs in $2).
s=$(dirname "$0")/red_under_mutant.py
r=$1
e=$2
E=./internal/simulation/engine/
D=./internal/simulation/des/
python3 "$s" "$r" "$e/red-m1-gbt18-under-m1-b2-since-ready-tick.log" m1-b2-since-ready-tick $E '^TestBatchStall$'
python3 "$s" "$r" "$e/red-m2-gbt19-under-optionB.log" r-b1-ignores-timeouts-after-horizon $E '^TestBatchGoldens$'
python3 "$s" "$r" "$e/red-m2-gbt20-under-deadlock-ignores-timeouts.log" deadlock-ignores-timeouts $E '^TestBatchDeadlocks$'
python3 "$s" "$r" "$e/red-m3-vector-under-setup-purpose-renamed.log" r-setup-purpose-renamed $E '^(TestSetupStreamVectors|TestSetupStreamsPerUnit)$'
python3 "$s" "$r" "$e/red-m3-gsu12-under-setup-ends-not-applied-at-finish.log" r-setup-ends-not-applied-at-finish $D '^TestSetupGoldenResults$'
python3 "$s" "$r" "$e/red-m4-gsu11-under-setup-complete-strict-w.log" r-setup-complete-strict-w $D '^TestSetupGoldenResults$'
python3 "$s" "$r" "$e/red-l1-gbt21-under-batch-end-dispatch-without-resources.log" r-batch-end-dispatch-without-resources $E '^TestBatchGoldens$'
python3 "$s" "$r" "$e/red-l2-gbt22-under-b1-since-oldest-entry.log" r-b1-since-oldest-entry $E '^TestBatchDeadlocks$'
