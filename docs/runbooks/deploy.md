# Runbook：部署（containers、migration、Railway）

依據：T-039（hackathon 切片，H-029 (3)）、[ADR-029](../adr/ADR-029-containers-and-staging-deployment.md)（PROPOSED）、[ADR-018](../adr/ADR-018-browser-session-and-oidc-provider.md)、[ADR-017](../adr/ADR-017-oidc-token-verification.md)、[ADR-024](../adr/ADR-024-job-queue-and-worker.md)、[ADR-028](../adr/ADR-028-opentelemetry.md)、[Auth0 runbook](auth0.md)、AGENTS.md §2、§6。雲端廠商：owner 的選擇（coordinator 轉達，H-030）為 **Railway**（同日改為「純用 railway」，不接 Cloudflare）。

**誰執行。** Railway 的部署由 coordinator 在有 Railway token 的 session 中以 `deploy/railway/deploy.sh --apply` 執行（H-032，§7.7），或由 owner 照 §7.1–§7.6 手動執行。撰寫本 runbook 與腳本的 agent 沒有執行 `--apply`、沒有呼叫 Railway 的 API、沒有建立任何東西（只執行了不連網的 `--dry-run` 與以替身進行的 rehearsal）。Repository 與 image 中沒有任何 credential；腳本產生的 secret 只經 stdin 交給 Railway CLI，不印出、不寫檔；owner 的 secret（§7.3：Auth0 client secret、AI key、live 開關）只由 owner 在 Railway 的 UI 填入，腳本不設也不讀。只放 synthetic 資料。Production 的批准（把網址公開、開啟 live AI）是人的決定（AGENTS.md §6）。

