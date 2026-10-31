# H-054 validation report: the photo prompt says side-by-side machines are one step (photo-0.3.0)

**Date: 2026-10-31 | Status: agent self-check; no independent review yet; no live call | Decision: H-054 (owner, 2026-10-31: 「Fix the prompt (Recommended)」: the photo prompt, a new prompt_template_version, says identical machines side by side doing one step are one process step whose resource capacity is their count, never a decision node or a split; today's `AI_OUTPUT_INVALID` is diagnosed; merged only if the live runs pass, by 11-01), with T-056, H-033, H-052, D-131, D-132 kept | Requirements: REQ-008 and REQ-009 (an AI proposal a person reviews), AI_SPEC §4–§5 (the server reviews; no fabricated number)**

## 1. Cause analysis

Data: what the server stores of a refused proposal (cleaned issues: a code, a path of known tokens, a fixed sentence; no answer text, by design) and the two run logs that recorded the proposal page (`checks/evidence/2026-10-27-T-057-live-workflow/workflow-singapore.log`, `checks/evidence/2026-10-28-live-photo-and-polish/bare.log`). No answer text exists anywhere, so every cause below is an inference that the tests in section 3 reproduce exactly, not an observation of what the model wrote.

### 1.1 `ROUTING_INVALID` at `/nodes/45` (four of the six photo proposals that reached Muse Spark 1.3)

Both recorded cases have the same six issues: "no source reaches this step" (`UNREACHABLE_NODE`, a warning) at `/nodes/41` to `/nodes/45`, and "the outgoing routing probabilities are not valid" (`ROUTING_INVALID`, the one error) at `/nodes/45`.

- The base of both had 41 nodes: the electronics template (`/nodes/0`–`/nodes/40`). The "bare line" case of 2026-10-27 (`bare.log`) was on version 1 of `mdl_01M5Z03CX94C22T9DX9P2YXE4X`, whose draft patch to the bare line had failed (`PATCH_TEST_REQUIRED`), so that version was the full template too. On the 4-node bare line, an index of 45 would need 42 added nodes; all four cases were almost certainly on a 41-node template.
- So the model added five nodes (41–45) that no source reaches, and the last one's outgoing flows are invalid. In `internal/model/validate/ir.go` `routing`, `ROUTING_INVALID` at a node's own pointer is a decision whose probabilities do not sum to 1 (or that has no outgoing flow), a non-decision with other than one outgoing flow, or a process with more than one upstream queue.
- The photo prompt `photo-0.2.0` says every number must be one the base model has, "單一路徑的機率為 1" (a single route's probability is 1), and only a resource's capacity may count the segments. Faced with the floor plan's ICT 1 and ICT 2 side by side, a split decision with probability 1 on each route sums to 2: `ROUTING_INVALID`. Any other split probability (0.5) is no number of the base, which `review.Photo` refuses. So under the photo rules a split can never pass; the only acceptable shape is the templates' (one ICT step whose `ict_tester` resource has capacity 2, `internal/domainpack/electronicsline/templates/electronics_assembly_test_line.json`, and the demo's reference answer `testdata/photos/electronics-line-demo.proposal.json`).
- Reproduction (`TestSideBySideTestersAreNoSplit`): on the template, "ICT 2" copied from the template's ICT (its buffer, step, result decision 0.95 / 0.05 and failure, every number the template's) plus a split before ICT 1 and ICT 2 with probability 1 on each route, the flow into ICT left unchanged, gives exactly the six live issues, in the same order, with `ROUTING_INVALID` at `/nodes/45` the only error. On the bare line, the split joined to the line is `ROUTING_INVALID` at the split alone; with 0.5 / 0.5 the model is valid and the photo rule refuses both routes' probability; one ICT step with two testers is ready and passes the photo rule.

Hypothesis confirmed as far as the stored data can confirm it: the prompt left a split as the natural reading of "two testers side by side" and gave it no number it could take.

### 1.2 `AI_OUTPUT_INVALID` at `/changes` (2026-10-31, `mdl_01M5Z03CX94C22T9DX9P2YXE4X`)

The worker's line `ai provider call ended ... code="AI_INVALID_OUTPUT" output_tokens=8026` says the provider client refused the answer (`provider.CodeInvalidOutput`), so the outcome is `review.InvalidOutput(issues)` in `internal/ai/photopipeline`, cleaned by `review.Clean`. The stored issue was exactly one: code `AI_OUTPUT_INVALID`, path `/changes`, sentence "the provider's answer is not an acceptable proposal" (the first sentence of the code, which replaces any message the catalogue does not have).

Every code path that can produce an issue of that code, checked against the three facts (call code `AI_INVALID_OUTPUT`, path exactly `/changes`, the first sentence):

| Path | Issue it gives | Matches? |
|---|---|---|
| `meta.answer`: stop reason other than `end_turn` / `max_tokens` / `refusal`, a tool or unknown block | no issue; `review.invalid` adds one at `""` | No: path `""` (and `max_tokens` would be `AI_OUTPUT_TRUNCATED`) |
| `meta.answer`: answer from another model | path `""` | No |
| `meta.decoded` (`structured.Projection.Decode`): a patch value that is not one strict JSON text | path `/changes/<i>/value`, which `Clean` keeps (indexes and schema members) | No |
| `provider.Client`: empty or over the output bytes, does not decode, web search | path `""` | No |
| `provider.Client` full-schema check, a patch operation's failure (op, path pattern or length, members) | `/changes/<i>/...`, kept by `Clean` | No |
| `provider.Client` full-schema check, **`changes` is `[]` with status `proposed`** (canonical `minItems: 1`) | `/changes`, message replaced by the first sentence | **Yes** |
| `provider.Client` full-schema check, `changes` not an array | `/changes` | Yes, but only if the API does not enforce the requested format |
| `provider.Client` full-schema check, more than 1,000 operations (`maxItems`) | `/changes` | Yes in shape, impossible in 8,026 output tokens |
| `review.outputIssues` / no-op / `review.Photo` at `/changes` | their own fixed sentences (`sentChanges`, `sentNoOp`, `sentPhotoSteps`), and the call code would be `OK` | No |

Ranking for a Muse Spark photo answer of about 8,000 output tokens:

1. **Most likely: `status: "proposed"` with `"changes": []`.** The format the request sends (`output_config.format`, the projection of `proposal-output.schema.json`) drops `minItems` (the structured-output subset takes none), so the API's format allows an empty patch; the canonical check then refuses it at `/changes`. The model of 2026-10-31 was most probably asked on version 3 of `mdl_01M5Z03CX94C22T9DX9P2YXE4X`: on 2026-10-28 a photo proposal on its version 2 (the bare line) was approved and created version 3 with AOI, ICT (2 testers), FCT and packing (`bare5.log`). On that base the floor plan shows nothing the model does not have, and photo-0.2.0 says nothing about that case (needs_input only when the photo shows too little). Most of the 8,026 tokens would be reasoning (Muse Spark counts reasoning tokens as output). Reproduced by `TestAProposalWithNoChangeIsRefusedAtChanges` (the Meta adapter over a local server) and `TestAnEmptyPhotoProposalIsRefusedAtChanges` (the worker's photo path: the stored issue and the log code are exactly the live ones).
2. Unlikely: `changes` written as something other than an array (for instance the whole patch as one JSON-text string, imitating the encoded values), which needs the API not to enforce the format.
3. Excluded in practice: over 1,000 operations.

To confirm (1) without any answer text: the proposal page says "asked ... on version N"; if N is 3 (or any version with every station of the floor plan), cause (1) is near certain.

## 2. The change

| Part | What | Where |
|---|---|---|
| Photo prompt, zh-Hant (same language as the file; English-first rewrite out of scope, H-054) | `photo-0.2.0` → **`photo-0.3.0`**. After the numbers rule, one sentence: identical machines side by side doing one step (two ICT testers side by side in the photo) are one process step: one node, one resource, the resource's capacity their count, as the templates model two ICT testers; when the base model already has that step, they are that step, not a new one. Never a decision node for them, never a split of the flow between them: a split has no probability to take (1 per route sums to more than 1, any other is no number of the base), and the server refuses it. In the needs_input paragraph: also needs_input when every station the photo shows is already in the base model with nothing to change, and a `proposed` answer has at least one change, never an empty `changes`. Nothing else in the file changed; the numbers rule is kept and the new sentence names no number but the count. | `prompts/photo/system.md` |
| Server photo rules, English (sent after the template, as for photo-0.2.0) | The same two rules, domain-neutral (the aicontext package may not name a pack's vocabulary: `TestNoDomainVocabulary`): "Identical machines side by side doing the same step ... are one process step: one node that requires one resource whose capacity is their count, as the domain's templates model several machines of one step; when the base model already has that step, it is that step ... Never add a decision node or split the flow between them ..."; and "... or everything it shows is already in the base model with nothing to change, answer needs_input ... A proposed answer has at least one change; its changes are never empty." | `internal/ai/aicontext/photo.go` (`photoRules`) |
| Version pins | `photo-0.2.0` → `photo-0.3.0` in the tests that pin the template version (aicontext, generator, aievals' photo smoke, the API's DB test). Not changed: `internal/proposals/generated_by_test.go` (`photo-0.2.0`) and `internal/audit/audit_test.go` (`photo-0.1.0`) are stored-metadata samples of older rows, not pins of the current template. No hash of the prompt file is pinned anywhere (searched the repository; `scripts/check_hashes.py` does not cover prompts). | `internal/ai/aicontext/photo_test.go`, `internal/proposals/generator/photo_test.go`, `internal/aievals/photo_test.go`, `internal/api/photo_test.go` |
| Docs | AI_SPEC T-056 paragraph (the template is photo-0.3.0, what it adds); ELECTRONICS_LINE §13 (a paragraph on H-054); this report. | `docs/AI_SPEC.md`, `docs/domains/ELECTRONICS_LINE.md` |

Unchanged: the concepts, `review.Photo`, `review.Review`, the validator, the projection, the schemas, the API, the web, the migrations. The server's behaviour is the same; only what the vision model is told changes.

Prompt size: the template grows from 4,044 to 4,738 bytes and the English rules by about 650 bytes, about 400 more input tokens on a photo prompt of about 6,700 (an estimate, not counted). The live reservation follows the count (count_tokens + 10%, H-037), so nothing else needs to change.

## 3. Tests (written before the prompt change; the prompt test failed until it changed)

- `internal/ai/review/photo_test.go` `TestSideBySideTestersAreNoSplit` (new): the live pattern on the template (section 1.1: exactly the six live issues, `ROUTING_INVALID` at `/nodes/45` the only error, every issue clean); the split joined to the bare line, probability 1 a route (`ROUTING_INVALID` at the split, `/nodes/4`, the only error); 0.5 / 0.5 (valid model, then `review.Photo` refuses `/flows/3/routing/probability` and `/flows/4/routing/probability` with its sentence); one ICT step with two testers (ready, no photo issue). This pins why the prompt must ask for the templates' shape: the server takes no other.
- `internal/ai/provider/meta/messages_test.go` `TestAProposalWithNoChangeIsRefusedAtChanges` (new): the projection the request sends drops `proposal-output.schema.json#/oneOf/0/properties/changes/allOf/1/minItems`; a Messages answer (`end_turn`) proposing `"changes": []` is `invalid_output`, not retryable, with one issue `AI_OUTPUT_INVALID` at `/changes`; the error carries none of the answer's text.
- `internal/proposals/generator/photo_test.go` `TestAnEmptyPhotoProposalIsRefusedAtChanges` (new): the worker's photo path (fake segmenter and vision model through the checked clients) on the demo's answer with an empty patch: the outcome is invalid with exactly `[{AI_OUTPUT_INVALID, /changes, "the provider's answer is not an acceptable proposal", error}]`, storable, and the call's operational line says `AI_INVALID_OUTPUT` (the live signature), with no answer text in the log.
- `internal/ai/aicontext/photo_test.go` `TestThePhotoRulesMakeSideBySideMachinesOneStep` (new; red before the change: version `photo-0.2.0` and 16 missing phrases): the prompt is `photo-0.3.0`; the English rules say "side by side", "are one process step", "capacity is their count", "as the domain's templates model", "it is that step", "Never add a decision node or split the flow", "already in the base model", "at least one change"; the zh-Hant template says 並排, 是同一個 process 步驟, capacity 是機台的數量, 就是那個步驟, 不得為它們新增 decision node, 不得把流向分給它們, base model 都已經有, changes 至少要有一個修改; neither names `0.5` or `50%`. `TestThePhotoRulesReadAFloorPlansShapes` now pins `photo-0.3.0`.
- Every existing golden and assertion kept; none weakened, none skipped. The fake eval reports `checks/ai-evals/fake-*.json` are byte-identical after `make ai-eval`.

The fake suite cannot show whether a live model now follows the rule: that is the live check of section 6.

## 4. Commands and results

Run on 2026-10-31 in the worktree (Go 1.27.3 from the module cache; Python from the main checkout's `.venv`, symlinked into the worktree and not committed).

| Command | Exit | Result |
|---|---|---|
| `go test -count=1 -run 'TestThePhotoRules' ./internal/ai/aicontext/` before the prompt change | 1 | FAIL as intended: template `photo-0.2.0`; 8 English and 8 zh-Hant phrases missing |
| `go test -count=1 ./internal/ai/aicontext/ ./prompts/` after it (first try) | 1 | FAIL `TestNoDomainVocabulary`: the first English wording named ICT; reworded domain-neutral |
| `go test -count=1 ./internal/ai/aicontext/ ./prompts/` | 0 | ok, ok |
| `go test -count=1 ./internal/ai/... ./internal/aievals/... ./internal/proposals/... ./prompts/... ./internal/api/...` | 0 | 20 packages ok (`internal/api`'s DB tests skip without a database: NOT RUN below) |
| `PATH=.venv/bin:$PATH make spec-check` | 0 | contracts, planning, docs, SQL inventory, reference math, decisions, TS contracts, hashes, DB-test render, deploy: `"errors": []` |
| `make ai-eval` (fake provider) | 0 | `TestAnalystEval`, `TestFakeProviderEval`, `TestReviewEval` PASS; reports unchanged |
| `make backend-test` (gofmt, `go vet`, `go test` of every package, `GEMINUS_FULL_DIFFERENTIAL=1`) | 0 | gofmt clean, `go vet` clean, 86 packages ok, 15 without test files, no FAIL (DB tests skip without a database) |
| `PATH=.venv/bin:$PATH make spec-check` again, after the docs and this report | 0 | `"errors": []`; check_docs PASS, 162 Markdown files, 305 relative links |

## 5. NOT RUN

- Any live call: SAM 3.1, Muse Spark 1.3 (`make ai-smoke-photo-live`, the live UI). By the task's rule; the coordinator runs them after review. Whether the model now follows the rule is unmeasured.
- `make integration-test` (disposable PostgreSQL): no database here; `internal/api/photo_test.go`'s DB tests (which now pin `photo-0.3.0` in the stored metadata) were skipped by `dbtest` ("NOT RUN: set GEMINUS_TEST_DATABASE_URL").
- `make backend-race`, `make e2e-test`, `make frontend-test` (no web file changed).
- An independent review of this change.

## 6. Recommended live check (the coordinator's; each photo proposal about USD 0.07–0.3)

1. `make ai-smoke-photo-live` twice (the demo floor plan on `testdata/photos/bare-line.json`, the worker's pipeline, USD 1 cap each): pass = `ready`, gates 0, one ICT step whose resource has capacity 2 (the report's `new_process_steps` 4).
2. Live UI, one or two runs on a model whose **latest version is the bare line** (4 nodes). `mdl_01M5Z03CX94C22T9DX9P2YXE4X` probably is not any more (version 3 has the stations); create a model from `testdata/photos/bare-line.json` instead. Pass = ready, no `ROUTING_INVALID`.
3. One run on `mdl_01M5Z03CX94C22T9DX9P2YXE4X`'s latest version (first read its version number on the proposal page of 2026-10-31): pass = `needs_input` (or a valid proposal), not `AI_OUTPUT_INVALID`.
4. Optional, one run on a model from the template (41 nodes): pass = needs_input or a valid proposal, not `ROUTING_INVALID`.

About 5 runs, under USD 2 in all (H-054). Merge only if they pass.

## 7. PROPOSED (not decided; the owner's)

- **P1 (implemented in the prompt; reversible by deleting one clause each):** "when the base model already has that step, they are that step, not a new one" and "a photo that shows only what the base model has is needs_input". Both go a little beyond H-054's sentence: the first because all four `ROUTING_INVALID` cases were on the 41-node template, where ICT already exists; the second because it is the likely cause of today's `AI_OUTPUT_INVALID` and the task asked for a rule that prevents it. The server's rules do not change, and a needs_input answer changes nothing.
- **P2 (not implemented): the uncleaned issue's keyword in the worker's log.** The stored issue cannot tell `minItems` from `type` at `/changes`, because `provider.Client` replaces every schema leaf's message with "does not match <schema>". Proposal: keep the failing schema keyword (a server constant from the validator's error kind: `minItems`, `type`, `pattern` ...) on each issue of an `invalid_output`, and log at most 20 `{path (cleaned by review.Clean's known tokens), code, keyword}` in a WARN line after "ai provider call ended"; never a message or answer text. Not tiny (the provider client, the generator, the analyzer's line, their tests), so left for a later slice.
- **P3 (not implemented): the review's own sentence for an empty patch.** With P2's keyword, `review.InvalidOutput` could map a `minItems` failure at exactly `/changes` to the existing sentence "a proposal has at least one change" (`sentChanges`), so the proposal page says why instead of the generic sentence.

## 8. Risks

- The fix is a prompt: a live model may still split the testers or answer an empty patch. The server stays safe either way (it refuses both, as before); only the pass rate is at stake. Section 6 measures it.
- The causes are inferred from cleaned issues; no answer text is stored (by design). The reproductions match the stored issues exactly, which is strong but not proof of what the model wrote.
- "A step the base model already has is that step" may lead the model, on the template, to propose only capacity changes the floor plan suggests (for instance FCT 3 → 1, since the plan draws one FCT tester). The photo rules allow a capacity from 1 to the box count; a person reviews and approves every proposal.
- The needs_input clause could make the model answer needs_input on a base that has only some of the stations; the clause says "every station the photo shows" to limit that. Watch run 2 of section 6.
- About 400 more input tokens a photo prompt (estimate), a few percent of its cost.
