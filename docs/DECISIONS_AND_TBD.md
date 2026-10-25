# Decisions & TBD Register

版本：handoff-0.1.0；來源：原報告 v0.1（2026-10-15）。各決策是否已批准以本檔各節狀態與 `planning/decisions.json` 為準；D-001–D-037 於 2026-10-15、D-038–D-039 於 2026-10-18 由 owner 批准；D-040、D-041（T-012 review 發現，gate T-013）與 D-042（T-010 review 發現）於 2026-10-18 批准（H-009），已分別在 T-013a、T-013b、T-010 實作。D-047–D-060 為 T-046 於 2026-10-19 提出，2026-10-20 由 owner 依建議批准（H-018）。D-061–D-062（T-025）與 D-063（T-046 phase 2）於 2026-10-20 提出，同日由 owner 批准（H-019：D-062 (i) 選 B，其餘依建議）。D-064（T-047）、D-067–D-069（T-026）與 D-070–D-072（T-017）於 2026-10-20 提出，2026-10-25 由 owner 依建議批准（H-021）；D-073 於 H-021 之後提出（PROPOSED）。D-074–D-075（T-048）與 D-078–D-080（T-028）於 2026-10-25 提出，同日由 owner 依建議批准（H-022）。

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

T-024 實作（2026-10-19，[ADR-027](adr/ADR-027-ai-provider-adapter.md) ACCEPTED（H-019）；本決議狀態不變）：`internal/ai/provider/anthropic` 依序嘗試 `org-a`（`GEMINUS_ANTHROPIC_API_KEY_A`）、`org-b`（`_B`）；401／402／403 把該 credential 標為不可用並換下一把（`failed_over_from` 記在 metadata，之後每次跳過它的呼叫也記），401／403 直到 process 重啟，402 休息 15 分鐘後再試（T-024 review，ADR-027），兩把都不可用回 `credentials_exhausted`；408／409／429／5xx（含 529）與連線錯誤只由 SDK 在同一把 key 重試（budget 的 `MaxRetries`）。Key 由 config 注入、adapter 不讀環境變數；錯誤、metadata 只帶 label。只對本機 httptest server 測過；任何 live 呼叫仍待 owner 核准 model、quota 與資料政策（H-017），config 的核准清單為空。401／403 的 key 不重新探測（換 key 即重啟 process）。

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
| D-043 | CB-27 | Import 的 profile、preview 與人工 mapping | **APPROVED（2026-10-19，H-010）。** (a) Import 增加 `profile`（profiling 完成前為 null）：`format`（csv／json_events／json_model）、`encoding`（`utf-8` 或 `utf-8-bom`）、`rows`、`columns[]`（`name` ≤256 字元、`index`、`inferred`：empty／boolean／integer／number／timestamp／string／mixed、`non_empty`、`max_length`）；每個 import 至多 256 欄，preview 的 `maxProperties` 改為 256；json_model 的 `columns` 為空。(b) `preview` 是前 100 筆的**原始**值，以來源欄名為 key（CSV 一律字串；JSON events 原值，另限深度、字串長度與總 bytes）；mapping 之後的 canonical 預覽屬 validation（T-017），`valid-import.json` 依此修正。(c) 人工 `SaveMapping` 改用只含 `source_column`、`target_field`、`transform` 的 mapping schema，不要求 AI 專用的 `confidence`、`evidence`、`requires_confirmation`；AI suggestion 仍用 ai-mapping。gate：T-016 開放 upload／profile 前（(c) 為 T-017 前）。**實作（T-016，2026-10-19，待獨立 review）：** (a)(b) 進入 `import.schema.json`（`profile` 與 `$defs/Profile`、`ProfileColumn`，json_model 的 rows 為 0、columns 為空；preview 的 `maxProperties` 256）與 `valid-import.json`（原始值、中文欄名）；`internal/import/profile` 依此產生 profile 與 preview，推斷規則與上限見 API_DATABASE §5，需要 owner 確認的選擇見 CB-39。(c) **實作（T-017，2026-10-20，待獨立 review）：** 新增 `import-mapping.schema.json`（每條只有 `source_column`、`target_field`、`transform`，`additionalProperties: false`；transform 由 target field 決定），`http.schema.json#/$defs/SaveMapping` 的 `mapping` 改為它，`ai-mapping` 的 target 與 transform 改用同一份定義；帶 `confidence`、`evidence` 或 `requires_confirmation` 的 mapping 一律 422。詞彙、確認與 lifecycle 的細節為 D-070–D-072（PROPOSED）。 |
| D-044 | 原報告 TBD（object storage） | P0 的 object storage | **APPROVED（2026-10-19，H-010）。** `internal/storage/objects` 定義 S3 相容的最小介面（put 時計算 SHA-256 與大小、get、delete 只給受控 maintenance；key 一律由 server 依 D-035 產生）。P0 先提供 filesystem adapter，只用於 development 與測試（`APP_ENV=production` 時拒絕啟動）；正式環境的 adapter（雲端廠商、bucket、region、加密金鑰、retention、signed URL 期限）由 owner 選定雲端後另立 ADR，未定前不收真實客戶資料（SECURITY §6）。下載先由 API 授權後串流，不發 signed URL。gate：T-016 寫入 upload 前。**實作（T-023，2026-10-19，待獨立 review）：** `internal/storage/objects`（`Store`、`Maintainer`、`NewKey`／`ValidKey` 與 DB 的 `object_key_ok` 同一規則）與 `FS` adapter（`OBJECT_STORAGE_PROVIDER=filesystem`、`OBJECT_STORAGE_ROOT`；只在 `APP_ENV` 為 development／test 時接受，`APP_ENV` 未設也拒絕；寫入不覆寫既有 key）；simulation trace 由 API 授權、核對大小與 SHA-256 後串流（`downloadSimulationTrace` 改回 `TraceArtifact`，見 CB-38(b)）；dataset snapshot 由 `internal/storage/snapshots` 讀取。正式 adapter 與孤兒 object 的 maintenance 未做。**T-016（2026-10-19，待獨立 review）：** 上傳以 `objects.NewKey` 的 key（`tenant/<org>/imports/<import>/upload`）串流寫入並記錄 SHA-256 與大小；error report 寫到本 import 自己的 key，由 API 授權後核對 SHA-256 與大小再串流（`downloadImportErrors` 不再回 `SignedDownload`，CB-38(b)）；worker 讀 upload 時同樣核對。 |

### 其他契約缺口（2026-10-19，APPROVED，H-010）

| ID | 解決 | 主題 | 提案 |
|---|---|---|---|
| D-045 | CB-17 | 使用者自己的 membership（`/me`） | **APPROVED（2026-10-19，H-010）。** 新增 `GET /v1/me`（任何已驗證的使用者；不帶 `X-Organization-ID`）：回 `user_id` 與 `memberships[]`（`organization_id`、organization `name`、`role`），依 organization 名稱再依 ID 排序，P0 至多 100 筆；未知 identity 回 403（不自動建立，D-008）。RLS 新增只讀的 policy：API 只看得到 `user_id` 等於驗證後 scope 中 user 的 membership，與這些 membership 所屬 organization 的 `id`、`name`（新 migration；其他 tenant 的任何 row 仍不可見）。UI 以此選 organization 並取得 role，role 仍只決定介面，API 每次自己授權。gate：T-043 的 UI bootstrap 前。**實作（T-043 的 D-045 slice，2026-10-19，待獨立 review）：** migration 0008 的 `own_memberships`、`own_organizations` 是只給 `twin_api` 的 permissive `FOR SELECT` policy，只在**沒有選 organization** 時成立，所以選了 organization 的 transaction 看到的與 0002 的 `tenant_isolation` 完全相同；`tenant.DB.RunSelf` 是只設 user 的唯讀 transaction；`internal/tenant/me` 與 `httpapi.Self`（帶 `X-Organization-ID` 即 400）；排序為 byte 順序（`COLLATE "C"`，與 DB 的 collation 無關）；超過 100 筆時回前 100 筆並帶 `truncated: true`；不寫 audit；client 的 `getMe()`。見 checks/T-043_VALIDATION.md 的 D-045 一節。 |
| D-046 | CB-31 | AI 新增 entity 的 ID allocation pool | **APPROVED（2026-10-19，H-010）。** 每個 proposal 在建立時由 server 產生 pool：每種 entity（node、flow、resource）16 個 ID，格式 `<kind>_<小寫 ULID>`（符合 IR 的 ID pattern），存在 `ai_proposals.id_pool`（新 migration；之後不可改）並放進 provider context。Validation 與 approval 時要求 patch 新增的每個 entity ID 都來自同 proposal 的 pool、種類相符、在 base IR 中未使用；不足時 provider 回 `needs_input`。gate：T-024 的 context builder 前。T-024 實作：`internal/ai/idpool`（產生、形狀檢查、`Check(base, candidate)` 回報 `not_in_pool`／`wrong_kind`／`used_in_base`／`missing_ir`）；schema `common.schema.json#/$defs/IdAllocationPool`；migration 0011 新增 `ai_proposals.id_pool`（NOT NULL、`twin.id_pool_ok` CHECK、建立後由 trigger 禁止修改），既有 proposal 的資料庫拒絕套用。違規的 issue code 未定（CB-40）。 |

### T-046 的提案：電子產線的引擎語義與 domain 規格（2026-10-19 提出，2026-10-20 APPROVED，H-018）

H-016 把 P0 domain 改為電子組裝／測試線，引擎要補上有限緩衝與阻塞、故障、班表、隨時間變化的投入與批次（REQ-024）；H-017（owner：「時間還很久，盡量保留所有功能」）把換線與 lognormal／Weibull 列為 P0 提案；agent 另依此方向建議把數項原本延後的功能納入 P0，由 owner 在本頁決定。H-017 只是方向，不批准任何 decision。T-046 phase 1 提出新核心 primitive 的 [ADR-026](adr/ADR-026-line-flow-primitives.md)（PROPOSED）與 domain 規格 [ELECTRONICS_LINE.md](domains/ELECTRONICS_LINE.md)（PROPOSED）。每項 decision 的選項與後果、對決定性與 result digest 的影響、golden vectors 草案與 SimPy reference 的做法寫在這兩份文件中對應 D-ID 的一節；下面只列問題與建議。全部 **PROPOSED**：approved_by／approved_at 為 null，批准前不實作 kernel、不改 schema 與 fixtures。Gate：T-046（phase 2 的 contract 變更）等 D-047–D-059；T-047–T-051 依各自用到的 decision；T-027 等 D-059；T-052 等 D-058–D-060。T-017 不另加：它的驗收（TEST-012、TEST-013）是一般的 mapping，station log 的 mapping 驗收在 T-052。

#### D-047 — 新 primitive 的相容性契約與 differential

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Simulation / Contracts。Gate：T-046 phase 2 改 schema 前；T-047–T-050 實作前。**

問題：新欄位如何進入 IR、Result 與 engine_version，才能讓沒用到的模型與現行 kernel 逐 bit 相同？選項：A optional 欄位、schema_version 維持 0.1；B 新 schema_version 0.2 加升級流程；C 新統計一律進 Result（會改所有既有 digest，不採用）。建議 A：Result 的新成員只在用到時出現；每個 kernel 任務升 engine_version minor；沒有新欄位的模型以「observation 序列相同、engine_version 換回 `des-go-0.1.0` 後 digest 等於鎖定值」判定，鎖定檔不重產；用了新欄位的中性設定只比 observation 序列與既有 Result 成員，且不得有任何新 observation（含 t=0 的 Capacity、ResourceUp）。

#### D-048 — 新事件的同 tick 順序

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Simulation。Gate：T-047 實作前。**

問題：故障、修復、班表容量變化、批次逾時與解除阻塞在同一 tick 的哪裡？選項：A 新增 priority 15（在 arrival 10 之後、dispatch 20 之前）；B priority 5；C priority 25。建議 A：dispatch 看到該 tick 最後的容量，既有三個 priority 的相對順序不變；解除阻塞在 dispatch 內進行，不是事件；t=0 的順序為 source、calendar（每個 calendar 一個事件）、各 unit 的故障時鐘。

#### D-049 — 有限緩衝與阻塞

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Simulation。Gate：T-047 實作前。**

問題：容量定義在哪、BAS 或 BBS、被擋 entity 持有什麼、與 resource-wise FIFO 和 bundle 的交互、source 與 delay 被擋時？選項：A blocking after service；B blocking before service（要跨 process 預留空位、提早抽 routing）；C 以下游 slot resource 模擬（resource 無法跨 process 持有）。建議 A：顯式 queue 的 `capacity` 限制等待中的 entity 數；被擋的 entity 留在出發 node（process 保留整個 bundle，source 形成 release backlog）；解除只在 dispatch 內依 FIFO 進行，且不再另排 dispatch；先解析、後提交，沒有被擋時 observation 與 seq 與現行相同，路由中途觸發 limit 時錯誤前的 observation 也與現行相同。

#### D-050 — 死結與停滯的防止與偵測

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Simulation。Gate：T-046 phase 2 前；T-047–T-050 實作前；T-051 的 template 須通過。**

