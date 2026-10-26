# T-057 web 切片驗證報告：AI 請求欄位的資料告知與 web 的 security header

- 日期：2026-10-26；分支 `t057-web`（本機，從 `1425e3a`；未 push、未開 PR）；agent 的自我檢查，**不是驗收**（H-003 的獨立 review 尚未進行）。
- 依據：H-035 (1)（請求欄位加一句「內容會送到 AI，只放 synthetic 資料」）、T-057 scope（公開 demo）、SECURITY §3（原本寫「CSP 與其他 security header 尚未設定」）、ADR-018（BFF）、DESIGN.md（簡潔、en＋zh-Hant、tokens）。
- 範圍只有 web：`apps/web`、`apps/web-e2e` 與文件。沒有改 `internal/`、`cmd/`、`deploy/`、`scripts/check_deploy.py`，沒有新增或升級任何 dependency（`package.json`、lockfile 都沒變），沒有動 `__screenshots__`。
- `planning/tasks.json` 的 T-057 維持 `not_started`（部署、demo 帳號、影片等其餘 scope 不在本切片），由 coordinator 決定要不要記入 `completion_evidence`。

## 1. 結論

1. **資料告知（H-035 (1)）**：model 頁「請 AI 提出修改」的文字欄位下方、在原本的說明之後，多一行 12 px Secondary Graphite 的說明：en「What you type or upload is sent to an AI service. Use synthetic data only.」，zh-Hant「內容會送到 AI，只放 synthetic 資料。」（owner 的原文；英文只照原意寫成完整句子）。字串在兩個 message catalog（key `ask.dataNotice`）；文字欄位（textarea）與照片按鈕都以 `aria-describedby` 指向它，所以螢幕閱讀器在兩處都會讀出。沒有新的控制項、顏色或 icon（H-027）。
2. **Security header**：`apps/web/proxy.ts`（Next.js 16 的 proxy，原 middleware；沒有 matcher）對**每個**回應（頁面、BFF 的 `/auth/*` 與 `/api/v1/*`、錯誤頁、靜態檔）加上 `apps/web/lib/security/headers.ts` 產生的 header：每個 request 一個 nonce 的 CSP、`X-Content-Type-Options`、`Referrer-Policy`、`Permissions-Policy`、`X-Frame-Options`，以及只在 app origin 為 https 時的 HSTS；`X-Powered-By` 關閉。整個既有的 e2e suite 在 policy 開啟下通過，而且**每個 e2e test 都監看 CSP violation**（§3.2）。
3. **Build 的唯一放寬**：`style-src-attr 'unsafe-inline'`（style attribute），理由見 §2.3；script 沒有任何放寬。`next dev` 另外允許 eval 與 inline `<style>`（§2.3），build 沒有。

## 2. 設計

### 2.1 資料告知

- `ui/field.tsx` 的 `TextField` 多一個選填的 `notice: { id, text }`：在 description 之後、錯誤訊息之前多一行（與 description 相同樣式），並把它的 ID 加在欄位 `aria-describedby` 的最後（React Aria 會把 description、error、傳入的 `aria-describedby` 合併）。ID 由呼叫者提供，所以其他控制項也能指向同一行。
- `features/ai-proposals/ask-form.tsx` 以 `useId()` 產生 `noticeId`，傳給欄位與 `PhotoForm`；`photo-form.tsx` 的按鈕 `aria-describedby` 為「（blocked 時的理由）、照片說明、資料告知」，`noticeId` 為必填，所以照片按鈕不會沒有這一行。
- 只出現在能請 AI 的地方：editor／owner 的 model 頁（viewer、role 未知時沒有 ask form，既有測試）。照片按鈕只在 pack 讀照片的 domain；文字欄位的告知不論 domain 都在。
- Screenshot baseline：告知只在 model 頁與 `/ui-gallery/editor`，都不在 `screenshots.spec.ts` 的四個頁面（`/`、`/projects`、`/no-such-page`、`/ui-gallery`），所以 baseline 不受影響。

### 2.2 Header 怎麼加上

