# T-013 驗證報告：Model 的 HTTP contract 與 client 整合

**日期：2026-10-19｜狀態：`review`（實作與自我驗證完成；送獨立 review，依 H-003 驗收）｜需求：REQ-004、REQ-005、REQ-006｜Gate：D-012、D-015、D-024、D-025（APPROVED）、D-040、D-041（APPROVED，H-009）｜驗收測試：TEST-001、TEST-005、TEST-009（model 部分）**

範圍依 tasks.json：

- 開放 route 之前實作 owner 的決定：D-040（IR 存 canonical 文字，以 reviewer 的 R11c probe 先紅後綠）、D-041（ModelVersion 保存 publish 的 comment）；
- 綁定 create／get／draft／patch／publish／list／version／reset 的 route；
- If-Match／ETag／media type／error 與 OpenAPI 的 contract 測試；
- TS client，不重複 IR 型別。

分三段交付：T-013a（9f7294e）、T-013b（7191164）、T-013c／d（8cce958）。逐條輸出在 `checks/evidence/2026-10-19-T-013/`。

## 交付內容

| 位置 | 內容 |
|---|---|
| `migrations/0005_ir_text.*`（D-040） | `model_versions.ir`、`model_drafts.ir` 改為 `text`；具名 CHECK：object、`model_id`（version 另有 `schema_version`）、`hash = 'sha256:' \|\| sha256(ir 的 UTF-8 bytes)`。SQL 無法從 jsonb 產生 canonical 形式，所以有 model 的 DB 會停止（ERRCODE 55000，看 row 時暫時 NO FORCE RLS，同一 transaction 內恢復）；down 回到 0001 的 jsonb 檢查 |
| `internal/models`、`internal/storage/models`（D-040） | 寫入 canonical bytes；讀出不再 canonicalize，只再核對 hash（不符即錯誤，不回傳） |
| `migrations/0006_version_comment.*`、contract（D-041） | `ModelVersion.comment` 必填、可為 null，1–2000 個字元（`char_length`，與 JSON Schema 同樣以字元計）；空字串視為沒有；version 不可修改，所以只在建立時寫入。Service 另拒絕超過 2000 字元、含 U+0000、非 UTF-8 的 comment（`ErrInvalidComment`）；新 negative fixture；TS contract 重新產生 |
| `internal/models/http.go` | 九個 route（`Routes` 與 `planning/api-operations.json` 比對）：path ID 檢查、If-Match 解析、draft 回應的強 ETag、patch body（`application/json-patch+json`、2 MiB、schema 與至少一個 operation）、service 錯誤對應 contract code |
| `internal/httpapi/request.go` | `DecodeMedia`：`DecodeJSON` 的其他 JSON media type 版本 |
| `internal/api/api.go`、`cmd/api/main.go` | `api.Handler` 自己建立 domain pack registry 並回傳錯誤（`cmd/api` 為 exit 1），cmd 與整合測試用同一組 pack；註冊 model routes |
| `apps/web/lib/api/client.ts` | `createModel`、`resetDraft`、`publishVersion`、`listVersions`、`getModelVersion`（T-014 已有讀取與 `patchDraft`）；寫入的 Idempotency-Key 由呼叫者給（`newIdempotencyKey`），送出前檢查格式；型別全部來自產生的 contract types |
| `scripts/run_db_tests.sh`、`scripts/render_db_tests.py`、`db/tests/` | 0005、0006 加入 up／down／up；fixture 改用 canonical 文字；`ir_rows.sql` 給 0005 的拒絕測試 |
| 文件 | ADR-022（D-040、D-041 的實作、HTTP 一節、comment 不再是缺口）、ARCHITECTURE §2、API_DATABASE §9、db/README |

## 實際執行結果

