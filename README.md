# Geminus

Geminus is an AI-native digital-twin tool for modelling a process, running discrete-event simulations on the model, and comparing what-if scenarios before a decision. The first domain is an electronics assembly and test line.

This repository holds the Go API and worker, the Next.js web app, the contracts (JSON Schema, OpenAPI, SQL migrations), the specs and decision log, and the validation evidence. It is a hackathon build with a public demo on synthetic data. It is not a production system, and no part of it has been used with real customer data.

Coding agents: read [AGENTS.md](AGENTS.md) first. It sets the rules for every change in this repository.

## What Geminus does

The product is built around one closed loop:

Describe / Import → Model → AI Proposal → Human Approval → Simulate → Compare → AI Explain → Iterate

- **AI only proposes; people approve.** The AI writes a proposal (a JSON Patch with its assumptions). The server validates it and computes the risk. Only a person with the Owner or Editor role can approve it. The AI cannot approve, publish, write to the database or start a simulation ([ADR-005](docs/adr/ADR-005-ai-cannot-mutate.md), [AGENTS.md §2](AGENTS.md)).
- **Two kinds of change are kept apart.** Approving an edit creates a new immutable model version. Approving a scenario saves a what-if on an existing version; the model stays as it is. Neither one starts a run; a person starts runs.
- **Results are reproducible.** Every run is bound to an immutable model version, a scenario patch, a dataset manifest, the engine version, the run config and a seed. The same inputs give the same result (hashes per RFC 8785, [ADR-014](docs/adr/ADR-014-jcs-canonicalization.md)).
- **Results are conditional estimates.** A result is an estimate under those inputs, not a forecast. When data is missing, the product returns `needs_input` or `unavailable` instead of a number.

Product facts and the intended users are in [PRODUCT.md](PRODUCT.md); the full requirements are in the [PRD](docs/PRD.md).

## The P0 domain: an electronics assembly and test line

The owner chose the electronics assembly and test line as the P0 domain (H-016, [ADR-025](docs/adr/ADR-025-p0-domain-electronics-line.md)). The domain spec is [docs/domains/ELECTRONICS_LINE.md](docs/domains/ELECTRONICS_LINE.md).

- **Template.** `electronics_assembly_test_line` (synthetic): two board releases, SMT → AOI → ICT → FCT → burn-in (batched) → pack and ship. Failed boards go through debug, rework and retest, or to scrap. Resources: `smt_line`, `aoi_machine`, `ict_tester`, `fct_tester`, `burn_in_chamber`, `debug_operator`, `pack_station`. It holds no real company's data, but its magnitudes (SMT pace and changeovers, AOI, ICT and FCT times, yields, an 8-hour burn-in) follow cited industry sources, with estimates marked as such ([ELECTRONICS_LINE §4.5](docs/domains/ELECTRONICS_LINE.md#45-sources-and-realism-of-the-template-h-046), H-046).
- **Start from a photo or floor plan.** On the project page's New model, beside the templates (H-062): the server creates the model from `photo_start`, a bare line with one SMT step (the base the live photo runs passed on), then the page sends the photo as the model's first photo proposal and opens it; the person reviews SAM 3.1's boxes and the stations Muse Spark proposes, and approves them as usual ([ELECTRONICS_LINE §4.6](docs/domains/ELECTRONICS_LINE.md)).
- **Engine.** A native Go discrete-event engine (`des-go-0.5.0`, [ADR-006](docs/adr/ADR-006-native-go-des.md)) with finite buffers and blocking, resource breakdowns (MTBF/MTTR), shift calendars, time-varying release, batch processes, changeovers, and lognormal and Weibull distributions ([ADR-026](docs/adr/ADR-026-line-flow-primitives.md)). SimPy is used only as an independent reference test.
- **Domain metrics.** The electronics pack adds line metrics such as scheduled utilization and blocking ratio per station. The generic kernel does not import any domain package; a domain comes in only through its DomainPack ([ADR-010](docs/adr/ADR-010-domain-pack-architecture.md)).
- **Production and logistics.** A second pack, `production_logistics` (H-045, [docs/domains/PRODUCTION_LOGISTICS.md](docs/domains/PRODUCTION_LOGISTICS.md)), expresses traditional factories and a warehouse with the same generic primitives: four synthetic templates whose structure and magnitudes follow cited public sources (H-046), a vulcanized sneaker factory (`shoe_factory`), a pan bread bakery (`food_plant`), a corrugated box plant (`carton_plant`) and a distribution center (`warehouse`), each with one bottleneck to try a what-if on. An editor creates a model from any template on the project page (New model).
- **Example pack.** The original report's Software Delivery pack (Source → Review → CI → Test → Deploy → Sink) is kept as an example and conformance pack.

