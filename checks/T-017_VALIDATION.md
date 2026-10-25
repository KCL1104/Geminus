# T-017 驗證報告：Canonical mapping 與全量 validation pipeline

**日期：2026-10-20｜狀態：獨立 review 第 1 輪 CHANGES REQUESTED（沒有 High；M1–M5、L1–L6、I2、I4、I5），修正見最後一節「Review 第 1 輪的修正」，待複審；DB 與 integration tests 在 agent 的 session 一律 NOT RUN（沒有一次性 DB，依指示不嘗試），第 0 輪的版本由 coordinator 在 16dec30 執行並通過，第 1 輪的修正尚待 coordinator 執行；`planning/tasks.json` 的 T-017 狀態未改（仍為 not_started），只更新了 planned outputs、gates 與 spec refs｜需求：REQ-007、REQ-019｜Gate：D-009、D-014、D-043、D-059（APPROVED）；D-070、D-071、D-072（本任務提出，PROPOSED，待 owner 決定）｜驗收測試：TEST-012、TEST-013**

範圍依 tasks.json 與 coordinator 的指示：

1. canonical-event mapper 與有限的 transform；
2. mapping revision 的 compare-and-set（stale 為 409，從不靜默覆寫）、以 job 執行的全量 rows validation、stale-job fence；
3. 低 confidence 的 mapping 要由人確認（AI 從不確認；為 T-027 的 suggestion 留下確認的位置）、invalid row 報表與重新 mapping；
4. 決定 D-059 的 `end_timestamp`、`shift`、`product` 是否在這裡成為 target field：**是**（D-070 (a)，理由見下）。

另依指示：先改 contract（OpenAPI 的 operation 與範例、typed error code、JSON Pointer、request_id、TS 契約測試）；回給 client 的列不得不安全地回顯像公式或 secret 的 cell（沿用 T-016 的中和）；只用 migration 0014（0013 保留給平行的 T-026），排在 0013 之後但不依賴它；新的 decision 只用 D-070–D-072（PROPOSED），contract blocker 只用 CB-43；不碰 T-047（`internal/simulation`、`internal/model/validate`）與 T-026（`internal/proposals`、model approval、migration 0013）的範圍。測試資料全是 synthetic（站別紀錄、序號、批號都是編的），沒有任何客戶 CSV。

逐條輸出在 `checks/evidence/2026-10-20-T-017/`：`exit-codes.txt`、`environment.txt`、各指令的 log、`red-*.txt`（先紅後綠）、`fuzz-*.txt`、`mutations.txt`、`sql-parse.txt`。

## Commit

| Commit | 內容 |
|---|---|
| T-017a（分支 22ee1aa；worktree 0db016b） | 契約：新增 `import-mapping.schema.json`（每條只有 `source_column`、`target_field`、`transform`；`allOf` 讓 transform 由 target field 決定；`TargetField` 含 D-059 的三欄）；`ai-mapping` 的 target 與 transform 改用它；`import.schema.json` 加 `VALIDATING`、必填的 `canonical_preview`（≤100 個 canonical event 或 null）與 VALIDATING／VALIDATED 的形狀規則；`SaveMapping` 改用人工 mapping 並加 `confirmed_low_confidence`；OpenAPI `saveImportMapping`（`Idempotency-Key`、202 的 VALIDATING 範例、409／422 的 code 與範例）與 `getImport` 的 `remap` 範例；6 個正例、15 個反例 fixture；TS 型別重產與 `contracts.test.ts` 的正反例；`aicontext.TargetFields` 改為 14 個並以 test 對照 schema；Go `Import` 加 `canonical_preview`（parity test 先紅） |
| T-017b（分支 539f48e；worktree 7ead97a） | 純粹的程式，先寫測試：`internal/import/profile.Scan`（與 `Read` 同一個有界掃描器，逐列交給 visitor）、`PreviewJSON`；`internal/import/mapping`（`Check`、`Compile`、`Plan.Apply`、D-072 的列規則、metadata）；`internal/import/validation`（`Validate`、`Write`）；兩個 fuzz target；architecture 規則 `import-parsing-is-pure` 涵蓋 `mapping`、`validation` |
| T-017c（分支 c2ec688；worktree f7c50c5） | Migration 0014（up／down）、`db/tests/import_mapping.sql`（新）、`imports.sql` 與 `roles.sql` 的 mapping 改為完整的存檔、`constraints.sql` 的註解、`render_db_tests.py` 的 ID、`run_db_tests.sh` 在 0012 之後以註解標出 0013 的位置再套用 0014 |
| T-017d（分支 e1da99c；worktree f85c835） | `internal/storage/imports/mapping.go`、`internal/import/upload/mapping.go`（service）與 HTTP route、`internal/import/validator`（job handler）、`cmd/worker` 登錄、architecture 規則 `job-handlers-only-in-the-worker` 涵蓋 validator（probe 先紅）、FAILED 的 `request_id` 改為失敗那一步的 request、envelope 的訊息改為中性、validator 的單元測試、DB-backed 的 API 測試（`internal/api/import_mapping_test.go`） |
| T-017e（分支 8acfe37；worktree 19ef508） | D-070、D-071、D-072（PROPOSED）、CB-43；API_DATABASE §5、§8、§9，ARCHITECTURE，SECURITY §6，TWIN_IR_SPEC §7，AI_SPEC §8，ELECTRONICS_LINE §6，CONTRACT_BLOCKERS（CB-18、CB-28、CB-29 指向新的 decision），db/README，Makefile 的 integration-test 說明；tasks.json 與 traceability |
| T-017f（分支 16dec30；worktree 4b10b5c） | 報告、evidence、Makefile 的註解、`make spec-check` 重產的 `checks/docs-check.json` |
| Coordinator（分支 534c9ba） | 在整合分支上的執行（見「Coordinator 的執行」一節） |

分支 `claude/lucid-meitner-vq1awo` 上的 hash 是 coordinator 把六個 commit cherry-pick 到 T-026 之後的結果；worktree 的 hash 是 agent 原本的分支 `t017`。第 1 輪的修正在 `t017-r1`（自 534c9ba 起），見最後一節。

## 稽核：scope 逐條

