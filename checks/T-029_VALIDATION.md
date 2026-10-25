# T-029 驗證報告：Results／baseline comparison API

**日期：2026-10-25｜狀態：agent 實作完成；DB 測試、DB mutant 與 `make integration-test` 已由 coordinator 在一次性 database 上執行（72e370d，全部 exit 0，見「Coordinator 的執行」）；等獨立 review（H-003）；T-029 在 tasks.json 仍為 `not_started`（agent 不改狀態）。｜需求：REQ-013、REQ-014、REQ-015（另涵蓋 REQ-002、REQ-019 中比較的部分）｜Gate：D-010 已 APPROVED；本任務提出的 D-088、D-089、D-090 是 **PROPOSED**（`approved_by`／`approved_at` 為 null），依建議實作並加進 T-029 的 gate（D-090 另加進 T-030、T-032）。｜驗收測試：TEST-022、TEST-023、TEST-024（另寫了 TEST-009 中比較的部分）｜本 worktree 沒有 PostgreSQL：所有 DB-backed Go 測試都 SKIP，`make integration-test` 為 NOT RUN（見「NOT RUN」）。**

範圍依 tasks.json 與 coordinator 的指示：

1. result／read readiness：只比較 completed 的 run，其他狀態一律 409 `RESULT_NOT_READY`（與 getSimulationResults 相同），不回部分結果；
2. same-context comparison：SIMULATION_SPEC §7（D-010 的展開，APPROVED）的條件逐項實作，不成立時 200 `comparable=false` 並列出原因；
3. absolute／relative delta、baseline 為 0、缺值、unavailable 與不可比的處理，不捏造 percentage；
4. analyst（T-030）可以安全引用的 metric evidence path；
5. 每個 DB 查詢以 organization 過濾（再加 RLS）、viewer／editor／owner 都可以比較、typed error 帶 JSON Pointer（query 參數：path ""、reason 為參數名）與 request_id、context 逐層傳遞、metric 的順序與 Go map 的順序無關。

**沒有任何 live AI 呼叫**，沒有讀取或設定任何 API key。沒有新 migration、沒有新 SQL、沒有新依賴。沒有改 `internal/simulation` 的任何檔案（engine、DES kernel、Result 型別；`compare` 只讀 `internal/simulation` 的 Result 型別）或 `internal/results/metrics`。逐條輸出在 `checks/evidence/2026-10-25-T-029/`：`environment.txt`、`exit-codes.txt`、各指令的 log、`db-not-run.txt`、red／green log、`mutate.py` 與 `mutants-*.log`。

## Commit

Branch `t029`，從 3e56705 開出，沒有 push。

| Commit | 內容 |
|---|---|
| bf3bd76 | Contract：`http.schema.json` 的 Comparison（新增 `incomparable_reasons`；`reason` 與 `warnings` 改為封閉 code 清單 `ComparisonReason`、`IncomparableReason`、`ComparisonWarning`；`metric_path` 的 `^/metrics/` pattern；maxItems 10000；if/then 規則）；OpenAPI compareSimulations 的語義、範例與自己的 400／404／409；手推 golden `valid-comparison.json`（輸入 `valid-result.json` 與新的 `valid-result-scenario.json`，推導寫在 `testdata/contracts/README.md`）、不可比的範例與 13 個反例（`cases.json`）；重產的 TS 契約型別與 `contracts.test.ts` |
| aa58230 | `internal/results/compare`（純粹的比較與 evidence path，kernel）與 unit 測試 |
| 8d2261c | `internal/comparisons`（service 與 HTTP adapter）、`internal/api` 的 route 與 DB-backed 整合測試、architecture 的 application layer 與 5 個 probe、Makefile 的 integration-test 說明 |
| 800cc13 | D-088–D-090 PROPOSED（DECISIONS_AND_TBD、decisions.json）；tasks.json 的 gate、planned outputs、scope 的 package 說明（status 不變）；ARCHITECTURE §2／§6、API_DATABASE §6、SIMULATION_SPEC §7、TWIN_IR_SPEC §2.1、AI_SPEC §9、CONTRACT_BLOCKERS CB-22／CB-23；traceability；重產的 `checks/*.json` |
| 0378204 | Mutation 第 1 輪發現的測試缺口：`Statistics` 的順序（見「Mutation」） |
| 8b64c4e | TS client 的 `compareSimulations`（AGENTS.md §5：新增 API 要有 generated client 的契約測試）與 `api.test.ts` |
| （本 commit） | 本報告與 evidence |

