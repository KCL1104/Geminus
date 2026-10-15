# ADR-007 — React Flow Authoring

狀態：SOURCE_LOCKED（依原報告，不代表新增工程提案已經人類批准）。

來源：原報告 pp. 2,12–14。

## Decision

React Flow作2D authoring adapter；domain model與UI state分開。

## Consequences / Review boundary

生成nodes/edges是projection，不保存第二套IR。

若要改此原報告架構邊界，必須提出新的ADR並由人類確認，不能以coding agent偏好替換。
