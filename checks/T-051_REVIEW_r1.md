# T-051 獨立審查 第 1 輪（hackathon 切片，H-003／H-029 (4)）

**日期：2026-10-26｜審查者：獨立 reviewer（沒有參與實作）｜對象：`claude/lucid-meitner-vq1awo` 上的 52a102b、4038ac6、949cae3、cce4587（T-051 切片）與 492fae6（coordinator 的整合驗證）｜報告：[`checks/T-051_VALIDATION.md`](T-051_VALIDATION.md)（作者與 coordinator 兩節）｜evidence：`checks/evidence/2026-10-26-T-051/`、`checks/evidence/2026-10-26-T-051-T-032-coordinator/`｜方式：只讀；除本檔與 `make spec-check` 重產的 `checks/docs-check.json` 計數（見 #12）外沒有改任何 tracked file；mutant 與 probe 都以 `go test -overlay` 從 scratch 目錄載入，tree 不變（每次之後 `git status` 都是乾淨的）**

依據：AGENTS.md §2（P0 invariants）、docs/domains/ELECTRONICS_LINE.md（§2–§5、§11）、DECISIONS_AND_TBD 的 D-058（APPROVED，選項 A）、D-117（PROPOSED）、T-050 的 ADR-026 與 D-096／D-097（PROPOSED）、SIMULATION_SPEC §5／§9、TWIN_IR_SPEC §4、52a102b 的 schema／OpenAPI／TS 變更、SoftwareDeliveryDomainPack 的 pack 寫法、`planning/tasks.json` 的 T-051（含 hackathon 切片那一行）。

## 結論

**CHANGES REQUESTED**：1 個 Medium（M1），沒有 High。M1 修好（含測試）並由 coordinator 驗證後即可依 H-029 (4) 完成；Low 依 H-029 記為比賽後的後續。

## 確認成立的部分

