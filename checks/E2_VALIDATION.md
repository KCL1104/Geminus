# E2 validation: the analyst on an Eino graph (H-059)

- Slice: E2 of [H-056_INVESTIGATIONS_ON_EINO.md](../docs/design/H-056_INVESTIGATIONS_ON_EINO.md) §6.4 ("analysis"), §6.6 (the old step sequence removed in the same change) and §3 ("E2's parity proof is `make integration-test` (internal/api/analyses_test.go)"), on E0 and E1 ([E0_VALIDATION.md](E0_VALIDATION.md), [E1_VALIDATION.md](E1_VALIDATION.md)).
- Decisions: H-058, H-059 (APPROVED: every AI role on Eino graphs, no behaviour change, no second pipeline), H-064 (APPROVED: sonic's one stderr line), H-055 (no effort or thinking option), H-049 (explanations stay on Muse Spark 1.3; no model choice reaches the analyst). [ADR-031](../docs/adr/ADR-031-eino.md) gains "As built in E2" (PROPOSED wording). The agent approved nothing.
- Requirements and rules: AGENTS.md §2 (the AI only suggests; nothing fabricated), §5 (typed errors, cancellation through every layer, tests first), §6 (NOT RUN listed); AI_SPEC §9 (analyst evidence and numeric checks), T-030 (the analysis job), T-053 (spend reservation before the call), T-035 / D-093 / D-094 (log lines), H-064 (the one stderr exception).
- Product behaviour: **unchanged.** No prompt, schema, OpenAPI, migration, UI, approval record, job type or grant changed. Every existing test, golden, InputHash and fake eval report is unchanged; the analyzer's own goldens (new, written from the code before E2) pass unchanged on the graph.

## 1. What changed

| File | Change |
|---|---|
| `internal/aigraph/analysis/graph.go` (new) | The analyst graph: `compose.Graph[Input, Output]` in DAG mode (`AllPredecessor`), named `analyst`, five Lambda nodes `decode → compare → build_context → generate → ground`. `decode`: `model.Decode` of the version's IR, the pack lookup, the stored results (strict), the frozen patch. `compare`: with a baseline, `Input.Compare` (injected by the handler), then `NotComparable` before the paid call, then the comparison's JSON. `build_context`: `aicontext.Analysis` and the attribution. `generate`: the system and user messages of the sealed prompt with `einomodel.Call{Analysis}` and `einomodel.Metered.Generate`; the client's error and metadata are unwrapped from `*einomodel.CallError`; `Config.Called` is told of the call as it returns. `ground`: `analyst.Check`. Every failure is an `*analysis.Error{Kind, Err, Reasons, Metadata, Issues}` (10 kinds), returned by `Run` unwrapped from Eino's error; its text names only the kind. Each node body is guarded: a panic is carried out of Eino and re-raised by `Run` with its own value. Compiled once in `New` (which checks the provider, packs and budget); no callback handler. |
| `internal/analyses/analyzer/analyzer.go` | `New` compiles the graph (`Called: h.logCall`). `analyze` keeps its signature: it builds `analysis.Input` from what the attempt read (the comparison injected as `compare.Compare(comparisons.Input(...), comparisons.Input(...))`), runs the graph, and maps each `Kind` to exactly the failure, message, retry decision, metadata, issue count and log line it had before. The old step sequence (`result` and the body of `analyze`) is deleted; `Handle`, `start`, `providerFailure`, `logCall` and `auditMeta` are unchanged. |
| `internal/analyses/analyzer/analyze_internal_test.go`, `testdata/analyze-goldens.json` (new; committed first, 71e1171) | White-box parity tests of `analyze` over 18 paths, with goldens written from the orchestration before E2 (§3). |
| `internal/aigraph/analysis/graph_test.go` (new) | The graph's own tests (§3). |
| `cmd/worker/sonic_line_test.go` (new) | The worker binary, built and started with an empty environment, writes H-064's one sonic line first and then JSON lines only. |
| `docs/adr/ADR-031-eino.md`, `docs/ARCHITECTURE.md` §2, `docs/AI_SPEC.md` §9, `checks/README.md`, `checks/docs-check.json` | As built in E2; the worker links Eino; govulncheck's first run; this report's row; the docs check's counts (machine-written). |

## 2. Findings and decisions taken in the slice

1. **The design has no written slice list.** E2 was identified from the design's §3 ("E2's parity proof is `make integration-test` (internal/api/analyses_test.go)"), E0's report ("H-059 (E2, E3) moves those handlers onto the role graphs") and the workflow's own description ("E2 analyst, E3 proposals, E5 photo"). E2 is §6.4's analysis graph.
2. **The worktree started without E0 and E1.** It was created at 24d233b; the main branch (`claude/lucid-meitner-vq1awo`) was at 31ab222 (the merges of E0, E1 and W1). The worktree branch had no commit of its own, so it was fast-forwarded to 31ab222 before any change (`git merge --ff-only`; no merge commit, the main branch untouched).
3. **Failures end the graph as node errors, not as data.** §6.3's `Answer{Message, Err}` lets the proposal graph turn some provider errors into outcomes (an invalid proposal). Every failure of an analysis ends its job, so each node returns a typed `*analysis.Error` and the handler maps it after the run, as it did inline. Errors keep their type through Eino (`errors.As`, tested).
4. **A panic stays the worker's panic.** Eino recovers a node's panic into an error whose text holds the panic value and the stack; returned as is, the worker would record a *retried* INTERNAL (another paid call when the panic came after the call) and log that text. The graph guards each node body, carries the panic out and re-raises it in the caller's goroutine with its own value, so the worker records INTERNAL, not retried, logs "job handler panicked" with only the value's type (or a runtime error's message) and the stack in its own field, exactly as before. (Review round 1, M1: as first built, that stack was the worker's and ended at `Run`, without the frame that panicked; the graph now raises an `analysis.Panic` carrying the stack where the node panicked, which the worker logs, §8.) That is how this slice meets §6.4's "a recovered panic becomes a typed INTERNAL error, and its stack never reaches the logs raw".
5. **Cancellation: Eino also stops between nodes.** *(Superseded by E6 review round 1, [E6_VALIDATION.md](E6_VALIDATION.md) §10: the graph now runs on a context that never ends and each step sees the attempt stop itself, as the proposal graph's do since E3's review round 1, so neither difference below remains; and the outcome was not the same: the worker records a handler's failure even while it stops, so a stopped attempt with bad input ended `WORKER_STOPPED`, retried, instead of its own final failure.)* Eino checks ctx before each step (`compose/graph_run.go`). Cancellation during the call (the case that matters: a call takes about 50 s) is byte for byte as before (golden `cancelled-during-the-call`). Two differences remain, only in an attempt that is already stopping, with the same outcome (nothing stored, nothing spent, the worker records `WORKER_STOPPED`, or nothing when the lease is lost): (a) cancelled before the call: before, the client was reached and refused before any I/O or reservation, and one `ai provider call ended` line (code `AI_CANCELLED`, empty labels) was written; now no node runs and no line is written (`TestAnalyzeCancelledBeforeTheCall` passes on both versions); (b) cancelled after the call answered and before `ground`: before, the check ran and `Finish` returned the stop; now the run returns Eino's cancellation (tested in the graph's `TestCancellation`, which cancels from the call's observer); the `job attempt failed` line's error text differs. PROPOSED for acceptance (§6).
6. **No callback handler.** A graph-level handler would see the graph's input (the IR, the results) and output (the analysis) (E1 finding 4). None is attached, per run or globally; `Run` takes no options. E6's source test against global handlers will hold it.
7. **The worker links Eino now.** `cmd/worker` is the first product binary that links Eino v0.9.21 and sonic v1.15.0 (compat path); it writes H-064's line once at start (seen with an empty environment, and pinned by the new worker test). `cmd/api`, `cmd/ai-eval-live`, `cmd/migrate` and `cmd/provision-tenant` link none of Eino's modules. `internal/api`'s closed-loop tests link Eino through the analyzer, which E0's rules allow (`no-eino-in-the-api-tests` holds `cmd/api` only).
8. **govulncheck ran** (ADR-031 asked the first slice that links Eino): v1.9.0, built from the module cache into the session scratchpad; DB updated 2026-10-27; source mode on `./...`: 0 vulnerabilities affect the code; 2 in a required module the code does not call, `golang.org/x/net` v0.60.0 (GO-2026-6866, GO-2026-6860, fixed in v0.62.0), brought by OpenTelemetry's gRPC exporter, not by Eino.
9. **The client's per-organization slots apply to the graph as before** (2 calls in flight per organization in one process, `provider.DefaultBudget`). A first concurrent test without raising them saw 2 of 16 runs refused `concurrency_limited`: the client's check working; the test now raises the slots to the cap.
10. **One mutant survived the first test set** (the model's names not given to `analyst.Check`): no answer quoted a model name holding a digit. `TestTheCheckGetsTheModelsNames` was added; it is killed now (§3).
11. **`make backend-test` hit Go's default 10-minute timeout in `scripts/check-architecture` again** (as in E1, finding 6): 89 packages passed; the checker's package passed alone with `-timeout 30m` in 864 s. Adding `-timeout` to the target stays PROPOSED.

## 3. Tests

Written before the change (commit 71e1171, on the code before E2), passing unchanged after it:

- `TestAnalyzeParity` (package `analyzer`, no database): 18 paths of `analyze` against `testdata/analyze-goldens.json`: explained beside a baseline (zh-Hant, a question), alone (en), and metered (a memory meter: the charge's organization, role, model, attribution and amount, the settlement); the version, a result, the patch or the baseline's result that does not decode; the domain not served; the runs that cannot be compared (the baseline's stored result is another run's: the `ERROR` line with the comparison's error) and that are not comparable (`MODEL_VERSION_MISMATCH`); the context too large and one that cannot be built (`WARN` line with `invalid_locale`); the provider refusing, unavailable, failing with an error not its own; an answer the schema refuses; an ungrounded answer (`ANALYSIS_NUMBER_UNVERIFIED at /scenario/summary`, 1 issue); cancelled during the call. Each records the result (analysis, failure or other error), the failure's code, message and retry decision or the error's text, the metadata (latency zeroed), the issue count, the checked analysis, every request the fake was sent (role, template version, locale, input hash, schema, the SHA-256 of the system and user texts, max tokens, model) and every log line (JSON, time and latency removed).
- `TestAnalyzeOutcomes`: the same paths checked one by one, so a wrong golden cannot hide them (no request before the call; metadata only after a call).
- `TestAnalyzeCancelledBeforeTheCall`: a cancellation, not a job failure, no metadata, no request, nothing reserved.

Written with the graph:

- `TestTheGraphIsTheSteps`: the graph and the same steps called directly send byte-identical requests and return the same analysis and metadata, beside a baseline and alone; the observer sees the one call.
- `TestFailuresAreTyped`: 13 failures, each an `*analysis.Error` of its kind with its cause (`compare.ErrInconsistent`, `aicontext.TooLarge`, the client's own `*provider.Error`, not the adapter's `CallError`), reasons, metadata (none before a call) and issues; no request and no observed call before `generate`; no question or answer text in the error.
- `TestTheCheckGetsTheModelsNames`, `TestNoComparisonWithoutABaseline`, `TestCancellation` (before the run: nothing runs; during the call: the client's cancelled error with its metadata, observed once; after the call answered: observed as answered, then a cancellation at the next node, not an `*analysis.Error`), `TestAPanicIsTheCallersPanic` (in the comparison and in the pack lookup: the caller recovers the node's own value; nothing is asked), `TestNewChecksTheConfiguration`, `TestConcurrentRuns` (16 runs on one compiled graph, `-race`), `TestErrorTextsNameTheKind`.
- `cmd/worker` `TestTheWorkerWritesOneLineThatIsNotJSON`.

Mutants (one change at a time, the analyzer's parity tests and the graph's tests run, the file restored byte for byte; scripts `e2/mutate.py` and `e2/mutate_analyzer.py` in the session scratchpad):

- Graph (`graph.go`), **12 of 12 killed** (after finding 10): the attribution dropped; the grounding skipped; the observer not told of an answer; not told of a failure; `Comparable` not checked; the question dropped; the panic guard removed; the names not given to the check (survived the first set, killed by the added test); the baseline and comparison not given to the check; the adapter's `CallError` not unwrapped; the comparison not sent; the patch not decoded.
- Handler (`analyzer.go`), **6 of 6 killed**: metadata kept on a cancelled call; the comparison's failure not logged; the issues not counted; the baseline dropped; the call's metadata dropped on a provider failure; a context too large mapped as invalid input.

## 4. Commands and results

Toolchain: go1.27.3 linux/amd64 (`PATH=~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH`), Python from `.venv` (symlinked from the main checkout, not committed). No live AI call; no `.env` read; no key used. The database: a throwaway PostgreSQL 16 cluster in the session scratchpad (`e2/pg.sh`, 127.0.0.1:55522, no Unix socket), stopped afterwards.

| Command | Exit | Result |
|---|---|---|
| `git merge --ff-only claude/lucid-meitner-vq1awo` | 0 | 24d233b → 31ab222 (E0, E1, W1); no merge commit |
| Tests first: `GEMINUS_WRITE_ANALYZER_GOLDENS=1 go test -count=1 -run TestAnalyzeParity -v ./internal/analyses/analyzer/` on the code before E2 | 0 | `wrote testdata/analyze-goldens.json (18 cases)` |
| `go test -count=3 -run TestAnalyze -v ./internal/analyses/analyzer/` (same code) | 0 | 3 × `TestAnalyzeParity`, `TestAnalyzeOutcomes`, `TestAnalyzeCancelledBeforeTheCall` PASS (deterministic); committed as 71e1171 |
| `go test -count=1 ./internal/aigraph/analysis/` (first run of the new graph) | 1, then 0 | `TestConcurrentRuns`: 2 of 16 runs `call_failed` (the client's 2 slots per organization, finding 9); the test raises the slots; then all 8 tests PASS |
| `go test -count=1 -run TestAnalyze -v ./internal/analyses/analyzer/` after the move | 0 | the 3 tests PASS against the unchanged goldens |
| `python3 -I e2/mutate.py`, first set | 0 | 11 of 12 killed; "names not passed to the check" survived (finding 10) |
| `go test -count=1 -run TestTheCheckGetsTheModelsNames -v ./internal/aigraph/analysis/` | 1, then 0 | first with "7 items wait." as the ungrounded case: grounded, since 7 is supplied (the seed); with 4242: PASS |
| `python3 -I e2/mutate.py` | 0 | 12 of 12 killed, `graph.go` restored |
| `python3 -I e2/mutate_analyzer.py` | 0 | 6 of 6 killed, `analyzer.go` restored |
| `make architecture-check` | 0 | `PASS architecture: 104 packages, 40 rules, 16 build configurations` (E1: 103) |
| `GOFLAGS=-mod=mod go mod tidy -diff` | 1 | only `go.sum` (dependencies' test-module checksums, as in E1; not applied); `go.mod` unchanged |
| `make backend-build`; `go version -m` of each binary, modules matching cloudwego, bytedance, gonja, logrus, eino-contrib | 0 | `bin/worker` 6 (eino v0.9.21, sonic v1.15.0, …); `bin/api`, `bin/ai-eval-live`, `bin/migrate`, `bin/provision-tenant` 0 |
| `env -i ./bin/worker`; `env -i ./bin/api` | 1, 1 | the worker: the sonic line, then `{"level":"ERROR","msg":"invalid configuration",…}`; the API: the JSON line only |
| `go test -count=1 -run TestTheWorkerWritesOneLineThatIsNotJSON -v ./cmd/worker/` | 0 | PASS (4.6 s) |
| `go test -race -count=1 ./internal/aigraph/... ./internal/analyses/... ./cmd/worker/` | 0 | all `ok` (`cmd/worker`'s DB tests skip without a database here; they ran under `make integration-test`) |
| `go test -race -count=3 -run TestConcurrentRuns ./internal/aigraph/analysis/` | 0 | `ok` |
| `make ai-eval` | 0 | TestAnalystEval, TestFakeProviderEval, TestReviewEval PASS; `git diff -- checks/ai-evals/` empty: the three fake reports byte-identical |
| govulncheck v1.9.0 (`go build` of `golang.org/x/vuln/cmd/govulncheck` from the module cache, `GOPROXY=off`), `govulncheck ./...`, then `-show verbose` | 0 | "Your code is affected by 0 vulnerabilities"; 2 in required modules not called (finding 8) |
| `e2/pg.sh start`, `createdb dt_test_e2a` | 0 | PostgreSQL 16, fresh database |
| `make integration-test` (`GEMINUS_TEST_DATABASE_URL`, `DATABASE_URL` = `dt_test_e2a`, `DT_ALLOW_DB_TESTS=yes`) | 0 | `make db-test`: "DB migration smoke up/down/up completed" and the SQL tests; `make db-go-test` (`GEMINUS_REQUIRE_DB_TESTS=1`, `-race`, `-p 1`): 39 packages `ok`, 0 failed, 0 skipped, among them `internal/api` (274.5 s: `TestAnalysisClosedLoop`, `TestAnUngroundedAnalysisFails`, `TestAnalysisRefusals`, the analyzer now run through the graph), `internal/analyses/analyzer` (the parity tests), `internal/worker`, `cmd/worker` (with W1's lane DB tests) and `cmd/api` (load average 20 to 60 from parallel agents). A comment-only edit of `analyzer.go` (`New`'s comment) and a new test case in `internal/aigraph/analysis` (not in this target) were made while it ran; the analyzer's tests ran again afterwards in `make backend-test` |
| `e2/pg.sh dropdb dt_test_e2a`, `list`, `stop` | 0 | dropped, no `dt_test_*` left, the cluster stopped |
| `go test -count=1 -run TestCancellation -v ./internal/aigraph/analysis/` (after adding the "after the answer" case) | 0 | PASS |
| `make backend-test` | 2 | gofmt clean, `go vet` clean, `GEMINUS_FULL_DIFFERENTIAL=1 go test`: 89 packages `ok` (among them `internal/aigraph/analysis`, `internal/analyses/analyzer`, `cmd/worker` with the new stderr test), 14 without test files; only `scripts/check-architecture` failed: `panic: test timed out after 10m0s` in `TestProbesAreCaught` (E1's finding 6; load average about 40 from parallel agents). Wall time 650 s |
| `GEMINUS_FULL_DIFFERENTIAL=1 go test -count=1 -timeout 30m ./scripts/check-architecture/` (alone, as the task allows) | 0 | `ok github.com/kcl1104/geminus/scripts/check-architecture 864.107s` (over the 10-minute default again, under load; finding 11) |
| After the last test edit (a no-op buffer removed from `TestErrorTextsNameTheKind`): `gofmt -l` of the touched packages, `go vet` and `go test -race -count=1 ./internal/aigraph/... ./internal/analyses/... ./cmd/worker/` | 0 | clean; all `ok` |
| `make architecture-check`, `make ai-eval` on the final tree | 0, 0 | `PASS architecture: 104 packages, 40 rules, 16 build configurations`; the three suites PASS, `checks/ai-evals/` unchanged |
| `PATH=.venv/bin:$PATH make spec-check` (first run, then on the final tree) | 0, 0 | every stage PASS (contracts `PASS_LIMITED_SPEC_CHECKS`, planning, docs `PASS` 172 Markdown files and 337 relative links, SQL inventory, reference arithmetic, engine vectors, decisions, TS contracts fresh, hashes, DB tests fresh, deploy); the only machine-written change is `checks/docs-check.json` |

## 5. NOT RUN

- Any live AI call (a live explanation through the graph; T-030's live check). The analyst's live eval was already NOT RUN (AI_SPEC §10).
- `make backend-race` in full (only the touched packages, and the DB packages under `make integration-test`, ran with `-race`).
- `make frontend-test`, `make e2e-test`: no web change.
- `make build-containers`, `make staging-smoke`, a deploy: the worker image now contains Eino; not built here.
- An independent review of this slice.

## 6. PROPOSED (for the coordinator or the owner)

- ~~Finding 5: the two cancellation differences in attempts that are already stopping (one `ai provider call ended` line with `AI_CANCELLED` and empty labels no longer written for a call that was never made; the `job attempt failed` error text after a call that answered as the attempt stopped). The alternative, running the graph on a context Eino cannot see cancelled and handing the real one to the call through the input, keeps those lines but hides cancellation from Eino between nodes, against §6.4's "ctx and cancellation reach every node".~~ Withdrawn by E6 review round 1 ([E6_VALIDATION.md](E6_VALIDATION.md) §10): there is no difference left.
- Finding 4: re-raising a node's panic in the caller's goroutine (the worker's existing panic path) as the meaning of §6.4's "typed INTERNAL error"; E3 and E5 may want the same guard (it is private to `internal/aigraph/analysis` here; E6 can share it).
- Finding 8: `golang.org/x/net` v0.62.0 (after ADR-011's two-week rule), outside this slice.
- Finding 11: `-timeout` on `make backend-test` (E1's finding 6, unchanged).
- ADR-031's "As built in E2" wording.

## 7. Risks

- `cmd/worker` links Eino from this slice on: its start writes the sonic line in every deployment (accepted in H-064); a log pipeline that parses every line as JSON sees it. The worker image grows by Eino's closure.
- The analyzer's parity goldens record the fake's fixed answer texts and request hashes. A later change to a prompt template or the fake moves them, as it moves the provider request goldens; they must then be rewritten from the code before that change, not after.
- E3 and E5 change `cmd/worker`'s linkage the same way and touch the same documents (ADR-031, ARCHITECTURE §2, `checks/README.md`, `checks/docs-check.json`): merge conflicts there are expected and textual.
- The race window of finding 5 (b) is tested at the graph's level only (the observer cancels); through the handler the observer is `logCall`, so the handler's side of it (`analyze` returning Eino's cancellation, then the worker's record) is argued from the code (§2), not run. *(E6 review round 1: the window is now tested through the handler as well, `TestAStopAfterTheAnswerStillChecksIt`, which stops the attempt as `logCall` writes the call's line; the answer is checked, as before the graph.)*

## 8. Review round 1

Two findings: one Medium, fixed; one Low, fixed with tests (the code was already right). No finding disputed.

### M1: a node's panic lost the stack where it happened

- **Problem.** `guard` recovered a node's panic and kept only its value, and `Run` raised that value again. So the worker's `debug.Stack()` (in `worker.handle`) started at `analysis.(*Graph).Run`, and the frame that panicked was gone from the `job handler panicked` line. That frame could be a nil map write in `aicontext.Analysis`, `compare.Compare` or `analyst.Check`, and that line is the only evidence of a bug that is not retried. Finding 4 (§2), the package comment and ADR-031's "As built in E2" all claimed the stack was the same as before. Reproduced with an overlay probe (below): on c92fc12, `origin frame present in stack = false`.
- **Fix.**
  - `internal/aigraph/analysis`: `guard` takes the stack of the goroutine that panicked at the moment it recovers. A deferred call runs before the frames unwind, so that stack still holds the frame that panicked. It uses `runtime.Stack`, formatted as `runtime/debug.Stack` formats it: `runtime` is in the graph's allowed imports (`aigraph-has-no-storage`) and `runtime/debug` is not. `Run` now raises an exported `analysis.Panic{Value, Stack}` instead of the bare value. Its `String` names only the value's type, because the value may hold data and the stack belongs in the worker's own field.
  - `internal/worker/handler`: a new `Reraised` interface (`PanicValue() any`, `PanicStack() string`). It is a panic raised again after code between the handler and the frame that panicked recovered it. `Handler`'s comment now says how the worker records a panic.
  - `internal/worker`: `handle` records a `handler.Reraised` with the value and the stack it carries (`recovered`). The log line's `panic` field is computed from that value as before: a runtime error's message, otherwise only its type. Its `stack` field is the stack where the code panicked. A Reraised whose methods panic, or that carries no value or no stack, is recorded like any other value, with the worker's own stack. Its methods are called under their own `recover`, so a broken one cannot stop the worker.
  - `internal/analyses/analyzer`: `var _ handler.Reraised = analysis.Panic{}`. If the graph's panic and the worker's contract drift apart, the build breaks. The two cannot import each other: `aigraph-has-no-storage`'s module list keeps the graph from importing the worker's packages, and `aigraph-only-in-the-worker` keeps `internal/worker` from importing the graph.
  - Documents: the package comment and `Run`'s comment in `graph.go`, ADR-031's "As built in E2", and a note in finding 4 (§2).
- **What the worker logs is as before E2.** The `panic` text is the same (the value's type, or the runtime error's message), and the job is still INTERNAL and not retried. The stack names the frame that panicked, then the guard, Eino's frames, `Run`, `analyze`, `Handle` and `worker.handle`: the nodes of this DAG run in the caller's goroutine. The only difference from before E2 is the extra frames (Eino's and the guard's).
- **One existing test's expectation changed: `TestAPanicIsTheCallersPanic`.** It was written in E2 (c92fc12) with the graph, and it is not one of the parity tests written before E2. It asserted that the caller recovers the node's bare value, which is exactly the defect. It now asserts more:
  - the caller recovers an `analysis.Panic` whose value is the node's own;
  - its stack starts with `goroutine ` and names the frame that panicked (`analysis_test.comparePanics(` and `analysis_test.panicking.Lookup(`);
  - formatted (`%v`, `%s`), the Panic shows neither the value's data nor the stack.

  No test, golden, InputHash or eval report from before E2 changed.

### L1: no test pinned that the observer gets the caller's context

The code was right: `n.called(ctx, ...)` at both call sites. A mutant that passes `context.Background()` survived both test packages, as the reviewer found. Two new tests kill it:

- `analysis.TestTheObserverGetsTheCallersContext`: a value the caller sets on ctx reaches `Config.Called` exactly once, for an answered call and for a failed one.
- `analyzer.TestTheCallLinesCarryTheJobsCorrelation`: it logs through `observability.NewHandler` (the process's handler), with `observability.With(ctx, organization_id, job_id)` set as the worker sets it. These lines each carry `job_id` and `organization_id`:
  - the `ai provider call ended` line of an answered call;
  - the `ai provider call ended` line of a refused call;
  - the `settling a live AI spend reservation failed` line of a metered call whose ledger cannot settle.

### Tests (round 1, written first)

- New:
  - `internal/aigraph/analysis`: `TestTheObserverGetsTheCallersContext`.
  - `internal/analyses/analyzer` (`worker_internal_test.go`, no database): `TestTheCallLinesCarryTheJobsCorrelation`, and `TestAPanicReachesTheWorkerWithItsStack`. In the second, a pack lookup panics: `analyze` raises a `handler.Reraised` with the value and a stack that names `analyzer.panickingPacks.Lookup(`; nothing is asked of the provider and nothing is logged.
  - `internal/worker` (`outcome_internal_test.go`, no database): `TestAReraisedPanicKeepsItsValueAndStack`. A re-raised runtime error is recorded as `handler panicked: assignment to entry in nil map` with the stack it carries, and a re-raised string as `handler panicked with a string`. Each of these is recorded as the value itself, with the worker's stack and no sentinel in the text: a plain value, a runtime panic, a Reraised whose methods panic, one without a stack, one without a value, and a nil pointer to one.
- Changed: `TestAPanicIsTheCallersPanic` (M1, above).
- Before the fix: the worker test FAILED ("handler panicked with a worker.reraised", with the worker's stack), and the graph and analyzer tests did not compile (`analysis.Panic` and `handler.Reraised` undefined).

**Mutants: 9 of 9 killed.** Each was run against the tests from before the round (the new ones skipped; every mutant passed them) and against the round's new tests (every mutant failed them). Files were restored byte for byte. The script is `e2r1/mutate.py` in the session scratchpad.

| Mutant | Old tests | New tests |
|---|---|---|
| the observer gets `context.Background()` on a failed call | pass | FAIL |
| the observer gets `context.Background()` on an answered call | pass | FAIL |
| `guard` keeps no stack | pass | FAIL |
| `Run` raises the bare value | pass | FAIL |
| `String` formats the value | pass | FAIL |
| the worker ignores a Reraised | pass | FAIL |
| the worker keeps its own stack | pass | FAIL |
| the worker does not guard a broken Reraised | pass | FAIL |
| the worker takes an empty stack | pass | FAIL |

**Probe: the reviewer's scenario.** An overlay test in package `worker`, with nothing written to the worktree (`go test -overlay`; scratchpad `e2r1/zz_probe_test.go`). An analysis graph whose comparison calls a function that writes to a nil map is run by a handler under `worker.handle`, and the result is read as the loop reads it.

- With c92fc12's `graph.go`, `worker.go` and `handler.go` overlaid: the panic text is kept (`handler panicked: assignment to entry in nil map`), but `origin frame present in stack = false`, and the stack begins at the worker's `debug.Stack`.
- With the fix: the same text, `origin frame present in stack = true` (`worker.probeWritesANilMap(`), and the stack also names `worker.(*Worker).handle`.

### Commands and results (round 1)

Same toolchain and environment as §4. No live AI call, no `.env` read, no key used.

| Command | Exit | Result |
|---|---|---|
| Tests first: `go test -count=1 -run 'TestAReraisedPanicKeepsItsValueAndStack\|TestPanickedTrustsOnlyTheRuntime' ./internal/worker/` before the fix; `go vet` of the graph and analyzer packages | 1, 1 | the worker test FAILED as intended (the value and stack of `worker.reraised`); the graph and analyzer tests did not compile (`undefined: analysis.Panic`, `undefined: handler.Reraised`) |
| After the fix: the six tests (`-run` the new and changed ones, `-v`) in `./internal/aigraph/analysis/ ./internal/analyses/analyzer/ ./internal/worker/` | 0 | all PASS |
| `python3 -I e2r1/mutate.py` | 0 | 9 of 9 killed, `graph.go` and `worker.go` restored |
| Probe on c92fc12's sources (overlay), then on the fix | 1, 0 | `origin frame present in stack = false`, then `true` |
| `gofmt -l internal/ cmd/ scripts/`, `go build ./...`, `go vet ./internal/aigraph/... ./internal/analyses/... ./internal/worker/... ./cmd/worker/` | 0 | clean |
| `go test -count=1 ./internal/aigraph/... ./internal/analyses/... ./internal/worker/... ./cmd/worker/` | 0 | all `ok` (the DB tests of `internal/worker` and `cmd/worker` skip without a database here; they ran under `make integration-test`) |
| `go test -race -count=1` of the same packages | 0 | all `ok`; `internal/worker` was run again under `-race` after the last test edit (one `var _ handler.Reraised = reraised{}` line) |
| `go test -count=1 -run TestAnalyze -v ./internal/analyses/analyzer/` | 0 | `TestAnalyzeParity`, `TestAnalyzeOutcomes`, `TestAnalyzeCancelledBeforeTheCall` PASS; `testdata/analyze-goldens.json` untouched |
| `make architecture-check` | 0 | `PASS architecture: 104 packages, 40 rules, 16 build configurations` |
| `make ai-eval` | 0 | TestAnalystEval, TestFakeProviderEval, TestReviewEval PASS; `git diff --quiet -- checks/ai-evals/`: the three fake reports byte-identical |
| `make backend-test` (scratchpad `e2/backend.sh r1`) | 2 | gofmt clean and `go vet` clean. `GEMINUS_FULL_DIFFERENTIAL=1 go test`: 89 packages `ok` (among them `internal/aigraph/analysis`, `internal/analyses/analyzer`, `internal/worker`, `cmd/worker`), 14 without test files. Only `scripts/check-architecture` failed: `panic: test timed out after 10m0s` in `TestProbesAreCaught` (finding 11 again; load average 12 to 19, with `make integration-test` started beside it for its last minutes). Wall time 609 s. The target passes no `-count=1`, so packages the round did not touch reused Go's test cache |
| `make integration-test` on a fresh `dt_test_e2r1` (E2's throwaway PostgreSQL 16 cluster, `e2/pg.sh`, 127.0.0.1:55522, no Unix socket; `GEMINUS_TEST_DATABASE_URL` = `DATABASE_URL`, `DT_ALLOW_DB_TESTS=yes`) | 0 | `make db-test`: "DB migration smoke up/down/up completed" and the SQL tests. `make db-go-test` (`GEMINUS_REQUIRE_DB_TESTS=1`, `-race`, `-p 1`, `-count=1`): 39 packages `ok`, 0 failed; among them `internal/api` (203.2 s, with the analysis closed loop through the graph), `internal/analyses/analyzer`, `internal/worker` (44.5 s: the loop's handler-failure table, its panics included) and `cmd/worker` |
| `e2/pg.sh dropdb dt_test_e2r1`, `list`, `stop` | 0 | dropped; no `dt_test_*` left; the cluster stopped |
| `GEMINUS_FULL_DIFFERENTIAL=1 go test -count=1 -timeout 30m ./scripts/check-architecture/` (alone, as the task allows) | 0 | `ok github.com/kcl1104/geminus/scripts/check-architecture 1117.077s`. Over the 10-minute default again, under a load average of 21 to 38 from other sessions (finding 11) |
| `PATH=.venv/bin:$PATH make spec-check` (`e2/spec.sh r1`, then `r1-final` on the final tree) | 0, 0 | every stage PASS: contracts `PASS_LIMITED_SPEC_CHECKS`, planning, docs `PASS`, SQL inventory, reference arithmetic, engine vectors, decisions, TS contracts fresh, hashes, DB tests fresh, deploy. `checks/docs-check.json` is unchanged (no Markdown file or relative link added) |

### NOT RUN (round 1)

As §5: any live AI call, `make backend-race` in full (only the touched packages, and the DB packages under `make integration-test`, ran with `-race`), `make frontend-test` and `make e2e-test` (no web change), container builds, staging smoke and any deploy. govulncheck was not run again: no dependency changed (`go.mod` and `go.sum` untouched). An independent review of this round's fix is also NOT RUN.

### PROPOSED and risks (round 1)

- PROPOSED (this updates §6's finding 4 item): `handler.Reraised` as the worker's contract for every role graph that re-raises a node's panic (E3 proposals, E5 photo, H-061's investigator). Each will need the same guard. E6 can move `guard`, `stack` and `Panic` into a shared `internal/aigraph` package; the worker's side needs no change.
- Finding 11 (`-timeout` on `make backend-test`) still stands: the target hit the 10-minute default again. On the main branch, 3c26a1b has since given it a 30-minute timeout; that commit is not in this worktree.
- Risk: the stack carried is the stack of the node's goroutine. This DAG runs its nodes one after the other in the caller's goroutine, and the probe's stack names `worker.(*Worker).handle`. A future graph whose nodes run in a goroutine Eino starts (parallel branches) would carry only that goroutine's stack: the frame that panicked, but not the worker's frames.
- Risk: the worker takes any value with the two methods as a re-raised panic. Only server code panics, and the carried stack goes through the log filter like any stack (`observability`, key `stack`, `KindStack`: sanitized, lines kept, cut at `MaxStack`).
