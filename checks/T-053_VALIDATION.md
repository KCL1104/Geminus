# T-053 驗證報告：Live AI 開啟（H-018）

**日期：2026-10-26｜狀態：獨立 review 第 1 輪（H-003）為 CHANGES REQUESTED，修正在 `t053-r1`（見最後一節「Review 第 1 輪」），待 re-review；live AI 在 re-review 接受之前保持不執行。`planning/tasks.json` 的狀態未改（仍為 not_started），由 coordinator／reviewer 決定。｜需求：REQ-008、REQ-016、REQ-019、REQ-020｜Gate：D-018（APPROVED）；本任務提出的 D-110–D-115 是 **PROPOSED**，`approved_by`／`approved_at` 為 null，T-053 依建議實作並以它們為 gate（tasks.json 的 `decision_gates` 加入 D-110–D-115）。｜驗收測試：TEST-027（audit／telemetry privacy 的 T-053 部分）；TEST-024（analyst grounding）不在本次範圍：analyst 的 live 使用依 D-112 不開，analyst job 是 T-030。｜原本的 T-053 build 中 live AI 無法開啟：H-018 的 record 缺 owner 對資料保留安排的確認（D-113，`retention_unconfirmed`）。Owner 的確認（H-026）已在 `t053-ret` 記入，record 已完整，見下節。Live smoke 與 live eval 都 NOT RUN；DB 測試與 `make integration-test` 為 NOT RUN（見「NOT RUN」）。**

## Owner 的確認（H-026）與 reservation ID 的 domain（追加，`t053-ret` branch）

