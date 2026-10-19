# T-023 驗證報告：Run API、worker 與 cancellation integration

**日期：2026-10-19｜狀態：待獨立 review（`planning/tasks.json` 的狀態未改，由 reviewer 決定）｜需求：REQ-011、REQ-012、REQ-019｜Gate：D-007、D-011、D-038、D-039（皆 APPROVED；D-038／D-039 的 gate 為「T-023 對外提供 run 結果前」，H-004）；ADR-024（ACCEPTED，H-010）；D-044（APPROVED，H-010）｜驗收測試：TEST-014、TEST-019、TEST-020、TEST-021、TEST-026**

範圍依 tasks.json：

1. 凍結 version／patch／config／datasets／engine／limits 並以 202 入列；
2. checkpoint cancellation、跨 retry 的 durable logical-run budget；
3. lease-fenced atomic results、progress／SSE 與 trace authorization；
4. 撤權（成員被移除或降級）時關閉已開的 SSE stream，或在送出下一個事件前重新檢查 membership 與 role（TEST-026，T-010 review L6）；
5. simulation 的 job handler 是 adapter，不在 generic kernel，另立 package 並記錄名稱（T-019 round 2 L3）。

T-023 是 domain-neutral 的：H-016 把 P0 domain 改為電子組裝／測試線，但 kernel 擴充（T-047–T-050）不屬於本任務，這裡沒有加入任何 domain 專屬的程式。Run service 與 handler 只經 `PackLookup` 使用部署登錄的 pack（目前與 API 相同，只有 software delivery 範例 pack）。

逐條輸出在 `checks/evidence/2026-10-19-T-023/`：`exit-codes.txt`、`environment.txt`、各指令的 log、`mutations.txt`（本輪的 mutation 與紅→綠紀錄）、`baseline-integration.log`（接手時的兩個 commit 在修正前的 integration 結果）。

## Commit

| Commit | 內容 |
|---|---|
| T-023a（ebcfa1e） | migration 0009（run 的生命週期、run 跟隨 job、result 與 trace 的約束）、`engine.Credit` 分批授予 event budget、`internal/storage/objects`（D-044 介面與 filesystem adapter）、object storage 設定 |
| T-023b（75039b7） | `internal/runs`（service 與 HTTP adapter）、`internal/runs/runner`（job handler）、`internal/storage/runs`、`internal/storage/snapshots`、worker audit event、`cmd/worker` 登錄 handler、契約（`CancelSimulation`、trace 下載回 `TraceArtifact`）、文件 |
| T-023c（本輪：c6626b9、2f5ed5b、a27c10a、855a282；本報告另一個 commit） | 0009 凍結 run 的 identity；architecture 規則 `job-handlers-only-in-the-worker`；TS 產生器的封閉空物件、run 的契約範例；四個補上的測試 |

T-023a／b 是前一個 agent 的工作，接手時未驗證。本輪先 rebase 到 `claude/lucid-meitner-vq1awo`（沒有衝突；migration 編號 0009 未與 main 的 0008 衝突），以修正前的程式跑一次 integration（`baseline-integration.log`，exit 0），再逐條稽核、補測試與修正，最後 rebase 到 2c9332c 後重跑全部驗證。

## 稽核：scope 逐條

