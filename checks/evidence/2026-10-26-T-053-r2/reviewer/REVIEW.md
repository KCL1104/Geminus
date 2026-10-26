# T-053 independent review, round 2 (H-003)

Reviewer: independent agent (did not write T-053 or its round-1 fixes). Date: 2026-10-26. Tree: `d150918`
(round-1 fixes `1ad02c4..d150918` on top of `245a375`; reviewed as `git diff 245a375 d150918`). All logs and
probes are in this directory; every log ends with its exit code. Probes are saved as `*_test.go.txt`; they ran
in `git archive d150918` copies outside the worktree and were never in the worktree's packages.

## Verdict: ACCEPT

No High or Medium finding. Every round-1 finding is fixed as the coordinator decided.

- **Within one checkout**, nothing gets around the run's cap or the record's day and month sums except operator
  action: editing the file, `-dir`, or raising `AI_DAILY_LIMIT_USD`.
- **Across checkouts, or after a revert of tracked files**, the record restarts at zero (L-new-2). Each run then
  still stays within its own cap, which is at most the day limit.

Four new Low findings and seven Info notes follow. None of them blocks the pre-authorized live smoke and live
eval (H-025), but L-new-4 and the operational preconditions in the last section should be settled before the
first live call.

Round-1 summary:

| Finding | Status | Evidence |
|---|---|---|
| H1 eval and smoke outside the day and month limits | FIXED | P3 PASS; Q1a PASS (two runs in one checkout); author's record tests; mutants K1–K15 killed; my N1, N2, N4–N6 killed, N3, N7, N8 survive (test gaps, I-new-6) |
| M1 no test of the ledger's organization filter | FIXED | P1 PASS (author's copy changes only the meter's construction, checked by diff); `TestOrganizationFilter` PASS on a fresh DB; D9, F1, F2, F4, F5 killed |
| M2 price assumes global inference | FIXED as decided (not pinned; reserve at 1.1x; settle by the reported geo) | P7 FAILS only because the decision is not to pin; reservation at 1.1x shown by P4 (`0.206492` = ⌈1.1 × 0.18772⌉ per attempt); official pages re-read today (VERIFIED) |
| M3 failover after attempts that may have been billed | FIXED | P4 PASS (A=3, B=0, 2 unanswered settled); Q2c, Q2d, Q2e PASS; R1, R5 killed |
| L1 settlement above the reservation without a signal | FIXED as decided (recorded in full, flagged) | P2 FAILS only because the decision is to record, not refuse; `TestASettlementAboveItsReservationIsRecordedAndFlagged` PASS (event flag, metric, refusal of the next reservation); Q1e for the eval |
| L2 keys compared as raw strings | FIXED | P6 PASS; W1–W3 killed |
| L3 the worker's clock picks the day | FIXED | `TestTheDatabaseClockPicksTheDay` PASS in UTC, UTC+14, UTC−12; C1, C2 killed |
| L4 `refusals.sh` could reach the API | FIXED | the script prints a notice and exits 2 before any step |
| L5 stale statements | FIXED | VALIDATION rows 2a, 3 and the NOT RUN row; `live_test.go` header; CB-36; SECURITY §7 first bullet all now describe H-026's state |
| L6 no spend record after a failed run | FIXED | `TestEveryEndClosesTheRecord` (cost on stdout, close line `error`); K10, K11 killed |
| I1 undecodable 200 settles at 0 | FIXED | P5 PASS (settles at the reservation); R2–R4 killed |
| I2, I4, I5 | Documented, deferred to T-039 (D-111, SECURITY §7) | acceptable: no live worker is deployed |
| I3 | Equivalent mutant, agreed | F3 EQUIVALENT again |
| I6 | Unchanged (Info) | absurd usage only |

## The round-1 probes, re-run

`probes-p3-p4-p5-p6-p7.log` (exit 1) and `probes-p1-p2.log` (exit 1), on `d150918`:

