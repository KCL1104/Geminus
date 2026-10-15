PYTHON ?= $(if $(wildcard .venv/bin/python),.venv/bin/python,python3)
# Explicit package roots: `./...` would also match Go files that npm packages
# ship under apps/web/node_modules (e.g. flatted/golang).
GO_PKGS := ./cmd/... ./internal/...
WEB := apps/web
export NEXT_TELEMETRY_DISABLED := 1
.PHONY: spec-check spec-contracts spec-planning spec-docs spec-sql-inventory spec-reference-math spec-decisions backend-test backend-race backend-build frontend-test frontend-build db-test integration-test e2e-test ai-eval ai-eval-live security-scan simulation-reference-test benchmark-test build-containers staging-smoke db-restore-test architecture-check
spec-check: spec-contracts spec-planning spec-docs spec-sql-inventory spec-reference-math spec-decisions
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
spec-decisions:
	$(PYTHON) scripts/check_decisions.py
backend-test:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included in this spec pack.' >&2; exit 2; }
	@unformatted=$$(gofmt -l cmd internal); [ -z "$$unformatted" ] || { echo "gofmt required:" >&2; echo "$$unformatted" >&2; exit 1; }
	go vet $(GO_PKGS)
	go test $(GO_PKGS)
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
db-test:
	bash scripts/run_db_tests.sh
integration-test e2e-test ai-eval ai-eval-live security-scan simulation-reference-test benchmark-test build-containers staging-smoke db-restore-test architecture-check:
	@echo 'NOT IMPLEMENTED: $@ must be replaced by real product checks in the assigned task; no fake PASS.' >&2
	@exit 2