```
request ──> proxy.ts
  nonce = 16 random bytes (CSPRNG), base64
  options = { nonce, https: servedOverHttps(BFF_APP_ORIGIN), development: NODE_ENV === "development" }
  request headers  : content-security-policy = policy, x-nonce = nonce   (client 送來的同名 header 被覆寫)
  response headers : securityHeaders(options)
       │
       ├─ Next.js app render：從 request 的 CSP 取 nonce → 加到每個 <script>、stylesheet／preload <link>
       ├─ app/layout.tsx：讀 x-nonce（只接受 nonce 的形狀）→ <meta name="csp-nonce" nonce> → React Aria 加 <style> 時讀它
       └─ route handlers（BFF）：回應保留 proxy 的 header（實測 §6）
```

- 所有頁面在 build 時都是 dynamic（`ƒ`，root layout 讀 cookie），所以每頁都有自己的 nonce；`/icon.svg` 是靜態檔（`○`），不是 HTML。
- HSTS 與 `upgrade-insecure-requests` 由部署設定的 `BFF_APP_ORIGIN` 決定（Railway 上是 `https://${{RAILWAY_PUBLIC_DOMAIN}}`），在 runtime 讀；不看 request 的 scheme 或 `X-Forwarded-Proto`（client 可控制，在平台的 TLS edge 後面也是 http）。實測：同一個 build，`BFF_APP_ORIGIN=https://…` 的 server 送 HSTS，`http://127.0.0.1` 的 server 即使收到 `X-Forwarded-Proto: https` 也不送（`checks/evidence/2026-10-26-T-057-web/hsts-runtime.txt`）。
- 實測發現的兩件事：(a) React Aria 1.21 的 `usePress` 在每個有按鈕的頁面加一個 `<style id="react-aria-pressable-style">`，nonce 從 `<meta name="csp-nonce">` 或 `__webpack_nonce__` 讀；沒有 meta 時會被 `style-src-elem` 擋掉（所以 layout 放 meta）。(b) Chromium 不認得 `Permissions-Policy` 的 `bluetooth`，每頁都會出現 console 警告，所以從清單移除。

### 2.3 完整 policy 與理由

Production、http（本機、e2e）：

```
Content-Security-Policy: default-src 'none'; script-src 'self' 'nonce-<n>' 'strict-dynamic'; style-src 'self' 'unsafe-inline'; style-src-elem 'self' 'nonce-<n>'; style-src-attr 'unsafe-inline'; img-src 'self'; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
Permissions-Policy: camera=(), microphone=(), geolocation=(), display-capture=(), screen-wake-lock=(), fullscreen=(), picture-in-picture=(), autoplay=(), encrypted-media=(), midi=(), usb=(), serial=(), hid=(), payment=(), publickey-credentials-get=(), accelerometer=(), gyroscope=(), magnetometer=(), idle-detection=(), local-fonts=(), browsing-topics=(), xr-spatial-tracking=()
X-Frame-Options: DENY
```

Production、https（部署）：CSP 最後再加 `; upgrade-insecure-requests`，並多 `Strict-Transport-Security: max-age=31536000`。`next dev`：`script-src` 多 `'unsafe-eval'`（Next.js 文件：React 在 development 以 eval 重建 server 的錯誤 stack；production 不用），`style-src-elem` 為 `'self' 'unsafe-inline'`（實測：dev overlay（`next-devtools`）加入沒有 nonce 的 `<style>`，第一版的 dev policy 每頁都有 violation；改後 6 個頁面沒有 violation、沒有 console error，`next-dev-probe.txt`）；build 出來的 app 兩者都沒有（unit test 固定）。沒有 `X-Powered-By`。