| Probe | Result | Why |
|---|---|---|
| P1 superuser organization filter | PASS | the author's adapted copy (`../../2026-10-26-T-053-r1/probe-p1-p2-adapted_test.go.txt`) differs from mine only in the meter's construction (`PinDay` instead of the removed `Config.Now`) and a helper's name; the assertions are unchanged |
| P2 settlement above the reservation is refused | FAIL | the only reason is the L1 decision: the settlement is recorded (organization day 5000 against a limit of 1000) and flagged; the flag, the metric and the next refusal are tested by `TestASettlementAboveItsReservationIsRecordedAndFlagged` |
| P3 run cap above the day limit | PASS (3/3) | refused before the backend is built |
| P4 failover after unanswered attempts | PASS | A=3, B=0, `unavailable`, 2 unanswered attempts settled at 0.412984 USD of a 0.619476 reservation |
| P5 undecodable 200 | PASS | settles at the whole reservation |
| P6 the same key with white space | PASS | |
| P7 the request pins `inference_geo: global` | FAIL | the only reason is the M2 decision: the request names no geo (`TestTheRequestNamesNoInferenceGeo`), the reservation assumes 1.1x |

## New attack surface (probes Q1, Q2)

`probes-q1-q2.log` (exit 1: Q1b, Q1c and Q2a fail by design of the probe, see below).

### The spend record (`internal/aievals/record.go`, `main.go`)

