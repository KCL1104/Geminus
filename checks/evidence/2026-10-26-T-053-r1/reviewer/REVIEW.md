# T-053 independent review, round 1 (H-003)

Reviewer: independent agent (did not write T-053). Date: 2026-10-26. Tree: `a0fb365` (T-053 commits
`4bfb8f2..8948b70`, fixes `fc0b277`, `4450490`, `921910f`; H-026 `f4db5d6`; `a0fb365` changes docs and
planning only). All logs and probes are in this directory; every log ends with its exit code.

## Verdict: CHANGES REQUESTED

One High and three Medium findings. Most of T-053 holds up:

- The DB ledger cannot be overshot by concurrent reservations.
- The gates fail closed.
- No key leaks.
- The SQL rules are well tested: all 7 SQL mutants I added were killed.
- The price table matches the live pricing page.

The problems are where money is counted. Two of them bear directly on H-025 (live runs right after this
review):

- The live eval and the smoke spend outside every in-code day and month limit (H1).
- The price assumes global inference, but the request does not ask for it (M2).

## High

### H1. The live eval and smoke are outside the in-code daily and monthly limits

Code: `internal/aievals/main.go:101-112,138`; `internal/aievals/live.go:539`; D-114 (b).

H-018 says USD 100 a month for both provider organizations "並另設每日上限" (and a daily limit). The
T-053 scope asks for an in-code monthly limit across both organizations. The only bound on
`make ai-eval-live` and `make ai-smoke-live` is a fresh in-memory `MemoryMeter(capNano)` per run (`main.go:138`).
The only check on that cap is `capNano > ai.Limits.Month` (`main.go:110`). Nothing records eval spend across
runs, and the DB ledger never sees it. This has three consequences:

- **Defaults.** In one UTC day, the defaults smoke (USD 2) plus eval (USD 10) can commit USD 12. The
  deployment daily limit is USD 10 (D-110). That is exactly the sequence H-025 pre-authorizes.
- **Overrides.** `AI_EVAL_LIVE_MAX_USD` may be anything up to the monthly limit. Probe P3 shows the backend is
  built and the run completes (exit 0) with caps of `100`, `50` and `10.01`. So one run may use the whole
  monthly budget in one day.
- **Repeated runs.** Running the eval again starts from zero each time. `WriteReport` overwrites a report from
  the same day (`os.WriteFile`, `live.go:539`). That report is the only record of eval spend, so a re-run on
  the same day erases the record of the earlier one.

The author disclosed this in D-114 (b), which is PROPOSED: "app 的月上限不含 eval 的花費；兩者合計由 Console
spend limit 限制" (the app's monthly limit excludes eval spend; the Console spend limit bounds both together).
But H-025 says the in-code limits must be reviewed and enabled before the live runs. For the runs H-025
authorizes, the only in-code limits that apply are the per-run caps. The Console limit values are not
recorded anywhere in the repository.

How I showed it: `probe-p3-aiapproval_test.go.txt` uses the in-process fake backend and fake keys, with no
network. Log: `probe-p3-head.log`, exit 1, all three subtests FAIL: the backend was built and the exit was 0.

Fix, at least one of:

- (a) Refuse a cap above `ai.Limits.Day`, and keep a small append-only local ledger of eval spend per UTC day
  and month (for example `checks/ai-evals/live-spend.jsonl`). Refuse a run whose cap plus the day's or month's
  recorded spend exceeds the limit.
- (b) Take D-114 option B: meter the eval through the DB ledger.
- (c) The owner explicitly approves D-114 (b), with the arithmetic above, before H-025 is used. Whichever is
  chosen, do not let a re-run overwrite an earlier report.

## Medium

### M1. Nothing tests the application organization filter in the ledger; three mutants survive, and the "no test can distinguish" claim is wrong

