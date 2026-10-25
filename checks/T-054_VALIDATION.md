# T-054 驗證報告：publishVersion 與 createSimulation 的 dataset snapshot 失敗回 503（H-022）

**日期：2026-10-25｜狀態：實作與 unit 測試完成；本 worktree 沒有 PostgreSQL，新增與受影響的 DB-backed 測試、DB mutant 與 `make integration-test` 都是 NOT RUN（由 coordinator 執行）；T-054 在 tasks.json 仍為 `not_started`（agent 不改狀態）。｜需求：REQ-003、REQ-011｜Gate：D-061、D-069 都已 APPROVED（H-019、H-021），本任務實作的是 H-022（決策頁 Q4）對 D-069 (b) 的補充；沒有新的決策，也沒有改任何決策的狀態。｜驗收測試：TEST-009（route 的錯誤回答；另見「差異」）**

範圍依 tasks.json 與 coordinator 的指示：`publishVersion` 與 `createSimulation` 的 dataset binding stage 讀不到或無法驗證 snapshot（infrastructure 錯誤，不是資料的 issue）時，與 `approveProposal`、`createManualScenario` 相同回 503 `SERVICE_UNAVAILABLE`，`details[0].code` 為 `DATASET_STORAGE_FAILED`；原因只寫入 log；request 本身被取消或逾時照一般的 context 錯誤處理；什麼都不寫入。Contract first（OpenAPI 的 503 與範例），測試先紅後綠，沿用 approval 與 scenarios 既有的 `bindingFailure`／`DatasetStorageError` 模式。

**沒有任何 live AI 呼叫**，沒有讀取或設定任何 API key，沒有新的 dependency（`apps/web` 以 `npm ci --engine-strict=false` 依 lockfile 安裝，lockfile 沒有改）。逐條輸出在 `checks/evidence/2026-10-25-T-054/`：`environment.txt`、`exit-codes.txt`、各指令的 log、`unit-red.log`／`unit-green.log`、`mutate.py` 與 `mutants-unit.log`、`mutants-db-compile.log`、`ts-client.log`、`db-not-run.txt`。

## Commit

Branch `t054`，從 2f4c33f 開出，沒有 push。

| Commit | 內容 |
|---|---|
| fdb6cd2 | 契約：OpenAPI `publishVersion` 與 `createSimulation` 的 503 改為 inline 回應：說明（什麼都不寫入、Idempotency-Key 不存回答、沒有 storage、讀不到 snapshot 的 `DATASET_STORAGE_FAILED`、原因只寫 log、request 未完成時沒有 details）與兩個範例 `no_storage`、`storage_failed`；重產的 `checks/contracts-check.json`（resolved local ref 839 → 841） |
| 3093ebb | `internal/models`、`internal/runs` 的實作與測試；`internal/api` 的 HTTP 整合測試；`apps/web` 的 client 測試；evidence：`environment.txt`、red／green log、`mutate.py`、`mutants-unit.log`、`mutants-db-compile.log`、`ts-client.log` |
| d347aec | 文件：API_DATABASE §4（publishVersion）與 §6（createSimulation）、D-069 的 H-022 補充說明實作位置與 Q4 不涵蓋的 route（狀態不變）、`planning/traceability.csv`（REQ-003、REQ-011 加上 T-054） |
| （本 commit） | 本報告、各指令的 log、`exit-codes.txt`、`db-not-run.txt`、`make spec-check` 重產的 `checks/*.json` |

## 每個 route 的變更

**原本為什麼是 500**：binding stage（`datasets.Resolve`）讀 snapshot 失敗時回 error 而不是 issue；`internal/models` 的 `check` 與 `internal/runs` 的 `freeze` 把這個 error 原樣往上傳，route 的 `refused`／`Refused` 不認得它，`httpapi.classify` 就當成未知錯誤回 500 `INTERNAL_ERROR`（並以 ERROR 記下原因）。

**publishVersion（`internal/models`）**

