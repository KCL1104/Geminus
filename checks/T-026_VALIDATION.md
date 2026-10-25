# T-026 驗證報告：Human approve/reject／canonical effect

**日期：2026-10-20｜狀態：獨立 review 第 2 輪 ACCEPT（2026-10-20，5dd554c）；round 1 的 DB 測試與帶 DB 的 mutant 已由 coordinator 執行並通過（見文末）；D-067–D-069 待 owner 批准後才標為 done。｜需求：REQ-006、REQ-009（另涵蓋 REQ-010 的 AI scenario 批准、REQ-018 的 audit）｜Gate：D-003、D-011、D-027、D-030、D-046、D-061、D-062 都已 APPROVED；本任務提出的 D-067、D-068、D-069 是 **PROPOSED**，`approved_by`／`approved_at` 為 null，T-026 依建議實作，並把三項加進 T-026 的 gate（D-067 另加進 T-028）。｜驗收測試：TEST-005、TEST-006、TEST-007、TEST-009｜本 worktree 不能執行 DB 測試。Coordinator 在一次性 database 上第一次執行 `make integration-test` 時 exit 2（四個 DB-backed 測試失敗，原因在測試設定，見「第二輪」）；修正後第二次執行（branch 上的 95b64e7）exit 0，見文末「Coordinator 的執行」。獨立 review round 1 為 CHANGES REQUESTED（1 Medium、5 Low、4 Info），在 branch `t026-r1` 修正（見「Review round 1」）；**round 1 新增與修改的 DB 測試還沒有在 database 上執行**。**

範圍依 tasks.json：

1. role 與 base 的重新驗證、共用的 model lock、idempotent 且恰好一個新 version；
2. dirty draft 保留、conflicted 狀態、不偷偷 rebase；
3. reject 不改任何東西；role 在讀取與批准之間改變、重複批准、批准與拒絕競爭、base 已變後批准（409 `MODEL_VERSION_CONFLICT`）。

另依 coordinator 的指示：server 在批准時重算 validation 與 risk，不採信存下的 provider claim；批准後 version 的 dataset manifest 依 D-062 (g) 凍結；audit 與 idempotency 在同一 transaction；scenario proposal 依規格建立 scenario（規格沒寫的部分登記為 PROPOSED）；package 的位置要讓 architecture check 接受，並像 T-025 一樣記錄。

批准不呼叫 provider。**沒有任何 live AI 呼叫**，沒有讀取或設定任何 API key；proposal 由 deterministic fake provider 產生（只在 DB 測試中，本 worktree 未執行）。逐條輸出在 `checks/evidence/2026-10-20-T-026/`：`exit-codes.txt`、`environment.txt`、各指令的 log、`db-not-run.txt`（未執行的 DB 測試清單與原因）、`mutants.log` 與 `mutate.py`（targeted mutants）、`public-red.log`／`public-green.log`。

## Commit

第一版與第二輪在 worktree 的 branch `t026`（從 0ee3d2a 開出，沒有 push）；coordinator 把它們 cherry-pick 到 `claude/lucid-meitner-vq1awo`，hash 因此不同，兩者並列。Review round 1 的 commit 在 `t026-r1`（從 `origin/claude/lucid-meitner-vq1awo` 的 534c9ba 開出，T-017 已在其中，沒有 push）。

| Branch 上的 commit | Worktree 的 commit | 內容 |
|---|---|---|
| 2bbd971 | 49b91c2 | 契約：OpenAPI `approveProposal`、`rejectProposal` 的說明與 201／200／409／422 範例；`proposal.schema.json` 的 `decided_by`、`decided_at`、`decision_comment` 與其規則；`http.schema.json` 的說明；14 個 fixture 與 cases；產生的 TS；`contracts.test.ts`；改 Go 之前 `TestPublicMatchesTheContract` 的 red log |
| c34c970 | b0eb9b2 | Migration 0013（up／down）；`db/tests/proposal_decisions.sql`（template 與 render 的 ID）；`constraints.sql` 的 fixture；`run_db_tests.sh` 依原本的順序加入 0013 |
| 3f64129 | ba56408 | `internal/proposals/approval`（service、revalidation、HTTP adapter、unit 與 DB-backed 測試）；`internal/storage/proposals` 的決策寫入與欄位；`internal/storage/scenarios`；`internal/storage/models.LockDraft`；`internal/models.Service.Advance`；Proposal resource 的決策紀錄；`internal/api` 的 route 與 HTTP 整合測試；architecture 規則 `decisions-only-in-the-api` 與 probe；mutation harness 與 log |
| 0fa5ce1 | 590f76f | 文件：D-067–D-069 PROPOSED（DECISIONS_AND_TBD、decisions.json）、CB-42、CB-33 的涵蓋；ARCHITECTURE、AI_SPEC、API_DATABASE、SECURITY、db/README；tasks.json 的 planned outputs、gate 與 package 的說明（status 未改）；traceability；重產的 `checks/*.json`；Makefile 的說明文字 |
| f41de5d | 154ce2b | 本報告（第一版）；`checks/evidence/2026-10-20-T-026/` 的 `environment.txt`、`exit-codes.txt`、各指令的 log、`db-not-run.txt` |
| 3398a6a | 38541d4 | 第二輪的修正（只改測試）：scenario 的回答以 scenario role 寫入 fake；`TestApprovalOnAMovedBase` 第四個 proposal 改為 `lowerCI`；不需 DB 的重現測試；red／green log 與 coordinator 失敗 log 的節錄 |
| 95b64e7 | bf57208 | 本報告的第二輪更新、`db-not-run.txt`、第二輪的執行 log（`*-r2.log`、`exit-codes-r2.txt`） |
| 4abeb96 | — | Coordinator：第二次 integration 的 log 與 exit code（`coordinator-*`），本報告文末一節 |
| 2432637（`t026-r1`） | — | Review round 1 契約：approveProposal／rejectProposal 的檢查順序、normalized fingerprint、replay 的 request_id；approveProposal 的 503（兩個範例）與 `IDEMPOTENCY_KEY_CONFLICT` 範例 |
| fd9a704（`t026-r1`） | — | Review round 1 程式與測試：M1、L1–L4、I2、I3（見「Review round 1」）；approval.go 的規則名稱 |
| 00e962a（`t026-r1`） | — | Review round 1 文件：D-068、D-069 的文字（仍為 PROPOSED）、API_DATABASE、AI_SPEC、SECURITY、CONTRACT_BLOCKERS、SQL 測試的註解 |
| （本 commit，`t026-r1`） | — | 本報告的 round 1 更新與 `checks/evidence/2026-10-20-T-026-r1/` |

## 稽核：scope 逐條

表中的「DB NOT RUN」是第一版時在本 worktree 的狀態。Coordinator 的第一次執行（f595f07）中，edit 的 DB 路徑與所有 SQL 都已通過，scenario 的路徑停在 `not ready`（見「第二輪」）；第二次執行（95b64e7）全部通過（見文末）。Review round 1 新增的 DB 測試還沒有執行（見「Review round 1」）。

