# T-046 phase 2 驗證報告：電子產線的 contract 變更

**日期：2026-10-20｜狀態：待獨立 review；`planning/tasks.json` 中 T-046 的狀態未改（in_progress），由 reviewer 決定。｜需求：REQ-003、REQ-004、REQ-013、REQ-015、REQ-024、REQ-025｜Gate：D-047–D-059 都已 APPROVED（H-018）；本任務提出的 D-063 是 **PROPOSED**，`approved_by`／`approved_at` 為 null，phase 2 依建議實作，並已列為 T-046、T-047–T-051 的 gate。｜驗收測試：TEST-001、TEST-002、TEST-034（contract 部分）｜`make frontend-test` 在 agent 的環境無法執行（Node 22），已由 coordinator 在 e0944c4 執行並通過（見文末「Coordinator 的執行」）；沒有改 migration 或 SQL，coordinator 的 `make integration-test` 也通過。Review 第 1 輪的修正見文末。**

範圍依 ADR-026 Consequences 的「批准後的 contract 變更（T-046 phase 2）」與 coordinator 的指示：

1. `model-ir.schema.json`：queue `capacity`；resource `breakdowns`、`calendar_id`、`shift_capacity`、`setups`；根層 `calendars`（shift `label`、`exceptions`）；source `arrival.schedule`（rate 型與 table 型）；process `batch`；distribution `lognormal`、`weibull`；`metrics[]` 的 `system_wip`、`flow_count`、`sink_count`、`period_throughput`。全部 optional，沒有它們的模型不變（D-047）。
2. `canonical-event.schema.json`（D-059）。
3. `simulation.schema.json`：Result 的 optional 新成員與每個 replication 的 resource state 積分（D-058）、trace 的新事件類型、run failure code `SIMULATION_DEADLOCK`／`SIMULATION_STALLED`。
4. Validator codes 與它們在 D-023 中的位置。
5. Patch allowlist（D-057）。
6. Fixtures、Go contract tests、`make ts-contracts`；只在 HTTP binding 受影響處改 OpenAPI，附範例。
7. **Fail closed**：kernel 不支援的 primitive 不能被當作不存在而模擬。
8. 不改 kernel 行為（以 differential 證明既有結果逐 byte 相同）；不做 T-047–T-052 的工作。

逐條輸出在 `checks/evidence/2026-10-20-T-046-p2/`。

## Commit

Hash 是 coordinator cherry-pick 到 `claude/lucid-meitner-vq1awo` 後的 commit（agent 分支上原為 56e04aa、d122343、46740ed、c1e2530、e31356f、8037908）；之後是 coordinator 的 d5eb6db。

| Commit | 內容 |
|---|---|
| 31aef83 | IR：`model-ir.schema.json`、`common.schema.json`（7 個新 ValidationCode）、Go 型別、validator 的 primitive support 階段（`PRIMITIVE_NOT_SUPPORTED`，D-063）、engine Compile 的 kernel 支援清單、IR 的 fixtures 與 cases、產生的 TS、en／zh-Hant 字串 |
| 551a5e3 | Event、Result、trace：`canonical-event.schema.json`、`simulation.schema.json`、Go 型別、fixtures、TS |
| 81640f4 | Patch allowlist（D-057） |
| 980b3f7 | OpenAPI：`publishVersion`、`createSimulation` 的 422 範例 |
| 2e631c7 | 文件：D-063 PROPOSED、TWIN_IR_SPEC、SIMULATION_SPEC、ADR-026、ELECTRONICS_LINE、TEST_PLAN、ARCHITECTURE、CB-38、tasks.json（T-046–T-052）、traceability、重產的 `checks/*.json` |
| e0944c4 | 本報告與 evidence |

## 稽核：scope 逐條

