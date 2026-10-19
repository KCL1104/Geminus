# Decisions & TBD Register

版本：handoff-0.1.0；來源：原報告 v0.1（2026-10-15）。各決策是否已批准以本檔各節狀態與 `planning/decisions.json` 為準；D-001–D-037 於 2026-10-15、D-038–D-039 於 2026-10-18 由 owner 批准；D-040、D-041（T-012 review 發現，gate T-013）與 D-042（T-010 review 發現）於 2026-10-18 批准（H-009），已分別在 T-013a、T-013b、T-010 實作。D-047–D-060 為 T-046 於 2026-10-19 提出的 PROPOSED，尚未批准。

## 狀態與採用方法

`SOURCE` = 報告已明確決定；`PROPOSED` = 為形成可實作合約而新增的候選；`TBD` = 報告仍未決定。
OpenAPI、Schema、SQL 中的候選細節目前都是 `PROPOSED`。可以先寫 parser、fixture、測試與 spike；在表列 gate 前，由人類更新 `planning/decisions.json` 的 status / approved_by / approved_at。不可讓 coding agent 自行填入批准者。
若不同文件出現衝突，先登記並停止受影響的變更，不能用「最新修改的檔案」自動覆蓋原始需求。標準契約的相容性查核見 sources/SOURCE_MAP.md；外部標準不構成新的產品需求。

T-001（2026-10-15）盤點出的跨文件矛盾與未定義行為列於 [CONTRACT_BLOCKERS.md](CONTRACT_BLOCKERS.md)（CB-01–CB-36），每項標示對應的 D-entry 或 NEW；task 與 decision gate 的對照由 `make spec-decisions` 產生於 `checks/decisions-check.json`。

## 原報告已鎖定的邊界

Go modular monolith + separate worker；Next.js / React / TypeScript；React Flow 2D authoring；PlayCanvas 3D read-only viewer；PostgreSQL + P0 PostgreSQL jobs；REST-first；Generic Twin IR + Domain Pack；native Go DES；AI proposal-only + deterministic validation + human approval；immutable versions；Scenario patch 不修改 canonical version。P1 的 broker、connectors、GraphQL、3D authoring、Rust engine、iTwin adapter 不可偷渡進 P0。

Owner 變更（H-016，2026-10-19）：原報告以 Software Delivery 為 P0 第一個 domain，現改為電子組裝／測試線；原屬 P1 的 calendar 中，班表（shift calendar）拉進 P0，priority／preemption 仍為 P1。其他鎖定邊界不變。

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

實作註記（T-023，2026-10-19，待獨立 review；不改變本決策的狀態）：run 在入列時凍結 server 的 execution limits（`internal/runs.DefaultLimits`：300 s、10M events、100k active entities、10k visits、100k trace events），client 無法設定。跨 retry 的 budget：wall clock 從第一次開始起算（`execution_deadline_at`），events 以 durable reservation 分批授予（預設每批 max_events 的十分之一），之前 attempt 保留的量算作已用；細節見 API_DATABASE §7、SIMULATION_SPEC §8。批次大小是實作參數，不是本決策的一部分；benchmark（T-037）後可調。

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
| D-040 | T-012 review M4 | IR 的儲存形式 | **APPROVED（H-009）。** jsonb 讀回時數字以完整十進位印出（`1e300` 為 301 位），1.94 MB 的 canonical draft 讀出 83.6 MB；新 migration 把 IR 欄位改存 canonical 文字（`json` 或 `text`），讀出即為 hash 涵蓋的 bytes。gate：T-013 開放 model routes 前。 |
| D-041 | T-012 review L9 | Publish 的 comment | **APPROVED（H-009）。** 在 ModelVersion 保存 comment（使用者資料，不放 audit；migration 與 contract）。gate：T-013 開放 publish route 前。 |
| D-042 | T-010 review L5 | Provisioning 的最小權限 role | **APPROVED（H-009）。** 新 migration 建立 NOLOGIN 的 provisioning role，只有 users、organizations、memberships、audit_events 所需的寫入（與所需的 SELECT），RLS 與 audit trigger 的 provisioning 例外改認此 role；`cmd/provision-tenant` 改為 SET ROLE 它，不再以 schema owner `twin_owner` 執行。gate：production 部署前（併入 T-010）。 |

