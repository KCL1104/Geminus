# T-007 驗證報告：RLS、runtime roles、pool isolation

**日期：2026-10-15｜狀態：`done`（H-003 agent 驗收）｜需求：REQ-001、REQ-019｜Gate：D-008、D-013、D-036、D-037（皆已批准；D-008 於 2026-10-15 批准）**

逐條輸出在 `checks/evidence/2026-10-15-T-007/`（`summary.tsv` 為 exit code 總表；`red-before-fix.log` 是修改 grant 前的失敗紀錄）。

## 交付內容

| 項目 | 內容 |
|---|---|
| `migrations/0002_rls.up.sql` | D-037／CB-16：worker 對 imports 只有 status、列數、preview、validation、mapping suggestion、error 類欄位的 UPDATE，對 jobs 只有 status、attempt、available_at、lease／lock、heartbeat、error；`twin.guard_worker_import` trigger 禁止 worker 將 import 設為 COMMITTED（mapping、mapping_revision、dataset_version_id、proposal_id 由欄位權限擋下）；trigger function 不對 PUBLIC 開放 |
| `db/tests/roles.sql`（新，由 template 產生） | D-037、D-036 owner membership 鎖定、organizations 無 UPDATE（所以不能 FOR UPDATE）、每張表 FORCE RLS 且有 policy、runtime role 不擁有物件、彼此不互為成員、worker 無 users／memberships／idempotency／draft 存取、twin schema 沒有 PUBLIC 可執行的 function |
| `internal/tenant` | `Run`（ID 先驗、transaction-local scope、先查 membership，非成員回 `ErrNoMembership`）、`Worker`（只服務 deployment 指派的 organization，D-008）、`Guard`（pgxpool AfterRelease：歸還時仍帶 scope 或 role 的連線直接丟棄）、`CheckRuntimeRole`（啟動時拒絕 superuser、BYPASSRLS、CREATEROLE、CREATEDB、twin_owner 成員、同時屬於兩個 runtime role、擁有 twin 物件的 login） |
| 文件 | API_DATABASE §2／§9、SECURITY 依 D-036 改寫 last-owner 鎖定；`db/README.md`；`make db-go-test` 納入 `internal/tenant` |

## 先紅後綠

`roles.sql` 先對既有 grant 執行：10 個 FAILED，**實測重現 CB-16**：worker 可改寫已確認的 mapping、mapping_revision、dataset_version_id、proposal_id、base_version_id，可改 job 的 payload、priority、max_attempts 與目標 ID；設定 COMMITTED 只被 CHECK 擋下（23514），不是權限。修改後全部通過。