| Directive／header | 值 | 理由 |
|---|---|---|
| `default-src` | `'none'` | 沒列的類型（frame、worker、media、manifest、object）一律不允許。 |
| `script-src` | `'self' 'nonce-<n>' 'strict-dynamic'` | Next.js 的每個 script 都帶 nonce；由它們載入的 chunk 由 `'strict-dynamic'` 信任；`'self'` 只給不認得 `'strict-dynamic'` 的舊瀏覽器。沒有 `'unsafe-inline'`、`'unsafe-eval'`、`'unsafe-hashes'`：inline event handler 與 eval 都被擋（e2e）。Next.js 從第一個以 `script-src` 開頭的 directive 取 nonce，所以它排在 style 之前、且沒有 `script-src-elem`。 |
| `style-src-elem` | `'self' 'nonce-<n>'` | Next.js 的 stylesheet `<link>` 帶 nonce 且同源；React Aria 加的 `<style>` 從 csp-nonce meta 取 nonce。沒有 nonce 的 `<style>` 被擋（e2e）。 |
| `style-src-attr` | `'unsafe-inline'` | **唯一放寬**。伺服器 render 的 HTML 有 style attribute，而 nonce 不能套用在 attribute：實測 12 個頁面中有 React Aria VisuallyHidden（Select 隱藏的原生 select，兩種固定值，32 處）、React Aria Table 的 `--table-row-level:1`（5 處）、虛擬化表格尚未量測時的 `height:480px`（`ui/table.tsx`，值是動態的）。`'unsafe-hashes'` 加 hash 對動態值不可行。擋掉的話這些隱藏元素會顯示（hydration 不補回 attribute）；browser mutant E2b（`style-src-attr 'none'`）在每個頁面都報 `style-src-attr` 的 violation（§5）。風險：要先能注入 HTML attribute 才用得上（React escape、lint 禁止 `dangerouslySetInnerHTML`），而 `img-src`、`font-src` 只有 `'self'`，CSS 無法對外載入或外洩資料。 |
| `style-src` | `'self' 'unsafe-inline'` | 只給不認得 `-elem`／`-attr` 的瀏覽器（Safari 15.4、Firefox 108 以前）：在那裡放 nonce 會讓 `'unsafe-inline'` 對 attribute 也失效。認得的瀏覽器不讀它，所以現代瀏覽器的 `<style>` 仍須 nonce。 |
| `img-src`／`font-src`／`connect-src` | `'self'` | 圖示同源；字型由 `@fontsource-variable` 自己提供（build 到 `/_next/static/media`，theme spec 檢查）；瀏覽器只呼叫同源的 BFF（ADR-018）。Token、revocation、JWKS 都是伺服器端呼叫；到 provider 是 top-level 轉址，不受 `connect-src` 限制。 |
| `object-src` | `'none'` | 沒有 plugin。 |
| `base-uri` | `'none'` | 不用 `<base>`；注入的 `<base>` 被擋（e2e）。 |
| `form-action` | `'self'` | 登入是連結 `/auth/login`，BFF 回 303 到 provider 的 authorize（navigation，不是表單）；provider 以 GET 302 回 `/auth/callback`（openid-client 預設 `response_mode=query`，`lib/session/oidc.ts` 沒有 form_post）；provider 頁面上的帳密表單受 provider 自己的 CSP 管；登出是 `fetch` 的 POST，再 `location.assign` 到 provider 的 logout。App 內唯一的 `<form>`（模擬設定）以 JS 送出。送到其他 origin 的表單被擋（e2e），登入仍可用。 |
| `frame-ancestors` | `'none'` | 不能被嵌入（e2e：其他頁面的 iframe 不 render app）。另送 `X-Frame-Options: DENY` 給不讀 CSP 的瀏覽器。 |
| `upgrade-insecure-requests` | 只在 https | http 的本機與 e2e 若加上，同源的子資源會被改成 https 而載入失敗。 |
| `X-Content-Type-Options` | `nosniff` | BFF 原本的錯誤回應已有，現在所有回應都有（不重複，實測只有一行）。 |
| `Referrer-Policy` | `no-referrer` | 比 `strict-origin-when-cross-origin` 嚴。沒有任何地方讀 Referer；BFF 的 CSRF 檢查看 `Origin`，而 `fetch`（cors mode）不論 referrer policy 都送 `Origin`（Fetch 規格只在非 cors 的請求把它換成 `null`）。e2e：登入連結的每一步 navigation（app、provider）都沒有 Referer，登入、登出、提案的寫入都通過，跨站寫入仍是 403。真的 Auth0 未測（NOT RUN）。 |
| `Permissions-Policy` | 22 個 feature 都是 `()` | App 不用任何一個（照片是 file input，不是相機）。只列 Chromium 認得的名稱：未知名稱每頁都是 console 警告，e2e 的監看會失敗。 |
| `Strict-Transport-Security` | `max-age=31536000`，只在 https | 一年；不加 `includeSubDomains`、`preload`（網域由 owner 決定，§9）。`__Host-` cookie 已經不能由子網域或 http 設定。 |

沒有做的：`report-uri`／`report-to`（沒有收集端點）、Trusted Types（`require-trusted-types-for 'script'`，React／Next.js 未驗證）、`Cross-Origin-Opener-Policy`（不在本次要求）。

### 2.4 已知限制

