# T-044 驗證報告：Bilingual UI foundation（en 預設、zh-Hant）

**日期：2026-10-18｜狀態：`review`（首輪獨立 review 為 CHANGES REQUESTED，已先紅後綠修正，等待同一 reviewer 複核；依 H-003 驗收）｜實作選擇：ADR-020（ACCEPTED，H-006）｜需求：REQ-022、REQ-004｜Gate：無｜驗收測試：TEST-031（T-044 部分，見「驗收對照」）**

逐條輸出在 `checks/evidence/2026-10-18-T-044/`。範圍依 tasks.json：i18n 機制（另立 ADR）、catalog key 一致性、lint 禁止硬編字串、cookie 語言切換與保存、Intl 格式、route 無語言前綴、既有 T-014 畫面與每個 Problem code 的兩語文案、DomainPack template 加 locale（contract 與 conformance suite 先改）且 SoftwareDelivery 提供 zh-Hant 名稱。

## 交付內容

| 位置 | 內容 |
|---|---|
| `schemas/common.schema.json` | `Locale`（`en`、`zh-Hant`）；`ValidationCode`（21 個：generic validator 14、patch 6、P0 pack 的 `DOMAIN_MISMATCH`）。`ValidationIssue.code` 仍是 string（pack 可有自己的 code） |
| `schemas/http.schema.json` | `CreateModel.locale`（optional，省略為 `en`），決定 template 預設名稱的語言 |
| `apps/web/lib/contracts/` | 由 schema 重新生成 |
| `internal/domainpack/domainpack.go` | `Locale`、`Locales`（預設在前）、optional `TemplateProvider`（`Templates()`、`Template(name, modelID, modelName, locale)`）；`DomainPack` method set 不變 |
| `internal/domainpack/conformance/conformance.go` | 新步驟 `templates`：`templates/list`（非空、不重複、無空名、panic 是 finding）、`templates/valid`（每個 locale：有 model 且無 error、domain／model ID／名稱為所要求者、通過 generic validator 與 pack 自己的規則（warning 不算））、`templates/deterministic`、`templates/locales`（除名稱外各 locale 的 canonical JSON 相同）、`templates/unknown-locale`（`""`、`fr`、`EN`、`zh-hant`、`zh-Hans` 只回 error）、`templates/unknown-template` |
| `internal/domainpack/conformance/broken_test.go` | 19 個壞 template pack（每個只壞一個性質）各觸發指定 finding；2 個正確 pack（queue template、只發 warning 的 template）無 finding |
| `internal/domainpack/softwaredelivery/template.go` | `Template(..., locale)`；`ErrUnknownLocale`；`templates/default_delivery_pipeline.zh-Hant.names.json` 依 ID 給名稱，嚴格比對（少、多、空名、未知 member、壞 JSON 皆為 internal error）；`Pack` 實作 `TemplateProvider` |
| `internal/model/validate/codes_test.go` | 宣告的 validator 與 patch code 必須與 contract 的 `ValidationCode`（扣除 pack 的 `DOMAIN_MISMATCH`）完全相同 |
| `apps/web/lib/i18n/` | `locale.ts`（locale、cookie）、`translate.ts`（translator、Intl 日期／數字／期間）、`provider.tsx`（`I18nProvider`、`useI18n`）、`server.ts`（server components 讀 cookie） |
| `apps/web/messages/en.ts`、`zh-Hant.ts` | 每個使用者可見字串；zh-Hant 型別對齊 en |
| `apps/web/app/` | 所有元件改用 catalog；`layout.tsx` 依 cookie 設 `<html lang>`、`I18nProvider` 與語言切換；`_components/locale-switcher.tsx`；ProblemView 依 error code 說明、依 validation code 說明 detail，server／client 的英文訊息以 `lang="en"` 列為技術細節；專案列表顯示建立時間（Intl） |
| `apps/web/eslint.config.mjs` | `.tsx`（tests 除外）禁止硬編使用者可見字串：JSX 文字、文字 attribute、由條件渲染的字串 |
| 文件 | ADR-020（ACCEPTED，H-006）；ARCHITECTURE.md §4 語言段落 |
| `checks/evidence/2026-10-18-T-044/mutants.py` | mutation harness（Go 經 `-overlay`；runtime 讀取的檔案與前端在 scratch copy） |

