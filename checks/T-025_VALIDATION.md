# T-025 驗證報告：Async proposal generation／validation

**日期：2026-10-20｜狀態：待獨立 review；`planning/tasks.json` 的狀態未改，由 reviewer 決定。｜需求：REQ-008、REQ-009｜Gate：D-002、D-014、D-016、D-020、D-022、D-027、D-046 都已 APPROVED；本任務提出的 D-061、D-062 是 **PROPOSED**，`approved_by`／`approved_at` 為 null，T-025 依建議實作，T-025 與 T-026 都以它們為 gate。｜驗收測試：TEST-003、TEST-006、TEST-007｜DB 測試與 `make integration-test` 為 NOT RUN（見「NOT RUN」）。**

範圍依 tasks.json：

1. durable generating job → proposed content → server validator／risk → ready；
2. needs_input／invalid／failed 狀態與 immutable ready content；
3. server 綁定所有 identity、validation、approval 欄位，沒有 write tool；
4. AI proposal 的 job handler 是 adapter，不在 `internal/ai`；另立 package 並記錄名稱（T-019 round 2 L3）。

另依 coordinator 的指示：proposal 跟隨它的 job，與 run（0009）、import（0010）的做法相同，每個 proposal 最多一個 generating job；CB-40 中本任務需要的項目（pool 違規的 issue code、目錄中的 `AI_OUTPUT_INVALID`、provider 錯誤對應的狀態與 HTTP）以 PROPOSED decision 與契約變更補上，不批准任何東西。

只用 deterministic fake provider（`internal/ai/provider/fake`）。**沒有任何 live AI 呼叫**，沒有讀取或設定任何 API key（H-001、H-017）。逐條輸出在 `checks/evidence/2026-10-20-T-025/`：`exit-codes.txt`、`environment.txt`、各指令的 log、`db-not-run.txt`（su 被拒的原文）；coordinator 的執行在同一目錄的 `coordinator-*.log` 與 `coordinator-exit-codes.txt`。Review 第 1 輪的輸出在 `checks/evidence/2026-10-20-T-025-r1/`（見文末「Review 第 1 輪」）。

## Commit

下表是 `claude/lucid-meitner-vq1awo` 上的 hash。原 worktree 的 hash（cba0393…579706d）在 coordinator cherry-pick 之後不在這個 branch 上（review L4 更正）。

| Commit | 內容 |
|---|---|
| 92167ec | `internal/ai/review`：server 對 provider 回答的 review 與 server risk；`ValidationCode` 新增 `ID_POOL_VIOLATION`、`AI_OUTPUT_INVALID`（schema、產生的 TS、en／zh-Hant 字串） |
| 60f2203 | 契約：`proposal.schema.json` 規定 invalid 必須帶 `validation.valid=false` 且沒有 changes，只有 failed 帶 `error`；fixtures 與 cases；OpenAPI 的 202 與五種狀態範例；`contracts.test.ts` 的正反例 |
| 7569028 | migration 0012：proposal 跟隨它的 job（三個 trigger、四個 CHECK、`output_locale`、`selected_entity_ids`）；`db/tests/proposal_jobs.sql`；`constraints.sql` 的 D-027 組合改由 worker 寫入 |
| 3d81e9c | review 修正：expected effects 中 intent 的數字也不算（eval MOD-050）；先紅後綠 |
| d164301 | `internal/storage/proposals`、`internal/proposals`（service＋HTTP）、`internal/proposals/generator`（handler）、cmd/api 與 cmd/worker 的 AI 模式、architecture 規則與 probe |
| 457bfc6 | `TestReviewEval`（`make ai-eval` 的 review suite，`checks/ai-evals/fake-review.json`）；Makefile 的 `db-go-test` 加入 `./internal/proposals/...` |
| 7d11f32 | 文件：D-061、D-062 PROPOSED；ARCHITECTURE、AI_SPEC、API_DATABASE、SECURITY、CB-40；tasks.json 記錄 package 名稱與 planned outputs（status 未改）；重產的 `checks/*.json` |
| b404b36 | Architecture probe 改由 `internal/runs` 連結 generator（原 probe 形成 import cycle，見「先紅後綠」） |
| 1f6ff31 | 本報告、evidence、`checks/docs-check.json` |
| 175e1ae | Coordinator：在 1f6ff31 執行 `make integration-test` 與 `make frontend-test` 的結果與 log |
| fcf824d | Coordinator：每個指令的 log 與 `coordinator-exit-codes.txt` |
| fa8077a…6b5b914（第 1 輪） | Review 第 1 輪的修正，見文末 |