### T-016 前的提案（2026-10-19，APPROVED，H-010）

| ID | 解決 | 主題 | 提案 |
|---|---|---|---|
| D-043 | CB-27 | Import 的 profile、preview 與人工 mapping | **APPROVED（2026-10-19，H-010）。** (a) Import 增加 `profile`（profiling 完成前為 null）：`format`（csv／json_events／json_model）、`encoding`（`utf-8` 或 `utf-8-bom`）、`rows`、`columns[]`（`name` ≤256 字元、`index`、`inferred`：empty／boolean／integer／number／timestamp／string／mixed、`non_empty`、`max_length`）；每個 import 至多 256 欄，preview 的 `maxProperties` 改為 256；json_model 的 `columns` 為空。(b) `preview` 是前 100 筆的**原始**值，以來源欄名為 key（CSV 一律字串；JSON events 原值，另限深度、字串長度與總 bytes）；mapping 之後的 canonical 預覽屬 validation（T-017），`valid-import.json` 依此修正。(c) 人工 `SaveMapping` 改用只含 `source_column`、`target_field`、`transform` 的 mapping schema，不要求 AI 專用的 `confidence`、`evidence`、`requires_confirmation`；AI suggestion 仍用 ai-mapping。gate：T-016 開放 upload／profile 前（(c) 為 T-017 前）。**實作（T-016，2026-10-19，待獨立 review）：** (a)(b) 進入 `import.schema.json`（`profile` 與 `$defs/Profile`、`ProfileColumn`，json_model 的 rows 為 0、columns 為空；preview 的 `maxProperties` 256）與 `valid-import.json`（原始值、中文欄名）；`internal/import/profile` 依此產生 profile 與 preview，推斷規則與上限見 API_DATABASE §5，需要 owner 確認的選擇見 CB-39。(c) 未做（T-017）。 |
| D-044 | 原報告 TBD（object storage） | P0 的 object storage | **APPROVED（2026-10-19，H-010）。** `internal/storage/objects` 定義 S3 相容的最小介面（put 時計算 SHA-256 與大小、get、delete 只給受控 maintenance；key 一律由 server 依 D-035 產生）。P0 先提供 filesystem adapter，只用於 development 與測試（`APP_ENV=production` 時拒絕啟動）；正式環境的 adapter（雲端廠商、bucket、region、加密金鑰、retention、signed URL 期限）由 owner 選定雲端後另立 ADR，未定前不收真實客戶資料（SECURITY §6）。下載先由 API 授權後串流，不發 signed URL。gate：T-016 寫入 upload 前。**實作（T-023，2026-10-19，待獨立 review）：** `internal/storage/objects`（`Store`、`Maintainer`、`NewKey`／`ValidKey` 與 DB 的 `object_key_ok` 同一規則）與 `FS` adapter（`OBJECT_STORAGE_PROVIDER=filesystem`、`OBJECT_STORAGE_ROOT`；只在 `APP_ENV` 為 development／test 時接受，`APP_ENV` 未設也拒絕；寫入不覆寫既有 key）；simulation trace 由 API 授權、核對大小與 SHA-256 後串流（`downloadSimulationTrace` 改回 `TraceArtifact`，見 CB-38(b)）；dataset snapshot 由 `internal/storage/snapshots` 讀取。正式 adapter 與孤兒 object 的 maintenance 未做。**T-016（2026-10-19，待獨立 review）：** 上傳以 `objects.NewKey` 的 key（`tenant/<org>/imports/<import>/upload`）串流寫入並記錄 SHA-256 與大小；error report 寫到本 import 自己的 key，由 API 授權後核對 SHA-256 與大小再串流（`downloadImportErrors` 不再回 `SignedDownload`，CB-38(b)）；worker 讀 upload 時同樣核對。 |

### 其他契約缺口（2026-10-19，APPROVED，H-010）

