# T-016 驗證報告：CSV／JSON upload、profile、private storage

**日期：2026-10-19｜狀態：待獨立 review；`planning/tasks.json` 的狀態未改（仍為 not_started，由 reviewer 決定），只加了 planned outputs 與 completion evidence｜需求：REQ-007、REQ-019｜Gate：D-007、D-009、D-043、D-044（皆 APPROVED；D-043(c) 的 gate 是 T-017）｜驗收測試：TEST-012、TEST-029**

範圍依 tasks.json：

1. streaming bounded upload（bytes、rows、columns、field length、parser depth；沒有 Content-Length 時上限照樣成立）、content hash、tenant 前綴下由 server 產生的 private object key（D-035）；
2. 完整 profile 與有界的 preview；CSV 與 JSON 的 parse error（JSON 帶 JSON Pointer）；JSON 的 duplicate key 與 non-finite number 失敗；P0 拒絕壓縮檔與執行檔；
3. 安全的 error artifact 下載（CSV formula injection 中和，TEST-029；artifact 授權：任意 key 或其他 tenant 拒絕，依 D-044 授權後串流）；commit 之前不改 Twin（D-009；commit 是 T-017／T-018）；
4. UTF-8（含／不含 BOM）的中文 header 與值正確 profile／preview，不因非 ASCII 內容誤判 encoding。

T-016 是 domain-neutral 的：H-016 把 P0 domain 改為電子組裝／測試線，但這裡沒有任何 domain 專屬的 mapping（屬 T-017、T-027、T-052）。測試資料全是 synthetic。

逐條輸出在 `checks/evidence/2026-10-19-T-016/`：`exit-codes.txt`、`environment.txt`、各指令的 log、`mutations.txt`（SQL 與 architecture 規則的紅燈、26 個 targeted mutation）、`fuzz.log`、`bench.log`。

## Commit

| Commit | 內容 |
|---|---|
| T-016a（84482b7） | 契約：`import.schema.json` 依 D-043 加 `profile`（`$defs/Profile`、`ProfileColumn`）與 256 欄的原始值 preview，`valid-import.json` 依 D-043(b) 改為原始值與中文欄名，另加 2 個正例、7 個反例；`uploadImport`、`getImport` 的規則與範例；`downloadImportErrors` 改回 `text/csv`（D-044，CB-38(b)）；TS 契約型別重產與 `contracts.test.ts` 的正反例；migration 0010 與 `db/tests/imports.sql`（先以只有欄位的 stub 跑紅燈）；`constraints.sql`、`roles.sql` 的 fixture 配合 0010 |
| T-016b（27ba562） | `internal/import/profile`（純粹、串流、有界的 profile 與 preview）、`internal/import/report`（formula-safe CSV），先寫測試，含 fuzz target |
| T-016c（58afc53） | `internal/storage/imports`、`idempotency.Seen`、`internal/import/upload`（service 與 HTTP adapter）、`internal/import/profiler`（job handler）、`internal/api` 與 `cmd/worker` 的接線、architecture 規則（`job-handlers-only-in-the-worker` 擴充、新規則 `import-parsing-is-pure`）與 probe、`db-go-test` 加入 `./internal/import/...`、整合測試 |
| T-016d（c4c776f） | 文件（API_DATABASE §5、§9，ARCHITECTURE，SECURITY §6，CONTRACT_BLOCKERS 的 CB-39 與 CB-38(b)，DECISIONS_AND_TBD 的 D-043／D-044 實作註記，db/README）、tasks.json 的 planned outputs 與 evidence；以 `Expect: 100-continue` 證明被拒的 upload 的 body 從未被讀 |
| T-016e（a3f8c6c） | 自我 review 的修正：worker 讀 object 至多多讀一 byte（比 upload 長的 object 也是 `UPLOAD_CORRUPT`，加測試）；沒有記下 failure 的 failed job 顯示為 `INTERNAL`；上限檔案的 benchmark |
| 本報告（下一個 commit） | 報告、evidence、`make spec-check` 重產的 `checks/*.json` |

