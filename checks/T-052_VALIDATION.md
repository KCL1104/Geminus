# T-052 驗證報告：Synthetic 產線資料與模擬／觀測比對 —— **hackathon 切片**（H-029）

**日期：2026-10-26｜範圍：T-052 的 hackathon 切片（H-029、[HACKATHON_PLAN](../planning/HACKATHON_PLAN.md) 第 1 項「Synthetic 產線資料」），不是 T-052 的全部範圍（其餘見「T-052 尚未完成的範圍」）｜狀態：實作完成，待 H-029 (4) 的一輪獨立審查與 coordinator 整合；`planning/tasks.json` 的 T-052 仍為 `not_started`（見「Planning 的狀態」）｜需求：REQ-007、REQ-021、REQ-025｜Gate：D-058、D-059、D-060、D-073（APPROVED）；本切片提出的 D-118 為 **PROPOSED**（agent 沒有批准）｜驗收測試：TEST-013、TEST-035、TEST-036（本切片涵蓋的部分）**

逐條輸出在 [`checks/evidence/2026-10-26-T-052/`](evidence/2026-10-26-T-052/)。依 coordinator 的指示：只在 `t052-hk` branch 工作、commit、不 push；先寫測試再實作（紅的 log 在 evidence 中）；沒有 migration、沒有 schema 或 OpenAPI 的變更（見「Contract」）；沒有新的 dependency；沒有任何 live AI 呼叫，沒有讀取任何 API key；沒有降低 assertion、沒有加 skip；沒有改其他任務的狀態或任何已批准的 decision。所有資料都是 **synthetic**，不是客戶資料、工廠實測值或「典型值」。

## 交付內容

| 項目 | 內容 |
|---|---|
| `scripts/gen-electronics-line-data`（Go） | Synthetic station log 的 generator：以 engine 的 `engine.Observer` 記錄 ground-truth model（電子產線 pack 的範本）一個 replication 的每片板子的作業與每台機台的停機，寫成 MES 式的 CSV；`-seed`、`-weeks`、`-out`、`-check` |
| `testdata/datasets/electronics-line/` | 一週的 log（`station-log-synthetic.csv`）、ground-truth model（`ground-truth-model.json`）、產生方式與各檔的 hash（`ground-truth.json`）、7 個負向 fixture（`negative/`）、[README](../testdata/datasets/electronics-line/README.md) |
| `internal/domainpack` | optional 的 `EventRulesProvider` 與 `EventIssue`（D-118 (c)；`DomainPack` 的 method set 不變） |
| `internal/domainpack/electronicsline` | `events.go`：D-059 的資料品質規則（8 個 warning code，D-118 (b)）；`observed.go`：ELECTRONICS_LINE §7 的比對 `CompareObserved`（D-118 (d)） |
| `internal/import/validation` | `ValidateWith`：pack 的檢查為 warning（D-072 (b)），排在列的 error 之後、計數、不進 error report；無法記錄的 issue 為 `ErrCheck`（fail closed） |
| `internal/import/validator`、`cmd/worker` | validation job 在 lease 下讀 import 綁定的 base version、以 `Config.Packs` 找 pack 的規則；`Domains()`；worker 傳入它服務的 packs |
| `internal/audit` | `import.validate` 的 metadata 加 `warnings`（計數） |
| `internal/results/observed` | D-060 選項 A 的算術（純函式，generic kernel）：模擬平均、replication 範圍、絕對與相對誤差、是否在範圍內；輸入一致性；unavailable 的 reason |
| `internal/api`（DB tests） | `TestSyntheticStationLogImport`、`TestSyntheticStationLogRules`、`TestSyntheticStationLogIsTheTemplatesRun` |
| 文件 | ELECTRONICS_LINE §6 的註記與 §12（實作紀錄）、TWIN_IR_SPEC §7、API_DATABASE §5、ARCHITECTURE §3、DECISIONS_AND_TBD 與 `planning/decisions.json` 的 D-118（PROPOSED）、`planning/tasks.json` 的 T-052 |

## Commit

