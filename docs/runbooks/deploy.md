# Runbook：部署（containers、migration、Railway）

依據：T-039（hackathon 切片，H-029 (3)）、[ADR-029](../adr/ADR-029-containers-and-staging-deployment.md)（PROPOSED）、[ADR-018](../adr/ADR-018-browser-session-and-oidc-provider.md)、[ADR-017](../adr/ADR-017-oidc-token-verification.md)、[ADR-024](../adr/ADR-024-job-queue-and-worker.md)、[ADR-028](../adr/ADR-028-opentelemetry.md)、[Auth0 runbook](auth0.md)、AGENTS.md §2、§6。雲端廠商：owner 的選擇為 **Railway**（H-030 先為「railway + cloudflare」，H-031 改為「純用 railway」，不接 Cloudflare）。

**誰執行。** Railway 的部署由 coordinator 在有 Railway token 的 session 中以 `deploy/railway/deploy.sh --apply` 執行（H-032，§7.7），或由 owner 照 §7.1–§7.6 手動執行。撰寫本 runbook 與腳本的 agent 沒有執行 `--apply`、沒有呼叫 Railway 的 API、沒有建立任何東西（只執行了不連網的 `--dry-run` 與以替身進行的 rehearsal）。Repository 與 image 中沒有任何 credential；腳本產生的 secret 只經 stdin 交給 Railway CLI，不印出、不寫檔；owner 的 secret（§7.3：AI key、live 開關；`GEMINUS_AUTH0_SETUP=manual` 時還有 Auth0 client secret）只由 owner 在 Railway 的 UI 填入（並 sealed），腳本不設；Railway CLI 列出變數時會回傳沒有 sealed 的值，腳本收到即丟棄（§7.3）。H-034 之後 Auth0 由 `deploy.sh` 的 Auth0 步驟以 Auth0 CLI 設定（owner 在自己的瀏覽器核准 CLI 的登入；client secret 只經 stdin 交給 Railway CLI 並 sealed；結束時登出），並建立評審帳號（H-035 (2)，§7.8）。只放 synthetic 資料。Production 的批准（把網址公開、開啟 live AI）是人的決定（AGENTS.md §6）。

§1–§6 與廠商無關；§7 是 Railway 的步驟（§7.7 是自動化的 `deploy.sh`，§7.8 是它的 Auth0 步驟與評審帳號）；§8 是上線前由人執行的檢查；§9 是本機的 `make staging-smoke`。

## 1. 組成

| 單位 | Image／來源 | Database login | 對外 | 健康檢查 | 停止 |
|---|---|---|---|---|---|
| migrate（每次部署前一次） | `Dockerfile.api`，命令 `geminus-migrate -dir /migrations up` | `geminus_migrator`（twin_owner） | 無 | 結束碼（0：完成或沒有待套用） | 執行完即結束 |
| api | `Dockerfile.api`（`geminus-api`） | `geminus_api`（twin_api） | https（BFF 呼叫它） | `GET /readyz`（`geminus-api -probe`） | SIGTERM：完成進行中的 request（20 秒） |
| worker | `Dockerfile.worker`（`geminus-worker`） | `geminus_worker`（twin_worker） | 無（沒有 HTTP listener） | process 存活 | SIGTERM：停止 handler、記錄 attempt（`WORKER_STOPPED`，會重試） |
| web | `apps/web/Dockerfile`（Next standalone、BFF） | 無 | https（使用者的網址） | `GET /` | SIGTERM：Next 立即結束（不 drain） |
| PostgreSQL | managed（16；migration 只在 16 上測過） | administrator 只用於 §3.1 | 不對外（只在 bootstrap 時暫時開） | 平台 | — |
| Object storage | S3 相容的 private bucket | — | 只有 API 與 worker 以 credential 存取 | — | — |
| seed（operator 的命令） | `geminus-provision-tenant`（API image 內，或 `go run ./cmd/provision-tenant`） | `geminus_provisioner`（D-042） | 無 | — | — |

OTLP backend：不設定（ADR-028：沒有 endpoint 時什麼都不送）。

## 2. Images

從 repository root 建置（`make build-containers`；CI 的 `containers` job 也一樣）：

```sh
docker build -f Dockerfile.api -t geminus-api:<tag> .
docker build -f Dockerfile.worker -t geminus-worker:<tag> .
docker build -f apps/web/Dockerfile -t geminus-web:<tag> .
```

- Base image 以 digest pin（[ADR-011](../adr/ADR-011-toolchain-bootstrap.md) 的表）；Go 的 builder 是 `go.mod` 的 1.27.3，runtime 是 distroless static（uid 65532、無 shell、無 package manager）；web 是 `.nvmrc` 的 Node 24.21.0（uid 1000 的 `node`），runtime 沒有 npm。
- 沒有 `ARG`、沒有 credential 的 `ENV`，`.dockerignore` 排除 `.env*`、`.git`、node_modules 與 build output：secret 只在執行時由平台的環境變數給。`scripts/check_deploy.py`（`make spec-check` 的一部分）檢查這些規則。
- Container scan、SBOM 與 image 簽章：**NOT RUN**（尚未選定工具，SECURITY §9）。

## 3. 資料庫：roles、logins、migration 的順序

### 3.1 Bootstrap（每個 database 一次，administrator）

```sh
psql "$ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 -f db/bootstrap_roles.sql
GEMINUS_MIGRATOR_DB_PASSWORD=… GEMINUS_API_DB_PASSWORD=… \
GEMINUS_WORKER_DB_PASSWORD=… GEMINUS_PROVISIONER_DB_PASSWORD=… \
  psql "$ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 -f deploy/sql/create_logins.sql
```

- `db/bootstrap_roles.sql` 建立 NOLOGIN 的 twin_owner、twin_api、twin_worker、twin_provisioner（D-008、D-013、D-042）。
- `deploy/sql/create_logins.sql`（psql 15 以上）建立四個 login，密碼只從環境變數讀（`\getenv`），每個至少 32 字元且四個不同：用 `openssl rand -hex 32` 產生（hex 不需要在 URL 中跳脫），存在 owner 的密碼管理器，並填入平台的 secret（§7.3）。Railway 上由 `deploy.sh` 產生並直接存為 sealed variable（§7.7），不經任何人的手。已存在的 login 不變；輪替以 `ALTER ROLE … PASSWORD`。
- 結果（腳本最後印出）：`geminus_migrator` 只屬於 twin_owner；`geminus_api` 只屬於 twin_api；`geminus_worker` 只屬於 twin_worker；`geminus_provisioner` NOINHERIT、只屬於 twin_provisioner；都沒有 SUPERUSER、BYPASSRLS、CREATEROLE、CREATEDB、REPLICATION；twin_owner 有該 database 的 CREATE。
- Server 的 log 可能記下失敗的 statement（含密碼），所以腳本在任何 CREATE ROLE 之前先檢查所有條件；不要開 `log_statement=all`。

### 3.2 Migration（每次部署，在 api 與 worker 之前）

`geminus-migrate -dir /migrations up`，只讀 `GEMINUS_MIGRATE_DATABASE_URL`（`geminus_migrator`）：

- 依序套用尚未套用的 `migrations/NNNN_*.up.sql`，每個與紀錄（`geminus_migrations.applied`：version、檔名、SHA-256、時間、login）同一個 transaction；沒有待套用時什麼都不做（exit 0）。stdout 是一個 JSON：`{"applied":[…],"current":N,"pending":[]}`。
- 拒絕（exit 4）superuser、runtime login（api、worker）、provisioning login 與不能以 twin_owner 行事的 login；runtime login 永遠不 migrate，migration login 只給這一步。
- 資料庫的紀錄與 image 的檔案不符（已套用的 migration 被改過、資料庫比 image 新、有 twin schema 卻沒有紀錄）時 exit 5、什麼都不改：已部署的 migration 不得改寫（AGENTS.md §3），由人決定。
- `geminus-migrate -dir /migrations status`：同樣的 JSON；有待套用時 exit 6。
- 並行的兩次執行輪流進行（advisory lock）。API 與 worker 不檢查 schema 的版本：部署順序由人保證（migrate 完成後才部署 api 與 worker）。

### 3.3 Seed（operator，一次）

使用者與 organization 只由 operator 建立（D-008、D-042），不經 API：

```sh
export GEMINUS_PROVISION_DATABASE_URL='postgresql://geminus_provisioner:<password>@<host>:<port>/<db>?sslmode=require'
go run ./cmd/provision-tenant organization -name 'Demo line (synthetic)' \
  -owner-issuer 'https://<tenant>.<region>.auth0.com/' -owner-subject 'auth0|<owner 的 Auth0 user_id>'
```

印出 `{"organization_id":"org_…","owner_user_id":"usr_…"}`：`organization_id` 填入 worker 的 `WORKER_ORGANIZATION_IDS`。Issuer 必須與 Auth0 access token 的 `iss` 完全相同（含結尾 `/`）；未 provision 的 identity 登入後 API 回 403。其他使用者：`provision-tenant user …` 後由 owner 以 membership API 加入。

## 4. 環境變數（全部）

來源欄是讀取它的程式。**Secret** 欄為「是」者只放平台的 sealed／secret 設定，不進 repository、image、log、報告或對話。

