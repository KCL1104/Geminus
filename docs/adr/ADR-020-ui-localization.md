# ADR-020 — UI 語言：自寫 typed catalog、cookie 語言偏好、template 兩語預設名稱（T-044）

狀態：ACCEPTED（T-044 agent 選定並先實作；owner 於 2026-10-18 批准，並決定中文先只做繁體 `zh-Hant`，H-006）。日期：2026-10-18。需求：REQ-022（owner 新增，H-005）、REQ-004。T-044 scope 要求「i18n 機制（自寫或 library，另立 ADR、版本釘住）」。

## Decision

### Catalog 與翻譯函式（`apps/web/lib/i18n/`、`apps/web/messages/`）

- **自寫**，不加依賴。Catalog 是 TypeScript object：`messages/en.ts`（`as const`）定義 key；`messages/zh-Hant.ts` 型別為 `Catalog`，所以少 key、多 key 或把複數訊息寫成字串都會編譯失敗。
- `createTranslator(locale)`：
  - 插值 `{name}`；數字參數依 locale 用 `Intl.NumberFormat` 格式化。
  - 複數訊息是 `{ one?, other }`，依 `params.count` 用 `Intl.PluralRules` 選；zh-Hant 只有 `other`。
  - 缺參數或 `count` 不是數字時 throw：這是程式錯誤，不顯示 `{name}`。只看 params 自己的值（`Object.hasOwn`），不看 prototype。
  - 修訂號、版本號這類編號以字串傳入，不加千分位；只有數量（count）依語言分組。
  - `gap`：句與句、標籤與值之間的間隔，英文一個空格，中文沒有（「已儲存。草稿修訂版 3。」、「技術細節：…」）。
- 日期、數字、期間：`formatDateTime`、`formatNumber`、`formatDuration`（`Intl.NumberFormat` unit style）。
  - `formatDateTime` 只接受 RFC 3339 date-time（contract 的格式，`T`／`Z` 大小寫皆可），且日期與時間必須存在：`Date.parse` 接受的範圍大得多，並把 2026-02-30 或 24:00 進位成別的時間（T-044 review L4）。檢查不依賴 V8：其他瀏覽器的 `Date` 解析不同。
  - 元件經 `useI18n().dateTime` 使用：API 給的字串若不是 date-time，就照原樣顯示；不是字串的值（缺欄位、`null`）顯示為空，該段文字略過，不讓整頁失敗（T-044 review M2）。
- `tests/i18n.test.ts` 鎖住：
  - 兩語 key 相同且沒有空值；placeholder 與複數形狀相同；
  - 兩語相同的文字只限 `app.name`、`home.title` 與兩個語言名稱；
  - 每個 `ErrorCode`、`ValidationCode`、`ProblemKind` 都有訊息。
- 不用 ICU MessageFormat 或 library（next-intl、FormatJS 皆 MIT，可行）：
  - 目前訊息只需插值與 one/other。
  - next-intl 以 route 前綴與 middleware 為主，本專案不加前綴。
  - 自寫的代價是 plural 與格式化由我們維護。
  - 需要 select、性別、巢狀、第三種語言或外部翻譯流程（抽取工具）時，改用 library 並另立 ADR。

### Locale 與保存

- Locale 只有 `en`（預設、主要）與 `zh-Hant`（H-005；H-006 決定先不加 `zh-Hans`）。
  - Contract 是 `common.schema.json#/$defs/Locale`；Go `domainpack.Locales` 與 TS `locales` 由測試鎖成相同。
- 偏好存在 cookie `geminus_locale`：
  - 值只能是 `en` 或 `zh-Hant`；`Path=/; Max-Age=31536000; SameSite=Lax`，https 下加 `Secure`。
  - 不設 HttpOnly：由 client 的語言切換寫入，內容不是秘密。
  - 不含身分或 session，不參與授權。Go API 不讀 cookie（ADR-018）。
  - 讀取時完全比對，其他值（含 `zh-hant`、`zh-Hans`、`EN`）一律視為未選、用 `en`。
