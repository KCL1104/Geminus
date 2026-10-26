# T-032 驗證報告（hackathon 切片）：從 scenario 執行模擬、進度、Marey 圖與主要指標比較

**日期：2026-10-26｜狀態：hackathon 切片（H-029）的 agent 實作完成，分支 `t032-hk`（自 c7cf86c），沒有 push；待 coordinator 整合與獨立 review（H-003；比賽期間依 HACKATHON_PLAN 的流程）。`planning/tasks.json` 的 T-032 維持 `not_started`：依指示改為 `in_progress` 時 `make spec-check` 失敗（`spec-decisions`：「T-032: started before its dependencies」，相依的 T-030、T-031 尚未開始；`checks/evidence/2026-10-26-T-032/spec-check-with-in-progress.log`），依 AGENTS.md §1 停下這一處、還原並記錄（T-015、T-051 的切片分支也是 `not_started`），由 coordinator／owner 決定（見「需要 owner 決定或知悉」第 1 點）。完整 T-032 的其餘 scope 未做（見「T-032 還剩什麼」）。｜需求：REQ-012、REQ-013、REQ-014、REQ-015、REQ-022、REQ-023（切片相關的部分）｜Gate：D-090（APPROVED，H-024）；並依 D-088（APPROVED）顯示可比性與 delta｜驗收測試：TEST-022（baseline 為 0 時沒有百分比）、TEST-024（不可比時不顯示差異）、TEST-031（雙語）、TEST-033（axe、鍵盤、簡潔；截圖 NOT RUN）的切片部分**

範圍（coordinator 的指示與 tasks.json T-032 的 hackathon 切片一行）：

1. 從 scenario（與它的 baseline）以既有 API 開始執行，顯示進度與狀態（queued／running／completed／failed／cancelled）與取消，錯誤沿用既有的 problem 處理；
2. 結果畫面：baseline 與 scenario 的主要指標並列與差異（不捏造指標，API 沒給的顯示「無資料」；數字依語言格式化，值不變）；取樣追蹤的 Marey 圖（SVG、可及：文字摘要／表格替代、無互動元素所以沒有焦點停留、不只靠顏色：虛線與實線承載差異）；engine_version 不同時（D-088）說明無法比較；
3. 追蹤 → 圖形的純函式（fixture 單元測試、決定性）、以 fake API 的元件測試（狀態轉換、取消、失敗、engine 不同、空追蹤）、簡潔測試、axe（vitest 與 Playwright）、en＋zh-Hant 字串；
4. 不改既有畫面外觀、不碰 `apps/web-e2e/tests/__screenshots__/`；新頁面不在截圖 spec 中，截圖列 NOT RUN；
5. 不加圖表套件：手寫 SVG。

analyst（T-030）不在切片內。**沒有改 contract、OpenAPI、Go 或 migration**：既有的 T-023／T-029 endpoint 足夠（見「API 缺口」）。沒有新依賴（`package.json`／lock 未變），所以 ADR-011 與 THIRD_PARTY_NOTICES 不需更新。沒有 live AI 呼叫。

## Commit

| Commit | 內容 |
|---|---|
| ab66b8d | 先寫的測試與純邏輯：API client 的 5 個 run 方法（`runs-api.test.ts`）、`features/results/marey.ts`（追蹤 → 圖形）、`metrics.ts`、`format.ts`、`features/simulations/run-config.ts`、`run-pair.ts` 與其測試；synthetic 電路板產線 fixture（真的 engine 輸出，見「Fixture 的來源」） |
| a90ae6b | 畫面：`/scenarios/[id]`、`/scenarios/[id]/results`、`/ui-gallery/results`；Button 的 `run` variant；雙語 catalog；DOM、簡潔、review 頁、run button 的測試；design-rules／tokens／server-only 的掃描範圍加入 `features/` |
| 66db83c | 瀏覽器檢查（`helpers.ts` 的 route、`results.spec.ts`）；手機上的表格捲動區（先紅後綠）；trace 表格改為每列一個步驟；review 頁改在 request 時讀 engine 輸出（bundled JSON 的數字被改，見「瀏覽器檢查抓到並修正的問題」）與防止再 bundle 的掃描；vitest 在高負載下的 poll 間隔 |
| e1cac95 | 兩個 run 的結果（都完成／沒有可比較的）以頁內的 status 區域宣告（WCAG 4.1.3；先紅後綠，`red/results-view-outcome.test.log`），取代原本的 info note |
| （最後一個 commit） | DESIGN.md 的 Simplifications、重產的 `checks/docs-check.json`、本報告與 evidence |

## 交付內容

