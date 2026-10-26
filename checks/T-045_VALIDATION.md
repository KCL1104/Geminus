# T-045 驗證報告：Design system、UI 函式庫評估與 UX gate

**日期：2026-10-19｜狀態：`in_progress`（實作與自我驗證完成；截圖 baseline 已產生並提交，CI 開始比對；prototype 與 baseline 待 owner 確認；之後送獨立 review，依 H-003 驗收）｜需求：REQ-022、REQ-023｜驗收測試：TEST-033｜決策：ADR-021 ACCEPTED（H-009）、H-007、H-008**

**2026-10-26 更新**：依 H-027 套用 Marey design system 並保持簡潔，見文末「Re-skin：套用 Marey design system，保持簡潔」一節；在新 baseline 由 CI 產生並經人確認之前，下方「截圖 baseline」一節描述的是 re-skin 之前的 baseline。

範圍依 tasks.json：比較多個 UI 函式庫並以 spike 決定（ADR-021）；design tokens、app shell 與基本元件，套用到 T-014／T-044 的畫面；每個 UI 任務先交 prototype 經 owner review，本任務以 T-015 與 T-031 的 low-fi prototype 驗證流程；自動檢查（axe、純鍵盤 E2E、en／zh-Hant × 390／1280 截圖比對，baseline 變更需人工確認）納入 make 與 CI。

## 交付內容

| 位置 | 內容 |
|---|---|
| `docs/adr/ADR-021-ui-library-and-design-system.md` | 三個 spike 的比較與 owner 的選擇（React Aria Components 1.21.1＋CSS Modules＋tokens；5 種 React Aria 繁中字串；axe 只作測試工具；保留 React Flow 署名；成功回饋用頁內 status）；實作紀錄（版本、overrides、為何 E2E 另立專案） |
| `checks/evidence/2026-10-18-T-045/` | survey 與三個 spike 的報告（spike 程式只在 scratch，不搬進 repo） |
| `apps/web/package.json`、`package-lock.json` | `react-aria-components` 1.21.1（唯一新增的 production 依賴，10 個 lock entry，皆 ≥ 兩週、無 install script）；overrides `@internationalized/number` 3.6.8、`@react-types/shared` 3.36.1；`axe-core` 4.13.0（dev）；`start` script；`npm audit --omit=dev` 0 |
| `apps/web/next.config.ts` | `agentRules: false` |
| `apps/web/ui/tokens.css`、`base.css` | 調色盤與語意 token（只用淺色；深色只需重定義語意 token）、系統字型與繁中 fallback、間距、density（設在 `<html>`，portal 也吃得到）、focus ring；元素預設在 `@layer base` |
| `apps/web/ui/*.tsx` | `UiProvider`（zh-Hant → React Aria 的 zh-TW）、`Button`、`Callout`、`StatusRegion`、`TextField`（只顯示 server 的驗證，`validationBehavior="aria"`）、`Checkbox`、`Select`、`ConfirmDialog`（alert dialog，焦點先在取消）、`DataTable`（React Aria grid；超過 50 列虛擬化，且只在 hydration 後 render；選取 checkbox 的名稱來自 catalog）；`shell.module.css`、`list.module.css` |
| `apps/web/app/` | app shell（skip link、header、nav、單一 main）；ProblemView、重試、提示與草稿動作改用元件；列表樣式；`/ui-gallery`（每個元件與頁面狀態，只在 `GEMINUS_UI_GALLERY=1` 時開放，否則 404） |
| `apps/web/messages/*.ts` | skip link、表格選取、gallery 的 catalog 字串（en／zh-Hant） |
| `apps/web/tests/` | `tokens.test.ts`、`ui-provider.test.tsx`、`ui-components.test.tsx`、`gallery.test.tsx`、`ui-harness.tsx` |
| `apps/web-e2e/` | 獨立的 npm 專案（`@playwright/test` 1.56.1、`@axe-core/playwright` 4.13.0）：`a11y`、`keyboard`、`strings`、`screenshots` 四組瀏覽器檢查；README 說明 baseline 流程 |
| `apps/web-e2e/tests/__screenshots__/` | 16 張截圖 baseline（4 個畫面 × en／zh-Hant × 390／1280），由 CI 固定容器產生（見「截圖 baseline」） |
| `Makefile` | `make e2e-test` 執行瀏覽器檢查，並說明沒涵蓋的部分（本機的截圖、T-040 的產品閉環） |
| `.github/workflows/ci.yml` | e2e job 在以 digest 固定的 `mcr.microsoft.com/playwright:v1.56.1-noble` 中執行；`workflow_dispatch` 的 `update_screenshots` 只產生 baseline 並上傳為 artifact，不會自動提交 |
| `docs/adr/ADR-011-toolchain-bootstrap.md` | 瀏覽器檢查工具、容器 image 與 `actions/upload-artifact` v7.0.1（SHA pin）的紀錄 |
| `checks/evidence/2026-10-19-T-045/prototypes/geminus-prototypes.html` | T-015 編輯器與 T-031 匯入／AI 建議審閱的 low-fi 可點擊 prototype |

