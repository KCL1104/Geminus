# ADR-021 — UI 函式庫與 design system（T-045）

狀態：ACCEPTED（T-045 agent 依 survey 與三個 spike 提出；owner 於 2026-10-18 採用全部建議，H-009）。日期：2026-10-18。依據：REQ-022、REQ-023、TEST-033、H-007、H-008、ADR-016、ADR-020；`checks/evidence/2026-10-18-T-045/` 的 `survey.md`、`spike-spec.md`、`spike-1-react-aria.md`、`spike-2-base-ui-tailwind.md`、`spike-3-mantine.md`。

## 背景

H-007 要求比較多個 UI 函式庫，不直接採用 Tailwind＋shadcn/ui。Survey 比較了 headless primitives、styled 元件庫、企業 design system 與 styling 方式，選出三組 spike。三組在同一頁 `/spike` 做相同內容：表單（ValidationIssue 錯誤、409 衝突）、200／5,000 列的錯誤表格（排序、選取、鍵盤、空與錯誤狀態）、確認 dialog 與成功訊息、React Flow 小畫布；量測相同的 9 項（`spike-spec.md`）。三組都遵守 H-008：只用系統字型、淺色主題、無付費授權、無安裝時腳本或 telemetry，版本皆於 2026-10-04 前發布。

## 比較（量測，機器負載 3–14，時間只能相對比較）

| 項目 | 1. React Aria Components 1.21.1＋CSS Modules | 2. Base UI 1.8.0＋Tailwind 4.3.3＋TanStack Table 9.2.4／Virtual 3.14.13 | 3. Mantine 9.6.3 |
|---|---|---|---|
| axe（WCAG 2.0–2.2 A／AA）瀏覽器 13 個狀態 | **0 violation** | **0 violation**（happy-dom 的 dialog 有 1 個 `aria-hidden-focus`，Chrome 為 incomplete） | **0 violation** |
| 純鍵盤流程 | **66／66**＋5,000 列 7／7 | **53／53**（200 與 5,000 列） | 29／32（React Flow Escape 丟焦點；通知遮住焦點 WCAG 2.4.11；Tab 落到滑出中的通知） |
| 表格的無障礙 | **函式庫內建**：完整 grid 鍵盤模型、排序、選取、虛擬化 | **自寫**：約 110 行，只涵蓋基本 APG grid（無範圍選取、type-ahead、編輯、欄寬） | **自寫**：256 行，另加 TanStack Virtual |
| `/spike` First Load JS（gzip，KiB） | 314.0 | **287.0** | ≈289.4（另外每個 route 多 ≈28 KiB JS） |
| 比 `/` 多出的 JS（gzip） | 170.2 KiB（React Flow ≈55.5、其餘 React Aria＋我們的程式；另有 ≈14.6 KiB 未用的語系可去除） | ≈143.4 KiB（Base UI ≈57、React Flow ≈55、TanStack ≈21） | ≈146.7 KiB |
| `/spike` CSS（gzip） | **5.6 KiB** | 6.8 KiB | ≈38 KiB（每個元件分開 import 時 ≈17） |
| 排序 5,000 列（虛擬化）中位數 | 257 ms | **46.9 ms** | ≈70 ms |
| 排序 200 列（未虛擬化） | 825 ms | **86.8 ms** | 175–230 ms |
| 我們寫的程式（`ui/`） | 553 行 TS＋673 行 CSS | 686 行 TSX＋174 行 CSS（＋61 行 globals） | 401 行 TS＋236 行 CSS |
| lint 例外 | **0** | 1（`useVirtualizer`） | 1（`useVirtualizer`） |
| 無法以 prop 覆寫的函式庫字串（zh-Hant） | 5 種 React Aria 繁中字串（見下，需 H-008 例外清單） | 0 | 0（但 close button 預設沒有名稱，漏傳就無名稱） |
| Root layout 影響 | 一個 client provider（zh-Hant→zh-TW 對應，有測試） | 無 | `ColorSchemeScript` inline script（日後 CSP 需 nonce）、無 JS 時控制項失去樣式、`suppressHydrationWarning` |
| 授權 | Apache-2.0 | MIT；Tailwind 4 建置時需要 lightningcss（MPL-2.0，不進 bundle） | MIT |
| 主要風險 | 1.21.1 在 server 建大型 collection 為 O(n²)（5,000 列 1 分 51 秒；已改為 hydration 後才渲染長表格）；`UNSTABLE_Toast` 不用 | TanStack Table v9 很新（多數範例仍是 v8）；Base UI 每月發版；grid 無障礙全由我們維護、未經螢幕報讀器驗證 | 單一維護者、每年一個 major；我們的 density／focus 覆寫依賴內部變數名；之後要日期元件需 dayjs |

