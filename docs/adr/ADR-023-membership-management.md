# ADR-023 — 成員管理與受控 provisioning（T-010）

狀態：PROPOSED（T-010 agent 選定，依 owner「除非真的阻擋作業或重大決策再來問我」先實作；owner 同意後改為 ACCEPTED）。日期：2026-10-18。依據：D-008、D-012、D-036（皆 APPROVED）、API_DATABASE §1、SECURITY §2、ADR-019（T-009 re-review R1）。

## Decision

### Provisioning（D-008）

- 使用者與 organization 只由 operator 以 `cmd/provision-tenant` 建立，API 沒有對應 route，API role 也沒有 `twin.users`、`twin.organizations` 的 INSERT（`TestAPICannotProvision`）。
- 指令：
  - `provision-tenant user -issuer … -subject …`：建立或找到該 OIDC identity 的 user，印出 `user_id`；
  - `provision-tenant organization -name … -owner-issuer … -owner-subject …`：在一個 transaction 中建立 organization、（必要時）owner user、owner membership，並寫 audit `organization.provision`（actor_type `system`，actor_id `provision-tenant`）。
- 資料庫連線為 `GEMINUS_PROVISION_DATABASE_URL`：這個 login 必須能 `SET ROLE twin_owner`，且不是 `twin_api`／`twin_worker` 的成員。Superuser 是每個 role 的成員，所以也被拒絕；provisioning 用自己的 login（db/README.md），建議 `NOINHERIT`：owner 權限只在 `SET ROLE` 之後才有。每個操作以 `SET LOCAL ROLE twin_owner` 執行，隨 transaction 結束。
- 輸入限制與 `tenant.ResolveUser` 相同（issuer ≤2048 bytes、subject ≤255 bytes、非空、UTF-8、無 U+0000），所以建立的 user 都能登入；organization 名稱 1–200 字、無控制字元。
- 位置：tasks.json 規劃的 `scripts/provision-tenant/` 改為 `cmd/provision-tenant`：architecture check 只允許 cmd 開啟資料庫連線，SQL 在 `internal/storage/provisioning`。

### 成員管理（D-012 routes，owner only）

- `GET/POST /v1/organizations/{organization_id}/memberships`、`PATCH/DELETE …/{user_id}`。Path 的 organization 必須等於 `X-Organization-ID` 選定的 organization，否則 404（與非成員相同，不揭露 organization 是否存在）。
- 新增：只能加入已存在的 user（以 ID）；沒有該 user 回 422 `REQUEST_VALIDATION_FAILED`（detail path `/user_id`），已是成員回 409 `LIFECYCLE_CONFLICT`。新增以 Idempotency-Key 冪等（D-011）。ID 是不可猜的 ULID，「不存在」只透露該 ID 不存在，不提供 user 列舉（SECURITY §2）。
- 改 role：設成相同 role 不寫入、不記 audit；PATCH 與 DELETE 依 contract 沒有 Idempotency-Key。
- 最後一位 owner（D-036）：取走唯一 owner 的 owner role 或移除他回 409 `LIFECYCLE_CONFLICT`。Owner 可以降級或移除自己，只要還有其他 owner。
- 列表依 user_id 排序（keyset），cursor 綁 organization 與列表。
- Audit：`membership.create`、`membership.update`、`membership.delete`，target 為 user ID，metadata 只有 role（與先前的 role）。

### 鎖的順序（ADR-019 R1）

- 寫入以 `tenant.DB.RunOwners` 執行：先依 user_id 順序 `SELECT … WHERE role='owner' … FOR UPDATE` 鎖住全部 owner membership（D-036），再讀 caller 的 role；不先以 `FOR SHARE` 鎖 caller 自己的 row。之後再 `FOR UPDATE` 鎖目標 row。
- 兩個 owner 同時變更成員：都先排隊取同一組有序鎖，不會形成 cycle（`TestOwnersChangeAtOnce`）。
- 成員的寫入（`Run(ReadWrite)` 以 `FOR SHARE` 鎖自己的 row）與移除他的變更：變更等寫入結束，寫入不等變更，不會 deadlock；移除生效後，該成員下一個 request 即被拒（`TestRemovalWaitsForAWrite`）。
- Caller 在取鎖期間剛被升為 owner、不在已鎖的集合內時，RunOwners 再取一次鎖；仍不一致回 retryable（503），不在未鎖的狀態下判斷「最後一位 owner」。

## Consequences

- 撤權即時影響寫入與讀取（每個 request 都在 transaction 內查 membership）。SSE stream 尚不存在：長連線在撤權時關閉屬 T-023／TEST-026，本 ADR 不涵蓋。
- 「最後一位 owner」只由 service 保證；API role 對 memberships 有 UPDATE／DELETE，DB 不強制（API_DATABASE §5 已說明）。
- 一個 organization 的成員變更依序執行；P0 成員變更很少，可接受。
