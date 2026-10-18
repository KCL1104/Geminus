# T-008 驗證報告：OIDC／JWT authentication

**日期：2026-10-18｜狀態：`review`（首輪獨立 review CHANGES REQUESTED → 已先紅後綠修正，等待同一 reviewer 複核；依 H-003 驗收）｜需求：REQ-001、REQ-002、REQ-019｜Gate：D-008（已批准）｜實作選擇：ADR-017（2026-10-18 owner 同意，ACCEPTED）**

逐條輸出：首版在 `checks/evidence/2026-10-15-T-008/`，review 修正後在 `checks/evidence/2026-10-18-T-008-review/`。範圍依 tasks.json 的 scope：「先用可替換 verifier + signed test tokens」。本任務只固定 API 如何驗證任何 OIDC provider 的 access token；provider（Auth0）與 browser session（BFF）由 ADR-018 決定，屬 T-043。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/auth/auth.go` | `Identity`（issuer、subject、expiry）、`Verifier` 介面、`ErrInvalidToken`（→401）／`ErrKeysUnavailable`（→503）、帶 reason code 的 `TokenError`（不含 token 或 claims；`ReasonKeys` 保留抓取失敗的原因）、identity 的 context 存取 |
| `internal/auth/oidc.go` | `OIDCVerifier`：只接受 compact JWS、≤8 KiB、三段皆為 strict base64url；alg allowlist（預設 RS256、ES256；只允許非對稱演算法，`none`／HMAC 無法設定）；`kid` 必填；拒 `jku`、`x5u`、`jwk`、`x5c`、`x5t`、`x5t#S256`、`crit`、`b64`、`zip`、`enc`；`typ` 只能是 JWT／at+jwt，`RequireAccessTokenType` 時必須是 at+jwt；header 與 payload 的重複成員拒絕；key 的 `alg` 與類型／curve 必須符合；簽章由 go-jose v4.1.5 驗證；`iss` 完全相同、`aud` 包含設定值、`exp` 必填、`nbf`／`iat` 檢查（預設 30 秒 skew，最多 5 分鐘）、NumericDate 範圍、`sub` ≤255 bytes 且只含可列印字元（拒控制、格式、分隔、私用字元）。JWKS：https、不跟隨 redirect、`FetchTimeout`（預設 10 秒，≤1 分鐘）、≤256 KiB、只取公開的簽章 key（RSA ≥2048、P-256／384／521、Ed25519）、重複 `kid` 皆不用。**Stale-while-revalidate**：快取的 key 立即使用，KeyMaxAge（1 小時）後背景 refresh，refresh 失敗時舊 key 最多用到 KeyMaxStale（24 小時）；未知 `kid` 等 refresh，但只等到 request context 結束；single-flight；兩次抓取至少間隔 MinRefreshInterval（1 分鐘），沒有可用 key 時 5 秒；抓取在自己的 goroutine 與 context 中執行、panic 轉為錯誤；錯誤保留原因、去掉 URL。設定：issuer／JWKS URL ≤2048 bytes，1 秒 ≤ MinRefreshInterval ≤ KeyMaxAge ≤ KeyMaxStale、MinRefreshInterval ≤24 小時、KeyMaxStale ≤7 天；錯誤不回顯值 |
| `internal/auth/http.go` | `BearerToken`：只讀單一 `Authorization: Bearer`；URL query 中任何大小寫、escape、`;` 分隔的 `access_token`／`id_token`，以及無法解讀的參數名稱，一律拒絕；不讀 cookie；他種 scheme 為 `not_bearer`、非 token68 為 `malformed`。`Challenge`（RFC 6750 §3）：沒有 Bearer credentials 時不帶 error code，`invalid_request`（多個 header、`Bearer` 無 token、URL token），`invalid_token`（其他），keys unavailable（503）不帶 challenge。`Middleware`：失敗時呼叫注入的 error writer，不進入 handler；非預期的 verifier 錯誤一律視為 invalid token |
| `internal/auth/authtest/` | test-only OIDC provider：產生 RSA／EC／Ed25519 key、以 TLS 提供 JWKS、可完全控制 header 與 payload bytes（含 `alg:none`、HMAC 以公鑰為 secret 等攻擊 token）、可暫停 JWKS 回應與計數。`scripts/check-architecture` 禁止 production code 連結它 |
| `internal/tenant/transaction.go` | `ResolveUser(ctx, issuer, subject)`：唯讀 transaction 內以 `set_config(..., true)` 設定 `app.oidc_issuer`／`app.oidc_subject`（users 的 RLS policy `identity_self` 讀取它們），再加 application filter 查詢，並確認 transaction 確實唯讀；沒有 user 回 `ErrUnknownUser`（不自動建立，D-008 受控 provisioning）；空、過長、非 UTF-8 或含 NUL 回 `ErrInvalidIdentity` |
| `internal/config/auth.go` | `LoadAuth`：`AUTH_OIDC_ISSUER`、`AUTH_OIDC_AUDIENCE`、`AUTH_OIDC_JWKS_URL`、可選 `AUTH_OIDC_ALGORITHMS` 與 `AUTH_OIDC_REQUIRE_AT_JWT`（`true`／`false`／未設定）；錯誤只帶變數名稱，不帶值 |
| `cmd/api` | 載入並驗證 auth 設定、建立 verifier（不連線 provider）；設定不安全（例如 HS256）時以 exitConfig 結束。仍因沒有 route（T-013）而拒絕啟動 |
| `scripts/check-architecture/rules.go` | `authtest` 列為 test infrastructure；`auth-authenticates-only`（auth 不依賴 storage、tenant、DB driver、domain、AI…）；3 個 probe |
| `go.mod`／`go.sum` | `github.com/go-jose/go-jose/v4 v4.1.5`（Apache-2.0，2026-09-03；v4.1.6 於 2026-10-14 發布，依 AGENTS.md 未滿兩週不採用） |
| 文件 | ADR-017（ACCEPTED，含 review 修訂段落）；SECURITY.md §3；ARCHITECTURE.md §2；DECISIONS_AND_TBD「Auth provider」列指向 ADR-018 |