## 稽核：scope 逐條

| Scope | 判定 | 證據／處理 |
|---|---|---|
| result／read readiness | 已完成；DB 部分 NOT RUN | `Service.Compare` 在同一個唯讀 REPEATABLE READ transaction 中先讀兩個 run（不存在或其他 organization：404），兩者都存在後才看狀態，不是 completed（queued、running、failed、cancelled）為 409 `RESULT_NOT_READY`，`details[0].reason` 為參數名，baseline 先於 scenario；completed 卻沒有 result、或 result 不能嚴格解碼為 500（`compare.ErrInconsistent`），不比較。Unit：`TestRefused`、`TestDecodeResult`。DB：`TestComparisonClosedLoop`（完成前 409）、`TestComparisonRefusals`（queued、cancelled、failed，404 先於 409） |
| same-context comparison | 已完成 | §7 的條件逐項（D-088 (a)）：`model_version_id`、`engine_version`、`dataset_manifest_hash`、measurement window 的 horizon 與 warmup tick、replications、seed 相同，baseline 的凍結 patch 為 `[]`；`trace_level` 不比（§9）。另外先核對每個 Result 是它自己 run 的（run ID、status、engine、seed、replications、manifest、window 的 tick；不符為 500）。Unit：`TestIncomparable`（9 種條件各自、全部同時且依固定順序）、`TestComparableAcrossSpellings`（1 day＝1440 minute、trace level）、`TestInconsistent`（21 種 × 兩邊）。DB：`TestComparisonIncomparable` |
| absolute／relative delta | 已完成 | absolute＝scenario − baseline、relative＝absolute／baseline，只在 `internal/results/compare` 計算一次（API 與之後的 analysis job 共用）。Unit：`TestDeltas`（含負的 baseline 照公式）、`TestGolden`（手推的 golden，逐 byte）。DB：`TestComparisonClosedLoop` 對每個 metric 以兩個 Result 的值重算 delta 並逐一比對 |
| baseline 為 0 | 已完成 | relative 為 null、reason `BASELINE_ZERO`，absolute 照算（TEST-022）；-0 也是 0；schema 的 if/then 也要求 `BASELINE_ZERO` 時 baseline 為 0、relative 為 null。Unit：`TestBaselineZero`；反例 `invalid-comparison-relative-at-zero-baseline`、`invalid-comparison-zero-reason-nonzero-baseline` |
| missing／unavailable | 已完成 | 任一邊 unavailable：status `unavailable`，兩個 delta 都是 null，另一邊的值保留（`BASELINE_UNAVAILABLE`、`SCENARIO_UNAVAILABLE`、`BOTH_UNAVAILABLE`）；只在一個 run 有的 metric 為 `MISSING_IN_BASELINE`／`MISSING_IN_SCENARIO`（優先）；queue 沒有 visit 時 waits unavailable、`queue_length_mean` 照比；unavailable 一律是 null，不是 0。Unit：`TestUnavailable`、`TestMissing`、`TestDORAStaysUnavailable`；反例 `invalid-comparison-unavailable-*` |
| incomparable | 已完成 | 200、`comparable=false`、`incomparable_reasons`；每個 metric `incomparable`（`RUNS_NOT_COMPARABLE`），兩邊的值並排、沒有 delta（§7 的原文；REQ-014）。Unit：`TestIncomparable`；DB：`TestComparisonIncomparable`、`TestComparisonClosedLoop` 的角色對調；反例 `invalid-comparison-incomparable-*` |
| metric evidence path（analyst 可安全引用） | 已完成 | RFC 6901 pointer，從 Result 的根指到單一統計值（D-090 (a)），與 analysis-output 的 `^/metrics/` pattern 相同；`compare.Statistics` 列出可引用的統計值、`compare.Resolve` 取值（unavailable 為 nil；summary 本身、不是統計值的 member、malformed 或不正規的拼法、Result 沒有的 key 都是 `ErrUnknownPath`）。Unit：`TestResolve`（32 個被拒絕的 path，另有沒有 domain 的 Result 與 nil）、`TestPathEscaping`；DB：`TestComparisonClosedLoop` 以 `Resolve` 核對 comparison 的每個值。T-024 的 ALLOWED_METRIC_REFERENCES 是 summary 層級，兩者的對應交給 T-030（D-090 (a)，見「殘餘風險」） |
| 順序與決定性 | 已完成 | 固定順序：run 層級、additional_metrics、dora、domain、resources、queues；key 依 byte 順序（兩個 run 的聯集再排序）；統計值固定順序；RFC 8785 輸出。Unit：`TestOrder`（明確的順序，以及 40 次不同 map 順序得到相同 bytes 與相同的 `Statistics` 順序）、`TestContract`（輸出就是 RFC 8785）；DB：三種 role 讀到相同 bytes |
| TEST-023（範例 pack 的 DORA） | 已完成；DB 部分 NOT RUN | 沒有 mapping 的 DORA 在兩邊都是 unavailable，比較中仍是 unavailable（`BOTH_UNAVAILABLE`）、沒有 delta；generic 的 `failure_count` 以自己的 path 比較，不出現在 `/metrics/dora/` 之下。Unit：`TestDORAStaysUnavailable`；DB：`TestComparisonClosedLoop`（software-delivery pack 實際產生的 DORA） |
| TEST-024 的基礎 | 已完成（analyst 本身屬 T-030） | 比較是 server 計算的唯一 delta（`Compare`）；`Statistics`／`Resolve` 讓 T-030 檢查 evidence 的 path 是否存在、值是否 available、delta 是否等於 comparison 的同一個 path。TEST-024 的 analyst 端（錯 run、錯 path、null metric、錯 delta、虛構的數字不 completed）是 T-030 的驗收 |
| tenant scope 與 role | 已完成；DB 部分 NOT RUN | 讀取用 `internal/storage/runs` 的 `Get`、`GetResult`（每個 statement 以 organization 過濾，再加 FORCE RLS，T-023）；role 依 x-required-roles（任何成員）。Unit：`TestRoutesMatchContract`（route 與 roles 等於 `api-operations.json`，viewer 可以）。DB：`TestComparisonRoleMatrix`（三種 role 200、其他 organization 的成員 404、其他 organization 的 run 404 且與不存在的 run 回應完全相同、未知 identity 403、沒有 token 401） |
| typed error、JSON Pointer、request_id | 已完成 | Query 的錯誤先於任何 DB 工作（400 `INVALID_REQUEST`，`details[0]` 的 path 為 ""、reason 為參數名，與 limit、cursor 相同）；404／409 也指出參數；HTTP 測試以 `code()` 依 `common.schema.json#/$defs/Error` 驗證並比對 `request_id` 與 `X-Request-ID`。Unit：`TestRunQuery`（13 種 query）、`TestRefused` |
| Package 位置 | 已完成 | 規劃的 `internal/results/http/` 落在 generic kernel，所以純粹的比較在 `internal/results/compare`、service 與 HTTP 在 `internal/comparisons`（application layer；D-089 (e)，ARCHITECTURE §6，tasks.json 的 scope） |
| Contract 先行 | 已完成 | schema、OpenAPI、fixtures 先於 Go；TS 型別以 `make ts-contracts` 重產；Go 型別以 `contracttest.Parity` 對 schema、fixtures 以 `RoundTrip`；Python（`make spec-contracts`）與 Go 兩個 JSON Schema validator 對每個 Comparison fixture 結果相同（`TestContract`） |