所有指令在 8cce958 的乾淨 worktree 執行（`git worktree`，先 `go clean -testcache`；log 中沒有 cached 結果），DB 為另一個一次性 PostgreSQL 16 cluster 上新建的 `dt_test_t013_final`。Log 在 `checks/evidence/2026-10-19-T-013/final/`，exit code 見 `exit-codes.txt`。CI：8cce958 的 run [39189160860](https://github.com/KCL1104/Geminus/actions/runs/39189160860) 5 個 job 全過（含 PostgreSQL 16 的 `make integration-test` 與截圖比對）；9f7294e 與 7191164 的 run 也都成功。

| 指令 | Exit | 說明 |
|---|---|---|
| `make backend-test`、`backend-race` | 0 | `backend-test.log`、`backend-race.log` |
| `make architecture-check` | 0 | 38 packages、10 rules、16 build configurations（`internal/api` 引入 software delivery pack，規則允許） |
| `make frontend-test` | 0 | lint、typecheck、vitest 14 個檔案 217 個 test（新增 6 個） |
| `make spec-check` | 0 | contracts（含新 negative fixture）、TS contracts 新鮮、DB test 新鮮等 |
| `make integration-test`（新建的 `dt_test_t013_final`） | 0 | migration 0001–0006 up／down／up、0005 拒絕有 model 的 DB、SQL tests、`db-go-test`；唯一的 NOT RUN 是 worker 整合（T-019） |
| `go test -race -p 1 -v`（internal/models、storage/models、api、httpapi、cmd/api） | 0 | `t013-go-tests.log`：63 個 test 通過，0 SKIP、0 FAIL |
| `http-mutants.py`（23 個） | 0 | `http-mutation.log`：23 個全部 killed、0 unexpected |

### 先紅後綠

- **D-040**：`TestStoredIRIsTheCanonicalBytes`（reviewer 的 R11c：4 批共 120 個 sink，每個 2300 個 `1e300`）在 jsonb 上失敗：「the stored draft is 83639428 bytes, its canonical form 1942370」（`red-d040-storage.log`）。0005 之後通過；本機 `internal/models` 的 DB 測試（含此測試）從 162 秒降為 38 秒。
- **D-041**：`TestPublishKeepsTheComment` 在只加欄位、尚未保存時失敗（每個 comment 讀回都是 null；`red-d041-comment.log`，其中三個不合格 comment 的錯誤是測試本身用了過時的 revision，修正測試後才看得出 service 的檢查）；保存後通過。
- **0005 的拒絕**：`run_db_tests.sh` 在 0005 down 之後寫入一個 model，要求 0005 以自己的訊息停止，且欄位仍為 jsonb、FORCE RLS 不變。把 0005 的拒絕區塊拿掉時，這一步回報「migration 0005 stopped for another reason」（hash CHECK 在既有 row 上失敗），所以它分得出兩種情形。
- **HTTP adapter**：本段先寫 handler 再寫 HTTP 測試，因此以 mutation 證明測試擋得下缺陷：`http-mutants.py` 以 `go test -overlay` 套用 23 個 mutant（不修改 repo），全部被擋下（If-Match 的 4 種放寬、ETag 缺漏或錯一號、空 patch、patch 上限降為 1 MiB、patch 當成 `application/json`、錯誤碼對調、issue details 遺失、未對應的 service 錯誤、publish 不要 key、reset 不看 If-Match、version path 收 model ID、route 接錯 handler、route 的 role、未註冊、沒有 pack）。第一次執行時 `patch-limit-1mib` 的寫法讓 import 沒被使用（harness 錯誤，不算 kill），改寫後被 `TestPatchBody` 的 1.5 MiB 案例擋下。

### 測試涵蓋

| 測試 | 驗證 | 需求 |
|---|---|---|
| `TestRoutesMatchContract`（internal/models） | 九個 route 的 method、path、role 與 api-operations.json 相同，且恰為這九個 | D-012、TEST-001 |
| `TestIfMatch` | 13 種 header：缺少 428；未加引號、weak、`0`、前導 0、`*`、list、兩個 header、空、超過 100 字元 400；`int64` 上限可用；超過上限 409 | D-003 |
| `TestRefused` | 每個 service 錯誤的 contract code 與 status；422 帶 issue 的 path、message、code、severity；comment 指向 `/comment`；tenant 與其他錯誤原樣交給 httpapi | ADR-019 |
| `TestModelRoutes`（internal/api，真實 handler、API login、TLS 測試 OIDC） | create（replay 回同一 body）→ get → draft 的 ETag → patch（ETag 2）→ publish 帶 comment（replay 相同、audit 一筆）→ versions → version 讀回 comment → model 的 latest 與 revision → patch → reset（ETag 5、內容回到 latest）→ list models；每個回應符合 schema 並帶 request ID | TEST-001、REQ-004–006 |
| `TestModelRoleMatrix` | 四個讀取 route × viewer 200、另一 organization 的成員 404、以自己 organization 讀別人的 404、未知 identity 403、無 token 401；四個寫入 route 以 viewer 403、以別的 organization 404，且 draft 不變；ID 種類或格式錯 400，格式正確但不存在 404 | TEST-009 |
| `TestDraftPreconditions` | patch、reset、publish 各自：缺 If-Match 428、格式錯 400、過時 409，皆不改 draft；兩個從 revision 1 同時 patch，恰一個 200、一個 409，draft 到 revision 2 | TEST-005、D-003 |
| `TestPatchBody` | `application/json` 415；非 JSON 400；`[]`、`move`、U+0000 為 422 REQUEST_VALIDATION_FAILED（path 指到欄位）；超過 2 MiB 413；1.5 MiB 會被讀入（之後因 name 過長 422 MODEL_VALIDATION_FAILED）；禁止的 path、schema 不合格的結果為 422 MODEL_VALIDATION_FAILED；全部不改 draft | D-029、ADR-019 |
| `TestPublishRefusals` | 無 key 400；clean draft 409 LIFECYCLE_CONFLICT；別的 base 409 MODEL_VERSION_CONFLICT；comment 2001 字元、U+0000、非字串為 422（`/comment`）；同一 key 不同 comment 409 IDEMPOTENCY_KEY_CONFLICT；有 error 的 draft 422 MODEL_VALIDATION_FAILED，details 含 `UNKNOWN_REFERENCE` | D-003、D-011、D-041 |
| `TestModelLists` | version 列表以 `limit=2` 翻頁不重不漏、最新在前；`limit` 0／101、亂寫的 cursor、別的 model 的 cursor 為 400 | API_DATABASE §3 |
| `TestStoredIRIsTheCanonicalBytes`、`TestStoredIRMustMatchItsHash`、`TestNumbersAreStoredAsWritten` | 存的是 hash 涵蓋的 bytes；讀出時 bytes 與 hash 不符即錯誤；`1e300` 照原樣存 | D-040 |
| `TestPublishKeepsTheComment` | 41 與 2000 字元的 comment 在 publish、讀取、列表都保存；空的為 null；三種不合格的被拒 | D-041 |
| `db/tests/constraints.sql` | IR 欄位為 text；version 讀回原 bytes；同一個 JSON 值的不同 bytes、錯的 hash、非 object 為 23514，非 JSON 為 22P02；draft 可換成另一個 canonical IR。Comment：空與 2001 字元 23514，2000 字元（6000 bytes）保存，修改 55000 | D-040、D-041、TEST-004 |
| `apps/web/tests/api.test.ts` | create、publish、reset、讀 version 的 method、path、header 與 body；reset 回應的 ETag 必須相符；錯的 ID 與 key 不送出；`newIdempotencyKey` 符合 server 規則；每個 client operation 的 method 與 path 等於 api-operations.json | TEST-001 |

## 決策與 owner 知悉

- D-040、D-041 依 owner 的決定（H-009）實作，沒有新的待決事項。
- 0005 不轉換既有資料：沒有任何部署，開發用 DB 需重建（db/README）。若日後已有資料，需要另寫以 Go 產生 canonical 文字的轉換程式。
- 讀取多一次 SHA-256（2 MiB 約數毫秒）。

## 與規劃的差異

- tasks.json 的 `internal/model/http/` 不可行：`internal/model` 是 generic kernel，architecture check 禁止它依賴 HTTP（AGENTS.md §2）。HTTP adapter 依 `internal/project` 的模式放在 `internal/models/http.go`；tasks.json 的 planned output 已更正。
- 本任務的 HTTP adapter 先寫實作再寫測試（AGENTS.md §5 要求先寫測試）；以 23 個 mutant 全部被擋下作為測試有效的證據。

## NOT RUN／延後

| 項目 | 原因／歸屬 |
|---|---|
| 綁 dataset 的 version 經 HTTP 的 503 | Dataset storage 尚不存在（T-016）；對應由 `TestRefused` 涵蓋，service 的 fail-closed 由 T-012 的測試涵蓋 |
| 前端編輯器使用新的 client operation | T-015（依 T-045 的 design gate） |
| 瀏覽器的產品閉環 E2E | T-040 |
| 真實 IdP | Auth0 tenant 尚未建立（ADR-018／T-043）；測試用 test OIDC provider |
