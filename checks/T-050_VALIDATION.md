# T-050 驗證報告：DES 批次 process 與換線（H-016）

**日期：2026-10-26｜狀態：實作完成，待 coordinator 整合與獨立 review（H-003）；`planning/tasks.json` 的狀態未改（仍為 not_started），由 coordinator／reviewer 決定（review 第 1 輪起為 in_progress，見文末「Review 第 1 輪」）。｜需求：REQ-011、REQ-013、REQ-024｜Gate：D-047、D-048、D-049、D-050、D-054、D-055、D-057 已 APPROVED（H-018），D-063 已 APPROVED（H-019），D-074 已 APPROVED（H-022）；本任務提出的 D-096、D-097 是 **PROPOSED**，`approved_by`／`approved_at` 為 null，T-050 依建議實作並以它們為 gate（tasks.json 的 `decision_gates` 加入 D-074、D-096、D-097）。｜驗收測試：TEST-015、TEST-019、TEST-034（第四部分）｜DB 測試與 `make integration-test` 為 NOT RUN（見「NOT RUN」）。**

範圍依 tasks.json 與 coordinator 的指示（每一條都要做）：

1. `batch` process：min／max batch size、等待上限、批內工件一起開始與結束；工件 ID 與 trace 仍可追蹤（成員的 `batch_id`）；Result `batches[<process>]`（`batches_started`、`members_started`）；批次的 lead time、湊批等待與 utilization 的定義寫入 SIMULATION_SPEC。
2. `setups`（換線，T-046 phase 2 分給 T-050）：`SETUP_INVALID`、`UNKNOWN_REFERENCE`（setups 的 entity type）、Result `resource_states` 的換線組（`setup_ticks`、`setups_started`、`setups_completed`、`completed_setup_ticks`）、trace 的 `setup_start`（`resource_id`）。
3. Validator：`BATCH_INVALID`、`SETUP_INVALID`、`UNKNOWN_REFERENCE`；與 D-050 bundle feasibility、B1／B2 的交互（等待湊批的 batch 不是 stall；換線只延長 service）。
4. 只解除自己的兩個 primitive：`validate.checked`、`engine.kernelExecutes`、engine_version 升為 des-go-0.5.0、`TestPrimitiveCatalog`、`TestPrimitiveGate`、`TestKernelRefusesWhatItDoesNotExecute` 與 cases.json；`internal/ai/review` 的 D-057 server risk；AI context（`aicontext.modeRules`、prompts/scenario）的 scenario allowlist 文字。
5. 沒有批次與換線的模型與 des-go-0.4.0 逐 byte 相同（D-047）：凍結 0.4.0 kernel 的副本、隨機模型的 differential、中性設定；舊的鎖照舊成立，0.5.0 另立自己的鎖；differential 的執行時間有上限（必要時像 `GEMINUS_FULL_DIFFERENTIAL` 一樣 gate）並報告時間。
6. ADR-026 原訂的每個批次與換線 golden 與找到的邊界（min batch 恰在等待上限達到、max batch 時還有更多 arrival、批次跨越下班或故障、第一件的換線、同產品之間換線為 0、warm-up／量測視窗的邊界、max_entities 在批中），每個附推導；SimPy reference（或明確拒絕）與隨機比對；只看 observation 的批次與換線檢查器；property tests；先紅後綠；mutation harness（每個改變行為的 mutant 都要擋下，或寫出等價的理由）。
7. 新 decision 從 D-096 起，PROPOSED；ADR 原訂而做不到的驗證列為偏離並寫入 T-037 的 scope。

依 coordinator 的指示：只在自己的 worktree（`t050`，從 4584ba6 開出）工作、commit、不 push、不開 PR；沒有 PostgreSQL，DB-backed 測試只 compile 與 vet；沒有 migration（Result 與 trace 的新成員都在 T-046 phase 2 的 schema 中，IR 的 `batch`、`setups` 也是，見「Migration 與 contract」）；沒有新的 dependency；沒有任何 live AI 呼叫，沒有讀取 `ANTHROPIC_API_KEY` 或 `GEMINUS_ANTHROPIC_API_KEY_*`；沒有改 T-049 的行為（des-go-0.4.0 的鎖、golden 與 differential 照舊通過）；沒有改任何既有 decision 的狀態；沒有降低任何 assertion 或加 skip。逐條輸出在 `checks/evidence/2026-10-26-T-050/`。

工作中 coordinator 通知：owner 已依建議批准 D-081–D-085、D-088–D-095、ADR-028 與 D-073（H-024，2026-10-26），T-049 的 des-go-0.4.0 行為因此是最終的，不需要 rebase；coordinator 在 main branch 上 commit 那些狀態變更，本任務的 D-096、D-097 只附加在 `docs/DECISIONS_AND_TBD.md` 與 `planning/decisions.json` 的最後，由 coordinator 在 cherry-pick 時合併。本 branch 中的 D-081–D-085 等仍是 4584ba6 的狀態（PROPOSED），本報告不改寫它們。

## Commit

`t050` branch，依時間順序（從 4584ba6 開出）：

| Commit | 內容 |
|---|---|
| 7509c04 | 凍結 4584ba6 的 des-go-0.4.0 kernel、replication 與 event queue（`frozen040_test.go`，以 T-049 的 go/ast 工具改名，只在測試中；工具原始碼 `freeze040.go.txt`）；`TestDifferentialAgainstFrozen040`（50,000 個沒有批次與換線的隨機模型，race 下 5,000 個）；開始前的 `make backend-test`（`baseline-backend-test.log`）與 differential（`baseline-differential040.log`） |
| 7c25863 | Validator：`internal/model/validate/batches.go` 的 `BATCH_INVALID`（min ≤ max、min < max 時要 max_wait、max_wait 量化後 ≤ 365 天、有限 buffer ≥ max_size）；`validate.checked` 加 `batch`（engine 尚未）；換線規則先寫好但仍被 gate 擋住；`model.Resource.Distributions()` 加入換線的分布與其 JSON Pointer（`Path`），呼叫者改用 `Path`；先寫的測試為紅（`red-validate.log`，43 行 `--- FAIL`） |
| 2fc93c7 | Kernel des-go-0.5.0 的批次（D-054）：batch buffer、就緒、batch request、BATCH_TIMEOUT（priority 15、世代號、只在 min < max 時）、授予（ObsBatchStart kind 17、成員、一次 service draw、空出幾個位置解除幾個）、完成（FIFO 路由、bundle 在最後一個被擋的成員移入時才釋放）、B1 等待逾時、B1／B2 回報最老成員；`kernelExecutes` 加 `batch`；Result 的 `batches[<process>]`、批次的 bundle 在 K 中只算一次；trace 的 `batch_id`；D-057 risk、expected effects 可引用的批次數字、scenario 規則與 scenario-0.6.0；手推 golden 14 個（`batch-goldens.json`）；紅（`red-engine-vet-batch.log`、`red-ai-batch.log`）與第一次執行（`first-engine-run-batch-goldens.log`、`first-des-run-batch-goldens.log`） |
| 7e8df45 | 只看 observation 的批次檢查器（`kernel_check_batch_test.go`、`kernel_check_blocking_test.go` 的 bchecker 延伸）：`TestRandomBatchModelsAgainstSpec`、`TestRandomBatchFailuresAreReal`、`FuzzBatches`；`FuzzKernel`、`FuzzRun` 以批次 golden 為 seed；第一次執行的產生器太弱（`checker-batch-first-run-weak-generator.log`），加強產生器、下限不變（`checker-batch.log`） |
| 57927ef | 換線（D-055）：validator 解除 `setups`（`SETUP_INVALID`、`UNKNOWN_REFERENCE`）；kernel 的 unit identity、unit 的選擇、換線時間與 `setup/<u>` stream、ObsSetup（kind 18）、換線＋service 的完成、故障的 unit、Compile 再檢查 P0 限制；`kernelExecutes` 加 `setups`；Result 的換線組與 ∫min(U − K − Z, S)；trace 的 `setup_start`；D-057 risk；手推 golden（`setup-goldens.json`）；中性換線的 differential 與 Result 比對；換線檢查器與 `FuzzSetups`；紅（`red-des-setups.log`、`red-des-setups-collector.log`、`red-validate-setups.log`、`red-ai-setups.log`）與第一次執行（`first-engine-run-setup-goldens.log`、`first-des-run-setup-goldens.log`） |
| 30e4200 | SimPy reference（`scripts/simpy_batch_reference.py`、Makefile、`TestSimPyReferenceExport`；T-047–T-049 的 `scripts/simpy_reference.py` 拒絕批次與換線）；`simulation-reference-test.log` |
| 86a933f | des-go-0.5.0 自己的 regression lock：`kernel-observation-digests-0.5.0.json`（30 個 case）、`engine-golden-digests-0.5.0.json`（28 個 Result），在手推期望與 SimPy 通過之後才取 |
| 457f43c | D-096、D-097（PROPOSED）；tasks.json 的 gate、planned outputs、spec refs、verification commands（status 未改）與 T-037 scope 的後續；SIMULATION_SPEC、TWIN_IR_SPEC、ADR-026、TEST_PLAN、ARCHITECTURE；`decisions-check.json`、`planning-check.json` |
| f1c57e0 | Mutation 第 1、2 次之後：手推 golden G-BT15、G-BT16（第一次執行就與手推一致之後才加入兩個 0.5.0 鎖，其他筆不變）；`TestBatchVisitsChargedToTheirMember`、`TestBatchServiceDrawsOncePerBatch`、`TestCompileChecksSetups`、`TestSetupDatasets`；mutation harness `t050-mutants.py`、`mutants-run1.log`；數量、SIMULATION_SPEC、TEST_PLAN；重跑的 `simulation-reference-test.log` |
| b5467c0 | Mutation 第 2 次之後：手推 golden G-BT17（同一批的兩個成員同時被擋；第一次執行就與手推一致之後才加入兩個 0.5.0 鎖，其他筆不變：`first-run-gbt17.log`、`locks-gbt17-before-update.log`、`batch-bundle-per-member-gbt17.log`）；Result 層的 `TestBatchAndSetupIdentities`；harness 的 `WITHOUT_LOCKS=1`、Result／trace mutant 只跑會編譯到被改檔案的 package（`go list -test -deps`）；被鎖先擋下的 mutant 略過鎖重跑（`lock-first-mutants-without-locks.log`、`one-stream-for-all-units-without-locks.log`）；數量、SIMULATION_SPEC、TEST_PLAN、T-037 scope 的後續（G-BT16、G-BT17） |
| （最後一個） | 本報告；最後的 tree（b5467c0）上的驗證 log、`mutants-run2.log`、`mutants-run3.log`、`mutants-run3-lock-first-without-locks.log`、`exit-codes.txt`、`environment.txt`；`make spec-check` 重產的 `checks/docs-check.json` |

只有最後的 tree（b5467c0 的程式與測試資料；最後一個 commit 只加本報告、evidence 與重產的 `checks/docs-check.json`）經過下面「實際執行結果」的完整驗證；中間的 commit 是依主題分開的檢視單位，沒有各自跑一次全部的驗證指令。

## 稽核：scope 逐條

