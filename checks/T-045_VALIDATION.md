# T-045 驗證報告：Design system、UI 函式庫評估與 UX gate

**日期：2026-10-19｜狀態：`in_progress`（實作與自我驗證完成；截圖 baseline 已產生並提交，CI 開始比對；prototype 與 baseline 待 owner 確認；之後送獨立 review，依 H-003 驗收）｜需求：REQ-022、REQ-023｜驗收測試：TEST-033｜決策：ADR-021 ACCEPTED（H-009）、H-007、H-008**

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
