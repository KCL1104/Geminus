# T-022 驗證報告：Metric aggregation、result digest、sampled trace、engine Run

**日期：2026-10-15｜狀態：`review`（等待獨立 review，依 H-003 驗收）｜需求：REQ-011、REQ-013、REQ-015、REQ-017｜Gate：D-006、D-010（皆已批准）；新增的實作語意登記為 D-039（PROPOSED，gate 在 T-023）**

逐條輸出在 `checks/evidence/2026-10-15-T-022/`。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/results/metrics` | `Collector`（engine Observer，每個 replication 一個）與 `Build`：measurement window [W,H]、throughput（completed sink，replication population）、lead time（arrival≥W 且 completion≤H 的 cohort，pooled entity population，無 CI）、`additional_metrics.replication_mean_lead_time_minutes`（replication population，帶 Student-t CI；只要有一個 replication 沒有 cohort 即 unavailable）、queue wait（queue_enter≥W 且 start≤H 的 visit，pooled）、queue_length_mean 與 utilization（[W,H] 的時間積分，含跨邊界的部分區間，再取 replication 平均）、failure／rework count、censored。Type 7 quantile；t 臨界值為 scipy 產生的常數表（`ttable.go`，`scripts/gen_t_table.py`）。所有乘積明確捨入（D-038 numerical profile） |
| `internal/simulation/trace` | `Recorder`：replication 0 中 entity ID 的 SHA-256 落在 1/8 以下者，依 engine 處理順序記錄 arrival／queue_enter（顯式 queue 的位置）／service／delay／failure／rework／complete／abandon，到 max_trace_events 截斷並標 truncated |
| `internal/simulation/des` | `Engine`（實作 `simulation.Engine`）：Compile → bounded pool（預設 GOMAXPROCS）跑 replications，共用 event budget 與 wall timeout，全部完成才算完成；typed 失敗不回部分結果；重算 effective_model_hash 並拒絕不一致的凍結值；config／manifest hash；`DomainMetrics` 注入 DORA；`hashing.Result` digest；`RunWithTrace` 另回 trace artifact |
| `schemas/simulation.schema.json` | CB-23：unavailable 必有 reason、只有 population=replication 可帶 CI；新增 `TraceArtifact`／`TraceEvent`（`geminus.trace.v1`）。正例 `valid-trace-artifact.json`，反例 `invalid-unavailable-without-reason.json`、`invalid-entity-population-ci.json`、`invalid-trace-event-type.json`、`invalid-trace-entity-id.json`（皆比對確切錯誤路徑） |
| `internal/simulation/types.go`、`apps/web/lib/contracts/simulation.ts` | Go 型別（`TraceArtifact` 等，parity 與 round-trip 測試）；TS 型別由 `scripts/gen_ts_contracts.py` 重新產生 |
| `RunInput.ModelHash` | pinned version 的 model_hash（ADR-012 補充） |
| `scripts/engine_reference.py` | 新增 trace 選取的獨立參考向量 |
| `testdata/simulation/engine-golden-digests.json` | GOLDEN-001／002 的 result digest（engine 輸出的 regression lock，標明不是 oracle） |
| 文件 | SIMULATION_SPEC §5、§9 實作段落；ADR-012 補充；CONTRACT_BLOCKERS CB-22／23／24 狀態；D-039 |

## 實際執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | 含新 fixture 與 TS contract 新鮮度 |
| `make backend-test` / `backend-race` / `backend-build` | 0 / 0 / 0 | |
| `REQUIRE_GOAMD64_V3=1 make backend-numeric` | 0 | 檢查範圍含 `internal/results`；途中抓到 `quantile7` 的 `(n-1)*p` 在 arm64 被融合，已明確捨入 |
| `go test -count=1 -v`（metrics、trace、des） | 0 | 43 個 RUN 全 PASS |
| `govulncheck` | 0 | |
| Fuzz `FuzzRun` 60 s（另 90 s 一輪） | 0 | 任意 IR 經整個 engine：typed 失敗，或 schema-valid 的 Result（digest 可重算）與 trace |
| Mutation（`results-mutants.py`，已提交） | 0 | **43/43 被擋，1 個 equivalent**（`overlap-ignores-horizon`：所有呼叫端的右端點都不超過 horizon，腳本中說明）。首輪存活 `engine-version-ignored` → 補 `TestDeploymentVersion` 後擋下 |
| T-021 kernel／T-020 engine mutation（套用 T-022 後重跑） | 0 | kernel 43/43＋2 equivalent；engine 51/51＋1 equivalent，與各自驗收時相同 |
| `make frontend-test` | 0 | lint、typecheck、66 個 unit test（TS contract 型別有新增） |
| GitHub Actions | 推送後補記 | |

## 驗收對照

| 測試 | 驗證內容 | 對應 |
|---|---|---|
| `TestQuantile7`、`TestCI95`、`TestStudentTTable` | Type 7 已知值（與 numpy 一致）；CI 對 scipy；t 表 999 個值以獨立的不完全 beta 函數驗證 CDF=0.975（誤差 ≤1e-10），2e-7 的擾動會被抓到 | TEST-022 |
| `TestGoldenResultMetrics`、`TestGoldenOneResult` | GOLDEN-001 的 metrics、summaries、window、DORA 與 `valid-result.json` 完全相同；hashes 等於 T-005 函式；digest 可重算；Result 通過 schema | TEST-015、TEST-019 |
| `TestCongestedMetrics` | GOLDEN-002：wait 47.5／90.25、lead 57.5／100.25、utilization 200/205、queue length 950/205 | TEST-016 |
| `TestMeasurementWindow`、`TestPartialIntervals`、`TestNothingCompleted` | W 與 H 恰為事件時間的邊界、cohort、censoring、跨 W 的部分 service 區間（498/998）、沒有完成時 throughput=0 可用而 lead time unavailable（不是 0） | TEST-017 |
| `TestReplicationStatistics` | 跨 replication 的 throughput CI、pooled lead 無 CI、replication-mean 帶 CI；有 replication 沒有 cohort 時 unavailable | TEST-022 |
| `TestOutcomesAndCounts`、`TestExplicitQueueOwnsMetrics`、`TestBuildRejects` | abandoned 不計入 throughput；failure／rework 只計 window 內；queue 指標歸顯式 queue；不一致的 replications 拒絕 | TEST-016 |
| `TestGoldenOneTrace`、`TestSelectedMatchesReference`、`TestTrajectories`、`TestCap`、`TestEmpty`、`TestSampledButEmptyTrace` | GOLDEN-001 的 trace 與手算的時間、sequence、節點完全相同；選取與 Python 參考一致；軌跡完整；cap 恰好；沒有事件時無 artifact | TEST-019（trace 不改 metrics） |
| `TestTraceDoesNotChangeMetrics` | trace_level 與 cap 不改任何數字；trace_level 在 config_hash 中 | TEST-019 |
| `TestDeterminism`、`TestGoldenDigests`、`TestDeploymentVersion` | 同一凍結輸入在 1／3／8／GOMAXPROCS 個 worker 下 Result 完全相同；另一個 seed 不同；golden digests 鎖定；engine_version 進入 digest | TEST-019 |
| `TestRunFailures`、`TestWallTimeoutDuringRun`、`TestCancelDuringRun` | event budget、active entity、invalid IR、缺 model hash、effective hash 不一致、wall timeout、取消：typed error，不回部分結果 | TEST-021（engine 部分） |
| `TestProgress`、`TestResultsValidate`、`TestDependencies` | progress 單調、不影響結果；所有 golden model 的 Result 通過 schema；engine 不依賴 domain pack、HTTP、DB、LLM | — |

## 需 owner 知悉（D-039，PROPOSED）

1. replication-mean lead time 只要有一個 replication 沒有完整 cohort 就 unavailable。
2. available summary 的 reason 一律 null；n<2 時 CI 為 null，以 sample_count 表達。
3. 暫定 reason codes：`NO_COMPLETED_ENTITY_IN_WINDOW`、`REPLICATION_WITHOUT_COMPLETED_COHORT`、`NO_COMPLETED_QUEUE_VISIT_IN_WINDOW`（CB-23 仍開）。
4. trace 只記 replication 0、rate 1/8、依 entity ID 的 SHA-256 選取，到上限截斷；沒有事件時不產生 artifact。
5. engine_version `des-go-0.1.0`，部署可附加 build identity。

**CB-24（預設 template 過載）已用 engine 實測確認**：5 個 replication，reviewer utilization 7 天 0.9946、90 天 0.9996；code_review 平均 queue 長度 7／30／90 天為 65／281／842；backlog 每天約多 18 個。template 未修改；建議 D-017 把 reviewer capacity 由 2 改為 6（ρ≈0.83），需 owner 決定。

## 與規劃的差異

- 規劃輸出為 `internal/results/metrics/` 與 `internal/simulation/trace/`，皆已建立；另新增 `internal/simulation/des/`，因為 engine Run 需要同時使用 kernel、metrics 與 trace，而 engine package 不能反向 import metrics。
- IR `metrics[]` 宣告仍未對應到 Result（CB-22 剩餘部分，移交 T-029）。
- DORA 仍全部 unavailable（D-010，未提供 mapping）。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| run_id、trace artifact 上傳與 artifact_id、lease、重試 | T-023 |
| baseline／scenario comparison | T-029 |
| SimPy differential、效能 benchmark | T-037 |
| 實體非 amd64 機器上的測試 | 本環境只有 amd64；以組語檢查與 GOAMD64=v3 替代 |
| GitHub Actions | 推送後補記 |
