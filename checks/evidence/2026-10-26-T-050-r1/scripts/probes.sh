#!/bin/bash
# T-050 review round 1: the reviewer's probes P1-P6 (reviewer/*.go.txt) run
# again on this tree, each added to its package only through go test
# -overlay (no file is added to the tree), each logged on its own.
e=$(cd "$(dirname "$0")/.." && pwd)
r=$(cd "$e/../../.." && pwd)
t=$(mktemp -d)
probe() { # <log> <probe file> <package dir> <test>
  echo "{\"Replace\": {\"$r/$3/zz_review_probe_test.go\": \"$e/reviewer/$2\"}}" > "$t/ov.json"
  "$e/scripts/logrun.sh" "$e/$1" "$e/scripts/gotest.sh" -v -overlay "$t/ov.json" -run "^$4\$" "./$3/"
  echo "$4: exit $?"
}
probe probe-rerun-P1-b2-since.log probe_b2_since_test.go.txt internal/simulation/engine TestProbeB2SinceOfABatchRequest
probe probe-rerun-P2-b1-timeout-after-horizon.log probe_b1_timeout_after_horizon_test.go.txt internal/simulation/engine TestProbeB1WithATimeoutAfterTheHorizon
probe probe-rerun-P3-fresh-unit.log probe_fresh_unit_test.go.txt internal/simulation/engine TestProbeFreshUnitVersusChangeover
probe probe-rerun-P4-resource-free-batch.log probe_resource_free_batch_test.go.txt internal/simulation/engine TestProbeResourceFreeBatchEnd
probe probe-rerun-P5-setup-at-w.log probe_setup_at_w_test.go.txt internal/simulation/des TestProbeChangeoverStartingAtW
probe probe-rerun-P6-setup-end-after-last-observation.log probe_setup_end_after_last_observation_test.go.txt internal/simulation/des TestProbeChangeoverEndingAfterTheLastObservation
rm -r "$t"