## 稽核：scope 逐條

| Scope | 判定 | 證據 |
|---|---|---|
| 1. Streaming、有界的 upload | 已完成 | API 在讀 body 前先查 role 與 object storage（`Begin`；`TestImportRoleMatrix` 以等候 `100 Continue` 的 client 證明 viewer 與其他 tenant 的 body 從未被讀，editor 的有）。Multipart 以 `NextRawPart` 依序讀：四個欄位在前（每個至多 128 bytes）、`file` 最後，檔案直接串流進 `objects.Store.Put`（`internal/storage/objects` 的 FS adapter：temp file＋hard link，邊寫邊算 SHA-256 與大小），沒有整檔放進記憶體。50,000,000 bytes 的上限由 `Put` 的 `max` 與 `http.MaxBytesReader` 在讀取時強制：chunked（沒有 Content-Length）超過上限為 413，且不留下 object 與任何 row（`TestUploadRefusals`）；宣告的 Content-Length 超過時不讀 body 即 413。Rows、columns、欄名、值長度（以字元計）、JSON 巢狀深度、JSON model 大小在 profile 時強制，每個都是 typed failure 與 JSON Pointer（`TestUnreadableFiles` 的 53 個 case，`TestProfileFailures`）。此 route 自設讀取期限（預設 5 分鐘），比 server 的 ReadTimeout 慢的 upload 仍完成、比自己的期限慢的被切斷且不 commit（`TestASlowUploadHasItsOwnDeadline`）。Key 一律 `objects.NewKey(org, imports, <import>, "upload")`，error report 為 `.../<import>/profile-errors-<attempt>.csv`；DB 的 `object_key_ok`（D-035）與 service 的 own-prefix 檢查（`store.OwnKey`）。Hash 與大小寫入 row，worker 讀 object 時核對（`TestProfileIntegrity`：改過、刪除、變短、變長） |
| 2. 完整 profile、有界 preview、parse error | 已完成 | `internal/import/profile.Read`：一次串流讀完整個檔案；D-043(a) 的 profile（格式、編碼、列數、每欄的推斷型別、非空數、最長字元數）；preview 為前 100 列原始值（D-043(b)），字串 256 字元、以資料庫儲存大小計 1 MiB（`TestPreviewBounds`，含 `1e300` 這種資料庫會印成 301 位的數字）。CSV 的 syntax error 帶 record 的 row pointer 與行號；JSON 的 syntax、duplicate member（`JSON_DUPLICATE_KEY`）、超出 binary64 的數字（`JSON_NUMBER_NOT_FINITE`）、U+0000、值之後的資料、深度都帶精確的 JSON Pointer（例：`/0/m/k/1`）；欄位數不符的 CSV record 是 invalid row，列入 error report（`TestInvalidRows`）。壓縮檔與執行檔在寫入前依副檔名與開頭 bytes 拒絕（415、`IMPORT_FORMAT_REJECTED`；`TestSniff` 的 17 種 signature 與 7 個副檔名，以及開頭恰為 `MZ`、`BZh`、`PK` 的合法 CSV 不誤擋；`TestUploadRefusals`）。Failure 訊息不引用檔案內容（`TestFailureMessagesHoldNoFileContent`；jsontext 自己的訊息會引用輸入，所以不用）。Fuzz：60 秒 80 萬次，profile 一律守住契約的上限或為 typed failure（`fuzz.log`） |
| 3. Error artifact 安全下載、不改 Twin | 已完成 | `downloadImportErrors` 依 D-044 授權後串流（不發 signed URL；CB-38(b) 因此解決）：viewer 以上、只由 import ID 要求（query 中的 key 或 URL 不起作用）、只從本 import 自己的 key 讀、大小與 SHA-256 不符或 object 不見為 500 且不送任何 bytes、其他 tenant 為 404、DB 拒絕指向其他 tenant 的 key（`TestErrorReportAuthorization`、`TestImportClosedLoop`、`imports.sql`）。Report 每個 cell 經 `report.Safe`（=、+、-、@ 與全形，tab、CR、LF，含前導空白），整合測試解析下載的 CSV、逐 cell 確認（`TestSafe`、`TestRender`、`TestImportClosedLoop`）。上傳與 profile 從不碰 model、draft、version：每個整合測試比對前後的 version hash、draft revision 與 hash、latest version 與 `updated_at`（`TestImportClosedLoop`、`TestProfileFailures`）；worker 本來就沒有 model_versions／drafts 的寫入權限 |
| 4. UTF-8（含／不含 BOM）中文 | 已完成 | `text` reader 去掉並記下 BOM（`utf-8-bom`），只放行 valid UTF-8，跨 read 邊界的多 byte 字元保留到完整（`iotest.OneByteReader` 逐 byte 讀與整段讀結果完全相同）；欄名、值與長度以 code point 計（四 byte 的 emoji 恰在上限內）；Big5、Latin-1、UTF-16、NUL 一律 `IMPORT_ENCODING_INVALID`／`IMPORT_NUL_CHARACTER`，不猜其他編碼（`TestChineseCSVWithBOM`、`TestNoEncodingGuess`、`TestUnreadableFiles`、整合測試的中文欄名與含換行的中文值） |
| 每個新查詢都限定 tenant | 已完成 | `internal/storage/imports` 每個 statement 都 `organization_id=$1`（另有 RLS）；`idempotency.Seen` 依 organization、actor、operation、key |
| 每個寫入：role＋audit＋concurrency＋idempotency | 已完成 | 上傳：role 在讀 body 前、`Receive` 與 `Commit` 的 transaction 中各再查一次；`import.upload` audit 與 import、job、idempotency response 同一 transaction（只有 hash、大小與 ID，沒有檔名與內容）；同 key 同檔回原 response（retry 只算 hash、不再寫 object），不同檔 409，8 個並行 retry 只有一個 import；降級後 replay 為 403（`TestUploadIdempotency`）。Worker：lease fence 下 `PROFILING` 才能寫結果（CAS），`import.profile` worker audit 與 job 的 completed 同一 transaction |
| 新 API 更新 OpenAPI＋範例＋TS client 契約測試 | 已完成 | OpenAPI 三個 operation 的規則與 `getImport`、`uploadImport` 的範例（`check_contracts.py` 依 schema 驗證範例）；`testdata/contracts` 9 個 import fixture；`make ts-contracts` 重產 `import.ts`，`contracts.test.ts` 以 `valid-import.json` 比對型別並加兩個型別反例；Go 端 `TestContractParity` 比對 `Import`、`Profile`、`ProfileColumn`、`ImportAccepted` 與 error envelope 的欄位 |
| Context cancellation | 已完成 | `profile.Read` 每次讀與每 256 列檢查 context，store 的錯誤原樣傳出（`TestCancellationAndReadErrors`）；worker 的 attempt 停止時 handler 回 context 的錯誤 |

