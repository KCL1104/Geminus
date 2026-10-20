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
| CB-17 | 無 `/me`／membership discovery，client 無法取得自己的 user ID 與可用 organization；非成員或不存在的 `X-Organization-ID` 回應碼未定 | openapi.yaml:2120-2127、:2186-2203；0002_rls.up.sql:24、:31-35 | T-008–T-010、UI bootstrap | D-008（部分）；**已由 D-045 解決（APPROVED，H-010；2026-10-19 實作於 T-043 的 D-045 slice，待獨立 review）**：`GET /v1/me` 回自己的 user ID 與 membership（organization ID、名稱、role），migration 0008；非成員或不存在的 `X-Organization-ID` 已由 T-009 定為 404 | 審查回報 |
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
| CB-31 | Server-issued ID allocation pool 無格式、範圍與儲存位置，approval 時無法 revalidate | AI_SPEC.md:33；proposal.schema.json；0001_core.up.sql:190-234 | T-024–T-026 | D-016；細節見 D-046（PROPOSED，2026-10-19） | 審查回報 |
| CB-32 | Eval corpus 無 context fixtures；expected labels（`reject_context_reference` 等）無對應 status／error code | TEST_PLAN.md:57；analyst.jsonl、import-mapper.jsonl、modeler.jsonl | T-025、T-027、T-030 的 `make ai-eval` | NEW | 審查回報 |
| CB-33 | AI scenario approval 沒有 scenario name 來源；「latest=base」規則套用於 scenario 使舊版本上的 AI scenario 永遠無法批准 | scenario.schema.json:32-36；0001_core.up.sql:247；http.schema.json:447-466；AI_SPEC.md:57、:61 | T-026、T-028 | D-004（部分）；T-026：名稱見 D-067（PROPOSED，建議 A 已實作：取自 proposal summary）；latest=base 由 D-062 (b)（APPROVED）與 AI_SPEC §6 決定，兩種 mode 都要求，舊版本上的 scenario 由人工建立（T-028） | 審查回報 |

## F. 本次 bootstrap 發現並已修正的重現性問題

| ID | 問題 | 處理 | 等級 |
|---|---|---|---|
| CB-34 | 依 `requirements-spec.txt` 建立的乾淨環境中 `make spec-check` **失敗**（exit 2）：jsonschema 缺 `rfc3339-validator` 時靜默略過 `date-time`，`invalid-timestamp-format` 反例因此通過 | 已 pin `rfc3339-validator`、`rfc3986-validator`；`check_contracts.py` 在 schema 使用的 format 沒有 checker 時直接失敗（以舊 pins 實測會失敗） | 實測 |
| CB-35 | `go … ./...` 會納入 `apps/web/node_modules/flatted/golang/...`；`check_docs.py` 會掃 `.venv`／`node_modules` 的 Markdown | Makefile 改用 `./cmd/... ./internal/...`；T-042 review 後 `go.mod` 以 `ignore ./apps/web/node_modules` 讓 `./...` 不再包含它（architecture check 因此檢查 module 的每個 package）；`check_docs.py` 排除 vendored 目錄 | 實測 |
| CB-36 | H-001 指定 Anthropic 並要求兩個 organization key rotation，原契約只有單一 credential | 登記 D-018（PROPOSED）；config 暫時拒絕 `AI_PROVIDER=anthropic`，直到 T-024 + D-018 + 資料政策 + quota。T-024：adapter 已依 D-018 寫好（`internal/ai/provider/anthropic`，只對本機 httptest server 測過）；config 現在只在 `AI_POLICY_APPROVAL` 是程式內已登記的 owner 核准時才接受 `anthropic`，而登記清單（`internal/config/aiapproval`）是空的，所以預設設定仍無法開啟 live AI；adapter 只接受該 package 才能產生的 `Approval` token（T-024 review L5） | 已核對 |

## G. T-042 boundary guard 發現

| ID | 問題 | 證據 | 阻擋 | 涵蓋 | 等級 |
|---|---|---|---|---|---|
| CB-37 | generic Result 的 domain metrics 欄位叫 `metrics.dora`：任何 DomainPack 的 `ComputeMetrics`（含 T-042 test-only generic queue pack 的 `longest_queue_wait_minutes`）都寫進名為 DORA 的欄位，generic contract 帶了 software-delivery 的名稱 | simulation.schema.json `Result.metrics.dora`；`des.Engine.DomainMetrics`；`internal/domainpack/conformance/queuepack_test.go` | 第二個真正的 domain（P1）；P0 只有 software delivery，不阻擋 | NEW（建議在第二個 pack 前以 schema 版本變更改名為 domain metrics，或依 pack ID 分組；不在 T-042 自行修改 contract） | 實測 |

