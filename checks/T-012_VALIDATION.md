# T-012 驗證報告：Model／Draft／Version service

**日期：2026-10-18｜狀態：`done`（首輪獨立 review 為 CHANGES REQUESTED，先紅後綠修正後，同一 reviewer 複核 ACCEPT；複核的 low 發現已處理；依 H-003 驗收）｜需求：REQ-005、REQ-006、REQ-009｜Gate：D-003、D-028（皆已批准；另用到已批准的 D-011、D-014、D-020、D-023、D-029）｜實作選擇：ADR-022（PROPOSED）**

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

## 首版的實際執行結果（0748bc0，review 前）

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
| `TestDatasets` | 未綁的 empirical ref 在 issues 並擋 publish；manifest 非空而沒有 dataset store 時回 503（用不用到都是）；snapshot 與凍結的 hash 不符 422；版本凍結 base 的全部 binding（review M3）；同一 dataset 之後的新版本不改已發布版本 | TEST-014、TWIN_IR_SPEC §5.2 |
| `TestLists` | model 與 version 分頁不重不漏、version 由新到舊；cursor 只能用於原本的 list 與 parent，竄改、補字或非 canonical 的 cursor 皆拒絕 | API_DATABASE §3 |
| `TestPackRules` | pack 自己的規則（不只 generic validator）：patch 後 error 存入 issues（code 與 path），publish 回 422 並帶該 issue | D-020、D-028 |
| `storage/models` 測試 | superuser 下 organization filter 本身成立；CAS；manifest JSON 與 binding rows 不一致時 commit 失敗；keyset 同 created_at 不重不漏 | AGENTS.md、API_DATABASE §8 |

## 獨立 review（2026-10-18，H-003）：CHANGES REQUESTED 與修正

Reviewer 以自己的 `git archive d04cc31` export 與新建的 disposable DB 執行，沒有使用工作樹。Tenancy、role、CAS、parent／latest、immutability、idempotency、audit、D-003 的 dirty draft 規則都經得起它的 probe；integration-test、15 個 DB test 重現。它的 mutation 重跑得到 65 killed、1 equivalent、1 unexpected（`sql-lock-no-lock` 只靠 race 測試的時序，負載下 7 次中 1 次存活），不是報告的 0 unexpected。發現 4 個 medium、9 個 low、9 個 info。

修正前的紅燈在 `checks/evidence/2026-10-18-T-012/review1/red.log`：以舊程式執行新測試，10 個失敗；`TestListLimits` 的 panic 中止了其後的測試。修正後 `green.log`（race、`GEMINUS_REQUIRE_DB_TESTS=1`）：41 個 test 全過、0 SKIP。