### Demo what-ifs

[deploy/demo/whatifs.json](deploy/demo/whatifs.json) defines five what-if scenarios on the template. [deploy/demo/seed-whatifs.cjs](deploy/demo/seed-whatifs.cjs) creates them through the product's own API as manual scenarios. Every replace in the patch is preceded by a `test` of the target's ID and old value, so a mismatch is rejected instead of changing something else. No AI is called.

| Scenario | Change |
|---|---|
| Third ICT tester | `ict_tester` capacity 2 → 3 |
| One more debug operator | `debug_operator` capacity 4 → 5 |
| Burn-in batches of 48 | `burn_in` batch min 20 → 48, max 40 → 48 |
| Bigger buffers before ICT and FCT | `q_ict` and `q_fct` capacity 10 → 30 |
| 20% more boards per release | `release_a` and `release_b` quantity per release 360 → 432 |

The recorded live results (5 replications, synthetic data) and the open copy issue are in [checks/DEMO_WHATIFS_VALIDATION.md](checks/DEMO_WHATIFS_VALIDATION.md).

## How Geminus uses Meta AI

Two Meta models do the AI steps of the loop, through the Meta Model API in Standard mode (H-029, H-033, [ADR-030](docs/adr/ADR-030-meta-model-api-adapter.md)). In every step the AI's output is only a proposal or a text. Nothing the AI says changes the model by itself.

| Step of the loop | Meta model | What the model does | What the server and the person do |
|---|---|---|---|
| Describe a change or a what-if in words | Muse Spark (`muse-spark-1.3`) | Writes an edit proposal or a scenario proposal from the person's words and the current version, with its assumptions. The person picks which kind ("A change to the model" or "A scenario to simulate"). | The server validates the patch (schema, allowlist, pack rules) and computes the risk. A person approves or rejects it. |
| Turn a photo or floor plan of a line into a model | SAM 3.1 (`sam-3.1`), then Muse Spark | SAM 3.1 finds boxes for the pack's concepts (machines, conveyors and similar). Muse Spark judges the stations and the flow between them and writes an edit proposal (T-056). | If SAM finds no objects, the server answers `needs_input` without calling Muse Spark (D-131 (d), PROPOSED). Otherwise the proposal goes through the same validation, risk and human approval. |
| Research a real process on the web first (opt-in per request, H-046) | Muse Spark with the Meta Model API's built-in `web_search` tool (Search grounding) | Before it writes an edit or a scenario proposal, searches the web for how the process works and its typical parameters, preferring English-language engineering and industry sources, and lists the pages it relied on. Meta runs the searches; Geminus fetches no page. | The person ticks the opt-in; the description they typed is used for the web search. The server keeps only sources the call actually asked the search tool to open (https only; Meta returns no tool result, so the proposal page calls them "Pages the AI asked to open" and says Geminus cannot confirm they loaded) and shows them with the number of searches; the proposal is validated, risk-scored and approved like any other. A photo is never researched. Live research is NOT RUN yet (it waits for independent review). |
| Explain a baseline vs. scenario comparison | Muse Spark (`muse-spark-1.3`) | Explains two completed runs: bottlenecks, what changed, assumptions, uncertainty, and suggested next experiments (T-030; live analyst approved in H-038). | The server checks every cited metric path and every number in the text against the computed comparison. An ungrounded answer fails with `ANALYSIS_UNGROUNDED` and is not saved. |