## 實際執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make db-test`（C／ICU en-US） | 0 / 0 | invariants、constraints、roles 三套，up/down/up |
| `make db-go-test`（C） | 0 | `internal/storage/postgres` 與 `internal/tenant`，`-race`，skip 視為失敗 |
| `go test -race -v ./internal/tenant/...`（ICU） | 0 | 5 個 test PASS |
| `go test -v ./internal/tenant/...`（無資料庫） | 0 | 1 PASS；4 個整合測試 **SKIP（NOT RUN）** |
| `make backend-test` / `backend-race` / `backend-build` | 0 / 0 / 0 | |
| T-007 mutation（9 Go + 6 SQL） | 0 | 15/15 被擋 |
| T-006 SQL mutation（回歸） | 0 | 18/18 被擋，1 個 documented equivalent |
| `govulncheck v1.9.0` | 0 | No vulnerabilities found |
| `actionlint v1.7.12` | 0 | |
| GitHub Actions [run 38755752833](https://github.com/KCL1104/Geminus/actions/runs/38755752833)（`4d326cb`） | success | 4 個 job 全部 success；db job 執行 `make db-go-test`（含 `internal/tenant`，skip 視為失敗） |

## 驗收測試對照

- **TEST-011**（runtime role + owner bypass）：測試以真實 login（`dt_test_api_login`、`dt_test_worker_login`，無密碼，只在一次性 cluster 的 trust 認證下）連線：`CheckRuntimeRole` 通過；API login `SET ROLE twin_owner`、`twin_worker`、管理者 role 皆被拒；未設 scope 時讀到 0 rows（FORCE RLS）。另以刻意不安全的 login（屬於 twin_owner、同時屬於兩個 runtime role）與 superuser 確認檢查會拒絕。SQL 結構檢查涵蓋每張表 FORCE RLS、runtime role 不擁有物件、互不為成員。
  - 為何不在 SQL 測試裡做：`SET ROLE` 依 session user 授權，在 superuser session 內切 role 再 `SET ROLE twin_owner` 必然成功，測不到真實 login 的行為。
- **TEST-010**（RLS／pool／FK）：`Run` 只看得到所屬 organization 的資料；非成員的 organization 回 `ErrNoMembership` 且 fn 不執行；寫入他 organization 回 `ErrPermission`；跨 tenant parent 回 `ErrReference`；更新看不到的列是 no-op；單一連線 pool 在成功、錯誤、panic、取消之後重用時沒有殘留 scope 或 role，且一般執行不需要丟棄任何連線（證明 scope 本身就是 transaction-local，`Guard` 只是保險）；session 層級的 `set_config` 或 `SET ROLE` 外洩時 `Guard` 會丟棄該連線。
- **TEST-009**（tenant／role 矩陣）：資料庫層的 tenant 隔離已涵蓋；各 HTTP 路徑的 Viewer 403／他 tenant 404 需 API（T-009、T-013），**NOT RUN**。

## 開發中發現並修正

1. Mutation 抓到 pool 測試的盲點：把 scope 改成 session 層級時，`Guard` 每次都丟棄連線，下一個 transaction 看起來仍乾淨，主要性質被保險機制遮住；改為同時檢查「一般執行不會替換任何連線」。
2. `Guard` 的 role 檢查原本沒有測試；補上 session 層級 `SET ROLE` 外洩的案例。
3. 新建的 trigger function 在 `REVOKE … FROM PUBLIC` 之後建立，仍帶預設的 PUBLIC EXECUTE；已明確撤銷並加測試。

## 需 owner 知悉

1. **D-008 尚未批准**：本任務的 tenant 選擇（header 只是 selector、membership 由 token 身分查詢）、受控 provisioning 與 worker allowlist 都依 D-008 提案實作。若 D-008 改變，`internal/tenant` 需要調整。
2. Worker 判定：trigger 以「擁有 twin_worker 權限且不是 twin_owner 成員」判斷是否為 worker；superuser 屬於所有 role，因此不受此 trigger 限制（superuser 本來就不應出現在 runtime）。
3. `Guard` 每次歸還連線多一次 round trip；若 T-037 benchmark 顯示成本過高，可改為抽樣或只在測試環境啟用，但需另立決策。

## 剩餘風險

| 風險 | 說明 |
|---|---|
| 並行下的 last-owner 規則 | D-036 的鎖定查詢已驗證可執行，但「兩個並行請求同時移除不同 owner」的完整 race 測試屬 membership API（T-010） |
| Worker 輪詢與 lease | 只做了 scope 與權限；claim、heartbeat、fencing 的並行測試屬 T-019 |
| 認證來源 | `Scope.UserID` 目前由呼叫者提供；從已驗證 JWT 解析屬 T-008 |

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| HTTP 層 tenant／role 矩陣（TEST-009） | T-009、T-013 |
| JWT 驗證與 user 解析 | T-008 |
| 並行 approval／lease／last-owner race | T-010、T-013、T-019 |

## 2026-10-15 獨立 review 與驗收

**驗收：`done`（2026-10-15）——依 owner 指示由 agent 獨立 review 驗收（H-003），owner 沒有逐行審查。** 審查發現、修正與證據見 [REVIEW_2026-10-15.md](REVIEW_2026-10-15.md)。

修正（commit f3f6089、e324bac、611fb0d）：`CheckRuntimeRole` 改以 `session_user` 檢查，並拒絕 REPLICATION、任何其他角色成員（含只能 SET ROLE 者與 predefined roles）、直接授權、擁有任何物件、可建立物件的 login；`Guard` 檢查所有 `app.*` 設定、session-level SET、cursor、temp table、advisory lock、LISTEN；worker 不能改已 COMMITTED 的 import、不能處理等待人類的 proposal、不能偽造 user audit；所有 twin function 固定 search_path。

**更正：** 本報告與 API_DATABASE §9 原稱 `CheckRuntimeRole` 拒絕 superuser／BYPASSRLS／owner，但對 SET-only 角色、predefined roles 與 startup role 不成立；`Guard`「丟棄任何仍帶 scope 的連線」原本只檢查兩個設定。現在兩者皆成立。

**移交：** `cmd/` 尚未呼叫 Guard 與 CheckRuntimeRole；T-013（API）與 T-019／T-023（worker）必須在啟動時呼叫。