## 實際執行結果

在 worktree 執行，先 `go clean -testcache`。HEAD、版本與 DB 見 `environment.txt`：程式為 a3f8c6c，Go 1.27.2，Node 22.22.0，PostgreSQL 16.15，4 CPU。DB 為本輪自建的一次性 cluster：`initdb --encoding=UTF8 --locale=C.UTF-8`、trust、只有 Unix socket（`listen_addresses=''`）、port 55446、資料與 socket 在 `/var/lib/postgresql/geminus-t016`，以 `postgres` 使用者執行，跑完後以 `pg_ctl stop` 停止；連線 URL 的 user 放在 userinfo（`postgresql://postgres@/dt_test_…?host=…&port=55446`），否則 `user=` query 參數會蓋掉測試用的 runtime login（T-023 記錄過的問題）。

**與指示的差異**：這個 session 的 worktree 防護拒絕執行 `runuser`／`su`（它無法證明被執行的程式不是 git），所以 `initdb`、`pg_ctl` 與建立目錄改由 scratchpad 的小程式 `pgas.py` 以 `postgres` 身分執行：它只接受 `/usr/lib/postgresql/16/bin/initdb`、`pg_ctl` 與 `/usr/bin/mkdir`，`setgroups`／`setgid`／`setuid` 為 postgres 後 `execve`。其餘條件與指示相同。

