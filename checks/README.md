# checks/

This directory holds the record of how each piece of work in this repository was checked:

- **Validation reports** (`T-0xx_VALIDATION.md`, `H-0xx_*_VALIDATION.md` and a few named reports). The agent that did a task or slice writes one. Later sections are added by the coordinator, by reviewers and when the owner approves the decisions the task raised.
- **Independent review reports** (`*_REVIEW_r1.md`, `REVIEW_2026-10-15.md`). A reviewer who did not write the code writes these (H-003; in hackathon mode one round per slice, H-029). Many reviews are recorded inside the validation report instead of in a separate file.
- **Machine-written check outputs** (`*.json`, `spec-check.log`), listed [below](#machine-written-files).
- **[evidence/](evidence/)**: raw command output for each slice.
- **[ai-evals/](ai-evals/)**: fake and live AI eval and smoke results, and the live spend record.

The reports are written in Traditional Chinese with English terms. They are kept as recorded and are not translated.

Each line below says what the report itself records. This index ran nothing again. PASS, ACCEPT and NOT RUN mean what the report says. A report's opening status line gives the state when it was written. Later sections (review rounds, coordinator runs, owner approvals) can change that state, and this index gives the latest state the report records.

The task status of record is `planning/tasks.json`. Several hackathon slices are still `not_started` or `in_progress` there although their report shows the slice delivered: `scripts/check_decisions.py` does not let a task start before its dependencies are in `review` or `done`, and agents do not change task status on their own.

## How to read a report

| Term or heading | Meaning |
|---|---|
| Header line | Date, status, requirement IDs (REQ-), decision gates (D-, H-), acceptance tests (TEST-) |
| 實際執行結果, 指令與結果, 命令與結果 | The commands that were run and their exit codes |
| NOT RUN, NOT RUN／延後 | What was not run, and why or which task owns it |
| 殘餘風險, 剩餘風險, 殘留風險, 風險 | Remaining risk |
| Review 第 N 輪 | Independent review round N: ACCEPT or CHANGES REQUESTED, with findings graded High, Medium, Low, Info (H1, M1, L1, I1) |
| Coordinator 的執行, Coordinator 驗證 | The coordinator merged the slice and ran the checks again, often the database tests on a disposable PostgreSQL database |
| Owner 批准 | The owner approved the decisions the task proposed |
| 先紅後綠 | A failing test was written first, then the fix |
| Live 確認 | A check on the deployed site on Railway |

## Spec package, bootstrap and early review

| Report | What it covers | Result recorded | Open items |
|---|---|---|---|
| [VALIDATION_REPORT.md](VALIDATION_REPORT.md) | Static checks of the received specification package (handoff-0.1.0, 2026-10-15) | The limited static checks passed. The product was not implemented. | At that time NOT RUN: full OpenAPI validation and SDK codegen, SQL parsing, migrations, RLS, Go, frontend, SimPy differential, live AI, real hashes, performance and staging (the package had no application) |
| [BOOTSTRAP_VALIDATION.md](BOOTSTRAP_VALIDATION.md) | T-001 and T-002: repository, toolchain and CI skeleton | The listed checks passed. `make spec-check` on the package as received first failed (exit 2, CB-34) until the validator pins were fixed. Status `review`; no product features yet. | NOT RUN: platforms other than linux/amd64, live AI, integration, E2E, security scan, performance, staging, full OpenAPI codegen, en_US collation |
| [REVIEW_2026-10-15.md](REVIEW_2026-10-15.md) | Independent adversarial review and acceptance of T-004, T-005, T-006, T-007, T-011 and T-014 by agents, at the owner's instruction (H-003). The owner did not review line by line. | Every CONFIRMED finding fixed with a failing test first and checked by mutation; CI 4/4 success | Some items handed to later tasks (T-013, T-019, T-020); two design limits in T-006 |
| [T-042_VALIDATION.md](T-042_VALIDATION.md) | Dependency guard between the generic kernel and adapters (`make architecture-check`) | `done`. Rounds 1 and 2 CHANGES REQUESTED; round 3 ACCEPT; its Low findings fixed. | At that time the frontend part of TEST-025 waited for T-015, T-033 and T-034, and the `ai-suggests-only` rule for `internal/ai` (T-024) |

## Contracts and the model service

| Report | What it covers | Result recorded | Open items |
|---|---|---|---|
| [T-003_VALIDATION.md](T-003_VALIDATION.md) | Froze the candidate contracts (typed server IDs, closed error codes, analysis output, server and provider risk fields) and the Go and TypeScript domain types | All listed commands exit 0; status `review` when written | SQL alignment moved to T-006; HTTP envelope types to T-012, T-013, T-025; SDK codegen with external `$ref` not verified |
| [T-004_VALIDATION.md](T-004_VALIDATION.md) | IR and JSON Patch semantic validator | `done` (H-003). Tests, race and fuzzing pass. The 500-node benchmark is a data point, not the T-037 performance acceptance. | Dataset binding (T-005), risk classification (T-025), RunConfig checks (T-020, T-023), performance gate (T-037) |
| [T-005_VALIDATION.md](T-005_VALIDATION.md) | JCS canonical hashing and the frozen dataset manifest | `done`. The Go hashes match an independent Python `rfc8785` implementation. | Binding speed of large snapshots; digests checked only on linux/amd64; the jcs library is a single-maintainer fork |
| [T-012_VALIDATION.md](T-012_VALIDATION.md) | Model, draft and version service: one transaction for model, draft and v1; draft revision compare-and-set; immutable versions; stale writes rejected | `done`. Round 1 CHANGES REQUESTED; same reviewer re-check ACCEPT. | At that time: HTTP routes (T-013), approval (T-026), runs (T-023), real dataset storage (T-016) |
| [T-013_VALIDATION.md](T-013_VALIDATION.md) | Model HTTP contract and TypeScript client: If-Match and ETag, canonical IR text (D-040), publish comment (D-041) | `done`. Round 1 CHANGES REQUESTED; round 2 ACCEPT. | At that time: editor UI (T-015), browser E2E (T-040), real identity provider |
| [T-054_VALIDATION.md](T-054_VALIDATION.md) | A failed dataset snapshot read returns 503 (H-022) in publishVersion, createSimulation, patchDraft, resetDraft and the draft sync at approval | Unit tests pass. DB tests NOT RUN in the agent's worktree; the coordinator ran them. Review round 2 ACCEPT; marked done. | Two Info items moved to T-039: a storage adapter's own timeout gives a 503 without details or log; one generator error has the same type as a read error |

## Database, tenancy, identity and jobs

| Report | What it covers | Result recorded | Open items |
|---|---|---|---|
| [T-006_VALIDATION.md](T-006_VALIDATION.md) | Core migration (ID domains, object key checks, risk columns) and the repository foundation | `done`. `make db-test` passed on C and ICU en-US databases. | Other ICU locales not tested; cross-tenant protection relies on composite foreign keys |
| [T-007_VALIDATION.md](T-007_VALIDATION.md) | Row-level security, runtime roles and connection-pool isolation | `done`. DB tests and race tests pass; mutation run. | At that time: concurrent last-owner race (T-010), leases (T-019), user from the JWT (T-008) |
| [T-008_VALIDATION.md](T-008_VALIDATION.md) | OIDC and JWT verification in the API (ADR-017) | `done`. Round 1 CHANGES REQUESTED; re-check ACCEPT; 114 of 114 mutants caught. | At that time: no Auth0 tenant, so only a test provider; full route-by-role matrix later |
| [T-009_VALIDATION.md](T-009_VALIDATION.md) | Project service, server RBAC and the shared HTTP layer (ADR-019); adds `make integration-test` | `done`. Round 1 CHANGES REQUESTED; re-check ACCEPT. | Rate limiting and tracing (T-035, T-036); other routes of the TEST-009 matrix |
| [T-010_VALIDATION.md](T-010_VALIDATION.md) | Operator-only onboarding, owner-only membership management, the last-owner lock (D-042) | `done`. Round 1 CHANGES REQUESTED; round 2 ACCEPT. | At that time: closing SSE streams on revocation (TEST-026, T-023), member UI, real identity provider |
| [T-019_VALIDATION.md](T-019_VALIDATION.md) | PostgreSQL job queue, leases and the worker loop (ADR-024) | Rounds 1 to 3 CHANGES REQUESTED; round 4 ACCEPT | Parameter benchmarks (T-037, T-039); several workers on several machines and database failover not tested |
| [T-035_VALIDATION.md](T-035_VALIDATION.md) | Audit, OpenTelemetry and structured logs (ADR-028) | DB tests run by the coordinator; review round 2 ACCEPT; owner approved D-091 to D-095 and ADR-028 (H-024) | Log redaction rules are heuristic; the header checks follow one OTel version |

## Domain packs

| Report | What it covers | Result recorded | Open items |
|---|---|---|---|
| [T-011_VALIDATION.md](T-011_VALIDATION.md) | SoftwareDeliveryDomainPack and its full synthetic template (Source, Review, CI, Test, Deploy, Sink) | `done`. Mutants all caught; govulncheck found no vulnerabilities. | Event inference (T-027); DORA metrics need mapped pilot data, not in P0 |
| [T-046_PHASE2_VALIDATION.md](T-046_PHASE2_VALIDATION.md) | Contract changes for the electronics line (schemas and fixtures; D-063) | Round 1 CHANGES REQUESTED; round 2 ACCEPT; owner approved D-063 (H-019) | The agent could not run `make frontend-test` (Node 22); the coordinator did. Semantic fixtures were derived by hand before the rules existed. |
| [T-051_VALIDATION.md](T-051_VALIDATION.md) | Hackathon slice: electronics-line DomainPack, full template and metrics | Delivered. Review CHANGES REQUESTED (M1); the fix was verified by the coordinator. D-117 PROPOSED. | Declarative metrics (D-058) still refused as not supported; full `make backend-race` and `make e2e-test` NOT RUN; Low findings deferred until after the hackathon |
| [T-051_REVIEW_r1.md](T-051_REVIEW_r1.md) | Review round 1 of the T-051 slice | CHANGES REQUESTED: one Medium (M1), no High | Low findings recorded as post-hackathon follow-ups |
| [T-052_VALIDATION.md](T-052_VALIDATION.md) | Hackathon slice: synthetic line data recorded from the engine's own runs, and a simulated-versus-observed comparison | Delivered. Review CHANGES REQUESTED (M1); fixes verified by the coordinator. D-118 PROPOSED. | D-073 transforms and dataset commit (T-018) not done. The data is synthetic ground truth from the same model, so the comparison checks the pipeline, not the model. |
| [T-052_REVIEW_r1.md](T-052_REVIEW_r1.md) | Review round 1 of the T-052 slice | CHANGES REQUESTED (M1), no High. Confirmed the generator only records the engine and gives the same bytes for the same seed. | Labeling of the data as synthetic (I2) |
| [H-046_ELECTRONICS_REALISM_VALIDATION.md](H-046_ELECTRONICS_REALISM_VALIDATION.md) | H-046 (1): the electronics line template's parameters researched and tuned to cited sources (or marked estimate); synthetic data set regenerated; the five demo what-ifs re-run on the engine. Written in English. | Delivered, not reviewed. Backend, frontend and spec checks as listed in the report. | NOT RUN: e2e, integration, live. Open: "One more debug operator" is a no-op (shift crews), the demo's one-day horizon, no what-if at the FCT bottleneck |

## Simulation engine and runs

| Report | What it covers | Result recorded | Open items |
|---|---|---|---|
| [T-020_VALIDATION.md](T-020_VALIDATION.md) | DES compiler, clock, event heap and random number generation | `done` (H-003). After the review the platform `math.Log` was replaced with a fixed `ln`. The report corrects five over-statements of its first version. | Tests on real non-amd64 hardware NOT RUN (no claim of identical digests across architectures); SimPy differential (T-037) |
| [T-021_VALIDATION.md](T-021_VALIDATION.md) | Resource, queue, service and routing kernel | `done`. The review asked for changes (dispatch cost); re-check ACCEPT. The reviewer's random models found no kernel correctness error. | SimPy differential and fixed-hardware benchmarks (T-037); non-amd64 hardware |
| [T-022_VALIDATION.md](T-022_VALIDATION.md) | Metric aggregation, result digest, sampled trace and the engine's Run | `done`. Review asked for changes; same reviewer re-check ACCEPT. | At that time: run IDs and trace upload (T-023), comparison (T-029), SimPy and performance (T-037) |
| [T-023_VALIDATION.md](T-023_VALIDATION.md) | Run API, worker and cancellation | Review ACCEPT, no High; several findings fixed | Follow-ups: M1 (object reads and compile inside the run-creation transaction), L3 to L6, I2. Browser closed loop and benchmarks NOT RUN. |
| [T-047_VALIDATION.md](T-047_VALIDATION.md) | Finite buffers and blocking (D-049, D-050 R1 and B1) | Review round 2 ACCEPT; owner approved D-064 (H-021); DB tests run by the coordinator | The golden values were derived by hand by the same agent; the reviewer re-derived part of them |
| [T-048_VALIDATION.md](T-048_VALIDATION.md) | Resource breakdowns (MTBF and MTTR), lognormal and Weibull | Round 1 CHANGES REQUESTED (two Medium; D-074 (a) changed to option B); round 2 ACCEPT; owner approved D-074 and D-075 (H-022) | The repair-time stream on repair is covered only by regression locks and the SimPy tolerance |
| [T-049_VALIDATION.md](T-049_VALIDATION.md) | Shift calendars and time-varying release schedules | Round 1 CHANGES REQUESTED; round 2 ACCEPT; owner approved D-081 to D-085 (H-024) | The Python reference covers straight lines only; rule R1 has no independent implementation (T-037) |
| [T-050_VALIDATION.md](T-050_VALIDATION.md) | Batch processes (burn-in) and changeovers | Round 1 CHANGES REQUESTED (four Medium, three Low); fixes merged and checked by the coordinator. D-096 and D-097 still PROPOSED, so not done. | No later review round recorded; combinations with blocking, calendars and shared resources covered only by hand-derived goldens and checkers |

## Datasets and import

| Report | What it covers | Result recorded | Open items |
|---|---|---|---|
| [T-016_VALIDATION.md](T-016_VALIDATION.md) | CSV and JSON upload, profile, private storage, safe error-report download | Round 1 CHANGES REQUESTED (H1 memory, M1 to M3, L1, L2); fixed; re-review ACCEPT, one Low fixed afterwards | Orphan objects not cleaned up (D-044); no upload rate or tenant quota (T-036); the 50 MB / 100k-row target not measured (T-037) |
| [T-017_VALIDATION.md](T-017_VALIDATION.md) | Canonical mapping and the full validation pipeline (D-070 to D-072) | Round 3 ACCEPT; owner approved D-070 to D-072 (H-021); marked done | Orphan datasets in one stale-job case; validation time of 100,000 rows not measured (T-037) |

## AI proposals and providers

| Report | What it covers | Result recorded | Open items |
|---|---|---|---|
| [T-024_VALIDATION.md](T-024_VALIDATION.md) | Sanitized AI context and the provider adapter with fake, disabled and live modes (ADR-027) | Round 1 CHANGES REQUESTED (H1, M2, L3 to L8); fixed; re-review ACCEPT. No live AI call; the live adapter was tested only against a local test server. | Redaction is heuristic; concurrency limits are per process; no period quota |
| [T-025_VALIDATION.md](T-025_VALIDATION.md) | Asynchronous proposal generation with server validation and risk | Round 2 ACCEPT; owner approved D-061 and D-062, (i) with option B (H-019); done | No token quota per tenant and period (T-036) |
| [T-026_VALIDATION.md](T-026_VALIDATION.md) | Human approve and reject, and the canonical effect (exactly one new version, idempotent, audit in the same transaction) | Round 2 ACCEPT; DB tests run and passed by the coordinator; owner approved D-067 to D-069 (H-021); done | Approval rereads the dataset snapshot while it holds the model row (lock time with large manifests not measured); after a risk-rule change, existing ready proposals return 409 at approval |
| [T-053_VALIDATION.md](T-053_VALIDATION.md) | Turning live AI on (H-018): spend limits, approval record, live eval and live smoke | Round 1 CHANGES REQUESTED (one High, three Medium); round 2 ACCEPT. Live rerun with Claude: smoke exit 0; eval 49 of 50 cases passed, the other (MOD-050) failed safely as `invalid`; zero-tolerance gates 0. | D-110, D-111, D-113 to D-115 PROPOSED; no combined in-app cap for eval and worker spend |
| [T-055_VALIDATION.md](T-055_VALIDATION.md) | Meta Model API adapter (Muse Spark, SAM 3.1); ADR-030 and D-121 to D-123 PROPOSED | Review round 1 fixes verified by the coordinator. Live Meta smoke exit 0. The first eval was refused by the daily limit with no provider call; the rerun passed 48 of 50 cases with zero-tolerance gates 0. | Eval cases with a status other than expected are listed by ID; photo calls waited for T-056 |
| [T-055_REVIEW_r1.md](T-055_REVIEW_r1.md) | Review round 1 of the Meta adapter | CHANGES REQUESTED: one Medium (M1, documentation only: a wrong cost figure); no High or Medium in the code | Fixed in T-055 round 1 |
| [T-056_VALIDATION.md](T-056_VALIDATION.md) | Hackathon slice: photo or floor plan to a model proposal (SAM 3.1 finds boxes, Muse Spark judges stations and flows); photo availability in the API (D-124); D-130 to D-132 PROPOSED | Review round 1 CHANGES REQUESTED; a later review of the judge output cap ACCEPT. Live photo smoke reached `ready` (server risk medium) after the cap was raised to 32,000. A live run found an audit field limit (34 fields, limit 32), fixed and tested. | Recognition on real photos NOT RUN; a failure after the AI answer still makes the retried job call the AI again (Low, deferred) |
| [T-056_REVIEW_r1.md](T-056_REVIEW_r1.md) | Review round 1 of the photo path, the photo spend reservation (H-037) and API photo availability | CHANGES REQUESTED: two Medium (M1, M2), five Low, seven Info. The safety core held: the AI only proposes; the server validates and computes risk. | Fixed in T-056 |

## Scenarios, results, comparison and analysis

| Report | What it covers | Result recorded | Open items |
|---|---|---|---|
| [T-028_VALIDATION.md](T-028_VALIDATION.md) | Scenario persistence and parameter-only approval (D-078 to D-080) | DB tests and `make integration-test` run by the coordinator (exit 0); round 1 fixes rerun by the coordinator; owner approved D-078 to D-080 (H-022); done | Candidate warnings of a manual scenario are not stored; `listScenarios` reads the whole IR; storage errors still give 500 in two other routes at that time (later T-054) |
| [T-029_VALIDATION.md](T-029_VALIDATION.md) | Results and baseline comparison API (D-088 to D-090) | DB tests and `make integration-test` run by the coordinator (exit 0); owner approved D-088 to D-090 (H-024); done | Metric keys with newlines or long keys would break the contract (not reachable today); each `Resolve` validates the Result again |
| [T-030_VALIDATION.md](T-030_VALIDATION.md) | Hackathon slice: grounded AI analyst. An answer is kept only if each claim matches data the server supplied (D-133 PROPOSED). | Review rounds recorded no High; fixes made. Live: the first request failed with `context_too_large` before any paid call; after the fix and redeploy an answer was kept on the first try. | One live sample only; known limits of the text check recorded in D-133 (h) |

## Web UI and 3D

| Report | What it covers | Result recorded | Open items |
|---|---|---|---|
| [T-014_VALIDATION.md](T-014_VALIDATION.md) | Frontend app and server-state boundary: a single HTTP exit and one typed problem union | `done` (H-003) | At that time: no DOM tests for discarding stale responses; no sign-in yet |
| [T-044_VALIDATION.md](T-044_VALIDATION.md) | Bilingual UI foundation, English by default and zh-Hant (ADR-020) | `done`. Round 1 CHANGES REQUESTED; re-check ACCEPT. | At that time: language switch with a real session and with unsaved draft edits not verified; AI prose by locale (T-025, T-030) |
| [T-045_VALIDATION.md](T-045_VALIDATION.md) | Design system, UI library evaluation and UX gate (ADR-021); re-skin (H-027) | Status `in_progress` in the report header. The 32 screenshot baselines were made in CI and confirmed by the owner (H-028). Review round 1 CHANGES REQUESTED; fixes merged by the coordinator. | Screen-reader checks need a person; Firefox and Safari not run; some raw logs of the re-skin were not kept (L3) |
| [T-015_VALIDATION.md](T-015_VALIDATION.md) | Hackathon slice: React Flow 2D editor (JSON Patch edits, If-Match, a 409 keeps the edits, no automatic rebase) | Review CHANGES REQUESTED (M1); fixes verified by the coordinator. A CI failure of a focus check was a test timing race; the test now waits for focus, without a weaker assertion. | After the hackathon: client validation, a guard for unsaved changes, many properties not editable, undo and redo |
| [T-015_REVIEW_r1.md](T-015_REVIEW_r1.md) | Review round 1 of the 2D editor slice | CHANGES REQUESTED (M1). Confirmed the editor is a projection of the IR with no second model. | Fixed in T-015 round 1 |
| [T-031_VALIDATION.md](T-031_VALIDATION.md) | Hackathon slice: ask the AI for a change, review the proposal, approve an edit and a scenario separately | Review CHANGES REQUESTED (M1: names and invisible or bidirectional characters in the diff); fixes verified by the coordinator | The import and mapping part of T-031 not done; owner questions on invisible characters and name uniqueness in the IR |
| [T-031_REVIEW_r1.md](T-031_REVIEW_r1.md) | Review round 1 of the proposal review slice | CHANGES REQUESTED (M1). Confirmed the screen never computes risk and keeps edit and scenario approval apart. | Fixed in T-031 round 1 |
| [T-032_VALIDATION.md](T-032_VALIDATION.md) | Hackathon slice: run from a scenario, progress, Marey chart, key metric comparison | Review CHANGES REQUESTED (M1); fixes verified. After the live demo, per-station metrics moved into a collapsed section. | How blocking is drawn in the Marey chart is the agent's reading; at most 400 lines per run |
| [T-032_REVIEW_r1.md](T-032_REVIEW_r1.md) | Review round 1 of the results slice | CHANGES REQUESTED (M1). Confirmed every value, delta and status comes from the server. | Fixed in T-032 round 1 |
| [T-033_VALIDATION.md](T-033_VALIDATION.md) | Hackathon slice: read-only PlayCanvas 3D view that shares IR IDs, layout and selection with 2D | Review round 1 (no High, five Medium) fixed; browser checks pass; live check on Railway passed | Playback (T-034) and touch gestures not in the slice; the scene sits small and low in the frame |
| [T-034_VALIDATION.md](T-034_VALIDATION.md) | Hackathon slice: 3D playback of the sampled trace on the results page | Review (no High) fixed; browser checks pass; live check passed | About 1 in 8 items sampled; first replication only; truncated traces may miss events in the last tick |

## Sign-in, deployment and operations

| Report | What it covers | Result recorded | Open items |
|---|---|---|---|
| [T-043_VALIDATION.md](T-043_VALIDATION.md) | Browser sign-in session (BFF, ADR-018), `GET /v1/me` (D-045) and the signed-in UI bootstrap | Status `in_progress`. BFF review round 3 ACCEPT; D-045 slice ACCEPT; UI bootstrap reviewed (next row) and merged. | Owner questions: 8 screenshot baselines, the second line of the mobile strip, how long the organization choice is kept |
| [T-043_BOOTSTRAP_REVIEW_r1.md](T-043_BOOTSTRAP_REVIEW_r1.md) | Review round 1 of the signed-in UI bootstrap | ACCEPT: no High, Medium or Low; Info I1 to I6 only | None blocking |
| [T-039_VALIDATION.md](T-039_VALIDATION.md) | Hackathon slice: containers, migration command, S3 adapter, local staging composition and the Railway deployer (ADR-029 PROPOSED) | Delivered. Review CHANGES REQUESTED (M1, M2); fixes merged by the coordinator. | NOT RUN: `make build-containers` and `make staging-smoke` (no Docker daemon), `deploy.sh --apply` |
| [T-039_REVIEW_r1.md](T-039_REVIEW_r1.md) | Review round 1 of the deployment slice | CHANGES REQUESTED, no High: M1 (TCP proxy cleanup and exit code), M2 (owner variable values) | Fixed in T-039 round 1 |
| [T-057_AUTH0_VALIDATION.md](T-057_AUTH0_VALIDATION.md) | The deployer's Auth0 step (H-034), the judge account (H-035 (2)), and the record of the first live deployment: seven `--apply` runs and their fixes, then checks after deployment (live AI, full flow, photo flow) | Deployment completed on 2026-10-27; health checks, sign-in redirect, security headers and sealed secrets confirmed. The live text proposal, approval, runs and comparison worked; the photo proposal on the full template was rejected by the server rules, and on 2026-10-28 one on an empty line reached ready and was approved. | Recorded issues: demo content had to be created by hand (a seed gap); Meta answered 404 from some Railway exits, handled by moving or redeploying the worker; `GOMEMLIMIT` not set |
| [T-057_AUTH0_REVIEW_r1.md](T-057_AUTH0_REVIEW_r1.md) | Review round 1 of the Auth0 step, rebuilt from the coordinator's notes because the original review file was lost | CHANGES REQUESTED (M1, M2); Low L1 to L6 may wait | The coordinator made and checked the M1 and M2 fixes alone; no second person checked them |
| [T-057_WEB_VALIDATION.md](T-057_WEB_VALIDATION.md) | Data notice under the AI request field (H-035 (1)) and the web security headers | Agent self-check, not acceptance; the coordinator verified it (frontend tests pass) | At that time NOT RUN: headers after deployment, real Auth0 sign-in, the standalone Docker image |

## Hackathon polish and demo

| Report | What it covers | Result recorded | Open items |
|---|---|---|---|
| [T-057_DEMO_FIXES_VALIDATION.md](T-057_DEMO_FIXES_VALIDATION.md) | Three issues found in the live demo: canvas layout after AI-added steps, AI text naming internal context blocks, links back to scenarios and results | Review round 1 fixed; browser checks pass; after a redeploy the live text proposal no longer named the blocks | Meta 404 from some Railway exits may come back after a redeploy; the photo proposal was not rerun (NOT RUN) |
| [H-039_UI_POLISH_VALIDATION.md](H-039_UI_POLISH_VALIDATION.md) | UI finish of the model, proposal review and results pages after an impeccable critique (H-039) | Review findings fixed except those listed; browser checks pass; CI passed all six jobs; deployed and checked live | Items deliberately left as they are, listed in §5 |
| [H-040_FRAME_VALIDATION.md](H-040_FRAME_VALIDATION.md) | Header band, side panels and instrument-key buttons, returning to the 2026-10-19 prototype frame (owner's choice, H-040) | Review findings fixed except those listed; browser checks pass | NOT RUN: backend and integration tests (no Go change); live, until the owner confirms new screenshot baselines |
| [H-041_VISUAL_VALIDATION.md](H-041_VISUAL_VALIDATION.md) | The whole visual style changed to a developer-tool look the owner chose (H-041) | Review findings fixed except those listed; unit and browser checks pass | All 32 screenshot baselines must be made again in CI and confirmed by the owner; `make backend-race` and `make integration-test` NOT RUN locally (left to CI); live NOT RUN |
| [H-045_PRODUCTION_LOGISTICS_VALIDATION.md](H-045_PRODUCTION_LOGISTICS_VALIDATION.md) | The production and logistics pack with four synthetic templates researched against public sources (a sneaker factory, a bread bakery, a corrugated box plant, a distribution center; H-045, H-046) and a new model from a template on the project page; written in English | Agent self-check, then review round 1 (no High, 1 Medium, 4 Low), all fixed; Go, web unit and browser checks pass | `make backend-race` and `make integration-test` NOT RUN locally; one model of each on the live demo organization NOT RUN |
| [H-046_RESEARCH_PROPOSALS_VALIDATION.md](H-046_RESEARCH_PROPOSALS_VALIDATION.md) | Edit and scenario proposals that may research the real process on the web first with Meta's built-in web search (H-046 (2)): the per-request opt-in, the approval record's research roles, the gates, the Meta adapter's search steps, the server's source filter, the spend reservation, the research prompt and the UI | Agent self-check, then review round 1 (no High, 3 Medium, 5 Low), all fixed; backend, frontend, contract and fake AI checks pass | NOT RUN: live research (the coordinator's, after review), `make integration-test`, `make backend-race`; the spend reservation is not a ceiling (only the attempt's timeout is); the exfiltration channel through the provider's own steps is mitigated, not closed |
| [H-048_LANDING_VALIDATION.md](H-048_LANDING_VALIDATION.md) | The start page `/` as a landing for the hackathon's judges, on real product screens made by a capture script (owner's choice, H-048) | Delivered; review round 1 (no High, 3 Medium, 10 Low) fixed except L4, L5 and L10, which the report answers; unit and browser checks pass | The `home-*` screenshot baselines must be made again; the two live-demo screens exist in light only; the web-search row and the four industries depend on H-045 and H-046 shipping, their names to be reconciled at merge |
| [H-045_H-048_LIVE_VALIDATION.md](H-045_H-048_LIVE_VALIDATION.md) | The merge of H-045, H-046 (1), H-046 (2) and H-048, CI, the deploy, and the live checks: five template models, a week of each new industry, the five what-ifs, one research proposal | CI green on all six jobs; deployed; checked live as the judge | A flaky architecture probe (§2); a Muse Wonder research proposal NOT RUN (H-049) |
| [H-049_MODEL_CHOICE_VALIDATION.md](H-049_MODEL_CHOICE_VALIDATION.md) | A person chooses Muse Spark 1.3 (the default) or Muse Wonder 1.0 for each proposal, in words, from a photo or as a scenario, with or without research (H-049): the contract's closed model list, the approval's model choice, Muse Wonder's prices and timeouts, the refusals before any I/O, the UI choice and the landing line; written in English | Agent self-check, then review round 1 (no High, 2 Medium, 4 Low): M1 (a single job attempt for Muse Wonder), L2, L3 fixed; M2 resolved by H-050 (USD 20 daily caps), L1 by the probe; backend, frontend, browser and contract checks pass | NOT RUN: the DB tests, screenshot comparisons (the `canvas-*` and `home-*` baselines will change), any live Muse Wonder call; L4 (the Claude fallback's default label) left for the owner |
| [H-062_MODEL_FROM_PHOTO_VALIDATION.md](H-062_MODEL_FROM_PHOTO_VALIDATION.md) | A new model starts from a photo or floor plan (H-062): the electronics pack's `photo_start` template (the live-tested bare line), the new-model page's "Start from a photo or floor plan" (create the model, then send the photo as its first photo proposal and open it), the model page's notice when the photo was not sent, and the e2e fake's photo route | Agent self-check, no review yet; Go, web unit, contract and browser checks pass | NOT RUN: live AI, the screenshot spec, `make backend-race`, `make ai-eval`; PROPOSED: one live photo run on a model made this way |
| [DEMO_WHATIFS_VALIDATION.md](DEMO_WHATIFS_VALIDATION.md) | Five ready what-if scenarios on the demo line (`deploy/demo/whatifs.json`, `deploy/demo/seed-whatifs.cjs`), made through the product API without AI | All five ran on the live site with a baseline and a scenario run | A "0% shorter" wording issue; two older AI scenarios left on the live model; results are estimates on synthetic data with 5 replications, not predictions for a real line |

