# T-015 驗證報告：React Flow 2D 編輯器（hackathon 切片）

**日期：2026-10-26｜範圍：H-029 的 T-015 hackathon 切片（不是 T-015 的完整範圍）｜分支：`t015-hk`（自 6041d39）｜狀態：實作與自我驗證完成，待獨立 review（H-029 (4)：每個切片一輪）｜需求：REQ-004、REQ-006、REQ-017（共用 ID）、REQ-022、REQ-023、REQ-024｜驗收測試：TEST-001、TEST-005（UI 端）、TEST-025（2D 端）、TEST-031、TEST-033｜依據：H-027 Q2（舊 prototype 的編輯器流程）、H-027 簡潔原則、ADR-007、ADR-016、ADR-020、ADR-021、DESIGN.md**

`planning/tasks.json` 的 T-015 **仍是 `not_started`**：依指示改為 `in_progress` 後，`make spec-check` 的 `check_decisions.py` 擋下（「T-015: started before its dependencies」：依賴的 T-045 仍是 `in_progress`，規則要求 `review` 或 `done`）。所以沒有改狀態，只加了一筆進度紀錄；T-045 送審（`review`）後由 coordinator 改為 `in_progress`（見文末「給 owner／coordinator 的問題」）。

## 交付內容

| 位置 | 內容 |
|---|---|
| `apps/web/package.json`、`package-lock.json` | `@xyflow/react` **12.12.0**（MIT，2026-09-24 發布，滿兩週；與 ADR-021 spike 同一版），連同 `@xyflow/system` 0.0.83、`zustand` 4.5.7（React Flow 內部的 store，app 不用）、`classcat` 5.0.5、9 個 d3 套件、6 個 `@types/d3-*`，共 19 個 lock entry，**皆無 install script**；`npm audit --omit=dev` 0 |
| `docs/adr/ADR-011`、`docs/THIRD_PARTY_NOTICES.md` | 依賴列（版本、日期、授權）；React Flow 與其套件的 copyright 與 MIT／ISC／BSD-3-Clause 全文 |
| `apps/web/features/model-editor/patch.ts` | JSON Pointer 與 JSON Patch（P0 profile：add、remove、replace、test；在副本上逐一套用、全有或全無、pointer 只解碼一次、`-` 只用於 append、test 以 JSON 值比較）——編輯器顯示的「工作中草稿」＝已存草稿＋待存的 ops，不是第二份模型 |
| `layout.ts` | 缺位置時的 layered 預設版面，與 `internal/domainpack.LayeredLayout` 同一規則（含 Go `math.Round` 的「半數遠離零」） |
| `flow.ts` | IR → React Flow：一個 IR node 一個 node、一個 flow 一個 edge，ID 與端點都是 IR 的；位置取 `views.2d.positions`，缺時用 layered；node 依閱讀順序排列（Tab 順序）；React Flow 的英文 `node`／`edge`／`Edge from…` 換成呼叫者給的 catalog 文字 |
| `edits.ts` | 每個編輯動作 → JSON Patch ops：新增／刪除步驟、連接／取消連接、移動、改名、佇列容量、步驟本身時間分布的數值、批次大小與等待上限、判斷的機率。改或刪 catalog 元素前同一 patch 先 `test` 其識別欄位（D-020）；刪除步驟一併移除它的 flow、引用它（或它的 flow）的 constraint 與 metric、它的位置；第一次改拓樸或移動時，把畫面上（layered 算出）的位置寫進 IR，其他步驟不會跳動 |
| `issues.ts` | 伺服器 issue 的 JSON Pointer → 實體（node／flow／resource）與其下的路徑；在 issue 所屬的 IR 中解析一次（已存草稿的 issue 用已存草稿，被拒的儲存用伺服器建的 candidate），之後的本機編輯改變 index 也不會讓 issue 跑到別的步驟；欄位取最具體的 issue |
| `canvas.tsx`、`canvas.module.css`、`vendor.css` | React Flow 畫布（受控模式、只用 `base.css` 並放進 `@layer vendor`、外觀由 tokens 設定、無格線、保留署名） |
| `model-editor.tsx`、`properties.tsx`、`fields.tsx`、`issue-list.tsx`、`editor.module.css`、`model-view.tsx` | 編輯器畫面（下節）；`model-view.tsx` 是 `/models/[modelId]` 的內容（原本在 page 裡），審閱用的預覽頁也用同一個元件 |
| `apps/web/lib/state/draft.ts` | 新事件 `reapplyLocal`：409 之後、重新載入目前草稿後，**只有使用者選擇時**才把本機編輯放到新 revision 上成為未存（下次儲存帶新的 If-Match）；重新載入本身不會這樣做 |
| `apps/web/ui/button.tsx`、`button.module.css`、`ui/menu.tsx` | Button 的 `blocked`（看起來停用、按下無作用，但仍可聚焦，`aria-disabled`，原因以 `aria-describedby` 連到畫面上的文字；DESIGN.md Buttons／Disabled）；`ActionMenu`（React Aria Menu，外觀沿用 Select 的 popover） |
| `apps/web/app/models/[modelId]/page.tsx` | 改為 `RequireSession` ＋ `ModelView` |
| `apps/web/app/ui-gallery/editor/` | 審閱用預覽 `/ui-gallery/editor`（與 `/ui-gallery` 相同：只有 `GEMINUS_UI_GALLERY=1` 才存在，否則 404）：真的 model page、API client 與 draft session，以 editor 身分（`viewer` 狀態為檢視者）連到頁內的替身 API（`stand-in.ts`：套用 patch、計 revision 與版本、過期的 If-Match 回 409；**不驗證模型**）；範例產線 `sample-line.ts`（synthetic）與四個狀態 `sample-states.ts` |
| `apps/web/messages/en.ts`、`zh-Hant.ts` | 編輯器的所有字串（兩語）；刪除 `model.notInBuild`（「此版本不含 2D 編輯器與 3D 檢視」已不正確） |
| `testdata/editor/patch-vectors.json`、`internal/model/patch/editor_vectors_test.go` | 22 組編輯器 patch（由 vitest 產生、檔案不同步時 vitest 失敗），Go 以伺服器的 manual-draft policy 套用：每組都必須被接受、schema-valid、model hash 與編輯器算的相同；範例產線經 `validate.IR` 沒有 error |
| `internal/domainpack/domainpack_test.go` | `TestLayoutRoundsHalfAwayFromZero`（與 TS 版同一案例，mutation M13 之後加入） |
| `DESIGN.md`、`docs/adr/ADR-021`、`ADR-020`、`docs/ARCHITECTURE.md`、`apps/web-e2e/README.md` | 編輯器的簡化記錄；React Flow 包裝的做法；ADR-020 留給 T-015 的「切換語言保留未存編輯」已驗證；架構說明 |

