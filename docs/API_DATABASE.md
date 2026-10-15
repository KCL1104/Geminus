# API / Database Contract

SOURCE：原報告 pp. 29–37、40–43。OpenAPI / SQL 完整化是 APPROVED D-001–D-004、D-006、D-008–D-015。**Migration 是需審查的草案，不是已在 PostgreSQL 執行過的結果。**

## 1. Contract files 與命名

`openapi/openapi.yaml`：OpenAPI 3.1.1，base `/v1`，使用 local external `$ref` 指向 `schemas/`，不重複定義 Model IR。`planning/api-operations.json` 列所有 operations 與 SOURCE/PROPOSED origin。新增的 listing/scenario/membership/download/reset/analysis-read endpoints 都有 x-origin=PROPOSED，標示它們不在原報告的 endpoint 清單內（來源標記，不是批准狀態；D-012 已批准）。

schemas/model-ir、proposal-output、proposal、scenario、simulation、analysis、canonical-event、ai-mapping、import、dataset-snapshot、http、common、json-patch 各有明确責任。stored `ai_proposals.patch` 對應 HTTP `changes`；model_versions.hash 對應 API `hash` / run.model_hash，序列化層顯式映射。public IDs 用 text；原 ER 圖 UUID 與 API 前綴的衝突見 D-001。base_version_id 統一命名，不另外接受含糊別名。

## 2. Authentication / tenant context

所有 `/v1` request：Authorization: Bearer OIDC JWT + X-Organization-ID。驗簽、issuer、audience、expiry 等完成後，才使用 issuer/subject 載入既有 user identity、membership。header 只選 tenant，不是授權。路徑中的 organization_id 若存在，必須等於已驗證 header scope。

APPROVED D-008：org/initial owner/user provisioning 是受控 bootstrap，不暴露 unauthenticated public provisioning endpoint；provider 與 onboarding automation 仍需 T-008/T-010 完成。Owner 可管理已知既有 user ID 的 membership；不提供全站 user enumeration。改／刪最後一位 Owner 時，依 D-036 在同一 transaction 先以 user_id 順序 `SELECT … FROM twin.memberships WHERE organization_id=$1 AND role='owner' ORDER BY user_id FOR UPDATE` 鎖住全部 owner membership，再檢查人數後拒絕；API 沒有 organizations 的 UPDATE grant，所以不鎖 organization row（CB-13）。

## 3. HTTP 規則與錯誤

response 使用 application/json，error 保留原報告格式：error.code/message/details[{path,reason}]/request_id；details 另可帶選填 code/severity（D-024）。`error.code` 是封閉清單 `common.schema.json#/$defs/ErrorCode`，`x-http-status` 指定每個 code 唯一對應的 status（D-025）。伺服器產生的資源 ID 為 `<prefix>_<ULID>`（D-001，見 TWIN_IR_SPEC §1）。所有錯誤避免 stack trace、SQL、其他 tenant 名稱、provider credentials；JSON Pointer 顯示 exact field。正常 request 帶 X-Request-ID correlation。

| HTTP | 語義 |
|---|---|
| 201 | 已 committed 的 create / publish / approval effect |
| 202 | 已 durably enqueue；不代表計算完成 |
| 400 | malformed JSON、duplicate keys、錯誤參數 |
| 401 / 403 | authentication 失敗／已認證但 role 不足 |
| 404 | 不存在或不在 authorized tenant/parent；不揭露跨 tenant 存在性 |
| 409 | MODEL_VERSION_CONFLICT、IDEMPOTENCY_KEY_CONFLICT、LIFECYCLE_CONFLICT、RESULT_NOT_READY、PROPOSAL_ALREADY_DECIDED（D-030） |
| 413 / 422 | 大小超限／schema、semantic、dataset 或 constraint 無效 |
| 415 | Content-Type 不符（UNSUPPORTED_MEDIA_TYPE） |
| 428 | 缺 If-Match（PRECONDITION_REQUIRED）；缺 Idempotency-Key 為 400 IDEMPOTENCY_KEY_REQUIRED |
| 429 / 503 | quota exceeded／provider or infrastructure unavailable |