Code: `internal/storage/aispend/aispend.go:205-206` (D9), `:140-141` (F1), `:276` (F2). Claim:
`checks/T-053_VALIDATION.md:139` and `:278`.

AGENTS.md §2 requires an application filter as well as RLS. The repository already tests that filter by
itself: it runs as the provisioning superuser, whom RLS does not restrict. See `TestOrganizationFilter` in
`internal/storage/projects/projects_test.go:17-21` and `internal/storage/proposals/proposals_test.go:83-87`.
The aispend tests run only as the worker under RLS, so they cannot fail when the filter goes away.

I ran `filter-mutants.py` on a `git archive HEAD` copy against the review database:

| Mutant | Author's 5 DB tests | Probe P1 (superuser runner) |
|---|---|---|
| D9: settle's `SELECT … FOR UPDATE` without `organization_id` | SURVIVED | KILLED (another organization's settle reaches A's reservation; fails only on a CHECK, not `ErrNotFound`) |
| F1: reserve's organization-day `UPDATE` without `organization_id` | SURVIVED | KILLED (updates every organization's row for the day, so the reservation is refused as `quota_exceeded`) |
| F2: `TotalsAt` reads the day without `organization_id` | SURVIVED | KILLED (SQLSTATE 21000) |
| F3: settle's reservation `UPDATE` without `organization_id` | SURVIVED | SURVIVED (equivalent: the filtered `SELECT … FOR UPDATE` in the same transaction comes first) |

Logs: `filter-mutants-existing-tests.log` (exit 1, 4 survived) and `filter-mutants-probe-p1.log` (exit 1, 3
killed, F3 survived). Probe: `probe-p1-p2-aispend_test.go.txt`, `TestProbeP1OrganizationFilterWithoutRLS`;
it passes on HEAD (`probe-db-head.log`).

None of these mutants corrupts the ledger on its own. Under the worker role RLS still filters. Without RLS,
the remaining filters and the deferred day check roll the transaction back. So the risk is losing a required
layer of defence without any test noticing, not a live tenant leak today.

Fix: add a superuser-runner test like P1 to `internal/storage/aispend`, and correct the two sentences in the
report.

### M2. The price table assumes global inference, but the request does not ask for it

Code: `internal/ai/spend/spend.go:34-36` ("the adapter sends no inference_geo; "us" would cost 1.1x");
`internal/ai/provider/anthropic/anthropic.go:312-318`; `docs/AI_SPEC.md:33` ("沒有 `inference_geo`", no
`inference_geo`); D-110 (a).

The data residency page (see `official-docs-excerpts.md`) says `default_inference_geo` "Sets the fallback geo
when `inference_geo` is omitted from a request". It also says workspaces that had opted out of global routing
were migrated to `default_inference_geo: "us"`, which is billed at 1.1x. The workspace setting is an account
setting the agent cannot see, like ZDR was.

If either organization's workspace defaults to `us`, both the reservation bound and the settlement are 10% low
on every call. The ledger then lets about 10% more spend through than the limits say.

Probe P7 (`probe-p4-p5-p7-anthropic_test.go.txt`, local httptest server only) shows the request body has no
`inference_geo`. Log: `probe-p4-p5-p7-head.log`, P7 FAIL.

Fix: send `InferenceGeo: sdk.String("global")` (the field `MessageNewParams.InferenceGeo` exists in SDK
v1.78.0, `message.go:14081`). A
workspace that disallows global then fails closed with a 4xx. Also check `usage.inference_geo`
(`message.go:12519`) in the answer: refuse to settle below cost, or price at 1.1x when it is not `global`. Add
a request-shape test.

### M3. "A call is billed for at most 1 + MaxRetries attempts" is false when failover follows unanswered attempts

Code: `internal/ai/provider/meter.go:36-38` and `:209-211`; `internal/ai/provider/anthropic/anthropic.go:320-371`;
D-111 (a).