三組共同的發現（與選擇無關）：

- React Flow 12.12.0：Escape 會讓節點失焦（三組都在 wrapper 修正）、edge 在 Tab 順序中先於 node、`<Controls>` 的 `aria-label` 沒有 role、連接點 6–8 px（低於 24 px）、說明文字在 hydration 前是英文、**署名連結 "React Flow" 是唯一無法翻譯的字串**（只能以 `proOptions.hideAttribution` 隱藏，MIT 允許，但廠商請未訂閱 Pro 者保留）。
- 390 px 時表格在自己的框內橫向捲動、畫布字太小：需要窄螢幕的表格與畫布設計（T-015）。
- Density 設在頁面 wrapper 時到不了 portal（dialog、下拉、通知），要設在 `<html>`。
- axe-core 與 @axe-core/playwright 為 MPL-2.0（弱 copyleft、只在開發與測試使用、不隨產品發布）；TEST-033 的自動檢查需要它們。
- Next.js 16.4.1 的 `next dev` 會在 `apps/web` 寫入 `AGENTS.md`，除非設 `agentRules: false`：會與 repo 的 AGENTS.md 混淆，不論選哪組都要設定。
- 螢幕報讀器（VoiceOver＋Safari、NVDA＋Firefox）三組都 NOT RUN，需要人執行。

## 建議（agent 的判斷）

**選 1：React Aria Components 1.21.1＋CSS Modules＋CSS custom-property tokens。**

- Survey 預先定的規則：「除非 1 在 axe／鍵盤上失敗而其他組沒有，或其 route JS 超過最佳者約 1.5 倍且沒有無障礙上的好處，否則選 1」。1 沒有任何歸因於 React Aria 的 axe 或鍵盤失敗；JS 為最佳者的 1.09 倍（314.0／287.0）。
- P0 的畫面以資料密集元件為主（import 錯誤表、proposal 審閱、run 比較、編輯器屬性面板），表格與 grid 的無障礙正是最容易出錯的地方。1 由函式庫提供完整的 grid 鍵盤模型與虛擬化；2 與 3 都要我們自己寫並長期維護，且未經螢幕報讀器驗證。
- 1 的代價與對策：
  - CSS 全部自己寫（我們的 tokens 本來就要自訂外觀）；
  - 未虛擬化的表格排序慢 → 超過約 50 列一律虛擬化；
  - 大型 collection 的 server render 慢 → 長表格只在 client 渲染；
  - 未用語系 ≈14.6 KiB → `@react-aria/optimize-locales-plugin` 2.1.0（2026-10-22 起符合兩週規則）確認支援 Turbopack 後加入；
  - 5 種函式庫繁中字串 → 依 H-008 列清單、審閱、以 Playwright 測試列出。
- 若 owner 更重視 Tailwind／shadcn 生態與 token 強制、願意自己維護 grid 無障礙，選 2；若更重視「立刻有完整外觀」而接受較大的 CSS 與 root layout 的 inline script，選 3。

## Owner 的決定（H-009）

1. **選 1：React Aria Components 1.21.1＋CSS Modules＋CSS custom-property tokens。**
2. 下列 React Aria 繁中字串依 H-008 使用函式庫翻譯（列清單、審閱，並以 Playwright 測試列出）：可排序的欄、已依據「…」欄遞增／遞減排序、已選取「…」。、關閉（Select 的隱藏關閉鈕）、數字欄位（不覆寫，保留 React Aria 在 iOS 的處理）。
3. axe-core 與 @axe-core/playwright（MPL-2.0）只作測試與開發工具，不隨產品發布。
4. 保留 React Flow 署名（不設 `proOptions.hideAttribution`）。
5. 成功回饋用頁內 `role="status"` 訊息，不用 toast。

## 選定後的實作（T-045 其餘 scope）

