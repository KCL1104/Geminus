# Decisions & TBD Register

版本：handoff-0.1.0；來源：原報告 v0.1（2026-10-15）。**本檔不代表人類已批准新增決策。**

## 狀態與採用方法

`SOURCE` = 報告已明確決定；`PROPOSED` = 為形成可實作合約而新增的候選；`TBD` = 報告仍未決定。
OpenAPI、Schema、SQL 中的候選細節目前都是 `PROPOSED`。可以先寫 parser、fixture、測試與 spike；在表列 gate 前，由人類更新 `planning/decisions.json` 的 status / approved_by / approved_at。不可讓 coding agent 自行填入批准者。
若不同文件出現衝突，先登記並停止受影響的變更，不能用「最新修改的檔案」自動覆蓋原始需求。標準契約的相容性查核見 sources/SOURCE_MAP.md；外部標準不構成新的產品需求。

T-001（2026-10-15）盤點出的跨文件矛盾與未定義行為列於 [CONTRACT_BLOCKERS.md](CONTRACT_BLOCKERS.md)（CB-01–CB-36），每項標示對應的 D-entry 或 NEW；task 與 decision gate 的對照由 `make spec-decisions` 產生於 `checks/decisions-check.json`。

## 原報告已鎖定的邊界

Go modular monolith + separate worker；Next.js / React / TypeScript；React Flow 2D authoring；PlayCanvas 3D read-only viewer；PostgreSQL + P0 PostgreSQL jobs；REST-first；Generic Twin IR + Domain Pack；native Go DES；AI proposal-only + deterministic validation + human approval；immutable versions；Scenario patch 不修改 canonical version。P1 的 broker、connectors、GraphQL、3D authoring、Rust engine、iTwin adapter 不可偷渡進 P0。

## 需要確認的工程提案

### D-001 — 識別碼與欄位命名

**狀態：PROPOSED。負責角色：Contracts / Platform。Gate：開始產生 SDK 或執行 migration 前。**

來源缺口（原報告 pp. 14–19、24、29–36）：報告 API/IR 使用 mdl_/mv_/org_ 前綴，ER 圖則寫 UUID；Proposal 同時出現 base_model_version_id 與 base_version_id。

候選決策：本草案保留 opaque prefixed string IDs；SQL 使用 text。HTTP/Proposal 統一 base_version_id；不得接受兩個互相衝突的別名。IR schema_version 保留 0.1。

### D-002 — Proposal 的狀態與信任邊界

**狀態：PROPOSED。負責角色：AI / Platform。Gate：AI persistence 前。**

來源缺口（原報告 pp. 23–26、32–33）：報告列出 generating、ready、approved、rejected；prompt 要求 needs_input，但範例 Schema 沒有完整定義。

候選決策：分開 provider output 與 server envelope。補 needs_input、invalid、failed、conflicted。provider 不得供應可信 ID、validation 或 approval 欄位；全部由 server 綁定。

### D-003 — 發布、審核與未發布 Draft 的交互作用

**狀態：PROPOSED。負責角色：Platform / Product。Gate：Version service 前。**

來源缺口（原報告 pp. 18–19、27、31–33）：報告要求 immutable versions、optimistic concurrency、不得偷偷 rebase，但未說明 AI approval 遇到 dirty draft 的行為。

候選決策：Publish 與 approve 鎖同一 model row。審核只對 published base 生效；clean draft 可同步至新版本，dirty draft 完全保留並標成 out-of-date。stale draft publish 回 409；reset 需明確確認與 If-Match。

### D-004 — Scenario Proposal 的批准結果

**狀態：PROPOSED。負責角色：AI / Simulation。Gate：Scenario 開發前。**

來源缺口（原報告 pp. 19、23、33–35、51）：報告說 Scenario 是 immutable version 上的 patch，不建立 canonical ModelVersion；Scenario Designer 沒有獨立 API/持久化合約。

候選決策：Proposal mode=edit 或 scenario。批准 edit 建立一個 ModelVersion；批准 scenario 建立 immutable Scenario、不得新增 ModelVersion。scenario 必須由使用者另行啟動 run。

### D-005 — DES 的未定義操作語義

**狀態：PROPOSED。負責角色：Simulation。Gate：建立 golden digest 前。**

來源缺口（原報告 pp. 20–22、34、45–46）：報告未固定 tick 精度、同時間事件優先序、warmup cohort、bundle acquire、zero-time cycles、queue node 與 process waiting 的關係。

候選決策：採 SIMULATION_SPEC.md 的候選語義：1 ms ticks、穩定 event order、atomic resource bundle、顯式 queue 作 downstream process 的等待位置、拒絕 zero-time cycles。

### D-006 — Hash、dataset freezing 與 RNG

**狀態：PROPOSED。負責角色：Simulation / Platform。Gate：版本 hash 上線前。**

來源缺口（原報告 pp. 15–22、34–35）：報告要求 hash、seed、deterministic digest；未固定 canonicalization、PRNG、empirical dataset revision 與 engine numerical profile。

