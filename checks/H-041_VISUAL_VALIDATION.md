# H-041 驗證報告：整套視覺換成開發者工具風格（hackathon 切片）

**日期：2026-10-29｜範圍：H-038 的 UI 收尾｜依據：owner 的回答 H-041、三張經 owner 核可的高擬真示意（`checks/evidence/2026-10-29-h041-visual/`）、impeccable new-work（owner 指定參考產品，走「照參考產品水準」的路徑）、`DESIGN.md` Simplicity（H-027）｜需求：REQ-022、REQ-023**

## 1. Owner 的決定

H-040 的截圖 baseline 給 owner 確認時，owner 回覆：「你有懂我的意思嗎？我覺得現在還是有點醜欸，太素了感覺根本沒有設計過的痕跡啊？」

接著一輪問答：

- 參考產品：Railway、Vercel / Geist、Figma / Framer。
- 評審的第一印象：全部頁面一樣重要。
- 改動幅度：整套換掉。

我做了三張高擬真示意（結果頁深色、模型頁深色、提案頁淺色），owner 選「就是這個，照做」，預設主題「跟隨系統」。H-040 的 8 張 `/projects` baseline 沒有 commit，因為外觀又變了。

## 2. 改動

| 範圍 | 改動 | 檔案 |
|---|---|---|
| Tokens | 中性色無彩度：淺色 #FAFAFA 頁面、白卡片；深色 #0A0A0A 頁面、#111111 卡片。新增 accent（紫，AI、選取、焦點）、`rule-strong`；scenario 綠改亮；深色的各色 wash 改為帶色調。字級 12、13、15、20、26。圓角 6、8、12、full。三種陰影 token（card、control、overlay）。焦點環改為 accent | `ui/tokens.css`、`DESIGN.md` front matter |
| 字型 | Geist、Geist Mono（`@fontsource-variable/geist`、`geist-mono` 5.3.0，OFL-1.1，自架）加 Noto Sans TC；移除 Archivo。ID、hash、程式碼改用 Mono | `ui/global-styles.ts`、`ui/base.css`、`package.json`、`docs/THIRD_PARTY_NOTICES.md` |
| 頂列 | 52 px；品牌標誌是兩個錯開的方塊，一個 accent、一個描邊，代表「twin」；Projects 連結改為 ghost 樣式 | `app/layout.tsx`、`ui/shell.module.css` |
| 頁首 | 灰色帶取消。麵包屑在上，標題用 26 px Display，來源資訊改成膠囊，動作放在右側並與膠囊對齊 | `ui/page-header.*` |
| 按鈕 | 8 px 圓角、Rule Strong 邊、極淡陰影。主要按鈕為反色實心。新增 `ai` 變體（accent），用於「Send to the AI」和「Explain these results」。執行按鈕維持綠色 | `ui/button.*`、`ask-form.tsx`、`analysis.tsx` |
| 控制項 | Select 的按鈕與一般按鈕同款。分段控制改為凹槽加浮起的選中項。欄位的標籤字重 500 | `ui/select.module.css`、`ui/segmented.*`、`ui/field.module.css` |
| 模型頁 | 工作區是一張卡片：點陣畫布、168 px 的卡片步驟（選中時 accent 外環）、浮在畫布上的工具膠囊（Add step、Show all steps），2D / 3D 在右上，右側是檢查面板 | `canvas.module.css`、`editor.module.css`、`model-editor.tsx` |
| 結果頁 | 每一部分都是卡片；前置時間那句用 20 px Headline；右側面板改為 sticky；Runs 區塊不再是卡中卡 | `results.module.css`、`simulations.module.css` |
| 提案頁 | 左欄是摘要、變更、假設、預期；右欄是風險與決定（sticky）；每一部分都是卡片；JSON Patch 用 Mono | `proposal-view.tsx`、`proposal.module.css` |
| 清單 | 專案與模型清單是卡片 | `ui/list.module.css` |

規格同步：`DESIGN.md` 的 Overview、Tokens in the app、Colors、Typography、Layout（頁首）、Elevation、Shapes、Buttons、Segmented 皆已改寫。T-045 原型畫面的描述保留結構，並加上舊名稱與新 token 的對照；`docs/DECISIONS_AND_TBD.md` 新增 H-041。

## 3. 測試

設計規則測試改成 H-041 的規則（owner 的決定取代 H-013 的視覺）。WCAG 對比檢查全部保留，並新增 accent 的對比組合：

- `tokens.test.ts`：
  - 允許的色相加上 accent。
  - 深色中性色仍必須零彩度；各色的 wash 可以帶色調。
  - 字級改為 12、13、15、20、26，字型改為 Geist、Geist Mono、Noto Sans TC。
  - 焦點環是 accent，深色時調亮。
  - 新增對比組合，文字至少 4.5:1：accent 在 sheet、ground、accent-wash 上；on-accent 在 accent-fill 上；ink 在 accent-wash 上；ink-3 在 raised 上。
  - 新增非文字對比組合，至少 3:1：accent 對 sheet、raised、ground。
  - 兩個主題全部通過。
- `design-rules.test.ts`：
  - 陰影只能來自三個 shadow token，或兩種狀態環（欄位無效、步驟選中）。
  - 漸層只允許畫布點陣那一個。
  - 圓角允許 `full`。
- `proposal-ask.test.tsx`：Send 的 variant 改為 `ai`，主要按鈕仍只有一個。

`apps/web-e2e`：
- `helpers.ts`：頁面底色改為 Ground（淺色 rgb(250,250,250)、深色 rgb(10,10,10)）。
- `theme.spec.ts`：檢查的字型檔改為 `geist-latin-wght-normal`，`document.fonts.check` 改查 `13px "Geist Variable"`。

