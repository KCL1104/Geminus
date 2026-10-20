# Native Go DES — Simulation Semantics & Engine Spec

SOURCE：原報告 pp. 19–22、33–35、44–46。**本文件的精確數學／排程細節來自 D-005 / D-006 / D-007 / D-010 / D-011（皆已批准；D-007 的數值為初始預設，待 T-037 benchmark），並非原報告已定義。** golden digest 由 engine 實作（T-020 起）產生後凍結。下面定義是一套可測的候選，不允許 agent 各自選擇互不相容的語義。

## 1. Execution model

Simulation 是 immutable ModelVersion + ordered Scenario Patch + frozen empirical manifest + RunConfig 的純執行。P0 engine 為 native Go discrete-event simulation；SimPy 僅 independent reference。每個 replication 單執行緒處理 event heap，replications 可在 bounded pool 平行；不得在同一 replication 用 goroutine scheduling 决定事件順序。

worker 在 engine 外負責授權、快照解析、dataset preload、job claim、timeout/cancellation、progress persistence、result atomic commit。engine 內無 network/DB/LLM、無任意 script。

H-016 的新 primitive（有限緩衝與阻塞、故障、班表、隨時間變化的投入、批次）提案見 [ADR-026](adr/ADR-026-line-flow-primitives.md) 與 D-047–D-057，2026-10-20 APPROVED（H-018）；在 T-047–T-050 實作並更新本文件之前，本文件的語義是唯一有效的版本。T-046 phase 2 已把它們的 IR 成員、Result 成員與 trace 事件類型加入 contract（TWIN_IR_SPEC §2.1、本文件 §5、§9），但 des-go-0.1.0 不執行任何一個：Compile 對用到它們的模型回 `*engine.InputError`（PRIMITIVE_NOT_SUPPORTED，path 在 `/model` 之下；validator 已先拒絕，engine 另有自己的 kernel 支援清單，D-063 APPROVED，H-019），run 在入列時就是 422，不會被當作沒有這些成員而執行。沒有這些成員的模型，observation、Result 與 digest 都與之前逐 byte 相同（D-047；T-046 phase 2 的 differential 見 checks/T-046_PHASE2_VALIDATION.md）。

## 2. Clock、event order、arrival

internal tick 固定 **1 millisecond**，int64；所有輸入換算成 tick 後做 overflow / safe bound 檢查。單位換算 second=1000、minute=60000、hour=3600000、day=86400000。非負 sample d 先換成 ms，再 round-half-up；sample>0 卻 round 為 0 時 clamp 到 1 tick，sample=0 保持 0。固定 engine numerical profile，不依不同語言的預設 rounding。實作為 `model.Ticks`（T-005）：一次 binary64 乘法後 `math.Round`（對非負值即 half up，且不像 `floor(x+0.5)` 有 0.49999999999999994 的進位誤差），超過 365 天回錯；config_hash、dataset binding 與 engine 共用此函式，修改即須換 engine_version。

heap key = (time_tick,event_priority,monotonic_sequence)。APPROVED priority（D-005、D-032）：SERVICE_COMPLETE=0，在同一事件內原子釋放資源後立即路由（進下一個 queue／delay／sink）；arrival、DELAY_COMPLETE 與其他 immediate state transitions=10，同樣立即路由；internal resource dispatch=20，為該 tick 最後一步。route 不是獨立事件，entity 任何時刻恰處於一個狀態。release 在同 tick 的 dispatch 之前完成；sequence 在 replication 內 strictly increasing。Go map iteration、wall clock、worker scheduling 不得參與 tie-breaking。

