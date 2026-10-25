# T-028 驗證報告：Scenario persistence／parameter-only approval

**日期：2026-10-25｜狀態：DB 測試與 `make integration-test` 已由 coordinator 在一次性 database `dt_test_t028` 上執行（615766c，exit 0，`checks/evidence/2026-10-25-T-028/coordinator-integration.log`、`coordinator-exit-codes.txt`，見「Coordinator 的執行」）；獨立 review 第 1 輪的修正見「Review 第 1 輪」，其中新增的 DB 測試為 NOT RUN；T-028 在 tasks.json 仍為 `not_started`（agent 不改狀態）。｜需求：REQ-010（另涵蓋 REQ-002、REQ-018、REQ-019 中 scenario 的部分）｜Gate：D-004、D-014、D-020、D-021、D-067 都已 APPROVED；本任務提出的 D-078、D-079、D-080 是 **PROPOSED**（`approved_by`／`approved_at` 為 null），依建議實作並加進 T-028 的 gate。｜驗收測試：TEST-007、TEST-008（另寫了 TEST-003、TEST-009、TEST-010 中 scenario 的部分）｜本 worktree 沒有 PostgreSQL：agent 執行時所有 DB-backed Go 測試都 SKIP、所有 SQL 都沒有執行（下列「NOT RUN」是 agent 端的狀態）；這些之後都由 coordinator 執行並通過（`checks/evidence/2026-10-25-T-028/coordinator-*`）。**

範圍依 tasks.json 與 coordinator 的指示：

1. 人建立的 Scenario 經 HTTP API 建立、讀取、列表（contract first：OpenAPI、`http.schema.json`／`scenario.schema.json`、範例、重產的 TS 契約型別與測試）；AI scenario mode 的批准 effect（T-026 已建立）由讀取與列表涵蓋，以 Approval 的 discriminant 區分；
2. scenario 是 immutable base version 加上只能改 parameter allowlist 的 patch（D-021、D-057，與 AI scenario 相同的規則）；base 必須存在且屬同一 tenant；stale／invalid 的輸入回 typed error（JSON Pointer、request_id）；role、audit、idempotency 與 tenant scope（application filter、RLS、composite FK）；
3. 斷言建立或批准 scenario 都不建立 model version、不啟動 run，scenario 不被原地修改。

**沒有任何 live AI 呼叫**，沒有讀取或設定任何 API key；AI scenario 只在 DB 測試中由 deterministic fake provider 產生（本 worktree 未執行）。逐條輸出在 `checks/evidence/2026-10-25-T-028/`：`environment.txt`、`exit-codes.txt`、各指令的 log、`db-not-run.txt`、red／green log、`mutate.py` 與 `mutants-unit*.log`、`sql_lint.py` 與 `sql-static.log`。

## Commit

Branch `t028`，從 21423aa 開出，沒有 push。

| Commit | 內容 |
|---|---|
| c080577 | 契約：OpenAPI `createManualScenario`、`listScenarios`、`getScenario` 的說明（檢查順序、allowlist、任何 version、idempotency、binding stage、不建立 version 與 run）與 201／200／409／422／503 範例；`approveProposal` 說明 Approval 的 `mode` 如何指向 effect；`scenario.schema.json`、`http.schema.json` 的說明（`source_proposal_id` 的 description 進入 TS）；10 個 fixture（4 正例、6 反例）與 cases；`valid-manual-scenario.json` 的 patch hash 是真的，由 `make spec-hashes` 與 `internal/model/hashing` 測試核對；重產的 TS；`contracts.test.ts`；TS client 的 `createScenario`、`listScenarios`、`getScenario` 與測試；TS 的 red／green log |
| 7cf96d2 | Migration 0015（up／down）；`db/tests/scenarios.sql`（template 與 render、新的 SCN ID）；`run_db_tests.sh` 在 0014 之後套用 0015（兩輪），down 時先 0015 |
| 7977a44 | `internal/scenarios`（service、純粹的 `check`、`Public`、HTTP adapter、unit 與 DB-backed 測試）；`internal/storage/scenarios` 的 `List`、`Position` 與 DB 測試；`internal/api` 的 route 與 HTTP 整合測試；architecture 規則 `scenarios-only-in-the-api` 與 7 個 probe；Makefile 的 `db-go-test` 加入 `./internal/scenarios/...`；unit 的 red／green log、mutation harness 與 log |
| 9dae2b5 | 文件：D-078–D-080 PROPOSED（DECISIONS_AND_TBD、decisions.json）、ARCHITECTURE、API_DATABASE（§4、§8、§9；§9 的安裝順序補上 0014 與 0015）、AI_SPEC §7、SECURITY §5、TWIN_IR_SPEC §6、CONTRACT_BLOCKERS CB-33、db/README；tasks.json 的 scope 說明、planned outputs 與 gate（status 不變）；traceability；重產的 `checks/*.json` |
| fb7ff84 | SQL 測試修正（`array_agg(id::text …)`，見「先紅後綠」）；`sql_lint.py` 與 `sql-static.log` |
| （本 commit） | 本報告、`environment.txt`、`exit-codes.txt`、各指令的 log、`db-not-run.txt`，重產的 `checks/docs-check.json` |

## 稽核：scope 逐條