Provider settings:

- The provider is a setting, not code. The repository's default is `AI_PROVIDER=disabled`. On the live demo, the API and the worker run with `AI_PROVIDER=meta` and `AI_MODEL=muse-spark-1.3` (recorded in [checks/T-057_AUTH0_VALIDATION.md](checks/T-057_AUTH0_VALIDATION.md), 2026-10-27).
- The Claude adapter (Anthropic) is kept as a switchable fallback for text proposals (H-033 (3)). The photo path uses only Meta (H-035 (3)).
- Only the worker holds an AI key; the API refuses to start with one. Calls run under in-code spend caps and a spend record ([checks/ai-evals/live-spend.jsonl](checks/ai-evals/live-spend.jsonl)). Only synthetic data, demo images and the text a person types are sent.
- Web research (H-046 (2)) is only for edit and scenario proposals, only when the person asks for it, and only with Meta: Search grounding costs USD 2.50 per 1,000 searches on top of tokens, and a research call reserves USD 1.61 under the same caps (an assumption, not a ceiling: a longer search loop is recorded at its real cost, and the caps then refuse later calls; a research call is never retried) ([checks/H-046_RESEARCH_PROPOSALS_VALIDATION.md](checks/H-046_RESEARCH_PROPOSALS_VALIDATION.md)).

Recorded evidence:

- `make ai-eval-live` on `muse-spark-1.3` (2026-10-26): 48 of 50 corpus cases passed, all 28 golden edits passed, every zero-tolerance gate at 0 ([report](checks/ai-evals/live-2026-10-26T220415Z-01M5X3W8RRJ8RDPQN1J7RRW3CA-muse-spark-1.3.json)).
- Four live photo smoke runs on 2026-10-27 (SAM 3.1 + Muse Spark): one `needs_input`, two `failed`, one `ready` ([checks/ai-evals/](checks/ai-evals/)).
- Live analyst checks on the deployed demo: [checks/T-030_VALIDATION.md](checks/T-030_VALIDATION.md).

**In progress (H-043, not merged yet).** Two features are being built and are not part of this commit:

- The UI shows which Meta model produced each proposal and each explanation.
- A "propose this as the next scenario" action: Muse Spark turns the explanation's suggested next experiment into a scenario proposal, which a person then reviews and approves.

## Live demo

- Web: https://web-production-16041.up.railway.app
- Sign-in is through the owner's Auth0 tenant. Self sign-up is off. Only the owner and one judge account can sign in (H-035 (2)). The judge account has access only to the demo organization. Its credentials are given in the hackathon submission, not in this repository.
- The demo project is "Demo electronics line (synthetic)". Its "Board assembly and test line" is the researched electronics template (H-046 (1)) with the five what-ifs above; it also holds one model of each production and logistics template: "Sneaker factory", "Bread bakery", "Corrugated box plant" and "Distribution center" (H-045). The earlier electronics model is kept as it was. Live checks: [checks/H-045_H-048_LIVE_VALIDATION.md](checks/H-045_H-048_LIVE_VALIDATION.md).
- All data on the demo is synthetic. The deployment is a public demo, not production (H-032).

## Architecture at a glance

A modular monolith with a separate worker ([docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)).

```text
Browser ── Next.js web app (BFF: holds the OIDC session; the browser holds no token)
              │  same-origin /api/v1/…
              ▼
           Go REST API ──── Auth0 (OIDC/JWT verification)
              │
              ▼
           PostgreSQL 16 (RLS, job queue) ◄──── Go worker ──── Meta Model API / Anthropic (worker only)
              │                                    │
              └──── private S3-compatible object storage ────┘
```