source 第一個 arrival 在從 t=0 抽出的 interarrival 之後，而不是自動 t=0；其後每次 arrival 再排下一個。max_entities 若指定，限制該 source 的產生數；否則直到 horizon。多 source 的初始化按 node ID 排序，entity IDs 由 replication/source/local sequence 派生，不使用隨機 UUID。實作（`internal/simulation/engine`，T-020）：node、resource、flow 一律以 ID 的 bytes 排序後編號；ARRIVAL 事件依序 (1) 建立 entity、(2) 為同一 source 抽下一個 interarrival 並排程、(3) 交給 kernel 路由（T-021），此順序決定 sequence，因此決定同 tick 的先後（同 tick 同 priority 的事件依排程先後執行：多 source 只有第一輪依 source ID，之後依各自排程的時間點，例如 a 每 10、b 每 20 分鐘時，t=20 的 b 第 1 個先於 a 第 2 個）；entity ID 為 `r<replication index>/<source node ID>/<n>`（n 從 1 起；node ID 不含 `/`）；tick 晚於 horizon 的事件不排程（抽樣仍已消耗）；stream 依 (node, purpose) 首次使用時建立，彼此獨立，新增一個 source 不改變其他 source 的 arrival。

所有 `event.time <= horizon` 都處理，包括 horizon 同 tick 產生的後續 immediate events；`>horizon` 不執行。horizon 不會自動 drain remaining work；在 horizon 仍未 completed 的 entity 是 censored，不能拿 horizon-arrival 當完整 lead time。zero-time cycles 在 compile 時拒絕（判定方式見 D-033：以分布可能的最小 tick 為準，empirical 於 dataset binding 階段判定），runtime 同時保留 visit/event caps。

## 3. Queue、resource、routing

process 可使用零個或多個 resource。requires bundle 採 all-or-nothing acquire；未湊齊不保留任何部分資源。FIFO request order 用 (queue_enter_tick,request_sequence)。dispatcher 按此序掃描：全部資源足夠、且沒有較早未授予且共享任一所需 resource 的 request，才可 grant；不共享 resource 的 request 可繼續進行。這是候選的 resource-wise FIFO，不是會阻塞所有獨立資源的 global head-of-line queue。

acquire→service_start→sample service duration→schedule complete；complete 原子釋放全部 units，再 route、觸發 dispatch。invariant：0≤in_use≤capacity；entity 同時最多在一個 queue/service/delay/sink 狀態；單一 entity 不會同時擁有兩次 service。

顯式 queue node 只把等待位置歸屬到接續 process；真正的 request buffer 不重複建立。沒有 queue node 時，queue 指標歸屬 process ID。顯式 queue 必須一個 outgoing process，process 至多一個顯式 queue upstream；其他 incoming direct route 仍進入相同 buffer，避免同 resource 出現兩份 metrics。

decision 依 flow ID 升序建立 cumulative probabilities，使用自己的 routing RNG。0% flow 永遠不被選；選第一個 U<cumulative 的 flow，最後一個作浮點容差兜底，但前提概率已通過 validator。非 decision 恰一條 p=1 route。failure/rework nodes 只產生對應 observation，再走明確 flow，不隱含 catch-all retry。

