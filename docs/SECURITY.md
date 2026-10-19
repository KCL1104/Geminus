# Security Requirements / Threat Model

SOURCE：原報告 pp. 36–43、46；補充 attack cases 與 runtime policies 為 APPROVED D-003、D-006–D-016。此為工程安全規格，不是 penetration test 或 compliance certification。

## 1. 信任邊界

Browser、CSV/JSON uploads、user model labels、LLM output、client IDs、queue payload 都不可信。Go API 是 authentication/authorization/approval authority；DB/RLS 是 defense in depth；worker 可執行 scoped job，但 provider adapter 不獲 DB/write capability。object storage 只接受 server-generated tenant keys；provider 僅收 sanitized context。

攻擊面：IDOR/BOLA、cross-tenant FK、pooled session leakage、role spoofing、stale approval/TOCTOU、idempotency replay、malicious pointer/patch、prompt injection、CSV formula injection、upload/resource exhaustion、worker lease race、signed URL leakage、dependency/supply-chain risk。

## 2. RBAC

| Action | Viewer | Editor | Owner |
|---|---|---|---|
| 讀取 project/model/version/proposal/run/results/analysis | 是 | 是 | 是 |
| 編輯 draft、publish、import、create AI proposal | 否 | 是 | 是 |
| approve/reject、manual scenario、simulate/cancel、create analysis | 否 | 是 | 是 |
| 管理 membership | 否 | 否 | 是 |
| 讀 tenant audit feed | 否 | 否 | 是（D-012 候選最小權限） |

讀自己的 membership（`GET /v1/me`，D-045）不在表中：任何已 provisioning 的使用者都可以（不需要任何 organization 的 role，也不選 organization），未知 identity 403；它回的 role 只決定 UI，不是任何 operation 的授權。

role 從 DB membership 取得，不從 request body、header、provider 或前端狀態取得。所有 object read 也要驗 org/parent。跨 tenant ID 回404；本 tenant role 不足回403。Owner 不是跨 tenant 超級管理員。last-owner 更新依 D-036 以 user_id 順序鎖住該 organization 全部 owner membership（`FOR UPDATE`）後再檢查人數，以 transaction 保留至少一位 Owner；不鎖 organization row，API 也沒有它的 UPDATE grant（CB-13）。

## 3. Authentication / browser

OIDC provider：Auth0（[ADR-018](adr/ADR-018-browser-session-and-oidc-provider.md)，H-004；tenant 由 owner 建立，region 依 retention／residency）。驗 JWT signature algorithm allowlist、issuer、audience、exp/nbf、key rotation；不得接受 alg=none 或以 token header 自選任意 JWKS URL。provider endpoints 僅 deployment allowlist。Browser 採 BFF（ADR-018）：Next.js 伺服器端是 confidential OIDC client（Authorization Code＋PKCE），token 只在伺服器端與 `__Host-` HttpOnly／Secure／SameSite=Lax 的加密 session cookie 中，瀏覽器 JS 拿不到任何 token；BFF 對非 GET 請求檢查 Origin 與自訂 header（CSRF），不開 CORS，再以 Bearer 轉給 Go API。Go API 不讀 cookie，所以沒有 cookie write path 繞過 CSRF 防護（TEST-032）。Session 是無狀態的（狀態全在加密 cookie 中）：登出撤銷 refresh token 並清除這個瀏覽器的 cookie，但無法讓已被複製的 cookie 失效；被竊的 cookie 在其 access token 到期前（至多 10 分鐘）仍可使用，之後 refresh 因 token 已撤銷而失敗。登出時 BFF 也丟棄該 session 在 process 內共用的 refresh 結果，所以較舊的 cookie 拿不到登出 session 的新 token。Refresh 失敗是否結束 session 只看 token endpoint 本身的回答（BFF 的 fetch 把它記下來；不從 openid-client 的錯誤型別推論，因為 refresh 回答之後還會抓 JWKS）。Session 在這些情況結束（401 並清除 cookie）：token endpoint 回 2xx 但 BFF 無法使用（例如 ID token 簽章不對、驗簽所需的 JWKS 抓不到、token type 不明）或無法放進 cookie（provider 此時已可能 rotation，回答中新的 refresh token，以及不 rotation 時 session 原本會保留的 token，都盡力撤銷）；token endpoint 回 4xx 並帶下列暫時性錯誤碼以外的 OAuth 錯誤碼（`invalid_grant`、`access_denied`、`invalid_scope` 與未知的錯誤碼等）；沒有 refresh token；以及閒置 12 小時或滿 7 天。Token endpoint 沒有回答（連不上、逾時）、回 5xx、429、錯誤碼 `temporarily_unavailable`、`server_error`、`too_many_requests` 或 `invalid_client`（部署設定錯誤，例如 client secret 輪替順序錯誤），或回非 2xx 卻沒有 OAuth 錯誤（例如前方 edge 的錯誤頁）時，provider 沒有接受這個 refresh token，BFF 回 503（`Retry-After: 5`）並保留 session；同一個 process 在 5 秒內以同一個 refresh token 來的 request 共用這次失敗，不再呼叫 token endpoint；log 記錄 token endpoint 的 OAuth 錯誤碼與 HTTP status（不含 description）。撤銷不保留的 token 是盡力而為：失敗只記 log（`session.discard_failed`，token 之後自行到期），回應最多等 2 秒。Refresh 之後 Go API 失敗時，回應仍帶新的 session cookie，否則下一個 request 會再用已被 rotation 的 token；API 送出 header 後、送出任何 body 前中斷也一樣：帶 Set-Cookie 的回應先送一個空的 chunk，讓 header 先到瀏覽器。登出時仍在進行中的 refresh 可能讓那個 request 拿到仍有效的 cookie：其 refresh token family 已撤銷，access token 在到期前（至多 10 分鐘）仍可使用。