| Scope | 判定 | 證據／處理 |
|---|---|---|
| 1. IR schema 的新成員 | 已完成 | 所有成員都是 optional；名稱與形狀定案於 TWIN_IR_SPEC §2.1 與 ADR-026 的表。Arrival 的 `distribution` 與 `schedule` 恰一個（`oneOf` required）；schedule 以 `process` 為 discriminator 分三支（poisson、deterministic 帶 `segments`，table 帶 `releases`）；`shift_capacity` 以 `dependentRequired` 要求 `calendar_id`；setups 的 `default` 與 `transitions` 至少一個；上限依 ADR（segments、releases ≤ 1000，shifts、exceptions ≤ 100，quantity ≤ 10000，queue capacity 1–100000）。Go 型別：新欄位一律是 pointer、map、slice 或 `omitzero`；source 有 schedule 時 `Arrival.Distribution` 為 zero value，encode 為缺少，`Node.Distributions()` 不回它 |
| 1a. Schema 版本（D-047） | 已完成 | `schema_version` 維持 `"0.1"`（D-047 A，H-018）；「未知 schema_version 拒絕」不變；closed object 讓舊 reader 拒絕新成員。`TestReencodingKeepsTheModelHash`：所有 IR fixture 與 golden model（含 35 個新的）decode 再 encode 後 model_hash 不變；`check_hashes.py --write` 的 diff 只新增 `valid-line-flow-ir.json` 一個 hash，既有 hash 一個都沒變 |
| 2. canonical-event（D-059） | 已完成 | `end_timestamp`（date-time）、`shift`（≤ 64）、`product`（≤ 128）；`end_timestamp ≥ timestamp` 等是 data-quality 規則，不在 schema。Mapping 的 target field（`ai-mapping.schema.json` 與 `aicontext.TargetFields`）**未**擴充：那會改 AI context 與 prompt，屬 station log mapping（T-052 的 scope 已加上這一項） |
| 3. Result、trace、run failure | 已完成（contract）；產生者是 T-047–T-051 | `metrics.domain`；`replication_summaries[i].resource_states`（`ResourceState`：D-058 表的全部積分，每組以 `dependentRequired` 同時出現，至少一組功能）、`blocking`、`buffers`（`full_ticks`，D-049 列出、D-058 表沒有，D-063 (b)）、`batches`、`declared`。Trace：`blocked`、`unblocked`、`setup_start`，`resource_id` 只在 `setup_start`、`batch_id` 只在批次成員的 `service_start`／`service_end`；format 維持 `geminus.trace.v1`。Run failure：ValidationCode 之外沒有 run failure 的目錄（CB-38(a) 仍開放），所以以 `Run.error` 的 `$comment` 與 fixture `valid-run-failed-deadlock.json` 記錄這兩個 code 及其暫定對應（`MODEL_VALIDATION_FAILED`），Go 常數與 runner 的對應留給產生它們的 T-047（避免沒有產生者的程式碼）。`TestLineFlowResultMembersOnlyWhenPresent`：既有 Result 不會 encode 出任何新成員 |
| 4. Validator codes 與 D-023 | 已完成（codes、順序、fixtures）；規則本身在 T-047–T-051 | 文件的決定：ADR-026 的「實作」欄把 D-050–D-056 分給 T-047–T-050，phase 2 的 bullet 只列 codes 與排序，所以規則不在本任務。`BLOCKING_CYCLE`、`CALENDAR_INVALID`、`RELEASE_SCHEDULE_INVALID`、`BATCH_INVALID`、`SETUP_INVALID`、`STALL_RISK`（error，D-050 A）進入 ValidationCode 目錄與 Go 常數（`TestCodesMatchContract`）。順序（TWIN_IR_SPEC §4）：JSON parse → schema → **primitive support** → limits → IDs → references → graph rules（R1）→ distribution／domain semantics（各成員的形狀規則）→ **bundle feasibility**（R2–R4，ADR 寫「在 distribution／domain semantics 之後」，因為它們需要形狀已正確的 calendar）→ constraints → dataset bindings。每個 code 的 path 寫在 §4 的表；16 個 `layer: semantic` 的手推 fixture 固定 code 與 path（R1 兩例、calendar 形狀與 R2 四例、未知 calendar、release schedule、batch、setup、R3 兩例、R4、TTF 為 0、lognormal 範圍、宣告的 metric ID 撞內建 key）。各規則由哪個任務實作記在 tasks.json 的 T-047–T-051 scope |
| 4a. R3 `RESOURCE_REQUIREMENT_INVALID` | 同上 | 延伸既有 code，path 為 `/nodes/<i>/requires/<k>/units`（units≠1）或 `/nodes/<i>/requires`（兩個故障 resource）；兩個 fixture |
| 5. Patch allowlist（D-057） | 已完成 | `internal/model/patch/policy.go`：manual 與 AI edit 加入根層 `calendars`，`calendars` 加入 D-020 的 identity（改元素前先 test id，有元素時不可整段 replace）；scenario 加入 `nodes/*/capacity`（任何 op）與只接受 `replace` 的 13 個路徑（`replaceOnly`，`+` 為任一 member 名稱）。新增或刪除 breakdowns、setups、calendars、schedule、batch、transition、segment、release、shift label，以及改 period、segment start、release 時刻、calendar 名稱都是 `PATCH_PATH_FORBIDDEN`。「不能創造 dataset binding」改為也掃 resource（新的分布位置）。`lineflow_test.go`：18 個允許、20 個禁止、test-before-modify、edit 模式、dataset、以及「scenario 新增 buffer 造成 cycle」（gate 解除前為 `PRIMITIVE_NOT_SUPPORTED`，之後為 `BLOCKING_CYCLE`）。D-057 的 server risk **未**實作：candidate 在 gate 中已 invalid，review 不會算到 risk；在 T-047–T-050 解除 gate 時一併加入（scope 已寫明） |
| 6. Fixtures、Go tests、TS、OpenAPI | 已完成；TS 測試只做了獨立 typecheck | 46 個新 case（6 個正例、24 個 schema 反例釘住 path、16 個 semantic）；`invalid-p1-distribution.json` 改用仍屬 P1 的 `mixture`（lognormal 依 D-056 已是 P0 的 schema 成員）。Go：model、validate、patch、simulation、engine、domainpack、conformance、AI review 的測試。TS：`make ts-contracts` 重產（新 type：`ReleaseSchedule`、`Setups`、`Calendar`、`ResourceState`、`NodeBlocking`、`BufferState`、`BatchCounts`），生成器的 ignored keywords 加入 `dependentRequired`、`propertyNames`、`minProperties`（只限制值、不改形狀）；`contracts.test.ts` 加正例與 4 個 `@ts-expect-error`，`i18n.test.ts` 的 ValidationCode 數由 23 改為 30。OpenAPI：`publishVersion`、`createSimulation` 的 422 改為 inline response，帶 `PRIMITIVE_NOT_SUPPORTED` 範例（`check_contracts.py` 依 schema 驗證範例） |
| 7. Fail closed | 已完成；新 code 需 owner 批准 | 沒有既有 code 適用（`IR_SCHEMA_INVALID` 會說「結構或型別不正確」，但 schema 接受這些成員），所以新增 `PRIMITIVE_NOT_SUPPORTED` 並提出 **D-063（PROPOSED）**，實作建議 A：(1) validator 的 primitive support 階段（`internal/model/validate/primitives.go`）在 schema 之後、limits 之前，對 `validate.Checked` 不含的每個 primitive 的每個用到之處回 error（path 為該成員；message 固定，`TestPrimitiveGateMessagesAreFixed` 以 sentinel 檢查），之後的階段不執行；(2) engine Compile 另有 `kernelExecutes` 清單（`internal/simulation/engine/compile.go`），validator 之後再檢查一次，所以「規則先到、kernel 未到」時仍拒絕。兩個清單目前都是空的。效果：draft 可存（D-028），publish、scenario、AI proposal（candidate validation）、run（入列時 Compile，422）全部拒絕。紅燈證據：沒有這個階段時，16 個 semantic 反例全部被既有 validator 判為 valid（`validate-red.log`），也就是可以 publish 成 immutable version |
| 8. 不改 kernel、differential | 已完成 | Kernel、sampler、事件順序、metrics、trace 的程式都沒改；Compile 只多一個輸入檢查（沒有新成員時不產生任何 issue）。Differential：base 175e1ae 與 branch head 對 4 個 golden model × 3 組 config 與 GOLDEN-001／002 的完整 Result 與 trace JSON，26 行、415,871 bytes，sha256 相同、`cmp` 相同（`differential.txt`）。鎖定的 digest 檔沒有重產；`TestGoldenDigests` 與 kernel observation digest 的測試照舊通過 |
| 不做 T-047–T-052 | 遵守 | 沒有實作任何規則、kernel、DomainPack、synthetic data；這些任務的 scope 記錄了它們各自要解除的 gate、規則、Result 與 trace 成員、risk 與 AI 文字 |