| Case | Result | Realistic in this repository's workflow? |
|---|---|---|
| Missing record | refused (`TestAnUnreadableRecordRefuses` "missing") | — |
| Empty record | a record of no runs: the full day and month (the committed initial state) | see L-new-2 |
| Truncated or reverted record (`git checkout`/`stash`/`reset` of the tracked file before the commit) while a live report is still next to it | **accepted as no runs** (Q1c FAIL) | **yes**: the reports are new untracked files, the record is tracked, so a revert of the tracked files keeps the reports and empties the record (L-new-2) |
| Record of another branch, worktree or fresh clone | each sees only what is committed and merged there | **yes**: agents here work in worktrees and often do not push (L-new-2) |
| Two branches that both appended | a git conflict at the end of the file; resolving by one side drops runs | yes, at the next merge (L-new-2) |
| Last line without its newline, an empty line, not JSON, unknown members, duplicate members, a BOM, string, exponent, fractional, `null` or above-int64 amounts, negative settled, committed below settled, zero cap, unknown kind or result, bad run ID, a report of another run, a day or month not of its time, a close before its open, a close without its open, two opens or two closes | refused (author's 19 cases; my Q1d adds duplicate member, BOM, exponent, fraction, `null`, > int64) | crafted only |
| Huge caps or amounts (MaxInt64, two runs of 9e18) | the sums saturate, so every run is refused (Q1d) | crafted only |
| A line dated in another day (a clock that was ahead) | counted only on its own day (Q1d, logged) | operator or environment |
| A rewritten line with a smaller amount | accepted: the record is not authenticated | operator only: whoever can rewrite the file can change the code |
| Two runs at once in one checkout | the second sees the first's open line at its whole cap and is refused (Q1a PASS: "holds USD 6.00 of the daily limit") | yes, handled |
| A run opened yesterday without a close line, still running across midnight | **not counted today**: a second run may take the whole new day (Q1b FAIL) | only with two runs across 00:00 UTC (L-new-1) |
| A crash between taking the lock and writing the open line | the lock stays and every run is refused until it is removed (fail closed; the message says how). SIGINT/SIGTERM do not kill the process (`signal.NotifyContext`), so only SIGKILL or a crash leaves it | rare |
| `-dir` pointing at another empty record | accepted (I-new-2) | operator only (`make` passes `-root` only) |
| `AI_DAILY_LIMIT_USD=100` and `AI_EVAL_LIVE_MAX_USD=100` | one run may take the month's USD 100 in one day (Q1f, logged) | operator only (I-new-1) |
| A call above its reservation in the eval | recorded in full: the close line's committed 4.006 USD exceeds the USD 2 cap, the report counts `over_reservation: 1`, stderr warns, the next run counts 4.006 (Q1e PASS) | as decided (M3, L1) |

### Inference geo (`internal/ai/spend/spend.go:107-165`)

- `tenthsOf` is ⌈n·tenths/10⌉ computed as `11·⌊n/10⌋ + ⌈11·(n mod 10)/10⌉` with saturating multiply and add: exact,
  never rounds down, no overflow before saturation. Every per-token price × 1.1 is an exact integer (4400, 22000,
  220, 5500, 8800 nano-USD), so per-call rounding adds at most 1 nano-USD.
- The reservation prices every attempt at 1.1x (P4; `TestTheReservationAssumesTheDearestGeo`); unanswered attempts
  settle at that bound; an answer at its reported geo; a missing, empty, unknown (`eu`) or differently cased
  (`GLOBAL`) geo at 1.1x with a warning (`TestTheAnswerIsPricedAtItsInferenceGeo`). The provider's text never leaves
  `spend.Geo`: the ledger refuses any other word, the audit catalogue accepts only `global`, `us`, `unknown`, the
  log key is a token.
- Source: VERIFIED today (`official-docs-excerpts.md`): the data residency page lists only `global` and `us`,
  1.1x "across all token pricing categories" for Claude 4.6 and later, `default_inference_geo` applies when the
  request omits the geo, and `usage.inference_geo` reports where inference ran. The pricing page's Opus 5.5 row
  is still $4 / $5 / $8 / $0.20 / $20 per MTok, with no long-context premium; fast mode is a request opt-in the
  adapter does not use.

### Failover and attempts (`internal/ai/provider/anthropic/anthropic.go:284-317`, `:356-423`)

| Case | Counted as may have been billed | Fails over on 401/402/403 after it | Probe |
|---|---|---|---|
| Lost connection after the request was sent | yes | no | P4, Q2c, Q2e |
| Attempt timeout | yes | no | Q2c "slow" (A=3, settled at the reservation) |
| Body cut after a 2xx status | yes, once | no | Q2c "cut", `TestACutBodyOnTheLastAttemptCountsOnce` |
| 2xx read to its end that does not decode | yes, and the call settles at the whole reservation | no | P5 |
| Connection refused, DNS or TLS failure (nothing sent) | yes (conservative over-count) | no | — |
| 429, 529, 408, 409, 500 | no | yes | Q2c, `TestNoFailoverAfterAnAttemptThatMayHaveBeenBilled` |
| **504 `timeout_error`** ("timed out while processing") | **no** | **yes** | **Q2a FAIL (L-new-3)** |
| 2xx with an all-zero usage | no: settles at 0 | — | Q2b, logged (I-new-3) |

Attempts per call never exceed 1 + MaxRetries on one credential, and possibly billed attempts never span two
credentials: Q2c (nine scripts, all ≤ 3 attempts, settlements ≤ the reservation), Q2d (A refuses once, B loses
three: 3 bounds), Q2e (529, lost, 403: no failover, one bound).

### Over-reservation and the DB clock

- `ai_spend.settle` carries `over_reservation: true` (catalogue: a flag), `geminus.ai.spend.warnings` counts
  `over_reservation` and `inference_geo_unknown`, and the generator's WARN lines carry only the reservation ID,
  the cost, the flag and this build's geo word. No prompt, answer or provider text.
- `aispend.Meter.today` runs `SELECT (now() AT TIME ZONE 'UTC')::date` as the first statement of the
  reservation's own transaction (`aispend.go:92-102`, called at `:143`); the month is derived from that day; settlement
  uses the reservation's stored day. The test sets the session time zone inside the same transaction and passes in
  UTC, UTC+14 and UTC−12. Production code cannot pin a day (`PinDay` is in `export_test.go`).

## New findings

### Low

**L-new-1. An unclosed run counts only in the UTC day it opened in.** `internal/aievals/record.go:287-301`.
An open line without its close is counted at its whole cap, but only on its open day and month. A run that opens
before 00:00 UTC and is still running after it holds nothing of the new day, so a second run in the same checkout
may take the whole new day while the first is still spending (Q1b: an open line at 23:42 UTC yesterday, cap USD
9.5, no close; today's run with a USD 10 cap was built, exit 0). Bounded: each run stays within its own cap, and
the two runs together stay within two days' limits. Fix: count an unclosed run in every UTC day and month from its
open up to now (or up to its open plus the longest a run can last: cases × `Budget.Timeout`).

**L-new-2. A reset or unmerged record restarts the day and month; nothing detects it.** `record.go:126-180`;
D-114 (b). The record is per checkout and starts empty. In this repository's workflow that is realistic in three
ways:

- A revert of tracked files after a run, before the commit (`git checkout -- .`, `git stash`, `git reset --hard`)
  empties the record but keeps the new, untracked live reports. Q1c: after a first run, the record was emptied
  with the report still next to it, and a second USD 10 run was built (exit 0).
- Agents run in worktrees and seldom push. A live run in one worktree is invisible to a run in another worktree,
  or in a fresh clone, until it is merged.
- Two branches that both append conflict at the end of the file, and resolving by one side drops runs.

Each run still stays within its cap (≤ the day limit). The Console spend limits (H-025, values not in the
repository) are the only bound across checkouts. The author discloses the per-checkout gap; the first and third
cases are not mentioned. Fix (cheap, any of):

- (a) Refuse when a `live-*.json` report in the directory names a run the record does not have.
- (b) Add `checks/ai-evals/live-spend.jsonl merge=union` to `.gitattributes`.
- (c) Also append every line to a machine-wide copy in the git common directory, which all worktrees of the
  clone share, and sum the union.

Until then, see the operational preconditions.

**L-new-3. A 504 `timeout_error` is treated as unbilled, and a 401/402/403 after it still fails over.**
`anthropic.go:284-296` (only transport failures and cut bodies are tallied) and `:402-421` (D-018's
classification, D-111 (a) and (b)). The errors page defines 504 as "The request
timed out while processing": the request reached the API and was being processed. No official page I read says
whether an error status is billed (NOT VERIFIED either way). The ledger's claim never to under-record rests on
"an error status is not billed" (D-111 (a)). Q2a: A answers 504, 504, 401; B answers. The call failed over and
settled B's 1,080,000 nano-USD only; the two 504s settled at 0. If 504s are billed, a call can be billed for more
attempts than its reservation holds (A's 504s plus B's attempts), which is the M3 gap again. The case is rare: the
default `max_tokens` 8192 is far below the SDK's 10-minute guard. Fix: count a 504 (and perhaps any 5xx except 529)
as an attempt that may have been billed, which also stops the failover. Or obtain Anthropic's statement that error
statuses are not billed and cite it in D-111.

**L-new-4 (decision and process). D-116 is an owner question that T-053 found, and it is answered "not
blocking" by the agents themselves.** D-116 (PROPOSED) asks the owner whether to name an `inference_geo`, and
whether the two organizations' workspaces have the same `default_inference_geo`/`allowed_inference_geos`. It then
says option A "不擋 H-025 的 live run" (does not block H-025's live runs) because it never under-records. That
reasoning is about spend only. However:

- SECURITY §7 says "provider/model/data residency/retention 必須人類確認" (data residency must be confirmed by a
  human).
- H-025 says that when T-053 finds a matter needing the owner's confirmation, live stays off and the owner is
  asked first. D-113's retention question, the same kind of account-setting question, did keep live off until
  H-026.

The M2 decision is safe for spend: reserving at 1.1x and settling by the reported geo cannot under-record.
H-025's runs send synthetic data only, so where inference runs has no customer-data impact, and I do not rate this
higher. The fix is not code. Before the first live call, either ask the owner the one D-116 account question (or
for an explicit OK to run on the workspaces' default geo), or record in the report why the synthetic-only H-025
runs fall outside H-025's ask-first clause.

### Info

- **I-new-1.** The daily limit the run's cap must fit is configuration. `AI_DAILY_LIMIT_USD` may equal the
  monthly limit (D-110 (b), PROPOSED), so `AI_DAILY_LIMIT_USD=100 AI_EVAL_LIVE_MAX_USD=100` builds a USD 100 run
  (Q1f). When approving D-110, the owner should decide whether the day limit may be raised by configuration at all.
- **I-new-2.** `-dir` points the record anywhere. A run started by hand with `-dir` on an empty record starts from
  zero; `make` passes only `-root`.
- **I-new-3.** A 2xx answer that decodes with an all-zero `usage` settles at 0 (Q2b). The real API always reports
  usage; counting such an answer as undecoded would be the conservative choice.
- **I-new-4.** D-110 option C says an organization at its Console spend limit answers 402 and D-018 would fail
  over. The errors page says a spend limit you set answers **400** (`invalid_request_error`), and a tier's spend
  cap 429. The adapter maps 400 to `rejected`, without failover or retry, so the Console limit stops calls on that
  organization instead of moving them to the other: the safe side. Only the decision text is inaccurate. Note that
  each later call on that credential is then also a 400, so B is not used: once org A's Console limit is reached,
  every case fails.
- **I-new-5.** The coordinator's `backend-test.log` and `backend-race.log` (`../../2026-10-26-T-053-r1/coordinator/`)
  report 29 of 74 packages `(cached)`. That is valid (the cache is keyed by content), but the table does not say
  it. My `backend-test` ran with `-count=1`: 74 ok, 0 cached.
- **I-new-6.** Three of my mutants survive, all test gaps rather than defects (see "Mutation"):
  - N3: the saturating sum of the record.
  - N7: `Main` summing the record for the run's own day. A test could inject `Main`'s clock, or run only when
    the UTC time is away from midnight.
  - N8: the report-exists check before the backend is built.
- **I-new-7.** The live eval's per-call reservation is about USD 1 (3 attempts × 1.1 × the bound of a ~30 KB
  prompt plus 8192 output tokens). A transport failure that sends nothing (for example the session's egress proxy
  refusing the host) still counts each attempt at its bound. So a smoke run that cannot reach the API records
  about USD 1 of the day without any real spend: the safe side, but it shrinks what the eval may take.

## Verified, no finding

- **Secrets.**
  - `git diff 245a375 d150918` and every log under `../../2026-10-26-T-053-r1/` and `checks/ai-evals/` contain
    no key-shaped string except the obvious fakes (`sk-ant-test-…`).
  - The record's lines are fixed structs (run ID, kind, times, model, approval, amounts, report name); the
    report's leak sentinels are tested.
  - The new WARN lines and audit members hold only amounts, IDs, flags and this build's geo word.
  - This session's environment sets `ANTHROPIC_BASE_URL` (by name only). The adapter ignores it: the base URL
    is explicit (`anthropic.go:80`, `:364`), and `TestRequestShape` traps a request sent to it.
- **Gates unchanged.** Live still needs the complete record, `AI_MODEL`, both clean and distinct keys and
  `GEMINUS_AI_LIVE=on`. The backend is built only after every refusal check and the open line. `Main` does no I/O
  to the provider before `rec.Open`.
- **The record's housekeeping.**
  - The lock file is git-ignored.
  - The record is in neither `MANIFEST.sha256` nor any `spec-check` input, so appending to it breaks no check
    and gives nobody a reason to reset it.
  - The repository starts with an empty record (0 bytes).
- **Decisions.** D-110–D-116 are `proposed`, with `approved_by` and `approved_at` null
  (`planning/decisions.json`). T-053 is `not_started`, its gates D-018 and D-110–D-116. H-025 and H-026 are
  recorded as human decisions.
- **Migration 0016 unchanged.** Not in `git diff 245a375 d150918`; it is on `origin` (commit `4450490`), so the
  round's "已 push，沒有修改" (pushed, not modified) is accurate.
- **No weakened assertion.** The changed expectations in the existing tests follow the decisions:
  - 1.1x per attempt.
  - Nine unanswered attempts settle at nine bounds, not a clamped three.
  - The database's day replaces a pinned one.
  - New report names.

  Each is listed in the report's "改變的期望值" (changed expectations), and none drops a check.
- **Corrected sentences.** The L5 sentences are corrected where they stood. The D9 sentence ("沒有測試能分辨",
  no test can tell them apart) is struck through and corrected in both places.

## Mutation

- **Round-1 harness** (`../../2026-10-26-T-053-r1/mutants.py`, 98 mutants) on a `git archive d150918` copy, with
  DB mutants on a fresh `dt_test_t053rr2` prepared by `make db-test` (`mutants-db-prepare.log`): see
  `mutants-r1-harness.log`.
- **My N-series** (`reviewer-mutants.py`, the same engine, 11 mutants) on the same copy and database: see
  `mutants-reviewer.log`.

- **Round-1 harness: 96 KILLED, 2 EQUIVALENT (F3, O5), no SURVIVED, INVALID or ERROR (exit 0).** This
  reproduces the author's and the coordinator's result on the integrated tree. I agree that F3 and O5 are
  equivalent: F3 for the reason given in round 1 (I3); for O5, the audit catalogue's `oneOf` refuses the same
  settlement's event in the same transaction, so the settle fails and writes nothing either way.
- **N-series:**
  - KILLED: N1 (a close dated before its open; re-run after I fixed my own mutant text, which did not build:
    `mutants-reviewer-n1-rerun.log`), N2 (negative settled), N4 (a run opened twice), N5 (zero cap), N6 (no final
    newline), N9 and N10 (a settlement exactly at its reservation flagged as over it, in the client and in the
    ledger), N11 (a refusal after a possibly billed attempt does not mark the credential).
  - SURVIVED: N3, N7, N8. All three are test gaps in code that is correct today (I-new-6):
    - **N3:** the saturating sum in `Record.Spent` is not tested; only crafted huge lines reach it. My Q1d case
      "two huge closed runs" would kill it.
    - **N7:** no test pins that `Main` sums the record for the run's own UTC day. `TestTheRecordBoundsTheNextRun`
      writes the same lines for today and tomorrow so as not to depend on the time of day, so a `Main` that sums
      tomorrow still passes. The arithmetic itself is pinned at fixed times (`TestTheRecordSumsTheDayAndTheMonth`).
    - **N8:** nothing tests that an existing report is refused before the backend is built. `WriteReport`'s
      `O_EXCL` (K12) still prevents an overwrite, so the effect would be paid calls and exit 3 instead of a free
      refusal. A collision needs a crafted file, because run IDs are fresh ULIDs.

## What I ran (exit codes; logs in this directory)

Go 1.27.3 (`GOTOOLCHAIN=local`); PostgreSQL 16 shared cluster (not started or stopped here), fresh databases
`dt_test_t053rr1`–`rr3`, dropped afterwards. Helpers: `gotest.sh`, `step.sh`, `db.sh`, `checks.sh`,
`run-mutants.sh`, `reviewer-mutants.py`.

| Command | Exit | Log |
|---|---:|---|
| `make backend-test GOFLAGS=-count=1` (worktree) | 0 (74 packages ok, 0 cached; DB tests skip here and ran below) | `backend-test.log` |
| `make db-test` on fresh `dt_test_t053rr1` | 0 | `db-test-rr1.log` |
| The 10 T-053 DB tests (`TestReserveAndSettle`, `TestLimitsRefuse`, `TestConcurrentReservationsNeverOvershoot`, `TestSettleKeepsItsDayAndOrganization`, `TestFailClosed`, `TestTheDatabaseClockPicksTheDay`, `TestOrganizationFilter`, `TestASettlementAboveItsReservationIsRecordedAndFlagged`, `TestAMeteredProposal`, `TestASpendLimitRefusesTheProposal`), `-race -v`, `GEMINUS_REQUIRE_DB_TESTS=1`, rr1 | 0 (10 PASS) | `t053-db-tests.log` |
| Probes P1, P2 (rr1) | 1 (P1 PASS; P2 FAIL by the L1 decision) | `probes-p1-p2.log` |
| Probes P3–P7 | 1 (P3–P6 PASS; P7 FAIL by the M2 decision) | `probes-p3-p4-p5-p6-p7.log` |
| Probes Q1a–Q1f, Q2a–Q2e | 1 (Q1b, Q1c, Q2a FAIL: the stricter expectations of L-new-1..3; the rest PASS or log only) | `probes-q1-q2.log` |
| `make db-test` on fresh `dt_test_t053rr2` (for the DB mutants) | 0 | `mutants-db-prepare.log` |
| Round-1 mutation harness (98) on a `git archive` copy, DB mutants on rr2 | 0 (96 KILLED, 2 EQUIVALENT) | `mutants-r1-harness.log` |
| My N-series (11) | 1 (N3, N7, N8 SURVIVED; N1 INVALID, my mistake) | `mutants-reviewer.log` |
| N1 corrected and re-run | 0 (KILLED) | `mutants-reviewer-n1-rerun.log` |
| `make ai-eval` | 0 (`fake-*.json` unchanged) | `ai-eval.log` |
| `make architecture-check` | 0 (86 packages, 23 rules, 16 build configurations) | `architecture-check.log` |
| `go test -race -count=1` on the T-053 packages (`internal/ai/...`, `config/...`, `aievals/...`, `storage/aispend/...`, `proposals/generator/...`, `cmd/worker/...`, `cmd/ai-eval-live/...`, `audit/...`, `observability/...`; DB tests skip here, raced on rr1 above) | 0 (19 packages ok, 0 cached) | `race-t053.log` |
| govulncheck v1.9.0 | 0 (No vulnerabilities found) | `govulncheck.log` |
| `go mod verify` | 0 | `go-mod-verify.log` |
| `make integration-test` on fresh `dt_test_t053rr3` | 0 (db-test up/down/up, then db-go-test: 35 packages ok, no FAIL or SKIP) | `integration-test.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` (after this report was written) | 0 (`PASS_TS_CONTRACTS_FRESH`, `PASS_DB_TESTS_FRESH`; it regenerates `checks/docs-check.json` for this report, committed with it) | `spec-check.log` |
| Official pages (data residency, pricing, errors) read with the web-fetch tool | — | `official-docs-excerpts.md` |

The AI variables of this session were checked by name only (`env-names.txt`).

## NOT RUN

- **`make ai-smoke-live` and `make ai-eval-live` against the real API.** Not run, by instruction. I did not set
  `GEMINUS_AI_LIVE=on` with any key and did not read, print or measure any key value. In this session the AI
  variables were checked by name only (`env-names.txt`): none of `GEMINUS_ANTHROPIC_API_KEY_A`, `_B`,
  `ANTHROPIC_API_KEY`, `GEMINUS_AI_LIVE`, `AI_PROVIDER`, `AI_MODEL` is set.
- **Refusal paths of the make targets with fake keys.** Not run as processes. The in-process tests cover them
  (`TestMainRefusesWhileLiveIsOff`, `TestTheLiveEvalRunsOnlyWhenLiveIsOn`, the record tests), and with the
  record complete a `make` run with fake keys and the switch on would reach the production API.
- **Full `make backend-race`.** I raced the T-053 packages only (see the table).
- **`make frontend-test`, `make e2e-test`.** No UI, HTTP contract, schema or TS change in `245a375..d150918`.
- **SQL mutants.** Migration 0016 is unchanged since round 1, where 7 of 7 were killed.

## Ready for the live smoke and the live eval?

**Yes, in my judgment the code is ready** for H-025's pre-authorized `make ai-smoke-live` and `make ai-eval-live`
with synthetic data, under these preconditions:

1. **L-new-4 first.** Get the owner's answer to D-116's account question, or their OK to run on the workspaces'
   default inference geo. Alternatively, record in the report why the synthetic-only runs are outside H-025's
   ask-first clause.
2. **One checkout, one run at a time.** Run both commands in the coordinator's main checkout, not a fresh worktree
   or clone. Commit `checks/ai-evals/` (the record and the report) after each run, before any other live run
   anywhere, and do not revert tracked files in between (L-new-2). Do not start a run within about two hours
   before 00:00 UTC (L-new-1).
3. **Default limits only.** Do not set `AI_DAILY_LIMIT_USD`, `AI_MONTHLY_LIMIT_USD` or
   `AI_ORGANIZATION_DAILY_LIMIT_USD` (defaults: day USD 10, month USD 100, organization day USD 5), and do not pass
   `-dir` (I-new-1, I-new-2).
4. **Smoke first**, with its default USD 2 cap. Continue only if it exits 0 with no spend warning, no unknown
   attempts and no transport error in its summary.
5. **The eval cap must fit the day.** Read the smoke's printed `committed` amount and set
   `AI_EVAL_LIVE_MAX_USD` to at most 10 − that amount, rounded down to the cent: for example `9` when the smoke
   committed ≤ USD 1.00, `8.9` when it committed USD 1.06. A cap that does not fit is refused with exit 2 before
   any call, at no cost. Alternatively, run the eval on the next UTC day with the default USD 10. The eval stops
   (exit 1, incomplete) when committed spend plus the next reservation (about USD 1) passes the cap; do not re-run
   it the same UTC day.
6. **Keys.** Put the keys and the switch only in the live command's own environment. Afterwards, check that the
   record, the report and the command output hold no key (`grep -r 'sk-ant-' checks/ai-evals`).
7. **Stop on any WARNING.** If `over_reservation` or `inference_geo_unknown` appears on stderr or in the report,
   stop and report before any further live run.
