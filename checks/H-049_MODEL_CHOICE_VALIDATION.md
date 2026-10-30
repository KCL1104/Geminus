# H-049 validation report: a person chooses Muse Spark 1.3 or Muse Wonder 1.0 for a proposal

**Date: 2026-10-31 | Status: agent self-check, not reviewed; no live call | Decision: H-049 (owner, 2026-10-30), with H-029, H-033, H-038, H-046 (2) and D-007, D-110, D-111 kept | Requirements: REQ-008, REQ-009, REQ-022 (proposals and their language), TEST-033 (browser checks)**

## 1. What the owner decided

H-049: the owner asked whether modeling should use Muse Wonder, Meta's most capable model, and answered 「不然就讓 user 可選就好」 ("then just let the user choose"). A person asking the AI for a proposal (a change to the model, a model from a photo or floor plan, a scenario, with or without research) chooses its model: **Muse Spark 1.3** (`muse-spark-1.3`, the default: faster, costs less) or **Muse Wonder 1.0** (`muse-wonder-1.0`: Meta's most capable model, in preview, slower, costs more). Explanations of results stay on Muse Spark 1.3. The choice is per request and recorded with the proposal (`generated_by.model`). The default and the wording are the coordinator's choices, kept as given.

Facts used (from H-049 and the coordinator's brief; nothing was probed again, no live call was made): the team's key lists `muse-wonder-1.0`; Standard mode only, in preview; USD 4.00 / 0.20 / 20.00 per million input / cached input / output tokens (Muse Spark 1.3: 1.25 / 0.15 / 4.25); web search USD 2.50 per 1,000 queries; same rate limits; the coordinator's probe with `web_search` and `output_config` returned Muse Spark's block shapes in 117 s.

## 2. Design

| Part | What it does | Where |
|---|---|---|
| Contract | `common.schema.json#/$defs/ProposalModel`, a closed enum of the two exact IDs. `ProposalRequest.model` (optional; absent is the deployment's model). `createImageProposal` takes an optional `model` form field before the file. createProposal's 503 now has its own description and an `AI_MODEL_NOT_APPROVED` example; a request example `with_wonder`. Fixtures: `valid-proposal-request-model`, `invalid-proposal-request-model-unknown` (`muse-spark-1.3-contributor`, path `/model`). TypeScript contracts regenerated. | `schemas/`, `openapi/openapi.yaml`, `testdata/contracts/`, `apps/web/lib/contracts/` |
| Approval | The H-029 record lists `ProposalModels: muse-spark-1.3, muse-wonder-1.0`. A record's choice must contain its `Model` (the default) and name no model twice (`record_incomplete`), and each model must be a Standard one (`model_not_standard`); `muse-wonder-1.0` joins `metaStandardModels` (Standard mode only, per the models page as H-049 records it). An Anthropic record lists none. `Approval.AllowsModel(role, model)`: the approval's model for any role it serves; a listed model for the modeler and scenario roles only, never the analyst. The live gate is off unless every listed model is priced. | `internal/config/aiapproval/aiapproval.go`, `internal/config/ai.go` |
| Spend | `muse-wonder-1.0` priced at USD 4.00 / 0.20 / 20.00 per million (cache writes at the input rate), search USD 0.0025 a query, source `dev.meta.ai/docs/pricing-rate-limits` read 2026-10-30. The meter refuses a quote whose model is not the one the request asks for (nothing reserved or sent), so a reservation is always at the chosen model's price. | `internal/ai/spend/spend.go`, `internal/ai/provider/meter.go` |
| Provider boundary | `ProposalContext.Model` and `Request.Model` ("" is the backend's own). `provider.ModelChooser` (`AcceptsModel(role, model)`); the client hands a request that names a model to no other backend, nor to one that may not ask it for the role: `model_not_approved`, not retryable, before any I/O. A metered answer from another model than the one asked for is `invalid_output` (its usage settled). The analyst has no model field. | `internal/ai/provider/model.go`, `provider.go` |
| Meta adapter | One backend per approval, as before (its own model is still the approval's `Model`); `AcceptsModel` is the approval's `AllowsModel`. The requested model is quoted, counted (a photo), sent, and the only one whose answer is taken; any other is refused in `Quote`, `count` and `Complete` before any I/O. No second backend or client, so the per-organization concurrency limit and the one ledger are unchanged. | `internal/ai/provider/meta/meta.go` |
| Fake | Accepts a chosen model for the proposal roles and answers as itself (`model` "fake": it never claims to be the chosen model); `Requests()` shows the model asked for. | `internal/ai/provider/fake/fake.go` |
| API | `Service.Models` / `Service.PhotoModels` from the settings alone (`cmd/api` `proposalModels`: the fake, the contract's two; a live provider that is on, its approval's list; otherwise none). A model not listed: 503 `AI_POLICY_NOT_APPROVED`, `details[0]` `{path: /model, code: AI_MODEL_NOT_APPROVED}`, nothing stored. A photo's model field is checked before the file is read (unknown: 422 at `/model`). The job payload carries `model`; `proposal.create` records `requested_model` (catalogue: one of the two IDs). The photo fingerprint names `model` only when sent, so a request without it has the fingerprint it had. | `internal/proposals/`, `internal/api/api.go`, `cmd/api/main.go`, `internal/audit/audit.go` |
| Worker | The job's `model` (checked against a model ID's shape) goes into `ProposalContext.Model`, and for a photo into `photopipeline.Request.Model`; a vision client that cannot ask it fails the proposal before the segmenter spends anything. `AI_MODEL_NOT_APPROVED` is shown as `AI_POLICY_NOT_APPROVED`. | `internal/proposals/generator/`, `internal/ai/photopipeline/` |
| UI | The "Ask AI for a change" panel gains a labelled React Aria `Select`, **Model**, on Muse Spark 1.3, with a line under it: "Faster, and costs less. The default." / "Meta's most capable model, in preview: slower, and costs more." It applies to what is sent in words and to the photo button. Under an explanation's suggestions, an editor gets the same choice ("Model for a proposed scenario") for every "Propose as a scenario". Only Muse Wonder adds `model` to a request (`modelField`), so a request on the default is byte for byte what it was. `AI_MODEL_NOT_APPROVED` shows "This deployment does not offer the model you chose. Choose Muse Spark 1.3 and send again." `ai.model.muse-wonder-1.0` names the writer ("Muse Wonder 1.0 (Meta)"). English first, zh-Hant translated; no hard-coded string. | `apps/web/features/ai-proposals/model-choice.tsx`, `ask-form.tsx`, `photo-form.tsx`, `features/results/analysis.tsx`, `messages/` |
| Landing | The Meta section lists Muse Wonder 1.0 beside Muse Spark 1.3 and SAM 3.1, with one line; the intro says three models; the Muse Spark and web search lines no longer say "every proposal" / "Muse Spark" only. No screenshot changed. | `apps/web/features/landing/landing.tsx`, `messages/` |
| e2e fake API | Accepts `model` (one of the two, else 422 at `/model`) and answers as written by that model (`generated_by.model`, with research its searches too). | `apps/web-e2e/fake/servers.mjs` |
| Persistence | No migration: the choice is frozen in the job's payload (as `research` is), recorded in the audit event, and the model that answered is in the row's provider metadata (`generated_by.model`). Tenant scope, RLS and the immutable proposal row are untouched. | (none) |

### Timeouts (D-007's 300 s is the cap)

Muse Wonder took 117 s for one small research question in the coordinator's probe, more than the 90 s default attempt. So a call that names it gets a research call's timeouts, `Budget.ResearchTimeout` (scaled by the research output bound over the budget's, at most `BudgetCaps.Timeout`, D-007's 300 s job timeout), for each attempt and for the whole call: 300 s and 300 s at the defaults, never more, whatever the deployment's budget. Its output bound and retries stay the call's own (8,192 tokens, 2 retries inside the one 300 s deadline). A research call or a photo already had these bounds on Muse Spark, so they are unchanged there. Muse Spark's plain calls keep 90 s / 120 s.

### Reservations at the defaults

| Call | Reservation |
|---|---|
| Muse Wonder, words, no research | (prompt bytes + schema + 4,096) × USD 4.00/M + 8,192 × USD 20.00/M per attempt, × 3 attempts (about USD 1.3 for a 60 KB prompt) |
| Muse Wonder, research | one attempt: 1,048,576 × USD 4.00/M + 32,000 × USD 20.00/M + 64 × USD 0.0025 = **USD 4.994304** (Muse Spark: USD 1.60672) |
| Muse Wonder, photo | (count + 10%) × USD 4.00/M + 32,000 × USD 20.00/M per attempt, × 3 |

The Muse Wonder research reservation is just under D-110's default USD 5 organization day, so it is refused (`AI_QUOTA_EXCEEDED`) once the organization has committed more than about USD 0.006 that day. See section 6.

## 3. Requirement mapping and tests

| Requirement | Tests (negative first) |
|---|---|
| Unknown model is a validation error | contract fixture `invalid-proposal-request-model-unknown` (`/model`); `TestThePhotoModelField` (a photo's unknown model, 422 at `/model`; sent twice refused); `TestProposalModelChoice` and `TestPhotoModelChoice` (DB, 422 at `/model`); e2e fake API |
| A model the approval does not list is refused by the server, never swapped | `TestH049ModelChoice`, `TestMetaGapReasons` (choice rules), `TestTheAPIsGateIsTheWorkers` (an unpriced model to choose keeps live AI off); `TestModelRefusal` (503 body equals the OpenAPI example); `TestAPIProposalModels` (Claude live lists none); `TestAModelGoesOnlyToABackendThatMay`; `TestAModelTheApprovalDoesNotListIsNeverSent` (Meta: no request sent, nothing spent, the analyst refused); `TestAnAnswerFromAnotherModelThanTheChosenOneIsRefused`; `TestAChosenModelIsReservedAtItsPrice` (a quote of another model: nothing reserved or sent; a swapped answer refused); `TestTheChosenModelIsAsked` (a backend without a choice: `AI_MODEL_NOT_APPROVED`, final, shown as `AI_POLICY_NOT_APPROVED`); `TestThePhotoAsksTheChosenModel` and `TestThePipelineAsksTheChosenVisionModel` (refused before the segmenter is asked); worker `TestTheMetaProviderIsTheConfigured` (Claude's client takes no choice) |
| Muse Wonder's reservation at Muse Wonder's rates | `TestTheMuseWonderPrice`; `TestAChosenModelIsReservedAtItsPrice` (text: 3 × the Wonder bound; research: USD 4.994304; settled at its price); `TestTheQuoteAndTheCountAreOfTheChosenModel` (the photo's count and reservation at Muse Wonder's price); `TestAChosenModelIsSentAndAnswers` (spend at its price) |
| Default is Muse Spark | `TestAChosenModelIsSentAndAnswers` (no model: `muse-spark-1.3` sent); unit "sends no model on the default"; e2e research and keyboard tests (their request bodies unchanged) |
| Muse Wonder chosen: the backend is called with it and generated_by says so | `TestAChosenModelIsSentAndAnswers` (request body `model` and metadata); `TestTheChosenModelIsAsked`; `TestFakeTakesAChosenModel`; unit "the proposal names Muse Wonder 1.0"; e2e "chooses Muse Wonder 1.0 with the keyboard, and the proposal names it" |
| Explanations stay on Muse Spark 1.3 | `TestH049ModelChoice` (analyst never Wonder); `TestAModelTheApprovalDoesNotListIsNeverSent` (analyst refused in Quote and Complete); `AnalysisContext` has no model field |
| Slow model within D-007 | `TestASlowModelHasTheResearchTimeouts` (300 s attempt and deadline at the defaults, never more at the caps; Muse Spark unchanged; research unchanged) |
| Recorded with the proposal | `TestTheJobCarriesTheModel`; DB `TestProposalModelChoice` (job payload, `requested_model` audit, the same key with another model is 409) |
| UI: native, keyboard, accessible, both languages | `proposal-model.test.tsx` (label, value, line linked as description, axe clean, en and zh-Hant; photo sends the field before the file; refusal advice); `results-view.test.tsx` (the explanation's choice; a viewer gets none); e2e keyboard test (Enter, ArrowDown, Enter) with axe |

## 4. Commands and results (run in the agent's worktree, 2026-10-31)

| Command | Result |
|---|---|
| `make backend-test` (Go 1.27.3: gofmt, `go vet`, `GEMINUS_FULL_DIFFERENTIAL=1 go test ./cmd/... ./internal/... ./schemas/... ./scripts/... ./prompts/...`) | exit 0; 86 packages ok, 15 without test files, none failed (the DB-gated tests among them skip: section 5) |
| `make frontend-test` (Node 24.21.0: eslint, `tsc --noEmit`, vitest) | exit 0; 58 test files, 1211 tests passed |
| `make e2e-test` (Playwright 1.56.1, Chromium, one worker) | exit 0; 645 tests: 573 passed, 72 skipped (all 72 the screenshot comparisons, NOT RUN without `E2E_SCREENSHOTS=1`), 0 failed; the new "en: chooses Muse Wonder 1.0 with the keyboard, and the proposal names it" passed |
| `make architecture-check` | exit 0; PASS, 101 packages, 26 rules, 16 build configurations |
| `PATH=.venv/bin:$PATH make spec-check` | exit 0; contracts `PASS_LIMITED_SPEC_CHECKS` (82 positive, 159 negative fixtures, 154 with the exact error path; 7 OpenAPI request examples), planning, docs (159 files, 301 links), SQL inventory, reference math, decisions, TS contracts, hashes, DB test render and deploy checks pass; the full OpenAPI validator is NOT RUN (unavailable), as before |
| `scripts/gen_ts_contracts.py --check` | exit 0; `PASS_TS_CONTRACTS_FRESH`, 15 files, 112 types |

## 5. NOT RUN

- **DB-gated tests** (no `GEMINUS_TEST_DATABASE_URL` in the worktree; they skip): `internal/api` `TestProposalModelChoice`, `TestPhotoModelChoice`, and every other DB test in `make db-go-test`. NOT RUN, not claimed.
- **Screenshot comparisons** (`E2E_SCREENSHOTS=1`, CI's pinned container only). The baselines were not regenerated.
- **Any live Muse Wonder call** (no network, no key read). The coordinator's live check (one proposal on each model) is after one independent review.
- `make backend-race`, `make integration-test`, `make ai-eval`: not run for this slice.

## 6. Risks

- **Muse Wonder research vs. the daily caps**: its reservation is USD 4.994304, just under D-110's USD 5 organization day (PROPOSED), so a Muse Wonder research proposal is refused (`AI_QUOTA_EXCEEDED`, shown as RATE_LIMITED) as soon as the organization has committed more than about half a cent that day, and two in one day never fit. The deployment's `AI_ORGANIZATION_DAILY_LIMIT_USD` decides it in practice; this slice changes no cap.
- **Timeouts**: 300 s is the most any call gets (D-007). A Muse Wonder research proposal on a real model prompt may need more than the probe's 117 s; one that runs out is `AI_TIMEOUT`, its attempt settled at its whole reservation (billed or not), and a research job is never retried. A plain Muse Wonder call's retries share the one 300 s deadline, so in practice a slow first attempt leaves none.
- **Not verified for Muse Wonder**: image input (the probe was text with web search; a photo proposal on it may be refused by Meta, `AI_REQUEST_REJECTED`), its context window (quotes use Muse Spark's 1,048,576-token bound, which over-reserves if Wonder's is smaller and is not a ceiling if larger), and whether 8,192 output tokens leave room for its reasoning on a plain proposal (else `AI_OUTPUT_TRUNCATED`).
- **The Claude fallback**: the UI sends `model` only for Muse Wonder. On the H-018 deployment the default request still works (Claude answers, and `generated_by` says so), but the control says Muse Spark 1.3 is the default; choosing Muse Wonder there is refused with its advice.

## 7. Screenshots affected

- `canvas-*` (the model page): the ask panel gains the Model choice and its line. Will change; not regenerated.
- `home-*` (the landing): the Meta section gains Muse Wonder 1.0 and its line, and its intro says three models. Will change; not regenerated.
- The results page has no baseline; the explanation's choice appears only on an explained run.

## 8. PROPOSED (beyond H-049)

- **P1**: a request on the default sends no `model` (absent is the deployment's model), so requests on the default are what they were and the Claude fallback keeps working; only Muse Wonder adds the field.
- **P2**: a model the approval does not list is 503 `AI_POLICY_NOT_APPROVED` with `details[0].code` `AI_MODEL_NOT_APPROVED` at `/model` (as research's refusal is shaped), and the job code `AI_MODEL_NOT_APPROVED` is shown as `AI_POLICY_NOT_APPROVED`.
- **P3**: a Muse Wonder call has a research call's timeouts (300 s attempt and deadline at the defaults), its output bound and retries unchanged.
- **P4**: the audit field `requested_model` on `proposal.create`.
- **P5**: the last choice is not remembered per browser (left out: a stored choice would differ between server and client renders and between screenshot runs).
