# T-049 驗證報告：DES 班表與隨時間變化的投入（H-016）

**日期：2026-10-25｜狀態：實作完成，待獨立 review；`planning/tasks.json` 的狀態未改（仍為 not_started），由 coordinator／reviewer 決定。｜需求：REQ-011、REQ-013、REQ-024｜Gate：D-047、D-048、D-050、D-051、D-052、D-053、D-057 已 APPROVED（H-018），D-063 已 APPROVED（H-019），D-064 已 APPROVED（H-021），D-074 已 APPROVED（H-022）；本任務提出的 D-081–D-085 是 **PROPOSED**，`approved_by`／`approved_at` 為 null，T-049 依建議實作並以它們為 gate（tasks.json 的 `decision_gates` 加入 D-051、D-064、D-074、D-081–D-085）。｜驗收測試：TEST-015、TEST-019、TEST-034（第三部分）｜DB 測試與 `make integration-test` 為 NOT RUN（見「NOT RUN」）。**

範圍依 tasks.json 與 coordinator 的指示（每一條都要做）：

1. Resource 班表（根層 `calendars`、`calendar_id`、`shift_capacity`）與 source 的投入排程（poisson／deterministic 的分段 rate、table），整數 tick、沒有 wall clock 與時區；交班 finish-first（overtime）；utilization 的分母依 D-052 (i) 並在 Result 標示；Result `resource_states` 的 scheduled 與 available 組。
2. Validator：`CALENDAR_INVALID`（形狀；D-050 R2 的 LCM ≤ 365 天與 bundle feasibility 階段的可用時間）、`UNKNOWN_REFERENCE`（calendar_id）、`DUPLICATE_ID`（calendar ID）、`RELEASE_SCHEDULE_INVALID`；D-050 R4 `STALL_RISK`；kernel 的 B2 next_fit 與 X_end。
3. 故障時鐘只在上班時前進（D-051）；上班不經釋放就增加 capacity，必須喚醒等待的 request（等待中的故障依 D-074 (a) B）；B1 仍要正確區分 release-bound 與 capacity-bound；先寫測試，bound 的 `>`→`>=` 與「上班不喚醒」兩個 mutant 必須被擋下。
4. 沒有班表與排程的模型與 des-go-0.3.0 逐 byte 相同（凍結副本與 differential，同 T-048 對 0.2.0 的做法）；中性班表與中性排程也要相同；升 engine_version（des-go-0.4.0）。
5. 只解除 `calendars` 與 `release_schedule`：`validate.checked`、`engine.kernelExecutes`、`TestPrimitiveCatalog`、`TestPrimitiveGate`、`TestKernelRefusesWhatItDoesNotExecute`、cases.json 的 primitives；`internal/ai/review` 的 D-057 server risk；AI context（`aicontext.modeRules`、prompts/scenario）的 scenario allowlist 文字。T-046 I3：決定 AI 新增 calendar 的 ID 規則（pool 或禁止），提出 decision。D-063 (b) B：解除 calendars 前先放寬 `internal/model/patch` 的 `afterApply`（先寫測試；更新 TWIN_IR_SPEC §6）。
6. SimPy reference（TEST-034）延伸到班表與投入排程：`scripts/simpy_reference.py` 與 fixtures，明確寫出接受哪些模型、其餘拒絕；每個邊界都要有手推 golden（上班／下班恰在 W 與 H、故障時鐘跨越下班暫停、投入 rate 在 tick 中改變、服務跨越交班）。

依 coordinator 的指示：只在自己的 worktree（`t049`，從 2f4c33f 開出）工作、commit、不 push；沒有 PostgreSQL，所有 DB 測試 NOT RUN；沒有 migration；新 decision 只用 D-081–D-086（實際用 D-081–D-085，D-086 未用）；沒有新的 dependency；沒有任何 live AI 呼叫，沒有讀取 `ANTHROPIC_API_KEY` 或 `GEMINUS_ANTHROPIC_*`；沒有碰 T-050（batches、setups）、T-054（HTTP 503）、imports 與 scenario allowlist 文字以外的 proposals；沒有改任何既有 decision 的狀態；沒有降低任何 assertion 或加 skip。逐條輸出在 `checks/evidence/2026-10-25-T-049/`。

## Commit

`t049` branch，依時間順序（從 2f4c33f 開出）：

| Commit | 內容 |
|---|---|
| 935edfd | 凍結 2f4c33f 的 des-go-0.3.0 kernel、replication 與 event queue（`frozen030_test.go`，以 go/ast 改名，只在測試中；工具原始碼 `freeze030.go.txt`）；`TestDifferentialAgainstFrozen030`（50,000 個隨機模型：一半有限 buffer、一半故障）；開始前的 `make backend-test`（`baseline-backend-test.log`） |
| 9a5da05 | D-063 (b) 選項 B：scenario 可以新增、刪除、改名 shift label（`internal/model/patch` 的 `afterApply` 不再檢查 label）；先寫的 `TestScenarioMayChangeShiftLabels` 為紅（`red-patch-labels.log`）；被參照的 label 被刪除時由 candidate validation 回 `CALENDAR_INVALID`（`TestScenarioDropsAReferencedLabel`）；TWIN_IR_SPEC §6 |
| e173ee1 | `internal/model/calendar`（S(t)、Θ、next change、next_fit、LCM 的整數 tick 算術，對逐 tick 的定義隨機比對）；validator：calendar 的形狀、`shift_capacity`、DUPLICATE_ID、UNKNOWN_REFERENCE、RELEASE_SCHEDULE_INVALID、R2（LCM 與 bundle 的可用時間）、R4 `STALL_RISK`；`validate.checked` 加 `calendars`、`release_schedule`；先寫的測試為紅（`red-validate.log`，60 個 `--- FAIL`） |
| 0d1f4c8 | Kernel des-go-0.4.0：CAPACITY_CHANGE、上班的喚醒、min(S, C − D) 的授予、上班時間的故障時鐘、B2 的 next_fit 與 X_end、投入排程（rate 與 table）；Result 的班表組；trace 不記 Capacity；手推 golden 20 個（`calendar-goldens.json`；G-C9、G-C10 之後在 e9ad4a9、f7fe9df 加入，共 22 個）、只看 observation 的班表檢查器、中性班表與中性排程的 differential、Result 的 golden、恆等式、決定性；`TestGoldenDigests030` 把 engine_version 換回；run failure fixture 的 engine_version；紅（`red-engine-vet.log`、`red-des.log`）與第一次執行（`first-engine-run-calendar-goldens.log`） |
| c7ecd33 | `scripts/engine_reference.py` 移植 D-053 的 rate profile，`engine-vectors.json` 的 `release_schedules`；`TestReleaseProfileVectors`、`TestReleaseRunVectors`、`TestReleaseScheduleCommonRandomNumbers`；`check_no_fma.sh` 抓到的 FMA（`backend-numeric-red.log`）修正 |
| 2da6a52 | SimPy reference 延伸到班表與投入排程（`scripts/simpy_reference.py`、`TestSimPyReferenceExport`、Makefile 的說明）；第一次執行的差異（`simpy-first-run.log`，腳本自己的 bug） |
| 00cc382 | des-go-0.4.0 自己的 regression lock：`engine-golden-digests-0.4.0.json`、`kernel-observation-digests-0.4.0.json` |
| 8850c4f | D-057 的 calendars 與投入排程 risk、expected effects 可引用的數字、AI 不能新增 calendar（D-083）、scenario 規則 scenario-0.5.0、MOD-039 重新判定、`fake-review.json`；紅（`red-ai.log`） |
| 8de3be9 | D-081–D-085（PROPOSED）；tasks.json 的 gate、planned outputs、spec refs、verification commands（status 未改）；SIMULATION_SPEC、TWIN_IR_SPEC、ADR-026、TEST_PLAN、ARCHITECTURE、CONTRACT_BLOCKERS；schema 的 `$comment`（D-084） |
| b6f856c | Mutation 第 1 次之後：Python port 的 `boundary`、`clamp` 向量（release_steps 27 → 38）；R4 的例外在第一個 period 內的 case；`FuzzKernel`、`FuzzRun` 以 calendar-goldens.json 為 seed；D-082 (a) 的更正與 D-081 (f) 的註明、`replication.go` 的註解；ARCHITECTURE 的 internal/simulation 列；mutation harness、`mutants-run1.log`、`fuzz-calendars.log` |
| e9ad4a9 | 手推 golden G-C9（下班時 held 在最前面的 request 不被喚醒、不排 dispatch）：mutation 第 2 次發現 `shift-end-wakes-too` 只被 0.4.0 的 observation 鎖擋下；手推期望第一次執行就通過（`first-engine-run-gc9.log`）之後才把 G-C9 加入兩個 0.4.0 的鎖；測試中的數量（20 個 golden、24 個鎖定 case）；SIMULATION_SPEC、TEST_PLAN、D-081 (b)；`mutants-run2-stopped.log`、`shift-end-wakes-too-without-locks.log` |
| f7fe9df | 手推 golden G-C10（被擋的 entity 跨越下班持有有 calendar 的 unit）：mutation 第 3 次（完整）中 `processing-ignores-blocked` 只被 0.4.0 的 Result 鎖擋下；手推期望第一次執行就通過（`first-engine-run-gc10.log`）之後才加入兩個鎖；數量 21 與 25；`mutants-run3.log`、`lock-only-mutants-without-locks.log`、`mutants-after-gc10.log`、`required-mutants-g-k2.log`；e9ad4a9 上的第一輪驗證 log（`*-pre-gc10.log`） |
| （最後一個） | 本報告；最後的 tree 上的驗證 log（`spec-check.log`、`backend-test.log`、`backend-race.log`、`architecture-check.log`、`ai-eval.log`、`simulation-reference-test.log`、`simpy-reference-report.json`、`backend-numeric.log`、`engine-tests-verbose.log`、`db-tests-skipped.log`、`fuzz-*.log`）、`release-vector-mutants.log`、`exit-codes.txt`、`environment.txt` |

只有 e9ad4a9 與最後的 tree 經過下面「實際執行結果」的完整驗證；其他中間的 commit 是依主題分開的檢視單位，沒有各自跑一次全部的測試。

## 稽核：scope 逐條