### 4.1 API 與 worker 共用（`internal/config/config.go`）

| 變數 | 服務 | 必填 | Secret | 說明 |
|---|---|---|---|---|
| `DATABASE_URL` | api、worker | 是 | 是（含密碼） | 各自的 runtime login（`geminus_api`／`geminus_worker`）；啟動時 `tenant.CheckRuntimeRole` 拒絕 superuser、BYPASSRLS、owner、其他 role 的成員（exit 3） |
| `APP_ENV` | api、worker | 部署時設 `production` | 否 | `development`、`test`、`production` 或不設；`production` 拒絕 fake provider 與 filesystem adapter |
| `OBJECT_STORAGE_PROVIDER` | api、worker | 部署時設 `s3` | 否 | `disabled`（預設：upload、需要 dataset 或 trace 的 run 回 503）、`filesystem`（只給 development／test）、`s3` |
| `OBJECT_STORAGE_ROOT` | api、worker | 只有 filesystem | 否 | 部署時不設（s3 時設了即拒絕啟動） |
| `OBJECT_STORAGE_S3_ENDPOINT` | api、worker | s3 時 | 否 | https origin（plain http 只在 loopback） |
| `OBJECT_STORAGE_S3_BUCKET` | api、worker | s3 時 | 否 | bucket 的 S3 名稱（DNS label） |
| `OBJECT_STORAGE_S3_REGION` | api、worker | s3 時 | 否 | Railway 的 bucket 為 `auto` |
| `OBJECT_STORAGE_S3_ACCESS_KEY_ID` | api、worker | s3 時 | 是 | bucket 的 access key ID |
| `OBJECT_STORAGE_S3_SECRET_ACCESS_KEY` | api、worker | s3 時 | 是 | bucket 的 secret（印出為 `[redacted]`） |
| `OBJECT_STORAGE_S3_URL_STYLE` | api、worker | 否 | 否 | `virtual-hosted`（預設）或 `path`；provider 不是 s3 時任何 `OBJECT_STORAGE_S3_*` 都拒絕啟動 |

### 4.2 API（`internal/config/auth.go`、`server.go`）

| 變數 | 必填 | Secret | 說明 |
|---|---|---|---|
| `AUTH_OIDC_ISSUER` | 是 | 否 | `https://<tenant>.<region>.auth0.com/`（與 token 的 `iss` 完全相同） |
| `AUTH_OIDC_AUDIENCE` | 是 | 否 | Auth0 API 的 Identifier（§6.1；不可等於任何 client ID） |
| `AUTH_OIDC_JWKS_URL` | 是 | 否 | `<issuer>.well-known/jwks.json` |
| `AUTH_OIDC_ALGORITHMS` | 否 | 否 | 預設 RS256、ES256 |
| `AUTH_OIDC_REQUIRE_AT_JWT` | 否（部署設 `true`） | 否 | 只接受 `typ: at+jwt`（Auth0 API 選 RFC 9068 profile） |
| `API_LISTEN_ADDR` | 否 | 否 | 預設 `:8080`；`geminus-api -probe` 用它的 port |

### 4.3 Worker（`internal/config/worker.go`）

| 變數 | 必填 | Secret | 說明 |
|---|---|---|---|
| `WORKER_ID` | 是 | 否 | lease 與 audit 中的名稱（字母開頭，至多 128）；每個 worker 不同，所以只開一個 replica |
| `WORKER_ORGANIZATION_IDS` | 是 | 否 | worker 服務的 organization（逗號分隔，D-008）：§3.3 的 `organization_id` |

### 4.4 AI（`internal/config/ai.go`、`apiai.go`；live 呼叫只在 worker，API 不持有 key）

