# T-043 後續（登入後的 UI bootstrap）獨立審查第 1 輪

**日期：2026-10-26｜審查者：獨立 reviewer（H-003；hackathon 模式 H-029：一輪），沒有參與撰寫｜方式：唯讀審查，除本檔與 `checks/T-031_REVIEW_r1.md` 外沒有改任何 tracked 檔，沒有 push｜審查的 tree：HEAD 5b66648（含下列 commits）｜結論：ACCEPT（沒有 High／Medium）**

## 審查範圍

- Commits：e0a9901（simplicity.test 的模型頁規則）、986b451（先寫的測試與 fake API 的 `/v1/me`）、e7aec84（實作）、96b6e42（報告、evidence、文件）、coordinator 的 f276494（整合驗證）。f276494 之後到 HEAD，這一片的檔案只有 `apps/web/app/_components/states.tsx` 改了一行（T-031 的 e7d9d2c 在 `Loadable` 加 `proposal`）；layout、auth-bootstrap、account、`lib/auth`、`lib/return-to.ts`、`lib/session` 都沒有再改。
- 報告：`checks/T-043_VALIDATION.md` 的「後續：登入後的 UI bootstrap」與「Coordinator 驗證（登入後的 UI bootstrap）」；evidence：`checks/evidence/2026-10-26-T-043-bootstrap/`（含 `coordinator/`）。
- 依據：AGENTS.md §2、docs/SECURITY.md §3（BFF、session、CSRF）、ADR-016、ADR-018（含 2026-10-26 補記）、DESIGN.md Simplicity 與「The account」、openapi `/me`（`NoOrganizationContext`）與 `OrganizationContext`、`schemas/http.schema.json` 的 `Me`／`OwnMembership`、BFF 程式（`apps/web/lib/session/*`、`apps/web/app/auth/*`、`apps/web/app/api/v1/[...path]/route.ts`）。

## 結論

這一片做的事少而正確：root layout 只把「session cookie 打得開」的布林值與一個合乎 D-001 的 organization ID 交給 client component；`/me` 經同源 proxy、不帶 `X-Organization-ID`；組織只從 `/me` 的清單中選（cookie 只是 selector）；未知 role 為 null、`canEdit` 為 false；沒有 session、401、403、0 個 membership、`/me` 失敗、選擇中都不發 tenant request；登出帶 `X-Geminus-Request`、只前往 http／https 的 URL。我試的 open redirect 編碼全部留在本站。測試對它們的宣稱有實際的斷言，報告中的數字與 evidence 相符。沒有 High、Medium 或 Low；以下 Info 供 coordinator 參考。

## 逐項檢查

