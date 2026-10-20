# Product Requirements — Product MVP

來源：原報告 pp. 1–8、47–53。原則與 P0/P1 分類為 SOURCE；本文件的 requirement IDs、細分驗收、closed-loop interaction 為衍生整理；未定義的行為見 D-001–D-017。Owner 於 2026-10-19 把 P0 第一個 domain 改為電子組裝／測試線（H-016，偏離原報告）；受影響的段落標 H-016。

## 1. 產品定位與成功假設

產品不是單純的產線看板（原報告舉 DevOps dashboard），也不是 3D 動畫工具。定位為 **AI-native Digital Twin Modeling + Simulation + Decision Support**，架構為 General-purpose Digital Twin Kernel + Domain Pack Architecture + AI Modeling Layer。第一個完整 domain 是電子組裝／測試線（H-016；原報告為 Software Delivery / DevOps，其 pack 保留為範例與 conformance pack），不是寫死在 kernel 的唯一 domain。

核心閉環：Describe / Import → Model → AI Proposal → Human Approval → Simulate → Compare → AI Explain → Iterate。

SOURCE 的三個驗證假設：AI 降低建模門檻；simulation 提供使用者願意用於決策的答案；同一 Generic IR / DES / AI 架構可支撐下一 domain。這些是 pilot 要驗證的假設，不是已達成的成果。第二個正式 domain（倉儲人力與尖峰，H-016）不是 P0 launch 必備功能。

Owner 的近期目標（H-016）：做出初步產品與技術驗證後投 launchpad。Owner 目前沒有可接觸的潛在使用者，示範資料一律為 synthetic，不宣稱客戶驗證或模型已對真實產線校準。

## 2. 角色與工作情境

主要使用者為電子製造廠的工業工程（IE）、生產技術與產線規劃人員，以及製造主管（H-016；原報告為工程主管、Platform/DevOps 負責人及 delivery process owner）。權限角色只有 Owner、Editor、Viewer。前兩者可編輯、建立 proposal、批准與啟動 simulation；Viewer 僅讀取；只有 Owner 管理成員。審核自己建立的 proposal 在原報告並未禁止，本草案不要求額外的四眼政策。

代表問題（H-016）：「目前 ICT 測試機 2 台、FCT 3 台、FCT 前緩衝 10 片；若增加 1 台 ICT 或加大緩衝，UPH、瓶頸站、WIP、utilization、阻塞時間與重測比例會如何改變？」原報告的例子是「2 reviewer、3 CI runner」。模型與資料不足時先要求補充，不得將例子中的參數當客戶事實。結果是條件化模擬估計，不是未來保證。

## 3. P0 功能與可驗收需求