T-053 的 commit 整合到 main（79c9423）之後，在 `t053-ret`（從 79c9423 開出，沒有 push）追加兩項可分開 review 的修改。以下的「實際執行結果」等章節仍是追加之前的紀錄；追加的驗證在本節。**Review 第 1 輪（L5）的更正**：H-026 之後 live AI 在條件齊全時會開啟，所以下面寫於 H-026 之前、說「`Retention` 空白」「指令會拒絕」的句子已改為現況（稽核表 2a、3 列與「NOT RUN」表）；review 第 1 輪的修正見本報告最後一節。

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
| 2a. 記錄 H-018 的批准 | `aiapproval.h018`：ID H-018、provider anthropic、model `claude-opus-5-5`、資料政策（synthetic 與使用者輸入的文字、只服務 modeler／scenario、log 不存原始文字）、每月 USD 100、credential `org-a`／`org-b`、approver「KCL1104 (repository owner)」、2026-10-20、來源（decisions.json 的 human_decisions 與 decision page）；**Console spend limit：KCL1104、2026-10-26、H-025**；`Retention`：H-026 之前空白，H-026 起為 standard、KCL1104、2026-10-26、來源 H-026（見本報告開頭一節） | `TestTheH018Record`（逐欄 golden）、`TestNothingElseIsRecorded`、`TestGapReasons`、`TestRecorded` | `red-aiapproval.log`；mutant U25–U30 |
| 2b. 只有這筆紀錄＋兩把 credential＋明確的開關能開啟 live；不一致保持關閉並給 typed reason；預設 fake | `config.LoadAI`：record 的 `Gap` → `AI_MODEL` 等於 record 的 model → 有價格 → `GEMINUS_AI_LIVE=on`；條件不合而開關 on 時拒絕啟動（`GEMINUS_AI_LIVE:retention_unconfirmed`、`AI_MODEL:model_mismatch`／`required`、`GEMINUS_ANTHROPIC_API_KEY_B:required` 等）；開關不是 on 時 `LiveOff` 為原因且不讀 key；`anthropic.New` 只接受 record 的 model 與 credential label（先檢查 approval：`ErrNotApproved`）；worker 啟動時記 live on／off 與原因 | `TestLiveTurnsOn`、`TestTheSwitchIsRequired`、`TestAnyMismatchKeepsLiveOff`（record 的每一欄、其他 model、沒有價格）、`TestBothCredentialsAreRequired`（少一把、B＝A）、`TestTheAdapterTakesOnlyTheApproval`、`TestThisBuildKeepsLiveOff`、`TestLiveAIStaysOff`、`TestNewRefuses`、`TestTheProviderIsTheConfigured`（worker） | mutant U22–U24、U31–U36；D-115（PROPOSED） |
| 2c. 只服務 H-018 資料政策內的角色 | `aiapproval.liveRoles`（modeler、scenario）；adapter 對其他 role 在 I/O 前回 `policy_not_approved` | `TestOnlyTheApprovedRoles`、`TestGapReasons`（record 列出其他 role → `policy_unsupported`） | mutant U19、U27、U30；D-112（PROPOSED） |
| 3. Anthropic 的保留／ZDR 規則：官方來源、日期、原文 | [SECURITY §7](../docs/SECURITY.md#7-ai-data-governance)（見下方「資料保留與 ZDR」）；ZDR 的開啟是 owner 才能確認的帳號設定；H-026 之前 record 的 `Retention` 空白、live 保持關閉，H-026 起 record 記 standard、完整 | `TestGapReasons`（`retention_unconfirmed`）、`TestThisBuildsRecord`（原 `TestThisBuildKeepsLiveOff`）、`TestH026TurnsLiveOn`、`TestTheLiveEvalRunsOnlyWhenLiveIsOn`（H-026 之前的 record 被拒） | mutant U25、U34；D-113（PROPOSED） |
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

**第 2 次（`mutants-run2.log`，補強的測試尚未 commit，所以最後一行列出那 5 個檔案為 CHANGED）與第 3 次（`mutants-run3.log`，c39624c 的 tree，還原檢查為 clean）**：46 個 unit mutant 全部 killed，D8 也被 unit 測試擋下（47 killed）；其餘 8 個 DB mutant（D1–D7、D9）都 build，是否被擋下 NOT RUN。D9（settle 的 SELECT 不帶 organization 條件）在 RLS 之下與原程式行為相同：以 worker 執行的測試看不到其他 tenant 的列，分辨不出。~~沒有測試能分辨~~（review 第 1 輪 M1 的更正：這句是錯的。以不受 RLS 限制的 superuser 執行 meter 的測試，如其他 storage package 的 `TestOrganizationFilter`，就能分辨；本任務當時缺這樣的測試。見最後一節，`TestOrganizationFilter` 擋下 D9 與另外四個 filter mutant）。application 的 organization 條件是 RLS 之外的 defense in depth（AGENTS.md §2：application filter＋RLS）。SQL（migration 0016 的 trigger、policy、grant）沒有做 mutation：需要 PostgreSQL。下表為第 3 次的結果：

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
| **Live smoke（`make ai-smoke-live`）** | 依安全規則，本 session 不做任何 live 呼叫。寫這列時 H-018 record 缺 D-113 的確認、指令會拒絕（exit 2）；H-026 記入之後，在有兩把 key 與 `GEMINUS_AI_LIVE=on` 的環境中指令**會呼叫 production API**（review 第 1 輪 L5 的更正）。之後由 coordinator 在有 key 的 session 執行（H-025 的預先授權），前提是 review 通過 |
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

指令印出它自己的 exit code（0 完整且 gate 為 0；1 gate 失敗或達上限停止，report 仍寫；2 拒絕、沒有呼叫；3 report 寫不進去）。`AI_EVAL_LIVE_MAX_USD` 可把上限調低（須 ≤ 月上限）。`ANTHROPIC_API_KEY` 不需要也不會被讀。Eval 的花費不經過 DB ledger（D-114 (b)），與 worker 的花費合計由 Console spend limit 限制。**（本段寫於 review 第 1 輪之前；之後的指令、上限、spend record 與 report 名稱見最後一節的「準備好的 live 指令（review 第 1 輪之後）」。）**

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

## Coordinator 的執行（f4db5d6，與 T-050 一起整合）

Coordinator 把本任務的五個 commit（4bfb8f2–8948b70）cherry-pick 到 `claude/lucid-meitner-vq1awo`（2e5a176 之後），接著是 T-050 的十一個 commit（14b7000–79c9423）、`t053-ret` 的三個 commit（fc0b277、4450490、921910f）與 coordinator 記錄 H-026 的 f4db5d6。衝突只在 `Makefile`（T-050 改了 `simulation-reference-test` 的說明，本任務新增 `ai-eval-live`、`ai-smoke-live`，兩邊都保留）、`planning/decisions.json` 與決策登記檔（D-096、D-097 與 D-110–D-115 都是新增，兩邊都保留，依 ID 排列），以及 `make spec-check` 重產的 `checks/*.json`（取一邊後重產）。

**第一次執行（79c9423，`checks/evidence/2026-10-26-T-050-T-053/coordinator-*-first-run*` 與本任務 evidence 的 `coordinator-*-first-run*`）**：`spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`frontend-test`、`simulation-reference-test`、`backend-numeric`、`backend-race` 都是 exit 0；`make integration-test` 與 DB mutant 用的 `make db-test` exit 2：`db/tests/constraints.sql:361` 的 `FAILED: ID columns without their D-001 domain: ai_spend_reservations.id=text`。這是本任務的錯誤（migration 0016 的 reservation ID 用了 `text`），由 `t053-ret` 的 4450490 修正（`twin.asr_id` domain；0016 從未 push 或部署，所以修改它而不是新增 migration）。

**第二次執行（f4db5d6，全新的 `dt_test_t050t053b` 與 `dt_test_t053r1m`）**：

| 項目 | 結果 | Log |
|---|---|---|
| `make spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`backend-race` | 全部 exit 0 | `2026-10-26-T-050-T-053/coordinator-*.log`、`coordinator-exit-codes.txt` |
| `make integration-test`（全新 DB，PostgreSQL 16.15） | exit 0 | `2026-10-26-T-050-T-053/coordinator-integration.log` |
| govulncheck v1.9.0（go1.27.3） | exit 0，No vulnerabilities found | `coordinator-govulncheck.log` |
| `make db-test`（全新 DB，含 `db/tests/ai_spend.sql` 與 constraints） | exit 0 | `coordinator-db-mutants-prepare.log` |
| 本任務的 DB 測試（`TestReserveAndSettle`、`TestLimitsRefuse`、`TestConcurrentReservationsNeverOvershoot`、`TestSettleKeepsItsDayAndOrganization`、`TestFailClosed`、`TestAMeteredProposal`、`TestASpendLimitRefusesTheProposal`；`-race -v`，`GEMINUS_REQUIRE_DB_TESTS=1`） | 全部 PASS | `coordinator-new-db-tests.log` |
| DB mutant D1–D9（`mutants.py --only D1..D9`，在 `git archive HEAD` 的複本上） | 8 killed、D9 survived；exit 1 是因為有存活的 mutant | `coordinator-mutants-db.log` |

- 本報告前面「DB 測試只編譯與 vet，NOT RUN」與「DB mutant 是否被擋下 NOT RUN」兩項，現在由 coordinator 在 PostgreSQL 上執行，結果如上。
- **D9 存活**：與「Mutation」一節的預測相同：settle 的 SELECT 少了 organization 條件，RLS 仍然只讓 tenant 看到自己的列，以 worker 執行的測試分辨不出。~~沒有測試能分辨~~（review 第 1 輪 M1 的更正：以 superuser 執行的測試能分辨，見最後一節）。application 的 organization 條件是 RLS 之外的 defense in depth（AGENTS.md §2）。交給 reviewer 判斷是否需要能分辨兩者的測試。
- `mutants.py` 最後的還原檢查在 archive 複本上印出 `fatal: not a git repository`：複本沒有 `.git`，所以這一行沒有意義；mutant 只改複本，複本用完即刪，working tree 沒有被修改。
- `frontend-test`、`simulation-reference-test` 與 `backend-numeric` 只在第一次（79c9423）執行：之後的五個 commit 沒有改 `apps/`、`internal/simulation/`、`internal/results/`、`schemas/`、`openapi/` 或 `scripts/`（`git diff --name-only 79c9423 f4db5d6`），所以結果仍適用。
- 第二次執行開始時 `coordinator-environment.txt` 記錄「6 other changes」：是本任務 evidence 中尚未 commit 的六個 `coordinator-*-first-run*` 檔，不是程式的修改。
- **NOT RUN**：`make ai-smoke-live` 與 `make ai-eval-live`（依 H-025，本任務通過獨立 review 之後才以 synthetic 資料執行）；`make e2e-test`（本任務沒有改 UI）；CI 在 push 之後確認。
- 本任務仍待：獨立 review（H-003）；owner 批准 D-110–D-115（PROPOSED）。

## Review 第 1 輪（2026-10-26）：CHANGES REQUESTED（一個 High、三個 Medium、六個 Low、Info）

獨立 reviewer（H-003）在 a0fb365 審查，報告與 evidence 在 `checks/evidence/2026-10-26-T-053-r1/reviewer/`（`REVIEW.md`；probe P1–P7 存為 `*_test.go.txt`）。本輪在 `t053-r1`（從 a0fb365 開出，先 cherry-pick reviewer 的 b786562 → d28eb79；沒有 push）修正，依 coordinator 對 H1、M1–M3 與各 Low 的決定實作。每個修正都先寫測試，在修改前的程式上執行為紅（log 存為 `red-*.log`），修正後為綠（`green-*.log`）；沒有降低任何 assertion，也沒有加 skip。因為 coordinator 的決定改變了規則（M2 的 1.1 倍、M3 的不再 clamp、L3 的 database 時鐘、H1 的 report 名稱），既有測試的期望值隨之改變的地方逐一列在「改變的期望值」。Migration 0016 已 push，沒有修改；本輪不需要 schema 變更，所以沒有 0017（DB 的 shape 不變，`make db-test` 照舊）。D-110–D-115 仍為 PROPOSED，新增的 D-116 也是 PROPOSED，`approved_by`／`approved_at` 都是 null；`planning/tasks.json` 中 T-053 的狀態未改（not_started）。**整輪沒有任何 live 呼叫**：沒有設定 `GEMINUS_AI_LIVE=on` 配真的 key，沒有執行 `make ai-smoke-live`／`make ai-eval-live`；沒有讀取、印出、量測或記錄 `GEMINUS_ANTHROPIC_API_KEY_A`／`_B` 或 `ANTHROPIC_API_KEY` 的值。測試的 key 都是明顯的假字串，backend 都是 process 內的 fake 或本機 httptest server。Evidence 在 `checks/evidence/2026-10-26-T-053-r1/`（`run.sh` 執行指令並把 exit code 寫在 log 的最後一行）。

### Commit

| Commit | 內容 |
|---|---|
| d28eb79 | Reviewer 的報告與 evidence（cherry-pick b786562） |
| 2cf81fe | 程式與測試：H1／L6（spend record、上限、report 名稱、錯誤時的 summary）、M1（superuser 的 organization filter 測試）、M2（inference geo）、M3／L1（不在可能已計費的 attempt 之後 failover、不 clamp、warning 與 counter）、L2、L3、L4（`refusals.sh` 的 guard）、L5（`live_test.go` 的註解）、I1；`checks/ai-evals/live-spend.jsonl`（空檔）、`.gitignore`、Makefile 的說明 |
| ea9a85a | D-110、D-111、D-114、D-115 的文字、D-116（PROPOSED）、tasks.json（T-053 的 gate 加 D-116；T-039 的後續）、SECURITY、CONTRACT_BLOCKERS、AI_SPEC、API_DATABASE、ARCHITECTURE、testdata/ai-evals/README；`TestOrganizationFilter` 改用每次 run 自己的金額（mutation 第 1 次 F4 存活的原因，見「Mutation」）；重產的 `checks/decisions-check.json`、`checks/planning-check.json` |
| （最後一個） | 本節、本報告前面的更正（L5、M1 的 D9 句）；evidence（red／green log、probe、mutation、驗證 log、`run.sh`、`mutants.py`）；重產的 `checks/docs-check.json` |

### H1（High）：live eval 與 smoke 不在程式內的日、月上限之內

- **發現**：`make ai-eval-live`／`make ai-smoke-live` 唯一的限制是每次 run 的 in-memory 上限，而那個上限只和月上限比較；沒有任何紀錄跨 run 累計 eval 的花費，DB ledger 也看不到。預設的 smoke（USD 2）加 eval（USD 10）在同一個 UTC 日可以用到 USD 12，超過部署的日上限 USD 10；`AI_EVAL_LIVE_MAX_USD` 可以設到月上限，一次 run 用掉整月（probe P3：100、50、10.01 都建立了 backend、exit 0）；同一天再跑一次會覆寫前一份 report，而 report 是 eval 花費唯一的紀錄。
- **修正**（`internal/aievals/record.go`、`main.go`、`live.go`；coordinator 的決定）：
  - Run 的上限須 ≤ 部署的日上限（config 的 `spend.Limits.Validate` 保持日 ≤ 月，所以也 ≤ 月上限；原本另外比較月上限的那一行因此不會成立，已拿掉並寫明理由），否則 exit 2、不建立 backend。
  - **Spend record** `checks/ai-evals/live-spend.jsonl`（append-only、每行一個 JSON object、與 report 一起 commit，repository 中以空檔開始）。第一次呼叫前寫 open line：`run_id`（ULID）、`kind`（eval／smoke）、`started_at`（UTC，秒）、`day`、`month`、`model`、`approval`、`cap_nano_usd`、`report`（檔名）。Run 結束時不論結果都寫 close line：`settled_nano_usd`（meter 的實際花費）、`committed_nano_usd`（meter 持有的金額，含沒結算的預留，計入的是這個）、`result`（`complete`／`gate_failed`／`stopped`／`error`／`report_unwritten`）。
  - 已花費：close line 的 committed 計入它開啟與結束的 UTC 日與月（跨午夜的 run 兩天都算），沒有 close 的 open line 以整個上限計（安全側）；其他日、月不算。Run 的上限大於該日或該月剩下的額度（上限 − 已花費）即 exit 2、不建立 backend、不寫檔。
  - Record 缺少、不是檔案、讀不了或任何一行格式不對（不是一個 JSON object、多或少成員、未知的 event／kind／result、run ID 不是 ULID、時間不是 `2006-01-02T15:04:05Z`、日或月與時間不符、上限不為正、金額為負、committed 小於 settled、report 檔名不是該 run 的、close 在 open 之前、同一個 run 開兩次或關兩次、最後一行沒有換行）即拒絕每個 run（fail closed；錯誤只說第幾行與原因，不引用內容）。
  - 一次只有一個 run 能讀 record 並寫 open line：旁邊的 `live-spend.jsonl.lock` 以 `O_EXCL` 建立，寫完 open line 就移除（`.gitignore`）；lock 存在時拒絕，訊息請 operator 確認沒有 run 在跑之後移除它。
  - Report 名稱改為 `live-[smoke-]<YYYY-MM-DD>T<hhmmss>Z-<run ID>-<model>.json`，以 `O_EXCL` 寫入，已存在即 `ErrReportExists`（從不覆寫）；建立 backend 之前也先確認不存在。Report 加 `run_id`、`inference_geos`、`spend_warnings`。
  - **仍有的缺口**（寫入 D-114 (b) 與本報告）：eval 的花費記在這個檔案，worker 的花費記在 DB ledger，兩者互不計入對方的上限。在沒有任何已部署、開啟 live 的 worker 之前，這個檔案就是 app 內的全部紀錄；兩者都有時，合計只由 Console spend limit 限制（H-025 設定的數值沒有記在 repository）。Record 也是每個 checkout 各自的：另一個 checkout 要等 commit 並合併後才看得到。D-114 (b) 提案（PROPOSED）：部署第一個開啟 live 的 worker 時，由 owner 選擇保持分開或改為選項 B（eval 也經 DB ledger）；T-039 的 scope 也列了這一項。
- **先紅後綠**：`red-h1-l6-main.log`（exit 1，修改前）：`TestTheRunCapFitsTheDayAndTheMonth` 的 5 個 subtest 失敗（P3 的 100、50、10.01，預設 eval 上限高於設低的日上限 5、預設 smoke 上限高於日上限 1.5；高於月上限的那個在修改前就會拒絕，通過）；`TestARunIsRecordedOpenThenClosed`（第一次呼叫前 record 是空的）、`TestTheRecordBoundsTheNextRun`（6 個中 4 個應拒絕的 case 都跑了；兩個應該跑的 case 通過）、`TestAnUnreadableRecordRefuses`（19 種格式錯誤都跑了，另外 missing 與目錄也跑了）、`TestEveryEndClosesTheRecord` 失敗；`red-h1-report-overwrite.log`（exit 1）：`TestAReportIsNeverOverwritten`，第二份 report 覆寫了第一份。修改後 `green-h1-l6-main.log`、`green-h1-report-overwrite.log` 都是 exit 0（另有 record 本身的 `TestTheRecordSumsTheDayAndTheMonth`：固定時間下 30 日、跨月結束、未關閉、九月、十二月、UTC−10 的同一 UTC 日；`TestTheRecordIsLockedWhileARunOpens`；`TestAnOpenLineIsChecked`）。Probe P3 原樣執行（`probes-p3.log`）：3 個 subtest 都 PASS（日上限的檢查在讀 record 之前）。
- **擋下的測試**（mutation 第 2 次）：K1（上限可超過日上限）被 `TestTheLiveEvalRunsOnlyWhenLiveIsOn`（訊息）擋下（沒有它時 record 的日額度檢查也會拒絕，因為剩下的額度不會超過日上限）；K2、K3（不查日、月剩下的額度）被 `TestTheRecordBoundsTheNextRun`；K4（沒有 close 的 run 不計）、K5（close 只算開啟的日）、K6（以 settled 代替 committed）被 `TestTheRecordSumsTheDayAndTheMonth`（K4 也被 `TestTheRecordBoundsTheNextRun`）；K7（格式錯誤的行被略過）、K8（缺少的 record 當空的）、K14（close 可以沒有 open）被 `TestAnUnreadableRecordRefuses`；K9（第一次呼叫前沒有 open line）被 `TestARunIsRecordedOpenThenClosed`；K10（錯誤時不寫 close）、K11（錯誤時不印 summary）、K15（停止的 run 記為 complete）被 `TestEveryEndClosesTheRecord`；K12（report 可覆寫）被 `TestAReportIsNeverOverwritten`；K13（lock 不是 exclusive）被 `TestTheRecordIsLockedWhileARunOpens`。

### L6（Low）：付費呼叫之後停止的 run 沒有留下花費

- **發現**：`Run` 回錯誤時 `Main` 只印「the run stopped」、exit 3，沒有 summary 與花費。
- **修正**：`Run` 回錯誤時也回目前為止的 report（incomplete，花費取自 meter）；`Main` 在 backend 建立之後的每一種結束都印 summary（run ID、case 數、gate、花費、committed、上限、token、spend warning）並寫 close line（`error`）；close line 寫不進去時在 stderr 說 open line 以整個上限計。`ExitReport`（3）的說明改為「已有呼叫而錯誤停止，或 report、close line 寫不進去」。
- **先紅後綠與擋下**：`TestEveryEndClosesTheRecord` 的第三段（暫時目錄的 corpus：MOD-002 之後的 case 的 fixture 不存在，第一個付費呼叫後 `Run` 停止）：修改前 stdout 是空的、record 沒有 close；修改後 stdout 有 `cost USD 0.012`、close line 的 result 為 `error`、settled 為那次呼叫的成本。Mutant K10、K11。

### M1（Medium）：沒有測試 ledger 自己的 organization filter

- **發現**：AGENTS.md §2 要求 application filter 加 RLS；aispend 的測試都以 worker 在 RLS 之下執行，filter 拿掉也不會失敗。Reviewer 的 mutant D9（settle 的 `SELECT … FOR UPDATE` 不帶 `organization_id`）、F1（reserve 的 organization 日 `UPDATE`）、F2（`TotalsAt`）都存活；本報告「Mutation」與 coordinator 一節說 D9「沒有測試能分辨」是錯的。
- **修正**：`internal/storage/aispend/filter_test.go` 的 `TestOrganizationFilter`，比照 projects、proposals 的同名測試，以不受 RLS 限制的 provisioning superuser 執行 meter（transaction 照樣設 `app.organization_id`）：A 的預留以 B 的身分結算是 `ErrNotFound`、總數不動、A 的預留仍是 reserved（D9）；以 superuser 為 A 預留只動 A 的日、不動 B 的（F1），而且 `ai_spend.reserve` event 的 `organization_day_committed_nano_usd` 是 A 的日（F4）；`TotalsAt` 讀各自的日（F2）；B 自己的結算只動 B 的日（F5）。金額每次 run 隨機（見「Mutation」）。本報告的兩句（「Mutation」一節與 coordinator 一節）以刪除線標出並更正。
- **先紅後綠**：這是對既有、正確的 filter 補測試，所以在目前的程式上本來就通過；紅是 mutant：reviewer 的 `filter-mutants-existing-tests.log`（舊的 5 個 DB 測試，D9、F1、F2、F3 都存活）；mutation 第 2 次：D9、F1、F2、F4、F5 都被 `TestOrganizationFilter` 擋下，F3 是等價的（同一 transaction 中有 organization 條件的 `SELECT … FOR UPDATE` 已經找到這個 organization 的預留，ID 唯一；reviewer 的 I3 同意不需要測試）。Probe P1 以只改了建構方式的複本執行（`probes-p1-p2-adapted.log`）：PASS。

### M2（Medium）：價格假設 global inference，但 request 沒有指定

- **查證**：直接取了官方頁面（WebFetch，2026-10-26；reviewer 的 `official-docs-excerpts.md` 與它一致）：https://platform.claude.com/docs/en/manage-claude/data-residency ：`inference_geo` 只有 `"global"`（預設）與 `"us"`（"Inference runs only in US-based infrastructure"）；"`default_inference_geo`: Sets the fallback geo when `inference_geo` is omitted from a request"；"`allowed_inference_geos`: Restricts which geos a workspace can use. If a request specifies an `inference_geo` not in this list, the API returns an error"；舊的 US-only opt-out 已改為 `allowed_inference_geos: ["us"]`、`default_inference_geo: "us"`；"The response `usage` object includes an `inference_geo` field indicating where inference ran"；"Claude 4.6 and later models: US-only inference (`inference_geo: "us"`) is priced at 1.1x the standard rate across all token pricing categories (input tokens, output tokens, cache writes, and cache reads)"；"Global routing (`inference_geo: "global"`): Standard pricing applies."。https://platform.claude.com/docs/en/about-claude/pricing 的 Data residency pricing 同樣是 1.1 倍；Opus 5.5 那一列仍是 $4／$5／$8／$0.20／$20。SDK v1.78.0：request 的 `MessageNewParams.InferenceGeo`（`param.Opt[string]`，`message.go:14081`），回答的 `Usage.InferenceGeo`（`string`，`message.go:12519`）。
- **修正**（coordinator 的決定：不在 request 中指定，那是 owner 的資料駐留選擇）：request 仍不送 `inference_geo`（`TestTheRequestNamesNoInferenceGeo` 固定這一點並寫明理由）。`internal/ai/spend` 加 `Geo`（只認 `global`、`us`，其他一律 `unknown`；provider 的原文不往下傳）、`AtGeo`（global 1.0、us 1.1、unknown 1.1，以 nano-USD 無條件進位）、`AtDearestGeo`。預留：每個 attempt 的上限 `AtDearestGeo(AttemptBound)`；結算：回答的 usage 以 `AtGeo(cost, Geo(usage.inference_geo))` 計；沒有回答的 attempt 仍以（1.1 倍的）attempt 上限計。沒有或不認得的 geo：metadata `inference_geo: unknown`、worker 的 WARN log（`inference_geo`，log key 登記為 token 形狀）、`ai_spend.settle` 的 `inference_geo`（audit catalogue 只接受 `global`／`us`／`unknown`）、metric `geminus.ai.spend.warnings`（`geminus.ai.warning`=`inference_geo_unknown`）；eval 的 report 有 `inference_geos` 與 `spend_warnings`。新增 **D-116（PROPOSED）**：是否指定、指定哪一個，以及兩個 organization 的 workspace 設定是否相同；D-110 (a) 的文字改為現在的做法。
- **先紅後綠**：`red-m3-i1-m2-adapter.log`（exit 1）：`TestTheAnswerIsPricedAtItsInferenceGeo` 中 `us`、沒有、空字串、`eu`、`GLOBAL` 都以牌價結算（1,080,000，want 1,188,000），預留 563,160,000（want 619,476,000）；`TestTheRequestNamesNoInferenceGeo` 在修改前就通過（固定現狀）。`red-m2-m3-provider.log`（exit 1）：`TestTheReservationAssumesTheDearestGeo`（1,091,520,000，want 1,200,672,000）。修改後 `green-m3-i1-m2-adapter.log`、`green-m2-m3-provider.log` exit 0；另有 `TestInferenceGeos`（spend）、`TestTheMetadataNamesThePricedGeo`、`TestSettlement` 的 us／沒有 geo／未知 geo 三個 case（provider）。Probe P7 原樣執行（`probes-p4-p5-p7.log`）：**FAIL，依決定**：它要求 request 指定 `global`，coordinator 決定不指定；由 `TestTheRequestNamesNoInferenceGeo` 與 D-116 取代。
- **擋下的測試**：G1（預留以牌價）、G2（結算不看 geo）、G3（未知 geo 以牌價）、G4（`Geo` 傳出 provider 的原文）、G5（adapter 丟掉 `usage.inference_geo`）、G6（request 指定 global）、G7（進位改為捨去）、G8（generator 不警告未知 geo）都被擋下（`TestTheReservationAssumesTheDearestGeo`、`TestTheAnswerIsPricedAtItsInferenceGeo`、`TestSettlement`、`TestInferenceGeos`、`TestTheMetadataNamesThePricedGeo`、`TestTheRequestNamesNoInferenceGeo`、`TestTheSpendLines`）；ledger 端 O4（未知 geo 不計 counter）被 `TestASettlementAboveItsReservationIsRecordedAndFlagged`。

### M3（Medium）：failover 跟在沒有回答的 attempt 之後時，「至多 1 + MaxRetries 個 attempt」不成立

- **發現**：SDK 在 A 上重試斷線（計為可能已計費），A 最後一個 attempt 回 401／402／403 時 adapter 換到 B，B 再有 1 + MaxRetries 個 attempt。Probe P4：A `hangUp, hangUp, 402`、B `hangUp ×3`：6 次、5 個沒有回答，ledger 結算 USD 0.56316（3 × 0.18772，clamp 到預留）而可能是 USD 0.9386。
- **修正**（coordinator 的決定）：adapter 在每把 credential 的呼叫之前記下 tally；回 401／402／403 時，若這把 credential 上的 attempt 有任何一個可能已被計費（斷線、逾時、body 讀不完、讀不了的 2xx），照樣標記 credential（401／403 停用、402 休息 15 分鐘），但**不換到下一把**，回 `unavailable`（帶 status 與 label，可重試）；下一次呼叫（自己的預留）就用下一把。每個 attempt 都是明確的錯誤 status（現行分類：429、408、409、5xx 由 SDK 重試，回錯誤 status 視為不計費；401／402／403 換 credential）時仍 failover。所以可能被計費的 attempt 都在同一把 credential 上，至多 1 + MaxRetries 個，與預留相符。**拿掉 clamp**：結算是可能被計費的金額，即使超過預留也照記，不少記；超過預留時 metadata `spend_over_reservation`、`ai_spend.settle` 的 `over_reservation: true`、metric `geminus.ai.spend.warnings`（`over_reservation`）、worker 的 WARN log。D-111 (a)、(b) 的文字改為現在的做法。
- **先紅後綠**：`red-m3-i1-m2-adapter.log`：`TestNoFailoverAfterAnAttemptThatMayHaveBeenBilled` 的三個 subtest（P4 的序列；斷線後 401；body 截斷後 403）都換到了 B（例如 P4：A=3、B=3，結算 563,160,000）；`red-m2-m3-provider.log`：`TestASettlementAboveTheReservationIsRecorded`（9 個沒有回答的 attempt 被 clamp 到 18,000,000，want 59,400,000）。修改後都通過；P4 序列：A=3、B=0、`unavailable`、結算 2 × 206,492,000，下一次呼叫走 B（B 再斷三次：3 個 attempt，仍在 1 + MaxRetries 之內）；429、529、402 之後仍 failover 到 B。Probe P4 原樣執行：PASS（`probes-p4-p5-p7.log`：A=3、B=0、unanswered 2）。
- **擋下的測試**：R1（可能已計費之後仍 failover）被 `TestNoFailoverAfterAnAttemptThatMayHaveBeenBilled`；R5（把 clamp 放回去）被 `TestASettlementAboveTheReservationIsRecorded`、`TestSettlement`；R6（metadata 不標示超過預留）被 `TestASettlementAboveTheReservationIsRecorded`；R7（generator 不警告）被 `TestTheSpendLines`。

### L1（Low）：超過預留的結算沒有任何訊號

- **發現**：結算以成本記錄、不 clamp（正確），但沒有 log 或 metric；probe P2：預留 600、organization 日上限 1000，結算 5000，日的總數變成 5000。Migration 0016 開頭「none can take a limit past its value」說得太滿。
- **修正**：照記（與 M3 一致），並標示：`ai_spend.settle` 的 `over_reservation: true`、`geminus.ai.spend.warnings`、metadata 與 WARN log（見 M3）。0016 已 push、不修改；開頭兩句的更正寫在 API_DATABASE §8 與本報告：「none can take a limit past its value」只對**預留**成立，超過預留的結算可使總數超過上限一次（下一個預留即被拒）；「The day of a reservation is the worker's UTC day」因 L3 不再成立。
- **先紅後綠**：`red-l1-l3-ledger.log`（exit 1）：`TestASettlementAboveItsReservationIsRecordedAndFlagged`：settle event 0 個有 `over_reservation`。修改後通過（`green-l1-l3-ledger.log`；最後的版本另以安裝的 metric reader 檢查 counter 與 `inference_geo`，在 `t053-db-tests.log` 中 PASS）。Probe P2 以複本執行（`probes-p1-p2-adapted.log`）：仍 FAIL，**依決定**：它要求超過上限的結算被拒，而決定是照記並標示（reviewer 也寫了「P2 FAIL by design」）。
- **擋下的測試**：O1（event 不標示）、O2（不計 counter）、O3（拒絕超過預留的結算，少記）、D3 都被 `TestASettlementAboveItsReservationIsRecordedAndFlagged` 擋下。O5（ledger 自己對 geo 的檢查拿掉）是等價的：audit catalogue 的 `oneOf("global", "us", "unknown")` 在同一 transaction 中拒絕那筆 `ai_spend.settle` event，transaction rollback，結算照樣失敗、什麼都沒寫（mutation 第 1 次：SURVIVED；第 2 次標為 equivalent）。

### L2（Low）：兩把 key 以原始字串比較

- **修正**：`config.loadKeys` 在比較之前拒絕前後有空白（`unicode.IsSpace`）或任何位置有 control character（C0、DEL、C1，`unicode.IsControl`）或不是 UTF-8 的 key：`GEMINUS_ANTHROPIC_API_KEY_A|B:invalid`，訊息只說變數。D-115 的文字加上這一點。
- **先紅後綠**：`red-l2-config.log`（exit 1）：`TestAKeyWithWhiteSpaceOrControlCharactersIsRefused` 的 12 個 subtest 都失敗（P6 的三種，加 `\r\n`、tab、no-break space、A 的換行、`\x00`、`\x1b`、DEL、`\u0085`、兩把不同的 key 中一把帶空白）；修改後 `green-l2-config.log` exit 0。Probe P6 原樣執行：PASS（`probes-p6.log`）。
- **擋下的測試**：W1（不檢查）、W2（只檢查尾端）、W3（不檢查內部的 control character）。

### L3（Low）：worker 的時鐘決定日與月

- **修正**：`aispend.Meter` 沒有時鐘了（`Config.Now` 拿掉）：預留的 transaction 第一個查詢 `SELECT (now() AT TIME ZONE 'UTC')::date`，月由這個日算出；結算本來就用預留自己的日。測試以 `export_test.go` 的 `PinDay`（只編進這個 package 的測試）指定自己的日；production 沒有方法指定。Generator 的 DB 測試（`TestAMeteredProposal`）不再固定日，改為檢查預留在 database 的 UTC 日。
- **先紅後綠**：`red-l1-l3-ledger.log`：`TestTheDatabaseClockPicksTheDay` 的三個時區 subtest 都失敗（meter 的時鐘設在 2299-12-31，預留就在 2299-12-31）；紅時的測試檔存為 `red-l3-clock_test.go.txt`。修改後 `Config.Now` 已不存在、無法再設一個偏差的時鐘，所以綠的版本改為：不指定日的 meter 在 session 時區 UTC、UTC+14、UTC−12 下預留都在 database 的 UTC 日，該 organization 沒有任何列在別的日（assertion 的意思不變；`green-l1-l3-ledger.log`、`t053-db-tests.log`）。
- **擋下的測試**：C1（以 session 時區的日）被三個時區的 subtest 擋下；C2（不指定時用零值的日）被 `TestTheDatabaseClockPicksTheDay` 與 `TestAMeteredProposal`。「以 Go 的時鐘取日」的 mutant 在同一台機器上與 database 的時鐘相同，任何測試都分辨不出，而程式中已沒有 Go 時鐘可以改，所以沒有列入。

### L4（Low）：`refusals.sh` 仍說不可能有 live 呼叫

- **修正**：沒有改它的邏輯；開頭加上說明與 `exit 2`：它是 5a202cc 上拒絕路徑那一次執行的紀錄，H-026 之後第 4、5 步會建立 live backend 並送 request 到 production API（假 key 回 401）、寫 live report，所以不得再執行；拒絕路徑改由 process 內的測試涵蓋。執行它只會印出那段說明並 exit 2（已確認）。

### L5（Low）：過時的敘述

- `checks/T-053_VALIDATION.md`：開頭一節加更正說明；稽核表 2a、3 列的「`Retention` 空白」改為 H-026 前後的現況；NOT RUN 表 live smoke 那一列改為「H-026 之後指令會呼叫 production API」。
- `internal/aievals/live_test.go` 開頭的註解（原「This build cannot turn live AI on」）。
- `docs/CONTRACT_BLOCKERS.md` CB-36、`docs/SECURITY.md` §7 第一句（原「該清單是空的」）。

### Info

- **I1**：讀不了的 2xx 回答：adapter 記錄最後一個 body 的結束方式（讀完、截斷）；非 API error 而 raw response 是 2xx、body 不是截斷的，就計為一個沒有回答的 attempt 並標 `Undecoded`，結算至少為整個預留（coordinator 的決定）。`TestAnUndecodableAnswerSettlesAtTheReservation`（MaxRetries 0 與 2：結算＝預留；紅：結算 0）、`TestSettlement` 的 case；截斷的 body 不會被算兩次（`TestACutBodyOnTheLastAttemptCountsOnce`）。Probe P5 原樣執行：PASS。Mutant R2、R3、R4。
- **I2**（未結算的預留沒有 operator 介面）、**I4**（限額是每個 process 的設定）：沒有改程式；寫入 D-111 的已知限制，並列入 T-039（部署）的 scope：每個 worker 以相同的限額部署或把限額移到資料庫、列出未結算預留的介面。
- **I3**：F3 是等價的 mutant（見 M1）。
- **I5**：API 在開關 on 時也會讀兩把 key（`config.Load` → `LoadAI`）：寫入 SECURITY §7（部署 API 時不設 key 的變數與開關）與 T-039 的 scope。
- **I6**：`MemoryMeter.Settle` 只在荒謬的 usage（約 4.6e14 token）下才會溢位；沒有改。

### 改變的期望值（依 coordinator 的決定，沒有降低）

- 每個 attempt 的預留上限是 1.1 倍（M2）：provider 的 `TestTheReservationBoundsTheCall`（3 × ⌈1.1 × bound⌉）、`TestSettlement` 的 `per`、anthropic 測試的 `perAttempt`；aievals `TestTheCapStopsTheRun` 的算式註解（結果不變）。
- 測試的 backend 回報 `inference_geo: global`，因為真的 API 每個回答都有這個欄位（本機 server 的回答、`billed`／`inProcess`／`billedFake`）；沒有 geo 的回答另有測試（以 1.1 倍計）。
- `TestSettlement`「more lost attempts than reserved for」：結算 9 個 attempt 的上限，不再是 clamp 後的 3 個（M3）。原本擋下 U8（「結算可超過預留」）的這個 assertion 現在要求相反的行為，U8 因此退役，由 R5（把 clamp 放回去）取代。
- Generator 的 `TestAMeteredProposal`：預留在 database 的 UTC 日，不再是固定的 2210-02-03（L3）。
- Live eval 的 report 名稱與 record（H1）：`TestTheReportIsApart`、`TestTheLiveEvalOverHTTP`、`TestTheLiveEvalRunsOnlyWhenLiveIsOn`（report 目錄先放空的 record；檔案數為 report 加 record；高於月上限的上限現在先被日上限拒絕，訊息為「above the deployment's daily limit」）。
- U42（「上限可超過月上限」）的那一行程式已拿掉（見 H1），U42 退役，由 K1、K3 取代。

### Reviewer 的 probe（暫時複製回 package，執行後刪除；`git status` 沒有留下）

| Probe | 結果 | 說明 |
|---|---|---|
| P1（superuser 的 organization filter） | 原樣：不能 build（`Config.Now` 已拿掉，L3）；改用 `PinDay` 的複本（`probe-p1-p2-adapted_test.go.txt`）：PASS | 由 `TestOrganizationFilter` 取代並擴充（F4、F5） |
| P2（超過預留的結算） | 複本：FAIL（依決定） | 決定是照記並標示；由 `TestASettlementAboveItsReservationIsRecordedAndFlagged` 取代 |
| P3（run 的上限 ≤ 日上限） | PASS | `probes-p3.log` |
| P4（failover 之前沒有回答的 attempt） | PASS | A=3、B=0、unanswered 2 |
| P5（讀不了的 200） | PASS | 結算 0.206492（＝預留，MaxRetries 0） |
| P6（同一把 key 加空白） | PASS | |
| P7（request 指定 `inference_geo: global`） | FAIL（依決定） | 不指定；由 `TestTheRequestNamesNoInferenceGeo` 與 D-116 取代 |

### Mutation

`checks/evidence/2026-10-26-T-053-r1/mutants.py`：作者的 harness 加上本輪的 mutant，在 `git archive HEAD` 的複本上執行（拒絕在有 `.git` 的目錄執行），DB mutant 在全新、已 `make db-test` 的 `dt_test_*` 上以 `GEMINUS_REQUIRE_DB_TESTS=1` 執行（沒有 DB 時不跑、記為 ERROR，不會記為 killed），每個檔案還原後比對 hash。作者的 U1–U46、D1–D9 改為修正後的程式的文字（U8、U42 退役，見上；D6 改為以 `time.Now()`），新增 F1–F5（M1）、C1–C2（L3）、O1–O5（L1）、G1–G8（M2）、R1–R7（M3、I1）、K1–K15（H1、L6）、W1–W3（L2）。

- **第 1 次**（2cf81fe，`mutants-run1.log`，exit 1）：98 個中 93 killed、F3 equivalent；G8、R1 INVALID（mutant 的文字讓一個 import 或變數沒有被使用，是 harness 的錯，已改）；F4 SURVIVED：`TestOrganizationFilter` 用固定的金額，而同一個固定的日上累積了之前每次 run 的 organization 的列，不帶 filter 的讀取恰好讀到之前某次 run 的 A，金額相同；改為每次 run 隨機的金額（ea9a85a）。O5 SURVIVED，是等價的（見 L1），改標 equivalent。
- **第 2 次**（ea9a85a，全新的 `dt_test_t053f3`，`mutants-run2.log`，exit 0）：**98 個中 96 killed、2 個 equivalent（F3、O5），沒有 SURVIVED、INVALID 或 ERROR**。F4 被 `TestOrganizationFilter` 擋下（金額改為每次 run 自己的之後），G8 被 `TestTheSpendLines`、R1 被 `TestNoFailoverAfterAnAttemptThatMayHaveBeenBilled` 擋下（mutant 的文字改正之後）。作者的 44 個 unit mutant（U1–U46 去掉退役的 U8、U42）與 9 個 DB mutant（D1–D9）也都被擋下：D9 現在由 `TestOrganizationFilter` 擋下（上一輪 coordinator 的 DB mutant 執行中 D9 存活）。每個 mutant 擋下的測試列在 log 的第 6 欄。

### 實際執行（本輪；log 在 `checks/evidence/2026-10-26-T-053-r1/`，最後一行是 exit code）

| 指令 | Exit | 說明 |
|---|---:|---|
| 先寫的測試，修改前（`red-m3-i1-m2-adapter.log`、`red-m2-m3-provider.log`、`red-l2-config.log`、`red-h1-report-overwrite.log`、`red-h1-l6-main.log`、`red-l1-l3-ledger.log`） | 1（六個都是） | 見各項 |
| 同一組測試，修改後（`green-*.log`，六個） | 0（六個都是） | |
| `make backend-test GOFLAGS=-count=1` | 0 | `backend-test.log`：gofmt、go vet、74 個 package ok，沒有 `(cached)`；DB-backed 測試 skip（它們在下面的 DB 執行中 PASS） |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `spec-check.log`（本節寫好之後執行）：`PASS_TS_CONTRACTS_FRESH`、`PASS_INDEPENDENT_HASHES`、`PASS_DB_TESTS_FRESH`；`decisions-check.json`（D-116）、`planning-check.json`、`docs-check.json` 重產 |
| `make ai-eval` | 0 | `ai-eval.log`；`checks/ai-evals/fake-*.json` 沒有改變 |
| `make architecture-check` | 0 | `architecture-check.log`：86 packages、23 rules、16 build configurations |
| govulncheck v1.9.0（go1.27.3） | 0 | `govulncheck.log`：No vulnerabilities found |
| `go mod verify` | 0 | `go-mod-verify.log`；go.mod／go.sum 沒有改變 |
| `make db-test`（全新 `dt_test_t053f4`） | 0 | `db-test.log` |
| T-053 的 DB 測試（舊的 7 個加新的 3 個；`-race -v`，`GEMINUS_REQUIRE_DB_TESTS=1`，`dt_test_t053f4`） | 0 | `t053-db-tests.log`：10 個都 PASS |
| `go test -race -count=1 -p 1`，改到的 package（`internal/ai/...`、`config/...`、`aievals/...`、`storage/aispend/...`、`proposals/...`、`audit/...`、`observability/...`、`cmd/worker/...`、`cmd/ai-eval-live/...`；DB 測試在 `dt_test_t053f4` 上） | 0 | `race-changed.log`：21 個 package ok |
| `make integration-test`（全新 `dt_test_t053f5`，PostgreSQL 16.15） | 0 | `integration-test.log`：db-test 之後 db-go-test 的 35 個 package ok，沒有 FAIL |
| Mutation 第 1 次、第 2 次 | 1、0 | 見「Mutation」（第 2 次：96 killed、2 equivalent） |
| Reviewer 的 probe | 見上表 | `probes-*.log` |
| `refusals.sh`（L4 的 guard） | 2 | 只印說明，沒有執行任何步驟 |

### NOT RUN（本輪）

- **`make ai-smoke-live`、`make ai-eval-live` 對真的 API**：依指示不做任何 live 呼叫；也沒有以乾淨環境執行它們的拒絕路徑（拒絕路徑由 process 內的測試涵蓋，避免任何可能讀到 session 環境的執行）。
- **全套 `make backend-race`**：只對改到的 package 做了 race（上表）。
- **`make frontend-test`、`make e2e-test`**：沒有改 UI、HTTP contract、schema 或 TS 型別（spec-check 的 `PASS_TS_CONTRACTS_FRESH`）。
- **SQL mutant**：migration 0016 沒有改；reviewer 的 7 個 SQL mutant 在 a0fb365 都被擋下（`reviewer/sql-mutants.log`）。

### 準備好的 live 指令（review 第 1 輪之後；NOT RUN）

在 review 接受本輪之後、有兩把 key 的 session 中，在同一個 checkout 依序執行，**每次 run 之後 commit `checks/ai-evals/`（record 與 report）**：

```sh
# 一個 synthetic case（MOD-002），上限 USD 2
AI_PROVIDER=anthropic AI_POLICY_APPROVAL=H-018 AI_MODEL=claude-opus-5-5 GEMINUS_AI_LIVE=on make ai-smoke-live
# 50 個 modeler case。同一個 UTC 日在 smoke 之後，預設的 USD 10 上限（＝日上限）放不下：
# 上限須 ≤ 10 − smoke 的花費，例如 9；或在另一個 UTC 日以預設上限執行。
AI_EVAL_LIVE_MAX_USD=9 AI_PROVIDER=anthropic AI_POLICY_APPROVAL=H-018 AI_MODEL=claude-opus-5-5 GEMINUS_AI_LIVE=on make ai-eval-live
```

指令的 exit code：0 完整且 gate 為 0；1 gate 失敗或達上限停止；2 拒絕、沒有呼叫、沒有寫檔（上限超過日上限或 record 留下的額度、record 不能用、lock 存在等）；3 已有呼叫而錯誤停止或 report／close line 寫不進去（summary 與花費在 stdout）。預留以 1.1 倍計，所以同樣的上限能放的呼叫比上一輪少約一成；結算依回答的 geo。

### 需要 owner 決定（本輪的變化）

- **D-116（新，PROPOSED）**：live request 是否指定 `inference_geo`、指定哪一個（A：不指定，已實作；B：`global`；C：`us`），以及兩個 provider organization 的 workspace 的 `default_inference_geo`／`allowed_inference_geos` 是否相同。A 不論設定都不會少記，所以不擋 H-025 的 live run。
- **D-114 (b)**：部署第一個開啟 live 的 worker 時，eval 的花費保持記在 record（與 worker 的 DB ledger 分開，合計由 Console spend limit 限制），或改為經 DB ledger（選項 B）。
- 其他不變：D-110–D-116 都待 owner 批准（文字已改為現在的做法）；H-025 的 Console spend limit 的數值不在 repository 中，H1 的缺口以它為合計的上限。

### 殘餘風險（本輪的變化）

- **Eval 與 worker 的花費分開計**：兩者都開啟時，app 內沒有合計的上限，只有 Console spend limit（D-114 (b)）。
- **Record 是每個 checkout 各自的**：兩個 checkout 同時執行、或執行後沒有 commit，下一個 checkout 看不到前一次的花費；lock 只防止同一個 checkout 中兩個 run 同時開始。Run 在取得 lock 與寫完 open line 之間當掉會留下 lock，之後每個 run 都被拒，直到 operator 移除它（fail closed）。
- **預留多一成**：在 owner 回答 D-116 之前，上限比 global 早 10% 拒絕。
- **讀不了的 2xx 以整個預留計**：可能多記（安全側）。
- **等價的 mutant**：F3、O5（理由見上）；Go 時鐘的 mutant 在同一台機器上無法分辨（程式中已沒有 Go 時鐘）。
- **DB 不檢查預留的日**：日由 Go 在 transaction 中從 `now()` 取得，DB 本身沒有 CHECK（測試要指定自己的日）；一個被改壞的 worker 仍能寫任何日，這與 worker 本來就能寫任何金額相同。

### Coordinator 的執行（第 1 輪修正，df7c091）

Coordinator 把 `t053-r1` 的四個 commit（d28eb79、2cf81fe、ea9a85a、e9a8a85）cherry-pick 到 `claude/lucid-meitner-vq1awo`（245a375 之後，成為 1ad02c4、a30004c、d9e3af3、df7c091）。衝突只在重產的 `checks/docs-check.json`（取一邊後由 `make spec-check` 重產）；`planning/decisions.json` 與決策登記檔自動合併（H-028 與 D-116 都保留）。

在整合後的 tree 上（全新的一次性資料庫；共用的 PostgreSQL 16.15 cluster 沒有重啟；`checks/evidence/2026-10-26-T-053-r1/coordinator/`）：

| 項目 | 結果 |
|---|---|
| `make spec-check`、`backend-test`、`architecture-check`、`ai-eval` | 全部 exit 0 |
| govulncheck v1.9.0（go1.27.3） | exit 0，No vulnerabilities found |
| `make integration-test`（全新 `dt_test_t053r1c1`） | exit 0 |
| `make db-test`（全新 `dt_test_t053r1c2`） | exit 0 |
| T-053 的 10 個 DB 測試（`-race -v`，`GEMINUS_REQUIRE_DB_TESTS=1`，含新的 `TestOrganizationFilter`、`TestASettlementAboveItsReservationIsRecordedAndFlagged`、`TestTheDatabaseClockPicksTheDay`） | 全部 PASS |
| `mutants.py`（本輪的 harness，`git archive HEAD` 複本，全新 `dt_test_t053r1c3`） | exit 0：98 個中 96 killed、2 個 equivalent（F3、O5），沒有 SURVIVED、INVALID 或 ERROR |
| `make backend-race`（全部套件） | exit 0 |

- 執行開始時唯一未 commit 的檔案是重產的 `checks/docs-check.json`（`environment.txt`）。三個一次性資料庫用完即刪。
- 證據中沒有 key（以 `sk-ant-` 樣式搜尋，只有既有的 `refusals.sh` 中的假 key）。
- **NOT RUN**：`make ai-smoke-live`、`make ai-eval-live`（依 H-025，第 2 輪獨立 review 接受之後才以 synthetic 資料執行）；`make frontend-test`、`make e2e-test`（本輪沒有改 UI 或 contract；同一 tree 的 UI 在 245a375 的 CI 全過）。
- 本任務仍待：第 2 輪獨立 review（H-003）；owner 批准 D-110–D-116（PROPOSED）。

## Review 第 2 輪（d150918）：ACCEPT

獨立 reviewer（H-003）審查 `git diff 245a375 d150918`：**ACCEPT**，沒有 High 或 Medium；第 1 輪的每一項都已依決定修正（P1、P3–P6 通過；P2、P7 依決定改變後的預期失敗；reviewer 自己的 probe Q1、Q2 與 11 個 mutant）。報告與證據：`checks/evidence/2026-10-26-T-053-r2/reviewer/`（1fba2cd）。Reviewer 重新查了 Anthropic 的 data residency、pricing 與 errors 頁：1.1 倍與價格表相符。

- **新的 Low（記為後續，H-029 的 hackathon 模式下延到比賽後，見 `planning/tasks.json` T-053）**：L-new-1 沒有結束紀錄的 run 只計入開始的那一天；L-new-2 花費紀錄被清空、未合併或分支衝突時無法察覺（每次 run 仍在自己的上限內）；L-new-3 504 `timeout_error` 被當作未收費；L-new-4 D-116（inference_geo）是需要 owner 回答的問題，依 H-025「需要 owner 確認的事項出現時 live 保持關閉並先詢問」，Anthropic live 執行前先請 owner 回答。
- **Info**：每日上限可設到月上限（D-110 批准時決定）；`-dir` 可指向任何位置；usage 全為 0 的回答結算為 0；D-110 文字中 Console spend limit 回 400 而非 402（adapter 的處理在安全的一邊，只有文字錯）；coordinator 的 backend-test／backend-race log 中 29 個套件是 cached 結果（reviewer 以 `-count=1` 重跑，74 個套件全過）；N3、N7、N8 三個 mutant 存活（目前正確的程式缺測試）；送不出去的 transport 失敗每次嘗試仍以上限計入 in-code ledger（沒有實際花費）。
- **Live 執行的前提**（reviewer 列出，執行時照做）：先回答 D-116；在主要的 checkout 中一次只跑一個 run，每次 run 後先 commit `checks/ai-evals/`；只用預設上限、不用 `-dir`；先跑 smoke（預設 2 美元），沒有任何 warning 才跑 eval；eval 的上限設為 10 減去 smoke 印出的 committed 金額（無條件捨去到分），或隔一個 UTC 日再跑；key 與開關只放在 live 指令自己的環境；跑完以 `grep -r 'sk-ant-' checks/ai-evals` 確認沒有外洩；出現 `over_reservation` 或 `inference_geo_unknown` warning 就停止並回報。
- **狀態**：程式已被 review 接受；T-053 標為 done 之前仍需 owner 批准 D-110–D-116（PROPOSED，第七張決策頁）。依 H-029，第七張決策頁與 Anthropic live 執行延到比賽後；比賽期間的 live AI 走 Meta provider（T-055），沿用本任務的上限與開關。