## 實際執行結果

| 指令 | 結果 |
|---|---|
| `npm run lint`、`npm run typecheck`（apps/web） | exit 0 |
| `npx vitest run`（apps/web） | 14 個檔案、211 個 test 全過 |
| `npx next build` | 成功；`/ui-gallery` 為 dynamic route |
| `make e2e-test`（本機，Chromium 1.56.1） | exit 0：37 passed、16 skipped（截圖 NOT RUN：只在 CI 的固定容器比對）；並印出未涵蓋的部分 |
| CI e2e job（ce9768d，固定容器） | 37 passed、16 skipped（當時還沒有 baseline，截圖 NOT RUN） |
| CI 手動 run 39183828451（5ed0c78，`update_screenshots`） | 5 個 job 全過；產生第一組 baseline，檢視時發現下述第 4 個問題，未採用 |
| CI 手動 run 39184979891（8a3e89a，`update_screenshots`） | 5 個 job 全過（含新的表格檢查）；產生的 16 張即提交的 baseline |
| CI run 39185593556（c33717f，提交 baseline 後） | 5 個 job 全過；browser checks 53 passed、0 skipped（截圖比對有執行） |
| 依賴稽核 | 新增套件的發布日期、授權、install script 逐一列出；`npm audit --omit=dev` 0 vulnerability |

### 自動檢查涵蓋的內容

- **axe-core（WCAG 2.0–2.2 A／AA）**
  - vitest（happy-dom，不含對比與目標大小）：每個元件、gallery 兩種語言。另有一個測試確認 axe 會抓到刻意放的違規（沒有名稱的按鈕）。
  - Playwright（含對比）：6 個 route × en／zh-Hant × 390／1280 px，以及 gallery 開著 status、listbox、dialog 的狀態。全部 0 violation，390 px 沒有頁面層級的橫向捲動。
- **只用鍵盤**：skip link 在第一個 Tab 且移焦點到 main；nav、語言選單的順序；以鍵盤換語言；status 訊息；dialog 的焦點在取消、被困在 dialog 內、Escape 回到開啟它的按鈕、確認後執行；select；表格只有一個 Tab stop、方向鍵移動、到 header 用 Enter 排序、Space 選取、Tab 離開；500 列的虛擬化表格可到第 501 列（`aria-rowcount` 501、DOM 中少於 60 列）。每一步都檢查 focus 可見（≥ 2 px outline）。
- **字串（H-008）**：gallery 在排序、選取、開著 select 與 dialog 時，每個看得到或聽得到的字串都必須來自 catalog、是資料，或是 owner 接受的 React Aria 字串。測試列出它找到的非 catalog 字串：
  - en：sortable column、sorted by column Path in ascending／descending order、「… selected.」、Dismiss；
  - zh-Hant：可排序的欄、已依據「路徑」欄遞增／遞減排序、已選取「…」。、關閉。
- **Tokens**：17 組文字（≥ 4.5:1）與 6 組非文字（≥ 3:1）對比；元件只用語意 token；只用系統字型；compact density 仍為 24 px。

### 瀏覽器檢查抓到並已修正的問題

1. 空表格取得焦點時沒有可見的 outline（`.table` 設了 `outline: none`）→ 加上 `[data-focus-visible]` 的 focus ring。
2. 選取一列時 React Aria 播報的是「已選取 1 個項目。」（項目計數，不在 owner 接受的清單中），因為 row 沒有 `textValue` → `DataTable` 要求 `rowText`，現在播報已接受的「已選取「…」。」。
3. 表格選取 checkbox 的「全選」「選取」是 React Aria 的字串，不在接受清單中 → 改用 catalog 文字。
4. （檢視第一組 baseline 時發現）390 px 的虛擬化長表格中，路徑文字畫到隔壁欄上：React Aria 的 TableLayout 讓各欄平分寬度，cell 不隨文字變寬 → `TableColumn.minWidth`（預設 120 px）設定虛擬化表格的最小欄寬，超出時表格在自己的框內橫向捲動；虛擬化的 cell 以刪節號截斷而不是溢出（列的 text value 仍有全文）；gallery 的欄寬容得下資料。新增瀏覽器檢查：每個 route、語言、寬度，表格 cell 都不得比其文字窄（溢出或被截斷都算）。修正前它在 390 px 兩種語言都失敗；拿掉 gallery 的欄寬時它回報被截斷的 cell。

## Prototype（owner review）

`checks/evidence/2026-10-19-T-045/prototypes/geminus-prototypes.html`，已傳給 owner；2026-10-19 依 owner 要求與截圖 baseline 一起整理成一個審閱頁（claude.ai artifact「Geminus UI 審閱」，private），內容為同一個檔案與 16 張 baseline，未改動。

