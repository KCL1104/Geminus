PYTHON ?= python3
.PHONY: spec-check spec-contracts spec-planning spec-docs spec-sql-inventory spec-reference-math backend-test backend-race frontend-test db-test integration-test e2e-test ai-eval ai-eval-live security-scan simulation-reference-test benchmark-test build-containers staging-smoke db-restore-test architecture-check
spec-check: spec-contracts spec-planning spec-docs spec-sql-inventory spec-reference-math
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
backend-test:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included in this spec pack.' >&2; exit 2; }
	go vet ./...
	go test ./...
backend-race:
	@test -f go.mod || { echo 'NOT IMPLEMENTED: Go application is not included.' >&2; exit 2; }
	go test -race ./...
frontend-test:
	@test -f apps/web/package.json || { echo 'NOT IMPLEMENTED: Next.js application is not included.' >&2; exit 2; }
	npm --prefix apps/web run lint
	npm --prefix apps/web run typecheck
	npm --prefix apps/web run test:unit
db-test:
	bash scripts/run_db_tests.sh
integration-test e2e-test ai-eval ai-eval-live security-scan simulation-reference-test benchmark-test build-containers staging-smoke db-restore-test architecture-check:
	@echo 'NOT IMPLEMENTED: $@ must be replaced by real product checks in the assigned task; no fake PASS.' >&2
	@exit 2
