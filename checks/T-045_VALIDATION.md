# T-045 驗證報告：Design system、UI 函式庫評估與 UX gate

**日期：2026-10-19｜狀態：`in_progress`（實作與自我驗證完成；re-skin（H-027）後的 32 張截圖 baseline 由 CI 固定容器產生、owner 確認（H-028）並提交，CI 比對通過；10-19 prototype 的兩個流程由 H-027 Q2 定為 T-015／T-031 的依據；獨立 review（H-003）第 1 輪 CHANGES REQUESTED，修正見文末；依 H-003 驗收）｜需求：REQ-022、REQ-023｜驗收測試：TEST-033｜決策：ADR-021 ACCEPTED（H-009）、H-007、H-008**

**2026-10-26 更新**：依 H-027 套用 Marey design system 並保持簡潔，見文末「Re-skin：套用 Marey design system，保持簡潔」一節；新的 32 張 baseline 已由 CI 產生、經 owner 確認（H-028）並提交（見文末「新的截圖 baseline（H-028）」）；下方「交付內容」「截圖 baseline」與 Prototype 一節中的 16 張是 re-skin 之前的 baseline，已被取代（review 第 1 輪 L2 更正）。

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
| `apps/web-e2e/tests/__screenshots__/` | 32 張截圖 baseline（4 個畫面 × en／zh-Hant × 390／1280 × 淺色／深色），由 CI 固定容器產生、owner 確認（H-028，見文末）；re-skin 之前是 16 張淺色（見「截圖 baseline」） |
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

**狀態（撰寫時）：實作與自我驗證完成（分支 `t045-reskin`，自 f4db5d6）；截圖 baseline 尚未重新產生（必須在 CI 的固定容器產生，見下），在那之前 CI 的截圖比對會失敗；待 coordinator 整合、CI 與獨立 review（H-003）。T-045 的 status 不變。**

**更新（review 第 1 輪，L2）**：coordinator 已整合（見「Coordinator 的執行」）；32 張 baseline 由 CI 產生、owner 確認（H-028）並提交，之後的 CI 截圖比對通過（run 40309207437）。本節的「CI 會紅」「待 owner 視覺審閱」是撰寫時的狀態。

### 依據

- H-013：Marey 圖表時刻表方向、scenario 深綠／baseline 石墨虛線、Archivo、深色灰黑、預設跟隨系統且可切換並記住；H-011：可用 OFL web font 與深色主題（放寬 H-008 (2)(3)）；H-012：桌機為主、手機只看結果與審核、深色不用藍；H-009／ADR-021：React Aria Components 1.21.1＋CSS Modules＋custom-property tokens 不變。
- H-027（owner，2026-10-26，coordinator 記錄中，依指示視為已批准）：「UI 都照建議，但我希望介面要是簡潔的」；Q1 以 Marey workspace 為設計基礎；Q2 舊 prototype 的模型編輯器與匯入／AI 審閱流程仍是 T-015／T-031 的基礎。同一決定的補充（coordinator 轉達）：「只保留所有應該要包含的按鈕功能等」。

### 「簡潔」的解讀與檢查（寫入 DESIGN.md 的 Simplicity 一節）

