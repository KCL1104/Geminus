# T-006 驗證報告：Core migration 與 repository foundation

**日期：2026-10-15｜狀態：`review`（待 owner 驗收）｜需求：REQ-001、REQ-005、REQ-018、REQ-019｜Gate：D-001、D-013、D-034、D-035（皆已批准）；另落實 D-027 的 SQL 部分**

逐條輸出在 `checks/evidence/2026-10-15-T-006/`（`summary.tsv` 為 exit code 總表；`red-before-fix.log` 是修改前的失敗紀錄）。

## 交付內容

| 項目 | 內容 |
|---|---|
| `migrations/0001_core.up.sql` | 每種 ID 一個 domain（`twin.org_id`…`twin.art_id`，`<prefix>_<ULID>`，D-001），89 個欄位改型；`twin.object_key_ok`（`starts_with`、拒 `..`、`//`、反斜線、控制字元、超過 1024 字元；D-035／CB-14）取代 4 處 LIKE；`twin.logical_ref`、`twin.time_unit` domain，manifest 比對 `ORDER BY logical_ref COLLATE "C"`（D-034／CB-15），run_datasets 補 unit／logical_ref／sample_count 上限；ai_proposals 新增 `server_risk_level`、`provider_risk_claim` 與 risk = max 檢查，審核後不可改（D-027）；audit user actor 必須是 `usr_` ID；immutable 表加 BEFORE TRUNCATE guard |
| `migrations/0002_rls.up.sql` | 所有 domain 的 USAGE、`object_key_ok` 的 EXECUTE、worker 可寫兩個 risk 欄位 |
| `db/tests/constraints.sql`（新）、`invariants.sql`（改 ULID 與真實 hash） | 由 `db/tests/templates/` 經 `scripts/render_db_tests.py` 產生；`make spec-check` 檢查未過期 |
| `internal/storage/postgres` | pgx v5.11.0 pool、`Tx`（error／panic／cancel 皆 rollback，rollback 用分離的 context）、`Classify`（SQLSTATE → typed error，不帶 DETAIL 或未分類錯誤的訊息）；整合測試 |
| Make／CI | `make db-go-test`；CI db job 裝 Go，在 migration 後跑整合測試且 skip 視為失敗；`check_sql_inventory.py` 改檢 `twin.org_id`、禁 LIKE、要求 `COLLATE "C"` |
| 文件 | `db/README.md` 重寫、API_DATABASE §8、SECURITY object key、ADR-015（pgx + explicit SQL，PROPOSED） |

處理方式：0001／0002 尚未在任何持久環境執行過（只在一次性 `dt_test_*` 資料庫），因此直接修改草案並在檔頭與 `db/README.md` 寫明：T-006 驗收後，已套用到持久環境的 migration 不再改寫，改以新編號檔案變更。

## 先紅後綠

修改 migration 前先寫 `constraints.sql`，在 ICU en-US 資料庫對舊 migration 執行（collect 模式）：25 個 FAILED，涵蓋 D-001 ID、audit actor／target／request ID、LIKE 萬用字元與 `//`、反斜線、控制字元、裸前綴的 object key、無 LIKE 的結構檢查，並在 byte 排序 manifest 被拒（CB-15）處中止。這也首次**實測重現 CB-15**：ICU en-US 下 `dataset:a_b` 排在 `dataset:a-b` 之前，byte 排序相反（T-001 時因環境沒有非 C collation 而未能實測）。修改後同一套測試在 C 與 ICU 資料庫皆通過。

## 實際執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | 含新 `spec-db-tests` 與更新後的 SQL inventory |
| `make db-test`（C.utf8 資料庫） | 0 | bootstrap → up → invariants + constraints → down → up → 兩套測試 |
| `make db-test`（ICU en-US 資料庫） | 0 | 同上；非 C collation 下 D-034 測試有意義 |
| `make db-go-test`（C） | 0 | `-race`，`GEMINUS_REQUIRE_DB_TESTS=1` |
| `go test -race -v ./internal/storage/postgres/...`（ICU） | 0 | 5 個 test PASS |
| `go test -v ./internal/storage/postgres/...`（無資料庫） | 0 | `TestClassify` PASS；4 個整合測試 **SKIP（NOT RUN）**，訊息寫明需要資料庫 |
| `make backend-test` / `backend-race` / `backend-build` | 0 / 0 / 0 | |
| SQL mutation（每個 mutant 一個新 ICU 資料庫） | 0 | 18/18 被擋；1 個 documented equivalent |
| Go mutation（`go test -overlay`，對 ICU 資料庫） | 0 | 9/9 被擋；1 個 documented equivalent |
| `govulncheck v1.9.0` | 0 | 加入 pgx 後 No vulnerabilities found |
| `make frontend-test` | 0 | 未改前端，確認無回歸 |
| `actionlint v1.7.12` | 0 | |