| ID | 解決 | 主題 | 提案 |
|---|---|---|---|
| D-045 | CB-17 | 使用者自己的 membership（`/me`） | **APPROVED（2026-10-19，H-010）。** 新增 `GET /v1/me`（任何已驗證的使用者；不帶 `X-Organization-ID`）：回 `user_id` 與 `memberships[]`（`organization_id`、organization `name`、`role`），依 organization 名稱再依 ID 排序，P0 至多 100 筆；未知 identity 回 403（不自動建立，D-008）。RLS 新增只讀的 policy：API 只看得到 `user_id` 等於驗證後 scope 中 user 的 membership，與這些 membership 所屬 organization 的 `id`、`name`（新 migration；其他 tenant 的任何 row 仍不可見）。UI 以此選 organization 並取得 role，role 仍只決定介面，API 每次自己授權。gate：T-043 的 UI bootstrap 前。**實作（T-043 的 D-045 slice，2026-10-19，待獨立 review）：** migration 0008 的 `own_memberships`、`own_organizations` 是只給 `twin_api` 的 permissive `FOR SELECT` policy，只在**沒有選 organization** 時成立，所以選了 organization 的 transaction 看到的與 0002 的 `tenant_isolation` 完全相同；`tenant.DB.RunSelf` 是只設 user 的唯讀 transaction；`internal/tenant/me` 與 `httpapi.Self`（帶 `X-Organization-ID` 即 400）；排序為 byte 順序（`COLLATE "C"`，與 DB 的 collation 無關）；超過 100 筆時回前 100 筆並帶 `truncated: true`；不寫 audit；client 的 `getMe()`。見 checks/T-043_VALIDATION.md 的 D-045 一節。 |
| D-046 | CB-31 | AI 新增 entity 的 ID allocation pool | **APPROVED（2026-10-19，H-010）。** 每個 proposal 在建立時由 server 產生 pool：每種 entity（node、flow、resource）16 個 ID，格式 `<kind>_<小寫 ULID>`（符合 IR 的 ID pattern），存在 `ai_proposals.id_pool`（新 migration；之後不可改）並放進 provider context。Validation 與 approval 時要求 patch 新增的每個 entity ID 都來自同 proposal 的 pool、種類相符、在 base IR 中未使用；不足時 provider 回 `needs_input`。gate：T-024 的 context builder 前。 |

### T-046 的提案：電子產線的引擎語義與 domain 規格（2026-10-19，PROPOSED，待 owner）

H-016 把 P0 domain 改為電子組裝／測試線，引擎要補上有限緩衝與阻塞、故障、班表、隨時間變化的投入與批次（REQ-024）；H-017（owner：「時間還很久，盡量保留所有功能」）把換線與 lognormal／Weibull 列為 P0 提案；agent 另依此方向建議把數項原本延後的功能納入 P0，由 owner 在本頁決定。H-017 只是方向，不批准任何 decision。T-046 phase 1 提出新核心 primitive 的 [ADR-026](adr/ADR-026-line-flow-primitives.md)（PROPOSED）與 domain 規格 [ELECTRONICS_LINE.md](domains/ELECTRONICS_LINE.md)（PROPOSED）。每項 decision 的選項與後果、對決定性與 result digest 的影響、golden vectors 草案與 SimPy reference 的做法寫在這兩份文件中對應 D-ID 的一節；下面只列問題與建議。全部 **PROPOSED**：approved_by／approved_at 為 null，批准前不實作 kernel、不改 schema 與 fixtures。Gate：T-046（phase 2 的 contract 變更）等 D-047–D-059；T-047–T-051 依各自用到的 decision；T-027 等 D-059；T-052 等 D-058–D-060。T-017 不另加：它的驗收（TEST-012、TEST-013）是一般的 mapping，station log 的 mapping 驗收在 T-052。

#### D-047 — 新 primitive 的相容性契約與 differential

**狀態：PROPOSED（2026-10-19）。負責角色：Simulation / Contracts。Gate：T-046 phase 2 改 schema 前；T-047–T-050 實作前。**

