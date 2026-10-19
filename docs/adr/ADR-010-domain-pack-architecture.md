# ADR-010 — Domain Pack Architecture

狀態：SOURCE_LOCKED（依原報告，不代表新增工程提案已經人類批准）。

來源：原報告 pp. 1–3,19–20,49–53。

## Decision

一個深入的SoftwareDeliveryDomainPack + generic kernel；DomainPack供catalog/rules/inference/metrics/prompts/views。

## Consequences / Review boundary

第二商業domain是P1；test-only generic pack可驗boundary，不應為新增domain改genericengine。

若要改此原報告架構邊界，必須提出新的ADR並由人類確認，不能以coding agent偏好替換。

修改（2026-10-19，owner H-016，[ADR-025](ADR-025-p0-domain-electronics-line.md)）：P0 深入做的 pack 改為電子組裝／測試線；SoftwareDeliveryDomainPack 保留為範例與 conformance pack；第二個 domain 為倉儲人力與尖峰（P1）。本 ADR 的架構邊界不變。