Design tokens（顏色、系統字型與繁中 fallback、間距、density、focus ring；深色預留）、app shell 與基本元件（button、field、select、dialog、status、table，以及 loading／empty／error／權限不足／409 狀態）套用到 T-014／T-044 的畫面；`agentRules: false`；axe 進 vitest 與 Playwright、純鍵盤 E2E、en／zh-Hant × 390／1280 截圖比對（baseline 變更需人工確認）；T-015 與 T-031 的 low-fi prototype 交 owner review。Spike 的程式只在 scratch，不直接搬進 repo，依選定的組重寫並先寫測試。

## 實作紀錄（T-045）

- 2026-10-19 安裝於 `apps/web`（`package-lock.json`）：
  - `react-aria-components` 1.21.1（Apache-2.0，2026-09-04）為唯一新增的 production 依賴，連同 `react-aria` 3.52.1、`react-stately` 3.50.0、`@internationalized/*`、`aria-hidden`、`clsx`、`use-sync-external-store`，共 10 個 lock entry，皆無 install script。
  - Overrides：`@internationalized/number` 3.6.8、`@react-types/shared` 3.36.1。React Aria 的範圍會解析到 2026-10-08 發布的 3.6.9／3.37.0，未滿兩週。
  - `axe-core` 4.13.0（MPL-2.0，dev）：只給 vitest 的無障礙檢查用；既有的 4.14.0（eslint-plugin-jsx-a11y 帶入）一併降到 4.13.0。
  - `npm audit --omit=dev`：0 vulnerability。
- 瀏覽器 E2E（`@playwright/test` 1.56.1，與機器上的 Chromium 相同版本；`@axe-core/playwright` 4.13.0）放在獨立的 npm 專案，不放進 `apps/web`。原因有二：Next.js 16.4.1 對 `@playwright/test` 宣告了 optional peer dependency，lockfile 因此把它標為非 dev，`npm ci --omit=dev` 會把它裝進 production；`@axe-core/playwright` 也會帶入未滿兩週的 playwright-core 1.64.1。
- `next.config.ts` 設 `agentRules: false`。
- Tokens 在 `apps/web/ui/tokens.css`，元素預設在 `ui/base.css`（`@layer base`）。`tests/tokens.test.ts` 檢查 17 組文字與 6 組非文字的對比、只用語意 token、只用系統字型、compact density 仍為 24 px。`UiProvider`（`ui/provider.tsx`）把 `zh-Hant` 對應為 React Aria 的 `zh-TW`（`tests/ui-provider.test.tsx`）。

## 套用 Marey design system（T-045 re-skin，H-013、H-027，2026-10-26）

Owner 於 H-027 採用 DESIGN.md（Marey 圖表時刻表）為設計基礎，條件是介面簡潔、只保留每個畫面需要的控制項。本 ADR 的選擇（React Aria Components＋CSS Modules＋custom-property tokens）不變；變更的是 tokens 與元件外觀：

