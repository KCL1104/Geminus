# T-048 驗證報告：DES resource 故障與停機（MTBF/MTTR，H-016）

**日期：2026-10-25｜狀態：待獨立 review；`planning/tasks.json` 的狀態未改（仍為 not_started），由 coordinator／reviewer 決定。｜需求：REQ-011、REQ-013、REQ-024｜Gate：D-047、D-048、D-050、D-051、D-056、D-057 已 APPROVED（H-018），D-063 已 APPROVED（H-019），D-064 已 APPROVED（H-021）；本任務提出的 D-074、D-075 是 **PROPOSED**，`approved_by`／`approved_at` 為 null，T-048 依建議實作並以它們為 gate（tasks.json 的 `decision_gates` 加入 D-064、D-074、D-075）。｜驗收測試：TEST-015、TEST-019、TEST-034（第二部分）｜DB 測試與 `make integration-test` 為 NOT RUN（見「NOT RUN」）。**

範圍依 tasks.json 與 coordinator 的指示（每一條都要做）：

1. Resource 故障：TTF／TTR 各用自己的 stream（resource ID、`ttf/<u>`／`ttr/<u>`），finish-first（D-051 A）；D-050 R3（`RESOURCE_REQUIREMENT_INVALID`，bundle feasibility 階段）；runtime B2 `ErrStalled` → `SIMULATION_STALLED`（TTR 的部分）；D-063 (c)（lognormal mean／Weibull scale 超過 365 天 → `LIMIT_EXCEEDED`）；lognormal 與 Weibull（參數範圍 → `DISTRIBUTION_INVALID`）；resource 分布的 limits、dataset binding、manifest 與 D-033 檢查；TTF 可能為 0 tick → `DISTRIBUTION_INVALID`。
2. Result：`resource_states` 的停機組與 available 組（`down_ticks`、`breakdowns_started`、`repairs_completed`、`completed_repair_ticks`、`available_ticks`、`idle_ticks`）與 availability；事件順序與 tie-break 的手推 golden。
3. 沒有故障的模型與 des-go-0.2.0 逐 byte 相同（對凍結副本的 differential），升 engine_version（des-go-0.3.0）。
4. 修復不經釋放而增加 capacity 時喚醒等待的 request；B1 要區分 release-bound 與 capacity-bound，先寫測試，含 deadlock bound 的 `>`→`>=` mutation 必須被擋下。
5. 只解除 `breakdowns`、`lognormal`、`weibull`：`validate.checked`、`engine.kernelExecutes`、`TestPrimitiveCatalog`、`TestPrimitiveGate`、`TestKernelRefusesWhatItDoesNotExecute`、cases.json 的 primitives；`internal/ai/review` 的 D-057 server risk；`aicontext.modeRules` 與 prompts/scenario 的 allowlist 說明；重新判定 eval MOD-038，以 `make ai-eval` 更新 `checks/ai-evals/fake-review.json`。R4 `STALL_RISK` 屬 T-049，不做。
6. SimPy reference（TEST-034）延伸到故障：`scripts/simpy_reference.py` 與 fixtures，明確寫出接受哪些模型、其餘拒絕。

依 coordinator 的指示：只在自己的 worktree（`t048`，從 21423aa 開出）工作、commit、不 push；沒有 PostgreSQL，所有 DB 測試 NOT RUN；沒有 migration；新 decision 只用 D-074–D-077（實際用 D-074、D-075，D-076、D-077 未用）；沒有新的 dependency；沒有任何 live AI 呼叫，沒有讀取 `ANTHROPIC_API_KEY` 或 `GEMINUS_ANTHROPIC_*`；沒有碰 T-049（calendars、R2、R4）、T-050（batches、setups）、imports、proposals 的範圍；沒有改任何既有 decision 的狀態；沒有降低任何 assertion 或加 skip。逐條輸出在 `checks/evidence/2026-10-25-T-048/`。

## Commit

`t048` branch，依時間順序（從 21423aa 開出）：

| Commit | 內容 |
|---|---|
| ffc141c | 凍結 21423aa 的 des-go-0.2.0 kernel、replication 與 event queue（`frozen020_test.go`，以 go/ast 改名，只在測試中）；`TestDifferentialAgainstFrozen020`（50,000 個隨機模型，奇數 seed 加有限 buffer） |
| 60fb361 | Validator：R3（新的 bundle feasibility 階段）、lognormal／Weibull 的範圍與下溢、TTF 的零時間、resource 分布的 limits、binding 與 D-033；`validate.checked` 加三個 primitive；`internal/storage/datasets` 的 resource 分布 |
| 7a22c5a | Kernel des-go-0.3.0：故障時鐘、finish-first、修復的喚醒、C − D 的 dispatch、B1 的 capacity-bound、B2 `ErrStalled`；lognormal／Weibull sampler、pinned exp 與 AS241；Compile 的 resource sampler、TTRmax、manifest；Result 的停機組；trace 不記故障；手推 golden、只看 observation 的檢查器、differential、0.3.0 的鎖、engine-vectors、`check_no_fma.sh` |
| 2eac291 | `ErrStalled` → 不重試的 `SIMULATION_STALLED`（runner），API 為 `MODEL_VALIDATION_FAILED`（`internal/runs`）；`valid-run-failed-stalled.json` |
| e70c855 | D-057 的 breakdowns risk、expected effects 可引用的數字、scenario 規則 scenario-0.4.0、MOD-038 重新判定、`fake-review.json` |
| bcb2463 | SimPy reference 延伸到故障（`scripts/simpy_reference.py`、`TestSimPyReferenceExport`、Makefile 的說明） |
| 5d641da | D-074、D-075（PROPOSED）；tasks.json 的 gate、planned outputs、spec refs（status 未改）；SIMULATION_SPEC、TWIN_IR_SPEC、ADR-026、TEST_PLAN、ARCHITECTURE、CONTRACT_BLOCKERS、THIRD_PARTY_NOTICES |
| 0c8b1f0 | 手推 golden G-F1W（warmup 12 分之後的視窗，`TestBreakdownGoldens` 與 `TestBreakdownGoldenResults`）與它在兩個 0.3.0 鎖中的項目；TEST_PLAN |
| （最後一個） | 本報告、evidence、mutation harness 與結果、重產的 `checks/*.json` |