`t052-hk` branch（從 8eecee4 開出），依主題分開：

| Commit | 內容 |
|---|---|
| a0a515e | Generator、資料集與其測試（golden 與對 engine 的交叉檢查） |
| aeb5c10 | D-059 的資料品質規則（pack、`EventRulesProvider`）、`ValidateWith`、validator 與 worker 的接線、audit 的 `warnings`；log 經 import 路徑與 run 的 DB tests |
| e2aa44f | Stretch：D-060 的比對（`internal/results/observed`、`electronicsline.CompareObserved`），與 API 上的 run 比對 |
| f259819 | `validator.Handler.Domains` 與 worker 的測試（部署真的檢查 pack 的規則）；mutation harness |
| 75199a2 | 比對：沒有 run 時輸出為 `SIMULATED_METRIC_UNAVAILABLE`（先前誤為 `OBSERVED_WINDOW_NOT_ALIGNED`；新的 assertion 在舊程式上失敗） |
| （最後一個） | 文件、D-118（PROPOSED）、`planning/`、`checks/*.json`、本報告與 evidence |

紅的 log 是在各實作之前的工作狀態取得的（先寫的測試、尚未存在的實作），不是各 commit 的狀態。下面「實際執行結果」的指令都在最後的程式碼上執行。

## Generator：為什麼用 Go、怎麼產生

**選 Go，不選 Python。** Ground truth 必須是範本**在 engine 中的實際行為**：有限緩衝與阻塞、上班時間才累積的故障與 finish-first、班表、整批投入、批次燒機、SMT 換線。用 Python（只能用 pinned 的 spec requirements）重寫等於第二套模擬：AGENTS.md §1 禁止自行維護第二套 simulation domain model，SimPy 也只能作獨立的 reference test；而且無法保證與 engine 一致。Go 的 generator 以 engine 自己的 `engine.Observer`（trace 與 metrics 也用它）記錄 replication，資料就是 engine 對 ground-truth model 的一次執行；RNG、時間量化與事件順序沿用 engine 已有固定 golden vectors 的實作，generator 本身沒有亂數。代價：engine 的軌跡改變（新的 `engine_version`）時 log 會變，要重產並重釘 hash（README 寫了步驟）。位置：`scripts/`（像 `scripts/check-architecture`）；`internal/domainpack/electronicsline` 的子 package 不能被 command import（architecture rule `packs-named-in-one-place`），範本經由 `internal/packs` 取得。

**產生方式**（D-118 (a)）：

- Ground truth：pack 的範本 `electronics_assembly_test_line`（41 個 node、47 條 flow，ELECTRONICS_LINE §4.1、§4.2、§4.4，T-051），以範本檔自己的 model ID 建立；全部參數是範本的 synthetic 示意值。
- Engine：replication 0、**seed 52**、horizon 15 天、des-go-0.5.0；第 1 週為暖機（產線從空的開始），log 為**開始時間**在第 2 週 [2026-10-05T00:00+08:00, 2026-10-12T00:00+08:00) 的作業與停機。
- 時間：model time 0＝2026-09-28（週一）00:00 +08:00（沒有夏令時間，§6 的跨 DST 問題不出現）；RFC 3339、截到整秒。
- 作業：start＝service 開始（SMT 換線後的第一片在換線之後才開始，換線是兩片之間的空檔，§6 的推論方式）、end＝service 結束；result：AOI／ICT／FCT／燒機（含重測）為 pass／fail，除錯為 pass（修好）／fail（報廢），其他站空白；station 為站別屬性（重測記在其站，如 MES）、修補與除錯為步驟 ID；equipment 為 `<機台 resource>_<n>`（engine 的 unit 可互換：以最小編號的閒置 unit 命名），人的步驟沒有 equipment、有 synthetic 的 `op-NN`（不匯入）；shift 為線班表在 start 的 label；product 為板子的產品族；serial `SYN-<A|B>-<到達序號>`；每列 `synthetic=true`。
- 停機：serial 與 equipment 皆為機台（D-059：停機的 case 是設備）、start＝停機、end＝修復、result 為原因碼 `breakdown`；取 engine 讓它停的 unit（閒置的，或正被釋放的：finish-first）。
- 自我檢查：每一步開始時，generator 記帳的 busy 與 down 數必須等於 engine 報告的 in-use 與 down 數，不符即失敗（log 不會是 engine 會否認的猜測）。

