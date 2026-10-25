# T-047 驗證報告：DES 有限緩衝與阻塞（H-016）

**日期：2026-10-20｜狀態：待獨立 review；`planning/tasks.json` 的狀態未改（仍為 not_started），由 reviewer 決定。｜需求：REQ-011、REQ-013、REQ-024｜Gate：D-047、D-048、D-049、D-050、D-057 已 APPROVED（H-018），D-063 已 APPROVED（H-019）；本任務提出的 D-064 是 **PROPOSED**，`approved_by`／`approved_at` 為 null，T-047 依建議實作並以它為 gate。｜驗收測試：TEST-015、TEST-016、TEST-019、TEST-034（第一部分）｜DB 測試與 `make integration-test` 為 NOT RUN（見「NOT RUN」）。**

範圍依 tasks.json：

1. 依 D-049 實作有限緩衝與阻塞（blocking after service）：上游被擋時保留 resource、記阻塞時間、FIFO 解除；與 resource-wise FIFO、all-or-nothing bundle 的交互有明確 golden；
2. deadlock 或循環阻塞依 D-050：validator R1 `BLOCKING_CYCLE`，runtime B1 `ErrDeadlock` → job failure `SIMULATION_DEADLOCK`，不默默跑到 horizon；
3. 固定 golden vectors、與舊 kernel 的 differential（無限緩衝時結果完全相同）、SimPy reference、mutation 與 fuzz；
4. T-046 phase 2 的分工：R1（graph 階段，path 為 cycle 上每個有限 queue 的 `/nodes/<i>/capacity`，`semantic-blocking-cycle-*.json` 回期望的 code 與 path）；B1 的 runner 與 `internal/runs` 對應（`valid-run-failed-deadlock.json`）；Result 的 `blocking`、`buffers`、`resource_states[].blocked_ticks`；trace 的 `blocked`、`unblocked`；只解除 `finite_buffer` 的 gate（`validate.checked`、`engine.kernelExecutes`），升 engine_version（D-047），D-057 的 server risk，更新 AI context（`aicontext.modeRules`、prompts/scenario）。

依 coordinator 的指示：沒有 migration；只用 CB-41；新 decision 只用 D-064–D-066（實際只用到 D-064）；不碰 T-026（`internal/proposals`、`internal/model` 的 approval、0013）與 T-017（`internal/import`、0014）的範圍，`internal/ai/review` 只加 D-057 的 risk。沒有任何 live AI 呼叫，沒有讀取或設定任何 API key。逐條輸出在 `checks/evidence/2026-10-20-T-047/`：`exit-codes.txt`、`environment.txt`、各指令的 log、red log、fuzz log、mutation harness 與結果、differential 與 SimPy reference 的輸出。

## Commit

下表是 `claude/lucid-meitner-vq1awo` 上的 hash（coordinator cherry-pick 到 T-017 的 534c9ba 之後）；括號中是原 worktree branch `t047`（從 0ee3d2a 開出）上的 hash（review 第 1 輪時改成 branch 上的 hash）。

| Commit | 內容 |
|---|---|
| 0d8f710（9cb0011） | 凍結 des-go-0.1.0 的 kernel、replication 與 event queue（`frozen010_test.go`，只在測試中、改名）；`TestDifferentialAgainstFrozenKernel`；`validate.Pointer` 共用一個 Replacer（原本每個 token 建一個，佔 differential 大部分時間；行為不變） |
| 80c5968（56c9e6c） | Validator R1、`finite_buffer` 列入 `checked`；kernel（des-go-0.2.0）：先解析後提交的路由、被擋、解除、B1 `ErrDeadlock`；Result 的 `resource_states`、`blocking`、`buffers`；trace 的 `blocked`、`unblocked`；D-057 的 server risk；手推 golden、只看 observation 的阻塞檢查器、中性 buffer 的 differential、des-go-0.2.0 的 digest 鎖 |
| 12c76c8（abc5ddf） | `ErrDeadlock` → 不重試的 job failure `SIMULATION_DEADLOCK`（runner），API 為 `MODEL_VALIDATION_FAILED`、severity error（`internal/runs`）；`valid-run-failed-deadlock.json` 的 message 改為 engine 真正的輸出 |
| 19c5e35（1e23484） | Scenario allowlist 的 AI context 與 `prompts/scenario/system.md`（scenario-0.3.0）說明 queue 的緩衝 capacity（D-057） |
| ac32da0（1df953c） | SimPy reference：`make simulation-reference-test`（不再是 placeholder）、`scripts/simpy_reference.py`、SimPy 4.1.2 的 pin、ADR-011、THIRD_PARTY_NOTICES、CI step |
| 3c6e612（ab9a1d0） | 第一版的 validator guard（engine 自己的測試；由 d3cad51 取代並刪除） |
| d3cad51（dfe8a51） | Architecture checker 的 source rule `guarded-variables`：production 檔案不可 assign `engine.validateIR` 或取它的位址（T-046 phase 2 review I2，在解除第一個 gate 之前） |
| 6f8d600（1d0d9ad） | `FuzzBlocking`、FuzzKernel／FuzzRun 的 finite-buffer seed、G-B1W、G-B5 的 member keys、R1 的 self-edge case、observation digest 鎖；D-064（PROPOSED）；SIMULATION_SPEC、TWIN_IR_SPEC、ADR-026、TEST_PLAN、ARCHITECTURE |
| ed4351f（e0e1855） | `TestBlockingDeterminism`（TEST-019 的 finite-buffer 版本）；tasks.json 的 planned outputs、spec refs 與 D-064 gate（status 未改） |
| f73812f（5ea751a） | Makefile：`backend-race` 加 `-timeout 30m`（見「實際執行結果」） |
| cb02b5f（747ba06） | 本報告、evidence、mutation harness 與結果、重產的 `checks/*.json` |
| 78d0549 | Coordinator：在整合後的 branch 上執行，log 在 `coordinator-*` |
| （review 第 1 輪） | 見文末「Review 第 1 輪」 |

## 稽核：scope 逐條

