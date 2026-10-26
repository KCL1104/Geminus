# T-039 驗證報告：containers 與 staging 參考部署（hackathon 切片，H-029 (3)）

**日期：2026-10-26｜Branch：`t039-hk`（從 6041d39 開出，沒有 push）｜狀態：hackathon 切片已交付；T-039 在 tasks.json **保持 `not_started`**（只加了 completion_evidence）：brief 要求設為 `in_progress`，但依賴的 T-043 仍為 `in_progress`，而 `scripts/check_decisions.py` 規定 `in_progress` 的任務其 dependencies 必須是 `review` 或 `done`（設了之後 `make spec-check` exit 2，`spec-check-in-progress.log`），不放寬檢查，由 coordinator 決定；沒有部署任何東西、沒有建立任何帳號｜需求：REQ-020｜Gate：D-093、D-095（APPROVED，H-024，本任務只沿用）；新的 ADR-029 為 PROPOSED｜驗收測試：TEST-030（本機 staging smoke：NOT RUN，沒有 Docker daemon；process-level smoke 通過）**

雲端：owner 的選擇由 coordinator 轉達：先為「railway + cloudflare」（H-030），同日改為「先不要接 cloudflare 好了，純用 railway 就好」（H-031；review 第 1 輪 I4 更正：「純 Railway」的出處是 H-031，不是 H-030）。所以本切片是 Railway（web、api、worker、managed PostgreSQL、Storage Bucket），使用 Railway 的網域與 TLS；Cloudflare／R2 的部分沒有做。之後（H-032，coordinator 轉達）：owner 提供了 Railway token（只在新的 session 中），coordinator 之後會部署；本切片因此加上以 Railway CLI 非互動套用的 `deploy/railway/deploy.sh`。**本 session 沒有執行 `--apply`、沒有呼叫 Railway 的 API、沒有建立任何東西**（auto mode 也拒絕了一次不帶 token 的 `--apply` 嘗試，那個嘗試本來只會在 preflight 失敗；之後只執行 `--dry-run` 與離線 rehearsal）。

**沒有 Docker daemon**（本機只有 docker CLI 29.8.2 與 compose v5.6.0）：`make build-containers` 與 `make staging-smoke` 依設計以 exit 2 回報 NOT RUN（`checks/evidence/2026-10-26-T-039/build-containers.log`、`staging-smoke.log`），沒有嘗試任何 privileged workaround。替代的驗證：以 Dockerfile 的 build context（只有 `go.mod`、`go.sum`、`cmd`、`internal`、`schemas`、`prompts`）與相同的 flags 建置四個 static binary，在一次性 database 上執行與 compose smoke 相同的步驟（process smoke，全部通過）；web 以 `GEMINUS_NEXT_STANDALONE=1` 建置 standalone server 並如 image 一樣啟動與停止；`docker compose config` 驗證 compose 檔；Railway 的 config 檔以 Railway 公布的 JSON schema 驗證。CI 的新 job `containers` 會在 GitHub runner 上真正建置三個 image 並執行 `make staging-smoke`：**尚未觀察到它的結果**（沒有 push）。

逐條輸出在 `checks/evidence/2026-10-26-T-039/`。

## 交付

| 項目 | 檔案 | 內容 |
|---|---|---|
| Images | `Dockerfile.api`、`Dockerfile.worker`、`apps/web/Dockerfile`、`.dockerignore` | Multi-stage；base 以 digest pin（`image-digests.txt`、ADR-011 的表）；runtime 非 root（distroless static uid 65532；Node 的 `node` uid 1000）、沒有 shell（Go）或 npm（web）；沒有 `ARG`、沒有 credential 的 `ENV`；`HEALTHCHECK`（`geminus-api -probe`；web 以 node 的 fetch）；`STOPSIGNAL SIGTERM`。API image 另含 `geminus-migrate`、`geminus-provision-tenant` 與 `/migrations/*.up.sql`。Web 為 Next 的 standalone server（`apps/web/next.config.ts`：只有 `GEMINUS_NEXT_STANDALONE=1` 時） |
| Health probes | `cmd/api/main.go` | `GET /healthz`（200 `{"status":"ok"}`）、`GET /readyz`（2 秒內 `pool.Ping` 成功 200 `{"status":"ready"}`，否則 503 `{"status":"unavailable"}`，不說原因）；不需 token、`Cache-Control: no-store`、不進 access log；其他 method 與 path 照舊交給 `/v1` handler；`geminus-api -probe`（image 的 health check） |
| Migration step | `cmd/migrate`、`internal/storage/migrations`、`deploy/sql/create_logins.sql` | 見 ADR-029：只用 migration login（拒絕 superuser、runtime 與 provisioning login），每個 migration 與 `geminus_migrations.applied` 的紀錄同一 transaction，advisory lock，紀錄與檔案不符時拒絕；`up`／`status`（exit 0、2、3、4、5、6）；新的 architecture 規則 `migrations-only-in-their-command`（3 個 probe）。`create_logins.sql`：四個 login，密碼只從環境變數（`\getenv`），至少 32 字元、四個不同、bootstrap 過的 role 才建立 |
| Object storage | `internal/storage/objects/s3.go`、`sigv4.go`、`internal/config`（`OBJECT_STORAGE_PROVIDER=s3`、`OBJECT_STORAGE_S3_*`）、`cmd/api`、`cmd/worker` | S3 相容 adapter（Railway 的 bucket）：SigV4 以標準函式庫實作（沒有新的 module），payload 以 SHA-256 簽章、`If-None-Match: *`（既有 object 不被取代）、先 HEAD、暫存在 `/tmp`、private（不 presign、不跟隨 redirect）、錯誤不含 credential、`[redacted]`；bucket 自己的 timeout 不是 context error（T-054 review 第 2 輪 I1）。設定為整組，provider 不是 s3 時設了任何 `OBJECT_STORAGE_S3_*` 即拒絕啟動 |
| 本機 staging | `deploy/local/compose.yaml`、`deploy/local/staging-smoke.sh`、`Makefile`（`build-containers`、`staging-smoke`、`spec-deploy`；`db-go-test` 加 `./cmd/migrate/...`） | **只給本機**（檔案第一行即標示；含 repository 的測試 OIDC provider）；每次執行產生一次性密碼與 session key；7 組檢查（見 `deploy/local/staging-smoke.sh` 的開頭）；沒有 Docker 時 exit 2 |
| CI | `.github/workflows/ci.yml` | 新 job `containers`：`make build-containers`、`make staging-smoke`；只用既有、以 SHA pin 的 `actions/checkout`；不 push image、不用 registry credential |
| Railway | `deploy/railway/{api,worker,web,migrate}.railway.json`、`deploy/railway/variables.json` | Config-as-code（Dockerfile builder、watch paths、healthcheck、restart policy、`drainingSeconds`、1 replica；`migrate` 的 start command 與 `NEVER`）；每個 service 的變數清單（secret 沒有值而有 `source`：`generated`、`bucket:…`、`owner`；reference 組成 URL；`forbidden` 清單） |
| Railway 的自動化（H-032） | `deploy/railway/deploy.sh`、`deploy/railway/railway_deploy.py`、`scripts/rehearse_railway_deploy.py` | `--dry-run`（不連網）、`--apply`、`--install-cli DIR`（Railway CLI 5.64.2，核對 release archive 的 SHA-256）；支援 `RAILWAY_TOKEN` 與 `RAILWAY_API_TOKEN`；xtrace 時拒絕執行（review 第 1 輪 L1 更正：原本的檢查在 `set +x` 之後，永遠不成立；現已移到最前面）、token 只由 CLI 從環境讀、引數中不放 secret（每次檢查）、輸出中的 secret 替換為 `[redacted]`；每步先查再建立（可重跑）；產生的 secret 以 Python `secrets` 產生、經 stdin、sealed；owner 的變數不設，verification 檢查必填者存在（review 第 1 輪 M2 更正：原本寫「不讀」並不正確，CLI 的變數清單會回傳沒有 sealed 的值；現在腳本收到即丟棄，見文末「Review round 1 fixes」）；8 個步驟見 runbook §7.7。Rehearsal：`railway`、`psql`、`go` 的替身，離線執行 `--apply` 五次（空的 project、重跑、登入導向錯誤兩種、`GEMINUS_RAILWAY_SEAL=off`、CLI 錯誤訊息引用 secret） |
| 靜態檢查 | `scripts/check_deploy.py`（`make spec-deploy`，`make spec-check` 的一部分）、`checks/deploy-check.json` | Dockerfile、`.dockerignore`、compose、Railway 檔與 `variables.json`、runbook 是否列出程式讀的每個變數 |
| 文件 | `docs/runbooks/deploy.md`（§7.7 為 `deploy.sh`）、`docs/adr/ADR-029-containers-and-staging-deployment.md`、ADR-011（base image 表）、THIRD_PARTY_NOTICES（SigV4 test vectors、base images）、ARCHITECTURE §2、§6、§8、SECURITY §6、§9、API_DATABASE §9、`db/README.md`、`.env.example` | 每個變數的來源、必填、secret；migration 順序；Auth0 的 callback／logout URL；Railway 的步驟（官方文件 URL 與取得日期）；owner 的 secret 清單 |
| Test data | `testdata/sigv4/`（15 個向量與 `SOURCE.txt`） | AWS SigV4 test suite（botocore 的鏡像，Apache-2.0，未修改，SHA-256 記錄） |

