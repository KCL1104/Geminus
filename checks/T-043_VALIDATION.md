# T-043 驗證報告：瀏覽器登入 session（BFF，ADR-018）

**日期：2026-10-19｜狀態：`in_progress`（BFF 部分：review round 1、2 為 CHANGES REQUESTED，round 3 為 ACCEPT 並有 5 個 low finding，已修正，修正由 T-043 結案前的 final review 審查；D-045 的 API 與 client 部分（`GET /v1/me`）已實作（161f62e），獨立 review ACCEPT 並有 3 個 low finding，已修正，見「D-045 slice」；登入後的 UI bootstrap 於 2026-10-26 實作（branch `t043-bootstrap`），待獨立 review，見最後一節）｜需求：REQ-019｜Gate：D-045（APPROVED，H-010）｜驗收測試：TEST-009（BFF 部分）、TEST-032**

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
- Access token 剩不到 60 秒時在伺服器端 refresh。同一個 refresh token 只 refresh 一次：並發的 request，以及 30 秒內仍帶舊 cookie 的晚到 request，共用同一個結果；否則 rotation 會把第二次使用當成竊用而撤銷整個 session。Provider 拒絕的 refresh 不保留，清 cookie 並回 401；provider 不可用的 refresh 回 503（`Retry-After: 5`），這個失敗共用 5 秒（`REFRESH_RETRY_MS`），之後的 request 再試。是否拒絕只看 token endpoint 本身的回答（SECURITY §3）。
- Session 的最後使用時間最多每 5 分鐘寫回一次。

## 實際執行結果

Review round 3 的修正（dade6c2）在該 commit 的乾淨 worktree 執行，Node 24.21.0、4 CPU；沒有 DB，也沒有連任何真的 identity provider。Log 在 `checks/evidence/2026-10-19-T-043/round3/`，exit code 見 `exit-codes.txt`。先前各輪的數字：送審時（52c667c）334 個 unit test、43 個 mutant；round 1 修正後（3f743eb）366 個、72 個；round 2 修正後（9875be4，在 02a4200 執行，log 在 `final/`）392 個、93 個。

| 指令 | Exit | 說明 |
|---|---|---|
| `make frontend-test` | 0 | lint（0 warning）、typecheck、unit：18 個檔案 408 個 test；其中 session、BFF、server 與 server-only guard 四個檔案 191 個（另行執行該四個檔案得出） |
| `go test -race ./internal/auth/` | 0 | 含 `TestCookiesAreNotRead` 與 go/ast 的 `TestNoGoCodeHandlesCookies`（TEST-032：Go API 不讀寫 cookie） |
| `make frontend-build` | 0 | `next build`：`/auth/login`、`/auth/callback`、`/auth/logout`、`/auth/error`、`/api/v1/[...path]` 都是動態 route |
| `make e2e-test` | 0 | Chromium 50 passed、16 skipped：skipped 的是截圖測試，只在 CI 的固定 Playwright container 中執行（NOT RUN）。TEST-032 的 `session.spec.ts` 9 個全過（真正的跨站 form；API 送出 header 後斷線仍拿到新 cookie），使用 `apps/web-e2e/fake/servers.mjs` 的測試 provider 與 API 替身。Log 中唯一的 `failed to pipe response` 是斷線那個測試刻意造成的 |
| `npm audit --omit=dev`（apps/web） | 0 | 0 vulnerabilities |
| `npm ls openid-client oauth4webapi jose` | 0 | openid-client 6.8.8、oauth4webapi 3.8.8、jose 6.2.12（沒有新依賴） |
| `ts-mutants.py`（105 個） | 0 | `ts-mutants.log`：104 killed、1 等價（`proxy-forwards-authorization`：proxy 在複製 header 之後一律覆寫 Authorization），0 unexpected |
| `make spec-check` | 0 | 重產的 `checks/*.json` 與 commit 相同（`spec-check-status.txt` 為空） |

CI（GitHub Actions `ci`）：f83c692 的 run 39201576112、5d8f44e 的 run 39206441390、9ca5ccd 的 run 39216342157、626fa25 的 run 39233159253、814a805 的 run 39239777512 與 dade6c2（本輪修正）的 run 39242110814 全部 job 通過，其中「browser checks (axe, keyboard, strings, screenshots)」job 在固定 container 中執行（沿用現有截圖 baseline；BFF 沒有改變任何頁面的外觀）。

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
| **L-3** 放不進 session 的 token 在 provider 端仍然有效 | 發出卻不保留的 refresh token 盡力撤銷：callback 的 token 不合格或放不進 cookie、refresh 的 token 不合格或放不進 cookie | callback 的案例檢查 provider 的撤銷紀錄；refresh 的案例當時沒有檢查（round 3 L-4 指出此處寫得過頭，round 3 補上）；`unkept-login-token-left-live`、`unkept-callback-token-left-live`、`unkept-refresh-token-left-live` |
| **L-4** 測試缺口 | refresh 時 token 不合格（原本只有放不進 cookie 的案例）；不 rotation 的 provider 保留舊 refresh token；BFF 自己的錯誤與登出回應的 nosniff；`SKEW_MS`、`REFRESH_REUSE_MS`、`REFRESH_MARGIN_MS` 的值 | `refresh-unfit-tokens-temporary`、`refresh-drops-unrotated-token`、`apierror-without-nosniff`、`logout-without-nosniff`、`skew-a-day`、`reuse-window-an-hour` |
| **I-1** `proxy-forwards-authorization` 的等價成立 | — | — |
| **I-2** 登出時仍在進行中的 refresh 可能讓那個 request 拿到仍有效的 cookie | SECURITY §3 寫明（refresh token family 已撤銷，access token 在到期前至多 10 分鐘仍可用；無狀態 session 的限制） | 文件 |
| **I-3** 被取消的 request 或遺失的回應拿不到 rotation 後的 cookie | Runbook：Auth0 的 reuse interval 改為一律 30 秒（只有一個 instance 時也不設 0），由 owner 在 Auth0 設定 | `docs/runbooks/auth0.md` |
| **I-4** 登出的 JSON 回應沒有 nosniff | 加上 | `logout-without-nosniff` |
| **I-5** OPTIONS 在 Next 由框架回應，proxy 的 OPTIONS 測試只是後備 | 記錄於此；後備仍保留並測試 | — |
| **I-6** `TestCookiesAreNotRead` 範圍窄 | 新增 `TestNoGoCodeHandlesCookies`：`internal/`、`cmd/` 中非測試的 Go 程式不得讀寫 cookie（`.Cookie(`、`.Cookies(`、`"Cookie"`、`http.Cookie`、`SetCookie`）；以一個讀 `session` cookie 的 probe 檔確認會失敗（round 3 I-5 改為 go/ast 檢查，見下） | `internal/auth/http_test.go` |

Round 2 表中引用的分類 mutant（`unusable-answer-temporary`、`unexpected-2xx-temporary`、`network-refuses`、`timeout-refuses`、`only-429-is-temporary`、`challenge-*`）針對的是 round 3 移除的 `refusal()`，在 626fa25 的 `ts-mutants.py` 中；round 3 以下表的 mutant 取代。

## Review round 3（9875be4、626fa25：ACCEPT，5 個 low finding）的修正

Round 3 確認 round 2 全部修好（L-3 的 refresh 路徑沒有測試釘住，列入 L-4），沒有 must-fix；提出 L-1–L-5 與 I-1–I-6，要求在 T-043 結案前修正或記錄。全部處理；這些修正由 T-043 結案前（D-045 與 UI bootstrap 之後）的 final review 審查，本輪不另外送審。

