# T-035 驗證報告：Audit／OpenTelemetry／structured logs

**日期：2026-10-26｜狀態：agent 實作完成；等 coordinator 在一次性 database 上執行 DB 測試、DB mutant 與 `make integration-test`，再交獨立 review（H-003）；T-035 在 tasks.json 的 status 沒有改（agent 不改狀態）。｜需求：REQ-018（audit）、REQ-020（structured logs、traces／metrics、telemetry 不含客戶內容）｜驗收測試：TEST-027（audit／telemetry privacy）｜Gate：本任務提出的 D-091–D-095 與 ADR-028 都是 **PROPOSED**（`approved_by`／`approved_at` 為 null，ADR 狀態 PROPOSED），依建議實作並加進 T-035 的 `decision_gates`（D-091、D-094 另加進 T-036，D-093、D-095 另加進 T-039）。｜本 worktree 沒有 PostgreSQL：所有 DB-backed Go 測試都 SKIP，列為 NOT RUN。**

範圍依 tasks.json 與 coordinator 的指示：

1. 每個 business write 在同一個 transaction 寫它的 audit event，帶 request／run／provider 的 correlation；
2. traces／metrics 與 structured JSON logging（`log/slog`）；
3. privacy filter（TEST-027），以對抗性的值檢查，含 error 的路徑；
4. job、simulation、LLM、import 的營運指標。

**沒有任何 live AI 呼叫**，沒有讀取或設定任何 API key（`ANTHROPIC_API_KEY`、`GEMINUS_ANTHROPIC_API_KEY_*` 都沒有讀）。沒有新 migration、沒有改任何既有 migration（含註解）。沒有改 `internal/simulation` 的任何檔案（engine、DES kernel、Result、digest、lock 都沒有動；runner 只在 engine 回來之後記 log 與 span）。Telemetry 預設不 export，沒有選任何廠商。逐條輸出在 `checks/evidence/2026-10-25-T-035/`：`inventory.txt`（改動前寫下的缺口）、`environment.txt`、`exit-codes.txt`、各指令的 log、`redfirst.py` 與 `redfirst/`、`mutate.py` 與 `mutants-*.log`、`licenses.txt`、`govulncheck*.log`、`d092-config-*`。

## Commit

Branch `t035`，從 38f0b7b 開出，沒有 push。

| Commit | 內容 |
|---|---|
| ed9f796 | `internal/audit`（catalogue、`Check`、`ValidActor`、`Label`）、`internal/storage/auditlog` 寫入前檢查與 `AppendSystem`、provisioning 改經 auditlog；worker loop 與 reaper 在同一 transaction 寫 `job.attempt_failed`（`jobs.ReapJobs`、`jobs.ReapFailure`）、`worker.New` 要求 actor reference；`internal/observability`（filter、handler、context correlation）、`httpapi.AccessLog`、request ID 與 annotation（`WriteError` 的 error code、`Scoped`／`Self` 的 organization 與 user）、API panic 不記值；generator 的 `provider_request_id` 與 provider call 的 log 行、runner／profiler／validator 的營運 log 行；`cmd/api`、`cmd/worker` 的 logger；`dbtest.RefuseAudit`；worker 與 jobs 的 DB 測試；architecture 規則 `observability-is-a-leaf`、`audit-catalogue-is-pure` 與 probe |
| 947749c | `internal/api` 的 DB 測試：15 個 audited route 在 audit event 寫不進去時什麼都不 commit |
| 6e989a4 | architecture 的 meta-test（`TestSyntheticGraph`、`TestEveryRuleEntryIsEnforced`）對新規則的修正（ed9f796 單獨時 `./scripts/...` 的測試失敗，見「差異」） |
| a229cbe | OpenTelemetry：`internal/observability/telemetry`（API）、`telemetry/export`（SDK、OTLP／HTTP）、`config.LoadTelemetry`；access log、job attempt、engine、provider call、import 的 span 與 metric；規則 `otel-sdk-only-in-export`、`telemetry-uses-the-api-only` 與 probe；`go.mod`／`go.sum`（OTel v1.47.0、x/net v0.60.0）；ADR-028（PROPOSED）；THIRD_PARTY_NOTICES |
| e905ff8 | export 的 resource：`service.name` 一律是服務名稱（`OTEL_SERVICE_NAME` 不覆蓋），文件與測試對齊 SDK 的行為 |
| 7c0a241 | D-091–D-095 PROPOSED（DECISIONS_AND_TBD、decisions.json）；tasks.json 的 gate 與 planned outputs（status 不變）；ARCHITECTURE、SECURITY、API_DATABASE、TWIN_IR_SPEC、TEST_PLAN；重產的 `checks/*.json` |
| fdc4d38 | 營運 log 行的測試：`TestTheCallsLine`、`TestEngineOutcome`（unit）；`TestRunClosedLoop`、`TestImportMappingClosedLoop` 的 engine 與 import 行（DB） |
| e2b0d2b | `cmd/worker` 的 `newLogger` 與 `TestTheWorkersLoggerIsFiltered`；原本的 run 測試改名 `TestTheWorkersLinesAreJSON`（它在 T-035 之前也成立，見「先紅後綠」） |
| fc2b8b7 | Mutation 第 1 輪找到的缺口（17 個存活的 mutant，見「Mutation」）；沒有閉合的雙引號其後全部隱藏；拿掉多餘的 LogValuer recover（slog 自己會 recover） |
| d4b2c49 | D-094 (c)(e)、decisions.json 與 SECURITY §9 的文字對齊未閉合引號的規則與 slog 的 LogValuer recover |
| a42ecfd | 本報告與 evidence（第一版） |
| 16fa72a | `redfirst.py` 檢查 tree 時忽略它自己的 log |
| （本 commit） | `redfirst/` 重跑（加上 `httpapi-self-user`）；本報告的最終數字 |

## 改動前的缺口（`inventory.txt`）與處理

| 缺口 | 處理 | 測試 |
|---|---|---|
| G1 worker loop 的 `jobs.Fail`（attempt 失敗、job 與 run／proposal／import 跟著 failed 或回 queued）沒有 audit | 同一 transaction 寫 `job.attempt_failed`（D-091 (b)） | unit `TestTheAttemptsEvent`；DB `TestFailedAttemptsAreAudited`、`TestAnAuditFailureCommitsNothing` |
| G2 reaper 收回過期 lease 沒有 audit | `jobs.ReapJobs` 回傳收回的 job，每個在 reap 的 transaction 寫一筆（`LEASE_EXPIRED`） | DB `TestTheReaperAudits`、`TestReapJobsReturnsWhatItTook`、`TestAnAuditFailureCommitsNothing` |
| G3 provisioning 自己寫 audit SQL | 改經 `auditlog.AppendSystem`；source 測試確認只有 `auditlog` 寫 `twin.audit_events` | unit `TestTheCatalogueMatchesTheCode`；DB `TestProvision`（既有） |
| G4 沒有任何東西檢查 audit event 的內容 | `internal/audit` 的封閉 catalogue，`auditlog` 寫入前 `Check`，不符則 fail closed（D-091 (c)） | unit `TestEveryActionFits`、`TestRefused`、`TestShapes`、`TestAnEventIsCheckedBeforeItIsWritten`；DB `TestAnUnwritableAuditEventCommitsNothing` |
| G5 `provider_request_id` 沒有記 | `proposal.generate`／`attempt_failed` 加上它（不是 label 時不記） | unit `TestAuditMeta` |
| G6 worker／system actor 的格式未定（0001「open until T-035」） | D-092 (a)：actor reference；`worker.New` 要求它；ADR-024 的 `WORKER_ID` 文字不改（D-092 (b)） | unit `TestActors`、`TestRefused`、`TestNewChecksTheConfig` |
| L1 沒有 access log | `httpapi.AccessLog`（route pattern、status、duration、size、error code） | unit `TestAccessLog`；DB `TestRunServesAndStops` |
| L2 request ID 在 9 處手動加入；worker handler 的行沒有 job | context 的 correlation：request 的每一行帶 `request_id`（解析後加 organization、user），job 的每一行帶 job 與資源 | unit `TestEveryLineCarriesTheContextsIDs`、`TestAnnotations`、`TestAccessLog`；DB `TestTheHandlersLinesCarryTheJob` |
| L3 沒有集中的 privacy filter | `internal/observability.Handler`：default deny、值的形狀、自由文字的 sanitize（D-094） | unit `internal/observability` 的 15 個測試 |
| L4 API 的 panic 記 panic 值 | `observability.PanicText`（型別或 runtime error 的訊息） | unit `TestRecover`、`TestPanicText` |
| L5 job 的行沒有建立它的 request | `correlation(job)` 帶 payload 的 `request_id` | unit `TestTheAttemptsEvent`；DB `TestTheHandlersLinesCarryTheJob` |
| 沒有 traces／metrics | OpenTelemetry，預設 no-op、不 export（D-095、ADR-028） | unit telemetry、export、config、httpapi 的測試 |