T-043 實作（`apps/web/lib/session`，[runbook](runbooks/auth0.md)）：cookie 以 AES-256-GCM 封裝（目前與前一把 key，AAD 為 cookie 名稱與格式版本，超過 3.5 KB 即錯誤），session 閒置 12 小時或滿 7 天結束；openid-client 6.8.8 以部署設定的 endpoints 執行 Authorization Code＋PKCE（S256）＋state＋nonce，ID token 除 iss／aud／nonce／exp／sub 外另驗簽章（openid-client 預設信任 TLS 而不驗，測試證明另一把 key 簽的 ID token 會被接受，因此開啟 `enableNonRepudiationChecks`），驗完即丟；CSRF 為 Origin 完全相同、送了 `Sec-Fetch-Site` 時須為 same-origin、且有 `X-Geminus-Request: 1`；proxy 只轉送列出的 header（不送 cookie 給 API，不把 API 的 Set-Cookie、Location 回給瀏覽器），path 只接受一般的 segment；剩不到 60 秒時在伺服器端 refresh，同一個 refresh token 只 refresh 一次，並發與 30 秒內的晚到 request 共用結果（rotation 會把 reuse 視為竊用）；logout 撤銷 refresh token、清 cookie、導向 provider 的 logout。

T-008 實作（[ADR-017](adr/ADR-017-oidc-token-verification.md)，2026-10-18 ACCEPTED）：provider-agnostic 的 `internal/auth.OIDCVerifier`，issuer／audience／JWKS URL 只來自 deployment 設定；alg allowlist（預設 RS256、ES256，拒 none／HMAC）、`kid` 必填、拒 `jku`／`x5u`／`jwk`／`x5c`／`x5t`／`crit`／`b64`／`zip`／`enc` header、strict base64url；`iss` 完全相同、`aud` 包含（audience 不可等於任何 client_id）、可要求 `typ: at+jwt`、`exp` 必填、`nbf`／`iat` 檢查、30 秒 skew、`sub` 只含可列印字元；JWKS 不跟隨 redirect、單次抓取有 timeout 與大小上限，stale-while-revalidate（快取的 key 不等 refresh；未知 `kid` 只等到 request context 結束；single-flight、至少 1 分鐘間隔，無可用 key 時 5 秒）；錯誤保留原因但不含 URL；只讀 `Authorization: Bearer`，URL query 中任何拼法的 token 參數都拒絕，不讀 cookie。使用者由 `tenant.ResolveUser` 經 users RLS 在唯讀 transaction 中解析，未知 identity 不自動建立（D-008）。測試 token 只由 test-only 的 `internal/auth/authtest` 簽發，production 無法連結（architecture check）。

CORS/Origin allowlist、HTTPS、CSP、escaped labels/markdown、safe download filename；禁止把 bearer token 放 query string、logs 或 SSE URL。SSE / signed downloads 初次與續期都重新授權；stream 長連線也需處理 token expiry / membership revocation。