| 位置 | 內容 |
|---|---|
| `apps/web/lib/api/client.ts` | `createSimulation`（202 `RunAccepted`，必帶 Idempotency-Key）、`getSimulation`、`cancelSimulation`（body `{}`＋key）、`getSimulationResults`、`downloadSimulationTrace`；ID 與 key 在送出前檢查（不合格不送出） |
| `apps/web/features/simulations/run-config.ts` | 執行設定：預設為模型時間單位的一天、暖機 0、5 次重複、seed 1；表單檢查（必填、數字、>0、暖機 < 長度、重複 1–1000、seed 0–2^53−1）；兩個 request 共用同一個 config（SIMULATION_SPEC §7 的可比條件）：baseline＝scenario 的 base version＋`scenario_patch: []`，scenario＝`scenario_id`；trace 一律 `sampled`；同一設定重試沿用同一組 key（D-011） |
| `apps/web/features/simulations/run-pair.ts`、`use-run-pair.ts` | 兩個 run 的狀態：每秒讀一次（worker 約每秒寫 progress，T-023），兩者都結束、讀取失敗（等重試）或 run 不屬於此 scenario 時停止；回應依 `progress.revision` 只往前（晚到的 poll 不會蓋掉已回覆的取消）；failed run 的 `error` 以 API 的 problem 顯示（code 不在 contract 中為 protocol problem）；取消只送給仍在 queued／running 的 run，各自的 key |
| `apps/web/features/simulations/simulate-view.tsx`、`run-status.tsx` | scenario 頁的表單與 run 按鈕；兩個 run 的狀態列（線條圖例、狀態、進度條 `role=progressbar` 與「2 of 5 replications」、停止中、失敗的 problem）與一個取消按鈕（editor／owner 才有） |
| `apps/web/features/results/marey.ts` | 純函式：`stepRows`（source、process、delay、sink 依流程順序；rework 的回邊不影響順序；sink 在最後）、`rowResolver`（queue 歸到它唯一供給的步驟）、`entityPaths`、`assignLanes`、`timeTicks`、`fitLabel`、`mareyLayout`、`traceSummary` |
| `apps/web/features/results/metrics.ts`、`format.ts`、`metrics-table.tsx` | 主要指標：固定的 run 層級列（完成數平均、前置時間中位數與 p95、各次重複平均的平均（有才列）、失敗、重工），再依 server 的順序列出每個 resource 的使用率與每個 queue 的平均等待，pack／DORA 的 mean 以 key 原樣列出；值、delta、status、reason 都直接取自 comparison；每個數字以 `<data value>` 保留 server 的原值；各 run 自己的 95% CI（只有 replication population 的 mean 有） |
| `apps/web/features/results/marey-chart.tsx`、`results-view.tsx`、`run-results.tsx`、`load.ts` | 結果頁：兩個 run 的結果以一個 status 區域宣告（「兩個執行都已完成。」或沒有可比較的；進度本身不每秒宣告，WCAG 4.1.3）；不可比的說明（逐項原因）、warnings、前置時間中位數的一句話、指標表、Marey 圖（圖例、取樣說明、截斷／缺少的說明、圖與「以表格顯示圖表內容」）、執行設定（版本、run ID、長度、暖機、重複、seed、engine；兩者不同時並列） |
| `apps/web/app/scenarios/[scenarioId]/page.tsx`、`.../results/page.tsx` | 兩個新 route；結果頁的網址帶兩個 run ID（重新整理後仍在），缺少、重複或不合格時為 not found 且不呼叫 API |
| `apps/web/app/ui-gallery/results/` | 只在 `GEMINUS_UI_GALLERY=1` 時存在的 review 頁（否則 404），以 fixture 顯示同一組元件、每個網址一個狀態（結果、不可比、執行中、失敗）；不呼叫 API；它唯一的控制項（取消）會把畫面變成已取消 |
| `apps/web/ui/button.tsx`、`button.module.css` | 新 variant `run`：Deep Scenario Green 底、On Scenario 字（DESIGN.md Buttons 的 Run）；不改既有 variant |
| `apps/web/app/_components/states.tsx` | `Loadable` 加入 `scenario`、`runs`、`results`（catalog 的 `loading.*`） |
| `apps/web/messages/en.ts`、`zh-Hant.ts` | 新字串（`loading.*`、`scenario.*`、`run.*`、`runs.*`、`compare.*`、`metric.*`、`marey.*`、`facts.*`、`galleryResults.*`）；每個 ComparisonReason、IncomparableReason、ComparisonWarning 都有兩語說明 |
| `DESIGN.md` | Simplifications in the app 加兩點：只有一種執行方式所以沒有「More ways to run」、run 按鈕 hover 改深邊框（不能用 token 以外的顏色變亮 8%）；結果是獨立頁面、Marey 圖沒有 playhead／scrubber／row 按鈕（還沒有畫布跟隨）、指標表沒有 rug／metric 按鈕／全螢幕、CI 為 95%（API 只定義這個） |
| `apps/web/tests/` | 新增 `marey`、`results-metrics`、`runs-api`、`simulations`、`results-view`、`results-simplicity`、`results-preview`、`run-button`；`design-rules`、`tokens`、`server-only` 的掃描加入 `features/`（並要求兩個新 stylesheet 在掃描範圍內），`tokens` 加入新的對比組 |
| `apps/web-e2e/tests/` | `helpers.ts` 的 route 清單加入兩個新 route（未登入）與 review 頁的四個狀態（所以 a11y、simplicity、theme 的既有 spec 都會跑它們）；新增 `results.spec.ts` |

## 每個畫面的控制項（H-027）

所有畫面共用的 app strip 不變（Skip、Projects、Language、Theme；見 T-045 報告）。

| 畫面 | 控制項 → 用途 | 依據（需求／測試） |
|---|---|---|
| `/scenarios/[id]`（editor／owner） | Back to the model（連結）→ 回到模型頁（strip 沒有這個目的地）；Run length、Warm-up、Replications、Seed（欄位）→ 明確的 run config；Run baseline and scenario（run 按鈕，唯一的執行動作）→ 兩個 run 入列並前往結果頁 | T-032 scope「explicit run config」、REQ-012、SIMULATION_SPEC §7；`results-view.test.tsx`（入列的 body、key、重試沿用 key、欄位錯誤）、`results-simplicity.test.tsx` |
| `/scenarios/[id]`（viewer／角色未知） | Back to the model；沒有表單與按鈕，只說明不能執行 | API 的 x-required-roles（createSimulation 為 owner／editor）；`results-view.test.tsx`、`results-simplicity.test.tsx` |
| `/scenarios/[id]/results`（執行中） | Back to the scenario（連結）→ 改設定再執行；Cancel the runs（按鈕，editor／owner、有 run 仍在進行時）→ 取消仍在進行的 run | T-032 scope「queued/running/cancelled/progress」、TEST-021 的 UI 端；`results-view.test.tsx`（取消、停止中、viewer 沒有取消）、`results-simplicity.test.tsx`（連按兩次只送一次） |
| `/scenarios/[id]/results`（完成） | Back to the scenario；「以表格顯示圖表內容」（disclosure）→ 圖的文字替代；problem 的 Details（disclosure，有失敗時）；手機上指標表與追蹤表各自的捲動區（可聚焦，不是按鈕） | WCAG 1.1.1、H-027 漸進揭露；`results.spec.ts`（鍵盤開啟表格、捲動區可聚焦）、`results-simplicity.test.tsx` |
| `/scenarios/[id]/results`（讀取失敗） | Try again → 重新開始讀取 | 沿用 `RemoteView` 的可重試規則；`results-view.test.tsx`（失敗後重試並完成） |
| `/ui-gallery/results`（review 頁） | 同上的元件；執行中狀態的 Cancel the runs 會把 review 頁變成已取消 | T-045 的 gallery 原則（只供 review 與自動檢查，預設 404）；`results-preview.test.tsx`、`results.spec.ts` |