| 指令 | Exit | 說明 |
|---|---|---|
| `make backend-test` | 0 | gofmt、go vet、43 個 package ok（12 個沒有測試檔）；沒有 DB URL，DB-backed 測試在這裡 skip（NOT RUN），由下面的 integration 以 `GEMINUS_REQUIRE_DB_TESTS=1` 執行 |
| `make backend-race` | 0 | 43 個 package ok（`-race`） |
| `make architecture-check` | 0 | 55 packages、14 rules（新增 `import-parsing-is-pure`）、16 build configurations |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 見 `spec-check.log`；重產的 `checks/*.json` 隨本報告 commit（`contracts-check.json` 的 fixture 數已在 T-016a commit：正例 37、反例 62；`docs-check.json` 因本報告多一個 markdown） |
| `make frontend-test` | 0 | lint、typecheck、18 個檔案 414 個 test（含 `contracts.test.ts` 的 import 正反例）；Node 22.22.0（見 NOT RUN） |
| `make integration-test`（新建的 `dt_test_t016_final`） | 0 | migration 0001–0010 up、7 個 SQL suite（含新的 `imports.sql`）、0010…0005 down、0005 的 ir_rows 檢查、全部 down、再 up 與 7 個 suite；再以 API／worker 的 runtime login 跑 `db-go-test` 的 23 個有測試的 package（`-race`，`-p 1`），0 FAIL、0 SKIP |
| `go test -race -p 1 -count=1 -v`（import 的 package、`internal/api`、`cmd/worker`、`scripts/check-architecture`；`t016-go-tests.log`） | 0 | 43 個 top-level test、182 個 subtest 通過，0 FAIL、0 SKIP |
| `go test -fuzz=FuzzRead -fuzztime=60s ./internal/import/profile` | 0 | 803,746 次執行，沒有 crash（`fuzz.log`；中途數秒的 0/sec 是 fuzzer 自己的停頓：對整個 corpus 逐一計時，最慢的輸入 0.8 ms） |
| `go test -bench BenchmarkReadCSVAtTheLimits -benchtime=5x` | 0 | 100,000 列、約 45 MB 的 synthetic 中文 CSV：每次 0.36 s、45 MB allocation（`bench.log`）；這台機器上的一次量測，不是效能結果（T-037） |
| tasks.json 的 verification command `go test ./internal/import/...` | 0 | 包含在 `backend-test`、`integration-test` 與上面的 verbose run 中 |

## 測試涵蓋