## 編輯器能做什麼

| 功能 | 做法 | 依據 |
|---|---|---|
| 檢視 | 已存草稿＋待存編輯畫在畫布上；初始畫面是產線開頭、可讀的大小（≥ 75%），「顯示所有步驟」縮放到整條線；捲動或觸控板縮放 | H-027 Q2、REQ-004 |
| 選取 | 點選或 Tab 到步驟後按 Enter；Escape 取消選取且焦點留在步驟上；選取存在 T-014 的共用 selection store（只接受 IR 中的 ID） | TEST-025（2D 端） |
| 移動 | 拖曳，或選取後方向鍵（Shift 四倍）；放開或按鍵才成為 `replace /views/2d/positions/<id>` | REQ-004 |
| 新增步驟 | 畫布工具列的「新增步驟」選單（8 種 generic 類型；沒有 entity type 的模型不列 source）；新步驟放在最深層之後第一個空位、被選取、焦點在名稱欄 | H-027 Q2 的步驟面板 |
| 刪除步驟 | 屬性面板「刪除步驟」或在步驟上按 Delete；確認對話框說明會一併移除幾個連接 | REQ-004 |
| 連接／取消連接 | 屬性面板「連接到」選單（排除自己、source、已連接者）或拖曳 handle；每個連接一列，可移除；判斷的每條出路可改機率 | REQ-004 |
| 屬性 | 見下表 | REQ-004、REQ-024 |
| 伺服器的欄位錯誤 | 已存草稿的 issues 與被拒儲存（422）的 details 依 JSON Pointer 顯示在欄位下（catalog 文字；未知 code 用伺服器英文訊息並標 `lang="en"`）；沒有欄位的 issue 列在面板頂端；有 error／warning 的步驟框為紅／琥珀色，名稱加「有錯誤」 | H-027 Q2 |
| 問題清單 | 已存草稿的 issues（嚴重度、說明、步驟名、路徑）；有步驟的 issue 是按鈕，按下選取並聚焦該步驟、畫布移到它；說明 issue 來自哪個 revision、未存的變更在儲存時才檢查 | H-027 Q2 |
| 儲存草稿 | 所有待存 ops 一個 JSON Patch，`If-Match: "draft-revision-N"`；儲存中仍可編輯，那些編輯等下一次儲存 | REQ-006、TEST-005（UI 端） |
| 409 | 保留全部編輯、畫布轉為唯讀、說明「沒有覆寫任何東西」；「載入目前的草稿並重新套用我的變更」：重新載入，編輯仍套得上時才成為新 revision 上的未存編輯，套不上時說明原因、只能捨棄；從不自動 rebase | REQ-006、AGENTS §2 |
| 捨棄 | 有未存編輯時「捨棄我的變更」，先確認 | — |
| 發布 | Primary「發布版本」；有未存編輯、草稿過時、已存草稿有 error、或與版本相同時 **blocked**，原因寫在旁邊；確認對話框寫出新版本號；`If-Match`、`expected_base_version_id`、Idempotency-Key（同 revision 重試沿用）；完成後重新載入 model 與草稿 | D-028、H-027 Q2 |
| 檢視者 | 沒有新增、儲存、發布、刪除、連接；欄位唯讀；顯示既有的唯讀說明 | T-014 |
| 手機（≤ 720 px） | 不放畫布，說明編輯需要較寬的螢幕（DESIGN.md：手機不做拓樸編輯） | H-012、DESIGN.md |