| Scope | 判定 | 證據／處理 |
|---|---|---|
| 1a. 批次的就緒（min／max／max_wait） | 已完成 | `kernel.batchCheck`、`ready`：等待數 ≥ max_size，或 min < max 且等待數 ≥ min 且最老的已等 ≥ max_wait（等於上限的 tick 即就緒，D-096 (c)）；在每次進入、每次授予之後、BATCH_TIMEOUT 時檢查；一個批次 process 至多一個未授予的 batch request（就緒時取得 request sequence 與 FIFO 位置）。Golden G-BT1（min＝max＝4：沒有逾時事件，40、80、120 開始，21 個事件）、G-BT2（ADR 的 G-T2：60、120 開始）、G-BT6（min 恰在等待上限的 tick 達到）、G-BT7（逾時使 min 件就緒）、G-BT15（max_wait 0：進入即就緒，不排逾時） |
| 1b. BATCH_TIMEOUT 與過時 | 已完成（D-096 (b)(c)，PROPOSED） | 只在 min < max 時排（D-096 (b)）；最老的未分批 entity（以進入序號識別）改變時排，世代號判定過時，已到期時不排、當場檢查。G-BT14（批次在最老的上限前湊滿，舊的逾時到時被忽略）、G-BT16（過時的逾時與兩個有效逾時同 tick：兩個 process 的 request 依有效逾時的順序取得 FIFO 位置）、G-BT15；檢查器自己排程與判定過時（28,560 個 BATCH_TIMEOUT）；mutant `stale-timeout-not-ignored`、`expired-limit-scheduled`、`timeouts-for-min-equals-max` 被擋下 |
| 1c. 批內工件一起開始與結束 | 已完成 | `kernel.grantBatch`：一次 bundle、ObsResource → `ObsBatchStart`（kind 17，Value＝成員數，Batch＝n）→ 各成員 ServiceStart（Batch＝n）；從 (process, service) 抽一次、一個 SERVICE_COMPLETE；授予時取 min(等待數, max_size)（就緒後才進入的也算，D-096 (a)）；授予後再檢查，多個 chamber 同一次 dispatch 各開一批。G-BT3（兩個 chamber、9 件、一次 dispatch 開兩批留 1 件）、G-BT8（max 3、9 件：就緒的 request 等忙碌的 chamber，授予時取 3 件，再檢查時下一個最老的已過上限）、`TestBatchServiceDrawsOncePerBatch`（第 k 批的 service＝stream 的第 k 個 draw，兩個成員同時結束） |
| 1d. 批次結束：路由、阻塞、visit 上限 | 已完成（D-096 (d)(f)） | `kernel.batchComplete`：先依 FIFO 解析每個成員的路由；沒有成員被擋時先釋放；有成員被擋時 bundle 在最後一個被擋的成員移入時才釋放；成員空出幾個位置就解除幾個被擋的 entity。G-BT4（ADR 的 G-T4：完成時下游已滿，chamber 持有到最後一個成員移入，bundle 在 K 中只算一次）、G-BT5（ADR 的 G-T5：成員的 routing draw 依 FIFO，由 stream 本身計算，`TestBatchRoutingDrawsInMemberOrder`）、G-BT12（成員空出的位置解除多個被擋的 entity）、G-BT17（同一批的兩個成員同時被擋 3 分鐘：bundle 在 K 中只算一次）、`TestBatchVisitsChargedToTheirMember`（visit 上限算在各自的成員上） |
| 1e. 工件 ID 與 trace | 已完成 | 成員保留自己的 entity ID；trace 的成員 `service_start`／`service_end` 帶 `batch_id`（`r<rep>/<process>/b<n>`，`engine.FormatBatchID`）；其他事件沒有 `batch_id`。`TestBatchTrace`；mutant `trace-no-batch-id` 被擋下 |
| 1f. Result `batches[<process>]` | 已完成（D-096 (j)） | `internal/results/metrics/aggregate.go`：每個批次 process 都列（沒有批次開始時為 0）：[W, H] 內開始的 `batches_started` 與它們的 `members_started`；批次不使 chamber 進入 `resource_states`。`TestBatchGoldenResults`（15 個 case，含 G-BT11 的量測視窗：W 與 H 恰為開始時刻的批次算、W 之前的不算），`TestBatchAndSetupIdentities`（min·b ≤ m ≤ max·b）；mutant `batches-outside-window`、`batch-bundle-per-member` 被擋下 |
| 1g. 定義寫入 SIMULATION_SPEC | 已完成 | §3「批次 process」的「定義」：lead time 與 queue wait 照每件計，queue wait 從進入 buffer 到批次開始（含湊批）；utilization 是 chamber 的忙碌 ∫U／(C·T)（不是槽位）；§5：平均批量＝members／batches，平均批量／max_size 由 domain pack 與 UI 計算 |
| 2a. Setups 的 validator | 已完成（D-097 (g)，PROPOSED） | `batches.go` 的 `setupRefs`（`UNKNOWN_REFERENCE`：`initial`、transition 的 from、to）、`setupRules`（`SETUP_INVALID`：from＝to、(from, to) 重複；P0 限制：units≠1、一個 process 第二個有 setups 的 resource、批次 process 需要有 setups 的 resource，報在 `/nodes/<i>/requires/<k>`）；換線分布與其他 resource 分布一起檢查（DISTRIBUTION_INVALID、LIMIT_EXCEEDED、binding）。`TestSetupRules`、`TestSetupMessages`、semantic fixture `semantic-setup-self-transition`；Compile 再檢查（`TestCompileChecksSetups`）；binding 的 path（`TestSetupDatasets`） |
| 2b. Unit identity、unit 的選擇、換線時間與 stream | 已完成 | `kernel.changeover`：在既不在使用中也未停機的 unit 中取設定相符者 index 最小的，否則取 index 最小的；transition → default → 0（0 tick 也是一次換線，D-097 (a)）；有分布時從 (resource ID, `setup/<u>`) 抽；`ObsSetup`（kind 18）在 ObsResource 之後、ServiceStart 之前；完成在換線＋service 之後。G-SU1（ADR 的 G-S1：第一件不換線、FIFO 交錯時換線、同產品之間為 0）、G-SU2（G-S2：選設定相符的 unit 1）、G-SU3（G-S3：transition 優先、default、都沒有時 0、initial 有與無）、`TestSetupStreamsPerUnit`（G-S5：capacity 加一不改變 unit 的 draw，由 stream 本身計算）；換線檢查器 |
| 2c. 故障與換線 | 已完成（D-097 (c)，PROPOSED） | 立即停機取閒置 unit 中 index 最小者；等待中的故障由被釋放或修復好的 unit 承擔（D-074 (a) B）；停機不改變設定。G-SU6（G-S6：換線中的故障等換線與 service 做完）、G-SU7（unit identity：立即停機取 index 最小的閒置 unit、等待的故障由被釋放的 unit 承擔、修復釋放時鐘的 unit、停機保留設定）；檢查器（21,003 次立即停機、964 次被釋放的 unit、356 次修復好的 unit） |
| 2d. Result 的換線組 | 已完成（D-097 (f)） | 有 setups 的 resource 都列在 `resource_states`：`setup_ticks`（∫Z）、`setups_started`、`setups_completed`、`completed_setup_ticks`、`scheduled_processing_ticks`＝∫min(U − K − Z, S)（換線的結束沒有 observation，collector 以 min-heap 處理）。`TestSetupGoldenResults`（G-SU8 量測視窗、G-SU9 班表、G-SU10 阻塞）、`TestBatchAndSetupIdentities`；mutant `setup-complete-ignores-end`、`setup-end-not-applied`、`processing-ignores-changeover*` 等被擋下 |
| 2e. Trace `setup_start` | 已完成 | `setup_start` 帶 entity、process 與 `resource_id`，在授予的事件中、該 entity 的 `service_start` 之前；其他事件沒有 `resource_id`。`TestSetupTrace`；mutant `trace-no-resource-id` 被擋下 |
| 3a. `BATCH_INVALID` | 已完成（D-096 (i)） | `batchRules`：min ≤ max、min < max 時要 max_wait（`/nodes/<i>/batch`）、max_wait 量化後 ≤ 365 天（`/nodes/<i>/batch/max_wait`）、有限 buffer ≥ max_size（`/nodes/<i>/batch/max_size`）；min＝max 的 max_wait 允許。`TestBatchRules`、`TestBatchRulesStage`、`semantic-batch-sizes`（`/nodes/1/batch`）；mutant `batch-capacity-unchecked`、`batch-wait-required-at-equal-sizes` 被擋下 |
| 3b. 與 D-050 的交互 | 已完成（D-096 (g)） | R1–R4 對批次 process 的 bundle 照常（bundle 以批計；批次 process 被擋時是 holder）。B1：還有待處理的 BATCH_TIMEOUT 時不判定 deadlock（湊批中不是循環等待：G-BT12 中 max_entities 使最後兩件永遠湊不成批，B1 照常執行、不報；mutant `deadlock-ignores-timeouts` 被擋下）；湊批中的 entity 沒有 request，不是 B2 的 held candidate；就緒的 batch request 才可能進入 B2，回報最老成員、since 為第一次在 tick 結束時處於 B2 狀態的 tick（D-074 (c)；G-BT13 中即就緒的 tick，`TestBatchStall`，mutant `failure-names-no-member`；review 第 1 輪 M1 更正：原文寫「since 為就緒的 tick」，與 kernel 不符，G-BT18 分得出兩者）。換線只延長授予後的服務（unit 在換線＋service 期間都在使用中），不新增等待的狀態。Validator 被繞過時每個 ErrDeadlock、ErrStalled 都是真的（`TestRandomBatchFailuresAreReal`：1,667 個 run 中 70 個 ErrDeadlock、55 個 ErrStalled，都是真的） |
| 4a. 只解除兩個 primitive | 已完成 | `validate.checked` 與 `engine.kernelExecutes` 加 `Batch`、`Setups`；`TestPrimitiveCatalog`、`TestPrimitiveGate`（valid-line-flow-ir.json 剩 5 個 PRIMITIVE_NOT_SUPPORTED：宣告的 metrics，T-051）、`TestPrimitiveGateStopsTheStages`、`TestKernelRefusesWhatItDoesNotExecute`（5 個）、`TestCompileRefusesLineFlowPrimitives`；cases.json 的 primitives 在 T-046 phase 2 已寫好（不改），兩個 semantic fixture 回手推的 code 與 path（`TestAgreesWithSharedFixtures`） |
| 4b. engine_version | 已完成 | `des.Version = "des-go-0.5.0"`；`valid-run-failed-deadlock.json`、`valid-run-failed-stalled.json` 的 engine_version 隨之更新；0.1.0–0.4.0 的 Result 鎖把 engine_version 換回後相等 |
| 4c. D-057 server risk | 已完成 | `internal/ai/review/risk.go`：批次新增、刪除或 min_size、max_size、max_wait 不同；setups 新增、刪除或 initial、default、transition 不同；任一方向至少 medium；`values()` 加入批量與等待上限（換線分布經 `Distributions()`）。`TestBatchRisk`、`TestSetupRisk`、`TestBatchValuesInEffects`、`TestSetupValuesInEffects`、`TestLiftedPrimitivesHaveRiskCases`；mutant `batch-risk-ignored`、`setup-risk-ignored`、`setup-risk-ignores-initial` 被擋下 |
| 4d. AI context | 已完成 | `aicontext.modeRules` 的 SCENARIO_PATCH_ALLOWLIST 與 `prompts/scenario/system.md`（scenario-0.5.0 → 0.6.0，本任務唯一的新版本）：`/nodes/<i>/batch/min_size`、`max_size`、`max_wait` 與 `/resources/<i>/setups/default`、`transitions/<j>/distribution`，只能 replace、至少 medium、BATCH_INVALID 的條件；patch 的 allowlist（`internal/model/patch/policy.go`）在 T-046 phase 2 已有這些 path，不改。`TestScenarioMode` |
| 5a. 與 des-go-0.4.0 逐 byte 相同 | 已完成 | `frozen040_test.go`（7509c04）；`TestDifferentialAgainstFrozen040`：50,000 個中 20,213 個 compile（4,572 個有有限 buffer、8,602 個有故障、5,291 個有班表、6,270 個有投入排程），11,773,609 個 observation 與 des-go-0.4.0 逐筆相同（見「Differential」） |
| 5b. 中性設定 | 已完成（D-097 (h)） | 換線：只有一種 entity type、沒有 `initial` 或 `initial` 就是那一種：`TestNeutralSetupsChangeNothing` （6,000 個中 1,994 個 compile，436 個有有限 buffer、908 個有故障、550 個有班表；1,169,148 個 observation 逐筆相同）、`TestNeutralSetupsKeepTheResult`；批次沒有中性設定（min＝max＝1 也改變 observation：BatchStart、request 在就緒時才取 sequence），所以只做「沒有批次」的 differential（SIMULATION_SPEC §3） |
| 5c. 舊的鎖、0.5.0 的鎖 | 已完成 | 0.1.0–0.4.0 的四個 Result 鎖與四個 observation 鎖不重產、照舊通過；`kernel-observation-digests-0.5.0.json`（33 個 case）、`engine-golden-digests-0.5.0.json`（31 個 Result），在手推期望與 SimPy 通過之後才取（G-BT15、G-BT16、G-BT17 的一筆在它們的手推期望通過之後才加入，其他筆不變） |
| 5d. Differential 的時間 | 已完成 | 見「新測試的執行時間」：`TestDifferentialAgainstFrozen040` 單獨 15.3 s（50,000 個模型，與 0.2.0、0.3.0 的 differential 的 15.8 s、13.9 s 相當）；race 下前 5,000 個（與 0.1.0–0.3.0 的 differential 相同的 gate），`make backend-test` 以 `GEMINUS_FULL_DIFFERENTIAL=1` 要求全部 |
| 6a. 手推 golden（ADR 原訂與邊界） | 已完成 | 見「手推 golden」：批次 17 個、換線 9 個（G-S4 是 differential），每個附推導；全部在 engine 第一次執行前寫好 |
| 6b. SimPy reference 與隨機比對 | 已完成（做法偏離 ADR，見「偏離」） | `scripts/simpy_batch_reference.py`：16 個確定性 case 逐值相同、7 個必須拒絕、3 個不比（原因寫在 fixture）；BT-S1–BT-S4、SU-S1–SU-S4 共 47 個隨機比對都在預先固定的容差內 |
| 6c. 只看 observation 的檢查器 | 已完成 | `kernel_check_batch_test.go`、`kernel_check_setup_test.go`（見「測試涵蓋」） |
| 6d. Property tests | 已完成 | 隨機模型的檢查器（3,000 個模型各兩組）、`TestRandomBatchFailuresAreReal`、`FuzzBatches`、`FuzzSetups`、Result 層的 `TestBatchAndSetupIdentities` |
| 6e. 先紅後綠、mutation | 已完成 | 見「先紅後綠」與「Mutation」 |
| 7. Decision、偏離、T-037 | 已完成 | D-096、D-097（PROPOSED）；偏離列在「偏離與原因」；T-037 的 scope 加入本任務沒有做的 ADR-026 驗證 |
| 不碰其他任務 | 符合 | 沒有 migration；沒有改 T-049 的 kernel 行為（differential 與 0.4.0 的鎖）；`internal/storage/datasets` 只改換線分布的 binding path（`Distributions()` 的 `Path`）；`internal/model/patch` 沒有改程式（只改 `lineflow_test.go` 中一段隨 gate 解除而過時的註解）；沒有碰 T-051 的 metrics |

## 實際執行結果

在 worktree 中執行（`environment.txt`：Go 1.27.3、Python 3.13.16、SimPy 4.1.2、Node 22.22.0、4 CPU；沒有 PostgreSQL）。Go 測試都以 `-count=1` 執行，不用 cache（`backend-test.log`、`backend-race.log` 中沒有 `(cached)`）。Exit code 與時間在 `exit-codes.txt`。下表的指令都在最後的 tree（b5467c0；最後一個 commit 只加本報告、evidence 與重產的 `checks/docs-check.json`）上執行。`backend-test` 與兩個 `go test -v` 是在機器上沒有其他測試時執行的；`backend-race` 執行期間，下表中 `simulation-reference-test`（第二次）到 FuzzBatches 的指令同時在跑，所以 race 與那些指令的時間包含競爭。

| 指令 | Exit | 時間 | 說明 |
|---|---:|---:|---|
| `make backend-test`（開始前，4584ba6） | 0 | — | `baseline-backend-test.log`：71 個 package ok；engine 131 s、check-architecture 193 s |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 9 s | `spec-check.log`：本報告寫好之後重跑；`PASS_TS_CONTRACTS_FRESH`（15 檔、109 types：沒有 contract 變更）、`PASS_INDEPENDENT_HASHES`（28）、`PASS_DB_TESTS_FRESH`（13）、`PASS_ENGINE_REFERENCE_VECTORS`；`decisions-check.json`：proposed 16（含 D-096、D-097），T-050 在 `next_after_review`、等 D-096、D-097（457f43c 重產，之後沒有改變）；`planning-check.json` 沒有改變；重產的 `docs-check.json`（多了本報告）與本報告一起 commit |
| `make backend-test GOFLAGS=-count=1` | 0 | 253 s | `backend-test.log`：gofmt、go vet、71 個 package ok；`GEMINUS_FULL_DIFFERENTIAL=1`（對 0.1.0–0.4.0 的 differential 都跑全部 50,000 個模型）；engine 226 s、des 14 s、check-architecture 223 s；DB-backed 測試 skip（NOT RUN） |
| `make backend-race GOFLAGS=-count=1` | 0 | 909 s | `backend-race.log`：完整的 race（不只 internal/simulation、internal/results、internal/model），71 個 package ok；engine 811 s、des 169 s、check-architecture 423 s（differential 在 race 下跑前 5,000 個；與其他指令同時執行） |
| `make simulation-reference-test PYTHON=…` | 0 | 30 s | `simulation-reference-test.log`、`simpy-reference-report.json`、`simpy-batch-reference-report.json`：T-047–T-049 的 34 個確定性 case 與 140 個隨機比對、T-050 的 16 個確定性 case 與 47 個隨機比對都通過；G-BT4、G-BT9、G-BT12、G-BT16、G-BT17、G-SU9、G-SU10 被拒絕；`calendar_reference.py` 0 個差異 |
| `make backend-numeric` | 0 | 293 s | `backend-numeric.log`：六個 target 沒有 FMA 與平台 math call，GOAMD64=v3 的數值測試通過（engine 244 s，與 race 同時） |
| `make architecture-check` | 0 | 12 s | `architecture-check.log`：82 packages、21 rules、16 build configurations |
| `make ai-eval` | 0 | 2 s | `ai-eval.log`：`TestFakeProviderEval`、`TestReviewEval` PASS；`checks/ai-evals/*.json` 沒有改變；live provider NOT RUN |
| `go test -v`（T-050 的測試與舊的 differential） | 0 | 74 s | `engine-tests-verbose.log`：計數（見「Differential」「測試涵蓋」）與時間 |
| `go test -v ./internal/simulation/engine/`（整個 package，單獨執行） | 0 | 123 s | `engine-package-times.log`：每個測試的時間（見「新測試的執行時間」） |
| `go test -v`（internal/simulation 以外的所有 package） | 0 | 286 s | `db-tests-skipped.log`：238 個 `--- SKIP`，都是 `NOT RUN: set GEMINUS_TEST_DATABASE_URL…`（DB-backed），0 個 FAIL |
| Fuzz（以次數為準） | 0 | — | 見「Fuzz」 |
| Mutation harness 第 3 次（完整） | 0 | 2,802 s | `mutants-run3.log`：55 個 mutant 中 52 個被擋下、3 個 equivalent（見「Mutation」） |
| 第 3 次中被鎖先擋下的 6 個，略過鎖重跑（`WITHOUT_LOCKS=1`） | 0 | 166 s | `mutants-run3-lock-first-without-locks.log`：6/6 被擋下 |