| 原則 | 本次怎麼做 | 檢查 |
|---|---|---|
| 1. 每個畫面一個主要動作 | Primary 只用 Graphite Ink；gallery 的停用「Publish」與「Save draft」改為預設樣式，gallery 也只有一個 primary | `tests/simplicity.test.tsx`（每種草稿狀態、gallery、失敗頁；review 第 1 輪起也含已登入的專案列表、專案頁、模型頁）、`apps/web-e2e/tests/simplicity.spec.ts`（每個 route 未登入時的樣子 × en／zh-Hant × 390／1280） |
| 2. 只保留需要的控制項（H-027 補充） | 每個控制項對應已實作且有測試的功能（下表）；移除專案頁重複的「All projects」；app strip 不放 Find、帳號、模式切換 | 瀏覽器（`simplicity.spec.ts`，每個 route 未登入時的樣子 × 兩種語言 × 390／1280）：gallery 以外沒有停用的控制項、沒有同名的可見按鈕、沒有兩個連到同一處的可見連結。Unit（`simplicity.test.tsx`，兩種語言，每個畫面連同 app shell 的控制項，review 第 1 輪 M1 起）：首頁、找不到頁面、登入錯誤頁、每種草稿狀態（含重新載入失敗的衝突）、載入失敗，以及以 fake API 與 session render 的已登入專案列表、專案頁、模型頁（載入成功與失敗）：沒有同名（同 role）的按鈕或連結、沒有兩個連到同一處的連結；已登入的頁面也沒有停用的控制項、至多一個 primary。已登入的頁面在瀏覽器中沒有檢查（app 尚未掛 session，T-043）。唯一例外是 owner 以 H-028 保留的一組 |
| 3. 漸進揭露 | Problem 的技術訊息與 request ID 收進「Details／詳細資料」；驗證細節與重試時間仍直接顯示 | `i18n-dom`／`ui` 測試仍讀得到全文；review |
| 4. 不裝飾 | 1 px hairline（去掉 callout 與 status 的 4 px 側邊條）；陰影只給 popover、dialog 與 invalid 內環；只有 180 ms 的狀態顏色轉換，沒有 overlay 進場動畫；顏色只有中性色、scenario 綠與 warning／error | `tests/design-rules.test.ts`、`tests/tokens.test.ts`；review 第 1 輪 L1 起，`simplicity.spec.ts` 在瀏覽器檢查每個轉換都是 180 ms 與該曲線，reduced motion 時都不超過 0.01 ms |
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
| `/models/[id]` | Back to project → 回到該專案（strip 沒有這個目的地）；衝突時 Reload the current draft（primary）與 Discard my changes（danger）；Try again | T-014、AGENTS §2（409 不 silent overwrite，不自動 rebase）；衝突的兩個動作由 `ui.test.tsx`、`dom.test.tsx` 檢查；Back to project 在已登入的模型頁出現且只有一個（`simplicity.test.tsx`，review 第 1 輪起） |
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
| Owner 對新外觀的視覺審閱 | 撰寫時待 owner；之後由 H-028 確認（32 張 baseline） |
| 螢幕報讀器、Firefox、Safari | 同前（需要人執行；P0 只跑 Chromium） |
| 已登入且有資料的畫面 | 沒有掛 AuthProvider，fake API 不回資料（同 impeccable audit 的限制）；這些狀態由 unit test 以元件檢查 |
| CSP 實測 | app 目前沒有 CSP；只依設計推論：字型同源（`font-src 'self'` 即可）、主題不需要我們的 inline script（Next.js 自己的 inline script 另需 nonce，與本次無關） |
| `impeccable` 的 critique／audit 重跑 | 本次沒有執行 impeccable；以上述自動檢查與截圖目視代替 |

### 風險

- **CI 在 baseline 重新產生前會紅**（截圖比對）。處理見上；已由 H-028 的 baseline 解決（run 40309207437 全綠）。
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

### 新的截圖 baseline（H-028，2026-10-26）

- 180c86f 的一般 CI（run 40304006677）：spec-check、backend、frontend、db + integration 四個 job 全過；browser checks 116 passed、32 failed，失敗的全是 `screenshots.spec.ts`（16 張外觀改變、16 張 `-dark` 沒有 baseline），與預期相同。
- 手動 CI run 40307255504（180c86f，`update_screenshots`，固定的 `playwright:v1.56.1-noble@sha256:f1e7e010…` 容器）產生 32 張，artifact `screenshots`（sha256:e4829e4cb845fc56c985ce064e09071bd800f40c85a3d95e9357a2ecc4d8502f，與下載的 zip 相同）。
- Coordinator 把 32 張做成審閱頁（https://claude.ai/artifact/BMscCLGDCXh9HHbjZZsFpB），owner 回覆「可以，就先這樣吧」（H-028）：32 張確認為 baseline；首頁的「Go to projects」維持現狀（coordinator 的解讀，選項 C）。
- 提交到 `apps/web-e2e/tests/__screenshots__/screenshots.spec.ts/` 的 32 個檔案與 artifact 逐 byte 相同（`cmp`）；覆蓋 16 張、新增 16 張 `-dark`。提交後的 CI 結果見下一點。
- 提交 baseline 後的 CI run 40309207437（245a375）：spec-check、backend、frontend、db + integration、browser checks 五個 job 全過；browser checks **148 passed、0 skipped**（截圖比對實際執行，32 張都與 baseline 相同）。之後 T-045 交給獨立 reviewer（H-003）。

## Review 第 1 輪（2026-10-26）：CHANGES REQUESTED（一個 Medium、五個 Low、Info）

獨立 reviewer（H-003）審查 df7c091（re-skin 與先前的 T-045 工作）：沒有 High；M1 為 Medium；L1–L5 為 Low；I1–I5 為 Info。報告、probe 與 log 在 `checks/evidence/2026-10-26-T-045-r1/reviewer/`（`REVIEW.md`）。在 `t045-r1`（自 d150918，先 cherry-pick reviewer 的 11846e9 → 2dabf25；不 push）修正。每個修正都先寫測試，確認它在舊程式或 mutant 上失敗（red），再修正並確認通過；沒有降低任何 assertion，也沒有加 skip。本輪的 log 在 `checks/evidence/2026-10-26-T-045-r1/`（reviewer 的在 `reviewer/` 子目錄）。

