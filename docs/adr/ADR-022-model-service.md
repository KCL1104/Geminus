# ADR-022 — Model/Draft/Version service (T-012)

Status: ACCEPTED (chosen and implemented first by the T-012 agent; approved by the owner on 2026-10-18, H-009). Date: 2026-10-18. Basis: D-003, D-028 (both APPROVED), D-011, D-020, D-029, API_DATABASE §3–§4, TWIN_IR_SPEC §5.

## Decision

### Package location

- The service is in `internal/models` (application layer, next to `internal/project`); the SQL is in `internal/storage/models`.
- The `internal/model/service/` planned in tasks.json is not feasible: `internal/model/...` is the generic kernel, and the architecture check forbids it to depend on tenant, storage or a domain pack (AGENTS.md §2).

### Draft state

- **clean**: the draft's hash equals its base version's hash (same content, however many times it was edited); otherwise it is **dirty**.
- **out_of_date**: the draft's base is not the model's latest version. Computed on read, not stored.
- **revision**: incremented by 1 on every draft write (patch, reset, sync after publish). The ETag is `"draft-revision-N"`; the service receives the expected revision, and the HTTP layer (T-013) handles the If-Match format.
- **issues** (D-028): stored in `model_drafts.issues` (migration 0003), computed on every draft write, not recomputed on read. The contents, in order:
  - the generic validator's semantic stage;
  - the domain pack's `Validate`;
  - the dataset binding stage against the base version's manifest (`datasets.Resolve`).
  - When a stage has an error, the next one does not run (D-023; T-012 review I1). The result is ordered per D-023.
  - At most 1000 (the limit of migration 0003 and of `Error.details`; T-012 review M2; the same for the patch itself and for schema 422s, review 2 N1): beyond that, the first 999 after ordering are kept, with a `LIMIT_EXCEEDED` whose path is `""` added in front, saying how many more were not listed; when the omitted ones include an error it is an error, so whether publish is blocked is the same as with the full list.
  - Paths: the IR part is relative to the IR (without `/ir`); the dataset part is `/dataset_bindings/<i>/…`, where `i` is the position in the base version's `dataset_bindings` (TWIN_IR_SPEC §5.2; T-012 review L2).
  - The result of a patch that fails the schema (including the 2 MiB limit) is not written; 422 is returned.

### Write locks and order

- Every write first locks the model row with `SELECT … FOR UPDATE`, then reads and writes the draft (the model → draft order of API_DATABASE §4; later approval adds the proposal). Writes to the same model therefore run one after another.
- Reads (model, draft, version and lists) run in one REPEATABLE READ read-only transaction (`tenant.ReadSnapshot`): publish changes model, draft and version together, and a read never sees half of it (T-012 review L1: separate READ COMMITTED reads once reported a draft with only manual edits as out_of_date).
- A revision or base mismatch is always 409 `MODEL_VERSION_CONFLICT`; no auto-rebase.

### Operations

- **Create** (idempotency key):
  - Builds the template with that domain pack's `TemplateProvider`, by locale (ADR-020).
  - The template must pass the schema, the generic semantics and the pack rules: an error is the pack's fault, so an internal error is returned and nothing is created; warnings are saved with the draft (API_DATABASE §4; T-012 review L4).
  - In the same transaction: creates the model, v1 (parent null, manifest `[]`) and draft revision 1, sets latest to v1, writes the audit `model.create` and the idempotency response.
- **Patch draft** (If-Match):
  - Applies the patch to the draft's canonical form with `patch.Apply(ManualDraft)`; an error in the patch itself or a result that fails the schema returns 422 and nothing is written.
  - A result whose string or member name contains U+0000 returns 422 (`IR_SCHEMA_INVALID`, path pointing at the value; for a member name, at its object, the same as `httpapi.DecodeJSON`): PostgreSQL's text and jsonb cannot store it (T-012 review L5, review 2 I2).
  - When it passes, stores the new IR, hash and issues, revision+1, and writes the audit `model.draft.update`.
- **Publish** (idempotency key, If-Match, `expected_base_version_id`):
  - The revision, expected base, draft base and latest must agree, otherwise 409 `MODEL_VERSION_CONFLICT`. A stale draft cannot be published (D-003).
  - A clean draft has nothing to publish: 409 `LIFECYCLE_CONFLICT`, and no new version with the same content is created.
  - Fully validates the candidate's canonical form: generic, pack, datasets resolved against the base manifest. Any error returns 422 `MODEL_VALIDATION_FAILED` (details are the issues).
  - The new version is `latest.version+1`, its parent is the base, and its manifest is the base's full manifest: unused bindings are also validated and frozen (TWIN_IR_SPEC §5.2: a run uses the version manifest). `model_version_datasets` is written at the same time, and a deferred trigger checks that it matches the manifest.
  - The request's `comment` is user text: it is stored on the new version (D-041, T-013b); the audit metadata holds only IDs and counts.
  - Then the draft is handled per the "version advance rule"; models' `latest_version_id` and `name` (following latest's IR name) are updated; the audit `model.publish` and the idempotency response are written.