- `models.go`：新增 `DatasetStorageError`（`Unwrap` 回原因）與 `bindingFailure(ctx, err)`，規則與 `internal/proposals/approval`、`internal/scenarios` 的相同：request 已結束（`ctx.Err() != nil`），或 error 是 `context.Canceled`／`context.DeadlineExceeded`，就保留原本的 error；否則包成 `DatasetStorageError`。`check` 對 `datasets.Resolve` 的 error 呼叫它。一次 publish 會經過 `check` 兩次：candidate 的檢查，以及 version INSERT 之後、`advance` → `syncDraft` 讓 clean draft 跟上新 version 時的檢查；兩次都因此涵蓋。
- `http.go`：`publishRefused(err)`：`DatasetStorageError` → 503 `SERVICE_UNAVAILABLE`，message `the dataset snapshots could not be read; nothing was published (retry later)`，details `[{path:"", reason:"the dataset storage failed", code:"DATASET_STORAGE_FAILED"}]`（與 approval、scenarios 的 details 相同）；其他 error 交給既有的 `refused`。publish handler 以 WARN 記下 `request_id`、`organization_id`、`model_id` 與原因（不送給 client），`handlers` 因此帶 logger（`Register` 原本就收 logger，只是沒有存）。
- 只有 publishVersion 用 `publishRefused`：`patchDraft`、`resetDraft` 也跑同一個 binding stage，但 H-022 只決定了 publishVersion 與 createSimulation，所以它們仍用 `refused`，同類的讀取失敗仍為 500（見「差異」與「殘留風險」）。
- 什麼都不寫入：錯誤從 `tenant.DB.Run` 的 fn 回傳，整個 transaction rollback（包括第二次讀取之前已 INSERT 的 version、`SetLatest`、`idempotency.Claim` 的 row）；Idempotency-Key 只存成功的回答（既有規則），所以同一 key 可在 storage 恢復後重送。

**createSimulation（`internal/runs`）**

- `runs.go`：新增 `DatasetStorageError` 與 `bindingFailure`（同上）；`freeze` 對 `datasets.Resolve` 的 error 呼叫它。
- `http.go`：`Refused` 把 `DatasetStorageError` 對應到 503，message `the dataset snapshots could not be read; nothing was queued (retry later)`，details 同上；create handler 以 WARN 記下 `request_id`、`organization_id`、`model_version_id` 與原因。`Refused` 是所有 run route 共用的，但只有 `freeze`（create）會產生這個 error。
- 什麼都不寫入：run、job、`run_datasets`、`simulation.create` audit 與 idempotency response 都在同一個 transaction，錯誤時一起 rollback。

**兩個 route 都沒有變的**：沒有 object storage（`ErrDatasetsUnavailable`／`ErrStorageUnavailable`）仍是沒有 details 的 503；snapshot 存在但內容不符、不存在是 422 `DATASET_BINDING_INVALID`（資料的 issue）；request 的 context 錯誤仍由 `httpapi` 回 503 `the request did not complete in time`（沒有 details）。

## 稽核：scope 逐條

