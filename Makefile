PYTHON ?= $(if $(wildcard .venv/bin/python),.venv/bin/python,python3)
# Explicit package roots: `./...` would also match Go files that npm packages
# ship under apps/web/node_modules (e.g. flatted/golang).
GO_PKGS := ./cmd/... ./internal/... ./schemas/... ./scripts/... ./prompts/...
WEB := apps/web
WEB_E2E := apps/web-e2e
export NEXT_TELEMETRY_DISABLED := 1
.PHONY: spec-check spec-contracts spec-planning spec-docs spec-sql-inventory spec-reference-math spec-decisions spec-ts-contracts spec-hashes spec-db-tests spec-deploy db-tests-render ts-contracts backend-test backend-race backend-build backend-numeric frontend-test frontend-build db-test db-go-test integration-test ai-eval ai-eval-live ai-smoke-live ai-smoke-photo-live security-scan simulation-reference-test benchmark-test build-containers staging-smoke db-restore-test architecture-check
spec-check: spec-contracts spec-planning spec-docs spec-sql-inventory spec-reference-math spec-decisions spec-ts-contracts spec-hashes spec-db-tests spec-deploy
spec-contracts:
	$(PYTHON) scripts/check_contracts.py
spec-planning:
	$(PYTHON) scripts/check_planning.py
spec-docs:
	$(PYTHON) scripts/check_docs.py
spec-sql-inventory:
	$(PYTHON) scripts/check_sql_inventory.py
spec-reference-math:
	$(PYTHON) scripts/check_reference_math.py
	$(PYTHON) scripts/engine_reference.py
spec-decisions:
	$(PYTHON) scripts/check_decisions.py
# TypeScript contract types are generated from schemas/; regenerate with `make ts-contracts`.
spec-ts-contracts:
	$(PYTHON) scripts/gen_ts_contracts.py --check
ts-contracts:
	$(PYTHON) scripts/gen_ts_contracts.py
# Independent (Python rfc8785) hashes that internal/model/hashing must match.
spec-hashes:
	$(PYTHON) scripts/check_hashes.py
# db/tests/*.sql are rendered from db/tests/templates; regenerate with `make db-tests-render`.
spec-db-tests:
	$(PYTHON) scripts/render_db_tests.py --check
db-tests-render:
	$(PYTHON) scripts/render_db_tests.py
# T-039: the Dockerfiles, the local composition and the Railway files, statically (no Docker needed):
# pinned bases, non-root users, no build arguments or credentials, the variables the code reads.
spec-deploy:
	$(PYTHON) scripts/check_deploy.py
backend-test:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included in this spec pack.' >&2; exit 2; }
	@unformatted=$$(gofmt -l cmd internal schemas scripts prompts); [ -z "$$unformatted" ] || { echo "gofmt required:" >&2; echo "$$unformatted" >&2; exit 1; }
	go vet $(GO_PKGS)
	GEMINUS_FULL_DIFFERENTIAL=1 go test $(GO_PKGS)
# No fused multiply-add in engine/statistics code on any GOARCH, and the numeric tests as an FMA-capable build.
backend-numeric:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	bash scripts/check_no_fma.sh
# -timeout 30m: with the race detector the engine package's D-047 differential (random models on the frozen
# and the current kernel) and the blocking checks of T-047 once took over go test's default 10m on a shared
# 4-CPU machine. Under -race the differential now runs 5,000 of its 50,000 models (raceEnabled in
# internal/simulation/engine; the kernel has no concurrency of its own); make backend-test runs them all and
# says so with GEMINUS_FULL_DIFFERENTIAL=1 (the test fails if fewer would run), which this target does not set.
backend-race:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	go test -race -timeout 30m $(GO_PKGS)
backend-build:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	go build -o bin/ ./cmd/...
frontend-test:
	@test -f $(WEB)/package.json || { echo 'NOT IMPLEMENTED: Next.js application is not included.' >&2; exit 2; }
	npm --prefix $(WEB) run lint
	npm --prefix $(WEB) run typecheck
	npm --prefix $(WEB) run test:unit
