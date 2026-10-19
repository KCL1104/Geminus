# ADR-026 — 產線流動的核心 primitive：有限緩衝與阻塞、故障、班表、投入排程、批次

狀態：**PROPOSED**（2026-10-19，T-046 phase 1 由 agent 提出；owner 尚未批准）。批准前不實作 kernel、不改 schema 與 fixtures，TWIN_IR_SPEC 與 SIMULATION_SPEC 現行的 P0 語義照舊。日期：2026-10-19。依據：H-016、[ADR-025](ADR-025-p0-domain-electronics-line.md)、[ADR-006](ADR-006-native-go-des.md)、[ADR-010](ADR-010-domain-pack-architecture.md)、TWIN_IR_SPEC §8、REQ-024、TEST-034。語義細節為 D-047–D-057（PROPOSED，[登記](../DECISIONS_AND_TBD.md)）；domain 規格與 D-058–D-060 見 [ELECTRONICS_LINE.md](../domains/ELECTRONICS_LINE.md)。

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

新增下列 generic primitive，全部是 IR 的 optional 欄位，缺欄位時語義與現行完全相同。欄位名稱為提案，於 T-046 phase 2 的 schema 變更中定案。

| Primitive | IR（提案） | 摘要 | Decision | 實作 |
|---|---|---|---|---|
| 相容性與驗證方法 | — | optional 欄位、schema_version 維持 0.1、Result 新成員只在用到時出現、每個 kernel 任務升 engine_version minor、凍結舊 kernel 做 differential | D-047 | T-047–T-050 |
| 同 tick 順序 | — | 新增 priority 15（故障、修復、班表容量變化、批次逾時）；解除阻塞在 dispatch 內 | D-048 | T-047–T-050 |
| 有限緩衝與阻塞 | queue node `capacity` | blocking after service；被擋的 entity 留在出發 node、保留整個 bundle；FIFO 解除 | D-049 | T-047 |
| 死結 | — | validator 的 `BLOCKING_CYCLE`（充分條件）＋ runtime `ErrDeadlock` backstop | D-050 | T-047 |
| 故障 | resource `breakdowns` | 每個 unit 一組 time-based 故障時鐘；finish-first；stream (resource ID, `ttf/<u>`／`ttr/<u>`) | D-051 | T-048 |
| 班表 | 根層 `calendars[]`、resource `calendar_id` | 週期性上班區間；交班時 finish-first；新增 scheduled utilization，原 utilization 不變 | D-052 | T-049 |
| 投入排程 | source `arrival.schedule` | 週期性分段常數 rate，以累積 rate 反函數產生；每個 arrival 一次 draw | D-053 | T-049 |
| 批次 | process `batch` | min／max／max_wait；一批一個 request、一次 service draw；整批離開才釋放 | D-054 | T-050 |
| 換線 | — | P1（建議） | D-055 | — |
| lognormal／Weibull | — | P1（建議） | D-056 | — |
| Scenario 與 risk | patch allowlist | 新參數可在 scenario 中修改，risk 至少 medium（擴大 buffer 除外） | D-057 | T-047–T-050 |

以下逐項列出問題、選項與後果、建議、對決定性與 digest 的影響、golden vectors 草案與 SimPy reference 的做法。Golden vectors 的數字由手算得出，是草案；實作時另以 `scripts/engine_reference.py`（逐運算移植）與 kernel 輸出交叉驗證後才凍結，不得看結果再改預期值。

### D-047 相容性契約與 differential 方法

**問題。** 新 primitive 如何進入 IR、Result 與 engine_version，才能讓沒用到的模型與現行 kernel 逐 bit 相同？怎麼證明？

**選項。**
- A：IR 加 optional 欄位，schema_version 維持 `0.1`，缺欄位＝現行語義；Result 的新成員只在模型用到對應功能（或在 IR `metrics[]` 宣告，D-058）時出現；每個改 kernel 的任務把 engine_version 升一個 minor。後果：既有 ModelVersion 的 bytes、model_hash 與結果都不變，不需 migration 或升級流程；closed-object schema 讓不認得新欄位的舊 reader 仍 fail closed；schema_version 不再表示「用到哪些功能」，要看 IR 內容。
- B：新 schema_version `0.2`（0.1 的 superset），0.1 文件禁止新欄位，另做明確的升級操作（產生新 draft，hash 改變）。後果：版本語義嚴格；多一個升級 API 與 UI 流程，template、AI、patch allowlist 要同時處理兩版。
- C：新統計一律加進每個 Result。後果：所有既有模型的 result_digest 都改變，違反 REQ-024 的「沒用到即相同」；不採用。

**建議：A。** IR 仍是 P0 草案（TWIN_IR_SPEC 開頭）、尚無 production 資料，A 是最少的變更；若 owner 要嚴格的版本語義，改採 B，其餘 decision 不受影響。

**細則。**
- Go 型別對缺少的新欄位一律不輸出（pointer 或 `omitzero`），既有 fixtures 重新 encode 後 model_hash 相同（contract test）。
- 新事件計入 `max_events`；被擋住或在 release backlog 中的 entity 計入 `max_active_entities`；新的 runtime 失敗有 typed error（`ErrDeadlock`，D-050）。
- 新 observation kind 接在現有編號之後，不重新編號（observation digest 編碼含 kind 數值）。
- 既有的鎖定檔（`kernel-observation-digests.json`、`engine-golden-digests.json`）**不重新產生**。