## 獨立 review（2026-10-18，H-003）與修正

Reviewer 未找到繞過 token 驗證的方法（所有演算法、key、header、編碼與 claims 攻擊皆被拒），但要求修改：一個中度可用性缺陷、兩個證明力不足的安全測試、數個報告過度宣稱。每項先寫會失敗的測試（`go test` 在舊程式上的紅燈輸出：`red-auth.log`、`red-tenant.log`），再修正。

| # | 發現 | 修正 | 證明的測試 |
|---|---|---|---|
| 1 | 中：refresh 持有 mutex 最長 10 秒，key 過 KeyMaxAge 後 provider 卡住時所有 request 一起卡（review probe：8 個 request 最大延遲 10 s） | stale-while-revalidate：快取 key 立即使用並背景 refresh；single-flight channel；未知 kid 等待 `done` 或 `ctx.Done()`；抓取不用任何 request 的 context | `TestRefreshNeverBlocksUsableKeys`（舊程式：`waited 10.008s`）；`TestCancelledRequestStillFetches`、`TestConcurrentVerify` 改為新語意 |
| 2 | 中（P0 invariant 的測試）：「查詢後連線上沒有 identity settings」的斷言不會失敗（session-level `set_config` mutant 存活） | 在沒有 release guard 的單連線 pool 上 ResolveUser 後直接讀 `current_setting(...)` 與可見 user 數 | `TestResolveUser`；mutant `resolve-session-settings` 被擋 |
| 3 | 中（測試）：outage 時 refresh 的間隔沒有測試（`throttle-only-after-success` 存活，200 個 request 抓 200 次） | — （程式正確） | `TestOutageRefreshThrottled`：provider 503 時 200 個 request（一半未知 kid）最多 1 次抓取 |
| 4 | 低–中：10 秒 timeout 沒有測試，且無法注入 | `Config.FetchTimeout`（預設 10 s，≤1 min）；預設 client 不再另設 Timeout，由 context 限制整個抓取（含 body） | `TestFetchTimeout`：hold 住的 provider、100 ms timeout，錯誤原因為 `context.DeadlineExceeded`（request context 以 Canceled 結束，因此 deadline 只可能來自抓取） |
| 5 | 低：JWKS 錯誤被吞掉，404／TLS／解析錯誤都一樣 | `keysUnavailable(cause)` 以 `%w` 保留原因；transport 錯誤去掉 `url.Error` 的 URL（可能含 query credential） | `TestKeysErrorKeepsTheCause`（`status 404`、`fetching JWKS`、無 `SECRETVALUE`）、`TestEndlessJWKSBody`（`256 KiB`） |
| 6 | 低：三段都接受多餘位元不同的編碼，token 字串可變 | 三段皆以 `RawURLEncoding.Strict()` 解碼後才解析 | `TestStrictEncoding` |
| 7 | 低：`Access_Token`、`ACCESS_TOKEN`、`;access_token`、`access_token=%zz` 不觸發 `token_in_query` | 自行掃描 raw query：`&`／`;` 分隔、QueryUnescape、不分大小寫；名稱無法解讀也拒絕 | `TestBearerToken` 7 個新案例 |
| 8 | 低：設定錯誤回顯 algorithm 值 | 只報第幾個 algorithm | `TestConfigBounds`「echoed algorithm」 |
| 9 | 低：issuer 長度無上限，而 ResolveUser 拒絕 >2048 bytes | issuer 與 JWKS URL ≤2048 bytes | `TestConfigBounds`（2049 拒、2048 接受） |
| 10 | 低（測試）：「API login 無法新增 user」在唯讀 transaction 中測，失敗原因是 25006 而非權限 | 改在讀寫 transaction、設定相符 identity 後 INSERT，要求 `postgres.ErrPermission`（42501）；ResolveUser 確認 transaction 唯讀 | `TestResolveUser`；mutant `resolve-not-read-only` 被擋 |
| 11 | 低：`Challenge` 不符 RFC 6750 §3.1 | 見上表 `http.go`；`ReasonNotBearer` 與 `ReasonMalformed` 分開 | `TestChallenge`、`TestBearerToken` |
| 12 | 低（測試缺口）：PS*／RS384／RS512／ES512 正向路徑、`x5t`／`zip`／`enc`、LimitReader 未測 | — | `TestAllAlgorithms`（8 個非預設演算法、無 alg 的 P-521 key、ES384 header 配 P-521 key 拒絕、4 個 header）、`TestEndlessJWKSBody` |
| 13 | 低：cold start 一次失敗後鎖 1 分鐘 | 沒有可用 key 時（含只剩超過 KeyMaxStale 的 key）重試間隔 5 秒 | `TestColdStartRecovers`（6 秒後恢復；恢復後未知 kid 為 `unknown_key`；過期 key 也適用） |
| P | token type 混淆（audience 等於 client_id 時 ID token 可用） | `RequireAccessTokenType`／`AUTH_OIDC_REQUIRE_AT_JWT`；ADR-017 寫明 audience 不可等於任何 client_id | `TestRequireAccessTokenType`、`config.TestLoadAuth` |
| P | 程式化設定的間隔可顛倒或無上限 | 間隔順序與上限（見交付內容） | `TestConfigBounds`（含邊界值接受） |
| P | ResolveUser 遇 NUL／非 UTF-8 回未分類的 SQLSTATE 22021 | 回 `ErrInvalidIdentity` | `TestResolveUser` 4 個新案例（舊程式：`SQLSTATE 22021`） |
| P | `sub` 接受 U+202E 等格式字元 | `printable` 改為 `unicode.IsPrint` | `TestSubjectFormatCharacters`（U+202E、U+200B、U+FEFF、U+2028、U+00A0、U+E000） |
| — | `resolve-subject-only` 只因 RLS 才「equivalent」 | superuser（不受 RLS 限制）測 application filter | `TestResolveUser`；mutant 被擋，不再列 equivalent |
| — | 背景抓取若 panic 會讓 process 結束（net/http 在 handler 內會攔下） | `safeFetch` 以 recover 轉為錯誤 | `TestFetchPanicContained` |