## 測試先行與 mutation

測試在實作之前寫好（同一個 session；紅的狀態沒有另存 log，以 mutation 證明測試會在缺少各行為時失敗；每個 mutant 套用後執行測試，再還原檔案）：

- **S3／SigV4**（`internal/storage/objects`：`TestSigV4Suite`、`TestSignedHeadersOfARequest`、`TestCanonicalURI`、`TestS3PutGet`（兩種 URL style）、`TestS3ConditionalPut`、`TestS3Errors`、`TestS3ContextAndDelete`、`TestS3OwnTimeoutIsNoContextError`、`TestNewS3`；假 S3 server 以同一套簽章驗證每個 request、核對 payload hash、要求 `If-None-Match: *`）：**17/17 mutant 被擋下**（`mutants-s3.log`：不帶 `If-None-Match`、412 不是 `ErrExists`、跟隨 redirect、沒有大小上限、任何 key、印出 secret、暫存檔留下、沒有先 HEAD、404 不是 `ErrNotFound`、未簽 payload、任何 host 的 http、回顯 error code、不合併空白、query 不排序、不簽 Content-Type、timeout 包住原因、caller 的 deadline 不回 context error）。S13 證實 Go 的 transport timeout 包起來時會符合 `context.DeadlineExceeded`：T-054 I1 指出的問題對這個 adapter 是真的，所以它從不包住原因。最初的兩個 mutant（跟隨 redirect、回顯 error code）存活，因此加強了測試（redirect 的 request 數、含空白的 error code）後重跑。
- **Config**（`TestLoadS3`）、**cmd**（`TestProbes`、`TestProbeCommand`、`TestObjectStore`、`TestTheObjectStoreIsTheConfigured`、`TestRunServesAndStops` 加 probes 與 access log）、**migrations**（`TestLoadTheRepositorysMigrations`、`TestLoadRefuses`；DB：`TestApplyOnAFreshDatabase`、`TestApplyRefusesUnsafeLogins`、`TestApplyRefusesAForeignState`、`TestAFailedMigrationKeepsNothing`、`TestConcurrentRunsTakeTurns`；`cmd/migrate`：`TestRunRefusesBeforeConnecting`、`TestRunMigrates`）：**18/18 DB mutant 被擋下**（`mutants-db.log`，在 `dt_test_t039a` 上；runtime role 的成員、superuser、紀錄在 COMMIT 之後、被改過的 migration、沒有紀錄的 twin schema、資料庫比 build 新、沒有 lock、migrate 讀 `DATABASE_URL`、`status` 恆為 0、probe 進 access log、database 停止時仍 ready、disabled 時忽略 bucket 變數、bucket 旁的 `OBJECT_STORAGE_ROOT`、不乾淨的 secret、BYPASSRLS、CREATEROLE、CREATEDB、REPLICATION）。第一輪 M2（superuser）與 M10（probe 進 access log）存活：superuser 同時是每個 role 的成員，所以仍被拒絕但理由不同；access log 只記 route 的 pattern，不會出現 `healthz` 字樣。因此 `TestApplyRefusesUnsafeLogins` 改為逐一檢查每個 login 被拒絕的理由，並加入一個帶 BYPASSRLS、CREATEROLE、CREATEDB、REPLICATION 的 twin_owner 成員；`TestRunServesAndStops` 改為要求 access log 只有兩行（兩個 `/v1` request），重跑 M2、M10 與新的 M15–M18（`mutants-db.log` 的第二段）。DB 測試在寫好後第一次執行就抓到一個 bug：record 的 table 先建立後，有 twin schema 而沒有紀錄的 database 會被當成空的而再跑 0001（失敗於 `schema "twin" already exists`）；已修正（沒有紀錄且有 twin schema 一律拒絕）。
- **Railway 的部署腳本**（`scripts/rehearse_railway_deploy.py`）：**12/12 mutant 被擋下**（`rehearsal-mutants.log`：secret 當作引數、設定 owner 的變數、第二個 organization、TCP proxy 沒有移除、API 在 migration 之前、每次重新產生 secret、不檢查登入導向、不檢查 owner 的變數、輸出不遮蔽、什麼都不 sealed、忽略 `GEMINUS_RAILWAY_SEAL`、bucket 的 key 不 sealed）。第一輪 R7、R8、R9 存活（第二次執行只看結果、第一次執行的失敗訊息也因登入導向而提到 client secret、替身從不把 secret 印出），因此 rehearsal 加上：owner 檢查的訊息本身、owner 已填但登入導向錯誤（503 與導向別的 host）時必須失敗、CLI 在錯誤訊息中引用 stdin 的值時輸出必須是 `[redacted]`；之後加入 R10–R12 與 sealed 的檢查。
- **部署檔的靜態檢查**：**17/17 mutant 被擋下**（`deploy-mutants.log`：未 pin 的 base、root、`ARG`、`ENV` 中的 credential、沒有 HEALTHCHECK、API 有 AI key、secret 有值、web 有 database、Railway 檔提到 loopback、compose 的明文密碼、拿掉 local-only 標示、runbook 少一個變數、worker 少 `WORKER_ID`、production 用 fake provider、`.env.*` 進 build context、URL 中的明文密碼、沒有程式讀的變數）。

## 實際執行結果

（Exit code 也在 `exit-codes.txt`。Go 1.27.3、Node 22.22.0（`.nvmrc` 為 24.21.0：以 `npm ci --engine-strict=false` 安裝，與 T-045、T-054 相同；image 用 24.21.0）、4 CPU 與其他 agent 共用。DB：共用的一次性 cluster（PostgreSQL 16.15，trust 認證），本任務只建立並刪除 `dt_test_t039*`、`dt_test_mig_*` database。）

從 `make backend-test` 到 `make frontend-build` 的命令在最後的程式上依序執行，沒有同時進行的 mutation（`exit-codes.txt` 依執行順序列出，包括第一次失敗的兩項）；部署檔的 mutation 在那之後、同樣在最後的檔案上。S3 與 DB 的 mutation、rehearsal 的 mutation、process smoke 與 web standalone 是本 session 較早的執行（DB 第二輪與 rehearsal 的 mutation 在最後的程式上；process smoke 與 web standalone 之後沒有在最後的程式上重跑）。

