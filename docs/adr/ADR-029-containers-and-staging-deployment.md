# ADR-029 — Containers、migration step 與 S3 相容 object storage（T-039 hackathon 切片）

狀態：**PROPOSED**（2026-10-26，T-039 agent 提出並實作；待 owner 與獨立審查）。依據：T-039 的 scope、H-029 (3)（影片＋公開網址：Docker＋managed Postgres、只放 synthetic 資料、owner 建立的 Auth0 tenant）、owner 的雲端選擇（coordinator 轉達，H-030：先為「railway + cloudflare」，同日改為「純用 railway」）、H-032（owner 提供 Railway token 給之後的 session；coordinator 以非互動的腳本部署）、D-044（object storage 的介面；正式 adapter 待 owner 選雲端後另立 ADR）、D-042（provisioning login）、ADR-017、ADR-018、ADR-024、ADR-028、AGENTS.md §2–§7。本 ADR 不批准任何 PROPOSED 決策，也不部署任何東西。

## Context

- P0 的部署單位是 web、api、worker、managed PostgreSQL、object storage、OTLP backend（ARCHITECTURE §8）。T-039 的 hackathon 切片要一個公開網址：Railway 執行三個 Docker service 與 managed PostgreSQL。
- API 與 worker 都讀寫 object storage（upload、dataset snapshot、trace）。唯一的 adapter（`FS`）只給 development／test（D-044：`APP_ENV=production` 拒絕），而 Railway 的 volume 屬於單一 service，API 與 worker 無法共用檔案。Railway 提供 S3 相容的 Storage Buckets（private、Tigris，2026-10-26 查證，見 [deploy runbook](../runbooks/deploy.md) §7.4）。
- 沒有正式的 migration 工具：`scripts/run_db_tests.sh` 只對一次性 `dt_test_*` database 跑 up／down。
- API 沒有不需 token 的 health endpoint，而平台（Railway 的 healthcheck）要求 2xx。

## Decision

### Images（`Dockerfile.api`、`Dockerfile.worker`、`apps/web/Dockerfile`）

- 一律從 repository root 建置；multi-stage；base image 以 digest pin（ADR-011 的表）；runtime 為非 root（Go：distroless static `nonroot`，uid 65532；web：Node 的 `node`，uid 1000）；不宣告任何 `ARG`（Railway 只把宣告為 `ARG` 的變數傳進 build，見 runbook §7）；沒有 credential 進 image；`.dockerignore` 排除 git、env 檔、node_modules、build output、checks、sources。
- API image 另含兩個 operator 命令：`geminus-migrate` 與 `geminus-provision-tenant`（各自的 login，API service 永遠拿不到），與 `/migrations/*.up.sql`。
- Web：Next 的 standalone server（只有 image 的 build 設 `GEMINUS_NEXT_STANDALONE=1`；`make frontend-build` 與瀏覽器檢查不變），runtime 移除 npm、corepack、yarn。
- 健康檢查：API 在 `/v1` 之外提供 `GET /healthz`（process 活著）與 `GET /readyz`（2 秒內 database 回應），不需 token、只回 `ok`／`ready`／`unavailable`、不進 access log；`geminus-api -probe` 是 image 的 `HEALTHCHECK`（image 沒有 shell 或 HTTP client）。它們不是 contract 的 operation（`/v1` 的 operation 都在一個 organization 中執行，`scripts/check_contracts.py`），所以不在 `openapi/openapi.yaml`。Worker 沒有 HTTP listener，平台看 process。Web 以 `/` 檢查。
- 停止：API 收到 SIGTERM 後停止接受、完成進行中的 request（20 秒）；worker 停止 handler 並記錄 attempt（ADR-024）；Next 的 server 收到 SIGTERM 即結束（不 drain）。

### Migration step（`cmd/migrate`、`internal/storage/migrations`）