問題：循環阻塞與「bundle 永遠湊不齊」（review 的兩個反例：班表不相交的 bundle、相位相反的 constant 故障）怎麼防止與以 typed error 偵測，保證到哪裡？原提案的 backstop 在有週期性班表或故障時永不觸發；例外日不是週期的，使期限與狀態簽章在例外日前後失效（review N6）。選項：A 靜態 R1–R4 全為 error，加 runtime 的 B1、B2；A′ R4 為 warning，另以狀態簽章偵測固定相位的鎖定；B 只做 runtime 偵測；C 不偵測（違反 TEST-034）。建議 A：R1 `BLOCKING_CYCLE`；R2 `CALENDAR_INVALID`（每個 bundle 在班表的最小公倍數週期內至少有一個 tick 湊得齊）；R3 故障 resource 的 request units＝1、一個 bundle 至多一個故障 resource；R4 `STALL_RISK`（故障 resource 上班時夥伴都夠，只看週期部分；訊息建議給機台作業員的 calendar）。每個 valid 的模型都可證明不會永久停滯；B1 `ErrDeadlock`（循環等待，立即、無誤報）；B2 `ErrStalled`（bundle 沒有任何 unit 在使用中，且已過期限 next_fit(max(t_q, X_end)＋TTRmax)；X_end 是 bundle 的 calendar 最後一段例外的結束）。兩者對 valid 的模型都不觸發、不記狀態。代價：R4 拒絕「會故障的機台沒有班表、作業員有班表」等寫法。A′ 的代價：簽章只能在 X_end 之後比對、記憶體要以每個 replication 的總量設限並計入 limits、只偵測 TTF／TTR 固定的鎖定，其他不偵測。這也回答了「warning 或 error」。

#### D-051 — Resource 故障（MTBF／MTTR）

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Simulation。Gate：T-048 實作前。**

問題：故障時鐘、服務中 entity 的處理、多 unit、RNG stream 命名、availability？選項（處理）：A finish-first、每個 unit 一組故障時鐘；B preempt-resume；C preempt-restart（B、C 屬 PRD P1）。選項（時鐘）：日曆時間、排班時間、忙碌時間。建議 A，時鐘一律只在 resource 上班時前進（由確定的班表直接算出故障時刻，不需 unit identity；沒有 calendar 時即日曆時間；日曆時鐘在 `two_shift_weekdays` 下會使上班時的故障率只剩約 48%）。D-050 建議 R4 為 error，所以不提供 `clock` 欄位；若 owner 改採 A′ 再加回。TTF 要以上班時間估計；stream 為 (resource ID, `ttf/<u>`／`ttr/<u>`)，既有 stream 不變；availability＝∫min(S, C−down)／∫S。代價：長時間 service 低估中斷影響。

#### D-052 — 班表與 utilization 分母

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Simulation。Gate：T-049 實作前。**

問題：班表在 IR 中的表示（tick、無時區）、交班時服務中的 entity、utilization 的分母、每班人數與例外日？選項：A 根層共用的 `calendars[]`＋ resource 的 `calendar_id`；A＋ 再加每班容量（`shift_capacity`）與以絕對 model 時間表示的 `exceptions`；B 每個 resource 自己的容量表；C 絕對日期與時區（時區不採用）。建議 A＋（agent 依 H-017 的方向）：交班 finish-first（記 overtime），原 `utilization_mean` 不變，另加 scheduled utilization 與 overtime；每個 calendar 在 S 改變時一個事件，只對 S 改變的 resource 記 observation。

#### D-053 — 隨時間變化的投入

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Simulation。Gate：T-049 實作前。**

問題：分時段的投入怎麼表示、與 common random numbers 的關係？選項：A 週期性分段常數 rate、以累積 rate 反函數產生（poisson 或 deterministic，每件一次 draw）；B 投入表（整批投入）；C 各時段各自的 interarrival 分布（打亂配對，不採用）。建議 A＋B（agent 依 H-017 的方向）：scenario 之間第 k 件用同一個 draw；numerical profile 固定（含 r 落在 (0, P] 之外時的修正、超過 365 天時的飽和）；投入表每列一個 ARRIVAL、不用 RNG。

#### D-054 — 批次 process（燒機）

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Simulation。Gate：T-050 實作前。**

問題：批量上下限、等待上限、成員 ID 與 trace、lead time 與 utilization？選項：A min／max／max_wait，就緒時一個 batch request、一次 service draw、整批離開才釋放；B 只有固定批量；C 合併成一個 entity（建議之後另立任務）。建議 A：成員保留自己的 entity ID，批次 ID `r<rep>/<process>/b<n>`；lead time 與 queue wait 照每件計；utilization 為 chamber 忙碌，另有批量統計；有成員被擋時的 observation 順序已固定。

#### D-055 — 換線（P0）

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Simulation / Product。Gate：T-050 實作前。**

依 H-017 列為 P0 提案；PRD 已列 P1 的優先權、preemption、進階 routing 仍為 P1。問題：產品怎麼表示、換線何時發生在哪個 unit、與 FIFO 和其他功能的交互？選項：A resource 層的 sequence-dependent 換線（`setups` 矩陣、每個 unit 自己的換線狀態、FIFO 派工）；B process 層的固定 setup（無法表示每台機的狀態與矩陣）；C A 加依產品分組的派工（屬 PRD P1）。建議 A：產品＝entity_type（每個產品一個 source）；授予時優先選狀態相符的 unit，先抽換線再抽 service，不新增事件種類；stream 為 (resource ID, `setup/<u>`)；單一產品的模型與現行逐筆相同。

#### D-056 — lognormal 與 Weibull（P0）

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Simulation。Gate：T-048 實作前。**

D-063 (c)（APPROVED，H-019）修正本項的錯誤碼：lognormal `mean`／Weibull `scale` 換算後超過 365 天回 `LIMIT_EXCEEDED`（D-029 的 limits 階段，先於 distribution 階段），不是本項原寫的 `DISTRIBUTION_INVALID`。

依 H-017 列為 P0 提案。問題：參數化、抽樣演算法與 numerical profile？選項：A 兩者皆以反函數法、每個樣本一次 draw（lognormal 以 mean、cv 參數化，Φ⁻¹ 用固定的 AS241；Weibull 的冪次以固定的 exp 與 ln 計算）；B lognormal 用 Box–Muller（每個樣本兩個 U，不採用）；C 只加其中一個。建議 A：stream 消耗與 golden 方法不變；需要固定的 exp、Python 逐運算移植、vectors 與新的 engine_version；實作併入 T-048。

#### D-057 — 新參數的 scenario allowlist 與 risk

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：AI / Simulation。Gate：T-047–T-050 各自開放對應參數前。**

問題：新參數可否在 scenario 中修改、risk 怎麼算？選項：A 擴充 D-021（buffer capacity、breakdown 分布、換線分布、投入 rate 與投入量、batch 參數、calendar 參照、每班容量、shifts 與例外）；B 不擴充（「加大 FCT 前緩衝」須發布新版本，與 baseline 不可比較）；C 只加 buffer capacity。建議 A：新參數的變更至少 medium，擴大 buffer 為 low；candidate 須通過 D-050 的 R1–R4（違反即 invalid；若 owner 改採 D-050 的 A′，新出現 `STALL_RISK` warning 時仍可建立、warning 顯示在審核內容中且 risk 至少 medium）；允許改分布的路徑都可換成 lognormal／Weibull；新增或刪除 breakdowns、setups、calendars、schedule、batch 仍走 draft→publish。

#### D-058 — 電子產線指標的資料來源與 Result 位置

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Domain / Simulation。Gate：T-046 phase 2 改 schema 前；T-051 與 T-052 實作前。**

問題：Pack 的 `ComputeMetrics` 只看 Result，指標放哪、需要哪些 generic 統計？選項：A optional 的 `ComputeModelMetrics(effective IR, result)`、新的 optional `metrics.domain`，並列出 pack 需要的 generic Result 成員；B 由 kernel 讀 domain 屬性（違反 ADR-010）；C 只在 UI 端計算（不在 digest 中）。建議 A：每個 replication、每個 resource 的整數 tick 積分（∫U、∫K、∫Z、∫D、∫S、∫A、∫min(U,S)、∫min(P,S)、∫max(0,A−U)、∫max(U−S,0)）與停機、修復、換線的次數，每個 node 的阻塞統計、每個批次 process 的批量，都只在用到對應功能時出現；宣告式的 `system_wip`、`flow_count`、`sink_count`、`period_throughput`（後三者為 agent 依 H-017 的方向新增的建議）；線班表的上班時間由 pack 從 IR 算出。FPY 是模型輸入；公式見 ELECTRONICS_LINE §5。

#### D-059 — Station event log 的 canonical fields

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Import / Domain。Gate：T-046 phase 2 改 canonical-event schema 前；T-027 的電子產線 inference 與 T-052 前。**

選項：A canonical-event 新增 optional `end_timestamp`、`shift` 與 `product`（多產品時必填，D-055），pack 定義 event_type／outcome 詞彙，停機紀錄以設備 ID 為 case_id；B 不改 schema、每筆拆成 start／end 兩個 event；C 沿用 duration_ms 並新增兩欄相減的 transform。建議 A；operator ID 不匯入；常數 transform 屬 CB-28。

#### D-060 — 模擬與觀測比對（REQ-025）

**狀態：APPROVED（2026-10-20，KCL1104，H-018：「全都照建議」）。負責角色：Simulation / Product。Gate：T-052 實作前。**

選項：A 分「輸出比對」與「輸入一致性」，以每期平均比較，顯示模擬平均、replication 範圍、絕對與相對誤差，標 in-sample／hold-out；B 只並列數字；C 統計檢定給通過判定。建議 A：不給通過判定、不自動校準；沒有觀測資料時 unavailable；宣告 `period_throughput`（D-058）時另做逐期比較。

### T-025 的提案：AI proposal 的產生與 server review（2026-10-20 提出，同日 APPROVED，H-019）

T-025 實作時需要的契約細節。兩項都已由 owner 批准（2026-10-20，H-019：「Q2 選 B，其他照建議」）：D-061 全部依建議；D-062 依建議，只有 (i) 選 B（見該項）。

