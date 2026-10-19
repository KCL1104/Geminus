# T-001 契約阻擋項盤點（Contract Blockers）

日期：2026-10-15｜任務：T-001｜狀態：**待人類 review**。本檔只列問題與需要的決定，**不批准、不選定任何 PROPOSED 方案**。

## 方法與可信度標示

三個唯讀審查分別比對 IR/Simulation、API/DB/Security、AI/Import 的衍生文件、schemas、OpenAPI、SQL、fixtures 與原報告頁碼，再由主導 agent 抽查。每項標示證據等級：

- **實測**：本次以命令重現（一次性 PostgreSQL 16.15、工具執行）。
- **已核對**：主導 agent 親自重讀所引行號，內容相符。
- **審查回報**：唯讀審查附行號回報，尚未由主導 agent 逐行重驗；review 時請優先確認。

「涵蓋」欄指既有 D-xxx 是否已登記此缺口；NEW 表示 decisions register 尚未涵蓋，是否升格為新 D-entry 由人類決定。

## A. 阻擋 T-003 / T-004 / T-005（契約凍結、validator、hash）

**2026-10-15 已決議**：CB-01–CB-12 由 owner 逐項選擇，登記為 D-019–D-030（CB-09 併入 D-001），見 [DECISIONS_AND_TBD.md](DECISIONS_AND_TBD.md)。

| ID | 問題 | 證據 | 阻擋 | 涵蓋 | 等級 |
|---|---|---|---|---|---|
| CB-01 | `patch_hash` 與 dataset `content_hash` 的雜湊輸入未定義；snapshot 內含 `content_hash` 可能自我引用；`config_hash` 未正規化 duration | scenario.schema.json:40-45、0001_core.up.sql:249 有欄位；docs 無 `patch_hash` 定義（grep 為空）；TWIN_IR_SPEC.md:64-70；dataset-snapshot.schema.json:29-44 | T-005、T-003 | D-006（未涵蓋輸入） | 已核對（`patch_hash` 部分）；其餘審查回報 |
| CB-02 | JSON Patch `test` op 與 allowlist 衝突：prompt／spec 建議 `test /resources/1/id`，但 AI scenario allowlist 僅列參數路徑；人類 scenario 無 allowlist；未定 prefix 或 exact 比對 | TWIN_IR_SPEC.md:78、:80；prompts/scenario/system.md:7；valid-scenario.json:8-11；AI_SPEC.md:45、:65 | T-004、T-025、T-028 | D-014/D-016（未涵蓋 per-op path） | 已核對 |
| CB-03 | AI 刪除 node 永遠無法通過驗證：AI allowlist 禁止 `/views`，但 dangling view position 是 error | TWIN_IR_SPEC.md:55、:78、:80；spec_fixture_lint.py:74-75；modeler.jsonl MOD-040 | T-003、T-025、T-026 | NEW | 已核對 |
| CB-04 | Validator 輸出不具決定性：stage 是否 fail-fast、issue 排序、重疊 code（UNKNOWN_REFERENCE/VIEW_REFERENCE_INVALID、ROUTING_INVALID/CONSTRAINT_FAILED）；缺 patch／run-config／tick overflow／runtime budget 的 code 名稱 | TWIN_IR_SPEC.md:42-58；SIMULATION_SPEC.md:13、:52、:88；cases.json:307-309 只靠 subset 比對通過 | T-004、T-020、T-021 | NEW | 審查回報 |
| CB-05 | HTTP `Error.details[]` 只容 `{path, reason}`，無法承載 spec 要求的 issue code／severity | common.schema.json:121-142 vs TWIN_IR_SPEC.md:42、AGENTS.md §5 | T-003、T-013 | NEW | 審查回報 |
| CB-06 | 沒有 HTTP error-code catalogue；缺 Idempotency-Key 回 400 或 428 未定；PATCH draft 無 415 | openapi.yaml:2142；API_DATABASE.md:28-30、:61；common.schema.json:111-115 | T-003、T-009、T-013 | NEW（D-012 只涵蓋 routes） | 審查回報 |
| CB-07 | Analyst provider output 必須供應 `run_id`（受信任 ID），且同一 schema 兼作儲存資源（審查 probe：交換 baseline/scenario run_id 仍 schema-valid） | analysis.schema.json（`/properties/scenario`、`baseline/anyOf/0`、evidence items 皆 required `run_id`）vs AI_SPEC.md:15；http.schema.json:756-833 | T-003、T-030 | NEW（D-002 只涵蓋 proposal） | 已核對 |
| CB-08 | `risk_level` provider 必填且與 server 計算值共用同一欄位／DB 欄 | proposal-output.schema.json:28-30；proposal.schema.json:80-82；0001_core.up.sql:206 | T-025、T-026 | D-016（原則有，契約未分離） | 審查回報 |
| CB-09 | ID prefix 清單與產生方式（ULID／random、entropy）未定；imports/scenarios/analyses/jobs/datasets 無 prefix | DECISIONS_AND_TBD.md D-001；報告 pp.30-33、42 | T-003、T-006 | D-001（缺清單） | 審查回報 |
| CB-10 | Draft 每次 PATCH 是否須 semantic valid 未定；Draft 物件無 issues 欄；TEST-001 要求的「minimal IR」正例不存在（報告 p.14 空圖被 schema 拒） | TWIN_IR_SPEC.md:9、:42；PRD.md:51；http.schema.json:178-220；model-ir.schema.json:444、:497 | T-003、T-004、T-012 | D-014（部分） | 審查回報 |
| CB-11 | 數值上限：constant/mean 上限 1e12 以 day 計＝8.64e19 ms，超過 int64 tick；metadata／pointer depth 與 IR 大小無具體數值 | model-ir.schema.json:750-758；SIMULATION_SPEC.md:13；TWIN_IR_SPEC.md:9、:74 | T-004、T-020 | D-007（未涵蓋） | 已核對（算術實測） |
| CB-12 | 已 approved proposal 再 approve：API_DATABASE 說可回同一 effect，AI_SPEC 說 base 移動回 409+conflicted；DB trigger 禁止 approved→conflicted，後者路徑會變 500 | API_DATABASE.md:43；AI_SPEC.md:57、:61；0001_core.up.sql:473-478 | T-003、T-026 | D-002/D-011（衝突本身 NEW） | 審查回報 |

