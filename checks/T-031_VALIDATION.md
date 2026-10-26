# T-031 驗證報告（hackathon 切片）：以自然語言提出修改、AI 提案審閱、edit 與 scenario 分開批准

**日期：2026-10-26｜狀態：hackathon 切片（H-029）的 agent 實作完成，分支 `t031-hk`（自 8eecee4），沒有 push；待 coordinator 整合與獨立 review（H-029 (4)：每個切片一輪）。`planning/tasks.json` 的 T-031 維持 `not_started`：`scripts/check_decisions.py` 要求相依任務（T-015、T-018、T-026、T-027、T-028）都在 review／done 才能 `in_progress`，與 T-015、T-032、T-051 的切片相同，只在 `completion_evidence` 記錄本切片。完整 T-031 的其餘 scope（匯入、欄位對應等）未做，見「T-031 還剩什麼」。｜需求：REQ-008（needs_input、invalid 不可批准）、REQ-009（diff、assumptions、server risk、validation 可見；批准／拒絕）、REQ-010（scenario 批准不改 version、不自動執行）、REQ-022、REQ-023 的切片部分｜Gate：D-003、D-004（APPROVED）；並依 D-016、D-027、D-030、D-067–D-069（APPROVED）顯示與處理｜驗收測試：TEST-006（reject 不改 model）、TEST-007（同一 proposal 只一份 effect：UI 端只送一次、重試沿用 key）、TEST-008（scenario 批准不建 version）、TEST-028 的 UI 端（stale 也涵蓋）、TEST-031、TEST-033（截圖 NOT RUN）的切片部分**

範圍（coordinator 的指示與 tasks.json T-031 的 hackathon 切片、H-031 兩行）：

1. 模型頁上一種以自然語言提出修改的方式（文字欄位與送出按鈕，送到既有 API），以讀取 proposal 顯示其狀態（`status`：generating、needs_input、ready、invalid、failed、approved、rejected、conflicted；`mode`：edit 或 scenario），錯誤沿用既有的 problem 處理（typed code、request ID）；
2. 審閱畫面：差異（步驟、連接、參數，以人看得懂的方式，JSON Patch 放在 disclosure 後）、AI 的假設、伺服器的 validation 與 risk 與 AI 自己的判斷並列且標明誰是誰、伺服器說 needs_input 時列出問題；批准 edit 與批准 scenario 是不同的動作與結果；拒絕可附原因；base 過時（409）時保留使用者的位置並說明變了什麼，從不自動 rebase；scenario 批准後連到 `/scenarios/<id>`；
3. 批准後依 API 的回答（edit 的 `new_version_id`、scenario 的 `scenario_id`），模型頁與編輯器以重新讀取顯示結果（同一套 entity ID，介面上沒有第二份模型）；
4. 測試：以 fake API 的元件測試涵蓋每個狀態、批准／拒絕、409、needs_input、惡意的 proposal 文字（看似 prompt injection 的假設以文字呈現，從不成為 markup 或連結）、簡潔與 a11y（vitest 的 axe）；在 gallery 審閱頁上的 Playwright；en＋zh-Hant 字串；
5. 不改既有畫面的外觀（只加入口），不碰 `apps/web-e2e/tests/__screenshots__/`；新頁面的截圖列 NOT RUN；新增 gallery 審閱頁（同 `/ui-gallery/results`）含各狀態；
6. 匯入與欄位對應畫面、照片辨識（T-056）不在切片內；審閱畫面要能以同一組元件呈現 T-056 的 proposal（新增步驟與 2D 位置）。

**沒有改 contract、OpenAPI、Go 程式或 migration**：既有的 createProposal、getProposal、approveProposal、rejectProposal、getModel、getModelVersion 足夠（見「API 缺口」）。沒有新依賴（`package.json`／lock 未變）。沒有任何 live AI 呼叫，沒有讀取任何 API key 或 secret；Go 只用來執行一個暫時的、對 sample 跑伺服器 review 的測試（已移除，內容存於 evidence）。

## Commit

| Commit | 內容 |
|---|---|
| 81d56d4 | 先寫的測試與純邏輯：API client 的 `createProposal`、`getProposal`、`approveProposal`、`rejectProposal` 與 `proposals-api.test.ts`；`features/ai-proposals/changes.ts`（JSON Patch → 人看得懂的變更）與 `proposal-changes.test.ts`；實作前的紅燈 `red/red-first-run.log` |
| （第二個 commit） | 畫面：模型頁的請求（`ask-form.tsx`）、提案頁 `/proposals/[proposalId]`（`proposal-view.tsx`、`change-list.tsx`、`load.ts`、`proposal.module.css`）、gallery 審閱頁 `/ui-gallery/proposal`（`samples.ts`、`stand-in.ts`、`preview.tsx`）、編輯器預覽的請求入口、`TextField` 的 `multiline`、雙語 catalog；元件、簡潔、gallery 測試；`proposal.spec.ts` 與 helpers 的 route；DESIGN.md、ARCHITECTURE、e2e README |
| （最後一個 commit） | 本報告、evidence、tasks.json 的 completion_evidence、重產的 `checks/*.json` |

## 交付內容

| 位置 | 內容 |
|---|---|
| `apps/web/lib/api/client.ts` | `createProposal`（202 `ProposalAccepted`，必帶 Idempotency-Key）、`getProposal`、`approveProposal`（body `{expected_base_version_id}`＋key）、`rejectProposal`（`{}` 或 `{comment}`＋key）；ID 與 key 在送出前檢查（不合格不送出） |
| `apps/web/features/ai-proposals/changes.ts` | 純函式 `describeChanges(base, changes)`：依伺服器套用的方式，逐 op 以「該 op 之前的模型」讀取（index 指的是當時的 entity；`test` 是保護，不是變更），產生：新增／移除的步驟、連接、資源、班表、物件類型、指標、機率檢查；欄位的變更（名稱、容量、機率、步驟的時間或其中一個參數、批次大小與等待上限、使用的資源與數量、故障與修復時間、換線、班表、班次容量、投入速率與數量），值以模型的時間單位、百分比、「不限」或文字讀出；新增步驟的畫布位置（T-056 的照片提案會有）併入該步驟的那一行，既有步驟的位置是一行「在畫布上移動」；新增項目附上它持有的值（時間、使用的資源、批次、容量、判斷的機率）；沒有字可說的路徑以路徑與 JSON 值呈現。名稱一律取自模型與 patch，不取自 AI 的文字。op 套不上 base、或路徑含 `__proto__`／`constructor`／`prototype` 時不猜測，畫面只給確切的 JSON Patch |
| `apps/web/features/ai-proposals/load.ts` | 提案頁讀取：proposal → model 與 base version →（model 已越過 base 時）最新 version →（已批准的 edit）它建立的 version；第一個失敗為準，不顯示一半；proposal 不存在（或屬於其他 organization）時只讀那一次 |
| `apps/web/features/ai-proposals/proposal-view.tsx`、`change-list.tsx`、`proposal.module.css` | 提案頁（見下節） |
| `apps/web/features/ai-proposals/ask-form.tsx` | 模型頁的請求（見下節） |
| `apps/web/features/model-editor/model-view.tsx`、`app/models/[modelId]/page.tsx` | `ModelView` 多一個選用的 `onProposal`：有它且角色為 editor／owner 時，在編輯器上方顯示請求；route 以 `router.push('/proposals/<id>')` 前往伺服器建立的 proposal。編輯器本身沒有改 |
| `apps/web/app/proposals/[proposalId]/page.tsx` | 新 route（`RequireSession`＋`ProposalRoute`） |
| `apps/web/app/ui-gallery/proposal/` | 只在 `GEMINUS_UI_GALLERY=1` 時存在的審閱頁（否則 404）：真的 `ProposalRoute`、API client 與決定，以 editor 身分（`viewer` 狀態為檢視者）連到頁內的替身 API（`stand-in.ts`）；14 個範例狀態（`samples.ts`）；連結指向其他審閱頁 |
| `apps/web/app/ui-gallery/editor/preview.tsx`、`stand-in.ts` | 編輯器預覽的請求：替身回 202 generating，前往提案審閱頁的「撰寫中」範例（不呼叫 AI） |
| `apps/web/ui/field.tsx`、`field.module.css` | `TextField` 的 `multiline`（React Aria `TextArea`，寬 720 px、可直向調整），既有用法不變 |
| `apps/web/app/_components/states.tsx` | `Loadable` 加入 `proposal`（catalog 的 `loading.proposal`） |
| `apps/web/messages/en.ts`、`zh-Hant.ts` | 新字串 `ask.*`、`proposal.*`、`galleryProposal.*`、`loading.proposal`（兩語，鍵相同） |
| `apps/web/tests/ui-harness.tsx` | 匯出 `unmountAll()`（之前只在 afterEach 內），讓一個測試可以依序掛上模型頁、提案頁、再掛模型頁 |
| `DESIGN.md` | Simplifications in the app 加一點（見「設計的簡化」） |
| `docs/ARCHITECTURE.md`、`apps/web-e2e/README.md` | 實作位置與 e2e spec 的說明 |
| `apps/web-e2e/tests/helpers.ts`、`proposal.spec.ts` | route 清單加入 `/proposals/<id>`（未登入）與 8 個審閱頁狀態（所以 `a11y.spec.ts`、`simplicity.spec.ts` 自動涵蓋）；新的 `proposal.spec.ts`（11 個） |