## 稽核：scope 逐條（需求 → 實作 → 測試 → 證據）

| Scope | 判定 | 實作 | 測試與證據 |
|---|---|---|---|
| 每個 business write 同一 transaction 寫 audit（REQ-018） | 已完成；DB 部分 NOT RUN | catalogue 的 25 個 action（D-091 (a)），全部經 `auditlog`；worker loop 與 reaper 的 `job.attempt_failed`；provisioning 經 `AppendSystem` | Unit：`TestEveryActionFits`（每個 action 一個與呼叫處相同形狀的樣本）、`TestTheCatalogueMatchesTheCode`（production 中每個 action 字串都在 catalogue、catalogue 的每個 action 都有人寫；只有 `auditlog.go` 寫 `twin.audit_events`）、`TestTheAttemptsEvent`（fake tx：事件經給定的 transaction 寫入，DB 的錯誤原樣回傳）。DB：`TestAnUnwritableAuditEventCommitsNothing`（API 的 15 個 action：project.create、model.create、model.draft.update、model.publish、model.draft.reset、scenario.create、simulation.create、simulation.cancel、proposal.create、proposal.reject、proposal.approve、membership.create／update／delete、import.upload；`dbtest.RefuseAudit` 讓 DB 只拒絕該 organization 的 event，每個 request 500 且 12 種狀態（projects、models、versions、drafts、scenarios、runs、jobs、proposals、memberships、imports、idempotency key、audit 數）完全不變，之後同一 request 成功且恰好一筆 event）、`TestAnAuditFailureCommitsNothing`（worker：handler 的 Finish、loop 的 failure、reaper 都不 commit）、`TestFailedAttemptsAreAudited`、`TestTheReaperAudits` |
| Audit 只收 catalogue 的形狀、fail closed | 已完成 | `audit.Check`：actor 種類與格式、target 種類與 ID 前綴、`req_` request ID、至多 32 個欄位、每個欄位的形狀（ID、hash、計數、旗標、code、列舉、label）；錯誤只寫 action、欄位名與原因 | Unit：`TestRefused`（29 種，含 comment、名稱、intent、prompt、chain-of-thought、provider 的回答、patch、CSV 列、檔名、由資料組成的 key，以及不是 actor reference 的 worker／system actor；錯誤不含 sentinel）、`TestShapes`（16 種形狀錯誤；33 個欄位的錯誤指名 `metadata`）、`TestAnEventIsCheckedBeforeItIsWritten`（不符時沒有任何 SQL 送出） |
| Request／run／provider correlation | 已完成；DB 部分 NOT RUN | Audit：API 的 event 帶 response 的 request ID；job 的 event 帶建立 job 的 request（payload），沒有時另發並標 `uncorrelated`；`provider_request_id`。Log：context 的 correlation（D-093 (b)）。Trace：`geminus.request_id`、`geminus.job_id` | Unit：`TestTheAttemptsEvent`（6 種 job：各資源的 target、request 的對應、不是 request ID 的值不用）、`TestAuditMeta`、`TestAccessLog`（response、envelope、access 行與 route 的行是同一個 request ID；client 的 `X-Request-ID` 不用）、`TestAccessLogTelemetry`。DB：`TestTheHandlersLinesCarryTheJob`（handler 與 loop 的行帶 organization、job、type、attempt、import、request；job 的 span） |
| Structured JSON logging（slog） | 已完成 | `observability.NewLogger`：stderr 的 JSON lines、`component`；API 與 worker 都用它（`newLogger`） | Unit：`TestTheLogIsFiltered`（cmd/api）、`TestTheWorkersLoggerIsFiltered`、`TestTheWorkersLinesAreJSON`（cmd/worker）、`TestAccessLog`；DB：`TestRunServesAndStops`（真的 server 的 access 行） |
| Privacy filter（TEST-027，對抗性的值，含 error 路徑） | 已完成 | D-094：default deny 的 key 清單；值的形狀；自由文字移除 credential、URL user information、credential header、e-mail、長 base64、雙引號中的值（未閉合時其後全部）、控制與 bidi 字元，長度上限；panic 只記型別；struct／map／slice 一律 redacted；context 與 `With` 的值同樣過濾 | Unit（`internal/observability`）：以 sentinel（CSV 列與格、德文欄名、Bearer、JWT、`sk-ant-`、AWS key、PEM、帶密碼的 DB URL、cookie、`DB_PASSWORD=`、e-mail、prompt、chain-of-thought、provider 的回答、comment、Negotiate header、裸的 base64）經 attribute、error、`%q` 與 PostgreSQL 的訊息、未閉合的引號、panic、LogValuer、group、`With`、context、訊息本身各路徑，`TestValuesOfTheWrongShapeAreRedacted`、`TestUnregisteredKeysAreRedacted`、`TestTextIsSanitized`、`TestTextIsBounded`、`TestOtherValues`、`TestPanicText`、`TestContextValuesAreFiltered`、`TestWithAttributesAreFiltered`、`TestTheMessageIsSanitized`、`TestSensitive`；`TestRecover`（API panic）；`TestAttributesAreFiltered`（span 與 metric）；`TestExportWhenConfigured`（送到 collector 的 bytes 沒有 sentinel）；`config` 的錯誤不含值（`TestLoadTelemetry`、`TestRunRefusesABadTelemetryEndpoint`／`Setting`） |
| Traces／metrics，預設不 export、不選廠商 | 已完成 | D-095、ADR-028：OTel API 在 `telemetry`（預設 no-op、不用 global provider），SDK 只在 `telemetry/export`，只有 operator 設定 OTLP endpoint 時才建立 exporter；request 與 job attempt 是新的 root span，engine、provider call、import 是事後記錄的子 span | Unit：`TestNothingIsRecordedByDefault`、`TestOffByDefault`（沒有設定、`OTEL_SDK_DISABLED`、兩個 `none`：collector 收到 0 個 request）、`TestExportWhenConfigured`（loopback collector 收到 traces 與 metrics、`service.name`）、`TestExportFailuresAreLogged`（SDK 的錯誤經過濾的 log）、`TestLoadTelemetry`（10 種接受、9 種拒絕）、`TestRequests`、`TestJobs`、`TestRootsAndFailedWork`、`TestAccessLogTelemetry`（client 的 `traceparent` 不用） |
| 低基數 attribute、不含客戶內容 | 已完成 | metric 只用 11 個固定 key，從不帶 ID；值經 `observability.Fits` | Unit：每個 telemetry 測試的 `checkMetrics`（不在清單的 key 即失敗）、`TestAttributesAreFiltered`、`TestAccessLogTelemetry`（metric 不帶 request ID） |
| Job、simulation、LLM、import 的營運指標 | 已完成；DB 部分 NOT RUN | Log 行（D-095 (a)）：`job ended`／`job attempt ended`（duration）、`simulation engine ran`、`ai provider call ended`、`import profiled`／`import validated`；同樣的 metric 與 span | Unit：`TestTheCallsLine`（provider 的 label、token、latency、outcome code、job 的 correlation，奇怪的 label 被 redacted、不記 error 的文字）、`TestEngineOutcome`、`TestJobs`、`TestProviderCallsAndImports`。DB：`TestRunClosedLoop`（engine 行）、`TestImportMappingClosedLoop`（profile 與兩次 validation 的行，沒有欄名、格或檔名）、`TestTheHandlersLinesCarryTheJob`（`job ended`） |
| 依賴 pin、授權、go.sum | 已完成 | OTel v1.47.0 與其依賴、x/net v0.60.0；ADR-028 的表（版本、發布日、授權） | `go mod verify`、govulncheck、`licenses.txt`（沒有 copyleft） |
| Architecture 規則與 probe | 已完成 | 4 條新規則（`observability-is-a-leaf`、`audit-catalogue-is-pure`、`otel-sdk-only-in-export`、`telemetry-uses-the-api-only`）；`internal/observability/...` 為 application layer（kernel、domain pack、`internal/ai` 不能依賴）；`github.com/google/uuid` 為 `db-only-in-storage` 的 gateway | `TestProbesAreCaught` 的 19 個新 probe、`TestSyntheticGraph`、`TestEveryRuleEntryIsEnforced`；`make architecture-check` |

## 實際執行結果

在本 worktree 執行（`environment.txt`：Go 1.27.3、Python 3.13.16、Node 22.22.0、4 CPU 與其他 agent 共用、沒有 PostgreSQL server；先 `go clean -testcache`）。Exit code 也在 `exit-codes.txt`（`verify.py` 依序執行，每個指令一個 log）。