- **global-error**（root layout 本身失敗時取代整份文件）是 client component，讀不到 request header，所以沒有 csp-nonce meta：React Aria 的 `touch-action` `<style>` 會被擋並產生一次 violation；按鈕仍可用（只是觸控裝置少了避免 double-tap zoom 延遲的設定）。沒有測試觸發這個頁面的瀏覽器狀態。
- Railway edge 是否另加或改寫 header 未查證（部署 NOT RUN）。
- 舊瀏覽器（不認得 `'strict-dynamic'` 或 `-elem`／`-attr`）的行為沒有在瀏覽器中測過，只依規格推論。

## 3. 測試

### 3.1 Unit（`make frontend-test`，vitest）

- `tests/security-headers.test.ts`（新，15）：nonce 為 16 bytes base64、2000 次全部不同；`isNonce` 只接受那個形狀（含引號、空白、`;`、少一個 `=`、多字元都拒）；policy 不收不合格的 nonce；production policy 的每個 directive（順序與值完全相同）；production 不論 http／https 都沒有 eval、wildcard、scheme、`data:`、`blob:`，`'unsafe-inline'` 只在 `style-src` 與 `style-src-attr`；`upgrade-insecure-requests` 只在 https；development 只多 `'unsafe-eval'` 與 `style-src-elem 'self' 'unsafe-inline'`，其他 directive 不變；Next.js 的 nonce 解析（照 `get-script-nonce-from-header.js`）找得到 nonce；其他 header 的名稱、順序、值；Permissions-Policy 的形狀（camera、microphone、geolocation 在前）；HSTS 只在 https；`servedOverHttps` 只看設定的 origin；proxy 沒有 matcher（Next.js 的 `unstable_doesMiddlewareMatch` 對頁面、BFF、靜態檔都 match）、回應有全部 header、request 帶同一個 policy 與 nonce、每個 request 不同 nonce、client 送的 nonce 與 policy 被覆寫、HSTS 看 `BFF_APP_ORIGIN` 而不是 request 的 scheme 或 `X-Forwarded-Proto`。
- `tests/layout.test.tsx`（+7）：root layout 把 request 的 nonce 放進 `<head>` 的 csp-nonce meta（en、zh-Hant＋dark 也一樣）；空值、短一個字元、含引號、attribute 注入、多字元時不 render meta、也沒有 `nonce=`。`layout-session`、`simplicity`、`proposal-simplicity`、`results-simplicity` 的 `next/headers` mock 多一個 `headers()`（layout 現在會讀；沒有 nonce）。
- `tests/proposal-ask.test.tsx`（+3）：en 與 zh-Hant 各一：告知只有一行、在欄位與說明之後、在「送給 AI」之前、不在 label 中；欄位的 description 依序為說明、告知；照片按鈕的 description 最後是告知；axe 無 violation。另一個測試固定 owner 的文字。
- `tests/proposal-photo.test.tsx`：既有兩個斷言的預期資料加上告知（照片按鈕的 description 由「說明」變成「說明、告知」；blocked 時「理由、說明、告知」）；standalone 的 `PhotoForm` 多傳 `noticeId`。沒有放寬任何斷言。
- 結果：48 個檔案、1062 個測試通過（開工前 47 個檔案、1037 個）；lint、typecheck 通過。

### 3.2 Browser（`make e2e-test`，Playwright 1.56.1，Chromium 1194）