**決定性與 digest。** engine_version 在 digest payload 中，升版會改變 digest 字串；因此「逐 bit 相同」的判準是：observation 序列（含 seq）相同，且把 Result 的 engine_version 換回 `des-go-0.1.0` 後算出的 digest 等於鎖定值。新版本另立自己的鎖。

**Differential（每個 T-047–T-050 都要做）。**
1. 把 des-go-0.1.0 的 kernel 與 replication 凍結成 test-only 副本（T-021 複核保留舊 kernel 的同一做法），以 `kernel_check_test.go` 的隨機模型產生器（bundle、顯式 queue、零時間 service、decision 迴圈、failure／rework）跑不含新欄位的模型，observation 序列逐筆相同；件數在實作任務中固定（T-021 的複核約 50,000 個）。
2. 「中性設定」必須與不設定逐筆相同：buffer capacity ≥ `max_active_entities`（不可能擋住）；breakdown 的 TTF 為 constant 且晚於 horizon（事件不排程、不取 seq）；calendar 只有一個涵蓋整個 period 的 shift（容量不變，不排事件）；單段的 deterministic 投入排程，rate 與 period 選可精確表示、1/rate 為整數 tick 的值（例如每 minute 0.5 件、period 60 minute），與同值的 constant interarrival 同時刻（兩者都不抽 RNG）。批次沒有中性設定（批次 request 取得 FIFO 位置的時點不同，D-054），不列入。
3. GOLDEN-001／002 與既有 des、metrics 測試在新 kernel 上照跑，依上面的判準比對鎖定 digest。

**SimPy。** 不適用；本項的 reference 是凍結的舊 kernel。

### D-048 新事件的同 tick 順序

**問題。** 故障、修復、班表容量變化、批次逾時與解除阻塞，在同一 tick 中排在哪裡？

**選項。**
- A：新增 priority 15（resource state 類：FAILURE、REPAIR、CAPACITY_CHANGE、BATCH_TIMEOUT），在 arrival／delay（10）之後、dispatch（20）之前，同 priority 依 seq；解除阻塞不是事件，在 dispatch 內、造成空位的授予之後立即進行（D-049）。後果：該 tick 的 dispatch 看到該 tick 最後的容量；priority 0 釋放的 unit 可被同 tick 的故障拿走；既有三個 priority 的相對順序不變。
- B：priority 5（service complete 之後、arrival 之前）。後果：dispatch 結果與 A 相同（dispatch 仍在 20），只有 observation 順序不同；沒有額外好處。
- C：priority 25（dispatch 之後）。後果：修好或上班的容量要等下一次 dispatch，每次都要多排一個 dispatch，事件數增加；同 tick 的 arrival 看不到新容量。

**建議：A。** 理由：容量的改變在授予之前生效，不需要額外的 dispatch，也不改變既有事件的順序。

**細則。** t=0 初始化順序：各 source 的第一個 arrival（node ID 順序，與現行相同），再依 resource ID 順序排各 unit 的第一次故障（unit index 順序）與各班表的第一次容量變化；批次逾時只在 entity 進入 buffer 後才排。若 dispatch 進行中為當前 tick 排了 priority 15 的事件（例如 0 tick 的修復），它在該次 dispatch 之後執行，並視需要再排一次 priority 20 的 dispatch（D-038 (j) 的延伸，tick 不倒退）。

**決定性與 digest。** 全序不變；沒有新功能的模型不會出現 priority 15 的事件，seq 與現行相同。

**Golden vectors。** (1) t 時 priority 0 釋放 unit，同 tick 故障（15）拿走它，等待的 request 在 t 不被授予；(2) 同 tick 的 arrival（10）與修復（15）：dispatch（20）在 t 授予該 arrival；(3) 同 tick 的 arrival 與上班：在 t 授予；(4) 0 tick 修復在 dispatch 之後到達：同 tick 再 dispatch 一次。

**SimPy。** SimPy 同時刻的事件依排程順序（部分內部事件為 URGENT），不嘗試重現本 kernel 的 tie-break；同 tick 的語義由手算 golden 固定，SimPy 比對只用沒有同時刻 tie 的確定性案例或預先固定容差的隨機案例。

### D-049 有限緩衝與阻塞（blocking）

**問題。** (1) 容量定義在哪、算什麼；(2) blocking after service（BAS）還是 before service（BBS）；(3) 被擋的 entity 如何持有 resource；(4) 與 resource-wise FIFO、all-or-nothing bundle 的交互；(5) source 與 delay 被擋時怎麼辦。

**選項。**
- A（BAS）：顯式 queue node 加 `capacity`（1–100000），只計等待中、尚未授予的 entity，缺＝無限；entity 的路由走到已滿的 buffer 時，留在出發的 node 成為 blocked：process 保留整個 bundle，source 形成 release backlog，delay 不占資源；buffer 有空位時依 blocked 的 FIFO 移入。後果：符合產線「做完的板子留在機台上，直到下一站有位置」；不需預留機制；不改 routing stream 的抽樣時點。
- B（BBS）：下游 buffer 有空位（或預留一格）才開始服務。後果：授予時要跨 process 預留下游空位，與 all-or-nothing bundle、resource-wise FIFO 的交互複雜；經 decision 的機率路由在服務前不知道去哪，要提早抽 routing 或預留所有可能的下游，改變 routing stream 的抽樣時點；不適合有測試失敗分支的產線。
- C：以「下游 slot resource」模擬（process 的 requires 加入下游 buffer slot）。後果：現行 kernel 在每個 process 結束時釋放全部 resource，slot 無法跨 process 持有；需要「跨步驟持有 resource」這種更一般的新 primitive（P1）。