較早、失敗或中途的執行（保留為 evidence）：

| 指令 | Exit | 說明 |
|---|---:|---|
| `make simulation-reference-test`（G-BT17 之前的 tree） | 0 | 被上表的執行覆寫，`exit-codes.txt` 的第一列 |
| Mutation harness 第 1 次 | 1 | `mutants-run1.log`：13 個中 10 個被擋下、3 個 SURVIVED |
| Mutation harness 第 2 次（三段） | 1 | `mutants-run2.log`：見「Mutation」 |
| 被鎖先擋下的 mutant 略過鎖重跑 | 0 | `lock-first-mutants-without-locks.log`（6/6 被擋下）、`one-stream-for-all-units-without-locks.log`（`go test` exit 1：被擋下）、`batch-bundle-per-member-gbt17.log`（exit 1：G-BT17 擋下） |
| G-BT15、G-BT16、G-BT17 的第一次執行 | 0 | `first-run-gbt15-gbt16.log`、`first-run-gbt17.log`：engine 與 Result 的子測試都 PASS（數量已先改好）；之後才取鎖（`locks-gbt15-gbt16-before-update.log`、`locks-gbt17-before-update.log`：只有鎖失敗） |
| 實作前的紅 | 1 | 見「先紅後綠」 |

## 測試涵蓋

| 層 | 測試 | 內容 |
|---|---|---|
| Validator | `internal/model/validate/batches_test.go`：`TestBatchRules`、`TestBatchRulesStage`；`setups_test.go`：`TestSetupRules`、`TestSetupMessages`；`primitives_test.go`：`TestPrimitiveCatalog`、`TestPrimitiveGate`、`TestPrimitiveGateStopsTheStages`；`TestAgreesWithSharedFixtures` | 正反例的 code、path 與 severity（min > max、min < max 沒有 max_wait、max_wait 超過 365 天、buffer < max_size；from＝to、重複的 pair、units 2、第二個有 setups 的 resource、批次 process、未知的 entity type、換線分布的形狀）；規則所在的 stage；`semantic-batch-sizes`、`semantic-setup-self-transition` |
| Compile | `engine/setup_test.go`：`TestCompileChecksSetups`；`primitives_test.go`：`TestKernelRefusesWhatItDoesNotExecute`、`TestCompileRefusesLineFlowPrimitives` | 繞過 validator 時 Compile 自己拒絕 D-055 的 P0 限制（5 個 case，各一個 `SETUP_INVALID`）；`kernelExecutes` 與 gate |
| Kernel：golden | `engine/batch_test.go`：`TestBatchGoldens`（15 個 case：arrival、service 開始、完成、批次與成員、BatchStart 的 sequence、阻塞、停機、事件數）、`TestBatchStall`（G-BT13）、`TestBatchRoutingDrawsInMemberOrder`（G-BT5）、`TestBatchVisitsChargedToTheirMember`、`TestBatchServiceDrawsOncePerBatch`；`engine/setup_test.go`：`TestSetupGoldens`（8 個 case：換線、unit、sequence、停機、等待中的故障）、`TestSetupStreamsPerUnit`（G-SU5） | 手推的期望；stream 本身計算的 draw |
| Kernel：只看 observation 的檢查器 | `kernel_check_batch_test.go`：`TestRandomBatchModelsAgainstSpec`、`TestRandomBatchFailuresAreReal`、`FuzzBatches`；`kernel_check_setup_test.go`：`TestRandomSetupModelsAgainstSpec`、`FuzzSetups`；`kernel_check_blocking_test.go` 的 bchecker 延伸（`FuzzKernel` 的 recorder 也接受 ObsSetup） | 批次：只由 IR 重建每個批次 process 的 buffer、依 D-054 建立 batch request 並放進 resource-wise FIFO、BATCH_TIMEOUT 依排程順序在 priority 15 處理（有效或過時以最老成員判定）、授予 min(等待數, max_size) 件與空出幾個位置解除幾個、一個事件的批次結束（先釋放，或 bundle 留到最後一個被擋的成員移入）、沒有任何 observation 的 tick（只讓逾時過去）、B1 等待逾時、B2 回報最老成員；換線：只由 IR 重建每個 unit 的使用、停機與設定，檢查 unit 的選擇、換線與其時間（constant 的值、0 tick）、故障的 unit（立即、被釋放、修復好）、constant service 在換線＋service 後結束。最後一次執行（`engine-tests-verbose.log`）：批次：3,000 個隨機模型中 1,629 個 compile（418 個有有限 buffer、294 個有故障、258 個有班表），58,733 批、126,309 個成員（37,517 批在進入或授予時就緒、12,091 批在等待上限就緒），28,560 個 BATCH_TIMEOUT、13,855 個沒有 observation 的 tick、1,016 個成員被擋、5,270 次解除；validator 被繞過的 1,667 個 run 中 70 個 ErrDeadlock、55 個 ErrStalled，都是真的。換線：3,000 個隨機模型中 1,590 個 compile（303 個有有限 buffer、662 個有故障、252 個有班表、108 個有批次），65,486 次授予在會換線的 unit 上、31,904 次換線（6,965 次 0 tick）、4,360 次取到不是第一個可用 unit 的設定相符 unit、472 次第一件；這種 resource 的停機：21,003 次立即、964 次在被釋放的 unit、356 次在修復好的 unit；79,764 次 service 結束的時刻被檢查 |
| Kernel：differential | `differential040_test.go`、`neutral_setups_test.go`；0.1.0–0.3.0 的 differential 照舊 | 見「Differential」 |
| Kernel：鎖 | `lock050_test.go`：`TestBatchSetupObservationDigests`（33 個 case）；舊的 `TestKernelObservationDigests`、`TestBlockingObservationDigests`、`TestBreakdownObservationDigests`、`TestCalendarObservationDigests` | 0.5.0 的編碼加上 Batch 第九個 word；舊的鎖不變 |
| Result | `des/batch_test.go`：`TestBatchGoldenResults`（15 個 case）、`TestBatchTrace`；`des/setup_test.go`：`TestSetupGoldenResults`（8 個 case）、`TestSetupTrace`、`TestNeutralSetupsKeepTheResult`、`TestBatchAndSetupIdentities`；`des/lock050_test.go`：`TestGoldenDigests050`（31 個 Result）；`TestGoldenDigests`、`TestGoldenDigests020`、`TestGoldenDigests030`、`TestGoldenDigests040`（engine_version 換回）；`reference_test.go` 的 `TestSimPyReferenceExport` | 每個 Result 依 schema 驗證、digest 重算；Result 的 identity 在 8 個隨機 reference case 的 30 replications 上 |
| Datasets | `internal/storage/datasets/setups_test.go`：`TestSetupDatasets` | 換線的 empirical 分布的 binding 與它的 path、0 tick 的樣本可以是換線 |
| Runner／API | `TestDeadlockError`、`TestStalledError` | fixture 的 engine_version 等於 `des.Version`（des-go-0.5.0） |
| AI | `internal/ai/review`：`TestBatchRisk`、`TestSetupRisk`、`TestBatchValuesInEffects`、`TestSetupValuesInEffects`、`TestLiftedPrimitivesHaveRiskCases`；`internal/ai/aicontext`：`TestScenarioMode` | 見 scope 4c、4d |
| SimPy | `make simulation-reference-test` | 見「SimPy reference」 |

### 先紅後綠

- 手推 golden（`batch-goldens.json`、`setup-goldens.json`）在 kernel 第一次執行前寫好；期望值都依各 case 的 `derivation` 手推輸入。
- **Validator**：`red-validate.log`（批次：43 行 `--- FAIL`（含子測試），gate 仍擋住 `batch`，回 `PRIMITIVE_NOT_SUPPORTED`）、`red-validate-setups.log`（換線：20 行）。
- **Engine**：`red-engine-vet-batch.log`：engine 的批次測試對實作前的 kernel 無法編譯（`ObsBatchStart`、`Program.IsBatch`、`Observation.Batch` 尚不存在）；`red-des-setups.log`（11 行 `--- FAIL`，含子測試：engine 仍以 invalid run input 拒絕 setups）、`red-des-setups-collector.log`（10 行：kernel 有換線、collector 還沒有換線組）。Kernel 的程式在本 session 中先於部分 engine 測試的程式碼寫成，所以 engine 的紅是以 overlay 對實作前的程式重現的；行為層面的紅由 mutation 證明（「Mutation」）。
- **AI**：`red-ai-batch.log`、`red-ai-setups.log`（各 9 行 `--- FAIL`（含子測試）：risk、effects 的數字、`TestLiftedPrimitivesHaveRiskCases`、scenario 規則）。
- **Datasets**：`red-datasets-setups.log`：`TestSetupDatasets` 對 T-050 之前的 `datasets.go`（`git show 4584ba6:…`，overlay）失敗（報在 `/ir/resources/0/setups/default/distribution/samples_ref`）；`green-datasets-setups.log`：通過。
- **第一次執行**：見「手推 golden」；`first-engine-run-setup-goldens.log` 的唯一失敗是 G-SU5 的模型太閒（測試的下限），不是期望值。
- **產生器太弱**：批次檢查器第一次執行時隨機產生器沒有達到測試自己的下限（`checker-batch-first-run-weak-generator.log`）；加強產生器，下限不變（`checker-batch.log`）。
- **Mutation 之後新增的測試**：`TestBatchVisitsChargedToTheirMember`（`routes-in-reverse` 在 run 2 存活，加入後被它擋下）、`TestBatchServiceDrawsOncePerBatch`（`batch-second-draw` 原本只被鎖擋下，加入後被它擋下）、G-BT15、G-BT16（`expired-limit-scheduled`、`stale-timeout-not-ignored` 在 run 1 存活，加入後被 `TestBatchGoldens` 擋下）、G-BT17（`batch-bundle-per-member` 在 run 2 存活，加入後被 `TestBatchGoldenResults` 擋下：`batch-bundle-per-member-gbt17.log`）；mutant 的 log 就是它們的紅。`TestBatchAndSetupIdentities` 第一次執行就通過，它的紅是 mutant `setup-end-not-applied`（`lock-first-mutants-without-locks.log`）。

## 手推 golden

`testdata/simulation/batch-goldens.json`（17 個）與 `setup-goldens.json`（9 個），status `HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN`；每個 case 的 `derivation` 是逐 tick 的手推（事件、priority、sequence、buffer 與 unit 的狀態），JSON 由 `checks/evidence/2026-10-26-T-050/batch_goldens_gen.py`、`setup_goldens_gen.py` 組成，腳本只組 JSON，沒有任何程式產生期望值。期望包括 arrival、service 開始、完成、批次（process、批號、開始時刻、成員）、BatchStart 的 sequence、換線（entity、process、resource、unit、開始、分鐘）與其 dispatch 的 sequence、阻塞、停機、等待中的故障、事件數，以及 Result 成員（有 `expected.result` 的 case）。

| ADR-026 原訂或 coordinator 指定的邊界 | Golden |
|---|---|
| D-054 G-T1（min＝max＝4，40、80、120…；等待 30、20、10、0，lead time 60…30） | G-BT1 |
| D-054 G-T2（min 2、max 4、max_wait 25：55 不就緒、60 開始） | G-BT2 |
| D-054 G-T3（2 chamber、9 件、max 4：一次 dispatch 開兩批留 1 件） | G-BT3 |
| D-054 G-T4（完成時下游已滿：chamber 持有到最後一個成員解除） | G-BT4 |
| D-054 G-T5（成員的 routing draw 依 FIFO） | G-BT5（期望由 stream 本身計算：結果不是回文，分得出順序） |
| min batch 恰在等待上限的 tick 達到 | G-BT6（arrival 與逾時同 tick，逾時找到已存在的 request）、G-BT7（逾時使 min 件就緒） |
| max batch 時還有更多 arrival | G-BT8（max 3、9 件；就緒的 request 等忙碌的 chamber，授予時取 3 件） |
| 批次跨越下班 | G-BT9（finish-first、overtime；下一批在下班時處於 B2 的狀態、下一班開始） |
| 批次中故障 | G-BT10（故障等整批做完，拿走它釋放的 chamber） |
| warm-up／量測視窗的邊界 | G-BT11（G-BT1 的 [80, 120]：W 與 H 恰為開始時刻的批次算，W 之前的不算）、G-SU8（[22, 41]：W 之前開始的換線只算 W 之後的部分，H 時還在進行的算開始、不算完成） |
| max_entities 在批中 | G-BT12（最後兩件永遠湊不成批、censored；湊批不是 deadlock：有限 buffer 使 B1 執行；成員空出的位置解除多個被擋的 entity） |
| D-050 B2 對 batch request | G-BT13（R4 被繞過：ErrStalled 回報最老成員，since 為進入 B2 狀態的 tick，此例即就緒的 tick；review 第 1 輪 M1 更正，原文「since 為就緒的 tick」）、G-BT18（review 第 1 輪：就緒時 bundle 還在使用中，since 晚於就緒的 tick） |
| 過時的逾時 | G-BT14、G-BT16（mutation 第 1 次之後） |
| max_wait 0 | G-BT15（mutation 第 1 次之後） |
| 同一批的兩個成員同時被擋 | G-BT17（mutation 第 2 次之後：bundle 在 blocked_ticks 只算一次，7 分；每個成員各算會是 10 分） |
| D-055 G-S1（1 unit、A→B、B→A 換線 3：第一件不換線、20–25、25–30、30 不換線、40 B2 先於 A4） | G-SU1（同產品之間換線為 0：A3 於 30–32） |
| D-055 G-S2（2 unit、狀態 A、B：B 的工件選 unit 1） | G-SU2 |
| D-055 G-S3（transition 優先、default、都沒有時 0、initial 有與無） | G-SU3（0 分鐘的換線仍記錄，D-097 (a)） |
| D-055 G-S4（中性與沒有 setups 逐筆相同） | 不是 golden：`TestNeutralSetupsChangeNothing`（differential）與 `TestNeutralSetupsKeepTheResult` |
| D-055 G-S5（capacity +1 時每個 unit 的 setup stream 不變） | G-SU5（`TestSetupStreamsPerUnit`，期望由 stream 本身計算，每個 unit 至少換線 20 次） |
| D-055 G-S6（等待中的故障落在換線中的 unit） | G-SU6 |
| 第一件的換線 | G-SU1、G-SU3（沒有 initial：第一件不換線；有 initial：與 initial 不同時換線） |
| 故障下的 unit identity | G-SU7 |
| 換線與班表、阻塞 | G-SU9（∫min(U − K − Z, S)，換線的結束沒有 observation）、G-SU10（被擋的 entity 持有它的 unit） |