**畫面沒有改變**：`apps/web-e2e/tests/__screenshots__/` 沒有動；產品程式只改了兩處，都不影響 render：I2（登入錯誤頁查 reason 的方式，只影響不認得的 reason）與 I3（瀏覽器讀 cookie 的方式，只影響 app 自己不會寫出的 percent-encoded 值）。L1 拿掉的 config 行從來沒有生效，瀏覽器檢查的環境不變（新的 180 ms 測試證明這一點，見 L1）。會改變像素的只有 L5 的建議，依指示沒有做。32 張 baseline 是否仍全部相符，要等 coordinator push 後的 CI 截圖比對確認（本機依設計不比對）。

### Commit

| Commit | 內容 |
|---|---|
| 2dabf25 | Reviewer 的報告與 evidence（cherry-pick 11846e9，沒有衝突） |
| 65980dd | 測試與修正：M1（`simplicity.test.tsx`）、L1（`playwright.config.ts`、`simplicity.spec.ts`）、L4（`tokens.test.ts`）、I2（`app/auth/error/page.tsx`、`layout.test.tsx`、`session.spec.ts`）、I3（`lib/cookie.ts`、`lib/theme/theme.ts`、`lib/i18n/locale.ts`、`theme.test.tsx`、`i18n.test.ts`）、I5（`a11y.spec.ts`）、`apps/web-e2e/README.md` |
| e9dff9e | 文字：DESIGN.md（M1、L1、L2、L4）、`.impeccable/design.json`（L2）、DECISIONS_AND_TBD 的 H-027 待辦（L2）、ADR-011（I4）、T-043 報告的後續（I2）、本報告前面過時的狀態與 Simplicity 表（L2、M1） |
| （最後一個） | 本節、evidence、`exit-codes.txt`、`environment.txt`、`checks/docs-check.json` |

### M1（Medium）：「同一動作不放兩個控制項」在有產品控制項的畫面沒有測試

- **發現**：DESIGN.md:434 與本報告的 Simplicity 表寫「沒有兩個連到同一處的連結、沒有同名按鈕」已檢查，但 unit test 只對首頁、找不到頁面與登入錯誤頁檢查共用的 href，完全沒有檢查同名按鈕；瀏覽器的 `simplicity.spec.ts` 只看得到未登入的 route，看不到模型列表、`DraftStatus`（Reload／Discard）、「Back to project」與 Try again。Reviewer 的 mutant M2（`DraftStatus` 有兩個「Discard my changes」）與 M4（每個模型列多一個連到同一模型的連結）在 547 個 test 下都存活；M3（加回「All projects」）只被瀏覽器擋下。
- **修正**（`apps/web/tests/simplicity.test.tsx`）：
  - 控制項是看得見、聽得到的 `a[href]`、`[role=link]`、`button`、`[role=button]`（`[hidden]`、`aria-hidden`、`inert` 之內的不算）。名稱依 `aria-labelledby`、`aria-label`、文字的順序取得（strip 的 Select 因此是「English Language」）。重複的定義：同 role 且同名，或兩個連結連到同一處（頁內 `#` 錨點不算）。
  - 每個畫面都與 root layout render 的 app shell 的控制項一起比較（skip link、Projects、Language、Theme），因為讀者是一起遇到它們的。兩種語言各跑一次，因為兩個不同的英文字串可能有相同的中文。
  - 畫面：首頁、找不到頁面、登入錯誤頁；每種草稿狀態（clean、dirty、saving、conflict、error、out of date）與重新載入失敗的衝突；`RemoteView` 對 projects、models、model 的載入失敗；以 fake API（app 自己的 `createApiClient`，fetch 依路徑回答）與 `AuthProvider`（editor 與 viewer）render 的已登入專案列表、專案頁、模型頁，各有載入成功與 503 失敗。已登入的頁面另外檢查沒有停用的控制項、至多一個 primary，並確認預期的控制項真的在（列表的連結、Back to project、草稿狀態、恰好一個 Try again），讓規則不會在空頁面上空轉。
  - 另有一個檢查 `duplicates()` 本身會回報重複的 case。
  - `<summary>`（Problem 的「Details」）與瀏覽器的規則一樣不算按鈕：重新載入失敗的衝突有兩個 Problem，各有自己的 Details，展開的是不同的內容。
