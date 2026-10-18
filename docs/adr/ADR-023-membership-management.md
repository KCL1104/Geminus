# ADR-023 — 成員管理與受控 provisioning（T-010）

狀態：ACCEPTED（T-010 agent 選定並先實作；owner 於 2026-10-18 批准，含 review round 1 後的鎖定設計，H-009）。日期：2026-10-18。依據：D-008、D-012、D-036（皆 APPROVED）、API_DATABASE §1、§2、§8、SECURITY §2、ADR-017、ADR-019（T-009 re-review R1）、T-010 review round 1（M1–M3、L1–L6、I1–I3）。

## Decision

### Provisioning（D-008）

- 使用者與 organization 只由 operator 以 `cmd/provision-tenant` 建立，API 沒有對應 route，API role 也沒有 `twin.users`、`twin.organizations` 的 INSERT（`TestAPICannotProvision`）。
- 指令：
  - `provision-tenant user -issuer … -subject …`：建立或找到該 OIDC identity 的 user，印出 `user_id`；
  - `provision-tenant organization -name … -owner-issuer … -owner-subject …`：在一個 transaction 中建立 organization、（必要時）owner user、owner membership，並寫 audit `organization.provision`（actor_type `system`，actor_id `provision-tenant`）。
- 資料庫連線為 `GEMINUS_PROVISION_DATABASE_URL`：這個 login 必須能 `SET ROLE twin_provisioner`，不是 `twin_owner`、`twin_api`、`twin_worker` 的成員，且沒有 SUPERUSER、BYPASSRLS、CREATEROLE、CREATEDB、REPLICATION 屬性（`TestCheckOperator`，T-010 review L2、I3；D-042）。Superuser 也是每個 role 的成員。Provisioning 用自己的 login（db/README.md），建議 `NOINHERIT`：權限只在 `SET ROLE` 之後才有。每個操作以 `SET LOCAL ROLE twin_provisioner` 執行，隨 transaction 結束；`twin_provisioner` 只有 users 的 SELECT／INSERT 與 organizations、memberships、audit_events 的 INSERT，RLS 照樣適用（migration 0004，`db/tests/roles.sql`）。
- 同一 identity 的 `user` 可並行執行：`INSERT … ON CONFLICT (oidc_issuer, oidc_subject) DO NOTHING` 後再讀，只建立一次（`TestUserOnceUnderConcurrency`，review L3）。
- 輸入限制與 ADR-017 verifier 相同（`auth.ValidIdentity`）：issuer 是 verifier 設定會接受的 absolute https URL（無 query、fragment、credentials，≤2048 bytes）且可列印，subject 1–255 bytes 且可列印（無控制、格式字元如 U+202E／U+200B、ASCII 空白以外的分隔字元），所以建立的 user 都能登入。Organization 名稱 1–200 字、全為可列印字元、不能全空白或以空白開頭結尾（review L4）。
- 位置：tasks.json 規劃的 `scripts/provision-tenant/` 改為 `cmd/provision-tenant`：architecture check 只允許 cmd 開啟資料庫連線，SQL 在 `internal/storage/provisioning`。

### 成員管理（D-012 routes，owner only）

- `GET/POST /v1/organizations/{organization_id}/memberships`、`PATCH/DELETE …/{user_id}`。Path 的 organization 必須等於 `X-Organization-ID` 選定的 organization，否則 404（與非成員相同，不揭露 organization 是否存在）。
- 新增：只能加入已存在的 user（以 ID）；沒有該 user 回 422 `REQUEST_VALIDATION_FAILED`（detail path `/user_id`），已是成員回 409 `LIFECYCLE_CONFLICT`。新增以 Idempotency-Key 冪等（D-011）。ID 是不可猜的 ULID，「不存在」只透露該 ID 不存在，不提供 user 列舉（API_DATABASE §2）。
- 改 role：設成相同 role 不寫入、不記 audit；PATCH 與 DELETE 依 contract 沒有 Idempotency-Key。
- 最後一位 owner（D-036）：取走唯一 owner 的 owner role 或移除他回 409 `LIFECYCLE_CONFLICT`。Owner 可以降級或移除自己，只要還有其他 owner。
- 列表依 user_id 排序（keyset），cursor 綁 organization 與列表。
- Audit：`membership.create`、`membership.update`、`membership.delete`，target 為 user ID，metadata 只有 role（與先前的 role）。

### 鎖的順序（ADR-019 R1；T-010 review M1–M3）

寫入以 `tenant.DB.RunOwners` 執行，依序：