第一次執行：批次 14 個（當時）、換線 8 個（G-SU5 除外）的 observation 與 Result 期望第一次就全部一致（`first-engine-run-*`、`first-des-run-*`）；G-SU5 的第一個模型太閒，unit 換線次數不到測試要求的 20 次（`first-engine-run-setup-goldens.log`），把模型改忙、下限不變。G-BT15、G-BT16、G-BT17 也是第一次就一致（`first-run-gbt15-gbt16.log`、`first-run-gbt17.log`）。沒有任何期望值在 engine 執行之後被改過。

## Differential（D-047）

- **對凍結的 des-go-0.4.0**（`frozen040_test.go`，7509c04；4584ba6 的 kernel.go、replication.go、events.go 以 T-049 的 go/ast 工具改名（後綴 040），共用 Program）：`TestDifferentialAgainstFrozen040`，50,000 個沒有批次與換線的隨機模型（`randomModel030` 的四種：一般、有限 buffer、故障、兩者；三分之一有班表、五分之二的 source 有投入排程），observation（kind、tick、seq、entity、node、resource、value、at）與 outcome、error 逐筆相同：50,000 個中 20,213 個 compile（4,572 個有有限 buffer、8,602 個有故障、5,291 個有班表、6,270 個有投入排程），11,773,609 個 observation 與 des-go-0.4.0 逐筆相同（其中 52,461 個 Blocked、219,031 個 ResourceDown、553,303 個 Capacity；結束：正常 16,627、active 上限 1,569、visit 上限 2,017）；golden model 與所有 blocking、breakdown、calendar golden 也比對。這些數字與開始前（`baseline-differential040.log`，凍結的 kernel 對它自己）完全相同。`make backend-test` 以 `GEMINUS_FULL_DIFFERENTIAL=1` 要求全部執行，race 下前 5,000 個。對 des-go-0.1.0、0.2.0、0.3.0 的 differential 與它們的中性設定照舊通過。
- **中性換線**（`TestNeutralSetupsChangeNothing`，D-047 step 2、ADR-026 G-S4、D-097 (h)）：上述隨機模型的 source 都改為同一種 entity type，部分 resource 加上永不換線的 setups（隨機的 default、有時有往返一個沒有 source 的 type 的 transition、有時 `initial` 就是那一種；D-055 的 P0 限制成立），新 kernel 與 des-go-0.4.0（它根本不讀 setups）逐筆相同（含 sequence）：6,000 個中 1,994 個 compile（436 個有有限 buffer、908 個有故障、550 個有班表），1,169,148 個 observation（25,984 個 ResourceDown、4,506 個 Blocked）逐筆相同；故障在中性設定下也落在確定的 unit，不改變 observation。Result 層 `TestNeutralSetupsKeepTheResult`：既有成員與 trace 不變，每個 resource 多一個換線組為 0 的 `resource_states`。
- **批次沒有中性設定**：min＝max＝1 的批次仍會改變 observation（BatchStart、request 在就緒時才取 sequence），D-047 step 2 不適用，所以有批次的模型沒有可與 des-go-0.4.0 比對的版本（SIMULATION_SPEC §3）。
- **鎖定的 digest**：0.1.0–0.4.0 的鎖不重產，Result 的 engine_version 換回各自的版本後重算的 digest 等於鎖定值（`TestGoldenDigests`、`TestGoldenDigests020`、`TestGoldenDigests030`、`TestGoldenDigests040`）；四個 kernel observation 鎖照舊通過。新版另立 `engine-golden-digests-0.5.0.json`（23 個 golden 的 Result、BT-S1–BT-S4、SU-S1–SU-S4）與 `kernel-observation-digests-0.5.0.json`（25 個 golden、8 個 reference case，編碼多 Batch 一個 word）：engine 輸出的 regression lock，在手推期望與 SimPy 通過之後才取。

## SimPy reference（`make simulation-reference-test`）

`scripts/simpy_batch_reference.py`（SimPy 4.1.2，`requirements-spec.txt` 已固定，沒有新 dependency）讀兩個 fixture 與 Go 的每個 replication 的結果（`TestSimPyReferenceExport` 先寫出 `bin/simpy-reference-go.json`）。站點由 D-054、D-055 的文字以 Python 寫成（不看 Go 程式），每個事件以明確的 priority 排在 SimPy 的 event queue 上（見「偏離」）；每個 resource 保存 unit 的狀態與設定，故障時鐘落在確定的 unit；抽樣用 Python 自己的 `random`（expovariate、triangular、lognormvariate、weibullvariate），不用 engine 的 stream 或 sampler。

- **接受的模型**（執行前檢查，其餘一律拒絕，不會當作成員不存在而照跑）：沒有有限 buffer、沒有班表、投入排程只能是 table、每個 process 至多需要一個 resource 的一個 unit 且該 resource 不被其他 process 需要、沒有 empirical。
- **確定性**（最後一次執行，`simulation-reference-test.log`）：G-BT1、G-BT2、G-BT3、G-BT6、G-BT7、G-BT8、G-BT10、G-BT11、G-BT14、G-BT15、G-SU1、G-SU2、G-SU3、G-SU6、G-SU7、G-SU8（16 個）逐值比對 service 開始、完成、每批與其成員、每次換線與其 unit 和時間、停機、等待中的故障與 Result 成員，都相同。必須拒絕的 7 個（G-BT4、G-BT12、G-BT17、G-SU10 有限 buffer；G-BT9、G-SU9 班表；G-BT16 共用 resource）都被拒絕；不比的 3 個（G-BT5、G-BT13、G-SU5）原因寫在 fixture。
- **隨機**：BT-S1（一個 chamber、min 2、max 4、max_wait 20）、BT-S2（兩個 chamber、滿批 3）、BT-S3（三站的線：prep、batch oven、test，10% scrap）、BT-S4（會故障的 chamber）、SU-S1（一個 unit、兩種產品）、SU-S2（三個 unit、三種產品、initial 與 default）、SU-S3（兩個會故障又換線的 unit）、SU-S4（兩站都換線的線）；30 replications、3,000 分鐘；47 個指標（completed、lead time、utilization、開始的批數與成員數、換線比例與次數、停機比例）都在 fixture 預先固定的容差內（|Δ| ≤ 4·√(s²_go/n＋s²_simpy/n)＋0.5%·max(|平均|)＋1e-9，沿用 T-047 的容差）。
- **T-047–T-049 的部分照舊**：`scripts/simpy_reference.py` 的確定性 case 與 140 個隨機比對都通過，它拒絕有批次或換線的模型；`calendar_reference.py` 0 個差異。
- **腳本開發中的執行**（只在終端機，log 沒有保留）：G-BT11、G-SU8 不一致，原因是腳本只記錄視窗內的完成（腳本自己的 bug，手推期望列的是所有完成），改為記錄所有完成後一致；arrival 的計數改為在抽下一個 arrival 之前加一。手推 golden 與容差都沒有改。
- **腳本的 mutation**：`py-unit-choice-ignores-state`、`py-ready-strictly-after-limit`、`py-no-changeover-time`（見「Mutation」）。

## Mutation

`checks/evidence/2026-10-26-T-050/t050-mutants.py`：每個 Go mutant 是一個精確的 search/replace，以 `go test -overlay` 套用（working tree 不變），列出的 package 有測試失敗即為 KILLED（`-failfast`：log 中的測試是第一個失敗的）；Result 與 trace 的 mutant 只跑會編譯到被改檔案的 package（`go list -test -deps`：des、trace、results；engine 的測試不編譯它們）；Python mutant 套用在 `scripts/simpy_batch_reference.py` 的暫存副本上，對 `bin/simpy-reference-go.json`（`make simulation-reference-test` 先寫出）執行，exit 非 0 即為 KILLED；pattern 不是恰好出現一次即為 BAD-MUTANT；標 equivalent 的必須存活，原因寫在 harness 中。`WITHOUT_LOCKS=1` 以 `go test -skip Digests` 略過 regression lock，用來確認被鎖先擋下的 mutant 也被其他測試擋下。

**最後的結果**：第 3 次（`mutants-run3.log`，最後的 tree b5467c0，完整一次）**55 個 mutant 中 52 個被擋下、3 個 equivalent**，exit 0。第一個失敗的測試是 0.5.0 的鎖的有 6 個（`one-stream-for-all-units` 與五個 Result mutant），在同一個 tree 上略過鎖重跑，6 個都被其他測試擋下（`mutants-run3-lock-first-without-locks.log`：`TestSetupStreamsPerUnit`、`TestSetupGoldenResults`、`TestSimPyReferenceExport`，exit 0）；其他 46 個被擋下的 mutant，第一個失敗的都是手推 golden、檢查器、由 stream 計算的測試、validator、binding、AI 的測試，或 SimPy 的比對。

| 類別 | Mutant（擋下它的測試） |
|---|---|
| 就緒（D-054、D-096 (b)(c)） | `ready-below-max`、`ready-ignores-min`、`ready-strictly-after-limit`（`TestBatchGoldens`）；`two-ungranted-requests`（`TestBatchGoldens`）；`dispatch-on-every-batch-entry`（沒有就緒也排 dispatch；`TestBatchGoldens`） |
| BATCH_TIMEOUT | `timeouts-for-min-equals-max`（`TestRandomBatchFailuresAreReal`）；`expired-limit-scheduled`（G-BT15）；`stale-timeout-not-ignored`（G-BT16） |
| 授予 | `batch-one-short`、`member-start-no-number`、`grant-no-recheck`（`TestBatchGoldens`）；`grant-unblocks-one`（`TestRandomBatchModelsAgainstSpec`）；`batch-second-draw`（`TestBatchServiceDrawsOncePerBatch`） |
| 批次結束 | `routes-in-reverse`（`TestBatchVisitsChargedToTheirMember`）；`members-take-routes-in-reverse`、`release-with-members-blocked`、`release-at-first-unblock`（`TestBatchGoldens`） |
| B1／B2（D-096 (g)） | `deadlock-ignores-timeouts`（`TestRandomBatchFailuresAreReal`）；`failure-names-no-member`（`TestBatchStall`） |
| 換線（D-055、D-097 (a)(e)） | `unit-choice-ignores-state`、`unit-choice-ignores-down`、`transition-ignored`、`default-ignored`、`zero-changeover-not-observed`、`first-piece-changes-over`、`state-not-updated`、`completion-ignores-changeover`（`TestRandomSetupModelsAgainstSpec`）；`one-stream-for-all-units`（第一個失敗的是 `TestBatchSetupObservationDigests`；單獨對 `TestSetupStreamsPerUnit` 也擋下：`one-stream-for-all-units-without-locks.log`） |
| 故障的 unit（D-097 (c)） | `failure-takes-the-clock-unit`、`release-frees-unit-0`、`downtime-resets-the-state`（`TestRandomSetupModelsAgainstSpec`） |
| Compile | `compile-skips-setup-limits`（`TestCompileChecksSetups`） |
| Result（D-096 (j)、D-097 (f)） | `batches-outside-window`（`TestBatchGoldenResults`／G-BT11）；`batch-bundle-per-member`（G-BT17）；`setups-started-outside-window`、`setup-complete-ignores-end`、`processing-ignores-changeover-on-shift`、`processing-ignores-changeover`、`setup-end-not-applied`（第一個失敗的是 `TestGoldenDigests050`；略過鎖時被 `TestSetupGoldenResults`（G-SU8、G-SU9）或 `TestSimPyReferenceExport` 擋下，`setup-end-not-applied` 也被 `TestBatchAndSetupIdentities` 擋下：`lock-first-mutants-without-locks.log`） |
| Trace | `trace-no-batch-id`（`TestBatchTrace`）；`trace-no-resource-id`（`TestSetupTrace`） |
| Validator | `batch-capacity-unchecked`、`batch-wait-required-at-equal-sizes`（`TestBatchRules`）；`setup-self-transition-allowed`（`TestAgreesWithSharedFixtures`）；`setup-two-units-allowed`（`TestSetupRules`） |
| Binding | `dataset-path-of-a-member`（`TestSetupDatasets`） |
| D-057 risk | `batch-risk-ignored`（`TestBatchRisk`）；`setup-risk-ignored`、`setup-risk-ignores-initial`（`TestLiftedPrimitivesHaveRiskCases`） |
| SimPy reference | `py-unit-choice-ignores-state`（G-SU2）、`py-ready-strictly-after-limit`（G-BT6）、`py-no-changeover-time`（G-SU1） |
| Equivalent | `ready-untimed-on-limit`（第 1 次 SURVIVED，分析後標為 equivalent）：untimed 即 min＝max，此時 count ≥ min 就是 count ≥ max，第一個子句已經成立；D-096 (b) 講的是逾時的排程，不是就緒。`release-hands-over-least-idle`、`repair-hands-over-least-idle`（預先標為 equivalent）：故障只在沒有閒置 unit 時等待（D-074 (a) B），每個變成可用的 unit 先給等待中的故障，所以釋放或修復時有等待中的故障的話，剛空出的 unit 是唯一閒置的 unit，「閒置 unit 中 index 最小者」就是它 |

**過程**（每一次的處理都先寫測試或手推期望，沒有降低任何 assertion）：

- **第 1 次**（`mutants-run1.log`，當時 harness 只有前 13 個批次 mutant）：13 個中 10 個被擋下、3 個 SURVIVED。`ready-untimed-on-limit` 分析後標為 equivalent（上表）；`expired-limit-scheduled`（已到期的上限也排一個逾時）與 `stale-timeout-not-ignored`（過時的逾時也當作有效）是測試太弱：新增手推 G-BT15（max_wait 0：已到期的上限不排事件，事件數與 sequence 不同）與 G-BT16（過時的逾時與兩個有效逾時同 tick：過時的若被當作有效，pa 的 request 會排在 pb 之前）。
- **第 2 次**（`mutants-run2.log`，分三段執行，kernel、collector、validator 與 review 的程式在三段之間沒有改變，只新增測試）：第一段 8 個中 7 個被擋下、1 個 equivalent、`routes-in-reverse` SURVIVED：路由反向解析、依 FIFO 提交時，目的地跟著 draw 走，只有 visit 算錯成員；新增 `TestBatchVisitsChargedToTheirMember`（第二段第一個就擋下它）。`batch-second-draw` 第一個失敗的是 0.5.0 的鎖：手推 golden 都是 constant service、SimPy 只做統計比較，新增 `TestBatchServiceDrawsOncePerBatch`（第三段第一個就擋下它）。第二段 11/11。第三段 27 個中 24 個被擋下、2 個 equivalent、`batch-bundle-per-member` SURVIVED：G-BT4 的第二個被擋成員在被擋的同一 tick 就移入，沒有任何 golden 有同一批的兩個成員同時被擋一段時間；新增手推 G-BT17（`batch-bundle-per-member-gbt17.log`：mutant 下 chamber 的 blocked_ticks 是 10 分而不是 7 分）。第三段中五個 Result mutant 第一個失敗的是 `TestGoldenDigests050`（des 的 `lock050_test.go` 在 `setup_test.go` 之前執行），略過鎖重跑都被其他測試擋下（上表）。
- **第 3 次**（`mutants-run3.log`，最後的程式與測試資料，完整一次）：55 個中 52 個被擋下、3 個 equivalent（上表），沒有 SURVIVED、BAD-MUTANT 或 BUILD-FAILED，exit 0，2,802 s（機器上沒有其他測試）；第 1、2 次存活的五個（`expired-limit-scheduled`、`stale-timeout-not-ignored`、`routes-in-reverse`、`batch-bundle-per-member`，與只被鎖擋下的 `batch-second-draw`）第一個失敗的都是為它們新增的 golden 或測試。被鎖先擋下的 6 個在同一個 tree 上略過鎖重跑都被擋下（`mutants-run3-lock-first-without-locks.log`）。