frontend-build:
	@test -f $(WEB)/package.json || { echo 'NOT IMPLEMENTED: Next.js application is not included.' >&2; exit 2; }
	npm --prefix $(WEB) run build
# Browser checks (TEST-033): axe, keyboard-only flows, library strings and,
# with E2E_SCREENSHOTS=1 in CI's pinned container, screenshots. Builds and
# starts apps/web itself; needs `npm ci` in apps/web and apps/web-e2e.
e2e-test:
	@test -f $(WEB_E2E)/package.json || { echo 'NOT IMPLEMENTED: browser checks are not included.' >&2; exit 2; }
	npm --prefix $(WEB_E2E) test
	@test "$$E2E_SCREENSHOTS" = 1 || echo 'e2e-test: screenshots NOT RUN (only with E2E_SCREENSHOTS=1, in CI'"'"'s pinned Playwright container).' >&2
	@echo 'e2e-test: UI checks covered; the product closed loop with the fake provider is NOT RUN until T-040.' >&2
db-test:
	bash scripts/run_db_tests.sh
# Go integration tests against the database `make db-test` left migrated; a skip is a failure here.
# The packages share that database and some tests change database-wide state (e.g. PUBLIC CREATE on
# schema public in tenant.TestRuntimeLoginCannotCreate), so they run one package at a time (-p 1).
db-go-test:
	@test -n "$$GEMINUS_TEST_DATABASE_URL" || { echo 'Set GEMINUS_TEST_DATABASE_URL to the disposable dt_test_* database used by make db-test.' >&2; exit 2; }
	GEMINUS_REQUIRE_DB_TESTS=1 go test -p 1 -count=1 -race ./internal/storage/... ./internal/tenant/... ./internal/models/... ./internal/runs/... ./internal/import/... ./internal/proposals/... ./internal/scenarios/... ./internal/comparisons/... ./internal/analyses/... ./internal/api/... ./internal/worker/... ./cmd/api/... ./cmd/worker/... ./cmd/provision-tenant/... ./cmd/migrate/...
# Package dependency rules of docs/ARCHITECTURE.md §2 (T-042), read from the build graph.
architecture-check:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	go build -o bin/check-architecture ./scripts/check-architecture
	bin/check-architecture
# API + worker + PostgreSQL/RLS integration on a fresh disposable database (T-009, T-019, T-023, T-016, T-017): the SQL tests (which
# migrate it), then the Go DB-backed tests through the real HTTP handler as the plain API login and through the
# job loop as the plain worker login.
integration-test:
	@test -n "$$GEMINUS_TEST_DATABASE_URL" || { echo 'Set GEMINUS_TEST_DATABASE_URL (and DATABASE_URL, DT_ALLOW_DB_TESTS=yes for make db-test) to a fresh dt_test_* database.' >&2; exit 2; }
	$(MAKE) db-test
	$(MAKE) db-go-test
	@echo 'integration-test: API, job loop, simulation (T-023), import profile (T-016), import validation (T-017) and AI proposal (T-025, fake provider) handlers, the proposal decisions (T-026), the scenarios (T-028), the run comparison (T-029), the AI analysis (T-030, fake provider) and database covered.'