- **邊界**：`internal/simulation/des` 只多了一個 `func(*model.ModelIR, *simulation.Result) map[string]simulation.MetricSummary` 欄位與 `modelMetrics`（des.go:59–64、265–273、293–311），沒有新的 import；`domainpack.ModelMetricsProvider` 是 optional interface（domainpack.go:80–93），`DomainPack` 的 method set 不變；pack 只由 `internal/packs` 註冊（packs.go:20）；runner 以 type assertion 接上（runner.go:295–297）。`make architecture-check` 我重跑為 PASS（87 packages、23 rules、16 build configurations）；作者的兩個 probe（kernel import pack、pack import `net/http`）的 log 顯示都被擋下。`des/modelmetrics_test.go` 以 stand-in 函式測 hook，沒有 import 任何 pack。
- **Hook 的語義**（D-058 A、D-117 (f)）：在 `DomainMetrics` 之後、digest 之前；傳 effective IR（`RunInput.Model`，types.go:29）的 encode／decode 副本；空的答案不寫 `metrics.domain`（schema 的 `minProperties: 1`）；panic 為 `ErrInternal`。測試與作者的 mutant（`domain-written-when-empty`、`hook-gets-the-callers-ir`、`hook-after-the-digest`）都涵蓋。
- **規則與嚴重度**：rules.go 與 D-117 (b) 一致：規則 1 `STATION_RESOURCE_INVALID` error、規則 3 `RETEST_RESOURCE_MISMATCH` error（首測沒有 primary 時不連鎖；沒有首測時也報）、規則 2 `TEST_RESULT_NOT_IDENTIFIED` warning（§3.4 唯一寫明嚴重度的一條）、`DOMAIN_ATTRIBUTES_INVALID` warning；path 與訊息（只含 pack 自己的站名與 role）照 D-117；`validate.Sort`。四個 code 在 `ValidationCode`、TS、UI 兩語、AI review 的固定句、TWIN_IR_SPEC §4，且都標 D-117 PROPOSED。
- **範本**：逐項對照 §4.1／§4.2／§4.4：41 個 node、47 條 flow 的拓樸與 routing 機率；7 個 resource 的 capacity、班表、TTF／TTR；11 個 process 的 service time 與 resource；buffer capacity（`q_aoi` 20、`q_ict` 10、`q_fct` 10、`q_burnin` 120、`q_pack` 400，其餘無限）；燒機批次 20／40／120、每批 constant 240；`board_a` 平日 06:00、`board_b` 平日 14:00 各 360 片的 table 投入；SMT A↔B tri(15, 25, 45)、initial `board_a`；兩班 06–14、14–22（S(t) 與 §4.2 的 [360, 1320)＋d·1440 相同）。`metadata` 標 `synthetic: true` 與 `source: "synthetic, for demonstration"`，建立的 model 保留。偏離（SMT 一個 server、不採 lognormal、§4.2＋§4.4 合併、班表拆兩個 label）都在報告與 D-117 (g) 說明。
- **指標**：metrics.go 的每個公式我都對照 §5：UPH（N·3.6e6／T）、排班 UPH（L 由 IR 的 calendar 以 `calendar.FromModel(cal, unit, 1, nil)` 算，不乘 capacity）、FPY 與直通率為模型輸入（`_input`）、六種狀態（P＝U−K−Z，下班＝1−U−D−I，相加為 1）、availability＝∫A／∫S、starved＝∫max(0, A−U)／(C·T) 只在專用／單一 resource／units 1／非批次時、scheduled utilization 與 overtime 只對有 calendar 的站、line balance 為 mean(w)／max(w)、每個 resource 一次；缺功能時回 §5 的 reason 而不是 0 或 100%；宣告式的 `line_wip`、`scrap_rate`、`simulated_first_pass_ratio`、`retest_ratio` 為 unavailable（`*_NOT_DECLARED`）；任何指標都不稱 OEE（只在 prompt rule 中說「utilization is not OEE」）。Pack 的判定（`Breakdowns != nil`、`Setups != nil`、`CalendarID != ""`、queue `Capacity > 0`）與 engine 的 `ResourceHasBreakdowns`／`ResourceHasSetups`／`ResourceHasCalendar`／`p.finite`（compile.go:492–527、645–652）一致，所以 `resource_states` 的出現條件與 pack 的期待相同。
- **決定性**：沒有任何結果依 map 的走訪順序（map 只用於查表；`out` map 由 canonical hashing 編碼）；跨 replication 依 index 彙整；regression lock 我重跑得到同一個 digest `sha256:f5fee84c…7f904`（des-go-0.5.0）。報告與 ELECTRONICS_LINE §11 的數字我以 probe 重現（見「執行紀錄」）：平均出貨 10,756.2、FCT 工作比例 0.898422、ICT 0.843250、SMT 0.825134、AOI 0.701142、包裝 0.410857、燒機 0.365079、SMT 換線每個 replication 30 次、平均批量 39.18、排班 UPH 44.8175、共 110 個 domain 指標。
- **Contract**：`CreateModel` 的 `oneOf`（每個 pack 一個分支）、3 個 fixture 與 cases.json、OpenAPI 的兩個 request 範例（`check_contracts.py` 現在也驗 request 範例）、TS union 與 `@ts-expect-error` 反例、`TestCreateModelContractNamesTheTemplates`（schema 的配對＝deployment 的範本）、`metrics.domain` 已在 `simulation.schema.json`（T-046 phase 2）。`make spec-check` 我重跑為 exit 0，重產的 `checks/*.json` 與 commit 的相同。
- **i18n**：en、zh-Hant 的 4 個 validation 訊息與 `domain.electronics_line.{node,resource,metric}.*`；`TestCataloguesHaveUILabels` 以 pack 的 catalog 對照兩語。
- **誠實狀態**：D-117 在 `planning/decisions.json` 為 `proposed`、`approved_by`／`approved_at` 為 null；DECISIONS_AND_TBD、ELECTRONICS_LINE §11、TWIN_IR_SPEC、SIMULATION_SPEC、ARCHITECTURE、common.schema.json 都寫 PROPOSED；T-051 保留 `not_started`（checker 拒絕，log 在 evidence）；NOT RUN 有列；沒有 skip、placeholder 或 fake 被稱為完成。紅的 log 與報告的描述一致；作者的 31 個 mutant 我讀過腳本與 log（31/31 killed，build failed 不算 killed）。

## Findings

### Medium

**M1 — 同一站別有兩個以上首測時，直通率重複乘上該站的 FPY（數字錯誤且沒有任何 warning）**