沒有加入：run 選單（More ways to run）、playhead／scrubber／播放、row 按鈕、全螢幕、rug、AI 分析入口（都還沒有功能，H-027）。

## Marey 圖的規則（`features/results/marey.ts`）

- **列**：source、process、delay、sink（queue、decision、failure、rework 不是步驟）；以 DFS 找出回邊（rework），在其餘的邊上取離 source 的最長路徑為順序，同順位依模型中的順序，sink 一律在最後。Scenario patch 不能增刪拓樸（D-021），所以兩個 run 共用 scenario 的 base version 的列。
- **事件 → 位置**：`arrival` 在 source 列；`queue_enter` 在該 queue 唯一供給的步驟（直接進 process 時就是 process，SIMULATION_SPEC §9）；queue 有多個出口時以同一 entity 之後的事件決定，仍無法決定時**該線在此結束**，不猜；`service_start`／`delay_start` 是離開該列的點，之後到下一列（斜線＝服務／移動）；等待＝在列中的水平線；`service_end` 之後被 `blocked` 時畫在兩列之間的水平線直到 `unblocked`（仍是「平＝等待」）；`complete`／`abandon` 落在 sink 列中央的 station line（DESIGN.md）；`failure`、`rework`、`setup_start` 不改位置。
- **結尾**：trace 沒有截斷時，結束時仍在等待的項目畫平線到 horizon（它確實在等待，`traceSummary` 計為「結束時仍在等待」）；正在服務的項目線停在開始服務處（去向未知）；**trace 截斷時一律不延伸**（SIMULATION_SPEC §9：不能補出虛構的軌跡），並在圖上方說明。
- **lane**：同一列中時間重疊的停留各自一條 lane（最低的空 lane，與 prototype 相同）；baseline 從列頂往下、scenario 從列底往上，兩者不共用 lane。
- **時間軸**：選最小可讀的單位（秒、分、時、天）與整齊的間隔（每兩個標籤至少 64 px）；主格線在每個標籤、次格線在每四分之一（DESIGN.md 的 Minor／Major Time Grid）。
- **每個 run 最多畫 400 條線**（取樣的前 400 個項目），超過時圖上方說明；fixture 每個 run 19 條。
- **線條**：baseline 1.4 px 虛線（5 3）石墨、scenario 1.75 px 實線深綠；release 點：baseline 空心、scenario 實心（DESIGN.md Marey Strip）。圖是 `role="img"`，有 `<title>` 與 `<desc>`（兩個 run 各畫幾條、平線代表等待）；圖中沒有可聚焦的元素。
- **寬度**：依實際寬度（ResizeObserver）重算幾何；第一次 render 以 1040 px 計算並以 `max-width: 100%` 縮放，所以不會造成頁面橫向捲動。

## Fixture 的來源

`apps/web/features/results/fixtures/board-line-*.json`（測試以 `board-line.ts` 讀檔；review 頁在 request 時讀檔，外框物件在 `board-line-objects.ts`；不以 module import JSON，理由見下一節的第一點）：一個合成的電路板產線模型（Boards in → SMT → AOI → ICT buffer → ICT → Pack → Shipped），baseline 與「ICT fixture 容量 2」的 scenario，600 分鐘、暖機 0、5 次重複、seed 42、sampled trace。Result、trace 與 comparison **不是手寫的**：以本 repo 的 `des.Engine.RunWithTrace`（des-go-0.5.0）與 `compare.Compare` 產生（暫時的 Go 測試檔，產生後刪除，內容存於 `checks/evidence/2026-10-26-T-032/fixture-generator/`），並以 repo 的 JSON Schema 驗證（Result、TraceArtifact、Comparison、IR 都通過；`fixture-schema-check.log`）。Run、scenario 與 version 物件是依 contract 手寫的外框（ID、時間、hash）。Result 中 `trace.artifact_id` 為 null（engine 不設，worker 上傳後才填）。

## API 缺口

沒有。切片需要的是 createSimulation、getSimulation、cancelSimulation、getSimulationResults、downloadSimulationTrace、compareSimulations、getScenario、getModelVersion，全部已有且由 T-023／T-029 測過。沒有使用 SSE（`streamSimulationEvents`）：每秒讀 run 已足夠，且經 BFF 的串流未在瀏覽器測過；列為後續。

## 測試

