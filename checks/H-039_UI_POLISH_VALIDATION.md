# H-039 驗證報告：模型頁、提案審查頁、結果頁的 UI 收尾（hackathon 切片）

**日期：2026-10-29｜範圍：H-038 排定的 10/31–11/01 UI 收尾，提前開始｜依據：impeccable critique（2026-10-29，雙 agent：設計審查與 detector 分開），owner 的回答 H-039，`DESIGN.md` Simplicity（H-027）｜需求：REQ-022、REQ-023**

## 1. 審核與 owner 的決定

- 審核紀錄：`.impeccable/critique/2026-10-28T16-51-01Z__apps-web-app-ui-gallery.md`。Nielsen 10 項 24/40（Acceptable），P1 三項、P2 四項。
- Detector：CLI 9 個目錄 0 個發現（以埋了問題的檔案確認 detector 有效）。瀏覽器 overlay 的真問題只有提案頁、執行狀態的行長；停用按鈕的對比、32 px 按鈕、五級字級是誤報（DESIGN.md 的規格）。
- Owner（H-039）：AI 表單保持展開但壓縮；結果頁收起重述的內容、刪掉「Both runs completed.」框；P1、P2 和小問題全修。

## 2. 改動

| 頁面 | 改動 | 檔案 |
|---|---|---|
| 模型頁 | AI 表單：說明與資料告知併成一行（`TextField` 的 `description` 與 `notice` 在同一個 `<p>`），文字框 2 行（`rows`），「AI 提出」選單、提示、Send、照片按鈕同一列；畫布在 1280×800 時從 y≈650 上移到 y≈583 | `ui/field.tsx`、`ask-form.tsx`、`photo-form.tsx`、`proposal.module.css` |
| 模型頁 | 「domain electronics_line」改為 pack 的名稱（catalog `domain.<id>.name`，D-117 (h)；沒有時仍顯示 ID） | `model-view.tsx`、`messages/*` |
| 結果頁 | 前置時間中位數那句：scenario 值綠色 600、放在 `<data value>`（DESIGN.md Words around it）；其下 12 px 說明重複次數與「server 有計算的指標，表中有 95% 區間」 | `results-view.tsx`、`results.module.css` |
| 結果頁 | 「Both runs completed.」：只播報不顯示。播報區從一開始就是隱藏的 live region（`StatusRegion quiet`），外觀不在播報時改變；執行停止時另以 Outcome 框顯示（`ShownStatus`，`aria-hidden`，避免重複朗讀） | `ui/status.*`、`run-results.tsx`、gallery `preview.tsx` |
| 結果頁 | AI 說明：兩段摘要與「What changed」收進一個關閉的 `<details>`（不刪）；Why、假設、不確定性、下一步保持展開 | `analysis.tsx`、`messages/*` |
| 結果頁 | 負數用減號 U+2212（`<data value>` 的原值不變；server 的 analyst 檢查本來就接受「−」，`internal/ai/locale/locale.go`） | `format.ts` |
| 結果頁 | 手機（≤720 px）比較表：每列為 grid，名稱一行、兩次執行一行、Δ 與 Δ % 一行，欄頭同樣排法；表格明示 table／row／cell 等 role（display 改變時保留語意）；caption 移到表格上方 | `metrics-table.tsx`、`results.module.css` |
| 提案頁 | 過期（base 不是最新）的 ready 提案，狀態列寫「Out of date…cannot be approved」，不再寫「Ready for your review」 | `proposal-view.tsx`、`messages/*` |
| 提案頁 | 批准確認的按鈕改為「Yes, create version N」／「Yes, save the scenario」，與 Approve 不同字 | `proposal-view.tsx`、`messages/*` |
| 提案頁、結果頁 | 段落行長上限 75ch（`.meta`、`.words`、`.hint`、Outcome 框） | `proposal.module.css`、`ui/status.module.css` |
| 3D | 第一個視角（52°）以透視計算距離，容納每個步驟的每個角，結果頁再加上播放欄位可達的最高高度（此時視角看向欄位高度的一半，產線與欄位共用畫面）；畫面寬度隨欄位、高度隨產線形狀（240–520 px）；編輯器內 3D 的說明文字與框內距一致 | `scene.ts`（`fitDistance`、`stageAspect`）、`engine.ts`、`viewer.tsx`、`twin-3d.module.css`、`model-editor.tsx`、`editor.module.css` |