| Scope | 判定 | 證據／處理 |
|---|---|---|
| role 重新驗證 | 已完成；DB 部分 NOT RUN | 每個決策都在 `tenant.DB.Run` 中先讀 membership（read-write transaction 以 `FOR SHARE` 鎖住該列），再 `Require(owner, editor)`，然後才 claim Idempotency-Key（replay 也先查 role，D-011）。Viewer 403、其他 organization 404、非成員選了 organization 404。Unit：`TestRoutesMatchContract`（route 與 `planning/api-operations.json` 的 roles 相同，viewer 不在其中）、`TestRefused`。DB：`TestDecisionRoles`（讀過 proposal 後被降為 viewer → 403；進行中的 role 變更會讓批准等待，commit 後看到新的 role → 403；什麼都沒改）、`TestApprovalIdempotency`（撤權後同一 key 的 replay → 403）、`TestApprovalRefusals` |
| base 重新驗證、409 | 已完成；DB NOT RUN | 鎖住 model row 之後才比較 proposal 的 base 與 model 的 latest：不同時 proposal 轉 conflicted（`decided_by` 為這位批准者）、audit `proposal.conflict`，回 409 `MODEL_VERSION_CONFLICT`（`/expected_base_version_id`），與狀態轉換同一 transaction commit 並存為該 key 的回應（D-069 (a)）。`expected_base_version_id` 不是 proposal 的 base 時 409 同 code，但什麼都不變。沒有 rebase：proposal 的 base 與 patch 都不會改。DB：`TestApprovalOnAMovedBase`（另一個 proposal 先批准、publish 移動 base、scenario proposal 也一樣；conflict 的 replay、之後換 key 為 `PROPOSAL_ALREADY_DECIDED`、拒絕 conflicted 也是）。0013 另在 commit 時要求 version 是 base 的下一個（`proposal_decisions.sql`） |
| proposal 的重新驗證（server 重算 validation 與 risk） | 已完成 | `recheck`（`internal/proposals/approval/recheck.go`）把存下的內容（summary、assumptions、canonical changes、expected effects、confidence、provider claim）當成 review 接受過的回答，以 `internal/ai/review.Review` 再 review 一次：mode 的 patch profile 與 allowlist、完整 candidate validation、該 proposal 的 ID pool、domain pack、expected effects 的數字、server risk。之後以 base 的整份 manifest 跑 dataset binding stage，snapshot 重新讀取。存下的 validation、risk、hash 都不採信。Unit：`TestRecheckAppliesTheReviewedChanges`、`TestRecheckTrustsNoStoredResult`（patch 被改而 hash 不變、hash 被改而 patch 不變、patch 與 hash 都改成被拒的變更、存下的 risk 低於 server 的、只有 server risk 被改低、另一個 pool、scenario 模式加 resource、捏造數字的 expected effect、缺內容、未知 mode）、`TestRecheckAppliesTodaysRules`（pack 新增的規則；issue 經 `review.Clean`）。DB：`TestApprovalReviewsAgain`（以 schema owner 停用 trigger 竄改存下的列）、`TestApprovalFreezesTheManifest`（snapshot 內容改變 → 422） |
| 共用的 model lock | 已完成；DB NOT RUN | 依 ARCHITECTURE §4（D-003）的順序 `LockModel` → `LockDraft`（新增）→ 鎖 proposal；reject 也照此順序。DB：`TestDecisionsWaitForTheModelRow`（另一個 transaction 持有 model row 時，approve 與 reject 都等待，釋放後完成） |
| idempotent、恰好一個新 version | 已完成；DB NOT RUN | 同一 key 與 body 回同一個 201（fingerprint 為 proposal ID 與 body；review round 1 起為 normalized parsed body）；換 key 為 409 `PROPOSAL_ALREADY_DECIDED`（D-030）；同一 key 用在另一個 body 為 409 `IDEMPOTENCY_KEY_CONFLICT`。**更正（review M1）**：第一版說「同一 key 用在另一個 proposal」也有測試，但 `TestApprovalIdempotency` 的那個 case 用的是另一個 model 的 proposal，body 的 `expected_base_version_id` 本來就不同，所以 fingerprint 中的 proposal ID 沒有被測到（mutant `fingerprint-ignores-proposal` 存活）。Review round 1 補上 `TestFingerprint`（不需 DB）、`TestTheKeyNamesTheProposal`（同 model、同 base、同 body 的兩個 proposal，approve 與 reject `{}`）與 `TestApprovalRefusals` 的 HTTP case。新 version：parent 為 base、number 為 base 的加一、IR 為 base 套上 review 過的 changes（RFC 8785，D-040）、`source_proposal_id`、comment、`created_by` 為批准者。DB 保證：0001 的 unique `source_proposal_id` 與 0013 的 commit 檢查（version 只屬於 edit proposal 且是 base 的下一個；第二個 version 23505；舊 base 上的另一個 number 23514）。DB：`TestApproveEdit`、`TestApprovalIdempotency`（8 個同 key 並行只有一個 version、回應全部相同）、`TestDecisionRaces`（6 個不同 key 並行只有一個 201）、`proposal_decisions.sql` |
| dataset manifest 依 D-062 (g) 凍結 | 已完成；DB NOT RUN | 新 version 的 manifest 是 proposal 的 `dataset_bindings`，必須等於 base 的整份 manifest（否則是 server 錯誤，不批准）；snapshot 在批准時重讀並驗證；base 綁 dataset 而 API 沒有 object storage 為 503。DB：`TestApprovalFreezesTheManifest`（沒有 object storage 503、snapshot 不符 422、恢復後 201 且新 version 與 binding row 凍結同一個 binding、clean draft 跟上） |
| dirty draft 保留、不 rebase | 已完成；DB NOT RUN | 與 publish 共用 `internal/models` 的 `Service.Advance`（由原本的 `advance` 抽出並 export，行為不變）：clean draft 同步到新 version、revision 加一；dirty draft 原樣保留並回 `draft_out_of_date=true`。DB：`TestApproveKeepsADirtyDraft`（revision、hash、IR 不變，之後 publish 這個 stale draft 為 `ErrConflict`）、`TestApproveEdit`（clean draft 到 revision 2） |
| conflicted 狀態 | 已完成；DB NOT RUN | 見「base 重新驗證」。Conflicted 是終態，決策紀錄只有決定者與時間（0013 CHECK、D-068 (b)） |
| reject 不改任何東西 | 已完成；DB NOT RUN | ready 或 needs_input → rejected（決定者、時間、comment），回 200 與 getProposal 相同的 Proposal；model、draft、version、scenario、simulation job 都不變。DB：`TestRejectAndStates`（`snapshot` 比對前後、replay、拒絕後批准為 `PROPOSAL_ALREADY_DECIDED`、needs_input 可拒絕、generating／invalid／failed 不可批准也不可拒絕）、`TestApprovalClosedLoop`（HTTP 上拒絕後 draft 的 body 與 ETag 不變） |
| 競爭：role 改變、重複批准、批准對拒絕、base 已變 | 已完成；DB NOT RUN | `TestDecisionRoles`、`TestApprovalIdempotency`、`TestDecisionRaces`（批准與拒絕並行：恰好一個成功，結果一致）、`TestApprovalOnAMovedBase` |
| scenario proposal | 已完成；名稱 PROPOSED；DB NOT RUN | 依 D-004 與 AI_SPEC §6–§7：恰好一個 immutable Scenario（base、review 過的 changes 與 hash、`source_proposal_id`），沒有新 version，不入列 run，model、draft 不變；名稱規則規格沒有寫，登記為 D-067（PROPOSED，建議 A：取自 summary）。兩種 mode 都要求 base 仍是 latest（D-062 (b)、AI_SPEC §6；CB-33 後半）。DB：`TestApproveScenario`、`TestApprovalClosedLoop`；0013 要求 scenario 的 patch、hash、base 與 proposal 相同 |
| audit 與 idempotency 同一 transaction | 已完成；DB NOT RUN | Effect、決策、audit、idempotency response 在同一個 `Run` 中；refusal 只有 conflict 會 commit。Audit 只記 ID、hash、risk 與狀態，不記 comment 或 provider 的文字（`TestApproveEdit`、`TestRejectAndStates` 以 SQL 檢查） |
| 錯誤 envelope：typed code、JSON Pointer、request_id | 已完成；HTTP 部分 NOT RUN | `Refused` 把每個 service 錯誤對應到契約的 code 與 path（`TestRefused`）；conflict 的 409 由 service 組成同一個 envelope。HTTP：`TestApprovalRefusals` 對每個拒絕檢查 status、code、path，並以 `code()` 依 `common.schema.json#/$defs/Error` 驗證、比對 `request_id` 與 `X-Request-ID` |
| Package 位置 | 已完成 | tasks.json 原規劃 `internal/model/approval/`，但那是 generic kernel，而批准讀寫 DB，是 adapter。改為 `internal/proposals/approval`（`internal/proposals/...` 已是 application layer），理由記在 ARCHITECTURE §6，tasks.json 的 scope 與 planned outputs 已更新。新規則 `decisions-only-in-the-api` 只讓 `cmd/api`、`internal/api` 連結它（probe：cmd/worker、internal/runs/runner、internal/runs、internal/ai/aicontext） |
| Contract first（OpenAPI、範例、TS 契約測試） | 契約與範例已完成；TS typecheck NOT RUN | OpenAPI 的兩個 operation 有 201／200／409／422 範例，`check_contracts.py` 依 schema 驗證每個範例；getProposal 多一個 approved 範例。Fixture：3 個正例 proposal、4 個反例 proposal、Approval／Approve／Reject 的正反例。`contracts.test.ts` 加入 4 個正例與 3 個 `@ts-expect-error`；`gen_ts_contracts --check` 確認產生的 TS 是最新，但 `make frontend-test` 無法執行（見 NOT RUN）。TS client 沒有加 approve／reject 方法（UI 是 T-031 的） |
| Migration、RLS、grant | 已完成；SQL NOT RUN | 0013 沒有新表，所以沒有新的 RLS 或 composite FK；新欄位 `decision_comment` 由 API 的表級 UPDATE 涵蓋；worker 沒有它的 UPDATE 權限（column-level UPDATE grant 不含它；它以表級 SELECT 讀得到，review L5），`proposal_decisions.sql` 檢查 UPDATE 的 `has_column_privilege`；`twin.check_proposal_effect` 固定 `search_path` 並撤銷 PUBLIC。0001–0012 都沒有修改 |