- 位置：`internal/domainpack/electronicsline/metrics.go:248–267`（`rolledYield` 對 `c.l.stations` 的每個測試站相乘）；`internal/domainpack/electronicsline/stations.go:175–181`（每個 `pass: first` 的站別 process 各自成為一個 station）。
- 問題：ELECTRONICS_LINE §5（APPROVED）把 rolled throughput yield 定義為「主線各測試站（AOI、ICT、FCT、燒機）FPY 的乘積」，即每個站別一次、串聯。實作是「每個首測 process 一次」。並聯的測試 cell（兩個 FCT process，各有自己的 test result；電子產線常見的建法，T-056「照片 → 模型 proposal」辨識出兩個 FCT 工作站時也可能產生這樣的模型）會讓 FCT 的 FPY 被乘兩次。Pack 的 `Validate` 對這樣的模型沒有任何 issue，所以 `rolled_throughput_yield_input` 以「available」顯示一個錯的模型輸入，違反 AGENTS.md §2「不得捏造 metric」與 REQ-015 的精神（數字不是由模型推得）。
- 失敗情境（我以 overlay probe `TestZZReviewParallelFCT` 執行）：範本加一個 decision `fct_split`（原本流到 `q_fct` 的 flow 改流到它），把 ICT 首測與重測的通過件各 0.5 分到 `q_fct` 與新的 `q_fct_b → fct_b → fct_b_result`（`result_of: fct_b`，pass 0.93／fail 0.07）。`validate.IR` 有效、`pack.Validate` 回 `[]`；run 後 `fpy_input.fct`＝`fpy_input.fct_b`＝0.93，但 `rolled_throughput_yield_input`＝**0.793020**（＝0.97·0.95·0.93·**0.93**·0.995），正確的串聯值是 0.852710。
- 最小修正：直通率依站別（`stationKind`，line order）各取一次 FPY；同一站別的多個首測 FPY 相同時用該值，不同時（或其 routing 份額不明時）回 unavailable 並帶 reason（例如沿用 `TEST_RESULT_NOT_IDENTIFIED`，或新增一個 reason 並記入 D-117 (c)(d)）。加一個手推測試：兩個 FPY 相同的並聯 FCT → 直通率等於串聯值；兩個 FPY 不同 → unavailable。同一測試可順便涵蓋 L4（line balance 每個 resource 一次、重測歸到 primary 相同的首測）。

### Low

**L1 — 「沒有任何功能的 resource」的分支沒有測試**（`metrics.go:325–333`）
- 問題：D-058（APPROVED）寫明「沒有任何功能的 resource 不出現在 `resource_states`：其處理＝utilization、閒置＝1−utilization，pack 由現行 `resource_utilization` 得出」。`metrics_test.go` 的手推 Result 對每個 resource 都列了 `resource_states`，「no finite buffer」的變體也保留了 oven、packer 的項目（真實 engine 此時不會列），範本與 conformance 的模型也都有功能，所以這個分支從來沒有被執行。
- 失敗情境：mutant `rv-featureless-idle-wrong`（`I: ct - in` → `I: ct`，沒有功能的站閒置比例變成 100%、下班比例變成負值）與 `rv-featureless-processing-zero`（`PS: in` → `PS: 0`，工作比例變成 0）都 **SURVIVED**。我的 probe `TestZZReviewFeatureless`（手寫 line 去掉所有 capacity、calendar、breakdowns、setups 後經 engine）確認現在的程式是對的（處理＝utilization、閒置＝1−utilization、下班 0），所以這是測試缺口，不是錯誤。
- 最小修正：在 `TestModelMetricsUnavailable`（或新的測試）加一個沒有任何功能、`ResourceStates` 為 nil 的 Result（或直接把上述模型經 engine），斷言 `processing_ratio`、`idle_ratio`、`off_shift_ratio`、`work_ratio` 與 `utilization` 的關係。報告「每個公式的值逐一手算」也應涵蓋此式。

**L2 — `resource_states` 缺單一成員時的 `RESULT_MEMBER_MISSING` 沒有測試**（`metrics.go:334–362`）
- 問題：D-117 (d) 的 `RESULT_MEMBER_MISSING`（Result 缺少模型功能所意味的成員）只測了整個 resource 項目被刪的情形（「a result without its member」）。
- 失敗情境：mutant `rv-missing-member-accepted`（`get` 對 nil 成員回 `0, true`）與 `rv-overtime-missing-accepted`（略過 `overtime_ticks` 的檢查）都 **SURVIVED**：一個缺 `blocked_ticks` 的 Result（例如另一個 engine 版本，或 T-050 之後改了出現條件）會讓 `blocking_ratio` 顯示 0 而不是 unavailable——正是 §5「沒有的功能不填 0」要避免的。
- 最小修正：加兩個變體：有限 buffer 的模型但某 resource 的 `BlockedTicks` 為 nil；有 calendar 的 resource 的 `OvertimeTicks` 為 nil；斷言對應指標為 `RESULT_MEMBER_MISSING`。

