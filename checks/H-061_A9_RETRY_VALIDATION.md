# H-061 A9 validation report: a failed step's tool is not called again (investigator-0.4.0), the analyst's output bound, and three operational findings

**Date: 2026-11-01 | Status: two independent reviews, no High/Medium; the live planner suites run on the reviewed code | Decisions: H-055 (no reasoning cut: the analyst's bound is raised, never lowered), H-061 (approved in H-063), H-066 (the worker in Singapore) | Slice A9 of [docs/design/H-061_BACKGROUND_INVESTIGATOR.md](../docs/design/H-061_BACKGROUND_INVESTIGATOR.md), on top of b2115b1 | Requirements: REQ-008, REQ-019, REQ-021**

## 1. The findings

All of these come from the live closed loop on the deployed site (prompt investigator-0.2.0) and from the six-case live planner suite (`cmd/ai-eval-live -suite investigator`).

1. **Meta answered 404 to one Singapore host.** After the cf28675 deploy, every Meta call from the worker got 404 `not_found_error`: planner turns, and a plain scenario proposal too. The same key answered 200 from elsewhere. A plain `railway redeploy --service worker` (same build, new host) fixed it. This is recorded in `docs/runbooks/deploy.md` §7.1, committed in b2115b1 with the deployer's TCP proxy fix.
2. **The planner called a failed tool again.** In the 0.2.0 suite (Muse Spark 1.3, 3 of 6 passed, US$0.39), `explain_results` failed `SERVICE_UNAVAILABLE` (the case's recorded results had run out, as a real outage would). Spark called it again, and once more. Three failed steps in a row then ended three cases `invalid_answer`. The hint said only "not the call's fault". The prompt's rule named refused calls, not failed ones, and the prompt never said what ends an investigation.
3. **The analyst's answers were cut at 8192 output tokens.** In the live Wonder-planned investigation (inv_01M6AK27FMNGBDXS6873XAKN1F), all four explanations were `AI_OUTPUT_TRUNCATED`: Muse Spark 1.3, about 37k input tokens, `output_tokens=8192`. Wonder then asked again "with a narrower question". An explanation a person asked for on the same line used 6,649 of the 8192.

## 2. What changed

| Part | What it does | Where |
|---|---|---|
| Hints | `SERVICE_UNAVAILABLE`, `DATASET_STORAGE_FAILED` and `INTERNAL` now end "(not the call's fault); do not call that tool again: use another offered tool or finish." Each is still one sentence under `TestHints`' rules. The schema consts and the Go map change together. | `internal/ai/investigator/tools.go`, `schemas/investigator.schema.json` |
| Prompt rule | "Never repeat a refused or failed call unchanged. Two invalid turns in a row, or three refused or failed steps in a row, end the investigation without a result: after a failed step, use another offered tool or finish with what you have." This matches `inARow` (handler.go), the live loop's counters (loop.go) and the eval's (aievals). | `prompts/investigator/system.md` |
| Version | investigator-0.2.0 → investigator-0.4.0, together in system.md, tools.md, `investigations.PromptTemplateVersion` and its pins. 0.3.0 had the hint ending "do not call it again" plus the rule; it was run live (§4) and never deployed. | as A9's first slice |
| Analyst bound | An analyst call gets `ImageMaxOutputTokens` (32,000) and the image path's scaled timeouts (300 s at the defaults). The reservation follows, because the meter prices `req.MaxOutputTokens`. The analyst cannot take the repair, research or slow-model paths (each is refused at seal or acceptance), and a planner turn overrides it afterwards. | `internal/ai/provider/provider.go`, `budget.go` comment |
| Docs | `docs/AI_SPEC.md` (the budget line, the template version); the design doc's "three refused or failed steps". | |

## 3. Tests first (red)

| Command | Result before the change |
|---|---|
| `go test ./internal/ai/investigator/ -run TestAFailedCallIsNotRepeated` (new) | FAIL: the three hints without the suffix; `investigator-0.2.0` |
| `go test ./internal/ai/provider/meta/ ./internal/ai/provider/anthropic/ -run Golden` (analyst-0.json set to `max_tokens` 32000) | FAIL `TestRoleRequestGoldens/analyst`: got 8192 |

## 4. Live results (synthetic cases; the key on stdin via the environment, never printed)

| Run | Prompt | Planner | Cases passed | Pass bar | Cost | Turn median / p95 / max |
|---|---|---|---|---|---|---|
| 01M6AK7ZGR75KM5RAYFXFE58V1 | 0.2.0 | Muse Spark 1.3 | 3 of 6 (three `invalid_answer`) | no | US$0.39 | 2.8 / 8.0 / 10.7 s |
| 01M6AKQMG051HZWP2F813453RR | 0.3.0 | Muse Spark 1.3 | 5 of 6 (INV-006 `invalid_answer`) | no | US$0.42 | 3.2 / 8.6 / 10.4 s |
| 01M6AKVGGR0XRW23C3GVBZ2DYF | 0.3.0 | Muse Wonder 1.0 | 5 of 6 (INV-006 `invalid_answer`) | no | US$1.16 | 4.4 / 17.6 / 21.1 s |
| 01M6AMN6S8VX5NKEDTXK7W4EGX | 0.4.0 | Muse Spark 1.3 | **6 of 6** | **yes** | US$0.42 | 3.5 / 8.3 / 12.6 s |
| 01M6AMRS1G0TY6ZQXNBVH535JN | 0.4.0 | Muse Wonder 1.0 | **6 of 6** | **yes** | US$1.20 | 4.1 / 10.0 / 10.1 s |

Under 0.3.0, INV-006 went: explanation failed, then a new what-if (right), which failed, then another new what-if, so three in a row. The planner read "it" as the call, not the tool. Under 0.4.0, every case that hit a failure moved to another tool or finished.

Reports: `checks/ai-evals/live-investigator-*.json`, with `checks/ai-evals/live-spend.jsonl` holding the open and close line of each run.

Live closed loop on the site (0.2.0, before this change):
- Spark scenario: 12 steps, 3 what-ifs, best FCT 3 → 4 + ICT 2 → 3, p50 lead time −10.0%.
- Edit mode on the empty line: `draft_written` (revision 4) in 1.2 min.
- Wonder scenario: `finished_by_ai` in 9.5 min, 14 steps, best FCT 3 → 5, −9.8%, with the four truncated explanations of §1.3.

## 5. Commands and results (after the change)

| Command | Result |
|---|---|
| `go test -count=1 ./internal/ai/... ./internal/investigations/... ./internal/aievals/... ./prompts/... ./internal/investigator/... ./internal/analyses/... ./cmd/ai-eval-live/... ./cmd/worker/` | all ok |
| `GEMINUS_WRITE_PROMPT_GOLDENS=1 go test -run Investigation ./internal/ai/aicontext/` | ok. Only the four investigator goldens changed: the hash, the version line, the rule line. |
| `make ai-eval` | exit 0. Only `fake-investigator.json`'s template version changed. |
| `make spec-check` | exit 0 |
| `scripts/gen_ts_contracts.py --check` | `PASS_TS_CONTRACTS_FRESH` (17 files, 165 types) |
| `make architecture-check`, `go vet`, `go test -race` on the touched packages, `make backend-test` and `make integration-test` (throwaway PostgreSQL 16) | see the commit message |

**Goldens changed by hand, only where the bound changed.** Meta's and Anthropic's `analyst-0.json` (`max_tokens` 8192 → 32000). The Eino parity golden `internal/analyses/analyzer/testdata/analyze-goldens.json`: only its 8 `max_output_tokens` and the one metered charge (179,448,000 → 483,000,000 nano-USD: 3 attempts × (20,000 × 1,250 + 32,000 × 4,250)). It was not rewritten, so everything else it pins still holds.

## 6. NOT RUN

- The analyst's new bound live. Whether explanations now complete, and what one costs, is measured after the deploy (§8).
- A live Wonder-planned investigation on 0.4.0 (H-061 §14 step 4 on this prompt). The one on 0.2.0 passed.
- The browser suite for this change (no UI change here).

## 7. Risks

- **Deploying with an investigation running.** A stored failed step's hint must equal `Hint(code)` (transcript.go). An investigation in flight that has a failed `SERVICE_UNAVAILABLE`, `DATASET_STORAGE_FAILED` or `INTERNAL` step stored under the old text, resumed by the new worker, ends `ai_failed`. Deploy when no investigation runs.
- **The analyst's reservation is larger.** It is about US$0.8–0.95 per live explanation (the byte-based quote), up from about US$0.5–0.65; it settles at actual use. Inside an investigation, the US$2.50 ceiling refuses a 4th explanation only if settled spend has passed about US$1.6. The realistic settled spend is US$1.0–1.3. A refusal costs a step, not the investigation.
- **A hung provider** can now hold one analysis for about 15 of the investigation's 20 minutes (3 attempts × 300 s), before about 6.
- **Meta's 404 per host** can come back on any deploy. The runbook's check after each deploy covers it.

## 8. After the deploy (b126549, 2026-11-01)

Done:
- The deploy verified 200 on /healthz and /readyz, the web answered, and no TCP proxy was left open.
- The new worker host reached Meta.
- An explanation a person asked for completed with 10,052 output tokens (64 s, US$0.089). The old 8192 bound would have cut it.
- A Spark-planned investigation on 0.4.0 (inv_01M6AQ5EKGFY5Z4ZM8SZBHXXYK) ended `finished_by_ai` in 13.0 min. Its three explanations completed with 13,509, 16,428 and 13,778 output tokens, each in 88 to 104 s.

So an investigation now takes longer: 13 min here, against the 20-minute limit. A slower one ends `time_limit`, which is a normal end, not a failure.

Still to do:

- Probe Meta from the new worker host.
- Start one investigation with each planner, and check that the explanations complete (worker log: role analyst, no `AI_OUTPUT_TRUNCATED`).
- Then record H-061 §14 step 4 for Muse Wonder in [H-061_PLANNER_EVAL.md](H-061_PLANNER_EVAL.md).
