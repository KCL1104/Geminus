# T-043 驗證報告：瀏覽器登入 session（BFF，ADR-018）

**日期：2026-10-19｜狀態：`in_progress`（BFF 部分完成，送獨立 reviewer；登入後的 UI bootstrap 等 D-045，見下）｜需求：REQ-019｜Gate：D-045（PROPOSED，待 owner）｜驗收測試：TEST-009（BFF 部分）、TEST-032**

範圍依 tasks.json：

- Next.js 伺服器端是 OIDC confidential client：Authorization Code＋PKCE（S256）、state、nonce；endpoints 只來自部署設定；通用 OIDC library，版本釘住並記錄 license；
- `__Host-` HttpOnly／Secure／SameSite=Lax 的加密 session cookie（AES-256-GCM、key 輪替、超過 3.5 KB 為錯誤）；瀏覽器 JS 拿不到 token；callback 後丟棄 ID token；
- 同源 `/api/v1/…` proxy：非 GET 檢查 Origin 與 `X-Geminus-Request`、不開 CORS、伺服器端 refresh、Bearer 轉給 Go API、不轉 cookie；串流；`lib/api/client.ts` 改呼叫 BFF；
- Logout：清 cookie、撤銷 refresh token、RP-initiated logout；未知 identity 顯示 403；
- 測試用 deterministic OIDC provider；Auth0 設定寫入 runbook，由 owner 建立帳號。

## 交付內容

| 位置 | 內容 |
|---|---|
| `apps/web/lib/session/seal.ts` | AES-256-GCM 封裝：目前的 key 封裝、前一把也能打開（輪替）；AAD 為 cookie 名稱與格式版本，所以為一個 cookie 封裝的值不能當另一個用；`SESSION_KEYS` 最多兩把、每把 32 bytes base64url（只接受 canonical 編碼），錯誤訊息不含 key；打不開、格式不對、超過大小一律視為沒有 |
| `apps/web/lib/session/session.ts` | 兩個 cookie：`__Host-geminus_session`（access／refresh token、到期時間、建立與最後使用時間；閒置 12 小時或滿 7 天結束；未來的時間不信任）與 `__Host-geminus_login`（state、nonce、code verifier、回到的路徑；10 分鐘）；屬性 `Path=/; HttpOnly; Secure; SameSite=Lax`、無 Domain；回到的路徑只接受同源的 `/…`（拒絕 `//host`、`/\host`、scheme、控制字元） |
| `apps/web/lib/session/oidc.ts` | openid-client 6.8.8：部署設定的 endpoints（不 discovery）、`client_secret_post`、RS256；login 帶 `audience`；callback 驗 state、PKCE、ID token（iss、aud、exp、nonce、sub，**另驗簽章**，見下）後丟棄；refresh 保留 rotation 回來的新 refresh token；revocation 與 end-session URL |
| `apps/web/lib/session/bff.ts` | `/auth/login`、`/auth/callback`（失敗導向 `/auth/error`，原因只有 expired／refused／failed，不顯示 provider 的回應）、`/auth/logout`（CSRF 檢查、撤銷失敗仍清 cookie）、`/api/v1/…` proxy（見下）；BFF 自己的錯誤用 API 的 error envelope 與 D-001 request ID |
| `apps/web/lib/session/config.ts`、`server.ts` | `BFF_*` 與 `SESSION_KEYS`：URL 必須是 https（全部都在 loopback 時才可 http）、無 credentials／fragment，origin 不能有 path；scope 必須含 `openid`；錯誤只列變數名稱。設定不完整時所有 auth 與 API route 回 503 |
| `apps/web/app/auth/{login,callback,logout}/route.ts`、`app/api/v1/[...path]/route.ts`、`app/auth/error/page.tsx` | 一行的 route 檔；錯誤頁的文字在 catalog（en、zh-Hant） |
| `apps/web/lib/api/client.ts`、`provider.tsx` | 只呼叫同源 `/api/v1/…`，帶 `X-Organization-ID` 與 `X-Geminus-Request: 1`，`credentials: 'same-origin'`、`redirect: 'error'`，不持有 token；`NEXT_PUBLIC_API_BASE_URL` 與 `lib/config.ts` 移除 |
| `apps/web-e2e/fake/servers.mjs`、`playwright.config.ts` | 測試用 OIDC provider（:3101）與 API 替身（:3102），Playwright 先啟動它們，再以指向它們的 `BFF_*` 啟動 app；絕不連 Auth0 |
| 文件 | `docs/runbooks/auth0.md`（owner 的 Auth0 設定步驟與部署變數）、ADR-011（openid-client 版本與 license）、ADR-016 補記、SECURITY §3、`.env.example`、`apps/web-e2e/README.md` |

### Proxy 的規則

- 非 GET／HEAD：`Origin` 必須等於 `BFF_APP_ORIGIN`、瀏覽器送了 `Sec-Fetch-Site` 時必須是 `same-origin`、且有 `X-Geminus-Request: 1`；不開 CORS。
- 轉給 API 的 header 只有 accept、content-type、if-match、idempotency-key、x-organization-id、last-event-id，加上 BFF 的 `Authorization: Bearer`；cookie、瀏覽器自己的 Authorization、X-Forwarded-For 等都不轉。
- 回給瀏覽器的 header 只有 content-type、etag、x-request-id、retry-after、content-disposition、cache-control；API 的 Set-Cookie 與 Location（會洩漏內部位址）不回。Body 雙向串流。
- Path 只接受一般的 segment（不能以 `.` 開頭、不能含分隔字元），所以到不了 `/v1` 以外。
- Access token 剩不到 60 秒時在伺服器端 refresh。同一個 refresh token 只 refresh 一次：並發的 request，以及 30 秒內仍帶舊 cookie 的晚到 request，共用同一個結果；否則 rotation 會把第二次使用當成竊用而撤銷整個 session。失敗的 refresh 不保留，下一個 request 會再試；provider 拒絕時清 cookie 並回 401。
- Session 的最後使用時間最多每 5 分鐘寫回一次。

