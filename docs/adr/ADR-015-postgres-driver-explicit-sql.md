# ADR-015 — PostgreSQL driver and explicit SQL

Status: ACCEPTED (chosen by the T-006 agent; owner KCL1104 agreed on 2026-10-15). Date: 2026-10-15. The original report lists "SQL generation: explicit SQL / sqlc" as TBD (DECISIONS_AND_TBD.md).

## Decision

- Driver and pool: `github.com/jackc/pgx/v5` v5.11.0 (MIT, released 2026-09-07; depends on `jackc/puddle/v2`, `pgpassfile`, `pgservicefile`, all MIT). It does not go through `database/sql`, so that pgx's transaction options, SQLSTATE and `pgconn.PgError` fields can be used.
- Queries are hand-written, parameterized explicit SQL in each repository; sqlc is not introduced for now. Reason: P0's security is concentrated at the transaction boundary (tenant context, lock order, CAS), and hand-written SQL keeps these steps visible in code review; the schema is still defined by `migrations/`, and correctness is verified on a disposable database by `make db-test` and `make db-go-test`.
- `internal/storage/postgres` provides only the pool, `Tx` (rollback on error/panic/cancel, using a context separate from the request for the rollback) and `Classify` (SQLSTATE → typed error, without DETAIL or other row values). The tenant scope helper belongs to T-007 (D-008).

## Consequences

- If the number of repositories or the rate of query changes makes type errors in hand-written SQL a problem, sqlc can be reevaluated; any change must keep `db/tests` and the Go integration tests.
- CI's db job installs Go and runs the integration tests with `GEMINUS_REQUIRE_DB_TESTS=1`, where a skip counts as a failure; under `make backend-test` without a database these tests are marked NOT RUN (skip), not PASS.
