# T-054 驗證報告：dataset snapshot 讀取失敗回 503（H-022）：publishVersion、createSimulation，及第 2 輪的 patchDraft、resetDraft 與批准時的 draft 同步

**日期：2026-10-25｜狀態：兩輪的實作與 unit 測試完成；本 worktree 沒有 PostgreSQL，新增與受影響的 DB-backed 測試、DB mutant 與 `make integration-test` 都是 NOT RUN（由 coordinator 執行）；T-054 在 tasks.json 仍為 `not_started`（agent 不改狀態）。｜需求：REQ-003、REQ-011｜Gate：D-061、D-069 都已 APPROVED（H-019、H-021），本任務實作的是 H-022（決策頁 Q4）對 D-069 (b) 的補充，第 2 輪是 coordinator 依 Q4 的「一律」所做的延伸；沒有新的決策，也沒有改任何決策的狀態。｜驗收測試：TEST-009（route 的錯誤回答；另見「差異」）**

**本報告分兩輪。** 第 1 輪（fdb6cd2–2360c37）：publishVersion 與 createSimulation。第 2 輪（8fea25b 起，見最後的「第 2 輪」一節）：coordinator 決定依 owner 的 Q4「一律改為 503 … 讓 client 知道可以稍後重試」，在本任務中延伸到 patchDraft、resetDraft 與 approveProposal 的 draft 同步。第 1 輪各節保留當時的紀錄；被第 2 輪取代的敘述標有「（第 2 輪已改）」。

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
- （第 2 輪已改：`publishRefused` 併入 `refused`，message 改為 `nothing was changed (retry later)`，log 由 `refuse` 記下。）`http.go`：`publishRefused(err)`：`DatasetStorageError` → 503 `SERVICE_UNAVAILABLE`，message `the dataset snapshots could not be read; nothing was published (retry later)`，details `[{path:"", reason:"the dataset storage failed", code:"DATASET_STORAGE_FAILED"}]`（與 approval、scenarios 的 details 相同）；其他 error 交給既有的 `refused`。publish handler 以 WARN 記下 `request_id`、`organization_id`、`model_id` 與原因（不送給 client），`handlers` 因此帶 logger（`Register` 原本就收 logger，只是沒有存）。
- （第 2 輪已改：patchDraft、resetDraft 也回 503。）只有 publishVersion 用 `publishRefused`：`patchDraft`、`resetDraft` 也跑同一個 binding stage，但 H-022 只決定了 publishVersion 與 createSimulation，所以它們仍用 `refused`，同類的讀取失敗仍為 500（見「差異」與「殘留風險」）。
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
| 文件 | 已完成 | API_DATABASE §4、§6；D-069 的 H-022 補充（實作位置、Q4 不涵蓋 patchDraft／resetDraft（第 2 輪已改）；狀態不變）；traceability |

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
| `internal/models` `TestPublishRefused`（第 2 輪已改：由 `TestRefusedDatasetStorage` 取代） | unit | 503、message、details 完全相等，包一層也一樣；不送出原因；其他 error 與 `refused` 的回答相同（`reflect.DeepEqual`）；context、tenant、未知 error 原樣交給 httpapi；`refused`（patchDraft、resetDraft）不對應 storage 失敗（H-022 的範圍） |
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
- 程式路徑受影響、應隨 `db-go-test` 重跑的既有 DB 測試：`internal/models` 的 `TestDatasets` 與其他 publish／patch／reset 測試；`internal/api` 的 `TestRunDatasets`、`TestRunClosedLoop` 與其他 createSimulation 測試、`models_test.go` 的 model route 測試；`internal/proposals/approval` 的 `TestApprovalFreezesTheManifest` 與 edit 批准（`models.Service.Advance` 的 draft 同步現在回 `models.DatasetStorageError`，approval 仍回 500，行為不變；第 2 輪已改為 503）。
- DB mutant（第 1 輪的，已由第 2 輪的取代）：`db-models-handler-maps-like-every-route`、`db-models-cause-not-logged`、`db-models-draft-check-ignored`、`db-runs-freeze-unwrapped`、`db-runs-cause-not-logged`。
- `make backend-race` 的完整範圍：只對受影響的 package 跑了 `go test -race`（見上表）。
- 完整的 OpenAPI 官方 validator：`NOT_RUN_UNAVAILABLE`（`make spec-check` 的既有限制）。

