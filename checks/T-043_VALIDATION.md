# T-043 驗證報告：瀏覽器登入 session（BFF，ADR-018）

**日期：2026-10-19｜狀態：`in_progress`（BFF 部分：review round 1、2 皆為 CHANGES REQUESTED，修正後送 round 3；登入後的 UI bootstrap 等 D-045，見下）｜需求：REQ-019｜Gate：D-045（PROPOSED，待 owner）｜驗收測試：TEST-009（BFF 部分）、TEST-032**

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

Review round 2 的修正（9875be4）在 02a4200（其上只有 T-019 的 commit，未改 `apps/`、`internal/auth`）的乾淨 worktree 執行，Node 24.21.0、4 CPU；沒有 DB，也沒有連任何真的 identity provider。Log 在 `checks/evidence/2026-10-19-T-043/final/`，exit code 見 `exit-codes.txt`。先前各輪的數字：送審時（52c667c）334 個 unit test、43 個 mutant；round 1 修正後（3f743eb）366 個、72 個。

| 指令 | Exit | 說明 |
|---|---|---|
| `make frontend-test` | 0 | lint（0 warning）、typecheck、unit：18 個檔案 392 個 test；其中 session、BFF、server 與 server-only guard 四個檔案 175 個 |
| `go test -race ./internal/auth/` | 0 | 含 `TestCookiesAreNotRead` 與 `TestNoGoCodeHandlesCookies`（TEST-032：Go API 不讀寫 cookie） |
| `make frontend-build` | 0 | `next build`：`/auth/login`、`/auth/callback`、`/auth/logout`、`/auth/error`、`/api/v1/[...path]` 都是動態 route |
| `make e2e-test` | 0 | Chromium 49 passed、16 skipped：skipped 的是截圖測試，只在 CI 的固定 Playwright container 中執行（NOT RUN）。TEST-032 的 `session.spec.ts` 8 個全過（真正的跨站 form），使用 `apps/web-e2e/fake/servers.mjs` 的測試 provider 與 API 替身 |
| `npm audit --omit=dev`（apps/web） | 0 | 0 vulnerabilities |
| `npm ls openid-client oauth4webapi jose` | 0 | openid-client 6.8.8、oauth4webapi 3.8.8、jose 6.2.12 |
| `ts-mutants.py`（93 個） | 0 | `ts-mutants.log`：92 killed、1 等價（`proxy-forwards-authorization`：proxy 在複製 header 之後一律覆寫 Authorization），0 unexpected |
| `make spec-check` | 0 | 重產的 `checks/*.json` 與 commit 相同（`spec-check-status.txt` 為空） |

CI（GitHub Actions `ci`）：f83c692 的 run 39201576112、5d8f44e 的 run 39206441390 與 9ca5ccd 的 run 39216342157 全部 job 通過，其中「browser checks (axe, keyboard, strings, screenshots)」job 在固定 container 中執行（沿用現有截圖 baseline；BFF 沒有改變任何頁面的外觀）。本次修正推上後以 CI 再確認。

### 先紅後綠與 mutation 找到的缺口

- openid-client 對 token endpoint 回來的 ID token 只信任 TLS、不驗簽章；測試以另一把 key 簽的 ID token 登入成功（紅），開啟 non-repudiation checks、以 JWKS 驗簽後被拒（綠）。
- Playwright 的 `context.cookies(url)` 在 http URL 上不回傳 Secure cookie，E2E 一開始看不到 session cookie；改為取全部 cookie 再依 domain 過濾。
- 送審前的 mutation：reuse 期限結束、失敗的 refresh 不保留、redirect URI 不隨 request 的 host 改變三個 mutant 起初存活，補測試後被擋下（那次執行的 log 沒有保留）。f83c692 最後一次之前的執行（`ts-mutation-before-review.log`，原名誤標為 first run）有 3 個存活：`non-canonical-base64` 補測試後被擋下；`proxy-forwards-authorization` 為等價；`no-subject-check` 當時被誤判為等價（review round 1 M-1d，見下）。

## Review round 1（74e327a、f83c692、5b02db2：CHANGES REQUESTED）的修正

獨立 reviewer 的 M-1、M-2、L-1–L-8 全部處理，Info 中可在本任務做的也做了；沒有任何一項以「不修」結案。證據欄的 mutant 名稱見 `ts-mutants.py`。

