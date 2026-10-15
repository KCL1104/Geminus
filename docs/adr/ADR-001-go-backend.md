# ADR-001 — Go Backend

狀態：SOURCE_LOCKED（依原報告，不代表新增工程提案已經人類批准）。

來源：原報告 pp. 9–10,50。

## Decision

採 Go 為 P0 SaaS backend，net/http / ordinary packages，framework保持輕量。

## Consequences / Review boundary

主要優勢為總體delivery risk，並非宣稱Go一定比Rust快。SDK版本與sqlc仍需後續選型。

若要改此原報告架構邊界，必須提出新的ADR並由人類確認，不能以coding agent偏好替換。