實作（`internal/simulation/engine/kernel.go`，T-021）：每個 resource 有一個 FIFO（依 (queue_enter_tick, request_sequence)）保存需要它的等待 request。依 resource-wise FIFO 規則，同一 resource 的 requests 彼此依序授予，因此只有「在自己每個 resource 的 FIFO 都排第一」的 request（candidate）可能被授予；dispatcher 依 sequence 檢查 candidates：bundle 每個 resource 都夠才授予（all-or-nothing），否則它留在最前面擋住共用這些 resources 的後來者，並標為 held，直到它的某個 resource 被釋放才再檢查（授予只會占用 units）；授予後下一個排到最前面的 request 若成為 candidate 就加入，順序與逐筆掃描完全相同（另以舊版逐筆掃描 kernel 做 differential 驗證）。每次 dispatch 只處理有變化的 candidates，成本不隨等待數增加（T-021 review 與複核）。一個依 FIFO 被擋住的 request 會擋住它 bundle 內所有 resources，即使其中某些 resource 有空（例：W1 要 R1（忙）、W2 要 R1+R2、W3 只要 R2：W3 等，R2 閒置）；這是已批准文字的直接結果。每次授予先回報 bundle 各 resource 新的 in-use，再開始 service、從該 process 的 service stream 抽樣，依授予順序。不需 resource 的 process 在該 tick 的 dispatch 開始服務。SERVICE_COMPLETE 先原子釋放全部 units，再在同一事件內路由；只要釋放了 resource 或有 request 進入 buffer，就確保該 tick 末有一次 dispatch；零時間 service 在 dispatch 之後同 tick 完成時會再排一次 dispatch（仍是 priority 20），等待者不會被擱置。visit 計數：source 為第 1 次，之後每進入一個 node 加 1，經過顯式 queue 時 queue 與其 process 各算一次；超過 max_entity_visits 回 `ErrEntityVisits`；系統內 entity 超過 max_active_entities 回 `ErrActiveEntities`；兩者都帶 entity、node／tick 並讓 run 失敗。sink 的 outcome=abandoned 記為未完成，但同樣離開系統；sink 未寫 outcome 時視為 completed（CB-22 的 sink 預設值）；entity 到達 sink 即釋放其紀錄，記憶體受 max_active_entities 限制。horizon 時仍在系統內的 entity 為 censored。kernel 以 `Observation` 回報事實（arrival、queue enter、service start／end、resource in-use、delay start／end、failure、rework、complete），依處理順序；指標彙整屬 T-022。以上實作解讀併入 D-038（2026-10-18 owner 批准）。

## 4. RNG、seed derivation、determinism

候選 RNG = 本段明確定義的 SplitMix64，不使用 Go math/rand 的未固定預設 generator。所有 uint64 运算 mod 2^64：

```text
state += 0x9e3779b97f4a7c15
z = state
z = (z XOR (z >> 30)) * 0xbf58476d1ce4e5b9
z = (z XOR (z >> 27)) * 0x94d049bb133111eb
output = z XOR (z >> 31)
U = ((output >> 12) + 0.5) / 2^52       # strictly 0 < U < 1
```

replication_seed = first 8 bytes, little-endian, of SHA256(`dt-rep-v1\0` UTF-8 || uint64LE(base_seed) || uint32LE(replication_index))。index 從 0 起。stream seed = first 8 LE bytes of SHA256(`dt-stream-v1\0` || uint64LE(replication_seed) || uint32LE(len(node_id_bytes)) || node_id_bytes || uint32LE(len(purpose_bytes)) || purpose_bytes)。purpose = arrival/service/delay/routing；相互獨立。trace sampling 使用另一 purpose，不消耗以上 streams。

constant 不消耗 random draw。empirical 以 floor(U*N) 等權抽 samples；exponential = -mean*ln(1-U)；triangular 依標準 inverse CDF，min=mode=max 時視 constant；min/max 任一邊退化要避免除 0。所有 sample 再經同一 tick quantizer。T-020 補充：(a) exponential 樣本至少 1 tick（即使 mean 極小、乘積下溢為 0；D-033 視 exponential 恆 >0）；(b) 抽樣結果超過 365 天（只有 exponential 尾端可能）以 365 天＋1 tick 表示，必晚於任何 horizon，事件永不處理，tick 運算不會溢位；(c) triangular 用 `U < (mode-min)/(max-min)` 選分支；(d) decision 的 routes 只含 p>0 的 flow，依 flow ID 排序累加，浮點兜底因此不可能選到 0% flow；(e) triangular 的值在量化前 clamp 回 [min,max]（min=mode=1e-300、U=2^-53 時 max-sqrt(...) 恰為 0，會成為 D-033 禁止的零 interarrival；T-020 review）。以上 (a)–(e)、ARRIVAL 順序、entity ID 與下列 numerical profile 是 T-020 的實作語意，登記為 **D-038（2026-10-18 owner 批准，H-004；gate：T-023 對外提供結果前）**。

