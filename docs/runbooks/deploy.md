# Runbook: deployment (containers, migration, Railway)

Basis: T-039 (hackathon slice, H-029 (3)), [ADR-029](../adr/ADR-029-containers-and-staging-deployment.md) (PROPOSED), [ADR-018](../adr/ADR-018-browser-session-and-oidc-provider.md), [ADR-017](../adr/ADR-017-oidc-token-verification.md), [ADR-024](../adr/ADR-024-job-queue-and-worker.md), [ADR-028](../adr/ADR-028-opentelemetry.md), [Auth0 runbook](auth0.md), AGENTS.md §2, §6. Cloud vendor: the owner's choice is **Railway** (H-030 first said 「railway + cloudflare」 ("railway + cloudflare"); H-031 changed it to 「純用 railway」 ("Railway only"), without Cloudflare).

**Who runs it.** The Railway deployment is run by the coordinator, in a session that has a Railway token, with `deploy/railway/deploy.sh --apply` (H-032, §7.7), or by the owner by hand following §7.1–§7.6. The agent that wrote this runbook and the script did not run `--apply`, did not call Railway's API and did not create anything (it ran only the offline `--dry-run` and a rehearsal with stand-ins). The repository and the images hold no credential. The secrets the script generates go to the Railway CLI only through stdin; they are not printed or written to a file. The owner's secrets (§7.3: AI keys, the live switch; with `GEMINUS_AUTH0_SETUP=manual` also the Auth0 client secret) are filled in only by the owner in Railway's UI (and sealed); the script does not set them. When the Railway CLI lists variables it returns the values that are not sealed; the script discards them on receipt (§7.3). After H-034, Auth0 is set up by the Auth0 step of `deploy.sh` with the Auth0 CLI (the owner approves the CLI's login in the owner's own browser; the client secret goes to the Railway CLI only through stdin and is sealed; the CLI logs out at the end), and that step creates the judge account (H-035 (2), §7.8). Only synthetic data. Production approval (making the URL public, turning on live AI) is a human decision (AGENTS.md §6).

§1–§6 are vendor-neutral; §7 is the Railway steps (§7.7 is the automated `deploy.sh`, §7.8 its Auth0 step and the judge account); §8 is the pre-launch checks run by a person; §9 is the local `make staging-smoke`.

## 1. Composition