- `apps/web-e2e/tests/fixtures.ts`（新）：每個 spec 改從這裡取 `test`、`expect`（只改 import 行）。自動 fixture 對 test 的 context：init script 把 `securitypolicyviolation` 轉成 console error；收集 context 中每個 page 的 CSP 與 Permissions-Policy console 訊息；檢查 app 送出的每個 document（頁面、303、錯誤頁、iframe 中的）的 header（`allHeaders()`），並確認沒有 `X-Powered-By`、沒有 HSTS（http）。Test 結束時先與每個 page 來回一次（讓先前的 console 訊息送達），任何 report 或 header 問題都使 test 失敗。只有故意觸發 violation 的 test 以 `csp.allow` 宣告。
- `apps/web-e2e/tests/security-headers.spec.ts`（新，7）：
  1. en、zh-Hant：`helpers.ts` 的每個 route（signed out 與 gallery，24 個）的回應有 header；HTML 中每個 script 與 stylesheet／preload link 都帶該回應的 nonce；csp-nonce meta 的 nonce 相同；React Aria 的 `<style>` 已加入且帶 nonce（script 在 policy 下執行了）；24 個頁面 24 個不同 nonce。
  2. en、zh-Hant：`signedInViews` 的 7 個 view 經 BFF 與測試 provider 登入：`/auth/login` 303、`/auth/callback` 303、頁面 200 都有 header，頁面的 script 都帶 nonce。
  3. 從 signed-out 頁面點「Sign in」：五步 navigation（`/projects`、`/auth/login`、provider 的 `/authorize`、`/auth/callback`、`/projects`）都沒有 Referer，登入成功。
  4. BFF 的 route handler 與其他回應：`GET /auth/login` 303、沒有 login 的 callback 303、跨站 `POST /auth/logout` 403、signed out 的 `/api/v1/me` 401、跨站 `POST /api/v1/projects` 403、`/auth/error` 200、不存在的頁面 404、icon、`/_next/static` 的 script；client 送的 `x-nonce` 與 CSP 不會出現在 HTML；兩個 request 兩個 nonce，各在自己的 HTML。
  5. Policy 真的擋：inline `onload` 不執行（`script-src-attr`）、帶 nonce 的 script 執行、eval 為 `EvalError`、沒有 nonce 的 `<style>` 不套用而帶 nonce 的套用、provider origin 的圖片與 `fetch` 被擋、送到 provider 的表單沒送出、`<base>` 不改變 base URI、iframe 中的 app 不 render；監看收到其中每一項的 report（證明監看看得到 violation）。
- 既有的整個 suite（a11y、keyboard、theme 與字型、simplicity、strings、session、bootstrap、editor（React Flow）、proposal、results）在 policy 開啟、而且每個 test 都監看 violation 的情況下執行，結果見 §6。
- `editor.spec.ts` 的滑鼠 test 改了一行：開始拖曳之前 `scrollIntoViewIfNeeded()` 把畫布捲進視窗。原因：第一次 `make e2e-test`（`e2e-test-run1.log`）只有這個 test 失敗（513 passed、1 failed、32 skipped）：告知讓畫布下移 22 px（實測：沒有告知時畫布頂端 y≈678，有告知時 y≈700），在 900 px 高的視窗中，要連接的兩個 handle 在 y≈894，第一次拖曳後出現的「請先儲存」理由又多一行，handle 就落到視窗外，連線沒有發生。這個 test 原本就貼著視窗底部（沒有告知也只剩約 6 px）。改法是 person 會做的事（先捲到畫布），斷言完全沒變；另一種做法是讓告知不佔高度（例如和模式的說明同一行），但那就不在「欄位下方」，所以沒有選。

## 4. Red → green

| 步驟 | 證據 | 結果 |
|---|---|---|
| Header 的 unit test 先寫 | `checks/evidence/2026-10-26-T-057-web/red-headers-unit.txt` | exit 1：`Cannot find module '../lib/security/headers'` |
| 實作 `headers.ts`、`proxy.ts` | — | 15 passed |
| Layout 的 nonce test 先寫 | `red-layout-nonce.txt` | exit 1：1 failed（沒有 meta） |
| 實作 layout 的 meta | — | 32 passed（一個 case 改正：`Headers` 會去掉值尾端的空白，所以「多一個空白」不是有效的反例，換成「多一個字元」） |
| 告知的 unit test 先寫 | `red-notice-unit.txt` | exit 1：5 failed |
| 實作告知 | — | 21 passed；全部 1062 passed |
| e2e security spec 第一次 | `e2e-security-spec-attempt1.log` | 3 failed：(a) 有些 preview 刻意保持 `aria-busy`，改照 `open()` 的等法；(b) 用 DOM API 加入的 script 在 `'strict-dynamic'` 下本來就被信任，改測 inline event handler；eval 在 DevTools 的 evaluate 中被允許，改在之後的 task 執行。都是測試寫錯，不是放寬 policy。 |
| e2e security spec 第二、三次 | `e2e-security-spec-attempt2.log`、`-attempt3.log` | 6 passed＋1 failed（report 的字串格式）→ 7 passed |
| 第一次 `make e2e-test` | `e2e-test-run1.log` | exit 2：513 passed、1 failed（editor 的滑鼠 test，§3.2）、32 skipped（screenshot） |
| `next dev` 的 probe | （本節之外，見 §2.3） | dev overlay 的 `<style>` 被擋 |
| dev 的 style test 先寫 | `red-dev-style.txt` | exit 1：1 failed |
| 實作 dev 的 `style-src-elem` | `next-dev-probe.txt` | 15 passed；dev 的 6 個頁面沒有 violation |
| 第二次 `make e2e-test` | `e2e-test-run2.log` | exit 0：514 passed、32 skipped（§6） |

