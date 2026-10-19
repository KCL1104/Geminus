# T-019 驗證報告：PostgreSQL job queue、lease 與 worker 迴圈

**日期：2026-10-19｜狀態：`review`（送獨立 reviewer；依 H-003 驗收）｜需求：REQ-012、REQ-018、REQ-019｜Gate：D-007、D-008、D-011（APPROVED）；ADR-024（PROPOSED，待 owner）｜驗收測試：TEST-010（worker 部分）、TEST-020、TEST-021（佇列部分）**

範圍依 tasks.json：

- scoped `SKIP LOCKED` claim、heartbeat、lease_token、有限次數的重試；
- 每個 assigned tenant 各自的 transaction，不做跨 tenant 的無限制 SELECT；
- resource、job、audit 在同一個 enqueue transaction；過時的 finalize 被拒；
- worker scope 移到只有 worker 端能 import 的 package，並以 architecture 規則禁止 API 端依賴（T-009 re-review R3b）。

分三段交付：T-019a（36d2d44）、T-019b（4ab2eff、962c8b1）、T-019c／d（c6a51c1）。逐條輸出在 `checks/evidence/2026-10-19-T-019/`。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/tenant/workerscope`、`internal/tenant/internal/scope`（T-019a） | worker scope：`New(pool, assigned)` 只接受 organization ID、每個一次；`Run` 只在 assigned organization 中、以 transaction-local 設定執行，不查 membership。Scope 的 ID 規則與 `set_config` 放在只有 `internal/tenant` 底下能 import 的 package |
| `scripts/check-architecture`（T-019a） | 規則 `worker-scope-only-in-the-worker`：只有 `cmd/worker`、`internal/worker` 與 workerscope 本身能依賴它；三個 probe（cmd/api、service、storage）在規則之前通過、之後被擋 |
| `internal/storage/jobs`（T-019b、c） | `Enqueue`（在 resource 與 audit 的 transaction 中；不合格的 job 在送到 DB 前拒絕）、`Claim`（organization filter、只取有 handler 的 type、priority／created_at／id、`SKIP LOCKED`、attempt 與 token 加一）、`Heartbeat`、`Lock`、`Complete`、`Cancel`、`Fail`（以 organization、job、worker、token、running、未過期為 fence；不符回 `ErrLeaseLost`）、`CancelQueued`（只取消仍為 queued 的 job）、`Reap`（逐 organization 取回過期 lease，`SKIP LOCKED`，跳過正在 finalize 的 job）、`Policy` 與 backoff |
| `migrations/0007_job_states.*`（T-019c） | trigger 對所有 role 強制 job 的生命週期：以 queued、無 attempt／lease／error 建立；queued 只能被 claim（attempt 恰加一、token 變大）或取消；completed／failed／cancelled 不可再改；identity 與輸入不可改；attempt 與 token 不回退；不改值的 UPDATE 照常通過。Down migration 移除 trigger 與 function |
| `internal/worker`（T-019c） | 迴圈：每次 pass 依 deployment 順序走過每個 organization，reap 後至多 claim 並執行一個 job；heartbeat 每 `Heartbeat` 續期（每次至多一個 `Heartbeat`），lease 失去或一整個 lease 續不上即取消 handler；`Finish` 先 `Lock`，再寫 handler 的結果，再以 completed 或 cancelled 結束 job；attempt 結果依 ADR-024 的表記錄（error 原文只寫 log） |
| `internal/config/worker.go`、`cmd/worker`（T-019c） | `WORKER_ID`、`WORKER_ORGANIZATION_IDS`（皆必填）；worker runtime role 檢查與 pool guard；跑到 SIGINT／SIGTERM。目前沒有 handler（T-016、T-023、T-025），所以驗完設定後 exit 2（NOT IMPLEMENTED），不連 DB |
| `internal/storage/dbtest` | `NewImport`（測試用的 import resource）、worker login |
| `Makefile`、`scripts/run_db_tests.sh`、`db/tests/` | `db-go-test` 加入 `internal/worker`、`cmd/worker`；0007 加入 up／down／up；0007 的 SQL 測試 |
| 文件 | ADR-024（PROPOSED）、ARCHITECTURE §2 與 §6、API_DATABASE §7 與 §9、db/README、.env.example |

## 實際執行結果

所有指令在 c6a51c1 的乾淨 worktree 執行（`git worktree`，先 `go clean -testcache`；log 中沒有 cached 結果），DB 為一次性 PostgreSQL 16 cluster 上新建的 `dt_test_t019_final`。Log 在 `checks/evidence/2026-10-19-T-019/final/`，exit code 見 `exit-codes.txt`。

| 指令 | Exit | 說明 |
|---|---|---|
| `make backend-test`、`backend-race` | 0 | 各 35 個 package ok |
| `make architecture-check` | 0 | 42 packages、11 rules、16 build configurations |
| `make frontend-test` | 0 | 14 個檔案 218 個 test（本任務未改前端） |
| `make spec-check` | 0 | 重產的 `checks/*.json` 與 commit 相同 |
| `make integration-test`（新建的 `dt_test_t019_final`） | 0 | migration 0001–0007 up／down／up、0005 拒絕有 model 的 DB、SQL tests、`db-go-test` 的 16 個 package（含 `internal/worker`、`cmd/worker`）；唯一的 NOT RUN 是各 job type 的 handler（T-016、T-023、T-025） |
| `go test -race -p 1 -v`（jobs、worker、workerscope、config、cmd/worker） | 0 | `t019-go-tests.log`：38 個 test 通過，0 SKIP、0 FAIL |
| `go test -race -count=5`（worker 中與時間有關的 7 個測試：名稱含 Heartbeat、Lease、Renewal、Stopping、Share） | 0 | `t019-timing-x5.log`：5 次全過，檢查 lease、heartbeat 與停止的測試不會 flaky |
| `mutants.py`（Go，67 個） | 0 | `mutation-go.log`：63 個 killed、4 個等價（理由寫在 harness 中：RLS 已限制 organization 的兩個 filter、不可能出現的「attempt 用完的 queued job」、已清除 worker 的終態 job），0 unexpected |
| `sql-mutants.py`（0007，17 個） | 0 | `mutation-sql.log`：baseline 時 SQL suite 與 jobs、worker、cmd/worker 的 Go 測試都過；17 個全部 killed |

### 先紅後綠與 mutation 找到的缺口

- 佇列（T-019b）：`claim-not-skip-locked` 與 `lock-not-for-update` 起初存活，補 `TestClaimSkipsAHeldJob`、`TestLockHoldsTheJob` 後被擋下。
- 迴圈（T-019c）：handler 先於測試寫成，因此以 mutation 證明測試擋得下缺陷（AGENTS.md §5 的順序未完全遵守，列於差異）。過程中找到兩個測試缺口：
  - `heartbeat-never-gives-up`（拿掉「一整個 lease 續不上就放棄」）起初存活：row lock 的測試裡，lease 到期後下一次續期因 `lease_expires_at>now()` 不成立而得到 `ErrLeaseLost`，測到的是「lease 已失去」。新增 `TestTheHeartbeatGivesUpWithoutTheDatabase`（pool 的 `PrepareConn` 讓每個 transaction 在 begin 失敗）後被擋下：handler 在 20 秒內都沒停。
  - `cmd-no-role-check` 讓 superuser login 的測試卡住（迴圈在無期限的 context 上執行），harness 只看到 timeout。該呼叫改用 5 秒的 context，現在得到 exit 0 而失敗；harness 也把 timeout 判為 killed（`-timeout=300s`）。
  - 兩個 mutant 的寫法讓變數未被使用而無法編譯（harness 錯誤，不算 kill），改寫後被擋下。
- 0007：第一版 SQL 測試以交易內固定的 `now()` 更新 `heartbeat_at`，值沒有改變，所以「終態不可改」的檢查看起來失敗；改用 `clock_timestamp()`。既有的 invariant 擋下 0007 的 function 沒有固定 `search_path`，已補上。SQL mutation 起初有一個存活（`finished-to-running`）：終態檢查與轉移檢查重複保護同一件事；把轉移規則改為只限制 queued 的去向後，兩個 mutant 都被擋下。

### 測試涵蓋

| 測試 | 驗證 | 需求 |
|---|---|---|
| `TestEnqueueWithTheResource`（jobs，API login） | resource、job、audit 一起 commit 或都不 commit；type 與 resource 不符、payload 不是 object、attempts 超出 1–10 在 SQL 前拒絕；worker login 不能 enqueue | D-011 |
| `TestClaimOrderAndScope`、`TestConcurrentClaims`、`TestClaimSkipsAHeldJob` | priority、created_at、id 的順序；只看自己的 organization、`available_at` 未到不取、只取有 handler 的 type；6 個並行 worker 每個 job 恰取一次；被鎖的 job 被跳過而不是等待 | TEST-010、D-008 |
| `TestTheLeaseFencesEveryWrite`、`TestLockHoldsTheJob` | heartbeat 延長 lease；另一個 worker、舊 token、別的 organization 對 heartbeat、lock、complete、cancel、fail 都得到 `ErrLeaseLost` 且不改 job；完成後同一個 lease 也不能再寫；`Lock` 持有 row lock 到 transaction 結束 | TEST-020 |
| `TestReapAfterACrash`、`TestReapSkipsAJobBeingFinalized`、`TestFailRetriesOnlyWhatMayBeRetried`、`TestBackoff` | 過期 lease 被 reap、backoff 後以新 token 重跑，當掉的 worker 寫不進去，attempt 用完即 failed；reaper 跳過正在 finalize 的 job；可重試與不可重試的 failure；backoff 倍增與上限 | TEST-020、D-011 |
| `TestCancellation`、`TestCancelRacesTheClaim` | queued job 立即取消且不再被 claim、重複取消回 false、別的 organization 取消不到；running job 不能直接取消，handler 以 `Cancel` 結束後 `Complete` 被拒，反之亦然；完成的 job 取消不到；3 個 worker claim 與取消並行時，每個 job 恰為其中一種 | TEST-021 |
| `TestAJobRunsOnceWithItsResult`、`TestAJobEndsCancelled`（worker） | job 只執行一次，結果（import 狀態）與 completed 一起 commit，handler 看到的 job 是 running、attempt 1、自己的 lease；以 cancelled 結束時結果一起 commit；completed／cancelled 以外的結束被拒且不執行 fn | TEST-020、TEST-021 |
| `TestFailedAttempts` | 不可重試的 Failure、包在 error 中的可重試 Failure、一般 error（retry，原文不存、只在 log）、最後一次 attempt 的 error（failed）、結果 transaction 失敗（rollback、retry）、panic（不重試）、沒有結果（NO_RESULT）；失敗的 attempt 都不留下結果 | D-011 |
| `TestTheHeartbeatKeepsALongJob`、`TestALostLeaseStopsTheHandler`、`TestFinishAfterTheLeaseExpired` | 執行時間為 lease 兩倍的 job 仍完成；lease 被別人取得後，下一次 heartbeat 就停止 handler（遠早於 lease 結束），之後的 `Finish` 為 `ErrLeaseLost`、fn 不執行、job 不被改；lease 過期但 heartbeat 尚未發現時 `Finish` 被拒，下一次 pass 把 job reap 回 queued | TEST-020 |
| `TestAHungRenewalCannotOutliveTheLease`、`TestTheHeartbeatGivesUpWithoutTheDatabase` | 續期卡在 row lock 時，每次在一個 heartbeat 後逾時，lease 結束時 handler 停止（不早於 lease）；DB 完全無回應時，一整個 lease 沒有續期成功才停止 handler；兩者都不寫入 job | TEST-020 |
| `TestStoppingRequeuesTheJob` | 停止中的 worker 把 job 以 `WORKER_STOPPED`（可重試）放回 queued | D-011 |
| `TestOrganizationsTakeTurns`、`TestAnUnhandledTypeStaysQueued`、`TestWorkersShareTheQueue` | 每次 pass 每個 organization 一個 job、依 deployment 順序；沒有 handler 的 type 保持 queued；兩個 worker 的 `Run` 各 job 恰執行一次，context 結束後 `Run` 返回 | D-008 |
| `TestNewChecksTheConfig`、`TestLoadWorker`、`cmd/worker` 的 5 個測試 | 設定驗證（ID、heartbeat 至多 lease 的三分之一、handler、idle、reap、log）；`WORKER_ID` 與 allowlist 必填、ID 只能是可見 ASCII；cmd 拒絕不合格的 allowlist（exit 1）、連不上 DB（exit 3）、superuser login（exit 3）、沒有 handler 的 build（exit 2，不連 DB），stderr 不含密碼；以 worker login 執行一個 job 後收到停止即 exit 0 | D-008、REQ-019 |
| `TestNewRefusesABadAllowlist`、`TestAssignment`（workerscope） | allowlist 的格式與重複；未指派的 organization 被拒；只看到指派 organization 的 row；不能讀 memberships；pool 中不留 scope | TEST-010、D-008 |
| `db/tests/constraints.sql`（0007） | 非 queued、帶 attempt、token 或 error 的 INSERT 被拒；queued 直接 completed／failed、沒換 token 或跳過 attempt 的 claim、改 payload／max_attempts／created_at 被拒；queued→queued、claim、heartbeat、retry、第二次 claim、completed 可行；token 或 attempt 回退被拒；終態的 job 對 API、worker、owner 都不可改；不改值的 UPDATE 可行 | TEST-020、TEST-021 |
| `scripts/check-architecture` | worker scope 只在 worker 端；三個 probe | ADR-019 R3b |

## 決策與 owner 知悉

- **ADR-024 為 PROPOSED，待 owner 決定**：
  - 參數：lease 30 秒與 heartbeat 10 秒是 D-007 已批准的候選值；max attempts 3 是 API_DATABASE §7 的候選值；backoff（5 秒起倍增、最多 5 分鐘）、無 job 時 1 秒、每次 pass 每個 organization reap 100 個是本 ADR 的提案。都不是 benchmark 的結果（D-007 要求之後 benchmark 與評審）。
  - `WORKER_STOPPED` 是否計入 attempt（目前計入，部署時反覆重啟可能讓 job 提早 failed）。
- Migration 0007 是新的 DB 規則（job 的終態不可改），不在 tasks.json 的明列 scope 內；它是 TEST-020／TEST-021 的「舊 lease 無權 finalize」「cancel 與 complete 互斥」在 DB 端的保證，與 runs、proposals 已有的終態保護一致。
- 沒有使用 live AI、雲端或任何外部服務。

## 與規劃的差異

- tasks.json 的 planned outputs 只列 `internal/storage/jobs/` 與 `cmd/worker/`；迴圈本身放在 `internal/worker`（cmd 只放組裝與生命週期，ARCHITECTURE §2），worker scope 依 scope 第 4 點移到 `internal/tenant/workerscope`。
- TEST-021 在本任務只涵蓋佇列的部分（queued 立即取消、cancel 與 complete 互斥、cancel 與 claim 競爭、終態不可改）。Run 的 `cancellation_requested`、handler 的 checkpoint、超出 budget 時不為 completed 屬 T-023（其 acceptance tests 也列 TEST-021）。
- `cmd/worker` 仍拒絕啟動：沒有 handler 時 claim 了也無法執行。迴圈已在 `cmd/worker` 的 DB 測試中以測試 handler 實際跑過。
- T-019c 的迴圈先寫實作再寫測試（AGENTS.md §5 要求先寫測試）；以 mutation 作為測試有效的證據（見上）。

## NOT RUN／延後

| 項目 | 原因／歸屬 |
|---|---|
| 任何實際 job type 的 handler（import profiling、simulation、AI） | T-016、T-023、T-025 |
| Run 的取消 checkpoint、budget 與 result finalize（鎖 job 再鎖 run、cancel flag） | T-023 |
| 參數的 benchmark（lease、heartbeat、backoff、idle、reap 上限） | D-007 要求；T-037（performance acceptance）、T-039（staging） |
| 多個 worker process 跨機器、DB failover 下的行為 | 沒有部署環境；以兩個 worker 共用 queue 與「DB 無回應」測試代替 |