| Unit | Image/source | Database login | External | Health check | Stop |
|---|---|---|---|---|---|
| migrate (once before each deploy) | `Dockerfile.api`, command `geminus-migrate -dir /migrations up` | `geminus_migrator` (twin_owner) | None | Exit code (0: done, or nothing pending) | Ends when done |
| api | `Dockerfile.api` (`geminus-api`) | `geminus_api` (twin_api) | https (the BFF calls it) | `GET /readyz` (`geminus-api -probe`) | SIGTERM: finishes in-flight requests (20 seconds) |
| worker | `Dockerfile.worker` (`geminus-worker`) | `geminus_worker` (twin_worker) | None (no HTTP listener) | Process alive | SIGTERM: stops the handler, records the attempt (`WORKER_STOPPED`, retried) |
| web | `apps/web/Dockerfile` (Next standalone, BFF) | None | https (the users' URL) | `GET /` | SIGTERM: Next exits at once (no drain) |
| PostgreSQL | managed (16; migrations tested only on 16) | administrator used only for §3.1 | Not external (opened temporarily only during bootstrap) | Platform | — |
| Object storage | S3-compatible private bucket | — | Only the API and the worker, with credentials | — | — |
| seed (an operator command) | `geminus-provision-tenant` (inside the API image, or `go run ./cmd/provision-tenant`) | `geminus_provisioner` (D-042) | None | — | — |

OTLP backend: not set (ADR-028: with no endpoint, nothing is sent).

## 2. Images

Build from the repository root (`make build-containers`; the CI `containers` job does the same):

```sh
docker build -f Dockerfile.api -t geminus-api:<tag> .
docker build -f Dockerfile.worker -t geminus-worker:<tag> .
docker build -f apps/web/Dockerfile -t geminus-web:<tag> .
```

- Base images are pinned by digest (the table in [ADR-011](../adr/ADR-011-toolchain-bootstrap.md)). The Go builder is 1.27.3 from `go.mod`; the runtime is distroless static (uid 65532, no shell, no package manager). The web uses Node 24.21.0 from `.nvmrc` (`node`, uid 1000), and its runtime has no npm.
- No `ARG`, no `ENV` with a credential; `.dockerignore` excludes `.env*`, `.git`, node_modules and build output: secrets come only at run time, from the platform's environment variables. `scripts/check_deploy.py` (part of `make spec-check`) checks these rules.
- Container scan, SBOM and image signing: **NOT RUN** (no tool chosen yet, SECURITY §9).

## 3. Database: roles, logins, the order of migrations

### 3.1 Bootstrap (once per database, administrator)

```sh
psql "$ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 -f db/bootstrap_roles.sql
GEMINUS_MIGRATOR_DB_PASSWORD=… GEMINUS_API_DB_PASSWORD=… \
GEMINUS_WORKER_DB_PASSWORD=… GEMINUS_PROVISIONER_DB_PASSWORD=… \
  psql "$ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 -f deploy/sql/create_logins.sql
```

- `db/bootstrap_roles.sql` creates the NOLOGIN roles twin_owner, twin_api, twin_worker, twin_provisioner (D-008, D-013, D-042).
- `deploy/sql/create_logins.sql` (psql 15 or later) creates four logins. Passwords are read only from environment variables (`\getenv`); each is at least 32 characters and all four differ. Generate them with `openssl rand -hex 32` (hex needs no escaping in a URL), keep them in the owner's password manager, and fill them into the platform's secrets (§7.3). On Railway, `deploy.sh` generates them and stores them directly as sealed variables (§7.7), through no one's hands. Existing logins are unchanged; rotate with `ALTER ROLE … PASSWORD`.
- Result (printed by the script at the end): `geminus_migrator` belongs only to twin_owner; `geminus_api` only to twin_api; `geminus_worker` only to twin_worker; `geminus_provisioner` is NOINHERIT and belongs only to twin_provisioner; none has SUPERUSER, BYPASSRLS, CREATEROLE, CREATEDB, REPLICATION; twin_owner has CREATE on the database.
- The server's log may record a failed statement (with the password), so the script checks every condition before any CREATE ROLE. Do not turn on `log_statement=all`.

### 3.2 Migration (every deploy, before api and worker)

`geminus-migrate -dir /migrations up` reads only `GEMINUS_MIGRATE_DATABASE_URL` (`geminus_migrator`):

- It applies the not-yet-applied `migrations/NNNN_*.up.sql` in order, each in the same transaction as its record (`geminus_migrations.applied`: version, file name, SHA-256, time, login). With nothing pending it does nothing (exit 0). stdout is one JSON: `{"applied":[…],"current":N,"pending":[]}`.
- It refuses (exit 4) a superuser, the runtime logins (api, worker), the provisioning login and a login that cannot act as twin_owner. Runtime logins never migrate; the migration login is only for this step.
- When the database's record does not match the image's files (an applied migration was changed, the database is newer than the image, a twin schema exists without a record), it exits 5 and changes nothing: a deployed migration must not be rewritten (AGENTS.md §3); a person decides.
- `geminus-migrate -dir /migrations status`: the same JSON; exit 6 when something is pending.
- Two concurrent runs take turns (advisory lock). The API and the worker do not check the schema version: a person ensures the deploy order (api and worker are deployed only after migrate completes).

### 3.3 Seed (operator, once)

Users and organizations are created only by an operator (D-008, D-042), not through the API:

```sh
export GEMINUS_PROVISION_DATABASE_URL='postgresql://geminus_provisioner:<password>@<host>:<port>/<db>?sslmode=require'
go run ./cmd/provision-tenant organization -name 'Demo line (synthetic)' \
  -owner-issuer 'https://<tenant>.<region>.auth0.com/' -owner-subject 'auth0|<Auth0 user_id of the owner>'
```

It prints `{"organization_id":"org_…","owner_user_id":"usr_…"}`: put `organization_id` into the worker's `WORKER_ORGANIZATION_IDS`. The issuer must be exactly the Auth0 access token's `iss` (with the trailing `/`); an identity that was not provisioned gets 403 from the API after sign-in. Other users: `provision-tenant user …`, then the owner adds them with the membership API.

## 4. Environment variables (all)

The source is the code that reads the variable. Variables with "Yes" in the **Secret** column go only into the platform's sealed/secret settings, never into the repository, an image, a log, a report or a conversation.

### 4.1 Shared by the API and the worker (`internal/config/config.go`)

| Variable | Services | Required | Secret | Notes |
|---|---|---|---|---|
| `DATABASE_URL` | api, worker | Yes | Yes (contains the password) | Each its own runtime login (`geminus_api`/`geminus_worker`); at startup `tenant.CheckRuntimeRole` refuses a superuser, BYPASSRLS, the owner, a member of other roles (exit 3) |
| `APP_ENV` | api, worker | Set `production` when deploying | No | `development`, `test`, `production` or unset; `production` refuses the fake provider and the filesystem adapter |
| `OBJECT_STORAGE_PROVIDER` | api, worker | Set `s3` when deploying | No | `disabled` (default: uploads, and runs that need a dataset or a trace, return 503), `filesystem` (development/test only), `s3` |
| `OBJECT_STORAGE_ROOT` | api, worker | Only for filesystem | No | Not set when deploying (with s3, setting it refuses startup) |
| `OBJECT_STORAGE_S3_ENDPOINT` | api, worker | With s3 | No | https origin (plain http only on loopback) |
| `OBJECT_STORAGE_S3_BUCKET` | api, worker | With s3 | No | The bucket's S3 name (DNS label) |
| `OBJECT_STORAGE_S3_REGION` | api, worker | With s3 | No | `auto` for a Railway bucket |
| `OBJECT_STORAGE_S3_ACCESS_KEY_ID` | api, worker | With s3 | Yes | The bucket's access key ID |
| `OBJECT_STORAGE_S3_SECRET_ACCESS_KEY` | api, worker | With s3 | Yes | The bucket's secret (printed as `[redacted]`) |
| `OBJECT_STORAGE_S3_URL_STYLE` | api, worker | No | No | `virtual-hosted` (default) or `path`; when the provider is not s3, any `OBJECT_STORAGE_S3_*` refuses startup |

### 4.2 API (`internal/config/auth.go`, `server.go`)

| Variable | Required | Secret | Notes |
|---|---|---|---|
| `AUTH_OIDC_ISSUER` | Yes | No | `https://<tenant>.<region>.auth0.com/` (exactly the token's `iss`) |
| `AUTH_OIDC_AUDIENCE` | Yes | No | The Auth0 API's Identifier (§6.1; must not equal any client ID) |
| `AUTH_OIDC_JWKS_URL` | Yes | No | `<issuer>.well-known/jwks.json` |
| `AUTH_OIDC_ALGORITHMS` | No | No | Default RS256, ES256 |
| `AUTH_OIDC_REQUIRE_AT_JWT` | No (set `true` when deploying) | No | Accepts only `typ: at+jwt` (choose the RFC 9068 profile for the Auth0 API) |
| `API_LISTEN_ADDR` | No | No | Default `:8080`; `geminus-api -probe` uses its port |

### 4.3 Worker (`internal/config/worker.go`)

| Variable | Required | Secret | Notes |
|---|---|---|---|
| `WORKER_ID` | Yes | No | The name in leases and audit (starts with a letter, at most 128); different for each worker, so run only one replica |
| `WORKER_ORGANIZATION_IDS` | Yes | No | The organizations the worker serves (comma-separated, D-008): the `organization_id` of §3.3 |

### 4.4 AI (`internal/config/ai.go`, `apiai.go`; live calls only in the worker; the API holds no key)

| Variable | Services | Required | Secret | Notes |
|---|---|---|---|---|
| `AI_PROVIDER` | api, worker (**the same on both**) | No | No | `disabled` (default), `fake` (development/test only; production refuses it), `anthropic`, `meta` (T-055: Meta Model API's Muse Spark, approved H-029) |
| `AI_POLICY_APPROVAL` | api, worker (**the same on both**) | When live | No | An approval registered in `internal/config/aiapproval` that belongs to `AI_PROVIDER` (`anthropic`: H-018; `meta`: H-029); otherwise `provider_mismatch` |
| `AI_MODEL` | api, worker (**the same on both**) | When live | No | Must equal the approved model (H-029: `muse-spark-1.3`; H-018: `claude-opus-5-5`), and that model must have a price (`internal/ai/spend`); no default |
| `GEMINUS_AI_LIVE` | api, worker (**the same on both**) | No | No | The owner's switch: `on`/`off`/unset (default off). API: when on and the three settings above all match, it accepts proposals; it does not read keys. Worker: also needs a key to call the provider |
| `GEMINUS_ANTHROPIC_API_KEY_A`, `GEMINUS_ANTHROPIC_API_KEY_B` | **worker only** | When Anthropic is live | Yes | The two keys of D-018; not used in the hackathon |
| `GEMINUS_MUSE_API_KEY` | **worker only** | When Meta is live | Yes | The Meta Model API key (one key, label `meta`; format `LLM_<numeric ID>_<secret>`; a key that does not match the format refuses startup); `MODEL_API_KEY` is not read |
| `AI_VISION_PROVIDER` | api, worker (**the same on both**) | No | No | The photo path (T-055 for T-056): unset = follows `AI_PROVIDER`; `disabled` turns it off; `anthropic`/`meta` as a different live provider needs the two variables below, its own key and the same switch; when the approval does not list images (H-018), the photo path stays off (`images_not_approved`) and proposals are unaffected. The API decides with the same logic whether it accepts photos (D-124), without reading keys: when the photo path is off, `createImageProposal` returns 503 before reading the body (`AI_PROVIDER_NOT_CONFIGURED` or `AI_POLICY_NOT_APPROVED`, with `details[0].code` `AI_IMAGE_UNSUPPORTED`) |
| `AI_VISION_POLICY_APPROVAL`, `AI_VISION_MODEL` | api, worker (**the same on both**) | When `AI_VISION_PROVIDER` is a different live provider | No | That provider's approval and model (the same rules as `AI_POLICY_APPROVAL`/`AI_MODEL`); in every other case they must be unset (the API and the worker both refuse to start) |
| `AI_MONTHLY_LIMIT_USD`, `AI_DAILY_LIMIT_USD`, `AI_ORGANIZATION_DAILY_LIMIT_USD` | The same on every worker | No | No | Spending limits (D-110); a limit is a per-process setting, so different workers must have the same values (T-053 I2) |
| `AI_MAX_PROMPT_BYTES`, `AI_MAX_OUTPUT_TOKENS`, `AI_TIMEOUT_SECONDS`, `AI_ATTEMPT_TIMEOUT_SECONDS`, `AI_MAX_RETRIES`, `AI_MAX_CONCURRENT_PER_ORGANIZATION` | worker | No | No | Override the provider's default budget. A request with an image (the photo judge) has `max_tokens` fixed at 32,000 (`provider.ImageMaxOutputTokens`), not affected by `AI_MAX_OUTPUT_TOKENS`; the two timeouts scale by the same ratio (32,000 ÷ `AI_MAX_OUTPUT_TOKENS`), up to 300 s; the spending cap is still the daily limit of D-110 |
| `AI_API_KEY` | — | — | — | Always refused (D-018) |
| `GEMINUS_META_API_KEY` (the old name of the Meta key), `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `MODEL_API_KEY` | — | — | — | Never read (D-018); **if any of them, or `AI_API_KEY`, or any key above is in the API's environment, the API refuses to start** (`key_forbidden`; the error writes only the variable name; T-053 I5) |

**The API holds no AI key (T-053 I5; the T-055 follow-up, the coordinator's decision).** The API decides whether it accepts AI proposals only from four non-secret settings: `AI_PROVIDER`, `AI_POLICY_APPROVAL`, `AI_MODEL`, `GEMINUS_AI_LIVE` (`config.LoadAPIAI`). The check is the worker's live gate (the same code, `liveGate`: an approval recorded in the build, belonging to `AI_PROVIDER`, with no item missing from the record; `AI_MODEL` equals the record's model and has a price; the switch is on), except that it reads and requires no key. When all four match, the API accepts proposals (the worker calls the provider with its own key); the startup log is `AI proposals are accepted; the worker calls the provider` (`provider`, `model`, `code` `LIVE_AI_ON`). On any mismatch: when the switch is not on, the API starts as usual and answers new proposals with 503 (`AI_POLICY_NOT_APPROVED`; `AI_PROVIDER_NOT_CONFIGURED` for `disabled`), and the startup log `AI proposals are refused` records the typed reason (`code`, for example `SWITCH_OFF`, `MODEL_MISMATCH`); when the switch is on, the API refuses to start (exit 1; the error code is that condition), as the worker does. If the API's environment has any AI key variable (the keys in the table above and the names that are never read), the API refuses to start (`key_forbidden`), so keys are set only on the worker. The four settings must be the same on the API and the worker: `scripts/check_deploy.py` checks that `variables.json` declares them alike on both, that the API has no other AI setting, and that the API's `forbidden` list contains every key variable the API refuses (the list is taken from `internal/config/apiai.go`); the owner sets the values in Railway's UI, which the static check cannot see. When the API accepts but the worker cannot call the provider: if the worker's switch is on and it has no key, the worker refuses to start (it builds no handler and opens no connection), and proposals stay in generating until a worker starts; if the worker's switch is not on, every proposal fails as `AI_POLICY_NOT_APPROVED`; if the provider rejects the key, the proposal fails as `AI_CREDENTIALS_EXHAUSTED` (`SERVICE_UNAVAILABLE` at the HTTP layer). A failed proposal is not retried and writes nothing except its error, job and audit (`TestAProposalTheWorkerCannotAskFailsSafely` in `internal/proposals/generator`). The live switch is off by default; only the owner turns it on, and on the API and the worker together.

**Photo availability on the API side (the T-056 follow-up, D-124 PROPOSED, the coordinator's decision).** The API also reads the photo path's three non-secret settings `AI_VISION_PROVIDER`, `AI_VISION_POLICY_APPROVAL`, `AI_VISION_MODEL`, and decides whether it accepts photos with the worker's same logic (`decide` in `internal/config`: the worker calls it through `loadLive`, which reads keys, and the API through `keyFree`, which does not). It accepts photos with fake (only in development/test), or when the live provider's approval lists images (H-029), `AI_VISION_MODEL` (or `AI_MODEL` when following) matches and the switch is on. Otherwise `createImageProposal` returns 503 before reading the body (`AI_PROVIDER_NOT_CONFIGURED` when the photo path is `disabled`, `AI_POLICY_NOT_APPROVED` when the live provider is not on; in both, `details[0].code` is `AI_IMAGE_UNSUPPORTED`, the code the worker would use) and stores nothing. Text proposals are unaffected (for example, with Claude as the main provider and no photo path, text proposals are accepted as usual and photos get 503). Startup log: when the photo path is a live provider, `photo proposals are accepted; the worker calls the provider` (`code` `PHOTO_PATH_ON`) or `photo proposals are refused` (`code` is the typed reason, for example `IMAGES_NOT_APPROVED`). When the photo path's settings do not match and the switch is on, the API refuses to start (with the same error code as the worker). The three settings are declared alike on api and worker in `variables.json` (`scripts/check_deploy.py`), and the owner sets the same values.

### 4.5 Telemetry (`internal/config/telemetry.go`, api, worker)

`OTEL_SDK_DISABLED`, `OTEL_TRACES_EXPORTER`, `OTEL_METRICS_EXPORTER`, `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`, `OTEL_EXPORTER_OTLP_HEADERS` (secret: the collector's credential), `OTEL_EXPORTER_OTLP_TRACES_HEADERS`, `OTEL_EXPORTER_OTLP_METRICS_HEADERS` (secret), and `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER`, which the SDK reads itself. All optional; the hackathon deployment **sets none of them** (with no endpoint, no telemetry is sent, ADR-028). Logs are JSON lines on stderr (visible in the platform's log interface; no prompts, answers or keys).

### 4.6 Web (BFF, `apps/web/lib/session/config.ts`, `seal.ts`; Next)

| Variable | Required | Secret | Notes |
|---|---|---|---|
| `BFF_APP_ORIGIN` | Yes | No | `https://<web domain>` (the Origin for CSRF, and the redirect URI) |
| `BFF_API_URL` | Yes | No | The API's https base URL (§7.6) |
| `BFF_OIDC_ISSUER` | Yes | No | The same as `AUTH_OIDC_ISSUER` |
| `BFF_OIDC_AUTHORIZATION_URL` | Yes | No | `<issuer>authorize` |
| `BFF_OIDC_TOKEN_URL` | Yes | No | `<issuer>oauth/token` |
| `BFF_OIDC_REVOCATION_URL` | Yes | No | `<issuer>oauth/revoke` |
| `BFF_OIDC_END_SESSION_URL` | Yes | No | `<issuer>oidc/logout` |
| `BFF_OIDC_JWKS_URL` | Yes | No | `<issuer>.well-known/jwks.json` |
| `BFF_OIDC_CLIENT_ID` | Yes | No | The Auth0 Application's Client ID |
| `BFF_OIDC_CLIENT_SECRET` | Yes | Yes | The Auth0 Application's Client Secret |
| `BFF_OIDC_AUDIENCE` | Yes | No | The same as `AUTH_OIDC_AUDIENCE` |
| `BFF_OIDC_SCOPES` | No | No | Not set (default `openid offline_access`) |
| `SESSION_KEYS` | Yes | Yes | `<id>:<32 bytes base64url>` (generation and rotation in [Auth0 runbook](auth0.md) §4) |
| `GEMINUS_UI_GALLERY` | No | No | The component gallery page, for browser checks only; **not set** when deploying |
| `PORT`, `HOSTNAME` | No | No | Next's listener (the image defaults to 3000, 0.0.0.0) |
| `NODE_ENV`, `NEXT_TELEMETRY_DISABLED` | No | No | The image already sets `production`, `1` |

Every BFF URL must be https (http only when all of them are on loopback). When the settings are incomplete, `/auth/*` and `/api/v1/*` return 503, and the log writes only variable names.

### 4.7 Operator commands and login passwords

| Variable | Used by | Secret | Notes |
|---|---|---|---|
| `GEMINUS_MIGRATE_DATABASE_URL` | migrate (`cmd/migrate`) | Yes | The URL of `geminus_migrator`; only for migrate, never for api, worker or web |
| `GEMINUS_PROVISION_DATABASE_URL` | seed (`cmd/provision-tenant`) | Yes | The URL of `geminus_provisioner`; only on the operator's machine |
| `GEMINUS_MIGRATOR_DB_PASSWORD`, `GEMINUS_API_DB_PASSWORD`, `GEMINUS_WORKER_DB_PASSWORD`, `GEMINUS_PROVISIONER_DB_PASSWORD` | `deploy/sql/create_logins.sql`; on Railway, references build each service's URL from them | Yes | §3.1 |
| `AI_EVAL_LIVE_MAX_USD` | `make ai-eval-live` (not a deployment variable) | No | The live eval's cap (T-053) |

## 5. HTTPS, cookies, CSP, data

- **TLS**: the platform's edge terminates TLS (Railway issues and renews certificates automatically); inside the container it is plain HTTP. The browser connects only to the web's https URL; the BFF connects to the API and to Auth0 over https; the API fetches Auth0's JWKS over https; the API and the worker connect to the bucket over https. Database connections are on the platform's private network (WireGuard), and Railway's PostgreSQL image has SSL; when connecting from the operator's machine through the TCP proxy, use `sslmode=require` (encrypted; the server certificate is not verified).
- **Cookies**: `__Host-geminus_session` and `__Host-geminus_login` are always `HttpOnly; Secure; SameSite=Lax; Path=/` with no Domain (independent of `APP_ENV`, `apps/web/lib/session/session.ts`), so they work only over https; browser JS cannot get the token (ADR-018).
- **CSP and security headers** (T-057, SECURITY §3): every web response has a Content-Security-Policy (one nonce per request), `X-Content-Type-Options`, `Referrer-Policy: no-referrer`, `Permissions-Policy`, `X-Frame-Options: DENY`, and no `X-Powered-By`. `Strict-Transport-Security: max-age=31536000` is sent only when `BFF_APP_ORIGIN` is https (on Railway it is `https://${{RAILWAY_PUBLIC_DOMAIN}}`, so it is sent; it is read at runtime, so no rebuild is needed). After deploying, confirm with `curl -sI https://<web domain>/`. Whether the Railway edge adds headers of its own is not verified.
- **Data**: only synthetic data and demo images. The bucket's encryption, retention and region are the provider's defaults, not decided by the owner (ADR-029). No customer data, and no personal data other than that of the real users.

## 6. Auth0 (the owner's tenant, the final domain)

After H-034 this is set up by the Auth0 step of `deploy.sh` (§7.8) with the Auth0 CLI. By hand, follow [Auth0 runbook](auth0.md) §2–§4 (steps in its §6) to create the API and the Regular Web Application, using the web service's public domain (§7.6, for example `<web>.up.railway.app`):

| Setting | Value |
|---|---|
| API Identifier (audience) | For example `https://api.geminus-demo.invalid` (it need not be reachable; it cannot be changed later) → `AUTH_OIDC_AUDIENCE`, `BFF_OIDC_AUDIENCE` |
| Access token profile | RFC 9068 (`typ: at+jwt`) → `AUTH_OIDC_REQUIRE_AT_JWT=true` |
| Allowed Callback URLs | `https://<web domain>/auth/callback` (this one only) |
| Allowed Logout URLs | `https://<web domain>/` |
| Allowed Web Origins, Allowed Origins (CORS) | Empty |
| Grant types | Only Authorization Code and Refresh Token |
| Sign-up | Off (users are provisioned only through §3.3) |

When the domain changes (for example, when your own domain is added later), change the callback/logout URLs and `BFF_APP_ORIGIN` together, and redeploy the web.

## 7. Railway (the owner's choice, H-031: Railway only)

The following cites Railway's official documentation (fetched 2026-10-26): [CLI](https://docs.railway.com/guides/cli), [template variable functions](https://docs.railway.com/templates/create), [variables reference](https://docs.railway.com/variables/reference), [managing secrets](https://docs.railway.com/guides/managing-secrets-on-railway), [config as code](https://docs.railway.com/reference/config-as-code), [monorepo](https://docs.railway.com/guides/monorepo), [Dockerfiles](https://docs.railway.com/guides/dockerfiles), [start command](https://docs.railway.com/guides/start-command), [healthchecks](https://docs.railway.com/guides/healthchecks), [pre-deploy command](https://docs.railway.com/guides/pre-deploy-command), [variables](https://docs.railway.com/guides/variables), [private networking](https://docs.railway.com/guides/private-networking), [public networking](https://docs.railway.com/guides/public-networking), [PostgreSQL](https://docs.railway.com/guides/postgresql), [Storage Buckets](https://docs.railway.com/storage-buckets), [Storage Buckets guide](https://docs.railway.com/guides/storage-buckets), [restart policy](https://docs.railway.com/deployments/restart-policy).

### 7.1 Project and services

In one Railway project (a separate environment is recommended, for example `staging`), create four services from the same GitHub repository, plus PostgreSQL and a Bucket:

| Service | Config-as-code file (enter the absolute path in the service's Settings) | Public domain |
|---|---|---|
| `migrate` | `/deploy/railway/migrate.railway.json` | None |
| `api` | `/deploy/railway/api.railway.json` | Generate Domain |
| `worker` | `/deploy/railway/worker.railway.json` | None |
| `web` | `/deploy/railway/web.railway.json` | Generate Domain |
| `Postgres` | Railway's PostgreSQL template | None (a TCP proxy opened only temporarily in §7.5) |
| `geminus-objects` | Bucket (Storage Buckets) | None (private) |

- Root directory is not set (the build context is the repository root; all three Dockerfiles are written that way). Railway docs: the config file does not follow the root directory, so it needs an absolute path; settings in code always override the dashboard's.
- The config files set the Dockerfile (`build.dockerfilePath`), watch paths, healthcheck, restart policy, `drainingSeconds` (the time from SIGTERM to SIGKILL: 30 seconds for api and worker) and 1 replica. `migrate`'s start command is `/usr/local/bin/geminus-migrate -dir /migrations up` (Railway docs: the start command, in exec form, replaces the image's ENTRYPOINT), with restart policy `NEVER`: with exit code 0 the deployment is Completed.
- **Migrations do not run as a pre-deploy command**: Railway docs say a pre-deploy command uses the service's environment variables, so api's pre-deploy would give the API service the migration login. Instead a separate `migrate` service is used, and only it has `GEMINUS_MIGRATE_DATABASE_URL`.
- If the service names differ, the references in `deploy/railway/variables.json` (`${{Postgres.PGHOST}}`, `${{api.RAILWAY_PUBLIC_DOMAIN}}`, `${{geminus-objects.ENDPOINT}}`) must change with them.

### 7.2 Healthcheck, port, stopping

- Railway's healthcheck runs only when a deployment starts: it calls the path on `PORT` from `healthcheck.railway.app`; any 2xx is success; it waits 300 seconds by default (the config sets 120). There is no continuous monitoring after go-live. The API sets `PORT=8080` and `API_LISTEN_ADDR=:8080`, path `/readyz`; the web sets `PORT=3000`, path `/`; worker and migrate have no healthcheck.
- The image `HEALTHCHECK` (`geminus-api -probe`, the web's `node -e fetch(...)`) is for Docker and compose; Railway uses the config's healthcheck.

### 7.3 Variables and secrets

`deploy/railway/variables.json` is the per-service list (`make spec-check` checks that it matches the code). A `value` is written as is (`{{…}}` is filled in by `deploy.sh` with this run's inputs, the bucket's settings or the seed's result; `${{…}}` is a Railway reference and is kept as is). `secret: true` has no value, and `source` says where it comes from. Secrets are always set as **sealed** (Railway docs: a sealed value is provided to builds and deployments but is visible in neither the UI nor the API, is not brought out by `railway variables`/`railway run`, and cannot be unsealed; CLI 5.64.2's `variable list --json` lists sealed values as `null`). The images declare no `ARG`, so variables do not enter the build.

**The list of secrets (all only in Railway's sealed variables; for a manual deploy, also kept in the owner's password manager):**

| Secret | Service | Who sets it (`source`) | Origin |
|---|---|---|---|
| `GEMINUS_MIGRATOR_DB_PASSWORD` | migrate | `deploy.sh` (`generated`) | Python `secrets.token_hex(32)`, through stdin |
| `GEMINUS_API_DB_PASSWORD` | api | `deploy.sh` (`generated`) | Same as above |
| `GEMINUS_WORKER_DB_PASSWORD` | worker | `deploy.sh` (`generated`) | Same as above |
| `GEMINUS_PROVISIONER_DB_PASSWORD` | Not on Railway | `deploy.sh` generates a new one on every run; it exists only in that run's seed environment and is stored nowhere | §3.3 |
| `SESSION_KEYS` | web | `deploy.sh` (`generated`, `format: session-key`) | `k1:` plus 32 bytes of base64url (the format of [Auth0 runbook](auth0.md) §4) |
| `OBJECT_STORAGE_S3_ACCESS_KEY_ID`, `OBJECT_STORAGE_S3_SECRET_ACCESS_KEY` | api, worker | `deploy.sh` (`bucket:…`) | The values of `railway bucket credentials --json`, copied through stdin (not printed) |
| `BFF_OIDC_CLIENT_SECRET` | web | The Auth0 step of `deploy.sh` (`auth0`, H-034; required); with `GEMINUS_AUTH0_SETUP=manual`, the **owner** | The Auth0 Application's Client Secret: read from the Auth0 CLI's JSON inside the deploy script's process, handed only through stdin to `railway variable set --stdin`, sealed at once (§7.8) |
| `GEMINUS_ANTHROPIC_API_KEY_A`, `GEMINUS_ANTHROPIC_API_KEY_B` | **worker only** | **owner** (optional; only when turning on live AI) | The two keys of D-018 |
| `GEMINUS_MUSE_API_KEY` | **worker only** | **owner** (optional; only when turning on Meta live AI) | Meta Model API (Standard mode, H-029) |
| `GEMINUS_AI_LIVE` (not a secret, but the owner's switch) | api, worker (**the same on both**) | **owner** (optional, default off) | §4.4 |
| `AI_POLICY_APPROVAL`, `AI_MODEL` (not secrets; the owner's settings) | api, worker (**the same on both**) | **owner** (optional; only when turning on live AI) | §4.4 (`deploy.sh` sets `AI_PROVIDER` to `disabled` only when it is missing; when the owner changes it to `anthropic` or `meta`, change it on both services together) |
| `AI_VISION_PROVIDER`, `AI_VISION_POLICY_APPROVAL`, `AI_VISION_MODEL` (not secrets; the owner's settings) | api, worker (**the same on both**) | **owner** (optional; unset = the photo path follows `AI_PROVIDER`) | §4.4 (the API decides from these three settings and the four above whether it accepts photos, D-124) |

The owner's variables: `deploy.sh` does not set them; it only checks at the end that the required ones exist. When one is missing, the verification fails and says on which service to fill it in (§7.7). `BFF_OIDC_CLIENT_SECRET` (`source: auth0`) is set and sealed by the Auth0 step of §7.8; with `GEMINUS_AUTH0_SETUP=manual` the owner fills it in instead, like the owner's variables (its value is discarded on receipt; when it is missing, the verification says where to fill it in). Live AI is off by default (`AI_PROVIDER=disabled`, no `GEMINUS_AI_LIVE`).

**What the script actually receives (review round 1 M2).** The only way to know which variables exist is `railway variable list --service <service> --json`, and CLI 5.64.2 returns in it **the value of every variable that is not sealed** (sealed ones are `null`; CLI source `src/commands/variable.rs`: "JSON and KV output include raw variable values"; there is no names-only option). So the values of an owner's `BFF_OIDC_CLIENT_SECRET` or AI keys that are not sealed enter the process running `deploy.sh` through the CLI's output. What the script does: at the moment it parses the JSON (`json.loads`'s `object_pairs_hook`), it keeps only the values it needs, namely the passwords it generated itself (to align the logins) and Postgres's `DATABASE_PUBLIC_URL`, and registers them as secrets (masked as `[redacted]` in output, never allowed in arguments). Every other value (all of the owner's, Railway's own, those not listed in `variables.json`) is replaced at parse time by a "set" or "sealed" marker; it enters no dict, output, error message or exception, and is written nowhere. Python cannot wipe memory: the CLI's raw output exists briefly in the process until it is collected. **The owner should seal a variable right after adding it** (service → Variables → the 3-dot menu on the right of the variable → Seal; Railway docs: a sealed variable cannot be unsealed, but its value can still be changed); after that the CLI returns only `null` and the value never reaches the script. The verification lists by name the owner secrets that are not yet sealed (`BFF_OIDC_CLIENT_SECRET`, `GEMINUS_ANTHROPIC_API_KEY_A`/`_B`, `GEMINUS_MUSE_API_KEY`) as a reminder to seal them; `GEMINUS_AI_LIVE` is not a secret and gets no reminder (its value is discarded too). This still falls short of the letter of H-033, "only look at variable names, never read values" (when a variable is not sealed, its value passes briefly through the script); the coordinator passes this to the owner to decide.

The variables in the `forbidden` lists of the API and the web (`variables.json`) must not be set on that service: the API has no AI key (§4.4: every key variable the API refuses to start with; the live switch, the other three AI settings and the photo path's three settings are the same as the worker's), and the web has no database, bucket or AI key.

### 7.4 Bucket (Railway Storage Buckets)

Railway docs (2026-10-26): Buckets are private, S3-compatible object storage (running on Tigris) that can be modified only with the bucket's credentials. Public buckets are not supported. The Credentials page provides `BUCKET`, `ACCESS_KEY_ID`, `SECRET_ACCESS_KEY`, `REGION` (for example `auto`), `ENDPOINT` (for example `https://t3.storageapi.dev`), which can be passed to services with reference variables. New buckets use virtual-hosted URLs; older ones may need path-style (the Credentials page says so). The region cannot change after creation. Currently buckets are reachable only over the public network. A service uploading to a bucket counts as service egress. Server-side encryption, versioning, object lock and lifecycle are not supported.

1. Create the bucket (name `geminus-objects`) and choose the region (it cannot change after creation, §10 question 3).
2. Set `OBJECT_STORAGE_*` on api and worker per `variables.json`; when the Credentials page says path-style, set `OBJECT_STORAGE_S3_URL_STYLE=path`.
3. **Before putting any data in, confirm that the bucket refuses overwrites** (the adapter uses `If-None-Match: *` to guarantee that an object is not replaced once written; Railway's docs do not mention conditional PUT; Tigris's docs say `If-None-Match: "*"` returns 412 when the object exists). The owner (or a session the owner authorized that can get the credentials) runs this on their own machine:

   ```sh
   GEMINUS_LIVE_S3=1 OBJECT_STORAGE_S3_ENDPOINT=… OBJECT_STORAGE_S3_BUCKET=… OBJECT_STORAGE_S3_REGION=auto \
   OBJECT_STORAGE_S3_ACCESS_KEY_ID=… OBJECT_STORAGE_S3_SECRET_ACCESS_KEY=… \
     go test -count=1 -run TestLiveBucket -v ./internal/storage/objects/
   ```

   It writes two very small synthetic objects (under an organization that does not exist), confirms that the bucket itself rejects the second PUT, reads them back and deletes them. If it fails, do not use this bucket (report to the coordinator).

### 7.5 Database bootstrap and seed (once)

`deploy.sh` does 1–5 below automatically (steps 4–6 of §7.7); for a manual deploy, follow them:

1. Postgres service → Settings → Networking: turn on Public Access temporarily (TCP proxy; Railway docs: it creates `DATABASE_PUBLIC_URL`, and traffic through the proxy is billed). Confirm the major version with `SELECT version()` (migrations are tested only on 16; if it is not 16, report first).
2. On the owner's machine, run the two SQL files of §3.1 with `DATABASE_PUBLIC_URL` (administrator: the template's postgres login, which must be able to CREATE ROLE; add `sslmode=require` to the URL).
3. Deploy `migrate`; check that it ends as Completed and that the JSON in the log is `"pending":[]`.
4. The seed of §3.3: `GEMINUS_PROVISION_DATABASE_URL` with `geminus_provisioner`, the proxy's host and port, `sslmode=require`.
5. Turn off Public Access.

### 7.6 Deploy order and web → api

1. `migrate` (deploy it first whenever there is a new migration, and wait for Completed).
2. `api`, `worker` (the worker's `WORKER_ORGANIZATION_IDS` uses the seed's result).
3. `web`.
4. Auth0's callback/logout URLs use the web's domain (§6).

`deploy.sh` follows this order (steps 5 and 7 of §7.7). The web calls the API through the API's public domain: `BFF_API_URL=https://${{api.RAILWAY_PUBLIC_DOMAIN}}`. Railway's private network (`http://api.railway.internal:8080`, WireGuard-encrypted) is plain http, and the BFF accepts http only when every URL is loopback, so it is not used; switching to the private network first needs a change to the BFF's check and a separate review (§10 question 2). The API's public domain accepts only Bearer tokens (no cookies, no CORS).

### 7.7 `deploy/railway/deploy.sh` (H-032, non-interactive)

Run by the coordinator (or the owner) on a machine with the token; the agents of this repository did not run `--apply`.

```sh
deploy/railway/deploy.sh --install-cli "$HOME/.local/railway-5.64.2"       # once: install the pinned Railway CLI
deploy/railway/deploy.sh --install-auth0-cli "$HOME/.local/auth0-1.37.0"   # once: install the pinned Auth0 CLI (§7.8)
export PATH="$HOME/.local/railway-5.64.2:$HOME/.local/auth0-1.37.0:$PATH"
export RAILWAY_API_TOKEN=…      # or RAILWAY_TOKEN=… (see below); keep it out of shell history and every file
export GEMINUS_AUTH0_ISSUER='https://<tenant>.<region>.auth0.com/' GEMINUS_AUTH0_AUDIENCE='https://api.geminus-demo.invalid' \
       GEMINUS_SEED_OWNER_SUBJECT='auth0|<user_id of the owner>' GEMINUS_RAILWAY_BUCKET_REGION=sjc
deploy/railway/deploy.sh --dry-run     # offline: checks the inputs, the tools and the deploy files, prints every command of --apply
deploy/railway/deploy.sh --apply       # step 2b waits for the owner to approve the Auth0 CLI's login in the owner's own browser (§7.8)
```

`GEMINUS_AUTH0_SETUP=manual` (when the Auth0 CLI path cannot be used; the manual fallback of §7.8): the Auth0 step does not run; `GEMINUS_AUTH0_CLIENT_ID='<Client ID>'` is needed in addition, and the owner fills in and seals `BFF_OIDC_CLIENT_SECRET` in Railway (T-039's original flow). With `GEMINUS_JUDGE_SUBJECT='auth0|<user_id of the judge account>'` set, the seed makes the judge account an editor of the demo organization.

- **Token** ([CLI docs](https://docs.railway.com/guides/cli), 2026-10-26; `src/config.rs` of CLI v5.64.2): `RAILWAY_TOKEN` is a project token; the project and the environment are the token's own and must already exist; the script adds no `--environment`. **A project token cannot create services** (found at the first `--apply` on 2026-10-27): CLI 5.64.2's `railway add` first creates the service, then records it in this directory's link (`src/commands/add.rs`, `Configs::link_service`); a project token has no such link, and `railway link` needs to list workspaces, which a project token cannot do either. So `add` reports "Project not found. Run `railway link`" after the service has been created. Therefore, under a project token, if any service is missing, the script stops before creating anything and asks for `RAILWAY_API_TOKEN` instead. A project token suits only a project whose services all exist already. With no token at all, a logged-in CLI (`railway login --browserless`: the CLI prints a URL and a code, the owner approves in the owner's own browser, and the session stays in the CLI's own `~/.railway`, which the script does not read) counts as an account token (it is enough that `railway whoami --json` succeeds); run `railway logout` after the deploy. `RAILWAY_API_TOKEN` is an account/workspace token: the script finds `GEMINUS_RAILWAY_PROJECT` (default `geminus-demo`) by name, runs `railway init` if it does not exist (`GEMINUS_RAILWAY_WORKSPACE` chooses the workspace), then links `GEMINUS_RAILWAY_ENVIRONMENT` (default `production`). When both are set, the CLI decides the project by `RAILWAY_TOKEN`. The token is read only by the CLI, from the environment: it is in no argument, output or log. The script registers the token as a secret (printed as `[redacted]` when a CLI error message quotes it) and removes it from the environment of every other program (psql, `go run`, git, `check_deploy.py`). `deploy.sh` refuses (exit 2) before running any command when xtrace is on (`bash -x`, `set -x` or `SHELLOPTS=xtrace`).
- **CLI version**: pinned at 5.64.2. `--install-cli` downloads the GitHub release's `railway-v5.64.2-x86_64-unknown-linux-musl.tar.gz` and checks the SHA-256 `a154c3ff5ac3e2ece99bdca9d8d5505fc7f85a949689346bd934fb425246e229` (computed 2026-10-26; the release has no checksum file, and npm's `@railway/cli@5.64.2` downloads the same file without checking it). Only Linux x86_64 is supported; on other platforms install the same version yourself. `--apply` checks that `railway --version` is 5.64.2 and runs with `CI=true`, `RAILWAY_NO_AUTO_UPDATE=1`, so the CLI does not update itself.
- **Requires**: python3, git, psql 15 or later (`\getenv`), go (`go run ./cmd/provision-tenant`). It refuses when the checkout has uncommitted changes (`railway up` uploads the working tree; `GEMINUS_ALLOW_DIRTY=1` accepts them).
- **Steps** (the docstring of `railway_deploy.py`; each step checks first and creates only what is missing, so it can be re-run):
  1. the project and the environment;
  2. PostgreSQL, the bucket (`geminus-objects`; the region cannot change after creation), the four services, each one's config-as-code file (`railway environment edit --service-config <service> configFile /deploy/railway/<service>.railway.json`), the public domains of api and web;
  2b. Auth0 (H-034, `GEMINUS_AUTH0_SETUP=cli`, the default): §7.8. When it finishes, the Auth0 CLI is logged out and its home is deleted, `GEMINUS_AUTH0_CLIENT_ID` is this step's output (for step 3's `BFF_OIDC_CLIENT_ID`), and the web's `BFF_OIDC_CLIENT_SECRET` is set and sealed;
  3. the variables of `variables.json` (`--skip-deploys`): generated secrets are generated with Python's `secrets` only when missing and set with `railway variable set KEY --stdin`; the bucket's keys are copied the same way; the owner's variables are not set (values received when listing variables are discarded, §7.3);
  4. it opens Postgres's TCP proxy for this run (when one already exists, it reuses it and does not remove it: the script cannot tell whether someone else opened it or an interrupted run left it; it reports it at the end, see "TCP proxy" below), and with the administrator's `DATABASE_PUBLIC_URL` (`sslmode=require`, only in psql's environment) runs `db/bootstrap_roles.sql` and `deploy/sql/create_logins.sql`; when a login exists and Railway's password can still be read, it aligns them with `ALTER ROLE … PASSWORD`; then it seals the generated and copied secrets (`variables.<KEY>.isSealed`);
  5. it deploys `migrate` and waits until the number of rows in `geminus_migrations.applied` equals the number of migrations in this checkout;
  6. when the worker has no `WORKER_ORGANIZATION_IDS` yet, it seeds a synthetic organization (`GEMINUS_SEED_ORG_NAME`, owner `GEMINUS_SEED_OWNER_SUBJECT`; the judge account becomes an editor in the same transaction with `-member-issuer`/`-member-subject`/`-member-role editor`: §7.8) and sets the `organization_id` on the worker; then it lists the judge account's memberships as the administrator: it must be only an editor of the demo organization, otherwise the verification fails and says what to do; it removes the TCP proxy this run opened (also when steps 4–6 fail, and on SIGTERM or SIGHUP);
  7. `railway up` for api, worker, web, waiting for each to succeed (api and web have Railway's healthcheck);
  8. verify: api's `/healthz` and `/readyz` 200, `/v1/me` 401; web's `/` 200, `/auth/login` 302/303 to `<issuer>authorize`; the worker still SUCCESS; the owner's required variables exist (names only); owner secrets that are not sealed are listed by name with a reminder to seal them (not a failure); Postgres has no TCP proxy open; it prints the callback and logout URLs to enter in Auth0.
- **TCP proxy (review round 1 M1)**: Postgres's public TCP proxy lets the administrator (the template's `postgres` superuser) log in from the public network, so it must not be left open silently. (a) The proxy this run opened is removed at the end of steps 4–6, and also on failure and on SIGTERM or SIGHUP (both are turned into an exit so that the cleanup runs; a second signal is ignored during the cleanup); SIGKILL cannot be caught, and a proxy left behind is reported by the next run. (b) When `railway tcp-proxy delete` exits non-zero, the script writes the proxy's id and the command to remove it by hand, and the whole run ends with exit 1. (c) Before every `--apply` ends (whether it succeeded, failed or was interrupted by a signal; in the verification, or at the end when it stopped before the verification), it lists Postgres's TCP proxies: if any proxy is still open, including one that existed before the run and was reused, it writes its id and the removal command (`railway tcp-proxy delete <id> --yes --service Postgres --environment <environment>`) and ends with exit 1; failing to list them is also exit 1. Only when `GEMINUS_KEEP_DB_PROXY=1` is explicitly set is an open proxy listed only as a warning (still with its id) and not counted as a failure; it does not exempt (b). The script does not remove a proxy that existed before the run (the owner may have opened it by hand per §7.5), but every run reports it until someone removes it or sets `GEMINUS_KEEP_DB_PROXY=1`.
- **Result**: exit 0 done; exit 1 a step failed or the TCP proxy is still open (the message says which step and what to do, for example `the owner has not set BFF_OIDC_CLIENT_SECRET on web (Railway → web → Variables)`; once the owner fills it in, just re-run); exit 2 usage error or xtrace on; exit 128+N interrupted by signal N (SIGTERM 143, SIGHUP 129; the cleanup has run; just re-run). The first run always stops at the verification before the owner fills in `BFF_OIDC_CLIENT_SECRET`: this is expected.
- **Secret generation**: H-032 wished to use Railway's `${{secret(...)}}`. Railway's docs describe it only as a **template** function ([templates/create](https://docs.railway.com/templates/create): "When a template is deployed, all template variable functions are executed"); [variables](https://docs.railway.com/guides/variables) and [variables reference](https://docs.railway.com/variables/reference) do not say that an ordinary service variable can use it; a community answer ([station](https://station.railway.com/questions/secret-generates-a-new-string-on-8523cc4d)) says that at the service level it may regenerate every time. So the script does not use it: it generates locally, sets through stdin, then seals. Generated values appear in no output, file or argument (checked before every call).
- **Not verified: whether a reference to a sealed variable is expanded.** `DATABASE_URL` and `GEMINUS_MIGRATE_DATABASE_URL` reference the sealed passwords with `${{GEMINUS_…_DB_PASSWORD}}`; Railway's docs do not say whether a reference expands a sealed value (the community has one case where a sealed reference still worked, but that is not documentation). If it does not expand, step 5's migrate fails with a wrong password, and the script stops there and points here. **Recovery**: in Railway's UI delete `GEMINUS_*_DB_PASSWORD` from migrate, api and worker (sealed ones can be deleted), and re-run with `GEMINUS_RAILWAY_SEAL=off deploy/railway/deploy.sh --apply`: the script regenerates the passwords, aligns them with `ALTER ROLE`, does not seal them (weaker protection: visible in the UI and in `railway variable list`), and reports to the coordinator (§10 question 5).
- **Rehearsal**: `scripts/rehearse_railway_deploy.py` runs the full `--apply` several times with stand-ins for `railway`, `psql` and `go` (offline). It checks: secrets go only through stdin and appear in no argument or output (including values and the token quoted in CLI error messages); the environment of psql and go has no token; the owner's variables are not set; when the CLI returns owner values that are not sealed, those values appear in none of the script's state, output, errors or any method's return value, and there is a by-name reminder to seal them; the API has no AI key; the deploy order; only api and web have domains; the seed runs only once; the TCP proxy is removed; generated and copied secrets are all sealed (none with `GEMINUS_RAILWAY_SEAL=off`); a second run creates nothing and changes no secret; the verification fails when the owner has not filled in a value, and separately when the login redirect is wrong; a failed TCP proxy removal, a proxy that existed before the run, a CLI that says it removed the proxy while it is still there, a create that prints no id, a final listing that fails, SIGTERM and SIGHUP (run as separate processes) all end non-zero and write the id and the removal command (with `GEMINUS_KEEP_DB_PROXY=1` they succeed but still list it); `deploy.sh` refuses under xtrace. It proves that the script is consistent with itself, not that Railway accepts every call (NOT RUN until the coordinator's `--apply`). The runs above use `GEMINUS_AUTH0_SETUP=manual`; the Auth0 step is rehearsed separately with an Auth0 CLI stand-in (the last item of §7.8).

### 7.8 The Auth0 step (H-034) and the judge account (H-035 (2))

Run by the coordinator in the deploy session as part of `deploy.sh --apply` (step 2b, after the web's public domain is created and before step 3's variables). The agent that wrote it did not log in and did not call any Auth0 tenant (only stand-ins and `--dry-run`).

**What the owner does on deploy day.** (1) Beforehand: sign up for Auth0 and create the tenant (the owner handles the account and the terms in person), create the owner's own user in the database connection (default `Username-Password-Authentication`), and share the tenant's domain and the owner's own `user_id` (the coordinator passes them in as `GEMINUS_AUTH0_ISSUER`, `GEMINUS_SEED_OWNER_SUBJECT`; the `user_id` is not written into the repo). (2) When `--apply` reaches 2b, the screen shows a line `Auth0 sign-in: open https://…auth0.com/activate?user_code=XXXX-XXXX …, check that it shows the code XXXX-XXXX, and approve`: open it in the owner's own browser, check that the code on the page is the same, sign in with the owner's own Auth0 account and approve (no password or token is handed over; within 15 minutes by default). (3) When the run ends, stderr shows the judge account's email and password once: copy them directly into the submission notes; do not save them to any file or evidence. The script does everything else.

**The CLI pin.** [auth0/auth0-cli](https://github.com/auth0/auth0-cli) release [v1.37.0](https://github.com/auth0/auth0-cli/releases/tag/v1.37.0) (released 2026-09-30, 26 days old on 2026-10-26; v1.38.0 is from 10-19, less than two weeks old), MIT (`LICENSE` in the tag). `--install-auth0-cli DIR` downloads `auth0-cli_1.37.0_Linux_x86_64.tar.gz`, first checks the SHA-256 listed in the release's `checksums.txt`, `277e8fa32b9e4f4feb8e30717892139251821c0c66cd3ad5f326ba759ec6ac4f`, then the SHA-256 of the `auth0` binary inside it, `2bc2ef7c1ab3c340a50bebb2c6bfd9692ea23dfee539bf7a2e1aac3debb3491c` (computed 2026-10-26 from the downloaded archive; the archive's SHA-256 equals the one in checksums.txt), and installs only when both match. At run time `deploy.sh` reads the `auth0` on PATH once, checks its SHA-256, and runs only a copy of those same bytes written into a temporary directory (0700) that only it can write to, deleted at the end; if the file on PATH is replaced after the check, the replacement is never run (review round 1 L2). `--apply` computes the SHA-256 of the `auth0` on PATH before running the CLI (not even `--version` runs first), and stops at the preflight if it is not the pinned one. `scripts/check_deploy.py` checks that the pins in `deploy.sh` and `railway_deploy.py` agree. Only Linux x86_64 is supported.

**The CLI's environment.** Every call adds `--agent-mode --no-input --no-color` (CLI 1.37.0's agent mode: the result is JSON on stdout; on failure stderr has only a JSON error envelope; no prompts, no browser). The environment has only `PATH`, locale, proxy and CA settings, `HOME=<state>/geminus-deploy/auth0-cli` (`$XDG_STATE_HOME` or `~/.local/state`, mode 0700), `AUTH0_CLI_ANALYTICS=false` (otherwise the CLI sends the tenant name and more to Heap analytics) and `DBUS_SESSION_BUS_ADDRESS=disabled:` (the desktop keyring is not touched: without a keyring the CLI writes the access token to `HOME/.config/auth0/config.json`, and this home is deleted at the end); no Railway token. A home left behind by a previous run killed with SIGKILL is first logged out and then deleted by the next run.

**Every action** (CLI 1.37.0 commands; check first, create only when missing, update only when it differs from the table below, read back and compare after writing, and stop with exit 1 when Auth0 did not keep the value; written JSON always goes through stdin with `--data @-`):

| Object | Command | Settings |
|---|---|---|
| Login | `auth0 login --domain <tenant>` | Device code: shows only the `verification_uri` and `user_code` the CLI prints; the logged-in tenant must be the domain of `GEMINUS_AUTH0_ISSUER`, otherwise nothing is changed and it stops |
| The owner's user | `auth0 users show <GEMINUS_SEED_OWNER_SUBJECT>` | Must be in `GEMINUS_AUTH0_CONNECTION` (the application enables only this connection, otherwise the owner cannot sign in); checked before any write |
| API | `auth0 apis list --number 1000`, `apis create`/`apis update <id>`, `apis show <id>` | identifier `GEMINUS_AUTH0_AUDIENCE`, name `Geminus demo API`, `signing_alg` RS256, `token_dialect` `rfc9068_profile`, `token_lifetime` 600, `allow_offline_access` true, `skip_consent_for_verifiable_first_party_clients` true |
| Regular Web Application | `auth0 apps list --number 1000` (found by name; there must be at most one), `apps create`/`apps update <id>`, `apps show <id>` | name `Geminus demo web`, `app_type` `regular_web`, `oidc_conformant` true, `token_endpoint_auth_method` `client_secret_post`, `grant_types` only `authorization_code`, `refresh_token`, `callbacks` only `https://<web domain>/auth/callback`, `allowed_logout_urls` only `https://<web domain>/` (the BFF's `apps/web/lib/session/oidc.ts`), `web_origins` and `allowed_origins` empty, `jwt_configuration.alg` RS256, `refresh_token`: `rotation_type` rotating, `expiration_type` expiring, `leeway` 30, `token_lifetime` 604800, `infinite_token_lifetime` false, `idle_token_lifetime` 43200, `infinite_idle_token_lifetime` false |
| Sign-up | `auth0 connections list`, `connections show <id>`, `connections update <id>` | `options.disable_signup` true. A PATCH replaces the whole `options`, and without `display_name` the display name is changed to the name (Management API docs), so it sends all the existing options plus `disable_signup` and the existing `display_name`. When the options hold values the script does not preserve (a custom database's scripts or configuration), it changes nothing and stops; use the manual fallback instead. Settings objects such as `authentication_methods.password` (`{"enabled": true}`, not a password) are kept as usual (review round 1 M1) |
| Only the database connection enabled | `auth0 connections enabled-clients show <id>`, `… update <id>` (JSON through stdin, **without** `--data`: in CLI 1.37.0 this command takes `--data`'s value as the JSON itself and does not understand `@-`) | The application is enabled only on `GEMINUS_AUTH0_CONNECTION`; other connections (Google and the like, the defaults of a new tenant) disable this application, otherwise anyone could create a user in Auth0 with a social login (H-035 (2): turn off self sign-up) |
| Judge account | `auth0 users search-by-email <GEMINUS_JUDGE_EMAIL>`, `users create` (or `users update <id>`) | In that connection; `email_verified` true, `verify_email` false, name `Geminus demo judge`; the password is generated with Python `secrets` (24 characters, with upper and lower case, digits and symbols, no three identical characters in a row), through stdin; when the user exists, the password is not changed; with `GEMINUS_JUDGE_RESET_PASSWORD=1` a new password is set |
| Client secret | `auth0 apps show <id> --reveal-secrets` → `railway variable set BFF_OIDC_CLIENT_SECRET --stdin --service web --skip-deploys` → `railway environment edit … variables.BFF_OIDC_CLIENT_SECRET.isSealed true` | Read only when this run created the application, when the web has no such variable, or when the web's `BFF_OIDC_CLIENT_ID` is not this application; unaffected by `GEMINUS_RAILWAY_SEAL=off` (no other variable references it); when Railway refuses to seal, this run fails at the verification (not done), and on a re-run a secret the web already has but that is not sealed is not read again but sealed directly (review round 1 L1) |
| Logout | `auth0 logout <tenant>` (each one `auth0 tenants list` lists), delete the CLI's home | Runs on success, failure, SIGTERM and SIGHUP. Without a keyring the CLI fails to delete the keyring after removing the tenant and exits 1 (CLI source), so `tenants list` decides whether a session remains; when one remains, it ends with exit 1 (the home is still deleted). When stdout cannot be written (pipe closed, terminal hangup), it stops at the next message; logout, deleting the home and removing the TCP proxy still run, and it ends with exit 74 (129 for SIGHUP; review round 1 M2) |

**Secrets.** The client secret, the judge password and the Railway token appear in no program's arguments, output or files, nor in the deploy script's state, a method's return value or an exception (checked by the rehearsal). At the moment the CLI's JSON is parsed, fields such as `client_secret`, `signing_secret`, `signing_keys`, `device_code`, `password` are replaced by markers (the same for other applications' secrets and the API's signing secret). The client secret that is needed goes only into that one call's own list, which is cleared once handed to stdin. Values to mask are kept only as HMAC digests, with a key generated fresh on every run. When a CLI call that carries a secret fails, only the category of the error envelope is shown, not the CLI's message. The judge password is written to **stderr** once, only at the end of the run (whether it succeeded, failed or was interrupted by a signal): the stdout of `deploy.sh --apply > log` does not have it; do not redirect stderr to a file (`2>&1 | tee` would write it into a file). If it is lost, re-run with `GEMINUS_JUDGE_RESET_PASSWORD=1` (shows a new one once), or change it in the dashboard. The CLI's access token remains valid on Auth0's side after logout until it expires (Auth0 decides the lifetime, not verified; logout only deletes the local copy); the local copy is deleted with the home.

**The judge account's membership.** The role is `editor`: in openapi's `x-required-roles`, submitting an AI proposal (`createProposal`, `createImageProposal`), approving (`approveProposal`), creating models and running simulations all need owner or editor; viewer can do none of them; owner can also manage members and read the audit log, which the demo does not need. The seed adds it with `provision-tenant organization … -member-issuer <issuer> -member-subject <the judge's user_id> -member-role editor` in the same transaction that creates the demo organization (the audit `organization.provision` carries `member_user_id`, `member_role`); afterwards it lists the judge account's memberships as the administrator: it must be only an editor of the demo organization. Provisioning can add a member only to the organization it creates in the same transaction (migration 0004), so **when the demo organization was seeded before the judge account existed** (for example after an earlier manual deploy), the script can only create the judge account as a user (`provision-tenant user`); the verification fails and writes its `usr_…`, and the demo organization's owner adds it with `POST /v1/organizations/<organization>/memberships` (`{"user_id": "usr_…", "role": "editor"}`, ADR-023; there is no UI yet). The judge's email defaults to `judge@geminus-demo.invalid` (not a real mailbox: the password cannot be reset by email).

**Manual fallback** (when the CLI cannot be obtained, the login fails, or any step above stops and cannot be fixed): follow [Auth0 runbook](auth0.md) §6 to make the same settings in the dashboard, then run `--apply` with `GEMINUS_AUTH0_SETUP=manual`, `GEMINUS_AUTH0_CLIENT_ID`, `GEMINUS_JUDGE_SUBJECT`; the owner fills in and seals `BFF_OIDC_CLIENT_SECRET` in Railway.

**Rehearsal** (`scripts/rehearse_railway_deploy.py`, a stand-in for Auth0 CLI 1.37.0; the JSON shapes of agent mode follow the CLI source): a new tenant and project (settings exactly as in the table above, the client secret only through stdin and sealed, the judge account only an editor of the demo organization, the password on stderr exactly once); a re-run (the stand-in lists the grant types in another order; Auth0 gets no write, the client secret is not read again, the password is not changed); tenant drift (an extra callback, grant, overlap 0, web origin, the API's dialect and lifetime, sign-up turned on again, the application on Google: all put back); a password reset and a home left by an interrupted run; a checksum mismatch (the CLI did not run even once); a failed step; a login to another tenant; Auth0 not keeping sign-up off; the CLI quoting the password in an error; a logout that did not remove the session; the owner not in the database connection; the demo organization seeded earlier; the judge account in another organization; the manual `GEMINUS_JUDGE_SUBJECT`; a dry run; SIGTERM before and after the judge account is created (separate processes, exit 143, logged out, the password on stderr exactly once); `--install-auth0-cli` getting an archive that is not the pinned one. The stand-ins prove that the script is consistent with itself, not that Auth0 accepts every call (NOT RUN until the deploy session).

## 8. Pre-launch checks (staging, run by a person; NOT RUN by agents)

1. `migrate` is Completed, with JSON `"pending":[]`; deploy `migrate` once more: `"applied":[]`.
2. `https://<api domain>/healthz` and `/readyz` are 200; `/v1/me` without a token is 401; api's log has `serving` and no `refusing the database login`.
3. The worker's log has `running` and an organization count of 1; no `invalid configuration`.
4. `TestLiveBucket` of §7.4 passes.
5. All checks of [Auth0 runbook](auth0.md) §5 (cookie attributes, no `Authorization` header reaching the browser, logout, 403 for a user that was not provisioned, the classification of refresh failures).
6. Walk through the demo once with synthetic data: create a model, upload a synthetic file (confirm that `tenant/<org>/imports/…` appears in the bucket), run a simulation.
7. No service on Railway has a variable from its `forbidden` list in its Variables; sealed values cannot be seen in the UI.
8. The owner decides before the URL is made public (AGENTS.md §6); before turning on live AI, also follow §4.4 and T-055.

## 9. Local staging smoke (`make staging-smoke`)

`make build-containers` builds `geminus-{api,worker,web}:local`; `make staging-smoke` runs `deploy/local/staging-smoke.sh`. With `deploy/local/compose.yaml` (**local only**, with the repository's test OIDC provider, never deployed) it starts PostgreSQL, migrate (twice), seed, api, worker and web, and checks: nothing other than the migration login can migrate; the API refuses the migration login; `/healthz`; `/readyz`; 401 without a token; the worker runs; the BFF's `/`; the login redirect (PKCE); 401 when not signed in; the images are non-root and have no credential variables; exit 0 on SIGTERM. At the end it removes everything. Each run generates one-time passwords and a session key. Without a Docker daemon it exits 2 (NOT RUN). The real Auth0 login, the bucket and the public URL can only be checked by a person on staging (§8). The CI `containers` job runs both targets.

## 10. Not covered and awaiting the owner's answer

- Not covered: container scan, SBOM, image signing; measuring the security headers after deploying (T-057's headers were verified only with a local `next start` and in e2e); a backup and restore drill (`make db-restore-test` is still NOT IMPLEMENTED); retention of and access to logs/telemetry; alerting (T-036); cleanup of orphan objects (D-044); an operator interface for spend reservations (T-053 I4 (2)); whether the live eval's spend record should go through the DB ledger instead (T-053 I4 (4), the owner's choice); the general approach of T-054 I1, and I2 (the S3 adapter no longer wraps its own timeout as a context error, `TestS3OwnTimeoutIsNoContextError`; the 503 marker for an object storage timeout is still not distinguished).
- Questions: (1) whether to accept ADR-029; (2) web → api over the public domain or the private network (the latter needs relaxing the BFF's http rule); (3) the bucket's region and data retention; (4) whether the API's key-free AI availability is done together in T-055 or as a separate task; (5) whether Railway expands a reference to a sealed variable's value (§7.7; the first `--apply` will tell); (6) whether to accept not using `${{secret()}}` for service variables and having the script generate secrets instead (H-032's preference); (7) PostgreSQL's major version (Railway's docs do not say; migrations are tested only on 16); (8) when the owner's variables are not sealed, the CLI's variable list hands their values to the script (discarded on receipt, §7.3): accept this, or the owner always adds them sealed (H-033's "only look at variable names, never read values"); (9) whether to accept that the Auth0 step (§7.8) disables the application on every connection other than the database connection (H-035 (2)'s "turn off self sign-up" also blocks social logins); (10) whether the judge account's email is `judge@geminus-demo.invalid` or a mailbox the owner names; (11) whether to accept that provision-tenant adds a member only when it creates the organization (migration 0004 is not relaxed), and that the owner adds the judge account through the API when the demo organization was seeded first.
