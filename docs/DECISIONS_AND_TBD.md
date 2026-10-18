# Decisions & TBD Register

版本：handoff-0.1.0；來源：原報告 v0.1（2026-10-15）。各決策是否已批准以本檔各節狀態與 `planning/decisions.json` 為準；D-001–D-037 於 2026-10-15、D-038–D-039 於 2026-10-18 由 owner 批准；D-040、D-041（T-012 review 發現，gate T-013）為 PROPOSED，待 owner 決定。

## 狀態與採用方法

`SOURCE` = 報告已明確決定；`PROPOSED` = 為形成可實作合約而新增的候選；`TBD` = 報告仍未決定。
OpenAPI、Schema、SQL 中的候選細節目前都是 `PROPOSED`。可以先寫 parser、fixture、測試與 spike；在表列 gate 前，由人類更新 `planning/decisions.json` 的 status / approved_by / approved_at。不可讓 coding agent 自行填入批准者。
若不同文件出現衝突，先登記並停止受影響的變更，不能用「最新修改的檔案」自動覆蓋原始需求。標準契約的相容性查核見 sources/SOURCE_MAP.md；外部標準不構成新的產品需求。

T-001（2026-10-15）盤點出的跨文件矛盾與未定義行為列於 [CONTRACT_BLOCKERS.md](CONTRACT_BLOCKERS.md)（CB-01–CB-36），每項標示對應的 D-entry 或 NEW；task 與 decision gate 的對照由 `make spec-decisions` 產生於 `checks/decisions-check.json`。

## 原報告已鎖定的邊界

Go modular monolith + separate worker；Next.js / React / TypeScript；React Flow 2D authoring；PlayCanvas 3D read-only viewer；PostgreSQL + P0 PostgreSQL jobs；REST-first；Generic Twin IR + Domain Pack；native Go DES；AI proposal-only + deterministic validation + human approval；immutable versions；Scenario patch 不修改 canonical version。P1 的 broker、connectors、GraphQL、3D authoring、Rust engine、iTwin adapter 不可偷渡進 P0。

## 需要確認的工程提案

### D-001 — 識別碼與欄位命名

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Contracts / Platform。Gate：開始產生 SDK 或執行 migration 前。**

來源缺口（原報告 pp. 14–19、24、29–36）：報告 API/IR 使用 mdl_/mv_/org_ 前綴，ER 圖則寫 UUID；Proposal 同時出現 base_model_version_id 與 base_version_id。

候選決策：本草案保留 opaque prefixed string IDs；SQL 使用 text。HTTP/Proposal 統一 base_version_id；不得接受兩個互相衝突的別名。IR schema_version 保留 0.1。

### D-002 — Proposal 的狀態與信任邊界

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：AI / Platform。Gate：AI persistence 前。**

來源缺口（原報告 pp. 23–26、32–33）：報告列出 generating、ready、approved、rejected；prompt 要求 needs_input，但範例 Schema 沒有完整定義。

候選決策：分開 provider output 與 server envelope。補 needs_input、invalid、failed、conflicted。provider 不得供應可信 ID、validation 或 approval 欄位；全部由 server 綁定。

### D-003 — 發布、審核與未發布 Draft 的交互作用

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Platform / Product。Gate：Version service 前。**

來源缺口（原報告 pp. 18–19、27、31–33）：報告要求 immutable versions、optimistic concurrency、不得偷偷 rebase，但未說明 AI approval 遇到 dirty draft 的行為。

候選決策：Publish 與 approve 鎖同一 model row。審核只對 published base 生效；clean draft 可同步至新版本，dirty draft 完全保留並標成 out-of-date。stale draft publish 回 409；reset 需明確確認與 If-Match。

### D-004 — Scenario Proposal 的批准結果

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：AI / Simulation。Gate：Scenario 開發前。**