| 項目 | 結果 | 依據 |
|---|---|---|
| token／cookie 的值不進 client JS、HTML、storage、log | 通過 | `app/layout.tsx:35-38` 只交出 `signedIn` 與 `parseOrganization(...)`；`lib/session/bff.ts:225-227` `hasSession` 只回布林、不 refresh、不 log；新程式沒有 `console`、`localStorage`、`sessionStorage`（grep）。`tests/layout-session.test.tsx` 斷言 HTML 不含封裝的值；`apps/web-e2e/tests/bootstrap.spec.ts:64-67` 檢查 HTML、`document.cookie`、localStorage、sessionStorage 沒有 token。我以 `next start`（沒有 BFF 設定）送出假的 session cookie：HTML 不含它，只含已驗證的 organization ID |
| `safeReturnTo` 擋 open redirect | 通過（見 I1） | `lib/return-to.ts:16-20`。Probe（`node --experimental-strip-types`，對 repo 的同一個檔）：`//evil.test`、`/\evil.test`、`%2F%2Fevil.test`、`/\tevil`、`\t//evil.test`、`/∕∕evil.test`（U+2215）、`/／／evil.test`（U+FF0F）、`⁄evil`、`javascript:…`、`JaVaScRiPt:…`、`/\r\nX: y`、`/ /evil`、`/\u0000` 都成為 `/`；BFF 以 `searchParams.get` 解一次碼後，`?return_to=%2F%2Fevil.test`、`/%2F%2Fevil.test`（→`///…`）、`%2F%5Cevil.test`、`/%09/evil.test`、`%E2%88%95%E2%88%95evil.test` 也都是 `/`。留下的 `/%2F%2Fevil.test`、`/%5Cevil.test`、`/javascript:alert(1)`、`/@evil.test`、`/..//evil.test` 在 callback 的 `${cfg.appOrigin}${returnTo}`（`bff.ts:194`）下 host 都是本站；連結的 `signInHref` 與 BFF 解碼後的值一致（沒有 mismatch） |
| `X-Organization-ID` 只來自 `/me`，`/me` 不帶 | 通過 | `lib/auth/bootstrap.ts:51-70`（記住的 ID 只在清單中才用）；`app/_components/auth-bootstrap.tsx:42-45`（session 只來自 ready 的 organization）；`lib/api/client.ts` 的 `getMe` 為 `organization: false`。`tests/auth-bootstrap.test.tsx:158-166`、`bootstrap.spec.ts:57-63`（fake API 看到 `/v1/me` 的 org 為 null，其他每個呼叫是該組織、有 Bearer、沒有 cookie） |
| role 對應 fail closed | 通過 | `bootstrap.ts:41`：不在 owner／editor／viewer 的值為 null；`canEdit(null)` 為 false。`tests/bootstrap.test.ts:112-115`、`auth-bootstrap.test.tsx:226-237`（admin → `none false`） |
| 登出的 CSRF header 與 provider logout URL | 通過（見 I2） | `lib/auth/sign-out.ts:16-52`：`X-Geminus-Request: 1`、`credentials: same-origin`、`redirect: error`、`no-store`；只接受 `https:`／`http:`，其他（含 `javascript:`、非 URL）回 `/`；失敗時回 problem、不清 organization、不轉頁。URL 來自 BFF 的設定（`oidc.ts:242-244`），不是 request 的輸入 |
| organization cookie 的驗證與範圍 | 通過（見 I3） | `lib/auth/organization.ts`：只接受 D-001 的 org ID；`Path=/; SameSite=Lax`、https 加 `Secure`、沒有 Max-Age（browser session）、沒有 Domain；登出時 Max-Age=0。layout 讀回時再以 `parseOrganization` 驗證（`layout-session.test.tsx:68-77` 含 `<script>` 與 `; Path=/`） |
| session 結束與 403 時不要 tenant 資料 | 通過 | `RequireSession`（`app/_components/states.tsx:76-97`）只在 session 存在時 render children；client 沒有 session 時本來就不送（ADR-016）。`auth-bootstrap.test.tsx:168-213`；e2e `bootstrap.spec.ts` 的 ended session 與 403／0 membership（「只呼叫了 `/v1/me`」） |
| 簡潔規則 | 通過 | 未登入時 strip 不變、登入只在需要它的頁面；登出只在有 session 時；組織選擇時 strip 沒有第二種選法；沒有 disabled 控制項。`tests/simplicity.test.tsx`（e0a9901 比 55d0703 的規則更嚴：模型頁不得有原生 disabled、editor 至少有一個 blocked 可檢查、每個 blocked 指向非空文字；其他已登入頁面不得有 blocked）、`simplicity.spec.ts` |
| 測試是否斷言其宣稱、報告數字 | 通過 | 作者：vitest 29 檔 737（`frontend-test.log`）、e2e 258 passed／32 skipped（`e2e-test.log`，bootstrap.spec 10 個在 145–154）、27／27 mutant；coordinator：865、353 passed／32 skipped（`coordinator/e2e-test.log`）、exit code 全為 0（`coordinator/exit-codes.txt`）。都與報告一致 |

## 發現

### High

無。

### Medium

無。

### Low

無。

### Info

**I1：`safeReturnTo` 接受 dot segment 與編碼的分隔字元，安全靠的是外層**

- 位置：`apps/web/lib/return-to.ts:16-20`；callback `apps/web/lib/session/bff.ts:194`。
- 觀察：`/..//evil.test`、`/.//evil.test`、`/./%2e%2e//evil.test` 都被接受；URL parser 把它們解成 path `//evil.test`（`https://app.example//evil.test`）。目前安全，因為 callback 一律以設定的 `appOrigin` 組出絕對 URL，而 Next.js 16.4.1 對 `//evil.test/x`、`/..//evil.test`、`/\evil.test` 回 `308 Location: /evil.test/x`（相對、同源；以 `next start` 與 `curl --path-as-is` 實測）。若之後改成相對 Location，或前方的 proxy 以不同方式正規化 `//`，這條就成了 open redirect。
- 建議（defense in depth，非必要）：比照 proxy 的 `SEGMENT` 規則，拒絕 `.`／`..` segment 與 `%2F`／`%5C`（不分大小寫），並在 `bootstrap.test.ts` 的例子中加入上述三個與 `\t`、U+2215、U+FF0F（我的 probe 已確認它們目前都成為 `/`）。

**I2：登出按鈕沒有進行中的防護與狀態**

- 位置：`apps/web/app/_components/account.tsx:47-67`。
- 觀察：連按兩次會送兩個 `POST /auth/logout`（第二次撤銷已撤銷的 token，RFC 7009 下無害），等待 provider 轉頁期間畫面沒有「Signing out…」之類的 status（提案頁的決定有 `StatusRegion`）。
- 建議：busy ref 加一個 status 區域；可在比賽後處理。