| Scope | 判定 | 證據／處理 |
|---|---|---|
| 1. 有限緩衝與阻塞（D-049） | 已完成 | `internal/simulation/engine/kernel.go`：entity 離開 source、process、delay 時，路由先解析（decision 抽樣、visit 計數、經過的 failure／rework）再提交；終點不是已滿 buffer 時 observation 與 seq 與 des-go-0.1.0 完全相同。已滿時 entity 留在出發 node（`ObsBlocked`），process 保留整個 bundle、不排 dispatch；source 形成 release backlog；delay 不持有東西。被擋的 entity 不在任何 resource FIFO 中，依處理順序排在該 buffer 的 blocked FIFO。解除只在 dispatch 內：授予使 buffer 少一件後，FIFO 最前面的一件釋放出發 process 的 bundle、`ObsUnblocked`、以新的 seq 排到 FIFO 最後；dispatch 做到 fixpoint，連鎖在同一個 dispatch 內完成。Golden G-B1–G-B9（`testdata/simulation/blocking-goldens.json`，HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN）：G-B7 為被擋 bundle 保留共用 resource、G-B8 為一個 dispatch 內的連鎖解除、G-B9 為被擋 entity 不在它等待的 FIFO 中占位、G-B3 為同 tick 兩個被擋（D-048 的 tie-break） |
| 2. Deadlock：R1 與 B1（D-050） | 已完成；DB 部分 NOT RUN | R1（`internal/model/validate/blocking.go`，graph 階段）：持有 resource 的 process 經 p > 0 的 flow、decision、failure、rework 與至多一個 queue 到某 process 的有限 buffer 是一條阻塞邊；共用 resource 的 process 合成一個 component（union-find）；component 圖的 SCC 上（含 self-edge）每個有限 queue 一個 `BLOCKING_CYCLE`，path `/nodes/<i>/capacity`，message 依 ID 排序。`semantic-blocking-cycle-retest.json`、`-shared-resource.json` 回期望的 code 與 path（`TestAgreesWithSharedFixtures`）。B1（`kernel.after`、`kernel.deadlock`）：有有限 buffer 的程式在每個事件結束、且沒有待執行的 dispatch 時檢查（D-064 (a)），沒有服務中與 delay 中的 entity、每個 held candidate 都放不下時回 `*engine.DeadlockError`（G-D2：7 分鐘，回報 `r0/src/5`）。Runner 對應為不重試的 `SIMULATION_DEADLOCK`（`TestEngineFailures`），API 為 `MODEL_VALIDATION_FAILED`（`TestDeadlockError` 依 `valid-run-failed-deadlock.json`）。Valid 的模型因 R1 不會走到 B1，所以沒有端對端的 DB 測試能產生 `SIMULATION_DEADLOCK`（見殘餘風險） |
| 3. Golden、differential、SimPy、mutation、fuzz | 已完成 | 見「測試涵蓋」「Differential」「SimPy reference」「Mutation」「Fuzz」 |
| 4a. Result 成員與 trace | 已完成 | `internal/results/metrics/aggregate.go`：`resource_states`（in_use、blocked、scheduled_processing）、`blocking`（route 到得了有限 buffer 的 node）、`buffers`（full_ticks），只在程式有有限 buffer 時出現（D-047）；warmup 之後的窗口（G-B1W）。`internal/simulation/trace/trace.go`：`blocked`、`unblocked`。`TestBlockingGoldenResults`、`TestBlockingMemberKeys`、`TestNoFiniteBufferNoNewMembers`、`TestBlockedEntitiesInTheTrace`，每個 Result 都依 `simulation.schema.json` 驗證 |
| 4b. 只解除 `finite_buffer` 的 gate | 已完成 | `validate.checked = {FiniteBuffer}`、`engine.kernelExecutes = {FiniteBuffer}`；`TestPrimitiveCatalog`、`TestPrimitiveGate`（valid-line-flow-ir.json 只剩 17 個 PRIMITIVE_NOT_SUPPORTED，兩個 finite buffer 不再列）、`TestCompileRefusesLineFlowPrimitives`、`TestCompileRefusesWhatTheKernelDoesNotRun`、`TestKernelRefusesWhatItDoesNotExecute`。cases.json 的 `primitives` 欄位已在 T-046 phase 2 寫好，不用改 |
| 4c. engine_version | 已完成 | `des.Version = "des-go-0.2.0"`。des-go-0.1.0 的 `engine-golden-digests.json` 不重產：GOLDEN-001／002 的 Result 把 engine_version 換回 des-go-0.1.0 後 digest 等於鎖定值（`TestGoldenDigests`）；新版另立 `engine-golden-digests-0.2.0.json` 與 `kernel-observation-digests-0.2.0.json`（engine 輸出的 regression lock，不是 oracle） |
| 4d. D-057 server risk | 已完成 | `internal/ai/review/risk.go`：queue 的 capacity 變小、或 base 為無限而 candidate 為有限 → 至少 medium；變大或改回無限 → low。`TestBufferCapacityRisk`、`TestLiftedPrimitivesHaveRiskCases` |
| 4e. AI context | 已完成 | `aicontext.modeRules` 的 SCENARIO_PATCH_ALLOWLIST 與 `prompts/scenario/system.md`（scenario-0.2.0 → 0.3.0）說明 queue 的緩衝 capacity 可設、改、移除，變小為至少 medium，造成循環阻塞會被拒絕；`TestScenarioMode`。沒有新的 context 種類，T-024 的 redaction／injection 測試照樣通過 |
| T-046 phase 2 review I2 | 已完成 | `engine.validateIR` 只有該 package 的測試可以替換：architecture checker 的 `guarded-variables` source rule 讀 go list 對 matrix 每個平台回報的 production 檔案（含 overlay）；`TestGuardedVariables`（assign、取位址為 violation，test 檔不是）、`TestGuardedVariableMustExist`（變數不再是 package 變數時為 error，不是默默通過） |
| 沒有 migration、只用 CB-41、D-064–D-066 | 符合 | 沒有新增或修改 migration；D-064 是 PROPOSED；D-065、D-066 未使用 |
| 不碰 T-026、T-017 | 符合 | 沒有修改 `internal/proposals`、`internal/model` 的 approval、`internal/import`、migration 0013／0014；`internal/ai/review` 只改 `risk.go` 與兩個測試檔 |

## 實際執行結果

在 worktree 中執行（`environment.txt`：Go 1.27.2、Python 3.13.16、SimPy 4.1.2、Node 22.22.0、4 CPU，與另外兩個 agent session 共用，它們同時在跑自己的 `go test -race`；沒有 PostgreSQL）。Go 測試都以 `-count=1`（`GOFLAGS=-count=1`）執行，不用 cache。Exit code 在 `exit-codes.txt`。

| 指令 | Exit | 說明 |
|---|---:|---|
| `make backend-test`（開始前，0ee3d2a） | 0 | `baseline-backend-test.log` |
| `make spec-check PYTHON=.venv/bin/python` | 0 | `spec-check.log`：contracts 52 正例、101 反例（96 個有精確 error path），semantic fixture 7 個；`PASS_TS_CONTRACTS_FRESH`（14 檔、102 types）；`PASS_INDEPENDENT_HASHES`（28）；`PASS_DB_TESTS_FRESH`（10）。重產的 `decisions-check.json`（proposed 0 → 1：D-064；T-047 從 `ready_tasks` 移到 `next_after_review`，等 D-064）、`planning-check.json`（proposed_decisions 1）、`docs-check.json`（markdown 檔 96，含本報告）與本報告一起 commit |
| `make backend-test GOFLAGS=-count=1` | 0 | `backend-test.log`：gofmt、go vet、57 個 package ok，沒有 `(cached)`；DB-backed 測試 skip（NOT RUN）。engine 48 s、check-architecture 148 s |
| `make backend-race GOFLAGS=-count=1`（第一次） | 2 | `backend-race-run1-timeout.log`：`internal/simulation/engine` 在 go test 預設的 10 分鐘 timeout 時仍在執行（`panic: test timed out after 10m0s`，當時在 `TestRandomCircularWaitsAreDetected`）。不是 data race，也不是失敗的 assertion：race detector 下 D-047 的 differential（50,000 個模型、兩個 kernel）與 T-047 的阻塞檢查需要超過 10 分鐘。處理：Makefile 的 `backend-race` 改為 `-timeout 30m`（註解說明理由），沒有縮小任何測試或加 skip |
| `make backend-race GOFLAGS=-count=1`（`-timeout 30m`） | 0 | `backend-race.log`：57 個 package ok，沒有 `(cached)`；engine 356 s、des 72 s、check-architecture 204 s（這次另外兩個 session 的負載較輕；第一次在較重的負載下超過 600 s） |
| `make architecture-check` | 0 | `architecture-check.log`：69 個 package、15 條 import 規則、16 種 build configuration，以及新的 `guarded-variables` source rule |
| `make ai-eval` | 0 | `ai-eval.log`：`TestFakeProviderEval`、`TestReviewEval` PASS；`checks/ai-evals/` 沒有變化（scenario 規則的文字與 prompt 版本不在 eval 的報告中，review 的指標也沒有變），所以沒有要 commit 的重產檔案 |
| `make simulation-reference-test PYTHON=.venv/bin/python` | 0 | `simulation-reference-test.log`、`simpy-reference-report.json`：見「SimPy reference」 |
| `make frontend-test` | 2 | NOT RUN：見「NOT RUN」 |
| `go test -count=1 -v ./internal/simulation/engine/` | 0 | `engine-tests-verbose.log`：80 個 PASS、0 個 FAIL，統計見「Differential」 |
| `go test -fuzz`（FuzzBlocking 3 分鐘、FuzzKernel 90 秒、FuzzRun 90 秒） | 0 | `fuzz-*.log`：見「Fuzz」 |
| `t047-mutants.py`（兩輪） | 1、0 | `mutants-run1.log`、`mutants-run2.log`：見「Mutation」 |
| `make ts-contracts` | — | 不需要：schema 沒有改；spec-check 的 `--check` 通過 |
| DB 測試、`make integration-test` | — | NOT RUN（沒有 PostgreSQL，依指示交給 coordinator） |

## 測試涵蓋

