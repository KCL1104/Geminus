# T-012 驗證報告：Model／Draft／Version service

**日期：2026-10-18｜狀態：`review`（實作與自我驗證完成，送獨立 review；依 H-003 驗收）｜需求：REQ-005、REQ-006、REQ-009｜Gate：D-003、D-028（皆已批准；另用到已批准的 D-011、D-014、D-020、D-023、D-029）｜實作選擇：ADR-022（PROPOSED）**

逐條輸出在 `checks/evidence/2026-10-18-T-012/`。範圍依 tasks.json：

- 一個 transaction 建立 model、draft 與 v1；
- draft revision CAS；
- publish 的 parent 與 latest；
- immutable version 與 dataset binding；
- clean／dirty draft 規則、明確的 reset、stale 一律拒絕。

HTTP route 與 TS client 屬 T-013。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/models` | Service（`DB`、`Packs`（`PackLookup`，即 `*domainpack.Registry`）、`Datasets`）：`Create`、`GetModel`、`ListModels`、`GetDraft`、`PatchDraft`、`ResetDraft`、`Publish`、`GetVersion`、`ListVersions`；版本推進規則 `advance`（之後的 approval 共用）；回應型別對應 `http.schema.json` 的 Model、Draft、ModelVersion、Models、Versions |
| `internal/storage/models` | models、model_versions（含 binding rows）、model_drafts 的 SQL；每個 statement 帶 organization filter；draft 更新只在 expected revision 時生效 |
| `migrations/0003_draft_issues.*` | `model_drafts.issues`（jsonb 陣列，≤1000，D-028）；SQL test 與 up／down／up runner 已加入 |
| `scripts/check-architecture/rules.go` | `internal/models` 列為 application layer |
| `Makefile` | `db-go-test`（因此 `integration-test` 與 CI db job）跑 `./internal/models/...` |
| 文件 | ADR-022（PROPOSED）；ARCHITECTURE.md package 表；API_DATABASE.md §8；db/README.md |

## 實際執行結果

所有指令都在 0748bc0（`commit.txt`）上執行，exit code 見 `exit-codes.txt`。CI：0748bc0 的 run [39155503173](https://github.com/KCL1104/Geminus/actions/runs/39155503173) 4/4 success（含 PostgreSQL 16 的 `make integration-test`）。DB 測試用一次性的 PostgreSQL 16 cluster 與新建的 `dt_test_t012`，不指向任何使用者環境。

| 指令 | Exit | 說明 |
|---|---|---|
| `make backend-test` | 0 | `backend-test.log`（go vet + go test；DB 測試在沒有 database 時 skip，由下面兩列實際執行） |
| `make backend-race` | 0 | `backend-race.log` |
| `make architecture-check` | 0 | `architecture-check.log`：`PASS architecture: 34 packages, 10 rules, 16 build configurations`（多了 `internal/models` 與 `internal/storage/models`） |
| `make spec-check` | 0 | `spec-check.log` |
| `make integration-test`（新建的 `dt_test_t012`） | 0 | `integration-test.log`：migration 0001–0003 up／down／up、SQL constraint 與 RLS tests、`db-go-test`（含 `./internal/models/...`、`./internal/storage/models/...`）全部 ok。Worker integration 仍為 NOT RUN（T-019） |
| `go test -race -p 1 -v`（models、storage/models，`GEMINUS_REQUIRE_DB_TESTS=1`） | 0 | `t012-go-tests.log`：15 個 test，0 FAIL，0 SKIP |
| mutation（`mutants.py`） | 0 | `mutation.log`：67 個，66 killed，1 equivalent，0 unexpected |

先紅後綠，保存的紀錄：

- `red-sql.log`：migration 0003 之前，draft issues 的 SQL test 因欄位不存在而失敗；`db-test-0003.log` 是加入後的 up／down／up。
- `red-pack-lookup.log`：`Service.Packs` 改為 interface 之前，`TestPackRules`（pack 自己的規則）無法編譯。

### Mutation

第一次全量執行（`mutation-pass1.log`）：67 個，59 killed，8 個 unexpected，全部處理後重跑：

| Mutant | 問題 | 處理 |
|---|---|---|
| `patch-mode-ai` | 沒有 patch 只改 views 的測試（AI 模式禁止 views，手動模式允許） | `TestPatchDraft` 加 `/views/2d/positions/…` 的 patch |
| `publish-no-audit` | 沒有測試檢查 publish 的 audit row | `TestPublish` 以 request ID 找到 `model.publish model_version <v2>` |
| `check-no-pack` | P0 pack 的 `Validate` 只檢查 domain，template 一定通過 | `Service.Packs` 改為 `PackLookup` interface；`TestPackRules` 用名稱為 `Forbidden` 時回 error 的 pack，證明 patch 存入、publish 擋下 |
| `model-never-out-of-date` | `GetModel` 的 draft 摘要沒有 out-of-date 的測試 | `TestDraftWhenAnotherVersionArrives` 檢查 `Draft.OutOfDate` |
| `cursor-not-canonical` | 補空白、仍可解碼的 cursor 沒有測試 | `TestLists` 加這個 cursor |
| `patch-issues-dropped`、`publish-full-manifest` | 無法編譯（未使用的變數），是 harness 錯誤 | 改寫為可編譯的 mutant，皆 killed |
| `patch-stale-not-conflict` | `UpdateDraft` 的 `ErrStale` 對應到 409 無法觸發 | 列為 equivalent：PatchDraft 先鎖 model row，再在同一 transaction 比對 revision；所有 draft 寫入都先取同一把鎖，CAS 不會輸。保留為第二道防線 |

## 驗收對照

| 測試 | 驗證內容 | 對應 |
|---|---|---|
| `TestCreate` | 201 且符合 Model schema；v1 parent null、manifest 空；draft revision 1 與 v1 同 hash；template draft 無 error；audit `model.create`；同 key 同 body 重播位元組相同；同 key 不同 body 或不同 project 為 idempotency conflict；zh-Hant 名稱、ID 不變 | API_DATABASE §4、D-011、ADR-020 |
| `TestRolesAndTenancy` | viewer 不能 create／patch／publish／reset、可讀；其他 organization 的 model、version、draft、project 一律 not found；非成員 `ErrNoMembership` | D-008、TEST-009 |
| `TestPatchDraft` | revision+1、audit；只改 views 的 patch 可以（手動編輯）；stale revision 409；patch 錯誤、禁止路徑、缺 id test、schema 不合格、缺目標、test 失敗、型別錯誤皆 422 且 draft 不變；語意錯誤（刪掉 sink）存入 issues | D-028、D-020 |
| `TestConcurrentPatches` | 8 個寫入同一 revision：恰一個成功，其餘 409，結果是贏家的內容 | TEST-005 |
| `TestPublish` | clean draft 409 LIFECYCLE_CONFLICT；stale revision、錯的 expected base 409；成功：v2 parent v1、latest 與 name 更新、draft 跟到 v2、audit `model.publish`；重播；有 error 的 draft 422 且不建版本 | TEST-004、D-003 |
| `TestPublishRace` | 6 個並行 publish：恰一個成功，其餘 409，共兩個版本 | TEST-005 |
| `TestDraftWhenAnotherVersionArrives` | 另一路徑（approval）產生的版本：clean draft 跟上；dirty draft 原樣保留且 out_of_date（draft 與 model 摘要皆是）；publish 拒絕；reset 需確認、latest 與目前 revision | D-003 |
| `TestVersionsAreImmutable` | API login 不能 UPDATE／DELETE model_versions；parent 不能是別的 model 的版本 | TEST-004 |
| `TestDatasets` | 未綁的 empirical ref 在 issues 並擋 publish；沒有 dataset store 時有 binding 的檢查回 503；snapshot 與凍結的 hash 不符 422；成功時版本只凍結用到的 binding；同一 dataset 之後的新版本不改已發布版本 | TEST-014 |
| `TestLists` | model 與 version 分頁不重不漏、version 由新到舊；cursor 只能用於原本的 list 與 parent，竄改、補字或非 canonical 的 cursor 皆拒絕 | API_DATABASE §3 |
| `TestPackRules` | pack 自己的規則（不只 generic validator）：patch 後 error 存入 issues（code 與 path），publish 回 422 並帶該 issue | D-020、D-028 |
| `storage/models` 測試 | superuser 下 organization filter 本身成立；CAS；manifest JSON 與 binding rows 不一致時 commit 失敗；keyset 同 created_at 不重不漏 | AGENTS.md、API_DATABASE §8 |

## 需 owner 知悉

- ADR-022 為 PROPOSED：
  - service 的位置（`internal/models`，不是規劃的 `internal/model/service/`）；
  - clean／dirty 的定義與 draft 規則；
  - 寫入以 model row 排隊；
  - manifest 只能沿用 base；
  - clean draft 不能 publish。
- Publish request 的 `comment` 沒有地方存：contract 沒有欄位，audit 不放使用者文字。目前不保存，T-013 決定是否加入 ModelVersion。
- Dataset snapshot 的讀取需要 object storage（T-016）。在那之前，用到 binding 的 publish 回 503，不假裝驗證過。

## 與規劃的差異

- `internal/model/service/` 改為 `internal/models`：前者在 generic kernel 內，architecture check 不允許依賴 storage 與 tenant。
- `internal/storage/model_repository.go` 依既有慣例改為 `internal/storage/models/` package。
- 新增 migration 0003：Draft 的 `issues` 原本沒有欄位。

## NOT RUN／延後

| 項目 | 原因／歸屬 |
|---|---|
| HTTP routes、If-Match／ETag、TS client | T-013 |
| Approval 產生版本 | T-026（`advance` 已共用並有測試替身） |
| Run 綁定版本、no draft runs | T-023 |
| 真實 dataset object storage | T-016（測試以記憶體 store 代替） |