- `geminus-migrate up|status`，只讀 `GEMINUS_MIGRATE_DATABASE_URL`（從不讀 runtime 的 `DATABASE_URL`）。Login 必須能以 twin_owner 行事、不屬於 twin_api／twin_worker／twin_provisioner、沒有 SUPERUSER、BYPASSRLS、CREATEROLE、CREATEDB、REPLICATION（superuser 是每個 role 的成員，因此也被拒絕）。
- 每個 migration 與它的紀錄（`geminus_migrations.applied`：version、檔名、檔案的 SHA-256、時間、login；owner twin_owner，PUBLIC 無權限）在同一個 transaction；檔案本身有 `BEGIN;…COMMIT;` 時紀錄插在它的 `COMMIT;` 之前，沒有時（0003）整個檔案包在一個 transaction 中；檔案中其他位置的 transaction 指令一律拒絕。Advisory lock 使並行的兩次執行輪流進行。紀錄與本 build 的檔案不符（已套用的檔案被改過、資料庫比 build 新、有 twin schema 卻沒有紀錄）時拒絕、不修補。
- 規則 `migrations-only-in-their-command`：只有 `cmd/migrate` 能連結 runner（API、worker、storage 的 probe）。
- Login 的建立：`deploy/sql/create_logins.sql`（psql 15+，密碼只從環境變數 `\getenv`），在 `db/bootstrap_roles.sql` 之後由 administrator 執行一次：`geminus_migrator`（twin_owner）、`geminus_api`、`geminus_worker`、`geminus_provisioner`（NOINHERIT，D-042），並給 twin_owner 該 database 的 CREATE。

### Object storage：S3 相容 adapter（`internal/storage/objects/s3.go`）

- `OBJECT_STORAGE_PROVIDER=s3`，任何 `APP_ENV` 都接受；`OBJECT_STORAGE_S3_ENDPOINT`（https origin，plain http 只在 loopback）、`_BUCKET`（DNS label）、`_REGION`、`_ACCESS_KEY_ID`、`_SECRET_ACCESS_KEY`（secret，印出為 `[redacted]`）、`_URL_STYLE`（`virtual-hosted` 預設或 `path`）。整組設定或完全不設：provider 不是 s3 時任何 `OBJECT_STORAGE_S3_*` 都拒絕啟動。
- 與 FS 相同的 `Store` 契約：key 只接受 server 產生的（D-035）；Put 先把 bytes 暫存在 `/tmp`（至多 max，超過時不送出），以 SHA-256 簽章（`x-amz-content-sha256`）並帶 `If-None-Match: *` 送出，所以既有 object 永不被取代（412 → `ErrExists`；之前先 HEAD，常見情況不必送出 bytes）；Get 串流；Delete 只給 maintenance。Bucket 是 private：每個 request 都簽章，不產生 presigned URL（D-044：API 授權後串流），不跟隨 redirect。錯誤只帶操作、HTTP status 與 service 的 error code。
- SigV4 自行以標準函式庫實作（約 150 行），不引入 SDK：以 AWS 公布的 test suite（15 個向量）驗證 canonical request、string to sign 與 Authorization；in-process 的假 S3 server（不連網）以同一套簽章驗證每個 request、核對 payload hash、模擬條件式 PUT。17 個 mutant 全部被測試擋下。
- 對真實 bucket 的條件式 PUT 是否成立，只能由 owner 以 bucket 的 credential 執行 `TestLiveBucket`（runbook §7.4）確認；未確認前不得以它保存任何非 synthetic 的資料。

### Railway 的自動化（`deploy/railway/deploy.sh`，H-032）

