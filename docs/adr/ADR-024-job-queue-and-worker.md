# ADR-024 — PostgreSQL job queue 與 worker 迴圈（T-019）

狀態：PROPOSED（T-019 agent 提出並先實作；待 owner 決定；review round 1 後修訂）。日期：2026-10-19。依據：D-007、D-008、D-011（皆 APPROVED）、ARCHITECTURE §6、API_DATABASE §6–§7、TEST-020、TEST-021、ADR-019（T-009 re-review R3b）。

## Decision

### 佇列（`internal/storage/jobs`）

- 只用 PostgreSQL 的 `twin.jobs`，不加 broker（ARCHITECTURE §6、AGENTS §7）。API 在建立或更新 resource、寫 audit 的同一個 transaction 中 `Enqueue`（D-011：202 只在三者一起 commit 之後回）。Job 必須恰有一個與 type 相符的 resource、payload 是 JSON object（只放凍結的輸入參照，不放 secret 或客戶原始資料）、`max_attempts` 1–10，否則在送到 DB 之前就拒絕。
- **Claim**：在一個 organization 的 transaction 中（`internal/tenant/workerscope`，D-008），只取 queued、`available_at` 已到、還有 attempt、且 worker 有 handler 的 type；順序為 priority 高者先，再依 `created_at`、`id`；`FOR UPDATE SKIP LOCKED`。Claim 使 attempt 加一、`lease_token` 加一，設 `locked_by` 與 `lease_expires_at`。Organization filter 與 RLS 並存（RLS 已限制在該 organization，filter 是 AGENTS 要求的 application filter）。
- **Lease fence**：之後每個寫入（`Heartbeat`、`Lock`、`Complete`、`Cancel`、`Fail`）都比對 organization、job、worker、token，且 job 仍為 running、lease 未過期；比對不到就回 `ErrLeaseLost`，什麼都不改。Lease 的時間一律用 DB 的 `now()`；worker 只用自己的時鐘決定何時停止 handler，而且只會更早停（見下）。
- **結束**：handler 的結果與 job 的結束在同一個 transaction：先 `Lock`（fence 下 `FOR UPDATE`，reaper 會跳過被鎖的 job），再寫結果，最後 `Complete` 或 `Cancel`。`Fail` 記錄 `Failure{code, message, retryable}`：可重試且還有 attempt 時回到 queued，等 backoff 後再 claim，否則 failed。存下的只有 Failure，不存 error 原文。
- **取消（TEST-021 的佇列部分）**：`CancelQueued` 只把仍為 queued 的 job 立即改為 cancelled（給 API 在取消 resource 的 transaction 中呼叫），回報是否取消；running 的 job 由 handler 在 checkpoint 看到取消要求後以 `Cancel` 結束（run 的 `cancellation_requested` 與 checkpoint 屬 T-023）。Cancel 與 Complete 都在 fence 下，先者生效，後者得到 `ErrLeaseLost`。
- **Reaper**：每次 pass 在每個 organization 取回 lease 已過期的 running job（`FOR UPDATE SKIP LOCKED`，每次至多 `ReapLimit` 個）：還有 attempt 的回到 queued 並記 `LEASE_EXPIRED`、等 backoff，否則 failed。過期 worker 的 token 留在原處，所以它之後的寫入全部被 fence 擋下。
- **DB 端的生命週期（migration 0007）**：trigger 在 INSERT 與 UPDATE 時強制（DELETE 與 TRUNCATE 屬 retention，runtime role 本來就沒有 DELETE）：
  - job 以 queued、無 attempt／lease／error 建立；queued 只能變成 running（claim：attempt 恰加一、token 變大）或 cancelled；queued 的 job 一定還有 attempt（最後一次 attempt 結束即為終態）；
  - completed、failed、cancelled 不可再改；identity 與輸入（id、organization、type、payload、resource、max_attempts、created_at）不可改；attempt 與 token 不回退；
  - 各 runtime role 只能做自己的那一步（review L3）：API 只能 enqueue、取消或重排（priority、available_at）queued 的 job，不能 claim、結束、接手或重試；worker 從 queued 只能 claim。只有 schema owner（operator、migration）不受這一條限制，但前兩條照樣適用；
  - 寫入不改任何值的 UPDATE 照常通過。

