# T-003 驗證報告：凍結候選契約與 Go/TypeScript domain types

**日期：2026-10-15｜狀態：`review`（待 owner 驗收）｜需求：REQ-003、REQ-005、REQ-008、REQ-011｜Gate：D-001、D-002、D-014、D-015、D-024–D-028、D-030（皆已由 owner 批准）**

逐條輸出在 `checks/evidence/2026-10-15-T-003/`（`summary.tsv` 為 exit code 總表）。

## 交付內容

| 項目 | 內容 |
|---|---|
| 契約（schemas／OpenAPI） | D-001 typed server IDs（16 種 prefix + ULID，128 處欄位／參數改用）；D-024 `Error.details` code／severity；D-025 封閉 `ErrorCode` + `x-http-status`、18 個有 request body 的 operation 加 415；D-026 `analysis-output.schema.json`（無 run ID，evidence 以角色引用）；D-027 `server_risk_level`／`provider_risk_claim` 且 schema 強制 `risk_level = max`；D-028 `Draft.issues[]`；D-030 `PROPOSAL_ALREADY_DECIDED` |
| Fixtures | 舊 slug ID 以固定演算法改為 ULID；新增 13 個正／反例；66 個 cases，其中 29 個反例釘住精確錯誤路徑（TEST-001「fail exact path」） |
| Go types | `internal/model`（IR、JSON Patch、DatasetBinding、ValidationIssue、嚴格 decoder）；`internal/simulation`（Engine、RunInput、RunConfig、ExecutionLimits、Result）；`internal/contracttest`（test-only） |
| TS types | `scripts/gen_ts_contracts.py` 由 schemas 產生 `apps/web/lib/contracts/`（14 檔、84 types）；`make spec-ts-contracts` 檢查是否過期 |
| 決策文件 | ADR-012（明確 RunInput，owner 選定）；TWIN_IR_SPEC §1 prefix 清單；API_DATABASE 錯誤碼表；AI_SPEC 與 analyst prompt（analyst-0.2.0）配合 D-026 |

## 實際執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | 含新增的 `spec-ts-contracts` |
| `make spec-contracts` | 0 | 25 正例、34 反例（29 個精確路徑）、7 語意反例、747 個 `$ref`；D-025 code／response 一致性檢查 |
| `go test ./internal/model/...` | 0 | schema parity、discriminator parity、正例 round-trip、嚴格 decode 反例、falsy patch value、有意義的零值、依賴限制 |
| `go test ./internal/simulation/...` | 0 | schema parity、Result／RunConfig round-trip、null 不變 0、只依賴 stdlib + internal/model |
| `make backend-test` / `backend-race` / `backend-build` | 0 / 0 / 0 | |
| `make frontend-test` | 0 | lint、typecheck（含 6 個 `@ts-expect-error` 反例）、Vitest 14 tests |
| `make frontend-build` | 0 | |
| `npm audit --omit=dev --audit-level=high` | 0 | D-031 production gate |
| `openapi-spec-validator 0.7.2` | 0 | 修改後的 openapi.yaml |
| `actionlint v1.7.12` | 0 | |
| GitHub Actions [run 38735650734](https://github.com/KCL1104/Geminus/actions/runs/38735650734)（`b51cdc1`） | success | spec-check、backend、frontend、db migration smoke 四個 job 全部 success |

## 證明測試會失敗（mutation）

| 檢查 | Mutant | 結果 |
|---|---|---|
| Go model | tag 改名、`requires` 用 `omitempty`、constant value 改非 pointer、刪 enum 值、刪欄位、關閉 RejectUnknownMembers、import `net/http` | 7/7 被擋 |
| Go simulation | nullable 加 `omitzero`、nullable `reason` 改非 pointer、tag 改名 | 3/3 被擋 |
| check_contracts.py | 反例釘錯路徑、移除一個 415、ErrorCode status 對應錯誤 | 3/3 被擋 |
| TS | 手改產出檔使 `ErrorCode = string` | typecheck 失敗（unused `@ts-expect-error`）且 freshness 檢查回報 STALE |

## 驗收測試涵蓋範圍（誠實界線）

- **TEST-001**：形狀層級已涵蓋（正例通過；未知欄位、負 capacity、型別錯誤、P1 distribution、schema_version 於精確路徑失敗）。語意層級（TEST-002）屬 T-004。
- **TEST-003**：只涵蓋形狀層級（move／copy、root path、非法 `~` escape 於精確路徑被拒）。identity path 拒絕、decoded-token allowlist、第二個 op 失敗時第一個不生效的原子性 **NOT RUN**，屬 T-004。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| `make db-test`（本機） | 本次未改 SQL；CI db-smoke job 仍會執行 |
| SQL 對齊 D-001／D-027 | 移入 T-006 scope（`planning/tasks.json`）：新增 risk 欄位與 max 檢查、收緊 public_id、測試 ID 改 ULID |
| `ValidationIssue.code` 封閉清單 | 屬 D-023／T-004 |
| `audit actor_id` 型別 | actor_type 可為 worker／system，留待 T-035 |
| HTTP envelope 的 Go types（Draft、Proposal 等） | 依任務歸屬於 T-012／T-013／T-025 |
| SDK codegen 對外部 `$ref` 的相容性 | 本輪 TS 改用自製 generator（不需要外部 `$ref` 抓取）；若日後改用第三方 SDK generator 需另驗 |
