# Runbook：Auth0 tenant 與瀏覽器 session（BFF）設定

依據：[ADR-018](../adr/ADR-018-browser-session-and-oidc-provider.md)（BFF、Auth0）、[ADR-017](../adr/ADR-017-oidc-token-verification.md)（API 驗 token）、T-043。

**這些步驟由 owner 執行。** Agent 不建立帳號、不接受條款、不持有任何 credential；repo 與 CI 都沒有 Auth0 的設定或密鑰，測試只用 repo 內的測試 provider（`apps/web/tests/fake-oidc.ts`、`apps/web-e2e/fake/servers.mjs`）。H-034 之後（T-057）：tenant 與 owner 自己的使用者仍由 owner 建立；§2、§3 的設定、關閉 sign-up 與評審帳號（H-035 (2)）由部署 session 的 `deploy/railway/deploy.sh --apply` 以 Auth0 CLI 設定（owner 在自己的瀏覽器核准 CLI 的登入，[deploy runbook](deploy.md) §7.8）；§6 是同樣設定的手動備援。

## 1. 建立前要先決定的事

- **Region**：依資料 residency 與 retention 決定（仍為 TBD）。決定之前只能有測試使用者，不放真實客戶的使用者資料（ADR-018 §3）。
- **方案與費用**：owner 註冊時確認。

## 2. API（access token 的 audience）

在 Auth0 Dashboard → Applications → APIs 建立：

| 設定 | 值 |
|---|---|
| Identifier | API 的 audience，例如 `https://api.<你的網域>`。之後不能改 |
| Signing algorithm | RS256 |
| Access token profile | RFC 9068（header `typ: at+jwt`），API 端開 `AUTH_OIDC_REQUIRE_AT_JWT=true` |
| Token expiration | 600 秒（10 分鐘） |
| Allow Offline Access | 開（才會發 refresh token） |

## 3. Application（BFF 是 confidential client）

Applications → Applications → Create → **Regular Web Application**：

| 設定 | 值 |
|---|---|
| Token endpoint authentication method | Client Secret (Post) |
| Allowed Callback URLs | `https://<app 網域>/auth/callback`（只有這一個） |
| Allowed Logout URLs | `https://<app 網域>/` |
| Allowed Web Origins、Allowed Origins (CORS) | 空白（BFF 不開 CORS，瀏覽器不直接連 Auth0 的 API） |
| Grant types | 只勾 Authorization Code 與 Refresh Token |
| ID token signature | RS256（BFF 固定以 RS256 驗 ID token 簽章） |
| Refresh token rotation | 開，reuse detection 開 |
| Rotation 的 reuse interval | 30 秒，與 BFF 在 process 內共用 refresh 結果的時間相同（`REFRESH_REUSE_MS`）。只有一個 web instance 時也不設 0：瀏覽器取消的 request 或遺失的回應拿不到 rotation 後的 cookie，30 秒內的重送仍能拿到新 token，而不是被當成 reuse 撤銷整個 session |
| Refresh token expiration | Inactivity lifetime 43200 秒（12 小時）、Absolute lifetime 604800 秒（7 天） |

使用者：關閉 database connection 的 sign-up。使用者與 organization 只由 operator 以 `cmd/provision-tenant` 建立，subject 用 Auth0 的 `user_id`（例如 `auth0|…`）；未 provision 的 identity 登入後 API 回 403，不會自動建立（D-008）。

## 4. 部署設定

Web（Next.js 伺服器端，BFF）：

| 變數 | 值 |
|---|---|
| `BFF_APP_ORIGIN` | `https://<app 網域>` |
| `BFF_API_URL` | Go API 的 base URL（https；只有全部 URL 都在 loopback 時才可 http） |
| `BFF_OIDC_ISSUER` | `https://<tenant>.<region>.auth0.com/`（含結尾的 `/`，與 ID token 的 `iss` 完全相同） |
| `BFF_OIDC_AUTHORIZATION_URL` | `<issuer>authorize` |
| `BFF_OIDC_TOKEN_URL` | `<issuer>oauth/token` |
| `BFF_OIDC_REVOCATION_URL` | `<issuer>oauth/revoke` |
| `BFF_OIDC_END_SESSION_URL` | `<issuer>oidc/logout` |
| `BFF_OIDC_JWKS_URL` | `<issuer>.well-known/jwks.json` |
| `BFF_OIDC_CLIENT_ID` | Application 的 Client ID |
| `BFF_OIDC_CLIENT_SECRET` | Application 的 Client Secret（只放 secret manager） |
| `BFF_OIDC_AUDIENCE` | 第 2 節的 API Identifier |
| `BFF_OIDC_SCOPES` | 不設（預設 `openid offline_access`） |
| `SESSION_KEYS` | `<id>:<32 bytes 的 base64url>`，見下方（只放 secret manager） |