## 畫面做什麼

### 模型頁的請求（editor／owner；檢視者與角色未知時不出現）

- 標題「Ask AI for a change／請 AI 提出修改」、多行文字欄位（說明：AI 依你的描述與最新版本擬出提案，批准前什麼都不會改變）、「The AI proposes／AI 提出的是」選單（A change to the model／對模型的修改＝`edit`；A scenario to simulate／要模擬的情境＝`scenario`，並在旁說明批准後的結果：成為版本 N＋1，或以版本 N 為基礎的情境、模型維持原樣），一個「Send to the AI／送給 AI」按鈕（default 樣式；Publish 仍是頁面唯一的 primary）。
- 送出：`createProposal(model.id, {base_version_id: 最新版本, mode, intent: 去頭尾空白, output_locale: 介面語言}, key)`；同一個請求重試沿用同一個 key（D-011），內容不同換新 key；空白時欄位標示錯誤、不送出；送出中以 status 區域說明並忽略重複按下。
- 成功（202）：前往 `/proposals/<proposal_id>`（API 回的 ID）。
- 失敗：以 `ProblemView` 顯示（例如 AI 未設定的 503 `AI_PROVIDER_NOT_CONFIGURED`／`AI_POLICY_NOT_APPROVED`，以 catalog 的說明與 request ID）；文字保留。409 `MODEL_VERSION_CONFLICT`（頁面載入後有人發布了新版本）時另給「Reload the model／重新載入模型」：只在使用者按下時重新讀取模型（未存的編輯照 T-014 的規則保留），再送出時用新的最新版本與新的 key；不會自動以新版本重送。
- 編輯器有未存的編輯時，送出是 blocked（`aria-disabled`），原因寫在旁邊並以 `aria-describedby` 連結（提案在另一頁開啟，離開會失去未存的編輯；DESIGN.md Buttons, Disabled）。這是會隨使用者的動作解除的條件，不是永遠停用。

### 提案頁 `/proposals/<id>`

依序：標題（「Proposed change to {模型}」或「Proposed scenario for {模型}」，依 `mode`）、來源（AI 的提案、提出時間、基於版本 N；最新版本是否仍是 N）、返回模型的連結、狀態列（`role="status"`、`data-proposal-status`），然後依狀態：

| 狀態 | 顯示 | 動作（editor／owner） |
|---|---|---|
| generating | 「AI 正在撰寫提案…」與「可以先離開再回來」；每 1.5 秒讀一次 proposal，直到不是 generating（讀取失敗時顯示 problem 與「重試」） | 無 |
| ready | 批准後的結果（建立版本 N＋1，或存成情境）；AI 的摘要；「Risk and checks」：左格 **Checked by Geminus**（Graphite Ink 框，伺服器的 `server_risk_level` 與 validation：通過所有檢查、或有 warning 時列出），右格 **What the AI said**（細線框，AI 的 `provider_risk_claim` 與信心，說明那是 AI 自己的估計、不會降低風險），其下一行「以兩者中較高的 X 風險計」（伺服器給的 `risk_level`，D-027；UI 不計算）；「Changes to version N」（人看得懂的逐行變更，Added／Removed 標籤；JSON Patch 與 patch hash 在 disclosure 後）；AI 的假設；AI 的預期（說明是預期不是結果）；「Your decision」 | edit：**Approve and create version N＋1**（primary）與 Reject；scenario：**Approve and save the scenario** 與 Reject |
| ready，但 model 已有較新版本 | 同上，但不顯示「批准後…」；決定區塊是警告 note（這個提案基於版本 N，最新是版本 M，無法批准，請回模型再問一次） | 只有 Reject（Approve 永遠不可能成功，所以不顯示，見「設計的簡化」） |
| needs_input | AI 的摘要、AI 的問題（有序清單）與「請回到模型，在新的請求中回答」、AI 的假設 | Reject（API 允許，關閉這個提案） |
| invalid | 「Geminus 拒絕了 AI 的回答」；伺服器的檢查：N 項沒有通過與每個 issue（catalog 的說明＋JSON Pointer）；「若要重試，請回到模型再提出一次」 | 無（API 不允許） |
| failed | `error` 以 `ProblemView` 顯示（error code 的說明、details、request ID 在 Details 中）；「若要重試…」 | 無 |
| approved | 審閱過的內容；edit：「它建立了版本 N」（讀取 `new_version_id` 的 version）；scenario：「Open the scenario」連到 `scenario_id`；有 comment 時顯示 | 無 |
| rejected | 審閱過的內容與原因 | 無 |
| conflicted | 審閱過的內容與警告 note：基於版本 N，批准前版本 M 已是最新；沒有套用任何變更，也不會移到版本 M；版本 M 的建立時間與 comment；「請回模型再問一次 AI」 | 無 |

**決定的流程**（DESIGN.md In-place confirmation）：按 Approve → 同一處換成問句（edit：會以版本 N 加上以上變更建立版本 N＋1，版本建立後不能修改；scenario：會存成情境，模型維持原樣，開始執行前不會執行模擬）＋確認（同名的 primary）＋Cancel，焦點移到確認；Cancel 回到原按鈕。Reject → 問句＋「Reason (optional)」多行欄位＋「Reject proposal」（danger）＋Cancel。送出中以 status 區域說明（Approving…／Rejecting…），重複按下只送一次。

**結果**：
- 批准 edit（201，`mode: edit`）：Outcome 框「Approved. Version N＋1 created.」與說明（以 API 回的 `version`）；`draft_out_of_date` 為 true 時說明模型的草稿有未發布的變更所以維持原樣、現在基於較舊版本（D-003）。焦點移到結果。
- 批准 scenario（201，`mode: scenario`）：「Approved. Scenario saved.」、模型維持在版本 N、把情境與基準一起執行；**Open the scenario** 連到 `/scenarios/<scenario_id>`（API 回的 ID）。
- 拒絕（200）：「Rejected. The model is unchanged.」與原因。
- 每個決定之後重新讀取 proposal 與 model（以及最新／新建的 version），所以狀態列（例如「Approved {時間}」）、「版本 M 是最新」都是伺服器的。
- 409 `MODEL_VERSION_CONFLICT`：頁面不換、審閱過的差異仍在原處；決定區塊換成上表 conflicted 的警告 note（以重新讀取的 model 與最新 version 說明變了什麼），焦點移到它；沒有錯誤 alert 重複說一次；不會重送、不會以新版本重新提出。
- 其他拒絕（422 `MODEL_VALIDATION_FAILED` 的 details、503 等）：`ProblemView`，提案仍可決定；再按 Approve 沿用同一個 key（D-011）。`PROPOSAL_ALREADY_DECIDED`／`LIFECYCLE_CONFLICT` 之後重新讀取，顯示伺服器現在的狀態。

