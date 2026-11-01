# H-061 A9 validation report: optional nullable members in the planner's shown schema, the decoder's null fill, and a refusal's hint (investigator-0.2.0)

**Date: 2026-11-01 | Status: agent self-check, no independent review yet; no live call; everything behind `GEMINUS_INVESTIGATIONS` (off on live) | Decisions: H-060, H-061 (approved by the owner in H-063), H-055 (no reasoning, effort or output-budget change: none here) | Slice A9 live finding of [docs/design/H-061_BACKGROUND_INVESTIGATOR.md](../docs/design/H-061_BACKGROUND_INVESTIGATOR.md) (§7 tools, §9 prompt role), on top of 016ae31 (tool_choice auto) | Requirements: REQ-008 (AI output decoded strictly, refused, never repaired silently), REQ-019 (a closed tool list, server-side checks), REQ-021 (invalid mutation = 0, AI leakage = 0)**

## 1. The finding

After 016ae31, a live investigation (planner Muse Spark 1.3, scenario mode) ended `invalid_answer` after two planner turns. Both were refused `INVALID_ARGUMENTS` at `/from_suggestion`.

The coordinator's probe, with the real `prompts/investigator/system.md` and the registry's tools, showed the cause. Spark wrote `propose_what_if {"from_suggestion": "null", "idea": "..."}`: the string `"null"`, not JSON null, for an idea of its own. The idea itself was good.

Two things led to it, and one kept it going:
- The input schema the model was shown (`investigator.ProviderTools` → `InputSchema`) required every member, with null where optional (H-061 §7). For example, `from_suggestion` was `anyOf [{analysis_id, index}, null]` and required.
- `finish`'s `best_scenario_id` and `explain_results`' `question` had the same shape.
- A refusal's observation (`investigator.ErrorObservation`) carried only `{code, path, budget}`, so the model repeated the mistake.

## 2. What changed