| Scope | 判定 | 證據／處理 |
|---|---|---|
| 讀不到或無法驗證 snapshot：503 `SERVICE_UNAVAILABLE`、`details[0].code` `DATASET_STORAGE_FAILED` | 已完成；DB 部分 NOT RUN | Unit：`TestPublishRefused`、`TestRefusedDatasetStorage`（code、message、details 完全相等）、`TestCheckStorageFailure`（`check` 對無法讀取的 storage 回 `DatasetStorageError`）。DB（NOT RUN）：`TestPublishSnapshotStorageFailure`（第 1 次或第 2 次讀取失敗）、`TestRunSnapshotStorageFailure`（經 `api.Handler`） |
| 與 approveProposal、createManualScenario 相同 | 已完成 | 同一個 `bindingFailure` 規則、同一個 details（path、reason、code）；`mutate.py` 的 `*-other-code`、`*-no-details`、`*-cause-sent` mutant 都被殺掉 |
| 原因只寫入 log | 已完成；DB 部分 NOT RUN | Unit：回應的 code、message、details 不含原因（`secret`）。DB：log 有 WARN、`request_id` 等於 `X-Request-ID`、含原因；回應 body 不含原因 |
| request 被取消或逾時照一般 context 錯誤處理 | 已完成；DB 部分 NOT RUN | `TestBindingFailure`（兩個 package，各 7 種情況：取消、逾時、request 已結束但 store 給自己的 error、request 還在但 error 是 context 的）；`TestCheckStorageFailure`（取消的 request、讀取 deadline）；`TestPublishRefused`／`TestRefusedDatasetStorage`（context error 原樣交給 httpapi）；DB：讀取回 `DeadlineExceeded` 時為沒有 details 的 503、不記成 storage 失敗 |
| 什麼都不寫入 | 已完成；DB NOT RUN | DB 測試比較失敗前後：publish 的 version 數、model 的 latest、draft 的 revision 與 hash、`model.publish` audit 數、該 key 的 idempotency row；run 的 `simulation_runs`、`jobs`、`run_datasets`、`simulation.create` audit、`createSimulation` 的 idempotency row 全為 0；storage 恢復後同一 key 成功一次（201／202） |
| 沿用既有模式 | 已完成 | approval／scenarios 的 `DatasetStorageError` + `bindingFailure` + `Refused` 的 case + handler 的 WARN log，逐字相同的規則與 details；沒有新的機制（見「殘留風險」：四份相同的程式） |
| Contract first：OpenAPI 503 與範例 | 已完成 | fdb6cd2；`make spec-check` 依 `common.schema.json#/$defs/Error` 驗證每個範例；範例的 message 與程式完全相同（unit 測試以同一字串比對） |
| 先紅後綠 | 已完成（unit）；DB NOT RUN | 見「先紅後綠」 |
| Mutation | 已完成（unit 24/24）；DB mutant NOT RUN（5 個都能編譯） | 見「Mutation」 |
| TS 契約測試 | 已完成 | `api.test.ts`：publishVersion 的 503 `storage_failed`（OpenAPI 範例）對應為 `unavailable` 並保留 `DATASET_STORAGE_FAILED`；client 不需修改（見「差異」） |
| 文件 | 已完成 | API_DATABASE §4、§6；D-069 的 H-022 補充（實作位置、Q4 不涵蓋 patchDraft／resetDraft；狀態不變）；traceability |

## 實際執行結果

在 worktree 中執行（`environment.txt`：Go 1.27.3、Node 22.22.0、4 CPU 與其他 agent 共用、沒有 PostgreSQL；`make backend-test` 前先 `go clean -testcache`）。Exit code 也在 `exit-codes.txt`。

| 指令 | Exit | 結果 |
|---|---|---|
| `make backend-test` | 0 | gofmt、`go vet`、`go test`（`GEMINUS_FULL_DIFFERENTIAL=1`）：63 個 package `ok`，沒有 FAIL；DB-backed 測試全部 SKIP（`backend-test.log`） |
| `go test -race -count=1 ./internal/models/... ./internal/runs/... ./internal/api/... ./internal/proposals/approval/... ./internal/scenarios/...` | 0 | 6 個 package `ok`，沒有 data race 報告；DB-backed 測試同樣 SKIP（`race-touched.log`）。完整的 `make backend-race` 沒有執行（T-054 沒有碰其他 package） |
| `make architecture-check` | 0 | `PASS architecture: 75 packages, 17 rules, 16 build configurations`（`architecture-check.log`） |
| `make frontend-test` | 0 | lint、typecheck、unit：18 個檔案、440 個測試通過（T-054 前 439，新增 1 個）。Node 22.22.0（`package.json` 要 24.21.0；本機沒有 Node 24，以 `npm ci --engine-strict=false` 依 lockfile 安裝）（`frontend-test.log`） |
| `go test -count=1 -v -run '^(TestBindingFailure\|TestCheckStorageFailure\|TestPublishRefused\|TestRefusedDatasetStorage\|TestRefused\|TestPublishSnapshotStorageFailure)$' ./internal/models/ ./internal/runs/` | 1 → 0 | 紅（stub）：5 個失敗；綠：全部 PASS，DB 測試 SKIP（`unit-red.log`、`unit-green.log`） |
| `mutate.py … unit` | 0 | 24 個 mutant 全部 KILLED（`mutants-unit.log`） |
| `mutate.py … compile` | 0 | 5 個 DB mutant 都能編譯（`go vet`），檔案已還原（`mutants-db-compile.log`） |
| `mutate.py … db`（沒有 database） | 1 | 依設計拒絕執行（沒有 `GEMINUS_TEST_DATABASE_URL` 時每個 mutant 都會「存活」） |
| `npx vitest run tests/api.test.ts -t "keeps why a publish was unavailable"` | 0；mutant 1 | 新的 TS 測試通過；把 `problemFromResponse` 改成丟掉 detail 的 `code` 時失敗，檔案已還原（`ts-client.log`） |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 在加入本報告之後執行：`PASS_LIMITED_SPEC_CHECKS`（69 個正例、130 個反例 fixture；42 個 OpenAPI operation，每個 inline 範例都依 schema 驗證，含新的 4 個；resolved local ref 841）；planning（54 個 task）、decisions（proposed 1：D-073）、TS 契約為最新（15 檔、106 型別）、hash、`render_db_tests --check`（13 檔）都 PASS；docs 101 個 markdown 檔（加上本報告）、77 個相對連結。重產的 `checks/contracts-check.json`（fdb6cd2）與 `checks/docs-check.json`（本 commit）已提交。完整的 OpenAPI 官方 validator 為 `NOT_RUN_UNAVAILABLE`（`spec-check.log`） |
| `make integration-test`、`make db-test`、`make db-go-test`、`mutate.py … db` | — | **NOT RUN**（沒有 PostgreSQL；見下） |

