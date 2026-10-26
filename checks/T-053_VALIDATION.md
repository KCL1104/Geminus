# T-053 驗證報告：Live AI 開啟（H-018）

**日期：2026-10-26｜狀態：實作完成，待獨立 review（H-003）；`planning/tasks.json` 的狀態未改（仍為 not_started），由 coordinator／reviewer 決定。｜需求：REQ-008、REQ-016、REQ-019、REQ-020｜Gate：D-018（APPROVED）；本任務提出的 D-110–D-115 是 **PROPOSED**，`approved_by`／`approved_at` 為 null，T-053 依建議實作並以它們為 gate（tasks.json 的 `decision_gates` 加入 D-110–D-115）。｜驗收測試：TEST-027（audit／telemetry privacy 的 T-053 部分）；TEST-024（analyst grounding）不在本次範圍：analyst 的 live 使用依 D-112 不開，analyst job 是 T-030。｜原本的 T-053 build 中 live AI 無法開啟：H-018 的 record 缺 owner 對資料保留安排的確認（D-113，`retention_unconfirmed`）。Owner 的確認（H-026）已在 `t053-ret` 記入，record 已完整，見下節。Live smoke 與 live eval 都 NOT RUN；DB 測試與 `make integration-test` 為 NOT RUN（見「NOT RUN」）。**

## Owner 的確認（H-026）與 reservation ID 的 domain（追加，`t053-ret` branch）

T-053 的 commit 整合到 main（79c9423）之後，在 `t053-ret`（從 79c9423 開出，沒有 push）追加兩項可分開 review 的修改。以下的「實際執行結果」等章節仍是追加之前的紀錄；追加的驗證在本節。

**(1) H-026：資料保留安排的確認。** Owner 回答 D-113 的問題（2026-10-26，coordinator 記錄為 H-026）：兩個 provider organization（`GEMINUS_ANTHROPIC_API_KEY_A`／`_B` 背後）都是預設的 standard retention，沒有 ZDR，兩者相同。
- `internal/config/aiapproval`：H-018 record 的 `Retention` 寫入 `Arrangement` `standard`、`By` KCL1104 (repository owner)、`At` 2026-10-26、`Source` H-026。形狀沒有改（一個 arrangement 代表兩個 organization）。`Confirmation.Given` 另要求 `At` 是 YYYY-MM-DD 的日曆日期（`SpendLimits` 也適用）；arrangement 只接受 `standard` 與 `zero_data_retention`，所以「只有一個 organization 開 ZDR」這種記法只能寫成不認得的 arrangement，因此被拒。
- 先寫的測試（`h026-red.log`，exit 1）：修改前，既有的 `TestTheH018Record` 與 `TestThisBuildKeepsLiveOff` 通過，證明 H-018 以 `retention_unconfirmed` 拒絕 live（要求 (1)）。新的 `TestH026TurnsLiveOn` 失敗（record 的 retention 是空的）。新的 `TestAnAlteredRetentionRefuses` 中，6 個日期格式的 subtest 失敗（`26/10/2026`、`2026/10/26`、帶時間、文字、`2026-02-30`、前面有空格），其餘 8 個通過。修改後（`h026-green.log`，exit 0）全部通過：
  - `TestH026TurnsLiveOn`（要求 (2)）：以 `t.Setenv` 設假 key、`AI_MODEL` 等於 record 的 model、`GEMINUS_AI_LIVE=on`，經 `os.Getenv` 讀設定時 live 開啟，adapter 接受這個設定，但沒有送出任何 request。沒有開關時 `switch_off`，也不讀 key。從頭到尾沒有向 `ANTHROPIC_API_KEY` 要值，而且這個 getenv 對 `ANTHROPIC_*` 一律不讀。
  - `TestAnAlteredRetentionRefuses`（要求 (3)）的 14 種改動：一個 organization 開 ZDR、兩種 arrangement 分開寫、`zdr` 縮寫、`Standard` 大寫、沒有 arrangement、沒有 source、沒有確認者、沒有日期，以及上面 6 種日期格式。每一種都是 `retention_unconfirmed`、不產生 token：開關 on 時拒絕啟動，開關 off 時不讀 key。
- 以前依賴「H-018 不完整」的測試改為新的狀態：
  - `TestTheH018Record`（golden 改為 H-026 的 retention、`Gap` 為空、會產生 token）
  - `TestRecorded`（改用本測試自己的 record）
  - `TestAnyMismatchKeepsLiveOff`（case 改名為 H-026 之前的 record）
  - `TestTheLiveEvalRunsOnlyWhenLiveIsOn`（H-026 之前的 record 被拒；build 的 record 在測試的 in-process backend 上跑）
  - `internal/config` 的 `TestThisBuildsRecord`（原 `TestThisBuildKeepsLiveOff`）
  - `internal/aievals` 的 `TestMainRefusesWhileLiveIsOff`（H-018 沒有開關時為 `switch_off`；加上其他 model、少一把 key、同一把 key 兩次）
  - `cmd/worker` 的 `TestTheProviderIsTheConfigured`（開關 on 時 live 開啟，但 worker 沒有 spend ledger 就拒絕建立 live client）

  這些測試都不呼叫 live client：沒有任何測試對 production URL 送出 request。
- 文件：D-113 記下 owner 的回答（docs/DECISIONS_AND_TBD.md、planning/decisions.json），D-113 本身仍為 PROPOSED。改了的還有 SECURITY §7、AI_SPEC（開頭、§2、§10）、ARCHITECTURE、Makefile 的說明，以及 config、worker、adapter 的註解：原本寫著「H-018 缺 retention 確認、live 無法開啟」的句子改為現況。human decisions 表的 H-026 列沒有加，由 coordinator 在 main 加入。
- **之後的行為改變**：這個 build 在部署設定 `AI_PROVIDER=anthropic`、`AI_POLICY_APPROVAL=H-018`、`AI_MODEL=claude-opus-5-5`、兩把 key 與 `GEMINUS_AI_LIVE=on` 時會開啟 live AI。worker 經 spend ledger 計量；`make ai-eval-live` 以每次 run 自己的上限計量。