規格同步：`DESIGN.md`（3D、AI 表單、提案、結果頁兩條 H-039）；`docs/DECISIONS_AND_TBD.md` H-039。

## 3. 測試

新增或改寫（沒有降低任何斷言）：

- `twin-3d.test.tsx`：第一個視角以獨立的透視投影（照 `engine.ts` 的相機擺法、每個方塊依自身旋轉）檢查每個角都在畫面內，編輯器（無欄位）與結果頁（有欄位）各在 5 種畫面比例；第一個視角填滿限制方向的 85% 以上；畫面的 aspect-ratio。Mutation：拿掉透視項、拿掉欄位高度，皆 KILLED。
- `results-view.test.tsx`：AI 摘要在關閉的 `<details>` 裡且含「What changed」的數字，Why 不在其中；完成的播報是 quiet live region；停止時 live region 是 quiet、Outcome 框顯示同一句；開頭那句的 scenario 值在 `<data value>`；說明的重複次數；減號。
- `proposal-view.test.tsx`：過期提案的狀態列；確認時 Approve 的字樣不在畫面上。
- 文字變更：`proposal-*.test.tsx`、`results-metrics.test.ts`、e2e `proposal.spec.ts`（確認按鈕、網域名稱）、`results.spec.ts`（停止的 Outcome 框）。

## 4. 獨立 review（H-029 一次）

| # | 發現 | 處理 |
|---|---|---|
| H1 | 3D 畫面的 `min-height` 經 aspect-ratio 變成最小寬度：390 px 時頁面左右溢出，編輯器內右側被裁 | **修正**：`.stage` 加 `width: 100%; min-width: 0`；e2e 的 390 px 播放測試（原本失敗）通過 |
| H2 | 手機版隱藏的「Metric」欄頭被 a11y e2e 的 `cellOverflow` 判為裁切（8 個測試） | **修正**：「Metric」改為單獨一行，不隱藏 |
| M1 | 取景沒算透視與播放欄位：近排步驟出畫面、滿高的欄位超出上緣 | **修正**：`fitDistance` 依每個角在相機空間的深度計算；有結果時加 `COLUMN_MAX`；`stageAspect` 用同一組角 |
| M2 | live region 在播報的同一次 render 變成隱藏，不確定每個螢幕閱讀器都會唸 | **修正**：live region 從一開始就隱藏；停止時另以 `aria-hidden` 的 Outcome 框顯示 |
| L1 | 連按兩次 Enter 仍會批准（焦點仍移到確認按鈕） | **不改**：DESIGN.md 的原位確認與 e2e 依此設計；按鈕已改字。是否改焦點由 owner 決定 |
| L2 | DESIGN.md 寫「四個數字一行」與實作不符；caption 位置 | **修正** |
| L3 | zh-Hant 說明用了「server」，catalog 其他地方用「伺服器」 | **修正** |
| L4 | `.summary` 加了 margin，影響 AI 摘要 | **修正**：開頭那句改用自己的 `.lead` |
| L5 | 測試缺口：停止的 Outcome、Approve 字樣、390 px 3D | **修正**：前兩項加了 unit test；390 px 3D 由既有 e2e 播放測試涵蓋 |

## 5. 刻意不改與限制

- 變更列的「Burn-in Smallest batch」不加分隔符號：名稱已是粗體；加「·」會改動大量逐字比對的 diff 測試，效益小。
- 「0.000309 min」不四捨五入：`format.ts` 刻意不把小數捨成 0（不把等待寫成沒有）。
- 「Medium risk」的原因：contract 沒有 server 風險原因的欄位，要加須先改 schema 與 server，不在本切片。
- 箭頭用次要灰色（DESIGN.md）：catalog 的 lint 不允許 JSX 字面箭頭，箭頭留在訊息裡；只強調 scenario 值。
- 3D 方塊的名稱只在 hover 顯示；編輯器的 undo／redo、Ctrl+S；失敗執行的標題用詞；「No percentage」每列重複：未處理。
- AI 表單只上移約 70 px（先前對 owner 說約 170 px，已更正）。

## 6. 命令與結果（2026-10-29，本機 WSL2；Node 24.21.0；Chromium，Playwright 1.56.1）