來源缺口（原報告 pp. 19、23、33–35、51）：報告說 Scenario 是 immutable version 上的 patch，不建立 canonical ModelVersion；Scenario Designer 沒有獨立 API/持久化合約。

候選決策：Proposal mode=edit 或 scenario。批准 edit 建立一個 ModelVersion；批准 scenario 建立 immutable Scenario、不得新增 ModelVersion。scenario 必須由使用者另行啟動 run。

### D-005 — DES 的未定義操作語義

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Simulation。Gate：建立 golden digest 前。**

來源缺口（原報告 pp. 20–22、34、45–46）：報告未固定 tick 精度、同時間事件優先序、warmup cohort、bundle acquire、zero-time cycles、queue node 與 process waiting 的關係。

候選決策：採 SIMULATION_SPEC.md 的候選語義：1 ms ticks、穩定 event order、atomic resource bundle、顯式 queue 作 downstream process 的等待位置、拒絕 zero-time cycles。

### D-006 — Hash、dataset freezing 與 RNG

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Simulation / Platform。Gate：版本 hash 上線前。**

來源缺口（原報告 pp. 15–22、34–35）：報告要求 hash、seed、deterministic digest；未固定 canonicalization、PRNG、empirical dataset revision 與 engine numerical profile。

候選決策：候選採 RFC 8785 JCS + SHA-256；IR hash 與 dataset manifest 分別保存。run freeze dataset content hashes。SIMULATION_SPEC 固定 seed derivation、SplitMix64、numeric profile 與 digest payload。

### D-007 — 初始工程限制與預設

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Platform / Simulation。Gate：Alpha / Beta 設定凍結前。**

來源缺口（原報告 pp. 7–8、37、50）：報告提供帶 TBD 的 graph/upload/replication/timeout 目標，未定義 event cap、trace cap、lease、idempotency TTL。

候選決策：原報告的 500 nodes / 1,000 edges、50 MB / 100k rows、1,000 replications、300 s timeout 僅作初始上限候選。另提案 event budget=10M/run、active entities=100k、trace=100k/run、lease=30s、heartbeat=10s、idempotency TTL=24h；均需 benchmark/評審。

### D-008 — Tenant context、onboarding 與 worker scope

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Security / Platform。Gate：任何共享 DB 服務啟動前。**

來源缺口（原報告 pp. 29、36–41）：報告有 membership/RLS/queue，但沒有 HTTP tenant selection、membership endpoints、bootstrap、RLS 下的跨 tenant worker claim 規格。

候選決策：X-Organization-ID 只作選擇器；server 以 JWT subject 查 membership。初始 org/owner 由受控 provisioning 完成。P0 worker 由 deployment 提供 assigned organization allowlist，逐 tenant SET LOCAL + claim，沒有 BYPASSRLS。

### D-009 — Import commit 的邊界

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Import / Product。Gate：Import commit 前。**

來源缺口（原報告 pp. 6–7、30、35–36、48、51）：報告把 import commit 寫成 import into model，但最後閉環要求 inferred model 先 proposal/approval；未明確區分 dataset commit 與 Twin mutation。

候選決策：commit 只凍結 validated dataset 並排入 inference/proposal；不直接 publish Twin。CSV 與 JSON model 都經同一審核入口；人工 2D 編輯仍可明確 publish。

### D-010 — 結果、缺值、DORA 與比較

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Simulation / Domain / Product。Gate：Results contract 前。**

來源缺口（原報告 pp. 2、4、7、26、34–35、44–46）：報告示例沒有 metric population、warmup/censoring、confidence method、baseline=0、DORA required events 的完整規格。

候選決策：保留 throughput/lead_time_minutes/resources/queues 結構，補 status、sample_count、population、CI、metric evidence。DORA 缺 stage/event mapping 就 unavailable；不把一般 process cycle time 自動當 DORA。

### D-011 — Async、重試、取消與冪等

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Platform。Gate：Worker queue 前。**