**(2) Migration 0016 的 reservation ID 改用 D-001 domain。** Coordinator 的 `make integration-test` 中，`db/tests/constraints.sql:361` 失敗：「ID columns without their D-001 domain: ai_spend_reservations.id=text want ?」。0016 沒有 push 也沒有部署過，所以可以改。
- `migrations/0016_ai_spend.up.sql`：新增 `CREATE DOMAIN twin.asr_id AS text CHECK (VALUE ~ '^asr_[0-7][0-9A-HJKMNP-TV-Z]{25}$')`，寫法同 0001。另外比照 0002、0004 對每個 domain 的做法 `GRANT USAGE ON TYPE` 給 `twin_api`、`twin_worker`、`twin_provisioner`。`ai_spend_reservations.id` 改為 `twin.asr_id NOT NULL`。`0016_ai_spend.down.sql` 在 drop table 之後 `DROP DOMAIN twin.asr_id`。
- `db/tests/templates/constraints.sql.tmpl` 加入 `('ai_spend_reservations','asr_id')`，以 `scripts/render_db_tests.py` 重產，只有 `db/tests/constraints.sql` 改變。0016 中其他會被這個測試檢查的欄位已經用了對應的 domain：`organization_id`（org_id）、`ai_proposal_id`（aip_id）、`ai_analysis_id`（ana_id）、`import_id`（imp_id）、`request_id`（req_id）。`ai_spend_periods` 沒有 ID 欄位。沒有其他欄位存 reservation ID（`reservation_id` 只出現在 audit 的 JSON metadata，`internal/audit` 已以 `idOf("asr")` 檢查）。
- D-001 prefix 登記處的檢查：
  - `internal/ids` 沒有 prefix 清單（`New(prefix)`），不需要改。
  - `schemas/common.schema.json` 只列出 HTTP payload 中出現的 ID（`ResourceID` 是 audit 的 `target_id`），而 reservation 不是任何 API 的 payload，也不是 audit target（AuditEvent 的 `metadata` 是自由的 object），所以沒有加。加了會改 contract，還要重產 TS。
  - 0001 的 `twin.public_id` 已部署，而且 reservation 不是 audit target，所以沒有改。
  - `internal/ai/aicontext/sanitize.go` 的 server ID redaction 清單沒有加 `asr`：reservation ID 不會進入 AI context，也不能經任何 API 指定。
  - D-001 的文字不列舉 prefix。D-111（PROPOSED）加了一句「ID 為 D-001 的 `asr_` + ULID，domain `twin.asr_id`」（docs/DECISIONS_AND_TBD.md、planning/decisions.json），API_DATABASE §8 也同步。
- 沒有 PostgreSQL，所以 SQL 都沒有執行：這次的修改、`constraints.sql` 與 `ai_spend.sql`（含 0016 的 down／up）都 NOT RUN，由 coordinator 重跑 db-test 與 integration-test。`ai_spend.sql` 中不合格 ID 的 case（`asr_not-an-id`）預期仍是 23514，因為 domain 的 CHECK 也回 23514。

**這次的驗證**（log 在 `checks/evidence/2026-10-26-T-053/h026-*.log`）：

| 指令 | Exit |
|---|---:|
| 先寫的測試，修改前（`h026-red.log`） | 1 |
| 同一組測試，修改後（`h026-green.log`） | 0 |
| `go test -count=1 ./internal/config/... ./internal/ai/... ./internal/aievals/... ./cmd/ai-eval-live/... ./cmd/worker/... ./internal/storage/aispend/...`（`h026-go-test.log`） | 0 |
| `go vet ./internal/config/... ./internal/ai/... ./internal/aievals/... ./cmd/... ./internal/storage/... ./internal/proposals/...`（`h026-go-vet.log`） | 0 |
| `make spec-check`（`h026-spec-check.log`；含 `PASS_DB_TESTS_FRESH`） | 0 |
| `make architecture-check`（`h026-architecture-check.log`） | 0（86 packages、23 rules） |
| `make ai-smoke-live`，本 session 的環境：沒有 `AI_PROVIDER`，也沒有 key 的變數（只查了變數名稱）（`h026-ai-smoke-live.log`） | 2（`AI_PROVIDER is not anthropic`，沒有呼叫；`checks/ai-evals` 只有兩個 fake 檔） |

這次沒有執行 `refusals.sh` 中「H-018＋假 key＋開關 on」的 case：H-026 之後那組設定會開啟 live，`ai-eval-live` 會以假 key 向 Anthropic 的 production API 送出 request，違反「不做 live 呼叫」的規則。同樣的拒絕條件已由 in-process 的 factory 測試（`TestMainRefusesWhileLiveIsOff`：沒有建立 backend）涵蓋。Live smoke 與 live eval 仍是 NOT RUN。

## 安全規則的遵守

- **沒有任何 live 呼叫**：沒有從程式、測試、curl 或任何工具呼叫 Anthropic 或其他 AI provider。所有測試只用 deterministic fake provider、process 內的 billable test backend，或 `httptest` 的本機 Messages API server（`internal/ai/provider/anthropic` 的測試以 `NewForTest` 指向本機 server；`ProductionURL` 只被比對、不被呼叫）。`make ai-eval-live`／`make ai-smoke-live` 只記錄了它們拒絕的路徑（exit 2，沒有建立 backend）。
- **沒有讀取 key 的值**：沒有讀取、印出、量測、hash、複製或記錄 `GEMINUS_ANTHROPIC_API_KEY_A`、`GEMINUS_ANTHROPIC_API_KEY_B` 或 `ANTHROPIC_API_KEY` 的值。程式只在 live 的條件全部成立時於 runtime 以設定讀 `_A`／`_B`（`config.loadKeys`），從不讀 `ANTHROPIC_API_KEY`（`TestLiveTurnsOn` 等以 recorder 證明 `ANTHROPIC_API_KEY` 沒有被讀）。測試的 key 都是 `t.Setenv`／map 中明顯的假字串（`sk-ant-test-primary-0000000000` 等）；`refusals.sh` 用 `env -i` 的乾淨環境與假字串執行 make，不繼承 session 的環境。
- Repository、log、fixture 中沒有 secret、原始 prompt、provider response、chain-of-thought 或客戶資料；live report 的格式只有狀態、code、hash、token 與成本（`TestRunScoresTheAnswers` 以 sentinel 檢查 report 不含回答的文字與 intent；`TestTheReportIsApart` 檢查 report 只能寫到 `live-*.json`）。