| 命令 | Exit | 結果（evidence） |
|---|---|---|
| `make backend-test` | 0 | gofmt、`go vet`、`go test`（`GEMINUS_FULL_DIFFERENTIAL=1`）：76 個 package ok，其中 71 個由 go test 的 cache 回答（相同的輸入）；`backend-test.log`。本 task 改過的 package 另以 `go test -count=1` 重跑：objects、migrations、config、cmd/...、check-architecture 全部 ok（`changed-packages-uncached.log`） |
| `make spec-check`（第一次） | 2 | `check_decisions.py`：`T-039: started before its dependencies`（T-039 設為 `in_progress` 而 T-043 仍為 `in_progress`）；`spec-check-in-progress.log`。T-039 因此保持 `not_started`（見開頭） |
| `make spec-check`（最後） | 0 | 包括 `spec-deploy`（`PASS_STATIC_DEPLOY_CHECKS`）；重新產生的 `checks/docs-check.json`、`checks/deploy-check.json` 已 commit；`spec-check.log` |
| `make architecture-check` | 0 | `PASS architecture: 88 packages, 24 rules, 16 build configurations`（含新的 `migrations-only-in-their-command`）；`architecture-check.log` |
| `python -I scripts/check_deploy.py` | 0 | `check-deploy.log` |
| `python scripts/rehearse_railway_deploy.py` | 0 | `rehearsal: PASS (the deployer against stand-ins; Railway itself NOT RUN)`；run 1：91 個 CLI 呼叫、7 個 psql、停在 verification（owner 未填 client secret）；run 2：done、沒有建立任何東西；run 3：兩種登入導向錯誤都失敗；run 4：CLI 錯誤訊息中的 secret 為 `[redacted]`；run 5：`GEMINUS_RAILWAY_SEAL=off` 不 sealed；`rehearsal.log` |
| `deploy/railway/deploy.sh --dry-run` | 0 | 不連網；列出 `--apply` 需要的輸入與 token、8 個步驟的每個命令；`deploy-dry-run.log` |
| `deploy/railway/deploy.sh`（沒有引數） | 2 | 用法 |
| `bash -n`（每個 script 單獨）、Python 的 `ast.parse` | 0 | `bash-n.log`（第一次把兩個檔案交給同一個 `bash -n`，只檢查了第一個，因此逐一重跑） |
| `make build-containers` | 2 | **NOT RUN**：`no Docker daemon is reachable (docker info failed); no image was built.`；`build-containers.log` |
| `make staging-smoke` | 2 | **NOT RUN**：同上；`staging-smoke.log` |
| `make integration-test`（第一次，`dt_test_t039c`） | 2 | 只有 `internal/worker` 的 `TestAnIdleWorkerPauses` 失敗（`2 transactions in a second of idling with a 200 ms pause, want about 10`：`-race` 下、與其他 agent 共用 4 CPU 時的計時測試；本 task 沒有改 `internal/worker`）；其餘 package 全部 ok；`integration-test-pass1.log` |
| `make integration-test`（第二次，新的 `dt_test_t039d`） | 0 | `make db-test`（SQL 測試與 migration）與 `make db-go-test`（`-race -count=1 -p 1`，含 `./cmd/migrate/...`、`./internal/storage/...` 的 migrations、cmd/api 的 probes）全部 ok；`integration-test.log`。兩個 database 都已刪除 |
| `make frontend-test` | 0 | lint、typecheck、unit：21 個檔案、547 個測試通過；`frontend-test.log` |
| `make frontend-build` | 0 | `next build`（沒有 `GEMINUS_NEXT_STANDALONE`：一般的輸出）；`frontend-build.log` |
| Mutation：S3／SigV4 | 17/17 | `mutants-s3.log` |
| Mutation：DB（migrations、cmd/migrate、cmd/api、config） | 18/18 | `mutants-db.log`（第一輪 14 個中 M2、M10 存活，加強測試後第二輪擋下，並加入 M15–M18） |
| Mutation：部署檔的靜態檢查 | 17/17 | `deploy-mutants.log`（最後的檔案上重跑；之後 `restored files: PASS`） |
| Mutation：Railway 部署腳本 | 12/12 | `rehearsal-mutants.log` |
| Process smoke（Docker build context 的 binaries，`dt_test_t039b`） | 0 failures | `process-smoke.log`：create_logins 的拒絕與建立、migration login 以外不能 migrate、`up` 兩次、seed、API 拒絕 migration login、`/healthz`、`/readyz`、401、`-probe`、worker、SIGTERM；`gobuild-context.log`：static、stripped |
| Web standalone（如 image 啟動） | — | `web-standalone.log`：`/` 200、`/auth/login` 303（PKCE）、`/api/v1/me` 401、SIGTERM 143（不 drain） |

Database：本任務建立的 `dt_test_t039a`、`dt_test_t039b`、`dt_test_t039c`、`dt_test_t039d` 與測試中的 `dt_test_mig_*` 都已刪除；`geminus_*` 與 `dt_test_migrator_api_login`、`dt_test_migrator_attributes_login` 不存在；`dt_test_migrator_login` 與其他 dbtest login 一樣保留。

## NOT RUN

| 項目 | 原因 |
|---|---|
| `make build-containers`（exit 2）、`make staging-smoke`（exit 2） | 沒有 Docker daemon；替代驗證見上方。CI 的 `containers` job 尚未在 GitHub 上執行過 |
| 在 container 中執行（image 的 HEALTHCHECK、`docker compose` 的 `network_mode: service:web`、volume 掛載、`--wait`、image 中的檔案權限） | 同上；distroless 的內容（`/tmp` 1777、uid 65532、CA bundle）以 registry 的 layer 確認（`distroless-contents.txt`） |
| Node 24.21.0 的 web build | 本機只有 Node 22；image 用 24.21.0 |
| `TestLiveBucket`（真實 bucket 的條件式 PUT） | 沒有 credential；由 owner 執行（runbook §7.4），通過前不得使用 bucket |
| `deploy/railway/deploy.sh --apply`、任何 Railway API 呼叫、Railway 上的資源 | H-032：由 coordinator 之後在有 token 的 session 執行；本 session 只跑 `--dry-run`（不連網）與 rehearsal（替身）。Rehearsal 證明腳本自身一致，不證明 Railway 接受每個 CLI 呼叫 |
| Railway、Auth0 的 staging 檢查（runbook §8、Auth0 runbook §5） | 需要 owner 的帳號與部署；agent 不部署 |
| `deploy.sh --install-cli` | 會下載 CLI；SHA-256 是先前以下載的 archive 計算的（`sources.txt`），安裝本身沒有執行 |
| hadolint、actionlint、shellcheck | 沒有安裝（依指示不安裝新工具）；`bash -n`、`docker compose config`、Railway schema、`scripts/check_deploy.py` 代替一部分 |
| Container scan、SBOM、image 簽章 | 沒有選定工具（SECURITY §9） |
| `make e2e-test` | 只有 `next.config.ts` 的條件式 standalone 改動；`make frontend-test`、`make frontend-build` 已跑 |

## T-039 的 scope 逐條

