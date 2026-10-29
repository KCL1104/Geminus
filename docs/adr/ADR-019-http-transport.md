# ADR-019 — HTTP transport, error envelope and request scope (T-009)

Status: ACCEPTED (chosen and implemented first by the T-009 agent; approved by the owner on 2026-10-18, H-006). Date: 2026-10-18. ADR-001 already locked `net/http` with no heavy framework; this ADR fixes the HTTP layer that the first routes (projects) built and that every later route shares.

## Decision

### Package roles

| Package | Content |
|---|---|
| `internal/httpapi` | error envelope, request ID, JSON body decoding and schema validation, the middleware that verifies the token and builds `tenant.Scope` |
| `internal/api` | assembles all routes into one `http.Handler`. `cmd/api` serves the same handler the integration tests use |
| `internal/<feature>` (e.g. `internal/project`) | service and handler |
| `internal/storage/<table>` | SQL |
| `internal/ids` | D-001 IDs (`<prefix>_<ULID>`) |
| `internal/storage/dbtest` | test-only DB fixture; the architecture check forbids production code from linking it |

The router uses the standard library's `http.ServeMux` method patterns (`POST /v1/projects`). Any other path or method always returns a 404 `NOT_FOUND` envelope.

### Errors

- A handler returns an error; `httpapi.WriteError` decides the status and the body. Successful responses are sent through `httpapi.WriteJSON`/`WriteBody`. Every response (errors included) carries `Content-Type: application/json`, `X-Content-Type-Options: nosniff` and `Cache-Control: no-store`. The format is `common.schema.json#/$defs/Error`: `code`, `message`, `details[{path, reason, code?, severity?}]`, `request_id`.
- `httpapi.Status` and the schema's `x-http-status` are locked to the same values by a contract test (D-025).
- How each package's sentinel errors map:

  | Error | Response |
  |---|---|
  | `auth.ErrInvalidToken` | 401, with `WWW-Authenticate` |
  | `auth.ErrKeysUnavailable` | 503 |
  | `tenant.ErrUnknownUser` | 403 |
  | `tenant.ErrNoMembership` | 404 (does not reveal whether the organization exists) |
  | `tenant.ErrForbidden` | 403 |
  | `tenant.ErrRetryable`, `ErrOutcomeUnknown` (the errors of `postgres.Classify`; httpapi does not import the storage base layer) | 503, with a hint to retry (a write with the same Idempotency-Key) |

- Every other error is a 500 `INTERNAL_ERROR` with the fixed message `internal error`; the cause goes only to the log (with the request_id).
- A panic becomes a 500; `http.ErrAbortHandler` aborts the response as intended.
- Requests outside the contract also get an envelope (T-009 review L2): a path not in clean form (which ServeMux would 307-redirect) and HEAD (which ServeMux would hand to the GET handler) are both 404 `NOT_FOUND`; so is any other undeclared method. `OPTIONS *` gets an empty 200 from net/http itself, and `*` with any other method such as `GET *` or `POST *` gets a plain-text 400 from net/http; none of these reach a handler (they are not envelopes).

### Request ID

- The server issues `req_<ULID>` for every request, puts it in `X-Request-ID`, and writes it into the error body and the audit row.
- An `X-Request-ID` sent by the client is never used, because it would go into the audit.

### Body

- The Content-Type must be `application/json`, with `charset=utf-8` as the only accepted parameter (media type, parameter names and values are case-insensitive); otherwise 415. A malformed parameter is also 415.
- The size limit is 1 MiB by default; above it, 413. Every operation with a request body declares 413 and 415 in openapi, and `scripts/check_contracts.py` checks this. A body whose read is interrupted returns 400, even when the part read so far is valid JSON.
- A parse failure returns 400 `INVALID_REQUEST`. This covers invalid JSON, duplicate member names, non-UTF-8 and trailing data; the detail carries a JSON Pointer.
- A string (a value or a member name) containing U+0000 returns 422 `REQUEST_VALIDATION_FAILED`, with the detail pointing at that value or that object: PostgreSQL text cannot store it, and it would otherwise become a 500 at the DB (T-009 review L1).
- A malformed query string (for example `;` separators, an undecodable `%`) returns 400; the server does not read only the parts it can (`httpapi.Query`, T-009 review L6).
- A request that fails validation against the request schema in `schemas/` returns 422 `REQUEST_VALIDATION_FAILED`, with one detail per failing JSON Pointer. It uses the same schema as the generic validator; no second copy is written. A schema reference that cannot be resolved, or a body that passes the schema but cannot be decoded into the Go type (schema and type disagree), is a server error: 500, never treated as a success.

