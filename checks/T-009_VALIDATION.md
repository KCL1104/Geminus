# T-009 驗證報告：Project service 與 server RBAC

**日期：2026-10-18｜狀態：`done`（首輪獨立 review 為 CHANGES REQUESTED，已先紅後綠修正；同一 reviewer 複核 98f5e9c 為 **ACCEPT**，其 low 發現已處理；依 H-003 驗收，owner 可隨時重開）｜需求：REQ-001、REQ-002｜Gate：D-008、D-012、D-025（皆已批准；另用到已批准的 D-001、D-011）｜實作選擇：ADR-019（ACCEPTED，H-006）**

逐條輸出在 `checks/evidence/2026-10-18-T-009/`。範圍依 tasks.json：project endpoints 與 scoped listing、每個 request 在同一 transaction 查 membership／role、錯誤／cursor／request_id 與 OpenAPI 對齊。本任務也建立了之後每個 route 共用的 HTTP 層（ADR-019）與 `make integration-test`。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/ids/` | D-001 ID：`<prefix>_<ULID>`（48 位元 Unix 毫秒＋80 位元 crypto/rand，Crockford base32 大寫、首位 0–7） |
| `internal/httpapi/errors.go` | 封閉的 20 個 error code 與 status（contract test 鎖成 `common.schema.json` 的 enum 與 `x-http-status`）；`WriteError`：auth、tenant、postgres 的 sentinel 對應 401／403／404／503，其他一律 500 `internal error`、原因只寫 log（帶 request_id）；catalogue 外的 code 也轉成 500；401 帶 RFC 6750 challenge；`WriteJSON`／`WriteBody`：每個回應帶 `Content-Type: application/json`、`nosniff`、`no-store`，無法編碼時仍回 error envelope |
| `internal/httpapi/request.go` | `Handler`：server 發 `req_<ULID>`（不用 client 的 `X-Request-ID`），放進 header、context、error body；panic 轉 500 並寫 log，`http.ErrAbortHandler` 照原意中止。`DecodeJSON`：media type 與 `charset=utf-8`（不分大小寫）否則 415；大小上限（預設 1 MiB）否則 413；讀取中斷 400；不合法 JSON、重複成員、非 UTF-8、尾端多餘資料 400；schema 失敗 422，每個失敗的 JSON Pointer 一個 detail（含 severity）；schema reference 無法解析或 Go type 與 schema 不一致是 500；值或成員名稱含 U+0000 為 422（review L1）。`Query`：格式錯誤的 query string 回 400（review L6） |
| `internal/httpapi/scope.go` | `Scoped`：先驗 Bearer token，再要求恰好一個合法的 `X-Organization-ID`（否則 400），以 `ResolveUser` 解析 user，把 `tenant.Scope` 交給 handler。Header 只是 selector；membership 與 role 由 handler 在 `tenant.DB.Run` 的 transaction 內檢查；讀寫的 run 以 `FOR SHARE` 鎖住 membership（review L3） |
| `internal/tenant/policy.go` | `Membership.Require(roles...)`（`ErrForbidden` → 403）；`Members`（owner、editor、viewer）與 `Writers`（owner、editor），與 `x-required-roles` 由 contract test 鎖住 |
| `internal/tenant/transaction.go` | `tenant.Tx`／`tenant.TxOptions`（pgx 的 alias）與 `ReadWrite`／`ReadOnly`，service 不必 import driver；`ErrRetryable`／`ErrOutcomeUnknown` 轉出 `postgres.Classify` 的錯誤，讓 httpapi 不必依賴 storage 基礎層（review M2）；讀寫的 `Run` 以 `FOR SHARE` 讀 membership |
| `internal/project/` | Service：`Create`（role → idempotency claim → insert → audit → 回應存入 idempotency，同一 transaction）、`Get`、`List`（唯讀 transaction）；cursor 是 base64url 的 `{v, o, l, t, i}`，綁 organization 與 list，未簽章。Handlers：`POST /v1/projects`、`GET /v1/projects`、`GET /v1/projects/{id}`；`Idempotency-Key` 為 8–128 個可見 ASCII 字元；`limit` 為 1–100 的十進位整數，預設 50；service 與 `Routes` 共用 `createRoles`／`readRoles`（review L5） |
| `internal/storage/idempotency/` | D-011：key 以 organization、actor、operation 為 scope；request hash 是 body 的 JCS canonical form 的 SHA-256；同 hash 重播、不同 hash `ErrConflict`；並行 request 由 primary key 序列化；保留 24 小時，過期後重新 claim |
| `internal/storage/projects/` | twin.projects 的 SQL；每個查詢都另帶 organization filter；依 `(created_at, id)` keyset 分頁 |
| `internal/storage/auditlog/` | 在 action 的 transaction 內 append `twin.audit_events`（actor_type `user`、actor 為 scope 的 user、request_id、只放 ID 與計數的 metadata） |
| `internal/api/` | 所有 route 組成一個 `http.Handler`；其他 path 或 method、非 clean form 的 path 與 HEAD 為 404 `NOT_FOUND` envelope（review L2）。`cmd/api` 與整合測試用同一個 handler |
| `internal/config/server.go` | `API_LISTEN_ADDR`（預設 `:8080`，port 0–65535） |
| `cmd/api` | 不再拒絕啟動：以 API runtime login 連 PostgreSQL（superuser、BYPASSRLS、table owner 拒絕）、pool 裝 `tenant.Guard`、server timeouts 與 header 上限、SIGINT／SIGTERM graceful shutdown（20 s）；`poolConfig`、`newServer`、`shutdownGrace` 由測試鎖住（review M1、L4）；不安全的 login 與無法連線的 DB 分開記錄（review L8） |
| `internal/storage/dbtest/` | test-only：disposable DB 的 provisioning（只接受 `dt_test_*`）、API login、organization 與 user fixtures。Architecture check 禁止 production 連結 |
| `scripts/check-architecture/` | `internal/httpapi`、`internal/api` 列為 app layer；`dbtest` 列為 test infrastructure；`db-only-in-storage` 新增 `through`：不追進 `internal/storage` 與 `internal/tenant` 內部的依賴（service 經由它們存取資料，自己不 import driver），另有 synthetic graph 測試（經 tenant 不違規、直接 import pgx 違規）。新規則 `db-handles-only-in-gateways`：storage 的基礎層 `internal/storage/postgres` 只能由 storage、tenant 與 cmd 依賴（review M2） |
| `Makefile` | `integration-test`：在新的 disposable DB 上跑 `db-test`（SQL tests，含 migration）與 `db-go-test`（storage、tenant、api、cmd/api，`-race`，skip 視為失敗）；worker 部分明確印出 NOT RUN（T-019） |
| `.github/workflows/ci.yml` | db job 改跑 `make integration-test` |
| `openapi/openapi.yaml` | 17 個有 request body 的 operation 補上 413（server 對每個 JSON body 都可能回 413）；`IdempotencyKey` 加上 pattern `^[!-~]{8,128}$` |
| `scripts/check_contracts.py` | 每個 request body 必須宣告 413 與 415；載入 openapi.yaml 時拒絕重複的 YAML key（PyYAML 預設保留最後一個，不報錯） |
| 測試基礎 | `db-go-test` 以 `-p 1` 依序執行 DB package（共用一個 database，部分測試改變 database 層級的狀態）；`dbtest` 與 tenant 測試建立 login 時容許另一個測試同時建立 |
| 文件 | ADR-019（ACCEPTED，H-006）；ARCHITECTURE.md §2；db/README.md |

## 獨立 review（2026-10-18，H-003）：CHANGES REQUESTED 與修正

Reviewer 以 `git archive fb6e265` 的 export 與自己的 disposable database 執行，沒有使用工作樹。結論：runtime 行為本身成立（授權順序、tenant filter、idempotency 含過期 key 的競爭、audit、error envelope 都經得起 probe；`integration-test`、56 RUN、mutation 數字皆重現），但 mutation 證據的分類有誤、新的 gateway 規則開了比 ADR-019 所寫更大的洞，另有數個 contract 與測試缺口。每項先寫會失敗的測試（`red-review1.log`；保護原本就正確之程式的測試，以它要擋的 mutant 證明會失敗），再修正：

| # | 發現 | 修正 | 證明 |
|---|---|---|---|
| M1 | **報告過度宣稱**：8 個「equivalent」中 3 個不是。`select-not-locked`：key 已 commit 且過期時，沒有 `FOR UPDATE` 的並行重試全部讀到「過期」、各自重新 claim、各建一個 project（reviewer probe：60 輪中 12–20 輪多建 3–6 個）；`cursor-length-unbounded`：以 JSON 空白補長的合法 cursor 超過 2048 仍可解碼；`guard-not-installed`：是沒有測試的一行，不是 equivalent。正確讀法是 156/159 被擋、3 個存活、5 個 equivalent；且首版證據由修正前的完整執行加 9 個 `--only` 拼成 | 三者都補上測試，不再列 equivalent；mutation 改為在最終的工作樹上單次完整執行 | `TestExpiredKeyRace`（30 輪，每輪先讓 key 過期，再 16 個並行重試；mutant 第 0 輪即 4 種不同回答）、`TestList` 的 2048／2050 字元 cursor（mutant：2050 得 200）、`TestServerSettings`（pool 的 `AfterRelease` 是 `tenant.Guard`） |
| M2 | **`through` 讓 app package 能自己開連線**：gateway 包含 `internal/storage/postgres`，它的 `Open` 接受任何連線字串；service 可 `postgres.Open` 後經 `tenant.Tx` 執行任意 SQL，完全繞過 `tenant.DB.Run`、`CheckRuntimeRole` 與 `Guard`，也就沒有 RLS，而 checker 回 PASS | 新規則 `db-handles-only-in-gateways`：只有 `internal/storage`、`internal/tenant` 與 `cmd` 可依賴 `internal/storage/postgres`；httpapi 原本為了兩個 sentinel 而 import 它，改由 `tenant.ErrRetryable`／`ErrOutcomeUnknown` 轉出；ADR-019 的限制說明改寫 | `TestRound2Probes`「service opening its own database handle」（reviewer 的 probe 位置）、`TestSyntheticGraph`（直接與經 ai）、`TestPolicy`；`tenant.TestRunErrors` |
| L1 | JSON 字串中的 U+0000 通過 schema，在 DB 變成 SQLSTATE 22021、回 500；共用 HTTP 層，之後每個文字欄位都會遇到 | `DecodeJSON` 拒絕含 U+0000 的值或成員名稱：422，detail 指向該值或該 object | `TestOutsideTheContract`（修正前 500）、`TestNULStrings`、`TestDecodeJSON` |
| L2 | 非 clean form 的 path（`/v1//projects`、`/v1/x/../projects`）得到 ServeMux 的 307 `text/html`，沒有 `X-Request-ID` 與 envelope；HEAD 由 GET 的 route 回 200，contract 沒有宣告 | `api.Handler` 對非 clean path 與 HEAD 回 404 envelope；`OPTIONS *` 由 net/http 自己回空的 200（不到 handler，記入 ADR） | `TestOutsideTheContract`（修正前：經 redirect 得到 200 的 list；HEAD 200） |
| L3 | 撤權可以在 write 進行中先 commit：`tenant.Run` 讀 membership 不上鎖（reviewer：editor 的 create 卡在 INSERT 時刪除其 membership 並 commit，放行後 create 仍 201） | 讀寫的 run 以 `FOR SHARE` 讀 membership，撤權或 role 變更等 write 結束；唯讀 run 不上鎖 | `tenant.TestRunLocksMembership`（修正前：role 變更在 run 進行中就 commit） |
| L4 | Server 的 timeouts、header 上限與 shutdown 等待時間沒有測試，拿掉任何一項都存活 | `newServer`、`poolConfig`、`shutdownGrace` 抽出並由測試鎖住 | `TestServerSettings`；每一項各一個 mutant |
| L5 | contract test 鎖的 `Route.Roles` 沒有被用來執行授權（service 寫死 `tenant.Writers`／`Members`） | service 與 `Routes` 共用 `createRoles`、`readRoles`，被鎖住的就是實際要求的 | `TestRoutesMatchContract`、`TestRoleMatrix`；mutant `create-roles-members`、`service-create-roles-bypassed` |
| L6 | 格式錯誤的 query（`?limit=1;cursor=x`）被 `r.URL.Query()` 默默丟棄，回 200 與預設頁 | `httpapi.Query`：格式錯誤回 400 | `TestOutsideTheContract`（修正前 3 個案例皆 200） |
| L7 | keyset 的 tie-break 測試依賴 query plan：拿掉 `id` 的 `ORDER BY` 在 index scan 下仍得到 id 順序 | `TestKeysetTies` 另以關閉 index scan 的 plan（heap 順序與 id 順序相反）分別測第一頁與逐頁 | mutant `keyset-first-page-without-id`（修正後的測試：順序相反）、`keyset-later-pages-without-id` |
| L8 | DB 無法連線時 log 為「refusing the database login」，註解說不含 URL 但含 host／user；GET 的 503 訊息叫 client 用同一 Idempotency-Key 重試 | 不安全的 login 與無法連線分開記錄（`refusing the database login`／`database unavailable`），註解改為「不含密碼」；503 訊息改為「重試（write 用同一 Idempotency-Key）」 | `TestRunNeedsTheDatabase`、`TestRunServesAndStops` |