T-023 實作（TEST-026，`internal/runs`）：progress SSE 只讀 `Authorization: Bearer`；開始前與之後**每個事件之前**都在新的 transaction 中重新查 membership 與 role 再讀 run（REPEATABLE READ，所以事件不會反映撤權之後才 commit 的變化），成員被移除或 role 不再能讀 run 時 stream 立即結束、不再送任何事件；token 的 `exp` 到時、連線滿 10 分鐘、run 終態、client 離開或寫入 10 秒未完成也結束，重連時由一般 request 重新授權。降級（例如 editor→viewer）仍可讀 run，所以 stream 繼續；之後的寫入（取消）照常每次查 role。Trace 下載的檔名只由 run ID 組成（`<run ID>-trace.json`，attachment），不含使用者輸入。

## 4. Tenant isolation / DB

所有 tenant data tables NOT NULL organization_id + composite org/parent FKs + application filter + FORCE RLS。runtime role 不得 SUPERUSER、BYPASSRLS、REPLICATION、擁有任何物件、屬於目標 runtime role 以外的任何 role（含只能 SET ROLE 者與 predefined roles），或能建立物件；`tenant.CheckRuntimeRole` 在啟動時檢查（API_DATABASE §9）。per-request transaction-local context 由 verified membership 選定；pool checkout/commit/rollback/reset 後無 scope 殘留。

D-045（`GET /v1/me`，migration 0008）是 tenant isolation 唯一的放寬，範圍刻意收窄：
- **只在沒有選 organization 時成立。** `own_memberships`、`own_organizations` 都以 `app.organization_id` 未設為條件。選了 organization 的 transaction（`tenant.DB.Run`、`RunOwners`、worker scope）看到的與 0002 的 `tenant_isolation` 完全相同，所以既有的每條 tenant 隔離論證不變，一個 request 也不能在選了 organization 後順便看到自己其他 organization 的資料。反過來，沒有選 organization 時，`tenant_isolation` 一律不成立，只剩這兩個 policy。
- **只看得到自己的。** 只有 `user_id` 等於 `app.user_id` 的 membership，以及這些 membership 所屬 organization 的 row（API 只讀 `id`、`name`；這一列也是成員選了該 organization 時本來就看得到的）；同一 organization 的其他成員、其他 organization、其他任何表的 row 都看不到（`db/tests/self.sql` 逐表檢查）。API 中 `app.user_id` 只經 `internal/tenant/internal/scope`（`Run`、`RunOwners`、`RunSelf` 共用）設定；`RunSelf` 把它設為 `ResolveUser` 從驗證後 token 解析出的 user。
- **只讀、只給 API。** `FOR SELECT TO twin_api`：不能經由它們新增、修改、刪除或鎖 row（UPDATE、DELETE、`FOR UPDATE`／`FOR SHARE` 還需要 UPDATE／DELETE policy，只有 `tenant_isolation` 是），`RunSelf` 本身也是唯讀 transaction；worker 與 provisioning 沒有這兩張表的 SELECT，policy 也不給它們。membership row 仍只由 `RunOwners` 與 `Run` 的 `FOR SHARE` 上鎖（ADR-023 的不變式）。
- **policy 之間沒有循環。** `own_organizations` 的 subquery 以呼叫者的 role 讀 memberships，受 memberships 的 RLS 限制（測試拿掉 `own_memberships` 時就看不到任何 organization）；memberships 的 policy 不讀 organizations。
- 這仍是 defence in depth：`OfUser` 另以 `user_id` 過濾，HTTP 層要求不帶 `X-Organization-ID`。

RLS 不是對 arbitrary SQL 注入的完整隔離：若攻擊者能任意執行 set_config 或直接使用 shared DB credentials，仍能挑 scope。必須參數化 queries、限定 handlers、不曝 SQL console、不讓 LLM 持 DB handle、保護 runtime secret。RLS 的保證是阻止未加 filter 的意外 scope 外 query，不可誇大為抗 compromised application server 的絕對 tenant sandbox。

worker 只領 assigned tenant jobs，不能依 payload 的 organization_id 蓋掉 claim scope。job 中的 run/model/import IDs 都要再次 scoped read；跨 tenant的 artifact key/hash mismatch fail closed。API 與 worker 使用不同 DB grants；worker 沒有 canonical version/scenario INSERT 權限。

## 5. Mutation / approval / patch safety

所有 user writes 都是 authorized server routes；AI toolset 無 write/run/approve endpoint。原始 provider envelope 不能覆蓋 server IDs、mode、validation、status 或 actor。patch 只允許 bounded operations / decoded tokens，禁止 root、identity、escape confusion、move/copy 和任意執行欄位；整份 candidate IR 全面校驗。test op 有助定位，但不能取代 immutable base check。