## 測試涵蓋

| 測試 | 種類 | 驗證 |
|---|---|---|
| `internal/models` `TestBindingFailure` | unit | 讀取失敗 → `DatasetStorageError`（`errors.Is` 仍找到原因）；6 種 context 情況保留原本的 error |
| `internal/models` `TestCheckStorageFailure` | unit | `check`（publish 兩次讀取所在）：無法讀取的 storage → `DatasetStorageError`；不存在的 snapshot → `/dataset_bindings/0/dataset_version_id` 的 issue、沒有 error；讀取 deadline、取消的 request → context error；沒有 manifest 不讀取 |
| `internal/models` `TestPublishRefused` | unit | 503、message、details 完全相等，包一層也一樣；不送出原因；其他 error 與 `refused` 的回答相同（`reflect.DeepEqual`）；context、tenant、未知 error 原樣交給 httpapi；`refused`（patchDraft、resetDraft）不對應 storage 失敗（H-022 的範圍） |
| `internal/runs` `TestBindingFailure` | unit | 同 models |
| `internal/runs` `TestRefusedDatasetStorage` | unit | 503、message、details 完全相等，包一層也一樣；不送出原因；沒有 object storage 仍是沒有 details 的 503；context、tenant、未知 error 原樣交給 httpapi |
| `internal/models` `TestPublishSnapshotStorageFailure` | DB，HTTP（`models.Register`）；**NOT RUN** | base 綁 dataset；第 1 次讀取（candidate）失敗與第 2 次讀取（version INSERT 後的 draft）失敗：503、details、`request_id` = `X-Request-ID`、body 合 `common.schema.json#/$defs/Error`、不含原因、WARN log 含原因與 request、讀取次數正確、什麼都沒寫；讀取 deadline：沒有 details 的 503、不記成 storage 失敗、什麼都沒寫；恢復後同一 key 201、version 3 凍結 base 的 manifest、audit 與 key 各加 1 |
| `internal/api` `TestRunSnapshotStorageFailure` | DB，HTTP（`api.Handler`）；**NOT RUN** | 同上，對 createSimulation：run、job、`run_datasets`、audit、key 都為 0；恢復後同一 key 202、1 個 run、1 個 queued job、1 個 binding row |
| `apps/web` `api.test.ts`「keeps why a publish was unavailable」 | TS unit | OpenAPI `storage_failed` 範例的回答 → `kind: "unavailable"`、code、status、message、`requestId`、details（含 `DATASET_STORAGE_FAILED`）、`retryAfterSeconds: null` |

既有的測試（`TestRefused`、`TestDatasets`、`TestRunDatasets` 等）沒有改，unit 部分都通過；DB 部分 NOT RUN（`db-not-run.txt`）。

## 先紅後綠