## 差異

1. **publishVersion 的 HTTP 測試經 `models.Register`，不是 `api.Handler`**：`api.Handler` 目前沒有給 model service dataset storage（`models.Service{DB, Packs}`，`Datasets` 為 nil；import commit 凍結 dataset 是 T-018 的），所以部署的 API 對綁 dataset 的 base 一律先回「沒有 storage」的 503（沒有 details），根本不會讀 snapshot。DB 測試因此直接用 `api.Handler` 掛的同一個 `models.Register` handler，配上會失敗的 dataset storage，驗證 T-018 接上 storage 之後的行為。createSimulation 的測試經 `api.Handler`（runs 已有 object storage）。這一點寫在 API_DATABASE §4。
2. （第 2 輪已改：coordinator 決定延伸，見「第 2 輪」。）**`patchDraft`、`resetDraft` 沒有改**：它們跑同一個 binding stage（draft 的 issue），同類的讀取失敗仍為 500。H-022（Q4）只決定了 publishVersion 與 createSimulation，擴大範圍需要 owner 決定；因此對應放在 publishVersion 專用的 `publishRefused`，而不是共用的 `refused`，`TestPublishRefused` 也確認 `refused` 不對應它。
3. **TS 測試一開始就通過**：client 的錯誤處理是通用的，不需修改；以 mutant 確認測試有作用。
4. **驗收測試 TEST-009**：tasks.json 為 T-054 列的是 TEST-009（tenant／role matrix）。本任務的測試驗證的是 route 的錯誤回答與不寫入；兩個 DB 測試以 editor 經真正的 membership 與 RLS 執行，但沒有新增 role matrix 的案例（既有的 `TestRolesAndTenancy`、run 的 role 測試涵蓋）。Traceability 只加了 task，沒有加 test ID，避免過度宣稱。
5. **沒有抽出共用的 helper**：依指示沿用既有模式，models 與 runs 各有一份 `DatasetStorageError`／`bindingFailure`（與 approval、scenarios 相同），沒有改 approval 與 scenarios。

## 殘留風險

- **DB 測試未在本 worktree 執行**：兩個新的 DB 測試與 5 個 DB mutant 只確認能編譯；`TestPublishSnapshotStorageFailure` 依賴的 fixture（`CommitVersion`、`e.dataset`、clean draft 跟上新 version）與既有的 `TestDatasets` 相同，但仍要由 coordinator 實際執行。
- **部署的 publishVersion 還碰不到這條路徑**：在 T-018 讓 model service 取得 dataset storage 之前，綁 dataset 的 base 一律是「沒有 storage」的 503；`DATASET_STORAGE_FAILED` 的 503 只在 handler 配上 storage 時出現（DB 測試）。
- （第 2 輪已處理。）**`approveProposal` 的 draft 同步**（T-026，不在 T-054 範圍）：批准 edit proposal 時，revalidation 之後 `models.Service.Advance` 讓 clean draft 跟上新 version，會再讀一次 snapshot；這次讀取失敗現在是 `models.DatasetStorageError`，`approval.Refused` 只認得自己的 `DatasetStorageError`，所以仍回 500（與 T-054 之前相同）。D-069 (b) 的 503 寫的是 revalidation 的 binding stage；要讓這次讀取也回 503，需要在 `approval.Refused` 也對應 `models.DatasetStorageError`，建議另開任務。
- （第 2 輪已處理。）**`patchDraft`、`resetDraft` 的 500**（見「差異」2）：需要 owner 決定是否一併改為 503。
- **分類的範圍**：與 approval、scenarios 相同，`bindingFailure` 把 `datasets.Resolve` 所有非 context 的 error 都當成 storage 失敗，包括理論上的 server 錯誤（例如 schema 驗證器本身出錯、schema-valid 的 snapshot 解碼失敗）；這些會回 503「retry later」而不是 500。原因都寫在 WARN log。
- **四份相同的程式**：`DatasetStorageError`／`bindingFailure` 現在在 approval、scenarios、models、runs 各一份；規則改變時要同時改四處（`TestBindingFailure` 在每個 package 都有，會抓到不一致）。
- **worker 端不變**：`internal/runs/runner` 讀 snapshot 失敗有自己的 job failure code（CB-38），不在本任務範圍。
- **Node 版本**：frontend-test 在 Node 22.22.0 上執行，不是 `package.json` 要求的 24.21.0。