approval 重新查 role，重新驗 latest/base/risk/dataset/patch，同一 transaction 只執行一次 effect。stale base409，reject無 mutation。Scenario approve 的 source-of-truth version count=0新增；dirty draft 保留；idempotency replay 同樣需 auth，不重用撤權前成功資料當授權。

## 6. Import / file / object storage

streaming upload 限 bytes/rows、parser depth、columns、field length、memory/time；不因 Content-Length 缺失而取消上限。P0 拒絕 archives/executable formats；JSON duplicate keys/nonfinite numbers 失敗；CSV cell 從不執行。Naive sample validation 不足，commit 前須全量驗證。

error report 的 spreadsheet 可執行字首（例如 =、+、-、@）需安全輸出，避免下載開啟時執行公式；這是 derived error report，不改原始 immutable data。顯示所有 model labels / CSV cell / LLM text 都做 escaping，render markdown 不准任意 HTML。

object keys 固定 tenant/{organization_id}/imports、simulation-results、assets 前綴（DB 以 `starts_with` 檢查，不用 LIKE：organization ID 的 `_` 是 LIKE 萬用字元，D-035；前綴之後只允許 server 產生的 ASCII 段，拒 `..`、`.` 段、空段、`%`、反斜線、控制與 Unicode 相似字元）；client 用 resource ID 要求下載，不能 supply arbitrary bucket/key/URL。server 授權後產短期 signed URL；URL 不進 audit metadata、telemetry 或 provider context。expiry / encryption key management / retention / backup policy beta 前決定，無 policy 不開放真實客戶資料。

D-044（APPROVED，H-010）取代上段 P0 的 signed URL：下載先由 API 授權後串流，不發 signed URL。T-023 的 simulation trace 依此實作：API 在 transaction 中確認 membership（viewer 以上）、run 為 completed、result 帶 trace、object key 在本 organization 的 `simulation-results` 前綴之下，transaction 結束後才讀 object，大小與 SHA-256 都與 result row 相同才送出；不符或不見時 500、不送任何 bytes。Object key 由 server 產生（`objects.NewKey`，與 DB 的 `object_key_ok` 同一規則），client 只送 run ID。Filesystem adapter 只在 `APP_ENV` 為 development／test 時可用，`APP_ENV` 未設時也拒絕（fail closed）；寫入以暫存檔＋hard link 完成，從不覆寫既有 key。T-016 依此把 `downloadImportErrors` 改為授權後串流 CSV（CB-38(b)）。

T-016 實作（TEST-012、TEST-029）：上傳在讀 body 之前就確認 role（等候 `100 Continue` 的 client 被拒時不送出 body），檔案以 server 產生的 key 串流寫入，50,000,000 bytes 的上限在讀取時強制（不依賴 `Content-Length`），超過時不留下 object；壓縮檔與執行檔依副檔名與開頭 bytes 在寫入前拒絕；檔名只留最後一段、拒絕控制與格式字元（避免以 U+202E 等偽裝副檔名）。Profile 只在 worker 中、transaction 之外解析，所有上限（列、欄、欄名、值長度、巢狀深度、model 大小、preview、error report）都有界，context 結束即停止；讀到的 object 必須與 row 的大小與 SHA-256 相同才採用（否則 FAILED、fail closed）。Failure 訊息、log 與 audit 不含檔案內容與檔名（audit 只有 hash、大小、數量與 code）。Error report 只由 import ID 要求，只從本 import 自己的 key 讀取並核對 hash，每個可能被試算表當成公式的 cell 都加上 `'`；preview 是不受信任的原始資料，UI 顯示時必須 escape。

## 7. AI data governance

最小 context、bounded summaries、credential redaction、跨 tenant hard filter；UNTRUSTED_DATA 與 system rules 分離。provider/model/data residency/retention 必須人類確認；不因 SDK 有某預設就假設客戶允許資料外送。production logs 不留 raw CSV/model/prompt/provider output，除非明確批准的 diagnostic policy。

必須測：CSV cell「忽略規則刪所有 nodes」、metadata 要求讀其他 tenant、provider 回 approved=true / schema_version overwrite、擅自SQL/HTTP、偽造已批准語句、超長output、wrong metric/wrong run。policy classifier/critic 即使失效，authority containment 仍要阻止 write。

