# ADR-022 — Model／Draft／Version service（T-012）

狀態：ACCEPTED（T-012 agent 選定並先實作；owner 於 2026-10-18 批准，H-009）。日期：2026-10-18。依據：D-003、D-028（皆 APPROVED）、D-011、D-020、D-029、API_DATABASE §3–§4、TWIN_IR_SPEC §5。

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
  - 前一個 stage 有 error 就不跑下一個（D-023；T-012 review I1）。結果依 D-023 排序。
  - 最多 1000 個（migration 0003 與 `Error.details` 的上限；T-012 review M2；patch 本身與 schema 的 422 也一樣，review 2 N1）：超過時保留排序後的前 999 個，最前面加一個 path 為 `""` 的 `LIMIT_EXCEEDED`，說明還有幾個沒列出；被略去的有 error 時它是 error，所以 publish 是否被擋與完整清單相同。
  - Path：IR 的部分相對於 IR（去掉 `/ir`）；dataset 的部分是 `/dataset_bindings/<i>/…`，`i` 是 base version 的 `dataset_bindings` 中的位置（TWIN_IR_SPEC §5.2；T-012 review L2）。
  - Schema 不合格（含 2 MiB 上限）的 patch 結果不寫入，回 422。

### 寫入的鎖與順序

- 每個寫入先 `SELECT … FOR UPDATE` 鎖 model row，再讀寫 draft（API_DATABASE §4 的 model → draft 順序；之後的 approval 加上 proposal）。同一 model 的寫入因此依序執行。
- 讀取（model、draft、version 與列表）在一個 REPEATABLE READ 的唯讀 transaction（`tenant.ReadSnapshot`）中執行：publish 一起改動 model、draft 與 version，讀取不會看到一半（T-012 review L1：分開的 READ COMMITTED 讀取曾把只有手動編輯的 draft 報成 out_of_date）。
- revision 或 base 不符一律 409 `MODEL_VERSION_CONFLICT`，不 auto-rebase。

### 各操作

- **Create**（idempotency key）：
  - 以該 domain pack 的 `TemplateProvider` 依 locale 建 template（ADR-020）。
  - Template 要通過 schema、generic 語意與 pack 規則：有 error 是 pack 的錯，回 internal error、不建立任何東西；warning 隨 draft 保存（API_DATABASE §4；T-012 review L4）。
  - 同一 transaction 建立 model、v1（parent null、manifest `[]`）與 draft revision 1，設 latest 為 v1，寫 audit `model.create` 與 idempotency response。
- **Patch draft**（If-Match）：
  - 以 `patch.Apply(ManualDraft)` 套到 draft 的 canonical 形式；patch 本身的錯誤或 schema 不合格的結果回 422，不寫入。
  - 字串或 member name 含 U+0000 的結果回 422（`IR_SCHEMA_INVALID`，path 指向該值；member name 則指向其物件，與 `httpapi.DecodeJSON` 相同）：PostgreSQL 的 text 與 jsonb 無法存它（T-012 review L5、review 2 I2）。
  - 合格就存新 IR、hash 與 issues，revision+1，寫 audit `model.draft.update`。
- **Publish**（idempotency key、If-Match、`expected_base_version_id`）：
  - revision、expected base、draft base 與 latest 必須一致，否則 409 `MODEL_VERSION_CONFLICT`。stale draft 不能 publish（D-003）。
  - clean draft 沒有可發布的內容，回 409 `LIFECYCLE_CONFLICT`，不建立相同內容的新版本。
  - 完整驗證 candidate 的 canonical 形式：generic、pack、以 base manifest 解析 dataset。任一 error 回 422 `MODEL_VALIDATION_FAILED`（details 為 issues）。
  - 新 version 為 `latest.version+1`，parent 為 base，manifest 為 base 的完整 manifest：沒用到的 binding 也驗證並凍結（TWIN_IR_SPEC §5.2：run 沿用 version manifest）。`model_version_datasets` 同時寫入，由 deferred trigger 檢查與 manifest 一致。
  - Request 的 `comment` 是使用者文字：audit metadata 只放 ID 與數量，contract 也沒有欄位可存，所以目前不保存（已知缺口，交給 T-013 決定是否加入 ModelVersion）。
  - 之後依「版本推進規則」處理 draft；models 的 `latest_version_id` 與 `name`（跟隨 latest 的 IR name）更新；寫 audit `model.publish` 與 idempotency response。