- **Reset** (If-Match, `expected_latest_version_id`, `confirm_discard: true`):
  - A mismatch returns 409.
  - The draft becomes latest's IR, its base becomes latest, revision+1, issues are recomputed; the audit `model.draft.reset` is written.
  - The contract has no idempotency key: a resend returns 409 because the revision has changed.
- **Version advance rule** (shared by publish and the later approval):
  - When a new version is created, if the draft is clean (its hash equals its own base, whether or not that base is latest; T-012 review L3), it syncs to the new version: IR and base become the new version's, revision+1.
  - If the draft is dirty, it is kept entirely; because its base is no longer latest, `out_of_date` is true (D-003).
  - On publish, the draft's content is the new version, so it always syncs.
- **Reads**:
  - model (with a draft summary and a latest summary), draft, version;
  - the list of models in a project (ascending `created_at, id`);
  - the list of a model's versions (descending version, newest first);
  - the cursor is bound to the organization and to the owning project or model.

### Where the dataset manifest comes from

- Neither the publish request nor the draft has a manifest field, so a manual publish can only reuse the base version's bindings.
- A new dataset can be bound to a version only through import commit → proposal → approval (T-018, T-026).
- If a draft references an empirical `samples_ref` with no binding, the issues include `DATASET_BINDING_INVALID`, and publish returns 422 (TEST-014).
- A manual publish keeps all of the base's bindings, so data not used for now can be used again later (T-012 review M3). The first version froze only the bindings in use, which did not match TWIN_IR_SPEC §5.2; this has been corrected.
- Resolving a snapshot needs dataset object storage (T-016). Until it is connected:
  - when the manifest is empty, no object needs to be read;
  - when the manifest is not empty (whether or not it is used) and there is no store, 503 `SERVICE_UNAVAILABLE` is returned, without pretending to have validated.

### HTTP (T-013c)

- The routes are in `internal/models/http.go`, in the same pattern as `internal/project` and `internal/tenant/memberships`: `Routes` is compared with `planning/api-operations.json` (method, path, role), and `api.Handler` registers them. `api.Handler` builds the domain pack registry itself (P0 has only software delivery), so `cmd/api` and the integration tests always use the same set of packs; dataset storage does not exist yet (T-016), so a version with bound datasets returns 503.
- An ID in the path must be a D-001 ID of its kind, otherwise 400; well-formed but nonexistent (or in another organization) is 404.
- **If-Match** (patch, reset, publish): must be exactly one `"draft-revision-N"`. Missing is 428 `PRECONDITION_REQUIRED`; a list, `*`, a weak ETag, `0` or leading zeros, more than 100 characters, or multiple headers is 400; a well-formed revision so large that no draft can reach it is passed to the service as -1, which checks role and tenant first (viewer 403, other organization 404) and then returns 409 (T-013 review L1).
- Draft responses (get, patch, reset) carry the strong ETag `"draft-revision-N"`, equal to the body's revision.
- **Patch body**: `application/json-patch+json` (anything else 415), at most 2 MiB (the same as the IR limit, D-029; beyond it 413). Not matching the patch schema, or having no operation at all, is 422 `REQUEST_VALIDATION_FAILED` (ADR-019: request schema); whether it applies and whether the result is a valid model is decided by the service, 422 `MODEL_VALIDATION_FAILED`, with the issues (including code and severity) as details.
- Mapping of service errors: not found 404; revision or base mismatch 409 `MODEL_VERSION_CONFLICT`; publishing a clean draft 409 `LIFECYCLE_CONFLICT`; cursor 400; an idempotency key used for a different request 409 `IDEMPOTENCY_KEY_CONFLICT`; comment 422 (`/comment`); dataset storage absent 503. Everything else (tenant, transaction) is classified by `httpapi`.
- The frontend client (`apps/web/lib/api/client.ts`) has all nine operations, handwritten on top of the generated contract types (the same as T-014; not a generated client); the Idempotency-Key of a write is given by the caller and reused on retries; keys, IDs and revisions are all checked in the client first, and an invalid one is not sent (a Problem is returned, no exception thrown). The client's tests call every operation with a fake fetch and compare the method and path with `api-operations.json`.

