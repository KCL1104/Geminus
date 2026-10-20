# T-025 驗證報告：Async proposal generation／validation

**日期：2026-10-20｜狀態：待獨立 review；`planning/tasks.json` 的狀態未改，由 reviewer 決定。｜需求：REQ-008、REQ-009｜Gate：D-002、D-014、D-016、D-020、D-022、D-027、D-046 都已 APPROVED；本任務提出的 D-061、D-062 是 **PROPOSED**，`approved_by`／`approved_at` 為 null，T-025 依建議實作，T-025 與 T-026 都以它們為 gate。｜驗收測試：TEST-003、TEST-006、TEST-007｜DB 測試與 `make integration-test` 為 NOT RUN（見「NOT RUN」）。**

範圍依 tasks.json：

1. durable generating job → proposed content → server validator／risk → ready；
2. needs_input／invalid／failed 狀態與 immutable ready content；
3. server 綁定所有 identity、validation、approval 欄位，沒有 write tool；
4. AI proposal 的 job handler 是 adapter，不在 `internal/ai`；另立 package 並記錄名稱（T-019 round 2 L3）。

另依 coordinator 的指示：proposal 跟隨它的 job，與 run（0009）、import（0010）的做法相同，每個 proposal 最多一個 generating job；CB-40 中本任務需要的項目（pool 違規的 issue code、目錄中的 `AI_OUTPUT_INVALID`、provider 錯誤對應的狀態與 HTTP）以 PROPOSED decision 與契約變更補上，不批准任何東西。

只用 deterministic fake provider（`internal/ai/provider/fake`）。**沒有任何 live AI 呼叫**，沒有讀取或設定任何 API key（H-001、H-017）。逐條輸出在 `checks/evidence/2026-10-20-T-025/`：`exit-codes.txt`、`environment.txt`、各指令的 log、`db-not-run.txt`（su 被拒的原文）。

## Commit

| Commit | 內容 |
|---|---|
| cba0393 | `internal/ai/review`：server 對 provider 回答的 review 與 server risk；`ValidationCode` 新增 `ID_POOL_VIOLATION`、`AI_OUTPUT_INVALID`（schema、產生的 TS、en／zh-Hant 字串） |
| 7460dc9 | 契約：`proposal.schema.json` 規定 invalid 必須帶 `validation.valid=false` 且沒有 changes，只有 failed 帶 `error`；fixtures 與 cases；OpenAPI 的 202 與五種狀態範例；`contracts.test.ts` 的正反例 |
| 7f15754 | migration 0012：proposal 跟隨它的 job（三個 trigger、四個 CHECK、`output_locale`、`selected_entity_ids`）；`db/tests/proposal_jobs.sql`；`constraints.sql` 的 D-027 組合改由 worker 寫入 |
| 0ef352e | review 修正：expected effects 中 intent 的數字也不算（eval MOD-050）；先紅後綠 |
| 8a92888 | `internal/storage/proposals`、`internal/proposals`（service＋HTTP）、`internal/proposals/generator`（handler）、cmd/api 與 cmd/worker 的 AI 模式、architecture 規則與 probe |
| c006692 | `TestReviewEval`（`make ai-eval` 的 review suite，`checks/ai-evals/fake-review.json`）；Makefile 的 `db-go-test` 加入 `./internal/proposals/...` |
| 45a46df | 文件：D-061、D-062 PROPOSED；ARCHITECTURE、AI_SPEC、API_DATABASE、SECURITY、CB-40；tasks.json 記錄 package 名稱與 planned outputs（status 未改）；重產的 `checks/*.json` |
| 579706d | Architecture probe 改由 `internal/runs` 連結 generator（原 probe 形成 import cycle，見「先紅後綠」） |
| 本報告的 commit | 本報告、evidence、`checks/docs-check.json` |

## 稽核：scope 逐條