## 稽核：scope 逐條

| Scope | 判定 | 證據／處理 |
|---|---|---|
| 1. generating job → content → server review → ready | 已完成；DB 部分 NOT RUN | `proposals.Service.Create` 依序檢查：role；Idempotency-Key（fingerprint 為 `{model_id, canonical body}`）；AI 是否可用（503，不寫入）；intent、locale、mode；鎖 model；version 屬於此 model（否則 422 `/base_version_id`）；是否為 latest（否則 409 `MODEL_VERSION_CONFLICT`）；server 產生 ID pool。之後 proposal、`model_proposal` job（payload 只有 proposal 與 request ID）、audit `proposal.create`、idempotency response 同一 transaction commit，回 202 `generating`。Handler：在 lease 下鎖 proposal、讀 base 與 snapshot keys；在 transaction 外建 context、呼叫 provider、`review.Review`、dataset binding stage；在 `Finish` 中重新鎖 proposal，確認仍是 generating，寫入結果與 audit `proposal.generate`。無 DB 的證據：`TestGenerateOutsideTheDatabase`、`internal/ai/review` 的 17 個 test、`TestReviewEval`。DB 的證據（NOT RUN）：`TestAReadyProposal`、`TestProposalClosedLoop` |
| 2. needs_input／invalid／failed、immutable ready | 已完成；DB 部分 NOT RUN | needs_input 只存 summary、assumptions、questions，沒有 patch、risk、validation。invalid 只存 issues（固定 message、JSON Pointer）與 provider metadata，不存回答的任何文字。**更正（review 第 1 輪 M1）：這句當時不成立**：patch、candidate validation、domain pack、dataset binding 的 issue message 與 path 會引用回答的文字（例如 `no node "…"`、`members not allowed: …`），invalid 的 validation 因此存下 provider 的文字；DB 也沒有禁止 invalid 帶 assumptions 等內容（L2）。兩者都在第 1 輪修正。provider 錯誤是 job failure，job 最終失敗時 trigger 把 proposal 轉為 failed 並帶 failure，API 以 ErrorEnvelope 顯示（D-061 (c)）。Ready 之後 review payload 不可改（0001 起），identity 不可改（0012）。`TestGenerated`、`TestProviderFailures`、`TestPublicMatchesTheContract`（每種狀態都依 schema 驗證）、`TestFailureErrors`；DB：`TestStatesFromAnswers`、`TestProviderFailuresFailTheProposal`、`proposal_jobs.sql`（NOT RUN） |
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
| `internal/ai/review`（17 個） | root、identity、move／copy、escape、P1 distribution 的 patch 被拒，第二個 op 失敗時整個 patch 不套用；scenario allowlist；新 ID 必須來自同種類 pool（固定 message，不引用 ID）；server risk 的規則（刪除、改 type、降 capacity、requirements、改接、failure／rework／cycle 上的 probability、新 cycle、新增或刪除 entity、constraint）與 max(server, claim)。**更正（review 第 1 輪 M4）：「每條規則」當時不成立**：改 type、改接、constraint、entity type、cycle 上的 probability 與 domain pack 的 error 沒有只觸發該規則的測試，拿掉這些規則測試仍通過；第 1 輪以 `TestEachRiskRule`、`TestThePacksRules` 補上；no-op；warning 保留；expected effects 的數字；needs_input；output 形狀；issue 上限 1000；不合格的 input | TEST-003、D-016、D-020–D-022、D-027、D-046 |
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

Coordinator 把 T-025 的 9 個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（解決 `docs/DECISIONS_AND_TBD.md` 與 `planning/decisions.json` 中與 H-018 的衝突，兩邊都保留），在 1f6ff31 上執行。每個指令的 log 與 exit code 在 `checks/evidence/2026-10-20-T-025/coordinator-*.log` 與 `coordinator-exit-codes.txt`（fcf824d 補上；之前只有 frontend 與 integration 的 log）：

