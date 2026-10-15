# T-022 驗證報告：Metric aggregation、result digest、sampled trace、engine Run

**日期：2026-10-15｜狀態：`done`（獨立 review 要求修改→修正→同一 reviewer 複核 ACCEPT，後續項目已修正；依 H-003 由 agent review 驗收，owner 可隨時重開）｜需求：REQ-011、REQ-013、REQ-015、REQ-017｜Gate：D-006、D-010（皆已批准）；新增的實作語意登記為 D-039（PROPOSED，gate 在 T-023）**

首版的逐條輸出在 `checks/evidence/2026-10-15-T-022/`，review 修正後的輸出在 `checks/evidence/2026-10-15-T-022-review/`，複核後續項目的輸出在其 `rereview/`。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/results/metrics` | `Collector`（engine Observer，每個 replication 一個；replication 結束時呼叫 `Finish` 釋放 entity 狀態）與 `Build`：measurement window [W,H]、throughput（completed sink，replication population）、lead time（arrival≥W 且 completion≤H 的 cohort，pooled entity population，無 CI）、`additional_metrics.replication_mean_lead_time_minutes`（replication population，帶 Student-t CI；只要有一個 replication 沒有 cohort 即 unavailable）、queue wait（queue_enter≥W 且 start≤H 的 visit，pooled）、queue_length_mean 與 utilization（[W,H] 的時間積分，含跨邊界的部分區間，再取 replication 平均）、failure／rework count、censored。Type 7 quantile；t 臨界值為 scipy 產生的常數表（`ttable.go`，`scripts/gen_t_table.py`）。所有乘積明確捨入（D-038 numerical profile） |
| `internal/simulation/trace` | `Recorder`：replication 0 中 entity ID 的 SHA-256 落在 1/8 以下者，依 engine 處理順序記錄 arrival／queue_enter（實際進入的 node）／service／delay／failure／rework／complete／abandon，到 max_trace_events 截斷並標 truncated |
| `internal/simulation/des` | `Engine`（實作 `simulation.Engine`）：Compile → bounded pool（預設 GOMAXPROCS）跑 replications，共用 event budget 與 wall timeout，全部完成才算完成；progress 由獨立 goroutine 非同步送出（只送最新 snapshot，不影響結果與時限）；replication 中的 panic 轉為 `ErrInternal`；typed 失敗不回部分結果；重算 effective_model_hash 並拒絕不一致的凍結值；config／manifest hash；`DomainMetrics` 注入 DORA；`hashing.Result` digest；`RunWithTrace` 另回 trace artifact |
| `internal/simulation/engine` | `Observation.At`：queue_enter 實際進入的 node（顯式 queue 或直通的 process） |
| `schemas/simulation.schema.json` | CB-23：`MetricSummary` 的 unavailable 必有 reason、只有 population=replication 可帶 CI；queue stats 的 unavailable 必有 reason 且 wait 全為 null、available 的 wait 必為數字且 sample_count≥1；resource 的 utilization_mean 為 null 時必有 reason；新增 `TraceArtifact`／`TraceEvent`（`geminus.trace.v1`）。正例 `valid-trace-artifact.json`；反例 8 個（皆比對確切錯誤路徑）：`invalid-unavailable-without-reason.json`、`invalid-entity-population-ci.json`、`invalid-trace-event-type.json`、`invalid-trace-entity-id.json`、`invalid-queue-unavailable-zero-wait.json`、`invalid-queue-unavailable-without-reason.json`、`invalid-queue-available-without-samples.json`、`invalid-resource-null-without-reason.json` |
| `internal/simulation/types.go`、`apps/web/lib/contracts/simulation.ts` | Go 型別（`TraceArtifact` 等，parity 與 round-trip 測試）；`ProgressSink` 的呼叫約定；TS 型別由 `scripts/gen_ts_contracts.py` 重新產生 |
| `RunInput.ModelHash` | pinned version 的 model_hash（ADR-012 補充） |
| `scripts/engine_reference.py` | 新增 trace 選取的獨立參考向量 |
| `testdata/simulation/engine-golden-digests.json` | GOLDEN-001／002 的 result digest（engine 輸出的 regression lock，以 DORA stub 計算，標明不是 oracle、也不是正式 digest） |
| `internal/domainpack/softwaredelivery/engine_test.go` | 真正的 `Pack{}.ComputeMetrics` 經過整個 engine（TEST-023） |
| 文件 | SIMULATION_SPEC §5、§9 實作段落；ADR-012 補充；CONTRACT_BLOCKERS CB-22／23／24 狀態；D-039 (a)–(h) |

## 獨立 review 結果與處理

Review 由另一個 agent 以對抗方式進行（H-003，commit 7f1db4a），未修改 repo。它另寫一個 oracle，從原始 observation 重算每個指標，在 17 個情境與 `Build` 一致；engine Run 的 race 與失敗 probe 全部正確（300 個混合失敗的 `-race` run 皆為 typed error、無部分結果）。要求修改的項目如下，全部先寫失敗測試再修（`red-before-fix.log`、`schema-holes-after.log`）。

| # | 發現 | 等級 | 處理 | 紅→綠證據 |
|---|---|---|---|---|
| 1 | progress sink 在 pool mutex 內被呼叫，sink 的延遲決定結果並讓 wall timeout 失效（review：50 ms 期限、sink 每次睡 1.5 s，6.003 s 後才回且 err=nil；400 ms 期限的 run 沒有 sink 時 16 ms 成功，sink 睡 150 ms 時變成 wall timeout） | 中 | 鎖內只取 snapshot；由獨立 goroutine 經一格「最新值」channel 送出，新的 snapshot 取代尚未送出的舊值；Run 不等待 sink。`ProgressSink` 文件寫明：從單一 goroutine 呼叫、不並行、不被等待、慢的 sink 只看到最新值（計數不倒退），Run 返回後可能仍有一次呼叫在執行。§8／SIMULATION_SPEC Engine 段落與 D-039 (g) 同步 | `TestSlowProgressSinkChangesNothing`：舊版等待 sink 2.45 s 後失敗；新版過期期限 1 s 內回 `ErrWallTimeout`，300 ms 的 sink 下結果與無 sink 相同、run 在 250 ms 內結束、最後一個 snapshot 為 8、呼叫次數 <8 且從不並行。mutant `progress-reported-synchronously`、`progress-never-delivered`、`stale-snapshot-kept` 被擋 |
| 2 | CB-23 只約束 `MetricSummary`；queue 的 unavailable 可帶 wait 0、無 reason，available 可無樣本，resource 的 null utilization 可無 reason；也沒有 engine 測試涵蓋 unavailable queue | 中 | schema 加上述 if/then 規則（附 `$comment`）與 4 個反例；`TestUnavailableQueue`（GOLDEN-002，W=150、H=151） | 三個 review 的 payload 由 valid 變 invalid（`schema-holes-after.log`）；mutant `queue-unavailable-no-reason`、`queue-unavailable-zero-wait`、`queue-unavailable-as-available-zero` 被擋 |
| 3 | 16 個非 equivalent mutant 存活：rework visit 不計、utilization 變 0/1、failure 與 rework 對調（每個 fixture 兩者相等）、跨 replication 平均未檢查、pooled wait 只取第一個 replication、replication-mean 用 pooled、unavailable queue ×3、budget 改為每個 replication、completion 不決定、trace 取最後一個或第 1 個 replication | 中 | 移植 review 的 oracle 為 `TestOracleAgreement`（17 個情境：default-delivery ×2、congested W45/H204、unavailable queue、deterministic W12、12 個 `multi` 模型：兩個 resource 容量 5／2、多 unit bundle、queue 與直通同一 buffer、rework 回到 queue、failure 與 rework 不同比例、delay）；新增 `TestSharedBudget`（各自不超過 budget、合計超過）、`TestCompletionDecides`（最後一個 replication 完成後才取消）、`TestTraceIsReplicationZero`（與只跑 replication 0 的 recorder 比對，workers 1／2／5）；`TestExplicitQueueOwnsMetrics` 改為比對數值 | 16 個存活者全部加入 `results-mutants.py` 並被擋；整體 **68/68＋1 equivalent**（`mutation.log`）。`ci-unrounded-square` 由 FMA guard 擋下（以 `fma` 旗標跑 `check_no_fma.sh`） |
| 4 | TEST-023 不在 traceability 中；engine 測試只用一個 entry 的 DORA stub | 低 | `TestDORAThroughTheEngine`（放在 domain pack，generic engine 不 import domain）：`des.Engine{DomainMetrics: Pack{}.ComputeMetrics}` 跑 default-delivery，五個 DORA 指標皆 unavailable（`MISSING_DORA_EVENT_MAPPING`），同時 failures 與 reworks >0（CI failure 不是 DORA）、Result 通過 schema、digest 與無 DomainMetrics 的 run 不同 | 驗收對照的 TEST-023 列 |
| 5 | Collector 的 `arrived`／`entered` map 保留到 `Build`（review：1000 個 collector × 10k active entity 保留 282 MiB） | 低 | `Collector.Finish()` 釋放兩個 map；`Build` 拒絕未 `Finish` 的 collector（`ErrInconsistent`），讓「忘記呼叫」成為錯誤而非無聲的記憶體成長；engine 在每個 replication 結束時呼叫 | `TestFinishReleasesEntityState`（congested，H=100，有 censored entity）：舊版 Finish 為 no-op 時失敗；mutant `collector-kept`、`finish-keeps-maps` 被擋 |

Review 列為 PLAUSIBLE 或需 owner 決定的項目：

| 項目 | 處理 |
|---|---|
| 直通 process 的 entity 在 trace 中記為 `queue_enter@inbox`（它沒有進入的 queue） | 修正：kernel 在 `Observation.At` 回報實際進入的 node，trace 用它；SIMULATION_SPEC §9 與 D-039 (f) 改為「實際進入的 node」。`TestTraceNamesTheEnteredNode` 舊版失敗（`arrival/8 queued at inbox, entered service`）；mutant `trace-at-explicit-queue`、`entered-node-is-buffer` 被擋 |
| effective hash 有兩個定義（engine 為 `hashing.Model(model.Encode(IR))`，API 為儲存的 bytes） | 新增 contract test `TestEffectiveHashMatchesStoredBytes`：所有 golden model 與 valid IR fixture 兩者相同。T-023 的 run API 需沿用同一個測試 |
| `Engine.Validate` 與註解不符 | 改註解：路徑相對於 IR（Compile 則在 `/model` 下回報同一批問題），dataset binding、manifest、config、limits 由 Run 檢查 |
| pool goroutine 沒有 `recover` | 加 `recover`，轉為 `ErrInternal` 並停止其他 replication；`TestReplicationPanicIsAnError` 舊版使測試 process panic |
| wall timeout 只涵蓋 replication pool（Compile、Build、digest 在外） | 不改，寫入 SIMULATION_SPEC Engine 段落：Build 在 1000 replications、10M events 的上限下約數百 ms（review 實測 290–380 ms），Compile 與 digest 隨 IR 大小成長 |
| golden digest lock 不是正式 digest（以 DORA stub 計算） | 不改，lock 檔已標明；正式 digest 由 T-023 的 worker 以 domain pack 計算 |
| `TraceArtifact` 沒有 seed、config hash 或 result digest 連到它的 run | 移交 T-023（artifact_id 與 run 的關聯在 persistence 層建立） |
| D-039 與 §5 原文不同：n<2 時 CI 為 null 但沒有 reason（§5 寫 null + reason）；任一 replication 沒有 cohort 時整個 replication-mean summary unavailable，而不只是 CI | 不自行決定，見下方「需 owner 知悉」 |

## 複核（2026-10-15，同一 reviewer，commit 4370ab5）

結論 **ACCEPT**，沒有阻擋項。reviewer 在 `git archive` 匯出的副本上：以 7 條返回路徑（完成、過期期限、event budget、active entity、panic、預先取消、hash 不一致）× 0／30 ms sink 驗證 `deliver` 不洩漏 goroutine、報告嚴格遞增且不並行、完成的 run 一定收到 N/N；200 次隨機壓力測試中最後一筆都送達（176 次在 Run 返回後才到，符合文件）；recover 不會留下鎖住的 `mu`；400 個隨機模型（容量 1–6、多 unit、rework、failure、一個 tick 的 window、真正的 DORA pack）的 Result 與 trace 全部通過新 schema 規則；30 個隨機模型的 `queue_enter` 位置全部正確；1000 replications／10M events 的 heap 峰值由 490 MiB 降為 15 MiB；本報告的 mutation 數字逐項重現。兩個 Low 項目與一個時間假設已先紅後綠處理：

| # | 發現 | 處理 | 證據 |
|---|---|---|---|
| L1 | panic 的 `ProgressSink` 會終止 process（reporter goroutine 沒有 recover，且可能在 Run 返回後發生）；`DomainMetrics` 同樣在呼叫端 goroutine 上無 recover | reporter recover 後不再呼叫 sink（`report` 不需要讀取端，會自行取代未讀的 snapshot）；`DomainMetrics` 包在 recover 內，panic 回 `ErrInternal`、不回結果；`ProgressSink` 文件與 SIMULATION_SPEC Engine 段落同步 | `TestPanicsOutsideTheReplications` 舊版使測試 process panic（`rereview/red-before-fix.log`）；mutant `sink-panic-unrecovered`、`domain-metrics-unrecovered` 被擋 |
| L2 | 沒有測試檢查 reporter goroutine 會結束（刪掉 `defer stopReports()` 的 mutant 存活） | `TestSlowProgressSinkChangesNothing` 在最後一筆送達後檢查 goroutine 數回到 run 之前 | mutant `reports-never-stopped` 被擋 |
| P1 | `TestSlowProgressSinkChangesNothing` 斷言 run 在 250 ms 內結束，`-race -cpu=1` 加背景負載下實測可達 404 ms | 改為決定性：sink 的第一次呼叫被擋住，直到 Run 返回後才放行；Run 若等待 sink 就永遠不會返回（30 s 視為失敗）。放行後最多再一次呼叫且為 8/8 | `-count=20 -cpu 1,4` 與 `-race -count=10 -cpu 1,4` 全部通過 |

另：reviewer 的 `report-outside-lock` mutant（在鎖外呼叫 report）會破壞順序保證但取決於排程，測試無法穩定抓到，維持以程式碼結構保證（report 只在 `mu` 內呼叫）；`defer mu.Unlock()` 的建議目前沒有可觸發的路徑，未修改。T-023 須知：progress 寫入要以 run status／lease fence，worker 不能等待或 flush sink。

### 首版說法更正

| 首版 | 更正 |
|---|---|
| 「Mutation 43/43」 | 數字可重現，但 `trace-every-replication` 只被並行存取 map 的 runtime crash 擋下，`utilization-ignores-capacity` 只被 `TestDeterminism` 中 schema 的 maximum 擋下。現在兩者都有 assertion：前者由 `TestTraceIsReplicationZero`（單獨跑，GOMAXPROCS=1，workers 1／2／5 皆報「the trace is not replication 0's」，`trace-every-replication.log`；整套跑時仍可能先 crash），後者由 `TestOracleAgreement` |
| 共用 budget、completion 決定、trace 只取 replication 0、utilization 與 queue length 的 replication 平均、pooled wait、unavailable queue、rework visit | 首版行為正確但沒有測試證明；現由上表第 3 項的測試證明 |
| 「CB-23：unavailable 必有 reason」 | 首版只對 `MetricSummary` 成立；現涵蓋 queue 與 resource |
| `TestExplicitQueueOwnsMetrics`「queue 指標歸顯式 queue」 | 首版只檢查 key 存在；現比對 `inbox` 的數值等於直通模型中 `service` 的數值 |
| TEST-023 | 首版未對應、也未列 NOT RUN；現由 `TestDORAThroughTheEngine` 對應 |
| 「CB-24 已用 engine 實測確認」 | 首版沒有 log、沒寫 seed；現為 `cb24-measurement.log`（seed 7、5 replications，產生它的 scratch test 為 `cb24-measurement_test.go.txt`）。重跑時發現先前的 log 把 lead time 的指標印成指標位址，已改印數值；其餘數字不變 |
| 「43 個 RUN 全 PASS」 | 現為 84 個 RUN（metrics、trace、des、softwaredelivery 的 engine 測試）全 PASS |

## 實際執行結果（review 修正後）

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | 45 個反例 fixture，其中 40 個比對確切路徑；含 TS contract 新鮮度（`spec-check.log`） |
| `make backend-test` / `backend-race` / `backend-build` | 0 / 0 / 0 | |
| `REQUIRE_GOAMD64_V3=1 make backend-numeric` | 0 | 檢查範圍含 `internal/results`；首版途中抓到 `quantile7` 的 `(n-1)*p` 在 arm64 被融合，已明確捨入 |
| `go test -count=1 -v`（metrics、trace、des、softwaredelivery） | 0 | 84 個 RUN 全 PASS（`t022-tests.log`） |
| `govulncheck ./...` | 0 | No vulnerabilities found |
| Fuzz `FuzzRun` 60 s | 0 | 747,947 execs；任意 IR 經整個 engine：typed 失敗，或 schema-valid 的 Result（digest 可重算）與 trace（`fuzz-run.log`） |
| Mutation（`results-mutants.py`，已提交） | 0 | **68/68 被擋，1 個 equivalent**（`overlap-ignores-horizon`：所有呼叫端的右端點都不超過 horizon，腳本中說明）；涵蓋 `internal/results`、`internal/simulation`、`internal/domainpack` |
| T-021 kernel／T-020 engine mutation（套用修正後重跑） | 0 / 0 | kernel 46/46＋2 equivalent、engine 51/51＋1 equivalent，與各自驗收時相同（`kernel-mutants.log`、`engine-mutants.log`）；`budget-ignored`／`no-budget` 各需約 5 分鐘（engine package 的測試以 budget 停止無界模型，要等到 `-timeout`），兩者都由 des 的 `TestRunFailures` 以 assertion 擋下 |
| `make frontend-test` | 0 | lint、typecheck、66 個 unit test |
| GitHub Actions（首版 0aa9a95） | success | [run 38800354726](https://github.com/KCL1104/Geminus/actions/runs/38800354726)：4/4 |
| GitHub Actions（review 修正 4370ab5） | success | [run 38806843899](https://github.com/KCL1104/Geminus/actions/runs/38806843899)：4/4 |
| 複核後續（`rereview/`）：`make backend-test`／`backend-race`／`REQUIRE_GOAMD64_V3=1 backend-numeric`／`spec-check` | 0 / 0 / 0 / 0 | |
| 複核後續：`go test -count=1 -v`（metrics、trace、des、softwaredelivery） | 0 | 85 個 RUN 全 PASS（新增 `TestPanicsOutsideTheReplications`） |
| 複核後續：mutation（`results-mutants.py`） | 0 | **71/71 被擋，1 個 equivalent**（新增 `reports-never-stopped`、`sink-panic-unrecovered`、`domain-metrics-unrecovered`） |
| GitHub Actions（複核後續 bc10200，與 T-042 一起） | success | [run 38811720980](https://github.com/KCL1104/Geminus/actions/runs/38811720980)：4/4（head 4866e37） |

## 驗收對照

| 測試 | 驗證內容 | 對應 |
|---|---|---|
| `TestQuantile7`、`TestCI95`、`TestStudentTTable` | Type 7 已知值（與 numpy 一致）；CI 對 scipy；t 表 999 個值以獨立的不完全 beta 函數驗證 CDF=0.975（誤差 ≤1e-10），2e-7 的擾動會被抓到 | TEST-022 |
| `TestOracleAgreement` | 17 個情境中，`Build` 的每個指標都等於從原始 observation 獨立重算的值（多 replication、容量 >1 的多 unit bundle、rework 回 queue、failure≠rework、warmup、沒有 visit 的 queue） | TEST-016、TEST-017、TEST-022 |
| `TestGoldenResultMetrics`、`TestGoldenOneResult` | GOLDEN-001 的 metrics、summaries、window、DORA 與 `valid-result.json` 完全相同；hashes 等於 T-005 函式；digest 可重算；Result 通過 schema | TEST-015、TEST-019 |
| `TestCongestedMetrics` | GOLDEN-002：wait 47.5／90.25、lead 57.5／100.25、utilization 200/205、queue length 950/205 | TEST-016 |
| `TestMeasurementWindow`、`TestPartialIntervals`、`TestNothingCompleted`、`TestUnavailableQueue` | W 與 H 恰為事件時間的邊界、cohort、censoring、跨 W 的部分 service 區間（498/998）、沒有完成時 throughput=0 可用而 lead time unavailable（不是 0）、window 內沒有 visit 的 queue 為 unavailable（wait 全 null、有 reason、queue length 仍可用） | TEST-017 |
| `TestReplicationStatistics` | 跨 replication 的 throughput CI、pooled lead 無 CI、replication-mean 帶 CI；有 replication 沒有 cohort 時 unavailable | TEST-022 |
| `TestOutcomesAndCounts`、`TestExplicitQueueOwnsMetrics`、`TestBuildRejects`、`TestFinishReleasesEntityState` | abandoned 不計入 throughput；failure／rework 只計 window 內；顯式 queue 的數值等於直通模型中 process 的數值；不一致或未 Finish 的 replications 拒絕；Finish 後 entity map 釋放且指標不變 | TEST-016 |
| `TestGoldenOneTrace`、`TestSelectedMatchesReference`、`TestTrajectories`、`TestCap`、`TestEmpty`、`TestSampledButEmptyTrace`、`TestTraceNamesTheEnteredNode` | GOLDEN-001 的 trace 與手算的時間、sequence、節點完全相同；選取與 Python 參考一致；軌跡完整；cap 恰好；沒有事件時無 artifact；queue_enter 記實際進入的 node | TEST-019（trace 不改 metrics） |
| `TestTraceDoesNotChangeMetrics`、`TestTraceIsReplicationZero` | trace_level 與 cap 不改任何數字；trace_level 在 config_hash 中；trace 等於只跑 replication 0 的 recorder（workers 1／2／5） | TEST-019 |
| `TestDeterminism`、`TestGoldenDigests`、`TestDeploymentVersion` | 同一凍結輸入在 1／3／8／GOMAXPROCS 個 worker 下 Result 完全相同；另一個 seed 不同；golden digests 鎖定；engine_version 進入 digest | TEST-019 |
| `TestRunFailures`、`TestWallTimeoutDuringRun`、`TestCancelDuringRun`、`TestSharedBudget`、`TestCompletionDecides`、`TestReplicationPanicIsAnError` | event budget、active entity、invalid IR、缺 model hash、effective hash 不一致、wall timeout、取消、跨 replication 共用的 budget、全部完成後的取消不使 run 失敗、replication panic：typed error，不回部分結果 | TEST-021（engine 部分） |
| `TestDORAThroughTheEngine` | 真正的 software-delivery pack 經過 engine：五個 DORA 指標 unavailable（缺 mapping），failure／rework >0 不產生 DORA 值，Result 通過 schema | TEST-023 |
| `TestProgress`、`TestSlowProgressSinkChangesNothing` | progress 嚴格遞增、最多 replications 次、到達最終值；慢的 sink 不改結果、不延長 run、不擋 wall timeout、不被並行呼叫 | §8（progress 不是正確性輸入） |
| `TestResultsValidate`、`TestEffectiveHashMatchesStoredBytes`、`TestDependencies` | 所有 golden model 的 Result 通過 schema；engine 重算的 effective hash 等於儲存 bytes 的 hash；engine 不依賴 domain pack、HTTP、DB、LLM | — |

## 需 owner 知悉（D-039，PROPOSED）

1. replication-mean lead time 只要有一個 replication 沒有完整 cohort 就整個 unavailable（§5 原文只說 CI 為 null；另一個做法是只用有 cohort 的 replication 並在 sample_count 反映）。
2. available summary 的 reason 一律 null；n<2 時 CI 為 null，以 sample_count 表達（與 `valid-result.json` 一致）。§5 原文為「null + reason」；schema 並不禁止 available summary 帶 reason，若 owner 要依原文，可改為 n<2 時填一個 reason code。
3. 暫定 reason codes：`NO_COMPLETED_ENTITY_IN_WINDOW`、`REPLICATION_WITHOUT_COMPLETED_COHORT`、`NO_COMPLETED_QUEUE_VISIT_IN_WINDOW`（CB-23 仍開）。
4. trace 只記 replication 0、rate 1/8、依 entity ID 的 SHA-256 選取，到上限截斷；沒有事件時不產生 artifact；queue_enter 記實際進入的 node。
5. progress 非同步、只送最新值；queue／resource 的 unavailable 必帶 reason。
6. engine_version `des-go-0.1.0`，部署可附加 build identity。

**CB-24（預設 template 過載）已用 engine 實測確認**（`cb24-measurement.log`，seed 7、5 replications、warmup 0）：reviewer utilization 7／30／90 天為 0.9946／0.9987／0.9996；code_review 平均 queue 長度 65／281／842；lead time 平均 3,279／13,203／39,663 分鐘並隨 horizon 成長；backlog 每天約多 18 個。template 未修改；建議 D-017 把 reviewer capacity 由 2 改為 6（ρ≈0.83），需 owner 決定。

## 與規劃的差異

- 規劃輸出為 `internal/results/metrics/` 與 `internal/simulation/trace/`，皆已建立；另新增 `internal/simulation/des/`，因為 engine Run 需要同時使用 kernel、metrics 與 trace，而 engine package 不能反向 import metrics。
- IR `metrics[]` 宣告仍未對應到 Result（CB-22 剩餘部分，移交 T-029）。
- DORA 仍全部 unavailable（D-010，未提供 mapping）。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| run_id、trace artifact 上傳與 artifact_id（含 artifact 與 run 的關聯）、lease、重試；run API 端的 effective hash contract test | T-023 |
| baseline／scenario comparison | T-029 |
| SimPy differential、效能 benchmark | T-037 |
| 實體非 amd64 機器上的測試 | 本環境只有 amd64；以組語檢查與 GOAMD64=v3 替代 |