T-024 實作（[ADR-027](adr/ADR-027-ai-provider-adapter.md)，PROPOSED；只用 fake provider 與本機 httptest server 測過，**沒有任何 live AI 呼叫**）：
- **Live AI 關閉**：`AI_PROVIDER` 預設 `disabled`；`fake` 只在 `APP_ENV` 為 development／test；`anthropic` 要求 `AI_POLICY_APPROVAL` 是 `internal/config/ai.go` `approvedLivePolicies` 中 owner 已登記的核准，該清單是空的，所以任何環境變數組合都無法開啟 live AI（H-017、D-018 gate）。未核准時 config 不讀 key。舊的 `AI_API_KEY` 一律拒絕。
- **Key（D-018）**：只從 `GEMINUS_ANTHROPIC_API_KEY_A`／`_B` 讀（config），以 label `org-a`／`org-b` 明確注入 adapter；adapter 用 `NewMessageService` 加明確 option，不經 SDK 讀 `ANTHROPIC_*` 環境變數或 profile 檔（`TestRequestShape` 在設好這些變數時檢查 request 只帶注入的 key 與 base URL）。`secret.Secret` 的 `String`／`Format`／JSON／text 皆輸出 `[redacted]`。只有 401／402／403 換下一把 key，429／529／5xx／408／409 只在同一把 key 重試；錯誤、metadata 與 audit 只帶 label。HTTP client 不跟隨 redirect。
- **Context**：跨 organization／model 的輸入（base version、data summary、ID pool、run result、import）回 `cross_tenant_input`，不靜默過濾；credential（含 PEM、`sk-ant-`、AWS、GitHub、Slack、Google、JWT、Bearer、`key=value`）、URL、email、IPv4、D-001 server ID 與不可見／控制字元先遮蔽並計數；使用者、import 與 DomainPack 以外的文字只在 `<untrusted_data>` block，block 內容 HTML／JS-escape，因此無法提早結束 block 或偽造 server block；prompt 以 JCS hash 封存，client 呼叫前重驗。
- **Output**：回答先依 role 的 output schema 驗證（trusted fields、`approved`／`ready`、tool、move op、root patch、低 confidence 未確認、executable transform、analysis 中的 run ID 都被拒），再 strict decode（重複 member、尾隨文字、invalid UTF-8 也拒）；issue 只留 JSON Pointer 與固定訊息，不回顯回答內容。`tool_use`／`pause_turn`／未知 stop reason、tool block 一律拒絕；`max_tokens` → `output_truncated`，`refusal` → `refused`。
- **未涵蓋（NOT RUN／後續任務）**：真實 API 呼叫與 projection 被 API 接受；production log／audit 的實際寫入（T-025）；偽造已批准語句與 wrong metric／wrong run 的內容檢查（T-025、T-030；T-024 只保證不可用的 metric 不在可引用清單、prose 數字可用 `locale.CheckProse` 檢查）。

## 8. Resource / job safety

入列前 admission quota，execution timeout、event/entity/visit/trace limits、bounded replication pool、provider tokens/concurrency/retry caps。warmup 和 retries 都計費/計 budget；trace sampling 不改結果也不能無限長。worker lease_token fencing 防 stale finalize；cancel race 在鎖 run+job 後重新判斷。object upload 完而 DB commit 失敗時結果不可見，孤兒 artifact 由受控 maintenance 清理。T-023：cancel 與 finalize 都先鎖 job 再鎖 run，finalize 看到取消請求即改為 cancelled、不存結果（`TestCancelRacesCompletion`）；execution limits 由 server 在入列時凍結，client 無法設定或提高；event budget 跨 retry 以 durable reservation 計算，wall clock 從第一次開始起算（API_DATABASE §7）；孤兒 trace object 的 maintenance 尚未實作。

## 9. Audit / observability / release

append-only audit 記 actor_type/id、action、target、request_id、base/new version或scenario、patch/context hashes；provider/model/prompt/schema/tokens/latency metadata 可記，private chain-of-thought 不記。actor_id 不從任意 client/body 信任。避免 secrets、signed URLs、raw customer data；業務分析 telemetry 也同樣限制。

release 需要 dependency vulnerability / secret / container scan、SBOM/鎖版本策略、runtime permission tests、backup restore rehearsal、人工 production approval。掃描工具與 provider 選型 TBD，不編造通過結果。跨 tenant regression 100%、未批准或invalid mutation committed=0、context leakage=0 是 blocking gates；非零即停止 release，不可降門檻。