## H. T-023 run API／worker 發現

| ID | 問題 | 證據 | 阻擋 | 涵蓋 | 等級 |
|---|---|---|---|---|---|
| CB-38 | (a) Run 的 failure 沒有 code 目錄：`Run.error` 是 common `Error` envelope，`error.code` 只能是封閉的 ErrorCode 清單，沒有 run 專用的 code（event budget、wall timeout、entity 上限、input、engine version、artifact storage、lease／worker 耗盡）。T-023 暫時對應為：上限與 engine 拒絕的 input → `MODEL_VALIDATION_FAILED`（D-023 把 runtime limit 算作 validator 的 issue），基礎設施重試用完 → `SERVICE_UNAVAILABLE`，其餘 → `INTERNAL_ERROR`；failure 本身的 code（`EVENT_BUDGET_EXCEEDED`、`WALL_TIMEOUT_EXCEEDED`、`ACTIVE_ENTITIES_EXCEEDED`、`ENTITY_VISITS_EXCEEDED`、`RUN_INPUT_INVALID`、`ENGINE_VERSION_MISMATCH`、`ENGINE_INTERNAL`、`ARTIFACT_STORAGE_FAILED`，及 ADR-024 的 `LEASE_EXPIRED`、`WORKER_STOPPED`、`INTERNAL`、`NO_RESULT`）放在 `details[0].code`，上限的 `details[0].path` 為 Run 中該上限的位置。(b) `downloadSimulationTrace` 原本回 `SignedDownload`，與 APPROVED D-044「授權後串流，不發 signed URL」衝突；T-023 依 D-044 改為直接回 `TraceArtifact`（`Content-Disposition: attachment`）。`downloadImportErrors` 仍回 `SignedDownload`，未改。 | simulation.schema.json `Run.error`；common.schema.json `ErrorCode`；`internal/runs/runs.go` `runError`；openapi `downloadSimulationTrace`、`downloadImportErrors` | (a) UI 依 code 顯示 run 失敗的原因（T-032）；(b) T-016／T-017 的 error report 下載 | (a) NEW；(b) D-044 | 實測 |

CB-38(a) 的延伸（T-046 phase 2）：ADR-026 D-050 的 runtime backstop 帶來兩個 run failure code，`SIMULATION_DEADLOCK`、`SIMULATION_STALLED`（T-047–T-050 產生），依同一暫定對應以 `MODEL_VALIDATION_FAILED` 顯示（`simulation.schema.json` 的 `Run.error` 註解、`valid-run-failed-deadlock.json`；D-063 (b) APPROVED，H-019）。目錄本身仍待 owner 決定。

## I. T-016 import 上傳與 profile 發現