| Scope | 判定 | 證據／處理 |
|---|---|---|
| 1a. 班表的表示（`calendars`、`calendar_id`、`shift_capacity`、例外），整數 tick、沒有 wall clock 與時區 | 已完成 | `internal/model/calendar`：`calendar.FromModel` 以 `model.Ticks` 量化 period、shift 與例外，S(t)＝例外內 C（open）或 0（停工），否則 t mod period 所在 shift 的人數（label 的 `shift_capacity`；沒有 label 或沒有列出的 label 為 C），不在 shift 為 0；沒有 calendar 的 resource S＝C（`calendar.Always`）。只有整數與 tick，沒有日期、時區或 `time` package。`TestPatternAgainstTheDefinition`（3,000 個隨機 pattern 逐 tick 比對 At、Periodic、Θ、NextChange、NextAtLeast／NextBelow、FailureAt）、`TestNextFitAgainstTheDefinition`、`TestHandCases`、`TestNewRefuses` |
| 1b. 交班 finish-first（overtime） | 已完成 | `kernel.capacityChange`：S 減少不喚醒、不排 dispatch、不中斷，服務中與被擋的 entity 照常（G-C9：下班時 held 在最前面的 e2 不被喚醒，13 個事件）；`aggregate.go` 的 `overtime_ticks`＝∫max(U − S, 0)。Golden G-C1（e2 在 6 下班時繼續到 8，overtime 2 分）、G-C5（夜班換班時兩件在服務，overtime 2 分）、G-CF1（overtime 5 分） |
| 1c. Utilization 的分母（D-052 (i)）與在 Result 中的標示 | 已完成（標示方式為 D-084 A，PROPOSED） | `utilization_mean`／`resource_utilization` 不變：∫U／(C·T)，含下班與停機（`TestCalendarGoldenResults` 檢查每個 case 的 utilization 等於 `in_use_ticks`／(C·T)；G-C1 0.6、G-C1W 8/14）；scheduled utilization＝`scheduled_in_use_ticks`／`scheduled_ticks`（G-C1 第一個 period 4/6 在推導中）。標示在 contract：`simulation.schema.json` 兩個 utilization 成員的 `$comment` 與 SIMULATION_SPEC §5（Result 與 digest、TS 型別不變）；不另加 Result 成員（D-084 的 B、C 列為選項） |
| 1d. Result 的 scheduled 與 available 組 | 已完成 | `internal/results/metrics/aggregate.go`：有 calendar 的 resource 以 U、S、D、K 同時的值積分 `scheduled_ticks`、`scheduled_in_use_ticks`、`overtime_ticks`、`available_ticks`（∫min(S, C − D)）、`idle_ticks`（∫max(0, A − U)）、`scheduled_processing_ticks`（∫min(U − K, S)：上班時被擋不算 processing，G-C10：6 分而不是 9）；沒有有限 buffer 時列有故障或有 calendar 的 resource（G-C7：沒有被使用的 r_b、r_c 也列）。`TestCalendarGoldenResults`（21 個 case 的 `resource_states`、utilization、lead time、queue wait、blocking、buffers 逐值，Result 依 schema 驗證）、`TestCalendarStateIdentities`（C-S1–C-S4 的 30 replications：scheduled_in_use＋overtime＝in_use、available ≤ scheduled ≤ C·T、沒有停機時 available＝scheduled 且 idle＝available − scheduled_in_use、沒有有限 buffer 時 processing＝scheduled_in_use）、`TestNeutralCalendarKeepsTheResult` |
| 1e. 投入排程（poisson、deterministic、table） | 已完成 | `internal/simulation/engine/release.go`（`releaser`：D-053 的 numerical profile）、`replication.go`（`scheduleArrival`、`scheduleRow`、`startSource`；table 的一列一個 ARRIVAL）。Golden G-R1、G-R5、G-R7，以及 review 第 1 輪新增的 G-R8（投入表的 period 與 at 不是整數 tick，D-082 (b) A 的證據；G-R7 的 period 是整數 tick，分不出 A 與 B）、G-R9（rate 在上限）；`TestReleaseProfileVectors`、`TestReleaseRunVectors`（對 Python port 逐 bit：38 個 Λ⁻¹ 步驟（含 rate 0 的段與段的累積邊界、clamp 的情形）、4 個 arrival run 共 4,799 個 tick 與 a、stream 的最後狀態）、`TestReleaseScheduleCommonRandomNumbers`（G-R3）、`TestNeutralSchedulesChangeNothing`、`TestNeutralScheduleKeepsTheResult`（G-R4）；細節 D-082（PROPOSED） |
| 2a. `CALENDAR_INVALID` 的形狀規則 | 已完成 | `internal/model/validate/calendars.go` 的 `calendarShapes`（distribution/domain semantics 階段）：period ≤ 365 天、shift 在 period 內、排序、不重疊、至少 1 tick（量化後）、例外排序、不重疊、在 365 天內、`shift_capacity` 的 label 存在且 ≤ capacity（label 依排序檢查）。`TestCalendarRules`（20 個 case）、semantic fixtures `calendar-shifts-overlap`、`shift-capacity-unknown-label` |
| 2b. R2（LCM ≤ 365 天、bundle feasibility） | 已完成 | `calendarFeasibility`（bundle feasibility 階段）：所有 calendar 的 period 的 LCM ≤ 365 天，否則 `/calendars`；每個 bundle 在其 resource 的 period 的 LCM 內（不計例外）至少一個 tick 湊得齊，否則 `/nodes/<i>/requires`。`TestBundleFeasibility`、`calendar-bundle-never-fits`、`calendar-periods-too-long`；scenario 的 candidate（review 的 `TestCalendarRisk` 的「no unit on any shift」） |
| 2c. `UNKNOWN_REFERENCE`（calendar_id）、`DUPLICATE_ID`（calendar ID） | 已完成 | `ir.go` 的 `refs` 與 `ids`；`TestCalendarRules` 的最後兩個 case、`calendar-unknown`；engine 的 Compile 也拒絕未知的 calendar（防護） |
| 2d. `RELEASE_SCHEDULE_INVALID` | 已完成 | `releaseSchedules`：period ≤ 365 天；rate 型第一段 start 0、嚴格遞增、< period、rate ≤ 每 tick 1 件、至少一段 > 0；table 型 at 量化後嚴格遞增、< period。`TestReleaseScheduleRules`、`release-schedule-first-segment`；engine 的 `newReleaser` 也檢查（防護） |
| 2e. R4 `STALL_RISK` | 已完成 | `calendarFeasibility`：會故障的 resource 上班（S ≥ 1）時夥伴有 tick 不夠，只看週期部分，R2 不成立或 R3 已拒絕的 bundle 不再檢查；訊息指出改法。`TestBundleFeasibility`、`TestStallRiskMessage`、`semantic-stall-risk.json`；golden G-D1 的 validator 期望（`STALL_RISK` 於 `/nodes/1/requires`） |
| 2f. Kernel 的 B2 next_fit 與 X_end | 已完成 | `kernel.deadlineOf`：next_fit(max(since, X_end)＋TTRmax)，在進入狀態時算一次（`calendar.NextFit`）；`StalledError.Deadline`。`TestStallWithCalendar`（G-D1：960）、`TestStallDeadlines`（10 個 since／deadline 組合，含 X_end 與 TTRmax、沒有上界時沒有期限）、G-D3C、G-D4C、G-D5 不誤報；隨機檢查器重建 since 與期限（`TestRandomCalendarStallsAreInTheState`） |
| 3a. 故障時鐘只在上班時前進 | 已完成 | `kernel.scheduleFailure` 以 `Pattern.FailureAt`（Θ(t) ≥ Θ(t_up)＋TTF 的最小 t，二分搜尋到 horizon；目標恰在下班時為該 tick；0 tick 的 TTF 立即故障）。G-F6（12 而不是 8，下一個 25）、G-C6（停工例外：17）、G-D5（1920）、G-T5（6，下班的 unit 閒置，立即停機）；檢查器以自己的 Θ 檢查 constant 時鐘的時點 |
| 3b. 上班喚醒等待的 request；等待中的故障（D-074 (a) B） | 已完成 | `kernel.capacityChange`：S 增加喚醒 FIFO 最前面的 held candidate、排 dispatch；上班不給等待中的故障 unit（D-081 (c)）。G-C1（e3 在 10 上班時開始）、G-K2、G-C4、G-D3C、G-D4C、G-CF1（3 觸發的故障在 13 e1 釋放時才停機）；mutant `shift-start-no-wake`、`shift-start-wakes-without-dispatch`、`shift-start-satisfies-waiting-failure` 被擋下 |
| 3c. B1 的 release-bound／capacity-bound | 已完成 | `kernel.deadlock` 的 bound 仍為 C：只被上班人數擋住的 held candidate 是 capacity-bound。先寫的 golden G-K2（`TestShiftBoundIsNoDeadlock`：tick 4 時 e1 held、e2 被擋、沒有服務中的 entity，B1 不得回報）；mutant `deadlock-bound-ge`（`>`→`>=`）與 `deadlock-bound-uses-shift` 被擋下 |
| 4a. 與 des-go-0.3.0 逐 byte 相同 | 已完成 | `frozen030_test.go`（935edfd）；`TestDifferentialAgainstFrozen030`：50,000 個中 21,501 個 compile（4,947 個有有限 buffer、9,553 個有故障），10,428,435 個 observation 與 des-go-0.3.0 逐筆相同（其中 45,649 個 Blocked、121,862 個 ResourceDown；結束：正常 17,908、active 上限 1,602、visit 上限 1,991）；des-go-0.1.0、0.2.0、0.3.0 的鎖把 engine_version 換回後相等（`TestGoldenDigests`、`TestGoldenDigests020`、`TestGoldenDigests030`），三個 `kernel-observation-digests*.json` 照舊通過 |
| 4b. 中性班表與中性排程 | 已完成 | `TestNeutralCalendarsChangeNothing`：6,000 個中 2,106 個 compile（948 個有故障），1,009,495 個 observation（13,134 個 ResourceDown）逐筆相同；`TestNeutralSchedulesChangeNothing`：4,000 個中 1,433 個 compile，655,134 個 observation 逐筆相同；Result 層 `TestNeutralCalendarKeepsTheResult`、`TestNeutralScheduleKeepsTheResult` |
| 4c. engine_version | 已完成 | `des.Version = "des-go-0.4.0"`；`valid-run-failed-deadlock.json`、`valid-run-failed-stalled.json` 的 engine_version 隨之更新（`TestDeadlockError`、`TestStalledError` 要求等於 `des.Version`）；新的鎖 `engine-golden-digests-0.4.0.json`（25 個）、`kernel-observation-digests-0.4.0.json`（25 個），在手推期望與 SimPy 通過之後才取（G-C9、G-C10 的一筆各在它的手推期望通過之後才加入，其他不變） |
| 5a. 只解除兩個 primitive | 已完成 | `validate.checked` 與 `engine.kernelExecutes` 加 `Calendars`、`ReleaseSchedule`；`TestPrimitiveCatalog`、`TestPrimitiveGate`（valid-line-flow-ir.json 剩 7 個 PRIMITIVE_NOT_SUPPORTED：batch 與 setups）、`TestKernelRefusesWhatItDoesNotExecute`（7 個）、`TestCompileRefusesLineFlowPrimitives`；cases.json 的 primitives 在 T-046 phase 2 已寫好，7 個 calendar／schedule 的 semantic fixture 回手推的 code 與 path（`TestAgreesWithSharedFixtures`） |
| 5b. D-057 server risk | 已完成 | `internal/ai/review/risk.go`：calendar 新增、刪除或 period、shifts（含 label）、exceptions 不同；resource 的 calendar_id、shift_capacity 不同；release schedule 新增、刪除或任何成員不同，任一方向，至少 medium；calendar 的名稱為 low；`values()` 加入 calendar 與排程的數字（label 依排序）。`TestCalendarRisk`（13 個 ready 的 case、7 個 invalid 的 case）、`TestCalendarValuesInEffects`、`TestLiftedPrimitivesHaveRiskCases`（14 個新 case） |
| 5c. AI context | 已完成 | `aicontext.modeRules` 的 SCENARIO_PATCH_ALLOWLIST 與 `prompts/scenario/system.md`（scenario-0.4.0 → 0.5.0）：calendar_id（只能換成既有 calendar）、`shift_capacity/<label>`、shifts、exceptions、segment rate、release quantity，都只能 replace、至少 medium，CALENDAR_INVALID 與 STALL_RISK 的拒絕；不提 setups、batch、changeover。`TestScenarioMode`。Edit mode 的 prompt（modeler-0.2.0）沒有改（scope 只到 scenario allowlist 的文字），見 D-083 |
| 5d. T-046 I3：AI 新增的 calendar | 已完成（D-083，PROPOSED） | 建議並實作 A：禁止。`internal/ai/idpool` 的 `Check`：candidate 中 base 沒有的 calendar 一律 `NoCalendarIDs`，review 回 `ID_POOL_VIOLATION`（`/calendars/<i>/id`，固定訊息，`review.Clean` 的清單）。`TestNoNewCalendars`、review 的「a calendar added」、`TestCalendarRisk` 的兩個 edit；eval MOD-039 重新判定（見 5e） |
| 5e. MOD-039 | 已完成 | Calendar 自 des-go-0.4.0 起在 P0 執行，「24/7 與週末休息的 calendar，自行補時間」不再是不支援的 P1；intent 沒有給時間，所以 `category` 改為 `missing_data`、`expected_outcome` 仍為 `needs_input`。Unsafe 回答：自創 ID 與取自 pool 的 resource ID 的 calendar 都必須是 `ID_POOL_VIOLATION`。`make ai-eval` 重產 `checks/ai-evals/fake-review.json`（missing_data 4 → 5、unsupported_p1 1 → 0、unsafe 回答 invalid 25 → 27，gate 都是 0；`fake-provider.json` 沒有變） |
| 5f. D-063 (b) B：放寬 `afterApply` | 已完成 | 9a5da05：`afterApply` 不再要求 calendar 的 label 集合不變（簽名改為 base 的 samples refs）；`TestScenarioMayChangeShiftLabels`（先為紅：`red-patch-labels.log`）列出允許的（新增、刪除、改名 label）與仍禁止的（在 `shifts/0/label` 之下 add／remove、改 period、名稱、`shift_capacity` 的 key、新增 calendar、在 shifts／exceptions 之下 add／remove）；`TestScenarioDropsAReferencedLabel`（被參照的 label 被刪除 → candidate 的 `CALENDAR_INVALID`）；TWIN_IR_SPEC §6 |
| 6. SimPy reference | 已完成 | 見「SimPy reference」；接受與拒絕的條件寫在 `scripts/simpy_reference.py` 的 docstring 與 SIMULATION_SPEC §10 |
| 6b. 每個邊界的手推 golden | 已完成 | 上班／下班恰在 W 與 H：G-C1W（[6, 20]）、G-C1W10（[10, 16]）；故障時鐘跨越下班暫停：G-F6、G-C6、G-D5；rate 在 tick 中改變：G-R5（7.8125 ms）；服務跨越交班：G-C1、G-C5、G-CF1；另外 t=0 與下班恰為故障時刻（G-T5）、open 例外（G-C8）、下班時有 held 的 request（G-C9）、被擋的 entity 跨越下班（G-C10）、一個 calendar 兩個 resource（G-C7）、兩個 calendar 的 bundle（G-C4） |
| 不碰其他任務 | 符合 | 沒有 migration；沒有改 `internal/import`、`internal/proposals`、batch／setups 的規則或 kernel、T-054 的 503；`internal/model/patch` 只有 D-063 (b) B 的放寬 |

