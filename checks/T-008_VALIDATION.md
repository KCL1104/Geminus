# T-008 驗證報告：OIDC／JWT authentication

**日期：2026-10-18｜狀態：`review`（等待獨立 review，依 H-003 驗收）｜需求：REQ-001、REQ-002、REQ-019｜Gate：D-008（已批准）｜實作選擇登記為 ADR-017（PROPOSED，待 owner 同意）**

逐條輸出在 `checks/evidence/2026-10-15-T-008/`。範圍依 tasks.json 的 scope：「先用可替換 verifier + signed test tokens」——**不選定 auth provider**（仍為 TBD），只固定 API 如何驗證任何 OIDC provider 的 access token。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/auth/auth.go` | `Identity`（issuer、subject、expiry）、`Verifier` 介面、`ErrInvalidToken`（→401）／`ErrKeysUnavailable`（→503）、帶 reason code 的 `TokenError`（不含 token 或 claims）、identity 的 context 存取 |
| `internal/auth/oidc.go` | `OIDCVerifier`：只接受 compact JWS、≤8 KiB；alg allowlist（預設 RS256、ES256；只允許非對稱演算法，`none`／HMAC 無法設定）；`kid` 必填；拒 `jku`、`x5u`、`jwk`、`x5c`、`x5t`、`x5t#S256`、`crit`、`b64`、`zip`、`enc`；`typ` 只能是 JWT／at+jwt；header 與 payload 的重複成員拒絕；key 的 `alg` 與類型／curve 必須符合；簽章由 go-jose v4.1.5 驗證；`iss` 完全相同、`aud` 包含設定值、`exp` 必填、`nbf`／`iat` 檢查（預設 30 秒 skew，最多 5 分鐘）、NumericDate 範圍、`sub` ≤255 bytes 且可列印。JWKS：https、不跟隨 redirect、10 秒 timeout、≤256 KiB、只取公開的簽章 key（RSA ≥2048、P-256／384／521、Ed25519）、重複 `kid` 皆不用；首次使用時抓取，未知 `kid` 觸發 refresh 但至少間隔 1 分鐘，1 小時後 refresh，refresh 失敗時舊 key 最多用 24 小時；refresh 不受單一 request 取消影響 |
| `internal/auth/http.go` | `BearerToken`（只讀單一 `Authorization: Bearer`；URL 中的 `access_token`／`id_token` 拒絕；不讀 cookie）、`Challenge`（RFC 6750）、`Middleware`（失敗時呼叫注入的 error writer，不進入 handler；非預期的 verifier 錯誤一律視為 invalid token） |
| `internal/auth/authtest/` | test-only OIDC provider：產生 RSA／EC／Ed25519 key、以 TLS 提供 JWKS、可完全控制 header 與 payload bytes（含 `alg:none`、HMAC 以公鑰為 secret 等攻擊 token）、可暫停 JWKS 回應與計數。`scripts/check-architecture` 禁止 production code 連結它 |
| `internal/tenant/transaction.go` | `ResolveUser(ctx, issuer, subject)`：唯讀 transaction 內以 `set_config(..., true)` 設定 `app.oidc_issuer`／`app.oidc_subject`（users 的 RLS policy `identity_self` 讀取它們），再加 application filter 查詢；沒有 user 回 `ErrUnknownUser`（不自動建立，D-008 受控 provisioning）；空或過長回 `ErrInvalidIdentity` |
| `internal/config/auth.go` | `LoadAuth`：`AUTH_OIDC_ISSUER`、`AUTH_OIDC_AUDIENCE`、`AUTH_OIDC_JWKS_URL`、可選 `AUTH_OIDC_ALGORITHMS`；錯誤只帶變數名稱，不帶值 |
| `cmd/api` | 載入並驗證 auth 設定、建立 verifier（不連線 provider）；設定不安全（例如 HS256）時以 exitConfig 結束。仍因沒有 route（T-013）而拒絕啟動 |
| `scripts/check-architecture/rules.go` | `authtest` 列為 test infrastructure；新增 `auth-authenticates-only`（auth 不依賴 storage、tenant、DB driver、domain、AI…）；3 個新 probe |
| `go.mod`／`go.sum` | `github.com/go-jose/go-jose/v4 v4.1.5`（Apache-2.0，2026-09-03；v4.1.6 為前一天發布，依 AGENTS.md 不採用） |
| 文件 | ADR-017（PROPOSED）；SECURITY.md §3 實作段落；ARCHITECTURE.md §2；DECISIONS_AND_TBD「Auth provider」列註明 provider 仍待決 |