| Scope | 判定 | 證據／處理 |
|---|---|---|
| 1. generating job → content → server review → ready | 已完成；DB 部分 NOT RUN | `proposals.Service.Create` 依序檢查：role；Idempotency-Key（fingerprint 為 `{model_id, canonical body}`）；AI 是否可用（503，不寫入）；intent、locale、mode；鎖 model；version 屬於此 model（否則 422 `/base_version_id`）；是否為 latest（否則 409 `MODEL_VERSION_CONFLICT`）；server 產生 ID pool。之後 proposal、`model_proposal` job（payload 只有 proposal 與 request ID）、audit `proposal.create`、idempotency response 同一 transaction commit，回 202 `generating`。Handler：在 lease 下鎖 proposal、讀 base 與 snapshot keys；在 transaction 外建 context、呼叫 provider、`review.Review`、dataset binding stage；在 `Finish` 中重新鎖 proposal，確認仍是 generating，寫入結果與 audit `proposal.generate`。無 DB 的證據：`TestGenerateOutsideTheDatabase`、`internal/ai/review` 的 17 個 test、`TestReviewEval`。DB 的證據（NOT RUN）：`TestAReadyProposal`、`TestProposalClosedLoop` |
| 2. needs_input／invalid／failed、immutable ready | 已完成；DB 部分 NOT RUN | needs_input 只存 summary、assumptions、questions，沒有 patch、risk、validation。invalid 只存 issues（固定 message、JSON Pointer）與 provider metadata，不存回答的任何文字。provider 錯誤是 job failure，job 最終失敗時 trigger 把 proposal 轉為 failed 並帶 failure，API 以 ErrorEnvelope 顯示（D-061 (c)）。Ready 之後 review payload 不可改（0001 起），identity 不可改（0012）。`TestGenerated`、`TestProviderFailures`、`TestPublicMatchesTheContract`（每種狀態都依 schema 驗證）、`TestFailureErrors`；DB：`TestStatesFromAnswers`、`TestProviderFailuresFailTheProposal`、`proposal_jobs.sql`（NOT RUN） |
| 3. server 綁定欄位、沒有 write tool | 已完成 | proposal ID、organization、model、base、mode、created_by、ID pool、job 由 server 綁定；patch_hash 由 server 以 JCS 計算（D-019）；risk 為 max(server, claim)（D-027）；validation 由 server 產生。回答中帶 `approved`、`approved_by`、`proposal_id`、`requires_confirmation=false`，或 root／identity patch，都判為 invalid（`TestRefusesWhatTheSchemaRefuses`、`TestRefusesUnsafeChanges`；eval 中 `unsafe_answer_ready` 為 0、`server_bound_field_from_answer` 為 0）。Handler 不寫 model、draft、version；`internal/ai` 仍不依賴 storage 與 tenant（architecture rule 與 probe） |
| 4. Handler 是 adapter、package 名稱 | 已完成 | tasks.json 原規劃 `internal/ai/proposals/`，改為四個 package：`internal/ai/review`（純粹）、`internal/proposals`（service＋HTTP）、`internal/proposals/generator`（handler）、`internal/storage/proposals`（SQL）。理由記在 ARCHITECTURE §6：handler 不能放在 `internal/ai`，而且避免三個 package 都叫 proposals。`job-handlers-only-in-the-worker` 只讓 cmd/worker 連結 generator（probe：cmd/api、internal/runs）。Analysis 的 handler 依賴 T-029，留給 T-030 |
| 每個 proposal 最多一個 generating job | 已完成；DB NOT RUN | 0001 的 `jobs_one_proposal` unique index；`proposal_jobs.sql` 測第二個 job 被拒（NOT RUN） |
| CB-40 | PROPOSED | D-061 (a)–(e)：兩個 ValidationCode 已加入 schema、TS 與 i18n；錯誤對應在 `internal/proposals/public.go`；provider_metadata 存 `provider.Metadata`；`id_pool` 與 `output_locale` 不對 client 顯示 |
| 每個 API 變更有 OpenAPI＋範例＋TS 契約測試 | 契約與範例已完成；TS typecheck NOT RUN | OpenAPI 的 createProposal 202、getProposal 五種狀態範例；`testdata/contracts` 8 個新 fixture；`contracts.test.ts` 的 `@ts-expect-error` 例子。`gen_ts_contracts --check` 通過（產生的 TS 為最新），但 `make frontend-test` 因 EBADENGINE 無法執行 |
| 新的 AI context 有 redaction 與 injection 測試 | 沒有新的 context 種類 | Handler 只用 T-024 的 `aicontext.Proposal`。`selected_entity_ids` 與 `output_locale` 由 server 驗證後交給它，T-024 的 redaction／injection 測試與 eval gate 仍涵蓋。`TestFakeProviderEval` 照樣通過 |