**建議：A。** 理由：BAS 是產線的常見假設、工程師容易理解；它只在「進入 buffer」這一步加檢查，routing 的抽樣時點、resource-wise FIFO 與 bundle 規則都不變，沒有被擋時與現行完全相同。

**語義（A）。**
- **容量與「滿」**：capacity 屬於 process 的 request buffer（D-005：顯式 queue 只是它的名字），不論 entity 經 queue node 或直接進入 process 都算；占用數 = 等待中、尚未授予的 entity 數（不含服務中）。只有顯式 queue node 能宣告 capacity，沒有顯式 queue 的 process 仍為無限。capacity 0（無緩衝的直接交接）延後：它需要「進入即授予」，改變「只在 dispatch 授予」的規則。
- **路由先解析**：decision／failure／rework 這段在出發事件中照現行解析（同時刻抽 routing、記 failure／rework observation、計 visit）；只有最後一步「進入 buffer」檢查容量。被擋的 entity 目標固定，解除時不重抽。
- **SERVICE_COMPLETE**：先解析目標。不會被擋時與現行完全相同（釋放 bundle → ServiceEnd → 路由 observation → 進入）；會被擋時記 ServiceEnd、路由 observation、Blocked，bundle 維持 in use。ARRIVAL 被擋時記 Arrival 後記 Blocked（release backlog）；DELAY_COMPLETE 被擋時記 DelayEnd 後記 Blocked。
- **解除**：只有授予會讓 entity 離開 buffer，所以解除只發生在 dispatch 中。每次授予使 P 的 buffer 少一件後，若 P 有 blocked entity，依 blocked FIFO（變成 blocked 的處理順序）移入最前面的一件：原子釋放其出發 process 的整個 bundle（ObsResource；這些 resource 的 FIFO 最前面的 held candidate 取消 held，比照 SERVICE_COMPLETE）、記 Unblocked、以新的 request seq 排到 P 的 FIFO 最後（ObsQueueEnter，At＝queue node）。Dispatch 持續取最小 seq 的 candidate，直到沒有 candidate 放得下為止（fixpoint）；連鎖（下游授予 → 解除 → 上游釋放 → 上游授予 → 再解除）都在同一個 dispatch 內完成。
- **不變量**：P 有 blocked entity ⇒ P 的占用數＝capacity（事件邊界上）；新到的 entity 遇到已滿的 buffer 一律排在既有 blocked entity 之後。
- **與 FIFO 和 bundle**：blocked entity 不在 P 的 resource FIFO 中占位置；它持有的 unit 對任何 request（包括出發 process 自己的）都不可用；P 的授予仍是 all-or-nothing，D-038 (m) 的「FIFO 擋住」照舊。
- **Source**：投入時刻不因阻塞改變（arrival stream 照常），entity 在 source 等待；lead time 由 arrival 起算，所以含 backlog 時間；backlog 另有自己的統計。
- **統計**（只在模型有有限 buffer 時出現，名稱於 schema 階段定案）：每個可能被擋的 node 的阻塞次數、完整阻塞時間的平均（W ≤ 開始、結束 ≤ H）、被擋 entity 數的時間平均；每個 resource 被 blocked entity 持有的 unit 時間比例 ∫K(t)dt/(C·T)；每個有限 buffer 滿的時間比例。既有 `utilization_mean` 定義不變（∫in_use/(C·T)，含被擋住的 unit）。

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

### D-050 死結的偵測

**問題。** 有限 buffer（尤其測試失敗 → 除錯 → 重測的迴圈與共用 resource）可能循環阻塞而永久卡住；怎麼偵測、回什麼 typed error？

**選項。**
- A：靜態拒絕＋runtime backstop。Validator 在 graph rules 階段建「阻塞相依圖」，有 cycle（含自環）即回 `BLOCKING_CYCLE` error；runtime 另檢查全面停滯，回 `ErrDeadlock`。後果：可證明的充分條件、成本低、在 publish／run 之前以 JSON Pointer 回報；保守：容量其實夠大的有限迴圈也被拒絕（每個迴圈至少要有一個無限 buffer）。
- B：只做 runtime 偵測（每次 dispatch 後建 wait-for graph 找 cycle）。後果：接受所有實際不會卡住的模型；但每次 dispatch 都有額外成本，規則要區分「之後會修好、會上班」與「永遠卡住」，容易出錯；錯誤在跑到一半、依 seed 才出現。
- C：不偵測，靠 horizon 與 limits。後果：違反 TEST-034（不得默默卡住）；不採用。

**建議：A。** 理由：錯誤在執行前就以 JSON Pointer 指出，與 seed 無關；規則簡單、可獨立實作比對；保守的代價（迴圈要有一個無限 buffer）對除錯與重測區通常可接受。

