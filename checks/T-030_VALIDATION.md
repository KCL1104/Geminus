# T-030 驗證報告：Grounded AI simulation analyst（hackathon 切片）

**日期：2026-10-28｜範圍：H-038 排定的產品完整度第二項；分析一次 completed 的情境執行與它的 baseline，AI 只能說結果裡有的東西｜需求：REQ-016、REQ-018、REQ-019、REQ-021、REQ-022、REQ-023｜驗收測試：TEST-024、TEST-027、TEST-031｜依據：AI_SPEC §9、§10，D-010、D-016、D-026、D-090（APPROVED），D-112（H-038 批准），D-133（PROPOSED，本切片提出）**

## 1. 結論

| 層 | 交付 |
|---|---|
| 審查（純計算） | `internal/ai/analyst`：AI 的回答只有在每一項說法都能對照 server 供應的資料時才保留（D-133 (a)–(c)）；存下的 delta 是 comparison 自己的值 |
| Context | `internal/ai/aicontext` 的 ALLOWED_METRIC_REFERENCES 加入 pack 的 `domain` 指標（電子產線的首次良率等），其他不變 |
| DB | migration `0018_analysis_jobs`：`output_locale` 欄位與 analysis 的生命週期（D-133 (e)），`db/tests/analysis_jobs.sql` |
| 儲存 | `internal/storage/analyses`：Insert、Get、Lock、Complete |
| API | `internal/analyses`：`POST /v1/simulations/{id}/analyses`（createAnalysis，editor／owner）、`GET /v1/analyses/{id}`（getAnalysis，所有成員）；idempotency、audit、tenant scope |
| Job | `internal/analyses/analyzer`（只由 `cmd/worker` 連結）：讀 analysis、兩次執行與結果、版本，以 `compare.Compare` 算 comparison，建 context、呼叫 provider、審查、寫入 |
| Live 政策 | H-029（Meta）紀錄的 live role 加入 `analyst`（H-038）；`importer` 仍拒絕；H-018（Anthropic）不變 |
| 畫面 | 結果頁的「AI 解讀」區塊（`features/results/analysis.tsx`），en／zh-Hant |
| Eval | `make ai-eval` 加入 `TestAnalystEval`（`checks/ai-evals/fake-analyst.json`）：analyst corpus 10 個情況各一個可保留與一個必須拒絕的手寫回答 |

OpenAPI、schema 不變（兩個 operation 與 `AnalysisRequest`、`AnalysisAccepted`、`AnalysisRecord`、`analysis-output` 原本就在 contract 裡）。

## 2. 設計

### 2.1 審查（`internal/ai/analyst`，D-133 (a)–(d)）

依序檢查，任何一項不符就整個拒絕（回傳 issue 的 code 與 JSON Pointer）：

| 規則 | Code |
|---|---|
| 沒有 baseline 時沒有 baseline 摘要、delta、baseline 的 evidence | `ANALYSIS_NO_BASELINE` |
| delta 的 path 是 comparison 的 path，同一個只能一次 | `ANALYSIS_DELTA_UNKNOWN`、`ANALYSIS_DELTA_REPEATED` |
| delta 的值以回答的小數位數四捨五入後等於 comparison 的值（含正負號）；comparison 為 null 時必須 null | `ANALYSIS_DELTA_MISMATCH` |
| evidence 的 run 有供應；path 是該角色列出的 metric，或其下 `compare.Resolve` 有值的統計值 | `ANALYSIS_EVIDENCE_UNKNOWN` |
| 文字中的數字是供應值（`locale.CheckProse`：依寫出的位數四捨五入、百分比；數字詞與非 ASCII 數字無法核對） | `ANALYSIS_NUMBER_UNVERIFIED` |
| 主張（摘要、理由、結論、實驗假設）不承諾結果 | `ANALYSIS_CERTAINTY` |
| 文字不含 server ID | `ANALYSIS_SERVER_ID` |
| 任一次執行少於 2 次 replication 時，uncertainty 不可為空 | `ANALYSIS_UNCERTAINTY_MISSING` |
| 結論是 observation 或 inference、有 evidence；實驗 `requires_confirmation` 為 true | `ANALYSIS_OUTPUT_INVALID` |

通過時回傳的 analysis 與回答不共用記憶體，delta 的值換成 comparison 的值。

已知上限（程式中的 `ponytail:` 註解）：承諾結果的詞表是固定的，主張中出現「not guaranteed」也會被拒絕；不確定與假設的欄位不檢查（那裡本來就會出現這些詞）。

### 2.2 Job（`internal/analyses/analyzer`）

- 在 lease 下讀 analysis（必須 generating）、兩次執行（必須 completed）與它們的 Result、分析那次執行的版本 IR。
- 在 transaction 外：解碼、pack、`compare.Compare`（API 的 compareSimulations 用的同一個函式，D-090），`aicontext.Analysis`，`AnalyzeSimulation`（花費由 T-053 的 meter 記到 `ai_analysis`），`analyst.Check`。
- 不 grounded：`ANALYSIS_UNGROUNDED`，不重試，訊息只含第一個 issue 的 code 與 path（server 自己的字）；audit `analysis.attempt_failed` 記花費與 issue 數。
- provider 的失敗：`AI_` 加 client 的 code，可恢復時重試；`AI_INVALID_OUTPUT` 不重試。
- 成功：在完成 job 的 transaction 裡 `Complete`，audit `analysis.generate` 記花費與 delta、結論、實驗的數量。

### 2.3 DB（migration 0018，D-133 (e)）