| 項目 | 修正 | 證據 |
|---|---|---|
| **L-1** Refresh 時 JWKS 抓不到被當成 provider 不可用（token endpoint 已回 200，Auth0 已 rotation） | 改由 token endpoint 本身的回答決定：client 的 `customFetch` 只記錄 token endpoint 的 status、body 中的 OAuth 錯誤碼與 2xx 回答中的新 refresh token（`AsyncLocalStorage`，每次 refresh 一份），不從 openid-client 的錯誤型別推論。2xx 但無法使用（包括驗 ID token 的 JWKS 抓不到）結束 session 並盡力撤銷新的 refresh token；log 的 status 是 token endpoint 的 | `ends the session, revoking the issued token, when the ID token's keys cannot be fetched`（冷的 BFF、JWKS 503）；`record-ignores-the-endpoint`、`2xx-answer-kept`、`issued-token-not-recorded`、`issued-token-not-discarded` |
| **L-2** `WWW-Authenticate` 沒有 `error` 參數的 4xx 拒絕被保留 | 錯誤碼一律從 token endpoint 的 body 讀（不看 challenge），所以分類與 log 都與 challenge 無關 | 3 個 challenge 無錯誤碼的案例（400／403／401，Bearer 與 Basic）為 401；log 帶 body 的錯誤碼；`error-code-not-recorded` |
| **L-3** API 送出 header 後、任何 body 之前中斷時，rotation 後的 cookie 到不了瀏覽器 | 帶 Set-Cookie 的回應在 API 的 body 前先送一個空 chunk（`headersFirst`），Next 因此先送出 header | `sends the headers ahead of the API's body when it sets the session`（chunk 為 [0, 2]，不帶 cookie 時為 [2]）；E2E `a refresh is kept when the API's answer is cut after its headers`（API 替身送出 header 後斷線：cookie 已換新，31 秒後仍為 200、0 個撤銷；拿掉 `headersFirst` 時失敗）；`headers-not-first`、`headers-first-always` |
| **L-4** Reviewer 的 mutant 存活 | 非 JSON 的 4xx（403／429 HTML、429 無 body、400 text、404 HTML）保留 session 的表；refresh 回答不合格時檢查撤銷數；`discard()` 失敗不影響回答；200 HTML 與 204 結束 session；5xx 與 429 帶任何錯誤碼都保留；log 的錯誤碼不分 status 都從 body 讀 | `keeps the session when the refresh is answered %i %s without an OAuth error`、`ends the session when the refresh answer has %s`（含撤銷數）、`answers without waiting long for a revocation, and logs one that fails`；`codeless-4xx-refuses`、`5xx-refuses`、`429-refuses`、`unavailable-codes-refuse`、`discard-rethrows`、`unfit-refresh-token-left-live` |
| **L-5** 文件 | SECURITY §3 重寫分類（不再自相矛盾；寫明沒有 OAuth 錯誤的非 2xx 保留、JWKS 的情況結束 session、「token 沒有被用掉」只用於 token endpoint 沒有接受的情況）；runbook 同步；本報告 NOT RUN 的引用改為 §5 第 6 點；round 2 L-3 的證據欄改正 | 文件 |
| **I-1** `discard()` 最多讓回答等到 OIDC client 的 timeout | 回答最多等 `DISCARD_WAIT_MS` = 2 秒，撤銷在背景繼續 | 撤銷 endpoint 不回應時，refresh 的 401 約 2 秒後回答（callback 用同一個 `discard()`，沒有另外計時）；`discard-unbounded` |
| **I-2** `discard()` 失敗沒有 log | 記 `session.discard_failed`（只有原因，不含 token） | 同上（log 中沒有 token）；`discard-failure-silent` |
| **I-3** 發出卻不保留的 token 有些仍有效 | 2xx 但無法使用的 refresh 回答中的新 token（L-1）與不 rotation 時原本會保留的 token 都撤銷；openid-client 拒絕的 callback 回答中的 token 仍未撤銷（login 時沒有任何 session 持有它，在 provider 端自行到期） | `revokes a provider's unrotated token when the session it would have kept ends`；`unrotated-token-left-live` |
| **I-4** `discard()` 沒有洩漏 | — | — |
| **I-5** `TestNoGoCodeHandlesCookies` 是 regex | 改為 go/ast：selector `Cookie`、`Cookies`、`CookiesNamed`、`AddCookie`、`SetCookie`、`ParseCookie`、`ParseSetCookie`，以及兩種字串 literal 中的 `Cookie`／`Set-Cookie`；以 `http.ParseCookie(r.Header.Get(`Cookie`))` 的 probe 確認會失敗後移除 | `internal/auth/http_test.go` |
| **I-6** Auth0 對錯誤 client secret 的回答未確認 | Runbook §5 第 5 點加上錯誤 secret 與失敗的 Action 的 staging 檢查；暫時失敗共用 5 秒（`REFRESH_RETRY_MS`），設定錯誤期間，每個 instance 對同一個 refresh token 每 5 秒最多呼叫 token endpoint 一次（共用表未滿時；滿了就不共用） | `keeps the session when the provider %s, and asks again after the retry window`；`failures-not-shared`、`failures-shared-for-the-reuse-window`、`retry-window-a-minute` |

## D-045 slice：`GET /v1/me`（2026-10-19，agent 實作，161f62e；獨立 review ACCEPT（H-003））

D-045（APPROVED，H-010；解決 CB-17）的 API、DB 與 client 部分，是 T-043 sign-in UI 的前置；UI 本身等 design system，不在這一片。

### 交付內容

| 位置 | 內容 |
|---|---|
| `schemas/http.schema.json` `Me`、`OwnMembership`；`testdata/contracts/` 2 個正例、5 個反例 | `user_id`、`memberships[]`（`organization_id`、`organization_name` 1–200 字、`role`）至多 100、`truncated`；反例：未知 role、多餘成員、缺 `truncated`、空名稱、101 筆 |
| `openapi/openapi.yaml` `GET /me`（`getMe`） | `x-tenant-scope: user`、`x-required-roles: []`；`NoOrganizationContext` 參數（schema `not: {}`：不得送出）；200（schema、兩個 example、`Cache-Control: no-store`）、自己的 400（只列 `INVALID_REQUEST`，帶 header 時）、401、自己的 403（未知 identity）、429、500、503 |
| `scripts/check_contracts.py` | operation 預設在 organization 中：必須要求 `X-Organization-ID` 且列出 role；`x-tenant-scope: user` 的不列 role、宣告 header 不得送出、有 400／401／403、path 不含 organization；operation 自己的錯誤 response 只列該 status 的 code；inline example 依 schema 驗證；`planning/api-operations.json` 的 method、path、origin、roles、scope 與 openapi 逐一相同（原本只比 operation ID） |
| `planning/api-operations.json` | `getMe`（`roles: []`、`scope: "user"`） |
| `migrations/0008_me.up.sql`／`.down.sql` | `own_memberships`、`own_organizations`：permissive、`FOR SELECT TO twin_api`，只在 `app.organization_id` 未設時成立（見 SECURITY §4、API_DATABASE §9） |
| `internal/tenant/self.go` | `DB.RunSelf(ctx, user, fn)`：先驗 D-001 user ID（`ErrInvalidScope`，不碰 DB），唯讀 transaction，經 `internal/scope.Set` 設 organization `""` 與 user，錯誤分類與 transaction-local 設定同 `Run` |
| `internal/storage/memberships.OfUser` | `memberships` join `organizations`，`WHERE m.user_id = $1`，`ORDER BY o.name COLLATE "C", m.organization_id COLLATE "C" LIMIT $2` |
| `internal/tenant/me` | `Service.Get`（向 `OfUser` 要 101 筆，有第 101 筆就只回 100 筆並 `truncated: true`；空清單編成 `[]`）；`Register`／`Routes`（`GET /v1/me`，scope `user`，沒有 role） |
| `internal/httpapi.Self` | 順序同 `Scoped`：驗 token → 帶任何 `X-Organization-ID`（含空值、多個）回 400 `INVALID_REQUEST`（detail reason `X-Organization-ID`）→ `ResolveUser`（未知 identity 403，沿用既有 mapping，不建立 user） |
| `internal/api`（wiring）、`cmd/api` 測試 | `me.Register`；`cmd/api` 實際 serve 的 binary 對 `GET /v1/me` 回 401 |
| `apps/web/lib/api/client.ts` `getMe()`；`lib/contracts/http.ts`（重新產生） | 不需要 organization 的 session、不送 `X-Organization-ID`，其餘與其他 operation 相同（`X-Geminus-Request`、`same-origin`、`redirect: error`、`no-store`、錯誤 mapping）。BFF proxy 不需要改：它只轉送瀏覽器送來的 `x-organization-id`、不會自己加，path 沒有 allowlist（只限一般 segment），測試確認 |
| 文件 | API_DATABASE §2、§9；SECURITY §2、§4；CONTRACT_BLOCKERS CB-17；DECISIONS_AND_TBD D-045 的實作註記；ARCHITECTURE（`internal/tenant`、`internal/httpapi`）；ADR-019 補記；db/README（0008、`self.sql` 與 Go 測試） |