## 實際執行結果

在本 worktree 執行（`environment.txt`：Go 1.27.3、Node 22.22.0、4 CPU 與其他 agent 共用、沒有 PostgreSQL；`make backend-test` 前先 `go clean -testcache`）。Exit code 也在 `exit-codes.txt`。

| 指令 | Exit | 結果 |
|---|---|---|
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `PASS_LIMITED_SPEC_CHECKS`：72 個正例、143 個反例 fixture（T-029 前為 69／130；新增 3 個正例、13 個反例，138 個反例釘在期望的 path，之前 125）；42 個 OpenAPI operation 的範例都依 schema 驗證（含 compareSimulations 的兩個 200 範例與 400、409 範例）；planning、decisions（proposed 9：D-073、D-081–D-085、D-088–D-090）、TS 契約為最新（15 檔、109 型別）、hash、`render_db_tests --check`（13 檔）都 PASS；重產的 `checks/*.json` 已提交。完整的 OpenAPI 官方 validator 為 `NOT_RUN_UNAVAILABLE`（`spec-check.log`） |
| `make backend-test` | 0 | gofmt、`go vet`、`go test`（`GEMINUS_FULL_DIFFERENTIAL=1`）：66 個 package `ok`（含新的 `internal/results/compare`、`internal/comparisons`），沒有 FAIL；DB-backed 測試全部 SKIP（`backend-test.log`，在 0378204 的 tree 上；之後的 8b64c4e 只改 TS） |
| `go test -race -count=1 ./internal/results/... ./internal/comparisons/... ./internal/api/... ./internal/runs/` | 0 | 碰到的 package，5 個 `ok`，沒有 data race 報告（`race-touched.log`） |
| `make backend-race` | 0 | `go test -race -timeout 30m`：66 個 package `ok`，沒有 FAIL、沒有 data race 報告；DB-backed 測試同樣 SKIP（`backend-race.log`） |
| `make architecture-check` | 0 | `PASS architecture: 78 packages, 17 rules, 16 build configurations`（`internal/comparisons` 為 application layer）；5 個新 probe 在 `scripts/check-architecture` 的測試中，`TestProbesAreCaught` 通過（`make backend-test` 的 `scripts/check-architecture ok`） |
| `go test -count=1 -v ./internal/results/... ./internal/comparisons/` | 0 | `internal/results/compare` 18 個測試（含子測試共 69 個 PASS）、`internal/results/metrics`、`internal/comparisons` 5 個測試都 `ok`（`results-test.log`、`compare-unit-green.log`、`comparisons-unit-green.log`） |
| `make frontend-test` | 0 | lint、typecheck、unit：18 個檔案、445 個測試通過（`frontend-test.log`）。Node 為 22.22.0（`package.json` 要 24.21.0；本機沒有 Node 24，以 `npm ci --engine-strict=false` 安裝 lockfile 的依賴，lockfile 沒有改） |
| `make ai-eval` | 0 | `TestFakeProviderEval`、`TestReviewEval` PASS；只有 fake provider；`checks/ai-evals/*.json` 沒有差異。T-029 沒有改 AI context，跑它只是 regression（`ai-eval.log`） |
| `make backend-numeric` | 0 | 6 個 target 的組合語言沒有 FMA 也沒有平台的 math 呼叫（`internal/results/...` 含新的 `compare`）；GOAMD64=v3 build 的 numeric 測試（含 `internal/results/compare`）通過（`backend-numeric.log`） |
| `python mutate.py <root> mutants-unit.log unit` | 0 | 第 2 輪：39 個 unit mutant 全部被測試擋下（KILLED 39、SURVIVED 0、不能 build 0）；第 1 輪見「Mutation」 |
| `python mutate.py <root> mutants-db-build.log dbbuild` | 0 | 5 個 DB mutant 都套得上且能 build；它們的測試 NOT RUN |
| `make integration-test`、`make db-test`、`make db-go-test` | — | NOT RUN（沒有 PostgreSQL；見下） |

