# ADR-017 — Provider-agnostic OIDC access-token verification (T-008)

狀態：ACCEPTED（T-008 agent 選定；owner KCL1104 於 2026-10-18 同意，H-004）。日期：2026-10-15；2026-10-18 依 T-008 獨立 review 修訂（見文末）。原報告把 auth provider 列為 TBD（DECISIONS_AND_TBD.md「Auth provider：managed OIDC」）；本 ADR **不選定 provider**，只固定 API 如何驗證任何 OIDC provider 簽發的 access token。Provider 與 browser session 見 [ADR-018](ADR-018-browser-session-and-oidc-provider.md)。

## Decision

- `internal/auth.OIDCVerifier` 驗 Bearer access token。Provider 的 issuer、audience、JWKS URL 只來自 deployment 設定（`AUTH_OIDC_ISSUER`、`AUTH_OIDC_AUDIENCE`、`AUTH_OIDC_JWKS_URL`、可選 `AUTH_OIDC_ALGORITHMS` 與 `AUTH_OIDC_REQUIRE_AT_JWT`），issuer 與 JWKS URL 各 ≤2048 bytes，不做 discovery，也不從 token 取得任何 endpoint（SECURITY §3「provider endpoints 僅 deployment allowlist」）。
- 函式庫：`github.com/go-jose/go-jose/v4` v4.1.5（Apache-2.0，2026-09-03 發布，只依賴 Go 標準函式庫）負責 compact JWS 與 JWK 的解析與簽章驗證；v4.1.6 於本日前一天發布，依 AGENTS.md 不採用未滿兩週的版本。Header 與 claims 的檢查由本 repo 的程式碼完成，不依賴函式庫的預設值。
- Token：只接受 compact JWS，只含 base64url 字元與 `.`（Go 的 decoder 即使 strict 也會略過 CR／LF），三段都必須是唯一的 base64url 編碼（strict，多餘位元非零即拒）；`alg` 必須在 allowlist（預設 RS256、ES256，deployment 可加入 RS/PS/ES 的 384／512 與 EdDSA；`none` 與 HMAC 一律拒絕）且與 key 的 `alg` 及類型相符；`kid` 必填；`jku`、`x5u`、`jwk`、`x5c`、`x5t`、`x5t#S256`、`crit`、`b64`、`zip`、`enc` 一律拒絕；`typ` 若存在須為 `JWT` 或 `at+jwt`，`AUTH_OIDC_REQUIRE_AT_JWT=true` 時必須為 `at+jwt`（RFC 9068；provider 支援時開啟，使 ID token 無法當 access token）；header 與 payload 的重複成員拒絕。Token 最大 8 KiB。
- Claims：`iss` 與設定完全相同；`aud`（字串或陣列）包含設定的 audience；`exp` 必填；`nbf`、`iat` 若存在則檢查；容許時鐘誤差預設 30 秒（最多 5 分鐘）；`sub` 必填、≤255 bytes、只含可列印字元（拒控制、格式如 U+202E、分隔與私用字元）。Audience 必須是 API 自己的 identifier，絕不可等於任何 client_id，否則 ID token 的 `aud` 可能相符。
- JWKS：https、不跟隨 redirect、每次抓取 timeout 預設 10 秒（`FetchTimeout`，≤1 分鐘）、最大 256 KiB（以 size-limited reader 讀取）。只保留公開、`use` 為 `sig` 或未標、有 `kid`、`alg` 在 allowlist、RSA ≥2048 bits、P-256／384／521 或 Ed25519 的 key；重複 `kid` 兩個都不用；無法解析的 key 略過。首次使用時抓取。**Stale-while-revalidate**：已快取的 key 一律立即使用；JWKS 使用 1 小時（KeyMaxAge）後在背景 refresh，refresh 失敗時已抓到的 key 最多再用 24 小時（KeyMaxStale）；只有未知 `kid` 需要等 refresh，且只等到 request context 結束，上一次抓取失敗時最多等 1 秒（provider 多半仍無法連線，不讓連線佔滿 FetchTimeout）。同時最多一次抓取（single-flight），兩次抓取至少間隔 1 分鐘（MinRefreshInterval）；沒有可用 key 時改為 5 秒，使 cold start 可恢復。間隔須滿足 1 秒 ≤ MinRefreshInterval ≤ KeyMaxAge ≤ KeyMaxStale、MinRefreshInterval ≤24 小時、KeyMaxStale ≤7 天。抓取在自己的 goroutine 與 context 中進行，panic 轉為錯誤，抓取未正常結束（例如 transport 呼叫 `runtime.Goexit`）時 refresh 狀態也會重設。設定的 `Now` 必須單調（預設 `time.Now`）：時鐘倒退時 refresh 延後到追上為止。抓不到 key 回 `ErrKeysUnavailable`（API 回 503，不帶 `WWW-Authenticate`），錯誤保留原因（status、解析錯誤、timeout）但不含 URL；其他失敗回 `ErrInvalidToken`（401 UNAUTHENTICATED）。`WWW-Authenticate`（RFC 6750 §3）：沒有 Bearer credentials（未帶或他種 scheme）時不帶 error code；多個 header、`Bearer` 後無 token、URL 中的 token 為 `error="invalid_request"`；其他為 `error="invalid_token"`。錯誤只帶 reason code，不帶 token 或 claims；`ReasonKeys` 的錯誤字串含 transport 原因（例如 `dial tcp …: connection refused`、內部 host 名稱），只供 log，T-013 的 error writer 不得把 `err.Error()` 回給 client。
- 傳遞：只讀單一 `Authorization: Bearer` header；URL query 中任何大小寫、escape 或 `;` 分隔的 `access_token`／`id_token`，以及無法解讀的參數名稱，一律拒絕；不讀 cookie（browser session 由 ADR-018 的 BFF 轉成 Bearer）。`auth.Middleware` 透過注入的 error writer 回應，不定義第二套錯誤格式（envelope 由 T-013 提供）。
- 使用者：`tenant.ResolveUser` 以 RLS 的 identity settings（`app.oidc_issuer`、`app.oidc_subject`，transaction-local）在唯讀 transaction 中查 `twin.users`（並確認該 transaction 確實唯讀）；issuer／subject 為空、過長、非 UTF-8 或含 NUL 回 `ErrInvalidIdentity`；沒有對應 user 回 `ErrUnknownUser`（API 回 403），**不**從 token 自動建立 user（D-008：受控 provisioning）。Role 與 membership 一律來自 DB（`tenant.Run`）。
- 測試用 token 只由 `internal/auth/authtest`（test-only provider，TLS JWKS、可產生惡意 token）簽發；`scripts/check-architecture` 禁止任何 production package 連結它，因此沒有可在 production 開啟的 test verifier。

## Consequences

- 選定 provider 時只需設定四個變數；provider 必須提供 JWT 格式的 access token（opaque token 與 introspection 不在本 ADR 範圍，需另立 ADR）。
- 在 provider 移除（撤銷）的 key，在 1 小時（KeyMaxAge）後的背景 refresh 完成前仍被接受（stale-while-revalidate：refresh 期間的 request 照用快取）；若 provider 同時無法存取，最多 24 小時。需要更快撤銷時調整這兩個值或另加 introspection。
- 未決：驗證失敗的 rate limiting 與告警（T-035／T-036）。Provider（Auth0）與 browser session（BFF）已由 ADR-018 決定。

## 2026-10-18 修訂（T-008 獨立 review）

Review 確認 refresh 期間持有 lock 會讓所有 request 等待 hung provider 最多 10 秒（即使 key 已快取），因此改為上述 stale-while-revalidate 與 single-flight；另補 strict base64、URL query 掃描、錯誤原因、RFC 6750 error code、`FetchTimeout`、`AUTH_OIDC_REQUIRE_AT_JWT`、間隔與長度上限、格式字元、ResolveUser 的輸入與唯讀檢查。決策方向不變；細節與測試見 checks/T-008_VALIDATION.md。