沒有 audit event：本 codebase 的讀取（list／get）都不記 audit，`GET /v1/me` 照做（`TestMe` 檢查）。`Cache-Control: no-store` 本來就由 `httpapi.WriteBody` 加在每個回應上，沒有另外加。Handler 不寫 log；未預期的錯誤只 log 錯誤本身（不含 organization 名稱，`TestHandler`、`TestMe` 檢查 log）。

### 與指定設計的差異

- **排序用 byte 順序（`COLLATE "C"`）。** D-045 只說「依名稱再依 ID」。DB 的預設 collation 各環境不同（CI 為 libc `en_US.utf8`，那裡 `acme` 排在 `Zeta` 前面），截斷到 100 筆時取哪 100 筆也就不同；比照 D-034／CB-15，以 `COLLATE "C"` 讓順序與 Go 的字串比較相同、與環境無關。UI 若要依語言排序，可在畫面上另排（清單至多 100 筆）。寫在 openapi、schema 的 description 與 API_DATABASE §2。
- **欄位名稱**為 parent 指定的 `organization_name`（D-045 原文「organization `name`」）。
- **`X-Organization-ID` 在 openapi 中以參數宣告**（`NoOrganizationContext`，`required: false`、schema `not: {}`），而不只寫在 description，讓 `check_contracts.py` 能檢查。
- **Organization 的整個 row 可讀，不只 `id`、`name`。** D-045 原文為「organization 的 `id`、`name`」；RLS 只能限制 row，不能限制欄位，`own_organizations` 讓 API 讀得到該 row 的所有欄位（`id`、`organization_id`、`name`、`created_at`）。這些欄位成員在選了該 organization 時本來就看得到（SECURITY §4），API 也只讀 `id` 與 `name`（review I2）。
- 其餘照指定設計；沒有做不到的部分。

### 測試

先寫的負向測試：`db/tests/self.sql` 在 0008 不存在時先跑，7 個 case 失敗後停在 savepoint 的 `DROP POLICY`（`red-self-sql-without-0008.log`）；`check_contracts.py` 在 openapi 加入 `/me` 後先以既有的「每個 operation 都要求 `X-Organization-ID`」失敗，再改寫檢查。Go 與 TypeScript 的實作與測試是同時寫的，沒有先紅的紀錄；以下的 mutation 補上這部分的證據。

- **SQL（`db/tests/self.sql`，由 `templates/self.sql.tmpl` 產生；`scripts/run_db_tests.sh` 在兩輪 up 之後都跑，down 先撤 0008）**：四個 organization、三個 user。沒有選 organization、只設 `app.user_id`：恰好是自己的 membership（跨 organization）、恰好是自己所屬的 organization（id、name），看不到同一 organization 其他成員的 membership、其他 organization、任何其他表的 row（逐表）；沒有 user、未知 user：什麼都看不到；`/me` 的 query 與順序。`own_organizations` 的 subquery 受 memberships 的 RLS 限制（savepoint 中拿掉 `own_memberships` 就看不到 organization），但它自己也寫明兩個條件（savepoint 中加一個顯示所有 membership 的 probe policy，仍只看到自己的 organization，選了 organization 時也不多）。SELECT only：INSERT 被拒（42501），UPDATE、DELETE、`FOR SHARE`、`FOR UPDATE` 碰不到 row，organizations 不能寫或鎖，嘗試之後 membership 不變。選了 organization（自己的、不是成員的、不存在的）：API 看到的與 owner role（只有 `tenant_isolation`）看到的逐一相同，且等於預期的 row，比較中確實有其他 tenant 的 row。Policy 是 permissive `FOR SELECT TO twin_api`、兩張表只有 `tenant_isolation` 與這兩個；owner role 沒有選 organization 時看不到。Worker：兩張表都沒有 SELECT（42501），沒有選 organization 時任何表都看不到。
- **Go（真實 runtime login，`dt_test_api_login`）**：`internal/tenant` 的 `TestRunSelf`（只看得到自己的 membership 與 organization，看不到自己 organization 的 project、users；另一個 user、未知 user ID）、`TestRunSelfModeAndScope`（唯讀：寫入 25006；fn 的錯誤原樣；成功、拒絕的寫入、錯誤、panic、取消後連線上沒有 scope，有無 `Guard` 皆然，`Guard` 沒有換過連線）、`TestRunSelfTakesNoLock`（`FOR SHARE`／`FOR UPDATE`／`FOR KEY SHARE` 25006；進行中的 `RunSelf` 不擋 role 變更）、`TestScopeIsValidatedBeforeTheDatabase`（7 個不合格的 user ID，不碰 DB）；`internal/storage/memberships.TestOfUser`（以 superuser 執行，只靠 user filter；`Zeta`、`acme`、`Acme`×2、`Ärzte`、`_Underscore`、`研發部`、`Acme `、`a`、`Z` 與 4 個以 ID 遞減順序插入的同名 organization：byte 順序與 ID tie-break；limit）；`internal/httpapi.TestSelf`（13 個 case：token 先驗、任何 selector 400 且在解析 user 之前、未知 identity 403）；`internal/tenant/me` 的 `TestPage`（0、1、99、100、101、150 筆）、`TestHandler`（10 個 case 與空清單：schema、header、log、拒絕時不讀清單）、`TestRoutesMatchContract`、`TestFixturesRoundTrip`；`internal/api` 的 `TestMe`（經真實 handler 與 TLS 測試 provider：9 個名稱含同名的 organization 依序、另一個 user 的 membership 不出現、空清單、未知 identity 403 且沒有建立 user、401、四種 `X-Organization-ID` 都 400、其他 method 404、撤權與 role 變更立即反映、撤權的 organization 照常 404、沒有 audit、log 沒有 organization 名稱）與 `TestMeTruncates`（101 筆：100 筆且 `truncated`，依名稱順序而非建立順序；恰好 100 筆：不 `truncated`）；`cmd/api.TestRunServesAndStops`（binary serve `GET /v1/me`）。
- **TypeScript**：`tests/api.test.ts`（`getMe` 沒有 session 也送、有 session 也不送 `X-Organization-ID`；6 種回答的 mapping；每個 operation 只有 organization 中的才帶 selector）；`tests/bff.test.ts`（`GET /api/v1/me` 轉成 `/v1/me`、帶 Bearer、不加 selector、`no-store` 回到瀏覽器；瀏覽器送的 selector 原樣轉送由 API 拒絕；未登入 401 且不呼叫 API）。