**屬性面板中，本切片可編輯的**：名稱、佇列容量（留空＝不限）、步驟本身時間分布的數值（process 的作業時間、delay 的延遲、source 的到達間隔；分布類型不變）、批次的最小／最大批量與湊批最長等待（max_wait 留空＝不限）、判斷各出路的機率、連接。**唯讀顯示**：ID、類型、分布類型、empirical 的資料集、source 投入的物件與投入排程、process 需要的資源（使用量／容量、會故障、依班表上班與班表名、換線的每個 from→to 與時間分布、預設換線）、sink 的結果、failure 的原因。**不在本切片**：新增或移除 batch、改分布類型、資源與換線／故障／班表的編輯、entity type、metrics、domain_attributes。

電子產線範本（T-051 切片，另一個 agent 進行中）：編輯器是 domain-neutral 的，不依 pack 的 kind 或 role；範例產線 `sample-line.ts`（SMT 兩種板子換線與故障、AOI、ICT 與除錯重工、燒機批次 20–40、包裝；`ELECTRONICS_LINE.md` §4 的形狀、較小、全部 synthetic）用到 batch、setups、breakdowns、calendars、有限緩衝，Go validator 對它沒有 error。T-051 的範本尚未提交，所以**沒有對真正的範本跑過**（NOT RUN）。

## 每個畫面的控制項（H-027：控制項 → 用途 → 需求／測試）

所有畫面共用的 app strip（Skip link、Projects、Language、Theme）不變，見 T-045 報告。

`/models/[id]`（editor／owner；`ModelView`）：