| 指令 | Exit | 結果 |
|---|---|---|
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `PASS_LIMITED_SPEC_CHECKS`：72 個正例、143 個反例 fixture（沒有變）；planning、decisions（proposed 14：D-073、D-081–D-085、D-088–D-095）、docs（105 個 markdown，含本報告）、TS 契約為最新（15 檔、109 型別）、`render_db_tests --check`（13 檔）都 PASS；完整的 OpenAPI 官方 validator 為 `NOT_RUN_UNAVAILABLE`（`spec-check.log`） |
| `make backend-test` | 0 | gofmt、`go vet`、`go test`（`GEMINUS_FULL_DIFFERENTIAL=1`）：71 個 package `ok`（含新的 `internal/audit`、`internal/observability`、`telemetry`、`telemetry/export`；`scripts/check-architecture` 193 秒），沒有 FAIL；DB-backed 測試全部 SKIP（`backend-test.log`） |
| `make architecture-check` | 0 | `PASS architecture: 82 packages, 21 rules, 16 build configurations`（T-035 前為 78 個 package、17 條規則）；19 個新 probe 在 `TestProbesAreCaught` 中通過（`make backend-test` 的 `scripts/check-architecture ok`；`architecture-probes*.log`） |
| `go test -race -count=1` 碰到的 15 個 package 路徑 | 0 | 17 個 package `ok`，沒有 data race 報告（`race-touched.log`） |
| `make backend-race` | 0 | `go test -race -timeout 30m`：71 個 package `ok`，沒有 FAIL、沒有 data race 報告（`backend-race.log`，502 秒） |
| `make ai-eval` | 0 | `TestFakeProviderEval`、`TestReviewEval` PASS；只有 fake provider；`checks/ai-evals/*.json` 沒有差異。T-035 改了 generator（`provider_request_id`、provider call 的 log 行），沒有改 AI context 或 review（`ai-eval.log`） |
| `go mod verify` | 0 | `all modules verified`（`mod-verify.log`） |
| `GOTOOLCHAIN=go1.27.3 go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./cmd/... ./internal/... ./schemas/... ./scripts/...` | 0 | `No vulnerabilities found`（`govulncheck.log`；x/net v0.59.0 時的 5 個弱點在 `govulncheck-x-net-v0.59.0.log`） |
| `python mutate.py <root> mutants-unit.log unit` | 0 | 第 3 輪：96 個 unit mutant 中 95 KILLED、1 SURVIVED（equivalent）、0 BUILD；第 1、2 輪見「Mutation」 |
| `python mutate.py <root> mutants-db-build.log dbbuild` | 0 | 12 個 DB mutant 都套得上且能 build（第一次有 2 個不能 build，改寫後重跑）；它們的測試 NOT RUN。這個模式本身一律 exit 0，結果在 log |
| `python redfirst.py <root> redfirst` | 0 | 見「先紅後綠」（`redfirst/summary.txt`） |
| `make integration-test`、`make db-test`、`make db-go-test` | — | NOT RUN（沒有 PostgreSQL；見下） |

`make frontend-test` **NOT RUN**：T-035 沒有改 `schemas/`、`openapi/`、`apps/web/` 或任何 TS（`git diff --stat 38f0b7b HEAD -- schemas openapi apps` 為空），依 coordinator 的指示不需要。

## 先紅後綠

- **寫測試時先失敗的**（實作過程中保留的 log）：`observability-red.log`（套件還沒有程式）、`audit-red.log`（同上；之後 `TestTheCatalogueMatchesTheCode` 失敗在 `job.attempt_failed`、`organization.provision` 還沒有 production 程式寫它們）、`auditlog-red.log`（沒有 `AppendSystem`）、`config-telemetry-red.log`（沒有 `LoadTelemetry`）、`generator-auditmeta-red.log`（行為上的 red：metadata 沒有 `provider_request_id`；奇怪的 provider request ID 讓 `proposal.attempt_failed` 被 catalogue 拒絕）、`httpapi-red.log`（沒有 `AccessLog`）、`architecture-probes*.log`（新 probe 的結果）。
- **事後對 base（38f0b7b）重跑的**（`redfirst.py`，`redfirst/summary.txt` 與每個 case 的 log）：不少測試與程式在同一段工作中一起寫成（telemetry、export、cmd、access log、worker 的 internal test），所以它們的 red 是事後把程式放回 T-035 之前的樣子再跑得到的。17 個 case 中 16 個 RED、1 個 NOT RED：
  - 行為上的 RED：`httpapi-request-layer`（base 的 request 層加上新的 access log：`TestRecover` 看到 panic 值被記下、`TestAccessLog` 的 access 行沒有 error code、organization、user，response 的 request ID 也不是 access 行的）、`httpapi-self-user`（base 的 `Self` 不把 user 放上 access 行）、`worker-id`（`1-worker`、`worker/1`、`wörker`、129 字元都被接受）、`cmd-api`（base 的 logger：`prompt` 沒有 redacted、sentinel 進了 log；`OTEL_EXPORTER_OTLP_ENDPOINT` 帶 credential 被忽略，以 exit 3（database）而不是 exit 1（config）結束）、`cmd-worker`（同樣兩種）、`audit-provisioning`（`provisioning.go writes twin.audit_events itself`）、`architecture-new-rules`（新規則的 12 個 probe 都沒有被擋下）。
  - 只是「程式還不存在」的 RED（build failed）：`observability`、`telemetry`、`export`、`config-telemetry`、`worker-attempt-event`、`generator-call-line`、`runner-engine-outcome`；DB 測試只能看 compile：`db-jobs-reapjobs`（沒有 `ReapJobs`）、`db-refuse-audit`（沒有 `dbtest.RefuseAudit`）。
  - NOT RED：`cmd-worker-json-lines`。原本名為 `TestTheWorkersLogIsFiltered` 的測試只檢查 JSON 行與 `component`，T-035 之前就成立；它的名字宣稱檢查 filter，所以改名 `TestTheWorkersLinesAreJSON`，另加真正檢查 filter 的 `TestTheWorkersLoggerIsFiltered`（e2b0d2b，RED 見 `cmd-worker`）。
- **Mutation 第 1 輪之後加的測試**（fc2b8b7）：每個都是因為一個存活的 mutant 而寫，第 2、3 輪該 mutant 被擋下（即測試在那個錯誤上是紅的）；另外 `TestSelfAnnotatesTheUser` 對 base 的 `scope.go` 是 RED（`redfirst/httpapi-self-user.log`），`TestPanicText`、`TestRootsAndFailedWork` 所在的 package 在 base 不存在（`observability`、`telemetry` 兩個 case）。
- **DB-backed 測試**沒有 red 也沒有 green（SKIP）；它們在本 worktree 只確認能編譯（`go vet`）。

## Mutation

`checks/evidence/2026-10-25-T-035/mutate.py`（照 T-029 的做法）：每個 mutant 替換確定的程式片段（必須恰好出現一次），先 `go vet`（不能 build 的記為 BUILD、不算擋下），再跑指定 package 的測試，之後還原。範圍：log filter（default deny、形狀、自由文字的每一條規則、長度、panic、LogValuer）、handler 與 context 的 correlation、audit catalogue 的每一項檢查、`auditlog` 的寫入前檢查、worker 的 audit 與 correlation、access log 與 request 層、telemetry 的 attribute 與 span、telemetry 設定與 export、provider call、engine 的 outcome。

- **第 1 輪**（`mutants-unit-r1.log`）：96 個中 77 KILLED、17 SURVIVED、2 BUILD。存活的與處理（fc2b8b7，測試或程式）：`route-with-any-path`（route 帶 query 或空白）、`credential-headers-kept`（既有 sentinel 都同時被 key=value 規則擋下：加 Negotiate 與非 credential 名稱的 cookie）、`long-base64-kept`（加裸的 base64）、`runtime-error-unsanitized`（任何型別都能宣稱是 runtime error：`TestPanicText`）、`panicking-logvaluer-crashes`（equivalent：slog 的 `Value.Resolve` 自己 recover，所以拿掉多餘的 recover，並以測試記下 slog 的行為；這個 mutant 隨程式一起移除）、`group-name-unchecked`、`with-shares-its-backing-array`（有剩餘容量的 slice 的 sibling）、`annotations-not-copied`、`worker-actor-unchecked`、`metadata-unbounded`（33 個欄位的錯誤要指名 `metadata`）、`status-zero-logged`（什麼都不寫的 handler）、`writer-not-unwrapped`（經真的 server 設 write deadline）、`self-user-not-annotated`（`TestSelfAnnotatesTheUser`）、`job-span-not-a-root`、`request-span-not-a-root`、`engine-span-never-failed`（`TestRootsAndFailedWork`）、`no-pre-cut`（見下）。2 個 BUILD（`text-unbounded`、`worker-id-unchecked`）改寫成能編譯的形式。檢查這些 mutant 時另外發現：**沒有閉合的雙引號**之後的值原本不會被隱藏（例如值本身帶 `"`，或文字被截短），改為其後全部隱藏（新 mutant `unterminated-quote-kept`）。
- **第 2 輪**（`mutants-unit-r2.log`）：93 KILLED、2 SURVIVED、1 BUILD；剩下的是 mutate.py 本身的問題：`unregistered-key-kept` 引用了已移除的 `resolve`、`self-user-not-annotated` 的測試清單漏了新測試。只改 mutate.py，沒有改程式或測試。
- **第 3 輪（最終）**（`mutants-unit.log`）：**96 個中 95 KILLED、1 SURVIVED、0 BUILD**。存活的 `no-pre-cut` 是 equivalent：`preCut`（64 KiB）只限制 pattern 掃描的文字量，之後 sanitize 本來就把文字截到至多 `MaxStack` 個 rune；截斷點切開一段引號時，未閉合引號的規則同樣隱藏其後全部。它限制的是 CPU，沒有輸出上的差異可以測。
- **DB mutant**（`mutants-db-build.log`）：12 個（loop 不 audit failure、reaper 不 audit、handler 的行沒有 correlation、reap 的 requeued 顛倒、reap 的順序顛倒、`project.create` 與 `simulation.create` 不 audit、provisioning 以 worker 身分 audit、engine／profile／validation 的營運行消失、`cmd/api` 不接 access log）。12 個都套得上且能 build（第一次 `db-project-not-audited`、`db-run-not-audited` 把函式放在 import 之前而不能 build，改為就地的 function literal 後重跑）；它們的測試 **NOT RUN**（`mutate.py <root> <log> db` 沒有 `GEMINUS_TEST_DATABASE_URL` 時拒絕執行）。
- **不列為 mutant**：「呼叫者忽略 audit 的錯誤」在 DB 層是 equivalent：PostgreSQL 在失敗的 INSERT 之後讓 transaction 進入 aborted 狀態，COMMIT 一定 rollback，所以 DB mutant 改為整個不寫 audit。Tenant 的 SQL 過濾沒有新 SQL（`ReapJobs` 沿用 `Reap` 的 UPDATE 與 organization 條件），沒有 SQL mutant。