### 實際執行結果

實作者在共用的 checkout 中（當時 HEAD 為 628c4ca，未 commit）執行下表；Go 1.27.2、Node 24.21.0、PostgreSQL 16.15（自建的一次性 cluster，port 55442，只用 `dt_test_d045_*` 資料庫）。Log 在 `checks/evidence/2026-10-19-T-043/d045/`。Commit 前，parent 在乾淨的 worktree（d040abd 加上這一片，即 161f62e 的內容）重跑 `spec-check`、`backend-test`（36 個 package）、`architecture-check`、`frontend-test`（411 個 test）與全新 `dt_test_d045_verify` 上的 `integration-test`（17 個 Go package、0 skip），皆 exit 0（`parent-verify-exit-codes.txt`、`parent-verify-integration-test.log`；review L3）。

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check PYTHON=.venv/bin/python` | 0 | contracts：openapi 42 個 operation，schema 正例 30（+2）、反例 52（+5，有精確 path 的 47）；db tests 5 個檔案 fresh |
| `make backend-test` | 0 | gofmt、`go vet`、`go test`：36 個 package ok（沒有 DB 時 DB 測試 skip，見下一列以 DB 執行） |
| `make backend-race` | 0 | 36 個 package ok |
| `make architecture-check` | 0 | 44 packages、11 rules、16 build configurations（新 package `internal/tenant/me` 在 `internal/tenant/...` 之下，不需要新規則） |
| `make frontend-test` | 0 | lint（0 warning）、typecheck、unit：18 個檔案 411 個 test（+3） |
| `make integration-test`（全新的 `dt_test_d045_final`，ICU `en-US` collation，`DT_ALLOW_DB_TESTS=yes`） | 0 | `db-test`：up（含 0008）、5 個 SQL suite、down（先撤 0008）、up、5 個 suite；`db-go-test`（`-race`、`GEMINUS_REQUIRE_DB_TESTS=1`）17 個 package ok。`integration-test.log` |
| D-045 的 Go 測試，`-race -v`（同一 DB） | 0 | 6 個 package 的 13 個 test 全過（`d045-go-tests.log`） |
| `go-mutants.py`（ICU DB） | 0 | 20 個 mutant 全部 killed（`go-mutation.log`） |
| `sql-mutants.py`（每個 mutant 一個全新 DB） | 0 | 10 個全部 killed（`sql-mutation.log`）；第一次執行 2 個存活，見下 |
| `ts-mutants.py` | 0 | 6 個全部 killed（`ts-mutation.log`） |
| `contract-mutants.py` | 0 | 10 個全部 killed（`contract-mutation.log`） |

基準：改動前在 C.UTF-8 的全新 DB 上 `make integration-test` exit 0（同一個 cluster）。

### Mutation

四個 harness 都不修改 repository（Go 用 `go test -overlay`，SQL 每個 mutant 一個全新 DB，TS 與 contract 用複本），build／install 失敗算 harness 錯誤，不算 killed；Go 的 test binary crash 算 killed（H-011）。

- **Go（20）**：RunSelf 不驗 user ID、讀寫 transaction、scope 沒有 user；`OfUser` 沒有 user filter、只依 ID 排序、用 DB 的 collation（ICU DB 才殺得掉，所以 harness 在 ICU DB 上跑）、沒有 ID tie-break、名稱倒序；`>=` 截斷（off-by-one）、只要 100 筆（看不出有沒有更多）、不截斷、上限 101、空清單為 nil；selector 不檢查、只拒絕兩個以上、先解析 user 再檢查、忽略 token 錯誤；未知 identity 改回 404；拿掉 `no-store`；route 沒有接上。全部 killed，沒有等價的。
- **SQL（10）**：memberships policy 不限 user、選了 organization 也成立、`FOR ALL`、給每個 role、restrictive；organizations policy 不限 user（任何成員）、不限 organization、選了 organization 也成立、`FOR ALL`、也給 worker。**第一次執行（`sql-mutation-first.log`）有 2 個存活**：`organizations-any-member` 與 `organizations-with-an-organization-selected`。原因：`own_organizations` 的 subquery 受 memberships 的 RLS 限制，所以在現有 policy 下，拿掉它自己的 user 條件或 organization 未設的條件，看到的 row 都不變；它的這兩個條件是 defence in depth，測試看不到。補上 probe policy 的測試（savepoint 中讓 API 看得到所有 membership，`own_organizations` 仍只顯示自己的 organization、選了 organization 時不多），第二次執行 10 個全部 killed。
- **TS（6）**：`getMe` 送 selector、需要 session、路徑錯誤；selector 完全不送；organization 的 operation 沒有 session 也送；沒有 CSRF header。
- **Contract（10）**：`/me` 沒有 scope、要求 selector、沒有 selector 參數、selector 可帶值、有 role；example 不符 schema；自己的 400 列了別的 status 的 code；沒有 403；inventory 沒有 scope、有 role。

### 給 reviewer 的注意事項

- 同一個 checkout 中另一個 session 同時在處理 T-019 round 4，已 commit 為 628c4ca；這一片的檔案不在那個 commit 中，另由 parent 驗證後 commit 為 161f62e（parent 為 d040abd）。
- 這一片沒有跑 `make e2e-test`（UI 沒有變；`apps/web-e2e/fake/servers.mjs` 的 API 替身沒有 `/v1/me`，sign-in UI 需要時再加）。

### Independent review（161f62e：ACCEPT）

獨立 reviewer agent（H-003）於 2026-10-19 以 `git archive 161f62e` 審查：**ACCEPT**，沒有 must-fix。SQL probe 找不到任何讀到他人 membership 或其他 organization row 的方式（未選 organization 的 self-join、相關子查詢、`COPY`；`RESET`、空白、小寫或尾端空白的 scope 值；ResolveUser 式的 transaction；generic plan 的 prepared statement；`row_security=off`、`FOR KEY SHARE`／`FOR NO KEY UPDATE`、upsert、`MERGE`；provisioner 與 worker 42501；沒有 view 或可利用的 definer function）。Reviewer 重跑 `spec-check`、`backend-test`、`backend-race`、`architecture-check`、`frontend-test`、全新 ICU `dt_test_rvd045_main` 上的 `integration-test` 皆 exit 0；作者的 mutants 全部重現（Go 20、SQL 10、TS 6、contract 10 全 killed）；自己的 mutants：Go 19（13 killed、3 等價、3 存活）、SQL 10（9 killed、1 等價）、TS 6（5 killed）、contract 13（7 killed）。

| 項目 | 處理 | 證據 |
|---|---|---|
| **L1** `/me` 的 request context 沒有測試：handler 改用 `context.Background()`、`getMe` 不傳 `signal` 都存活 | `internal/tenant/me.TestTheReadEndsWithTheRequest`（取消的 request context 傳到讀取）；`api.test.ts`「ends the /me request with the caller's signal」 | 兩個 mutant 都被擋下（`d045/review-fix.txt`） |
| **L2** `organization_name` 的 `maxLength: 200`（須等於 DB 的 `length(name) BETWEEN 1 AND 200`）與 `Me` 的 `additionalProperties: false` 沒有 fixture 釘住 | 新 fixture：`valid-me-name-200`（200 個 CJK 字元，以 code point 計）、`invalid-me-name-201`、`invalid-me-extra-member`（多一個頂層成員）、`valid-me-100`（恰好 100 筆、`truncated`）；`TestFixturesRoundTrip` 改為 4 個正例 | `maxLength` 199、500 與 `additionalProperties: true` 三個 mutant 都由 `check_contracts.py` 擋下 |
| **L3** 報告與 tasks.json 寫的驗證基準為 628c4ca 上未 commit 的 worktree，沒有提到 parent 的重新驗證 | 「實際執行結果」與 tasks.json 改正 | 本報告 |
| **I1** collation 的 mutant 只在非 C 的 DB 上擋得下 | 接受並記錄（CI 為 `en_US.utf8`、ICU DB 都擋得下） | 本報告 |
| **I2** Organization 整個 row 可讀 | 列入「與指定設計的差異」 | 本報告 |
| **I3** `check_contracts.py` 不檢查 operation 層級的 `security` 與宣告的 `Cache-Control` header | 接受（`security` 的缺口在這一片之前就存在；實際的 header 由 Go 測試檢查） | — |
| **I4** `EXPLAIN ANALYZE` 的 `Rows Removed by Filter` 透露被 RLS 隱藏的 row 數 | 接受（需要任意 SQL，SECURITY.md 已排除；選了 organization 時亦同） | — |
| **I5** Go 與 TS 測試沒有先紅的紀錄 | 已在本報告說明，由 mutation 補足 | — |
| **I6** `RunSelf` 的唯讀模式來自可被重新指定的 exported 變數 `tenant.ReadOnly` | 改為字面的 `pgx.TxOptions{AccessMode: pgx.ReadOnly}`；`TestRunSelfModeAndScope` 仍檢查寫入得到 25006 | 測試 |

## 決策與 owner 知悉

- **D-045（APPROVED，H-010）**：登入之後的 UI bootstrap（登入／登出按鈕、organization 選擇、從 API 取得 role、未知 identity 在 UI 顯示 403）需要 `GET /v1/me`。API、DB 與 client（`getMe()`）已在「D-045 slice」實作、待獨立 review；在 UI 實作前，BFF 已能登入、轉送與登出，但 UI 仍一律顯示未登入（沒有元件知道 session 存在）。T-043 的 decision gate 因此包含 D-045；BFF 部分先送審。
- 加入登入／登出按鈕會改變每個頁面的 header，所有截圖 baseline 需要重做；依 T-045 的規則只能由 owner 以 CI 的 `update_screenshots` 產生並確認後提交。（2026-10-26 的 bootstrap 沒有改變未登入的 header：登入只在「需要登入」的頁面上，登出只在有 session 時出現；改變的只有未登入的專案頁的 8 個 baseline，見最後一節。）
- Auth0 tenant 與 credential 由 owner 依 `docs/runbooks/auth0.md` 建立；agent 沒有建立帳號、沒有任何 credential，也沒有連過 Auth0。
- **Auth0 的 rotation reuse interval（owner 已決定，H-010：30 秒）**：runbook 一律設 30 秒（round 2 I-3）。代價是：refresh token 被 rotation 後 30 秒內，被竊的舊 token 仍能換到新 token 而不觸發 reuse detection；好處是被取消的 request 或遺失的回應不會讓使用者被登出。設 0 也能運作，只是上述情況會結束 session。
- 新依賴：openid-client 6.8.8（MIT），帶入 oauth4webapi 3.8.8 與 jose 6.2.12（MIT），三者皆於 2026-09-05 發布；`npm audit --omit=dev` 為 0。

## 與規劃的差異

- tasks.json 列了 `internal/auth/authtest/` 作為測試 provider 的位置；改為兩個 TypeScript 的測試 provider：單元測試用的 in-process `apps/web/tests/fake-oidc.ts` 與瀏覽器測試用的 `apps/web-e2e/fake/servers.mjs`。Go 的 `authtest` 是給 Go API 測試的 JWT 簽發器，E2E 這次不需要 Go API（API 以替身代替；完整的瀏覽器→BFF→Go API→DB 閉環是 T-040）。
- 「未知 identity 顯示 403」：BFF 原樣轉送 API 的 403（E2E 以 API 替身驗證）；UI 顯示要等 D-045 的 bootstrap。
- 實作先於測試寫成的部分以 mutation 佐證（見上）。

## NOT RUN／延後

| 項目 | 原因／歸屬 |
|---|---|
| 連 Auth0 的檢查 | 沒有 tenant 與 credential；`docs/runbooks/auth0.md` §5 在 staging 由人執行 |
| 登入 UI、organization 選擇、UI 中的 403 | 2026-10-26 已實作（「後續：登入後的 UI bootstrap」，待獨立 review）；未登入的專案頁的 8 個截圖 baseline 由 owner 在 CI 產生並確認 |
| `make e2e-test`（D-045 slice） | NOT RUN：這一片沒有改 UI；E2E 的 API 替身尚無 `/v1/me` |
| 瀏覽器→BFF→Go API→DB 的閉環 | T-040 |
| 多個 web instance 之間的 refresh 協調 | P0 單一 instance；多 instance 時以 Auth0 的 reuse interval 30 秒涵蓋（runbook） |
| Login、callback、refresh 的 rate limiting 與告警 | T-036／T-035（ADR-018 §3）。BFF 不轉送 client IP（不送 X-Forwarded-For），API 端的 rate limiting 若要以 IP 區分，T-036 需決定由 BFF 轉送可信的 client IP |
| SSE 經過 proxy 的長時間連線 | proxy 沒有自己的 timeout，undici 預設 300 秒；T-023 的 run events 需要比這更頻繁的 heartbeat |
| Auth0 登出是否要求確認（BFF 不帶 `id_token_hint`） | staging，`docs/runbooks/auth0.md` §5 第 6 點 |
| Auth0 對錯誤 client secret 與失敗的 Action 實際回的 status 與錯誤碼 | staging，`docs/runbooks/auth0.md` §5 第 5 點（round 3 I-6）；未確認前，`invalid_client` 保留 session 是依 RFC 6749 的推論 |

## 後續：T-045 review 第 1 輪 I2（2026-10-26）

T-045 的獨立 review（H-003，`checks/evidence/2026-10-26-T-045-r1/reviewer/REVIEW.md` I2）在 T-043 的登入錯誤頁找到一個缺陷，在 T-045 的修正分支 `t045-r1` 修正；細節與 log 見 `checks/T-045_VALIDATION.md`「Review 第 1 輪」。

- **發現**：`apps/web/app/auth/error/page.tsx`（74e327a）以 `reason in reasons` 查表；`in` 也找得到 `Object.prototype` 的名稱，所以 `/auth/error?reason=toString`（以及 `constructor`、`__proto__`、`hasOwnProperty`、`valueOf`）把函式交給 `t()`，丟出 "needs a numeric count"，伺服器回 500（reviewer 的 `cookie-probe.log` 與 probe P11）。只顯示固定的文字，沒有資料外洩；但任何人都能讓這一頁回 500，也不符「其他理由一律為 failed」的設計。
- **修正**：改為 `Object.hasOwn(reasons, reason)`（只查自己的 key）。
- **先紅後綠**：`apps/web/tests/layout.test.tsx`「says a sign-in failed for a reason it does not know, even one named like an object's own property」：`toString`、`constructor`、`__proto__`、`hasOwnProperty`、`valueOf`、`isPrototypeOf`、`propertyIsEnumerable`、`toLocaleString`、不認得的字、空字串、沒有 reason、兩個 reason，都必須顯示一般的失敗文字與「Sign in again」連結；修正前失敗（`checks/evidence/2026-10-26-T-045-r1/red-i2-i3.log`），修正後通過。瀏覽器：`apps/web-e2e/tests/session.spec.ts`「a sign-in error page with an unknown reason says the sign-in failed, never a 500」，修正前 `toString` 得到 500（`red-e2e-run1.log`），修正後 200（`e2e-test.log`）。
- BFF 只以 `expired`、`refused`、`failed` 三個 reason 轉址到這一頁（`lib/session/bff.ts`；`tests/bff.test.ts` 檢查），所以這只在有人自己輸入或傳遞網址時發生。

## 後續：登入後的 UI bootstrap（hackathon，H-029）

2026-10-26，agent 實作，branch `t043-bootstrap`（parent 74cb9ae，T-015 切片整合後）。Commits：0ae7952（既有失敗的修正，見下）、512eb32（先寫的測試，紅）、f76631c（實作）、之後一個 commit 為文件、本節與 evidence。待獨立 review（H-029：比賽期間每個切片一輪）。

這一片補上 T-043 一直缺的部分：BFF 能登入、轉送與登出，`GET /v1/me`（D-045）也已實作，但沒有元件知道 session 存在，所以真實部署中每個資料頁一律顯示「需要登入」，而且那個狀態沒有登入的動作（tasks.json T-043 scope 的後續、impeccable audit 2026-10-19 的 P0）。Demo 的流程（H-029）需要：登入 → 選組織（只有一個時自動）→ 專案 → 模型編輯器 → proposal → 執行與結果。

### 做了什麼

| 位置 | 內容 |
|---|---|
| `apps/web/app/layout.tsx` | root layout 以 `AuthBootstrap` 包住 strip 與頁面。伺服器只做兩件事：session cookie 存在時問 BFF 它是否打得開且未到期（`hasSession`），以及把 `geminus_organization` cookie 讀成 organization ID（不是 ID 就是 null）。交給 client component 的只有這個布林值與 ID：cookie 的值、token、封裝的內容都不進 HTML（`tests/layout-session.test.tsx`） |
| `apps/web/lib/session/bff.ts`、`server.ts` 的 `hasSession` | 解封並檢查閒置／絕對期限與時鐘容差（沿用 `readSession`）；**不** refresh、不呼叫 provider 或 API。refresh 後輪替的 token 只能放進回應的 Set-Cookie，而 server component 不能設 cookie，所以 refresh 仍只在 proxy 中進行。BFF 設定不完整時為 false |
| `apps/web/app/_components/auth-bootstrap.tsx` | 有 session 時經 BFF 呼叫 `getMe()`（不帶 `X-Organization-ID`，D-045）；沒有 session 時什麼都不問，第一個 paint 就是未登入。結果經既有的 `lib/auth/session.tsx` context 提供：`session`（選定的 organization）、`role`，加上 `account`（bootstrap 的狀態與 `choose`／`retry`／`signOut`）。沒有第二個 state model（ADR-016）：頁面、`useApi()` 與編輯器的唯讀行為照舊讀 `session` 與 `role`。每個 organization 一個 session 物件，切換時頁面以新的 client 重新載入 |
| `apps/web/lib/auth/bootstrap.ts` | 純函式 `bootstrapFrom`：`/me` 載入中 → loading；401（沒有 session，或 refresh 被拒而 session 結束）→ signed_out；403（Geminus 沒有這個 identity 的 user，D-008：不在此建立）→ no_access，保留 API 的 problem 與 request ID；其他失敗（provider 或 API 不可用、網路、429、500）→ failed，可重試；不符 contract 的回答（memberships 不是陣列、organization ID 不合 D-001、名稱不是字串、同一 organization 出現兩次、缺 `truncated`）→ protocol 失敗，不猜；0 個 membership → no_access；1 個 → 自動選；多個 → 記住的那個仍在清單中才用，否則請使用者選。Role：owner／editor／viewer 照 API；不認得的 role 為 null（唯讀，`canEdit` 為 false） |
| `apps/web/lib/auth/organization.ts` | `geminus_organization`：只接受 organization ID；browser session 的 cookie（沒有 Max-Age）、`Path=/; SameSite=Lax`、https 時加 `Secure`；登出時清除。它只是 selector（D-008）：API 每次以 token 的 membership 授權 |
| `apps/web/lib/return-to.ts` | `safeReturnTo` 從 `lib/session/session.ts` 移到這裡（session.ts re-export 同一個函式，BFF 與頁面的規則因此相同；`bootstrap.test.ts` 檢查是同一個函式）；`signInHref(目前的 path 與 query)` → `/auth/login?return_to=…`，BFF 不會回去的路徑一律為 `/` |
| `apps/web/lib/auth/sign-out.ts` | `POST /auth/logout`（`X-Geminus-Request: 1`、`credentials: same-origin`、`redirect: error`、`no-store`）；成功時清掉 organization cookie，前往 BFF 回答的 provider logout URL（只接受 http／https，其他一律回首頁）；失敗時回 problem，不吞錯 |
| `apps/web/app/_components/states.tsx` | `RequireSession` 依 bootstrap 顯示：未登入 →「需要登入」加上**一個**「登入」連結（`SignInLink`，return_to 為目前的頁面）；載入中；`/me` 失敗 → problem 與重試；無權限 → 既有的權限狀態（`ProblemView` forbidden），說明改為「這個登入帳號沒有任何 Geminus 組織的存取權…」；多個組織 → 選擇（每個組織一個按鈕、旁邊寫 role、同名時加上 ID、超過 100 個時說明只列前 100）。`RemoteView` 遇到 401（頁面載入時 session 已結束）也提供登入連結 |
| `apps/web/app/_components/account.tsx` | strip 的 `OrganizationSwitcher`（已選組織且有兩個以上時；strip 的 Select，長名稱以省略號截斷，清單與 accessible name 保留全名）與 `SignOutButton`（quiet button，有 session 時才出現：已選組織、選擇中、無權限、`/me` 失敗；失敗時旁邊顯示 `role="alert"` 的「登出未完成，請再試一次。」） |
| `apps/web/app/_components/problem-view.tsx` | `guidance` prop：view 比 problem 的 code 更清楚時（無權限）取代預設的說明 |
| `apps/web/ui/shell.module.css`、`select.tsx`、`select.module.css` | strip 的 `.account`：沒有 session 時是空的、不佔空間；手機（≤720 px）時自成一列，第一列與未登入時完全相同。`.nav` 佔去列中的空白（空的 box，沒有東西移動），讓換到下一列的 account 仍靠右。Select 的 `clipValue` |
| `apps/web/messages/en.ts`、`zh-Hant.ts` | 新字串：`auth.signIn`（登入）、`auth.signOut`（登出）、`auth.signOutFailed`、`access.none`、`organization.*`（label、選擇的標題與說明、同名時的「{name}（{id}）」、超過 100 個）、`role.*`、`loading.account` |
| 文件 | DESIGN.md（Simplicity 2、3 與「The account」）、ADR-016 與 ADR-018 補記、SECURITY §3、`apps/web-e2e/README.md` |

**Organization 怎麼選、怎麼送：** API 以 header `X-Organization-ID` 選 organization（openapi `components.parameters.OrganizationContext`、`internal/httpapi.Scoped`；`GET /me` 不得帶，`NoOrganizationContext`、`httpapi.Self`）。`lib/api/client.ts` 本來就從 `session.organizationId` 送這個 header，`getMe()` 本來就不送，BFF 原樣轉送。這一片只決定 `session.organizationId` 是哪一個：只有一個 membership 時就是它；多個時，是使用者選的、或之前選過且 `/me` 仍列出的那個（cookie `geminus_organization`，browser session）。沒有新的機制，也沒有改 API、contract、schema 或 BFF 的 CSRF 規則。

**T-032 的結果頁：** 尚未存在。它會放在 `RequireSession` 之下並經 `useApi()` 呼叫（`compareSimulations` 等），organization 與 role 自動跟著 bootstrap，不需要再接。

### 控制項（H-027）

| 控制項 | 何時出現 | 做什麼 | 依據 |
|---|---|---|---|
| 「登入」連結 | 未登入或 session 已結束（401）的「需要登入」狀態；每個 view 一個 | `/auth/login?return_to=目前的頁面` | T-043 scope 的後續（impeccable audit P0；H-027 的必要控制項）；ADR-018 |
| 「登出」（quiet button，strip） | 有 session：已選組織、選擇組織中、無權限、`/me` 失敗；未登入與載入中不出現 | `POST /auth/logout` → provider 的 logout → 首頁 | ADR-018 Logout |
| 組織選擇（每個組織一個按鈕） | 兩個以上的組織且還沒選（或記住的已不在清單中）；此時 strip 沒有切換器 | 選定組織，記到 browser session 結束 | D-045 |
| 組織切換（strip 的 Select） | 已選組織且有兩個以上 | 換組織；頁面在同一個 route 以新組織重新載入（不轉頁，WCAG 3.2.2） | D-045 |
| 重試 | `/me` 以可重試的原因失敗（503、網路、429、500、protocol） | 再呼叫一次 `/me` | 既有的 `RemoteView` |

沒有加入的：strip 上的登入按鈕（登入只在需要它的頁面，未登入的 strip 不變）、帳號名稱或頭像（`/me` 沒有名稱）、組織選擇的「略過」或「記住我」、只有一個組織時的切換器。

### 與指定設計的差異與自行做的選擇

- **手機的 strip 在登入後是兩列。** 390 px 時 strip 的一列只剩 8–14 px（實測），任何 account 控制項都放不下；DESIGN.md「the strip stays one 48 px row on a 390 px phone」是對語言與主題而言。做法：登入後 account 自成第二列，第一列與未登入時完全相同（元素從 8 px 開始，第二列從 48 px 開始，共 89 px）；1280 px 仍是一列 48 px。英文在 721 到約 800 px 之間 account 也會換到第二列（仍靠右），此時第一列比未登入時高 4 px。DESIGN.md 已補記；**是否接受、或改成一個帳號選單，由 owner 決定**（見下）。
- **組織的選擇只記到 browser session 結束**（語言與主題記一年）。任務說「remembered per session/cookie」；這樣共用的瀏覽器下次開啟時不會預設另一個人的組織（就算預設了，`/me` 沒列出也不會用）。
- **切換組織不轉頁**：留在同一個 route，所以在另一個組織的模型頁上切換會看到「找不到」，要從「專案」再進入。轉到 `/projects` 會是 select 的值改變造成的 context change（WCAG 3.2.2）。模型頁上未儲存的編輯在切換時會消失（沒有確認）。
- **無權限的說明**：403 與 0 個 membership 都是既有的權限狀態（heading「你沒有權限執行此操作」），但說明改為 `access.none`，因為預設的「你在此組織中的角色不允許此操作。」對沒有組織的人不正確；403 的 request ID 仍在「詳細資料」中。
- **既有失敗的修正（0ae7952）**：74cb9ae 上 `make frontend-test` 已有 2 個失敗（`simplicity.test.tsx` 的模型頁，en 與 zh-Hant；`baseline-frontend-test-74cb9ae.log`）。T-015 把編輯器放上模型頁後，Save draft 與 Publish 在不能用時是 `aria-disabled` 並在旁邊寫原因（DESIGN.md Buttons，Disabled），這個 unit test 卻仍要求頁面上沒有任何 `aria-disabled`。改為 `simplicity.spec.ts` 對同一個 view 已在用的規則：沒有 `disabled`、editor 至少有一個 blocked 的控制項被檢查、每個 blocked 的控制項以 `aria-describedby` 指向畫面上的文字。拿掉 Save 的 `aria-describedby` 的 probe 會失敗。**改的是別的切片的測試，請 coordinator 確認。**
- `session.spec.ts` 的 `signIn` 改為等到頁面自己的呼叫（`/me` 與專案清單）結束後才回傳：現在登入後頁面會呼叫 API，計算 API 呼叫數的測試否則會不穩。Assertion 沒有改。

### 測試

先寫的測試在實作前執行為紅。Unit：`red-unit.log`，6 個檔案失敗（新模組不存在、沒有登入連結）。瀏覽器：`red-e2e-before-bootstrap.log`，新的 `bootstrap.spec.ts` 在**改動前的 app**（0ae7952 的 `apps/web`，另外 build）上 10 個中 9 個失敗；「return path 指向其他網站時回首頁」本來就成立（BFF 既有的檢查）。`session.spec.ts` 在改動前的 app 與新的 API 替身上 10 個全過，表示替身的擴充與既有測試相容。

- **Unit（vitest）**：`tests/bootstrap.test.ts`（`bootstrapFrom` 的每個狀態、role 對應、contract 不符、同名、cookie 的格式與屬性、return path 的 10 個例子且與 BFF 用同一個函式、sign-out 的 request、目的地與失敗）；`tests/auth-bootstrap.test.tsx`（happy-dom，瀏覽器的 fetch 換成假的 BFF：未登入不發任何 request 且只有一個登入連結；return path 含 query；zh-Hant；伺服器的第一個 paint 未登入時就是未登入、有 session 時是載入中；載入中不呼叫 tenant API；`/me` 不帶 organization 與 token；401；503 與重試；403；0 個 membership；一個組織自動並以 `X-Organization-ID` 送出；四種 role；多個組織的選擇與 cookie、記住的、不在清單中的、從 strip 切換、同名、超過 100；登出與登出失敗）；`tests/layout-session.test.tsx`（layout 只在有 cookie 時問 BFF；交出的 props 只有 `signedIn` 與 organization ID；HTML 不含 cookie 的值）；`tests/bff.test.ts` 的 `hasSession`（access token 過期仍為 true，且不 refresh、不呼叫 provider 或 API；沒有、竄改、login cookie 的值、閒置到期為 false）；`tests/ui.test.tsx`（`RequireSession` 一個登入連結；只有 401 的 `RemoteView` 有登入連結）；`tests/simplicity.test.tsx`（登入後的 strip 加上專案清單；組織選擇，含同名；無權限（403、0 個）；`/me` 失敗；未登入；載入中：沒有重複、最多一個 primary、沒有 disabled，登入只在未登入時、登出只在有 session 時）；`tests/i18n-dom.test.tsx`（zh-Hant 的登入連結）。
- **瀏覽器（Playwright；測試用 provider 與 API 替身，絕不連 Auth0）**：`tests/bootstrap.spec.ts` 10 個：從未登入的專案頁登入 → 只有一個組織自動 → 專案 → 專案的模型 → 模型在 2D 編輯器中（步驟、草稿狀態、Add step 與 Save draft），API 替身看到的 `/v1/me` 沒有 organization、其他每個呼叫都是該組織且帶 BFF 的 token、沒有 cookie，HTML、`document.cookie`、localStorage、sessionStorage 都沒有 token；viewer 的模型唯讀；登入後回到原本的頁面；`//evil.test/x`、`https://evil.test/`、`/\evil.test`、`javascript:` 都回本站首頁；多個組織（先選、cookie 是 session cookie、reload 後記得、strip 切換後呼叫帶新的組織）；登出（provider 的 logout、refresh token 撤銷、兩個 cookie 都沒了、再看專案頁是登入）；provider 結束 session（refresh token 全部撤銷、access token 30 秒）後 reload → BFF refresh 被拒 → 登入、cookie 已清 → 再登入後資料回來；沒有 membership、未知 identity（403）：權限狀態、可登出、只呼叫了 `/v1/me`；鍵盤：登入、選組織、登出，每一站焦點可見。`a11y.spec.ts`：7 個登入後的 view（專案、專案的模型、模型（editor、viewer）、選組織、有切換器的專案、無權限）× 淺／深 × en／zh-Hant × 390／1280，共 56 個：axe 無 violation、無橫向捲動。`simplicity.spec.ts`：同樣 7 個 view × 2 種語言 × 2 種寬度，共 28 個（最多一個 primary、沒有重複、模型頁的 blocked 都有原因、其他沒有 disabled、沒有側欄；strip 在 1280 px 為一列 48 px，在 390 px 為兩列且第一列與未登入時相同）；未登入的 route 另外檢查 strip 只有一列。`strings.spec.ts`：登入後的 view 在兩種語言中的字串都是 catalog、資料或已接受的 library 字串。
- **Mutation（`ts-mutants.py`：每個 mutant 只改一個檔案，執行相關的 vitest 檔案後還原）**：27 個全部 killed（`ts-mutation.log`）。第一次執行有 1 個存活：「未登入也等 `/me`」（effect 執行後結果相同，只有第一個 paint 不同）；補上伺服器第一個 paint 的測試後 killed。