## 實際執行結果

在 worktree 中執行（`environment.txt`：Go 1.27.2、Node 22.22.0、4 CPU；沒有 PostgreSQL server；先 `go clean -testcache`）。指令與 exit code 也在 `exit-codes.txt`。

| 指令 | Exit | 結果 |
|---|---|---|
| `make spec-check PYTHON=.venv/bin/python` | 0 | `PASS_LIMITED_SPEC_CHECKS`（58 個正例、109 個反例 fixture、42 個 OpenAPI operation 的範例都依 schema 驗證）；planning、decisions（proposed 3：D-067、D-068、D-069）、TS 契約為最新（14 檔、102 型別）、hash、`render_db_tests --check`（11 檔皆最新）都 PASS。重產的 `checks/*.json` 與 590f76f 相同，沒有差異。加入本報告後再跑一次（`spec-check-with-report.log`，exit 0），唯一的差異是 `checks/docs-check.json` 的 `markdown_files` 95 → 96，隨本 commit 提交。完整的 OpenAPI 官方 validator 為 `NOT_RUN_UNAVAILABLE` |
| `make backend-test` | 0 | `go vet` 與 `go test`：58 個 package `ok`，沒有 FAIL；DB-backed 測試全部 SKIP（沒有 DB URL） |
| `make backend-race` | 0 | `go test -race`：58 個 package `ok`；DB-backed 測試同樣 SKIP |
| `make architecture-check` | 0 | `PASS architecture: 71 packages, 16 rules, 16 build configurations`（含新規則 `decisions-only-in-the-api`）；probe 在 `scripts/check-architecture` 的測試中（backend-test 與 backend-race 都 `ok`） |
| `make ai-eval` | 0 | `TestFakeProviderEval`、`TestReviewEval` PASS（只有 fake provider；golden 正確率、live provider、DB 效果為 NOT RUN，log 中列出） |
| `make ts-contracts PYTHON=.venv/bin/python` | 0 | 重寫 14 檔、102 型別到 `apps/web/lib/contracts`，`git status` 沒有任何差異 |
| `make frontend-test` | 2 | 失敗：`eslint` 找不到套件（`ERR_MODULE_NOT_FOUND`），因為 `apps/web/node_modules` 不存在；本機 Node 為 22.22.0，`package.json` 要求 `>=24.21.0 <25`。沒有安裝依賴（見 NOT RUN） |
| `go test -count=1 -v ./internal/proposals/... ./internal/storage/proposals/ ./internal/storage/scenarios/ ./internal/storage/models/ ./internal/models/ ./internal/api/` | 0 | 30 個 PASS、98 個 SKIP、0 個 FAIL。PASS 包含 approval 的 `TestRoutesMatchContract`、`TestRefused`、`TestScenarioName`、`TestDecisionComment`、三個 `TestRecheck*`，以及 internal/proposals 的 `TestPublicMatchesTheContract`。SKIP 全部是 DB-backed 測試（訊息 `NOT RUN: set GEMINUS_TEST_DATABASE_URL ...`），包括 T-026 新增的 14 個；`internal/storage/scenarios` 沒有自己的測試檔（只由 approval 的 DB 測試使用） |
| `make integration-test`、`make db-test` | — | NOT RUN（沒有 PostgreSQL server；見下） |

## 測試涵蓋（對應 TEST-005／006／007／009）