## 測試涵蓋（對應 TEST-022／023／024／009）

| 測試 | 驗證 | 驗收 |
|---|---|---|
| `TestDeltas`、`TestBaselineZero`、`TestGolden`（unit，`internal/results/compare`） | absolute／relative 的公式（含負的 baseline）、相同值為 0、baseline 0 與 -0 的 relative 為 null（`BASELINE_ZERO`）而 absolute 照算；手推的 golden 逐 byte 相同 | TEST-022、REQ-014 |
| `TestUnavailable`、`TestMissing`、`TestOutOfRange`、`TestNoNegativeZero`（unit） | unavailable 為 null 不是 0、沒有 delta、另一邊的值保留；queue 沒有 visit 時 length 照比；只在一個 run 有的 metric；超出範圍為 null 帶原因、schema-valid；沒有 -0 | REQ-014（缺值不偽造 percentage） |
| `TestDORAStaysUnavailable`（unit）、`TestComparisonClosedLoop` 的 DORA 部分（DB） | 沒有 mapping 的 DORA 在比較中仍是 unavailable、沒有 delta；generic 的 failure count 以自己的 path 比較、不冒稱 DORA | TEST-023、REQ-015 |
| `TestIncomparable`、`TestComparableAcrossSpellings`（unit） | §7 的每個條件各自造成恰好一個原因、全部同時時依固定順序；不可比時每個 metric incomparable、值並排、沒有 delta；1 day＝1440 minute、trace level 不影響 | REQ-014、D-088 (a) |
| `TestWarnings`、`TestTruncation`（unit） | 四種 warning 與其順序、`Warnings` 為 `[]` 不是 null；超過 10000 筆時保留前 10000 筆並加 warning、恰好 10000 筆時不加 | D-088 (b)(d) |
| `TestOrder`（unit） | 明確的 metric 順序；40 次不同的 map 順序得到相同的 bytes，`Statistics` 的順序也相同 | AGENTS.md §5（map 不決定順序） |
| `TestResolve`、`TestPathEscaping`（unit） | 每個 `Statistics` 的 path 以 `Resolve` 取得相同的值；32 個不是統計值、malformed、不正規拼法或不存在的 path 為 `ErrUnknownPath`；`~`、`/` 的 RFC 6901 escape | TEST-024 的基礎、D-090 (a) |
| `TestInconsistent`、`TestSameRun`（unit） | 21 種「result 不是該 run 的」或違反 Result contract 的情況，兩邊各自，都是 `ErrInconsistent`；同一個 run 為 `ErrSameRun` | D-089 (b)、fail closed |
| `TestContract`（unit） | Go 型別與 schema 的 parity、兩個正例 fixture 的 round trip、Go 與 Python 兩個 validator 對所有 15 個 Comparison fixture（2 個正例、13 個反例）結果相同、產生的比較是 schema-valid 且為 RFC 8785 | 契約（AGENTS.md §1） |
| `TestRoutesMatchContract`、`TestRunQuery`、`TestRefused`、`TestInput`、`TestDecodeResult`（unit，`internal/comparisons`） | route 與 roles 等於 `api-operations.json`（viewer 可以）；13 種 query 的接受與 400；錯誤對應與參數名；不一致的 result 不被當成 404；送給比較的正是 run 凍結的 input；result 嚴格解碼 | TEST-009（unit 部分）、D-089 (a) |
| DB：`TestComparisonClosedLoop` | 完成前 409（baseline 先）；viewer 比較 congested queue 的 baseline 與兩台 server：每個值等於兩個 Result 在同一 path 的值、每個 delta 在測試中重算、wait mean 47.5 分（SIMULATION_SPEC §10）、DORA、failure count、三種 role 相同 bytes、不寫 audit；角色對調時不可比、值並排 | TEST-022、TEST-023、REQ-014 |
| DB：`TestComparisonIncomparable` | 另一個 seed、replication 數、warmup、model version 各自恰好一個原因；另一個 trace level 可比 | D-088 (a) |
| DB：`TestComparisonRoleMatrix` | 三種 role 200；其他 organization 的成員 404；其他 organization 的 run 404 並指出參數，回應與不存在的 run 完全相同；未知 identity 403；沒有 token 401 | TEST-009、REQ-019 |
| DB：`TestComparisonRefusals` | 400 的 5 種 query；兩個都 queued 時 409 指 baseline；不存在的 scenario 404 先於 baseline 的 409；cancelled 在兩邊都是 409；event budget 失敗的 run 為 409 | API_DATABASE §6、D-089 (a) |
| Architecture probe（5 個） | compare 讀 storage、tenant、net/http 都被 generic-kernel 規則擋下；kernel（internal/results/metrics）與 `internal/ai/aicontext` 連結 `internal/comparisons` 被擋下 | AGENTS.md §2、§3 |
| TS：`contracts.test.ts`、`api.test.ts` | 不可比 fixture 的型別、golden 只含 contract 的 code、4 個 `@ts-expect-error`（warning 不是自由文字、reason 不是自由文字、必有 `incomparable_reasons`、沒有 trace level 的原因）；client 的 method、path、query、tenant selector、409 的對應、送出前拒絕錯的 ID 與同一個 run | D-089 (d)、AGENTS.md §5 |