只有最後的 tree 經過下面「實際執行結果」的完整驗證（mutation 的第 2 次完整執行在加入 G-F1W 之前；G-F1W 只加測試資料與鎖，之後重跑兩個相關的 mutant）；中間的 commit 是依主題分開的檢視單位，沒有各自跑一次全部的測試。

## 稽核：scope 逐條

| Scope | 判定 | 證據／處理 |
|---|---|---|
| 1a. TTF／TTR 的 stream、finish-first（D-051 A） | 已完成 | `internal/simulation/engine/kernel.go`：每個 unit u 一個故障時鐘，TTF 由 (resource ID, `ttf/<u>`)、TTR 由 (resource ID, `ttr/<u>`) 抽樣（首次使用時建立，`kernel.streams`）；FAILURE（priority 15）有閒置 unit（C − D − in_use > 0）時立即停機，否則等待下一個 job 釋放的 unit（FIFO）；釋放時先給等待中的故障，再喚醒 held front；REPAIR 抽下一個 TTF。`TestBreakdownStreams`（沒有 process 需要的 resource，停機恰為兩個 stream 的抽樣；capacity 3 與 4 時 unit 0–2 相同）、`TestBreakdownStreamVectors`（對 Python port 的向量）、golden G-F1–G-F3、G-F7、G-T1、G-T2、G-T4 |
| 1b. R3（bundle feasibility 階段） | 已完成 | `internal/model/validate/ir.go` 的 `feasibility`（distributions 之後、constraints 之前）：有 breakdowns 的 resource 的 request units≠1（`/nodes/<i>/requires/<k>/units`），一個 bundle 兩個以上有 breakdowns 的 resource（`/nodes/<i>/requires`，message 依 ID 列出）。`TestBreakdownRules`、`TestR3Message`、`semantic-breakdown-units.json`、`semantic-two-breakdown-resources.json`（`TestAgreesWithSharedFixtures`）、engine 的 `TestCompileRefusesR3`、review 的 R3 candidate（`TestBreakdownRisk`） |
| 1c. B2 `ErrStalled` → `SIMULATION_STALLED`（TTR 的部分） | 已完成；DB 部分 NOT RUN | `kernel.after`／`kernel.stall`：有故障的程式在每個 tick 的最後一個事件之後檢查（D-074 (c)）；`*engine.StalledError`（Tick、Entity、Node、Since、Deadline、`Reason()`）。Runner：不重試的 `SIMULATION_STALLED`（`internal/storage/runs/failures.go`、`internal/runs/runner/runner.go`，`TestStalledFailure`）；API：`MODEL_VALIDATION_FAILED`、severity error（`internal/runs/runs.go`，`TestStalledError` 依新的 `testdata/contracts/valid-run-failed-stalled.json`，message 為 G-S1 手推的 reason）。Golden G-S1（繞過 R3：在 30 分鐘 tick 結束時 stall，since 20）、G-S2（deadline tick 中修復稍後才到，不報）、`TestStallNamesTheEarliestRequest`、exponential TTR 不檢查 |
| 1d. D-063 (c) 的 365 天 code | 已完成 | limits 階段的 `durations`：lognormal `mean`、Weibull `scale` 與 resource 中所有分布參數換算超過 365 天為 `LIMIT_EXCEEDED`（`TestLognormalWeibullRules`、`TestResourceDistributions`）；engine 的 `newSampler` 也拒絕（`TestSamplerRejects`） |
| 1e. lognormal／Weibull 與參數範圍 | 已完成 | `internal/simulation/engine/sample.go`：lognormal x = exp(μ＋σ·Φ⁻¹(U))、Weibull x = scale·exp(ln(−ln(1−U))/shape)，pinned `exp` 與 AS241 `ndtri`，樣本至少 1 tick；validator 與 Compile 都檢查 0 < cv ≤ 10、0.1 ≤ shape ≤ 20、mean／scale 下溢（D-075，PROPOSED）。`TestExpProfile`、`TestNdtriProfile`、`TestLognormalParameters`、`TestSamplerVectors`（10 個新 case）、`TestLognormalWeibullStatistics`（KS、平均、變異數）、`TestLognormalWeibullBounds`、`TestLognormalWeibullRules`、`TestLognormalWeibullUnderflow`、`semantic-lognormal-cv-range.json` |
| 1f. resource 分布的 limits、binding、manifest、D-033 | 已完成 | `model.Resource.Distributions()`；validator 的 limits、distributions、`Bindings`（empirical TTF 有 0 tick 的樣本為 `DISTRIBUTION_INVALID`）；`internal/storage/datasets` 的 `shape()`（resource 的 empirical 也要 binding）；engine 的 `checkManifest`、`resolveDatasets`、`build`（`located`／`distributions()`）。`TestBindingsOfBreakdowns`、`TestBreakdownDatasets`、`TestCompileBreakdownDatasets` |
| 1g. TTF 可能為 0 tick | 已完成 | constant 0、triangular min 0 → `DISTRIBUTION_INVALID`（`/resources/<i>/breakdowns/time_to_failure/distribution`）；TTR 可以為 0。`TestBreakdownRules`、`semantic-breakdown-zero-time-to-failure.json` |
| 2a. Result 的停機組與 available 組 | 已完成 | `internal/results/metrics/aggregate.go`：∫D、[W, H] 內開始的停機、完整修復（開始 ≥ W、修復 ≤ H，長度 0 也算）、available＝C·T − down、idle＝available − in_use；沒有有限 buffer 時只列有故障的 resource（D-074 (e)）。Availability 的比例（`available_ticks`／(C·T)、站別 availability）依 D-058 由 domain pack 從這些整數成員計算（`metrics.domain`，T-051），engine 不另算比例。`TestBreakdownGoldenResults`（11 個 case 的 `resource_states`、utilization、lead time、queue wait、blocking、buffers 逐值）、`TestBreakdownStateIdentities`（四個隨機 reference case 的 30 replications：A＝C·T − D、idle＝A − U、processing＝U − K）、`TestNeutralBreakdownsKeepTheResult` |
| 2b. 事件順序與 tie-break 的 golden | 已完成 | `testdata/simulation/breakdown-goldens.json`（HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN；由 `checks/evidence/2026-10-25-T-048/breakdown_goldens_gen.py` 組成 JSON，期望值都依各 case 的 `derivation` 手推輸入，沒有任何程式產生期望值）：G-F1（閒置時故障）、G-F1W（同一個 run 在 warmup 12 分之後的視窗，停機成員的視窗計數）、G-F2（服務中故障等到釋放）、G-F3（兩個 unit、D-074 (a)）、G-F7、G-F8（bundle 中只有一個會故障）、G-T1（同 tick 釋放與故障）、G-T2（同 tick arrival 與修復）、G-T4（TTR 0 的修復在 dispatch 之後、同 tick 再 dispatch；tick 8 的 observation 順序逐筆）、G-K1、G-D4、G-S1、G-S2；priority 15 的事件順序向量（`TestEventOrderWithResourceState`） |
| 3. 沒有故障的模型與 des-go-0.2.0 逐 byte 相同 | 已完成 | `frozen020_test.go`（21423aa 的 kernel、replication、events，以 go/ast 改名凍結，ffc141c）；`TestDifferentialAgainstFrozen020`：50,000 個隨機模型（一半加有限 buffer），observation（含 seq）逐筆相同；`TestNeutralBreakdownsChangeNothing`：constant TTF 晚於 horizon 的故障與 des-go-0.2.0 逐筆相同；des-go-0.1.0、0.2.0 的鎖定 digest 把 engine_version 換回後相等（`TestGoldenDigests`、`TestGoldenDigests020`），`kernel-observation-digests*.json`（0.1.0、0.2.0）照舊通過；`des.Version = "des-go-0.3.0"`，另立 `engine-golden-digests-0.3.0.json` 與 `kernel-observation-digests-0.3.0.json`（engine 輸出的 regression lock，在手推期望通過後才取，不是 oracle） |
| 4. 修復喚醒、B1 的 release-bound／capacity-bound | 已完成 | `kernel.repair` 喚醒該 resource FIFO 最前面的 held candidate 並排 dispatch；`kernel.deadlock` 以全部 capacity C 判斷 release-bound（註解說明）。先寫的測試：G-K1（`TestCapacityBoundIsNoDeadlock`：tick 2 時 held、沒有 unit 在使用中、unit 停機，B1 不得回報；修復在 11 讓它進入）；mutant `deadlock-bound-ge`（`>`→`>=`）被 `TestBreakdownGoldens`（G-K1）擋下，T-047 的 equivalent mutant `deadlock-ignores-fitting-held` 現在也被擋下（`deadlock-ignores-capacity-bound`）；`repair-wakes-no-held`、`repair-wakes-without-dispatch` 被擋下 |
| 5a. 只解除三個 primitive | 已完成 | `validate.checked = {FiniteBuffer, Breakdowns, Lognormal, Weibull}`、`engine.kernelExecutes` 相同；`TestPrimitiveCatalog`、`TestPrimitiveGate`（valid-line-flow-ir.json 剩 14 個 PRIMITIVE_NOT_SUPPORTED）、`TestKernelRefusesWhatItDoesNotExecute`、`TestCompileRefusesLineFlowPrimitives`；cases.json 的 `primitives` 在 T-046 phase 2 已寫好；`semantic-stall-risk.json` 也用 calendars，仍回 PRIMITIVE_NOT_SUPPORTED |
| 5b. D-057 server risk | 已完成 | `internal/ai/review/risk.go`：同一個 resource 的 breakdowns 新增或刪除（edit），或 TTF／TTR 分布依值比較有任何不同（任一方向），至少 medium；arrival／service／delay 改為 lognormal、Weibull 仍為 low；`values()` 加入 resource 分布與 cv、shape、scale（expected effects 可以引用）。`TestBreakdownRisk`、`TestBreakdownValuesInEffects`、`TestLiftedPrimitivesHaveRiskCases`；scenario 新增 breakdowns 為 `PATCH_PATH_FORBIDDEN`（allowlist 在 T-046 phase 2 已是 replace only） |
| 5c. AI context | 已完成 | `aicontext.modeRules` 的 SCENARIO_PATCH_ALLOWLIST 與 `prompts/scenario/system.md`（scenario-0.3.0 → 0.4.0）：已有 breakdowns 的 resource 可 replace TTF／TTR 分布、不增刪 breakdowns、至少 medium；可改的分布都可為 lognormal（0 < cv ≤ 10）或 Weibull（0.1 ≤ shape ≤ 20）；不提 calendar、setups、batch、shift（gate 仍在）。`TestScenarioMode` |
| 5d. MOD-038 | 已完成 | 重新判定（T-046 review 第 1 輪 I2）：lognormal 自 des-go-0.3.0 起在 P0 執行，「把 review 改用 lognormal」不再是不支援的 P1 要求；intent 沒有給 mean 與 cv，所以 `category` 改為 `missing_data`、`expected_outcome` 改為 `needs_input`。Unsafe 回答：帶 `sd` 的 lognormal 仍為 `IR_SCHEMA_INVALID`，原本的「合法 lognormal → PRIMITIVE_NOT_SUPPORTED」改為 cv 1000 → `DISTRIBUTION_INVALID`；合法但捏造 cv 的回答在 fake suite 中無法評分（要看真實模型的回答，T-038），寫在 `internal/ai/corpus/review_test.go` 的註解。`make ai-eval` 重產 `checks/ai-evals/fake-review.json`（`fake-provider.json` 沒有變） |
| 6. SimPy reference | 已完成 | 見「SimPy reference」；接受與拒絕的條件寫在 `scripts/simpy_reference.py` 的 docstring 與 SIMULATION_SPEC §10 |
| 不碰其他任務 | 符合 | 沒有 migration；沒有改 `internal/import`、`internal/proposals`、calendars／batches／setups 的規則或 kernel；`internal/model/patch` 沒有改（D-057 的 allowlist 在 T-046 phase 2 已完成） |

