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

**已知限制**（review r1 M1 指出，記錄而非修正）：
- 文字說的方向（「上升」「下降」）不核對；只有 structured delta 的正負號核對。「前置時間增加 8 分鐘」在實際減少 8 分鐘時會通過。
- 文字的數字對整批供應值（兩次結果、comparison、設定、容量與機率的所有數字）以寫出的位數比對；供應值越多，越容易碰巧對上。
- 冒號後的數字（`3:97`）視為時間或比例，不核對。
- 因此畫面的說明改為「它提到的數字與指標都會對照結果檢查」，不說「每一項說法」。

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
| `internal/ai/analyst`（4 個 test，含 28 個拒絕的子測試） | grounded 的回答保留且存 comparison 的值、不共用記憶體；沒有 baseline；每一種捏造都被拒絕且 path 正確；不確定與假設欄位的疑問詞不被拒絕 |
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
## 5. 獨立 review 第 1 輪（H-029）與修正

Reviewer（獨立 subagent，只讀 commit 448357f 的程式，沒有 shell）：**High 0**、Medium 4、Low 7。

| # | 發現 | 處理 |
|---|---|---|
| M1 | 文字的核對比文件與畫面說的弱：方向不核對；數字對整批供應值；`.97` 與冒號後的數字不核對 | **修正**：`.97` 視為數字（`locale.Numbers`，`TestLeadingDotNumbers`）；方向、整批比對、冒號後的數字記為已知限制（§2.1、D-133 (h)、AI_SPEC §9）；畫面說明改寫 |
| M2 | 網址中的 analysis 若屬於別的執行，仍會顯示在本頁的結果下 | **修正**：`run_id`、`baseline_run_id` 與本頁不符時不顯示、停止輪詢，editor 可重新要求；測試 |
| M3 | 驗證紀錄沒有貼命令與結果；子測試數量寫錯；AI_SPEC 仍說 analyst 檢查 NOT RUN | **修正**：本節與第 6 節；28 個；AI_SPEC §9、§10 |
| M4 | 不可比較的一對（不同 seed、版本、patch）仍會付費分析並存為 completed | **修正**：API 以 `compare.Compare` 先判斷，不可比較即 422 `/baseline_run_id`、不排 job；job 在呼叫前以 `ANALYSIS_NOT_COMPARABLE` 失敗；整合測試（另一個 seed 的 baseline） |
| L | migration 的「已有 analysis 就停」在 FORCE RLS 下看不到任何列 | **修正**：照 0005、0011 先 `NO FORCE ROW LEVEL SECURITY`；對應的 SQL 測試延後 |
| L | relative delta 的容許差是絕對的（寫 0 可代表 ±40%） | **修正**：relative 至少以 2 位小數比對；兩個子測試 |
| L | `cmd/worker` 的文件說 analysis 還沒做 | **修正** |
| L | client contract 測試沒有 createAnalysis、getAnalysis | **修正**：`tests/api.test.ts` |
| L | `analysis.ungrounded` 只說「數字或指標」 | **修正**：「數字、指標或保證」 |
| L | 輪詢沒有上限；一次讀取失敗就停到按重試 | 延後 |
| L | Anthropic 的 live 部署沒有 analyst role，API 仍回 202、job 才失敗 | 延後（demo 部署是 Meta，H-029 有 analyst） |

## 6. 命令與結果（2026-10-28，本機 WSL2；Go 1.27.3、Node 24.21.0；disposable PostgreSQL 16，trust，port 54329）

| 命令 | 結果 |
|---|---|
| `make spec-check`（含 `render_db_tests.py --check`） | exit 0 |
| `make backend-test` | exit 0：go vet、**85 個 package ok** |
| `make backend-race` | exit 0：**85 個 package ok**，沒有 data race |
| `make integration-test`（`make db-test` 的 SQL suites 含 `analysis_jobs.sql` 與 0018 的 up／down／up，加 `db-go-test` 的 `-race` Go DB 測試） | exit 0：**38 個 package ok**；`internal/api` 的 `TestAnalysisClosedLoop`、`TestAnUngroundedAnalysisFails`、`TestAnalysisRefusals`（含不可比較的 baseline）通過 |
| `make ai-eval` | exit 0：`checks/ai-evals/fake-analyst.json`：grounded 10／10 保留、unsafe 10／10 因各自的缺陷被拒絕；gate `reference_refused` 0、`unsafe_answer_kept` 0；context 與 review 兩個 suite 照舊通過 |
| `make frontend-test` | exit 0：eslint、tsc、vitest **49 files、1080 tests passed** |
| Mutation A1–A8、DM1–DM3 | 見第 3 節，各以 `go test`／`make db-test` 確認 KILLED |

