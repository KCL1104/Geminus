# T-052 hackathon 切片獨立審查（H-003；H-029 一輪）

**日期：2026-10-26｜審查者：獨立 reviewer（非作者）｜範圍：branch `claude/lucid-meitner-vq1awo` 的 b0a5d2d、2c10de5、ab999d6、346a2dd、9dcd3b3、8e24b93 與 coordinator 的 f923447；`checks/T-052_VALIDATION.md`（作者與 coordinator 兩節）、`checks/evidence/2026-10-26-T-052/`、`checks/evidence/2026-10-26-T-051-T-052-T-015-T-032-coordinator/`｜方式：唯讀；mutant 只用 `go test -overlay`（tree 不變，每次之後 `git status` 乾淨）；DB 只用新建的 `dt_test_rv052a`，跑完刪除。**

**結論：CHANGES REQUESTED（M1）。** 沒有 High。Generator 確實只記錄 engine 自己的 replication（`engine.Compile` 加 `engine.RunReplication` 與 `engine.Observer`，沒有亂數、沒有第二套模型），同一 seed 得到同一 bytes，hash 與報告相符，也不受時區與 GOMAXPROCS 影響。D-059 的規則與 D-072 (b) 的批准文字一致（pack 的規則為 warning，end 早於 start 仍是 invalid row），負向 fixture 各只改一格。Import 路徑真的經過 HTTP 與 job loop；報告說 commit（T-018）沒有實作，這是正確的。D-060 的算術（模擬平均、min–max 與 20 個以上的 Type 7、誤差的符號、觀測 0 時相對誤差為 null）與 §7 一致。唯一的 Medium 是比對函式本身的問題：給它一個 log 沒有涵蓋的視窗，它不回 unavailable，而是把「沒有資料」當成「產出 0」，算出 available 的數字。

## Findings

### M1（Medium）視窗沒有觀測資料時，輸出比對算出捏造的數字

- **位置**：`internal/domainpack/electronicsline/observed.go:233-269`（`case len(events) == 0` 只看整個 event 清單是否為空）；測試 `internal/domainpack/electronicsline/observed_test.go:203-216`（`TestCompareObservedUnavailable` 只測 `nil` events）。
- **問題**：`line_output_per_week` 與 `line_output_per_day` 只在整個 event 清單為空時回 `NO_OBSERVED_DATA`。視窗內沒有任何 event，或只涵蓋視窗的一部分時，觀測端的「最後一站在視窗內 end 的作業數」就是 0 或只有部分，結果卻標為 `available`，而且有絕對誤差與是否在範圍內的值。這違反 D-060（APPROVED，H-018）的「沒有觀測資料時 unavailable」、ELECTRONICS_LINE §7 的 `OBSERVED_WINDOW_TOO_SHORT`、AGENTS.md §2 的「凡資料不足，回 unavailable；不得捏造 metric」，也違反函式自己的註解「no number is ever made up」（`observed.go:141-148`）。同一個視窗裡，FPY 與 cycle time 回的是 `NO_OBSERVED_DATA`，兩者自相矛盾。
- **重現的情境**（以 `go test -overlay` 加入一個 probe test，資料為提交的 log，run 為 seed 2026、5 個 replication）：
  - 視窗為 log 之後的一週 [2026-10-12, 2026-10-19)（+08:00）：`line_output_per_week` 為 available，觀測 0、模擬 3,613、絕對誤差 +3,613、相對誤差 null（`OBSERVED_ZERO`）、在範圍外；`line_output_per_day` 為觀測 0、絕對誤差 +516.143。同一視窗的 `first_pass_yield.fct` 與 `mean_cycle_minutes.fct` 則是 unavailable（`NO_OBSERVED_DATA`）。
  - 視窗為兩週 [anchor, anchor+14d)，log 只涵蓋第二週：`line_output_per_week` 為 available，觀測 1,794、模擬 3,613、相對誤差 +101.4%、在範圍外。
  - 另一個 mutant 拿掉 FPY 的 `len(order) == 0` 判斷（`observed.go:319`），所有測試都通過：沒有任何測試涵蓋「有 event，但視窗內沒有」的情況（reason 會變成 `OBSERVED_VALUE_NOT_FINITE` 而沒有人發現）。