**L3 — 直通率沒有測試站、FPY 有多條 pass flow 的情形沒有斷言**（`metrics.go:263–265`；`stations.go:163–172`）
- 失敗情境：mutant `rv-rty-without-tests-is-1`（沒有測試站時回 1.0，一個捏造的 100% 直通率）**SURVIVED**：「one station」變體只斷言 `line_balance`。Mutant `rv-fpy-last-pass-flow-only`（`r.pass +=` → `r.pass =`，只取最後一條 pass flow）**SURVIVED**：沒有任何 test result 有兩條 pass flow。
- 最小修正：「one station」變體加 `"rolled_throughput_yield_input": "TEST_RESULT_NOT_IDENTIFIED"`；加一個 pass 分成兩條 flow（例如 0.6／0.33，fail 0.07）的變體，斷言 `fpy_input`＝0.93。

**L4 — 多個同站別首測時的兩個行為沒有測試**（`metrics.go:271–284`；`stations.go:182–192`）
- 失敗情境：mutant `rv-line-balance-no-dedupe`（同一 resource 算多次）與 `rv-retest-any-primary`（重測歸到第一個同站別首測，不論 primary）都 **SURVIVED**；只有在 M1 的模型（同站別的多個首測）才看得出差別。
- 最小修正：併入 M1 的測試。

**L5 — 規則 1 允許任何額外的 resource，不只作業員；D-117 (b) 沒有記錄這個讀法**（`stations.go:196–208`、`rules.go:186–191`）
- 問題：§3.4 規則 1 為「恰有一個 role 與站別相符的 primary resource（另可加作業員）」。實作只數 role 相符的 requirement，其他任何 requirement（包括別站的測試機）都接受。
- 失敗情境：probe `TestZZReviewRules`：範本的 `fct` 改為同時需要 `fct_tester` 與 `ict_tester`，`pack.Validate` 回 `[]`（generic validator 可能另報 `BLOCKING_CYCLE`，但那不是 pack 的規則）。
- 最小修正：二擇一並寫進 D-117 (b)：(i) 額外的 requirement 只能是沒有站別 role 的 resource（例如 role 不在 `stationKinds` 的 role 中，或 type `human`），否則 `STATION_RESOURCE_INVALID`；(ii) 維持現狀，但在 D-117 (b) 與 TWIN_IR_SPEC §4 的說明寫明「其他 resource 不限」並加一個正向測試。

**L6 — 首測站沒有任何 test result decision 時沒有 warning**（`rules.go:218–224` 只檢查有 `result_of` 的 decision）
- 失敗情境：probe：`metrics_test.go` 的手寫 line（燒機是測試站別、沒有 test result）`pack.Validate` 回 `[]`，但 `fpy_input.burn` 與整條線的 `rolled_throughput_yield_input` 都是 `TEST_RESULT_NOT_IDENTIFIED`。使用者在編輯器看不到任何提示就失去直通率。
- 最小修正：對 `stationKind.test` 為 true、沒有 test result 的首測報 `TEST_RESULT_NOT_IDENTIFIED` warning（path 為該 process），或在 D-117 (b) 寫明不報並說明理由。

**L7 — 報告與 §11 的範本數字沒有 evidence log**（`checks/T-051_VALIDATION.md:46`；`docs/domains/ELECTRONICS_LINE.md:364`）
- 問題：10,756 片、各站工作比例、30 次換線、39.2 片、44.8 UPH 等數字在 evidence 中找不到產生它們的輸出（`grep` 只在 SimPy 對照 log 中碰巧比中無關的值）；`TestTemplateThroughTheEngine` 只鎖 digest 與幾個不等式。我以 probe 重現全部數字，數字正確，但讀者無法從 evidence 驗證。
- 最小修正：把印出這些數字的指令與輸出存成 evidence（或在測試中以 `t.Logf` 輸出並在 `-v` 的 log 中保留），報告引用該 log。

### Info