| Scope | 判定 | 證據 |
|---|---|---|
| 1. Canonical-event mapper 與有限的 transform | 已完成 | `internal/import/mapping`（純粹，`import-parsing-is-pure` 禁止它碰 storage、tenant、HTTP、檔案）：`Check` 對 profile 的欄名檢查 mapping（D-070 (c)），`Compile` 產生 plan，`Plan.Apply` 把一列轉成 `domainpack.CanonicalEvent`。Transform 只有 identity、rfc3339、integer_ms、boolean_literal（AI_SPEC §8 的 APPROVED D-009），沒有參數、expression 或程式；每個 target field 的 transform 固定，schema（`allOf`）與 server 都檢查（`TestVocabularyIsTheSchemas` 以 JSON Schema validator 本身證明 schema 與 Go 的規則相同）。D-059 的 end_timestamp、shift、product 在 T-017 加入：station log 沒有 end_timestamp 就無法 validate，等 T-052 會讓 P0 的電子產線 log 在那之前無法匯入；pack 層的資料品質規則（多產品時 product 必填、詞彙）留在 T-052。常數與單位換算的 transform（CB-28）**沒有**做：AI_SPEC 的批准文字只列四種，D-059 把常數 transform 指向 CB-28，屬 owner 的決定（D-070 (b)） |
| 2a. Mapping revision CAS | 已完成 | `saveImportMapping`：先鎖 import 的 queued／running validation job、再鎖 import，`expected_mapping_revision` 不等於目前 revision（舊的或超前的）為 409 `LIFECYCLE_CONFLICT`，`details[0]` 為 `MAPPING_REVISION_CONFLICT` 與 `/expected_mapping_revision`，不覆寫、不合併、不 rebase；`internal/storage/imports.SaveMapping` 的 UPDATE 也以 revision 與狀態為條件；0014 只接受下一個 revision。並行測試：8 個同 revision、不同 key 的存檔只有一個成功、其餘 409 且不留 job 或 audit；8 個同 key 的 retry 只有一個 revision、答案相同；降級後 replay 為 403（`TestMappingRevisionConflicts`，DB-backed，NOT RUN） |
| 2b. 全量 rows validation，以 job 執行 | 已完成 | `import_validate` job（`internal/import/validator`，只有 cmd/worker 連結）：在 transaction 外以 T-016 的有界掃描器讀整個 upload（核對大小與 SHA-256），每列 `Apply`，全部讀完才決定結果；全部合法時再讀一次，以 pipe 把 canonical dataset 串流寫入 object storage（不在記憶體中持有檔案或 dataset），大小必須等於第一次讀的預測。結果與 job 的 completed 同一 transaction（`handler.Run.Finish`）。純粹部分：`TestAValidLog`、`TestInvalidRows`、`TestJSONEvents`、`TestBounds`、`TestTheFileIsTheProfiled`、`FuzzValidate`；整條路徑：`TestImportMappingClosedLoop`、`TestJSONEventsMapping`（DB-backed，NOT RUN） |
| 2c. Stale-job fence | 已完成 | 三層：存檔時取消其他 revision 仍 queued 的 job；job 只在 import 仍是 VALIDATING 且 revision 相同時才 validate 與寫入，否則 completed 且 audit outcome 為 `stale`；0014 的 deferred constraint trigger 要求離開 VALIDATING 時目前 revision 的 job 在同一 transaction completed，`follow_import_validate_job` 讓其他 revision 的 job 怎麼結束都不影響 import。判斷看 job 與 import 的狀態，不看 `pg_trigger_depth()`（`import_mapping.sql` 以 projects 上的 probe trigger 證明其他 trigger 不能冒充）。`TestStaleValidationJobs`：queued 的舊 job 被取消、從未執行；running 的舊 job 在讀 import 之前與 validate 完所有列之後被新的存檔蓋過，都不寫入（後者原本會是 VALIDATED，import 卻記錄新 revision 的 AWAITING_MAPPING）（DB-backed，NOT RUN） |
| 3a. 低 confidence 要由人確認，AI 從不確認 | 已完成（第 1 輪 M1、M2 修正：原本只信任儲存的 `requires_confirmation` 旗標，也不要求確認 AI 建議不匯入的欄；現在由 server 判定，見最後一節） | D-070 (d)：import 有 ready 的 AI suggestion 時，mapping 中與 `requires_confirmation` 條目完全相同的每一條，都要在 `confirmed_low_confidence` 由存檔的人列出，否則 422 `MAPPING_CONFIRMATION_REQUIRED`（path 指向該條）；列出其他名稱為 `MAPPING_CONFIRMATION_UNKNOWN`；人改成其他 target 的欄不需確認；`confirmed` 只能是 `true`。AI 沒有寫 mapping 的路徑：worker 沒有 mapping 欄位的 grant（`import_mapping.sql`），schema 拒絕帶 confidence、evidence、requires_confirmation 的 mapping（fixture `invalid-import-mapping-ai-member`、`invalid-save-mapping-ai-suggestion`），suggestion 不會變成 mapping（`TestLowConfidenceNeedsAPerson`、`TestLowConfidenceSuggestion`）。T-027 尚未產生 suggestion，DB-backed test 以 owner 寫入的 suggestion 驗證 |
| 3b. Invalid row 報表與重新 mapping | 已完成 | 有不合法的列時 import 為 AWAITING_MAPPING，帶 `validation.valid=false`、issues（`/<列>/<欄名>`，至多 100）、`invalid_rows`、canonical preview（不合法的列為 null）與 error report（`validation-errors-r<rev>-a<attempt>.csv`，T-016 的格式與 formula 中和，value 欄一律空白）；同一個 import 可從看到的 revision 重新 mapping，舊的報表與結果隨新 revision 清空（`TestImportMappingClosedLoop`：第一次以有一列不是 RFC 3339 的欄 mapping，第二次改用 UTC 欄後 VALIDATED） |
| 4. D-059 的 target field | 已決定並實作 | 見 scope 1；`aicontext.TargetFields` 改為 14 個，`TestMappingVocabularyIsTheSchemas` 以 schema 對照，AI 測試（含 anthropic 投影）通過 |
| 回給 client 的列不回顯公式或 secret | 已完成 | Validation 的 issue message 是固定句「<field>: …」，不引用值（`FuzzApply` 確認任何輸入的 message 都在固定句集合中；它的另一半「沒有 issue 的列是 schema-valid 的 event」在第 1 輪 M4 之前從未執行，見最後一節）；error report 的 value 欄一律空白，欄名經 `report.Safe`（`TestInvalidRows`；整合測試解析下載的 CSV、逐 cell 確認，並確認報表中沒有那一列的值）；`import.map`、`import.validate` audit 只有數量與 code（整合測試檢查不含序號、欄名、檔名）。Canonical preview 是 JSON 資料，與 T-016 的 raw preview 一樣帶原始文字（像公式的序號照原樣保留），由 UI 以文字顯示（SECURITY §6；見殘餘風險） |
| Typed error code、JSON Pointer、request_id | 已完成 | 409／422 的每個 detail 有 code 與 body 中的 JSON Pointer（`TestMappingRefusals` 的 20 個 body case、8 個 transport／caller case、3 個狀態 case）；validation issue 的 path 是檔案中的 cell；FAILED 的 `error.request_id` 是該 revision 存檔的 request（`TestValidationJobFailures`）；每個回應有 `X-Request-ID` 並與 body 相同（`code()` helper） |
| 每個新查詢都限定 tenant | 已完成 | `internal/storage/imports/mapping.go` 每個 statement 都 `organization_id=$1`（另有 RLS）；`Get`／`Lock` 新的 LEFT JOIN 也以 organization 與 import 連接 |
| 每個寫入：role＋audit＋concurrency＋idempotency | 已完成 | 存檔：editor 以上（在 transaction 中查）、`Idempotency-Key`（fingerprint 為 import ID 與 body 的 canonical JSON；CB-18）、CAS、`import.map` audit 與 import、job、idempotency response 同一 transaction。Worker：lease 下的 Checkpoint 與 Finish，`import.validate` audit 與 job 的結束同一 transaction |
| 新 API 更新 OpenAPI＋範例＋TS client 契約測試 | 已完成 | `saveImportMapping` 的描述、request／202／409／422 範例（`check_contracts.py` 依 schema 驗證）；`make ts-contracts` 重產；`contracts.test.ts` 以 fixture 比對 `ImportMapping`、`SaveMapping`、`Import`（VALIDATED、canonical preview）並有 5 個型別反例；Go 的 `TestContractParity` 涵蓋 `canonical_preview`、`TestRoutesMatchContract` 涵蓋新 route |
| Context cancellation | 已完成 | `Scan`、`Validate`、`Write` 每列檢查 context（`TestTheFileIsTheProfiled`）；validator 在 context 結束時回 context 的錯誤，不記成 storage failure |
| Go maps 不決定順序 | 已完成 | Issues 依 plan 順序、列的 issue 最後；metadata 依 mapping 順序；dataset 依列順序；`Check` 的 issue 依 entry 順序（`TestApplyAll`、`TestMetadata`） |