### 先紅後綠

- **Go，`internal/results/compare`**：測試先寫好，套件只放能編譯但什麼都不做的 stub（`Compare` 回空的 Comparison、`Resolve` 回 nil……），18 個測試全部失敗（`compare-unit-red.log`，exit 1；`TestContract` 失敗在 schema 還沒有 `incomparable_reasons`，即 contract 尚未修改）；改 contract、實作後全部通過（`compare-unit-green.log`）。
- **Go，`internal/comparisons`**：同樣以 stub 先跑，5 個測試全部失敗（`comparisons-unit-red.log`），實作後通過（`comparisons-unit-green.log`）。
- **TS 契約型別**：`contracts.test.ts` 先寫好，以 T-029 之前產生的 `http.ts` 執行 typecheck：8 個錯誤（沒有 `ComparisonReason` 等三個型別、沒有 `incomparable_reasons`，以及 3 個未使用的 `@ts-expect-error`，`ts-contracts-red.log`，exit 2）；`make ts-contracts` 重產後 `make frontend-test` 通過。
- **TS client**：`api.test.ts` 先寫好，沒有 `compareSimulations` 時 typecheck 6 個錯誤、vitest 34 個中 4 個失敗（`ts-client-red.log`）；加上 method 後 typecheck 與 62 個測試通過（`ts-client-green.log`）。
- **Mutation 第 1 輪找到的缺口**：`keys-in-map-order` 存活（見下），先在 `TestOrder` 加上 `Statistics` 的順序檢查，第 2 輪該 mutant 被擋下。
- **DB-backed 測試**（`internal/api/comparisons_test.go`）在本 worktree 只能 SKIP，所以沒有它們的 red 或 green；它們先於 `internal/comparisons` 的實作寫好，以 `go vet` 確認能編譯。