| 檔案 | Tests | 內容 |
|---|---|---|
| `marey.test.ts` | 28 | 列的順序（queue 省略、rework 回邊、平行分支、sink 最後、delay）；事件 → 位置（等待、服務、blocked 在兩列之間、source 被擋、結束時等待、截斷不延伸、服務中結束、rework 往上、無法放置的 queue、delay、abandon、交錯的 entity、空 trace）；lane；時間軸（6 種長度）；標籤截斷；版面（x 對應、baseline 頂／scenario 底、station line、只畫有的 run、上限、格線）；真實 engine 輸出：每次相同、線數與 release 數、都在圖內、ICT 的等待只在 baseline |
| `results-metrics.test.ts` | 12 | 列的順序與分組、值與 server 完全相同、標籤（模型名稱）、CI 只取 mean、缺少的列、pack／DORA 列與未知單位、不可比、RFC 6901 key、中位數的一句話只在可比時、en／zh 的數字、delta 的符號、百分點、relative × 100 |
| `runs-api.test.ts` | 6 | 5 個方法的 method、path、header、body、key；與 `planning/api-operations.json` 一致；409／404 的對應；不合格的 ID 與 key 不送出 |
| `simulations.test.ts` | 11 | 預設值、解析、每種欄位錯誤、兩個 request、名稱上限、key 的沿用；run 狀態只往前、reducer、failed run 的 problem、run 是否屬於此 scenario |
| `results-view.test.tsx` | 21 | scenario 頁（顯示、入列兩個 run 與 key、欄位錯誤不送出、失敗時說明是哪個並以同一 key 重試、viewer、not found）；結果頁（queued → running → completed 的轉換與 axe、server 的值與 `<data>`、圖與表格、取消、停止中、失敗、engine 不同、沒有 trace／trace 沒有事件、只有一邊有 trace、截斷、viewer 沒有取消、讀取失敗後重試、不屬於此 scenario、zh-Hant）；route（從網址讀 run、缺少或重複為 not found、未登入不呼叫 API） |
| `results-simplicity.test.tsx` | 23 | 兩語 × 11 個狀態（含 app shell）：沒有同名按鈕／連結、沒有兩個連到同處、至多一個 primary、沒有停用的控制項；取消連按兩次只送一次 |
| `results-preview.test.tsx` | 9 | review 頁預設 404、只接受自己的狀態、交出的資料與檔案相同、沒有 app module import fixture 的 JSON、各狀態 axe、取消有效 |
| `run-button.test.ts` | 2 | run variant 為 scenario 綠底與 on-scenario 字，且只有它用 scenario 色（mutation 之後補上） |
| `design-rules`、`tokens`、`server-only` | （既有） | 掃描範圍加入 `features/`；`tokens` 新增 3 組文字對比與 3 組非文字對比（run 按鈕、scenario 欄中的 CI、圖的兩種線在 sheet 與 scenario wash 上） |
| `apps/web-e2e/tests/results.spec.ts` | 見下 | 390／1280：線數、虛線與實線的計算樣式、圖在頁內、圖中沒有焦點；手機上數字不換行、表格在自己的可聚焦捲動區；兩語：以鍵盤開啟圖的表格、開啟後 axe、`<data>` 為 server 的值；鍵盤取消；兩語：所有字串是 catalog 或模型資料 |
| 既有 e2e spec | 見下 | `a11y.spec.ts`（新 route × 2 主題 × 2 語言 × 2 寬度，及選定主題）、`simplicity.spec.ts`（新 route × 2 語言 × 2 寬度）自動涵蓋新 route |

先紅後綠：每個新的測試檔在實作前執行並失敗（`checks/evidence/2026-10-26-T-032/red/`：模組不存在或斷言失敗）；之後實作讓它們通過。例外：review 頁的測試（`results-preview.test.tsx`）與 `run-button.test.ts` 是在程式之後寫的，改以 mutation 證明它們擋得下缺陷（下節）。

### Mutation（`mutants.txt`）

27 個手寫的 mutant（每個只改一處，跑對應的測試檔，之後還原；`scripts/mutants.py`、`mutants2.py`）：26 個被擋下，1 個等價，0 個存活。第一輪的 25 個中有 2 個存活：「中位數也取 CI」為等價（只有 replication population 的 summary 能有 CI，非 mean 的列只有 entity population 的前置時間，CI 一律 null，合法的 Result 無法區分）；「run 按鈕不是綠色」暴露沒有測試讀它的顏色 → 補 `run-button.test.ts` 後被擋下。bundled JSON 的發現之後另加 2 個（review 頁 import fixture JSON、交出與檔案不同的資料），都被擋下。

## 瀏覽器檢查抓到並修正的問題

- **Production build 改了 bundled JSON 的數字**：review 頁原本把 fixture 的 JSON 當 module import，`results.spec.ts` 比對 `<data value>` 與 fixture 檔案時失敗：頁面上是 `29.138691666666663`、檔案是 `29.138691666666666`（另一個 double；delta 也一樣，`red/e2e-results-bundled-json.log`）。`.next/static` 中的 chunk 確實把數字字面值寫成 `...663`：Next.js 16 的 minifier 改了 number literal。真正的結果頁不受影響（API 回應在執行時以 `JSON.parse` 解析），但任何 bundle 進瀏覽器的數字資料都可能被改。修正：review 頁（server component）在 request 時讀檔、以 props 傳給 client 元件（RSC 序列化保留原值）；`board-line-objects.ts` 不 import JSON；新測試掃描 `app`、`features`、`lib`、`ui`，任何 `.json` import 都失敗，另檢查 review 頁交出的資料與檔案相同（兩個 mutant 都被擋下）。修正後 e2e 的值與檔案相同。
- **手機上指標表的數字被拆成兩行**（例如「-9.37 分 / 鐘」）：先在 `results.spec.ts` 加「數字不換行、窄表格在自己的可聚焦捲動區」的檢查，以本機探針確認在 390 px 失敗（`red/phone-table-probe-before.log`：en 16 個、zh-Hant 14 個數字換行），再讓表格保持 600 px 的最小寬度、數字不換行，放進 `role="region"`、`tabIndex=0`、以標題命名的捲動區（頁面本身不橫向捲動）；之後 e2e 通過。
- **追蹤表格的 `th scope="rowgroup"` 在 Chromium 不是 row header**：改為每列一個步驟（`th scope="row"`），兩個 run 各三欄在 `colgroup` 標題下，所以每個格子都有列標題與兩層欄標題。
- `results.spec.ts` 自己的錯：沒有 disclosure 的頁面上點第一個 `summary` 會等到逾時；改為打開頁面上所有的 disclosure。
- Vitest 在高負載下（共用機器 load average 約 24–28）：每 1 ms poll 一次讓 `act()` 無法穩定，一個測試逾時後連帶後面的測試失敗；改為 25 ms、等待上限 10 秒。之後全套連續執行皆通過。