| 命令 | 結果 |
|---|---|
| `make frontend-test` | exit 0：lint、typecheck、**1103 個 unit test 通過** |
| `make e2e-test`（review 修正前） | 12 failed（H1 2 個、H2 8 個、開頭那句的 scenario 值不在 `<data>` 2 個），506 passed，32 skipped |
| `make e2e-test`（review 修正後） | exit 0：**518 passed**，32 skipped（截圖，只在 CI 的固定 image 比對），17.0 分鐘 |
| 取景中心上移後：`npx playwright test tests/results.spec.ts tests/editor.spec.ts -g "3D"` | 4 passed；`twin-3d` unit 11／11、playback 6／6；eslint、`tsc` 無錯 |
| 截圖（本機 gallery build，1280 與 390、淺色與深色、en 與 zh-Hant） | 手機版表格說明被擠成一欄、Δ 溢出：已修正並重拍確認。證據：`checks/evidence/2026-10-29-ui-polish/` |
| `make spec-check` | exit 0 |

CI 的截圖 baseline（`/`、`/projects`、`/no-such-page`、`/ui-gallery`，TEST-033）不含這三頁；`/ui-gallery` 有 TextField，其說明改放在一個 `<p>`（外觀應相同）。本機不能比對（只在 CI 的固定 image）：push 後看 CI。

### CI（66d4d48）與修正

`66d4d48` 的 CI：spec-check、frontend、browser checks（含截圖比對）、db＋integration、containers 通過；**backend 失敗**：`TestCataloguesHaveUILabels`（`internal/domainpack/electronicsline`）要求 UI 的每個 `domain.electronics_line.*` 標籤都是 pack 自己的，新增的 `domain.electronics_line.name` 不是。原本以「沒有改 Go」為由沒跑 `make backend-test`，判斷錯誤：Go 測試也讀前端的 catalog。

修正：pack 加上 `Name`（"Electronics assembly and test line"），prompt 的 summary 改由它組成（位元組不變，`aicontext` 的 prompt 雜湊測試通過）；測試要求 UI 的名稱等於 `Name`。

| 命令 | 結果 |
|---|---|
| `make backend-test` | exit 0：**85 個 package ok** |

`make backend-race`、`make integration-test` 本機 NOT RUN（Go 只加一個常數）。前兩次 push（`14bb5f2`、`007ff57`）的 CI 失敗是 spec-check：新增驗證文件後沒有更新 `checks/docs-check.json`；`66d4d48` 已更新且 spec-check 通過。

`f06ca92` 的 CI（run 40744508253）：6 個 job 全部通過（spec-check、frontend、browser checks 含截圖比對、db migration＋API 整合、backend 含 race、containers）。

### Live（2026-10-29，Railway，`f06ca92`）

`deploy.sh --apply`：18 個 migration 已記錄；api、worker、web SUCCESS；`/healthz`、`/readyz` 200，`/v1/me` 401，Postgres 沒有開著的 TCP proxy；完成後 `railway logout`。以評審帳號在 Chromium 檢查（沒有發出新的 AI 請求）：

| 檢查 | 結果 |
|---|---|
| 結果頁開頭那句 | scenario 值在 `<data value="461.108…">`，顯示「461.11 min」，綠色 rgb(11,107,75)；其下說明「From 5 replications of each run…95% intervals.」 |
| 「Both runs completed.」 | 只在 quiet live region（`data-quiet`），畫面上 0 個可見 |
| 減號 | 頁面 26 個 U+2212，表格中沒有以「-」開頭的數字 |
| 手機（390 px） | 結果頁、模型頁無水平溢出；比較表的列為 grid；結果頁 3D 畫面 358×240 |
| 模型頁 | 顯示「Electronics assembly and test line」，不顯示 `electronics_line`；文字框 2 行；Send 在 y=325，2D 畫布在 y=520；1280 無溢出 |
| 提案頁 | 兩個既有提案分別是「could not be written」與「needs more information」，沒有可批准的提案 |
| Page errors | 無 |

NOT RUN（live）：AI 摘要的 `<details>`（頁面要再問一次 AI 才會顯示說明，為省 live 呼叫未問；unit 與 e2e 已涵蓋）；確認按鈕的新字樣與過期狀態（live 沒有 ready 的提案；unit 與 e2e 已涵蓋）。
