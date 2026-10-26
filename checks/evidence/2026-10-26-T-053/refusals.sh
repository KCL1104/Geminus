#!/usr/bin/env bash
# HISTORICAL RECORD, DO NOT RE-RUN (T-053 review round 1, L4). This script
# made ai-eval-live-refusals.log on the tree of 5a202cc, when the H-018
# record lacked the owner's retention confirmation and every step below
# refused before building a backend. Since H-026 the record is complete:
# steps 4 and 5 (H-018 + fake keys + GEMINUS_AI_LIVE=on) would now build
# the live backend and send requests to the production API (answered 401
# for the fake keys) and write a live report. The guard below stops it; the
# refusal paths are tested in process instead (internal/aievals
# TestMainRefusesWhileLiveIsOff, internal/config/aiapproval
# TestTheLiveEvalRunsOnlyWhenLiveIsOn and the spend record tests).
echo "refusals.sh is the record of the refusal run of 5a202cc; at this tree it would reach the production API. Not run." >&2
exit 2

# --- the original script, kept as it ran -----------------------------------
# Records the refusal paths of make ai-eval-live and make ai-smoke-live
# (T-053). No live call is possible: this build's H-018 record lacks the
# owner's retention confirmation, so the command refuses before building
# any backend. The keys are obviously fake strings; no real key is read.
set -u
cd "$1"
out="$2"
GOBIN=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin
base=(PATH="$GOBIN:/usr/bin:/bin" HOME=/root)
fake=(AI_PROVIDER=anthropic AI_POLICY_APPROVAL=H-018 AI_MODEL=claude-opus-5-5
  GEMINUS_ANTHROPIC_API_KEY_A=sk-ant-test-fake-aaaaaaaa GEMINUS_ANTHROPIC_API_KEY_B=sk-ant-test-fake-bbbbbbbb)
: > "$out"
step() {
  local title="$1"; shift
  echo "### $title" >> "$out"
  env -i "$@" >> "$out" 2>&1
  echo "exit=$?" >> "$out"
}
step "make ai-eval-live (clean environment)" "${base[@]}" make ai-eval-live
step "make ai-smoke-live (clean environment)" "${base[@]}" make ai-smoke-live
step "make ai-eval-live (H-018 named, fake keys, no switch)" "${base[@]}" "${fake[@]}" make ai-eval-live
step "make ai-smoke-live (H-018 named, fake keys, GEMINUS_AI_LIVE=on)" "${base[@]}" "${fake[@]}" GEMINUS_AI_LIVE=on make ai-smoke-live
step "make ai-eval-live (another model, fake keys, GEMINUS_AI_LIVE=on)" "${base[@]}" "${fake[@]}" AI_MODEL=claude-sonnet-5-5 GEMINUS_AI_LIVE=on make ai-eval-live
echo "### files in checks/ai-evals after the runs" >> "$out"
ls checks/ai-evals >> "$out"