### 參數（`jobs.DefaultPolicy` 與 `cmd/worker`）

| 參數 | 值 | 來源 |
|---|---|---|
| Lease | 30 秒 | D-007 候選值 |
| Heartbeat | 10 秒（至多 lease 的三分之一） | D-007 候選值 |
| 停止 handler 的時間 | 最後一次成功續期（一開始是 claim）的 transaction 開始前的時間 + lease − 半個 heartbeat；預設即 25 秒，可容許一次續期失敗 | 本 ADR（review M1） |
| Max attempts | 3（每個 job 可設 1–10） | API_DATABASE §7 候選值 |
| Backoff | 第 n 次 attempt 後等 5 秒 × 2^(n−1)，最多 5 分鐘 | 本 ADR（API_DATABASE §7 要求有限 exponential backoff，參數待定） |
| 無 job 時的間隔 | 1 秒 | 本 ADR |
| 每次 pass、每個 organization 的 reap 上限 | 100 | 本 ADR |

全部需要 benchmark 與評審（D-007）；不是效能測試的結果。

### Worker 迴圈（`internal/worker`、`cmd/worker`）

- 設定：`WORKER_ID`（必填，1–200 個可見 ASCII 字元，寫入 `locked_by`；fence 靠 token，名稱只用來辨識）與 `WORKER_ORGANIZATION_IDS`（必填，逗號分隔，D-008 的 allowlist，`workerscope` 檢查每個都是 organization ID 且不重複）。連線的 login 必須是 `twin_worker` 的成員，不是 superuser、不能 BYPASSRLS、不是 owner（`tenant.CheckRuntimeRole`），pool 歸還時檢查沒有殘留 tenant scope（`tenant.Guard`）。
- 一次 pass 依 deployment 的順序走過每個 organization：先 reap，再 claim 至多一個 job 並執行。每個 organization 每次 pass 至多一個 job，所以一個 organization 的大量 job 不會餓死其他 organization。一個 process 一次只執行一個 job；要更多並行就多開 process（每個 job 只會被一個 worker 取得，`TestWorkersShareTheQueue`）。
- 執行時另有 heartbeat，handler 一定在 DB 的 lease 結束之前被停止（review M1）：
  - DB 的 lease 從 transaction 的 `now()` 起算，不早於 worker 在該 transaction 開始前讀的時間。Worker 從那個時間（claim 的，之後每次成功續期的）算起，在 lease 減半個 heartbeat 時取消 handler 的 context。Lease 為三個 heartbeat 時，一次續期失敗、下一次成功仍能保住 job。半個 heartbeat 的餘裕涵蓋停止所需的時間與 worker 時鐘略慢於 DB 的情況；更大的時鐘偏差由部署（NTP）避免。
  - 續期發現 lease 已被取得或已結束（`ErrLeaseLost`）時立刻停止。
  - 續期失敗或卡住都不改變期限：續期本身以期限為時限，期限到時不論續期是否還在進行都會停止。
  - 停止之後 handler 做的任何事都不會寫入（每次 DB 存取都先 `Lock`，fence 擋下）。
- **Handler 如何接上（review M3）**：handler 實作 `internal/worker/handler` 的 `Handler`。這個 package 是 leaf，不依賴 worker scope，所以 import、simulation、AI 的 package（T-016、T-023、T-025）不會違反 `worker-scope-only-in-the-worker`；`internal/worker` 執行它們，`cmd/worker` 的 `handlers()` 登錄它們。Handler 拿到的 `Run` 只有三個方法：
  - `Job()`：claim 到的 job；
  - `Checkpoint(ctx, fn)`：在 lease 下（先 `Lock`）的一個 transaction，用來讀輸入、寫進度、檢查是否要求取消；
  - `Finish(ctx, end, fn)`：同上，並在同一個 transaction 中以 `jobs.Completed` 或 `jobs.Cancelled` 結束 job，其他狀態回 `ErrInvalidEnd`。
  - 這兩種 transaction 持有 job 的 row lock，續期要等它；所以必須在 lease 減一個 heartbeat 內結束，否則期限到時 handler 被停止、transaction rollback（review L7）。長的計算放在 transaction 之外，transaction 只寫結果。
- Attempt 的其他結果（依序判斷）：

