# T-034 驗證報告：3D 抽樣軌跡播放（hackathon 切片）

**日期：2026-10-28｜範圍：H-038 排定的產品完整度第四項；結果頁的 3D 檢視播放抽樣軌跡｜需求：REQ-017、REQ-023｜驗收測試：TEST-025、TEST-026（播放端）｜依據：T-033 的唯讀 3D 檢視、SIMULATION_SPEC §9（trace）、AGENTS.md §2（3D 為唯讀投影與播放，共用 entity ID 與 IR topology）**

`planning/tasks.json` 的 T-034 狀態不變（依賴 T-033 仍為 `not_started`，同 T-033 的說明），留給 coordinator。

## 1. 結論

| 項目 | 交付 |
|---|---|
| 軌跡的讀取 | 不另寫 loader：結果頁已以 run 的授權下載抽樣軌跡（T-032 的 `loadResults`，`downloadSimulationTrace`；D-044：授權後串流，沒有 signed URL），播放用同一份 |
| 位置（純計算） | `features/twin-3d/playback/occupancy.ts`：每個抽樣的工作項目在它最後一個定位事件（arrival、queue_enter、service_start、delay_start）的 node，直到 complete 或 abandon；服務後被擋（blocked）的留在該站。只用軌跡自己的事件：不內插、軌跡沒有的項目不畫、model 沒有的 node 不猜；軌跡被截斷時，播放在最後一個事件處結束。游標使向前播放只套用新的移動，往回拖則從頭重算 |
| 繪製 | `engine.ts` 的 `setOccupancy`：每個 node 一根柱子，高度隨該處的抽樣項目數（每個 0.4、上限 5），顏色為該執行的 token（baseline／scenario）；柱子每個場景建立一次，之後只改大小與顯示，一次更新是 node 數的成本 |
| 控制 | `playback.tsx`：執行的選擇（兩個執行都有軌跡時）、播放／暫停、時間滑桿（`aria-valuetext`「5 hours of 10 hours」，單位依執行長度取至少 2 個的最大單位、到 0.1）、目前的時間（不是 live region）；播放 30 秒走完整個執行，畫面至多每 100 ms 更新；切換執行保留時間（同一時刻比較兩個執行）；選取的步驟顯示該處的項目數（與 2D／3D 共用的選取） |
| 標示 | 抽樣比例（「Sampled trace of replication 1: about 1 in 8 work items.」）、柱子的意義、截斷時的說明；顏色說明改為「所顯示的執行」 |
| 沒有軌跡 | 兩個執行都沒有記錄：「沒有可播放的內容」，沒有控制、沒有柱子；只有一個有：說明另一個沒有記錄；下載失敗：說「無法載入」，不說成沒有記錄 |
| 結果頁 | 「以 3D 檢視產線並播放執行過程」不再只在有 AI 說明時出現：審查頁（`/ui-gallery/results`）也有，所以瀏覽器的 a11y 與簡潔規則涵蓋它 |

沒有新的 API、schema 或 migration；3D 與播放不寫回任何資料。

## 2. 測試

| 測試 | 內容 |
|---|---|
| `apps/web/tests/twin-3d-occupancy.test.ts` | 定位事件、被擋時留在站上、完成後消失、執行結束時仍在服務；往回拖與從頭算相同；截斷的軌跡在最後一個事件結束；未知 node 不畫；空軌跡 |
| `apps/web/tests/twin-3d-playback.test.tsx` | 滑桿到一半時 view 收到的數量等於 `timeline` 的計算；切換到 baseline 同一時刻；抽樣標示；播放前進、暫停後停止；沒有軌跡時沒有控制、沒有柱子；只有一個執行有軌跡時沒有選擇 |
| `apps/web-e2e/tests/results.spec.ts`（新 describe） | Chromium（WebGL）：打開 3D、抽樣標示、Tab 到滑桿且焦點可見、End／Home、拖到一半時畫面改變、播放與暫停、切換到 baseline、axe 0、沒有 page error |

截圖（Chromium、WebGL、board line 審查資料）：`checks/evidence/2026-10-28-t034-playback/T034-playback-scenario.png`（7:00，scenario，項目在一站上）與 `T034-playback-baseline.png`（同一時刻，baseline 的使用率顏色）。

## 3. 獨立 review（H-029）與修正

Reviewer 跑了三個 vitest 檔（17 個測試）、eslint、`tsc`，並在 Chromium 以 aria snapshot 確認 `<output>` 的角色；Playwright 未跑。High 0；occupancy 的事件語義與 engine 的 trace（`internal/simulation/trace`、kernel）一致。

