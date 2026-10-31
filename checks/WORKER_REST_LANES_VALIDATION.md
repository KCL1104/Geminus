# WORKER_REST_LANES: several rest lanes per worker, validation

Follow-up to W1 ([H-056_W1_VALIDATION.md](H-056_W1_VALIDATION.md)) after the H-065 validation's P5 ([H-065_WONDER_CHOICE_VALIDATION.md](H-065_WONDER_CHOICE_VALIDATION.md) §5). Status: **PROPOSED**. Built and tested on branch `worktree-wf_04a8c56d-188-1` (base `1c205a8`). Not merged, not deployed, not independently reviewed. No live AI call was made, and no `.env` value or key was read. The coordinator records the decision in `docs/DECISIONS_AND_TBD.md`; this slice does not edit it.

## 1. Why, and why 2

**The finding (H-065, P5).** A Muse Wonder proposal can run for up to about 1,800 s: 900 s for the call and up to 900 s more for its H-057 repair. With W1 (`WORKER_LANES=split`), simulations have a lane of their own. Every other job type still shares the one rest lane. So while a Muse Wonder proposal runs, no other AI job of any organization runs: no Muse Spark proposal, no explanation, no import.

**The change (the coordinator's decision; routine engineering, no product change).** `WORKER_REST_LANES` runs that many rest lanes, each with the same handler set.

**Why 2, not more (the finding of a first run, which the coordinator passed on, checked here in the code).**
- The worker builds its AI clients once, and every rest lane shares them (`cmd/worker/main.go`):
  - `handlers()`: one `provider.Client` for proposals and analyses (`aiClient`);
  - `photoPath`: the photo path's judge and segmenter, each a client of its own.
  - All three take the same budget, `aiBudget(ai.Budget)`.
- Each client lets one organization have at most `MaxConcurrentPerOrganization` calls in flight in the process:
  - the default is 2 (`internal/ai/provider/budget.go:45`), and `AI_MAX_CONCURRENT_PER_ORGANIZATION` is not set in `deploy/railway/variables.json`;
  - the client refuses one more call at `acquire`, before the quote and the reservation, with `CodeConcurrencyLimited` (`internal/ai/provider/provider.go:385`; the segmenter's twin is in `segment.go`).
- That code is retryable for a job with attempts left. But a Muse Wonder proposal's job has one attempt (`proposals.SlowModelJobAttempts` = 1), and so does a research proposal's (`proposals.ResearchJobAttempts` = 1). For them the refusal is **final**: the person's proposal fails without a model ever being asked.
- A rest lane runs one job at a time, and a job makes its AI calls one after another. `grep` finds no `go func`, `errgroup` or `.Go(` in `internal/proposals`, `internal/analyses` or `internal/ai`, outside tests. So with N rest lanes, an organization has at most N calls in flight on each client.
- **With N ≤ the limit, no call of the worker's is ever refused for another call of its own.** With 3 or 4 rest lanes and the default limit, a third AI job of one organization would be refused at once.
- Hence 2, with every AI limit unchanged. The worker refuses to start when `WORKER_REST_LANES` exceeds the configured per-organization limit (§2). That limit is readable in the worker's config: `config.AI.Budget.MaxConcurrentPerOrganization`, 0 when unset, which `aiBudget` turns into the provider's default of 2.

**Why several loops claiming the same types are safe.**
- `jobs.Claim` selects with `FOR UPDATE SKIP LOCKED LIMIT 1` (`internal/storage/jobs/jobs.go:241`).
- Each lane holds its leases under its own `locked_by`, fenced by `lease_token`.
- So two rest lanes are two workers in ADR-024's sense, exactly as two processes would be (`TestWorkersShareTheQueue`, unchanged). Per-organization AI concurrency limits are unchanged.
- Review round 1 (§9) refines this: what stops two claims of one job is the row lock of `FOR UPDATE` (with PostgreSQL's recheck of `status='queued'`) and the running-row trigger behind it. `SKIP LOCKED` only spares a lane waiting on a row another lane is claiming.

## 2. What changed

- **`internal/config/worker.go`**:
  - `Worker.RestLanes` is read from `WORKER_REST_LANES`.
    - With `split`, unset is 1. Otherwise only exactly `1` to `4` (`config.MaxRestLanes`) is accepted: no sign, leading zero or space (`+2`, `02`, ` 2`, `2.0`, `0x2` and overflow are refused).
    - In single mode it is 0. Any value there, even `1`, is `WORKER_REST_LANES (invalid)`, with a message that names `WORKER_LANES=split`: never ignored.
    - The error never repeats the value.
  - `RestLaneSuffixOf(i)` names the rest lanes: `:rest` (W1's name of the one rest lane, unchanged), then `:rest2`, `:rest3`, `:rest4`.
  - The `WORKER_ID` bound under split follows the longest suffix (D-092, an actor reference is at most 128 characters): 123 characters with one rest lane, as W1, and 122 with 2 or more.
  - An unknown `WORKER_LANES` is reported alone, unless the count is also out of range. Every problem is still reported together.
- **`cmd/worker/lanes.go`**:
  - `plan(w config.Worker, handlers)` (it took the mode and the ID before) gives:
    - Single: the one loop under `WORKER_ID`, as before.
    - Split: `<id>:sim` with `{simulation}`, then `<id>:rest`, `<id>:rest2`, …, one per `RestLanes`. Each rest lane gets its own map of the same handler values: one AI client and one spend meter for all.
    - It refuses a count outside 1 to 4 that did not come from `LoadWorker`.
  - New `checkRestLanes(w, perOrganization)`: a `config.Error` on `WORKER_REST_LANES` when `RestLanes` exceeds the limit. The message names `AI_MAX_CONCURRENT_PER_ORGANIZATION`, the limit, and why.
  - `runLanes` is unchanged. It already ran any number of lanes:
    - a stop drains every lane;
    - a lane that panics or ends stops and drains all the others, and the worker exits 4 (`exitLane`).
- **`cmd/worker/main.go`**:
  - `run` calls `checkRestLanes(wcfg, aiBudget(ai.Budget).MaxConcurrentPerOrganization)` right after the configuration is loaded, before any connection. A refusal is exit 1, as for any other bad configuration.
  - `plan` takes `wcfg`. Each lane's `worker.Worker` is still built before the role check, and a `running` line is logged per lane.
- **H-061's agent lanes** (`WORKER_AGENT_LANES`, `<id>:agent<i>`) are not built. They would be further lanes appended by `plan`, with their own types and their own client (H-061 §5.1). `checkRestLanes` concerns only the rest lanes.
- **`deploy/railway/variables.json`**:
  - The worker gets `WORKER_REST_LANES` = `2`. Its `about` says why 2, and that it must not exceed `AI_MAX_CONCURRENT_PER_ORGANIZATION` (unset there: 2).
  - `WORKER_LANES`'s `about` now names the rest lanes.
  - The replica count is unchanged (1), and no AI limit is changed.
- **Docs**:
  - `docs/runbooks/deploy.md`: §4.3 has a `WORKER_REST_LANES` row, and the 122-character note on `WORKER_ID`; §4.4's budget row says that `AI_MAX_CONCURRENT_PER_ORGANIZATION` must be at least `WORKER_REST_LANES`.
  - `docs/adr/ADR-024-job-queue-and-worker.md`: a second sentence marked "PROPOSED addition, not yet reviewed", after W1's.
  - `.env.example`: the `WORKER_REST_LANES=` line.
- **Regenerated by `make spec-check`**: `checks/deploy-check.json` (the worker's variables, and `WORKER_REST_LANES` among the code's) and `checks/docs-check.json` (this report).

No migration, schema, OpenAPI or prompt change. Nothing changed in `internal/worker`, `internal/storage/jobs`, `internal/ai` or any handler.

## 3. Requirement and decision IDs

- **H-065** (Muse Wonder as a person's choice; its validation's §5 P5 and §7 risk 3) and **H-056 design §9 / W1** (worker lanes).
- **ADR-024 / T-019** (the job loop, the claim's `SKIP LOCKED`, the lease fence, the reaper) and **D-092** (actor references, the length bound).
- **H-049 / H-046** (one job attempt for a slow model's and for a research proposal), and **T-024** (`MaxConcurrentPerOrganization`, `CodeConcurrencyLimited`).
- **H-061** (`WORKER_AGENT_LANES`): its naming is kept open. Not built.

## 4. Tests (written first; red before the implementation)

**Red run** (the tests written, before `worker.go`, `lanes.go` and `main.go` changed): `go test -count=1 ./internal/config/ ./cmd/worker/` failed to build both packages:
- `w.RestLanes undefined`, `undefined: MaxRestLanes`, `undefined: RestLaneSuffixOf`;
- `not enough arguments in call to plan`, `unknown field RestLanes`.

That is the expected red.

### Without a database
- **`internal/config` `TestLoadWorkerRestLanes`**:
  - With split: unset, `1`, `2`, `3` and `4` give 1, 1, 2, 3 and 4 rest lanes. `0`, `5`, `16`, `-1`, `+2`, `02`, ` 2`, `2 `, `two`, `2.0`, `0x2` and `9223372036854775809` are `WORKER_REST_LANES:invalid`, and the value is never in the message.
  - With unset or `single`: 0 rest lanes. `1`, `2`, `4`, `0` and `two` are refused, naming `WORKER_LANES=split`.
  - `both` with `2` is `WORKER_LANES:invalid` alone; `both` with `9` reports both.
  - The ID bound: 122 characters pass with 2, 3 and 4 rest lanes. 123 characters with 4 rest lanes are `WORKER_ID:invalid`, naming `WORKER_REST_LANES`. 123 characters with 1 rest lane pass.
  - Every problem is reported together.
- **`TestRestLaneSuffixes`**: `:rest`, `:rest2`, `:rest3`, `:rest4`, `:sim`, and `MaxRestLanes` = 4.
- **`cmd/worker` `TestThePlanOfEveryLaneSetting`**: every combination of `WORKER_LANES` ∈ {unset, `single`, `split`} × `WORKER_REST_LANES` ∈ {unset, `1`…`4`, `0`, `5`}, read through `config.LoadWorker`, over the real build's 5 handlers:
  - single gives `[worker-0]` with every handler;
  - split gives `[worker-0:sim, worker-0:rest]`, `…, worker-0:rest2` and so on up to `:rest4`, in order;
  - the sim lane has only the simulation handler, and each rest lane has exactly the other four, the identical values;
  - every other combination is refused as `WORKER_REST_LANES (invalid)`.
- **`TestThePlanRefusesARestCountOutOfRange`**: split with -1, 0 or 5 rest lanes, not from `LoadWorker`.
- **`TestRestLanesFitTheAILimit`**: for every limit from 1 to `BudgetCaps` (16) and every count from 1 to 4, refused exactly when count > limit. The refusal is a `config.Error` on `WORKER_REST_LANES` that names `AI_MAX_CONCURRENT_PER_ORGANIZATION` and "at most <limit>". Single is never refused. The default limit is 2.
- **`TestRunRefusesALaneSettingItCannotUse`**: W1's 4 cases, plus 6 new ones. Each exits 1, names the variable and connects to nothing. The new cases:
  - a rest count without split;
  - 5 rest lanes;
  - 123 characters with 2 rest lanes;
  - 3 rest lanes over the default limit;
  - 2 over a limit of 1;
  - 4 over a limit of 3.
- **`TestRestLanesWithinTheAILimitStart`**: each of these builds every lane's loop, and a signal before the start is exit 0:
  - 2 rest lanes at the default limit;
  - 3 at a limit of 3;
  - 4 at 16;
  - 1 at a limit of 1;
  - single at a limit of 1.
- **`TestAStopDrainsEveryRestLane`**: 5 lanes (sim and four rest). `runLanes` returns only after each has finished draining, and nothing is logged as a failure.
- **`TestARestLanesPanicStopsEveryOtherLane`**: `worker-0:rest2` panics while sim, rest and rest3 run. All three are drained, the one report names `worker-0:rest2` (`WORKER_LANE_PANICKED`), and the panic's value is not logged.

### DB-backed (`cmd/worker/lanes_db_test.go`)
These run through `run`, as the plain worker login. Runs are created through `internal/runs` as the plain API login.

**`TestTwoRestLanesRunTwoLongJobsAtOnce`** (`WORKER_LANES=split`, `WORKER_REST_LANES=2`):
1. Two long import jobs are queued before the start. Both are claimed at once, one held by `worker-0:rest` and the other by `worker-0:rest2`. Both are `running`, attempt 1, in the database.
2. A third job, a simulation, is then claimed by `worker-0:sim` and runs to its end (`failed`, attempt 1, `TEST_SIMULATION_ENDED`). Both long jobs are still running.
3. A further import job stays `queued` with attempt 0 for more than two idle passes, while both rest lanes are busy.
4. One long job is released (`failed`, `TEST_LONG_ENDED`). The waiting job is then claimed by the lane that held it, and ends (`TEST_ENDED`), while the other long job still runs.
5. A third long job is claimed by that same freed lane.
6. At the stop, the worker exits 0. Both in-flight long jobs are `queued`, attempt 1, `WORKER_STOPPED`, with no holder. Each of the 5 jobs was claimed exactly once. (Review round 1, §9: no two lanes compete for one job in this test, so this count could not fail on a claim race. Since round 1 the contention is tested by `TestRestLanesClaimEachQueuedJobOnce`.)
7. The `running` lines name `worker-0:sim`, `worker-0:rest` and `worker-0:rest2`, in order. No `WORKER_LANE_` report appears.

W1's 3 lane DB tests and `TestRunWorksAndStops` pass unchanged. `getenv` now takes extra variables, and `profile` gained `profileAs` (a job ID chosen by the test). No assertion was weakened, and no skip was added.

### Mutation check
`wrl/mutants.py` in the session scratchpad makes one change per mutant, runs the named tests against `dt_test_wrl1` with the DB tests required, and restores the file byte for byte. The three files' SHA-256 matched before and after.

The baseline passed first. **12 of 12 killed**, 0 survived, 0 invalid:

| Mutant | Killed by |
|---|---|
| M1: every split has one rest lane | `TestThePlanOfEveryLaneSetting`, `TestTwoRestLanesRunTwoLongJobsAtOnce` |
| M2: every rest lane named `:rest` | `TestThePlanOfEveryLaneSetting`, `TestTwoRestLanesRunTwoLongJobsAtOnce` |
| M3: no refusal over the AI limit | `TestRestLanesFitTheAILimit`, `TestRunRefusesALaneSettingItCannotUse` |
| M4: a refusal at the limit itself (`<` for `<=`) | `TestRestLanesFitTheAILimit`, `TestRestLanesWithinTheAILimitStart`, `TestTwoRestLanesRunTwoLongJobsAtOnce` |
| M5: `run` never checks the limit | `TestRunRefusesALaneSettingItCannotUse` |
| M6: `run` ignores a configured limit (always 2) | `TestRestLanesWithinTheAILimitStart`, `TestRunRefusesALaneSettingItCannotUse` |
| M7: the rest lanes also claim simulations | `TestTheLanesSplitTheBuildsHandlers`, `TestThePlanOfEveryLaneSetting` |
| M8: single accepts a rest count | `TestLoadWorkerRestLanes`, `TestRunRefusesALaneSettingItCannotUse`, `TestThePlanOfEveryLaneSetting` |
| M9: up to 16 rest lanes | `TestLoadWorkerRestLanes` |
| M10: the ID bound ignores `:rest2` | `TestLoadWorkerRestLanes`, `TestRunRefusesALaneSettingItCannotUse` |
| M11: a loose count (`+2`, `02`) | `TestLoadWorkerRestLanes` |
| M12: split defaults to 2 rest lanes | `TestLoadWorkerLanes`, `TestLoadWorkerRestLanes`, `TestRestLanesWithinTheAILimitStart`, `TestSplitLanesRunSimulationsBesideOtherJobs`, `TestThePlanOfEveryLaneSetting` |

W1's ten mutants of `runLanes` (no stop of the other lanes, no wait, exit 0 after a failure, …) were not run again. `runLanes` is unchanged, and the two new runLanes tests cover its N-lane behaviour.

## 5. Commands and real results

Toolchain: Go 1.27.3 (`GOTOOLCHAIN=local`) and the borrowed `.venv` (a symlink, not committed). The only matching variable in the session was `AI_AGENT`; it is read by no code and no test, and the mutation script removed every `AI_*`, `GEMINUS_*`, `ANTHROPIC_*`, `OTEL_*` and `WORKER_*` variable, by name only.

The DB tests ran on a throwaway PostgreSQL 16.15 cluster that this slice started in the session scratchpad:
- `wrl/pgdata`: trust auth, TCP 127.0.0.1:55613 only, no Unix socket, `fsync=off`;
- on `dt_test_wrl1`, created for this slice.

Other agents' clusters (127.0.0.1:55487 and :55497) were left alone.

| Command | Exit | Result |
|---|---|---|
| `initdb -A trust` and `pg_ctl start` (port 55613, `listen_addresses=127.0.0.1`, no socket), `CREATE DATABASE dt_test_wrl1` | 0 | PostgreSQL 16.15 up, the database created |
| `make db-test` (`DT_ALLOW_DB_TESTS=yes`, `DATABASE_URL` = `dt_test_wrl1`) | 0 | "DB migration smoke up/down/up completed" |
| `go test -count=1 ./internal/config/ ./cmd/worker/` (red, before the implementation) | 1 | both packages fail to build on the undefined API (§4): the expected red |
| `gofmt -l cmd internal`, then `gofmt -w cmd/worker/lanes_db_test.go` | 0 | the one file it listed was formatted; clean afterwards |
| `go vet ./cmd/worker/ ./internal/config/` | 0 | clean |
| `go test -count=1 -v ./internal/config/ ./cmd/worker/` (no database) | 0 | 2 packages ok; 64 PASS, among them every new unit test of §4; the 5 DB tests SKIP without a database (run below) |
| `go test -count=1 -race -v ./cmd/worker/` (`GEMINUS_TEST_DATABASE_URL` = `dt_test_wrl1`, `GEMINUS_REQUIRE_DB_TESTS=1`) | 0 | 41 PASS, 0 SKIP, 0 FAIL: `TestTwoRestLanesRunTwoLongJobsAtOnce` (4.70 s), `TestSplitLanesRunSimulationsBesideOtherJobs` (3.54 s), `TestALanesPanicStopsTheWorker`, `TestASingleLoopHoldsLeasesUnderItsID`, `TestRunWorksAndStops` and every unit test |
| `python3 wrl/mutants.py <dt_test_wrl1>` (DB tests required), then `sha256sum -c` of the three mutated files | 0 | baseline PASS; 12/12 KILLED (§4); `lanes.go`, `main.go`, `worker.go`: OK |
| `go test -p 1 -count=1 -race ./internal/config/ ./internal/worker/ ./cmd/worker/` (`dt_test_wrl1`, DB tests required) | 0 | `internal/config` ok (1.3 s), `internal/worker` ok (42.7 s), `cmd/worker` ok (10.3 s) |
| `make architecture-check` | 0 | "PASS architecture: 103 packages, 40 rules, 16 build configurations" |
| `PATH=.venv/bin:$PATH make spec-check` (final) | 0 | see below |
| `dropdb dt_test_wrl1`, `pg_ctl stop` | 0 | dropped, no `dt_test_*` database left on the cluster, the cluster stopped |

`make spec-check` (final) passed every part:
- contracts `PASS_LIMITED_SPEC_CHECKS`;
- planning PASS;
- docs PASS (173 Markdown files, 334 relative links);
- the SQL inventory, reference math and engine vectors;
- decisions `PASS_REGISTER_SHAPE_ONLY`;
- TS contracts fresh, independent hashes, DB test render fresh;
- deploy `PASS_STATIC_DEPLOY_CHECKS`: the worker has 27 variables and the runbook names 70, and `WORKER_REST_LANES` is among the code's variables.

It also rewrote `checks/contracts-check.json` (positive fixtures 86 → 87, negative 164 → 165, exact error paths 159 → 160). Those fixtures were already on the base `1c205a8`, and this slice touches no schema or fixture. The file was restored, and is left out of this commit for the coordinator's merged-tree run.

## 6. NOT RUN

- `make backend-test`, `make backend-race`, `make integration-test` (the full DB suite), `make frontend-test`, `make e2e-test`, the browser suite and `make ai-eval`. The test policy for this slice: the coordinator runs the full suites on the merged tree, and CI runs them again. Only the touched packages ran here (§5).
- `make build-containers`, `make staging-smoke`, `make db-restore-test`, `make simulation-reference-test`, `make backend-numeric`.
- Any deployment. Neither `deploy/railway/deploy.sh` nor anything on Railway was run, so `WORKER_REST_LANES=2` is in `variables.json` only. Also not exercised: Railway's restart on exit 4, and the drain of three lanes inside the 30 s `drainingSeconds`.
- Any live AI call. None was needed: two real AI jobs of one organization in flight at once were not run against a provider.
- Memory, CPU or pool measurement with three lanes in one process.
- An independent review of this slice.
- The full official OpenAPI validator (unavailable, as before; there is no OpenAPI change).

## 7. PROPOSED (for review, the coordinator or the owner)

- The whole slice is PROPOSED, including the ADR-024 addition and `WORKER_REST_LANES=2` in `variables.json`. The variable takes effect only at the next `deploy/railway/deploy.sh` run, with the owner's authorization.
- **Two docs now describe the old single-loop layout.** Both say that while a Muse Wonder proposal runs, the worker runs nothing else, and that `MaxConcurrentPerOrganization` "never comes into play":
  - `docs/AI_SPEC.md`'s H-065 **Job** item (line 76);
  - the H-065 report's §5 P5 and §7 risk 3.

  With `WORKER_LANES=split` and `WORKER_REST_LANES=2` deployed, both sentences become partly wrong:
  - simulations keep their lane, and other AI and import jobs keep the second rest lane;
  - two Muse Wonder proposals at once still fill both rest lanes;
  - the limit is reached exactly, but never exceeded, by the worker's own lanes.

  They are not edited here, because the task limited this slice's doc changes to where W1 documented `WORKER_LANES`. For the coordinator to update with the decision record (AGENTS.md §1).
- A deployment that wants 3 or 4 rest lanes must also raise `AI_MAX_CONCURRENT_PER_ORGANIZATION` to at least that number. That changes an AI limit, which this slice does not do.
- The rest lanes' audit actors: as in W1, the loop's own events (reaps, `job.attempt_failed`) carry the lane's name (`…:rest2`). The handlers' events keep `WORKER_ID`.

## 8. Risks

- **Spend reservations at once.**
  - Two Muse Wonder proposals of one organization can now be in flight together. Each is reserved at its worst case before it runs: about USD 6.91 for research, about USD 7.9 for a 20,000-token edit with its retries (H-050 as amended by H-065).
  - So up to about USD 16 of the USD 40 daily caps can be held at once, where one lane held about half that. A later call can be refused for the cap earlier in the day while both reservations are open; it gets the ledger's typed code, never a silent failure.
  - The ledger's transactions were already safe across processes. This was not measured live.
- **The limit check sees this process only.**
  - `MaxConcurrentPerOrganization` is an in-process count, so `checkRestLanes` guards the worker's own lanes.
  - A second worker process (H-065 P5's option (c)), or H-061's agent lanes, would have their own counts. H-061 gives the investigator its own client (§5.1), so its lanes take no rest-lane slot.
- **Database pool.**
  - Three lanes share one pgx pool of the default size, `max(4, CPUs)`. At peak there are about seven short transactions at once: three heartbeats, three handler transactions and the meter's.
  - Transactions are short (ADR-024), so the expected effect is brief waits. A heartbeat delayed beyond the 30 s lease would let the reaper take the job back (the fence stops the old holder). That needs a wait of about 20 s, which is not expected and not measured. The pool size is unchanged.
- **Memory and CPU.**
  - Two AI jobs can now build prompts and decode answers at once beside a simulation. Photo proposals decode images, and Muse Wonder answers can reach 128,000 tokens.
  - No GOMEMLIMIT or container limit is configured (W1 risk 1, still open), and nothing was measured.
- **Two Muse Wonder proposals still fill both rest lanes**, and each further one in the queue waits up to about 1,800 s. Only the second lane is new. The claim cannot tell models apart (H-065 P5 (b)).
- **A lane failure stops all three lanes** (exit 4), and each in-flight attempt is recorded as `WORKER_STOPPED`. That ends a Muse Wonder or research proposal in flight, because its job has one attempt: this was already true at any stop or redeploy (H-065 §7 risk 2). The drain runs in parallel, so Railway's 30 s grace covers the slowest lane's stop, not the sum.

## 9. Review round 1

Two Low findings, both fixed. Same branch, base `1c205a8`, on top of `6d7a15b`. Status still **PROPOSED**: not merged, not deployed. No live AI call was made, and no `.env` value or key was read.

### L1: the DB test did not show that no job is claimed twice (fixed: a contending test)

**The finding.** In `TestTwoRestLanesRunTwoLongJobsAtOnce`, no two lanes ever compete for one queued job: each job it counts is already `running` when another lane polls. The reviewer removed `FOR UPDATE SKIP LOCKED` from `jobs.Claim` in an `-overlay` copy, and the test still passed 5 of 5. So §1, §4 step 6 and the test's comment overstated what it shows. The finding was confirmed here before the fix: that test passes on that mutant (5 of 5 below).

**The fix (tests only, no production change):**
- **New `TestRestLanesClaimEachQueuedJobOnce`** (`cmd/worker/lanes_db_test.go`), through `run` as the plain worker login:
  - `WORKER_LANES=split`, `WORKER_REST_LANES=4` at `AI_MAX_CONCURRENT_PER_ORGANIZATION=4`;
  - 24 short import jobs are queued before the start, so the four rest lanes' claims compete for the same oldest job from the first pass on;
  - each lane's first job is held until every rest lane holds one. So all four claim from the queue at once, and each appears in `locked_by`. After that, the jobs end at once, and the four lanes go back to the queue together.
  - It checks:
    - every job ends `failed`, attempt 1, `TEST_ENDED`, with no holder, and was claimed exactly once (24 claims, no other);
    - the jobs were claimed by exactly `worker-0:rest`, `:rest2`, `:rest3` and `:rest4`, and the sim lane took none;
    - stderr has no `claiming failed`, `reaping failed`, lost lease, unrecorded end, failed renewal or `WORKER_LANE_` line;
    - the `running` lines name the sim lane and the four rest lanes, in order, and the worker exits 0 at the stop.
- **`TestTwoRestLanesRunTwoLongJobsAtOnce`**:
  - Its comment now says what it shows. It also says that two lanes never taking one job is shown by the new test and by `jobs.TestConcurrentClaims`. Its count of claims stays, unweakened, as a check of the handlers' bookkeeping.
  - It now holds both rest lanes' jobs past one heartbeat (10 s), as the reviewer suggested as an option. Before the stop it waits until both held leases (`long2` in one lane, `long3` in the other) have been renewed (`heartbeat_at` after `locked_at`). Then both must still be `running`, attempt 1, each held by its own lane. So two lanes of one process renew their leases under their own IDs at the same time. The test now takes about 14.7 s, up from 4.7 s.

**Mutants** (each an `-overlay` copy in the session scratchpad, `wrl2/mut*`; the worktree was never touched, and `sha256sum -c` of the mutated file was OK after each):

| Mutant | Tests run | Result |
|---|---|---|
| R1: the reviewer's: `FOR UPDATE SKIP LOCKED` removed from `jobs.Claim` (`LIMIT 1` alone) | the new test and `TestTwoRestLanesRunTwoLongJobsAtOnce`, 5 runs | **killed 5 of 5**, by the new test. The trigger refuses the second claim of a running job ("immutable record ... the worker only renews, ends or retries a running job, under its lease"), and the lane logs `claiming failed`. `TestTwoRestLanesRunTwoLongJobsAtOnce` passed 5 of 5, as the reviewer found. `jobs.TestConcurrentClaims` fails on this mutant with the same trigger error, also as the reviewer found. |
| R2: only `SKIP LOCKED` removed (`FOR UPDATE LIMIT 1`) | the same two tests, 3 runs | survived 3 of 3. **Not a defect, and no test should kill it.** Under READ COMMITTED, PostgreSQL rechecks a row it waited to lock against the claim's `WHERE` (`status='queued'`) and skips it once the other claim has committed, so no job is taken twice. The claims only wait for each other. `jobs.TestConcurrentClaims` also passed 20 of 20 on this mutant. |
| R3: the loop's renewal does not write (`jobs.Heartbeat` replaced by `return nil` in `internal/worker`) | `TestTwoRestLanesRunTwoLongJobsAtOnce` | **killed**: "waited 20 s for both held leases are renewed" |

R2 corrects one sentence of round 0. §1 credited `SKIP LOCKED` with "several loops claiming the same types are safe". In fact `SKIP LOCKED` is about throughput: a lane does not wait on a row another lane is claiming. The no-double-claim guarantee is the row lock of `FOR UPDATE` with that recheck, and the running-row trigger behind it. §1 now says so. The other docs (`lanes.go`, the runbook, ADR-024 and `variables.json`) name `FOR UPDATE SKIP LOCKED` together with the lease fence, which is still accurate, so they are unchanged.

The new test also passed 20 of 20 in a row (`-count=20 -race`, 8.7 s), so the barrier does not make it flaky.

### L2: the WORKER_LANES docs still described two loops (fixed)

The `WORKER_LANES` wording now matches `WORKER_REST_LANES` and the deployed `variables.json` (split plus 2 rest lanes, so three loops):
- **`docs/runbooks/deploy.md` §4.3, the `WORKER_LANES` row**: split runs "a sim loop ... and `WORKER_REST_LANES` rest loops (1 by default; see the next row)" under `<WORKER_ID>:rest`, `<WORKER_ID>:rest2`, ...; "a stop drains every lane"; "the other lanes are stopped and drained".
- **`internal/config/worker.go`**: the `Lanes` type's doc says one loop for every type, or a sim loop and `WORKER_REST_LANES` loops for the other types.
- **`.env.example`**: the `WORKER_LANES` comment names the `WORKER_REST_LANES` rest loops, `<WORKER_ID>:rest`, `:rest2`, ...

No other place W1 documented `WORKER_LANES` still says two lanes. `grep` finds "both lanes", "the other lane" and "two loops" nowhere in the runbook, `internal/config`, `cmd/worker` or `.env.example`. The one hit is `variables.json`'s `WORKER_REST_LANES` text, where "the other lane" is right for 2 rest lanes. W1's own sentence in ADR-024 ("runs two such loops") is kept as W1's record, and the next sentence adds the rest lanes.

### Commands and real results (round 1)

Toolchain: Go 1.27.3 (`GOTOOLCHAIN=local`) and the borrowed `.venv` (a symlink, not committed). The test wrapper (`wrl2/gt.sh` in the scratchpad) removes every `AI_*`, `GEMINUS_*` (but the test database's), `ANTHROPIC_*`, `MODEL_API_KEY`, `OTEL_*` and `WORKER_*` variable of the session, by name only.

The DB tests ran on a new throwaway PostgreSQL 16.15 cluster in the session scratchpad:
- `wrl2/pgdata`: trust auth, TCP 127.0.0.1:55614 only, no Unix socket, `fsync=off`;
- on `dt_test_wrl2`, created fresh for this round.

The other agents' cluster (127.0.0.1:55491) was left alone.

| Command | Exit | Result |
|---|---|---|
| `initdb -A trust`, `pg_ctl start` (port 55614), `CREATE DATABASE dt_test_wrl2` | 0 | PostgreSQL 16.15 up, the database created |
| `make db-test` (`DT_ALLOW_DB_TESTS=yes`, `DATABASE_URL` = `dt_test_wrl2`) | 0 | "DB migration smoke up/down/up completed" |
| `go test -count=1 -race -run 'TestTwoRestLanesRunTwoLongJobsAtOnce\|TestRestLanesClaimEachQueuedJobOnce' -v ./cmd/worker/` (DB tests required), on the real code | 0 | both PASS (14.71 s, 0.50 s) |
| the same, with `-overlay` R2, 3 runs | 0 | both PASS each time: R2 survives (see above) |
| the same, with `-overlay` R1, 5 runs | 1 | `TestRestLanesClaimEachQueuedJobOnce` FAIL 5 of 5 (`claiming failed`, the trigger's error); `TestTwoRestLanesRunTwoLongJobsAtOnce` PASS 5 of 5 |
| `go test -race -count=20 -run 'TestConcurrentClaims$' ./internal/storage/jobs/` with R2 | 0 | ok: 20 of 20 pass |
| `go test -race -count=1 -run 'TestConcurrentClaims$' -v ./internal/storage/jobs/` with R1 | 1 | FAIL: the trigger's "immutable record" error |
| `go test -count=1 -race -run TestTwoRestLanesRunTwoLongJobsAtOnce -v ./cmd/worker/` with `-overlay` R3 | 1 | FAIL (24.7 s): "waited 20 s for both held leases are renewed" |
| `sha256sum -c` of `jobs.go` and `worker.go` after the mutants | 0 | OK |
| `gofmt -l cmd internal`; `go vet ./cmd/worker/ ./internal/config/ ./internal/worker/` | 0 | clean |
| `go test -p 1 -count=1 -race -v ./internal/config/ ./internal/worker/ ./cmd/worker/` (`dt_test_wrl2`, DB tests required) | 0 | `internal/config` ok (1.3 s, 28 tests), `internal/worker` ok (41.6 s, 36 tests), `cmd/worker` ok (20.9 s, 42 tests, among them all 6 lane DB tests and `TestRunWorksAndStops`); 0 SKIP, 0 FAIL |
| `go test -count=20 -race -run TestRestLanesClaimEachQueuedJobOnce ./cmd/worker/` | 0 | ok (8.7 s): 20 of 20 |
| `make architecture-check` | 0 | "PASS architecture: 103 packages, 40 rules, 16 build configurations" |
| `PATH=.venv/bin:$PATH make spec-check` | 0 | every part passed: contracts `PASS_LIMITED_SPEC_CHECKS`, planning PASS, docs PASS (173 Markdown files, 334 relative links), the SQL inventory, reference math and engine vectors, decisions `PASS_REGISTER_SHAPE_ONLY`, TS contracts fresh, independent hashes, DB test render fresh, deploy `PASS_STATIC_DEPLOY_CHECKS` (with `WORKER_REST_LANES` among the code's variables). As in round 0, it rewrote `checks/contracts-check.json` for the base's H-065 fixtures (86 → 87, 164 → 165, 159 → 160); the file was restored and is left out of this commit |
| `DROP DATABASE dt_test_wrl2`, `pg_ctl stop` | 0 | dropped, no `dt_test_*` database left on the cluster, the cluster stopped |

### NOT RUN (round 1)

- Everything §6 lists as NOT RUN is still NOT RUN: `make backend-test`, `make backend-race`, `make integration-test` (the full DB suite), `make frontend-test`, `make e2e-test`, the browser suite, `make ai-eval`, any deployment and any live AI call. This is the slice's test policy: the coordinator runs the full suites on the merged tree, and CI runs them again.
- `jobs.TestConcurrentClaims` and the rest of `internal/storage/jobs` were not run on the unmutated code in this round, because the slice does not touch that package. They ran only on the mutants above.
- R1 with the running-row trigger dropped, which would show a double claim itself rather than the trigger's refusal, was not run. The new test would fail either way, because it checks both the claim count and the absence of `claiming failed` (reasoned, not run).