| 變數 | 服務 | 必填 | Secret | 說明 |
|---|---|---|---|---|
| `AI_PROVIDER` | api、worker（**兩者相同**） | 否 | 否 | `disabled`（預設）、`fake`（只給 development／test，production 拒絕）、`anthropic`、`meta`（T-055：Meta Model API 的 Muse Spark，批准 H-029） |
| `AI_POLICY_APPROVAL` | api、worker（**兩者相同**） | live 時 | 否 | `internal/config/aiapproval` 登記、且屬於 `AI_PROVIDER` 的批准（`anthropic`：H-018；`meta`：H-029），否則 `provider_mismatch` |
| `AI_MODEL` | api、worker（**兩者相同**） | live 時 | 否 | 必須等於批准的 model（H-029：`muse-spark-1.3`；H-018：`claude-opus-5-5`），且該 model 有價格（`internal/ai/spend`），沒有預設 |
| `GEMINUS_AI_LIVE` | api、worker（**兩者相同**） | 否 | 否 | owner 的開關：`on`／`off`／不設（預設關）。API：on 且上面三個設定都相符時接受 proposal，不讀 key；worker：另需 key 才呼叫 provider |
| `GEMINUS_ANTHROPIC_API_KEY_A`、`GEMINUS_ANTHROPIC_API_KEY_B` | **只 worker** | Anthropic live 時 | 是 | D-018 的兩把 key；hackathon 不用 |
| `GEMINUS_MUSE_API_KEY` | **只 worker** | Meta live 時 | 是 | Meta Model API 的 key（一把，label `meta`；格式 `LLM_<數字 ID>_<secret>`，格式不符拒絕啟動）；`MODEL_API_KEY` 不讀 |
| `AI_VISION_PROVIDER` | api、worker（**兩者相同**） | 否 | 否 | 照片路徑（T-055 給 T-056）：不設＝跟 `AI_PROVIDER`；`disabled` 關閉；`anthropic`／`meta` 為另一個 live provider 時需下面兩個變數、自己的 key 與同一個開關；批准沒列圖片（H-018）時照片路徑維持關閉（`images_not_approved`），proposal 不受影響。API 以同一段判斷決定是否接受照片（D-124），不讀 key：照片路徑關閉時 `createImageProposal` 在讀 body 前回 503（`AI_PROVIDER_NOT_CONFIGURED` 或 `AI_POLICY_NOT_APPROVED`，`details[0].code` 為 `AI_IMAGE_UNSUPPORTED`） |
| `AI_VISION_POLICY_APPROVAL`、`AI_VISION_MODEL` | api、worker（**兩者相同**） | `AI_VISION_PROVIDER` 是另一個 live provider 時 | 否 | 該 provider 的批准與 model（同 `AI_POLICY_APPROVAL`／`AI_MODEL` 的規則）；其他情況必須不設（API 與 worker 都拒絕啟動） |
| `AI_MONTHLY_LIMIT_USD`、`AI_DAILY_LIMIT_USD`、`AI_ORGANIZATION_DAILY_LIMIT_USD` | 每個 worker 相同 | 否 | 否 | 花費上限（D-110）；限額是每個 process 的設定，不同的 worker 必須相同（T-053 I2） |
| `AI_MAX_PROMPT_BYTES`、`AI_MAX_OUTPUT_TOKENS`、`AI_TIMEOUT_SECONDS`、`AI_ATTEMPT_TIMEOUT_SECONDS`、`AI_MAX_RETRIES`、`AI_MAX_CONCURRENT_PER_ORGANIZATION` | worker | 否 | 否 | 覆蓋 provider 的預設 budget；有圖片的 request（照片 judge）的 `max_tokens` 固定為 32,000（`provider.ImageMaxOutputTokens`），不受 `AI_MAX_OUTPUT_TOKENS` 影響，兩個 timeout 依同一比例（32,000 ÷ `AI_MAX_OUTPUT_TOKENS`）放大、至多 300 s；花費的上限仍是 D-110 的日上限 |
| `AI_API_KEY` | — | — | — | 一律拒絕（D-018） |
| `GEMINUS_META_API_KEY`（Meta key 的舊名稱）、`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`MODEL_API_KEY` | — | — | — | 從不讀取（D-018）；**API 的環境中有其中任何一個，或 `AI_API_KEY`、上面任何一把 key，API 即拒絕啟動**（`key_forbidden`，錯誤只寫變數名稱；T-053 I5） |

**API 不持有任何 AI key（T-053 I5；T-055 的 follow-up，coordinator 的決定）。** API 只以四個非 secret 的設定決定是否接受 AI proposal：`AI_PROVIDER`、`AI_POLICY_APPROVAL`、`AI_MODEL`、`GEMINUS_AI_LIVE`（`config.LoadAPIAI`）。檢查與 worker 的 live gate 相同（同一段程式 `liveGate`：build 記錄的批准、屬於 `AI_PROVIDER`、record 不缺任何項目；`AI_MODEL` 等於 record 的 model 且有價格；開關 on），只是不讀、也不要求任何 key。四個都相符時 API 接受 proposal（worker 以自己的 key 呼叫 provider），啟動 log 為 `AI proposals are accepted; the worker calls the provider`（`provider`、`model`、`code` `LIVE_AI_ON`）。任何不合：開關不是 on 時 API 照常啟動、對新的 proposal 回 503（`AI_POLICY_NOT_APPROVED`；`disabled` 為 `AI_PROVIDER_NOT_CONFIGURED`），啟動 log `AI proposals are refused` 記 typed reason（`code`，例如 `SWITCH_OFF`、`MODEL_MISMATCH`）；開關 on 時 API 拒絕啟動（exit 1，錯誤碼即該條件），與 worker 相同。API 的環境中有任何 AI key 的變數（上表的 key 與從不讀取的名稱）即拒絕啟動（`key_forbidden`），所以 key 只設在 worker。API 與 worker 的四個設定必須相同：`scripts/check_deploy.py` 檢查 `variables.json` 中兩者的宣告相同、API 沒有其他 AI 設定、API 的 `forbidden` 包含 API 拒絕的每個 key 變數（清單取自 `internal/config/apiai.go`）；值由 owner 在 Railway 的 UI 設定，靜態檢查看不到。API 接受、但 worker 不能呼叫 provider 時：worker 的開關 on 而沒有 key，worker 拒絕啟動（不建 handler、不連線），proposal 停在 generating，直到有 worker 啟動；worker 的開關不是 on，每個 proposal 失敗為 `AI_POLICY_NOT_APPROVED`；key 被 provider 拒絕，失敗為 `AI_CREDENTIALS_EXHAUSTED`（HTTP 層 `SERVICE_UNAVAILABLE`）。失敗的 proposal 不重試，除了它的 error、job 與 audit 之外不寫入任何東西（`internal/proposals/generator` 的 `TestAProposalTheWorkerCannotAskFailsSafely`）。Live 開關預設關，只有 owner 開，且 API 與 worker 一起開。

**API 端的照片可用性（T-056 的 follow-up，D-124 PROPOSED，coordinator 的決定）。** API 也讀照片路徑的三個非 secret 設定 `AI_VISION_PROVIDER`、`AI_VISION_POLICY_APPROVAL`、`AI_VISION_MODEL`，以 worker 的同一段判斷（`internal/config` 的 `decide`：worker 以讀 key 的 `loadLive`、API 以不讀 key 的 `keyFree` 呼叫）決定是否接受照片：fake（只在 development／test）、或 live provider 的批准列有圖片（H-029）、`AI_VISION_MODEL`（或跟隨時的 `AI_MODEL`）相符且開關 on 時接受；否則 `createImageProposal` 在讀 body 前回 503（照片路徑為 `disabled` 時 `AI_PROVIDER_NOT_CONFIGURED`，live provider 未開時 `AI_POLICY_NOT_APPROVED`，兩者的 `details[0].code` 都是 worker 會用的 `AI_IMAGE_UNSUPPORTED`），什麼都不存。文字 proposal 不受影響（例如 Claude 為主、沒有照片路徑時，文字 proposal 照常接受，照片 503）。啟動 log：照片路徑為 live provider 時 `photo proposals are accepted; the worker calls the provider`（`code` `PHOTO_PATH_ON`）或 `photo proposals are refused`（`code` 為 typed reason，例如 `IMAGES_NOT_APPROVED`）。照片路徑的設定不合而開關 on 時 API 拒絕啟動（與 worker 相同的錯誤碼）；三個設定在 `variables.json` 的 api 與 worker 宣告相同（`scripts/check_deploy.py`），值由 owner 設相同的值。

### 4.5 Telemetry（`internal/config/telemetry.go`，api、worker）

`OTEL_SDK_DISABLED`、`OTEL_TRACES_EXPORTER`、`OTEL_METRICS_EXPORTER`、`OTEL_EXPORTER_OTLP_PROTOCOL`、`OTEL_EXPORTER_OTLP_ENDPOINT`、`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`、`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`、`OTEL_EXPORTER_OTLP_HEADERS`（secret：collector 的 credential）、`OTEL_EXPORTER_OTLP_TRACES_HEADERS`、`OTEL_EXPORTER_OTLP_METRICS_HEADERS`（secret），以及 SDK 自己讀的 `OTEL_RESOURCE_ATTRIBUTES`、`OTEL_TRACES_SAMPLER`。全部選填；hackathon 部署**都不設**（沒有 endpoint 就不送出任何 telemetry，ADR-028）。Log 是 stderr 的 JSON lines（平台的 log 介面可見；不含 prompt、回答、key）。

### 4.6 Web（BFF，`apps/web/lib/session/config.ts`、`seal.ts`；Next）

| 變數 | 必填 | Secret | 說明 |
|---|---|---|---|
| `BFF_APP_ORIGIN` | 是 | 否 | `https://<web 網域>`（CSRF 的 Origin 與 redirect URI） |
| `BFF_API_URL` | 是 | 否 | API 的 https base URL（§7.6） |
| `BFF_OIDC_ISSUER` | 是 | 否 | 與 `AUTH_OIDC_ISSUER` 相同 |
| `BFF_OIDC_AUTHORIZATION_URL` | 是 | 否 | `<issuer>authorize` |
| `BFF_OIDC_TOKEN_URL` | 是 | 否 | `<issuer>oauth/token` |
| `BFF_OIDC_REVOCATION_URL` | 是 | 否 | `<issuer>oauth/revoke` |
| `BFF_OIDC_END_SESSION_URL` | 是 | 否 | `<issuer>oidc/logout` |
| `BFF_OIDC_JWKS_URL` | 是 | 否 | `<issuer>.well-known/jwks.json` |
| `BFF_OIDC_CLIENT_ID` | 是 | 否 | Auth0 Application 的 Client ID |
| `BFF_OIDC_CLIENT_SECRET` | 是 | 是 | Auth0 Application 的 Client Secret |
| `BFF_OIDC_AUDIENCE` | 是 | 否 | 與 `AUTH_OIDC_AUDIENCE` 相同 |
| `BFF_OIDC_SCOPES` | 否 | 否 | 不設（預設 `openid offline_access`） |
| `SESSION_KEYS` | 是 | 是 | `<id>:<32 bytes base64url>`（[Auth0 runbook](auth0.md) §4 的產生與輪替） |
| `GEMINUS_UI_GALLERY` | 否 | 否 | 元件展示頁，只給瀏覽器檢查；部署**不設** |
| `PORT`、`HOSTNAME` | 否 | 否 | Next 的 listener（image 預設 3000、0.0.0.0） |
| `NODE_ENV`、`NEXT_TELEMETRY_DISABLED` | 否 | 否 | image 已設 `production`、`1` |

BFF 的 URL 必須全部是 https（只有全部在 loopback 時才可 http）；設定不完整時 `/auth/*` 與 `/api/v1/*` 回 503，log 只寫變數名稱。

### 4.7 Operator 的命令與 login 的密碼

| 變數 | 誰用 | Secret | 說明 |
|---|---|---|---|
| `GEMINUS_MIGRATE_DATABASE_URL` | migrate（`cmd/migrate`） | 是 | `geminus_migrator` 的 URL；只給 migrate，絕不給 api、worker、web |
| `GEMINUS_PROVISION_DATABASE_URL` | seed（`cmd/provision-tenant`） | 是 | `geminus_provisioner` 的 URL；只在 operator 的機器上 |
| `GEMINUS_MIGRATOR_DB_PASSWORD`、`GEMINUS_API_DB_PASSWORD`、`GEMINUS_WORKER_DB_PASSWORD`、`GEMINUS_PROVISIONER_DB_PASSWORD` | `deploy/sql/create_logins.sql`；Railway 以 reference 組成各 service 的 URL | 是 | §3.1 |
| `AI_EVAL_LIVE_MAX_USD` | `make ai-eval-live`（不是部署的變數） | 否 | live eval 的上限（T-053） |

## 5. HTTPS、cookie、CSP、資料

- **TLS**：平台的 edge 終結 TLS（Railway 自動簽發與更新憑證），container 內是 plain HTTP。瀏覽器只連 web 的 https 網址；BFF 以 https 連 API 與 Auth0；API 以 https 抓 Auth0 的 JWKS；API 與 worker 以 https 連 bucket。Database 連線在平台的 private network（WireGuard），Railway 的 PostgreSQL image 有 SSL；從 operator 的機器經 TCP proxy 連線時用 `sslmode=require`（加密，不驗證 server 憑證）。
- **Cookie**：`__Host-geminus_session`、`__Host-geminus_login` 永遠是 `HttpOnly; Secure; SameSite=Lax; Path=/`、無 Domain（與 `APP_ENV` 無關，`apps/web/lib/session/session.ts`），所以只在 https 下可用；瀏覽器 JS 拿不到 token（ADR-018）。
- **CSP 與 security header**（T-057，SECURITY §3）：web 的每個回應都有 Content-Security-Policy（每個 request 一個 nonce）、`X-Content-Type-Options`、`Referrer-Policy: no-referrer`、`Permissions-Policy`、`X-Frame-Options: DENY`，沒有 `X-Powered-By`；`Strict-Transport-Security: max-age=31536000` 只在 `BFF_APP_ORIGIN` 是 https 時送（Railway 上是 `https://${{RAILWAY_PUBLIC_DOMAIN}}`，所以會送；在 runtime 讀，不需要 rebuild）。部署後可用 `curl -sI https://<web 網域>/` 確認。Railway edge 是否另加 header 未查證。
- **資料**：只放 synthetic 資料與 demo 圖片；bucket 的加密、保存期限與 region 是供應者的預設，未經 owner 決定（ADR-029）。不放客戶資料、不放真實使用者以外的個資。

