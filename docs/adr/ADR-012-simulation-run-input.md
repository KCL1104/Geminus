# ADR-012 — SimulationEngine dataset injection: explicit RunInput

狀態：APPROVED（2026-10-15，KCL1104 於 T-003 選擇「明確的 RunInput」）。來源：docs/ARCHITECTURE.md §3、T-003 scope。

## Decision

`internal/simulation.Engine` 的 Run 簽名為 `Run(ctx, RunInput, ProgressSink)`，取代原報告字面上的 `Run(ctx, ModelIR, RunConfig, ProgressSink)`。`RunInput` 一次帶入 effective IR、effective model hash、RunConfig、ExecutionLimits、依 logical_ref 的已驗證 datasets，以及依 logical_ref 排序的 dataset manifest。

## Consequences

- 「所有輸入在 Run 前凍結」由型別保證；engine 沒有任何 resolver、DB、object storage 或網路入口，重試與重播讀到同一份輸入。
- RunConfig 維持純 API payload；不在其中藏不序列化的內部欄位，避免從 JSON 解出的 RunConfig 「安靜地沒有 datasets」。
- 與原報告簽名的差異只在參數打包方式，Validate 與 ProgressSink 概念不變；`ProgressSink.Report` 只帶 engine 擁有的 replications／events 計數，run identity、status、revision 由 worker 補上。