候選決策：候選採 RFC 8785 JCS + SHA-256；IR hash 與 dataset manifest 分別保存。run freeze dataset content hashes。SIMULATION_SPEC 固定 seed derivation、SplitMix64、numeric profile 與 digest payload。

### D-007 — 初始工程限制與預設

**狀態：PROPOSED。負責角色：Platform / Simulation。Gate：Alpha / Beta 設定凍結前。**

來源缺口（原報告 pp. 7–8、37、50）：報告提供帶 TBD 的 graph/upload/replication/timeout 目標，未定義 event cap、trace cap、lease、idempotency TTL。

候選決策：原報告的 500 nodes / 1,000 edges、50 MB / 100k rows、1,000 replications、300 s timeout 僅作初始上限候選。另提案 event budget=10M/run、active entities=100k、trace=100k/run、lease=30s、heartbeat=10s、idempotency TTL=24h；均需 benchmark/評審。

### D-008 — Tenant context、onboarding 與 worker scope

**狀態：PROPOSED。負責角色：Security / Platform。Gate：任何共享 DB 服務啟動前。**

來源缺口（原報告 pp. 29、36–41）：報告有 membership/RLS/queue，但沒有 HTTP tenant selection、membership endpoints、bootstrap、RLS 下的跨 tenant worker claim 規格。

候選決策：X-Organization-ID 只作選擇器；server 以 JWT subject 查 membership。初始 org/owner 由受控 provisioning 完成。P0 worker 由 deployment 提供 assigned organization allowlist，逐 tenant SET LOCAL + claim，沒有 BYPASSRLS。

### D-009 — Import commit 的邊界

**狀態：PROPOSED。負責角色：Import / Product。Gate：Import commit 前。**

來源缺口（原報告 pp. 6–7、30、35–36、48、51）：報告把 import commit 寫成 import into model，但最後閉環要求 inferred model 先 proposal/approval；未明確區分 dataset commit 與 Twin mutation。

候選決策：commit 只凍結 validated dataset 並排入 inference/proposal；不直接 publish Twin。CSV 與 JSON model 都經同一審核入口；人工 2D 編輯仍可明確 publish。

### D-010 — 結果、缺值、DORA 與比較

**狀態：PROPOSED。負責角色：Simulation / Domain / Product。Gate：Results contract 前。**

來源缺口（原報告 pp. 2、4、7、26、34–35、44–46）：報告示例沒有 metric population、warmup/censoring、confidence method、baseline=0、DORA required events 的完整規格。

候選決策：保留 throughput/lead_time_minutes/resources/queues 結構，補 status、sample_count、population、CI、metric evidence。DORA 缺 stage/event mapping 就 unavailable；不把一般 process cycle time 自動當 DORA。

### D-011 — Async、重試、取消與冪等

**狀態：PROPOSED。負責角色：Platform。Gate：Worker queue 前。**

來源缺口（原報告 pp. 7、27–28、32–37）：報告提供 202/SSE/cancellation/SKIP LOCKED/Idempotency-Key，但未指定 transition、lease recovery、duplicate response、completion fencing。

候選決策：採 API_DATABASE.md 的 CAS + lease_token、same-key/same-body replay、different-body 409、cancel checkpoint、result atomic commit。analysis 與 proposal 也走 durable job；不在 HTTP request 開長交易。

### D-012 — HTTP 合約補全

**狀態：PROPOSED。負責角色：Platform / Frontend。Gate：HTTP codegen 前。**

來源缺口（原報告 pp. 29–35、40）：報告只列核心 endpoint，未列 collection list、scenario、mapping suggestion、import error report、playback、analysis read、draft reset、membership 管理。

候選決策：OpenAPI 對新增 routes 標 x-origin=PROPOSED；供 P0 UI 閉環使用，不新增 connectors、3D authoring 或 autonomous action。

### D-013 — SQL 完整性與生命周期

**狀態：PROPOSED。負責角色：Security / Platform。Gate：Migration review 前。**

來源缺口（原報告 pp. 36–43）：報告 ER 圖沒有 complete columns、cross-tenant FK、immutable triggers、dataset revisions、idempotency、analysis/job constraints。

候選決策：migration 為完整性候選草案：composite tenant+parent FKs、FORCE RLS、runtime grants、immutable guards、job/run inputs guard。RBAC、人類審核與 last-owner invariant 仍由 API transaction 驗證。

### D-014 — Schema profile 與校驗層

**狀態：PROPOSED。負責角色：Contracts / Security。Gate：Parser / validator 前。**

來源缺口（原報告 pp. 14–18、24–26、42）：報告給出 IR 範例，未提供完整 schema，也未區分 schema-valid 與 semantically valid。

候選決策：提供 JSON Schema 2020-12 closed-object profile；JSON Patch P0 allowlist 為 add/remove/replace/test，拒绝 move/copy/root patch。Graph/data refs/risk/authorization 由 deterministic semantic validator 檢查。

### D-015 — OpenAPI dialect

**狀態：PROPOSED。負責角色：Contracts。Gate：SDK generator 選型前。**