Numerical profile（T-020 review 後）：ln 不用平台的 `math.Log`（amd64／s390x 為 assembly，其他 GOARCH 為 pure Go 且可能被編譯器融合為 FMA，末位不同），改用固定的 `engine.ln`：Go `math.log` 的 pure-Go 演算法（源自 FreeBSD e_log.c），每個乘積以明確 `float64()` 轉換捨入，Go 規格保證不融合，因此每步皆為一次 binary64 rounding；sqrt、round、floor、frexp 本身正確捨入或精確，可直接用。`scripts/engine_reference.py` 逐運算移植同一演算法；`testdata/simulation/engine-vectors.json` 保存 12-draw 向量、每個 sampler 10^5 次 draw 的 SHA-256（含量化前 binary64 值，因此公式或運算順序的任何改變都會被抓到，即使 tick 不變）、10^6 個 ln 輸入的 SHA-256、ln 常數 bit pattern 與邊界值、arrival 結束時的 stream state；Go 必須逐 bit 相同。`make backend-numeric`（`scripts/check_no_fma.sh`）檢查 engine 與結果統計程式在 amd64 v3、arm64、ppc64le、s390x、riscv64、loong64 的組語中沒有 fused multiply-add 也沒有呼叫平台 transcendental，並以 GOAMD64=v3（可融合 FMA）重跑數值測試。尚未在實體非 amd64 機器上執行測試（NOT RUN），因此跨架構的 byte-identical digest 仍不宣稱。算法、log/sqrt numerical profile 改變時必須換 engine_version，不把同一 human-readable semver 用於不同數值實作。

SOURCE invariant 仍為同 IR + engine version + run config + seed →同 aggregate digest；本草案另外固定 dataset manifest。Compile 不信任呼叫端：manifest 每個 binding 要通過 schema、依 logical_ref bytes 排序且不重複、涵蓋每個 empirical distribution，且 RunInput.Datasets 必須恰為 manifest 的 bindings（模型未用到的 binding 允許，與 datasets.Resolve 相同）。engine_version 要能唯一追到 container digest、source commit 與 numerical profile。跨架構的 byte-identical digest 未經測試不得宣稱；可部署同一受測 numerical profile 以維持承諾。

## 5. Warmup、metrics、統計 population

令 W=warmup tick，H=horizon tick，必須 0≤W<H≤365天（horizon cap 也是 D-005 候選）。觀測時間長度 T=H-W；邊界 events W≤t≤H 計入，time integral 取 [W,H]。warmup 前照常執行事件、維護資源與 queue，但 warmup 不重設系統狀態。

| 指標 | 候選定義 |
|---|---|
| throughput | 每個 replication 在 [W,H] 進入 completed sink 的 entity count；mean/p50/p95 跨 replications，不是每分鐘速率 |
| lead_time_minutes | arrival≥W 且 completion≤H 的完整 entity cohort；(completion-arrival)/60000；跨 replications pooled entity quantiles，另保留每 replication mean |
| queue wait | queue_enter≥W 且 service_start≤H 的完整 visit；包括零等待；重作每次 visit 都算一筆；不把未離隊 entity 當已知等待 |
| queue_length_mean | ∫queue_length(t)dt / T；從 W 的真實 queue state 積分 |
| utilization | ∫in_use(t)dt / (capacity*T)；含跨 W/H 的部分 service interval；跨 replications 算 mean |
| failure_count / rework_count | 在 [W,H] 執行 generic FAILURE/REWORK observation 的次數；不是 DORA failure rate |
| censored_entities | H 時仍 active 的 entities 數；呈現作截尾風險，不能納入 completed lead sample |

quantile 候選採線性插值 Type 7：排序 x，h=(n-1)*p；interpolate floor(h)/ceil(h)。mean 是該 population 的算術平均。禁止把「各 replication 的 p95 平均」稱為 pooled p95。

