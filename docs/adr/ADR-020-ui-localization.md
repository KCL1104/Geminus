# ADR-020 — UI 語言：自寫 typed catalog、cookie 語言偏好、template 兩語預設名稱（T-044）

狀態：ACCEPTED（T-044 agent 選定並先實作；owner 於 2026-10-18 批准，並決定中文先只做繁體 `zh-Hant`，H-006）。日期：2026-10-18。需求：REQ-022（owner 新增，H-005）、REQ-004。T-044 scope 要求「i18n 機制（自寫或 library，另立 ADR、版本釘住）」。

## Decision

### Catalog 與翻譯函式（`apps/web/lib/i18n/`、`apps/web/messages/`）

- **自寫**，不加依賴。Catalog 是 TypeScript object：`messages/en.ts`（`as const`）定義 key；`messages/zh-Hant.ts` 型別為 `Catalog`，所以少 key、多 key 或把複數訊息寫成字串都會編譯失敗。
- `createTranslator(locale)`：
  - 插值 `{name}`；數字參數依 locale 用 `Intl.NumberFormat` 格式化。
  - 複數訊息是 `{ one?, other }`，依 `params.count` 用 `Intl.PluralRules` 選；zh-Hant 只有 `other`。
  - 缺參數或 `count` 不是數字時 throw：這是程式錯誤，不顯示 `{name}`。
- 日期、數字、期間：`formatDateTime`（只接受 RFC 3339 date-time，即 contract 的格式；`Date.parse` 接受的範圍大得多）、`formatNumber`、`formatDuration`（`Intl.NumberFormat` unit style）。
  - 元件經 `useI18n().dateTime` 使用：API 給的值若不是 date-time，就照原樣顯示，不讓整頁失敗。
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
  - 未知 locale 或 template 只回 error。
- SoftwareDelivery：`templates/<template>.zh-Hant.names.json` 依 ID 給名稱，嚴格比對。
  - 少一個、多一個、空名或未知 member 都是 internal error，避免某語言混雜兩種語言。
  - ID 與其他內容在各語言相同。
- `CreateModel` request 加 optional `locale`（省略即 `en`）。建立 model 的 handler（T-012／T-013）傳入它。建立後的名稱是使用者資料，切換語言不會改。

### Lint（`apps/web/eslint.config.mjs`，對 `**/*.tsx`，tests 除外）

- `react/jsx-no-literals`：noStrings、ignoreProps；允許空白與少數標點。
- `no-restricted-syntax` 擋兩類寫法：
  - 文字 attribute（`aria-label`、`aria-description`、`aria-placeholder`、`aria-roledescription`、`aria-valuetext`、`title`、`alt`、`placeholder`、`label`）的字串、字串 expression 或 template literal；
  - 由條件（`&&`、`||`、`?:`，最多三層）在 element 或 fragment 直接渲染的含字母或中文字串與 template literal。
  - `t(a ? "k.x" : "k.y")` 的 key 是參數，不算。
- `tests/lint-guards.test.ts` 對每個 selector 有正反例。
- **限制**（需人工 review）：
  - 在 JSX 外組好的字串（`const s = "Hello"; … {s}`）；
  - `lib/` 內產生的英文診斷（刻意保留為 technical message）；
  - 四層以上的條件；
  - `metadata` 物件。

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