**模型頁與編輯器的結果**：回到模型頁時（返回連結或任何導覽），`ModelView` 重新讀取 model 與 draft：摘要顯示新的最新版本，乾淨的草稿已移到新版本，編輯器畫的是伺服器的 IR（同一套步驟 ID）；有未發布變更的草稿顯示既有的「draft out of date」說明。UI 從不在本機套用 proposal 的 patch 當作新模型；`changes.ts` 只用 base version 讀出變更的文字。

**AI 的文字**：摘要、假設、預期、問題、patch 中的名稱，一律以 React 的文字節點呈現（`white-space: pre-line`、長字可斷行），從不經過 HTML、markdown 或自動連結；頁面上唯一的連結是頁面自己的（返回模型、批准後的情境）。

## 每個畫面的控制項（H-027：控制項 → 用途 → 需求／測試）

所有畫面共用的 app strip（Skip link、Projects、Language、Theme）不變，見 T-045 報告。

`/models/[id]`（新增的部分；編輯器的控制項見 T-015 報告，未變）：

| 控制項 | 何時出現 | 用途 | 需求／測試 |
|---|---|---|---|
| Describe the change（多行欄位） | editor／owner | 使用者的請求文字 | H-029 item 4、REQ-008；`proposal-ask.test.tsx`「sends the words…」「says what is missing…」 |
| The AI proposes（Select：A change to the model／A scenario to simulate） | 同上 | proposal 的 `mode`（API 必填，建立後不能改；D-004 的兩種 effect） | REQ-009、REQ-010；「asks for a scenario when the person chooses one…」 |
| Send to the AI（default 按鈕） | 同上；有未存的編輯時 blocked，原因在旁 | `createProposal`，前往伺服器建立的 proposal | H-029 item 4；「sends the words…」「blocks sending while the editor has unsaved edits…」、`proposal-simplicity.test.tsx` |
| Reload the model | 送出得到 409 `MODEL_VERSION_CONFLICT` 時 | 只在使用者要求時讀取新的最新版本 | AGENTS §2（不自動 rebase）；「on a 409 … keeps the words, reloads only when asked…」 |
| Details（problem 的技術細節） | 有 problem 時 | 技術訊息與 request ID | T-045 |

`/proposals/[id]`：

| 控制項 | 何時出現 | 用途 | 需求／測試 |
|---|---|---|---|
| Back to the model（連結） | 一直 | 回到模型頁，重新讀取模型（批准 edit 後看新版本） | 需求 3；`proposal-ask.test.tsx` 的閉環測試 |
| The changes as JSON Patch（disclosure） | 有 changes 時 | 確切的 patch 與 patch hash（審閱者與開發者用；H-027 漸進揭露） | 需求 2；「shows the diff in human terms… JSON Patch… behind a disclosure」、e2e「the exact JSON Patch opens from the keyboard…」 |
| Approve and create version N（primary） | editor／owner，edit，ready，base 仍是最新 | 進入原處確認 | REQ-009、D-003；「approves only after the person confirms…」 |
| Approve and save the scenario（primary） | editor／owner，scenario，ready，base 仍是最新 | 進入原處確認 | REQ-010、D-004；「saves a scenario and links to it…」 |
| 確認的 Approve…（primary，同名）／Cancel | 確認中 | 送出 `approveProposal`／回到原按鈕 | 「approves only after…」「goes back without a request…」「sends one approval when … pressed twice」 |
| Reject（danger） | editor／owner，ready 或 needs_input | 進入原處確認 | REQ-009、TEST-006；「rejects with the reason…」 |
| Reason (optional)（多行欄位）／Reject proposal（danger）／Cancel | 拒絕確認中 | 送出 `rejectProposal`（原因可省略） | 需求 2；「rejects with the reason…」「rejects without a reason as an empty body」 |
| Open the scenario（連結） | 批准 scenario 之後，或已批准的 scenario | 前往 `/scenarios/<scenario_id>` 執行（T-032） | 需求 2；「saves a scenario and links to it…」 |
| Try again | 讀取或輪詢失敗時 | 重新讀取 | 沿用 `RemoteView` 的規則 |
| Details（problem 的技術細節） | 有 problem 時 | 技術訊息與 request ID | T-045 |

檢視者與角色未知：只剩返回連結與 JSON Patch disclosure，並說明不能批准或拒絕。

`/ui-gallery/proposal`（審閱工具，`GEMINUS_UI_GALLERY=1` 時才存在）：上述全部，加上 14 個「範例提案」連結（每個一個狀態），與 `/ui-gallery/editor` 一樣是給 owner 審閱與瀏覽器檢查用；返回連結指向 `/ui-gallery/editor`、情境連結指向 `/ui-gallery/results`。

沒有加入：proposal 列表、在畫布上畫出 proposal（Review 模式）、「Edit first」（先修改 proposal 再批准）、「Redo on vN」、批准的 comment 欄位、伺服器 risk 的原因（API 沒有，見「API 缺口」）。

## 設計的簡化（DESIGN.md「Simplifications in the app」新增一點）

- 請求是模型頁上的一個表單；審閱是獨立頁面（在畫布有 Review 模式之前，與 T-032 的結果頁相同的做法）：沒有側欄、不畫畫布。
- 風險兩格：Geminus 的檢查（Graphite Ink 框）與 AI 的說法（細線框），其下為計入的風險；變更是細線分隔的列，Added（Graphite Ink 標籤）、Removed（Error Red 標籤），4 px 圓角（token 沒有膠囊的圓角）。
- 只有 Approve 與 Reject；沒有「Edit first」與「Redo on vN」（再問一次就是模型頁的那一個請求）。
- base 不再是最新的 ready 提案永遠不能批准，所以**不顯示** Approve（prototype 是 blocked 加原因），以警告 note 說明（H-027：沒有永遠停用的控制項）。這是本切片在測試先改、再實作的決定（`red/red-stale-not-offered.log`）。
- 結果用 Outcome 框，沒有綠色勾號（還沒有 icon）。

## API 缺口

API 本身足夠，沒有改 contract。觀察到、影響畫面但不在本切片處理的：

1. **getProposal 不回傳使用者的請求（intent）**：prototype 的審閱畫面有「Request」；本切片只能顯示 AI 的摘要。要顯示請求需在 Proposal resource 加欄位（contract 與 Go 的變更），列為 owner 問題。
2. **伺服器 risk 只有等級，沒有原因**：prototype 有「High because it adds a resource pool…」；API 沒有，所以畫面不說原因。
3. **AI edit 的 allowlist 沒有 `views`**：T-056 的照片提案若帶 2D 位置，伺服器會判為 invalid（`PATCH_PATH_FORBIDDEN`）。以伺服器的 review 驗證過：`new-steps` 範例帶位置時 invalid，去掉位置後 ready、medium（`sample-server-review.log`）。畫面已能呈現帶位置的新增步驟；是否讓 AI edit 寫入 `views` 要由 T-056／owner 決定。
4. `decided_by` 只是 user ID，沒有名稱，所以畫面不寫「誰」批准。

## 測試