- **瀏覽器中的已登入頁面：NOT RUN**。現有的 e2e 環境做不到，除非加新的 infrastructure：app 沒有掛 `AuthProvider`（`app/layout.tsx`：等 T-008／T-043 的 UI bootstrap、D-045），所以即使經 BFF 登入（`session.spec.ts`），每頁仍是未登入的樣子；fake API（`fake/servers.mjs`）的專案列表是空的，其他路徑回 404。要做到需要改產品程式與 fake API，不在本輪範圍。
- **先紅後綠**（`mutants.py.txt`：套用 mutant、跑完整的 `npx vitest run`、逐 byte 還原）：
  - 舊測試（2dabf25 的 `simplicity.test.tsx` 與 `tokens.test.ts`，`mutants-head-tests.log`）：R-M2、R-M3、R-M4 與新增的 A-back-twice（模型頁兩個「Back to project」）**都存活**（550 passed：547 加上本輪 I2、I3 的 3 個）；A-retry-twice（失敗時兩個 Try again）原本就被「只有一個按鈕」擋下。
  - 新測試（`mutants-new-tests.log`）：七個 mutant 全部被擋下。R-M2 擋在 conflict、error 與重新載入失敗的衝突（兩種語言，6 個）；R-M3 擋在專案頁的載入成功與失敗（4 個，現在 unit test 也擋得下，不只瀏覽器）；R-M4 擋在專案頁的載入成功（2 個）；A-back-twice 擋在模型頁（2 個）；A-retry-twice 擋在 13 個。
- **文字**：DESIGN.md 的 rule 2（原 434 行）改為分別寫出瀏覽器與 unit test 各檢查什麼、在哪些畫面，並寫明已登入的頁面在瀏覽器中沒有檢查；rule 1 補上已登入的頁面。本報告 Simplicity 表的第 1、2 列同樣改寫，每個畫面的控制項表中「Back to project 沒有專門的測試」改為由 `simplicity.test.tsx` 檢查。

### L1（Low）：`reducedMotion: "reduce"` 在 Playwright Test 中無效，reduced motion 的規則沒有測試

- **發現**：`apps/web-e2e/playwright.config.ts:49` 把 `reducedMotion` 放在 `use` 的頂層。Playwright Test 1.56 只在 `contextOptions` 中接受它，所以這一行從 dd9dc34 起都沒有生效；`base.css` 的 reduced-motion 規則也沒有任何測試。
- **修正**：
  - 依 coordinator 的決定，拿掉這一行，**不**改成全域的 `contextOptions`，因為那會改變被接受的 baseline 產生時的環境。改為留一段註解說明。
  - `simplicity.spec.ts` 新增兩個 case：
    - 一般情況（motion 開著）：gallery 上每個有轉換的元素都是 `0.18s`、`cubic-bezier(0.2, 0.8, 0.2, 1)`，且 `matchMedia("(prefers-reduced-motion: reduce)")` 為 false。這一點把瀏覽器檢查的環境固定為 baseline 產生時的樣子。
    - `page.emulateMedia({ reducedMotion: "reduce" })` 時：每個轉換與動畫都不超過 0.01 ms（Chromium 回報 `1e-05s`）。
- **先紅後綠**：
  - `red-e2e-run1.log`（config 仍保留那一行）：180 ms 的 case **通過**，證明那一行沒有傳到頁面。拿掉 `base.css` 的 reduced-motion 區塊後，reduce 的 case 失敗（`0.18s`）。
  - `red-e2e-run2-context-options.log`（改為 `use: { contextOptions: { reducedMotion: "reduce" } }`）：180 ms 的 case 失敗（「these checks run with motion on」）。若之後有人把 reduced motion 加回全域，這個 case 會擋下。
  - 我自己的兩個錯誤也保留了 log：
    - `red-e2e-run1-test-bug.log`：新測試把每個屬性各一個的 easing 當成一個值，先修正測試再重跑。
    - `red-e2e-run2-option-misplaced.log`：mutant 誤把 `contextOptions` 放在 config 的頂層，同樣無效，所以 180 ms 的 case 通過。這次 run 也顯示新測試原本期望的 `1e-05s, 1e-05s, 1e-05s` 不對：規則對所有屬性只設一個值，Chromium 回報 `1e-05s`，測試已改為這個值。
- **環境**：本輪 133 個、之前 116 個瀏覽器檢查，以及 CI 的截圖，都在 motion 開著的情況下執行，和 baseline 產生時相同（`toHaveScreenshot` 另有 `animations: "disabled"`）。
- **型別檢查**：用 apps/web 的 `tsc` 臨時檢查 2dabf25 的 config，會得到 TS2769「'reducedMotion' does not exist in type 'UseOptions<…>'」（`tsc-e2e-reviewed-config.log`）；新的 config 與所有 spec 都通過（`tsc-e2e.log`，exit 0）。`apps/web-e2e` 沒有 tsconfig 也沒有 `@types/node`，所以沒有加進 make（見殘餘風險）。

### L2（Low）：H-028 之後過時的狀態文字