### 實際執行結果

在這個 worktree 執行（Node 22.22.0，repo 要求 24，所以 `npm ci --engine-strict=false`；Chromium 為預先安裝的）。`e2e-test` 在 f76631c 執行；`frontend-test`、`frontend-build` 與 `spec-check` 在 f76631c 加上文件的變更（即下一個 commit 的內容，沒有程式變更）上執行。Log 在 `checks/evidence/2026-10-26-T-043-bootstrap/`。

| 指令 | Exit | 說明 |
|---|---|---|
| `make frontend-test`（74cb9ae，改動前） | 2 | 2 個既有失敗（見上），`baseline-frontend-test-74cb9ae.log` |
| `make frontend-test` | 0 | lint（0 warning）、typecheck、unit：29 個檔案 737 個 test（74cb9ae 為 26 個檔案 642 個，其中 2 個失敗）；`frontend-test.log` |
| `make frontend-build`（全新的 `.next`） | 0 | `frontend-build.log` |
| `make e2e-test`（全新的 `.next`） | 0 | 290 個 test：258 passed、32 skipped（截圖，NOT RUN，見下）；新增 bootstrap 10、登入後的 a11y 56、simplicity 28、strings 2；`e2e-test.log`（log 中的「failed to pipe response」是 `session.spec.ts` 刻意在 header 之後切斷 API 回答的測試） |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities（`npm-audit.log`） |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 所有子檢查 PASS（`spec-check.log`；含 tasks.json 的新條目與文件連結） |
| `ts-mutants.py` | 0 | 27／27 killed（`ts-mutation.log`） |