| 檔案 | Tests | 內容 |
|---|---|---|
| `proposals-api.test.ts` | 7 | 4 個方法的 method、path、header、body、key；與 `planning/api-operations.json` 一致；409／422／503 的對應（code、request ID、details）；不合格的 ID 與 key 不送出 |
| `proposal-changes.test.ts` | 10 | 批次（兩個值、略過 test）、資源容量、機率（百分比）、等待上限（時間單位、移除＝缺）、時間參數與整個分布（shape 無單位）、佇列容量移除、名稱、模型名稱；新增步驟（含位置與持有的值：時間、資源依 patch 之後的名稱、批次）、新增連接（判斷的機率）、新增資源、新增佇列（不限）、既有步驟的位置；移除依移除前的名稱、之後的 index 依位移後的模型、已移除步驟的名稱取 base；沒有字的路徑；惡意的名稱照原樣是文字；套不上 base、`__proto__`、`constructor` 不猜；base 不被改變 |
| `proposal-view.test.tsx`（happy-dom，真 client＋替身 API） | 27 | ready：兩格標明誰是誰、伺服器 medium／AI low／信心 90%／計入 medium、通過檢查、axe；差異的人話與 JSON Patch／hash 的 disclosure；假設與預期；批准只在確認後、body 為 base、有 key、結果、重新讀取、焦點、axe；Cancel 不送出且焦點回原按鈕；連按兩次只送一次；草稿過時的說明；503 後重試同一 key；422 details；scenario：AI 的 claim 較高時計入 AI 的（D-027）、確認問句、連結為 API 的 `scenario_id`、沒有新 version、axe；拒絕附原因與空 body；409：頁面保留、說明版本 1→2 與其 comment、沒有錯誤 alert、焦點、沒有重送；過時：不顯示 Approve、警告 note、沒有 aria-disabled；已 conflicted；generating→ready（只有 GET，不決定）；needs_input；invalid；failed（problem 與 request ID）；approved（建立的版本）；rejected（原因）；檢視者；新增步驟（T-056）的行與持有的值；zh-Hant；不存在時只讀一次；惡意文字（不成為 img／script／iframe／連結，只有返回連結，不決定，伺服器 high 仍在）；值的格式（分鐘、百分比、不限，en 與 zh-Hant；套不上時只給 patch） |
| `proposal-ask.test.tsx`（真 `ModelView`＋替身） | 9（原寫 8，review 第 1 輪 I2 更正：含閉環） | 表單的元件、Publish 仍是唯一 primary、axe；空白不送出；送出的 body（最新版本、去空白、output_locale）與 key、前往 API 回的 ID；選 scenario、zh-Hant 的 output_locale、axe；AI 未設定的 503、文字保留、重試同一 key；409、只在要求時重新載入、以新版本與新 key 再送；有未存編輯時 blocked 且原因在旁、按下不送出；檢視者／角色未知不出現；**閉環**：模型頁送出 → 提案頁 ready → 批准 → 再開模型頁：版本 2、步驟 ID 與 base 相同、燒機的最小／最大批量為 48（編輯器的屬性面板，伺服器的 IR） |
| `proposal-simplicity.test.tsx` | 33 | 兩語 × 14 個狀態（含 app shell）：沒有同名按鈕／連結、沒有兩個連到同處、至多一個 primary、沒有未說明原因的停用控制項，**審閱頁沒有任何停用的控制項**；確認、三種結果、409、503 之後；模型頁：乾淨、AI 被拒、409、未存編輯（Send blocked 且有原因）；檢查本身能失敗 |
| `proposal-preview.test.tsx` | 19 | 審閱頁預設 404、只接受自己的狀態；範例合乎 contract（每個狀態都有、patch 套得上版本 1、risk＝max、needs_input 沒有 changes、invalid／failed 沒有內容、只有已決定的有 decided_at）；每個 patch_hash 是 RFC 8785 形式的 SHA-256；14 個狀態與兩個 zh-Hant 狀態 axe |
| `design-rules`、`tokens`、`server-only`、`i18n`、`lint-guards`（既有） | — | 自動涵蓋 `features/ai-proposals` 的 CSS 與原始碼、兩語 catalog 的鍵與 placeholder |
| `apps/web-e2e/tests/proposal.spec.ts` | 11 | 只用鍵盤批准 edit（原處確認、焦點可見、結果被聚焦與宣告、axe）；批准 scenario 與其連結；兩語以鍵盤拒絕並輸入原因；409 保留頁面並說明；generating→ready；從編輯器預覽送出並到達提案；惡意文字（沒有 img／script／iframe、`article` 內唯一的連結是返回、axe）；390 px 開啟 JSON Patch 不造成橫向捲動；兩語：每個字串是 catalog 或資料 |
| 既有 e2e spec | 見下 | `a11y.spec.ts`（9 個新 route × 2 主題 × 2 語言 × 2 寬度，及選定主題）、`simplicity.spec.ts`（9 個新 route × 2 語言 × 2 寬度）、`strings.spec.ts`（編輯器預覽上的請求表單） |

**先紅後綠**：`proposals-api.test.ts`、`proposal-changes.test.ts`、`proposal-view.test.tsx`、`proposal-ask.test.tsx`、`proposal-simplicity.test.tsx`、`proposal-preview.test.tsx` 在實作前執行並失敗（`red/red-first-run.log`：模組不存在、client 沒有方法）。之後的三個改動也是先改測試（紅）再改程式（綠）：新增項目持有的值（`red/red-added-holds.log`、`red/red-holds-view.log`）、過時時不顯示 Approve（`red/red-stale-not-offered.log`）。測試本身的錯：`domain_attributes` 不存在時 JSON Patch 要用 `add` 而非 `replace`（測試修正）；`i18n.test.ts` 抓到 en 與 zh 相同的 `{value} {unit}`（改用 `Intl` 的時間單位格式）；simplicity 測試少了 `next/navigation` 的 mock。

### Mutation（`mutants.txt`、`mutants-M6-rerun.txt`；`scripts/mutants.py`）

畫面在第一次實作就通過了測試，所以以 21 個手寫 mutant（每個只改一處，跑對應的測試檔，之後還原）證明測試擋得下缺陷：第一輪 20 個被擋下、1 個存活。存活的 M6（409 只顯示成一般 problem）：因為重新讀取後 proposal 是 conflicted，警告 note 仍會出現，差別只在多一個重複的錯誤 alert → 在 409 的測試加上「note 就是 409 的回答，旁邊沒有錯誤 alert」，重跑 M6 被擋下。被擋下的包括：不經確認就批准、重試換 key、伺服器格顯示 AI 的 claim、計入的風險只取伺服器的、摘要以 HTML 呈現、過時仍顯示 Approve、空原因也送 comment、generating 不再讀取、檢視者可以決定、情境連結用 proposal ID、決定後不重新讀取、AI 文字固定英文、忽略未存編輯、重試換 key、送出空白、逐 op 改為都對 base 讀、讀取 prototype 成員、時間不帶單位、佇列容量移除寫成「未設定」、`test` 被列成變更。

## 範例資料的來源與檢查

`app/ui-gallery/proposal/samples.ts` 的提案是**手寫的範例**（沒有呼叫 AI），建在編輯器預覽的範例產線（`sample-line.ts`，synthetic）上。伺服器的部分以伺服器自己的程式核對：

- **伺服器 review**（暫時的 Go 測試，`scripts/zz_t031_samples_test.go.txt`，執行後移除；`sample-server-review.log`）：以 `internal/ai/review.Review`＋電子產線 pack，把 ready、scenario、hostile 範例當作 provider 的回答跑一次：狀態（ready）、server risk（medium、low、high）、計入的 risk（medium、medium、high）與 patch hash 都與範例寫的相同；`new-steps` 因為畫布位置被判 invalid（見 API 缺口 3），去掉位置後 ready、medium。惡意文字的範例通過伺服器 review（伺服器不過濾散文），所以畫面以文字呈現是必要的。
- **JSON Schema**（`scripts/validate_samples.py`、`sample-schema-check.log`，資料由 `scripts/zz-dump-samples.test.ts.txt` 產生）：每個範例 proposal（含 generating 的答案、替身批准後與 409 之後的 proposal）符合 `proposal.schema.json`，替身的兩種 Approval 符合 `http.schema.json#/$defs/Approval`，base 與每個 candidate IR 符合 `model-ir.schema.json`。
- 替身（`stand-in.ts`）只做伺服器在這些流程中的記帳（version、草稿、proposal、scenario、409、已決定、key 的重放），**不**呼叫 AI、不 review、不驗證模型、不算風險；頁面上明寫「不會呼叫 AI，批准或拒絕只會改變這個頁面」。**不可**拿審閱頁當 demo 中「伺服器驗證」或「AI 回答」的證據。

## 指令與結果（Node 22.22.0；`npm ci --engine-strict=false`，repo 要求 Node 24）