## 指令與結果（Node 22.22.0；`npm ci --engine-strict=false`，package.json 要求 Node 24）

（最後一次執行的 log 與 exit code 在 `checks/evidence/2026-10-26-T-032/`：`exit-codes.txt` 與各 log。）

| 指令 | 程式 | 結果 |
|---|---|---|
| `npm ci --prefix apps/web --engine-strict=false`、`npm ci --prefix apps/web-e2e --engine-strict=false` | — | exit 0、exit 0（lock 未變） |
| `make frontend-test`（lint、typecheck、vitest） | e1cac95 | exit 0：29 個檔案、713 個 test（開始前 21 個檔案、589 個；`frontend-test.log`）。同一套在高負載下連續跑時，既有的 `bff.test.ts`「streams an upload…」曾逾時一次，重跑通過（不是本任務的程式） |
| `make frontend-build` | e1cac95 | exit 0；新 route `/scenarios/[scenarioId]`、`/scenarios/[scenarioId]/results`、`/ui-gallery/results`（`frontend-build.log`） |
| `make e2e-test`（本機 Chromium build 1194） | 66db83c | exit 0：228 passed、32 skipped（截圖，只在 CI 的固定容器：NOT RUN）；開始前（c7cf86c，T-045 coordinator 的紀錄）為 133 passed、32 skipped（`e2e-test-66db83c.log`） |
| `make e2e-test` | e1cac95 | exit 0：228 passed、32 skipped（截圖 NOT RUN）。新增的 95 個都通過：`results.spec.ts` 11 個，以及兩個新 route（未登入）與 review 頁四個狀態在 `a11y.spec.ts`（60 個：2 主題 × 2 語言 × 2 寬度，加上選定主題；0 violation、390 px 無橫向捲動）與 `simplicity.spec.ts`（24 個）中（`e2e-test.log`） |
| `npm audit --prefix apps/web --omit=dev` | — | exit 0，0 vulnerabilities（`npm-audit.log`） |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（T-032 改為 `in_progress`） | — | **exit 2**：`spec-decisions` 的「T-032: started before its dependencies」（`spec-check-with-in-progress.log`）→ 狀態還原 |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（T-032 維持 `not_started`） | 最後的 tree | exit 0；重產的 `checks/docs-check.json` 只多了本報告（`spec-check.log`） |
| Fixture 的 schema 檢查（`fixture-generator/validate_fixtures.py`，repo 的 JSON Schema） | — | exit 0：IR、兩個 Result、兩個 TraceArtifact、Comparison 都通過（`fixture-schema-check.log`） |
| Mutation（`scripts/mutants.py`、`mutants2.py`） | — | 27 個：26 個擋下、1 個等價、0 個存活（`mutants.txt`） |
| 本機預覽截圖（`scripts/shots.mjs`，`next start` 在 port 3200） | e1cac95 | exit 0：12 張，每張的頁面 `scrollWidth` 等於視窗寬度（`local-shots/scroll-widths.txt`） |

e2e 的 `[WebServer] Error: failed to pipe response` 來自 `session.spec.ts` 刻意切斷的回應（T-045 報告已說明），不是失敗。共用機器上其他 agent 也在跑同一組固定 port（3100–3102）的瀏覽器檢查，所以每次 e2e 都先等 port 空出來（`scripts/run_when_free.py`）。Go 測試與 `make integration-test` 沒有執行：沒有改 Go、schema、OpenAPI 或 migration。

## 本機預覽截圖（不是 baseline）

`checks/evidence/2026-10-26-T-032/local-shots/`：review 頁（`/ui-gallery/results`）的結果與執行中狀態，en／zh-Hant × 1280／390 px × 淺色／深色（執行中只拍 1280），以本機 `next start`（port 3200）與本機 Chromium 拍攝，每張都記下頁面的 `scrollWidth`（等於視窗寬度，沒有橫向捲動）。**這些不是 baseline**：`apps/web-e2e/tests/__screenshots__/` 沒有被改動，新頁面也不在 `screenshots.spec.ts` 中。

## NOT RUN／限制

| 項目 | 原因 |
|---|---|
| 截圖比對（TEST-033） | 只在 CI 的固定容器執行；新頁面不在截圖 spec 中，是否加入與 baseline 由 owner 決定（H-028 的流程） |
| 已登入、有真實資料的頁面在瀏覽器中 | app 尚未掛 `AuthProvider`（`app/layout.tsx`），瀏覽器中所有頁面都是未登入；瀏覽器只檢查了未登入的兩個新 route 與 review 頁，已登入的行為由 vitest 以 fake API 檢查 |
| 與真的 API／worker 的閉環（建立 → worker 執行 → 比較 → trace） | 前端只用 fake API；後端的閉環已由 T-023／T-029 的 integration 測試涵蓋；端到端閉環屬 T-040 |
| Go 測試、`make integration-test` | 沒有改 Go、schema 或 migration |
| SSE 進度（`streamSimulationEvents`） | 未使用（每秒讀 run）；後續 |
| Node 24 | 本機只有 Node 22.22.0 |
| 螢幕報讀器、Firefox、Safari | 需要人執行；P0 只跑 Chromium |
| push 與 CI | 依指示不 push |

## T-032 還剩什麼（切片以外）

- analyst（T-030）的分析與「下一個實驗」的人類動作；
- SSE 進度、run 的列表與歷史（`listSimulations`）、trace level 的選擇；
- 指標的 population、sample count 與不確定性的完整呈現（目前只有各 run 自己的 95% CI）、rug、全螢幕 Compare、可選的指標；
- Marey 圖的 playhead／scrubber／播放、與 2D 畫布（T-015）同步的列選取；
- domain pack 指標（T-051）的名稱與單位（目前以 key 原樣顯示；review 第 1 輪後 `ratio` 已以百分比與百分點顯示，其他單位如 `entity_per_hour`、`entity` 仍在名稱旁原樣列出）；有 calendar 的 resource 的 scheduled utilization（D-084）；
- 已登入頁面的瀏覽器 E2E 與閉環（T-040）、截圖 baseline；
- 從模型頁進入 scenario 的入口（模型頁屬 T-015）。