| Scope | 判定 |
|---|---|
| web／api／worker 分開的 container，managed DB／object／OTLP adapter | Containers：已完成（container 中的執行 NOT RUN）。Object：S3 相容 adapter 已完成（真實 bucket NOT RUN）。OTLP：既有的 adapter（ADR-028），backend 未選，部署不設 |
| 非 root／runtime roles、secret injection、readiness、graceful shutdown | 已完成：image 非 root；四個 login 與 migration login 的分離（process smoke：API 拒絕 migration login、runtime login 不能 migrate）；secret 只在平台；`/readyz`；SIGTERM（process smoke：API、worker exit 0；Next exit 143，不 drain） |
| cloud／provider 不進 domain code，production 人工 gate | 已完成：Railway 只在 `deploy/railway/` 與 runbook；公開與開 live AI 由 owner 決定 |
| web container 為 Node server（BFF），session key 與 client secret 由 secret store 注入 | 已完成（Railway 的 sealed variable） |
| T-054 review 第 2 輪 I1、I2 | I1：對 S3 adapter 以構造解決（自己的 timeout 從不是 context error，測試與 mutant S13、S14）；`datasets.Failure` 標記「request 已結束」的一般做法**未做**（目前沒有其他會自己逾時的 store）。I2：worker 沒有改用 `StorageFailure`，**未做**（不適用） |
| T-053 review 第 1 輪 I2、I4、I5、D-114 (b) | (1) 每個 worker 相同的限額：runbook §4.4（1 個 replica、同一組變數）；(2) 未結算預留的 operator 介面：**未做**；(3) API 不設 AI key：`variables.json` 的 `forbidden` 與檢查，但 **API 在開關 on 時仍要求 key**，key-free 的可用性判斷需要改 `LoadAI`（**未做**，建議由正在改 `LoadAI` 的 T-055 一併完成，否則部署的 API 無法接受 live AI 的 proposal）；(4) D-114：owner 的選擇，未決 |
| Hackathon 切片：部署步驟、synthetic 資料、Auth0、secret 由 owner 填入 | 步驟已完成（runbook、`variables.json`、`deploy.sh`）；部署本身由 coordinator 以 `deploy.sh --apply` 或 owner 手動執行（NOT RUN）；owner 的 secret 只由 owner 填 |

## 發現

1. API 原本沒有不需 token 的 health endpoint，而 Railway 的 healthcheck 要求 2xx：新增 `/healthz`、`/readyz`（不在 `/v1` 的 contract：`check_contracts.py` 要求每個 operation 都有 organization selector 與 role；ADR-029 請 owner 確認）。
2. 沒有正式的 migration 工具（`run_db_tests.sh` 只給 `dt_test_*`）：新增 `cmd/migrate`。Railway 的 pre-deploy command 會把 migration login 交給 API service，所以改為獨立的 `migrate` service。
3. FS adapter 在 production 拒絕啟動，Railway 的 volume 屬於單一 service：需要 bucket，Railway 有 S3 相容的 Buckets（Tigris），所以實作 S3 adapter；Railway 文件沒有寫條件式 PUT（Tigris 文件有），所以在用之前要以 `TestLiveBucket` 確認。
4. T-053 I5 的現況與部署矛盾：API 不拿 key 就無法在 live 開啟時接受 AI proposal（見上表）。
5. BFF 只在全部 URL 都在 loopback 時接受 http，Railway 的 private network 是 plain http：web → api 經 API 的公開 https 網域（runbook §7.6，owner 問題 2）。
6. CSP 與其他 security header 尚未設定；Next 的 server 在 SIGTERM 時不 drain 進行中的 request。
7. 本機 composition 無法完成真正的登入：測試 provider 的 access token 不是 API 驗證的 JWT，API 也只接受 https 的 issuer；登入的 closed loop 只能在 staging 由人檢查。
8. H-032 希望產生的 secret 用 Railway 的 `${{secret(...)}}`：Railway 文件只把它寫成 template 部署時的函式，沒有寫一般 service variable 可用，社群回答說在 service 層級可能每次重新產生（runbook §7.7、`sources.txt`）。所以 `deploy.sh` 在本機以 Python 的 `secrets` 產生、經 stdin 設定、再 sealed。
9. Railway 文件沒有寫 reference（`${{GEMINUS_API_DB_PASSWORD}}`）到 sealed variable 時是否展開；若不展開，第一次 `--apply` 會停在 migrate 並指向 runbook §7.7 的復原（刪除密碼變數、`GEMINUS_RAILWAY_SEAL=off` 重跑）。
10. Railway CLI 會自行更新：`deploy.sh` 以 `CI=true`、`RAILWAY_NO_AUTO_UPDATE=1` 執行並檢查版本為 5.64.2；CLI 的 release 沒有 checksum 檔，pin 的 SHA-256 是本 session 計算的。

## Owner 的清單

帳號（owner 建立，agent 不代做）：Railway 帳號與 token（H-032 已提供；`RAILWAY_API_TOKEN` 可建立 project，`RAILWAY_TOKEN` 需要 project 已存在）；Auth0 tenant（API 與 Regular Web Application，[Auth0 runbook](../docs/runbooks/auth0.md)）；（T-055 合併後）Meta Model API key。Project、PostgreSQL、Bucket、四個 service、網域由 `deploy.sh --apply` 建立（或照 runbook §7.1–§7.6 手動）。

`deploy.sh` 的輸入（不是 secret）：`GEMINUS_AUTH0_ISSUER`、`GEMINUS_AUTH0_AUDIENCE`、`GEMINUS_AUTH0_CLIENT_ID`、`GEMINUS_SEED_OWNER_SUBJECT`（owner 的 Auth0 user_id）、`GEMINUS_RAILWAY_BUCKET_REGION`（sjc、iad、ams、sin，建立後不能改）。

Secret：`deploy.sh` 產生並 sealed 的：`GEMINUS_MIGRATOR_DB_PASSWORD`（migrate）、`GEMINUS_API_DB_PASSWORD`（api）、`GEMINUS_WORKER_DB_PASSWORD`（worker）、`SESSION_KEYS`（web）；從 bucket 複製並 sealed 的：`OBJECT_STORAGE_S3_ACCESS_KEY_ID`、`OBJECT_STORAGE_S3_SECRET_ACCESS_KEY`（api、worker）；`GEMINUS_PROVISIONER_DB_PASSWORD` 每次執行新產生、不存任何地方。**Owner 在 Railway 的 UI 填並立刻 sealed（腳本不設；CLI 的變數清單會回傳沒有 sealed 的值，腳本收到即丟棄：review 第 1 輪 M2）**：`BFF_OIDC_CLIENT_SECRET`（web，必填：沒有時 verification 失敗並寫出要填在哪裡，填好後重跑）；只在開 live AI 時：worker 的 `GEMINUS_AI_LIVE`、`GEMINUS_ANTHROPIC_API_KEY_A`／`_B`（或 T-055 之後的 `GEMINUS_MUSE_API_KEY`，H-033 的名稱），live AI 預設關，API 永遠不設。

Auth0：Allowed Callback URL `https://<web 網域>/auth/callback`、Allowed Logout URL `https://<web 網域>/`、API Identifier（同時填 `AUTH_OIDC_AUDIENCE` 與 `BFF_OIDC_AUDIENCE`）、RFC 9068 access token、只有 Authorization Code 與 Refresh Token、關閉 sign-up。

順序：`deploy.sh --dry-run` → `--apply`（停在 verification：owner 填 `BFF_OIDC_CLIENT_SECRET`）→ 再 `--apply` → Auth0 Application 填它印出的 callback 與 logout URL → `TestLiveBucket`（runbook §7.4）→ runbook §8 的檢查。手動時照 runbook §7.5 的 bootstrap（暫開 TCP proxy → `bootstrap_roles.sql`、`create_logins.sql` → 部署 migrate → seed → 關 proxy）→ api、worker → web。

## 只有 owner 能回答的問題