§1–§6 與廠商無關；§7 是 Railway 的步驟（§7.7 是自動化的 `deploy.sh`）；§8 是上線前由人執行的檢查；§9 是本機的 `make staging-smoke`。

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
| `AI_VISION_PROVIDER` | worker | 否 | 否 | 照片路徑（T-055 給 T-056）：不設＝跟 `AI_PROVIDER`；`disabled` 關閉；`anthropic`／`meta` 為另一個 live provider 時需下面兩個變數、自己的 key 與同一個開關；批准沒列圖片（H-018）時照片路徑維持關閉（`images_not_approved`），proposal 不受影響 |
| `AI_VISION_POLICY_APPROVAL`、`AI_VISION_MODEL` | worker | `AI_VISION_PROVIDER` 是另一個 live provider 時 | 否 | 該 provider 的批准與 model（同 `AI_POLICY_APPROVAL`／`AI_MODEL` 的規則）；其他情況必須不設 |
| `AI_MONTHLY_LIMIT_USD`、`AI_DAILY_LIMIT_USD`、`AI_ORGANIZATION_DAILY_LIMIT_USD` | 每個 worker 相同 | 否 | 否 | 花費上限（D-110）；限額是每個 process 的設定，不同的 worker 必須相同（T-053 I2） |
| `AI_MAX_PROMPT_BYTES`、`AI_MAX_OUTPUT_TOKENS`、`AI_TIMEOUT_SECONDS`、`AI_ATTEMPT_TIMEOUT_SECONDS`、`AI_MAX_RETRIES`、`AI_MAX_CONCURRENT_PER_ORGANIZATION` | worker | 否 | 否 | 覆蓋 provider 的預設 budget |
| `AI_API_KEY` | — | — | — | 一律拒絕（D-018） |
| `GEMINUS_META_API_KEY`（Meta key 的舊名稱）、`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`MODEL_API_KEY` | — | — | — | 從不讀取（D-018）；**API 的環境中有其中任何一個，或 `AI_API_KEY`、上面任何一把 key，API 即拒絕啟動**（`key_forbidden`，錯誤只寫變數名稱；T-053 I5） |

**API 不持有任何 AI key（T-053 I5；T-055 的 follow-up，coordinator 的決定）。** API 只以四個非 secret 的設定決定是否接受 AI proposal：`AI_PROVIDER`、`AI_POLICY_APPROVAL`、`AI_MODEL`、`GEMINUS_AI_LIVE`（`config.LoadAPIAI`）。檢查與 worker 的 live gate 相同（同一段程式 `liveGate`：build 記錄的批准、屬於 `AI_PROVIDER`、record 不缺任何項目；`AI_MODEL` 等於 record 的 model 且有價格；開關 on），只是不讀、也不要求任何 key。四個都相符時 API 接受 proposal（worker 以自己的 key 呼叫 provider），啟動 log 為 `AI proposals are accepted; the worker calls the provider`（`provider`、`model`、`code` `LIVE_AI_ON`）。任何不合：開關不是 on 時 API 照常啟動、對新的 proposal 回 503（`AI_POLICY_NOT_APPROVED`；`disabled` 為 `AI_PROVIDER_NOT_CONFIGURED`），啟動 log `AI proposals are refused` 記 typed reason（`code`，例如 `SWITCH_OFF`、`MODEL_MISMATCH`）；開關 on 時 API 拒絕啟動（exit 1，錯誤碼即該條件），與 worker 相同。API 的環境中有任何 AI key 的變數（上表的 key 與從不讀取的名稱）即拒絕啟動（`key_forbidden`），所以 key 只設在 worker。API 與 worker 的四個設定必須相同：`scripts/check_deploy.py` 檢查 `variables.json` 中兩者的宣告相同、API 沒有其他 AI 設定、API 的 `forbidden` 包含 API 拒絕的每個 key 變數（清單取自 `internal/config/apiai.go`）；值由 owner 在 Railway 的 UI 設定，靜態檢查看不到。API 接受、但 worker 不能呼叫 provider 時：worker 的開關 on 而沒有 key，worker 拒絕啟動（不建 handler、不連線），proposal 停在 generating，直到有 worker 啟動；worker 的開關不是 on，每個 proposal 失敗為 `AI_POLICY_NOT_APPROVED`；key 被 provider 拒絕，失敗為 `AI_CREDENTIALS_EXHAUSTED`（HTTP 層 `SERVICE_UNAVAILABLE`）。失敗的 proposal 不重試，除了它的 error、job 與 audit 之外不寫入任何東西（`internal/proposals/generator` 的 `TestAProposalTheWorkerCannotAskFailsSafely`）。Live 開關預設關，只有 owner 開，且 API 與 worker 一起開。

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
- **CSP**：**尚未設定**（app 沒有 Content-Security-Policy、HSTS 或其他 security header；SECURITY §3 的 CSP 仍是後續工作，Next 的 inline script 需要 nonce 設計）。Railway edge 是否加 HSTS 未查證。
- **資料**：只放 synthetic 資料與 demo 圖片；bucket 的加密、保存期限與 region 是供應者的預設，未經 owner 決定（ADR-029）。不放客戶資料、不放真實使用者以外的個資。

## 6. Auth0（owner 的 tenant，最終網域）

照 [Auth0 runbook](auth0.md) §2–§4 建立 API 與 Regular Web Application，網域用 web service 的公開網域（§7.6，例如 `<web>.up.railway.app`）：

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

## 7. Railway（owner 的選擇，H-030）

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
| `BFF_OIDC_CLIENT_SECRET` | web | **owner**（必填） | Auth0 Application 的 Client Secret |
| `GEMINUS_ANTHROPIC_API_KEY_A`、`GEMINUS_ANTHROPIC_API_KEY_B` | **只 worker** | **owner**（選填；只在開 live AI 時） | D-018 的兩把 key |
| `GEMINUS_MUSE_API_KEY` | **只 worker** | **owner**（選填；只在開 Meta live AI 時） | Meta Model API（Standard mode，H-029） |
| `GEMINUS_AI_LIVE`（不是 secret，但是 owner 的開關） | api、worker（**兩者相同**） | **owner**（選填，預設關） | §4.4 |
| `AI_POLICY_APPROVAL`、`AI_MODEL`（不是 secret，owner 的設定） | api、worker（**兩者相同**） | **owner**（選填；只在開 live AI 時） | §4.4（`AI_PROVIDER` 由 `deploy.sh` 只在沒有時設為 `disabled`，owner 改為 `anthropic` 或 `meta` 時兩個 service 一起改） |

Owner 的變數：`deploy.sh` 不設、不讀（只看名稱是否存在），只在最後檢查必填的 `BFF_OIDC_CLIENT_SECRET` 已存在；沒有時 verification 失敗並寫出要在哪個 service 填（§7.7）。Live AI 預設關（`AI_PROVIDER=disabled`、沒有 `GEMINUS_AI_LIVE`）。

API 與 web 各自的 `forbidden` 清單（`variables.json`）中的變數不得設在該 service：API 沒有任何 AI key（§4.4：API 拒絕啟動的每個 key 變數；live 開關與其他三個 AI 設定則與 worker 相同），web 沒有 database、bucket 與 AI key。

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
deploy/railway/deploy.sh --install-cli "$HOME/.local/railway-5.64.2"   # 一次：安裝 pin 住的 CLI
export PATH="$HOME/.local/railway-5.64.2:$PATH"
export RAILWAY_API_TOKEN=…      # 或 RAILWAY_TOKEN=…（見下）；不要放進 shell history 或任何檔案
export GEMINUS_AUTH0_ISSUER='https://<tenant>.<region>.auth0.com/' GEMINUS_AUTH0_AUDIENCE='https://api.geminus-demo.invalid' \
       GEMINUS_AUTH0_CLIENT_ID='<Client ID>' GEMINUS_SEED_OWNER_SUBJECT='auth0|<owner 的 user_id>' GEMINUS_RAILWAY_BUCKET_REGION=sjc
deploy/railway/deploy.sh --dry-run     # 不連網：檢查輸入、工具與部署檔，印出 --apply 的每個命令
deploy/railway/deploy.sh --apply
```

