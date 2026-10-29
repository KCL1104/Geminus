# AGENTS.md — Coding Agent Rules

This file applies to the whole repository. Source: the original report pp. 10–11, 38–39, 48–51. New execution details are governed by `docs/DECISIONS_AND_TBD.md`.

## 1. Read first, confirm first, then change

Read in this order: README → DECISIONS_AND_TBD → PRD → ARCHITECTURE → the Specs this task touches → planning/tasks.json. When a source and a derived document contradict each other, stop the affected part, record the difference, and keep the other work that can move forward independently. Do not turn proposed / TBD into approved on your own. Do not claim that this spec package already has an application.

`schemas/` is the source of truth for JSON payloads; `openapi/openapi.yaml` is the source of truth for the HTTP binding; `migrations/` is the draft of the DB shape. Go/TypeScript types are generated from the schemas or constrained by contract tests. Do not maintain a second React Flow / AI / simulation domain model of your own.

## 2. P0 invariants that must not be violated

- The four boundaries Generic Twin IR + DomainPack + SimulationEngine + ModelChangeProposal stay separate. The generic engine does not import any domain package (electronics-line, software-delivery, etc.), HTTP, pgx, an LLM SDK or frontend types.
- AI can only suggest. It cannot approve, publish, write to the DB, run arbitrary code/SQL, call production tools or start a simulation on its own. Schema-valid is not authorized; risk/validation must be computed by the server.
- ModelVersion, Scenario, dataset revision and completed result are never modified in place. A simulation is always bound to an immutable version, patch, dataset manifest and engine/config/seed.
- A stale revision/base returns 409; no silent overwrite / auto-rebase. Approval completes revalidation, a one-time canonical effect, audit and the idempotency response in the same transaction.
- Every tenant-owned row carries organization_id; application filter + RLS + composite tenant FKs. The API / worker roles must not be superuser, BYPASSRLS or table owner. Tenant context must be transaction-local and must not remain on a pool connection.
- 2D is authoring; 3D is a read-only projection/playback. They share entity IDs and the IR topology. Do not build a separate 3D topology.
- Whenever data is insufficient, return needs_input / unavailable. Do not fabricate a metric, a dataset, performance test numbers, passing tests or a user's approval.

## 3. Repository boundaries

```text
apps/web/               Next.js UI; created by the bootstrap task
cmd/api/ cmd/worker/     assembly, configuration and lifecycle; no business rules
internal/model/         draft/version, IR validation, commit service
internal/domainpack/    electronics line pack (P0, H-016) and SoftwareDeliveryDomainPack (example); connected to the generic kernel only through interfaces
internal/simulation/    pure Go DES; CPU/memory/time bounded
internal/ai/            context, provider adapter, proposal, analyst; no mutation tool
internal/import/        upload/profile/map/validate/freeze/inference
internal/auth/ tenant/  JWT + membership + policy
internal/results/       aggregates, comparison, trace artifact
internal/audit/ storage/ controlled persistence and object storage adapters
schemas/ openapi/       change the contract first, then handlers/clients
migrations/             never rewrite a deployed migration; add a new migration
prompts/ testdata/       prompt version, golden, negative, eval fixtures
```

The original report's choices of Go/Next.js/React/TypeScript/React Flow/PlayCanvas/PostgreSQL are kept. Versions, provider, cloud, sqlc/explicit SQL and frontend state libraries are not all decided yet. Do not use a floating `latest`; at bootstrap, record the chosen versions, licenses, lockfile and ADR.

## 4. Commands and honest status

This package can run right away:

```sh
python -m pip install -r requirements-spec.txt
make spec-check           # Schema / fixture / OpenAPI structure+refs / task DAG / docs links
make spec-contracts
make spec-planning
```

After bootstrap, these must be provided and must actually pass:

```sh
make backend-test         # go vet + go test; must fail when there is no go.mod
make backend-race         # go test -race; run only on a supported platform, never pretend it ran
make frontend-test        # lint + typecheck + unit; must fail when there is no package.json
make integration-test    # disposable PostgreSQL + API + worker + RLS, never pointed at production
make e2e-test             # browser closed-loop with deterministic fake provider
make ai-eval              # app-owned fixture suite; fake and live provider results kept separate
```

Targets whose app is not yet included return a non-zero status until they are replaced by real commands. SQL migrations still have to be run on a disposable DB. Do not automatically run a destructive down / reset against a user's environment. `make spec-check` does not run SQL, and it does not mean that the full official OpenAPI validator has passed.

## 5. Development flow for each task

Confirm dependencies / scope / decision gate → write negative and happy-path tests first → contract → pure domain logic → persistence/HTTP adapter → UI adapter → security/concurrency regression → documentation and traceability. Deliver one bounded slice that can be accepted at a time; avoid large changes that span several unfinished dependencies.

Core errors have a typed error code, a JSON Pointer path and a request_id. Context cancellation is passed down through every layer. Do not swallow errors and do not return a fake success. Go maps must not decide the simulation iteration order. RNG, time quantization, hash and event tie-break have fixed golden vectors.

Every new DB query is checked for tenant scope. Every new write is checked for role + audit + concurrency + idempotency (where applicable). Every new AI context gets redaction/injection tests. Every new API updates OpenAPI + examples + the generated client contract test.

## 6. PR / task Definition of Done

It has requirement IDs, positive/negative/concurrency tests, the migration/RLS impact, failure handling, observability and updated documentation. Paste the actual commands and results. Items not run are listed as NOT RUN, never as PASS. Do not present a skip/placeholder/fake provider as a production capability.

When security tests, immutable semantics or a deterministic golden fail, do not weaken an assertion / add a skip to get green. Do not commit secrets, raw customer CSV, raw prompts or private chain-of-thought. Do not perform a production approval on behalf of a human. Deploying to production needs explicit authorization.

## 7. Scope stop line

P0 does not add native MES/CFX/GitHub/Jira connectors, Kafka/Redis/RabbitMQ, GraphQL, CRDT, BIM/CAD, physics simulation, 3D authoring, an arbitrary Python runtime, an autonomous multi-agent framework or AI production actions. SimPy is only an independent reference test. Rust / iTwin only keep an interface; do not rewrite ahead of time.
