# ADR-026 — 產線流動的核心 primitive：有限緩衝與阻塞、故障、班表、投入排程、批次

狀態：**ACCEPTED**（2026-10-20，owner H-018：「全都照建議」；D-050 採 A）。2026-10-19 由 T-046 phase 1 提出。Schema 與 fixtures 的變更在 T-046 phase 2，kernel 在 T-047–T-050；實作前 TWIN_IR_SPEC 與 SIMULATION_SPEC 現行的 P0 語義照舊。**實作進度：有限緩衝與阻塞（D-049）、D-050 的 R1 與 B1、queue `capacity` 的 D-057 risk 已由 T-047 實作（des-go-0.2.0，2026-10-20，待獨立 review；checks/T-047_VALIDATION.md）；resource 故障（D-051，班表之外的部分）、lognormal 與 Weibull（D-056）、D-050 的 R3 與 B2（TTR 的部分）、breakdowns 的 D-057 risk 已由 T-048 實作（des-go-0.3.0，2026-10-25，獨立 review 第 2 輪 ACCEPT；checks/T-048_VALIDATION.md；實作解讀 D-074、D-075 於同日 APPROVED，H-022）；班表（D-052：calendars、shift_capacity、例外）、隨時間變化的投入（D-053：poisson、deterministic、table）、D-050 的 R2、R4 與 B2 的 next_fit／X_end、故障時鐘只在上班時前進（D-051 (ii)）、calendars 與投入排程的 D-057 risk 與 AI 不能新增 calendar 的規則已由 T-049 實作（des-go-0.4.0，2026-10-25，待獨立 review；checks/T-049_VALIDATION.md；實作解讀 D-081–D-085 為 PROPOSED）**；其他 primitive（批次、換線、宣告式 metrics）仍被 D-063 的 gate 擋住。日期：2026-10-19。依據：H-016、[ADR-025](ADR-025-p0-domain-electronics-line.md)、[ADR-006](ADR-006-native-go-des.md)、[ADR-010](ADR-010-domain-pack-architecture.md)、TWIN_IR_SPEC §8、REQ-024、TEST-034。語義細節為 D-047–D-057（APPROVED，H-018，[登記](../DECISIONS_AND_TBD.md)）；domain 規格與 D-058–D-060 見 [ELECTRONICS_LINE.md](../domains/ELECTRONICS_LINE.md)。

## Context

**現行 kernel（`internal/simulation/engine`，des-go-0.1.0，D-005／D-032／D-038 已批准）的做法**，本 ADR 的每個提案都以它為基準：

- 事件 heap 以 (tick, priority, seq) 全序；priority 只有三個：SERVICE_COMPLETE=0、ARRIVAL／DELAY_COMPLETE=10、DISPATCH=20。`schedule` 對晚於 horizon 的事件直接丟棄、不取 seq。route 不是事件：SERVICE_COMPLETE 先原子釋放整個 bundle、記 ServiceEnd，再在同一事件內沿 decision／failure／rework 走到下一個 buffer、delay 或 sink；ARRIVAL 依序建立 entity、排下一個 arrival、路由。
- 每個 process 有一個無限的 request buffer（顯式 queue node 只是它的名字）；每個 resource 一個 FIFO；只有在自己每個 resource 的 FIFO 都排第一的 request 是 candidate，dispatch 依 seq 取 candidate，bundle 全部放得下才授予（all-or-nothing），否則標 held，直到它的某個 resource 被釋放才再檢查（D-038 (k)(m)）。
- Resource 的 unit 是匿名的：kernel 只有 `inUse` 計數，不知道哪個 unit 服務哪個 entity；capacity 在整個 run 中固定。
- RNG stream 以 (node ID, purpose) 在第一次使用時建立（purpose：arrival、service、delay、routing；trace 另用）；seed 由 SHA-256 推導，彼此獨立。
- Kernel 以 Observation（kind、tick、seq、entity、node、resource、value）逐筆回報；`testdata/simulation/kernel-observation-digests.json` 鎖住 observation 序列；`engine-golden-digests.json` 鎖住 GOLDEN-001／002 的 result_digest，其 payload 含 engine_version、metrics、replication_summaries、measurement_window、reproducibility。

**為何需要新 primitive。** 研究稿（未提交、未驗證，只當輸入）認為工廠工程師把有限緩衝與阻塞、機台故障、班表、批次視為基本功能，缺了會直接否定結果；換線在 high-mix SMT 也常見。這些都無法用現有 primitive 組出來：resource 在每個 process 結束時就釋放，無法跨步驟持有「下游空位」；capacity 不隨時間變；routing 沒有記憶；source 只有固定分布的 interarrival。以 script 或 domain pack 繞過違反 AGENTS §2、§7 與 ADR-010。

**限制。** Kernel 仍是純 Go、單執行緒 replication、bounded events、沒有任意 script（REQ-011、REQ-024）；generic kernel 不讀 domain 屬性；每個新功能都要讓**沒用到它的模型與現行 kernel 逐 bit 相同**，並有 golden vectors 與獨立 SimPy reference（TEST-034）。

## Decision（提案）

新增下列 generic primitive，全部是 IR 的 optional 欄位，缺欄位時語義與現行完全相同。欄位名稱已於 T-046 phase 2 的 schema 變更中定案（下表；完整形狀見 TWIN_IR_SPEC §2.1，Result 與 trace 成員見 SIMULATION_SPEC §5、§9），schema_version 維持 `0.1`。T-047–T-051 實作前，用到任何一個的模型由 validator 與 engine 以 `PRIMITIVE_NOT_SUPPORTED` 拒絕（D-063 APPROVED，H-019）。

| Primitive | IR | 摘要 | Decision | 實作 |
|---|---|---|---|---|
| 相容性與驗證方法 | — | optional 欄位、schema_version 維持 0.1、Result 新成員只在用到時出現、每個 kernel 任務升 engine_version minor、凍結舊 kernel 做 differential | D-047 | T-047–T-050 |
| 同 tick 順序 | — | 新增 priority 15（故障、修復、班表容量變化、批次逾時）；解除阻塞在 dispatch 內 | D-048 | T-047–T-050 |
| 有限緩衝與阻塞 | queue node `capacity` | blocking after service；被擋的 entity 留在出發 node、保留整個 bundle；FIFO 解除 | D-049 | T-047（已實作，des-go-0.2.0） |
| 死結與停滯 | — | validator（全為 error）：`BLOCKING_CYCLE`、bundle 的班表交集（`CALENDAR_INVALID`）、故障 resource 的 bundle 形狀、故障 resource 與夥伴的上班時間（`STALL_RISK`）；runtime：`ErrDeadlock`（循環等待，精確）與 `ErrStalled`（湊不齊，期限 next_fit(max(t_q, X_end)＋TTRmax)；不誤報）；保證涵蓋每個 valid 模型 | D-050 | T-047–T-050 |
| 故障 | resource `breakdowns` | 每個 unit 一組故障時鐘，只在該 resource 上班時前進；finish-first；stream (resource ID, `ttf/<u>`／`ttr/<u>`) | D-051 | T-048（已實作，des-go-0.3.0；只在上班時前進的部分隨 T-049 的班表） |
| 班表 | 根層 `calendars[]`（含 `exceptions`）、resource `calendar_id` 與 `shift_capacity` | 週期性上班區間、每班容量、以絕對 model 時間表示的例外日；交班時 finish-first；新增 scheduled utilization，原 utilization 不變 | D-052 | T-049 |
| 投入排程 | source `arrival.schedule` | 週期性分段常數 rate（poisson／deterministic，每個 arrival 一次 draw）或投入表（table） | D-053 | T-049 |
| 批次 | process `batch` | min／max／max_wait；一批一個 request、一次 service draw；整批離開才釋放 | D-054 | T-050 |
| 換線 | resource `setups` | P0：sequence-dependent 換線矩陣、每個 unit 自己的換線狀態、FIFO 派工；stream (resource ID, `setup/<u>`) | D-055 | T-050 |
| lognormal／Weibull | distribution `lognormal`（mean、cv）、`weibull`（shape、scale） | P0：反函數法、每個樣本一次 draw、固定的 exp 與 AS241 | D-056 | T-048（已實作，des-go-0.3.0） |
| Scenario 與 risk | patch allowlist | 新參數可在 scenario 中修改，risk 至少 medium（擴大 buffer 除外） | D-057 | T-047–T-050 |

以下逐項列出問題、選項與後果、建議、對決定性與 digest 的影響、golden vectors 草案與 SimPy reference 的做法。Golden vectors 的數字由手算得出，是草案，不得看 kernel 的結果再改預期值。`scripts/engine_reference.py` 只移植數值 profile（RNG、量化、sampler、事件順序與 arrival），它自己聲明不是 DES engine，所以只能交叉驗證數值向量（例如 D-053 的 G-R1／G-R2／G-R6、D-056 的分布向量）；阻塞、故障、班表、批次、換線的 kernel golden 要由獨立的 SimPy reference，或擴充 `kernel_check_test.go` 那種只看 observation 的檢查器來獨立確認，再凍結。

### D-047 相容性契約與 differential 方法

**問題。** 新 primitive 如何進入 IR、Result 與 engine_version，才能讓沒用到的模型與現行 kernel 逐 bit 相同？怎麼證明？

**選項。**
- A：IR 加 optional 欄位，schema_version 維持 `0.1`，缺欄位＝現行語義；Result 的新成員只在模型用到對應功能（或在 IR `metrics[]` 宣告，D-058）時出現；每個改 kernel 的任務把 engine_version 升一個 minor。後果：既有 ModelVersion 的 bytes、model_hash 與結果都不變，不需 migration 或升級流程；closed-object schema 讓不認得新欄位的舊 reader 仍 fail closed；schema_version 不再表示「用到哪些功能」，要看 IR 內容。
- B：新 schema_version `0.2`（0.1 的 superset），0.1 文件禁止新欄位，另做明確的升級操作（產生新 draft，hash 改變）。後果：版本語義嚴格；多一個升級 API 與 UI 流程，template、AI、patch allowlist 要同時處理兩版。
- C：新統計一律加進每個 Result。後果：所有既有模型的 result_digest 都改變，違反 REQ-024 的「沒用到即相同」；不採用。

**建議：A。** IR 仍是 P0 草案（TWIN_IR_SPEC 開頭）、尚無 production 資料，A 是最少的變更；若 owner 要嚴格的版本語義，改採 B，其餘 decision 不受影響。