## 第 2 輪：延伸到 patchDraft、resetDraft 與批准時的 draft 同步

**決定**：coordinator 回覆第 1 輪的兩個未決點：owner 對 Q4 的選擇是「一律改為 503 … 讓 client 知道可以稍後重試」，所以在本任務中延伸到每個 dataset binding stage 會因讀 snapshot 而失敗的 route：patchDraft、resetDraft，以及 approveProposal 在新 version 之後的 draft 同步（`models.Service.Advance`）。D-069 的 H-022 補充記為「coordinator 依 H-022 Q4 的『一律』延伸到 patchDraft、resetDraft 與批准時的 draft 同步」；沒有改任何決策的狀態。第 1 輪的「差異」2 與「殘留風險」中 approval draft 同步、patchDraft／resetDraft 兩項因此已處理。

### Commit（第 2 輪，接在 2360c37 之後，沒有 push）

| Commit | 內容 |
|---|---|
| 8fea25b | 契約：OpenAPI `patchDraft`、`resetDraft` 的 503 改為 inline 回應（說明：draft 沒有改變、同一 If-Match 可重送、沒有 storage、讀不到 snapshot 的 `DATASET_STORAGE_FAILED`、原因只寫 log、request 未完成時沒有 details）與範例 `no_storage`、`storage_failed`；`approveProposal` 的說明與 503 說明涵蓋 clean draft 同步時的讀取；`publishVersion` 的 `storage_failed` 範例 message 改為 model route 共用的 `nothing was changed (retry later)`；重產的 `checks/contracts-check.json`（resolved local ref 841 → 843） |
| e87067a | `internal/models`（一個對應、一個記 log 的 helper）、`internal/proposals/approval`（`storageCause`）的實作與測試；TS 測試跟著新的範例；`mutate.py`（第 2 輪）與 `r2-*` 的 red／green、mutant log |
| 98d233a | 文件：API_DATABASE §4（所有會讀 snapshot 的 model 寫入與批准的 draft 同步、不寫入、可重送；approveProposal 一項）；D-069 的 H-022 補充（上述字句與實作位置） |
| （本 commit） | 本節、`r2-*` 的指令 log、`exit-codes.txt`、`db-not-run.txt`、`make spec-check` 重產的 `checks/*.json` |

### 每個 route 的變更（第 2 輪）

- **共同（`internal/models`）**：`refused` 是所有 model route 唯一的對應，`DatasetStorageError` → 503 `SERVICE_UNAVAILABLE`，message `the dataset snapshots could not be read; nothing was changed (retry later)`（與 approveProposal 的相同），details `[{path:"", reason:"the dataset storage failed", code:"DATASET_STORAGE_FAILED"}]`。第 1 輪的 `publishRefused` 移除。`handlers.refuse` 是唯一記 log 的地方（WARN：`request_id`、`operation_id`、`organization_id`、`model_id`、原因），publishVersion、patchDraft、resetDraft 的 handler 都經過它。分類仍在 `check`（`bindingFailure`），所以每個讀 snapshot 的地方都涵蓋。
- **patchDraft**：`PatchDraft` → `s.issues`（對 base 的 manifest）→ `check` 讀一次 snapshot；失敗 → 503，transaction rollback：沒有 `UpdateDraft`（revision 不變）、沒有 `model.draft.update` audit。這個 route 不用 Idempotency-Key；同一個 If-Match 可在 storage 恢復後重送。
- **resetDraft**：`ResetDraft` → `syncDraft`（latest version 的內容）→ `s.issues`（對該 version 的 manifest）讀一次；失敗 → 503，沒有新的 revision、沒有 `model.draft.reset` audit。
- **publishVersion**：行為與第 1 輪相同（兩次讀取都涵蓋），只有 message 改為共用的一句。
- **approveProposal（edit 的 draft 同步）**：`storageCause(err)` 認得 revalidation 的 `DatasetStorageError` 與 draft 同步的 `modelsvc.DatasetStorageError`，回傳原因；`Refused` 以它決定 503（兩者回答完全相同），handler 以它記下原因（WARN：`request_id`、`organization_id`、`proposal_id`、原因）。錯誤從 `apply` 回傳，整個 transaction rollback：已 INSERT 的 version、`SetLatest` 都撤回，proposal 仍是 ready 且未決定、沒有 `proposal.approve` audit、key 不存回答。approval 原本就 import `internal/models`（`modelsvc`，為了 `Advance`），所以沒有新的 package 依賴；`make architecture-check` PASS。
- **createSimulation**：沒有變。
- **共用與副本**：每個 package 一個對應（models 的 `refused`、approval 的 `storageCause`、runs 的 `Refused`、scenarios 的 `Refused`）；`DatasetStorageError`／`bindingFailure` 仍在 approval、scenarios、models、runs 各一份（跨 package 共用會讓 runs、scenarios 依賴 `internal/models` 或要新增共用 package，不是簡單的修改），每個 package 都保留自己的 `TestBindingFailure`。

