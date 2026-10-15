# T-021 驗證報告：DES resource／queue／service／routing kernel

**日期：2026-10-15｜狀態：`review`（review 修正已完成；CI 與 reviewer 複核後依 H-003 驗收）｜需求：REQ-011、REQ-013｜Gate：D-005、D-007、D-023、D-032（皆已批准）；實作解讀併入 D-038（PROPOSED，gate 在 T-023）**

首輪輸出在 `checks/evidence/2026-10-15-T-021/`，review 修正後的輸出在 `checks/evidence/2026-10-15-T-021-review/`。

> **更正（2026-10-15 獨立 review 後）**：首版報告有幾處說得太滿，已在下文改正，原句列在「首版說法更正」。

## 交付內容（`internal/simulation/engine`）

| 檔案 | 內容 |
|---|---|
| `kernel.go` | `RunReplication(ctx, program, index, budget, observer)`：在 T-020 的 replication clock 上加入 queue、resource、service、delay、decision、failure／rework、sink。Resource-wise FIFO dispatcher（見下）；SERVICE_COMPLETE（priority 0）原子釋放整個 bundle 後在同一事件內路由；dispatch 在 priority 20，零時間 service 在 dispatch 後同 tick 完成時會再 dispatch 一次。Typed 失敗：`ErrActiveEntities`、`ErrEntityVisits`（帶 entity、node、tick）、T-020 的 `ErrEventBudget`、context 錯誤、`ErrInternal`（含 replication index 超出 program）。以 `Observation` 回報事實，指標彙整屬 T-022 |
| `replication.go` | entity 紀錄只保留系統內的 entity，到達 sink 即釋放；`EntityID` 對已離開的 entity 回 ""；`FormatEntityID` 給只看得到 source 與序號的 observer |
| `kernel_test.go`、`kernel_check_test.go` | 見下方對照。`kernel_check_test.go` 是從獨立 review 移植的 observation-only 模型：只從 Observation 重建等待、資源與 entity 狀態，逐次 dispatch 用規格原文的規則比對 |
| `testdata/simulation/kernel-observation-digests.json` | 兩個隨機 run 的完整 observation stream SHA-256（kernel 輸出的 regression lock，不是獨立 oracle） |
| `docs/SIMULATION_SPEC.md` §3 | kernel 的實作段落 |

**Dispatcher**：每個 resource 有一個 FIFO，保存需要它的等待 request。依 resource-wise FIFO 規則，同一 resource 的 request 彼此依序授予，所以只有「在自己每個 resource 的 FIFO 都排第一」的 request（candidate）可能被授予。dispatcher 依 sequence 檢查 candidates，bundle 都夠才授予，否則它留在最前面擋住共用這些 resources 的後來者；授予後新排到最前面、成為 candidate 的 request 加入檢查。決定順序與逐筆掃描完全相同（由 observation-only 模型逐次比對），每次 dispatch 的成本與 resource 數成正比，不隨等待數增加。

## 獨立 review 結果與處理

Review 由另一個 agent 以對抗方式進行（H-003），未修改 repo。它以自己的 observation-only 模型跑了 4,394 個可編譯的隨機模型，沒有發現 kernel 的正確性錯誤；以下是要求修改的項目。