## 實際執行結果

在 worktree 執行（Go 1.27.2、Node 22.22.0、Python 3.13.16，4 CPU；沒有 PostgreSQL）。`.venv` 依 `requirements-spec.txt` 在 worktree 建立。下表的「實作後」結果是在最終的程式（與 e31356f 相同）上跑的；中間的 commit 依主題切分（C1 只含 IR 的 case，`cases.json` 以中間版本提交），沒有逐一 checkout 重跑。

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check PYTHON=.venv/bin/python`（改動前） | 0 | `baseline-spec-check.log` |
| `make backend-test`（改動前，第一次） | 2 | `scripts/check-architecture` 的 `TestReviewProbes` 失敗：它讀 package 檔案時，我放在 `internal/simulation/des` 的暫時 differential 測試檔剛被刪除（`no such file or directory`）。是我的操作干擾，不是基線問題 |
| `make backend-test`（改動前，第二次） | 0 | `baseline-backend-test.log`；之後跑 differential 時不再與其他 Go 測試同時進行 |
| `.venv/bin/python scripts/check_contracts.py`（fixtures 已加、schema 未改） | 1 | 紅燈：`valid-line-flow-ir` 不被接受（`spec-contracts-red.log`） |
| `go test ./internal/model/validate/`（stub：`Uses` 回空、全部視為已檢查） | 1 | 紅燈：16 個 semantic 反例全部被判為 valid、gate 測試失敗（`validate-red.log`） |
| `go test ./internal/model/patch/`（allowlist 未改） | 1 | 紅燈：18 個允許的路徑被拒、calendars 的 test-before-modify、edit 模式、resource 中的 dataset（`patch-red.log`） |
| `go vet ./internal/simulation/engine/`（kernel 清單未實作） | 1 | 紅燈：`kernelExecutes`、`unsupported` 未定義（`engine-red.log`） |
| `go test ./internal/simulation/...`（Go Result／trace 型別未改） | 1 | 紅燈：`TestSchemaParity`、`TestFixturesRoundTrip`（`simulation-types-red.log`） |
| 同上各項（實作後） | 0 | `validate-green.log`、`patch-green.log`、`engine-green.log`（`./internal/simulation/...` 全部，含 Result／trace 型別） |
| `make spec-check PYTHON=.venv/bin/python` | 0 | `spec-check.log`：contracts（52 正例、98 反例、93 個釘 path、7 個 lint、16 個 Go semantic）、planning、docs、decisions、TS contracts fresh、independent hashes、DB tests fresh；重產的 `checks/*.json` 與 C5 一起 commit |
| `make backend-test`（e31356f 上） | 0 | `backend-test.log`：gofmt、go vet、57 個 package ok；DB-backed 測試沒有 DB URL 而 skip（本任務不需要） |
| `go test -count=1 -v`（本任務的新測試與調整過的測試，`t046p2-go-tests.log`） | 0 | 22 個 top-level test、93 個 PASS（含 subtest），0 FAIL；`TestAgreesWithSharedFixtures`：58 個 IR fixture，17 個被 gate 拒絕；`TestGoldenDigests` 照舊通過 |
| `make backend-race` | 0 | `backend-race.log`：57 個 package ok（`-race`） |
| `make architecture-check` | 0 | `architecture-check.log`：69 packages、15 rules、16 build configurations；generic kernel 仍不依賴任何 domain pack |
| `make ts-contracts PYTHON=.venv/bin/python` | 0 | 重產後與 commit 的檔案相同（`ts-contracts.log`） |
| `make ai-eval` | 0 | `ai-eval.log`；`checks/ai-evals/*.json` 沒有變化 |
| differential（base 與 head） | 0 | 相同（`differential.txt`） |
| 獨立 typecheck：`/opt/node22/bin/tsc -p tsconfig.json`（TypeScript 6.0.2） | 0 | 產生的 contracts、兩個 message catalog（經 `lib/i18n/translate` 的型別）、新的 TS 片段與 6 個 `@ts-expect-error`；對照組（把 mixture 換成合法的 lognormal）exit 2、報告 unused directive，證明檢查有效（`tscheck.log`）。專案用 6.0.3，這裡是 6.0.2 |
| `npm --prefix apps/web ci --ignore-scripts` | 1 | `EBADENGINE`：需要 node ≥ 24.21.0 < 25，這裡是 22.22.0（`npm-ci.log`）。沒有繞過 engine-strict |
| `make frontend-test` | 2 | 沒有 dependencies，無法執行（`frontend-test.log`，NOT RUN） |
| `make simulation-reference-test` | 2 | Makefile 的 placeholder：`NOT IMPLEMENTED`（`simulation-reference-test.log`）；SimPy reference 屬 T-047–T-050 |

## 測試涵蓋

| 測試 | 驗證 | 驗收 |
|---|---|---|
| `TestAgreesWithSharedFixtures`（validate，延伸） | 每個 IR case：列出的 primitives 等於 `validate.Uses` 找到的；有未檢查的 primitive 時 issues 恰為 gate 的 issues；全部檢查後，正例 valid、semantic 反例在期望 path 回期望 code。17 個 IR fixture 目前被 gate 拒絕 | TEST-001、TEST-002 |
| `TestPrimitiveCatalog`、`TestPrimitiveGate`、`TestPrimitiveGateStopsTheStages`、`TestPrimitiveGateMessagesAreFixed`、`TestNoPrimitiveNoUse` | 12 個 primitive 各有實作任務且目前都未檢查；完整 fixture 的 19 個 issue（手寫的 path 與 message）；每個 primitive 單獨用時只回一個 issue，且同一模型中的未知 flow 目標不報（之後的階段不跑）；message 不引用模型內容、依 D-023 排序；舊 fixture 與 golden model 沒有 use | D-063 |
| `TestCompileRefusesLineFlowPrimitives`、`TestKernelRefusesWhatItDoesNotExecute`（engine） | Compile 回 19 個 `/model/...` 的 `PRIMITIVE_NOT_SUPPORTED`；kernel 清單獨立於 validator、golden model 不受影響 | D-063、D-047 |
| `TestReencodingKeepsTheModelHash`、`TestScheduleSourceHasNoArrivalDistribution`、`TestSchemaParity`、`TestDiscriminatorsMatchSchema`、`TestPositiveFixturesRoundTrip`（model） | model_hash 在 re-encode 後不變；schedule source 沒有 arrival distribution；Go 型別與 schema 成員一致（含 release process discriminator） | D-047 |
| `TestSchemaParity`、`TestTraceEventTypesMatchSchema`、`TestLineFlowResultMembersOnlyWhenPresent`、`TestFixturesRoundTrip`（simulation） | Result／trace 型別與 schema 一致；舊 Result 不會出現新成員 | D-047、D-058 |
| `TestCanonicalEventContract`（domainpack） | parity 與所有 canonical-event 正例的 round trip | D-059 |
| `TestInvalidVariantsBreakTheirRule`（conformance，加一個 variant） | 宣告 `system_wip` 的 variant 在任何 domain 名稱下都回 `PRIMITIVE_NOT_SUPPORTED`（新階段也受 domain invariance 檢查） | TEST-002、REQ-003 |
| `lineflow_test.go`（patch） | D-057 的允許與禁止、replace-only、D-020、edit 模式、resource 中的 dataset、新增 buffer 的 scenario | D-057、D-020、D-021 |
| `TestRefusesUnsafeChanges`（AI review，調整） | P1 分布改用 `mixture`（仍是 `IR_SCHEMA_INVALID`）；新增 lognormal 與 calendars 的 edit 回 `PRIMITIVE_NOT_SUPPORTED`（invalid） | D-016、D-063 |
| `check_contracts.py`、`check_hashes.py` | schema 正反例與 path；semantic case 必須 schema-valid 且欄位完整；OpenAPI 範例依 schema；獨立 Python JCS hash | TEST-001 |

## NOT RUN

- `make frontend-test`（lint、typecheck、unit）：`npm ci` 回 `EBADENGINE`（Node 22.22.0）。`i18n.test.ts`（30 個 ValidationCode、新字串的 key 與標點）與 `contracts.test.ts`（新正例與 `@ts-expect-error`）沒有在 vitest 中執行；只做了上面的獨立 `tsc` typecheck。
- `make simulation-reference-test`：placeholder（exit 2），SimPy reference 屬 T-047–T-050。
- `make integration-test`／DB 測試：沒有改 migration 或 SQL，不需要；未執行。
- `make e2e-test`：UI 尚無建模畫面使用這些成員。
- Live AI 與 `make ai-eval-live`：未核准（H-017），本任務沒有 AI 呼叫。

## 需要 owner 決定

- **D-063（PROPOSED）**：(a) `PRIMITIVE_NOT_SUPPORTED` 與 validator、engine 兩處的 fail closed（已依建議 A 實作）；(b) phase 2 中由 agent 選定的 contract 細節（calendars 數量與形狀、`shift_capacity` 的 object 形式、arrival 的二擇一、setups 的至少一個、cv／shape 只在 schema 要求 > 0、metric 的成員、`buffers.full_ticks`、`declared` 的值與 1000 期上限、trace 的 additive 擴充、deadlock／stalled 的暫定 HTTP 對應、replace-only 的 scenario 路徑、bundle feasibility 階段、各 code 的 path）；(c) D-056 與 D-029 對超過 365 天的 mean／scale 回不同 code，建議 `LIMIT_EXCEEDED`。T-046、T-047–T-051 都以 D-063 為 gate。
- CB-38(a)（run failure 的 code 目錄）仍開放；本任務只沿用 T-023 的暫定對應。

## 殘餘風險

- `make frontend-test` 沒跑：`i18n.test.ts` 的計數與 `contracts.test.ts` 的 runtime 比對只由獨立 typecheck 與 review 涵蓋。
- 16 個 semantic fixture 的期望（code、path）是依 ADR 手推，規則尚未實作；T-047–T-051 若發現某個 path 的約定不合理，要經 review 改 fixture 與 TWIN_IR_SPEC，而不是照實作結果改。
- 解除 gate 的任務必須同時處理：D-057 的 server risk（目前 `ServerRisk` 不看新成員，gate 解除後若沒補上會低估 risk）、resource 中分布的 limits／dataset binding／D-033（目前只看 node 的分布）、AI context 的 scenario allowlist 文字、eval MOD-038。這些都寫進了各任務的 scope，但沒有自動測試會在漏做時失敗（除了 `TestPrimitiveCatalog` 與 gate 測試會逼解除者更新它們）。
- Mapping 的 target field 尚未包含 `end_timestamp`、`shift`、`product`，在 T-052 之前 station log 的這三欄無法由 mapping 寫入。
- `declared` 以 raw JSON 表示（number、陣列或 null），Go 端不做形狀檢查，依賴 runner 存入前的 schema 驗證；T-051 可改為具名型別。
- Engine Compile 多了一個輸入檢查。對沒有新成員的模型它不產生 issue，differential 證明結果不變，但這仍是 `internal/simulation/engine` 的程式變更（不是 kernel 行為）。

## Coordinator 的執行（e0944c4）

Coordinator 把六個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（fcf824d 之後，沒有衝突），在 e0944c4 上執行 `make spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`frontend-test` 與 `integration-test`（新的一次性資料庫 `dt_test_t046_p2`），全部 exit 0。`make frontend-test`：18 個 test file、422 個 test 通過，所以上方 NOT RUN 中的 `make frontend-test` 已由 coordinator 執行；`integration-test`：26 個 package ok，沒有 FAIL 或 SKIP。Log 與 exit code 在 `checks/evidence/2026-10-20-T-046-p2/coordinator-*`。`make simulation-reference-test` 仍為 NOT RUN（目標尚未實作）。

## Review 第 1 輪（CHANGES REQUESTED：0 High、2 Medium）

**基底：766984f**（T-025 review 第 1 輪之後）。第一次在 r1 上做的修正因 container 重啟遺失，本節是重做的結果；每個 finding 一個 commit，都先紅後綠。Evidence 在 `checks/evidence/2026-10-20-T-046-p2-r1/`。Mutation 以 reviewer 的 `t046p2-review-mutate.py`（未修改）在 worktree 的 scratch 副本上執行。

**基底本身是紅的**：766984f 上 `go test ./internal/ai/review/` 失敗（`base-review-sentences-red.log`）：T-025 review 第 1 輪加的 `TestEveryCodeHasASentence` 要求 ValidationCode 目錄中每個 code 都有固定句，而目錄已有 T-046 phase 2 的 7 個 code，句子表沒有。dc1ae02 在 `internal/ai/review/clean.go` 補上這 7 句（`PRIMITIVE_NOT_SUPPORTED` 另保留 gate 自己的 12 個固定 message，它們不引用模型內容）；`risk.go` 沒有改。

| Finding | 修正 | Commit | 紅 → 綠的證據 |
|---|---|---|---|
| M1：分布型別在每個位置都算 gate，但沒有逐位置的測試（M1、M2、M3 mutation 存活；M3 下 lognormal 的 delay 會變成 valid、可 publish） | `internal/model/validate/positions_test.go`：7 個 `$ref Distribution` 位置（arrival、service、delay、TTF、TTR、setups default、transition）× {lognormal, weibull}，手寫 path，validator 與 engine Compile 都必須只在那些 path 回 `PRIMITIVE_NOT_SUPPORTED`（容器本身也 gated 時連同容器的 path）；另一個測試從 schema 根沿 properties、items、additionalProperties、oneOf／anyOf／allOf 與本地 `$defs` 走訪，找到的位置必須恰為這 7 個 | 1a6f0c1 | `m1-mutations-M1-M3.log`：M1、M2、M3 都被 `TestEveryDistributionPositionIsGated` 抓到（exit 1） |
| M2：Compile 的 kernel 清單檢查沒有經過 Compile 的測試（M10 存活；M16＋M10 時有限 buffer 的模型會被編譯並當作無限模擬） | Compile 經由未匯出的 `validateIR`（預設 `validate.IR`）呼叫 validator；`TestCompileRefusesWhatTheKernelDoesNotRun` 換成放行一切的 validator，要求 12 個 primitive 各自在 golden model 中只回一個 `/model/...` 的 `PRIMITIVE_NOT_SUPPORTED`，再以真正的 validator 確認同一批模型先被 validator 擋下。測試不平行、以 defer 還原；`go test -race ./internal/simulation/...` 通過 | cf96c7a | `m2-red-before-injection.log`（注入點不存在時無法編譯）；`m2-mutation-M10.log`：M10 被抓到；`m2-reviewer-m10-script.log`：reviewer 的 M16＋M10 腳本，「M10 alone, repository tests」由 exit 0 變為 exit 1 |
| L1：「primitive support 在 limits 之前」沒有測試（M7 存活） | `TestPrimitiveGateRunsBeforeEveryLaterStage`：分別破壞 limits（metadata 過深、超過 365 天的分布）、IDs（重複 node）、refs（未知 flow 目標）並宣告 `system_wip`，只能回 gate 的 issue；不宣告時各自回該階段的 error（對照） | 6a7250f | `l1-mutation-M7.log`：M7 被抓到 |
| L2：沒有東西逼解除 gate 的人補 D-057 的 risk | 新檔 `internal/ai/review/lineflow_risk_test.go`（`risk.go` 未改）：`validate.Checked(p)` 為真而 `lineFlowRiskCases[p]` 沒有 case 時失敗，已登記的 case 必須對 `ServerRisk` 成立；目前沒有 case | 811982b | `l2-M16-review-test.log`：把 finite_buffer 標為 checked 時這個測試失敗；`l2-mutation-M16.log` |
| L3：「shifts」允許的 case 刪掉了 `shift_capacity` 還在用的 `evening` | 改為保留 day 與 evening 兩個 label | 1795090 | `l3-l5-shift-labels-green.log` |
| L4：UI 的「This version…」「此版本…」像是 ModelVersion | 改為「This build of Geminus…」「目前的系統版本…」 | 48ef19b | `l4-tscheck.log`（catalog 的型別檢查）；i18n 測試的 vitest 執行見 NOT RUN |
| L5：D-063 (b) 漏列的選擇；TWIN_IR_SPEC §6 與 allowlist 對 shift label 不一致 | D-063 (b) 補列：exceptions 至少 1 段、`batch_id` 從 b1 起與 rep ≤ 999、宣告式 metric 的 gate path `/metrics/<i>/type`、`ResourceState` 的 anyOf 與 if/then。Shift label 採較嚴格的解讀並實作：scenario 的 `replace calendars/*/shifts` 可移動、增減 shift，但 calendar 的 shift label 集合不可變（新增、刪除、改名為 `PATCH_PATH_FORBIDDEN`）；draft edit 不受限。TWIN_IR_SPEC §6 改為一致的文字；問題列入 D-063 (b) 請 owner 決定。D-057 的批准文字未動 | 1795090、bb3cc49 | `l3-l5-shift-labels-red.log`（5 個禁止的 case 原本都被套用）→ `l3-l5-shift-labels-green.log` |
| I1：報告開頭仍寫 frontend-test NOT RUN；commit 表是 cherry-pick 前的 hash | 開頭改指向 coordinator 的執行；commit 表改為分支上的 31aef83、551a5e3、81640f4、980b3f7、2e631c7、e0944c4 | 本報告的 commit | — |
| I2：MOD-038 的 unsafe 回答用了 `sd`，被 `IR_SCHEMA_INVALID` 擋下，不是 gate | `internal/ai/corpus/review_test.go`：保留它並釘成 `IR_SCHEMA_INVALID`，另加合法的 lognormal（mean、cv），要求 `PRIMITIVE_NOT_SUPPORTED`；T-048 的 scope 記錄解除 lognormal 時要重新判定這個 case | 5fb4719、bb3cc49 | `i2-ai-eval.log`：`make ai-eval` exit 0，unsafe 回答 25 個都 invalid（原 24），zero-tolerance gate 都是 0；`checks/ai-evals/fake-review.json` 隨之更新 |
| I3：AI edit 可新增 calendars，但 ID pool 不涵蓋 calendar ID | 記入 T-049 的 scope（解除 gate 前決定 AI 新增 calendar 的 ID 規則） | bb3cc49 | — |
| I4：ADR-026 phase 2 bullet 的 R3 規則延後 | D-063 新增 (d)：validator 規則（含 R3）延到 T-047–T-050，與 (a) 一併請 owner 確認 | bb3cc49 | — |
| I5：`valid-run-failed-deadlock.json` 的 engine version 與 message 格式在產生者存在之前就定了 | 記錄於此與殘餘風險：fixture 的 `engine_version` 為假設的 `des-go-0.2.0`，message 為示意格式；只有 code（`SIMULATION_DEADLOCK`）、`details[0]` 的位置與暫定的 `MODEL_VALIDATION_FAILED` 是 contract。T-047 實作 B1 時要以實際輸出更新這個 fixture | — | — |

### 第 1 輪的執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `go test ./internal/ai/review/`（766984f） | 1 | 基底的紅燈（`base-review-sentences-red.log`） |
| `go test ./internal/ai/...`（dc1ae02 之後） | 0 | `base-review-sentences-green.log` |
| `make spec-check PYTHON=.venv/bin/python` | 0 | `spec-check.log`（在填好本節之後執行）；重產的 `checks/*.json` 沒有差異（本輪唯一變動的 checks json 是 5fb4719 的 `checks/ai-evals/fake-review.json`） |
| `make backend-test` | 0 | `backend-test.log`：57 個 package ok，沒有 FAIL 或 SKIP |
| `make backend-race` | 0 | `backend-race.log`：57 個 package ok，沒有 FAIL |
| `make architecture-check` | 0 | `architecture-check.log`：69 packages、15 rules、16 build configurations |
| `make ts-contracts PYTHON=.venv/bin/python` | 0 | schema 本輪沒有改；重產後沒有差異（`ts-contracts.log`） |
| `make ai-eval` | 0 | `i2-ai-eval.log` |
| Mutation M1、M2、M3、M7、M10（最終的程式，reviewer 的腳本） | 每個 mutation 下 test 都 exit 1（5/5 caught）；腳本本身 exit 0 | `mutations-final.log`：M1、M2、M3 被 `TestEveryDistributionPositionIsGated` 抓到，M7 被 `TestPrimitiveGateRunsBeforeEveryLaterStage`，M10 被 `TestCompileRefusesWhatTheKernelDoesNotRun` |
| `npm --prefix apps/web ci --ignore-scripts` | 1 | `EBADENGINE`（Node 22.22.0，需要 ≥ 24.21.0）（`frontend-not-run.log`）；沒有繞過 |

**NOT RUN（第 1 輪）**：`make frontend-test`（同上，請 coordinator 執行：L4 的字串與 i18n 測試）；`make integration-test`（本輪沒有改 migration 或 SQL）；`make simulation-reference-test`（目標尚未實作）。

**殘餘風險（第 1 輪新增）**：shift label 的解讀是較嚴格的一邊，若 owner 認為 scenario 可以改 label，要放寬 `afterApply`（T-049 的 scope 已記錄）；`validateIR` 是 package 變數，只有 engine 自己的測試會換，production 程式沒有寫它的路徑；`lineFlowRiskCases` 目前是空的，它只保證「解除 gate 時要登記 case」，case 本身的內容仍要 review；`valid-run-failed-deadlock.json` 見 I5。

### Coordinator 的執行（Review 第 1 輪之後，dc1ae02）

Coordinator 把第 1 輪的 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（fbb0f8f 之後）。worktree 上的 beddc8d 與 coordinator 的 6b5b914 為同樣 7 個 code 補了固定句；採用 beddc8d 的版本（branch 上為 dc1ae02），因為它保留 gate 對每個 primitive 的固定 message，6b5b914 只給一句通用句。之後 `spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`frontend-test` 與 `integration-test`（新的 `dt_test_t046_r1`：26 個 package ok，沒有 FAIL 或 SKIP）都是 exit 0。`backend-race` 第一次 exit 2：9 個 package 因 session 的磁碟額度用完而 build 失敗（`no space left on device`），沒有任何 test 失敗；清掉 scratchpad 中舊 review 的副本後重跑 exit 0（57 個 package ok）。因此本輪 NOT RUN 中的 `make frontend-test`（含 L4 的字串與 i18n 測試）已由 coordinator 執行並通過。Log 與 exit code 在 `checks/evidence/2026-10-20-T-046-p2-r1/coordinator-*`。

## Review 第 2 輪（H-003，d09f7aa）：ACCEPT

同一位獨立 reviewer 在 d09f7aa 上複審（以 `git archive` 匯出的副本執行，沒有改動 repo）：第 1 輪的 M1、M2、L1–L5、I2 都已修正，I3–I5 已記錄；M1、M2、M3、M7、M7b、M10、M16（含 M16＋M10）、N1、N3、N4 都被測試抓到；d09f7aa 對 fcf824d 的 differential 逐 byte 相同。沒有新的 High 或 Medium，結論 **ACCEPT**。這只是 contract 變更的審查結論；D-063 仍是 PROPOSED，T-046 以它為 gate。

新的 finding 由 coordinator 處理（本節的 commit）：

- **L1**（scenario 的 shift label 限制擋得比理由多）：沒被 `shift_capacity` 參照的 label 也不能用 scenario 砍掉，但沒被參照的 label 不影響 kernel，被參照的 label 刪除或改名本來就由 candidate validation 回 `CALENDAR_INVALID`。程式維持暫行的較嚴格解讀（fail-safe；calendars 目前被 gate 擋住，production 走不到），D-063 (b) 改寫為兩個選項並建議 B（scenario 可改 label），選 B 時 T-049 放寬 `afterApply`。
- **L2**（第 1 輪表的 hash 不在 branch 上）：改為 branch 上的 hash：13cd033→1a6f0c1、3e5a3a3→cf96c7a、ac7b83f→6a7250f、da0f336→811982b、1e8429f→1795090、43fe8aa→48ef19b、52f07dd→5fb4719、6a207ea→bb3cc49、0576fe4→2f74a02、beddc8d→dc1ae02（evidence 檔案內的 hash 不改）。
- **I1**（schema 走訪測試的盲點，N2 存活）：`TestDistributionPositionsCoverTheSchema` 另檢查 schema 全文中 `"#/$defs/Distribution"` 的出現次數等於走訪到的位置數。紅：在 batch 的 `patternProperties` 下加一個 Distribution，exit 1（「the schema has 8 references to Distribution, the walk reaches 7」，`checks/evidence/2026-10-20-T-046-p2-r2/i1-red.log`）；還原後 exit 0（`i1-green.log`）。

記錄、不修：
- I2：engine 的 `validateIR` 只有測試會改；production 的 `init()` 換掉它會被 `TestCompileRejects` 抓到（N4）。在 architecture-check 加「非 `_test.go` 不得指派 `validateIR`」的規則列為後續，在 T-047 解除第一個 gate 前處理。
- I3：與 T-025 的交互（dc1ae02 的固定句、`knownPath` 保留 gate 的 path）沒有問題。
- I4：kernel 行為沒有改變（differential 相同）。

## Owner 批准（H-019）

2026-10-20 owner 對決策頁回覆「Q2 選 B，其他照建議」：D-063 依建議 APPROVED。(a) 採 A，兩道關卡 fail closed；(b) 的 shift label 採 B：scenario 可新增、刪除、改名 label，被 `shift_capacity` 參照的 label 刪除或改名由 candidate validation 回 `CALENDAR_INVALID`。T-049 解除 calendars 的 gate 前放寬 `internal/model/patch/policy.go` 的 `afterApply`（已寫入 T-049 的 scope；在那之前 calendars 被 gate 擋住，沒有行為差異）。(c) 超過 365 天的分布參數回 `LIMIT_EXCEEDED`（D-056 的段落已加註）；(d) validator 規則延到 T-047–T-050。T-046 因此標為 done。