## 實際執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `go test -count=1 -v ./internal/auth/... ./internal/config/... ./cmd/...` | 0 | 39 個 RUN 全 PASS（`auth-tests.log`） |
| `go test -race -count=3 ./internal/auth/...` | 0 | 並行 verify 與 JWKS refresh（`auth-race.log`） |
| `DT_ALLOW_DB_TESTS=yes make db-test`（一次性 PostgreSQL 16，`dt_test_t008`） | 0 | migration up/down/up 與 SQL 測試（`db-test.log`） |
| `make db-go-test` | 0 | 含新的 `TestResolveUser`（`GEMINUS_REQUIRE_DB_TESTS=1`，skip 會失敗；`db-go-test.log`） |
| `make architecture-check` | 0 | `PASS architecture: 24 packages, 9 rules, 16 build configurations`（與 T-042 review 後的 build matrix 合併後） |
| `govulncheck ./cmd/... ./internal/... ./schemas/... ./scripts/...` | 0 | No vulnerabilities found（含 go-jose v4.1.5） |
| Mutation（`mutants.py`，已提交） | 0 | **54/54 被擋，2 個 equivalent**（`mutation.log`）。首輪 45/56：`key-alg-ignored`、`curve-ignored`、`jwks-size-unlimited`、`jwks-status-ignored`、`fetch-cancelled-with-request`、`middleware-continues-after-failure` 存活 → 補 `TestKeyAlgorithmPinned`、JWKS 錯誤狀態帶合法內容、合法 JSON 後補空白超過 256 KiB、`TestCancelledRequestStillFetches`、handler 呼叫次數；3 個 mutant 寫法未編譯已改寫；`private-keys-used`（private JWK 解析為 private key 型別，`fits` 不接受，IsPublic 為縱深防禦）與 `resolve-subject-only`（RLS 已限制為同一 issuer／subject，WHERE 是 AGENTS.md 要求的 application filter）列為 equivalent 並在腳本說明 |
| `make backend-test`／`backend-race`／`backend-build`／`REQUIRE_GOAMD64_V3=1 backend-numeric`／`spec-check`／`frontend-test` | 合併後補記 | |
| GitHub Actions | 推送後補記 | |

## 驗收對照

| 測試 | 驗證內容 | 對應 |
|---|---|---|
| `TestValidTokens` | RS256／ES256、aud 陣列、typ at+jwt、無 typ、無 nbf／iat、255 bytes 的 sub | REQ-001 |
| `TestRejectedTokens`（53 個案例） | 格式、大小、`alg:none`、HS256 以公鑰／JWK 為 secret、未允許的 alg、alg 與 key 類型不符、無 kid、`jku`／`x5u`／嵌入 `jwk`／`x5c`／`crit`／`b64`、typ、未知 kid、同 kid 不同 key 的簽章、簽後改 payload、claims 型別、重複 claim、iss／aud／exp／nbf／iat／sub 各種錯誤；每個都是 `ErrInvalidToken` 且錯誤訊息不含 token 或 subject | SECURITY §3、REQ-002 |
| `TestClockSkewBoundaries`、`TestKeyAlgorithmPinned` | skew 邊界精確；JWK 的 alg 釘住同家族的演算法、curve 必須相符 | SECURITY §3 |
| `TestKeyRotation`、`TestJWKSUnavailable`、`TestJWKSContent`、`TestJWKSRedirectNotFollowed`、`TestConcurrentVerify`、`TestCancelledRequestStillFetches` | 輪替的 key 在間隔後生效、100 個隨機 kid 不造成額外抓取、移除的 key 在 KeyMaxAge 後失效；provider 失效時的 503 與 24 小時上限；私鑰、重複 kid、enc key、1024-bit RSA、未允許 alg、未知 key 類型、錯誤狀態、過大內容；不跟隨 redirect；32 個並行首次使用只抓一次 | SECURITY §3「key rotation」「endpoints 僅 deployment allowlist」 |
| `TestConfigValidation`、`config.TestLoadAuth`、`cmd/api` 測試 | http、query、fragment、credentials、HS256、none、skew 範圍；錯誤不回顯值；API 在設定不安全時不啟動 | SECURITY §3 |
| `TestBearerToken`、`TestChallenge`、`TestMiddleware` | 只接受單一 Bearer header；URL token 拒絕；RFC 6750 challenge；失敗時 handler 不執行；非預期錯誤 fail closed | TEST-009（authentication 部分）、SECURITY §3 |
| `tenant.TestResolveUser`（DB） | 兩個 tenant 的 user 各自解析；未知 subject、同 subject 不同 issuer、issuer 多一個斜線、大小寫、SQL 與 LIKE 字元皆為 `ErrUnknownUser`；長度檢查；查詢後連線上沒有 identity settings；RLS 只讓該 identity 看到自己的 row；API login 無法新增 user | TEST-009（user 解析）、TEST-011（API login 受 RLS 約束）、D-008 |
| `scripts/check-architecture` probes | cmd 連結 `authtest`、auth 依賴 tenant 或 pgx 都被擋 | 「production 禁用 test verifier」 |

## 需 owner 知悉

- **ADR-017（PROPOSED）**：provider-agnostic 的驗證方式與 go-jose 的採用。Owner 同意後改為 ACCEPTED。
- **仍待決**：auth provider 本身；browser 端 token 儲存、refresh 與是否採 BFF／cookie（SECURITY §3 要求另立 auth ADR，影響前端 T-014 之後的登入流程）；驗證失敗的 rate limiting 與告警（T-035）。
- 已撤銷的 key 最多 1 小時後才不被接受（provider 也無法連線時最多 24 小時）；若需要更快，調整 KeyMaxAge 或另加 introspection。

## 與規劃的差異

- 規劃輸出 `internal/auth/` 與 `cmd/api/` 皆已處理；另在 `internal/tenant` 新增 `ResolveUser`，因為 user 解析需要 DB，而 auth 依架構規則不得接觸 DB。
- HTTP error envelope（需要 request_id）屬 T-013；`auth.Middleware` 以注入的 error writer 回應，不另定義錯誤格式。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| `make integration-test` | target 尚未實作（需要 API routes 與 worker，T-013 起） |
| TEST-009 的完整 route × role 矩陣（read／write／list／download／SSE） | routes 屬 T-013 之後；本任務只提供 authentication 與 user 解析 |
| 真實 OIDC provider 的整合測試 | provider 未選定；以 TLS 的 test provider 驗證協定行為 |
| 獨立 review | 依 H-003 推送後進行 |