問題：新欄位如何進入 IR、Result 與 engine_version，才能讓沒用到的模型與現行 kernel 逐 bit 相同？選項：A optional 欄位、schema_version 維持 0.1；B 新 schema_version 0.2 加升級流程；C 新統計一律進 Result（會改所有既有 digest，不採用）。建議 A：Result 的新成員只在用到時出現；每個 kernel 任務升 engine_version minor；沒有新欄位的模型以「observation 序列相同、engine_version 換回 `des-go-0.1.0` 後 digest 等於鎖定值」判定，鎖定檔不重產；用了新欄位的中性設定只比 observation 序列與既有 Result 成員，且不得有任何新 observation（含 t=0 的 Capacity、ResourceUp）。

#### D-048 — 新事件的同 tick 順序

**狀態：PROPOSED（2026-10-19）。負責角色：Simulation。Gate：T-047 實作前。**

問題：故障、修復、班表容量變化、批次逾時與解除阻塞在同一 tick 的哪裡？選項：A 新增 priority 15（在 arrival 10 之後、dispatch 20 之前）；B priority 5；C priority 25。建議 A：dispatch 看到該 tick 最後的容量，既有三個 priority 的相對順序不變；解除阻塞在 dispatch 內進行，不是事件；t=0 的順序為 source、calendar（每個 calendar 一個事件）、各 unit 的故障時鐘。

#### D-049 — 有限緩衝與阻塞

**狀態：PROPOSED（2026-10-19）。負責角色：Simulation。Gate：T-047 實作前。**

問題：容量定義在哪、BAS 或 BBS、被擋 entity 持有什麼、與 resource-wise FIFO 和 bundle 的交互、source 與 delay 被擋時？選項：A blocking after service；B blocking before service（要跨 process 預留空位、提早抽 routing）；C 以下游 slot resource 模擬（resource 無法跨 process 持有）。建議 A：顯式 queue 的 `capacity` 限制等待中的 entity 數；被擋的 entity 留在出發 node（process 保留整個 bundle，source 形成 release backlog）；解除只在 dispatch 內依 FIFO 進行，且不再另排 dispatch；先解析、後提交，沒有被擋時 observation 與 seq 與現行相同，路由中途觸發 limit 時錯誤前的 observation 也與現行相同。

#### D-050 — 死結與停滯的防止與偵測

**狀態：PROPOSED（2026-10-19）。負責角色：Simulation。Gate：T-046 phase 2 前；T-047–T-050 實作前；T-051 的 template 須通過。**

問題：循環阻塞與「bundle 永遠湊不齊」（review 的兩個反例：班表不相交的 bundle、相位相反的 constant 故障）怎麼防止與以 typed error 偵測，保證到哪裡？原提案的 backstop 在有週期性班表或故障時永不觸發；例外日不是週期的，使期限與狀態簽章在例外日前後失效（review N6）。選項：A 靜態 R1–R4 全為 error，加 runtime 的 B1、B2；A′ R4 為 warning，另以狀態簽章偵測固定相位的鎖定；B 只做 runtime 偵測；C 不偵測（違反 TEST-034）。建議 A：R1 `BLOCKING_CYCLE`；R2 `CALENDAR_INVALID`（每個 bundle 在班表的最小公倍數週期內至少有一個 tick 湊得齊）；R3 故障 resource 的 request units＝1、一個 bundle 至多一個故障 resource；R4 `STALL_RISK`（故障 resource 上班時夥伴都夠，只看週期部分；訊息建議給機台作業員的 calendar）。每個 valid 的模型都可證明不會永久停滯；B1 `ErrDeadlock`（循環等待，立即、無誤報）；B2 `ErrStalled`（bundle 沒有任何 unit 在使用中，且已過期限 next_fit(max(t_q, X_end)＋TTRmax)；X_end 是 bundle 的 calendar 最後一段例外的結束）。兩者對 valid 的模型都不觸發、不記狀態。代價：R4 拒絕「會故障的機台沒有班表、作業員有班表」等寫法。A′ 的代價：簽章只能在 X_end 之後比對、記憶體要以每個 replication 的總量設限並計入 limits、只偵測 TTF／TTR 固定的鎖定，其他不偵測。這也回答了「warning 或 error」。

#### D-051 — Resource 故障（MTBF／MTTR）

**狀態：PROPOSED（2026-10-19）。負責角色：Simulation。Gate：T-048 實作前。**

