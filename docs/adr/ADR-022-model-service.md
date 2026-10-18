# ADR-022 — Model／Draft／Version service（T-012）

狀態：PROPOSED（T-012 agent 選定，依 owner「除非真的阻擋作業或重大決策再來問我」先實作；owner 同意後改為 ACCEPTED）。日期：2026-10-18。依據：D-003、D-028（皆 APPROVED）、D-011、D-020、D-029、API_DATABASE §3–§4、TWIN_IR_SPEC §5。

## Decision

### Package 位置

- Service 在 `internal/models`（application layer，與 `internal/project` 並列），SQL 在 `internal/storage/models`。
- tasks.json 規劃的 `internal/model/service/` 不可行：`internal/model/...` 是 generic kernel，architecture check 禁止它依賴 tenant、storage 或 domain pack（AGENTS.md §2）。

### Draft 的狀態

- **clean**：draft 的 hash 等於其 base version 的 hash（內容相同，不論編輯過幾次）；否則為 **dirty**。
- **out_of_date**：draft 的 base 不是 model 的 latest version。讀取時計算，不另存。
- **revision**：每次寫入 draft（patch、reset、publish 後同步）加 1。ETag 為 `"draft-revision-N"`；service 收 expected revision，HTTP 層（T-013）負責 If-Match 的格式。
- **issues**（D-028）：存在 `model_drafts.issues`（migration 0003），在每次寫入 draft 時計算，讀取時不重算。內容依序為：
  - generic validator 的語意 stage；
  - domain pack 的 `Validate`；
  - 以 base version 的 manifest 做 dataset binding stage（`datasets.Resolve`）。
  - Schema 不合格（含 2 MiB 上限）的 patch 結果不寫入，回 422。

### 寫入的鎖與順序

- 每個寫入先 `SELECT … FOR UPDATE` 鎖 model row，再讀寫 draft（API_DATABASE §4 的 model → draft 順序；之後的 approval 加上 proposal）。同一 model 的寫入因此依序執行。
- revision 或 base 不符一律 409 `MODEL_VERSION_CONFLICT`，不 auto-rebase。

### 各操作

- **Create**（idempotency key）：
  - 以該 domain pack 的 `TemplateProvider` 依 locale 建 template（ADR-020）。
  - 同一 transaction 建立 model、v1（parent null、manifest `[]`）與 draft revision 1，設 latest 為 v1，寫 audit `model.create` 與 idempotency response。
- **Patch draft**（If-Match）：
  - 以 `patch.Apply(ManualDraft)` 套到 draft；patch 本身的錯誤或 schema 不合格的結果回 422，不寫入。
  - 合格就存新 IR、hash 與 issues，revision+1，寫 audit `model.draft.update`。
- **Publish**（idempotency key、If-Match、`expected_base_version_id`）：
  - revision、expected base、draft base 與 latest 必須一致，否則 409 `MODEL_VERSION_CONFLICT`。stale draft 不能 publish（D-003）。
  - clean draft 沒有可發布的內容，回 409 `LIFECYCLE_CONFLICT`，不建立相同內容的新版本。
  - 完整驗證 candidate：generic、pack、以 base manifest 解析 dataset。任一 error 回 422 `MODEL_VALIDATION_FAILED`（details 為 issues）。
  - 新 version 為 `latest.version+1`，parent 為 base，manifest 為 base manifest 中 candidate 有用到的 binding（依 logical_ref 排序）。`model_version_datasets` 同時寫入，由 deferred trigger 檢查與 manifest 一致。
  - Request 的 `comment` 是使用者文字：audit metadata 只放 ID 與數量，contract 也沒有欄位可存，所以目前不保存（已知缺口，交給 T-013 決定是否加入 ModelVersion）。
  - 之後依「版本推進規則」處理 draft；models 的 `latest_version_id` 與 `name`（跟隨 latest 的 IR name）更新；寫 audit `model.publish` 與 idempotency response。
- **Reset**（If-Match、`expected_latest_version_id`、`confirm_discard: true`）：
  - 不符回 409。
  - draft 改為 latest 的 IR，base 為 latest，revision+1，issues 重算；寫 audit `model.draft.reset`。
  - Contract 沒有 idempotency key：重送會因 revision 已變而回 409。
- **版本推進規則**（publish 與之後的 approval 共用）：
  - 新版本建立時，draft 若 clean（hash 等於舊 base），就同步到新版本：IR 與 base 換成新版本，revision+1。
  - draft 若 dirty，完全保留；因為 base 已不是 latest，`out_of_date` 為 true（D-003）。
  - Publish 時 draft 的內容就是新版本，所以總是同步。
- **讀取**：
  - model（含 draft 摘要與 latest 摘要）、draft、version；
  - project 內的 model 列表（`created_at, id` 遞增）；
  - model 的 version 列表（version 遞減，最新在前）；
  - cursor 綁 organization 與所屬的 project 或 model。

### Dataset manifest 的來源

- Publish request 與 draft 都沒有 manifest 欄位，所以手動 publish 只能沿用 base version 的 binding。
- 新的 dataset 只能經 import commit → proposal → approval（T-018、T-026）綁到某個版本。
- Draft 若引用沒有 binding 的 empirical `samples_ref`，issues 中有 `DATASET_BINDING_INVALID`，publish 回 422（TEST-014）。
- 解析 snapshot 需要 dataset object storage（T-016）。在它接上之前：
  - manifest 為空時，不需讀任何物件；
  - manifest 非空而沒有 store 時，回 503 `SERVICE_UNAVAILABLE`，不假裝驗證過。

## Consequences

- 所有寫入依 model 排隊，同一 model 無法並行寫入。P0 一個 model 的編輯者很少，可接受。
- `issues` 是寫入當時的結果：dataset 是 immutable 的，同一 IR 與 manifest 的結果不會變。
- Version 列表最新在前，與 project 列表（最舊在前）不同；兩者都是穩定的 keyset 排序。