## Mutation

`checks/evidence/2026-10-25-T-029/mutate.py`：每個 mutant 只替換一段確定的程式碼，先 `go vet`（不能 build 的 mutant 記為 BUILD、不算擋下：必須是測試而不是 compiler 擋下它），再跑指定的測試，之後還原。

- **第 1 輪**（`mutants-unit-r1.log`）：39 個中 34 個擋下、1 個存活、4 個不能 build。存活的 `keys-in-map-order`（不排序一個 map 的 key）：`Compare` 會把兩個 run 的 key 聯集後再排序，所以只有 `Statistics`（analyst 可引用的清單）依賴那個排序，而沒有測試檢查它的順序；補上測試（0378204）。4 個不能 build 的（`warmup-not-compared`、`baseline-patch-not-checked`、`horizon-by-spelling`、`absent-key-resolves`：把條件換成 `false` 後變數未使用）改寫成能編譯的形式（`&& false`）。
- **第 2 輪**（`mutants-unit.log`）：**39 個全部 KILLED**，0 存活、0 不能 build。涵蓋 coordinator 要求的類別：baseline 0（`baseline-zero-not-special`、`baseline-zero-drops-absolute`）、null 傳遞（`unavailable-summary-as-zero`、`unavailable-wait-as-zero`、`missing-as-both-unavailable`、`missing-scenario-ignored`）、不可比的偵測（seed、engine、version、manifest、replications、warmup、baseline patch、以拼法比 horizon、不可比卻保留 delta）、順序（`keys-in-map-order`、`union-not-sorted`）、role（`viewer-cannot-compare`）、query 與 tenant 相關的輸入（`any-id-kind`、`same-run-not-refused`、`repeated-parameter-accepted`）、錯誤對應（`not-ready-as-not-found`、`not-found-names-no-parameter`、`inconsistent-result-shown-as-404`）、fail closed（`result-of-another-run`、`lenient-result-decoding`）、公式、範圍、-0、warning、截斷、evidence path（`non-statistic-resolves`、`absent-key-resolves`、`bad-escape-accepted`）。
- **DB mutant**（`mutants-db-build.log`）：`db-status-not-checked`、`db-scenario-named-as-baseline`、`db-readiness-before-existence`、`db-results-swapped`、`db-route-not-registered`；5 個都套得上且能 build，測試 **NOT RUN**（`mutate.py <root> <log> db` 沒有 `GEMINUS_TEST_DATABASE_URL` 時拒絕執行）。
- **不列為 mutant**：拿掉 `m.Require(readRoles...)` 對「任何成員都可以」的讀取是 equivalent（非成員在 `tenant.DB.Run` 就被拒絕），改由 `viewer-cannot-compare` 檢查 role 的清單。Tenant 的 SQL 過濾在 `internal/storage/runs`（T-023 的程式與其 DB 測試），本任務沒有改它，所以沒有 SQL 的 mutant；比較自己的隔離由 `TestComparisonRoleMatrix`（DB，NOT RUN）檢查。

## Migration、RLS 與 grant 的影響

沒有。沒有新 migration、沒有新表或欄位、沒有新 SQL：讀取用 `internal/storage/runs` 既有的 `Get`、`GetResult`（每個 statement 以 `organization_id` 過濾，`simulation_runs`、`simulation_results`、`jobs` 的 FORCE RLS 與 grant 沿用 0001、0002、0009）。API 的 runtime role 只需要它已有的 SELECT。比較不寫任何資料，也不寫 audit。

## NOT RUN

完整清單在 `checks/evidence/2026-10-25-T-029/db-not-run.txt`，原因都是本 worktree 沒有 PostgreSQL：