- **不做 Accept-Language 協商**：H-005 定英文為主要語言，瀏覽器語言是中文也從 `en` 開始，由使用者切換。
- **Route 不加語言前綴**：root layout 在 server 端讀 cookie，設定 `<html lang>` 與 `I18nProvider`。
  - 代價：所有 route 變成 dynamic rendering（`next build` 標 ƒ）。ADR-018 的 BFF session 本來就需要每個 request 渲染。
- 切換：`LocaleSwitcher` 寫 cookie，再 `router.refresh()`，server components 以新語言重新渲染，不換 URL。
  - 依 Next.js 文件，`router.refresh()` 保留 client state，未存的 draft 編輯應保留。T-044 尚無可編輯 draft 的頁面，所以這點**未驗證**，留給 T-015 的 E2E。
  - 每個語言名稱以其本身的語言顯示（`English`、`繁體中文`，`<option lang>`）。

### API 錯誤與資料

- API 只回語言中立的 `code` 與英文 `message`（開發者訊息），UI 依 code 顯示。
  - `error.<ErrorCode>` 是說明；`problem.heading.<kind>` 是標題。
  - `message`（server 的，或 client 自己產生的 network／protocol 診斷）放在「Technical details／技術細節」後，標 `lang="en"`，不翻譯。
  - 404 仍不顯示 message 與 details（TEST-009）。
- Validation details：已知的 `ValidationCode` 顯示 `validation.<code>`；未知 code（pack 自訂）顯示 server 的 `reason`，標 `lang="en"`。
- 新增 contract `common.schema.json#/$defs/ValidationCode`（21 個）。
  - Go 測試鎖成與 `internal/model/validate`、`internal/model/patch` 宣告的常數及 P0 pack 的 `DOMAIN_MISMATCH` 相同。
  - `ValidationIssue.code` 仍是 string，pack 可以有自己的 code。
- 使用者輸入的名稱、label、dataset 內容是資料，不翻譯。ID、JSON Pointer、enum、metric path 永不翻譯。

### Template 兩語預設名稱（`internal/domainpack/`）

- 新增 optional interface `domainpack.TemplateProvider`。`DomainPack` 保持原報告的 method set。
  - `Templates()`。
  - `Template(name, modelID, modelName, locale)`：未知 template 或 locale 回 error。
- Conformance suite 新增 `templates` 步驟，對每個 template 檢查：
  - 列表非空、不重複、無空名；
  - 每個 locale 都產生一個通過 generic validator 與 pack 規則的 model，domain、ID、名稱正確；
  - 兩次呼叫相同；
  - 除名稱外各 locale 完全相同；
  - 未知 locale 或 template 只回 error：locale 包括 `zh`、`zh-TW`、`zh-Hant-TW`、`en-US` 這類近似值，template 包括大小寫不同或前後有空白的名稱。
  - 每個呼叫者拿到自己的 model：以另一個 model ID 與名稱再要一次，並改動它的每個名稱，第一個 model 不變（`templates/copies`）。
  - `Template` 也在 `concurrent` 步驟中並行呼叫；data race 由 `make backend-race` 抓（T-044 review M1）。
- SoftwareDelivery：`templates/<template>.zh-Hant.names.json` 依 ID 給名稱，嚴格比對。
  - 少一個、多一個、空白或含控制字元的名稱、超過 IR 上限（200 字元）的名稱、未知 member 都是 internal error，避免某語言混雜兩種語言；不會變成表示「呼叫者輸入有誤」的 `InvalidError`（T-044 review L6）。
  - ID 與其他內容在各語言相同。
- `CreateModel` request 加 optional `locale`（省略即 `en`）。建立 model 的 handler（T-012／T-013）傳入它。建立後的名稱是使用者資料，切換語言不會改。

### Lint（`apps/web/eslint.config.mjs`，對 `**/*.tsx`，tests 除外）