來源缺口（原報告 pp. 29）：報告要求 OpenAPI，未固定 OpenAPI 小版本。

候選決策：本草案選 OpenAPI 3.1.1，與 JSON Schema 2020-12 協作；不是聲稱 3.1.1 為最新版本。產生 SDK 前驗證所選工具對外部 $ref 的支援。

### D-016 — AI 風險及有限工具

**狀態：PROPOSED。負責角色：AI / Security。Gate：AI approval 前。**

來源缺口（原報告 pp. 24–26、41–43、46）：報告要求 path/operation allowlists、risk classification，未提供完整規則。

候選決策：AI read-only tools 只接受 server-issued scoped IDs；risk 由 server 計算且不能被模型下調。刪除 node/resource=high；降 capacity/new cycle/增 failure probability 至少 medium；confidence 不是授權條件。

### D-017 — 範例、時間尺度與模板

**狀態：PROPOSED。負責角色：Domain / Product。Gate：Default template 發布前。**

來源缺口（原報告 pp. 3、6、15–18）：報告完整 JSON 實為 Source→Review→CI→Decision→Sink；P0 story 則要求包含 Test/Deploy。mean=16 minutes 不等於連續 24 小時每天 30 件。

候選決策：保留報告為來源，不把示例數字稱為實测。另提供有明確 synthetic assumptions 的 full pipeline fixture；P0 day=24h，不暗加 8-hour calendar。每日 30 件的連續 arrival mean=48 min。

### D-018 — LLM credential pool（兩個獨立 Anthropic organization）

**狀態：APPROVED（2026-10-15，KCL1104：選擇 failover）。負責角色：AI / Security。Gate：T-024 provider adapter 實作前；任何 live AI 呼叫前。**

來源缺口：H-001 指定 Anthropic Claude API，並提供兩個分屬獨立 organization 的 API key，要求 rotation 以使用兩邊 credits。原報告與 AI_SPEC 只假設單一 provider credential，未定義多帳號選用、失效切換、資料政策一致性與 audit。

候選決策：有序 primary→secondary failover。只有 402 `billing_error`（credits 用完）或 401/403（key 無效／撤銷）才把該 credential 標為不可用並切換；429/529/5xx 只在同一 credential 依 SDK backoff 重試，不跨 organization，以免實質合併兩個 organization 的 rate limit（是否符合 Anthropic 條款需人類確認）。Key 只以 label 出現在 proposal／analysis／audit；adapter 明確注入 key，不依賴 `ANTHROPIC_API_KEY`；兩個 organization 的 retention／ZDR 設定須一致並寫入資料政策；key 只存 secret manager。

## 人類決策紀錄

由 agent 依 repository owner 的明確指示記錄，措辭待 owner 於 PR review 確認；機器可讀版本在 `planning/decisions.json` 的 `human_decisions`。

| ID | 日期 | 決定 | 仍未決 |
|---|---|---|---|
| H-001 | 2026-10-15 | LLM provider 改為 Anthropic Claude API（Claude Console API key），保留 provider interface；兩個 key 分屬獨立 organization，需 rotation 使用兩邊 credits | LLM model（eval 後）、retention／資料政策、token quota |
| H-002 | 2026-10-15 | 確認 Go backend；與 ADR-001 一致（`net/http`，不引入重型 framework），無變更 | — |

## 原報告保留的 TBD

| 項目 | 原報告建議／限制 | 決定時間／阻擋範圍 |
|---|---|---|
| LLM provider | ~~OpenAI adapter first + provider interface，未定案~~ **已由 H-001 決定：Anthropic Claude API** | 真實 provider integration 仍受 D-018、資料政策、token quota 阻擋 |
| LLM model | eval 後決定，不 hard-code | Week 7；live AI release |
| Auth provider | managed OIDC | Week 1；production login |
| Cloud / managed DB | GCP reference / AWS equivalent；managed PostgreSQL | Week 1–2；production deploy |
| Object storage | cloud-native / S3-compatible abstraction | Week 2；持久化 artifact |
| Frontend state / UI libraries | TanStack Query、Zustand、Tailwind/shadcn 原文均 TBD | Frontend bootstrap；不影響 IR |
| SQL generation | explicit SQL / sqlc TBD | Backend bootstrap |
| Model / upload / replications / timeout | 原報告初始目標，不是量測成果 | Alpha benchmark |
| AI token / tenant quota | per operation / tenant，數值未定 | AI Alpha；無上限時不得開 live AI |
| Reference device / server / load workload | 未決定 | Performance gate 不得宣告已過 |
| Retention / residency | beta 前決定 | 未批准不得放真實客戶資料進 provider / production |
| Billing / collaboration / second domain / Rust / iTwin | P1 或 post-MVP | 不阻擋 P0，不擅自實作 |

## 不得被假設取代的資料

實際 reviewer service distribution、stage/event taxonomy、DORA measurement boundaries、客戶部署/失敗/復原語義、業務時間 calendar 均非報告已提供的客戶資料。缺少時回 needs_input 或 unavailable。測試 fixture 中的數值只能用於測試，不能用來補使用者資料。