未改：go-jose v4.1.6（2026-10-14）未滿兩週，依 AGENTS.md 不升級；滿兩週後檢查其 changelog 是否為安全修正。

### 首版報告的更正

| 首版說法 | 實際 |
|---|---|
| `TestRejectedTokens`「53 個案例」 | 首版 52 個；本輪加 2 個（段數錯誤的 token 帶 `alg:none` 也是 `malformed`）共 54 個 |
| 「錯誤不回顯值」 | algorithm 值會回顯（#8，已修） |
| 「查詢後連線上沒有 identity settings」「API login 無法新增 user」已測 | 兩個斷言都證明不了（#2、#10，已改寫） |
| 10 秒 timeout、outage 時的抓取間隔 | 已實作但未測（#3、#4，已補） |
| mutation「54/54」 | 數字可重現，但 reviewer 另寫的 15 個 mutant 中 14 個存活；本輪全部納入新腳本（見下） |
| `resolve-subject-only` equivalent | 只因 RLS 遮住；已加不受 RLS 限制的測試 |

## 實際執行結果（review 修正後）

| 指令 | Exit | 說明 |
|---|---|---|
| `go test -count=1 -v ./internal/auth/... ./internal/config/... ./cmd/...` | 0 | 51 個 RUN 全 PASS（`auth-tests.log`） |
| `go test -race -count=3 ./internal/auth/... ./internal/config/...` | 0 | `auth-race.log` |
| `GEMINUS_REQUIRE_DB_TESTS=1 go test -count=1 -run TestResolveUser -v ./internal/tenant/`（一次性 PostgreSQL 16，`dt_test_t008`） | 0 | `tenant-resolve.log` |
| Mutation（`mutants.py`，取代首版腳本；首版的 pattern 指向已改寫的程式） | 0 | **114/114 被擋，2 個 equivalent**（`mutation.log`），另 `throttle-only-after-success` 1/1（`mutation-addendum.log`，`--only` 單獨執行）。共 117 個 mutant，含 reviewer 另寫的 15 個與每項修正各自的 mutant。首次執行 109/115：`segment-count-unchecked` 存活 → 補「段數錯誤但可解碼、`alg:none`」2 個案例；`negative-intervals-allowed` 存活 → 該分支是 dead code（負值必然落入下一個 case），刪除；4 個 mutant 寫法未編譯（unused import）已改寫。Equivalent：`private-keys-used`（go-jose 的 `IsPublic` 接受的正是 `fits` 接受的三種型別，reviewer 亦確認）、`resolve-read-only-unasserted`（斷言只在 transaction 非唯讀時觸發，`resolve-not-read-only` 證明它會觸發） |
| 紅燈：新測試在舊程式上 | 1 | `red-auth.log`（9 個測試失敗，例如 `waited 10.008s for a hung refresh`）、`red-tenant.log`（NUL／非 UTF-8 回 `SQLSTATE 22021`）、`red-auth-round2.log`（需要新設定欄位的測試在舊程式上無法編譯） |
| `make backend-test`／`backend-race`／`backend-build`／`architecture-check`／`spec-check`／`frontend-test` | 見下 | BRANCH_RESULTS |
| GitHub Actions | 推送後補記 | |

