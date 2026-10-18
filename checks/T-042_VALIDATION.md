# T-042 驗證報告：Generic kernel／adapter dependency guard

**日期：2026-10-15（review 修正 2026-10-18）｜狀態：`review`（首輪獨立 review 為 CHANGES REQUESTED，已修正，等待同一 reviewer 複核；依 H-003 驗收）｜需求：REQ-003、REQ-011｜Gate：無｜驗收測試：TEST-001、TEST-002、TEST-025（後端部分）**

逐條輸出在 `checks/evidence/2026-10-15-T-042/`。範圍依 IMPLEMENTATION_PLAN：這是 test-only 的 boundary guard，**不是**第二個產品 domain。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/domainpack/conformance/conformance.go` | DomainPack conformance suite（test-only，只在 `_test.go` 中 import）。`Check(ctx, Suite)` 回傳 findings，`Run(t, Suite)` 把每個 finding 報成測試錯誤；pack 的每次呼叫都在 recover 內，panic 是 finding 而不是讓 suite 崩潰。檢查項見下表 |
| `internal/domainpack/conformance/queuepack_test.go`、`testdata/generic-queue.json` | 只存在於測試檔的 generic queue pack（`generic_queue`：客人排隊等 clerk 的 service counter，容量 2、rework 回到 queue），用與 software delivery 相同的 generic primitives、同一個 IR schema、validator、engine 與 view 資料；它的 `longest_queue_wait_minutes` 由 engine 的 queue stats 推導（available），`abandon_rate` 為 unavailable 並帶 reason |
| `internal/domainpack/conformance/broken_test.go` | 40 個各自只壞一個方法的 pack，每個都必須產生指定的 finding；另有 suite 本身的錯誤（沒有 model、model 不 valid、engine 無法執行的 model）與 `Run` 確實讓測試失敗（子 process） |
| `internal/domainpack/softwaredelivery/conformance_test.go` | P0 pack 以預設 template 與 4 個 golden model（report-example 以合成的 frozen samples 解析 empirical dataset）通過 suite |
| `scripts/check-architecture/` | `make architecture-check`：從 `go list -e -deps -test` 的 build graph 檢查 8 條遞移依賴規則（`rules.go`），違規時列出整條 import chain 與規則理由，exit 1；任何 package 無法 build 是錯誤（exit 2）；一條規則若沒有套用到任何 package 也是錯誤（除非標為 future），避免打錯字的規則什麼都不檢查 |
| `Makefile`、`.github/workflows/ci.yml` | `architecture-check` 由 NOT IMPLEMENTED 換成真實命令；CI backend job 新增此步驟；`GO_PKGS`、gofmt 與 govulncheck 涵蓋 `./scripts/...` |
| `docs/ARCHITECTURE.md` §3 | T-042 實作段落 |
| `docs/CONTRACT_BLOCKERS.md` | CB-37（見下） |

### Conformance 檢查項

| 檢查 | 內容 |
|---|---|
| `identity/*` | ID 是合法的 IR domain 值且穩定；suite 的 model 都屬於此 domain |
| `catalog/*` | node／resource catalog 非空、kind 不重複、有 label、只用 generic node types 與 human／machine／generic；回傳副本（呼叫端修改不影響 pack） |
| `validate/*` | valid model 無 error、不改 IR、決定性；其他 domain 與 nil 回 `DOMAIN_MISMATCH` error（fail closed）；**generic validator 對改名 domain 的同一 IR 回相同 issues**（kernel 不讀 domain） |
| `views/*` | `DefaultViews` 對原樣、去掉 positions、只留一個已存位置三種輸入：只回本 IR node ID 的 positions（不多不少）、不重疊、保留已存位置、不改 IR、決定性，套回 IR 後仍 valid（2D 是同一 IR 的 views，3D 由 2D 推導，沒有另一份 topology） |
| `metrics/*` | 經過 generic engine：`ComputeMetrics` 不改 Result、engine 的 generic 指標不因 pack 而變、Result（含 pack 指標）通過 schema、digest 可重現、metric key 非空；**同一 IR 改名 domain 後 engine 的數字完全相同** |
| `inference/*` | 只能回 `ErrUnavailable`（不帶結果）、typed 拒絕，或本 domain 的 valid model；不回 (nil, nil)；取消的 context 回錯誤 |
| `prompt/*` | domain 等於 ID、有 summary、無空規則、catalog 與 pack 的 catalog 相同、靜態 |
| `concurrent` | 8 個 goroutine 同時呼叫的結果與循序呼叫相同、不 panic；data race 由 `make backend-race` 檢查 |

### 架構規則（`scripts/check-architecture/rules.go`）

| 規則 | 內容 |
|---|---|
| `generic-kernel` | `internal/model`、`internal/simulation`、`internal/results`、`internal/domainpack`（介面）與 `schemas` 不依賴任何 domain pack、application layer（cmd、auth、tenant、project、storage、import、ai、audit、config）、test helpers、`testing`、網路／DB／process／plugin I/O（`net`、`net/http`、`database/sql`、`os/exec`…；`net/url`、`net/netip` 為 parser，允許）、DB driver、LLM SDK；第三方只允許 jcs、jsonschema、x/text（allowlist，新增需改此表） |
| `generic-kernel-tests` | 上述 package 的測試也不 import 具體 pack、application layer、DB 或 LLM |
| `domain-packs` | pack 只建在 kernel 上：不依賴其他 pack、application layer、test helpers、I/O、DB、LLM；可用自己的 subpackage |
| `conformance-is-generic` | conformance suite 不指名具體 pack |
| `no-test-infra-in-production` | production code 不連結 `testing`、`contracttest` 或 conformance |
| `llm-sdks-only-in-ai` | LLM SDK 只在 `internal/ai` 與 `cmd` |
| `db-only-in-storage` | DB driver／`database/sql` 只在 `internal/storage`、`internal/tenant` 與 `cmd` |
| `ai-suggests-only`（future） | `internal/ai` 不得依賴 storage、tenant、cmd、`os/exec`、DB；package 尚未建立，標為 future |

既有各 package 的 `TestDependencies`（`contracttest.OnlyDeps`）保留；本 checker 是跨整個 module、含遞移與測試依賴的集中檢查。

## 獨立 review 結果與處理（2026-10-18）

Review 由另一個 agent 在 `git archive` 的副本上以 overlay 進行（H-003），未修改 repo；首輪因額度上限中斷，額度重置後由同一 agent 續完。結論 **CHANGES REQUESTED**：checker 在 linux/amd64 上能抓到 kernel 依賴 pack、HTTP、DB driver、LLM SDK，40 個壞 pack 也都被抓到，報告數字全部可重現；但有以下缺口。reviewer 的 probe 在舊版的結果保留為 `checks/evidence/2026-10-15-T-042-review/arch-probes-before.log`、`conformance-probes-before.log`、`reviewer-extra-mutants-before.log`。

| # | 發現 | 等級 | 處理 | 證據 |
|---|---|---|---|---|
| A1 | 只讀 linux/amd64、cgo 開的 graph：`_arm64.go`、`_windows.go`、`//go:build !cgo`、自訂 tag 的檔案（含 import pack 的 kernel 測試）都看不到 | 中 | graph 改為 8 個平台 × cgo 開／關（16 個設定）的聯集；任何設定都不編譯的檔案以 `every-file-built` 報為違規；平台上不存在的 package 不是錯誤 | `TestReviewProbes`（arm64、windows、!cgo、自訂 tag、tag 後的 kernel 測試、windows-only package） |
| A2 | 只檢查 `cmd`、`internal`、`schemas`、`scripts`：`pkg/`、`tools/`、module 根目錄的違規通過 | 低中 | 預設 pattern 改為 `./...`；`go.mod` 加 `ignore ./apps/web/node_modules`（Go ≥1.25），vendored Go 不再出現在 `./...`（CB-35） | `TestReviewProbes`（pkg/、tools/、根目錄）、`TestVendoredGoIsNotChecked` |
| A3 | `import "C"` 被豁免 | 低中 | kernel 與 pack 禁止 `C`；移除 checker 對 `C`／`unsafe` 的豁免 | `TestReviewProbes`（cgo socket） |
| A4 | import 層級檢查看不到 `syscall.Socket`、`os.StartProcess`；`internal/domainpack/packs/{alpha,beta}` 被當成同一個 pack | 低 | kernel 與 pack 不得自己 import `os`、`os/signal`、`os/user`、`syscall`、`unsafe`、`runtime/cgo`、`C`、`plugin`、`golang.org/x/sys`（`forbidDirect`；目前沒有 kernel／pack package 直接使用它們）；pack 必須是 `internal/domainpack` 的直接子 package（`pack-layout`）。文件改為明說檢查的是 import 而非呼叫 | `TestReviewProbes`（syscall、os、unsafe、兩個 pack 同目錄） |
| A5 | `.test` entry 的錯誤（測試檔語法錯誤）被丟掉；`go run` 把 exit 1／2 都變成 1 | 低 | 所有 entry（含 test variant）先檢查錯誤再過濾；`main` 改為可測的 `run`，`make architecture-check` 先 build 再執行 binary | `TestBrokenBuildIsAnError`（測試檔語法錯誤、xtest、arm64-only 缺 import）、`TestRunExitCodes` |
| B1 | 「kernel 不讀 domain」只比對 valid model，且改名為 ID 加 `_other`：只對其他 domain 停用錯誤規則、或依 domain 前綴分支的 kernel mutant 都存活 | 中 | 改名為不相干的固定 domain（`zz.conformance:renamed`）；同時比對 5 個破壞單一規則的變體（未知 flow 目標、機率和、重複 ID、沒有 sink、錯誤 triangular）的完整 issue 清單 | reviewer 的 3 個 kernel mutant 與長度 mutant 現在被 suite 擋下；`PAIRS` 證明是這兩個 site 擋下的（`mutation.log`） |
| B2 | 3 個「equivalent」site 中 2 個可達：NaN 指標與只在 engine 路徑 panic 的 pack 能觸發第二個 `metrics/run` | 中（證據正確性） | 補 `nanMetric`、`panicsOnEnginePath`；該 site 不再列 equivalent；首輪標題改正為「80/82 被擋、1 equivalent、2 kernel guard」 | `TestBrokenPacksAreCaught` |
| B3 | 123 字元 ID 的正確 pack 因 suite 自己改名超過 128 字元而得到 finding | 低 | 固定的改名 domain 與 ID 長度無關 | `longID` 正確 pack 0 finding |
| B4 | pack 可把看過的 model 名稱放進 `PromptContext` 而通過 `prompt/static` | 中低 | 在 pack 看到任何 IR 前讀 prompt 與 catalog；最後讓它看帶 sentinel 的 model、result 與 events，再讀一次：必須相同且不含 sentinel | `leaksIntoPrompt`、`countsIntoPrompt` |
| B5 | 空 dataset 推論出 model 被接受 | 低中 | 空 dataset 必須回錯誤（`inference/needs-input`）；回答檢查改用有 events 的 dataset；新增會回答的正確 pack `answers`（0 finding） | `infersFromNothing` |
| B6 | `genericPart` 清空整個 `Reproducibility`：pack 可在 engine 路徑改 seed 與 model hash | 低 | pack 對照只清 `ResultDigest`（`engineOwned`）；改名對照另外只清兩個 model hash（`domainFree`） | `rewritesSeed`、`rewritesQueues`、`rewritesAdditional` |
| B7 | 並行檢查不呼叫 `ComputeMetrics`、`InferFromEvents` | 低 | 兩者加入並行檢查 | `TestConcurrentDisagreement`（`countsMetrics`、`countsInference`）；reviewer 的 racy pack 在 `-race` 下被抓到（`conformance-probes-after-race.log`） |
| B8 | `views/no-overlap` 不會把已存位置放在 layout 格點上 | 低 | 新變體：把第二個 node 存在第一個 node 的預設位置 | `ignoresSavedWhenPlacing`（reviewer 的 R4、R9 現在有 finding） |
| C | reviewer 自寫 34 個 mutant 中 29 個存活：`main` exit code、規則表各 entry、`Run` 的錯誤路徑、catalog 副本與 fail-closed 的子條件、views 變體、inference 取消、prompt resources、`genericPart` 的範圍 | 低 | `TestEveryRuleEntryIsEnforced`（每條規則的每個 forbid／direct pattern 與第三方 allowlist 各用一個 synthetic graph 驗證）、`TestRunExitCodes`、`TestRunFailsInvalidSuites`（子 process）、`sharedResourceTypes`、`sharedResources`、`mismatchWithoutPath`、`mismatchAsWarning`、`layoutOnlyWithSavedPositions`、`promptDropsResources`、`answers` | `mutation.log` |

reviewer 的 conformance probe 在新版：R1（NaN）、R3（改寫 seed）、R4／R9（重疊）、R5（無中生有的推論）、R6（prompt 洩漏）、R8（engine 路徑 panic）都有 finding；R2（長 ID 的正確 pack）0 finding；R7（racy ComputeMetrics）在 `-race` 下由 suite 的並行檢查觸發 race（`conformance-probes-after.log`、`conformance-probes-after-race.log`）。

### 首版說法更正

| 首版 | 更正 |
|---|---|
| 「Mutation 80/80＋3 equivalent＋2 kernel guards」 | 其中一個「equivalent」可達（B2）、kernel guard 的證明只涵蓋完全相同與長度分支：首輪實為 80/82 被擋、1 equivalent、2 kernel guard。review 後的數字見下 |
| 「40 個各自只壞一個方法的 pack」 | 40 列測試、38 種 pack（`acceptsAnything`、`mutatingMetrics` 各用兩次），其中 3 種為了讓 prompt 的 catalog 一致而覆寫兩個方法 |
| 「任何 package 無法 build 是錯誤」 | 首版不含測試檔（A5），已修正 |
| 「並行呼叫結果一致」 | 首版不含 `ComputeMetrics`、`InferFromEvents`（B7），已修正 |
| 「kernel 不讀 domain」／TEST-002 | 首版只比對 valid model 與完全相同／長度分支（B1），已修正 |
| TEST-001 | 本任務只對應正半部（非 software delivery 的 valid IR 通過同一 validator）；負半部屬 T-003／T-004 |
| NOT RUN | 漏列非 amd64、cgo 關閉的檢查；現在由 build matrix 涵蓋 |

## 實際執行結果

review 修正後（`checks/evidence/2026-10-15-T-042-review/`）：

| 指令 | Exit | 說明 |
|---|---|---|
| `make architecture-check` | 0 | `PASS architecture: 22 packages, 8 rules, 16 build configurations`（`architecture-check.log`） |
| `go test -count=1 -v ./internal/domainpack/... ./scripts/...` | 0 | 見 `t042-tests.log` |
| reviewer 的 conformance probe（改名後以 overlay 放入） | 0 | R1、R3–R6、R8、R9 有 finding，R2 0 finding（`conformance-probes-after.log`）；`-race` 下 R7 觸發 race（`conformance-probes-after-race.log`） |
| Mutation（`checks/evidence/2026-10-15-T-042-review/mutants.py`） | 0 | **117/117 被擋，3 個 equivalent，2 個 kernel guard（3/3 個 pair 證明）**（`mutation.log`）。54 個 finding site 各自 no-op；5 個 kernel mutant（含 reviewer 的「只對已知 domain 執行錯誤規則」與前綴分支）都被 suite 擋下，且 kernel mutant 加上對應 site 的 no-op 時存活，證明是那兩個 site 擋下的；suite、checker、規則表與 exit code 的 mutant。equivalent：freeze 對 valid model 不會失敗、改名後的 run 不會失敗、test variant 不進 graph 的過濾（walk 不會走到 variant）。過程中：`concurrent-no-result`、`matrix-without-arm64`（darwin/arm64 仍編譯 `_arm64.go`，probe 改為 `_linux_arm64.go`）、`platform-absent-package-is-error`（`./...` 不列出全被排除的 package，改以明確 pattern 測試，並另以目錄掃描抓整個 package 在自訂 tag 後的情形）與 4 個規則表刪除（由表本身產生的測試抓不到刪除，補上獨立於 rules.go 的 `TestPolicy`）先存活，均已補測試；pack 規則中多餘的 `C` 項移除（`forbidDirect` 已涵蓋） |
| 其他命令 | 合併後補記 | |

首輪（`checks/evidence/2026-10-15-T-042/`）：

| 指令 | Exit | 說明 |
|---|---|---|
| `make architecture-check`（T-042 前） | 2 | `NOT IMPLEMENTED: architecture-check …`（`before.log`） |
| `make architecture-check` | 0 | `PASS architecture: 22 packages, 8 rules`（`architecture-check.log`） |
| `go test -count=1 -v ./internal/domainpack/... ./scripts/...` | 0 | 92 個 RUN 全 PASS（`t042-tests.log`） |
| Mutation（`mutants.py`，已提交） | 0 | 首版寫「**80/80 被擋，3 個 equivalent，2 個 kernel guard**」——review 指出其中一個 equivalent 可達，實為 80/82（見上方更正）（`mutation.log`）。46 個 finding site 各自變成 no-op；另有 conformance 機制、checker 邏輯、規則表與預設 patterns 的 mutant，以及兩個讓 kernel 讀 domain 的 mutant（validator 對某個 domain 多一個 warning、engine seed 加上 domain 長度）——只在 `./internal/domainpack/...` 跑，由 conformance suite 擋下。equivalent：freeze 對 valid model 不會失敗、第二次 run 與第一次同輸入、改名 domain 不會讓 run 失敗。kernel guard：`validate/generic-ignores-domain` 與 `metrics/engine-ignores-domain` 的主要 site 無法由 pack 觸發，由上述 kernel mutant 證明 |
| 首輪 mutation | 0 | 74/80：`run-ignores-findings`、`unknown-dep-trusted`、`list-errors-ignored`、`violations-stop-walk-dropped`、`patterns-whole-tree` 存活，`tests-ignored` 未編譯 → 補 `TestRunReportsFindings`（子 process）、synthetic graph 的未知依賴與「違規 package 的依賴只經由它回報一次」、改用 `go list -e` 並補 `TestBrokenBuildIsAnError`、以 overlay 在 `apps/web/node_modules` 放一個 Go package 的 `TestVendoredGoIsNotChecked`（不依賴機器上是否有 node_modules），mutant 改寫為可編譯 |
| `make spec-check` / `backend-test` / `backend-race` / `backend-build` / `REQUIRE_GOAMD64_V3=1 backend-numeric` / `frontend-test` | 0 / 0 / 0 / 0 / 0 / 0 | 在分支上（含 T-022 複核後續 bc10200）重跑，各自的 log 在 evidence 目錄 |
| `govulncheck ./cmd/... ./internal/... ./schemas/... ./scripts/...` | 0 | No vulnerabilities found |
| `actionlint .github/workflows/ci.yml` | 0 | 新增的 CI 步驟 |
| GitHub Actions | 推送後補記 | |

## 驗收對照

| 測試 | 驗證內容 | 對應 |
|---|---|---|
| `TestGenericQueuePackConforms`、`TestGenericQueuePackHasAnAvailableMetric` | 第二個（test-only）domain 用同一個 IR schema、generic validator、engine 與 views 跑完整 suite；pack 指標由 engine 數字推導 | TEST-001（非 software delivery 的 valid IR 通過同一 validator）、REQ-003 |
| `softwaredelivery.TestConformance` | P0 pack 在 template 與 4 個 golden model 上通過 suite | TEST-001、REQ-003 |
| suite 的 `validate/generic-ignores-domain`、`metrics/engine-ignores-domain` 與兩個 kernel mutant | 改名 domain 不改變 validator issues 與 engine 數字 | TEST-002（referential／graph／distribution 規則與 domain 無關）、REQ-011 |
| suite 的 `views/*` | positions 只用 IR 的 node ID、不多不少、保留已存位置、套回 IR 仍 valid；沒有第二份 topology | TEST-025（後端部分） |
| `TestBrokenPacksAreCaught`、`TestInvalidSuites`、`TestUnrunnableModel`、`TestConcurrentDisagreement`、`TestRunReportsFindings` | 每個檢查項都有會觸發它的壞 pack；suite 誤設定是錯誤而非通過；`Run` 讓測試失敗 | — |
| `TestRepositoryConforms`、`TestProbesAreCaught`（16 個 overlay probe）、`TestSyntheticGraph`、`TestPatterns`、`TestVendoredGoIsNotChecked`、`TestBrokenBuildIsAnError` | repo 符合規則且每條非 future 規則都套用到 package；kernel 加入 net/http、pack、storage、pgx、未列入的第三方、os/exec、testing，kernel 測試 import pack，pack 加入 net/http、tenant、conformance，conformance 指名 pack，cmd／storage 連結 test helpers 都被抓到並附 chain；LLM SDK、`internal/ai`、pack 之間依賴、遞移 chain 以 synthetic graph 驗證 | REQ-003 |

## 需 owner 知悉

- **CB-37（NEW）**：generic Result 的 domain metrics 欄位叫 `metrics.dora`。任何 pack 的 `ComputeMetrics` 都寫進這個名為 DORA 的欄位（generic queue pack 的 `longest_queue_wait_minutes` 也在 `dora` 下）。P0 只有 software delivery，不阻擋；建議在第二個真正的 domain 之前以 schema 版本變更改名（例如 domain metrics，或依 pack ID 分組）。未自行修改 contract。
- DB driver 目前也被 `internal/tenant` 使用（`postgres.Tx`），規則照現況允許 storage 與 tenant；若 owner 希望 tenant 只透過 storage 介面，需另外調整。

## 與規劃的差異

- 規劃輸出 `internal/domainpack/conformance/` 與 `scripts/check-architecture/` 皆已建立；checker 以 Go 撰寫（讀 `go list` 的 build graph，與編譯器看到的依賴一致），不是 Python。
- 前端：Go 無法 import TS 型別，「generic engine 不 import frontend types」由語言邊界保證；前端的 LLM／DB import 已由 ESLint `no-restricted-imports` 擋（T-014，`apps/web/tests/lint-guards.test.ts`）。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| TEST-025 前端部分（2D／3D 同 ID 互選、3D 不能寫 IR、renderer 切換） | React Flow／PlayCanvas adapter 尚未建立：T-015、T-033、T-034 |
| `ai-suggests-only` 實際套用 | `internal/ai` 尚未建立（T-024）；規則已在表中並標 future |
| 獨立 review | 依 H-003 推送後進行 |