## 實際執行結果

在 worktree 中執行（`environment.txt`：Go 1.27.3、Python 3.13.16、SimPy 4.1.2、Node 22.22.0、4 CPU，與其他 agent session 共用；沒有 PostgreSQL）。Go 測試都以 `-count=1` 執行，不用 cache。Exit code 在 `exit-codes.txt`。下表的 `make` 指令都在最後的 tree（0c8b1f0 加上本報告與 evidence，Go 與測試資料和最後一個 commit 相同）上重跑；加入 G-F1W 之前的一次完整執行也都是 exit 0，log 保留為 `*-pre-gf1w.log`。

| 指令 | Exit | 說明 |
|---|---:|---|
| `make backend-test`（開始前，21423aa） | 0 | `baseline-backend-test.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `spec-check.log`（本報告寫好之後重跑）；`PASS_TS_CONTRACTS_FRESH`（15 檔、106 types，schema 沒有改）、`PASS_INDEPENDENT_HASHES`（28）、`PASS_DB_TESTS_FRESH`（12）。重產的 `contracts-check.json`（新 fixture `valid-run-failed-stalled`）、`decisions-check.json`（proposed 加 D-074、D-075；T-048 在 `next_after_review`，等 D-074、D-075）、`planning-check.json`、`docs-check.json` 與本報告一起 commit |
| `make backend-test GOFLAGS=-count=1` | 0 | `backend-test.log`：gofmt、go vet、61 個 package ok，沒有 `(cached)`；`GEMINUS_FULL_DIFFERENTIAL=1`（兩個 differential 都跑全部 50,000 個模型）；DB-backed 測試 skip（NOT RUN）。engine 102 s、des 9 s、check-architecture 132 s |
| `make backend-race GOFLAGS=-count=1` | 0 | `backend-race.log`：61 個 package ok，沒有 `(cached)`；engine 298 s（differential 在 race 下各跑前 5,000 個）、des 72 s、check-architecture 183 s |
| `make architecture-check` | 0 | `architecture-check.log`：74 packages、16 rules、16 build configurations |
| `make ai-eval` | 0 | `ai-eval.log`：`TestFakeProviderEval`、`TestReviewEval` PASS；第一次執行（`ai-eval-pre-gf1w.log`）重產的 `checks/ai-evals/fake-review.json`：missing_data 3 → 4、unsupported_p1 2 → 1（MOD-038），已在 e70c855；重跑沒有再改變；`fake-provider.json` 沒有變；live provider NOT RUN |
| `make simulation-reference-test PYTHON=…`（第一次） | 2 | `simpy-first-run.log`：83 個隨機比對都通過，G-F7 的「等待中的故障」多了 23、35 分兩筆等待 0 分鐘的紀錄（見「SimPy reference」）；腳本改為只記正的等待，手推 golden 沒有改 |
| `make simulation-reference-test PYTHON=…` | 0 | `simulation-reference-test.log`、`simpy-reference-report.json`：確定性 G-F3、G-F7（與 T-047 的 11 個）逐值相同，83 個隨機比對（T-047 的 45 個、B-S1–B-S4 的 38 個）都在容差內，G-F8、G-B13 被拒絕 |
| `make backend-numeric`（第一次） | 2 | `backend-numeric-red.log`：arm64、riscv64、loong64 把 `ln(mean)-s2/2` 的 s2·0.5 融合進減法（FMSUBD／FNMSUBD，sample.go:136）；改為 `ln(mean)-float64(s2/2)` |
| `make backend-numeric` | 0 | `backend-numeric.log`：六個 target 沒有 FMA 與平台 math call（每個 listing 都要有 `engine.ln`、`engine.exp`、`engine.ndtri`），GOAMD64=v3 的數值測試通過 |
| Mutation harness（第 1 次） | 1 | `mutants-run1.log`：59 個 mutant 中 57 個被擋下、1 個 equivalent、1 個 BUILD-FAILED（mutant 本身寫錯：拿掉呼叫後變數未使用）；修正該 mutant、改正一個名稱並新增一個後重跑三個：都被擋下 |
| Mutation harness（第 2 次，加入 G-F1W 之前的 tree） | 0 | `mutants-run2.log`：60 個 mutant 中 59 個被擋下、1 個 equivalent（見「Mutation」） |
| Mutation harness（G-F1W 之後，兩個視窗的 mutant） | 0 | `mutants-gf1w.log`：2 個都被 `TestBreakdownGoldenResults` 擋下 |
| `go test -v`（重點測試，加入 G-F1W 之前） | 0 | `engine-tests-verbose.log`：differential、中性故障、隨機檢查器的計數 |
| Fuzz（加入 G-F1W 之前） | 0 | `fuzz-breakdowns.log`、`fuzz-kernel.log`、`fuzz-run.log`（見「Fuzz」） |

## 測試涵蓋

| 層 | 測試 | 內容 |
|---|---|---|
| Validator | `internal/model/validate/breakdowns_test.go`：`TestBreakdownRules`、`TestR3Message`、`TestLognormalWeibullRules`、`TestLognormalWeibullUnderflow`、`TestBindingsOfBreakdowns`、`TestResourceDistributions`；`primitives_test.go` 的 `liftedPrimitives`、`TestPrimitiveGate` | 正反例的 code 與 path：R3 兩種形狀、TTF 的 constant 0／triangular min 0、cv（0、10、10.01）、shape（0.09、0.1、20、20.01）、mean／scale 1e-400 下溢、365 天的 LIMIT_EXCEEDED、resource 的 empirical binding 與 0 tick 樣本；gate 只對三個 primitive 解除 |
| Dataset binding | `internal/storage/datasets/breakdowns_test.go`：`TestBreakdownDatasets` | resource 的 empirical 要 binding（path 在 `/ir/resources/...`），TTF 的 0 tick 樣本為 DISTRIBUTION_INVALID |
| Kernel：golden | `internal/simulation/engine/breakdown_test.go`：`TestBreakdownGoldens`（11 個 case：service 開始、完成、每段阻塞、每段停機、每個等待中的故障）、`TestZeroTickRepairDispatchesAgain`（G-T4 的 tick 8 逐筆 observation 與兩個 dispatch）、`TestCapacityBoundIsNoDeadlock`（G-K1）、`TestDeadlockBesideBreakdowns`（G-D4）、`TestStallIsATypedError`（G-S1，與 exponential TTR 不檢查）、`TestStallNamesTheEarliestRequest`、`TestNoStallBeforeTheTickEnds`（G-S2）、`TestBreakdownStreams`、`TestNeutralBreakdownsChangeNothing`、`TestCompileRefusesR3`、`TestCompileBreakdownDatasets`、`TestRepairBound`、`TestBreakdownObservationDigests` | 手推的期望（breakdown-goldens.json）；0.3.0 的 observation 鎖（含 At） |
| Kernel：只看 observation 的檢查器 | `kernel_check_breakdown_test.go`：`TestRandomBreakdownModelsAgainstSpec`、`TestRandomStallsAreInTheState`、`FuzzBreakdowns`；`kernel_check_blocking_test.go` 的 bchecker 延伸 | 從 IR 與 observation 重建 in_use、D、等待中的故障與 constant 時鐘的到期時點，檢查：有閒置 unit 才立即停機、否則等待；釋放的 unit 先依序給等待中的故障；停機恰為 TTR、修復後 TTF 再故障；U＋D ≤ C；dispatch 是以 C − D 的 literal fixpoint；沒有 tick 結束時還有可授予的 request（修復不會讓放得下的 request 一直等）；valid 的模型不會 ErrDeadlock／ErrStalled；R3 被繞過時每個 ErrStalled 都在 B2 的狀態、在它的 tick 結束時 |
| Sampler 與 numerical profile | `sample_t048_test.go`：`TestExpProfile`、`TestExpAccuracy`、`TestNdtriProfile`、`TestLognormalParameters`、`TestBreakdownStreamVectors`、`TestEventOrderWithResourceState`、`TestLognormalWeibullStatistics`、`TestLognormalWeibullBounds`；`sample_test.go` 的 `TestSamplerVectors`（新增 10 個 lognormal／Weibull case）、`TestSamplerRejects` | 對 Python port 逐 bit（常數 bit pattern、邊界、10⁶ 個輸入的 SHA-256）；exp 與 256-bit 參考值的誤差 < 1 ulp；KS ≤ 1.95/√n 與 6 個標準誤內的平均、變異數；範圍外拒絕 |
| Result | `internal/simulation/des/breakdown_test.go`：`TestBreakdownGoldenResults`、`TestBreakdownStateIdentities`、`TestNeutralBreakdownsKeepTheResult`、`TestBreakdownDeterminism`、`TestBreakdownsAreNotTraced`、`TestGoldenDigests030`；`des_test.go` 的 `TestGoldenDigests`、`TestGoldenDigests020`、`FuzzRun` 的故障 seed；`reference_test.go` | 每個 Result 依 schema 驗證；worker 數 1、3、8、0 的 digest 與 trace 相同（TEST-019），另一個 seed 不同 |
| Runner／API | `TestStalledFailure`、`TestStalledError`、`TestRunError` | 不重試、reason、`MODEL_VALIDATION_FAILED`、fixture 的 engine_version 等於 `des.Version` |
| AI | `TestBreakdownRisk`、`TestBreakdownValuesInEffects`、`TestLiftedPrimitivesHaveRiskCases`、`review_test.go` 的 cv 11、`TestScenarioMode`、corpus 的 MOD-038 | 見 scope 5b–5d |

### 先紅後綠

測試與 fixture 先寫，紅的輸出保留在 evidence：

- `red-validate.log`：validator 的新測試在 gate 解除與規則實作之前失敗 52 處（例如 R3 的 case 回 `PRIMITIVE_NOT_SUPPORTED`）；`red-ai.log`：`TestBreakdownRisk`、`TestBreakdownValuesInEffects`、`TestLiftedPrimitivesHaveRiskCases`、`TestRefusesUnsafeChanges`、`TestScenarioMode`、`TestReviewEval` 失敗；`red-engine-vet.log`：engine 的新測試在 kernel 實作前無法編譯（`ObsResourceDown`、`HasBreakdowns` 等尚不存在）。Engine 的行為層面的「紅」由 mutation 證明：每條 kernel 規則被拿掉或改錯時，都有測試失敗（「Mutation」）。
- 實作後全綠（`backend-test.log`、`backend-race.log`）。期間有兩個由測試發現、在實作而不是期望上修正的問題：`make backend-numeric` 抓到 `ln(mean)-s2/2` 的 FMA 融合（`backend-numeric-red.log`）；SimPy 的第一次比對見下。
- 一個測試本身寫錯：`TestExpAccuracy` 原本以 `math.Exp` 為參考、要求相差 ≤ 1 ulp，在 exp(−493.8360700786439) 失敗（`exp-accuracy-red.log`）。以 256-bit 的值檢查：真值的正確捨入為 3.386256097486197e-215，`engine.exp` 高 1 ulp、`math.Exp`（amd64 assembly）低 1 ulp，兩者與真值的誤差都小於 1 ulp、彼此差 2 ulp。改為與 256-bit 參考值比較、要求誤差 < 1 ulp（300,000 個輸入，race 下 30,000 個）；這是更準確的性質，不是放寬（舊的斷言把一個本身有誤差的 assembly 當成真值），記在此供 reviewer 判斷。
- `TestBreakdownRisk` 的「the same breakdowns」原本是一個完全不改任何值的 patch，review 依既有規則以 no-op 拒絕（`AI_OUTPUT_INVALID`）；改為「同一個修復時間重寫一次、另外提高 capacity」，仍然檢查依值比較、寫回相同的 breakdowns 不算變更（low）。

## Differential（D-047）

- **對凍結的 des-go-0.2.0**（`frozen020_test.go`，ffc141c；21423aa 的 kernel.go、replication.go、events.go 以 go/ast 改名，共用 Program）：`TestDifferentialAgainstFrozen020`，50,000 個隨機模型（PCG(seed, 4801)，奇數 seed 加 1–3 的有限 buffer、去掉 R1 拒絕的），observation（kind、tick、seq、entity、node、resource、value、at）與 outcome、error 逐筆相同：23,746 個 compile 成功、5,516 個有有限 buffer、11,698,033 筆 observation（47,742 筆 Blocked）；ending 的下限（none ≥ 1,000、visits ≥ 100、active ≥ 100、blocks ≥ 1,000）都達到；golden model 1、14、60 天與所有 blocking golden 也比對。`make backend-test` 以 `GEMINUS_FULL_DIFFERENTIAL=1` 要求全部執行，race 下前 5,000 個。
- **對凍結的 des-go-0.1.0**（T-047 的 `TestDifferentialAgainstFrozenKernel`）照舊通過：36,643 個 compile 成功、17,427,661 筆 observation 相同。
- **中性故障**（`TestNeutralBreakdownsChangeNothing`）：隨機模型（一半有有限 buffer）的部分 resource 加上 TTF 為晚於 horizon 的 constant 的故障（繞過 validator，因為 R3 可能拒絕 2-unit 的 request），新 kernel 的 observation 與 des-go-0.2.0 跑原模型逐筆相同，沒有任何 Down、Up、Pending：2,158 個模型、4,770 個有故障的 resource。Result 層（`TestNeutralBreakdownsKeepTheResult`）：既有成員與 trace 相同，只多出有故障的 resource 的 `resource_states`（停機全為 0）。
- **鎖定的 digest**：`engine-golden-digests.json`（0.1.0）與 `engine-golden-digests-0.2.0.json` 不重產；把 Result 的 engine_version 換回各自的版本後重算的 digest 等於鎖定值（`TestGoldenDigests`、`TestGoldenDigests020`）；`kernel-observation-digests.json` 與 `-0.2.0.json` 照舊通過。新版另立 `engine-golden-digests-0.3.0.json`（GOLDEN-001／002、15 個 blocking golden、11 個 breakdown golden、B-S1–B-S4）與 `kernel-observation-digests-0.3.0.json`（11 個 golden 與 4 個 reference case，含 At）：engine 輸出的 regression lock，在手推期望通過之後才取。

## SimPy reference（`make simulation-reference-test`）

`scripts/simpy_reference.py`（SimPy 4.1.2，沒有新 dependency）以 SimPy 自己的 primitive 解讀故障：有故障的 resource 是 `simpy.PriorityResource`，job 以 priority 0 request；每個 unit 一個故障時鐘 process：TTF 結束時有閒置 unit（`len(users) < capacity`）就以 priority −1 request（SimPy 立即授予，腳本檢查確實立即授予），否則故障依序等待下一個 job 釋放的 unit，job 把 request 交給故障 process（finish-first）；修復把 unit 釋放回 SimPy 的 queue，所以拿到它的是等待中的 job（D-074 (a)）。抽樣用 Python 的 `random`（不用 engine 的 stream 或 sampler）。

- **接受的模型**（執行前檢查，其餘一律拒絕）：T-047 的條件（每個需求 1 unit；每個 bundle 至多一個共用 resource；自己的 resource 的 capacity 不小於共用的）；有故障的 resource 必須是每個需要它的 bundle 中唯一的 resource；分布只能是 constant、exponential、triangular、lognormal、Weibull。拒絕：G-F8（bundle 有會故障的 m 與 o）、G-B13。
- **確定性**：G-F3（兩個 unit、D-074 (a)）、G-F7 逐值比對 service 開始、完成、每段停機、等待中的故障與 Result 成員（含 `resource_states` 的故障組），與 T-047 的 11 個 case 一起通過。G-F1、G-F1W、G-F2、G-T1、G-T2、G-T4（D-048 的同 tick 順序，SimPy 依排程先後）、G-K1、G-S2（kernel 的 B1／B2）、G-D4、G-S1（SimPy 不偵測 deadlock 與 stall）不比，原因寫在 fixture 的 `simpy` 欄位。
- **第一次執行的差異**（`simpy-first-run.log`）：G-F7 的停機完全相同，但腳本在 23、35 分多記了兩個「等待中的故障」：SimPy 在同一時刻先處理故障再處理釋放（排程先後），故障「等待」0 分鐘後拿到被釋放的 unit；kernel 先釋放（priority 0 < 15），故障看到閒置的 unit。兩者都在同一時刻停機，差別只在紀錄。腳本改為只把正的等待記為等待中的故障，並要求被比對的 case 中每個手推的等待中的故障都有正的等待（否則該 case 不可比對而失敗）；手推的 golden 沒有改。
- **隨機**：B-S1（exponential 的一切）、B-S2（三個 unit、Weibull TTF、lognormal TTR 與 service）、B-S3（有限 buffer 前的故障：被擋的 entity 持有 unit 時故障要等）、B-S4（兩個 process 共用的故障 resource、lognormal TTF、Weibull TTR 與 service、triangular service）；30 replications、warmup 300、horizon 3,000 分鐘；38 個指標（completed、lead time、utilization、停機比例、available 與 idle 比例、開始的停機數、有限 buffer 的成員）全部在 fixture 預先固定的容差內（|Δ| ≤ 4·√(s²_go/n＋s²_simpy/n)＋0.5%·max(|平均|)＋1e-9，沿用 T-047 的容差，沒有看結果再調）。
- **腳本自己的 mutation**：`simpy-repair-satisfies-waiting-failure`（修復也滿足等待中的故障）、`simpy-no-finish-first`（釋放不交給等待中的故障）都讓 G-F3 失敗；`simpy-failure-preempts-nothing-waits` 被腳本自己的檢查擋下（忙碌時的 request 沒有立即授予）。

## Mutation

`checks/evidence/2026-10-25-T-048/t048-mutants.py`：每個 Go mutant 是一個精確的 search/replace，以 `go test -overlay` 套用（working tree 不變），列出的 package 有測試失敗即為 KILLED；Python mutant 套用在 `scripts/simpy_reference.py` 的暫存副本上，對 Go 的 reference 結果執行，exit 非 0 即為 KILLED；pattern 不是恰好出現一次即為 BAD-MUTANT；標 equivalent 的必須存活。第 2 次（加入 G-F1W 之前的 tree，`mutants-run2.log`）：**60 個中 59 個被擋下、1 個 equivalent**，exit 0。

| 類別 | Mutant（擋下它的測試） |
|---|---|
| B1（scope 4） | `deadlock-bound-ge`：`>`→`>=`（`TestBreakdownGoldens`／G-K1）；`deadlock-ignores-capacity-bound`：T-047 的 equivalent mutant，現在被擋下（G-K1）；`deadlock-bound-uses-up-units`：以 C − D 判斷（G-K1） |
| Dispatch 與故障 | `dispatch-ignores-down`、`failure-preempts`（不看 in_use）、`failure-never-waits`、`release-ignores-waiting-failures`（G-F2 等）；`waiting-failures-lifo`（`TestRandomBreakdownModelsAgainstSpec`）；`release-satisfies-one-failure`（只有 R3 被繞過時才有差別，`TestRandomStallsAreInTheState`）；`release-wakes-no-held`（`TestBlockingGoldenResults`；第 1 次的名稱是 `release-before-failures-observed-after-wake`，內容相同） |
| 修復（scope 4） | `repair-wakes-no-held`、`repair-wakes-without-dispatch`、`repair-satisfies-waiting-failure`（D-074 (a)，G-F3）、`repair-draws-no-next-failure` |
| Stream 與順序 | `clocks-share-one-stream`、`ttf-and-ttr-one-stream`（`TestBreakdownStreams`）；`clocks-start-before-sources`、`failures-at-dispatch-priority`（`TestBreakdownObservationDigests`）；`clock-beyond-horizon-takes-a-seq`（`TestNeutralBreakdownsChangeNothing`） |
| B2 | `no-stall-check`、`stall-deadline-strict`（`>=`→`>`）、`stall-unbounded-checked`、`stall-since-reset-by-a-wake`（`TestStallIsATypedError`）；`stall-check-after-every-event`（G-S2）；`stall-ignores-units-in-use`；`stall-reports-the-latest`（`TestStallNamesTheEarliestRequest`）；`bundle-ttrmax-ignores-unbounded` |
| Compile 與 sampler | `bound-of-triangular-is-mode`（`TestRepairBound`）、`no-breakdowns-flag`、`kernel-does-not-execute-breakdowns`、`kernel-does-not-execute-weibull`、`lognormal-mu-without-correction`、`weibull-sample-without-shape` |
| Validator | `r3-off`、`r3-allows-two-units`、`r3-allows-two-resources`、`cv-range-100`、`shape-min-0.01`、`ttf-may-be-zero`、`resource-limits-unchecked`（第 1 次 BUILD-FAILED：mutant 拿掉呼叫後變數未使用，改為 `_ = d` 後被擋下）、`breakdowns-unchecked` |
| Result | `down-not-integrated`、`zero-repairs-not-counted`（D-074 (d)）、`available-ignores-down`、`every-resource-listed`（G-F8）；`downs-started-ignore-window`、`repairs-ignore-window`（第 2 次由 `TestGoldenDigests030`、`TestBreakdownStateIdentities` 擋下；之後新增手推的 G-F1W，兩者改由 `TestBreakdownGoldenResults` 擋下，`mutants-gf1w.log`） |
| Version、runner、API | `old-engine-version`、`stall-retried`、`stall-details-dropped`、`stall-internal-error` |
| Risk 與 effects | `risk-breakdowns-off`、`risk-breakdowns-added-low`、`risk-ignores-cv`、`risk-ignores-repair`、`effects-without-resource-values` |
| SimPy reference | `simpy-repair-satisfies-waiting-failure`、`simpy-no-finish-first`（G-F3 不符）；`simpy-failure-preempts-nothing-waits`（腳本自己的「閒置 unit 要立即授予」檢查） |
| Equivalent | `trace-sees-breakdowns`：trace 只記被選中且 active 的 entity，故障的 observation 的 Entity 是 0，不是任何 entity handle，所以拿掉明確的略過也一樣；明確的略過只是說明 |

第 2 次執行時 `downs-started-ignore-window` 只被 regression lock 擋下：當時的手推 golden 的 warmup 都是 0。為了不讓視窗的計數只靠 engine 自己的輸出，新增手推的 **G-F1W**（G-F1 在 [12, 30] 的視窗：開始的停機 1、完整修復 1（3 分）、down 4 分、available 14 分、idle 4 分、utilization 10/18、lead time 與 queue wait 的 cohort，推導在 fixture 中），手推期望先通過，之後才把它加入兩個 0.3.0 的鎖（它的 run 與 G-F1 相同，只有視窗不同）；兩個視窗的 mutant 重跑後由 `TestBreakdownGoldenResults` 擋下（`mutants-gf1w.log`）。

## Fuzz

| Fuzzer | 時間 | Execs | 結果 |
|---|---:|---:|---|
| `FuzzBreakdowns`（新；隨機模型的 seed、有限 buffer、constant 時鐘、繞過 R3：每個 run 對只看 observation 的檢查器，ErrStalled 只在繞過時、且在 B2 的狀態） | 120 s | 145,146 | PASS（`fuzz-breakdowns.log`） |
| `FuzzKernel`（加 breakdown-goldens.json 的模型為 seed） | 60 s | 128,626 | PASS（`fuzz-kernel.log`） |
| `FuzzRun`（加 breakdown-goldens.json 的模型為 seed：typed failure 或 schema-valid、digest 可重算的 Result 與 trace） | 60 s | 489,205 | PASS（`fuzz-run.log`） |

Exec 數包括很快就 decode 或 compile 失敗的輸入（`FuzzKernel`、`FuzzRun` 的輸入是任意 bytes），不等於跑完的模型數。新的 interesting input 只存在 Go 的 fuzz cache，沒有失敗的 input，repository 中沒有新的 `testdata/fuzz` 檔案。

## NOT RUN

- **所有 DB 測試，交給 coordinator 在一次性 DB 上執行**：`make integration-test`（`make db-test` 與 `make db-go-test`）。這裡沒有 PostgreSQL，依指示沒有跑、也沒有嘗試以其他方式取得 DB。本任務沒有 migration，`db/tests/*.sql` 沒有改（`PASS_DB_TESTS_FRESH`）。engine_version（des-go-0.3.0）與 `SIMULATION_STALLED` 的對應會經過 DB，下列 DB-backed Go 測試在本 worktree 中都是 skip（`db-tests-skipped.log` 列出全部 212 個 skip）：
  - `internal/runs/runner`：`TestEventBudgetAcrossRetries`、`TestWallClockAcrossRetries`、`TestACrashedAttemptIsRunAgain`、`TestNoResultAfterALostLease`、`TestALateCancellationWins`、`TestRefusedAttempts`（engine_version 不符）、`TestOneJobPerRun`、`TestACancellationOutlivesACrash`、`TestTheWallClockEndsAnAttempt` 等；
  - `internal/api` 的 run 測試：`TestRunClosedLoop`、`TestRunRoleMatrix`、`TestRunRequestRefusals`、`TestRunIdempotency`、`TestCancelRunning`、`TestRunDatasets`、`TestRunDeterminism`、`TestScenarioRun`、`TestRunFailures`、`TestStreamFollowsTheRun` 等；
  - `cmd/api`、`cmd/worker`、`cmd/provision-tenant`、`internal/worker`、`internal/models`、`internal/proposals/...`、`internal/storage/...`、`internal/tenant/...` 的 DB-backed 測試與整個 `make integration-test`。
- **沒有端對端的 `SIMULATION_STALLED` 測試**：valid 的模型因 R3 不會 stall，經 API 建立的 run 走不到 B2；runner 與 API 的對應只有 unit test（`TestStalledFailure`、`TestStalledError`），failure 寫入 `jobs.failure`、run 轉 failed、`getSimulation` 顯示 envelope 的 DB 路徑沒有以 stall 執行過（與 `SIMULATION_DEADLOCK`、其他 execution limit 走同一條路徑）。
- **故障模型經 API 的 run**：有 breakdowns、lognormal 或 Weibull 的模型經 publish → scenario → run → result 的 closed loop 只在 engine 與 des 層測過，DB／API 層（`TestRunClosedLoop` 等）在本 worktree skip。
- `make frontend-test`：本任務沒有改 schema、產生的 TS 或 `apps/web`（`PASS_TS_CONTRACTS_FRESH`），依指示「TS 有改才跑」沒有執行；Node 22.22.0 也不符 `apps/web` 要求的 >=24.21.0（T-047 已記錄）。UI 如何顯示停機成員屬 T-032／T-034。
- `make e2e-test`：沒有 UI 變更，未執行。`make benchmark-test`：仍是 NOT IMPLEMENTED 的 placeholder；故障事件、B2 每個 tick 的檢查與 lognormal／Weibull 抽樣的成本沒有效能量測。
- Live AI 與 `make ai-eval-live`：live AI 未核准（H-001、H-017，T-053 之前關閉）；MOD-038 的真實模型回答（是否捏造 cv）沒有評分（T-038）。
- `make backend-race` 只在 linux/amd64 執行；`make backend-numeric` 只檢查其他 GOARCH 的組語，沒有在實體 arm64 等機器上執行數值測試，跨架構的 byte-identical digest 仍不宣稱（SIMULATION_SPEC §4）。
- CI 上沒有執行（只在本機）。
- Mutation 的第 1 次執行中三個 mutant（改名、修正、新增）的單獨重跑只在終端機看到結果（都被擋下），沒有存成 log；第 2 次完整執行（`mutants-run2.log`）涵蓋它們。

## 需要 owner 決定

兩項都是 **PROPOSED**（docs/DECISIONS_AND_TBD.md 的「T-048 的提案」與 planning/decisions.json；`approved_by`／`approved_at` 為 null）。建議都已實作，可以回退；不採用時要改的地方寫在各項最後。T-048 以它們為 gate，所以 `make spec-decisions` 把 T-048 列在 `next_after_review`、等 D-074、D-075。

- **D-074 — resource 故障的 kernel 解讀**。建議：(a) 等待中的故障只由 job 釋放的 unit 滿足，修復好的 unit 回到可用（A；B 為修復也滿足，G-F3 的 s5 會等到 33）；(b) 釋放時先回報 in_use、再依 FIFO 給等待中的故障、最後喚醒 held front（照列）；(c) B2 在 tick 結束時檢查、since 為第一次在 tick 結束時處於狀態的 tick、TTRmax 為上界的最大值（無上界不檢查）、回報 queue 順序最早的 request（A；B 每個事件後檢查會在 G-S2 誤報，C 只在 dispatch 後檢查會晚報）；(d) TTR 0 的停機算一次開始與一次長度 0 的完整修復（A；B 不計）；(e) 完整修復＝開始 ≥ W 且修復 ≤ H、available＝C·T − down、idle＝available − in_use、沒有有限 buffer 時只列有故障的 resource（A）；(f) observation、stream、t=0 順序、中性設定、REPAIR 只在喚醒時排 dispatch、B1 以全部 capacity 判斷（照 D-048、D-050、D-051 的文字，列出供確認）。
- **D-075 — lognormal／Weibull 的範圍與 numerical profile**。建議：(a) 保留候選範圍 0 < cv ≤ 10、0.1 ≤ shape ≤ 20（A；附溢位分析；B 放寬、C 收緊）；(b) pinned exp 與 AS241、σ²／μ 在 compile 時各算一次且乘積先捨入、樣本至少 1 tick（A；B Box–Muller 違反一次 draw，C 平台 math 違反 D-038）。

另外給 owner／reviewer 的說明（不需選擇）：
- `release-satisfies-one-failure`（每次釋放只給一個等待中的故障）只被 R3 被繞過的隨機測試擋下：valid 的模型對會故障的 resource 一次只要 1 unit，所以這個差別在 valid 的模型中不會出現。
- 故障時鐘依 D-051 只在 resource 上班時前進；目前沒有班表（T-049），所以時鐘一直前進。T-049 要把 B2 的 TTRmax 與 R4、時鐘暫停一起處理。
- TTF 的分布可以為 empirical（有 0 tick 樣本時拒絕），但 SimPy reference 不涵蓋 empirical。

## 殘餘風險

- **B2 的保證依賴 R3**：B2 只在 TTRmax 有上界時檢查；exponential、lognormal、Weibull 的 TTR 沒有上界，R3 被繞過時（只在測試中）這樣的 bundle 會停到 horizon 而不報錯，與 D-050「無上界時不檢查」相同。Valid 的模型因 R3 永遠會在 TTRmax 內被授予（`kernel.stall` 的註解有推理，隨機檢查器在 1,126 個 valid 的隨機模型中沒有 stall）。
- **B1 仍是全域的**（D-064 (g)）：故障的 resource 旁有一直在服務的不相關 process 時，局部的循環等待不報（只在 R1 被繞過時可能）。
- **同 tick 的順序只由 kernel 自己的 golden 驗證**：SimPy 依排程先後處理同一時刻的事件，D-048 的 priority 15 與釋放、arrival、dispatch 的同 tick 順序（G-F1、G-F2、G-T1、G-T2、G-T4）只有手推 golden 與只看 observation 的檢查器，沒有獨立的 reference。
- **SimPy 只涵蓋部分模型**：會故障的 resource 必須是 bundle 中唯一的 resource，沒有 empirical；bundle 中另有其他 resource 的情形（G-F8）只由手推 golden 與檢查器驗證。
- **數值**：exp 的 < 1 ulp 是抽測（300,000 個輸入），不是證明；AS241 的精度依原論文（約 1e-16），只以 Python port 逐 bit 與 KS／動差檢查驗證分布，沒有獨立的高精度參考。跨架構只檢查組語，沒有在實體非 amd64 機器上跑。
- **效能**：每個 unit 一個故障時鐘，capacity 很大（例如 10,000）的會故障的 resource 每個 TTF 都有一個事件；B2 在每個有 held candidate 的 tick 結束時逐 resource 檢查 FIFO 前端（O(resources)）。沒有 benchmark（T-037）。
- **Regression lock 不是 oracle**：`engine-golden-digests-0.3.0.json`、`kernel-observation-digests-0.3.0.json` 是 engine 的輸出；語義由手推 golden、檢查器與 SimPy 驗證。
- **中間的 commit 沒有各自驗證**：只有最後的 tree 跑過全部的指令。