- **Go unit**：先加入只能編譯、行為與原本相同的 stub（`DatasetStorageError` 型別、`bindingFailure` 原樣回傳 error、`publishRefused` 就是 `refused`、`check`／`freeze` 不分類），再寫測試：`TestPublishRefused`、兩個 `TestBindingFailure`、`TestCheckStorageFailure`、`TestRefusedDatasetStorage` 共 5 個失敗（`unit-red.log`，exit 1；失敗訊息顯示原本的行為：原因原樣傳出、`check` 回 `reading dataset version …` 的原始 error）。`TestBindingFailure` 的 context 情況在 stub 下通過（stub 原樣回傳正是那些情況的正確答案），由 `*-binding-always-storage` 等 mutant 確認它們有作用。實作後全部通過（`unit-green.log`，exit 0）。
- **DB 測試**：沒有 database，無法紅也無法綠（SKIP，NOT RUN）；`go vet` 確認可編譯。依 stub 的行為，這兩個測試在實作前會得到 500（第一個斷言 `503` 即失敗）。
- **TS**：client 本來就保留每個錯誤的 details 與 code，所以新測試一開始就通過（特性測試，不是紅綠）；以 mutant（丟掉 detail 的 `code`）確認它會失敗（`ts-client.log`）。

## Mutation

`checks/evidence/2026-10-25-T-054/mutate.py`（T-028 harness 的形式：每個 mutant 替換一段唯一的程式碼、跑指定測試、期待失敗、還原檔案）。

- **unit（24 個，全部 KILLED，`mutants-unit.log`）**：models 的 `check` 不分類；`bindingFailure` 忽略 `ctx.Err()`、忽略 `Canceled`、忽略 `DeadlineExceeded`、一律 storage、一律不是 storage（models、runs 各 5 個）；`publishRefused` 不對應、送出原因、別的 code、沒有 details、500、message 說 saved；`refused` 對所有 route 對應 storage 失敗；runs 的 `Refused` 不對應、送出原因、別的 code、500、message 說 saved、沒有 storage 的 503 也帶 details。
- **DB（5 個，NOT RUN，都能編譯，`mutants-db-compile.log`）**：publish handler 用 `refused`（→ 500）、publish 不記 log、`syncDraft` 忽略 draft 檢查的錯誤（→ 第 2 次讀取失敗時仍發布）、`freeze` 不分類（→ 500）、run 不記 log。需要 `GEMINUS_TEST_DATABASE_URL`：`python checks/evidence/2026-10-25-T-054/mutate.py <repo> <log> db`。

## NOT RUN

沒有 PostgreSQL（`environment.txt`），以下都沒有執行，由 coordinator 在一次性 database 上執行（細節：`db-not-run.txt`）：

- `make integration-test`（`make db-test` 與 `make db-go-test`；T-054 沒有改 migration 或 SQL suite，`db-go-test` 已包含 `./internal/models/...`、`./internal/runs/...`、`./internal/api/...`）。
- 新的 DB-backed 測試：`internal/models` 的 **`TestPublishSnapshotStorageFailure`**；`internal/api` 的 **`TestRunSnapshotStorageFailure`**。
- 程式路徑受影響、應隨 `db-go-test` 重跑的既有 DB 測試：`internal/models` 的 `TestDatasets` 與其他 publish／patch／reset 測試；`internal/api` 的 `TestRunDatasets`、`TestRunClosedLoop` 與其他 createSimulation 測試、`models_test.go` 的 model route 測試；`internal/proposals/approval` 的 `TestApprovalFreezesTheManifest` 與 edit 批准（`models.Service.Advance` 的 draft 同步現在回 `models.DatasetStorageError`，approval 仍回 500，行為不變）。
- DB mutant：`db-models-handler-maps-like-every-route`、`db-models-cause-not-logged`、`db-models-draft-check-ignored`、`db-runs-freeze-unwrapped`、`db-runs-cause-not-logged`。
- `make backend-race` 的完整範圍：只對受影響的 package 跑了 `go test -race`（見上表）。
- 完整的 OpenAPI 官方 validator：`NOT_RUN_UNAVAILABLE`（`make spec-check` 的既有限制）。

## 差異