| # | 發現 | 修正 |
|---|---|---|
| M1 | Publish 與 reset 驗證的是 jsonb 印出的文字（`, `、`: `、數字印成完整十進位）：68 KB 的 draft 讀回 2.8 MB，因 2 MiB 上限無法 publish，回未分類的 500 | Patch、publish、版本推進都先取 canonical 形式再驗證；version 存 canonical 形式。`TestStoredTextIsNotWhatIsChecked`：jsonb 文字超過 2 MiB 的 draft 可 publish，另一路徑的同內容版本可 reset |
| M2 | 超過 1000 個語意 issue 的 schema-valid patch 撞上 migration 0003 的上限，回未分類的 500，違反 D-028 | 排序後保留前 999 個，最前面加 path `""` 的 `LIMIT_EXCEEDED` 說明省略幾個；被省略的有 error 時它也是 error。`TestMoreIssuesThanADraftHolds`、`TestCappedBoundary`（1000／1001 邊界） |
| M3 | Version 只凍結用到的 binding，與 TWIN_IR_SPEC §5.2「未使用的 binding 仍會被驗證並凍結」矛盾且未記錄；暫時不用的資料之後無法再用 | 依 §5.2：base 的全部 binding 都驗證並凍結。`TestPublishKeepsUnusedBindings`、`TestDatasets` |
| M4 | jsonb 讀取放大：1.94 MB canonical 的 draft 是 83.6 MB 文字，GetDraft 3.9 秒 | 需要決定儲存形式：記入 ADR-022「待 owner 決定」（建議 IR 欄位改存 canonical 文字），列為 T-013 開放 route 之前的工作（tasks.json） |
| L1 | 讀取分成多個 READ COMMITTED statement，只有手動編輯的 draft 被報成 out_of_date（981 次中 9 次） | 讀取改在 REPEATABLE READ 唯讀 transaction（`tenant.ReadSnapshot`）。`TestReadsSeeOneSnapshot` |
| L2 | Dataset issue 的 path 索引的是篩過的 manifest，不是 base version 的 | 隨 M3 一起修正；ADR-022 說明 path 指向 base version 的 `dataset_bindings`。`TestDatasetIssuePaths` |
| L3 | `advance` 以 latest 而不是 draft 自己的 base 判斷 clean | 改用 draft 自己的 base。`TestCleanAgainstItsOwnBase` |
| L4 | Create 不檢查 template 是否有效 | Schema 或語意／pack error 為 internal error，不建立任何東西；warning 隨 draft 保存。`TestCreateChecksTheTemplate` |
| L5 | Patch 值中的 U+0000 到達資料庫（22P05，500） | 回 422 `IR_SCHEMA_INVALID`，path 指向該字串或 member name。`TestNULIsRefused` |
| L6 | `ListModels`／`ListVersions` 在 limit 0 時 panic | 1–100 以外回 `ErrInvalidLimit`。`TestListLimits` |
| L7 | 鎖只靠時序驗證；publish 指紋含 If-Match、replay 前的 role 檢查、cursor 的 organization 與 ID、cursor 分支的 organization filter、reset 重算 issue、排序、Create 的語意 issue 都沒有測試 | `TestWritesWaitForTheModelRow`（持有 model row 時 patch 與 publish 等待）、`TestPublishIdempotencyScope`、`TestCursorBindings`、storage `TestOrganizationFilter` 的 cursor 分支、`TestIssueOrderAndStages`、`TestCreateChecksTheTemplate`；reviewer 的 11 個額外 mutant 併入 harness |
| L8 | tasks.json 的 verification command 與 planned outputs 仍是舊路徑 | 改為 `./internal/models/...`、`./internal/storage/models/...` |
| L9 | Publish 的 `comment` 納入 idempotency hash 但不保存 | 記入 ADR-022「待 owner 決定」，T-013 處理 |
| I1 | Pack 的 `Validate` 在 generic 有 error 時仍執行，違反 D-023 的 stage 停止規則 | 有 error 就不跑 pack。`TestIssueOrderAndStages` |
| I2 | 未確認的 reset 回 `IR_SCHEMA_INVALID` | 回 `ErrNotConfirmed`（HTTP contract 本身要求 `confirm_discard: true`） |
| I3–I9 | 規則改變後 issue 過時（publish 會重驗）；Model 列表的 N+1；revision 與 latest 只由 service 保證；key 過期後重建；cached test 結果；ADR 的 503 措辭；AGENTS.md §3 仍寫 `internal/model/` | I4、I5、I6 記入 ADR-022 Consequences；I7 本次執行前清除 test cache；I8 隨 M3 改正；I3 不需要改；I9 為 owner 的檔案，列入需 owner 知悉 |

Mutation（`review1/mutation-full.log`、`mutation-rerun.log`）：harness 增加到 94 個（新程式的規則與 reviewer 的 11 個）。全量執行途中磁碟用盡（三個 T-045 spike 的 node_modules 與 Go build cache），14 個 mutant 成為 BUILD-FAILED（harness 錯誤，不算 kill），清出空間後以 `--only` 重跑這 14 個與新增邊界測試後的 `capped-off-by-one`。合計：94 個，92 killed，2 equivalent，0 unexpected。

- `patch-stale-not-conflict`（同首版）：所有 draft 寫入先鎖 model row，revision 在鎖之後比對，CAS 不會輸；reviewer 以略過鎖的寫入者證實只在那種情況可觸發，保留為第二道防線。
- `patch-base-stored-text`（新）：`patch.Apply` 會解碼 base 並重新編碼 candidate，base 的文字形式不進入任何檢查。
- `reads-not-one-snapshot` 由 `TestReadsSeeOneSnapshot` 擋下：讀到不一致是機率事件（修正前 10 次以上），這個 kill 依賴時序。其餘 kill 是確定性的；`sql-lock-no-lock` 現在由 `TestWritesWaitForTheModelRow` 確定性地擋下。

### 修正後（f4fde3a，`review1/`）

在 f4fde3a 的乾淨 worktree（`git worktree`，不含工作樹中其他任務的檔案）上執行，先 `go clean -testcache`，log 中沒有 cached 結果。

| 指令 | Exit | 說明 |
|---|---|---|
| `make backend-test`、`backend-race` | 0 | `backend-test.log`、`backend-race.log` |
| `make architecture-check` | 0 | `PASS architecture: 34 packages, 10 rules, 16 build configurations` |
| `make spec-check` | 0 | `spec-check.log` |
| `make integration-test`（新建的 `dt_test_t012`） | 0 | `integration-test.log` |
| `go test -race -p 1 -v`（models、storage/models、tenant） | 0 | `green.log`：41 個 test，0 SKIP |
| mutation | 0 | 94 個：92 killed、2 equivalent、0 unexpected |