| ID | 解決 | 主題 | 提案 |
|---|---|---|---|
| D-061 | CB-40 | AI 錯誤與 issue 的契約 | **APPROVED（2026-10-20，KCL1104，H-019；同日由 T-025 提出）。** (a) `ValidationCode` 新增 `ID_POOL_VIOLATION`：patch 新增的 entity ID 不是此 proposal 同種類 pool 的 ID（`idpool.Check` 的 `not_in_pool`／`wrong_kind`／`used_in_base`／`missing_ir`），path 指向該 ID，message 為固定句，不引用 ID 本身。(b) `ValidationCode` 新增 `AI_OUTPUT_INVALID`：provider 回答不符 output schema、不是 proposal（status、requires_confirmation、changes 與 questions 的組合不對）、patch 不改變模型、文字為空或含 U+0000（summary、assumptions、questions、expected effects，以及 changes 的值、member 名稱與 path；PostgreSQL 的 text 與 jsonb 存不下，H-003 review M2），或 expected effects 含數字（D-062 (e)）；message 固定，不引用回答內容。兩者都有 en／zh-Hant UI 字串。(c) Provider 錯誤是 job failure，code 為 `AI_` 加上 provider client code 的大寫（例如 `AI_RATE_LIMITED`）。message 固定，不含 provider 原文、HTTP status 或 credential label。只有 `concurrency_limited`、`timeout`、`rate_limited`、`unavailable` 會重試（ADR-024 的 attempt 上限內）。Provider 呼叫之後，除了這四種 provider 錯誤、worker 的中斷（`LEASE_EXPIRED`、`WORKER_STOPPED`）與寫入結果時 DB 的暫時錯誤（worker 記為可重試的 `INTERNAL`，例如連線中斷、serialization failure），任何失敗都不重試，因為重試會再付費呼叫一次 provider（H-003 review L3）：base version 的 dataset snapshot 在呼叫 provider 之前讀取並驗證；讀取失敗為可重試的 `DATASET_STORAGE_FAILED`（尚未呼叫 provider，重試不花費），snapshot 驗證不過或 worker 沒有 object storage 為不重試的 `DATASET_STORAGE_FAILED`；呼叫之後的 dataset binding stage 只讀這些副本，不讀 storage，若仍出錯（只可能是 server 自己的錯）也不重試。DB 拒絕存放產生的內容（SQLSTATE 22021、22P05、23514）為不重試的 `PROPOSAL_OUTPUT_UNSTORABLE`（H-003 review M2）。Job 最終失敗時 proposal 跟著轉為 `failed`（migration 0012）。API 的 `error` 以 `details[0].code` 保留原 code，`error.code` 對應到 `ErrorCode`：`AI_PROVIDER_NOT_CONFIGURED`、`AI_POLICY_NOT_APPROVED` 不變；`AI_RATE_LIMITED`、`AI_CONCURRENCY_LIMITED` 對應 `RATE_LIMITED`；`AI_TIMEOUT`、`AI_UNAVAILABLE`、`AI_CREDENTIALS_EXHAUSTED`、`DATASET_STORAGE_FAILED`、`LEASE_EXPIRED`、`WORKER_STOPPED` 對應 `SERVICE_UNAVAILABLE`；`AI_CONTEXT_TOO_LARGE` 對應 `PAYLOAD_TOO_LARGE`；其餘（`AI_REFUSED`、`AI_OUTPUT_TRUNCATED`、`AI_REQUEST_REJECTED`、`AI_INVALID_REQUEST`、`PROPOSAL_INPUT_INVALID`、`PROPOSAL_OUTPUT_UNSTORABLE`、`JOB_CANCELLED`、`INTERNAL`）對應 `INTERNAL_ERROR`。`invalid_output` 不是 failure，proposal 轉為 `invalid`，並以 (b) 的 issue 說明（AI_SPEC §4）。部署沒有設定 AI 或 live AI 未批准時，`createProposal` 在寫入任何資料前回 503 `AI_PROVIDER_NOT_CONFIGURED`／`AI_POLICY_NOT_APPROVED`。(d) `ai_proposals.provider_metadata` 存 T-024 的 `provider.Metadata`：provider、model、credential label、role、template、schema、locale、hash、usage、latency、省略與遮蔽計數；不存 prompt、回答或 key。目前不另立 schema，HTTP 也不顯示。(e) HTTP `Proposal` 不顯示 `id_pool` 與 `output_locale`，UI 目前用不到；T-028 需要時再加入 contract。(f) Review 回傳的每個 issue（invalid 的，以及 ready 保留的 warning）都經清理（`review.Clean`，H-003 review M1）：message 換成該 code 的固定句（review 自己的固定句保留；catalogue 以外的 code 用一句通用句），不引用回答、ID、member 名稱、值或 path；path 只保留 array index 與 contract schema（model-ir、common、json-patch、proposal-output、proposal）宣告的 member 名稱，遇到其他 token 即截到最深的已知祖先（例如 `/views/2d/positions/<node ID>` 截為 `/views/2d/positions`）。T-024 client 的 invalid_output issue、patch、candidate validation、domain pack 與 dataset binding stage 的 issue 都一樣。不採用時：(a)、(b) 改回 open code，需改 schema、i18n 與 `internal/ai/review` 的 codes；(c) 改 `internal/proposals/public.go` 的對應表與 generator 的重試；(f) 改 `internal/ai/review/clean.go`。 |
| D-062 | T-025 實作的解讀 | Proposal 生命周期與 server review | **APPROVED（2026-10-20，KCL1104，H-019；同日由 T-025 提出；(i) 選 B，2026-10-25 owner 確認為本意，H-022）。** (a) Proposal 跟隨它的 job，與 run（0009）、import（0010）的做法相同：每個 proposal 最多一個 `model_proposal` job（0001 的 `jobs_one_proposal`）。`generating` 轉為 `ready`／`needs_input`／`invalid` 時，job 必須在同一 transaction 中 completed（deferred constraint trigger）。Job failed 或 cancelled 時，proposal 由 trigger 轉為 `failed` 並帶 job 的 failure，同時清空 generating 期間寫入的內容；proposal 只在它的 job 已 failed 或 cancelled 時才能轉為 `failed`，不以 trigger 的深度判斷（H-003 review I1）。API 不能改 `generating` 的 proposal。建立後 ID、`output_locale`、`selected_entity_ids`、`created_by`、`created_at` 都不能改。(b) Stale base：`base_version_id` 不是 model 的 latest version 時，`createProposal` 回 409 `MODEL_VERSION_CONFLICT`（`/base_version_id`），不建立 proposal。Generation 期間 base 變舊不影響產生，由 T-026 的 approval 回 409。(c) Schema 與 DB 加嚴：`invalid` 必須帶 `validation.valid=false`，只存 issues 與 provider metadata，不存回答的任何文字：沒有 summary、assumptions、changes、patch_hash、expected effects、questions、dataset bindings、server risk、provider claim、effective risk、confidence，issue 的 message 與 path 也不引用回答（D-061 (f)）。只有 `failed` 帶 `error`，`failed` 除 error 外沒有任何產生的內容，也沒有 provider metadata。Proposal 建立時沒有 `decided_by`、`decided_at`（H-003 review L2；migration 0012 的 CHECK 與 INSERT guard、`proposal.schema.json`）。(d) Server risk（D-016 的細節）以兩個 IR 的 entity ID 比對計算，不看 patch 或 provider 怎麼說。High：base 的 node 或 resource 不見了、node 改了 type。至少 medium：capacity 降低；process 需要的 resource 或數量改變（數量減少也算，H-003 review L1）；flow 改接；流向 failure／rework node，或位在 cycle 上的 flow，probability 提高；新增 cycle；新增或刪除 entity、entity type、metric；constraint 改變。其他為 low。最終 risk 為 max(server, claim)（D-027）。(e) Expected effects 只能是定性描述（AI_SPEC §5）：其中的數字必須是 base 或 candidate 的數值參數（capacity、units、分布參數、arrival 上限、routing probability、constraint 值），或 base 的名稱中的數字，否則以 `AI_OUTPUT_INVALID` 判為 `invalid`。Patch 新增或改寫的名稱是 provider 寫的文字，其中的數字不算（H-003 review M3）。Intent 中的數字也不算，因為把使用者要求的結果寫成效果仍是預測（eval MOD-050）；使用者提供的數字應寫在 assumptions。(f) Review 順序：output 形狀（含空文字與 U+0000）；patch（D-020／D-021／D-022 allowlist、test-before-modify、atomic）；candidate validation；no-op；ID pool；domain pack；expected effects；risk；最後是 dataset binding stage（D-006、D-033；base 的 snapshot 在呼叫 provider 前讀取並驗證，D-061 (c)）。遇到第一個有 error 的階段即停止。(g) Ready proposal 的 `dataset_bindings` 是 base version 的整份 dataset manifest，依 logical_ref 的 bytes 排序（D-034），不論 candidate 是否用到其中每個 binding；這與 T-012 由 draft 發布 version 時保留 base 的全部 binding 相同（`internal/models`、TWIN_IR_SPEC §5.2）。T-026 批准 edit proposal 時以此凍結新 version 的 manifest（H-003 review L5）。(h) 實作中其他決策沒有涵蓋的選擇（H-003 review I5）：`selected_entity_ids` 中不在 base 的 ID 照樣接受，只在 context 中計為省略（`CONTEXT_OMISSIONS`），不回 422；`getProposal` 不顯示 intent 與 `selected_entity_ids`（與 D-061 (e) 的 `id_pool`、`output_locale` 相同，T-028 需要時再加入 contract）；worker 沒有 model 的 domain pack 時為不重試的 `PROPOSAL_INPUT_INVALID`，部署必須登錄所有 model 用到的 pack；DB 不要求 generating 的 proposal 有 job（與 0009 的 run 相同），API 在同一 transaction 建立兩者，沒有 job 的 proposal 會一直是 generating。(i) Owner 已選 B（H-003 review I4；H-019）：flow 改接使某個 node 從 source 不可達時，目前為 medium（flow 改接的規則）並帶 `UNREACHABLE_NODE` warning。選項 A（建議，維持現狀）：node 仍在模型中，之後可以再接回；TWIN_IR_SPEC §4 把不可達的 node 定為不阻擋 publish 的 warning，D-016 只把刪除定為 high。選項 B：視同刪除，改為 high。**Owner 選 B（H-019）**：base 中有 source 到得了（經 probability > 0 的 flow，與 `UNREACHABLE_NODE` 相同）、candidate 中仍存在但沒有任何 source 到得了的 node，與刪除同為 high；依同一理由，刪除 flow 造成的不可達也算（`internal/ai/review/risk.go` 的 `reached`）。Base 中本來就不可達的 node 不受影響。不採用時：(a) 需改 migration 0012；(b) 改為建立後失敗，或允許舊 base；(c) 需改 0012 與 `proposal.schema.json`；(d)、(e) 改 `internal/ai/review`；(g) T-026 另定 manifest 的規則，並改 generator；(h) 各項改 `internal/proposals`、generator 或 0012；(i) 選 B 時改 `internal/ai/review/risk.go`。 |

### T-046 phase 2 的提案：kernel 支援前的 fail closed（2026-10-20 提出，同日 APPROVED，H-019）

T-046 phase 2 依 ADR-026 Consequences 改 contract 時需要的選擇。Owner 於 2026-10-20 依建議批准（H-019）：(a) 採 A；(b) 的 shift label 採建議 B；(c) 採 `LIMIT_EXCEEDED`；(d) 照列。

| ID | 解決 | 主題 | 提案 |
|---|---|---|---|
| D-063 | T-046 phase 2 | 產線流動 primitive 在 kernel 支援前的 fail closed，與 phase 2 的 contract 細節 | **APPROVED（2026-10-20，KCL1104，H-019；同日由 T-046 phase 2 提出）。** (a) 問題：schema 依 ADR-026 接受新成員後，現行 validator 沒有它們的語意規則（D-050 的 R1–R4、班表、投入、批次、換線），會把班表重疊、循環阻塞等模型判為 valid，允許 publish 成不可修改的 version；des-go-0.1.0 也不讀這些成員，會把模型當作沒有它們而模擬，等於捏造結果。選項：A 新增 ValidationCode `PRIMITIVE_NOT_SUPPORTED`（error）：validator 在 JSON Schema 之後設 primitive support 階段，對本 build 尚未實作規則的每個用到之處回 error（path 為該成員，message 固定、不引用模型內容），之後的階段不執行（D-023）；engine 的 Compile 另有自己的 kernel 支援清單，同樣拒絕；T-047–T-051 在同一個任務中加入規則、kernel、engine_version 與 D-057 的 risk 後，才把自己的 primitive 加進兩個清單。B 沿用 `IR_SCHEMA_INVALID`：schema 其實接受這些成員，UI 會顯示「值的結構或型別不正確」，誤導。C 只在 engine 拒絕：validator 仍把未檢查的模型判為 valid、可 publish，T-049 之後才發現既有 version 無效。D 等各 kernel 任務才改 schema：違反 ADR-026 的 phase 2 與 D-047、D-058 的 gate，fixtures 與 TS 型別要分五次改。**建議 A（已實作）。** 後果：draft 可以存這些成員（D-028 只要求 schema-valid），publish、scenario、AI proposal 與 run 都被拒絕；en／zh-Hant 字串。(b) Phase 2 中由 agent 選定、批准文字沒有寫到的細節：根層 `calendars` 有 1–100 個（缺＝沒有 calendar），calendar 有 optional `name`；shift `label` 用 ID 格式，多個 shift 可共用 label；`shift_capacity` 是 label→0..100000 的 object（1–100 個 label），只能與 `calendar_id` 一起出現；exception 的 `open` 必填；arrival 的 `distribution` 與 `schedule` 恰有一個；setups 的 `default` 與 `transitions` 至少一個，transitions 至多 9900；lognormal 的 `cv`、Weibull 的 `shape` 在 schema 只要求 > 0（ADR 的候選範圍屬 DISTRIBUTION_INVALID，T-048 定案）；metrics 為 `flow_count`（`flow_id`）、`sink_count`（`sink_id` 必填）、`period_throughput`（`period`）、`system_wip`；Result 加入 D-058 表中沒有的 `buffers[<queue>].full_ticks`（D-049 列的 buffer 滿比例），`declared` 的值為數、計數陣列（至多 1000 期，候選上限）或 null，resource state 的每組成員以 `dependentRequired` 同時出現；trace 加 `blocked`、`unblocked`、`setup_start` 與 `resource_id`、`batch_id`，format 維持 `geminus.trace.v1`；`SIMULATION_DEADLOCK`／`SIMULATION_STALLED` 以 `MODEL_VALIDATION_FAILED` 顯示（沿用 T-023 對 execution limit 的暫定對應，CB-38(a) 仍待定）；D-057 標 replace 的 scenario 路徑只接受 `replace`，queue `capacity` 接受 replace、add、remove，`calendars` 適用 D-020；D-050 的 R2–R4 放在 distribution／domain semantics 之後新設的 bundle feasibility 階段；R3、`BATCH_INVALID`、`SETUP_INVALID`、`CALENDAR_INVALID` 的 path 依 TWIN_IR_SPEC §4；（review 第 1 輪補列）calendar 的 `exceptions` 若出現須至少 1 段（缺＝沒有例外）；`batch_id` 為 `r<rep>/<process>/b<n>`，n 從 1 起、rep 至多 999（與 entity ID 相同）；宣告式 metric 的 gate path 為 `/metrics/<i>/type`；`ResourceState` 以 `anyOf` 要求至少一組功能成員、以 if/then 要求有 `available_ticks` 時必有 `down_ticks` 或 `scheduled_ticks`；scenario 的 `replace calendars/*/shifts` 可移動、增減 shift，但每個 calendar 的 shift label 集合不可改變（新增、刪除、改名 label 為 PATCH_PATH_FORBIDDEN）——D-057 只寫「shifts（replace）」，這是 phase 2 暫行的較嚴格解讀，**請 owner 決定 scenario 可否改 shift label**。選項 A（暫行）：維持；後果是有 label 的班即使沒有任何 `shift_capacity` 參照它，也不能用 scenario 砍掉（T-046 phase 2 review 第 2 輪 L1 實測），要走 draft→publish，與 baseline 的比較就不是同一個 model。選項 B（建議；**owner 依建議選 B，H-019**）：scenario 可改 label；被 `shift_capacity` 參照的 label 若被刪除或改名，由 candidate validation 回 `CALENDAR_INVALID`（D-052，T-049 實作）；沒被參照的 label 不影響 kernel（D-052：沒列出的 label 用全部 capacity）。選 B 時，T-049 解除 calendars 的 gate 前放寬 `internal/model/patch/policy.go` 的 `afterApply`；calendars 目前被 gate 擋住，T-049 之前兩者沒有行為差異。(c) 衍生文件之間的差異：D-056 寫 lognormal `mean`／Weibull `scale` 換算後超過 365 天回 `DISTRIBUTION_INVALID`，但 D-029 的 limits 階段對所有分布參數回 `LIMIT_EXCEEDED` 且先執行；建議 `LIMIT_EXCEEDED`（與 D-029 一致）；**owner 依建議批准（H-019）**。(d) ADR-026 phase 2 的 bullet 列出 validator codes 與「`RESOURCE_REQUIREMENT_INVALID` 的 D-050 R3 規則」：phase 2 只交付 codes、它們在 D-023 中的位置與手推 fixtures，規則本身（含 R3）延到 T-047–T-050（ADR 的「實作」欄），在那之前由 (a) 的 gate 擋下；這個分工與 (a) 一併請 owner 確認。不採用時：(a) 改 B 只需換 code 與字串；改 C 要拿掉 validator 的階段；(b)(c) 各自改 schema、fixtures 與 TWIN_IR_SPEC。gate：T-047–T-051 各自解除 gate 前。 |