| 命令 | Exit | 結果 |
|---|---:|---|
| `make spec-check PYTHON=.venv/bin/python` | 0 | `PASS_DB_TESTS_FRESH`，10 個 SQL 檔 |
| `make backend-test` | 0 | 57 個 package ok |
| `make architecture-check` | 0 | 69 個 package、15 條規則、16 種 build configuration |
| `make ai-eval` | 0 | `TestReviewEval` PASS；live provider 與 real model 的 eval 仍為 NOT RUN |
| `make frontend-test` | 0 | 18 個 test file、420 個 test 通過（`coordinator-frontend-test.log`） |
| `make integration-test`（新的 `dt_test_t025_main`，一次性 cluster） | 0 | `run_db_tests.sh` 依序跑 0001–0012 up、所有 SQL suite（含 `proposals.sql`、`proposal_jobs.sql`）、0012 與 0011 down、`pool_rows.sql`、0011 拒絕重跑、其餘 down 與重新 up；之後 DB-backed Go 測試全部 ok，包括 `internal/proposals`、`internal/proposals/generator`、`internal/storage/proposals`、`internal/api`、`internal/worker`；`--- SKIP` 為 0（`coordinator-integration-test.log`） |

因此上方「NOT RUN」中的 DB 測試、migration 0012 與 `make frontend-test` 已由 coordinator 執行並通過；「殘餘風險」第一條與最後一條中的「從未執行」不再成立，lifecycle 的保證以這次執行為證據。Mutation harness、live provider 仍為 NOT RUN。這一段說的是 1f6ff31 的 0012 與 DB 測試；第 1 輪修改了 0012 並新增 DB 測試，那些尚未執行（見下一節）。

## Review 第 1 輪（H-003，2026-10-20）

獨立 review 的結論是 CHANGES REQUESTED（0 個 High、4 個 Medium）。修正在 branch `t025-fix1`（從 fcf824d 開出，沒有 push）。每一項都先寫失敗的測試並留下 red log，修正後留下 green log，都在 `checks/evidence/2026-10-20-T-025-r1/`。D-061、D-062 仍是 PROPOSED，`approved_by`／`approved_at` 為 null；這一輪只改了它們的文字（見下文）。

### Commit

| Commit | 內容 |
|---|---|
| fa8077a（worktree 上為 3404171） | M1：review 回傳的每個 issue 都經 `review.Clean` |
| a46a206（worktree 上為 b7802fb） | M2：U+0000 與空字串判為 invalid；DB 拒絕的內容不重試 |
| 2fc7edf（worktree 上為 e3a10b4） | M3：patch 寫入的名稱不提供數字 |
| bc05de2（worktree 上為 61801db） | M4、L1：每條 risk 規則一個測試；數量減少也是 medium |
| 531182f（worktree 上為 ac0a088） | L3、L5：呼叫 provider 前讀取並驗證 snapshot；整份 manifest；OpenAPI 文字 |
| dc7b574（worktree 上為 e492c1f） | L2、I1：invalid 與 failed 不帶產生的內容；proposal 只在 job 結束後 failed；SQL 測試 |
| 4ae2c40（worktree 上為 2ff89bd） | D-061、D-062 的文字（仍 PROPOSED）；AI_SPEC、SECURITY、API_DATABASE、ARCHITECTURE |
| 99048d5（worktree 上為 eb33c38） | 本報告的更正與本節、evidence、重產的 `checks/ai-evals/fake-review.json` |
| 766984f（worktree 上為 e7b4060） | D-061 (c)：寫入結果時 DB 的暫時錯誤仍重試（只改文件與註解） |
| 6b5b914 | Coordinator：T-046 phase 2 新增的 7 個 code 的固定句（之後由 dc1ae02 取代） |
| fbb0f8f | Coordinator 的執行與 evidence |

### 發現、修正與證據