| # | 發現 | 等級 | 處理 | 紅→綠證據 |
|---|---|---|---|---|
| D1 | 每次 dispatch 重掃所有等待 request，成本 O(等待數×bundle)，event budget 限制不了 CPU（review 實測 1M events：90,000 等待時 130.9 µs/event，20,000 等待×100 resources 時 530.2 µs/event） | 中 | 改為上述 candidate dispatcher | `TestDispatchWorkIsBounded`：舊版檢查了 103,501,501 個 request（110,999 events），新版不超過 4×events；同一 cost probe 在新版為 0.26–0.35 µs/event，與等待數、bundle 大小無關（`dispatch-cost-before.log`／`dispatch-cost-after.log`） |
| D2 | 9 個以上有意義的 mutant 存活：多 unit 容量、多 unit FIFO、bundle 擋住、顯式 queue 路由、同 tick 再 dispatch、零時間步驟下的 D-032 順序、RunReplication 的 ctx／budget、failure 的 visit、Events 計數 | 中 | 補 `TestMultiUnitFIFO`、`TestZeroTimeStepsKeepD032Order`、`TestSameTickRedispatch`、`TestQueueNodeRoutingAndVisits`、`TestFailureAndReworkAreVisits`、`TestRunReplicationCheckpoints`、`TestHorizonEqualityInKernel`，並移植 observation-only 模型（`TestRandomModelsAgainstSpec`，300 個隨機模型，含多 unit bundle、queue 與直通同一 buffer、零時間 service／delay） | mutation 43/43＋2 equivalent（含 review 的全部存活類別） |
| D3 | 同一次 dispatch 的多個授予在全部套用後才回報 in-use（容量 3、三個授予都報 3） | 低 | 每次授予先回報新的 in-use 再開始 service | 移植的模型與 `TestGrantObservationsStepwise` 在舊版失敗（報 3,3,3，應為 1,2,3），新版通過 |
| D4 | 報告說「再 dispatch」與「授予順序」已併入 D-038，但 register 沒有 | 低 | D-038 補 (j)–(m)：同 tick 再 dispatch、授予順序與逐筆回報、經過 queue 的 visit、FIFO 擋住整個 bundle | — |
| P1 | 經過顯式 queue 時 visit 計數與直通相同，未記錄也未測試 | 待決 | 改為依規格原文「每進入一個 node 加 1」：queue 與其 process 各算一次；登記 D-038 (l) | `TestQueueNodeRoutingAndVisits`：舊版 src→q→work→end 在上限 3 時通過，新版需要 4 |
| P2 | 被 FIFO 擋住的 request 擋住其 bundle 內所有 resources，可能讓 resource 閒置 | 待決 | 這是已批准文字的直接結果，不改；寫入 SIMULATION_SPEC §3 並登記 D-038 (m) 供 owner 知悉 | — |
| P3 | 隨機 kernel 輸出沒有凍結的 golden vector | 待決 | 新增 `kernel-observation-digests.json`（default-delivery 與 report-example 的完整 observation stream；標明為 regression lock） | `TestKernelObservationDigests` |
| P4 | limit 錯誤沒有位置資訊 | 待決 | 錯誤帶 entity ID、node、tick（`errors.Is` 不變） | `TestKernelEdges` 舊版失敗 |
| P5 | `EntityID` 對已離開的 entity 會 nil dereference | 待決 | 回 "" | `TestKernelEdges` 舊版 panic |
| P6 | RunReplication 不檢查 index < replications | 待決 | 回 `ErrInternal`；測試同步宣告足夠的 replications | `TestKernelEdges` |

