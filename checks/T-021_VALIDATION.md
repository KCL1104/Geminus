# T-021 驗證報告：DES resource／queue／service／routing kernel

**日期：2026-10-15｜狀態：`review`（等待獨立 review，依 H-003 驗收）｜需求：REQ-011、REQ-013｜Gate：D-005、D-007、D-023、D-032（皆已批准）；新增的實作語意併入 D-038（PROPOSED，gate 在 T-023）**

逐條輸出在 `checks/evidence/2026-10-15-T-021/`。

## 交付內容（`internal/simulation/engine`）

| 檔案 | 內容 |
|---|---|
| `kernel.go` | `RunReplication(ctx, program, index, budget, observer)`：在 T-020 的 replication clock 上加入 queue、resource、service、delay、decision、failure／rework、sink。Resource-wise FIFO dispatcher：所有等待 request 依 (queue_enter_tick, request_sequence) 排成一列；bundle 的每個 resource 都夠且沒有被較早未授予、共用 resource 的 request 擋住時才授予，授予是 all-or-nothing。SERVICE_COMPLETE（priority 0）原子釋放整個 bundle 後在同一事件內路由；dispatch 在 priority 20。Typed 失敗：`ErrActiveEntities`、`ErrEntityVisits`（以及 T-020 的 `ErrEventBudget`、context 錯誤、`ErrInternal`）。以 `Observation` 回報事實（arrival 附到達序號、queue enter、service start／end、resource in-use、delay start／end、failure、rework、complete），指標彙整屬 T-022 |
| `replication.go` | entity 紀錄只保留系統內的 entity，到達 sink 即釋放（T-020 review：原本 10M 個 entity 會留下約 604 MB）；`onProcessed` 與目前事件的 hook；`FormatEntityID` 給只看得到 source 與序號的 observer 用 |
| `kernel_test.go` | 見下方對照 |
| `docs/SIMULATION_SPEC.md` §3 | kernel 的實作段落 |

`ReplicationOutcome` 回報 Created、Exited（到達任一 sink，含 abandoned）、Censored（horizon 時仍在系統內）、PeakActive 與 Events。`RunReplication` 結束時檢查 entity 紀錄數等於 active 數，不相等回 `ErrInternal`。

## 實際執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test` / `backend-race` / `backend-build` | 0 / 0 / 0 | |
| `REQUIRE_GOAMD64_V3=1 make backend-numeric` | 0 | 六個 target 無 FMA、無平台 math 呼叫；GOAMD64=v3 測試通過 |
| `go test -count=1 -v ./internal/simulation/engine/` | 0 | 98 個 test／subtest PASS，0 FAIL（`engine-tests.log`） |
| `govulncheck ./cmd/... ./internal/... ./schemas/...` | 0 | No vulnerabilities found |
| Fuzz `FuzzKernel` 60 s | 0 | 1,136,930 次；任意 IR bytes 經 Compile 後執行 kernel，不 panic、不破壞不變量 |
| Kernel mutation（`kernel-mutants.py`，已提交） | 0 | **28/28 被擋，1 個 equivalent**（`record-invariant-unchecked`：紀錄不變量只在另一個 bug 留下紀錄時才觸發，`entity-record-kept` 透過它被擋）。首輪存活 `visits-not-reset`（visit 上限差一）→ 補 `TestKernelLimitsAreExact` 後擋下 |
| T-020 engine mutation（套用 kernel 後重跑） | 0 | 51/51＋1 equivalent，與 T-020 驗收時相同 |
| GitHub Actions | 推送後補記 | |

## 驗收對照

| 測試 | 驗證內容 | 對應 |
|---|---|---|
| `TestGoldenCases` | GOLDEN-001：完成 100、wait 0、lead 5、utilization 500/1010、首次到達 10、最後完成 1005；GOLDEN-002：wait mean 47.5／p95 90.25、lead 57.5／p95 100.25、utilization 200/205，censored 0。與 `expected-cases.json` 手算值相差 ≤1e-12（相對） | TEST-015、TEST-016 |
| `TestBundleAndResourceWiseFIFO` | P 需要 R1+R2、R2 被 H 佔用：P 等待期間 R1 不被部分佔用；只需 R1 的 Q 排在 P 後面而等到 P 之後；不相干的 Z 照常開始（starts H=1、P=101、Q=106、Z=4） | TEST-018 |
| `TestSameTickReleaseBeforeArrival` | D-032：同 tick 的完成先釋放並路由進下一個 buffer，再處理 arrival（starts A=10、B=20、C=70） | TEST-018 |
| `TestRoutingDelayObservations` | 100%／0% routing（0% flow 0 個）、固定 seed 的 30% 分流（20,000 個，誤差 <1.5%）、delay 恰好 7 分鐘、failure／rework observation、abandoned sink 記為未完成、無 resource 的 process 不等待 | TEST-016 |
| `TestCensoring` | horizon=100 分：建立 20、離開 9、censored 11 | TEST-017 |
| `TestKernelLimits`、`TestKernelLimitsAreExact` | active entity 與 visit 上限回 typed error；上限恰好可通過、少 1 失敗（source 算第 1 次 visit） | TEST-021（limits 部分） |
| `TestKernelDeterminism` | 同 program／index 結果逐 observation 相同，並行亦同 | TEST-019（kernel 部分） |
| `TestDelayCompletesAfterServiceInATick` | 同 tick 的 SERVICE_COMPLETE（0）先於 DELAY_COMPLETE（10） | TEST-018 |
| `TestStreamsUsedByPurpose` | service、delay、routing 各用自己的 (node, purpose) stream，依事件順序重算結果相同 | — |
| `TestGrantsStartInRequestOrder` | 同一次 dispatch 授予的 request 依 request 順序開始並抽樣 | TEST-018 |
| `TestEntityRecordsAreReleased` | 300,000 個 entity 依序經過 1 ms delay：PeakActive=1，紀錄不殘留 | T-020 review P2 |
| `TestRoutingTolerance` | decision 的累積機率不足 1 時剩餘 U 走最後一條；非 decision 不抽 U，p=1-1e-9 的 flow 一定通過 | T-020 review P3 |
| recorder 不變量（每個測試） | entity 任何時刻恰處一個狀態、0≤in_use≤capacity、時間不倒退、service 只在排隊後開始、arrival 序號連續 | TEST-018 |

## 需 owner 知悉（併入 D-038，PROPOSED）

1. sink 沒寫 outcome 時視為 completed（CB-22 的 sink 預設值）。
2. 只要有 resource 被釋放或有 request 進入 buffer，就保證該 tick 末有一次 dispatch；若該 tick 的 dispatch 已執行過，會再排一次（仍是 priority 20）。
3. 同一次 dispatch 授予的 request 依 request 順序開始服務並抽樣。

## 與規劃的差異

- 規劃輸出 `internal/simulation/runtime/` 未建立：kernel 放在 `internal/simulation/engine/`，因為它直接使用 replication 與 `Program` 的未匯出結構（同 T-020 的 compile）。
- `ReplicationOutcome.Completed` 改名為 `Exited`，避免與「completed sink」指標混淆。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| 指標彙整、warmup cohort、CI、result digest、trace | T-022 |
| wall timeout、跨 replication 的平行 pool、progress | T-022（engine Run）／T-023 |
| queued 立即取消、cancel-vs-complete 互斥、lease | T-023 |
| SimPy differential | T-037 |
| 實體非 amd64 機器上的測試 | 本環境只有 amd64；以組語檢查與 GOAMD64=v3 替代 |
| GitHub Actions | 推送後補記 |
