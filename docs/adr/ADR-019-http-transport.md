# ADR-019 — HTTP transport, error envelope and request scope (T-009)

狀態：PROPOSED（T-009 agent 選定，依 owner 授權先實作；owner 同意後改為 ACCEPTED）。日期：2026-10-18。ADR-001 已鎖定 `net/http`、不引入重型 framework；本 ADR 固定第一批 route（projects）所建立、之後每個 route 共用的 HTTP 層。

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
  | `postgres.ErrRetryable`、`ErrOutcomeUnknown` | 503，提示以同一 Idempotency-Key 重試 |

- 其他錯誤一律 500 `INTERNAL_ERROR`，訊息固定為 `internal error`，原因只寫 log（帶 request_id）。
- Panic 轉為 500；`http.ErrAbortHandler` 照原意中止回應。

### Request ID

- 每個 request 都由 server 發 `req_<ULID>`，放在 `X-Request-ID`，同時寫進 error body 與 audit row。
- Client 送來的 `X-Request-ID` 一律不用，因為它會進 audit。

### Body

- Content-Type 必須是 `application/json`，只接受 `charset=utf-8` 參數（media type、參數名稱與值皆不分大小寫），否則 415；參數格式錯誤也是 415。
- 大小上限預設 1 MiB，超過回 413。每個有 request body 的 operation 都在 openapi 宣告 413 與 415，`scripts/check_contracts.py` 檢查。讀取中斷的 body 回 400，即使已讀到的部分是合法 JSON。
- 解析失敗回 400 `INVALID_REQUEST`，涵蓋不合法 JSON、重複成員名稱、非 UTF-8、尾端多餘資料，detail 附 JSON Pointer。
- 對 `schemas/` 的 request schema 驗證不過回 422 `REQUEST_VALIDATION_FAILED`，每個失敗的 JSON Pointer 各一個 detail。用的是 generic validator 同一份 schema，不另寫一套。Schema reference 無法解析，或通過 schema 的 body 無法解碼成 Go type（schema 與 type 不一致），是 server 的錯：500，不會當成功處理。

### Scope

- `Scoped` 依序處理：
  1. 只讀單一 `Authorization: Bearer`（ADR-017），先驗 token；
  2. 要求恰好一個合法格式的 `X-Organization-ID`，否則 400；
  3. 以 `tenant.ResolveUser` 解析 user；
  4. 把 scope 交給 handler。
- Header 只是 selector（D-008）。Membership 與 role 由 handler 在 `tenant.DB.Run` 的同一個 transaction 中檢查：非 member 回 404，role 不足回 403。
- Route 的 role 與 `planning/api-operations.json` 的 `x-required-roles` 由 contract test 鎖住。

### 寫入

一個 write 的效果、audit event 與 idempotency response 在同一個 transaction 中完成：

- **Idempotency（D-011）**：
  - Key 以 organization、actor、operation 為 scope。之後有 path 參數或 concurrency header 的 write，須把 resource 與這些 header 納入 operation scope 或 request hash（API_DATABASE §7）；`createProject` 沒有。
  - Role 在 claim 與重播之前檢查：被降級或移除的 user 重試舊 key 得到 403／404，不是舊的成功回應（API_DATABASE §7）。
  - Request hash 是 body 的 JCS canonical form 的 SHA-256。
  - 同 key、同 hash 重播已存的 status 與 body，不同 hash 回 409 `IDEMPOTENCY_KEY_CONFLICT`。
  - 兩個並行 request 搶同一個 key 時，由 primary key 序列化：後到的等前者 commit 後重播。
  - 保留 24 小時，過期後重新 claim。
  - Response 以 JCS canonical form 送出，jsonb 往返後重播的 bytes 仍相同。
  - `Idempotency-Key` 只接受 8–128 個可見 ASCII 字元（openapi `IdempotencyKey` 的 pattern `^[!-~]{8,128}$`）。
- **Audit**：`internal/storage/auditlog` 在同一個 transaction 寫 `twin.audit_events`，`actor_id` 為 scope 的 user；DB trigger 也會強制這點。Metadata 只放 ID 與計數。

### List

- 依 `(created_at, id)` 排序。
- Cursor 是 base64url 的 `{v, o, l, t, i}`，綁 organization 與 list，未簽章：它只能在 caller 自己 organization 的資料內移動，由 RLS 與 organization filter 限制。
- 用在別的 organization 或 list、或被改動過的 cursor 回 400。
- `limit` 為 1–100 的十進位整數，預設 50。

### DB rule 的 gateway

`db-only-in-storage` 改為不追進 `internal/storage` 與 `internal/tenant` 內部的依賴（`through`）：其他 package 經由它們存取資料，自己不 import driver。`tenant.Tx`／`tenant.TxOptions` 是 pgx 的 alias，service 持有 transaction 只為交給 storage。

Import 規則看不到 SQL 字串：service 透過 `tenant.Tx` 自己寫 SQL 這件事，只能靠 review 擋下（`internal/project` 沒有 SQL）。

### Server

`cmd/api` 的啟動與關閉：

- **啟動**：
  - 以 API runtime login 連 PostgreSQL；superuser、BYPASSRLS 或 table owner 都會被拒絕（`tenant.CheckRuntimeRole`）。
  - Pool 裝 `tenant.Guard`。
  - Timeouts：read header 10 s、read 30 s、write 60 s、idle 120 s；header 上限 32 KiB。
  - 監聽 `API_LISTEN_ADDR`（預設 `:8080`）。
- **關閉**：SIGINT／SIGTERM 時 graceful shutdown，最多等 20 s。

## Consequences

- 之後每個 route 只寫 service、handler 與 storage，錯誤格式、request ID、body 規則與 scope 都共用。
- 每個 request 開兩個 transaction：解析 user 的唯讀 transaction，加上 handler 的 transaction。若之後需要，可以快取 user 解析結果。
- 未簽章的 cursor 依賴 RLS 與 organization filter；若將來 cursor 要攜帶 filter，需要在 cursor 中綁定該 filter。
- Rate limiting（429）與 OTel tracing 尚未實作，分屬 T-035／T-036。