來源缺口（原報告 pp. 7、27–28、32–37）：報告提供 202/SSE/cancellation/SKIP LOCKED/Idempotency-Key，但未指定 transition、lease recovery、duplicate response、completion fencing。

候選決策：採 API_DATABASE.md 的 CAS + lease_token、same-key/same-body replay、different-body 409、cancel checkpoint、result atomic commit。analysis 與 proposal 也走 durable job；不在 HTTP request 開長交易。

### D-012 — HTTP 合約補全

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Platform / Frontend。Gate：HTTP codegen 前。**

來源缺口（原報告 pp. 29–35、40）：報告只列核心 endpoint，未列 collection list、scenario、mapping suggestion、import error report、playback、analysis read、draft reset、membership 管理。

候選決策：OpenAPI 對新增 routes 標 x-origin=PROPOSED；供 P0 UI 閉環使用，不新增 connectors、3D authoring 或 autonomous action。

### D-013 — SQL 完整性與生命周期

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Security / Platform。Gate：Migration review 前。**

來源缺口（原報告 pp. 36–43）：報告 ER 圖沒有 complete columns、cross-tenant FK、immutable triggers、dataset revisions、idempotency、analysis/job constraints。

候選決策：migration 為完整性候選草案：composite tenant+parent FKs、FORCE RLS、runtime grants、immutable guards、job/run inputs guard。RBAC、人類審核與 last-owner invariant 仍由 API transaction 驗證。

### D-014 — Schema profile 與校驗層

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Contracts / Security。Gate：Parser / validator 前。**

來源缺口（原報告 pp. 14–18、24–26、42）：報告給出 IR 範例，未提供完整 schema，也未區分 schema-valid 與 semantically valid。

候選決策：提供 JSON Schema 2020-12 closed-object profile；JSON Patch P0 allowlist 為 add/remove/replace/test，拒绝 move/copy/root patch。Graph/data refs/risk/authorization 由 deterministic semantic validator 檢查。

### D-015 — OpenAPI dialect

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Contracts。Gate：SDK generator 選型前。**

來源缺口（原報告 pp. 29）：報告要求 OpenAPI，未固定 OpenAPI 小版本。

候選決策：本草案選 OpenAPI 3.1.1，與 JSON Schema 2020-12 協作；不是聲稱 3.1.1 為最新版本。產生 SDK 前驗證所選工具對外部 $ref 的支援。

### D-016 — AI 風險及有限工具

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：AI / Security。Gate：AI approval 前。**

來源缺口（原報告 pp. 24–26、41–43、46）：報告要求 path/operation allowlists、risk classification，未提供完整規則。

候選決策：AI read-only tools 只接受 server-issued scoped IDs；risk 由 server 計算且不能被模型下調。刪除 node/resource=high；降 capacity/new cycle/增 failure probability 至少 medium；confidence 不是授權條件。

### D-017 — 範例、時間尺度與模板

**狀態：APPROVED（2026-10-15，KCL1104）。負責角色：Domain / Product。Gate：Default template 發布前。**

來源缺口（原報告 pp. 3、6、15–18）：報告完整 JSON 實為 Source→Review→CI→Decision→Sink；P0 story 則要求包含 Test/Deploy。mean=16 minutes 不等於連續 24 小時每天 30 件。

候選決策：保留報告為來源，不把示例數字稱為實测。另提供有明確 synthetic assumptions 的 full pipeline fixture；P0 day=24h，不暗加 8-hour calendar。每日 30 件的連續 arrival mean=48 min。

### D-018 — LLM credential pool（兩個獨立 Anthropic organization）

**狀態：APPROVED（2026-10-15，KCL1104：選擇 failover）。負責角色：AI / Security。Gate：T-024 provider adapter 實作前；任何 live AI 呼叫前。**

來源缺口：H-001 指定 Anthropic Claude API，並提供兩個分屬獨立 organization 的 API key，要求 rotation 以使用兩邊 credits。原報告與 AI_SPEC 只假設單一 provider credential，未定義多帳號選用、失效切換、資料政策一致性與 audit。