- **為何不是 High**：比對沒有 API 或 UI。demo 與 API test 用的是 ground truth 的正確視窗，所以 +0.70% 這個數字本身沒有錯。但這個函式就是 D-060 的實作，之後的 contract、UI 與 analyst 會直接呼叫它。
- **最小修正**：輸出比對中，`[from, to)` 的每個 `unit`（lcm(期, 循環)）內都至少要有一個 log 的 event（任何紀錄類型），否則回 `NO_OBSERVED_DATA`；理由與 reason 寫進 D-118 (d)（PROPOSED）。補兩個測試：log 之後的一週，以及只涵蓋一半的兩週；再補一個 FPY 與 cycle 的測試：有 event、視窗內沒有，結果應為 `NO_OBSERVED_DATA`。更完整的做法是由呼叫者傳入 log 的觀測期間（dataset 的涵蓋範圍），並要求 `[From, To)` 包含在其中。這需要 T-018 之後的 dataset metadata，可以列為 T-052 其餘範圍。

### L1（Low）輸出「以 end 計數」沒有測試能分辨

- **位置**：`internal/domainpack/electronicsline/observed.go:257`。
- **問題**：D-118 (d) 規定觀測端是「視窗內 **end** 的作業數」。把它改成以 start 計（`case ev.start >= from && ev.start < to:`）的 mutant，`./internal/domainpack/electronicsline/` 的所有測試都通過。原因是 synthetic log 在視窗邊界（週一 00:00）沒有跨越邊界的包裝作業：包裝站下班，週末沒有生產。
- **情境**：一個 24/7 的最後一站，或另一個 anchor 的視窗：start 在 `to` 之前、end 在之後的出貨會被多算，start 在 `from` 之前、end 在視窗內的會被少算，而且沒有測試會失敗。
- **最小修正**：加一個手寫 event 的測試，放兩筆跨邊界的包裝作業（一筆跨 `from`、一筆跨 `to`），斷言計數。

### L2（Low）Generator 的記帳自我檢查從未被測試觸發

- **位置**：`scripts/gen-electronics-line-data/observer.go:274-280`（`check`）、`:429`（呼叫處）。
- **問題**：報告「產生方式」與 D-118 (a) 都把「每一步開始時記帳必須等於 engine 的 in-use 與 down 數，否則 generator 失敗」列為保證。把 `o.check(r)` 換成 `_ = r` 的 mutant，**全部**測試（包括 golden hash）都通過。這在一致的 run 上是預期的，但它表示這個保證沒有任何測試證明它真的會失敗。
- **情境**：之後 engine 的 observation 順序改變（例如新的 `engine_version` 讓 release 的 `ObsResource` 晚於 `ObsServiceEnd`），或有人改壞了 `check`：檢查不再生效，log 可能以錯的 unit 命名或重複預約，只剩 `TestEquipmentIsNeverDoubleBooked` 這種間接檢查。
- **最小修正**：一個 package 內的 unit test，對 `newObserver` 餵一段手寫的 observation（例如 `ObsResource` 報 in-use 2，之後一個只取一個 unit 的 `ObsServiceStart`），斷言 `obs.err != nil`，訊息含 "booked"。

## Info