### T-047 的提案：有限緩衝與阻塞的實作細節（2026-10-20 提出，PROPOSED）

T-047 依 D-047–D-050、D-057（APPROVED，H-018）與 D-063（APPROVED，H-019）實作時，批准文字沒有寫到、但會影響行為或 observation 的細節。全部已依建議實作（局部、可回復），**owner 尚未決定**：`approved_by`／`approved_at` 為 null。

| ID | 解決 | 主題 | 提案 |
|---|---|---|---|
| D-064 | T-047 實作的解讀 | D-050 B1 的檢查時點、R1 的範圍、解除的順序、ErrDeadlock 回報的 entity、長度 0 的阻塞、解除後的 queue wait | **APPROVED（2026-10-25，H-021，依建議：(a)(b)(d)(e)(f) 採 A，(c) 照列，(g)(h) 為說明）；2026-10-20 T-047 提出。** (a) **B1 的檢查時點**：批准文字寫「每次 dispatch 結束時檢查」，G-D2 寫「在最後一個服務中的 entity 完成並被擋的那個 tick 回 ErrDeadlock」。被擋的 SERVICE_COMPLETE 不釋放、不 enqueue，所以不排 dispatch（D-049）；若該 tick 沒有其他 dispatch，只在 dispatch 結束時檢查會漏掉這個 deadlock，之後到達的 entity 也被擋、不再有 dispatch，run 默默跑到 horizon（違反 TEST-034 與 D-050 的「立即」）。選項 A（建議，已實作）：程式有有限 buffer 時，每個事件結束、且本 tick 沒有待執行的 dispatch 時檢查（dispatch 結束時即屬此情形；有待執行的 dispatch 時延到它結束，所以不誤報）；檢查只讀計數、不改事件與抽樣。選項 B：只在 dispatch 結束時檢查（字面）；後果：G-D2 這類循環等待不報錯，run 以截尾結束。(b) **R1 的範圍**：批准文字的阻塞邊沒有限定 source 到得了的 node；A（建議，已實作）照字面，對到不了的 node 也檢查（保守：之後接回時已保證沒有循環阻塞）；B 只檢查到得了的 node（D-062 (i) 把「到得了」當成有意義的區別）；後果：A 會拒絕一個 dormant 部分的循環，B 在接回時才拒絕。(c) **解除的 observation 順序**：「授予使 buffer 少一件後」解讀為該次授予的 ObsResource、ServiceStart 與 service 抽樣之後，才是被擋 entity 的釋放（ObsResource）、Unblocked 與 QueueEnter（golden G-B8 固定）；只影響同一個 dispatch 內的 observation 順序，不影響任何數值或 RNG（抽樣用不同的 stream）。(d) **ErrDeadlock 回報的 entity**：照字面為「最早的 blocked entity」，包括 source 的 release backlog（G-D2 回報 `r0/src/5`，在 src，不是 cycle 上的 holder）；A（建議，已實作）照字面；B 優先回報最早被擋的 holder（較能指出 cycle 的位置，訊息格式不變）。「最早」指被擋的先後（事件的處理順序），不是被擋起的 tick，也不是 buffer 的 node 順序：同一 tick 被擋的兩件依處理順序（golden G-D3 固定，T-047 review 第 1 輪 L2）。（review 第 1 輪 I2 補列）(e) **長度為 0 的阻塞**：同一 tick 中 arrival 與 DELAY_COMPLETE（priority 10）在 dispatch（priority 20）之前處理（D-048），所以到達時 buffer 已滿、在同一 tick 的 dispatch 中就被解除的 entity，記為一次長度 0 的阻塞（blocked 與 unblocked 同 tick）：`blocks_started`、`blocks_completed` 各加 1，tick 為 0（G-B4 的 e3）。A（建議，已實作）照實際的 observation 計數；B 不計長度 0 的阻塞（metrics 要特別排除，trace 仍有這兩個事件）。(f) **解除後的 queue wait**：被擋的 entity 在解除時才進入 buffer（D-049：以新的 seq 排到 FIFO 最後），queue wait 從解除（queue_enter）起算；被擋的時間記在出發 node 的 `blocking`，不重複算進 queue wait。A（建議，已實作）；B 從被擋起算（同一段時間同時算阻塞與等待）。給 owner 的說明（不需選擇）：(g) B1 依批准文字只偵測**全域**的循環等待（沒有任何服務中或 delay 中的 entity）：循環旁若有一個一直在服務的不相關 process，循環上的 entity 會停到 horizon 而不報錯；只有 R1 被繞過時才會發生（valid 的模型沒有循環阻塞），review 第 1 輪 I3 實測。要偵測局部的循環等待需要另一個 decision（例如 wait-for graph），不在 T-047 的範圍。(h) `SIMULATION_DEADLOCK` 不重試（同一個凍結輸入一定再 deadlock）；多個 replication 都 deadlock 時回報哪一個取決於 worker 的排程，與其他 execution limit 相同（SIMULATION_SPEC §9 的 Engine 段）。不採用時：(a) B 改 `internal/simulation/engine/kernel.go` 的 `after`，並改 G-D2 的期望；(b) B 改 `internal/model/validate/blocking.go`，並加 reachability；(c) 改 `kernel.dispatch` 與 G-B8 的期望；(d) B 改 `kernel.deadlock`、G-D2、G-D3 與 `valid-run-failed-deadlock.json` 的 message；(e) B 改 `internal/results/metrics/aggregate.go` 的阻塞計數與 G-B4、G-B4W 的期望；(f) B 改 queue wait 的起點與 G-B1、G-B1W、G-B4W、G-B10–G-B12 的 queue wait 期望。Gate：T-047 驗收前；T-048–T-050 延伸 B1／B2 時沿用 (a)。 |

### T-026 的提案：人類批准與拒絕（2026-10-20 提出，2026-10-25 APPROVED，H-021）

T-026 實作批准與拒絕時，已批准的決策（D-003、D-004、D-011、D-027、D-030、D-046、D-062）與 AI_SPEC §6 沒有涵蓋的選擇。三項於 2026-10-25 由 owner 依建議批准（H-021）；在那之前都是 PROPOSED，agent 沒有批准任何一項。建議都只動 T-026 自己的程式、schema 的新成員或 migration 0013，可以撤回，所以依建議實作；owner 選其他選項時依各項「不採用時」修改。Gate：T-026（D-067 另為 T-028，D-068 另為 T-031 的 UI）。

| ID | 解決 | 主題 | 提案 |
|---|---|---|---|
| D-067 | CB-33 | AI scenario 批准所建立 Scenario 的名稱 | **APPROVED（2026-10-25，H-021，依建議：採 A，名稱取自 proposal summary）；2026-10-20 T-026 提出。** 問題：`scenario.schema.json` 要求 `name`（1–200 字元），但 scenario proposal 與 Approve 都沒有名稱來源（CB-33 前半）。選項 A（建議，已實作於 `internal/proposals/approval` 的 `scenarioName`）：名稱取自批准者審閱過的 proposal summary，每段連續空白合為一個空格並去掉頭尾空白，超過 200 字元時取前 199 字元（去掉結尾空白）加「…」；summary 只有空白時用 proposal ID。後果：名稱是 provider 寫的文字（與 summary 相同，UI 須 escape），scenario 不可修改，名稱之後不能改。選項 B：Approve 新增 `scenario_name`（scenario proposal 必填、edit proposal 不得帶，否則 422），由批准者命名；需改 `http.schema.json`、OpenAPI 與 TS。選項 C：固定格式（例如 proposal ID）。CB-33 後半（「latest=base」使舊版本上的 AI scenario 永遠不能批准）：D-062 (b)（APPROVED）讓 `createProposal` 只接受 latest，AI_SPEC §6 要求批准時 latest=expected=proposal.base，T-026 對兩種 mode 都照此檢查；舊版本上的 scenario 由人工建立（T-028）。若 owner 希望 base 變舊後仍能批准 AI scenario（scenario 不改 canonical version，理論上不需要 latest），需另立決策。不採用 A 時：B 改 contract 與 `approval.go`；C 只改 `scenarioName`。 |
| D-068 | CB-42 (a) | 決策紀錄：comment、決定者與時間 | **APPROVED（2026-10-25，H-021，依建議：採 A）；2026-10-20 T-026 提出。** 問題：Approve 與 Reject 都有 `comment`（0–2000 字元），但資料庫沒有存放處（D-041 只涵蓋 publish 的 comment），Proposal resource 也不顯示誰、何時決定。選項 A（建議，已實作）：(a) migration 0013 新增 `ai_proposals.decision_comment`：1–2000 字元，空字串或沒有即 null；只在 approved 或 rejected 上出現，由決策本身寫入，之後不可改（0001）；worker 對這個欄位沒有 UPDATE 權限（它與其他欄位一樣以表級 SELECT 讀到它，例如 generator 的 `store.Lock`；review L5）；edit 批准的 comment 同時是新 version 的 `comment`（與 publish 保存 Publish.comment 相同，D-041）；comment 是使用者文字，不放 audit。(b) conflicted 也記錄 `decided_by`（發現 base 已變的那位批准者）與 `decided_at`，沒有 comment（0013 的 CHECK）。(c) `proposal.schema.json` 與 getProposal 在 approved、rejected、conflicted 顯示 `decided_by`、`decided_at`，approved 與 rejected 有 comment 時顯示 `decision_comment`；其他狀態都沒有這三個成員。選項 B：不保存 comment，從 Approve、Reject 拿掉 `comment`（contract 變更）。選項 C：comment 放 audit metadata（違反 D-041「使用者資料不放 audit」）。不採用 A 時：B 改 `http.schema.json`、`proposal.schema.json`、TS，並以新 migration 撤掉欄位；C 改 audit 的寫入與 SECURITY §9。 |
| D-069 | CB-42 (b)–(e) | 批准與拒絕的檢查順序、錯誤碼、revalidation 與資料庫保證 | **APPROVED（2026-10-25，H-021，依建議：照列；(b) risk 改變時 409、不批准（決策頁 Q3），revalidation 失敗的 proposal 維持 ready（B1）；`publishVersion`／`createSimulation` 是否改回 503 不在這次批准範圍）；2026-10-20 T-026 提出。** 已批准的決策沒有涵蓋的選擇，實作於 `internal/proposals/approval` 與 migration 0013。(a) **順序與錯誤碼**：request 本身的檢查先於 role，與其他 route 相同：path 的 ID（400）、media type（415）、body（400／422）、`Idempotency-Key` header（400）（review I4）→ role（403）→ Idempotency-Key：fingerprint 為 proposal ID 與 normalized parsed body，空的 comment 即沒有 comment（D-068），所以 `{}` 與 `{"comment":""}` 是同一個 request，schema 沒有的 member 不會被略過（review I3）；key 的 scope（organization、actor、operation、key）不含 proposal，所以同一 key 與同一 body 用在另一個 proposal（即使同一 model、同一 base）是 409 `IDEMPOTENCY_KEY_CONFLICT`，該 proposal 不變（review M1）；replay 前仍先查 role（D-011）；replay 的錯誤回答（決策只存 conflict 的 409）除了 `error.request_id` 改為這次 request 的 ID 之外 byte 相同：每個錯誤回答的 `request_id` 都等於 response 的 `X-Request-ID`。既有的 idempotent endpoint（T-009 projects、T-013 models、T-023 runs、T-025 proposals）只存成功的回答，它們的錯誤 envelope 一律帶這次的 request（`internal/api` 測試的 `code()` 也這樣要求），本項沿用；第一次 request 的 ID 留在 conflict 的 audit event（review L1）→ proposal（不存在或屬於其他 organization：404）→ 依序鎖 model row、draft、proposal（ARCHITECTURE §4 的順序；reject 也照此順序，所以同一 proposal 的批准與拒絕輪流進行）→ 狀態：approved、rejected、conflicted 一律 409 `PROPOSAL_ALREADY_DECIDED`（D-030 只寫 approved／rejected；conflicted 也算，因為它是終態，0001 也為它記 `decided_at`）；generating、needs_input（批准時）、invalid、failed 為 409 `LIFECYCLE_CONFLICT` → base 已不是 model 的 latest：proposal 轉為 conflicted（記錄這位批准者）、audit `proposal.conflict`，回 409 `MODEL_VERSION_CONFLICT`（`/expected_base_version_id`）；這是唯一會寫入的拒絕，409 與狀態轉換、audit 同一 transaction commit，並存為該 key 的回應（同一 key 重送得到同一個 409）→ `expected_base_version_id` 不是 proposal 的 base：409 `MODEL_VERSION_CONFLICT`（`/expected_base_version_id`），什麼都不變（client 看錯了 base，proposal 本身仍可批准）。(b) **Revalidation**：server 以 `internal/ai/review.Review` 對存下的內容（summary、assumptions、canonical changes、expected effects、confidence、provider claim）再 review 一次（mode 的 patch profile 與 allowlist、完整 candidate validation、該 proposal 的 ID pool、domain pack、expected effects 的數字、server risk），再以 base 的整份 manifest 跑 dataset binding stage 並重讀 snapshot；存下的 validation、risk 與 hash 都不採信。Review 或 binding stage 有 error：422 `MODEL_VALIDATION_FAILED`（issue 經 `review.Clean`），proposal 維持 ready（state machine 沒有 ready → invalid；人仍可 reject）。重算的 server risk 或 effective risk 與存下的不同：409 `LIFECYCLE_CONFLICT`（`/risk_level` 或 `/server_risk_level`），因為人審閱的是另一個 risk，需要新的 proposal；其他選項：B 只在變高時拒絕，C 照新的 risk 批准並記錄。重算的 canonical patch 或 patch hash 與存下的不同，或 proposal 的 `dataset_bindings` 不是 base 的整份 manifest：server 錯誤（500），不批准，什麼都不變（review L2 的測試）。Base 綁 dataset 而 API 沒有 object storage：503 `SERVICE_UNAVAILABLE`。Binding stage 讀不到或無法驗證 snapshot（infrastructure 錯誤，不是資料的 issue）：503 `SERVICE_UNAVAILABLE`，`details[0].code` 為 `DATASET_STORAGE_FAILED`，與 generator 的同類失敗一致（D-061 (c)），原因只寫入 log；request 本身被取消或逾時則照一般的 context 錯誤處理（review I2）。`publishVersion` 與 `createSimulation` 對同一類錯誤原本回 500；owner 於 H-022（決策頁 Q4）決定一併改為 503 `SERVICE_UNAVAILABLE`（`details[0].code` 為 `DATASET_STORAGE_FAILED`），由 T-054 實作；批准時的 draft 同步屬 approveProposal 自己的 binding stage，在本項 (b) 的範圍內；patchDraft 與 resetDraft 的延伸超出 H-022 批准的兩個 endpoint（T-054 review M1：coordinator 原以為是例行的一致性修正而自行決定，這是錯的），另立 D-087，由 owner 於 H-023 批准（`internal/models` 的 `bindingFailure` 與 `refused`、`internal/runs` 的 `bindingFailure` 與 `Refused`、`internal/proposals/approval` 的 `storageCause`；API_DATABASE §4、§6）。**Revalidation 失敗後的 proposal**（review I1）：被拒的批准不寫入任何東西，所以 422 或 risk 改變的 409 之後，proposal 仍是 ready，但只要規則或 snapshot 不變，就永遠無法批准，只能 reject 後重新提出。選項 B1（建議，已實作）：維持現狀；拒絕不改狀態，422／409 說明原因，UI（T-031）應提示 reject 並重新提出；可撤回，不動 state machine。B2：第一次失敗的批准把 proposal 轉為 conflicted（0001 允許 ready → conflicted），但 conflicted 的意思會從「base 已變」擴大，需要記錄原因的新欄位（新 migration）與 API 的變更。B3：轉為 invalid，需要新 migration 放寬 0001 的 transition（ready → invalid 目前不允許）與 0012 對 invalid 內容的 CHECK。B2、B3 都會讓一次被拒的批准寫入狀態，不是 local 的修改，所以沒有實作。(c) **Reject**：ready 或 needs_input → rejected（決定者、時間、comment）、audit `proposal.reject`（metadata 含原本的狀態），回 200 與 getProposal 相同的 Proposal；不碰 model、draft、version、scenario。(d) **Audit metadata**（都不含 comment 或 provider 的文字）：`proposal.approve` 有 mode、model、base、patch_hash、server risk、effective risk，edit 另有 new_version_id、version、draft_out_of_date，scenario 另有 scenario_id；`proposal.reject` 有 mode、model、base、from；`proposal.conflict` 有 mode、model、base、latest。(e) **資料庫保證（0013）**：以某 proposal 為來源的 version 或 scenario，在 commit 時必須是那個 approved proposal 自己的 effect：version 只屬於 edit proposal，parent 是 base、number 是 base 的加一（與 0001 的 unique version number 合起來，base 已不是 latest 的批准無法 commit）；scenario 只屬於 scenario proposal，base、patch、patch hash 都與 proposal 相同。(f) 批准不呼叫 provider，所以不受 AI 是否開啟影響；批准者可以是建立 proposal 的人（PRD §2：原報告不要求四眼）。不採用時：(a)–(d)、(f) 改 `internal/proposals/approval`（fingerprint、replay 與 503 也在這裡，contract 的說明在 approveProposal／rejectProposal）；(e) 以新 migration 調整 0013 的 trigger。 |

