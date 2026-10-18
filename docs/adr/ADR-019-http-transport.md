# ADR-019 — HTTP transport, error envelope and request scope (T-009)

狀態：ACCEPTED（T-009 agent 選定並先實作；owner 於 2026-10-18 批准，H-006）。日期：2026-10-18。ADR-001 已鎖定 `net/http`、不引入重型 framework；本 ADR 固定第一批 route（projects）所建立、之後每個 route 共用的 HTTP 層。

## Decision

### Package 分工

| Package | 內容 |
|---|---|
| `internal/httpapi` | error envelope、request ID、JSON body 解碼與 schema 驗證、驗證 token 並組出 `tenant.Scope` 的 middleware |
| `internal/api` | 把所有 route 組成一個 `http.Handler`。`cmd/api` serve 的與整合測試使用的是同一個 handler |
| `internal/<feature>`（如 `internal/project`） | service 與 handler |
| `internal/storage/<table>` | SQL |
| `internal/ids` | D-001 ID（`<prefix>_<ULID>`） |
| `internal/storage/dbtest` | test-only 的 DB fixture，architecture check 禁止 production 連結 |

Router 用標準函式庫的 `http.ServeMux` method pattern（`POST /v1/projects`）。其他 path 或 method 一律回 404 `NOT_FOUND` envelope。

### 錯誤

- Handler 回傳 error，由 `httpapi.WriteError` 決定 status 與 body。成功的回應經 `httpapi.WriteJSON`／`WriteBody` 送出；每個回應（含錯誤）都帶 `Content-Type: application/json`、`X-Content-Type-Options: nosniff`、`Cache-Control: no-store`。格式是 `common.schema.json#/$defs/Error`：`code`、`message`、`details[{path, reason, code?, severity?}]`、`request_id`。
- `httpapi.Status` 與 schema 的 `x-http-status` 由 contract test 鎖成相同（D-025）。
- 各套件的 sentinel error 對應：

  | 錯誤 | 回應 |
  |---|---|
  | `auth.ErrInvalidToken` | 401，帶 `WWW-Authenticate` |
  | `auth.ErrKeysUnavailable` | 503 |
  | `tenant.ErrUnknownUser` | 403 |
  | `tenant.ErrNoMembership` | 404（不揭露 organization 是否存在） |
  | `tenant.ErrForbidden` | 403 |
  | `tenant.ErrRetryable`、`ErrOutcomeUnknown`（即 `postgres.Classify` 的錯誤；httpapi 不 import storage 基礎層） | 503，提示重試（write 用同一 Idempotency-Key） |

- 其他錯誤一律 500 `INTERNAL_ERROR`，訊息固定為 `internal error`，原因只寫 log（帶 request_id）。
- Panic 轉為 500；`http.ErrAbortHandler` 照原意中止回應。
- Contract 以外的 request 也回 envelope（T-009 review L2）：非 clean form 的 path（ServeMux 原本會 307 redirect）與 HEAD（ServeMux 原本交給 GET 的 handler）都是 404 `NOT_FOUND`；其他未宣告的 method 亦同。`OPTIONS *` 由 net/http 自己回空的 200，`GET *`、`POST *` 等其他 method 的 `*` 由 net/http 回純文字 400，都不到 handler（不是 envelope）。

### Request ID

- 每個 request 都由 server 發 `req_<ULID>`，放在 `X-Request-ID`，同時寫進 error body 與 audit row。
- Client 送來的 `X-Request-ID` 一律不用，因為它會進 audit。

### Body