- **Token**（[CLI 文件](https://docs.railway.com/guides/cli)，2026-10-26；CLI v5.64.2 的 `src/config.rs`）：`RAILWAY_TOKEN` 是 project token，project 與 environment 是 token 自己的、必須已存在，腳本不加 `--environment`；`RAILWAY_API_TOKEN` 是 account／workspace token，腳本以名稱找 `GEMINUS_RAILWAY_PROJECT`（預設 `geminus-demo`），沒有就 `railway init`（`GEMINUS_RAILWAY_WORKSPACE` 選 workspace），再 link `GEMINUS_RAILWAY_ENVIRONMENT`（預設 `production`）。兩個都設時 CLI 以 `RAILWAY_TOKEN` 決定 project。Token 只由 CLI 從環境讀：不在任何引數、輸出或 log 中；`deploy.sh` 在 xtrace（`set -x`）開著時拒絕執行。
- **CLI 的版本**：pin 在 5.64.2。`--install-cli` 下載 GitHub release 的 `railway-v5.64.2-x86_64-unknown-linux-musl.tar.gz` 並核對 SHA-256 `a154c3ff5ac3e2ece99bdca9d8d5505fc7f85a949689346bd934fb425246e229`（2026-10-26 計算；release 沒有 checksum 檔，npm 的 `@railway/cli@5.64.2` 也下載同一個檔而不核對）；只支援 Linux x86_64，其他平台自行安裝同一版本。`--apply` 檢查 `railway --version` 為 5.64.2，並以 `CI=true`、`RAILWAY_NO_AUTO_UPDATE=1` 執行，CLI 不會自行更新。
- **需要**：python3、git、psql 15 以上（`\getenv`）、go（`go run ./cmd/provision-tenant`）。Checkout 有未 commit 的修改時拒絕（`railway up` 上傳 working tree；`GEMINUS_ALLOW_DIRTY=1` 接受）。
- **步驟**（`railway_deploy.py` 的 docstring；每步都先查、只在缺少時建立，所以可以重跑）：
  1. project 與 environment；
  2. PostgreSQL、bucket（`geminus-objects`，region 建立後不能改）、四個 service、各自的 config-as-code 檔（`railway environment edit --service-config <service> configFile /deploy/railway/<service>.railway.json`）、api 與 web 的公開網域；
  3. `variables.json` 的變數（`--skip-deploys`）：產生的 secret 只在缺少時以 Python 的 `secrets` 產生，經 `railway variable set KEY --stdin` 設定；bucket 的 key 以同樣方式複製；owner 的變數略過；
  4. 為本次執行開 Postgres 的 TCP proxy（已經有一個時沿用它，結束時也不移除：那是別人開的），以 administrator 的 `DATABASE_PUBLIC_URL`（`sslmode=require`，只放在 psql 的環境）執行 `db/bootstrap_roles.sql` 與 `deploy/sql/create_logins.sql`；login 已存在而 Railway 的密碼還讀得到時以 `ALTER ROLE … PASSWORD` 對齊；然後把產生與複製的 secret 設為 sealed（`variables.<KEY>.isSealed`）；
  5. 部署 `migrate`，等到 `geminus_migrations.applied` 的筆數等於這個 checkout 的 migration 數；
  6. worker 還沒有 `WORKER_ORGANIZATION_IDS` 時 seed 一個 synthetic organization（`GEMINUS_SEED_ORG_NAME`，owner 為 `GEMINUS_SEED_OWNER_SUBJECT`），把 `organization_id` 設給 worker；移除本次開的 TCP proxy（失敗時也移除）；
  7. `railway up` api、worker、web，等每個成功（api、web 有 Railway 的 healthcheck）；
  8. verify：api 的 `/healthz`、`/readyz` 200、`/v1/me` 401，web 的 `/` 200、`/auth/login` 302／303 到 `<issuer>authorize`，worker 仍為 SUCCESS，owner 的必填變數存在（只看名稱）；印出 Auth0 要填的 callback 與 logout URL。
- **結果**：exit 0 完成；exit 1 某一步失敗（訊息寫是哪一步與怎麼做，例如 `the owner has not set BFF_OIDC_CLIENT_SECRET on web (Railway → web → Variables)`，owner 填好後重跑即可）；exit 2 用法錯誤。第一次執行在 owner 填 `BFF_OIDC_CLIENT_SECRET` 之前必定停在 verification：這是預期的。
- **Secret 的產生**：H-032 希望用 Railway 的 `${{secret(...)}}`。Railway 文件只把它寫成 **template** 的函式（[templates/create](https://docs.railway.com/templates/create)：「When a template is deployed, all template variable functions are executed」）；[variables](https://docs.railway.com/guides/variables) 與 [variables reference](https://docs.railway.com/variables/reference) 沒有說一般 service variable 可以用；社群的回答（[station](https://station.railway.com/questions/secret-generates-a-new-string-on-8523cc4d)）說在 service 層級它可能每次重新產生。所以腳本不用它，改在本機產生、經 stdin 設定、再 sealed；產生的值不出現在任何輸出、檔案或引數中（每次呼叫前檢查）。
- **未查證：reference 到 sealed variable 時是否展開。** `DATABASE_URL` 與 `GEMINUS_MIGRATE_DATABASE_URL` 以 `${{GEMINUS_…_DB_PASSWORD}}` 引用 sealed 的密碼；Railway 文件沒有寫 reference 是否展開 sealed 的值（社群有一例 sealed 的 reference 仍可用，但不是文件）。若不展開，步驟 5 的 migrate 會以密碼錯誤失敗，腳本停在那裡並指向這裡。**復原**：在 Railway 的 UI 刪除 migrate、api、worker 的 `GEMINUS_*_DB_PASSWORD`（sealed 的可以刪），以 `GEMINUS_RAILWAY_SEAL=off deploy/railway/deploy.sh --apply` 重跑：腳本重新產生密碼、以 `ALTER ROLE` 對齊、不 sealed（保護較弱：可在 UI 與 `railway variable list` 看到），並回報 coordinator（§10 問題 5）。
- **Rehearsal**：`scripts/rehearse_railway_deploy.py` 以 `railway`、`psql`、`go` 的替身（不連網）執行完整的 `--apply` 數次：secret 只經 stdin、不出現在引數與輸出（包括 CLI 錯誤訊息中引用的值）、不設 owner 的變數、API 沒有 AI key、部署順序、只 seed 一次、TCP proxy 被移除、產生與複製的 secret 都 sealed（`GEMINUS_RAILWAY_SEAL=off` 時都不）、第二次執行不建立任何東西也不換 secret、owner 未填時與登入導向錯誤時 verification 分別失敗。它證明腳本自身一致，不證明 Railway 接受每個呼叫（NOT RUN，直到 coordinator 的 `--apply`）。

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

- 未涵蓋：container scan、SBOM、image 簽章；CSP 與 security header；備份與還原演練（`make db-restore-test` 仍為 NOT IMPLEMENTED）；log／telemetry 的保存與存取；告警（T-036）；孤兒 object 的清理（D-044）；花費預留的 operator 介面（T-053 I4 (2)）；live eval 的花費紀錄要不要改經 DB ledger（T-053 I4 (4)，owner 選擇）；T-054 I1 的一般做法與 I2（S3 adapter 已不把自己的逾時包成 context 的錯誤，`TestS3OwnTimeoutIsNoContextError`；object storage 逾時時的 503 標記仍未區分）。
- 問題：(1) 是否接受 ADR-029；(2) web → api 用公開網域還是 private network（需要放寬 BFF 的 http 規則）；(3) bucket 的 region 與資料保存期限；(4) API 的 key-free AI 可用性由 T-055 一併做，或另開任務；(5) Railway 是否展開 reference 到 sealed variable 的值（§7.7；第一次 `--apply` 會知道）；(6) `${{secret()}}` 不用於 service variable、改由腳本產生，是否接受（H-032 的偏好）；(7) PostgreSQL 的 major 版本（Railway 文件沒寫；migration 只在 16 上測過）。