**細則。**
- Go 型別對缺少的新欄位一律不輸出（pointer 或 `omitzero`），既有 fixtures 重新 encode 後 model_hash 相同（contract test）。
- 新事件計入 `max_events`；被擋住或在 release backlog 中的 entity 計入 `max_active_entities`；新的 runtime 失敗有 typed error（`ErrDeadlock`、`ErrStalled`，D-050）。
- 新 observation kind 接在現有編號之後，不重新編號（observation digest 編碼含 kind 數值）。新功能只對實際的轉換記 observation：t=0 的初始容量、初始的「正常」狀態都不記（Capacity、ResourceUp 只在改變時出現），初始值由 Program 提供給 Collector。
- 既有的鎖定檔（`kernel-observation-digests.json`、`engine-golden-digests.json`）**不重新產生**。

**決定性與 digest。** engine_version 在 digest payload 中，升版會改變 digest 字串；因此對**沒有任何新欄位**的模型，「逐 bit 相同」的判準是：observation 序列（含 seq）相同，且把 Result 的 engine_version 換回 `des-go-0.1.0` 後算出的 digest 等於鎖定值。用了新欄位（即使是中性設定）的模型會多出新的 Result 成員，digest 必然不同，所以只比 observation 序列與既有的 Result 成員（見下方第 2 步）。新版本另立自己的鎖。

**Differential（每個 T-047–T-050 都要做）。**
1. 把 des-go-0.1.0 的 kernel 與 replication 凍結成 test-only 副本（T-021 複核保留舊 kernel 的同一做法），以 `kernel_check_test.go` 的隨機模型產生器（bundle、顯式 queue、零時間 service、decision 迴圈、failure／rework）跑不含新欄位的模型，observation 序列逐筆相同；件數在實作任務中固定（T-021 的複核約 50,000 個）。
2. 「中性設定」與不設定的模型比較：observation 序列（含 seq）逐筆相同，且既有的每個 Result 成員（metrics 的 throughput、lead time、resources 的既有欄位、queues、failure／rework count、replication_summaries 的既有欄位、measurement_window）逐值相同；功能帶來的新成員不比，也不比整份 digest。中性設定中不得出現任何新 observation（包括 t=0 的 Capacity 或 ResourceUp）。中性設定：buffer capacity ≥ `max_active_entities`（不可能擋住）；breakdown 的 TTF 為 constant 且晚於 horizon（事件不排程、不取 seq）；calendar 只有一個涵蓋整個 period 的 shift（容量不變，不排事件）；單段的 deterministic 投入排程，rate 與 period 選可精確表示、1/rate 為整數 tick 的值（例如每 minute 0.5 件、period 60 minute），與同值的 constant interarrival 同時刻（兩者都不抽 RNG）；單一產品、沒有 `initial` 的 setups（永不換線，D-055）。批次沒有中性設定（批次 request 取得 FIFO 位置的時點不同，D-054），不列入。
3. GOLDEN-001／002 與既有 des、metrics 測試（模型都沒有新欄位）在新 kernel 上照跑，依上面「沒有新欄位」的判準比對鎖定 digest。

**SimPy。** 不適用；本項的 reference 是凍結的舊 kernel。

### D-048 新事件的同 tick 順序

**問題。** 故障、修復、班表容量變化、批次逾時與解除阻塞，在同一 tick 中排在哪裡？

**選項。**
- A：新增 priority 15（resource state 類：FAILURE、REPAIR、CAPACITY_CHANGE、BATCH_TIMEOUT），在 arrival／delay（10）之後、dispatch（20）之前，同 priority 依 seq；解除阻塞不是事件，在 dispatch 內、造成空位的授予之後立即進行（D-049）。後果：該 tick 的 dispatch 看到該 tick 最後的容量；priority 0 釋放的 unit 可被同 tick 的故障拿走；既有三個 priority 的相對順序不變。
- B：priority 5（service complete 之後、arrival 之前）。後果：dispatch 結果與 A 相同（dispatch 仍在 20），只有 observation 順序不同；沒有額外好處。
- C：priority 25（dispatch 之後）。後果：修好或上班的容量要等下一次 dispatch，每次都要多排一個 dispatch，事件數增加；同 tick 的 arrival 看不到新容量。

**建議：A。** 理由：容量的改變在授予之前生效，不需要額外的 dispatch，也不改變既有事件的順序。

**細則。** t=0 初始化順序（review L3）：(1) 各 source 的第一個 arrival（node ID 順序，與現行相同）；(2) 各 calendar 的第一個 CAPACITY_CHANGE（S 會改變的才排；calendar ID 順序；每個 calendar 一個事件，D-052）；(3) 各有 breakdowns 的 resource（resource ID 順序）的各 unit 第一次故障（unit index 順序；scheduled 時鐘依 calendar 算出時刻，D-051）。批次逾時只在 entity 進入 buffer 後才排。同一 tick、同為 priority 15 的事件依 seq，也就是依排程的先後。若 dispatch 進行中為當前 tick 排了 priority 15 的事件（例如 0 tick 的修復），它在該次 dispatch 之後執行，並視需要再排一次 priority 20 的 dispatch（D-038 (j) 的延伸，tick 不倒退）。

**決定性與 digest。** 全序不變；沒有新功能的模型不會出現 priority 15 的事件，seq 與現行相同。

**Golden vectors。** (1) t 時 priority 0 釋放 unit，同 tick 故障（15）拿走它，等待的 request 在 t 不被授予；(2) 同 tick 的 arrival（10）與修復（15）：dispatch（20）在 t 授予該 arrival；(3) 同 tick 的 arrival 與上班：在 t 授予；(4) 0 tick 修復在 dispatch 之後到達：同 tick 再 dispatch 一次。

**SimPy。** SimPy 同時刻的事件依排程順序（部分內部事件為 URGENT），不嘗試重現本 kernel 的 tie-break；同 tick 的語義由手算 golden 固定，SimPy 比對只用沒有同時刻 tie 的確定性案例或預先固定容差的隨機案例。

### D-049 有限緩衝與阻塞（blocking）

**問題。** (1) 容量定義在哪、算什麼；(2) blocking after service（BAS）還是 before service（BBS）；(3) 被擋的 entity 如何持有 resource；(4) 與 resource-wise FIFO、all-or-nothing bundle 的交互；(5) source 與 delay 被擋時怎麼辦。

**選項。**
- A（BAS）：顯式 queue node 加 `capacity`（1–100000），只計等待中、尚未授予的 entity，缺＝無限；entity 的路由走到已滿的 buffer 時，留在出發的 node 成為 blocked：process 保留整個 bundle，source 形成 release backlog，delay 不占資源；buffer 有空位時依 blocked 的 FIFO 移入。後果：符合產線「做完的板子留在機台上，直到下一站有位置」；不需預留機制；不改 routing stream 的抽樣時點。
- B（BBS）：下游 buffer 有空位（或預留一格）才開始服務。後果：授予時要跨 process 預留下游空位，與 all-or-nothing bundle、resource-wise FIFO 的交互複雜；經 decision 的機率路由在服務前不知道去哪，要提早抽 routing 或預留所有可能的下游，改變 routing stream 的抽樣時點；不適合有測試失敗分支的產線。
- C：以「下游 slot resource」模擬（process 的 requires 加入下游 buffer slot）。後果：現行 kernel 在每個 process 結束時釋放全部 resource，slot 無法跨 process 持有；需要「跨步驟持有 resource」這種更一般的新 primitive（未排入 P0，見 Consequences）。

**建議：A。** 理由：BAS 是產線的常見假設、工程師容易理解；它只在「進入 buffer」這一步加檢查，routing 的抽樣時點、resource-wise FIFO 與 bundle 規則都不變，沒有被擋時與現行完全相同。

**語義（A）。**
- **容量與「滿」**：capacity 屬於 process 的 request buffer（D-005：顯式 queue 只是它的名字），不論 entity 經 queue node 或直接進入 process 都算；占用數 = 等待中、尚未授予的 entity 數（不含服務中）。只有顯式 queue node 能宣告 capacity，沒有顯式 queue 的 process 仍為無限。capacity 0（無緩衝的直接交接）延後：它需要「進入即授予」，改變「只在 dispatch 授予」的規則。
- **路由先解析**：decision／failure／rework 這段在出發事件中照現行解析（同時刻抽 routing、記 failure／rework observation、計 visit）；只有最後一步「進入 buffer」檢查容量。被擋的 entity 目標固定，解除時不重抽。
- **SERVICE_COMPLETE**：先解析目標。解析只抽 decision 的 routing 並計算 visit，不排程任何事件、不記 observation；之後的提交照現行的步驟與順序。不會被擋時與現行完全相同（釋放 bundle 的 ObsResource → ServiceEnd → needDispatch → 路由 observation → 進入 buffer／排 delay（delay 的抽樣仍在此時）／sink），所以 seq 也相同；會被擋時記 ServiceEnd、路由 observation、Blocked，bundle 維持 in use，**不**呼叫 needDispatch（沒有釋放、也沒有 enqueue，不取 seq）。ARRIVAL 被擋時記 Arrival 後記 Blocked（release backlog）；DELAY_COMPLETE 被擋時記 DelayEnd 後記 Blocked。
- **路由中途觸發 limit（review L3）**：解析時若超過 `max_entity_visits`（或其他 limit），先依現行順序送出已經會記的 observation（釋放的 ObsResource、ServiceEnd，以及失敗 node 之前的路由 observation），再回 `ErrEntityVisits`，與現行 kernel 在錯誤前送出的 observation 完全相同（differential 依賴這個順序）。
- **解除**：只有授予會讓 entity 離開 buffer，所以解除只發生在 dispatch 中。每次授予使 P 的 buffer 少一件後，若 P 有 blocked entity，依 blocked FIFO（變成 blocked 的處理順序）移入最前面的一件：原子釋放其出發 process 的整個 bundle（ObsResource；這些 resource 的 FIFO 最前面的 held candidate 取消 held，比照 SERVICE_COMPLETE）、記 Unblocked、以新的 request seq 排到 P 的 FIFO 最後（ObsQueueEnter，At＝queue node）。Dispatch 持續取最小 seq 的 candidate，直到沒有 candidate 放得下為止（fixpoint）；連鎖（下游授予 → 解除 → 上游釋放 → 上游授予 → 再解除）都在同一個 dispatch 內完成。在 dispatch 中的釋放與 enqueue **不**再排 DISPATCH 事件（現行 `enqueue` 會呼叫 needDispatch；dispatch 進行中視為已有待處理的 dispatch，不取 seq）。釋放的 unit 先滿足等待中的故障（D-051），其餘才可授予。
- **不變量**：P 有 blocked entity ⇒ P 的占用數＝capacity（事件邊界上）；新到的 entity 遇到已滿的 buffer 一律排在既有 blocked entity 之後。
- **與 FIFO 和 bundle**：blocked entity 不在 P 的 resource FIFO 中占位置；它持有的 unit 對任何 request（包括出發 process 自己的）都不可用；P 的授予仍是 all-or-nothing，D-038 (m) 的「FIFO 擋住」照舊。
- **Source**：投入時刻不因阻塞改變（arrival stream 照常），entity 在 source 等待；lead time 由 arrival 起算，所以含 backlog 時間；backlog 另有自己的統計。
- **統計**（只在模型有有限 buffer 時出現；T-046 phase 2 定名為每個 replication 的 `blocking[<node>]`（`blocks_started`、`blocks_completed`、`completed_block_ticks`、`blocked_entity_ticks`）、`resource_states[<resource>].blocked_ticks` 與 `buffers[<queue>].full_ticks`，SIMULATION_SPEC §5）：每個可能被擋的 node 的阻塞次數、完整阻塞時間的平均（W ≤ 開始、結束 ≤ H）、被擋 entity 數的時間平均；每個 resource 被 blocked entity 持有的 unit 時間比例 ∫K(t)dt/(C·T)；每個有限 buffer 滿的時間比例。既有 `utilization_mean` 定義不變（∫in_use/(C·T)，含被擋住的 unit）。