| Handler 的結果 | 記錄 | 重試 |
|---|---|---|
| lease 已失去 | 不寫任何東西（reaper 或新的 holder 負責） | — |
| `*jobs.Failure`（可包在其他 error 中；worker 正在停止時也以它為準，review L1） | 原樣；code 不符 `^[A-Z][A-Z0-9_]{0,63}$` 時記為 `INTERNAL`（保留其 retry 判斷），message 空白或超過 1000 字時改為通用文字（review L2）；nil 的 `*jobs.Failure` 視為一般 error（review M2） | 依 `Retryable` |
| panic | `INTERNAL` | 否（是 bug，不是暫時的錯誤） |
| worker 收到 SIGINT／SIGTERM | `WORKER_STOPPED` | 是 |
| 沒有呼叫 `Finish` 就回 nil | `NO_RESULT` | 否 |
| 其他 error | `INTERNAL` | 是 |

- **Log（review L8）**：存下的永遠只有上表的 Failure。Handler 的 error 文字會寫進 log（去除控制字元、至多 500 字），所以 handler 的 error 不得含客戶資料、prompt、provider 輸出或 secret（SECURITY §7）；panic 只記 runtime error 的訊息或 panic 值的型別（值可能是 handler 正在處理的資料）與 stack。

- 停止時：取消 handler 的 context，等它返回，在自己的 context（不隨停止取消，時限一個 heartbeat）上記錄結果；若連這也失敗，lease 過期後 reaper 取回。第二個 SIGINT／SIGTERM 立刻結束 process（review I4），給不理會 context 的 handler；迴圈開始前收到的 signal 是停止（exit 0），不是 DB 錯誤。
- 設定在連線與 handler 檢查之前全部驗完，包括 allowlist（review L4）。
- 目前沒有任何 handler：import profiling 是 T-016，simulation 是 T-023，AI proposal 與 analysis 是 T-025。在那之前 `cmd/worker` 驗完設定後以 exit 2（NOT IMPLEMENTED）拒絕啟動，不會 claim 無法執行的 job。

## Consequences

- **At-least-once**：DB 中的結果與 job 的結束一起 commit，所以完成只會發生一次；但 commit 前 worker 當掉或失去 lease 時，job 會再執行一次。Handler 在 `Finish` 之前對外部（object storage）做的事必須可重複（例如 object key 由輸入決定），後續 handler 的任務各自負責。因為 handler 在 lease 結束前就被停止，另一個 worker 要等 lease 結束、reaper 取回、再等 backoff 才能 claim，同一個 job 正常情況下不會同時有兩個 handler；不理會 context 的 handler 例外。
- 不理會 context 的 handler 在失去 lease 後仍會繼續跑到結束，只是寫不進去；handler 應在 checkpoint 檢查 context。
- **與 T-023 的分工（CB-19）**：running→cancelled 的 job 終態已定（`Cancel`、0007）。Run 的 `cancellation_requested`、cancel 與 finalize 的鎖順序（job 再 run）、progress 寫入的 fence（以 `Checkpoint`）、以及 reaper 重排已要求取消的 run（handler 在下一次 attempt 開始時先檢查 `cancellation_requested`）屬 T-023。
- 迴圈本身沒有每個 job 的總時限：時限屬於 handler 與其 resource（D-007 的 simulation wall／event budget，T-023）。
- 每次 pass 每個 organization 兩個短 transaction（reap、claim）；閒置的 worker 每秒 2×N 個 transaction（N 為 assigned organization 數）。Organization 很多時要另立 ADR 設計 dispatcher（ARCHITECTURE §6），不在 worker 中加入跨 tenant 的掃描或 BYPASSRLS。
- `WORKER_STOPPED` 會用掉一次 attempt：部署期間反覆重啟可能讓 job 提早 failed。若成為問題，可改為不計 attempt 的 requeue（需要 owner 決定）。
- 參數改動只需改 `jobs.DefaultPolicy` 與 `cmd/worker` 的常數，不影響 schema。

## 待 owner 決定

- 是否接受上表的參數與 backoff 公式（D-007 的 lease／heartbeat 已是 APPROVED 候選值；其餘為本 ADR 提案）。
- `WORKER_STOPPED` 是否計入 attempt（目前計入）。