| 測試 | 驗證 | 驗收 |
|---|---|---|
| `TestRoutesMatchContract`、`TestRefused`、`TestDecisionComment`（approval，unit） | route 與 roles 等於 `api-operations.json`，viewer 不能決定；每個錯誤的 code 與 path；comment 的上限、U+0000、UTF-8 | TEST-009、D-025 |
| `TestRecheckAppliesTheReviewedChanges`、`TestRecheckTrustsNoStoredResult`、`TestRecheckAppliesTodaysRules`（approval，unit） | 見上方「proposal 的重新驗證」 | TEST-007、SECURITY §5 |
| `TestScenarioName`（unit） | D-067 的名稱規則（一行、200 字元、「…」、只有空白時用 ID） | REQ-010 |
| `TestPublicMatchesTheContract`（internal/proposals，unit，已改） | 只有 approved、rejected、conflicted 有決策紀錄，conflicted 沒有 comment，`decided_at` 為 UTC；決定後的狀態少了紀錄是 server 錯誤 | D-068 |
| Architecture probe（5 個新的） | worker、job handler、其他 service、`internal/ai` 連結 approval 都被擋下 | AGENTS.md §2 |
| DB：`TestApproveEdit`、`TestApproveKeepsADirtyDraft`、`TestDecisionsWaitForTheModelRow` | 恰好一個 version 的內容；clean draft 跟上；dirty draft 保留、之後 publish 為 409；決策等待 model row | TEST-005、TEST-007 |
| DB：`TestRejectAndStates`、`TestApproveScenario`、`TestApprovalReviewsAgain` | 拒絕、scenario 批准與被拒的批准都不改 model、draft、version（與 scenario、simulation job） | TEST-006 |
| DB：`TestApprovalIdempotency`、`TestDecisionRaces`、`TestApprovalOnAMovedBase`、`TestApprovalFreezesTheManifest`、`proposal_decisions.sql` | 同 key／不同 key 只有一份 effect；不同 proposal 同 base 一個成功一個 409；DB 的一份 effect 保證 | TEST-007 |
| DB：`TestDecisionRoles`、`TestApprovalRefusals`、`TestApprovalClosedLoop` | role 矩陣、其他 organization 404、HTTP 的所有拒絕與 closed loop | TEST-009、TEST-006 |
| DB：`TestDecisionsNeedTheirState`（storage） | 決策 SQL 只離開它能離開的狀態，organization filter 本身也成立 | TEST-009 |
| `TestScriptedAnswersReachEachMode`、`TestTheDBTestsAnswersAreReady`（approval）、`TestTheClosedLoopsAnswersAreReady`（api），第二輪新增，不需 DB | DB 測試寫入的回答以 generator 的方式詢問時，會以 mode 的 role 被讀到，並在它被詢問的 base 上 ready | 測試設定本身（第二輪） |

### 先紅後綠

- `TestPublicMatchesTheContract`：契約加入「決定後的狀態必須有 `decided_by`、`decided_at`」之後、`Public` 尚未輸出它們之前，conflicted 的 resource 不符 schema（`public-red.log`，exit 1）；`Public` 加上決策紀錄後通過（`public-green.log`）。
- Approval 的 unit 測試與實作是一起寫的，不是嚴格地先寫測試。以 22 個 targeted mutant 證明每個檢查都有測試擋下（`mutate.py`、`mutants.log`）：不比 risk、不比 server risk、信任存下的 patch、信任存下的 hash、忽略 review 的結果、把 scenario 當 edit review、不問 pack、不重查 expected effects、把 base 當 candidate、名稱不截斷、不合成一行、空白名稱、comment 2001、comment 含 U+0000、三個錯誤對應、viewer 可決定、conflicted 顯示 comment、ready 顯示決策紀錄、決策紀錄可缺、`decided_at` 不轉 UTC。第一次執行時 `stored-hash-trusted` 只因編譯失敗而「被擋下」（harness 的 mutant 寫錯），也發現沒有「只改 hash」的測試；補上該測試、改正 mutant 後重跑，22 個都由測試擋下。
- Architecture probe：第一次以 `internal/ai/review` 連結 approval，因 approval import review 而形成 import cycle、無法載入（沒有保留 log）；改用 approval 不 import 的 `internal/ai/aicontext`，與 T-025 遇到的情況相同。
- DB 測試與 SQL 都沒有執行，所以沒有它們的 red 或 green。

## 第二輪：coordinator 的 integration 失敗與修正

**Coordinator 的結果**（把五個 commit cherry-pick 到 `claude/lucid-meitner-vq1awo`，f595f07，一次性 `dt_test_*` database）：spec-check、backend-test、backend-race、architecture-check、ai-eval、frontend-test 都 exit 0（frontend-test 在那裡可以執行：18 個檔案）；`make integration-test` exit 2。Migration 0013 的 up／down／up 與所有 SQL suite（包括 `proposal_decisions.sql`、`constraints.sql`）都通過，25 個 Go package ok。失敗的是四個 DB-backed 測試，訊息都是 `not ready`：proposal 的 `Mode:scenario`，結果是 `Status:needs_input`，`Questions:[Which change should the proposal make?]`，`Changes:null`。四個測試是 `internal/proposals/approval` 的 `TestApproveScenario`（db_test.go:431）、`TestApprovalOnAMovedBase`（:591）、`TestDecisionsWaitForTheModelRow`（:760），以及 `internal/api` 的 `TestApprovalClosedLoop`（approvals_test.go:93）。節錄在 `integration-red-excerpt.log`。Edit mode 的 DB 測試都通過了：`TestApproveEdit`、`TestApproveKeepsADirtyDraft`、`TestApprovalIdempotency`、`TestDecisionRaces`、`TestDecisionRoles`、`TestRejectAndStates`、`TestApprovalReviewsAgain`、`TestApprovalFreezesTheManifest`、`TestDecisionsNeedTheirState`、`TestApprovalRefusals`。

**根本原因：測試設定錯誤，不是 production code 的問題。** 兩個 helper（`db_test.go` 的 `ready`、`approvals_test.go` 的 `readyOver`）不論 mode，都以 `provider.RoleModeler` 寫入 fake 的回答。但 generator 是以 mode 的 role 發出請求的（`provider.Mode(pr.Mode)` 再由 `aicontext.Proposal` 轉成 `Mode.Role()`：edit 用 modeler，scenario 用 scenario）。Fake 只讀請求 role 底下的 script（`fake.Complete` 以 `script[req.Prompt.Role][key]` 查找；`fake_test.go` 的 `TestScript` 也明確測了「scenario role 不使用 modeler 的 script」），所以 scenario 請求沒找到 script，拿到 fake 預設的安全回答 needs_input。T-025 的 generator 測試（`loop_test.go`）對 scenario 本來就用 `RoleScenario`，generator、fake、review 都沒有錯。

**先紅後綠（沒有 DB）**：
- 新增 `internal/proposals/approval/script_test.go` 與 `internal/api/script_test.go`（不需 DB）。DB 測試現在都透過 `scriptAnswer`／`scriptProposal` 寫入回答，而這兩個測試以 generator 在兩個 transaction 之間做的事（`aicontext.Proposal` 建 context、provider client 呼叫 fake、`review.Review`）詢問同一個 script。`TestScriptedAnswersReachEachMode` 檢查請求的 role 是 mode 的 role、回答是 proposed、review 是 ready。`TestTheDBTestsAnswersAreReady` 與 `TestTheClosedLoopsAnswersAreReady` 把 DB 測試期望 ready 的每個回答，放在它被詢問的 base 上（version 1、approved raiseCI 之後、再 publish 一個改名之後）檢查。
- Red（`scenario-script-red.log`，exit 1）：helper 仍寫入 modeler 時，三個 scenario 案例都得到 `needs_input`，問題與 coordinator 的 log 相同，這就是 coordinator 看到的錯誤。同一個檢查也找到第二個錯誤：之前被第一個錯誤蓋住，DB 上還沒跑到。`TestApprovalOnAMovedBase` 的第四個 proposal 在 version 3 上又提出 `raiseCI`，但 version 3 的 CI capacity 已經是 6（來自第一個批准），review 把這個什麼都不改的 patch 判為 invalid（`AI_OUTPUT_INVALID` `/changes`「the changes leave the model as it is」）。不修正的話，coordinator 重跑時這個測試會在 db_test.go:655 再失敗一次。
- 修正（38541d4）：兩個 helper 改為以 `provider.Mode(mode).Role()` 寫入，mode 沒有 role 時直接 panic。第四個 proposal 改為 `lowerCI`，並加上原因的註解。closed loop 的 scenario 回答抽成 `eightRunners` 常數，讓 DB 測試與不需 DB 的檢查使用同一份。沒有降低或移除任何 assertion，也沒有加 skip；production code 沒有改。
- Green（`scenario-script-green.log`，exit 0）：三個新測試都通過。