**規則（A）。**
- **Holder**：至少需要一個 resource 的 process（含批次 process）；source、delay、沒有 resource 的 process 被擋時不持有任何東西。
- **阻塞邊** A → P：A 是 holder，且 A 的出路（沿 p>0 flow，經 decision／failure／rework，可再經一個 queue node）可能到達 P 的有限 buffer。
- **共用分量**：需要同一個 resource 的 process 屬同一分量（union-find，依 ID 順序）。
- **判定**：把阻塞邊投影到分量上（分量(A) → 分量(P)），有 cycle（含分量(A)＝分量(P)）就拒絕。Issue：`BLOCKING_CYCLE`、error、path 為 cycle 上每個有限 queue 的 `/nodes/<i>/capacity`，message 依 ID 順序列出 cycle 的 node；依 D-023 排序。
- **充分性（草證）**：永久被擋的 entity 等的 buffer P 永不再授予；P 最前面的 request 永遠放不下，其 resource 的 unit 由服務中（有限時間）、停機（有限修復）、下班（下一個 shift 會來，D-052 要求至少一個 shift）或 blocked entity 持有，也可能排在共用 resource 的其他 request 之後；只剩 blocked holder 能永久占住，而它們屬於 P 的共用分量，又在等另一個有限 buffer；圖是有限的，故必有 cycle。沒有 cycle 就沒有永久阻塞。
- **Runtime backstop**：dispatch 結束時若有 blocked entity，而 heap 中沒有任何可能釋放 unit 或空位的事件（SERVICE_COMPLETE、DELAY_COMPLETE、REPAIR、增加容量的 CAPACITY_CHANGE、BATCH_TIMEOUT），且沒有此類事件因晚於 horizon 被丟棄，回 `ErrDeadlock`（帶最早 blocked entity 的 ID、node、tick；job failure code 暫定 `SIMULATION_DEADLOCK`）。在 A 的規則下它不應觸發，若觸發即為規則缺口，以 bug 處理並保留最小重現。
- Template 的除錯與重測 buffer 為無限，因此通過（[ELECTRONICS_LINE §4](../domains/ELECTRONICS_LINE.md)）。

**決定性與 digest。** Validator 輸出依 D-023 排序；runtime 檢查只讀計數，不影響事件順序。

**Golden vectors。** Invalid：(1) process 經 decision 回到自己的有限 queue（自環）；(2) 兩個有限 buffer 構成迴圈；(3) A 與 P 共用 resource 且 A 可能被 P 的有限 buffer 擋住（圖上沒有 flow cycle 也拒絕）。Valid：template 的迴圈（除錯、重測 buffer 無限）；迴圈中唯一的無限步驟是 delay（delay 不是 holder）。Runtime：test-only 的 Program 繞過 validator，建一個已知的兩站死結，在預期 tick 回 `ErrDeadlock`。

**SimPy。** SimPy 本身不偵測死結（事件用完即停）。Reference 以 Python 獨立實作同一個圖規則，在隨機 IR 上與 Go validator 比對 issue 清單；另以 SimPy 跑被拒絕的小 buffer 模型，作為「確實可能停滯」的示意（不作為驗收條件）。

### D-051 Resource 故障（MTBF／MTTR）

**問題。** (1) 故障時鐘依日曆時間還是忙碌時間；(2) 故障時服務中的 entity：preempt-resume、preempt-restart 或 finish-first；(3) 多 unit 的 resource；(4) RNG stream 命名，使既有 stream 不變；(5) availability 的定義。

**選項（服務中 entity 的處理）。**
- A：每個 unit 一組獨立的 alternating renewal，時鐘依日曆時間（從修好起算，不論忙閒、班表、阻塞）；故障時若有不在使用中的 unit（含下班的）立即停機；全部在使用中時 finish-first：等下一個被釋放的 unit 停機（多個等待中的故障依故障時刻 FIFO），修復時間從停機起算；服務中的工件照常完成。後果：kernel 的匿名 unit 就夠，不需 unit identity、不需取消已排程的事件；每次故障的停機時間＝TTR，產能損失與 preempt-resume 相同，只有那一件工件完成得較早；長時間 service（燒機、機櫃測試）會低估中斷的影響。
- B：preempt-resume（中斷、修好後做完剩餘時間）。後果：需要 unit identity（哪個工件被中斷）、取消並重排 SERVICE_COMPLETE（heap 要 lazy deletion）、規定 bundle 其他 resource 在中斷期間保留或釋放；與阻塞、班表、批次的交互多；P1。
- C：preempt-restart（中斷後重做或報廢）。後果：同 B，另需重抽 service（RNG 消耗改變）或報廢路由；P1。

時鐘的其他選項：忙碌時間（需要每個 unit 的忙碌累計，即 unit identity）、次數制（每 N 件）；都延後。

**建議：A，時鐘依日曆時間。** 理由：實作最小、與匿名 unit 相容；故障序列與負載無關，scenario 之間配對最好；MTBF 可直接由停機紀錄的「修好到下次停機」日曆時間估計（[ELECTRONICS_LINE §6](../domains/ELECTRONICS_LINE.md)）。