- Content-Type 必須是 `application/json`，只接受 `charset=utf-8` 參數（media type、參數名稱與值皆不分大小寫），否則 415；參數格式錯誤也是 415。
- 大小上限預設 1 MiB，超過回 413。每個有 request body 的 operation 都在 openapi 宣告 413 與 415，`scripts/check_contracts.py` 檢查。讀取中斷的 body 回 400，即使已讀到的部分是合法 JSON。
- 解析失敗回 400 `INVALID_REQUEST`，涵蓋不合法 JSON、重複成員名稱、非 UTF-8、尾端多餘資料，detail 附 JSON Pointer。
- 字串（值或成員名稱）含 U+0000 回 422 `REQUEST_VALIDATION_FAILED`，detail 指向該值或該 object：PostgreSQL 的 text 無法存放它，否則會在 DB 變成 500（T-009 review L1）。
- Query string 格式錯誤（例如 `;` 分隔、無法解碼的 `%`）回 400，不只讀其中能讀的部分（`httpapi.Query`，T-009 review L6）。
- 對 `schemas/` 的 request schema 驗證不過回 422 `REQUEST_VALIDATION_FAILED`，每個失敗的 JSON Pointer 各一個 detail。用的是 generic validator 同一份 schema，不另寫一套。Schema reference 無法解析，或通過 schema 的 body 無法解碼成 Go type（schema 與 type 不一致），是 server 的錯：500，不會當成功處理。

### Scope

- `Scoped` 依序處理：
  1. 只讀單一 `Authorization: Bearer`（ADR-017），先驗 token；
  2. 要求恰好一個合法格式的 `X-Organization-ID`，否則 400；
  3. 以 `tenant.ResolveUser` 解析 user；
  4. 把 scope 交給 handler。
- Header 只是 selector（D-008）。Membership 與 role 由 handler 在 `tenant.DB.Run` 的同一個 transaction 中檢查：非 member 回 404，role 不足回 403。
- 讀寫的 run 以 `FOR SHARE` 鎖住它檢查過的 membership row 直到 transaction 結束：同時發生的撤權或 role 變更會等這個 write commit，不會先 commit 而讓 write 繼續（T-009 review L3）。唯讀的 run 不上鎖。
  - **已知限制（T-009 re-review R1，latent）**：D-036 的成員變更 transaction 依 user_id 順序以 `FOR UPDATE` 鎖該 organization 全部 owner membership。若它跑在 `Run(ReadWrite)` 之上，每個 owner 已先持有自己 row 的 `FOR SHARE`，兩個 owner 同時變更成員會形成 lock cycle（reviewer probe：3／3 次 `40P01`，回 `ErrRetryable`／503）。T-009 沒有變更 membership 的 route。T-010 必須提供不先取 `FOR SHARE` 的 run 模式：先取 D-036 的有序鎖（owner caller 自己的 row 在其中），再重讀 caller 的 role；並加上在 `Run` 之上執行 D-036 的並行測試。
- Route 的 role 與 `planning/api-operations.json` 的 `x-required-roles` 由 contract test 鎖住；service 要求的是同一組值（`createRoles`、`readRoles`），所以被鎖住的就是實際執行的（T-009 review L5）。

### 寫入

一個 write 的效果、audit event 與 idempotency response 在同一個 transaction 中完成：

- **Idempotency（D-011）**：
  - Key 以 organization、actor、operation 為 scope。之後有 path 參數或 concurrency header 的 write，須把 resource 與這些 header 納入 operation scope 或 request hash（API_DATABASE §7）；`createProject` 沒有。
  - Role 在 claim 與重播之前檢查：被降級或移除的 user 重試舊 key 得到 403／404，不是舊的成功回應（API_DATABASE §7）。
  - Request hash 是 body 的 JCS canonical form 的 SHA-256。
  - 同 key、同 hash 重播已存的 status 與 body，不同 hash 回 409 `IDEMPOTENCY_KEY_CONFLICT`。
  - 兩個並行 request 搶同一個 key 時，由 primary key 序列化：後到的等前者 commit 後重播。
  - 保留 24 小時，過期後重新 claim。已存在的 key 以 `FOR UPDATE` 讀取：搶同一個過期 key 的重試依序讀到前一個留下的狀態，只有第一個重新 claim（T-009 review M1）。
  - Response 以 JCS canonical form 送出，jsonb 往返後重播的 bytes 仍相同。
  - `Idempotency-Key` 只接受 8–128 個可見 ASCII 字元（openapi `IdempotencyKey` 的 pattern `^[!-~]{8,128}$`）。