| Scope | 判定 | 證據／處理 |
|---|---|---|
| 人工 Scenario 的 create（HTTP） | 已完成；DB 部分（coordinator 執行通過，`coordinator-integration.log`） | `POST /v1/model-versions/{id}/scenarios`：request 本身的檢查（path 400、`Idempotency-Key` 400、media type 415、body 400／413／422、只有空白的名稱 422 `/name`）→ 一個 `tenant.DB.Run`：role（editor／owner）→ Idempotency-Key（fingerprint 為 version ID 與 body 的 RFC 8785 形式）→ version（本 organization 的任何 version，D-067；不存在或其他 organization 404）→ `check` → dataset binding stage → 恰好一個 scenario、audit `scenario.create`、idempotency response，回 201 Scenario。Unit：`TestCheck*`、`TestNameIssues`、`TestFingerprint`、`TestRefused`。DB：`TestCreateManualScenario`、`TestCreateRefusals`、`TestScenarioIdempotency`、`TestScenarioDatasets`；HTTP：`TestScenarioRequestRefusals`、`TestScenarioClosedLoop` |
| read／list，兩種 scenario 都涵蓋 | 已完成；DB 部分（coordinator 執行通過，`coordinator-integration.log`） | `GET /v1/scenarios/{id}`、`GET /v1/model-versions/{id}/scenarios`：viewer 以上；人建立的 `source_proposal_id` 為 null，AI scenario 批准建立的為該 proposal；列表依 (created_at, id)、cursor 綁 organization、version 與 list；回答前把 patch 轉成 RFC 8785 並核對 `patch_hash`（不符 500）。Unit：`TestPublic`（兩種 row 對 fixture、schema parity、jsonb 的拼法、UTC、8 種壞 row 不顯示）、`TestCursor`。DB：`TestListPages`、`TestListAndGet`（storage）；HTTP：`TestScenarioClosedLoop`（批准後 `getScenario(scenario_id)` 的 patch 與 hash 就是 proposal 的、列表兩種都有、分頁） |
| AI mode=scenario | 已完成（沒有新的 AI 程式） | AI scenario 的產生、review 與批准是 T-025／T-026 的；本任務以 `TestTheAIScenarioModeTakesTheSameRules` 證明人工的 `check` 與 `review.Review`（scenario 模式）對 13 個 patch 接受與拒絕相同、拒絕的 code 相同，只有「AI 必須改變模型」不同（D-078 (b)）。tasks.json 原規劃的 `internal/ai/scenario/` 因此不需要（見「差異」） |
| approved response discriminant | 已完成 | `Approval` 是以 `mode` 區分的 oneOf（T-026）。TS：`effectOf(a)` 依 `mode` 讀 `new_version_id` 或 `scenario_id`；`@ts-expect-error` 擋下未依 `mode` 判斷就讀 `scenario_id`，以及 scenario 的 Approval 帶 `new_version_id`。OpenAPI 的 approveProposal 說明指向 getScenario／listScenarios；HTTP：`TestScenarioClosedLoop` 依 `scenario_id` 讀回 scenario |
| parameter allowlist | 已完成 | `check` 用 `patch.Scenario`（D-020 test-before-modify、D-021、D-057），與 createSimulation 的 inline patch、AI review 是同一個 kernel 函式。`TestCheck`：刪 node、加 resource、改名、改 flow 的 `to` 為 `PATCH_PATH_FORBIDDEN`；沒有 test 為 `PATCH_TEST_REQUIRED`；舊的 test 為 `PATCH_TEST_FAILED`；不存在為 `PATCH_TARGET_MISSING`；新的 dataset 為 `PATCH_PATH_FORBIDDEN`（`/patch/1/value`）；move 為 `PATCH_SCHEMA_INVALID`（HTTP 在 schema 就 422）；超過 2 MiB 為 `LIMIT_EXCEEDED`；candidate 無效（機率和、capacity 0）與 pack 的規則也拒絕 |
| immutable base／patch | 已完成；SQL（coordinator 執行通過，`coordinator-integration.log`） | Base 是不可修改的 version；scenario 對任何 role 都不能 UPDATE／DELETE／TRUNCATE（0001，API 沒有 privilege），`scenarios.sql` 對 API（42501）與 owner（55000）都測；存的 patch 是 RFC 8785 形式（`TestCheckStoresTheCanonicalPatch`），讀取時核對 hash |
| base 存在、同一 tenant | 已完成；DB／SQL（coordinator 執行通過，`coordinator-integration.log`） | `models.GetVersion` 以 organization 過濾（再加 RLS）；其他 organization 的 version 為 404（`TestScenarioRoles`、`TestScenarioRoleMatrix`）；composite FK 擋下其他 model、其他 tenant 或不存在的 version（`scenarios.sql`：23503） |
| typed error、JSON Pointer、request_id | 已完成；HTTP 部分（coordinator 執行通過，`coordinator-integration.log`） | `Refused` 對應每個錯誤（`TestRefused`）；patch 的 issue 在 `/patch/<op>/...`、candidate 的在 candidate 的 path、dataset 的在 `/dataset_bindings/<i>/...`；HTTP 測試以 `code()` 依 `common.schema.json#/$defs/Error` 驗證並比對 `request_id` 與 `X-Request-ID` |
| role、audit、idempotency、tenant scope | 已完成；DB 部分（coordinator 執行通過，`coordinator-integration.log`） | Role 在 transaction 中讀 membership，replay 前也查（降為 viewer 後 replay 403）；audit 只有 ID、hash 與 op 數（不含名稱與 patch，`TestCreateManualScenario` 以 SQL 檢查）；同 key 同 body 回同一個 201、另一個 body 或另一個 version 為 409、8 個並行同 key 只有一個 scenario；每個 SQL 都以 organization 過濾（`TestListAndGet` 以不受 RLS 限制的 superuser 驗證）；RLS 與 grant 見 SQL 測試 |
| 不建立 version、不啟動 run | 已完成；DB（coordinator 執行通過，`coordinator-integration.log`） | `TestCreateManualScenario` 比較建立前後的 latest、version 數、draft 的 revision 與 hash、organization 的 job 數與 run 數；`TestScenarioClosedLoop`：AI scenario 批准與人工 scenario 之後 version 數不變、simulation job 為 0，人以 `scenario_id` 呼叫 createSimulation 之後才有 1 個（TEST-008）。架構規則 `scenarios-only-in-the-api` 讓 worker、job handler、其他 service 與 `internal/ai` 無法連結 scenario service |
| Package 位置 | 已完成 | `internal/scenarios`（application layer），不是規劃的 `internal/model/scenarios/`（generic kernel 不得碰 DB 與 HTTP）；記在 ARCHITECTURE §6 與 tasks.json 的 scope（見「差異」） |