## 實際執行結果

在 worktree 中執行（`environment.txt`：Go 1.27.2、Node 22.22.0、4 CPU；沒有 PostgreSQL；先 `go clean -testcache`）。

| 指令 | Exit | 說明 |
|---|---|---|
| `go test -count=1 ./internal/ai/...` | 0 | 8 個 package ok（含 `review`、`corpus`） |
| `make backend-test`（第一次） | 2 | `TestProbesAreCaught/the-proposal-service-links-the-generator` 形成 import cycle（`backend-test-run1-fail.log`）；579706d 修正 |
| `make backend-test` | 0 | gofmt、go vet、57 個 package ok；沒有 DB URL，DB-backed 測試 skip（NOT RUN） |
| `make backend-race` | 0 | 57 個 package ok（`-race`） |
| `make architecture-check` | 0 | 69 packages、15 rules、16 build configurations |
| `make ai-eval` | 0 | `TestFakeProviderEval`（`fake-provider.json` 未改變）與 `TestReviewEval`（新的 `fake-review.json`）。Review suite 的結果：50 個 case；30 個 reference proposal 都 ready，且只靠 server 達到 minimum_risk；20 個 needs_input 都沒有 change；20 個 unsafe 回答都 invalid；5 個 zero-tolerance gate 都是 0 |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 重產 `decisions-check.json`、`planning-check.json`（proposed 由 14 變 16；T-025 從 `ready_tasks` 移到 `next_after_review`，等 D-061、D-062），與 45a46df 一起 commit；`docs-check.json` 隨本報告 commit；TS contracts 與 db/tests 都是最新 |
| `go test -count=1 -v`（T-025 的 package 與 api、cmd/api、cmd/worker、corpus；`t025-go-tests.log`） | 0 | 47 個 top-level test PASS，0 FAIL；DB-backed 測試 SKIP（T-025 的 9 個：`TestAReadyProposal`、`TestStatesFromAnswers`、`TestProviderFailuresFailTheProposal`、`TestOrganizationFilter`、`TestProposalClosedLoop`、`TestProposalRoleMatrix`、`TestProposalRequestRefusals`、`TestProposalsNeedAI`、`TestProposalIdempotency`） |
| `npm --prefix apps/web ci` | 1 | `EBADENGINE`：需要 node ≥24.21.0 <25，這裡是 22.22.0。沒有繞過 engine-strict |
| `make frontend-test` | 2 | 沒有安裝 dependencies，無法執行（NOT RUN） |
| `su postgres -c 'id'` | 被工具拒絕 | 原文在 `db-not-run.txt`。依指示不跑任何 DB 測試，沒有改用其他方式 |

## 測試涵蓋