`make integration-test` 在 worker 部分 NOT RUN 時 exit 0，reviewer 列為資訊項：target 明確印出 NOT RUN，本報告亦列出（見下）。

## 獨立複核（2026-10-18，H-003）：ACCEPT

同一 reviewer 以自己的 `git archive 98f5e9c` export 與新的 disposable database（`dt_test_review_2`、`dt_test_review_mut2`）複核，未使用工作樹。結論：

- 首輪全部發現已修，且經得起 probe。
- 報告數字全部重現：
  - `make integration-test` exit 0；62 RUN、0 SKIP、0 FAIL；
  - `backend-test`、`backend-race`、`architecture-check`、`spec-check` 皆 exit 0；
  - 新測試在負載下重複 5–10 次皆通過；
  - mutation 191 個：186 killed、5 equivalent、無 survivor、無靠 timeout 的 kill。單次執行在 133／191 被外部中止，其餘 58 個以 `--only` 在同一 tree 補跑，每個 mutant 各跑一次。
- Path normalisation 的 probe（absolute-form、`%2F`、`%2e%2e`、`%00`、`CONNECT`、`//`、`/./`、結尾斜線、HEAD）都回 envelope，沒有 redirect。

它另提出三個 low 發現，T-009 的 route 都觸發不到，處理如下（證據在 `checks/evidence/2026-10-18-T-009/review2/`）：