首版的執行紀錄（39 個 RUN、db-test、govulncheck、mutation 54/54＋2 equivalent）保留在 `checks/evidence/2026-10-15-T-008/`。

## 驗收對照

| 測試 | 驗證內容 | 對應 |
|---|---|---|
| `TestValidTokens`、`TestAllAlgorithms` | RS256／ES256 與 8 個非預設演算法、aud 陣列、typ at+jwt、無 typ、無 nbf／iat、255 bytes 的 sub | REQ-001 |
| `TestRejectedTokens`（54 個案例）、`TestStrictEncoding`、`TestSubjectFormatCharacters`、`TestRequireAccessTokenType` | 格式、段數、大小、strict 編碼、`alg:none`、HS256 以公鑰／JWK 為 secret、未允許的 alg、alg 與 key 類型不符、無 kid、禁止的 header、typ、未知 kid、同 kid 不同 key、簽後改 payload、claims 型別、重複 claim、iss／aud／exp／nbf／iat／sub 各種錯誤；每個都是 `ErrInvalidToken` 且錯誤訊息不含 token 或 subject | SECURITY §3、REQ-002 |
| `TestClockSkewBoundaries`、`TestKeyAlgorithmPinned` | skew 邊界精確；JWK 的 alg 釘住同家族的演算法、curve 必須相符 | SECURITY §3 |
| `TestKeyRotation`、`TestJWKSUnavailable`、`TestJWKSContent`、`TestJWKSRedirectNotFollowed`、`TestConcurrentVerify`、`TestCancelledRequestStillFetches`、`TestRefreshNeverBlocksUsableKeys`、`TestOutageRefreshThrottled`、`TestFetchTimeout`、`TestColdStartRecovers`、`TestKeysErrorKeepsTheCause`、`TestEndlessJWKSBody`、`TestFetchPanicContained` | 輪替的 key 在間隔後生效、100 個隨機 kid 不造成額外抓取、移除的 key 在 KeyMaxAge 與背景 refresh 後失效；hung refresh 不阻擋可用的 key；outage 時抓取仍受間隔限制、失敗的 refresh 不清掉快取；503 與 24 小時上限；cold start 5 秒重試；timeout、原因、大小上限、panic；私鑰、重複 kid、enc key、1024-bit RSA、未允許 alg、未知 key 類型、錯誤狀態、過大內容；不跟隨 redirect；32 個並行首次使用只抓一次 | SECURITY §3「key rotation」「endpoints 僅 deployment allowlist」 |
| `TestConfigValidation`、`TestConfigBounds`、`config.TestLoadAuth`、`cmd/api` 測試 | http、query、fragment、credentials、長度、HS256、none、skew、timeout、間隔順序與上限（含邊界值）；錯誤不回顯值；API 在設定不安全時不啟動 | SECURITY §3 |
| `TestBearerToken`、`TestChallenge`、`TestMiddleware` | 只接受單一 Bearer header；URL token 的各種拼法拒絕；RFC 6750 challenge；失敗時 handler 不執行；非預期錯誤 fail closed | TEST-009（authentication 部分）、SECURITY §3 |
| `tenant.TestResolveUser`（DB） | 兩個 tenant 的 user 各自解析；未知 subject、同 subject 不同 issuer、issuer 多一個斜線、大小寫、SQL 與 LIKE 字元皆為 `ErrUnknownUser`；長度、NUL、非 UTF-8 為 `ErrInvalidIdentity`，最長合法值可用；無 guard 的連線在查詢後沒有 identity settings、看不到任何 user；RLS 只讓該 identity 看到自己的 row；不受 RLS 限制時 application filter 仍只解析完全相同的 issuer 與 subject；API login 在讀寫 transaction 中新增 user 得到 permission denied | TEST-009（user 解析）、TEST-010（pool 回收後無 context）、TEST-011（API login 受 grants 約束）、D-008 |
| `scripts/check-architecture` probes | cmd 連結 `authtest`、auth 依賴 tenant 或 pgx 都被擋 | 「production 禁用 test verifier」 |

