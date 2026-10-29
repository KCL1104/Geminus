# ADR-023 — Membership management and controlled provisioning (T-010)

Status: ACCEPTED (chosen and implemented first by the T-010 agent; approved by the owner on 2026-10-18, including the locking design after review round 1, H-009). Date: 2026-10-18. Basis: D-008, D-012, D-036 (all APPROVED), API_DATABASE §1, §2, §8, SECURITY §2, ADR-017, ADR-019 (T-009 re-review R1), T-010 review round 1 (M1–M3, L1–L6, I1–I3).

## Decision

### Provisioning (D-008)

- Users and organizations are created only by an operator with `cmd/provision-tenant`; the API has no corresponding route, and the API role has no INSERT on `twin.users` or `twin.organizations` (`TestAPICannotProvision`).
- Commands:
  - `provision-tenant user -issuer … -subject …`: creates or finds the user of that OIDC identity and prints `user_id`;
  - `provision-tenant organization -name … -owner-issuer … -owner-subject …`: in one transaction, creates the organization, the owner user (when needed) and the owner membership, and writes the audit `organization.provision` (actor_type `system`, actor_id `provision-tenant`).
- The database connection is `GEMINUS_PROVISION_DATABASE_URL`: this login must be able to `SET ROLE twin_provisioner`, must not be a member of `twin_owner`, `twin_api` or `twin_worker`, and must not have the SUPERUSER, BYPASSRLS, CREATEROLE, CREATEDB or REPLICATION attributes (`TestCheckOperator`, T-010 review L2, I3; D-042). A superuser is also a member of every role. Provisioning uses its own login (db/README.md), `NOINHERIT` recommended: the privileges exist only after `SET ROLE`. Each operation runs with `SET LOCAL ROLE twin_provisioner`, which ends with the transaction; `twin_provisioner` has only SELECT/INSERT on users and INSERT on organizations, memberships and audit_events, and RLS applies as usual (migration 0004, `db/tests/roles.sql`).
- `user` for the same identity can run concurrently: `INSERT … ON CONFLICT (oidc_issuer, oidc_subject) DO NOTHING`, then a read; it is created only once (`TestUserOnceUnderConcurrency`, review L3).
- Input limits are the same as the ADR-017 verifier's (`auth.ValidIdentity`): the issuer is an absolute https URL that the verifier's configuration would accept (no query, fragment or credentials, ≤2048 bytes) and printable; the subject is 1–255 bytes and printable (no control characters, no format characters such as U+202E/U+200B, no separator characters other than the ASCII space), so every user created can sign in. An organization name is 1–200 characters, all printable, not all whitespace, and does not start or end with whitespace (review L4).
- Location: the `scripts/provision-tenant/` planned in tasks.json becomes `cmd/provision-tenant`: the architecture check allows only cmd to open database connections; the SQL is in `internal/storage/provisioning`.

### Membership management (D-012 routes, owner only)

- `GET/POST /v1/organizations/{organization_id}/memberships`, `PATCH/DELETE …/{user_id}`. The organization in the path must equal the organization selected by `X-Organization-ID`, otherwise 404 (the same as for a non-member; whether the organization exists is not revealed).
- Add: only an existing user can be added (by ID); when that user does not exist, 422 `REQUEST_VALIDATION_FAILED` (detail path `/user_id`); when already a member, 409 `LIFECYCLE_CONFLICT`. Adding is idempotent with an Idempotency-Key (D-011). IDs are unguessable ULIDs; "does not exist" reveals only that this ID does not exist and offers no user enumeration (API_DATABASE §2).
- Changing a role: setting the same role writes nothing and records no audit; PATCH and DELETE have no Idempotency-Key, per the contract.
- The last owner (D-036): taking the owner role from the only owner, or removing them, returns 409 `LIFECYCLE_CONFLICT`. An owner may demote or remove themselves as long as there is another owner.
- The list is ordered by user_id (keyset); the cursor is bound to the organization and the list.
- Audit: `membership.create`, `membership.update`, `membership.delete`; the target is the user ID; the metadata has only the role (and the previous role).

### Lock order (ADR-019 R1; T-010 review M1–M3)

Writes run with `tenant.DB.RunOwners`, in this order:

1. **Check first, without locking**: read the caller's role (no lock); a non-member gets `ErrNoMembership` (404), a non-owner `ErrForbidden` (403), and no lock is taken. A refused request (including one from a user of another tenant who knows the organization ID) neither waits nor blocks any write (`TestRefusedRequestsTakeNoLock`; review M1).
2. **One membership change at a time per organization**: `SELECT pg_advisory_xact_lock(hashtextextended('twin.memberships:' || organization_id, 0))`. The transaction-level advisory lock is released on commit/rollback; the pool's `Guard` is not affected. Only membership changes take this lock.
3. **D-036's ordered owner lock**: `SELECT … WHERE role='owner' … FOR UPDATE` in user_id order, then check the count. This statement starts only after the previous change commits, so the set of owners it sees is current and does not change again before this transaction ends (`TestOwnerSetIsCurrent`; review M3).
4. **Read the caller's role again**: a caller demoted or removed while waiting is refused as in 1, and nothing is changed (`TestCallerChangedWhileWaiting`).
5. Then lock the target row with `FOR UPDATE`, and perform the change, the audit and the idempotency record.

