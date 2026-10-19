# Product

<!-- impeccable:product-schema 1 -->

本檔記錄 Geminus 的產品事實，供 UI 設計與評審使用，不含視覺決定（視覺系統待 owner 選定方向後另記於 DESIGN.md）。來源：`docs/PRD.md`、`docs/DECISIONS_AND_TBD.md`，以及 owner 在 2026-10-19 的回答（H-012、H-016）。與 SOURCE 或 PRD 衝突時以它們為準。

## Platform

web

## Users

- **長期對象**：任何需要先模擬再決策的流程或營運負責人。Owner 希望涵蓋製造業、工業、倉儲、航太、城市、建築工程等場景（H-012）。
- **P0 對象**：電子組裝／測試線的使用者，包括工業工程（IE）、生產技術、產線規劃人員和製造主管（H-016，PRD §2）。原報告的 P0 是 Software Delivery，現在只保留為範例 pack。
- **權限角色**：只有 Owner、Editor、Viewer 三種。Owner 與 Editor 可以編輯、建立 proposal、批准、啟動 simulation；Viewer 只能讀取；只有 Owner 能管理成員。

Owner 確認的三種使用情境（H-012）：

1. **桌機決策**：一週數次，每次 30–60 分鐘。使用者帶著 what-if 問題來，例如「多一台 ICT 測試機或加大 FCT 前的緩衝後，UPH、瓶頸站、WIP、utilization 會怎麼變」，要的是可信的比較，不是看動畫。
2. **每天長時間建模**：process owner 每天在 2D 編輯器裡長時間維護模型。這需要長時間使用不疲勞、資訊夠密、鍵盤操作順手。
3. **會議投影**：baseline 與 scenario 的比較結果常在會議中投影或分享螢幕給團隊看。結果畫面在投影機和低品質螢幕分享下也必須看得清楚。

## Product Purpose

Geminus 是 AI-native 的 Digital Twin Modeling + Simulation + Decision Support 產品。它的核心閉環是：Describe／Import → Model → AI Proposal → Human Approval → Simulate → Compare → AI Explain → Iterate。

成功的定義是驗證 SOURCE 的三個假設：AI 降低建模門檻；simulation 給出使用者願意拿來決策的答案；同一套 Generic IR、DES 和 AI 架構能支撐下一個 domain。這三個是 pilot 要驗證的假設，目前都尚未證實。

## Positioning

Geminus 不是產線看板，也不是 3D 動畫工具。它在架構上由四個彼此分離的邊界組成：一個通用的 Twin kernel（Generic IR + DES），各領域各自的 DomainPack，一個只能提出建議的 AI 層，以及由人類批准的 ModelChangeProposal。

它的差異在三點：

- **AI 只建議，人類批准**：AI 不能批准、發布或自行啟動 simulation。
- **結果可重現**：每個結果都綁定不可變的 model version、dataset、engine、config 和 seed，因此可以重跑出同樣的結果。
- **結果是條件化估計**：結果是在上述條件下的估計，不是對未來的保證；資料不足時，產品明說 needs_input 或 unavailable，不會捏造數字。

## Operating Context

主要旅程（PRD §4）：

- **建立與人工修改**：選 project → 選 template → 在 2D 編輯 draft → 處理 validation issue → Save Draft → 顯式 Publish。
- **匯入與 AI 建模**：CSV／JSON 上傳 → preview 與 row errors → mapping → 驗證 → commit dataset → AI proposal → 審 diff 與 assumptions → approve 或 reject。
- **Scenario 與決策**：選不可變的 version → 跑 baseline run → 提出 scenario → 審核並批准 → 手動 Run → monitor 或 cancel → compare → AI analysis → 決定下一個實驗。
- **3D 與結果**：3D 讀取同一個 IR、version 和 run；2D 與 3D 的選取共用 entity ID；playback 用 sampled trace，並明示是取樣。

產品必須讓使用者看得出兩種操作的差別：「把變更存成實際的 model version」和「只做 what-if scenario」。兩者的按鈕和回傳物件不可混用。

## Capabilities and Constraints