| ID | 能力 | 驗收標準 |
|---|---|---|
| REQ-001 | Workspace / tenancy | authenticated user 在有權限的 organization 建 project；跨 tenant 的 read/write/job/artifact 一律不可見；isolation regression 100% pass |
| REQ-002 | RBAC / membership | Owner/Editor/Viewer server-side enforcement；UI 按鈕不構成授權；Owner 可管理既有使用者的 membership |
| REQ-003 | Generic IR / Domain Pack | 電子產線 DomainPack（H-016）產生 valid IR；SoftwareDeliveryDomainPack 保留為範例；generic simulation package 不依賴任何 domain package；unsupported domain fail closed |
| REQ-004 | Template + 2D authoring | 電子產線 full template（H-016；站別由 T-046 定義，研究提案為 SMT→AOI→ICT→FCT→燒機→包裝出貨，含測試失敗→除錯→重測）；原報告的 Source→Review→CI→Test→Deploy→Sink 保留在範例 pack；可 add/delete/move nodes、連接 edges、編輯 properties；client/server validation 一致 |
| REQ-005 | Draft / immutable versions | create 建 model+draft+v1；publish 產生新 immutable version，保留 parent；simulation 不接受 draft ID |
| REQ-006 | Optimistic concurrency | stale If-Match 或 published base 回 409 MODEL_VERSION_CONFLICT；不得覆寫未保存改動或偷偷 rebase |
| REQ-007 | CSV / JSON import | upload→profile/preview→map→validate→commit；invalid rows 可下載；JSON invalid 回 JSON Pointer；未 commit 不修改 Twin |
| REQ-008 | AI modeling | 自然語言輸出 structured proposal；schema/semantic-invalid proposal 不得 ready；資訊不足 needs_input |
| REQ-009 | Human approval | diff、assumptions、server risk、validation 可見；approve 重新驗證；edit 一次 transaction 只建一個新 version；reject 不改 model hash |
| REQ-010 | Scenario | baseline 為空 patch，alternative 為 immutable base 上的 patch；scenario 不改 canonical version；AI scenario 需人類批准後再手動 run |
| REQ-011 | DES | 固定 IR、dataset、engine、config、seed 的 aggregate digest 可重現；bounded event processing；沒有任意腳本執行 |
| REQ-012 | Async runs | durable enqueue 後立即 202；queued/running/completed/failed/cancelled 與 progress 可查；取消於 checkpoint 停止 |
| REQ-013 | Replications / metrics | 初始範圍 1–1000（hard cap TBD）；throughput（含每小時產出 UPH）、lead/cycle、queue、WIP、utilization、failure/rework，以及 REQ-024 帶來的阻塞／缺料時間與故障停機（H-016）；統計 population 與不足情況可見 |
| REQ-014 | Baseline comparison | 同時呈現 baseline、scenario、absolute delta、relative delta；0 分母、缺值及不可比 config 不偽造 percentage |
| REQ-015 | Domain 指標的資料門檻 | domain 指標只在語義資料充足時產生：電子產線的首次良率（FPY）、站別 availability 等由 T-046 定義（H-016）；範例 pack 的 DORA 指標需要 deployment/change/failure/recovery 資料；缺資料標 unavailable，不以一般 cycle time 冒稱 DORA，也不以 utilization 冒稱 OEE |
| REQ-016 | AI analyst | 僅引用供應的 results，重大結論有 metric evidence；區分 observation/inference；不得 invent metric 或宣稱必然結果 |
| REQ-017 | 3D viewer / playback | procedural scene、orbit/pan/zoom、hover/select、tokens、queue/utilization、timeline play/pause/scrub、baseline/scenario switch；same entity IDs；無 3D authoring |
| REQ-018 | Audit | create/edit/publish/proposal/approve/reject/import/run 有 tenant-scoped audit；可追 request_id / run_id；不儲存 private chain-of-thought |
| REQ-019 | Security | JWT/OIDC、tenant isolation、RBAC、RLS、authorized artifact access、AI authority containment、resource budgets |
| REQ-020 | Observability / deployment | structured logs + traces/metrics；containerized web/api/worker、staging 與受控 production approval；no customer content in product telemetry |
| REQ-021 | Quality gates | 六層測試、50 curated modeling intents 初始目標、invalid mutation=0、AI leakage=0、mandatory adversarial invented metrics=0 |
| REQ-022 | Bilingual UI（owner 新增，H-005） | 英文（`en`）為預設與主要語言，另支援繁體中文（`zh-Hant`）；所有使用者可見字串來自 message catalog，兩語 key 完全一致；使用者可切換並記住語言；日期／數字／期間依語言格式化；API 錯誤由 UI 依 error code 在地化；AI 文字輸出依使用者語言，ID、JSON Pointer、enum 與 metric path 不翻譯 |
| REQ-023 | UI design & accessibility（owner 新增，H-007） | 每個 P0 畫面符合 WCAG 2.2 AA（只用鍵盤可完成主流程、focus 可見、色彩對比、螢幕閱讀器可讀名稱與狀態）；共用 design tokens 與元件，元件庫與 styling 方式經比較多個 UI 庫後以 ADR 決定；每個 UI 任務先交可點擊 prototype 經 owner review 再實作；en 與 zh-Hant、手機與桌面寬度皆不跑版 |
| REQ-024 | 產線流動語義（owner 變更 domain 後新增，H-016） | 有限緩衝與阻塞、resource 故障（MTBF/MTTR）與停機、班表（resource 的可用時段）、隨時間變化的投入（release schedule）、批次 process；語義依 T-046 提出並經 owner 批准的 decisions；每項有固定 golden vectors 與獨立 SimPy reference 比對；仍是 bounded event processing、沒有任意腳本 |
| REQ-025 | 模擬與觀測的比對（H-016） | baseline run 有對應的觀測資料時，並列模擬與觀測的產出（例如每週產出、站別 cycle time）與誤差；沒有觀測資料時標 unavailable，不宣稱模型已校準；示範資料標為 synthetic |

REQ-022 不是原報告需求：owner 於 2026-10-18 新增（H-005）；中文定為繁體 `zh-Hant`，owner 確認先只做繁體（H-006）。

REQ-022 的例外（H-008）：UI 元件庫內建、無法以 prop 覆寫的無障礙字串，可用該庫自己的翻譯，但須列清單、審閱並以測試列出。

REQ-024、REQ-025 不是原報告需求：owner 於 2026-10-19 採用 agent 建議、把 P0 domain 改為電子組裝／測試線（H-016）而新增；語義細節為 D-047–D-060，2026-10-20 由 owner 依建議批准（H-018）。