## 實際執行結果

在 worktree 執行（`t017` 分支）。HEAD、版本見 `environment.txt`：Go 1.27.2、Node 22.22.0、Python 3.13.16（`.venv`）、4 CPU；沒有 `GEMINUS_TEST_DATABASE_URL`，所以 DB-backed 的 Go 測試在 `make backend-test` 中以 repo 既有的 `dbtest` 機制 skip 並印出 NOT RUN（不是本任務新增的 skip）。

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check PYTHON=.venv/bin/python` | 0 | `spec-check.txt`：14 個 metaschema、58 個正例、116 個反例（111 個有精確的 error path；T-017 加了 1 個 schema、6 個正例、15 個反例）、42 個 OpenAPI operation、task DAG、decisions 的形狀（D-070–D-072 為 proposed、沒有 approver）、TS 契約已是最新、DB test 已依 template 重產；重產的 `checks/decisions-check.json`、`checks/planning-check.json` 已 commit（T-017e） |
| `make backend-test` | 0 | gofmt、go vet、60 個 package ok（12 個沒有測試檔），沒有 cached 結果；DB-backed 測試 skip（NOT RUN） |
| `make backend-race` | 0 | 60 個 package ok（`-race`），沒有 cached 結果 |
| `make architecture-check` | 0 | 72 packages、15 rules、16 build configurations |
| `make ai-eval` | 0 | fake provider 的 context 與 review suite；只是 fake 的結果，`checks/ai-evals/*.json` 沒有變化 |
| `make ts-contracts PYTHON=.venv/bin/python` | 0 | 15 個檔案、106 個型別；重產後與 commit 的內容相同（`git status` 沒有變化） |
| `make frontend-test` | 0 | lint、typecheck、18 個檔案 425 個 test（含 `contracts.test.ts` 的 mapping 正反例）；Node 22.22.0（見 NOT RUN） |
| `go test -count=1 -v ./internal/import/... ./internal/ai/aicontext/ ./cmd/worker/` | 0 | `t017-go-tests.txt`：92 個 top-level test、204 個 subtest 通過；`cmd/worker` 的 `TestRunWorksAndStops` 需要 DB，skip（NOT RUN） |
| `go test -fuzz` 三個 target，各 60 秒 | 0 | 見下一節 |
| targeted mutation（17 個，`go test -overlay`） | 0 | 17 個都被擋下，見下一節 |
| pglast 靜態解析與 `bash -n scripts/run_db_tests.sh` | 0 | `sql-parse.txt`；只是語法，不是 DB 執行 |
| tasks.json 的 verification command `go test ./internal/import/...` | 0 | 包含在 `backend-test`、`backend-race` 與上面的 verbose run 中 |
| tasks.json 的 verification command `make integration-test` | NOT RUN | 沒有一次性 DB（依指示不嘗試），見 NOT RUN |

## 測試涵蓋

| 測試 | 驗證 | 驗收 |
|---|---|---|
| `TestVocabularyIsTheSchemas`（mapping） | target field、transform、必要欄位與字元上限都與 `import-mapping`、`canonical-event` schema 相同；每個 field 只接受自己的 transform（以 JSON Schema validator 驗證每個組合） | D-070 (a)(b) |
| `TestCheck`、`TestCompile`（mapping） | 未知欄、重複欄、重複 field、metadata 可多欄、缺 case_id／event_type／timestamp、錯的 transform、不匯入的欄非 identity、未知 target、metadata 超過 64 欄：code 與 body 中的 pointer，message 不引用欄名；Compile 拒絕 Check 不接受的 mapping | TEST-012、D-070 (c)(e) |
| `TestLowConfidenceNeedsAPerson`（mapping） | ready suggestion 的低信心條目要逐欄確認、確認其他名稱被拒、人改過的條目不需確認、沒有 suggestion 時不能確認任何東西 | D-070 (d) |
| `TestTransforms`、`TestMetadata`、`TestRowRules`、`TestApplyAll`（mapping） | 四種 transform 的接受與拒絕（offset、大小寫、前導 0、2^53−1、JSON 數字與布林）、字元上限以 code point 計、空值；metadata 的順序、16 KiB 與 8 層的邊界；end 早於 start、duration 與 end−start 差 1 ms 的邊界（含跨 offset 與超出 `time.Duration` 的範圍）；一列的每個問題依 plan 順序回報 | TEST-013、D-072 (a)(b)(c) |
| `FuzzApply` | 任何 cell 都不 panic；issue 的 message 只來自固定句；沒有 issue 的列是 schema-valid 的 canonical event——**更正（第 1 輪 M4）**：原本每個欄位依 `i%3` 共用三個輸入，start 與 rework 拿到同一個輸入，沒有任何一列能 valid，這一半從未執行；第 1 輪改為每個欄位一個 cell，並以必須 valid 的 seed 確認它確實執行 | TEST-013、SECURITY |
| `TestAValidLog`、`TestInvalidRows`、`TestJSONEvents`、`TestBounds`、`TestTheFileIsTheProfiled`（validation） | 合法的 station log：dataset 是依列順序的 schema-valid event、大小與預測相同、逐 byte 讀也相同、像公式的序號是資料；不合法的列：每個 cell 的 pointer、行號、固定訊息、沒有值、preview 為 null、Write 拒絕；JSON events；issues、report、preview、dataset 的上限；欄位變了為 `ErrChanged`、壞掉的檔案為 profile 的 failure、context 結束 | TEST-012、TEST-013、D-072 |
| `FuzzValidate` | 任何接在 station header 之後的列：要嘛與 profile 一樣失敗，要嘛每列都有交代（invalid 不多於列數、上限內、issue 都在檔案中的列）；合法時 Write 寫出的大小等於預測、每個元素都是 canonical event | TEST-013 |
| `TestScanVisitsEveryRow`、`TestScanRefusals`、`TestShapesAreTheInferences`、`TestPreviewJSON`（profile） | `Scan` 與 `Read` 讀到的完全相同並逐列交出；失敗與 `Read` 相同；profile 的推斷型別恰是 transform 接受的形狀；preview 的截斷 | T-016 不變 |
| `TestNew`、`TestAJobThatNamesNoImport`、`TestValidationIsTheSchemas`（validator） | 設定的檢查；payload 不合（9 種）時不重試、不開任何 transaction；記錄的 validation 符合 `common.schema.json#/$defs/Validation` | ADR-024 |
| `TestRoutesMatchContract`、`TestContractParity`（upload，修改）；`TestTheBuildRunsSimulations`（cmd/worker，修改） | route 與 `api-operations.json`；`Import` 與 schema 的成員；worker 登錄四個 handler（含 validator） | D-012 |
| `TestMappingVocabularyIsTheSchemas`（aicontext） | AI context 的 target field 與 schema 相同，含 D-059 的三欄 | D-070 (a) |
| `scripts/check-architecture`：5 個新 probe | mapping 讀 tenant、validation 讀 object storage、validation 開檔案（`import-parsing-is-pure`）；cmd/api 與 upload service 連結 validator（`job-handlers-only-in-the-worker`） | ADR-024 |
| `contracts.test.ts`、`check_contracts.py` 的 21 個新 fixture | 人工 mapping、SaveMapping、station mapping、VALIDATING／VALIDATED／重新 mapping 的 import 的正例；timestamp 用 identity、不匯入的欄有 transform、未知 target、expression、AI 成員、空 mapping、未確認、AI suggestion 當 SaveMapping、VALIDATING 帶 validation 或 preview、VALIDATED 有 invalid row、不 valid、沒有 mapping、AI mapping 當 mapping、preview 中的未知成員的反例（每個都有精確的 error path） | TEST-012 |
| **`db/tests/import_mapping.sql`（新，NOT RUN）** | 見 db/README：只存成下一個 revision、只在已 profile 且非終態時、同 transaction 必有該 revision 的 job（deferred）；每 revision 一個 job；worker 不能 mapping；stale-job fence；import 跟隨目前 revision 的 job 到 FAILED；VALIDATED 與 AWAITING_MAPPING 的內容；VALIDATED 不回 AWAITING_MAPPING；重新 mapping；API 不寫 validation；其他 trigger 不能冒充；trigger 關掉時 CHECK 仍成立；canonical key 的 D-035 規則；worker 的欄位 grant | TEST-012、TEST-013、D-071 |
| **`imports.sql`、`roles.sql`（修改，NOT RUN）** | 原本 API 寫 placeholder mapping 的地方改為完整的存檔（VALIDATING、清空 report、同 transaction 的 job），斷言不變 | D-037 |
| **`internal/api/import_mapping_test.go`（新，DB-backed，NOT RUN）** | `TestImportMappingClosedLoop`、`TestMappingRevisionConflicts`、`TestStaleValidationJobs`、`TestValidationJobFailures`、`TestMappingRefusals`、`TestLowConfidenceSuggestion`、`TestJSONEventsMapping`（內容見 db/README 與上面各 scope） | REQ-007、TEST-012、TEST-013、D-009、D-011 |

### 先紅後綠、fuzz 與 mutation

- 契約：`TestContractParity` 先在 schema 加了 `canonical_preview` 而 Go 尚未加時失敗（`red-contract-parity.txt`），加上後通過。
- `internal/import/mapping`、`internal/import/validation` 的測試先於實作：以只有型別與空函式的 stub 執行，全部失敗（`red-mapping.txt` 153 行 failure、`red-validation.txt`），實作後通過。
- Architecture：以 0ee3d2a 的 `rules.go` 執行 5 個新 probe 全部失敗（`red-architecture.txt`），新規則下通過。
- SQL：沒有 DB，無法先紅後綠（NOT RUN）；只做了 pglast 的靜態語法解析（`sql-parse.txt`：0014 up／down、`import_mapping.sql`、`imports.sql`、`roles.sql`、`constraints.sql` 全部可解析，PL/pgSQL 的 function body 也解析；`must_fail` 字串中的 statement 不解析；沒有執行任何東西）與 `bash -n scripts/run_db_tests.sh`。
- Fuzz（各 60 秒，`fuzz-*.txt`）：`FuzzApply` 951,761 次、`FuzzValidate` 821,416 次、`FuzzRead`（T-016 的 profile，因 `Read` 改為與 `Scan` 共用而重跑）112,180 次，都沒有 crash 或違反不變式。
- Targeted mutation（`mutations.txt`、`mutations-harness.py`）：每個 mutation 是一行原始碼的改動，以 `go test -overlay` 套用，worktree 從未被修改。17 個全部被擋下：duration 的容許誤差改為 ±1 ms（`TestRowRules`）、不檢查 end 早於 start（`TestRowRules`）、接受同一 field 兩欄（`TestCheck`）、低信心條目不需確認與確認任何名稱都接受（`TestLowConfidenceNeedsAPerson`）、integer_ms 接受前導 0（`TestTransforms`）、不檢查 transform（`TestCheck`、`TestCompile`）、必要欄位可空（`TestTransforms`、`TestApplyAll`）、metadata 欄數上限差一（`TestCheck`）、metadata bytes 晚一 byte（`TestMetadata`）、issues 不設上限與 dataset 不設上限（`TestBounds`）、CSV 欄位數不符的列當作合法（`TestInvalidRows`）、欄位改變不偵測（`TestTheFileIsTheProfiled`）、revision 0 與其他 import 的 job 照跑（`TestAJobThatNamesNoImport`）、cmd/api 可連結 validator（`TestProbesAreCaught`）。
- Service、storage 與 HTTP adapter 的行為（CAS、idempotency、stale-job fence、FAILED 的對應）只由 DB-backed 測試涵蓋，沒有執行，也沒有 mutation（NOT RUN）。

## NOT RUN

依指示沒有嘗試建立或連線 DB；以下每一項都要由 coordinator 在新的 `dt_test_*` 資料庫執行（`make integration-test`，即 `make db-test` 加 `make db-go-test`）：

- **Migration**：0014 up、down、再 up（`scripts/run_db_tests.sh` 兩輪；0013 的位置以註解標出，0013 由 T-026 提供，integrate 時由 coordinator 排序）；0014 的 down 是否完整還原 0010 的 `protect_import`（文字已逐字比對，未執行）。
- **SQL suite**：`db/tests/import_mapping.sql`（新）、`db/tests/imports.sql`（修改）、`db/tests/roles.sql`（修改）、`db/tests/constraints.sql`（只改註解，但 0014 之後它的 `import_validate` job 生命週期要重跑），以及其餘既有 suite 在 0014 之下的回歸：`invariants.sql`、`runs.sql`、`proposals.sql`、`proposal_jobs.sql`、`self.sql`、`ir_rows.sql`、`pool_rows.sql`。
- **DB-backed Go 測試（`internal/api`，新）**：`TestImportMappingClosedLoop`、`TestMappingRevisionConflicts`、`TestStaleValidationJobs`、`TestValidationJobFailures`、`TestMappingRefusals`、`TestLowConfidenceSuggestion`、`TestJSONEventsMapping`。
- **DB-backed Go 測試（既有，受影響）**：T-016 的 `TestImportClosedLoop`、`TestImportRoleMatrix`、`TestUploadRefusals`、`TestUploadIdempotency`、`TestProfileFailures`、`TestProfileIntegrity`、`TestProfileJobFailures`、`TestErrorReportAuthorization`、`TestASlowUploadHasItsOwnDeadline`（`Get`／`Lock` 多了一個 LEFT JOIN、FAILED 的 envelope 訊息改了）；`internal/worker` 與 `internal/storage/jobs` 中以 import 為 resource 的測試（0014 的新 trigger 掛在 jobs 上）；`cmd/worker` 的 DB 測試。
- `make integration-test` 整體、`make e2e-test`（UI 尚無 mapping 畫面，T-031）。
- 全面的 mutation harness：只跑了上面的 targeted mutation；SQL 沒有 mutation（沒有 DB）。
- 效能：50 MB／100k rows 的 validation（讀兩次）沒有 benchmark（T-037）。
- 正式的 object storage adapter（D-044）。
- `frontend-test` 以 Node 22.22.0 執行；`apps/web/package.json` 要求 ≥24.21.0（`npm ci --engine-strict=false`，lockfile 未改）。

## 需要 owner 或 reviewer 決定

- **D-070（PROPOSED）**：(a) D-059 的三欄現在就成為 target field；(b) transform 由 field 決定、沒有參數；**CB-28 的常數與單位換算 transform 是否另立 decision**（建議：T-052 前依 D-059 的詞彙定義有列舉參數的 `constant`）；(c) 重複欄與 field、必要欄位、metadata 64 欄；(d) 低信心建議逐欄確認（建議 A；B 只要 `confirmed`；C 一律改寫）；(e) 存檔被拒的 code。
- **D-071（PROPOSED）**：CAS 與 409 的 code、`VALIDATING`、存檔需要 Idempotency-Key（CB-18）、stale-job fence、結果與 FAILED 的對應、**canonical dataset 由 validation 產生**（建議 A；B 在 commit 時才 mapping）、JSON model 的 mapping 為 422（validation 屬 T-018）、COMMITTED 由 T-018 定義。
- **D-072（PROPOSED）**：值與列的規則、timestamp 照原文保留 offset（B：轉 UTC）、報表不帶 value（C：帶 value）、各項上限。
- **CB-43**：mapping 的 code 不在封閉的 ErrorCode／ValidationCode 目錄（UI 只能顯示英文 message）；T-018 的交接；孤兒 object；實作參數；operator ID 可被放進 metadata。

## 殘餘風險

- **沒有在 DB 上執行過**：0014、SQL suite 與 DB-backed Go 測試都只經過編譯、靜態解析與推理；trigger 之間的互動（`protect_import`、兩個 deferred constraint trigger、`follow_import_validate_job`、0010 的 `follow_import_profile_job`）與 RLS 下的實際行為要等 coordinator 的 `make integration-test` 才算驗證。
- Canonical preview 與 raw preview（T-016）一樣帶原始的 cell 文字（像公式或像 secret 的值都照原樣），是給 UI 以文字顯示的 JSON；UI（T-031）必須 escape，不得放進試算表或 markdown。
- 孤兒 object：stale job 的 dataset、舊 revision 的報表與 dataset、重試前 attempt 的 object 都不再被任何 row 指向，沒有 maintenance（D-044，CB-43 (c)）；一個 import 每次存檔至多多出一份 dataset（≤256 MiB）。
- Validation 讀 upload 兩次（第二次只在全部合法時）；兩次之間檔案被換掉時第二次的 hash 檢查會失敗（FAILED），但第一次讀完到第二次讀之間沒有鎖住 object（filesystem adapter 不覆寫 key，正式 adapter 需要同樣的保證）。
- 沒有限制同時 validation 的數量或每個 tenant 的配額（T-036）；一個 editor 可以反覆存檔，每次都排一個讀整個檔案的 job（舊的 queued job 會被取消，running 的會跑完）。
- D-059 的「operator ID 不匯入」只靠詞彙沒有 operator 欄位；人可以把 operator 欄放進 metadata（CB-43 (f)）。
- JSON model import 的 validation 尚未實作（T-018）；目前它無法 mapping，也無法 VALIDATED。
- 0014 的 down 會把 VALIDATING／VALIDATED 的 import 留在 0010 不認得的狀態（down 的 CHECK 會失敗）：只適用於一次性 DB；已部署的環境不得 down（AGENTS §3）。

## Coordinator 的執行（16dec30）

Coordinator 把六個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（T-026 的 4abeb96 之後）。與 T-026 的衝突：contract fixtures、`cases.json`、`contracts.test.ts` 兩邊都保留；`run_db_tests.sh` 依序套用 0013、0014，回退時先 0014 再 0013；`render_db_tests.py` 中兩邊共用 JOB_A51–A57（每個 SQL suite 都 rollback，所以可共用）；決策登記、CONTRACT_BLOCKERS（T-026 為 K 節、T-017 改為 L 節）、API_DATABASE、db/README、Makefile 訊息與 traceability（每個需求的 task 取聯集）都保留兩邊。

之後 `spec-check`、`backend-test`、`backend-race`、`architecture-check`、`ai-eval`、`frontend-test`（429 個 test）與 `integration-test`（新的 `dt_test_t017`：0013 與 0014 的 up／down／up、所有 SQL suite 含 `import_mapping.sql`；30 個 package ok，沒有 FAIL 或 SKIP）都是 exit 0。上方 NOT RUN 中的 migration 0014、SQL suite 與 DB-backed Go 測試已由 coordinator 執行並通過。Log 與 exit code 在 `checks/evidence/2026-10-20-T-017/coordinator-*`。

## Review 第 1 輪的修正（2026-10-20）

獨立 review 第 1 輪：CHANGES REQUESTED，沒有 High，5 個 Medium（M1–M5）、6 個 Low（L1–L6）與 I2、I4、I5。修正在 worktree 的分支 `t017-r1`（自整合分支的 534c9ba 起），先寫測試、跑紅燈，再修正。Reviewer 的 probe（`probe_mapping_test.go`、`probe_validation_test.go`）以 `go test -overlay` 加入，不寫進 worktree。

### Commit

| Commit | 內容 |
|---|---|
| e9a2b10 | 契約先行：M5 的 `getImport` 描述與 `validation_failed` 範例、API_DATABASE §5；M1、M2 的 `saveImportMapping` 描述與 `SaveMapping.confirmed_low_confidence`；TS 重產 |
| a726a9e | 先紅後綠的修正：M1–M4、L1–L6 的程式與測試，SQL 測試（L1、L2），DB-backed 測試的擴充 |
| 36864e4 | 文件：D-070–D-072（仍為 PROPOSED，只改這三列）、SECURITY §6（I5、M4）、API_DATABASE、AI_SPEC §8、TWIN_IR_SPEC §7、CB-43、db/README |
| e9ff0ed | R21 的獨立測試、mutation 結果、報告的 commit 表（I4）與 FuzzApply 的更正（M4） |
| f83c409 | e9a2b10 在 OpenAPI 的單引號字串中留了未跳脫的 `'`，`make spec-check` 因此失敗（`r1-spec-check-first-run.txt`）；修正 |
| 本報告（下一個 commit） | 本節、evidence、`make spec-check` 重產的 `checks/*.json` |

### 逐條

| Finding | 修正 | 證據 |
|---|---|---|
| M1 低信心確認只信任 AI 自己的旗標 | `SuggestedEntry.NeedsPerson()`：confidence < 0.80、target 為 null 或旗標，由 server 判定，旗標只能增加確認；`internal/import/upload` 每次讀取儲存的 suggestion 都先以 `ai-mapping.schema.json` 驗證（ready 卻沒有 suggestion 也算），不符時存檔 fail closed：500 `INTERNAL_ERROR`、`details[0].code` `MAPPING_SUGGESTION_INVALID`，什麼都不寫；D-070 (d) 改寫 | `TestTheServerDecidesWhatNeedsAPerson`（0.1 與 0.7999 沒有旗標仍要確認、0.80 不用、0.99 有旗標要、不匯入的欄一律要）、`TestStoredSuggestion`（7 種不符 schema 的 suggestion、非 ready 不讀）、`TestRefused`；reviewer 的 `TestProbeLowConfidenceWithoutTheFlag` 由紅轉綠；DB-backed `TestTheServerJudgesTheSuggestion`（500、沒有任何 row，NOT RUN） |
| M2 target 為 null 的建議不需確認、也無法確認 | AI 建議不匯入的欄（需要人確認時），mapping 也不匯入時——寫成 null 的條目（issue 指向該條）或根本沒列（issue 指向 `/mapping/mappings`）——要由人列在 `confirmed_low_confidence`，列了就通過；人匯入該欄則不需要、也不能確認。**測試的更正（不是降低斷言）**：`TestLowConfidenceNeedsAPerson` 原本的 case「a column the person did not import」釘住的是 AI_SPEC §8 禁止的行為（0.2 confidence 的「不匯入」不需確認、確認它反而 422），改為要求確認，並新增「人匯入了 AI 建議不匯入的欄」「沒列的欄」「沒列的欄以名稱確認」的 case；其他 case 因同一條 null 建議而多了一個需要確認的欄，期望值一併更新 | `TestLowConfidenceNeedsAPerson`、`TestTheServerDecidesWhatNeedsAPerson`；reviewer 的 `TestProbeNullSuggestionNeedsConfirmation` 由紅轉綠；DB-backed `TestTheServerJudgesTheSuggestion`（NOT RUN） |
| M3 `+24:00`、`+05:60` 的 event 進入 VALIDATED 的 dataset | profile 的 timestamp pattern 的 offset 改為 `([01][0-9]|2[0-3]):[0-5][0-9]`，與 canonical-event schema 的 date-time 相同（rfc3339 transform 沿用同一個 `IsTimestamp`，三者一致）；每個 event 在可能成為 dataset 的一部分之前，最後以 `canonical-event.schema.json` 驗證：不符為 invalid row（`EVENT_NOT_CANONICAL`，path 指向整列），Write 拒絕檔案 | `TestTransforms`（+23:59、−23:59、+00:00 接受；+24:00、−24:00、+05:60、+99:00 拒絕，start 與 end 皆然）、`TestInference`、`TestShapesAreTheInferences`、`TestOffsets`、`TestTheSchemaIsTheLastWord`、`TestEveryEventGoesThroughTheSchema`（以只拒絕某一列的 schema 證明每列都經過它）、兩個 fuzz 的 corpus；reviewer 的 `TestProbeOffset2400` 由紅轉綠 |
| M4 FuzzApply 的正向不變式從未執行 | 每個 plan entry 有自己的 cell（以 `\x1f` 分隔的一列），seed 從三列必須 valid 的列開始，fuzz 開始前先確認它們沒有 issue（否則 `f.Fatal`），所以 schema 的檢查確實執行；SECURITY §6 與本報告前面的說法已更正 | `FuzzApply` 的 seed 檢查；reviewer 的 `TestProbeFuzzApplyNeverReachesAValidRow` 仍通過（它證明的是舊的 `i%3` 設計，與新的 fuzz 無關） |
| M5 OpenAPI 與 API_DATABASE 說 `error.request_id` 是上傳的 | 契約先行：`getImport` 改為「失敗那一步的 request」，新增 `validation_failed` 範例（revision 2 的 validation 讀不到 object storage，request_id 為該次存檔）；API_DATABASE §5 同步 | `check_contracts.py`（範例依 schema 驗證）；DB-backed `TestValidationJobFailures` 早已斷言存檔的 request（coordinator 於 16dec30 執行通過） |
| L1 0014 允許 VALIDATING／AWAITING_MAPPING → COMMITTED | `protect_import` 對所有 role：COMMITTED 只能從 VALIDATED 來（0014 尚未部署，直接修改）；`roles.sql` 的 commit 改為先由 worker 以其 job 寫成 VALIDATED | `import_mapping.sql` 新增 API 與 owner 的 4 個反例（55000）、`roles.sql`（NOT RUN） |
| L2 存檔的 deferred check 不看 job 的狀態 | `check_import_validating_with_its_job` 改為重讀 import：仍是 VALIDATING 時，目前 revision 必須有 queued 或 running 的 job（同一個 transaction 中多次存檔以最後一次判斷） | `import_mapping.sql`：在存檔之前插入並取消的 job 不算數（commit 時 55000）（NOT RUN） |
| L3 `IMPORT_INPUT_INVALID` 顯示為 `REQUEST_VALIDATION_FAILED` | 依 CB-39(a) 改為 `INTERNAL_ERROR`（它表示 server 讀不懂自己存的東西） | `TestEnvelope`（新增 `IMPORT_INPUT_INVALID`、`UPLOAD_CHANGED`、`JOB_CANCELLED`、`OBJECT_STORAGE_UNAVAILABLE`，每個 case 也檢查 message） |
| L4 未被測試涵蓋的保證 | 新增測試：pointer 的 `~0`／`~1`（`TestPointersEscapeColumnNames`）、preview 的 byte 邊界（`TestPreviewByteBoundary`：恰好、少一 byte、放不下第一列）、Write 的逐列重查（`TestWriteRefusesARowThatDoesNotMap`；schema 檢查加入後它也會擋下，所以另以 `TestWriteMapsEveryRowAgain` 把 schema 換成全部接受來單獨要求重查）、validator 的 preview 上限與列數比對（`internal/import/validator` 的新測試直接呼叫 `validate`，用 filesystem object store：`TestTheCanonicalPreviewFollowsTheRawPreview`、`TestTheUploadIsTheProfiled`）、envelope 的「could not be processed」（`TestEnvelope`） | reviewer 存活的 R17、R19、R21、R24、R26、R27 在新的 mutation 中全部被擋下 |
| L5 stale 但仍在跑的 job 會寫出至多 256 MiB 的孤兒 dataset | 第一次讀完、全部合法時，於 lease 下（`Checkpoint`）再確認 import 仍是 VALIDATING 且 revision 相同，否則停止、不寫 dataset，Finish 記 `stale` | `TestTheSecondReadIsForTheCurrentRevisionOnly`（stale 時沒有任何 object、checkpoint 失敗時回其錯誤、有不合法列時不做第二次讀取也不 checkpoint）、`TestAValidImport`；DB-backed `TestStaleValidationJobs` 新增「第一次讀完、第二次讀取前被蓋過」的情況（不留下該 revision 的 object，NOT RUN） |
| L6 `-00:00` 被當成 UTC | 建議拒絕（RFC 3339 §4.3：offset 不明；D-072 「不假設時區」），已實作：profile 推斷為 string，rfc3339 transform 拒絕；列入 D-072 (a) 由 owner 決定（選項 B：照 schema 接受） | `TestTransforms`、`TestInference`、`TestShapesAreTheInferences`、`TestOffsets` |
| I2 D-072 (b) 與 TWIN_IR_SPEC §7 的說法不同 | 不改行為；D-072 (b) 列出選項：A（建議，現狀）invalid row；B 改為 warning，留給 inference 的 data-quality；TWIN_IR_SPEC §7 註明此差異 | 文件 |
| I4 commit 表用的是 cherry-pick 前的 hash | 本報告的 commit 表改列分支 hash（22ee1aa、539f48e、c2ec688、e1da99c、8acfe37、16dec30、534c9ba），worktree 的 hash 並列 | 本報告 |
| I5 canonical preview 與 dataset 帶原始 cell 文字 | SECURITY §6：T-031 只能以文字顯示，T-027 的 AI context 必須遮蔽並標為 UNTRUSTED_DATA | 文件 |

### 實際執行結果（第 1 輪）

在 worktree 的 `t017-r1` 執行，HEAD f83c409（`r1-exit-codes.txt`），Go 1.27.2、Node 22.22.0、4 CPU；同一台機器上另有其他 agent 的 `go test` 同時在跑。沒有 `GEMINUS_TEST_DATABASE_URL`，DB-backed 測試以 repo 既有的 `dbtest` 機制 skip 並印出 NOT RUN。

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check PYTHON=.venv/bin/python` | 0 | `r1-spec-check.txt`；第 1 輪第一次執行時因 e9a2b10 的 YAML 引號而失敗（exit 2，`r1-spec-check-first-run.txt`），f83c409 修正後通過 |
| `make backend-test` | 0 | gofmt、go vet、61 個 package ok；其中 46 個是 Go 的 test cache（輸入與先前相同的 package），沒有 FAIL |
| `make backend-race` | 0 | 61 個 package ok，沒有 cached 結果 |
| `make architecture-check` | 0 | 74 packages、16 rules、16 build configurations（`validation` 新 import 的 `internal/model/validate` 在 kernel 的允許範圍內） |
| `make ai-eval` | 0 | 只有 fake provider 的結果 |
| `make ts-contracts PYTHON=.venv/bin/python` | 0 | 重產後與 commit 的內容相同 |
| `make frontend-test` | 0 | 429 個 test；Node 22.22.0（`package.json` 要求 ≥24.21.0） |
| `go test -fuzz` 三個 target，各 60 秒 | 0 | `FuzzApply` 1,135,413 次（新設計，seed 必須 valid）、`FuzzValidate` 1,046,555 次、`FuzzRead` 71,002 次（每次執行都多了 schema 的檢查或與其他 agent 共用 CPU，次數較少），都沒有 crash |
| reviewer 的 probe（`-overlay`） | 0 | `r1-probe-mapping.txt`、`r1-probe-validation.txt`：8 個全部通過（第 0 輪時 `TestProbeLowConfidenceWithoutTheFlag`、`TestProbeNullSuggestionNeedsConfirmation`、`TestProbeOffset2400` 失敗） |
| `go test -count=1 -v ./internal/import/... ./internal/ai/aicontext/ ./cmd/worker/` | 0 | `r1-go-tests.txt`：106 個 top-level test、221 個 subtest 通過；`TestRunWorksAndStops` 需要 DB（NOT RUN） |
| 34 個 targeted mutation | 0 | 34/34 被擋下（`r1-mutations.txt`） |
| pglast 靜態解析與 `bash -n scripts/run_db_tests.sh` | 0 | `r1-sql-parse.txt`；只是語法 |
| `make integration-test` | NOT RUN | 沒有 DB（依指示不嘗試），見下方 NOT RUN |

### 先紅後綠與 mutation

- 紅燈：`r1-red.txt`（mapping：`TestLowConfidenceNeedsAPerson`、`TestTheServerDecidesWhatNeedsAPerson`、`TestTransforms`、`FuzzApply` 的 seed；profile：`TestInference`、`TestShapesAreTheInferences`；validator 與 upload 的新測試因新程式尚不存在而無法編譯）、`r1-red-validation.txt`（`TestOffsets` 與 `FuzzValidate` 的 seed；需要新程式才能編譯的 internal test 以 `-overlay` 換成空檔）；reviewer 的 probe 在第 0 輪的紅燈見 reviewer 的 log，修正後 `r1-probe-*.txt` 全部通過。L4 的測試多半是針對既有行為（第 0 輪就是對的，只是沒有測試），所以它們不會先紅；它們擋不擋得下缺陷以 mutation 證明。
- Mutation（`r1-mutations.txt`、`r1-mutations-harness.py`，`go test -overlay`，worktree 從未被修改）：34 個——第 0 輪的 17 個（M4 依新的程式重新定位）、reviewer 存活的 R17、R19、R21、R24、R26、R27，與第 1 輪的 N1–N11（confidence 與 null target 不再由 server 判定、沒列的欄不需確認、null 條目不算採用、offset 超過 23:59、`-00:00`、Validate 與 Write 不查 schema、不驗證儲存的 suggestion、`IMPORT_INPUT_INVALID` 的對應、第二次讀取前不 checkpoint）。第一次執行 33 個被擋、R21 存活（schema 檢查也擋下了有 issue 的列的零值 event，所以拿掉 Write 自己的重查看不出差別），加了 `TestWriteMapsEveryRowAgain` 後 34/34。
- SQL：沒有 DB，無法先紅後綠；`sql-parse.txt` 的 pglast 靜態解析在第 1 輪重跑（`r1-sql-parse.txt`）。
- 仍只由 DB-backed 測試涵蓋、沒有 mutation 的：存檔的 CAS、idempotency、stale-job fence 在 Finish 中的判斷（reviewer 的 R25、R28–R30），coordinator 的 integration run 會執行這些測試。

### NOT RUN（第 1 輪；全部要由 coordinator 在新的 `dt_test_*` 資料庫以 `make integration-test` 執行）

- Migration 0014（up、down、up，兩輪；0013 之後）：本輪改了 `protect_import`（L1）與 `check_import_validating_with_its_job`（L2）。
- SQL suite：`db/tests/import_mapping.sql`（L1 的 4 個反例、L2 的反例）、`db/tests/roles.sql`（commit 前先 VALIDATED），以及其餘全部 suite 的回歸：`imports.sql`、`constraints.sql`、`invariants.sql`、`runs.sql`、`proposals.sql`、`proposal_jobs.sql`、`proposal_decisions.sql`、`self.sql`、`ir_rows.sql`、`pool_rows.sql`。
- DB-backed Go 測試（`internal/api`）：`TestImportMappingClosedLoop`、`TestMappingRevisionConflicts`、`TestStaleValidationJobs`（新增 L5 的情況）、`TestValidationJobFailures`、`TestMappingRefusals`、`TestLowConfidenceSuggestion`、`TestJSONEventsMapping`、`TestTheServerJudgesTheSuggestion`（新）；T-016 的 import 測試（`TestImportClosedLoop`、`TestImportRoleMatrix`、`TestUploadRefusals`、`TestUploadIdempotency`、`TestProfileFailures`、`TestProfileIntegrity`、`TestProfileJobFailures`、`TestErrorReportAuthorization`、`TestASlowUploadHasItsOwnDeadline`）因 profile 的 timestamp 規則與 envelope 的對應改變而要重跑；`cmd/worker` 的 `TestRunWorksAndStops`；`internal/worker`、`internal/storage/jobs` 的 DB 測試。
- `make e2e-test`、正式 object storage adapter、T-037 的效能（第 1 輪每個 event 多一次 JSON Schema 驗證，沒有量測）。

### 需要 owner 決定（第 1 輪新增或改變）

- D-070 (d)：需要人確認的條目改由 server 判定；不匯入的欄的確認規則；儲存的 suggestion 不符 schema 時 500。
- D-072 (a)：拒絕 `-00:00`（建議，已實作）或照 schema 接受（L6）。
- D-072 (b)：end 早於 start、duration 不一致為 invalid row（建議 A，現狀）或 warning（B）（I2）。
- D-071 (g)：COMMITTED 只能從 VALIDATED 來，0014 已強制（L1）。

### 殘餘風險（第 1 輪後）

- 第 1 輪的 SQL 與 DB-backed 測試尚未在 DB 上執行。
- 每個 event 多一次 JSON Schema 驗證，100,000 列的 validation 時間沒有量測（T-037）。
- `-00:00` 與 I2 的行為是 agent 的建議，owner 若選 B 需改程式與測試。
- 在 dataset 寫完之後才被新存檔蓋過的 job，仍留下一份孤兒 dataset（L5 只擋下寫之前的情況；D-044 的 maintenance 未做）。

## Coordinator 的執行（第 1 輪，4bdec75）

第 1 輪的 commit 套到整合分支後（worktree 後來的 OpenAPI 引號修正併入第一個 commit），第一次 `integration-test` 在 `roles.sql:133` 失敗（`coordinator-r1-integration-red.log`）：L1 的修改以 `SET CONSTRAINTS ALL IMMEDIATE` 檢查 VALIDATED 的 import 有其 job，也觸發了同一個測試先前讓 proposal ready、卻沒有完成 job 的 0012 deferred check。4bdec75 改為只指名 T-017 的 `twin.import_validated_with_its_job`（與 `runs.sql` 的做法相同），要檢查的事不變。之後 `spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`frontend-test`（429 個 test）、`backend-race` 與新的 `dt_test_*` 上的 `integration-test`（30 個 package ok，沒有 FAIL 或 SKIP）都是 exit 0；第 1 輪 NOT RUN 中的 SQL 與 DB-backed 測試都已執行並通過。Log 與 exit code 在 `checks/evidence/2026-10-20-T-017/coordinator-r1-*`。

## Review 第 2 輪（2026-10-20）

獨立 review 第 2 輪（審查 4bdec75）：CHANGES REQUESTED，第 1 輪的項目都已修正；新的 N1（Medium）、N2（Low）、N3（Info），沒有 High。由 coordinator 直接修正（N1）或記入後續 task（N2、N3），先紅後綠。Evidence 在 `checks/evidence/2026-10-20-T-017-r2/`。

### 逐條

| Finding | 處理 | 證據 |
|---|---|---|
| N1 AI suggestion 中不是此 import 欄位的條目也被要求確認：AI 建議不匯入（target null）的欄，mapping 自然不會列，於是每個這種條目都要求列在 `confirmed_low_confidence`；超過 256 個（`maxItems`）時 mapping 永遠無法存檔 | `confirmations` 改為接收 `Check` 已有的 `known`（profile 的欄名），沒列的 null 條目只有在是此 import 的欄時才需要確認；這種條目也不能列出（列了是 `MAPPING_CONFIRMATION_UNKNOWN`）。D-070 (d)、OpenAPI 的 `saveImportMapping`、`SaveMapping.confirmed_low_confidence`（TS 重產）與 AI_SPEC §8 寫明；T-027 寫入 suggestion 時丟掉或拒絕這種條目（T-027 的 scope） | 新的 `TestASuggestionOfOtherColumnsAsksNothing`：一個與 300 個不屬於 import 的低信心 null 條目不產生 issue、以名稱確認為 `MAPPING_CONFIRMATION_UNKNOWN`、旁邊屬於 import 的欄仍要確認；紅燈 `n1-red.log`（4 個斷言都失敗，300 個條目產生 300 個 issue）、綠燈 `n1-green.log` |
| N2 不符 schema 的 suggestion 讓該 import 每次存 mapping 都是 500，T-017 沒有清掉它的路徑 | 不改行為（fail closed 是 D-070 (d) 的建議）；T-027 的 scope 新增：寫入 suggestion 前先以 `ai-mapping.schema.json` 驗證，不合就把 `mapping_suggestion_status` 標 failed、不存；D-070 (d) 加入「是否另需人工解除的路徑請 owner 決定」 | `planning/tasks.json`（T-027）、D-070 (d) |
| N3 每個 event 的 schema 檢查讓 `Validate` 慢約 2.2–2.5 倍（reviewer 實測 10 萬列 9.5 MB：1.66 s 對 0.66 s；49.8 MB、32 個 metadata 欄：5.9 s 對 2.7 s；`Write` 再讀一次） | 不改行為；列入 T-037 的 benchmark scope | `planning/tasks.json`（T-037） |

### 實際執行結果（第 2 輪，coordinator 在整合分支上）

| 指令 | Exit | 說明 |
|---|---|---|
| `go test -run TestASuggestionOfOtherColumnsAsksNothing ./internal/import/mapping/`（修正前） | 1 | 紅燈 `n1-red.log` |
| `go test ./internal/import/mapping/`（修正後） | 0 | `n1-green.log` |
| `make spec-check PYTHON=.venv/bin/python` | 0 | 第一次因 `http.ts` 過期而失敗（schema 描述改了），`make ts-contracts` 重產後通過；本節寫完後再跑一次（`coordinator-spec-check-final.log`） |
| `make backend-test` | 0 | 寫完本節後再跑一次（`coordinator-backend-test-final.log`） |
| `make architecture-check`、`make ai-eval`、`make frontend-test`、`make backend-race` | 0 | `coordinator-*.log` |
| `make integration-test`（新的 `dt_test_t017r2`） | 0 | 所有 migration 與 SQL suite，30 個 package ok，沒有 FAIL 或 SKIP（`coordinator-integration.log`） |

Exit code 在 `coordinator-exit-codes.txt`。沒有 NOT RUN 的新項目；`make e2e-test`、正式 object storage adapter 與 T-037 的效能仍如前述。

### 需要 owner 決定（第 2 輪新增）

- D-070 (d)：suggestion 中不是此 import 欄位的條目不需確認（N1，已實作）；不符 schema 的 suggestion 是否需要人工解除的路徑（N2，T-027 先在寫入時擋下）。

## Review 第 3 輪（2026-10-25）

獨立 review 第 3 輪（審查 6ed6b71）：**ACCEPT**，沒有 High 或 Medium。N1 已修（reviewer 的 probe `TestProbeGhostColumns` 通過，拿掉 `known` 條件與把它反過來的兩個 mutation 都被擋下）；N2、N3 的處理（T-027 與 T-037 的 scope、D-070 (d) 請 owner 決定）reviewer 同意；第 1 輪 M1–M3 的 probe 仍通過。這是對程式的判定：D-070–D-072 仍是 PROPOSED，它們的 gate 是 T-017 驗收前，所以 T-017 記為 in_progress（`planning/tasks.json` 的 completion_evidence），owner 批准後才標為 done。

| Finding | 處理 | 證據 |
|---|---|---|
| I1（Info）`confirmations` 的第一個迴圈不看欄位是否存在：mapping 列了 import 沒有的欄、且列法與需要確認的 suggestion 條目相同時，除了 `MAPPING_UNKNOWN_COLUMN` 還多一個 `MAPPING_CONFIRMATION_REQUIRED`（不會存進任何東西，只是與「不存在的欄不需確認」不一致） | Coordinator 修正：該迴圈略過不在 `known` 中的欄；以名稱確認這種欄為 `MAPPING_CONFIRMATION_UNKNOWN`。D-070 (d) 與 OpenAPI 的說法本來就涵蓋，不改 | `TestASuggestionOfOtherColumnsAsksNothing` 新增兩個斷言；紅燈 `checks/evidence/2026-10-20-T-017-r3/i1-red.log`（兩個都失敗）、綠燈 `i1-green.log` |

I1 修正後，`make spec-check`、`make backend-test`、`go test -race ./internal/import/...` 與新的 `dt_test_t017r3` 上的 `make integration-test`（所有 migration 與 SQL suite，30 個 package ok，沒有 FAIL 或 SKIP）都是 exit 0（`checks/evidence/2026-10-20-T-017-r3/coordinator-*`）。`backend-race` 的其他 package、`architecture-check`、`ai-eval` 與 `frontend-test` 未在本次重跑：本次只改 `internal/import/mapping` 的一個純函式與其測試，它們在 6ed6b71 的 tree 上已通過。