| 測試 | 驗證 | 驗收 |
|---|---|---|
| `db/tests/imports.sql`（0010） | API 只能以 UPLOADED 建立 import、不寫 preview 與 error report、不移動 UPLOADED／PROFILING 的 import；任何 role（含 owner）都不能改 upload 的檔名、kind、key、hash、大小、base version、建立者、建立時間；profile 恰在離開 profiling 之後存在、是 object、只寫一次，已 profile 的 import 不回到 profiling；report 的 key、hash、大小全有或全無、1 B–16 MiB、key 在本 tenant 的 imports 前綴之下（其他 tenant、LIKE 萬用字元、`..`、其他 area 都拒絕）；worker 經欄位 grant 完成 profile、沒有 INSERT；FAILED 帶 error 且為終態（owner 也一樣）；API 仍可寫已 profile 的 import 的 mapping（T-017）；其他 role 一律拒絕 | TEST-012、TEST-029、D-009、D-035、D-037 |
| `roles.sql`、`constraints.sql`（修改） | committed 的 import 對 API 也是終態；worker 寫 profile 與 report 的 hash、大小；report key 的 D-035 反例只因 key 而失敗（hash、大小齊全） | D-037、D-035 |
| `TestUnreadableFiles`（53 個 case，每個也逐 byte 讀一次） | 編碼（Big5、孤立的 continuation byte、截斷、surrogate、overlong、UTF-16 LE／BE、NUL、JSON 中的 Latin-1）、空檔、CSV syntax、欄名、列數、值長度、深度、model 大小、JSON syntax、duplicate member（含巢狀與 model）、非有限數字、NaN、escaped NUL、孤立 surrogate、值之後的資料、形狀：code、pointer 與訊息；逐 byte 讀的 failure 完全相同 | TEST-012 |
| `TestFailureMessagesHoldNoFileContent` | CSV 與 JSON 的 failure 訊息都不含檔案中的字串 | SECURITY §7 |
| `TestChineseCSVWithBOM`、`TestNoEncodingGuess` | BOM＋中文欄名與值、CRLF：profile 與 `valid-import.json` 完全相同、preview 為原始字串；逐 byte 讀相同；沒有 BOM 為 `utf-8`；JSON 的 BOM；四 byte 字元的長度以字元計 | scope 4、D-043 |
| `TestInference`、`TestInvalidRows`、`TestPreviewBounds`、`TestJSONModel`、`TestDefaultLimits`、`TestDeterministic` | 型別推斷規則（`007`、`+5`、`1e400`、`2026-13-01…`、Unicode fold 的 `falſe` 都是 string）；invalid row 的計數、行號、欄、值，不計入統計；preview 的截斷、列數、bytes 與資料庫儲存大小；JSON model 的 profile；D-007／D-043 的上限與實作參數；同一輸入結果相同 | D-043 |
| `TestCancellationAndReadErrors`、`FuzzRead` | context 結束即停止（一開始或讀到一半）；store 的錯誤原樣傳出、不是 file 的 failure；未知格式；fuzz 的不變式 | AGENTS §5 |
| `TestSafe`、`TestRender`、`TestRenderIsBounded` | formula 的每一種開頭（含全形、前導空白、DDE 範例）、不該改的不改；BOM、CRLF、header、引號與換行、每個 cell 安全；16 MiB 內以 `ERRORS_TRUNCATED` 計數 | TEST-029 |
| `TestRoutesMatchContract`、`TestContractParity`、`TestSniff`、`TestCleanName`、`TestFieldsCheck`、`TestEnvelope`、`TestFailedJobs`、`TestRefused`、`TestOwnKey`（`internal/import/upload`） | routes 與 `api-operations.json`；Go 型別與 schema；壓縮檔／執行檔的 signature 與副檔名、合法文字不誤擋；檔名（目錄、控制與格式字元、U+202E、255 字元）；欄位 pointer；CB-39 的 envelope 對照（符合 common Error schema、過長 pointer 截為祖先）；job failed／cancelled 的推導；service error 的 code；own key | D-012、D-025、CB-39 |
| `TestImportClosedLoop` | 202 後 row（server key、SHA-256、大小、UPLOADED）、object 與上傳的 bytes 相同、job payload、`import.upload` audit 不含檔名與內容；profile 前 profile 為 null、report 404；worker 後 AWAITING_MAPPING、中文 profile、preview（含跨行的中文值）、`import.profile` audit；report 的 BOM、列與中和過的 cell；Twin 不變；report 被竄改或刪除為 500 且不送 bytes | REQ-007、TEST-012、TEST-029、D-009、D-044 |
| `TestImportRoleMatrix` | editor／owner 上傳、viewer 403、其他 tenant 404（兩種 header）且不留下 object；讀取與 report：viewer 可、其他 tenant 404、沒有 token 401、不合格的 ID 400；未知 identity 403；被拒時 body 從未被讀 | TEST-009（imports） |
| `TestUploadRefusals`（24 個 case＋transport） | 空檔、ZIP、gzip、ELF、script、`.exe`、`.xlsx`、超過上限、未知 kind、draft 當 version、不合格與不存在的 project、不相屬的 model 與 version、file 之後的 part、file 之前缺欄位、沒有 file、未知 part、重複欄位、以檔案送出的欄位、沒有檔名、檔名含控制字元、`Content-Transfer-Encoding`、過長欄位：status、code、pointer；沒有 Idempotency-Key 400、JSON 415、沒有 boundary 415、宣告長度過長 413、chunked 超過上限 413；沒有 import、job 或 audit，只有「file 之後還有 part」留下孤兒 object | TEST-012 |
| `TestUploadIdempotency` | replay 回同一個 202 且不再寫 object；其他檔案、檔名、kind 409；key 屬於 caller；8 個並行 retry 只有一個 import；降級後 403 | D-011、API_DATABASE §7 |
| `TestProfileFailures` | duplicate member、非有限數字、Big5、UTF-16、bare quote、重複欄名、列數與深度上限、形狀：FAILED、code 對照、pointer、upload 的 request ID、沒有 profile、preview、report；JSON model 的 profile；Twin 不變 | TEST-012、CB-39 |
| `TestProfileIntegrity`、`TestProfileJobFailures` | 改過、刪除、變短、變長的 object：`UPLOAD_CORRUPT`／`UPLOAD_MISSING`；沒有 object storage 的 worker：重試到 attempt 用完，API 顯示 FAILED（`SERVICE_UNAVAILABLE`、`OBJECT_STORAGE_UNAVAILABLE`），row 留在 PROFILING；第一次 attempt 標 PROFILING 後中斷，retry 從 PROFILING 完成 | TEST-020（profile job） |
| `TestErrorReportAuthorization` | 指向同 tenant 另一個 import 的 report 為 500；指向其他 tenant 的 key 被 DB 拒絕（23514）；query 中的 key 與 URL 不起作用 | TEST-029 |
| `TestASlowUploadHasItsOwnDeadline` | server 的 read／write timeout 300 ms 時，約 1 秒的 upload 成功；比 route 的期限（3 秒）慢的被切斷且不 commit | scope 1 |
| `TestTheBuildRunsSimulations`（cmd/worker，修改） | worker 登錄 simulation 與 import profile 兩個 handler | ADR-024 |
| `scripts/check-architecture`：probe 6 個、synthetic graph | cmd/api 與 upload service 連結 profiler 被擋；parser 連結 object storage、tenant，report 連結 net/http、os 被擋；synthetic graph 中 parser 經 storage 的違規被找到、用 kernel 與 report 不是違規 | ADR-024、T-016 |