Draft ETag 固定候選格式 `"draft-revision-N"`。PATCH draft 使用 application/json-patch+json + If-Match；publish/reset 也帶 If-Match。原報告明確 stale 回 409，本草案保留，沒有換成一般 HTTP 常見的其他 status。

Lists 使用 items/next_cursor、limit 1–100，cursor 綁 tenant、parent、sort/filter；不能讓其他 tenant 的 cursor 取得 scope 外資料。服務端選穩定 created_at/id 排序，不在刪改中用不穩定 offset 做授權假設。

## 4. Model / proposal / scenario transactions

create：template valid →insert model、v1、draft revision1→audit→commit。publish：expected_base + If-Match + model lock→candidate validate→insert version(parent=base,version=latest+1)→update latest/draft→audit→idempotency response→commit。

proposal request 回 202 generating；worker 生成內容後只 UPDATE proposal，不能 INSERT model_versions/scenarios。ready->approve 是 API 中的人類 action。先驗 role/key，再按 model→draft→proposal lock order；重新驗 latest/base、patch、dataset manifest、risk；一次 transaction 建 version **或** scenario。mode=scenario 不建立 version。UI / generated client 必須使用 discriminated approval response，不能一律讀 new_version_id。

同一 proposal 已批准且 expected base 符合原 proposal 時，可回同一 effect；不可因換 Idempotency-Key 建第二份。兩個不同 proposals 競爭同一 base 時，一個成功後另一個回 409。dirty draft 保留；顯式 reset 須 confirm_discard=true +最新 ETag；沒有 auto-rebase endpoint。

## 5. Import API 與資料保留

POST imports 是 bounded multipart（kind、project_id、model_id、base_version_id、file），接受 CSV events、JSON events、JSON model。profile/preview 不修改 Twin。CSV UTF-8（可處理 BOM）；其他格式回具體錯誤，不自動猜編碼。JSON event import 為 canonical-event array；JSON model 必須完整 IR，server-bind model identity 後產生待審 diff，不能匯入 server/tenant envelope。

mapper suggestions 是 async read-only suggestion；不覆寫已確認 mapping。POST mapping 帶 expected_mapping_revision、confirmed=true，增加 revision 並排 validation，回 202；同 revision 的 worker 才能發布 validation。VALIDATED 意味所有 rows 合法；invalid rows 回 AWAITING_MAPPING + error report；fatal parse/storage 才 FAILED。JSON model 無欄位 mapping 時 revision=0 仍需 validation。

APPROVED D-009 commit：驗 state/revision/base/current permission/valid rows→freeze dataset version→create generating proposal + inference job→標 COMMITTED→audit→202。report 中「import into model」在此解讀為交付審核 pipeline，不是直接 mutation。source JSON model identity/name/graph 差異都要展示；拒絕 proposal 後 dataset 已 commit 但 Twin hash 不變。

dataset model inference 可產生每 stage 的 empirical numeric snapshots，保留 parent_dataset_version_id 作 lineage。ready proposal 保存 candidate_dataset_bindings；批准時不得重新指向「最新 dataset」。原始 upload、error artifact 與推導樣本都為 tenant-private；retention TBD 不等於永久保留已獲同意。

## 6. Runs / async states / SSE

POST simulations 只接受 immutable model_version_id +（inline scenario_patch 或 scenario_id 二選一）+ explicit config。baseline 用 []。saved scenario 必須與 model_version_id 匹配；inline patch 是人類明確的 what-if request，仍完整 validator，不能允許 provider tool 直接呼叫此 route。enqueue 時 freeze engine_version、hashes、datasets、execution_limits；202 只在 resource+job transaction 已 commit 後回。

run states：queued→running→completed/failed/cancelled；queued 可直接 cancelled；暫時失敗 retry 可 running→queued，保留原 inputs。completed/failed/cancelled terminal 不可再更新或轉回 running。progress revision 單調增加；replication progress 可在 retry 時重新開始，UI 不把舊 attempt 進度當 completed 結果。