### 測試（第 2 輪）

| 測試 | 種類 | 驗證 |
|---|---|---|
| `internal/models` `TestRefusedDatasetStorage`（取代 `TestPublishRefused`） | unit | `refused` 對 `DatasetStorageError`（直接、包在 publish 與 draft 同步的 error 裡）回 503、message、details 完全相等，不送出原因；沒有 storage 仍是沒有 details 的 503；context、tenant、未知 error 原樣交給 httpapi |
| `internal/proposals/approval` `TestRefusedDraftSyncStorage` | unit | `Refused(modelsvc.DatasetStorageError)`（直接與包一層）與 revalidation 的 503 `reflect.DeepEqual` 相同、不送出原因；`storageCause` 對兩種 error 都回原因、對 context 與其他 error 不認 |
| `internal/models` `TestDraftSnapshotStorageFailure` | DB，HTTP（`models.Register`）；**NOT RUN** | patchDraft 與 resetDraft：讀取失敗 → 503、details、`request_id`、body 合 schema、不含原因、WARN log 含原因與 model；讀取次數 1；什麼都沒寫（version 數、latest、draft 的 revision 與 hash、三種 audit 數）；讀取 deadline → 沒有 details 的 503、什麼都沒寫；恢復後同一 If-Match → 200、ETag 為下一個 revision、合 `Draft` schema、只多一筆對應的 audit；reset 之後 draft 的 hash 等於 base 的 |
| `internal/proposals/approval` `TestApprovalDraftSyncStorageFailure` | DB，HTTP（`approval.Register`）；**NOT RUN** | base 綁 dataset、clean draft；第 1 次讀取（revalidation）通過、第 2 次（draft 同步）失敗 → 503 `DATASET_STORAGE_FAILED`、message、不含原因、WARN log 含原因與 proposal、讀取次數 2；model 的 snapshot（latest、version 數、draft）不變、proposal 仍 ready 且未決定、沒有 `proposal.approve` audit、key 沒有 row；draft 同步的讀取 deadline → 沒有 details 的 503、同樣什麼都沒寫；恢復後同一 key → 201、新 version 是 latest、draft 跟上、proposal approved |
| `internal/models` `TestPublishSnapshotStorageFailure`（改寫） | DB；**NOT RUN** | 與第 1 輪相同的斷言，改用共用 helper（`boundModel`、`call`、`storageFailed`、`ended`）與新的 message |
| `apps/web` `api.test.ts` | TS unit | 跟著 OpenAPI 的新範例 message；client 不變 |

### 先紅後綠（第 2 輪）

- **紅**（`r2-unit-red.log`，exit 1）：在第 1 輪的程式上執行新測試：`internal/models` 的 `TestRefusedDatasetStorage`（`refused` 不認得 `DatasetStorageError`，原樣傳出 → 500 路徑）與 approval 的 `TestRefusedDraftSyncStorage`（`Refused(modelsvc.DatasetStorageError)` 原樣傳出 → 500）失敗。approval 的 `storageCause` 先以「只認 revalidation 的 error」的 stub 加入（即原本的行為），讓測試能編譯。
- **綠**（`r2-unit-green.log`，exit 0）。DB 測試 SKIP（NOT RUN），`go vet` 確認可編譯。
- 第 1 輪 `TestPublishRefused` 的最後一個斷言（`refused` 不對應 storage 失敗）是第 1 輪範圍的守門；coordinator 的決定改變了範圍，所以以 `TestRefusedDatasetStorage` 取代，改為斷言每個 model route 都對應（更嚴格，不是放寬）。