## 6. Auth0（owner 的 tenant，最終網域）

H-034 之後由 `deploy.sh` 的 Auth0 步驟（§7.8）以 Auth0 CLI 設定；手動時照 [Auth0 runbook](auth0.md) §2–§4（步驟見其 §6）建立 API 與 Regular Web Application，網域用 web service 的公開網域（§7.6，例如 `<web>.up.railway.app`）：

| 設定 | 值 |
|---|---|
| API Identifier（audience） | 例如 `https://api.geminus-demo.invalid`（不需要能連線；之後不能改）→ `AUTH_OIDC_AUDIENCE`、`BFF_OIDC_AUDIENCE` |
| Access token profile | RFC 9068（`typ: at+jwt`）→ `AUTH_OIDC_REQUIRE_AT_JWT=true` |
| Allowed Callback URLs | `https://<web 網域>/auth/callback`（只有這一個） |
| Allowed Logout URLs | `https://<web 網域>/` |
| Allowed Web Origins、Allowed Origins (CORS) | 空白 |
| Grant types | 只有 Authorization Code 與 Refresh Token |
| Sign-up | 關閉（使用者只由 §3.3 provision） |

換網域（例如之後加自己的網域）時同時改 callback／logout URL、`BFF_APP_ORIGIN`，並重新部署 web。

## 7. Railway（owner 的選擇，H-031：純 Railway）