**決定性與 digest。** 新增 Blocked、Unblocked observation，只出現在有有限 buffer 的模型；「先解析、後釋放」只改變內部步驟，不會被擋時 observation 順序與現行相同。

**Golden vectors。**
- G-B1 兩站串聯（constant）：interarrival 1 min、A 服務 1 min（1 unit，無限 buffer）、B 前 queue capacity 1、B 服務 3 min（1 unit）。B 的完成時刻 5、8、11、…（2+3k）；A 的阻塞區間 [4,5]、[6,8]、[9,11]、…；每次解除與 A 的下一次授予在同一 tick。
- G-B2 同一模型、B 前 buffer 無限：A 從不被擋，B 的 queue 線性增長（與 G-B1 對照）。
- G-B3 兩個上游 process 在同一 tick 被同一 buffer 擋住：依事件處理順序解除。
- G-B4 source backlog：interarrival 1、queue capacity 1、服務 2：backlog 每 2 分鐘多一件；第 k 件於 1+2k 完成，lead time 1+k。
- G-B5 decision 後被擋：routing stream 的 draw 數等於已解析的 decision 數，解除時不再抽。
- G-B6 中性：capacity ≥ max_active_entities 與無限 buffer 逐筆相同（D-047）。
- G-B7 被擋 entity 的 bundle 含與其他 process 共用的 resource（不構成 D-050 的 cycle）：被擋期間該 unit 對他人不可用。

**SimPy。** Machine 為 `simpy.Resource(capacity)`；有限 buffer 為「空位」的 `simpy.Resource(capacity=B)`：entity 取得 A、timeout(service)、在**仍持有 A** 的情況下 request B 的空位（阻塞）、取得空位後釋放 A、request B 的 machine、取得後釋放空位。Source 照時刻產生 entity，各自先 request 空位。確定性且沒有同時刻 tie 的案例逐值比對，隨機案例以預先固定的容差比對 throughput、阻塞比例、buffer 滿的比例。

### D-050 死結與停滯（deadlock／starvation）的防止與偵測

**問題。** 有限 buffer、共用 resource、班表、故障、批次與換線合在一起時，request 可能永遠等下去：(1) 循環阻塞（重測迴圈、共用 resource）；(2) bundle 的 resource 永遠湊不齊。Review 指出兩個反例：班表 R1 [0,480)、R2 [480,1440)（period 1440）而 process X 需要 R1＋R2，X 永不授予，`q_X` 填滿後上游永久被擋，且 X 被 FIFO 擋住的 request 讓其他使用 R1／R2 的 request 也永遠等（D-038 (m)）；capacity 2、TTF＝TTR＝10（constant）而 X 需要 2 unit，兩個故障時鐘最後相差 10，每次一個 unit 修好，另一個的故障都在 dispatch 之前處理，永遠沒有 2 個空 unit。原提案的 runtime backstop 要求「heap 中沒有能釋放 unit 的事件」，但週期性的 CAPACITY_CHANGE 與 REPAIR 一直都在，所以有班表或故障時它永不觸發。要怎麼靜態防止、runtime 怎麼以 typed error 偵測，保證到哪裡？

**選項。**
- A：靜態規則 R1–R4 全為 error，加上 runtime 的 B1（循環等待，精確）與 B2（湊不齊，確定的期限）。後果：所有 valid 的模型都在可證明不會永久停滯的範圍內；B2 只是實作或規則缺口的 backstop；runtime 不需要狀態記錄。代價是 R4 會拒絕「會故障的機台沒有班表、作業員有班表」這類常見寫法，使用者要照錯誤訊息給機台作業員的班表（機台本來就只在作業員在時運轉）；同一台會故障的機台被兩個班別不重疊的作業員使用時無法滿足 R4，要拆成兩個 resource。
- A′：R1–R3 為 error、R4 為 warning，B2 對有 warning 的 bundle 另以狀態簽章偵測固定相位的永久鎖定（上一版的 B2b）。後果：接受更多模型；但有 warning 的模型只在 TTF、TTR 固定時能偵測，其他鎖定不偵測；簽章必須從所有例外日結束之後才記錄與比對（例外日不是週期的，review N6 (a)）；簽章的記憶體要以每個 replication 的總量設上限、計入 run 的 limits，存 digest 時在回報前要以完整狀態或重新模擬確認（review N7）；實作與驗證成本明顯較高；D-051 的 `clock` 欄位隨之恢復，R4 的 warning 也要加回「有 calendar 卻用日曆時鐘」的條件，否則沒有 warning 的 bundle 仍可能在夥伴下班時故障，不誤報的論證就不成立（review round 4 L2）。
- B：只做 runtime 偵測（一般 wait-for 分析加週期性判斷）。後果：在有隨機故障的週期性模型中，「之後會不會恢復」沒有一般的判定法；錯誤在跑到一半、依 seed 才出現。
- C：不偵測，靠 horizon 與 limits。後果：違反 TEST-034；不採用。

**建議：A（R4 為 error）。** 理由：大部分錯誤在執行前就以 JSON Pointer 指出、與 seed 無關，而且附有具體的修正方式；保證涵蓋每個 valid 的模型；runtime 只有兩個不誤報的 backstop，沒有狀態記錄的成本。若 owner 希望接受更多寫法，改採 A′，代價如上。這也回答了「warning 或 error」的問題。

**靜態規則（validator；R1 在 graph rules 階段，R2–R4 在 distribution／domain semantics 之後；全為 error）。**
- **R1 `BLOCKING_CYCLE`**：holder＝至少需要一個 resource 的 process（含批次 process）；source、delay、沒有 resource 的 process 被擋時不持有任何東西。A 是 holder 且 A 的出路（沿 p>0 flow，經 decision／failure／rework，可再經一個 queue node）可能到達 P 的有限 buffer 時，有阻塞邊 A→P；需要同一個 resource 的 process 合為一個共用分量（union-find，依 ID 順序）；把阻塞邊投影到分量上，有 cycle（含分量(A)＝分量(P)）就拒絕。Path 為 cycle 上每個有限 queue 的 `/nodes/<i>/capacity`；message 依 ID 順序列出 cycle 上的 node 與造成合併的共用 resource，並說明修正方式：讓 cycle 上至少一個 buffer 為無限，或讓被擋的 process 不與擋住它的 process 共用 resource。
- **R2 `CALENDAR_INVALID`（bundle 的可用時間）**：模型中所有 calendar 的 period 的最小公倍數 L ≤ 365 天；每個 process（含批次）在 [0, L) 中至少有一個 tick，使 bundle 的每個 resource r 都有 S_r(t) ≥ 其 units（沒有班表時 S_r≡C_r；每班容量見 D-052）。只看週期部分，不計例外日。Path 為 `/nodes/<i>/requires`。第一個反例被拒絕。
- **R3 `RESOURCE_REQUIREMENT_INVALID`（故障 resource 的 bundle 形狀）**：對有 breakdowns 的 resource 的 request，units 必須為 1；一個 bundle 至多一個有 breakdowns 的 resource。第二個反例與「兩個反相故障的 resource 在同一 bundle」被拒絕。代價：同時需要兩台會故障的設備的步驟，要建成一個代表整組的 resource（整組一起故障）。
- **R4 `STALL_RISK`（故障 resource 與夥伴的上班時間）**：bundle 中有故障 resource R 時，在 R 上班（S_R ≥ 1）的每個 tick，bundle 的其他 resource Q 都要 S_Q ≥ 其 units。只看週期部分，不計例外日（例外日由下面的 X_end 處理）。Path 為 `/nodes/<i>/requires`；message 指出 R 與不夠的 Q，並建議「給 R 一個包含在 Q 上班時間內的 calendar（例如作業員的 calendar）」。D-051 的故障時鐘一律只在 S > 0 時前進，所以沒有另外的時鐘條件。

**記號。** 對一個 bundle 定義 fit(t)：bundle 的每個 resource r 都有 S_r(t) ≥ units_r（只看班表，含每班容量與例外日，不看停機與使用中）。next_fit(t)＝最小的 t′ ≥ t 使 fit(t′)。X_end(q)＝q 的 bundle 中所有 calendar 的最後一段例外的結束 tick（沒有例外時為 0；例外都在 365 天內）。班表是確定的，next_fit 可由各 calendar 的變化點（週期部分與有限個例外）合併後以整數 tick 算出；R2 保證最後一個例外之後的每個長度 L 的窗中都有 fit 的 tick，所以 next_fit 一定存在，計算也有界。