### Mutation（第 2 輪）

`mutate.py` 改寫為針對第 2 輪後的程式（第 1 輪的版本在 git 3093ebb，log 為 `mutants-unit.log`、`mutants-db-compile.log`）。

- **unit（29 個，全部 KILLED，`r2-mutants-unit.log`）**：models 的 `check` 不分類、`bindingFailure` 的 5 種、`refused` 不對應、送出原因、別的 code、沒有 details、500、message 改回 published、沒有 storage 的 503 帶 details；approval 的 `storageCause` 不認 draft 同步、記下包裝而不是原因、不認 revalidation、`Refused` 不對應、把所有 error 當成 storage；runs 的 11 個（同第 1 輪）。
- **DB（10 個，NOT RUN，都能編譯，`r2-mutants-db-compile.log`）**：publishVersion、patchDraft、resetDraft 的 handler 不經 `refuse`（不記 log）、`refuse` 不記 log、`syncDraft` 吞掉 draft 檢查的錯誤、`PatchDraft` 吞掉 issue 檢查的錯誤、approval 吞掉 `Advance` 的錯誤（→ 讀取失敗仍批准）、approval 不記 log、`freeze` 不分類、run 不記 log。

### 實際執行結果（第 2 輪，在 e87067a／98d233a 上）

| 指令 | Exit | 結果 |
|---|---|---|
| `go test -count=1 -v -run '^(TestRefusedDatasetStorage\|TestRefusedDraftSyncStorage\|TestRefused\|TestBindingFailure\|TestCheckStorageFailure\|TestDraftSnapshotStorageFailure\|TestPublishSnapshotStorageFailure\|TestApprovalDraftSyncStorageFailure)$' ./internal/models/ ./internal/proposals/approval/` | 1 → 0 | 紅（第 1 輪的程式）：2 個失敗；綠：全部 PASS，3 個 DB 測試 SKIP（`r2-unit-red.log`、`r2-unit-green.log`） |
| `mutate.py … unit` | 0 | 29 個 mutant 全部 KILLED（`r2-mutants-unit.log`） |
| `mutate.py … compile` | 0 | 10 個 DB mutant 都能編譯，檔案已還原（`r2-mutants-db-compile.log`） |
| `make backend-test` | 0 | 先 `go clean -testcache`；gofmt、`go vet`、`go test`（`GEMINUS_FULL_DIFFERENTIAL=1`）：63 個 package `ok`，沒有 FAIL；DB-backed 測試 SKIP（`r2-backend-test.log`） |
| `go test -race -count=1 ./internal/models/... ./internal/proposals/... ./internal/runs/... ./internal/api/... ./internal/scenarios/...` | 0 | 8 個 package `ok`，沒有 data race 報告（`r2-race-touched.log`） |
| `make architecture-check` | 0 | `PASS architecture: 75 packages, 17 rules, 16 build configurations`；approval 對 `internal/models` 的依賴是既有的（`r2-architecture-check.log`） |
| `make frontend-test` | 0 | lint、typecheck、unit：18 個檔案、440 個測試通過（TS 測試的 message 已更新）；Node 22.22.0（`r2-frontend-test.log`） |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 最後執行（本節寫好之後）：`PASS_LIMITED_SPEC_CHECKS`（42 個 operation，inline 範例都依 schema 驗證，含 patchDraft、resetDraft 新的 4 個；resolved local ref 843）；planning、decisions、TS 契約、hash、db tests 都 PASS；重產的 `checks/*.json` 已提交（`r2-spec-check.log`） |
| `make integration-test`、`mutate.py … db` | — | **NOT RUN**（沒有 PostgreSQL） |

### NOT RUN（第 2 輪）