- **修正**：
  - DESIGN.md:418：改為 owner 已確認 32 張 baseline（H-028）。
  - `.impeccable/design.json` 的 overview（原 1196 行）：狀態與 app tokens 的描述改為與 DESIGN.md 一致。
  - DECISIONS_AND_TBD 的 H-027 待辦欄：「16 張」改為「32 張（淺色、深色各 16 張）」，並以刪除線加「H-028：已確認」標示；只改數量與狀態，owner 的原話不變。
  - 本報告第 3、5、23 行；re-skin 一節的狀態（原 98 行）改為「撰寫時」並加一段更新；同節 NOT RUN 的「Owner 對新外觀的視覺審閱」與風險「CI 在 baseline 重新產生前會紅」補上 H-028 的結果；原 254 行改為「見下一點」。
- **CI 結果**：run 40309207437 已記在「新的截圖 baseline（H-028）」的最後一點：5 個 job 全過，browser checks 148 passed、0 skipped，32 張都比對過。
- **run 40307255504 為什麼顯示 cancelled**：`update_screenshots` 的 run 40307255504 整體結論是 **cancelled**。原因是 coordinator 的下一次 push 觸發了 workflow 的 concurrency group（`.github/workflows/ci.yml`：`group: ci-${{ github.ref }}`、`cancel-in-progress: true`），取消了這個 run 中還在執行的 backend job。在那之前，它的 browser checks job 已經成功，並上傳了 artifact `screenshots`（reviewer 下載的 12643138154，sha256 與報告相同，32 張與提交的檔案逐 byte 相同）。所以 baseline 的來源沒有問題。
- **沒有改的地方**：H-013 那一列的待辦欄同樣寫著「換裝後的截圖 baseline 待 owner 確認」。依指示這次只改 H-027 那一列，H-013 留給 coordinator。

### L3（Low）：re-skin agent 的部分結果沒有 log

re-skin 的四個結果的**原始 log 都沒有保留**：strip 按鍵防護的 mutation、第一次 e2e run、本機預覽截圖的 run、first-load 大小表。以下是本輪的重現，reviewer 也重現過前兩項中的兩個結果（`reviewer/mutations.log` M1、`reviewer/first-load-probe.log`）。

- **按鍵防護的 mutation**（重現）：`mutants.py.txt` 的 `L3-strip-guard-removed`（`<div onKeyDownCapture={onlyByChoice}>` → `<div>`）在舊、新測試下都讓 `ui-components.test.tsx` 失敗 4 個 case，與報告的「4 個 case 失敗」及 reviewer 的 M1 相同：ArrowLeft、ArrowRight、「繁」、「e」。
- **First-load 大小**（重現）：`first-load.py.txt` 與 `first-load.log`，先 `next build` 再 `next start`，取 HTML 中的 script 與 stylesheet。re-skin 之前是 f4db5d6 的 apps/web 封存副本；re-skin 是 2dabf25 的封存副本，它的 apps/web 與 df7c091、245a375 相同；兩份都用同一份 node_modules，兩者只差兩個字型套件。另外量了本輪 worktree 的 build，結果與 2dabf25 相差在 0.1 KiB 以內。數字為 gzip 後的 KiB：

| Route | 量法 | re-skin 前 JS／CSS | re-skin 後 JS／CSS |
|---|---|---|---|
| `/` | 原報告 | 183.9／1.4 | 247.7／38.7 |
| `/` | 本輪 gzip-6 | 183.4／1.4 | 246.9／38.0 |
| `/` | 本輪 gzip-9 | 183.0／1.4 | 246.3／37.8 |
| `/` | reviewer（gzip-9） | 未量 | 246.3／37.8 |
| `/projects` | 原報告 | 203.3／3.0 | 253.0／38.7 |
| `/projects` | 本輪 gzip-6 | 202.9／3.0 | 252.2／38.0 |
| `/projects` | 本輪 gzip-9 | 202.4／3.0 | 251.6／37.8 |
| `/projects` | reviewer（gzip-9） | 未量 | 251.6／37.8 |
| `/ui-gallery` | 原報告 | 282.9／3.0 | 293.4／38.7 |
| `/ui-gallery` | 本輪 gzip-6 | 282.2／3.0 | 292.5／38.0 |
| `/ui-gallery` | 本輪 gzip-9 | 281.5／3.0 | 291.8／37.8 |
| `/ui-gallery` | reviewer（gzip-9） | 未量 | 291.8／37.8 |

  - 本輪的 gzip-9 與 reviewer 完全相同。gzip 等級只解釋約 0.6 KiB 的差距；原報告與本輪 gzip-6 之間還有 0.4–0.9 KiB，原因不明，因為原始的量法與 log 沒有保留。
  - 結論不變。`/` 的 JS 多了約 63 KiB，`/projects` 多了約 49 KiB；gallery 原本就用 Select，只多 10 KiB。每頁的 CSS 多了約 35–37 KiB。原「風險」中「每頁多約 50–64 KiB」指的是 `/` 與 `/projects`。
  - Next 16 的 `next build` 不印出每個 route 的大小（`first-load-build-*.log`），所以只看 `make frontend-build` 無法確認這些數字。