- **Domain 範圍**：P0 先做電子組裝／測試線（SMT、AOI、ICT、FCT、燒機），第二個正式 domain 是倉儲人力與尖峰，屬於 P1（H-016）。Owner 原本想從軟體開發起步，研究後改選電子產線：它是離散事件模擬最大的應用、和台灣產業最吻合，也最能帶出 3D 與實體場景的願景。長期願景涵蓋所有場景（H-012）。因此 kernel、介面詞彙、圖示和設計系統都不能預設只有一種情境，domain 專屬的東西由 DomainPack 提供。
- **引擎範圍**：為了讓產線工程師相信結果，P0 引擎補上有限緩衝與阻塞、機台故障、班表、隨時間變化的投入和批次（REQ-024）；語義待 owner 批准（T-046）。
- **2D 與 3D**：P0 中 2D 是 authoring，3D 是唯讀的 projection 和 playback；兩者共用 entity ID 與 IR topology（SOURCE）。Owner 認為 3D 對後續的空間型 domain（工廠、倉儲、城市、建築）可能更關鍵（H-012）。P0 只做唯讀 3D；3D authoring 在選定第二個 domain 時再決定（H-014）。3D 是一級畫面，不是附屬品。
- **裝置**：以桌機為主。手機可以看結果、審核和批准或拒絕 proposal，但不編輯拓撲（H-012）。所有寬度都不能跑版（REQ-023）。
- **語言**：英文 `en` 是預設，另支援繁體中文 `zh-Hant`。所有使用者可見的字串都來自 message catalog（REQ-022）。
- **並行與不可變**：版本衝突（stale）時回 409 並清楚顯示，不 silent overwrite，也不自動 rebase。ModelVersion、Scenario 和完成的 result 都不可原地修改。
- **P0 不做**：BIM／CAD、physics simulation、3D authoring、即時串流、AI 的 production 動作（AGENTS.md §7）。
- **尚未決定**：live AI provider 的使用政策與 quota、cloud 廠商、Auth0 tenant 設定、launchpad 的計畫名稱與期限（H-016）。視覺方向已由 H-013 決定。

## Brand Commitments

產品名稱是 **Geminus**。目前沒有 logo、品牌色或語氣規範（H-012），這些由設計系統建立，並經 owner 批准。

Owner 指定的視覺限制（H-012）：深色主題用灰黑色，不用藍色。

## Evidence on Hand

- 目前沒有真實客戶、客戶資料、benchmark、testimonial 或已達成的指標；owner 也還沒有可接觸的潛在使用者（H-016）。
- PRD 裡的例子（例如 2 台 ICT、3 台 FCT）只是示意，不是客戶的實際情況。原型和 UI 中的示範資料一律標為 synthetic。
- 有 Software Delivery 範例 template，pipeline 為 Source → Review → CI → Test → Deploy → Sink（T-011）；電子產線 template 尚未建立（T-051）。
- PRD §6 的工程目標不是已達成的成果，不可寫成產品宣稱。

## Product Principles

1. **人類做決定**：AI 的輸出是 proposal，風險與驗證由 server 計算後，交給人類審核、批准或拒絕。
2. **每個數字都能追溯**：結果標明 version、dataset、seed、取樣情況和統計量；不足的地方說 unavailable，不粉飾。
3. **一個拓撲，多種投影**：2D 和 3D 是同一個 IR 的不同投影，選取與 ID 共用。
4. **核心中立，領域外掛**：通用部分不寫死任何一個 domain，domain 特性來自 DomainPack。
5. **顯式勝過默默**：衝突、過期、取消、失敗都明說，並給出下一步。

## Accessibility & Inclusion

- 符合 WCAG 2.2 AA，主流程只用鍵盤就能完成，focus 永遠可見（REQ-023）。
- 由於有會議投影的情境，結果畫面的對比和字級要在投影下仍可讀，也不能只靠顏色區分 baseline 與 scenario。
- 每天長時間建模的使用者需要低疲勞的閱讀環境，因此淺色和深色主題都要支援（H-011）。
- en 和 zh-Hant 兩種語言在手機與桌機寬度下都不能跑版。
