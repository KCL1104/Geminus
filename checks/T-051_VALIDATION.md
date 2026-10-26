# T-051 驗證報告：電子產線 DomainPack／full template／指標 —— **hackathon 切片**（H-029）

**日期：2026-10-26｜範圍：T-051 的 hackathon 切片（H-029、[HACKATHON_PLAN](../planning/HACKATHON_PLAN.md)「電子產線範本與指標」），不是 T-051 的全部範圍（其餘見「T-051 尚未完成的範圍」）｜狀態：實作完成，待 H-029 (4) 的一輪獨立審查與 coordinator 整合；`planning/tasks.json` 的 T-051 仍為 `not_started`（見「Planning 的狀態」）｜需求：REQ-003、REQ-004、REQ-015、REQ-022、REQ-024｜Gate：D-047、D-050、D-055、D-056、D-058、D-063（APPROVED）；本切片提出的 D-117 為 **PROPOSED**（agent 沒有批准）｜驗收測試：TEST-001、TEST-002、TEST-035（本切片涵蓋的部分）**

逐條輸出在 [`checks/evidence/2026-10-26-T-051/`](evidence/2026-10-26-T-051/)。依 coordinator 的指示：只在 `t051-hk` branch 工作、commit、不 push；先寫測試再實作（紅的 log 在 evidence 中）；沒有 migration（Result 的 `metrics.domain` 與 IR 的產線成員在 T-046 phase 2 已進 schema）；沒有新的 dependency；沒有任何 live AI 呼叫，沒有讀取任何 API key；沒有降低 assertion、沒有加 skip；沒有改其他任務的狀態或任何已批准的 decision。

## 交付內容

| 項目 | 內容 |
|---|---|
| `internal/domainpack/electronicsline` | 電子產線 pack（domain `electronics_line`）：§3.1 的 17 個 node kind、§3.2 的 11 個 resource role（測試對照 ELECTRONICS_LINE.md 的兩個表）；§3.4 的規則（`rules.go`、`stations.go`）；範本 `electronics_assembly_test_line`（`templates/`，en 與 zh-Hant）；§5 的指標（`metrics.go`，`ComputeModelMetrics`）；`InferFromEvents` 在 T-027 前回 `ErrUnavailable`；`DefaultViews` 為 `LayeredLayout`（範本自帶位置）；`PromptContext` 只含靜態 domain 知識 |
| `internal/domainpack` | optional 的 `ModelMetricsProvider`（D-058 選項 A；`DomainPack` 的 method set 不變） |
| `internal/simulation/des` | `Engine.ModelMetrics`：在 `DomainMetrics` 之後、digest 之前以 effective IR 的副本與 generic Result 呼叫，寫入 `metrics.domain`；空的答案不寫（其他 pack 的 Result 與 digest 不變，D-047）；panic 為 `ErrInternal` |
| `internal/results/metrics` | `Summarize`：Result 統計共用的 summary（mean、Type 7、Student-t CI），供 pack 使用 |
| `internal/runs/runner`、`internal/packs` | runner 對有 `ModelMetricsProvider` 的 pack 接上；deployment 註冊電子產線 pack 與 software delivery 範例 pack |
| `internal/domainpack/conformance` | `model-metrics/*` 步驟（不改 IR 與 Result、key 非空、Result 通過 schema、generic 部分不變、經 engine 與直接呼叫相同、可重現），並納入 stateless 與並行檢查；10 個破壞單一性質的 pack 各被抓到 |
| Contract | `common.schema.json` 的 `ValidationCode` 加 4 個 pack code；`http.schema.json` 的 `CreateModel` 改為每個 pack 一個 `oneOf` 分支（3 個新 fixture、cases.json）；OpenAPI `createModel` 的 request 範例（兩個範本），`scripts/check_contracts.py` 改為也驗 request 範例；TS 型別重產（`CreateModel` 為 union）與 TS contract 測試 |
| UI catalog | `apps/web/messages` 的 en、zh-Hant：4 個 validation code 的訊息，`domain.electronics_line.*` 的 kind 與指標名稱（Go 測試對照 pack 的 catalog） |
| AI | `internal/ai/review` 的 4 個固定句；AI context 與 server review 對範本的測試（不改 builder） |
| 文件 | ELECTRONICS_LINE §11（實作紀錄）、TWIN_IR_SPEC §4 的 code 表、SIMULATION_SPEC §5、§9、ARCHITECTURE §3、DECISIONS_AND_TBD 與 `planning/decisions.json` 的 D-117（PROPOSED）、`planning/tasks.json` 的 T-051 |
| 其他 | `scripts/check_no_fma.sh` 的 FMA 檢查涵蓋 `internal/domainpack`（pack 的指標在 digest 中） |

## Commit

`t051-hk` branch（從 6041d39 開出），依主題分開：

| Commit | 內容 |
|---|---|
| 53f2687 | Contract 先行：`ValidationCode` 的 4 個 pack code、`CreateModel` 的 `oneOf`、3 個 fixture 與 cases.json、OpenAPI 的 request 範例與其檢查、TS 型別重產與 TS 測試、UI catalog |
| bf0ebe0 | `ModelMetricsProvider`、`des.Engine.ModelMetrics`、`metrics.Summarize`、conformance 的 `model-metrics/*`、runner 的接線、FMA 檢查涵蓋 `internal/domainpack`，與它們的測試 |
| 7775d21 | 電子產線 pack（catalog、規則、範本、指標）與其測試、regression lock；`internal/packs` 的註冊；review 的固定句；AI context、review 與 API closed loop 對範本的測試 |
| （最後一個） | 文件（ELECTRONICS_LINE §11、TWIN_IR_SPEC、SIMULATION_SPEC、ARCHITECTURE）、D-117（PROPOSED）、`planning/` 與 `checks/*.json`、本報告與 evidence |

紅的 log 是在各實作之前的工作狀態取得的（先寫的測試、尚未存在的實作），不是各 commit 的狀態；中間的 commit 是依主題分開的檢視單位，沒有各自跑一次全部驗證（例如 53f2687 已有新的 `ValidationCode`，而宣告它們的 `codes.go` 在 7775d21）。下面「實際執行結果」的指令都在最後的程式碼上執行。

## 範本：`electronics_assembly_test_line`