**語義（A）。**
- IR：resource 的 `breakdowns: {time_to_failure: {distribution}, time_to_repair: {distribution}}`。TTF 的最小可能 tick 必須 > 0（同 D-033 對 interarrival 的規則，避免同一 tick 的事件風暴），否則 `DISTRIBUTION_INVALID`；TTR ≥ 0；empirical 經 dataset binding（D-006）。
- 每個 unit u（0..capacity−1）在 t=0 為正常，第一個 TTF 在 t=0 抽；故障 → 停機時抽 TTR、排 REPAIR；REPAIR 時 down 減一、抽下一個 TTF、排 FAILURE。
- 授予條件改為 `inUse + units ≤ min(S(t), C − down)`（S 為班表容量，沒有班表時為 C，D-052）。等待中的故障在每次釋放 unit 時優先滿足（被釋放的 unit 直接停機），其餘才變成可用；REPAIR 與上班一樣會取消該 resource 最前面 candidate 的 held 並排 dispatch。
- **Stream**：(resource ID, `ttf/<u>`) 與 (resource ID, `ttr/<u>`)，u 為十進位 unit index；validator 已要求 node 與 resource ID 不重複（DUPLICATE_ID），purpose 也是新的字串，既有 stream 不受影響。Capacity 加一只新增 unit C 的 stream，unit 0..C−1 的抽樣序列不變（stream 層級的 common random numbers）。
- **統計**（只對有 breakdowns 的 resource）：視窗內開始停機的次數、停機比例 ∫D/(C·T)、完整修復時間的平均、availability ＝ ∫min(S, C−D)dt / ∫S dt（∫S=0 時 unavailable）。等待中（尚未停機）的時間不算停機。Equipment breakdown 與 generic `failure` node（測試不良）是不同的東西，`failure_count` 照舊只算 failure node（D-010）。

**決定性與 digest。** 新事件（FAILURE、REPAIR，priority 15）與 ResourceDown／ResourceUp observation 只出現在有 breakdowns 的模型。

**Golden vectors。**
- G-F1 閒置時故障：1 unit、interarrival 2、服務 1、TTF 10、TTR 3（constant）：t=10 的 arrival 先進 buffer，故障（15）使 unit 停機到 13，13 時授予，下一次故障在 23。
- G-F2 服務中故障（finish-first）：interarrival 3、服務 2、TTF 10、TTR 3：10 時 unit 忙（9–11），故障等待；11 釋放即停機到 14；12 到的 entity 在 14 開始；下一次故障在 24，此時 unit 閒置，24 到的 entity 等到 27。
- G-F3 2 unit、一個等待中的故障、兩次釋放：第一個被釋放的 unit 停機、第二個變成可用。
- G-F4 capacity +1 的 scenario：unit 0..C−1 的 TTF／TTR 抽樣序列與 baseline 相同（比對 stream 輸出，不比對時刻）。
- G-F5 中性：constant TTF 晚於 horizon 與沒有 breakdowns 逐筆相同。
- `engine-vectors.json` 加各 unit 前 k 個 TTF／TTR 的 Python 參考值。

**SimPy。** 用 `simpy.PriorityResource`：每個 unit 一個故障 process，`timeout(TTF)` 後以比工件高的 priority request 一個 unit（non-preemptive），取得後 hold TTR 再釋放。Non-preemptive priority 的語義正是「取閒置 unit，否則取下一個被釋放的 unit」；同 priority 依 request 時刻排序，對應等待中故障的 FIFO。

### D-052 班表（shift calendar）

**問題。** (1) 班表在 IR 中以 tick 表示且不用時區；(2) 交班時服務中的 entity；(3) utilization 的分母。

**選項（表示）。**
- A：根層 `calendars[]`（`id`、`period`、`shifts`：period 內的 [start, end) 上班區間，以 model 的 time_unit 表示），resource 以 `calendar_id` 參照；上班時 S(t)＝capacity，下班時 0。後果：多站共用一份班表，有共同的「線的排班時數」可作 UPH 分母；無法表達每班人數不同（白班 2 人、夜班 1 人），延後為 optional 的每班容量，不破壞 A 的文件。
- B：每個 resource 自己的週期性容量表（[{at, capacity}]）。後果：可表達每班不同人數；每個 resource 重複一份；沒有共同的線班表，UPH 每排班小時要另外指定分母。
- C：絕對日期的班表（含假日、例外）與時區。後果：需要時區、DST 與日期函式，違反「以 tick 表示、不用 wall clock」（T-049 scope）；P1。

**建議：A。** 理由：多站共用同一份班表是產線的常態，也給「排班小時」一個明確的分母；每班人數不同可以之後以 optional 欄位加入，不破壞 A 的文件。

**語義（A）。**
- t=0 對應 period 的 offset 0（model 本地時間；沒有時區、DST、假日）。Period ≤ 365 天；shifts 至少一個、依 start 排序、不重疊、0 ≤ start < end ≤ period（跨午夜的夜班拆成兩段）；以 `model.Ticks` 量化，量化後仍須嚴格遞增；至多 100 段（D-029 類的候選上限）。違反回 `CALENDAR_INVALID`；未知 `calendar_id` 回 `UNKNOWN_REFERENCE`。沒有 `calendar_id` 的 resource 全天可用（D-017：不暗加班表）。
- Kernel 只在 S 改變時排 CAPACITY_CHANGE（priority 15）；同時間只有一個待處理的變化事件。
- **交班選項**：(a) finish-first：服務中與被擋住的 entity 照常完成或等待，超出排班的忙碌時間記為 overtime；(b) preempt-resume：與 D-051 B 相同的機制，P1；(c) 做不完就不開始：要在授予前知道 service 長度並改變抽樣時點，P1。**建議 (a)。**
- **分母選項**：(i) 原 `utilization_mean`（∫inUse/(C·T)）不變，另對有班表的 resource 加 `scheduled_utilization` ＝ ∫min(inUse, S)dt/∫S dt 與 overtime ＝ ∫max(inUse−S, 0)dt；(ii) 有班表的 resource 改用 ∫S 當 `utilization_mean` 的分母：同一欄位對不同 resource 意義不同，舊的使用者會誤讀；(iii) 只有 C·T：單班滿載的站顯示約 33%，工程師會覺得錯。**建議 (i)。**
- 與故障：故障時鐘在下班時照走；授予條件 `inUse + units ≤ min(S, C−down)`。