| Scope | 判定 | 證據／處理 |
|---|---|---|
| 1. 凍結並 202 入列 | 已完成 | `runs.Service.Create`：先查 role 與 Idempotency-Key，再在同一 transaction 凍結 version（只接受 `mv_`，draft 為 422）、canonical patch（inline 或 saved scenario，皆經 scenario patch policy 與完整 validator）、effective model hash（並確認 engine 重算得同值）、config 與 hash、整份 dataset manifest 與 hash、engine version、server 的 execution limits（client 無法設定）；run、job、audit、idempotency response 一起 commit 後才回 202。`TestRunClosedLoop`、`TestRunRequestRefusals`、`TestRunIdempotency`、`TestScenarioRun`、`TestRunDatasets` |
| 2. Checkpoint cancellation、跨 retry 的 budget | 已完成；補一個測試 | Wall clock 從第一次開始起算（`execution_deadline_at` 不重設），events 以 durable reservation 分批授予，之前 attempt 保留的量算作已用。`TestEventBudgetAcrossRetries`、`TestWallClockAcrossRetries`、`TestCancelQueued`、`TestCancelRunning`、`TestALateCancellationWins`。**缺口**：attempt 執行中請求的取消在 attempt crash、reaper 重排後是否仍有效沒有測試 → 新增 `TestACancellationOutlivesACrash`；單一 attempt 超過 wall time 沒有 DB 端測試 → 新增 `TestTheWallClockEndsAnAttempt` |
| 3. Lease-fenced results、progress／SSE、trace 授權 | 已完成 | Result、run completed、audit、job completed 在 `Finish` 的同一 transaction（job 先鎖、再鎖 run，看到取消請求即改為 cancelled）；progress 與 credit 都在 `Checkpoint` 的 lease fence 下寫入。Trace 只由 run ID 要求，API 授權後讀 object，大小與 SHA-256 不符或 object 不見一律 500、不送 bytes（D-044）。`TestNoResultAfterALostLease`、`TestACrashedAttemptIsRunAgain`、`TestCancelRacesCompletion`、`TestRunRoleMatrix`、`TestRunClosedLoop`（竄改與刪除 trace object）。**缺口**：API 的 UPDATE grant 涵蓋 `simulation_runs` 的所有欄位，0001 只凍結 input，所以 API login 可以改 queued／running run 的 `name`、`created_by`、`created_at` → 0009 補上 identity 不可改（所有 role，含 owner；尚未部署，可修改） |
| 4. 撤權時的 SSE（TEST-026） | 已完成；補一個測試 | 每次 poll（預設 1 秒）在新的 REPEATABLE READ transaction 中重新查 membership 與 role 再讀 run，失敗即結束 stream；keep-alive 也只在檢查成功後才寫。降級為 viewer 仍可讀 run（SECURITY §2），stream 繼續。`TestStreamEndsWhenTheMemberIsRemoved` 只涵蓋「移除後 run 又變了」的情形 → 新增 `TestAnIdleStreamEndsWhenTheMemberIsRemoved`（run 不變時也在下一次 poll 結束） |
| 5. Handler 是 adapter、package 名稱 | 已完成；補一條規則 | `internal/runs/runner`（handler）、`internal/runs`（service＋HTTP）、`internal/storage/runs`（SQL）；名稱與理由記在 ARCHITECTURE §6（tasks.json 原規劃的 `internal/simulation/service`、`internal/simulation/http` 會落在 generic kernel 之下）。**缺口**：ADR-024 說 handler 只由 `cmd/worker` 登錄，但沒有任何檢查擋住 API 連結 handler → 新增規則 `job-handlers-only-in-the-worker` |
| 每個 API 變更有 OpenAPI＋範例＋產生的 client 契約測試 | 補上 | 契約已改 `cancelSimulation`（`CancelSimulation`）與 `downloadSimulationTrace`（`TraceArtifact`），但沒有範例，且產生的 TS 型別 `CancelSimulation = Record<string, unknown>` 接受任何 member → 產生器把封閉空物件改為 `Record<string, never>`（只影響這個型別），`contracts.test.ts` 加正反例；`testdata/contracts` 加 `CancelSimulation` 正反例、失敗的 `Run`（CB-38 的暫定對應）與 `ProgressSnapshot` |
| Idempotency replay 前仍檢查權限（API_DATABASE §7） | 程式正確；補測試 | `Create`、`Cancel` 都在 `idempotency.Claim` 之前 `Require`。新增 `TestARevokedMemberGetsNoReplay`：降級後的 replay 為 403、移除後為 404，不寫任何東西 |

沒有發現錯誤的行為（測試失敗或違反 spec 的程式）；發現的是上表四個缺口（一個 DB 約束、一條架構規則、TS 型別過寬、契約範例缺漏）與四個未測的路徑。

## 實際執行結果