## Fuzz

都以 `-fuzztime Nx`（次數為準）、`-parallel 2` 在最後的 tree 上執行（T-049 的經驗：以時間為準時最後一批的 exec 數不可信）。

| Fuzzer | Execs | 時間 | 結果 |
|---|---:|---:|---|
| `FuzzBatches`（新；隨機批次模型的 seed 與是否繞過 validator；每個 run 對只看 observation 的檢查器；經 validator 的模型不得 deadlock 或 stall，繞過時失敗必須是真的） | 100,000 | 183 s（前段與 backend-race 同時） | PASS（`fuzz-batches-count.log`）；之前先跑的 20,000 次也是 PASS（log 被覆寫，`exit-codes.txt`） |
| `FuzzSetups`（新；隨機換線模型的 seed；對檢查器；valid 的模型不得 deadlock 或 stall） | 100,000 | 218 s | PASS（`fuzz-setups-count.log`） |
| `FuzzKernel`（seed 含批次與換線的 golden 模型；recorder 另外檢查 ObsSetup 只出現在授予中、在 queue 與 service 開始之間） | 500,000 | 22 s | PASS（`fuzz-kernel-count.log`） |
| `FuzzRun`（seed 含批次與換線的 golden 模型：typed failure 或 schema-valid、digest 可重算的 Result 與 trace） | 200,000 | 21 s | PASS（`fuzz-run-count.log`） |

Exec 數包括很快就 decode 或 compile 失敗的輸入（`FuzzKernel`、`FuzzRun` 的輸入是任意 bytes；`FuzzBatches`、`FuzzSetups` 的產生器也會產生 compile 不了的模型），不等於跑完的模型數。新的 interesting input 只存在 Go 的 fuzz cache，沒有失敗的 input，repository 中沒有新的 `testdata/fuzz` 檔案。

## 新測試的執行時間

`engine-package-times.log`：整個 engine package（`GEMINUS_FULL_DIFFERENTIAL=1`，機器上沒有其他測試）122.3 s，其中 T-050 新增的測試：

| 測試 | 時間 |
|---|---:|
| `TestDifferentialAgainstFrozen040`（50,000 個模型；race 下 5,000 個） | 15.28 s |
| `TestRandomSetupModelsAgainstSpec`（3,000 個模型） | 4.33 s |
| `TestRandomBatchModelsAgainstSpec`（3,000 個模型） | 3.86 s |
| `TestRandomBatchFailuresAreReal`（validator 被繞過的 1,667 個 run） | 3.50 s |
| `TestNeutralSetupsChangeNothing`（6,000 個模型） | 2.32 s |
| 其他（golden、鎖、stream、visit、service draw、Compile） | < 0.2 s |
| 合計 | 約 29.5 s |

同一個 package 中舊的 differential：0.1.0（`TestDifferentialAgainstFrozenKernel`）16.3 s、0.2.0 15.8 s、0.3.0 13.9 s，所以 0.4.0 的 differential 與它們同一個量級。des package：`TestBatchAndSetupIdentities` 0.15 s、其他 T-050 的 Result 測試都 < 0.1 s（`engine-tests-verbose.log`）。

在 `make backend-test` 中（package 平行執行、彼此競爭）engine 是 226 s，開始前（4584ba6，`baseline-backend-test.log`）是 131 s；`make backend-test` 整體 253 s。兩次都和 check-architecture（223 s、193 s）同時執行，競爭的程度不同，所以 95 s 的差不等於 T-050 的成本；單獨執行時 T-050 的測試約 29.5 s。Race 下（`make backend-race`，與其他指令同時）engine 811 s（T-049 的 review 第 1 輪是 605 s），在 Makefile 的 30 分鐘上限內，但比較接近；coordinator 若在 CI 上看到 race 的時間接近上限，可以像 differential 一樣減少 race 下的批次與換線檢查器、中性換線的模型數（目前沒有這樣做：只有 differential 在 race 下減少，其他的數量與非 race 相同）。

## Migration 與 contract

- **沒有 migration**：IR 的 `batch`、`setups`，Result 的 `batches`、`resource_states` 的換線組與 trace 的 `batch_id`、`setup_start`（`resource_id`）都在 T-046 phase 2 的 schema 中（`schemas/model-ir.schema.json`、`simulation.schema.json`）；ModelVersion、Result 與 trace 都存成 JSON（D-047 的 A：schema_version 維持 0.1），DB 的 shape 不變，所以不需要新的 migration。`migrations/` 沒有改。
- **Schema 與 OpenAPI 沒有改**：`schemas/`、`openapi/openapi.yaml` 與產生的 TS 型別都沒有變（`PASS_TS_CONTRACTS_FRESH`，15 檔、109 types）。
- **Go 型別**：`model.ResourceDistribution` 加 `Path`（換線的 default 本身就是分布，path 不是 member＋`/distribution`），`model.Resource.Distributions()` 列出換線的分布；報告 path 的呼叫者（validator 的分布檢查、`internal/storage/datasets` 的 binding）改用 `Path`，AI review 的 `values()` 經它讀到換線分布的數字。`engine.Observation` 加 `Batch`；`ObsBatchStart`、`ObsSetup` 編號在既有 kind 之後（D-047：舊的 kind 值不變）。
- **engine_version**：des-go-0.5.0；Result 的 digest 含 engine_version，所以所有 Result 的 digest 都改變（照 ADR-026 Consequences：每個改 kernel 的任務升一個 minor），舊版本的鎖以換回 engine_version 的方式驗證。

## NOT RUN

- **所有 DB 測試，交給 coordinator 在一次性 DB 上執行**：`make integration-test`（`make db-test` 與 `make db-go-test`）。這裡沒有 PostgreSQL，依指示沒有跑、也沒有嘗試以其他方式取得 DB。本任務沒有 migration，`db/tests/*.sql` 沒有改（`PASS_DB_TESTS_FRESH`）。DB-backed 的 Go 測試在本 worktree 中只 compile 與 vet（`make backend-test` 的 `go vet` 與 test binary 的編譯），執行時 skip（`db-tests-skipped.log` 列出全部 skip）：`internal/runs/runner`（engine_version 不符的拒絕等）、`internal/api` 的 run、scenario、proposal 測試、`cmd/api`、`cmd/worker`、`internal/worker`、`internal/models`、`internal/proposals/...`、`internal/scenarios`、`internal/storage/...`、`internal/tenant/...`。
- **有批次或換線的模型經 API 的 closed loop**（publish → scenario → run → result）只在 validator、patch、review、engine 與 des 層測過。
- `make frontend-test`：沒有 TS、schema 或 `apps/web` 的變更（`PASS_TS_CONTRACTS_FRESH`），未執行。UI 如何顯示批次與換線屬 T-031／T-032／T-034。
- `make e2e-test`：沒有 UI 變更，未執行。`make benchmark-test`：仍是 NOT IMPLEMENTED 的 placeholder（T-037）。
- Live AI 與 `make ai-eval-live`：live AI 未開啟（T-053 之前），沒有讀取任何 API key；scenario-0.6.0 的真實模型回答沒有評分（T-038）。
- `make backend-race` 只在 linux/amd64 執行；`make backend-numeric` 只檢查其他 GOARCH 的組語，沒有在實體 arm64 等機器上執行數值測試。
- CI 上沒有執行（只在本機）。
- ADR-026 原訂而沒有做的驗證：見「偏離與原因」，已列入 T-037 的 scope。

## 需要 owner 決定

兩項都是 **PROPOSED**（docs/DECISIONS_AND_TBD.md 的「T-050 的提案」與 planning/decisions.json；`approved_by`／`approved_at` 為 null）。建議都已實作，可以回退；不採用時要改的地方寫在各項最後。T-050 以它們為 gate，所以 `make spec-decisions` 不再把 T-050 列為 ready。

- **D-096 — 批次的 kernel 解讀**（D-048、D-049、D-050、D-054、D-058 沒有寫到的細節）。建議：(a) 成員在授予時決定：取授予時刻的 min(等待數, max_size)，就緒後才進入的也算（A；B：就緒時凍結）；(b) BATCH_TIMEOUT 只在 min < max 時排程，min＝max 時允許但不使用 max_wait（A；B：每次都排，多出不起作用的事件與 sequence）；(c) 最老成員以進入序號識別、世代號判定過時、已到期時當場檢查，等於上限的 tick 即就緒；(d) 授予的 observation 順序、一次 service draw、空出 k 個位置解除至多 k 個被擋的 entity、授予後再檢查；(e) 只有就緒的批次排 dispatch；(f) 完成時的 visit 上限依 FIFO、前面的成員已提交；(g) B1 在還有待處理的逾時（含過時的與晚於 horizon 的）時不判定 deadlock，B1／B2 回報最老成員、since 為就緒的 tick（A；B：不計晚於 horizon 的逾時，更早報 deadlock）（review 第 1 輪改寫：B2 的 since 依 D-074 (c)、晚於 horizon 的逾時是 ADR-026 D-050 B1 已批准的文字、建議改為 B 但未實作，見文末「Review 第 1 輪」）；(h) ObsBatchStart kind 17、`Observation.Batch`、批號從 1 起；(i) max_wait 量化後超過 365 天為 `BATCH_INVALID`；(j) 每個批次 process 都有 `batches[<process>]`，批次的 bundle 在 K 中只算一次。
- **D-097 — 換線的 kernel 解讀**（D-047、D-051、D-055、D-058、D-074 沒有寫到的細節）。建議：(a) 0 tick 的換線也是換線（記 ObsSetup、計數，A；B：不記也不計）；(b) 可用的 unit＝既不在使用中也未停機，下班的 unit 不另外識別；(c) 故障落在確定的 unit：立即停機取閒置 unit 中 index 最小者，等待中的故障由被釋放或修復好的 unit 承擔，停機不改變設定（A；B：時鐘 u 固定對應 unit u，會出現有閒置 unit 卻要等待的情形）；(d) ObsSetup kind 18 的欄位與位置、trace 的 `setup_start` 不帶 unit；(e) 只有換線有分布時才從 (resource ID, `setup/<u>`) 抽，先於 service，完成在換線＋service 之後；(f) Result 的換線組與 ∫min(U − K − Z, S)；(g) validator 的 path；(h) 只有一種 entity type 而 `initial` 就是那一種也是中性設定。

Owner 也許想特別看的取捨：D-096 (a)（授予時決定成員：燒機室實際上是「開門時把排隊的放進去」，選 B 則就緒後才到的要等下一批）、D-096 (g)（保守地不在有待處理逾時時報 deadlock）、D-097 (a)（0 分鐘的換線在換線次數中算一次）與 D-097 (c)（故障的 unit identity）。

## 偏離與原因

- **ADR-026 D-054 原訂的 SimPy 做法沒有採用**（ADR-026 D-054「SimPy」：buffer 為 `simpy.Store`、一個 batcher process request chamber）。原因：SimPy 的 Store 與 Resource 以 NORMAL priority、依建立順序授予，表達不了 D-048 同一時刻的順序（完成、arrival 與 delay 結束、故障／修復／等待上限、dispatch），而確定性 case（G-BT6 的 arrival 恰在等待上限的 tick、G-BT14 的過時逾時）依賴這個順序。腳本改為把每個事件以明確的 priority（0 完成、1 arrival 與 delay 結束、2 故障／修復／等待上限、3 dispatch）排在 SimPy 的 event queue 上，站點與 batcher 的規則由 D-054 的文字以 Python 寫成（不看 Go 程式）。D-055 的 SimPy 做法（自寫的 dispatcher 管理 unit 狀態，搭配 SimPy 的時間推進）照原訂。偏離寫在腳本的 docstring、SIMULATION_SPEC §10 與 T-037 的 scope。
- **SimPy reference 拒絕的組合沒有獨立 reference**：有限 buffer（G-BT4、G-BT12、G-BT17、G-SU10）、班表（G-BT9、G-SU9）與兩個 process 共用的 resource（G-BT16）由腳本拒絕（必須拒絕，否則 `make simulation-reference-test` 失敗）；它們只由手推 golden 與只看 observation 的檢查器驗證。ADR-026 的「確定性案例逐值比對、隨機案例以容差比對」因此只涵蓋沒有阻塞、班表與共用 resource 的模型；列入 T-037 的 scope。
- **不比的確定性 case**：G-BT5（routing draw 是 engine 的 stream，SimPy 用 Python 的 random；G-BT5 本身由 stream 計算期望）、G-BT13（SimPy 不偵測 stall）、G-SU5（stream identity 的測試）。
- **被 0.5.0 的鎖先擋下的 mutant**：`batch-second-draw`（每批抽兩次 service）原本只被鎖擋下，新增 `TestBatchServiceDrawsOncePerBatch` 後第一個失敗的就是它；`one-stream-for-all-units`（所有 unit 用 unit 0 的 stream）的第一個失敗是鎖（`lock050_test.go` 在 `setup_test.go` 之前執行），單獨對 `TestSetupStreamsPerUnit` 執行也被擋下（`one-stream-for-all-units-without-locks.log`）；五個 Result mutant 的第一個失敗是 `TestGoldenDigests050`，略過鎖時都被手推 golden 或 `TestSimPyReferenceExport` 擋下（`lock-first-mutants-without-locks.log`）。鎖只是 engine 自己的輸出，所以這些都另外確認了不依賴鎖的測試。
- **Benchmark 沒有做**：ADR-026 Consequences 的效能一項（有 setups 的 resource 每次授予以線性掃描選 unit，O(C)；BATCH_TIMEOUT 每次最老成員改變排一個事件）屬 T-037 的 benchmark；`make benchmark-test` 仍是 NOT IMPLEMENTED 的 placeholder。只有本報告「新測試的執行時間」的測試時間。
- **G-S4 不是 golden**：ADR-026 D-055 的 G-S4（中性設定與沒有 setups 逐筆相同）以 differential（6,000 個隨機模型）與 Result 層的比對實作，比單一 golden 更強。
- **Fixture 中 ADR 的編號改名**：ADR-026 D-054 的 G-T1–G-T5 在 fixture 中是 G-BT1–G-BT5（G-T 已用於 D-048 的同 tick golden），D-055 的 G-S1–G-S6 是 G-SU1–G-SU6（G-S 已用於 T-048 的 B2 golden）。
- **本 branch 的 D-081–D-085 等仍是 PROPOSED**：coordinator 通知 owner 已批准（H-024），狀態變更在 main branch 上由 coordinator commit；本 branch 不重複修改，以免 cherry-pick 衝突。

## 剩下的工作