## 需要 owner 決定或知悉

1. **T-032 的狀態**：指示要求改為 `in_progress`，但 `scripts/check_decisions.py` 規定相依任務都在 review／done 才能 in_progress（T-030、T-031 尚未開始），`make spec-check` 因此失敗。本分支沒有改狀態。選項：(a) 維持 `not_started`，切片的完成紀錄寫在 completion_evidence；(b) 由 owner 記錄 hackathon 切片可以先於相依任務開始（H-029 的補充），並在 check 中加入有紀錄的例外；(c) 新增一個「切片」狀態。建議 (a)，與 T-015、T-051 的切片一致。
2. **Demo 需要已登入的 app**：目前 `app/layout.tsx` 沒有掛 `AuthProvider`，瀏覽器中每個頁面都顯示「需要登入」（T-014／T-043 的既定狀態）。比賽的 demo 要在瀏覽器中走完整流程，就需要把 BFF 的 session 接到 `AuthProvider`（organization 與角色來自 `/v1/me`）；這不在本切片內（屬 T-043 的後續或 T-039／T-057 的部署切片），請指定由誰做。
3. **進入點**：結果流程從 `/scenarios/<scenario id>` 開始。T-031 的切片批准 scenario proposal 後，outcome 的下一步建議連到這裡；模型頁目前沒有 scenario 列表（屬 T-015 的模型頁）。
4. **預設的執行設定**：模型時間單位的一天、暖機 0、5 次重複、seed 1。電子產線的 demo 是否要其他預設（例如一個班 8 小時、10 次重複）？
5. **手機上的表格**：指標表在 390 px 以自己的捲動區橫向捲動（頁面不捲動、數字不換行）。可以接受嗎？
6. **DESIGN.md 新增的兩點簡化**（見「交付內容」）：run 按鈕沒有「More ways to run」、hover 改深邊框；結果是獨立頁面、圖沒有 playhead 等、95% CI。請確認。
7. **新頁面的截圖**：要不要把 review 頁（`/ui-gallery/results`）加入 `screenshots.spec.ts` 並在 CI 產生 baseline 給你確認？

## 殘餘風險

- Marey 圖的「blocked 在兩列之間」與「queue 有多個出口時以之後的事件決定」是本任務的解讀（DESIGN.md 與 prototype 沒有寫）；有單元測試鎖住，若 owner 要別的畫法需改 `marey.ts`。
- 每個 run 最多畫 400 條線；長 horizon、大量取樣時圖會很密（仍有說明與表格）。
- 一個既有測試（`bff.test.ts` 的 upload streaming）在高負載下曾逾時一次，重跑通過；不是本任務的程式。
- 列標籤的寬度以字元寬度估算（純函式），極長的名稱以「…」截斷，完整名稱在表格中。

## Coordinator 驗證（T-051＋T-032 合併後，2026-10-26）

T-051 的四個 commit（52a102b..cce4587）與 T-032 的五個 commit（f21b02a..cae974b）cherry-pick 到 T-015 已整合的分支上（55d0703 之後）。衝突：`apps/web-e2e/tests/helpers.ts` 的 routes（兩邊都保留：T-015 的 `/ui-gallery/editor` 與 T-032 的 scenario、results 與 review 頁）、`DESIGN.md`（T-015 與 T-032 各自新增的簡化條目都保留）、生成的 `checks/*.json`（以 `make spec-check` 重產）。

合併後出現一個語意衝突：T-032 把 `tests/tokens.test.ts`「自家 CSS 不載入任何東西」的掃描擴大到 `features/`，掃到 T-015 的 `features/model-editor/vendor.css` 對 React Flow `base.css` 的一個 `@import`（由 node_modules 打包，不是遠端或字型載入）。規則維持一樣嚴格、改為精確：每個自家樣式表只能含列出的套件 import，而被 import 的套件檔本身不得有 `@font-face`、`@import` 或 `url(`（8eecee4）。Mutant：多加一個字型服務的 import、改 import 另一個套件檔，測試都失敗。