**第二輪在本 worktree 的執行**（38541d4，先 `go clean -testcache`；`exit-codes-r2.txt`、`*-r2.log`）：

| 指令 | Exit | 結果 |
|---|---|---|
| `make spec-check PYTHON=.venv/bin/python` | 0 | 與第一輪相同，重產的 `checks/*.json` 沒有差異 |
| `make backend-test` | 0 | 58 個 package ok |
| `make backend-race` | 0 | 58 個 package ok |
| `make architecture-check` | 0 | `PASS architecture: 71 packages, 16 rules, 16 build configurations`（新測試 import 的 AI package 不違反規則） |
| `make ai-eval` | 0 | fake provider 的 suite PASS |
| `make ts-contracts PYTHON=.venv/bin/python` | 0 | 沒有差異 |
| `make frontend-test` | 2 | 與第一輪相同：本機沒有 `node_modules`，Node 版本不符。Coordinator 那裡 exit 0 |
| `go test -count=1 -v`（T-026 相關 package） | 0 | 33 個 PASS（多了 3 個新測試）、98 個 SKIP（都是 DB-backed）、0 個 FAIL |
| `make integration-test` | — | NOT RUN（本 worktree）；等 coordinator 重跑 |

**仍然只在 DB 上才能驗證的部分**（之後 coordinator 的第二次執行全部通過，見文末）：這四個測試在 `not ready` 之後的步驟（scenario 批准的 201 與 Approval schema、`twin.scenarios` 的一列與它的名稱、patch、hash、0013 的 `scenario_is_its_proposals_effect` 在 API role 下 commit、scenario 的 conflict、publish 移動 base 後的 conflict、指定別的 base 不改任何東西、scenario 批准等待 model row、HTTP 上的 scenario 批准與之後的拒絕）**從未在 PostgreSQL 上執行過**。這一輪我逐行重讀了這些步驟對應的 SQL 與 schema（scenarios 的欄位與 grant、0001 的 approval 規則、proposal schema 的 approved scenario 規則、error envelope），沒有找到其他錯誤，但這不能代替執行。

## Review round 1：CHANGES REQUESTED 的修正

獨立 review round 1 的結論是 CHANGES REQUESTED：沒有 High，1 個 Medium、5 個 Low、4 個 Info。修正都在 branch `t026-r1`（從 534c9ba 開出，T-017 的 0014 已排在 0013 之後）。原則是先寫測試：不需 DB 的部分先看到 red，再改程式；程式本來就對的部分，以 mutant 證明新測試擋得下。D-067、D-068、D-069 仍是 **PROPOSED**，這一輪只改了它們的文字。證據在 `checks/evidence/2026-10-20-T-026-r1/`。

| 項目 | 問題 | 修正 | 測試（DB：在本 worktree NOT RUN） |
|---|---|---|---|
| M1 | Key 的 scope（organization、actor、operation、key）不含 proposal，只有 fingerprint 能分辨「同一 key 用在另一個 proposal」，但沒有測試：`TestApprovalIdempotency` 的那個 case 用另一個 model，body 的 base 本來就不同；mutant `fingerprint-ignores-proposal` 存活。若退化，同一 key 拒絕 A 再拒絕 B（都是 `{}`）會重送 A 的 200，B 仍是 ready | 程式本來就把 proposal ID 放進 fingerprint，沒有改行為；補上測試，並更正報告的說法（稽核表的「idempotent」一列） | 不需 DB：`TestFingerprint`（同一 body、另一個 proposal 的 fingerprint 不同）。DB：`TestTheKeyNamesTheProposal`（同一 model、同一 base、同一 body 的兩個 ready proposal：approve 與 reject `{}` 的第二次都是 `idempotency.ErrConflict`，第二個 proposal 不變，model 不變）；`TestApprovalRefusals` 的 HTTP case（approve 與 reject 各一，409 `IDEMPOTENCY_KEY_CONFLICT`，proposal 仍是 ready） |
| L1 | Replay 的 conflict 409 帶第一次 request 的 `request_id`，與這次 response 的 `X-Request-ID` 不同（repo 的 `code()` helper 要求兩者相同） | 既有的 idempotent endpoint（T-009 projects、T-013 models、T-023 runs、T-025 proposals）只存成功的回答，錯誤 envelope 一律帶這次的 request。沿用這個做法：`replayed` 重送 byte 相同的回答，只有錯誤回答的 `error.request_id` 改為這次的 ID；第一次的 ID 留在 `proposal.conflict` 的 audit。規則寫進 D-069 (a) 與 OpenAPI | 不需 DB：`TestReplayed`。DB：`TestApprovalOnAMovedBase`（replay 的 body 等於第一次的、只有 request_id 換成 retry 的，而且與第一次不同）；HTTP：`TestApprovalRefusals`（replay 經 `code()` 比對 `X-Request-ID`，其餘 body 相同）。這是改了既有的 assertion（原本要求 byte 相同），新的 assertion 更嚴格，不是放寬 |
| L2 | D-069 (b)「proposal 的 `dataset_bindings` 不是 base 的整份 manifest：500，不批准」沒有測試（mutant `manifest-equality-removed` 存活） | 測試；程式不變 | DB：`TestApprovalFreezesTheManifest` 以 schema owner 把一個 ready proposal 的 `candidate_dataset_bindings` 改成 `[]`（base 綁一個 dataset）：批准回傳不屬於任何 typed refusal 的錯誤（500），proposal 仍是 ready，model 不變 |
| L3 | D-003 的鎖順序只測了一部分（mutant `lock-draft-before-model` 存活；若退化，會與 PatchDraft／publish 死結） | 測試；程式不變 | DB：`TestDecisionsTakeTheModelRowFirst`：另一個 transaction 持有 draft row，approve（以及 reject）開始後，以 `pg_stat_activity` 等到它在 `twin.model_drafts` 的 `FOR UPDATE` 上等待；這時 holder 執行 `SELECT … FROM twin.models … FOR UPDATE NOWAIT`，必須得到 55P03（批准已持有 model row）。釋放後決策完成 |
| L4 | `TestDecisionsNeedTheirState` 的跨 organization case 用 generating proposal，光靠 status filter 就擋下了（mutant `storage-reject-other-org` 存活） | 測試；程式不變 | DB：`TestDecisionsStayInTheirOrganization`：以不受 RLS 限制的 superuser，在一個 rollback 的 transaction 中產生 ready 與 needs_input proposal，以另一個 organization 執行 Approve、Conflict、Reject（ready）與 Reject（needs_input），都必須是 `ErrNotDecidable`，proposal 不變 |
| L5 | 報告與文件不準確 | 報告開頭、NOT RUN 與殘餘風險改為引用 coordinator 的兩次執行；commit 表並列 branch 與 worktree 的 hash；`docs/CONTRACT_BLOCKERS.md` 的 CB-42 與 `docs/SECURITY.md` §5 引用 coordinator 的執行；`approval.go` 的 package doc 改為 `decisions-only-in-the-api`；D-068 (a)、API_DATABASE §8、本報告改為「worker 沒有 UPDATE 權限」（它以表級 SELECT 讀到這個欄位，例如 generator 的 `store.Lock`）。Migration 0013 第 10 行的註解「The worker cannot name the column」也不精確，但已部署的 migration 不可修改，所以沒有改；`proposal_decisions.sql` 的 template 註解已改並重新 render | — |
| I2 | `datasets.Resolve` 的 infrastructure 錯誤回 500，D-069 只寫了「沒有 object storage 503」，generator 把同一類錯誤當成可重試的 `DATASET_STORAGE_FAILED` | `bindingFailure`：binding stage 回傳錯誤（不是 issue），而且 request 本身沒有被取消或逾時時，回 `DatasetStorageError`，HTTP 為 503 `SERVICE_UNAVAILABLE`、`details[0].code` `DATASET_STORAGE_FAILED`；原因由 handler 寫入 log，不送給 client。寫進 D-069 (b) 與 OpenAPI（approveProposal 的 503 有兩個範例）。`publishVersion` 與 `createSimulation` 對同一類錯誤仍回 500，D-069 (b) 請 owner 決定是否一併修改 | 不需 DB：`TestBindingFailure`、`TestRefused`（503、detail code，不含原因的文字）。DB：`TestApprovalFreezesTheManifest`（讀取失敗的 object storage：`DatasetStorageError`，什麼都不變） |
| I3 | Fingerprint 用原始的 canonical body，所以 `{}` 與 `{"comment":""}`（依 D-068 意思相同）不同；API_DATABASE §7 寫的是「normalized parsed body」 | `fingerprint` 先把 body 解析成兩種決策共用的 `decisionBody`（`expected_base_version_id`、`comment`，空值省略），schema 沒有的 member 直接拒絕，再 canonical 化。寫進 D-069 (a) 與 OpenAPI | 不需 DB：`TestFingerprint`（member 順序、空白、空 comment 都不影響；base、comment 不同就不同；未知 member 為錯誤）。DB：`TestTheKeyNamesTheProposal` 與 HTTP 的 `TestApprovalRefusals`（同一 key 先送 `{}` 再送 `{"comment":""}`，replay 第一次的 200） |
| I4 | D-069 (a) 寫「role first」，但 400／415／422 與 Idempotency-Key header 的檢查在 403 之前（與其他 handler 相同） | D-069 (a)、API_DATABASE §4、AI_SPEC §6、OpenAPI 與 package doc 都寫明：request 本身的檢查在 role 之前 | 既有的 `TestApprovalRefusals`（viewer 的 403、bad JSON 的 400 等） |
| I1 | Re-review 失敗（422）或 server risk 改變（409）的 ready proposal 會一直是 ready，永遠無法批准 | 記入 D-069 (b) 給 owner 選：B1（建議，即現在的行為）被拒的批准不寫入，UI 提示 reject 後重提；B2 第一次失敗就轉 conflicted（要新增記錄原因的欄位）；B3 轉 invalid（要新 migration 放寬 0001 的 transition 與 0012 的 CHECK）。B2、B3 都會改 state machine，不是 local 也不容易撤回的修改，所以沒有實作 | — |