| 控制項 | 何時出現 | 用途 | 需求／測試 |
|---|---|---|---|
| Back to project（連結） | 一直 | 回到該專案 | T-014；`editor.test.tsx`「only the controls…」（沒有重複連結） |
| Discard my changes（danger） | 有未存編輯且不在儲存中（含 409、儲存失敗） | 捨棄本機編輯（先確認） | AGENTS §2（明確捨棄）；`editor.test.tsx`「discards…」「says so when the edits no longer fit…」 |
| Save draft | 一直（editor）；沒有可存的、儲存中或 409 時 blocked，原因是狀態列 | 以 If-Match 儲存 | REQ-006；「saves the edits as one JSON Patch…」「Saving…」「on a 409…」 |
| Publish version（唯一的 primary） | 一直（editor）；未存、過時、有 error、無變更時 blocked，原因在旁邊 | 發布（先確認） | D-028、H-027 Q2；「blocks Publish while…」「publishes a clean, changed draft…」「does not offer Publish for a draft that equals its version」 |
| Load the current draft and reapply my changes | 409 時，直到套用或發現套不上 | 重新載入並（套得上時）重新套用 | REQ-006、T-014 移交；「on a 409…」、e2e「a 409 keeps the edits…」 |
| Add step（選單） | editor 且不在 409 | 新增步驟（H-027 Q2 的步驟面板） | REQ-004；「adds a step…」、e2e 鍵盤流程 |
| Show all steps（quiet） | 一直 | 縮放到整條線（初始畫面只顯示開頭） | 產線很寬；e2e a11y 各狀態 |
| 畫布上的步驟（每個一個 Tab stop） | 一直 | 選取、移動、刪除（Delete 先確認） | REQ-004、TEST-025；「deselects with Escape…」「deletes a step…」、e2e 鍵盤與滑鼠 |
| 問題清單中每個有步驟的 issue（quiet 按鈕） | 有 issue 時 | 選取並聚焦該步驟 | H-027 Q2；「lists the server's issues…」、e2e 鍵盤流程 |
| 屬性面板的欄位（名稱、容量、時間、批次、機率） | 選取步驟時（檢視者與 409 時唯讀） | 編輯屬性；顯示伺服器的欄位錯誤 | REQ-004；「shows a refused save's details…」「says when a number is needed…」「shows batch and changeover settings…」 |
| Remove（每個連接一個，名稱為「Remove the connection to X」） | editor 選取非終點步驟、有連接時 | 取消連接 | REQ-004；「connects and disconnects…」 |
| Connect to（選單） | editor 選取非終點步驟、有可連的步驟時 | 連接 | REQ-004；「connects and disconnects…」、e2e 鍵盤流程 |
| Delete step（danger） | editor 選取步驟時 | 刪除（先確認） | REQ-004；「deletes a step…」 |
| 對話框：刪除、捨棄、發布 | 對應動作時 | Cancel／Keep editing（焦點起點）與確認 | ADR-021 ConfirmDialog；e2e a11y 對話框狀態 |
| Details（problem 的技術細節） | 有 problem 時 | 技術訊息與 request ID | T-045 |

檢視者：只剩 Back to project、Show all steps、畫布步驟（只能選取）、問題清單按鈕；欄位唯讀。手機：只有 Back to project 與說明。

`/ui-gallery/editor`（審閱工具，`GEMINUS_UI_GALLERY=1` 時才存在）：上述全部，加上四個「範例狀態」連結（有問題、可以發布、別人已儲存、檢視者），與 `/ui-gallery` 一樣是給 owner 審閱與瀏覽器檢查用。

沒有加入：撤銷／重做、資源池、2D／3D 切換、縮放按鈕組、caption 框、issues pill、key caps（DESIGN.md 中有，但本切片不需要；記在 DESIGN.md「Simplifications in the app」）。

## 測試（先寫測試）