## Migration、RLS 與 grant 的影響

沒有 migration，沒有改任何 migration。

- `twin.audit_events` 的寫入欄位與 role 不變；`actor_ref`、`guard_audit_actor` 等 DB 規則不變（D-092 選的就是 DB 已經強制的格式）。Provisioning 的 event 改經 `auditlog`：同樣的 actor、action、target、自己發的 request ID，metadata 由 `jsonb_build_object` 改為 `json.Marshal` 的 `{"owner_user_id": ...}`（相同內容）。
- `jobs.ReapJobs` 是同一個 UPDATE，`RETURNING` 由 `j.status` 改為 job 的欄位（`qualified`，與 claim 的 RETURNING 相同的欄位）；worker role 本來就對 `twin.jobs` 有 SELECT（claim 需要）。
- Worker 的 `job.attempt_failed` 由 worker role 寫 worker event（0004 已允許）；audit 由 loop 在既有的 `jobs.Fail`／reap transaction 中寫，沒有新的 transaction。
- `dbtest.RefuseAudit` 只在測試 database 以 `twin_owner` 建立、cleanup 時移除一個只拒絕該 organization 的 trigger。
- 沒有新的 grant、table、欄位或 RLS policy。

## 依賴與授權（ADR-028，PROPOSED）

全部 pin 在 `go.mod`／`go.sum`，`go mod verify` 通過；發布日都在 2026-10-11 之前（ADR-011 的兩週規則，以 proxy.golang.org 的 `.info` 為準）；授權取自各 module 自己的 LICENSE（`licenses.txt`），**沒有 copyleft、沒有不明的授權**。

| Module | 版本 | License |
|---|---|---|
| `go.opentelemetry.io/otel`、`/trace`、`/metric`、`/sdk`、`/sdk/metric`、`/log`、`/exporters/otlp/otlptrace`、`/otlptrace/otlptracehttp`、`/otlpmetric/otlpmetrichttp` | v1.47.0（2026-10-02） | Apache-2.0 |
| `go.opentelemetry.io/auto/sdk` | v1.2.1 | Apache-2.0 |
| `go.opentelemetry.io/proto/otlp` | v1.11.0 | Apache-2.0 |
| `google.golang.org/grpc` | v1.83.2 | Apache-2.0（另有 NOTICE.txt） |
| `google.golang.org/genproto/googleapis/api`、`/rpc` | v0.0.0-20260928230214-8a89bd6388cc | Apache-2.0 |
| `google.golang.org/protobuf` | v1.36.12 | BSD-3-Clause |
| `github.com/grpc-ecosystem/grpc-gateway/v2` | v2.30.0 | BSD-3-Clause |
| `github.com/cenkalti/backoff/v5` | v5.0.3 | MIT |
| `github.com/cespare/xxhash/v2` | v2.3.0 | MIT |
| `github.com/go-logr/logr` | v1.4.4 | Apache-2.0 |
| `github.com/go-logr/stdr` | v1.2.2 | Apache-2.0 |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause |
| `golang.org/x/net` | v0.60.0（2026-10-08；OTel 要求的 v0.59.0 有 5 個 govulncheck 的弱點，`govulncheck-x-net-v0.59.0.log`） | BSD-3-Clause |
| `golang.org/x/sys` | v0.48.0 | BSD-3-Clause |

`docs/THIRD_PARTY_NOTICES.md` 已加上這些 module。只有 `cmd/api`、`cmd/worker` 經 `telemetry/export` 連結 SDK、exporter、protobuf 與 gRPC（規則 `otel-sdk-only-in-export`）；generic kernel、domain pack、`internal/ai` 都不連結 OTel。

## NOT RUN

完整清單在 `checks/evidence/2026-10-25-T-035/db-not-run.txt`，原因都是本 worktree 沒有 PostgreSQL（coordinator 規定不安裝、不啟動）：

- `make integration-test`、`make db-test`、`make db-go-test`。
- 新的 DB-backed 測試：`internal/api` 的 `TestAnUnwritableAuditEventCommitsNothing`；`internal/worker` 的 `TestFailedAttemptsAreAudited`、`TestTheReaperAudits`、`TestAnAuditFailureCommitsNothing`、`TestTheHandlersLinesCarryTheJob`；`internal/storage/jobs` 的 `TestReapJobsReturnsWhatItTook`。
- 加了 T-035 assertion 的既有 DB 測試：`TestRunClosedLoop`、`TestImportMappingClosedLoop`（`internal/api`）、`TestRunServesAndStops`（`cmd/api`）。
- 程式路徑被 T-035 改到的既有 DB 測試：每個寫 audit event 的測試（寫入前多了 catalogue 檢查）、provisioning 的測試、jobs 的 reap、`cmd/worker` 的 DB 測試。
- 12 個 DB mutant 的測試。
- `make frontend-test`（沒有 contract 或 TS 的變更，見上）、`make e2e-test`、`make ai-eval-live` 與任何 live provider；送到真的 OTLP collector 或廠商（只測了 loopback 的測試 collector）；完整的 OpenAPI 官方 validator（`spec-check` 的 `NOT_RUN_UNAVAILABLE`）。

## 提出的決策（PROPOSED，agent 沒有批准任何一項）

| ID | 主題 | 建議（已實作） | 其他選項（不採用時改什麼） |
|---|---|---|---|
| D-091 | 哪些寫入要 audit、catalogue、job 失敗的 audit、fail closed、provider correlation | (a) business write＝建立、改內容、決定或結束 tenant 資源（含一次 attempt 的失敗），同一 transaction 恰好一筆；營運寫入（claim、heartbeat、progress、event budget、PROFILING、idempotency row）不 audit；(b) loop 與 reaper 的 `job.attempt_failed`（target 為 job 的資源、建立 job 的 request、沒有 failure message）；(c) 封閉 catalogue、寫入前檢查、fail closed、錯誤不含值、只有 `auditlog` 寫入；(d) `provider_request_id`，不是 label 時不記 | (a) B 營運寫入也 audit、C 只 audit 使用者；(b) B 每種資源各自的 action、C target 為 job；(c) B 只在測試檢查、C 丟掉不認得的欄位 |
| D-092 | Worker／system actor 的格式；ADR-024 的 `WORKER_ID` | (a) actor reference（`twin.actor_ref` 的 pattern），worker 為 `WORKER_ID`、system 為 `provision-tenant`，`worker.New` 要求它；(b) 與 ADR-024（ACCEPTED）的「1–200 個可見 ASCII」不一致：**不改** config，建議 owner 修改 ADR-024 並採用 `d092-config-proposal.diff`（`config.LoadWorker` 以 `WORKER_ID` 的錯誤碼更早拒絕；它的 red 在 `d092-config-red.log`） | (a) B 有結構的名稱、C worker 登記表（P1） |
| D-093 | Log 的格式、欄位、correlation、access log | stderr 的 JSON lines、登記的 key；request 的行帶 `request_id`（解析後加 organization、user 的不透明 ID）；job 的行帶 job、type、attempt、資源與建立它的 request；每個 request 一行 access log（route pattern，不記 path、query、header、body），5xx 為 WARN；保存期限不在本項（T-039） | (b) B 不記 `user_id` |
| D-094 | Log 的隱私規則 | default deny 的 key 清單、值的形狀、自由文字的 sanitize（credential、URL user information、credential header、e-mail、長 base64、雙引號中的值與未閉合引號之後、控制與 bidi 字元、長度上限）、panic 只記型別、其他值 redacted；同一套形狀用於 trace 與 metric | B key 黑名單；C 不集中過濾 |
| D-095 | 營運指標與 trace、export 預設關閉 | (a) 以 log 記錄的 job、simulation、LLM、import、HTTP 指標；(b) OpenTelemetry SDK 與 OTLP／HTTP exporter，只有 operator 設定 endpoint 時才 export，沒有預設 endpoint，`OTEL_SDK_DISABLED`／`none` 關閉，endpoint 不帶 credential、query、fragment；(c) metric 只用 11 個低基數 attribute、不帶 ID，span 另帶 request／job 的不透明 ID，不接受 client 的 trace context；(d) SDK 預設取樣、resource 只有 `service.name` | B P0 只用 (a)、OTel 延到 T-039；C Prometheus pull endpoint |
| ADR-028 | OpenTelemetry 的依賴、package 邊界與設定 | 見上表與 ADR；狀態 PROPOSED | 不採用時移除 `telemetry`／`export` 的 SDK 部分與依賴，保留 D-095 (a) 的 log 指標 |