**保證（前提：service、delay、修復與換線時間有限；TTF 至少 1 tick，D-051）。** 每個 valid 的模型（通過 R1–R4），在不限 horizon 時，每個等待中的 request 終會被授予、每個 blocked entity 終會移動，B1、B2 永不觸發。草證：
1. 需要同一個 resource 的 process 屬同一共用分量，所以一個 request 在任何 FIFO 中前面的 request 都屬同一分量。因此每個有等待 request 的分量中，seq 最小的那個 request 在它每個 resource 的 FIFO 都排第一，一定是 candidate；依 D-038 (m)，共用其 resource 的較晚 request 不能先授予，所以被釋放或修好的 unit 都留給它。
2. 一個 candidate 的 bundle 中的 unit 只會是：服務中（有限時間內釋放，或依 BAS 變成被擋）、被 blocked entity 持有、停機（有限修復）、下班。
3. 被 blocked entity（在 A）持有的 unit 屬於 A 的分量，A 在等 P 的 buffer，這是分量之間的一條阻塞邊。R1 使分量圖無 cycle，所以可以依分量圖的反拓樸順序（從沒有向外阻塞邊的分量開始）歸納。
4. 歸納的基本步：一個分量中沒有 unit 被 blocked entity 長期持有時（沒有向外阻塞邊，或下游分量都已滿足歸納假設），其 seq 最小的 candidate q 的 bundle 中，服務中的 unit 在有限時間內釋放，之後只缺停機或下班的容量。沒有故障 resource 時，q 在 next_fit(此時) 被授予（該 tick 的 CAPACITY_CHANGE 會觸發 dispatch）。有故障 resource R 時（R3：唯一一個，units＝1），只看 X_end(q) 之後：那之後班表只剩週期部分，R4 使「R 上班」蘊含 fit；每個停機的 unit 在 TTR 內修好；R 的時鐘只在上班時前進，所以修好的 unit 在 R 下一次上班的 tick 仍可用（下班時不會故障；剛上班的那個 tick 也不會，因為上班時間還沒累積），那個 tick 也是 fit 的 tick，q 被授予。X_end 之前，夥伴可能在例外日中停工而 R 仍上班、unit 可能故障（review N6 (b)），只會延後，不會永久。
5. 歸納步：下游分量的每個 request 終被授予 → 其 buffer 一再出現空位 → 本分量中的 blocked entity 依 FIFO 終會移入並釋放其 bundle → 回到第 4 步，本分量 seq 最小的 candidate 終被授予；它離開後下一個最小的又是 candidate，所以本分量的每個 request 終被授予。
6. 其他功能：批次 request 只在就緒後存在，就緒前的湊批等待不是停滯（沒有 request）；換線只延長 service，unit 的選擇不影響「放不放得下」（計數）；投入排程只增加 arrival；finish-first 的 overtime 與等待中的故障都屬「使用中、有限時間內釋放」。
7. 期限：q 於 t_q 進入「bundle 中沒有任何 unit 在使用中」的狀態時，至遲在 deadline(q)＝next_fit(max(t_q, X_end(q))＋TTRmax(q)) 被授予，其中 TTRmax(q) 為 q 的故障 resource 的 TTR 分布可能的最大值（沒有故障 resource 時為 0；exponential、lognormal、Weibull 無上界，此時沒有期限）。理由：max(t_q, X_end) 時已停機的 unit 在其後 TTRmax 內都已修好，且依第 4 步一直可用到下一個 fit 的 tick；那時若沒有停機的 unit，q 在 next_fit(max(t_q, X_end)) 前就已授予。

**不保證（明列）。**
- TTR 分布無上界時，等待終會結束（機率為 1）但沒有期限，B2 不檢查。
- R1 是保守的：迴圈中有限 buffer 足以容納所有可能在迴圈內的 entity 時，模型其實不會死結，仍被拒絕；R4 也會拒絕某些其實不會鎖定的寫法（例如故障分布不固定時）。
- 停滯只指永久等待；在 horizon 時尚未結束的等待是截尾，不是錯誤。

**Runtime backstop。** 兩者只讀計數與確定的班表，不影響事件順序或 RNG，沒有狀態記錄；觸發即 run failed、沒有部分結果（SIMULATION_SPEC §8）。每次 dispatch 結束時檢查；修好、上班與 batch 就緒都會觸發 dispatch。依上面的保證，兩者對 valid 的模型都不觸發，只在規則或實作有缺口時發生，也都不會讓一個之後會前進的 run 失敗。
- **B1 `ErrDeadlock`（循環等待，精確）**：有 blocked entity 或 held candidate；heap 中沒有 SERVICE_COMPLETE、DELAY_COMPLETE、BATCH_TIMEOUT，也沒有這些事件因晚於 horizon 被丟棄；且每個 held candidate 都是 release-bound（bundle 中某個 r 有 inUse_r＋units > C_r，修好或上班都不能讓它放得下）→ 回 `ErrDeadlock`（job failure code 暫定 `SIMULATION_DEADLOCK`），帶最早的 blocked entity（沒有時為最早的 candidate）的 ID、node 與 tick。沒有誤報：沒有任何進行中的工作能釋放 unit，容量事件也幫不了 release-bound 的 candidate。
- **B2 `ErrStalled`（湊不齊，確定的期限）**：held candidate q 的 bundle 中每個 resource 都沒有 unit 在使用中。這個狀態只會因 q 被授予而結束（其他 request 不能先取這些 unit），所以從進入的 tick t_q 起計；dispatch 結束時 q 仍在此狀態且 now ≥ deadline(q) → `ErrStalled`（暫定 `SIMULATION_STALLED`），帶 q 的 entity ID、process、t_q 與 deadline。TTRmax 無上界時不檢查。

**決定性與 digest。** Validator 輸出依 D-023 排序；runtime 檢查不改變事件或抽樣；失敗的 run 沒有 result digest。

**Golden vectors。**
- Invalid：(1) process 經 decision 回到自己的有限 queue；(2) 兩個有限 buffer 構成迴圈；(3) A 與 P 共用 resource 且 A 可能被 P 的有限 buffer 擋住（沒有 flow cycle 也拒絕）；(4) 第一個反例 → `CALENDAR_INVALID` 於 X 的 `/requires`；(5) period 300 天與 7 天（最小公倍數 > 365 天）→ `CALENDAR_INVALID`；(6) 第二個反例（故障 resource 上 units＝2）→ `RESOURCE_REQUIREMENT_INVALID`；(7) 同一 bundle 兩個故障 resource → 同上；(8) 機台（無 calendar、有故障）＋作業員（有 calendar）在同一 bundle → `STALL_RISK`，message 建議給機台作業員的 calendar；改正後 valid。Valid：template。
- G-D1（test-only，繞過 R4，確認 B2 會觸發）：機台沒有 calendar、TTF 960／TTR 480 為 constant，作業員 shift 為每天 [960, 1440)，只有一個 arrival 在 t=10 分鐘。deadline＝next_fit(10＋480)＝960；960 時機台在 dispatch 之前故障，q 未授予 → 在 960 分鐘回 `ErrStalled`。
- G-D2（B1）：test-only 的 Program 繞過 R1，兩站各 1 unit、capacity 1 的 queue 互相回流，另有一個無關 resource 帶班表（使 heap 永不為空），在最後一個服務中的 entity 完成並被擋的那個 tick 回 `ErrDeadlock`（tick 於 T-047 手算後固定）。T-047 的版本（班表在 T-049 才有）以一個直接進 sink 的無關 source 使 heap 永不為空：src 每 1 分鐘 → q1（1）→ s1（2 分鐘）→ q2（1）→ s2（3 分鐘）→ q1；7 分鐘時 e3 在 s1 完成並被擋，沒有服務中的 entity，回 `ErrDeadlock`，回報最早仍被擋的 r0/src/5（src，自 5 分鐘）（`testdata/simulation/blocking-goldens.json`）。
- G-D3（review N1 (a)，不得觸發）：R capacity 2、`shift_capacity` 為 day [360, 840)＝2、evening [840, 1320)＝1、其餘 0（period 1440）；X 需要 R×2；一件在 900 到達，此時沒有 unit 在使用中。唯一待處理的班表事件是 1320 的 1→0；next_fit(900)＝次日 360 → 不回報，X 在次日 360 被授予。
- G-D4（review N1 (b)，不得觸發）：R1 shift [0, 600)、R2 shift [540, 1440)、period 1440，X 需要 R1＋R2；R1 有 100 段 open＝false 的例外 [540＋1440k, 600＋1440k)（k＝0..99）。X_end＝99·1440＋600，deadline＝next_fit(X_end)＝第 100 天的 540 → 不回報，X 在該 tick 被授予。
- G-D5（review N6 (b)，不得觸發）：M 與 O 的 shift 都是 [0, 480)、period 1440；O 有 open＝false 的例外 [1440, 1920)；M 的 TTF 960、TTR 1500（constant）；X 需要 M＋O；一件在 1450 到達。M 在 1920 故障、停機到 3420；X_end＝1920，deadline＝next_fit(1920＋1500)＝next_fit(3420)＝4320；3420 的 REPAIR dispatch 不回報，X 在第 3 天的 4320 被授予。
- Property：隨機產生 valid 的模型（含 constant 的 TTF／TTR、units > 1 的 request 搭配 `shift_capacity`、多段例外日，且常讓例外日只出現在 bundle 的其中一個 calendar），長 horizon 下 B1、B2 不觸發；Python 獨立實作的 R1–R4、next_fit 與 X_end 與 Go 在隨機輸入上相同。

**SimPy。** SimPy 不偵測停滯（事件用完即停，週期性模型則一直跑到 horizon）。Reference 做三件事：Python 獨立實作 R1–R4、next_fit 與 X_end 並與 Go 比對；以 SimPy 跑兩個反例與 G-D1，確認第一次之後再也沒有授予（示意，不作驗收）；對隨機的 valid 模型，確認 SimPy 與 Go 每個 L 都有授予。

### D-051 Resource 故障（MTBF／MTTR）

**問題。** (1) 故障時鐘依日曆時間、排班時間還是忙碌時間；(2) 故障時服務中的 entity：preempt-resume、preempt-restart 或 finish-first；(3) 多 unit 的 resource；(4) RNG stream 命名，使既有 stream 不變；(5) availability 的定義。

**選項（服務中 entity 的處理）。**
- A：每個 unit 一組獨立的 alternating renewal；故障時若有不在使用中的 unit（含下班的）立即停機；全部在使用中時 finish-first：等下一個被釋放的 unit 停機（多個等待中的故障依故障時刻 FIFO），修復時間從停機起算；服務中的工件照常完成。後果：kernel 的匿名 unit 就夠，不需 unit identity、不需取消已排程的事件；每次故障的停機時間＝TTR，產能損失與 preempt-resume 相同，只有那一件工件完成得較早；長時間 service（燒機、機櫃測試）會低估中斷的影響。
- B：preempt-resume（中斷、修好後做完剩餘時間）。後果：需要 unit identity（哪個工件被中斷）、取消並重排 SERVICE_COMPLETE（heap 要 lazy deletion）、規定 bundle 其他 resource 在中斷期間保留或釋放；與阻塞、班表、批次的交互多；屬 PRD P1 的 preemption。
- C：preempt-restart（中斷後重做或報廢）。後果：同 B，另需重抽 service（RNG 消耗改變）或報廢路由；屬 PRD P1 的 preemption。