| 項目 | 修正 | 證據 |
|---|---|---|
| **M-1a** 所有 session 共用 reuse cache 的 key 時，B 會拿到 A refresh 後的 token，沒有測試 | reuse cache 抽成 `RefreshCache`（以 refresh token 的 digest 為 key、上限、30 秒、失敗不保留） | `gives two sessions refreshing at once their own tokens`；`RefreshCache` 的 3 個單元測試；`refresh-key-constant` |
| **M-1b** 輪替後仍以舊 key 封裝，沒有測試 | （程式本來正確）補測試 | `seals under the current key after a rotation, never the previous one`；`seal-with-previous-key`、`fixed-iv` |
| **M-1c** `server.ts` 沒有測試，設定不完整時的 503 未驗證 | 新的 `tests/server.test.ts`：三種錯誤設定都回 503、handler 不被呼叫、log 只寫一次且只有變數名稱（不含 client secret 與 key）；完整設定只建一次 | `unconfigured-is-500`、`built-per-request` |
| **M-1d** `no-subject-check` 不是等價：oauth4webapi 只檢查 `sub` 是字串，`sub: ""` 會通過 | 從 EQUIVALENT 移除，補 `sub: ""` 的測試 | `refuses an ID token with an empty subject`；`no-subject-check` 現在 killed |
| **M-2** provider 暫時失敗會登出使用者；報告與測試都說下一個 request 會再試，但測試重送的是 401 已叫瀏覽器刪掉的 cookie | `OidcFailure.refused`：只有 `invalid_grant`、沒有 refresh token，或回應無法放進 session 時才結束 session（401、清 cookie）；5xx、連不上、逾時、429 等回 503 `SERVICE_UNAVAILABLE`，不送 Set-Cookie。測試改為瀏覽器保留原 cookie 後重試 | `keeps the session when the provider answers 503 / cannot be reached / answers 429 too_many_requests`；`outage-ends-the-session`、`outage-clears-the-cookie`、`every-refresh-failure-temporary`、`every-provider-error-refuses`（429 的案例是為了它補的：oauth4webapi 只對 4xx 的 JSON 錯誤丟 `ResponseBodyError`） |
| **L-1** E2E 的「跨站 form」其實是同源 | 以 `page.route` 提供 `http://evil.test` 的頁面，送出寫入與登出兩個 form：都是 403，瀏覽器送出的 Origin 為 `http://evil.test`、沒有帶 session cookie，登出沒有生效（之後同源 API 呼叫仍為 200）。Chromium 在這個 http 頁面不送 `Sec-Fetch-Site`，所以不斷言它 | `session.spec.ts` |
| **L-2** token 大小限制不一致：讀取拒絕超過 2048 字的 token，封裝卻接受；超過 cookie 上限時 callback 500、refresh 後新 token 遺失 | `finishLogin`／`refresh` 以與讀取相同的規則（`sessionToken`：1–2048 個可見 ASCII）檢查 token，不合格即 `OidcFailure`；callback 封裝前捕捉 `SealedTooLarge` 導向錯誤頁；proxy 在呼叫 API 前封裝，放不下即結束 session（舊 refresh token 可能已被 rotation 用掉） | 3 個 callback 案例與 1 個 refresh 案例；`tokens-unchecked`、`too-large-at-the-callback-is-500`、`too-large-at-refresh-is-500` |
| **L-3** 非 Latin-1 的 `return_to` 讓 callback 500 | `safeReturnTo` 只接受可見 ASCII（瀏覽器送出的路徑本來就是 percent-encoded），其他回 `/` | `safeReturnTo` 的新案例、`returns to a percent-encoded path, and to / for a raw non-ASCII one`；`return-to-non-ascii` |
| **L-4** API 的 `nosniff` 被丟掉；BFF 加 Set-Cookie 時仍轉送上游的 `cache-control` | proxy 與 BFF 自己的錯誤回應一律 `X-Content-Type-Options: nosniff`；帶 Set-Cookie 的回應一律 `Cache-Control: no-store` | `answers with nosniff, and never lets a cache store a response that sets the session`；`no-nosniff`、`cookie-response-cacheable` |
| **L-5** 時鐘容差為 0 | 封裝的時間可以比現在晚 60 秒（`SKEW_MS`），session 與 login transaction 都是 | session 與 login 的 skew 案例；`session-from-the-future`、`no-skew-tolerance`、`login-from-the-future` |
| **L-6** `accessExpiresAt` 的測試在負載下會 flaky（openid-client 的 `expiresIn()` 以 `Date.now()` 計算） | 改用回應本身的 `expires_in` | `bff.test.ts` 的精確斷言不再依賴時間 |
| **L-7** 報告與 evidence 的錯誤 | first-run log 改名並如實描述（見上）；TEST-032 的「Go API 不讀 cookie」新增 Go 測試 `TestCookiesAreNotRead`（各種名稱的 cookie 帶有效 token、沒有 Authorization 時一律 401、handler 不被呼叫）；CI 的通過以 run ID 記錄（見上方結果）；runbook 改為 `/auth/error` 照常顯示 | `internal/auth/http_test.go` |
| **L-8** 串流、取消、回傳的 content-type、`last-event-id`、`redirect: manual`、method allowlist、cache 上限沒有測試 | 補測試：256 KB 上傳以 stream 轉送（`duplex: half`）且內容相同；SSE 在上游結束前就讀到第一個事件；瀏覽器取消會傳到 API 的 signal；上游的 302 不被跟隨、`Location` 不回傳；OPTIONS、PROPFIND、PURGE 回 400 且不呼叫 API；`RefreshCache` 的上限 | `upload-buffered`、`download-buffered`、`no-cancellation`、`no-content-type-back`、`no-last-event-id`、`follow-api-redirects`、`any-method`、`refresh-cache-unbounded` |
| Info：登出後 30 秒內，舊 cookie 仍可從 reuse cache 拿到新 token | 登出時 `RefreshCache.forget` 丟棄該 session 用掉與發出的 refresh；SECURITY §3 寫明無狀態 session 的限制（被複製的 cookie 在 access token 到期前仍有效，至多 10 分鐘） | `after logout, an older cookie cannot get the session's tokens from the shared refresh`；`logout-keeps-the-shared-refresh`、`forget-keeps-what-it-issued`、`forget-keeps-what-it-spent` |
| Info：server-only guard 只追 relative import | 新增測試確認 `tsconfig.json` 沒有 `paths`／`baseUrl`（有的話 alias import 會繞過掃描）；沒有加 `import "server-only"`：Next 內建的 alias 沒有型別，需要新依賴或 vitest alias，留待需要時決定 | `server-only.test.ts` |
| Info：client IP、proxy timeout、Auth0 logout 的 `id_token_hint` | 記入下方 NOT RUN／移交（T-036 的 rate limiting 需要決定是否由 BFF 轉送可信的 client IP；T-023 的 SSE 需要比 undici 預設 300 秒更頻繁的 heartbeat）；runbook §5 加上 staging 檢查 Auth0 登出是否要求確認 | 文件 |