1. **publishVersion 的 HTTP 測試經 `models.Register`，不是 `api.Handler`**：`api.Handler` 目前沒有給 model service dataset storage（`models.Service{DB, Packs}`，`Datasets` 為 nil；import commit 凍結 dataset 是 T-018 的），所以部署的 API 對綁 dataset 的 base 一律先回「沒有 storage」的 503（沒有 details），根本不會讀 snapshot。DB 測試因此直接用 `api.Handler` 掛的同一個 `models.Register` handler，配上會失敗的 dataset storage，驗證 T-018 接上 storage 之後的行為。createSimulation 的測試經 `api.Handler`（runs 已有 object storage）。這一點寫在 API_DATABASE §4。
2. **`patchDraft`、`resetDraft` 沒有改**：它們跑同一個 binding stage（draft 的 issue），同類的讀取失敗仍為 500。H-022（Q4）只決定了 publishVersion 與 createSimulation，擴大範圍需要 owner 決定；因此對應放在 publishVersion 專用的 `publishRefused`，而不是共用的 `refused`，`TestPublishRefused` 也確認 `refused` 不對應它。
3. **TS 測試一開始就通過**：client 的錯誤處理是通用的，不需修改；以 mutant 確認測試有作用。
4. **驗收測試 TEST-009**：tasks.json 為 T-054 列的是 TEST-009（tenant／role matrix）。本任務的測試驗證的是 route 的錯誤回答與不寫入；兩個 DB 測試以 editor 經真正的 membership 與 RLS 執行，但沒有新增 role matrix 的案例（既有的 `TestRolesAndTenancy`、run 的 role 測試涵蓋）。Traceability 只加了 task，沒有加 test ID，避免過度宣稱。
5. **沒有抽出共用的 helper**：依指示沿用既有模式，models 與 runs 各有一份 `DatasetStorageError`／`bindingFailure`（與 approval、scenarios 相同），沒有改 approval 與 scenarios。

## 殘留風險

- **DB 測試未在本 worktree 執行**：兩個新的 DB 測試與 5 個 DB mutant 只確認能編譯；`TestPublishSnapshotStorageFailure` 依賴的 fixture（`CommitVersion`、`e.dataset`、clean draft 跟上新 version）與既有的 `TestDatasets` 相同，但仍要由 coordinator 實際執行。
- **部署的 publishVersion 還碰不到這條路徑**：在 T-018 讓 model service 取得 dataset storage 之前，綁 dataset 的 base 一律是「沒有 storage」的 503；`DATASET_STORAGE_FAILED` 的 503 只在 handler 配上 storage 時出現（DB 測試）。
- **`approveProposal` 的 draft 同步**（T-026，不在 T-054 範圍）：批准 edit proposal 時，revalidation 之後 `models.Service.Advance` 讓 clean draft 跟上新 version，會再讀一次 snapshot；這次讀取失敗現在是 `models.DatasetStorageError`，`approval.Refused` 只認得自己的 `DatasetStorageError`，所以仍回 500（與 T-054 之前相同）。D-069 (b) 的 503 寫的是 revalidation 的 binding stage；要讓這次讀取也回 503，需要在 `approval.Refused` 也對應 `models.DatasetStorageError`，建議另開任務。
- **`patchDraft`、`resetDraft` 的 500**（見「差異」2）：需要 owner 決定是否一併改為 503。
- **分類的範圍**：與 approval、scenarios 相同，`bindingFailure` 把 `datasets.Resolve` 所有非 context 的 error 都當成 storage 失敗，包括理論上的 server 錯誤（例如 schema 驗證器本身出錯、schema-valid 的 snapshot 解碼失敗）；這些會回 503「retry later」而不是 500。原因都寫在 WARN log。
- **四份相同的程式**：`DatasetStorageError`／`bindingFailure` 現在在 approval、scenarios、models、runs 各一份；規則改變時要同時改四處（`TestBindingFailure` 在每個 package 都有，會抓到不一致）。
- **worker 端不變**：`internal/runs/runner` 讀 snapshot 失敗有自己的 job failure code（CB-38），不在本任務範圍。
- **Node 版本**：frontend-test 在 Node 22.22.0 上執行，不是 `package.json` 要求的 24.21.0。