95% mean CI 採 independent replication mean 的 Student-t interval；n<2、replication 沒完整 cohort、沒有足夠 independent observations 時回 null + reason。對 lead time，CI 描述「replication mean 的平均」而非 pooled entity mean；UI 標示 estimand，不暗示 interval 對 p95 也有效。若要避免這個不同 estimand 混淆，可先令 pooled lead summary 的 CI=null，另在 additional_metrics 輸出 replication_mean_lead_time。**本草案採後者**：只有 population=replication 的 summary 可附此 CI。

產線流動的 Result 成員（D-058，T-046 phase 2 定名；只在模型用到對應功能時出現，所以沒有這些功能的 Result 與 digest 不變，D-047）：`metrics.domain`（DomainPack 的 model metrics，T-051）；每個 replication 的 `replication_summaries[i].resource_states[<resource>]`（[W, H] 內的整數 tick 積分與次數：`in_use_ticks`、`scheduled_processing_ticks` 一律有；`blocked_ticks`（模型有有限 buffer）；`setup_ticks`、`setups_started`、`setups_completed`、`completed_setup_ticks`（setups）；`down_ticks`、`breakdowns_started`、`repairs_completed`、`completed_repair_ticks`（breakdowns）；`scheduled_ticks`、`scheduled_in_use_ticks`、`overtime_ticks`（calendar）；`available_ticks`、`idle_ticks`（breakdowns 或 calendar）；schema 以 `dependentRequired` 要求每組同時出現）、`blocking[<node>]`（`blocks_started`、`blocks_completed`、`completed_block_ticks`、`blocked_entity_ticks`）、`buffers[<queue>]`（`full_ticks`，D-049 的 buffer 滿比例）、`batches[<process>]`（`batches_started`、`members_started`）、`declared[<metric id>]`（宣告式指標的每個 replication 的值：system_wip 為數，flow_count／sink_count 為計數，period_throughput 為每期計數的陣列（至多 1000 期），不可用時為 null）；宣告式指標跨 replication 的 summary 在 `metrics.additional_metrics[<metric id>]`。各成員的定義與公式見 ELECTRONICS_LINE §5；由哪個任務產生見 TWIN_IR_SPEC §2.1。

schema 中 `status=unavailable` 時 mean/p50/p95/CI 都是 null，reason 說明缺少什麼。沒有 completed entities 時 throughput=0 仍 available（有 replication count 樣本），lead time unavailable；缺 metric 不能回 0。

實作（`internal/results/metrics`，T-022；以下細節為 **D-039**，2026-10-18 owner 批准）：每個 replication 一個 `Collector`（engine Observer），依 replication_index 升序 `Build`；所有加總依固定順序，乘積明確捨入（與 D-038 同一 numerical profile，`make backend-numeric` 檢查）。schema 現在要求 unavailable 必有 reason，且只有 population=replication 可帶 CI（CB-23）；queue stats 同樣：unavailable 時 waits 為 null 且有 reason，available 時 waits 為數字且 sample_count≥1；resource 的 utilization_mean 為 null 時必有 reason（T-022 review）。available summary 的 reason 一律 null；n<2 時 CI 為 null，原因由 sample_count 表達（與 valid-result fixture 一致）。`additional_metrics.replication_mean_lead_time_minutes` 是各 replication cohort 平均的 summary，帶 CI；只要有一個 replication 沒有完整 cohort 就 unavailable（`REPLICATION_WITHOUT_COMPLETED_COHORT`，所有 replication 都沒有時為 `NO_COMPLETED_ENTITY_IN_WINDOW`），因為「replication means 的平均」此時沒有定義。queue wait 跨 replications pooled（每個 visit 一筆），沒有 visit 時該 queue unavailable（`NO_COMPLETED_QUEUE_VISIT_IN_WINDOW`）；queue_length_mean 與 utilization 是各 replication 時間積分的平均。reason codes 仍為暫定（CB-23）。IR 的 `metrics[]` 宣告尚未對應到 Result（CB-22 剩餘部分，T-029）。