- **模型編輯器（T-015）**：步驟面板、畫布（方向鍵移動、Enter 選取）、屬性面板（含 server 回傳的欄位錯誤）、驗證問題表（點選即跳到該步驟）、儲存草稿、有錯誤時無法發布、重設與離開的確認、409（保留使用者的變更，可重新載入後套用或捨棄）、檢視者唯讀、API 無法連線。
- **匯入與 AI 建議審閱（T-031）**：上傳、無效列與下載、欄位對應（建議需逐一確認）、確認匯入；建議的 diff、AI 的假設、server 計算的風險與 AI 的判斷並列、驗證、預期影響；修改模型與情境的批准分開，文字也不同；needs_input；base 過時時無法批准。
- 已檢查：Chromium 中三個畫面 × en／zh-Hant × 390／1280 皆 0 axe violation、沒有橫向捲動、沒有 JS 錯誤。
- Owner review 結果：**尚未收到**（待補；TEST-033 要求紀錄）。

## 待 owner 決定或知悉

- **Prototype 審閱**：流程與版面是否符合期待；回饋將記入本報告並帶入 T-015／T-031。
- **React Aria 選取通知的同類字串（H-010：接受）**：除了「已選取「…」。」，owner 也接受「未選取「…」。」「已選取所有項目。」「已選取 N 個項目。」（英文為 `… not selected.`、`All items selected.`、`N item(s) selected.`）。字串測試把它們列入接受清單，並實際操作表格：以鍵盤全選、取消一列（從全選離開時只播報剩下的數量）、再取消一列（播報該列與數量），檢查三句都出現過。React Aria 會把同一次變更的幾則通知合成一句（「未選取「…」。 已選取 3 個項目。」），測試因此逐句比對。拿掉「未選取「…」。」的接受項時，繁中的測試失敗（反向檢查）。同一則數量訊息在 0 個時的寫法「未選取任何項目。」不在 owner 接受的清單中，測試不觸發它；若之後出現，字串測試會擋下，再請 owner 確認。
- **截圖 baseline 的人工確認**：見下節；TEST-033 要求 baseline 變更由人確認，目前只有 agent 看過。

## 截圖 baseline