第一次整合測試（review 前）`TestAnUngroundedAnalysisFails` 失敗：之後加入的「少於 2 次 replication 必須寫出不確定性」使測試的捏造回答多一個 issue（2 而非 1）；測試回答補上不確定性，只留下捏造數字一個缺陷。

## 7. 部署後的 live 確認：context 超過 budget（2026-10-28）

部署 a383141（owner 授權；`deploy.sh --apply`，`GEMINUS_AUTH0_SETUP=manual`）：18 個 migration 都已記錄（含 0018）、api／worker／web 都是 SUCCESS、`/healthz` 與 `/readyz` 200、`/v1/me` 401、web 200、`/auth/login` 303 到 Auth0、Postgres 沒有開著的 TCP proxy。

以評審帳號在 demo 的一對執行（電子產線範本、ICT tester 2 → 3、5 次 replication、1 天）要求解讀：**失敗**，`context_too_large`（「the results are too large for the AI context budget」），在付費呼叫之前。以同兩個 Result（API 讀回）在本機重建 context 量得：

| 區塊 | bytes |
|---|---|
| COMPARISON（396 列，120 列 unavailable） | 80,247 |
| SCENARIO_RESULTS、BASELINE_RESULTS（各有 110 個 domain metric，40 個 unavailable） | 35,137、35,177 |
| ALLOWED_METRIC_REFERENCES | 12,817 |
| system 與其他 | 8,383 |
| **合計** | **171,761**（預設 budget 131,072） |

測試的 Result fixture 只有少數 metric，所以先前的測試與 eval 都沒有遇到。

**修正**（`internal/ai/aicontext/analysis.go`）：
- `comparisonView`：COMPARISON 只留 delta：不列 unavailable 的列（它們沒有 delta），也不列每列的 baseline／scenario 值（兩次執行的值已在各自的 results）；留下的列數記在 CONTEXT_OMISSIONS（`comparison.metrics`、`not_available`）。核對不變：`analyst.Check` 仍以完整的 server comparison 比對 delta。
- `resultView`：`dora`、`additional_metrics`、`domain` 中不是 available 的 metric 不列，數量記在 CONTEXT_OMISSIONS（`<run>_results.unavailable_named_metrics`）。ALLOWED_METRIC_REFERENCES 只列 available 的，所以可引用的都還在。
- Prompt `analyst-0.3.2`：baseline 與 scenario 的值取自兩個 results 區塊，delta 只取自 COMPARISON（review 第 2 輪 M2）。
- CONTEXT_OMISSIONS 中的數量（省略與遮蔽）也算 server 供應的數字，AI 可以在 uncertainty 中說（review 第 2 輪 M1）。
- 部署：worker 設 `AI_MAX_PROMPT_BYTES=262144`（`deploy/railway/variables.json`；上限 512 KiB；花費仍受 D-110 的日上限），留給 AI 新增的工站與長問題。

新測試 `TestElectronicsLineAnalysisContext`：以 des 引擎跑兩次電子產線範本（同 demo 的設定）、server 比較、建 context：在預設 budget 內（**127,000 / 131,072 bytes**）；COMPARISON 沒有 unavailable 列、沒有 baseline／scenario 值；results 只有 available 的 domain metric；每個可引用的 metric 都在它的 results 中；省略的數量正確，且屬於供應的數字。Mutation：拿掉 comparison 的修剪、拿掉 results 的修剪（兩者都 `context_too_large`）、不把省略數量算入供應數字：皆 KILLED。

### 獨立 review 第 2 輪（H-029）與修正

Reviewer（獨立 subagent，只讀，沒有 shell）：**High 0**、Medium 2、Low 5。確認：供應數字只會變少（被拿掉的都在 results 中或是 unavailable 的），核對不會放寬；unavailable 的列 delta 都是 null；ALLOWED 與顯示一致；沒有共用資料被修改；incomparable 的列保留且在付費之前就被擋下；部署變數從 `config/ai.go` 一路到 analyzer。