在 worktree 中執行（HEAD 見 `environment.txt`，先 `go clean -testcache`）。DB 為本輪自建的一次性 PostgreSQL 16.15 cluster（`initdb`、trust、Unix socket、port 55444、`listen_addresses=''`），資料庫 `dt_test_t023_final`，跑完後以 `pg_ctl stop` 停止。

**與指示的差異**：指示要求 cluster 放在 scratchpad 之下。Sandbox 只讓 root 在 scratchpad 建立檔案（`postgres` 使用者可以 mkdir，但建立任何檔案都是 Permission denied，777 的目錄也一樣），PostgreSQL 又不能以 root 執行，關閉 sandbox 的請求被拒；因此 cluster 放在 `postgres` 使用者的 home（`/var/lib/postgresql/geminus-t023-v2`，socket 也在那裡），其餘條件不變。第一次嘗試的 URL 以 `user=postgres` query 參數覆蓋了測試 login，那次 run 無效、已中止，整個 cluster 重建後才開始計算結果。

Log 與 exit code 在 `checks/evidence/2026-10-19-T-023/`（`exit-codes.txt`）；環境（`environment.txt`）：HEAD 855a282、Go 1.27.2、Node 22.22.0、PostgreSQL 16.15、4 CPU。

| 指令 | Exit | 說明 |
|---|---|---|
| `make backend-test` | 0 | gofmt、go vet、39 個 package ok；沒有 DB URL，DB-backed 測試在這裡 skip（NOT RUN），由下面的 integration 以 `GEMINUS_REQUIRE_DB_TESTS=1` 執行 |
| `make backend-race` | 0 | 39 個 package ok（`-race`） |
| `make architecture-check` | 0 | 49 packages、12 rules（新增 `job-handlers-only-in-the-worker`）、16 build configurations |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 重產的 `checks/*.json` 中只有 `docs-check.json` 的 markdown 數因本報告而由 87 變 88（`spec-check-status.txt`），已隨本報告 commit；`contracts-check.json` 的新 fixture 數已在 T-023c commit；db/tests 與 TS contracts 皆為最新 |
| `make frontend-test` | 0 | lint、typecheck、18 個檔案 413 個 test（含 `contracts.test.ts` 的新正反例）；Node 22.22.0（見 NOT RUN） |
| `make integration-test`（新建的 `dt_test_t023_final`） | 0 | migration 0001–0009 up／down／up 與 6 個 SQL suite（含 `runs.sql` 的 identity 列），再以 API／worker 的 runtime login 跑 `db-go-test` 的 20 個 package（`-race`，`-p 1`），0 FAIL、0 SKIP；唯一的 NOT RUN 是 import 與 AI 的 job handler（T-016、T-025） |
| `go test -race -count=1 ./internal/simulation/...`（tasks.json 的 verification command） | 0 | simulation、des、engine、trace |
| `go test -race -p 1 -count=1 -v`（runs、runs/runner、storage/objects、engine、des、config、api、cmd/worker；`t023-go-tests.log`） | 0 | 162 個 top-level test 通過，0 FAIL、0 SKIP；`TestCancelRunning` 量到取消在請求後 16 ms 生效（測試的 checkpoint 間隔為 20 ms；正式預設 1 s，不是 benchmark） |
| 修正前的 baseline：`make integration-test`（接手的 T-023a／b，rebase 到 3adad92 後，`dt_test_t023_base`；`baseline-integration.log`） | 0 | 接手的程式在修正前就通過全部 integration |

## 測試涵蓋