**選項（故障時鐘，T-046 review M1）。**
- (i) 日曆時間：從修好起算，不論忙閒、上下班。後果：最簡單；但機台是在運轉中故障的，停機紀錄中「修好到下次故障」的日曆間隔包含夜間與週末，以 `two_shift_weekdays`（∫S／T＝80／168）而言，模型在上班時的故障率只有資料的約 48%，而下班時的故障拿走閒置 unit、不造成任何損失，系統性地低估上班時的停機。
- (ii) 排班時間（scheduled）：時鐘只在該 resource 上班（S(t) > 0）時前進。以 resource 為單位，不需 unit identity；因為班表是確定的，抽到 TTF 時就能直接算出故障時刻：t_fail＝min{t : Θ(t) ≥ Θ(t_up)＋TTF}，Θ(t) 為 [0, t) 中 S > 0 的 tick 數（整數運算，不需暫停或取消事件）。後果：上班時的故障率與「以上班時間估計」的 MTBF 一致；每班不同人數時（D-052）只要 S > 0 就前進，是近似。
- (iii) 忙碌時間：只在 unit 服務中前進。後果：最貼近磨耗型故障，但需要所有 unit 的 identity 與忙碌累計；未排入 P0（見 Consequences）。

**建議：A，時鐘一律用 (ii) scheduled（沒有 calendar 的 resource 即日曆時間），不提供選擇欄位。** 理由：A 實作最小、與匿名 unit 相容；(ii) 修正了 (i) 在班表下的系統性低估，又不需 unit identity；D-050 建議的 R4 為 error，有 calendar 卻用 (i) 會讓故障發生在夥伴下班時，正是 R4 要排除的情形，所以 (i) 不需要成為選項。若 owner 改採 D-050 的 A′（R4 為 warning），再加回 optional 的 `clock: "scheduled" | "calendar"`。TTF 須以同一時鐘估計：scheduled 時為「修好到下次故障之間的上班時間」（[ELECTRONICS_LINE §6](../domains/ELECTRONICS_LINE.md)）。

**語義（A＋(ii)）。**
- IR：resource 的 `breakdowns: {time_to_failure: {distribution}, time_to_repair: {distribution}}`；TTF 依該 resource 的上班時間（S > 0）累積，沒有 `calendar_id` 時即日曆時間。TTF 的最小可能 tick 必須 > 0（同 D-033 對 interarrival 的規則，避免同一 tick 的事件風暴），否則 `DISTRIBUTION_INVALID`；TTR ≥ 0，以日曆時間計（修復可跨下班）；empirical 經 dataset binding（D-006）。D-050 R3：對有 breakdowns 的 resource 的 request units＝1，一個 bundle 至多一個有 breakdowns 的 resource。
- 每個 unit u（0..capacity−1）在 t=0 為正常，第一個 TTF 在 t=0 抽；故障 → 停機時抽 TTR、排 REPAIR；REPAIR 時 down 減一、抽下一個 TTF、依時鐘算出故障時刻並排 FAILURE。scheduled 時鐘下故障只會發生在上班時間；目標恰在一段上班的結束時達到時，故障時刻即該段的結束 tick。抽到的 TTF 飽和為 365 天＋1 tick（D-038 (b)），或目標上班時間在 horizon 之前達不到時，FAILURE 事件不排程（與 horizon 之後的事件相同，不取 seq）；t_fail 的搜尋沿班表的變化點前進，到 horizon 即停，不會無界地搜尋（review N4）。
- 授予條件改為 `inUse + units ≤ min(S(t), C − down)`（S 為班表容量，沒有班表時為 C，D-052）。等待中的故障在每次釋放 unit 時優先滿足（被釋放的 unit 直接停機），其餘才變成可用；REPAIR 與上班一樣會取消該 resource 最前面 candidate 的 held 並排 dispatch。
- **Stream**：(resource ID, `ttf/<u>`) 與 (resource ID, `ttr/<u>`)，u 為十進位 unit index；validator 已要求 node 與 resource ID 不重複（DUPLICATE_ID），purpose 也是新的字串，既有 stream 不受影響。Capacity 加一只新增 unit C 的 stream，unit 0..C−1 的抽樣序列不變（stream 層級的 common random numbers）。時鐘不影響抽樣，只影響抽到的 TTF 如何換成時刻。
- **統計**（只對有 breakdowns 的 resource；Result 中的位置見 D-058）：視窗內開始停機的次數、停機比例 ∫D/(C·T)、完整修復時間的平均、availability ＝ ∫min(S, C−D)dt / ∫S dt（∫S=0 時 unavailable）。等待中（尚未停機）的時間不算停機。Equipment breakdown 與 generic `failure` node（測試不良）是不同的東西，`failure_count` 照舊只算 failure node（D-010）。

**決定性與 digest。** 新事件（FAILURE、REPAIR，priority 15）與 ResourceDown／ResourceUp observation 只出現在有 breakdowns 的模型；t=0 不記 ResourceUp（只記實際的轉換，D-047）。Θ 的計算全為整數 tick。

**Golden vectors。**
- G-F1 閒置時故障：1 unit、interarrival 2、服務 1、TTF 10、TTR 3（constant，無 calendar）：t=10 的 arrival 先進 buffer，故障（15）使 unit 停機到 13，13 時授予，下一次故障在 23。
- G-F2 服務中故障（finish-first）：interarrival 3、服務 2、TTF 10、TTR 3：10 時 unit 忙（9–11），故障等待；11 釋放即停機到 14；12 到的 entity 在 14 開始；下一次故障在 24，此時 unit 閒置，24 到的 entity 等到 27。
- G-F3 2 unit、一個等待中的故障、兩次釋放：第一個被釋放的 unit 停機、第二個變成可用。
- G-F4 capacity +1 的 scenario：unit 0..C−1 的 TTF／TTR 抽樣序列與 baseline 相同（比對 stream 輸出，不比對時刻）。
- G-F5 中性：constant TTF 晚於 horizon 與沒有 breakdowns 逐筆相同。
- G-F6 時鐘：calendar period 10 分鐘、shift [0,6)、TTF 8、TTR 1（constant）。scheduled：上班時間在 [0,6) 累積 6、[10,12) 再 2，於 12 故障，13 修好，再累積 8（[13,16) 的 3 與 [20,25) 的 5）於 25 故障。對照不採用的日曆時鐘：於 8 故障（下班，拿走閒置 unit，沒有損失），9 修好，17 再故障。
- `engine-vectors.json` 加各 unit 前 k 個 TTF／TTR 的 Python 參考值。

**SimPy。** 用 `simpy.PriorityResource`：每個 unit 一個故障 process，`timeout(TTF)` 後以比工件高的 priority request 一個 unit（non-preemptive），取得後 hold TTR 再釋放。Non-preemptive priority 的語義正是「取閒置 unit，否則取下一個被釋放的 unit」；同 priority 依 request 時刻排序，對應等待中故障的 FIFO。scheduled 時鐘：故障 process 的 timeout 只在上班期間累積（以班表 process 的事件暫停與恢復），以隨機容差比對。

### D-052 班表（shift calendar）

**問題。** (1) 班表在 IR 中以 tick 表示且不用時區；(2) 交班時服務中的 entity；(3) utilization 的分母；(4) 每班不同人數與假日等例外是否納入（H-017 的方向是盡量保留功能，是否納入由 owner 決定）。

**選項（表示）。**
- A：根層 `calendars[]`（`id`、`period`、`shifts`：period 內的 [start, end) 上班區間，可帶 `label`，以 model 的 time_unit 表示），resource 以 `calendar_id` 參照；上班時 S(t)＝capacity，下班時 0。後果：多站共用一份班表，有共同的「線的排班時數」可作 UPH 分母。
- A＋：A 再加兩個 optional 欄位：resource 的 `shift_capacity`（label → 0..capacity，表達白班 2 人、夜班 1 人）與 calendar 的 `exceptions`（[{start, end, open}]，以從 t=0 起的絕對 model 時間表示，不用日期與時區；open＝false 為停工，如假日；open＝true 為加班，S＝capacity）。後果：每班人數與假日都可表達，仍只是 S(t) 的另一種寫法，kernel 不變；validator 與 D-050 的 R2 要多處理兩種情形。
- B：每個 resource 自己的週期性容量表（[{at, capacity}]）。後果：可表達每班不同人數；每個 resource 重複一份；沒有共同的線班表，UPH 每排班小時要另外指定分母。
- C：絕對日期的班表與時區。後果：需要時區、DST 與日期函式，違反「以 tick 表示、不用 wall clock」（T-049 scope）；不採用（例外日以 A＋ 的絕對 model 時間表示即可）。

**建議：A＋。** 理由：多站共用同一份班表是產線的常態，也給「排班小時」一個明確的分母；agent 依 H-017 的方向建議一併納入：每班人數與假日的成本低（都只改 S(t)）。H-017 不批准任何 decision。

**語義（A＋）。**
- t=0 對應 period 的 offset 0（model 本地時間；沒有時區、DST）。Period ≤ 365 天；shifts 至少一個、依 start 排序、不重疊、0 ≤ start < end ≤ period（跨午夜的夜班拆成兩段）；以 `model.Ticks` 量化，量化後仍須嚴格遞增；至多 100 段。`shift_capacity` 的 label 必須存在於該 calendar、值為 0..capacity 的整數；沒有列出的 label 與沒有 label 的 shift 用 capacity。`exceptions` 依 start 排序、不重疊、0 ≤ start < end ≤ 365 天、至多 100 段；例外內 S＝0（open＝false）或 capacity（open＝true）。違反回 `CALENDAR_INVALID`；未知 `calendar_id` 回 `UNKNOWN_REFERENCE`；D-050 的 R2 另檢查每個 bundle 的可用時間（不計例外）。沒有 `calendar_id` 的 resource 全天可用（D-017：不暗加班表）。
- **事件**：每個 calendar 在它的 S 模式改變的時刻排一個 CAPACITY_CHANGE（priority 15）；同一 calendar 同時只有一個待處理的變化事件。處理時依 resource ID 順序更新所有引用它的 resource，只對 S 真的改變的 resource 記 Capacity observation；容量增加的 resource 取消其最前面 candidate 的 held 並排 dispatch。t=0 的初始 S 由 Program 決定，不記 observation（D-047）。
- **交班選項**：(a) finish-first：服務中與被擋住的 entity 照常完成或等待，超出排班的忙碌時間記為 overtime；(b) preempt-resume：與 D-051 B 相同的機制，屬 PRD P1 的 preemption；(c) 做不完就不開始：要在授予前知道 service 長度並改變抽樣時點，未排入 P0（見 Consequences）。**建議 (a)。**
- **分母選項**：(i) 原 `utilization_mean`（∫inUse/(C·T)）不變，另對有班表的 resource 加 `scheduled_utilization` ＝ ∫min(inUse, S)dt/∫S dt 與 overtime ＝ ∫max(inUse−S, 0)dt；(ii) 有班表的 resource 改用 ∫S 當 `utilization_mean` 的分母：同一欄位對不同 resource 意義不同，舊的使用者會誤讀；(iii) 只有 C·T：單班滿載的站顯示約 33%，工程師會覺得錯。**建議 (i)。**
- 與故障：故障時鐘在 S＝0 時暫停（含停工的例外，D-051）；授予條件 `inUse + units ≤ min(S, C−down)`。