| # | 發現 | 處理 |
|---|---|---|
| M1 | CONTEXT_OMISSIONS 的數量不在供應數字中；AI 在 uncertainty 寫「120 個比較項目無法取得」會使整個回答被拒（`ANALYSIS_UNGROUNDED`，已付費、不重試） | **修正**；測試（mutation KILLED） |
| M2 | `prompts/analyst/system.md` 仍說 baseline／scenario 取自 COMPARISON | **修正**：analyst-0.3.2 |
| L3 | 本機預設 budget 只剩約 3% | 記為已知限制：live 的 worker 是 256 KiB；本機與 CI 的 fake provider 用預設 budget，超過時是明確的 `context_too_large`，不是錯誤的回答 |
| L4 | 測試沒有檢查可引用的 metric 仍在 results、dora／additional_metrics 與 baseline 的修剪、`comparisonView` 的邊界 | **部分修正**：可引用的 metric 都在 results（兩個 run）；其餘延後 |
| L5 | 省略的計數有重複（`metric_references.*` 與新的計數）；comparison 在 sanitizer 之後才修剪 | 延後（只影響計數的呈現） |
| L6 | 不列 unavailable 的 metric 也拿掉了它的 reason | 延後 |
| L7 | `comparisonView` 只排除 `unavailable` | 接受 |

### 命令與結果（修正後，2026-10-28，本機 WSL2；Go 1.27.3；disposable PostgreSQL 16 `dt_test_t030`）

| 命令 | 結果 |
|---|---|
| `make backend-test` | exit 0：**85 個 package ok** |
| `make architecture-check` | exit 0 |
| `make ai-eval` | exit 0（fake analyst：grounded 保留、unsafe 拒絕，gate 皆 0） |
| `make spec-check` | exit 0 |
| `make integration-test`（重建的 `dt_test_t030`） | exit 0：**38 個 package ok** |
| `scripts/check_deploy.py`、`scripts/rehearse_railway_deploy.py` | exit 0；`rehearsal: PASS`（Railway 本身 NOT RUN） |
| Mutation（comparison 修剪、results 修剪、省略數量） | 皆 KILLED |

`make backend-race` 與 `make frontend-test` 本輪沒有重跑（沒有前端與並行的改動）：NOT RUN。

### Live 確認（8602c4b 部署後，2026-10-28，評審帳號，Meta `muse-spark-1.3`）

部署 8602c4b（owner 授權）：18 個 migration、api／worker／web SUCCESS、健康檢查與登入導向都正常、沒有開著的 TCP proxy；worker 有 `AI_MAX_PROMPT_BYTES=262144`。證據：`checks/evidence/2026-10-28-live-analyst-3d/`（腳本 `check-0028.cjs.txt`，密碼只經環境變數；各次 log；截圖 `D1-analysis.png`）。

| 次 | 結果 |
|---|---|
| 修正前（a383141） | `context_too_large`，沒有呼叫 provider |
| 修正後第 1 次 | provider 回答（35,879 input、6,790 output tokens，USD 0.0737），server **拒絕**：`ANALYSIS_NUMBER_UNVERIFIED` at `/bottleneck/0/statement`（AI 在文字中寫了一個無法對應供應值的數字；回答不保存，所以無法得知是哪個數字）；畫面顯示「沒有保留」與「再問一次」 |
| 修正後第 2 次 | **保留**：AI 指出加第 3 台 ICT tester 沒有提高產出（463.4 → 463.6），因為主要限制在 SMT 之前（SMT 前的 buffer 平均等候約 230 分鐘、平均佇列約 118）；ICT 前的等候 −31.6%、ICT tester 使用率 −5.3 pp、FCT 前的等候 +42.6%；每個 bottleneck 都附兩次執行的 evidence；假設與不確定性寫出 1 天、5 次 replication |

兩次中一次被拒（樣本只有 2，不代表比例）。**畫面上看到的問題**（下一步）：AI 的文字照抄完整精度的數字與欄位名稱（`497.4225096419336 minute`、`wait_mean_minutes`）；「What changed」中 `/metrics/lead_time_minutes/mean` 與兩個 domain metric（`scheduled_utilization.ict`、`blocking_ratio.ict`）以 path 顯示，沒有名稱。

## 8. AI 說明的可讀性（2026-10-28，§7 看到的問題）

### 改動

- **Prompt `analyst-0.3.3`**（`prompts/analyst/system.md`）：prose 寫給不看 JSON 的人讀：指標用一般說法加 node／resource 的名稱，不寫 metric path、欄位名、指標 key 或 ID（path 只放在 evidence 與 delta）；數字四捨五入到 1 位小數（100 以上可寫整數），比率寫成帶 % 的百分比；只寫供應資料中有的數，不自行計算差、倍數或比例，比率的差不寫成「百分點」，百分位寫 p50、p95。這些規則對應 server 的核對（`internal/ai/locale` 的 `matches`）：寫出的小數位數內四捨五入相等即通過，帶 % 的數與 100 倍的值比；不帶 % 的「5.3 百分點」與「第 95 百分位」的 95 不會通過，AI 自己算的差也不會。範例用中性的 `<緩衝區名稱>`、`<資源名稱>`，generic prompt 不含 domain 詞彙（`TestNoDomainVocabulary`；第一版用了 SMT／ICT 的例子，被這個測試擋下後改掉）。
- **前端的指標名稱**（`apps/web/features/results/metrics.ts`、`metrics-table.tsx`）：pack 的指標以 catalog 已有的 `domain.<domain>.metric.<名稱>`（D-117 (h)）命名，站的指標加站的 node 名稱（`metric.atStation`：en「{metric}: {name}」、zh-Hant「{metric}：{name}」；key 在第一個 `.` 分開，D-117 (d)）；catalog 沒有的 key 照舊以 `<code>` 顯示 key 與單位。結果表多一列可選的「Lead time, mean」（`/metrics/lead_time_minutes/mean`，comparison 有才列），AI 的「What changed」與 evidence 因而有名稱與分鐘單位。