以下引用 Railway 官方文件（2026-10-26 取得）：[CLI](https://docs.railway.com/guides/cli)、[template variable functions](https://docs.railway.com/templates/create)、[variables reference](https://docs.railway.com/variables/reference)、[managing secrets](https://docs.railway.com/guides/managing-secrets-on-railway)、[config as code](https://docs.railway.com/reference/config-as-code)、[monorepo](https://docs.railway.com/guides/monorepo)、[Dockerfiles](https://docs.railway.com/guides/dockerfiles)、[start command](https://docs.railway.com/guides/start-command)、[healthchecks](https://docs.railway.com/guides/healthchecks)、[pre-deploy command](https://docs.railway.com/guides/pre-deploy-command)、[variables](https://docs.railway.com/guides/variables)、[private networking](https://docs.railway.com/guides/private-networking)、[public networking](https://docs.railway.com/guides/public-networking)、[PostgreSQL](https://docs.railway.com/guides/postgresql)、[Storage Buckets](https://docs.railway.com/storage-buckets)、[Storage Buckets guide](https://docs.railway.com/guides/storage-buckets)、[restart policy](https://docs.railway.com/deployments/restart-policy)。

### 7.1 Project 與 services

在一個 Railway project（建議另開一個 environment，例如 `staging`）中，從同一個 GitHub repository 建立四個 service，加上 PostgreSQL 與一個 Bucket：

| Service | Config-as-code 檔（service 的 Settings 中填絕對路徑） | 公開網域 |
|---|---|---|
| `migrate` | `/deploy/railway/migrate.railway.json` | 無 |
| `api` | `/deploy/railway/api.railway.json` | Generate Domain |
| `worker` | `/deploy/railway/worker.railway.json` | 無 |
| `web` | `/deploy/railway/web.railway.json` | Generate Domain |
| `Postgres` | Railway 的 PostgreSQL template | 無（只在 §7.5 暫時開 TCP proxy） |
| `geminus-objects` | Bucket（Storage Buckets） | 無（private） |

- Root directory 不設（build context 是 repository root，三個 Dockerfile 都這樣寫）。Railway 文件：config file 不跟隨 root directory，要填絕對路徑；code 中的設定一律覆蓋 dashboard 的設定。
- Config 檔設定 Dockerfile（`build.dockerfilePath`）、watch paths、healthcheck、restart policy、`drainingSeconds`（SIGTERM 到 SIGKILL 的時間：api、worker 30 秒）與 1 個 replica。`migrate` 的 start command 是 `/usr/local/bin/geminus-migrate -dir /migrations up`（Railway 文件：start command 以 exec form 取代 image 的 ENTRYPOINT），restart policy `NEVER`：結束碼 0 時 deployment 為 Completed。
- **不用 pre-deploy command 執行 migration**：Railway 文件說 pre-deploy 使用該 service 的環境變數，api 的 pre-deploy 會讓 API service 拿到 migration login。改為獨立的 `migrate` service，只有它有 `GEMINUS_MIGRATE_DATABASE_URL`。
- Service 名稱若不同，`deploy/railway/variables.json` 中的 reference（`${{Postgres.PGHOST}}`、`${{api.RAILWAY_PUBLIC_DOMAIN}}`、`${{geminus-objects.ENDPOINT}}`）要跟著改。

### 7.2 Healthcheck、port、停止

- Railway 的 healthcheck 只在部署開始時執行，從 `healthcheck.railway.app` 打 `PORT` 上的路徑，任何 2xx 即成功，預設等 300 秒（config 設 120）；上線後不持續監控。API 設 `PORT=8080` 與 `API_LISTEN_ADDR=:8080`、路徑 `/readyz`；web 設 `PORT=3000`、路徑 `/`；worker 與 migrate 沒有 healthcheck。
- Image 的 `HEALTHCHECK`（`geminus-api -probe`、web 的 `node -e fetch(...)`）給 Docker 與 compose 用；Railway 用 config 的 healthcheck。

### 7.3 Variables 與 secrets

`deploy/railway/variables.json` 是每個 service 的清單（`make spec-check` 檢查它與程式一致）：`value` 照寫（`{{…}}` 由 `deploy.sh` 以本次的輸入、bucket 的設定或 seed 的結果填入；`${{…}}` 是 Railway 的 reference，原樣保留）；`secret: true` 沒有值，`source` 說明來源。Secret 一律設為 **sealed**（Railway 文件：sealed 的值會提供給 build 與 deployment，但 UI 與 API 都看不到，也不會被 `railway variables`／`railway run` 帶出，且不能解除 sealed；CLI 5.64.2 的 `variable list --json` 把 sealed 的值列為 `null`）。Image 不宣告 `ARG`，所以變數不會進入 build。

**Secret 的清單（全部只在 Railway 的 sealed variable 中；手動部署時另存 owner 的密碼管理器）：**

| Secret | Service | 誰設（`source`） | 來源 |
|---|---|---|---|
| `GEMINUS_MIGRATOR_DB_PASSWORD` | migrate | `deploy.sh`（`generated`） | Python `secrets.token_hex(32)`，經 stdin |
| `GEMINUS_API_DB_PASSWORD` | api | `deploy.sh`（`generated`） | 同上 |
| `GEMINUS_WORKER_DB_PASSWORD` | worker | `deploy.sh`（`generated`） | 同上 |
| `GEMINUS_PROVISIONER_DB_PASSWORD` | 不放 Railway | `deploy.sh` 每次執行新產生，只在該次 seed 的環境中，不存任何地方 | §3.3 |
| `SESSION_KEYS` | web | `deploy.sh`（`generated`，`format: session-key`） | `k1:` 加 32 bytes 的 base64url（[Auth0 runbook](auth0.md) §4 的格式） |
| `OBJECT_STORAGE_S3_ACCESS_KEY_ID`、`OBJECT_STORAGE_S3_SECRET_ACCESS_KEY` | api、worker | `deploy.sh`（`bucket:…`） | `railway bucket credentials --json` 的值，經 stdin 複製（不印出） |
| `BFF_OIDC_CLIENT_SECRET` | web | `deploy.sh` 的 Auth0 步驟（`auth0`，H-034；必填）；`GEMINUS_AUTH0_SETUP=manual` 時為 **owner** | Auth0 Application 的 Client Secret：Auth0 CLI 的 JSON 在部署腳本的 process 內讀出，只經 stdin 交給 `railway variable set --stdin`，立刻 sealed（§7.8） |
| `GEMINUS_ANTHROPIC_API_KEY_A`、`GEMINUS_ANTHROPIC_API_KEY_B` | **只 worker** | **owner**（選填；只在開 live AI 時） | D-018 的兩把 key |
| `GEMINUS_MUSE_API_KEY` | **只 worker** | **owner**（選填；只在開 Meta live AI 時） | Meta Model API（Standard mode，H-029） |
| `GEMINUS_AI_LIVE`（不是 secret，但是 owner 的開關） | api、worker（**兩者相同**） | **owner**（選填，預設關） | §4.4 |
| `AI_POLICY_APPROVAL`、`AI_MODEL`（不是 secret，owner 的設定） | api、worker（**兩者相同**） | **owner**（選填；只在開 live AI 時） | §4.4（`AI_PROVIDER` 由 `deploy.sh` 只在沒有時設為 `disabled`，owner 改為 `anthropic` 或 `meta` 時兩個 service 一起改） |
| `AI_VISION_PROVIDER`、`AI_VISION_POLICY_APPROVAL`、`AI_VISION_MODEL`（不是 secret，owner 的設定） | api、worker（**兩者相同**） | **owner**（選填；不設＝照片路徑跟 `AI_PROVIDER`） | §4.4（API 以這三個設定與上面四個決定是否接受照片，D-124） |

Owner 的變數：`deploy.sh` 不設，只在最後檢查必填者已存在；沒有時 verification 失敗並寫出要在哪個 service 填（§7.7）。`BFF_OIDC_CLIENT_SECRET`（`source: auth0`）由 §7.8 的 Auth0 步驟設定並 sealed；`GEMINUS_AUTH0_SETUP=manual` 時改由 owner 填，與 owner 的變數相同（值收到即丟棄、沒有時 verification 寫出要在哪裡填）。Live AI 預設關（`AI_PROVIDER=disabled`、沒有 `GEMINUS_AI_LIVE`）。

**腳本實際收到什麼（review 第 1 輪 M2）。** 要知道哪些變數已存在，唯一的方法是 `railway variable list --service <service> --json`，而 CLI 5.64.2 會在其中回傳**每個沒有 sealed 的變數的值**（sealed 的為 `null`；CLI 原始碼 `src/commands/variable.rs`：「JSON and KV output include raw variable values」，沒有只列名稱的選項）。所以 owner 沒有 sealed 的 `BFF_OIDC_CLIENT_SECRET`、AI key 的值會經 CLI 的輸出進入執行 `deploy.sh` 的 process。腳本的處理：在解析 JSON 的當下（`json.loads` 的 `object_pairs_hook`）只保留它需要的值，也就是自己產生的密碼（對齊 login 用）與 Postgres 的 `DATABASE_PUBLIC_URL`，並登記為 secret（輸出中遮蔽為 `[redacted]`、不得出現在引數中）；其他每個值（owner 的全部、Railway 自己的、沒有列在 `variables.json` 的）在解析時就換成「已設」或「sealed」的標記，不進任何 dict、輸出、錯誤訊息或 exception，也不寫入任何地方。Python 不能抹除記憶體：CLI 的原始輸出在 process 中短暫存在，直到被回收。**Owner 應該在新增變數後立刻 sealed**（service → Variables → 變數右側的 3-dot 選單 → Seal；Railway 文件：sealed 後不能解除，但值仍可以修改），之後 CLI 只回傳 `null`，值不會到達腳本。Verification 會以名稱列出還沒有 sealed 的 owner secret（`BFF_OIDC_CLIENT_SECRET`、`GEMINUS_ANTHROPIC_API_KEY_A`／`_B`、`GEMINUS_MUSE_API_KEY`），提醒 sealed；`GEMINUS_AI_LIVE` 不是 secret，不提醒（它的值同樣丟棄）。這與 H-033「只看變數名稱、不讀值」的字面仍有差距（沒有 sealed 時值會短暫經過腳本），由 coordinator 轉給 owner 決定。

API 與 web 各自的 `forbidden` 清單（`variables.json`）中的變數不得設在該 service：API 沒有任何 AI key（§4.4：API 拒絕啟動的每個 key 變數；live 開關、其他三個 AI 設定與照片路徑的三個設定則與 worker 相同），web 沒有 database、bucket 與 AI key。

### 7.4 Bucket（Railway Storage Buckets）

Railway 文件（2026-10-26）：Buckets 是 private、S3 相容的 object storage（跑在 Tigris 上），只能以 bucket 的 credential 修改；不支援 public bucket；Credentials 頁提供 `BUCKET`、`ACCESS_KEY_ID`、`SECRET_ACCESS_KEY`、`REGION`（例如 `auto`）、`ENDPOINT`（例如 `https://t3.storageapi.dev`），可用 reference variable 帶給 service；新的 bucket 用 virtual-hosted 的 URL，較早建立的可能需要 path-style（Credentials 頁會寫）；region 建立後不能改；目前只能經公開網路存取；service 上傳到 bucket 算 service egress；不支援 server-side encryption、versioning、object lock、lifecycle。

1. 建立 bucket（名稱 `geminus-objects`），選 region（建立後不能改，§10 問題 3）。
2. api 與 worker 照 `variables.json` 設 `OBJECT_STORAGE_*`；Credentials 頁寫 path-style 時 `OBJECT_STORAGE_S3_URL_STYLE=path`。
3. **在放任何資料之前，確認 bucket 拒絕覆寫**（adapter 以 `If-None-Match: *` 保證 object 寫入後不被取代；Railway 文件沒有寫到條件式 PUT，Tigris 文件寫 `If-None-Match: "*"` 在 object 存在時回 412）。由 owner（或 owner 授權、拿得到 credential 的 session）在自己的機器上：

   ```sh
   GEMINUS_LIVE_S3=1 OBJECT_STORAGE_S3_ENDPOINT=… OBJECT_STORAGE_S3_BUCKET=… OBJECT_STORAGE_S3_REGION=auto \
   OBJECT_STORAGE_S3_ACCESS_KEY_ID=… OBJECT_STORAGE_S3_SECRET_ACCESS_KEY=… \
     go test -count=1 -run TestLiveBucket -v ./internal/storage/objects/
   ```

   它寫兩個很小的 synthetic object（一個不存在的 organization 之下）、確認第二次 PUT 被 bucket 本身拒絕、讀回、刪除。失敗時不要使用這個 bucket（回報 coordinator）。

### 7.5 Database 的 bootstrap 與 seed（一次）

`deploy.sh` 自動做以下 1–5（§7.7 的步驟 4–6）；手動部署時照做：

1. Postgres service → Settings → Networking 暫時開 Public Access（TCP proxy；Railway 文件：會產生 `DATABASE_PUBLIC_URL`，經 proxy 的流量計費）。用 `SELECT version()` 確認 major 版本（migration 只在 16 上測過；不是 16 時先回報）。
2. 在 owner 的機器上以 `DATABASE_PUBLIC_URL`（administrator：template 的 postgres login，需要能 CREATE ROLE；URL 加 `sslmode=require`）執行 §3.1 的兩個 SQL。
3. 部署 `migrate`，看它結束為 Completed、log 中的 JSON 為 `"pending":[]`。
4. §3.3 的 seed：`GEMINUS_PROVISION_DATABASE_URL` 用 `geminus_provisioner`、proxy 的 host 與 port、`sslmode=require`。
5. 關掉 Public Access。

### 7.6 部署順序與 web → api

1. `migrate`（每次有新的 migration 時先部署它，等 Completed）。
2. `api`、`worker`（worker 的 `WORKER_ORGANIZATION_IDS` 用 seed 的結果）。
3. `web`。
4. Auth0 的 callback／logout URL 用 web 的網域（§6）。

`deploy.sh` 照這個順序（§7.7 的步驟 5、7）。Web 經 API 的公開網域呼叫它：`BFF_API_URL=https://${{api.RAILWAY_PUBLIC_DOMAIN}}`。Railway 的 private network（`http://api.railway.internal:8080`，WireGuard 加密）是 plain http，而 BFF 只在全部 URL 都是 loopback 時接受 http，所以不使用；要改用 private network 需要先改 BFF 的檢查並另行審查（§10 問題 2）。API 的公開網域只接受 Bearer token（沒有 cookie、沒有 CORS）。

### 7.7 `deploy/railway/deploy.sh`（H-032，非互動）

由 coordinator（或 owner）在有 token 的機器上執行；本 repository 的 agent 沒有執行 `--apply`。

```sh
deploy/railway/deploy.sh --install-cli "$HOME/.local/railway-5.64.2"       # 一次：安裝 pin 住的 Railway CLI
deploy/railway/deploy.sh --install-auth0-cli "$HOME/.local/auth0-1.37.0"   # 一次：安裝 pin 住的 Auth0 CLI（§7.8）
export PATH="$HOME/.local/railway-5.64.2:$HOME/.local/auth0-1.37.0:$PATH"
export RAILWAY_API_TOKEN=…      # 或 RAILWAY_TOKEN=…（見下）；不要放進 shell history 或任何檔案
export GEMINUS_AUTH0_ISSUER='https://<tenant>.<region>.auth0.com/' GEMINUS_AUTH0_AUDIENCE='https://api.geminus-demo.invalid' \
       GEMINUS_SEED_OWNER_SUBJECT='auth0|<owner 的 user_id>' GEMINUS_RAILWAY_BUCKET_REGION=sjc
deploy/railway/deploy.sh --dry-run     # 不連網：檢查輸入、工具與部署檔，印出 --apply 的每個命令
deploy/railway/deploy.sh --apply       # 步驟 2b 等 owner 在自己的瀏覽器核准 Auth0 CLI 的登入（§7.8）
```

`GEMINUS_AUTH0_SETUP=manual`（Auth0 CLI 的路徑不能用時，§7.8 的手動備援）：不執行 Auth0 步驟，另外需要 `GEMINUS_AUTH0_CLIENT_ID='<Client ID>'`，`BFF_OIDC_CLIENT_SECRET` 由 owner 在 Railway 填並 sealed（T-039 原本的流程）；設 `GEMINUS_JUDGE_SUBJECT='auth0|<評審帳號的 user_id>'` 時 seed 把評審帳號設為 demo 組織的 editor。

- **Token**（[CLI 文件](https://docs.railway.com/guides/cli)，2026-10-26；CLI v5.64.2 的 `src/config.rs`）：`RAILWAY_TOKEN` 是 project token，project 與 environment 是 token 自己的、必須已存在，腳本不加 `--environment`；`RAILWAY_API_TOKEN` 是 account／workspace token，腳本以名稱找 `GEMINUS_RAILWAY_PROJECT`（預設 `geminus-demo`），沒有就 `railway init`（`GEMINUS_RAILWAY_WORKSPACE` 選 workspace），再 link `GEMINUS_RAILWAY_ENVIRONMENT`（預設 `production`）。兩個都設時 CLI 以 `RAILWAY_TOKEN` 決定 project。Token 只由 CLI 從環境讀：不在任何引數、輸出或 log 中；腳本把 token 登記為 secret（CLI 的錯誤訊息引用它時印成 `[redacted]`），並從其他每個程式（psql、`go run`、git、`check_deploy.py`）的環境中移除；`deploy.sh` 在 xtrace 開著時（`bash -x`、`set -x` 或 `SHELLOPTS=xtrace`）在執行任何命令之前拒絕（exit 2）。
- **CLI 的版本**：pin 在 5.64.2。`--install-cli` 下載 GitHub release 的 `railway-v5.64.2-x86_64-unknown-linux-musl.tar.gz` 並核對 SHA-256 `a154c3ff5ac3e2ece99bdca9d8d5505fc7f85a949689346bd934fb425246e229`（2026-10-26 計算；release 沒有 checksum 檔，npm 的 `@railway/cli@5.64.2` 也下載同一個檔而不核對）；只支援 Linux x86_64，其他平台自行安裝同一版本。`--apply` 檢查 `railway --version` 為 5.64.2，並以 `CI=true`、`RAILWAY_NO_AUTO_UPDATE=1` 執行，CLI 不會自行更新。
- **需要**：python3、git、psql 15 以上（`\getenv`）、go（`go run ./cmd/provision-tenant`）。Checkout 有未 commit 的修改時拒絕（`railway up` 上傳 working tree；`GEMINUS_ALLOW_DIRTY=1` 接受）。
- **步驟**（`railway_deploy.py` 的 docstring；每步都先查、只在缺少時建立，所以可以重跑）：
  1. project 與 environment；
  2. PostgreSQL、bucket（`geminus-objects`，region 建立後不能改）、四個 service、各自的 config-as-code 檔（`railway environment edit --service-config <service> configFile /deploy/railway/<service>.railway.json`）、api 與 web 的公開網域；
  2b. Auth0（H-034，`GEMINUS_AUTH0_SETUP=cli`，預設）：§7.8；完成時 Auth0 CLI 已登出、它的 home 已刪除，`GEMINUS_AUTH0_CLIENT_ID` 是這一步的輸出（給步驟 3 的 `BFF_OIDC_CLIENT_ID`），web 的 `BFF_OIDC_CLIENT_SECRET` 已設定並 sealed；
  3. `variables.json` 的變數（`--skip-deploys`）：產生的 secret 只在缺少時以 Python 的 `secrets` 產生，經 `railway variable set KEY --stdin` 設定；bucket 的 key 以同樣方式複製；owner 的變數不設（列出變數時收到的值即丟棄，§7.3）；
  4. 為本次執行開 Postgres 的 TCP proxy（已經有一個時沿用它而不移除：腳本無法分辨它是別人開的，還是被中斷的執行留下的；結束時會回報，見下方「TCP proxy」），以 administrator 的 `DATABASE_PUBLIC_URL`（`sslmode=require`，只放在 psql 的環境）執行 `db/bootstrap_roles.sql` 與 `deploy/sql/create_logins.sql`；login 已存在而 Railway 的密碼還讀得到時以 `ALTER ROLE … PASSWORD` 對齊；然後把產生與複製的 secret 設為 sealed（`variables.<KEY>.isSealed`）；
  5. 部署 `migrate`，等到 `geminus_migrations.applied` 的筆數等於這個 checkout 的 migration 數；
  6. worker 還沒有 `WORKER_ORGANIZATION_IDS` 時 seed 一個 synthetic organization（`GEMINUS_SEED_ORG_NAME`，owner 為 `GEMINUS_SEED_OWNER_SUBJECT`，評審帳號以 `-member-issuer`／`-member-subject`／`-member-role editor` 在同一個 transaction 成為 editor：§7.8），把 `organization_id` 設給 worker；再以 administrator 列出評審帳號的 membership：必須只有 demo 組織的 editor，否則 verification 失敗並寫出怎麼做；移除本次開的 TCP proxy（步驟 4–6 失敗時、收到 SIGTERM 或 SIGHUP 時也移除）；
  7. `railway up` api、worker、web，等每個成功（api、web 有 Railway 的 healthcheck）；
  8. verify：api 的 `/healthz`、`/readyz` 200、`/v1/me` 401，web 的 `/` 200、`/auth/login` 302／303 到 `<issuer>authorize`，worker 仍為 SUCCESS，owner 的必填變數存在（只看名稱）、沒有 sealed 的 owner secret 以名稱列出並提醒 sealed（不算失敗）、Postgres 沒有任何 TCP proxy 開著；印出 Auth0 要填的 callback 與 logout URL。
- **TCP proxy（review 第 1 輪 M1）**：Postgres 的公開 TCP proxy 讓 administrator（template 的 `postgres` superuser）可從公開網路登入，所以不得無聲地留著。(a) 本次開的 proxy 在步驟 4–6 結束時移除，失敗時、收到 SIGTERM 或 SIGHUP 時也一樣（兩者轉成 exit，讓清理執行；第二個訊號在清理期間被忽略）；SIGKILL 無法攔截，留下的 proxy 由下一次執行回報。(b) `railway tcp-proxy delete` 的 exit code 非 0 時，腳本寫出 proxy 的 id 與手動移除的命令，整次執行以 exit 1 結束。(c) 每次 `--apply` 結束前（不論成功、失敗或被訊號中斷；在 verification 中，或 verification 之前就停下時於結束時）都列出 Postgres 的 TCP proxy：還有任何 proxy 開著，包括執行前就存在、被本次沿用的，就寫出它的 id 與移除命令（`railway tcp-proxy delete <id> --yes --service Postgres --environment <environment>`）並以 exit 1 結束；列不出來也是 exit 1。只有明確設了 `GEMINUS_KEEP_DB_PROXY=1` 時，開著的 proxy 只以警告列出（仍寫 id）、不算失敗；它不豁免 (b)。執行前就存在的 proxy 腳本不移除（可能是 owner 依 §7.5 手動開的），但每次執行都回報，直到有人移除它或設了 `GEMINUS_KEEP_DB_PROXY=1`。
- **結果**：exit 0 完成；exit 1 某一步失敗或 TCP proxy 還開著（訊息寫是哪一步與怎麼做，例如 `the owner has not set BFF_OIDC_CLIENT_SECRET on web (Railway → web → Variables)`，owner 填好後重跑即可）；exit 2 用法錯誤或 xtrace 開著；exit 128+N 被訊號 N 中斷（SIGTERM 143、SIGHUP 129；清理已執行，重跑即可）。第一次執行在 owner 填 `BFF_OIDC_CLIENT_SECRET` 之前必定停在 verification：這是預期的。
- **Secret 的產生**：H-032 希望用 Railway 的 `${{secret(...)}}`。Railway 文件只把它寫成 **template** 的函式（[templates/create](https://docs.railway.com/templates/create)：「When a template is deployed, all template variable functions are executed」）；[variables](https://docs.railway.com/guides/variables) 與 [variables reference](https://docs.railway.com/variables/reference) 沒有說一般 service variable 可以用；社群的回答（[station](https://station.railway.com/questions/secret-generates-a-new-string-on-8523cc4d)）說在 service 層級它可能每次重新產生。所以腳本不用它，改在本機產生、經 stdin 設定、再 sealed；產生的值不出現在任何輸出、檔案或引數中（每次呼叫前檢查）。
- **未查證：reference 到 sealed variable 時是否展開。** `DATABASE_URL` 與 `GEMINUS_MIGRATE_DATABASE_URL` 以 `${{GEMINUS_…_DB_PASSWORD}}` 引用 sealed 的密碼；Railway 文件沒有寫 reference 是否展開 sealed 的值（社群有一例 sealed 的 reference 仍可用，但不是文件）。若不展開，步驟 5 的 migrate 會以密碼錯誤失敗，腳本停在那裡並指向這裡。**復原**：在 Railway 的 UI 刪除 migrate、api、worker 的 `GEMINUS_*_DB_PASSWORD`（sealed 的可以刪），以 `GEMINUS_RAILWAY_SEAL=off deploy/railway/deploy.sh --apply` 重跑：腳本重新產生密碼、以 `ALTER ROLE` 對齊、不 sealed（保護較弱：可在 UI 與 `railway variable list` 看到），並回報 coordinator（§10 問題 5）。
- **Rehearsal**：`scripts/rehearse_railway_deploy.py` 以 `railway`、`psql`、`go` 的替身（不連網）執行完整的 `--apply` 數次：secret 只經 stdin、不出現在引數與輸出（包括 CLI 錯誤訊息中引用的值與 token）、psql 與 go 的環境沒有 token、不設 owner 的變數、CLI 回傳 owner 沒有 sealed 的值時，這些值不出現在腳本的狀態、輸出、錯誤與任何 method 的回傳值中，且以名稱提醒 sealed、API 沒有 AI key、部署順序、只有 api 與 web 有網域、只 seed 一次、TCP proxy 被移除、產生與複製的 secret 都 sealed（`GEMINUS_RAILWAY_SEAL=off` 時都不）、第二次執行不建立任何東西也不換 secret、owner 未填時與登入導向錯誤時 verification 分別失敗；TCP proxy 的移除失敗、執行前就存在的 proxy、CLI 說移除了卻還在、create 沒有印出 id、最後列不出來、SIGTERM 與 SIGHUP（以獨立的 process 執行）都以非 0 結束並寫出 id 與移除命令（`GEMINUS_KEEP_DB_PROXY=1` 時成功但仍列出）；`deploy.sh` 在 xtrace 時拒絕。它證明腳本自身一致，不證明 Railway 接受每個呼叫（NOT RUN，直到 coordinator 的 `--apply`）。以上是 `GEMINUS_AUTH0_SETUP=manual` 的執行；Auth0 步驟另以 Auth0 CLI 的替身演練（§7.8 最後一條）。

### 7.8 Auth0 步驟（H-034）與評審帳號（H-035 (2)）

由 coordinator 在部署 session 中隨 `deploy.sh --apply` 執行（步驟 2b，web 的公開網域建立之後、步驟 3 的變數之前）；撰寫它的 agent 沒有登入、沒有呼叫任何 Auth0 tenant（只有替身與 `--dry-run`）。

**部署當天 owner 要做的事。** (1) 事先：註冊 Auth0、建立 tenant（帳號與條款本人處理）、在 database connection（預設 `Username-Password-Authentication`）建立自己的使用者，告知 tenant 的網域與自己的 `user_id`（coordinator 以 `GEMINUS_AUTH0_ISSUER`、`GEMINUS_SEED_OWNER_SUBJECT` 傳入；`user_id` 不寫進 repo）。(2) `--apply` 跑到 2b 時，畫面出現一行 `Auth0 sign-in: open https://…auth0.com/activate?user_code=XXXX-XXXX …, check that it shows the code XXXX-XXXX, and approve`：在自己的瀏覽器打開，確認畫面上的 code 相同，以自己的 Auth0 帳號登入並核准（不交出任何密碼或 token；預設 15 分鐘內）。(3) 執行結束時 stderr 出現一次評審帳號的 email 與密碼：直接抄進提交說明，不要存到任何檔案或 evidence。其他都由腳本做。

**CLI 的 pin。** [auth0/auth0-cli](https://github.com/auth0/auth0-cli) release [v1.37.0](https://github.com/auth0/auth0-cli/releases/tag/v1.37.0)（2026-09-30 發行，2026-10-26 時 26 天；v1.38.0 是 10-19，不到兩週）、MIT（tag 中的 `LICENSE`）。`--install-auth0-cli DIR` 下載 `auth0-cli_1.37.0_Linux_x86_64.tar.gz`，先核對 release 的 `checksums.txt` 列的 SHA-256 `277e8fa32b9e4f4feb8e30717892139251821c0c66cd3ad5f326ba759ec6ac4f`，再核對其中 `auth0` binary 的 SHA-256 `2bc2ef7c1ab3c340a50bebb2c6bfd9692ea23dfee539bf7a2e1aac3debb3491c`（2026-10-26 以下載的 archive 計算，archive 的 SHA-256 與 checksums.txt 相同），兩者都對才安裝。執行時 `deploy.sh` 讀 PATH 上的 `auth0` 一次、核對 SHA-256，只執行把同一份 bytes 寫進只有自己能寫的暫存目錄（0700）的副本，結束時刪除；核對之後 PATH 上的檔案被換掉也不會被執行（review 第 1 輪 L2）。`--apply` 在執行 CLI（連 `--version` 也不跑）之前計算 PATH 上 `auth0` 的 SHA-256，不是 pin 住的就停在 preflight。`scripts/check_deploy.py` 檢查 `deploy.sh` 與 `railway_deploy.py` 的 pin 一致。只支援 Linux x86_64。

**CLI 的執行環境。** 每次呼叫都加 `--agent-mode --no-input --no-color`（CLI 1.37.0 的 agent mode：結果是 stdout 上的 JSON，失敗時 stderr 只有 JSON error envelope，不提示、不開瀏覽器）。環境只有 `PATH`、語系、proxy 與 CA 設定、`HOME=<state>/geminus-deploy/auth0-cli`（`$XDG_STATE_HOME` 或 `~/.local/state`，權限 0700）、`AUTH0_CLI_ANALYTICS=false`（否則 CLI 會把 tenant 名稱等送到 Heap analytics）與 `DBUS_SESSION_BUS_ADDRESS=disabled:`（不碰桌面的 keyring：CLI 沒有 keyring 時把 access token 寫在 `HOME/.config/auth0/config.json`，這個 home 在結束時刪除）；沒有 Railway token。上一次執行被 SIGKILL 留下的 home，下一次執行先登出再刪除。

**每一個動作**（CLI 1.37.0 的命令；先查，只在缺少時建立，只在與下表不同時更新，寫入後讀回比對，Auth0 沒有保留時以 exit 1 停下；寫入的 JSON 一律經 stdin `--data @-`）：

| 對象 | 命令 | 設定 |
|---|---|---|
| 登入 | `auth0 login --domain <tenant>` | device code：只顯示 CLI 印出的 `verification_uri` 與 `user_code`；登入的 tenant 必須是 `GEMINUS_AUTH0_ISSUER` 的網域，否則什麼都不改並停下 |
| Owner 的使用者 | `auth0 users show <GEMINUS_SEED_OWNER_SUBJECT>` | 必須在 `GEMINUS_AUTH0_CONNECTION` 中（應用程式只開這個 connection，否則 owner 無法登入）；在任何寫入之前檢查 |
| API | `auth0 apis list --number 1000`、`apis create`／`apis update <id>`、`apis show <id>` | identifier `GEMINUS_AUTH0_AUDIENCE`、name `Geminus demo API`、`signing_alg` RS256、`token_dialect` `rfc9068_profile`、`token_lifetime` 600、`allow_offline_access` true、`skip_consent_for_verifiable_first_party_clients` true |
| Regular Web Application | `auth0 apps list --number 1000`（以 name 找，必須至多一個）、`apps create`／`apps update <id>`、`apps show <id>` | name `Geminus demo web`、`app_type` `regular_web`、`oidc_conformant` true、`token_endpoint_auth_method` `client_secret_post`、`grant_types` 只有 `authorization_code`、`refresh_token`、`callbacks` 只有 `https://<web 網域>/auth/callback`、`allowed_logout_urls` 只有 `https://<web 網域>/`（BFF 的 `apps/web/lib/session/oidc.ts`）、`web_origins` 與 `allowed_origins` 空、`jwt_configuration.alg` RS256、`refresh_token`：`rotation_type` rotating、`expiration_type` expiring、`leeway` 30、`token_lifetime` 604800、`infinite_token_lifetime` false、`idle_token_lifetime` 43200、`infinite_idle_token_lifetime` false |
| Sign-up | `auth0 connections list`、`connections show <id>`、`connections update <id>` | `options.disable_signup` true；PATCH 會取代整個 `options`、沒有 `display_name` 時會被改成 name（Management API 文件），所以送出現有的全部 options 加上 `disable_signup` 與現有的 `display_name`；options 中有腳本不保留的值（custom database 的 scripts 或 configuration）時不改、停下，改用手動備援；`authentication_methods.password` 這類設定物件（`{"enabled": true}`，不是密碼）照常保留（review 第 1 輪 M1） |
| 只開 database connection | `auth0 connections enabled-clients show <id>`、`… update <id>` | 應用程式只在 `GEMINUS_AUTH0_CONNECTION` 啟用；其他 connection（新 tenant 預設的 Google 等）停用這個應用程式，否則任何人都能以社群登入在 Auth0 建立使用者（H-035 (2)：關閉自行註冊） |
| 評審帳號 | `auth0 users search-by-email <GEMINUS_JUDGE_EMAIL>`、`users create`（或 `users update <id>`） | 在該 connection；`email_verified` true、`verify_email` false、name `Geminus demo judge`；密碼以 Python `secrets` 產生（24 字元，大小寫、數字、符號都有，沒有三個相同字元相連），經 stdin；已存在時不改密碼，`GEMINUS_JUDGE_RESET_PASSWORD=1` 時設新密碼 |
| Client secret | `auth0 apps show <id> --reveal-secrets` → `railway variable set BFF_OIDC_CLIENT_SECRET --stdin --service web --skip-deploys` → `railway environment edit … variables.BFF_OIDC_CLIENT_SECRET.isSealed true` | 只在本次建立了應用程式、web 沒有這個變數、或 web 的 `BFF_OIDC_CLIENT_ID` 不是這個應用程式時讀取；不受 `GEMINUS_RAILWAY_SEAL=off` 影響（沒有別的變數 reference 它）；Railway 拒絕 seal 時本次執行在 verification 失敗（不是 done），重跑時 web 已有而沒有 sealed 的 secret 不再讀取、直接 seal（review 第 1 輪 L1） |
| 登出 | `auth0 logout <tenant>`（`auth0 tenants list` 列出的每一個）、刪除 CLI 的 home | 成功、失敗、SIGTERM、SIGHUP 都執行；沒有 keyring 時 CLI 在移除 tenant 之後刪 keyring 失敗、exit 1（CLI 原始碼），所以以 `tenants list` 判斷是否還有 session；還有時以 exit 1 結束（home 仍刪除）；stdout 寫不出去（pipe 關閉、終端機 hangup）時在下一個訊息處停下，登出、刪 home、移除 TCP proxy 照常執行，以 exit 74 結束（SIGHUP 為 129；review 第 1 輪 M2） |

**Secret。** Client secret、評審密碼與 Railway token 不出現在任何程式的引數、輸出、檔案、部署腳本的狀態、method 的回傳值或 exception 中（rehearsal 檢查）：CLI 的 JSON 在解析的當下把 `client_secret`、`signing_secret`、`signing_keys`、`device_code`、`password` 等欄位換成標記（別的應用程式的 secret、API 的 signing secret 也一樣）；需要的 client secret 只放進那一次呼叫自己的 list，交給 stdin 後清空；要遮蔽的值只以每次執行新產生 key 的 HMAC digest 保存；帶 secret 的 CLI 呼叫失敗時只顯示 error envelope 的類別，不顯示 CLI 的訊息。評審密碼只在執行結束時（不論成功、失敗或被訊號中斷）寫到 **stderr** 一次：`deploy.sh --apply > log` 的 stdout 不會有它；不要把 stderr 導到檔案（`2>&1 | tee` 會把它寫進檔案）。遺失時以 `GEMINUS_JUDGE_RESET_PASSWORD=1` 重跑（顯示新的一次），或在 dashboard 改。CLI 的 access token 在登出後仍在 Auth0 端有效到它過期為止（期限由 Auth0 決定，未查證；登出只刪除本機的副本），本機的副本隨 home 刪除。

**評審帳號的 membership。** Role 是 `editor`：openapi 的 `x-required-roles` 中，提出 AI proposal（`createProposal`、`createImageProposal`）、核准（`approveProposal`）、建立模型與執行模擬都要 owner 或 editor；viewer 都不行；owner 另外能管理成員與讀 audit log，demo 不需要。Seed 以 `provision-tenant organization … -member-issuer <issuer> -member-subject <評審的 user_id> -member-role editor` 在建立 demo 組織的同一個 transaction 加入它（audit `organization.provision` 帶 `member_user_id`、`member_role`），之後以 administrator 列出評審帳號的 membership，必須只有 demo 組織的 editor。Provisioning 只能把成員加到它在同一個 transaction 建立的 organization（migration 0004），所以 **demo 組織在評審帳號存在之前就 seed 過時**（例如先以 manual 部署過），腳本只能把評審帳號建成 user（`provision-tenant user`），verification 失敗並寫出它的 `usr_…`：由 demo 組織的 owner 以 `POST /v1/organizations/<organization>/memberships`（`{"user_id": "usr_…", "role": "editor"}`，ADR-023；目前沒有 UI）加入。評審的 email 預設 `judge@geminus-demo.invalid`（不是真的信箱：無法以 email 重設密碼）。

**手動備援**（CLI 拿不到、登入失敗、或上面任何一步停下而無法修正時）：照 [Auth0 runbook](auth0.md) §6 在 dashboard 做同樣的設定，再以 `GEMINUS_AUTH0_SETUP=manual`、`GEMINUS_AUTH0_CLIENT_ID`、`GEMINUS_JUDGE_SUBJECT` 執行 `--apply`，`BFF_OIDC_CLIENT_SECRET` 由 owner 在 Railway 填並 sealed。

**Rehearsal**（`scripts/rehearse_railway_deploy.py`，Auth0 CLI 1.37.0 的替身，agent mode 的 JSON 形狀依 CLI 原始碼）：新的 tenant 與 project（設定值與上表完全相同、client secret 只經 stdin 並 sealed、評審帳號只是 demo 組織的 editor、密碼在 stderr 恰好一次）、重跑（替身以另一個順序列出 grant types；Auth0 沒有任何寫入、不再讀 client secret、不改密碼）、tenant 漂移（多的 callback、grant、overlap 0、web origin、API 的 dialect 與 lifetime、sign-up 又開、應用程式在 Google 上：全部改回）、密碼重設與被中斷的執行留下的 home、checksum 不符（CLI 一次都沒跑）、步驟失敗、登入到別的 tenant、Auth0 沒有保留 sign-up 關閉、CLI 在錯誤中引用密碼、登出沒有移除 session、owner 不在 database connection、demo 組織先 seed 過、評審帳號在別的組織、manual 的 `GEMINUS_JUDGE_SUBJECT`、dry run、評審帳號建立前與建立後的 SIGTERM（獨立的 process，exit 143、登出、密碼在 stderr 恰好一次）、`--install-auth0-cli` 拿到不是 pin 住的 archive。替身證明腳本自身一致，不證明 Auth0 接受每個呼叫（NOT RUN，直到部署 session）。

## 8. 上線前檢查（staging，由人執行；NOT RUN by agents）

1. `migrate` 為 Completed，JSON 為 `"pending":[]`；再部署一次 `migrate`，`"applied":[]`。
2. `https://<api 網域>/healthz` 與 `/readyz` 為 200；`/v1/me` 不帶 token 為 401；api 的 log 有 `serving`，沒有 `refusing the database login`。
3. Worker 的 log 有 `running` 與 organization 的數量 1；沒有 `invalid configuration`。
4. §7.4 的 `TestLiveBucket` 通過。
5. [Auth0 runbook](auth0.md) §5 的全部檢查（cookie 屬性、沒有 `Authorization` header 到瀏覽器、登出、未 provision 的使用者 403、refresh 失敗的分類）。
6. 以 synthetic 資料走一次 demo：建立 model、上傳 synthetic 檔案（確認 bucket 中出現 `tenant/<org>/imports/…`）、執行模擬。
7. Railway 每個 service 的 Variables 中沒有 `forbidden` 清單的變數；sealed 的值在 UI 看不到。
8. 公開網址前由 owner 決定（AGENTS.md §6）；開 live AI 前另依 §4.4 與 T-055。

## 9. 本機 staging smoke（`make staging-smoke`）

`make build-containers` 建立 `geminus-{api,worker,web}:local`，`make staging-smoke` 執行 `deploy/local/staging-smoke.sh`：以 `deploy/local/compose.yaml`（**只給本機**，含 repository 的測試 OIDC provider，絕不部署）啟動 PostgreSQL、migrate（兩次）、seed、api、worker、web，檢查 migration login 以外不能 migrate、API 拒絕 migration login、`/healthz`、`/readyz`、未帶 token 的 401、worker 運作、BFF 的 `/`、登入導向（PKCE）、未登入的 401、image 為非 root 且沒有 credential 變數、SIGTERM 時 exit 0，最後全部移除。每次執行產生一次性的密碼與 session key。沒有 Docker daemon 時 exit 2（NOT RUN）。真正的 Auth0 登入、bucket 與公開網址只能在 staging 由人檢查（§8）。CI 的 `containers` job 執行這兩個 target。

## 10. 未涵蓋與待 owner 回答

- 未涵蓋：container scan、SBOM、image 簽章；部署後的 security header 實測（T-057 的 header 只在本機 `next start` 與 e2e 驗證過）；備份與還原演練（`make db-restore-test` 仍為 NOT IMPLEMENTED）；log／telemetry 的保存與存取；告警（T-036）；孤兒 object 的清理（D-044）；花費預留的 operator 介面（T-053 I4 (2)）；live eval 的花費紀錄要不要改經 DB ledger（T-053 I4 (4)，owner 選擇）；T-054 I1 的一般做法與 I2（S3 adapter 已不把自己的逾時包成 context 的錯誤，`TestS3OwnTimeoutIsNoContextError`；object storage 逾時時的 503 標記仍未區分）。
- 問題：(1) 是否接受 ADR-029；(2) web → api 用公開網域還是 private network（需要放寬 BFF 的 http 規則）；(3) bucket 的 region 與資料保存期限；(4) API 的 key-free AI 可用性由 T-055 一併做，或另開任務；(5) Railway 是否展開 reference 到 sealed variable 的值（§7.7；第一次 `--apply` 會知道）；(6) `${{secret()}}` 不用於 service variable、改由腳本產生，是否接受（H-032 的偏好）；(7) PostgreSQL 的 major 版本（Railway 文件沒寫；migration 只在 16 上測過）；(8) owner 的變數沒有 sealed 時，CLI 的變數清單會把值交給腳本（收到即丟棄，§7.3），是否接受，或 owner 一律以 sealed 新增（H-033 的「只看變數名稱、不讀值」）；(9) Auth0 步驟（§7.8）把應用程式在 database connection 以外的 connection 都停用（H-035 (2) 的「關閉自行註冊」也擋社群登入），是否接受；(10) 評審帳號的 email 用 `judge@geminus-demo.invalid` 或 owner 指定的信箱；(11) provision-tenant 只在建立組織時加入一個成員（不放寬 migration 0004），demo 組織先 seed 過時由 owner 以 API 加入評審帳號，是否接受。