T-035 的 `decision_gates` 為 D-091–D-095；D-091、D-094 另加進 T-036（security 測試會以它們為準），D-093、D-095 另加進 T-039（部署與 OTLP adapter）。這兩個任務的 status 沒有改。`planning/traceability.csv` 的 REQ-018、REQ-020 本來就列出 T-035 與 TEST-027，沒有需要改的。

## 與指示或規劃的差異

- **`TestRecover` 的 assertion 反轉**：T-035 之前它要求 panic 值（`boom`）出現在 log 中；D-094 (d) 禁止記 panic 值，所以改為要求值**不**出現、型別、request ID 與 stack 出現，並加上 runtime error 的訊息保留。這是更嚴的檢查，不是降低。
- **`TestNewChecksTheConfig`**：合法 ID 由 200 字元改為 128 字元的 actor reference，並加 4 個拒絕的例子（`worker.New` 依 D-092 (a) 收緊）。ADR-024 的 config 仍接受 1–200 個可見 ASCII，所以不是 actor reference 的 `WORKER_ID` 通過 `config.LoadWorker`、在 `worker.New`（以及原本就檢查的四個 handler constructor）被拒絕（D-092 (b)）。
- **ADR-024 的矛盾**：依指示停在那一部分、記錄為 D-092 (b)，config 沒有改；提議的修改在 `d092-config-proposal.diff`。
- **ed9f796 單獨不能通過 `./scripts/...` 的測試**：新規則的 meta-test（`TestSyntheticGraph`、`TestEveryRuleEntryIsEnforced`）在 6e989a4 才修正；cherry-pick 時兩者要一起。
- **Access log 在 `cmd/api` 包在 `api.Handler` 外**（D-093 (c)），所以經 `api.Handler` 的整合測試沒有 access 行（`TestRunServesAndStops` 經真的 server 檢查）。
- **`db-only-in-storage` 的 gateway 加上 `github.com/google/uuid`**（OTel SDK 的 resource 經它 import `database/sql/driver`，不開連線）。
- **planned outputs**：T-035 的 `planned_outputs` 加上 `internal/observability/telemetry/`、`telemetry/export/`、`internal/httpapi/access.go`。
- **多數測試的 red 是事後產生的**（見「先紅後綠」），不是每個都在寫程式之前跑過。
- **`TestTheWorkersLogIsFiltered` 改名** `TestTheWorkersLinesAreJSON`（它檢查的東西在 T-035 之前就成立），另加真正的 filter 測試。

## 殘餘風險與剩餘工作

- **DB 測試沒有在 agent 端執行**：catalogue 檢查現在在每個 audited write 之前；某個呼叫處的 metadata 值若有 `TestEveryActionFits` 的樣本沒有涵蓋的型別或形狀，那個寫入會 fail closed（500）。Coordinator 的 DB 執行是這件事的檢查；第一次在 database 上跑時可能需要修正 catalogue 或測試。
- **`import.map` 不在 API 的 refusal 測試中**（它的 event 由 `TestImportMappingClosedLoop` 檢查，但「event 寫不進去時什麼都不 commit」沒有）；worker 端的 refusal 只以一個 fake handler 的 Finish 檢查，沒有逐一跑 profiler、validator、generator、runner 的 handler。
- **沒有引號的值**：error 的自由文字中沒有引號的值無法與 error 自己的文字區分（D-094 (c)）；handler 不得把資料放進 error 的規則（ADR-024）仍是唯一的防線。
- **`listAuditEvents`**（SOURCE 的 route）在 tasks.json 沒有任務負責；audit feed 的讀取還沒有實作。
- **保存期限、存放位置與存取權限**（log 與 telemetry）是原報告的 TBD，屬 T-039；告警規則屬 T-036（access log 的 `error_code` 是依據）。
- **取樣**：SDK 預設 ParentBased(AlwaysOn)，而每個 request 是新的 root，所以 export 開啟時每個 request 都有 trace；量大時 operator 需要設定 `OTEL_TRACES_SAMPLER`。
- **Export 只測了 loopback 的測試 collector**，沒有對任何廠商或真的 collector；`proto/otlp` 帶進 gRPC 與 protobuf（只有 `cmd` 的 binary 連結，binary 變大）。
- **效能**：每個自由文字的 attribute 經約 15 個 regexp（只在 error、panic、stack 等行）；沒有 benchmark（T-037）。
- **D-091–D-095、ADR-028 待 owner 決定**；選其他選項時依各項「不採用時」修改。

## Coordinator 的執行（f9365b8＋測試修正）

`t035` 從 38f0b7b 開出，所以 coordinator 以 fast-forward 把 13 個 commit 接到 `claude/lucid-meitner-vq1awo`，hash 不變（ed9f796 … f9365b8）。

### 第一次執行（f9365b8，`coordinator-*-first-run.*`）