**決定性與 digest。** CAPACITY_CHANGE 不用 RNG；只有引用班表的 resource 會有新事件與 Capacity observation。

**Golden vectors。** G-C1：1 unit、period 10 min、shift [0,6)、interarrival 2、服務 3：服務 2–5、5–8（6 下班，overtime 2 分鐘）；6 到的 entity 在 10 上班時開始（10–13），8 到的在 13 開始；第一個 period 的 scheduled utilization 4/6。G-C2：上班與 arrival 同 tick，於該 tick 授予。G-C3：中性（一個涵蓋整個 period 的 shift）與沒有班表逐筆相同。G-C4：bundle 的兩個 resource 有不同班表，只在兩者都上班時授予。

**SimPy。** 每個 resource 一個班表 process：下班時以最高 priority（non-preemptive）request 全部 unit 並持有到上班；上班時釋放已取得的 unit，並取消尚未取得的 request。Finish-first 與本提案相同。與故障同時使用時只做隨機容差比對。

### D-053 隨時間變化的投入（release schedule）

**問題。** 投入率依時段變化要怎麼表示？與 common random numbers 的關係？

**選項。**
- A：週期性分段常數 rate，以累積 rate 反函數產生：source 的 `arrival.schedule`（`period`、`segments: [{start, rate}]`，rate 為每個 model time_unit 的期望件數，`process`：`poisson` 或 `deterministic`），與 `arrival.distribution` 二擇一。令 Λ(t)＝∫₀ᵗ λ(s)ds、a₀＝0，第 k 件 aₖ＝aₖ₋₁＋Xₖ，Xₖ＝1（deterministic）或 −ln(1−U)（poisson，從該 source 的 arrival stream 抽一次），時刻 τₖ＝min{t : Λ(t) ≥ aₖ}，量化為 tick 後至少比上一件晚 1 tick。後果：每件恰一次 draw，不同 scenario 的第 k 件用同一個 draw（配對保留）；poisson 是精確的非齊次 Poisson process，deterministic 是精確的 takt；不支援 triangular／empirical interarrival；一次投入一整批以短時段高 rate 近似。
- B：明確的投入表（[{at, quantity}]，週期或絕對）。後果：最貼近生產計畫、不用 RNG；長 horizon 的 takt 投入需要大量列（每 30 秒一件、一週約 2 萬列）；可作為 P1 附加。
- C：各時段各自的 interarrival 分布，跨段時丟棄並在下一段起點重抽。後果：支援任意分布；跨段規則對非指數分布有扭曲；draw 的消耗取決於跨段次數，scenario 之間的配對被打亂。

**建議：A。** 理由：規劃者通常以「每小時投入多少」描述投入；每件恰一次 draw，使 scenario 之間的比較保持配對；沒有跨段規則的歧義。

**語義（A）。**
- Validation（`RELEASE_SCHEDULE_INVALID`）：period ∈ (0, 365 天]；第一段 start＝0、start 嚴格遞增且 < period；rate 有限且 ≥ 0、至少一段 > 0、每段 rate ≤ 每 tick 1 件；至多 1000 段；`max_entities` 照舊適用。
- Numerical profile：P＝Λ(period) 依段順序以 binary64 累加（每個乘積明確捨入，同 D-038 (g)）；k＝⌈a/P⌉−1、r＝a−k·P ∈ (0, P]，取第一個累積 ≥ r 的正 rate 段，τ＝k·period＋startⱼ＋(r−cumⱼ)/rateⱼ；量化同 `model.Ticks`（一次乘法、round half up）；ln 用固定的 `engine.ln`。累加器 a 保留未量化的值，不從量化後的 tick 回推，避免漂移。
- 第一件在 Λ⁻¹(X₁)，與現行「t=0 起抽第一個 interarrival」一致。

**決定性與 digest。** 只有用 schedule 的 source 改變；其他 source 的 stream 與事件不變。

**Golden vectors。** G-R1 deterministic：period 60 min、[0,30) rate 1/min、[30,60) rate 0：arrival 在 1..30、61..90、121..150 分鐘（a＝30 落在 30 而不是 60，固定 ⌈⌉ 的邊界）。G-R2 poisson：固定 seed 的前 N 個 arrival tick 由 Python reference 逐 bit 產生。G-R3 一段 rate 加倍的 scenario：第 k 個 draw 與 baseline 相同。G-R4 中性：上述可精確表示的單段 rate 與同值的 constant interarrival 同時刻。G-R5 高 rate：tick 嚴格遞增。

**SimPy。** 以不同的演算法產生非齊次 Poisson process（Lewis–Shedler thinning，Python 自己的 RNG），在大量 replication 上比對各段件數的平均與變異數（預先固定容差）；deterministic 逐值比對。

### D-054 批次 process（燒機）

**問題。** 批量的上下限與等待上限；批內工件的 ID 與 trace；lead time 與 utilization 的定義。