## 實際執行結果（review 修正後）

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test` / `backend-race` / `backend-build` | 0 / 0 / 0 | |
| `REQUIRE_GOAMD64_V3=1 make backend-numeric` | 0 | |
| `go test -count=1 -v ./internal/simulation/engine/` | 0 | 120 個 RUN（Test、subtest 與 10 個 fuzz seed），全部 PASS（`engine-tests.log`） |
| `TestRandomModelsAgainstSpec` 以 overlay 放大到 3,000 個模型 | 0 | 2,225 個可編譯；142,249 次授予、133,398 次 dispatch 全部與規格原文一致，最多 206 個同時等待 |
| Fuzz `FuzzKernel` 60 s | 0 | 1,062,442 次 |
| Kernel mutation（`checks/evidence/2026-10-15-T-021/kernel-mutants.py`，已更新） | 0 | **43/43 被擋，2 個 equivalent**（`record-invariant-unchecked`、`fifo-pop-unchecked`：不變量檢查，只有另一個 bug 能觸發；腳本中說明） |
| T-020 engine mutation | 0 | 51/51＋1 equivalent |
| GitHub Actions | 推送後補記 | 首版 19540b6 的 run 38786963032 為 4/4 成功 |

## 驗收對照

| 測試 | 驗證內容 | 對應 |
|---|---|---|
| `TestGoldenCases` | GOLDEN-001／002 與 `expected-cases.json` 手算值相差 ≤1e-12（相對） | TEST-015、TEST-016 |
| `TestRandomModelsAgainstSpec`（observation-only 模型） | 每次 dispatch 授予的恰為規格原文規則的結果、tick 結束時沒有可授予卻在等的 request、回報的 in-use 逐筆與重建一致且在 [0, capacity]、每個事件後 entity 恰處一個狀態、時間不倒退、Created＝Exited＋Censored；同一 replication 並行執行結果相同 | TEST-018、TEST-019（kernel 部分） |
| `TestBundleAndResourceWiseFIFO`、`TestMultiUnitFIFO`、`TestGrantsStartInRequestOrder`、`TestGrantObservationsStepwise` | bundle 不部分佔用；多 unit 時 FIFO 擋住後來者；不相干者照常；授予依 request 順序並逐筆回報 | TEST-018 |
| `TestSameTickReleaseBeforeArrival`、`TestDelayCompletesAfterServiceInATick`、`TestZeroTimeStepsKeepD032Order`、`TestSameTickRedispatch` | D-032：priority 0／10／20 的先後，含零時間步驟；零時間 service 後同 tick 再 dispatch | TEST-018 |
| `TestRoutingDelayObservations`、`TestRoutingTolerance`、`TestQueueNodeRoutingAndVisits` | 100%／0% routing、固定 seed 分流、delay、failure／rework、abandoned sink、無 resource 的 process、decision 浮點兜底、顯式 queue 進入 process 的 buffer | TEST-016 |
| `TestCensoring`、`TestHorizonEqualityInKernel` | horizon 時仍在系統內者為 censored；完成恰在 horizon 時計入，少 1 ms 則 censored | TEST-017（kernel 部分；warmup 屬 T-022） |
| `TestKernelLimits`、`TestKernelLimitsAreExact`、`TestFailureAndReworkAreVisits`、`TestRunReplicationCheckpoints`、`TestKernelEdges` | 上限恰好可過、少 1 失敗並帶位置；RunReplication 內取消回 context 錯誤；小 budget 失敗；Events 等於 budget 使用量；index 超出 program 回錯 | TEST-021（limits、checkpoints 部分） |
| `TestEntityRecordsAreReleased`、`TestDispatchWorkIsBounded` | 記憶體受 active entity 限制；dispatch 工作量不隨等待數增加 | T-020 review P2、T-021 review D1 |
| `TestKernelDeterminism`、`TestStreamsUsedByPurpose`、`TestKernelObservationDigests` | 決定性、各用途的 stream、完整 observation stream 的 regression lock | TEST-019（kernel 部分） |

recorder（`newRecorder`）只用在有傳入它的測試；`TestKernelLimits*`、`TestEntityRecordsAreReleased` 等以 nil observer 執行，狀態不變量由 `TestRandomModelsAgainstSpec` 的 observation-only 模型涵蓋。

## 需 owner 知悉（D-038，PROPOSED）

1. sink 沒寫 outcome 時視為 completed（CB-22）。
2. 零時間 service 在 dispatch 後同 tick 完成時再 dispatch 一次，因此 dispatch 不一定是該 tick 最後一個事件（D-032 原文寫「為該 tick 最後一步」）。
3. 同一次 dispatch 依 request 順序授予，每次授予先回報新的 in-use。
4. 經過顯式 queue 時 queue 與其 process 各算一次 visit。
5. 被 FIFO 擋住的 request 擋住其 bundle 內所有 resources，可能讓部分 resource 閒置（已批准規則的直接結果）。

## 首版說法更正

| 首版 | 更正 |
|---|---|
| 「recorder 不變量（每個測試）」 | 只在傳入 recorder 的測試；recorder 也只檢查 tick 不倒退（seq 在同 tick 不同 priority 間可變小）。完整的狀態不變量現由 observation-only 模型檢查 |
| 「Mutation 28/28」 | 該清單正確但漏掉 D2 的類別；現為 43/43＋2 equivalent |
| TEST-018「capacity bounds 永不破」 | 首版沒有多 unit 的測試；現有 `TestMultiUnitFIFO` 與隨機模型的 1–3 unit bundle |
| TEST-017 | 首版只有 censoring；現補 horizon 恰為完成時間的案例 |
| NOT RUN 清單 | 漏列 RunReplication 層的 context checkpoint；現已測試（`TestRunReplicationCheckpoints`） |
| 「98 個 test／subtest PASS」 | 那是 Test* 的數目；`go test -v` 另有 fuzz seed 的 RUN。現為 120 個 RUN 全 PASS |
| 「再 dispatch、授予順序併入 D-038」 | 首版沒有寫進 register；現已補 |
| T-020 報告的 TEST-017／TEST-019 | 兩者對調，已在 `checks/T-020_VALIDATION.md` 更正 |

## 與規劃的差異

- 規劃輸出 `internal/simulation/runtime/` 未建立：kernel 放在 `internal/simulation/engine/`，因為它直接使用 replication 與 `Program` 的未匯出結構（同 T-020 的 compile）。
- `ReplicationOutcome.Completed` 改名為 `Exited`，避免與「completed sink」指標混淆。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| 指標彙整、warmup cohort、CI、result digest、trace | T-022 |
| wall timeout、跨 replication 的平行 pool、progress | T-022（engine Run）／T-023 |
| queued 立即取消、cancel-vs-complete 互斥、lease | T-023 |
| SimPy differential、固定硬體的效能 benchmark | T-037 |
| 實體非 amd64 機器上的測試 | 本環境只有 amd64；以組語檢查與 GOAMD64=v3 替代 |
| GitHub Actions | 推送後補記 |