The SDK retries lost connections on credential A, which counts them as unknown attempts. If A's last attempt
then gets 401, 402 or 403, the adapter fails over to B, and B gets another `1 + MaxRetries` attempts. With
`MaxRetries = 2`, up to 5 attempts may have been billed against a reservation of 3. The settlement is clamped
to the reservation (`meter.go:209-211`), so the ledger quietly records less than the possible cost.

Probe P4: A gets `hangUp, hangUp, 402`; B gets `hangUp ×3`. Result: 6 hits, 5 unanswered. The ledger settles
USD 0.56316 (3 × 0.18772) of a possible USD 0.9386. The claim sits in a PROPOSED decision the owner is asked
to approve. The amount is small per call (at most `2·MaxRetries` attempt bounds) and needs an unusual sequence.

Log: `probe-p4-p5-p7-head.log`, P4 FAIL.

Fix, one of:

- Reserve `(1 + MaxRetries) × len(credentials)` attempt bounds.
- Do not fail over once the call has an unanswered attempt; return the failure instead.
- Either way, drop the clamp or keep it only where the bound is proven. Add a test.

## Low

### L1. A settlement above its reservation goes past the limit with no signal

Code: `internal/storage/aispend/aispend.go:217-241`; `internal/ai/provider/meter.go:212-213`;
`migrations/0016_ai_spend.up.sql:5-6`.

A usage answer settles at its cost without a clamp. That is correct, because what was billed must be
recorded. But nothing logs or counts the case. Probe P2: reserve 600 against an organization-day limit of
1000, then settle 5000. The settle succeeds and the day holds 5000. Log: `probe-db-head.log`, P2 FAIL by
design.

D-111 discloses this, but the migration header "none can take a limit past its value" overstates it. Each
call can overrun only if the quote under-estimates (bytes + 4096 tokens), so this is unlikely.

