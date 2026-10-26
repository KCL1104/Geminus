# T-050 independent review, round 1 (H-003)

Reviewer: independent agent (did not write T-050). Date: 2026-10-26.
Scope: T-050 commits 14b7000..79c9423 (`git diff 8948b70 79c9423`), checked on HEAD a0fb365 of this worktree (later commits fc0b277..a0fb365 are T-053/docs; none of them touches a T-050 file, D-096, D-097 or T-050's entries in `planning/`).

**Verdict: CHANGES REQUESTED** (no High finding; 4 Medium, 3 Low).

The kernel, validator, collector and trace changes are careful and, as far as I could find, implement the approved D-048/D-049/D-050/D-054/D-055/D-057/D-058 text correctly. I found no determinism defect (no map iteration decides order; streams are keyed by (ID, purpose); every new tie is broken by event priority and sequence), the frozen des-go-0.4.0 copy is a faithful rename, the claimed runs reproduce on this tree, the three hand goldens I re-derived independently match, and D-096/D-097 stay PROPOSED. The changes I request are: (M1) the text of D-096 (g), which the owner is asked to approve, does not describe the implemented B2 behavior; (M2) an untested and, I think, unjustified option inside D-096 (g); (M3) two approved rules that only the engine's own regression lock pins (the D-055 stream name, and changeover ends after the last observation in the Result); (M4) the W boundary of a complete changeover (D-097 (f)) has no test.

Notes on the mechanics: every mutant and probe ran on a scratch copy of this tree (`git archive a0fb365`), never on the worktree. Mutants are applied with `go test -overlay` (same mechanics as the author's harness; my harness is `review-mutants.py`). Probes P2–P6 are added to the build only through `-overlay` and P1 was removed from the scratch copy before any mutant ran, so no probe affected a mutant result (the `# cwd=...plus the reviewer's probe file` header line of the logs is a leftover of my wrapper script and is inaccurate for the mutant logs). "Killed by a lock" means the first failing test was `TestBatchSetupObservationDigests` or `TestGoldenDigests050`, the engine's recorded output, not an oracle; I reran those mutants with `WITHOUT_LOCKS=1` (`go test -skip Digests`).

## Findings

### Medium

**M1. D-096 (g) and SIMULATION_SPEC §3 misdescribe B2's `since` for a batch request; the golden cited for it cannot tell the two readings apart.**

- Where: `docs/DECISIONS_AND_TBD.md:428` (D-096 (g): "B1、B2 對 batch request 回報它的最老成員…，since 為批次就緒的 tick（G-BT13）"); `docs/SIMULATION_SPEC.md:69` (same sentence); `checks/T-050_VALIDATION.md:149` ("G-BT13（…ErrStalled 回報最老成員，since 為就緒的 tick）"). Code: `internal/simulation/engine/kernel.go:1464-1475` (`stall()` reports `due.stalled`, the first tick at whose end the request was in B2's state, as for every request under D-074 (c)); the checker agrees with the kernel, not with the text (`kernel_check_batch_test.go:158`, `delete(c.cal.since, batchKey(p))`, then `b2Update`).
- What is wrong: for B2 the implemented `since` is the B2-state tick (D-074 (c)), not the tick the batch became ready. They coincide in G-BT13 (both 20 min), so G-BT13 "fixes" neither reading. The owner is asked to approve D-096 (g) as written; approving it would approve a behavior the kernel does not have (or, read literally, require changing the kernel).
- How shown: probe `probe_b2_since_test.go.txt` (log `probe-b2-since.log`, exit 1 by design). A batch process `work` (min = max = 1) needs `machine` + `operator`; its only member arrives at 20 min while `machine` is busy with another process's entity until 100 min; the operator is off shift until 960; R4 bypassed as in G-BT13. Result: `ErrStalled ... since 6000000 (= 100 min) deadline 57600000 (= 960 min)`; the text predicts since = 1200000 (20 min).
- Fix: change the text of D-096 (g) (and SIMULATION_SPEC §3, the report's table) to say that B2's `since` of a batch request is, as for any request, the first tick at whose end it is in B2's state (D-074 (c)), while B1's (`DeadlockError.Since`, `Queued`) is the tick it became ready; add a golden in which the two differ (the probe's model is one). Keep D-096 PROPOSED.

**M2. D-096 (g) option A (a BATCH_TIMEOUT after the horizon keeps D-050 B1 off for the rest of the run) is pinned by no test, and its stated reason does not hold for timeouts after the horizon.**

- Where: `internal/simulation/engine/kernel.go:1139-1146` (`k.timeouts++` also for a timeout `schedule` drops because it is after the horizon) and `:1374` (`k.timeouts > 0` suppresses B1); D-096 (g) ("含過時的與晚於 horizon 的…逾時可能讓批次開始；晚於 horizon 的永遠不會處理，所以 B1 在那之後也不判定，保守"; option B "不計晚於 horizon 的逾時（更早報 deadlock）").
- What is wrong: (1) Untested: my mutant `r-b1-ignores-timeouts-after-horizon` (option B: count a timeout only when it is at or before the horizon) survives the whole engine package (`mutants-A.log`), so the option the owner is asked to choose between is not distinguished by any golden, checker or fuzz target. (2) A timeout after the horizon is never processed, so it can never start a batch within the run; counting it can only suppress B1. Option A therefore turns off the backstop for every circular wait in the run — unrelated ones included — as soon as one batch member's wait limit lands after the horizon, with no case where it avoids a false ErrDeadlock. (For timeouts within the horizon I could not construct a B1 state that a timeout resolves unless BATCH_INVALID's capacity ≥ max_size is bypassed: with it, an entity blocked at a batch buffer means the buffer holds ≥ max_size, so that batch is already ready, and a batch elsewhere started by a timeout only takes units and adds requests at the back of FIFOs. This is reasoning, not a proof; the point of the finding does not depend on it.) Practical impact is limited to models past the validator (valid models never reach B1, R1), which is why this is Medium, not High.
- How shown: probe `probe_b1_timeout_after_horizon_test.go.txt`. G-D2 (blocking-goldens.json, R1 bypassed) alone: `ErrDeadlock at tick 420000` (7 min). G-D2 plus an unrelated resource-free batch process `bp` (min 1, max 2, max_wait 100) with one member at 1 min, horizon 60: the run ends with **no error** — the cycle stays stuck for 53 minutes unreported (`probe-b1-timeout-after-horizon.log`, exit 0). The same probe with the option-B mutant applied: `ErrDeadlock at tick 420000` (`probe-b1-timeout-after-horizon-under-optionB-mutant.log`, exit 1), so the probe kills the mutant.
- Fix: recommend option B in D-096 (g) (or give a case in which A avoids a wrong ErrDeadlock), and add a test pinning whichever option the owner picks (the probe's two runs are enough). Keep D-096 PROPOSED.

**M3. Two approved rules are pinned only by the 0.5.0 regression locks (engine output), not by any golden, checker, vector or reference.**

1. *The changeover stream name (resource ID, `setup/<u>`)* — D-055 ("stream 為 (resource ID, `setup/<u>`)") and ADR-026 D-055. `internal/simulation/engine/rng.go:58` (`const PurposeSetupPrefix = "setup/"`). Mutant `r-setup-purpose-renamed` (`"setup/"` → `"setups/"`): killed by `TestBatchSetupObservationDigests` (`mutants-C.log`), **survives with the locks skipped**. `TestSetupStreamsPerUnit` derives its expectation from the same Go constant (`engine/setup_test.go:217`), so it cannot see a rename; the SimPy reference uses Python's own `random`. For T-048's `ttf/<u>`, `ttr/<u>` the repository has an independent Python vector (`scripts/engine_reference.py` `breakdown_streams()`, checked by `sample_t048_test.go` and `PASS_ENGINE_REFERENCE_VECTORS`); T-050 added none for `setup/<u>` (`grep -c setup/ testdata/simulation/engine-vectors.json` = 0). AGENTS.md §5 asks for fixed golden vectors of RNG streams. Fix: add a `setup_streams` entry to `scripts/engine_reference.py` / `engine-vectors.json` (as `breakdown_streams`), or at least spell the purpose string literally in `TestSetupStreamsPerUnit`.
2. *Changeover ends after the last observation* — D-097 (f) / SIMULATION_SPEC §5 ("換線的結束沒有 observation，collector 以 min-heap 處理"). `internal/results/metrics/aggregate.go:268-269` (`Finish` → `c.endSetups(c.h)`). Mutant `r-setup-ends-not-applied-at-finish` (drop that line): killed by `TestGoldenDigests050` (`mutants-B.log`), **survives with the locks skipped** (`mutants-D-without-locks.log`). Probe P6 (`probe_setup_end_after_last_observation_test.go.txt`): G-SU1 over [0, 49] — changeovers 20–23, 25–28, 40–43, 45–48, the last one ending after the last observation — gives `setup_ticks 720000` (12 min, hand count) on the kernel and `780000` with the mutant, `scheduled_processing_ticks` 660000 vs 600000 (`probe-setup-end-after-last-observation*.log`). No hand golden has a changeover ending after the run's last observation (G-SU8/G-SU9 always have a later observation).
- Fix: add P6 (or an equivalent hand golden) to `setup-goldens.json`'s Result cases.

**M4. The W boundary of a complete changeover (D-097 (f): "開始 ≥ W、結束 ≤ H") has no test.**

- Where: `internal/results/metrics/aggregate.go:342` (`if o.Tick >= c.w && o.Tick+o.Value <= c.h`).
- How shown: mutant `r-setup-complete-strict-w` (`>=` → `>`) survives everything, locks included (`mutants-B.log`). Probe P5 (`probe_setup_at_w_test.go.txt`): G-SU1 with warm-up 20 min, horizon 55 (B1's changeover starts exactly at W): kernel `setups_started 4, setups_completed 4, completed_setup_ticks 720000` (my hand count: 20, 25, 40, 45, 3 min each); with the mutant `setups_completed 3, completed_setup_ticks 540000` (`probe-setup-at-w*.log`). G-SU8's window [22, 41] has no changeover starting at W. The H side is covered (`setup-complete-ignores-end` is killed), and the batch W/H boundary is covered (G-BT11).
- Fix: add a hand golden (or move G-SU8's W) so that a changeover starts exactly at W.

### Low

**L1. A resource-free batch process (valid: `TestBatchRules` "a batch process without resources") is in no golden, lock or checker run, so the "no dispatch after a batch end that releases no bundle" rule is untested.** `internal/simulation/engine/kernel.go:1336` (`if len(reqs) > 0 { needDispatch }`, as `serviceComplete` does). Mutant `r-batch-end-dispatch-without-resources` (always ask) survives everything, locks included (`mutants-A.log`). It is behavior-changing: probe P4 (`probe_resource_free_batch_test.go.txt`; 4 arrivals into a resource-free min = max = 2 batch process) gives 8 events on the kernel (my hand count) and 10 with the mutant, and the sequence numbers of later observations shift (`probe-resource-free-batch*.log`). Only event counts and sequences differ, hence Low. Fix: add P4's model as a golden (events, BatchStart sequences).

**L2. B1's `DeadlockError.Since` for a batch request (the `Queued` branch: "since 為批次就緒的 tick") is untested.** `kernel.go:1168` (`entered: k.r.now`) and `:1415`. Mutant `r-b1-since-oldest-entry` (`entered: front.enter`) survives (`mutants-A.log`). The branch needs a held, release-bound request with nothing blocked, i.e. a requirement larger than the capacity past the validator, so it is close to unreachable; Low. Fix: one validator-bypassed golden, or state in D-096 (g) that this branch is defensive.

**L3. The AI scenario rules for batches omit one BATCH_INVALID condition.** `internal/ai/aicontext/proposal.go:66-69` and `prompts/scenario/system.md:5` list 1 ≤ min ≤ max ≤ 10000, max_wait when min < max and buffer ≥ max_size, but not "max_wait at most 365 days once quantized" (D-096 (i), `validate/batches.go:49`). The server still refuses such a candidate, so nothing unsafe happens; the model is just told less than the validator checks. Fix: add the bound to both texts with the next scenario prompt version.

### Info

- **I1. D-055's unit choice and never-used units (consider listing in D-097).** With no `initial`, a unit that has never served is not "set up for" any type, so a grant prefers the least-index idle unit even when that means a changeover and another idle unit would need none. Probe P3 (`probe_fresh_unit_test.go.txt`, `probe-fresh-unit.log`): two units; a1 (board_a) uses unit 0; b1 (board_b) at 20 gets unit 0 with a 7-minute changeover while unit 1 has never been used. This is the literal approved rule ("優先狀態相符者，否則 index 最小的可用 unit") and the checker and SimPy script read it the same way, so it is not a defect, but it is the kind of reading D-097 lists for the owner and it is not there.
- **I2. Equivalent mutant.** `r-setup-end-applied-late` (`c.setupEnds[0].tick <= t` → `< t`) survives and is equivalent: `integral.set` adds `level × (t − since)` and moves `since` (`aggregate.go:45-48`), and an end is still applied at its own tick before any observation of a later tick, so the only reordering is between zero-length steps at that tick.
- **I3. Observation-only checkers and SimPy share the author's reading.** The batch checker re-implements the kernel's oldest-member/serial/generation scheme in the same shape (`kernel_check_batch_test.go:126-161` vs `kernel.go:1133-1180`), the setup checker transcribes D-055's unit choice (`kernel_check_setup_test.go:148-153`), and `scripts/simpy_batch_reference.py:347-380` has the same structure again; the checkers also accept any value for a non-constant changeover and time only constant services. They are good guards against implementation slips (they killed most of my kernel mutants), not against a misreading of D-054/D-055 — which is what M1 and M2 are. The report says this in its residual risks. The SimPy reference refuses finite buffers, calendars and shared resources; G-BT4, G-BT9 and G-BT12 were marked `"simpy": "compare"` at 88f34e3 and became `"refused"` when the script landed (ce01c55) — disclosed in the report's deviations and in T-037's scope.
- **I4. Hand goldens.** I re-derived G-BT16, G-BT17 and G-SU7 from their models and the decision text without running the engine; every expected value matches (event counts 13/16/28, BatchStart sequences 4/9/11, chamber blocked_ticks 7 min with the bundle counted once, unit identity under breakdowns, setup and scheduled-processing ticks). The committed history shows only additions (G-BT15, G-BT16, G-BT17) and no changed expectation (`golden-history.log`). That the expectations were written before the engine ran cannot be shown from git (goldens and kernel land in the same commits); the first-run logs are consistent with the claim, and a leftover helper in the shared scratchpad that inserted G-BT15/G-BT16 into `batch_goldens_gen.py` contains only literal values and no engine call.
- **I5. Verified as claimed.** The frozen des-go-0.4.0 copy is a faithful rename: re-running the author's `freeze040.go.txt` on 4584ba6's kernel.go, replication.go and events.go reproduces `frozen040_test.go` byte for byte (and 4584ba6..8948b70 does not touch those files). No new map iteration decides order (only `for id := range types`, then sorted). `internal/simulation/...` depends on no domain pack, HTTP, pgx, LLM SDK or frontend package (`simulation-deps.log`, `architecture-check.log`). D-096 and D-097 are `proposed` with `approved_by`/`approved_at` null in `planning/decisions.json` and PROPOSED in the register; nothing later changes them. The 0.5.0 locks hold 31 Results (23 golden + 8 reference) and 33 observation cases (25 + 8), as SIMULATION_SPEC says. The counts the report quotes (differential 20,213 compiled / 11,773,609 observations; checkers; neutral setups) match `engine-tests-verbose.log`. All 55 patterns of the author's harness still match exactly once at HEAD (`author-harness-patterns.log`); I did not re-run those 55.
- **I6. Process.** The report says the kernel was written before some engine tests and the engine "red" was reproduced with an overlay — a deviation from AGENTS.md §5's test-first order, disclosed. The report's commit table cites the `t050` branch hashes (7509c04 … b5467c0); in this branch they are 14b7000 … 4715f49 (same order; `git diff b5467c0 4715f49` over T-050's code differs only in comments carrying H-024's APPROVED words). `planning/tasks.json` keeps T-050 `not_started`, left to the coordinator. No migration, schema, OpenAPI or generated-type change (`PASS_TS_CONTRACTS_FRESH`).

## What I ran

Go 1.27.3 (`GOTOOLCHAIN=local`), Python `/home/user/Geminus/.venv/bin/python`, 4 CPUs shared with another reviewer (load average 10–13 during the mutant runs). Logs are in this directory; each starts with the command and ends with `# exit=… elapsed=…`.

| Command | Where | Exit | Time | Log |
|---|---|---:|---:|---|
| `make backend-test GOFLAGS=-count=1` (gofmt, vet, `GEMINUS_FULL_DIFFERENTIAL=1`, 74 packages ok; DB tests skip) | worktree | 0 | 394 s | `backend-test.log` |
| `make architecture-check` (86 packages, 23 rules, 16 build configurations) | worktree | 0 | 14 s | `architecture-check.log` |
| `go list -deps` of `./internal/simulation/...` (non-test and test) | worktree | 0 | — | `simulation-deps.log` |
| `make simulation-reference-test PYTHON=…` (T-047–T-049 reference, batch and changeover reference, calendar rules: `"differences": 0`) | worktree | 0 | 35 s | `simulation-reference-test.log` |
| `make spec-check PYTHON=…` (no tracked file changed) | worktree | 0 | 13 s | `spec-check.log` |
| `make spec-check PYTHON=…` again with this REVIEW.md present: only `checks/docs-check.json` changes (`markdown_files` 107 → 108), committed with this review | worktree | 0 | 11 s | `spec-check-after-review.log` |
| `make ai-eval` (fake provider only) | worktree | 0 | 3 s | `ai-eval.log` |
| `make integration-test` on a fresh `dt_test_t050rv1` (db-test, then db-go-test with `-race`, 35 packages ok) | worktree | 0 | 455 s | `integration-test.log` |
| `go test -race -count=1 -run 'Batch\|Setup\|Neutral\|Frozen040\|KernelRefuses\|PrimitiveGate\|CompileChecksSetups\|GoldenDigests050\|SimPyReferenceExport'` on engine, des, trace, results/metrics (T-050's goldens, checkers, the 0.4.0 differential (5,000 models under race), neutral setups, locks) | worktree | 0 | 363 s | `race-t050.log` |
| `go test -run '^$' -fuzz '^FuzzBatches$' -fuzztime 20000x -parallel 2 ./internal/simulation/engine/` | worktree | 0 | 47 s | `fuzz-batches-20000x.log` |
| `go test -run '^$' -fuzz '^FuzzSetups$' -fuzztime 20000x -parallel 2 ./internal/simulation/engine/` | worktree | 0 | 56 s | `fuzz-setups-20000x.log` |
| Re-run of `freeze040.go.txt` on 4584ba6's kernel.go, replication.go, events.go; `diff` with `frozen040_test.go`: identical | scratch | 0 | — | (stdout; see I5) |
| Golden fixture history across the T-050 commits | worktree (git show) | 0 | — | `golden-history.log` |
| Author's harness `t050-mutants.py --patterns` (55 patterns, none bad) | scratch | 0 | 0 s | `author-harness-patterns.log` |
| Reviewer mutants, batch A (6 kernel mutants) | scratch | 0 | 1284 s | `mutants-A.log` |
| Reviewer mutants, batch B (21: kernel, Result, validator, risk) | scratch | 0 | 1380 s | `mutants-B.log` (one BUILD-FAILED, rerun fixed: `mutants-E-risk-rerun.log`) |
| Reviewer mutants, batch C (stream-name mutant with locks, then 3 lock-first mutants with `WITHOUT_LOCKS=1`) | scratch | 0 | 1151 s | `mutants-C.log` |
| `WITHOUT_LOCKS=1` rerun of `r-setup-ends-not-applied-at-finish` | scratch | 0 | 21 s | `mutants-D-without-locks.log` |
| Probe P1 B2 since (fails by design: shows the M1 mismatch) | scratch | 1 | 6 s | `probe-b2-since.log` |
| Probe P2 B1 with a timeout after the horizon: kernel / option-B mutant | scratch (overlay) | 0 / 1 | 7 s / 5 s | `probe-b1-timeout-after-horizon.log`, `…-under-optionB-mutant.log` |
| Probe P3 fresh unit vs changeover | scratch (overlay) | 0 | 7 s | `probe-fresh-unit.log` |
| Probe P4 resource-free batch: kernel / mutant | scratch (overlay) | 0 / 1 | 7 s / 7 s | `probe-resource-free-batch.log`, `…-under-mutant.log` |
| Probe P5 changeover starting at W: kernel / mutant | scratch (overlay) | 0 / 1 | 2 s / 1 s | `probe-setup-at-w.log`, `…-under-mutant.log` |
| Probe P6 changeover ending after the last observation: kernel / mutant | scratch (overlay) | 0 / 1 | 2 s / 1 s | `probe-setup-end-after-last-observation.log`, `…-under-mutant.log` |

Reviewer mutant results (28 run; `review-mutants.py`):

| Mutant | Rule | Result |
|---|---|---|
| r-oldest-by-entry-tick | D-096 (c) oldest by entry serial | KILLED `TestBatchGoldens` |
| r-b1-ignores-timeouts-after-horizon | D-096 (g) option A vs B | **SURVIVED** (M2) |
| r-b1-since-oldest-entry | D-096 (g) B1 since | **SURVIVED** (L2) |
| r-batch-end-dispatch-without-resources | no dispatch without a release | **SURVIVED** (L1) |
| r-members-ignore-taken-places | D-054 later members see earlier places | KILLED `TestBatchGoldens` |
| r-timeout-never-uncounted | B1 timeout accounting | KILLED `TestRandomBatchFailuresAreReal` |
| r-batch-not-in-service | B1 in-service count | KILLED `TestBatchGoldens` |
| r-ready-above-max | D-054 readiness at max_size | KILLED `TestBatchGoldens` |
| r-setup-stream-by-process | D-055 stream key | KILLED by lock; without locks KILLED `TestSetupStreamsPerUnit` |
| r-setup-from-service-stream | D-055 own stream | KILLED by lock; without locks KILLED `TestSetupStreamsPerUnit` |
| r-setup-purpose-renamed | D-055 stream name | KILLED by lock; **SURVIVED without locks** (M3.1) |
| r-matching-unit-may-be-down | D-055 available unit | KILLED `TestRandomSetupModelsAgainstSpec` |
| r-matching-unit-greatest-index | D-055 least index | KILLED `TestRandomSetupModelsAgainstSpec` |
| r-setup-end-applied-late | collector end order | SURVIVED, equivalent (I2) |
| r-setup-ends-not-applied-at-finish | D-097 (f) ends after the last observation | KILLED by lock; **SURVIVED without locks** (M3.2) |
| r-setup-complete-strict-w | D-097 (f) W boundary | **SURVIVED** (M4) |
| r-batch-bundle-released-early | D-096 (j) bundle once in K | KILLED `TestBatchGoldenResults` |
| r-max-wait-unbounded | D-096 (i) | KILLED `TestBatchRules` |
| r-buffer-equal-to-max-refused | BATCH_INVALID boundary | KILLED `TestBatchRules` |
| r-min-equal-max-refused | BATCH_INVALID boundary | KILLED `TestBatchRules` |
| r-duplicate-pair-allowed | SETUP_INVALID | KILLED `TestSetupRules` |
| r-second-setup-resource-allowed | SETUP_INVALID P0 limit | KILLED `TestSetupRules` |
| r-batch-needing-setups-allowed | SETUP_INVALID P0 limit | KILLED `TestSetupRules` |
| r-unknown-initial-allowed | UNKNOWN_REFERENCE | KILLED `TestSetupRules` |
| r-risk-ignores-max-wait | D-057 risk | KILLED `TestBatchRisk` (after fixing a build failure) |
| r-risk-ignores-transition-distribution | D-057 risk | KILLED `TestLiftedPrimitivesHaveRiskCases` |
| r-risk-ignores-default | D-057 risk | KILLED `TestLiftedPrimitivesHaveRiskCases` |
| r-max-wait-not-a-value | expected-effects values | KILLED `TestBatchValuesInEffects` |

## NOT RUN

- `make backend-race` (full): NOT RUN, ≈15 min on a shared machine; instead the targeted `go test -race` of T-050's tests above. The author's and coordinator's full runs (exit 0) are in their evidence.
- `make backend-numeric`: NOT RUN; T-050 adds no sampler or numeric code (the setup stream reuses existing samplers). Author and coordinator ran it (exit 0).
- `make frontend-test`, `make e2e-test`: NOT RUN; T-050 changes no `apps/`, schema or OpenAPI file.
- The author's 55 mutants: NOT RUN again (patterns checked only); their `mutants-run3.log` reports 52 killed, 3 equivalent.
- Longer fuzzing (the author's 100,000 / 500,000 / 200,000 execs): NOT RUN; only 20,000 execs each of `FuzzBatches` and `FuzzSetups`. `FuzzKernel`, `FuzzRun`: NOT RUN.
- Live AI, `make ai-eval-live`: NOT RUN (not relevant to T-050; no key read).
- A batch or changeover model through the API / DB closed loop: NOT RUN by anyone (`make integration-test` has no such model), as the report says.
