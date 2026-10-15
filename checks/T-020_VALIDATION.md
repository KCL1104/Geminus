# T-020 驗證報告：DES compiler、clock、heap、RNG

**日期：2026-10-15｜狀態：`review`｜需求：REQ-007、REQ-008、REQ-009｜Gate：D-005、D-006、D-023、D-029、D-032、D-033（皆已批准）**

逐條輸出在 `checks/evidence/2026-10-15-T-020/`。

## 交付內容（`internal/simulation/engine`）

| 檔案 | 內容 |
|---|---|
| `rng.go` | SplitMix64、(0,1) uniform、replication seed、(node, purpose) stream seed（SIMULATION_SPEC §4） |
| `sample.go` | 四種分布的 sampler：constant 與 min=mode=max 的 triangular 不消耗 draw；exponential = -mean·ln(1-U)，至少 1 tick；triangular 標準 inverse CDF；empirical floor(U·N)。量化與 `model.Ticks` 相同（有測試鎖定），超過 365 天時改為「必晚於 horizon」的值，不會失敗也不會溢位 |
| `events.go` | 事件 heap，key = (tick, priority 0/10/20, sequence)；sequence 在 replication 內嚴格遞增 |
| `compile.go` | `Compile(RunInput)`：重新完整驗證 IR（schema、語意、以實際 dataset 樣本重跑 D-033）、RunConfig 與 ExecutionLimits（schema＋0≤warmup<horizon）；產生 immutable 的 `Program`：node、resource、flow 依 ID bytes 排序編號，decision routes 只含 p>0 的 flow 並依 flow ID 累加，explicit queue 指向其 process 的 buffer。錯誤為帶 issue 的 `*InputError` |
| `replication.go` | 單一 replication 的 clock 與 event loop：source 依 ID 初始化、第一個 arrival 在一個 interarrival 之後、max_entities、horizon 同 tick 的事件會處理、晚於 horizon 不排程；entity ID `r<rep>/<source>/<n>`；每次 pop 檢查 cancellation 並扣共用的 event budget（`ErrEventBudget`），不變量破壞回 `ErrInternal` |
| `scripts/engine_reference.py` | 獨立的 Python 參考實作（不共用 Go 程式碼）：SplitMix64、seed、量化、四種 sampler、事件順序、arrival 流程；產生 `testdata/simulation/engine-vectors.json`，`make spec-reference-math` 檢查 |

佇列、資源、服務與路由的 kernel 是 T-021；指標與 digest 是 T-022。這裡的 `onArrival`／`onEvent` hook 是 T-021 接入點；未接 handler 的事件會回 `ErrInternal`，不會被略過。

## 實際執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | 含 `engine_reference.py`（10 個 sampler 案例、10 個事件、9 個 arrival） |
| `go test -count=1 -v ./internal/simulation/...` | 0 | 61 個 test／subtest PASS，0 FAIL |
| `make backend-test` / `backend-race` / `backend-build` | 0 / 0 / 0 | race：8 個並行 replication 結果相同 |
| `govulncheck v1.9.0 ./...` | 0 | |
| Fuzz `FuzzCompileAndArrive` 30 s | 0 | 851,555 次；任意 IR bytes 不 panic，編譯成功的 program 在 budget 內跑完 arrival |
| Mutation（`go test -overlay`） | — | **30/30 被擋**（首輪 29/30：「flow 不排序」存活，因 golden model 的 flow 恰好已依 ID 排列；補 `TestDecisionRoutesInFlowIDOrder` 後擋下） |
| GitHub Actions | 推送後補記 | |

## 驗收對照

- **Golden vectors**：sampler（含下溢、尾端超過 365 天、退化 triangular 不消耗 draw）、事件順序、三個 source 的 arrival 序列（含 max_entities、entity ID）與 Python 參考逐 tick 相同；stream 狀態也相同，證明消耗的 draw 數一致。
- **GOLDEN-001 的 arrival**：100 個 entity 於 10、20、…、1000 分鐘到達（手算預期值，`expected-cases.json`）；horizon 恰在 1000 分時第 100 個仍處理，999.99999 分時只有 99 個。
- **決定性**：同一 program 與 replication index 結果相同，平行執行也相同；不同 index 不同；新增 source 不改變其他 source 的 arrival；同 tick 的不同 source 依 ID 順序。
- **D-033／溢位**：dataset 有 0 樣本且位於 rework loop 時回 ZERO_TIME_CYCLE（IR 本身合法，只有實際樣本能看出）；interarrival 可能為 0 時回 DISTRIBUTION_INVALID；delay 超出範圍回 `ErrInternal`。
- **拒絕輸入**：不合法 IR、缺 dataset、dataset 屬於其他 ref、樣本數不符、樣本超過 365 天、warmup≥horizon、replications=0、horizon>365 天、負 seed、未知 trace level、limits 超過或缺少——全部回帶 JSON Pointer 的 `*InputError`。
- **不共用狀態**：Compile 後修改輸入 IR 不影響 program。

## 需 owner 知悉的實作解讀

1. ARRIVAL 事件先排下一個 arrival、再路由新 entity（決定同 tick 的 sequence）。
2. 晚於 horizon 的事件不放進 heap，但抽樣仍消耗 stream（與 Python 參考一致）。
3. exponential 樣本至少 1 tick；超過 365 天的樣本視為「永不在 horizon 內」。
4. decision 的 0% flow 不列入 routes。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| queue／resource／service／routing kernel、GOLDEN-001/002 的完整結果 | T-021 |
| 指標、warmup cohort、result digest、trace | T-022 |
| SimPy differential、跨架構 digest | T-037 |
| GitHub Actions | 推送後補記 |