## 實際執行結果

在 worktree 中執行（`environment.txt`：Go 1.27.3、Node 22.22.0、4 CPU 與其他 agent 共用、沒有 PostgreSQL；`make backend-test` 前先 `go clean -testcache`）。`make backend-test`、`backend-race`、`architecture-check`、`ai-eval`、`frontend-test` 在 9dae2b5 上執行；之後的 fb7ff84 只改 SQL 測試與 evidence（沒有 Go、TS 或 contract 的變更），`make spec-check` 在加入本報告之後最後再跑一次。Exit code 也在 `exit-codes.txt`。

| 指令 | Exit | 結果 |
|---|---|---|
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `PASS_LIMITED_SPEC_CHECKS`（68 個正例、130 個反例 fixture，T-028 前為 64／124；42 個 OpenAPI operation 的範例都依 schema 驗證）；planning、decisions（proposed 4：D-073 與 D-078–D-080）、TS 契約為最新（15 檔、106 型別）、hash（含 `valid-manual-scenario.json`）、`render_db_tests --check`（13 檔）都 PASS；重產的 `checks/*.json` 已提交。完整的 OpenAPI 官方 validator 為 `NOT_RUN_UNAVAILABLE` |
| `make backend-test` | 0 | gofmt、`go vet`、`go test`（`GEMINUS_FULL_DIFFERENTIAL=1`）：63 個 package `ok`，沒有 FAIL；DB-backed 測試全部 SKIP |
| `make backend-race` | 0 | `go test -race -timeout 30m`：63 個 package `ok`，沒有 FAIL、沒有 data race 報告；DB-backed 測試同樣 SKIP |
| `make architecture-check` | 0 | `PASS architecture: 75 packages, 17 rules, 16 build configurations`（新規則 `scenarios-only-in-the-api`）；7 個新 probe 在 `scripts/check-architecture` 的測試中，`TestProbesAreCaught` 通過 |
| `make ai-eval` | 0 | `TestFakeProviderEval`、`TestReviewEval` PASS；只有 fake provider，golden 正確率、live provider、DB 效果為 NOT RUN（log 中列出）；`checks/ai-evals/*.json` 沒有差異 |
| `make frontend-test` | 0 | lint、typecheck、unit：18 個檔案、439 個測試通過。Node 為 22.22.0（`package.json` 要 24.21.0；本機沒有 Node 24，以 `npm ci --engine-strict=false` 安裝 lockfile 的依賴，lockfile 沒有改） |
| `bash -n scripts/run_db_tests.sh`、`render_db_tests.py --check`、`sql_lint.py` | 0 | `sql-static.log`：語法沒有錯、render 為最新；`sql_lint.py` 是詞法檢查（dollar quote、引號、括號、statement 的結尾），對 0015 與新 suite 都 OK，對兩個已知正確的檔案也 OK，對刪了一個 `)` 的副本報錯。**沒有 PostgreSQL parser，所以不證明語法** |
| `go test -count=1 -v ./internal/scenarios/ ./internal/storage/scenarios/` | 0 | 11 個 unit 測試 PASS，7 個 DB-backed SKIP（`scenarios-unit-green.log`） |
| `make integration-test`、`make db-test`、`make db-go-test` | — | NOT RUN（沒有 PostgreSQL；見下） |

## 測試涵蓋（對應 TEST-003／007／008／009／010）