## 5. Mutation

臨時 harness（不進 repo；副本在 evidence 的 `mutate.py.txt`、`mutate-e2e.py.txt`）：每個 mutant 只替換一個檔案中的一段字串，執行相關的測試，記錄後還原（之後 `git status` 乾淨）。

- **Unit，程式（45 個，最終程式碼）**：`headers.ts` 30 個（拿掉 `'strict-dynamic'`、加 `'unsafe-inline'`、eval 不限 development、style-src-elem 沒有 nonce、dev 的 style 放寬出現在 build、development 也帶 nonce、拿掉 style-src-attr、fallback 放 nonce、frame-ancestors `'self'`、拿掉 object-src、base-uri `'self'`、form-action／connect-src 加 `https:`、img-src 加 `data:`、default-src `'self'`、upgrade 永遠加、HSTS 永遠／永不、max-age 一天、拿掉 nosniff、referrer 改 `strict-origin-when-cross-origin`、拿掉 XFO、允許 camera、geolocation `(self)`、nonce regex 不錨定結尾、policy 收任何 nonce、8 bytes nonce、固定 nonce、`!== "http:"` 當 https、沒有 origin 當 https）；`proxy.ts` 7 個（request 沒有 policy、沒有 `x-nonce`、沿用 client 的 nonce、https 看 request、development 看 `!== "production"`、回應沒有 header、加上略過 BFF 的 matcher）；`layout.tsx` 2 個（任何值都 render meta、沒有 meta）；告知 6 個（欄位不指向告知、不 render、ask form 不傳、放錯 key、照片按鈕指向理由、照片按鈕不指向告知）。**45 killed，0 survived**（`mutants-unit-code.tsv`）。
- **Unit，catalog（3 個）**：英文改寫、中文改寫、中文換成英文。**3 killed**（`mutants-unit-catalog.tsv`）。
- **Browser（4 個，在共用 lock 下 rebuild 後只跑相關 spec）**：E1 沒有 csp-nonce meta：每頁 `style-src-elem` 的 violation（React Aria 的 `<style>`），security spec 與 theme spec 11 failed；E2 拿掉 `style-src-attr`：只被 header 的完全比對殺死（11 failed），**瀏覽器中沒有 violation**，因為 attribute 退回 `style-src 'self' 'unsafe-inline'`，有效 policy 與原本相同（等價 mutant）；E2b `style-src-attr 'none'`：每頁 `style-src-attr` 的 violation，11 failed；E3 Permissions-Policy 加入 Chromium 不認得的 `bluetooth`：每頁 console 警告，theme spec 4 failed。**4 killed**（`mutants-e2e.tsv`、`e2e-mutant-*.log`）。這證明 suite 的監看在既有 spec（theme）中也會因 violation 失敗，不只是新的 spec。

## 6. 命令與結果

環境：Node v22.22.0（repo 要求 24；兩個 npm project 以 `npm ci --engine-strict=false` 安裝，lockfile 未變）、Playwright 1.56.1、Chromium 1194（`/opt/pw-browsers`）。所有 log 在 `checks/evidence/2026-10-26-T-057-web/`。e2e 在 coordinator 的 `exit-codes.txt` 出現 `e2e-test exit` 之後、在共用 lock（`flock …/scratchpad/e2e.lock`）下、確認 3100–3102 沒有被佔用後執行（`e2e-run.sh.txt`）。