Go API：`AUTH_OIDC_ISSUER` 與上面相同、`AUTH_OIDC_AUDIENCE` 為 API Identifier、`AUTH_OIDC_JWKS_URL` 同上、`AUTH_OIDC_REQUIRE_AT_JWT=true`（ADR-017）。

設定不完整或不合格時，BFF 的 `/auth/login`、`/auth/callback`、`/auth/logout` 與 `/api/v1/*` 一律回 503（`/auth/error` 是一般頁面，照常顯示），log 只寫變數名稱，不寫值。

BFF 依 token endpoint 本身的回答分類 refresh 失敗（SECURITY §3）。Auth0 暫時無法 refresh（沒有回答：連不上、逾時；5xx、429；錯誤碼 `temporarily_unavailable`、`server_error`、`too_many_requests`；或非 2xx 卻沒有 OAuth 錯誤，例如前方 edge 的錯誤頁）或 client 設定錯誤（`invalid_client`）時，`/api/v1/*` 回 503（`Retry-After: 5`），session cookie 保留；同一個 instance 在 5 秒內以同一個 refresh token 來的 request 共用這次失敗，之後的 request 再試。Log 的 `session.refresh_unavailable` 帶 token endpoint 的 status 與 body 中的 OAuth 錯誤碼（沒有錯誤碼時只有 status），可分辨限流、edge 錯誤頁與 client secret 錯誤。Auth0 拒絕 refresh token（4xx 並帶上述以外的錯誤碼，例如 `invalid_grant`：reuse、撤銷、過期；`access_denied`）、session 沒有 refresh token，或 Auth0 回 2xx 但回答無法使用（包括驗 ID token 所需的 JWKS 抓不到）時才結束 session（401 並清除 cookie，log 為 `session.refresh_failed`）；2xx 時 Auth0 已可能 rotation，BFF 盡力撤銷回答中的新 refresh token，撤銷失敗記 `session.discard_failed`。

### Session 金鑰與輪替

```sh
# 產生一把 key（32 random bytes，base64url，無 padding）
openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
```

- `SESSION_KEYS="k2:<新 key>,k1:<舊 key>"`：第一把用來封裝，第二把只用來打開舊 cookie，最多兩把。
- 輪替：先部署「新,舊」，等至少 7 天（session 的絕對期限）後再部署只有「新」。舊 key 外流時直接只放新 key：所有 session 失效、使用者重新登入。

## 5. 上線前檢查（staging）

1. 以測試使用者登入：瀏覽器開發工具中只有 `__Host-geminus_session`，屬性為 HttpOnly、Secure、SameSite=Lax、Path=/、無 Domain；`document.cookie`、localStorage、sessionStorage 中沒有 token。
2. 呼叫任一頁面的 API：request 只到同源 `/api/v1/…`，沒有 `Authorization` header。
3. 登出：Auth0 的 log 有 refresh token 撤銷與 logout；之後 `/api/v1/…` 回 401。
4. 未 provision 的使用者登入：API 回 403。
5. 封鎖測試使用者、以及讓 refresh token 過期，確認 Auth0 回的錯誤碼（預期 `invalid_grant`）讓 session 以 401 結束，而不是 503。
   另外確認兩種情況下 Auth0 實際回的 status 與錯誤碼（看 `session.refresh_unavailable`／`session.refresh_failed` 的 log）：(a) BFF 以錯誤的 client secret refresh（只在 staging 的另一個部署設定錯的值，不動正式設定）——4xx 回答只有在錯誤碼是 `invalid_client` 或沒有錯誤碼時才保留 session；若 Auth0 回的是其他錯誤碼（例如 401 `access_denied`），secret 輪替順序錯誤會結束所有 session，輪替前必須先在 Auth0 加上新 secret、部署、再移除舊的，並把結果記回本 runbook；(b) 讓 refresh 流程中執行的 Auth0 Action 刻意失敗，記下 Auth0 回的 status 與錯誤碼，以及 BFF 因此結束（401）或保留（503）session，確認符合預期。
6. 登出時 BFF 不帶 `id_token_hint`（ID token 驗完即丟）：確認 Auth0 是否因此顯示登出確認畫面；若會，評估 Auth0 的 logout 設定或改為保留 ID token 的 hash 以外的方式（需另行決定）。

自動化的對應測試是 `apps/web/tests/bff.test.ts`（單元）與 `apps/web-e2e/tests/session.spec.ts`（瀏覽器，TEST-032），兩者都只連測試 provider；連 Auth0 的檢查只能在 staging 由人執行（NOT RUN in CI）。

## 6. 手動備援：在 dashboard 做 Auth0 步驟的設定（H-034）