## B. 阻擋 T-006 / T-007 / T-008–T-010（DB、RLS、tenant）

| ID | 問題 | 證據 | 阻擋 | 涵蓋 | 等級 |
|---|---|---|---|---|---|
| CB-13 | 文件要求 last-Owner 檢查鎖 organization row，但 `twin_api` 只有 SELECT；`SELECT … FOR UPDATE` 失敗 | API_DATABASE.md:15、SECURITY.md:21 vs 0002_rls.up.sql:38、:46-47；**實測**：`ERROR: permission denied for table organizations` | T-007、T-010 | NEW | 實測 |
| CB-14 | **安全**：object key CHECK 用 `LIKE 'tenant/'‖org‖'/%'`，`_` 是萬用字元，`tenant/orgXa/…` 可通過 `org_a` 的檢查 | 0001_core.up.sql:132、185、186、334；**實測**：LIKE=true、`starts_with`=false | T-006 | NEW | 實測 |
| CB-15 | Dataset manifest 以 DB 預設 collation `ORDER BY logical_ref`（無 `COLLATE "C"`），與 Go byte 排序可能不同，造成 commit 失敗或 hash 不一致 | 0001_core.up.sql:497、:521；TWIN_IR_SPEC.md:68；common.schema.json:173 | T-005、T-006 | D-006（未定排序） | 已核對（本環境未裝 en_US，排序差異未實測） |
| CB-16 | Worker 對 `imports`、`jobs` 有整表 UPDATE，可改寫已確認 mapping 或設 COMMITTED；其他表用 column grant | 0002_rls.up.sql:54 vs API_DATABASE.md:49、:51、D-009 | T-007、T-016–T-018 | D-013（細節 NEW） | 已核對 |
| CB-17 | 無 `/me`／membership discovery，client 無法取得自己的 user ID 與可用 organization；非成員或不存在的 `X-Organization-ID` 回應碼未定 | openapi.yaml:2120-2127、:2186-2203；0002_rls.up.sql:24、:31-35 | T-008–T-010、UI bootstrap | D-008（部分） | 審查回報 |
| CB-18 | Idempotency：過期但未清除的 key 重用行為未定；`saveImportMapping` 是唯一沒有 Idempotency-Key 的 POST | API_DATABASE.md:69；0001_core.up.sql:392-407；openapi.yaml:1140-1153 | T-017、T-019 | D-007/D-011（細節 NEW） | 審查回報 |