**先紅後綠**（不需 DB 的部分；`unit-red.log`、`unit-green.log`）：先把 replay 與 binding failure 抽成 `replayed`、`bindingFailure` 兩個函式，行為不變；再寫 `TestFingerprint`、`TestReplayed`、`TestBindingFailure` 與 `TestRefused` 的新 case。Red（exit 1）：`{}` 與 `{"comment":""}` 的 fingerprint 不同、未知 member 被接受、replay 的 409 仍是第一次的 request_id、讀取失敗沒有成為 `DatasetStorageError`、`Refused` 沒有對應 503。修改後 green（exit 0）。`TestFingerprint` 中「另一個 proposal」的部分在現有程式上本來就通過，它擋得下的證據是 mutant。

**Mutants**（`r1-mutate.py`、`r1-mutants.log`，沒有 DB）：共 15 個。不需 DB 的 10 個全部被擋下：fingerprint 的 4 個（忽略 proposal、忽略 body、不 normalize、接受未知 member）由 `TestFingerprint` 擋下，replay 的 2 個由 `TestReplayed` 擋下，binding failure 的 2 個（storage 錯誤當 500、取消當 storage 錯誤）由 `TestBindingFailure` 擋下，503 對應的 2 個（沒有對應、少了 detail code）由 `TestRefused` 擋下。另外 5 個標為 db-only：`apply-ignores-binding-failure`、`manifest-equality-removed`、`lock-draft-before-model`、`storage-reject-other-org`、`storage-approve-other-org`。它們只有 DB-backed 測試會經過，沒有 DB 時如預期存活，要等 coordinator 帶 database 執行

**Reviewer 的 harness**（`t026-review-mutate.py`，唯讀，未修改；本 worktree 沒有 DB，結果在 `review-harness-mutants.log`）：25 個中有 4 個被擋下，reviewer 執行時是 2 個。新增的兩個是 `fingerprint-ignores-proposal` 與 `fingerprint-ignores-body`，現在不需 DB 就由 `TestFingerprint` 擋下；`empty-comment-kept`、`pool-not-rechecked` 與之前相同。其餘 21 個改的都是只有 DB-backed 測試會經過的路徑（鎖、狀態、audit、storage 的 SQL），沒有 DB 時存活，與 reviewer 的結果相同。其中 L2–L4 的三個（`manifest-equality-removed`、`lock-draft-before-model`、`storage-reject-other-org`）現在有對應的 DB 測試。`claim-is-stored-effective-risk` 在結果上與原程式等價：effective risk 是 max(server, claim)，以它代替 claim 重算，得到的仍是同一個 risk，而 server risk 另外比較

**Round 1 在本 worktree 的執行**（先 `go clean -testcache`；`exit-codes.txt`、`*.log`）：

環境（`environment.txt`）：Go 1.27.2、Node 22.22.0、4 CPU，沒有 PostgreSQL server。程式在 00e962a；本報告是之後的 commit。

| 指令 | Exit | 結果 |
|---|---|---|
| `make spec-check PYTHON=.venv/bin/python` | 0 | `PASS_LIMITED_SPEC_CHECKS`（42 個 OpenAPI operation 的範例都依 schema 驗證，包括新的 503 與 `IDEMPOTENCY_KEY_CONFLICT` 範例）；planning、decisions（proposed 6：T-026 的 D-067–D-069 與 T-017 的 D-070–D-072）、TS 契約（15 檔、106 型別）、hash、`render_db_tests --check`（12 檔）都 PASS；重產的 `checks/*.json` 沒有差異。加入本節後再跑一次，見 `spec-check-with-report.log` |
| `make backend-test` | 0 | `go vet` 與 `go test`：61 個 package `ok`，沒有 FAIL；DB-backed 測試都 SKIP |
| `make backend-race` | 0 | `go test -race`：61 個 package `ok` |
| `make architecture-check` | 0 | `PASS architecture: 74 packages, 16 rules, 16 build configurations` |
| `make ai-eval` | 0 | `TestFakeProviderEval`、`TestReviewEval` PASS（只有 fake provider） |
| `make ts-contracts PYTHON=.venv/bin/python` | 0 | 15 檔、106 型別，`git status` 沒有差異（round 1 沒有改 schema） |
| `make frontend-test` | 2 | 本機沒有 `apps/web/node_modules`（`eslint` 為 `ERR_MODULE_NOT_FOUND`），Node 22.22.0 也不符 `>=24.21.0 <25`；沒有安裝。Round 1 沒有改 TS 或 schema；coordinator 上一次在那裡 exit 0 |
| `go test -count=1 -v`（T-026 相關 package） | 0 | 36 個 PASS（多了 `TestFingerprint`、`TestReplayed`、`TestBindingFailure`）、108 個 SKIP（都是 DB-backed，包括新的 `TestTheKeyNamesTheProposal`、`TestDecisionsTakeTheModelRowFirst`、`TestDecisionsStayInTheirOrganization`）、0 個 FAIL |
| `make integration-test` | — | NOT RUN（本 worktree 沒有 PostgreSQL；見下） |