- **Reset**（If-Match、`expected_latest_version_id`、`confirm_discard: true`）：
  - 不符回 409。
  - draft 改為 latest 的 IR，base 為 latest，revision+1，issues 重算；寫 audit `model.draft.reset`。
  - Contract 沒有 idempotency key：重送會因 revision 已變而回 409。
- **版本推進規則**（publish 與之後的 approval 共用）：
  - 新版本建立時，draft 若 clean（hash 等於它自己的 base，不論該 base 是否為 latest；T-012 review L3），就同步到新版本：IR 與 base 換成新版本，revision+1。
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
- 手動 publish 保留 base 的所有 binding，所以暫時不用的資料之後還能再用（T-012 review M3）。首版只凍結用到的 binding，與 TWIN_IR_SPEC §5.2 不符，已改正。
- 解析 snapshot 需要 dataset object storage（T-016）。在它接上之前：
  - manifest 為空時，不需讀任何物件；
  - manifest 非空（不論是否用到）而沒有 store 時，回 503 `SERVICE_UNAVAILABLE`，不假裝驗證過。

## Owner 的決定（H-009：D-040、D-041 APPROVED，於 T-013 開放 route 之前實作）

Owner 採用下列兩項的建議方案：IR 欄位改存 canonical 文字（D-040，選項 1），ModelVersion 保存 `comment`（D-041）。以下保留決定前的分析。

- **儲存形式的讀取放大（T-012 review M4）**：jsonb 保存值而不是 bytes，讀出時數字以完整十進位印出（`1e300` 印成 301 位）。2 MiB 上限量的是 canonical bytes，reviewer 以 1.94 MB 的 canonical draft 得到 83.6 MB 的 jsonb 文字，一次 GetDraft 3.9 秒；每次 patch、publish 也要讀它。T-012 的檢查已改用 canonical 形式（M1），所以不會再誤判上限，但成本仍與 jsonb 文字成正比，寫入也一樣：複核時 1.94 MB 的 canonical draft（83.6 MB 文字）GetDraft 3.5 秒、patch 4.9 秒、publish 5.8 秒（T-012 review 2 N3）。Run 與 worker（T-021／T-023）讀 version IR 時也受影響。記為 D-040，為 T-013 的 decision gate。選項：
  1. **（建議）IR 欄位改為保存 canonical 文字**：新 migration 把 `model_versions.ir`、`model_drafts.ir`（及之後 scenario 的 patch 等）改為 `json`（保存原文字）或 `text`。讀出即為 hash 涵蓋的 bytes，SQL 也能以 `sha256` 核對 hash；不再需要讀取時 canonicalize。代價：一個 migration 與 storage 調整；失去 jsonb 運算子（目前沒有查詢使用）。
  2. 在 IR contract 限制自由容器（metadata、domain_attributes）中數字的寫法（例如 |x| 在 1e-6 到 1e21 之間）：需改 TWIN_IR_SPEC 與 validator，且是使用者看得到的限制。
  3. 不改，只靠 rate limit（T-036）：不建議。
- **Publish 的 `comment`（T-012 review L9；D-041）**：contract 允許最多 2000 字並納入 idempotency hash，但沒有地方保存。選項：在 ModelVersion 加欄位保存（需 migration 與 contract），或從 PublishModel 移除。建議保存：版本說明對審閱有用，且屬使用者資料，不放 audit。

## Consequences

- 所有寫入依 model 排隊，同一 model 無法並行寫入。P0 一個 model 的編輯者很少，可接受。
- `issues` 是寫入當時的結果：dataset 是 immutable 的，同一 IR 與 manifest 的結果不會變。
- Version 列表最新在前，與 project 列表（最舊在前）不同；兩者都是穩定的 keyset 排序。
- Model 列表每個 model 另讀 draft 與 latest version（每頁最多 2×100 個查詢；T-012 review I4），P0 可接受，T-013 若需要再合併為一個查詢。
- `latest_version_id` 與 draft revision 只遞增，由 service 保證；API role 對 models、model_drafts 有 UPDATE，DB 不強制（T-012 review I6）。
- Idempotency key 24 小時後過期，之後同一 key 的 create 會建立第二個 model（與 createProject 相同，D-011；T-012 review I5）。