- **Tokens**：`ui/tokens.css` 改為 DESIGN.md 的值，一個 key 一個 `--gm-color-<key>`（淺色與灰黑深色）；字級只有五個（`--gm-font-size-label|body|title|headline|display`）、間距與圓角照 DESIGN.md 的 scale。原本的 palette 層（`--gm-palette-*`）與藍色 accent 移除：DESIGN.md 的 key 本身就是語意名稱，再包一層沒有好處。`tests/tokens.test.ts` 逐值比對 DESIGN.md，`tests/design-rules.test.ts` 擋任何 token 以外的顏色、字級、間距、圓角、陰影與 transition。
- **主題**（H-011 允許深色，H-013 預設跟隨系統、可切換並記住）：選擇存在 cookie `geminus_theme`（只有 `light` 或 `dark`；沒有 cookie 即跟隨系統；`Path=/; Max-Age=31536000; SameSite=Lax`，https 加 `Secure`，不設 HttpOnly，不含身分、不參與授權，與 ADR-020 的語言 cookie 同一模式）。Root layout 在 server 端讀 cookie，輸出 `<html data-theme>`，所以第一次繪製就是正確的主題，不需要在 paint 前執行的 inline script（日後嚴格 CSP 不必為它開 nonce 或 hash）。選擇時 client 直接改 `data-theme`，不需重新渲染。選 localStorage 會需要 paint 前的 inline script，才不會閃，因此不用。
- **字型**（H-011 允許 OFL web font，取代 H-008 (3)）：`@fontsource-variable/archivo` 5.3.0 與 `@fontsource-variable/noto-sans-tc` 5.3.0（版本與授權見 ADR-011、THIRD_PARTY_NOTICES）。`ui/global-styles.ts` import 套件內的 CSS，字型檔由 build 放進 `/_next/static/media`，從 app 自己的 origin 載入；Noto Sans TC 依 unicode-range 分檔，頁面只下載用到的字。沒有改用 `next/font/google`：它在 build 時連 Google Fonts。
- **語言與主題的選擇**改用本系統的 `Select`（原本語言是原生 `<select>`）：與表單的單選同一個元件；方向鍵打開清單、只有選定才換語言（WCAG 3.2.2；impeccable audit 2026-10-19 的 P1：原生 select 在方向鍵就換語言，且 `key={locale}` 重建元件讓焦點掉到 body，現在焦點留在控制項上）。
- **元件**：button（primary 為 Graphite Ink；danger 為預設外框加紅字；disabled 為 Inset Well）、field（34 px、15 px 字、invalid 為紅框加內環）、select、checkbox（焦點環只畫一圈）、dialog（560 px、8 px 圓角、hairline 分隔、12vh）、callout（edge＋wash、墨色文字，去掉 4 px 側邊條）、status（Outcome 框）、table（12 px 600 表頭、Control Edge 表頭線、無列 hover）、shell（48 px app strip）。Problem 的技術訊息與 request ID 收進「Details」展開（H-027 漸進揭露）。`CrashNotice` 改用 `Button`；`global-error` 自己載入全域樣式與記住的主題。
- **已知的簡化與不做**：overlay 不做進場動畫；note 先不加圖示；app strip 沒有 Find、帳號與模式切換（尚無功能）。理由與檢查見 DESIGN.md 的 Simplicity 一節。

## React Flow 的包裝（T-015 hackathon 切片，2026-10-26）

ADR-007 指定 React Flow 為 2D authoring adapter；本 ADR 的 spike 用的 `@xyflow/react` 12.12.0 即正式版本（版本、日期與授權見 ADR-011、THIRD_PARTY_NOTICES）。Spike 的共同發現在 `apps/web/features/model-editor/` 這樣處理：

- **樣式**：只用 `base.css`（結構），以 `@import … layer(vendor)` 放進 vendor layer（build 後的 CSS 確認在 `@layer vendor` 內）；外觀由 `canvas.module.css` 以 tokens 設定 React Flow 的 CSS 變數與 node／edge／attribution。不用 `<Controls>`、`<MiniMap>`、`<Background>`（無點狀格線，DESIGN.md）。
- **字串**：`ariaLabelConfig` 全部取自 catalog（node 與 edge 的說明、移動時的 aria-live、handle）；React Flow 寫死的英文 `aria-roledescription`「node」「edge」與 edge 的預設名稱以 `domAttributes`、`ariaLabel` 換成 catalog 文字（由 `strings.spec.ts` 的編輯器測試抓到「edge」後修正）。**署名保留**（H-009 (4)）：連結文字「React Flow」與它的 `aria-label`「React Flow attribution」是畫布上唯二無法翻譯的字串，`strings.spec.ts` 只接受這兩個並列出它們。
- **鍵盤**：Escape 原本會取消選取並讓 node 失焦 → editor 在 capture 階段攔下，只取消選取、焦點留在 node；Delete 由 editor 處理並先確認（React Flow 的 `deleteKeyCode` 關閉：它聽整個 document，焦點在屬性面板時也會刪掉選取的 node）；edge 不可聚焦也不可選取，Tab 不先停在 edge 上，連接改在步驟的屬性中（選單）或拖曳 handle 完成；node 依閱讀順序（左到右、上到下）排列，Tab 順序因此合理。
- **受控模式**：nodes／edges 每次由 IR 產生（`flow.ts`），React Flow 只回報變更；node 量到的大小存在 canvas 元件內再交回，否則每次 render 都會重新量測。拖曳中的位置只在畫面，放開或方向鍵才成為 JSON Patch。
- **已知限制**：每個步驟是一個 Tab stop（範例產線約 25 個），鍵盤使用者從畫布到屬性面板要經過其餘步驟；之後可考慮 roving tabindex 或「跳到屬性」的連結（T-015 完整範圍）。