### NOT RUN

| 項目 | 原因 |
|---|---|
| 截圖比對（`screenshots.spec.ts`，32 個） | 只在 CI 的 pinned container 以 `E2E_SCREENSHOTS=1` 執行。**未登入的 `/projects` 多了「登入」連結，8 個已接受的 baseline 會改變**：`projects-en-390`、`projects-en-390-dark`、`projects-en-1280`、`projects-en-1280-dark`、`projects-zh-Hant-390`、`projects-zh-Hant-390-dark`、`projects-zh-Hant-1280`、`projects-zh-Hant-1280-dark`（H-028）。首頁、not found、gallery 的畫面不變：未登入的 strip 不變，`.nav` 的 `flex-grow` 沒有移動任何東西（未登入時 strip 各部分的位置在 390、721、1280 px 以暫時的量測腳本比較，加上 `flex-grow` 前後相同；`simplicity.spec.ts` 與 `keyboard.spec.ts` 通過）。Agent 沒有動 `apps/web-e2e/tests/__screenshots__/`；由 coordinator 以 CI 的 `update_screenshots` 產生，owner 確認 |
| Go 測試（`backend-test`、`backend-race`、`integration-test`） | 這一片沒有改 Go、contract、schema 或 migration |
| 真的 Auth0 | 沒有 tenant 與 credential；`docs/runbooks/auth0.md` §5 在 staging 由人執行 |
| 瀏覽器 → BFF → Go API → DB 的閉環 | T-040；這裡的 API 是替身（`fake/servers.mjs`），`/v1/me` 的真實行為由 D-045 slice 的 Go 與 SQL 測試涵蓋 |
| proposal、執行與結果的畫面 | T-031、T-032 的切片 |

