# ADR-024 — PostgreSQL job queue 與 worker 迴圈（T-019）

狀態：PROPOSED（T-019 agent 提出並先實作；待 owner 決定）。日期：2026-10-19。依據：D-007、D-008、D-011（皆 APPROVED）、ARCHITECTURE §6、API_DATABASE §6–§7、TEST-020、TEST-021、ADR-019（T-009 re-review R3b）。

## Decision

### 佇列（`internal/storage/jobs`）

- 只用 PostgreSQL 的 `twin.jobs`，不加 broker（ARCHITECTURE §6、AGENTS §7）。API 在建立或更新 resource、寫 audit 的同一個 transaction 中 `Enqueue`（D-011：202 只在三者一起 commit 之後回）。Job 必須恰有一個與 type 相符的 resource、payload 是 JSON object（只放凍結的輸入參照，不放 secret 或客戶原始資料）、`max_attempts` 1–10，否則在送到 DB 之前就拒絕。
- **Claim**：在一個 organization 的 transaction 中（`internal/tenant/workerscope`，D-008），只取 queued、`available_at` 已到、還有 attempt、且 worker 有 handler 的 type；順序為 priority 高者先，再依 `created_at`、`id`；`FOR UPDATE SKIP LOCKED`。Claim 使 attempt 加一、`lease_token` 加一，設 `locked_by` 與 `lease_expires_at`。Organization filter 與 RLS 並存（RLS 已限制在該 organization，filter 是 AGENTS 要求的 application filter）。
- **Lease fence**：之後每個寫入（`Heartbeat`、`Lock`、`Complete`、`Cancel`、`Fail`）都比對 organization、job、worker、token，且 job 仍為 running、lease 未過期；比對不到就回 `ErrLeaseLost`，什麼都不改。時間一律用 DB 的 `now()`，不用 worker 的時鐘。
- **結束**：handler 的結果與 job 的結束在同一個 transaction：先 `Lock`（fence 下 `FOR UPDATE`，reaper 會跳過被鎖的 job），再寫結果，最後 `Complete` 或 `Cancel`。`Fail` 記錄 `Failure{code, message, retryable}`：可重試且還有 attempt 時回到 queued，等 backoff 後再 claim，否則 failed。存下的只有 Failure，不存 error 原文。
- **取消（TEST-021 的佇列部分）**：`CancelQueued` 只把仍為 queued 的 job 立即改為 cancelled（給 API 在取消 resource 的 transaction 中呼叫），回報是否取消；running 的 job 由 handler 在 checkpoint 看到取消要求後以 `Cancel` 結束（run 的 `cancellation_requested` 與 checkpoint 屬 T-023）。Cancel 與 Complete 都在 fence 下，先者生效，後者得到 `ErrLeaseLost`。
- **Reaper**：每次 pass 在每個 organization 取回 lease 已過期的 running job（`FOR UPDATE SKIP LOCKED`，每次至多 `ReapLimit` 個）：還有 attempt 的回到 queued 並記 `LEASE_EXPIRED`、等 backoff，否則 failed。過期 worker 的 token 留在原處，所以它之後的寫入全部被 fence 擋下。
- **DB 端的生命週期（migration 0007）**：trigger 對所有 role（含 API 的整表 UPDATE 與 schema owner）強制：job 以 queued、無 attempt／lease／error 建立；queued 只能變成 running（claim：attempt 恰加一、token 變大）或 cancelled；completed、failed、cancelled 不可再改；identity 與輸入（id、organization、type、payload、resource、max_attempts、created_at）不可改；attempt 與 token 不回退。寫入不改任何值的 UPDATE 照常通過。

### 參數（`jobs.DefaultPolicy` 與 `cmd/worker`）

| 參數 | 值 | 來源 |
|---|---|---|
| Lease | 30 秒 | D-007 候選值 |
| Heartbeat | 10 秒（至多 lease 的三分之一：lease 結束前容許兩次續期失敗） | D-007 候選值 |
| 每次續期的時限 | 一個 heartbeat | 本 ADR |
| Max attempts | 3（每個 job 可設 1–10） | API_DATABASE §7 候選值 |
| Backoff | 第 n 次 attempt 後等 5 秒 × 2^(n−1)，最多 5 分鐘 | 本 ADR（API_DATABASE §7 要求有限 exponential backoff，參數待定） |
| 無 job 時的間隔 | 1 秒 | 本 ADR |
| 每次 pass、每個 organization 的 reap 上限 | 100 | 本 ADR |

