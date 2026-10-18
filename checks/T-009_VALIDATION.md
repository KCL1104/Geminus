# T-009 驗證報告：Project service 與 server RBAC

**日期：2026-10-18｜狀態：`review`（等待獨立 review，H-003）｜需求：REQ-001、REQ-002｜Gate：D-008、D-012、D-025（皆已批准；另用到已批准的 D-001、D-011）｜實作選擇：ADR-019（PROPOSED，依 owner 授權先實作）**

逐條輸出在 `checks/evidence/2026-10-18-T-009/`。範圍依 tasks.json：project endpoints 與 scoped listing、每個 request 在同一 transaction 查 membership／role、錯誤／cursor／request_id 與 OpenAPI 對齊。本任務也建立了之後每個 route 共用的 HTTP 層（ADR-019）與 `make integration-test`。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/ids/` | D-001 ID：`<prefix>_<ULID>`（48 位元 Unix 毫秒＋80 位元 crypto/rand，Crockford base32 大寫、首位 0–7） |
| `internal/httpapi/errors.go` | 封閉的 20 個 error code 與 status（contract test 鎖成 `common.schema.json` 的 enum 與 `x-http-status`）；`WriteError`：auth、tenant、postgres 的 sentinel 對應 401／403／404／503，其他一律 500 `internal error`、原因只寫 log（帶 request_id）；catalogue 外的 code 也轉成 500；401 帶 RFC 6750 challenge；`WriteJSON`／`WriteBody`：每個回應帶 `Content-Type: application/json`、`nosniff`、`no-store`，無法編碼時仍回 error envelope |
| `internal/httpapi/request.go` | `Handler`：server 發 `req_<ULID>`（不用 client 的 `X-Request-ID`），放進 header、context、error body；panic 轉 500 並寫 log，`http.ErrAbortHandler` 照原意中止。`DecodeJSON`：media type 與 `charset=utf-8`（不分大小寫）否則 415；大小上限（預設 1 MiB）否則 413；讀取中斷 400；不合法 JSON、重複成員、非 UTF-8、尾端多餘資料 400；schema 失敗 422，每個失敗的 JSON Pointer 一個 detail（含 severity）；schema reference 無法解析或 Go type 與 schema 不一致是 500 |
| `internal/httpapi/scope.go` | `Scoped`：先驗 Bearer token，再要求恰好一個合法的 `X-Organization-ID`（否則 400），以 `ResolveUser` 解析 user，把 `tenant.Scope` 交給 handler。Header 只是 selector；membership 與 role 由 handler 在 `tenant.DB.Run` 的 transaction 內檢查 |
| `internal/tenant/policy.go` | `Membership.Require(roles...)`（`ErrForbidden` → 403）；`Members`（owner、editor、viewer）與 `Writers`（owner、editor），與 `x-required-roles` 由 contract test 鎖住 |
| `internal/tenant/transaction.go` | `tenant.Tx`／`tenant.TxOptions`（pgx 的 alias）與 `ReadWrite`／`ReadOnly`，service 不必 import driver |
| `internal/project/` | Service：`Create`（role → idempotency claim → insert → audit → 回應存入 idempotency，同一 transaction）、`Get`、`List`（唯讀 transaction）；cursor 是 base64url 的 `{v, o, l, t, i}`，綁 organization 與 list，未簽章。Handlers：`POST /v1/projects`、`GET /v1/projects`、`GET /v1/projects/{id}`；`Idempotency-Key` 為 8–128 個可見 ASCII 字元；`limit` 為 1–100 的十進位整數，預設 50 |
| `internal/storage/idempotency/` | D-011：key 以 organization、actor、operation 為 scope；request hash 是 body 的 JCS canonical form 的 SHA-256；同 hash 重播、不同 hash `ErrConflict`；並行 request 由 primary key 序列化；保留 24 小時，過期後重新 claim |
| `internal/storage/projects/` | twin.projects 的 SQL；每個查詢都另帶 organization filter；依 `(created_at, id)` keyset 分頁 |
| `internal/storage/auditlog/` | 在 action 的 transaction 內 append `twin.audit_events`（actor_type `user`、actor 為 scope 的 user、request_id、只放 ID 與計數的 metadata） |
| `internal/api/` | 所有 route 組成一個 `http.Handler`；其他 path 或 method 為 404 `NOT_FOUND` envelope。`cmd/api` 與整合測試用同一個 handler |
| `internal/config/server.go` | `API_LISTEN_ADDR`（預設 `:8080`，port 0–65535） |
| `cmd/api` | 不再拒絕啟動：以 API runtime login 連 PostgreSQL（superuser、BYPASSRLS、table owner 拒絕）、pool 裝 `tenant.Guard`、server timeouts 與 header 上限、SIGINT／SIGTERM graceful shutdown（20 s） |
| `internal/storage/dbtest/` | test-only：disposable DB 的 provisioning（只接受 `dt_test_*`）、API login、organization 與 user fixtures。Architecture check 禁止 production 連結 |
| `scripts/check-architecture/` | `internal/httpapi`、`internal/api` 列為 app layer；`dbtest` 列為 test infrastructure；`db-only-in-storage` 新增 `through`：不追進 `internal/storage` 與 `internal/tenant` 內部的依賴（service 經由它們存取資料，自己不 import driver），另有 synthetic graph 測試（經 tenant 不違規、直接 import pgx 違規） |
| `Makefile` | `integration-test`：在新的 disposable DB 上跑 `db-test`（SQL tests，含 migration）與 `db-go-test`（storage、tenant、api、cmd/api，`-race`，skip 視為失敗）；worker 部分明確印出 NOT RUN（T-019） |
| `.github/workflows/ci.yml` | db job 改跑 `make integration-test` |
| `openapi/openapi.yaml` | 17 個有 request body 的 operation 補上 413（server 對每個 JSON body 都可能回 413）；`IdempotencyKey` 加上 pattern `^[!-~]{8,128}$` |
| `scripts/check_contracts.py` | 每個 request body 必須宣告 413 與 415；載入 openapi.yaml 時拒絕重複的 YAML key（PyYAML 預設保留最後一個，不報錯） |
| 測試基礎 | `db-go-test` 以 `-p 1` 依序執行 DB package（共用一個 database，部分測試改變 database 層級的狀態）；`dbtest` 與 tenant 測試建立 login 時容許另一個測試同時建立 |
| 文件 | ADR-019（PROPOSED）；ARCHITECTURE.md §2；db/README.md |

## 實際執行結果

所有 DB 測試都在一次性 PostgreSQL 16（trust 認證、無密碼、無客戶資料）。`integration-test` 在**新建的 cluster**（`/var/lib/postgresql/geminus-t009-fresh`，port 55440；建立前沒有任何 `twin_*`／`dt_test_*` role）的新 database 執行，等同 CI 的 service container；mutation 在既有的 `dt_test_t009`。

| 指令 | Exit | 說明 |
|---|---|---|
| `make backend-test` | 0 | gofmt、go vet、go test（56 s；`backend-test.log`） |
| `make backend-race` | 0 | 77 s（`backend-race.log`） |
| `make backend-numeric`、`make backend-build` | 0 | `backend-numeric.log`、`backend-build.log` |
| `make architecture-check` | 0 | `PASS architecture: 32 packages, 9 rules, 16 build configurations`（`architecture-check.log`） |
| `make spec-check` | 0 | 含 contracts（413／415、重複 YAML key）、planning、docs、decisions；重新產生的 `checks/*.json` 已一併提交（`spec-check.log`） |
| `make frontend-test` | 0 | 7 個 test file、66 個 test（前端未修改；`frontend-test.log`） |
| `DT_ALLOW_DB_TESTS=yes … make integration-test`（新 cluster、`dt_test_fresh2`） | 0 | SQL tests up／down／up，再以 `-p 1 -race` 跑 storage、tenant、api、cmd/api；worker 部分印出 NOT RUN（`integration-test.log`）。最後一輪測試修改前也在同一 cluster 的 `dt_test_fresh` 通過一次 |
| `go test -p 1 -count=1 -v`（T-009 的 8 個 package，DB 設定） | 0 | 56 個 RUN，0 FAIL，0 SKIP（`t009-tests.log`） |
| `govulncheck@v1.9.0`（`GOTOOLCHAIN=go1.27.2`） | 0 | `No vulnerabilities found.`（`govulncheck.log`；本任務未新增依賴） |
| Mutation（`mutants.py`，164 個） | — | **156/156 被擋，8 個 equivalent**（見下） |
| 紅燈：新的 contract 檢查 | 1 | 重複的 `'415'` key 與缺少的 413 都被 `check_contracts.py` 擋下（`red-contracts.log`） |
| Login 建立的競爭（`role-race-probe.log`） | — | 8 個 session 同時執行舊的 `IF NOT EXISTS … CREATE ROLE` 20 輪：4 個失敗；修正後 0。4 個 package 並行、每輪先刪 login 的 Go probe 在新舊程式都 0/10（package 啟動時間錯開），因此以 SQL 層的 probe 證明 |
| GitHub Actions | — | 推送後補上 |

### Mutation

每個 mutant 移除或弱化一項檢查（`go test -overlay`，不改工作樹），`-p 1`。

| 回合 | 結果 | 處理 |
|---|---|---|
| 第一輪（`mutation-first-run.log`，診斷用） | 144/162 被擋，2 equivalent | 15 個 mutant 沒有編譯（`if false` 讓變數或 import 未使用）→ 改寫，之後先以 `go test -run '^$'` 確認 164 個都能編譯。**4 個 equivalent mutant 被「擋下」**，失敗的是 `TestCreate`／`TestList`，與 mutant 無關，因此是 flaky test：(1) `TestCreate` 計算整個 database 的 project 數，其他 package 的測試同時在寫；(2) `tenant.TestRuntimeLoginCannotCreate` 暫時對 PUBLIC 開放 schema public 的 CREATE，同時執行的 API 測試的 `CheckRuntimeRole` 因此拒絕 login。修正：計數限定 organization；`db-go-test` 與 mutation 皆 `-p 1`（DB package 依序執行）；login 建立容許並行。1 個存活：`list-limit-ignored`（storage 回傳多於 limit 的 row，service 會截斷）→ `TestKeysetTies` 檢查 limit |
| 完整重跑（`mutation.log`） | 154/156 被擋，8 equivalent | 存活 `unknown-code-not-caught`：拿掉 catalogue 檢查後 `WriteHeader(0)` panic，由 recover 回 500，回應相同，只有 log 不同 → 測試檢查 log 為 `unknown error code`。存活 `audit-metadata-null`：json/v2 把 nil map 編成 `{}`，該分支是 dead code → 刪除分支與 mutant |
| 補充（`mutation-addendum.log`，`--only`） | 9/9 被擋 | 上述兩項；新增的 `replay-before-role-check`（role 檢查移到重播之後，被降級的 editor 重試得到舊的 201）；改過檔案的 audit mutant；ID regex 的 mutant 由新的 schema contract test 擋下 |

最終：164 個 mutant，156 被擋，8 個 equivalent（理由寫在 `mutants.py`）：`in-progress-unmapped`、`in-progress-unchecked`（claim 與 complete 同一 transaction，committed 的 key 一定有回應）、`limit-error-ignored`（Atoi 的錯誤值必然落在範圍外或與 `Itoa` 不符）、`cursor-length-unbounded`（超過 2048 bytes 的 cursor 解碼必然失敗）、`select-not-locked`（`INSERT … ON CONFLICT` 已等待衝突的 transaction）、`complete-rows-unchecked`（complete 一定接在同一 transaction 的 claim 之後）、`split-error-ignored`（`SplitHostPort` 失敗時 port 為空，Atoi 拒絕）、`guard-not-installed`（`tenant.TestGuardDiscardsSessionState` 已證明 guard；cmd/api 只負責裝上）。

## 驗收對照

| 測試 | 驗證內容 | 對應 |
|---|---|---|
| `api.TestRoleMatrix`（DB，經真實 handler、TLS test OIDC provider、API login） | create：owner、editor 201；viewer 403；其他 organization 的 member、沒有 membership 的 user 404；沒有 user 的 identity 403；沒有／錯誤的 token 401 與 challenge；缺少或格式錯誤的 organization 400；只有 owner 與 editor 的 project 被建立。get／list：三種 role 皆可，get 回傳的就是建立的 project；其他 organization 的 member 與 stranger 皆 404 | TEST-009（projects）、REQ-001、REQ-002、D-008 |
| `api.TestCreate` | 回應符合 schema 與 headers；audit event 帶 user、action、target、request_id、空 metadata；缺 key、key 長度 7／129、空白、非 ASCII、兩個 key header、不合法 JSON、重複成員、未知成員（含 `organization_id`、`created_by`）、空名稱、過長名稱、錯誤 content type、超過 1 MiB 皆在寫入前拒絕且不建立 project 或 key；長度 8、128 與所有可見符號的 key 可用；未知 route 與 method 404 | D-012、D-025、REQ-018 |
| `api.TestIdempotency` | 同 key 同 body（含不同空白）重播 byte 相同的回應與 201，但有新的 request ID；不同 body 409；同 key 在其他 user 或 organization 是獨立的；16 個並行重試只建立 1 個 project、答案相同；key 保留 24 小時；過期後重新 claim，新 request 可重播，舊 body 變成 409 | D-011 |
| `api.TestIdempotency`（撤權） | editor 建立後被降為 viewer，以同一 key 重試得到 403；membership 被刪除後 404，都不是舊的 201 | API_DATABASE §7 |
| `api.TestList` | 7 個 project 以 limit 3 分 3 頁、順序正確、無重複；預設 limit 50（51 個 project）；其他 organization 或 list、改過版本、未知成員、非 project ID、不是時間、非 base64、padding、非 canonical 編碼、空、兩個 cursor 皆 400，canonical 編碼可用；limit 0、101、-1、abc、05、+5、1.5、兩個 limit 皆 400，1 與 100 可用；b 的 project 不出現在 a 的 list、從 a 取得為 404；格式錯誤的 ID 400 | TEST-010、API_DATABASE §3 |
| `projects.TestOrganizationFilter`、`TestKeysetTies`（DB，superuser，不受 RLS 限制） | 沒有 RLS 時 organization filter 仍只回自己的 row；同一 transaction 建立（created_at 相同）的 project 分頁時不重複不遺漏 | AGENTS.md「application filter + RLS」 |
| `tenant.TestRunModes`（DB） | `ReadOnly` 的 transaction 確實唯讀，`ReadWrite` 不是 | ADR-019 |
| `httpapi` 單元測試 | Status 與 schema 相同；每個 sentinel 的 status 與 challenge；內部錯誤不洩漏原因並寫 log；request ID 唯一且不用 client 的值；panic 與 ErrAbortHandler；body 的各種 media type、大小邊界（恰好上限／多 1 byte）、讀取錯誤、schema 失敗與 detail；`Scoped` 的順序（拒絕前不解析 user）、selector 格式（前後綴、超過 128 位元、其他 kind） | ADR-019、D-025 |
| `project.TestRoutesMatchContract`、`TestCursor`、`TestProjectIDMatchesSchema`、`httpapi.TestOrganizationIDMatchesSchema` | Route、method、operationId 與 role 與 `planning/api-operations.json` 相同；cursor round trip 與拒絕；ID 的 regex 就是 `common.schema.json` 的 pattern | D-012、D-001 |
| `cmd/api` 測試 | 設定錯誤 exit 1；DB 無法連線 exit 3 且不洩漏密碼；superuser login 拒絕；API login 服務 route 並在 context 結束後 exit 0 | ADR-019 |
| `scripts/check-architecture` | 真實 repo 通過；synthetic graph：經 tenant 存取 DB 不違規、直接 import pgx 違規；新的 app layer 與 test infra 項目 | ARCHITECTURE §2 |

## 需 owner 知悉

- ADR-019 為 PROPOSED：HTTP 層的分工、錯誤對應、request ID、body 規則、idempotency 與 cursor 的設計。依授權先實作；owner 同意後改為 ACCEPTED。
- 沒有任何任務負責一般 API 的 rate limiting（429 `RATE_LIMITED`）：T-036 只涵蓋 BFF 的 login／refresh，T-023 等為 admission quota。建議併入 T-036。
- Cursor 未簽章：只能在 caller 自己 organization 的資料內移動（RLS＋organization filter）。若將來 cursor 攜帶 filter，需在 cursor 內綁定它。

## 與規劃的差異

- 規劃輸出 `internal/project/` 與 `internal/tenant/policy.go` 皆已交付。另建立 `internal/httpapi`、`internal/api`、`internal/ids`、三個 storage package 與 `dbtest`，因為第一個 route 需要它們；之後的 route 共用。
- T-008 報告寫 HTTP error envelope 屬 T-013；實際上 envelope、cursor、request_id 是 T-009 的 scope，因此在本任務完成。T-013 只需處理 model routes 的 If-Match／ETag 與 TS client。
- Audit append 放在 `internal/storage/auditlog`（SQL 只能在 storage）；T-035 的 `internal/audit` 之後可在其上加 correlation 與 privacy filter。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| `make integration-test` 的 worker 部分 | worker 的 job loop 屬 T-019；target 明確印出 NOT RUN |
| TEST-009 的其他 route（model、run、import、download、SSE） | 各自的任務實作 route 時加入同一矩陣；完整矩陣屬 T-036 |
| Rate limiting、OTel tracing | 見上；T-035／T-036 |
| `make e2e-test`、`make ai-eval` | 不屬本任務；target 仍回非零 |
| 獨立 review | 推送後進行（H-003） |
