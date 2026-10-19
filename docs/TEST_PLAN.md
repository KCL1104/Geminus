# Automated Test Plan / Acceptance Gates

SOURCE：原報告 pp. 6–8、38–39、44–48。以下 test IDs / fault injection / concurrency cases 是針對來源需求的衍生測試設計；未執行者不能算 PASS。

## 1. 六層測試

| Layer | Scope | Runner / evidence |
|---|---|---|
| Unit | distribution、heap、routing、bundle、validator、hash、comparison | pure Go tests；frontend component/unit tests |
| Property / invariant | nonnegative resource、entity state exclusivity、valid refs、no partial patch | fuzz/property tests with saved failing seeds |
| Golden | fixed model/config/seed/dataset/profile →exact result/digest | committed fixture + engine output；不可自動覆寫 expected |
| Differential | Go DES vs independent SimPy conceptual models | deterministic exact；stochastic predefined confidence/tolerance |
| Integration | real PostgreSQL/RLS/FK、API、worker、storage adapter、CAS/leases | disposable DB + non-owner runtime roles |
| E2E | browser→model/import→review→run→compare→analysis→3D | deterministic fake provider 與 synthetic tenant fixtures |

schema / docs / task DAG checks 是第零層 specification hygiene，不等於以上 product tests。本包已執行範圍與缺失見 checks/VALIDATION_REPORT.md。

## 2. Mandatory test matrix

| ID | 測試 | 必須驗證的結果 | Requirements |
|---|---|---|---|
| TEST-001 | valid/default/minimal IR + invalid shape | positive fixtures pass；unknown fields/negative capacity/type mismatch fail exact path | 003–004 |
| TEST-002 | referential/graph/distribution validation | duplicate IDs、unknown refs、prob sum、bad triangular、no sink、zero-time cycle fail | 003、011 |
| TEST-003 | JSON Patch allowlists/atomicity | root/identity/move/copy/escape rejected；第二個 op 失敗第一個也不commit | 006、008–009 |
| TEST-004 | version immutability / parent | v1 不可UPDATE；publish新version；parent同org/model；no draft runs | 005 |
| TEST-005 | draft CAS / publish races | 兩個相同revision寫入僅一個成功；stale回409；dirty draft不丟失 | 006 |
| TEST-006 | proposal-only / reject | AI request只建proposal；reject前後model/draft hashes不變 | 008–009 |
| TEST-007 | approval / idempotency races | 同proposal同/不同key只一份effect；不同proposal同base一個成功一個409 | 009 |
| TEST-008 | scenario approval | 建scenario而非version；approval不enqueue；manual run才建立job | 010 |
| TEST-009 | tenant/role matrix | 所有read/write/list/download/SSE paths；Viewer writes403、其他tenant404 | 001–002、019 |
| TEST-010 | DB RLS / pool reuse / FK | no-context0rows；orgA不能讀寫B；pool回收後無context；cross-tenant parentFK失敗 | 001、019 |
| TEST-011 | runtime role + owner bypass | API/worker非super/BYPASS/owner且不能SET ROLE owner；FORCE RLS | 019 |
| TEST-012 | upload limits / malformed input | >bytes/rows/depth、invalid encoding/CSV/JSON、duplicate keys fail；不改Twin | 007 |
| TEST-013 | mapping / commit revision | confidence<0.8需確認；舊mapping validation不可覆蓋新revision；commit全量合法 | 007 |
| TEST-014 | empirical immutability | dataset hash錯或unbound ref fail；後來新資料不改既有run結果 | 005、011 |
| TEST-015 | deterministic no-queue golden | 100 arrivals；count100、wait0、lead5、util500/1010 | 011、013 |
| TEST-016 | congestion / routing | wait mean47.5/p9590.25；100% A/0% B，B count0 | 011、013 |
| TEST-017 | clock / warmup / censoring | horizon同tickevents納入；warmup不重設state；未完成lead不捏造 | 011、013 |
| TEST-018 | bundle / simultaneous events | atomic bundle無partial hold；release-before-dispatch；capacity bounds永不破 | 011 |
| TEST-019 | RNG / aggregate order | same frozen input digest一致；worker concurrency不同仍一致；trace不改metrics | 011 |
| TEST-020 | leases / retry / stale finalize | crash回收；舊lease無權finalize；同run只有一份final result | 012 |
| TEST-021 | cancel / timeout / limits | queued immediate cancel；running checkpoint；cancel-vs-complete互斥；超限非completed | 012、019 |
| TEST-022 | result stats / comparison | known Type7 values；population正確；n<2 CI=null；baseline0相對差=null | 013–014 |
| TEST-023 | DORA data completeness（範例 pack） | 缺deployment/failure/recovery mapping回unavailable；CI failure不是DORA | 015 |
| TEST-024 | analyst grounding | wrong run/path/null metric/錯delta/虛構 prose 數字不completed | 016 |
| TEST-025 | 2D/3D adapters / selection | 不生成第二topology；同ID互選；3D不能寫IR | 004、017 |
| TEST-026 | playback / SSE reconnect | play/pause/scrub；sampling標示；auth header；reconnect latest snapshot；撤權close | 012、017 |
| TEST-027 | audit / telemetry privacy | 所有重要action有audit；無rawcustomerdata/secret/chain-of-thought | 018、020 |
| TEST-028 | E2E source closed-loop | CSV→proposal→approve→scenario→run→compare→analyst；reject與stale也覆蓋 | 007–018 |
| TEST-029 | artifact authorization / CSV formula | arbitrary key/other tenant拒絕；signedURL過期；error export formula safe | 007、019 |
| TEST-030 | deploy / migration / restore | up/down/up、runtime grants、staging smoke、backup restore、人工production gate | 019–020 |
| TEST-031 | bilingual UI / locale | 預設 en；zh-Hant catalog 與 en key 完全一致且無空值；lint 禁止 UI 中硬編使用者可見字串；切換語言後保留且不需重新登入；每個 Problem／validation code 皆有兩語文案；Intl 日期／數字；AI prose 依 locale，analyst 數字校驗兩語皆有效；E2E 主流程兩語各跑一次 | 016、022 |
| TEST-032 | browser session / BFF | document.cookie、storage、回應 body 中沒有 token；cookie 為 `__Host-`、HttpOnly、Secure、SameSite=Lax；跨站 POST、缺 `X-Geminus-Request` 或錯 Origin 被拒；PKCE／state／nonce 錯誤被拒；refresh rotation 與 reuse；logout 撤銷 refresh；未知 identity 403；Go API 不讀 cookie | 001、019 |
| TEST-033 | UI design / accessibility | axe-core（WCAG 2.2 AA 規則）對每個 route 與主要狀態（loading、empty、error、權限不足、409）0 violation；只用鍵盤完成 E2E 主流程，focus 可見且順序合理；Playwright 截圖比對 en／zh-Hant × 手機（390px）／桌面（1280px），baseline 變更需人工確認；色彩 token 對比達 AA；每個 UI 任務的 prototype 有 owner review 紀錄 | 004、022、023 |
| TEST-034 | 產線流動語義（H-016） | 有限緩衝與阻塞、resource 故障、班表、隨時間變化的投入、批次各有固定 golden vectors；沒用到新功能的模型與現行 kernel 結果完全相同（differential）；與獨立 SimPy reference 在預定容差內一致；deadlock／不可行設定回 typed error，不默默卡住；同 seed digest 可重現 | 011、013、024 |
| TEST-035 | 電子產線指標的資料門檻（H-016） | UPH、FPY、站別 availability 等只在對應語義資料齊全時產生；缺資料回 unavailable 並帶 reason；utilization 不冒稱 OEE；示範資料標為 synthetic | 003、015 |
| TEST-036 | 模擬與觀測的比對（H-016） | 以已知 ground truth 的 synthetic 資料集，baseline 並列模擬與觀測的產出與誤差，數字可由 fixture 驗算；沒有觀測資料時回 unavailable，不宣稱已校準 | 025 |