**決定性與 digest。** CAPACITY_CHANGE 不用 RNG；只有引用班表的 resource 會有新事件與 Capacity observation。

**Golden vectors。** G-C1：1 unit、period 10 min、shift [0,6)、interarrival 2、服務 3：服務 2–5、5–8（6 下班，overtime 2 分鐘）；6 到的 entity 在 10 上班時開始（10–13），8 到的在 13 開始；第一個 period 的 scheduled utilization 4/6。G-C2：上班與 arrival 同 tick，於該 tick 授予。G-C3：中性（一個涵蓋整個 period 的 shift）與沒有班表逐筆相同。G-C4：bundle 的兩個 resource 有不同但有交集的班表，只在兩者都上班時授予。G-C5：capacity 2、`day` 班 2、`night` 班 1：夜班同時至多 1 件在服務；換班時已在服務的第 2 件做完（overtime）。G-C6：一段 open＝false 的例外使該段沒有授予，scheduled 時鐘的故障時刻順延同樣長度。G-C7：兩個 resource 引用同一 calendar：一個 CAPACITY_CHANGE，Capacity observation 依 resource ID 順序。

**SimPy。** 每個 resource 一個班表 process：容量從 c1 降到 c2 時，以最高 priority（non-preemptive）request c1−c2 個 unit 並持有到容量回升；回升時釋放已取得的 unit，並取消尚未取得的 request。Finish-first 與本提案相同；例外日與每班容量都是同一個 process 的不同時刻表。與故障同時使用時只做隨機容差比對。

### D-053 隨時間變化的投入（release schedule）

**問題。** 投入率依時段變化要怎麼表示？與 common random numbers 的關係？生產計畫式的投入表是否也納入（H-017 的方向，由 owner 決定）？

**選項。**
- A：週期性分段常數 rate，以累積 rate 反函數產生：source 的 `arrival.schedule`（`period`、`segments: [{start, rate}]`，rate 為每個 model time_unit 的期望件數，`process`：`poisson` 或 `deterministic`），與 `arrival.distribution` 二擇一。令 Λ(t)＝∫₀ᵗ λ(s)ds、a₀＝0，第 k 件 aₖ＝aₖ₋₁＋Xₖ，Xₖ＝1（deterministic）或 −ln(1−U)（poisson，從該 source 的 arrival stream 抽一次），時刻 τₖ＝min{t : Λ(t) ≥ aₖ}，量化為 tick 後至少比上一件晚 1 tick。後果：每件恰一次 draw，不同 scenario 的第 k 件用同一個 draw（配對保留）；poisson 是精確的非齊次 Poisson process，deterministic 是精確的 takt；不支援 triangular／empirical interarrival。
- B：明確的投入表（`process: "table"`，`releases: [{at, quantity}]`，period 內週期重複）。後果：最貼近生產計畫（例如每班開始投入一批）、不用 RNG；長 horizon 的 takt 投入要很多列，所以作為 A 之外的第三種 process，而不是取代 A。
- C：各時段各自的 interarrival 分布，跨段時丟棄並在下一段起點重抽。後果：支援任意分布；跨段規則對非指數分布有扭曲；draw 的消耗取決於跨段次數，scenario 之間的配對被打亂；不採用。

**建議：A＋B（三種 process：poisson、deterministic、table）。** 理由：規劃者通常以「每小時投入多少」描述投入，A 每件恰一次 draw、配對保留；agent 依 H-017 的方向建議一併納入 B：B 的成本低（不用 RNG、validation 簡單）且能表達整批投入，一併納入。

**語義。**
- Validation（`RELEASE_SCHEDULE_INVALID`）：period ∈ (0, 365 天]。Rate 型：第一段 start＝0、start 嚴格遞增且 < period；rate 有限且 ≥ 0、至少一段 > 0、每段 rate ≤ 每 tick 1 件；至多 1000 段。Table 型：`at` 在 [0, period) 內嚴格遞增、`quantity` 為 1..10000 的整數、至多 1000 列。`max_entities` 照舊適用。
- **Rate 型的 numerical profile（review L4）**：P＝Λ(period) 依段順序以 binary64 累加（每個乘積明確捨入，同 D-038 (g)），同時記下每段的起點累積 cumStartⱼ 與終點累積 cumEndⱼ。k＝⌈a/P⌉−1、r＝a−k·P；binary64 下 r 可能落在 (0, P] 之外（例：P＝0.3、a＝502.5），所以修正一次：r ≤ 0 時 k 減 1、r 加 P；r > P 時 k 加 1、r 減 P。取第一個 rateⱼ > 0 且 cumEndⱼ ≥ r 的段 j，τ＝k·period＋startⱼ＋(r−cumStartⱼ)/rateⱼ，再 clamp 到 [k·period＋startⱼ, k·period＋endⱼ]。τ 以 sampler 的量化規則換成 tick（一次乘法、round half up；超過 365 天時飽和為 365 天＋1 tick，事件不處理，不用會報錯的 `model.Ticks`），再取 max(該 tick, 上一件＋1)。ln 用固定的 `engine.ln`。累加器 a 保留未量化的值，不從量化後的 tick 回推，避免漂移。第一件在 Λ⁻¹(X₁)，與現行「t=0 起抽第一個 interarrival」一致。
- **Table 型**：每一列一個 ARRIVAL 事件；處理時先排下一列的 ARRIVAL，再依序建立 quantity 個 entity，每建立一個就照現行路由（entity 編號連續）；達到 `max_entities` 即停止。同 tick 的多件投入是有限的（quantity 上限），不構成 D-033 所禁止的零時間循環。

**決定性與 digest。** 只有用 schedule 的 source 改變；其他 source 的 stream 與事件不變。Table 型不用 RNG。

**Golden vectors。** G-R1 deterministic：period 60 min、[0,30) rate 1/min、[30,60) rate 0：arrival 在 1..30、61..90、121..150 分鐘（a＝30 落在 30 而不是 60，固定 ⌈⌉ 的邊界）。G-R2 poisson：固定 seed 的前 N 個 arrival tick 由 Python reference 逐 bit 產生。G-R3 一段 rate 加倍的 scenario：第 k 個 draw 與 baseline 相同。G-R4 中性：上述可精確表示的單段 rate 與同值的 constant interarrival 同時刻。G-R5 高 rate：tick 嚴格遞增。G-R6 修正步驟：P＝0.3、a＝502.5 等落在邊界外的 r 被修正，且 τ 與 Python reference 逐 bit 相同。G-R7 table：period 1440、[{at 360, quantity 50}, {at 840, quantity 50}]：每天 360 與 840 分鐘各 50 件、entity 編號連續、同 tick 依建立順序路由。

**SimPy。** Rate 型以不同的演算法產生非齊次 Poisson process（Lewis–Shedler thinning，Python 自己的 RNG），在大量 replication 上比對各段件數的平均與變異數（預先固定容差）；deterministic 與 table 逐值比對。

### D-054 批次 process（燒機）

**問題。** 批量的上下限與等待上限；批內工件的 ID 與 trace；lead time 與 utilization 的定義。

**選項。**
- A：process 加 `batch: {min_size, max_size, max_wait}`。Buffer 中等待的工件達 max_size，或最老的已等 max_wait 且至少 min_size 件時，「就緒」並建立一個 batch request（一個 bundle、取得一個 FIFO 位置）；授予時依 FIFO 取 min(等待數, max_size) 件；整批一次 service draw、同時開始與結束；完成後依 FIFO 逐件路由；整批都離開（含被擋住的）才釋放 bundle。後果：符合燒機室；batch request 在「就緒」時才取得 FIFO 位置（一般 process 是每件進入時）；每個批次 process 同時至多一個未授予的 batch request。
- B：只有固定批量（湊滿 max_size 才開始，沒有 max_wait）。後果：最簡單；低投入時段（夜班）可能永遠湊不滿，不真實。
- C：合併成一個 entity（assemble／separate）。後果：需要合併與拆分、ID 對照，trace 與 lead time 另定；未排入 P0（拼板／分板也屬此類，見 Consequences）。

**建議：A。** 理由：燒機室的典型規則就是「滿批或等太久就開」；成員保留自己的 ID，lead time、trace 與 3D 回放都不需新的 entity 概念。

**語義（A）。**
- Validation（`BATCH_INVALID`）：1 ≤ min_size ≤ max_size ≤ 10000；min_size < max_size 時必須有 max_wait（≥ 0）；buffer 為有限時 capacity ≥ max_size。
- 就緒檢查：每次有 entity 進入 buffer、每次授予之後、以及 BATCH_TIMEOUT（priority 15）時。最老的未分批 entity 改變時排一個逾時事件（進入時刻＋max_wait；已過期則立即檢查），過時的逾時事件到時忽略。授予後立即重新檢查，所以多個 chamber 可在同一次 dispatch 各開一批。
- 授予：一次 bundle（units 以批計）、從 (process, service) stream 抽一次；成員依 FIFO 各記 ServiceStart 並帶批次 ID `r<rep>/<process>/b<n>`，另記 BatchStart（批量）；observation 順序為 bundle 的 ObsResource → BatchStart → 各成員的 ServiceStart。完成（一個 SERVICE_COMPLETE）：先依 FIFO 解析每個成員的路由（routing draw 依成員順序，進入同一 buffer 時依序占位，後面的成員看到前面成員已占的位置）。沒有成員被擋時：bundle 的 ObsResource（一次）→ needDispatch → 依 FIFO 每個成員的 ServiceEnd、路由 observation、進入 buffer／delay／sink。有成員被擋時（review L3）：不釋放、不呼叫 needDispatch（除非有成員 enqueue）；依 FIFO 每個成員記 ServiceEnd、路由 observation，然後是進入（未被擋的成員照常 enqueue，enqueue 會排 dispatch）或 Blocked。之後在 dispatch 中解除非最後一個的被擋成員時，只記 Unblocked 與 QueueEnter；解除最後一個時，先記 bundle 的 ObsResource（釋放、先滿足等待中的故障），再記 Unblocked 與 QueueEnter。
- Lead time、queue wait 照每件計（queue wait 含等待湊批）；resource 的 utilization 是 chamber 忙碌（不是槽位）；批次統計（只對批次 process）：開始的批數、平均批量、平均批量／max_size。
- 與阻塞：成員被擋時持有 chamber，批次 process 是 D-050 的 holder。與故障、班表：finish-first（整批做完）。