## The owner's decisions (H-009: D-040, D-041 APPROVED, implemented before T-013 opened the routes)

The owner adopted the recommended option for the following two items: the IR columns store canonical text (D-040, option 1), and ModelVersion keeps `comment` (D-041). The analysis from before the decision is kept below.

**D-040 implemented (T-013a)**: migration 0005 changes the two `ir` columns to `text` and adds the CHECK `hash = 'sha256:' || encode(sha256(convert_to(ir,'UTF8')),'hex')`; the service writes canonical bytes, and on read no longer canonicalizes, only checks the hash once more (a mismatch is an error and nothing is returned). T-013 review M1: `patch.Apply` used to produce the candidate with Go's deterministic JSON, which differs from RFC 8785 in two places (`-0`; member names sorted by UTF-8 bytes rather than UTF-16 code units, for example the full-width "：" next to CJK Ext-B characters), so `PatchDraft` stored non-canonical bytes, which the DB's hash CHECK rejected, returning 500. Now `patch.Apply` returns the RFC 8785 form, and validation, hashing and storage use the same bytes (regression tests at the unit, service and HTTP layers). The reviewer's 1.94 MB draft is stored as 1,942,370 bytes (83,639,428 as jsonb), and the local DB tests of `internal/models` (including the new `TestStoredIRIsTheCanonicalBytes`) went from 162 seconds to 38 seconds. 0005 stops on a DB that already has models: SQL cannot produce the canonical form from jsonb, and there is no deployment yet.

**D-041 implemented (T-013b)**: `ModelVersion.comment` (required in the contract, may be null) keeps Publish's comment; an empty string counts as no comment. Migration 0006 limits it to 1 to 2000 characters with `char_length` (counted in characters, like the JSON Schema `maxLength`); versions cannot be modified, so the comment is written only at creation. The service also rejects a comment over 2000 characters, containing U+0000 or not UTF-8 (`ErrInvalidComment`; the HTTP layer rejects it first). The audit metadata still records only IDs and counts.

- **Read amplification of the storage form (T-012 review M4)**: jsonb stores values, not bytes, and on read prints numbers in full decimal (`1e300` prints as 301 digits). The 2 MiB limit measures canonical bytes; the reviewer got 83.6 MB of jsonb text from a 1.94 MB canonical draft, 3.9 seconds for one GetDraft; every patch and publish also has to read it. T-012's check now uses the canonical form (M1), so the limit is no longer misjudged, but the cost is still proportional to the jsonb text, and writes are the same: on re-check, a 1.94 MB canonical draft (83.6 MB of text) took 3.5 seconds for GetDraft, 4.9 seconds for patch, 5.8 seconds for publish (T-012 review 2 N3). Runs and the worker (T-021/T-023) are also affected when they read a version's IR. Recorded as D-040, a decision gate of T-013. Options:
  1. **(Recommended) Store canonical text in the IR columns**: a new migration changes `model_versions.ir`, `model_drafts.ir` (and later the scenario patch etc.) to `json` (keeps the original text) or `text`. What is read is the bytes the hash covers, and SQL can also check the hash with `sha256`; no canonicalization on read is needed any more. Cost: one migration and storage changes; the jsonb operators are lost (no query uses them now).
  2. Restrict how numbers are written in the free containers (metadata, domain_attributes) in the IR contract (for example |x| between 1e-6 and 1e21): needs changes to TWIN_IR_SPEC and the validator, and is a restriction users can see.
  3. Change nothing and rely on rate limits (T-036): not recommended.
- **Publish's `comment` (T-012 review L9; D-041)**: the contract allows up to 2000 characters and includes it in the idempotency hash, but nothing stores it. Options: add a column on ModelVersion to store it (needs a migration and a contract change), or remove it from PublishModel. Recommended: store it: a version note is useful for review, and it is user data, so it does not go into audit.

## Consequences

- All writes queue per model; one model cannot be written concurrently. A P0 model has few editors, so this is acceptable.
- `issues` is the result at write time: datasets are immutable, so the result for the same IR and manifest does not change.
- The version list is newest first, unlike the project list (oldest first); both are stable keyset orders.
- The model list reads the draft and the latest version separately for each model (at most 2×100 queries per page; T-012 review I4); acceptable for P0, and T-013 can merge them into one query if needed.
- `latest_version_id` and the draft revision only increase, guaranteed by the service; the API role has UPDATE on models and model_drafts, and the DB does not enforce it (T-012 review I6).
- An idempotency key expires after 24 hours; after that, a create with the same key creates a second model (the same as createProject, D-011; T-012 review I5).
