# ADR-017 — Provider-agnostic OIDC access-token verification (T-008)

狀態：PROPOSED（T-008 agent 選定，待 owner 同意）。日期：2026-10-15。原報告把 auth provider 列為 TBD（DECISIONS_AND_TBD.md「Auth provider：managed OIDC」）；本 ADR **不選定 provider**，只固定 API 如何驗證任何 OIDC provider 簽發的 access token。

## Decision

- `internal/auth.OIDCVerifier` 驗 Bearer access token。Provider 的 issuer、audience、JWKS URL 只來自 deployment 設定（`AUTH_OIDC_ISSUER`、`AUTH_OIDC_AUDIENCE`、`AUTH_OIDC_JWKS_URL`、可選 `AUTH_OIDC_ALGORITHMS`），不做 discovery，也不從 token 取得任何 endpoint（SECURITY §3「provider endpoints 僅 deployment allowlist」）。
- 函式庫：`github.com/go-jose/go-jose/v4` v4.1.5（Apache-2.0，2026-09-03 發布，只依賴 Go 標準函式庫）負責 compact JWS 與 JWK 的解析與簽章驗證；v4.1.6 於本日前一天發布，依 AGENTS.md 不採用未滿兩週的版本。Header 與 claims 的檢查由本 repo 的程式碼完成，不依賴函式庫的預設值。
- Token：只接受 compact JWS；`alg` 必須在 allowlist（預設 RS256、ES256，deployment 可加入 RS/PS/ES 的 384／512 與 EdDSA；`none` 與 HMAC 一律拒絕）且與 key 的 `alg` 及類型相符；`kid` 必填；`jku`、`x5u`、`jwk`、`x5c`、`x5t`、`x5t#S256`、`crit`、`b64`、`zip`、`enc` 一律拒絕；`typ` 若存在須為 `JWT` 或 `at+jwt`；header 與 payload 的重複成員拒絕。Token 最大 8 KiB。
- Claims：`iss` 與設定完全相同；`aud`（字串或陣列）包含設定的 audience；`exp` 必填；`nbf`、`iat` 若存在則檢查；容許時鐘誤差預設 30 秒（最多 5 分鐘）；`sub` 必填、≤255 bytes、可列印。
- JWKS：https、不跟隨 redirect、10 秒 timeout、最大 256 KiB。只保留公開、`use` 為 `sig` 或未標、有 `kid`、`alg` 在 allowlist、RSA ≥2048 bits、P-256／384／521 或 Ed25519 的 key；重複 `kid` 兩個都不用；無法解析的 key 略過。首次使用時抓取；未知 `kid` 觸發 refresh，但兩次抓取至少間隔 1 分鐘；JWKS 使用 1 小時後 refresh；refresh 失敗時已抓到的 key 最多再用 24 小時；抓不到 key 回 `ErrKeysUnavailable`（API 回 503），其他失敗回 `ErrInvalidToken`（401 UNAUTHENTICATED，`WWW-Authenticate: Bearer realm="geminus", error="invalid_token"`）。錯誤只帶 reason code，不帶 token 或 claims。
- 傳遞：只讀單一 `Authorization: Bearer` header；URL 中出現 `access_token`／`id_token` 即拒絕；不讀 cookie（cookie 模式需要下方未決的 browser ADR 與 CSRF 設計）。`auth.Middleware` 透過注入的 error writer 回應，不定義第二套錯誤格式（envelope 由 T-013 提供）。
- 使用者：`tenant.ResolveUser` 以 RLS 的 identity settings（`app.oidc_issuer`、`app.oidc_subject`，transaction-local）在唯讀 transaction 中查 `twin.users`；沒有對應 user 回 `ErrUnknownUser`（API 回 403），**不**從 token 自動建立 user（D-008：受控 provisioning）。Role 與 membership 一律來自 DB（`tenant.Run`）。
- 測試用 token 只由 `internal/auth/authtest`（test-only provider，TLS JWKS、可產生惡意 token）簽發；`scripts/check-architecture` 禁止任何 production package 連結它，因此沒有可在 production 開啟的 test verifier。

## Consequences

- 選定 provider 時只需設定四個變數；provider 必須提供 JWT 格式的 access token（opaque token 與 introspection 不在本 ADR 範圍，需另立 ADR）。
- 在 provider 移除（撤銷）的 key，最多在 1 小時（KeyMaxAge）後才不再被接受；若 provider 同時無法存取，最多 24 小時。需要更快撤銷時調整這兩個值或另加 introspection。
- 未決、需 owner 決定：auth provider（TBD）；browser 端 token 儲存、refresh 與是否採 BFF／cookie（SECURITY §3 要求另立 auth ADR）；驗證失敗的 rate limiting 與告警（T-035 observability）。
