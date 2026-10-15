# ADR-015 — PostgreSQL driver and explicit SQL

狀態：ACCEPTED（T-006 agent 選定；owner KCL1104 於 2026-10-15 同意）。日期：2026-10-15。原報告把「SQL generation：explicit SQL / sqlc」列為 TBD（DECISIONS_AND_TBD.md）。

## Decision

- Driver 與 pool：`github.com/jackc/pgx/v5` v5.11.0（MIT，2026-09-07 發布；依賴 `jackc/puddle/v2`、`pgpassfile`、`pgservicefile`，皆 MIT）。不經 `database/sql`，以便使用 pgx 的 transaction options、SQLSTATE 與 `pgconn.PgError` 欄位。
- 查詢以手寫、參數化的 explicit SQL 放在各 repository；暫不引入 sqlc。理由：P0 的安全性集中在 transaction 邊界（tenant context、lock order、CAS），手寫 SQL 讓這些步驟在 code review 中可見；schema 仍以 `migrations/` 為準，正確性由 `make db-test` 與 `make db-go-test` 在一次性資料庫驗證。
- `internal/storage/postgres` 只提供 pool、`Tx`（error／panic／cancel 皆 rollback，rollback 用與 request 分離的 context）與 `Classify`（SQLSTATE → typed error，不帶 DETAIL 等列值）。tenant scope helper 屬 T-007（D-008）。

## Consequences

- 若 repository 數量或 query 變更頻率讓手寫 SQL 的型別錯誤成為問題，可再評估 sqlc；改變時須保留 `db/tests` 與 Go 整合測試。
- CI db job 安裝 Go 並以 `GEMINUS_REQUIRE_DB_TESTS=1` 執行整合測試，skip 視為失敗；`make backend-test` 沒有資料庫時這些測試標 NOT RUN（skip），不是 PASS。