| 檔案 | 內容 | 數量 |
|---|---|---|
| `tests/editor-patch.test.ts` | pointer 編解碼、patch 的原子性與失敗位置、root／`-`／index 規則、test 以 JSON 值比較 | 7 |
| `tests/editor-adapter.test.ts` | layered layout（與 Go 相同的案例，含半數遠離零）、IR→React Flow（ID 與端點完全相同、data 只有 label／kind／severity、位置、閱讀順序、選取與可編輯、edge 不可聚焦、React Flow 英文字串被換掉）、每個編輯的 ops（含伺服器 D-020 規則的 TS 重述）、往返（新增後刪除、連接後取消、移動後移回都還原成同一份 IR）、issue 定位與欄位歸屬 | 17 |
| `tests/editor-vectors.test.ts` | 產生 `testdata/editor/patch-vectors.json` 並要求檔案相同；TS 的 model hash（RFC 8785＋SHA-256）與 `testdata/hashing/expected-hashes.json` 相同 | 2 |
| `tests/editor-draft.test.ts` | `reapplyLocal` 只在 409 後、無儲存中、有編輯時生效；重新載入本身仍是 conflict | 3 |
| `tests/editor.test.tsx`（happy-dom，真 client＋draft session＋替身 API） | 畫布 ID、儲存（If-Match、patch 內容、revision）、儲存中再編輯、409 保留與重新套用、套不上時只能捨棄、發布 blocked 與原因、發布（If-Match、base、key）、無變更不發布、問題清單導覽與欄位錯誤、422 details 到欄位、非數字不送出、新增、連接／取消／機率、刪除與 Delete 鍵、Escape 保留焦點、捨棄、產線的批次與換線顯示、檢視者、手機、zh-Hant；簡潔（每個狀態最多一個 primary、按鈕不同名、連結不重複、每個 blocked 都有畫面上的原因、沒有 `disabled`）；axe | 24 |
| `tests/design-rules.test.ts`（既有，擴大） | 掃描範圍加入 `features/`：編輯器的 CSS 一樣只用 tokens、無陰影、1 px 邊框 | （既有） |
| `internal/model/patch` `TestEditorVectors` | 22 組 patch 被伺服器接受、schema-valid、model hash 相同；範例產線無 error | 1（22 subtests） |
| `internal/domainpack` `TestLayoutRoundsHalfAwayFromZero` | 與 TS 相同的取整案例 | 1 |
| `apps/web-e2e/tests/editor.spec.ts` | 只用鍵盤：問題→步驟、方向鍵移動與 aria-live、Escape、欄位、連接選單、新增、Delete 與確認、儲存，每一站 focus 可見；滑鼠拖曳移動與 handle 連接；409 與重新套用；發布；切換語言保留未存編輯；axe：選取、兩個選單、刪除與發布對話框、可以發布、檢視者、409，× en／zh-Hant × 淺色／深色 | 13 |
| `a11y.spec.ts`、`simplicity.spec.ts`（`routes` 加入 `/ui-gallery/editor`） | axe（含對比）× 2 語言 × 390／1280 × 淺／深；簡潔規則（編輯器的 blocked 控制項必須有可見原因，其他 route 仍要求 0 個停用） | 8＋4 |
| `strings.spec.ts`（新增編輯器的兩個測試） | 每個看得到或聽得到的字串是 catalog、模型的資料、已接受的 React Aria 字串，或 React Flow 署名的兩個字串 | 2 |

**先紅後綠**：`editor-patch`、`editor-adapter`、`editor-draft` 在實作前執行為紅（`checks/evidence/2026-10-26-T-015/red-adapter.log`、`red-draft.log`）。元件測試是在第一版元件之後寫的（老實記錄）：第一次執行 24 個中 14 個失敗，多數是測試 helper，但也抓到真的錯：選另一個步驟時同一批變更中的「取消選取」把選取清掉（`canvas.tsx` 修正）。瀏覽器的字串測試抓到 React Flow edge 寫死的英文 `aria-roledescription="edge"`（改為 catalog 的「connection／連接」）。自我檢查時發現：新增步驟後，之後再選回該步驟時焦點會又被搶到名稱欄；先加測試（紅），再改為只有新增當下才聚焦名稱（綠）。