候選決策：有序 primary→secondary failover。只有 402 `billing_error`（credits 用完）或 401/403（key 無效／撤銷）才把該 credential 標為不可用並切換；429/529/5xx 只在同一 credential 依 SDK backoff 重試，不跨 organization，以免實質合併兩個 organization 的 rate limit（是否符合 Anthropic 條款需人類確認）。Key 只以 label 出現在 proposal／analysis／audit；adapter 明確注入 key，不依賴 `ANTHROPIC_API_KEY`；兩個 organization 的 retention／ZDR 設定須一致並寫入資料政策；key 只存 secret manager。

## T-001 阻擋項決議（2026-10-15，KCL1104 逐項選擇）

D-001 另定：伺服器產生的資源 ID 採「前綴 + ULID」（併解 CB-09）。以下為新登記且已批准的決議；CB-13 以後的項目會在各自任務開工前再逐項詢問。

| ID | 解決 | 主題 | 決議 |
|---|---|---|---|
| D-019 | CB-01 | Hash 輸入定義 | patch_hash = SHA-256(JCS(patch 陣列))，baseline `[]` 亦有值；dataset content_hash = 儲存物件原始 bytes 的 SHA-256，snapshot JSON 不內含自身 hash；config_hash 先把 duration 正規化為 ticks 再算。以 D-006 的 JCS+SHA-256 為前提；D-006 若改 canonicalization 須重議。 |
| D-020 | CB-02 | JSON Patch test op 與 allowlist 比對 | test 可指向任何既有路徑，不算 business mutation、不受 path allowlist 限制；server 強制「修改 array 元素前必須先 test 該元素 id」；allowlist 以解碼後 token 做前綴比對。 |
| D-021 | CB-02 | 人工 scenario 的修改範圍 | 人工 scenario 與 AI scenario 使用同一 allowlist：只能改既有 capacity、分布參數、routing 機率，不能增刪拓樸；結構變更走 draft→publish。 |
| D-022 | CB-03 | AI 刪除 node 時的 view 座標 | AI edit allowlist 只額外允許 `remove /views/2d/positions/{id}`，且 id 必須是同一 patch 中被刪除的 node；審核 patch 與 hash 不另加 server 衍生 op。 |
| D-023 | CB-04 | Validator 輸出決定性 | 同一階段收集全部 issue，該階段有 error 即不進下一階段；issues 依 (path, code) 排序；重疊時 VIEW_REFERENCE_INVALID 優先於 UNKNOWN_REFERENCE、ROUTING_INVALID 優先於 CONSTRAINT_FAILED；補 patch／run config／tick overflow／runtime limit 的 code。 |
| D-024 | CB-05 | HTTP Error.details 承載 issue code／severity | Error.details[] 增加選填 code、severity；保留報告 p.29 錯誤外形。 |
| D-025 | CB-06 | HTTP error code 封閉清單 | T-003 建立 error code enum；缺 Idempotency-Key → 400 IDEMPOTENCY_KEY_REQUIRED；缺 If-Match → 428；Content-Type 錯誤 → 415。 |
| D-026 | CB-07 | Analyst provider output 與 server 紀錄分離 | 新增不含 run ID 的 analysis-output schema；server 注入 run_id／baseline_run_id；evidence 以角色（baseline／scenario）引用。 |
| D-027 | CB-08 | Proposal risk：provider claim 與 server risk 分離 | provider 值另存為 provider_risk_claim；最終 risk_level = max(server 計算, provider claim)，模型只能調高。 |
| D-028 | CB-10 | Draft 驗證時機與 minimal IR | PATCH draft 只要求 schema-valid；語意 issues 放在 Draft.issues[]，publish／run 時強制；新增 source→process→sink 的 minimal 正例；報告 p.14 空圖維持示意。 |
| D-029 | CB-11 | 數值上限的檢查位置 | validator 將 duration 換算為 ticks，超過 365 天（沿用 horizon 上限）回 LIMIT_EXCEEDED；候選限制：pointer 深度 ≤32、metadata 深度 ≤8 且 ≤16 KB、IR ≤2 MB（屬 D-007 候選，benchmark 後可調）。 |
| D-030 | CB-12 | 已決定 proposal 的重複批准 | 以不同 Idempotency-Key 再批准已 approved／rejected 的 proposal → 409 PROPOSAL_ALREADY_DECIDED，狀態不變、無第二次效果。 |
| D-031 | ADR-011 | braces dev-only advisory 風險接受 | 接受 GHSA-vfj7-8cjw-p6xm（braces <=3.0.3，僅 dev lint 鏈）為已知風險；CI 以 `npm audit --omit=dev --audit-level=high` 擋 production 依賴；upstream 有修補即升級。 |