| 測試 | 驗證 | 驗收 |
|---|---|---|
| `TestCheck`、`TestCheckStoresTheCanonicalPatch`、`TestCheckKeepsABaseline`、`TestCheckRefusals`（unit） | allowlist、test-before-modify、atomic、candidate validation、pack、大小上限、存 RFC 8785 形式、`[]`（baseline）與非空 no-op（一般 scenario，照原樣保存，review 第 1 輪補檢查）接受、舊 base 被新規則拒絕時回 issue（不是 server 錯誤）、不是 JSON 的 base 與沒有 pack 的 domain 是 server 錯誤 | TEST-003、REQ-010 |
| `TestTheAIScenarioModeTakesTheSameRules`（unit） | 人工與 AI scenario 用同一套規則（D-021）；AI 的 no-op 是 `AI_OUTPUT_INVALID`、人的被當作一般 scenario 接受 | D-021、D-078 (a)(b) |
| `TestNameIssues`、`TestPublic`、`TestFingerprint`、`TestCursor`、`TestRoutesMatchContract`、`TestRefused`（unit） | 名稱規則；Scenario resource（兩種、schema parity、UTC、fail closed）；fingerprint 不受拼法影響但受 version 與 body 影響；cursor 綁 organization、version、list；route 與 roles 等於 `api-operations.json`（viewer 不能建立）；錯誤對應與不洩漏 storage 的原因 | TEST-009、D-079 |
| DB：`TestCreateManualScenario` | 舊版本上的人工 scenario、row 的內容、沒有 version／draft／job／run 的變化、audit、getScenario 與列表、replay 不再寫入、baseline | TEST-008、REQ-010 |
| DB：`TestCreateRefusals` | 每個拒絕 typed、path 正確、不寫入（沒有 scenario、audit、留下的 key） | TEST-003 |
| DB：`TestScenarioRoles`、HTTP：`TestScenarioRoleMatrix` | owner／editor 建立、viewer 讀、其他 organization 404、非成員、未知 identity 403、沒有 token 401、撤權後的 replay 403 | TEST-009 |
| DB：`TestScenarioIdempotency` | 同 key 一個 effect、另一個 body 或 version 為 409、key 屬於 actor、8 個並行同 key、8 個並行不同 key | TEST-007 |
| DB：`TestListPages`、`TestListAndGet`（storage） | 分頁、cursor、limit；organization 與 version 的 filter 本身成立 | TEST-009、TEST-010 |
| DB：`TestScenarioDatasets` | 沒有 object storage 503、storage 失敗 503、snapshot 不符 422、恢復後 201 | D-006、D-078 (a) |
| HTTP：`TestScenarioClosedLoop` | AI scenario 批准→getScenario→人工 scenario→列表兩種→沒有 version 與 run→人以 `scenario_id` 執行才入列 1 個 run | TEST-008 |
| HTTP：`TestScenarioRequestRefusals` | 14 種 request 拒絕的 status、code、path，與 409、404、400 | TEST-003、D-079 (a) |
| SQL：`db/tests/scenarios.sql` | grant、FORCE RLS、guard（建立者、時間）、名稱 CHECK、composite FK、immutability、RLS 每個 tenant 與沒有 tenant、worker 只能讀、列表順序、index | TEST-004、TEST-010、D-080 |
| Architecture probe（7 個） | worker、job handler（generator）、其他 service（runs、approval）、`internal/ai`、kernel 都無法連結 scenario service | AGENTS.md §2 |
| TS：`contracts.test.ts`、`api.test.ts` | fixture 的型別、Approval 的 discriminant、6 個 `@ts-expect-error`；client 的 method、path、key、body、cursor、錯誤與本地拒絕 | D-079 |

### 先紅後綠

- **Go unit**：測試先寫好，`internal/scenarios` 只放能編譯但行為錯誤的 stub（`check` 什麼都接受、`Public` 回空、`Routes` 為空……），10 個測試都失敗（`scenarios-unit-red.log`，exit 1；第一次執行時 `TestRoutesMatchContract` 對空的 `Routes` 取 index 而 panic，改用 `t.Fatalf` 後重跑，log 為重跑的版本）。實作後全部通過（`scenarios-unit-green.log`）。
- **TS client**：先寫 `api.test.ts` 的 scenario 測試，以舊的 `client.ts` 執行：typecheck 13 個錯誤（12 個 TS2339：沒有 `createScenario` 等 method；1 個隨之而來的 TS7006），vitest 30 個中 5 個失敗（`ts-client-red.log`）；加上 method 後 typecheck 與 56 個測試通過（`ts-client-green.log`）。契約的型別與 fixture 是一起寫的，由 typecheck 的 `@ts-expect-error` 確認每個反例確實是型別錯誤。
- **Mutation 第 1 輪**（`mutants-unit-r1.log`）：20 個 mutant 中 2 個存活、2 個只因編譯失敗而被擋下。修正：`TestRefused` 原本只看 `Error()`（不含 details），補上檢查送出的 code、message 與 details 不含 storage 的原因（`storage-cause-sent`）；「fingerprint 不先 canonical 化 body」是 equivalent mutant（`hashing.Of` 會把整份 fingerprint 再 canonical 化），換成「fingerprint 不含 body」；兩個編譯失敗的 mutant 改成能編譯的寫法；另補 `TestCheckStoresTheCanonicalPatch` 與 `TestPublic` 的 patch 拼法檢查（原本 `sameJSON` 把兩邊都 canonical 化，`raw-patch-stored`、`jsonb-spelling-shown` 會存活）。**第 2 輪 20 個全部被擋下，沒有一個是因為編譯失敗**（`mutants-unit.log`）。
- **SQL**：寫完 `scenarios.sql` 後重讀時發現列表順序的檢查以 `twin.scn_id` domain 的陣列與 `text[]` 比較，可能沒有對應的 operator，改為 `array_agg(id::text …)`（fb7ff84）。這是靜態 review 的發現，**沒有在 database 上的 red 或 green**。
- DB-backed Go 測試與 SQL 都沒有執行，所以沒有它們的 red 或 green。9 個 DB mutant（`mutate.py db`）都能編譯（以 `go vet` 確認後還原），需要 database 才能執行。

## Migration、RLS 與 grant 的影響

