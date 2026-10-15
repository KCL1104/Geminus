# ADR-009 — PostgreSQL-backed P0 Jobs

狀態：SOURCE_LOCKED（依原報告，不代表新增工程提案已經人類批准）。

來源：原報告 pp. 37,39–40。

## Decision

P0 PostgreSQL pull queue + SKIP LOCKED；不預先增加broker。

## Consequences / Review boundary

RLS-aware tenant assignment、lease/retry/fencing由D-008/D-011補完。

若要改此原報告架構邊界，必須提出新的ADR並由人類確認，不能以coding agent偏好替換。