## 4. 獨立 review（H-029 一次）

| # | 發現 | 處理 |
|---|---|---|
| H1 | 頂列改成 52 px，e2e 的 `simplicity.spec` 仍斷言 48 px；390 px 時頂列可能折成兩行 | **修正**：頂列恢復 48 px。實測 390 px 時確實折行（81 px），因為 Geist 比 Archivo 寬、加了標誌，Projects 連結也有內距。手機版改為隱藏標誌、移除連結內距，en 與 zh-Hant 在 390 及 1280 px 都是一行 48 px。e2e 第一輪有 124 個失敗，全部是這個原因 |
| M2 | AI 按鈕 hover 時底色換成 accent，深色模式下白字對比只有約 2.7:1 | **修正**：hover 時保留 accent-fill 底色，只把邊框改成 accent |
| M3 | 批准或拒絕失敗時，錯誤訊息出現在左欄底部，離右側的決定按鈕很遠 | **修正**：錯誤與「僅能檢視」的提示都移到右欄、決定區上方 |
| M4 | sticky 的側欄沒有 max-height，比視窗高時下半部看不到 | **修正**：結果頁 `.side` 與提案頁 `.aside` 設 `max-height: calc(100vh - 2 × 16px)`，超出時在側欄內捲動 |
| M5 | DESIGN.md 與實作不符：Do's and Don'ts 寫「不要點陣」「不要 monospace」「Archivo」；h1 寫 18 px；front matter 的 segmented、node、canvas、side-panel、display 字距 | **修正**：逐條改寫；front matter 的元件值對齊 CSS |
| M6 | 32 張截圖 baseline 未重拍；驗證文件沒有命令與結果 | baseline 由 CI 重拍，交 owner 確認後才 commit；命令與結果見第 6 節 |
| L | `font-stretch: 87.5%` 對 Geist 無效 | **修正**：刪除 |
| L | `.more` 類別在 metrics table 裡也有使用，造成卡中卡 | **修正**：3D 區塊改用自己的 `.card3d` |
| L | 提案頁右欄為空時（產生中、失敗、已決定、檢視者）仍保留欄寬 | **修正**：`.page:has(> .aside:empty)` 改為單欄 |
| L | 3D 使用率的 busy 色（warning-edge）在深色變暗 | **修正**：warning-edge 恢復原本的 #D29A3A／#B07A1E；3D 的焦點環改用 `--gm-focus-ring` |
| L | 註解與文件過時（shell、proposal、design-rules 的開頭說明；ADR-021；e2e README） | **修正** |
| L | 浮動工具列在 721–790 px 可能放不下 | **修正**：`.toolbar` 允許換行 |
| L | 一頁可同時出現 Publish（黑）與 Send to the AI（紫）兩個實心按鈕 | **不改**：「一個主要按鈕」規則仍成立；AI 按鈕的紫色是 owner 選的示意 |
| L | 中文標題的負字距；3D 模式下側欄高度跟著變短 | **不改**：影響小，記錄於此 |

## 5. 刻意不改與限制

- 截圖 baseline（`/`、`/projects`、`/no-such-page`、`/ui-gallery`，共 32 張）全部會變：由 CI 的 `update_screenshots` 重拍，交 owner 確認後才 commit（H-028 的 baseline 是 owner 確認過的）。在那之前，CI 的截圖比對會失敗。
- 檢查面板沒有做成示意圖上的分頁（Properties｜Ask the AI），而是由上到下排列：問 AI 在上，步驟屬性在下。分頁會在切換時卸載表單、丟掉已輸入的文字。
- live 模型上原本就有兩個 AI 提出的情境，名稱是原始 ID（例如「Increase ict_tester capacity from 2 to 3…」），沒有刪除，由 owner 決定是否清理。
- 載入中與失敗頁沒有頁首（與切片前相同）。

## 6. 命令與結果（2026-10-29，本機 WSL2；Node 24.21.0；Chromium，Playwright 1.56.1）

| 命令 | 結果 |
|---|---|
| `make frontend-test`（最後一次） | exit 0：lint、typecheck、**1121 個 unit test 通過**（比先前多了 accent 的對比組合） |
| `make e2e-test`（第一輪） | 75 個以上失敗後中止：e2e 寫死舊的頁面底色（`sheet`）。修正後，a11y 與 theme 的 177 個測試通過 |
| `make e2e-test`（第二輪，review 前） | 124 failed（全部是 `simplicity.spec` 的頂列 48 px、390 px 時折行），394 passed，32 skipped |
| `make e2e-test`（review 修正後） | exit 0：**518 passed**，32 skipped（截圖，只在 CI 的固定 image 比對），17.1 分鐘 |
| `make spec-check` | exit 0（`checks/docs-check.json` 已更新） |
| 截圖（本機 gallery build：1280 與 390、淺色與深色、en 與 zh-Hant） | 無水平溢出、無 page error。390 px 時頂列在兩種語言都是一行 48 px |

`make backend-test`：本次沒有改 Go 的產品程式碼。同一個 commit 帶上 `internal/ai/provider/segment.go` 的計時修正：延遲改從設定逾時之前開始計。CI 在 `b553195` 與 `983cfeb` 量到 47 ms 與 49 ms，低於 50 ms 的逾時。修正後 `go test -count=200 -run TestASegmentationsLatencyIsReturned` 全部通過，`make backend-test` exit 0（85 個 package ok）。`make backend-race`、`make integration-test` 本機 NOT RUN（交給 CI）。Live NOT RUN：待部署。