- **0015**（新，D-080 PROPOSED）：`scenarios_name_length` CHECK（1–200 字元）；BEFORE INSERT trigger `protect_scenario_creation`（`search_path` 固定、撤銷 PUBLIC）：schema owner 以外，`created_by` 必須是 `app.user_id`（42501）、`created_at` 必須是 transaction 的時間（55000）；index `scenarios_base_idx (organization_id, base_version_id, created_at, id)`。Down 撤掉三者。0001–0014 都沒有修改。
- **沒有新表**：RLS（FORCE）、composite tenant FK、不可修改與 grant 都沿用 0001、0002（API 對 scenarios 只有 SELECT／INSERT，worker 只有 SELECT），以 proposal 為來源者沿用 0013。`roles.sql` 對所有 twin function 的 `search_path` 與 PUBLIC 權限的通用檢查也涵蓋新的 function。
- **對既有寫入的影響**：T-026 的 approval 以 API 為批准者寫入 scenario（`created_by` 為 `app.user_id`、`created_at` 為預設的 `now()`），符合 guard；Go 測試中以 owner 寫入的 scenario 不受 guard 限制；`proposal_decisions.sql` 以 API 寫入的 scenario 也符合。若部署的 database 已有 scenario 的名稱超過 200 字元，0015 會因 CHECK 失敗（現有的寫入路徑都不會產生這種名稱）。
- 這些都 **NOT RUN**：0015 的 up／down／up 與所有 SQL suite 等 coordinator 執行。

## NOT RUN

完整清單在 `checks/evidence/2026-10-25-T-028/db-not-run.txt`，原因都是本 worktree 沒有 PostgreSQL：

- `make integration-test`、`make db-test`、`make db-go-test`；migration 0015 的 up／down／up。
- SQL：`db/tests/scenarios.sql`（新），以及套用 0015 之後重跑的 `invariants.sql`、`constraints.sql`、`roles.sql`、`self.sql`、`runs.sql`、`imports.sql`、`proposals.sql`、`proposal_jobs.sql`、`proposal_decisions.sql`、`import_mapping.sql`、`pool_rows.sql`、`ir_rows.sql`。
- 新的 DB-backed Go 測試：`internal/scenarios` 的 `TestCreateManualScenario`、`TestCreateRefusals`、`TestScenarioRoles`、`TestScenarioIdempotency`、`TestListPages`、`TestScenarioDatasets`；`internal/storage/scenarios` 的 `TestListAndGet`；`internal/api` 的 `TestScenarioClosedLoop`、`TestScenarioRoleMatrix`、`TestScenarioRequestRefusals`。
- 0015 可能影響的既有 DB 測試：`internal/proposals/approval` 的 `TestApproveScenario`、`TestApprovalOnAMovedBase`、`TestDecisionsWaitForTheModelRow`、`TestApprovalIdempotency`、`TestDecisionRaces`；`internal/api` 的 `TestApprovalClosedLoop`、`TestRunRequestRefusals` 與 `simulations_results_test.go` 的 saved-scenario run（以 owner 寫入，不受 guard 限制）。
- 9 個 DB mutant（`mutate.py <root> <log> db`，沒有 `GEMINUS_TEST_DATABASE_URL` 時拒絕執行）。
- `make e2e-test`（UI 屬 T-031）；live AI（T-053 之前一律關閉）；完整的 OpenAPI 官方 validator（`NOT_RUN_UNAVAILABLE`）。

## 提出的決策（PROPOSED，agent 沒有批准任何一項）

| ID | 主題 | 建議（已實作） | 其他選項 |
|---|---|---|---|
| D-078 | 人工 scenario 的檢查範圍、baseline、base 與名稱 | (a) A：與 AI scenario 的 review 中不涉及 provider 的部分相同（allowlist、candidate validation、pack、binding stage）；(b) A：只有 `[]` 是 baseline（REQ-010）；非空但不改變模型的 patch 當作一般 scenario 接受（review 第 1 輪 I3 改寫）；(c) 任何 version、不鎖 model row（D-067 的解讀，請確認）；(d) 名稱照原樣，只有空白為 422；Cc 與 bidi 覆寫字元 A：照原樣保存、UI escape（review 第 1 輪 I4） | (a) B 只查 patch 與 candidate；(b) B 拒絕非空 no-op（新 ValidationCode）；(d) B 接受任何名稱；Cc 與 bidi B：422 `/name` |
| D-079 | Scenario 的 HTTP binding | 檢查順序與錯誤碼、fingerprint 為 version ID 與 RFC 8785 body、只存 201、audit 不含名稱與 patch、列表依 (created_at, id) 與綁定的 cursor（已知限制：較早開始、較晚 commit 的 scenario 可能被往後翻頁的 client 漏掉，repository 的慣例，review 第 1 輪 L4）、讀取時核對 hash | 各項「不採用時」列在 DECISIONS_AND_TBD |
| D-080 | Migration 0015 | A：名稱 CHECK、API 的建立者與時間 guard、列表 index（created_at 固定為 `now()` 也限制了 D-079 (e) 之後的修正，review 第 1 輪 L4） | B：不新增 migration |

## 與指示或規劃的差異