| 測試 | 驗證 | 驗收 |
|---|---|---|
| `TestRunClosedLoop` | 202 後 run、job（payload 只有 run 與 request ID）、`simulation.create` audit 都已 commit；queued 時 results 與 trace 為 409 `RESULT_NOT_READY`；worker 完成後 Result、digest、trace（`Content-Disposition: attachment`、hash 與 row 相同）；trace object 被竄改或刪除時 500 且不含 events；worker 的 `simulation.complete` audit；list | REQ-011、REQ-012、D-044 |
| `TestRunRoleMatrix` | editor／owner 建立、viewer 403；其他 organization 的成員、陌生人、其他 organization 的 version 一律 404；所有讀取路徑（get、results、trace、list、SSE）與取消的 tenant／role 矩陣；不合格的 path ID 400 | TEST-009（runs）、REQ-019 |
| `TestRunRequestRefusals` | draft ID、replications 範圍、patch 與 scenario 同時或都沒有、未知 member、warmup ≥ horizon、horizon > 365 天、patch 不在 scenario allowlist、patch 讓 model 無效、不存在或屬於其他 version 的 scenario：各自的 code 與 JSON Pointer；缺 Idempotency-Key 400、錯 Content-Type 415；被拒的 request 不寫任何 run 或 job；沒有 object storage 時 sampled trace 為 503 | D-011、D-029 |
| `TestRunIdempotency`、`TestARevokedMemberGetsNoReplay` | 同 key 同 body（含 12 個並行 retry）回同一個 202 且只有一個 run／job，不同 body 409；key 屬於使用者；降級後 create／cancel 的 replay 403、移除後 404，不寫 run 或 audit | D-011、API_DATABASE §7 |
| `TestRunDeterminism` | 同一凍結 input 在 worker 的 replication pool 為 1、4、2 時 digest 相同；sampled trace 不改 metrics（只改 config hash）；served result 重算的 digest 等於存下的 | TEST-019 |
| `TestScenarioRun` | saved scenario 的 patch 被凍結並記名；effective hash 與 version 不同；baseline 的 queue wait 為 SIMULATION_SPEC §10 的 47.5 | REQ-010、REQ-011 |
| `TestRunDatasets` | run 綁定 version 凍結的 manifest；之後的 dataset version 不改既有 result，也不改同一 version 的新 run；bytes 被改時入列為 422（`/dataset_bindings/0/content_hash`），已入列的 run 在 worker 失敗（`RUN_INPUT_INVALID`）；object 不見為 422；patch 不能引入 version 沒綁的 dataset | TEST-014 |
| `TestRunFailures` | 超過 frozen event budget 的 run failed、不重試、沒有 result、保留量不超過上限；error 為契約 envelope（`MODEL_VALIDATION_FAILED`，`details[0]` 為 `EVENT_BUDGET_EXCEEDED` 與 `/execution_limits/max_events`，request ID 為建立 run 的 request）；取消已失敗的 run 為 409 | TEST-021、CB-38 |
| `TestCancelQueued` | queued run 與 job 一起立即 cancelled、不再被執行；replay 回同一 body；同 key 用在另一個 run 409；已取消的 run 再取消回同一 run、不寫 audit；body 必須是 `{}`；已完成的 run 409 | TEST-021 |
| `TestCancelRunning` | running run 的取消請求在下一個 checkpoint 生效：job 在同一 attempt cancelled、沒有 result、保留的 events 遠少於整個 run 所需；請求與結束各一筆 audit；實測取消時間記在 log（目標 ≤5 s，不是 benchmark） | TEST-021 |
| `TestCancelRacesCompletion` | 24 輪隨機時間點的取消與完成：200 一定是 cancelled 且沒有 result，409 一定是 completed 且恰一個 result，不會兩者都有 | TEST-021 |
| `TestStreamFollowsTheRun`、`TestStreamEndsWhenTheMemberIsRemoved`、`TestAnIdleStreamEndsWhenTheMemberIsRemoved`、`TestStreamEndsWithItsToken` | 只接受 `Authorization` header（URL 中的 token 401）；第一個事件為最新 snapshot，revision 遞增、`id` 為 `<run>:<revision>`，靜默時 keep-alive，終態後結束；重連得到最新 snapshot，`Last-Event-ID` 只接受本 run；成員被移除後（run 有變化或完全不變）在下一次 poll 結束、不再送事件，重連 404；降級為 viewer 的 stream 繼續；token 到期即結束 | TEST-026 |
| `TestEventBudgetAcrossRetries`、`TestWallClockAcrossRetries` | 失敗的 attempt 保留的 events 算作已用：有空間時 retry 完成且 digest 與不中斷的 run 相同，沒有空間時 `EVENT_BUDGET_EXCEEDED`（不重試），保留量從不超過 max_events；deadline 與 started_at 不被 retry 重設，過了 deadline 的 retry 立即 `WALL_TIMEOUT_EXCEEDED` 且不保留 events | D-007 |
| `TestTheWallClockEndsAnAttempt` | 一個 attempt 超過 1 秒的 wall time：`WALL_TIMEOUT_EXCEEDED`、不重試、沒有 result，deadline 為 started_at＋1 s，attempt 在 5 秒內結束 | D-007、TEST-021 |
| `TestACrashedAttemptIsRunAgain`、`TestNoResultAfterALostLease` | crash 的 attempt 之後什麼都寫不進去；reaper 重排、run 跟隨（queued、progress 歸零），下一個 attempt 完成且只有一個 result；lease 在 finish 前過期時不存 result（孤兒 trace 留給 maintenance） | TEST-020 |
| `TestACancellationOutlivesACrash` | attempt 執行中請求的取消在 crash 與 reaper 之後仍有效：下一個 attempt 在保留任何 events 前以 cancelled 結束 job（attempt 2）、沒有 result、worker 的 `simulation.cancelled` audit | TEST-020、TEST-021 |
| `TestALateCancellationWins`、`TestRefusedAttempts`、`TestOneJobPerRun` | engine 最後一個 checkpoint 之後的取消仍然勝出；engine version 不符與 payload 不合格立即失敗、不重試；一個 run 只能有一個 job | TEST-021、D-011 |
| `TestRoutesMatchContract`、`TestRefused`、`TestRunError`、`TestLastEventID`、`TestConfigIssues`、`TestCursor`（`internal/runs`） | routes 與 `planning/api-operations.json` 的 roles 相同；service error 對應的 code；run failure 的 envelope 對照（CB-38）與 schema；Last-Event-ID 解析；config 的 ticks 檢查；cursor 綁定 organization、model 與 list | D-012、D-025 |
| `des/credit_test.go`、`engine/credit_test.go` | 分批授予的 budget 不改結果（批次大小、第一批、worker 數）；不超過 limit；Grant 失敗與等待隨 run 結束 | D-007、TEST-019 |
| `internal/storage/objects` | server 產生的 key 與 DB 的 `object_key_ok` 規則相同；production 拒絕 filesystem adapter；Put 計算 SHA-256 與大小、不覆寫、並行 Put；context 與 maintenance 的 Delete | D-044、D-035 |
| `db/tests/runs.sql`、`constraints.sql` | run 以 queued、什麼都沒做的狀態建立；各 role 只能做自己的那一步；每個可見的變化推進 revision；取消請求不可撤回；run 跟隨 job（retry、failure、取消）；result 只存給 running 且未請求取消的 run、必須重現凍結 input；trace 四欄全有或全無、在本 run 的前綴之下；**run 的 identity 對 API 與 owner 不可改（55000），worker 的欄位 grant 本來就不含（42501）** | TEST-020、TEST-021、D-013 |
| `scripts/check-architecture` | `job-handlers-only-in-the-worker`：cmd/api 與 internal/models 連結 `internal/runs/runner` 的 probe 被擋；repo 本身符合所有規則 | ADR-024 |
| `apps/web/tests/contracts.test.ts` | `CancelSimulation` 的 `{}` 型別等於 fixture；`{force: true}` 是型別錯誤 | D-015、T-003 |