## 產生的資料

| 檔案 | 大小 | 內容 |
|---|---|---|
| `station-log-synthetic.csv` | 2,626,641 bytes，22,739 列（22,705 作業、34 停機），SHA-256 `7ad4a396aa034c4b1538377dba593aa9e55b8a2e3d34e4be0f06378ce1214cc5` | 3,843 片板子；SMT 開始 3,620 片（A 1,800、B 1,820，含上週帶過來的 B）、出貨 3,588 片、報廢 24 片；測試失敗 AOI 104、ICT 187、FCT 249、燒機 16；SMT 換產品 10 次；停機 SMT 6、AOI 5、ICT 每台 4、FCT 每台 4、燒機 chamber 1 與 5 各 2、1 次 |
| `ground-truth-model.json` | 29,781 bytes | 範本的 IR（model_hash `sha256:74685b6f…667d`） |
| `ground-truth.json` | 5,940 bytes | seed、generator（`electronics-line-synthetic-1`）與 engine 版本、anchor、視窗、規則、各檔的 SHA-256／大小／列數、以上的計數 |
| `negative/*.csv` | 7 個，各約 4.6 KB | log 的前 40 列改一格：end 早於 start、未知站 `xray`、未知產品 `board_c`、缺產品、未知紀錄類型 `maintenance`、測試站缺 result、result `PASS` |

全部約 2.7 MB（測試要求整個資料集 < 3 MiB）。`-weeks 3` 產生 3 個觀測週（7,837,772 bytes，不提交）；較長的 log 前一週與提交的一週逐列相同（`TestMoreWeeksExtendTheSameLog`），所以 §7 建議的「前幾週推論、最後一週比對」可以延伸現有資料。另一個 seed（53）得到另一份 log（2,609,722 bytes）。

## Import 與 run 顯示什麼

- **Import 路徑**（`TestSyntheticStationLogImport`，DB，經真的 HTTP handler 與 worker 的 job loop）：editor 以範本建立 model（version 1）；上傳 2.6 MB 的 CSV → profile（22,739 列、11 欄，start／end 推斷為 timestamp）→ 以 D-059 的 mapping 存檔（operator 不匯入、synthetic 進 metadata；不需要 D-073 的 transform）→ validation job（帶 worker 服務的 packs）→ **VALIDATED**，0 個 invalid row、**0 個 warning**（log 遵守它自己的 model 的詞彙），canonical dataset 在 import 自己的 key 下、SHA-256 與大小與 row 一致、與 `validation.Write` 對同一檔案與 mapping 的輸出逐 byte 相同、每個 event 通過 canonical-event schema、沒有 operator、每個 event 都標 synthetic；audit 的 `import.validate` 沒有 `warnings`；model 的 Twin hash 不變（D-009）。
- **負向 fixture**（`TestSyntheticStationLogRules`）：end 早於 start → AWAITING_MAPPING、1 個 invalid row、`EVENT_END_BEFORE_START` 在 `/<列>/end`、有 error report、沒有 dataset；其餘 6 個 → VALIDATED、恰 1 個 **warning**（`EVENT_STAGE_UNKNOWN`、`EVENT_PRODUCT_UNKNOWN`、`EVENT_PRODUCT_MISSING`、`EVENT_TYPE_UNKNOWN`、`EVENT_OUTCOME_MISSING`、`EVENT_OUTCOME_UNKNOWN`）在改過的那一格，message 不引用值，有 dataset、沒有 error report，audit 的 `warnings` 為 1。
- **綁到 run**（`TestSyntheticStationLogIsTheTemplatesRun`）：API 給的 version 1 與 ground-truth model 除了 model ID 與名稱之外相同（RFC 8785 hash 相等），沒有綁 dataset；以 **generator 的 seed（52）、1 個 replication、horizon 14 天、warmup 7 天**經 API 與 worker 執行，replication 0 在量測視窗出貨、測試失敗與修理的數量**等於 log 的**（3,588、556、533）——log 就是 engine 的那個 replication，不是另一個模型；run 凍結的是該 immutable version（`model_hash`）、它的 dataset manifest（`[]`，與 manifest hash）與 seed，`run_datasets` 沒有列。另一個 seed（2026）是同一條線的另一週。
- **還不能做的**：`commitImport`（凍結 VALIDATED 的 canonical dataset 為 dataset version，D-009）是 T-018，尚未實作（route 不存在），所以 import 的 dataset 還不能進 run 的 manifest；範本本身也不需要 empirical dataset。見「與 spec／指示的差異」。