- **I1 DST 與 offset 的對齊只看視窗內的 event**（`observed.go:156-178`、`:243`）：anchor 的 offset 沒有放進 `offsets`。anchor 在 DST 之前、視窗整段在 DST 之後時，視窗內只有一種 offset，相位檢查（以絕對時間計）可能通過，但工廠的當地班表已和 model 的 calendar 差一小時。log 以 UTC（`Z`）寫出時，也完全偵測不到 DST。§6 與 D-118 (d) 只寫了「視窗內跨 DST」。建議在 D-118 (d) 寫明這個限制（或在 log 帶非零 offset 時，要求它等於 anchor 的 offset），不要求本輪修正。
- **I2 對自己產生的資料標 `hold_out`**（`internal/api/synthetic_line_test.go:235`、`internal/domainpack/electronicsline/observed_test.go:40`）：依 §7 的定義（參數不是由該資料推論）沒有錯，報告與文件的文字也都寫明「只檢查 pipeline，不是對真實產線的驗證」（作者的問題 4、coordinator 的處理 4）。但 `Comparison.Sample` 是唯一機器可讀的來源標示：之後的 UI 若在「輸出比對」旁顯示 hold-out，容易被讀成驗證。owner 回答問題 4 之前，建議 demo 用 `""`（未說明），或在 D-118 (d) 另立一個標示。
- **I3 Mutation 的強度**：作者的 30/30 中，`gen-highest-idle-unit` 與 `gen-rounded-seconds` 只被釘住的 hash 與「提交的檔案就是輸出」兩個 golden 測試殺死（我只跑非 golden 測試時兩者都存活）。它們是命名與解析度的慣例，golden 作為 regression lock 是合理的；但報告最好寫明「這兩個只由 golden 抓到」，以免把 30/30 讀成 30 個獨立檢查。
- **I4 Log 的取列方式與比對的計數方式不同**：generator 依 **start** 取列（`generate.go:168-176`），比對的輸出與 `TestTheLogIsTheEngineReplication` 依 engine 的視窗（實際是 **end**）計數。兩者在這份資料上一致，是因為週末停線，沒有跨週一 00:00 的作業或燒機批次。換 seed 時仍成立，換 model 或 anchor 時不一定，建議在 README 註明。
- **I5 比較函式的排序**：`generate.go:179`（與 `generate_test.go:371`）的 `int(a.start - b.start)` 在 32 位元 int 上，當 `-weeks` 為 4 以上時會溢位（差值超過 2^31 ms，約 24.8 天），排序會錯。提交的一週不會溢位，`make backend-numeric` 的目標也都是 64 位元。建議改成 `cmp.Compare`。
- **I6 `quantile7` 重複**：`internal/results/observed/observed.go:156-165` 與 `internal/results/metrics/stats.go:23-29` 的程式碼逐行相同（「as the Result's」）。日後若只改其中一份，會不知不覺分歧；建議共用（例如由 metrics 匯出），或加一個測試比較兩者的輸出。
- **I7 Evidence 的小誤差**：`comparison-values.txt` 對沒有範圍的輸入一致性列印出 `within false`（實際是 null），而且產生它的程式沒有提交（我用 probe 重算 seed 2026、5 個 replication：3,613、+25、0.00696767，與報告相同）；`verification-summary.txt:2` 把失敗（exit 2）的第一次 `make backend-test` 指向 `backend-test.log`，該次的 log 實際是 `backend-test-run1-timeout.log`；作者一節的 commit 表（`checks/T-052_VALIDATION.md:24-33`、`:124`）用的是 `t052-hk` 上的 hash（a0a515e…75199a2，存在於 repo），與本 branch 的 b0a5d2d…8e24b93 沒有對照表。
- **I8 `EVENT_END_MISSING` 為 warning**：§6 把 end 列為「是（推論需要）」，D-072 (b) 的批准文字只說「產品、詞彙」的 pack 規則為 warning。把缺 end 也列為 warning 是 D-118 (b)（PROPOSED）的延伸，作者的問題 1 已涵蓋，請 owner 決定時一併確認。
- **I9 未測試的防禦路徑**：validator 的「base version 對該 organization 不可見」（`internal/import/validator/validator.go:199-202`）沒有測試。依 `migrations/0001_core.up.sql:136` 的 composite FK（organization_id, model_id, base_version_id），這條路徑在結構上到不了，可以接受。
- **I10 `checks/docs-check.json`**：本審查檔讓 `markdown_files` 多 1。依指示只改本檔，沒有重產該檔，整合時由 coordinator 的 `make spec-check` 重產（它只寫入，不比對）。

## 逐項確認（無問題的部分）