## 6. DORA-oriented metrics 的 gating

本節適用 Software Delivery 範例 pack。P0 domain 自 H-016 起為電子組裝／測試線，其指標（FPY、站別 availability 等）與資料門檻由 T-046 定義（REQ-015；提案見 [ELECTRONICS_LINE.md](domains/ELECTRONICS_LINE.md) §5 與 D-058，APPROVED，H-018）；同一原則：未提供 mapping 一律 unavailable，不以 utilization 冒稱 OEE。

SOURCE 僅要求資料充足才計算；沒有提供完整 domain event taxonomy。APPROVED D-010：由 domain pack 保存明確 stage/event-to-milestone mapping 與 data-quality evidence，未提供 mapping 一律 unavailable。一般 source→sink lead time 不能自動當 Change Lead Time。

Change Lead Time 需要 change commit/deployed 關係與時間；Deployment Frequency 需要可辨識 production deployment 及觀測期間；Failed Deployment Recovery Time 需要 failure 與 recovery 配對；Change Failure Percentage 需要 deployment denominator 與 failure classification；Deployment Rework Rate 需要可辨識 rework deployment 與 denominator。公式、dedup/batching、calendar denominator 需在 DomainPack metric definition 明列並經 pilot 資料確認。P0 fixture 的 CI fail/rework 不足以冒稱 deployment failure/recovery。

## 7. Baseline / scenario comparison

比較前要求同 organization、model_version、engine numerical profile、dataset manifest、horizon、warmup、replications、seed；scenario_patch 不同是實驗變因。baseline patch 為 []。若條件不相同，回 comparable=false / reason，不以 inferential comparison 呈現；可保留並排 descriptive values。

absolute_delta=scenario-baseline；relative_delta=absolute_delta/baseline。baseline=0 時 relative_delta=null，reason=BASELINE_ZERO；任何一邊 unavailable 則相關 delta=null。API 提供實際計算值，AI 不重新發明。UI 的百分比為 relative_delta*100，不把 -0.11 當 -0.11%。source 中展示 -11% 等數字是示例，不是本包驗算結果。

matching seed/node streams 可用於 paired experiments，但 scenario topology 變更、不同 replication cohort 或不相容 config 時，不預設 paired CI 有效。P0 先只輸出已定義的 absolute/relative delta；paired difference CI 可在專門測試/評審後新增。

## 8. Execution budgets、progress、cancellation

初始候選：≤1000 replications；≤300s wall timeout/run；≤10M processed events/run（所有 replications 合計）；≤100k active entities/replication；≤10k node visits/entity；sampled trace≤100k events/run。warmup 事件同樣消耗 budget。API 不能接受使用者把上限調高；effective execution limits 在 enqueue 時保存。CPU/memory container limits 與 admission quotas 仍需獨立配置。

context checkpoint 至少每個 heap pop、每個 distribution 批量處理段、每次 replication、每個 artifact/aggregation batch。T-020 實作：每次 pop 先檢查 cancellation，再從整個 run 共用的 event budget 扣 1（含 warmup；平行 replications 以 atomic 計數，超過即 `ErrEventBudget`，run 失敗、不產生結果）；不變量被破壞回 `ErrInternal`，不會靜默略過。worker bounded polling 將 cancellation_requested 傳入 context。≤5s 是原報告待 benchmark 的目標，不是已測結果。timeout / event cap / active entity cap / cancellation 分別有 typed error；不要包裝為 completed。D-050 的 runtime backstop 另有兩個 typed error，由 T-047–T-050 實作：`ErrDeadlock`（B1，循環等待；job failure code `SIMULATION_DEADLOCK`）與 `ErrStalled`（B2，bundle 永遠湊不齊；`SIMULATION_STALLED`），run failed、沒有部分結果；API 照 T-023 對 execution limit 的暫定對應以 `MODEL_VALIDATION_FAILED` 顯示，原 code 在 `details[0].code`（CB-38(a)；`simulation.schema.json` 的 `Run.error` 註解、`testdata/contracts/valid-run-failed-deadlock.json`）。