REQ-023 不是原報告需求：owner 於 2026-10-18 同意 agent 建議新增（H-007），並要求比較多個 UI 庫的做法、不直接採用 Tailwind＋shadcn/ui；owner 尚無視覺偏好，預設為中性、資訊密集的工具型介面。

工程補充（已批准）：REQ-002 的 org bootstrap / membership HTTP 範圍見 D-008 / D-012；REQ-007 commit 與 proposal 邊界見 D-009；REQ-010 scenario approval 結果見 D-004；其餘缺值、clock 與一致性細節依專門 Spec。

## 4. UI 與主要旅程

### 建立與人工修改

選 project → 選電子產線 template（H-016）→ 2D editor 顯示 draft revision/base/latest → 編輯 properties → 顯示 validation issue 與定位 → Save Draft → 顯式 Publish。未保存 editor state 與 server draft 分開；切版本不能默默覆蓋 local edits。409 時展示 server revision，要求重新載入／顯式處理，不能自動合併。

### 匯入與 AI 建模

Upload CSV/JSON → preview 與 row errors → mapping 建議（低於 0.80 需確認）→人類確認 mapping → deterministic validation → commit dataset →生成 inference/model proposal → review diff/assumptions/data quality →approve 或 reject。APPROVED D-009：commit 不等於 publish；reject 後 frozen dataset 可保留，但 canonical Twin 完全不變。

### Scenario 與決策

選 immutable version →建立 baseline run →提出 scenario（manual 或 AI）→review/approve AI scenario →手動 Run →monitor/cancel →compare →AI analysis →人類選擇下一個 experiment。使用者必須分辨「把變更存為實際 model version」和「只用於 what-if scenario」；兩種按鈕及回傳物件不可混用。

### 3D 與結果

3D 讀同一 IR/version/run，2D/3D 選取狀態共用 entity ID。sampled trace 不是所有 entity 的完整動畫；UI 明確標示 sampling。沒有 trace 時仍可看 aggregates；不能捏造 playback trajectory。3D 載入失敗不可阻擋 2D / simulation / results。

## 5. P1 與明確排除

P1：MES／CFX 與 GitHub/GitLab/Jira/CI connectors、parameter sweeps、advanced routing/priority/preemption（班表已因 H-016 進 P0）、P0 之後的產線功能（H-018：零容量緩衝、拼板與分板、跨步驟持有資源、依忙碌時間的故障時鐘、交班前做不完不開始、AGV／搬運）、batch AI experiment approval、rich 3D assets、collaboration、SSO/SAML、billing、telemetry streaming、第二 domain（倉儲人力與尖峰，H-016）、iTwin adapter、必要時 dedicated broker / GraphQL。

P0 不做 BIM/CAD、IoT synchronization、physics/CFD/FEA、CRDT、AI autonomous mutation/production actions、arbitrary Python simulation runtime、3D authoring。完整 viewer 本身仍是 P0，不能因為 non-goal 而移除它。

## 6. 工程目標，而非已達成指標

| 面向 | 原報告初始目標 | 限定 |
|---|---|---|
| Graph | 500 nodes / 1,000 edges | Alpha 校準 |
| 2D / 3D initial render | <2s / <3s | reference device TBD |
| 3D interaction | median ≥30 FPS | reference device TBD |
| CSV | 100k rows / 50 MB | load test 後確認 |
| DES | 1M events <15s；peak RSS <1 GB | 2 vCPU / 4 GB reference server TBD |
| API / validation | normal API p95 <500ms；max model validation <1s | AI / simulation 執行時間除外；工作負載 TBD |
| Cancel | requested 後 ≤5s | benchmark 待確認 |
| Security | tenant regression 100%；unsafe/invalid mutations committed=0 | 必須 gating，不是平均值 |

## 7. Launch gates / 驗收證據

Alpha：manual model→simulation→results。AI Alpha：intent→proposal→approve→new version。Product Alpha：CSV→inferred model→approval→scenario→simulate→compare→analysis。Private Beta：外部團隊真實資料與重複 what-if 決策，並已批准 retention/residency/provider policy。

H-016 的近期目標（初步產品＋技術驗證，投 launchpad）對應 Product Alpha，以 synthetic 資料完成；Private Beta 需要外部真實資料，owner 目前沒有接觸對象。

工程完成需要測試與可重現 workload；產品成功需要 pilot 的 decision usefulness 證據。time-to-first-model、time-to-first-simulation、proposal acceptance/rejection、runs/project、comparisons/project 可收集，但 raw model/CSV/prompt 不進 telemetry。原報告未給轉換率或商業 KPI 門檻，不新增虛構目標。
