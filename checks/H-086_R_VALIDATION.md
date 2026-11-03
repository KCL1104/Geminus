# H-086 (R) validation: one re-ask of an explanation the grounding check refused

Slice H-086 (R), branch `h086-retry` from `main` 93095a3. Agent self-check, no independent review yet. No live AI call was made; no `.env` value or key was read; nothing was pushed or deployed.

## 1. What the owner decided and what this slice does

The owner's decisions of 2026-11-03 (the coordinator records them as H-086):

- For explanations the server's grounding check refuses (live that day: 2 of 5 refused with `ANALYSIS_NUMBER_UNVERIFIED`, at `/suggested_next_experiments/0/name` and at `/scenario/summary`), the owner chose 「做 (Recommended)」: ask the AI once more with the issues, as the photo and text proposals already do ("Revised once after Geminus's check.", H-057), without loosening the check.
- 「B：A＋平行測試」 (Muse Wonder 1.0 as the investigation planner, and parallel what-if testing by the one planner) is **not** in this slice.
- 「不要管 今晚凍結 這件事了，反正就是明天我錄音前做完就好」: no code freeze tonight; the work must be done, reviewed and deployable before the owner records the video's voice on 2026-11-04.

What the slice does (AI_SPEC §9, the H-086 (R) paragraph):

| Part | Change | Files |
|---|---|---|
| When | An answer the client took as schema-valid and handed back (`provider.AnalysisResult.Prior`, new) that `analyst.Check` refuses with at least one issue, while the attempt has not stopped, is asked again once in the same job attempt. For an investigation's analysis, only while the investigation still runs, read again just before the re-ask (review round 1, §7). `analyst.Check` is unchanged. | `internal/aigraph/analysis/graph.go` |
| Not re-asked | A first call that failed: a provider error, a spend cap, a truncated answer, a schema-invalid answer (`AI_INVALID_OUTPUT`), a cancellation. These keep their existing job failure and retry rule. An attempt that stopped once the first answer was in also keeps its refusal final. | same |
| Why this rule | The owner's choice covers the grounding check's refusals. A failed call has no checked answer to fix: the existing rules already decide whether the job retries (a timeout, rate limit or unavailable provider) or fails (truncated, refused, invalid output, spend cap). Re-asking those would change behaviour the owner did not ask to change and would spend money on calls whose cause the issues cannot explain. A stopped attempt's refusal stays final (as `TestAStopAfterTheAnswerStillChecksIt` pinned before this slice), so no later attempt pays again. | |
| The second request | `aicontext.ReviseAnalysis`. It resends the first call's sealed prompt byte for byte (system text, user message, role analyst, locale). The model's own answer goes back as the assistant turn, through `provider.Revision`, which the analyst's prompt now takes as well as the proposal roles' (`provider.Role.Revised`). Then comes the follow-up: `prompts/reground/system.md` (`reground-0.1.0`, English) and one `server_data` block `SERVER_CHECK` with at most 50 issues (the rest are counted). Each issue is a code the check emits, a path cut to the analysis output schema's own members and indexes, and the code's fixed rule (`aicontext.CheckRule`). | `internal/ai/aicontext/reground.go`, `prompts/reground/system.md`, `internal/ai/provider/{provider,revision}.go` |
| The outcome | The second answer goes through exactly the first one's checks against the same supplied data. If it passes, it is the analysis. If it is refused, the job fails with `ANALYSIS_UNGROUNDED` and the second answer's issues. If the second call fails (any error, the attempt stopping included), the first refusal stands, not retried. If the server cannot build the follow-up, the job fails with `ANALYSIS_INPUT_INVALID`, not retried. Nothing asks a third time: the branch has no cycle. | `graph.go`, `internal/analyses/analyzer/analyzer.go` |
| Spend | The second call goes through the same metered ChatModel and client with the same attribution: its own reservation (its quote holds the prior answer and the follow-up), its own settlement, the same caps, and for an investigation's analysis the USD 2.50 ceiling (the ledger counts every reservation of the investigation's analyses). It has the analyst's output bound and timeouts (reasoning not lowered, H-055). `explain_results` queues the same job, so investigations get the same re-ask. | |
| Record | A completed analysis stores the first call's metadata plus `repair` {outcome grounded, issues, issues_not_listed, the second call's metadata}. The audit events `analysis.generate` and `analysis.attempt_failed` gain `repair*` fields (outcomes grounded, ungrounded and failed; their own enum in the audit catalogue). The second call's log line adds `call_attempt` 2; the first line is unchanged. | `analyzer.go`, `internal/audit/audit.go` |
| Label | `generated_by.revised_after_check` is true on a completed analysis that is the second answer (`proposals.FromMetadata` reads outcome grounded). The explanation shows "Revised once after Geminus's check." / 「經 Geminus 檢查後修正過一次。」 with the existing catalogue key `proposal.revisedAfterCheck`, so no catalogue change. | `internal/proposals/public.go`, `apps/web/features/results/analysis.tsx` |
| Contract | Description text only, no new field: `common.schema.json` GeneratedBy and `revised_after_check` (it no longer says "Never on an analysis"), and the `getAnalysis` description in `openapi.yaml`. The TS contracts were regenerated (`apps/web/lib/contracts/common.ts`, doc comment only). No migration: `provider_metadata` is a JSON object already. | |