- **I1 — pack 的指標在 digest 中，但 Result 沒有 pack 的版本**：改 metrics.go 會在同一個 `engine_version`（des-go-0.5.0）下改變 digest；範本的 lock 會擋下範本，但已存的 run 無法從 `engine_version` 判斷是哪一版 pack 算的。這與既有的 DORA（`DomainMetrics`）相同，不是本切片新引入的模式；建議在 D-117 (f) 寫明「pack 的指標改變時升 engine_version 或另記 pack 版本」。
- **I2 — hook 拿到的是 live 的 Result**（`des.go:266`）：IR 有複製，Result 沒有；pack 的 bug 可以在 digest 前改 generic 部分。與 `DomainMetrics` 相同，conformance 的 `model-metrics/read-only` 與 `generic-unchanged` 會抓到；只記錄。
- **I3 — UI 標籤目前沒有使用者**：T-032 的結果頁以 key 原文顯示 pack 的指標（`metrics-table.tsx:19–22`，「data, not catalog text」），所以 `domain.electronics_line.metric.*` 尚未被讀取；pack 自己的 unavailable reason（`NO_BATCH_MODEL`、`NO_COMMON_LINE_CALENDAR` 等）也沒有 UI 訊息。比賽後與指標 catalog 的 API（報告「尚未完成」第 5 項）一起處理。
- **I4 — 報告主體的 commit 表**（`checks/T-051_VALIDATION.md:29–31`）列的是 `t051-hk` 上的原始 hash（53f2687、bf0ebe0、7775d21），整合分支上是 52a102b、4038ac6、949cae3、cce4587；coordinator 一節有對照，建議在表上註明。
- **I5 — 範本依賴 T-050 的 PROPOSED 解讀**：範本用了批次與換線，lock 的 digest 與 §11 的數字取決於 D-096／D-097（PROPOSED）的 kernel 行為；報告的 gate 列表沒有列這兩項（問題 4 只談 engine_version）。建議在 gate 或「問題」中點名。
- **I6 — `fpy_input` 以 replication 的 summary 呈現**：模型輸入也帶 `sample_count`＝replication 數與零寬度的 Student-t CI，可能讓人以為是模擬統計；key 的 `_input` 與 prompt rule 已說明，只記錄。

## 我執行的項目

環境：go1.27.3 linux/amd64（`GOTOOLCHAIN=local`）、Python 3.13（`.venv`）、PostgreSQL 16.15（共用的 cluster，socket `/var/lib/postgresql/geminus-t009-fresh`、port 55440；沒有啟動或停止它）、4 CPU（load 10–22，其他 agent 同時在跑）。HEAD 492fae6。

