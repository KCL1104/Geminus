# ADR-025 — P0 domain：電子組裝／測試線

狀態：ACCEPTED（2026-10-19，owner H-016：「用你推薦的吧」）。日期：2026-10-19。修改 ADR-010 選定的 domain；ADR-010 的架構邊界不變。

## Context

ADR-010（SOURCE_LOCKED）定下「一個深入的 SoftwareDeliveryDomainPack + generic kernel」，並規定改動原報告的架構邊界須另立 ADR、由人類確認。Owner 認為 Software Delivery 作為 P0 demo 太弱，請 agent 先深入研究再討論。Agent 以五組研究比較候選 domain（製造、倉儲物流、商業流程、醫療＋軟體交付、跨領域與台灣），重點如下：

- Software Delivery：資料最好取得、與引擎最合、法規最輕；但 LinearB、Swarmia、Jellyfish 已把 Monte Carlo what-if 當功能販售，沒找到更深模擬的需求證據，也無法預告實體場景的願景。
- 製造（電子組裝／測試線）：離散事件模擬最大的應用、台灣產業最吻合、最能帶出 3D；但競爭最擠（Siemens、AnyLogic 的 AI 助手，Simio 2026-09-23 起內建 MCP），且工廠工程師把阻塞、故障、班表、換線、批次視為基本功能。
- 倉儲人力與尖峰：流程問題、資料容易取得、法規輕，台灣買家少；引擎元件與製造大多共用。

研究以網路公開資料為主，搜尋額度中途用完，市場規模數字來自研調新聞稿、彼此差距大；沒有客戶訪談。Owner 目前沒有可接觸的潛在使用者，近期目標是初步產品與技術驗證後投 launchpad。

## Decision

- P0 深入做的 domain pack 改為**電子組裝／測試線**（SMT、AOI、ICT、FCT、燒機，含測試失敗→除錯→重測；AI 伺服器機櫃整合測試為變體）。站別、資源 catalog、指標與 canonical event fields 由 T-046 定義。
- SoftwareDeliveryDomainPack（T-011）保留，作為範例與 conformance pack；不刪除，不再是產品主打。
- 第二個 domain 是倉儲人力與尖峰，屬 P1；它開始前決定 3D authoring（H-014）。
- ADR-010 的邊界不變：generic kernel 不 import 任何 domain pack；domain 專屬的 catalog、規則、推論、指標、prompt 和 view 都在 pack 裡。
- 電子產線需要的新核心 primitive（有限緩衝與阻塞、resource 故障、班表、隨時間變化的投入、批次；換線待定）依 TWIN_IR_SPEC §8 另立 ADR，由 T-046 提出，語義細節為 PROPOSED decisions，owner 批准前不實作。

## Consequences

- PRD：REQ-003、REQ-004、REQ-013、REQ-015 改寫，新增 REQ-024（產線流動語義）與 REQ-025（模擬與觀測的比對）；P1 的 calendar 中，班表拉進 P0，priority／preemption 仍為 P1。
- 計畫：新增 T-046–T-052；T-015、T-017、T-027、T-037、T-038、T-040 加上新的相依。原報告的 16 週估計早於本決定，新增的引擎工作尚未重估週數。
- 示範資料一律標為 synthetic，不宣稱客戶驗證，也不宣稱模型已對真實產線校準（REQ-025 沒有觀測資料時回 unavailable）。
- 風險：工廠 DES 市場競爭最擠，AI 輔助建模不是獨特賣點，差異要放在「作業紀錄 → 模型 → 與觀測比對 → 人類批准」；工程師的擬真門檻高，缺阻塞或故障的結果會被直接否定；產線資料涉及營業秘密、客戶偏好地端部署，與尚未決定的雲端部署方式可能衝突。