完整的值、來源與 2D 位置見 [ELECTRONICS_LINE §11](../docs/domains/ELECTRONICS_LINE.md#11-t-051-的實作hackathon-切片2026-10-26h-029)。全部參數是 **synthetic, for demonstration**：取自已批准的 ELECTRONICS_LINE §4.1、§4.2、§4.4（H-018）的示意值，不是任何工廠的實測值或「典型值」；範本的 `metadata` 記 `synthetic: true`、`source: "synthetic, for demonstration"`、來源與 8 條假設，由範本建立的 model 保留這些標記（但不是 fixture）。

- **步驟（11 個 process）**：SMT 線（印刷、SPI、置件、迴焊視為一個 server，§2）→ AOI →（不良 3%：修補）→ ICT →（不良 5%：除錯 → 重測，或報廢）→ FCT →（不良 7%：除錯 → 重測，或報廢）→ 燒機（批次 20–40 片、等待上限 120 分、每批 240 分）→（不良 0.5%：回 FCT 除錯）→ 包裝出貨。另有 11 個 buffer（`q_aoi` 20、`q_ict` 10、`q_fct` 10、`q_burnin` 120、`q_pack` 400，其餘無限）、8 個 decision、4 個 failure、3 個 rework、出貨與報廢兩個 sink、兩個 source。
- **Resource（7 個）**：`smt_line` 1、`aoi_machine` 1、`ict_tester` 2、`fct_tester` 3、`burn_in_chamber` 6（24/7）、`debug_operator` 4、`pack_station` 2；前五個有 exponential TTF／triangular TTR 的故障（上班時間計，D-051）。
- **兩個產品族**：`board_a` 平日 06:00、`board_b` 平日 14:00 各整批 360 片（table 投入，D-053）；SMT 線 A↔B 換線 tri(15, 25, 45) 分（D-055）。
- **班表**：週一至週五 06:00–14:00 `early`、14:00–22:00 `late`；除錯與包裝列出等於 capacity 的每班人數（D-117 (g)）。
- **Primitive**：`validate.Uses` 為 batch、breakdowns、calendars、finite_buffer、release_schedule、setups；沒有宣告式 metrics。

**以引擎確認 §4.2 的粗估**（regression lock 的設定：horizon 28 天、warmup 7 天、5 個 replication、seed 2026）：21 天的量測視窗平均出貨 10,756 片（投入 10,800，WIP 未累積），上班時間的工作比例 FCT 0.898 最高（§4.2 預期的瓶頸），ICT 0.843、SMT 0.825、AOI 0.701、包裝 0.411、燒機 0.365；SMT 換線 21 天 30 次（每天約 2 次）；燒機平均批量 39.2 片；UPH（排班小時）44.8（上班時投入 45／小時）。這些是 synthetic 模型的模擬輸出；參數沒有因此調整。

## 指標（`metrics.domain`，ELECTRONICS_LINE §5、D-058）

線的 key 為名稱，站的 key 為 `<名稱>.<首測的 node ID>`；每個 replication 一個值，以 `metrics.Summarize` 彙整（population replication、mean、Type 7 p50／p95、Student-t CI），任一 replication 沒有值時整個指標 unavailable 並帶該 replication 的 reason（D-117 (d)）。範本共有 110 個 domain 指標（6 個線的、104 個站的；`TestTemplateThroughTheEngine` 要求數量恰為 catalog 推得的數量）。

| 可用（範本中） | 範本中 unavailable 及其 reason |
|---|---|
| `uph`、`uph_scheduled`、`line_balance`、`rolled_throughput_yield_input`；各站 `processing_ratio`、`blocking_ratio`、`idle_ratio`、`off_shift_ratio`、`work_ratio`；有故障的站 `down_ratio`、`availability`；SMT 的 `setup_ratio`、`setups_started`；有 calendar 的站 `scheduled_utilization`、`overtime_minutes`；測試站 `fpy_input`（模型輸入）；專用單一 resource 的站 `starved_ratio`；燒機的 `batches_started`、`mean_batch_size`、`batch_fill_ratio`；有完整阻塞的站 `mean_blocking_minutes` | **本 build 做不到**：`line_wip`（`WIP_NOT_DECLARED`）、`scrap_rate`（`SINK_COUNT_NOT_DECLARED`）、測試站的 `simulated_first_pass_ratio` 與 ICT、FCT 的 `retest_ratio`（`FLOW_COUNT_NOT_DECLARED`）：需要 D-058 的宣告式 metrics，validator 與 engine 仍以 `PRIMITIVE_NOT_SUPPORTED` 拒絕宣告（T-051 其餘範圍）。**模型沒有該功能**：非 SMT 站的換線（`NO_SETUP_MODEL`）、包裝站的停機與 availability（`NO_BREAKDOWN_MODEL`）、非批次站的批次指標（`NO_BATCH_MODEL`）、燒機的缺料（`STARVATION_UNDEFINED_BATCH`，以 `idle_ratio` 顯示同一公式）、沒有完整阻塞的站（`NO_COMPLETED_BLOCK_IN_WINDOW`，例如包裝站不會被擋）。沒有 calendar 的燒機不列 `scheduled_utilization`、`overtime_minutes`（§5「不另列」） |

其他 reason（手推測試逐一觸發）：`NO_COMPLETED_SINK`、`NO_COMMON_LINE_CALENDAR`（沒有或兩個以上的線班表）、`NO_SCHEDULED_TIME_IN_WINDOW`、`NO_FINITE_BUFFERS`、`STARVATION_UNDEFINED_SHARED_RESOURCE`（共用或多 resource）、`STARVATION_UNDEFINED_MULTI_UNIT`、`TOO_FEW_STATIONS`、`NO_PROCESSING_IN_WINDOW`、`STATION_ROLE_MISSING`、`TEST_RESULT_NOT_IDENTIFIED`（沒有測試結果或 fail flow 不是恰好一條）、`NO_BATCH_IN_WINDOW`、`RESULT_MEMBER_MISSING`。P0 不計算 OEE；任何指標都不稱 OEE；FPY 與直通率標為模型輸入（key 的 `_input`）。

## 規則（ELECTRONICS_LINE §3.4）與 code

| 規則 | Code | 嚴重度 | 反向測試 |
|---|---|---|---|
| 1：站別 process 恰有一個 role 與站別相符的 resource（可另加作業員） | `STATION_RESOURCE_INVALID` | error | `TestRuleStationResource`：role 改掉（首測與重測各一）、換成別站的測試機、沒有 resource、兩個同 role 的 resource；加作業員通過 |
| 3：重測與首測共用 primary resource | `RETEST_RESOURCE_MISMATCH` | error | `TestRuleRetestResource`：另一台同 role 的測試機、該站沒有首測；首測本身違反規則 1 時不連鎖 |
| 2：測試結果恰有一條 flow 到 failure node | `TEST_RESULT_NOT_IDENTIFIED` | warning | `TestRuleTestResult`：fail 分支繞過 failure node、兩條到 failure node |
| §3.3：pack 能用的屬性 | `DOMAIN_ATTRIBUTES_INVALID` | warning | `TestRuleDomainAttributes`：不認得的 station、station 不是字串、不認得或缺少 pass、queue 上的 station、result_of 指向 queue／不存在／沒有流到該 decision 的站、process 上的 result_of；使用者其他的屬性不報 |
| 4：範本標 synthetic | — | 範本測試 | `TestTemplateMetadata`（範本檔與建立的 model） |
| 5：不重複 generic validator 的規則 | — | — | 範本與手推的 line 先經 `validate.IR`；review 測試確認 generic 的 `BATCH_INVALID`、`BLOCKING_CYCLE` 先於 pack |

訊息只含 pack 自己的站別與 role 名稱，不引用 model 的值；四個 code 在 `ValidationCode`、UI 兩語訊息與 AI review 的固定句（`TestEveryCodeHasASentence`、`TestCodesMatchContract` 讀各 pack 的 `codes.go`）。

## 先紅後綠

先寫的測試在實作前執行（`red-*.log`）：pack 的 package 不存在（`red-electronicsline.log`）；`domainpack.ModelMetricsProvider`、`des.Engine.ModelMetrics`、`metrics.Summarize` 未定義（`red-conformance-model-metrics.log`、`red-des-model-metrics.log`、`red-results-summarize.log`；`red-des-model-metrics.log` 的最後一行 `exit 0` 是 `tail` 的，不是 `go test` 的，前面是 build failed）；registry 只有一個 pack、contract 的範本與 registry 不符（`red-packs.log`）；review 缺 4 個固定句（`red-review-sentences.log`）；沒有 pack 在 `codes.go` 宣告 code（`red-validate-codes.log`）；TS 舊的型別拒絕電子產線的 `CreateModel`（`red-frontend-typecheck.log`，tsc exit 2）、catalog 缺 4 個 validation code（`red-frontend-unit.log`）。實作後第一次執行只有 regression lock 的佔位值失敗（`first-run-electronicsline-lock-placeholder.log`，其他 26 個測試通過），之後才寫入 digest；兩個測試曾以非決定性的 `json.Marshal`（map 順序）比較，改為 `json.Deterministic(true)`（測試本身的錯，不是 pack 的）。`TestElectronicsLineTemplateContext`（AI context）第一次執行即通過：它是 builder 對新 pack 的特性測試，沒有對應的實作變更。`TestElectronicsLineReview` 第一次以「FCT 改用 ICT 測試機」當作違反規則 1 的 edit，被 generic 的 `BLOCKING_CYCLE` 先擋下（共用 resource 使 `q_fct` 形成循環）——validator 的順序正確，測試改為只違反 pack 規則的 edit（FCT 測試機的 role 改掉）。

## 測試涵蓋

- **手推的 oracle**（`metrics_test.go`）：一條 4 站的手寫 line 與手選的 Result 成員，每個公式的值逐一手算（UPH 9、排班 UPH 18、線平衡 57/96、SMT 的六種狀態相加為 1 等），每個 reason 各一個變體（16 個），跨 replication 的彙整（mean、p50、p95、CI）與「任一 replication 沒有值即 unavailable」，nil 輸入、沒有 replication、空視窗不 panic、不改輸入。
- **範本經 engine**（`engine_test.go`）：Result 通過 schema；pack 宣告的每個指標對每個站都在，可用或帶預期的 reason；數字與 generic Result 一致（UPH＝throughput×3.6e6／T，排班 UPH 的 L 等於 SMT 線的 `scheduled_ticks`，六種狀態相加為 1，填滿率＝平均批量／40，FPY 與直通率等於輸入）；FCT 的工作比例最高；同一 seed 同一 digest、另一 seed 不同；沒有 pack 的 engine 沒有 `metrics.domain` 且 digest 不同；digest 等於 regression lock；zh-Hant 範本的指標與 en 完全相同。
- **範本**（`template_test.go`）：完整驗證且沒有任何 issue；用到的 primitive；主線與迴圈的正機率路徑；每個參數等於 §4.2、§4.4；metadata；兩語名稱（每個 entity type、resource、node 都有兩語且不同，其餘內容相同）；names 檔的嚴格性；typed error、不回顯輸入。
- **Catalog、views、prompt**（`pack_test.go`）：catalog 等於 spec 的兩個表；副本；DOMAIN_MISMATCH；2D 位置不重疊、主線在第一列、process 依 x 再依 y 的順序即 Marey 的列順序；layered layout；prompt context 的規則。
- **UI 標籤**（`TestCataloguesHaveUILabels`）：每個 kind 與指標名稱在 en、zh-Hant catalog 都有、兩語不同，en 等於 pack 的 Label；catalog 沒有 pack 沒有的 key。
- **Conformance**：pack（範本 en、zh-Hant 與手寫 line）通過整個 suite，含新的 `model-metrics/*`；`TestModelMetricsChecks` 的 10 個破壞單一性質的 provider 各被抓到，正確的 provider 沒有 finding。
- **Engine hook**（`des/modelmetrics_test.go`）：hook 看到 effective IR 的副本（改它不影響呼叫者）、`metrics.domain` 進 digest、generic Result 不變、nil／空答案不寫 `metrics.domain` 且 digest 等於沒有 hook、panic 為 `ErrInternal`。
- **Contract 與 registry**：`TestCreateModelContractNamesTheTemplates`（schema 的 domain／template 配對恰為 deployment 的 pack 提供的範本）、TS 的 typed literal 等於 fixture、`@ts-expect-error` 的別的 domain 的範本與不存在的 domain、OpenAPI request 範例符合 schema（把範例改成別的 domain 的範本時檢查失敗，手動確認後還原）。
- **AI**：範本的 scenario 與 edit context 在預設 budget 內完整（只省略 server 綁定的 model ID 與 2D 位置，與所有 model 相同），沒有 redaction，站別屬性與 synthetic 標記送達，domain rules 是 pack 的；server review：「燒機改成每批 48 片」為 ready、risk medium（D-057，不論 AI 宣稱 low），超過 buffer 的批量為 `BATCH_INVALID`，違反 pack 規則的 edit 為 `STATION_RESOURCE_INVALID` 且只存固定句。
- **Integration**（DB）：`TestElectronicsLineTemplateClosedLoop`：editor 以 HTTP 用 zh-Hant 建立 model，draft 沒有 issue、名稱為中文、metadata 標 synthetic；別的 domain 的範本 422；version 1 的 run 經 worker（deployment 的 registry）完成，API 提供的 Result 通過 schema，`metrics.domain` 有可用的 UPH、線平衡等，`fpy_input.fct` 為 0.93，宣告式指標與燒機的缺料為 unavailable。
- **Architecture**：`make architecture-check` PASS；兩個 probe：pack import `net/http`、kernel（`des`）import pack，都被擋下（`architecture-probe-*.log`，exit 1），之後移除。

## Mutation

`evidence/2026-10-26-T-051/t051-mutants.py`：每個 mutant 以 `go test -overlay` 改一處行為（tree 不變），跑它列出的 package。31 個 mutant（rules、attributes、各公式、彙整、範本標記、engine hook、conformance、registry、review、runner 接線）。第 1 次執行（`mutants-run1.log`）把 build failed 也算成 killed；第 2 次加上區分（`mutants-run2.log`：3 個 mutant 無法 build，不算 killed）；修正那 3 個 mutant 使其可 build 後第 3 次 30/30 killed（`mutants-run3.log`）；第 4 次加上需要 PostgreSQL 的 runner 接線 mutant（只有 API closed loop 看得到），31/31 killed（`mutants-run4-with-db.log`）。

## 實際執行結果

環境：`environment.txt`（go1.27.3 linux/amd64、Python 3.13.16、SimPy 4.1.2、Node v22.22.0、PostgreSQL 16.15、4 CPU；`apps/web/node_modules` 是主 checkout 的 symlink，`package-lock.json` 相同，commit 前移除）。程式碼在 13:18 的 `gofmt -w` 之後沒有再改，下表的指令都在那之後、與 commit 7775d21 相同的程式碼上執行（文件、planning 與本報告之後才改）。

| 指令 | 結果 | Log |
|---|---|---|
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | exit 0：contract（含 3 個 OpenAPI request 範例）、planning、docs 連結、SQL inventory、reference 算術、engine reference vectors、decisions（D-117 的形狀）、TS contract 已重產、獨立 hash、DB 測試已重產，16 秒（`checks/*.json` 由它重產） | `spec-check.log` |
| `make backend-test` | exit 0：`go vet` 與 `GEMINUS_FULL_DIFFERENTIAL=1 go test`，75 個 package ok，10 分 54 秒 | `backend-test.log` |
| `make architecture-check` | exit 0：`PASS architecture: 87 packages, 23 rules, 16 build configurations`，17 秒；兩個 probe exit 1（被擋下，之後移除） | `architecture-check.log`、`architecture-probe-*.log` |
| `make backend-numeric` | exit 0：6 個 GOARCH（amd64 v3、arm64、ppc64le、s390x、riscv64、loong64）的 listing 沒有 FMA 與平台相依的 math 呼叫（涵蓋 `internal/simulation`、`internal/results`、`internal/domainpack`），之後這三組 package 以 GOAMD64=v3 build 執行測試全部 ok，259 秒 | `backend-numeric.log` |
| `make simulation-reference-test PYTHON=/home/user/Geminus/.venv/bin/python` | exit 0，27 秒 | `simulation-reference-test.log` |
| `make ai-eval` | exit 0：fake provider 的 context suite 與 review eval；live provider 為 NOT RUN（H-001、H-017），`checks/ai-evals/*.json` 沒有變化 | `ai-eval.log` |
| `make frontend-test` | exit 0：lint、typecheck、21 個 test file、551 個測試，33 秒 | `frontend-test.log` |
| `make integration-test`（新建的 `dt_test_t051a`；`DATABASE_URL`＝`GEMINUS_TEST_DATABASE_URL`、`DT_ALLOW_DB_TESTS=yes`） | exit 0，447 秒（含 migration up／down／up 的 smoke） | `integration-test.log` |
| `go test -count=1 -v -run TestElectronicsLineTemplateClosedLoop ./internal/api/`（同一 DB） | exit 0 | `api-electronics-closed-loop.log` |
| `go test -count=1 -v ./internal/domainpack/electronicsline/` | 27 個測試 PASS | `electronicsline-tests.log` |
| `go test -race -count=1`（改動的 11 個 package：`internal/domainpack`、`…/conformance`、`…/electronicsline`、`…/softwaredelivery`、`internal/simulation/des`、`internal/results/metrics`、`internal/packs`、`internal/ai/review`、`internal/ai/aicontext`、`internal/runs`、`internal/runs/runner`） | exit 0，3 分 05 秒 | `race-changed-packages.log` |
| Mutation（`t051-mutants.py`，含 DB 的 mutant） | 31/31 killed | `mutants-run4-with-db.log` |

`make simulation-reference-test` 的 SimPy 對照沒有涵蓋範本的組合（批次＋有限 buffer＋班表＋故障＋換線同時出現）；它對照的是各 primitive 既有的 reference 模型（Makefile 的說明：批次與換線的對照不含有限 buffer 與班表）。範本的數字只由本 repo 的 engine 與手推的 oracle（`metrics_test.go`）檢查，沒有獨立的 reference 實作。

## 與 spec／指示的差異

- **D-117（PROPOSED）**：批准文字沒有寫到的細節（識別、code 與嚴重度、站別、指標 key 與彙整、unavailable 的指標、engine 的 hook、範本的設定、UI 標籤）都記在 D-117 的 (a)–(h)，含「不採用時要改的地方」。agent 沒有批准；`planning/decisions.json` 的 D-117 為 `proposed`，`approved_by`、`approved_at` 為 null。
- **SMT 線是一個 server**：依 ELECTRONICS_LINE §2 與 §9，印刷、SPI、置件、迴焊沒有拆成四個 process，只寫在 node 名稱中。指示寫「SMT line (printer, SPI, pick-and-place, reflow)」——若 owner 要四個獨立的 process，需要改 spec 與範本（見「問題」）。
- **§4.4 的 lognormal TTR 沒有採用**：§4.4 寫「可改用」；範本保留 §4.2 的 triangular，使 §4.2 的產能粗估仍可對照（D-117 (g)）。
- **§4.2 與 §4.4 合為一個範本**：指示要求兩個產品族、release schedule、換線、班表與故障同時出現，所以範本是 §4.1 拓樸＋§4.2 參數＋§4.4 的兩族與換線；§4.3 的 rack 變體沒有做。
- **班表拆成兩個有 label 的 shift**：`early`、`late`，人數等於 capacity，數字與 §4.2 相同（D-117 (g)）。
- **Yield／first-pass**：指示要求「只在 spec 與 engine 支援時」；FPY 與直通率以 §5 的「模型輸入」提供（`fpy_input`、`rolled_throughput_yield_input`），模擬得到的首次通過率需要宣告式 flow count，標為 unavailable（`FLOW_COUNT_NOT_DECLARED`）。沒有任何指標稱 OEE。

## Planning 的狀態

指示要把 `planning/tasks.json` 的 T-051 設為 `in_progress`，但 `scripts/check_decisions.py` 拒絕：T-051 依賴 T-050，而 T-050 在本 branch 的 base（6041d39）仍為 `not_started`（「T-051: started before its dependencies」，`tasks-in-progress-refused.log`）。為了不讓 `make spec-check` 失敗、也不改別的任務的狀態，T-051 保留 `not_started`；其 `decision_gates` 加入 D-117，`planned_outputs`、`spec_refs`、`verification_commands` 與 `completion_evidence`（本報告）已更新。Coordinator 把 T-050 合併並設為 `review`／`done` 之後，再把 T-051 設為 `in_progress`。

## T-051 尚未完成的範圍

本切片之外、T-051 的完整範圍仍要做的：

1. **D-058 的宣告式 metrics**：IR 的 `metrics` 宣告（`system_wip`、`flow_count`、`sink_count`、`period_throughput`）仍被 validator 與 engine 以 `PRIMITIVE_NOT_SUPPORTED` 擋下。要做：validator 的規則（`DUPLICATE_ID`、`UNKNOWN_REFERENCE`、period 的上限）、kernel 的 flow 計數、Result 的 `additional_metrics`／declared 成員、解除 gate、`engine_version` 的升級與鎖的重產、各 primitive 的測試與 golden、D-057 的 risk 分類、AI context 的納入。
2. 上面完成後，`line_wip`、`scrap_rate`、`simulated_first_pass_ratio`、`retest_ratio` 由 unavailable 改為可用，範本加入對應的宣告。
3. ELECTRONICS_LINE §4.3 的 rack（燒機架）變體範本。
4. §7 的觀測值對照（T-052 的範圍）；`InferFromEvents`（T-027 前回 `ErrUnavailable`）。
5. H-029 (4) 的一輪獨立審查；指標 catalog 的 API（T-032 讀取指標名稱與單位的方式）。

## NOT RUN

- 完整的 `make backend-race`（只跑了改動的 11 個 package，見上表）。
- `make e2e-test`（沒有改 UI 元件，只改 message catalog 與 contract 型別；本切片沒有 browser 流程的測試）。
- Live AI provider（沒有批准的 model、quota 與 data policy，H-001、H-017；沒有讀取任何 API key）。
- 範本組合的 SimPy 獨立對照（見上）。
- amd64 以外的實機（FMA 檢查只檢查各 GOARCH 的組合語言 listing，不是在那些硬體上執行）。

## 問題（給 owner／coordinator）

1. SMT 線要維持 §2 的一個 server，還是拆成印刷、SPI、置件、迴焊四個 process（需改 ELECTRONICS_LINE §2、§4 與範本、重產鎖）？
2. D-117 的 (a)–(h) 是否採用；特別是 (b) 規則的嚴重度（建議 A：規則 1、3 為 error）與 (d) 指標 key 的格式（T-032 會讀）。
3. T-051 的狀態：T-050 進入 `review`／`done` 後由 coordinator 設為 `in_progress`？
4. 若 T-050 的審查使 `engine_version` 由 des-go-0.5.0 升級，`testdata/template-run-lock.json` 的 digest 要重新產生（它是 regression lock，不是 oracle）。
5. D-117 的編號：其他並行的 agent 也可能提出新的 D 編號，合併時若衝突需重新編號（`planning/decisions.json`、DECISIONS_AND_TBD、ELECTRONICS_LINE §11、`planning/tasks.json` 與本報告都引用 D-117）。

## Coordinator 驗證（T-051＋T-032 合併後，2026-10-26）

T-051 的四個 commit（52a102b..cce4587）與 T-032 的五個 commit（f21b02a..cae974b）cherry-pick 到 T-015 已整合的分支上（55d0703 之後）。衝突：`apps/web-e2e/tests/helpers.ts` 的 routes（兩邊都保留：T-015 的 `/ui-gallery/editor` 與 T-032 的 scenario、results 與 review 頁）、`DESIGN.md`（T-015 與 T-032 各自新增的簡化條目都保留）、生成的 `checks/*.json`（以 `make spec-check` 重產）。

合併後出現一個語意衝突：T-032 把 `tests/tokens.test.ts`「自家 CSS 不載入任何東西」的掃描擴大到 `features/`，掃到 T-015 的 `features/model-editor/vendor.css` 對 React Flow `base.css` 的一個 `@import`（由 node_modules 打包，不是遠端或字型載入）。規則維持一樣嚴格、改為精確：每個自家樣式表只能含列出的套件 import，而被 import 的套件檔本身不得有 `@font-face`、`@import` 或 `url(`（8eecee4）。Mutant：多加一個字型服務的 import、改 import 另一個套件檔，測試都失敗。

指令與結果（evidence：`checks/evidence/2026-10-26-T-051-T-032-coordinator/`，`exit-codes.txt` 依執行順序；Node 22.22.0，go1.27.3）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test GOFLAGS=-count=1` | 0 | 75 個 package 都 ok |
| `make architecture-check` | 0 | 87 packages、23 rules、16 build configurations |
| `make backend-numeric` | 0 | |
| `make simulation-reference-test` | 0 | |
| `make ai-eval` | 0 | fake provider；live 為 NOT RUN |
| `go test -race -count=1`（simulation、results、domainpack、model、cmd） | 1 → 0 | 第一次只有 `internal/simulation/engine` 在 Go 預設的 10 分鐘逾時（腳本漏了 `make backend-race` 的 `-timeout 30m`；共用的 4 CPU，load 9–11）；其餘 19 個 package ok。以 `-timeout 30m` 重跑 engine：ok，604.8 s（`race-engine-30m.log`） |
| `make integration-test`（新的 dt_test_hk5132c，跑完刪除） | 0 | |
| `npm ci`（web、web-e2e，`--engine-strict=false`） | 0／0 | |
| `make frontend-test` | 2 → 0 | 第一次是上面的語意衝突（769／770）；修正後 770／770 |
| `make frontend-build` | 0 | |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| `make e2e-test` | 2 → 0 | 第一次是另一個 agent 的 e2e 占用 127.0.0.1:3101（本機沒有 `ss`，原本的 port 檢查沒有作用，改用 `/dev/tcp` 與 `pgrep`）；等 port 空出後單獨重跑：257 passed、32 skipped（全部是 `screenshots.spec.ts`，只在 CI 的固定環境比對） |

NOT RUN：截圖比對（只在 CI）；完整的 `make backend-race`（只跑改動的 package）；live AI；Node 24。

Coordinator 對 T-051 問題的處理（例行事項；需要 owner 的另列）：
1. SMT 線維持 ELECTRONICS_LINE §2 的一個 server（照 spec；拆成四個 process 要改 spec，不在切片內）。
2. D-117 維持 PROPOSED，比賽後放進決策頁（與 D-096、D-097、D-110–D-116 同一頁）；切片依各項的建議選項實作。
3. T-051 維持 `not_started`：T-050 仍為 `in_progress`（等 D-096、D-097），checker 不允許相依未進 review／done 的任務 in_progress。
4. `engine_version` 若因 T-050 的決定升級，重產 `testdata/template-run-lock.json`（regression lock）。
5. D-117 的編號沒有衝突（`make spec-check` 的 decisions 檢查通過）。

下一步：一輪獨立審查（H-029）。

## Review round 1 fixes（M1 與 L4，2026-10-26）

**審查：** 獨立審查第 1 輪（`checks/T-051_REVIEW_r1.md`，在 reviewer 的 branch 上；結論 CHANGES REQUESTED：1 個 Medium、沒有 High、7 個 Low）。**範圍：** 只修 M1，並依審查的建議在同一個測試檔補上 L4 的測試；其餘 Low 為比賽後的後續（見最後一小節）。**Branch：** `t051-m1`（從 d229225 開出），commit、不 push。**Evidence：** [`checks/evidence/2026-10-26-T-051-r1/fixes/`](evidence/2026-10-26-T-051-r1/fixes/)，`exit-codes.txt` 依執行順序記錄每個指令的 exit code。D-117 仍為 **PROPOSED**（`approved_by`／`approved_at` 為 null，agent 沒有批准）；ELECTRONICS_LINE §5（APPROVED）沒有改。

| Commit | 內容 |
|---|---|
| 6547935 | `metrics.go` 的 `rolledYield` 與 reason `PARALLEL_TEST_YIELDS_DIFFER`；`metrics_test.go` 的 `TestRolledYieldTakesEachTestTypeOnce`、`TestParallelFirstTestsHandDerived` 與「one station」變體的一個新斷言 |
| （本節所在的 commit） | D-117 (c)(d)（DECISIONS_AND_TBD、`planning/decisions.json`）、ELECTRONICS_LINE §11、本節、evidence、`make spec-check` 重產的 `checks/*.json` |

### Finding → 修改 → 測試 → mutant

| Finding | 修改 | 測試 | Mutant（在 `git archive` 的副本中，見下） |
|---|---|---|---|
| **M1**（Medium）：直通率對每個 `pass: first` 的測試 process 各乘一次 FPY；並聯的 FCT cell 使 FCT 的 FPY 被乘兩次（0.79302032325，應為 0.852710025），pack 的規則沒有任何 issue | `rolledYield` 依線的順序（`stationKinds`：AOI、ICT、FCT、燒機）對模型中有首測的每個**測試站別**乘一次 FPY。同一站別的多個首測是並聯的 cell：FPY 在 IR 的 routing 容差內（`validate.ProbabilityEpsilon`＝1e-9，TWIN_IR_SPEC §4）相同時，該站別取 node 順序第一個首測的值；不同時，該站別的 FPY 要以各 cell 的 routing 份額加權，pack 不推導份額，直通率 unavailable，reason **`PARALLEL_TEST_YIELDS_DIFFER`**（pack 自己的 reason，與 `NO_BATCH_MODEL` 等同列，如 §5 的其他 reason 為暫定）；任一 cell 沒有 FPY 時該站別也沒有（`TEST_RESULT_NOT_IDENTIFIED`，先於比較）；reason 取線的順序中第一個沒有單一 FPY 的站別；沒有任何測試站時仍為 `TEST_RESULT_NOT_IDENTIFIED`。`fpy_input.<站>` 不變（每個首測一個） | `TestRolledYieldTakesEachTestTypeOnce`：範本加 `fct_split`（ICT 首測與重測的通過件各 0.5 分到 `q_fct` 與新的 `q_fct_b`）、`fct_b`（FCT 首測，共用 `fct_tester`）與 `fct_b_result`（`result_of: fct_b`），先經 `validate.IR`（valid）與 `pack.Validate`，再經 engine（7 天、warmup 1 天、1 個 replication、seed 3）。6 個變體：同為 0.93 → **0.852710025**；pass 分成 0.6＋0.33（float64 為 0.9299999999999999，容差內相同）→ 0.852710025；相差 5e-10（容差內）→ 0.852710025（取 `fct` 的 0.93，不是 `fct_b` 的）；0.92 → `PARALLEL_TEST_YIELDS_DIFFER`；相差 1e-6 → `PARALLEL_TEST_YIELDS_DIFFER`；`fct_b_result` 的不良繞過 failure node（規則 2 的 warning）→ `fpy_input.fct_b` 與直通率 `TEST_RESULT_NOT_IDENTIFIED`。每個變體都斷言 AOI 0.97、ICT 0.95、FCT 0.93、燒機 0.995 的 `fpy_input` 與 `fpy_input.fct_b`。`TestModelMetricsUnavailable` 的「one station」變體加一個斷言：沒有測試站時直通率為 `TEST_RESULT_NOT_IDENTIFIED`（不是 1；改寫後的計數方式不同，需要這個防護） | 本次的 7 個 M1 mutant 全部 KILLED：`m1-one-factor-per-first-test`（還原為修正前的函式）、`m1-exact-comparison`、`m1-wide-tolerance`（1e-5）、`m1-differing-yields-accepted`、`m1-type-yield-last-cell`、`m1-missing-cell-skipped`、`m1-empty-type-counted` |
| **L4**（Low，依審查併入 M1 的測試）：line balance 每個 resource 一次、重測歸 primary 相同的首測，沒有測試 | 沒有程式變更（行為原本就正確，是測試缺口） | `TestParallelFirstTestsHandDerived`（手推，手寫 line 加第二個 FCT cell）：**共用測試機**時 line balance 仍為 57/96（w＝4/6、5/12、6/24、3/12；重複計算會得到 mean(4/6, 5/12, 5/12, 6/24, 3/12)／(4/6)＝3/5），重測歸 node 順序第一個 primary 相同的首測（`fct`：平均阻塞 4 分；`fct_b` 自己的 2 分），兩站的 starved 為 `STARVATION_UNDEFINED_SHARED_RESOURCE`；**自己的測試機 `tester_b`**（C 1、手選的積分）時 line balance 為 5/8（w＝4/6、5/12、3/6、6/24、3/12，mean 25/60 除以 2/3），重測歸 `fct_b`（雖然 `fct` 在 node 順序上是第一個 FCT 首測）：`retest_ratio.fct_b` 有、`retest_ratio.fct` 沒有，`mean_blocking_minutes.fct_b`＝(120,000＋240,000)／2 tick＝3 分，`starved_ratio.fct_b`＝2.8e6／12e6，以及 `tester_b` 的處理、阻塞、閒置、下班、scheduled utilization、overtime、work ratio。`TestRolledYieldTakesEachTestTypeOnce` 另以 engine 的 Result 確認共用測試機的兩個 cell 在 line balance 中只算一次（六項的 mean／max） | `rv-line-balance-no-dedupe` **KILLED**（兩個測試）、`rv-retest-any-primary` **KILLED**（`TestParallelFirstTestsHandDerived`） |

手算：0.97·0.95＝0.9215，·0.93＝0.856995，·0.995＝**0.852710025**；修正前的 0.97·0.95·0.93·0.93·0.995＝0.79302032325（reviewer 的 probe 印出的 0.793020）。

**設計上的限制（寫入 D-117 (c)）：** pack 不分析 routing，所以同一站別的兩個首測若其實是串聯（不是 ELECTRONICS_LINE §2 的結構），也當作並聯 cell：FPY 相同時只乘一次（偏高），不同時 unavailable。並聯 cell 的 FPY 不同時 `pack.Validate` 沒有 warning（要新增 `ValidationCode`、contract、UI 兩語訊息與 AI review 的固定句，與 L6 同類），只有指標的 reason；列為後續。

### 範本與 regression lock

`internal/domainpack/electronicsline/testdata/template-run-lock.json` **沒有改**，digest 仍為 `sha256:f5fee84c…7f904`（des-go-0.5.0）。原因：範本每個測試站別只有一個首測，且首測的 node 順序（`aoi`、`ict`、`fct`、`burn_in`）就是線的順序，所以乘積的運算順序 ((((1·0.97)·0.95)·0.93)·0.995) 與修正前相同、逐位元相同；`TestTemplateThroughTheEngine` 以未改的 lock 通過（`unit.log`、`race.log`、`backend-test.log`）。`metrics.domain` 在 digest 中，所以範本的任何指標若改變，lock 測試會失敗。

### 先紅後綠

- `red-reviewer-probe-parallel-fct.log`：reviewer 的 probe（`TestZZReviewParallelFCT`，以 `go test -overlay` 載入，tree 不變）在修正前的程式（d229225）上重現：`pack issues: []`，`fpy_input.fct`＝`fpy_input.fct_b`＝0.9300，直通率 **0.793020**（串聯值 0.852710）。
- `red-new-tests.log`：最終版本的新測試對修正前的 `metrics.go`（以 overlay 換回 d229225 的檔案，log 中有其 sha256）：`TestRolledYieldTakesEachTestTypeOnce` 的 5 個變體失敗（0.79302032325、0.7930203232499999、0.793020323676355 而不是 0.852710025；0.92 與 1e-6 的變體是 available 而不是 `PARALLEL_TEST_YIELDS_DIFFER`），exit 1。「沒有可用 test result 的 cell」變體與 `TestParallelFirstTestsHandDerived` 在修正前就通過：前者在舊程式中也是 `TEST_RESULT_NOT_IDENTIFIED`，後者是 L4 的特性測試（行為原本正確）；它們能抓到錯誤由下面的 mutant 證明。
- `green-new-tests.log`、`green-reviewer-probe-parallel-fct.log`：修正後同樣的測試 exit 0；reviewer 的 probe 印出直通率 0.852710。

### Mutation

`r1-mutants.py`（evidence 中有一份）：在 `git archive 6547935` 解開到 scratch 的副本中執行，每個 mutant 以 `go test -count=1 -overlay` 改一處，跑 `./internal/domainpack/electronicsline/`、`./internal/domainpack/conformance/`；worktree 沒有被改。Build failed 不算 killed。第 1 次（`mutants-run1.log`）有 3 個 M1 mutant 因為 `validate` 的 import 沒有被使用而無法 build；改寫使其可 build 後第 2 次（`mutants.log`）：

| Mutant | Finding | 結果 |
|---|---|---|
| `rv-line-balance-no-dedupe` | L4 | **KILLED** |
| `rv-retest-any-primary` | L4 | **KILLED** |
| `m1-one-factor-per-first-test`、`m1-exact-comparison`、`m1-wide-tolerance`、`m1-differing-yields-accepted`、`m1-type-yield-last-cell`、`m1-missing-cell-skipped`、`m1-empty-type-counted` | M1 | 7 個 **KILLED** |
| `rv-rty-without-tests-is-1` | L3 | KILLED（「one station」的新斷言） |
| `rv-fpy-last-pass-flow-only` | L3 | KILLED（0.6＋0.33 的變體：只取最後一條 pass flow 時 `fpy_input.fct_b` 為 0.33） |
| `rv-blocking-first-test-only`、`rv-uph-scheduled-calendar-any-resource`、`rv-starved-own-ignored`（reviewer 改寫後的版本）、`rv-batch-fill-over-min` | — | KILLED（審查時已 KILLED） |
| `rv-featureless-idle-wrong`、`rv-featureless-processing-zero` | L1 | SURVIVED（延後） |
| `rv-missing-member-accepted`、`rv-overtime-missing-accepted` | L2 | SURVIVED（延後） |
| `rv-rule3-skip-retest-without-primary` | — | SURVIVED（審查記錄為不列 finding：只會多報一個連鎖的 issue） |

合計 15/20 KILLED；存活的 5 個都是延後的 L1、L2 與審查不列為 finding 的那一個。

### 執行的指令

環境：go1.27.3 linux/amd64（`GOTOOLCHAIN=local`）、Python 3.13（`.venv`）、PostgreSQL 16.15（共用的 cluster，socket `/var/lib/postgresql/geminus-t009-fresh`、port 55440；沒有啟動或停止它）、4 CPU（其他 agent 同時在跑）。除 mutation 外都在 worktree 中、6547935 的程式碼上執行（之後只改了文件、planning、本報告與 evidence）。

| # | 指令 | exit | 結果 | Log |
|---|---|---|---|---|
| 1 | reviewer 的 probe，修正前（d229225，`go test -overlay`） | 0 | 重現 M1：直通率 0.793020、`pack issues: []` | `red-reviewer-probe-parallel-fct.log` |
| 2 | 新測試對修正前的 `metrics.go`（`go test -overlay`，`-run 'TestRolledYieldTakesEachTestTypeOnce\|TestParallelFirstTestsHandDerived'`） | 1 | 紅：M1 的 5 個變體失敗（見「先紅後綠」） | `red-new-tests.log` |
| 3 | 同上，6547935 | 0 | 綠 | `green-new-tests.log` |
| 4 | reviewer 的 probe，6547935 | 0 | 直通率 0.852710 | `green-reviewer-probe-parallel-fct.log` |
| 5 | `gofmt -l cmd internal schemas scripts prompts` | 0 | 沒有輸出 | `gofmt.log` |
| 6 | `go vet ./internal/domainpack/... ./internal/results/... ./internal/packs/...` | 0 | 沒有輸出 | `vet.log` |
| 7 | `go test -count=1 -v ./internal/domainpack/... ./internal/results/... ./internal/packs/...` | 0 | 7 個 package ok，沒有 skip（含 `TestTemplateThroughTheEngine` 以未改的 lock 通過），12 s | `unit.log` |
| 8 | `go test -race -count=1`（同上 3 組） | 0 | 7 個 package ok（electronicsline 86.3 s、conformance 66.7 s），1 分 39 秒 | `race.log` |
| 9 | `make architecture-check` | 0 | `PASS architecture: 89 packages, 24 rules, 16 build configurations`；pack 只多 import `internal/model/validate`（本來就在 `stations.go` 中），generic engine 沒有變 | `architecture-check.log` |
| 10 | `env GOFLAGS=-count=1 make backend-test` | 0 | gofmt、`go vet`、`GEMINUS_FULL_DIFFERENTIAL=1 go test`：77 個 package ok，10 分 49 秒（load 18–24） | `backend-test.log` |
| 11 | `make integration-test`（新建的 `dt_test_t051m1a`；`DATABASE_URL`＝`GEMINUS_TEST_DATABASE_URL`、`DT_ALLOW_DB_TESTS=yes`；跑完 `DROP DATABASE`，確認沒有留下 `dt_test_t051m1*`） | 0 | migration 與 SQL 測試、`db-go-test`（`-race`、`GEMINUS_REQUIRE_DB_TESTS=1`）37 個 package ok（含 `internal/api` 的 `TestElectronicsLineTemplateClosedLoop`、`internal/runs/runner`、`internal/worker`），沒有 skip，6 分鐘 | `integration-test.log` |
| 12 | `make backend-numeric` | 0 | 6 個 GOARCH 沒有 FMA 與平台相依的 math 呼叫（涵蓋 `internal/domainpack`），GOAMD64=v3 的 numeric 測試 ok，8 分鐘 | `backend-numeric.log` |
| 13 | mutation（`r1-mutants.py`，`git archive` 的副本） | — | 第 1 次 3 個 BUILD-ERROR；第 2 次 15/20 KILLED（見上表） | `mutants-run1.log`、`mutants.log` |
| 14 | `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python`（文件與本節之後） | 0 | contract、planning、docs、decisions（D-117 仍為 proposed、沒有 approver）等全部通過；重產的 `checks/docs-check.json` 由 115 個 markdown／106 個連結變為 121／114：base（d229225）的樹本身就是 121／112（T-039 的 commit 沒有重產這個檔），本節與 §11 各多一個連結 | `spec-check-1.log`、`spec-check-2.log` |

### NOT RUN

- 完整的 `make backend-race`：只對 `./internal/domainpack/... ./internal/results/... ./internal/packs/...` 跑 `-race`（`race.log`）；`make integration-test` 的 `db-go-test` 另以 `-race` 跑 DB 的 package。
- `make simulation-reference-test`、`make ai-eval`：本次只改 pack 的直通率，沒有改 engine、reference 或 AI 的程式，沒有重跑。
- `make frontend-test`、`make e2e-test`：依指示不執行（沒有改 UI 或 message catalog；新的 reason 跟其他 pack 的 reason 一樣只是 Result 的資料，T-032 的結果頁以原文顯示）。
- Live AI provider：沒有呼叫，沒有讀任何 key 或 secret。
- 範本組合的 SimPy 獨立對照、amd64 以外的硬體（與原報告相同）。

### 延後的 Low（比賽後，H-029）

- **L1**：沒有任何功能的 resource（不在 `resource_states`）的分支沒有測試；`rv-featureless-*` 兩個 mutant 仍 SURVIVED。
- **L2**：`resource_states` 缺單一成員（`blocked_ticks`、`overtime_ticks`）時的 `RESULT_MEMBER_MISSING` 沒有測試；兩個 mutant 仍 SURVIVED。
- **L3**：沒有作為獨立項目修。M1 需要的兩個斷言（「one station」沒有測試站時直通率 unavailable；pass 分成兩條 flow 時 `fpy_input`＝0.93）正好是審查建議的內容，兩個 L3 mutant 因此都 KILLED；是否視為已涵蓋由下一輪審查判斷。
- **L5**：規則 1 允許任何額外的 resource（D-117 (b) 要二擇一並寫明）。
- **L6**：首測站沒有 test result decision 時沒有 warning（並聯 cell 的 FPY 不同時沒有 validation warning 屬同一類）。
- **L7**：報告與 §11 的範本數字沒有 evidence log。
- Info I1–I6 沒有處理。

### Coordinator 驗證（review 第 1 輪修正，2026-10-26）

`t051-m1` 的兩個 commit（6547935、4443410）cherry-pick 到 f60fedf 之後，與 T-015／T-032 的修正與 T-052 切片一起驗證；只有生成的 `checks/docs-check.json` 衝突（重產）。

指令與結果（evidence：`checks/evidence/2026-10-26-T-051-T-052-T-015-T-032-coordinator/`，`exit-codes.txt` 依執行順序；HEAD 312ea90；Node 22.22.0，go1.27.3）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test GOFLAGS=-count=1` | 0 | 79 個 package ok |
| `make architecture-check` | 0 | |
| `make backend-numeric` | 0 | |
| `make simulation-reference-test` | 0 | |
| `make ai-eval` | 0 | fake provider |
| `go run ./scripts/gen-electronics-line-data -check` | 0 | 檔案與 generator 的輸出相同 |
| `go test -race -count=1 -timeout 30m`（domainpack、results、import、audit、cmd/worker、generator） | 0 | 16 個 package ok |
| `make integration-test`（新的 dt_test_hkb3c，跑完刪除） | 0 | |
| `npm ci`（web、web-e2e） | 0／0 | |
| `make frontend-test` | 0 | 985／985 |
| `make frontend-build` | 0 | |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| `make e2e-test`（單獨執行） | 0 | 490 passed、32 skipped（全部是只在 CI 比對的截圖） |

NOT RUN：截圖比對（只在 CI）；完整的 `make backend-race`；live AI；Node 24。

M1 修正並經 coordinator 驗證，依 H-029 的比賽模式，**T-051 的 hackathon 切片完成**；Low（L1–L3、L5–L7）與 Info 為比賽後的後續。D-117 仍為 PROPOSED。已知限制（D-117 (c)）：pack 不讀 routing，同一站別串聯的兩個首測會被當作並聯 cell。