批次與換線兩個 slice 都已交付，scope 中沒有留下未做的項目。T-050 完成之前還需要：

- owner 決定 D-096、D-097（PROPOSED；T-050 的 gate）；
- coordinator 在整合後的 tree 上跑 `make integration-test`（一次性 DB）與其他驗證，並交給獨立 reviewer（H-003）；
- 列入 T-037 的後續（不屬 T-050）：D-054 原訂的 SimPy 做法、批次或換線與阻塞、班表、共用 resource 的獨立 reference、有批次與換線的 benchmark。

## 殘餘風險

- **D-096、D-097 仍是 PROPOSED**：owner 若選其他選項，kernel、golden 與 0.5.0 的兩個鎖要改（各項的「不採用時」）；T-050 在那之前不能標為 done。
- **批次或換線與阻塞、班表、共用 resource 的組合只有手推 golden 與檢查器**（見「偏離」）：同 tick 的順序與 finish-first 的交互由 kernel 自己的 golden 與只看 observation 的檢查器驗證，沒有獨立的 SimPy reference。
- **檢查器與 kernel 由同一個 session 寫成**：檢查器只由 IR 與 observation 重建狀態、不呼叫 kernel 的函式，但對 D-054、D-055 文字的理解相同；不同的理解只會由 SimPy（限於它接受的模型）或 reviewer 找到。
- **Mutation 是抽樣**：55 個 mutant 之外的規則仍可能只有鎖（engine 自己的輸出）在保護；harness 中原本只被鎖擋下的 `batch-second-draw` 已補測試，其他被鎖先擋下的也確認了被別的測試擋下（見「偏離」）。
- **Regression lock 不是 oracle**：`engine-golden-digests-0.5.0.json`、`kernel-observation-digests-0.5.0.json` 是 engine 的輸出；語義由手推 golden、檢查器與 SimPy 驗證。
- **Differential 只涵蓋沒有批次的模型**：批次沒有中性設定（D-047 step 2 不適用），有批次的模型與 des-go-0.4.0 沒有可比的對象。
- **效能沒有量測**：unit 的線性掃描、BATCH_TIMEOUT 的事件數、`unitOf` map 與 collector 的 min-heap 的成本沒有 benchmark（T-037）；只有測試時間。
- **DB／API 層沒有在本 worktree 執行**：有批次或換線的模型經 publish → scenario（含 AI scenario 改批量或換線時間）→ run → result 的 closed loop 只在 validator、patch、review、engine 與 des 層測過；`internal/storage/datasets` 的 binding path 改動只有 unit test（DB-backed 的部分 skip）。
- **AI**：live model 對新的 scenario 規則（scenario-0.6.0）的回答沒有評分（T-038）；edit mode 的 prompt 沒有改（scope 只到 scenario allowlist 的文字）。

## Coordinator 的執行（f4db5d6，與 T-053 一起整合）

Coordinator 把本任務的十一個 commit（14b7000–79c9423）接在 T-053 的五個 commit 之後 cherry-pick 到 `claude/lucid-meitner-vq1awo`，之後是 T-053 的修正（fc0b277、4450490、921910f）與 H-026 的紀錄（f4db5d6）。衝突只在 `Makefile`（本任務改 `simulation-reference-test` 的說明，T-053 新增 live 的兩個 target，兩邊都保留）、`planning/decisions.json` 與決策登記檔（D-096、D-097 與 D-110–D-115 都是新增，兩邊都保留），以及重產的 `checks/*.json`。D-081–D-085 的狀態依 H-024 已在 main branch 上改為 APPROVED。

**第一次執行（79c9423，`checks/evidence/2026-10-26-T-050-T-053/coordinator-*-first-run*`）**：`spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`frontend-test`、`simulation-reference-test`（`"differences": 0`）、`backend-numeric`、`backend-race` 都是 exit 0；`make integration-test` exit 2，原因是 T-053 的 migration 0016（`ai_spend_reservations.id` 沒有 D-001 domain），與本任務無關，由 T-053 的 4450490 修正。

**第二次執行（f4db5d6，全新的 `dt_test_t050t053b`）**：`spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`backend-race`、`integration-test` 都是 exit 0（`coordinator-exit-codes.txt`）。`frontend-test`、`simulation-reference-test`、`backend-numeric` 只在第一次執行：之後的 commit 沒有改 `apps/`、`internal/simulation/`、`internal/results/`、`schemas/`、`openapi/` 或 `scripts/`，結果仍適用。

- 「殘餘風險」中「DB／API 層沒有在本 worktree 執行」：整合後的 tree 上 `make integration-test` 通過，表示既有的 run、scenario 與 comparison 路徑沒有被 0.5.0 弄壞；但整合測試沒有含批次或換線的模型，有批次或換線的模型經 publish → scenario → run → result 的 closed loop 仍未在 DB／API 層測過（列入 T-037 的後續）。
- **NOT RUN**：`make e2e-test`（本任務沒有改 UI）；`make benchmark-test`（placeholder，T-037）；CI 在 push 之後確認。
- 本任務仍待：獨立 review（H-003）；owner 決定 D-096、D-097（PROPOSED）。

## Review 第 1 輪（2026-10-26）：CHANGES REQUESTED（四個 Medium、三個 Low、Info）

獨立 reviewer（H-003）的報告在 `checks/evidence/2026-10-26-T-050-r1/reviewer/REVIEW.md`（沒有 High；M1–M4、L1–L3、I1–I6）。本輪在 `t050-r1`（從 `claude/lucid-meitner-vq1awo` 的 180c86f 開出；先 cherry-pick reviewer 的 584cc53 為 207eaba，衝突只在產生的 `checks/docs-check.json`，取本 branch 的版本、由 spec-check 重產；不 push）修正。每個修正都先寫測試或手推 golden（推導逐 tick 寫在 fixture 的 `derivation`，engine 執行前寫好），確認它在 reviewer 的 mutant（或照舊文字讀法的 mutant）下失敗（red），再確認在現在的程式上通過；沒有降低任何 assertion、沒有加 skip、沒有改任何既有 golden 的期望值（兩個 builder 重產後舊的 case 逐值相同，只多新 case）。**Kernel（`internal/simulation/engine`）與 collector（`internal/results/metrics`）的程式沒有改**；改的只有測試、fixture、獨立的 Python 向量、AI 的 scenario 規則與 prompt（L3）、決策與規格文字。Reviewer 的報告、probe 與工具只讀取使用。Evidence 在 `checks/evidence/2026-10-26-T-050-r1/`（每個 log 開頭是時間、cwd 與指令，結尾是 `# exit=… elapsed=…`；`scripts/` 是本輪的工具）。D-096、D-097 仍是 **PROPOSED**（`approved_by`／`approved_at` 為 null）；T-050 在 `planning/tasks.json` 改為 `in_progress`（I6）。

**與 coordinator 指示不同之處（M2）**：coordinator 決定把 D-096 (g) 改為 B（晚於 horizon 的 BATCH_TIMEOUT 不使 D-050 B1 停止判定）並實作。查 ADR-026 D-050（ACCEPTED，H-018，D-050 採 A）的 B1 定義時發現，它的文字就是 A：「有 blocked entity 或 held candidate；heap 中沒有 SERVICE_COMPLETE、DELAY_COMPLETE、BATCH_TIMEOUT，**也沒有這些事件因晚於 horizon 被丟棄**；且每個 held candidate 都是 release-bound」。B 因此不是批准文字沒寫到的細節，而是修訂已批准的規則；依 AGENTS.md §1（來源與衍生文件矛盾時停止受影響的部分、記錄差異；不得自行把 proposed 轉為 approved），本輪**沒有改 kernel**：以手推 golden 固定已批准的行為（A），D-096 (g) 依 coordinator 的決定把建議改為 B，寫明 B 修訂已批准的文字、owner 批准後才實作，並寫出 A 的取捨、B 與晚於 horizon 的服務（D-064 (g)）不一致之處和一致的 C。B 需要改的程式與它對鎖的影響（沒有）都已確認（見 M2）。

### Commit

| Commit | 內容 |
|---|---|
| 207eaba | Reviewer 的報告與 evidence（584cc53 的 cherry-pick） |
| 59ce20b | 手推 golden G-BT18–G-BT22（`batch-goldens.json`）、G-SU11、G-SU12（`setup-goldens.json`；builder `batch_goldens_gen.py`、`setup_goldens_gen.py` 只加 case）；`scripts/engine_reference.py` 的 `setup_streams` 與 `engine-vectors.json`（只加這一項）；`TestBatchStall` 跑每個 stall golden、新的 `TestBatchDeadlocks`、`calendarCase` 的 optional `deadlock` 期望、`TestSetupStreamVectors`、測試中的數量；兩個 0.5.0 鎖加入 G-BT21、G-SU11、G-SU12（`add_lock_entries.py`：既有的每一筆都與 engine 現在的輸出相同才寫入）；第一次執行、鎖更新前後與 SimPy 的 log |
| 5d7d8c9 | L3：`aicontext.modeRules` 與 prompts/scenario 寫明 max_wait 的 365 天上限，scenario-0.6.0 → scenario-0.7.0；`TestScenarioMode`；red／green log |
| e99e91b | Mutation harness `t050-mutants.py` 加入 reviewer 存活或只被鎖擋下的 6 個 mutant、照 D-096 (g) 舊文字讀法的 M1 mutant、reviewer 的 equivalent mutant（I2）：63 個 |
| 927c071 | D-096 (g)(i)、D-097 (i)（DECISIONS_AND_TBD 與 decisions.json，仍為 proposed）、SIMULATION_SPEC §3、§5、§10、TEST_PLAN、TWIN_IR_SPEC（scenario-0.7.0）、tasks.json 的 T-050 狀態；每個新測試在其 mutant 下的 red log、B 的 mutant 下的鎖、reviewer 的 probe P1–P6 重跑 |
| （最後一個） | 本節、本報告前面 M1 指出的三處與 D-096 (g) 摘要的更正、D-097 (e)(f) 引用新 golden；驗證 log、`mutants-run4*.log`、`exit-codes.txt`；`make spec-check` 重產的 `checks/*.json` |

### Commit 對照（I6）

本報告前面「Commit」表引用的是 `t050` branch 的 hash；coordinator 依序 cherry-pick 到 `claude/lucid-meitner-vq1awo`（`git log --oneline 8948b70..79c9423`），標題相同。Reviewer 確認 `git diff b5467c0 4715f49` 在 T-050 的程式上只差 H-024 的 APPROVED 字樣的註解。

| `t050` | `claude/lucid-meitner-vq1awo` | 標題（`T-050: …`） |
|---|---|---|
| 7509c04 | 14b7000 | freeze the des-go-0.4.0 kernel for the D-047 differential |
| 7c25863 | f49d104 | validator rules of batch processes (BATCH_INVALID), gate lifted in the validator |
| 2fc93c7 | 88f34e3 | des-go-0.5.0 batch processes (D-054): kernel, Result, trace, D-057 risk |
| 7e8df45 | 0461dce | observation-only checker of batch processes, property tests, fuzz seeds |
| 57927ef | fd8aa08 | changeovers (D-055) in des-go-0.5.0: validator, kernel, Result, trace, D-057 risk |
| 30e4200 | ce01c55 | SimPy reference of batch processes and changeovers |
| 86a933f | 3317c7a | des-go-0.5.0 regression locks of its own |
| 457f43c | 7e43691 | D-096, D-097 (PROPOSED); specs, ADR, test plan, planning |
| f1c57e0 | fbb828a | G-BT15, G-BT16 and tests after mutation runs 1 and 2; mutation harness |
| b5467c0 | 4715f49 | G-BT17 and Result identities after mutation run 2 |
| 4c7c7be | 79c9423 | validation report and the verification on the final tree |

### M1（Medium）：D-096 (g) 與 SIMULATION_SPEC §3 寫錯 B2 對 batch request 的 since

- **發現**：文字寫「B1、B2 對 batch request 回報它的最老成員，since 為批次就緒的 tick（G-BT13）」，本報告 scope 3b 與「手推 golden」表的 G-BT13 列也這樣寫；kernel 的 `stall()` 與只看 observation 的檢查器（`kernel_check_batch_test.go` 在新的 batch request 時 `delete(c.cal.since, batchKey(p))`，之後由 B2 的更新記錄）對任何 request（含 batch request）都照 D-074 (c)：since 是第一次在 tick 結束時處於 B2 狀態的 tick。G-BT13 中兩者都是 20 分，分不出。Reviewer 的 P1：批次在 20 分就緒，machine 被另一個 process 用到 100 分，kernel 報 since 100 分。
- **修正**（依 coordinator 的決定：已批准的是 D-074 (c)，改文字不改程式）：D-096 (g)（DECISIONS_AND_TBD、decisions.json）、SIMULATION_SPEC §3、TEST_PLAN、本報告 scope 3b 與 G-BT13 列改為 D-074 (c) 的讀法（標「review 第 1 輪 M1 更正」）；B1 沒有被擋的 entity 時（`Queued`）的 since 才是 batch request 建立、即批次就緒的 tick（見 L2）。手推 golden **G-BT18**（P1 的模型，R4 被繞過）：t=20 就緒時 machine 在使用中，不是 B2 的狀態；t=100 P1 做完、machine 釋放，operator 下班，從這個 tick 結束起處於 B2 狀態：since 100 分（6,000,000 tick），deadline next_fit(100＋480)＝960；t=960 上班、machine 立即停機，仍在狀態中：ErrStalled 於 960 分，回報 `r0/srcw/1`、node work。`TestBatchStall` 改為跑每個有 `stalled` 期望的 golden（G-BT13、G-BT18）。
- **先紅後綠**：`first-run-new-goldens.log`：G-BT18 第一次執行就與手推一致；`red-m1-gbt18-under-m1-b2-since-ready-tick.log`：照舊文字讀法的 mutant `m1-b2-since-ready-tick`（batch request 的 B2 since 取就緒的 tick）下 G-BT13 通過、G-BT18 失敗（since 1,200,000，手推 6,000,000）。
- **擋下的測試**：`m1-b2-since-ready-tick` 被 `TestBatchStall`／G-BT18 擋下（harness 第 4 次）。Reviewer 的 P1 斷言舊文字的讀法，所以重跑仍是 exit 1（`probe-rerun-P1-b2-since.log`，依設計），由 G-BT18 取代。

### M2（Medium）：晚於 horizon 的 BATCH_TIMEOUT 與 B1 沒有測試；B 是已批准文字的修訂