- **Audit**：`internal/storage/auditlog` 在同一個 transaction 寫 `twin.audit_events`，`actor_id` 為 scope 的 user；DB trigger 也會強制這點。Metadata 只放 ID 與計數。

### List

- 依 `(created_at, id)` 排序。
- Cursor 是 base64url 的 `{v, o, l, t, i}`，綁 organization 與 list，未簽章：它只能在 caller 自己 organization 的資料內移動，由 RLS 與 organization filter 限制。
- 用在別的 organization 或 list、或被改動過的 cursor 回 400。
- `limit` 為 1–100 的十進位整數，預設 50。

### DB rule 的 gateway

- `db-only-in-storage` 改為不追進 `internal/storage` 與 `internal/tenant` 內部的依賴（`through`）：其他 package 經由它們存取資料，自己不 import driver。`tenant.Tx`／`tenant.TxOptions` 是 pgx 的 alias，service 持有 transaction 只為交給 storage。
- 這兩個 gateway 包含 storage 的基礎層 `internal/storage/postgres`，它的 `Open` 接受任何連線字串：經由它，一個 package 可以自己開連線，繞過 `tenant.DB.Run`、`CheckRuntimeRole` 與 `Guard`（T-009 review M2）。新規則 `db-handles-only-in-gateways`：只有 `internal/storage`、`internal/tenant` 與 `cmd` 可以依賴它；httpapi 需要的錯誤由 `tenant` 轉出（`tenant.ErrRetryable`、`ErrOutcomeUnknown`）。
- Import 規則看不到 SQL 字串：service 透過 `tenant.Tx` 自己寫 SQL（包括經 `tx.Conn()` 取得的連線）這件事只能靠 review 擋下（`internal/project` 沒有 SQL）。這些 SQL 在 `tenant.DB.Run` 的 transaction 內以同一個 API login 執行，但**不受 scope 約束**：它可以 `set_config('app.organization_id', …)` 改掉 transaction 的 tenant context，讀別的 organization（T-009 re-review R3a）。所以 SQL 只寫在 `internal/storage`，由 review 把關；T-036 評估把 `tenant.Tx` 縮成沒有 `Conn()` 的介面。
- Worker scope（R3b）：`tenant.NewWorker` 在 `internal/tenant`，API 端程式也能 import，以任何 organization 執行而不查 membership，import 規則看不出來。目前沒有 production 呼叫者。T-019 必須把它移到只有 worker 端可 import 的 package，並加 architecture 規則。

### Server

`cmd/api` 的啟動與關閉：

- **啟動**：
  - 以 API runtime login 連 PostgreSQL；superuser、BYPASSRLS 或 table owner 都會被拒絕（`tenant.CheckRuntimeRole`，log 為 `refusing the database login`）；DB 無法連線時 log 為 `database unavailable`，含 host、user、database，不含密碼。
  - Pool 裝 `tenant.Guard`（`poolConfig`）。
  - Timeouts：read header 10 s、read 30 s、write 60 s、idle 120 s；header 上限 32 KiB（`newServer`）。兩者與 shutdown 等待時間由測試鎖住，因為少了任何一項都不會反映在回應上（T-009 review M1、L4）。
  - 監聽 `API_LISTEN_ADDR`（預設 `:8080`）。
- **關閉**：SIGINT／SIGTERM 時 graceful shutdown，最多等 20 s。

## Consequences

- 之後每個 route 只寫 service、handler 與 storage，錯誤格式、request ID、body 規則與 scope 都共用。
- 每個 request 開兩個 transaction：解析 user 的唯讀 transaction，加上 handler 的 transaction。若之後需要，可以快取 user 解析結果。
- 未簽章的 cursor 依賴 RLS 與 organization filter；若將來 cursor 要攜帶 filter，需要在 cursor 中綁定該 filter。
- Rate limiting（429）與 OTel tracing 尚未實作，分屬 T-035／T-036。