0012 對 proposal 的做法，套到 analysis：開始時 generating、什麼都沒產生；API 只為 scoped user 建立；request（兩次執行、問題、語言）、ID、建立者與時間永不改變；只在完成 job 的 transaction 裡變 completed（deferred constraint trigger）、只在 job failed／cancelled 時隨之 failed（`follow_analysis_job`，清掉 provider metadata）；completed 與 failed 不可再改；只有 API、worker 與 schema owner 能寫。API 沒有 `ai_analyses` 的 UPDATE，因此 API 結束 analysis 的 job 會被拒絕（42501）；API 沒有這樣的 route，operator 可以結束。資料庫已有 analysis 時 migration 停下（之前沒有任何程式建立 analysis）。

### 2.4 畫面（`features/results/analysis.tsx`，D-133 (g)）

- editor／owner：說明、選填的問題（多行）、「解讀這些結果」按鈕；相同 request 重送沿用同一個 Idempotency-Key（D-011）。
- 建立後 analysis ID 寫進網址（`router.replace`，`&analysis=ana_…`），每 2 秒讀一次直到不是 generating；狀態以 status region 宣告。
- completed：AI 的摘要；「改變了什麼」（指標名稱取自指標表、數值用 server 的 delta 並以 `<data value>` 保留精確值、AI 的理由）；「為什麼」（觀察／推論標籤、依據的指標名稱與 run 角色，表中沒有的 path 以 `<code>` 顯示）；假設、不確定的地方；「接下來可以試什麼」並說明只是建議。
- failed：`ANALYSIS_UNGROUNDED` 以一句話說明沒有保留、不顯示任何回答內容；其他失敗以 ProblemView 顯示；editor 可「再問一次」。
- 檢視者：網址有 analysis 時看得到，沒有時整個區塊不出現（H-027）。
- 結果頁的 UI gallery 不傳 analysis，所以截圖不變。

## 3. 測試

| 測試 | 內容 |
|---|---|
| `internal/ai/analyst`（4 個 test，含 27 個拒絕的子測試） | grounded 的回答保留且存 comparison 的值、不共用記憶體；沒有 baseline；每一種捏造都被拒絕且 path 正確；不確定與假設欄位的疑問詞不被拒絕 |
| `internal/ai/aicontext` | domain 指標可引用；system text 含 `context-rules-2` |
| `internal/ai/corpus` `TestAnalystEval` | 10 個情況：grounded 回答 10／10 保留，unsafe 回答 10／10 因該情況的缺陷被拒絕；gate `reference_refused`、`unsafe_answer_kept` 皆 0 |
| `db/tests/analysis_jobs.sql` | 0018 的每一條規則（建立、request 不變、各狀態內容、隨 job 完成、隨 job 失敗與取消、完成與失敗後不可改、其他 role） |
| `internal/api` `TestAnalysisClosedLoop` | 經真的 HTTP handler（API login）與 job loop（worker login）：409 前、202、replay 同一回應、同 key 不同 body 409、檢視者讀到 generating 再 completed、run 由 server 綁定、zh-Hant、audit 兩筆且不含問題、其他 tenant 404 |
| `internal/api` `TestAnUngroundedAnalysisFails` | fake 回答捏造數字：analysis failed、沒有內容、`INTERNAL_ERROR` 且 `details[0].code` 為 `ANALYSIS_UNGROUNDED`、`analysis.attempt_failed` 記 1 個 issue |
| `internal/api` `TestAnalysisRefusals` | 檢視者 403；沒有這次執行或 baseline 404（指出哪個）；未完成 409（指出哪個）；baseline 是另一個 model 或同一次執行、問題是空白、不支援的語言 422；ID 種類錯 400；AI 關閉 503；被拒的 request 沒有存任何 analysis |
| `cmd/worker` | handler 登記為 `analysis` |
| `internal/audit` | 三個新 action 的範例 |
| `internal/config/aiapproval` | H-029 允許 analyst、不允許 importer；未知 role 使紀錄不完整 |
| `apps/web` `the AI explanation of the results`（3 個） | editor 帶問題要求、body 與 key、網址、讀到完成後顯示 server 的數值與指標名稱、axe；ungrounded 的說明與再問一次；檢視者（zh-Hant）只讀、沒有 analysis 時不顯示 |

先寫 `internal/ai/analyst` 的測試再寫程式；其他層與程式一起寫，以下面的 mutation 證明測試會抓到錯誤。

### Mutation

| Mutant | 結果 |
|---|---|
| A1 delta 任何值都接受 | KILLED |
| A2 列出的 metric 之下任何 path 都接受 | KILLED |
| A3 不檢查承諾結果的詞 | KILLED |
| A4 允許 server ID | KILLED |
| A5 從不要求 uncertainty | KILLED |
| A6 同一 metric 兩次 | KILLED |
| A7 存回答自己的 delta 值 | KILLED |
| A8 不檢查文字中的數字 | KILLED |
| DM1 完成或失敗後可以改 | KILLED |
| DM2 job 失敗時 analysis 不跟著失敗 | KILLED |
| DM3 job 還在跑就能完成 | KILLED |

## 4. NOT RUN

- **Live**：analyst 在 Meta 上的實際回答、通過審查的比例與花費，等獨立 review 後部署再驗證（H-029 (4)）。
- **`make e2e-test`**：結果頁的 UI gallery 不含 AI 解讀，瀏覽器檢查沒有涵蓋這個區塊；元件以 vitest + axe 測。
- **Race**：見第 5 節。

## 5. 命令與結果

（填入於第 6 節之前，見下。）