| 項目 | 問題 | 修正 | Red | Green |
|---|---|---|---|---|
| M1 | invalid 的 validation 存下 provider 的文字：patch（`allowlist does not include <path>`）、candidate validation（`no node "…"`、`members not allowed: …`、pattern 引用值）、dataset stage（samples_ref）、domain pack 的 message，以及含 member 名稱的 path，都原樣存進 `ai_proposals.validation`，`getProposal` 讀得到 | `internal/ai/review/clean.go`：每個 issue（invalid 的，以及 ready 保留的 warning）的 message 換成該 code 的固定句（review 自己的固定句保留，catalogue 以外的 code 用通用句），path 只保留 array index 與 contract schema 宣告的 member 名稱，遇到其他 token 截到最深的已知祖先；`invalid()`、`InvalidOutput`、`Refuse` 與 ready 都套用。`wantInvalid` 與 eval 的 unsafe 檢查改為也要求 message 與 path 已清理（`review.Clean(is) == is`）；`TestInvalidOutput` 的 `/approved`（不是 schema 的 member）現在截為 `""` | `m1-review-red.log`：`TestInvalidKeepsNoProviderText` 的 10 個 vector 中 6 個，加上 client、dataset stage、pack 的 issue 都引用 marker；`m1-eval-red.log`：MOD-036 的三個 marker 回答 | `m1-review-green.log`、`m1-eval-green.log`；mutant M1a–M1c 被擋下 |
| M2 | 含 U+0000 的回答通過 review，`Finish` 的 UPDATE 在 PostgreSQL 失敗（22021、22P05），worker 當成 INTERNAL 重試，每次都再呼叫 provider，最後 failed／INTERNAL_ERROR；空的 needs_input summary 違反 `ai_proposals_needs_input_content`，一樣重試 | Review 拒絕 summary、assumptions、questions、expected effects、changes（值、member 名稱、path）中的 U+0000 與空字串，在該位置回 `AI_OUTPUT_INVALID`（固定句）；`store.Generate` 把 22021、22P05、23514 轉成 `ErrUnstorable`，generator 以不重試的 `PROPOSAL_OUTPUT_UNSTORABLE` 結束 attempt（API 為 INTERNAL_ERROR），並 audit 這次 provider 呼叫 | `m2-review-red.log`：`TestUnstorableText` 14 個 case 都失敗，11 個為 ready 或 needs_input，3 個雖為 invalid 但不是在 U+0000 的位置（schema 或 allowlist 的 issue；其中 `NUL deep in a patch value` 的 member 在 red 之後由 `x` 改為 marker，因為 `x` 是 schema 的 member，path 不會在那裡截斷）；`m2-generator-red.log`：經 client 的 3 個 case 都是 ready；`m2-finish-red.log`：`TestFinishFailures`、`TestUnstorable` 編譯失敗（`ErrUnstorable`、`finishFailure` 尚不存在） | `m2-green.log`；mutant M2a–M2d 被擋下；DB 測試 `TestUnstorableAnswersAreInvalid`、`TestGenerateRefusesUnstorable` NOT RUN |
| M3 | Expected effects 的數字可以來自 provider 剛寫進 candidate 名稱的數字（改名為 "Pipeline 11" 再寫 "Lead time drops 11%." 為 ready） | 名稱中的數字只取 base 的；candidate 的數值參數照樣算 | `m3-review-red.log`：`TestNamesDoNotSupplyNumbers` 三個 case（model、resource、新 node 的名稱）都是 ready；`m3-eval-red.log`：MOD-050 的第三個 unsafe 回答是 ready | `m3-green.log`；mutant M3 被擋下 |
| M4 | 多條 server risk 規則沒有獨立測試（reviewer 的 M02、M08、M05、M06、M04b 存活；M10 也存活） | `TestEachRiskRule`：每個 case 從 valid 的 base（必要時先套用一個人做的修改）改成 valid 的 candidate，只觸發一條規則（node 改 type、flow 改接、constraint 刪除、新增 entity type／metric／node／flow、刪除 flow、換 resource、流向不在 cycle 上的 failure node、cycle 上流向 process 的 decision）；`TestThePacksRules` 以 stub DomainPack 證明 pack 的 error 使 proposal invalid、warning 保留 | `m4-mutants-red.log`（本 worktree 的 harness，加測試前）：M02、M03、M04b、M04c、M05、M06、M08、M31、M33、M34 存活；M10 已被 M1 的 pack case 擋下 | `m4-mutants-green.log`、`mutants-final.log`：31 個 mutant 中 30 個被擋下。M03（新 cycle 不算 medium）存活：新 cycle 必須新增或改接 flow，那本身已是 medium，所以這個 mutant 對 `ServerRisk` 的結果是 equivalent |
| L1 | D-062 (d) 說 process 需要的 resource 或數量改變至少 medium，但 `n > b` 把數量減少當成沒改變 | `sameRequirements` 改為數量必須相等；`ServerRisk` 的註解改為「another quantity (more or fewer units)」，並改指 D-062 (d)（原寫 D-061） | `m4-l1-red.log`：`fewer units` 為 low | `m4-l1-green.log`；L1 mutant（還原為 `n > b`）被擋下 |
| L2 | 0012 不禁止 invalid 帶 assumptions、expected effects、questions、dataset bindings、confidence、provider claim；failed 沒有內容的 CHECK；schema 的 invalid 只禁止 changes；INSERT 不要求 decided_by／decided_at 為 null | 0012 原地修改（尚未部署）：`ai_proposals_invalid_content` 加嚴、新增 `ai_proposals_failed_content`（含 provider metadata）、INSERT guard 拒絕 decider 與決定時間；`follow_proposal_job` 轉 failed 時清空 generating 期間寫入的內容，CHECK 不會擋下 job 的結束；`proposal.schema.json` 的 invalid 與 failed 都以 `not anyOf required` 禁止每個產生的 member，新增三個反例 fixture；`db/tests/proposal_jobs.sql` 新增測試 | `l2-schema-red.log`：舊 schema 接受 22 個「invalid／failed 加一個 member」的組合；SQL 測試 NOT RUN | `l2-schema-green.log`；SQL NOT RUN |
| L3 | `DATASET_STORAGE_FAILED` 可重試，且發生在 provider 呼叫之後，重試會再付費呼叫一次 provider | 呼叫 provider 之前讀取 base manifest 的每個 snapshot（每個至多 `MaxSnapshotBytes+1` bytes）並以 binding stage 驗證 base：讀取失敗可重試（還沒呼叫 provider），驗證不過或沒有 object storage 不重試；呼叫之後的 binding stage 只讀這些副本，不讀 storage，若仍出錯（只可能是 server 自己的錯）不重試。D-061 (c) 寫明此規則 | `l3-l5-red.log`：storage 失敗、沒有 object storage、snapshot 被改的三個 case 都先呼叫了 provider；成功時 snapshot 在呼叫之後才讀 | `l3-l5-green.log`；mutant L3a–L3c 被擋下 |
| L4 | 本報告的 commit 表列的是 cherry-pick 前的 hash；:35 與 :65 的說法被 M1、M4 推翻；SECURITY.md 仍說 DB 測試 NOT RUN | Commit 表改為 branch 上的 hash，加上 175e1ae、fcf824d；引用 coordinator 的 log 與 exit code；:35、:65 加上更正；SECURITY.md 改為 coordinator 的執行結果與本輪 NOT RUN 的 DB 測試；本節 | — | — |
| L5 | OpenAPI 說 ready 帶「the dataset_bindings the candidate uses」，程式存的是 base 的整份 manifest | T-012 發布 version 時保留 base 的全部 binding（`internal/models` 的 `check`、TWIN_IR_SPEC §5.2：未使用的 binding 也驗證並凍結），所以程式的行為與 T-012 一致；改 OpenAPI 的文字，generator 改為存 binding stage 驗證過的 manifest（D-034 排序），規則記在 D-062 (g)，供 T-026 凍結批准後 version 的 manifest | 沒有 red：行為本來就符合 T-012 的規則，測試記錄這條規則 | `TestAReadyProposalBindsTheBasesManifest`（`l3-l5-green.log`）：candidate 不再使用 dataset 時仍帶兩個 binding，順序依 logical_ref；mutant L5 被擋下 |
| I1 | 0012 以 `pg_trigger_depth() > 1` 辨認「job 的 follow」，任何其他寫 proposal 的 trigger 都會通過 | `protect_proposal_lifecycle` 改為只在這個 proposal 的 job 已 failed 或 cancelled 時允許轉 failed（AFTER trigger 看得到 job 的新狀態；job 一律以 queued 建立，0007）；SQL 測試：`twin.projects` 上的 trigger 在 job 仍執行時把 proposal 轉 failed，預期 55000 | SQL NOT RUN（`db-not-run.txt` 列出舊 migration 下預期失敗的 statement） | NOT RUN |
| I5 | 沒有決策涵蓋的實作選擇 | 寫進 D-062 (h)：不在 base 的 `selected_entity_ids` 照樣接受、只計為 context 的省略；`getProposal` 不顯示 intent 與 `selected_entity_ids`；worker 沒有 domain pack 時為不重試的 `PROPOSAL_INPUT_INVALID`；DB 不要求 generating 的 proposal 有 job（與 0009 相同） | — | — |
| I4 | flow 改接使 node 不可達時只是 medium 加 `UNREACHABLE_NODE` warning | 不改程式；寫進 D-062 (i) 作為 owner 的選項：A（建議）維持，因為 node 仍在模型中可再接回，TWIN_IR_SPEC §4 把不可達的 node 定為不阻擋 publish 的 warning，D-016 只把刪除定為 high；B 視同刪除改為 high | — | — |