## Machine-written files

`make spec-check` runs all the scripts below that write a file and rewrites those files. Do not edit them by hand.

| File | Written by | Notes |
|---|---|---|
| `contracts-check.json` | `scripts/check_contracts.py` (`make spec-contracts`) | Schemas, fixtures and OpenAPI structure and references |
| `decisions-check.json` | `scripts/check_decisions.py` (`make spec-decisions`) | Shape of the decision register and the gates of each task; shape only, never approval |
| `deploy-check.json` | `scripts/check_deploy.py` (`make spec-deploy`) | Static checks of the Dockerfiles, compose file and Railway files; not a container build or scan |
| `docs-check.json` | `scripts/check_docs.py` (`make spec-docs`) | Fence pairing and relative links in every Markdown file; its file count changes when a Markdown file is added or removed |
| `planning-check.json` | `scripts/check_planning.py` (`make spec-planning`) | Task graph, references and requirement traceability |
| `reference-arithmetic.json` | `scripts/check_reference_math.py` (`make spec-reference-math`) | Reference arithmetic fixtures |
| `sql-inventory.json` | `scripts/check_sql_inventory.py` (`make spec-sql-inventory`) | Inventory of the SQL files; not a SQL parser |
| `environment-and-hygiene.json` | No script in this repository | Came with the received package (2026-10-15) and has not changed since; listed in `MANIFEST.sha256` and [VALIDATION_REPORT.md](VALIDATION_REPORT.md) |
| `spec-check.log` | No make target | A saved `make spec-check` output from the received package (2026-10-15), unchanged since; its counts are from that date |