## 實際執行結果

BFF 部分（74e327a、f83c692；之後的 commit 未改 `apps/`）在 52c667c 的乾淨 worktree 執行，Node 24.21.0、4 CPU；沒有 DB，也沒有連任何真的 identity provider。Log 在 `checks/evidence/2026-10-19-T-043/final/`，exit code 見 `exit-codes.txt`。

| 指令 | Exit | 說明 |
|---|---|---|
| `make frontend-test` | 0 | lint（0 warning）、typecheck、unit：17 個檔案 334 個 test；其中 session、BFF 與 server-only guard 三個檔案 117 個 |
| `make frontend-build` | 0 | `next build`：`/auth/login`、`/auth/callback`、`/auth/logout`、`/auth/error`、`/api/v1/[...path]` 都是動態 route |
| `make e2e-test` | 0 | Chromium 49 passed、16 skipped：skipped 的是截圖測試，只在 CI 的固定 Playwright container 中執行（NOT RUN）。TEST-032 的 `session.spec.ts` 8 個全過，使用 `apps/web-e2e/fake/servers.mjs` 的測試 provider 與 API 替身 |
| `npm audit --omit=dev`（apps/web） | 0 | 0 vulnerabilities |
| `npm ls openid-client oauth4webapi jose` | 0 | openid-client 6.8.8、oauth4webapi 3.8.8、jose 6.2.12 |
| `ts-mutants.py`（43 個） | 0 | `ts-mutants.log`：41 killed、2 等價（理由在 harness：proxy 一律覆寫 Authorization；oauth4webapi 本身就要求 `sub`），0 unexpected。第一次執行（`ts-mutation-first-run.log`）有 3 個存活，補測試後被擋下（f83c692） |
| `make spec-check` | 0 | 重產的 `checks/*.json` 與 commit 相同（`spec-check-status.txt` 為空） |

CI（GitHub Actions `ci`）在 f83c692 與之後的 commit 都通過，包含在固定 container 中的截圖比對（沿用現有 baseline；BFF 沒有改變任何頁面的外觀）。

### 先紅後綠與 mutation 找到的缺口

- openid-client 對 token endpoint 回來的 ID token 只信任 TLS、不驗簽章；測試以另一把 key 簽的 ID token 登入成功（紅），開啟 non-repudiation checks、以 JWKS 驗簽後被拒（綠）。
- Playwright 的 `context.cookies(url)` 在 http URL 上不回傳 Secure cookie，E2E 一開始看不到 session cookie；改為取全部 cookie 再依 domain 過濾。
- 第一次 mutation 有 3 個存活：reuse 期限結束、失敗的 refresh 不保留、redirect URI 不隨 request 的 host 改變；補測試後被擋下（另補 non-canonical base64、401 清 cookie 的測試）。

## 決策與 owner 知悉

- **D-045（PROPOSED，待 owner）**：登入之後的 UI bootstrap（登入／登出按鈕、organization 選擇、從 API 取得 role、未知 identity 在 UI 顯示 403）需要 `GET /v1/me`。在 D-045 批准並實作前，BFF 已能登入、轉送與登出，但 UI 仍一律顯示未登入（沒有元件知道 session 存在）。T-043 的 decision gate 因此包含 D-045；BFF 部分先送審。
- 加入登入／登出按鈕會改變每個頁面的 header，所有截圖 baseline 需要重做；依 T-045 的規則只能由 owner 以 CI 的 `update_screenshots` 產生並確認後提交。
- Auth0 tenant 與 credential 由 owner 依 `docs/runbooks/auth0.md` 建立；agent 沒有建立帳號、沒有任何 credential，也沒有連過 Auth0。
- 新依賴：openid-client 6.8.8（MIT），帶入 oauth4webapi 3.8.8 與 jose 6.2.12（MIT），三者皆於 2026-09-05 發布；`npm audit --omit=dev` 為 0。

## 與規劃的差異

- tasks.json 列了 `internal/auth/authtest/` 作為測試 provider 的位置；改為兩個 TypeScript 的測試 provider：單元測試用的 in-process `apps/web/tests/fake-oidc.ts` 與瀏覽器測試用的 `apps/web-e2e/fake/servers.mjs`。Go 的 `authtest` 是給 Go API 測試的 JWT 簽發器，E2E 這次不需要 Go API（API 以替身代替；完整的瀏覽器→BFF→Go API→DB 閉環是 T-040）。
- 「未知 identity 顯示 403」：BFF 原樣轉送 API 的 403（E2E 以 API 替身驗證）；UI 顯示要等 D-045 的 bootstrap。
- 實作先於測試寫成的部分以 mutation 佐證（見上）。

## NOT RUN／延後

| 項目 | 原因／歸屬 |
|---|---|
| 連 Auth0 的檢查 | 沒有 tenant 與 credential；`docs/runbooks/auth0.md` §5 在 staging 由人執行 |
| 登入 UI、organization 選擇、UI 中的 403 | D-045 待 owner；之後隨新截圖 baseline 一起做 |
| 瀏覽器→BFF→Go API→DB 的閉環 | T-040 |
| 多個 web instance 之間的 refresh 協調 | P0 單一 instance；多 instance 時以 Auth0 的 reuse interval 30 秒涵蓋（runbook） |
| Login、callback、refresh 的 rate limiting 與告警 | T-036／T-035（ADR-018 §3） |