問題：故障時鐘、服務中 entity 的處理、多 unit、RNG stream 命名、availability？選項（處理）：A finish-first、每個 unit 一組故障時鐘；B preempt-resume；C preempt-restart（B、C 屬 PRD P1）。選項（時鐘）：日曆時間、排班時間、忙碌時間。建議 A，時鐘一律只在 resource 上班時前進（由確定的班表直接算出故障時刻，不需 unit identity；沒有 calendar 時即日曆時間；日曆時鐘在 `two_shift_weekdays` 下會使上班時的故障率只剩約 48%）。D-050 建議 R4 為 error，所以不提供 `clock` 欄位；若 owner 改採 A′ 再加回。TTF 要以上班時間估計；stream 為 (resource ID, `ttf/<u>`／`ttr/<u>`)，既有 stream 不變；availability＝∫min(S, C−down)／∫S。代價：長時間 service 低估中斷影響。

#### D-052 — 班表與 utilization 分母

**狀態：PROPOSED（2026-10-19）。負責角色：Simulation。Gate：T-049 實作前。**

問題：班表在 IR 中的表示（tick、無時區）、交班時服務中的 entity、utilization 的分母、每班人數與例外日？選項：A 根層共用的 `calendars[]`＋ resource 的 `calendar_id`；A＋ 再加每班容量（`shift_capacity`）與以絕對 model 時間表示的 `exceptions`；B 每個 resource 自己的容量表；C 絕對日期與時區（時區不採用）。建議 A＋（agent 依 H-017 的方向）：交班 finish-first（記 overtime），原 `utilization_mean` 不變，另加 scheduled utilization 與 overtime；每個 calendar 在 S 改變時一個事件，只對 S 改變的 resource 記 observation。

#### D-053 — 隨時間變化的投入

**狀態：PROPOSED（2026-10-19）。負責角色：Simulation。Gate：T-049 實作前。**

問題：分時段的投入怎麼表示、與 common random numbers 的關係？選項：A 週期性分段常數 rate、以累積 rate 反函數產生（poisson 或 deterministic，每件一次 draw）；B 投入表（整批投入）；C 各時段各自的 interarrival 分布（打亂配對，不採用）。建議 A＋B（agent 依 H-017 的方向）：scenario 之間第 k 件用同一個 draw；numerical profile 固定（含 r 落在 (0, P] 之外時的修正、超過 365 天時的飽和）；投入表每列一個 ARRIVAL、不用 RNG。

#### D-054 — 批次 process（燒機）

**狀態：PROPOSED（2026-10-19）。負責角色：Simulation。Gate：T-050 實作前。**

問題：批量上下限、等待上限、成員 ID 與 trace、lead time 與 utilization？選項：A min／max／max_wait，就緒時一個 batch request、一次 service draw、整批離開才釋放；B 只有固定批量；C 合併成一個 entity（建議之後另立任務）。建議 A：成員保留自己的 entity ID，批次 ID `r<rep>/<process>/b<n>`；lead time 與 queue wait 照每件計；utilization 為 chamber 忙碌，另有批量統計；有成員被擋時的 observation 順序已固定。

#### D-055 — 換線（P0）

**狀態：PROPOSED（2026-10-19）。負責角色：Simulation / Product。Gate：T-050 實作前。**

依 H-017 列為 P0 提案；PRD 已列 P1 的優先權、preemption、進階 routing 仍為 P1。問題：產品怎麼表示、換線何時發生在哪個 unit、與 FIFO 和其他功能的交互？選項：A resource 層的 sequence-dependent 換線（`setups` 矩陣、每個 unit 自己的換線狀態、FIFO 派工）；B process 層的固定 setup（無法表示每台機的狀態與矩陣）；C A 加依產品分組的派工（屬 PRD P1）。建議 A：產品＝entity_type（每個產品一個 source）；授予時優先選狀態相符的 unit，先抽換線再抽 service，不新增事件種類；stream 為 (resource ID, `setup/<u>`)；單一產品的模型與現行逐筆相同。

#### D-056 — lognormal 與 Weibull（P0）

**狀態：PROPOSED（2026-10-19）。負責角色：Simulation。Gate：T-048 實作前。**

