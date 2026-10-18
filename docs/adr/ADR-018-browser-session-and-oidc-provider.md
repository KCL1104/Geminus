# ADR-018 — Browser session through a BFF, and Auth0 as the OIDC provider

狀態：ACCEPTED。2026-10-18 由 agent 依 owner 授權決定：owner 在 session 中回覆「ok, you can do it right now」（H-004），並非 owner 逐項審查。Owner 可隨時重開；任何 provider 帳號、方案與費用由 owner 確認。

原報告把 Auth provider 列為 TBD（「managed OIDC」，DECISIONS_AND_TBD.md）。SECURITY §3 要求 browser 端 token 儲存、refresh 與 BFF 模式另立 auth ADR。ADR-017 只固定 API 怎麼驗 access token，不選 provider。本 ADR 補上這兩項，前端登入流程（T-043）依此實作。

## Decision

### 1. Browser session：BFF（Backend-for-Frontend）

- **OIDC client 在伺服器端。** Next.js 伺服器端（Node runtime，不是 static export）是 confidential OIDC client。
  - 流程：Authorization Code ＋ PKCE（S256）＋ `state` ＋ `nonce`。
  - Client 認證：client secret 或 `private_key_jwt`，只存在 web server 的 secret store。
  - Endpoints：authorization、token、revocation、end-session 都只來自 deployment 設定，不在 runtime 做 discovery。這與 SECURITY §3 的「provider endpoints 僅 deployment allowlist」一致。
- **瀏覽器拿不到任何 token。**
  - Access、refresh、ID token 都不放進 JS 可讀的地方：localStorage、sessionStorage、IndexedDB、JS 可讀的 cookie、回應 body 都不行。
  - ID token 只在 callback 驗證後使用（`iss`、`aud`、`nonce`、`sub`），隨即丟棄。
- **Session cookie。**
  - 屬性：`__Host-geminus_session`，`HttpOnly`、`Secure`、`SameSite=Lax`、`Path=/`，不設 `Domain`。
  - 內容：以 AES-256-GCM 加密的 access token、refresh token、到期時間與 session 絕對期限；AAD 為 cookie 名稱與格式版本。
  - Key：同時保留一把目前、一把前一把的 key，以支援輪替；key 來自 secret store。
  - 大小：cookie 超過 3.5 KB 視為錯誤，不切塊。屆時改用 server-side session store，另立 ADR。
- **CSRF 防護。**
  - 除了 `SameSite=Lax` 之外，BFF 對所有非 GET／HEAD 請求要求兩件事：`Origin` 與設定的 app origin 完全相同（瀏覽器有送 `Sec-Fetch-Site` 時須為 `same-origin`），且帶自訂 header `X-Geminus-Request: 1`，這會迫使跨站請求先做 preflight。
  - BFF 不開 CORS。
- **API proxy。**
  - 瀏覽器只呼叫同源的 `/api/v1/...`。BFF 換上 `Authorization: Bearer <access token>` 轉給 Go API，`X-Organization-ID` 原樣轉送；它只是 selector（D-008）。
  - Access token 剩不到 60 秒時，BFF 在伺服器端 refresh。
  - BFF 不把 cookie 轉給 Go API。Go API 維持只讀 Bearer、不讀 cookie（ADR-017 不變）。
  - SSE（TEST-026）與 upload 以串流轉送；大小限制仍由 API 執行（TEST-012）。
- **Token 期限的起始值**（在 provider 設定，記入部署 runbook；變更需 security review）：
  - Access token：10 分鐘。
  - Refresh token：rotation，並開啟 reuse detection。
  - Session：閒置 12 小時、絕對期限 7 天。
- **Logout。** 依序：清除 cookie；在 revocation endpoint 撤銷 refresh token；再導向設定的 end-session URL（RP-initiated logout）。
- **Library。** 使用通用的 OIDC／OAuth client，不用 provider 專屬 SDK，讓 provider 可以替換。候選是 `openid-client`（MIT）。版本在 T-043 依 AGENTS.md 選定、釘住並記錄：發布滿兩週、有 lockfile、有 license 記錄。

### 2. OIDC provider：Auth0

選 Auth0 作為 alpha 與 production 的 managed OIDC provider，理由：

- **Access token 綁 API audience。** Auth0 可替 API identifier（`aud`）簽發 JWT access token，且每個 API 可選 RFC 9068 profile（header `typ: at+jwt`）。因此可開啟 `AUTH_OIDC_REQUIRE_AT_JWT`（ADR-017），ID token 無法被拿來當 access token 用。
- **不綁雲端。** Cloud 仍是 TBD（GCP reference／AWS equivalent），Auth0 兩邊都能用。
- **標準介面。** 提供標準的 OIDC endpoints 與 JWKS，預設 RS256，在 ADR-017 的預設 allowlist 內。
- **Refresh token。** 支援 rotation 與 reuse detection。
- **日後的 B2B SSO。** 若需要，可用 organizations 或 enterprise connections。Tenancy 與 role 仍一律來自 Geminus 的 DB（D-008），絕不取自 IdP claims。

比較過的替代方案：

| 方案 | 不選的原因 |
|---|---|
| Amazon Cognito | Access token 只有在 resource binding 時才帶 `aud`；而且綁定 AWS |
| Google Identity Platform | 以 ID token 為主，不是替自訂 API audience 簽 access token 的 authorization server |
| Microsoft Entra External ID | 可行的替代方案；`typ` 為 `JWT` 而不是 `at+jwt` |
| 自架 Keycloak | 不是 managed，增加維運負擔 |

換 provider 只需改設定：API 的 `AUTH_OIDC_*` 與 BFF 的 client 設定，再重跑 TEST-009／TEST-032。

### 3. 條件與仍未決

- **帳號屬 owner。** Auth0 tenant 由 owner 建立並持有，方案與費用由 owner 在註冊時確認。Agent 不建立帳號、不接受條款，也不持有 credential。
- **Region 與資料。** Tenant region 依 retention／residency 決定（TBD）。決定前只能有測試使用者，不放真實客戶的使用者資料。
- **Provisioning 受控。** 遵守 D-008／T-010：未知 identity 登入時回 403（`tenant.ErrUnknownUser`），不從 token 自動建立 user。
- **本機與 CI。** E2E 使用 repo 內的 deterministic 測試 OIDC provider：在 `internal/auth/authtest` 加上 authorize 與 token endpoint，或改用同等的 test-only 程式。絕不連 Auth0，CI 也沒有 Auth0 credential。
- **Rate limiting。** Login、callback、refresh 的 rate limiting 與告警屬 T-036／T-035。

## Consequences

- Web 需要伺服器端 runtime：T-039 的 web container 是 Node server，不是靜態檔。
- ADR-016 的 `lib/api/client.ts` 原本「帶 `Authorization`、不送 cookie」，在 T-043 改為：
  - 呼叫同源 BFF，`credentials: 'same-origin'`；
  - 帶 `X-Geminus-Request`；
  - 瀏覽器不再持有 token，也不設 `Authorization`。

  ADR-016 其餘部分不變。
- Go API 不變：仍只接受 Bearer，也不新增 cookie 寫入路徑。SECURITY §3 要求的 cookie 防護（CSRF、SameSite、Secure、HttpOnly）由 BFF 執行，並由 TEST-032 驗證。