## 獨立 review（2026-10-18，H-003）：CHANGES REQUESTED 與修正

Reviewer 以自己的 `git archive 1d6eadd` export 執行，沒有使用工作樹。報告數字全部重現：118 tests、mutation 102／104、2 個 equivalent 屬實。它沒有 blocker，提出 3 個 medium、8 個 low。每項先寫會失敗的測試再修正，證據在 `checks/evidence/2026-10-18-T-044/review1/`：`red-*.log`、`green-*.log`；`red-frontend.log` 是本輪的前端測試對 1d6eadd 的執行結果，22 個失敗，兩個測試檔因新頁面不存在而無法載入。

| # | 發現 | 修正 |
|---|---|---|
| M1 | Conformance 的 `templates` 不檢查並行與共用：每個 locale 快取同一個 `*ModelIR` 的 pack 0 finding，tenant A 的 model 就是 B 的 | `Template` 加入 `concurrent` 步驟；新檢查 `templates/copies`：以另一個 ID 與名稱再要一次並改動它的每個名稱，第一個 model 必須不變。未知 locale 加 `zh`、`zh-TW`、`zh-Hant-TW`、`en-US`、`en-GB`；未知 template 加大小寫不同與前後空白的名稱。新增 5 個壞 pack：快取、共用 resources、並行時結果不同、接受近似 locale、不分大小寫。全部 race-free，`backend-race` 通過 |
| M2 | zh-Hant 下未知 route 顯示 Next.js 內建的英文 404（標成 `lang="zh-Hant"`）；沒有 `created_at` 的 project 讓整頁失敗，`null` 顯示為「建立於 null」 | `app/not-found.tsx`（含 `<title>`）、`app/error.tsx`、`app/global-error.tsx`（瀏覽器讀 cookie 的 `useRememberedLocale`），共用 catalog 文字的 `CrashNotice`：可重試、顯示 `digest`、不顯示 error message。`dateTime` 對非字串回空字串，該段略過 |
| M3 | Lint 漏掉：文字 attribute 的條件、`??`、串接、cast；元件的 text prop；submit 的 value；物件 prop；陣列；callback 回傳；`alert`／`confirm`／`document.title`；重音字、CJK 擴充區 | 規則擴大（見 ADR-020 Lint）；lint-guards 新增 17 個應擋、10 個應放行的 case。Reviewer 列的模式中，仍擋不到的（經函式呼叫的字串、其他名稱的 prop、只有符號的文字）列入 ADR-020 限制 |
| L1 | `lint-condition-any-path` 的 kill 來自無效的 ESLint config | Harness 把 config 錯誤（`CONFIG-INVALID`）與逾時（`TIMEOUT`）當 harness 錯誤，不算 kill；該 mutant 改為有效寫法，由「allows a catalog key chosen by a condition」擋下 |
| L2 | 3 個 kill 其實是 T-014 random-sequence 測試在負載下逾時（5 s 預設） | 該測試設自己的 60 s timeout，assertion 不變；harness 不再把逾時算 kill |
| L3 | 報告說 zh 的 404 不洩漏有測試，其實沒有 | `i18n-dom.test.tsx` 新增 zh-Hant 404 測試 |
| L4 | 不存在的日期（2026-02-30、24:00）被 V8 進位；小寫 `t`／`z` 被拒 | 欄位檢查（月份天數、閏年、時、分、秒、時區）；大小寫皆可 |
| L5 | `ValidationCode` 說明寫未知 code 顯示通用文字，UI 實際顯示 server 的 reason | 改正 schema 說明並重新產生 TS |
| L6 | 名稱檔接受空白或含控制字元的名稱；過長名稱變成 `ErrInvalidTemplateInput` | 都改為 internal error；200 字元邊界測試 |
| L7 | 「版本」同指 build 與 model version；句子間多空格；用語 | 「目前的應用程式尚未提供…」、「目前尚無法翻頁」；`I18n.gap`（英文空格、中文無）；主選單、發布、分布 |
| L8 | `CreateModel.locale` 沒有 contract fixture | 有 locale、沒有 locale、無效 locale（`zh-TW`，錯在 `/locale`）三個 fixture |
| info | 參數經 prototype 查找；修訂號被分組；`exit-codes.txt` 寫成 `exit=exit=0` | `Object.hasOwn`；修訂與版本號以字串傳入；改正 |