| # | 指令 | exit | 結果 |
|---|---|---|---|
| 1 | `go test -count=1 -v ./internal/domainpack/electronicsline/ ./internal/domainpack/conformance/ ./internal/simulation/des/ ./internal/results/metrics/ ./internal/packs/ ./internal/ai/review/ ./internal/ai/aicontext/ ./internal/model/validate/` | 0 | 8 個 package ok，212 個 `--- PASS`，34.7 s |
| 2 | `go test -count=1 -overlay <scratch>/probe.json -run TestZZReviewPrintNumbers -v ./internal/domainpack/electronicsline/`（印出 lock 設定下的全部 domain 指標） | 0 | digest 等於 lock；報告與 §11 的數字全部重現；110 個指標 |
| 3 | reviewer 的 mutant（`<scratch>/rv_mutants.py`，`go test -overlay`，`./internal/domainpack/electronicsline/ ./internal/domainpack/conformance/`） | — | 13 個：3 KILLED（`rv-blocking-first-test-only`、`rv-uph-scheduled-calendar-any-resource`、`rv-batch-fill-over-min`）、9 SURVIVED（見 L1–L4 與下表）、1 BUILD-ERROR（`rv-starved-own-ignored`，改寫後重跑） |
| 4 | `go test -count=1 -overlay <scratch>/rv-starved-own-ignored2.json ./internal/domainpack/electronicsline/` | 1 | KILLED（`TestModelMetricsUnavailable`「a shared tester」） |
| 5 | `go test -count=1 -overlay <scratch>/probe2.json -run TestZZReview -v ./internal/domainpack/electronicsline/`（L1 的無功能模型、M1 的並聯 FCT） | 0 | 無功能：處理＝utilization、閒置＝1−utilization、下班 0（程式正確）；並聯 FCT：直通率 0.793020，應為 0.852710，`pack.Validate` 回 `[]` |
| 6 | `go test -count=1 -overlay <scratch>/probe3.json -run TestZZReviewRules -v ./internal/domainpack/electronicsline/`（L5、L6） | 0 | 兩個模型 `pack.Validate` 都回 `[]` |
| 7 | `make architecture-check` | 0 | `PASS architecture: 87 packages, 23 rules, 16 build configurations`，16 s |
| 8 | `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 13 s；之後 `git status` 乾淨（重產的 `checks/*.json` 與 commit 相同） |
| 9 | `go vet` 改動的 package（domainpack/...、simulation/des、packs、runs/runner、results/metrics、ai/review、ai/aicontext、api）；`gofmt -l` 同一組 | 0 | 沒有輸出 |
| 10 | `make integration-test`（新建的 `dt_test_rv051a`；`GEMINUS_TEST_DATABASE_URL`＝`DATABASE_URL`、`DT_ALLOW_DB_TESTS=yes`；跑完 `DROP DATABASE`，確認沒有留下 `dt_test_rv051*`） | 0 | migration up／down／up smoke 完成；`db-go-test`（`-race`）35 個 package ok（含 `internal/api` 的 `TestElectronicsLineTemplateClosedLoop`、`internal/runs/runner`、`internal/worker`），沒有 skip，8 分 53 秒 |
| 11 | `go test -race -count=1 ./internal/domainpack/... ./internal/results/metrics/ ./internal/packs/` | 0 | 6 個 package ok（electronicsline 72.5 s、conformance 49.1 s），1 分 19 秒 |
| 12 | 加入本檔後再跑 `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 只有 `checks/docs-check.json` 的計數改變（markdown 118 → 119、連結 94 → 95），與本檔一起 commit（同 50770c1 的做法）；沒有其他 tracked file 改變 |

Reviewer mutant 的結果（每個只改一行；tree 不變）：

| Mutant | 檔案 | 結果 |
|---|---|---|
| `rv-featureless-idle-wrong`（沒有功能的站閒置＝100%） | metrics.go:332 | SURVIVED（L1） |
| `rv-featureless-processing-zero`（沒有功能的站工作比例＝0） | metrics.go:332 | SURVIVED（L1） |
| `rv-line-balance-no-dedupe` | metrics.go:281 | SURVIVED（L4） |
| `rv-blocking-first-test-only`（平均阻塞只看首測） | metrics.go:412 | KILLED |
| `rv-retest-any-primary` | stations.go:187 | SURVIVED（L4） |
| `rv-missing-member-accepted` | metrics.go:338–339 | SURVIVED（L2） |
| `rv-rty-without-tests-is-1` | metrics.go:263–265 | SURVIVED（L3） |
| `rv-overtime-missing-accepted` | metrics.go:354 | SURVIVED（L2） |
| `rv-fpy-last-pass-flow-only` | stations.go:170 | SURVIVED（L3） |
| `rv-uph-scheduled-calendar-any-resource` | metrics.go:215 | KILLED |
| `rv-starved-own-ignored`（改寫後） | metrics.go:515 | KILLED |
| `rv-rule3-skip-retest-without-primary`（重測自己沒有 primary 時也檢查規則 3） | rules.go:193 | SURVIVED（只會多報一個連鎖的 issue；D-117 (b) 只要求「首測自己沒有 primary 時不報」，記錄不列為 finding） |
| `rv-batch-fill-over-min` | metrics.go:489 | KILLED |

## 沒有執行的項目（NOT RUN）

- `make backend-test`（全部）、`make backend-race`（全部）、`make backend-numeric`、`make simulation-reference-test`、`make ai-eval`：作者與 coordinator 的 log 我讀過（exit code 與報告一致；coordinator 的 race 第一次是 `internal/simulation/engine` 在預設 10 分鐘逾時，以 `-timeout 30m` 重跑 ok，`exit-codes.txt` 有紀錄），我沒有重跑。
- `internal/simulation/des`、`internal/ai/review`、`internal/ai/aicontext` 的 `-race`：沒有重跑（作者與 coordinator 的 log 為 ok）；其餘改動的 package 由 #10、#11 以 `-race` 執行。
- `make frontend-test`、`make e2e-test`：依指示不執行。
- 作者的 mutation harness（`t051-mutants.py`）：讀了腳本與 4 次執行的 log，沒有重跑。
- Live AI provider：沒有呼叫，沒有讀任何 key。
- amd64 以外的硬體、SimPy 對範本組合的獨立對照（報告也列為 NOT RUN）。

## 要修的項目（H-029 (4)）

1. **M1**：直通率依站別各取一次 FPY（並聯首測 FPY 不同時 unavailable 並帶 reason），寫進 D-117 (c)(d)，加手推測試（可同時涵蓋 L4）。

Low（L1–L7）與 Info 記為比賽後的後續；L1–L3 是加測試即可的小修，若 M1 的修正順手補上更好。