| Part | What it does | Where |
|---|---|---|
| Shown schema | `InputSchema` leaves each optional member out of `required`. An optional member is one the full schema requires that may be null (`investigator.Optional`: an `anyOf` with the null schema, or a type list naming null; top level only). Each optional member gets its tools.md description; no other schema node carries one. The full schema is unchanged. | `internal/ai/investigator/planner.go` |
| Optional members | `propose_what_if.from_suggestion`, `explain_results.question`, `finish.best_scenario_id`. Below the top level every member of every tool is required and none may be null; a test holds this, so the top-level fill is complete. | `Optional` |
| Decoder fill | `Decoder.DecodeCall` (provider `ToolDecoder`, used by `PlanTurn`'s `planAnswer`) runs `withNulls` before decoding. If the input is exactly one JSON object as `jsontext` reads one by default (valid UTF-8, no duplicate member, nothing after it), each optional member it leaves out is added as `"<member>":null` before the closing brace. Any other input is passed through unchanged. Then it decodes against the full, unchanged schema. | `planner.go` |
| What the fill does not touch | Bytes of members that are present are never touched. Nothing is coerced. `Decode` (the full schema's decoder) is unchanged and still refuses a left-out member: the executor, the transcript's ledger check and `Encode` use it. | `tools.go` |
| 8 KiB bound | Now applied to the input as the model wrote it (`decode(..., written)`), so a fill never turns an 8 KiB call into a refusal. A schema-valid canonical form stays under 8.1 KiB (idea 2000 runes × 4 bytes, plus members). | `tools.go` |
| Hint | `ErrorObservation` is now `{is_error, error {code, path}, hint, budget}`. `hint` is one fixed English sentence per `InvestigationToolCode` (`investigator.Hint`, 15 sentences). It is built from the code alone and never holds anything a call held. `isErrorObservation` (the transcript's ledger check) requires the code's own hint. | `tools.go`, `transcript.go` |
| Contract | `ErrorObservation` gains `hint` (string, 1..300, required). An `allOf` of 15 `if error.code = C then hint = const` rules pins each code's sentence, so a stored observation with another code's hint or free text is refused. The file's `$comment` records that the shown schema leaves nullable members optional and that the decoder fills them. Argument `$defs` are unchanged. | `schemas/investigator.schema.json` |
| Fixtures | `valid-investigator-observation-error.json` (hint added); `invalid-investigator-observation-http-code.json` (hint added, so its only fault stays `/error/code`). New: `valid-investigator-observation-invalid-arguments.json` (the live refusal at `/from_suggestion`), `invalid-investigator-observation-no-hint.json` (`""`), `invalid-investigator-observation-hint-of-another-code.json` (`/hint`), `invalid-investigator-observation-hint-free-text.json` (`/hint`). All are listed in `cases.json`. | `testdata/contracts/` |
| TS types | `ErrorObservation.hint: string` (regenerated). The typed contract test's `refusedObservation` gains the fixture's hint. | `apps/web/lib/contracts/investigator.ts`, `apps/web/tests/contracts.test.ts` |
| Tool text | `propose_what_if`, `explain_results` and `finish` each say, in their own description, to leave the member out when it is unused and never to write it as the string null. Each optional member has a `### <member>` section ("Omit it when ...; Never write it as the string null."). | `prompts/investigator/tools.md` |
| Member parser | `prompts.MemberDescriptions` reads the `### <member>` sections. `ToolDescriptions` keeps its signature; a tool's text stops at its first member heading. Malformed files are refused: a member heading outside a tool, a member described twice, an empty member, a tool with only members, another heading level, or a member name of free text. `ProviderTools` refuses tools.md if it describes a member that is not optional. | `prompts/embed.go` |
| System prompt | The DIGEST line now names a refusal's hint. Two rules are added: a member that may be null is optional (leave it out, never the string null); a refused or failed step's result holds the server's own fixed hint, and a refused call is never repeated unchanged. | `prompts/investigator/system.md` |
| Version | `investigator-0.1.0` → `investigator-0.2.0` in system.md and tools.md (which must match), `investigations.PromptTemplateVersion` (stored with each new investigation; a new test holds it equal to the template's), the pins in `prompts/embed_test.go`, `internal/ai/aicontext/investigation_test.go` and `internal/aievals/investigator_test.go`, the four investigator prompt goldens, and `checks/ai-evals/fake-investigator.json`. | see §5 |
| Docs | `docs/AI_SPEC.md`: the Tools and Transcript lines. Comments in `provider.ToolDecoder` and the Meta turn adapter. No request bytes change. | |

**Kept from A5 and A6.** `ErrorObservation` is still the only shape of a refused or failed row's observation. It now includes the hint, and the transcript refuses a ledger without it. A step's arguments are the decoder's canonical form; an invalid turn stores no arguments. The digest and the replay show a result only through the context's sanitizer: the hints pass through it unchanged (resume golden). No AI answer text is stored or logged.

## 3. Tests first (red, before any product change)

| Command | Result |
|---|---|
| `go vet ./prompts/ ./internal/ai/investigator/ ./internal/ai/provider/meta/ ./internal/ai/aicontext/ ./internal/investigations/` | build failed: `undefined: MemberDescriptions` (prompts), `undefined: prompts.MemberDescriptions` (investigator), `undefined: investigator.Hint` (aicontext) |
| `go test -count=1 ./internal/contracttest/ ./internal/investigations/ ./internal/ai/provider/meta/` | FAIL. contracttest: `valid-investigator-observation-error: want valid, got [members not allowed: hint error]`, the invalid-arguments fixture likewise, `invalid-investigator-observation-no-hint: schema negative accepted`, two `no issue at "/hint"`. investigations: `stored "investigator-0.1.0"` fails the 0.2.0 pin. meta `TestTheLiveNullAnswer`: `from_suggestion left out: ... invalid_output`, `best_scenario_id left out: ... invalid_output`, `required [idea from_suggestion]`, `[run_id question]`, `[outcome best_scenario_id]`. The string-"null" case already passed (it is refused today, and still is). |
| `python scripts/check_contracts.py` | `AssertionError: Unexpected outcome for valid-investigator-observation-error ... 'hint' was unexpected` |

The new and changed tests:
- `internal/ai/investigator/nullable_test.go`:
  - `TestShownSchemaLeavesNullableMembersOut`: every registry tool's shown `required` is the full schema's minus its nullable members, each with tools.md's description and nothing else described. The optional members are exactly the three above. No nested object has an optional member. The shown schema takes `invalid-investigator-missing-nullable-member.json`, which `Decode` still refuses.
  - `TestDecodeCallFillsAbsentNullableMembers`: a left-out `from_suggestion`, `question` or `best_scenario_id` decodes to exactly the explicit null's canonical form, and the executor's `Decode` of that form gives it back. Also covered: spacing, an escaped member name, edit mode's `finish`, and a given suggestion left untouched.
  - `TestDecodeCallRefusesAsToday`: 34 cases, listed below.
  - `TestDecodeCallBoundsWhatTheModelWrote`: a call of exactly 8,192 written bytes that leaves `from_suggestion` out is taken; 8,193 bytes is refused at `""`.
  - `TestHints`: the Go table equals the schema's 15 `if`/`then` pairs, one per code, each one printable-ASCII sentence of at most 300 bytes. Every code's observation carries its hint within 6 KiB. The live refusal's observation holds none of the call's text and equals the new fixture.
  - `TestDecodeCallConcurrently`: 16 goroutines under `-race`, run first so `Optional`'s `sync.Once` initializes under contention.
- `TestDecodeCallRefusesAsToday` cases. Each is refused with the code and path that `Decode` gives today for the same call with the left-out member written as null (the explicit null the shown schema required):
  - present and wrong: `from_suggestion` as `"null"`, `{}`, without index, of a run ID, with a member of its own, as text, `false`, index `1.0`; `best_scenario_id` as `"null"` or a run ID; `question` as `7` or `""`;
  - wrong elsewhere, with a nullable member left out: no idea, no outcome, a line break, an outcome of its own, a member named by the model, run settings, an idea not in NFC;
  - not one clean JSON object: a duplicate member, trailing data, two objects, invalid UTF-8, a lone surrogate, not closed, not JSON, `null`, `"null"`, an array, nothing, over 8 KiB;
  - another mode's tool, an unknown tool.
- **The test's oracle.** It was first written as "the same as `Decode` of the same input". The NFC case showed that this was the wrong oracle. `{"idea":"café"}` with `from_suggestion` left out used to be refused at `""`: the missing member was the deepest schema issue, and the NFC check runs only after the schema passes. With the fill, the non-NFC idea is refused at `/idea`, exactly as when the model writes `"from_suggestion":null`. The present-and-wrong member is still refused and nothing is coerced. Only the left-out member's own issue is gone. The oracle is now stated per case as the explicit-null call. For every schema-level refusal the two oracles agree, because removing a `""` issue never changes the deepest path.
- `investigator_test.go`:
  - `TestProviderTools`: "no `description` anywhere" became "exactly as many `description`s as tools.md describes members for the tool", with no `$ref`. This is narrowed, not weakened: `TestShownSchemaLeavesNullableMembersOut` checks where each description is and what it says.
  - `TestErrorObservation`: the exact bytes, with the hint, equal the fixture.
  - `TestNewTranscriptRefuses`: three new cases. An error result without its hint, with another code's hint, or with free text is refused.
- `prompts/embed_test.go`:
  - the 0.2.0 pin;
  - the three tools say to leave their member out;
  - `TestMemberDescriptions`: exactly three members, each "Omit it when ..." and "Never write it as the string null.";
  - `TestToolsDocRefuses`: 9 malformed files;
  - the template mentions `hint`, "Never repeat a refused call unchanged", "leave out a member you do not use" and "string null".
- `internal/ai/provider/meta/plan_test.go` `TestTheLiveNullAnswer`: through the Meta adapter and a local test server.
  - The live answer (`"null"`) is an invalid turn `INVALID_ARGUMENTS` at `/from_suggestion`.
  - A left-out `from_suggestion` or `best_scenario_id` is the call with null, canonical.
  - The sent tools' `required` are `[idea]`, `[run_id]`, `[outcome]`, `[proposal_id]` and `[scenario_id]`, with the optional members described.
  - Every sent schema passes `meta.SchemaViolations`.
- `internal/ai/aicontext/investigation_test.go`: the resume digest's invalid step shows its hint; the 0.2.0 pin.
- `internal/investigations/investigations_test.go` `TestPromptTemplateVersionIsTheTemplates`: the stored version is the template's.

## 4. Commands and results (after the change)

Toolchain: `go1.27.3 linux/amd64` (`~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin`). `.venv` is a symlink to `/home/kuoba123/Geminus/.venv` and is not committed.

| Command | Result |
|---|---|
| `go test -race -count=1 ./internal/ai/investigator/ ./internal/ai/provider/... ./internal/investigator/ ./internal/aigraph/... ./internal/aievals/ ./internal/contracttest/ ./prompts/ ./internal/ai/aicontext/ ./internal/investigations/` | exit 0. 18 packages `ok`: investigator 1.9s, provider 2.9s, anthropic 46.5s, fake 1.3s, meta 24.0s, structured 2.5s, internal/investigator 15.4s, aigraph 22.8s, analysis 5.7s, einomodel 1.6s, aigraph/investigator 1.2s, photo 1.7s, proposal 13.3s, aievals 35.6s, contracttest 2.5s, prompts 1.1s, aicontext 21.0s, investigations 1.7s |
| `go test -race -count=1 -run Concurrently -v ./internal/ai/investigator/` (after adding `TestDecodeCallConcurrently`), then `go test -race -count=1 ./internal/ai/investigator/` | `PASS TestDecodeCallConcurrently`, `PASS TestDecodeConcurrently`, ok; then `ok ... 1.592s` |
| `go test -count=1 -v ./internal/ai/investigator/ ./prompts/` | all 23 + 4 tests PASS (the new ones above included) |
| `gofmt -l cmd internal schemas scripts prompts` | no output |
| `go vet` on the same packages as the race run | no output, exit 0 |
| `GEMINUS_WRITE_PROMPT_GOLDENS=1 go test -count=1 -run TestInvestigationGoldens ./internal/ai/aicontext/`, then `go test -count=1 ./internal/ai/aicontext/` | ok, ok (only the four investigator goldens were rewritten; §5) |
| `make ai-eval` | exit 0. `TestAnalystEval`, `TestFakeProviderEval`, `TestReviewEval`, `TestInvestigatorEval` PASS. Only `checks/ai-evals/fake-investigator.json` changed (§5). |
| `make architecture-check` | `PASS architecture: 115 packages, 41 rules, 16 build configurations` |
| `PATH=<worktree>/.venv/bin:/usr/local/bin:/usr/bin:/bin make spec-check` (the venv first on PATH, spelled out) | exit 0. contracts `PASS_LIMITED_SPEC_CHECKS` (positive fixtures 125 → 126, negative 227 → 230, with exact error path 222 → 225); planning, docs PASS; sql inventory `PASS_STATIC_INVENTORY_ONLY`; reference math; engine reference vectors; decisions `PASS_REGISTER_SHAPE_ONLY`; `PASS_TS_CONTRACTS_FRESH` (17 files, 165 types); `PASS_INDEPENDENT_HASHES`; `PASS_DB_TESTS_FRESH`; `PASS_STATIC_DEPLOY_CHECKS`. Run again after this report: see §4.1. |
| `.venv/bin/python scripts/gen_ts_contracts.py --check` | `{"status": "PASS_TS_CONTRACTS_FRESH", "files": 17, "types": 165}` |

### 4.1 After this report and its index row
Rerun after writing this file: `make spec-check` (as above) exit 0 with the same statuses, and `gen_ts_contracts.py --check` `PASS_TS_CONTRACTS_FRESH` (17 files, 165 types). `checks/docs-check.json`: markdown files 189 → 190, relative links 400 → 402.

## 5. Goldens and the eval: what changed and why

**The four investigator prompt goldens** (`internal/ai/aicontext/testdata/investigation/{scenario,entry-b,edit,resume}.golden`):
- They change only through the template:
  - the InputHash;
  - the version line;
  - the DIGEST line ("code, path and hint");
  - the two new rule lines.
- The resume golden's digest also shows the hint of its two refused steps, PROPOSAL_DECIDED and UNKNOWN_TOOL, unchanged by the sanitizer.
- The other roles' prompt goldens did not change and were not rewritten.

**`checks/ai-evals/fake-investigator.json`**: `prompt_template_version` `investigator-0.1.0` → `investigator-0.2.0`, and nothing else:
- the same six cases, 6 of 6 passed, the same steps, codes and gates;
- the scripted fake turns write explicit nulls, which the decoder takes as before;
- the eval records no tool schema, description or observation text.

Cases were deliberately not added. The coordinator's rule is that the eval changes only where the shown schema or descriptions change; the new behavior is covered by the unit and Meta-adapter tests in §3. `fake-provider.json`, `fake-review.json` and `fake-analyst.json` were rewritten by `make ai-eval` byte-identical.

**Unchanged:**
- Meta's request goldens (`internal/ai/provider/meta/testdata/requests/*`: Muse Spark and Muse Wonder modeler, scenario, analyst, importer, research and repair), with no diff;
- every other role's template, rules and InputHash;
- the `Complete` path;
- the planner's request fields (tool_choice auto with no parallel calls, max_tokens 8,192, no thinking or effort).

**Version strings deliberately left at investigator-0.1.0.** These record an investigation that was created under that template, not the current pin, so they are data, not pins (the H-054 photo bump left its data the same way):
- the contract fixtures and OpenAPI examples of investigation records;
- the storage, worker, executor and database test rows (each writes and reads back its own value);
- `apps/web/app/ui-gallery/investigation/samples.ts` and the typed `contracts.test.ts` investigation;
- the A2 to A8 reports and the design doc;
- the hand-sealed test prompt label in `internal/aigraph/investigator/agent_test.go` (system text "rules", not the template).

## 6. NOT RUN

- Any live AI call. The live planner eval (`cmd/ai-eval-live -suite investigator`) and the coordinator's A9 closed loop were not run. Whether Muse Spark 1.3 now leaves `from_suggestion` out, or writes JSON null, is not measured.
- The database-backed tests, which skip without a database: `internal/investigator` `TestTheJobOnTheDatabase`, `TestEditModeEndsWithTheDraftOnTheDatabase`, `TestADraftWritesProposalIsReadInItsOrganization`, `TestAResumeSendsNoCallTwice`, `TestAStaleLeaseCannotRecordACall` and `TestTheGeneratorFailsFastOnAnEndedInvestigation`. Also not run: `make integration-test`, `make db-test`, and `db/tests/*.sql` on a disposable PostgreSQL. No migration changed: the ledger's observation column does not check the observation's shape.
- `make backend-test` and `make backend-race` (full suites), `make frontend-test` (lint, typecheck, unit: includes the edited `apps/web/tests/contracts.test.ts`), and the browser suite. These were not run by the coordinator's test policy; the coordinator runs the full suites after the merge.
- No independent review.

## 7. PROPOSED (for the owner; nothing here is decided)

1. **H-061 §7 "Arguments" line.** It says "every member required, with null where optional". The contract, the ledger, the executor and `Decode` still hold that. Only what the planner is shown differs: there, a member that may be null is optional and described, and the planner turn's decoder fills it. A one-line amendment of §7 would record this. The design doc was not edited, because it is approved.
2. **The string "null" as a `question`.** `explain_results.question` is any string of 1 to 500 characters, so `"question":"null"` is valid today and stays valid: the analyst is asked the question "null". The tool text tells the model never to write it, but the server does not refuse it. Refusing the literal would be a contract change (the argument `$defs`), and it is out of this slice's "refused exactly as today" rule. It is not done.
3. **Translations of the hints.** The hints are model-facing English, like the template; zh-Hant comes later with the template (H-061 §9). Nothing a person sees shows them: the investigation view and the API do not expose observations.

## 8. Risks

- **Deploying with an investigation in flight** (only with `GEMINUS_INVESTIGATIONS` on; it is off on live).
  - A refused or failed row observed by the old worker has no hint. When the new worker resumes it, `NewTranscript` refuses the ledger (`INVESTIGATION_LEDGER_INVALID`) and the investigation ends `ai_failed`. This fails closed and never builds a silent wrong transcript.
  - The reverse also holds: an old worker that reads a new-shape observation refuses it the same way during a deploy overlap.
  - Mitigation: deploy when no investigation is running. Investigations last at most 20 minutes.
- **The template version of a resumed investigation.** An investigation created under 0.1.0 and resumed by a 0.2.0 worker is sent the 0.2.0 template, while its row still says 0.1.0. This was already true of any template change and is not introduced here; recorded for A9's closed loop.
- **Model behavior.** That Muse Spark omits, or writes null, after this change is a hypothesis, not a measurement (§6). With tool_choice auto it may still answer without a call; that remains an invalid turn as before.
- **Hints as text in a result.** The hint sits in an `untrusted_data` DIGEST block, beside AI text. The system prompt says the hint is the server's own sentence, but a model could still discount it as data. No model can forge it: the observation is built by the worker from the code, the path and the budget, and the schema pins each code's sentence.
- **Coverage of the fill.** `Optional` sees a nullable member only as an `anyOf` with the null schema or a type list with null. A future `$ref` to a nullable definition would not be seen as optional. `TestShownSchemaLeavesNullableMembersOut` would then fail on that member's description or `required` and stop the change. The same holds for a nullable member below the top level, which the fill does not cover and the test refuses.