**Mutation**（`checks/evidence/2026-10-26-T-015/mutation-frontend.json`，scratch 腳本逐一改一行、跑對應測試、還原）：13 個，第一輪 12 killed、1 survived（M13：kept position 的格子用 JS 的 `Math.round` 而非 Go 的 `math.Round`）→ 在 TS 與 Go 各加同一個案例，重跑 killed（`mutation-M13-rerun.log`）。Go 端另做 2 個：拿掉一個 `test` op → `PATCH_TEST_REQUIRED`；改一個位置 → model hash 不符；皆失敗（`mutation-go-*.log`）。

## 指令與結果（Node 22.22.0、npm 10.9.4；`npm ci --engine-strict=false`，repo 要求 Node 24；`checks/evidence/2026-10-26-T-015/exit-codes.txt`）

| 指令 | 結果 |
|---|---|
| `make frontend-test`（lint `--max-warnings=0`、typecheck、vitest） | exit 0：26 個檔案、600 個 test（T-045 re-skin 後為 21 個檔案、547 個） |
| `make frontend-build` | exit 0；`/ui-gallery/editor` 為 dynamic route；React Flow 的 CSS 在 build 後位於 `@layer vendor` |
| `make e2e-test`（本機 Chromium build 1194，Playwright 1.56.1） | exit 0：**143 passed、32 skipped**（截圖，只在 CI 的固定容器比對：NOT RUN）；T-045 之後為 116 passed，新增 27 個：`editor.spec.ts` 13、`/ui-gallery/editor` 的 axe 8 與簡潔 4、編輯器字串 2。字串測試列出的非 catalog 字串只有 React Aria 已接受的「Dismiss／關閉」（選單的隱藏關閉鈕）；React Flow 署名的兩個字串存在。log 中的 `failed to pipe response` 來自 `session.spec.ts` 刻意切斷的回應（T-045 報告已說明），不是失敗。第一次完整執行時兩個編輯器字串測試失敗（測試以滑鼠點擊畫面外的步驟；改為鍵盤選取），之前的單獨執行抓到 edge 的英文 roledescription（已修正） |
| `apps/web-e2e/tests/__screenshots__/` | `git status` 確認沒有任何變更 |
| `npm audit --prefix apps/web --omit=dev` | exit 0，0 vulnerabilities（dev 依賴 happy-dom 的 high 為既有，未變） |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | exit 0（T-015 狀態保持 `not_started`；改為 `in_progress` 時 exit 2，原因見開頭） |
| `gofmt -l`、`go vet`、`go test ./internal/model/patch/ ./internal/domainpack/...` | exit 0（`go-test.log`：patch、domainpack、conformance、softwaredelivery 皆 ok） |

過程中的一次 `make frontend-test` 有 `tests/bff.test.ts`「streams an upload…」逾時（5 秒）；單獨執行 139 個全過，當時機器 load average 約 25（其他 agent 同時執行），與本切片無關（未改 BFF）；最後一次完整執行全過。

## NOT RUN／限制

| 項目 | 原因 |
|---|---|
| 截圖比對與 baseline | 依指示不新增、不修改 baseline；`/ui-gallery/editor` 不在 `screenshots.spec.ts`。本機預覽截圖（**不是 baseline**）：`checks/evidence/2026-10-26-T-015/local-shots/`，10 張（en／zh-Hant、淺／深、1280／390，含選取、選單、對話框、409、檢視者、手機） |
| 對真正的 Go API 與已登入的瀏覽器操作編輯器 | 瀏覽器端沒有掛 `AuthProvider`（登入後的 UI bootstrap：`/me`、選 organization，屬 D-045／T-043 後續），所以真的 `/models/[id]` 在瀏覽器只顯示「需要登入」。編輯器在預覽頁（替身 API）與 vitest（替身 API）驗證；伺服器端的 patch 相容性由 `TestEditorVectors` 驗證 |
| T-051 的電子產線範本 | 尚未提交；以結構相同的範例產線代替 |
| `make backend-test`、`backend-race`、`integration-test` | 只改了兩個 Go 測試檔與一個 testdata 檔；只跑了這兩個 package 的相關測試（上表） |
| CI | 依指示不推送 |
| Node 24、Firefox、Safari、螢幕報讀器 | 本機只有 Node 22；P0 只跑 Chromium；螢幕報讀器需要人 |
| 獨立 review | H-029：每個切片一輪，待 coordinator 安排 |