全部需要 benchmark 與評審（D-007）；不是效能測試的結果。

### Worker 迴圈（`internal/worker`、`cmd/worker`）

- 設定：`WORKER_ID`（必填，1–200 個可見 ASCII 字元，寫入 `locked_by`；fence 靠 token，名稱只用來辨識）與 `WORKER_ORGANIZATION_IDS`（必填，逗號分隔，D-008 的 allowlist，`workerscope` 檢查每個都是 organization ID 且不重複）。連線的 login 必須是 `twin_worker` 的成員，不是 superuser、不能 BYPASSRLS、不是 owner（`tenant.CheckRuntimeRole`），pool 歸還時檢查沒有殘留 tenant scope（`tenant.Guard`）。
- 一次 pass 依 deployment 的順序走過每個 organization：先 reap，再 claim 至多一個 job 並執行。每個 organization 每次 pass 至多一個 job，所以一個 organization 的大量 job 不會餓死其他 organization。一個 process 一次只執行一個 job；要更多並行就多開 process（每個 job 只會被一個 worker 取得，`TestWorkersShareTheQueue`）。
- 執行時另有 heartbeat goroutine。Lease 被別人取得或已結束（`ErrLeaseLost`），或一整個 lease 都續不上（DB 無回應時每次續期在一個 heartbeat 後逾時），handler 的 context 就被取消；之後 handler 做的任何事都不會寫入（`Finish` 先 `Lock`，fence 擋下）。
- Handler 只能以 `Finish(ctx, jobs.Completed 或 jobs.Cancelled, fn)` 結束 job，其他狀態回 `ErrInvalidEnd`。Attempt 的其他結果：

| Handler 的結果 | 記錄 | 重試 |
|---|---|---|
| `*jobs.Failure`（可包在其他 error 中） | 原樣 | 依 `Retryable` |
| 其他 error | `INTERNAL`（error 原文只寫 log） | 是 |
| panic | `INTERNAL` | 否（是 bug，不是暫時的錯誤） |
| 沒有呼叫 `Finish` 就回 nil | `NO_RESULT` | 否 |
| worker 收到 SIGINT／SIGTERM | `WORKER_STOPPED` | 是 |
| lease 已失去 | 不寫任何東西（reaper 或新的 holder 負責） | — |

- 停止時：取消 handler 的 context，等它返回，在自己的 context（不隨停止取消，時限一個 heartbeat）上記錄 `WORKER_STOPPED`；若連這也失敗，lease 過期後 reaper 取回。
- 目前沒有任何 handler：import profiling 是 T-016，simulation 是 T-023，AI proposal 與 analysis 是 T-025。在那之前 `cmd/worker` 驗完設定後以 exit 2（NOT IMPLEMENTED）拒絕啟動，不會 claim 無法執行的 job。

## Consequences

- **At-least-once**：DB 中的結果與 job 的結束一起 commit，所以完成只會發生一次；但 commit 前 worker 當掉或失去 lease 時，job 會再執行一次。Handler 在 `Finish` 之前對外部（object storage）做的事必須可重複（例如 object key 由輸入決定），後續 handler 的任務各自負責。
- 不理會 context 的 handler 在失去 lease 後仍會繼續跑到結束，只是寫不進去；handler 應在 checkpoint 檢查 context。
- 迴圈本身沒有每個 job 的總時限：時限屬於 handler 與其 resource（D-007 的 simulation wall／event budget，T-023）。
- 每次 pass 每個 organization 兩個短 transaction（reap、claim）；閒置的 worker 每秒 2×N 個 transaction（N 為 assigned organization 數）。Organization 很多時要另立 ADR 設計 dispatcher（ARCHITECTURE §6），不在 worker 中加入跨 tenant 的掃描或 BYPASSRLS。
- `WORKER_STOPPED` 會用掉一次 attempt：部署期間反覆重啟可能讓 job 提早 failed。若成為問題，可改為不計 attempt 的 requeue（需要 owner 決定）。
- 參數改動只需改 `jobs.DefaultPolicy` 與 `cmd/worker` 的常數，不影響 schema。

## 待 owner 決定

- 是否接受上表的參數與 backoff 公式（D-007 的 lease／heartbeat 已是 APPROVED 候選值；其餘為本 ADR 提案）。
- `WORKER_STOPPED` 是否計入 attempt（目前計入）。