### T-017 的提案：canonical mapping 與全量 validation（2026-10-20 提出，PROPOSED）

T-017 實作時需要、既有 decision 沒有寫到的契約細節。三項都是 **PROPOSED**：approved_by／approved_at 為 null，由 owner 決定，agent 不自行批准。各項的建議都已實作，因為只涉及 T-017 新增的 schema、`internal/import/mapping`、`internal/import/validation`、`internal/import/validator`、`internal/import/upload/mapping.go` 與 migration 0014，可以回退；不採用時要改的地方列在各項最後。契約上的缺口（ErrorCode 目錄、T-018 的交接、孤兒 object、實作參數）見 CB-43。

| ID | 解決 | 主題 | 提案 |
|---|---|---|---|
| D-070 | T-017（D-043 (c)、D-059、CB-28 的一部分） | 人工 mapping 的詞彙、transform 與低信心建議的確認 | **APPROVED（2026-10-25，H-021，依建議：(a) 採 A；(b) 照列，CB-28 另立 D-073（決策頁 Q4）；(c) 照列；(d) 採 A，逐欄確認（Q1），不另做人工清除 suggestion 的路徑（Q5）；(e) 照列）；2026-10-20 T-017 提出。** (a) Target field 詞彙：人工 mapping 與 AI suggestion 共用 `import-mapping.schema.json#/$defs/TargetField`，即 canonical-event 的 13 個欄位（case_id、event_type、timestamp、end_timestamp、stage、duration_ms、resource_id、outcome、shift、product、deployment_id、is_failure、is_rework）加 metadata。D-059 的 end_timestamp、shift、product 在 T-017 就加入：station log 沒有 end_timestamp 就無法 mapping，也就無法 validate。選項 A（建議，已實作）：現在加入；B：等 T-052，在那之前 station log 無法匯入。Pack 層的資料品質規則（多產品時 product 必填、event_type／outcome 的詞彙）仍屬 T-052。(b) Transform 由 target field 決定，沒有參數：文字欄位與 metadata 為 identity，timestamp、end_timestamp 為 rfc3339，duration_ms 為 integer_ms，is_failure、is_rework 為 boolean_literal；不匯入的欄（`target_field: null`）為 identity；schema 與 server 都檢查。常數、單位換算與無時區時間的 transform（CB-28）不在 T-017 範圍：沒有 offset 的時間與不是毫秒的 duration 都是 invalid row，CSV 沒有 event_type 欄時無法 mapping（D-059 指向 CB-28）。CB-28 另立 decision（owner 於 H-021 選擇另立，見 D-073；原建議：T-052 前依 D-059 的詞彙定義有列舉參數的 `constant` transform）。(c) 一個 source column 至多一條；metadata 以外的 field 只能由一欄填入；case_id、event_type、timestamp 必須有欄；至多 64 欄進 metadata，以欄名為 key、照 mapping 的順序。D-059 的「operator ID 不匯入」：詞彙中沒有 operator 欄位，但人可以把它放進 metadata，T-017 無從辨認（CB-43 (f)）。(d) 需要人確認的建議（AI_SPEC §8：confidence < 0.80 或 target_field = null 必須確認）：哪些條目需要人確認由 server 決定——confidence 低於 0.80、target 為 null（不匯入該欄），或 AI 自己標了 `requires_confirmation`；儲存的旗標只是 AI 的主張，只能增加、不能免除確認（review 第 1 輪 M1、M2）。import 有 ready 的 AI suggestion 時，mapping 照這種條目採用的每一欄（target 與 transform 都相同；null 條目則是 mapping 也不匯入該欄，不論是寫成 null 的條目或根本沒列）都要由存檔的人在 `confirmed_low_confidence` 列出欄名，否則 422 `MAPPING_CONFIRMATION_REQUIRED`（path 指向該條；沒列的欄指向 `/mapping/mappings`）；列出的名稱不是這種欄時 422 `MAPPING_CONFIRMATION_UNKNOWN`；人改成其他 target（或匯入了 AI 建議不匯入的欄）不需確認；suggestion 中不是此 import 欄位的條目不需要任何人確認，也不能列出（review 第 2 輪 N1：否則超過 256 個這種條目時 mapping 永遠無法存檔），T-027 寫入 suggestion 時應先丟掉或拒絕這種條目；`confirmed: true` 仍是對整份 mapping 的確認。儲存的 suggestion 由 worker（AI 那一側）寫入，DB 不檢查形狀，所以每次存檔讀它時都先以 `ai-mapping.schema.json` 驗證；不符（例如 confidence 0.1 卻沒標旗標）時 fail closed：500 `INTERNAL_ERROR`，`details[0].code` 為 `MAPPING_SUGGESTION_INVALID`，什麼都不寫；T-017 沒有清掉這種 suggestion 的路徑，所以在換掉之前每次存檔都是 500（review 第 2 輪 N2），T-027 寫入時須先以 schema 驗證、不合就標 failed（T-027 的 gate），不另做人工解除的路徑（H-021，依建議）。AI 沒有寫 mapping 的路徑：worker 沒有 mapping 欄位的 grant，0014 也拒絕；schema 拒絕帶 confidence、evidence 或 requires_confirmation 的 mapping。選項 A（建議，已實作）：逐欄確認；B 只要 `confirmed: true`（低信心建議可能被整批接受）；C 低信心條目一律要人改寫（多餘的操作）。T-027 尚未產生 suggestion，所以 (d) 目前只有 Go 單元測試與 DB-backed test（以 owner 寫入的 suggestion）驗證。(e) 存檔被拒的 code：`MAPPING_UNKNOWN_COLUMN`、`MAPPING_DUPLICATE_COLUMN`、`MAPPING_UNKNOWN_TARGET`、`MAPPING_DUPLICATE_TARGET`、`MAPPING_REQUIRED_TARGET_MISSING`、`MAPPING_TRANSFORM_INVALID`、`MAPPING_METADATA_LIMIT`、`MAPPING_CONFIRMATION_REQUIRED`、`MAPPING_CONFIRMATION_UNKNOWN`，以及 JSON model 的 `MAPPING_NOT_APPLICABLE`（D-071 (f)），放在 422 `REQUEST_VALIDATION_FAILED` 的 `details[].code`；path 是 body 中的 JSON Pointer，message 是固定句，不引用欄名或值。不採用時：(a) 從 `TargetField` 移除三欄，並改 fixtures、TS 型別與 `internal/import/mapping`；(b)(c) 改 schema 與 `mapping.Check`；(d) 改 `mapping.confirmations`、`SuggestedEntry.NeedsPerson`、`internal/import/upload` 的 `suggestion` 與 SaveMapping schema。gate：T-017 驗收前；(d) 另為 T-027 與 mapping UI（T-031）前。 |
| D-071 | T-017（CB-18、CB-29） | Mapping revision 與 validation 的生命週期 | **APPROVED（2026-10-25，H-021，依建議：照列，(e) 採 A）；2026-10-20 T-017 提出。** (a) Compare-and-set：`expected_mapping_revision` 必須等於 import 目前的 `mapping_revision`，否則 409 `LIFECYCLE_CONFLICT`，`details[0]` 的 code 為 `MAPPING_REVISION_CONFLICT`、path 為 `/expected_mapping_revision`；不覆寫、不合併、不自動 rebase，比目前 revision 大也一樣。AWAITING_MAPPING、VALIDATING、VALIDATED 時可以存檔；UPLOADED、PROFILING 為 409 `IMPORT_NOT_PROFILED`，FAILED、COMMITTED 為 409 `IMPORT_FINAL`。saveImportMapping 改為需要 `Idempotency-Key`（CB-18）：fingerprint 是 import ID 與 body 的 canonical JSON，replay 仍要求 editor 以上。(b) 新增狀態 `VALIDATING`（CB-29）。存檔在同一 transaction 中：`mapping_revision` 加 1、狀態改為 VALIDATING、清空 validation、invalid_rows、canonical preview 與 dataset、error report；排入該 revision 的 `import_validate` job（payload 為 import_id、mapping_revision、request_id；每個 revision 一個，partial unique index）；取消其他 revision 仍在 queued 的 job；寫 `import.map` audit（只有數量）；回 202 與 import。(c) Stale-job fence：job 只在 import 仍是 VALIDATING 且 revision 相同時寫入結果，否則以 completed 結束、不寫 import、audit 的 outcome 為 `stale`。0014 也強制：離開 VALIDATING（到 VALIDATED 或 AWAITING_MAPPING）時，目前 revision 的 job 必須在同一 transaction 中 completed（deferred constraint trigger），其他 revision 的 job 結束不影響 import；判斷看 job 與 import 的狀態，不看 `pg_trigger_depth()`。(d) 結果：每列都合法時為 VALIDATED，帶 `validation.valid=true`、canonical preview 與 canonical dataset（(e)）；有不合法的列時為 AWAITING_MAPPING，帶 `validation.valid=false`、issues、invalid_rows 與 error report，可以重新 mapping；檔案已不是 profile 的那份時為 FAILED（大小或 SHA-256 不符 `UPLOAD_CORRUPT`、不見 `UPLOAD_MISSING`、兩者相符但列數或欄位不同 `UPLOAD_CHANGED`）。Object storage 失敗會重試，attempt 用完後 import 跟著 job 變 FAILED（`follow_import_validate_job`）；operator 取消目前 revision 的 job 也一樣（`JOB_CANCELLED`）。FAILED 的 `error.request_id` 是該 revision 存檔的 request；`error.code` 沿用 CB-39(a) 的對應（`UPLOAD_CHANGED` 為 `INTERNAL_ERROR`）。VALIDATED 不能回到 AWAITING_MAPPING，只能經由新的 revision。(e) Canonical dataset：每列都合法時，validation job 再讀一次 upload（同樣核對 SHA-256 與大小），依列的順序把 canonical event 寫成 compact JSON array，存在 `tenant/<org>/imports/<import>/canonical-r<rev>-a<attempt>.json`，row 記下 key、SHA-256 與大小（`canonical_*`：三者全有或全無，只在 VALIDATED，key 在本 import 的前綴下，不是 upload 或 report 的 key）。T-018 commit 時凍結的就是這份 bytes（D-006、D-019），commit 的 transaction 不必重新 mapping。選項 A（建議，已實作）：由 validation 產生；B：commit 時才 mapping（commit 變成長時間的工作，而且 validation 之後檔案若改變，得到的 dataset 會與驗證的不同）。Job 在第二次讀取之前，於 lease 下再確認 import 仍是 VALIDATING 且 revision 相同，否則停止、不寫 dataset（review 第 1 輪 L5）；只有在 dataset 寫完之後才被新的存檔蓋過的 job，寫入的 object 成為孤兒（D-044 的 maintenance 尚未實作）。(f) JSON model import 沒有欄位可 mapping：saveImportMapping 回 422 `MAPPING_NOT_APPLICABLE`（`/mapping`）；它的 validation 與身分綁定屬 T-018（API_DATABASE §5「JSON model 無欄位 mapping 時 revision=0 仍需 validation」由 T-018 實作）。(g) COMMITTED 只能從 VALIDATED 來，0014 對所有 role 強制（review 第 1 輪 L1：先前 API 在 VALIDATING 或 AWAITING_MAPPING 時只要寫上 dataset version 與 proposal 就能直接 COMMITTED，繞過 D-009）；commit 還需要什麼（凍結 dataset、proposal、job）由 T-018 的 migration 定義。存檔的 transaction 結束時，仍是 VALIDATING 的 import 必須有目前 revision 的 queued 或 running job（review 第 1 輪 L2：先前在存檔之前插入並取消的 job 也算數，import 會停在 VALIDATING）。不採用時：(a) 改 `internal/import/upload/mapping.go` 與 OpenAPI；(b)(c)(d) 改 0014、validator 與 `import.schema.json`；(e) 選 B 時移除 `canonical_*` 欄位與 validator 的第二次讀取；(f) 改為接受空的 mapping。gate：T-017 驗收前；(e)(g) 另為 T-018 前。 |
| D-072 | T-017（D-059、TEST-013） | 每一列的 validation 規則、回報與上限 | **APPROVED（2026-10-25，H-021，依建議：採 A，保留原 offset、拒絕 `-00:00`、報表不帶值；(b) 為 invalid row（決策頁 Q2）；(g) 上限待 T-037）；2026-10-20 T-017 提出。** (a) 值：identity 的值照原文，不去空白，長度以 Unicode code point 計：case_id、resource_id、deployment_id 至多 256，event_type、stage、outcome、product 至多 128，shift 至多 64（canonical-event schema），超過為 `MAPPING_VALUE_TOO_LONG`。rfc3339 沿用 profile 的 timestamp 規則（必須有 offset），值照原文保留 offset，不轉成 UTC；offset 是 RFC 3339 的 00:00 到 23:59，與 canonical-event schema 的 date-time 相同（review 第 1 輪 M3：先前 profile 的 pattern 與 Go 的 `time.Parse` 都接受 `+24:00`、`+05:60`，VALIDATED 的 dataset 會有 schema 拒絕的 event）；RFC 3339 §4.3 的 `-00:00` 表示「offset 不明」，**建議**拒絕（不假設任何時區；已實作，profile 也推斷為 string，review 第 1 輪 L6），選項 B 為照 schema 接受、視為 UTC；integer_ms 為 0 到 2^53−1 的十進位整數（不接受前導 0、正負號或小數）；boolean_literal 為 ASCII 的 true／false（不分大小寫），JSON 的 true／false 也接受；JSON 數字對 identity 與 integer_ms 視為其原文；JSON object 與 array 只能進 metadata，照原樣。不合法的值為 `MAPPING_VALUE_INVALID`。空值（空字串、JSON null、缺少的 member）：必要欄位為 `MAPPING_REQUIRED_VALUE_MISSING`，其他欄位不輸出該成員，也不放進 metadata。(b) 列的規則（D-059）：同時有 timestamp 與 end_timestamp 時，end 不得早於 start（`EVENT_END_BEFORE_START`，path 指向 end 欄）；再有 duration_ms 時，它與 end − start 的差必須小於 1 ms（`EVENT_DURATION_INCONSISTENT`，path 指向 duration 欄），以整數精確計算，不受 `time.Duration` 範圍限制。兩條規則的後果（H-021 採 A）（review 第 1 輪 I2：TWIN_IR_SPEC §7 把 duration 不一致稱為 domain inference 的 data-quality issue，D-059 與 canonical-event schema 的註解也稱它們為 data-quality 規則）：選項 A（建議，已實作）：invalid row——這種列無法形成一致的 event，all-or-nothing（AI_SPEC §8）之下整個 import 回到 AWAITING_MAPPING，人可以改 mapping（例如不匯入 duration）或修正檔案；T-052 的 pack 品質規則（產品、詞彙）仍是 warning。選項 B：validation 接受這些列，改為 warning（issue 的 severity 為 warning，import 仍可 VALIDATED），由 T-027／T-052 的 inference 決定如何處理；後果是凍結的 dataset 中會有 end 早於 start、或 duration 與時間矛盾的 event，下游每個使用者都要自己處理。選 B 時改 `mapping.Plan.Apply` 的兩條規則與其 tests，並讓 validation 的 issues 可帶 warning。(c) 每列的 metadata 以 compact JSON 計至多 16 KiB、巢狀 8 層，否則 `EVENT_METADATA_TOO_LARGE`。(d) CSV 欄位數與 header 不同的列為 `CSV_FIELD_COUNT`（與 T-016 的 profile 相同），path 指向整列（`/<列>`），報表的 column 是該列缺少的第一欄（欄位過多時空白）。(e) 回報：issue 的 path 是把檔案視為列陣列的 `/<列>/<欄名>`（欄名依 JSON Pointer escape），message 是固定句「<field>: …」，不引用值；`validation.issues` 至多 100 個；error report 沿用 T-016 的格式，至多 10,000 列，`value` 欄一律空白（validation 的錯誤不回顯 cell，以免帶出 secret 或公式；欄名仍經 `report.Safe`）；canonical preview 是前 100 列（與 raw preview 對齊），不合法的列為 null，字串截斷規則與 1 MiB 的上限與 raw preview 相同。(f) Canonical dataset 以 compact JSON 計至多 256 MiB；超過時 validation 不通過，issue 為 `DATASET_TOO_LARGE`，建議少放幾欄進 metadata。每個 event 在可能成為 dataset 的一部分之前，最後都以 `canonical-event.schema.json` 驗證：mapping 的規則本應與 schema 相同，不一致是 server 的缺陷，該列為 invalid row（`EVENT_NOT_CANONICAL`，path 指向整列），Write 拒絕整個檔案（fail closed，review 第 1 輪 M3）。(g) 上限都是實作參數，待 benchmark（T-037）。選項 A（建議，已實作）如上；B timestamp 一律轉成 UTC（dataset 失去原本的 offset，而班別推斷需要它）；C 報表帶出 value（比較好除錯，但可能回顯 secret 或公式）。不採用時改 `internal/import/mapping`、`internal/import/validation`、`internal/import/profile` 的 timestamp 規則（`-00:00`）與它們的 tests。gate：T-017 驗收前。 |
| D-073 | CB-28（D-070 (b)、H-021 Q4） | 常數與單位換算的 transform | **PROPOSED（2026-10-25，owner 於 H-021 決定另立；草案，T-052 前定案）。** 問題：D-009（APPROVED）只允許 identity／rfc3339／integer_ms／boolean_literal 且沒有參數（D-070 (b)），所以沒有 event_type 欄的 station log（每站一個檔）無法 mapping，以秒或分鐘記錄的 duration 與沒有 offset 的時間都是 invalid row。草案（需修改 D-009 的允許清單，須 owner 批准）：(a) `constant`：沒有 source column 的 mapping 條目，對整份檔填入一個值；只用於 event_type、outcome、stage、shift、product，值須是 DomainPack 詞彙中的值（D-059；pack 的詞彙檢查屬 T-052），case_id 與時間不能是常數；一個 field 仍只由一條填入（D-070 (c)）。(b) duration 的單位換算：`seconds_to_ms`、`minutes_to_ms`，十進位、整數精確計算，結果須為 0..2^53−1 的整數毫秒，否則 invalid row。(c) 沒有 offset 的時間：建議仍拒絕（D-072「不假設時區」）；選項是 import 層級由人宣告一個 offset（例如 +08:00）後套用，絕不使用 server 的時區。選項 A（建議）：(a)(b)，(c) 維持拒絕；B：(a)(b)(c) 全做；C：都不做，CB-28 維持現狀，資料須先在外部整理。影響：`import-mapping.schema.json` 與 `ai-mapping.schema.json` 的 transform、`mapping.Check`／`Compile`、AI suggestion 的 prompt 與 eval、TS 型別。gate：T-052 前（或第一次以真實 station log 展示匯入前，以先到者為準）。 |