下列指令 exit 0：
- `spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`frontend-test`、`simulation-reference-test`、`backend-numeric`、`backend-race`；
- 另一個新的 `dt_test_t035m` 上：
  - 新與改過的 DB 測試（`GEMINUS_REQUIRE_DB_TESTS=1`，`-v`）全部 PASS：`TestAnUnwritableAuditEventCommitsNothing`、`TestFailedAttemptsAreAudited`、`TestTheReaperAudits`、`TestAnAuditFailureCommitsNothing`、`TestTheHandlersLinesCarryTheJob`、`TestReapJobsReturnsWhatItTook`、`TestRunClosedLoop`、`TestImportMappingClosedLoop`、`TestRunServesAndStops`（`coordinator-new-db-tests.log`）；
  - 12 個 DB mutant 全部被測試擋下（`coordinator-mutants-db.log`）。

兩項失敗：
1. **`integration-test`（`dt_test_t035`）exit 2**：只有 `internal/storage/jobs` 的 `TestEnqueueWithTheResource` 失敗，訊息為 `audit: import.create: action: not in the catalogue`。
   - 這個 T-019 的既有測試以一個實際程式不存在的 action（`import.create`；upload 寫的是 `import.upload`）作為與入列同一 transaction 的 audit。T-035 的 catalogue 依 D-091 fail closed，所以擋下它。這正是實作者在「風險」中預告的情形，是測試資料的錯，不是產品行為。
   - Coordinator 把它改為 catalogue 中的 `import.upload`（metadata 都是選填）。測試的意圖不變：失敗的 request 不留下 import、job 與 audit，成功時三者都在。沒有降低任何 assertion。
   - 另以腳本比對所有 `_test.go` 的 `Action:` 字面值與 catalogue：另一個不在 catalogue 中的 `project.delete`（`auditlog_test.go`）是刻意的反例（「an unknown action」必須被拒絕），保留。
2. **govulncheck exit 1**：coordinator 腳本的錯誤，不是弱點。`GOTOOLCHAIN=local` 讓 PATH 上的 go1.24.7 執行，`golang.org/x/vuln@v1.9.0` 要求 go ≥ 1.26，所以沒有掃描。

### 重跑（f9365b8＋上述一行測試修正）

| 指令 | Exit | 說明 |
|---|---:|---|
| `make spec-check` | 0 | |
| `make backend-test` | 0 | |
| `make integration-test`（新的 `dt_test_t035b`） | 0 | 34 個 package ok，沒有 FAIL |
| `govulncheck@v1.9.0 ./cmd/... ./internal/... ./schemas/... ./scripts/...`（module cache 中的 go1.27.3，`GOTOOLCHAIN=local`，與 CI 相同的指令） | 0 | No vulnerabilities found，包括新加入的 OpenTelemetry、gRPC、protobuf 與 x/net v0.60.0 |

測試修正只改一個 DB 測試的 action 字面值。第一次執行中 exit 0 的 unit、race、numeric 結果不受影響：該測試在沒有 database 時 SKIP。

Log 與 exit code 在 `checks/evidence/2026-10-25-T-035/coordinator-*`。

仍為 NOT RUN：`make e2e-test`、live AI、送到真的 OTLP collector、OpenAPI 官方 validator、Node 24 上的 `frontend-test`（CI 執行）。

D-091–D-095 與 ADR-028 仍是 PROPOSED。D-092 與已 ACCEPTED 的 ADR-024 的不一致，留給 owner 決定。T-035 在 tasks.json 仍為 `not_started`。

## Review 第 1 輪（7cbde1e：CHANGES REQUESTED，1 Medium、7 Low、Info）

Reviewer 獨立確認了 audit 的完整性：16 個會寫入的 route 與每個 worker 路徑都在同一 transaction 寫 audit，catalogue 沒有拒絕任何合法的 production event。修正在新 branch `t035-r1`（從 7cbde1e 開出，沒有 push）。Log 在 `checks/evidence/2026-10-26-T-035-r1/`。

### Commit

| Commit | 內容 |
|---|---|
| 4ceb306 | M1、L6、L7：export 先接 OTel 的 error handler 與 logger 才建 exporter；`config.LoadTelemetry` 檢查三個 `*_HEADERS`；endpoint 只接受 https 或本機的 http；SDK 的 diagnostics 以可讀、過濾後的形式寫入 log |
| 2349682 | L1–L5、I7：其他引號、只信任 Go runtime 的 error、group 外的 correlation、`failureOutcome`，以及 mutant 找到的沒有測試的界限；reviewer probe 的額外修正（格式字元、session 與簽章參數） |
| 586efbb | I8：architecture 規則的 `bring` 欄位，uuid 只能帶進 `database/sql/driver`；`observability-is-a-leaf` 允許 `reflect` |
| 047622f | 文件：D-091、D-092、D-094、D-095、ADR-028、SECURITY §9、T-036 的 scope（都仍是 PROPOSED） |
| 143941d | Mutation 找到的缺口：行與段落分隔字元（U+2028、U+2029）的測試；測試中的不可見與 typographic 字元改寫為 Go escape |
| （本 commit） | 本節與 `checks/evidence/2026-10-26-T-035-r1/` |

### 發現 → 修正 → 測試 → 證據

| 發現 | 修正 | 擋下它的測試（先紅） | 證據 |
|---|---|---|---|
| **M1**（Medium）：exporter 在 `otel.SetErrorHandler`／`SetLogger` 之前建立；`OTEL_EXPORTER_OTLP_HEADERS` 解析不了時，OTel 預設的 stdr 把原始值（含 credential）以非 JSON 寫到 stderr，每個 exporter 一次 | (1) `export.Setup` 在建立任何 exporter（以及 resource、provider）之前就安裝 error handler 與 logger；(2) `config.LoadTelemetry` 以 exporter 的同一套規則（逗號分隔、`=`、trim 後的 name 是 HTTP token、value 可 `url.PathUnescape`）檢查 `OTEL_EXPORTER_OTLP_HEADERS`、`_TRACES_HEADERS`、`_METRICS_HEADERS`，不合時拒絕啟動，錯誤只寫變數名稱。文字：`internal/config/telemetry.go` 的說明、ADR-028 的 export 列與設定段、D-095 (b)（DECISIONS_AND_TBD、decisions.json） | `TestTheSDKWritesNothingRawToStderr`：把 test binary 當成新的 process 執行（`TestMain` 的 helper，OTel 的 logger 仍是預設），讀它**真的 stderr**，兩種格式錯誤（沒有 `=` 的 `Authorization: Bearer …`、沒有 URL-encode 的 `%ZZ`）：沒有 sentinel、每一行都是過濾後的 JSON、有一行說明 SDK 解析不了什麼。紅：`export-stderr-red.log`（stdr 的原始行帶 `HDR-SENTINEL`）。`TestLoadTelemetry` 的 6 種 header 拒絕與 1 種接受、`TestRunRefusesABadTelemetryEndpoint`／`Setting`（cmd）：紅在 `config-red.log`、`cmd-red.log` | mutant `r1-sdk-logger-not-installed`、`r1-headers-unchecked`、`r1-header-escape-unchecked`、`r1-header-name-any`、`r1-header-name-non-ascii` |
| **L1**：free-text key 下的 group 沒有測試（`group-under-text-kept` 存活；程式本身正確） | 不改程式 | `TestOtherValues`：`slog.Group("error", …)`，以及解析成 group 的 LogValuer 放在 `stack`、`panic` 下，都是 `[redacted]`（測試在現行程式上通過，由 mutant 證明它擋得下） | `r1-group-under-text-kept` |
| **L2**：只有雙引號算值的邊界；單引號、backtick、“ ” 會漏 | 加上 backtick、“ ”、‘ ’（閉合，或沒有閉合時到結尾），以及在字的開頭開啟、字的結尾關閉的單引號（字中的 apostrophe 不算：don't、job's、workers'）。沒有引號的值仍是殘餘風險：記在 D-094 (c)，並在 T-036 的 scope 加上後續：對 handler package 中以 `%s`／`%v` 格式化非 ID 值的 error 做 lint | `TestTextIsSanitized`（9 種新的引號情形）、`TestQuotesKeepWords`（縮寫與所有格不被隱藏；代價也寫進測試：pgx 放在 backtick 中的 user 與 database 會被隱藏）。紅：`observability-red.log` | `r1-single-quotes-kept`、`r1-apostrophes-as-quotes`、`r1-quote-closes-inside-words`、`r1-backticks-kept`、`r1-curly-kept`、`r1-curly-single-kept`、`r1-unclosed-backtick-kept` |
| **L3**：`PanicText` 信任任何有 `RuntimeError()` method 的型別 | `observability.RuntimeError`：只有型別宣告在 package `runtime` 本身的 error（`reflect` 的 PkgPath；pointer 先 unwrap）；其他型別一律只記型別。Worker 的 `panicked.Error` 用同一個規則。原本刻意接受舊行為的測試已改 | `TestPanicText`（假的 runtime error、它的 pointer、nil、真的 index out of range、nil map、`*runtime.TypeAssertionError`）、`TestPanickedTrustsOnlyTheRuntime`（worker）。紅：`observability-red.log`、`worker-panicked-red.log` | `r1-runtime-error-by-method`、`r1-runtime-pointer-not-unwrapped`、`r1-worker-trusts-the-method` |
| **L4**：沒有測試的界限（`pointer-bound-is-text`、`access-status-last-write`、`empty-label-in-list`、`endpoint-force-query`） | 不改程式：程式本來就是對的，例如 label 清單本來就拒絕空字串 | `TestTextIsBounded`（JSON Pointer 截在 256）、`TestAccessLogKeepsTheWriter`（WriteHeader 兩次，記第一個）、`TestShapes`（清單中的空 label）、`TestLoadTelemetry`（只以 `?` 結尾的 endpoint） | `r1-pointer-bound-is-text`、`r1-access-status-last-write`、`r1-empty-label-in-list`、`r1-endpoint-force-query` |
| **L5**：outcome 與 status 沒有測試（`lease-lost-outcome-dropped`、`job-span-error-only-failed`） | 把記錄 failure 之後的 outcome 抽成 `failureOutcome`（lease_lost、not_recorded、requeued、failed） | `TestFailureOutcome`（紅：`worker-red.log`，函式還不存在）、`TestJobOutcomes`（6 種 outcome 的 span status 與 metric） | `r1-lease-lost-outcome-dropped`、`r1-not-recorded-as-failed`、`r1-job-span-error-only-failed` |
| **L6**：接受 http 的 OTLP endpoint，credential 會以明文送出 | 只接受 https，或 host 為 `localhost`、127.0.0.0/8、`::1` 的 http。D-095 (b) 與 ADR-028 已改（仍 PROPOSED） | `TestLoadTelemetry`：4 種本機的 http 接受；另一台 host、`localhost.example.com`、私有位址都拒絕。`TestRunRefusesABadTelemetryEndpoint`／`Setting`。紅：`config-red.log`、`cmd-red.log` | `r1-http-anywhere`、`r1-loopback-any-ip`、`r1-loopback-by-prefix` |
| **L7**：SDK 的 diagnostics 用 `err` 這個 key（沒有登記，一律 redacted），Warn（V(1)）在 INFO 被丟掉 | `diagnostics` handler（logr 與過濾後的 log 之間）：`err` 改為 `error`（自由文字，經過濾）；V(1) 為 WARN；V(4)／V(8) 不記；訊息以 `telemetry: ` 開頭；其他 key（`input`、`value` 等原始值）仍是 redacted | `TestDiagnostics`（internal，紅＝`diagnostics` 還不存在）；`TestTheSDKWritesNothingRawToStderr` 要求 SDK 的那一行帶有可讀的 `error` | `r1-diagnostics-err-unrenamed`、`r1-diagnostics-warn-dropped`、`r1-diagnostics-debug-logged`、`r1-diagnostics-unprefixed`、`r1-diagnostics-with-values-unrenamed` |
| **I2／I3** | D-091 的文字：一次失敗的 proposal attempt 有兩筆 event（generator 盡力寫的 `proposal.attempt_failed`，以及 loop 的 `job.attempt_failed`）；reaper fail closed（audit 寫不進去時，該 organization 過期的 lease 不收回）。T-036 的 scope 加上對 `reaping failed` 告警 | 文件 | DECISIONS_AND_TBD、decisions.json、tasks.json |
| **I6** | SECURITY §9 改為引用 coordinator 的 DB 執行（7cbde1e） | 文件 | `docs/SECURITY.md` |
| **I7**：`WithGroup` 讓 correlation 跑進 group 裡 | Handler 記住 group 與 group 中的 `WithAttrs`，Handle 時在 correlation 之後才開 group，所以 correlation 一律在最外層；沒有 group 時行為不變 | `TestCorrelationStaysAtTheTop`：兩層 group 與 group 中的 `With`；request_id、job_id、organization_id 各在最外層出現一次；group 中與 context 同名的 key 屬於 group。紅：`observability-red.log` | `r1-grouped-correlation-dropped`、`r1-correlation-inside-groups`、`r1-record-keys-suppress-grouped-correlation` |
| **I8**：對 `github.com/google/uuid` 的豁免涵蓋它整個 closure | 規則引擎加上 `bring`：一個 package 只能帶進列出的 forbidden package，其他依賴照常檢查；`db-only-in-storage` 改用 `bring: {uuid: [database/sql/driver]}`（原本的 `through` 是精確到 package 的 gateway，但會跳過它全部的依賴） | `TestTheUUIDGatewayBringsOnlyTheDriver`：經由 uuid 連到 `database/sql`、pgx 都是 violation；直接 import driver 也是；只帶 driver 時不是。紅：`architecture-red.log` | `r1-uuid-brings-anything`；`make architecture-check` PASS |
| **D-092** | 加一句給 owner：`WORKER_ID` 的唯一性沒有任何地方強制 | 文件 | DECISIONS_AND_TBD、decisions.json |
| Reviewer 的 probe（`obs_probe_test.go.txt`）另外顯示的洩漏 | 格式字元（zero-width、bidi）在 credential pattern 之前先移除；`JSESSIONID`、`PHPSESSID`、`X-Amz-Signature`、`sig` 參數視為 credential | `TestTextIsSanitized` | probe 修正前後：`reviewer-obs-probe-before.txt`、`reviewer-obs-probe-after.txt`（單引號、typographic、backtick、JSESSIONID、AWS 簽章、zero-width、假的 runtime error 由 leak 變為不 leak）。仍會 leak 的都是設計上的殘餘：沒有引號的值、符合形狀的名稱或 token、由資料組成的小寫 group 名稱、`?code=`、stack／pointer／addr 的自由文字 |

### Mutation

`checks/evidence/2026-10-26-T-035-r1/mutate.py`：以 2026-10-25 的 harness 為基礎，更新修正移動過的 snippet，加入 reviewer 存活的 mutant（套在修正後的程式上），以及新程式的 mutant（名稱以 `r1-` 開頭）。

- **主要一輪**（`mutants-unit.log`，在 143941d 之前的程式上）：136 個 unit mutant 中 132 KILLED、4 SURVIVED、0 BUILD。
  - 包括 reviewer 存活的 8 個（`r1-group-under-text-kept`、`r1-pointer-bound-is-text`、`r1-access-status-last-write`、`r1-empty-label-in-list`、`r1-gotype-may-be-a-credential`、`r1-endpoint-force-query`、`r1-lease-lost-outcome-dropped`、`r1-job-span-error-only-failed`），**全部 KILLED**。
  - 名稱以 `r1-` 開頭的 39 個中 38 KILLED（`r1-invalid-utf8-kept` 是 equivalent，見下）。
- **存活的 4 個**：
  - `line-separators-kept`：沒有測試，加上 U+2028／U+2029 的測試（143941d）；重跑 KILLED（`mutants-unit-rerun.log`，同時重跑了 5 個相關的 mutant，都 KILLED）。
  - `no-pre-cut`：equivalent，理由同上一輪。
  - `runtime-error-unsanitized`：equivalent。L3 之後只有 package `runtime` 自己的 error 會到這裡，它們的訊息由 runtime 寫成，只有型別名稱與數字。sanitize 是縱深防禦，沒有可測的差異：沒有 runtime error 會帶 credential。
  - `r1-invalid-utf8-kept`：equivalent。接下來的格式字元那一輪是 `strings.Map`，它本來就把每個 invalid byte 寫成 U+FFFD（`TestTextHelper` 的 invalid UTF-8 在兩種程式上都通過）。
- **Reviewer 的 `with-keys-not-tracked`** 因 snippet 移動而沒有套上，移植為 `r1-with-keys-not-tracked`：KILLED（`mutants-unit-rerun2.log`）。
- **最終**：137 個 unit mutant 中 134 KILLED，3 個 equivalent，0 BUILD。
- **Reviewer 的 harness**（`reviewer/run.py`，以 `go test -overlay` 不改 worktree）在 143941d 上重跑（`reviewer-mutants-after.log`；修正前為 `reviewer-mutants-before.log`）：25 個中 19 KILLED、3 SURVIVED、3 NOT APPLIED。
  - NOT APPLIED 的是 snippet 被修正移動的 `with-keys-not-tracked`、`endpoint-force-query`、`lease-lost-outcome-dropped`；移植版都 KILLED。
  - SURVIVED 的 3 個見下。
- **DB mutant**：12 個都套得上且能 build（`mutants-db-build.log`），測試 NOT RUN。

Reviewer harness（`reviewer/run.py`）中，不在要求的 8 個之內、修正前也存活的 mutant：
- `retryable-always-true`：由新的 `TestTheAttemptsEventOfEveryType` 擋下（reviewer 的 harness 與 `r1-retryable-always-true` 都是 KILLED）。
- `invalid-utf8-kept`：equivalent，理由同 `r1-invalid-utf8-kept`。
- `endpoint-opaque`：equivalent。`url.Parse` 只在 scheme 之後沒有 `//` 時設定 `Opaque`，這時沒有 authority，`Host` 為空，`validEndpoint` 本來就拒絕。
- `loop-audit-error-ignored`：沒有 unit 測試擋下它。
  - DB 的錯誤在 DB 層是 equivalent：失敗的 INSERT 讓 PostgreSQL 的 transaction 進入 aborted，COMMIT 一定 rollback。
  - catalogue 的拒絕（沒有送出 SQL）只會出現在 loop 自己的 event 不合 catalogue 時。`TestTheAttemptsEventOfEveryType` 顯示 6 種 job type 的 event 都合，worker ID 由 `worker.New` 檢查。
  - coordinator 的 DB mutant `db-failure-not-audited` 已擋下「不寫 audit」。