- **Package 位置**：tasks.json 規劃 `internal/model/scenarios/` 與 `internal/ai/scenario/`。前者是 generic kernel，ARCHITECTURE 禁止它碰 storage、DB、HTTP，所以改為 `internal/scenarios`（與 T-023、T-025、T-026 的做法相同）；後者不需要，因為 AI 的 scenario mode 已由 T-025／T-026 完成，本任務沒有新增 AI 程式。已更新 tasks.json 的 planned outputs 與 scope 說明、ARCHITECTURE §2、§6。
- **TS client**：coordinator 要求契約型別與其測試；我另加了 `createScenario`、`listScenarios`、`getScenario` 三個 client method 與測試（新增 API 須有 generated client 的契約測試，AGENTS.md §5），沒有加 approve／reject 或 UI（屬 T-031）。
- **API_DATABASE §9**：安裝順序原本停在 0013，我一併補上 0014（T-017 的）與 0015。
- **Node 版本**：本機只有 Node 22.22.0，以 `--engine-strict=false` 安裝依賴後執行 `make frontend-test`（exit 0）；coordinator 的環境應以 Node 24 再跑一次。
- **SQL 的靜態檢查**：repository 沒有 PostgreSQL parser，不得自行新增依賴，所以寫了詞法檢查 `sql_lint.py`（放在 evidence，不是 repository 的工具），它不證明語法。
- **驗收測試**：tasks.json 的 acceptance tests 仍只列 TEST-007、TEST-008；TEST-003、TEST-009、TEST-010 中 scenario 的部分只加進 traceability 的 REQ-010。

## 殘餘風險

- **DB 與 SQL 測試**：agent 端沒有執行（本 worktree 沒有 PostgreSQL），期望值是依 PostgreSQL 的順序推論的；之後 coordinator 在 615766c 上全部執行並通過（`checks/evidence/2026-10-25-T-028/coordinator-integration.log`，見「Coordinator 的執行」）。Review 第 1 輪新增的 DB 測試 `TestCorruptScenarioIsNotShown` 與 4 個 DB mutant 仍為 NOT RUN（見「Review 第 1 輪」）。
- **listScenarios 讀出整份 IR**（review 第 1 輪 I2，只列為成本，不處理）：列表以 `models.GetVersion` 確認 version 存在且屬本 organization，這會讀出該 version 的整份 IR（上限 `validate.MaxIRBytes`），只為了決定 404；每次列表因此多一次最多約 2 MiB 的讀取。不影響正確性與 tenant 隔離；之後可改為只查存在與否的 query。
- **D-078–D-080 待 owner 決定**：若選其他選項，需依各項「不採用時」修改；特別是 D-078 (b)（若拒絕非空 no-op）需要新的 ValidationCode 與兩語字串。
- **Warning 不保存**：人工 scenario 的 candidate warning（例如 `UNREACHABLE_NODE`）不拒絕也不顯示在 Scenario resource 中（D-078 (a)），使用者要到執行或比較時才看得到。
- **舊 base 與新規則**：在現行規則已不接受的舊 version 上建立 scenario 會回 422（candidate 的 issue），AI review 對同樣的 base 則是 server 錯誤；這是 AI 與人工唯一可能的行為差異，只有在規則變嚴後才會出現。
- **建立時不鎖 model row**：scenario 不改 model、draft 或 version，所以與 publish、批准並行時沒有一致性的問題；但若之後加入「刪除 version」之類的操作，需要重新檢查。
- **`publishVersion`／`createSimulation` 的 storage 錯誤仍回 500**（H-021 不在範圍），而 createManualScenario 與 approveProposal 回 503，三者不一致的狀態延續。
- **前端在 Node 22 上通過**，未在 `.nvmrc` 的 Node 24 上執行。

## Coordinator 的執行（615766c）