| 測試 | 驗證 | 驗收 |
|---|---|---|
| `TestBlockingGoldens`、`TestUnblockChainInOneDispatch`、`TestBlockedRouteIsDrawnOnce`、`TestNeutralBufferGolden`、`TestDeadlockIsATypedError`（engine） | 手推 golden 的 observation 層：G-B1–G-B4、G-B7–G-B9 每個 entity 的 service 開始、完成與每段阻塞；G-B8 的連鎖解除在一個 dispatch 內、依 D-049 的順序（授予 → 它允許的解除：釋放的 bundle、解除的 entity、新的 request → 下一個 seq 最小的 candidate）；G-B5 的 decision 每次完成只抽一次，解除時不重抽；G-B6 capacity ≥ max_active_entities 與 G-B2 逐筆相同；G-D2 在循環等待形成的 tick（7 分鐘）回 `*DeadlockError`，回報最早仍被擋的 entity，不跑到 horizon，同一模型被 validator 以 R1 拒絕 | TEST-034 |
| `TestBlockingGoldenResults`、`TestBlockingMemberKeys`、`TestNoFiniteBufferNoNewMembers`、`TestBlockedEntitiesInTheTrace`（des） | 手推 golden 的 Result 層：8 個 case 的 `blocking`、`buffers`、`resource_states` 完全相同，completed、censored、utilization、lead time、queue wait 與手推值相符，digest 重算一致；G-B1W 只計 warmup 之後；G-B5 的 member key 不隨 routing 抽樣改變；沒有有限 buffer 的模型沒有新成員；trace 的 `blocked`、`unblocked` 在被擋的 node、依 engine 順序 | TEST-034 |
| `TestBlockingObservationDigests`、`TestGoldenDigests020`（regression lock，不是 oracle） | 每個 finite-buffer golden 與 reference case 的 observation 序列（kind、tick、seq、entity、node、resource、value）與 des-go-0.2.0 的 Result digest 被鎖定；任何順序或 seq 的改變都會被看到 | TEST-015、TEST-016、TEST-034 |
| `TestGoldenDigests`、`TestGoldenOneResult`、`TestGoldenOneTrace` | GOLDEN-001／002 在新 kernel 上：把 engine_version 換回 des-go-0.1.0 後 digest 等於 des-go-0.1.0 的鎖定值（D-047，鎖不重產） | TEST-015、TEST-016 |
| `TestDeterminism`、`TestBlockingDeterminism` | 同一凍結輸入，不論 1、3、8 或預設 worker 數，Result 與 trace 逐 bit 相同；另一個 seed 得到另一個 digest。後者用四個隨機 reference case（30 replications，含阻塞、rework、共用 bundle） | TEST-019 |
| `TestDifferentialAgainstFrozenKernel` | 凍結的 des-go-0.1.0 kernel（`frozen010_test.go`）與新 kernel 對 50,000 個沒有有限 buffer 的隨機模型與 golden models：observation、結局與錯誤完全相同，包括在路由中途因 `max_entity_visits` 結束、在 arrival 因 `max_active_entities` 結束的 run | TEST-034、D-047 |
| `TestNeutralBuffersChangeNothing` | 隨機模型加上 capacity ≥ max_active_entities 的中性 buffer：observation（含 seq）與沒有 capacity 的模型在凍結 kernel 上相同，沒有任何 `ObsBlocked`／`ObsUnblocked` | TEST-034、D-047 |
| `TestRandomBlockingModelsAgainstSpec`、`TestRandomCircularWaitsAreDetected`、`FuzzBlocking`（`kernel_check_blocking_test.go`） | 只看 observation 的阻塞模型（不讀 kernel 內部）：每個 buffer 的等待數不超過 capacity；被擋的 entity 保留 bundle、不在 FIFO 中；解除依 FIFO、在授予之後、同一 dispatch；同一個 replication 平行執行結果相同。R1 被繞過（只在測試中）時，每個 `ErrDeadlock` 在那個 tick 都是真的循環等待，其他 run 沒有經過任何以循環等待結束的 tick（不誤報、不漏報）。**更正（review 第 1 輪 I3）**：「不漏報」只在 D-050 B1 的全域定義下成立（沒有任何服務中或 delay 中的 entity）；循環旁有一直服務中的 process 時，局部的循環等待依定義不報，停到 horizon（D-064 (g)） | TEST-034、D-050 |
| `TestBlockingCycles`、`TestBlockingCycleMessage`、`TestBlockingCycleStage`、`TestAgreesWithSharedFixtures`（validate） | R1 的 15 個 case：一條線、process 自己 buffer 的重測迴圈、經無限 queue 的迴圈、兩個有限 buffer 的迴圈、一有限一無限、共用 resource 但沒有 flow cycle、resource 鏈合併 component、繞過有限 queue 直接進 process、直接進同一 component（self-edge）、直接進共用 resource 的 process、經 failure 與 rework、delay 先釋放 bundle、沒有 resource 的 process 不持有、0% flow 不是路由、source 到不了的 cycle 也拒絕；message 依 ID 排序、不受 IR 陣列順序影響；R1 屬 graph 階段（D-023）；兩個 semantic fixture 回期望的 code 與 path | TEST-001、TEST-034 |
| `TestPrimitiveCatalog`、`TestPrimitiveGate`、`TestCompileRefusesLineFlowPrimitives`、`TestCompileRefusesWhatTheKernelDoesNotRun`、`TestKernelRefusesWhatItDoesNotExecute` | 只解除 `finite_buffer`；其他 primitive 在 validator 與 Compile 仍回 `PRIMITIVE_NOT_SUPPORTED` | D-063 |
| `TestEngineFailures`（runner）、`TestDeadlockError`（runs） | `*DeadlockError` 與單獨的 `ErrDeadlock` 都是不重試的 `SIMULATION_DEADLOCK`；message 為 G-D2 的手推 reason；API 的 envelope 等於 `valid-run-failed-deadlock.json`，其 engine_version 等於 `des.Version` | D-050、D-063 (b) |
| `TestBufferCapacityRisk`、`TestLiftedPrimitivesHaveRiskCases`（review） | D-057：變小、無限變有限為 medium；變大、改回無限為 low；每個已解除 gate 的 primitive 都要有 risk case | D-057 |
| `TestScenarioMode`（aicontext） | Scenario 規則說明 queue 的緩衝 capacity、D-057、BLOCKING_CYCLE、至少 medium；不提 gate 仍開著的 breakdowns、calendar、setups、batch | AI_SPEC |
| `TestGuardedVariables`、`TestGuardedVariableMustExist`（check-architecture） | production 檔案 assign `validateIR` 或取位址為 violation，test 檔不是；變數不再是 package 變數時為 error | T-046 phase 2 review I2 |
| `TestSimPyReferenceExport` ＋ `scripts/simpy_reference.py` | 見「SimPy reference」 | TEST-034 |
| DB（NOT RUN）：見「NOT RUN」 | | |

### 先紅後綠

測試先寫，實作前的 red log 在 evidence 目錄（`baseline-backend-test.log` 為開始前 0ee3d2a 的 `make backend-test`，exit 0）：

- `red-validate.log`：`TestBlockingCycles` 等在 R1 之前失敗（finite buffer 只得到 `PRIMITIVE_NOT_SUPPORTED`，沒有 `BLOCKING_CYCLE`）。
- `red-engine-vet.log`：engine 的新測試無法編譯（`ObsBlocked`、`ObsUnblocked`、`HasFiniteBuffers`、`DeadlockError`、`ErrDeadlock` 尚不存在），exit 1。
- `red-des.log`：`TestBlockingGoldenResults` 的 `blocking`、`buffers`、`resource_states` 都是空的。
- `red-review.log`：`TestBufferCapacityRisk`、`TestLiftedPrimitivesHaveRiskCases` 中變小與無限變有限都是 low。
- `red-runs.log`：`store.SimulationDeadlock` 尚不存在，runner 與 runs 的測試無法編譯。
- `FuzzBlocking` 第一次執行時 harness 把 `UNREACHABLE_NODE` warning 當成拒絕，修正為只看 error severity，並保留 regression seed（`testdata/fuzz/FuzzBlocking/e738668a1bb5995d`）；kernel 沒有改。
- `TestSyntheticGraph` 在 source rule 加入後失敗（synthetic graph 的 package 沒有檔案，被當成「宣告 0 次」）；改為跳過沒有目錄的 package，另加 `TestGuardedVariableMustExist` 證明真實 package 中變數消失時仍是 error。

## Differential（D-047）

數字取自 `engine-tests-verbose.log`（`go test -count=1 -v ./internal/simulation/engine/`，只留 `---` 行與統計行；80 個 PASS、0 個 FAIL）：

- `TestDifferentialAgainstFrozenKernel`：50,000 個隨機模型中 36,643 個 compile（其餘被 validator 拒絕），每個 5 個 replication 中的一個在兩個 kernel 上執行，17,427,661 個 observation 完全相同；結局：31,073 個正常結束、3,190 個在 `max_entity_visits`（路由中途）、2,380 個在 `max_active_entities`，錯誤前的 observation 也相同。之後是 golden models 在 1、14、60 天、每個 3 個 replication 的比對。測試自己要求至少 1/4 compile、各種結局至少 100／1,000 個，否則判為太弱而失敗。
- `TestNeutralBuffersChangeNothing`：3,728 個隨機模型、5,000 個中性 buffer（capacity ≥ max_active_entities），observation（含 seq）與沒有 capacity 的模型在凍結 kernel 上相同，沒有任何 Blocked／Unblocked。
- `TestRandomBlockingModelsAgainstSpec`：3,000 個隨機模型中 2,195 個 compile、666 個有有限 buffer；11,095 次被擋、7,501 次解除（114 個 dispatch 有連鎖解除）、131,949 次授予，都與只看 observation 的模型一致。`TestRandomCircularWaitsAreDetected`：R1 繞過時 1,059 個 run，其中 123 個以 `ErrDeadlock` 結束，每一個都是真的循環等待，其他 run 沒有漏報。
- 外部比對（`differential-base-vs-head.txt`、`differential_dump_test.go.txt`）：同一個 dump 測試以 overlay 分別在 0ee3d2a 與本 branch 上執行，四個 golden model 各三種 horizon／warmup／replication 的完整 Result 與 trace JSON（engine_version 換回 des-go-0.1.0、digest 重算），兩邊 24 行、201,023 bytes，sha256 都是 `0dcb6d050982c611a1a96727ecb27fc5d679eb41f676654e8487e60e63b82e01`：IDENTICAL。這次比對（06:03）在 engine、compile、metrics、trace 與 validator 的 production code 最後一次修改（80c5968，worktree 上為 56c9e6c，05:31）之後；其後只有 runner／runs 的 failure 對應與 AI context（12c76c8、19c5e35，不影響 engine 的輸出）以及測試、文件、architecture checker 與 Makefile 的 commit。