### 指令與 exit code（`checks/evidence/2026-10-26-T-035-r1/exit-codes.txt`）

在 143941d 上執行（`verify.py`；先 `go clean -testcache`；Go 1.27.3）：

| 指令 | Exit | 結果 |
|---|---|---|
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `PASS_LIMITED_SPEC_CHECKS`；proposed 14；重產的 `checks/*.json` 沒有差異（`spec-check.log`） |
| `make backend-test` | 0 | gofmt、`go vet`（含 DB 測試的編譯）、`go test`：71 個 package `ok`，沒有 FAIL；DB-backed 測試 SKIP（`backend-test.log`） |
| `make architecture-check` | 0 | `PASS architecture: 82 packages, 21 rules, 16 build configurations`（`architecture-check.log`） |
| `go test -race -count=1` 碰到的 12 個 package 路徑（含 `./scripts/check-architecture/`、`./internal/api/`） | 0 | 16 個 package `ok`，沒有 data race 報告（`race-touched.log`） |
| `make backend-race` | 0 | 71 個 package `ok`，沒有 data race 報告（`backend-race.log`） |
| `make ai-eval` | 0 | fake provider 的兩個 suite PASS；本輪沒有改 AI 的程式（`ai-eval.log`） |
| `go mod verify` | 0 | all modules verified；本輪沒有新依賴，`go.mod`／`go.sum` 沒有改 |
| `GOTOOLCHAIN=go1.27.3 go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./cmd/... ./internal/... ./schemas/... ./scripts/...` | 0 | No vulnerabilities found（`govulncheck.log`） |
| `python mutate.py <root> mutants-unit.log unit`（及兩次指定 mutant 的重跑） | 0 | 見「Mutation」 |
| `python mutate.py <root> mutants-db-build.log dbbuild` | 0 | 12 個 DB mutant 都能 build（這個模式一律 exit 0，結果在 log） |
| `reviewer/run.py`（overlay） | 0 | 見「Mutation」 |
| 先紅的 log：`export-stderr-red.log`、`config-red.log`、`cmd-red.log`、`observability-red.log`、`worker-red.log`、`worker-panicked-red.log`、`architecture-red.log` | 1 | 每個都在修正之前失敗（log 結尾的 `exit=1`） |
| `make integration-test`、`make db-test`、`make db-go-test` | — | NOT RUN（沒有 PostgreSQL） |