未處理的 info：
- `domainpack.Locales` 是可改的 exported slice：沿用，以測試鎖成與 contract 相同；
- zh-Hant template 的 `metadata.origin` 與 assumptions 仍是英文：屬 template 的說明資料，T-015 顯示時再處理；
- `formatDuration` 目前沒有 UI 使用：留給 T-032；
- 本機 Node 22 與 `engines` 的 24 不同：CI 使用 24。

### 修正後（beb96e1，`review1/`）

| 指令 | Exit | 說明 |
|---|---|---|
| `make frontend-test` | 0 | 10 個檔案、153 個 test |
| `make frontend-build` | 0 | 所有 route 為 dynamic；`/_not-found` 為自己的頁面 |
| `make backend-test`、`backend-race`、`architecture-check`、`spec-check` | 0 | `PASS architecture: 32 packages, 10 rules, 16 build configurations` |
| `go test -count=1 -v`（domainpack、conformance、softwaredelivery、model/validate、coldstart） | 0 | 190 個 RUN，0 FAIL |
| mutation | 0 | 144 個：142 killed（58 由 Go test、84 由 vitest）、2 equivalent、0 unexpected、0 harness 錯誤（`mutation.log`）。第一次修正後的全量執行（`mutation-pass1.log`）有 1 個存活：blank-name 的測試值含 tab，被控制字元檢查擋下，分不出兩個檢查；改用只有空白的值後重跑全量 |
| `curl`（`next start`） | — | `server-render.log`：`/no-such-route` 回 404，無 cookie 為英文、zh-Hant 為中文 title／h1／內文／連結，沒有 Next.js 的英文 404 文字 |
| Chromium | 0 | `browser-switch.log`：切換、重新載入、切回，無 console error |

## 首版的實際執行結果