## 驗收測試對照

- **TEST-004**（version immutability／parent）：runtime role 對 7 個 immutable 表沒有 UPDATE／DELETE／TRUNCATE；owner 的 UPDATE、DELETE、TRUNCATE（含 `TRUNCATE … CASCADE`）回 55000；v2 以同 model 的 v1 為 parent 可建立；parent 為他 model、他 org 或 model 屬他 org 時回 23503；IR 的 model_id 不符回 23514；simulation_runs 沒有指向 draft 的 FK。
- **TEST-010**（RLS／pool／FK）：無 context 讀到 0 rows、跨 tenant 讀寫失敗、跨 tenant parent FK 失敗（invariants.sql 與 Go 測試）；**單一連線的 pool 重用時 `set_config(..., true)` 與 `SET LOCAL ROLE` 不殘留**，並以 session 層級設定的對照組證明測試能偵測洩漏。以真實 runtime login 連線、tenant scope helper 與並行下的 pool 隔離屬 **T-007，NOT RUN**。
- **TEST-030**（部分）：up/down/up 與 runtime grants 已在兩種 collation 執行；staging smoke、backup restore、人工 production gate **NOT RUN**（屬部署相關任務）。

## Equivalent mutants

1. **移除 domain 的 USAGE grant**：PostgreSQL 型別預設對 PUBLIC 有 USAGE，DML 也不檢查欄位型別的 USAGE；真正的閘門是 `REVOKE ALL ON SCHEMA twin FROM PUBLIC`。grant 保留作為明示。
2. **rollback 用已取消的 context**：pgx 在 context 已取消時關閉連線，PostgreSQL 隨即結束 transaction 並釋放 lock，可觀察行為相同；分離 context 只是避免丟掉一條連線。

## 開發中發現並修正

1. 測試 helper 第一版遇到「意外成功」不會 rollback，collect 模式下會連鎖誤報；改為每個 `must_fail` 一律在 subtransaction 內 rollback。
2. ID domain 的結構檢查第一版會放過「名稱像 ID 卻沒有 domain」的純 text 欄位，且 audit 與 idempotency 的 `actor_id` 對應錯誤；改為未分類即失敗、依表分別對應。
3. `Classify` 第一版把未分類錯誤的 server message 一併回傳；22P02 等訊息會引用輸入值，改為只回 SQLSTATE。
4. Panic 測試第一版偵測不到連線洩漏（未 commit 的資料本來就看不到）；改用單一連線 pool 加 deadline。

## 需 owner 知悉的實作解讀

1. `audit_events.actor_id` 對 worker／system actor 暫用寬鬆格式（`twin.actor_ref`），user actor 強制 `usr_` ULID；正式格式待 T-035。`target_id` 接受任何已知 prefix 的 ID。
2. dataset object key 只要求 `tenant/<org>/` 前綴（D-035 原文）；imports 與 simulation-results 維持各自子路徑。SECURITY.md 列的 `assets` 前綴目前沒有對應欄位。
3. Risk：`risk_level` 有值時 server 與 provider 兩者都必須有值且等於最大值；只有 provider claim、尚未分類的 proposal（例如 invalid）允許存在。

## 剩餘風險

| 風險 | 說明 |
|---|---|
| Collation 覆蓋 | 本機測了 C 與 ICU en-US，CI 為 libc en_US.utf8；其他 ICU locale 未測，但 manifest 比對已固定 `COLLATE "C"`、`logical_ref` 限 ASCII |
| 未經 RLS 的 RI 檢查 | FK 檢查不受 RLS 影響（PostgreSQL 設計），跨 tenant 防護仰賴 composite FK；已以測試覆蓋主要 parent 關係，未逐一列舉所有 FK |
| D-036／D-037、CB-13／CB-16 | last-owner 鎖定與 worker 欄位級權限屬 T-007，本次未改；worker 仍有 imports／jobs 整表 UPDATE |

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| Runtime login、tenant scope helper、並行 pool 隔離 | T-007（gate D-008 仍為 PROPOSED） |
| Staging smoke、backup／restore、production gate（TEST-030 其餘部分） | 部署任務；需要 owner 授權的環境 |
| GitHub Actions | 推送後補記 |