# The AI eval corpus through the context builder and the deterministic fake provider: the context-side
# zero-tolerance gates for every case (T-024, checks/ai-evals/fake-provider.json) and the server's review of
# hand-written reference and unsafe answers (T-025, checks/ai-evals/fake-review.json); the analyst checks on hand-written
# grounded and unsafe answers (T-030, checks/ai-evals/fake-analyst.json). Fake results only.
ai-eval:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	@mkdir -p checks/ai-evals
	GEMINUS_AI_EVAL_REPORT=$(CURDIR)/checks/ai-evals/fake-provider.json GEMINUS_AI_EVAL_REVIEW_REPORT=$(CURDIR)/checks/ai-evals/fake-review.json \
		GEMINUS_AI_EVAL_ANALYST_REPORT=$(CURDIR)/checks/ai-evals/fake-analyst.json \
		go test -count=1 -run '^(TestFakeProviderEval|TestReviewEval|TestAnalystEval)$$' -v ./internal/ai/corpus/
	@echo 'ai-eval: fake-provider context, review and analyst-check suites only (the analyst checks on hand-written answers, T-030). Scoring the answers of a real model (golden >=90%, T-038), database effects (T-025 make integration-test, T-026) and the live provider eval (make ai-eval-live) are NOT RUN.' >&2
# The independent SimPy reference of finite buffers and blocking (T-047), of resource breakdowns (T-048) and
# of resource calendars and release schedules (T-049), TEST-034 (SimPy pinned in requirements-spec.txt): the Go
# engine's per-replication results of the reference cases, then scripts/simpy_reference.py, which checks the
# hand-derived goldens with SimPy and compares the stochastic cases within the tolerance fixed in
# testdata/simulation/blocking-goldens.json, breakdown-goldens.json and calendar-goldens.json. Then ADR-026
# D-050's planned cross-check (T-049 review round 1 L2): scripts/calendar_reference.py, an independent Python
# implementation of R2, R3, R4, next_fit, X_end and TTRmax, against the Go validator and compiler on random
# models. The engine never calls either script.
simulation-reference-test:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	@mkdir -p bin
	GEMINUS_SIMPY_REFERENCE_OUT=$(CURDIR)/bin/simpy-reference-go.json go test -count=1 -run '^TestSimPyReferenceExport$$' ./internal/simulation/des/
	$(PYTHON) scripts/simpy_reference.py --go-results bin/simpy-reference-go.json --report bin/simpy-reference-report.json
	$(PYTHON) scripts/simpy_batch_reference.py --go-results bin/simpy-reference-go.json --report bin/simpy-batch-reference-report.json
	GEMINUS_CALENDAR_REFERENCE_OUT=$(CURDIR)/bin/calendar-reference-go.json go test -count=1 -run '^TestCalendarRulesExport$$' ./internal/simulation/engine/
	$(PYTHON) scripts/calendar_reference.py --go-results bin/calendar-reference-go.json
	@echo 'simulation-reference-test: finite buffers and blocking (T-047), breakdowns (T-048), calendars and release schedules (T-049), batch processes and changeovers (T-050, without finite buffers or calendars), on the models each script accepts (see their docstrings); D-050 R2, R3, R4, next_fit, X_end and TTRmax against an independent Python implementation (R1 is not, see checks/T-049_VALIDATION.md).' >&2
# The live provider eval (T-053; T-038 owns the baseline): the modeler corpus through the live adapter, a client
# metered by the run's cap (AI_EVAL_LIVE_MAX_USD, default USD 10) and the server's review. Refused with exit 2 and no
# provider call unless the configuration turns live AI on exactly as the worker's does: the owner's complete recorded
# approval (internal/config/aiapproval; H-018, complete since the retention confirmation H-026), AI_PROVIDER=anthropic,
# AI_POLICY_APPROVAL, AI_MODEL equal to its model, GEMINUS_ANTHROPIC_API_KEY_A and _B, and GEMINUS_AI_LIVE=on; and
# unless the cap fits the deployment's daily and monthly limits less what the spend record checks/ai-evals/live-spend.jsonl
# holds for the UTC day and month (review round 1, H1: the record is append-only and committed with the reports; a run
# opens its line before any call and closes it with what it cost). The report goes to
# checks/ai-evals/live-<UTC start>-<run ID>-<model>.json, never a fake result's file nor an existing one, and keeps no
# prompt, answer text or key. The command's own exit code (printed; make itself exits 2 on any failure): 1 a gate
# failed or the run stopped at its cap, 2 refused before any call, 3 calls were made but the run stopped on an error or
# the report or the record's close line could not be written (the summary and cost are printed). Commit the record
# and the report after every run.
ai-eval-live:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	go build -o bin/ai-eval-live ./cmd/ai-eval-live
	@bin/ai-eval-live -root $(CURDIR); code=$$?; [ $$code = 0 ] || { echo "ai-eval-live: exit $$code" >&2; exit $$code; }