| 命令 | Exit | 結果 | Log |
|---|---|---|---|
| `make frontend-test`（lint、typecheck、vitest） | 0 | lint 與 typecheck 無訊息；48 個檔案、1062 個測試通過 | `frontend-test.log` |
| `make frontend-build` | 0 | 所有頁面 `ƒ`（dynamic）、`/icon.svg` `○`、`ƒ Proxy (Middleware)` | `frontend-build.log` |
| `npm audit --prefix apps/web --omit=dev` | 0 | found 0 vulnerabilities | `npm-audit.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 全部通過（`checks/docs-check.json` 由它重寫：137 個 markdown、130 個連結） | `spec-check.log` |
| `make e2e-test`（第一次） | 2 | 513 passed、1 failed（editor 滑鼠 test，§3.2）、32 skipped | `e2e-test-run1.log` |
| `npm --prefix apps/web-e2e test -- tests/editor.spec.ts`（修正後） | 0 | 13 passed | `e2e-editor-spec-after-fix.log` |
| `make e2e-test`（最終） | 0 | **514 passed、0 failed、32 skipped**（screenshot，NOT RUN）；含新的 7 個 security test，且其他 507 個 test 每個都在監看下沒有任何 CSP／Permissions-Policy report、每個 app document 都有 header | `e2e-test-run2.log` |
| 本機 `next start`，`BFF_APP_ORIGIN=https://geminus.example` 與 `http://127.0.0.1:3310`（同一個 build） | — | https：HSTS 與 `upgrade-insecure-requests`；http（即使 `X-Forwarded-Proto: https`）：都沒有 | `hsts-runtime.txt` |
| 本機 `next dev`（:3330）＋ probe | 0 | dev policy；6 個頁面沒有 violation、沒有 console error 或 warning | `next-dev-probe.txt` |

最終 `make e2e-test` 之後只改了文件與 `headers.ts` 的一行註解（"console error" 改為 "console warning"，Chromium 對未知 feature 印的是 warning）；`make frontend-test`、`make frontend-build`、`npm audit`、`make spec-check` 都在那之後重跑（上表），e2e 沒有重跑。

兩次 e2e 中 WebServer 的 `failed to pipe response`（`UND_ERR_SOCKET`）來自 `session.spec.ts` 故意切斷 API 回應的 test；coordinator 的 log 中也有，不是本切片造成的。

## 7. NOT RUN

- 部署到 Railway 後的 header 實測（`curl -sI https://<web 網域>/`）與真的 Auth0 登入（含 `Referrer-Policy: no-referrer`、`form-action 'self'`）：NOT RUN（本切片不部署）。
- Docker image（`GEMINUS_NEXT_STANDALONE=1` 的 standalone server）中的 proxy：NOT RUN；只驗證了 `next start`。
- Screenshot 比對：NOT RUN（只在 CI 的 pinned container，`E2E_SCREENSHOTS=1`）；告知不在 screenshot 的四個頁面中。
- Chromium 以外的瀏覽器（Firefox、Safari、舊瀏覽器的 fallback）：NOT RUN。
- global-error 頁在瀏覽器中的 CSP 行為：NOT RUN（§2.4）。

## 8. 其他觀察

- `npm ci` 回報 dev dependency `happy-dom@20.14.5` 有一個 high（GHSA-834v-8h66-3h79，修正版 21.0.4 為 major）；它只在 unit test 中使用，`npm audit --omit=dev` 為 0。本切片沒有處理（不在範圍，也不新增或升級套件）。
- `docs/runbooks/deploy.md` §5 與未涵蓋清單原本說 header 尚未設定，已改為指向 SECURITY §3（只改文件）。

## 9. 問題

1. **zh-Hant 的用字**：告知照 owner 原文「內容會送到 AI，只放 synthetic 資料。」，但 catalog 其他地方把 synthetic 譯為「合成」（例如 `galleryResults.intro`）。要不要改成「只放合成資料」？
2. **HSTS**：目前 `max-age=31536000`，沒有 `includeSubDomains`、`preload`。最終網域確定後要不要加？（加了 preload 很難撤回。）
3. **Referrer-Policy**：選了比要求更嚴的 `no-referrer`（e2e 的登入、登出、寫入都通過）。真的 Auth0 沒有測；如果部署後的登入有問題，退回 `strict-origin-when-cross-origin` 只要改一行。可以接受嗎？
4. **`style-src-attr 'unsafe-inline'`** 是唯一放寬（§2.3）。接受，或要求把 React Aria 的 VisuallyHidden 等改成 CSS class（需要包裝或 patch library）？
5. **CSP report**：要不要之後加 `report-to` 與收集端點（目前沒有，violation 只在瀏覽器中看得到）？
6. **Editor 的滑鼠 test**：改為先把畫布捲進視窗（§3.2）。可以接受，或希望 model 頁的版面讓畫布在 900 px 高的視窗中不用捲動就完整可見（那需要重新安排 ask form，例如收合）？