cancel queued 原子取消 job/run；cancel running 設 cancellation_requested；worker checkpoint 終止並 finalizes cancelled。completed/failed 取消回409，重複 cancelled 可回同一物件。result endpoint 對非 completed 回 RESULT_NOT_READY；partial attempt output 不得拿來分析。

SSE `/events` 是 progress snapshots，不是原始 simulation trace。使用 authenticated fetch streaming，避免 browser EventSource 無法設定 Authorization 而把 token 放 URL。id=run_id:progress_revision；Last-Event-ID 綁同 run；reconnect 送 latest snapshot，未保證全歷史 replay。重連／定期 poll 重新確認 token/membership，撤權或 expiry 關閉 stream。sampled playback 走獨立 authorized trace artifact endpoint。

## 7. Idempotency / crash recovery / fencing

Scope = organization_id + actor_id + operation/resource + key。request_hash 包含 normalized parsed body、URL resource、concurrency headers；multipart 用 file content hash+metadata。不可僅 hash body 而忽略 If-Match/expected base。相同 key+相同 fingerprint 回原 response；不同 fingerprint 回409。權限檢查在 replay 前仍必須執行；舊成功 response 不是撤權後的存取通行證。

reserve key 與 operation effect、audit、response 同一短 transaction；在處理 duplicate reservation 時鎖同一 key。LLM/DES 在 transaction 外。TTL 初始24h 是 D-007 提案；versions/source_proposal unique 與 result.run_id unique 仍保證 TTL 過後不重複 canonical effect。expired key cleanup 是受控 maintenance，不給一般 runtime arbitrary DELETE。

claim 用 organization filter + SKIP LOCKED + priority DESC/created_at/id。attempt 增加、lease_token 增加、lease_expires_at 保存；初始30s lease /10s heartbeat /max_attempts3 是候選。heartbeat/finalize 都核對 organization、job_id、worker_id、lease_token、running、not expired；更新零 rows 表示失去 ownership，不可重试 unscoped write。

result finalize transaction 鎖 job，再鎖 run，核實有效 lease +無 cancel flag；insert unique result、run completed、job completed、audit 同時 commit。lease expired 的舊 worker 永遠不能覆寫新結果。reaper 逐 tenant 處理 expired lease，先核實 retry budget；run frozen input 不變。non-transient validation/budget failure 不 retry；有限 exponential backoff 的參數仍需配置。

APPROVED D-007：logical run 的 wall/event budgets 要包含 retries，而非每次重試重設。worker 使用 durable budget reservations、有限批次 credit 與外部 cancellation governor；engine 只讀授予的 budget/context，不直接讀 DB。實作時要測 crash 前已保留但未用完的 credit 可被保守消耗，不能超額。這一部分為 T-021/T-023 的必測項，不在本包聲稱已完成。

## 8. Relational model / constraints

| Tables | Responsibility / immutable boundary |
|---|---|
| users、organizations、memberships、projects | identity、scope、role；users 為 global auth identity 特例，其餘 tenant owned |
| models、model_drafts、model_versions | latest pointer、CAS draft、immutable full IR；parent 同 org/model |
| datasets、dataset_versions、model_version_datasets | immutable object hash/unit/lineage；version binding 不指 mutable alias |
| imports、ai_proposals、scenarios | lifecycle、immutable review payload、scenario patch；source proposal unique effect |
| simulation_runs、run_datasets、simulation_results | frozen inputs、snapshot manifest、unique immutable result；mutable progress 與 input 分離 |
| ai_analyses | async analysis output + evidence；沒有 model write authority |
| jobs、idempotency_keys、audit_events | scoped execution leases、dedupe、append-only audit |