**I3：organization cookie 的 `Secure` 只在純函式測試；沒有 `__Host-` 前綴**

- 位置：`apps/web/app/_components/auth-bootstrap.tsx:24`、`:51`、`:60`。
- 觀察：`organizationCookieValue(…, true)` 有測試，但元件在 https 下是否傳 `true` 沒有測（happy-dom 與 e2e 都是 http）。cookie 也不是 `__Host-`，同一個上層網域下的其他站可以 cookie tossing 一個同名 cookie；影響只是預先選了使用者**自己**的另一個組織（`/me` 沒列出就不用），不是授權。記錄即可。

**I4：`/me` 的 contract 檢查不完整**

- 位置：`apps/web/lib/auth/bootstrap.ts:39`。
- 觀察：`organization_name` 只檢查是字串，沒有檢查 1–200 字元（`OwnMembership`）；`user_id` 沒有檢查。空字串的名稱會成為沒有名稱的按鈕。API 依 contract 不會送；與報告「不符合 contract 的回答…不猜」的描述相比是部分的。

**I5：已知、已記錄、由 coordinator 為比賽期間決定（不是 owner 的 APPROVED）**

- 切換組織時留在同一個 route（模型頁顯示「找不到」），未存的編輯不詢問（`checks/T-043_VALIDATION.md:292`、`:371`）；手機登入後 strip 兩列（`:290`、`:369`）；組織只記到瀏覽器關閉（`:291`、`:370`）；頁面停留期間 session 結束後 strip 的登出與切換器要到 reload 才消失（`:332`）。這些是 coordinator 在 H-027／H-029 下的決定或 agent 的選擇，owner 尚未確認，報告也如此寫，沒有被寫成 APPROVED。
- 8 張 `projects-*` 截圖 baseline 會因「Sign in」連結改變（H-028）：push 後 CI 的截圖比對在 owner 以 `update_screenshots` 接受新 baseline 前會失敗（`:365`）。

**I6：tasks.json 的 evidence 條目寫的是 cherry-pick 前的 hash**

- 位置：`planning/tasks.json:2836-2843`（`0ae7952、512eb32、f76631c，parent 74cb9ae`、`reviewer: pending`）。本分支上的 commit 是 e0a9901、986b451、e7aec84、96b6e42；對應只寫在報告的 coordinator 一節（`checks/T-043_VALIDATION.md:346`）。接受後更新 hash 與 reviewer 欄即可。

## 我執行了什麼（Node 22.22.0；repo 要求 24）

| 指令 | Exit | 結果 |
|---|---|---|
| `npm ci --prefix apps/web --engine-strict=false` | 0 | |
| `make frontend-test`（HEAD 5b66648） | 0 | lint 0 warning、typecheck、vitest 43 檔 985 個全過 |
| `make frontend-build` | 0 | Next.js 16.4.1；`/auth/*`、`/api/v1/[...path]`、所有頁面為動態 |
| `node --experimental-strip-types`：`safeReturnTo`／`signInHref` 的 probe（scratchpad 中的 `lib/return-to.ts` 副本，36 個輸入） | 0 | 見上表與 I1；每個結果的 host 都是本站 |
| `npx next start --port 3917 --hostname 127.0.0.1`（沒有 BFF 設定）＋ `curl --path-as-is` | — | `//evil.test/x` → 308 `/evil.test/x`；`/..//evil.test`、`/\evil.test`、`/%2e%2e//evil.test` → 308 `/evil.test`；`/%2F%2Fevil.test`、`/%5Cevil.test` → 404；`/auth/login` → 503 JSON；帶假的 session cookie 的 `/projects?x=1`：HTML 不含 cookie 的值，登入連結為 `/auth/login?return_to=%2Fprojects%3Fx%3D1`。之後以 PID 停止（exit 143） |

沒有執行：`make e2e-test`（共用的固定 port，依指示只讀 evidence 的 log）、`make spec-check`（會重產 tracked 的 `checks/*.json`）、Go 測試（這一片沒有 Go、contract、schema 或 migration 的變更）、`npm ci --prefix apps/web-e2e`、Node 24、真的 Auth0、截圖比對、任何 live 呼叫。沒有讀任何 key 或 secret。

結束前移除了我建立的 `apps/web/node_modules`、`apps/web/.next`、`apps/web/next-env.d.ts` 與 `apps/web/tsconfig.tsbuildinfo`（都在 .gitignore 中）；`git status --ignored` 除兩份審查檔外乾淨。

## 結論

**ACCEPT**：沒有 High、Medium 或 Low。Info I1–I6 不阻擋；I6 的 hash 在記錄接受時一併更新。
