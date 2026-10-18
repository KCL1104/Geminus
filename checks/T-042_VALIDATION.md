# T-042 驗證報告：Generic kernel／adapter dependency guard

**日期：2026-10-15（review 修正 2026-10-18，第二輪與第三輪修正 2026-10-18）｜狀態：`done`（第二輪 CHANGES REQUESTED 已修正；同一 reviewer 第三次複核 bc6da42 為 **ACCEPT**，其 low 發現已先紅後綠修正；依 H-003 驗收，owner 可隨時重開）｜需求：REQ-003、REQ-011｜Gate：無｜驗收測試：TEST-001、TEST-002、TEST-025（後端部分）**

逐條輸出：首版 `checks/evidence/2026-10-15-T-042/`，第一輪 review 後 `checks/evidence/2026-10-15-T-042-review/`，第二輪 review 後 `checks/evidence/2026-10-18-T-042-review2/`，第三輪後 `checks/evidence/2026-10-18-T-042-review3/`。以下「交付內容」「檢查項」「架構規則」三表描述目前（第三輪修正後，含 T-009 對 checker 的修改）的狀態。範圍依 IMPLEMENTATION_PLAN：這是 test-only 的 boundary guard，**不是**第二個產品 domain。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/domainpack/conformance/conformance.go` | DomainPack conformance suite（test-only，只在 `_test.go` 中 import）。`Check(ctx, Suite)` 回傳 findings，`Run(t, Suite)` 把每個 finding 報成測試錯誤；pack 的每次呼叫都在 recover 內，panic 是 finding 而不是讓 suite 崩潰。檢查項見下表 |
| `internal/domainpack/conformance/queuepack_test.go`、`testdata/generic-queue.json` | 只存在於測試檔的 generic queue pack（`generic_queue`：客人排隊等 clerk 的 service counter，容量 2、rework 回到 queue），用與 software delivery 相同的 generic primitives、同一個 IR schema、validator、engine 與 view 資料；它的 `longest_queue_wait_minutes` 由 engine 的 queue stats 推導（available），`abandon_rate` 為 unavailable 並帶 reason |
| `internal/domainpack/domainpack.go` | `NewRegistry` 只接受型別宣告在 `internal/domainpack` 自己的直接子 package 中的 pack，且一個 package 一個 pack（第三輪 L2）：架構規則把這些 package 分開，同一 package 的第二個 pack、宣告在 pack subpackage 的型別（例如在別處 instantiate 的 generic）或 `internal/domainpack` 以外的 pack 都會逃過規則。測試 pack 經 `export_test.go` 的 `NewRegistryAnyPackage` 登錄 |
| `internal/domainpack/conformance/broken_test.go` | 69 列測試、66 種壞 pack（大多只壞一個方法；為了讓 prompt 的 catalog 一致或為了洩漏，少數覆寫兩個方法），每列都必須產生指定的 finding（部分列另要求 detail 內容，區分「含 sentinel」與「只是改變」）；4 個正確 pack（拒絕推論、會推論、123 字元 ID、ID 等於 suite 的改名 domain）0 finding；另有 suite 本身的錯誤（沒有 model、model 不 valid、engine 無法執行的 model）與 `Run` 確實讓測試失敗（子 process） |
| `internal/domainpack/softwaredelivery/conformance_test.go` | P0 pack 以預設 template 與 4 個 golden model（report-example 以合成的 frozen samples 解析 empirical dataset）通過 suite |
| `scripts/check-architecture/` | `make architecture-check`：把 `go list -e -deps -test` 的 build graph 在 8 個平台 × cgo 開／關（16 個設定）上合併，檢查 9 條依賴規則（`rules.go`）與結構規則（`every-file-built`、`every-package-listed`、`pack-layout`）；違規時列出整條 import chain 或原因與規則理由，exit 1；任何 package（含測試檔）無法 build 是錯誤（exit 2）；一條規則若沒有套用到任何 package 也是錯誤（除非標為 future） |
| `Makefile`、`.github/workflows/ci.yml` | `architecture-check` 由 NOT IMPLEMENTED 換成真實命令；CI backend job 新增此步驟；`GO_PKGS`、gofmt 與 govulncheck 涵蓋 `./scripts/...` |
| `docs/ARCHITECTURE.md` §3 | T-042 實作段落 |
| `docs/CONTRACT_BLOCKERS.md` | CB-37（見下） |

### Conformance 檢查項

| 檢查 | 內容 |
|---|---|
| `identity/*` | ID 是合法的 IR domain 值且穩定；suite 的 model 都屬於此 domain |
| `catalog/*` | node／resource catalog 非空、kind 不重複、有 label、只用 generic node types 與 human／machine／generic；回傳副本（呼叫端修改不影響 pack） |
| `validate/*` | valid model 無 error、不改 IR、決定性；其他 domain 與 nil 回 `DOMAIN_MISMATCH` error（fail closed）；**generic validator 對改名為不相干 domain（`zz.conformance:renamed`）的同一 IR 回相同 issues**，對原樣與 8 個各破壞一條規則的變體都比對（validator 的 limits、ids、refs、routing、distributions、constraints 每個 stage 與 zero-time cycle 各至少一個，由 `TestInvalidVariantsBreakTheirRule` 確認每個變體確實產生該 error） |
| `views/*` | `DefaultViews` 對原樣、去掉 positions、只留一個已存位置、已存位置在預設格點上四種輸入：只回本 IR node ID 的 positions（不多不少）、不重疊、保留已存位置、不改 IR、決定性，套回 IR 後仍 valid（2D 是同一 IR 的 views，3D 由 2D 推導，沒有另一份 topology） |
| `metrics/*` | 經過 generic engine：`ComputeMetrics` 不改 Result、engine 擁有的部分（除 domain metrics 與 digest 外全部）不因 pack 而變、Result（含 pack 指標）通過 schema、digest 可重現、metric key 非空；**同一 IR 改名 domain 後能執行（`metrics/renamed-run`）且 engine 的數字完全相同** |
| `inference/*` | 空 dataset 與取消的 context 必須回錯誤且不帶結果；有 events 時只能回錯誤（不帶結果）或本 domain 的 valid model，不回 (nil, nil)；同一 dataset 兩次呼叫結果相同（`inference/deterministic`：ID 由 server 分配，D-016） |
| `prompt/*` | domain 等於 ID、有 summary、無空規則、catalog 與 pack 的 catalog 相同；pack 看過 sentinel model（名稱、node 名稱與所有 ID 都帶 sentinel，reference 一併改名，仍是 valid model）與帶 sentinel 的 events 後，prompt 與 catalog 不含 sentinel 且與 pack 看到任何 IR 前相同 |
| `stateless` | pack 看過 sentinel model 後，對 suite 每個 model 的 `Validate`、`DefaultViews` 與 `ComputeMetrics` 回答與之前相同且不含 sentinel 或 sentinel ID 前綴（不把一個 tenant 的 model，包括它的 ID，帶進另一個的回答；第三輪 L3） |
| `concurrent` | 8 個 goroutine 同時呼叫每個方法（含 `ComputeMetrics`、`InferFromEvents`）的結果與循序呼叫相同、不 panic，finding 指出是哪個方法；data race 由 `make backend-race` 檢查 |

### 架構規則（`scripts/check-architecture/rules.go`）

| 規則 | 內容 |
|---|---|
| `generic-kernel` | `internal/model`、`internal/simulation`、`internal/results`、`internal/domainpack`（介面）與 `schemas` 不依賴任何 domain pack、application layer（cmd、auth、tenant、project、storage、import、ai、audit、config、httpapi、api）、test helpers、`testing`、網路／DB／process／plugin I/O（`net`、`net/http`、`database/sql`、`os/exec`…；`net/url`、`net/netip` 由第三方 parser 使用，遞移允許）、DB driver、LLM SDK。三個 allowlist：遞移依賴中的 module package 只能是 kernel 本身（`moduleOnly`，module 內其他 package 不能替它做 I/O）；第三方只允許 jcs、jsonschema，以及 x/text 中 message catalog 連結的 package（`language`、`message/...`、`feature/plural`、`internal/...`；整個 module 不行，例如 `unicode/cldr` 會讀目錄與 zip，第三輪 L1）；package **自己** import 的標準函式庫只能是 `kernelStd`（計算、編碼、hash、embed；`io/ioutil`、`path/filepath`、`text/template`、`archive/zip`、`mime`、`crypto/x509`、`log`、`runtime/debug` 等會在底下碰檔案或網路的 package 不在其中）。`os`、`syscall`、`unsafe`、`C`、`golang.org/x/sys` 都不在 allowlist 中，因此被拒絕（第二輪起不再另有 `forbidDirect`）；且只能有 Go 原始檔（`goOnly`：assembly、C、`.syso` 等可以不經 import 直接做 system call）。新增任何一項都要改此表 |
| `generic-kernel-tests` | 上述 package 的測試也不 import 具體 pack、application layer、DB 或 LLM |
| `domain-packs` | pack 只建在 kernel 上：不依賴其他 pack、application layer、test helpers、I/O、DB、LLM；可用自己的 subpackage；與 kernel 相同的 `moduleOnly`（kernel＋自己的 unit）、第三方 allowlist、`kernelStd` 與 `goOnly` |
| `conformance-is-generic` | conformance suite 不指名具體 pack |
| `no-test-infra-in-production` | production code 不連結 `testing`、`contracttest` 或 conformance |
| `llm-sdks-only-in-ai` | LLM SDK 只在 `internal/ai` 與 `cmd` |
| `db-only-in-storage` | DB driver／`database/sql` 只在 `internal/storage`、`internal/tenant` 與 `cmd`。T-009 起這兩者是 gateway（`through`）：經由它們存取資料的 package 不因它們內部的 driver 而違規，自己 import driver 仍是違規（ADR-019） |
| `ai-suggests-only`（future） | `internal/ai` 不得依賴 storage、tenant、cmd、`os/exec`、DB；package 尚未建立，標為 future |

結構規則：`every-file-built`（任何設定都不編譯的檔案、或只在自訂 tag 下存在的 package）、`every-package-listed`（被 import 卻不在 `./...` 中的 module package：`testdata`、以 `_` 或 `.` 開頭的目錄；module path 下另一個 `go.mod` 的 package 同樣拒絕，訊息指出是 nested module，第三輪 L4）、`pack-layout`（pack 是 `internal/domainpack` 的直接子 package；pack 的 subpackage 只能由 pack 自己 import，且不得依賴 `internal/domainpack`）。**更正（第三輪 L2）**：第二輪寫「所以 subpackage 不可能是第二個 pack」是錯的：同一 package 中嵌入 pack 的第二個型別、或宣告在 subpackage 且只 import kernel 的 generic pack 在 pack package 中 instantiate，都通過 `pack-layout`。「一個 package 一個 pack」改由 `NewRegistry` 在登錄時強制（見交付內容）。`pack-layout` 也因此拒絕一些合理的版面：依賴 `domainpack` 型別的 catalog subpackage、拆成 subpackage 的 conformance suite、由 cmd 直接 import pack 的 subpackage（例如 templates）；需要時再放寬並記錄理由。

**Import 規則看不到的事**（文件與 rules.go 都明說）：經 `fmt.Print`、`log` 或 `print` 寫到 stdout／stderr，`time.LoadLocation` 讀 zoneinfo，以及允許的第三方 package 內部的檔案存取（jsonschema 的 `FileLoader` 會讀 `file://` URL，pack 使用該 library 時無法以 import 規則排除，第三輪 L1）。這些由 review 與 determinism golden 涵蓋。

既有各 package 的 `TestDependencies`（`contracttest.OnlyDeps`）保留；本 checker 是跨整個 module、含遞移與測試依賴的集中檢查。

## 第三輪獨立 review（2026-10-18，同一 reviewer 複核 bc6da42）：ACCEPT

Reviewer 全部結果來自 `git archive bc6da42` 的 export（工作樹當時有未提交的 T-009 修改，未被使用）。N1–N11 皆已修正（N4 依定義重新界定）；16 個設定下 kernel 與 pack 都碰不到 net/http、DB driver、LLM SDK、其他 pack、application layer、module proxy、非 Go 原始檔或未列出的 package；報告數字全部重現（mutation 166/166＋1 equivalent＋3 guard、4/4 pair；165 RUN；67 列、64 種壞 pack）。CONFIRMED 的 low 發現，驗收後先寫會失敗的測試再修正（紅燈 log 在 `checks/evidence/2026-10-18-T-042-review3/`）：

| # | 發現 | 修正 | 證明 |
|---|---|---|---|
| L1 | 第三方 allowlist 以整個 module 為單位：kernel 可 import `golang.org/x/text/unicode/cldr` 並 `DecodePath("/etc")`；pack 可用 jsonschema 的 `FileLoader` 讀檔 | x/text 只允許 catalog 連結的 4 組 package；`FileLoader` 列入「import 規則看不到的事」（library 在用，無法以 import 排除） | `TestRound2Probes` 新增 kernel 與 pack 的 `unicode/cldr` probe（舊 allowlist：2 個 FAIL，`red-l1.log`） |
| L2 | `pack-layout` 無法保證一個目錄一個 pack（同 package 的第二個 pack、subpackage 的 generic pack 都通過），報告說法為誤；另有合理版面被拒 | `NewRegistry` 在登錄時要求每個 pack 的型別宣告在 `internal/domainpack` 自己的直接子 package、一個 package 一個 pack；報告與 checker 註解更正；被拒的版面列為已知限制 | `TestRegistryPackages`（路徑規則，含 `main`、`_test`、無 `/` 的前綴）、`softwaredelivery.TestRegistration`（同 package 的 `zzSecond` 被拒、pointer 可用）（舊程式：同 package 兩個 pack 被接受，`red-l2.log`） |
| L3 | `stateless` 的 sentinel model 與 Models[0] 共用 node ID：回報上一個 model node ID 的 pack 0 finding（真實情況會洩漏另一個 tenant 的 ID） | `sentinelModel`：名稱與所有 ID（entity type、resource、node、flow、metric、view positions）都帶 sentinel 前綴，reference 一併改名，仍是 valid model；`stateless` 與 `prompt/static` 也檢查前綴 | `remembersLastNodeIDs`（舊程式 0 finding，`red-l3.log`）、`leaksIDsIntoPrompt`、`TestSentinelModel`（每個 ID 與 reference 帶前綴且 model valid） |
| L4 | module path 下的 nested module（`tools/go.mod`＋replace）被當成 main module 的 package，以誤導的訊息拒絕 | `go list` 的 `Module` 欄位：不屬於 main module 的 package 以「nested module」訊息拒絕（仍 fail closed） | `TestNestedModule`、`TestModuleInfoLoaded`；reviewer 的 nested repo 以新 binary 執行，訊息為 `a package of the nested module github.com/kcl1104/geminus/tools …`（`nested-module-probe.log`） |
| L5 | CI 成本約加倍（checker 測試在 `-race` 下 65 s） | 不改：checker 的 probe 測試以 `t.Parallel` 並行呼叫 `go list`，保留 `-race`；目前每個 backend job 約 2 分鐘可接受 | — |
| L6 | 報告「架構規則」表仍寫已刪除的 `forbidDirect` | 表已更新 | — |

PLAUSIBLE（未重現或依設計）：stale `ComputeMetrics` cache 需新的 pack instance 才能偵測；overlap 只比較完全相同的位置；`kernelStd` 拒絕 `hash/fnv`、`encoding/base64`（需要時各為一行 reviewed 變更）；`every-package-listed` 在實際版面無 false positive；freeze site 只能經 kernel 的 hashing 缺陷到達。

## 第二輪獨立 review（2026-10-18，同一 reviewer 複核 a0feead）與修正

結論 **CHANGES REQUESTED**：首輪的發現全部確認已修（原 probe 皆得到預期結果，已提交的 mutation 可完整重現），核心規則在 16 個設定上成立；但三項新防護可被繞過，且 rules.go、ARCHITECTURE §3 與本報告宣稱它們完整。reviewer 的 probe 與舊版結果在 `checks/evidence/2026-10-18-T-042-review2/`（`arch2.py`、`arch3.py`，舊版輸出見 reviewer 的 scratchpad，新版輸出為 `arch2-after.log`、`arch3-after.log`）。

| # | 發現 | 等級 | 處理 | 證據 |
|---|---|---|---|---|
| N1 | `forbidDirect` 只看 kernel／pack 自己的 import：經 module 內任何其他 package（`internal/zzproxy`、`pkg/zzproc`、cgo＋`!cgo` stub、`unsafe`＋`go:linkname`）做 I/O 都通過；`io/ioutil.WriteFile`、`filepath.Walk` 不需任何被禁的 import；rules.go 的「I/O 需要被禁的 package」為誤 | 低中 | kernel 與 pack 的遞移依賴中，module package 只能是 kernel（與 pack 自己的 unit）（`moduleOnly`）；pack 也套用第三方 allowlist；自己 import 的標準函式庫改為 allowlist（`kernelStd`）；註解與文件改為列出 import 規則看不到的管道 | `TestRound2Probes`（internal proxy、pkg/ proxy、`io/ioutil`、`path/filepath`、`text/template`、`archive/zip`、pack 的第三方 module）；`cgo-stub-probes.log`（cgo＋stub 的 proxy，kernel 與 pack 皆違規）；`arch3-after.log`（linkname proxy） |
| N2 | `_` 開頭與 `testdata` 目錄的 package 可被 import 並連結，但從未被當作 package 檢查（無 `forbidDirect`、無 `every-file-built`） | 低中 | 圖中任何不在 `./...` 的 module package 為 `every-package-listed` 違規 | `TestRound2Probes`（`_zz` syscall、`testdata/zz`、`_zz` 內自訂 tag 檔） |
| N3 | assembly 檔可不經 import 做 raw system call（reviewer 實測開出 TCP socket）；`.syso` 同理 | 低中 | kernel 與 pack 不得有任何非 Go 原始檔（`goOnly`；含被 build constraint 排除的 `IgnoredOtherFiles`） | `TestRound2Probes`（`zz_probe_amd64.s`、`.syso`） |
| N4 | 加一個 `doc.go` 讓 `packs` 成為 package，`packs/alpha` 與 `packs/beta` 即可互相 import | 低 | pack 的 subpackage 只能由 pack 自己 import；subpackage 不得依賴 `internal/domainpack`（遞移）。reviewer 原 probe（alpha、beta 都不碰介面）在新版仍 PASS：兩者無法實作 DomainPack，因此只是 `packs` 這一個 pack 的內部 package；能成為第二個 pack 的寫法（import 介面、經別的 package 取得介面型別、由外部直接 import subpackage）都是違規 | `TestRound2Probes`（doc.go＋兩個實作介面的 subpackage、經型別 alias 間接依賴、由外部登錄兩個 subpackage、其他 package 的測試 import pack subpackage）；`TestSyntheticGraph` |
| N5 | site 557（改名後 run 失敗）列為 equivalent，但 kernel 拒絕改名 domain 時可達 | 低（證據） | 該 site 改名為 `metrics/renamed-run`，列為 kernel guard；新增 kernel mutant `kernel-engine-rejects-renamed-domain` 與 pair 證明 | `mutation.log` |
| N6 | TEST-002 變體未觸及 validator 的 `limits`、`constraints` stage，也沒有 zero-time cycle | 低 | 新增 3 個變體（metadata 巢狀過深、sink 上的 constraint、splice 進第一條 flow 的 decision 自迴圈）；`variantCodes` 列出每個變體必須產生的 error code | `TestInvalidVariantsBreakTheirRule` |
| N7 | 只有一個 model 時，Validate 夾帶上一個 model 名稱的跨呼叫洩漏看不到 | 低 | `stateless`：sentinel 前後對每個 model 的 Validate、DefaultViews、ComputeMetrics 必須相同且不含 sentinel | `remembersLastModel`、`countsIntoIssues`、`viewsRememberLastModel`、`countsMetrics` |
| N8 | 推論必須 deterministic 但介面沒說，且 finding 訊息誤導（`goroutine N disagrees`） | 低 | DomainPack 文件寫明 deterministic、stateless、ID 由 server 分配（D-016）；新增 `inference/deterministic`；`concurrent` finding 指出方法 | `mintsModelIDs`、`countsInference`、`TestConcurrentDisagreement` |
| N9 | 空 dataset 回 (model, error) 通過 | 低 | 空 dataset、取消的 context 與一般回答：有錯誤時不得帶結果 | `modelAndErrorWhenEmpty`、`modelAndError`、`modelAndErrorWhenCancelled` |
| N10 | reviewer 14 個額外 mutant 中 12 個存活 | 低（測試） | 全部納入新腳本：變體各自移除（`TestInvalidVariantsBreakTheirRule` 要求每個名稱存在）；views／events 的 sentinel 暴露（`leaksViewsIntoPrompt`、`leaksEventsIntoPrompt`，並要求 detail 為「含 sentinel」而非「改變」）；ComputeMetrics 的暴露已移除（engine result 不含客戶文字，且 metrics step 已給過它，與 `c.initial` 的比對涵蓋）；`renamed()` 忽略 pack ID（正確 pack「ID 等於改名 domain」）；TestImports／XTestImports 合併（arm64-only 的 internal／external kernel 測試 probe） | `mutation.log` |
| N11 | 本報告上方三表仍描述首版 | 低（文件） | 三表改為目前狀態；「首版說法更正」的「其中一個 equivalent 可達」改正為兩個（見下） | 本檔 |

PLAUSIBLE（reviewer 未重現）：巢狀 module 或第三方 module 當 pack 的 proxy → pack 現在套用第三方 allowlist；`//go:build race`、`amd64.v3`、`ignore` 檔會讓 `every-file-built` 失敗 → 維持 fail closed（CI 會編譯 race 與 v3，但這些 tag 下才存在的程式碼不在 matrix 中檢查），本 repo 目前沒有這類檔案。CI 成本：reviewer 量測 checker 測試約 33 s（`-race` 41 s）；本輪 probe 增加後在本機約 60 s，接受。

## 第一輪獨立 review 結果與處理（2026-10-18）

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
| 「Mutation 80/80＋3 equivalent＋2 kernel guards」 | 其中一個「equivalent」可達（B2）、kernel guard 的證明只涵蓋完全相同與長度分支：首輪實為 80/82 被擋、1 equivalent、2 kernel guard。另一個「equivalent」（改名後 run 失敗的 site）在 kernel 拒絕改名 domain 時也可達（第二輪 N5），應為 kernel guard |
| 第一輪 review 後「117/117 被擋，3 個 equivalent，2 個 kernel guard」 | 同上，site 557 不是 equivalent：實為 117/117 被擋、2 equivalent、3 kernel guard（其中一個當時沒有 pair 證明）。第二輪後的數字見下 |
| 「40 個各自只壞一個方法的 pack」 | 40 列測試、38 種 pack（`acceptsAnything`、`mutatingMetrics` 各用兩次），其中 3 種為了讓 prompt 的 catalog 一致而覆寫兩個方法（第二輪後為 67 列、64 種） |
| 「kernel 與 pack 不得自己 import os、syscall…，I/O 需要被禁的 package」（第一輪） | 可經 module 內的 proxy package、`_`／`testdata` 目錄、assembly 或不需 os 的標準函式庫繞過（N1–N3），已改為 allowlist 與結構規則 |
| 「任何 package 無法 build 是錯誤」 | 首版不含測試檔（A5），已修正 |
| 「並行呼叫結果一致」 | 首版不含 `ComputeMetrics`、`InferFromEvents`（B7），已修正 |
| 「kernel 不讀 domain」／TEST-002 | 首版只比對 valid model 與完全相同／長度分支（B1），已修正 |
| TEST-001 | 本任務只對應正半部（非 software delivery 的 valid IR 通過同一 validator）；負半部屬 T-003／T-004 |
| NOT RUN | 漏列非 amd64、cgo 關閉的檢查；現在由 build matrix 涵蓋 |

## 實際執行結果

第三輪 review 修正後（`checks/evidence/2026-10-18-T-042-review3/`，含 T-009 fb6e265）：

| 指令 | Exit | 說明 |
|---|---|---|
| `make architecture-check` | 0 | `PASS architecture: 32 packages, 9 rules, 16 build configurations`（`architecture-check.log`；T-009 的 package 加入後） |
| `go test -count=1 -v ./internal/domainpack/... ./scripts/...` | 0 | 174 個 RUN 全 PASS（`t042-tests.log`；第二輪 165，加上本輪的 9 個） |
| `go test -race -count=1 ./internal/domainpack/... ./scripts/...` | 0 | `t042-race.log` |
| 紅燈：本輪的新測試在修正前的程式上 | 1 | `red-l1.log`（舊 allowlist：kernel 與 pack 的 `unicode/cldr` probe 2 個 FAIL）、`red-l2.log`（同一 package 的兩個 pack 被接受；新的 API 尚不存在而無法編譯）、`red-l3.log`（`remembersLastNodeIDs` 0 finding） |
| reviewer 的 nested module repo，以新 binary 執行 | 1 | 預期的違規，訊息為 nested module（`nested-module-probe.log`） |
| Mutation（`mutants.py`，本目錄） | 0 | **196/196 被擋，1 個 equivalent，3 個 kernel guard（4/4 個 pair 證明）**（`mutation.log`，單次完整執行）。52 個 finding site、6 個 kernel mutant、4 個 pair、148 個 suite／checker／規則表 mutant：第二輪的 118 個（3 個 pattern 跟著改寫的程式更新）加上本輪 30 個（sentinel model 的每類 ID 與 reference、前綴檢查、`NewRegistry` 的套件檢查與其各個條件、x/text 的範圍與各項、nested module 訊息與 `Module` 欄位）。Equivalent 同第二輪：`metrics/run` 的 freeze site |
| `make spec-check` | 0 | |
| GitHub Actions | 推送後補記 | |

第二輪 review 修正後（`checks/evidence/2026-10-18-T-042-review2/`）：

| 指令 | Exit | 說明 |
|---|---|---|
| `make architecture-check` | 0 | `PASS architecture: 24 packages, 9 rules, 16 build configurations`（`architecture-check.log`） |
| `go test -count=1 -v ./internal/domainpack/... ./scripts/...` | 0 | 165 個 RUN 全 PASS（`t042-tests.log`） |
| `go test -race -count=1 ./internal/domainpack/... ./scripts/...` | 0 | `t042-race.log` |
| reviewer 的 checker probe（`arch2.py`、`arch3.py`，以新 binary 執行） | — | `arch2-after.log`、`arch3-after.log`：proxy、`_`／`testdata` 目錄、linkname、custom tag 皆為違規（exit 1）。三個非 exit 1：`grouped-packs-with-parent-doc` exit 0（alpha、beta 不碰 DomainPack 介面，依 N4 的定義是 `packs` 這一個 pack 的內部 package；實作介面的版本見 `TestRound2Probes`）；`pack-via-proxy-cgo`／`kernel-via-proxy-cgo` exit 2（proxy 沒有 `!cgo` stub，cgo 關閉的設定無法 build，fail closed；加上 stub 的版本為違規，見 `cgo-stub-probes.log`）；`vendor-dir-import` exit 2（module mode 下 Go 本身拒絕 `vendor` import path，fail closed） |
| Mutation（`mutants.py`，本目錄） | 0 | **166/166 被擋，1 個 equivalent，3 個 kernel guard（4/4 個 pair 證明）**（`mutation.log`）。52 個 finding site、6 個 kernel mutant、4 個 pair、118 個 suite／checker／規則表 mutant（含 reviewer 的 14 個額外 mutant）。首次執行（`mutation-first-run.log`）160/170：3 個 mutant 寫法未編譯（已改寫）；1 個 pair 未證明（reviewer 版的 kernel mutant 拒絕所有含 `.`／`:` 的 domain，也讓「ID 等於改名 domain」的正確 pack 自己的 model 失敗，由 `metrics/run` 擋下，改為只拒絕第二個改名 domain）；`variant-entries-kept` 不再 equivalent（`every-package-listed` 會把 test variant 當未列出的 package）；`forbidDirect` 的 4 個 mutant 存活——新的 allowlist 已完全涵蓋，`forbidDirect`／`directIO` 刪除；`net-allowed` 存活——`TestPolicy` 與 `TestEveryRuleEntryIsEnforced` 只測直接 import，改為經允許的第三方 package 遞移到達；`go-only-ignores-ignored-files` 存活——補「其他平台的 assembly」probe。Equivalent：`metrics/run` 的 freeze site（valid model 的 encode 與 hash 不會失敗） |
| `make spec-check` | 0 | |
| GitHub Actions | 推送後補記 | |

第一輪 review 修正後（`checks/evidence/2026-10-15-T-042-review/`）：

| 指令 | Exit | 說明 |
|---|---|---|
| `make architecture-check` | 0 | `PASS architecture: 24 packages, 9 rules, 16 build configurations`（`architecture-check.log`；含 T-008 的 auth packages 與 `auth-authenticates-only`） |
| `go test -count=1 -v ./internal/domainpack/... ./scripts/...` | 0 | 132 個 RUN 全 PASS（`t042-tests.log`；與 T-008 合併後） |
| reviewer 的 conformance probe（改名後以 overlay 放入） | 0 | R1、R3–R6、R8、R9 有 finding，R2 0 finding（`conformance-probes-after.log`）；`-race` 下 R7 觸發 race（`conformance-probes-after-race.log`） |
| Mutation（`checks/evidence/2026-10-15-T-042-review/mutants.py`） | 0 | **117/117 被擋，3 個 equivalent，2 個 kernel guard（3/3 個 pair 證明）**（`mutation.log`）。54 個 finding site 各自 no-op；5 個 kernel mutant（含 reviewer 的「只對已知 domain 執行錯誤規則」與前綴分支）都被 suite 擋下，且 kernel mutant 加上對應 site 的 no-op 時存活，證明是那兩個 site 擋下的；suite、checker、規則表與 exit code 的 mutant。equivalent：freeze 對 valid model 不會失敗、改名後的 run 不會失敗、test variant 不進 graph 的過濾（walk 不會走到 variant）。過程中：`concurrent-no-result`、`matrix-without-arm64`（darwin/arm64 仍編譯 `_arm64.go`，probe 改為 `_linux_arm64.go`）、`platform-absent-package-is-error`（`./...` 不列出全被排除的 package，改以明確 pattern 測試，並另以目錄掃描抓整個 package 在自訂 tag 後的情形）與 4 個規則表刪除（由表本身產生的測試抓不到刪除，補上獨立於 rules.go 的 `TestPolicy`）先存活，均已補測試；pack 規則中多餘的 `C` 項移除（`forbidDirect` 已涵蓋） |
| `make spec-check`／`backend-test`／`backend-race`／`backend-build`／`REQUIRE_GOAMD64_V3=1 backend-numeric`／`frontend-test` | 0／0／0／0／0／0 | 與 T-008 合併後在分支上執行，log 在同一目錄 |
| `govulncheck ./...`、`actionlint` | 0、0 | |
| GitHub Actions | 推送後補記 | |

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
| `TestRegistryPackages`、`softwaredelivery.TestRegistration` | 登錄的 pack 型別在自己的直接子 package、一個 package 一個 pack | REQ-003 |
| `TestSentinelModel`、`remembersLastNodeIDs`、`leaksIDsIntoPrompt` | sentinel model 的所有 ID 與 reference 帶 sentinel 且仍 valid；回報或放入 prompt 的上一個 model 的 ID 被抓到 | SECURITY §7 |
| `TestNestedModule`、`TestModuleInfoLoaded` | nested module 的 package 以正確原因拒絕；`go list` 提供每個 package 的 module | — |
| `TestRepositoryConforms`、`TestProbesAreCaught`（16 個 overlay probe）、`TestSyntheticGraph`、`TestPatterns`、`TestVendoredGoIsNotChecked`、`TestBrokenBuildIsAnError` | repo 符合規則且每條非 future 規則都套用到 package；kernel 加入 net/http、pack、storage、pgx、未列入的第三方、os/exec、testing，kernel 測試 import pack，pack 加入 net/http、tenant、conformance，conformance 指名 pack，cmd／storage 連結 test helpers 都被抓到並附 chain；LLM SDK、`internal/ai`、pack 之間依賴、遞移 chain 以 synthetic graph 驗證 | REQ-003 |

## 需 owner 知悉

- **CB-37（NEW）**：generic Result 的 domain metrics 欄位叫 `metrics.dora`。任何 pack 的 `ComputeMetrics` 都寫進這個名為 DORA 的欄位（generic queue pack 的 `longest_queue_wait_minutes` 也在 `dora` 下）。P0 只有 software delivery，不阻擋；建議在第二個真正的 domain 之前以 schema 版本變更改名（例如 domain metrics，或依 pack ID 分組）。未自行修改 contract。
- DB driver 也被 `internal/tenant` 使用（`postgres.Tx`），規則允許 storage 與 tenant，T-009 起兩者是 gateway（ADR-019，PROPOSED）；若 owner 希望 tenant 只透過 storage 介面，需另外調整。

## 與規劃的差異

- 規劃輸出 `internal/domainpack/conformance/` 與 `scripts/check-architecture/` 皆已建立；checker 以 Go 撰寫（讀 `go list` 的 build graph，與編譯器看到的依賴一致），不是 Python。
- 前端：Go 無法 import TS 型別，「generic engine 不 import frontend types」由語言邊界保證；前端的 LLM／DB import 已由 ESLint `no-restricted-imports` 擋（T-014，`apps/web/tests/lint-guards.test.ts`）。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| TEST-025 前端部分（2D／3D 同 ID 互選、3D 不能寫 IR、renderer 切換） | React Flow／PlayCanvas adapter 尚未建立：T-015、T-033、T-034 |
| `ai-suggests-only` 實際套用 | `internal/ai` 尚未建立（T-024）；規則已在表中並標 future |