### 風險與後續

- 切換組織時，模型頁上未儲存的編輯會消失，沒有確認（低；demo 通常只有一個組織）。
- 頁面停留期間 session 結束：頁面的下一次載入顯示「需要登入」與登入連結，但 strip 的「登出」要到 reload 才消失；草稿儲存時的 401 只顯示 problem，沒有登入連結（離開頁面本來就會失去未存的編輯）。
- 手機上登入後，strip 在 `/me` 回來時才多出第二列（版面位移一次）。
- 超過 100 個組織時只能選前 100 個（D-045 的上限；畫面有說明）。
- `geminus_organization` 是 JS 讀得到的 cookie：只有 organization ID，不是 credential。

### Owner 的問題

1. 8 個 `projects-*` 截圖 baseline 會因「登入」連結改變：請在 CI 產生並確認（H-028）。
2. 手機登入後 strip 為兩列（account 在第二列）：接受，或改成一個帳號選單（語言、主題、組織、登出放在一起）？
3. 組織的選擇只記到瀏覽器關閉：可以嗎，還是像語言一樣記一年？
4. 切換組織時留在同一頁（可能顯示「找不到」）：可以嗎，還是改為回到「專案」（並先告知）？

### Coordinator 驗證（登入後的 UI bootstrap，2026-10-26）