**選項。**
- A：process 加 `batch: {min_size, max_size, max_wait}`。Buffer 中等待的工件達 max_size，或最老的已等 max_wait 且至少 min_size 件時，「就緒」並建立一個 batch request（一個 bundle、取得一個 FIFO 位置）；授予時依 FIFO 取 min(等待數, max_size) 件；整批一次 service draw、同時開始與結束；完成後依 FIFO 逐件路由；整批都離開（含被擋住的）才釋放 bundle。後果：符合燒機室；batch request 在「就緒」時才取得 FIFO 位置（一般 process 是每件進入時）；每個批次 process 同時至多一個未授予的 batch request。
- B：只有固定批量（湊滿 max_size 才開始，沒有 max_wait）。後果：最簡單；低投入時段（夜班）可能永遠湊不滿，不真實。
- C：合併成一個 entity（assemble／separate）。後果：需要合併與拆分、ID 對照，trace 與 lead time 另定；P1（拼板／分板也屬此類）。

**建議：A。** 理由：燒機室的典型規則就是「滿批或等太久就開」；成員保留自己的 ID，lead time、trace 與 3D 回放都不需新的 entity 概念。

**語義（A）。**
- Validation（`BATCH_INVALID`）：1 ≤ min_size ≤ max_size ≤ 10000；min_size < max_size 時必須有 max_wait（≥ 0）；buffer 為有限時 capacity ≥ max_size。
- 就緒檢查：每次有 entity 進入 buffer、每次授予之後、以及 BATCH_TIMEOUT（priority 15）時。最老的未分批 entity 改變時排一個逾時事件（進入時刻＋max_wait；已過期則立即檢查），過時的逾時事件到時忽略。授予後立即重新檢查，所以多個 chamber 可在同一次 dispatch 各開一批。
- 授予：一次 bundle（units 以批計）、從 (process, service) stream 抽一次；成員依 FIFO 各記 ServiceStart 並帶批次 ID `r<rep>/<process>/b<n>`，另記 BatchStart（批量）。完成：先依 FIFO 解析每個成員的路由（routing draw 依成員順序，進入同一 buffer 時依序占位）；沒有成員被擋時，先釋放 bundle 再依序記各成員的 ServiceEnd 與路由；有成員被擋時不釋放，被擋的成員依 D-049 等待，最後一個離開時才釋放。
- Lead time、queue wait 照每件計（queue wait 含等待湊批）；resource 的 utilization 是 chamber 忙碌（不是槽位）；批次統計（只對批次 process）：開始的批數、平均批量、平均批量／max_size。
- 與阻塞：成員被擋時持有 chamber，批次 process 是 D-050 的 holder。與故障、班表：finish-first（整批做完）。

**決定性與 digest。** 一批一個 SERVICE_COMPLETE、一次 service draw；BATCH_TIMEOUT 與批次 observation 只出現在有批次 process 的模型。

**Golden vectors。** G-T1：1 chamber、min＝max＝4、服務 30、interarrival 10：批次在 40、80、120…開始；成員等待 30、20、10、0 分鐘，lead time 60、50、40、30。G-T2：min 2、max 4、max_wait 25、服務 30、interarrival 30：55 時只有 1 件不就緒；60 時第二件到、最老已等 30 ≥ 25 → (e1, e2) 於 60–90；同樣 (e3, e4) 於 120–150。G-T3：2 chamber、9 件等待、max 4：同一次 dispatch 開兩批、留 1 件。G-T4：完成時下游已滿：chamber 持有到最後一個成員解除。G-T5：成員的 routing draw 依 FIFO 順序。

**SimPy。** Buffer 為 `simpy.Store`；一個 batcher process 等到件數 ≥ max_size，或件數 ≥ min_size 且最老的已等 max_wait（以 timeout 事件喚醒），再 request chamber、取出至多 max_size 件、timeout(service)、逐件放到下一站。確定性案例逐值比對，隨機案例以預先固定的容差比對。

### D-055 換線（setup／changeover）是否進 P0

**問題。** High-mix SMT 的換線時間是否在 P0 建模？

**選項。**
- A：延到 P1。後果：示範只用一個產品族；換線損失若必要，以班表中的計畫停機或較長的 cycle time 近似，並標示為近似。
- B：P0 最小版：process 在 entity_type 與上一件不同時加一段固定的 setup 時間。後果：kernel 的 unit 是匿名的，「上一件」只能以 process 為單位，capacity > 1 時語義模糊；source 需要產品組合（mix），目前沒有。
- C：P0 完整版：setup matrix（from→to）、依 unit 記錄狀態、依產品分組的派工規則。後果：需要 unit identity 與非 FIFO 的派工，與 resource-wise FIFO 衝突；多個新 decision，工作量最大。

**建議：A。** 理由：需要產品組合、unit identity 與派工規則三個尚不存在的基礎；P0 的示範問題（加測試機、加緩衝）不依賴換線。延後項目：產品組合、sequence-dependent setup、依產品分組的派工、換線的停機統計。

**決定性與 digest。** P0 無影響。**Golden／SimPy。** 不適用（P1 時另立 decision）。

### D-056 lognormal 與 Weibull 是否進 P0

**問題。** 修復時間與故障間隔常用 lognormal、Weibull 描述；是否在 P0 加入？

**選項。**
- A：延到 P1，P0 維持 constant、exponential、triangular、empirical；TTF／TTR 由停機紀錄以 empirical 表示，沒有資料時用 exponential 或 triangular。
- B：P0 加 lognormal（以 mean 與 CV 參數化）。
- C：P0 加 lognormal 與 Weibull。

B、C 的後果：Weibull 的反函數需要 pow（＝exp(y·ln x)），lognormal 需要常態分布的反函數（有理逼近）與 exp；每個都要固定演算法（不用平台 `math.Exp`／`math.Pow`、不融合 FMA）、Python 逐運算移植、vectors、`make backend-numeric` 的組語檢查、參數範圍與 365 天上限的 validator 規則，以及新的 engine_version。