### 第二輪決議（2026-10-15，D-005／D-006／D-013 gate）

| ID | 解決 | 主題 | 決議 |
|---|---|---|---|
| D-032 | CB-20 | 同 tick 事件順序：完成後立即路由 | SERVICE_COMPLETE（priority 0）在同一事件內：原子釋放資源→立即路由進下一個 queue／delay／sink→該 tick 的 dispatch 以 priority 20 最後執行；DELAY_COMPLETE 為 priority 10，同樣立即路由；priority 表移除 route。entity 任何時刻恰處於一個狀態。 |
| D-033 | CB-21 | 零時間與嚴格正值的判定 | 以分布可能的最小 tick 判定：constant=0、triangular min=0、empirical 任一 sample=0 視為零時間；exponential 恆 >0。source interarrival 可能為 0 一律拒絕。empirical 部分在 dataset binding 階段檢查，zero-time cycle 亦於該階段再判一次。 |
| D-034 | CB-15 | Dataset manifest 排序與 logical_ref 字元集 | manifest 一律依 UTF-8 bytes 排序；SQL 使用 ORDER BY logical_ref COLLATE "C"；logical_ref 限 ASCII：`dataset:` 加 `[a-z0-9_.-]+`。 |
| D-035 | CB-14 | Object key tenant 前綴檢查 | 以 starts_with(key, 'tenant/'||organization_id||'/') 取代 LIKE；所有 object key 拒絕 `..`、`//`、反斜線與控制字元；新增跨 tenant key 的反例測試。 |
| D-036 | CB-13 | 最後一位 owner 的鎖定方式 | 成員變更 transaction 依 user_id 順序 SELECT 該 org 全部 owner membership … FOR UPDATE 後再檢查人數；不新增 organizations 的 UPDATE grant；文件同步改寫。 |
| D-037 | CB-16 | Worker 對 imports／jobs 的欄位級權限 | worker 只能 UPDATE imports 的 profile／validation／mapping_suggestion／error 類欄位與 jobs 的 lease／status／attempt／error 類欄位；guard trigger 禁止 worker 將 import 設為 COMMITTED 或修改 mapping、mapping_revision、dataset_version_id、proposal_id。 |
| D-038 | T-020 實作與獨立 review | DES engine 實作語意與 numerical profile | exponential ≥1 tick；>365 天樣本以 365 天＋1 tick 表示；triangular 分支規則並 clamp 回 [min,max]；decision 只含 p>0 flow 並依 flow ID 排序；ARRIVAL 順序為建立→排下一個→路由，同 tick 同 priority 依排程順序；entity ID `r<rep>/<source>/<n>`；ln 為固定演算法（不使用平台 `math.Log`、不融合 FMA）；Compile 自行核對 manifest；sink 未寫 outcome 視為 completed（CB-22，T-021）；零時間 service 完成後同 tick 再 dispatch（dispatch 不一定是該 tick 最後一個事件）；授予依 request 順序並逐筆回報 in-use；經過顯式 queue 時 queue 與 process 各算一次 visit；被 FIFO 擋住的 request 擋住其整個 bundle。詳見 SIMULATION_SPEC.md §2–§4。**2026-10-18 owner 依原文批准（H-004）；gate：T-023 對外提供 run 結果前。** |
| D-039 | T-022 實作 | 結果彙整與 sampled trace 的實作語意 | replication-mean lead time 只要有一個 replication 沒有完整 cohort 即 unavailable；available summary 的 reason 為 null、n<2 時 CI 為 null；暫定 reason codes（NO_COMPLETED_ENTITY_IN_WINDOW、REPLICATION_WITHOUT_COMPLETED_COHORT、NO_COMPLETED_QUEUE_VISIT_IN_WINDOW）；trace 只記 replication 0、rate 1/8、SHA-256(entity ID) 選取、到 max_trace_events 截斷並標 truncated、無事件時不產生 artifact、queue_enter 記實際進入的 node；progress 非同步且不影響 run；queue／resource stats 的 unavailable 必帶 reason；engine_version `des-go-0.1.0`。詳見 SIMULATION_SPEC.md §5、§9。**2026-10-18 owner 依原文批准（H-004）；gate：T-023 對外提供 run 結果前。** |
| D-040 | T-012 review M4 | IR 的儲存形式 | **PROPOSED。** jsonb 讀回時數字以完整十進位印出（`1e300` 為 301 位），1.94 MB 的 canonical draft 讀出 83.6 MB；讀寫與 run 都受影響。建議：新 migration 把 IR 欄位改存 canonical 文字（`json` 或 `text`），讀出即為 hash 涵蓋的 bytes。替代：限制自由容器中數字的寫法，或不改。詳見 ADR-022。gate：T-013 開放 model routes 前。 |
| D-041 | T-012 review L9 | Publish 的 comment | **PROPOSED。** PublishModel 允許 comment 並納入 idempotency hash，但沒有地方保存。建議：在 ModelVersion 保存（使用者資料，不放 audit）；替代：從 contract 移除。gate：T-013 開放 publish route 前。 |