依 H-017 列為 P0 提案。問題：參數化、抽樣演算法與 numerical profile？選項：A 兩者皆以反函數法、每個樣本一次 draw（lognormal 以 mean、cv 參數化，Φ⁻¹ 用固定的 AS241；Weibull 的冪次以固定的 exp 與 ln 計算）；B lognormal 用 Box–Muller（每個樣本兩個 U，不採用）；C 只加其中一個。建議 A：stream 消耗與 golden 方法不變；需要固定的 exp、Python 逐運算移植、vectors 與新的 engine_version；實作併入 T-048。

#### D-057 — 新參數的 scenario allowlist 與 risk

**狀態：PROPOSED（2026-10-19）。負責角色：AI / Simulation。Gate：T-047–T-050 各自開放對應參數前。**

問題：新參數可否在 scenario 中修改、risk 怎麼算？選項：A 擴充 D-021（buffer capacity、breakdown 分布、換線分布、投入 rate 與投入量、batch 參數、calendar 參照、每班容量、shifts 與例外）；B 不擴充（「加大 FCT 前緩衝」須發布新版本，與 baseline 不可比較）；C 只加 buffer capacity。建議 A：新參數的變更至少 medium，擴大 buffer 為 low；candidate 須通過 D-050 的 R1–R4（違反即 invalid；若 owner 改採 D-050 的 A′，新出現 `STALL_RISK` warning 時仍可建立、warning 顯示在審核內容中且 risk 至少 medium）；允許改分布的路徑都可換成 lognormal／Weibull；新增或刪除 breakdowns、setups、calendars、schedule、batch 仍走 draft→publish。

#### D-058 — 電子產線指標的資料來源與 Result 位置

**狀態：PROPOSED（2026-10-19）。負責角色：Domain / Simulation。Gate：T-046 phase 2 改 schema 前；T-051 與 T-052 實作前。**

問題：Pack 的 `ComputeMetrics` 只看 Result，指標放哪、需要哪些 generic 統計？選項：A optional 的 `ComputeModelMetrics(effective IR, result)`、新的 optional `metrics.domain`，並列出 pack 需要的 generic Result 成員；B 由 kernel 讀 domain 屬性（違反 ADR-010）；C 只在 UI 端計算（不在 digest 中）。建議 A：每個 replication、每個 resource 的整數 tick 積分（∫U、∫K、∫Z、∫D、∫S、∫A、∫min(U,S)、∫min(P,S)、∫max(0,A−U)、∫max(U−S,0)）與停機、修復、換線的次數，每個 node 的阻塞統計、每個批次 process 的批量，都只在用到對應功能時出現；宣告式的 `system_wip`、`flow_count`、`sink_count`、`period_throughput`（後三者為 agent 依 H-017 的方向新增的建議）；線班表的上班時間由 pack 從 IR 算出。FPY 是模型輸入；公式見 ELECTRONICS_LINE §5。

#### D-059 — Station event log 的 canonical fields

**狀態：PROPOSED（2026-10-19）。負責角色：Import / Domain。Gate：T-046 phase 2 改 canonical-event schema 前；T-027 的電子產線 inference 與 T-052 前。**

選項：A canonical-event 新增 optional `end_timestamp`、`shift` 與 `product`（多產品時必填，D-055），pack 定義 event_type／outcome 詞彙，停機紀錄以設備 ID 為 case_id；B 不改 schema、每筆拆成 start／end 兩個 event；C 沿用 duration_ms 並新增兩欄相減的 transform。建議 A；operator ID 不匯入；常數 transform 屬 CB-28。

#### D-060 — 模擬與觀測比對（REQ-025）

**狀態：PROPOSED（2026-10-19）。負責角色：Simulation / Product。Gate：T-052 實作前。**