- **Web** ([apps/web](apps/web)): Next.js 16.4.1, React 19.3.1, React Aria Components; React Flow (`@xyflow/react` 12.12.0) for the 2D editor; PlayCanvas 2.23.2 for the read-only 3D view and sampled playback; English (`en`, default) and Traditional Chinese (`zh-Hant`) message catalogs ([ADR-018](docs/adr/ADR-018-browser-session-and-oidc-provider.md), [ADR-020](docs/adr/ADR-020-ui-localization.md), [ADR-021](docs/adr/ADR-021-ui-library-and-design-system.md)).
- **API** ([cmd/api](cmd/api)): Go 1.27.3, `net/http`, no web framework ([ADR-019](docs/adr/ADR-019-http-transport.md)).
- **Worker** ([cmd/worker](cmd/worker)): a PostgreSQL pull queue with `SKIP LOCKED` and leases; runs simulations and AI jobs ([ADR-024](docs/adr/ADR-024-job-queue-and-worker.md)).
- **Database**: PostgreSQL with row-level security. Every tenant-owned row carries `organization_id`; the API and worker logins are not superuser, not BYPASSRLS and not table owner ([docs/API_DATABASE.md](docs/API_DATABASE.md), [docs/SECURITY.md](docs/SECURITY.md)).
- **Four boundaries** stay separate: the generic Twin IR, the DomainPack, the SimulationEngine and the ModelChangeProposal ([AGENTS.md §2](AGENTS.md)).
- **Contracts**: [schemas/](schemas) is the source of truth for JSON payloads, [openapi/openapi.yaml](openapi/openapi.yaml) for HTTP, [migrations/](migrations) for the database shape. TypeScript contract types in `apps/web/lib/contracts/` are generated from the schemas (`make ts-contracts`).

Specs: [Twin IR](docs/TWIN_IR_SPEC.md), [Simulation](docs/SIMULATION_SPEC.md), [AI](docs/AI_SPEC.md), [API and database](docs/API_DATABASE.md), [Security](docs/SECURITY.md), [Test plan](docs/TEST_PLAN.md), [Implementation plan](docs/IMPLEMENTATION_PLAN.md), and the ADRs in [docs/adr/](docs/adr).

## Repository map

| Path | Contents |
|---|---|
| [apps/web/](apps/web) | Next.js web app and BFF |
| [apps/web-e2e/](apps/web-e2e) | Playwright browser checks (axe, keyboard, both languages, screenshots) and the test OIDC provider for local runs |
| [cmd/](cmd) | `api`, `worker`, `migrate`, `provision-tenant`, `ai-eval-live` |
| [internal/](internal) | Go packages: model service, simulation engine, domain packs, AI providers and context, import, results and comparison, auth and tenant, storage, audit, observability |
| [schemas/](schemas), [openapi/](openapi), [migrations/](migrations), [db/](db) | Contracts, migrations, roles and SQL tests |
| [prompts/](prompts) | Versioned prompts: modeler, scenario, photo, analyst, critic, importer |
| [testdata/](testdata) | Golden models, contract fixtures, hashing vectors, simulation goldens, AI eval corpus, demo photos |
| [deploy/](deploy) | Railway config and deploy script, local staging composition, demo what-ifs |
| [scripts/](scripts) | Spec checks, independent references (SimPy, hashes), generators |
| [docs/](docs) | PRD, architecture, specs, ADRs, runbooks, decision log |
| [planning/](planning) | Task DAG (`tasks.json`), traceability, decision register, hackathon plan |
| [checks/](checks) | Validation and review reports, evidence and AI eval results ([index](checks/README.md)) |
| [sources/](sources) | The original 54-page report and its source map |
| [DESIGN.md](DESIGN.md), [PRODUCT.md](PRODUCT.md) | Design system and product facts |
| [START_HERE.md](START_HERE.md) | The first-round instruction for a coding agent, from the spec package |

## Running locally

Toolchain ([ADR-011](docs/adr/ADR-011-toolchain-bootstrap.md)): Go 1.27.3 (the `go.mod` toolchain), Node 24.21.0 (`apps/web/.nvmrc`), Python 3.13 for the spec checks, PostgreSQL 16 for the database tests, Docker for the container targets.

