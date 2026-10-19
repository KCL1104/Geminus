# Runbook：Auth0 tenant 與瀏覽器 session（BFF）設定

依據：[ADR-018](../adr/ADR-018-browser-session-and-oidc-provider.md)（BFF、Auth0）、[ADR-017](../adr/ADR-017-oidc-token-verification.md)（API 驗 token）、T-043。

**這些步驟由 owner 執行。** Agent 不建立帳號、不接受條款、不持有任何 credential；repo 與 CI 都沒有 Auth0 的設定或密鑰，測試只用 repo 內的測試 provider（`apps/web/tests/fake-oidc.ts`、`apps/web-e2e/fake/servers.mjs`）。

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
| Rotation 的 reuse interval | 只有一個 web instance 時設 0；多個 instance 時設 30 秒，與 BFF 在 process 內共用 refresh 結果的時間相同（`REFRESH_REUSE_MS`） |
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

設定不完整或不合格時，BFF 的 `/auth/*` 與 `/api/v1/*` 一律回 503，log 只寫變數名稱，不寫值。

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

自動化的對應測試是 `apps/web/tests/bff.test.ts`（單元）與 `apps/web-e2e/tests/session.spec.ts`（瀏覽器，TEST-032），兩者都只連測試 provider；連 Auth0 的檢查只能在 staging 由人執行（NOT RUN in CI）。