**決定性與 digest。** 一批一個 SERVICE_COMPLETE、一次 service draw；BATCH_TIMEOUT 與批次 observation 只出現在有批次 process 的模型。

**Golden vectors。** G-T1：1 chamber、min＝max＝4、服務 30、interarrival 10：批次在 40、80、120…開始；成員等待 30、20、10、0 分鐘，lead time 60、50、40、30。G-T2：min 2、max 4、max_wait 25、服務 30、interarrival 30：55 時只有 1 件不就緒；60 時第二件到、最老已等 30 ≥ 25 → (e1, e2) 於 60–90；同樣 (e3, e4) 於 120–150。G-T3：2 chamber、9 件等待、max 4：同一次 dispatch 開兩批、留 1 件。G-T4：完成時下游已滿：chamber 持有到最後一個成員解除。G-T5：成員的 routing draw 依 FIFO 順序。

**SimPy。** Buffer 為 `simpy.Store`；一個 batcher process 等到件數 ≥ max_size，或件數 ≥ min_size 且最老的已等 max_wait（以 timeout 事件喚醒），再 request chamber、取出至多 max_size 件、timeout(service)、逐件放到下一站。確定性案例逐值比對，隨機案例以預先固定的容差比對。

### D-055 換線（setup／changeover，P0）

Owner 的方向（H-017，2026-10-19：「時間還很久，盡量保留所有功能」；H-017 把換線列為 P0 提案，不批准語義）：換線規劃在 P0；PRD 已列為 P1 的優先權、preemption 與進階 routing 仍為 P1。以下語義於 2026-10-20 APPROVED（H-018）。

**問題。** 產品怎麼表示；換線何時發生、時間怎麼決定、發生在哪個 unit；與 resource-wise FIFO、bundle、故障、班表、批次的交互？

**選項。**
- A：Resource 層的 sequence-dependent setup：resource 的 optional `setups`（`initial`、`default`、`transitions: [{from, to, distribution}]`）；產品＝entity 的 `entity_type`（多產品以多個 source 表示，每個 source 一種 entity type，現有 IR 已支援）；有 setups 的 resource 記錄每個 unit 的換線狀態；授予時選 unit（優先狀態相符者，否則 index 最小的可用 unit），狀態不同時先抽換線時間再抽 service，unit 在換線＋service 期間都在使用中；派工仍是 FIFO。後果：涵蓋每台機各自的狀態與換線矩陣；resource-wise FIFO 與 all-or-nothing 不變；FIFO 下產品交錯會造成較多換線（實務上以排程分組降低，分組派工屬 PRD P1 的 priority／dispatch）。
- B：Process 層的固定 setup：process 在 entity_type 與上一件不同時加一段 setup。後果：較簡單；capacity > 1 時「上一件」只能以 process 計，不符合每台機各自的狀態；無法表達換線矩陣。
- C：A＋依產品分組的派工（campaign、最小換線排序）。後果：最接近實務排程，但屬 PRD P1 的 priority／dispatch rules，且與 resource-wise FIFO 衝突；P1。

**建議：A。** 理由：每台機各自的換線狀態與換線矩陣是工程師對換線的基本期待；只改「授予後、service 前」這一步與 unit 的選擇，派工規則不變。

**語義（A）。**
- IR：resource 的 optional `setups`：`initial`（entity type，可省略）、`default`（distribution，可省略）、`transitions`（from ≠ to、(from, to) 不重複、皆為既有 entity type）。換線時間取 (目前狀態, 產品) 的 transition，沒有列出時用 default，default 也沒有時為 0；狀態相同不換線。`initial` 省略時，每個 unit 的第一件不換線並把狀態設為該產品。違反回 `SETUP_INVALID`；未知 entity type 回 `UNKNOWN_REFERENCE`。
- P0 限制（違反回 `SETUP_INVALID`）：有 setups 的 resource 在每個 requires 中 units＝1；一個 process 至多需要一個有 setups 的 resource；批次 process 不得需要有 setups 的 resource（批內可能混產品）。
- **Unit identity**：只對有 setups 的 resource，記錄每個 unit 的狀態（使用中、停機、閒置）與換線狀態；容量規則仍是計數（`inUse + units ≤ min(S, C−down)`，D-051、D-052）。授予時在可用 unit 中取狀態與產品相符者 index 最小的，否則取可用 unit 中 index 最小的。D-051 的故障語義不變，只是確定是哪個 unit：立即停機時取閒置 unit 中 index 最小者；等待中的故障由下一個被釋放的 unit 承擔；停機不改變該 unit 的換線狀態。
- **抽樣與事件**：授予時若需換線，先從 (resource ID, `setup/<u>`) 抽換線時間，再照現行從 (process, service) 抽 service；完成事件排在換線＋service 之後，仍是一個 SERVICE_COMPLETE，不新增事件種類。授予時記 Setup observation（resource、unit、時間），再記 ServiceStart（現行）；queue wait 仍算到授予為止。
- **統計**（只對有 setups 的 resource）：視窗內開始的換線次數、換線的 unit 時間比例 ∫(換線中的 unit)dt/(C·T)（跨 W／H 的部分依重疊計）、完整換線時間的平均。ELECTRONICS_LINE §5 的站別狀態把「處理」分成處理與換線。
- **與其他功能**：故障與交班 finish-first（換線與 service 都做完）；被擋時 unit 照 D-049 持有。Scenario 可依 D-057 的規則修改 `setups` 中的分布（replace，risk 至少 medium）；新增或刪除 setups 走 draft→publish。
- **Event log**：推論換線需要每個 serial 的產品（D-059 的 optional `product`）。

**決定性與 digest。** 只影響有 setups 的 resource。單一產品且沒有 `initial` 的模型永不換線、不記 Setup observation，與沒有 setups 時逐筆相同（D-047 的中性設定）；unit 的選擇是固定規則，不依 map 順序。實作於 T-050（其 scope 已含「換線若納入 P0」）。

**Golden vectors。** G-S1：1 unit；`src_a` interarrival 10、`src_b` interarrival 20（constant）；service 2；A→B 與 B→A 換線 3：A1 於 10–12（第一件不換線，狀態 A）；t=20 B1 的 arrival seq 較小（t=0 排程）先進 buffer，於 20–25（換線 3＋service 2），A2 於 25–30；t=30 的 A3 不換線（30–32）；t=40 B2 先於 A4。G-S2：2 unit、狀態分別為 A、B：B 的工件選 unit 1、不換線。G-S3：transitions 優先於 default、default 缺時為 0、`initial` 有與無。G-S4：中性（單一產品）與沒有 setups 逐筆相同。G-S5：capacity +1 時 unit 0..C−1 的 setup stream 序列不變。G-S6：等待中的故障落在換線中的 unit：做完換線與 service 後才停機。

**SimPy。** Unit 選擇是自訂規則（SimPy 的 Store／FilterStore 不直接支援「優先相符，否則最小 index」），以 Python 自寫的 dispatcher 管理 unit 狀態，搭配 SimPy 的時間推進；確定性案例逐值比對，隨機案例比對換線次數、換線比例與 throughput（預先固定容差）。

**仍為 P1。** 依產品分組的派工與換線最小化排序（PRD P1：priority／dispatch）；換線中斷（preemption）。

### D-056 lognormal 與 Weibull（P0）

Owner 的方向同 D-055（H-017 把 lognormal／Weibull 列為 P0 提案）：納入 P0。以下語義於 2026-10-20 APPROVED（H-018）。

**問題。** 修復時間與故障間隔常以 lognormal、Weibull 描述；參數化、抽樣演算法與 numerical profile 怎麼定，才能維持「跨平台逐 bit 相同、每個樣本一次 draw」？

**選項。**
- A：兩者皆以反函數法、每個樣本一個 U：Weibull x＝scale·(−ln(1−U))^(1/shape)，冪次以 exp(ln(y)/shape) 計算；lognormal 以 mean 與 cv 參數化，σ²＝ln(1＋cv²)、μ＝ln(mean)−σ²/2，x＝exp(μ＋σ·Φ⁻¹(U))，Φ⁻¹ 用固定的有理逼近（Wichura AS241）。exp 採 Go `math.exp` 的 pure-Go 演算法、每個乘積明確捨入（同 D-038 (g) 對 ln 的做法）。後果：stream 的消耗與現有 sampler 相同（一個樣本一次 draw）；需要固定的 exp 與 AS241、Python 逐運算移植、vectors、組語檢查與新的 engine_version。
- B：lognormal 以 Box–Muller 產生常態。後果：每個樣本兩個 U（或要快取一個），stream 消耗與其他 sampler 不同，另需固定的 sin／cos；不採用。
- C：只加其中一個。後果：工作約少一半；另一個常見的模型仍缺。

**建議：A。** 理由：兩者是故障間隔與修復時間最常見的參數化分布，owner 要求盡量保留功能；反函數法讓「一個樣本一次 draw」與 golden 的方法都不變；mean 與 cv 是工程師較熟悉的參數。

**語義（A）。**
- IR：distribution 新增 `{"type": "lognormal", "mean", "cv"}` 與 `{"type": "weibull", "shape", "scale"}`，參數以 model time_unit 表示（cv、shape 無單位）；`mean`、`scale` > 0；候選範圍 0 < cv ≤ 10、0.1 ≤ shape ≤ 20（待 vectors 與溢位分析後定案）；可用於所有分布欄位（interarrival、service、delay、TTF、TTR、換線）。
- D-033：兩者的支援為 (0, ∞)，視為恆 > 0；樣本量化後至少 1 tick（同 D-038 (a)）；超過 365 天以 365 天＋1 tick 表示（D-038 (b)）。Validation：參數有限、> 0、在範圍內，`mean`／`scale` 換算後不超過 365 天（D-029）；違反回 `DISTRIBUTION_INVALID`。
- Numerical profile：exp、ln、sqrt 與 AS241 的運算順序與常數（bit pattern）寫入 vectors；不呼叫平台 `math.Exp`／`math.Pow`；`make backend-numeric` 一併檢查。改動任一即換 engine_version。實作併入 T-048（故障參數最常用到）。