### 先紅後綠與 mutation（`mutations.txt`）

- 0009 的 identity guard：新的 `runs.sql` 列在修正前失敗（`UPDATE ... SET name='renamed'` 成功），修正後通過。
- TS 型別：`contracts.test.ts` 的 `cancelOption` 在產生器修正前讓 typecheck 失敗（TS2578，`Record<string, unknown>` 接受 `{force: true}`），修正後通過。
- Architecture 規則：兩個 probe 在規則加入前失敗（沒有 violation），加入後通過。
- 四個新的 Go 測試在接手的程式上就通過（程式本來就正確），所以以 mutation 證明它們擋得下缺陷：拿掉 attempt 開始時的取消檢查、把 wall timeout 改為可重試、把 role 檢查移到 replay 之後、讓 SSE 在撤權後寫 keep-alive 並繼續——4 個都被擋下（killed）。每次 mutation 後檔案還原，`git diff` 為空。
- 沒有對 T-023a／b 全部程式跑 `mutants.py`／`sql-mutants.py` 那種全面的 mutation（見 NOT RUN）。

## NOT RUN

- `make e2e-test` 的 product closed loop（瀏覽器 → run → compare → analyst）：UI 尚無 run 畫面（T-032；closed loop 為 T-040）；本輪未改 UI。
- 全面的 mutation harness（`mutants.py`、`sql-mutants.py`）於 T-023 的程式與 0009：只跑了上述 7 個有針對性的紅綠／mutation 檢查。
- 取消 ≤5 s、run 的 wall／event 上限與 batch 大小的 benchmark（T-037）：`TestCancelRunning` 只在測試機上量一次，不是效能測試結果。
- Production object storage adapter：等 owner 選雲端（D-044）；只有 development／test 的 filesystem adapter。
- 經 `cmd/api` 的 `http.Server`（`WriteTimeout` 60 s）超過 60 秒的 SSE：handler 每次寫入前以 `ResponseController.SetWriteDeadline` 延長期限，但沒有超過 60 秒的端對端測試（API 測試使用 `httptest` server）。
- `frontend-test` 以 Node 22.22.0 執行；`apps/web/package.json` 要求 ≥24.21.0，這台機器沒有 Node 24（之前的驗證用 24.21.0）。
- Import（T-016）與 AI（T-025）的 job handler：尚不存在。