### Prompt template versions (for the coordinator to record)

- New template: `prompts/reground/system.md`, version **`reground-0.1.0`** (English). The re-ask's prompt version is **`analyst-0.3.3+reground-0.1.0`**.
- `prompts/analyst/system.md` (**`analyst-0.3.3`**) is **unchanged**. Every first call's request is byte for byte the same as before: the analyzer golden's first request, log line and metadata are unchanged (§2).
- Nothing pins template versions in an approval record (`internal/config/aiapproval` records model, data policy, quota and credentials only). The versions are pinned by tests only: `prompts/embed_test.go` (the `reground` template's key words), `internal/ai/aicontext/reground_test.go` (`reground-0.1.0`, `analyst-0.3.3+reground-0.1.0`) and the analyzer goldens.
- The rules each issue carries (`aicontext.CheckRule`, ten codes) are server text inside the follow-up, covered by the second call's InputHash. They are not versioned on their own, just as the proposal repair's fixed sentences (`review.Clean`) are not.

## 2. Goldens and assertions changed on purpose

- `internal/analyses/analyzer/testdata/analyze-goldens.json` was rewritten with `GEMINUS_WRITE_ANALYZER_GOLDENS=1` and compared with the file before:
  - The diff is **insertions only** (641 lines added, 0 removed).
  - **One case changed**: `the-answer-is-ungrounded`. It now also has the second request (`analyst-0.3.3+reground-0.1.0`, with its `revision_sha256`), the second log line (`call_attempt` 2) and the re-ask's record (outcome ungrounded). Its first request, first log line, metadata, message and issue count are identical to before.
  - **Four cases added**: `reask-grounded`, `reask-grounded-metered`, `reask-ungrounded-again`, `reask-call-fails`.
  - Every other case is byte for byte as before.
- `internal/aigraph/analysis/graph_test.go` `TestFailuresAreTyped`: the ungrounded case expected exactly one call. It now expects two (the answer and its one re-ask), checks the re-ask's record, and checks that no other failure is asked again. The count check is now exact for every case (it used to be "one or none").
- `internal/analyses/analyzer` `TestAnalyzeOutcomes`: the ungrounded case now expects 2 requests; the four new cases are added.
- `internal/ai/provider/revision_test.go`: the negative case "a role that takes no proposal" (analyst) is replaced by the importer and the investigator, which still take no revision. The analyst's revision is now allowed and tested (`TestAnExplanationsRevision`).
- `internal/aigraph/analysis` `Config.Called` gains the call number (1 or 2); its test observers were updated.

No security, immutability or golden assertion was weakened or skipped.

## 3. Gates (commands run in the worktree; toolchain go1.27.3, node 24.21.0, Python 3.13 venv from `requirements-spec.txt` in the session scratchpad)

| Command | Exit | Result |
|---|---|---|
| `make spec-check` (`PYTHON` the scratch venv) | 0 | contracts `PASS_LIMITED_SPEC_CHECKS`, planning, docs, decisions shape `PASS_REGISTER_SHAPE_ONLY`, `PASS_TS_CONTRACTS_FRESH` (17 files, 166 types), `PASS_INDEPENDENT_HASHES`, `PASS_DB_TESTS_FRESH`, `PASS_STATIC_DEPLOY_CHECKS`, every other part PASS |
| `scripts/gen_ts_contracts.py` | 0 | wrote 17 files, 166 types; only `apps/web/lib/contracts/common.ts` changed (the GeneratedBy and `revised_after_check` doc comments) |
| `make backend-test` (gofmt, `go vet`, `go test` of every package, full differential) | 0 | 104 packages ok, 0 failed, 11 without tests; `scripts/check-architecture` ok (510 s). Database tests skip here (no database variables); `make integration-test` runs them. |
| `make integration-test` on a fresh disposable `dt_test_h086r3`: a PostgreSQL 16.15 cluster started for this slice by `initdb` in the session scratchpad (trust auth, 127.0.0.1:56486 only, no Unix socket, `fsync=off`); dropped and stopped afterwards | 0 | `DB migration smoke up/down/up completed` and the SQL tests; `db-go-test` (`GEMINUS_REQUIRE_DB_TESTS=1`, `-race`, `-p 1`) 46 packages ok, 0 failed, none skipped, among them `internal/api` (`TestAnUngroundedAnalysisFails`, `TestAnAnalysisAskedAgainIsCompleted`, confirmed by a `-v` run of the three analysis tests on the same database) and `internal/analyses/analyzer` |
| The run before it, on `dt_test_h086r2` | 2 | `TestWhatTheJobWritesOfTheReask` failed once: it compared two encodings of the metadata byte for byte, and JSON v2 does not order the disclosure's maps. The test now compares canonical JSON. That test then passed `-race -count=20`, and the graph, context and parity tests passed `-count=10`, before the rerun above. |
| `make ai-eval` | 0 | `TestFakeProviderEval`, `TestReviewEval`, `TestAnalystEval` (gates `reference_refused` 0, `unsafe_answer_kept` 0, `recheck_follow_up_echoes_untrusted_text` 0) and `TestInvestigatorEval` PASS. Fake results only; the live eval is NOT RUN. |
| `make frontend-test` (eslint `--max-warnings=0`, `tsc --noEmit`, vitest) | 0 | 174 files, 3,376 tests passed |
| `flock <session scratchpad>/e2e.lock make e2e-test` | 0 | the default build (Muse Wonder on offer): 805 passed, 72 skipped (the screenshot tests). The build without Muse Wonder (`GEMINUS_E2E_OFFER_WONDER=0`): 805 passed. Screenshots NOT RUN (only with `E2E_SCREENSHOTS=1` in CI's container). The product closed loop with the fake provider: NOT RUN (T-040), as the target says. |
| `make backend-race` in full | NOT RUN | |
| Any live provider call (`make ai-eval-live`, `make ai-smoke-live`) | NOT RUN | |

## 4. Tests added

- `internal/ai/aicontext/reground_test.go`:
  - `TestTheRecheckContext` covers redaction and prompt injection:
    - The question and a node name carry an injection sentinel, a fake block tag, an e-mail address and a figure.
    - The prior answer carries the sentinel, an `sk-ant-` string and an e-mail address.
    - Issue paths and codes carry the sentinel and a closing block tag.
    - The follow-up holds none of these, only cleaned codes, paths and rules, as `server_data`.
    - The first turn is the first context's redacted one.
    - The prior answer prints as withheld.
    - It refuses no prior answer, no issues, too many issues, a negative count, a re-ask of a re-ask, a proposal's prompt and a tampered prompt.
  - `TestCheckPaths`.
- `internal/ai/analyst/codes_test.go` `TestEveryCheckCodeHasItsRule`: the ten codes declared in `analyst.go`, `analyst.Codes` and `aicontext.CheckCodes` are equal.
- `internal/ai/provider/revision_test.go` `TestAnExplanationsRevision`:
  - The analyst's schema-valid answer is held as the prior answer and withheld when printed or encoded; a refused one is not held.
  - The analyst's revision is sealed, and its call has the analyst's output bound and timeouts.
  - Research is never allowed in it.
  - `Role.Revised` holds for the proposal and analyst roles only.
- `internal/aigraph/analysis/recheck_test.go`:
  - The first answer is ungrounded and the second grounded: the analysis is completed and labelled. The second request is checked byte for byte, and so are the follow-up's contents and the first call's metadata.
  - Both answers ungrounded: it fails with the **second** answer's issues, and nothing asks a third time.
  - The second call fails (unavailable, truncated, rate limited, an error that is not the client's, a schema-invalid answer): the first answer's issues stand, and the record names the code.
  - A failed first call (schema-invalid, truncated, unavailable, a spend limit) is not re-asked.
  - Metering: two reservations on the same attribution, the second larger, two settlements, two different reservation IDs. A second reservation the investigation ceiling refuses (`AI_QUOTA_EXCEEDED`) sends nothing more and leaves the first refusal.
  - A stop before the re-ask: no second call. A stop during the re-ask: the first refusal stands with `AI_CANCELLED`.
  - The follow-up bound: 57 issues give 50 listed and 7 counted, with nothing of the answer echoed.
- `internal/analyses/analyzer/recheck_internal_test.go`:
  - The stored metadata, with and without a re-ask (byte for byte as before without one).
  - `FromMetadata` returns revised for a re-asked analysis.
  - The `analysis.generate` and `analysis.attempt_failed` events of the three outcomes pass `audit.Check`.
  - No prose of either answer is in the stored metadata or the audit events.
- `internal/audit/audit_test.go`:
  - Valid examples of the three outcomes.
  - Refused: a proposal's repair outcome on an analysis, a re-ask's outcome on a proposal, and an answer field.
- `internal/proposals/revised_test.go`: the `grounded` outcome shows the flag; `ungrounded` and `failed` do not; nothing of the second call is shown.
- `internal/analyses/analyses_test.go` `TestARevisedAnalysisSaysSo`: the record validates as the contract's `AnalysisRecord` with and without the flag, and shows nothing of the second call.
- `internal/api/analyses_test.go` (database):
  - `TestAnUngroundedAnalysisFails` now also checks 2 provider requests, the audit's `repair` ungrounded, `repair_issues` and the template, and a failed row with no content.
  - New `TestAnAnalysisAskedAgainIsCompleted`:
    - The analysis is completed with the second answer, and a viewer sees `revised_after_check`.
    - The stored `repair` and the `analysis.generate` repair fields are correct.
    - The first answer's sentinel is in no audit event and no analysis row.
- `internal/ai/corpus/analyst_eval_test.go` (`make ai-eval`):
  - Each refused unsafe answer's re-ask is built as the job builds it.
  - New gate `recheck_follow_up_echoes_untrusted_text` (0).
  - New check `recheck_follow_up_lists_every_issue_with_its_rule` (9 cases).
  - New check `recheck_not_asked_after_the_clients_refusal` (1 case: ANA-010's answer is schema-invalid, so the client refuses it before the check).
  - `checks/ai-evals/fake-analyst.json` is regenerated; the other three reports are unchanged.
- `prompts/embed_test.go`: the `reground` template's key words.
- `apps/web/tests/analysis-revised.test.tsx`:
  - The line is shown once in en and in zh-Hant, with no `lang` of its own and no axe violation.
  - No line when the flag is absent.
  - Mutation check: with the condition replaced by `false`, the 4 flag tests fail (2 projects x 2 languages).

## 5. NOT RUN and risks

- **Any live call.** Not measured:
  - Whether Meta's endpoint takes the analyst's prior assistant turn. Its adapter sends the same user, assistant, user conversation as H-057's repair, which was also NOT RUN live for the analyst role.
  - Whether the re-ask fixes the live refusals, and what it costs. An explanation's input is about 37,000 tokens, and the re-ask sends it again plus the prior answer, so a refused explanation costs about twice as much.
  - The live smoke after deploy should ask for one explanation of a pair that was refused before (the five of 2026-11-03), and read the audit's `repair` and the log's `call_attempt` 2.
- `make backend-race` in full: NOT RUN. `make integration-test` ran the database packages with `-race`.
- Screenshots: NOT RUN locally (only `E2E_SCREENSHOTS=1` in CI's container). No gallery sample was changed, so no baseline should move. The new line shows only when the API sets the flag, which no fixture or gallery sample does.
- Time: a refused explanation now takes up to two analyst calls (each up to 300 s, the cap), so up to about twice as long. The worker sets no per-attempt bound on a job (the lease is renewed by the heartbeat). An investigation's own deadline still ends it with `time_limit`.
- ~~H-061 §12's check that an investigation still runs happens at the attempt's start, not again before the second call.~~ Fixed in review round 1 (§7): the re-ask reads it again first. The proposal repair (H-057) still reads it only at the attempt's start; that gap predates this slice and is recorded as open in H-061 §13.

## 6. For the coordinator

- Record H-086 (the owner's three decisions, quoted in §1) in `docs/DECISIONS_AND_TBD.md`; this slice did not edit it.
- Record the prompt versions in §1: `reground-0.1.0` is new, `analyst-0.3.3` is unchanged, and the re-ask's version is `analyst-0.3.3+reground-0.1.0`.
- Record the re-ask rule chosen in §1 (grounding refusals only; failed first calls as before; a stop before the re-ask keeps the refusal final; a failed second call keeps the first refusal, not retried), or ask the owner if they want a different rule.
- The analyzer goldens' rule ("never rewritten") was broken on purpose for one case; see §2.

## 7. Review round 1

Four findings, all minor. Each was checked against the code; all four were fixed.

| # | Finding | Verified | Fix |
|---|---|---|---|
| 1 | `graph.go` (recheck_context): the re-ask is a second paid call made without reading the investigation again. A stop does not cancel the analysis job's context, and 0019's `check_investigation_spend` refuses only past USD 2.50, not after a stop, so a re-ask could be sent after the person stopped the investigation (H-061 §12, §13). | Yes. `investigations.LabelOf` ran only in `analyzer.start`. The new DB test (below), run with the hook switched off, got the investigation `stopped`, the analysis `completed` and **2** analyst requests. | `analysis.Input.MayAskAgain func(ctx) (bool, error)`, asked once in `recheck_context`, after the stop check and before the follow-up is built. False: no re-ask, no record, the first refusal stands (as after a stop). An error ends the graph as the new kind `RecheckUnchecked` (the first answer's issues and the first call's metadata), which the analyzer maps to the first refusal, `ANALYSIS_UNGROUNDED`, not retried, with an ERROR log line (none when the attempt stopped). The analyzer sets it only for a labelled analysis: `stillRuns` runs `investigations.LabelOf` in a `hr.Checkpoint` (a short transaction under the job's lease; the graph still reads no storage). A labelled analysis without the read asks nothing more (fails closed). An investigation that has ended is logged at INFO ("the analysis's investigation has ended; its refused answer is not asked again"). A person's analysis is unchanged: no hook, no extra transaction. |
| 2 | H-061 §12's cap row still said at most 10 delegated calls. | Yes. | The row says at most 14 (3 proposals × 2 with H-057's repair, plus 4 analyses × 2 with H-086 (R)'s re-ask; research at most once). The estimates list says the USD 0.8 and 1.9 figures predate H-086 (R) and that a refused explanation costs about twice as much. |
| 3 | The same stale 10 in H-056's table, and H-061 §13's "Running child jobs either complete inertly or fail fast before their paid call", which the re-ask broke. | Yes. | H-056's table says ≤ 14. §13 now says the stop does not cancel a running child job's context and that the re-ask reads the investigation again first. Its sentence "the database refuses every labelled write and every ledger insert" was also corrected: no trigger on `ai_spend_reservations` (0016's two, 0019's `check_investigation_spend`) and nothing in `internal/storage/aispend` checks the investigation's status, so a ledger insert is bounded only by the ceiling and the planner-turn count. The proposal repair has the same gap from H-057 (verified: `generator.start` is its only `LabelOf`, and `repair.Plan` checks only `ctx.Err()`). It is recorded in §13 as **open, for the owner**, with the analysis graph's fix as the pattern. It is not changed here: it is H-057's code, outside this slice. |
| 4 | ARCHITECTURE.md still described the analysis graph without the re-ask branch, with `analyze`'s signature "kept" and one log line. | Yes. | The entry lists `ground ↳ recheck_context → generate_2 → ground_2` (no cycle, at most one re-ask), says `analyze` returns `analyzed` (the analysis, the first call's metadata and the re-ask's record), names the injected pre-re-ask check, says there is one log line per call (`call_attempt` 2 on the re-ask), and links this report. AI_SPEC §9's H-086 (R) paragraph, which said the check "stays at the attempt's start", was corrected too. |

Tests added (each fails with the hook switched off in `recheck_context`, checked by mutation):
- `internal/aigraph/analysis` `TestTheReaskAsksWhetherItMayFirst`:
  - The hook is asked once, after the first call, under the attempt's context. "It may": the re-ask as before, 2 requests and 2 reservations. "It may not": ungrounded with the first answer's issues and the first call's metadata, no record, 1 request, 1 reservation, 1 settlement. "It cannot tell": `RecheckUnchecked` wrapping the hook's error, the same otherwise, and its text names only the kind.
  - The hook is not asked for a grounded first answer, a failed first call, or an attempt that stopped before the re-ask.
- `internal/analyses/analyzer` `TestAnInvestigationsAnalysisIsAskedAgainOnlyWhileItRuns`:
  - It still runs: the re-ask's analysis.
  - It has ended: the first refusal, not retried, 1 request, the INFO line.
  - The read fails: the same, with the ERROR line.
  - No read on a labelled analysis: the same, no extra line.
  - A person's analysis is never asked about it.
- `internal/analyses/analyzer` `TestStillRunsReadsTheInvestigationUnderTheLease`:
  - `LabelOf`'s query for the analysis (organization and ID) in one Checkpoint, with a fake transaction.
  - A query error, a stopped attempt (`handler.ErrStopped`) and a lost lease (`jobs.ErrLeaseLost`) are errors, never "still runs".
- `internal/proposals/effect` `TestAnExplanationIsNotAskedAgainOnceItsInvestigationStops` (database, the real API and worker logins):
  - An investigation proposes, saves and runs a what-if, then queues an explanation through `analyses.Queue` in its acting-for scope.
  - The analyzer's first analyst call gets an ungrounded answer while the requester stops the investigation (`investigations.Stop`) during that call: one analyst request; the analysis is `failed` with `ANALYSIS_UNGROUNDED`; its `analysis.attempt_failed` event has no `repair` field.
  - The same without the stop: two requests, `completed`, `analysis.generate` `repair` grounded.
  - With the hook switched off, the stopped case fails: the analysis was `completed` after 2 analyst requests.
- `internal/proposals/effect/db_test.go`: `env` keeps the worker login's scope (`wscope`) so a test can run a loop of other handlers.

No assertion was weakened or skipped; no golden changed (no golden case is an investigation's analysis).

Residual risk: the read and the second call's reservation are two steps. A stop that commits in the milliseconds between them is not seen (the call up to 300 s before it is). The proposal repair's gap (finding 3) is unchanged.

Gates of review round 1 (run in the worktree; toolchain go1.27.3, the Python venv from `requirements-spec.txt` in the session scratchpad):

| Command | Exit | Result |
|---|---|---|
| `make spec-check` (run again after the last doc edit) | 0 | every part PASS, among them `PASS_LIMITED_SPEC_CHECKS`, `PASS_REGISTER_SHAPE_ONLY`, `PASS_TS_CONTRACTS_FRESH` (17 files, 166 types), `PASS_DB_TESTS_FRESH` (18 files), `PASS_STATIC_DEPLOY_CHECKS`; `checks/docs-check.json` regenerated (one more relative link) |
| `make backend-test` | 0 | 104 packages ok, 0 failed, 11 without tests; `scripts/check-architecture` ok (519 s). Database tests skip here; `make integration-test` runs them. |
| `make integration-test` on a fresh disposable `dt_test_h086f2` (PostgreSQL 16.15 started by `initdb` in the session scratchpad, trust auth, 127.0.0.1:56491 only, no Unix socket, `fsync=off`; dropped and stopped afterwards) | 0 | `DB migration smoke up/down/up completed` and the SQL tests; `db-go-test` (`GEMINUS_REQUIRE_DB_TESTS=1`, `-race`, `-p 1`) 46 packages ok, 0 failed, none skipped, among them `internal/proposals/effect` (the new test) and `internal/analyses/analyzer`. Before it, on `dt_test_h086f1`, the new DB test passed with `-race`, and failed with the hook switched off as described above. |
| `make ai-eval` | 0 | `TestFakeProviderEval`, `TestReviewEval`, `TestAnalystEval`, `TestInvestigatorEval` PASS; fake results only, no report changed; the live eval is NOT RUN. |
| `make frontend-test`, `make e2e-test` | NOT RUN | `apps/web` was not changed in this round. |
| `make backend-race` in full; any live provider call | NOT RUN | |