T-023 實作（`engine.Credit`、`des.Options`、`internal/runs/runner`）：event budget 改為分批授予的 credit。`engine.NewCreditBudget(limit, granted, credit)` 先授予 `granted` 個 events，用完時（在 mutex 下，同一時間只有一個 `Grant`，其他 replication 等它）呼叫 `Credit.Grant(ctx, granted)` 取得新的總授予量（必須大於舊值且不超過 limit，否則 `ErrInternal`）；已達 limit 時為 `ErrEventBudget`，Grant 的錯誤（含 context 取消、wall timeout）使 run 以該錯誤失敗，之後不再 Grant。每次 pop 的扣除照舊（先檢查 cancellation 再扣 1）；只有授予量改變，事件的處理順序、RNG 與結果不變：不同批次大小與 worker 數下 digest 相同（`des/credit_test.go`）。`des.Output.Events` 是實際用掉的 events。Worker 的 `Grant` 在 lease 下把 run 的 `event_budget_reserved` 由上次的值推進到新值才回傳，所以 engine 從不使用未保留的 events；batch 預設為 max_events 的十分之一（至少 1）。Retry 時 engine 的 limit 為 `max_events − 之前 attempt 保留的量`、wall timeout 為從第一次開始起剩下的時間（無條件進位到秒，且 attempt 另以絕對 deadline 結束）。Progress 由 worker 每秒寫入一次（engine 的 sink 只保留最新一筆）；同一個 checkpoint 讀回 `cancellation_requested`，已請求時以 `context.CancelCause` 停止 engine（engine 在下一個 pop 前看到），worker 以 cancelled 結束 job 與 run，不存結果。≤5 s 的取消目標在測試中實測（`TestCancelRunning`，見 checks/T-023_VALIDATION.md），不是 benchmark。

aggregate 依 replication_index 升序彙整；progress 不是 correctness input。所有 replications 完成後才可完成 run。重試使用原 frozen input；lease token 失效的 attempt 必須丟棄 finalize，不可覆蓋後來的成功結果。

## 9. Result digest / sampled trace

result_digest 的 payload（D-006 已批准，實作為 `hashing.Result`，T-005）：engine_version、metrics、replication_summaries、measurement_window、reproducibility（排除 result_digest 本身）；不含 run_id、status、created_at、wall duration、progress、worker ID、trace（含 object key）、LLM output。對此 payload 做 JCS + SHA-256；保留完整 numerical values，不因方便而捏造 round-trip equality；NaN／Infinity 無法序列化，回錯而非產生 digest。

trace_level 不應改變數值結果；它仍在 config_hash 中，因此改 trace_level 可改 digest，但 metrics 必須相同。sampled trace 以穩定 entity hash 選取，trace caps 不得停 simulation。trace event 至少 time_tick、sequence、type、entity_id、node_id，可有 resource_id；順序依同一 event order。UI 明示 sampling，不能把缺失樣本補成虛構 trajectory。