## 人類決策紀錄

由 agent 依 repository owner 的明確指示記錄，措辭待 owner 於 PR review 確認；機器可讀版本在 `planning/decisions.json` 的 `human_decisions`。

| ID | 日期 | 決定 | 仍未決 |
|---|---|---|---|
| H-001 | 2026-10-15 | LLM provider 改為 Anthropic Claude API（Claude Console API key），保留 provider interface；兩個 key 分屬獨立 organization，需 rotation 使用兩邊 credits | LLM model（eval 後）、retention／資料政策、token quota |
| H-002 | 2026-10-15 | 確認 Go backend；與 ADR-001 一致（`net/http`，不引入重型 framework），無變更 | — |
| H-003 | 2026-10-15 | 任務驗收由 agent 自行 review（owner 指示「你自己進行 review 驗收吧」）；驗收紀錄標明為 agent review，owner 可隨時重開任務 | — |
| H-004 | 2026-10-18 | 依原文批准 D-038、D-039（解除 T-023 的 decision gate）與 ADR-017；auth provider 與 browser token 儲存授權 agent 決定，記為 [ADR-018](adr/ADR-018-browser-session-and-oidc-provider.md)（BFF＋HttpOnly 加密 session cookie；Auth0） | Auth0 tenant 由 owner 建立、方案與費用由 owner 確認；tenant region 依 retention／residency |
| H-005 | 2026-10-18 | App 支援英文與中文，英文為主要（預設）語言（REQ-022）。Agent 將中文定為繁體中文 `zh-Hant`；範圍為產品 UI 與 AI 文字輸出，repository 文件語言不變 | ~~是否另需簡體中文~~（H-006：先只做繁體）；文件是否改英文 |
| H-006 | 2026-10-18 | 批准 [ADR-019](adr/ADR-019-http-transport.md)（HTTP transport）與 [ADR-020](adr/ADR-020-ui-localization.md)（UI 語言）為 ACCEPTED；中文先只支援繁體 `zh-Hant`，不加 `zh-Hans`；一般 API 的 rate limiting（429 `RATE_LIMITED`、`Retry-After`）併入 T-036 | 限額數值與計算單位（user／organization）於 T-036 提出 |
| H-007 | 2026-10-18 | 採用 agent 建議的 UI 設計關卡：REQ-023（WCAG 2.2 AA、共用 design tokens 與元件、UI 任務先交 prototype 經 owner review、兩語與手機／桌面不跑版）、TEST-033、T-045（在 T-015 之前）；UI 庫須比較多個庫的做法後以 ADR-021 決定，不直接採用 Tailwind＋shadcn/ui；owner 尚無視覺偏好，預設中性的工具型介面 | UI 庫與 styling（ADR-021，owner 確認）；視覺取向（看 prototype 後回饋） |
| H-008 | 2026-10-18 | 採用 T-045 UI 庫比較（`checks/evidence/2026-10-18-T-045/survey.md`）的四項建議：(1) UI 元件庫內建、無法以 prop 覆寫的無障礙字串可用該庫自己的翻譯（例如 React Aria 的 `zh-TW`），須列清單、在 spike 中審閱用語，並以測試列出所有不來自 catalog 的字串；(2) P0 只做淺色主題，tokens 預留深色；(3) P0 只用系統字型（含繁中 fallback），不載入 web font；(4) 不使用付費元件授權，也不使用安裝時回傳資料的套件 | UI 庫與 styling 仍由 T-045 spike 後的 ADR-021 決定 |