## 實際執行結果

在 worktree 中執行（`environment.txt`：Go 1.27.3、Python 3.13.16、SimPy 4.1.2、Node 22.22.0、4 CPU，與其他 agent session 共用；沒有 PostgreSQL）。Go 測試都以 `-count=1` 執行，不用 cache。Exit code 在 `exit-codes.txt`。下表上半部的指令都在最後的 tree（f7fe9df 的程式與測試資料；最後一個 commit 只加本報告、evidence 與重產的 `checks/*.json`）上執行；加入 G-C10 之前（e9ad4a9）的一次完整執行也都是 exit 0，log 保留為 `*-pre-gc10.log`（當時 mutation 第 3 次同時在跑）。

| 指令 | Exit | 說明 |
|---|---:|---|
| `make backend-test`（開始前，2f4c33f） | 0 | `baseline-backend-test.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `spec-check.log`（本報告寫好之後重跑）；`PASS_TS_CONTRACTS_FRESH`（15 檔、106 types：只改了 schema 的 `$comment`）、`PASS_INDEPENDENT_HASHES`（28）、`PASS_DB_TESTS_FRESH`（13）、`PASS_ENGINE_REFERENCE_VECTORS`（release_steps 38、release_arrivals 4,799）；`decisions-check.json`（proposed 6：D-081–D-085 與先前的一個；T-049 在 `next_after_review`，等 D-081–D-085）、`planning-check.json` 已在 b6f856c 重產，本次沒有再改變；重產的 `docs-check.json`（markdown 100 → 101 檔：本報告）與本報告一起 commit |
| `make backend-test GOFLAGS=-count=1` | 0 | `backend-test.log`：gofmt、go vet、64 個 package ok，沒有 `(cached)`；`GEMINUS_FULL_DIFFERENTIAL=1`（對 0.1.0、0.2.0、0.3.0 的 differential 都跑全部 50,000 個模型）；DB-backed 測試 skip（NOT RUN）。engine 159 s、des 13 s、check-architecture 155 s |
| `make backend-race GOFLAGS=-count=1` | 0 | `backend-race.log`：64 個 package ok，沒有 `(cached)`；engine 410 s、des 98 s、check-architecture 188 s（differential 在 race 下跑前 5,000 個） |
| `make architecture-check` | 0 | `architecture-check.log`：76 packages、17 rules、16 build configurations |
| `make ai-eval` | 0 | `ai-eval.log`：`TestFakeProviderEval`、`TestReviewEval` PASS；`checks/ai-evals/fake-review.json` 在 8850c4f 重產（missing_data 4 → 5、unsupported_p1 1 → 0、unsafe 回答 invalid 25 → 27），之後的執行沒有再改變；`fake-provider.json` 沒有變；live provider NOT RUN |
| `make simulation-reference-test PYTHON=…` | 0 | `simulation-reference-test.log`、`simpy-reference-report.json`：確定性 32 個 case（T-049 的 15 個）逐值相同，140 個隨機比對（C-S1–C-S4 的 57 個）都在容差內，G-C4、G-D3C、G-D4C、G-D5 被拒絕 |
| `make backend-numeric` | 0 | `backend-numeric.log`：六個 target 沒有 FMA 與平台 math call，GOAMD64=v3 的數值測試通過 |
| `go test -v`（重點測試） | 0 | `engine-tests-verbose.log`：differential、中性班表、中性排程、隨機檢查器的計數（見「Differential」） |
| `go test -v ./...`（列出 skip） | 0 | `db-tests-skipped.log`：216 個 `--- SKIP`（沒有 `GEMINUS_TEST_DATABASE_URL` 的 DB-backed 測試），沒有 FAIL |
| Fuzz | 0 | `fuzz-kernel-count.log`（500,000 execs）、`fuzz-run-count.log`（200,000 execs）、`fuzz-kernel.log`、`fuzz-run.log`（60 s）、`fuzz-calendars.log`（b6f856c 之前，120 s）（見「Fuzz」） |
| Mutation harness（第 3 次，e9ad4a9，完整） | 0 | `mutants-run3.log`：54 個中 51 個被擋下、3 個 equivalent（見「Mutation」） |
| Mutation harness（最後的程式，三個 mutant） | 0 | `mutants-after-gc10.log`：3/3 被擋下 |
| 單獨的 mutant 檢查 | — | `required-mutants-g-k2.log`（指定的兩個 mutant 對 G-K2：兩個 `go test` 都 exit 1）、`release-vector-mutants.log`（五個 release mutant 對向量：都 exit 1）、`lock-only-mutants-without-locks.log`、`shift-end-wakes-too-without-locks.log`（略過鎖：exit 0 即只有鎖擋下，促成 G-C10、G-C9） |

較早、失敗或中途的執行（保留為 evidence）：

| 指令 | Exit | 說明 |
|---|---:|---|
| `make simulation-reference-test PYTHON=…`（第一次） | 2 | `simpy-first-run.log`：C-S1 的五個指標超出容差（腳本沒有為 S(0) 擋住 unit，見「SimPy reference」）；腳本修正，手推 golden 與容差沒有改 |
| `make backend-numeric`（第一次） | 2 | `backend-numeric-red.log`：arm64、riscv64、loong64 把 `releaser.draw` 的 `1 − U` 與 inline 的 Uniform 的乘法（÷2^52）融合（arm64 的 FMSUBD 等）；改為 `1 - float64(rng.Uniform())` |
| Mutation harness（第 1 次） | 1 | `mutants-run1.log`：54 個中 47 個被擋下、1 個 equivalent、3 個 BUILD-FAILED、3 個 SURVIVED（處理見「Mutation」） |
| Mutation harness（第 2 次，b6f856c） | — | `mutants-run2-stopped.log`：前 18 個中 17 個被擋下、1 個 equivalent，`shift-end-wakes-too` 只被鎖擋下後中止 |
| G-C9、G-C10 的第一次執行 | 1 | `first-engine-run-gc9.log`、`first-engine-run-gc10.log`：該 case 的 engine 與 Result 子測試 PASS；只有尚未更新的數量與鎖失敗 |
| 實作前的紅 | 1 | `red-patch-labels.log`、`red-validate.log`、`red-engine-vet.log`、`red-des.log`、`red-ai.log`（見「先紅後綠」） |


## 測試涵蓋

| 層 | 測試 | 內容 |
|---|---|---|
| Calendar 算術 | `internal/model/calendar/calendar_test.go`：`TestPatternAgainstTheDefinition`、`TestNextFitAgainstTheDefinition`、`TestHandCases`、`TestNewRefuses` | 3,000 個隨機 pattern（shift、label 的人數、open／closed 例外）逐 tick 對定義：S(t)、週期部分、Θ、下一次改變、NextAtLeast／NextBelow（含與不含例外）、FailureAt（含 0 tick 的 TTF）；隨機 bundle 的 next_fit；G-F6、G-C6、G-C8 的手算值；LCM 的上限 |
| Validator | `internal/model/validate/calendars_test.go`：`TestCalendarRules`、`TestReleaseScheduleRules`、`TestBundleFeasibility`、`TestStallRiskMessage`；`primitives_test.go` 的 `TestPrimitiveCatalog`、`TestPrimitiveGate`；`TestAgreesWithSharedFixtures` | 正反例的 code、path 與 severity；R2 的 LCM 與 bundle、R4 的週期部分（例外不算：例外在第一個 period 內與之外各一個 case）、R2 或 R3 失敗時不報 R4；7 個 semantic fixture |
| Patch | `internal/model/patch/lineflow_test.go`：`TestScenarioMayChangeShiftLabels`、`TestScenarioDropsAReferencedLabel`、`TestEditsMayAddCalendars` | D-063 (b) B 的允許與仍禁止的 path |
| Kernel：golden | `internal/simulation/engine/calendar_test.go`：`TestCalendarGoldens`（21 個 case：arrival、service 開始、完成、阻塞、停機、等待中的故障、S 的每次改變與其 sequence、service 開始的 sequence、事件數）、`TestShiftEndBeforeFailure`（G-T5 的 tick 6）、`TestShiftBoundIsNoDeadlock`（G-K2）、`TestStallWithCalendar`（G-D1）、`TestStallDeadlines`、`TestCalendarObservationDigests` | 手推的期望（calendar-goldens.json）；0.4.0 的 observation 鎖 |
| Kernel：投入排程 | `release_test.go`：`TestReleaseProfileVectors`、`TestReleaseRunVectors`、`TestReleaseScheduleCommonRandomNumbers` | 對 Python port 逐 bit；G-R3 |
| Kernel：只看 observation 的檢查器 | `kernel_check_calendar_test.go`：`TestRandomCalendarModelsAgainstSpec`、`TestRandomCalendarStallsAreInTheState`、`FuzzCalendars`；`kernel_check_blocking_test.go` 的 bchecker 延伸 | 只由 IR 重建 S(t)（step function）與 Θ，不用 `internal/model/calendar`：min(S, C − D) 的授予與 dispatch fixpoint、沒有 tick 結束時還有可授予的 request（上班會喚醒）、每次 S 的改變恰為一個 Capacity observation（一個 calendar 一個事件、依 resource 順序、沒有多餘的）、constant 時鐘在上班時間上的時點、投入表的時刻與數量與編號、rate 排程嚴格遞增、deterministic 排程在精確 Λ⁻¹（math/big）的 1 tick 內、沒有漏掉的 arrival 與 S 的改變；B2：每個 request 的 since 與 next_fit 期限，沒有 tick 在期限後結束而沒有 ErrStalled，ErrStalled 的 since、期限與最早的 request 都對；valid 的模型不 stall、不 deadlock。最後一次執行（`engine-tests-verbose.log`）：3,000 個隨機模型中 1,201 個 compile（763 個有班表、其中 276 個也有故障，732 個有投入排程，184 個有有限 buffer）；檢查了 100,980 個 Capacity observation（50,784 次 S 增加）、6,620 次上班時間上的 constant 故障時點、12,838 件投入表的 arrival 與 41,589 件 rate 排程的 arrival（其中 21,363 件 deterministic 對精確 Λ⁻¹）、4,574 次進入 B2 的狀態；validator 被繞過的 1,931 個 run 中 69 個 ErrStalled，都在 B2 的狀態 |
| Kernel：differential | `differential030_test.go`、`neutral_calendar_test.go` | 見「Differential」 |
| Result | `internal/simulation/des/calendar_test.go`：`TestCalendarGoldenResults`、`TestCalendarStateIdentities`、`TestNeutralCalendarKeepsTheResult`、`TestNeutralScheduleKeepsTheResult`、`TestCalendarDeterminism`、`TestGoldenDigests040`；`breakdown_test.go` 的 `TestGoldenDigests030`（engine_version 換回）；`reference_test.go` | 每個 Result 依 schema 驗證；worker 數 1、3、8、0 的 digest 與 trace 相同（TEST-019），另一個 seed 不同 |
| Runner／API | `TestDeadlockError`、`TestStalledError` | fixture 的 engine_version 等於 `des.Version`（des-go-0.4.0） |
| AI | `TestCalendarRisk`、`TestCalendarValuesInEffects`、`TestLiftedPrimitivesHaveRiskCases`、`TestRefusesUnsafeChanges` 的「a calendar added」、`TestNoNewCalendars`、`TestScenarioMode`、corpus 的 MOD-039 | 見 scope 5b–5e |

### 先紅後綠

- 手推 golden（`calendar-goldens.json`，由 `checks/evidence/2026-10-25-T-049/calendar_goldens_gen.py` 組成 JSON；期望值都依各 case 的 `derivation` 手推輸入，沒有任何程式產生期望值）在 kernel 第一次執行前寫好。
- `red-patch-labels.log`：D-063 (b) B 的新測試在放寬前失敗；`red-validate.log`：validator 的新測試在 gate 解除與規則實作前失敗（60 個 `--- FAIL`，例如回 `PRIMITIVE_NOT_SUPPORTED`）；`red-engine-vet.log`：engine 的新測試對 e173ee1 的 kernel（`go vet -overlay`）無法編譯（`ObsCapacity`、`HasCalendars`、`capS`、`deadlineOf` 尚不存在）；`red-des.log`：des 的新測試對同一個 kernel 失敗（engine 以 PRIMITIVE_NOT_SUPPORTED 拒絕）；`red-ai.log`：AI 的新測試對 00cc382 的 review、idpool、aicontext 失敗（`TestCalendarRisk`、`TestCalendarValuesInEffects`、`TestLiftedPrimitivesHaveRiskCases`、`TestRefusesUnsafeChanges`、`TestScenarioMode`、`TestReviewEval`、idpool 無法編譯）。Engine 的 kernel 程式在本 session 中先於 engine 測試的程式碼寫成，所以 engine 的紅是以 overlay 對實作前的 kernel 重現的；行為層面的紅由 mutation 證明（「Mutation」）。
- **第一次執行**（`first-engine-run-calendar-goldens.log`）：kernel 第一次跑手推 golden，除了 G-C1（與 G-C1W，同一個 run）的事件數之外全部一致。推導的總結寫 29 個事件，把 tick 16 算成 3 個；它自己逐 tick 的推導在 16 列出 4 個事件（e4 的 SERVICE_COMPLETE、e8 的 ARRIVAL、CAPACITY_CHANGE、DISPATCH），自己的 sequence number 也以 20 的 dispatch 為 seq 30 結束：30 個事件。總結那一行在生成腳本中改正（改正寫在推導中），JSON 重產，其他都沒有改；是推導本身的計數錯誤，不是依 engine 改期望。Result 層的 19 個期望第一次就全部一致。
- **G-C9、G-C10 的第一次執行**（`first-engine-run-gc9.log`、`first-engine-run-gc10.log`）：mutation 第 2、3 次之後新增的兩個 golden，手推的 observation（G-C9 另有事件數 13 與 sequence number；G-C10 另有 block 與 buffer）與 Result 第一次就一致；同一次執行中只有數量的檢查（19／23、20／24）與兩個鎖失敗（尚未更新）。之後才把它們的 engine 輸出加入兩個 0.4.0 的鎖，並提高數量（最後 21 與 25）。
- `make backend-numeric` 第一次失敗（`backend-numeric-red.log`）：`releaser.draw` 的 `1 − U` 與 inline 的 Uniform 的乘法在 arm64、riscv64、loong64 被融合；改為先明確捨入 U（amd64 v1 的值與向量不變）。
- SimPy 的第一次執行失敗（`simpy-first-run.log`，見「SimPy reference」）：腳本自己的 bug。

## Differential（D-047）

- **對凍結的 des-go-0.3.0**（`frozen030_test.go`，935edfd；2f4c33f 的 kernel.go、replication.go、events.go 以 go/ast 改名，共用 Program）：`TestDifferentialAgainstFrozen030`，50,000 個隨機模型（PCG(seed, 4901)；seed%2 加 1–3 的有限 buffer，seed%4 ≥ 2 加 R3 成立的故障，constant 或隨機時鐘；部分 service 為 lognormal、Weibull），observation（kind、tick、seq、entity、node、resource、value、at）與 outcome、error 逐筆相同：50,000 個中 21,501 個 compile（4,947 個有有限 buffer、9,553 個有故障），10,428,435 個 observation 與 des-go-0.3.0 逐筆相同（其中 45,649 個 Blocked、121,862 個 ResourceDown；結束：正常 17,908、active 上限 1,602、visit 上限 1,991）；golden model 1、14、60 天、所有 blocking 與 breakdown golden 也比對。`make backend-test` 以 `GEMINUS_FULL_DIFFERENTIAL=1` 要求全部執行，race 下前 5,000 個。對 des-go-0.1.0、0.2.0 的 differential 照舊通過。
- **中性班表**（`TestNeutralCalendarsChangeNothing`）：上述隨機模型的部分 resource 放到 horizon 內 S＝C 的 calendar 上（一個涵蓋 period 的 shift；兩個 label 都是 C 或沒有列出；open 的例外；horizon 之後才停工的例外，使 pattern 不是 always-on、故障時鐘走上班時間的搜尋），新 kernel 對 des-go-0.3.0 跑同一個 Program 逐筆相同，沒有任何 Capacity observation：6,000 個中 2,106 個 compile（948 個有故障），1,009,495 個 observation（13,134 個 ResourceDown）逐筆相同。
- **中性排程**（`TestNeutralSchedulesChangeNothing`，D-053 G-R4）：source 的 constant interarrival c（2 的冪：0.25–8 分）換成一段 rate 1/c 的 deterministic 排程（period c·m），新 kernel 跑排程的模型與 des-go-0.3.0 跑 constant 的模型逐筆相同（含 sequence）：4,000 個中 1,433 個 compile，655,134 個 observation 逐筆相同。
- **鎖定的 digest**：0.1.0、0.2.0、0.3.0 的鎖不重產，Result 的 engine_version 換回各自的版本後重算的 digest 等於鎖定值；三個 kernel observation 鎖照舊通過。新版另立 `engine-golden-digests-0.4.0.json`（21 個 golden、C-S1–C-S4）與 `kernel-observation-digests-0.4.0.json`（21 個 golden、C-S1–C-S4）：engine 輸出的 regression lock，在手推期望與 SimPy 通過之後才取（G-C9、G-C10 的一筆在 e9ad4a9、f7fe9df 加入，其他不變）。

## SimPy reference（`make simulation-reference-test`）

`scripts/simpy_reference.py`（SimPy 4.1.2，沒有新 dependency）以 SimPy 自己的 primitive 解讀班表：有 calendar 的 resource 是 `simpy.PriorityResource`，下班的 unit 由 priority −0.5 的 blocker request 持有（在故障的 −1 之後、job 的 0 之前）；S(t) 由腳本自己從 IR 計算（不用 engine 的 calendar package）；t=0 先擋住 C − S(0) 個 unit；S 減少時 request C − S 個 blocker（服務中的 job 先做完），增加時先取消還在等的 blocker、再釋放已取得的；calendar 的事件與有 calendar 的 resource 的故障時鐘、修復以 SimPy 的 URGENT priority 排程（kernel 的 priority 15 先於 dispatch）；故障時鐘在上班時間上累積 TTF。投入表在 k·P＋A_j tick 投入（P、A_j 是 period 與 at 各自量化後的 tick，D-082 (b) A；review 第 1 輪 M1 之前腳本誤以連續時間的 k·period＋at 投入，見「Review 第 1 輪」）；deterministic 以 `fractions.Fraction` 精確計算 Λ⁻¹ 再依 D-053 量化；poisson 以 Lewis–Shedler thinning（Python 的 random）產生，是與 engine 的累積 rate 反函數不同的演算法。

- **接受的模型**（執行前檢查，其餘一律拒絕）：T-047、T-048 的條件；有 calendar 的 resource 必須是每個需要它的 bundle 中唯一的 resource；有 calendar 又會故障的 resource 每班人數只能是 0 或 C；每個需求 1 unit。拒絕：G-C4、G-D4C（兩個有 calendar 的 resource 的 bundle）、G-D3C（units 2）、G-D5（會故障的 m 與夥伴 o 同一 bundle）。
- **確定性**：G-C1、G-C1W、G-C1W10、G-C2、G-C5、G-C6、G-C7、G-C8、G-C9、G-C10、G-F6、G-CF1、G-R1、G-R5、G-R7 逐值比對 arrival、service 開始、完成、停機、等待中的故障、S 的每次改變與 Result 成員（含 `resource_states` 的班表組），與 T-047、T-048 的 case 一起通過。G-T5（故障恰在下班的 tick：腳本的 blocker 先取得下班的 unit，kernel 把它算作閒置）、G-K2（kernel 的 B1）、G-D1（SimPy 不偵測 stall）不比，原因寫在 fixture 的 `simpy` 欄位。
- **第一次執行的差異**（`simpy-first-run.log`）：C-S1 的 completed、utilization、scheduled utilization、overtime、idle 五個指標超出容差，其他 case 都通過。原因是腳本自己的 bug：C-S1 的 calendar 從 0 到 360 都下班，腳本只在 S 改變時要求 blocker，沒有為 S(0) 擋住 unit，所以它的機台在第一班之前就服務了 job。Engine 的數字與粗估一致（第 0 天約 330 件 × 3 分＝990 unit-分鐘，第 1 天 [2280, 2760) 滿載 960，合計約 1956，engine 1960）。腳本改為 t=0 先擋住下班的 unit（不記為一次改變，D-047）；手推 golden 與容差都沒有改。
- **隨機**：C-S1（兩班、停工的一天、exponential）、C-S2（每班 3／2／1 人、lognormal service、三段 poisson 排程）、C-S3（有班表又會故障的站：Weibull TTF（上班時間）、lognormal TTR，在有限 buffer 之後）、C-S4（deterministic 排程與投入表進入兩個不同班表的站，中間有限 buffer）；30 replications、warmup 300、horizon 3,000 分鐘；57 個指標（C-S1 8、C-S2 11、C-S3 16、C-S4 22）（completed、lead time、utilization、scheduled utilization、overtime／available／idle 比例、停機、阻塞、rework、每個排程段或投入列的件數）都在 fixture 預先固定的容差內（|Δ| ≤ 4·√(s²_go/n＋s²_simpy/n)＋0.5%·max(|平均|)＋1e-9，沿用 T-047 的容差）。全部 140 個隨機比對（T-047 的 45 個、T-048 的 38 個、本任務的 57 個）都通過，確定性的 32 個 case（T-049 的 15 個）逐值相同。
- **腳本自己的 mutation**：見「Mutation」的 SimPy 列。

## Mutation

`checks/evidence/2026-10-25-T-049/t049-mutants.py`：每個 Go mutant 是一個精確的 search/replace，以 `go test -overlay` 套用（working tree 不變），列出的 package 有測試失敗即為 KILLED（`-failfast`：log 中的測試是第一個失敗的）；Python mutant 套用在 `scripts/simpy_reference.py` 的暫存副本上，對 Go 的 reference 結果（`bin/simpy-reference-go.json`，由 `make simulation-reference-test` 先寫出）執行，exit 非 0 即為 KILLED；pattern 不是恰好出現一次即為 BAD-MUTANT；標 equivalent 的必須存活，原因寫在 harness 中。

**最後的結果**：第 3 次（`mutants-run3.log`，e9ad4a9，完整）**54 個 mutant 中 51 個被擋下、3 個 equivalent**，exit 0；其中兩個第一個失敗的測試是 0.4.0 的鎖，處理之後（G-C10，f7fe9df）三個相關 mutant 在最後的 tree 上重跑都被擋下（`mutants-after-gc10.log`，exit 0）。Task 指定的兩個 mutant（bound 的 `>`→`>=`、上班不喚醒）單獨對 G-K2 執行也都被擋下（`required-mutants-g-k2.log`：`deadlock-bound-ge` 使 G-K2 回 ErrDeadlock，`shift-start-no-wake` 使它在 tick 300000 回 ErrStalled）。

| 類別 | Mutant（擋下它的測試） |
|---|---|
| B1（scope 3c，指定的 mutant） | `deadlock-bound-ge`：`>`→`>=`（`TestBreakdownGoldens`；單獨對 G-K2 也擋下）；`deadlock-bound-uses-shift`：bound 改用上班人數 S（`TestCalendarGoldens`／G-K2） |
| 上班的喚醒（scope 3b，指定的 mutant） | `shift-start-no-wake`：上班不喚醒（`TestCalendarGoldens`）；`shift-start-wakes-without-dispatch`（喚醒但不排 dispatch）；`shift-end-wakes-too`（下班也喚醒：G-C9 的事件數 14、e2 的 dispatch seq 11）；`shift-start-satisfies-waiting-failure`（上班給等待中的故障 unit，D-081 (c)，G-CF1） |
| 授予與 CAPACITY_CHANGE | `dispatch-ignores-shift`（授予不看 S）；`capacity-observes-unchanged`（沒有改變也記 observation）；`capacity-next-change-last`（先改 S 再排下一個，D-081 (a) B）；`capacity-resources-reversed`、`calendars-reverse-id-order`（順序）；`clocks-before-calendars`（t=0 的順序，`TestShiftEndBeforeFailure`／G-T5） |
| 故障時鐘（scope 3a） | `failure-clock-calendar-time`（時鐘走日曆時間：`TestCalendarGoldens`）；`failure-at-end-is-next-on`（目標恰在下班時改為下一次上班：`TestPatternAgainstTheDefinition`） |
| B2（scope 2f） | `b2-deadline-ignores-xend`、`b2-deadline-no-next-fit`（`TestCalendarGoldens`）；`b2-deadline-no-ttrmax`（`TestBreakdownGoldens`）；`b2-since-reset-by-shift-wake`（`TestStallWithCalendar`） |
| 投入排程（scope 1e） | `release-no-floor`（`TestReleaseRunVectors`）；`release-no-correction`、`release-no-clamp`（`TestReleaseProfileVectors`：P＝0.3、a＝502.5 的修正，`clamp` 向量）；`release-segment-strictly-after`（第一個失敗的是隨機檢查器 `TestRandomCalendarModelsAgainstSpec`）；`release-extra-draw`（第一個失敗的是 `TestCalendarObservationDigests`）；五個 release mutant 只對 Python port 的向量執行也都被擋下（`release-vector-mutants.log`：`boundary` 向量擋下 `release-segment-strictly-after`，`TestReleaseRunVectors` 與 G-R3 擋下 `release-extra-draw`）；`table-rows-after-entities`、`table-schedules-an-empty-row`、`table-quantity-not-clipped`（`TestCalendarGoldens`／G-R7） |
| Result（scope 1c、1d） | `overtime-zero`、`available-ignores-shift`、`down-does-not-update-shift-members`、`calendar-resources-not-listed`（`TestCalendarGoldenResults`）；`processing-ignores-blocked`（∫min(U, S) 取代 ∫min(U − K, S)：G-C10 的 scheduled processing 9 而不是 6，見下） |
| Validator（scope 2b、2e） | `r2-off`、`r4-off`、`r4-reads-exceptions`、`lcm-unbounded`（`TestBundleFeasibility`） |
| Risk、pool 與 effects（scope 5b、5d） | `risk-resource-calendar-off`、`risk-ignores-shift-capacity`、`risk-calendar-ignores-exceptions`、`risk-calendar-ignores-shifts`、`risk-schedule-off`（`TestCalendarRisk`）；`risk-calendars-added-low`、`risk-schedule-ignores-releases`（`TestLiftedPrimitivesHaveRiskCases`）；`idpool-allows-new-calendars`（`TestReviewEval`）；`effects-without-calendar-values`、`effects-without-schedule-values`（`TestCalendarValuesInEffects`） |
| SimPy reference | `simpy-no-blockers`、`simpy-no-initial-blockers`、`simpy-failure-clock-calendar-time`、`simpy-calendar-events-normal`、`simpy-thinning-keeps-all`、`simpy-shift-start-releases-nothing`（手推 golden 或隨機比對不符） |
| Equivalent | `b2-next-fit-ignores-exceptions`：next_fit 只從 max(since, X_end) 之後問起，那裡已沒有例外，計不計入例外相同（預先標為 equivalent）；`b2-off-with-calendars-only`（第 1 次 SURVIVED，分析後標為 equivalent，D-081 (f)）：沒有故障時 B2 永遠不會觸發：在 B2 狀態的 request 是它每個 FIFO 的最前面、bundle 沒有任何 unit 在使用，期限 next_fit(…) 是某個 CAPACITY_CHANGE 讓 bundle 湊齊的 tick，那個事件喚醒它、同 tick 的 dispatch 授予它（它是最前面，別人拿不走），所以不會過期限，檢查只是防護；`release-first-floor-one-tick`（第 1 次 SURVIVED，分析後標為 equivalent，D-082 (a) 因此更正）：U 嚴格在 (0, 1) 內（`rng.go`），X₁ > 0、τ(X₁) > 0，`quantize` 對正的時間至少給 1 tick，所以第一件的下限是 0 或 1 都不會用到 |

**過程**（每一次的處理都先寫測試或手推期望，沒有降低任何 assertion）：

- **第 1 次**（`mutants-run1.log`，8de3be9 加上當時未 commit 的修改，harness 是當時的版本）：54 個中 47 個被擋下、1 個 equivalent、3 個 BUILD-FAILED、3 個 SURVIVED，exit 1。處理都在 b6f856c：
  - 三個 BUILD-FAILED（`shift-end-wakes-too`、`release-no-clamp`、`processing-ignores-blocked`）是 mutant 本身寫錯（拿掉的運算使變數未使用），改為保留變數的寫法。
  - `b2-off-with-calendars-only`、`release-first-floor-one-tick` 的 SURVIVED 經分析是等價的（上表），在 harness 中標為 equivalent 並寫出原因；D-082 (a) 原本寫「只有 X₁＝0 時第一件在 tick 0」，這不可能發生，D-082 (a)、planning/decisions.json 與 `replication.go` 的註解改正（行為不變），D-081 (f) 註明 B2 在沒有故障的程式中只是防護。
  - `r4-reads-exceptions` 的 SURVIVED 是測試太弱：「R4: the exceptions do not count」的例外在 [1440, 1920)，在兩個 period 的 LCM（一天）之外，R4 本來就看不到；新增例外在第一個 period 內的 case（`R4: a closed exception in the first period does not count either`：o 在 100–200 停工、m 在上班），計入例外的 mutant 會回 `STALL_RISK`。
  - `release-segment-strictly-after` 只被隨機檢查器擋下、`release-no-clamp` 沒有被任何向量測到：Python port 新增 `boundary`（rate 0 的段夾在兩段中間，a 恰為第一段的累積終點時在第一段的結束投入）與 `clamp`（P＝0.1×3 進位為 0.30000000000000004，a＝P 時 τ 超出段的結束，clamp 回 3）的 Λ⁻¹ 向量，`engine-vectors.json` 的 release_steps 27 → 38。
- **第 2 次**（`mutants-run2-stopped.log`，b6f856c）在第 18 個 mutant 後由我中止：`shift-end-wakes-too`（修正後可以編譯）第一個失敗的是 `TestCalendarObservationDigests`；略過兩個鎖重跑 engine 與 des 的全部測試都通過（`shift-end-wakes-too-without-locks.log`）。它唯一的效果是下班時多一個不授予任何東西的 DISPATCH（事件數與之後的 sequence number），而沒有任何 golden 在下班時有 held 在最前面、該 tick 又沒有別的事件喚醒的 request。為了不讓這條規則（D-052 (a)、D-081 (b)）只靠 engine 自己的輸出，新增手推的 **G-C9**（e9ad4a9：1 unit、每 10 分鐘的 [0, 5) 上班、兩件、服務 6 分；e2 在 5 下班時 held 在最前面；13 個事件、e2 在 10 由 seq 10 的 dispatch 開始；Result：in use 12、scheduled 10、overtime 3、idle 1）。
- **第 3 次**（`mutants-run3.log`，e9ad4a9，與第一輪驗證同時執行）：51/51 被擋下、3 個 equivalent，exit 0。我逐一檢查第一個失敗的測試：`release-extra-draw` 與 `processing-ignores-blocked` 是 0.4.0 的鎖。略過鎖單獨重跑（`lock-only-mutants-without-locks.log`）：`release-extra-draw` 由 `TestReleaseRunVectors`（Python port 的逐 bit arrival）擋下；`processing-ignores-blocked` 沒有其他測試擋下：沒有任何 golden 有被擋的 entity 持有有 calendar 的 unit。新增手推的 **G-C10**（f7fe9df：m 每 20 分鐘的 [0, 10) 上班，2 分的 work 之後是容量 1 的 buffer 與 10 分的 pack；e3 在 7–13 被擋在 work、跨越 10 的下班持有 m；m 的 in use 12、blocked 6、scheduled 15、min(U, S) 9、overtime 3、idle 6、scheduled processing 6（mutant 給 9）；也寫出 block、buffer 滿的時間 18 分與等待）。
- **G-C9、G-C10 的順序**：先寫推導與期望，engine 第一次執行就與手推一致（`first-engine-run-gc9.log`、`first-engine-run-gc10.log`：該 case 的子測試 PASS，只有尚未更新的數量檢查與鎖失敗），之後才把 engine 的輸出加入兩個 0.4.0 的鎖（其他筆不變）並提高數量；SimPy 也比對兩者。`mutants-after-gc10.log`：`shift-end-wakes-too`（`TestCalendarGoldens`）、`processing-ignores-blocked`（`TestCalendarGoldenResults`）、`release-extra-draw` 在最後的 tree 上都被擋下。

## Fuzz

| Fuzzer | 執行 | Execs | 結果 |
|---|---|---:|---|
| `FuzzCalendars`（新；隨機模型的 seed 與是否繞過 validator：班表、投入排程、故障；每個 run 對只看 observation 的班表檢查器；經 validator 的模型不得 stall 或 deadlock，繞過時 ErrStalled 必須在 B2 的狀態） | 120 s，`-parallel 1`（b6f856c 之前） | 53,731 | PASS（`fuzz-calendars.log`） |
| `FuzzKernel`（加 calendar-goldens.json 的模型為 seed） | `-fuzztime 500000x`，`-parallel 2`，25 s | 500,000 | PASS（`fuzz-kernel-count.log`） |
| `FuzzRun`（加 calendar-goldens.json 的模型為 seed：typed failure 或 schema-valid、digest 可重算的 Result 與 trace） | `-fuzztime 200000x`，`-parallel 2`，24 s | 200,000 | PASS（`fuzz-run-count.log`） |

另外 `FuzzKernel`、`FuzzRun` 各以 `-fuzztime 60s` 執行（`fuzz-kernel.log`、`fuzz-run.log`，以及 e9ad4a9 上的 `*-pre-gc10.log`），都是 PASS，但它們最後一行的 exec 數（192、84；e9ad4a9 上 4,639、120,761）不可信：Go 的 fuzz worker 分批回報 exec 數，60 秒結束時還在進行的那一批沒有計入（log 中長時間 0/sec 之後一次跳增數十萬）。為了確認不是某個 input 跑太久，我用一個加了 5 秒 watchdog 的 `FuzzKernel` 暫時副本（存下超時的 input 並中止 worker；沒有 commit，只在終端機）跑了 90 秒：843,851 個 exec，沒有任何 input 超過 5 秒；之後改用以次數為準的 `-fuzztime Nx` 取得上表的數字。

Exec 數包括很快就 decode 或 compile 失敗的輸入（`FuzzKernel`、`FuzzRun` 的輸入是任意 bytes），不等於跑完的模型數。新的 interesting input 只存在 Go 的 fuzz cache，沒有失敗的 input，repository 中沒有新的 `testdata/fuzz` 檔案。


## NOT RUN

- **所有 DB 測試，交給 coordinator 在一次性 DB 上執行**：`make integration-test`（`make db-test` 與 `make db-go-test`）。這裡沒有 PostgreSQL，依指示沒有跑、也沒有嘗試以其他方式取得 DB。本任務沒有 migration，`db/tests/*.sql` 沒有改（`PASS_DB_TESTS_FRESH`）。engine_version（des-go-0.4.0）、班表與排程的模型經過 DB 的路徑，下列 DB-backed Go 測試在本 worktree 中都是 skip（`db-tests-skipped.log` 列出全部 skip）：
  - `internal/runs/runner`：`TestEventBudgetAcrossRetries`、`TestWallClockAcrossRetries`、`TestACrashedAttemptIsRunAgain`、`TestNoResultAfterALostLease`、`TestALateCancellationWins`、`TestRefusedAttempts`（engine_version 不符）、`TestOneJobPerRun` 等；
  - `internal/api` 的 run、proposal、scenario 測試：`TestRunClosedLoop`、`TestRunRoleMatrix`、`TestRunRequestRefusals`、`TestRunIdempotency`、`TestRunDeterminism`、`TestScenarioRun`、`TestRunFailures` 等；
  - `cmd/api`、`cmd/worker`、`cmd/provision-tenant`、`internal/worker`、`internal/models`、`internal/proposals/...`、`internal/scenarios`、`internal/storage/...`、`internal/tenant/...` 的 DB-backed 測試與整個 `make integration-test`。
- **有班表或排程的模型經 API 的 closed loop**（publish → scenario（含 AI scenario 改 shifts）→ run → result）只在 validator、patch、review、engine 與 des 層測過，DB／API 層在本 worktree skip。
- **沒有端對端的 `SIMULATION_STALLED`（班表的部分）測試**：valid 的模型因 R2–R4 不會 stall，經 API 建立的 run 走不到 B2；runner 與 API 的對應只有 unit test（message 格式不變）。
- `make frontend-test`：本任務只改了 schema 的 `$comment`，產生的 TS 沒有變（`PASS_TS_CONTRACTS_FRESH`，15 檔、106 types），`apps/web` 沒有改；依指示「TS 有改才跑」沒有執行。UI 如何顯示 S(t)、overtime 與 scheduled utilization 屬 T-031／T-032／T-034。
- `make e2e-test`：沒有 UI 變更，未執行。`make benchmark-test`：仍是 NOT IMPLEMENTED 的 placeholder；CAPACITY_CHANGE、上班時間的故障時鐘搜尋（每次 FailureAt 對 Θ 二分搜尋）與 B2 的 next_fit 的成本沒有效能量測（T-037）。
- Live AI 與 `make ai-eval-live`：live AI 未核准（H-001、H-017，T-053 之前關閉）；MOD-039 的真實模型回答（是否捏造時間、是否嘗試新增 calendar）沒有評分（T-038）。Edit mode 的 prompt 沒有告訴 model「不能新增 calendar」（D-083 的後續）。
- `make backend-race` 只在 linux/amd64 執行；`make backend-numeric` 只檢查其他 GOARCH 的組語，沒有在實體 arm64 等機器上執行數值測試，跨架構的 byte-identical digest 仍不宣稱（SIMULATION_SPEC §4）。
- CI 上沒有執行（只在本機）。

## 需要 owner 決定

五項都是 **PROPOSED**（docs/DECISIONS_AND_TBD.md 的「T-049 的提案」與 planning/decisions.json；`approved_by`／`approved_at` 為 null）。建議都已實作，可以回退；不採用時要改的地方寫在各項最後。T-049 以它們為 gate，所以 `make spec-decisions` 把 T-049 列在 `next_after_review`、等 D-081–D-085。

- **D-081 — 班表的 kernel 解讀**。建議：(a) 一個 calendar 一個 CAPACITY_CHANGE，先排下一個再改 S，只記改變的 resource（A；B 先改 S 只改 sequence；C 每個 resource 一個事件）；(b) S 增加喚醒 held front 並排 dispatch、減少不喚醒（照 D-052）；(c) 上班不滿足等待中的故障（A；B 會讓故障搶走服務中的 unit）；(d) t=0：sources、calendars（依 ID）、故障時鐘；(e) 故障時鐘依 Θ 只在上班時前進，目標恰在下班時為該 tick，0 tick 的 TTF 立即故障；(f) B2 在有故障或班表的程式中都檢查，期限 next_fit(max(since, X_end)＋TTRmax) 在進入狀態時算一次，沒有故障時不會觸發（只是防護）；B1 的 bound 仍是 C；(g) 授予 in_use＋units ≤ min(S, C − D)。
- **D-082 — 投入排程的 kernel 解讀**。建議：(a) 第一件的下限 tick 0（不會用到：U 在 (0, 1) 內，第一件至少在 tick 1）、晚於 horizon 不排程；(b) 投入表的 period 與 at 先各自量化（A，證據是 review 第 1 輪新增的 G-R8；B 以 model 時間計算會漂移）；(c) 先排下一列再建立 entity，達到 max_entities 的列不再排下一列（A；B 只改 sequence；C 多一個空事件）；(d) poisson 用 arrival stream、U 先捨入（FMA）。
- **D-083 — AI proposal 新增的 calendar 的 ID（T-046 review I3）**。建議 A：禁止（pool 不配發 calendar ID，`ID_POOL_VIOLATION`）；B：pool 加 calendar 種類（schema、migration、generator、context、review 都要改）；C：任意 ID（違反 D-016）。另：edit 的 prompt 沒有在 T-049 改，告訴 model 這條規則列為後續（modeler-0.3.0）。
- **D-084 — utilization 分母的標示**。建議 A：不新增 Result 成員，在 schema 的 `$comment` 與 SIMULATION_SPEC 標示 C·T 與 scheduled utilization 的算法（Result、digest、TS 不變）；B：加標示成員；C：加 scheduled_utilization 的 MetricSummary。依據（review 第 1 輪更新）：APPROVED 的 D-058（H-018）與 ELECTRONICS_LINE §5 已把 ADR-026 D-052 (i) 分開：generic 的積分在 `resource_states`（本任務已實作），Scheduled utilization 與 Overtime 的比例是 `metrics.domain` 的 pack 指標（T-051），所以 C 會與 D-058 的指標重複。留給 owner 的問題只剩：除了 contract 的 `$comment`，Result 實例本身是否必須帶分母的標示（B）（見「偏離」）。
- **D-085 — validator 細節**。建議照列：LCM 涵蓋所有 calendar、報在 `/calendars`；R2 報在 `/nodes/<i>/requires`；R4 只看週期部分、R2／R3 失敗的 bundle 不再檢查；沒有被參照的 calendar 允許；投入表時刻量化後檢查；calendar ID 自己的 namespace。

## 偏離與原因

- **Utilization 分母的「在 Result 中標示」**：以 contract（`simulation.schema.json` 的 `$comment`）與 SIMULATION_SPEC 標示，不是每個 Result 實例中的成員（D-084 A）。原因：加成員是 contract、TS 與所有 Result digest 的變更；之後 APPROVED 的 D-058（H-018；ELECTRONICS_LINE §5 的指標表與 resource_states 表）已把 ADR-026 D-052 (i) 分成 generic 的 `resource_states` 積分（本任務已實作）與 `metrics.domain` 中的 pack 指標 Scheduled utilization、Overtime（T-051），所以 ADR-026 (i) 的 `scheduled_utilization` 由 D-058 的 pack 指標提供，C 會與它重複。Reviewer 指出 A 沒有字面上做到「在 Result 中標明」：owner 若認為 Result 實例本身必須帶分母標示，改採 D-084 B（review 第 1 輪更新）。
- **ADR-026 D-050 原訂的 reference 只做了一部分**（review 第 1 輪 L2 指出原本沒有列出）：R2、R3、R4、next_fit、X_end（與 TTRmax）的獨立 Python 實作已在 review 第 1 輪與 Go 比對（`scripts/calendar_reference.py`，`make simulation-reference-test`）；R1 的獨立 Python 實作、以 SimPy 跑 D-050 的兩個反例與 G-D1（示意）、對隨機 valid 模型以 SimPy 確認每個 L 內都有授予，三者都沒有做（ADR-026 D-050 的「SimPy」段與「Property」），列為 T-037 scope 的後續（`planning/tasks.json`）；原因見「Review 第 1 輪」的 L2。
- **Edit mode 的 prompt 沒有改**：D-083 的規則由 server 強制（review 與 approval 的 revalidation），但 modeler-0.2.0 的 prompt 沒有告訴 model；scope 只到 scenario allowlist 的文字（coordinator 的「proposals beyond the scenario allowlist text」不碰）。後果：live model 若嘗試新增 calendar，proposal 會是 invalid（安全），而不是事先回 needs_input。
- **Engine 的紅以 overlay 重現**：kernel 的程式在本 session 中先於 engine 測試的程式碼寫成（手推 golden 先寫好）；engine 與 des 的紅是把測試對 e173ee1 的 kernel 以 `go test -overlay`／`go vet -overlay` 執行而得（`red-engine-vet.log`、`red-des.log`），行為層面由 mutation 補足。
- **G-C1 的事件數**：推導總結的 29 改為 30（推導本身的計數錯誤，逐 tick 的推導與其 sequence number 給 30；改正寫在推導中，見「先紅後綠」）。
- **D-082 (a) 的更正**：最初寫「只有 X₁＝0 時第一件在 tick 0」；mutation 顯示下限永遠用不到（U 嚴格在 (0, 1) 內，X₁ > 0），D-082 (a)、planning/decisions.json 與 `replication.go` 的註解在 b6f856c 改正，行為不變。
- **驗證之後又加了兩個 golden**：mutation 第 2、3 次各找到一個只被 0.4.0 的鎖擋下的 mutant，所以在第一輪驗證（e9ad4a9，`*-pre-gc10.log`）之後新增 G-C10（G-C9 在那之前），tree 因此改變；最後的 tree 上重跑了全部的驗證指令與受影響的三個 mutant（`mutants-after-gc10.log`），沒有重跑完整的 54 個 mutant（G-C10 只加測試資料與數量，程式沒有變，加測試只會擋下更多 mutant）。
- **SimPy 的 URGENT priority 只用在有 calendar 的 resource**：T-048 的故障時鐘（沒有 calendar 的 resource）仍以 NORMAL 排程，T-048 的 deterministic 比對與「不比」的清單都不變。

## 殘餘風險

- **B2 的保證依賴 R2–R4**：valid 的模型因 R2–R4 永遠會在期限前被授予；沒有故障的程式中 B2 不會觸發（D-081 (f) 的推理，mutant `b2-off-with-calendars-only` 等價）；TTRmax 沒有上界時不檢查。R4 只看週期部分，例外中的情形由 B2 的 X_end 涵蓋（G-D5）。
- **R4 是保守的**：會拒絕「會故障的機台沒有班表、作業員有班表」的寫法（D-050 的已知代價），訊息指出改法。
- **同 tick 的順序只由 kernel 自己的 golden 驗證**：SimPy 以 URGENT priority 近似 priority 15，但下班恰為故障時刻（G-T5）與 B1 的狀態（G-K2）沒有獨立的 reference；故障與上班人數 0 < S < C 同時用的情形 SimPy 不接受，只由手推 golden 與檢查器驗證。
- **SimPy 只涵蓋部分模型**：有 calendar 的 resource 必須是 bundle 中唯一的 resource；兩個班表的 bundle（G-C4、G-D4C）只由手推 golden 與檢查器驗證。
- **數值**：deterministic 排程的 tick 與精確值的差 ≤ 1 tick 由檢查器容許（binary64 與精確值在 .5 附近可能不同捨入），Python port 逐 bit 固定 engine 的值；跨架構只檢查組語。
- **效能**：FailureAt 每次對 Θ 二分搜尋（每步 O(例外數)），CAPACITY_CHANGE 每個 calendar 每次改變一個事件，B2 的 next_fit 在 request 進入狀態時算一次（掃描 S 的改變點直到湊齊或 horizon）；沒有 benchmark（T-037）。
- **Mutation 是抽樣**：54 個 mutant 中有兩個（`shift-end-wakes-too`、`processing-ignores-blocked`）原本只被 0.4.0 的鎖擋下，已補手推的 G-C9、G-C10；harness 沒有涵蓋的規則仍可能只有鎖（engine 自己的輸出）在保護。
- **Regression lock 不是 oracle**：`engine-golden-digests-0.4.0.json`、`kernel-observation-digests-0.4.0.json` 是 engine 的輸出；語義由手推 golden、檢查器與 SimPy 驗證。
- **中間的 commit 沒有各自驗證**：只有 e9ad4a9 與最後的 tree 跑過全部的指令；完整的 mutation 第 3 次在 e9ad4a9 上。

## Coordinator 的執行（b4ef811）

Coordinator 把十三個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（T-054 完成的 7357b8d 之後）。衝突只在 `planning/decisions.json`（本任務的 D-081–D-085 與 H-023 的 D-087 都是新增，兩邊都保留，依 ID 排列；決策登記檔自動合併）與 `make spec-check` 重產的 `checks/*.json`（取本任務的版本後重產）。T-054 在同一段時間改了 `internal/storage/datasets` 與各 route 的 binding failure 對應，本任務沒有碰那些程式，cherry-pick 沒有衝突；兩者合在一起的行為由下面的整合執行驗證。

第一次執行（`coordinator-exit-codes-first-run.txt`）：`spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`frontend-test`、`simulation-reference-test` 都是 exit 0；`backend-numeric`、`backend-race` 與 `integration-test` exit 2，原因是磁碟已滿（log 為 `no space left on device`，`*-first-run-disk-full.log`），不是測試失敗。Coordinator 清除 Go 的 build cache（15 GB）並移除四個已整合任務的乾淨 worktree 後，在同一個 tree 上重跑這三項：`backend-numeric`、`backend-race`（engine package 434 s）與新的 `dt_test_t049b` 上的 `integration-test`（所有 migration 與 SQL suite，32 個 package ok，沒有 FAIL 或 SKIP）都是 exit 0（`coordinator-environment.txt` 記錄 HEAD b4ef811、go1.27.3、PostgreSQL 16.15）。本報告 NOT RUN 中的 DB 測試因此都已執行；`make e2e-test`、benchmark 仍為 NOT RUN。D-081–D-085 仍是 PROPOSED。Log 與 exit code 在 `checks/evidence/2026-10-25-T-049/coordinator-*`。

## Review 第 1 輪（2026-10-25）：CHANGES REQUESTED（兩個 Medium、兩個 Low、Info）

在 `t049-r1`（從整合後的 3e56705 開出，不 push）修正；每個修正都先寫測試，確認新測試在 mutant 或舊程式上失敗（red），再確認通過；沒有降低任何 assertion 或加 skip，沒有改程式碼的行為（只有測試、fixture、SimPy 腳本、新的 Python reference 與文件）。Reviewer 的 probe 與工具（`t049-review-tools/`）只讀取使用。Evidence 在 `checks/evidence/2026-10-25-T-049-r1/`。D-081–D-085 仍是 PROPOSED（`approved_by`／`approved_at` 為 null），建議不變；T-049 在 tasks.json 的狀態未改。

### Commit

| Commit | 內容 |
|---|---|
| 2fa48e6 | M2、L1：`TestBundleFeasibility` 加 R4 只在較大 period 之後衝突的 case、LCM 恰為 365 天與 730 天的 case；`TestCalendarRules` 加 period 恰為 365 天（valid）與 365 天＋1 tick（invalid）；`TestHandCases` 加 LCM 的上限；red／green log |
| 9101a72 | M1、I1、I2：手推 golden G-R8（投入表的 period 與 at 不是整數 tick）、G-R9（rate 在上限）；`scripts/simpy_reference.py` 自己量化投入表；G-F6 推導的文字；兩個 0.4.0 鎖加入 G-R8、G-R9（其他不變）；測試中的數量（23 個 golden、27 個鎖定 case）；mutation harness 加入 reviewer 的三個 mutant 與兩個 SimPy mutant；red／green log |
| b6781e3 | L2：`TestCalendarRulesExport`（Go 端）、`scripts/calendar_reference.py`（獨立的 Python R2、R3、R4、next_fit、X_end、TTRmax）、`make simulation-reference-test` 執行兩者；L2 的 mutant harness 與 log；T-037 scope 的後續註記 |
| 534de30 | D-081 (e)（I5）、D-082 (b)（M1 的證據）、D-084（依據 D-058 與 ELECTRONICS_LINE §5）的文字；SIMULATION_SPEC、TEST_PLAN；`docs-check.json` |
| （最後一個） | 本節、本報告前面引用 G-R7 為 D-082 (b) 證據之處與 D-084 的「偏離」改正；驗證 log、`mutants-run4.log`、`exit-codes.txt`、`environment.txt` |

### M1（Medium）：D-082 (b) 的投入表量化沒有測試；SimPy 錯讀 period 不是整數 tick 的投入表

- **發現**：mutant `rv-table-period-in-model-time`（選項 B：以 model 時間算 k·period＋at 再量化）通過所有測試：G-R7 的 period 是整數 tick，A 與 B 相同，所以 D-082 (b) 與本報告以 G-R7 為 A 的證據是錯的。`scripts/simpy_reference.py` 在連續時間 k·period＋at 投入，沒有量化，也沒有拒絕這種模型。
- **修正**：手推 golden **G-R8**（`calendar-goldens.json`，推導在 `calendar_goldens_gen.py`）：秒為單位，period 0.0026 s＝2.6 ms → 3 tick，兩列 at 0 → 0、0.0012 s＝1.2 ms → 1 tick（都不在半 tick 附近，binary64 動不了）；A 在 0、1、3、4、6、7、9、10、12、13 ms 投入（horizon 13 ms，10 個事件），B 在 0、1、3、4、5、6、8、9、10、12、13 ms（11 件，第三個 period 起不同）。SimPy 腳本改為自己由 IR 把 period 與每列的 at 各自量化（round half up、正值至少 1 tick），在 k·P＋A_j tick 投入，並逐值比對 G-R8。D-082 (b)（DECISIONS_AND_TBD 與 decisions.json）、本報告 scope 1e、「需要 owner 決定」與 SimPy 一節改為引用 G-R8。
- **先紅後綠**：`first-engine-run-gr8-gr9.log`：G-R8 的 engine 與 Result 子測試第一次就與手推一致（只有尚未更新的數量與鎖失敗），之後才把 engine 輸出加入兩個鎖；`red-simpy-gr8.log`：改腳本之前 `make simulation-reference-test` exit 2（G-R8 的 arrival 在 0.0012、0.0026、0.0038…，11 件）；`green-simpy-gr8.log`：exit 0。
- **擋下的測試**：`rv-table-period-in-model-time` 被 `TestCalendarGoldens`／G-R8 擋下（11 個事件，arrival 正是推導中 B 的時刻）；SimPy 的 mutant `simpy-table-model-time`（腳本改回 B）被 G-R8 擋下（`mutants-run4.log`）。

### M2（Medium）：R4 只在較大 period 之後才衝突的情形沒有測試

- **發現**：mutant `rv-r4-first-period-only`（`calendars.go` 的 span 取兩個 period 的較大者而不是 LCM）通過所有測試。
- **修正**：`TestBundleFeasibility` 加入 reviewer 的 probe：會故障的 worker 在每 4 分鐘的 [2, 3) 上班，helper 每 6 分鐘在 [4, 5) 下班；兩者在 2、6 同時上班（R2 成立），worker 上班而 helper 下班只在 [10, 11)：在較大的 period（6）之後、LCM（12）之內 → `STALL_RISK` 於 `/nodes/1/requires`。
- **擋下**：`red-m2-l1-validator.log`：mutant 下該 case 回 0 個 issue（FAIL）；`green-m2-l1-validator.log`：通過；`mutants-run4.log`：`rv-r4-first-period-only` 被 `TestBundleFeasibility` 擋下；L2 的 Python 比對也擋下它（seed 1002）。

### L1（Low）：LCM 恰為 365 天

- **發現**：mutant `rv-lcm-limit-strict`（`calendar.go` 的 `<=` → `<`）通過所有測試；D-050 的 L ≤ 365 天包含等號。
- **修正**：`TestCalendarRules`：period 恰為 365 天 valid、365 天＋1 tick（1/60000 分鐘）invalid（`/calendars/0/period`）；`TestBundleFeasibility`：73 天與 5 天（LCM 恰為 365 天）valid、73 天與 10 天（730 天）`CALENDAR_INVALID` 於 `/calendars`；`TestHandCases`：LCM(73 天, 5 天)、LCM(1 tick, 365 天) 等於上限，LCM(365 天, 7 tick) 超過。
- **擋下**：`red-m2-l1-validator.log`、`red-l1-calendar-lcm.log`：mutant 下三個新 case 失敗；`mutants-run4.log`：`rv-lcm-limit-strict` 被 `TestHandCases` 擋下；L2 的 Python 比對也擋下（seed 17）。

### L2（Low）：ADR-026 D-050 原訂的驗證沒有做，也沒有列為偏離

ADR-026 D-050 的「SimPy」段原訂三件事：(1) Python 獨立實作 R1–R4、next_fit 與 X_end 並與 Go 在隨機輸入上比對；(2) 以 SimPy 跑兩個反例與 G-D1（示意）；(3) 對隨機的 valid 模型，確認 SimPy 與 Go 每個 L 都有授予。

- **(1) 做了 R2、R3、R4、next_fit、X_end（與 TTRmax）**：`internal/simulation/engine/calendar_rules_export_test.go` 的 `TestCalendarRulesExport` 產生 5,000 個隨機的產線模型（2–4 個 resource、capacity 1–3；1–3 個 calendar，多數 period 為幾分鐘的整數、1–3 段 shift，五分之一為以天計的 period（1、5、7、10、73、146、365 天），label、`shift_capacity`、open／closed 例外；1–3 個 process 需要 1–3 個 resource，部分 resource 會故障、不刻意滿足 R3），跑 `validate.IR`，valid 的再 `Compile`；設定 `GEMINUS_CALENDAR_REFERENCE_OUT` 時寫出每個模型的 IR、issue（code 與 path），以及每個 process 的 X_end、TTRmax 與從數個 tick 起的 next_fit（含例外、到 horizon）。`scripts/calendar_reference.py` 只讀 IR，依 D-052 的 S(t) 與 D-050 的文字重算（沒有 import engine，沒有用 validator、calendar package 或 leapfrog：對 S 的所有改變點暴力搜尋），比對所有 issue 與數值，並要求每條規則都被涵蓋到最低數量（R2、R3、R4 各至少 200 個、R4 衝突只在較大 period 之後至少 15 個、LCM 超過 365 天至少 100 個、恰為 365 天至少 50 個、next_fit 至少 10,000 次…；下限低於目前產生器的數量，產生器變弱就失敗）。`make simulation-reference-test` 執行兩者（Makefile）。結果：5,000 個模型、2,774 個 valid；R2 拒絕 738 個 bundle、R3 拒絕 1,447 個、R4 826 個（其中 20 個衝突只在較大的 period 之後）、LCM 超過 365 天 298 個、恰為 365 天 152 個；23,604 次 next_fit（20,703 次找到、2,901 次 horizon 前沒有）、X_end > 0 的 process 2,778 個、TTRmax 有上界（> 0）1,005 個、沒有上界 497 個，0 個差異。**能否擋下錯誤的規則**（`t049-r1-l2-mutants.py`，`l2-mutants.log`）：18 個 Go mutant（M2 的 `rv-r4-first-period-only`、L1 的 `rv-lcm-limit-strict`，以及 R2 的 bundle period、讀例外、關閉、LCM 無上限；R3 的兩條；R4 的關閉、讀例外、夥伴只看 1 unit、略過沒有 calendar 的故障 resource、R3 拒絕後仍檢查；X_end 只取最後一個 resource、取例外的開始；next_fit 跳過例外結束的 tick、open 例外當下班；TTRmax 取 triangular 的 mode）在 export 執行時以 overlay 套用，**18 個都被 Python 比對擋下**。
- **沒有做的部分（偏離）**：R1（`BLOCKING_CYCLE`，T-047 的 graph rule：沿 p>0 的 flow 經 decision／failure／rework 與 queue 找阻塞邊、以共用 resource 做 union-find 再找 cycle）需要另一個產生隨機圖形拓樸與有限 buffer 的產生器與一份 union-find 的實作，與本 harness（只有直線的產線）不合；(2) 兩個反例與 G-D1 都是「有 calendar 的 resource 與別的 resource 在同一 bundle」或「會故障又 0 < S < C」，現行 SimPy 腳本依設計拒絕這些模型（逐一取得 resource 的解讀不成立），要先擴充腳本的接受範圍；(3) 同樣受限於腳本接受的模型（有 calendar 的 resource 必須獨佔 bundle），而且需要另一套「每個 L 內都有授予」的觀測。三者都列為 T-037 scope 的後續（`planning/tasks.json` 的 T-037 scope 最後一項），在此列為偏離（ADR-026 D-050「SimPy」段）。

### Info

- **I1**：G-F6 的推導寫下一次故障在 38；Θ(26)＝18（[0,6)、[10,16)、[20,26) 各 6），目標 26 在 [30,36) 到 24、[40,42) 到 26：42。只改推導文字（38 與 42 都在 horizon 30 之後，沒有任何期望值改變），並註明是 review 第 1 輪的更正。
- **I2**：ADR-026 原訂的 G-R5 是「高 rate：tick 嚴格遞增」，fixture 的 G-R5 是另一個 case（rate 在 tick 中改變）。不改既有編號，以新的 **G-R9** 加入 ADR 的 case：rate 1000/s（validator 的上限，每 tick 一件），arrival 在 1、2、…、12 ms，SimPy 也逐值比對。**指定的「floor 會作用的手推 golden」做不到**：對任何 valid 的 deterministic 排程，每段 rate ≤ 每 tick 1 件，所以 Λ(t＋1 tick) − Λ(t) ≤ 1、Λ⁻¹(n＋1) − Λ⁻¹(n) ≥ 1 tick（精確），且第一件 Λ⁻¹(1) ≥ 1 tick；SimPy 以 Fraction 精確計算再 round half up，相差至少 1 的兩個值捨入後仍相差至少 1，所以 `max(·, last＋1)` 永遠不會改變結果。SimPy 的 mutant `simpy-deterministic-no-floor` 因此是等價的（在 harness 中標為 equivalent 並寫出這個推理；`mutants-run4.log`：EQUIVALENT）。Engine 的 floor 只在 binary64 的捨入誤差與 poisson 的抽樣時會作用，由 `TestReleaseRunVectors`（G-R-fast、G-R2）逐 bit 固定（mutant `release-no-floor` 被它擋下）。
- **I5**：D-081 (e) 補上後果：Θ 計的是 S > 0 的 tick，所以部分人力的班（0 < S < C）中 C 個故障時鐘都前進（D-051 不需要 unit 的身分；下班的 unit 也算閒置，可以停機，G-T5），只有 S＝0 時一起暫停。只改文字，D-081 仍為 PROPOSED。
- **D-084**（仍為 PROPOSED，建議仍為 A）：依 coordinator 的解讀改寫依據：之後 APPROVED 的 D-058（H-018）與 ELECTRONICS_LINE §5 已把 ADR-026 D-052 (i) 分開，generic 的積分在 `resource_states`（本任務已實作），Scheduled utilization 與 Overtime 的比例是 `metrics.domain` 的 pack 指標（T-051）；C 會與 D-058 的指標重複；留給 owner 的問題只剩：除了 contract 的 `$comment`，Result 實例本身是否必須帶分母標示（B）。B、C 都沒有實作。本報告的「需要 owner 決定」與「偏離」一併改寫。
- **Reviewer 的另外兩個存活的 Go mutant**（`rv-overtime-vs-available`：overtime 以 A 代替 S；`rv-sched-inuse-vs-available`：min(U, A) 代替 min(U, S)）是等價的：kernel 永遠維持 U＋D ≤ C（授予時 in_use＋units ≤ min(S, C − D)，故障只取閒置的 unit 或等待），A＝min(S, C − D)；S ≤ C − D 時 A＝S，兩式相同；否則 A＝C − D < S 且 U ≤ C − D＝A，max(U − A, 0)＝0＝max(U − S, 0)、min(U, A)＝U＝min(U, S)。Reviewer 沒有要求修正，這裡只記錄理由。

### Mutation（第 4 次，`mutants-run4.log`）

`t049-mutants.py` 在 t049-r1 的程式與測試資料上完整執行（與本輪的驗證同時）：**59 個 mutant 中 55 個被擋下、4 個 equivalent（原有的三個與 `simpy-deterministic-no-floor`）**，exit 0。新增的五個：`rv-table-period-in-model-time`（M1，`TestCalendarGoldens`）、`rv-r4-first-period-only`（M2，`TestBundleFeasibility`）、`rv-lcm-limit-strict`（L1，`TestHandCases`）、`simpy-table-model-time`（G-R8 不符）都被擋下，`simpy-deterministic-no-floor` 為 equivalent（I2 的推理）。第一個失敗的測試是 0.4.0 鎖的只剩 `release-extra-draw`（`TestCalendarObservationDigests`），它也被 `TestReleaseRunVectors` 與 G-R3 擋下（`checks/evidence/2026-10-25-T-049/release-vector-mutants.log`；`release.go` 本輪沒有改）。其他 54 個被擋下的 mutant，第一個失敗的都是手推 golden、檢查器、向量、validator 或 AI 的測試，或 SimPy 的比對。

### 實際執行（本輪，t049-r1 的 tree：534de30 的程式與測試資料）

| 指令 | Exit | 說明 |
|---|---:|---|
| `make simulation-reference-test PYTHON=…` | 0 | `simulation-reference-test.log`、`simpy-reference-report.json`：SimPy 確定性 34 個 case（T-049 的 17 個，含 G-R8、G-R9）逐值相同，140 個隨機比對都在容差內；`calendar_reference.py`：上述 L2 的數量，0 個差異 |
| `make backend-test GOFLAGS=-count=1` | 0 | `backend-test.log`：gofmt、go vet、64 個 package ok，沒有 `(cached)`；`GEMINUS_FULL_DIFFERENTIAL=1`；engine 230 s、des 15 s、check-architecture 217 s；DB-backed 測試 skip |
| `make backend-race GOFLAGS=-count=1` | 0 | `backend-race.log`：完整的 race（不只 internal/simulation、internal/results、internal/model/calendar），64 個 package ok，沒有 `(cached)`；engine 605 s、des 160 s |
| `make architecture-check` | 0 | `architecture-check.log`：76 packages、17 rules、16 build configurations |
| `make ai-eval` | 0 | `ai-eval.log`：`TestFakeProviderEval`、`TestReviewEval` PASS；`checks/ai-evals/*.json` 沒有改變 |
| `make backend-numeric` | 0 | `backend-numeric.log`：六個 target 沒有 FMA 與平台 math call，GOAMD64=v3 的數值測試通過 |
| `go test -v`（重點測試） | 0 | `engine-tests-verbose.log`：`TestCalendarRulesExport`（5,000 個模型）、differential、中性班表與排程、隨機檢查器的計數與上一輪相同 |
| Fuzz（以次數為準） | 0 | `fuzz-kernel-count.log`（FuzzKernel 500,000 execs）、`fuzz-run-count.log`（FuzzRun 200,000）、`fuzz-calendars-count.log`（FuzzCalendars 20,000）：seed 含 G-R8、G-R9 |
| `make spec-check PYTHON=…` | 0 | `spec-check.log`（本節寫好之後）：`PASS_TS_CONTRACTS_FRESH`（15 檔、106 types）、`PASS_INDEPENDENT_HASHES`、`PASS_DB_TESTS_FRESH`、`PASS_ENGINE_REFERENCE_VECTORS`（release_steps 38）；`docs-check.json` 在 534de30 重產（多一個相對連結），`decisions-check.json`、`planning-check.json` 沒有改變（D-081、D-082、D-084 仍為 proposed） |
| Mutation harness 第 4 次（完整） | 0 | `mutants-run4.log`：59 個 mutant 中 55 個被擋下、4 個 equivalent（原有的三個與 `simpy-deterministic-no-floor`） |
| L2 的 mutant（`t049-r1-l2-mutants.py`） | 0 | `l2-mutants.log`：18/18 被 Python 比對擋下 |
| 新 case 對 reviewer 的 mutant（red） | 1 | `red-m2-l1-validator.log`、`red-l1-calendar-lcm.log`：每個 `go test` 都 exit 1；`green-m2-l1-validator.log`：exit 0 |
| SimPy 改腳本前（red） | 2 | `red-simpy-gr8.log`：G-R8 的四個差異；`green-simpy-gr8.log`：exit 0 |
| G-R8、G-R9 的第一次執行 | 1 | `first-engine-run-gr8-gr9.log`：兩個 case 的 engine 與 Result 子測試 PASS；只有尚未更新的數量與鎖失敗 |

### NOT RUN（本輪）

- `make integration-test` 與所有 DB-backed 測試：本輪沒有 PostgreSQL，依指示沒有執行（只在 `dt_test_*` 上執行，由 coordinator 進行）；本輪只改測試、fixture、SimPy 腳本、新的 Python reference 與文件，沒有改經過 DB 的程式（`db-tests-skipped` 的清單與上一輪相同，沒有重產）。Coordinator 在 b4ef811 的整合執行見上一節。
- `make frontend-test`：沒有 TS、schema 或 `apps/web` 的變更（`PASS_TS_CONTRACTS_FRESH`）。
- `make e2e-test`、`make benchmark-test`、`make ai-eval-live`、CI。
- L2 的 R1、ADR-026 D-050 的 SimPy (2)、(3)：見 L2，列為 T-037 的後續。
- I2 的「floor 會作用的 deterministic 手推 golden」：見 I2，對 valid 的模型不可能。

### 需要 owner 決定（本輪的變化）

- D-081 (e)：補上部分人力時所有故障時鐘都前進的後果（文字）。
- D-082 (b)：A 的證據改為 G-R8（G-R7 分不出 A 與 B）。
- D-084：A 的依據改為 D-058 與 ELECTRONICS_LINE §5；留給 owner 的問題是 Result 實例是否必須帶分母標示（B）。
- 其他不變；五項仍是 PROPOSED，T-049 仍以它們為 gate。

### 殘餘風險（本輪的變化）

- **Python 比對只涵蓋直線產線**：`calendar_reference.py` 的隨機模型都是 start → p0 → … → end，沒有 decision、rework、有限 buffer；R2–R4 只看 bundle，與拓樸無關，但 R1 沒有獨立實作（T-037）。
- **SimPy 的投入表量化**：腳本以 Fraction 精確值 round half up，engine 以 binary64 的乘積再 round；兩者只在某個值與半 tick 的距離小於 binary64 的捨入時可能不同，G-R8 的值都遠離半 tick。
- **Mutation 仍是抽樣**：本輪 reviewer 的 29 個 mutant 中有 5 個存活，3 個已補測試、2 個等價；harness 沒有涵蓋的規則仍可能只有鎖在保護。

### Coordinator 的執行（第 1 輪，96f96de）

Coordinator 把 `t049-r1` 的五個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`，接在 T-029 的 5d96749 之後：2fa48e6 → dacc73a、9101a72 → e0faf02、b6781e3 → ba8a0f1、534de30 → aa60c9d、82d62d4 → 96f96de。唯一的衝突是產生的 `checks/docs-check.json`：取本輪的版本，由 spec-check 重新產生（Markdown 檔數 102 → 103）。`planning/decisions.json` 自動合併，D-081–D-085 的新文字與 T-029 的 D-088–D-090 都在，順序依 ID。

之後在同一個 tree 上（`coordinator-environment.txt`：HEAD 96f96de、沒有其他變更、go1.27.3、Node 22.22.0、PostgreSQL 16.15）執行以下指令，全部 exit 0：

| 指令 | Exit | 說明 |
|---|---:|---|
| `make spec-check` | 0 | 重新產生 `checks/docs-check.json` |
| `make backend-test` | 0 | |
| `make architecture-check` | 0 | |
| `make ai-eval` | 0 | |
| `make frontend-test` | 0 | Node 22.22.0；Node 24 由 CI 執行 |
| `make simulation-reference-test` | 0 | SimPy 140/140 個隨機比對；確定性 case 含 G-R8、G-R9；`calendar_reference.py` 對 Go 的 R2、R3、R4、next_fit、X_end、TTRmax 為 0 個差異 |
| `make backend-numeric` | 0 | |
| `make backend-race` | 0 | |
| `make integration-test`（新的 `dt_test_t049r1`） | 0 | 32 個 package ok，沒有 FAIL |

本輪沒有新的 DB 測試或 DB mutant，整合測試是為了確認整合後的 tree 沒有 regression。Log 與 exit code 在 `checks/evidence/2026-10-25-T-049-r1/coordinator-*`。D-081–D-085 仍是 PROPOSED，T-049 在 tasks.json 仍為 `not_started`。

## Review 第 2 輪（c1c027c）：ACCEPT

獨立 reviewer（H-003）在 c1c027c 重審：第 1 輪的 M1、M2、L1、L2 都已解決，Info 也都已處理；本輪沒有新的 High、Medium 或 Low。
- 三個第 1 輪存活的 Go mutant（`rv-table-period-in-model-time`、`rv-r4-first-period-only`、`rv-lcm-limit-strict`）現在都被擋下。
- 新的 SimPy mutant `simpy-table-unquantized` 被 G-R8 擋下。
- G-R8、G-R9 的推導由 reviewer 手推確認。
- I2 的等價論證成立：validator 讓 rate ≤ 每 tick 1 件，Λ⁻¹ 相鄰至少差 1 tick，round half up 單調，第一件也至少在 1 tick。
- `calendar_reference.py` 確認與 engine 獨立，並擋下 M2、L1 的 mutant。
- 本輪沒有改任何非測試的 Go 程式碼；0.4.0 的鎖只新增 G-R8、G-R9 兩筆。

三個 Info：
- I-a：reviewer 環境的 `GOTOOLCHAIN=local` 指向 go1.24.7，改用 go1.27.3 執行；coordinator 的環境本來就是 go1.27.3（`coordinator-environment.txt`），不是 T-049 的缺陷。
- I-b：隨機比對沒有涵蓋半 tick 附近的 duration 捨入，Python 與 Go 對 empirical TTR 的上界讀法不同（產生器不產生 empirical，目前沒有差異）；已加入 T-037 的 scope 作為擴充獨立比對時的項目。
- I-c：`r4_past_larger_period` 的實際值 20 接近下限 15；seed 固定，結果穩定。

Reviewer 同意 D-081–D-085 的建議，D-084 也同意依 D-058 細化後的 A，只剩 B（Result 實例中的分母標示）要 owner 決定。D-081–D-085 仍是 PROPOSED，它們的 gate 是 T-049 驗收前，所以 T-049 記為 in_progress（`planning/tasks.json` 的 completion_evidence），owner 批准後才標為 done。