T-016 的同一個 pattern：`migrations/0009_simulation_runs.up.sql`（`followed := pg_trigger_depth() > 1`）與 `0010_import_uploads.up.sql` 的 `protect_import` 也以 trigger depth 辨認 follow；`checks/T-016_VALIDATION.md` 的「Info：`protect_import` 的 `pg_trigger_depth() > 1`」已把它列為 T-017／T-018 的後續事項。本輪沒有修改 T-016 或 T-023 的程式，建議以 I1 的方式（檢查自己的 job 是否已結束）一併處理。

### 實際執行（本輪）

在 worktree 中執行，Go 1.27.2、Node 22.22.0、4 CPU，沒有 PostgreSQL server；`make backend-test` 前先 `go clean -testcache`。Exit code 與 log 在 `exit-codes.txt`。

| 指令 | Exit | 說明 |
|---|---:|---|
| `make spec-check PYTHON=.venv/bin/python` | 0 | `spec-check.log`；contracts 46 正例、77 反例；`PASS_DB_TESTS_FRESH` |
| `make backend-test` | 0 | `backend-test.log`：gofmt、go vet、57 個 package ok；DB-backed 測試 skip（NOT RUN） |
| `make backend-race` | 0 | `backend-race.log`：57 個 package ok |
| `make architecture-check` | 0 | 69 個 package、15 條規則、16 種 build configuration |
| `make ai-eval` | 0 | `TestFakeProviderEval`、`TestReviewEval` PASS；`fake-review.json` 的 `unsafe_answer_invalid` 由 20 變 24（MOD-036 三個 marker 回答、MOD-050 改名回答），5 個 zero-tolerance gate 仍為 0 |
| `make ts-contracts PYTHON=.venv/bin/python` | 0 | 產生的 TS 沒有改變（schema 的 `not` 條件不影響 type） |
| `make frontend-test` | 2 | NOT RUN：沒有 `apps/web/node_modules`，Node 22.22.0 而 `package.json` 要求 ≥24.21.0 <25；沒有繞過 engine-strict |
| `mutate.py`（targeted mutants） | — | `mutants-final.log`：31 個中 30 個被擋下，M03 為 equivalent |

