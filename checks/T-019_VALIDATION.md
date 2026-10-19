# T-019 驗證報告：PostgreSQL job queue、lease 與 worker 迴圈

**日期：2026-10-19｜狀態：`review`（review round 1、2 皆為 CHANGES REQUESTED，修正後送 round 3；依 H-003 驗收）｜需求：REQ-012、REQ-018、REQ-019｜Gate：D-007、D-008、D-011（APPROVED）；ADR-024（PROPOSED，待 owner）｜驗收測試：TEST-010（worker 部分）、TEST-020、TEST-021（佇列部分）**

範圍依 tasks.json：

- scoped `SKIP LOCKED` claim、heartbeat、lease_token、有限次數的重試；
- 每個 assigned tenant 各自的 transaction，不做跨 tenant 的無限制 SELECT；
- resource、job、audit 在同一個 enqueue transaction；過時的 finalize 被拒；
- worker scope 移到只有 worker 端能 import 的 package，並以 architecture 規則禁止 API 端依賴（T-009 re-review R3b）。

分三段交付：T-019a（36d2d44）、T-019b（4ab2eff、962c8b1）、T-019c／d（c6a51c1、5a68042），review round 1 的修正見下方專節。逐條輸出在 `checks/evidence/2026-10-19-T-019/`（round 1、2 修正後的在 `round1/`、`round2/`）。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/tenant/workerscope`、`internal/tenant/internal/scope`（T-019a） | worker scope：`New(pool, assigned)` 只接受 organization ID、每個一次；`Run` 只在 assigned organization 中、以 transaction-local 設定執行，不查 membership。Scope 的 ID 規則與 `set_config` 放在只有 `internal/tenant` 底下能 import 的 package |
| `scripts/check-architecture`（T-019a） | 規則 `worker-scope-only-in-the-worker`：只有 `cmd/worker`、`internal/worker` 與 workerscope 本身能依賴它；三個 probe（cmd/api、service、storage）在規則之前通過、之後被擋 |
| `internal/storage/jobs`（T-019b、c） | `Enqueue`（在 resource 與 audit 的 transaction 中；不合格的 job 在送到 DB 前拒絕）、`Claim`（organization filter、只取有 handler 的 type、priority／created_at／id、`SKIP LOCKED`、attempt 與 token 加一）、`Heartbeat`、`Lock`、`Complete`、`Cancel`、`Fail`（以 organization、job、worker、token、running、未過期為 fence；不符回 `ErrLeaseLost`）、`CancelQueued`（只取消仍為 queued 的 job）、`Reap`（逐 organization 取回過期 lease，`SKIP LOCKED`，跳過正在 finalize 的 job）、`Policy` 與 backoff |
| `migrations/0007_job_states.*`（T-019c、round 1） | trigger 對所有 role 強制 job 的生命週期：以 queued、無 attempt／lease／error 建立；queued 只能被 claim（attempt 恰加一、token 變大）或取消；queued 的 job 一定還有 attempt；completed／failed／cancelled 不可再改；identity 與輸入不可改；attempt 與 token 不回退；不改值的 UPDATE 照常通過。另依 role 限制：API 只能取消或重排 queued 的 job，worker 從 queued 只能 claim，只有 schema owner 不受這一條限制。只管 INSERT 與 UPDATE。Down migration 移除 trigger 與 function |
| `internal/worker`（T-019c、round 1） | 迴圈：每次 pass 依 deployment 順序走過每個 organization，reap 後至多 claim 並執行一個 job；heartbeat 每 `Heartbeat` 續期，lease 失去時立即、否則在「最後一次成功續期的開始 + lease − 半個 heartbeat」取消 handler，不等進行中的續期；`Checkpoint` 與 `Finish` 都先 `Lock`；`Finish` 在同一個 transaction 寫 handler 的結果並以 completed 或 cancelled 結束 job；attempt 結果依 ADR-024 的表記錄（handler 的 Failure 經檢查；error 原文只以有界、去除控制字元的形式寫 log；panic 值不寫 log） |
| `internal/worker/handler`（round 1） | handler 的接口（`Handler`、`Func`、`Run`）；leaf package，不依賴 worker scope，T-016、T-023、T-025 的 handler 實作它而不違反 architecture 規則 |
| `internal/config/worker.go`、`cmd/worker`（T-019c、round 1） | `WORKER_ID`、`WORKER_ORGANIZATION_IDS`（皆必填，allowlist 在 handler 檢查之前驗）；worker runtime role 檢查與 pool guard；第一個 SIGINT／SIGTERM 停止迴圈，第二個立即結束 process。目前沒有 handler（T-016、T-023、T-025），所以驗完設定後 exit 2（NOT IMPLEMENTED），不連 DB |
| `internal/storage/dbtest` | `NewImport`（測試用的 import resource）、worker login |
| `Makefile`、`scripts/run_db_tests.sh`、`db/tests/` | `db-go-test` 加入 `internal/worker`、`cmd/worker`；0007 加入 up／down／up；0007 的 SQL 測試 |
| 文件 | ADR-024（PROPOSED）、ARCHITECTURE §2 與 §6、API_DATABASE §7 與 §9、db/README、.env.example |

## 實際執行結果

Review round 2 的修正（02a4200）在乾淨的 worktree 執行（`git worktree`，先 `go clean -testcache`；log 中沒有 cached 結果），DB 為一次性 PostgreSQL 16 cluster 上新建的 `dt_test_t019_r2`，執行後刪除。Log 在 `checks/evidence/2026-10-19-T-019/round2/`，exit code 見 `exit-codes.txt`，環境見 `environment.txt`（Go 1.27.2、Node 24.21.0、PostgreSQL 16.15、4 CPU）。先前各輪的結果保留在 `final/`（c6a51c1）與 `round1/`（5d8f44e）。

這一次驗證之前，同一份修正的第一次乾淨驗證（4a54180，未 push）在 `go test -race` 抓到新 heartbeat 的 data race（見下方 round 2 一節），修正後 amend 為 02a4200 再從頭驗證。

| 指令 | Exit | 說明 |
|---|---|---|
| `make backend-test`、`backend-race` | 0 | 各 35 個 package ok |
| `make architecture-check` | 0 | 43 packages、11 rules、16 build configurations |
| `make frontend-test` | 0 | 18 個檔案 392 個 test（本任務未改前端；數量包含 T-043） |
| `make spec-check` | 0 | 重產的 `checks/*.json` 與 commit 相同（`spec-check-status.txt` 為空） |
| `make integration-test`（新建的 `dt_test_t019_r2`） | 0 | migration 0001–0007 up／down／up、SQL tests、`db-go-test` 的 16 個 package（race）；唯一的 NOT RUN 是各 job type 的 handler（T-016、T-023、T-025） |
| `go test -race -p 1 -v`（jobs、worker、worker/handler、workerscope、config、cmd/worker） | 0 | `t019-go-tests.log`：56 個 top-level test 通過，0 SKIP、0 FAIL |
| `go test -race -count=5`（worker 與 cmd/worker 中與時間有關的 20 個測試） | 0 | `timing-x5.log`：100/100 通過，沒有 data race |
| `mutants.py`（Go，115 個） | 0 | `mutation-go.log`：在 02a4200 上完整跑一次，110 killed、5 等價（理由在 harness：RLS 已限制 organization 的兩個 filter、0007 擋下沒有 attempt 的 queued job、不在 running 的 job 沒有可對上的 holder、停止的 attempt 由 `AfterFunc` 立即取消其 transaction），0 unexpected |
| `sql-mutants.py`（0007，39 個） | 0 | `mutation-sql.log`：baseline 時 SQL suite 與 jobs、worker、cmd/worker 的 Go 測試都過；38 killed、1 等價（`api-claims`：claim 必須改 attempt 與 token，API 的檢查不准改），0 unexpected |

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
| `TestFailedAttempts`、`TestAFailureIsAnError` | 不可重試的 Failure、包在 error 中的可重試 Failure、一般 error（retry，原文不存；log 中去除控制字元、至多 500 字）、最後一次 attempt 的 error（failed）、typed nil 的 Failure、沒有或不合格的 code、空白或過長的 message、結果 transaction 失敗（rollback、retry）、帶值的 panic 與 runtime panic（不重試；值不進 log，runtime error 的訊息與 stack 進 log）、沒有結果（NO_RESULT）；失敗的 attempt 都不留下結果 | D-011、SECURITY §7 |
| `TestTheHeartbeatKeepsALongJob`、`TestALostLeaseStopsTheHandler`、`TestFinishAfterTheLeaseExpired` | 執行時間為 lease 兩倍的 job 仍完成；lease 被別人取得後，下一次 heartbeat 就停止 handler（遠早於 lease 結束），之後的 `Finish` 為 `ErrLeaseLost`、fn 不執行、job 不被改；lease 過期但 heartbeat 尚未發現時 `Finish` 被拒，下一次 pass 把 job reap 回 queued | TEST-020 |
| `TestAHungRenewalCannotOutliveTheLease`、`TestTheHeartbeatGivesUpWithoutTheDatabase`、`TestOneFailedRenewalIsSurvived`、`TestASlowRenewalCountsFromItsStart`、`TestTheLeaseLastsOneLease` | lease 3 s、heartbeat 1 s：續期卡在 row lock 或 DB 完全無回應時，handler 在 2.5 s ± 300 ms 停止，且當時 DB 的 lease 仍有效；一次續期失敗、下一次成功時 job 照常完成；1 s 開始、2.2 s 才成功的續期讓期限從 1 s 起算（3.5 s 停止，lease 仍有效）；claim 與 heartbeat 寫入的 lease 為 `now()+Lease`；都不寫入 job | TEST-020 |
| `TestStoppingRequeuesTheJob`、`TestTheFailureRecordIsBounded`、`TestAnIdleWorkerPauses` | 停止中的 worker 把 job 以 `WORKER_STOPPED`（可重試）放回 queued，但 handler 自己回的不可重試 Failure 照樣為準；記錄 attempt 的結束至多等一個 heartbeat；閒置的 worker 每個 Idle 只有兩個 transaction | D-011 |
| `TestOrganizationsTakeTurns`、`TestAnUnhandledTypeStaysQueued`、`TestWorkersShareTheQueue` | 每次 pass 每個 organization 一個 job、依 deployment 順序；沒有 handler 的 type 保持 queued；兩個 worker 的 `Run` 各 job 恰執行一次，context 結束後 `Run` 返回 | D-008 |
| `TestNewChecksTheConfig`、`TestLoadWorker`、`cmd/worker` 的 8 個測試 | 設定驗證（ID、heartbeat 至多 lease 的三分之一、handler、idle、reap、log）；`WORKER_ID` 與 allowlist 必填、ID 只能是可見 ASCII；cmd 拒絕不合格的 allowlist（exit 1，有沒有 handler 都一樣）、連不上 DB（exit 3）、superuser login（exit 3）、沒有 handler 的 build（exit 2，不連 DB），stderr 不含密碼；pool 歸還時有 guard；以 worker login 執行一個 job 後收到停止即 exit 0；啟動途中收到 signal 為停止（exit 0），不是 DB 錯誤；第二個 SIGINT 結束 process（子 process） | D-008、REQ-019 |
| `TestNewRefusesABadAllowlist`、`TestAssignment`（workerscope） | allowlist 的格式與重複；未指派的 organization 被拒；只看到指派 organization 的 row；不能讀 memberships；pool 中不留 scope | TEST-010、D-008 |
| `db/tests/constraints.sql`（0007） | 非 queued、帶 attempt、token 或 error 的 INSERT 被拒；API 只能取消或重排 queued 的 job（不能 claim、改 attempt 或 error、直接 completed，也不能結束、接手、重排或取消 running 的 job）；worker 從 queued 只能 claim（不能取消、重排、直接 completed、不換 token 或跳過 attempt 的 claim）；claim、heartbeat、retry、第二次 claim、completed 可行；最後一次 attempt 不能回到 queued；token 或 attempt 回退被拒；owner 逐欄改 identity 與輸入都被拒；終態的 job 對 API、worker、owner 都不可改；不改值的 UPDATE 可行；同時屬於 owner 與 runtime role 的 login（suite 的 superuser）視同 owner，可手動結束 running 的 job | TEST-020、TEST-021 |
| `scripts/check-architecture` | worker scope 只在 worker 端；三個 probe；`internal/worker/handler` 之外的 package 實作 handler 不違反規則 | ADR-019 R3b |

## Review round 1（5a68042：CHANGES REQUESTED）的修正

獨立 reviewer 的 M1–M4、L1–L8、I1–I5 全部處理；沒有任何一項以「不修」結案。每一項都有測試或 mutant 佐證（下表「證據」欄的 mutant 名稱見兩個 harness）。

| 項目 | 修正 | 證據 |
|---|---|---|
| **M1** heartbeat 在 DB 的 lease 結束後約一個 heartbeat 才放棄，預設設定下兩個 handler 會同時跑同一個 job | 期限改為「最後一次成功續期（一開始是 claim）的 transaction 開始前讀的時間 + lease − 半個 heartbeat」，由獨立的 timer 執行，不等進行中的續期；續期本身以期限為時限；同時只有一個續期 | `TestAHungRenewalCannotOutliveTheLease`、`TestTheHeartbeatGivesUpWithoutTheDatabase` 改為檢查上下界（2.5 s ± 300 ms）且停止時 DB 的 lease 仍有效；新增 `TestOneFailedRenewalIsSurvived`、`TestASlowRenewalCountsFromItsStart`（續期在 1 s 開始、2.2 s 才完成，期限從 1 s 起算）。Mutants：`deadline-*`、`rv-give-up-after-two-leases`、`rv-renewed-never-updated`、`deadline-timer-not-reset`、`deadline-from-the-renewal-end`、`renewals-overlap` |
| **M2** typed-nil 的 `*jobs.Failure` 讓整個 process panic | `errors.As(err, &f) && f != nil`，typed nil 視為一般 error（INTERNAL、可重試）；`(*Failure).Error()` 對 nil 也回答 | `TestFailedAttempts` 的 typed nil 案例、`TestAFailureIsAnError`；`typed-nil-failure-used`、`nil-failure-crashes` |
| **M3** handler 介面在 `internal/worker`（依賴 workerscope），T-016／T-023／T-025 的 handler 會違反 architecture 規則；handler 沒有 lease 下的讀取與 checkpoint | 新的 leaf package `internal/worker/handler`：`Handler`、`Func`、`Run`（`Job`、`Checkpoint`、`Finish`）；`Checkpoint` 與 `Finish` 都先 `Lock`，失去 lease 時立即停止 handler | `TestHandlersPlugInWithoutTheWorkerScope`（architecture；舊的位置會失敗）、`TestCheckpointsUnderTheLease`（含失去 lease 後 handler 的 context 立即結束）；`locked-without-lock`、`locked-lease-loss-not-noted` |
| **M4** 沒有測試限制 heartbeat 的時間上限 | 見 M1；另驗 claim 與 heartbeat 寫入的 lease 長度 | `TestTheLeaseLastsOneLease`；`rv-claim-lease-x10`、`rv-heartbeat-lease-x10`。`rv-heartbeat-timeout-100x` 不再適用（每次續期不再有自己的時限，而以期限為界）；對應的 `renewal-unbounded` 列為等價（理由在 harness） |
| **L1** worker 停止時蓋過 handler 自己的結果 | 判斷順序改為：完成 → lease 失去 → handler 的 Failure → panic → 停止 → 無結果 → 其他 error | `TestStoppingRequeuesTheJob` 兩個案例；`stop-overrides-the-verdict` |
| **L2** 沒有 code 的 Failure 不被記錄，job 留到 lease 過期 | `checked`：code 不符 `^[A-Z][A-Z0-9_]{0,63}$` 記為 INTERNAL（保留 retry 判斷）；message 空白或超過 1000 字改為通用文字 | `TestFailedAttempts` 的四個案例；`failure-not-checked`、`checked-*` |
| **L3** API login 能 claim、結束、接手 job，把 queued job 的 attempt 設滿；worker login 能直接取消 queued job | 0007 加上：queued 的 job 必須還有 attempt；API 只能把 queued 改為 queued（priority、available_at）或 cancelled，且不能動 attempt、token、lease、error；worker 從 queued 只能 claim；只有 schema owner 不受角色限制。`claim-ignores-attempts` 的等價理由因此對所有寫入成立 | `constraints.sql` 的 0007 段逐一以 twin_api、twin_worker、twin_owner 測試；SQL mutants `queued-without-attempts`、`api-*`、`worker-any-move`、`roles-unrestricted`、`owner-restricted-as-api` |
| **L4** `cmd/worker` 先檢查 handler 才檢查 allowlist | allowlist 先以 `workerscope.New(nil, …)` 檢查 | `TestRunRefusesABadAllowlist`（有、無 handler 兩種）；`cmd-allowlist-checked-late` |
| **L5** SQL 測試只改 payload、max_attempts、created_at | 以 owner 逐欄改 id、organization_id、type、type＋resource、import_id（改成另一個有效 import）、simulation_run_id、ai_proposal_id、ai_analysis_id，都必須由 trigger 以 55000 拒絕（CHECK 或 FK 先拒絕也算測試失敗） | SQL mutants `rv-*-mutable`、`proposal-id-mutable`、`analysis-id-mutable` |
| **L6** backoff、reap 上限、idle、shutdown 記錄的時限、pool guard 沒有測試 | 新增測試 | `TestRetriesBackOffByAttempt`、`TestReapTakesItsLimit`、`TestAnIdleWorkerPauses`、`TestTheFailureRecordIsBounded`、`TestPoolGuardsReleasedConnections`；對應的 5 個 `rv-*` mutants |
| **L7** 超過一個 lease 的 finalize transaction 一定失敗 | 記為限制（`handler.Run` 的說明與 ADR-024）：`Checkpoint`／`Finish` 的 transaction 持有 row lock，必須在 lease 減一個 heartbeat 內結束；長的計算放在 transaction 之外。沒有採用「`Lock` 時延長 lease、暫停續期」：那會多出第二條續期路徑 | 文件 |
| **L8** handler 的 error 與 panic 值原樣寫入 log | Error 文字去除控制字元、至多 500 字（`logText`）；panic 只記 runtime error 的訊息或值的型別，加 stack；`handler.Handler` 與 ADR-024 規定 handler 的 error 不得含客戶資料、prompt、provider 輸出或 secret | `TestFailedAttempts`：secret 的 panic 值不進 log、控制字元與長度、stack 有記錄；`panic-value-logged`、`error-logged-raw`、`log-text-*`、`runtime-panic-message-dropped`、`panic-stack-dropped` |
| **I1** `cmd-no-role-check` 只靠 300 秒 timeout 被擋 | `TestRunNeedsTheDatabase` 改用 10 秒的 context | 見 mutation log 的失敗 test 名稱 |
| **I2** 0007 不管 DELETE | 文件寫明 0007 只管 INSERT 與 UPDATE；刪除屬保存期限（runtime role 沒有 DELETE） | 0007 的註解、ADR-024 |
| **I3** ADR 說只用 DB 的 `now()` | ADR 改寫：worker 以自己在 transaction 開始前讀的時間起算，半個 heartbeat 的餘裕涵蓋停止與小的時鐘偏差，更大的偏差由部署（NTP）避免 | 文件 |
| **I4** 第二個 SIGTERM 被吞掉 | `listen()`：收到第一個 signal 時先恢復預設處理、再結束 context，所以第二個 signal 立即結束 process。第一版（`signal.NotifyContext` 加 goroutine）有 race：context 先結束，第二個 signal 若在 `stop()` 之前到達就被吞掉——新測試第一次執行就失敗，因此改寫。每個 job 的總時限屬於 handler 與其 resource（ADR-024 Consequences） | `TestASecondSignalEndsTheProcess`（以子 process 送兩次 SIGINT，檢查它被 SIGINT 結束）；`cmd-second-signal-swallowed`。另外，迴圈開始前收到的 signal 原本會以 exit 3「database unavailable」結束，改為停止（exit 0）：`TestRunStopsBeforeStarting`、`cmd-stop-before-start-fails` |
| **I5** T-016 的 `depends_on` 沒有 T-019 | 已加入 | `planning/tasks.json` |

## Review round 2（52c667c：CHANGES REQUESTED）的修正

Round 2 確認 M1、M4、L1、L4、L5、L6、I1、I4、I5 已修好，L3 的 API 部分已修好；提出新的 M1、L1–L5 與 I1–I5。全部處理，I4 為接受並記錄。

| 項目 | 修正 | 證據 |
|---|---|---|
| **M1** handler 的 error 型別本身壞掉（typed nil 的 `*os.PathError`、`Error()` 讀取自己的值、包住自己的 Unwrap）時，worker 在 recover 之外讀取它而崩潰或卡住 | `read`：在 recover 之內讀取 error（`errors.Is`／`errors.As`、`Error()`），先檢查 Unwrap 樹至多 64 個 error；讀不了的 error 記為 `INTERNAL`、不重試，log 只寫型別。永遠卡住的方法無法在 process 內防範（寫明於 ADR-024） | `TestFailedAttempts` 的 typed nil `*os.PathError`、typed nil 自訂型別、包住自己的 error；`read-unprotected`、`chains-unbounded`、`broken-not-noted`、`broken-retried` |
| **L1** handler 被停止後（甚至 DB 的 lease 結束後），以 detached context 呼叫的 `Checkpoint`／`Finish` 仍寫入 | Attempt 停止後兩者都不再開始（lease 失去時 `jobs.ErrLeaseLost`，worker 停止時新的 `handler.ErrStopped`）；transaction 的 context 也隨 attempt 結束（`context.AfterFunc`），進行中的 transaction 被取消並 rollback。文件改為與行為一致 | `TestNothingIsWrittenAfterTheAttemptStops`（期限與 worker 停止兩種）、`TestATransactionIsCancelledWhenTheAttemptStops`（已寫入、持有 row lock 越過期限的 checkpoint 沒有 commit）；`writes-after-the-stop`、`transaction-outlives-the-attempt`、`stopped-hides-the-lost-lease`、`mid-transaction-stop-unreported` |
| **L2** 「可容許一次續期失敗」只在失敗於一個 heartbeat 內回來時成立 | Heartbeat 改為依序續期：每次至多一個 heartbeat（也不超過期限），卡住的續期放棄後立刻開始下一次（ticker 保留錯過的 tick）；期限由獨立的 goroutine 執行 | `TestAHungRenewalIsRetried`（約 1 秒的續期卡 1.5 秒後失敗，job 仍完成）；`TestASlowRenewalCountsFromItsStart` 改為在一個 heartbeat 內完成的慢續期；`renewal-unbounded`（原本列為等價，現在被擋下）、`renewal-bounded-by-the-deadline-only`、`earliest-is-latest` |
| **L3** handler package 從 T-023、T-025 的 planned paths 用不了 | 寫明 handler 是 adapter，不能放在 generic kernel 或 `internal/ai`（既有規則禁止它們依賴 storage 與 tenant）；T-023、T-025 需要各自的 adapter package，由那兩個任務命名並記錄（`handler.go`、ADR-024） | `TestHandlersPlugInWithoutTheWorkerScope` 改為三個位置：T-016 的 `internal/import/upload` 不違反任何規則；`internal/simulation/handler` 違反 `generic-kernel`；`internal/ai/handler` 違反 `ai-suggests-only`；三者都不碰 worker scope |
| **L4** PostgreSQL 存不了的 message（NUL、非 UTF-8）讓 Failure 沒被記錄 | `checked` 也替換非 UTF-8 或含 NUL 的 message | `TestFailedAttempts` 兩個案例；`checked-keeps-nul`、`checked-keeps-invalid-utf8` |
| **L5** 測試缺口 | worker 停止時 handler 回 nil 記為 `WORKER_STOPPED`（不是 `NO_RESULT`）；API 不能動 queued job 的 lease 欄位；claim 的 statement 拖 0.8 秒時期限仍從 claim 的 transaction 開始前算起；code 超過 64 字；message 以字元而非 byte 計算；`Run` 在 context 結束後 5 秒內返回 | `TestStoppingRequeuesTheJob` 第三個案例、`constraints.sql`、`TestASlowClaimCountsFromBeforeIt`、`TestFailedAttempts`、`TestRunReturnsWhenStopped`；`api-lease-columns-unchecked`、`code-length-unbounded`、`checked-counts-bytes`；`loop-ignores-stop` 現在 5 秒內被擋下。`rv2-cmd-stop-after-cancel`（`listen()` 中先 `cancel` 再 `Stop`）的時間窗只有幾微秒，子 process 測試碰不到；順序是刻意的，寫在註解中 |
| **I1** worker login 可以把 running job 的 attempt 調高、直接接手別人的 lease、把 lease 延長一年、保留 lease 回到 queued | 0007：worker 對 running job 只能續期（attempt、token、holder、error 不變），離開 running 時必須清除 lease；attempt 與 token 只在 claim 時改變。Lease 的長度上限沒有放進 DB：那是部署的參數（ADR-024），DB 中再寫一份會重複設定 | `constraints.sql` 的 9 個新的 must_fail；`worker-running-unrestricted`、`worker-changes-attempt-or-token`、`worker-takes-over`、`worker-keeps-the-lease` |
| **I2** 0007 讓三個 role 以外的 role 通過 | 不是 owner、API 或 worker 的 role 一律被拒；只有 owner 與 API 能 enqueue | `constraints.sql`：在 rollback 的 transaction 中授權給 `twin_provisioner` 與 worker 後仍以 55000 被拒；`other-roles-allowed`、`anyone-enqueues` |
| **I3** 時鐘的說法不精確 | 改寫：worker 以 monotonic clock 計時，與 DB 的時鐘差距不影響；餘裕涵蓋停止 handler、DB 時鐘往前跳與短於半個 heartbeat 的 process 暫停 | `worker.go` 註解、ADR-024 |
| **I4** 期限停止 attempt 時，handler 這時才回的 Failure 不記錄 | 接受並記錄：reaper 以可重試的 `LEASE_EXPIRED` 取回，下一次 attempt 會再得到 handler 的判斷 | ADR-024 Consequences |
| **I5** pgx 拒絕的 `DATABASE_URL` 在 handler 檢查之後才被發現 | 在 handler 檢查之前以 pgx 解析 | `TestRunRefusesABadConnectionString`；`cmd-bad-connection-string-not-a-config-error` |

`fence-any-status` 的等價理由依 reviewer 的意見改寫（現在由 0007 對 worker 的規則保證）。

這一輪 mutation 找到的：

- `typed-nil-failure-used` 起初存活：`read` 中的 `&& f != nil` 與 `run` 的 `v.failure != nil` 重複（typed nil 的 `*jobs.Failure` 在欄位中就是 nil）。移除前者，mutant 改為針對真正的防線（`run` 的判斷），被擋下。
- `writes-after-the-stop`（拿掉「attempt 停止後不再開始」）存活，列為等價：attempt 停止後 `context.AfterFunc` 立即取消 transaction 的 context，呼叫在第一個 statement 就失敗、什麼都不寫；提早返回讓這件事不依賴取消的 goroutine 是否先跑，也不執行 fn。
- 乾淨 worktree 的驗證（`go test -race`）抓到新 heartbeat 的 data race：期限的 goroutine 在啟動時讀取 `deadline`，而續期迴圈同時寫入。改為在 goroutine 啟動前建立 timer，goroutine 只經由 timer 與 channel 取得期限；之後 `-race -count=3` 通過。Mutation harness 不以 `-race` 執行，所以沒有發現它。
- SQL 的 `attempt-goes-back`、`token-goes-back` 起初存活：worker 的新規則先擋下了原本的測試；補 owner（不受角色限制）把 attempt 與 token 調回的案例後被擋下。

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