### 獨立 review（H-029）與修正

Reviewer 跑了 `tsc`、eslint、vitest（results-metrics 14／14、i18n 18／18）與 `go test`（aicontext、analyst、locale），並以 `CheckProse` 的副本實測句子；e2e NOT RUN。High 0。

| # | 發現 | 處理 |
|---|---|---|
| M1 | 比率的 absolute_delta 只能寫成「下降 5.4%」才通過核對（「5.4 個百分點」不帶 %，被拒），讀者會當成相對變化，與表格的「−5.4 pp」「−9.1%」不一致 | **修正**（prompt）：比率的變化只寫兩個 run 的值或 relative_delta，不寫它的 absolute_delta |
| M2 | Prompt 要 AI 寫 node／resource 名稱，名稱中的數字（「Station 7」「Line 2」）被當成數字核對，整個分析被拒（`ANALYSIS_NUMBER_UNVERIFIED`）；範本的名稱沒有數字，使用者建的線會有 | **修正**：`aicontext.AnalysisPrompt.Names`（MODEL_SUMMARY 顯示的名稱）經 analyzer 傳給 `analyst.Input.Names`；核對 prose 前把同時有數字與字母的名稱換成空白（長的先，「Station 13」先於「Station 1」）；只有數字的名稱不換（否則該數到處都會通過）。`TestNamesWithDigits`；三個 mutation（不換、不限字母、不排序）皆 KILLED |
| M3 | Server 的 locale instruction（每個 prompt 的 server rules）仍要求「單位照抄、數字用供應值的單位」，與新的 prose 規則矛盾 | **修正**：structured 欄位照抄單位；prose 的數字可四捨五入、比率可寫成帶 % 的百分比、單位以輸出語言寫。Server rules 版本 `context-rules-3`（也影響提案的 prompt：提案的 prose 核對是同一個 `matches`，四捨五入與 % 原本就通過）；ADR-027 已同步 |
| L1 | prose 欄位清單漏了 delta 的 reason 與 experiment 的 name（兩者都核對、都顯示） | **修正**：「所有 prose 欄位（…）」 |
| L2 | 「95th percentile」的 95 也被拒 | **修正**：prompt 加上英文的例子 |
| L3 | 「What changed」引用表格沒有的統計（`queue_length_mean`、`wait_p95_minutes`、`domain/<key>/p95`）時仍顯示 path | **延後**：AI 多引用表格的列；需要時以「統計＋名稱」做 fallback |
| L4 | `MetricName` 的新分支沒有 render 測試 | **修正**：`tests/metric-name.test.tsx`（en、zh-Hant、catalog 沒有的 key） |
| L5 | `dora`、`additional_metrics` 的 key 若與 pack 的指標同名，會被冠上 pack 的名稱 | **修正**：只在 `domain` 群組命名；測試 |
| L6 | `isStepMeasure` 上方的註解過時 | **修正** |

另外：提案的 review（`internal/ai/review`）對名稱中的數字原本就另有處理（`nameNumbers` 把 base 名稱中的數算作供應值），本次未改。

### 命令與結果（修正後，2026-10-28，本機 WSL2；Go 1.27.3、Node 24.21.0；disposable PostgreSQL 16 `dt_test_t030`，重建）

| 命令 | 結果 |
|---|---|
| `make backend-test` | exit 0：**85 個 package ok** |
| `make architecture-check` | exit 0 |
| `make ai-eval` | exit 0（fake provider 的 context、review、analyst 檢查） |
| `make spec-check` | exit 0 |
| `make frontend-test` | exit 0：lint、typecheck、**1088 個 unit test 通過** |
| `make integration-test` | exit 0：**38 個 package ok** |
| Mutation（名稱不換、不限字母、不排序） | 皆 KILLED |

`make backend-race`、`make e2e-test` NOT RUN。Live（`analyst-0.3.3`、`context-rules-3` 部署後再問一次）尚未執行：NOT RUN。