```sh
python -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements-spec.txt
make spec-check

npm ci --prefix apps/web
npm ci --prefix apps/web-e2e
make backend-test frontend-test
```

The database tests need a disposable database whose name starts with `dt_test_`; see [db/README.md](db/README.md). Never point them at a database you care about.

### Make targets and their recorded status

A target that cannot run here fails with a non-zero exit and says NOT RUN or NOT IMPLEMENTED; it never reports a fake PASS ([AGENTS.md §4](AGENTS.md)). The status below is what the cited reports record, not a fresh run.

| Target | What it checks | Recorded status |
|---|---|---|
| `make spec-check` | Schemas, fixtures, OpenAPI structure and refs, task DAG, decision register, doc links, SQL inventory, reference math, generated TS types, hashes, rendered DB tests, deploy files. Static only: it runs no SQL and is not the official OpenAPI validator. | exit 0 on 2026-10-29 ([H-041](checks/H-041_VISUAL_VALIDATION.md)) |
| `make backend-test` | `gofmt`, `go vet`, `go test` with the full D-047 differential | exit 0, 85 packages ok, on 2026-10-29 ([H-041](checks/H-041_VISUAL_VALIDATION.md)) |
| `make backend-race` | `go test -race` | Full run exit 0 (85 packages) on 2026-10-28 ([T-030](checks/T-030_VALIDATION.md)); later slices ran `-race` only on changed packages. CI runs it. |
| `make backend-numeric`, `make backend-build` | No fused multiply-add in engine code; build the binaries | Run in CI |
| `make architecture-check` | Package dependency rules of ARCHITECTURE §2 | exit 0, 96 packages, 26 rules ([T-056](checks/T-056_VALIDATION.md)) |
| `make frontend-test` | ESLint, `tsc`, Vitest | exit 0, 1121 unit tests, on 2026-10-29 ([H-041](checks/H-041_VISUAL_VALIDATION.md)) |
| `make frontend-build` | `next build` | Run in CI |
| `make e2e-test` | Playwright: axe, keyboard-only flows, both languages; screenshots only with `E2E_SCREENSHOTS=1` in CI's pinned container | exit 0, 518 passed, 32 skipped (screenshots), on 2026-10-29 ([H-041](checks/H-041_VISUAL_VALIDATION.md)). Per the H-041 report, the screenshot baselines are re-shot by CI and committed only after the owner confirms them. The product closed loop with the fake provider is NOT RUN until T-040. |
| `make integration-test` (`db-test` + `db-go-test`) | Migrations up/down/up, SQL tests, Go DB-backed tests through the real handler and job loop, as the runtime logins with RLS | exit 0, 38 packages, no FAIL or SKIP, on a fresh `dt_test_*` database ([T-056](checks/T-056_VALIDATION.md)); CI's db job runs it |
| `make ai-eval` | AI eval corpus through the context builder and the deterministic fake provider | exit 0 ([T-056](checks/T-056_VALIDATION.md)). Fake results only; scoring a real model's answers (T-038) is NOT RUN. |
| `make ai-eval-live`, `make ai-smoke-live`, `make ai-smoke-photo-live` | The live provider corpus and smoke runs | Refused (exit 2, no call) unless the owner's approval record, the keys and `GEMINUS_AI_LIVE=on` are all set. Reports are in [checks/ai-evals/](checks/ai-evals). |
| `make simulation-reference-test` | Independent SimPy and Python references for blocking, breakdowns, calendars, release, batches and changeovers (TEST-034) | exit 0 ([T-050](checks/T-050_VALIDATION.md), [T-052](checks/T-052_VALIDATION.md)) |
| `make build-containers`, `make staging-smoke` | Build the three images; local staging composition with a test OIDC provider | NOT RUN locally in the recorded reports (no Docker daemon, [T-039](checks/T-039_VALIDATION.md)). CI's containers job ran both; CI run 40744508253 on `f06ca92` passed all six jobs ([H-039](checks/H-039_UI_POLISH_VALIDATION.md)). |
| `make security-scan`, `make benchmark-test`, `make db-restore-test` | Not built yet | NOT IMPLEMENTED (exit 2) |