- 流程：CI 手動 run（`update_screenshots`）在固定容器產生 → 下載 artifact → 逐張檢視 → 提交到 `apps/web-e2e/tests/__screenshots__/`。之後每次 CI 都與它比對（`maxDiffPixelRatio` 0.002）；缺少 baseline 的新畫面會失敗。
- 兩次 run 之間沒有改動的 14 張逐位元組相同，只有修正過的 390 px gallery 兩張不同：容器內的 render 是確定的。
- Agent 的檢視：繁中字形正常（容器有 CJK 字型）；沒有重疊或截斷；390 px 時寬表格在自己的框內橫向捲動，頁面本身不橫向捲動；錯誤、警告、成功、資訊的顏色與文字一致；停用的「發布」按鈕顏色較淡（停用元件不受對比要求）。
- **Owner 確認：尚未收到**（已傳送截圖；TEST-033 要求紀錄）。若 owner 要求修改，以新的手動 run 重新產生。
- 提交 baseline 後第一次 CI 的比對：c33717f 的 run [39185593556](https://github.com/KCL1104/Geminus/actions/runs/39185593556) 的 browser checks job 53 passed、0 skipped，16 張截圖都與 baseline 比對通過；之後的 push 也都通過。

## NOT RUN／限制

| 項目 | 原因 |
|---|---|
| 螢幕報讀器（VoiceOver＋Safari、NVDA＋Firefox） | 需要人執行（ADR-021） |
| Firefox、Safari 的瀏覽器檢查 | P0 只跑 Chromium |
| 本機的截圖比對 | 字型與 CI 容器不同；只在 CI 的固定容器比對 |
| 產品的閉環 E2E（假 provider） | T-040 |
| React Flow 的包裝（Escape 失焦等 spike 發現） | T-015 實作畫布時處理 |
| 超過 50 列且可多選的 `DataTable` | 尚無呼叫者，未驗證：虛擬化時選取欄的寬度未設定。第一個這樣用的任務需補上並加入 gallery |
| 窄螢幕上寬表格的橫向捲動提示 | 只有被切掉的右緣；是否需要更明顯的提示列為 owner 審閱項目 |

## Re-skin：套用 Marey design system，保持簡潔（2026-10-26，H-027）

**狀態：實作與自我驗證完成（分支 `t045-reskin`，自 f4db5d6）；截圖 baseline 尚未重新產生（必須在 CI 的固定容器產生，見下），在那之前 CI 的截圖比對會失敗；待 coordinator 整合、CI 與獨立 review（H-003）。T-045 的 status 不變。**

### 依據

- H-013：Marey 圖表時刻表方向、scenario 深綠／baseline 石墨虛線、Archivo、深色灰黑、預設跟隨系統且可切換並記住；H-011：可用 OFL web font 與深色主題（放寬 H-008 (2)(3)）；H-012：桌機為主、手機只看結果與審核、深色不用藍；H-009／ADR-021：React Aria Components 1.21.1＋CSS Modules＋custom-property tokens 不變。
- H-027（owner，2026-10-26，coordinator 記錄中，依指示視為已批准）：「UI 都照建議，但我希望介面要是簡潔的」；Q1 以 Marey workspace 為設計基礎；Q2 舊 prototype 的模型編輯器與匯入／AI 審閱流程仍是 T-015／T-031 的基礎。同一決定的補充（coordinator 轉達）：「只保留所有應該要包含的按鈕功能等」。

### 「簡潔」的解讀與檢查（寫入 DESIGN.md 的 Simplicity 一節）

| 原則 | 本次怎麼做 | 檢查 |
|---|---|---|
| 1. 每個畫面一個主要動作 | Primary 只用 Graphite Ink；gallery 的停用「Publish」與「Save draft」改為預設樣式，gallery 也只有一個 primary | `tests/simplicity.test.tsx`（每種草稿狀態、gallery、失敗頁）、`apps/web-e2e/tests/simplicity.spec.ts`（每個 route × en／zh-Hant × 390／1280） |
| 2. 只保留需要的控制項（H-027 補充） | 每個控制項對應已實作且有測試的功能（下表）；移除專案頁重複的「All projects」；app strip 不放 Find、帳號、模式切換 | 產品 route 沒有停用的控制項、沒有兩個連到同一處的連結、沒有同名按鈕（唯一例外為待 owner 決定的一組） |
| 3. 漸進揭露 | Problem 的技術訊息與 request ID 收進「Details／詳細資料」；驗證細節與重試時間仍直接顯示 | `i18n-dom`／`ui` 測試仍讀得到全文；review |
| 4. 不裝飾 | 1 px hairline（去掉 callout 與 status 的 4 px 側邊條）；陰影只給 popover、dialog 與 invalid 內環；只有 180 ms 的狀態顏色轉換，沒有 overlay 進場動畫；顏色只有中性色、scenario 綠與 warning／error | `tests/design-rules.test.ts`、`tests/tokens.test.ts` |
| 5. 版面安靜、文字簡短 | 一個 main、沒有側欄；內容與 app strip 左緣對齊；間距只用 DESIGN.md 的 scale；文案去掉 task ID | `layout.test.tsx`、`simplicity.spec.ts`、`design-rules.test.ts` |

DESIGN.md 中比上述原則繁複的元件，app 採較簡單的做法並記錄理由（DESIGN.md「Simplifications in the app」）：語言與主題都用本系統的 `Select`（不是語言 segmented control 加循環的主題按鈕）；overlay 沒有進場動畫；note 先不加圖示；表格列沒有 hover 底色；頁面標題用 Headline／Title／Body；動作結果是 Outcome 框。

### 變更內容

| 位置 | 內容 |
|---|---|
| `apps/web/ui/tokens.css` | DESIGN.md 的所有顏色（`--gm-color-<key>`，淺色與灰黑深色，深色在 `prefers-color-scheme: dark` 與 `[data-theme="dark"]` 兩處，值相同）、五個字級、間距與圓角 scale、overlay 陰影、motion、focus ring（2 px Graphite Ink）；移除舊的 palette 層與藍色 accent |
| `apps/web/ui/base.css`、`ui/global-styles.ts` | 元素預設（白色 sheet、tabular figures、連結為墨色底線、`code` 不用等寬字、`summary`）；字型由 `@fontsource-variable/archivo` 5.3.0（standard：字重＋字寬軸）與 `@fontsource-variable/noto-sans-tc` 5.3.0（字重，依 unicode-range 分檔）自架 |
| `apps/web/ui/*.module.css`、`ui/select.tsx` | 依 DESIGN.md 重做 button、field、select、checkbox、dialog、callout、status、table、list、shell；`Select` 加 `variant="strip"`（12 px 小標籤，≤720 px 只隱藏視覺；關閉時 ArrowLeft／ArrowRight／type-ahead 不會直接改值） |
| `apps/web/lib/theme/`、`app/_components/theme-switcher.tsx`、`app/layout.tsx`、`app/global-error.tsx` | 主題：cookie `geminus_theme`（`light`／`dark`；無 cookie 即跟隨系統），server 輸出 `<html data-theme>`，第一次繪製即正確，不用 inline script；選擇時直接改 `data-theme`；global-error 也讀記住的主題並載入全域樣式 |
| `app/_components/locale-switcher.tsx` | 語言選擇改用 `Select`：方向鍵只開清單、選定才換；元件不再以 `key` 重建，焦點留在控制項上（impeccable audit 2026-10-19 的 P1） |
| `app/_components/problem-view.tsx`、`crash.tsx` | 技術細節收進 `<details>`；`CrashNotice` 用 `Button` |
| `app/projects/[projectId]/page.tsx`、`app/ui-gallery/gallery.tsx` | 移除重複的「All projects」；gallery 只留一個 primary |
| `apps/web/messages/*.ts` | 新增 `theme.*`、`problem.details`；刪除 `models.allProjects`；`model.notInBuild` 去掉 task ID（en：「The 2D editor and 3D view are not part of this build.」／zh：「此版本不含 2D 編輯器與 3D 檢視。」）；`gallery.intro` 縮短 |
| `apps/web/package.json`、`package-lock.json` | 兩個字型套件（OFL-1.1，2026-07-19 發布，各一個 lock entry，無依賴、無 install script） |
| `apps/web/tests/` | `tokens.test.ts` 改寫（逐值比對 DESIGN.md、兩個主題的對比、深色零彩度、色相只限三族、字級／間距／圓角 scale、字型自架與授權）；新增 `design-rules.test.ts`、`simplicity.test.tsx`、`theme.test.tsx`；更新 `layout.test.tsx`、`i18n-dom.test.tsx`、`ui-components.test.tsx` |
| `apps/web-e2e/tests/` | `a11y.spec.ts` 加深色（系統偏好）；`keyboard.spec.ts` 更新 shell 順序並加主題；`screenshots.spec.ts` 加深色（`-dark`）；新增 `theme.spec.ts`、`simplicity.spec.ts`；`helpers.ts` 等字型載入 |
| `DESIGN.md`、`.impeccable/design.json` | 狀態改為 owner 採用（H-027）；Simplicity、Simplifications in the app、Tokens in the app 三節 |
| `docs/adr/ADR-021`、`ADR-020`、`ADR-011`、`docs/THIRD_PARTY_NOTICES.md` | re-skin 的實作紀錄；語言切換器的描述；字型套件的版本與授權；字型的 copyright 與 OFL 全文 |
| `checks/evidence/2026-10-26-T-045-reskin/local-shots/` | 本機預覽截圖 32 張（**不是 baseline**，見下） |

### 被更新的既有測試（理由）

- `tokens.test.ts`：原本要求「只有淺色、只用系統字型、語意 token 指向 palette」，這些是 H-008 (2)(3) 的限制，H-011／H-013 已放寬；改為檢查 DESIGN.md 的值與兩個主題。對比檢查保留且擴大（每個主題文字 28 組、非文字 11 組；原本只有淺色 17＋6 組）。
- `layout.test.tsx`、`i18n-dom.test.tsx`、`keyboard.spec.ts`：語言選擇由原生 `<select>` 改為 design system 的 `Select`，斷言改為新的結構（名稱為「值＋標籤」，例如「English Language」）；鍵盤測試的 Tab 順序多了主題，並新增「關閉時方向鍵與輸入字母不換語言、選定後焦點留在控制項」的檢查。沒有刪除或放寬任何斷言。

### 每個畫面的控制項（H-027 補充要求）

所有畫面共用的 app strip：

| 控制項 | 用途 | 依據 |
|---|---|---|
| Skip to main content（連結，Tab 時才出現） | 跳到 main | REQ-023、TEST-033；`keyboard.spec.ts` |
| Projects（導覽連結） | 開專案列表 | T-014 shell；`layout.test.tsx` |
| Language（Select） | en／zh-Hant，記在 cookie | REQ-022、ADR-020、TEST-031；`layout.test.tsx`、`i18n-dom.test.tsx`、`keyboard.spec.ts` |
| Theme（Select） | 跟隨系統／淺色／深色，記在 cookie | H-013；`theme.test.tsx`、`theme.spec.ts`、`keyboard.spec.ts` |

| 畫面 | 控制項 → 用途 | 依據 |
|---|---|---|
| `/` | Go to projects → 專案列表（**與 strip 的 Projects 重複，保留，請 owner 決定**） | T-044；`layout.test.tsx`、`keyboard.spec.ts` |
| `/projects` | 每個專案名稱（連結）→ 開專案；Try again（可重試的失敗時）→ 重新載入 | T-014；`dom.test.tsx`；未登入時只顯示「需要登入」，沒有控制項 |
| `/projects/[id]` | 每個模型名稱（連結）→ 開模型；Try again | T-014；`ui.test.tsx`、`dom.test.tsx` |
| `/models/[id]` | Back to project → 回到該專案（strip 沒有這個目的地）；衝突時 Reload the current draft（primary）與 Discard my changes（danger）；Try again | T-014、AGENTS §2（409 不 silent overwrite，不自動 rebase）；衝突的兩個動作由 `ui.test.tsx`、`dom.test.tsx` 檢查；Back to project 沒有專門的測試 |
| Problem（各頁的錯誤） | Details／詳細資料（展開）→ 技術訊息與 request ID | TEST-009（request ID 仍在頁中）；`ui.test.tsx`、`i18n-dom.test.tsx` |
| 找不到頁面 | Go to the start page | T-044 review M2（任何沒有頁面的網址的出路）；頁面由 `layout.test.tsx`、`simplicity.test.tsx` 檢查，連結本身沒有專門的測試 |
| `/auth/error` | Sign in again → `/auth/login` | T-043、ADR-018（登入失敗後唯一的出路）；頁面由 `a11y.spec.ts`、`simplicity.spec.ts` 檢查，連結本身沒有專門的測試 |
| route 失敗（error／global-error） | Try again | T-044 review M2；`i18n-dom.test.tsx` |
| `/ui-gallery`（審閱工具，只在 `GEMINUS_UI_GALLERY=1` 時存在，否則 404） | 每個元件的樣本（Save、Cancel、Delete、More options、停用的 Publish、欄位、Time unit、Include archived、Save draft、Discard changes、三個表格、頁面狀態） | T-045 scope；`gallery.test.tsx`、`a11y.spec.ts`、`keyboard.spec.ts`、`strings.spec.ts` |

移除：專案頁的「All projects／所有專案」連結（與 strip 的 Projects 同一目的地；沒有測試或規格要求它）。沒有移除其他控制項。沒有加入：Find、帳號、模式切換、provenance row 的動作（尚無功能；DESIGN.md 的 app strip 中這些等到該功能實作時再加）。

給 owner 的問題：

1. 首頁的「Go to projects／前往專案」與 strip 的「Projects／專案」功能相同。T-044 的測試要求它，所以保留；要移除嗎？（移除時一併更新 `layout.test.tsx`、`keyboard.spec.ts` 與 `simplicity` 測試中的例外。）
2. 未登入時，專案與模型頁只說「需要登入」，沒有登入的動作（impeccable audit 2026-10-19 的 P0；屬 T-043 的 UI）。依「不加功能」沒有加入；是否列為 T-043 的後續？

### 指令與結果（Node 22.22.0；`npm ci --engine-strict=false`，package.json 要求 Node 24）

| 指令 | 結果 |
|---|---|
| `make frontend-test`（lint、typecheck、vitest） | exit 0；vitest 21 個檔案、547 個 test 全過（re-skin 前 18 個檔案、445 個） |
| `make frontend-build` | exit 0 |
| `make e2e-test`（本機 Chromium build 1194，對應 Playwright 1.56.1） | 見下方「最後一次執行」 |
| `make spec-check` | exit 0（`checks/docs-check.json` 的連結數 86 → 87） |
| `npm audit --prefix apps/web --omit=dev` | 0 vulnerability（dev 依賴的 happy-dom high 為既有，未變） |
| 移除 strip `Select` 的按鍵防護（mutation） | `ui-components.test.tsx` 的 4 個 case 失敗，還原後通過 |
| 第一次 `npm --prefix apps/web-e2e test` | exit 1：114 passed、2 failed、32 skipped；兩個失敗都是新測試本身的錯（`getByRole("link", { name: "Projects" })` 未設 exact；把 Next.js 的 RSC 資料 script 誤當成決定主題的 script），修正測試後通過 |
| 第二次 `make e2e-test` | exit 0：116 passed、32 skipped（截圖 NOT RUN） |
| 最後一次 `make e2e-test`（含 strip `Select` 的按鍵防護與其鍵盤檢查） | exit 0：116 passed、32 skipped（截圖 NOT RUN）。a11y 64（7 個 route＋gallery 狀態 × 2 語言 × 2 寬度 × 淺色／深色，全部 0 violation、390 px 無橫向捲動）、keyboard 8、simplicity 28、theme 5、strings 2、session 9。WebServer 的 `failed to pipe response` 來自 session 測試刻意切斷的回應，非失敗 |
| 本機預覽截圖（暫存的 Playwright config，snapshot 路徑改到 evidence 目錄） | exit 0：32 張寫入 `checks/evidence/2026-10-26-T-045-reskin/local-shots/`；`apps/web-e2e/tests/__screenshots__/` 沒有被改動（`git status` 確認） |
| First-load 資源（`next start` 後取 HTML 中的 script 與 stylesheet，gzip 加總；re-skin 前為 f4db5d6 的 `git archive` 另外 build） | `/`：JS 183.9 → 247.7 KiB、CSS 1.4 → 38.7 KiB；`/projects`：JS 203.3 → 253.0 KiB、CSS 3.0 → 38.7 KiB；`/ui-gallery`：JS 282.9 → 293.4 KiB、CSS 3.0 → 38.7 KiB。JS 增加來自 shell 改用 React Aria `Select`（Select／ListBox／Popover）；CSS 增加的 32.9 KiB 是 Noto Sans TC 的 105 個 unicode-range `@font-face`（套件 CSS 原始 106 KB），Archivo 只有 0.4 KiB |

### 本機預覽截圖（不是 baseline）

`checks/evidence/2026-10-26-T-045-reskin/local-shots/`：與 CI 相同的 4 個畫面 × en／zh-Hant × 390／1280 × 淺色／深色，共 32 張，檔名同 baseline（深色加 `-dark`）。這是本機 Chromium 與本機字型（沒有 CJK 系統字型，中文由自架的 Noto Sans TC 顯示）的結果，只供 owner 先看外觀；CI 容器的 Chromium 與系統字型不同，像素不會相同，**不可當 baseline 提交**。

### 截圖 baseline：coordinator 需要做的事

re-skin 改變了每個畫面，因此提交前的 16 張 baseline 全部會不符，另有 16 張深色是新的（缺 baseline 即失敗）。在重新產生之前，CI 的 `browser checks` job 會在截圖比對失敗；同一個 job 也跑 axe、鍵盤、字串與主題檢查，所以失敗時要看 job log 確認只有截圖失敗。

1. 把 `t045-reskin` 推到要整合的分支（以下稱 `<ref>`）。
2. 以 workflow_dispatch 手動執行 workflow `ci`，ref 為 `<ref>`，input `update_screenshots` 為 `true`：`gh workflow run ci.yml --ref <ref> -f update_screenshots=true`。這個 run 的 e2e job 只執行 `npm --prefix apps/web-e2e test -- tests/screenshots.spec.ts --update-snapshots`（固定的 `mcr.microsoft.com/playwright:v1.56.1-noble@sha256:f1e7e010…b837a` 容器），不會提交任何東西。
3. 下載 artifact `screenshots`：`gh run download <run-id> -n screenshots -D <dir>`。內容是 `screenshots.spec.ts/` 下 32 張 PNG：16 張與現有檔名相同（淺色）、16 張以 `-dark.png` 結尾。
4. 由人逐張檢視（TEST-033 要求 baseline 變更由人確認；可先對照本機預覽截圖），把 32 張放到 `apps/web-e2e/tests/__screenshots__/screenshots.spec.ts/`（覆蓋 16 張、新增 16 張）並提交到 `<ref>`。
5. 對 `<ref>` 的一般 CI run：`browser checks` 應全過，截圖比對有執行（0 skipped）。

**建議的落地方式（最不打擾）**：re-skin 與新 baseline 在同一個分支完成後一起合併，main 上的 CI 永遠不會有紅色的截圖比對。順序：推分支 → 一般 CI（預期只有截圖失敗，確認其他檢查全過）→ 同分支手動 `update_screenshots` → owner 看 artifact（或先看本機預覽）並確認 → 提交 baseline 到同分支 → CI 全綠 → 合併。若 owner 的確認需要時間，分支可以先完成獨立 review，baseline 的提交與合併等確認後再做；不建議先合併再補 baseline（main 的 e2e job 會紅，也會遮住其他檢查的結果），也不建議為此暫時跳過截圖比對。

### NOT RUN／限制

| 項目 | 原因 |
|---|---|
| CI 固定容器中的截圖比對與新 baseline 產生 | 只能在 CI 的 workflow_dispatch 執行（見上）；本機只有預覽截圖 |
| Node 24 | 本機只有 Node 22.22.0；以 `--engine-strict=false` 安裝與執行。CI 依 `.nvmrc` 使用 24.21.0 |
| CI run | 依指示不推送、不觸發 CI |
| Owner 對新外觀的視覺審閱 | 待 owner；本報告的預覽截圖與 CI 的 baseline artifact 供審閱 |
| 螢幕報讀器、Firefox、Safari | 同前（需要人執行；P0 只跑 Chromium） |
| 已登入且有資料的畫面 | 沒有掛 AuthProvider，fake API 不回資料（同 impeccable audit 的限制）；這些狀態由 unit test 以元件檢查 |
| CSP 實測 | app 目前沒有 CSP；只依設計推論：字型同源（`font-src 'self'` 即可）、主題不需要我們的 inline script（Next.js 自己的 inline script 另需 nonce，與本次無關） |
| `impeccable` 的 critique／audit 重跑 | 本次沒有執行 impeccable；以上述自動檢查與截圖目視代替 |

### 風險

- **CI 在 baseline 重新產生前會紅**（截圖比對）。處理見上。
- **字型下載**：自架的 woff2 共約 4.5 MB 進入 `/_next/static/media`（105 個 Noto Sans TC 分檔與 3 個 Archivo 檔），每頁只下載用到的分檔（`theme.spec.ts` 檢查 zh-Hant 首頁少於 40 個中文分檔）。`font-display: swap`：第一次造訪可能先以 fallback 字型顯示再換字（沒有做 size-adjust fallback）。
- **First-load JS 與 CSS 變大**（數字見上表）：每頁多約 50–64 KiB gzip 的 JS（shell 的 `Select`；之後 T-015 等頁面本來就會用到它，屬提前付出）與約 37 KiB gzip 的 render-blocking CSS（Noto Sans TC 的 `@font-face` 表，英文頁也載入）。替代方案供 owner 決定：(a) 不載入 Noto Sans TC web font，中文改用各系統的字型（PingFang TC、Microsoft JhengHei、Linux 的 Noto Sans CJK），省下約 33 KiB CSS 與 4.3 MB 靜態檔，但中文字形依作業系統而異、CI 截圖依賴容器字型；(b) 語言與主題改回原生 `<select>`（幾乎不增加 JS，但與 design system 的 Select 外觀不同，且原生 select 在 Windows／Linux 按方向鍵就會換語言）。
- **OFL**：授權與 copyright 記在 `docs/THIRD_PARTY_NOTICES.md`；下載的分檔本身不一定帶授權文字。產品對外提供時是否需要使用者看得到的授權頁，請 owner（或法律意見）決定。
- **主題 cookie**：與語言 cookie 相同的模式（非 HttpOnly、不含身分）；部署的 CDN 快取需依 cookie 區分 HTML（ADR-020 已記錄的同一要求）。
- **React Aria Select 的按鍵**：strip 版本攔下關閉狀態的 ArrowLeft／ArrowRight／type-ahead（以 `onKeyDownCapture` 在 React Aria 之前停止事件）。若 React Aria 日後改變這些鍵的處理方式，`ui-components.test.tsx` 與 `keyboard.spec.ts` 會失敗。
- **DESIGN.md 與 app 的差異**：記在 DESIGN.md「Simplifications in the app」；之後的 UI 任務（T-015、T-031、T-032）若需要 segmented control、overlay 動畫或圖示，應依 DESIGN.md 加入並保留 H-027 的檢查。

## Coordinator 的執行（re-skin，420e36f）

Coordinator 把 `t045-reskin` 的三個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（a0fb365 之後，成為 f522328、9f52384、420e36f）。衝突只在 `DESIGN.md` 的狀態段落：coordinator 在 a0fb365 記錄 H-027 時也改了同一段；取 re-skin 的版本（它描述了 app 已對應 tokens），再補上「換裝後的截圖 baseline 仍待 owner 確認（TEST-033）」。

在整合後的 tree 上執行（Node 22.22.0、npm 10.9.4；`checks/evidence/2026-10-26-T-045-reskin/coordinator-*.log`、`coordinator-exit-codes.txt`）：

| 指令 | 結果 |
|---|---|
| `npm ci --prefix apps/web`、`npm ci --prefix apps/web-e2e`（`--engine-strict=false`，本機沒有 Node 24） | exit 0、exit 0 |
| `make spec-check` | exit 0 |
| `make frontend-test` | exit 0：21 個檔案、547 個 test |
| `make frontend-build` | exit 0 |
| `npm audit --prefix apps/web --omit=dev` | exit 0，0 vulnerabilities |
| `make e2e-test`（本機 Chromium） | exit 0：116 passed、32 skipped（截圖，只在 CI 的固定容器執行：NOT RUN） |

- e2e log 中的 `[WebServer] Error: failed to pipe response`（`UND_ERR_SOCKET`）來自 `session.spec.ts`「a refresh is kept when the API's answer is cut after its headers」：該測試刻意在 header 之後切斷 API 的回應；T-043 第 3 輪的 log（`checks/evidence/2026-10-19-T-043/round3/e2e-test.log`）也有同一行。不是失敗。
- Agent 列給 owner 的三個問題，coordinator 的處理：
  1. 首頁的「Go to projects」與 app strip 的「Projects」重複（T-044 的測試要求它）：交給 owner，與截圖 baseline 一起確認。
  2. 未登入的頁面沒有登入動作：屬 T-043（登入流程），列為 T-043 的後續（`planning/tasks.json`），不在 T-045 新增。
  3. Noto Sans TC web font 的成本：維持現狀。H-011 允許 OFL web font；中文 UI 與英文 UI 中的中文使用者資料（IR 名稱）用同一套字型；瀏覽器只下載用到的分檔。若 T-037 的效能預算不過，再改為只在 zh-Hant 載入或改用系統字型（可回退）。
- **下一步**：push 後 CI 的截圖比對**預期失敗**（16 張外觀改變、16 張 `-dark` 是新的），直到以 `update_screenshots` 在 CI 的固定容器重新產生 baseline、由 owner 逐張確認（TEST-033：baseline 由人確認，不自動覆寫）並提交。其他 job（axe、鍵盤、字串、主題、後端）須為綠。之後交給獨立 reviewer（H-003）。
- **NOT RUN**：CI 固定容器中的截圖比對與 baseline 產生（push 後執行）；Node 24；owner 的視覺確認；螢幕報讀器、Firefox、Safari；已登入且有資料的頁面（只有元件層的 unit test）；CSP 實測。