| 指令 | 程式 | 結果 |
|---|---|---|
| `npm ci --prefix apps/web --engine-strict=false`、`npm ci --prefix apps/web-e2e --engine-strict=false` | — | exit 0、exit 0（lock 未變） |
| `make frontend-test`（lint、typecheck、vitest） | 0a27426 | exit 0：40 個檔案、875 個 test（`frontend-test-3.log`；第一次 exit 2 是 i18n 測試抓到 en／zh 相同的字串，`frontend-test-1.log`，修正後 `-2` exit 0） |
| `make frontend-build` | 0a27426 | exit 0；新 route `/proposals/[proposalId]`、`/ui-gallery/proposal`（`frontend-build.log`） |
| `npx playwright test tests/proposal.spec.ts` | 0a27426 之前（含全部畫面；scenario 範例的 AI claim 改為 medium 之前） | exit 0：11 passed |
| `make e2e-test`（完整） | 實作中的 tree | **被我停止**（為了在最後的 commit 上重跑）：停止前 231 passed、0 failed（`e2e-test-1-stopped.log`）；之後 port 被另一個 agent 的 e2e 佔用，**0a27426 上的完整 `make e2e-test`：NOT RUN** |
| `npm audit --prefix apps/web --omit=dev` | — | exit 0，0 vulnerabilities（`npm-audit.log`） |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | T-031 維持 `not_started` | exit 0；重產的 `checks/docs-check.json` |
| Mutation（`scripts/mutants.py`） | — | 21 個：第一輪 20 killed、1 survived（M6）→ 補測試後 M6 killed（`mutants.txt`、`mutants-M6-rerun.txt`；後者只重跑 M6，結尾的「total 21」是腳本 mutant 表的大小，不是那次執行的數量：那次執行 1 個、1 killed，review 第 1 輪 I2） |
| 範例的 JSON Schema（`scripts/validate_samples.py`） | — | exit 0（`sample-schema-check.log`） |
| 範例的伺服器 review（暫時的 Go 測試，已移除） | — | exit 0（`sample-server-review.log`） |

## 本機預覽截圖（不是 baseline）

`checks/evidence/2026-10-26-T-031/local-shots/`：18 張，以本機 `next start`（port 3210，`GEMINUS_UI_GALLERY=1`，`make frontend-build` 的 build，0a27426 的程式）與本機 Chromium 拍攝（`scripts/shots.mjs`）：審閱頁 ready 的 en／zh-Hant × 1280／390 × 淺色／深色（8 張）；新增步驟、批准 edit 後的結果、409 後保留的頁面、模型頁的請求，各 en 1280 淺色與深色（8 張）；zh-Hant 的 scenario 批准結果（1280）與 needs_input（390）。每張的頁面 `scrollWidth` 等於視窗寬度（`local-shots/scroll-widths.txt`，沒有橫向捲動）。**這些不是 baseline**：`apps/web-e2e/tests/__screenshots__/` 沒有被改動，新頁面也不在 `screenshots.spec.ts` 中。

## NOT RUN／限制

| 項目 | 原因 |
|---|---|
| 截圖比對（TEST-033）與新頁面的 baseline | 依指示不新增、不修改 baseline；`/ui-gallery/proposal` 與 `/proposals/<id>` 不在 `screenshots.spec.ts`；本機預覽截圖見上（不是 baseline） |
| 已登入、有真實資料的頁面在瀏覽器中 | app 尚未掛 `AuthProvider`（另一個 agent 進行中的 T-043 後續），瀏覽器中 `/models/<id>` 與 `/proposals/<id>` 都是未登入；已登入的行為以 vitest（替身）與審閱頁（替身）檢查 |
| 與真的 Go API／worker／provider 的閉環（請求 → worker 以 fake provider 產生 → 審閱 → 批准 → 執行） | 前端只用替身；後端已由 T-025／T-026／T-028 的整合測試涵蓋；瀏覽器端到端閉環屬 T-040 |
| live AI（Claude） | 依指示不呼叫；本切片也不需要 |
| `make backend-test`、`backend-race`、`integration-test` | 沒有改 Go、schema、OpenAPI 或 migration；只執行一個暫時的 Go 測試（已移除）核對範例 |
| Node 24、Firefox、Safari、螢幕報讀器 | 本機只有 Node 22；P0 只跑 Chromium；螢幕報讀器需要人 |
| push 與 CI | 依指示不 push |
| 獨立 review | H-029：每個切片一輪，待 coordinator 安排 |

## T-031 還剩什麼（切片以外）

- 匯入：上傳、預覽、無效列與下載、逐一確認的欄位對應、commit（REQ-007、TEST-013），以及從匯入推論的 proposal；
- 模型的 proposal 列表（`listProposals`）與模型頁上「等待審閱」的數字；generating 的提案只能以網址回到；
- 在畫布上畫出 proposal（DESIGN.md 的 Review 模式：82% 的畫布、Added／Removed 標在節點與連接上），與 2D／3D 共用選取；
- 「Edit first」（先修改 proposal 的值再批准，prototype 有）與批准的 comment；
- 請求文字與伺服器 risk 原因的顯示（API 缺口 1、2）；
- SSE 或較省的輪詢（目前每 1.5 秒讀一次）；
- 已登入的瀏覽器 E2E、TEST-028 的完整閉環、截圖 baseline。

## 需要 owner 決定或知悉

1. **T-031 的狀態**：維持 `not_started`，切片記在 completion_evidence（與 T-015、T-032、T-051 一致）。若要改 `in_progress` 需要相依任務先到 review／done，或在 check 中加入有紀錄的例外。
2. **審閱是獨立頁面**，請求在模型頁編輯器上方（DESIGN.md 新增的簡化）。可以嗎？或要等畫布的 Review 模式？
3. **過時的 ready 提案不顯示 Approve**（prototype 是 blocked 加原因）。理由：它永遠不可能成功，H-027 不要永遠停用的控制項。可以嗎？
4. **mode 的選擇**用 Select（預設「對模型的修改」），而不是兩個送出按鈕。可以嗎？demo 要先示範哪一個？
5. **請求文字**（intent）目前 API 不回傳，審閱頁只顯示 AI 的摘要。要不要在 Proposal resource 加上（contract 與 Go 的變更）？
6. **T-056 的畫布位置**：AI edit 的 allowlist 沒有 `views`，帶位置的照片提案會是 invalid。T-056 要去掉位置（畫布用 layered 預設版面），或是要一個決策讓 AI edit 寫入 `views`（PROPOSED，不在本切片批准）？
7. **資料說明**：請求欄位的說明只寫「AI 依你的描述與版本 N 擬出提案」。是否要寫明文字會送到 AI 服務（Claude）、只放 synthetic 資料（H-018 的資料政策）？
8. **Demo 仍需要已登入的 app**（另一個 agent 進行中）：在那之前，部署後的模型頁與提案頁都只顯示「需要登入」。
9. **新頁面的截圖**：要不要把 `/ui-gallery/proposal` 加入 `screenshots.spec.ts`，由 CI 產生 baseline 給你確認？

## 殘餘風險

- 審閱頁的範例與替身不是伺服器；範例的伺服器部分已用伺服器程式核對，但替身的流程（例如 key 的重放、草稿的移動）是依 contract 手寫的，真實行為以 T-026 的測試為準。
- `changes.ts` 對沒有字可說的路徑以路徑與 JSON 顯示（例如 `domain_attributes`、`metadata`）；新增的 schema 欄位需要補字。
- AI 的散文語言依建立時的 `output_locale`，介面語言之後改變時，AI 的文字仍是原來的語言；Proposal 沒有回傳 `output_locale`，所以畫面無法標 `lang`。
- 輪詢間隔固定 1.5 秒；長時間開著 generating 的頁面會一直讀。

## Coordinator 驗證（2026-10-26）

`t031-hk` 的三個 commit（81d56d4、0a27426、84a6143）cherry-pick 到 T-053 的修正之後（fb19e95、e7d9d2c、f272ffc）；衝突：`apps/web/app/_components/states.tsx` 的 `Loadable`（合併 T-043 的 `account` 與本切片的 `proposal`；catalog 兩邊的 key 都在）與生成的 `checks/docs-check.json`（重產）。提案頁用 T-043 後續的 `RequireSession` 與 `useAuth`，已登入的 app 會給它組織與角色。

