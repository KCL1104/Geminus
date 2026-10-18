# T-010 驗證報告：受控 onboarding 與成員管理

**日期：2026-10-18｜狀態：`review`（實作與自我驗證完成，送獨立 review；依 H-003 驗收）｜需求：REQ-001、REQ-002、REQ-019｜Gate：D-008、D-012、D-036（皆已批准）｜實作選擇：ADR-023（PROPOSED）｜驗收測試：TEST-009（membership 部分）、TEST-011（provisioning 部分）；TEST-026 見 NOT RUN**

逐條輸出在 `checks/evidence/2026-10-18-T-010/`。範圍依 tasks.json：

- operator-only 的初始 org／owner／user provisioning；
- owner-only 的成員 list／add／update／remove（已知的 user）；
- 鎖定 organization 保留最後一位 owner；撤權立即影響寫入；
- 成員變更不在 `Run(ReadWrite)` 的 `FOR SHARE` 之上取 D-036 的有序鎖（T-009 re-review R1），並以兩個 owner 並行變更證明無 deadlock。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/tenant/owners.go` | `DB.RunOwners`：先依 user_id 順序 `FOR UPDATE` 鎖住全部 owner membership，再讀 caller 的 role；不先以 `FOR SHARE` 鎖 caller 自己的 row；caller 在等鎖時被升為 owner 時再鎖一次 |
| `internal/tenant/memberships` | Service：`List`、`Create`（Idempotency-Key）、`Update`、`Delete`；最後一位 owner、audit、cursor；HTTP handler 與 `Routes`（path 的 organization 必須是選定的） |
| `internal/storage/memberships` | twin.memberships 的 SQL，每個 statement 帶 organization filter；新增以 `ON CONFLICT DO NOTHING` 分辨已是成員，FK 分辨未知 user |
| `internal/storage/provisioning`、`cmd/provision-tenant` | Operator 的 provisioning：`user` 與 `organization` 指令；login 必須能 `SET ROLE twin_owner` 且不是 runtime login 或 superuser；建立 organization 時同一 transaction 建立 owner membership 與 audit `organization.provision` |
| `internal/httpapi` | `IdempotencyKey`、`PageQuery`、`InvalidCursor` 從 project 移出共用 |
| `internal/api` | 註冊 membership routes |
| `Makefile` | `db-go-test` 加入 `./cmd/provision-tenant/...` |
| 文件 | ADR-023（PROPOSED）；ADR-019 的 R1 註記；db/README.md 的 provisioning login 與測試表 |

## 實際執行結果

所有指令都在 d83a1af（`commit.txt`）的乾淨 worktree 上執行（`git worktree`，先 `go clean -testcache`；log 中沒有 cached 結果），exit code 見 `exit-codes.txt`。CI：d83a1af 的 run [39164574740](https://github.com/KCL1104/Geminus/actions/runs/39164574740) 4/4 success（含 PostgreSQL 16 的 `make integration-test`）。DB 測試用一次性的 PostgreSQL 16 cluster 與新建的 `dt_test_t010`。

| 指令 | Exit | 說明 |
|---|---|---|
| `make backend-test`、`backend-race` | 0 | `backend-test.log`、`backend-race.log` |
| `make architecture-check` | 0 | `PASS architecture: 38 packages, 10 rules, 16 build configurations`（多了 memberships、provisioning 與 `cmd/provision-tenant`） |
| `make spec-check` | 0 | `spec-check.log` |
| `make integration-test`（新建的 `dt_test_t010`） | 0 | `integration-test.log`：migration up／down／up、SQL tests、`db-go-test`（含 `./cmd/provision-tenant/...`） |
| `go test -race -p 1 -v`（tenant、tenant/memberships、storage/memberships、storage/provisioning、cmd/provision-tenant、api） | 0 | `t010-go-tests.log`：38 個 test，0 SKIP |
| mutation（`mutants.py`） | 0 | 48 個：46 killed，2 equivalent，0 unexpected（見下） |

先紅後綠：

- `red-owner-deadlock.log`：`RunOwners` 先以天真版本（在 `Run(ReadWrite)` 的 `FOR SHARE` 之上取 D-036 的鎖）執行 `TestOwnersChangeAtOnce`：15 輪中 11 輪 `40P01` deadlock（11.7 秒）；改為先取有序鎖再讀 role 後全過（0.7 秒，`green-memberships.log`）。這是 T-009 re-review R1 要求的證明。
- 其他測試與程式同時寫成，沒有先紅的紀錄；它們確實會擋下錯誤由 mutation 證明。

### Mutation

第一次全量執行（`mutation-full.log`）：48 個，36 killed，12 個未擋下，全部處理後以 `--only` 重跑這 12 個（`mutation-rerun.log`）：

| Mutant | 問題 | 處理 |
|---|---|---|
| `owners-scope-unchecked` | 沒有以無效 scope 呼叫 `RunOwners` 的測試 | `TestScopeIsValidatedBeforeTheDatabase` 加入 `RunOwners` |
| `owners-no-scope`、`sql-list-after-inclusive` | 逾時（harness 錯誤）：`TestRemovalWaitsForAWrite` 失敗時沒有放開成員的寫入；`TestList` 在 cursor 不前進時無限迴圈 | 失敗時一定放開寫入；分頁最多 n+1 次。重跑皆 killed |
| `cursor-not-canonical` | 同位置的另一種寫法沒有測試 | `TestList` 加入加了空白的 cursor |
| `sql-get/list/setrole/delete-any-org`、`sql-delete-rows-unchecked` | RLS 擋住了，organization filter 本身沒有測試 | `internal/storage/memberships` 以 superuser（RLS 不限制）執行的 `TestOrganizationFilter`、`TestListAfter` |
| `prov-owner-unchecked` | 沒有「既非 runtime 也非 owner」的 login 測試 | `TestCheckOperator` 加入沒有任何 role 的 login |
| `owners-unordered` | 列為 equivalent | 只有 `RunOwners` 鎖 owner rows，且都是同一個 query、同一個 plan，彼此遇到 row 的順序本來就相同；user_id 順序是 D-036 對其他鎖 owner rows 的程式（T-007 的 SQL 測試）的約定，本 package 的測試分不出來 |
| `update-target-unlocked` | 列為 equivalent | 所有成員變更都在 `RunOwners` 內並全程持有 owner 鎖，Get 與 SetRole 之間沒有其他變更能改到目標；成員自己的寫入以 `FOR SHARE` 鎖 row，SetRole 的 UPDATE 本來就會等。保留目標 row 的鎖，防範日後不經 `RunOwners` 的寫入者 |

## 驗收對照

| 測試 | 驗證內容 | 對應 |
|---|---|---|
| `TestRoles` | editor、viewer 對 list／add／update／delete 一律 403；非成員與其他 organization 的 owner 為 not a member（404）；被拒的呼叫不改任何東西、不寫 audit；owner 碰不到其他 organization 的成員 | TEST-009、SECURITY §2 |
| `TestCreate` | 201 且符合 Membership schema；新成員立刻能以該 role 工作；同 key 同 body 重播位元組相同；同 key 不同 body 衝突；已是成員 409；未知 user 422；非 user ID 或 role 被拒；audit `membership.create`（只有 role） | D-011、D-008 |
| `TestUpdate` | 唯一 owner 不能降級；升級、相同 role 不寫入；兩位 owner 時可自行降級，之後另一位就是最後一位；非成員 404；audit 含先前 role | D-036 |
| `TestDelete` | 唯一 owner 不能移除；被移除者下一個 request 被拒；再移除 404；audit | D-036、撤權 |
| `TestRemovalWaitsForAWrite` | 成員已開始的寫入未結束前，移除等待；寫入完成後移除生效，下一個寫入被拒 | ADR-019 |
| `TestOwnersChangeAtOnce` | 15 輪兩位 owner 同時互相降級，並夾雜 owner 自己的寫入：每輪恰一個成功、另一個被拒、剩一位 owner，沒有 deadlock 或 503 | T-009 re-review R1、D-036 |
| `TestPromotedWhileWaiting` | 等鎖時被升為 owner 的 caller 可降級另一位 owner（再鎖一次，不以不完整的集合誤判為最後一位） | ADR-023 |
| `TestScopeIsValidatedBeforeTheDatabase`（internal/tenant） | 無效的 organization／user ID 在碰資料庫前即被拒（`Run` 與 `RunOwners`） | D-008 |
| `TestOrganizationFilter`、`TestListAfter`（storage/memberships） | 以 superuser 執行時 organization filter 本身讓 a 讀不到、改不到、刪不到 b 的成員；keyset 從給定 user ID 之後開始 | AGENTS.md（application filter 與 RLS 並存） |
| `TestList` | 以 user_id 分頁不重不漏、符合 Memberships schema；cursor 不能用於其他 organization、竄改即拒；limit 0／101 被拒 | API_DATABASE §3 |
| `TestRoutesMatchContract` | Routes 的 method、path、role 等於 api-operations.json | D-012 |
| `TestMembershipRoutes`（internal/api） | 整個 HTTP stack：owner 200、editor 403、其他 organization 404、path 與 header 的 organization 不同 404、401；缺 Idempotency-Key 400；409／422 的 code；PATCH、DELETE 204（無 body、有 X-Request-ID）；被移除者下一個 request 404；3 筆 audit | TEST-009 |
| `TestCheckOperator`、`TestAPICannotProvision`、`TestProvision`（storage/provisioning） | Operator login（`NOINHERIT … IN ROLE twin_owner`）可以，superuser、API login 與沒有任何 role 的 login 被拒；API login 即使設了 RLS 讀的 setting 也不能 INSERT users／organizations（42501）；user 依 issuer／subject 只建一次；organization 有 owner 與 system audit，owner 立刻能解析並以 owner 身分工作；不合格的名稱與 identity 被拒 | D-008、TEST-011 |
| `TestProvision`、`TestRefusals`（cmd/provision-tenant） | 輸出只有 ID 的 JSON；API login、superuser exit 4；用法與輸入錯誤 exit 2，且沒有輸出 | D-008 |

## 需 owner 知悉

- ADR-023 為 PROPOSED：provisioning 指令與 login 規則（superuser 也拒絕）、成員 API 的錯誤碼（最後一位 owner 與已是成員為 409 LIFECYCLE_CONFLICT、未知 user 為 422）、PATCH 相同 role 不寫入、列表依 user_id 排序。
- 部署時需要為 provisioning 建立一個 `NOINHERIT … IN ROLE twin_owner` 的 login（db/README.md）。

## 與規劃的差異

- `scripts/provision-tenant/` 改為 `cmd/provision-tenant`：architecture check 只允許 cmd 開啟資料庫連線；SQL 在 `internal/storage/provisioning`。
- Service 在 `internal/tenant/memberships`（tasks.json 的 planned output），SQL 依慣例在 `internal/storage/memberships`。

## NOT RUN／延後

| 項目 | 原因／歸屬 |
|---|---|
| 撤權時關閉 SSE stream（TEST-026） | SSE 尚不存在（T-023）；撤權後的下一個 request 已被拒（`TestDelete`、`TestMembershipRoutes`） |
| 前端成員管理畫面 | 不在 T-010 scope；UI 依 T-045 的 design gate |
| 真實 IdP 的 subject 與 provisioning | Auth0 tenant 尚未建立（ADR-018／T-043）；測試用 test OIDC provider |