- **沒有重現**：
  - 第一次 `npm --prefix apps/web-e2e test`（114 passed、2 failed）：那兩個失敗是新測試本身的錯，在提交前已修正，無法以提交的程式重現。之後的結果（116 passed）由 coordinator、reviewer 重現，本輪為 133 passed。
  - 本機預覽截圖的 run：32 張輸出圖檔已提交在 `checks/evidence/2026-10-26-T-045-reskin/local-shots/`，run 的 log 沒有保留。它們不是 baseline，baseline 已由 H-028 取代。

### L4（Low）：一組使用中的非文字對比沒有測試；DESIGN.md 的數字

- **發現**：表格中被選取的列，選取 checkbox 的 Control Edge 邊框畫在 Selected Row Tint 上，對比為淺色 3.31:1、深色 3.06:1。`tokens.test.ts` 沒有這一組。DESIGN.md:471 寫 Control Edge「至少 3.17:1」，那是 prototype 的數字，app 中最低的是 3.06:1。
- **修正**：
  - `tokens.test.ts` 的 `nonTextPairs` 加入 `--gm-color-edge` 對 `--gm-color-selection-tint`，兩個主題都以 3:1 檢查。
  - DESIGN.md:471 改為：3.17:1 是 prototype 的數字；app 中最低的是 3.06:1（深色，被選取列的 checkbox 在 Selected Row Tint 上；淺色為 3.31:1）；並列出 `tokens.test.ts` 檢查的五個表面。
  - 沒有改任何顏色。
- **先紅後綠**：mutant `L4-selection-tint-dark-lighter` 把深色的 Selected Row Tint 調亮一階（#262626 → #2A2A2A），DESIGN.md 的 front matter 與 `tokens.css` 的兩個深色區塊一起改，讓逐值比對仍通過，此時 Control Edge 對它是 2.90:1。這個 mutant 在舊測試下存活（`mutants-head-tests.log`），在新測試下被「dark: --gm-color-edge against --gm-color-selection-tint is at least 3:1」擋下（`mutants-new-tests.log`）。

### L5（Low，判斷）：手機上主題控制項只看得到值

- **不修改**：owner 已接受這些截圖（H-028），所以這一項記錄給 owner。
- **Reviewer 的理由**：
  - ≤ 720 px 時，strip 的標籤只在視覺上隱藏。主題控制項只顯示「System」或「跟隨系統」等值與一個箭頭（baseline `home-en-390.png`）。
  - 它的 accessible name 仍包含標籤（「System Theme」），所以 WCAG 2.5.3 成立，axe 也通過。
  - 但看得見的人無法從畫面知道這是主題的選擇。WCAG 3.3.2 只能說是透過打開後的選項清單勉強符合。
  - 語言控制項沒有這個問題，因為「English」與「繁體中文」本身就說明了它是什麼。
- **可能的修改**（都會改變 390 px 的截圖，需要 owner 另行同意）：在手機上保留短的可見標籤，或加上主題圖示（DESIGN.md：圖示隨正式的 icon set 一起加入）。

### Info

- **I1：已知行為，不修改**。關閉的 strip Select 上按可列印的鍵，什麼都不會發生：不換值，也不打開清單。APG 的 select-only combobox 會打開清單並移到相符的選項。
  - 不修改的理由：要做到需要在 `Select` 裡控制開關狀態，並在清單中把焦點移到相符的選項，不是一行就能完成的修改；而且它會改變 `ui-components.test.tsx` 與 `keyboard.spec.ts` 檢查的鍵盤行為。
  - 目前的行為：ArrowUp、ArrowDown、Enter、Space 會打開清單；清單打開後 type-ahead 正常（reviewer 的 P1–P3）。
  - 這不違反 WCAG。若之後的畫面需要，再依 APG 加入。
- **I2（T-043 的程式）：已修正**。
  - 發現：`/auth/error?reason=toString`（以及 `constructor`、`__proto__`、`hasOwnProperty`、`valueOf`）回 500，因為 `reason in reasons` 也找得到 `Object.prototype` 的名稱。
  - 修正：改為 `Object.hasOwn(reasons, reason)`。
  - 先紅後綠：`layout.test.tsx` 的新 case 涵蓋這些名稱、`isPrototypeOf` 等、不認得的字、空字串、沒有 reason、兩個 reason，每一種都必須顯示一般的失敗文字與「Sign in again」。它在修正前失敗（`red-i2-i3.log`：「message function toString() … needs a numeric count」）。
  - 瀏覽器：`session.spec.ts` 的新 case 在修正前得到 500（`red-e2e-run1.log`），修正後為 200。
  - `checks/T-043_VALIDATION.md` 已加上後續一節。