指令與結果（evidence：`checks/evidence/2026-10-26-T-053-T-031-coordinator/`，`exit-codes.txt` 依執行順序；HEAD f272ffc；Node 22.22.0，go1.27.3）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test GOFLAGS=-count=1` | 2 | 只有 `scripts/check-architecture` 在 Go 預設的 10 分鐘逾時（`TestRunExitCodes` 執行中；共用的 4 CPU，load 15–20）；其餘 package 都 ok。這個 package 平常要 461–575 s（T-039、T-051／T-032 的 log），本次兩個切片都沒有改它 |
| `go test -count=1 -timeout 30m ./scripts/check-architecture/...`（重跑） | 0（197 s，load 3.6） | `check-architecture-rerun.log` |
| `make architecture-check` | 0 | |
| `make ai-eval` | 0 | fake provider |
| `go test -race -count=1`（ai、aievals、proposals、cmd/ai-eval-live） | 0 | |
| `go test -fuzz FuzzDecode -fuzztime 60s`（anthropic） | 0 | 277,761 次，沒有失敗 |
| `make integration-test`（新的 dt_test_hk531c，跑完刪除） | 0 | |
| `npm ci`（web、web-e2e） | 0／0 | |
| `make frontend-test` | 0 | 970／970 |
| `make frontend-build` | 0 | |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| `make e2e-test`（等 3100／3101 空出後單獨執行） | 0 | 490 passed、32 skipped（全部是只在 CI 比對的截圖） |

NOT RUN：任何 live 呼叫；截圖比對（只在 CI）；完整的 `make backend-race`；Node 24。

Coordinator 對本切片問題的處理：
1. 狀態維持 `not_started`，切片紀錄在 completion_evidence（與 T-015、T-032、T-051 一致）。
2. 審閱是獨立頁面、提問表單在編輯器上方：接受（與 T-032 結果頁的做法一致，符合簡潔原則）。
3. 過時的 proposal 隱藏 Approve 並寫出原因：接受（符合「沒有原因就不停用」）。
4. 以一個 Select 選 edit／scenario，而不是兩個送出鈕：接受（控制項較少）。
5. 審閱頁顯示使用者的原始請求需要 contract 與 Go 的改動：比賽後。
6. T-056 的 proposal 不帶 2D 位置（AI 編輯的 allowlist 沒有 `views`），新步驟由編輯器的 layered 版面排列：不需要新的決定（已寫入 T-056 的 H-033 一行）。
7. 請求欄位加一句「內容會送到 AI，只放 synthetic 資料」（H-018）：在 T-057 加入。
8. 伺服器的風險理由：API 只給等級，比賽後。
9. 新頁面的截圖 baseline：T-057。
H-033 之後 provider 為 Meta（Muse Spark，T-055），Claude 為備援；UI 不依 provider 而變。

下一步：一輪獨立審查（H-029）。

## Review round 1 fixes（2026-10-26）

依獨立審查第 1 輪（`checks/T-031_REVIEW_r1.md`，另一個 worktree，CHANGES REQUESTED：M1；L1–L5；I1–I7）修正。分支 `t031-r1`（自 5b66648），沒有 push。**沒有改 contract、OpenAPI、schema、Go 程式或 migration，沒有新依賴**（兩個 `package.json`／lock 未變；`apps/web/eslint.config.mjs` 多一條規則）。沒有任何 live AI 呼叫，沒有讀取 key 或 secret。T-031 在 `planning/tasks.json` 仍是 `not_started`（理由同上），本節沒有改它。Evidence：`checks/evidence/2026-10-26-T-031-r1/fixes/`。

### Commit

| Commit | 內容 |
|---|---|
| 18d1ca6 | M1、L1–L4、I1、I3 的程式與測試：`shown.ts`、`words.tsx`（新）、`changes.ts`、`change-list.tsx`、`proposal-view.tsx`、CSS、兩語 catalog、`react/no-danger`；hostile 範例的 patch 帶惡意名稱；DESIGN.md；紅燈 log 與範例的重新核對 |
| e8d7381 | I3 的測試（error code 為 `toString`／`constructor`／`__proto__`）與 mutant 腳本 |
| db77bfe | L5：e2e fake API 的 proposal routes、`fake/proposal-answer.json`、`proposal.spec.ts` 已登入的流程與 hostile 名稱的瀏覽器檢查；e2e README、ARCHITECTURE；I2；mutant 結果 |
| 06ec164 | `proposal.spec.ts` 的字串檢查把範例產線自己的 ID 算作資料（e2e 第 1 次的發現，見下） |
| （本節最後的 commit） | 本節與最後的 evidence（frontend-test／build、兩次 e2e、audit、spec-check） |

### 逐項：發現 → 變更 → 測試 → mutant

| 發現 | 變更 | 測試 | Mutant |
|---|---|---|---|
| **M1 (a)** 同名的不同 entity 看起來一樣 | `changes.ts`：每個被提到的 entity 是 `Ref`（ID、名稱、`showId`）。在 base、每個 op 之後與 patch 最後的模型中，同類（步驟、資源、班表、物件類型）有兩個以上 entity 的名稱「讀起來相同」（`shown.ts` `nameKey`：NFKC、大小寫、空白與不可見字元不計），或這個 patch 新增的 entity，`showId` 為真；兩條連接的兩端相同時連接也帶 ID。`change-list.tsx` 以 catalog 新的 `proposal.named`「{name} ({id})」／「{name}（{id}）」顯示（與 `organization.named` 相同的規則），參照欄位（需要的資源、連接的兩端、班表、物件類型、使用量、換線）也一樣；沒有名稱的步驟本身就以 ID 顯示，不重複 | `proposal-changes.test.ts`（新 describe 6 個：審查者的 ICT tester 情境、結尾零寬空白、base 中兩個同名資源、改名造成同名、步驟與無名步驟、平行連接）；`proposal-names.test.tsx`（DOM：ICT tester 情境 en 與 zh-Hant、零寬空白、同名不同 ID）；`proposal-view.test.tsx` 的新增步驟（T-056）現在帶 ID | M1-no-ids、M1-new-not-shown、M1-exact-names、M1-flow-ids、M1-key-case：都 killed |
| **M1 (b)** 雙向控制與不可見字元沒有顯示、沒有隔離 | `shown.ts` `visible()`：名稱與值中的控制字元（含 tab、換行）、`\p{Cf}`（U+061C、U+200B–U+200F、U+202A–U+202E、U+2060–U+2064、U+2066–U+2069、U+FEFF、U+00AD、tag 字元等）、孤立的 surrogate、U+2028／U+2029、default-ignorable（填充字元、variation selector、CGJ）、U+2800 與 U+0020 以外的空白，一律寫成看得見的「⟨U+202E⟩」；AI 的散文（摘要、假設、預期、問題）與人的 comment 用 prose 模式（保留換行、tab、空白，其餘相同）。`words.tsx`：每一行由 catalog 的文字與資料組成，資料（名稱、ID、路徑、文字與 JSON 值）各自是 `<bdi>` 中的文字節點；模型名稱在 h1 也一樣。JSON Patch disclosure 以 `patchJson()` 把字串中的這些字元寫成 `‮` 等 JSON escape（值不變，`JSON.parse` 相同） | `proposal-shown.test.ts`（8 個：每類字元、保留的字元、prose、`nameKey` 兩個、`readsAlike`、ASCII JSON、patch JSON）；`proposal-names.test.tsx`（11 個）：U+202E 改名、零寬空白、希伯來文名稱，斷言 DOM 中沒有任何原始的 `\p{Cf}`／`\p{Cs}`／`\p{Zl}`／`\p{Zp}`／控制字元，且每個名稱與值在自己的 `<bdi>` 中；hostile 範例在 vitest 與瀏覽器中同樣檢查 | M1-raw、M1-no-bdi、M1-patch-raw、M1-prose-raw：都 killed |
| **M1 (c)** 兩側仍相同時讀成「X → X」 | `change-list.tsx`：一行變更的前後顯示（NFKC 後）相同時，值真的相同（canonical JSON 相等）→ `proposal.change.same`「{field}: stays {value}; the proposal sets the value it already has」；值不同 → `proposal.change.exact` 以確切的 JSON 顯示兩側，若仍讀起來相同（例如 é 與 e＋U+0301）則以 ASCII escape 的 JSON 顯示 | `proposal-names.test.tsx`：同值、四捨五入後相同的百分比（0.95 → 0.950001）、NFC／NFD 的改名 | M1-no-same、M1-no-ascii：killed |
| **L1** patch 名稱以文字呈現沒有 DOM 測試 | 頁面層級測試；`eslint.config.mjs` 對 `**/*.tsx`（不含 tests）開 `react/no-danger` | `proposal-names.test.tsx`（`ProposalRoute`：diff 中帶 markup、`javascript:` 與 U+202E 的名稱、摘要、假設、預期；needs_input 的問題）；hostile 範例的 patch 現在帶這些名稱（`proposal-view.test.tsx`、`proposal.spec.ts`）；`lint-guards.test.ts` 證明規則會觸發 | 審查者的 MA（加在現在的對應位置：新增／移除行的 subject，`MA-subject`）、MA（共用的 `<bdi>`）、MA-assumptions、MA-questions：都 killed，四個的 eslint 也都 exit 1（`react/no-danger`） |
| **L2** 409 之後不重送沒有斷言 | 測試 | `proposal-view.test.tsx` 的 409 測試：只有一個批准請求，body 是原本的 base，再等 100 ms 仍只有一個 | 審查者的 MB：killed |
| **L3** 拒絕的重試沿用 key 沒有測試 | 測試 | 503 之後以同一原因再拒絕：同一個 key；改了原因：新 key | 審查者的 MC：killed |
| **L4** 422 或風險改變的 409 之後仍提供 Approve | `proposal-view.tsx`：422 `MODEL_VALIDATION_FAILED`，或 409 `LIFECYCLE_CONFLICT` 且 details 的 path 是 `/risk_level`／`/server_risk_level`（`internal/proposals/approval/http.go` 的寫法），視為伺服器重新檢查後拒絕（D-069 (b) B1：proposal 維持 ready、這樣的批准永遠不會成功）：不再提供 Approve、不顯示「批准後…」與草稿提示，在 Approve 原本的位置顯示 warning note（`proposal.refused.checks`／`proposal.refused.risk`：為什麼，以及請拒絕後回模型再問一次），焦點移到它；Reject 保留；伺服器的 problem（含 details 與 request ID）照常顯示；503 仍可重試同一 key。DESIGN.md 的簡化一點同步 | `proposal-view.test.tsx`：422（原本斷言「仍有 Approve」改為不提供，並斷言 note、焦點、沒有停用的控制項、axe、之後可拒絕）、兩種 risk path 的 409（note、伺服器的理由、狀態仍 ready、重新讀取、只一個批准）；`proposal-simplicity.test.tsx` 兩語 × 兩種拒絕 | L4-never、L4-no-risk、L4-approve-shown、L4-focus：killed |
| **L5** 已登入的頁面沒有在瀏覽器中走過 BFF | `apps/web-e2e/fake/servers.mjs`：Line Works 的 proposal routes（createProposal 202 generating；getProposal 第一次讀為 generating、之後為 ready；approveProposal：edit 從最新版本與 patch 建下一版並移動乾淨的草稿、scenario 給 scenario ID；rejectProposal；idempotency key 同請求重放、不同請求 409；viewer 403；其他 organization 404）；固定的回答在 `fake/proposal-answer.json`（依 `output_locale` 兩語；是為測試寫的，不是 AI 的回答，也不是伺服器的 review）；`POST /__reset`（:3101）還原租戶資料；API 收到的 POST 記下 key 與 body。新檔在 `apps/web-e2e/fake/` 之下，`deploy/local/compose.yaml` 已整個掛載 `/fake`，所以 compose 不必改（`make spec-check` 的 `check_deploy.py` 通過） | `proposal.spec.ts`「signed in, through the BFF, on the fake API's proposals」：en 與 zh-Hant 各一：以 editor 經 BFF 與測試 provider 登入、模型頁用鍵盤輸入並送出、到 `/proposals/<id>`、讀到 ready、差異「Reviewer Capacity: 2 → 3」、AI 的摘要與假設、伺服器格、鍵盤 Approve → 原處確認（焦點）→ 結果（焦點、版本 2）、API 收到的兩個 POST（body、key、`X-Organization-ID`、沒有 cookie）、Shift+Tab 回模型頁：最新版本 2、草稿 revision 4；每一步 axe；另一個以原因拒絕、模型仍是版本 1 | —（瀏覽器測試：兩次 e2e 都通過，見下） |
| **I1** gallery 預覽的註解過時 | `preview.tsx` 註解改寫；同樣過時的 `proposal.spec.ts` 與 `proposal-simplicity.test.tsx` 開頭註解也改 | — | — |
| **I2** 報告的小誤差 | 本報告測試表的 `proposal-ask.test.tsx` 改為 9；Mutation 一列註明 `mutants-M6-rerun.txt` 的「total 21」是 mutant 表的大小、那次只跑 M6（1 killed）。舊 evidence 檔案未改；本輪的腳本 `mutants_r1.py` 印出它實際執行的數量 | — | — |
| **I3** `failureProblem` 以 `in` 查 code | `Object.hasOwn(errorCodeStatus, code)` | `proposal-view.test.tsx`：code 為 `toString`、`constructor`、`__proto__` 的 failed proposal 是 protocol problem，訊息是「The proposal failed without an API error.」 | I3（改回 `in`）：killed |

Mutation：`fixes/mutants.txt`（`fixes/scripts/mutants_r1.py`，每個 mutant 只改一處、跑指定的測試檔、還原並以 `git diff --quiet` 確認）：**22 個，22 killed，0 survived**（審查者的 MA、MB、MC 都在其中；MA 的原位置 `change-list.tsx:111` 已重寫，對應到 `MA-subject` 與共用 `<bdi>` 的 `MA`）。

### 現在的行為

- 差異的每一行：catalog 的文字＋資料；資料是 `<bdi>` 中的文字節點，看不見或會重排文字的字元顯示為 ⟨U+XXXX⟩；DOM 中不會出現原始的雙向控制、零寬或其他格式字元。審查者的情境現在讀成「Added Resource ICT tester (resource_01m4ydqk00e1ectr0n1c5x0009)」（其下 Capacity: 9）與「ICT Resource it needs: “ICT tester” (ict_tester) → “ICT tester” (resource_01m4ydqk00e1ectr0n1c5x0009)」；U+202E 的改名讀成「“Burn-in” → “Burn-in⟨U+202E⟩84 → 02”」。
- 同名（含只差大小寫、全形、空白或不可見字元）或新增的 entity 附 ID；新增的步驟、資源也因此附 ID（T-056 的照片提案會比較長）。
- 兩側讀起來仍相同的變更明寫「維持原值」或以確切值（必要時 ASCII escape）顯示。
- 伺服器在批准時重新檢查而拒絕（422、風險改變的 409）之後只剩 Reject，原因在原處；重新整理頁面後（伺服器仍是 ready）Approve 會再出現，再按一次會再得到同樣的拒絕與說明（伺服器沒有記錄這次拒絕，D-069 B1）。
- 已登入的 demo 路徑（模型頁 → 提案頁 → 批准 → 版本 2）在瀏覽器中經 BFF 走過，fake API 是固定答案。

### 先紅後綠

- `fixes/red/red-first-run.log`：實作前，6 個測試檔 131 個 test 中 28 個失敗（`shown.ts` 不存在、`changes.ts` 的新形狀、ID、⟨U+…⟩、`<bdi>`、same／exact、L1 頁面測試、L4 的 simplicity、`react/no-danger`），`proposal-shown.test.ts` 與 `proposal-view.test.tsx` 在 import 就失敗。
- `fixes/red/red-view-before-fix.log`：因為上一個 log 中 `proposal-view.test.tsx` 沒有執行，另以修正前的 `proposal-view.tsx`（`git show 5b66648:…`，跑完還原並以 `cmp` 確認）跑三個檔：76 個中 7 個失敗：L4 的三個、兩語 simplicity、L1 的兩個（散文中的 U+202E 原樣進入 DOM）。L2、L3 的測試在修正前就通過（程式本來就對），由 MB、MC 證明它們擋得下。
- 測試本身的錯一處：希伯來文的 metadata 值是文字（引號＋`<bdi>`），我原本寫成 JSON，改正期望值。
- `fixes/green-proposal-tests.log`：10 個相關檔 224 個 test 全過。

### hostile 範例的重新核對

hostile 範例的 patch 改為：原本移除 scrap 的 6 個 op，加上新增同名的 ICT tester（`HOSTILE_TESTER`）並把 ICT 移過去、Burn-in 改名為帶 U+202E 的名稱、AOI machine 改名為結尾零寬空白、Pack and ship 改名為 markup 與 `javascript:` 連結（`samples.ts` 的 `hostileNames`）；patch hash 重算。以與原 evidence 相同的方法核對：

- 伺服器 review（暫時的 Go 測試，pool 多一個 resource ID，跑完移除；`fixes/scripts/zz_t031_samples_test.go.txt`、`fixes/sample-server-review.log`，exit 0）：hostile 為 ready、server risk high、計入 high，patch hash 與範例相同。第一次核對時伺服器回 invalid `PATCH_TEST_REQUIRED /changes/8`（D-020：移除步驟之後要先 test 該元素的 `id` 才能改它），範例改用 `id` 的 test 後通過。**伺服器接受這些名稱**（U+202E、U+200B、markup、與既有資源同名），這也是下面 owner 問題 1 的依據。其他範例的結果與原 evidence 相同。
- JSON Schema（原 evidence 的 `validate_samples.py`，輸入由 `fixes/scripts/zz-dump-samples.test.ts.txt` 產生到 session 的暫存目錄，未保留；`fixes/sample-schema-check.log`，exit 0）：每個範例 proposal、批准之後的 proposal、三種 Approval、base 與每個 candidate IR 都通過。

### 指令與結果（Node 22.22.0，`npm ci --engine-strict=false`；go1.27.3 只用於暫時的 Go 測試）

| 指令 | Exit | 結果 |
|---|---|---|
| `npm ci --prefix apps/web --engine-strict=false`、`npm ci --prefix apps/web-e2e --engine-strict=false` | 0、0 | lock 未變 |
| `env -C apps/web npx vitest run …`（實作前） | 1 | 紅燈，見上（`red/red-first-run.log`） |
| 同上，以修正前的 `proposal-view.tsx` | 1 | 7 failed（`red/red-view-before-fix.log`） |
| `env -C apps/web npx vitest run`（10 個相關檔） | 0 | 224 passed（`green-proposal-tests.log`） |
| `python -I fixes/scripts/mutants_r1.py <repo>` | 0 | 22 killed、0 survived（`mutants.txt`） |
| 暫時的 Go 測試（`go test -count=1 -run TestT031Samples -v ./internal/ai/review/`） | 0 | `sample-server-review.log` |
| `.venv/bin/python -I …/validate_samples.py` | 0 | `sample-schema-check.log` |
| `make frontend-test`（db77bfe；之後 `apps/web` 沒有再改） | 0 | lint 0 warning、typecheck、vitest 45 檔 1017 個全過（審查時 43 檔 985 個；`frontend-test.log`） |
| `make frontend-build`（同上） | 0 | `/proposals/[proposalId]`、`/ui-gallery/proposal` 為動態 route（`frontend-build.log`） |
| `make e2e-test` 第 1 次（等另一個 agent 的 e2e 結束、3100–3102 沒有人聽之後單獨執行；db77bfe） | 2 | 491 passed、2 failed、32 skipped（13.3 分）：失敗的是 gallery 的兩語「每個字串是 catalog 或資料」，它找到 `ict_tester`——hostile 範例中既有資源的 ID，因為與新資源同名而顯示在名稱旁（正確的行為）；spec 的資料清單只列了新增 entity 的 ID，是測試的遺漏。已登入的三個新測試（en、zh-Hant 批准，en 拒絕）通過。修正：資料清單加上範例產線自己的 ID（06ec164），斷言不變（`e2e-test-1.log`） |
| `make e2e-test` 第 2 次（又等另一個 agent 的 e2e 結束後單獨執行；06ec164） | 0 | 493 passed、0 failed、32 skipped（全部是只在 CI 比對的截圖），18.3 分；coordinator 上一次是 490 passed，多的 3 個是本輪已登入的測試（`e2e-test-2.log`）。stderr 的一行「failed to pipe response」來自 `session.spec.ts` 刻意切斷 API 連線的測試，coordinator 的 log 也有 |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities（`npm-audit.log`） |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 沒有重產任何 tracked 檔（`spec-check.log`） |

### NOT RUN

| 項目 | 原因 |
|---|---|
| 截圖比對與新頁面的 baseline | 只在 CI 的固定容器（`E2E_SCREENSHOTS=1`）；沒有碰 `apps/web-e2e/tests/__screenshots__/` |
| 雙向文字在瀏覽器中的「畫面」檢查（截圖或量位置） | DOM 中已沒有原始的雙向控制字元可以重排（vitest 與 Chromium 都斷言），每個名稱在 `<bdi>`；沒有另外拍圖 |
| 與真的 Go API、worker、provider 的閉環 | e2e 的 fake API 只記帳、回固定答案，不 review、不驗證；屬 T-040 |
| `make backend-test`、`backend-race`、`integration-test`、`ai-eval` | 沒有 Go、schema、OpenAPI 或 migration 的變更；只跑了一個暫時的 Go 測試（已移除）核對範例 |
| live AI | 依指示不呼叫 |
| Node 24、Firefox、Safari、螢幕報讀器 | 本機只有 Node 22；P0 只跑 Chromium；報讀器需要人 |
| push、CI、獨立 review 第 2 輪 | 依指示不 push；H-029 一輪 |

### 延後（審查的 Info，只記錄）

- **I4**：輪詢 generating → ready 時只更新 proposal、不重讀 model；生成期間有人發布新版時，頁面仍提供 Approve，按下得到 409 並顯示 conflicted 的說明（安全，多一次必然失敗的請求）。
- **I5**：決定時的 401 只顯示 problem，沒有 `SignInLink`（與 T-043 列的草稿儲存相同）。
- **I6**：批准語意依據的決策狀態如審查所述；本輪沒有把任何 PROPOSED／TBD 當成 APPROVED，L4 依 D-069 (b) 的 B1（已實作的建議；B1 本身是否最終仍待 owner，見問題 3）。
- **I7**：generating 每 1.5 秒輪詢，沒有上限。
- 其他：`lib/api/problem.ts` 的 `problemFromResponse` 也以 `in` 查 code（與 I3 同類；審查已推算最後成為 protocol problem），本輪只依指示改 `failureProblem`；模型頁（`ModelView`）的 h1 與其他頁面上的名稱不在本切片，沒有加 `<bdi>`／⟨U+…⟩。

### 給 owner 的問題

1. **伺服器是否拒絕（或正規化）IR 名稱中的不可見與格式字元**（`\p{Cc}`、`\p{Cf}`、孤立 surrogate、U+2028／U+2029 等）？目前 schema 只有 1–200 字元，伺服器 review 接受本輪 hostile 範例的名稱（見上）。這是 contract 變更（`schemas/model-ir.schema.json`、`internal/model` 的驗證、AI review），本輪沒有做；畫面已能安全顯示。
2. **同類 entity 的名稱是否要唯一**（或至少給 warning）？畫面以 ID 區分同名者，但 IR 沒有規則。另：跨文字系統的相似字（例如拉丁字母中混入西里爾字母的「е」）不是 NFKC 能合併的，兩個既有、名稱只差這類字元的 entity 不會被標上 ID（新增的 entity 一律帶 ID）；要完整處理需要 UTS #39 的 confusable 檢查，較適合放在伺服器。
3. **D-069 (b) B1 之後的提示**：本輪在 422／風險改變的 409 之後只提供 Reject 並說明，但伺服器不記錄這次拒絕，所以重新整理後 Approve 會再出現。若要頁面一開就知道（B2：轉為 conflicted 並記原因，需要 migration 與 API 變更），需要 owner 決定。
4. **新增的 entity 一律附 ID**（依本輪指示）會讓 T-056 的照片提案每個新步驟都帶一個長 ID。可以嗎？或只在名稱與其他 entity 相同時才附？

