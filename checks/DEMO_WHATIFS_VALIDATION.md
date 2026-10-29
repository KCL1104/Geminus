# 示範用 what-if 情境（2026-10-29）

**需求：owner 2026-10-29「要不要多加一個情景？製造業的情景」，選「同產線，多幾個 what-if」，並決定先完成 UI 重做（H-041）再做。範圍：只新增示範資料，不新增功能或 API。**

## 做法

- `deploy/demo/whatifs.json`：五個情境。每個變更以 ID 指名目標，並寫明預期被取代的舊值。
- `deploy/demo/seed-whatifs.cjs`：以編輯者身分透過 BFF 登入，經產品自己的 API 建立手動情境（T-028，`POST /model-versions/{id}/scenarios`），patch 由情境定義產生。
  - 每個 replace 之前都會 `test` 目標的 ID 與舊值（D-014），所以模型和範本不同時會被拒絕，而不是改到別的地方。
  - 版本上已有同名情境就跳過，因此可以重跑。
  - 加 `--run` 時，會在每個情境頁按「Run baseline and scenario」並等待結果頁。
  - 過程中不呼叫 AI。
  - 密碼只從環境變數讀取。
- 上線前，先以 server 自己的情境檢查器（`internal/scenarios` 的 `check`：允許清單、完整驗證、電子產線 pack 的規則），對 live 模型 v1 的 IR 檢查五個 patch，全部接受。檢查用的暫時測試檔沒有 commit。

## Live 結果（Railway，模型「Electronics assembly and test line」v1，評審帳號）

| 情境 | 變更 | 前置時間中位數（baseline → scenario） | 其他 |
|---|---|---|---|
| Third ICT tester | ICT 測試機 2 → 3 | 469.51 → 461.11 min，縮短 1.8% | 產出 +0.2 |
| One more debug operator | 除錯人員 4 → 5 | 不變 | 人員使用率 40.6% → 32.5% |
| Burn-in batches of 48 | 燒機批量最小 20、最大 40 → 48 | 469.51 → 475.37 min，延長 1.2% | 燒機箱使用率 −8.3 pp |
| Bigger buffers before ICT and FCT | 兩個緩衝 10 → 30 | 469.51 → 469.42 min（約 0.02%，畫面寫「0% shorter」） | 幾乎不變 |
| 20% more boards per release | 每批投入 360 → 432 | 469.51 → 526.88 min，延長 12.2% | 產出 +43.2（+9.3%） |

每個情境都有完成的 baseline 與 scenario 執行，結果頁網址記錄在本機 log（scratchpad），不放進 repo。

## 限制與待 owner 決定

- 「0% shorter」：差異小於 0.05% 時，四捨五入後顯示為 0%，但句子仍寫「shorter」，屬於文案問題，尚未修改。
- live 模型上原本有兩個 AI 提出的情境，名稱是原始 ID（與「Third ICT tester」內容相同），沒有刪除。是否清理由 owner 決定。
- 結果是合成資料在此模型與設定下的估計（5 次重複），不是對真實產線的預測。