## SimPy reference（`make simulation-reference-test`）

SimPy 4.1.2（MIT；2026-05-24 發布，滿兩週；wheel sha256 與 PyPI 公布值相同，ADR-011、THIRD_PARTY_NOTICES）。`TestSimPyReferenceExport` 寫出 Go engine 對 reference cases 的每個 replication 結果，`scripts/simpy_reference.py` 以 SimPy 自己的 resource 解讀同一個 IR（有限 buffer 為「空位」resource，blocking after service 時繼續持有機台），用 Python 自己的 RNG：

- 確定性：G-B1、G-B1W、G-B2、G-B4、G-B7、G-B8、G-B9 逐值等於手推的 service 開始、完成、每段阻塞與 Result 成員；G-D2 為示意（SimPy 不偵測 deadlock：最後一個 service 開始於 5 分鐘、7 分鐘起無法前進，與 engine 的 7 分鐘相同）。
- 不比：G-B3（同 tick 的 tie-break 是 kernel 的 D-048，SimPy 不重現）、G-B5（kernel 的 routing stream）。
- 隨機：S1–S4 各 30 replications，45 個指標的平均都在 fixture 中預先固定的容差內（|Δ| ≤ 4·√(s²_go/n＋s²_simpy/n)＋0.5%·max(|平均|)），45/45。
- 限制：units 必須為 1；bundle 依「共用 resource 先、自己的後」逐一取得，只在一個 bundle 至多一個共用 resource 時等於 all-or-nothing，腳本對其他模型拒絕執行。**更正（review 第 1 輪 L1）**：這個條件不夠，還要自己的每個 resource 的 capacity 不小於共用的那個；反例 G-B13（engine 在 11 讓 Y 開始，腳本會在 3）。腳本已改為拒絕這種模型，fixture 中標為 refused 的 case 每次都必須被拒絕。**再更正（review 第 2 輪 L1′）**：只有自己的 resource 的 bundle 原依 ID 取得，capacity 遞減時（G-B14）也與 engine 不同；改為自己的 resource 依 capacity 由小到大取得，見文末「Review 第 2 輪」。這是 reference，不是 oracle：兩邊同樣誤解 D-049 的地方它看不出來，手推 golden 才是語義的依據。

## Mutation

`checks/evidence/2026-10-20-T-047/t047-mutants.py`：每個 mutant 是一個精確的 search／replace，以 `go test -overlay -failfast` 套用（working tree 不改），列出的 package 測試失敗即 KILLED；pattern 不是恰好出現一次即 BAD-MUTANT。62 個 mutant 涵蓋 kernel 的阻塞、解除、dispatch 與 B1（27）、compile（5）、R1 與 gate（10）、Result 成員（7）、trace（1）、engine_version（1）、runner 與 API 的對應（5）、architecture checker 的 guard（3）、D-057 risk（3）。

- 第 1 輪（`mutants-run1.log`，61 個）：55 KILLED、1 EQUIVALENT、1 SURVIVED、4 個 mutant 本身無法編譯（拿掉一行後留下未使用的變數：`deadlock-ignores-fitting-held`、`resolve-counts-no-visits`、`r1-no-components`、`deadlock-unmapped`）。
- 修正這 4 個 mutant 的寫法（改為 `_ = 變數`），把 `deadlock-unmapped` 拆成「兩個 case 都拿掉」與新的 `deadlock-details-dropped`（只拿掉帶細節的 case），並把存活的 `enqueue-capacity-unchecked` 標為 equivalent 後重跑（`mutants-run2.log`）：4 KILLED、2 EQUIVALENT。
- 合計：**62 個中 59 個 KILLED、3 個 EQUIVALENT、0 個存活**。沒有為了殺 mutant 而降低或改動任何 assertion。

Equivalent 的理由（寫在 harness 的註解）：

| Mutant | 理由 |
|---|---|
| `enqueue-capacity-unchecked` | `enqueue` 中 buffer 已滿時回 `ErrInternal` 的 invariant 檢查。呼叫者都先判斷：路由到已滿的 buffer 時改為被擋（`resolve`、`full`），解除只在同一個 dispatch 的授予使 buffer 少一件之後；正確的 kernel 走不到這個分支 |
| `deadlock-ignores-fitting-held` | B1 中「held candidate 放得下就不是 deadlock」的分支。`release()` 會喚醒它釋放的每個 resource FIFO 最前面的 request 並排 dispatch，所以 `after()` 執行時（沒有待執行的 dispatch）每個 held candidate 都放不下；這個分支只在 invariant 被破壞時防止誤報 |
| `delay-blocks-hold-units` | 只有 process 有 requirements，其他 node 的 `Requires` 是空的，所以 node type 的判斷只省下一次空迴圈 |

## Fuzz

各 fuzz target 以 `-fuzztime` 執行（log 在 evidence 目錄），沒有發現失敗：

- `FuzzBlocking`（engine）：3 分鐘，coordinator 顯示 130,298 execs、592 個 corpus（`fuzz-blocking.log`）。
- `FuzzKernel`（engine，加入 finite-buffer seed）：90 秒，顯示 484,000 execs（`fuzz-kernel.log`）。
- `FuzzRun`（des，加入 finite-buffer seed）：90 秒，顯示 35 execs（`fuzz-run.log`）。
- Go fuzzing 的 coordinator 顯示的 execs 會少算：臨時加了每個 worker 的計數器（不 commit）跑 25 秒，coordinator 顯示 13，四個 worker 實際各執行 221,359–357,830 次（`fuzz-exec-count-diagnostic.txt`）。FuzzRun 每次執行整個 engine（最多 255 replications），實際次數沒有量測，顯示的 35 只代表下限。
- 三個 target 的 seed corpus 也在 `make backend-test` 中當一般測試執行。

## NOT RUN

- **所有 DB 測試，交給 coordinator 在一次性 DB 上執行**：`make integration-test`（`make db-test` 與 `make db-go-test`）。這裡沒有 PostgreSQL，依指示不跑任何 DB 測試，也沒有嘗試以其他方式取得 DB。本任務沒有 migration，`db/tests/*.sql` 沒有改（`PASS_DB_TESTS_FRESH`），但 engine_version（des-go-0.2.0）與 failure 的對應會經過 DB，以下 DB-backed Go 測試在本 worktree 中都是 skip：
  - `internal/runs/runner`：`TestEventBudgetAcrossRetries`、`TestWallClockAcrossRetries`、`TestACrashedAttemptIsRunAgain`、`TestNoResultAfterALostLease`、`TestALateCancellationWins`、`TestRefusedAttempts`（engine_version 不符）、`TestOneJobPerRun`、`TestACancellationOutlivesACrash`、`TestTheWallClockEndsAnAttempt`；
  - `internal/api` 的 `simulations_test.go`：`TestRunClosedLoop`、`TestRunRoleMatrix`、`TestRunRequestRefusals`、`TestRunIdempotency`、`TestARevokedMemberGetsNoReplay`；
  - `cmd/worker`、`internal/worker`、`internal/storage/...` 的 DB-backed 測試與 `db/tests/runs.sql`（整個 `make integration-test`）。
- **沒有端對端的 `SIMULATION_DEADLOCK` 測試**：valid 的模型因 R1 不會 deadlock，經 API 建立的 run 走不到 B1；runner 與 API 的對應只有 unit test（`TestEngineFailures`、`TestDeadlockError`），failure 寫入 `jobs.failure`、run 轉 failed、`getSimulation` 顯示 envelope 的 DB 路徑沒有以 deadlock 執行過（與其他 execution limit 走同一條路徑）。
- `make frontend-test`：exit 2（`frontend-test.log`）：沒有 `apps/web/node_modules`，`eslint.config.mjs` 找不到 package `eslint`（`ERR_MODULE_NOT_FOUND`）；Node 是 22.22.0，`apps/web/package.json` 要求 `>=24.21.0 <25`，依 engine-strict 不能安裝，沒有繞過。本任務沒有改 schema、產生的 TS 或 `apps/web`，前端的 lint、typecheck、unit test 都沒有執行。
- `make e2e-test`：本任務沒有 UI 變更，未執行。`make benchmark-test`：仍是 NOT IMPLEMENTED 的 placeholder，B1 每個事件的檢查與阻塞的成本沒有效能量測。
- Live AI 與 `make ai-eval-live`：live AI 未核准（H-001、H-017）。
- `make backend-race` 只在 linux/amd64 執行；architecture-check 的 16 種 build configuration 只做 import 與 source 規則，不執行測試。
- CI 新增的 `make simulation-reference-test` step 沒有在 GitHub Actions 上執行過（只在本機執行）。
- `make ts-contracts`：schema 沒有改，不需要重產；`make spec-check` 中的 `gen_ts_contracts.py --check` 為 `PASS_TS_CONTRACTS_FRESH`。

## 需要 owner 或 reviewer 決定