實作（`internal/simulation/trace`、`internal/simulation/des`，T-022；**D-039**，2026-10-18 owner 批准）：trace 只記 replication 0；entity 被選中的條件是 SHA-256(`dt-trace-v1\0` || entity ID) 的前 8 bytes（little-endian）< 2^64/8（rate 1/8），不消耗任何 RNG stream（`scripts/engine_reference.py` 有獨立的選取向量）。記錄到 max_trace_events 為止，之後標 `truncated=true`，simulation 照常跑完。artifact 格式為 `simulation.schema.json#/$defs/TraceArtifact`（`geminus.trace.v1`）；events 依 engine 處理順序（tick、event priority、sequence），因此同一 tick 內 sequence 可能變小，消費端保留陣列順序；queue_enter 的 node 是 entity 實際進入的 node：經過顯式 queue 時為該 queue，直接進入 process 時為 process（即使該 process 上游另有顯式 queue；queue 指標仍依 §3 歸顯式 queue，T-022 review）。trace_level=none 或 max_trace_events=0 時沒有 artifact；沒有任何事件時也沒有 artifact，Result.trace.available=false。artifact_id 由 worker 上傳後填入（T-023）。T-046 phase 2 在 `geminus.trace.v1` 加入 ADR-026 的事件類型（additive，與 D-047 對 IR 的做法相同，format 不改）：`blocked`、`unblocked`（D-049，node 為 entity 被擋住的出發 node，T-047）、`setup_start`（D-055，帶 `resource_id`，T-050），以及批次成員 `service_start`／`service_end` 的 `batch_id`（`r<rep>/<process>/b<n>`，D-054，T-050）；schema 規定 `resource_id` 只在 `setup_start`、`batch_id` 只在 `service_start`／`service_end`。des-go-0.1.0 不產生這些事件。

Engine（`des.Engine`）：Compile → 以 bounded pool（預設 GOMAXPROCS）跑各 replication，共用 event budget 與 wall timeout（timeout 涵蓋 replication pool；Compile、Build 與 digest 在其外，Build 在 1000 replications、10M events 的上限下約數百 ms）；progress 由獨立 goroutine 以「只保留最新一筆」的方式送出，從不並行、計數不倒退、最後一筆一定送達（可能在 Run 返回後），sink 再慢也不影響 run 的結果或時間（T-022 review）；sink panic 後不再收到 progress，run 不受影響；worker 寫 progress 時須以 run status／lease fence，因為最後一筆可能在 Run 返回後才到（T-023）；replication 內或 `DomainMetrics` 中的 panic 視為 bug，回 `ErrInternal` 而不終止 process；全部 replication 完成才算完成（之後才到的 deadline 或 cancel 不推翻已完成的 run）；任何 replication 失敗即整個 run 失敗並回 typed error（`ErrWallTimeout`、`engine.ErrEventBudget`、`ErrActiveEntities`、`ErrEntityVisits`、context 錯誤、`*engine.InputError`），不回部分結果；多個 replication 同時失敗時回報哪一個 limit 可能不同，但回報的 limit 一定真的超過。effective_model_hash 由 engine 從實際執行的 IR 重算，與凍結值不同即拒絕；model_hash 為 pinned version 的值，照凍結值記錄。DORA 由程序注入的 `DomainMetrics`（DomainPack.ComputeMetrics）在 digest 之前填入，generic engine 不 import domain pack。engine_version 為 `des-go-0.1.0`，部署可附加 build identity。run_id 由 worker 設定。GOLDEN-001／002 的 digest 鎖在 `testdata/simulation/engine-golden-digests.json`（engine 輸出的 regression lock，不是獨立 oracle）。

## 10. 必備正確性案例

`testdata/simulation/` 提供**預期值 fixtures，不是已執行 Go engine 的成果**。首個案例：interarrival=10 min、service=5 min、capacity=1、100 entities、horizon=1010 min，第一個 arrival=10，最後 completion=1005；completed=100、wait=0、lead=5、utilization=500/1010。

congestion：interarrival=5、service=10、capacity=1、20 entities、horizon=205，完成 20、wait_i=5*i（i=0..19），wait mean=47.5、p95=90.25、utilization=200/205。原報告「queue grows」不能理解為每個 event 都單調；服務完成會降低 queue。候選驗收在 arrival 同 tick dispatch 完成後取樣，檢查 source active period 的 queue 趨勢。

還要測 100%/0% routing、same-tick release/arrival、bundle acquire、nonnegative capacity、zero-time cycle reject、empirical binding/hash mismatch、warmup cohort、horizon equality、censoring、worker retry determinism、cancel/finalize race。SimPy stochastic differential 比較 confidence/tolerance，不要求兩套 RNG byte-identical；tolerance 須先固定，不能看失敗結果再調到過。