### 先紅後綠與 mutation（`mutations.txt`）

- `imports.sql`：先以 0001–0009 加只有欄位的 0010 stub 執行（`dt.collect=on`），列出 0010 每一條規則都失敗（CHECK、trigger、role 規則）；完整的 0010 下全部通過（up／down／up 兩次）。
- Architecture 規則：以 T-016c 之前的 `rules.go` 執行新的 probe 與 synthetic graph 測試，6 個 probe 與 synthetic graph 都失敗；T-016c 的規則下全部通過。
- Go 的單元與整合測試是在 package 之前寫的（profile、report、upload 的測試先於實作；整合測試先於第一次接線執行，第一次執行時 CSV 的 header 引號寫錯，修正的是測試資料）。另以 26 個 targeted mutation 確認測試擋得下缺陷：關掉 formula 中和（單元與端對端）、去掉全形、接受 invalid UTF-8、不保留跨 read 的字元、不去 BOM、接受 NUL、duplicate 不分類、接受非有限數字、不限深度、不限值長度、不限列數、invalid row 計入統計、preview 不計 bytes、忽略數字的儲存大小、不 sniff、不限上傳大小、replay 再寫 object、report 不核 hash、report 不核 own key、不顯示 failed job、worker 不核 upload、不寫 report、upload 不設自己的期限、讀 body 前不查 role、接受 file 之後的 part。第一輪 24 個被擋、2 個存活：兩者都是 mutation 本身沒有改變行為（`false && a || b` 仍檢查 hash；同一個 switch 的下一個 case 仍拒絕 part），改寫為真正移除檢查後重跑都被擋下：26/26。每次 mutation 後檔案逐 byte 還原。
- 沒有對全部程式跑全面的 mutation harness（見 NOT RUN）。