## Commit

`t053` branch（從 44a31bf 開出，origin `claude/lucid-meitner-vq1awo`；沒有 push、沒有 PR）：

| Commit | 內容 |
|---|---|
| 7e3e9aa | `internal/ai/spend`（牌價、nano-USD、上限、UTC 期間）；H-018 的 approval record、`Gap` 的 typed reason、`Approval` token（`internal/config/aiapproval`）；`config.LoadAI` 的 live 條件（record → model → 價格 → 開關、兩把 key、上限）；`provider.Billable`／`Meter`／`NewMetered`、預留／結算／釋放、`MemoryMeter`；anthropic adapter 的 `Quote`、未回答 attempt 的計數、沒有預留與未核准 role 的拒絕；先寫的測試為紅（`red-spend.log`、`red-provider-meter.log`、`red-anthropic-spend.log`、`red-aiapproval.log`） |
| 5a202cc | Migration 0016（ledger、RLS、grants、trigger）與 DB 測試 `db/tests/ai_spend.sql`；`internal/storage/aispend`（PostgreSQL 的 meter）與其 DB 測試；worker 以 ledger 計量 live client；generator 的 attribution、log、audit、錯誤對應（先為紅：`red-generator-mapping.log`）；audit catalogue、log key、metric；`internal/aievals`、`cmd/ai-eval-live`、Makefile 的 `ai-eval-live`／`ai-smoke-live`；architecture rules；拒絕路徑（`ai-eval-live-refusals.log`） |
| 94e5e75 | D-110–D-115（PROPOSED）、tasks.json 的 gate；SECURITY §7（保留規則的來源與原文）、AI_SPEC §10、ARCHITECTURE、API_DATABASE §8–§9、testdata/ai-evals/README；重產的 `checks/*-check.json` |
| c39624c | Mutation 第 1 次後補強的測試（U6、U24、D8，見「Mutation」）與 `anthropic.ErrNotApproved`；mutation runner `mutants.py`、`mutants-run1.log`、`mutants-run2.log` |
| （最後一個） | 本報告；最後的 tree 上的驗證 log（`backend-test.log`、`backend-race.log`、`architecture-check.log`、`ai-eval.log`、`go-mod-verify.log`、`govulncheck.log`、`db-tests-skipped.log`、`mutants-run3.log`、`spec-check-final.log`、`exit-codes.txt`）、先前一輪改名保留的 log（`*-94e5e75.log`）、執行用的 script（`verify.sh`、`final.sh`、`refusals.sh`）；重產的 `checks/docs-check.json` |

## 稽核：需求 → 實作 → 測試 → 證據