| ID | 問題 | 證據 | 阻擋 | 涵蓋 | 等級 |
|---|---|---|---|---|---|
| CB-39 | (a) Import 的 failure 沒有 code 目錄：`Import.error` 是 common `Error` envelope，`error.code` 只能是封閉的 ErrorCode 清單。T-016 暫時對應為：超過上限（`IMPORT_LIMIT_EXCEEDED`）→ `PAYLOAD_TOO_LARGE`；檔案本身的其他問題（`IMPORT_ENCODING_INVALID`、`IMPORT_NUL_CHARACTER`、`IMPORT_EMPTY`、`IMPORT_COLUMN_INVALID`、`IMPORT_SHAPE_INVALID`、`CSV_SYNTAX_INVALID`、`JSON_INVALID`、`JSON_DUPLICATE_KEY`、`JSON_NUMBER_NOT_FINITE`）→ `REQUEST_VALIDATION_FAILED`；基礎設施重試用完（`OBJECT_STORAGE_FAILED`、`OBJECT_STORAGE_UNAVAILABLE`、`LEASE_EXPIRED`、`WORKER_STOPPED`）→ `SERVICE_UNAVAILABLE`；其餘（`UPLOAD_CORRUPT`、`UPLOAD_MISSING`、`JOB_CANCELLED`、`IMPORT_INPUT_INVALID`、`INTERNAL`）→ `INTERNAL_ERROR`；failure 自己的 code 與 JSON Pointer 放在 `details[0]`（與 CB-38(a) 的做法相同）。(b) 需要 owner 確認的行為選擇：只有 header 或空陣列的檔案為 FAILED（`IMPORT_EMPTY`），而不是 0 列的 profile；CSV 欄位數不符的 record 是 invalid row（AWAITING_MAPPING＋error report），不是整個檔案失敗；CSV 只接受逗號分隔；JSON object／array 值推斷為 mixed；preview 字串在 256 字元處以「…」截斷、以 1 MiB 為上限，資料庫會印出超過 400 字元的 JSON 數字改存 binary64 最短形式；profile job 失敗或被取消時 import 的 row 在同一 transaction 跟著變 FAILED（migration 0010，T-016 review M1；先前以讀取時推導，已移除）。(c) 實作參數，待 benchmark（T-037）：值 32,768 字元、JSON 巢狀 32 層、preview 256 字元與 1 MiB、error report 16 MiB 與保留 10,000 筆 invalid row、上傳讀取期限 5 分鐘；T-016 review H1 新增：一筆 CSV record（含 header）與 JSON events 的一列（含前面的空白與逗號，也用於陣列之前與之後的資料）以寫入的 bytes 計至多 1 MiB（`Limits.MaxRecordBytes`），一列只保留 header 欄數加一個欄位；JSON model 2 MiB 不變；failure path 至多 2,048 字元（common.schema.json 的上限，review L1）。值 32,768 字元同時寫在 `import.schema.json` 的 `ProfileColumn.max_length` maximum，調整它是 contract 變更（review L5）；error report 在記憶體中產生（至多 16 MiB），待正式 object storage adapter 時再評估串流寫入（review L3）。(d) 孤兒 object（file 之後還有 part、與同一 key 競爭落敗、commit 失敗）沒有 maintenance 清理，與 T-023 的 L6 同一項。 | `internal/import/upload/resource.go` `envelope`；`internal/import/profile` `DefaultLimits`；openapi `uploadImport`、`getImport`、`downloadImportErrors` | (a) UI 依 code 顯示 import 失敗的原因（T-015／T-017）；(b)(c) Alpha 設定凍結（D-007）；(d) 正式 object storage adapter 前 | (a) NEW；(b) D-043；(c) D-007；(d) D-044 | 實測 |

CB-38(b) 已由 T-016 依 D-044 解決：`downloadImportErrors` 改為授權後直接串流 CSV（`text/csv`，attachment），不再回 `SignedDownload`；`SignedDownload` 仍留在 http.schema.json，P0 沒有 operation 使用（正式 adapter 的 ADR 決定是否需要）。
## J. T-024 AI provider／context 發現

| ID | 問題 | 證據 | 阻擋 | 涵蓋 | 等級 |
|---|---|---|---|---|---|
| CB-40 | (a) ID pool 違規沒有 issue code：`idpool.Check` 回報 `not_in_pool`／`wrong_kind`／`used_in_base`／`missing_ir` 與 JSON Pointer，但 ValidationCode 目錄沒有對應 code，T-026 approval revalidation 需要一個（建議 `ID_POOL_VIOLATION`，reason 放 message 或 details）。(b) provider 回答不符 output schema 時，T-024 以 issue code `AI_OUTPUT_INVALID` 回報（ValidationIssue.code 為 open，所以合法，但不在 ValidationCode 目錄，UI 只能顯示英文 message）。(c) provider adapter 的 14 個內部錯誤 code（`provider_not_configured`、`policy_not_approved`、`rate_limited`、`concurrency_limited`、`timeout`、`unavailable`、`credentials_exhausted`、`refused`、`output_truncated`、`invalid_output`、`context_too_large`…）沒有對應 D-025 ErrorCode 或 proposal／analysis `failed` 的 `error` 內容的契約；T-024 只提供 typed code，HTTP／狀態對應留給 T-025／T-027／T-030（建議：前兩者 → `AI_PROVIDER_NOT_CONFIGURED`／`AI_POLICY_NOT_APPROVED`，`rate_limited`／`concurrency_limited` → `RATE_LIMITED`，其餘 → proposal `failed` 並以 code 放 `details[0].code`）。(d) `ai_proposals.provider_metadata`（jsonb，預設 `{}`）沒有 schema；T-024 的 `provider.Metadata`（provider、model、credential_label、failed_over_from、role、prompt_template_version、schema、output_locale、input_hash、output_hash、usage、latency_ms、provider_request_id、context 的省略與遮蔽計數）是目前唯一的定義。(e) D-046 的 ID pool 存在 `ai_proposals.id_pool`（migration 0011），但 HTTP `Proposal` 不顯示它，也不顯示 `output_locale`；是否要讓 UI 看到由 T-025／T-028 決定。 | `internal/ai/idpool`；`internal/ai/provider/errors.go`、`provider.go`；common.schema.json `ErrorCode`、`ValidationCode`；0001_core.up.sql `provider_metadata`；0011_ai_id_pool.up.sql | T-025、T-026、T-027、T-030 | D-061（APPROVED，2026-10-20，H-019；T-025 已實作）：(a) `ID_POOL_VIOLATION`、(b) `AI_OUTPUT_INVALID` 已加入 ValidationCode；(c) 的對應在 `internal/proposals/public.go`；(d) 存 `provider.Metadata`，不另立 schema；(e) 不對 client 顯示；D-046（儲存，APPROVED） | 實測 |