## evidence/

[evidence/](evidence/) has one directory per slice, review round or live check, named `<date>-<task or decision>` with a suffix such as `-r1` (review round 1), `-coordinator` or `-live`. `evidence/2026-10-15/` holds the bootstrap run. The directories hold the raw logs, exit-code lists (`exit-codes.txt` or `summary.tsv`), environment notes, mutation scripts and screenshots that the reports cite. The reports state that the sign-in scripts there take the password from an environment variable.

## ai-evals/

[ai-evals/](ai-evals/) holds:

- `fake-provider.json`, `fake-review.json`, `fake-analyst.json`: written by `make ai-eval`, the fixture suite with the deterministic fake provider. Fake results only; they are not a measure of a real model.
- `live-<UTC start>-<run ID>-<model>.json`: reports of `make ai-eval-live`.
- `live-smoke-<UTC start>-<run ID>-<model>.json`: reports of `make ai-smoke-live`.
- `live-smoke-photo-<UTC start>-<run ID>-<model>.json`: reports of `make ai-smoke-photo-live`.
- `live-spend.jsonl`: the append-only spend record. Each live run opens a line before any call and closes it with the cost; a run is refused when its cap does not fit the daily and monthly limits less what the record holds.

The live reports keep IDs, statuses, token counts and cost, and no prompt, answer text or key.