### NOT RUN（本輪）

- **DB 測試，交給 coordinator 在一次性 DB 上執行**（`make integration-test`）：修改後的 migration 0012（up 與 down，從未在 PostgreSQL 上執行）；`db/tests/proposal_jobs.sql` 新增的部分（INSERT 帶 decided_by／decided_at、invalid 帶各種產生的內容、attempt 寫入內容後 job 失敗會清空、關掉所有 trigger 時的兩個 CHECK、`twin.projects` 上的 trigger 在 job 執行中把 proposal 轉 failed）；Go 的 `internal/proposals/generator` `TestUnstorableAnswersAreInvalid`、`internal/storage/proposals` `TestGenerateRefusesUnstorable`；以及受影響 package 的所有 DB-backed 測試（`internal/proposals/...`、`internal/storage/proposals`、`internal/api`、`internal/worker`、`cmd/api`、`cmd/worker`，例如 `TestAReadyProposal`、`TestStatesFromAnswers`、`TestProviderFailuresFailTheProposal`、`TestOrganizationFilter`、`TestProposalClosedLoop`、`TestProposalRoleMatrix`、`TestProposalRequestRefusals`、`TestProposalsNeedAI`、`TestProposalIdempotency`）。說明在 `db-not-run.txt`。
- `make frontend-test`：見上表。
- Live AI 與 `make ai-eval-live`：live AI 未核准（H-001、H-017）。
- 全面的 mutation harness（`mutants.py`、`sql-mutants.py`）；本輪只跑 targeted mutants，SQL 沒有 mutant。