- **發現**：Reviewer 的 mutant `r-b1-ignores-timeouts-after-horizon`（D-096 (g) 的 B：只計 ≤ horizon 的逾時）通過整個 engine package；P2：G-D2 單獨在 7 分 ErrDeadlock，加上一個不相關、等待上限在 101 分的批次（horizon 60）後跑到 horizon、沒有錯誤。Reviewer 建議改為 B（晚於 horizon 的逾時永遠不會處理，計入只會關掉 B1，連不相關的循環等待也藏起來），或給出 A 避免誤報的例子；coordinator 決定實作 B。
- **與已批准文字的衝突**：見本節開頭。ADR-026 D-050 B1 的同一句對 SERVICE_COMPLETE、DELAY_COMPLETE 也成立：kernel 的 `inService`、`inDelay` 不論完成是否晚於 horizon 都計（SIMULATION_SPEC §3 的 B1；D-064 (g)，APPROVED：循環旁一個一直在服務的不相關 process 使 B1 不判定，這是批准文字的**全域**定義）。所以 A 不只是字面，也與已批准的服務、delay 一致；B 只對逾時改這條規則，「做到 horizon 之後的服務」仍關掉 B1、「horizon 之後的等待上限」不再關掉。三個選項都只影響 validator 被繞過的模型（valid 的模型因 R1 不會走到 B1）。本輪沒有實作 B，交 owner 決定。
- **修正**：(1) 手推 golden **G-BT19**（P2 的模型：G-D2＋`bsrc` 在 1 分投入一件到沒有 resource 的批次 process `bp`（min 1、max 2、max_wait 100、service 5）→ done；horizon 60，R1 被繞過）：推導先逐 tick 重推 G-D2 的循環（7 分形成）；A 下逾時（101 分）晚於 horizon、不排程但算待處理，B1 不判定，run 跑到 horizon、沒有錯誤：src 的 entity 從 7 分起每分鐘被擋在 src、z 的每分鐘完成，129 個事件（121 個 arrival、4 個 SERVICE_COMPLETE、4 個 DISPATCH）；B 下在 7 分 ErrDeadlock（同 G-D2）。**G-BT20**（同一模型、max_wait 30）：逾時（31 分）在 horizon 內，A、B 都使 B1 等待；31 分逾時使批次就緒、開始，36 分批次結束（不排 dispatch）後 ErrDeadlock（2,160,000 tick，回報 `r0/src/5` 在 src、since 5 分）：固定「horizon 內的逾時照樣使 B1 等待、處理之後不再計」。新的 `TestBatchDeadlocks` 跑有 `deadlock` 期望的 golden（G-BT20、G-BT22），G-BT19 在 `TestBatchGoldens` 中（RunReplication 不得有錯誤，事件數、service 開始、完成與阻塞逐筆比對）。兩者都繞過 validator（R1），不在鎖中。(2) 文字：D-096 (g) 依 coordinator 的決定**建議 B**，寫明 B 修訂已批准的 ADR-026 D-050 B1 文字（對 BATCH_TIMEOUT 刪去「也沒有…因晚於 horizon 被丟棄」）、owner 批准前 kernel 照 A；A 列為已批准、已實作的選項並附取捨；另列 C（三種事件晚於 horizon 的都不計：一致，但改變 T-047–T-049 已批准的 B1）。採 B 時要改：`kernel.batchCheck`（只計 進入＋max_wait ≤ horizon 的逾時；即 mutant `r-b1-ignores-timeouts-after-horizon`）、檢查器的 `pendingTimeouts`（同樣只計 ≤ horizon）、G-BT19 的期望（ErrDeadlock 於 7 分）、ADR-026 D-050 B1 與 SIMULATION_SPEC §3 的文字。
- **鎖**：B 的 mutant 下只跑鎖（`optionB-locks-engine.log`、`optionB-locks-des.log`）：0.1.0–0.5.0 的 observation 鎖與 Result 鎖都通過，所以 B 不會改變任何鎖定的 digest（鎖定的 case 都經過 validator，不會走到 B1）。本輪沒有改 kernel，鎖中既有的 digest 都沒有改變（見「鎖定的 digest」）。
- **先紅後綠**：G-BT19、G-BT20 第一次執行就與手推一致（`first-run-new-goldens.log`）；`red-m2-gbt19-under-optionB.log`：B 的 mutant 下 G-BT19 失敗（`the replication deadlocked at tick 420000: r0/src/5 is blocked at node src since tick 300000`），其他批次 golden 通過；`red-m2-gbt20-under-deadlock-ignores-timeouts.log`：B1 不看逾時的 mutant 下 G-BT20 在 7 分 ErrDeadlock（手推 36 分）。
- **擋下的測試**：`r-b1-ignores-timeouts-after-horizon` 被 `TestBatchGoldens`／G-BT19 擋下（harness 第 4 次）；`deadlock-ignores-timeouts` 原本被 `TestRandomBatchFailuresAreReal` 擋下，現在也被 G-BT20 擋下。Reviewer 的 P2 斷言 A，重跑通過（`probe-rerun-P2-b1-timeout-after-horizon.log`）。

### M3（Medium）：兩條已批准的規則只有鎖在固定

**(a) 換線的 stream 名稱 (resource ID, `setup/<u>`)**（D-055、ADR-026 D-055）

- **發現**：`r-setup-purpose-renamed`（`"setup/"` → `"setups/"`）只被 0.5.0 的鎖擋下；`TestSetupStreamsPerUnit` 從同一個 Go 常數算期望，SimPy 用 Python 自己的 `random`；`engine-vectors.json` 沒有 `setup/` 的向量。
- **修正**：比照 T-048 的 `ttf/<u>`、`ttr/<u>`（`scripts/engine_reference.py` 的 `breakdown_streams`；Go 端 `TestBreakdownStreamVectors` 與 `TestBreakdownStreams` 都以字面寫 stream 名稱）：`engine_reference.py` 新增 `setup_streams`：G-SU5 的 resource（smt）、default（exponential，mean 3 分）與 seed（4242，replication 0），capacity 3，每個 unit 以 Python 的 SplitMix64、stream seed 與 exponential sampler（同一支腳本已逐 bit 對過 Go 的部分）抽 20 個換線時間與之後的 stream state，寫入 `engine-vectors.json`（`--write` 前 `engine_reference.py` 回 FAIL，之後 PASS；diff 只加 92 行；`PASS_ENGINE_REFERENCE_VECTORS` 多 `setup_stream_units: 3`）。`TestSetupStreamVectors`：(1) Go 的 `Stream(rep, "smt", "setup/"+u)`（名稱以字面寫在測試中）經 default 的 sampler 逐 bit 等於向量，stream state 也相同；(2) kernel 在 G-SU5 的模型上（capacity 2 與 3）每個 unit 的前 20 次換線時間等於向量中該 unit 的 draw；並檢查向量確實是 G-SU5 的 resource、default 與 seed。
- **先紅後綠**：第一次執行通過（`first-run-new-goldens.log`）；`red-m3-vector-under-setup-purpose-renamed.log`：改名的 mutant 下（略過鎖）`TestSetupStreamsPerUnit` 通過、`TestSetupStreamVectors` 失敗（每個 unit 的換線都不是向量中的 draw）。
- **擋下的測試**：`r-setup-purpose-renamed` 被 `TestSetupStreamVectors` 擋下，略過鎖時也是（harness 第 4 次的兩段）。

**(b) 換線在 run 的最後一個 observation 之後結束**（D-097 (f)、SIMULATION_SPEC §5）

- **發現**：`r-setup-ends-not-applied-at-finish`（拿掉 `Collector.Finish` 的 `c.endSetups(c.h)`）只被 `TestGoldenDigests050` 擋下；沒有任何手推 golden 有在最後一個 observation 之後、H 之前結束的換線（G-SU8、G-SU9 之後都還有 observation）。
- **修正**：手推 golden **G-SU12**（P6：G-SU1 的模型、horizon 49）：到 32 分與 G-SU1 相同；40 分 src_a 的下一件（50）晚於 H、不排程、不取 sequence，所以之後的 sequence 比 G-SU1 少一；A4 在 45 分換線（到 48），service 要到 50（晚於 H，不排程），45 分之後沒有任何 observation，A4 的換線只能由 `Finish` 結束。`setup_ticks` 12 分（720,000）、4 次開始、4 次完整（12 分）、`in_use_ticks` 23 分、`scheduled_processing_ticks` 23 − 12＝11 分（660,000）；19 個事件，換線的 DISPATCH sequence 8、11、17、19；5 件完成、A4 censored。**與 coordinator 的數字不同**：指示寫「`scheduled_processing_ticks` 600000 by hand」；手推是 660,000，600,000 是 mutant 下的值（Z 在 [48, 49] 仍為 1：Z 13 分、processing 23 − 13＝10 分）；reviewer 的報告也寫「660000 vs 600000」（kernel 對 mutant）。SimPy reference 也逐值比對 G-SU12，與手推相同。
- **先紅後綠**：第一次執行通過；`red-m3-gsu12-under-setup-ends-not-applied-at-finish.log`：mutant 下 G-SU12 的 `setup_ticks` 780,000、`scheduled_processing_ticks` 600,000（手推 720,000、660,000），其他換線 golden 通過。
- **擋下的測試**：`r-setup-ends-not-applied-at-finish` 被 `TestSetupGoldenResults`／G-SU12 擋下，略過鎖時也是。

### M4（Medium）：完整換線的 W 邊界

- **發現**：`r-setup-complete-strict-w`（`o.Tick >= c.w` → `>`）通過所有測試（含鎖）；G-SU8 的 W（22）沒有恰在 W 開始的換線。H 的一側與批次的 W／H 已有測試。
- **修正**：手推 golden **G-SU11**（P5：G-SU1 的模型、warm-up 20、horizon 55）：B1 的換線恰在 W＝20 開始、23 結束：算開始，也算完整（開始 ≥ W）；4 次開始、4 次完整（12 分）、`setup_ticks` 12 分、`in_use_ticks` 24 分（utilization 24/35）、processing 12 分；6 件完成（A1 在 W 之前）、lead time 5、10、2、5、10、2、queue wait 0、5、0、0、5、0；50 分之後 A5 的 service 到 52，在 H 內；24 個事件，換線的 DISPATCH sequence 8、11、17、20。SimPy 也逐值比對，相同。
- **先紅後綠**：第一次執行通過；`red-m4-gsu11-under-setup-complete-strict-w.log`：mutant 下 G-SU11 的 `setups_completed` 3、`completed_setup_ticks` 540,000（手推 4、720,000）。
- **擋下的測試**：`r-setup-complete-strict-w` 被 `TestSetupGoldenResults`／G-SU11 擋下，略過鎖時也是。

### L1（Low）：沒有 resource 的批次 process

- **發現**：validator 接受沒有 resource 的批次 process（`TestBatchRules`），但沒有 golden、鎖或檢查器跑過它；`r-batch-end-dispatch-without-resources`（批次結束一律排 dispatch）通過所有測試，它改變事件數與 sequence。
- **修正**：手推 golden **G-BT21**（P4：4 件、min＝max＝2、service 5、沒有 resource）：批次結束沒有 bundle 可釋放，不排 dispatch，與一般 service 結束相同；8 個事件（4 arrival、20 與 40 分的 DISPATCH、25 與 45 分的 SERVICE_COMPLETE），BatchStart 在 DISPATCH sequence 4 與 7；Result：4 件完成、lead time 15、5、15、5、queue wait 10、0、10、0、batches[oven] 2 批 4 件、沒有 resource_states。SimPy 逐值比對，相同。手推通過後加入兩個 0.5.0 鎖。
- **先紅後綠**：第一次執行通過；`red-l1-gbt21-under-batch-end-dispatch-without-resources.log`：mutant 下 10 個事件、b2 在 DISPATCH sequence 8（手推 8、7）。
- **擋下的測試**：`r-batch-end-dispatch-without-resources` 被 `TestBatchGoldens`／G-BT21 擋下。

### L2（Low）：B1 對 batch request 的 `Queued` since

- **可達性（證明）**：B1 的 `Queued` 分支要求沒有被擋的 entity（nBlocked＝0）、沒有服務中或 delay 中的 entity、每個 held candidate 都 release-bound（bundle 中某個 r 有 inUse_r＋units > C_r）。Unit 只由服務中的 entity（含服務中的批次）與被擋的 entity（持有 bundle 的 process，或成員被擋、bundle 還沒釋放的批次）持有；delay 與 source 的 backlog 不持有 resource。所以 inService＝0 且 nBlocked＝0 時每個 r 的 inUse_r＝0，release-bound 就是 units > C_r：validator 以 `RESOURCE_REQUIREMENT_INVALID` 拒絕（`validate/ir.go`），Compile 不再檢查。分支因此在 validator 被繞過時到得了，一般 request 也一樣；它不是 kernel 內部不變量被破壞（是模型本身 invalid），所以**沒有改成 `ErrInternal`**，保留現在的 typed `ErrDeadlock`（與一般 request 相同），D-096 (g) 寫明是防禦。
- **修正**：手推 golden **G-BT22**：src 兩件（1、2 分）→ q（capacity 2）→ bp（min＝max＝2，需要 capacity 1 的 r 的 2 個 unit，validator 被繞過）：2 分就緒、held；之後 B1：ErrDeadlock 於 2 分（120,000），沒有被擋的 entity，回報最早的 held request：batch request 的最老成員 `r0/src/1`、node bp、since 2 分（request 建立的 tick）、`Queued`（"waits for"）；最老成員在 1 分進入。
- **先紅後綠**：第一次執行通過；`red-l2-gbt22-under-b1-since-oldest-entry.log`：`r-b1-since-oldest-entry`（since 取最老成員的進入）下 since 60,000（手推 120,000）。
- **擋下的測試**：`r-b1-since-oldest-entry` 被 `TestBatchDeadlocks`／G-BT22 擋下。

### L3（Low）：AI 的 scenario 規則少一個 BATCH_INVALID 條件

- **修正**：`aicontext.modeRules` 的 SCENARIO_PATCH_ALLOWLIST 加「max_wait is at most 365 days once quantized to 1 ms ticks」，prompts/scenario 加「max_wait 量化為 1 ms tick 後至多 365 天」（D-096 (i)、`validate/batches.go`）。Prompt 的文字改了，所以照 repo 的做法升版本：scenario-0.6.0 → **scenario-0.7.0**（`prompts/embed.go` 由第一行讀出版本，每次 provider 呼叫都記錄；其他 fixture 或 golden 沒有帶 scenario 的版本或 prompt hash：`internal/proposals/generator` 的測試用自己的 metadata，`checks/ai-evals/*.json` 在 `make ai-eval` 重跑後沒有改變）。TWIN_IR_SPEC、TEST_PLAN、D-096 (i) 的版本與文字一併更新。
- **先紅後綠**：`red-l3-scenario-rules.log`：`TestScenarioMode` 要求 scenario-0.7.0 與兩段文字中的上限，對舊的規則與 template 失敗（3 個 error）；`green-l3-scenario-rules.log`：通過（`TestTemplates` 也通過）。Live model 沒有看過 scenario-0.7.0（NOT RUN，T-038）。

### Info

