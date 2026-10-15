# Database drafts / execution boundary

The SQL files are candidate migrations, not a deployed database. They have not been parsed or executed against PostgreSQL in the generation environment. `make spec-sql-inventory` checks only names and safety markers; it cannot establish SQL correctness or RLS isolation.

After human review of D-001/D-008/D-013, use a fresh disposable PostgreSQL cluster/database named `dt_test_*`, with a provisioning administrator and no customer data:

```sh
export DATABASE_URL='postgresql://<test-admin>@<disposable-host>/dt_test_handoff'
export DT_ALLOW_DB_TESTS=yes
make db-test
```

The runner installs role groups and schema, runs smoke tests, performs destructive down/up, then runs tests again. It refuses an existing `twin` schema and does not create login passwords. Do not point it at shared production infrastructure. A successful run is still only migration/policy smoke evidence: separate real runtime-login connections, connection-pool cleanup, concurrent approval/leases, API RBAC, artifact access and application schema validation remain mandatory.

Tenant-bound deferred manifest checks must run before transaction-local context is cleared. One application transaction may operate on only one organization. Do not change organization context mid-transaction; the multi-tenant setup in invariants.sql is test-only and only creates deferred manifest records after selecting the final test tenant.