**請 coordinator 執行的 DB 測試**（`db-not-run.txt`）：在新的 `dt_test_*` database 上跑整個 `make integration-test`（0001–0014 up、所有 SQL suite，包括 render 後只改了註解的 `proposal_decisions.sql`，以及 db-go-test）。Round 1 新增的 DB 測試：`internal/proposals/approval` 的 `TestTheKeyNamesTheProposal`、`TestDecisionsTakeTheModelRowFirst`，`internal/storage/proposals` 的 `TestDecisionsStayInTheirOrganization`。Round 1 修改過、需要重跑的 DB 測試：`TestApprovalOnAMovedBase`（replay 的 request_id）、`TestApprovalFreezesTheManifest`（storage 失敗、manifest 被竄改）、`TestApprovalReviewsAgain`（tamper 改為共用的 helper），以及 `internal/api` 的 `TestApprovalRefusals`（M1、L1、I3 的 HTTP case）。其他 approval 的 DB 測試也要重跑，因為 `Approve`／`Reject` 的 fingerprint 改了。建議再帶 `GEMINUS_TEST_DATABASE_URL` 跑一次 `r1-mutate.py`：五個 db-only mutant 應該全部被擋下。

## NOT RUN

- **在本 worktree 執行的 DB 測試：無**（沒有 PostgreSQL server；依 coordinator 的指示沒有嘗試 su、runuser 或其他方式）。下面第一版與第二輪的 DB 測試，coordinator 已在第二次執行（95b64e7）中跑過且通過；round 1 要重跑的清單在「Review round 1」與 `checks/evidence/2026-10-20-T-026-r1/db-not-run.txt`。
  - （第二輪時）修正後必須重跑：`make integration-test` 全部。最少也要跑先前失敗的 `TestApproveScenario`、`TestApprovalOnAMovedBase`、`TestDecisionsWaitForTheModelRow`、`TestApprovalClosedLoop`，以及 helper 被改到、上一次通過的其他 approval DB 測試與 `TestApprovalRefusals`。
  - SQL：`db/tests/proposal_decisions.sql`、`db/tests/constraints.sql`、migration 0013 的 up／down／up 已在 coordinator 的兩次執行中通過。
  - 第一版新增的 DB-backed Go 測試：`internal/proposals/approval` 的 `TestApproveEdit`、`TestApproveKeepsADirtyDraft`、`TestApproveScenario`、`TestApprovalIdempotency`、`TestDecisionRaces`、`TestApprovalOnAMovedBase`、`TestDecisionRoles`、`TestDecisionsWaitForTheModelRow`、`TestRejectAndStates`、`TestApprovalReviewsAgain`、`TestApprovalFreezesTheManifest`；`internal/api` 的 `TestApprovalClosedLoop`、`TestApprovalRefusals`；`internal/storage/proposals` 的 `TestDecisionsNeedTheirState`。
  - 受影響、必須重跑的既有 DB 測試：`internal/proposals/...`（generator 讀的欄位多了決策紀錄）、`internal/storage/proposals`、`internal/models/...`（`Advance`）、`internal/storage/models`（`LockDraft`）、`internal/api/...`、`internal/worker`、`cmd/api`、`cmd/worker`，以及所有 SQL suite。
- `make frontend-test`：執行了但 exit 2：`apps/web/node_modules` 不存在（`eslint` 為 `ERR_MODULE_NOT_FOUND`），且本機 Node 22.22.0 不符 `package.json` 的 `>=24.21.0 <25`；沒有為此安裝 Node 或依賴，因此 lint、typecheck、unit 都未實際執行，不能列為 PASS。`contracts.test.ts` 的新正反例與 typecheck 都沒有執行；只有 `gen_ts_contracts --check` 確認產生的 TS 是最新的。
- `make e2e-test`：UI 尚無 proposal 審核畫面（T-031）。
- Live AI、`make ai-eval-live`：live AI 未核准（H-001、H-017），批准本身也不呼叫 provider。
- 全面的 mutation harness（`mutants.py`、`sql-mutants.py`）；只跑了上述 targeted mutant，SQL 與 DB-backed 的路徑沒有 mutant。

## 需要 owner 決定（PROPOSED，agent 沒有批准）

- **D-067**（CB-33）：AI scenario 批准所建立 scenario 的名稱。A（建議，已實作）取自 summary；B 由批准者在 Approve 中命名（contract 變更）；C 固定格式。另請確認：base 變舊後是否仍應能批准 AI scenario（目前依 D-062 (b) 與 AI_SPEC §6 不可以）。
- **D-068**（CB-42 (a)）：決策紀錄。A（建議，已實作）以 0013 的 `decision_comment` 保存 comment（edit 的 comment 也是新 version 的 comment），conflicted 記錄發現者，Proposal 顯示 `decided_by`、`decided_at`、`decision_comment`；B 不保存並拿掉 comment；C 放 audit。
- **D-069**（CB-42 (b)–(e)）：檢查順序與錯誤碼（request 的檢查在 role 之前；conflicted 也算「已決定」；conflict 的 409 會寫入並存為 key 的回應，replay 時 `error.request_id` 為這次的 request；fingerprint 為 proposal ID 與 normalized parsed body；指定了別的 base 時什麼都不變）、revalidation（risk 與 review 時不同就拒絕；其他選項：只在變高時拒絕，或照新 risk 批准；snapshot 讀不到為 503 `DATASET_STORAGE_FAILED`，`publishVersion` 與 `createSimulation` 是否一併改請 owner 決定；revalidation 失敗的 proposal 維持 ready，B1／B2／B3）、reject、audit metadata、0013 的 DB 保證。
- Gate：tasks.json 中 T-026 加了三項，T-028 加了 D-067；`checks/decisions-check.json` 的 proposed 現在是 6（T-026 的三項與 T-017 的 D-070–D-072）。

## 殘餘風險