1. **不上鎖先檢查**：讀 caller 的 role（不上鎖）；非成員回 `ErrNoMembership`（404）、非 owner 回 `ErrForbidden`（403），不取任何鎖。被拒的 request（含知道 organization ID 的其他 tenant 使用者）不等、也不擋任何寫入（`TestRefusedRequestsTakeNoLock`；review M1）。
2. **每個 organization 一次一個成員變更**：`SELECT pg_advisory_xact_lock(hashtextextended('twin.memberships:' || organization_id, 0))`。Transaction 層級的 advisory lock 隨 commit／rollback 釋放，pool 的 `Guard` 不受影響。只有成員變更取這個鎖。
3. **D-036 的有序 owner 鎖**：依 user_id 順序 `SELECT … WHERE role='owner' … FOR UPDATE`，再檢查人數。這個 statement 在前一個變更 commit 之後才開始，所以看到的 owner 集合是最新的，且在本 transaction 結束前不會再變（`TestOwnerSetIsCurrent`；review M3）。
4. **再讀一次 caller 的 role**：等待期間被降級或移除的 caller 依 1 的方式被拒，什麼都不改（`TestCallerChangedWhileWaiting`）。
5. 之後以 `FOR UPDATE` 鎖目標 row，執行變更、audit 與 idempotency。

為什麼不會 deadlock：membership row 的鎖只有兩種人取。成員變更已由 2 排成一次一個；`Run(ReadWrite)` 的 `FOR SHARE` 是它在 transaction 中第一個取的鎖，等待時不持有任何鎖，取得後也不再等其他 membership row。所以沒有兩個 transaction 能各持一個 membership row 又互等。`TestMembershipChangesUnderLoad`（3 個 owner、6 個 user、10 個 worker、5 秒，隨機升降級、移除、重新加入、寫入、列表；每輪約 370–420 次成功變更）與 `TestOwnerLockAcrossPlans`（審查者的 probe：兩個變更用不同 plan，heap 順序不同於 user_id 順序）都沒有 40P01，也從未出現沒有 owner 的 organization。拿掉 advisory lock 與 ORDER BY 時兩者都出現 40P01。

D-036 的 ORDER BY 在有 advisory lock 之後不再是避免 deadlock 所必需（拿掉它的 mutant 不被任何測試殺死，列為等價並寫明理由），但 D-036 已批准，保留它；鎖 owner row 本身仍是必要的：它讓變更等 owner 進行中的寫入結束。

語意：request 以它開始時已 commit 的 role 判斷。尚未 commit 的升級不算，caller 會收到 403，重試即可（取代原本的「再取一次鎖」）。

## Consequences

- 撤權即時影響寫入與讀取（每個 request 都在 transaction 內查 membership）。SSE stream 尚不存在：長連線在撤權時關閉屬 T-023／TEST-026（已寫入 T-023 的 scope），本 ADR 不涵蓋。
- 「最後一位 owner」只由 service 保證；API role 對 memberships 有 UPDATE／DELETE，DB 不強制（API_DATABASE §8 已說明）。
- 一個 organization 的成員變更依序執行；P0 成員變更很少，可接受。
- 等待關係（修正先前「寫入不等變更」的錯誤說法）：
  - 變更等所有 owner 進行中的寫入（有序 `FOR UPDATE` 與它們的 `FOR SHARE` 衝突），也等目標成員進行中的寫入；
  - 變更進行中（含排隊等鎖時已鎖到的 owner row），那些 owner 新開始的寫入要等它結束；目標成員的寫入也要等；
  - 其他成員的寫入、所有讀取、所有被拒的 request 都不等。
  - 所以一個很長的 owner 寫入會讓之後的成員變更與部分 owner 的寫入一起等。P0 的寫入都很短，可接受；若日後出現長 transaction，需要 lock timeout 或改設計。
- 已知風險（review I3）：
  - owner 可以把任何已存在的 user ID 加入自己的 organization，不需要該 user 同意（D-008 允許加入已知 user）；
  - 新增回 422（user 不存在）與 409／201（存在）的差異，會透露某個全域 user ID 是否存在。ULID 不可猜，風險低，P0 接受；若日後需要，改為一律回同一種答案或改用邀請流程。
  - Request 先驗格式再驗權限：viewer 送錯誤格式會先得到 400／422 而不是 403。不揭露資料，無害。
- **Provisioning 的最小權限 role（review L5；D-042 APPROVED，H-009，已實作）**：provisioning login 原以 `twin_owner`（schema owner）執行，平常的 onboarding 憑證因此也有 DDL 與 policy 權限。現在改用 NOLOGIN 的 `twin_provisioner`（`db/bootstrap_roles.sql` 建立、migration 0004 授權），只能做 provisioning 需要的事。部署需要先跑 bootstrap 與 0004，再把 provisioning login 改為 `IN ROLE twin_provisioner`；原本 `IN ROLE twin_owner` 的 login 會被拒絕。
- **Audit 只能以自己的身分寫入（實作 D-042 時發現）**：0002 的 guard 只限制 `user` event，API role 可以寫出 `worker` 或 `system` 的 event。Migration 0004 改為：API 只寫 scoped user 的 `user` event，worker 寫 `worker` 或 `system`（不能以人的身分），provisioning 只寫 `system`；schema owner 不受限。
