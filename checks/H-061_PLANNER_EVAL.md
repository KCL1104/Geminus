# H-061 §14 planner evaluation: Muse Spark 1.3 and Muse Wonder 1.0 as an investigation's planner

**Date: 2026-11-01 | Status: steps 2 and 3 pass on investigator-0.4.0 for both planners; step 4 passes on 0.4.0 for Muse Spark; for Muse Wonder it passed on 0.2.0 and waits for the next UTC day on 0.4.0 (the organization's 10 investigations a day were used) | Gates 2 and 3 (Muse Wonder offered as planner): not set yet | Decisions: H-061 §14 (approved in H-063), H-065 (Muse Wonder's budget), H-055 (no reasoning cut)**

The coordinator sets gates 2 and 3 (`INVESTIGATION_PLANNER_MODELS` naming `muse-wonder-1.0` on the API and the worker; `NEXT_PUBLIC_GEMINUS_OFFER_WONDER_PLANNER=1` in the web build) only after this file records a pass, and then tells the owner.

## The steps

| Step (H-061 §14) | Muse Spark 1.3 | Muse Wonder 1.0 |
|---|---|---|
| 1. A1's probes P1–P5 | Run in slice A1, not repeated here. The suites below exercise the replayed digest and `is_error` results on both models. | as Spark |
| 2. The fake investigator suite (`make ai-eval`) | 6 of 6, exit 0 (scripted turns, no model) | — |
| 3. Six live cases (`cmd/ai-eval-live -suite investigator -planner <model>`) | **pass**: run 01M6AMN6S8VX5NKEDTXK7W4EGX, 6 of 6, US$0.42 | **pass**: run 01M6AMRS1G0TY6ZQXNBVH535JN, 6 of 6, US$1.20 |
| 4. One live end-to-end investigation | **pass on 0.4.0** (b126549 deployed): inv_01M6AQ5EKGFY5Z4ZM8SZBHXXYK `finished_by_ai` in 13.0 min, 13 steps, 3 what-ifs, 4 runs, 3 explanations, best SMT 1 → 2 + FCT 3 → 4, p50 lead time −11.1%. Its three explanations completed with 13,509, 16,428 and 13,778 output tokens, each over the old 8192 bound. Earlier, on 0.2.0: a scenario run (12 steps, −10.0%) and edit mode (`draft_written`). | passed on 0.2.0 (inv_01M6AK27FMNGBDXS6873XAKN1F: `finished_by_ai`, 14 steps, 9.5 min, −9.8%); **to repeat on 0.4.0** |

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

## NOT RUN

- Step 4 on investigator-0.4.0 for Muse Wonder. createInvestigation answered 429 `INVESTIGATION_DAILY_LIMIT`: the organization had started its 10 investigations of the UTC day (H-060). It runs after 00:00 UTC on 2026-11-02.
- `finish_rubric` (NOT_COMPUTED in the reports): no rubric is scored; the bar above does not include one.