所有指令都在 459fedc（`commit.txt`；之後的 commit 只改文件與 T-009）上執行，exit code 見 `exit-codes.txt`。CI：d2b8fd1（含 459fedc 的程式）的 run [39144694773](https://github.com/KCL1104/Geminus/actions/runs/39144694773) 4/4 success。

| 指令 | Exit | 說明 |
|---|---|---|
| `make frontend-test`（lint、typecheck、vitest） | 0 | `frontend-test.log`：10 個檔案、118 個 test |
| `make frontend-build` | 0 | `frontend-build.log`；所有 route 為 dynamic（ƒ），原因見 ADR-020 |
| `make backend-test` | 0 | `backend-test.log` |
| `make backend-race` | 0 | `backend-race.log` |
| `make architecture-check` | 0 | `architecture-check.log`：`PASS architecture: 32 packages, 10 rules, 16 build configurations` |
| `make spec-check` | 0 | `spec-check.log` |
| `go test -count=1 -v`（domainpack、conformance、softwaredelivery、model/validate、coldstart） | 0 | `t044-go-tests.log`：185 個 RUN（含 subtest），0 FAIL |
| mutation（`mutants.py`） | 0 | `mutation.log`：104 個，102 killed（48 由 Go test、54 由 vitest），2 equivalent，0 unexpected |
| 瀏覽器（`next start` + Chromium 1194，Playwright 1.56.1） | 0 | `browser-switch.log`、`browser-switch.cjs`：預設 en；選 zh-Hant 後 `<html lang>`、h1、h2 換成中文，URL 不變，cookie 為 `geminus_locale=zh-Hant; path=/; SameSite=Lax`；重新載入仍為 zh-Hant；切回 en；無 console error 或 hydration 警告 |
| `curl`（`next start`） | — | `server-render.log`：無 cookie 為 `lang="en"`；`zh-Hant` cookie 時 nav、label、h1、連結為中文；`zh-hans`、`zh-hant`、`EN` cookie 視為未選（en）；h1 由 server 渲染、頁面 h2 為「需要登入」 |

先紅後綠，保存的紀錄：

- `red-conformance.log`：`templates` 步驟實作前，8 個壞 template pack 都沒有 finding。
- `red-web.log`：lint 規則加入前，8 個 hard-coded 字串 case 未被擋；`i18n-dom.test.tsx` 在元件改寫與 `LocaleSwitcher` 建立前失敗。
- `red-names.log`、`red-namesfail.log`：`applyNames`／`templateFrom` 拆出前無法編譯。

下列紅燈在 session 中看到但未存檔：條件中的 key、巢狀條件與 fragment 的 lint case；日期只接受 RFC 3339、provider fallback 與單數訊息。它們的效果另由 mutation 證明：`lint-condition-depth-1`、`lint-condition-no-fragment`、`lint-condition-any-path`、`date-shape-unchecked`、`provider-date-throws`、`plural-always-other` 皆 killed。

### Mutation

第一次全量執行發現三個 harness 問題與三個測試缺口，都已修正後重跑：

- 沒有壞 pack 對有效 locale 同時回 model 與 error：新增 `templateErrorAndModel`。
- 三個 mutant 無法編譯（未使用變數）：改寫；harness 現在把無法編譯的 mutant 視為 harness 錯誤，不算 killed。
- `localize-error-ignored` 存活：embedded 名稱檔永遠正確，沒有任何測試證明 `Template` 會回傳 `localize` 的錯誤。改由 `templateFrom(fsys, …)` 讀檔，`TestTemplateNamesFailure` 以缺檔與不完整的名稱檔證明回 error 且無 model。
- `lint-attr-template`、`lint-condition-template` 存活：新增 attribute 與條件中的 template literal case。

補測試時一度讓 SoftwareDelivery pack import `io/fs`（408933b），被 `make architecture-check` 擋下（pack 不得做 I/O），408933b、2d3d909、fbe45c3 三個 commit 的 CI backend job 因此失敗；459fedc 改為傳入讀檔函式後恢復。

最終結果（`mutation.log`，459fedc）：104 個，102 killed，2 equivalent，0 unexpected。Equivalent 兩個，各附理由：

- `list-panic-continues`：`Templates()` panic 時 pack 已因 `templates/list panicked` 失敗；繼續執行只多跑 unknown-template，只會多 finding，不會少。
- `date-invalid-unchecked`：`Intl.DateTimeFormat.format` 對 Invalid Date 本身會 throw RangeError。

## 驗收對照（TEST-031 的 T-044 部分）

| TEST-031 項目 | 測試 |
|---|---|
| 預設 en | `layout.test.tsx`（無 cookie → `lang="en"`、英文 shell、`en` 為選取項）；`i18n.test.ts`（`defaultLocale`、`parseLocale` 未知值） |
| zh-Hant 與 en key 完全一致、無空值 | `i18n.test.ts`：key 集合、空值、placeholder 與複數形狀、只有 4 個允許相同的文字；型別（`Catalog`）讓缺 key 無法編譯 |
| lint 禁止硬編字串 | `lint-guards.test.ts`：33 個應擋、17 個應放行（review M3 後）；`make frontend-test` 的 lint 在 0 warning 下通過 |
| 切換語言後保留 | `i18n-dom.test.tsx`（只寫語言 cookie、呼叫 refresh；`LocaleMenu` 經 router refresh）；`layout.test.tsx`（cookie → `lang="zh-Hant"` 與中文 shell；`zh-hant`、`zh-Hans`、`zh`、`EN`、空值、帶屬性的值都視為未選）；瀏覽器紀錄 |
| 每個 Problem／validation code 皆有兩語文案 | `i18n.test.ts`（每個 `ErrorCode`、`ValidationCode`、`ProblemKind`）；`codes_test.go`、`TestValidationCodeInContract`（Go 宣告的 code 等於 contract）；`i18n-dom.test.tsx`（中文說明、server 英文訊息標 `lang="en"`、未知 code 顯示 reason） |
| Intl 日期／數字 | `i18n.test.ts`（日期、數字、百分比、期間、千分位、複數；只接受 RFC 3339）；`i18n-dom.test.tsx`（`建立於 2026年10月15日`、`草稿修訂版 3。`、`2 項變更尚未儲存。`、`請在 30 秒後重試。`） |
| Template 兩語預設名稱 | `TestTemplateLocales`（en／zh 名稱、除名稱外相同、未知 locale）、`TestNamesFileStrict`、`TestTemplateNamesFailure`、`TestConformance`（P0 pack 通過 `templates` 步驟）、`TestBrokenPacksAreCaught`（24 個壞 template pack）、`TestLocalesMatchContract` |
| 找不到與錯誤的頁面 | `layout.test.tsx`（not-found 兩語、global-error 的 server 快照）、`i18n-dom.test.tsx`（error 頁兩語、重試、digest、不顯示 message；`useRememberedLocale`；缺 `created_at` 不讓整頁失敗） |
| 404 不洩漏（TEST-009 既有） | `ui.test.tsx`（en）與 `i18n-dom.test.tsx`（zh-Hant，review L3 補上）：不顯示 message／details |

## 需 owner 知悉

- ADR-020 已由 owner 批准為 ACCEPTED（H-006）：自寫 catalog（不用 next-intl／FormatJS）、cookie 偏好、不做 Accept-Language 協商（瀏覽器是中文也從 en 開始）、route 無前綴、所有頁面 dynamic rendering。
- 中文先只做繁體 `zh-Hant`（H-006）。日後要加簡體，只需 catalog、names file 與 enum。
- zh-Hant 文案由 agent 撰寫，未經母語審稿；正式 release 前建議審閱 `apps/web/messages/zh-Hant.ts` 與名稱檔。
- 既有 T-011 template 的英文預設名稱 `Ci Runner` 是由 ID 轉換而來（應為 `CI runner`）；未在本任務修改（會改 golden model），列為 follow-up。

## 與規劃的差異

- `ValidationCode` enum 原本不在 contract；UI 依 code 在地化需要封閉清單，因此先改 contract 再改 UI（AGENTS.md §1）。
- `ProblemView` 原本只對 409 依 code 說明；現在每個已知 code 都依 code 說明，其他 kind 的通用說明只在沒有 code 時使用。
- Server 的 message 從「直接顯示」改為「技術細節」並標 `lang="en"`：client 自己產生的 network／protocol 診斷也是英文，不能標成「伺服器訊息」。

## NOT RUN／延後

| 項目 | 原因／歸屬 |
|---|---|
| 切換語言「不需重新登入」 | 登入 session 尚不存在（T-043）；cookie 不含身分，切換也不碰 auth，T-043 需以真實 session 驗證 |
| 切換語言保留未存的 draft 編輯 | 尚無可編輯 draft 的頁面（T-015）；`router.refresh()` 依 Next.js 文件保留 client state，未驗證 |
| AI prose 依 locale、analyst 數字校驗兩語 | T-025／T-030，受 D-018 與資料政策 gate |
| E2E 主流程兩語各跑一次 | T-040；本任務只有 scratch Playwright 紀錄（未列入 `make e2e-test`） |
| Palette 的 node／resource label 兩語 | Pack catalog 的 `Label` 是英文 pack 資料；T-015 以 kind 對應 UI catalog |
| Create-model handler 傳入 `locale` | T-012／T-013（contract 已加欄位） |