### D-061、D-062 的文字變更（仍 PROPOSED）

- D-061 (b)：空字串與 U+0000 為 `AI_OUTPUT_INVALID`。(c)：provider 呼叫之後，除了 provider 可重試的錯誤與 `LEASE_EXPIRED`、`WORKER_STOPPED` 外不重試；base 的 snapshot 在呼叫前讀取與驗證；新增 `PROPOSAL_OUTPUT_UNSTORABLE`（INTERNAL_ERROR）。新增 (f)：issue 的清理規則。
- D-062 (a)：proposal 只在 job 已結束時轉 failed，follow 清空內容。(c)：invalid 與 failed 不帶產生的內容，建立時沒有 decider。(d)：數量減少也是 medium。(e)：名稱的數字只取 base 的。新增 (g) manifest 規則、(h) I5 的選擇、(i) I4 的 owner 選項。

### 殘餘風險

- 修改後的 0012 與新增的 SQL、Go DB 測試都沒有在 PostgreSQL 上執行過；尤其 lifecycle trigger 在 API、worker、owner 三種 role 下透過 RLS 查 job、follow trigger 以 worker 的欄位權限清空內容、`ALTER TABLE … DISABLE TRIGGER USER` 在測試 transaction 中的行為。Coordinator 的 `make integration-test` 之前，不能認定 L2、I1、M2 的 DB 部分成立。
- Path 的清理以 schema 的詞彙為準：provider 若讓 ID 剛好等於某個 schema member 名稱（例如名為 `name` 的 position key），該 token 會留在 path 中；能留下的只有 schema 的詞，message 一律是固定句。
- Ready proposal 的 warning 也換成固定句，人看到的細節變少；UI 本來就依 code 顯示在地化的文字。
- 一個 attempt 在 provider 呼叫期間把 base 的 snapshot 放在記憶體中（每個至多 4 MiB，與 run 預載 sample 相同的量級）。
- Review 只檢查 provider 回答的文字；provider metadata（例如 live provider 回傳的 model 名稱）若帶 U+0000，會由 DB 拒絕並以不重試的 `PROPOSAL_OUTPUT_UNSTORABLE` 結束，不會重試。
- Worker 中斷（`LEASE_EXPIRED`、`WORKER_STOPPED`）發生在 provider 呼叫之後時，重試仍會再呼叫一次 provider；這無法在 worker 內避免。寫入結果時 DB 的暫時錯誤（連線中斷、serialization failure，worker 記為可重試的 `INTERNAL`）也一樣會重試並再呼叫一次 provider；本輪只把 DB 拒絕內容（22021、22P05、23514）改為不重試，暫時錯誤保留重試（D-061 (c) 已寫明）。

### Coordinator 的執行（Review 第 1 輪之後）