Why there is no deadlock: only two kinds of callers take locks on membership rows. Membership changes are already serialized one at a time by 2; the `FOR SHARE` of `Run(ReadWrite)` is the first lock it takes in its transaction, it holds no lock while waiting, and once it has the lock it does not wait for any other membership row. So no two transactions can each hold one membership row and wait for each other. `TestMembershipChangesUnderLoad` (3 owners, 6 users, 10 workers, 5 seconds, random promotions and demotions, removals, re-adds, writes, lists; about 370–420 successful changes per round) and `TestOwnerLockAcrossPlans` (the reviewer's probe: two changes use different plans, with a heap order different from user_id order) both show no 40P01, and an organization without an owner never appeared. With the advisory lock and the ORDER BY removed, both show 40P01.

This argument depends on an invariant that later tasks must keep (review 2 I2): **membership rows are locked only by `RunOwners` and by the first `FOR SHARE` of `Run`, and no foreign key references `twin.memberships`** (an FK referencing it would make writes to other tables take KEY SHARE locks on membership rows). `db/tests/roles.sql` checks that there is no such FK; new code that locks membership rows must redo the argument and update this ADR.

With the advisory lock in place, D-036's ORDER BY is no longer needed to avoid deadlock (the mutant that removes it is killed by no test; it is listed as equivalent with the reason written down), but D-036 is approved, so it is kept; locking the owner rows themselves is still necessary: it makes a change wait for the owners' in-progress writes to finish.

Semantics: a request is judged by the role committed when it started. A promotion not yet committed does not count; the caller gets 403 and can simply retry (replacing the original "take the lock again").

## Consequences

- Revoking access takes effect immediately for writes and reads (every request looks up the membership inside its transaction). SSE streams do not exist yet: closing long-lived connections on revocation belongs to T-023/TEST-026 (written into T-023's scope) and is not covered by this ADR.
- "The last owner" is guaranteed only by the service; the API role has UPDATE/DELETE on memberships, and the DB does not enforce it (stated in API_DATABASE §8).
- The membership changes of one organization run one after another; P0 membership changes are rare, so this is acceptable.
- Waiting relations (correcting the earlier wrong statement that "writes do not wait for changes"):
  - A change waits for all owners' in-progress writes (the ordered `FOR UPDATE` conflicts with their `FOR SHARE`), and also for the target member's in-progress writes;
  - While a change is in progress (including the owner rows already locked while it queues for the lock), new writes started by those owners wait for it to finish; so do the target member's writes;
  - Other members' writes, all reads and all refused requests do not wait.
  - So one very long owner write makes later membership changes and some owners' writes wait along with it. P0 writes are all short, so this is acceptable; if long transactions appear later, a lock timeout or a design change is needed.
- Known risks (review I3):
  - An owner can add any existing user ID to their own organization without that user's consent (D-008 allows adding known users);
  - The difference between 422 on add (the user does not exist) and 409/201 (it exists) reveals whether a given global user ID exists. ULIDs are unguessable, so the risk is low and P0 accepts it; if needed later, always return the same answer or switch to an invitation flow.
  - A request's format is validated before its permission: a viewer sending a malformed request gets 400/422 first rather than 403. It reveals no data, so it is harmless.
- **The least-privilege role for provisioning (review L5; D-042 APPROVED, H-009, implemented)**: the provisioning login used to run as `twin_owner` (the schema owner), so the everyday onboarding credential also had DDL and policy privileges. It now uses the NOLOGIN `twin_provisioner` (created by `db/bootstrap_roles.sql`, granted by migration 0004): only SELECT/INSERT on users and INSERT on organizations, memberships and audit_events. RLS reads the scope the role sets itself, so with these grants alone it could still add any user as the owner of any existing organization (review 2 L1); 0004 also uses a trigger to restrict it to adding members and audit events only to the organization created in the same transaction (the organization row's xmin equals the current transaction ID; `TestProvisionerEntersOnlyTheOrganizationItCreates`). A deployment must first run the bootstrap and 0004, then change the provisioning login to `IN ROLE twin_provisioner`; a login that is still `IN ROLE twin_owner` is refused.
- **Audit can only be written under one's own identity (found while implementing D-042)**: 0002's guard restricted only `user` events, so the API role could write `worker` or `system` events. Migration 0004 changes this: the API writes only `user` events of the scoped user, the worker writes `worker` or `system` (never under a person's identity), and provisioning writes only `system`; the schema owner is not restricted.