- `react/jsx-no-literals`：noStrings、ignoreProps；允許空白與少數標點。它只看 JSX 中裸露的字串。
- `no-restricted-syntax`（T-044 review M3 擴大）：
  - 「含字母」以 `\p{L}` 判斷（任何文字，含重音字母與 CJK 擴充區）。
  - 文字 prop：`aria-label`、`aria-description`、`aria-placeholder`、`aria-roledescription`、`aria-valuetext`、`title`、`alt`、`placeholder`、`label`，以及元件常用的 `text`、`message`、`description`、`heading`、`caption`、`tooltip`、`hint`、`helperText`、`errorMessage`。它們的含字母字串值都擋，經最多三層「傳遞」也擋。傳遞指條件 `&&`／`||`／`??`／`?:`、`+` 串接、`as`／`satisfies`／`!`、陣列。`alt=""`（裝飾圖）可以。
  - Element 或 fragment 的子 expression，經一到三層傳遞後的含字母字串或 template literal；callback 回傳的字串（`xs.map(() => "item")`）也算。
  - `<input type="submit|button|reset" value="…">`；JSX prop 中物件的文字欄位（`problem={{ message: "…" }}`）。
  - `document.title = …`（用 metadata）。
- `no-restricted-globals`／`no-restricted-properties`：`alert`、`confirm`、`prompt`（含 `window.`），改用頁內 dialog。
- 函式呼叫不是傳遞：`t(a ? "k.x" : "k.y")` 的 key、`onClick={() => set("edit")}` 的參數、`m === "edit"` 的比較都不算。
- `tests/lint-guards.test.ts` 對每類寫法有正反例，mutation 證明每個 selector 都有測試擋。
- **限制**（需人工 review）：
  - 在 JSX 外組好的字串（`const s = "Hello"; … {s}`），以及經函式呼叫才進 JSX 的字串（`String("x")`、`label("x")`、`.join("and")`）；
  - 其他名稱的文字 prop；
  - 四層以上的傳遞；
  - 只有符號、沒有字母的文字（`→`、`©`）；
  - `lib/` 內產生的英文診斷（刻意保留為 technical message）；
  - `metadata` 物件。

### 錯誤與找不到的頁面（T-044 review M2）

- `app/not-found.tsx`：任何沒有頁面的網址，以讀者的語言顯示（含 `<title>`）。Next.js 內建的 404 頁只有英文，且會標成 `lang="zh-Hant"`。
- `app/error.tsx`：route 渲染失敗時，在 root layout 內以 catalog 文字顯示，可重試；有 server 的 `digest` 就顯示為參考編號，不顯示 error message（可能含內部資訊）。
- `app/global-error.tsx`：root layout 本身失敗時取代整份文件。它沒有 server 的 locale，先以英文渲染，瀏覽器讀到 cookie 後換成記住的語言（`useRememberedLocale`）。

### 不在本 ADR

- Pack 的 `NodeCatalog`／`ResourceCatalog` 的 `Label`／`Description` 目前是英文 pack 資料。T-015 palette 應依 kind 由 UI catalog 顯示兩語（follow-up）。
- AI 文字依使用者語言輸出：T-025／T-030，受 D-018 與資料政策 gate。

## Consequences

- 沒有新依賴與 license 變化；翻譯與格式化函式約 70 行，由 unit 與 mutation tests 保護。
- zh-Hant 文案由 agent 撰寫，沒有母語審稿。正式 release 前建議 owner 或母語者審閱 `messages/zh-Hant.ts`。
- 所有頁面 dynamic rendering；部署與 CDN 快取（T-039）須以 cookie 區分，或不快取 HTML。
- TEST-031 只由 T-044 完成 catalog、lint、切換與保存、Problem／validation code、Intl 部分。以下由後續任務完成：
  - 「不需重新登入」：T-043，屆時登入 session 才存在；
  - AI prose：T-025／T-030；
  - E2E 主流程兩語各跑一次：T-040。