The container scan, SBOM and image signing are NOT RUN (no tool chosen yet).

## Deployment

The demo runs on Railway (H-031): `migrate`, `api`, `worker` and `web` services built from the repository's Dockerfiles, Railway PostgreSQL and a Railway Storage Bucket. [deploy/railway/deploy.sh](deploy/railway/deploy.sh) applies the configuration non-interactively with a pinned Railway CLI; `--dry-run` makes no network call. Secrets are generated or entered by the owner on Railway and sealed; none are in the repository.

- [Deploy runbook](docs/runbooks/deploy.md): services, images, migration order, every environment variable, Railway steps, pre-launch checks.
- [Auth0 runbook](docs/runbooks/auth0.md): the tenant, the API and the web application settings.
- [ADR-029](docs/adr/ADR-029-containers-and-staging-deployment.md) (PROPOSED): containers and the staging deployment.

## Design system

[DESIGN.md](DESIGN.md) is the design system. Since H-041 (2026-10-29) the app has a developer-tool look the owner chose: Geist and Geist Mono (Noto Sans TC for Chinese), cards on a light or near-black page, one violet accent for the AI, selection and focus, and pills for status. The data grammar comes from Marey's graphic timetable and did not change: baseline is dashed gray, scenario is solid green. The H-027 simplicity rule holds: one primary action per page and only the controls each view needs. The UI targets WCAG 2.2 AA, with light and dark themes that follow the system by default.

## Decisions and evidence

- [docs/DECISIONS_AND_TBD.md](docs/DECISIONS_AND_TBD.md) is the decision log. `D-` rows are engineering decisions (PROPOSED, APPROVED, or superseded); `H-` rows record the owner's decisions with the owner's own words. Agents never approve a decision. The machine-readable register is [planning/decisions.json](planning/decisions.json).
- [planning/tasks.json](planning/tasks.json) is the task DAG. At this commit it lists 57 tasks: 35 done, 5 in progress, 17 not started. Several hackathon slices are recorded in `checks/` while their full task stays open, because a task's dependencies must be done first.
- [checks/](checks) holds a validation report per slice and the independent review reports, with raw command output under `checks/evidence/`. [checks/README.md](checks/README.md) indexes them.
- [sources/ORIGINAL_REPORT.pdf](sources/ORIGINAL_REPORT.pdf) is the original 54-page report; [sources/SOURCE_MAP.md](sources/SOURCE_MAP.md) maps requirements to its pages.
- `MANIFEST.sha256` is the checksum list of the original handoff package. It has not been regenerated since, and no script checks it.

## Hackathon context

Geminus is entered in the Meta Global AI Developer Hackathon (Cerebral Valley and Meta), Indie track (H-029). The plan is [planning/HACKATHON_PLAN.md](planning/HACKATHON_PLAN.md).

- Code freeze: 2026-11-03 23:59 UTC. Product work runs until 2026-11-01; 2026-11-02 to 11-03 are for the demo only (H-038).
- Submission deadline: 2026-11-04 21:00 ET (2026-11-05 02:00 UTC).
- The spec package (2026-10-15) and much of the platform predate the hackathon. The slices of the hackathon plan were built from 2026-10-26: the Meta provider (T-055), the photo path (T-056), the electronics template and synthetic data (T-051, T-052), the 2D editor (T-015), the proposal review and results screens (T-031, T-032), the deployment (T-039, T-057), the grounded analyst and the 3D view and playback (T-030, T-033, T-034, H-038), the UI work of H-039 to H-041 and the demo what-ifs. The dated reports in [checks/](checks/README.md) and the git history show what was built when.

## License

Proprietary software, all rights reserved ([LICENSE](LICENSE), owner decision H-015). No use, copying, modification, distribution or provision as a service without the copyright holder's written consent. Third-party components are under their own licenses; see [THIRD_PARTY_NOTICES](docs/THIRD_PARTY_NOTICES.md) and the license columns of ADR-011 and ADR-021.
