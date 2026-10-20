PYTHON ?= $(if $(wildcard .venv/bin/python),.venv/bin/python,python3)
# Explicit package roots: `./...` would also match Go files that npm packages
# ship under apps/web/node_modules (e.g. flatted/golang).
GO_PKGS := ./cmd/... ./internal/... ./schemas/... ./scripts/... ./prompts/...
WEB := apps/web
WEB_E2E := apps/web-e2e
export NEXT_TELEMETRY_DISABLED := 1
.PHONY: spec-check spec-contracts spec-planning spec-docs spec-sql-inventory spec-reference-math spec-decisions spec-ts-contracts spec-hashes spec-db-tests db-tests-render ts-contracts backend-test backend-race backend-build backend-numeric frontend-test frontend-build db-test db-go-test integration-test ai-eval ai-eval-live security-scan simulation-reference-test benchmark-test build-containers staging-smoke db-restore-test architecture-check
spec-check: spec-contracts spec-planning spec-docs spec-sql-inventory spec-reference-math spec-decisions spec-ts-contracts spec-hashes spec-db-tests
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
backend-test:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included in this spec pack.' >&2; exit 2; }
	@unformatted=$$(gofmt -l cmd internal schemas scripts prompts); [ -z "$$unformatted" ] || { echo "gofmt required:" >&2; echo "$$unformatted" >&2; exit 1; }
	go vet $(GO_PKGS)
	go test $(GO_PKGS)
# No fused multiply-add in engine/statistics code on any GOARCH, and the numeric tests as an FMA-capable build.
backend-numeric:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	bash scripts/check_no_fma.sh
backend-race:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	go test -race $(GO_PKGS)
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
	GEMINUS_REQUIRE_DB_TESTS=1 go test -p 1 -count=1 -race ./internal/storage/... ./internal/tenant/... ./internal/models/... ./internal/runs/... ./internal/import/... ./internal/proposals/... ./internal/api/... ./internal/worker/... ./cmd/api/... ./cmd/worker/... ./cmd/provision-tenant/...
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
	@echo 'integration-test: API, job loop, simulation (T-023), import profile (T-016), import validation (T-017) and AI proposal (T-025, fake provider) handlers, the proposal decisions (T-026) and database covered; the AI analysis (T-030) job handler is NOT RUN until that task adds it.'
# The AI eval corpus through the context builder and the deterministic fake provider: the context-side
# zero-tolerance gates for every case (T-024, checks/ai-evals/fake-provider.json) and the server's review of
# hand-written reference and unsafe answers (T-025, checks/ai-evals/fake-review.json). Fake results only.
ai-eval:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	@mkdir -p checks/ai-evals
	GEMINUS_AI_EVAL_REPORT=$(CURDIR)/checks/ai-evals/fake-provider.json GEMINUS_AI_EVAL_REVIEW_REPORT=$(CURDIR)/checks/ai-evals/fake-review.json \
		go test -count=1 -run '^(TestFakeProviderEval|TestReviewEval)$$' -v ./internal/ai/corpus/
	@echo 'ai-eval: fake-provider context and review suites only. Scoring the answers of a real model (golden >=90%, T-038), database effects (T-025 make integration-test, T-026), the analyst output checks (T-030) and the live provider eval (make ai-eval-live) are NOT RUN.' >&2
ai-eval-live security-scan simulation-reference-test benchmark-test build-containers staging-smoke db-restore-test:
	@echo 'NOT IMPLEMENTED: $@ must be replaced by real product checks in the assigned task; no fake PASS.' >&2
	@exit 2