## D-059 的資料品質規則

詞彙取自 import 綁定的 base version（D-118 (b)）：站名（站別 process 的 `station` 屬性）與所有 process 的 node ID、測試站（aoi、ict、fct、burn_in 中 model 有的）、entity types。

| Code | 條件 | Path |
|---|---|---|
| `EVENT_TYPE_UNKNOWN` | event_type 不是 operation、downtime | event_type 的欄 |
| `EVENT_STAGE_MISSING`／`EVENT_STAGE_UNKNOWN` | 作業或停機沒有站／站不是 model 的站或步驟（大小寫不同即不認得） | stage 的欄 |
| `EVENT_END_MISSING` | 作業或停機沒有 end_timestamp | end_timestamp 的欄 |
| `EVENT_OUTCOME_MISSING`／`EVENT_OUTCOME_UNKNOWN` | 測試站的作業沒有 result／作業的 result 不是 pass、fail（停機的 result 是使用者的原因碼，不檢查） | outcome 的欄 |
| `EVENT_PRODUCT_MISSING`／`EVENT_PRODUCT_UNKNOWN` | entity type 兩個以上時作業沒有 product（D-055）／product 不是 entity type | product 的欄 |

欄位沒有 mapping 時 path 為整列。全部是 warning（D-072 (b) 的批准文字：「T-052 的 pack 品質規則（產品、詞彙）仍是 warning」），import 仍 VALIDATED；end 早於 start、duration 不一致仍是 D-072 (b) 的 invalid row。`validation.issues` 先列 error、再列 warning，合計至多 100；warning 不進 error report。§6 的跨列檢查（同一 serial 的作業重疊、超過 capacity、站序與拓樸不符、停機中仍有作業、資料缺口）與「停機的 case_id 為設備 ID」沒有做（T-027 的 inference 範圍）。

## D-073 與 target fields

- **Target fields**：`end_timestamp`、`shift`、`product` 在 T-017 已加入 `import-mapping.schema.json#/$defs/TargetField`（`ai-mapping.schema.json` 以 `$ref` 共用）與 `internal/ai/aicontext.TargetFields`（D-070 (a)），本切片不需要再改。
- **D-073 的 transform**（constant、`seconds_to_ms`、`minutes_to_ms`，沒有 offset 的時間仍拒絕）：**未實作**。指示是「只做 demo 資料需要的部分」：demo 的 log 有 record type 欄、沒有 duration 欄、時間都有 offset，所以三個 transform 都不需要；做它們要改兩個 schema 的 transform、`mapping.Check`／`Compile`／`Apply`、constant 值的 pack 詞彙檢查（saveImportMapping 需要 model 的 pack）、AI suggestion 的 prompt 與 eval、TS 型別，屬 T-052 其餘的範圍。

## D-060 的比對（stretch）

`internal/results/observed`（generic、純算術）與 `electronicsline.CompareObserved`（哪些量、哪一站、哪個視窗；D-118 (d)）。對 synthetic 資料與 run（`checks/evidence/2026-10-26-T-052/comparison-values.txt`）：