- `make integration-test`（`db-test`、`db-go-test`；`db-go-test` 已包含 `./internal/models/...`、`./internal/proposals/...`、`./internal/runs/...`、`./internal/api/...`）。
- 新的 DB-backed 測試：`internal/models` 的 **`TestDraftSnapshotStorageFailure`**；`internal/proposals/approval` 的 **`TestApprovalDraftSyncStorageFailure`**。改寫的：`internal/models` 的 **`TestPublishSnapshotStorageFailure`**。第 1 輪的 `internal/api` **`TestRunSnapshotStorageFailure`** 仍是 NOT RUN。
- 程式路徑受影響的既有 DB 測試：`internal/models` 的所有 patch／reset／publish 測試（handler 經 `refuse`）、`internal/proposals/approval` 的 edit 批准（`TestApproveEdit`、`TestApproveKeepsADirtyDraft`、`TestApprovalFreezesTheManifest` 等，`Refused` 與 handler 改用 `storageCause`）。
- 10 個 DB mutant（`db-not-run.txt` 列出名稱）。
- 完整的 `make backend-race`（只跑受影響的 package）、完整的 OpenAPI 官方 validator。

### 殘留風險（第 2 輪）

- **DB 測試未在本 worktree 執行**：三個 DB 測試（另加第 1 輪的 run 測試）與 10 個 DB mutant 只確認能編譯。`TestApprovalDraftSyncStorageFailure` 假設 revalidation 對一個 binding 只讀一次 object（`datasets.Resolve` 每個 binding 讀一次），以讀取次數 2 斷言第 2 次是 draft 同步；若之後 revalidation 讀取次數改變，這個測試會明確失敗而不是默默通過。
- **部署時的差別**：publishVersion、patchDraft、resetDraft 在 T-018 讓 model service 取得 dataset storage 之前碰不到這條路徑（綁 dataset 一律是「沒有 storage」的 503）；approveProposal 的 draft 同步用批准自己的 object storage，部署時就會走到：edit 批准在 draft 同步讀取失敗時，現在回 503（原本 500），且同樣什麼都不 commit。
- **message 改變**：publishVersion 的 `storage_failed` message 從第 1 輪的 `nothing was published` 改為共用的 `nothing was changed`；第 1 輪尚未整合，client 只依 code 與 details（TS 測試已更新）。
- **分類的範圍與四份副本**：同第 1 輪（`bindingFailure` 把 `datasets.Resolve` 所有非 context 的 error 當成 storage 失敗；四個 package 各一份）。
- **未涵蓋**：worker 端（`internal/runs/runner`、`internal/proposals/generator`）有自己的 failure code，不在本任務範圍；`createModel` 的 `check` 沒有 manifest，不讀 snapshot。

## Coordinator 的執行（dfe9723）

Coordinator 把八個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（H-022 的 2f4c33f 之後，沒有衝突）：fdb6cd2 → 042ad4f、3093ebb → b859054、d347aec → 127506d、2360c37 → 9cd50d9、8fea25b → 9575125、e87067a → 5f1d1b6、98d233a → 07f1217、063018b → dfe9723。第 2 輪把 503 延伸到 patchDraft、resetDraft 與批准時的 draft 同步，是 coordinator 依 H-022 Q4 的選項文字（「一律改為 503，讓 client 知道可以稍後重試」）所做的決定，已記在 D-069 的 H-022 補充。

之後在 dfe9723 的 tree 上（`coordinator-environment.txt`），`spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`frontend-test`、`backend-race` 與新的 `dt_test_t054` 上的 `integration-test`（所有 migration 與 SQL suite，32 個 package ok，沒有 FAIL 或 SKIP；含 `TestPublishSnapshotStorageFailure`、`TestDraftSnapshotStorageFailure`、`TestApprovalDraftSyncStorageFailure`、`TestRunSnapshotStorageFailure` 與受影響的既有 DB 測試）都是 exit 0。10 個 DB mutant 在匯出的副本上以同一個 database 執行：10 個都被測試失敗擋下（`coordinator-mutants-db.log`、`coordinator-mutants-db-run.txt`）。本報告 NOT RUN 中的 DB 項目因此都已執行；OpenAPI 官方 validator 仍為 NOT RUN。Log 與 exit code 在 `checks/evidence/2026-10-25-T-054/coordinator-*`。