| # | 發現 | 處理 |
|---|---|---|
| M1 | 時間顯示是 `<output>`（隱含 `role=status`），播放時每 100 ms 被螢幕閱讀器朗讀 | **修正**：改為 `aria-hidden` 的文字，時間由滑桿的 `aria-valuetext` 說；測試確認沒有 live region |
| M2 | 軌跡下載失敗被說成「沒有記錄」；只有一邊有軌跡時沒有說明 | **修正**：每個不能播放的執行各自說明（`marey.failed.*` 或新的 `playback.missing.*`）；測試 |
| M3 | h:mm 沒有單位，看起來像分：秒；不到 1 分鐘的執行一直是「0:00 of 0:00」；多日顯示「720:00」 | **修正**：`formatDuration`（Intl 的單位格式，兩種語言），單位依長度選擇、到 0.1；測試涵蓋秒、分、時、日與 zh-Hant |
| L4 | 切換到較短（截斷）的執行時，時間可超過它的結尾 | **修正**：顯示與計算都限制在該執行的結尾 |
| L5 | 第一個 frame 的時間戳可能早於 effect 的 `performance.now()`，短暫出現負的時間 | **修正**：`Math.max(0, …)` |
| L6 | 所有事件都在 tick 0 的截斷軌跡（end＝0）時，播放停不下來 | **修正**：end＝0 時不顯示控制 |
| L7 | 點柱子不會選取它的步驟 | **延後**（點步驟本身可以） |
| L8 | 截斷軌跡的最後一個 tick 可能不完整 | **接受**，已在限制中說明 |
| L9 | 測試缺 abandon、delay、batch 的情形；沒有軌跡的測試在 `setOccupancy` 沒被呼叫時也會通過；e2e 只有 en、1280 px；切換執行沒有斷言 | **修正**：新增 occupancy 的 delay／batch／abandon／rework 案例、呼叫次數的檢查、e2e 的 en 與 zh-Hant 390 px（控制在頁面內、無橫向捲動、axe 0）、切換後的斷言；播放測試仍用真的計時器（約 1 秒），以及「至多每 100 ms 更新」沒有計數測試：**延後** |
| L10 | zh-Hant 用了「抽樣」「軌跡」，catalog 其他地方是「取樣」「追蹤」 | **修正** |
| L11 | 3D 無法執行時仍顯示柱子的說明 | **延後**（控制與數量的文字仍有用） |

「選取同步」：選取的步驟在切換執行時保留，並顯示該處的數量；與結果頁的 Marey 圖沒有連動（結果頁沒有 2D 畫布）。

## 4. 已知限制

- 相機的取景偏小、偏低（T-033 已記錄），留給 UI polish。
- 柱子是抽樣項目數（約 1／8），不是全部項目；標示已說明。
- 播放的是第一個 replication 的抽樣軌跡（trace 只記錄一個 replication）。
- 截斷的軌跡在最後一個事件的 tick 結束；事件上限可能在該 tick 的中途達到，該 tick 其他項目的事件可能缺少（review L8）。

## 5. 命令與結果（修正後，2026-10-28，本機 WSL2；Node 24.21.0；Chromium，Playwright 1.56.1）

| 命令 | 結果 |
|---|---|
| `make frontend-test` | exit 0：lint、typecheck、**1100 個 unit test 通過** |
| `make e2e-test` | exit 0：**518 passed**（T-033 時為 515：新增播放的 1280 px 測試與 en、zh-Hant 390 px 兩個），32 skipped（截圖，只在 CI 的固定 image 比對），16.9 分鐘 |

沒有改動 Go、API、schema 或 migration：`make backend-test`、`make integration-test` 本輪 NOT RUN。Live 見第 6 節。

## 6. Live 確認（14bb5f2 部署後，2026-10-29，評審帳號）

部署與證據同 `checks/T-030_VALIDATION.md` §8 的 live 確認（`checks/evidence/2026-10-29-live-t030-t034/`，`1-check.log`、截圖 `D2*`）。示範產線（41 個步驟，24 小時，5 個 replication）的結果頁：

| 項目 | 結果 |
|---|---|
| 3D 開啟 | canvas 1、沒有「cannot run」；兩個執行都有軌跡，所以出現 Baseline／Scenario 的選擇，預設 Scenario |
| 起點 | 「0 hours of 24 hours」、0 個項目 |
| 播放 3 秒後暫停 | 「3.3 hours of 24 hours」（30 秒走完 24 小時；含點擊的時間） |
| 滑桿到一半 | 「12 hours of 24 hours」、scenario 41 個項目，各步驟上有欄位（`D2b-results-3d-half.png`） |
| 切換到 Baseline | 時間不變（12 hours）、radio checked、42 個項目（`D2c-results-3d-baseline.png`） |
| 提示 | 「Sampled trace of replication 1: about 1 in 8 work items.」 |
| 頁面錯誤 | 無 |

畫面上看到的問題：產線在 3D 畫面中偏小，上方大片空白（鏡頭距離固定）。屬 UI 收尾，下一個切片處理。