| 需求 | 實作 | 測試 | 證據 |
|---|---|---|---|
| 1a. 每日與每月上限（H-018：兩個 provider organization 合計每月 USD 100；每日上限；T-053：每個 organization 每日上限） | `internal/ai/spend`：`Limits{Month, Day, OrganizationDay}`、`DefaultLimits`（日 USD 10、organization 日 USD 5，D-110 建議）、UTC 的 `Day`／`Month`；`config.loadLimits`：月上限預設＝record 的預算、`AI_MONTHLY_LIMIT_USD` 只能更低、organization 日 ≤ 日 ≤ 月 | `TestLimits`、`TestPeriods`、`TestSpendLimitsAreChecked`（超過預算、日 > 月、organization 日 > 日、非法金額都是 typed error） | `backend-test.log`、mutant U37 |
| 1b. 以美元計：USD↔token 的換算 | 牌價固定在程式（`claude-opus-5-5`：輸入 4、cache 寫入 5／8、cache 讀取 0.20、輸出 20 USD／MTok，2026-10-26 取自 https://platform.claude.com/docs/en/about-claude/pricing）；`Cost`（cache 寫入以較高的 1h 價）、`AttemptBound`；沒有價格的 model 不能預留 | `TestThePinnedPrice`、`TestCost`、`TestAttemptBound`、`TestParseAndFormatUSD`、`TestAnyMismatchKeepsLiveOff`（`model_unpriced`） | mutant U1–U3、U33；D-110（PROPOSED） |
| 1c. 超過即拒絕，typed error，不呼叫 provider | `provider.Client.call`：acquire 之後、`Complete` 之前預留；被拒為 `quota_exceeded`（`Error.Limit`：`organization_day`／`day`／`month`／`run`）或 `spend_unavailable`；generator 的 job failure `AI_QUOTA_EXCEEDED`（不重試）／`AI_SPEND_UNAVAILABLE`（可重試）；HTTP `RATE_LIMITED`／`SERVICE_UNAVAILABLE` | `TestARefusedReservationRefusesTheCall`（backend 沒有被呼叫、沒有結算）、`TestNothingIsReservedForARefusedCall`、`TestProviderFailures`、`TestFailureErrors`、`TestASpendLimitRefusesTheProposal`（DB） | mutant U5、U6、U14–U17、U43、U44 |
| 1d. 預留／結算／釋放 | `provider.reserve`（(1+MaxRetries)×attempt 上限）、`settlement`（usage；未回答的 attempt 以上限計；不可能被計費為 0）、`settle`（不受呼叫者 context 影響，10 s；失敗時預留留著，`spend_unsettled`）；adapter 的 `tallying` transport 與 `tallyBody` | `TestTheReservationBoundsTheCall`、`TestSettlement`、`TestAFailedSettlementKeepsTheReservation`、`TestMemoryMeter`、`TestQuote`、`TestSpend`、`TestAttemptsWithoutAnAnswerCount`（斷線、body 讀不完、attempt 逾時） | mutant U2、U4、U7–U13、U20、U21；D-111（PROPOSED） |
| 1e. 計數持久化、跨 process 一致 | Migration 0016：`twin.ai_spend_periods`（部署全體的日、月）、`twin.ai_spend_days`（tenant 的日，RLS）、`twin.ai_spend_reservations`（RLS、composite FK、只能結算一次、沒有 DELETE）、deferred trigger 檢查 organization 日＝其預留的總和；`internal/storage/aispend.Meter`：同一 transaction 內以月 → 日 → organization 日的鎖順序做條件 UPDATE | `TestReserveAndSettle`、`TestLimitsRefuse`、`TestConcurrentReservationsNeverOvershoot`（兩個 pool 模擬兩個 process）、`TestSettleKeepsItsDayAndOrganization`、`TestFailClosed`（DB）；`db/tests/ai_spend.sql`（RLS、grants、trigger、shape、結算一次、down／up 再跑） | DB 測試只編譯與 vet，**NOT RUN**；mutant D1–D7、D9 只確認可 build（NOT RUN） |
| 1f. 計入 audit，不記 prompt 或回應 | Audit `ai_spend.reserve`／`settle`／`refuse`（worker；target 為 proposal／analysis／import；只有金額、token、角色、model label、credential label，沒有部署全體的總數）；`proposal.generate` 的 `cost_nano_usd`、`spend_reservation_id`、`spend_unsettled` | `TestEveryActionFits`、`TestRefused`（使用者記帳、simulation target、未知 limit、部署總數、其他 tenant、負數、文字金額、key 當 model、未知 role、別的 ID、自由文字）、`TestTheCatalogueMatchesTheCode` | TEST-027；mutant U45、D7（NOT RUN） |
| 1g. 觀測 | Metric `geminus.ai.spend`、`geminus.ai.spend.refusals`（`geminus.ai.limit`）；ID 形狀的 model 名稱 → `[redacted]`；log key 與 WARN 行 | `TestSpend`（telemetry）、`TestAttributesAreFiltered`、`TestTheSpendLines` | mutant U46 |
| 2a. 記錄 H-018 的批准 | `aiapproval.h018`：ID H-018、provider anthropic、model `claude-opus-5-5`、資料政策（synthetic 與使用者輸入的文字、只服務 modeler／scenario、log 不存原始文字）、每月 USD 100、credential `org-a`／`org-b`、approver「KCL1104 (repository owner)」、2026-10-20、來源（decisions.json 的 human_decisions 與 decision page）；**Console spend limit：KCL1104、2026-10-26、H-025**；`Retention` 空白 | `TestTheH018Record`（逐欄 golden）、`TestNothingElseIsRecorded`、`TestGapReasons`、`TestRecorded` | `red-aiapproval.log`；mutant U25–U30 |
| 2b. 只有這筆紀錄＋兩把 credential＋明確的開關能開啟 live；不一致保持關閉並給 typed reason；預設 fake | `config.LoadAI`：record 的 `Gap` → `AI_MODEL` 等於 record 的 model → 有價格 → `GEMINUS_AI_LIVE=on`；條件不合而開關 on 時拒絕啟動（`GEMINUS_AI_LIVE:retention_unconfirmed`、`AI_MODEL:model_mismatch`／`required`、`GEMINUS_ANTHROPIC_API_KEY_B:required` 等）；開關不是 on 時 `LiveOff` 為原因且不讀 key；`anthropic.New` 只接受 record 的 model 與 credential label（先檢查 approval：`ErrNotApproved`）；worker 啟動時記 live on／off 與原因 | `TestLiveTurnsOn`、`TestTheSwitchIsRequired`、`TestAnyMismatchKeepsLiveOff`（record 的每一欄、其他 model、沒有價格）、`TestBothCredentialsAreRequired`（少一把、B＝A）、`TestTheAdapterTakesOnlyTheApproval`、`TestThisBuildKeepsLiveOff`、`TestLiveAIStaysOff`、`TestNewRefuses`、`TestTheProviderIsTheConfigured`（worker） | mutant U22–U24、U31–U36；D-115（PROPOSED） |
| 2c. 只服務 H-018 資料政策內的角色 | `aiapproval.liveRoles`（modeler、scenario）；adapter 對其他 role 在 I/O 前回 `policy_not_approved` | `TestOnlyTheApprovedRoles`、`TestGapReasons`（record 列出其他 role → `policy_unsupported`） | mutant U19、U27、U30；D-112（PROPOSED） |
| 3. Anthropic 的保留／ZDR 規則：官方來源、日期、原文 | [SECURITY §7](../docs/SECURITY.md#7-ai-data-governance)（見下方「資料保留與 ZDR」）；ZDR 的開啟是 owner 才能確認的帳號設定，record 的 `Retention` 空白，live 保持關閉 | `TestGapReasons`（`retention_unconfirmed`）、`TestThisBuildKeepsLiveOff`、`TestTheLiveEvalRunsOnlyWhenLiveIsOn`（本 build 的 record 被拒） | mutant U25、U34；D-113（PROPOSED） |
| 4a. `make ai-eval-live`：拒絕，除非 approval＋credentials＋開關 | `cmd/ai-eval-live` → `aievals.Main`：`config.LoadAI` 與 worker 相同；live 關閉 → exit 2、不建立 backend、不寫檔 | `TestMainRefusesWhileLiveIsOff`、`TestTheLiveEvalRunsOnlyWhenLiveIsOn` | `ai-eval-live-refusals.log`（五種情形都是 exit 2，`checks/ai-evals` 只有兩個 fake 檔）；mutant U38、U42 |
| 4b. 寫 `checks/ai-evals/live-*.json`，與 fake 分開；記 model、日期、token、成本 | `aievals.ReportPath`、`WriteReport`（檔名須符合 `live-[smoke-]<date>-<model>.json`，否則 `ErrNotALiveReport`）；report：model、served model、日期、approval、template 版本、token、成本（USD 與 nano-USD）、上限、每個 case 的狀態、code、risk、patch hash；gate；exit 0／1／2／3 | `TestRunScoresTheAnswers`、`TestTheCapStopsTheRun`、`TestTheReportIsApart`、`TestTheLiveEvalOverHTTP`（adapter 對本機 Messages API server 跑完整的 eval、寫 report） | mutant U39–U41；D-114（PROPOSED） |
| 4c. Live 只在 worker 與 live eval 的 command | Architecture rules `live-ai-only-in-the-worker`、`live-eval-only-in-its-command` | `scripts/check-architecture` 的 probe（api、handler、ledger 連結 adapter；worker、service 連結 live eval） | `architecture-check.log` |
| 5. Live smoke 的指令準備好，NOT RUN | `make ai-smoke-live`（MOD-002，USD 2 上限），指令見下方「準備好的 live 指令」 | 拒絕路徑同 4a | NOT RUN |

## 實際執行結果

在 worktree 中執行（Go 1.27.3、Python venv `/home/user/Geminus/.venv`、與其他 agent session 共用的機器；沒有 PostgreSQL）。逐條輸出在 `checks/evidence/2026-10-26-T-053/`，每個 log 的最後一行是 exit code；最後的 tree 上的一輪由 `final.sh` 依序執行（`verify.sh` 記錄 Go 版本與 exit code），摘要在 `exit-codes.txt`。下表上半部在最後的 tree（c39624c 的程式與測試；最後一個 commit 只加本報告、evidence 與重產的 `checks/docs-check.json`）上執行，Go 測試以 `GOFLAGS=-count=1` 執行，不用 cache。

| 指令 | Exit | 說明 |
|---|---:|---|
| `make backend-test GOFLAGS=-count=1` | 0 | `backend-test.log`：gofmt、go vet、74 個 package ok，沒有 `(cached)`；DB-backed 測試 skip（NOT RUN） |
| `make backend-race GOFLAGS=-count=1` | 0 | `backend-race.log`：74 個 package ok，沒有 `(cached)`（engine 590 s、check-architecture 399 s） |
| `make architecture-check` | 0 | `architecture-check.log`：86 packages、23 rules、16 build configurations |
| `make ai-eval` | 0 | `ai-eval.log`：`TestFakeProviderEval`、`TestReviewEval` PASS；`checks/ai-evals/fake-provider.json`、`fake-review.json` 沒有改變（working tree 沒有 diff） |
| `go mod verify` | 0 | `go-mod-verify.log`；`go.mod`／`go.sum` 沒有改變（沒有新 dependency） |
| `GOTOOLCHAIN=go1.27.3 go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./cmd/... ./internal/... ./schemas/... ./scripts/...` | 0 | `govulncheck.log`：No vulnerabilities found |
| `go test -count=1 -v -run '<7 個 DB 測試>' ./internal/storage/aispend/ ./internal/proposals/generator/` | 0 | `db-tests-skipped.log`：7 個 DB 測試都是 SKIP（沒有 `GEMINUS_TEST_DATABASE_URL`），即 NOT RUN，不是 PASS |
| Mutation 第 3 次（c39624c） | 0 | `mutants-run3.log`：47 killed、8 個 DB mutant 只 build；還原檢查 clean |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `spec-check-final.log`（本報告寫好之後執行；重產的 `checks/docs-check.json` 與本報告一起 commit） |
| `make ai-eval-live`／`make ai-smoke-live` 的拒絕路徑（`refusals.sh`：`env -i` 的乾淨環境、假 key） | 2（五個都是） | `ai-eval-live-refusals.log`（5a202cc 的 tree）：乾淨環境（`AI_PROVIDER is not anthropic`）；H-018＋假 key、沒有開關（`retention_unconfirmed`）；H-018＋假 key＋`GEMINUS_AI_LIVE=on`（`GEMINUS_AI_LIVE:retention_unconfirmed`）；其他 model（同上：record 先被檢查）；之後 `checks/ai-evals` 只有兩個 fake 檔 |

先前的 tree 上的執行（保留作為紀錄）：

| 指令 | Exit | 說明 |
|---|---:|---|
| `make spec-check` | 0 | `spec-check.log`（94e5e75） |
| `make backend-test` | 0 | `backend-test-94e5e75.log`（部分 package 為 cached） |
| `make architecture-check`、`make ai-eval` | 0、0 | `architecture-check-94e5e75.log`、`ai-eval-94e5e75.log` |
| Mutation 第 1 次（94e5e75） | 1 | `mutants-run1.log`：44 killed、2 survived（U6、U24）、9 個 DB mutant 只 build |
| Mutation 第 2 次（補強的測試，尚未 commit） | 0 | `mutants-run2.log`：47 killed、8 個 DB mutant 只 build（還原檢查列出的 5 個檔案是當時未 commit 的補強，內容即 c39624c） |

`make frontend-test` NOT RUN：OpenAPI、schema 與 TS contract 都沒有改（`spec-check` 的 `PASS_TS_CONTRACTS_FRESH`）。

## Red-first

測試在實作之前寫好並先執行為紅（log 在 7e3e9aa、5a202cc 中）：

- `red-spend.log`：`internal/ai/spend` 只有空的 price table 時，`TestThePinnedPrice`、`TestCost`、`TestAttemptBound`、`TestParseAndFormatUSD`、`TestLimits`、`TestPeriods` 失敗（exit 1）。
- `red-provider-meter.log`：`NewMetered` 與預留之前，`TestBillableNeedsAMeter`（`New took a billable backend`）、`TestTheReservationBoundsTheCall`、`TestARefusedReservationRefusesTheCall`、`TestNothingIsReservedForARefusedCall`、`TestSettlement`、`TestAFailedSettlementKeepsTheReservation`、`TestMemoryMeter` 失敗（exit 1）。
- `red-anthropic-spend.log`：adapter 改為只接受有預留的 request 之後、metered 的測試 client 之前，既有測試與新的 `TestOnlyAMeteredClientCallsIt`、`TestQuote`、`TestOnlyTheApprovedRoles`、`TestSpend`、`TestAttemptsWithoutAnAnswerCount` 失敗。
- `red-aiapproval.log`：H-018 記錄之前，`TestTheH018Record`（`H-018 is not recorded`）、`TestGapReasons`、`TestRecorded`、`TestLiveTurnsOn`、`TestTheSwitchIsRequired`、`TestAnyMismatchKeepsLiveOff`、`TestBothCredentialsAreRequired`、`TestSpendLimitsAreChecked` 失敗。
- `red-generator-mapping.log`：錯誤對應之前，`TestFailureErrors`（`AI_QUOTA_EXCEEDED` 為 `INTERNAL_ERROR`，want `RATE_LIMITED`）與 `TestProviderFailures` 失敗（exit 1）。

## Mutation

`checks/evidence/2026-10-26-T-053/mutants.py`：每個 mutant 在一個檔案中替換恰好出現一次的文字，先 `go vet` 它列出的 package（不能 build 的 mutant 記為 INVALID，不算 killed），再以 `-count=1` 跑那些 package 的測試；finally 還原檔案，最後檢查 `internal/`、`cmd/` 相對最後的 commit 沒有改變。DB mutant 只確認 build：它們的測試需要 PostgreSQL，沒有 `GEMINUS_TEST_DATABASE_URL` 時記為 NOT RUN，不算 killed。

**第 1 次（`mutants-run1.log`）**：46 個 unit mutant 中 44 個 killed，2 個 survived；9 個 DB mutant 都 build。

- U6（meter 回空的 reservation ID 仍呼叫）survived：沒有測試給空 ID。補 `TestARefusedReservationRefusesTheCall` 的「no reservation ID」（`spend_unavailable`、可重試、backend 沒被呼叫）。
- U24（`anthropic.New` 不檢查 approval）survived：後面的 model 與 credential 檢查對無效的 token 一定失敗（`Approval.Model()` 為空），所以行為相同，但理由被遮住。改為 `anthropic.ErrNotApproved`，`TestTheAdapterTakesOnlyTheApproval` 與 `TestNewRefuses` 要求沒有 approval、record 不完整、record 改變後的 token 都是這個 error，其他情形都不是。
- D8（generator 不把 attribution 交給 client）只被 DB 測試涵蓋：補 unit 測試 `TestAMeteredCallIsChargedToItsTarget`（metered client、記錄 charge 的 meter：charge 的 target、request、organization、role 與 reservation）。

**第 2 次（`mutants-run2.log`，補強的測試尚未 commit，所以最後一行列出那 5 個檔案為 CHANGED）與第 3 次（`mutants-run3.log`，c39624c 的 tree，還原檢查為 clean）**：46 個 unit mutant 全部 killed，D8 也被 unit 測試擋下（47 killed）；其餘 8 個 DB mutant（D1–D7、D9）都 build，是否被擋下 NOT RUN。D9（settle 的 SELECT 不帶 organization 條件）在 RLS 之下是等價的 mutant：tenant scope 本來就只看得到自己的列，沒有測試能分辨；application 的 organization 條件是 RLS 之外的 defense in depth（AGENTS.md §2：application filter＋RLS）。SQL（migration 0016 的 trigger、policy、grant）沒有做 mutation：需要 PostgreSQL。下表為第 3 次的結果：

| ID | Mutant | 擋下的測試 |
|---|---|---|
| U1 | Cost ignores output tokens（`internal/ai/spend/spend.go`） | KILLED：`TestCost` |
| U2 | AttemptBound ignores max_tokens（`internal/ai/spend/spend.go`） | KILLED：`TestAttemptBound`, `TestSettlement` |
| U3 | cache writes at the cheaper 5m rate（`internal/ai/spend/spend.go`） | KILLED：`TestCost` |
| U4 | the reservation ignores retries（`internal/ai/provider/meter.go`） | KILLED：`TestAttemptsWithoutAnAnswerCount`, `TestSettlement`, `TestTheReservationBoundsTheCall` |
| U5 | a quota refusal becomes spend_unavailable（`internal/ai/provider/meter.go`） | KILLED：`TestARefusedReservationRefusesTheCall`, `TestSpend` |
| U6 | a reservation without an ID is accepted（`internal/ai/provider/meter.go`） | KILLED：`TestARefusedReservationRefusesTheCall` |
| U7 | attempts without an answer are not charged（`internal/ai/provider/meter.go`） | KILLED：`TestAttemptsWithoutAnAnswerCount`, `TestSettlement` |
| U8 | the settlement may exceed the reservation（`internal/ai/provider/meter.go`） | KILLED：`TestSettlement` |
| U9 | settle runs on the caller's (maybe cancelled) context（`internal/ai/provider/meter.go`） | KILLED：`TestSettlement` |
| U10 | a call is never settled (reservation never released)（`internal/ai/provider/provider.go`） | KILLED：`TestAFailedSettlementKeepsTheReservation`, `TestAttemptsWithoutAnAnswerCount`, `TestOnlyTheApprovedRoles`, `TestSettlement`, `TestSpend`, `TestTheLiveEvalOverHTTP` |
| U11 | New accepts a billable backend (no meter)（`internal/ai/provider/provider.go`） | KILLED：`TestBillableNeedsAMeter`, `TestOnlyAMeteredClientCallsIt` |
| U12 | MemoryMeter refuses a charge that exactly fits（`internal/ai/provider/meter.go`） | KILLED：`TestMemoryMeter` |
| U13 | MemoryMeter settle keeps the whole reservation（`internal/ai/provider/meter.go`） | KILLED：`TestMemoryMeter` |
| U14 | a metered call without attribution is accepted（`internal/ai/provider/provider.go`） | KILLED：`TestNothingIsReservedForARefusedCall` |
| U15 | a refused reservation still calls the provider（`internal/ai/provider/provider.go`） | KILLED：`TestARefusedReservationRefusesTheCall`, `TestNothingIsReservedForARefusedCall`, `TestSpend` |
| U16 | the quota limit is not passed on（`internal/ai/provider/meter.go`） | KILLED：`TestARefusedReservationRefusesTheCall` |
| U17 | spend_unavailable is not retryable（`internal/ai/provider/errors.go`） | KILLED：`TestARefusedReservationRefusesTheCall` |
| U18 | the adapter calls without a reservation（`internal/ai/provider/anthropic/anthropic.go`） | KILLED：`TestOnlyAMeteredClientCallsIt` |
| U19 | the adapter serves any prompt role（`internal/ai/provider/anthropic/anthropic.go`） | KILLED：`TestOnlyTheApprovedRoles` |
| U20 | a transport failure is not counted（`internal/ai/provider/anthropic/anthropic.go`） | KILLED：`TestAttemptsWithoutAnAnswerCount` |
| U21 | a cut body is not counted（`internal/ai/provider/anthropic/anthropic.go`） | KILLED：`TestAttemptsWithoutAnAnswerCount` |
| U22 | New accepts another model（`internal/ai/provider/anthropic/anthropic.go`） | KILLED：`TestTheAdapterTakesOnlyTheApproval` |
| U23 | New accepts other credentials（`internal/ai/provider/anthropic/anthropic.go`） | KILLED：`TestTheAdapterTakesOnlyTheApproval` |
| U24 | New accepts no approval（`internal/ai/provider/anthropic/anthropic.go`） | KILLED：`TestNewRefuses`, `TestTheAdapterTakesOnlyTheApproval` |
| U25 | the record needs no retention confirmation（`internal/config/aiapproval/aiapproval.go`） | KILLED：`TestAnyMismatchKeepsLiveOff`, `TestGapReasons`, `TestMainRefusesWhileLiveIsOff`, `TestRecorded`, `TestTheH018Record`, `TestTheLiveEvalRunsOnlyWhenLiveIsOn` |
| U26 | the record needs no spend-limit confirmation（`internal/config/aiapproval/aiapproval.go`） | KILLED：`TestAnyMismatchKeepsLiveOff`, `TestGapReasons` |
| U27 | the record may name any role（`internal/config/aiapproval/aiapproval.go`） | KILLED：`TestGapReasons` |
| U28 | the record may keep raw text in logs（`internal/config/aiapproval/aiapproval.go`） | KILLED：`TestAnyMismatchKeepsLiveOff`, `TestGapReasons` |
| U29 | Valid does not recheck the record（`internal/config/aiapproval/aiapproval.go`） | KILLED：`TestRecorded` |
| U30 | AllowsRole ignores the data policy（`internal/config/aiapproval/aiapproval.go`） | KILLED：`TestRecorded` |
| U31 | the switch is not needed（`internal/config/ai.go`） | KILLED：`TestTheSwitchIsRequired` |
| U32 | any AI_MODEL is accepted（`internal/config/ai.go`） | KILLED：`TestAnyMismatchKeepsLiveOff` |
| U33 | the price check is skipped（`internal/config/ai.go`） | KILLED：`TestAnyMismatchKeepsLiveOff` |
| U34 | the record's gap is ignored（`internal/config/ai.go`） | KILLED：`TestAnyMismatchKeepsLiveOff`, `TestMainRefusesWhileLiveIsOff`, `TestTheLiveEvalRunsOnlyWhenLiveIsOn`, `TestThisBuildKeepsLiveOff` |
| U35 | one key is enough（`internal/config/ai.go`） | KILLED：`TestBothCredentialsAreRequired` |
| U36 | the secondary key may be the primary key（`internal/config/ai.go`） | KILLED：`TestBothCredentialsAreRequired` |
| U37 | the monthly limit may exceed the budget（`internal/config/ai.go`） | KILLED：`TestSpendLimitsAreChecked` |
| U38 | the live eval runs while live AI is off（`internal/aievals/main.go`） | KILLED：`TestMainRefusesWhileLiveIsOff` |
| U39 | a live report may take any name（`internal/aievals/live.go`） | KILLED：`TestTheLiveEvalOverHTTP`, `TestTheReportIsApart` |
| U40 | a ready safe-failure case breaks no gate（`internal/aievals/live.go`） | KILLED：`TestRunScoresTheAnswers` |
| U41 | the run goes on after its cap refuses（`internal/aievals/live.go`） | KILLED：`TestTheCapStopsTheRun` |
| U42 | the run's cap may exceed the monthly limit（`internal/aievals/main.go`） | KILLED：`TestTheLiveEvalRunsOnlyWhenLiveIsOn` |
| U43 | a spend limit is not RATE_LIMITED in the API（`internal/proposals/public.go`） | KILLED：`TestFailureErrors` |
| U44 | the generator has no message for a spend limit（`internal/proposals/generator/generator.go`） | KILLED：`TestProviderFailures` |
| U45 | the refusal's limit may be any label（`internal/audit/audit.go`） | KILLED：`TestRefused` |
| U46 | an ID-shaped model name reaches a metric（`internal/observability/telemetry/telemetry.go`） | KILLED：`TestSpend` |
| D1 | the month limit is not checked（`internal/storage/aispend/aispend.go`） | BUILDS（NOT RUN：測試需要 PostgreSQL） |
| D2 | the deployment day limit is not checked（`internal/storage/aispend/aispend.go`） | BUILDS（NOT RUN：測試需要 PostgreSQL） |
| D3 | the organization day limit is not checked（`internal/storage/aispend/aispend.go`） | BUILDS（NOT RUN：測試需要 PostgreSQL） |
| D4 | settle moves the totals the wrong way（`internal/storage/aispend/aispend.go`） | BUILDS（NOT RUN：測試需要 PostgreSQL） |
| D5 | settle does not check the state（`internal/storage/aispend/aispend.go`） | BUILDS（NOT RUN：測試需要 PostgreSQL） |
| D6 | settle charges today instead of the reservation's day（`internal/storage/aispend/aispend.go`） | BUILDS（NOT RUN：測試需要 PostgreSQL） |
| D7 | a reservation is not audited（`internal/storage/aispend/aispend.go`） | BUILDS（NOT RUN：測試需要 PostgreSQL） |
| D8 | the generator charges no target（`internal/proposals/generator/generator.go`） | KILLED：`TestAMeteredCallIsChargedToItsTarget` |
| D9 | settle reads without the organization filter (RLS still filters)（`internal/storage/aispend/aispend.go`） | BUILDS（NOT RUN：測試需要 PostgreSQL） |

## NOT RUN

| 項目 | 原因 |
|---|---|
| **Live smoke（`make ai-smoke-live`）** | 依安全規則，本 session 不做任何 live 呼叫；而且這個 build 的 H-018 record 缺 D-113 的確認，指令會拒絕（exit 2）。之後由 coordinator 在有 key 的 session 執行（H-025 的預先授權），前提是 review 通過與 D-113 的確認記入 record |
| **Live eval（`make ai-eval-live`）** | 同上 |
| DB 測試：`internal/storage/aispend`、`internal/proposals/generator/metered_test.go` 的 DB 測試、`db/tests/ai_spend.sql`（含 0016 的 down／up）、其他既有 DB 測試 | 沒有 PostgreSQL（依指示不安裝）；Go DB 測試已編譯並 vet（在 `backend-test` 中 skip），SQL 已依 template 重產（`PASS_DB_TESTS_FRESH`），未在資料庫上執行 |
| `make integration-test` | 同上（需要一次性 PostgreSQL＋API＋worker） |
| DB mutant D1–D9 是否被擋下 | 同上；只確認 build |
| `make frontend-test` | HTTP contract、schema、TS 型別沒有改 |
| Analyst／mapper 的 live 使用與它們的 corpus | D-112：H-018 的資料政策不含 import 的 sample rows 與 analyst 的結果；T-027、T-030 |

## 準備好的 live 指令（NOT RUN）

在 D-113 的確認以 reviewed change 寫入 `internal/config/aiapproval` 的 H-018 record（`Retention`：`Arrangement`、`By`、`At`、`Source`）並通過 review 之後，於雲端環境 secret 提供 `GEMINUS_ANTHROPIC_API_KEY_A`／`_B` 的 session，在 repository root：

```sh
# 一個 synthetic case（MOD-002），上限 USD 2；report: checks/ai-evals/live-smoke-<date>-claude-opus-5-5.json
AI_PROVIDER=anthropic AI_POLICY_APPROVAL=H-018 AI_MODEL=claude-opus-5-5 GEMINUS_AI_LIVE=on make ai-smoke-live
# 50 個 modeler case，上限 USD 10；report: checks/ai-evals/live-<date>-claude-opus-5-5.json
AI_PROVIDER=anthropic AI_POLICY_APPROVAL=H-018 AI_MODEL=claude-opus-5-5 GEMINUS_AI_LIVE=on make ai-eval-live
```

指令印出它自己的 exit code（0 完整且 gate 為 0；1 gate 失敗或達上限停止，report 仍寫；2 拒絕、沒有呼叫；3 report 寫不進去）。`AI_EVAL_LIVE_MAX_USD` 可把上限調低（須 ≤ 月上限）。`ANTHROPIC_API_KEY` 不需要也不會被讀。Eval 的花費不經過 DB ledger（D-114 (b)），與 worker 的花費合計由 Console spend limit 限制。

## 資料保留與 ZDR（官方來源，2026-10-26 取得）

完整引文在 [SECURITY §7](../docs/SECURITY.md#7-ai-data-governance)：

- https://platform.claude.com/docs/en/manage-claude/api-and-data-retention ：「Conversation content (your prompts and Claude's outputs) is not retained by default; the exception is Covered Models, which require 30-day retention.」；「ZDR is enabled per organization; each new organization requires ZDR to be enabled separately by your account team, and enablement does not automatically extend to other organizations under the same account.」；Console（含 playground）不在 ZDR 內；被 trust and safety 標記時「Anthropic may retain inputs and outputs for up to 2 years」；structured outputs「Only the JSON schema is cached, for up to 24 hours since last use.」
- https://privacy.claude.com/en/articles/7996866-how-long-do-you-store-my-organization-s-data （頁面日期 2026-07-01）：API「we automatically delete inputs and outputs on our backend within 30 days of receipt or generation」；標記時 inputs／outputs 至多 2 年、classification score 至多 7 年。兩頁的說法不同，本專案以較保守的「至多 30 天」為準。
- https://support.claude.com/en/articles/15425695 （頁面日期 2026-09-01）：Covered Models 為 Claude Mythos 5.1、Fable 5.1、Mythos 5、Fable 5；Claude Opus 5.5 不在其中。

兩個 provider organization 是否開了 ZDR 是只有 owner 看得到的帳號設定；agent 無法查證，所以 live 保持關閉（D-113）。

## 需要 owner 確認的事項

1. **（已確認：H-026，2026-10-26，standard、沒有 ZDR、兩者相同；見上方「Owner 的確認（H-026）」。以下是原本的請求。）D-113：兩個 provider organization（`GEMINUS_ANTHROPIC_API_KEY_A`／`_B` 背後）的資料保留安排**是 standard 還是 ZDR，且兩者相同；並接受 standard 下的保留（30 天內刪除；被標記時至多 2 年）與 structured outputs 的 schema cache（至多 24 小時）。確認後由 reviewed change 寫入 H-018 record 的 `Retention`；在此之前 live AI 無法開啟（`retention_unconfirmed`）。**這是 live smoke／eval 之前唯一缺少的 owner 確認。**
2. D-110–D-115 的批准（或改選其他選項）：上限的單位與數值（月 USD 100、部署日 USD 10、organization 日 USD 5）、預留與結算的規則、只開 modeler／scenario、live eval 的範圍與上限（eval USD 10、smoke USD 2）、開關與兩把 key。
3. 已確認（H-025，2026-10-26，coordinator 轉達）：兩個 provider organization 的 Console spend limit 已設定；H-018 record 的 `SpendLimits` 已記錄（KCL1104、2026-10-26、H-025）。H-025 本身記在 main branch，不在本 branch。
4. 已確認（H-025）：coordinator 可在 T-053 通過獨立 review 後，以 synthetic 資料執行 live smoke 與 `make ai-eval-live`；但仍以第 1 項為前提。

## 偏差

- **兩把 key 都必要**（D-115）：ADR-027 中 `_B` 為選填；live 改為兩把都要（T-053 的「兩把 key」與 D-018 的兩個 organization）。Fake／disabled 不變。
- **`AI_MODEL` 必須等於 record 的 model**，沒有預設；不使用 server-side `fallbacks`（H-018 只批准一個 model）。
- **Live eval 不經 DB ledger**（D-114 (b)）：以每次 run 自己的上限計量；app 的月上限不含 eval 的花費，合計由 Console spend limit 限制。
- **Migration 編號 0016**：若其他進行中的 task 也用 0016，cherry-pick 時需改號（migration 尚未部署）。
- tasks.json 的 verification command `make integration-test` 與 `make ai-eval-live` 都 NOT RUN（見上）。

## 尚未決定（PROPOSED）

D-110、D-111、D-112、D-113、D-114、D-115（docs/DECISIONS_AND_TBD.md「T-053 的提案」、planning/decisions.json），都沒有被 agent 批准。

## 風險

- 預留的 4096 token framing 保留若小於 API 實際加的輸入，結算可能略超過預留，總數可能略超過上限一次（下一個預留即被拒）；Console spend limit 是最後一道。
- `max_tokens` 8192 同時限制 thinking 與輸出：live 的回答可能因 `max_tokens` 截斷而成為 invalid output（計費但沒有 proposal），live eval 會顯示比例。
- DB 層（條件 UPDATE、RLS、trigger、grants、down／up）只經過編譯、vet 與閱讀檢查，沒有在 PostgreSQL 上執行；SQL 或 Go 的 DB 程式若有錯，要到 coordinator 的 DB 測試才會發現；`TestConcurrentReservationsNeverOvershoot` 是跨 process 一致的主要證據，NOT RUN。
- H-025 不在本 branch：`aiapproval` 的 `SpendLimits.Source` 指向它，合併時需與 main 的 human_decisions 一致。