選項：A 分「輸出比對」與「輸入一致性」，以每期平均比較，顯示模擬平均、replication 範圍、絕對與相對誤差，標 in-sample／hold-out；B 只並列數字；C 統計檢定給通過判定。建議 A：不給通過判定、不自動校準；沒有觀測資料時 unavailable；宣告 `period_throughput`（D-058）時另做逐期比較。

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
| H-009 | 2026-10-18 | 10 項全部照 agent 建議（「全都照建議」）：UI 用 React Aria Components 1.21.1＋CSS Modules（[ADR-021](adr/ADR-021-ui-library-and-design-system.md) ACCEPTED）；依 H-008 接受 5 種 React Aria 內建繁中字串；axe-core／@axe-core/playwright（MPL-2.0）只作測試工具；保留 React Flow 署名；成功回饋用頁內 `role="status"`；D-040（IR 存 canonical 文字）、D-041（保存 comment）、D-042（provisioning 最小權限 role）APPROVED；[ADR-022](adr/ADR-022-model-service.md)、[ADR-023](adr/ADR-023-membership-management.md) ACCEPTED | 視覺取向（看 prototype 後）；螢幕報讀器實測（需人執行） |
| H-010 | 2026-10-19 | 照 agent 建議（「其他都照你建議吧」）：批准 D-043、D-044、D-045、D-046；[ADR-024](adr/ADR-024-job-queue-and-worker.md) ACCEPTED（參數照提案，`WORKER_STOPPED` 計入 attempt）；ADR-011、ADR-013 ACCEPTED；Auth0 refresh token rotation 的 reuse interval 為 30 秒；接受 React Aria 同一組選取通知的其餘繁中字串（「未選取「…」。」「已選取所有項目。」「已選取 N 個項目。」） | T-045 prototype 審閱與截圖 baseline 確認（已整理成 artifact，待 owner 回覆）；live AI 的資料政策、retention、quota 與 model；雲端廠商與 region；Auth0 tenant 由 owner 建立 |
| H-011 | 2026-10-19 | 放寬 H-008：可使用 web font（須為 OFL 等開源授權）、可做深色主題。Owner 認為目前的 UI 太普通，請 agent 研究各大產品與 digital twin 產品的 UI 後提出方向；agent 提出 A 控制室、B 精密工具、C 工程藍圖三個方向（建議 C 為主，融合 A 與 B） | ~~選定視覺方向與預設主題~~ 已由 H-013 決定 |
| H-012 | 2026-10-19 | 產品願景涵蓋所有場景（製造業、工業、倉儲、航太、城市、建築工程等），~~P0 仍先做 Software Delivery，不改 P0／P1 範圍~~（H-016 改為電子組裝／測試線）；PRODUCT.md 須寫入 3D（P0 的 3D 仍唯讀，SOURCE）；使用情境為桌機決策、每天長時間建模、會議投影；以桌機為主，手機可看結果與審核 proposal、不編輯拓撲；品牌只有名稱 Geminus；深色主題用灰黑色、不用藍色；以 impeccable（Apache-2.0）做 UI 檢查與改進。詳見 `PRODUCT.md` | ~~3D authoring 是否及何時納入~~ H-014：P0 不做，選定第二個 domain 時再決定；~~視覺方向與預設主題~~ H-013 |
| H-013 | 2026-10-19 | 視覺方向選 Marey 圖表時刻表（impeccable 方向輪，agent 排名第一的候選）：時間由左往右、步驟由上往下、每個工作項目一條線，線變平就是排隊；scenario 深綠、baseline 石墨虛線；字型 Archivo；深色灰黑。預設主題跟隨系統，使用者可切換並記住。草稿在 `.impeccable/mocks/decision/` | 依此方向建成的 design system（DESIGN.md、tokens、元件）待 owner 檢視 |
| H-014 | 2026-10-19 | 3D 維持現狀：P0 只做唯讀 3D 檢視（T-033、T-034），3D authoring 不進 P0；選定第二個（空間型）domain 時再決定；不另開空間型 IR 擴充 spike | 選定第二個 domain 時決定 3D authoring |
| H-015 | 2026-10-19 | 專案授權為私有＋保留所有權利（`LICENSE`）：不授予任何使用、複製、修改、散布或作為服務提供的權利。Agent 提出 FSL-1.1-ALv2（建議，若維持公開）、私有、AGPL-3.0、Apache-2.0 四個選項 | owner 在 GitHub 將 repo 改為私有；著作權人名稱（目前為 KCL1104）；AI 生成程式碼著作權與原報告權利請律師確認 |
| H-016 | 2026-10-19 | P0 第一個 domain 由 Software Delivery 改為**電子組裝／測試線**（SMT、AOI、ICT、FCT、燒機，含測試失敗→除錯→重測；AI 伺服器機櫃整合測試為變體），倉儲人力與尖峰為第二個 domain（P1）。採 agent 比較五組研究後的建議（理由見 `planning/decisions.json` 的 rationale）。SoftwareDeliveryDomainPack 保留為範例與 conformance pack。引擎在 P0 補上有限緩衝與阻塞、機台故障、班表、隨時間變化的投入、批次；語義由 T-046 提出 PROPOSED decisions 待批准，換線是否進 P0 另決定。近期目標為初步產品與技術驗證後投 launchpad；owner 尚無可接觸的潛在使用者，示範只用標為 synthetic 的資料。3D 不變（H-014）。偏離原報告：原報告以 Software Delivery 為 P0，calendar 屬 P1 | T-046 的引擎語義 decisions；launchpad 名稱（期限不急，H-017）；示範用 live AI 的 model、quota 與資料政策；研究稿是否提交（repo 改私有後）；客戶訪談（建議 5–10 場）；倉儲開始前決定 3D authoring |
| H-017 | 2026-10-19 | 時程還很久，盡量保留所有功能：不為時程刪減 P0，換線與 lognormal／Weibull 列為 P0 提案（D-055、D-056）；PRD 的 P1 項目仍為 P1。示範要用真的 AI，但 live AI 的 gate 不變（T-024＋model、quota、資料政策；agent 建議 Claude Opus 5.5、開發期每月 100 美元、示範期只送 synthetic）。環境中目前沒有 API key，須由 owner 以環境 secret 提供（`GEMINUS_ANTHROPIC_API_KEY_A`／`_B`），不貼在對話。重開被中斷的 T-023 與 DESIGN.md | live AI 的 model、quota、資料政策；owner 提供 key；T-046 延後項目是否納入 P0；launchpad 名稱 |

