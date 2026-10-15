# ADR-003 — Immutable Model Versions

狀態：SOURCE_LOCKED（依原報告，不代表新增工程提案已經人類批准）。

來源：原報告 pp. 6,18–19,31–33。

## Decision

Draft mutable；published versions immutable並保留parent；run只引用version。

## Consequences / Review boundary

dirty draft與approval互動由D-003補足；hash/dataset freezing見D-006。

若要改此原報告架構邊界，必須提出新的ADR並由人類確認，不能以coding agent偏好替換。