- **D-064**，PROPOSED（`planning/decisions.json`、DECISIONS_AND_TBD）：(a) B1 在每個事件結束、且沒有待執行的 dispatch 時檢查（批准文字寫「每次 dispatch 結束時」，照字面會漏掉 G-D2，run 默默跑到 horizon）；(b) R1 也檢查 source 到不了的 node；(c) 解除在該次授予的 ObsResource、ServiceStart 與 service 抽樣之後（只影響 observation 順序）；(d) `ErrDeadlock` 回報最早的 blocked entity（含 source 的 backlog）。四項都已依建議實作，可回復；不採用時要改的檔案與 golden 寫在 D-064。Review 第 1 輪另加 (e)(f) 與兩項說明 (g)(h)，見文末。Gate：T-047 驗收前；planning check 中 T-047 因此為 `next_after_review`，等 D-064。
- D-065、D-066 未使用。
- 其他實作選擇（都在已批准的範圍內，寫在 SIMULATION_SPEC §3、§10 與 TWIN_IR_SPEC §4，請 reviewer 確認解讀）：`blocking` 的 key 為「路由到得了有限 buffer 的 node」（不隨抽樣改變）；Result 的新成員只在程式有有限 buffer 時出現（D-047）；R1 的 message 列出 cycle 上的 process、有限 buffer 與合併 component 的 resource；SimPy reference 的容差與不比的 case。

## 殘餘風險

- **D-064 仍是 PROPOSED**。Owner 若對 (a)–(d) 任一項選 B，要改 kernel 或 validator 與對應的 golden、fixture（D-064 列出檔案）；(a) 選 B 會讓 G-D2 這類循環等待默默跑到 horizon，違反 TEST-034。
- **手推 golden 與 kernel 出自同一個 agent**：`blocking-goldens.json` 是依 D-049／D-050 的文字手推，不是由 engine 產生，但推導者與實作者相同，兩者可能有同樣的誤讀。SimPy reference 以不同的實作方式（SimPy 的 resource、Python 的 RNG）重現了確定性 case 與隨機指標，降低但沒有消除這個風險；G-B3 的同 tick 順序與 G-B5 的 routing stream 只有手推值。Reviewer 宜獨立重推至少 G-B7、G-B8、G-D2。
- **R1 是保守的檢查**（D-050 已批准）：只看圖，不看 capacity 的大小，所以實際上不會卡死的循環（例如 buffer 夠大、或循環很少被走到）也會被拒絕，scenario 把 buffer 改為有限時也可能因此 invalid。D-064 (b) 選 A 時，source 到不了的部分也會被拒絕。
- **B1 只在 R1 被繞過時才會觸發**：production 中它是第二道防線；它的正確性由 `TestRandomCircularWaitsAreDetected`（R1 繞過時 1,059 個 run、123 個 deadlock，依全域定義不誤報、不漏報；review 第 1 輪 I3）、G-D2 與 G-D3 支撐，沒有端對端的 DB 測試（見 NOT RUN）。
- **Regression lock 不是 oracle**：`engine-golden-digests-0.2.0.json`、`kernel-observation-digests-0.2.0.json` 由 des-go-0.2.0 產生，只讓之後的任何改變被看到；它們本身的正確性靠手推 golden、阻塞檢查器與 SimPy。
- **Result 成員的解讀**：`blocking` 的 key 是「路由到得了有限 buffer 的 node」（G-B5 固定，不隨抽樣改變），`buffers` 只列有限 buffer，`resource_states` 列所有 resource；這些是 D-058 表的實作解讀，T-048–T-050 會再擴充同一組成員。
- **SimPy reference 的範圍**：units 必須為 1，bundle 至多一個共用 resource，且自己的 resource 的 capacity 不小於共用的那個（review 第 1 輪 L1 更正）；自己的 resource 依 capacity 由小到大取得（第 2 輪 L1′ 更正）；四個隨機 case、每個 30 replications；容差 z=4 加 0.5% 相對誤差，小於容差的系統性差異（約 0.5% 加上統計誤差）看不出來。
- **Fuzz 的次數**：Go fuzzing 顯示的 execs 會少算，FuzzRun 實際執行次數沒有量測；fuzz 時間各為 90 秒到 3 分鐘。
- **部署時的 engine_version**：run 凍結 engine_version，升到 des-go-0.2.0 後，部署前以 des-go-0.1.0 入列、尚未執行的 run 會以不重試的 `ENGINE_VERSION_MISMATCH` 失敗（T-023 的既有行為，`TestRefusedAttempts`，DB NOT RUN）；目前沒有任何部署。
- **效能**：有有限 buffer 的程式在每個事件後做 B1 檢查（服務中或 delay 中有 entity 時立即返回，否則掃描 resource FIFO 與 blocked FIFO）；沒有 benchmark。沒有有限 buffer 的程式不檢查（D-047），differential 證明其 observation 不變。

## Coordinator 的執行（cb02b5f）

Coordinator 把 11 個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（T-017 的 534c9ba 之後）。衝突只在決策登記（D-064 一節放在 T-026、T-017 兩節之前）與 `planning/decisions.json`（加入 D-064）；產生的 `checks/*.json` 由 `make spec-check` 重產。SimPy 4.1.2：MIT（PyPI 的 license 與 classifier），2026-05-24 上傳，超過兩週。

之後 `spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`simulation-reference-test`（PASS）、`frontend-test`（429 個 test）、`backend-race`（30 分鐘 timeout）與 `integration-test`（新的 `dt_test_t047`：30 個 package ok，沒有 FAIL 或 SKIP，包括 `internal/runs/runner`、`internal/api` 的 run 測試）都是 exit 0。上方 NOT RUN 中的 DB 測試與 `make frontend-test` 已由 coordinator 執行並通過。Log 與 exit code 在 `checks/evidence/2026-10-20-T-047/coordinator-*`。

## Review 第 1 輪（H-003，2026-10-20）

獨立 review 的結論是 CHANGES REQUESTED（0 個 High、3 個 Medium，另有 Low 與 Info）。Reviewer 獨立重推 G-B7、G-B8、G-D2、G-B1／G-B1W／G-B3／G-B4／G-B9 與六個新情境，都與 engine 相符；D-047 的 differential（16 個 golden 設定、400 個隨機模型）逐 byte 相同。問題都在測試沒有釘住的地方。修正在 branch `t047-r1`（從 8413288 開出，沒有 push）；沒有修改任何 production 的 Go code（kernel、compile、metrics、validator、risk 都沒動），只加測試、golden、SimPy 腳本的拒絕條件、race 下的 differential 件數與文件。D-064 仍是 PROPOSED，`approved_by`／`approved_at` 為 null；沒有碰 T-017（`internal/import`、0014）與 T-026 的範圍，決策登記只改 D-064。Evidence 在 `checks/evidence/2026-10-20-T-047-r1/`。

### Commit

下表是 `claude/lucid-meitner-vq1awo` 上的 hash（coordinator cherry-pick 到 T-017 的 6ed6b71 之後，沒有衝突）；括號中是 worktree branch `t047-r1` 上的 hash。

| Commit | 內容 |
|---|---|
| 6d6c5dd（ddf2f06） | 新的手推 golden：G-B4W（M1）、G-B10（M2）、G-B11、G-B12（M3）、G-B13（L1 的 engine 端）、G-D3（L2）；L3 的 risk case；runner 與 runs 的測試改為以 ID 取 G-D2；兩個 regression lock 加入新 case |
| 6ffe1a1（46f5942） | L1：SimPy 腳本拒絕「自己的 resource 的 capacity 小於共用的」bundle；fixture 中標為 refused 的 case 每次都必須被拒絕 |
| 4fdd524（2960fb1） | I5：race detector 下 frozen-kernel differential 只跑前 5,000 個模型 |
| b159c7d（16a4112） | I2、I3：D-064 (e)–(h)（PROPOSED）、SIMULATION_SPEC §3／§5／§9／§10、TEST_PLAN、Makefile 的註解 |
| a4cd3a0（a2afc08） | I5 的修正：2960fb1 用 `//go:build race` 檔，被 architecture checker 的 every-file-built（T-042）擋下；改為讀 test binary 的 `-race` build setting |
| 9e62311（a15eef0） | 本報告的本節與更正、evidence |

### 發現、修正與證據

每個新 golden 的期望值都先依 D-048／D-049 手推（推導寫在 `blocking-goldens.json` 每個 case 前的註解，產生器只組 JSON），再以未改動的 engine 執行；engine 端與 Result 端都一次通過。新增的 regression lock 只在手推值通過之後才取。