| Key | 類別 | 觀測（log） | 模擬（seed 2026、5 個 replication） | 誤差 |
|---|---|---|---|---|
| `line_output_per_week` | 輸出 | 3,588 | 平均 3,613、min–max 3,555–3,677 | +25（+0.70%），在範圍內 |
| `line_output_per_day` | 輸出 | 512.57 | 516.14、507.86–525.29 | +3.57（+0.70%），在範圍內 |
| `first_pass_yield.aoi`／`ict`／`fct`／`burn_in` | 輸入一致性 | 0.9713／0.9541／0.9399／0.9955 | 模型輸入 0.97／0.95／0.93／0.995 | −0.0013／−0.0041／−0.0099／−0.0005 |
| `mean_cycle_minutes.smt`／`aoi`／`ict`／`fct`／`burn_in`／`pack_ship` | 輸入一致性 | 1.097／0.934／2.129／3.328／240／1.105 | 模型平均 1.1／0.933／2.133／3.333／240／1.1 | 皆 < 0.5% |

20 個 replication 時範圍改為 Type 7 的 2.5%／97.5%（3,487.97–3,669.40）。以 generator 的 seed、1 個 replication，模擬即 log，輸出的誤差為 0。標示 hold-out：範本的參數不是由這份資料推論；但資料由同一 model 產生，所以這只檢查比對的 pipeline，不是對真實產線的驗證。不給通過判定、不調整模型。資料不足時（沒有 event、視窗短於或不是 model 循環（一週）的整數倍、相位不同、跨兩個 UTC offset、result 或 end 缺、replication 沒有、模型的測試結果無法辨識、Weibull／empirical 的平均）為 D-060 的 reason，不給數字。沒有 API、contract 或 UI（指示：不加 UI）。

## Contract

沒有改 `schemas/` 或 `openapi/`：warning 用既有的 `ValidationIssue.severity`，VALIDATED 的 `validation` 本來就允許 issue；新的 code 是 import issue 的 code（`ValidationIssue.code` 是自由字串），不是 model 的 `ValidationCode`（所以不在 pack 的 `codes.go`）；比對沒有 HTTP 介面。Audit 的 `import.validate` 多一個計數成員（`internal/audit` 的 catalogue 與其測試）。沒有 migration（worker 本來就能 SELECT `model_versions`）。

## 先紅後綠

先寫的測試在實作前執行：generator 的 package 不存在（`red-generator.log`）；`EventRulesProvider`、`EventIssue`、`ValidateWith`、`eventCheck` 未定義，audit 拒絕 `warnings`（`red-event-rules.log`）；`internal/results/observed` 沒有程式（`red-results-observed.log`）；`CompareObserved` 未定義（`red-pack-observed.log`）；`validator.Handler.Domains` 未定義（`red-validator-domains.log`）；沒有 run 的比對（新的 assertion）在修正前的程式上失敗（`red-observed-no-run.log`，以 `go test -overlay` 換回前一版的 `observed.go`）。實作後第一次執行：generator 只有釘住的佔位 hash 與尚未提交的檔案失敗（`first-run-generator.log`，其他 7 個測試通過），之後才寫入資料與 hash；pack 的一個測試以 `model.Encode`（map 順序非決定性）比較 IR，改為 `json.Deterministic(true)`（測試本身的錯）。整合測試（`internal/api/synthetic_line_test.go`）在 unit 綠了之後才寫，所以沒有紅的 log；它看得到的接線由 mutation 證明（`validator-drops-the-check` 只有它抓得到）。第一次執行時 version 與 ground truth 的比較以 `json.Marshal` 比 bytes，`domain_attributes` 的原始 JSON 在縮排的檔案中帶空白而不同，改為以 RFC 8785 的 model hash 比較（測試本身的錯）。

## 測試涵蓋