- **I3：已修正，瀏覽器改為和伺服器一樣解碼**。伺服器以 Next.js 的 `cookies().get` 讀 cookie，會 percent-decode 值；瀏覽器端的 `cookieTheme` 與 `cookieLocale` 原本不解碼。語言 cookie 也有同樣的差異，所以兩者一起改用新的 `lib/cookie.ts`，它照 `@edge-runtime/cookies` 的 `parseCookie` 讀：以「; 」分開、值 percent-decode、無法解碼的那一對略過、沒有「=」的讀成 "true"、同名的取最後一個。
  - 為什麼不改成伺服器不解碼：那需要兩個 server helper 放棄 `cookies().get`，改讀原始的 Cookie header。
  - 測試：`theme.test.tsx` 與 `i18n.test.ts` 各以 20 個 header（含 `%64ark`、`zh%2DHant`、最後一個無法解碼、`%2564ark` 雙重編碼、沒有 `=`、空白、引號）與已安裝的 Next.js `RequestCookies` 逐一比對。它們在修正前失敗（`red-i2-i3.log`）；Next.js 日後若改變解析方式，這兩個測試也會失敗。
  - app 自己只寫出 `light`、`dark`、`en`、`zh-Hant`，所以畫面不變。
- **I4：已修正**。ADR-011 原本寫「5.3.1 為 2026-10-11 發布」，這只對 `noto-sans-tc` 成立；`archivo` 的最新版是 5.3.0。兩者都固定在 5.3.0。發布時間見 `npm-view-archivo-time.json`、`npm-view-noto-sans-tc-time.json`。
- **I5**：
  - **主題由讀者明確選擇時的 axe**：已加入。`a11y.spec.ts` 新增 14 個 case：深色選在淺色系統上、淺色選在深色系統上，en、1280 px，每個 route，確認 `data-theme` 與背景色，0 violation。這些 case 不截圖，所以 baseline 不受影響。
  - **之後的 strict CSP**：React Aria 與虛擬化表格會在 SSR 的 HTML 中輸出 `style="…"` 屬性。之後加入 CSP 的任務需要處理 `style-src-attr`（例如允許 inline style 屬性），或改變這些元件的作法。DESIGN.md「Tokens in the app」寫的「nothing for a CSP to allow」只指主題與字型，這一點仍正確。目前 app 不送 CSP。
  - **320 px 的 reflow**：reviewer 的 probe P12 顯示兩種語言都沒有橫向捲動，strip 換成兩列（81 px）；48 px 一列的說法只針對 390 px。不需要修改。

### 實際執行（本輪，e9dff9e 的 tree；Node 22.22.0、npm 10.9.4、Chromium build 1194）

| 指令 | Exit | 說明 |
|---|---:|---|
| `npm ci --prefix apps/web --engine-strict=false`、`npm ci --prefix apps/web-e2e --engine-strict=false` | 0、0 | `npm-ci-web.log`、`npm-ci-e2e.log`；EBADENGINE 警告來自 Node 22（要求 24） |
| `make frontend-test` | 0 | `frontend-test.log`：lint、typecheck、vitest 21 個檔案、589 個 test。之前是 547 個；新增 42 個：simplicity 11 → 48、tokens 2、theme 1、i18n 1、layout 1 |
| `make frontend-build` | 0 | `frontend-build.log` |
| `npm audit --prefix apps/web --omit=dev` | 0 | `npm-audit-omit-dev.log`：0 vulnerabilities；沒有新增依賴 |
| `make e2e-test`（本機 Chromium） | 0 | `e2e-test.log`：133 passed、32 skipped（截圖依設計在本機 NOT RUN）。a11y 78（64＋14 個明確主題）、keyboard 8、simplicity 30（28＋2 個 motion）、theme 5、strings 2、session 10（9＋1 個 I2）；`failed to pipe response` 與之前相同，來自 session 測試刻意切斷的回應 |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `spec-check.log`（本節寫好之後執行）；重新產生 `checks/docs-check.json` |
| Mutation harness，舊測試（`mutants.py.txt --tests head`） | 1 | `mutants-head-tests.log`：預期的結果，R-M2、R-M3、R-M4、A-back-twice、L4 存活；A-retry-twice、L3 被擋下 |
| Mutation harness，新測試（`--tests new`，最後版本的測試） | 0 | `mutants-new-tests.log`：7／7 被擋下 |
| I2、I3 的新 unit test，修正前（red） | 1 | `red-i2-i3.log`：3 個失敗 |
| 新的瀏覽器測試，I2 修正還原、reduced-motion 區塊拿掉（red） | 1 | `red-e2e-run1.log`：I2 與 reduce 2 個失敗，180 ms 與 14 個明確主題通過；`red-e2e-run1-test-bug.log` 是先前有測試錯誤的那一次 |
| `use: { contextOptions: { reducedMotion } }`（red） | 1 | `red-e2e-run2-context-options.log`：180 ms 的 case 失敗；`red-e2e-run2-option-misplaced.log` 是放錯位置的那一次 |
| First-load 量測（f4db5d6、2dabf25、本輪） | 0、0、0 | `first-load.log`；build log 為 `first-load-build-*.log` |
| 臨時的 `tsc`（apps/web-e2e） | 0；2 | `tsc-e2e.log`（新的 config 與 spec）；`tsc-e2e-reviewed-config.log`（2dabf25 的 config：TS2769） |
| `npm view … time --json`（兩個字型套件） | 0 | `npm-view-*.json` |