- **Generator 只記錄 engine**：`scripts/gen-electronics-line-data/generate.go:152-165` 以 `engine.Compile`、`engine.RunReplication(…, 0, …, obs)` 執行，與 `des.Engine.Run` 用的是同一個 kernel 函式（`internal/simulation/des/des.go:213`）；沒有 import `math/rand`，也沒有自己抽樣。換線的 service start 是 engine 的 `ObsSetup` 給的 tick 數（`kernel.go:1231`），不是推算出來的。`TestTheLogIsTheEngineReplication`（des 的 Collector）與 `TestSyntheticStationLogIsTheTemplatesRun`（經 API 與 runner，seed 52）都以 engine 自己的計數（出貨 3,588、測試失敗、修理、各機台停機、SMT 換線）對照 log。
- **決定性**：本機 `go run ./scripts/gen-electronics-line-data -check` 在預設、`TZ=Asia/Taipei`、`TZ=America/New_York GOMAXPROCS=1` 下都是 exit 0；輸出不依 map 順序（`main.go:49-55` 排序檔名，紀錄以 (start, seq) 全序排序，JSON 用 `Deterministic(true)`）；anchor 是 `time.FixedZone`，不讀 local zone。
- **數字**：CSV 2,626,641 bytes、22,739 列（22,705 作業、34 停機）、SHA-256 `7ad4a396…14cc5`；`synthetic` 欄不是 `true` 的列為 0；包裝作業 3,588；`ground-truth-model.json` 29,781 bytes、`model_hash` `sha256:74685b6f…667d`；`ground-truth.json` 5,940 bytes；`-weeks 3` 7,837,772 bytes、seed 53 為 2,609,722 bytes；7 個 fixture 各 41 行。全部與報告相符。
- **D-059 與 D-072 (b)**：`events.go` 的八個 code 都是 warning；`ValidateWith` 只對合法的列檢查，warning 排在 error 之後，合計至多 MaxIssues，計數，不進 error report，無法記錄的 issue 為 `ErrCheck`。我的 mutant（缺 end 只對作業檢查、第一列不檢查）都被殺死。API test 中每個 fixture 恰好一個 issue，位置在改動的那一格，message 不引用值；end 早於 start 為 AWAITING_MAPPING，有 report、沒有 dataset。
- **Import 路徑**：上傳、存 mapping、讀取都經 HTTP handler，profile 與 validation 經 job loop（validator 帶 `packs.Registry()`）；VALIDATED 的 canonical dataset 與 `validation.Write` 對同一檔案與 mapping 的輸出逐 byte 相同，operator 不在其中。`commitImport` 只在 OpenAPI 中（`openapi/openapi.yaml:2611`），`internal/import/upload/http.go:32` 寫明 commit 是 T-018 的；報告沒有宣稱 dataset 已凍結或已綁到 run。
- **Audit**：`import.validate` 只多一個 `warnings` 計數（catalogue 的 shape 為 count），沒有欄名或值。
- **架構**：`internal/import/validation` 只拿到一個函式型別；`internal/import/validator` 只依賴 `domainpack` 介面與 `PackLookup`；`internal/results/observed` 只 import `internal/simulation`；generic engine 沒有新的 import。`make architecture-check`：PASS（91 packages、24 rules）。
- **Tenant**：validator 以 job 的 organization 在 tenant transaction 中 `models.GetVersion(ctx, tx, org, …)`；DB tests 以 admin 連線讀取時都以 id 過濾，每個 test 用自己的 `setupRuns` 環境。
- **PROPOSED／APPROVED**：D-058、D-059、D-060、D-072、D-073 為 APPROVED；D-117、D-118 為 PROPOSED（`planning/decisions.json` 的 `approved_by`／`approved_at` 為 null），文件與報告的標示一致；T-052 仍為 `not_started`；D-073 沒有實作，已誠實揭露，並由 coordinator 決定延後。
- **用語**：報告、ELECTRONICS_LINE §12、README、D-118 與程式註解都寫明資料是同一個 model 產生的 synthetic ground truth，比對只檢查 pipeline，不給判定，也不校準；我沒有找到把它說成「對真實資料驗證」的文字（見 I2 的標示問題）。

## 執行的指令

環境：go1.27.3 linux/amd64（`GOTOOLCHAIN=local`）、4 CPU、PostgreSQL 16.15（共用的 cluster，socket `/var/lib/postgresql/geminus-t009-fresh`，port 55440；沒有啟動或停止它）。