- **Generator**（`scripts/gen-electronics-line-data`，10 個測試）：同一 seed 每個檔案逐 byte 相同、CSV 的 SHA-256 等於釘住的值，另一個 seed 不同；提交的檔案就是輸出（目錄中沒有多餘的檔案，整個資料集 < 3 MiB）；ground truth 是範本、標 synthetic、hash 一致；**engine 對同一 replication、同一視窗自己的計數等於 log 的**（出貨、測試失敗、修理、每台機台的停機、SMT 換線）；每列的形狀、詞彙、+08:00、整秒、在視窗內、shift 與獨立寫的班表一致；沒有機台或作業員同時做兩件事（燒機的一批除外）、停機中不作業；每片板子的步驟照 §4.1 的路由、時間不倒退；換線是 ≥ 15 分的空檔；更長的 log 延伸同一軌跡；負向 fixture 恰改一格。
- **規則**（`events_test.go`）：28 個事件逐一的 code 與 field、message 不引用值；一個產品族不要求 product；沒有站別屬性的 model 只認 step ID、沒有必填 result；別的 domain 沒有規則；不改 model 與 event；**提交的 log 的 22,739 列沒有任何 warning，每個 fixture 恰一個預期的 code**。
- **Validation**（`TestPackWarnings`）：warning 在 cell 或整列、列仍合法、dataset 與 preview 不變、不進 report、先 error 後 warning、MaxIssues、計數、不合法的列不檢查、7 種無法記錄的 issue 被拒。**Validator**（`TestEventCheck`）：沒有 Packs、沒有規則的 pack、電子產線的規則、不服務的 domain 與無法 decode 的 version（不重試的 `INPUT_INVALID`）、`Domains()`。**Worker**：部署的 validation 檢查 API 服務的 packs。**Audit**：`warnings` 計數。
- **比對**（`internal/results/observed`）：手推的向量（binary64，以 Python 驗算）：範圍內、外、端點、觀測 0、1 個與 5 個 replication、20 個的 Type 7、不改輸入；輸入一致性；7 種 unavailable。（`observed_test.go` in pack）：對 log 的獨立計數（每週與每日出貨、4 站 FPY、6 站 cycle）；另一個 seed 的 replication；key 與順序；11 種 unavailable 的情況；不改輸入；沒有 model 與不是 canonical 的時間為 error。
- **Integration**（DB）：見上「Import 與 run 顯示什麼」；另 `TestSyntheticStationLogImport` 最後把 VALIDATED 的 dataset 與 API 提供的 5 個 replication 的 Result 比對。

## Mutation

`evidence/2026-10-26-T-052/t052-mutants.py`：每個 mutant 以 `go test -overlay` 改一處行為（tree 不變），跑它列出的 package。30 個 mutant（generator 8、規則 5、validation 4、validator 與 worker 3、比對 9、需要 PostgreSQL 的 validator 接線 1）。第 1 次（`mutants-run1.log`）29/30 killed、1 個無法 build（不算 killed）；修正該 mutant 使其可 build 後第 2 次 30/30 killed（`mutants-run2-with-db.log`，含 DB 的 mutant，在 `dt_test_t052a` 上）；75199a2 之後在最後的程式碼上第 3 次（`mutants-run3-with-db.log`）。

## 實際執行結果

環境：`environment.txt`（go1.27.3 linux/amd64、Python 3.13.16、PostgreSQL 16.15、4 CPU）。

程式碼在 commit 75199a2 之後沒有再改；下表的指令都在那之後執行（`verification-summary.txt` 有時間與 exit code）。這台 4 CPU 的機器同時有其他 agent 在跑測試與 browser（load average 10–28）。

