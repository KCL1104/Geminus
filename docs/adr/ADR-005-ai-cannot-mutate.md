# ADR-005 — AI Cannot Mutate Model

狀態：SOURCE_LOCKED（依原報告，不代表新增工程提案已經人類批准）。

來源：原報告 pp. 3,23–27,41–43。

## Decision

AI只提proposal；server deterministic validator + human approval才有canonical effect。

## Consequences / Review boundary

provider沒有DB/write/run tools；Critic不能覆蓋server authority。

若要改此原報告架構邊界，必須提出新的ADR並由人類確認，不能以coding agent偏好替換。