## C. 阻擋 T-019（queue / lease）

| ID | 問題 | 證據 | 阻擋 | 涵蓋 | 等級 |
|---|---|---|---|---|---|
| CB-19 | Cancel 的 lock 順序未定（finalize 為 job→run，可能 deadlock）；reaper 不看 `cancellation_requested`，worker crash 後已取消的 run 會重跑；progress 寫入沒有 lease fence；running→cancelled 的 job 終態未定 | SECURITY.md:59；API_DATABASE.md:71、:73；db/queries.sql:126-139、161-162；0002_rls.up.sql:58-59 | T-019、T-023 | D-011（細節 NEW） | 審查回報 |

## D. 阻擋 T-020–T-022（DES、metrics）

| ID | 問題 | 證據 | 阻擋 | 涵蓋 | 等級 |
|---|---|---|---|---|---|
| CB-20 | 同 tick 事件順序矛盾：priority 表把 route 放 10，但 completion 描述為 release 後 inline route；delay completion 無 priority；兩種解讀產生不同 FIFO 與 digest | SIMULATION_SPEC.md:15 vs :25；報告 p.22 事件類型 | T-020、T-021 | D-005（候選文字自身不一致） | 已核對 |
| CB-21 | 「strictly positive／zero-time」只對 constant/triangular 定義；empirical 0 樣本與 cycle 判定階段未定 | TWIN_IR_SPEC.md:36、:51、:53；SIMULATION_SPEC.md:13；dataset-snapshot.schema.json:33 | T-004、T-020 | D-005（NEW 細節） | 審查回報 |
| CB-22 | IR `metrics[]` 宣告與 Result 固定 keys 無對應規則；sink `outcome` 預設值未定，影響 throughput/lead time | model-ir.schema.json:363-369、528-670；simulation.schema.json:525-695；SIMULATION_SPEC.md:56 | T-021、T-022 | NEW | 審查回報 |
| CB-23 | Result summary 規則（unavailable 必有 reason、只有 replication population 可帶 CI）未入 schema；metric path 格式不一（`/metrics/...` vs `lead_time_minutes.p50`）；DORA metric 名稱、reason codes、stage/event mapping 位置未定，P0 DORA 無法成為 available | simulation.schema.json:430-505、672-677；analysis.schema.json:62-66；http.schema.json:986-990；SIMULATION_SPEC.md:66-74；valid-result.json:69-82 | T-022、T-029、T-030 | D-010（部分） | 審查回報 |
| CB-24 | Default template fixture 過載：ρ=(1/48)·240/2=2.5，review queue 無界增長 | default-delivery.json:14-20、48-69 | T-022、AI eval baseline | D-017 | 審查回報 |
| CB-25 | `report-example.json` 自稱保留報告 pp.15-18，但 "Ci Runner"／positions 0-800 與報告 "CI Runner"／0-1000 不同 | golden-models/README.md:3；report-example.json:24、153-174 | T-005 hash vectors | NEW | 審查回報 |