| 項目 | 問題 | 修正 | Red（修正前） | Green |
|---|---|---|---|---|
| M1 | 「開始 ≥ W」的下界沒有測試：G-B1W 沒有任何阻塞剛好在 W 開始，`aggregate.go` 的 blocks_started 與 blocks_completed 改成嚴格不等號都存活 | G-B4W：G-B4 加 warmup 6、horizon 20，e6 的阻塞在 6 開始。手推：blocks_started 15、blocks_completed 6、completed_block_ticks 1,980,000（33 分）、blocked_entity_ticks 4,200,000（70 分：e5 在 [6,7] 的 1、e6–e11 的 33、e12–e20 的 36）、qw full_ticks 840,000、完成 7、lead time 7／8／9／10（平均 8.5）、qw 的 queue wait 6 筆各 2、censored 11；與 reviewer 的值相同。Result 層（`TestBlockingGoldenResults`）與 SimPy 都比對 | `review-mutants-before.log`（reviewer 的執行）：`blocks-started-strict-warmup`、`blocks-done-strict-warmup` SURVIVED | `review-mutants-rerun.log`：兩者 KILLED（`TestBlockingGoldenResults`）；SimPy 的確定性比對包含 G-B4W |
| M2 | units > 1 時的 blocked_ticks 沒有測試（所有 golden 都是 units 1，SimPy 腳本拒絕 units ≠ 1）：`sign * int64(q.Units)` 改成 `sign` 存活 | G-B10（reviewer 的 N4）：A 需要 ra×2（capacity 2）。手推：e3 在 A 被擋 [4,5]，持有兩個 unit；ra in_use_ticks 480,000、blocked_ticks 120,000、scheduled_processing_ticks 360,000；utilization 依 §5 為 8／(2×20)。SimPy 不比（units 2），fixture 寫明理由 | `held-ignores-units` SURVIVED | KILLED（`TestBlockingGoldenResults`） |
| M3 | 哪些 node 出現在 `blocking[<node>]` 沒有測試：`compile.go` 的 `reachesFinite` 只經 decision，或讓 delay 不結束路由，都存活 | G-B11（N5：經 failure node，A 是 key，blocking.A = 1/1/60,000/60,000，failures 3，qB full_ticks 300,000）、G-B12（N6：delay 結束路由，只有 dly 是 key，blocking.dly = 2/2/240,000/240,000，src 不是 key）。兩者都在 golden 的 Result 中（`blocking` 以 `reflect.DeepEqual` 比對整個 map）並列 `member_keys`（`TestBlockingMemberKeys` 現在要求至少 3 個 case）；SimPy 也比對兩者 | `canblock-skips-failure-rework`、`canblock-through-delay` SURVIVED | 兩者 KILLED（`TestBlockingGoldenResults`） |
| L1 | SimPy 腳本的有效條件寫錯：「units 1、bundle 至多一個共用 resource」不夠，還要自己的 resource 的 capacity 不小於共用的那個。反例 N3：engine 在 11 讓 Y 開始，腳本在 3 | 腳本在 `Model` 中拒絕這種 bundle（理由寫在註解：取得共用的 unit 時，至多 capacity(S)−1 個其他 entity 持有自己的 O，capacity(O) ≥ capacity(S) 時 O 一定有空）；G-B13（N3）成為 engine 的 golden（Y 在 11 開始：held 的 x2 擋住 S 的 FIFO），fixture 標為 refused，腳本每次都要拒絕它。docstring、SIMULATION_SPEC §10、本報告的 SimPy 段與殘餘風險已更正 | `l1-simpy-red.log`：腳本接受 G-B13（`the script accepted a model it cannot interpret`），exit 2 | `l1-simpy-green.log`：以 capacity 的理由拒絕 G-B13，其他確定性 case 與 45／45 隨機指標照樣通過，exit 0 |
| L2 | D-064 (d)（回報哪個 entity）與「node 順序最前的 buffer」無法區分：G-D2 中最早被擋的 entity 也在 ID 最前的 buffer，`block-order-not-incremented` 存活 | G-D3（R1 繞過，與 G-D2 相同的方式）：x1、y1 都在 4 被擋，y1 等待的 X 的 buffer 在 node 順序上較前，但 x1 先被擋（X 的 SERVICE_COMPLETE 先處理）：回報 `r0/sx/1` 在 X、since 240,000。`TestDeadlockNamesTheEntityBlockedFirst` 也檢查 validator 的 code 與 path，以及 X 的 index 確實小於 Y | `block-order-not-incremented`、`deadlock-earliest-by-since` SURVIVED | 兩者 KILLED（`TestDeadlockNamesTheEntityBlockedFirst`） |
| L3 | `risk.go` 的 `<` 改成 `<=` 存活 | `TestBufferCapacityRisk` 加「an unchanged buffer beside another change」：base 有 capacity 4 的 q_test，scenario 只改 test 的 service time → low | `risk-equal-capacity-medium` SURVIVED | KILLED（`TestBufferCapacityRisk`） |
| I2 | 文件沒有寫：長度 0 的阻塞算一次、解除後的 queue wait 從解除起算、`SIMULATION_DEADLOCK` 不重試、回報哪個 replication 取決於排程 | SIMULATION_SPEC §5 新增「有限 buffer 成員的計數細節」一段（含給 UI 的說明）；§9 的 typed error 列表加 `*engine.DeadlockError`；屬語意選擇的兩項列為 D-064 (e)、(f)（PROPOSED），另兩項為 (h) 的說明 | — | — |
| I3 | B1 只偵測全域的循環等待：循環旁有一直服務中的不相關 process 時會停到 horizon（只在 R1 被繞過時） | 報告與 TEST_PLAN 改為「依全域定義不漏報」；SIMULATION_SPEC §3 寫明；D-064 (g) 給 owner 的說明（要偵測局部循環等待需另一個 decision）。沒有加釘住這個行為的測試（它是定義的限制，不是要保護的行為） | — | — |
| I5 | race detector 下 engine package 約 607 s，幾乎都在 50,000 個模型的 differential，而 kernel 本身沒有並行 | 只在 race build 下把 frozen-kernel differential 降為前 5,000 個 seed（3,632 個 compile；結局 visits 292、active 234、none 3,106）；`make backend-test` 仍跑 50,000 個（36,643；3,190／2,380／31,073）。結局的下限（100、100、1,000）是絕對數，不變；compile 比例仍是「跑的模型的 1/4」。**偏離指示的地方**：指示要「`race` build tag 的常數」，2960fb1 照做，但 `make backend-test` 中 `TestRepositoryConforms` 失敗：architecture checker 的 every-file-built（T-042）拒絕只有 race tag 才編譯的 `race_test.go`（matrix 的 16 種設定都不帶 race，`backend-test-run1-race-tag.log`）。a2afc08 改為讀 test binary 的 build setting `-race`（`runtime/debug.ReadBuildInfo`；沒有 build information 時跑全部），效果相同，不需要改 checker | `backend-race-run1-timeout.log`（第 0 輪）與 reviewer 的 607 s | 見下表的 backend-race |

### Reviewer 的 mutant 重跑

以 reviewer 的 `review_mutants.py`（唯讀，輸出寫到 scratchpad）在本 branch 上只重跑第 1 輪存活的 9 個（`review-mutants-rerun.log`；修正前的結果是 reviewer 的 `review-mutants-before.log`）：

| Mutant | 第 1 輪 | 本輪 |
|---|---|---|
| `deadlock-earliest-by-since` | SURVIVED | KILLED（`TestDeadlockNamesTheEntityBlockedFirst`） |
| `block-order-not-incremented` | SURVIVED | KILLED（同上） |
| `deadlock-bound-ge` | SURVIVED | SURVIVED：**equivalent**。`after()` 只在沒有待執行的 dispatch 時檢查；`release()` 會喚醒每個被釋放的 resource FIFO 最前面的 request 並排 dispatch，所以那時每個 held candidate 都至少有一個 resource 放不下（`inUse + units > capacity`），`>` 與 `>=` 的結果相同。與第 0 輪的 `deadlock-ignores-fitting-held` 同一個理由 |
| `blocks-done-strict-warmup` | SURVIVED | KILLED（`TestBlockingGoldenResults`，G-B4W） |
| `blocks-started-strict-warmup` | SURVIVED | KILLED（同上） |
| `held-ignores-units` | SURVIVED | KILLED（同上，G-B10） |
| `risk-equal-capacity-medium` | SURVIVED | KILLED（`TestBufferCapacityRisk`） |
| `canblock-skips-failure-rework` | SURVIVED | KILLED（`TestBlockingGoldenResults`，G-B11） |
| `canblock-through-delay` | SURVIVED | KILLED（同上，G-B12） |

指示說「除了兩個 equivalent 之外都要被擋下」：本輪 8 個被擋下，只剩 `deadlock-bound-ge` 一個 equivalent；`deadlock-earliest-by-since` 也被 G-D3（兩個阻塞同一 tick 開始）擋下，所以不必當成 equivalent。第 0 輪自己的 62 個 mutant 沒有重跑（production code 沒有改，測試只有增加）。

### 實際執行（本輪）

Go 1.27.2、Python 3.13.16（SimPy 4.1.2）、Node 22.22.0、4 CPU（與其他 session 共用），沒有 PostgreSQL；Go 測試都以 `-count=1`。Exit code 在 `exit-codes.txt`。