### NOT RUN

- DB-backed 測試（本 worktree 沒有 PostgreSQL）：只做了 compile 與 `go vet`（`make backend-test` 的 vet 涵蓋 test 檔）。本輪沒有新的 DB 測試；程式路徑被本輪改到的既有 DB 測試要重跑：
  - `internal/worker` 的 4 個 T-035 DB 測試：`failureOutcome`、`panicked`。
  - `cmd/api`、`cmd/worker` 的 DB 測試：telemetry 設定。
  - 每個寫 log 的 DB 測試：handler 與 filter 的改動。
- `make integration-test`、`make db-test`、`make db-go-test`。
- 12 個 DB mutant 的測試（`mutants-db-build.log`：都能 build）。
- `make frontend-test`：沒有改 contract 或 TS。
- `make e2e-test`、live AI、送到真的 OTLP collector、OpenAPI 官方 validator。

### 殘餘風險（本輪新增或改變）

- **單引號的規則是啟發式的**：在字的開頭開啟、字的結尾關閉才算，所以 `'90s` 這種以 apostrophe 開頭的字會把其後全部隱藏（過度隱藏）；反過來，字中的引號不算邊界。
- **Backtick 的代價**：pgx 把連線的 user 與 database 放在 backtick 中，log 中看不到它們（不是 secret）。
- **Header 的檢查**沿用 OTel v1.47.0 envconfig 的規則；升級 OTel 時，若 SDK 的解析變了，兩者可能不一致。不過 M1 的修正 (1) 保證 SDK 的抱怨仍只經過過濾後的 log。
- **其他 `OTEL_EXPORTER_OTLP_*` 變數**（TIMEOUT、COMPRESSION、CERTIFICATE…）沒有在 config 檢查；它們解析不了時的訊息現在也只經過濾後的 log（原始值所在的 key 是 redacted）。
- **Group 名稱**：由資料組成的小寫 group 名稱仍會出現在 log 中（符合 key 的形狀）；production 程式沒有使用 `WithGroup`。

### Coordinator 的執行（第 1 輪，d26767d）

`t035-r1` 從 7cbde1e（推送後的 head）開出，所以六個 commit 以 fast-forward 接上，hash 不變（4ceb306 … d26767d）。之後在 d26767d 的 tree 上執行以下指令，全部 exit 0（`coordinator-environment.txt`：沒有其他變更、go1.27.3、Node 22.22.0、PostgreSQL 16.15）：

| 指令 | Exit | 說明 |
|---|---:|---|
| `make spec-check` | 0 | |
| `make backend-test` | 0 | |
| `make architecture-check` | 0 | |
| `make ai-eval` | 0 | |
| `make frontend-test` | 0 | Node 22.22.0；Node 24 由 CI 執行 |
| `make simulation-reference-test` | 0 | |
| `make backend-numeric` | 0 | |
| `make backend-race` | 0 | |
| `make integration-test`（新的 `dt_test_t035r1`） | 0 | 34 個 package ok，沒有 FAIL |
| `govulncheck@v1.9.0 ./cmd/... ./internal/... ./schemas/... ./scripts/...` | 0 | module cache 中的 go1.27.3，`GOTOOLCHAIN=local`；No vulnerabilities found |
| `make db-test`（另一個新的 `dt_test_t035r1m`，供下面兩項） | 0 | |
| 新與改過的 DB 測試（`GEMINUS_REQUIRE_DB_TESTS=1`，`-v`） | 0 | 9 個都 PASS：`TestAnUnwritableAuditEventCommitsNothing`、`TestFailedAttemptsAreAudited`、`TestTheReaperAudits`、`TestAnAuditFailureCommitsNothing`、`TestTheHandlersLinesCarryTheJob`、`TestReapJobsReturnsWhatItTook`、`TestRunClosedLoop`、`TestImportMappingClosedLoop`、`TestRunServesAndStops` |
| 本輪的 `mutate.py <git archive 副本> <log> db` | 0 | 12 個 DB mutant 全部被測試擋下 |

Log 與 exit code 在 `checks/evidence/2026-10-26-T-035-r1/coordinator-*`。

仍為 NOT RUN：`make e2e-test`、live AI、送到真的 OTLP collector、OpenAPI 官方 validator。D-091–D-095 與 ADR-028 仍是 PROPOSED，T-035 在 tasks.json 仍為 `not_started`。

## Review 第 2 輪（b5f3a7e）：ACCEPT

獨立 reviewer（H-003）在 b5f3a7e 重審：
- M1 已修正：以原 header probe 重跑，stderr 沒有任何原始值，四行都是過濾後的 JSON；`validHeaders` 與 OTel v1.47.0 envconfig 的規則一致；`TestTheSDKWritesNothingRawToStderr` 擋下三個專門的 mutant。
- L1–L7、I6–I8 已修正或補上測試；第 1 輪 8 個會改變行為的存活 mutant 全部擋下。
- 本輪 33 個有效 mutant 中 31 個被擋下；`diagnostics-all-levels` 是 equivalent。
- 沒有 High、Medium。

本輪的發現：
- **N1（Low）**：r1 evidence 中的 probe 複本 `reviewer/obs_probe_test.go` 讓 `go list ./...` 多出一個 package（architecture-check 計數 82 → 83），`go test ./...` 也會編譯並執行它，但不影響 CI 的 `GO_PKGS`。依 T-048 的前例由 coordinator 修正：改名為 `obs_probe_test.go.txt`。之後 `go list ./...` 不再列出 `checks/` 下的 package，`make architecture-check` 回到「82 packages、21 rules、16 build configurations」，exit 0。
- **N2（Info）**：新的引號規則會過度隱藏一些文字：OTel 的 `missing '=`、pgx 放在 backtick 中的 user／database、`'90s`。前兩項已寫入 `TestQuotesKeepWords` 與殘餘風險，reviewer 認為是可接受的取捨。
- **N3（Info）**：mutant `runtime-by-package-suffix`（以 `HasSuffix(PkgPath, "runtime")` 取代精確比對）存活。程式是對的，但「名稱也叫 runtime 的其他 package 不被信任」沒有測試，要測需要一個路徑以 `/runtime` 結尾的 testdata package。列為殘餘風險。
- **N4（Info）**：即使沒有設定 endpoint，環境中殘留一個格式錯的 `OTEL_EXPORTER_OTLP_HEADERS` 也會讓 process 拒絕啟動。這是 fail closed，與文件一致。
- **N5（Info）**：效能沒有退步，64 KiB 惡意輸入的最壞情況新舊版大致相同。

Reviewer 同意 D-091–D-095 與 ADR-028 的建議。D-092 方面，`worker.New` 的檢查合理：handler 的 constructor 與 DB 的 `twin.actor_ref` 自 T-023 起就要求同樣的格式；ADR-024 的文字應由 owner 修改，並採用 `d092-config-proposal.diff`。

D-091–D-095 與 ADR-028 仍是 PROPOSED，它們的 gate 是 T-035 驗收前，所以 T-035 記為 in_progress（`planning/tasks.json` 的 completion_evidence），owner 批准後才標為 done。

## Owner 批准（H-024，2026-10-26）

Owner 對第六張決策頁（https://claude.ai/artifact/MsMai9v9qSb8XLdzW8DkPR）回覆「第五、六頁都照建議」：D-091–D-095 依建議 APPROVED，ADR-028 ACCEPTED。
- D-095 (b)：P0 加入 OpenTelemetry，預設不 export。
- D-093 (b)：log 記錄 `user_id`。
- D-092 (b)：修改 ADR-024 的 `WORKER_ID`。

依 D-092 (b)，coordinator 在 H-024 的 commit 中做了兩件事：
- 把 ADR-024 的 `WORKER_ID` 改為 actor reference。
- 套用本報告 evidence 中準備好的 `d092-config-proposal.diff`。先只套用測試：`TestLoadWorker` 在舊程式上失敗，「wwww…（129 字）」、`1worker`、`-worker`、`worker/1`、`worker@pod`、`w~1` 都被接受，exit 1（`checks/evidence/2026-10-26-H-024/d092-red.log`）。再套用 `config.LoadWorker` 的檢查後，`go test ./internal/config/ ./cmd/...` exit 0（`d092-green.log`）。

之後的完整驗證見 H-024 commit 的 evidence。T-035 標為 done。