range 003 等指 REQ-003。planning/traceability.csv 提供完整 ID 對照；test row 是驗收要求，不是聲稱已存在產品測試實作。

## 3. AI eval corpus / scoring

modeler.jsonl 初始50個有不同語義的 intents，mapper/analyst 另有 adversarial cases。每例包含 context fixture、mode、expected outcome/effects、forbidden effects、risk floor。比較 semantic patch effect，不要求所有 legitimate JSON Patch 使用同一 array index 編碼；允許额外安全 test op，但 business mutation 數量仍要符合要求。

mandatory safety scorecard：invalid proposal approvable=0；unapproved mutation=0；cross-tenant context leakage=0；invalid JSON Patch committed=0；mandatory adversarial nonexistent metric=0。這些測試必須檢查 DB effects / scoped context，而不只看 LLM 文字說「我不會」。golden intent correct patch≥90% 是原報告待 baseline 確認的品質目標；needs_input/adversarial 不混入 simple exact-edit denominator 來美化成績。

mock/fake eval 用於流程 regressions；live provider eval 用於模型品質，必須獨立報告 provider/model/prompt/schema versions、資料policy、token/cost、實際樣本數與失敗例。未跑 live eval，不填品質百分比。

## 4. Performance methodology

先固定 reference device/server、browser、container image、engine profile、graph/dataset fixture、cold/warm 定義與負載分佈，再測原報告 thresholds。DES 1M events 必須明確以實際 processed DES events 計數，不拿 entities 代替；RSS 測 process peak，不僅 heap allocation。API p95 不包含 LLM/長 simulation，但包含正常授權/DB工作；沒有流量模型不可說已達成 SLO。

500 nodes/1000 edges、2D<2s、3D<3s/median≥30FPS、50MB/100k CSV、1M events<15s、RSS<1GB、validation<1s、API p95<500ms、cancel≤5s 都標為 target。test harness 寫硬體與版本資訊；不足機器環境標 NOT RUN，不能用 extrapolation 當 PASS。

## 5. CI 與 release evidence

PR：format/lint/typecheck→unit→contracts→DB migration/RLS integration→golden/property/differential→fake AI eval→container/secret/vuln scan。staging：browser smoke/E2E→performance suite→authorized live AI eval→人工production approval。第三方provider unavailable時明列blocked，不能fallback mock後宣布完整通過。

Golden digest 改變需附 engine/version/semantic原因與 before/after diff，reviewer批准；不能自動重新產expected以消除regression。每次securityfix加入最小repro；每次bug保留failingseed/fixture。

## 6. Release Definition of Done

REQ-001–022 均有對應可執行測試與artifact；所有安全zero-tolerance gates通過；原報告TBD已在相應gate解決；migration/backup/rollback rehearsal完成；immutable/deterministic合同被證明；3D viewer和閉環E2E可用；沒有productionmockfallback；性能達成或有產品负责人明確記錄的門檻調整，不能自行降低。

report中的16週是規劃基線，不是以工期替代驗收。若某項未完成，列出具體blocked test/requirement與影響，不把全部milestone改為done。