Exit code 也列在 `exit-codes.txt`，環境在 `environment.txt`。

### NOT RUN（本輪）

| 項目 | 原因 |
|---|---|
| 本機的截圖比對 | 依設計只在 CI 的固定容器執行；由 coordinator push 後確認 32 張 baseline 仍全部相符 |
| 瀏覽器中的已登入頁面（專案列表、專案頁、模型頁、409） | app 沒有掛 `AuthProvider`，fake API 不回資料（見 M1）；這些畫面只在 unit test 檢查 |
| L3 的第一次 e2e run 與預覽截圖 run | 原始 log 沒有保留，無法以提交的程式重現（見 L3） |
| `apps/web-e2e` 的型別檢查加入 make | 沒有 tsconfig 與 `@types/node`（加入會是新依賴）；只有臨時的 `tsc` |
| Node 24 | 本機只有 Node 22.22.0 |
| Firefox、Safari、螢幕報讀器 | P0 只跑 Chromium；報讀器需要人執行 |
| `global-error` 在瀏覽器中 | 沒有不改程式就讓 root layout 失敗的方法（同 reviewer） |
| 真實的 CSP header | app 不送 CSP；I5 只依 SSR 的 HTML 推論 |
| Push、CI、baseline 變更 | 依指示不做；`__screenshots__/` 沒有改動 |

### 需要 owner 知悉

- **L5**：手機上的主題控制項只看得到值（見 L5）。要加可見標籤或圖示，需要 owner 同意新的 390 px 截圖。
- **I1**：關閉的 strip Select 不處理可列印的鍵（已知行為，見 I1）。

### 殘餘風險

- **Unit test 中的控制項規則是近似的**：
  - 名稱只依 `aria-labelledby`、`aria-label`、文字取得，不是完整的 accname 演算法。
  - 是否隱藏只看 `hidden`、`aria-hidden`、`inert`，因為 happy-dom 沒有 layout。
  - `<summary>` 不算按鈕。
  - 已登入頁面的瀏覽器檢查要等 app 掛上 session。
- **Motion 的瀏覽器檢查只在 gallery**：gallery 有每一種元件，但之後新增的畫面若有自己的轉換，只有 `design-rules.test.ts`（只允許 `--gm-motion`）擋得下。
- **`apps/web-e2e` 沒有型別檢查**：這正是 L1 的成因。建議之後加入，需要在 apps/web-e2e 加 tsconfig 與 `@types/node`。
- **Cookie 的解析綁定 Next.js 16.4.1 的 `@edge-runtime/cookies`**：差異測試直接比對已安裝的版本，升級 Next.js 時若解析方式改變，測試會失敗。
- **CSP 的 `style` 屬性**：見 I5。
- **H-013 的待辦欄**：仍寫著 baseline 待確認，留給 coordinator。

### Coordinator 的執行（第 1 輪修正，556cdc6）

Coordinator 把 `t045-r1` 的四個 commit（2dabf25、65980dd、e9dff9e、9a957c2）cherry-pick 到 `claude/lucid-meitner-vq1awo`（6e75b60 之後，成為 c74f33f、e1225ed、4e33c39、556cdc6）；衝突只在重產的 `checks/docs-check.json`。在整合後的 tree 上（Node 22.22.0，`--engine-strict=false`；`checks/evidence/2026-10-26-T-045-r1/coordinator/`）：`npm ci`（兩個專案）、`make spec-check`、`make frontend-test`（21 個檔案、589 個 test）、`make frontend-build`、`npm audit --omit=dev`（0 vulnerabilities）、`make e2e-test`（133 passed、32 skipped：截圖只在 CI 的固定容器比對）全部 exit 0。e2e log 中的 `failed to pipe response` 仍是 `session.spec.ts` 刻意截斷回應的測試（見上）。`apps/web-e2e/tests/__screenshots__/` 沒有改動；push 後由 CI 以 owner 確認的 32 張 baseline 比對（H-028）。依 H-029 的 hackathon 模式，本輪的 Medium 已修正並經 coordinator 驗證，T-045 在 CI 通過後接受；Low 的後續（L5 手機寬度的主題標籤、e2e 專案沒有 typecheck）列在本節之前的報告中。
