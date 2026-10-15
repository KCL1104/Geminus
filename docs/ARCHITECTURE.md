# Architecture & Module Responsibilities

SOURCE：原報告 pp. 8–14、19–23、27–28、36–43、49–53。補充 transaction、worker isolation 與接口參數為工程補充（D-003、D-006、D-008–D-013，皆已批准）。

## 1. Runtime topology

採 modular monolith + separate worker，對應原報告第 8 頁架構圖。P0 不拆成多個 microservices。

```mermaid
flowchart LR
  UI[Next.js Web App] --> API[Go REST API]
  UI --- R2[React Flow Authoring Adapter]
  UI --- R3[PlayCanvas Read-only Adapter]
  API --> AUTH[Auth / Tenant / Policy]
  API --> MODEL[Model / Version / Proposal Commit]
  API --> IMPORT[Import / Mapping]
  API --> JOB[Job Enqueue]
  MODEL --> DB[(PostgreSQL + RLS)]
  IMPORT --> DB
  JOB --> DB
  WORKER[Go Worker] --> DB
  WORKER --> DES[Generic DES / Metrics]
  WORKER --> AI[AI Orchestrator / Provider Adapter]
  AI --> LLM[LLM Provider TBD]
  API --> OBJ[(Private Object Storage)]
  WORKER --> OBJ
  API --> OTEL[Traces / Metrics + Structured Logs]
  WORKER --> OTEL
```

箭頭是受控調用，不表示 LLM 可反向寫資料庫。API 與 worker 共用純 domain packages、schema 與 storage interfaces；runtime credentials 和 grants 分開。LLM SDK type 不得出現在 domain signatures。

## 2. 模組責任與禁止依賴

| Package / feature | Owns | 不得負責 |
|---|---|---|
| internal/auth | JWT verification、subject extraction、issuer/audience/expiry | 信任 client role／選擇任意 tenant |
| internal/tenant | membership、organization authorization、transaction context | 以 hidden UI button 代替授權 |
| internal/project | tenant project lifecycle | 直接處理 IR 或 DES |
| internal/model | IR validation、draft CAS、versions、canonical commit | provider-specific prompt、renderer layout runtime |
| internal/domainpack | catalog、domain rules、event inference、domain metrics、prompt context、default views | 修改 generic engine source 才能加 domain |
| internal/import | file profile、column map、row validation、frozen datasets、inference proposal | 未批准直接 publish Twin |
| internal/ai | sanitized context、provider interface、typed outputs、validation orchestration | 直接持有 ModelCommitter、SQL handle、production tool |
| internal/simulation | heap、clock、RNG、resource queues、routing、metrics | HTTP/DB/network、tenant discovery、UI |
| internal/results | aggregation、comparison、evidence catalog、trace artifacts | AI 自己计算未供應的實際數值 |
| internal/audit | append-only events、request correlation | raw prompts/secrets/chain-of-thought |
| internal/storage | tenant-aware repos、object adapter、transactions | 將 untrusted organization_id 自動當授權 |
| cmd/api / cmd/worker | composition root、config、lifecycle | 隱藏 business rule 或不同版本 validator |
| apps/web/features | editor、import、proposals、simulations、results、3D | 第二套 topology、server authorization |

## 3. 四個穩定 contracts

**Model IR**：JSON + schemas/model-ir.schema.json；UI、AI、import、engine 與 future domain 的唯一 model interchange。React Flow nodes/edges 只是投影。

**DomainPack**（保留原報告接口概念）：

```go
type DomainPack interface {
    ID() string
    NodeCatalog() []NodeDefinition
    ResourceCatalog() []ResourceDefinition
    Validate(ModelIR) []ValidationIssue
    InferFromEvents(context.Context, CanonicalEventDataset) (*InferenceResult, error)
    ComputeMetrics(SimulationResult) map[string]Metric
    PromptContext() DomainPromptContext
    DefaultViews(ModelIR) ViewDefinitions
}
```

T-011 實作（`internal/domainpack`）：介面保留上列方法；packs 由 process 明確註冊進 `Registry`，未註冊的 domain 回 `ErrUnsupportedDomain`。`SoftwareDeliveryDomainPack` 的 `InferFromEvents` 在 T-027 前回 `ErrUnavailable`（不假裝推論）；`ComputeMetrics` 在沒有已批准的 deployment／failure／recovery mapping 時把五個 DORA 指標全部標為 unavailable 並附原因（D-010）；`DefaultViews` 保留既有 2D 位置，其餘以 BFS 分層的決定性排版補齊；建立 model 用的 template 是 D-017 synthetic full pipeline（與 `testdata/golden-models/default-delivery.json` 由測試保持一致）。

**SimulationEngine**（原報告接口）：

```go
type SimulationEngine interface {
    Validate(context.Context, ModelIR) ([]ValidationIssue, error)
    Run(context.Context, ModelIR, RunConfig, ProgressSink) (*SimulationResult, error)
}
```