## T-015 完整範圍中尚未完成的部分（比賽後）

- **client validation**：目前只擋「不是數字」；範圍、必要欄位與圖的規則仍只由伺服器在儲存時檢查（REQ-004「client/server validation 一致」）。
- **dirty navigation guard**：離開或重新整理頁面時未存的編輯會遺失（T-014 的設計），沒有離開前的確認。
- 未編輯的屬性：新增／移除 batch、改分布類型、資源（容量、故障、班表、換線）、entity type、metrics、calendar、release schedule。
- 撤銷／重做、資源池在畫布上、多選、複製貼上；pack catalog 的 kind（`smt_station` 等）與兩語標籤（ADR-020 的 follow-up）——目前只有 8 種 generic 類型。
- 3D 的 adapter（T-017）：selection store 已共用 IR ID，3D 尚未實作。
- 每個步驟一個 Tab stop：約 25 個步驟時，鍵盤使用者從畫布到屬性面板要經過其餘步驟；可改 roving tabindex 或加「跳到屬性」連結。
- 「重新套用」只以元素識別（D-020 的 test）保護：別人改了同一個欄位時，重新套用後是「我的值」（使用者明確選擇後的 last-writer-wins）；欄位層級的衝突比對未做。
- 第一次改拓樸或移動時把整個 layered 版面寫進 IR（patch 會多一些 `add /views/2d/positions/<id>`），讓其他步驟不跳動。
- 新步驟的時間預設為 1 個時間單位（constant），等使用者在屬性中改；這是編輯器的初值，不是任何資料。
- 截圖 baseline（TEST-033）在畫面定案後由 owner 確認。

## 風險

- **預覽頁**：替身 API 在瀏覽器中、不驗證模型；頁面上明寫「範例資料、替身伺服器、不檢查模型」。只在 `GEMINUS_UI_GALLERY=1` 時存在。**不可**拿它當 demo 中「伺服器驗證」的證據。
- **頁寬**：有編輯器的頁面以 `main:has(.editor)` 取消 1152 px 的閱讀寬度（產線很寬）；其他頁面不變。
- **React Flow 的 `zustand`**：React Flow 內部使用；app 不 import，ADR-016「不用 state library」不變。
- **Lockfile**：`@types/react`、`@types/react-dom`、`csstype` 由 `dev` 變為 `devOptional`（React Flow 宣告它們為 optional peer）；`npm ci --omit=dev` 不安裝它們（audit 0）。

## 給 owner／coordinator 的問題

1. **Demo 的關鍵缺口**：沒有登入後的 UI bootstrap（`/me` → `AuthProvider`、選 organization），部署後的 `/models/[id]` 永遠顯示「需要登入」，編輯器在真實環境無法使用。誰在 T-057 前補上（D-045／T-043 的後續）？
2. **T-015 狀態**：spec-check 要求 T-045 先到 `review` 或 `done`；T-045 送審時一併把 T-015 改為 `in_progress`？
3. **簡化的選擇**（H-027）：步驟面板做成「新增步驟」選單（不是一排按鈕）；只有「顯示所有步驟」而沒有縮放按鈕組；儲存與發布在不能用時保持可見但 blocked（附原因），而不是隱藏；編輯器頁面用整個視窗寬度。是否接受？
4. **鍵盤**：每個步驟一個 Tab stop（約 25 個）先接受，或本切片就加「跳到屬性」連結？
5. **409 後的重新套用**：同一欄位兩人都改時，重新套用後保留使用者的值（使用者明確選擇後）。可以嗎，或要先做欄位層級的衝突顯示？
6. **新步驟的初值**（1 個時間單位）：可以，或要求使用者在新增時就輸入？