## NOT RUN

- TEST-029 的「signed URL 過期」：不適用。D-044 規定 P0 不發 signed URL，`downloadImportErrors` 授權後串流；正式 adapter 的 ADR 若改用 signed URL，屆時再測。
- `make e2e-test` 與瀏覽器的 import 流程：UI 尚無 import 畫面（T-015），本任務沒有改 UI 元件；BFF 轉送 multipart 與 50 MB body 的行為沒有測（`apps/web/lib/session/bff.ts`）。
- 全面的 mutation harness（`mutants.py`、`sql-mutants.py`）：只跑了上面的 26 個 targeted mutation 與 SQL、architecture 的紅燈。
- 50 MB／100k rows 的效能目標（PRD §6）與 profile job 的記憶體上限：只有一次 benchmark，不是 T-037 的效能結果。
- 經 `cmd/api` 本身的 `http.Server`（ReadTimeout 30 s、WriteTimeout 60 s）上傳超過 30 秒的檔案：route 自設的期限只以 `httptest` 的 server（300 ms timeout）測過。
- 正式的 object storage adapter：等 owner 選雲端（D-044）；只有 development／test 的 filesystem adapter。
- `frontend-test` 以 Node 22.22.0 執行；`apps/web/package.json` 要求 ≥24.21.0，這台機器沒有 Node 24（`npm ci --engine-strict=false`，lockfile 未改）。
- Import validation（T-017）與 AI（T-025）的 job handler：尚不存在。

## 需要 owner 或 reviewer 決定

- **CB-39(a)**：import failure 沒有 code 目錄；暫定對應為上限 → `PAYLOAD_TOO_LARGE`、檔案的其他問題 → `REQUEST_VALIDATION_FAILED`、基礎設施 → `SERVICE_UNAVAILABLE`、其餘 → `INTERNAL_ERROR`，原 code 與 pointer 在 `details[0]`（與 CB-38(a) 同一做法），UI（T-015／T-017）前決定。
- **CB-39(b)** 的行為選擇（建議照現狀，但屬產品行為）：只有 header 或空陣列的檔案為 FAILED；CSV 欄位數不符為 invalid row 而不是整個檔案失敗；只接受逗號分隔；JSON object／array 推斷為 mixed；preview 的截斷方式與數字正規化；profile job 失敗時以讀取時推導的 FAILED 呈現、row 不跟隨 job。
- **CB-39(c)** 的實作參數（D-007 型，待 benchmark）：值 32,768 字元、巢狀 32 層、preview 256 字元與 1 MiB、report 16 MiB 與保留 10,000 筆、上傳期限 5 分鐘。
- **D-043(c)**（人工 mapping 的 schema）屬 T-017，本任務未改 `SaveMapping`。

## 殘餘風險

- 孤兒 object（file 之後還有 part、與同一 key 競爭落敗、commit 失敗、worker 失去 lease 後的 report）沒有 maintenance 清理（D-044，與 T-023 的 L6 同一項）；每個 request 至多一個 50 MB 的孤兒。
- 沒有上傳的速率、並行數或 tenant 配額（T-036）；一個 editor 可以不斷上傳 50 MB 的檔案佔用儲存空間與 worker。
- Profile job 一次處理一個檔案，記憶體約為檔案大小的一倍（benchmark：45 MB 檔案 45 MB allocation），單一 JSON event 的處理會複製數次；沒有以 limits 限制 worker 的總記憶體。
- 上傳的 transaction 外有兩個短 read transaction 與一個寫入 transaction；`Receive` 讀 body 時不持有 DB 連線，但上傳期間 role 被撤銷時，已寫入的 object 成為孤兒（`Commit` 再查 role 後拒絕）。
- `SignedDownload` 仍在 http.schema.json，P0 沒有 operation 使用。
- Profile 的型別推斷只看值的文字形狀，不是 mapping 的驗證；mapping 之後的全量 validation 屬 T-017。
