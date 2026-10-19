# T-010 驗證報告：受控 onboarding 與成員管理

**日期：2026-10-18｜狀態：`review`（review round 1 CHANGES REQUESTED 已修正，並實作 owner 批准的 D-042；送同一 reviewer 複核；依 H-003 驗收）｜需求：REQ-001、REQ-002、REQ-019｜Gate：D-008、D-012、D-036（皆已批准）｜實作選擇：ADR-023（ACCEPTED，H-009）、D-042（APPROVED）｜驗收測試：TEST-009（membership 部分）、TEST-011（provisioning 部分）；TEST-026 見 NOT RUN**

逐條輸出在 `checks/evidence/2026-10-18-T-010/`。範圍依 tasks.json：

- operator-only 的初始 org／owner／user provisioning；
- owner-only 的成員 list／add／update／remove（已知的 user）；
- 鎖定 organization 保留最後一位 owner；撤權立即影響寫入；
- 成員變更不在 `Run(ReadWrite)` 的 `FOR SHARE` 之上取 D-036 的有序鎖（T-009 re-review R1），並證明無 deadlock（round 1 後以三個以上 owner 的隨機壓力測試）。

Review round 1 的發現與處理見文末；以下各節已更新為修正與 D-042 之後（e2faab3）的狀態。

## 交付內容

| 位置 | 內容 |
|---|---|
| `internal/tenant/owners.go` | `DB.RunOwners`：不上鎖讀 caller 的 role，非成員 404、非 owner 403，不取任何鎖；取該 organization 的 transaction 層級 advisory lock（成員變更一次一個）；依 user_id 順序 `FOR UPDATE` 鎖住全部 owner membership（D-036）；再讀一次 caller 的 role |
| `internal/tenant/memberships` | Service：`List`、`Create`（Idempotency-Key）、`Update`、`Delete`；最後一位 owner、audit、cursor；HTTP handler 與 `Routes`（path 的 organization 必須是選定的） |
| `internal/storage/memberships` | twin.memberships 的 SQL，每個 statement 帶 organization filter；新增以 `ON CONFLICT DO NOTHING` 分辨已是成員，FK 分辨未知 user |
| `internal/storage/provisioning`、`cmd/provision-tenant` | Operator 的 provisioning：`user` 與 `organization` 指令，以 `SET LOCAL ROLE twin_provisioner` 執行；login 必須能 `SET ROLE twin_provisioner`，不是 twin_owner 或 runtime role 的成員，沒有 SUPERUSER／BYPASSRLS／CREATEROLE／CREATEDB／REPLICATION；同一 identity 並行只建立一次（`ON CONFLICT`）；issuer／subject 依 ADR-017 verifier（`auth.ValidIdentity`）、名稱需可列印且無前後空白；建立 organization 時同一 transaction 建立 owner membership 與 audit `organization.provision` |
| `internal/auth` | `ValidIdentity`：verifier 會接受的 issuer 與 subject，給 provisioning 用 |
| `db/bootstrap_roles.sql`、`migrations/0004_provisioner.*`、`db/tests/roles.sql`（D-042） | NOLOGIN 的 `twin_provisioner`：只有 users 的 SELECT／INSERT 與 organizations、memberships、audit_events 的 INSERT，RLS 照樣適用；audit guard 改為每個 role 只能以自己的身分寫 event（API：scoped user；worker：worker 或 system；provisioning：system）；SQL 測試含完整權限矩陣與被拒的語句 |
| `internal/httpapi` | `IdempotencyKey`、`PageQuery`、`InvalidCursor` 從 project 移出共用 |
| `internal/api` | 註冊 membership routes |
| `Makefile` | `db-go-test` 加入 `./cmd/provision-tenant/...` |
| 文件 | ADR-023（PROPOSED；round 1 後改寫鎖的設計、等待關係、風險與待 owner 決定事項）；ADR-019 的 R1 註記（更正「確認無 deadlock」）；db/README.md 的 provisioning login 與測試表；tasks.json：T-023 scope 加入撤權時關閉 stream（L6） |