在 `deploy.sh` 的 Auth0 步驟（[deploy runbook](deploy.md) §7.8）無法使用時（拿不到 pin 住的 CLI、裝置登入失敗、或某一步停下而無法修正）由 owner 執行，設定值與 §7.8 的表完全相同。Dashboard 的名稱依 Auth0 文件（2026-10-26 取得：[API settings](https://auth0.com/docs/get-started/apis/api-settings)、[application settings](https://auth0.com/docs/get-started/applications/application-settings)、[credential settings](https://auth0.com/docs/get-started/applications/credentials)、[refresh token rotation](https://auth0.com/docs/secure/tokens/refresh-tokens/configure-refresh-token-rotation)、[refresh token expiration](https://auth0.com/docs/secure/tokens/refresh-tokens/configure-refresh-token-expiration)、[application connections](https://auth0.com/docs/get-started/applications/update-application-connections)、[access token profiles](https://auth0.com/docs/secure/tokens/access-tokens/access-token-profiles)）；「Disable Sign Ups」的文件頁沒有取得，名稱依 dashboard（API 欄位為 connection 的 `options.disable_signup`）。

先要有 web 的公開網域（Railway → web → Settings → Networking；或先以 `GEMINUS_AUTH0_SETUP=manual` 跑一次 `--apply`，步驟 2 會建立它，verification 會印出 callback 與 logout URL）。

1. **API**：Applications → APIs → **+ Create API**：Name `Geminus demo API`；Identifier 為 `GEMINUS_AUTH0_AUDIENCE`（例如 `https://api.geminus-demo.invalid`，之後不能改）；**JSON Web Token (JWT) Profile** 選 **RFC 9068**；**JSON Web Token (JWT) Signing Algorithm** 選 **RS256** → Create。該 API 的 **Settings**：Token Settings 的 **Maximum Access Token Lifetime** 填 `600`；Access Settings 的 **Allow Skipping User Consent** 開、**Allow Offline Access** 開 → Save。
2. **Application**：Applications → Applications → **+ Create Application**：Name `Geminus demo web`，選 **Regular Web Applications** → Create。
   - **Settings** → Application URIs：**Allowed Callback URLs** 只有 `https://<web 網域>/auth/callback`；**Allowed Logout URLs** 只有 `https://<web 網域>/`；**Allowed Web Origins**、**Allowed Origins (CORS)** 空白。
   - **Settings** → **Refresh Token Rotation**：**Allow Refresh Token Rotation** 開，**Rotation Overlap Period** 填 `30`。**Refresh Token Expiration**：**Set Idle Refresh Token Lifetime** 開、**Idle Refresh Token Lifetime** 填 `43200`；**Set Maximum Refresh Token Lifetime** 開、**Maximum Refresh Token Lifetime** 填 `604800`。
   - **Settings** → **Advanced Settings** → **OAuth**：**JSON Web Token (JWT) Signature Algorithm** 為 RS256、**OIDC Conformant** 開；**Grant Types**：只勾 **Authorization Code** 與 **Refresh Token**（其他全部取消）→ **Save Changes**。
   - **Credentials**：**Authentication Method** 選 **Client Secret (Post)** → Save。**Client ID** 給 `GEMINUS_AUTH0_CLIENT_ID`；**Client Secret** 只貼到 Railway → web → Variables 的 `BFF_OIDC_CLIENT_SECRET`，立刻 Seal（變數右側的 3-dot 選單），不存到其他任何地方。
   - **Connections**：只開 `Username-Password-Authentication`（database connection），其他（例如 Google）全部關閉。
3. **關閉 sign-up**：Authentication → Database → `Username-Password-Authentication` → **Disable Sign Ups** 開 → Save。
4. **評審帳號**（H-035 (2)）：User Management → Users → **+ Create User**：Email `judge@geminus-demo.invalid`（或與 coordinator 約定的地址），Password 以密碼管理器產生（20 字元以上，含大小寫、數字與符號），Connection `Username-Password-Authentication` → Create。在該使用者的頁面複製 **user_id**（`auth0|…`）給 `GEMINUS_JUDGE_SUBJECT`。密碼直接寫進提交說明，不存到其他地方。
5. 執行 `GEMINUS_AUTH0_SETUP=manual GEMINUS_AUTH0_CLIENT_ID='<Client ID>' GEMINUS_JUDGE_SUBJECT='auth0|<評審的 user_id>' deploy/railway/deploy.sh --apply`（其他輸入同 deploy runbook §7.7）：seed 把評審帳號設為 demo 組織的 editor 並檢查它不是其他組織的成員；`BFF_OIDC_CLIENT_SECRET` 未填時停在 verification，填好並 Seal 後重跑。demo 組織若已經 seed 過，見 deploy runbook §7.8 的「評審帳號的 membership」。
6. 對照 deploy runbook §7.8 的表逐項確認（例如 API 的 Settings 與 application 的 Settings、Credentials、Connections 三頁），再做 §5 的檢查。