所有 tenant-owned table 帶 NOT NULL organization_id。composite FKs 保證 child 不能指不同 tenant/project/model 的 parent；單一 UUID/text FK 並不足夠。模型 version、scenario、dataset revision、binding、completed result、audit 有 immutable triggers + runtime grants；run input 有 guard trigger。DB JSONB shape checks 不取代 JSON Schema / semantic validator。

whole-manifest 另外存在 model_versions / simulation_runs 的 frozen dataset_bindings JSON；deferred constraint triggers 要求 relational bindings 在 commit 時完全一致，避免 immutable child 仍可被 late INSERT 改變 manifest。API 回傳 frozen parent manifest，不從可附加的 child collection 推導 authority。event_budget_reserved 與 execution_deadline_at 支援跨 retry budget；deadline/first start 不可重設，reservation 不可倒退。

T-006 起 schema 另強制（`db/tests/constraints.sql` 驗證）：每種 ID 有自己的 domain（`twin.org_id`、`twin.mdl_id`…，`<prefix>_<ULID>`，D-001），欄位不能放別種 ID；object key 以 `twin.object_key_ok` 檢查（`starts_with` 前綴、拒 `..`、`//`、反斜線、控制字元、>1024 字元；D-035／CB-14）；`logical_ref` 為 `twin.logical_ref` domain（`^dataset:[a-z0-9_.-]+$`），manifest 比對用 `ORDER BY logical_ref COLLATE "C"`，與 Go 的 byte 排序一致（D-034／CB-15）；ai_proposals 有 `server_risk_level`、`provider_risk_claim`，`risk_level` 必須等於兩者最大值（D-027）；immutable 表另有 BEFORE TRUNCATE guard（TRUNCATE 不經 row trigger 與 RLS）。

SQL 不宣稱 DB 本身已強制 human approval、last Owner、full graph validity、JCS hash equality、same scenario patch snapshot 或 metric evidence；這些仍是 application transaction / worker tests 的責任。不要把 RLS 當完整 RBAC 或 SQL injection sandbox。

## 9. RLS / roles / install

先以受控 administrator 執行 db/bootstrap_roles.sql；migration login 可 SET ROLE twin_owner，API/worker logins只能繼承對應 runtime role。依序 0001_core.up.sql、0002_rls.up.sql。runtime roles 無 SUPERUSER / BYPASSRLS，不能 owner/inherit owner；表強制 RLS。未設 app.organization_id 時可見 rows=0，wrong-tenant INSERT/UPDATE 失敗。

每個 checked-out pool connection 都用 explicit transaction + SELECT set_config(...,true)，transaction 結束 scope 清空；避免 session-level SET。實作為 `internal/tenant`（T-007）：`Run` 先驗 membership 再執行、`Worker` 只服務 deployment 指派的 organization、`Guard`（pgxpool AfterRelease）丟棄任何歸還時仍帶 scope 或 role 的連線、`CheckRuntimeRole` 在啟動時拒絕 superuser／BYPASSRLS／CREATEROLE／CREATEDB／owner 成員／同時屬於兩個 runtime role 的 login。worker 對 imports／jobs 只有欄位級 UPDATE，且 trigger 禁止 worker commit import（D-037）。users 以 verified issuer/subject policy 做 self identity lookup，再設 app.user_id 查 membership。API 應在每個 transaction 檢查 membership，不能靠長期 cache 授權。

P0 worker 只 poll deployment assigned organization IDs，逐 scope claim；db/queries.sql 提供參數化候選。SQL template 不是可直接貼到 client 任意執行的 endpoint。

## 10. Migration review / rollback gate

在 disposable PostgreSQL 上執行 up、schema smoke、runtime-role RLS tests、cross-tenant FK tests、immutable tests、up/down/up。production migration 需獨立審查、備份／restore rehearsal、相容部署策略；down 會刪資料或移除 security，不能自動 rollback。users/org bootstrap 與 secrets 透過 deployment/provisioning 完成，不使用示例明文密碼。

本包工具環境沒有 PostgreSQL server/client；SQL 尚未執行，不能宣告 migration/RLS 已通過。詳見 checks/VALIDATION_REPORT.md。