- Review round 1 新增與修改的 DB 測試（`TestTheKeyNamesTheProposal`、`TestDecisionsTakeTheModelRowFirst`、`TestDecisionsStayInTheirOrganization`、`TestApprovalOnAMovedBase`、`TestApprovalFreezesTheManifest`、`TestApprovalReviewsAgain`、`TestApprovalRefusals`）已由 coordinator 在一次性 database 上執行並通過；帶 DB 的 15 個 mutant 全部被擋下（見文末「Coordinator 的執行（review round 1 之後）」）。
- `TestTheDBTestsAnswersAreReady` 的表格是手寫的，對應 DB 測試詢問的 base；DB 測試以後改了 base 或 changes，表格不會自動跟著改。`TestApprovalFreezesTheManifest` 新增的 proposal 是在綁 dataset 的 base 上，表格沒有這個 base。
- `TestDecisionsTakeTheModelRowFirst` 以 `pg_stat_activity` 的 query 文字（`FROM twin.model_drafts … FOR UPDATE`）找出等待中的決策；若 `LockDraft` 的 SQL 改寫成別的形式，這個測試會在 10 秒後以「no statement … waits」失敗，而不是默默通過。
- Migration 0013 第 10 行的註解說 worker「cannot name the column」，不精確（worker 讀得到，只是沒有 UPDATE 權限）；已部署的 migration 不可修改，正確的說法在 D-068 (a)、API_DATABASE §8 與 SQL 測試的註解。
- 批准在持有 model row 的 transaction 中重讀 dataset snapshot（與 publish、建立 run 相同）；manifest 大時（至多 1000 個 binding、每個至多 4 MiB）會延長 lock 的時間，沒有量測。
- 批准後回傳的 `scenario_id` 在 T-028 之前不能以 `GET /v1/scenarios/{id}` 讀取（route 尚未實作）；`createSimulation` 已能以 `scenario_id` 執行它（T-023）。
- D-067 的名稱是 provider 寫的 summary，scenario 不可修改，名稱之後無法更正；UI 必須 escape。
- Risk 規則改變（例如 T-047 在 `internal/ai/review` 加入 D-057 的 risk）之後，risk 因此改變的既有 ready proposal 都會在批准時回 409 `LIFECYCLE_CONFLICT`，只能拒絕後重新提出；這是 D-069 (b) 建議的行為，與 T-047 合併後需要知道。
- Base 已變而沒有人嘗試批准的 proposal 會一直顯示為 ready；UI（T-031）應以 base 與 latest 比較來提示。
- Conflict 的 409 存為 key 的回應；review round 1 起 replay 時 `error.request_id` 改為這次 request 的 ID（D-069 (a)，PROPOSED），第一次 request 的 ID 只留在 `proposal.conflict` 的 audit event。
- TS client 沒有 approve／reject 方法，前端的型別測試沒有執行。
- `internal/storage/scenarios`（`Insert`、`Get`）沒有自己的測試檔，只由 approval 的 DB 測試（`TestApproveScenario`、`TestApprovalClosedLoop`）間接涵蓋；兩者自第 2 輪起在 coordinator 的每次 integration 中都通過。
- `TestDecisionRoles` 與 `TestDecisionsWaitForTheModelRow` 以 500 ms 判斷「沒有等待」（與 T-012 的鎖測試相同的做法），在很慢的機器上可能誤報。

## Coordinator 的執行

第 1 輪 cherry-pick 到 `claude/lucid-meitner-vq1awo`（0ee3d2a 之後，f595f07）後，`make integration-test` 在一次性的 `dt_test_t026` 上 exit 2：0013 的 up／down／up 與所有 SQL suite 通過，4 個 DB-backed Go 測試失敗（TestApproveScenario、TestApprovalOnAMovedBase、TestDecisionsWaitForTheModelRow、TestApprovalClosedLoop），原因見「第二輪」：測試 helper 以 modeler role 寫入 scenario 的 fake 回答，scenario proposal 停在 needs_input。Coordinator 把本機 branch 退回 0ee3d2a，交回 agent 修正（只改測試）。

第 2 輪後七個 commit 一起 cherry-pick（95b64e7），`spec-check`、`backend-test`、`backend-race`、`architecture-check`、`ai-eval`、`frontend-test`（426 個 test）與 `integration-test`（新的 `dt_test_t026_r2`：27 個 package ok，沒有 FAIL 或 SKIP；scenario 批准的整條路徑第一次在 PostgreSQL 上執行並通過）都是 exit 0。Log 與 exit code 在 `checks/evidence/2026-10-20-T-026/coordinator-*`。上方「NOT RUN」中的 DB 測試與 `make frontend-test` 已由 coordinator 執行。

### Coordinator 的執行（review round 1 之後，5dd554c、d80feb5）

Round 1 的四個 commit 在 T-047 之後 cherry-pick，沒有衝突。`spec-check`、`backend-test`、`architecture-check`、`ai-eval`、`frontend-test`（429 個 test）與 `backend-race` 都是 exit 0。`integration-test`（一次性的 `dt_test_t026_r1b`）中所有 migration 與 SQL suite、29 個 Go package（含本輪新增與修改的 approval DB 測試）都通過，唯一失敗是 T-026 範圍外的 T-019 計時測試 `TestASlowRenewalCountsFromItsStart`（4.34 s，期望 3.5 ± 0.3 s；機器的 load average 為 18–19，4 顆 CPU）。在新的 `dt_test_t026_r1c` 上重跑一次：該測試 3.66 s 通過，整個 `internal/worker` 也通過；沒有放寬任何 assertion。之後以同一個 database 執行 `r1-mutate.py`（coordinator 的副本加上 `-p 1`）：15 個 mutant 全部被擋下，包括 5 個只有 DB 測試能擋的。Log 在 `checks/evidence/2026-10-20-T-026-r1/coordinator-*`。

## Review 第 2 輪（H-003，5dd554c）：ACCEPT

同一位獨立 reviewer 以 `git archive` 匯出的 5dd554c 複審：M1、L1–L4 已修正，L5 大致修正，I1 交給 owner（D-069 (b)），I2–I4 已修正；沒有新的 High 或 Medium。Reviewer 也認同 worker 計時失敗與 T-026 無關（`internal/worker` 在 0ee3d2a..5dd554c 之間沒有變更）。新的 Low 由 coordinator 修正（本節的 commit）：

- **N1**：`TestDecisionsTakeTheModelRowFirst` 的 holder transaction 在 `Begin` 後加上 `defer holder.Rollback(ctx)`，`t.Fatalf` 的路徑不會留下鎖住的 draft row 與借出的連線，避免 pool 的 `Close` 等到 go test 的 timeout。已在一次性 database 上執行（`checks/evidence/2026-10-20-T-026-r2/n1-db.log`）。
- **N2**：本報告開頭、殘餘風險與 coordinator 一節，以及 `docs/SECURITY.md`、`docs/CONTRACT_BLOCKERS.md`（CB-42）中「round 1 的 DB 測試待重跑」的說法，改為引用上面的執行。
- **N3**：`TestBindingFailure` 加上「request 已取消、store 回普通錯誤」的案例（不是 `DatasetStorageError`）。紅：拿掉 `bindingFailure` 的 `ctx.Err()` 條件時 exit 1（`n3-red.log`）；綠：還原後 exit 0（`n3-green.log`）。

Info（記錄，不修）：I-a request 仍有效而 object store 自己逾時時被當成 request 逾時（一般 503、沒有 `DATASET_STORAGE_FAILED`），`Resolve` 中 server 自己的錯誤也回 503，可在之後收窄分類；I-b 兩個 equivalent mutant；I-c 只有含空 comment 的 body，fingerprint 在部署前後不同（首次部署前沒有影響）；I-d L2 的測試只 assert 不是 typed 錯誤。T-019 的計時測試對 CPU 飢餓敏感，記為 T-019 的後續。

## Owner 批准（H-021，2026-10-25）

Owner 對決策頁（https://claude.ai/artifact/T1xUsmk9ToV274qsPVRKqm）回覆「全都照建議」：D-067、D-068、D-069 依建議 APPROVED（`planning/decisions.json`、`docs/DECISIONS_AND_TBD.md`）。本報告前文寫作時這些決策仍是 PROPOSED，前文保留原貌；T-026 因此標為 done。