| # | 發現 | 處理 | 證明 |
|---|---|---|---|
| R1 | 讀寫 `Run` 的 `FOR SHARE` 與 D-036 的 owner 有序 `FOR UPDATE` 鎖會 deadlock：兩個 owner 同時變更成員時，reviewer probe 3／3 次 `40P01`，拿掉 `FOR SHARE` 則 0／3。T-009 沒有變更 membership 的 route | 記入 ADR-019（已知限制）與 T-010 scope：成員變更須用不先取 `FOR SHARE` 的 run 模式（先取 D-036 有序鎖，再重讀 caller role），並以兩個 owner 並行變更的測試證明 | 屬 T-010 實作；本任務不加未使用的模式 |
| R2 | U+0000 detail 的 JSON Pointer 沒有長度上限，可超過 `common.schema.json` 的 `path` `maxLength` 2048（`CreateProject` 只有 `/name`，觸發不到） | 改經 `model.NewIssue`，與 validator issue 一樣截到放得下的最深 ancestor | `red-r2.log`（3012 字元）→ `green-r2.log`；`mutant-r2.log`（還原為未截斷的 pointer 即失敗） |
| R3 | ADR-019 說經 `tenant.Tx` 寫的 SQL 仍「在 RLS 與 scope 之下」，不對：(a) `Run` 內可 `set_config` 改 tenant context 讀別的 organization；(b) `tenant.NewWorker` 可被 API 端呼叫，不查 membership 讀任何 organization，architecture-check 看不出來 | 改正 ADR-019；(b) 記入 T-019 scope（worker scope 移到只有 worker 端可 import 的 package＋architecture 規則；目前無 production 呼叫者）；(a) 與「`tenant.Tx` 縮成不含 `Conn()` 的介面」記入 T-036 | 文件 |
| 資訊 | `GET *`、`POST *` 由 net/http 回純文字 400，不到 handler | ADR-019 補記 | — |