| 測試 | 驗證 | 驗收 |
|---|---|---|
| `internal/ai/review`（17 個） | root、identity、move／copy、escape、P1 distribution 的 patch 被拒，第二個 op 失敗時整個 patch 不套用；scenario allowlist；新 ID 必須來自同種類 pool（固定 message，不引用 ID）；server risk 的每條規則（刪除、改 type、降 capacity、requirements、改接、failure／rework／cycle 上的 probability、新 cycle、新增或刪除 entity、constraint）與 max(server, claim)；no-op；warning 保留；expected effects 的數字；needs_input；output 形狀；issue 上限 1000；不合格的 input | TEST-003、D-016、D-020–D-022、D-027、D-046 |
| `TestReviewEval`（`make ai-eval`） | 整個 modeler corpus 經 context、fake、client、review。Golden reference 都 ready，semantic effect 與 business mutation 數相符，patch hash 由 server 計算，server risk 達到 floor（回答一律宣稱 low），宣稱 high 時 risk 為 high。Needs_input 不留 change。20 個 unsafe 回答都 invalid，且不保留回答內容：0 台、150%、2.5、負值、三角分布順序、捏造 dataset、捏造 resource（`ID_POOL_VIOLATION`）、lognormal、不修參照的刪除、scenario 刪 node、`approved`、`requires_confirmation=false`、`approved_by`、root replace、`model_id`、`organization_id`、捏造的 1000 次 simulation 與 11% | REQ-008、AI_SPEC §5、§10 |
| `TestGenerateOutsideTheDatabase` | 不用 DB 跑 handler 的中段：ready（帶 metadata）；fake 預設的 needs_input；多出 member → invalid `AI_OUTPUT_INVALID` 且沒有 changes；未綁定的 dataset → invalid `DATASET_BINDING_INVALID`（`/nodes/1/service_time/distribution/samples_ref`，不讀 object storage）；rate limited → 可重試的 `AI_RATE_LIMITED`；Disabled → 不重試的 `AI_POLICY_NOT_APPROVED`；壞的 pool → `PROPOSAL_INPUT_INVALID`；取消時只回 context 的錯誤 | AI_SPEC §4、D-006、D-033 |
| `TestProviderFailures`、`TestFailureErrors`、`TestGenerated`、`TestAuditMeta`、`TestNew` | 12 個 provider code 各有固定 message（不含 credential label 或 status），只有 4 個可重試，API 顯示契約內的 ErrorCode 與原 code；每種結果的 row 內容；audit 只有 metadata 的 13 個 key；設定檢查 | D-061 (c)、SECURITY §9 |
| `TestRoutesMatchContract`、`TestRefused`、`TestPublicMatchesTheContract`、`TestCursor`（`internal/proposals`） | routes 與 `planning/api-operations.json` 的 roles 相同；service error 對應的 code；每種狀態的 resource 依 `proposal.schema.json` 驗證；cursor 綁定 organization、model 與 list | D-012、D-025 |
| `TestAIAvailability`、`TestTheProviderIsTheConfigured`、`TestTheBuildRunsSimulations` | API 與 worker 的 AI 模式由 config 決定（fake、已核准的 live、未核准、未設定）；worker 登錄三個 handler | AI_SPEC §2 |
| `TestGenerateOnlyGenerates` | storage 的 `Generate` 只寫 ready、needs_input、invalid | 0012 |
| DB（NOT RUN）：`TestAReadyProposal`、`TestStatesFromAnswers`、`TestProviderFailuresFailTheProposal`、`TestOrganizationFilter`、`TestProposalClosedLoop`、`TestProposalRoleMatrix`、`TestProposalRequestRefusals`、`TestProposalsNeedAI`、`TestProposalIdempotency`、`db/tests/proposal_jobs.sql`、`constraints.sql` 的 D-027 段 | closed loop 中 model、draft、version 不變；每種回答的狀態；provider failure 的重試與每個 attempt 的 audit；application 的 organization filter（以不受 RLS 限制的 superuser 測）；role 矩陣；refusal 不寫入；AI 關閉時 503；12 個並行 retry 只得一個 proposal 與 job；0012 的 trigger 與 CHECK | TEST-006、TEST-007、TEST-009 |

### 先紅後綠

- `internal/ai/review`：先寫測試（`review-red.log`，實作前失敗），實作後 76 個 RUN 都 PASS（`review-green.log`）。
- Expected effects：MOD-050 的情境（intent 寫「保證 lead time 下降 11%」，回答寫「Lead time falls 11%」）原本會 ready。測試先改為要求 invalid，因此失敗（`effects-red.log`）；review 改為不讀 intent 後通過（`effects-green.log`）。`TestReviewEval` 的 MOD-050 unsafe 回答也涵蓋這個情境。
- Architecture probe：`the-proposal-service-links-the-generator` 在 `make backend-test` 中失敗。原因是 generator 的測試 import `internal/proposals`，probe 因此形成 import cycle（`backend-test-run1-fail.log`）。這個連結本來就無法編譯，所以改由 `internal/runs` 連結，被規則擋下。
- 沒有對 T-025 的程式或 0012 跑全面的 mutation harness（見 NOT RUN）。

## NOT RUN