| 指令 | Exit | 說明 |
|---|---:|---|
| `make simulation-reference-test PYTHON=.venv/bin/python`（L1 修正前） | 2 | `l1-simpy-red.log`：腳本接受 G-B13 |
| `make simulation-reference-test PYTHON=.venv/bin/python`（L1 修正後） | 0 | `l1-simpy-green.log` |
| `review_mutants.py`（reviewer 的 9 個存活 mutant） | 0 | `review-mutants-rerun.log`：8 KILLED、1 equivalent |
| `go test -fuzz`：FuzzBlocking 90 秒、FuzzKernel 60 秒、FuzzRun 60 秒 | 0 | `fuzz-*.log`：沒有發現失敗；coordinator 顯示 68,040、341,235、46 execs（會少算，見第 0 輪「Fuzz」）；seed 含新 case 的模型 |
| `make spec-check PYTHON=.venv/bin/python` | 0 | `spec-check.log`；重產的 `checks/*.json` 沒有變化（D-064 仍是唯一的 T-047 proposed decision） |
| `make backend-test GOFLAGS=-count=1`（2960fb1） | 2 | `backend-test-run1-race-tag.log`：`TestRepositoryConforms` 的 every-file-built 拒絕 `race_test.go` |
| `make backend-test GOFLAGS=-count=1` | 0 | `backend-test.log`：61 個 package ok，沒有 `(cached)`；engine 52 s、check-architecture 190 s；DB-backed 測試 skip（NOT RUN） |
| `make backend-race GOFLAGS=-count=1` | 0 | `backend-race.log`：61 個 package ok，沒有 `(cached)`；engine package 308 s（與其他 package 並行，check-architecture 314 s 最久）。另單獨執行 engine package（`engine-race-alone.log`）：174 s，其中 differential 28 s（5,000 個模型）、`TestNeutralBuffersChangeNothing` 67 s、`TestRandomBlockingModelsAgainstSpec` 38 s、`TestRandomCircularWaitsAreDetected` 21 s；第 0 輪 race 下 engine 為 356 s，reviewer 量到 607 s |
| `make architecture-check` | 0 | `architecture-check.log`：74 個 package、16 條規則、16 種 build configuration |
| `make ai-eval` | 0 | `ai-eval.log`：`TestFakeProviderEval`、`TestReviewEval` PASS；`checks/ai-evals/` 沒有變化 |
| `make simulation-reference-test PYTHON=.venv/bin/python` | 0 | `simulation-reference-test.log`、`simpy-reference-report.json`：確定性 G-B1、G-B1W、G-B2、G-B4、G-B4W、G-B7、G-B8、G-B9、G-B11、G-B12 與 G-D2（示意）；G-B13 被拒絕；45／45 |
| DB 測試、`make integration-test` | — | NOT RUN（coordinator 執行） |

### NOT RUN（本輪）

- DB 測試與 `make integration-test`：交給 coordinator。本輪沒有 migration、沒有改 production code；受影響的只有測試：`internal/runs/runner` 的 `TestEngineFailures`、`internal/runs` 的 `TestDeadlockError`（都不需要 DB，已在 `backend-test` 中通過）。
- `make frontend-test`：本輪沒有改 schema、TS 或 `apps/web`，沒有執行（Node 22.22.0，沒有 `node_modules`）。
- 第 0 輪自己的 mutation harness（62 個）沒有重跑。
- CI 上的 `make backend-race` 與 `make simulation-reference-test`：只在本機執行。

### 需要 owner 決定（本輪的變化）

D-064 仍是 PROPOSED，本輪補上：(d) 的「最早」是被擋的處理順序（G-D3）；(e) 長度 0 的阻塞算一次（建議 A）；(f) 解除後的 queue wait 從解除起算（建議 A）；(g)、(h) 為說明（全域的 B1、`SIMULATION_DEADLOCK` 不重試與回報哪個 replication）。D-065、D-066 仍未使用。

### 殘餘風險（本輪的變化）

- 新的六個 golden 與 reviewer 的值一致，但期望值仍是同一個 agent 依文字手推；reviewer 已獨立重推第 0 輪的主要 case，本輪的 G-B4W、G-B10、G-B11、G-B12、G-B13 是 reviewer 先給出的情境與數值，我另外重推並相符，G-D3 只有我的推導。
- `deadlock-bound-ge` 與第 0 輪的 `deadlock-ignores-fitting-held`、`enqueue-capacity-unchecked` 一樣是 invariant 的分支；正確的 kernel 走不到，所以沒有測試能直接檢查它們。
- race build 下的 differential 只跑 5,000 個模型；完整的 50,000 個只在非 race 的 build 中跑（`make backend-test`、CI 的 backend job）。判斷 race build 依賴 test binary 的 build information（Go 1.18 起提供）；沒有時跑全部，只會變慢、不會變弱。
- B1 的全域定義（D-064 (g)）：R1 被繞過的模型中，局部的循環等待會停到 horizon；valid 的模型因 R1 不會有循環阻塞。

### Coordinator 的執行（第 1 輪修正，9e62311）

Coordinator 把第 1 輪的六個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（T-017 第 2 輪的 6ed6b71 之後），沒有衝突；本節之前的 commit 表改為整合分支上的 hash。I5 的偏離（以 test binary 的 `-race` build setting 取代 build tag）由 coordinator 接受：效果與指示相同，也不必放寬 architecture checker 的 every-file-built。Coordinator 另以 `-v` 確認兩種 build 的件數：`go test -race -run '^TestDifferentialAgainstFrozenKernel$'` 為 5,000 個中 3,632 個 compile（race detector: true；結局 visits 292、active 234、none 3,106），不帶 `-race` 為 50,000 個中 36,643 個（race detector: false；3,190／2,380／31,073）。