## K. T-026 人類批准與拒絕的發現

| ID | 問題 | 證據 | 阻擋 | 涵蓋 | 等級 |
|---|---|---|---|---|---|
| CB-42 | (a) Approve 與 Reject 的 `comment` 沒有存放處：`ai_proposals` 沒有欄位，D-041 只涵蓋 publish 的 comment，Proposal 也不顯示誰、何時決定。(b) 非 ready 狀態（generating、needs_input、invalid、failed）的 approve／reject，以及再批准 conflicted 的 proposal，沒有錯誤碼（D-030 只寫 approved／rejected）。(c) Base 已變時 AI_SPEC §6 要求「回 409 且 proposal 轉 conflicted」：一個被拒絕的 request 寫入狀態，它的 409 是否為 Idempotency-Key 的回應、client 指定的 `expected_base_version_id` 與 proposal 的 base 不同時怎麼處理，都沒有定義。(d) 批准時重算的 validation、risk、patch hash 或 manifest 與存下的不同時怎麼處理沒有定義（AI_SPEC §6 只說「重新驗」）。(e) Audit metadata 與資料庫對「一個 proposal 一個 effect」的保證範圍沒有定義（0001 只檢查 approved proposal 指向的 effect 指回它）。另：API_DATABASE §4「同一 proposal 已批准…可回同一 effect」與 D-030 的 409 不一致，CB-12 已由 D-030 解決，T-026 依 D-030 實作，§4 已加註。 | http.schema.json Approve／Reject；proposal.schema.json；0001_core.up.sql `ai_proposals`、`protect_proposal_content`；AI_SPEC §4、§6；API_DATABASE §4 | T-026、T-028、T-031 | D-068（(a)，PROPOSED）、D-069（(b)–(e)，PROPOSED）；T-026 依建議實作（`internal/proposals/approval`、migration 0013） | 實測（T-026 的 Go 與 SQL 測試；SQL 與 DB 測試待 coordinator 執行） |

## 已核對為一致的項目（摘要）

Golden 算術（GOLDEN-001/002）與 SplitMix64／replication seed vectors 可重算；proposal、import、run、analysis 狀態 enum 在 schema、OpenAPI、SQL CHECK 間一致；composite tenant FK 指向既有 PK/unique；`x-required-roles` 與 SECURITY.md RBAC 及報告 p.40 一致；runtime roles NOSUPERUSER/NOBYPASSRLS、FORCE RLS；0.80 confidence 門檻各處一致；provider output schema 不接受 trusted fields。

## 需要人類確認的決定（依下一個任務排序）

1. **T-003 開始前**：批准或修改 D-001、D-002、D-014、D-015，並一併決定 CB-01–CB-12 是否併入這些 D-entry 或另立新 D-entry。
2. **T-006/T-007 migration review 前**：D-008、D-013，並處理 CB-13–CB-16（CB-14 為安全項，建議優先）。
3. **T-005/T-020 golden 前**：D-005、D-006（CB-01、CB-15、CB-20、CB-21）。
4. **T-019 前**：D-007、D-011（CB-18、CB-19）。
5. **T-024 前**：D-016、D-018，以及資料政策與 token quota（原報告 TBD）。
6. **T-032 前**：CB-38（run failure 的 code 目錄；T-023 的暫定對應見該列）；~~T-016／T-017 前決定 `downloadImportErrors` 是否也依 D-044 改為串流~~（T-016 已依 D-044 改為串流）。T-015／T-017 前：CB-39（import failure 的 code 目錄與 T-016 的行為選擇、實作參數）。
7. 其他 TBD（auth provider、cloud/DB、object storage、SQL generation、frontend state libs）見 [DECISIONS_AND_TBD.md](DECISIONS_AND_TBD.md)；機器可讀的 gate 對照見 `checks/decisions-check.json`。