- **所有 DB 測試**：`make integration-test`、`make db-test`、`db-go-test`，包括上表 9 個 Go 測試、`proposal_jobs.sql` 與修改過的 `constraints.sql`。`su postgres -c 'id'` 被工具拒絕（原文在 `db-not-run.txt`），依指示交給 coordinator 執行。**Migration 0012 的 SQL 從未在任何 PostgreSQL 上執行過**：語法、trigger 行為、與 0001–0011 的互動、down migration 都沒有驗證。
- `make frontend-test`：`npm ci` 回 `EBADENGINE`（Node 22.22.0；需要 ≥24.21.0）。`contracts.test.ts` 的新正反例、`i18n.test.ts` 的數量（23）與 typecheck 都沒有執行；只有 `gen_ts_contracts --check` 確認產生的 TS 是最新的。
- Live AI 與 `make ai-eval-live`：live AI 未核准（H-001、H-017）。Eval 的回答是手寫的，評的是 server，不是 model，所以 golden ≥90%（T-038）沒有任何結果。
- `make e2e-test`：UI 尚無 proposal 畫面（T-028）。
- 全面的 mutation harness（`mutants.py`、`sql-mutants.py`）。
- Analysis 的 job handler：依賴 T-029，屬 T-030。

## 需要 owner 或 reviewer 決定

- **D-061（CB-40）**，PROPOSED：兩個 ValidationCode、provider 錯誤的對應與重試、provider_metadata 的內容、不顯示 `id_pool` 與 `output_locale`。
- **D-062**，PROPOSED：proposal 跟隨 job、stale base 在建立時回 409、invalid／failed 的內容、server risk 規則的解讀（D-016 之外的部分：改 type 為 high；requirements、改接、新增或刪除 entity、constraint 改變為 medium）、expected effects 不接受 intent 的數字、review 的順序。
- Gate：tasks.json 中 T-025 與 T-026 都加了這兩項；在批准之前，T-025 在 planning check 中為 `next_after_review`。

## 殘餘風險

- 0012 與 9 個 DB 測試從未執行，是最大的風險。Coordinator 跑 `make integration-test` 之前，不能認定 lifecycle 的保證成立。
- Provider 回答的文字（summary、assumptions、questions）不掃描 secret，依賴 context 本來就不含 secret（T-024 的 `secrets_sent` gate）。
- 依 tenant 與期間累計的 token quota 沒有做（沒有 quota 數字；T-036）。每個 request 的 budget 與每個 process 的並行上限是 T-024 的。
- Generation 期間 base 變舊不會使 proposal 失效；由 T-026 的 approval revalidation 回 409。
- 可重試的 provider 錯誤每個 attempt 都會再呼叫一次 provider，成本由 ADR-024 的 attempt 上限與 T-024 的 retry 上限決定。
- 0012 的 deferred constraint trigger 把「job completed」與「proposal generated」綁在同一 transaction；這個設計只有 SQL 測試（NOT RUN）驗證。

## Coordinator 的執行（1f6ff31）

Coordinator 把 T-025 的 9 個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（解決 `docs/DECISIONS_AND_TBD.md` 與 `planning/decisions.json` 中與 H-018 的衝突，兩邊都保留），在 1f6ff31 上執行：

| 命令 | Exit | 結果 |
|---|---:|---|
| `make spec-check PYTHON=.venv/bin/python` | 0 | `PASS_DB_TESTS_FRESH`，10 個 SQL 檔 |
| `make backend-test` | 0 | 57 個 package ok |
| `make architecture-check` | 0 | 69 個 package、15 條規則、16 種 build configuration |
| `make ai-eval` | 0 | `TestReviewEval` PASS；live provider 與 real model 的 eval 仍為 NOT RUN |
| `make frontend-test` | 0 | 18 個 test file、420 個 test 通過（`coordinator-frontend-test.log`） |
| `make integration-test`（新的 `dt_test_t025_main`，一次性 cluster） | 0 | `run_db_tests.sh` 依序跑 0001–0012 up、所有 SQL suite（含 `proposals.sql`、`proposal_jobs.sql`）、0012 與 0011 down、`pool_rows.sql`、0011 拒絕重跑、其餘 down 與重新 up；之後 DB-backed Go 測試全部 ok，包括 `internal/proposals`、`internal/proposals/generator`、`internal/storage/proposals`、`internal/api`、`internal/worker`；`--- SKIP` 為 0（`coordinator-integration-test.log`） |

因此上方「NOT RUN」中的 DB 測試、migration 0012 與 `make frontend-test` 已由 coordinator 執行並通過；「殘餘風險」第一條與最後一條中的「從未執行」不再成立，lifecycle 的保證以這次執行為證據。Mutation harness、live provider 仍為 NOT RUN。