# The live smoke (T-053): one synthetic case (MOD-002: the default pipeline, a capacity change the user typed), the same
# gate and spend record, a USD 2 cap by default; report checks/ai-evals/live-smoke-<UTC start>-<run ID>-<model>.json.
ai-smoke-live:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	go build -o bin/ai-eval-live ./cmd/ai-eval-live
	@bin/ai-eval-live -smoke -root $(CURDIR); code=$$?; [ $$code = 0 ] || { echo "ai-eval-live: exit $$code" >&2; exit $$code; }
# The live photo smoke (T-056 review round 1, I1: the first live SAM 3.1 call): the worker's photo pipeline once
# (internal/ai/photopipeline: SAM 3.1 per concept, count_tokens, Muse Spark, the server's review) on
# testdata/photos/electronics-line-demo.png with testdata/photos/bare-line.json, behind the worker's photo-path gate, the
# same spend record, a USD 1 cap by default; report checks/ai-evals/live-smoke-photo-<UTC start>-<run ID>-<model>.json.
# AI_SMOKE_PHOTO_CONCEPTS="rectangle,arrow" (or bin/ai-eval-live -concepts) asks SAM for those concepts instead of the
# pack's, to explore their wording (at most 8; one SAM image each); the worker never reads it.
ai-smoke-photo-live:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	go build -o bin/ai-eval-live ./cmd/ai-eval-live
	@bin/ai-eval-live -smoke-photo -root $(CURDIR); code=$$?; [ $$code = 0 ] || { echo "ai-eval-live: exit $$code" >&2; exit $$code; }
# T-039: the three images, from the repository root (Dockerfile.api, Dockerfile.worker, apps/web/Dockerfile), tagged
# geminus-{api,worker,web}:$(CONTAINER_TAG); not pushed, not scanned (no container scanner is chosen yet). Without a
# Docker daemon it fails with NOT RUN (exit 2), never a fake PASS.
CONTAINER_TAG ?= local
build-containers:
	@command -v docker >/dev/null 2>&1 || { echo 'build-containers: NOT RUN: docker is not installed; no image was built.' >&2; exit 2; }
	@docker info >/dev/null 2>&1 || { echo 'build-containers: NOT RUN: no Docker daemon is reachable (docker info failed); no image was built.' >&2; exit 2; }
	docker build -f Dockerfile.api -t geminus-api:$(CONTAINER_TAG) .
	docker build -f Dockerfile.worker -t geminus-worker:$(CONTAINER_TAG) .
	docker build -f apps/web/Dockerfile -t geminus-web:$(CONTAINER_TAG) .
	@echo 'build-containers: geminus-api, geminus-worker, geminus-web:$(CONTAINER_TAG) built (not pushed; the container scan is NOT RUN).' >&2
# T-039 (TEST-030): the local staging composition on those images (deploy/local/compose.yaml, LOCAL ONLY, with the
# repository's test OIDC provider): migrations as the migration login, seed, API, worker and web, refusals, stop.
# NOT RUN (exit 2) without Docker. The public deployment's checks are a person's (docs/runbooks/deploy.md).
staging-smoke:
	bash deploy/local/staging-smoke.sh
security-scan benchmark-test db-restore-test:
	@echo 'NOT IMPLEMENTED: $@ must be replaced by real product checks in the assigned task; no fake PASS.' >&2
	@exit 2