修正後的檢查（`review2/`）：`backend-test`、`architecture-check`、`spec-check` exit 0；`make integration-test` 在新的 `dt_test_fresh4` exit 0（worker 部分 NOT RUN，T-019）。

## 實際執行結果

### Review 修正後（`checks/evidence/2026-10-18-T-009/review1/` 與同目錄的 mutation log）

| 指令 | Exit | 說明 |
|---|---|---|
| `make backend-test`、`backend-race`、`backend-numeric`、`backend-build` | 0 | `review1/*.log` |
| `make architecture-check` | 0 | `PASS architecture: 32 packages, 10 rules, 16 build configurations` |
| `make spec-check`、`make frontend-test` | 0 | |
| `make integration-test`（新 cluster 的新 database `dt_test_fresh3`） | 0 | `review1/integration-test.log` |
| `go test -p 1 -count=1 -v`（T-009 的 8 個 package，DB 設定） | 0 | 62 個 RUN，0 FAIL，0 SKIP（`review1/t009-tests.log`；首版 56，加上本輪 6 個） |
| `govulncheck@v1.9.0` | 0 | `No vulnerabilities found.` |
| 紅燈（`red-review1.log`） | 1 | 新測試在 fb6e265 的程式上：非 clean path 與 HEAD 得 200、格式錯誤的 query 200、U+0000 500、role 變更在 run 進行中 commit、checker 沒有新規則；保護原本正確之程式的測試以 mutant 證明：`select-not-locked`（第 0 輪 4 種回答）、`cursor-length-unbounded`（2050 字元得 200）、keyset 無 `id`（順序相反）。第一次執行時 `TestRunLocksMembership` 因測試本身對同一結果等待兩次而卡住，已改正並重跑 |
| Mutation，最終工作樹單次完整執行（`mutation-review1.log`） | 0 | **186/186 被擋，5 個 equivalent**（191 個 mutant；沒有任何一個只因 timeout 被擋）。新增 27 個：server 的每個 timeout、header 上限、ErrorLog、shutdown 等待時間；兩種 DB 錯誤訊息；membership 的鎖（拿掉、只用 KEY SHARE、唯讀也上鎖）；tenant 轉出的錯誤；U+0000 的檢查、前置過濾、成員名稱的 pointer、深層字串；query 格式；role 共用的值；HEAD 與非 clean path；keyset 兩個 query 的 tie-break；新規則與其 gateway。Equivalent 5 個（理由在 `mutants.py`）：`in-progress-unmapped`、`in-progress-unchecked`、`limit-error-ignored`、`complete-rows-unchecked`、`split-error-ignored`；review 指出的 3 個已改為一般 mutant 並被擋下 |
| 前一次完整執行（`mutation-review1-first.log`） | 1 | 185/186 被擋、5 equivalent，1 個存活：`nul-ignored-when-deep`（深度 10 以上的 U+0000 沒有測試）→ `TestNULStrings` 補上；`runtime-role-unchecked` 只因 10 分鐘 timeout 而被擋（拿掉 role 檢查後，無法連線 DB 的 run 開始服務、永不結束）→ cmd/api 的測試改為一開始服務就立即失敗 |
| GitHub Actions | 推送後補記 | |

