# AGENTS.md — Coding Agent Rules

本檔適用整個 repository。來源：原報告 pp. 10–11、38–39、48–51；新增執行細節受 `docs/DECISIONS_AND_TBD.md` 管理。

## 1. 先讀、先確認、再改動

依序讀 README → DECISIONS_AND_TBD → PRD → ARCHITECTURE → 本次任務涉及的 Spec → planning/tasks.json。遇來源與衍生文件矛盾，停止受影響部分、記錄差異，保留其他可獨立推進工作。不得自行把 proposed / TBD 轉為 approved；不得把本規格包宣稱為已有 application。

`schemas/` 是 JSON payload source of truth；`openapi/openapi.yaml` 是 HTTP binding source of truth；`migrations/` 是 DB shape draft。Go/TypeScript types 由 schema 生成或接受 contract tests 約束。禁止自行維護第二套 React Flow / AI / simulation domain model。

## 2. 不可違反的 P0 invariants

- Generic Twin IR + DomainPack + SimulationEngine + ModelChangeProposal 四個邊界保持分離；generic engine 不 import 任何 domain package（electronics-line、software-delivery 等）、HTTP、pgx、LLM SDK 或 frontend types。
- AI 只能建議，不能 approve、publish、寫 DB、執行任意程式／SQL、呼叫 production tools、自行啟動 simulation。Schema-valid 不是 authorized；risk/validation 必須由 server 計算。
- ModelVersion、Scenario、dataset revision、completed result 均不可原地修改。Simulation 永遠綁 immutable version、patch、dataset manifest、engine/config/seed。
- stale revision/base 回 409，不 silent overwrite / auto-rebase。Approval 以同一 transaction 完成 revalidation、一次性 canonical effect、audit、idempotency response。
- 每個 tenant-owned row 帶 organization_id；application filter + RLS + composite tenant FKs。API / worker role 不得 superuser、BYPASSRLS 或 table owner。Tenant context 必須 transaction-local，不可殘留於 pool connection。
- 2D 為 authoring，3D 為 read-only projection/playback；共用 entity IDs 與 IR topology。不可建立另一份 3D topology。
- 凡資料不足，回 needs_input / unavailable；不得捏造 metric、dataset、性能測試數字、通過的測試或用戶批准。

## 3. Repository 邊界

```text
apps/web/               Next.js UI；由 task bootstrap 建立
cmd/api/ cmd/worker/     組裝、設定與生命周期，不放 business rules
internal/model/         draft/version、IR validation、commit service
internal/domainpack/    電子產線 pack（P0，H-016）與 SoftwareDeliveryDomainPack（範例）；只透過接口連接 generic kernel
internal/simulation/    pure Go DES；CPU/memory/time bounded
internal/ai/            context、provider adapter、proposal、analyst；無 mutation tool
internal/import/        upload/profile/map/validate/freeze/inference
internal/auth/ tenant/  JWT + membership + policy
internal/results/       aggregates、comparison、trace artifact
internal/audit/ storage/ 受控 persistence 與 object storage adapters
schemas/ openapi/       先改 contract，再改 handlers/clients
migrations/             已部署 migration 不得改寫；新增 migration
prompts/ testdata/       prompt version、golden、negative、eval fixtures
```

原報告 Go/Next.js/React/TypeScript/React Flow/PlayCanvas/PostgreSQL 的選擇保留。版本、provider、cloud、sqlc/explicit SQL、前端 state libraries 尚未全定；不要使用 floating `latest`，在 bootstrap 時記錄所選版本、license、lockfile 和 ADR。

## 4. 執行指令與誠實狀態

本包可立即執行：

```sh
python -m pip install -r requirements-spec.txt
make spec-check           # Schema / fixture / OpenAPI structure+refs / task DAG / docs links
make spec-contracts
make spec-planning
```

bootstrap 後必須提供並實際通過：

```sh
make backend-test         # go vet + go test；無 go.mod 時必須失敗
make backend-race         # go test -race；支援平台才執行，不能假裝跑過
make frontend-test        # lint + typecheck + unit；無 package.json 時必須失敗
make integration-test    # disposable PostgreSQL + API + worker + RLS，不指向 production
make e2e-test             # browser closed-loop with deterministic fake provider
make ai-eval              # app-owned fixture suite；fake 與 live provider 結果分開
```

目前未附 app 的 targets 會回非零狀態，直到被真實命令替換。SQL migration 尚須在一次性 DB 執行；不要自動對使用者環境執行 destructive down / reset。`make spec-check` 不會執行 SQL，也不代表完整 OpenAPI 官方 validator 已通過。

## 5. 每次任務的開發流程

確認 dependencies / scope / decision gate → 先寫 negative 和 happy-path tests → contract → pure domain logic → persistence/HTTP adapter → UI adapter → security/concurrency regression → 文件與 traceability。一次只交付一個可驗收的 bounded slice，避免跨越多個未完成依賴的大型修改。

核心錯誤要有 typed error code、JSON Pointer path、request_id；context cancellation 逐層傳遞；不吞錯、不回假成功。Go maps 不可决定 simulation iteration order；RNG、time quantization、hash、event tie-break 要有固定 golden vectors。

新增任何 DB 查詢都檢查 tenant scope；新增 write 都檢查 role + audit + concurrency + idempotency（適用時）；新增 AI context 都做 redaction/injection tests；新增 API 都更新 OpenAPI + examples + generated client contract test。

## 6. PR / 任務 Definition of Done

有需求 ID、正向/反向/併發測試、migration/RLS 影響、失敗處理、觀測資訊、更新文件。實際貼出命令與結果；未執行項目列為 NOT RUN，不可列 PASS。不可把 skip/placeholder/fake provider 當 production capability。

安全 tests、immutable semantics 或 deterministic golden 失敗時不得降低 assertion／加 skip 以求綠燈。不得提交 secret、客戶原始 CSV、raw prompt、private chain-of-thought。不得替人類執行 production approval；部署到 production 需明確授權。

## 7. 範圍停止線

P0 不加入 native MES/CFX/GitHub/Jira connectors、Kafka/Redis/RabbitMQ、GraphQL、CRDT、BIM/CAD、physics simulation、3D authoring、arbitrary Python runtime、autonomous multi-agent framework 或 AI production actions。SimPy 僅 independent reference test。Rust / iTwin 只留接口，不預先重寫。
