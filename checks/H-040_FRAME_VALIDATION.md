# H-040 驗證報告：頁首帶、側欄與儀器按鍵（hackathon 切片）

**日期：2026-10-29｜範圍：H-038 的 UI 收尾（10/29–11/01）｜依據：owner 的回答 H-040，10/19 採用的原型（`checks/evidence/2026-10-19-T-045/prototypes/workspace.html`），`DESIGN.md` Simplicity（H-027）｜需求：REQ-022、REQ-023**

## 1. Owner 的決定

H-039 上線後 owner 說：「我能理解我想要的是極簡風，但這個會不會也太簡潔的，button 的樣式感覺完全沒設計過．整體 layout 也感覺沒有設計過」。

我把原型 `workspace.html` 和現在的 app 並排截圖。差別在 app 拿掉了原型的框架：來源列（Version／Draft／Scenario／Runs）、右側面板、帶圖示的按鈕。所有內容變成一條直欄往下排，按鈕只剩白底加灰框。

我做了一張示意圖給 owner：現在的按鈕、兩種新按鈕，以及模型頁和結果頁套回原型框架的樣子。Owner 選了「套回原型框架」和「A 儀器按鍵」，記錄在 `docs/DECISIONS_AND_TBD.md` H-040。

## 2. 改動

| 範圍 | 改動 | 檔案 |
|---|---|---|
| 按鈕 | 儀器按鍵：Rule 邊框、Control Edge 底邊；hover 時整圈 Secondary Graphite。主要動作的標籤前有 16 px 線條圖示（間距 6 px）。Select 的按鈕和 Add step 選單用同樣的鍵 | `ui/button.*`、`ui/icons.tsx`、`ui/menu.tsx`、`ui/select.module.css` |
| 頁首帶 | 新元件 `PageHeader`。Pale Shell 從左緣到右緣，底部以 Control Edge 收邊。內容依序為：返回的上層連結（麵包屑，分隔線 `/` 為畫出的符號，不朗讀）、標題、來源列，來源列右端放動作。手機版的來源列改為兩欄 grid | `ui/page-header.*`、`ui/tokens.css`（`--gm-gutter-start/end`） |
| 模型頁 | 頁首帶顯示最新版本、領域、草稿（狀態與 revision），右端是 Discard、Save draft、Publish；拿掉「Back to project」和摘要句。右側 360 px 面板：上方「問 AI」，下方步驟屬性；面板與畫布欄等高，超出時在面板內捲動 | `model-view.tsx`、`model-editor.tsx`、`editor.module.css`、`ask-form.tsx`、`photo-form.tsx`、`proposal.module.css` |
| 結果頁 | 頁首帶的麵包屑為 Model / Scenario。內容分兩欄：左欄是比較表、3D、Marey 圖；右側 400 px 面板是 AI 說明與執行設定，兩個執行 ID 各占一行。1100 px 以下改為單欄 | `run-results.tsx`、`results-view.tsx`、`results.module.css` |
| 提案頁 | 標題與狀態（`role=status`）放在頁首帶。Approve、Reject 及其確認按鈕加上圖示 | `proposal-view.tsx` |
| 情境頁、專案頁 | 套用頁首帶 | `simulate-view.tsx`、`app/projects/*` |
| 文案 | 返回連結改為麵包屑標籤：Project／Model／Scenario；zh-Hant「所屬專案」，避免和頂列的「專案」同名。新增 `nav.trail`、`band.*`；刪除 `model.summary` | `messages/*` |

規格同步：`DESIGN.md`（Buttons、Layout 的頁首帶、編輯器、結果頁、問 AI）；`docs/DECISIONS_AND_TBD.md` H-040。

## 3. 測試

新增或改寫的測試（沒有降低任何斷言）：

- `proposal-names.test.tsx`：AI 文字不得變成元素。檢查範圍從 `<article>` 擴大到整頁（含頁首帶）。只排除 app 自己畫在「有文字標籤的按鈕」裡、`aria-hidden` 的圖示。payload 加入 `<button><svg aria-hidden="true">`：它和 app 圖示同形但沒有標籤，一旦被渲染成元素仍會被抓到。連結檢查仍只允許返回模型的那一個。
- e2e `proposal.spec.ts`：惡意文字測試分別檢查 article 與頁首帶。不得出現 img、script、iframe、object、embed，也不得有隱藏字元。頁首帶只能有返回模型的連結，article 不得有連結。模型頁的版本與領域改為檢查頁首帶。
- `proposal-ask.test.tsx`、`editor.test.tsx`：最新版本改從頁首帶的 `<data value>` 讀。
- `results-view.test.tsx`（zh-Hant）：完成後兩個執行的狀態在頁首帶，Runs 區塊不出現。
- `proposal-preview.test.tsx`：gallery 狀態連結的計數只看 gallery 的 nav，因為頁首帶多了一個麵包屑 nav。