## 複核（review round 2，2026-10-18，H-003）：ACCEPT

同一 reviewer 以 `git archive 3950245` 的 export（程式為 f4fde3a）與新建的 `dt_test_t012_review_r2*` 複核，結論 **ACCEPT**：首輪 medium 與 low 全部已修，經它的 probe 驗證（R1–R17 重跑全過；另 9 個新 probe S1–S9 除 S1 外全過）；M4 與 L9 記為 owner 決定可接受。它重現：integration-test、43 個 DB test（race、0 SKIP）、backend-test／race（0 cached）、architecture、spec-check 皆 exit 0；mutation 94 個 92 killed＋2 equivalent＋0 unexpected，兩個 equivalent 屬實。另提出 4 個 low、5 個 info，依過去做法在 ACCEPT 後處理，證據在 `checks/evidence/2026-10-18-T-012/review2/`（`red.log` 為修正前的新測試、`green.log` 為修正後）：

| # | 發現 | 處理 |
|---|---|---|
| N1 | PatchDraft 的 422（patch 本身與 schema 的 issue）沒有封頂：996 個不合格 resource 產生 1992 個 issue，超過 `Error.details` 的 1000 | 兩者也經 `capped`（排序後保留 999 個加 `LIMIT_EXCEEDED`）；patch 本身可產生超過 1000 個 issue（1100 個 `move` op），也測到。`TestMoreIssuesThanADraftHolds` |
| N2 | `tenant.ReadSnapshot` 沒有確定性的測試 | `internal/tenant` 的 `TestReadSnapshot`：另一個 transaction 在兩個 statement 之間 commit 的 row，ReadSnapshot 看不到、ReadOnly 看得到；ReadSnapshot 不能寫入。對應 mutant 併入 harness |
| N3 | M4 只記在 ADR-022 與 T-013 scope，沒有登記為 decision gate | 新增 D-040（IR 儲存形式）、D-041（publish comment），皆 PROPOSED，列為 T-013 的 decision gate：`checks/decisions-check.json` 顯示 T-013 等待這兩個決定。ADR-022 補上寫入成本（GetDraft 3.5 秒、patch 4.9 秒、publish 5.8 秒）與 run／worker 也受影響 |
| N4 | 首輪報告的 `review1/green.log` 與 `mutation-full.log` 早於最後加入的測試 | 本節的 `green.log` 在最終程式上執行；mutation 的最終數字見下 |
| I1 | tasks.json 的 summary 仍是首版內容 | 驗收時更新 |
| I2 | NUL 在 member name 時，pointer 本身含 U+0000，與 httpapi 指向父物件不一致 | 改為指向父物件，與 `httpapi.DecodeJSON` 相同。`TestNULIsRefused` |
| I3 | 全 manifest 規則讓有 binding 的 model 每次寫入都重讀所有 snapshot | 記給 T-016：依 content hash 快取 |
| I4 | `advance` 為了比對 hash 讀整個 version（含 IR） | 屬 D-040 的成本；決定儲存形式時一併改為只讀 hash |
| I5 | Service 層的 Create 名稱含 NUL 回 22021 | HTTP 層的 `DecodeJSON` 已擋（T-009 L1）；T-013 的 route 測試涵蓋 |

複核後的結果：

| 指令 | Exit | 說明 |
|---|---|---|
| `go test -race -p 1 -v`（models、storage/models、tenant） | 0 | `review2/green.log`：44 個 test，0 SKIP |
| CI | — | d83a1af 的 run [39164574740](https://github.com/KCL1104/Geminus/actions/runs/39164574740) 4/4 success |
| mutation（新增與改動的 mutant） | 0 | `review2/mutation.log`：新增的 5 個（patch 與 schema 422 的封頂、member name 的 pointer、ReadSnapshot 的隔離等級與唯讀）與 pattern 改動的 4 個全部 killed。Harness 合計 99 個：97 killed、2 equivalent、0 unexpected |

## 需 owner 知悉

- **D-040、D-041（PROPOSED，T-013 的 decision gate）**：IR 儲存形式（review M4，建議改存 canonical 文字）與 publish `comment` 的保存或移除（L9），見 ADR-022「待 owner 決定」。T-013 在 owner 決定前不能開始。
- AGENTS.md §3 仍把 `internal/model/` 寫成 commit service 的位置（review I9）；那是 owner 維護的檔案，建議改成 `internal/models/`。
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