### T-048 的提案：resource 故障與 lognormal／Weibull 的實作細節（2026-10-25 提出，同日 APPROVED，H-022）

T-048 依 D-047、D-048、D-050、D-051、D-056、D-057（APPROVED，H-018）與 D-063（APPROVED，H-019）實作時，批准文字沒有寫到、但會影響行為、observation 或數值的細節。兩項於 2026-10-25 由 owner 依建議批准（H-022）；在那之前是 PROPOSED，agent 沒有批准。建議都已實作，因為只動 T-048 自己的 kernel、validator 規則與手推 golden，可以回退；不採用時要改的地方列在各項最後。Gate：T-048 驗收前；T-049 延伸 B2 與故障時鐘（只在上班時前進）時沿用。

| ID | 解決 | 主題 | 提案 |
|---|---|---|---|
| D-074 | T-048 實作的解讀 | 等待中的故障由誰滿足、釋放與喚醒的順序、B2 的檢查時點與 TTRmax、長度 0 的停機、故障成員的計數、observation 與 stream 的細節 | **APPROVED（2026-10-25，H-022，依建議：(a) 採 B，變成可用的 unit 先依故障時刻 FIFO 給等待中的故障（決策頁 Q1），(b)–(f) 照列）；2026-10-25 T-048 提出。** (a) **變成可用的 unit 先給等待中的故障**：D-051／ADR-026 §(A) 寫「全部使用中時 finish-first：等下一個被釋放的 unit 停機（等待中的故障依時刻 FIFO）」與「每個 unit 一組獨立的 alternating renewal」，沒有說修復好的 unit 算不算「被釋放的 unit」。選項 B（建議；自 review 第 1 輪起實作）：job 釋放的 unit 與修復好的 unit 都先依故障時刻 FIFO 給等待中的故障（立即停機、抽 TTR），沒有故障在等時才使 capacity 增加、喚醒 held front；所以等待中的故障永遠不會與閒置的 unit 並存。手推 golden G-F9（reviewer 的 N8：capacity 2、TTF 10、TTR 5，一件從 1 起服務 100 分鐘的 job，horizon 60）：時鐘 1 在 10 等待、15 時拿到修復好的 unit，之後每 15 分鐘兩個時鐘輪流停機：8 次停機、down 35 分、7 次完整修復、available 85 分、idle 26 分；G-F3 依 B 重推：時鐘 1 在 12 拿到修復好的 unit，s4/1 在 27 開始（A 為 26）、s5/1 仍在 28。選項 A（第一版的建議與實作）：只有 job 釋放的 unit 滿足等待中的故障，修復好的 unit 回到可用、喚醒 held front；後果：修復留下閒置的 unit 時，等待中的故障繼續等、它的時鐘停住，其他時鐘之後的故障卻先看到閒置的 unit 而停機。N8 中時鐘 1 從 10 等到 horizon，時鐘 0 在 10、25、40、55 停機：只有 4 次停機、down 20 分、idle 41 分。Reviewer 的 N9（burn-in oven：capacity 3、TTF 指數 mean 120、TTR 指數 mean 20、lognormal 的 service，300 replications，同一組亂數）：每個 replication 的停機次數 A 245.4、B 254.9（z −9.5），down_fraction A 0.1263、B 0.1313（z −6.9）；各時鐘獨立 renewal 時為 TTR／(TTF＋TTR) ≈ 0.143，A 偏離較多（B 也低於 0.143：等待中的時鐘仍要等一個 unit 變成可用，finish-first 本來就會延後故障）。第一版寫的 B 的後果（「G-F3 的 s5 要等到 33」）是錯的：B 下 s5/1 仍在 28 開始。(b) **釋放的順序**：bundle 釋放時，每個 resource 先回報新的 in_use（ObsResource），被釋放的 unit 依故障觸發的順序（FIFO，每個 unit 一個）立即停機、抽 TTR、排 REPAIR（ObsResourceDown），之後才喚醒該 resource FIFO 最前面的 held candidate（它在 dispatch 中再被判斷一次，放不下就再 held）；golden G-T4 的 tick 8 固定這個順序。只影響同一事件內的 observation 順序。(c) **B2 的檢查時點、TTRmax 與回報的 request**：D-050 B2 的狀態是「held candidate 的 bundle 中每個 resource 都沒有 unit 在使用中」，批准文字沒有說何時檢查。選項 A（建議，已實作）：在每個 tick 的最後一個事件之後檢查（下一個事件的 tick 較大，或沒有事件）；since＝第一次在 tick 結束時處於這個狀態的 tick，tick 中被喚醒又 held 不重設；deadline＝since＋TTRmax，tick ≥ deadline 時回 `ErrStalled`；TTRmax 為 bundle 中有故障的 resource 的 TTR 上界之最大值（constant 的值、triangular 的 max、empirical 的最大樣本），任一個為 exponential、lognormal、Weibull（沒有上界）時不檢查（D-050「TTRmax 無上界時不檢查」）；同時有幾個時回報依 queue 順序最早的 request。選項 B：每個事件之後檢查；後果：golden G-S2 中修復在 deadline 那個 tick 稍後才讓 request 進入，會被誤報為 stall。選項 C：只在 dispatch 結束時檢查；後果：沒有 dispatch 的 tick 不檢查，stall 晚一點才報。(d) **長度 0 的停機**：TTR 為 0（constant 0 或 0 tick 的樣本）的停機在同一 tick 修復（priority 15、序號較後）。選項 A（建議，已實作）：照 observation 計數：`breakdowns_started` 加 1，並算一次長度 0 的完整修復（golden G-T4）；修復在 dispatch 之後才到時再排一次 dispatch。選項 B：不計長度 0 的停機（metrics 要特別排除）。(e) **故障成員的計數**：`breakdowns_started` 是 [W, H] 內開始的停機數；完整修復是停機開始 ≥ W 且修復 ≤ H（與 D-064 的完整阻塞相同）；沒有班表時 S＝C，`available_ticks`＝C·T − `down_ticks`，`idle_ticks`＝`available_ticks` − `in_use_ticks`（每個 tick U＋D ≤ C）；沒有有限 buffer 時 `resource_states` 只列有故障的 resource（D-058「沒有任何功能的 resource 不出現」，golden G-F8），有有限 buffer 時每個 resource 都列、有故障的另加這一組。選項 A（建議，已實作）如上；選項 B：完整修復只看修復在 [W, H] 內（跨 W 的停機也算），會與 `completed_repair_ticks` ≤ `down_ticks` 的關係衝突。(f) **Observation、stream 與 t=0**：observation 新增 ObsResourceDown、ObsResourceUp、ObsBreakdownPending（編號接在 ObsUnblocked 之後，At＝故障時鐘的 unit index，Value 為停機或等待中的數量）；故障與修復不寫進 trace；unit u 的 stream 為 (resource ID, `ttf/<u>`)、(resource ID, `ttr/<u>`)，首次使用時建立；TTR 在停機時抽、下一個 TTF 在修復時抽；t=0 時 sources 排第一個 arrival 之後，有故障的 resource 依 ID、unit 依 index 排 FAILURE；晚於 horizon 的 FAILURE／REPAIR 不排程也不取 sequence（抽樣已消耗），所以 constant TTF 晚於 horizon 的模型與沒有故障的模型逐筆相同（D-047 的中性設定，`TestNeutralBreakdownsChangeNothing`）；REPAIR 只喚醒自己 resource 的 held front，且只在有喚醒時排 dispatch；D-050 B1 仍以全部 capacity C 判斷 release-bound（放得下 C、放不下 C − D 的 held candidate 是 capacity-bound，不是 deadlock，golden G-K1）。這些照 D-048、D-050、D-051 的文字直接推得，列出供 owner 確認。不採用時：(a) A 改 `kernel.repair`（拿掉修復時先給等待中的故障的分支）、只看 observation 的檢查器、`scripts/simpy_reference.py` 的 clock()、G-F3、G-F3b、G-F9、G-F9R10、G-F10 的期望（後三個為 review 第 2 輪新增，A 下都會變），以及兩個 0.3.0 鎖中的這五個與 B-S2、B-S4；(b) 改 `kernel.release` 與 G-T4 的 tick 8；(c) B 改 `kernel.after` 與 G-S2，C 改為只在 `dispatch` 結束時呼叫 `stall`；TTRmax 的定義改 `sampler.bound` 與 `TestRepairBound`；(d) B 改 `internal/results/metrics/aggregate.go` 的修復計數與 G-T4 的 Result；(e) B 改完整修復的條件與各 golden 的 `repairs_completed`；(f) 改 kernel 的 observation 與 stream、`breakdown-goldens.json`、`engine-vectors.json` 與兩個 0.3.0 的鎖定檔。 |
| D-075 | T-048（D-056 的候選範圍與數值方法） | lognormal 與 Weibull 的參數範圍、溢位分析與 numerical profile | **APPROVED（2026-10-25，H-022，依建議：採 A）；2026-10-25 T-048 提出。** D-056（APPROVED）定了公式（反函數法、每個樣本一次 draw、固定的 exp 與 AS241），ADR-026 列 cv ≤ 10、0.1 ≤ shape ≤ 20 為「候選範圍，T-048 定案」。(a) **範圍**：選項 A（建議，已實作）：保留候選範圍：lognormal 0 < cv ≤ 10、Weibull 0.1 ≤ shape ≤ 20，超出為 `DISTRIBUTION_INVALID`（path 為該 distribution；validator 與 engine 的 Compile 都檢查），mean、scale 解碼後下溢為 0 也是；mean 與 scale 是時間參數，照 D-029 換算後超過 365 天為 `LIMIT_EXCEEDED`（D-063 (c)），cv、shape 沒有單位。溢位分析：SplitMix64 的 U 在 [2⁻⁵³, 1 − 2⁻⁵³]，所以 |Φ⁻¹(U)| ≤ 8.2095；cv ≤ 10 時 σ ≤ √ln 101 ≈ 2.148，exp 的引數 μ＋σz = ln(mean) − σ²/2＋σz，在這個範圍內最大為 ln(mean) − 2.148²/2＋2.148·8.2095 ≈ ln(mean)＋15.33（−σ²/2＋8.2095σ 在 σ ≤ 2.148 時遞增；第一版寫的 ln(mean)＋17.7 是略去 −σ²/2 的寬鬆上界，review 第 1 輪的 Info），不會溢位；shape ≥ 0.1 時 (−ln(1−U))^(1/shape) ≤ 36.74¹⁰ ≈ 4.4×10¹⁵ 倍 scale，超過 365 天的樣本照 D-038 (b) 記為 365 天＋1 tick（事件不處理）；shape ≤ 20 時下界 (2⁻⁵³)^(1/20) ≈ 0.16 倍 scale。選項 B：放寬（例如 cv ≤ 100、shape ≥ 0.01）：尾端更常超過 365 天，模型多半是輸入錯誤；選項 C：收緊到常見的工業範圍（例如 cv ≤ 3、0.5 ≤ shape ≤ 10）：會拒絕少數真實的長尾修復時間。(b) **數值方法**：exp 為 Go `math.exp` 的 pure-Go 演算法移植，每個乘積以 `float64()` 捨入、以 `math.Ldexp` 縮放（不呼叫平台的 `math.Exp`，它在 amd64、arm64 等是 assembly）；Φ⁻¹ 為 AS241（PPND16）的雙精度版本，48 個常數以 bit pattern 固定；lognormal x = exp(μ＋σ·Φ⁻¹(U))，σ² = ln(1＋cv²)、μ = ln(mean) − σ²/2 在 compile 時各算一次（乘積先捨入；arm64、riscv64、loong64 曾把 σ²/2 融合進減法，`make backend-numeric` 抓到後改為明確捨入）；Weibull x = scale·exp(ln(−ln(1−U))/shape)；兩者與 exponential 一樣至少 1 tick（D-038 (a)）。`scripts/engine_reference.py` 逐運算移植，`engine-vectors.json` 固定 exp 與 ndtri 的常數、邊界與 10⁶ 個輸入的 SHA-256、lognormal 的 (μ, σ)、兩個 sampler 的向量；Go 逐 bit 相同。選項 B：Box–Muller（每個樣本兩個 uniform，違反 D-056 的「一次 draw」）；選項 C：用平台的 `math.Exp`／`math.Pow`（末位隨 GOARCH 不同，違反 D-038 的 numerical profile）。不採用時：(a) 改 `internal/model/validate/ir.go` 的 `MaxLognormalCV`、`MinWeibullShape`、`MaxWeibullShape` 與 `internal/simulation/engine/sample.go` 的同名常數、`semantic-lognormal-cv-range.json` 與 AI 的 scenario 規則（`aicontext.modeRules`、prompts/scenario）；(b) 換 sampler 即換 engine_version，重產 `engine-vectors.json` 與 0.3.0 的鎖定檔。 |