Reviewer 誤在 repository 目錄執行了一次 `npx vitest`（下載 vitest 到 `~/.npm/_npx`、改寫 `node_modules/.vite` 的 cache）；`git status` 乾淨，沒有影響任何 commit。

## Review round 2（3f743eb、9ca5ccd：CHANGES REQUESTED）的修正

Round 2 確認 round 1 全部修好並有測試釘住；提出新的 M-1、L-1–L-4 與 I-1–I-6。全部處理。

| 項目 | 修正 | 證據 |
|---|---|---|
| **M-1** Refresh 之後 Go API 連不上時，503 沒有帶已封裝好的新 cookie：瀏覽器留著已被 rotation 的 refresh token，30 秒後被當成 reuse，整個 session 被撤銷（round 1 之前就有的問題） | API 連不上的 503 也帶新的 session cookie；API 回任何 status 時本來就會帶 | `delivers the refreshed session when the API does not answer / answers an error`（31 秒後以新 cookie 仍為 200）；`api-down-drops-the-refreshed-cookie`、`cookie-only-on-2xx` |
| **L-1** Provider 回 200 但 openid-client 拒絕的 refresh 被當成暫時失敗（舊 token 可能已被用掉） | `refusal()` 依 provider 的回答分類：只有沒有回答、逾時、5xx、429、`temporarily_unavailable`／`server_error`／`too_many_requests`／`invalid_client`，或沒有 OAuth 錯誤的非 2xx 回答才保留 session；2xx 但無法使用（例如 ID token 簽章不對、token type 不明、空的 refresh token）結束 session | 4 個「refresh 回答無法使用」的案例；refresh 回答帶有效 ID token 時通過；`unusable-answer-temporary`、`unexpected-2xx-temporary` |
| **L-2** 其他 OAuth 拒絕最多 503 長達 12 小時；log 分不出原因 | 分類如上（`access_denied`、`unauthorized_client`、`invalid_scope`、`invalid_request` 與未知的錯誤碼都結束 session；`invalid_client` 視為部署設定錯誤而保留）；log 帶 OAuth 錯誤碼與 HTTP status（不帶 description）；provider 不可用的 503 帶 `Retry-After: 5`。Runbook §5 加上在 staging 確認 Auth0 對封鎖使用者與過期 refresh token 回的錯誤碼 | 16 個依回答分類的案例（含 WWW-Authenticate 與 201），逾時與連不上的案例；`network-refuses`、`timeout-refuses`、`invalid-client-refuses`、`only-429-is-temporary`、`challenge-*`、`codes-not-logged`、`no-retry-after` |
| **L-3** 放不進 session 的 token 在 provider 端仍然有效 | 發出卻不保留的 refresh token 盡力撤銷：callback 的 token 不合格或放不進 cookie、refresh 的 token 不合格或放不進 cookie | callback 與 refresh 的案例都檢查 provider 的撤銷紀錄；`unkept-login-token-left-live`、`unkept-callback-token-left-live`、`unkept-refresh-token-left-live` |
| **L-4** 測試缺口 | refresh 時 token 不合格（原本只有放不進 cookie 的案例）；不 rotation 的 provider 保留舊 refresh token；BFF 自己的錯誤與登出回應的 nosniff；`SKEW_MS`、`REFRESH_REUSE_MS`、`REFRESH_MARGIN_MS` 的值 | `refresh-unfit-tokens-temporary`、`refresh-drops-unrotated-token`、`apierror-without-nosniff`、`logout-without-nosniff`、`skew-a-day`、`reuse-window-an-hour` |
| **I-1** `proxy-forwards-authorization` 的等價成立 | — | — |
| **I-2** 登出時仍在進行中的 refresh 可能讓那個 request 拿到仍有效的 cookie | SECURITY §3 寫明（refresh token family 已撤銷，access token 在到期前至多 10 分鐘仍可用；無狀態 session 的限制） | 文件 |
| **I-3** 被取消的 request 或遺失的回應拿不到 rotation 後的 cookie | Runbook：Auth0 的 reuse interval 改為一律 30 秒（只有一個 instance 時也不設 0），由 owner 在 Auth0 設定 | `docs/runbooks/auth0.md` |
| **I-4** 登出的 JSON 回應沒有 nosniff | 加上 | `logout-without-nosniff` |
| **I-5** OPTIONS 在 Next 由框架回應，proxy 的 OPTIONS 測試只是後備 | 記錄於此；後備仍保留並測試 | — |
| **I-6** `TestCookiesAreNotRead` 範圍窄 | 新增 `TestNoGoCodeHandlesCookies`：`internal/`、`cmd/` 中非測試的 Go 程式不得讀寫 cookie（`.Cookie(`、`.Cookies(`、`"Cookie"`、`http.Cookie`、`SetCookie`）；以一個讀 `session` cookie 的 probe 檔確認會失敗 | `internal/auth/http_test.go` |