**2026-10-15 處理狀態（T-020–T-022 實作後）**：CB-20 → D-032、CB-21 → D-033（已批准）。CB-22：sink 未寫 outcome 視為 completed（併入 D-038，2026-10-18 批准）；IR `metrics[]` 與 Result 的對應仍未定，移交 T-029。CB-23：「unavailable 必有 reason、只有 population=replication 可帶 CI」已寫入 `simulation.schema.json`，queue stats（unavailable 時 waits 為 null 並帶 reason、available 時 sample_count≥1）與 resource stats（utilization 為 null 時帶 reason）也一併加入（T-022 與其 review，皆附反例 fixture）；reason codes 依 D-039（2026-10-18 批准），metric path 格式與 DORA 名稱、mapping 位置仍歸 T-029／T-030／D-010。CB-24：T-022 以 engine 實測確認（5 replications：reviewer utilization 7 天 0.9946、90 天 0.9996；code_review queue 平均長度 7／30／90 天為 65／281／842；每天約多 18 個 backlog），template 未修改，需 D-017 template 的決定（建議把 reviewer capacity 由 2 提高到 6，ρ≈0.83）。

## E. 阻擋 T-016 / T-017 / T-024–T-030（Import、AI）

| ID | 問題 | 證據 | 阻擋 | 涵蓋 | 等級 |
|---|---|---|---|---|---|
| CB-26 | Import inference proposal 沒有 generator 契約：deterministic DomainPack 或 LLM 未定；`InferenceResult` 無 schema；confidence/risk/intent、data quality 欄位來源未定 | ARCHITECTURE.md:63；AI_SPEC.md:16-20、:75；proposal.schema.json:135-180；0001_core.up.sql:198-228 | T-018、T-025、T-027 | D-009（只涵蓋 commit 邊界） | 審查回報 |
| CB-27 | Import resource 無 profile/columns 契約；preview 是 raw 還是 mapped 不一致；人工 mapping 被迫填 AI 專用 confidence/evidence | import.schema.json:7-166；valid-import.json:13-46；ai-mapping.schema.json:64-71；http.schema.json:585-605 | T-016、T-017、T-027 | D-043（PROPOSED，2026-10-19） | 審查回報 |
| CB-28 | Transform 只有 identity/rfc3339/integer_ms/boolean_literal 且無參數：naive timestamp 或非 ms duration 永遠無法 validate；`metadata` 無 transform；重複 target 行為未定 | ai-mapping.schema.json:33、51-58；canonical-event.schema.json:18-58；TWIN_IR_SPEC.md:86 | T-017、T-027 | D-009（行為 NEW） | 審查回報 |
| CB-29 | Import 無「validation 進行中」狀態，也未記錄 validation 對應的 mapping revision | API_DATABASE.md:49；import.schema.json:51-61；0001_core.up.sql:161-165 | T-003、T-017 | NEW | 審查回報 |
| CB-30 | Prose 中數字與確定性語句的驗證規則未定（P0 是否驗證或禁止數字、違規時 failed 或剝除欄位） | AI_SPEC.md:53、:83；TEST_PLAN.md TEST-024；analysis.schema.json:19-49、121-124 | T-025、T-030 | NEW | 審查回報 |
| CB-31 | Server-issued ID allocation pool 無格式、範圍與儲存位置，approval 時無法 revalidate | AI_SPEC.md:33；proposal.schema.json；0001_core.up.sql:190-234 | T-024–T-026 | D-016（細節 NEW） | 審查回報 |
| CB-32 | Eval corpus 無 context fixtures；expected labels（`reject_context_reference` 等）無對應 status／error code | TEST_PLAN.md:57；analyst.jsonl、import-mapper.jsonl、modeler.jsonl | T-025、T-027、T-030 的 `make ai-eval` | NEW | 審查回報 |
| CB-33 | AI scenario approval 沒有 scenario name 來源；「latest=base」規則套用於 scenario 使舊版本上的 AI scenario 永遠無法批准 | scenario.schema.json:32-36；0001_core.up.sql:247；http.schema.json:447-466；AI_SPEC.md:57、:61 | T-026、T-028 | D-004（部分） | 審查回報 |

