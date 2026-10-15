# Database: migrations, SQL tests, execution boundary

`migrations/` holds the schema (0001 core, 0002 RLS and grants). Until T-006 is accepted they are drafts that have only run on disposable `dt_test_*` databases; from then on a migration applied to any persistent environment is never edited — changes go into a new numbered file.

## Run

Use a fresh disposable PostgreSQL 16 database named `dt_test_*`, a provisioning administrator and no customer data:

```sh
export DATABASE_URL='postgresql://<test-admin>@<disposable-host>/dt_test_handoff'
DT_ALLOW_DB_TESTS=yes make db-test          # roles, up, tests, down, up, tests
GEMINUS_TEST_DATABASE_URL="$DATABASE_URL" make db-go-test   # Go repository tests on the migrated schema
```

The runner refuses a non-`dt_test_*` database or an existing `twin` schema and creates no login passwords. Never point it at shared or production infrastructure; the down migrations are destructive.

## Tests

| File | Covers |
|---|---|
| `db/tests/invariants.sql` | RLS: no-context reads return 0 rows, cross-tenant read/write fail, cross-tenant parent FK fails, runtime roles are not superuser/BYPASSRLS/owner, immutable version trigger |
| `db/tests/constraints.sql` | D-001 per-kind ID domains, D-035 object keys (`starts_with`; `..`, `//`, `\`, control characters, length), D-034 byte-order manifests and `logical_ref` charset, D-027 risk = max(server, claim), TEST-004 immutability incl. TRUNCATE, runtime privileges |
| `db/tests/roles.sql` | D-037 worker column privileges and the import-commit guard, D-036 owner-membership lock (no `organizations` UPDATE), FORCE RLS and a policy on every table, runtime roles separate and owning nothing, no PUBLIC-executable function |
| `internal/storage/postgres` (Go) | commit/rollback/panic, transaction-local settings on a reused pooled connection, cancellation releases locks, typed errors that never quote row data |
| `internal/tenant` (Go) | real runtime logins (created by the test as `dt_test_*_login`, no password): `CheckRuntimeRole`, no `SET ROLE` to the owner, membership-checked scope, wrong-org and cross-tenant writes, pool reuse after success/error/panic/cancel, `Guard` discarding leaked scope or role, worker allowlist |

Both SQL files are rendered from `db/tests/templates/` by `scripts/render_db_tests.py` (`make db-tests-render`; `make spec-check` fails if they are stale), which fills valid ULID test IDs and real RFC 8785 model hashes. Run them on databases with different default collations: CI's PostgreSQL image uses libc `en_US.utf8`; locally an ICU database works (`CREATE DATABASE dt_test_icu TEMPLATE template0 ENCODING 'UTF8' LOCALE_PROVIDER icu ICU_LOCALE 'en-US' LOCALE 'C.utf8'`). For diagnosis, `PGOPTIONS='-c dt.collect=on'` reports every failing case instead of stopping at the first.

## What this does not prove

Concurrent approval/leases (T-013/T-019), API RBAC, artifact access and application schema validation remain separate. Tenant-bound deferred manifest checks must run before transaction-local context is cleared: one application transaction operates on one organization and never changes organization context mid-transaction (the test suites flush deferred checks before switching tenants).