## 原報告保留的 TBD

| 項目 | 原報告建議／限制 | 決定時間／阻擋範圍 |
|---|---|---|
| LLM provider | ~~OpenAI adapter first + provider interface，未定案~~ **已由 H-001 決定：Anthropic Claude API** | 真實 provider integration 仍受 D-018、資料政策、token quota 阻擋 |
| LLM model | eval 後決定，不 hard-code | Week 7；live AI release |
| Auth provider | ~~managed OIDC，未定案~~ **已由 H-004／ADR-018 決定：Auth0；browser 經 BFF（HttpOnly 加密 session cookie）** | API 端驗證見 ADR-017（ACCEPTED）；登入流程 T-043；Auth0 tenant 由 owner 建立，region 依 retention／residency |
| Cloud / managed DB | GCP reference / AWS equivalent；managed PostgreSQL | Week 1–2；production deploy |
| Object storage | cloud-native / S3-compatible abstraction；P0 介面與 development 用 adapter 見 D-044（APPROVED，H-010），正式廠商待 owner 選定雲端 | Week 2；持久化 artifact |
| Frontend state / UI libraries | TanStack Query、Zustand、Tailwind/shadcn 原文均 TBD；server state 已由 ADR-016 決定；**UI 庫與 styling 由 T-045 比較多個庫後以 ADR-021 決定（H-007）** | T-045；不影響 IR |
| SQL generation | explicit SQL / sqlc TBD | Backend bootstrap |
| Model / upload / replications / timeout | 原報告初始目標，不是量測成果 | Alpha benchmark |
| AI token / tenant quota | per operation / tenant，數值未定 | AI Alpha；無上限時不得開 live AI |
| Reference device / server / load workload | 未決定 | Performance gate 不得宣告已過 |
| Retention / residency | beta 前決定 | 未批准不得放真實客戶資料進 provider / production |
| Billing / collaboration / second domain / Rust / iTwin | P1 或 post-MVP | 不阻擋 P0，不擅自實作 |

## 不得被假設取代的資料

實際 reviewer service distribution、stage/event taxonomy、DORA measurement boundaries、客戶部署/失敗/復原語義、業務時間 calendar 均非報告已提供的客戶資料。缺少時回 needs_input 或 unavailable。測試 fixture 中的數值只能用於測試，不能用來補使用者資料。
