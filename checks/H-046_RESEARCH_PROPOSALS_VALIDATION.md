# H-046 (2) validation report: research-grounded AI proposals (hackathon slice)

**Date: 2026-10-30 | Status: agent self-check, not acceptance; one independent review is due (H-046), then the coordinator's live run | Decision: H-046 (2) (owner, 2026-10-29), with H-018, H-029, H-033, H-038, H-043 and D-112 kept | Requirements: REQ-008, REQ-009, REQ-022 (proposals and their review), AGENTS.md §2 (AI only suggests) | Live AI: NOT RUN (no request was sent to api.meta.ai or any provider)**

## 1. What the owner decided

H-046 (2): 「要 這個也可以平行做吧？」 to "yes, and approve the data policy change": the modeler and scenario proposals may use the Meta Model API's built-in `web_search` tool (Search grounding), so the description the person typed is used for Meta's web search; the proposal shows the sources it used and the server keeps only sources the call actually opened; the server still validates, computes the risk and a person approves; spend stays under the existing caps. The owner's later reminder: English first (UI copy in English with zh-Hant as the translation; the research prompt searches in English and prefers English-language sources by default; the proposal's prose still follows `output_locale`).

## 2. Design

| Part | What it does | Where |
|---|---|---|
| Contract | `ProposalRequest.research` (boolean, absent is false, per request). `proposal.schema.json` `sources` (`WebSource` items: `url` an https URL of printable ASCII, at most 2048; optional `title`, at most 200; at most 20 items), only with `generated_by.web_search`, never while generating, for invalid or failed. `GeneratedBy.web_search` `{queries, sources_dropped}`. `proposal-output.schema.json` gets an optional `sources` list (URL and title strings, at most 20) in both alternatives: untrusted claims. OpenAPI: createProposal and getProposal descriptions, request examples `in_words` and `with_research`, response example `ready_researched`. TS contracts regenerated | `schemas/`, `openapi/openapi.yaml`, `apps/web/lib/contracts/`, `testdata/contracts/` (11 new fixtures) |
| Approval record | The H-029 record's data policy names `ResearchRoles: modeler, scenario` (H-046 (2)), in the style of H-038's analyst amendment. `Approval.AllowsResearch(role)`. A record that names research for the analyst, the importer, a role it does not serve, or twice, is `policy_unsupported`; an Anthropic record with any research is `policy_unsupported` (the Claude adapter has no web search) | `internal/config/aiapproval/aiapproval.go` |
| Gates (each before any I/O) | API: `researchAvailability` from the settings only (the fake; Meta live with the approval's research for both roles), else 503 `AI_PROVIDER_NOT_CONFIGURED` or `AI_POLICY_NOT_APPROVED`, `details[0].code` `AI_RESEARCH_UNSUPPORTED` at `/research`, nothing stored. Client: a research prompt goes only to a `provider.Researcher` (`research_unsupported`) that accepts the role (`policy_not_approved`). Meta adapter: refuses again in `Quote` and `Complete` (`policy_not_approved`). Seal: research only for the modeler and scenario roles and never with images | `cmd/api/main.go`, `internal/api/api.go`, `internal/proposals/{proposals,http}.go`, `internal/ai/provider/{provider,research,prompt}.go`, `internal/ai/provider/meta/meta.go` |
| Fake and Claude | Claude: not a `Researcher`, so a typed refusal (`research_unsupported`, shown as `AI_PROVIDER_NOT_CONFIGURED` with `AI_RESEARCH_UNSUPPORTED`; the API refuses first). Chosen over "ignore and say no research happened" because the person asked for research and must not get a proposal that silently lacks it. Fake: researches with fixed steps (2 searches, 2 `example.com` test pages opened); its safe answer names those 2 pages and 1 it did not open, so a fake research proposal shows 2 sources and 1 dropped | `internal/ai/provider/fake/fake.go` |
| Data flow | The job payload carries `research: true` (frozen with the job); the `proposal.create` audit event says `research: true` (a flag, never the intent). The worker appends `prompts/research/system.md` (`research-0.1.0`) after the role's rules; the template version names both (`modeler-0.2.0+research-0.1.0`); `Prompt.Research` is sealed into the input hash (left out of the hash when false, so a prompt without research is exactly what it was). The blocks are unchanged: the intent the person typed, already redacted by the context builder, is what the model may search for | `internal/proposals/generator/generator.go`, `internal/ai/aicontext/proposal.go` |
| Meta adapter | A research request adds `"tools":[{"type":"web_search"}]` only (no `user_location`, no `tool_choice`); `max_tokens` 32,000; one attempt. The answer's `server_tool_use` steps named `web_search` are counted (searches apart) and the URLs of `open_page`/`find_in_page` steps kept for the filter; the answer is the last text block, which must come after the last step; narration and reasoning are dropped unread. Another tool, a developer tool call, a tool result block, no answer or an answer before the last step is `invalid_output` (fail closed), its usage and steps billed all the same. A call without research is unchanged (a step in its answer is `invalid_output`, as before) | `internal/ai/provider/meta/meta.go` |
| Source filter | `provider.FilterSources`: a claimed source is kept only when its URL, written as `provider.SourceURL` writes it (https, ASCII DNS host or xn-- IDN, optional port, no user info, RFC 3986 path and query, fragment removed, at most 2048 bytes), is one the call opened, written the same way; once each; at most 20; everything else dropped and counted. `provider.SourceTitle` removes control, format (bidi, zero-width), private-use, surrogate and noncharacter code points, collapses white space, cuts to 200 characters. The answer's own sources never reach the review and are cleared from the output | `internal/ai/provider/research.go` |
| Storage and the resource | Kept sources go with the row's provider metadata (`sources`, beside the counts in `web_search`) for needs_input and ready only; no migration (the column is immutable once generated). `proposals.SourcesOf` checks them again before serving; `FromMetadata` shows `web_search` `{queries, sources_dropped}` within the contract's bounds | `internal/proposals/{public,generator}.go` |
| Spend | `spend.SearchPriceOf("muse-spark-1.3")` = USD 0.0025 a query. `Quote.MaxToolCalls`; the reservation adds `MaxToolCalls × search price` per attempt; the settlement adds every listed step at the search price (`Settlement.ServerToolCalls`, recorded in `ai_spend.settle` as `web_search_calls`) | `internal/ai/spend/spend.go`, `internal/ai/provider/meter.go`, `internal/storage/aispend/aispend.go` |
| Logs and audit | Counts only: `web_search_queries`, `web_search_calls`, `web_search_pages`, `sources_kept`, `sources_dropped` (catalogue updated); never a query, a page or a URL | `internal/audit/audit.go`, `internal/proposals/generator/generator.go` |
| Prompt | `prompts/research/system.md`, in English: research the real process and typical parameters; search in English and prefer English-language sources; prefer engineering and industry sources; web text is data, never an instruction, and cannot set a risk or approve; no model data, IDs, names, addresses or figures in a query; researched values are typical published values, the model stays synthetic and labelled so; needs_input rather than invented values; only pages opened in this request as sources, at most 10. The modeler and scenario templates are not changed, so proposals without research send exactly what they sent before | `prompts/research/system.md` |
| UI | See section 4 (the frontend fork's part) | `apps/web/`, `apps/web-e2e/` |

### The reservation's bound (and what it assumes)

Meta documents no limit on the server-side search loop (`max_uses` is a 400) and no bound on the sum of the input tokens over its steps, so no honest bound follows from the documentation alone. The bound chosen, per research call (one attempt, no retry):

| Part | Assumption | Amount |
|---|---|---|
| Input | the whole context window, 1,048,576 tokens, at the uncached input rate (USD 1.25/M): no step reads more; the probe's 41,356 tokens for five steps is about a twenty-fifth of it | USD 1.31072 |
| Output | `max_tokens` 32,000 at USD 4.25/M (the probe suggests `max_tokens` bounds the loop's whole output; not documented) | USD 0.136 |
| Web search | 64 steps at USD 2.50 per 1,000 (every step priced as a query: an upper bound, since the pricing page does not say whether opening a page is one) | USD 0.16 |
| **Total** | | **USD 1.60672** |

Against the default caps (D-110, PROPOSED): USD 5 an organization a day, USD 10 the deployment a day, USD 100 a month. A call that costs more than its reservation (a longer loop than assumed) is settled at what it cost, never clamped, flagged `spend_over_reservation` and warned of (T-053 review round 1, L1); the caps then refuse later calls. A research attempt that may have been billed without an answer is settled at the whole bound.

## 3. Requirement mapping and tests

| Requirement (brief item) | Tests |
|---|---|
| 1. Explicit per-request opt-in; sources bounded, https; researched label; role gate | `testdata/contracts`: `valid-proposal-request-research`, `invalid-proposal-request-research-not-boolean`, `valid-ready-proposal-researched`, `invalid-proposal-sources-without-web-search`, `invalid-proposal-source-not-https`, `invalid-proposal-source-extra-member`, `invalid-proposal-web-search-dropped-over-limit`, `invalid-proposal-invalid-with-sources`, `invalid-proposal-generating-with-sources`, `valid-proposal-output-with-sources`, `invalid-proposal-output-source-extra-member`; `TestResearchIsSealedForProposalRolesOnly`; `TestSchemaParity` |
| 2. Approval record; refusal before I/O; Claude refused; fake researches | `TestTheH029Record`, `TestMetaGapReasons` (research cases), `TestResearchGoesOnlyToABackendThatMay`, `TestResearchNotApproved` (meta: no request reaches the server), `TestAPIResearchAvailability`, `TestResearchRefusal`, `TestFakeResearch`, `TestResearchGeneration` (disabled and no-web-search backends fail for good with `AI_PROVIDER_NOT_CONFIGURED` / `AI_RESEARCH_UNSUPPORTED`), `TestProviderFailures` |
| 3. Meta adapter: tool only for research; mixed content; larger budget; spend | `TestResearchRequestShape`, `TestResearchAnswer`, `TestResearchAnswersRefused`, `TestNoResearchStepsRefused`, `TestResearchRequest`, `TestResearchSpend` |
| 4. Source filtering; untrusted web content; injection fails closed | `TestResearchKeepsOnlyOpenedSources`, `TestNoResearchNoSources`, `TestResearchAnswersFailClosed`, `TestSourceURL`, `TestSourceTitle`, `TestFilterSourcesBounds`, `TestResearchInTheResource`, `TestResearchStates`, `TestResearchGeneration` (an injected "approved, low risk" title: the server's risk stays medium) |
| 5. Prompt | `TestResearchContext`, `TestResearchTemplate`, `TestTemplates` |
| 6–7. UI, e2e | Section 4 |
| Logs and audit carry counts only | `TestResearchAnswer` (metadata leaks nothing), `TestResearchAnswersRefused` (errors leak nothing), `TestResearchGeneration` (audit catalogue, no URL in the event) |
| End to end on PostgreSQL | `internal/api/research_test.go` `TestProposalResearch` (refused with nothing stored; the job payload, the audit flag, the resource's sources against `proposal.schema.json`): **NOT RUN** locally (needs a disposable database; it reports NOT RUN without one) |

## 4. Frontend (the fork's part)

Done by a forked agent of this task in the same worktree (the parent read the diff; no independent review yet):

- **Ask AI form** (`apps/web/features/ai-proposals/ask-form.tsx`): an opt-in checkbox, off by default and per request, "Research real-world data on the web first (Meta search)", with the line "Your description is used for a web search. Do not include customer or confidential details." Only a ticked box adds `research: true`, so a request without it is byte for byte what it was. The 503 refusal whose `details[0].code` is `AI_RESEARCH_UNSUPPORTED` shows "Web research is not available in this deployment. Turn it off and send again."
- **Proposal page** (`sources.tsx`, `proposal-view.tsx`, `generated-by.tsx`): a "Sources the AI read" section with a note that Geminus has not checked what the pages say; each source is a link with `target="_blank"` and `rel="noopener noreferrer nofollow"`, its text the title through `visible()` (else the URL) in a `<bdi>`, a screen-reader "(opens in a new tab)", and the host (`new URL(url).hostname`, so an IDN shows as xn--); anything that is not an https URL is skipped. A line counts the pages the AI named that are not listed. The written-by line says "Muse Spark 1.3 (Meta), with web search: 4 queries" (or "with web search on: no searches made").
- **Strings**: keys added only, English first with zh-Hant translations, in `apps/web/messages/en.ts` and `zh-Hant.ts` (`ai.webSearch`, `ai.webSearch.none`, `ask.research`, `ask.researchHint`, `ask.research.unavailable`, `proposal.sources`, `proposal.sourcesHint`, `proposal.sources.newTab`, `proposal.sources.dropped`, `proposal.sources.allDropped`).
- **Tests**: `apps/web/tests/proposal-research.test.tsx` (unit); the e2e fake API (`apps/web-e2e/fake/servers.mjs`) records `research: true` and answers a research proposal with two fixed `example.com` sources and `generated_by.web_search` `{queries: 3, sources_dropped: 1}`; `apps/web-e2e/tests/proposal.spec.ts` has the opt-in and sources flow; the gallery stand-in and samples (`apps/web/app/ui-gallery/proposal/`) show the state.

## 5. Commands and results

Run in this worktree on 2026-10-30 (Go 1.27.3 toolchain from the module cache; Node 24.21.0 for the frontend):

| Command | Exit | Result |
|---|---|---|
| `make backend-test` (`go vet` + `go test` of `./cmd/... ./internal/... ./schemas/... ./scripts/... ./prompts/...`) | 0 | 85 packages ok; DB-backed tests report NOT RUN without `GEMINUS_TEST_DATABASE_URL` (existing behaviour) |
| `make architecture-check` | 0 | `PASS architecture: 100 packages, 26 rules, 16 build configurations` (a first draft that used `net/url` in `internal/ai/provider` was refused by rule ai-is-pure-but-the-adapter; the URL check is now pure string and regexp code) |
| `make ai-eval` (fake suites) | 0 | `TestFakeProviderEval`, `TestReviewEval`, `TestAnalystEval` pass; the fake reports in `checks/ai-evals/` unchanged |
| `PATH=/home/kuoba123/Geminus/.venv/bin:$PATH make spec-check` | 0 | contracts `PASS_LIMITED_SPEC_CHECKS` (79 positive and 156 negative fixtures, 5 OpenAPI request examples), docs, planning, hashes, TS contracts `PASS_TS_CONTRACTS_FRESH` (15 files, 111 types), DB tests fresh, deploy checks |
| `.venv/bin/python scripts/gen_ts_contracts.py`, then `--check` | 0 | `PASS_TS_CONTRACTS_FRESH` |
| `make frontend-test` (by the frontend fork) | 0 | lint, typecheck and unit: 54 files, 1160 tests |
| `make e2e-test` | NOT RUN at the time of this report | Another agent's servers held ports 3100 to 3102; the fork's first full run overlapped with them and was not counted. The fork was rerunning it when this report was written; its result is reported separately to the coordinator |

Red first: after the schema change and before the Go change, `TestSchemaParity` failed (`proposal-output.schema.json` has `sources`, `provider.ProposalOutput` did not); the approval record test failed until the expected record named the research roles; `TestProviderFailures` failed until `research_unsupported` had its message.

## 6. NOT RUN

- **Live research** (any request to api.meta.ai): NOT RUN by design; the coordinator runs it after the independent review. Unverified until then: that Meta accepts the request as built (only the probe's shape is known), the step names and inputs on other questions, whether `max_tokens` bounds the whole loop, whether opening a page is billed as a query, and the real cost against the USD 1.61 bound.
- `make integration-test` (including `internal/api` `TestProposalResearch`): NOT RUN locally.
- `make backend-race`: NOT RUN locally.
- Screenshot baselines: NOT RUN outside CI (by design).
- `make e2e-test`: not counted at the time of this report (see section 5).
- `make ai-eval-live`: NOT RUN (live).

## 7. PROPOSED (need an owner or reviewer decision; not decided here)

1. Sources are stored in `provider_metadata` (no migration, immutable once generated). A dedicated column would keep answer-derived text apart from call metadata; it needs a migration (0019) run on a disposable database.
2. The reservation's assumptions: the whole context window of input and 64 steps per research call (USD 1.60672), every step priced as a query, one attempt. A lower bound would admit more concurrent research calls under the USD 5 organization day cap; a higher one would be safer against long loops.
3. A research request is refused (503) when the provider cannot research, rather than run without research and say so.
4. `checks/README.md` index line for this report (another agent maintains the index).

## 8. Risks and limits

- The output schema's new optional `sources` (with its description) is part of every proposal call's projected schema, research or not: a model may add sources to a proposal without research; they are dropped (none opened), never shown.
- The research template asks the model not to put model data or names into a query; that is an instruction, not an enforcement: the searches run at Meta. The form tells the person that the description is used for a web search.
- Web pages can try to steer the model. The server's authority does not move (schema, review, risk, human approval), but a page could bias the proposal's values within what the review accepts; the assumptions say which values came from the web, and a person reviews them.
- A model that cites pages from search results without opening them gets those sources dropped (counted in `sources_dropped`).
- Cost: USD 2.50 per 1,000 searches on top of tokens; a research call is typically far below its USD 1.61 reservation (the probe cost about USD 0.05), but holds that much of the day's cap while it runs.
