# 來源與需求追溯

## 原始報告

- 檔案：[ORIGINAL_REPORT.pdf](ORIGINAL_REPORT.pdf)
- 原標題：AI-native Digital Twin SaaS Product-MVP Implementation Spec
- 原狀態：v0.1 — Developer-ready draft；日期：2026-10-15；共 54 頁。
- SHA-256：`0dd63e4fcddbc3159e001970e9dfb624dfd62839afb5f0ef880bd63f5109be16`
- 本套件為拆分與工程化草案，並非已實作產品，亦非原作者已批准的修訂版。

本套件以 PDF 印刷頁碼標示來源。本文的 PROPOSED 不是原報告內容；任何原報告未支持的欄位、行為、演算法或 endpoint，都由 decisions register 追蹤。

| 報告頁碼 | 原內容 | 主要落地文件 |
|---|---|---|
| 1–8 | 定位、P0/P1、user stories、工程目標 | docs/PRD.md、docs/TEST_PLAN.md |
| 8–14 | 第 8 頁 runtime 架構圖、Go、repo、2D/3D adapters | docs/ARCHITECTURE.md、AGENTS.md |
| 14–20 | IR 範例、version、scenario、DomainPack | docs/TWIN_IR_SPEC.md、schemas/model-ir.schema.json |
| 20–22 | DES、event heap、integer ticks、seed invariant | docs/SIMULATION_SPEC.md |
| 23–28 | AI roles、prompts、human approval；第 27–28 頁 sequence 圖 | docs/AI_SPEC.md、prompts/ |
| 29–35 | REST routes、errors、examples、import lifecycle | docs/API_DATABASE.md、openapi/openapi.yaml |
| 36–37 | 第 36 頁 ER 圖、tenant ownership、RLS、job queue | migrations/、docs/SECURITY.md |
| 38–46 | CI、deploy、RBAC、audit、telemetry、六層測試、AI gates | docs/SECURITY.md、docs/TEST_PLAN.md |
| 47–50 | 人力、週程、launch gates、implementation order、TBD | docs/IMPLEMENTATION_PLAN.md、planning/tasks.json |
| 51–53 | 核心閉環、多 domain strategy、四個穩定 contracts | README.md、docs/ARCHITECTURE.md |
| 53–54 | 原報告外部參考資料 | 保留於原 PDF，未將其產品現況敘述當新驗證結果 |

## 僅為契約相容性而查核的外部技術依據

以下不是新增產品需求，亦不取代原報告。查核日期 2026-10-15；本套件沒有指定「最新」Go、Next.js、SDK 或雲端產品版本。

- OpenAPI Specification 3.1.1，作為 D-015 的固定 dialect 技術參考：`https://spec.openapis.org/oas/v3.1.1.html`
- RFC 8785 / JSON Canonicalization Scheme，作為 D-006 的**新提案**，原報告沒有指定此 canonicalization：`https://www.rfc-editor.org/info/rfc8785/`
- PostgreSQL 18 Row Security Policies，僅查核 policy / owner / BYPASSRLS 邊界；不代表已指定 production DB major：`https://www.postgresql.org/docs/18/ddl-rowsecurity.html`

沒有重驗原報告全部外部時效性敘述；本交付目的是根據報告建置 implementation handoff。