- **I1**：D-097 新增 (i)：沒有 `initial` 時從未使用過的 unit 不算與任何產品相符，不比需要換線的 unit 優先（D-055 的字面規則；建議 A，已實作），以 reviewer 的 P3 為例，列出 B（視為不必換線，排在需要換線的 unit 之前）與其後果，請 owner 確認；沒有改程式（P3 重跑：b1 取 unit 0、換線 7 分，`probe-rerun-P3-fresh-unit.log`）。
- **I2**：`r-setup-end-applied-late` 加入 harness，標為 equivalent（reviewer 的理由寫在 harness 中：`integral.set` 加 level ×（t − since）並移動 since，晚套用的結束仍在任何較晚 tick 的 observation 之前、以它自己的 tick 套用，只重排同一 tick 的長度 0 的步驟；run 結束時恰在 H 的結束沒套用只涵蓋 [H, H]）；第 4 次的兩段都必須存活（EQUIVALENT）。
- **I3**：檢查器與 SimPy 腳本與 kernel 由同一個 session 寫成、讀法相同，是殘餘風險（見原報告「殘餘風險」）；本輪的 7 個新 golden 都是手推，SimPy 另外逐值比對了其中可比的三個（G-BT21、G-SU11、G-SU12）。
- **I4**：本輪的 7 個 golden 的推導與期望值都在 engine 執行它們之前寫在 builder 中；`first-run-new-goldens.log` 是 engine 第一次執行它們，全部一致，沒有任何期望值在執行後改過。
- **I5**：沒有需要修正的地方。
- **I6**：T-050 在 tasks.json 改為 `in_progress`；commit 對照見上。

### 鎖定的 digest

本輪沒有改 kernel 或 collector，**0.1.0–0.5.0 的鎖中沒有任何既有的 digest 改變**：`locks-before-update.log`（新 case 加入 fixture 之後、鎖更新之前）只有兩個 0.5.0 鎖因新 case 失敗，0.1.0–0.4.0 的鎖都通過；`add_lock_entries.py` 先確認每一筆既有的 entry 與 engine 現在的輸出相同、新的 ID 恰為指定的三個，才加入新的 entry（只加不改）。兩個 0.5.0 鎖加入 G-BT21、G-SU11、G-SU12：`kernel-observation-digests-0.5.0.json` 33 → 36 個 case、`engine-golden-digests-0.5.0.json` 31 → 34 個 digest（繞過 validator 的 G-BT18、G-BT19、G-BT20、G-BT22 不鎖），都在它們的手推期望通過之後（`first-run-new-goldens.log`），SimPy 也比對過這三個（`simulation-reference-test-first.log`）；`locks-after-update.log` 全部通過。兩個鎖的 `status` 文字記錄了這三筆的來由。D-096 (g) 的 B 也不改變任何鎖（M2）。

### Reviewer 的 probe P1–P6（重跑）

`scripts/probes.sh` 以 `go test -overlay` 把每個 probe 加進它的 package（沒有在 tree 中加檔案），各自一個 log：

| Probe | Exit | 結果 |
|---|---:|---|
| P1 B2 since（`probe-rerun-P1-b2-since.log`） | 1 | 依設計失敗：它斷言舊文字的讀法（since＝就緒的 tick）；kernel 報 since 100 分，與手推的 G-BT18 相同（M1） |
| P2 B1 與晚於 horizon 的逾時（`probe-rerun-P2-b1-timeout-after-horizon.log`） | 0 | 斷言 A：沒有錯誤；G-BT19、G-BT20 取代（M2） |
| P3 從未使用的 unit（`probe-rerun-P3-fresh-unit.log`） | 0 | 只記錄：b1 取 unit 0、換線 7 分；D-097 (i)（I1） |
| P4 沒有 resource 的批次（`probe-rerun-P4-resource-free-batch.log`） | 0 | 8 個事件；G-BT21 取代（L1） |
| P5 恰在 W 開始的換線（`probe-rerun-P5-setup-at-w.log`） | 0 | 4 次完整；G-SU11 取代（M4） |
| P6 最後一個 observation 之後結束的換線（`probe-rerun-P6-setup-end-after-last-observation.log`） | 0 | setup_ticks 720,000、processing 660,000；G-SU12 取代（M3） |

### Mutation（第 4 次）

`t050-mutants.py`（e99e91b，63 個 mutant）在 tree 外的 scratch copy（`git archive` 的 e99e91b；程式與測試資料與 927c071 相同）上完整執行兩次，mutant 都以 `go test -overlay` 套用（`scripts/mutants.sh`；Python mutant 用同一個 tree 的 `bin/simpy-reference-go.json`）：

| 段 | 結果 | 時間 | Log |
|---|---|---:|---|
| 有鎖 | **59 個被擋下、4 個 equivalent**，沒有 SURVIVED、BAD-MUTANT 或 BUILD-FAILED，exit 0 | 4,059 s | `mutants-run4.log` |
| `WITHOUT_LOCKS=1`（`go test -skip Digests`） | **59 個被擋下、4 個 equivalent**，exit 0 | 2,853 s | `mutants-run4-without-locks.log` |

本輪加入的 8 個：`m1-b2-since-ready-tick`（`TestBatchStall`／G-BT18）、`r-b1-ignores-timeouts-after-horizon`（`TestBatchGoldens`／G-BT19）、`r-setup-purpose-renamed`（有鎖時第一個失敗的是 `TestBatchSetupObservationDigests`，略過鎖時 `TestSetupStreamVectors`）、`r-setup-ends-not-applied-at-finish`（有鎖時 `TestGoldenDigests050`，略過鎖時 `TestSetupGoldenResults`／G-SU12）、`r-setup-complete-strict-w`（有鎖時 `TestGoldenDigests050`，略過鎖時 `TestSetupGoldenResults`／G-SU11）、`r-batch-end-dispatch-without-resources`（`TestBatchGoldens`／G-BT21）、`r-b1-since-oldest-entry`（`TestBatchDeadlocks`／G-BT22）都被擋下；`r-setup-end-applied-late` 兩段都存活（EQUIVALENT，I2）。第一個失敗的測試是鎖的共 9 個（第 3 次的 6 個與本輪的上述 3 個，des 的 `lock050_test.go` 在 `setup_test.go` 之前執行、engine 的鎖在 `setup_test.go` 之前執行）；略過鎖時 9 個都被其他測試擋下：`one-stream-for-all-units`（`TestSetupStreamsPerUnit`）、`setups-started-outside-window`、`setup-complete-ignores-end`、`processing-ignores-changeover-on-shift`、`processing-ignores-changeover`（`TestSetupGoldenResults`）、`setup-end-not-applied`（`TestSimPyReferenceExport`）與本輪的三個。其他已有的 mutant 結果與第 3 次相同，只有 `deadlock-ignores-timeouts` 的第一個失敗改為 `TestBatchGoldens`（G-BT19：不看逾時就在 7 分 ErrDeadlock），第 3 次是 `TestRandomBatchFailuresAreReal`。4 個 equivalent 是 `ready-untimed-on-limit`、`release-hands-over-least-idle`、`repair-hands-over-least-idle`（理由在原「Mutation」）與 `r-setup-end-applied-late`。

### 實際執行（本輪）

在 `t050-r1` 上（`environment.txt`：Go 1.27.3、Python 3.13.16、SimPy 4.1.2、PostgreSQL 16.15、4 CPU；機器同時有另一個 session 的整合測試與本輪的 mutation harness，load average 8–23，所以時間含競爭）。下表的指令都在 927c071 的程式與測試資料上執行（之後的 commit 只加本報告、D-097 (e)(f) 的引用與 evidence）；Go 測試都以 `-count=1` 執行。Exit code 收在 `exit-codes.txt`。

| 指令 | Exit | 時間 | 說明 |
|---|---:|---:|---|
| 新 golden 與向量的第一次執行（`scripts/first-run.sh`） | 0 | 2 s | `first-run-new-goldens.log`：`TestBatchGoldens`（17）、`TestBatchStall`（2）、`TestBatchDeadlocks`（2）、`TestSetupGoldens`（10）、`TestSetupStreamVectors`、`TestSetupStreamsPerUnit`、`TestBatchGoldenResults`（16）、`TestSetupGoldenResults`（10），7 個新 golden 第一次就與手推一致 |
| 鎖（更新前／後，`scripts/locks.sh`） | 1／0 | 2 s／5 s | `locks-before-update.log`：只有兩個 0.5.0 的鎖因新 case 失敗；`locks-after-update.log`：全部通過 |
| 每個新測試在其 mutant 下（`scripts/red-runs.sh`，scratch copy，略過鎖） | 各 1 | 2–6 s | 8 個 `red-*.log`（見各 finding） |
| L3 的 red／green（`scripts/ai-scenario.sh`） | 1／0 | 1 s | `red-l3-scenario-rules.log`、`green-l3-scenario-rules.log` |
| B 的 mutant 下只跑鎖 | 0／0 | 2 s／5 s | `optionB-locks-engine.log`、`optionB-locks-des.log`：0.1.0–0.5.0 的鎖都通過 |
| Reviewer 的 probe P1–P6（`scripts/probes.sh`，overlay） | 1、0×5 | 1–3 s | 見上表；P1 依設計失敗 |
| `make backend-test GOFLAGS=-count=1` | 0 | 584 s | `backend-test.log`：gofmt、go vet、74 個 package ok，沒有 `(cached)`；`GEMINUS_FULL_DIFFERENTIAL=1`；engine 505 s、des 21 s、check-architecture 514 s；DB-backed 測試 skip（由 integration-test 執行） |
| `make architecture-check` | 0 | 19 s | `architecture-check.log`：86 packages、23 rules、16 build configurations |
| `make backend-numeric` | 0 | 328 s | `backend-numeric.log`：六個 target 沒有 FMA 與平台 math call，GOAMD64=v3 的數值測試通過 |
| `make ai-eval` | 0 | 2 s | `ai-eval.log`：`TestFakeProviderEval`、`TestReviewEval` PASS；`checks/ai-evals/*.json` 沒有改變；live provider NOT RUN |
| `make simulation-reference-test PYTHON=…`（第一次，鎖更新前） | 0 | 28 s | `simulation-reference-test-first.log`：batch／changeover 的確定性 case 19 個（多 G-BT21、G-SU11、G-SU12），0 個 failure |
| `make simulation-reference-test PYTHON=…`（最後） | 0 | 29 s | `simulation-reference-test.log`：T-047–T-049 的 140 個隨機比對、T-050 的 19 個確定性 case 與 47 個隨機比對都通過；`calendar_reference.py` 0 個差異 |
| `go test -race ./internal/simulation/... ./internal/results/...` | 0 | 722 s | `race-simulation-results.log`：6 個 package ok（engine 712 s，differential 在 race 下跑前 5,000 個） |
| `go test -race ./internal/ai/... ./prompts/` | 0 | 36 s | `race-ai-prompts.log`：10 個 package ok（L3 的改動所在） |
| `FuzzBatches`、`FuzzSetups`（各 `-fuzztime 20000x -parallel 2`） | 0／0 | 54 s／69 s | `fuzz-batches-20000x.log`、`fuzz-setups-20000x.log`：PASS；seed 含本輪的新 golden 模型 |
| `make integration-test`（全新的 `dt_test_t050f1`） | 0 | 401 s | `integration-test.log`：db-test（SQL），之後 db-go-test（`-race`、`-p 1`）35 個 package ok，0 個 FAIL；本輪改了 `internal/ai/aicontext` 與 prompt（kernel 之外），所以執行 |
| `make spec-check PYTHON=…`（本節寫好之後） | 0 | 10 s | `spec-check.log`：`PASS_ENGINE_REFERENCE_VECTORS`（多 `setup_stream_units: 3`）、`PASS_TS_CONTRACTS_FRESH`（15 檔、109 types：沒有 contract 變更）、`PASS_INDEPENDENT_HASHES`、`PASS_DB_TESTS_FRESH`（14）；重產的 `checks/decisions-check.json`：proposed 8（D-096、D-097 仍在），T-050 為 in_progress 後不再列在 `next_after_review`；`checks/docs-check.json`：Markdown 107 → 108（reviewer 的 REVIEW.md）；`planning-check.json` 沒有改變 |
| Mutation harness 第 4 次（63 個，有鎖／`WITHOUT_LOCKS=1`） | 0／0 | 4,059 s／2,853 s | `mutants-run4.log`、`mutants-run4-without-locks.log`（見「Mutation」） |

### NOT RUN（本輪）

- `make backend-race`（完整的 race）：機器與另一個 session 的整合測試共用 4 個 CPU，本輪改跑本輪改動所在 package 的 race（見上表）；`make integration-test` 的 db-go-test 也以 `-race` 執行。原報告與 coordinator 的完整 `make backend-race` 是 exit 0，本輪沒有改 kernel 或 collector 的程式。
- `make frontend-test`、`make e2e-test`：沒有 `apps/`、schema、OpenAPI 或產生的 TS 型別的變更（`PASS_TS_CONTRACTS_FRESH`）。`make benchmark-test`：仍是 placeholder（T-037）。
- Live AI（`make ai-eval-live`、`make ai-smoke-live`）：scenario-0.7.0 沒有 live model 的評分（T-038），沒有讀取任何 API key。
- 較長的 fuzz：`FuzzBatches`、`FuzzSetups` 各只跑 20,000 次；`FuzzKernel`、`FuzzRun`（seed 多了本輪的 7 個模型）NOT RUN。
- 有批次或換線的模型經 API／DB 的 closed loop：仍未測（列入 T-037 的後續，同原報告）。
- CI：只在本機。
- D-096 (g) 的 B：沒有實作（M2，等 owner）。

### 需要 owner 決定（本輪的變化）

- **D-096 (g)**：(1) B2 對 batch request 的 since 改寫為 D-074 (c)（已批准的規則，文字更正；kernel 一直如此）；(2) **晚於 horizon 的 BATCH_TIMEOUT**：A＝ADR-026 D-050 B1 已批准的文字（現在的實作）、B＝建議（review 第 1 輪 coordinator 的決定；只對逾時修訂已批准的文字，與晚於 horizon 的服務不一致）、C＝三種事件晚於 horizon 的都不計（一致，改變 T-047–T-049 已批准的 B1）；選 B 或 C 時同時修訂 ADR-026 D-050 B1，kernel 與 G-BT19 依 D-096 (g) 的「採 B 時」修改，鎖不變；(3) `Queued` 分支為防禦（只在 validator 被繞過時到得了）。
- **D-096 (i)**：AI 的 scenario 規則寫明上限（scenario-0.7.0）。
- **D-097 (i)**（新）：沒有 `initial` 時從未使用過的 unit 是否優先於需要換線的 unit；建議 A（D-055 的字面，已實作）。
- 其他不變；兩項仍是 PROPOSED，T-050 仍以它們為 gate。

### 殘餘風險（本輪的變化）

- **M2 沒有照 coordinator 的指示實作 B**：原因是 B 修訂已批准的 ADR-026 D-050 B1 文字（見本節開頭）。在 owner 決定之前，validator 被繞過的模型中，一個最老成員的等待上限晚於 horizon 的批次仍會使 B1 在 run 剩下的時間都不判定（與做到 horizon 之後的服務相同）；valid 的模型不受影響（R1）。
- **檢查器、SimPy 腳本與 kernel 的讀法相同**（I3）：本輪新增的都是手推 golden 與獨立的 Python 向量，但 SimPy 仍不接受有限 buffer、班表與共用 resource，B1／B2 的 golden（G-BT18–G-BT20、G-BT22）只有手推與檢查器。
- **Mutation 仍是抽樣**：reviewer 的 28 個 mutant 中存活或只被鎖擋下的 6 個都補了不依賴鎖的測試，另一個為 equivalent；harness 沒有涵蓋的規則仍可能只有鎖在保護。
- **ADR-026 的實作進度文字**（「T-050 …待獨立 review」）沒有改，由 coordinator 在整合與 review 結束後更新。