### T-028 的提案：人工 scenario 與 scenario 的讀取（2026-10-25 提出，同日 APPROVED，H-022）

T-028 實作人工 scenario 的建立與兩種 scenario 的讀取、列表時，已批准的決策（D-004、D-011、D-014、D-020、D-021、D-057、D-067）沒有涵蓋的選擇。三項於 2026-10-25 由 owner 依建議批准（H-022）；在那之前是 PROPOSED，agent 沒有批准。建議都只動 T-028 新增的 `internal/scenarios`、`internal/storage/scenarios` 的 List、contract 的說明與 migration 0015，可以撤回，所以依建議實作；owner 選其他選項時依各項「不採用時」修改。Gate：T-028 驗收前。

| ID | 解決 | 主題 | 提案 |
|---|---|---|---|
| D-078 | T-028 實作的解讀（D-021、D-067） | 人工 scenario 的檢查範圍、baseline、base 與名稱 | **APPROVED（2026-10-25，H-022，依建議：(a)(b) 採 A；(c) 本 organization 的任何 version 都可當 base（決策頁 Q2）；(d) 採 A，名稱照原樣保存、由 UI escape（Q3））；2026-10-25 T-028 提出。** 問題：D-021 只說人工與 AI scenario 用同一個 allowlist，D-004、D-067 定了 AI scenario 批准的 effect 與名稱，但沒有寫人工建立時還檢查什麼、`[]` 或不改變模型的 patch 可否存、名稱的規則。(a) **檢查範圍**：選項 A（建議，已實作於 `internal/scenarios` 的 `check` 與 `Create`）：與 AI scenario 的 review 中不涉及 provider 的部分相同：patch profile 與 scenario allowlist（D-014、D-020、D-021、D-057，`patch.Scenario`）、完整 candidate validation（error 拒絕；warning 不拒絕也不保存，Scenario resource 沒有 issues 成員）、domain pack 的規則，再以 base 的整份 manifest 跑 dataset binding stage 並重讀 snapshot（D-006、D-033，與 AI scenario 批准時相同）。不適用：ID pool（scenario 模式不能新增 entity）、expected effects 與 risk（屬 AI proposal，D-016、D-027）、「必須改變模型」（`AI_OUTPUT_INVALID` 是 provider 回答的規則）。單元測試 `TestTheAIScenarioModeTakesTheSameRules` 對同一組 patch 比較 `review.Review`（scenario 模式）與 `check`：接受與拒絕相同、拒絕的 code 相同，只有 (b) 不同。Base 是現行規則已不接受的舊版本時（D-067 允許任何 version），candidate 的 issue 照 createSimulation 的方式回 422，不是 server 錯誤。選項 B：只檢查 patch 與 candidate validation，pack 與 binding stage 留給 createSimulation（之後一定再查）；後果：可以存下一個永遠無法執行的 scenario。(b) **`[]` 與不改變模型的 patch**（review 第 1 輪 I3 改寫）：選項 A（建議，已實作）：只有 `[]` 是 baseline（REQ-010，`docs/PRD.md:34`：「baseline 為空 patch」；`json-patch.schema.json` 的說明也只在 baseline／no-op 的情境允許 `[]`；createSimulation 也接受同樣的 inline patch）；非空但不改變模型的 patch（例如只有 `test`，或把值 replace 成原值）不是 baseline，當作一般 scenario 接受，照原樣（RFC 8785 形式）保存，不改寫成 `[]`（單元測試 `TestCheckKeepsABaseline`）；「必須改變模型」只是 provider 回答的規則（`AI_OUTPUT_INVALID`），不適用於人。選項 B：拒絕非空但不改變模型的 patch（`[]` 仍是 baseline），需要新的 ValidationCode、兩語字串與 `check` 的規則。(c) **Base**：本 organization 的任何 immutable version，不只 latest（D-067 的「舊版本上的 scenario 由人工建立」）；不鎖 model row，因為 version 不可改、model 與 draft 都不變。這是 D-067 的解讀，列出請 owner 確認。(d) **名稱**：人的文字，照原樣保存（不像 D-067 對 provider 文字那樣合併空白或截斷）；schema 的 1–200 字元，httpapi 拒絕 U+0000；只有空白的名稱為 422 `REQUEST_VALIDATION_FAILED`（`/name`，`details[0].code` 為 `CONSTRAINT_FAILED`），與 createProposal 拒絕只有空白的 intent 相同，屬 request 本身的檢查，先於 role。選項 B：接受任何 schema-valid 的名稱。（review 第 1 輪 I4 補列）名稱中的控制字元（Unicode Cc，U+0000 以外，例如換行、U+001B）與雙向文字覆寫字元（bidi override／isolate，U+202A–U+202E、U+2066–U+2069）：選項 A（建議，維持現狀，不改 code）：照原樣保存，顯示時由 UI escape（與 D-067 對 proposal summary 文字「UI 須 escape」的要求相同；名稱不進 log 與 audit，D-079 (d)）；後果：API 的其他 client 也必須自行 escape，名稱在 UI 以外可能顯示成與實際字元不同的順序。選項 B：以 422 `REQUEST_VALIDATION_FAILED`（`/name`，`details[0].code` 為 `CONSTRAINT_FAILED`）拒絕含 Cc（U+0009 等空白是否例外須一併決定）或 bidi 覆寫字元的名稱；需改 `nameIssues`、測試，並在 OpenAPI 與 `scenario.schema.json` 的說明中寫明。不採用時：(a) B 改 `Create`（拿掉 pack 與 binding stage）；(b) B 改 `common.schema.json` 的 ValidationCode、i18n 與 `check`；(c) 只接受 latest 時，`Create` 要鎖 model row 並比較 latest；(d) B 拿掉 `nameIssues`；控制字元與 bidi 改採 B 時，改 `nameIssues` 與兩份 contract 的說明。 |
| D-079 | T-028 實作的解讀（D-011、D-012） | Scenario 的 HTTP binding：檢查順序、idempotency、回應、audit、列表與讀取 | **APPROVED（2026-10-25，H-022，依建議：照列，(e) 的已知限制照記）；2026-10-25 T-028 提出。** (a) **順序與錯誤碼**（與其他 route 相同，request 本身的檢查在先）：path ID（400）→ `Idempotency-Key` header（400）→ media type（415）→ body（400／413／422；上限同 createSimulation：`validate.MaxIRBytes` 加 64 KiB）→ 名稱（422，D-078 (d)）→ role（403）→ Idempotency-Key → version（不存在或其他 organization：404）→ patch 與 candidate（422 `MODEL_VALIDATION_FAILED`：patch 的 issue 在 `/patch/<op>/...`，candidate 與 pack 的 issue 在 candidate 的 path，dataset 的 issue 在 `/dataset_bindings/<i>/...`，與 createSimulation 相同）→ binding stage（base 綁 dataset 而沒有 object storage：503；snapshot 讀不到：503，`details[0].code` 為 `DATASET_STORAGE_FAILED`，與 D-069 (b) 相同，原因只寫入 log）。(b) **Idempotency**（D-011）：createManualScenario 需要 `Idempotency-Key`（OpenAPI 已列為必填）；fingerprint 為 URL 的 version ID 與 body 的 RFC 8785 形式（member 順序與空白不影響）；key 的 scope 是 organization、actor、operation、key，所以同 key 同 body 用在另一個 version 為 409 `IDEMPOTENCY_KEY_CONFLICT`；只存成功的 201（與 projects、models、runs、proposals 相同），錯誤不存，被拒的 request 不留下 key；replay 前仍查 role。(c) **回應**：201 為 `scenario.schema.json` 的 Scenario：patch 為 RFC 8785 形式、patch_hash 為 D-019 的 hash、`source_proposal_id` 為 null、`created_by` 為 caller、`created_at` 為 DB 的時間（UTC）。(d) **Audit**：`scenario.create`，metadata 只有 model_id、base_version_id、patch_hash 與 operations（op 的數量）；不含名稱（人的文字，同 D-041、D-068）與 patch 的內容（SECURITY §9）。讀取不寫 audit。(e) **列表**：listScenarios 列出 base 為該 version 的所有 scenario（人建立的與 AI scenario 批准建立的，以 `source_proposal_id` 區分），依 (created_at, id) 由舊到新；limit 1–100（預設 50）；cursor 綁 organization、version 與 list，其他 cursor 為 400；version 不存在或屬於其他 organization 為 404。已知限制（review 第 1 輪 L4）：keyset paging 依 (created_at, id)，而 created_at 是 transaction 開始的時間（`now()`，D-080 (b) 固定）；一個較早開始、較晚 commit 的 scenario，其 (created_at, id) 可能在 client 已拿到的 cursor 之前，此後以該 cursor 往後翻頁的 client 會永久漏掉它（只有從第一頁重新列出時才看得到）。這是 repository 的慣例（listProjects、listModels 同樣以 (created_at, id) 為 keyset，created_at 預設 `now()`），不是 T-028 獨有；修正（例如改用 commit 順序的序號，或 cursor 之前留一段重疊時間）屬跨列表的決策，本決策不處理。(f) **讀取**：getScenario 與列表都把存下的 patch 轉成 RFC 8785 形式（jsonb 讀回的拼法不同），並在回答前檢查它的 hash 等於 patch_hash；不符是 server 錯誤（500，`ErrCorrupt`），不顯示。Scenario 不可修改（0001），所以每次回答相同。不採用時：(a)(b)(c)(d)(f) 改 `internal/scenarios`；(e) 改 `internal/storage/scenarios.List` 與 cursor；兩者都要改 OpenAPI 的說明。 |
| D-080 | T-028（D-013） | Migration 0015：scenario 的資料庫保證 | **APPROVED（2026-10-25，H-022，依建議：採 A）；2026-10-25 T-028 提出。** 選項 A（建議，已實作）：(a) `scenarios.name` 為 1–200 字元（CHECK `scenarios_name_length`），與 `scenario.schema.json` 相同；(b) 除了 schema owner，任何 role 建立 scenario（API 是唯一有 INSERT 的 runtime role，0002）都只能為 transaction 的 `app.user_id` 建立（42501），且 `created_at` 必須是 transaction 的時間（55000），與 0012 對 proposal 建立者的做法相同，所以列表的順序與 cursor 只由 server 決定；schema owner（operator、SQL 測試）不受限；（review 第 1 輪 L4）把 created_at 固定為 `now()`（transaction 開始的時間）也限制了之後對 D-079 (e) 漏列問題的修正：若改以 commit 時間或其他欄位排序，需要新 migration 放寬或改寫此 trigger（已部署的 0015 不得改寫），並重新決定 created_at 的意義；(c) 新 index `scenarios_base_idx (organization_id, base_version_id, created_at, id)` 供列表使用；(d) 其他沿用：0001 的 immutable trigger 與 composite tenant FK、0002 的 FORCE RLS 與 grant（API 只有 SELECT／INSERT，worker 只有 SELECT）、0013 的 proposal effect 檢查。Patch 是否為 scenario patch、套用後是否 valid、patch_hash 是否為其 hash，仍由 API transaction 檢查（API_DATABASE §8）。測試：`db/tests/scenarios.sql`。選項 B：不新增 migration，只靠 application（creator 與時間沒有 DB 端的保證，列表沒有 index）。不採用時：B 以新 migration 撤掉 0015 的 trigger、CHECK 與 index。 |
| D-087 | T-054（H-022 Q4、D-069 (b)、T-054 review M1） | patchDraft 與 resetDraft 讀不到 dataset snapshot 時的回應 | **APPROVED（2026-10-25，H-023）。** 問題：H-022 只批准 publishVersion 與 createSimulation 改回 503；patchDraft 與 resetDraft 也跑同一個 binding stage，讀不到 snapshot 時原本回 500。A（批准，已實作於 T-054 第 2 輪）：與 publishVersion、createSimulation、approveProposal、createManualScenario 相同，回 503 `SERVICE_UNAVAILABLE`，`details[0].code` 為 `DATASET_STORAGE_FAILED`，原因只寫入 log；request 本身被取消或逾時照一般的 context 錯誤處理；transaction rollback，draft revision 不變、沒有 audit。B（不採用）：維持 500，只回退這兩個 route 的對應。不涵蓋：run trace 下載與 import error report 的 object storage 讀取失敗（不是 snapshot）。gate：T-054 驗收前。 |

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
| H-018 | 2026-10-20 | Owner 對決策頁回覆「全都照建議」：D-047–D-060 APPROVED、ADR-026 ACCEPTED（D-050 採 A：R1–R4 全為 error；schema_version 維持 0.1；故障、交班、換線 finish-first；D-057 完整 allowlist）；每班人數、例外日、批量投入表、產線計數器納入 P0，零容量緩衝、拼板／分板、跨步驟持有資源、忙碌時間故障時鐘、交班前做不完不開始、AGV 排在 P0 之後；示範用 live AI：Claude Opus 5.5、開發期兩個 organization 合計每月 100 美元並另設每日上限、示範期只送 synthetic 與使用者輸入的文字、log 不存原始 prompt 與回應。D-061、D-062 與 ADR-027 是之後才提出的，不在此範圍 | live AI 開啟前：每日 token 上限（T-053）、在 aiapproval 記錄本批准、owner 提供 key 與 Console spend limit、查證 Anthropic 資料保留規則；ADR-027；D-061、D-062 |
| H-019 | 2026-10-20 | Owner 對第二張決策頁（https://claude.ai/artifact/QkiAbmqDLi1bELibi6Gk5w）回覆「Q2 選 B，其他照建議」：ADR-027 ACCEPTED；D-061 APPROVED（含 Q5：寫入結果時 DB 的暫時錯誤仍重試）；D-062 APPROVED，(e) 預期效果只能定性（Q1），(i) 選 B：flow 改接或刪除使 base 中到得了的 node 變成不可達時，與刪除同為 high（Q2）；D-063 APPROVED：(a) 採 A，兩道關卡 fail closed（Q3），(b) scenario 可改 shift label，被 `shift_capacity` 參照的 label 刪除或改名由 candidate validation 回 `CALENDAR_INVALID`，T-049 解除 calendars 的 gate 前放寬 patch 的 `afterApply`（Q4），(c) 超過 365 天的分布參數回 `LIMIT_EXCEEDED`，(d) validator 規則延到 T-047–T-050。不包含 live AI 開啟（T-053）。 | live AI 開啟前：每日 token 上限（T-053）、在 aiapproval 記錄 H-018 的批准、owner 提供 key 與 Console spend limit、查證 Anthropic 資料保留規則 |
| H-020 | 2026-10-25 | Owner 在對話中選擇「升到 go1.27.3 (Recommended)」：Go 標準函式庫的 GO-2026-6809（crypto/x509，2026-10-21 公布）使 CI 的 govulncheck 在 go1.27.2 上失敗；修正版 go1.27.3 於 2026-10-19 發布，未滿依賴的兩週規則。核准此一例外：`go.mod` 的 toolchain 改為 go1.27.3（ADR-011），其他依賴與之後的 Go 版本仍依兩週規則。不採用的選項：在 CI 暫時豁免此漏洞到 2026-11-02、或讓 CI 維持紅燈到滿兩週。 |
| H-021 | 2026-10-25 | Owner 對第三張決策頁（https://claude.ai/artifact/T1xUsmk9ToV274qsPVRKqm）回覆「全都照建議」：D-064、D-067、D-068、D-069、D-070、D-071、D-072 依建議 APPROVED。五個有取捨的題目都採建議：Q1 低信心 AI 建議逐欄確認（D-070 (d) A）；Q2 結束早於開始或耗時不一致為 invalid row（D-072 (b) A）；Q3 批准時重算的 risk 與存下的不同則拒絕批准（D-069 (b)）；Q4 常數與單位換算的 transform 另立 decision（D-073，PROPOSED）；Q5 不另做人工清除 AI suggestion 的路徑（D-070 (d)）。T-017、T-026、T-047 因此完成。不在這次範圍：D-069 中 `publishVersion`／`createSimulation` 是否改回 503（決策頁沒有列出）。 |
| H-022 | 2026-10-25 | Owner 對第四張決策頁（https://claude.ai/artifact/2VEE9wK2WmnqXQdir8EeUq）回覆「全都照建議」：D-074、D-075、D-078、D-079、D-080 依建議 APPROVED。Q1 變成可用的 unit 先依故障時刻 FIFO 給等待中的故障（D-074 (a) B）；Q2 人工 scenario 可建在本 organization 的任何 version 上（D-078 (c)）；Q3 名稱照原樣保存、由 UI escape（D-078 (d) A）；Q4 `publishVersion` 與 `createSimulation` 讀不到 dataset snapshot 時也回 503 `SERVICE_UNAVAILABLE`（`DATASET_STORAGE_FAILED`），由 T-054 實作（D-069 的補充）；Q5 H-019 的 D-062 (i) 選 B 是本意，維持。T-048、T-028 因此完成。 |
| H-023 | 2026-10-25 | T-054 review（M1）指出 coordinator 把 503 延伸到 patchDraft、resetDraft 超出 H-022 批准的「兩個 endpoint」。Agent 以選擇題詢問，owner 選「也改為 503 (Recommended)」：D-087 APPROVED（patchDraft、resetDraft 讀不到 dataset snapshot 時回 503 `DATASET_STORAGE_FAILED`）。批准時的 draft 同步屬 D-069 (b) 的範圍。 |

## 原報告保留的 TBD

| 項目 | 原報告建議／限制 | 決定時間／阻擋範圍 |
|---|---|---|
| LLM provider | ~~OpenAI adapter first + provider interface，未定案~~ **已由 H-001 決定：Anthropic Claude API** | 真實 provider integration 仍受 D-018、資料政策、token quota 阻擋 |
| LLM model | ~~eval 後決定，不 hard-code~~ **H-018：示範用 Claude Opus 5.5**（config 指定，不 hard-code；T-038 評估時再比較，換 model 由 owner 決定） | live AI 開啟前的條件見 H-018 與 T-053 |
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