Coordinator 把第 1 輪的 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`（T-046 phase 2 之後；`checks/contracts-check.json` 的衝突以 `make spec-check` 重產）。整合後的 766984f 上 `make backend-test` exit 2：`TestEveryCodeHasASentence` 要求每個 ValidationCode 都有固定句，但 T-046 phase 2 新增的 7 個 code（`BLOCKING_CYCLE`、`CALENDAR_INVALID`、`RELEASE_SCHEDULE_INVALID`、`BATCH_INVALID`、`SETUP_INVALID`、`STALL_RISK`、`PRIMITIVE_NOT_SUPPORTED`）沒有。6b5b914 為它們加上不引用任何內容的固定句。之後 `spec-check`、`backend-test`、`backend-race`、`architecture-check`、`ai-eval`（`checks/ai-evals` 沒有變化）與 `integration-test`（新的 `dt_test_t025_r1`：26 個 package ok，沒有 FAIL 或 SKIP，包括本輪新增與修改的 DB 測試與 `proposal_jobs.sql`）都是 exit 0；`frontend-test` 在 766984f 上 exit 0。Log 與 exit code 在 `checks/evidence/2026-10-20-T-025-r1/coordinator-*`。因此本輪「NOT RUN」中由 coordinator 執行的 DB 測試與 `make frontend-test` 已執行並通過。

## Review 第 2 輪（H-003，fbb0f8f）：ACCEPT

同一位獨立 reviewer 在 fbb0f8f 上複審（以 `git archive` 匯出的副本執行，沒有改動 repo）：第 1 輪的 M1–M4、L1–L3、L5、I1 都已修正，原 probe 重跑得到修正後的結果，mutant 26 個被抓到、3 個為 equivalent 或走不到（M03、N14、N21）；L4 部分修正；I4、I5 已記入 D-062 (i)、(h)。沒有新的 High 或 Medium。結論 **ACCEPT**；這只是程式審查的結論，D-061、D-062 仍是 PROPOSED，T-025 以兩者為 gate。

新的 Low 由 coordinator 修正（本節的 commit）：

- **L1**：第 1 輪 commit 表列的是 worktree 上的 hash。已改為 branch 上的 hash（fa8077a、a46a206、2fc7edf、bc05de2、531182f、dc7b574、4ae2c40、99048d5、766984f、6b5b914、fbb0f8f），並保留 worktree 上的 hash 對照。
- **L2**：`docs/SECURITY.md` 仍寫第 1 輪的 DB 測試 NOT RUN；已改為 coordinator 在 6b5b914 的執行結果。
- **L3**：`internal/ai/review/risk.go` 的註解寫 entity「added, removed or changed」為 medium，程式（與 D-062 (d)）只看 entity 的新增與刪除，constraint 才看改變；註解已改成與程式一致，程式未改。

Info（不修，記錄）：
- I1：呼叫 provider 之後的 binding stage 出錯時不重試（`generator.go`）沒有測試；這條路徑讀記憶體中的副本，實際走不到。
- I2：Finish 因 DB 暫時錯誤失敗時，`failed()` 不 audit 這次已付費的 provider 呼叫，重試又再呼叫一次（D-061 (c) 已寫明會重試）；AI_SPEC §10 的 token 記錄在此情況有缺口，交 T-036（quota）與 T-053（live AI 開啟前）處理。
- I3：每個 attempt 在呼叫 provider 前讀取並驗證整份 base manifest，snapshot 在呼叫期間留在記憶體中（殘餘風險已記錄）。
- I4：與 T-046 phase 2 的互動目前 fail closed；`ServerRisk` 的 line-flow 規則與 calendar 的 ID pool 由 T-047–T-051 解除 gate 時補上（T-046 review 第 1 輪的 811982b 與 T-049 scope）。
- I5：0012 是原地修改的，只在它從未部署時成立（目前沒有任何部署）；套用過舊版 0012 的一次性 `dt_test_*` DB 都必須重建。

## Owner 批准（H-019）與 D-062 (i) 的實作

2026-10-20 owner 對決策頁回覆「Q2 選 B，其他照建議」：D-061 依建議 APPROVED；D-062 依建議 APPROVED，只有 (i) 選 B。T-025 因此標為 done。

D-062 (i) B 的實作（`internal/ai/review/risk.go`）：base 中有 source 到得了（經 probability > 0 的 flow，與 validator 的 `UNREACHABLE_NODE` 相同）、candidate 中仍存在但沒有 source 到得了的 node，與刪除同為 high。依同一理由，刪除 flow 造成的不可達也算；base 中本來就不可達的 node 不受影響。

先紅後綠（`checks/evidence/2026-10-20-H-019/`）：`TestEachRiskRule` 原本的「a flow is re-targeted」（f_test_deploy 改接到 sink，deploy 變成不可達）與「a flow is removed」（刪掉 f_retry，failure 與 rework 不可達）在 B 之下屬於新規則，改名為「a re-target cuts a node off」「a removed flow cuts nodes off」並期望 high；`risk-red.log` 中兩者在改程式前都是 medium（exit 1）。另加三個只觸發舊規則的 case，讓改接與刪除 flow 的 medium 規則仍各自有測試：「a flow is re-targeted」（f_rework_ci 改接到 test，所有 node 仍到得了）、「a flow is removed」（先加 f_skip 再刪掉），以及「a node no source reached stays so」（base 中本來不可達的 sink，結果為 low）。改程式後 `risk-green.log` exit 0。