Coordinator 把六個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（H-021 的 21423aa 之後，沒有衝突）：c080577 → 17c17fd、7cf96d2 → 64c2de0、7977a44 → 0af723a、9dae2b5 → 35386ef、fb7ff84 → 9bc053d、b9b1c10 → 615766c。之後 `spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`frontend-test`、`simulation-reference-test`、`backend-race` 與新的 `dt_test_t028` 上的 `integration-test` 都是 exit 0：migration 0015 的 up／down／up（`run_db_tests.sh`）、所有 SQL suite（含新的 `db/tests/scenarios.sql`，兩輪），以及 32 個 package 的 DB-backed Go 測試（含 `internal/scenarios`、`internal/storage/scenarios`、`internal/api` 的新測試與 approval 的既有測試），沒有 FAIL 或 SKIP。本報告 NOT RUN 中的 DB 項目因此都已執行。

DB mutant：coordinator 以同一個已 migrate 的 database，在 `git archive` 匯出的副本上執行 `mutate.py <副本> <log> db`（`GEMINUS_REQUIRE_DB_TESTS=1`）：9 個全部被擋下，每一個都是測試失敗而不是編譯錯誤（`coordinator-mutants-db.log`、`coordinator-mutants-db-run.txt`）。Log 與 exit code 在 `checks/evidence/2026-10-25-T-028/coordinator-*`。仍為 NOT RUN：`make e2e-test`、live AI、OpenAPI 官方 validator。D-078–D-080 仍是 PROPOSED。

## Review 第 1 輪

Branch `t028-r1`，從 19f676b（整合後的 head，含上一節）開出，沒有 push。依 coordinator 的指示本 worktree 沒有 database：本輪新增的 DB 測試與 DB mutant 為 NOT RUN。沒有 live AI，沒有讀取或設定任何 key。D-078–D-080 仍是 PROPOSED（`approved_by`／`approved_at` 為 null），T-028 在 tasks.json 仍為 `not_started`。Log 在 `checks/evidence/2026-10-25-T-028-r1/`。

### Commit

| Commit | 內容 |
|---|---|
| 3a1c460 | L1、L2、I1 的 unit 測試（`internal/scenarios/edges_test.go`）；L3 的 DB-backed HTTP 測試（`internal/api/scenarios_test.go` 的 `TestCorruptScenarioIsNotShown`）；I3：`TestCheckKeepsABaseline` 補檢查保存的 patch、`check` 與 parity 測試的說明改寫；本輪的 mutation harness（`mutate.py`）與 log |
| b31f160 | L4、I3、I4：DECISIONS_AND_TBD 的 D-078 (b)(d)、D-079 (e)、D-080 (b) 與 `planning/decisions.json`（狀態不變）；API_DATABASE §4；L5：SECURITY §5 引用 coordinator 的執行 |
| （本 commit） | 本報告的 L5、I2、I3 修正與本節；`environment.txt`、`exit-codes.txt`、各指令的 log。`make spec-check` 重產的 `checks/*.json` 沒有差異，所以沒有提交 |

### 發現 → 修正 → 證據

| 項目 | 發現 | 修正 | 證據 |
|---|---|---|---|
| L1 | 沒有只回 warning 的 pack 的測試；mutant `warnings-refuse`（`isError` 永遠 true）存活 | `TestCheckAcceptsWarnings`：只回 `SeverityWarning` 的 pack（`warningPack`）→ 接受；candidate 本身有 warning 的 patch（CI 結果全部流向 test，`f_pass` 1、`f_retry` 0，測試先確認 `patch.Apply` 的結果 valid 且有 `UNREACHABLE_NODE` warning）→ 接受；回 error 的 pack 仍拒絕 | `mutants-unit.log`：`warnings-refuse`、`pack-warnings-refuse`、`model-warnings-refuse` 在舊測試下 SURVIVED、新測試 KILLED |
| L2 | 沒有 `bindingFailure` 對 context 錯誤的測試；mutant `binding-cancel-is-503` 存活 | `TestBindingFailure`（T-026 approval 的同名測試的模式）：request 還在、store 失敗 → `DatasetStorageError`（503 `DATASET_STORAGE_FAILED`）且 wrap 原因；request 已取消或已過 deadline（不論錯誤是 wrap 的 `context.Canceled`／`DeadlineExceeded` 還是 store 自己的錯誤），或 request 還在但錯誤是 context 的錯誤 → 不是 `DatasetStorageError`，原錯誤原樣回傳，由 httpapi 依 context 錯誤處理（503 `SERVICE_UNAVAILABLE`「the request did not complete in time」，沒有 `DATASET_STORAGE_FAILED` 的 details） | `binding-cancel-is-503`、`binding-only-the-context`、`binding-only-the-error`、`binding-wraps-everything`：舊測試下 SURVIVED（沒有 database 時；最後一個與第一輪的 DB mutant `db-storage-failure-unwrapped` 效果相同，後者由 DB 測試 `TestScenarioDatasets` 擋下，但本輪沒有以 database 驗證這一個）、新測試 KILLED |
| L3 | 沒有經 HTTP 讀取 hash 不符的 row 的測試 | `TestCorruptScenarioIsNotShown`（DB-backed，`internal/api`）：以 `twin_owner` 寫入一個 patch 為 capacity 97、`patch_hash` 為另一個（真的）scenario 的 hash 的 row；getScenario（viewer、editor）與 listScenarios（預設 limit 與 limit=2）都是 500 `INTERNAL_ERROR`，依 `common.schema.json#/$defs/Error` 驗證、`request_id` 等於 `X-Request-ID`、message 為「internal error」、沒有 details，body 不含 `capacity`、`/resources/1`、`"op"`、`"value"`、`patch`、名稱、兩個 scenario 的 ID 與 hash；另一個 scenario 仍可讀；在該 row 之前結束的一頁（limit=1）仍回 200 與 next_cursor；log 中每個 request ID 的那一行有 `patch_hash` 與該 row 的 ID，整份 log 沒有 patch 的內容與名稱 | **NOT RUN**（沒有 database，`new-tests-green.log` 為 SKIP）。4 個 DB mutant（`db-hash-not-checked-on-read`、`db-list-skips-the-corrupt-row`、`db-get-corrupt-is-not-found`、`db-corrupt-cause-sent`）以 `mutate.py <root> <log> db` 執行（需要 `GEMINUS_TEST_DATABASE_URL`），NOT RUN；`db-compile` 確認 4 個 snippet 都只出現一次、套用後都能編譯（`mutants-db-compile.log`） |
| L4 | (created_at, id) 的 keyset 與 transaction 開始時間的 created_at 可能永久漏列 | D-079 (e) 寫入已知限制（較早開始、較晚 commit 的 scenario 可能被往後翻頁的 client 漏掉；listProjects、listModels 相同，是 repository 的慣例；修正屬跨列表的決策）；D-080 (b) 寫入「created_at 固定為 `now()` 也限制了之後的修正（需新 migration 改寫 trigger）」；API_DATABASE §4 的 listScenarios 一併註明；兩項仍 PROPOSED | DECISIONS_AND_TBD、`planning/decisions.json`；`spec-check` 的 decisions 檢查 |
| L5 | SECURITY.md:66 與本報告 :3、:30、:31、:38 仍寫 NOT RUN，但 coordinator 已執行 | SECURITY §5 的 T-028 段與本報告第 3 行引用 `checks/evidence/2026-10-25-T-028/coordinator-*`；scope 表中 7 列（:30、:31、:35–:39，後 4 列同樣寫 NOT RUN，一併修正）改為「coordinator 執行通過」；殘餘風險的第一條改寫 | 本報告、`docs/SECURITY.md` |
| I1 | `capped` 沒有超過上限的直接測試；`capped-off-by-one`、`capped-disabled` 存活 | `TestCapped`：0、1、999、1000 個原樣保留；1001 個 warning → 1000 項，第一項為 `LIMIT_EXCEEDED`（path ""、warning、「2 more issues are not listed」），其後是前 999 項；最後一項為 error 時第一項為 error；2500 個 →「1501 more issues are not listed」 | `capped-off-by-one`、`capped-disabled`、`capped-miscounts`、`capped-severity-of-the-kept`、`capped-keeps-the-last`：舊測試下 SURVIVED、新測試 KILLED |
| I3 | D-078 (b) 把「不改變模型的 patch」也稱為 baseline，與 REQ-010（`docs/PRD.md:34`：baseline 為空 patch）不符 | D-078 (b) 改為「只有 `[]` 是 baseline；非空但不改變模型的 patch 當作一般 scenario 接受」（DECISIONS_AND_TBD、decisions.json、API_DATABASE §4、本報告）；`check` 的說明、`TestCheckKeepsABaseline` 與 parity 測試的說明同樣改寫；`TestCheckKeepsABaseline` 補檢查非空 no-op 照原樣（RFC 8785）保存、不改寫成 `[]`。程式行為不變（只改說明） | `no-op-stored-as-baseline`：舊測試（`TestCheckKeepsABaseline` 也略過，19f676b 的版本只比較 candidate）下 SURVIVED、新的檢查 KILLED |
| I4 | 名稱中的控制字元與 bidi 覆寫字元沒有明確的選項 | D-078 (d) 加入：A 維持現狀（照原樣保存、UI escape，同 D-067）；B 以 422 `/name` 拒絕 Cc（U+0000 以外）與 bidi 覆寫字元（U+202A–U+202E、U+2066–U+2069）。建議 A，不改 code | DECISIONS_AND_TBD、decisions.json |
| I2 | listScenarios 為了 404 讀出整份 IR | 依指示只列為殘餘成本（「殘餘風險」） | — |
| I5、I6、I7 | — | 依指示不處理（I7：CI 以 Node 24 執行且為綠） | — |

### 指令與 exit code

在 worktree 中執行（`environment.txt`：Go 1.27.3、4 CPU 與其他 agent 共用，執行時另有其他 worktree 的 `go test -race` 在跑；沒有 PostgreSQL server）。程式與測試在 3a1c460 之後沒有再改（b31f160 與本 commit 只改文件與 evidence）。Exit code 也在 `exit-codes.txt`。

| 指令 | Exit | 結果 |
|---|---|---|
| `go vet ./internal/scenarios/ ./internal/api/` | 0 | — |
| `go test -count=1 -v -run '^(TestCheckAcceptsWarnings\|TestBindingFailure\|TestCapped\|TestCorruptScenarioIsNotShown)$' ./internal/scenarios/ ./internal/api/` | 0 | 3 個 PASS；`TestCorruptScenarioIsNotShown` SKIP（沒有 database，NOT RUN）；`new-tests-green.log` |
| `python mutate.py <root> mutants-unit.log unit` | 0 | 13 個 mutant 在舊測試下都 SURVIVED（即 review 的發現），都被新測試 KILLED；13 個 kill 都是測試斷言失敗（`--- FAIL`），沒有編譯失敗；檔案每次都還原；`mutants-unit.log` |
| `python mutate.py <root> mutants-db-compile.log db-compile` | 0 | 4 個 DB mutant 都只出現一次、套用後 `go vet` 通過；測試 NOT RUN；`mutants-db-compile.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `PASS_LIMITED_SPEC_CHECKS`（68 個正例、130 個反例 fixture，42 個 OpenAPI operation）；decisions：approved 70、proposed 4（D-073 與 D-078–D-080）；TS 契約為最新（15 檔、106 型別）、hash 28 個、`render_db_tests --check`（13 檔）都 PASS；重產的 `checks/*.json` 沒有差異；完整的 OpenAPI 官方 validator 為 `NOT_RUN_UNAVAILABLE`；`spec-check.log`（在本報告完成後執行） |
| `go clean -testcache && make backend-test` | 0 | gofmt、`go vet`、`go test`（`GEMINUS_FULL_DIFFERENTIAL=1`）：63 個 package `ok`，沒有 FAIL；DB-backed 測試 SKIP；`backend-test.log` |
| `go test -race -count=1 ./internal/scenarios/ ./internal/api/` | 0 | 兩個 package `ok`，沒有 data race 報告（DB-backed 測試 SKIP）；`race-touched.log` |
| `make architecture-check` | 0 | `PASS architecture: 75 packages, 17 rules, 16 build configurations`；`architecture-check.log` |
| `make frontend-test` | — | NOT RUN：本輪沒有 TS 的變更 |

### NOT RUN

- DB-backed：`internal/api` 的 `TestCorruptScenarioIsNotShown`（本輪新增）；4 個 DB mutant（`mutate.py <root> <log> db`）。本輪沒有改 migration 或 SQL，其他 DB 測試的程式也沒有改，但本 branch 上沒有重跑 `make integration-test`、`make db-test`、`make db-go-test`。
- `make frontend-test`：本輪沒有改 TS、schema 或 OpenAPI（指示：只在 TS 有改時執行）。
- `make ai-eval`、`make backend-race`（全部 package）：不在本輪要求的清單中，race 只對改到的 package 執行（見上）；本輪沒有 AI 程式的變更。
- `make e2e-test`、live AI（T-053 之前一律關閉）、OpenAPI 官方 validator（`NOT_RUN_UNAVAILABLE`）。