1. 是否接受 ADR-029（S3 adapter 與自行實作的 SigV4、`geminus_migrations` 紀錄、health probes 不進 OpenAPI）。
2. Web → API 用 API 的公開網域（目前的做法），或放寬 BFF 讓它以 http 連 Railway 的 private network（需要改程式並審查）。
3. Bucket 的 region（建立後不能改）與 synthetic 資料的保存期限；Railway PostgreSQL 的 major 版本（migration 只在 16 上測過）。
4. API 的 key-free AI 可用性（T-053 I5）由 T-055 一併做，或另開任務；在那之前部署的 API 拒絕新的 AI proposal。
5. T-053 I4 (4)／D-114：live eval 的花費保持記在 `checks/ai-evals/live-spend.jsonl`，或改經 DB ledger。
6. 產生的 secret 由 `deploy.sh` 在本機產生並 sealed（不用 `${{secret()}}`），是否接受；若 Railway 不展開 reference 到 sealed variable 的值，選 `GEMINUS_RAILWAY_SEAL=off`（密碼在 UI 可見）或改為把整個 database URL 存成 sealed（需要改腳本與檢查）。
7. H-031（純 Railway；review 第 1 輪 I4 更正，原寫 H-030）與 H-032（token 與由 coordinator 部署）由 coordinator 記錄；ADR-029 的編號若與 T-055 的新 ADR 衝突，由 coordinator 重新編號。

## T-039 剩下的工作

Container 中的實際執行與 CI 的 `containers` job 結果；`deploy.sh --apply` 與 staging 的檢查；`TestLiveBucket`；T-053 I4 (2) 的 operator 介面、I5 的程式修改、D-114 (b)；T-054 I1 的一般做法與 I2；container scan、SBOM、image 簽章；CSP 與 security header；備份與還原演練（`make db-restore-test`）；log／telemetry 的保存；孤兒 object 的清理；OTLP backend；正式（非 hackathon）環境的 region、retention 與 production approval。

## Coordinator 驗證（2026-10-26）

`t039-hk` 的三個 commit（f4c199e、b024bea、5327129）cherry-pick 到 492fae6（T-015、T-051、T-032 已整合）之後；衝突只在 `docs/THIRD_PARTY_NOTICES.md`（開頭一句合併為兩邊都列，T-015 的 React Flow 一節與 T-039 的 SigV4、base image 兩節都保留）與生成的 `checks/docs-check.json`（重產）。

