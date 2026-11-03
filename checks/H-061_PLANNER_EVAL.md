# H-061 §14 planner evaluation: Muse Spark 1.3 and Muse Wonder 1.0 as an investigation's planner

**Date: 2026-11-01; step 4 for Muse Wonder on 2026-11-03 | Status: steps 2 and 3 pass on investigator-0.4.0 for both planners; step 4 passes on 0.4.0 for both (Muse Spark on 2026-11-01; Muse Wonder on 2026-11-03, inv_01M6G7229PCQAJJG36VG3PCYK7); the finish-reason rubric is NOT_COMPUTED for either | Gates 2 and 3 (Muse Wonder offered as planner): set on 2026-11-03 (H-086): gate 2 in Railway on the API and the worker, gate 3 by the web build of the H-086 commit (live once the web is deployed from it) | Decisions: H-061 §14 (approved in H-063), H-065 (Muse Wonder's budget), H-055 (no reasoning cut), H-086 (the owner: Muse Wonder as planner, after the cut line)**

The coordinator sets gates 2 and 3 (`INVESTIGATION_PLANNER_MODELS` naming `muse-wonder-1.0` on the API and the worker; `NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER=1` in the web build) only after this file records a pass, and then tells the owner. Step 4 itself needs gate 2 (createInvestigation answers 422 at `/planner_model` for a planner the API does not list, and the worker ends it `ai_failed`), so the coordinator set gate 2 for the run with the web's flag off, so that no person could choose Muse Wonder before the pass; the section below says when.

## The steps

| Step (H-061 §14) | Muse Spark 1.3 | Muse Wonder 1.0 |
|---|---|---|
| 1. A1's probes P1–P5 | Run in slice A1, not repeated here. The suites below exercise the replayed digest and `is_error` results on both models. | as Spark |
| 2. The fake investigator suite (`make ai-eval`) | 6 of 6, exit 0 (scripted turns, no model) | — |
| 3. Six live cases (`cmd/ai-eval-live -suite investigator -planner <model>`) | **pass**: run 01M6AMN6S8VX5NKEDTXK7W4EGX, 6 of 6, US$0.42 | **pass**: run 01M6AMRS1G0TY6ZQXNBVH535JN, 6 of 6, US$1.20 |
| 4. One live end-to-end investigation | **pass on 0.4.0** (b126549 deployed): inv_01M6AQ5EKGFY5Z4ZM8SZBHXXYK `finished_by_ai` in 13.0 min, 13 steps, 3 what-ifs, 4 runs, 3 explanations, best SMT 1 → 2 + FCT 3 → 4, p50 lead time −11.1%. Its three explanations completed with 13,509, 16,428 and 13,778 output tokens, each over the old 8192 bound. Earlier, on 0.2.0: a scenario run (12 steps, −10.0%) and edit mode (`draft_written`). | **pass on 0.4.0** (93095a3 deployed; 2026-11-03, the section below): inv_01M6G7229PCQAJJG36VG3PCYK7 `finished_by_ai` in 12.8 min, 12 steps, 3 what-ifs, 3 runs, 2 explanations, best the packing pace (a suggestion), p50 lead time −1.7%. Earlier, on 0.2.0: inv_01M6AK27FMNGBDXS6873XAKN1F (`finished_by_ai`, 14 steps, 9.5 min, −9.8%). |

## Muse Wonder's pass bar on step 3 (run 01M6AMRS1G0TY6ZQXNBVH535JN)

| Bar | Measured | |
|---|---|---|
| ≥ 95% of calls valid after the strict decode | 100% | pass |
| No `AI_OUTPUT_TRUNCATED`, no `invalid_answer` end | none, none | pass |
| No garbled text (valid UTF-8, no U+FFFD, the locale's script) | 0 | pass |
| Median turn ≤ 30 s, p95 ≤ 60 s, none over 120 s | 4.1 s, 10.0 s, max 10.1 s | pass |
| Planner cost ≤ US$1.00 per case, no `spend_cap` end | US$0.20 per case on average | pass |

The cases' ends: INV-001 `finished_by_ai` (improvement_found), INV-002 `finished_by_ai` (no_improvement), INV-003 `finished_by_ai` (improvement_found), INV-004 `draft_written`, INV-005 `finished_by_ai` (needs_person), INV-006 (injection) `finished_by_ai` (no_improvement), with no forbidden call. The history of the earlier runs (0.2.0: 3 of 6; 0.3.0: 5 of 6 for each planner) is in [H-061_A9_RETRY_VALIDATION.md](H-061_A9_RETRY_VALIDATION.md) §4.

The reports: `checks/ai-evals/live-investigator-2026-11-01T040737Z-01M6AMN6S8VX5NKEDTXK7W4EGX-muse-spark-1.3.json` and `checks/ai-evals/live-investigator-2026-11-01T040934Z-01M6AMRS1G0TY6ZQXNBVH535JN-muse-wonder-1.0.json`.

## Step 4 for Muse Wonder on investigator-0.4.0 (2026-11-03, H-086)

The owner, asked whether "Act on it" can use Muse Wonder, answered 「B：A＋平行測試」; after the parallel design (h086-par) modelled 13 → 10-12 min for about 25 agent-hours, 「先不做 (Recommended)」 for the parallel part, and for Muse Wonder as planner 「可以，全部由你執行 (Recommended)」: set the settings, run the live check, and on a pass build the web with the Planner choice. On 2026-11-01 this step had answered 429 `INVESTIGATION_DAILY_LIMIT` (the organization had started its 10 investigations of the UTC day, H-060). The coordinator did, on the live site:

- **Gate 2 for the run.** 07:58 UTC: `INVESTIGATION_PLANNER_MODELS=muse-spark-1.3,muse-wonder-1.0` set in Railway on the worker and the API. The API redeployed at 07:58; the worker did not redeploy on its own and was redeployed at about 08:05 (its lanes running at 08:05:03 UTC, the worker's log). The web's flag stayed off, so no person could choose Muse Wonder.
- **The run.** main 93095a3 deployed (the analyst's re-ask of H-086 (R) is not deployed). createInvestigation as the judge account (an editor), entry B from the saved explanation ana_01M6FTFKR4QQT9AAK124M634N8 ('One more debug operator') on model mdl_01M6E6FE0VAKSJ57WQNPHGCZ1Y ('Start here'), `planner_model` `muse-wonder-1.0` (planner `meta`/`muse-wonder-1.0`, `investigator-0.4.0`), `proposal_model` `muse-spark-1.3`, `output_locale` `en`, run settings 10,080 min, no warmup, 5 replications, seed 1.

| Step 4 check | Measured (inv_01M6G7229PCQAJJG36VG3PCYK7) | |
|---|---|---|
| Ends `finished_by_ai` or `limit_reached`, never `time_limit` | `finished_by_ai`, `ai_outcome` `improvement_found`: created 08:05:24.9Z, finished 08:18:14.9Z (12.8 min of the 20-minute cap) | pass |
| No `invalid_answer` end; the steps | 12 steps, all `done`, no refused step, no `invalid_call` step, no error. Used: 3 of 3 what-ifs, 3 of 3 runs, 2 of 4 explanations, 12 of 14 steps | pass |
| No garbled text in an idea or a note (valid UTF-8, no U+FFFD, the locale's script) | none: every idea, question and note is English; the only character outside ASCII is the em dash (U+2014) | pass |
| Turn time: median ≤ 30 s, p95 ≤ 60 s, none over 120 s | from each step's `requested_at` back to the previous step's `observed_at` (the first: `created_at`), n = 12: median 14.9 s, max 46.5 s (step 9; nearest-rank p95 = 46.5 s), none over 120 s. Each gap holds the planner's turn and the worker's own time around it, so it bounds the turn from above | pass |
| The run within USD 2.50, no `spend_cap` end | the investigation's ceiling (migration 0019's trigger: settled plus open reservations of its planner turns, proposals and analyses, at most USD 2.50) held, and it did not end `spend_cap`. The delegated calls settled USD 0.5485 in all (below); the planner turns' own spend is not in the worker's log lines kept, so it is known only to be at most USD 2.50 − 0.5485 | pass |

The worker's log of the delegated calls (all `meta`; the window is the coordinator's copy of the live log):

| Call | Model | Ended (the provider's latency) | USD settled |
|---|---|---|---|
| Proposal aip_01M6G72V8F4H38VFMCAAT42JSN (step 1) | `muse-spark-1.3` | OK, 22.9 s | 0.0265 |
| Analysis ana_01M6G75ERN5J7Z7F2HM72V95EC (step 4) | `muse-spark-1.3` | OK, 109.0 s | 0.0900 |
| Proposal aip_01M6G79NGW53WCZXD43JDA2DZW (step 5) | `muse-spark-1.3` | OK, 65.0 s | 0.0392 |
| Analysis ana_01M6G7DTTF9R0KNN0CDH57T1S8 (step 8), attempt 1 | `muse-spark-1.3` | `AI_UNAVAILABLE` after 125.1 s, retried | 0.3078 |
| the same, attempt 2 | `muse-spark-1.3` | OK, 103.6 s | 0.0580 |
| Proposal aip_01M6G7PZ9WSTV66WDAX2Q7QC1X (step 9) | `muse-spark-1.3` | OK, 31.0 s | 0.0270 |

The three runs (sim_01M6G74MD3010SGB6T8KMD6FZT, sim_01M6G7CXHEX7T40B4ECJP6JN6Z, sim_01M6G7RTWGHB43HN7CSN9H54YE) each completed in under 0.1 s of engine time, against the reused baseline sim_01M6E6GRP1B79W7QDKPB5E1QD1. The server's comparison of the three what-ifs on the objective (p50 lead time, lower is better; baseline 1,010.96 min):

| What-if (the idea, in short) | From | p50 lead time | Change |
|---|---|---|---|
| 1. Raise the packing pace | suggestion 1 of the saved explanation ana_01M6FTFKR4QQT9AAK124M634N8 | 993.70 min | −17.26 min (−1.7%), the best |
| 2. Relieve the wait before SMT | suggestion 0 of what-if 1's explanation (ana_01M6G75ERN5J7Z7F2HM72V95EC) | 995.52 min | −15.44 min (−1.5%) |
| 3. SMT line and AOI machine each from 1 to 2 | the planner's own idea | 996.06 min | −14.90 min (−1.5%) |

The closing line: `improved`, candidate 1, compared 3, no draft. Every comparison is comparable, with the `CENSORED_ENTITIES` warning. The planner's `finish` named the packing scenario (scn_01M6G74963AJ86X9H80Y89HM9E), the server's best as well. Muse Spark's step 4 (−11.1%, 2026-11-01) was on another model, so the two gains are not compared. The proposals' contents were not read here; the ideas are the planner's words.

**Gates set.** Gate 2: in Railway as above (07:58 UTC on the API, about 08:05 UTC on the worker), and in the repository by the H-086 commit (`deploy/railway/variables.json`'s API `INVESTIGATION_PLANNER_MODELS`, so that `deploy.sh` does not set it back; the worker's stays the coordinator's, which `scripts/check_deploy.py` keeps out of the file). Gate 3: by the same commit's web build (`NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER` defaults to `1` in `apps/web/next.config.ts`; `""` builds the app without it), live once the web is deployed from it.

The raw record and the log window are the coordinator's (the session's scratchpad, not in the repository).

## NOT RUN

- `finish_rubric` (NOT_COMPUTED in the reports and for step 4): no rubric is scored. H-061 §14's bar names "a finish-reason rubric score at least Muse Spark's"; neither planner has one, so that line of the bar is neither met nor failed: it is not computed. The owner may waive it or score it by hand.
- For step 4: the share of valid planner calls and `AI_OUTPUT_TRUNCATED`. An invalid turn that leaves no step (`no_call`, `invalid_text`, `truncated`) is not in the investigation's record, and the worker's planner-turn log lines were not kept; the run has no `invalid_call` step and did not end `invalid_answer`.
- For step 4: the planner turns' own count and spend (the ledger's `investigator` reservations of the investigation were not read).