## 需要 owner 或 reviewer 決定

- **CB-38**：run failure 沒有 code 目錄，目前的暫定對應（上限與 input → `MODEL_VALIDATION_FAILED`，基礎設施 → `SERVICE_UNAVAILABLE`，其餘 → `INTERNAL_ERROR`，原 code 在 `details[0].code`）待 owner 在 T-032 前決定。`downloadImportErrors` 是否也依 D-044 改為串流留給 T-016／T-017。
- **tasks.json 的 planned outputs**：T-023 仍列 `internal/simulation/service`、`internal/simulation/http`；實際 package 記在 ARCHITECTURE §6，planning 是否更新由 reviewer 決定（本輪未改 tasks.json）。
- **降級的語意**：降級為 viewer 的成員仍可讀 run，所以 stream 繼續；若 owner 希望任何 role 變更都結束 stream，需要另外決定。
- D-007 的 event batch（max_events 的十分之一）是實作參數，benchmark 後可調。

## 殘餘風險

- **入列時在 DB transaction 內讀 dataset snapshot 與 `engine.Compile`**：讀取有 D-007 的上限（每個 dataset ≤100k samples），但 object I/O 發生在持有 idempotency key row lock 的 transaction 中。Object storage 很慢時會拉長 transaction；改為 transaction 外先解析、再於 transaction 中確認凍結值，是之後可做的改動。
- **兩份 pack 登錄**：`internal/api.Handler` 與 `cmd/worker` 的 `handlers()` 各自登錄 software delivery pack。兩邊不一致時，API 接受的 run 會在 worker 以 `RUN_INPUT_INVALID` 失敗（不重試，fail closed）。H-016 換 domain 時兩處都要改。
- **滾動部署時的 engine version**：worker 的 engine 與 run 凍結的不同時，run 以 `ENGINE_VERSION_MISMATCH` 失敗且不重試；部署期間新舊 worker 並存會讓 run 失敗而不是等待。
- **孤兒 trace object**：lease 失去或 commit 失敗後上傳的 object 留在 storage，maintenance 尚未實作。
- Trace 下載在記憶體中讀完整個 artifact（≤64 MiB）再核對與送出。
- Run 失敗只記在 job 與 run 的 row，沒有 audit event（ADR-024 的迴圈不寫 audit）；建立、取消請求、取消結束、完成都有。
- 所有 worker 共用一個 DB role（ADR-024 round 3 I3，已接受）；`WORKER_STOPPED` 計入 attempt（H-010）。
- 這台機器上留有前一個 agent 的 cluster 目錄 `/var/lib/postgresql/geminus-t023`（沒有在執行的 process，只剩 lock 檔）；本輪沒有動它。