`t043-bootstrap` 的四個 commit（0ae7952、512eb32、f76631c、9d0ed9e）cherry-pick 到 ca2c6b1 之後（e0a9901、986b451、e7aec84、96b6e42）。衝突：
- `apps/web/tests/simplicity.test.tsx`：coordinator 在 55d0703 已把規則改為「沒有原因的 disabled 控制項為 0」；本切片的 0ae7952 更嚴格（任何頁面不得有原生 `disabled`；模型頁的每個 `aria-disabled` 必須以 `aria-describedby` 指向看得見的非空文字、編輯者必須至少有一個 blocked 動作可檢查；其他已登入頁面不得有 blocked 控制項），採用本切片的版本。
- `apps/web/app/_components/states.tsx`：`Loadable` 合併兩邊（T-032 的 scenario、runs、results 與本切片的 account）；catalog 兩邊的 key 都在。
- `DESIGN.md` Simplicity 第 2 條：採用本切片的文字，並把 unit 測試的描述改為上面的實際規則；第 3 條採用本切片的文字。
- 生成的 `checks/*.json`：重產。

指令與結果（evidence：`checks/evidence/2026-10-26-T-043-bootstrap/coordinator/`；HEAD 96b6e42；Node 22.22.0）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `npm ci`（web、web-e2e） | 0／0 | |
| `make frontend-test` | 0 | 865／865 |
| `make frontend-build` | 0 | |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| `make e2e-test`（等 3100／3101 空出後單獨執行） | 0 | 353 passed、32 skipped（全部是只在 CI 比對的截圖） |

沒有改 Go、contract 或 migration，所以沒有跑 Go 測試。

截圖：未登入的 `/projects` 多了「Sign in」連結，8 張已接受的 baseline（`projects-{en,zh-Hant}-{390,1280}` 與 `-dark`）會改變。push 後 CI 的截圖比對在這 8 張會失敗，直到 owner 接受新的 baseline（H-028：以 `update_screenshots` 在 CI 產生、owner 確認後 commit）。

Coordinator 對本切片問題的處理：
1. 8 張 baseline：交給 owner 確認（見上）。
2. 手機上的第二列（Sign out 與組織切換）：比賽期間維持。
3. 組織的選擇只記到瀏覽器關閉：維持（較安全的預設）。
4. 切換組織時留在原頁、未存的編輯不詢問就消失：demo 只有一個組織，比賽期間維持；比賽後改為回到 Projects，有未存的編輯時先詢問。

下一步：一輪獨立審查（H-029）。