指令與結果（evidence：`checks/evidence/2026-10-26-T-051-T-032-coordinator/`，`exit-codes.txt` 依執行順序；Node 22.22.0，go1.27.3）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test GOFLAGS=-count=1` | 0 | 75 個 package 都 ok |
| `make architecture-check` | 0 | 87 packages、23 rules、16 build configurations |
| `make backend-numeric` | 0 | |
| `make simulation-reference-test` | 0 | |
| `make ai-eval` | 0 | fake provider；live 為 NOT RUN |
| `go test -race -count=1`（simulation、results、domainpack、model、cmd） | 1 → 0 | 第一次只有 `internal/simulation/engine` 在 Go 預設的 10 分鐘逾時（腳本漏了 `make backend-race` 的 `-timeout 30m`；共用的 4 CPU，load 9–11）；其餘 19 個 package ok。以 `-timeout 30m` 重跑 engine：ok，604.8 s（`race-engine-30m.log`） |
| `make integration-test`（新的 dt_test_hk5132c，跑完刪除） | 0 | |
| `npm ci`（web、web-e2e，`--engine-strict=false`） | 0／0 | |
| `make frontend-test` | 2 → 0 | 第一次是上面的語意衝突（769／770）；修正後 770／770 |
| `make frontend-build` | 0 | |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| `make e2e-test` | 2 → 0 | 第一次是另一個 agent 的 e2e 占用 127.0.0.1:3101（本機沒有 `ss`，原本的 port 檢查沒有作用，改用 `/dev/tcp` 與 `pgrep`）；等 port 空出後單獨重跑：257 passed、32 skipped（全部是 `screenshots.spec.ts`，只在 CI 的固定環境比對） |

NOT RUN：截圖比對（只在 CI）；完整的 `make backend-race`（只跑改動的 package）；live AI；Node 24。

Coordinator 對 T-032 問題的處理（例行事項；需要 owner 的另列）：
1. 狀態採 (a)：維持 `not_started`，切片紀錄在 completion_evidence（與 T-015、T-051 一致）。
2. 已登入的 app：由 T-043 的後續（AuthProvider 接 BFF session 與 `/v1/me`）處理，agent 執行中。
3. 進入點：T-031 切片批准 scenario 後連到 `/scenarios/<id>`（已寫進 T-031 切片的指示）。
4. 預設執行設定：先維持；demo 的設定在 T-057 的 demo 腳本決定。
5. 手機上的表格在自己的捲動區橫向捲動：接受（DESIGN.md 允許表格在自己的 overflow 容器內）。
6. DESIGN.md 的兩點簡化：符合 owner 的簡潔要求（H-027），coordinator 接受，列在給 owner 的報告中。
7. 新頁面的截圖 baseline：等已登入的 UI 穩定後（T-043 後續、T-031），在 T-057 加入 `screenshots.spec.ts`，由 CI 產生、owner 確認（H-028）。

下一步：一輪獨立審查（H-029）。

## Review round 1 fixes（2026-10-26，分支 `t015-t032-m1`，自 d229225）

審查：`checks/T-032_REVIEW_r1.md`（CHANGES REQUESTED，M1；檔案在 reviewer 的 worktree，本分支未收錄）。本節修 M1（Medium）與 L4（Low，demo 看得到）；其他 Low 與 Info 依指示延到比賽之後（見下）。Commit：702607e。Evidence：`checks/evidence/2026-10-26-T-015-T-032-r1/fixes/`（`exit-codes.txt` 與各 log；指令表與 T-015 報告的同一節相同）。

| 發現 | 變更 | 測試 | Mutant |
|---|---|---|---|
| **M1**：取消依序送出，第一個失敗就 `return`；剛完成的 baseline 回 409 時 scenario 不會被取消，畫面說取消失敗 | `features/simulations/use-run-pair.ts`：對每個仍在進行的 run 同時、各自送 cancel（`Promise.all`，每個 run 沿用自己的 Idempotency-Key，D-011）；409 `LIFECYCLE_CONFLICT`（run 在上次讀取後已結束）不算失敗；其他失敗（如 503）各自記下，不影響另一個；全部回來後一律 `cancelDone` 並重新讀取（`nextAttempt()`，poll 因讀取失敗停下時也一樣）。`run-pair.ts`：`cancelFailed` 帶 `runId`，狀態 `cancelProblem` 改為 `{ side: baseline｜scenario｜both, problem }`（兩個都失敗時保留第一個 problem）。`run-status.tsx`：說明是誰的取消沒有成功，再顯示 API 的 problem（含 request ID，在「Details」中）。catalog：`runs.cancelFailed` 改為 `runs.cancelFailed.both`／`.baseline`／`.scenario`（en 與 zh-Hant） | `results-view.test.tsx` 新增 4 個；`simulations.test.ts` 的 reducer 測試改為新事件並加上 side 與 both 的斷言 | T032-A–G，全部 killed |
| **L4**：T-051 pack 的 `ratio` 指標以原始小數與英文「(ratio)」顯示，Δ 不是百分點 | `features/results/metrics.ts` `unitOf`：`ratio` 對到 MetricUnit `ratio`（既有的格式：百分比、Δ 為百分點），名稱旁不再列單位；`<data value>` 仍是伺服器的原值。其他未知單位（如 `entity_per_hour`）照舊在名稱旁原樣列出 | `results-metrics.test.ts` 新增 1 個；`results-view.test.tsx` 新增 1 個（zh-Hant） | T032-L4 killed |

**現在的行為**

- 兩個 run 都在執行，按「Cancel the runs」：baseline 在上次讀取後已完成，cancel 回 409 `LIFECYCLE_CONFLICT`；scenario 的 cancel 回 200（cancellation_requested）→ 兩個 POST 都送出，scenario 顯示「Stopping…」，重新讀取後 baseline 顯示「Completed」，沒有任何取消失敗的說明或 alert（審查者的重現）。
- poll 因讀取失敗（503）停下、畫面有重試時按取消：取消後重新讀取，baseline 顯示「Completed」、scenario「Stopping…」。
- baseline 的 cancel 回 503、scenario 的回 200：兩個都送出；「The baseline run could not be cancelled.」＋ problem（`unavailable`、`error.SERVICE_UNAVAILABLE` 的說明、request ID）；scenario 顯示「Stopping…」；baseline 仍在執行，所以取消仍在，再按一次以同一個 key 重送。
- 兩個都失敗：「The runs could not be cancelled.」／「無法取消這兩個執行。」，一個 alert。
- `ratio` 指標（以 scrap_rate 為例，zh-Hant）：名稱只有 `scrap_rate`，值「4%」「2.5%」、Δ「-1.5 個百分點」、Δ%「-37.5%」；`<data value>` 為 0.04、0.025、-0.015、-0.375。

**測試（先寫，先紅後綠）**

| 檔案 | 新增或改動 |
|---|---|
| `tests/results-view.test.tsx` | 新增 5 個：一個 409＋一個 200（兩個 POST、stopping、沒有失敗說明、axe）；poll 停下後取消會重新讀取；503 顯示 problem 與 request ID、指名 baseline、另一個仍取消、重按沿用同一 key（axe）；兩個都失敗時指名兩者（zh-Hant）；`ratio` 指標的百分比與百分點（zh-Hant、axe） |
| `tests/results-metrics.test.ts` | 新增 1 個：`ratio` 的 domain 指標（line 與 station key）為 MetricUnit `ratio`、名稱旁沒有單位、值不變 |
| `tests/simulations.test.ts` | reducer：一個 run 的 `cancelFailed` 指名該 run、不結束取消（`cancelDone` 才結束）；第二個失敗為 `both` 且保留第一個 problem；不屬於這組的 run 不改變狀態 |

紅：在未修改的程式上執行 `results-view.test.tsx`、`results-metrics.test.ts`，6 failed／33 passed（`red-t032.log`，exit 1）。行為上的紅：409＋200 的測試等不到「Stopping…」（舊程式在 baseline 的 409 就停，scenario 的 POST 沒有送出）、poll 停下後的測試等不到重新讀取、`ratio` 的兩個測試看到「scrap_rate (ratio)」與 number 單位。503 與「兩個都失敗」兩個測試在舊程式上失敗的原因之一是新的 catalog 字串尚不存在（老實記錄）；它們的行為由 mutant T032-C、E、F、G 證明。綠：修正後 91／91（含 `simulations`、`results-simplicity`、`i18n`），完整 `make frontend-test` 785／785。

**Mutant**（`mutants.py`、`mutants.json`、`mutants-T032-G.json`、`mutant-*.log`；逐一改一處、跑對應測試、還原，結束後 `git status` 乾淨）

| ID | 改動 | 結果（擋下它的測試） |
|---|---|---|
| T032-A | 409 `LIFECYCLE_CONFLICT` 當作取消失敗 | killed（409＋200） |
| T032-B | 取消後不重新讀取 | killed（poll 停下後的取消） |
| T032-C | 只取消第一個仍在進行的 run | killed（5 個，含既有的「cancels the runs that are still going…」） |
| T032-D | 第二個失敗取代第一個、不指名兩者 | killed（reducer、兩個都失敗） |
| T032-E | 失敗說明不指名是誰的取消 | killed（503） |
| T032-F | 不顯示取消的 problem | killed（503、兩個都失敗） |
| T032-G | 取消失敗不回報（審查 L1 的 R2 在新位置） | killed（503、兩個都失敗）：L1 中 R2 的缺口因此補上 |
| T032-L4 | `unitOf` 不認 `ratio` | killed（metrics、zh-Hant 元件） |

**指令與結果**（同一棵 tree，與 T-015 報告的同名一節為同一次執行；Node 22.22.0，`--engine-strict=false`）

| 指令 | exit | 結果 |
|---|---|---|
| `npx vitest run tests/results-view.test.tsx tests/results-metrics.test.ts`（修正前） | 1 | 6 failed／33 passed（`red-t032.log`） |
| `make frontend-test` | 0 | 34 個檔案、785 個 test（`frontend-test.log`） |
| `make frontend-build` | 0 | 含 `/scenarios/[scenarioId]`、`/scenarios/[scenarioId]/results`、`/ui-gallery/results` |
| `make e2e-test`（單獨執行，先確認 3100／3101 空著、沒有其他 Playwright） | 0 | 257 passed、32 skipped（截圖，只在 CI）；`results.spec.ts` 的鍵盤取消與字串測試通過 |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | PASS；`checks/docs-check.json` 重產（121 個 Markdown 檔） |
| `go test -count=1 ./internal/model/...` | 0 | （T-015 的 vectors；T-032 沒有改 Go） |
| Mutant（`mutants.py`） | — | T032-A–G、T032-L4：8 個全部 killed |

**NOT RUN**：截圖比對（只在 CI）；已登入、有真實 API／worker 的瀏覽器閉環（T-040；app 尚未掛 AuthProvider）；SSE；Node 24、Firefox／Safari、螢幕報讀器；Go 測試只跑了 `./internal/model/...`（本輪沒有改 Go）；沒有 live AI 呼叫，沒有讀任何 key 或 secret；沒有 push。

**延到比賽之後（未修）**：L1（結果頁的反向路徑：比較 API 失敗 R6、trace 下載失敗 R3；R2 已由本輪的 503 測試涵蓋，mutant T032-G）、L2（結束時仍被擋的線位置是猜的、表格不計）、L3（`checkPair` 不檢查 baseline 的版本）、L5（`planning/tasks.json` 的 T-032 completion_evidence，由 coordinator 加入）；Info I1–I8（I3：取消請求進行中再按沒有說明，未變；I8 的 `checks/docs-check.json` 由 coordinator 在收錄 review 檔後重產）。

### Coordinator 驗證（review 第 1 輪修正，2026-10-26）

`t015-t032-m1` 的 702607e（取消逐一送出、409 不算失敗、之後一律重新讀取；ratio 以百分比與百分點顯示）與 T-015 的修正一起 cherry-pick 與驗證。

指令與結果（evidence：`checks/evidence/2026-10-26-T-051-T-052-T-015-T-032-coordinator/`，`exit-codes.txt` 依執行順序；HEAD 312ea90；Node 22.22.0，go1.27.3）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test GOFLAGS=-count=1` | 0 | 79 個 package ok |
| `make architecture-check` | 0 | |
| `make backend-numeric` | 0 | |
| `make simulation-reference-test` | 0 | |
| `make ai-eval` | 0 | fake provider |
| `go run ./scripts/gen-electronics-line-data -check` | 0 | 檔案與 generator 的輸出相同 |
| `go test -race -count=1 -timeout 30m`（domainpack、results、import、audit、cmd/worker、generator） | 0 | 16 個 package ok |
| `make integration-test`（新的 dt_test_hkb3c，跑完刪除） | 0 | |
| `npm ci`（web、web-e2e） | 0／0 | |
| `make frontend-test` | 0 | 985／985 |
| `make frontend-build` | 0 | |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| `make e2e-test`（單獨執行） | 0 | 490 passed、32 skipped（全部是只在 CI 比對的截圖） |

NOT RUN：截圖比對（只在 CI）；完整的 `make backend-race`；live AI；Node 24。

M1 修正並經 coordinator 驗證，依 H-029 的比賽模式，**T-032 的 hackathon 切片完成**；L1（R6、R3）、L2、L3、L5 與 Info 為比賽後的後續。