指令與結果（evidence：`checks/evidence/2026-10-26-T-039/coordinator/`，`exit-codes.txt` 依執行順序；HEAD d229225；Node 22.22.0，go1.27.3）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check`（含 `scripts/check_deploy.py`） | 0 | |
| `make backend-test GOFLAGS=-count=1` | 0 | 77 個 package ok |
| `make architecture-check` | 0 | |
| `go test -race -count=1`（storage/objects、storage/migrations、config、cmd、check-architecture） | 0 | 9 個 package ok |
| `python scripts/rehearse_railway_deploy.py` | 0 | `rehearsal: PASS`（替身；Railway 本身 NOT RUN） |
| `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN deploy/railway/deploy.sh --dry-run` | 0 | 環境中沒有任何 Railway token；log 只有變數名稱與 `${{…}}` 參照，沒有值 |
| `bash -n`（deploy.sh、staging-smoke.sh） | 0 | |
| `make integration-test`（新的 dt_test_hk039c，跑完刪除） | 0 | |
| `npm ci`（web、web-e2e） | 0／0 | |
| `make frontend-test` | 0 | 770／770 |
| `make frontend-build` | 0 | `next.config.ts` 的改動之後 |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| key scan（T-039 的檔案：deploy、三個 Dockerfile、`.env.example`、runbook、報告與 evidence；`sk-ant-` 與 key／secret／token 指派的形狀） | 0 | 沒有符合；以已知的 fake key 做正向對照，pattern 有效 |
| `make e2e-test`（等 3100／3101 空出後單獨執行） | 0 | 257 passed、32 skipped（全部是只在 CI 比對的截圖） |

NOT RUN：image build、compose smoke、CI 的 `containers` job（本機沒有 Docker daemon；push 後 CI 第一次執行）；`deploy.sh --apply` 與任何 Railway、Auth0 的呼叫；`TestLiveBucket`；hadolint、actionlint、shellcheck；Node 24。

Coordinator 對 T-039 問題的處理（例行事項；需要 owner 的另列）：
1. ADR-029 維持 PROPOSED，比賽後放進決策頁；切片依它實作。
2. web → api 走 API 的公開 HTTPS domain（Railway 的 private network 是 plain HTTP，web server 只在 loopback 允許）：維持。
3. Railway PostgreSQL 用 16（migration 只在 16 測過）；bucket 的資料只有 synthetic，保留到評審結束（11-09）後再由 owner 決定。
4. API 的 AI 可用性（T-053 I5）：T-055 暫停，API 維持 `AI_PROVIDER=disabled`。
5. D-114：比賽後的決策頁。
6. H-030、H-032 已記錄；T-039 維持 `not_started`（T-043 仍為 `in_progress`）；ADR-029 的編號沒有衝突。
7. 沒有 CSP、HSTS 等瀏覽器安全 header：公開 demo 前在 T-057 處理；Next server 關閉時不等 in-flight request：Low，比賽後。

需要 owner 決定（部署前，約 10-31，一起問）：
- **Secret 的產生方式**：H-032 寫「session secret 由 Railway 產生」；腳本改為在部署的 session 中以 Python `secrets` 產生、經 stdin 寫入並 sealed（不印出），因為 `${{secret()}}` 只在 template 部署有文件，且可能重新產生。這與 H-032 的字面不同，由 owner 決定。若 sealed 變數不能被其他變數參照，`--apply` 會停在 migrate，屆時選 `GEMINUS_RAILWAY_SEAL=off` 或存整個 sealed 的 database URL。
- **Bucket 的區域**：sjc、iad、ams 或 sin（建立後不能改）。

下一步：一輪獨立審查（H-029）。

### CI 的第一次 containers job（run 203）與修正

CI run 203（ca2c6b1）：除了新的 `containers` job 以外都通過（backend 含 race 與數值、frontend、browser 含截圖、spec-check、PostgreSQL 16 integration）。`containers` 中 `make build-containers` 通過；`make staging-smoke` 的 migrate、seed、登入檢查與 api、worker、web 的 health 都通過，只有 `oidc-test` 一直 unhealthy：它用 web 的 image 執行測試 provider，繼承了 image 的 `HEALTHCHECK`（`127.0.0.1:$PORT`，image 中為 3000），而 provider 在 3101。修正（3bcd860）：先在 `scripts/check_deploy.py` 加規則（長時間執行、取代 image command 的 service 必須有自己的 healthcheck；修正前 exit 1、修正後 0），再給 `oidc-test` 檢查 `127.0.0.1:3101/.well-known/jwks.json` 的 healthcheck。本機重現（沒有 Docker）：image 的 healthcheck exit 1、新的 exit 0、provider 停止後新的 exit 1（`checks/evidence/2026-10-26-T-039/coordinator/ci-203-containers-fix.txt`）。containers job 本身只能由下一次 CI 驗證。

CI run 204（f276494）：healthcheck 的修正生效（api、web、worker healthy），但 `oidc-test` 啟動即 exit 1：T-043 的後續讓 `apps/web-e2e/fake/servers.mjs` 讀 `testdata/contracts/valid-draft-with-issues.json`（以 `new URL("../../../testdata/…", import.meta.url)`，在 container 中解析為 `/testdata/contracts/valid-draft-with-issues.json`），而 compose 只 mount `/fake`（兩個切片之間的語意衝突，各自的測試都看不到）。修正：先在 `scripts/check_deploy.py` 加規則（`servers.mjs` 以 `import.meta.url` 讀的每個檔案都必須被 `oidc-test` 的 volume mount 到它在 container 中解析的位置；修正前 exit 1，訊息即 CI 的路徑），再以唯讀 volume mount 這個 fixture。同一次 run 的 browser checks（含截圖比對）、backend、frontend、spec-check、integration 全部通過：T-043 讓未登入的 `/projects` 多了 Sign in 連結，但已接受的 baseline 比對仍通過，所以不需要新的 baseline。staging smoke 在 `up --wait` 之後的步驟（登入導向、401、SIGTERM 等）CI 尚未執行過，下一次 run 才是第一次。

## Review round 1 fixes（2026-10-26）

**Branch：`t039-r1`（從 5b66648 開出，沒有 push）｜Commits：f1855bb（修正、測試、文件）、2c721a0（rehearsal 再加三個情境與 mutant 定義）、600e601（P6 的 assertion，mutant R10）、973b484（rehearsal 的摘要只印 run 2 掃描的數量，assertion 沒有改）、加上本節與 evidence 的 commit｜審查：`checks/T-039_REVIEW_r1.md`（獨立 reviewer，CHANGES REQUESTED：M1、M2，沒有 High）｜沒有部署、沒有呼叫 Railway 或 Auth0，沒有讀取、印出或移動任何 token 或 secret：每個命令都以 `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN` 執行，rehearsal 只用假的 `REHEARSAL-TOKEN-NOT-REAL`｜沒有改 Go、migration 或 DB 的程式，沒有建立任何 `dt_test_*` database 或 role｜Evidence：`checks/evidence/2026-10-26-T-039-r1/fixes/`**

**測試先行。** Rehearsal 的新情境與 `scripts/check_deploy.py` 的新規則先在未修正的檔案上執行，兩者都失敗：`red-check-deploy.log`（exit 1：`.dockerignore: **/.env is not excluded`）；`.dockerignore` 與變數名稱修好、這個 check 通過之後，`red-rehearsal.log`（exit 1：未修正的 deployer 上 21 項失敗，包括 reviewer 的三個情境、xtrace、token、psql／go 的環境、網域以外的全部新情境）。Reviewer 的 harness（複製到本輪的 scratch；reviewer 的檔案沒有修改）修正前 `reviewer-harness-before.log`：三個情境都是沒有警告的 `done`，owner 的兩個值進入 deployer；修正後 `reviewer-harness-after.log`（只讓 `recording()` 轉交新的 `keep=` 參數）：收到的 owner 值為 `[]`，delete 失敗與被中斷的執行留下的 proxy 都以 `FAIL: verification:` 結束並寫出 id。為了 mutant 再加的 assertion（P6 的移除失敗、P7、run 5b、`DATABASE_PUBLIC_URL` 的登記、P1／P3 必須在 verification 中回報）是修正後才寫的，它們的「紅」由對應的 mutant 證明（下方）。

### 逐項

| Finding | 修改 | 測試 | Mutant |
|---|---|---|---|
| **M1** TCP proxy | `proxy_close` 檢查 CLI 的 exit code：失敗時記下 proxy 的 id 與手動移除命令（`railway tcp-proxy delete <id> --yes --service Postgres --environment <environment>`），整次執行 exit 1（不受 `GEMINUS_KEEP_DB_PROXY` 豁免）。新的 `check_proxies()`：在 verify 中，以及每次 `--apply` 結束前（成功、步驟失敗、被訊號中斷；verify 已列過時不重列）列出 Postgres 的 TCP proxy（`{"proxies": [...]}`，依 CLI v5.64.2 原始碼），還開著的任何 proxy（本次的，或執行前就存在、被沿用的）以 id 與移除命令回報並 exit 1；`GEMINUS_KEEP_DB_PROXY=1` 時只以警告列出（仍寫 id）；列不出來也 exit 1。SIGTERM、SIGHUP 轉為 `SystemExit(128+N)`（清理期間第二個訊號被忽略），清理後 exit 143／129；`proxy_open` 移進 `try`；`tcp-proxy create` 印出 `"proxy": null` 時以 listing 找出新 proxy 的 id | run 1（移除之後再列一次）；P1 移除失敗；P2 執行前就存在的 proxy（連續兩次都失敗、不建立第二個、不移除；`GEMINUS_KEEP_DB_PROXY=1` 時成功但仍列出 id）；P3 CLI 說移除了卻還在；P4 create 沒有 id；P5 最後列不出來；P6 SIGTERM、SIGHUP、SIGTERM 加移除失敗（以獨立的 process 執行 `railway_deploy.py --apply`，如 `deploy.sh` 所做）；P7 步驟失敗時 proxy 還開著 | R1–R12 killed；R13、R14 equivalent |
| **M2** owner 的值 | `variables_of(service, keep)` 以 `json.loads` 的 `object_pairs_hook` 在解析當下處理：只有 `keep` 中的名稱（自己產生的密碼、Postgres 的 `DATABASE_PUBLIC_URL`）保留值並 `self.secret()`；其他值（owner 的全部、Railway 自己的、清單以外的）換成 `Seen.SET`／`Seen.SEALED`（空字串維持空字串），owner 的名稱不能放進 `keep`。`Run.run(parse=...)` 只回傳解析後的結果，不回傳 CLI 的原始輸出；不是 JSON 時不保留帶著整份輸出的 `JSONDecodeError`。`verify` 以名稱列出沒有 sealed 的 owner secret（`secret: true` 的四個；`GEMINUS_AI_LIVE` 不是 secret，不提醒，值同樣丟棄），寫出 sealed 的方法（Railway 文件：變數的 3-dot 選單 → Seal），不算失敗 | run 2：owner 的 5 個值與一個清單以外的值都沒有 sealed，在 `vars(run)`、輸出、結果、每個 `Run` method 的回傳值與 raise 的 exception（含 `__cause__`、`__context__`）中都找不到（run 2 期間的 473 個回傳值）；四個 secret 以名稱提醒，`GEMINUS_AI_LIVE` 與清單以外的不提醒。run 2b：owner sealed 後不提醒。run 5b：`GEMINUS_RAILWAY_SEAL=off` 時讀回的密碼被登記遮蔽。run 1：`DATABASE_PUBLIC_URL` 被登記。替身的 `variable list` 另回傳 Railway 自己的變數（`RAILWAY_SERVICE_NAME` 等）與 Postgres template 的 administrator 密碼 | R15–R20 killed |
| **L1** xtrace | `deploy.sh` 的 `case "$-" in *x*)` 移到第一個命令（`set -euo pipefail` 之前），在其中 `set +x` 後 exit 2；`usage` 改為讀開頭的註解直到第一個非註解行（不再依固定行號） | `bash -x deploy.sh --help`、`SHELLOPTS=xtrace bash deploy.sh --help` 為 2，一般為 0；`xtrace.log`（`--dry-run`：2／2） | R21 killed |
| **L2** token | `Run.__init__` 以 `self.secret()` 登記 `RAILWAY_TOKEN`、`RAILWAY_API_TOKEN`（不印出）；所有子程序都經 `Run.run` 與 `child_env()`：只有 `railway` 拿到 token，psql、`go run`、git、`check_deploy.py` 的環境中移除（原本 preflight 與 `admin()` 另外直接呼叫 `subprocess.run`，現在只剩一個呼叫點） | psql 與 go 的替身記錄環境中的 token 名稱，必須都沒有；railway 的每次呼叫都有；E1：CLI 在錯誤訊息中引用 token 時印成 `[redacted]` | R22、R23 killed |
| **L3** `.dockerignore` | 加 `**/.env`、`**/.env.*`、`!**/.env.example`；`check_deploy.py` 要求這三個 pattern、拒絕其他把 env 檔放回來的 `!` 例外，並以 Docker 的比對規則（最後一個符合的 pattern 決定、`**` 為任意層目錄、符合上層目錄即排除）檢查 `apps/web/.env`、`apps/web/.env.local`、`apps/web/.env.production`、`cmd/api/.env`、`.env`、`.env.local` 被排除，`.env.example`、`apps/web/.env.example` 與原始碼在 context 中 | `red-check-deploy.log`（修正前 exit 1） | D1–D5 killed |
| **L5(b)** 網域與 healthcheck | `check_deploy.py` 要求只有 api 與 web 有 `public_domain`（port 等於該 service 的 `PORT`），api 的 healthcheck 為 `/readyz`、web 為 `/`、worker 與 migrate 沒有 | rehearsal 斷言只對 api 與 web 建立網域 | R24、D6–D12 killed |
| **I4** 出處 | 「純 Railway」改引 H-031：ADR-029、本報告（開頭、owner 問題 7）、`variables.json` 的 `about`、`deploy.sh` 與 `railway_deploy.py` 的開頭；另外 runbook 的開頭與 §7 標題、ARCHITECTURE §8、SECURITY §6 的 bucket 一句 | — | — |
| H-033 的名稱 | `GEMINUS_META_API_KEY` 改為 `GEMINUS_MUSE_API_KEY`：`variables.json`（worker 的 owner 變數、api 與 web 的 `forbidden`）、`check_deploy.py`（`PENDING`、API 必須禁止的 key）、rehearsal、`deploy.sh` 開頭、runbook §4.4 與 §7.3 的表、本報告的 owner 清單 | `check_deploy.py` | D13、D14 killed |

文件改成實際行為：runbook §7.3（「腳本實際收到什麼」一段）、§7.7（token、步驟 3、4、6、8、新的「TCP proxy」一條、結果的 exit code、rehearsal）、§10 問題 (8)；SECURITY §6；ADR-029（token、變數清單的處理與不登記全部值的理由、TCP proxy、L4 的已知缺口、待 owner 決定 5）；本報告開頭與交付表中「不讀」與「xtrace 時拒絕」的聲明已就地更正。

**與 brief 字面不同的一點（請 coordinator 判斷）。** Brief 寫「其他收到的非 null 值一律 `self.secret()`」。這做不到：Railway 自己的變數與 Postgres template 的變數有 `railway`、`production`、`api`、`5432`、`postgres` 這類值，登記為 secret 之後，每次呼叫前的引數檢查會拒絕 CLI 本身（argv[0] 就是 `railway`）、`--environment production`、`--service api`、`--port 5432`，部署完全無法進行。所以改為：腳本不需要的值一律在解析時丟棄（不保留就沒有可外洩的值），保留的值一律登記遮蔽。效果比「保留並遮蔽」更保守。

**決定：執行前就存在的 TCP proxy。** 腳本沿用它（CLI 原始碼的說明：每個 service 只允許一個 TCP proxy），**不移除**，因為無法分辨它是 owner 依 runbook §7.5 手動開的，還是被 SIGKILL 的執行留下的；但每次 `--apply` 結束時都寫出它的 id 與移除命令並 exit 1，直到有人移除它，或明確設 `GEMINUS_KEEP_DB_PROXY=1`（只警告）。Reviewer 的情境 3 現在每次執行都失敗並寫出 `tcp_left_by_killed_run`。SIGKILL 本身無法攔截，所以「被殺掉的執行」的保證是「下一次執行一定回報」，不是「立即移除」。

### Mutation

`checks/evidence/2026-10-26-T-039-r1/fixes/mutants.py`（本輪的 mutant 定義，L7）在 `git archive` 的複本中執行（reviewer 的 git 替身在 PATH 最前面）：每個 mutant 只替換一處文字並跑一個 check（rehearsal 或 `check_deploy.py`），之後還原並比對 SHA-256；兩個 check 先在未改動的複本上通過。

- 第一輪（2c721a0，`mutants-pass1.log`，exit 1）：35/36 killed、2 equivalent、0 invalid；**R10 存活**（被中斷的執行不回報 proxy）：P6 的移除失敗情境在整個輸出中找移除命令，而 `$ railway tcp-proxy delete tcp_1 …` 這行命令的回顯本身就含有它。P6 改為只看回報的行（600e601），assertion 沒有放寬。
- 第二輪（973b484，最後的 rehearsal；`mutants.log`，exit 0）：**36/36 killed**（R1–R12、R15–R24、D1–D14）、2 equivalent（R13、R14）、0 invalid、`restored files: PASS`。（600e601 上開始的一次在 973b484 改了摘要的列印之後停止並丟棄，沒有結果。）

Equivalent 的兩個：R13（成功的執行在 verify 之後再列一次 proxy：那時一定是空的，否則 verify 已失敗；保留它，讓「結束前一定列出」不依賴 verify 的寫法）；R14（`proxy_open` 移回 `try` 之外：沒有替身在 create 與 `try` 之間失敗或送訊號，所以 rehearsal 分不出來；移進 `try` 是為了 create 之後立即失敗的情況）。

### 實際執行結果（全部以 `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN`；`exit-codes.txt`）

| 命令 | Exit | 結果（evidence） |
|---|---|---|
| `python -I scripts/check_deploy.py`（新規則、修正前的 `.dockerignore`） | 1 | `red-check-deploy.log` |
| `python -I scripts/rehearse_railway_deploy.py`（新情境、未修正的 deployer） | 1 | `red-rehearsal.log`：21 項 |
| Reviewer 的 harness（5b66648 的複本） | 0 | `reviewer-harness-before.log`：三個情境都 `done`（harness 只印結果，不判定） |
| Reviewer 的 harness（f1855bb 的複本，`keep=` 的轉交） | 0 | `reviewer-harness-after.log` |
| `bash -n deploy/railway/deploy.sh`、`bash -n deploy/local/staging-smoke.sh`、三個 Python 檔的 `ast.parse` | 0 | `bash-n.log` |
| `bash -x deploy/railway/deploy.sh --dry-run`；`SHELLOPTS=xtrace bash deploy/railway/deploy.sh --dry-run` | 2／2 | `xtrace.log`：`refusing to run with xtrace on` |
| `bash deploy/railway/deploy.sh --help` | 0 | `deploy-help.log` |
| `python -I scripts/check_deploy.py` | 0 | `check-deploy.log`：`PASS_STATIC_DEPLOY_CHECKS`，`public_domains: [api, web]` |
| `python -I scripts/rehearse_railway_deploy.py` | 0 | `rehearsal.log`：`rehearsal: PASS`；run 1 92 個 CLI 呼叫、7 個 psql；P1–P7、E1 的訊息；signals `[143, 129, 143]`；xtrace `[2, 2, 0]` |
| `./deploy/railway/deploy.sh --dry-run` | 0 | `deploy-dry-run.log`：不連網；新的「would list Postgres's TCP proxies」一行 |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（加入本節之後） | 0 | `spec-check.log`；重新產生的 `checks/deploy-check.json` 已在 f1855bb commit（變數名稱與 `public_domains`），`checks/docs-check.json` 沒有改變 |
| Mutation 第一輪（2c721a0）／第二輪（973b484） | 1／0 | `mutants-pass1.log`：35/36（R10 存活）；`mutants.log`：36/36、2 equivalent |

### NOT RUN

- `deploy.sh --apply`、`--install-cli`、任何 Railway 或 Auth0 的呼叫（依指示）。Sealed 的 owner 變數在真正的 `variable list` 中是否為 `null`、`tcp-proxy delete` 實際的錯誤與 exit code，只依 CLI v5.64.2 的原始碼（`sources-r1.txt`）與替身。
- `make backend-test`、`make backend-race`、`make integration-test`：沒有改 Go、migration 或 DB 的程式（依指示只在改了時執行）。
- `make e2e-test`、frontend 測試（依指示）。
- `make build-containers`、`make staging-smoke`：沒有 Docker daemon。新的 `.dockerignore` 只以 `check_deploy.py` 對 Docker 比對規則的模擬檢查，**沒有以真正的 `docker build` 驗證**（push 後 CI 的 containers job 會建置）。
- shellcheck、hadolint、actionlint（沒有安裝）。
- 真正的 SIGKILL（無法攔截；以 P2「執行前就存在的 proxy」代表下一次執行的回報）。

### 延後（本輪不修）

- **L4**：migration 的 transaction 指令偵測（`END;`、`COMMIT WORK;`、`COMMIT AND CHAIN;`、`PREPARE TRANSACTION`、與其他 statement 同一行的 `COMMIT;`）未修；ADR-029 加了一句：修正之前新的 migration 不得使用這些寫法。
- **L5(a)**：`CheckLogin` 的 runtime role 清單拿掉 `twin_provisioner` 的 mutant 仍會存活（沒有同時屬於 twin_owner 與 twin_provisioner 的測試 login）。
- **L6**：經公開 TCP proxy 的 bootstrap 沒有 channel binding（`PGCHANNELBINDING=require`）、login 密碼以明文 literal 送到 server。
- **L7**：本輪的 mutant 定義已 commit（`mutants.py`）；原作者的 S3 17、DB 18、部署檢查 17、rehearsal 12 個 mutant 的定義沒有重建。
- **I1**（`/readyz` 每次 ping）、**I2**（bucket 的 key 改用 reference variable）、**I3**（CLI 其他命令的 JSON 形狀、`status()` 取第一個 deployment）、**I7**（`log_statement` 下的明文密碼）未處理。本輪以 CLI 原始碼核對了 `variable list` 與 `tcp-proxy` 的 JSON 形狀，其他仍未查證。
- **I5** 是 coordinator 的（報告最後一段已過時，CI run 206 已執行 staging smoke）；**I6** 屬於 T-057。
- `planning/tasks.json` 中 T-039 的 completion_evidence 仍寫「owner 的 secret 腳本不設不讀」與 H-030，`planning/decisions.json` 仍寫 `GEMINUS_META_API_KEY`：planning 檔沒有改，由 coordinator 處理。

### 給 owner 的問題（由 coordinator 轉達；本輪不決定）

> H-033 寫 key「只看變數名稱、不讀值、不印出」，H-032 寫 Anthropic key 與 Auth0 的 secret「agent 不讀取也不搬移」。部署腳本要知道 Railway 上已經有哪些變數，只能用 Railway CLI 的 `railway variable list --json`，而它會把每個**沒有 sealed** 的變數連同值一起回傳（sealed 的只有名稱；CLI 沒有只列名稱的選項）。現在的腳本在解析的當下就把你的變數（`BFF_OIDC_CLIENT_SECRET`、`GEMINUS_ANTHROPIC_API_KEY_A`／`_B`、`GEMINUS_MUSE_API_KEY`、`GEMINUS_AI_LIVE`）的值換成「已設／sealed」的標記：不保留、不印出、不寫檔、不傳給其他程式，verification 只以名稱提醒哪些還沒有 sealed。但沒有 sealed 時，值仍會短暫經過部署 session 的記憶體。請選一個：(A，建議) 你在 Railway 新增這些變數後立刻 sealed（變數右側的 3-dot 選單 → Seal；sealed 後不能解除，但仍可以改值），之後值不會再到達腳本；(B) 接受現在的做法：沒有 sealed 也可以，腳本收到即丟棄；(C) 其他做法（例如部署時完全不列出 web 與 worker 的變數：那樣腳本無法判斷產生的 secret 與 bucket 的 key 是否已存在，也無法檢查 `BFF_OIDC_CLIENT_SECRET` 是否已填，需要改設計並重新審查）。

（H-034 之後由 agent 經 stdin 寫入並 sealed 的 Auth0 client secret 不受影響：sealed 的值 CLI 只回傳 `null`。）


## Coordinator 驗證：review 第 1 輪修正（2026-10-26）

`t039-r1` 的五個 commit（f1855bb..d52b2b1）cherry-pick 到已整合 T-055、T-056 與 API key-free availability（D-124）的主線。第一個 commit 與 D-124 的部署變更在 5 個檔案衝突，處理方式：
- `deploy/railway/variables.json`：`about` 用本輪的文字（owner 填完即 sealed、腳本收到即丟棄），保留「API 不設 AI key，AI 設定與 worker 相同」；api 的 `forbidden` 用 D-124 的清單（不再禁止 `GEMINUS_AI_LIVE`，加上其他 key 名稱）；`GEMINUS_MUSE_API_KEY` 拿掉「T-055 新增中」的註記。
- `scripts/check_deploy.py`：保留兩邊的檢查（D-124 的 API key 與四個設定、本輪的 public domain 與 healthcheck）；`PENDING` 為空。
- `deploy/railway/deploy.sh` 的開頭說明、`docs/runbooks/deploy.md` 的兩張表：合併兩邊的內容。

另外修正一個合併後才出現的問題（71dff87）：rehearsal 的替身 psql 回答固定的 16 個 migration，T-056 新增 0017 之後，部署腳本會等滿 900 s 才失敗，rehearsal 不可能通過。替身改為讀 checkout 的 `migrations/*.up.sql` 數目；rehearsal 也檢查部署腳本不設 owner 的 `AI_POLICY_APPROVAL`、`AI_MODEL`（D-124 之後的 owner 設定）。

指令與結果（evidence：`checks/evidence/2026-10-26-T-039r1-T-031r1-apiavail-coordinator/`，`exit-codes.txt` 依執行順序；HEAD 1425e3a；go1.27.3、Node 22，`--engine-strict=false`；Railway 的指令全部以 `env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN` 執行）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `scripts/check_deploy.py` | 0 | |
| `scripts/rehearse_railway_deploy.py` | 0 | `rehearsal: PASS`（替身；Railway 本身 NOT RUN） |
| `deploy/railway/deploy.sh --dry-run` | 0 | |
| `make backend-test "GOFLAGS=-count=1 -timeout=30m"` | 0 | 83 個 package ok（`scripts/check-architecture` 252 s） |
| `make architecture-check` | 0 | 95 packages、25 rules、16 build configurations |
| `make ai-eval` | 0 | fake provider |
| `go test -race -count=1 -timeout 30m ./cmd/... ./internal/config/... ./internal/proposals/...` | 0 | 11 個 package ok |
| `make integration-test`（共用 cluster） | 2 | 沒有執行任何測試：container 重啟後共用的 PostgreSQL（port 55440）沒有在跑，連線被拒（`integration-shared-cluster-down.log`）；依規則不啟動它 |
| `make integration-test`（暫時的 cluster：`/var/lib/postgresql/geminus-throwaway-hkb6`，port 55450，PostgreSQL 16.15，新的 dt_test_hkb6c，跑完刪除） | 0 | 38 個 package ok，沒有 FAIL 或 SKIP（`integration.log`） |
| `npm ci`（apps/web、apps/web-e2e） | 0、0 | |
| `make frontend-test` | 0 | 47 個檔案、1037 個測試 |
| `make frontend-build` | 0 | |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| `make e2e-test`（3100–3102 沒有其他執行時才開始） | 0 | 507 passed、32 skipped（只在 CI 比對的 screenshot） |

NOT RUN：任何 live 呼叫；Railway 與 Auth0 上的任何操作；完整的 `make backend-race`；CI 的 screenshot 比對（push 後由 CI 執行）。

Coordinator 的處理：
- M1、M2 已修正並驗證；M2 給 owner 的問題由 owner 選 A（H-036：填完立刻 sealed）。依 H-029，本切片（hackathon 部署準備）完成；`deploy.sh --apply` 仍未執行，在部署 session 由 coordinator 執行。
- 延後的 L4、L5(a)、L6、L7、I1–I3、I7 比賽後處理；I5 已過時（CI run 206 之後 containers job 全綠）；I6 屬於 T-057。
- planning 中 T-039 的 completion_evidence 新增本輪的紀錄（舊紀錄保留原文）；`planning/decisions.json` 中 H-029 的未決事項保留當時的名稱 `GEMINUS_META_API_KEY`（歷史紀錄；H-033 之後為 `GEMINUS_MUSE_API_KEY`）。