## 原報告保留的 TBD

| 項目 | 原報告建議／限制 | 決定時間／阻擋範圍 |
|---|---|---|
| LLM provider | ~~OpenAI adapter first + provider interface，未定案~~ **已由 H-001 決定：Anthropic Claude API** | 真實 provider integration 仍受 D-018、資料政策、token quota 阻擋 |
| LLM model | eval 後決定，不 hard-code | Week 7；live AI release |
| Auth provider | ~~managed OIDC，未定案~~ **已由 H-004／ADR-018 決定：Auth0；browser 經 BFF（HttpOnly 加密 session cookie）** | API 端驗證見 ADR-017（ACCEPTED）；登入流程 T-043；Auth0 tenant 由 owner 建立，region 依 retention／residency |
| Cloud / managed DB | GCP reference / AWS equivalent；managed PostgreSQL | Week 1–2；production deploy |
| Object storage | cloud-native / S3-compatible abstraction | Week 2；持久化 artifact |
| Frontend state / UI libraries | TanStack Query、Zustand、Tailwind/shadcn 原文均 TBD；server state 已由 ADR-016 決定；**UI 庫與 styling 由 T-045 比較多個庫後以 ADR-021 決定（H-007）** | T-045；不影響 IR |
| SQL generation | explicit SQL / sqlc TBD | Backend bootstrap |
| Model / upload / replications / timeout | 原報告初始目標，不是量測成果 | Alpha benchmark |
| AI token / tenant quota | per operation / tenant，數值未定 | AI Alpha；無上限時不得開 live AI |
| Reference device / server / load workload | 未決定 | Performance gate 不得宣告已過 |
| Retention / residency | beta 前決定 | 未批准不得放真實客戶資料進 provider / production |
| Billing / collaboration / second domain / Rust / iTwin | P1 或 post-MVP | 不阻擋 P0，不擅自實作 |

## 不得被假設取代的資料

實際 reviewer service distribution、stage/event taxonomy、DORA measurement boundaries、客戶部署/失敗/復原語義、業務時間 calendar 均非報告已提供的客戶資料。缺少時回 needs_input 或 unavailable。測試 fixture 中的數值只能用於測試，不能用來補使用者資料。