## F. 本次 bootstrap 發現並已修正的重現性問題

| ID | 問題 | 處理 | 等級 |
|---|---|---|---|
| CB-34 | 依 `requirements-spec.txt` 建立的乾淨環境中 `make spec-check` **失敗**（exit 2）：jsonschema 缺 `rfc3339-validator` 時靜默略過 `date-time`，`invalid-timestamp-format` 反例因此通過 | 已 pin `rfc3339-validator`、`rfc3986-validator`；`check_contracts.py` 在 schema 使用的 format 沒有 checker 時直接失敗（以舊 pins 實測會失敗） | 實測 |
| CB-35 | `go … ./...` 會納入 `apps/web/node_modules/flatted/golang/...`；`check_docs.py` 會掃 `.venv`／`node_modules` 的 Markdown | Makefile 改用 `./cmd/... ./internal/...`；T-042 review 後 `go.mod` 以 `ignore ./apps/web/node_modules` 讓 `./...` 不再包含它（architecture check 因此檢查 module 的每個 package）；`check_docs.py` 排除 vendored 目錄 | 實測 |
| CB-36 | H-001 指定 Anthropic 並要求兩個 organization key rotation，原契約只有單一 credential | 登記 D-018（PROPOSED）；config 暫時拒絕 `AI_PROVIDER=anthropic`，直到 T-024 + D-018 + 資料政策 + quota | 已核對 |

## G. T-042 boundary guard 發現

| ID | 問題 | 證據 | 阻擋 | 涵蓋 | 等級 |
|---|---|---|---|---|---|
| CB-37 | generic Result 的 domain metrics 欄位叫 `metrics.dora`：任何 DomainPack 的 `ComputeMetrics`（含 T-042 test-only generic queue pack 的 `longest_queue_wait_minutes`）都寫進名為 DORA 的欄位，generic contract 帶了 software-delivery 的名稱 | simulation.schema.json `Result.metrics.dora`；`des.Engine.DomainMetrics`；`internal/domainpack/conformance/queuepack_test.go` | 第二個真正的 domain（P1）；P0 只有 software delivery，不阻擋 | NEW（建議在第二個 pack 前以 schema 版本變更改名為 domain metrics，或依 pack ID 分組；不在 T-042 自行修改 contract） | 實測 |

## 已核對為一致的項目（摘要）

Golden 算術（GOLDEN-001/002）與 SplitMix64／replication seed vectors 可重算；proposal、import、run、analysis 狀態 enum 在 schema、OpenAPI、SQL CHECK 間一致；composite tenant FK 指向既有 PK/unique；`x-required-roles` 與 SECURITY.md RBAC 及報告 p.40 一致；runtime roles NOSUPERUSER/NOBYPASSRLS、FORCE RLS；0.80 confidence 門檻各處一致；provider output schema 不接受 trusted fields。

## 需要人類確認的決定（依下一個任務排序）

1. **T-003 開始前**：批准或修改 D-001、D-002、D-014、D-015，並一併決定 CB-01–CB-12 是否併入這些 D-entry 或另立新 D-entry。
2. **T-006/T-007 migration review 前**：D-008、D-013，並處理 CB-13–CB-16（CB-14 為安全項，建議優先）。
3. **T-005/T-020 golden 前**：D-005、D-006（CB-01、CB-15、CB-20、CB-21）。
4. **T-019 前**：D-007、D-011（CB-18、CB-19）。
5. **T-024 前**：D-016、D-018，以及資料政策與 token quota（原報告 TBD）。
6. 其他 TBD（auth provider、cloud/DB、object storage、SQL generation、frontend state libs）見 [DECISIONS_AND_TBD.md](DECISIONS_AND_TBD.md)；機器可讀的 gate 對照見 `checks/decisions-check.json`。