| 指令 | 結果 |
|---|---|
| `go test -count=1 ./scripts/gen-electronics-line-data/ ./internal/domainpack/... ./internal/import/... ./internal/results/... ./internal/audit/ ./cmd/worker/` | exit 0（17 個 package ok，`profiler` 沒有測試） |
| `go run ./scripts/gen-electronics-line-data -check`（預設；`TZ=Asia/Taipei`；`TZ=America/New_York GOMAXPROCS=1`） | 三次都是 exit 0 |
| `go run ./scripts/gen-electronics-line-data -weeks 3 -out <scratch>`、`-seed 53 -out <scratch>` | exit 0（大小見上）；`-seed 53 -check` 為 exit 1（預期中的不同） |
| `sha256sum`、`wc`、`awk`（資料集的大小、列數、synthetic 標示、出貨數） | 與報告相符 |
| Probe test（`go test -overlay`，`-run TestReviewProbe ./internal/domainpack/electronicsline/`） | exit 0；印出 M1 的數字，並重算 +25／+0.70% |
| Reviewer 的 mutant（`go test -overlay`，12 個） | `gen-highest-idle-unit`、`gen-rounded-seconds`（只跑非 golden 測試時）、`gen-no-self-check`、`observed-output-by-start`、`observed-fpy-zero-boards` 存活；`gen-batch-member-own-unit`、`gen-waiting-breakdown-dropped`、`gen-downtime-shift-empty`、`gen-downtime-station-is-resource`、`observed-cycle-unknown-mean`、`rules-end-only-for-operations`、`validation-check-before-canonical` 被殺死 |
| `make integration-test`（新建的 `dt_test_rv052a`；`DATABASE_URL`＝`GEMINUS_TEST_DATABASE_URL`、`DT_ALLOW_DB_TESTS=yes`） | exit 0（17:28:38Z–17:34:54Z；SQL tests、migration up／down／up、`-race` 的 Go DB tests，`internal/api` 88.4 秒） |
| `GEMINUS_REQUIRE_DB_TESTS=1 go test -count=1 -race -v -run '^(TestSyntheticStationLogImport\|TestSyntheticStationLogRules\|TestSyntheticStationLogIsTheTemplatesRun)$' ./internal/api/`（同一 DB） | exit 0：3 個 PASS（27.3 秒、3.8 秒、2.7 秒） |
| `make architecture-check` | exit 0：`PASS architecture: 91 packages, 24 rules, 16 build configurations` |
| `go test -count=1 -race -timeout 30m ./scripts/gen-electronics-line-data/ ./internal/results/observed/ ./internal/domainpack/electronicsline/ ./internal/import/validation/ ./internal/import/validator/ ./cmd/worker/` | exit 0（6 個 package ok） |
| DB mutant（`go test -overlay`：validator 在 VALIDATED 時不寫 audit 的 `warnings`；`-run '^TestSyntheticStationLogRules$' ./internal/api/`，同一 DB） | 被殺死（exit 1，`TestSyntheticStationLogRules` FAIL：audit 沒有 `warnings`） |
| `DROP DATABASE dt_test_rv052a` | 完成（之後查詢為 0 筆） |
| `/home/user/Geminus/.venv/bin/python -I scripts/check_docs.py`（含本檔的連結與 code fence） | exit 0；它改寫的 `checks/docs-check.json` 已以 `git checkout --` 還原（I10） |
| `git status --short`（每次 mutant 之後與結束時） | 乾淨（只有本檔） |

## 沒有執行的

- `make e2e-test`、`make frontend-test`、`npm` 相關（依指示不跑；本切片沒有 UI）。
- 完整的 `make backend-test`、`make backend-race`、`make backend-numeric`、`make simulation-reference-test`、`make spec-check`、`make ai-eval`：沒有重跑，採信 coordinator 在 312ea90 上的 evidence（`exit-codes.txt` 全部為 0；312ea90 與 8e24b93 的程式碼相同，只差 T-053 的 evidence，f923447 只改 `checks/`）。
- 作者的 `t052-mutants.py` 沒有重跑（我只看了它與 `mutants-run3-with-db.log`，另外跑了上表自己的 mutant）。
- Live AI（沒有用到；沒有讀取任何 key 或 secret）。
- `checks/docs-check.json` 沒有重產（見 I10）。

## 要求的修正

- **M1**：輸出比對在視窗內（每個 lcm 單位）沒有觀測資料時回 `NO_OBSERVED_DATA`，並補上述三個測試；D-118 (d) 記錄這條規則。
- Low（不阻擋，建議同一輪處理）：L1 的跨邊界計數測試、L2 的自我檢查測試。