Fix: WARN plus a metric when `settled > reserved` (it means the bound's assumption broke). Pin the behaviour
with a DB test. Reword the header.

### L2. The two-key check compares raw strings

Code: `internal/config/ai.go:248`.

The same key with a trailing newline or a leading or trailing space is accepted as "two organizations".
Probe P6 (`probe-p6-config_test.go.txt`, fake strings) fails three of three. Log: `probe-p6-head.log`.

Fix: reject keys with surrounding white space or control characters (or trim, then compare).

### L3. The worker's clock picks the day and month

Code: `internal/storage/aispend/aispend.go:105-106`; `migrations/0016_ai_spend.up.sql:30-32`.

A worker whose clock is a day or month off reserves into a fresh bucket. Together with correctly set
workers, the deployment can then spend up to twice the day or month limit.

Fix: take the day from the database (`(now() AT TIME ZONE 'UTC')::date`), or refuse a meter clock more than a
few minutes from the database's `now()`. Keep an injected clock for the tests only.

### L4. `refusals.sh` still claims no live call is possible

Code: `checks/evidence/2026-10-26-T-053/refusals.sh:2-5`.

That was true before H-026. At HEAD, steps 4 and 5 (`H-018` + fake keys + `GEMINUS_AI_LIVE=on`) would build
the live backend. They would send requests to `api.anthropic.com` (401s) and write a `live-smoke-*.json`
report. The author knew this and did not re-run it (report line 50), but the script is still there.

Fix: delete steps 4 and 5 or mark the script obsolete.

### L5. Stale statements

- `checks/T-053_VALIDATION.md`:
  - The NOT RUN row (line 203) still says the live command "會拒絕（exit 2）" (would refuse, exit 2). At HEAD
    it would call, given keys and the switch.
  - Rows 2a and 3 (lines 81, 84) say `Retention` is blank.
  - The top section says these sections predate the change, but the NOT RUN reason now matters.
- `internal/aievals/live_test.go:26-30`: "This build cannot turn live AI on".
- `docs/CONTRACT_BLOCKERS.md:83` (CB-36): "登記清單是空的" (the approval list is empty).
- `docs/SECURITY.md:93`: the first sentence still says the list is empty; the parenthetical corrects it.

### L6. A live-eval run that stops after paid calls leaves no record of its spend

Code: `internal/aievals/main.go:143-147` and `:29-31`.

When `Run` returns an error (for example a review error after the provider answered), `Main` prints only "the
run stopped" and exits 3. The doc comment of `ExitReport` says "the summary on stdout is what remains", but
no summary or cost is printed on that path. Given H1, this was the only record of what the run spent.

Fix: print the meter's `Spent()` and `Committed()` on every exit after the backend was built.

## Info

- **I1.** A 200 answer read to its end that the SDK cannot decode settles at 0 (`none`). Probe P5,
  `probe-p4-p5-p7-head.log`. This is unlikely from the real API.
- **I2.** An open reservation (the worker died between reserve and settle, or settle failed) never expires. It
  holds its bound until the day or month ends: the safe side, but no operator view lists them.
- **I3.** F3 is an equivalent mutant (see M1); no test is needed.
- **I4.** The limits are per-process configuration, not stored in the database. Two workers configured
  differently enforce different limits on the same totals, and the higher one wins.
- **I5.** The API process reads both keys when the switch is on (`cmd/api/main.go:50` → `config.Load` →
  `LoadAI`), though it never uses them. Deploy the API without the key variables.
- **I6.** `MemoryMeter.Settle` would overflow `committed` only with an absurd usage (about 4.6e14 tokens).

## Verified, no finding

- **Price.** The `claude-opus-5-5` row of the live pricing page (fetched 2026-10-26) is $4 / $5 (5m) / $8
  (1h) / $0.20 / $20 per MTok, which matches `spend.go:53`. Opus 5.5 has no long-context premium. Cache writes
  are priced at the 1h rate (upper bound). Excerpts: `official-docs-excerpts.md`.
- **Retention.** The SECURITY §7 quotes match the api-and-data-retention page. Opus 5.5 is not a Covered
  Model. The H-018 record's `Retention` is exactly H-026: `standard`, KCL1104 (repository owner), 2026-10-26,
  source H-026.
- **Decisions.** D-110–D-115 are `proposed`, with `approved_by` and `approved_at` null
  (`planning/decisions.json`).
- **Gates.** Live needs `AI_PROVIDER=anthropic`, `AI_POLICY_APPROVAL=H-018`, `AI_MODEL` equal to the record,
  a priced model, `GEMINUS_AI_LIVE=on`, and two keys, so no single variable enables it.
  - The switch on with any gap refuses to start.
  - The default is `disabled`; the fake works only in development or test.
  - `anthropic.New` takes only a valid `Approval`, the approved model and the labels `org-a`, `org-b`.
  - `provider.New` refuses a Billable backend, and the worker refuses live AI without the ledger.
  - The architecture rules confine the adapter to `cmd/worker` and `cmd/ai-eval-live`.
- **`ANTHROPIC_API_KEY`.** It is never read. The adapter uses `sdk.NewMessageService(opts…)`, which (unlike
  `DefaultClientOptions`) reads no environment (`anthropic-sdk-go@v1.78.0/message.go:37`, `client.go:53-117`).
  config's recorder tests confirm it.
- **Secrets.** `secret.Secret` redacts under every verb, JSON and slog. Errors name variables and labels
  only, and the report and audit hold no key, prompt or answer text. In this session only variable names were
  checked: none of the AI variables is set.
- **Ledger concurrency.**
  - The lock order is month → day → organization day in both Reserve and Settle. The first-time inserts all
    come before the UPDATEs, in one order, so I found no deadlock cycle.
  - Conditional UPDATEs under READ COMMITTED recheck the latest value.
  - `TestConcurrentReservationsNeverOvershoot` passes on two pools.
  - Settle charges the reservation's own day and month.
  - A refused limit rolls back. A crash leaves the bound held (I2).
  - DB, audit or ledger errors give `spend_unavailable` and no call.
- **SQL mutants** (`sql-mutants.py`; each on a fresh `dt_test_*` database with full `make db-test`):
  `sql-mutants.log`, 7 of 7 killed (exit 0). Mutants: FORCE RLS removed, settle twice, DELETE granted,
  wrong day sum, deployment totals unscoped, an immediate trigger, any `created_at`.
- **The author's unit mutants U1–U46**, re-run with the author's harness on an archive copy:
  `unit-mutants.log`, 46 of 46 killed (exit 0). The harness's last line, "CHANGED: fatal: not a git
  repository", comes from the copy having no `.git`.
- **AI boundary.** The metered path writes only the spend tables and audit events. The request offers no
  tools and no fallbacks, and thinking is dropped unread.

## What I ran (exit codes; logs in this directory)

| Command | Exit | Log |
|---|---:|---|
| `make backend-test GOFLAGS=-count=1` | 0 (74 packages ok) | `backend-test.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `spec-check.log` |
| `make ai-eval` | 0 | `ai-eval.log` |
| `make architecture-check` | 0 | `architecture-check.log` |
| `go test -race -count=1` on the T-053 packages (`internal/ai/...`, `internal/config/...`, `internal/aievals/...`, `internal/storage/aispend/...`, `internal/proposals/generator/...`, `cmd/worker/...`, `cmd/ai-eval-live/...`, `internal/audit/...`, `internal/observability/...`) | 0 | `race-t053.log` |
| govulncheck v1.9.0 (go1.27.3) | 0 | `govulncheck.log` |
| `go mod verify` | 0 | `go-mod-verify.log` |
| `make db-test` on `dt_test_t053rv1` (up/down/up, `ai_spend.sql`) | 0 | `db-test.log` |
| The 7 T-053 DB tests, `-race -v`, `GEMINUS_REQUIRE_DB_TESTS=1` | 0 (all PASS) | `t053-db-tests.log` |
| `make integration-test` on a fresh `dt_test_t053rv2` (PostgreSQL 16.15) | 0 | `integration-test.log` |
| Probes P1, P2 (DB) | 1 (P1 PASS, P2 FAIL, see L1) | `probe-db-head.log` |
| Filter mutants with the author's DB tests | 1 (4 survived) | `filter-mutants-existing-tests.log` |
| Filter mutants with P1 | 1 (3 killed, F3 equivalent) | `filter-mutants-probe-p1.log` |
| Probe P3 (eval cap) | 1 (H1) | `probe-p3-head.log` |
| Probes P4, P5, P7 (local httptest) | 1 (M3, I1, M2) | `probe-p4-p5-p7-head.log` |
| Probe P6 (keys) | 1 (L2) | `probe-p6-head.log` |
| SQL mutants S1–S6, S8 | 0 (7 killed) | `sql-mutants.log` |
| Author's unit mutants U1–U46 | 0 (46 killed) | `unit-mutants.log` |

The probes are saved as `*_test.go.txt`. While they ran they sat in the package directories or in the
`git archive` copies, and they were removed afterwards. The worktree has no Go change (`git status`).

## NOT RUN

- **`make ai-smoke-live` and `make ai-eval-live` against the real API.** Not run, by instruction. I did not
  set `GEMINUS_AI_LIVE=on` with any key, and did not read, print or measure any key value.
- **The author's `refusals.sh`.** At HEAD it would reach the production API (L4).
- **Full `make backend-race`.** I raced the T-053 packages only; the coordinator's full run is
  `checks/evidence/2026-10-26-T-050-T-053/coordinator-backend-race.log`.
- **`make frontend-test` and `make e2e-test`.** No UI or contract change.
- **The author's DB mutants D1–D8.** Not re-run here; the coordinator's run is `coordinator-mutants-db.log`.
  D9 is re-run above.