## 需 owner 知悉

- ADR-017 已由 owner 同意（H-004）；本輪修訂不改方向，內容見 ADR 文末。
- Provider 為 Auth0、browser 採 BFF（ADR-018，owner 授權 agent 決定）：Auth0 tenant 需由 owner 建立，region 依 retention／residency；provider 支援 RFC 9068 時建議設 `AUTH_OIDC_REQUIRE_AT_JWT=true`。
- 已撤銷的 key 在 KeyMaxAge（1 小時）後的背景 refresh 完成前仍被接受（provider 也無法連線時最多 24 小時）；若需要更快，調整 KeyMaxAge 或另加 introspection。
- 驗證失敗的 rate limiting 與告警屬 T-035／T-036。

## 與規劃的差異

- 規劃輸出 `internal/auth/` 與 `cmd/api/` 皆已處理；另在 `internal/tenant` 新增 `ResolveUser`，因為 user 解析需要 DB，而 auth 依架構規則不得接觸 DB。
- HTTP error envelope（需要 request_id）屬 T-013；`auth.Middleware` 以注入的 error writer 回應，不另定義錯誤格式。RFC 6750 建議 `invalid_request` 用 400；本 API 對所有 `ErrInvalidToken` 回 401（envelope 由 T-013 決定狀態碼），challenge 的 error code 依 RFC。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| `make integration-test` | target 尚未實作（需要 API routes 與 worker，T-013 起） |
| TEST-009 的完整 route × role 矩陣（read／write／list／download／SSE） | routes 屬 T-013 之後；本任務只提供 authentication 與 user 解析 |
| 真實 OIDC provider（Auth0）的整合測試 | Auth0 tenant 尚未由 owner 建立；以 TLS 的 test provider 驗證協定行為；E2E 也只用 test provider（ADR-018） |
| `govulncheck`（本輪） | 本輪未新增依賴；首版結果見 `2026-10-15-T-008/`；CI 的 govulncheck 見 GitHub Actions |
| 獨立 review 複核 | 推送後由同一 reviewer 進行 |