| 指令 | 結果 | Log |
|---|---|---|
| `go run ./scripts/gen-electronics-line-data -check` | exit 0：提交的 10 個檔案就是 generator 的輸出 | `generator-check.log` |
| `make backend-test`（第 1 次） | **exit 2**：`scripts/check-architecture` 的測試超過 `go test` 預設的 10 分鐘（load average 約 18）；其他 76 個 package ok。該 package 在 T-051 的執行已用 547.7 秒，接近上限；本切片沒有改它 | `backend-test-run1-timeout.log` |
| `make backend-test`（第 2 次） | exit 0：`go vet` 與 `GEMINUS_FULL_DIFFERENTIAL=1 go test`，77 個 package ok，528 秒；`scripts/check-architecture` 重跑 520.6 秒通過，其餘 76 個為第 1 次在同一份程式碼上的結果（go test 的 cache） | `backend-test.log` |
| `make architecture-check` | exit 0：`PASS architecture: 89 packages, 23 rules, 16 build configurations`（新增 `scripts/gen-electronics-line-data`、`internal/results/observed`） | `architecture-check.log` |
| `make backend-numeric` | exit 0：6 個 GOARCH 的 listing 沒有 FMA 與平台相依的 math 呼叫（涵蓋新的 `internal/results/observed` 與 pack 的 `observed.go`），之後以 GOAMD64=v3 build 執行測試全部 ok，382 秒 | `backend-numeric.log` |
| `go test -race -count=1`（改動的 15 個 package：generator、`internal/domainpack/...`、`internal/import/...`、`internal/results/...`、`internal/audit`、`cmd/worker`） | exit 0，119 秒 | `race-changed-packages.log` |
| `make ai-eval` | exit 0：fake provider 的 context 與 review suite（本切片沒有改 AI，跑來確認沒有回歸）；`checks/ai-evals/*.json` 沒有變化；live provider 為 NOT RUN | `ai-eval.log` |
| `make integration-test`（新建的 `dt_test_t052b`；`DATABASE_URL`＝`GEMINUS_TEST_DATABASE_URL`、`DT_ALLOW_DB_TESTS=yes`） | exit 0，352 秒（SQL tests、migration up／down／up 的 smoke、`-race` 的 Go DB tests，`internal/api` 99.6 秒） | `integration-test.log` |
| `go test -race -v -run 'TestSyntheticStationLog\|TestElectronicsLineTemplateClosedLoop' ./internal/api/`（同一 DB） | exit 0：4 個測試 PASS（import 34.0 秒） | `api-synthetic-line.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | exit 0：contract、planning、docs 連結、SQL inventory、reference 算術、decisions（D-118 的形狀）、TS contract、獨立 hash、DB 測試（`checks/*.json` 由它重產） | `spec-check.log` |
| Mutation（`t052-mutants.py`，含 DB 的 mutant） | 30/30 killed（第 3 次，在最後的程式碼上） | `mutants-run3-with-db.log` |
| T-052 設為 `in_progress` 時的 `check_decisions.py` | exit 1：「T-052: started before its dependencies」（之後還原） | `tasks-in-progress-refused.log` |

## 與 spec／指示的差異

- **Commit 與凍結的 dataset revision 不存在**：指示寫「import 已經端到端可用（…T-017 mapping/validate/commit/freeze…）」，並要整合測試走「upload → profile → mapping → validate → commit → frozen dataset revision」。但 `commitImport` 是 **T-018（`not_started`）**：OpenAPI 有這個 operation，`internal/import/upload.Routes` 沒有（註解：「suggestion 與 commit 是 T-027 與 T-018 的」），沒有任何 handler；`suggestImportMapping`（T-027）也一樣。依 AGENTS.md §1（來源與衍生文件矛盾時停止受影響部分、記錄差異），本切片停在 VALIDATED 的 canonical dataset（T-017 寫的、commit 將凍結的那份 bytes），沒有替 T-018 實作 commit，也沒有以 owner SQL 假造 dataset version。Run 綁的是範本 version 自己的（空的）manifest 與 seed；log 與 run 的關係以「generator 的 seed 重現 log」證明。
- **D-059 的 target fields 已存在**（T-017，D-070 (a)），不需要再加；**D-073 未實作**（demo 資料不需要，見上）。
- **AI mapping suggestion**：指示提到 T-025/T-026 的 fake provider mapping suggestion；mapping suggestion 的 route 與 job 是 T-027（`not_started`），本切片沒有用到 AI，也沒有改 AI 的 context 或 prompt。
- **資料量**：只提交一週（2.6 MB，「well under 5 MB」）；§7 建議的多週 hold-out 切分以 `-weeks` 產生、不提交。
- **時間截到整秒**：MES 匯出常見的解析度；log 的 cycle 平均與 engine 的差小於 1 秒（測試以 2% 為界對模型平均）。

## Planning 的狀態

T-052 保留 `not_started`：`scripts/check_decisions.py` 拒絕在依賴（T-051、T-017）未到 review／done 時設為 in_progress（T-051 仍為 not_started）。`decision_gates` 加入 D-118，`planned_outputs`、`spec_refs`、`verification_commands` 與 `completion_evidence`（本報告）已更新。

## T-052 尚未完成的範圍

1. **D-073**：constant（event_type、outcome、stage、shift、product，值須在 pack 詞彙中）、`seconds_to_ms`、`minutes_to_ms` 的 schema、`mapping.Check`／`Compile`／`Apply`、saveImportMapping 的詞彙檢查、AI suggestion 的 prompt 與 eval、TS 型別，先寫測試。
2. **Commit 之後**（依賴 T-018）：凍結的 dataset version、它與 baseline run 的連結（「有連結的觀測 dataset」），以及比對的 contract／API（目前只有純函式）。
3. **§7 的其餘量**：站別 queue wait 與 lead time（近似）、cycle time 的 p50／p95、availability／MTBF／MTTR（停機紀錄），以及宣告 `period_throughput`（D-058，仍被 gate 擋）後的逐期比較。
4. **§6 的跨列資料品質檢查**與「停機的 case_id 為設備 ID」；conformance suite 對 `EventRulesProvider` 的檢查（不改 IR、決定性、並行）。
5. 作為 T-027 inference、T-038 AI eval 與 T-040 E2E 的 fixture（資料已在 testdata，尚未被它們使用）；H-029 (4) 的一輪獨立審查。

## NOT RUN

- 完整的 `make backend-race`（只跑了改動的 15 個 package，見上表；`internal/simulation/engine` 沒有改）。
- `make e2e-test`（指示：不跑，固定 port 與其他 agent 共用；本切片沒有 UI）與 `make frontend-test`（沒有改 `apps/web`、schema 或 TS 型別）。
- `make simulation-reference-test`（沒有改 engine；generator 只觀察 engine）。
- Live AI provider（沒有用到 AI；沒有讀取任何 API key）。
- D-073 的 transform、commit（T-018）與其後的 dataset／run 連結：沒有實作，所以沒有測試（見上）。
- 範本組合的 SimPy 獨立對照（T-051 已記為 NOT RUN）：synthetic 資料由同一個 engine 產生，與 engine 的一致性不是獨立的 oracle；獨立的檢查是 log 的形狀、路由、不重複預約與換線空檔，以及比對對 log 的獨立計數。

## 問題（給 owner／coordinator）

1. D-118 的 (a)–(d) 是否採用；特別是 (b) 規則全部為 warning（D-072 (b) 的文字）與詞彙的來源（站名＋所有 step ID），以及 (d) 的對齊規則（兩邊視窗都是 model 循環的整數倍、同相位）。
2. Commit（T-018）實作前，demo 要「綁到 run」的是什麼：目前只能展示 VALIDATED 的 dataset 與以 generator 的 seed 重現的 run；觀測 dataset 與 baseline run 的連結需要 T-018 與一個新的 contract。
3. D-073 是否要在 hackathon 期間做（demo 資料不需要；若要展示「每站一個檔、沒有紀錄類型欄」的匯入才需要 constant）。
4. 對它自己的 synthetic 資料，比對標 hold-out 是否恰當（參數不是由資料推論，但資料由同一 model 產生）；或另立一個標示（例如「ground truth」）。
5. 若 T-050 的審查使 `engine_version` 由 des-go-0.5.0 升級並改變範本的軌跡，資料集要重產、hash 要重釘（README 有步驟；它是 regression lock，不是 oracle）。
6. D-118 的編號：其他並行的 agent 也可能提出 D-118，合併時若衝突需重新編號（`planning/decisions.json`、DECISIONS_AND_TBD、ELECTRONICS_LINE §12、TWIN_IR_SPEC §7、API_DATABASE §5、ARCHITECTURE、`planning/tasks.json`、README 與本報告都引用它）。