- `make integration-test`、`make db-test`、`make db-go-test`（T-029 沒有新的 SQL suite 與 migration，SQL 部分只是重跑）。
- 新的 DB-backed Go 測試：`internal/api` 的 `TestComparisonClosedLoop`、`TestComparisonIncomparable`、`TestComparisonRoleMatrix`、`TestComparisonRefusals`（能編譯、`go vet` 通過，在此 SKIP）。
- 可能受影響的既有 DB 測試：`internal/api` 的所有測試（`internal/api/api.go` 多註冊一個 route）。
- 5 個 DB mutant 的測試。
- `make e2e-test`（比較的 UI 屬 T-032，還沒有使用 compareSimulations 的瀏覽器流程）；live AI（T-053 之前一律關閉）；完整的 OpenAPI 官方 validator（`NOT_RUN_UNAVAILABLE`）。

## 提出的決策（PROPOSED，agent 沒有批准任何一項）

| ID | 主題 | 建議（已實作） | 其他選項 |
|---|---|---|---|
| D-088 | 可比性、比較的統計值與順序、delta、缺值與 warning | (a) §7 逐項：`engine_version` 代表 numerical profile、window 的 tick、baseline patch 必須是 `[]`、不比 trace level；不可比時 200、`comparable=false`、列出所有原因、值並排、沒有 delta；(b) 比較所有統計值（mean／p50／p95、utilization、三個 wait 與 queue length），固定順序，maxItems 10000 並在超過時加 `METRICS_TRUNCATED`；(c) 公式照 §7、baseline 0 與缺值與範圍外的處理、CI 不進入 delta；(d) 四種 warning | (a) B：D-047 保證時允許不同 engine，C：不比 engine；不可比時 422；(b) 只比 mean，或只比 IR 宣告的指標 |
| D-089 | compareSimulations 的 HTTP binding 與 contract 變更 | query 的 400（指出參數）→ role → 404 → 409（baseline 先、404 先於 409）；同一個 run 為 400；不一致的 result 為 500；RFC 8785、不寫 audit；`incomparable_reasons`、封閉的 code 清單、`metric_path` pattern、maxItems 10000、if/then；kernel 與 adapter 分成 `internal/results/compare` 與 `internal/comparisons` | 同一個 run 允許（delta 全為 0） |
| D-090 | Metric path 的格式與 IR `metrics[]` 的位置 | (a) 從 Result 根開始、指到單一統計值的 RFC 6901 pointer，`Statistics`／`Resolve`；T-030 需對齊 T-024 的 summary 層級的 ALLOWED_METRIC_REFERENCES；(b) 每種宣告在 Result 中的位置（只有文件，沒有程式） | (a) 點號寫法；(b) 只比較宣告的指標 |

三項都依 AGENTS.md 只寫成 PROPOSED，`approved_by`／`approved_at` 為 null；T-029 的 gate 已加上這三項，D-090 另加進 T-030 與 T-032 的 gate（其 gate 文字寫「T-030、T-032 實作前」）。

## 與指示或規劃的差異

- **Package 位置**：tasks.json 規劃 `internal/results/http/` 與 `internal/results/compare/`。前者落在 generic kernel（`./internal/results/...`），architecture 規則禁止它碰 HTTP、tenant 與 storage，所以 adapter 改為 `internal/comparisons`（與 T-023、T-025、T-026、T-028 的做法相同）；已更新 tasks.json 的 planned outputs 與 scope、ARCHITECTURE §2、§6。
- **Contract 變更**：coordinator 允許在必要時改 contract。`maxItems` 1000 在 IR 的上限下不夠（單一 run 約 6000 筆），不可比的原因在原 schema 中沒有位置，自由文字的 reason 與 warnings 不能依 code 顯示兩語（REQ-022），所以改 schema（D-089 (d)）；Comparison 之前沒有任何實作或使用者。
- **TS client**：coordinator 只要求契約型別與其測試；依 AGENTS.md §5（新增 API 要有 generated client 的契約測試）另加了 `compareSimulations` 與測試。UI 與兩語字串仍屬 T-032。
- **D-090 也加進 T-030、T-032 的 gate**：這兩個任務的 status 沒有改。
- **`make backend-race` 與 `make backend-numeric`**：coordinator 要求的是碰到的 package 的 `go test -race`；完整的兩個 target 也跑了（見上表）。
- **Node 版本**：本機只有 Node 22.22.0，以 `--engine-strict=false` 安裝依賴後執行 `make frontend-test`（exit 0）；coordinator 的環境應以 Node 24 再跑一次。
- **驗收測試**：tasks.json 的 acceptance tests 仍只列 TEST-022、TEST-023、TEST-024；TEST-009 中比較的部分只加進 traceability 的 REQ-014。TEST-022 中「known Type7 values、population、n<2 CI=null」是 T-022 的 `internal/results/metrics` 測試（已存在、本任務沒有改）；本任務涵蓋的是 TEST-022 的比較部分。