**決定性與 digest。** 只影響使用這兩種分布的模型；其他 sampler 與 stream 不變。

**Golden vectors。** 同 T-020 的做法：每個 sampler 的 12-draw 向量與 10⁵ 次 draw 的 SHA-256（含量化前的 binary64 值）；10⁶ 個 exp 輸入的 SHA-256 與邊界值（下溢、上溢、次正規數）；AS241 在 U 接近 0、0.5、1 的值；由 mean、cv 換算出的 μ、σ 的 bit pattern。

**SimPy。** Python reference 逐運算移植以求逐 bit 相同；另以 numpy／scipy 等獨立實作的分布，對 10⁵ 個樣本做 KS 檢定與前兩階動差比對（預先固定容差），確認 profile 在統計上正確。

### D-057 新參數的 scenario allowlist 與 risk

**問題。** 新參數可否在 scenario（同一 ModelVersion 上的 patch）中修改？Server risk 怎麼算？

**選項。**
- A：擴充 D-021 的 scenario allowlist（人工與 AI 相同）：queue node 的 `capacity`（replace，以及 add／remove，即無限↔有限）、`breakdowns` 的 TTF／TTR distribution（replace）、`arrival/schedule/segments/*/rate` 與 `arrival/schedule/releases/*/quantity`（replace）、`batch` 的 min_size／max_size／max_wait（replace）、`setups` 的 `transitions/*/distribution` 與 `default`（replace，review M2）、resource 的 `calendar_id`（replace，只能指向既有 calendar）、`shift_capacity/*`（replace）、`calendars/*/shifts` 與 `calendars/*/exceptions`（replace）；凡是允許修改 distribution 的路徑，都可換成 D-056 的 lognormal／Weibull。新增或刪除 breakdowns、setups、calendars、schedule、batch，仍走 draft→publish。Manual 與 AI edit allowlist 加入根層 `calendars`。D-020 的「先 test 元素 id」照舊；candidate 照舊完整驗證（含 D-050 的 R1–R4，違反即 invalid；例如把 calendar_id 改成與夥伴不一致的班表會違反 R4，scenario 不能建立）。若 owner 改採 D-050 的 A′（R4 為 warning），則 scenario 新出現 `STALL_RISK` 時仍可建立，warning 顯示在審核內容中，server risk 至少 medium（review N3）。Risk：縮小 buffer 或把無限改為有限、任何 breakdown 參數、任何換線參數、改 calendar、shifts、每班容量或例外、改 release rate 或投入量、改 batch 參數至少 medium；擴大既有的有限 buffer 或改回無限為 low（與增加 resource capacity 對稱）；模型只能調高（D-027）。
- B：不擴充，新參數只能發布新版本。後果：示範的「加大 FCT 前緩衝」必須發布新版本，baseline 與 scenario 不再屬同一 ModelVersion，SIMULATION_SPEC §7 的比較條件不成立（comparable=false），只能並列描述值。
- C：只加 queue `capacity`（示範所需的最小集合）。後果：最小；班表、故障、投入的 what-if 要發布新版本，同樣不可比較。

**建議：A。** 理由：工廠的典型 what-if（加緩衝、加一班、縮短 MTTR、縮短換線、改投入）都是參數，不是拓樸；B、C 會讓這些比較失去 §7 的可比性。若 owner 希望先收斂，C 是最小可行的退路。

**決定性與 digest。** 不影響 kernel；patch_hash 與 effective_model_hash 照 D-019。

**Golden vectors。** Patch fixtures：允許的路徑通過；在 scenario 中新增 breakdowns 回 `PATCH_PATH_FORBIDDEN`；縮小 buffer 的 server risk 為 medium、擴大為 low；造成 `BLOCKING_CYCLE` 的 scenario 回 validation error。**SimPy。** 不適用。

## 不採用的架構做法

- **在 domain pack 中以現有 primitive 模擬**：resource 無法跨 process 持有、capacity 不隨時間變，做不到；也違反 ADR-010（domain 不改 generic engine）。
- **改用外部 DES 函式庫或在 production 跑 SimPy**：違反 ADR-006（native Go DES）與 AGENTS §7（不加 arbitrary Python runtime）；SimPy 只作獨立 reference。
- **一般化的「跨步驟持有 resource」或 script hook**：能表達更多（機櫃在測試槽內除錯），但會改變 D-050 的 holder 與阻塞圖，範圍與驗證成本大；script hook 違反 AGENTS §7。跨步驟持有建議在核心完成後另立任務（見下表）。

## Consequences

- **批准後的 contract 變更（T-046 phase 2；已完成並通過 review，見 checks/T-046_PHASE2_VALIDATION.md）**：`model-ir.schema.json`（queue `capacity`；resource `breakdowns`、`calendar_id`、`shift_capacity`、`setups`；根層 `calendars`（含 shift `label` 與 `exceptions`）；source `arrival.schedule`（rate 型與 table 型）；process `batch`；distribution 的 `lognormal` 與 `weibull`；`metrics[]` 的 `system_wip`、`flow_count`、`sink_count`、`period_throughput`，D-058）；`canonical-event.schema.json`（D-059）；`simulation.schema.json`（Result 的 optional 新成員與每個 replication 的 resource 狀態積分（D-058）、trace 的新事件類型、run failure 的 `SIMULATION_DEADLOCK`／`SIMULATION_STALLED`）；validator codes（`BLOCKING_CYCLE`、`CALENDAR_INVALID`、`RELEASE_SCHEDULE_INVALID`、`BATCH_INVALID`、`SETUP_INVALID`、`STALL_RISK`（error；A′ 時為 warning），以及 `RESOURCE_REQUIREMENT_INVALID` 的 D-050 R3 規則）與 D-023 的排序；patch allowlist（D-057）；valid／invalid fixtures；`make ts-contracts`。Phase 2 的做法：上述 codes 進入 ValidationCode 目錄（含 en／zh-Hant 字串），規則本身與它們在 D-023 中的位置寫在 TWIN_IR_SPEC §4（R1 在 graph rules 階段；R2–R4 在 distribution／domain semantics 之後新設的 bundle feasibility 階段），由 T-047–T-050 實作；`testdata/contracts/semantic-*.json` 的 16 個手推 fixture 先固定它們的 code 與 path。Kernel 支援前的 fail closed 需要新的 code `PRIMITIVE_NOT_SUPPORTED` 與 schema 之後的 primitive support 階段，另立 D-063（APPROVED，H-019）；phase 2 中其他由 agent 選定、批准文字沒有寫到的 contract 細節也列在 D-063。
- **Engine**：T-047–T-050 各升一次 engine_version minor（des-go-0.2.0 起；T-047 為 des-go-0.2.0，T-048 為 des-go-0.3.0）；鎖定檔不重新產生；凍結的舊 kernel 保留在 test 中；`make simulation-reference-test` 由 SimPy reference 取代（SimPy 的版本與授權在引入時固定並記入 THIRD_PARTY_NOTICES，不用 floating latest）。
- **效能**：解除阻塞在 dispatch 內連鎖、每個 unit 的故障事件與班表事件增加事件數，D-050 的檢查與 held candidate 數成正比；全部計入 event budget，T-037 的 benchmark 需涵蓋代表性的產線模型。
- **PRD 已列為 P1、維持 P1**：preemption（故障、交班與換線的中斷）、優先權與派工規則（含依產品分組的換線排序）、進階 routing、connectors（MES／CFX 等）。
- **風險**：finish-first 在長 service（燒機、機櫃測試）下低估故障與交班的影響，UI 與文件須標示；D-050 的 R1 是保守條件，可能拒絕實際不會卡住的模型，錯誤訊息要指出 cycle 上的 buffer 與共用 resource，並說明兩種修正（cycle 上至少一個 buffer 改為無限，或讓被擋的 process 不與擋住它的 process 共用 resource）；R4（`STALL_RISK`）為 error 時會拒絕「會故障的機台沒有班表、作業員有班表」這類寫法，錯誤訊息要指出改法（給機台作業員的 calendar）；若 owner 改採 D-050 的 A′（warning），有 warning 的模型可能在殘餘情形下只表現為 backlog 增長。

**其餘功能的建議（H-017：owner 希望盡量保留功能，這些項目由 owner 在 T-046 的決策頁決定）。** 下表是 agent 原先為了收斂而延後、不屬 PRD P1 的項目；「P0」者已寫進對應 decision 的提案，仍待 owner 批准。

| 項目 | 建議 | 成本 | 理由 |
|---|---|---|---|
| 每班不同人數 | P0（D-052 A＋） | 低：只是 S(t) 的另一種寫法；R2 改以 S ≥ units 判定 | 夜班人力較少很常見 |
| 假日與例外日（絕對 model 時間，不用時區） | P0（D-052 A＋） | 低：S(t) 的覆蓋；D-050 的 next_fit 已計入例外 | 週別產出比對（REQ-025）需要 |
| 投入表（整批投入） | P0（D-053 table 型） | 低：不用 RNG，validation 簡單 | 生產計畫常以整批投入 |
| 實際的 FPY 與重測次數、報廢率、逐期的模擬序列 | P0（D-058 的宣告式 generic metrics） | 低到中：只在 IR `metrics[]` 宣告時計數；逐期需每個 replication 一個陣列 | FPY 的檢查與 REQ-025 的逐期比較 |
| Capacity 0 的 buffer（無緩衝直接交接） | 之後：T-047 完成後另立任務 | 中到高：要「進入即授予」的保留語義，改變「只在 dispatch 授予」的不變量 | 以 capacity 1 近似，差距是一個暫存位 |
| 合併／拆分（拼板、分板） | 之後：核心完成後另立任務 | 高：新的 node 類型、entity 合併與新 ID、lead time、trace 與 3D 的定義 | P0 以板為單位可運作 |
| 跨步驟持有 resource（機櫃在槽內除錯） | 之後：核心完成後另立任務 | 高：新 primitive，改變 D-050 的 holder 與阻塞圖 | P0 有標示過的近似 |
| 忙碌時間的故障時鐘 | 之後 | 中：所有故障 resource 都要 unit identity | D-051 的 scheduled 時鐘已修正主要偏差 |
| 做不完就不開始（交班前） | 之後 | 中：要先抽 service 再決定是否開始，抽樣的丟棄規則會影響配對 | 需求較少，finish-first 已記 overtime |
| 搬運系統（AGV、輸送） | 之後 | 高：載具 resource、路徑與距離 | delay node（加上需要人力的 process）可近似 |
| 分段的 interarrival 分布（D-053 C） | 不採用 | — | 跨段規則扭曲分布，且打亂 scenario 之間的配對 |