## 4. 獨立 review（H-029 一次）

| # | 發現 | 處理 |
|---|---|---|
| H1 | e2e 仍引用已刪除的 `model.summary`。惡意文字測試的連結與狀態移出 `<article>`，測試會失敗；最容易的錯誤修法是放寬斷言。截圖 baseline 是舊的 | **修正**：e2e 改查頁首帶，斷言如第 3 節，沒有放寬；baseline 由 CI 重拍，交 owner 確認 |
| M2 | `main:has(.band)` 讓所有頁面滿版：專案列表、提醒、問題框、情境頁摘要、Runs 區塊在寬螢幕上拉到約 1890 px | **修正**：清單、Callout（含 ProblemView）、simulations 的 section 上限為 `--gm-content-width`；情境頁摘要用 `.intro`（720 px）|
| M3 | DESIGN.md 自相矛盾（「version and domain stay in the line under the model's name」、結果頁「no side panel」、「Above the editor」）；結果頁的頁首帶沒有來源列 | **修正**：DESIGN.md 三處改寫。結果頁在兩個執行都完成時，頁首帶的來源列顯示 Baseline／Scenario（Completed、重複次數），Runs 區塊（進度、失敗、取消）只在完成前出現，每件事只說一次 |
| M3（工具列） | H-040 示意圖裡畫布工具列浮在畫布上，實作仍是畫布上方一條 | **不改**：浮動會蓋住節點；它在框內、有分隔線，外觀已經是工具列。列為 owner 可再決定的項目 |
| M4 | 手機（≤720 px）的模型頁不再有「問 AI」 | **修正**：手機版在「需要較大螢幕才能編輯」提示下方顯示表單 |
| L5 | Gallery 預覽的頁首帶貼在狀態連結下；`PageHeader` 沒有 gallery 項目與單元測試 | **不改**：gallery 的外觀問題；元件由各頁的測試涵蓋 |
| L6 | 載入中、失敗、未登入時沒有 h1 與返回連結 | **不改**：這個切片之前就是這樣（h1 本來就只在載入後出現）|
| L7 | 圖示的排除條件過寬 | **修正**：只排除有文字標籤的按鈕內的圖示，payload 加入同形的 `<button><svg>` |
| L8 | `ids.status` 沒有被 `aria-describedby` 引用 | **不改**：無害 |
| e2e 第一輪（review 修正前）另有 | 結果頁加了側欄後主欄變窄，zh-Hant 在 1280 px 時，指標名稱「完成的工作項目（每次重複的平均）」溢出格子 3 px（a11y 的 `cellOverflow` 2 個測試）。原因是 Chromium 把行尾的「）」修剪成半形，排版時的寬度卻沒有跟著縮小 | **修正**：表頭格 `text-spacing-trim: space-all`；實測 130/127 → 不溢出 |

## 5. 刻意不改與限制

- 畫布工具列仍是畫布上方的一條，沒有浮在畫布上（理由見 review M3），由 owner 再決定。
- 結果頁的 Runs 區塊在兩個執行都完成後不再出現，狀態改由頁首帶顯示；執行中、失敗、停止時區塊照舊。
- 載入中與失敗的頁面沒有頁首帶（與切片前相同）。
- `PageHeader` 沒有放進 gallery，也沒有自己的單元測試。
- 截圖 baseline（`/`、`/projects`、`/no-such-page`、`/ui-gallery`）會因按鈕與頁首帶而不同：由 CI 重拍（`update_screenshots`），交 owner 確認後再 commit（H-028 的 baseline 是 owner 確認過的）。

## 6. 命令與結果（2026-10-29，本機 WSL2；Node 24.21.0；Chromium，Playwright 1.56.1）

| 命令 | 結果 |
|---|---|
| `make frontend-test`（最後一次） | exit 0：lint、typecheck、**1103 個 unit test 通過** |
| `make e2e-test`（review 修正前） | 6 failed（e2e 仍引用 `model.summary` 3 個、惡意文字測試 1 個，皆為 review H1；CJK 表頭溢出 2 個），512 passed，32 skipped |
| `make e2e-test`（修正後） | exit 0：**518 passed**，32 skipped（截圖，只在 CI 的固定 image 比對），17.0 分鐘 |
| `go test -count=1 ./internal/domainpack/electronicsline/`（讀 UI catalog 的測試） | ok |
| `make spec-check` | exit 0（`checks/docs-check.json` 的 markdown 檔數更新） |
| 截圖（本機 gallery build：1280 與 390、淺色與深色、en 與 zh-Hant） | 無水平溢出、無 page error。證據：`checks/evidence/2026-10-29-h040-frame/`（含給 owner 的示意圖 `owner-mock.png`） |

`make backend-test`、`make backend-race`、`make integration-test` NOT RUN（沒有改 Go；讀前端 catalog 的 Go 測試已單獨跑過）。Live NOT RUN：待截圖 baseline 經 owner 確認後部署。
