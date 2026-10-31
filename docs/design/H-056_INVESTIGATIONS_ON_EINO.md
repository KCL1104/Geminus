# Geminus AI Investigations on Eino: H-056 autonomy plus the H-058/H-059 refactor

> **Status (2026-10-31):** synthesized design (three independent designs, two judges). The owner's answers in **H-060** override it where they differ:
> 1. **Muse Spark picks the tools.** The investigation is an Eino ADK `ChatModelAgent` (native tool calls; verified live on 2026-10-31 for Muse Spark 1.3 and Muse Wonder 1.0: both returned `tool_use` for a `run_simulation` tool). It is not a server-ordered `decide()` policy. A new prompt role (`investigator`) is added to the live approval (synthetic data only).
> 2. **It keeps going in the background** after the page closes, with a narrow stored acting-for scope (the requester, the organization, the role re-checked at every tool call), ending at the time cap. This amends D-008. Sections 4 (page-open clock), 5 (effects only in the page's requests), 6.5 (API advance graph) and 9 (pure decide policy) are **superseded by the architecture delta (H-061, pending)**.
> 3. **Caps:** as in section 11 (3 what-ifs, 4 runs, 4 explanations, 14 steps, 20 minutes; runs at most 10 replications and 28 days; 1 running per person; 10 per organization per day; USD 2.50 per investigation).
> 4. **Copy and edit mode:** the Guardrails and in-app copy below and the edit-mode rule (the AI writes only into a draft with none of the person's changes) are approved; ADR-032 ("the AI acts for a person in an investigation") is to be written.
> 5. **Muse Wonder:** evaluate it as an optional planner model for investigations (short tool calls avoid the long strict-JSON answers it failed on, H-051); ship only after live checks pass.
>
> Sections 6.1-6.4 and 6.6 (Eino pin, placement rules, the metered ChatModel adapter, role graphs, removing the old orchestration) stand, with one change: the adapter must also implement tool calling (`ToolCallingChatModel`) over the metered provider.


Status: PROPOSED. This is a design only. Nothing here has been built, run, or approved by the owner.
Base: the winning "reuse-first" design (Design 1), with the judges' grafts added and every blocker resolved (see §3).

## 1. Problem

Today a person runs the improvement loop by hand:
1. Ask Muse Spark 1.3 for a what-if (a scenario proposal).
2. Approve it, which saves a scenario.
3. Start a baseline/scenario run pair.
4. Ask for an explanation. The analyst's figures are checked against the runs.
5. Click "Propose as a scenario" on one of the analyst's "What to try next" ideas.

The AI writes structured answers. The server reviews every answer. A person approves, saves, runs and publishes.

The owner has decided three things:
- **H-056.** On a person's request, and only within that person's organization and role, the AI may start simulations of its own proposals and suggestions in a capped loop. It may also write its edits into a model's draft and save scenarios, with no approval step. Publishing a version still needs a person. Every AI-made scenario, run and draft edit must be labelled as the AI's in the UI and the API. Everything else in AGENTS.md §2 stays.
- **H-058.** That loop is orchestrated with Eino (github.com/cloudwego/eino), pinned to an exact version. Eino reaches models only through an adapter over the reviewed metered provider client. Its tools call the server's own services under the requesting person's tenant context, role, audit and idempotency.
- **H-059.** Every existing AI role moves onto Eino graphs: edit, scenario, research and photo proposals, the analyst, and H-057's repair attempt. Behaviour must stay the same: every existing test, golden and fake eval passes unchanged, live results are no worse, and no second pipeline is left behind.

Constraints:
- Code freeze is 2026-11-03 23:59 UTC. The owner starts the demo on the evening of 11-03 (UTC+8), about 11-03 10:00 UTC.
- Reasoning effort is never lowered (H-055).
- H-057 (repair) and H-054 (photo prompt) are in flight in parallel.

## 2. Decision references and how this design reads them

| Ref | What this design takes from it |
|---|---|
| H-056 (DECISIONS_AND_TBD.md:538) | The autonomy terms, the labels, and "caps set in the slice design, shown to the owner". The landing Guardrails are revised (§14, landing copy). |
| H-058 (:539) | Supersedes H-056's "no Eino". Eino is pinned (ADR-031). The only model path is an adapter over `provider.LLMProvider`, which is the metered `*provider.Client`. The tools are Eino `InvokableTool`s over the existing services. The engine and domain packs never import Eino. |
| H-059 (:540) | Each role moves onto an Eino graph behind entry points that keep their signatures, so white-box tests still pass. Each slice deletes the code it replaces. An AST rule then forbids a second pipeline. |
| H-057 (no row yet) | One repair attempt, as an unrolled DAG branch of the proposal graph. Its rule must be written as its own DECISIONS row first (owner question 7). |
| H-055 | No `effort` or thinking option is ever sent. Default `max_tokens` budgets are unchanged. |
| H-050, H-018 | USD 20/day per organization, USD 20/day per deployment and USD 100/month stay. A per-investigation ceiling is added on top. |
| H-047 | Default run settings: one week, warmup 0, 5 replications, seed 1, sampled trace. |
| H-049, H-051 | The person's model choice is passed to every call. Muse Wonder stays hidden unless its flag is on. |
| H-029 / D-112 | **No new prompt role.** Only the approved live roles (modeler, scenario, analyst) are used, so the data policy record does not change. |
| H-052, H-054 | Judges land on a finished comparison, so that page gets the second entry point. Photo proposals are not used inside investigations in P0. |
| D-007 | Engine limits are server-set and frozen per run. The AI never touches them. |
| D-008 | API scope comes from a verified token. Every AI write happens inside the requester's own authenticated request (§5), so D-008 holds as written. |
| D-020, D-021 | One allowlist for people and the AI (patch.Scenario, patch.AIEdit), with test-before-modify. |
| ADR-005 (SOURCE_LOCKED) | Superseded in part by a new, human-confirmed ADR-032 "The AI acts for a person in an investigation". |
| ADR-022 | Lock order model → draft → proposal, extended by → investigation. The draft rule on publish is unchanged. |
| ADR-027, ADR-030 | The adapters' no-tool, structured-output requests stay byte-identical. |

**Native tool calls (H-058's question).** Meta's Messages docs (fetched 2026-10-31 through a summarizer, not curl plus sha256) list:
- custom tools, `tool_use` and `tool_result` blocks;
- `tool_choice` auto, any or none (a named tool returns 400);
- the warning "arguments are not guaranteed to validate".

They do not describe custom tools combined with `output_config.format`. Nothing in this repo has ever sent a custom tool live. Both adapters refuse `tool_use` by design, and tests pin that (meta.go:711, messages_test.go:244-252, anthropic_test.go:259).

So the verdict is: documented, unverified, and not used in P0. The Eino graphs are built around **structured JSON actions**. The AI's reviewed structured outputs (a scenario proposal's patch, an edit proposal, the analyst's `suggested_next_experiments`) are the actions. The server turns them into Eino tool calls. A one-call synthetic probe, costing cents, can be run after the demo. If it passes, the same tools can sit under an ADK `ChatModelAgent` (§16).

## 3. What changed from the winning design

| Blocker or graft | Resolution |
|---|---|
| Eino under `internal/ai` would weaken ai-is-pure-but-the-adapter (Eino's closure brings net, os, syscall, unsafe, gob, text/template and gonja). | Graphs live in a new **`internal/aigraph/...`** with its own rules: eino-fenced, aigraph-has-no-storage, no-eino-ext. `internal/ai/...` keeps its rule unchanged. One narrow, reviewed gateway exemption remains, for `internal/ai/photopipeline` (§6.2). |
| `bind` (datasets.Resolve) is storage, which ai-suggests-only forbids. | Every I/O node body is injected by the job handler as a Lambda: preload, verify, bind, compare, SAM. The graph packages import no storage, tenant, pgx or app service. |
| The analyzer has no package tests. | E2's parity proof is `make integration-test` (internal/api/analyses_test.go) on a disposable DB. It is reported NOT RUN until it runs. |
| Design 1's "advance graph is ceremony" / H-058 reading. | The loop's tools are real Eino tools, one per effect: save_scenario, run_simulation, explain, propose_next, write_draft, finish, stop. The AI chooses the content (what to try, the patch, the next ideas). The server chooses the order. This is stated plainly, and the owner is asked to confirm it (question 1). |
| A pure-Go decide() would link Eino into the API for nothing. | Eino in the API is fenced to `internal/investigations/loop`, where the ToolsNode actually runs the effects. No ChatModel and no `internal/aigraph` package is ever linked into cmd/api (rule). |
| Proposal prompts say "a person reviews every suggestion", which is false inside an investigation. | One extra server-rules line is added, only for investigation calls. All other prompts, goldens and InputHashes are byte-identical (§10). |
| Labels could be forgotten in code. | A transaction-local `app.investigation_id` plus DB triggers make an unlabelled AI write, or a forged label on a person's write, fail at INSERT/UPDATE. Audit events in that transaction must carry `investigation_id` (§8). |
| Single worker replica, sequential jobs. | Worker lanes: `sim` (simulation) and `rest` (everything else) loops in one process, using the existing `jobs.Claim(types)` filter. No DB change, behind `WORKER_LANES=split`. |
| Demo hook. | Second entry point "Let the AI test these" on a finished analysis (the H-052 page). It is seeded from that analysis's suggestions, and the analysis's baseline run is reused when it fits. |
| Verdict without fabrication. | An optional objective (a fixed metric list, with the direction fixed per metric), a deterministic server ranking, and a server-written closing line. No AI headline exists, so no new prompt role is needed. |
| Spend number for the owner. | A per-investigation USD 2.50 ceiling, checked inside the worker's reservation transaction (optional slice A6). |
| Deadline safety. | Behind `GEMINUS_INVESTIGATIONS` (off: routes answer 503, the web hides the option). Cut line: an H-059 refactor slice not reviewed by 2026-11-03 06:00 UTC does not merge. The LLMProvider role methods are kept (deleting them would break about 20 test files); an AST rule says only the adapter calls them in non-test code. |
| Contract churn. | New label fields are **optional and present only on AI rows**. Existing response bodies, fixtures and golden responses stay byte-identical. |

## 4. User flow

1. **Entry A (model page).** An owner or editor opens Ask AI, types an intent, and picks "What-if" or "Change to the model". They tick **Let the AI act on it**.
   - The hint states the caps, and that the AI never publishes.
   - What-if mode shows run settings, defaulting to H-047 and editable within the caps (≤ 28 days, ≤ 10 replications), and an objective Select (default: median lead time, lower is better).
   - The person can tick research for the first proposal only.
   - The button stays `variant="ai"`, so Publish remains the one primary button.
   - Viewers see the read-only notice.
2. **Entry B (results page, H-052).** Under "What to try next", **Let the AI test these** (`variant="ai"`) starts an investigation seeded from that analysis. The analysis's baseline run is reused when its patch is `[]` and its config is within the AI caps. If it is not, the page explains why it cannot start.
   - If the caller already has a running investigation, the button becomes a link: "The AI is already testing ideas: watch it".
3. The web calls `createInvestigation` and gets 202. In the same transaction, the server inserts the investigation and queues candidate 1 through the reused proposals path (scenario or edit mode, intent = the person's text, or `"<name>: <hypothesis>"` of the first suggestion). The browser goes to `/investigations/inv_…`, filed under the rail's "AI proposals" section.
4. **The investigation page header** shows:
   - status;
   - "Done by Muse Spark 1.3 (Meta) for you";
   - the version;
   - the objective;
   - "What-ifs 1 of 3 · Runs 0 of 4 · 18 min left".
   
   A note reads: "The AI moves on while this page is open. If you leave, it waits and goes on when you come back, until 14:32." The page polls `getInvestigation` every 2 s (the ProposalRoute pattern).
5. The worker runs the proposal through the Eino proposal graph: the same prompts, metering and server review. When it is ready, its summary and ChangeList appear.
6. When `next.due` is true, the page's driver posts `advanceInvestigation {expected_step}` with the key `inv-<id>-s<step>`, so two tabs collapse into one call. Each advance runs one server step:
   - **save_scenario**: re-review under lock, then save the scenario labelled "Saved by the AI", and mark the proposal *applied*.
   - **run_simulation**: start the shared baseline (once) and this what-if's run with the frozen config. The page shows the existing RunPairStatus progress bars.
   - **explain**: queue the analyst for the pair. Muse Spark explains, and analyst.Check grounds every figure.
   - **propose_next**: the first untried suggestion of the newest explanation becomes the next scenario proposal. It has no research and uses the same model and locale.
   - Then the loop repeats until a cap or a natural end.
7. **Candidate table.** Each row shows only server figures: the objective metric baseline → what-if, the delta, and "not comparable" with its reasons, warnings and a "Saved by the AI" mark. It links to the existing results page (Marey chart, 3D, explanation) with `?investigation=`, and the driver also runs there.
8. **End.** The outcome section receives focus once and gets one StatusRegion announcement. It says:
   - the reason in words, for example "Finished: tried 3 what-ifs (the limit)" or "Stopped: the AI needs more information" with a link to its questions;
   - the server's closing line, for example: "Lowest median lead time: '<name>', 6.1% lower than the baseline. Geminus compares the runs' means and does not test whether the difference is significant."
9. **Edit mode.** The AI's ready edit is re-reviewed and written into the draft, but only if the draft is clean at the proposal's base. Otherwise it stops with "Your draft has changes; the proposal waits for your review".
   - The model page shows a Callout: "This draft has a change the AI wrote for you. Check it on the canvas before you publish."
   - The publish dialog repeats this, and the new version records it.
   - Publishing is the person's step.
10. **Control.**
    - **Stop the AI** (secondary, with a ConfirmDialog) is available to the requester or an org owner. What the AI made stays. Runs in flight are asked to cancel. A proposal or explanation in flight finishes, but nothing more is saved or run.
    - A person approving or rejecting the AI's ready proposal ends the investigation (`decided_by_person`).
    - Viewers and other editors watch read-only; an advance from them answers 403.
    - If the requester loses the editor role, their next advance answers 403, so nothing more happens.

## 5. Architecture

```
browser (requester's page)  --poll GET-->  API: internal/investigations (Get: observe + decide, read-only)
        |  next.due                              |
        +--POST advance (JWT, Idempotency-Key)-->  API: Eino advance graph (internal/investigations/loop)
                                                   observe -> decide -> branch -> ToolsNode{save_scenario | run_simulation |
                                                   explain | propose_next | write_draft | finish | stop} -> read_back
                                                   each tool: tenant.DB.Run(requester scope + app.investigation_id)
                                                   -> existing services (runs.Queue, analyses.Queue, proposals.Queue,
                                                      proposals/effect, models.WriteDraft) -> rows + jobs + audit + idempotency
worker (lanes sim | rest) <-- jobs (unchanged types) --
   model_proposal -> Eino proposal graph / photo graph -> einomodel adapter -> provider.Client (metered) -> Meta
   analysis       -> Eino analyst graph               -> einomodel adapter -> provider.Client (metered) -> Meta
   simulation     -> runs/runner (unchanged)
```

- **Model calls stay in the worker.** live-ai-only-in-the-worker is unchanged. The worker's job types, roles and grants are unchanged; only `SELECT` on investigations is added, for the spend ceiling.
- **Effects stay in the API.** Every AI write is made inside an authenticated request of the person who asked. That request is driven by their open page. Membership and role are re-checked FOR SHARE in every write transaction. The user audit event and the idempotency key are in that person's own key space. D-008 holds; no background executor and no stored scope are introduced.
- **The clock.** The page polls and advances. If it is closed, the investigation waits at its next step until `deadline_at`. The next `createInvestigation` by the same person first stops an expired one (`time_limit`) in its own transaction.
- **No second domain model.** decide() reads the existing rows (proposals, scenarios, runs, analyses, draft) through the new `investigation_id` links. The graphs work on the existing aicontext, provider, review and analyst types.

## 6. Eino integration

### 6.1 Version and ADR
- Pin `github.com/cloudwego/eino v0.9.21`: published 2026-09-23, commit `ba04fde8641057055c358d7ab5d3015a9ba825e1`, go.sum `h1:kBAyAjsnPq5NDWjt0VdmP9HY/XVUHs05LoeqybudPRQ=`, `/go.mod h1:OBD1mrkfkt/pJa4rkg1P0VnaMeOVl7l8IAdEqY//3IQ=`, Apache-2.0. v0.9.22 is 12 days old, fails the two-week rule until 2026-11-02, and adds nothing this design needs. A scratch probe built it on go1.27.3 with GOTOOLCHAIN=local.
- **ADR-031** records:
  - the version and hashes;
  - every new transitive module with its license (sonic Apache-2.0, bytedance/gopkg Apache-2.0, eino-contrib/jsonschema MIT, gonja MIT, go-humanize MIT, emperror MIT, json-iterator MIT, easyjson MIT, modern-go Apache-2.0, go-toml/v2 MIT, pkg/errors BSD-2, logrus MIT, pyfmt BSD-3, go-ordered-map Apache-2.0, filepathx MIT, x/exp BSD-3, yaml.v3 MIT/Apache-2.0);
  - why eino-ext is not used: its provider SDKs would bypass the meter, the Standard-mode check, strict decoding and no-store;
  - placement, the adapter contract, and the sonic decision.
- ADR-011's tool table and docs/THIRD_PARTY_NOTICES.md are updated. govulncheck v1.9.0 is run on the repository with Eino added; it is NOT RUN yet (only the probe was scanned: 0 affecting).
- **sonic on go1.27.3.** v1.15.0 builds its compat path: std encoding/json, with no JIT and no executable memory. It prints one constant non-JSON line on process stderr at init, in the worker and the API. This is recorded as a reviewed exception to T-035/D-094. The cmd tests capture `run`'s writer, not os.Stderr, so they stay unchanged. The alternative is sonic v1.15.4 (native JIT, executable memory, no line). Owner question 6.

### 6.2 Placement and architecture rules
All rules below are added in E0, with `future: true` where the package does not exist yet, so later slices do not edit rules.go.
- `internal/aigraph/einomodel`: the only Eino ChatModel.
- `internal/aigraph/{proposal,photo,analysis}`: the role graphs.
- `internal/investigations/loop`: the API's advance graph and tools.
- **eino-fenced**: `github.com/cloudwego/eino/...` may be imported only by `internal/aigraph/...` and `internal/investigations/loop`.
- **no-eino-ext**: `github.com/cloudwego/eino-ext/...` is forbidden everywhere.
- **aigraph-has-no-storage**:
  - `internal/aigraph/...` may use `internal/ai/...` (pure), `internal/model/...`, `internal/domainpack` (interface) and `internal/results/...`;
  - plus kernel third party and Eino's closure;
  - and no storage, tenant, pgx, app service, net/http, os/exec or database/sql.
- **aigraph-only-in-the-worker**: `internal/aigraph/...` is linked by cmd/worker, cmd/ai-eval-live and cmd/ai-eval and the packages they own; never by cmd/api.
- **investigations-only-in-the-api**: only cmd/api and internal/api link `internal/investigations/...`.
- **proposal-effects-only-in-the-api**: only cmd/api, internal/api, internal/proposals/approval and internal/investigations link the new `internal/proposals/effect`.
- decisions-only-in-the-api and scenarios-only-in-the-api are **unchanged**. investigations never links approval or the scenario service; it uses the shared effect package over storage/scenarios, as approval already does.
- **photopipeline gateway (the one reviewed exemption).**
  - ai-is-pure-but-the-adapter excepts `./internal/ai/photopipeline`.
  - A new rule photopipeline-is-pure-but-its-graph keeps the same forbid, thirdParty and stdOnly lists for photopipeline itself, with `through: ["./internal/aigraph/photo"]`.
  - So photopipeline's own imports stay kernel-only, and it reaches Eino only through that one gateway, which has its own rule.
  - Probe tests: photopipeline importing `net` directly still fails, and the kernel and domain packs cannot link Eino (they already refuse it through kernelThirdParty; this makes it explicit).

### 6.3 The ChatModel adapter (`einomodel.Metered`)
`Metered` implements `model.ToolCallingChatModel` over `provider.LLMProvider`, the existing metered `*provider.Client`. One instance is built over the text client and one over the vision client.

- **Input.** `Generate` needs exactly two messages: system then user. The user message's `Extra["geminus.call"]` holds a `*einomodel.Call`: a sealed `provider.ProposalContext` or `provider.AnalysisContext` from aicontext, carrying Attribution and the chosen model (H-049).
- **Tripwire.** The adapter refuses (`CodeInvalidRequest`) unless the message texts equal `prompt.System` and `prompt.User()`. No node can change what is sent.
- **Dispatch.** A proposal call goes to `GenerateModelProposal`, an analysis call to `AnalyzeSimulation`. So `Client.call` still runs, unchanged:
  - p.Verify, attribution, image and research gating;
  - the approved-model check;
  - MaxPromptBytes and the per-org slot;
  - quote, reserve and settle;
  - the returned-model check;
  - MaxOutputBytes, validate.Document and unmarshalStrict;
  - the output hash and no-store.
- **Output.** An assistant message: `Content` = the canonical answer JSON, `ResponseMeta.Usage` from `provider.Metadata`, and `Extra["geminus.result"]` = the typed result with its Metadata.
- **Errors.** `*einomodel.CallError{Metadata, Err}`. It unwraps to `*provider.Error`, so `errors.As` works through compose errors (verified in the probe), and logCall and auditMeta keep their metadata.
- **Refusals.** `WithTools`, or any tools or tool-choice option, gives `CodeInvalidRequest`.
- **Other methods.** `Stream` returns Generate as one chunk. `IsCallbacksEnabled()` is true: the adapter fires its own callbacks with `Messages: nil`, token counts, model and latency only.
- **Not used.** `ModelRetryConfig` and `ModelFailoverConfig` are never set, because the client retries under its own reservation. No `effort` or thinking option. No Eino ChatTemplate (prompts come only from aicontext and Seal).
- **Why the call sits in a Lambda node.** The reviewed generator turns some provider errors into outcomes (invalid_output becomes an invalid proposal; research and slow-model failures are final). A ChatModel node would end the graph on any error. So the call runs in a Lambda node, `generate`, that holds the Metered model and returns `Answer{Message, Err}` as data, and the branch after it reproduces today's mapping exactly.

### 6.4 Role graphs (worker, H-059)
All are `compose.NewGraph` in DAG mode (AllPredecessor). They are compiled once at start, with callbacks attached per run and never globally. ctx and cancellation reach every node. A recovered panic becomes a typed INTERNAL error, and its stack never reaches the logs raw.

- **proposal** (`internal/aigraph/proposal`):
  - decode_base → build_context (aicontext.Proposal) → preload → verify (both injected; before the paid call);
  - → generate;
  - → classify: invalid_output becomes `review.InvalidOutput`; an error goes to END with a typed error; ok continues;
  - → review (review.Review);
  - → [H-057 branch: repairable and no repair yet → repair_context → generate_2 → classify_2 → review_2; exactly one repair by construction, no cycle];
  - → branch (not ready → END) → bind (datasets.Resolve, injected) → END.
  
  `generator.generateAsked` and `generate` keep their signatures and map the graph's typed errors to today's `failure(...)` values and log lines, byte-identical. `internal/aievals/live.go` calls the same graph with preload, verify and bind set to nil (passthrough). So the live eval measures the production path.
- **photo** (`internal/aigraph/photo`):
  - segment (SAM 3.1 via the existing Segmenter, metered as today);
  - → photo checks (no boxes means needs_input; too many; frame mismatch);
  - → image context (aicontext.ImageProposal);
  - → generate (the vision Metered);
  - → review plus review.Photo.
  
  Its node bodies and types move to a new pure `internal/ai/photostep`. `internal/ai/photopipeline` becomes a facade: type aliases (`Clients`, `Request`, `Observer`, `Result`, `ContextError`, `ReviewError`), `Concepts`, `TakesImages`, `TakesModel`, and `Propose` = `photograph.Run`. photopipeline_test.go is an external test package that uses only this API, so it is unchanged. `generator.generatePhoto` and aievals/photo.go are unchanged callers.
- **analysis** (`internal/aigraph/analysis`):
  - decode → compare (injected; uses comparisons.Input and compare.Compare; NotComparable stops before the paid call);
  - → build_context (aicontext.Analysis) → generate → ground (analyst.Check) → END.
  
  `analyzer.analyze` keeps its signature.
- **Importer.** The importer role has no production caller, so there is no orchestration to move. The AST rule (E6) makes its future job use a graph.

### 6.5 The investigation advance graph (API, H-056 + H-058)
The graph is `compose.Graph[AdvanceInput, Investigation]` in DAG mode:
1. **observe**: a ReadSnapshot transaction (the requester's scope). It reads the investigation, its proposals, scenarios, runs (slots) and analyses, the model's latest version and the draft. Comparisons come from an in-process cache keyed by (baseline_run_id, run_id). The inputs are immutable, so the cache is always correct.
2. **decide**: a pure Go policy (§9). It returns either an assistant `schema.Message` with exactly one server-built ToolCall (`ID = "s<step>"`, `Arguments` = canonical JSON `{investigation_id, expected_step, candidate}`), or `wait`.
3. **branch**: on `wait`, END with `ErrNotDue` (409 RESULT_NOT_READY). Nothing is stored under the key, because the transaction never opens.
4. **ToolsNode** with `ExecuteSequentially: true` and `UnknownToolsHandler: nil` (an unknown name is an error). The tools are save_scenario, run_simulation, explain, propose_next, write_draft, finish and stop. Each is a `utils.InferTool` with `WithUnmarshalArguments` set to encoding/json/v2 with `RejectUnknownMembers` and no duplicate names, and with `WithMarshalOutput` set to canonical JSON.
5. **read_back**: a ReadSnapshot giving the Investigation resource.

**Each tool opens its own `tenant.DB.Run(ReadWrite)`.** No pgx transaction crosses Eino goroutines. Inside it:
1. `m.Require(Writers)`, and `sc.UserID` must equal `created_by`. stop also allows an owner.
2. `idempotency.Claim` with scope `advanceInvestigation` and fingerprint `{investigation_id, expected_step}`.
3. Lock model → draft → proposal → investigation (FOR UPDATE).
4. Re-observe and re-decide under the lock. A different action, or a stale step, is 409 LIFECYCLE_CONFLICT at `/expected_step`.
5. `set_config('app.investigation_id', id, true)`.
6. Exactly one effect plus its audit events, `step = step + 1`, then `idempotency.Complete`.

All of this commits together, as Approve does. Transient failures (503 DATASET_STORAGE_FAILED, object storage missing) roll back, so nothing is stored and the page retries.

The tools' effects reuse reviewed code that A3 extracts from each Create, which keeps its HTTP behaviour byte for byte:
- **save_scenario**: `proposals/effect.SaveScenario`, shared with Approve: recheck (review.Review against the base IR), manifest check, datasets.Resolve, scenarioName, storage/scenarios.Insert (with `investigation_id`), and proposal status `applied` (decided_by NULL, never `approved`). Audit `proposal.apply`. A recheck failure becomes the effect `stop(invalid_answer)` instead.
- **run_simulation**: `runs.Queue` (extracted from runs.Service.Create; freeze is unchanged). It queues slot 0 (baseline, patch `[]`) once, unless a baseline is reused, and slot k (scenario_id). Config, version and names are server-built from the frozen investigation. Audit `simulation.create`.
- **explain**: `analyses.Queue` for the slot-k run against the baseline. Audit `analysis.create`.
- **propose_next**: `proposals.Queue` (scenario mode, base = latest is checked, no research, the investigation's model and locale, intent `"<name>: <hypothesis>"` from the stored analysis). Audit `proposal.create` with source `suggestion`.
- **write_draft**: `proposals/effect.WriteDraft`: recheck against the base IR, check the draft is clean at the base (base = latest, hash = the base's hash), then `models.WriteDraft` (the extracted tail of PatchDraft: issues, hash, CAS on the revision read under lock), with `ai_proposal_id`, and proposal status `applied` with `applied_draft_revision`. Audit `proposal.apply` and `model.draft.update`.
- **finish** and **stop**: a status and reason change only. Audit `investigation.finish` or `investigation.stop`.

**Not used, and why.** No CheckPointStore and no interrupt/resume: the DB rows are the state, the graph re-derives everything each time, and an Eino checkpoint would be a second store that must agree with the rows atomically. No global callback handlers. No ChatModel in the API. Callbacks only record node names and durations to telemetry.

### 6.6 Removing the old orchestration
- Each H-059 slice deletes the step sequence it moves, in the same merge: the generateAsked body, analyze's sequence, Propose's body (moved to photostep and the graph), and aievals' direct call.
- E6 adds a go/ast source test: non-test code outside `internal/aigraph/einomodel` and `internal/ai/provider` may not call `GenerateModelProposal`, `AnalyzeSimulation` or `ProposeImportMapping`.
- E6 also adds a test that no `WithCheckPointStore`, `CheckPointStore`, `AppendGlobalHandlers` or `InitCallbackHandlers` appears anywhere.
- The LLMProvider interface is kept, because the fakes and about 20 test files use it.
- **Cut line**: a refactor slice not reviewed by 2026-11-03 06:00 UTC does not merge, so that role keeps its single old pipeline and no role ever has two.

## 7. Contract (schemas/ and openapi/ first)
- **common.schema.json** gains:
  - `InvestigationID` (`^inv_[0-7][0-9A-HJKMNP-TV-Z]{25}$`);
  - `InvestigationReason` enum: limit_reached, no_more_ideas, draft_written, time_limit, needs_input, invalid_answer, ai_failed, spend_cap, base_moved, draft_changed, run_failed, not_comparable, explanation_refused, decided_by_person, stopped_by_person;
  - `InvestigationObjective` {metric_path: enum `/metrics/lead_time_minutes/p50|mean|p95`, `/metrics/throughput/mean`; the direction is fixed by the server per metric};
  - `MadeByAI` {investigation_id, requested_by: UserID, generated_by: GeneratedBy}.
  
  No new ErrorCode values are needed.
- **New investigation.schema.json**:
  - `InvestigationRequest` is a oneOf:
    - from a request: {base_version_id, mode scenario|edit, intent 1..20000, context?, output_locale, research?, model?, run_config? (required for scenario, forbidden for edit; replications 1..10, horizon ≤ 28 days, trace none|sampled), baseline_name? (required with run_config), objective?};
    - from an analysis: {from_analysis_id, output_locale, model?, objective?}.
  - `Investigation`: {id, organization_id, model_id, base_version_id, mode, source request|analysis, source_analysis_id|null, objective, status running|finished|stopped, reason|null, step 0..14, next {action wait|save_scenario|run_simulation|explain|propose_next|write_draft|finish|stop, waiting_for|null, due}|null, caps, used {what_ifs, runs, explanations, steps}, run_config|null, baseline_run_id|null, candidates[≤3], best {index, objective ComparisonMetric}|null, deadline_at, created_by, created_at, finished_at|null}.
  - `Candidate`: {index, source request|suggestion, idea|null (AI text), proposal_id, proposal_status, scenario_id|null, run_id|null, analysis_ids[], applied_draft_revision|null, comparison {comparable, incomparable_reasons, warnings, objective ComparisonMetric|null, metrics ComparisonMetric[≤6]}|null}.
  - `AdvanceRequest` {expected_step 0..13}. `StopRequest` {} (any member is refused). `additionalProperties: false` throughout.
- **Labels, all optional and present only on AI rows, so every existing body is byte-identical:**
  - `Run.made_by_ai`, `Scenario.made_by_ai`;
  - `AnalysisRecord.investigation_id` (the AI asked for it);
  - `Proposal.investigation` {id, index} plus status `applied` with an if/then (scenario requires scenario_id; edit requires applied_draft_revision; decided_by stays null);
  - `Draft.ai_edit` {proposal_id, investigation_id, revision, operations};
  - `ModelVersion.ai_edit` {proposal_id, investigation_id}.
  
  source_proposal_id's description is updated: approved by a person, or applied by the AI.
- **openapi.yaml plus planning/api-operations.json**:
  - `createInvestigation` POST `/v1/models/{modelId}/investigations` (owner, editor; Idempotency-Key; 202). Errors: 404; 409 MODEL_VERSION_CONFLICT at /base_version_id; 409 LIFECYCLE_CONFLICT (the caller has a running investigation); 409 IDEMPOTENCY_KEY_CONFLICT; 422 REQUEST_VALIDATION_FAILED at /run_config/…; 429 RATE_LIMITED with Retry-After; 503 SERVICE_UNAVAILABLE (feature off); 503 AI_PROVIDER_NOT_CONFIGURED / AI_POLICY_NOT_APPROVED.
  - `listInvestigations` GET `/v1/models/{modelId}/investigations` (members, cursor).
  - `getInvestigation` GET `/v1/investigations/{investigationId}` (members).
  - `advanceInvestigation` POST `…/advance` (the requester, owner or editor; Idempotency-Key; 200). Errors: 403; 404; 409 LIFECYCLE_CONFLICT at /expected_step; 409 RESULT_NOT_READY (nothing stored); 409 IDEMPOTENCY_KEY_CONFLICT; 503.
  - `stopInvestigation` POST `…/stop` (the requester, or an owner; Idempotency-Key; 200; 409 LIFECYCLE_CONFLICT if already ended).
  - Every response has an example.
- **Audit catalogue (internal/audit)**:
  - New actions (User): `investigation.create` {model_id, base_version_id, mode, source, output_locale, research flag, requested_model, replications}, `investigation.advance` {step, action}, `investigation.finish` / `investigation.stop` {reason, step}, `proposal.apply` {mode, model_id, base_version_id, patch_hash, server_risk_level, risk_level, scenario_id | draft_revision}.
  - Optional `investigation_id` (idOf inv) plus `by_ai` (flag) on proposal.create, simulation.create, analysis.create and model.draft.update.
  - Optional `contains_ai_edit` flag on model.publish.
  - Never free text.
- Generated TS (scripts/gen_ts_contracts.py), the web client functions, and Go and TS contract tests with new AI-row fixtures. Exhaustive switches on proposal status gain `applied`; a missing case is a compile error.

## 8. Database: migration 0019_investigations (new; 0001-0018 untouched; live rows kept)
- **IDs.** Domain `twin.inv_id`. The `twin.public_id` CHECK is replaced with `inv` added; the generated name is looked up in a DO block over pg_constraint, never guessed. `internal/ids` learns `inv`. An audit target-type CHECK, if one exists, gains `investigation`.
- **twin.investigations.** Columns:
  - id, organization_id, model_id, base_version_id, mode, source, source_analysis_id, output_locale, research, requested_model;
  - run_config jsonb + run_config_hash (NOT NULL exactly when mode = scenario); baseline_name; baseline_run_id (a reused person's run: same version and config_hash, patch `[]`, checked at INSERT);
  - objective, caps jsonb (frozen);
  - status, reason (NULL exactly while running), step 0..14;
  - deadline_at (CHECK: created_at + 20 min), created_by usr_id, created_at, finished_at.
  
  Constraints and indexes:
  - UNIQUE(org, id) and UNIQUE(org, model_id, id); composite FKs to models, model_versions, ai_analyses and simulation_runs;
  - partial UNIQUE (organization_id, created_by) WHERE status = 'running';
  - an index on (org, model_id, created_at, id);
  - a daily-count index for the 10 per org per day check, done in the create transaction under an org-scoped advisory lock.
  
  RLS is ENABLEd and FORCEd with the tenant_isolation policy. Grants:
  - twin_api: SELECT, INSERT, and UPDATE(status, reason, step, finished_at);
  - twin_worker: SELECT (spend ceiling only);
  - twin_provisioner: nothing.
- **protect_investigation** (BEFORE INSERT OR UPDATE; schema owner exempt):
  - INSERT: created_by = app.user_id, status running, step 0, created_at = now().
  - UPDATE: identity and request columns are immutable. The step moves by exactly +1 or stays. running → finished or stopped only, and terminal rows are final. finished_at = now() on a terminal move.
  - Only app.user_id = created_by may update, except stop with reason stopped_by_person by an owner. The API role can read memberships.
- **Acting-for guard (fail-closed labels).** BEFORE INSERT on ai_proposals, scenarios, simulation_runs and ai_analyses: `NEW.investigation_id IS NOT DISTINCT FROM nullif(current_setting('app.investigation_id', true), '')`. An unlabelled write inside an investigation transaction and a forged label outside one both fail.
  - The labelled row's investigation must be running and in the same org and model, with created_by = the investigation's created_by = app.user_id.
  - audit_events BEFORE INSERT: when the setting is non-empty and actor_type = 'user', `metadata->>'investigation_id'` must equal it; when it is empty, the key must be absent.
  - model_drafts BEFORE UPDATE under the setting: NEW.ai_proposal_id must be set (the deferred check below proves it).
- **ai_proposals.**
  - Adds investigation_id, investigation_index (1..3; NULL together with investigation_id) and applied_draft_revision. UNIQUE (org, investigation_id, investigation_index). A composite FK to investigations.
  - Index > 1 requires mode scenario. base_version_id and mode must equal the investigation's.
  - The status CHECK and the 0001 effect CHECK are replaced (both by definition lookup): `status IN ('approved','applied') OR (new_version_id IS NULL AND scenario_id IS NULL)`.
  - An applied CHECK: patch, validation, risk and confidence present (as for ready/approved); decided_by and decided_at NULL; investigation_id NOT NULL; mode scenario means scenario_id set and new_version_id NULL; mode edit means applied_draft_revision set and both IDs NULL.
- **protect_proposal_content** is redefined in 0019 (copied from 0001, plus one transition):
  - `ready → applied`, only when investigation_id IS NOT NULL and app.user_id = created_by;
  - its effect must exist: a scenario with source_proposal_id = NEW.id and the same investigation_id, or a model_drafts row with ai_proposal_id = NEW.id and revision = NEW.applied_draft_revision;
  - applied joins the immutable terminal set, and approve or reject of an applied proposal is refused;
  - guard_worker_proposal is unchanged.
- **check_proposal_effect** (0013) is redefined: the scenario branch accepts `approved` with NEW.investigation_id NULL, or `applied` with the same investigation_id. The version branch is unchanged: the AI never creates a version.
- **scenarios.** Adds investigation_id with a composite FK, and CHECK investigation_id IS NULL OR source_proposal_id IS NOT NULL. It is written only at INSERT; immutable_record and protect_scenario_creation are unchanged.
- **simulation_runs.** Adds investigation_id and investigation_slot (0..3; NULL together). UNIQUE (org, investigation_id, investigation_slot), so at most 4 AI runs by construction. `check_investigation_run` (BEFORE INSERT):
  - the investigation is running; created_by = its created_by = app.user_id; model_version_id = its base; config_hash = its run_config_hash;
  - slot 0: scenario_id NULL and patch `[]`, and only when there is no reused baseline;
  - slot k: scenario_id names a scenario of this investigation whose source proposal has investigation_index = k.
  
  A BEFORE UPDATE trigger keeps both columns immutable.
- **ai_analyses.** Adds investigation_id and investigation_index (1..4), UNIQUE. A BEFORE INSERT check: the investigation is running, created_by matches, run_id is a slot ≥ 1 run of it, and baseline_run_id is its slot 0 run or its reused baseline.
- **model_drafts.** Adds ai_proposal_id with an FK. A deferred constraint trigger: a change to non-NULL must name an edit proposal of this model that is `applied` at commit, with applied_draft_revision = NEW.revision. syncDraft and ResetDraft write NULL. A person's PatchDraft carries it forward.
- **model_versions.** Adds ai_edit_proposal_id with an FK, set at INSERT by Publish from the draft. A constraint trigger requires it to name an applied edit proposal of the model. It is immutable through immutable_record.
- **Unchanged:** job types, protect_job_state, the spend ledger tables and their role/target CHECKs, guard_audit_actor, actor_type, and every existing grant. db/tests/roles.sql, invariants.sql and scenarios.sql pass unchanged.
- **Down migration**: refuses when investigation-labelled rows exist, and is never run automatically.
- **New db/tests/investigations.sql** covers:
  - RLS across organizations;
  - the worker has no INSERT on investigations, scenarios or runs;
  - a 4th what-if, 5th run or 5th analysis is refused;
  - a run with a different config, base or creator is refused;
  - an unlabelled insert under the setting, and a forged label without it, are refused;
  - audit metadata without investigation_id under the setting is refused;
  - ready → applied by a non-requester is refused;
  - approving an applied proposal is refused;
  - an applied proposal is immutable;
  - a scenario labelled from a non-applied proposal is refused;
  - a draft's ai_proposal_id without an applied revision is refused;
  - run provenance is immutable;
  - a second running investigation per person is refused;
  - the step cannot skip; a terminal row is final.
  
  It runs on a disposable DB only.
- **tenant.** `Scope` gains an optional `ActingFor` (an inv_ ID). DB.Run sets `app.investigation_id` transaction-locally when it is present. It is added to `ScopeSettings`, so Guard discards any connection still carrying it. An AST test allows only internal/investigations to set `ActingFor`.

## 9. Worker and loop policy
- **Worker lanes** (`WORKER_LANES=split`; default single, so the existing cmd/worker tests are unchanged). Two `worker.Worker` loops run in one process on the same scope, with IDs `<id>:sim` and `<id>:rest` and disjoint handler sets: sim = simulation; rest = model_proposal, analysis and import_*. Claim already filters by type, and the lease fence is (locked_by, lease_token). The investigation's runs never queue behind 50-60 s AI jobs. The Railway replica count is unchanged.
- The **worker runs no investigation job**. It runs the ordinary proposal, analysis and simulation jobs, with the same attempts (research 1, slow model 1, others 3). It reads `investigation_id` from ai_proposals and ai_analyses only to add the investigation server-rules line (§10) and, in A6, to apply the per-investigation spend ceiling.
- **decide()**, a pure policy over the last candidate (table-tested over every state):
  - Past deadline_at: stop(time_limit).
  - Proposal generating: wait. needs_input: stop(needs_input). invalid: stop(invalid_answer). failed: stop(ai_failed), or stop(spend_cap) when its code is AI_QUOTA_EXCEEDED. Approved, rejected or conflicted by a person: stop(decided_by_person).
  - Ready, but the base is not latest: stop(base_moved), and the proposal stays ready for a person.
  - Ready, scenario mode: save_scenario. Ready, edit mode: write_draft if the draft is clean at the base, else stop(draft_changed); after write_draft, finish(draft_written).
  - Applied scenario with no runs: run_simulation. Runs queued or running: wait. A run failed or cancelled: stop(run_failed). Not comparable (for example the engine version changed mid-investigation): stop(not_comparable).
  - No analysis: explain. Analysis generating: wait. Analysis failed: explain again once per investigation; otherwise the candidate shows "explanation refused" and the loop goes on with untried suggestions from earlier analyses, or finish(explanation_refused).
  - Analysis completed: at 3 what-ifs, finish(limit_reached). With no untried suggestion (deduplicated by normalized name across the investigation): finish(no_more_ideas). Otherwise propose_next.
- **Closing line and ranking.** The server orders comparable candidates by the objective's relative delta in its fixed direction, then by fewer operations, then by index. The closing line is a catalog template filled with server figures only. Without a comparable improvement it says "None of the N what-ifs improved <metric> on the baseline, by the server's comparison."

## 10. Prompts
- **No new role, template or block layout.** modeler-0.2.0, scenario-0.7.0, research-0.2.0, photo-0.3.0 and analyst-0.3.3 are byte-identical for every call outside an investigation, so goldens and InputHash do not move.
- **One gated server-rules line** for calls whose proposal or analysis has an investigation_id. `ProposalInput.Investigation` and `AnalysisInput.Investigation` are flags, and when false the output is byte-identical. The line is versioned on its own (`investigation-rules-1`) and English-first.
  - Proposals: "In this request the server may act on a ready answer for the person who asked, within fixed limits: it may save a what-if as a scenario and run it against the baseline, or write an edit into a clean draft. It still validates every change and sets the risk itself, and only a person publishes. This line replaces any instruction that says a person reviews first."
  - Analyst: "In this request the server may turn your first suggested experiment that has not been tried into the next what-if for the person who asked; it is reviewed like any proposal."
  - New goldens cover both variants.
- **Follow-up intents** are built by the server from the stored, schema-valid analysis (`"<name>: <hypothesis>"`, exactly ProposeNext's text, analysis.tsx:334). They go in as untrusted USER_INTENT. The worst an injected suggestion can do is propose another allowlisted what-if, which is still fully reviewed, capped and labelled. Redaction and injection tests cover the follow-up path ("publish v3", "approve", "set replications 1000000", "act as another user").

## 11. Caps (PROPOSED; DB-enforced where marked)

| Cap | Value | Enforced by |
|---|---|---|
| What-ifs per investigation | 3 (the person's request + up to 2 AI ideas; or 3 suggestions) | ai_proposals.investigation_index CHECK 1..3 + UNIQUE; decide() |
| Runs per investigation | 4 (baseline slot 0 + slots 1..3; 3 when the baseline is reused) | simulation_runs.investigation_slot CHECK 0..3 + UNIQUE; binding trigger |
| Run settings for AI runs | replications 1..10; horizon ≤ 28 days in the model's unit; warmup < horizon; trace none or sampled; default H-047; frozen per investigation | request schema + service; run trigger (config_hash) |
| Engine limits per run | unchanged D-007: 300 s wall, 10,000,000 events, 100,000 active entities, 10,000 visits, 100,000 trace events; limit failures never retried | runs.freeze / runner (unchanged) |
| Explanations | 4 (one per what-if + one retry) | ai_analyses.investigation_index CHECK 1..4 + UNIQUE |
| AI calls | ≤ 10 (3 proposals × at most 2 with H-057's repair + 4 analyses); research ≤ 1 (first proposal, only if ticked) | caps above |
| Server steps | 14 | investigations.step CHECK 0..14, +1 per advance |
| Wall time | 20 minutes from creation | deadline_at CHECK; decide() |
| Concurrency | 1 running investigation per person per organization | partial UNIQUE index |
| Rate | 10 created per organization per UTC day (429 RATE_LIMITED) | create transaction, org-scoped lock |
| Spend | USD 2.50 per investigation (settled + open reservations), inside USD 20/day per org, USD 20/day per deployment, USD 100/month | aispend.Reserve, same transaction (A6) |
| Edit mode | 1 proposal, 1 draft write, 0 runs, clean draft only | decide() + effect + DB effect check |
| Reasoning | never lowered; no effort or thinking option sent | adapter test (request bytes) |

**Expected figures**, estimated from recorded live costs on Muse Spark 1.3 (scenario proposal about USD 0.04, analysis about 0.074, research 0.155): about USD 0.35 per 3-what-if investigation, about 0.5 with research or repairs. Expected time is 3-5 minutes (proposals 10-25 s, runs 1-3 s, an explanation about 50 s). These are estimates, not measurements. A9 measures them, and nothing is quoted before then.

## 12. Safety argument per AGENTS.md §2
1. **Four boundaries stay separate.** The engine, IR and domain packs cannot link Eino (kernelThirdParty, plus new probe tests). Eino is fenced to `internal/aigraph/...` and `internal/investigations/loop`. `internal/ai/...` stays pure, with one gateway for photopipeline. ModelChangeProposal and approval keep their meaning: the AI path writes a distinct `applied` status. Graphs use existing types, so there is no second domain model.
2. **The AI acts only as H-056 allows.** It writes only inside an investigation a person started, through that person's authenticated requests, within their org and role (re-checked FOR SHARE in every write), and up to DB-enforced caps.
   - It **never approves**: Approve is unchanged and refuses `applied`; decided_by stays NULL; the trigger refuses `approved` without the scoped person.
   - It **never publishes**: internal/investigations has no publish path. A source test refuses Publish or InsertVersion references in internal/investigations and internal/proposals/effect. An integration test asserts that no investigation creates a model_versions row.
   - **No arbitrary code or SQL**: the tools are fixed Go functions with server-built, strictly decoded arguments.
   - **Schema-valid is not authorized**: every effect re-runs the server review (recheck, datasets.Resolve, freeze) in its own write transaction.
3. **Immutability.** Scenarios are insert-only with the label written at INSERT. AI runs are ordinary frozen runs on an immutable version (never a draft), with an allowlisted patch, the whole manifest, the hash cross-check, the engine/config/seed and server limits. Results are stored only by the unchanged runner. `applied` is terminal. Provenance columns are immutable.
4. **Stale base means 409, and effects are atomic.**
   - An AI save requires base = latest; otherwise stop(base_moved) and the proposal waits for a person.
   - An AI draft write requires a clean draft at the base and a CAS on the revision read under lock; otherwise stop(draft_changed) and the draft is untouched. A person's open canvas learns of it on their next save, through the existing 409 → "Reapply my edits" flow (no auto-rebase).
   - Two tabs collide on expected_step or replay the same key.
   - Effect, audit, step+1 and idempotency answer commit in one transaction.
5. **Tenant isolation.** Every new row has organization_id with composite FKs. RLS is FORCEd. `app.investigation_id` is transaction-local and in ScopeSettings. No grant is widened except worker SELECT on investigations. Runtime roles stay non-superuser, non-BYPASSRLS and non-owner. Another org gets 404.
6. **2D/3D.** Unchanged: AI runs open in the existing results page and the read-only 3D projection, with the same entity IDs.
7. **Insufficient data means needs_input or unavailable.** needs_input, invalid, failed, not comparable and refused explanations end or annotate the investigation with a typed reason in words. Figures come only from the server's comparisons. Explanations pass analyst.Check. No winner is invented. P0 has no confidence interval for a difference, and the UI says so.

Also kept:
- **Spend**: every call reserves before it runs, through the unchanged client, and the caps stay.
- **No-store**: requests are stateless and byte-identical, reasoning blocks are dropped, there is no checkpoint store, and the investigation row holds only server state.
- **Typed errors**: existing ErrorCodes with JSON Pointer paths and request_id.
- **Cancellation**: passed through every graph node and tool.

## 13. Labelling
- **API**: `made_by_ai` on Run and Scenario; `investigation` and `applied` on Proposal; `investigation_id` on AnalysisRecord; `ai_edit` on Draft and ModelVersion; and the Investigation resource. All are present only on AI rows.
- **DB**: the acting-for guard makes labels impossible to forget or forge.
- **Audit**: each AI write is the requester's `user` event (guard_audit_actor unchanged) with `investigation_id` and `by_ai`. Owners see them in the audit list. No intent, idea text, names, patches or prompts are recorded.
- **UI** (violet accent marks "what the AI does", never a status):
  - "Saved by the AI" on scenario rows and the scenario page;
  - "Started by the AI" on runs rows and as a results-header fact ("Run by the AI for <person> · See the investigation");
  - proposal status "Done by the AI" (an ok-tone status pill) with an outcome section;
  - the draft Callout and a publish-dialog line;
  - the version fact "Includes a change the AI wrote".

## 14. UI
- **AskForm**: a checkbox "Let the AI act on it", a mode-specific hint, run settings (run-config.ts defaults and parseRunForm, plus the caps), and an objective Select. It is blocked by the existing `ask.saveFirst` while there are unsaved ops.
- **/investigations/[id]** (RequireSession; `sectionOf` maps it to AI proposals; RailModel):
  - the header facts;
  - the clock note;
  - a step list derived from candidate rows;
  - the active pair through useRunPair and RunPairStatus;
  - the candidate table (server figures, AI text through `visible()` and `<bdi>`);
  - the outcome section;
  - Stop the AI.
  
  The `<InvestigationDriver>` advances only when next.due. It treats 409 RESULT_NOT_READY or LIFECYCLE_CONFLICT as a re-read and 403 as watch-only. It is also mounted on results pages opened with `?investigation=`.
- **Analysis**: "Let the AI test these" plus the revised note.
- **Proposal page**: the `applied` outcome section. Approve and Reject stay available as human override, with a note that the AI will act unless you decide first.
- **Model page**: a Callout for `draft.ai_edit`.
- **Design rules**: one primary per view; no animation, spinner or toast; aria-disabled with reasons; StatusRegion for outcomes only; works at 390 px.
- **i18n**: English first, then zh-Hant, every key in both catalogs, including every reason and AI_QUOTA_EXCEEDED.
- **UI gallery**: a stand-in API plus StateMenu (running, waiting, refused, finished, stopped, edit written, viewer).

## 15. Tests (write negative tests first in each slice)
- **Rules**: probes for eino-fenced, no-eino-ext, aigraph-has-no-storage, aigraph-only-in-the-worker, investigations-only-in-the-api, proposal-effects-only-in-the-api, the photopipeline gateway, and kernel/domain-pack refusal of Eino.
- **Adapter parity** (fake backend + MemoryMeter + Meta httptest server):
  - the same reservations and settlements, InputHash, and request bytes as a direct call;
  - refusals: disabled, wrong org, Verify failure, unapproved model, too large, research gating, images;
  - errors.As through a compiled graph;
  - the tripwire on altered messages;
  - tools refused;
  - callbacks without content;
  - no effort or thinking on the wire;
  - cancellation still settles.
- **H-059 parity**: every existing generator, analyzer (via internal/api DB tests), photopipeline, aievals, provider, corpus and adapter test, golden, and the `make ai-eval` fake reports run unchanged and byte-identical. Any test that pins internal call order and would need to change is raised as a finding, never adapted silently.
- **Loop**:
  - decide() table tests over every state;
  - strict-argument tests (unknown member, duplicate, wrong type);
  - graph wiring and not-due rollback.
- **Integration** (fake provider, in-process worker lanes, disposable DB):
  - the full 3-what-if loop to limit_reached; the from-analysis entry with a reused baseline; edit to draft_written then a person publishes;
  - replay; concurrent advance; stale step;
  - base_moved; draft_changed; decided_by_person; role lost (403); another editor (403); another org (404); deadline; second running (409); daily cap (429);
  - needs_input; explanation refused then retried once; run_failed; not_comparable; spend_cap;
  - no model_versions row created; every AI row labelled; audit events with investigation_id and actor = requester;
  - `-race`.
- **DB**: db/tests/investigations.sql (§8).
- **Web**: unit plus axe; simplicity, strings, keyboard and 390 px; helpers.ts views; gallery screenshots. The browser closed loop against a real backend is NOT RUN (T-040 is not built); the server closed loop is covered by the Go integration tests.
- **Live** (A9): NOT RUN until done.

## 16. Risks
- **H-058 reading.** The server sequences the steps; the AI chooses the content. If the owner wants model-chosen tool calls, the same InvokableTools can sit under an ADK ChatModelAgent with a new `investigator` role. That needs H-029 approval, a probe, and measured live quality, and it does not fit before the freeze.
- **The page-open clock** pauses when the tab closes. It is honest and keeps D-008. A server clock is a D-008 amendment.
- **Migration 0019 replaces reviewed triggers and unnamed CHECKs.** Names are looked up by definition. The migration runs up/down/up on a disposable DB, then on a copy of live, before deploy.
- **H-057 interplay.** If H-057 lands in the old generator first, E3 absorbs it. Without H-057's DECISIONS row by 11-02 12:00 UTC, E4 is not built.
- **Two lanes in one process** with no GOMEMLIMIT or container limit configured (SIMULATION_SPEC.md:157). AI runs are capped, but memory headroom on Railway is unknown.
- **Live quality.** Proposals can be needs_input, and the analyst was refused 1 of 2 times in T-030. Investigations may stop early; the stop is shown honestly. Rehearse on the researched electronics model, and leave one real finished investigation in the demo org for judges who cannot wait.
- **Shared demo account.** One running per person and 10 per org per day mean concurrent judges watch the same one, or hit 429 with a link to a finished one.
- **sonic stderr line** (see 6.1). **govulncheck** on the repository is NOT RUN yet.
- **Merge contention.** generator.go (H-057), prompts/photo (H-054, done), the four services touched by A3, and messages/en.ts (A7 then A8, sequential).
- **Owner gates** before live: ADR-032, the caps, the copy, the clock. The code merges with the feature off.

## 17. Out of scope (P0)
- Native custom tool_use.
- An ADK ChatModelAgent planner with a new prompt role.
- A server-side clock.
- Photo proposals inside investigations.
- An AI closing headline.
- "Use this in the draft" per row.
- Per-operation AI marks on the canvas.
- Simulating an AI draft edit before a person publishes it (runs bind published versions).
- A second worker replica.
- GOMEMLIMIT tuning.
- Eino checkpoint/interrupt persistence.
- eino-ext.
- Any change to the approval path's semantics (TEST-008 still holds for approvals).