之後 `spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`frontend-test`、`simulation-reference-test`（確定性 case 與 45 個隨機比對都 ok）、`backend-race`（engine package 185 s）與新的 `dt_test_t047r1` 上的 `integration-test`（所有 migration 與 SQL suite，30 個 package ok，沒有 FAIL 或 SKIP）都是 exit 0。本輪的 NOT RUN 中的 DB 測試因此已執行；`make e2e-test` 與第 0 輪的 62 個 mutant 的重跑仍為 NOT RUN。Log 與 exit code 在 `checks/evidence/2026-10-20-T-047-r1/coordinator-*`。

## Review 第 2 輪（H-003，2026-10-25）：ACCEPT

同一位獨立 reviewer 在整合後的 branch 上複審第 1 輪的修正，結論是 **ACCEPT**，另有一個 Low（L1′）與 Info。Reviewer 重跑自己的 14 個 mutant，13 個被擋下，`deadlock-bound-ge` 存活（第 1 輪已說明為 equivalent）。這只是程式審查的結論；D-064 仍是 PROPOSED，`approved_by`／`approved_at` 為 null，本輪沒有批准任何東西，也沒有改 `planning/tasks.json` 或決策登記。修正在 branch `t047-r2`（從 ff11908 開出，沒有 push），evidence 在 `checks/evidence/2026-10-25-T-047-r2/`。本輪沒有改任何 production 的 Go code，只改 SimPy 腳本、測試、golden、Makefile 與文件。

### Commit

| Commit | 內容 |
|---|---|
| 1f9a909 | G-B14（reviewer 的 N7）：手推的 engine golden，加入 engine 的 golden 清單、Result golden（至少 14 個 case）、SimPy 比對與兩個 regression lock（既有的值都沒變） |
| 0638a0d | L1′：SimPy 腳本的自己的 resource 依 capacity 由小到大取得；docstring 與 SIMULATION_SPEC §10 寫明腳本接受哪些模型與理由；red／green log |
| 4cb708d | I2：`make backend-test` 設 `GEMINUS_FULL_DIFFERENTIAL=1`，differential 在少於 50,000 個模型時失敗；mutant 的 red log |
| （本節的 commit） | 本節、報告中的更正、evidence |

### 各項

| 項目 | 內容 | 處理 | Red | Green |
|---|---|---|---|---|
| L1′（Low） | 第 1 輪的拒絕條件只在 bundle 有共用 resource 時比較 capacity。只有自己的 resource、且依 ID 取得時 capacity 遞減的 bundle 仍被接受，但讀法與 engine 的 all-or-nothing 不同。Reviewer 的 N7：X 需要 O1（capacity 2）與 O2（capacity 1），engine 中 O1 in_use 1,200,000（utilization 1/3），腳本讓 x2 先取 O1 再等 O2，O1 in_use 29 分（0.483） | 依 coordinator 的決定：共用的之後，自己的 resource 依 capacity 由小到大取得（同 capacity 依 ID），保留「自己的 ≥ 共用的」的拒絕。理由（寫在腳本的註解與 docstring、SIMULATION_SPEC §10）：一起釋放、依序取得時，持有某個 resource 的 unit 的 entity 一定也持有排在它前面的每個 resource 的 unit；某個 entity 持有前一個 R、要下一個 N 時，至多 capacity(R)−1 個其他 entity 持有 N，capacity(N) ≥ capacity(R) 時 N 一定有空。所以第一個取得之後其他都立即取得，整個 bundle 在第一個 resource（共用的，或沒有共用時最小的自己的）對 FIFO 最前面的 request 有空 unit 時取得，正是 engine 授予的時刻。G-B14 先手推再進 golden（推導寫在 fixture 的註解）：x1 X [1,11]；x2 在 2 被 held，不取任何 resource，O1 的第二個 unit 閒置；11 時授予 x2，X [11,21]；完成 11、21；沒有阻塞；lead time 10、19；qX 的 queue wait 0、9；utilization O1 20/60、O2 20/30；`blocking` 只有 sx（全 0）；qX full_ticks 0；O1、O2 各 in_use 1,200,000、blocked 0、processing 1,200,000，與 coordinator 的推導相同 | `l1p-simpy-red.log`：腳本修正前，G-B14 的 O1 utilization 0.48333 ≠ 1/3、in_use 1,740,000 ≠ 1,200,000，exit 2 | `l1p-simpy-green.log`：G-B14 比對通過、G-B13 照樣被拒絕、隨機 45/45，exit 0 |
| I1（Info） | `deadlock-bound-ge` 存活 | 依第 1 輪的說明視為 equivalent，reviewer 接受。Reviewer 對 T-048／T-049 的提醒由 coordinator 記錄，本輪不改 | — | — |
| I2（Info） | 沒有任何 assertion 確認非 race build 跑完整件數：`raceEnabled` 判斷錯時，`make backend-test` 會默默只跑 5,000 個模型 | `make backend-test` 設 `GEMINUS_FULL_DIFFERENTIAL=1`（`make backend-race` 不設），`TestDifferentialAgainstFrozenKernel` 在設了這個變數、且要跑的件數少於 50,000 時失敗。CI 的 backend job 執行 `make backend-test`（`.github/workflows/ci.yml`），所以 CI 也有這個檢查。以兩個 mutant 證明：`frozenDifferentialModels` 改用 `!raceEnabled`，以及沒有 `-race` build setting 時當成 race build | `i2-mutant-before.log`：加入檢查前，兩個 mutant 在一般 build 下都通過，只跑 5,000 個模型（3,632 個 compile）。`i2-red-inverted-backend-test.log`、`i2-red-default-true-backend-test.log`：加入檢查後，以 `-overlay` 套用 mutant 的 `make backend-test` 都 exit 2，60 個 package ok，只有 engine 的 `TestDifferentialAgainstFrozenKernel` 失敗（`GEMINUS_FULL_DIFFERENTIAL=1 (make backend-test) but 5000 of 50000 models would run`） | `backend-test.log`：沒有 mutant 時 exit 0；`simulation-race.log`：race build 不設這個變數，照樣只跑 5,000 個並通過 |
| I3、I4（Info） | Coordinator 轉來的指示只列出編號，沒有附內容，也沒有要求修改 | 本輪沒有修改；內容以 reviewer 的報告為準，本報告不轉述 | — | — |

### 實際執行（本輪）

Go 1.27.2、Python 3.13.16（SimPy 4.1.2）、4 CPU（與其他 session 共用），沒有 PostgreSQL；Go 測試都以 `-count=1`。Exit code 在 `exit-codes.txt`。

| 指令 | Exit | 說明 |
|---|---:|---|
| `make simulation-reference-test PYTHON=.venv/bin/python`（L1′ 修正前） | 2 | `l1p-simpy-red.log`：G-B14 的 O1 不符 |
| `make simulation-reference-test PYTHON=.venv/bin/python`（L1′ 修正後） | 0 | `l1p-simpy-green.log` |
| `go test -run TestDifferentialAgainstFrozenKernel`，兩個 race-detection mutant，加入檢查前 | 0、0 | `i2-mutant-before.log`：兩者都只跑 5,000 個模型並通過（缺口） |
| `make backend-test`，`-overlay` 套用 mutant `inverted` | 2 | `i2-red-inverted-backend-test.log` |
| `make backend-test`，`-overlay` 套用 mutant `default-true` | 2 | `i2-red-default-true-backend-test.log` |
| `make spec-check PYTHON=.venv/bin/python` | 0 | `spec-check.log`；重產的 `checks/*.json` 沒有變化 |
| `make backend-test GOFLAGS=-count=1` | 0 | `backend-test.log`：61 個 package ok，沒有 `(cached)`；engine 53 s（跑完 50,000 個）、check-architecture 120 s；DB-backed 測試 skip（NOT RUN） |
| `make architecture-check` | 0 | `architecture-check.log`：74 個 package、16 條規則、16 種 build configuration |
| `make simulation-reference-test PYTHON=.venv/bin/python` | 0 | `simulation-reference-test.log`、`simpy-reference-report.json`：確定性 G-B1、G-B1W、G-B2、G-B4、G-B4W、G-B7、G-B8、G-B9、G-B11、G-B12、G-B14 與 G-D2（示意）；G-B13 被拒絕；45/45 |
| `go test -race -count=1 -timeout 30m ./internal/simulation/...` | 0 | `simulation-race.log`：4 個 package ok；engine 147 s、des 44 s |

### NOT RUN（本輪）

- **DB 測試與 `make integration-test`**（`make db-test`、`make db-go-test`）：這裡沒有 PostgreSQL，交給 coordinator 執行。本輪沒有 migration，也沒有改 production code，DB-backed 的測試（例如 `internal/runs/runner` 的 `TestEventBudgetAcrossRetries`、`TestRefusedAttempts`，`internal/api` 的 `TestRunClosedLoop`、`TestRunRoleMatrix`、`TestRunRequestRefusals`、`TestRunIdempotency`、`TestARevokedMemberGetsNoReplay`）在本 worktree 中都是 skip。
- 完整的 `make backend-race`：只依指示跑了 `./internal/simulation/...` 的 race。
- `make ai-eval`、`make frontend-test`：本輪沒有改 AI context、prompt、schema、TS 或 `apps/web`，沒有執行。
- Reviewer 的 mutant 與第 0 輪自己的 62 個 mutant：本輪沒有重跑（production code 沒有改；本輪的兩個 race-detection mutant 見上表）。
- CI：`GEMINUS_FULL_DIFFERENTIAL` 的檢查只在本機的 `make backend-test` 中執行過，沒有在 GitHub Actions 上跑過。

### 殘餘風險（本輪的變化）

- SimPy reference 現在接受的模型：units 1、每個 bundle 至多一個共用 resource、有共用時自己的每個 resource 的 capacity 不小於共用的；自己的 resource 依 capacity 由小到大取得。這個等價是對 units 1 的論證；units > 1 仍一律拒絕，G-B10 這類模型只有手推 golden。
- G-B14 的期望值是我與 coordinator 各自推導、結果相同，reviewer 的 N7 也給出相同的 engine 輸出；它仍不是由第三方獨立實作產生的。
- `GEMINUS_FULL_DIFFERENTIAL` 依賴 Makefile：直接執行 `go test ./internal/simulation/engine/` 不設這個變數，此時件數只由 `raceEnabled` 決定（一般 build 為 50,000）。
- I3、I4 的內容我沒有看到，本節沒有對它們做任何判斷。

### Coordinator 的執行（第 2 輪修正，76c3521）

Coordinator 把第 2 輪的四個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（c80dd75 之後，沒有衝突）：1f9a909 → 3b5f063、0638a0d → 541f98f、4cb708d → c29bf10、5b25aaa → 76c3521。Reviewer 第 2 輪的 I3（SimPy harness 的「refused case 不檢查」mutant 不會失敗，屬測試的測試）與 I4（報告與文件誠實、與程式一致）不需要修改；I1（`deadlock-bound-ge` 為 equivalent）的提醒已記入 T-048、T-049 的 scope（c80dd75）。Reviewer 的判定為 ACCEPT，第 2 輪的修正（一個 Low、一個 Info）依 T-026 的前例由 coordinator 驗證後接受，不另開一輪 review。

之後 `spec-check`、`backend-test`（含 `GEMINUS_FULL_DIFFERENTIAL=1` 的斷言）、`architecture-check`、`ai-eval`、`frontend-test`、`simulation-reference-test`（確定性 case 含 G-B14，G-B13 被拒絕，45/45）、`backend-race`（engine package 190 s）與新的 `dt_test_t047r2` 上的 `integration-test`（所有 migration 與 SQL suite，30 個 package ok，沒有 FAIL 或 SKIP）都是 exit 0（`checks/evidence/2026-10-25-T-047-r2/coordinator-*`）。D-064 仍是 PROPOSED，它的 gate 是 T-047 驗收前，所以 T-047 記為 in_progress（`planning/tasks.json` 的 completion_evidence），owner 批准後才標為 done。CI 上的 `GEMINUS_FULL_DIFFERENTIAL` 檢查在本次 push 後第一次執行。

## Owner 批准（H-021，2026-10-25）

Owner 對決策頁（https://claude.ai/artifact/T1xUsmk9ToV274qsPVRKqm）回覆「全都照建議」：D-064 依建議 APPROVED（`planning/decisions.json`、`docs/DECISIONS_AND_TBD.md`）。本報告前文寫作時這些決策仍是 PROPOSED，前文保留原貌；T-047 因此標為 done。