### 首版（fb6e265）

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
| GitHub Actions run 39124340510（fb6e265） | success | 4/4：backend（含 architecture-check、govulncheck）、db migrations + API integration（`make integration-test`，PostgreSQL 16 service container）、frontend、spec-check |

### Mutation（首版，fb6e265）

> **更正（review M1）**：下文的「156/156 被擋，8 個 equivalent」分類有誤：`select-not-locked`、`cursor-length-unbounded`、`guard-not-installed` 不是 equivalent（見上表），正確為 156/159 被擋、3 個存活、5 個 equivalent。修正後的單次完整執行見「review 修正後」。

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
| `api.TestExpiredKeyRace` | 每輪讓 key 過期後 16 個並行重試，只重新 claim 一次、所有回答相同，30 輪 | D-011、API_DATABASE §7 |
| `api.TestOutsideTheContract` | 非 clean path、HEAD 與其他未宣告的 method 為 404 envelope；格式錯誤的 query 400；U+0000 422，跳脫的反斜線只是文字 | ADR-019、D-025 |
| `tenant.TestRunLocksMembership`、`TestRunErrors` | 讀寫 run 進行中 role 變更等待，唯讀 run 不擋；httpapi 對應的錯誤就是 `Run` 回傳的 | D-008 |
| `cmd/api.TestServerSettings` | Timeouts、header 上限、ErrorLog、shutdown 等待時間與 pool 的 `tenant.Guard` | ADR-019 |
| `project.TestRoutesMatchContract`、`TestCursor`、`TestProjectIDMatchesSchema`、`httpapi.TestOrganizationIDMatchesSchema` | Route、method、operationId 與 role 與 `planning/api-operations.json` 相同；cursor round trip 與拒絕；ID 的 regex 就是 `common.schema.json` 的 pattern | D-012、D-001 |
| `cmd/api` 測試 | 設定錯誤 exit 1；DB 無法連線 exit 3 且不洩漏密碼；superuser login 拒絕；API login 服務 route 並在 context 結束後 exit 0 | ADR-019 |
| `scripts/check-architecture` | 真實 repo 通過；synthetic graph：經 tenant 存取 DB 不違規、直接 import pgx 違規；新的 app layer 與 test infra 項目 | ARCHITECTURE §2 |

## 需 owner 知悉

- ADR-019（HTTP 層的分工、錯誤對應、request ID、body 規則、idempotency 與 cursor 的設計）已由 owner 批准為 ACCEPTED（H-006，2026-10-18）。
- 一般 API 的 rate limiting（429 `RATE_LIMITED`）原本沒有任務負責（T-036 只涵蓋 BFF 的 login／refresh，T-023 等為 admission quota）；owner 同意併入 T-036（H-006），已加入其 scope。
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