## 殘餘風險

- **DB 與整合測試沒有在 agent 端執行**：四個新的 DB-backed 測試與 5 個 DB mutant 的期望值是依 T-023 的行為推論的（例如 congested queue 的 baseline wait mean 47.5 分、`TestRunFailures` 的 event budget 會讓 deterministic queue 的 2 個 replication 失敗）；第一次在 database 上執行時可能需要修正測試本身。
- **T-030 的對齊**（D-090 (a)）：T-024 的 ALLOWED_METRIC_REFERENCES 是 summary 層級（`/metrics/throughput`），comparison 與 `Resolve` 是統計值層級（`/metrics/throughput/mean`）；T-030 若不處理，analyst 引用統計值的 evidence 會被當成不在 allowed 清單中。本任務沒有改 `internal/ai`。
- **D-088 (a) 選 A 的後果**：同一個 version 在 engine 升版前後的 run 不可比（`ENGINE_VERSION_MISMATCH`），即使 D-047 保證沒用到新功能的模型輸出相同；使用者需要在新 engine 上重跑 baseline。
- **回應大小**：最多 10000 筆（約 2 MB）。T-030 把 comparison 放進 analyst 的 prompt 時要受 T-024 的 budget 限制（128 KiB），需要選擇或省略。
- **每次比較讀兩個完整的 Result**（1000 個 replication 時可能數 MB），沒有快取；目前與 getSimulationResults 相同，benchmark 屬 T-037。
- **UI 字串**：新的 code（9 個 reason、8 個不可比原因、4 個 warning）在 UI 的 message catalog 中還沒有兩語文案（T-032）；`apps/web` 的 i18n 測試只列舉 ErrorCode 與 ValidationCode，不會因此失敗。
- **D-088–D-090 待 owner 決定**：選其他選項時依各項「不採用時」修改；特別是 D-088 (a) 改為 B 或不可比時改回 422，會改 schema、fixtures 與 golden。

## Coordinator 的執行（72e370d）

`t029` 是從 3e56705（整合後的 head）開出的，所以 coordinator 以 fast-forward 把七個 commit 接到 `claude/lucid-meitner-vq1awo`，hash 不變（bf3bd76 … 72e370d）。之後在同一個 tree 上（`coordinator-environment.txt`：HEAD 72e370d、沒有其他變更、go1.27.3、Node 22.22.0、PostgreSQL 16.15）：

| 指令 | Exit | 說明 |
|---|---:|---|
| `make spec-check` | 0 | |
| `make backend-test` | 0 | |
| `make architecture-check` | 0 | |
| `make ai-eval` | 0 | |
| `make frontend-test` | 0 | Node 22.22.0；Node 24 由 CI 執行 |
| `make simulation-reference-test` | 0 | |
| `make backend-race` | 0 | |
| `make integration-test`（新的 `dt_test_t029`） | 0 | 32 個 package ok，沒有 FAIL |
| `make db-test`（另一個新的 `dt_test_t029m`，供下面兩項） | 0 | |
| `go test -count=1 -v -run TestComparison ./internal/api/`（`GEMINUS_REQUIRE_DB_TESTS=1`） | 0 | 四個新的 DB 測試在 database 上執行並 PASS，不是 SKIP：`TestComparisonClosedLoop`、`TestComparisonIncomparable`、`TestComparisonRoleMatrix`、`TestComparisonRefusals`。agent 推論的期望值（例如 baseline wait mean 47.5 分）不需要修改 |
| `mutate.py <git archive 副本> <log> db`（`GEMINUS_REQUIRE_DB_TESTS=1`） | 0 | 5 個 DB mutant 全部被測試擋下（不是編譯錯誤）：`db-status-not-checked`、`db-scenario-named-as-baseline`、`db-readiness-before-existence`、`db-results-swapped`、`db-route-not-registered` |

Log 與 exit code 在 `checks/evidence/2026-10-25-T-029/coordinator-*`。仍為 NOT RUN：`make e2e-test`（比較的 UI 屬 T-032）、live AI、OpenAPI 官方 validator、Node 24 上的 `frontend-test`（CI 執行）。D-088、D-089、D-090 仍是 PROPOSED，T-029 在 tasks.json 仍為 `not_started`。