### Scope

- `Scoped` works in this order:
  1. read only a single `Authorization: Bearer` (ADR-017) and verify the token first;
  2. require exactly one well-formed `X-Organization-ID`, otherwise 400;
  3. resolve the user with `tenant.ResolveUser`;
  4. hand the scope to the handler.
- The header is only a selector (D-008). Membership and role are checked by the handler in the same transaction of `tenant.DB.Run`: a non-member gets 404, an insufficient role gets 403.
- A read-write run locks the membership row it checked with `FOR SHARE` until the end of the transaction: a concurrent revocation or role change waits for this write to commit, rather than committing first and letting the write go on (T-009 review L3). A read-only run takes no lock.
  - **Known limitation (T-009 re-review R1, latent)**: D-036's member-change transaction locks all owner memberships of the organization with `FOR UPDATE` in user_id order. If it runs on top of `Run(ReadWrite)`, each owner already holds `FOR SHARE` on their own row, and two owners changing members at the same time form a lock cycle (reviewer probe: `40P01` 3/3 times, returning `ErrRetryable`/503). T-009 has no route that changes membership. T-010 must provide a run mode that does not take `FOR SHARE` first: take D-036's ordered locks first (the owner caller's own row is among them), then reread the caller's role; and add a concurrency test that runs D-036 on top of `Run`.
  - **Handled in T-010**: member changes use `tenant.DB.RunOwners` instead (ADR-023). The first version (take the ordered owner locks first, then read the caller's role) only solved the two-owner case: T-010 review round 1 still got 40P01 in a stress test with three or more owners, and a rejected request also locked owner rows first (M1, M2). Current approach: first check without locks that the caller is an owner, then take a transaction-level advisory lock per organization (member changes one at a time), and only then D-036's ordered owner locks and the role reread. `TestMembershipChangesUnderLoad`, `TestOwnerLockAcrossPlans` and `TestOwnersChangeAtOnce` get no 40P01 under the current approach; this is a test result, not a proof; see ADR-023 for the reasons.
- The route's roles and `x-required-roles` in `planning/api-operations.json` are locked by a contract test; the service requires the same set of values (`createRoles`, `readRoles`), so what is locked is what actually runs (T-009 review L5).
- **Operations that select no organization (D-045, 2026-10-19 addendum)**: `GET /v1/me` uses a second middleware, `Self`, in the same order as `Scoped`: verify the token first; any `X-Organization-ID` sent (even empty) returns 400 (never ignored, so the client never wrongly believes an organization was selected); then resolve the user with `ResolveUser` (an unknown identity gets 403 and is not created). The handler gets only the user ID and reads in `tenant.DB.RunSelf` (a read-only transaction that sets only the user). openapi marks it with `x-tenant-scope: user`, `x-required-roles` is empty, and the header is declared as not to be sent through `NoOrganizationContext`, whose schema is `not: {}`. `scripts/check_contracts.py` checks that an organization-scope operation always requires the header and lists roles, that a user-scope one lists no roles and has 400/401/403, and that the method, path, origin, roles and scope in `planning/api-operations.json` are the same as in openapi.

### Writes

A write's effect, audit event and idempotency response are completed in the same transaction:

- **Idempotency (D-011)**:
  - The key is scoped by organization, actor and operation. A later write with path parameters or concurrency headers must include the resource and those headers in the operation scope or the request hash (API_DATABASE §7); `createProject` has none.
  - The role is checked before the claim and the replay: a user who was demoted or removed and retries an old key gets 403/404, not the old success response (API_DATABASE §7).
  - The request hash is the SHA-256 of the body's JCS canonical form.
  - The same key with the same hash replays the stored status and body; a different hash returns 409 `IDEMPOTENCY_KEY_CONFLICT`.
  - When two concurrent requests race for the same key, the primary key serializes them: the later one waits for the first to commit and then replays.
  - Kept for 24 hours; after expiry the key is claimed again. An existing key is read with `FOR UPDATE`: retries racing for the same expired key read, in turn, the state the previous one left, and only the first one reclaims it (T-009 review M1).
  - The response is sent in JCS canonical form, so the replayed bytes are still the same after the jsonb round trip.
  - `Idempotency-Key` accepts only 8–128 visible ASCII characters (the pattern `^[!-~]{8,128}$` of openapi's `IdempotencyKey`).
- **Audit**: `internal/storage/auditlog` writes `twin.audit_events` in the same transaction, with `actor_id` being the scope's user; a DB trigger also enforces this. Metadata holds only IDs and counts.

### List

- Ordered by `(created_at, id)`.
- The cursor is a base64url `{v, o, l, t, i}`, bound to the organization and the list, and not signed: it can only move within the caller's own organization's data, which RLS and the organization filter limit.
- A cursor used in another organization or list, or one that was tampered with, returns 400.
- `limit` is a decimal integer from 1 to 100, default 50.

### Gateways of the DB rules

- `db-only-in-storage` no longer follows the dependencies inside `internal/storage` and `internal/tenant` (`through`): other packages reach data through them and do not import the driver themselves. `tenant.Tx`/`tenant.TxOptions` are pgx aliases; a service holds a transaction only to hand it to storage.
- These two gateways include the storage base layer `internal/storage/postgres`, whose `Open` accepts any connection string: through it, a package could open its own connection and bypass `tenant.DB.Run`, `CheckRuntimeRole` and `Guard` (T-009 review M2). New rule `db-handles-only-in-gateways`: only `internal/storage`, `internal/tenant` and `cmd` may depend on it; the errors httpapi needs are re-exported by `tenant` (`tenant.ErrRetryable`, `ErrOutcomeUnknown`).
- The import rules cannot see SQL strings: a service writing its own SQL through `tenant.Tx` (including a connection obtained through `tx.Conn()`) can only be stopped by review (`internal/project` has no SQL). That SQL runs inside the `tenant.DB.Run` transaction with the same API login, but is **not bound by the scope**: it could `set_config('app.organization_id', …)` to change the transaction's tenant context and read another organization (T-009 re-review R3a). So SQL is written only in `internal/storage`, guarded by review; T-036 evaluates shrinking `tenant.Tx` to an interface without `Conn()`.
- Worker scope (R3b): `tenant.NewWorker` is in `internal/tenant`, so API-side code could also import it and run as any organization without a membership check, and the import rules cannot tell. There is no production caller at present. T-019 must move it to a package that only the worker side can import, and add an architecture rule. **Handled (T-019a)**: moved to `internal/tenant/workerscope` (the SQL that sets the scope is in `internal/tenant/internal/scope`, which only `internal/tenant/...` can import). The architecture rule `worker-scope-only-in-the-worker` allows only `cmd/worker`, `internal/worker` and the package itself to import it; three probes (cmd/api, a service, storage) confirm that violations are caught.

### Server

Startup and shutdown of `cmd/api`:

- **Startup**:
  - Connects to PostgreSQL with the API runtime login; superuser, BYPASSRLS or table owner are refused (`tenant.CheckRuntimeRole`, logged as `refusing the database login`). When the DB cannot be reached the log is `database unavailable`, with host, user and database, without the password.
  - The pool installs `tenant.Guard` (`poolConfig`).
  - Timeouts: read header 10 s, read 30 s, write 60 s, idle 120 s; header limit 32 KiB (`newServer`). These two and the shutdown wait are locked by tests, because the absence of any of them would not show in a response (T-009 review M1, L4).
  - Listens on `API_LISTEN_ADDR` (default `:8080`).
- **Shutdown**: graceful shutdown on SIGINT/SIGTERM, waiting at most 20 s.

## Consequences

- Every later route writes only a service, a handler and storage; the error format, request ID, body rules and scope are shared.
- Each request opens two transactions: the read-only transaction that resolves the user, plus the handler's transaction. If needed later, the user resolution can be cached.
- The unsigned cursor relies on RLS and the organization filter; if a cursor is to carry a filter in future, that filter must be bound in the cursor.
- Rate limiting (429) and OTel tracing are not implemented yet; they belong to T-035/T-036 respectively.