- `deploy.sh --apply` 以 pin 住的 Railway CLI 5.64.2（`--install-cli` 核對 release archive 的 SHA-256；`CI=true`、`RAILWAY_NO_AUTO_UPDATE=1` 使它不自行更新）完成 runbook §7.1–§7.6：project 與 environment、PostgreSQL、bucket、四個 service 與各自的 config-as-code 檔、公開網域、`variables.json` 的變數、login、migrate、seed、api／worker／web、verification；每一步先查、只在缺少時建立，可重跑。`--dry-run` 不連網，檢查輸入、工具與部署檔並印出每個命令。
- Token 只由 CLI 從環境讀（`RAILWAY_TOKEN`：project token；`RAILWAY_API_TOKEN`：account／workspace token）；腳本在 xtrace 開著時拒絕執行，每次呼叫前檢查引數中沒有 secret，輸出中已知的 secret 一律替換為 `[redacted]`，CLI 中含 secret 的輸出（variable list、bucket credentials）只解析不印出。
- 產生的 secret（三個 login 的密碼、`SESSION_KEYS`）在本機以 Python 的 `secrets` 產生，經 `railway variable set KEY --stdin` 設定後 sealed；不用 Railway 的 `${{secret()}}`：Railway 文件只把它寫成 template 部署時的函式，沒有寫一般 service variable 可用（runbook §7.7）。Provisioning login 的密碼每次執行新產生、只在該次 seed 的環境中。Owner 的變數（`BFF_OIDC_CLIENT_SECRET`、AI key、`GEMINUS_AI_LIVE`）腳本不設也不讀，只在 verification 檢查必填者的名稱存在。
- Database 的 bootstrap 經本次執行暫開、結束時移除的 TCP proxy（`sslmode=require`）。Migration 不用 pre-deploy command（會把 migration login 交給 API service），而是部署獨立的 `migrate` service，等 `geminus_migrations.applied` 的筆數等於 checkout 的 migration 數之後才部署 api 與 worker。
- `scripts/rehearse_railway_deploy.py` 以 `railway`、`psql`、`go` 的替身離線執行完整的 `--apply`（12 個 mutant 全部被擋下）；它不證明 Railway 接受每個呼叫。未查證：reference 到 sealed variable 時是否展開（runbook §7.7 的復原：`GEMINUS_RAILWAY_SEAL=off`）。

## Consequences

- D-044 的「正式 adapter」有了實作，但加密金鑰、retention、region、備份仍是 bucket 供應者的預設、未經 owner 決定：只放 synthetic 資料（SECURITY §6）。
- Put 需要暫存空間：API 的 upload 至多 50 MB、worker 的 canonical dataset 至多 256 MiB，寫在 container 的 `/tmp`。
- Railway 的 bucket 只能經公開網路存取（Railway 文件），service 上傳到 bucket 算 service egress。
- 部署順序（migrate → api／worker → web）由 `deploy.sh` 或手動部署的人保證；平台不強制。
- `deploy.sh` 的 CLI 命令與 JSON 形狀依 CLI v5.64.2 的原始碼；CLI 升級前要重新核對並更新 pin 與 rehearsal 的替身。
- 若 owner 不採用：移除 `s3.go`、`sigv4.go`、config 的 `s3` 分支（回到 not implemented）、`cmd/migrate` 與 `internal/storage/migrations`（以其他工具執行 migration 時，必須同樣以 migration login、絕不以 runtime login）、health probes 與 `deploy/`。

## 待 owner 決定

1. 是否接受本 ADR（S3 adapter、自行實作 SigV4、migration 紀錄的 schema `geminus_migrations`、health probes 不進 OpenAPI）。
2. BFF 經 API 的公開網域（https）呼叫 API；Railway 的 private network 是 plain http（WireGuard 加密），BFF 目前只在全部 URL 都是 loopback 時才接受 http。是否允許 BFF 以 http 連 private network 的 API（需要改 BFF 的檢查並另行審查），或維持公開網域。
3. Bucket 的 region（建立後不能改）與資料保存的期限。
4. Secret 由 `deploy.sh` 在本機產生並 sealed（不用 `${{secret()}}`），是否接受；若第一次 `--apply` 發現 Railway 不展開 reference 到 sealed variable 的值，是否接受 `GEMINUS_RAILWAY_SEAL=off`（密碼可在 Railway 的 UI 看到）或改為把整個 database URL 存成 sealed variable（需要修改腳本與檢查）。
