# ADR-004 — JSON Patch Change Protocol

狀態：SOURCE_LOCKED（依原報告，不代表新增工程提案已經人類批准）。

來源：原報告 pp. 19,24–25,31–33。

## Decision

修改以RFC6902 JSON Patch表示；base/revision pinning避免silent overwrite。

## Consequences / Review boundary

P0 allowlist與pointer安全由D-014限制；不是任意RFCoperation都可用。

若要改此原報告架構邊界，必須提出新的ADR並由人類確認，不能以coding agent偏好替換。