## 決策與 owner 知悉

- **D-045（PROPOSED，待 owner）**：登入之後的 UI bootstrap（登入／登出按鈕、organization 選擇、從 API 取得 role、未知 identity 在 UI 顯示 403）需要 `GET /v1/me`。在 D-045 批准並實作前，BFF 已能登入、轉送與登出，但 UI 仍一律顯示未登入（沒有元件知道 session 存在）。T-043 的 decision gate 因此包含 D-045；BFF 部分先送審。
- 加入登入／登出按鈕會改變每個頁面的 header，所有截圖 baseline 需要重做；依 T-045 的規則只能由 owner 以 CI 的 `update_screenshots` 產生並確認後提交。
- Auth0 tenant 與 credential 由 owner 依 `docs/runbooks/auth0.md` 建立；agent 沒有建立帳號、沒有任何 credential，也沒有連過 Auth0。
- **Auth0 的 rotation reuse interval（owner 決定）**：runbook 建議一律設 30 秒（round 2 I-3）。代價是：refresh token 被 rotation 後 30 秒內，被竊的舊 token 仍能換到新 token 而不觸發 reuse detection；好處是被取消的 request 或遺失的回應不會讓使用者被登出。設 0 也能運作，只是上述情況會結束 session。
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
| Login、callback、refresh 的 rate limiting 與告警 | T-036／T-035（ADR-018 §3）。BFF 不轉送 client IP（不送 X-Forwarded-For），API 端的 rate limiting 若要以 IP 區分，T-036 需決定由 BFF 轉送可信的 client IP |
| SSE 經過 proxy 的長時間連線 | proxy 沒有自己的 timeout，undici 預設 300 秒；T-023 的 run events 需要比這更頻繁的 heartbeat |
| Auth0 登出是否要求確認（BFF 不帶 `id_token_hint`） | staging，`docs/runbooks/auth0.md` §5 第 5 點 |