**建議：A。** 理由：P0 的主線是「匯入紀錄 → empirical」，參數化分布的示範價值低，數值 profile 的成本高。批准 B 或 C 時，工作併入 T-048。

**決定性與 digest。** A 無影響。**Golden／SimPy。** B、C 時：每個 sampler 10⁵ 次 draw 的 SHA-256（同 T-020）；SimPy 端以 numpy／scipy 的分布做統計比對。

### D-057 新參數的 scenario allowlist 與 risk

**問題。** 新參數可否在 scenario（同一 ModelVersion 上的 patch）中修改？Server risk 怎麼算？

**選項。**
- A：擴充 D-021 的 scenario allowlist（人工與 AI 相同）：queue node 的 `capacity`（replace，以及 add／remove，即無限↔有限）、`breakdowns` 的 TTF／TTR distribution（replace）、`arrival/schedule/segments/*/rate`（replace）、`batch` 的 min_size／max_size／max_wait（replace）、resource 的 `calendar_id`（replace，只能指向既有 calendar）與 `calendars/*/shifts`（replace）；新增或刪除 breakdowns、calendars、schedule、batch 仍走 draft→publish。Manual 與 AI edit allowlist 加入根層 `calendars`。D-020 的「先 test 元素 id」照舊；candidate 照舊完整驗證（含 `BLOCKING_CYCLE`）。Risk：縮小 buffer 或把無限改為有限、任何 breakdown 參數、改 calendar 或 shifts、改 release rate、改 batch 參數至少 medium；擴大既有的有限 buffer 或改回無限為 low（與增加 resource capacity 對稱）；模型只能調高（D-027）。
- B：不擴充，新參數只能發布新版本。後果：示範的「加大 FCT 前緩衝」必須發布新版本，baseline 與 scenario 不再屬同一 ModelVersion，SIMULATION_SPEC §7 的比較條件不成立（comparable=false），只能並列描述值。
- C：只加 queue `capacity`（示範所需的最小集合）。後果：最小；班表、故障、投入的 what-if 要發布新版本，同樣不可比較。

**建議：A。** 理由：工廠的典型 what-if（加緩衝、加一班、縮短 MTTR、改投入）都是參數，不是拓樸；B、C 會讓這些比較失去 §7 的可比性。若 owner 希望先收斂，C 是最小可行的退路。

**決定性與 digest。** 不影響 kernel；patch_hash 與 effective_model_hash 照 D-019。

**Golden vectors。** Patch fixtures：允許的路徑通過；在 scenario 中新增 breakdowns 回 `PATCH_PATH_FORBIDDEN`；縮小 buffer 的 server risk 為 medium、擴大為 low；造成 `BLOCKING_CYCLE` 的 scenario 回 validation error。**SimPy。** 不適用。

## 不採用的架構做法

- **在 domain pack 中以現有 primitive 模擬**：resource 無法跨 process 持有、capacity 不隨時間變，做不到；也違反 ADR-010（domain 不改 generic engine）。
- **改用外部 DES 函式庫或在 production 跑 SimPy**：違反 ADR-006（native Go DES）與 AGENTS §7（不加 arbitrary Python runtime）；SimPy 只作獨立 reference。
- **一般化的「跨步驟持有 resource」或 script hook**：能表達更多（機櫃在測試槽內除錯），但範圍與驗證成本遠大於 P0 需要；延後（見 ELECTRONICS_LINE §9）。

## Consequences

- **批准後的 contract 變更（T-046 phase 2）**：`model-ir.schema.json`（queue `capacity`、resource `breakdowns` 與 `calendar_id`、根層 `calendars`、source `arrival.schedule`、process `batch`、`metrics[]` 的 `system_wip`，D-058）；`simulation.schema.json`（Result 的 optional 新成員、trace 的新事件類型）；validator codes（`BLOCKING_CYCLE`、`CALENDAR_INVALID`、`RELEASE_SCHEDULE_INVALID`、`BATCH_INVALID`）與 D-023 的排序；patch allowlist（D-057）；valid／invalid fixtures；`make ts-contracts`。
- **Engine**：T-047–T-050 各升一次 engine_version minor（des-go-0.2.0 起）；鎖定檔不重新產生；凍結的舊 kernel 保留在 test 中；`make simulation-reference-test` 由 SimPy reference 取代（SimPy 的版本與授權在引入時固定並記入 THIRD_PARTY_NOTICES，不用 floating latest）。
- **效能**：解除阻塞在 dispatch 內連鎖、每個 unit 的故障事件與班表事件增加事件數；全部計入 event budget，T-037 的 benchmark 需涵蓋代表性的產線模型。
- **限制（延後）**：preemption（故障與交班）、換線、lognormal／Weibull、每班不同人數、假日與時區、明確投入表、capacity 0 的 buffer、合併／拆分（拼板）、跨步驟持有 resource、優先權與派工規則、搬運（P0 用 delay node）、實際的 FPY 與重測次數統計（T-029 的 metrics 對應）。
- **風險**：finish-first 在長 service（燒機、機櫃測試）下低估故障與交班的影響，UI 與文件須標示；`BLOCKING_CYCLE` 是保守條件，可能拒絕實際不會卡住的模型，錯誤訊息要說明「每個迴圈至少一個無限 buffer」。