APPROVED D-006：worker 在呼叫前組裝 immutable `RunInput`，包含 ModelIR、resolved empirical datasets、dataset manifest、effective model hash 與 frozen config。可以把 resolved dataset bindings 放入 RunConfig 的 internal-only 欄位或一個 explicit RunInput；**不能讓 pure engine 在 Run() 中查 DB／object storage**。T-003 必須確認 Go interface 的最終編碼，不自行隱藏可變全域 resolver。

**ModelChangeProposal**：provider 只能供應內容；server 綁定 identity/base/mode/status/validation，經人類批准才觸發 canonical commit。Schema 分 provider output、stored proposal、approval response，避免把 readOnly 註解當 security boundary。

## 4. Model / draft / version / scenario 生命週期

Create 以單一 DB transaction 建 model、valid template v1、draft revision=1/base=v1。Draft 可改，但每次 mutation 帶 If-Match。Publish 鎖 model row，驗 base/latest 與 revision，建立 parent-linked version、同步 draft。Version content 永不 UPDATE。

APPROVED D-003：approval 的 lock order 為 model → draft → proposal → idempotency completion（idempotency reservation 先做，但不得與其他操作造成反向鎖順序）。先完成授權與 key reservation，再以一致的 domain lock order 執行。若 draft content 與其 base 完全相同且 base=latest，可同步新版本並增加 revision；dirty draft 不動，回應 `draft_out_of_date=true`。下一次 publish stale draft 必須顯式處理，不會偷偷 rebase。

Scenario 是 ModelVersion + ordered JSON Patch 的 immutable experiment description。AI scenario approval 寫 scenario，不寫 ModelVersion；run 在 enqueue 時 freeze patch，不從 mutable scenario/config 取得資料。

## 5. Request / transaction boundary

APPROVED D-008：每個 tenant request 的順序為 verify JWT →取得 subject→begin transaction→SET LOCAL organization_id/user_id→查 membership→role check→scoped query + RLS。X-Organization-ID 只是 tenant selector，header 不含可信 role。resource 的 org 與 URL parent 必須一致；其他 tenant 的 ID 回 not found，不揭露其存在。

不能在 DB transaction 中等待 LLM 或跑 DES。enqueue：transaction 內建立 resource/status + job + audit + idempotency response → commit →202。worker 讀 snapshot、commit claim →transaction 外執行→用 lease fence 的 transaction commit result。

## 6. Queue / storage / isolation

PostgreSQL jobs 使用 `FOR UPDATE SKIP LOCKED`，P0 不加入 broker。APPROVED D-008：worker 只輪詢 deployment 指派的 organization allowlist，逐 tenant 設 transaction-local context；**不提供跨 tenant 的 unrestricted jobs SELECT，也不授 BYPASSRLS**。之後若需要 dispatcher，用獨立 ADR 設計最小權限接口，不把 security-definer global scanner 偷塞進 worker。

大檔與 trace/result artifact 放 private object storage，DB 放 metadata、hash、organization_id、authorized object key。client 只能持有 resource ID，不可送任意 bucket/key/url 讓 server 讀取。empirical datasets 在編譯前下載驗 hash、轉換 units、freeze；run 中斷網不影響純 DES。

## 7. Frontend adapters

server state（versions、proposals、jobs）與 unsaved draft/editor state 分開（T-014 實作見 ADR-016：`apps/web/lib/api` 為唯一 HTTP 出口、`lib/state/draft.ts` 分開 remote／in-flight／unsaved，409 保留本機編輯且不自動 rebase）。ViewAdapter 以 IR ID 建立 React Flow nodes/edges；2D positions 是 views 的資料，不是 domain semantics。3D derives topology 與 layout，不可回寫 graph；same selectedEntityId store 支援切換 renderer。Playback loader 與 aggregate results 分離，可按需取 authorized sampled trace。

AI review 必須顯示 base version、candidate effective diff、server-computed risk、assumptions、validation。Scenario approval 與 publish 的 UI copy 不可混淆。所有 pending/needs_input/invalid/conflicted 狀態都有明確下一步，不把 provider timeout 當空 patch success。

## 8. Deployment / observability / failure modes

部署單位：web、api、worker、managed PostgreSQL、object storage、OTLP backend。cloud/provider/retention TBD；Cloud Run 僅原報告 reference，不寫進 domain code。structured JSON logs + trace correlation；不得依賴原報告當時對特定 SDK 版本的時效敘述。

Provider fail：proposal/analysis failed，不 mutation。DB commit fail：不回 successful 202/201。Worker crash：lease 回收、same frozen input retry。Artifact upload 成功但 DB commit fail：無可見 completed result，孤兒 artifact 由保留政策清理。Stale lease worker：不能 finalize。取消：checkpoint 後不產生 completed metrics；partial debug state 不被 analyst 當 final results。

## 9. 架構演進條件

保留原報告 trigger：代表性模型超出 SLO、profiling 證明 >60% runtime 是 DES compute、演算法與資料結構調整仍無效，才 benchmark Rust prototype。Broker 的導入依 queue latency / sustained throughput / cross-region / fanout 實測；不能因偏好而先引入。第二 domain 的測試以 minimal test pack 證明 generic boundary；不等於 P0 要交付 manufacturing。