## 實際執行結果（review round 1 修正與 D-042 之後，e2faab3）

所有指令在 e2faab3 的乾淨 worktree 上執行（`git worktree`，先 `go clean -testcache`；log 中沒有 cached 結果），DB 為一次性 PostgreSQL 16 cluster 上新建的 `dt_test_t010_final`（Go mutation 用新建的 `dt_test_t010_mut`，SQL mutation 每個 mutant 新建 `dt_test_sqlmut`）。Log 在 `checks/evidence/2026-10-18-T-010/review1/`，exit code 見 `exit-codes.txt`。CI：e2faab3 的 run [39177185147](https://github.com/KCL1104/Geminus/actions/runs/39177185147) 成功（含 PostgreSQL 16 的 `make integration-test`）。

| 指令 | Exit | 說明 |
|---|---|---|
| `make backend-test`、`backend-race` | 0 | `backend-test.log`、`backend-race.log` |
| `make architecture-check` | 0 | `PASS architecture: 38 packages, 10 rules, 16 build configurations`（provisioning 改 import `internal/auth`，規則允許） |
| `make spec-check` | 0 | `spec-check.log` |
| `make integration-test`（新建的 `dt_test_t010_final`） | 0 | `integration-test.log`：migration 0001–0004 up／down／up、SQL tests、`db-go-test`；唯一的 NOT RUN 是 worker 整合（T-019） |
| `go test -race -p 1 -v`（tenant、tenant/memberships、storage/memberships、storage/provisioning、api、cmd/provision-tenant、auth） | 0 | `t010-go-tests.log`：77 個 test 通過，0 SKIP、0 FAIL |
| Go mutation（`mutants.py`，76 個） | 0 | `mutation-final.log`：76 個，73 killed、3 equivalent、0 unexpected |
| SQL mutation（`sql-mutants.py`，migration 0004，12 個） | 0 | `sql-mutation.log`：12 個全部 killed、0 unexpected |

CI 曾在 adbb22b 與 f2ce4b2 失敗：`TestOwnerLockAcrossPlans`（移植自審查者的 probe）把 `&options=…` 直接接在 database URL 後面，CI 的 URL 沒有 query，options 變成 database 名稱的一部分。e34aac6 改用 `net/url` 設定並把空格編成 `%20`（pgx 不把 `+` 當空格）；本機以 mutant 確認 options 仍有效（拿掉 advisory lock 與 ORDER BY 時 3 輪皆 40P01）。

先紅後綠（round 1）：

- `red-memberships.log`：新的 serialize 測試在修正前的程式上：`TestRefusedRequestsTakeNoLock` 兩個情形都等滿 2 秒；`TestOwnerSetIsCurrent` 三個變體都在有 2 位 owner 時被拒；`TestMembershipChangesUnderLoad` 13 個 40P01。修正後 `green-memberships.log`、`green-serialize-x3.log`（連跑三次，每輪 370–417 次成功變更）。
- `red-provisioning.log`：新的 provisioning 測試在舊程式上：4 個多餘屬性的 owner 被接受、14 個不合格輸入被接受、並行建立 user 得 23505。修正後 `green-provisioning.log`。
- `TestOwnerRowsLockedDuringAChange`、`TestOwnerLockAcrossPlans`、`TestCallerChangedWhileWaiting` 以 overlay mutant 確認會失敗（owner 列不上鎖、拿掉 advisory lock 與 ORDER BY、拿掉 role 重讀）。
- `red-roles-sql-without-0004.log`：新的 `db/tests/roles.sql` 在沒有 migration 0004 的 DB 上：API 寫 worker 與 system 的 audit event 成功（2 個 FAILED），provisioning role 連 schema 都不能用；套用 0004 後 SQL suites 全過（`integration-test.log`）。

### Mutation（round 1 後）

76 個 Go mutant（`mutants.py`），涵蓋 `RunOwners` 的每一步（不上鎖的檢查、advisory lock、owner 鎖、role 重讀、非成員／非 owner 的錯誤）、service、SQL、HTTP、provisioning（SET ROLE twin_provisioner、每個被拒的 role 與屬性、名稱與 identity 規則、`ON CONFLICT`）與 `auth.ValidIdentity`；另有 12 個 SQL mutant 針對 migration 0004。

- 第一次全量執行（`mutation-first-run.log`，57cb767）：66 killed、3 equivalent、3 BUILD-FAILED（harness 錯誤，不算 kill）。原因：我用來防磁碟滿的 build cache 清理器刪了使用中的檔案（`owners-no-recheck`），以及兩個 mutant 的寫法讓 import 沒被使用（`prov-identity-unchecked`、`prov-name-controls-only`）。清理器改為只在磁碟剩不到 8 GB 時才動作，兩個 mutant 改寫後以 `--only` 重跑皆 killed（`mutation-rerun-build-failed.log`）。
- Round 1 修正後的全量執行（`mutation-final-e34aac6.log`）：72 個，69 killed、3 equivalent、0 unexpected，exit 0。
- D-042 之後（e2faab3）加入 provisioning role 的 mutant（不檢查 SET、接受 twin_owner／twin_api 成員、改回 SET ROLE twin_owner 等），共 76 個。`prov-set-not-required`（把 `SET` 改為 `MEMBER`）第一次存活：沒有「是成員但不能 SET ROLE」的 login 測試；補上 `GRANT … WITH INHERIT TRUE, SET FALSE` 的案例後被擋下。最終全量執行（`mutation-final.log`）：76 個，73 killed、3 equivalent、0 unexpected，exit 0。
- SQL mutation（`sql-mutants.py`）：每個 mutant 削弱 migration 0004 的一條規則（少一個 grant、多一個 grant、schema CREATE、API 寫 worker／system event、provisioning 寫 worker／user event、worker 寫 user event、任意 actor、函式沒有固定 search_path），在新建的 DB 上套用後跑三個 SQL suite。結果（`sql-mutation.log`）：12 個全部 killed、0 unexpected，exit 0。

| Equivalent | 理由 |
|---|---|
| `owners-unordered` | 有了 advisory lock，一次只有一個 transaction 取 owner 鎖，而 `Run` 的 `FOR SHARE` 是它第一個取的鎖、等待時不持有任何鎖，所以取 owner row 的順序不可能形成 cycle。審查者的 plan-order probe（`TestOwnerLockAcrossPlans`）與壓力測試在這個 mutant 下通過，同時拿掉 advisory lock 時失敗。ORDER BY 依已批准的 D-036 保留。Round 1 前的理由（同一 query 同一 plan）是錯的（review L1） |
| `update-target-unlocked` | 成員變更一次一個，Get 與 SetRole 之間沒有其他變更能改到目標；SetRole 的 UPDATE 本來就會等成員的寫入，只有「相同 role」的 no-op 路徑不再等。保留以防日後不經 `RunOwners` 的寫入者（審查者已接受） |
| `prov-superuser-attribute` | Superuser 是 twin_api、twin_worker 的成員，runtime role 的檢查已先拒絕它 |

### 首版（d83a1af，review round 1 之前）

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

#### 首版的 mutation

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
| `TestCallerChangedWhileWaiting` | 尚未 commit 的升級不算：caller 立刻 403、不等鎖；等 membership lock 時被降級 403、被移除 404；目標不變、無 audit（取代 round 1 前的 `TestPromotedWhileWaiting`） | ADR-023、review M3 |
| `TestRefusedRequestsTakeNoLock` | 非成員與 viewer 在某 owner 寫入進行中仍立刻被拒，且另一位 owner 的寫入不等它們 | review M1 |
| `TestOwnerSetIsCurrent` | 升級因目標的寫入而等待時，接著的自我降級、降級新 owner、移除新 owner 都成功（不誤判最後一位），最後剩一位 owner | review M3 |
| `TestMembershipChangesUnderLoad` | 3 位 owner、6 個 user、10 個 worker、5 秒隨機升降級、移除、重新加入、寫入、列表：每輪 370–420 次成功變更（少於 50 即失敗），沒有 40P01 或其他非預期錯誤，從未出現沒有 owner 的 organization | review M2 |
| `TestOwnerLockAcrossPlans` | 審查者的 probe：兩個變更以不同 plan（seq scan 的 heap 順序≠user_id 順序）取 owner 鎖、兩位 owner 都在寫入中：3 輪皆無 deadlock 且都成功 | review L1、M2 |
| `TestOwnerRowsLockedDuringAChange` | D-036：變更進行中另一位 owner 的寫入要等；非 owner 也非目標的成員寫入不等 | D-036、ADR-023 |
| `TestReplayNeedsTheRole` | 被降級的 owner 重播同一 Idempotency-Key 得 403，被移除得 404 | D-011、review L2 |
| `TestScopeIsValidatedBeforeTheDatabase`（internal/tenant） | 無效的 organization／user ID 在碰資料庫前即被拒（`Run` 與 `RunOwners`） | D-008 |
| `TestOrganizationFilter`、`TestListAfter`（storage/memberships） | 以 superuser 執行時 organization filter 本身讓 a 讀不到、改不到、刪不到 b 的成員；keyset 從給定 user ID 之後開始 | AGENTS.md（application filter 與 RLS 並存） |
| `TestList` | 以 user_id 分頁不重不漏、符合 Memberships schema；cursor 不能用於其他 organization、竄改即拒；limit 0／101 被拒 | API_DATABASE §3 |
| `TestRoutesMatchContract` | Routes 的 method、path、role 等於 api-operations.json | D-012 |
| `TestMembershipRoutes`（internal/api） | 整個 HTTP stack：owner 200、editor 403、其他 organization 404、path 與 header 的 organization 不同 404、401；缺 Idempotency-Key 400；409／422 的 code；PATCH、DELETE 204（無 body、有 X-Request-ID）；被移除者下一個 request 404；3 筆 audit | TEST-009 |
| `TestCheckOperator`、`TestProvisionerCannotChangeAnything`、`TestAPICannotProvision`、`TestProvision`、`TestUserOnceUnderConcurrency`（storage/provisioning） | Operator login（`NOINHERIT` 或 `INHERIT … IN ROLE twin_provisioner`）可以；superuser、API login、worker login、沒有任何 role 的 login、schema owner 的成員、同時是 twin_owner／twin_worker／twin_api 的 provisioner、是成員但不能 SET ROLE 的 login、有 BYPASSRLS／CREATEROLE／CREATEDB／REPLICATION 的 provisioner 被拒；以 twin_provisioner 不能改或讀 memberships、organizations、audit_events，不能關 RLS，operator login 未 SET ROLE 時沒有任何權限；API login 即使設了 RLS 讀的 setting 也不能 INSERT users／organizations（42501）；user 依 issuer／subject 只建一次，8 個並行呼叫 10 輪都回同一 ID、恰一個回報建立；organization 有 owner 與 system audit，owner 立刻能解析並以 owner 身分工作；空白、前後空白、C1 控制、U+202E、U+200B、NBSP、private use、非 UTF-8 的名稱與 verifier 不會接受的 issuer／subject（http、相對、換行、U+0000、過長）被拒 | D-008、D-042、TEST-011、review L2–L4、I3 |
| `db/tests/roles.sql`（D-042 部分） | twin_provisioner 的權限矩陣恰為五項、不能在 twin 建立物件、不是其他 twin role 的成員、不擁有物件；以它建立 user／organization／owner／system audit，只讀得到自己設定的 user；跨 organization 的 membership 被 RLS 擋下；寫 user／worker event、改刪、TRUNCATE、DDL、關 RLS、刪 policy 皆 42501；API 不能寫 worker／system event，worker 可寫 system | D-042、TEST-011 |
| `TestValidIdentity`（internal/auth） | issuer 與 verifier 設定的規則一致（同一組值兩邊判斷相同），subject 與 token `sub` 的規則一致，含 255／256 bytes 邊界 | ADR-017、review L4 |
| `TestProvision`、`TestRefusals`（cmd/provision-tenant） | 輸出只有 ID 的 JSON；API login、superuser exit 4；用法與輸入錯誤 exit 2，且沒有輸出 | D-008 |

## 需 owner 知悉

- ADR-023 為 PROPOSED：provisioning 指令與 login 規則（superuser 也拒絕）、成員 API 的錯誤碼（最後一位 owner 與已是成員為 409 LIFECYCLE_CONFLICT、未知 user 為 422）、PATCH 相同 role 不寫入、列表依 user_id 排序。
- 部署時需要為 provisioning 建立一個 `NOINHERIT … IN ROLE twin_owner` 的 login（db/README.md）。
- **D-042（owner 已批准，H-009）已實作**：provisioning 改以 `twin_provisioner` 執行。部署時先跑 `db/bootstrap_roles.sql` 與 migration 0004，再把 provisioning login 改為 `IN ROLE twin_provisioner`；原本 `IN ROLE twin_owner` 的 login 會被拒絕（exit 4）。
- **實作 D-042 時發現並一併修正**：0002 的 audit guard 只限制 `user` event，API role 能寫出 `worker`／`system` 的 audit event；0004 改為每個 role 只能以自己的身分寫入。
- 語意改變（review M1）：成員變更以 request 開始時已 commit 的 role 判斷；尚未 commit 的升級不算，caller 收到 403、重試即可。
- 已知風險（review I3，ADR-023）：owner 可加入任何已存在的 user ID（不需該 user 同意）；新增的 422 與 409／201 會透露某個全域 user ID 是否存在（ULID 不可猜）。

## 與規劃的差異

- `scripts/provision-tenant/` 改為 `cmd/provision-tenant`：architecture check 只允許 cmd 開啟資料庫連線；SQL 在 `internal/storage/provisioning`。
- Service 在 `internal/tenant/memberships`（tasks.json 的 planned output），SQL 依慣例在 `internal/storage/memberships`。

## NOT RUN／延後

| 項目 | 原因／歸屬 |
|---|---|
| 撤權時關閉 SSE stream（TEST-026） | SSE 尚不存在（T-023）；撤權後的下一個 request 已被拒（`TestDelete`、`TestMembershipRoutes`） |
| 前端成員管理畫面 | 不在 T-010 scope；UI 依 T-045 的 design gate |
| 真實 IdP 的 subject 與 provisioning | Auth0 tenant 尚未建立（ADR-018／T-043）；測試用 test OIDC provider |

## Review round 1（2026-10-18，同一 reviewer，審 e779f6b）

結論 CHANGES REQUESTED：所有 make target、38 個 test、mutation 46＋2 皆重現，但 probe 證實三個 medium 缺陷（皆未破壞「至少一位 owner」）。處理如下，修正在 57cb767（CI 的 URL 問題在 e34aac6），D-042 在 e2faab3，證據在 `checks/evidence/2026-10-18-T-010/review1/`。

| 發現 | 內容 | 處理 |
|---|---|---|
| M1 | 非成員與非 owner 在 membership 檢查前就鎖住該 organization 的 owner rows（跨 tenant 干擾，被拒前擋住 owner 的寫入 0.7 s） | `RunOwners` 先不上鎖讀 role，非成員／非 owner 在取任何鎖前被拒；`TestRefusedRequestsTakeNoLock`（紅：兩個情形都等滿 2 s；綠）。審查者的 HTTP 跨 tenant probe：被拒 22 ms，另一 owner 的寫入 18 ms（原 1.0 s／0.7 s） |
| M2 | 仍有 deadlock：re-lock 路徑，以及 READ COMMITTED 下分多個 statement 取鎖；只測了兩位 owner | 每個 organization 的 transaction 層級 advisory lock，成員變更一次一個；移除 re-lock。`TestMembershipChangesUnderLoad`（紅：13 個 40P01；綠：三輪 370–417 次成功變更、0 非預期錯誤）、`TestOwnerLockAcrossPlans`；拿掉 advisory lock 與 ORDER BY 時兩者都出現 40P01 |
| M3 | owner 集合取自 FOR UPDATE statement 的 snapshot，等待中 commit 的新 owner 不在其中，誤回「最後一位 owner」409 | advisory lock 之後才開始的 FOR UPDATE statement 看得到前一個變更；`TestOwnerSetIsCurrent`（紅：三個變體都 2 位 owner 卻被拒；綠） |
| L1 | `owners-unordered` 的等價理由（同一 query 同一 plan）不成立 | 加入審查者的 plan-order probe（`TestOwnerLockAcrossPlans`）。在 advisory lock 下這個 mutant 確實不改變行為（理由改寫，見 mutation 節），ORDER BY 依已批准的 D-036 保留 |
| L2 | 測試缺口：重播前未檢查 role、CheckOperator 的 twin_worker 子句 | `TestReplayNeedsTheRole`；`TestCheckOperator` 加入 owner＋worker、owner＋API、worker login；mutant `prov-worker-allowed` |
| L3 | `provision-tenant user` 並行時 23505 | `INSERT … ON CONFLICT (oidc_issuer, oidc_subject) DO NOTHING` 後再讀；`TestUserOnceUnderConcurrency`（舊程式紅：23505） |
| L4 | 名稱接受 C1、U+202E、U+200B、只有空白；subject 接受換行（verifier 不接受，無法登入） | 名稱需全為可列印字元、無前後空白；issuer／subject 用 `auth.ValidIdentity`（與 verifier 同一組規則，`TestValidIdentity`）；舊程式在新的 `TestProvision` 下 14 個輸入被接受（`red-provisioning.log`） |
| L5 | provisioning login 以 schema owner 執行，權限過大 | Owner 批准 D-042（H-009）並已實作：`twin_provisioner` 與 migration 0004（見上）；CheckOperator 另拒多餘屬性（I3） |
| L6 | 撤權時關閉 stream 只寫在 T-010 | 加入 T-023 scope |
| I1 | ADR-023「寫入不等變更」錯誤、未寫明被拒的嘗試也會擋寫入、兩處引用章節錯誤；ADR-019「確認無 deadlock」 | ADR-023 改寫等待關係並更正引用（API_DATABASE §8、§2）；ADR-019 改為說明 round 1 的發現與現行做法，並註明是測試結果不是證明 |
| I2 | 報告說 mutation exit 0，但留存的 log 都以 exit=1 結尾 | 最終全量 mutation 在乾淨 worktree 執行，log 以 exit=0 結尾：e34aac6 的 `mutation-final-e34aac6.log` 與 e2faab3 的 `mutation-final.log` |
| I3 | 未經同意加入、user ID 是否存在的揭露、先驗格式再驗權限；CheckOperator 接受有 BYPASSRLS／CREATEROLE 的 owner | 風險記入 ADR-023；CheckOperator 拒絕 SUPERUSER、BYPASSRLS、CREATEROLE、CREATEDB、REPLICATION |

審查者的 9 個 membership probe 與 2 個 HTTP probe 在修正後的程式上全部通過（`reviewer-probes-on-fix.log`、`reviewer-http-probes-on-fix.log`；`TestReviewRelockDeadlock` 現在因 caller 尚未 commit 的升級不算而得 403，0 deadlock）。

