# Database: migrations, SQL tests, execution boundary

`migrations/` holds the schema (0001 core, 0002 RLS and grants, 0003 the draft's semantic issues for T-012 / D-028). Until T-006 is accepted they are drafts that have only run on disposable `dt_test_*` databases; from then on a migration applied to any persistent environment is never edited — changes go into a new numbered file.

## Run

Use a fresh disposable PostgreSQL 16 database named `dt_test_*`, a provisioning administrator and no customer data:

```sh
export DATABASE_URL='postgresql://<test-admin>@<disposable-host>/dt_test_handoff'
DT_ALLOW_DB_TESTS=yes make db-test          # roles, up, tests, down, up, tests
GEMINUS_TEST_DATABASE_URL="$DATABASE_URL" make db-go-test   # Go repository tests on the migrated schema
# or both in one step (what CI runs):
DT_ALLOW_DB_TESTS=yes GEMINUS_TEST_DATABASE_URL="$DATABASE_URL" make integration-test
```

The runner refuses a non-`dt_test_*` database or an existing `twin` schema and creates no login passwords. Never point it at shared or production infrastructure; the down migrations are destructive.

### Provisioning a tenant (T-010, ADR-023)

Users and organizations are created by an operator, never through the API (D-008). Give provisioning a login of its own that may act as `twin_provisioner` and is nothing else (D-042). `twin_provisioner` can only read users and insert users, organizations, memberships and system audit events; RLS applies to it (`migrations/0004_provisioner.up.sql`). `cmd/provision-tenant` refuses a login that cannot `SET ROLE twin_provisioner`, one that is a member of `twin_owner`, `twin_api` or `twin_worker` (a superuser is a member of every role), and one with SUPERUSER, BYPASSRLS, CREATEROLE, CREATEDB or REPLICATION:

```sql
CREATE ROLE geminus_provisioner LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB IN ROLE twin_provisioner;  -- NOINHERIT: rights only after SET ROLE; set its password out of band
```

```sh
export GEMINUS_PROVISION_DATABASE_URL='postgresql://geminus_provisioner@<host>/<db>'
go run ./cmd/provision-tenant organization -name 'Acme' -owner-issuer 'https://<tenant>.auth0.com/' -owner-subject 'auth0|…'
go run ./cmd/provision-tenant user -issuer 'https://<tenant>.auth0.com/' -subject 'auth0|…'   # then an owner adds the user by ID
```

## Tests

| File | Covers |
|---|---|
| `db/tests/invariants.sql` | RLS: no-context reads return 0 rows, cross-tenant read/write fail, cross-tenant parent FK fails, runtime roles are not superuser/BYPASSRLS/owner, immutable version trigger |
| `db/tests/constraints.sql` | D-001 per-kind ID domains, D-035 object keys (`starts_with`; `..`, `//`, `\`, control characters, length), D-034 byte-order manifests and `logical_ref` charset, D-027 risk = max(server, claim), TEST-004 immutability incl. TRUNCATE, runtime privileges |
| `db/tests/roles.sql` | D-037 worker column privileges and the import-commit guard, D-036 owner-membership lock (no `organizations` UPDATE), FORCE RLS and a policy on every table, runtime roles separate and owning nothing, no PUBLIC-executable function, D-042 `twin_provisioner` (exactly its five table privileges, no CREATE, not a member of another twin role, RLS applies, cannot change or read what it does not need) and each role writing audit events only as itself |
| `internal/storage/postgres` (Go) | commit/rollback/panic, transaction-local settings on a reused pooled connection, cancellation releases locks, typed errors that never quote row data |
| `internal/storage/projects` (Go) | the organization filter alone (as the provisioning superuser, whom RLS does not restrict) and keyset paging across rows with the same `created_at` |
| `internal/api`, `cmd/api` (Go, T-009) | the API through its real handler as the plain API login (`dt_test_api_login`) with a TLS test OIDC provider: role × tenant matrix of the project routes, idempotency (replay, conflict, races, expiry), audit rows, cursor tampering, TEST-010 for projects; `cmd/api` refusing the superuser login and serving/stopping as the API login |
| `internal/storage/provisioning`, `cmd/provision-tenant` (Go, T-010) | the operator login (`dt_test_provisioner_login`, created by the test `IN ROLE twin_provisioner`, no password) creates users and organizations with their first owner and audit event, and as `twin_provisioner` cannot change or read anything else; the API login, the worker login, a superuser, a member of the schema owner, a provisioning login that is also the owner or a runtime login, one that cannot SET ROLE and one with extra attributes are refused; parallel calls for one identity create one user; inputs a verified token could not carry are refused; the API login cannot INSERT users or organizations even with the RLS settings |
| `internal/tenant/memberships` (Go, T-010) | owner-only list/add/update/remove as the API login: roles, idempotency (a replay still needs the role), last owner (D-036) judged on the current owner set, removal waiting for a member's write, refused requests taking no lock, a caller demoted or removed while waiting, owner locks under different plans, and a 5 s randomized load of three owners with no deadlock or owner-less organization (ADR-023) |
| `internal/tenant` (Go) | real runtime logins (created by the test as `dt_test_*_login`, no password): `CheckRuntimeRole`, no `SET ROLE` to the owner, membership-checked scope, wrong-org and cross-tenant writes, pool reuse after success/error/panic/cancel, `Guard` discarding leaked scope or role, worker allowlist |

Both SQL files are rendered from `db/tests/templates/` by `scripts/render_db_tests.py` (`make db-tests-render`; `make spec-check` fails if they are stale), which fills valid ULID test IDs and real RFC 8785 model hashes. Run them on databases with different default collations: CI's PostgreSQL image uses libc `en_US.utf8`; locally an ICU database works (`CREATE DATABASE dt_test_icu TEMPLATE template0 ENCODING 'UTF8' LOCALE_PROVIDER icu ICU_LOCALE 'en-US' LOCALE 'C.utf8'`). For diagnosis, `PGOPTIONS='-c dt.collect=on'` reports every failing case instead of stopping at the first.

## What this does not prove

Concurrent approval/leases (T-013/T-019), API RBAC, artifact access and application schema validation remain separate. Tenant-bound deferred manifest checks must run before transaction-local context is cleared: one application transaction operates on one organization and never changes organization context mid-transaction (the test suites flush deferred checks before switching tenants).
