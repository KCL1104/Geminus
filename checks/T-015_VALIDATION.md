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
- ~~「重新套用」只以元素識別（D-020 的 test）保護：別人改了同一個欄位時，重新套用後是「我的值」（使用者明確選擇後的 last-writer-wins）；欄位層級的衝突比對未做。~~ 已由 review 第 1 輪修正取代（文末「Review round 1 fixes」）：別人改過的欄位不再被重新套用覆寫，只能捨棄。
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
5. **409 後的重新套用**：同一欄位兩人都改時，重新套用後保留使用者的值（使用者明確選擇後）。可以嗎，或要先做欄位層級的衝突顯示？（review 第 1 輪後不再需要決定：同一欄位兩人都改時重新套用不成立，只能捨棄，見文末「Review round 1 fixes」。）
6. **新步驟的初值**（1 個時間單位）：可以，或要求使用者在新增時就輸入？

## Coordinator 的執行（hackathon 切片，74cb9ae 之後）

Coordinator 把 `t015-hk` 的五個 commit（9f5ef0c、2fb5838、11691c1、766d4a6、1a89ca2）cherry-pick 到 `claude/lucid-meitner-vq1awo`（09ed10e 之後，成為 df6a644、f70bec7、71ccdd8、0993814、74cb9ae）。衝突：`docs/adr/ADR-011` 的依賴表（T-045 review 1 更正了字型那一列，本切片新增 React Flow 一列；兩者都保留）與重產的 `checks/*.json`。T-045 已完成，T-015 改為 `in_progress`（spec-check 通過）。

**整合時的衝突與處理**：T-045 review 1 新增的 `tests/simplicity.test.tsx` 規定登入後的頁面「沒有停用的控制項」，而本切片的編輯器在條件不符時讓「儲存草稿」與「發布」維持可聚焦的 `aria-disabled`，並以 `aria-describedby` 指向旁邊寫出的原因（DESIGN.md 的編輯器一節）。兩者衝突，模型頁的兩個 case 失敗（`coordinator/frontend-test.log`）。H-027 禁止的是佔位與永遠停用的按鈕，不是等待條件、已說明原因的真實動作，所以 coordinator 把規則細化為「沒有**未說明原因**的停用控制項」：每個停用的控制項都要以 `aria-describedby` 指向看得見、非空的文字；DESIGN.md Simplicity 第 2 條同步更新。這不是放寬：把「儲存草稿」的 `aria-describedby` 拿掉的 mutant 讓同兩個 case 失敗（`coordinator/simplicity-refined-red-mutant-no-reason.log`，exit 1），恢復後通過（`simplicity-refined-green.log`）。

在整合後的 tree 上（`checks/evidence/2026-10-26-T-015/coordinator/`，Node 22.22.0，`--engine-strict=false`）：`npm ci`（兩個專案）、`make spec-check`、`make frontend-build`、`npm audit --omit=dev`（0 vulnerabilities）、`go test -count=1 ./internal/model/... ./internal/domainpack/...` exit 0；`make frontend-test` 第一次 exit 2（上述衝突），修正後 `frontend-test-2` exit 0（642 個 test）；`make e2e-test` 第一次 exit 2（另一個 agent 同時執行 e2e，佔用 `127.0.0.1:3101`，不是測試失敗），單獨重跑 `e2e-test-2` exit 0（162 passed、32 skipped：截圖只在 CI 比對）。

- **Coordinator 對切片問題的決定**（H-027 的簡潔原則之內，owner 可再改）：Add step 用選單、只有「顯示全部」不加縮放按鈕、被擋的 Save draft／Publish 保留並寫出原因、編輯器頁使用整個視窗寬度、每個步驟一個 Tab 停點（暫不加「跳到屬性」連結）、~~重新套用時同一欄位以使用者自己的值為準~~（**已撤回**：並行語意不在 H-027 委託的範圍內，review 第 1 輪 M1；見文末「Review round 1 fixes」）、新步驟的時間預設為 1 個時間單位。
- **阻擋 demo 的缺口**：app 沒有掛上登入後的 bootstrap（`/me` → `AuthProvider`、選擇組織），部署後模型頁只會顯示「需要登入」；由 T-043 的後續處理（另一個 agent 進行中）。
- **NOT RUN**：截圖比對（編輯器不在截圖 spec 中）；登入後在瀏覽器以真實 API 操作；T-051 的正式範本（尚未整合）；full backend-test／backend-race／integration-test；獨立 review（依 H-029，接下來進行一輪）。

## Review round 1 fixes（2026-10-26，分支 `t015-t032-m1`，自 d229225）

審查：`checks/T-015_REVIEW_r1.md`（CHANGES REQUESTED，M1；檔案在 reviewer 的 worktree，本分支未收錄）。本節只修 M1（Medium）；Low 與 Info 依指示延到比賽之後（見下）。Commit：036f60b（測試、修正、重產的 vectors）。Evidence：`checks/evidence/2026-10-26-T-015-T-032-r1/fixes/`（`exit-codes.txt` 與各 log）。

**撤回 coordinator 的決定**：上方 coordinator 一節的「重新套用時同一欄位以使用者自己的值為準」撤回。H-027 委託 coordinator 的是介面的簡潔，不是並行語意；last-writer-wins 正是 REQ-006 與 AGENTS.md §2（不 silent overwrite、不 auto-rebase）要擋的 lost update。給 owner 的問題 5 不再需要決定：現在同一欄位兩人都改時，重新套用不成立，只能捨棄（審查者的選項 (a)）。

| 發現 | 變更 | 測試 | Mutant |
|---|---|---|---|
| **M1**：409 後「重新套用」只靠 D-020 的識別 test，別人在新 revision 改過、本機也改過的欄位被本機的值覆寫，畫面不說 | (1) `features/model-editor/edits.ts`：每個 `replace`／`remove` 之前加上被取代或刪除的值的 `test`：欄位（名稱、佇列容量、時間參數、批次三欄、判斷機率、2D 位置）測舊值；刪除整個元素（node、flow、metric、constraint）時，在 D-020 的識別 test 之前先 test 整個元素的舊值；刪除步驟時其位置也先 test。舊值以 JSON 複製，ops 不與 IR 共用物件。(2) `patch.ts` 新增 `reapplyPatch`：與 `applyPatch` 相同，但 `add` 的物件成員已存在、或 append（`/-`）的元素 `id` 已在陣列中時失敗（`test_failed`）。JSON Patch 不能 test「不存在」，而編輯器只 add 不存在的成員，所以這等於在重新套用前比較 base 與新 revision 在該路徑的值。`applyPatch` 照舊依 RFC 6902（與伺服器相同）。(3) `model-editor.tsx`：「重新套用」改以 `reapplyPatch` 判斷；409 期間畫布也以 `reapplyPatch` 顯示（套不上時顯示伺服器的草稿，與訊息一致）。套不上時沿用既有的 `reapplyFailed` 路徑與訊息（「Your changes no longer fit the current draft…」，只能捨棄）。(4) `testdata/editor/patch-vectors.json` 重產（22 組） | 見下表 | 9 個，全部 killed（見下） |

**現在的行為**

- 審查者的情境（替身 API、真的 client 與 draft session）：A 把「Before AOI」的容量由 20 改為 12（revision 4）；B 仍在 revision 3，改為 25、儲存得 409，按「載入目前的草稿並重新套用我的變更」→ 訊息「Your changes no longer fit the current draft…」、狀態「Not saved: the draft changed on the server. Draft revision 4.」、重新套用按鈕消失、Save draft blocked、容量欄顯示 **12**（A 的值）；只能捨棄 →「All changes saved. Draft revision 4.」，伺服器仍是 12，全程只送過一次 PATCH。
- 別人新增了我也新增的成員（「Before SMT」原本沒有上限，A 設 40、B 設 50）：同樣只能捨棄，欄位顯示 40。
- 不衝突的照常重新套用：同一步驟的不同欄位（A 改名稱、B 改容量）→ 重新套用、儲存為 revision 5，兩者都在；第二次 PATCH 帶 `test /nodes/4/capacity 20`。既有的「別人改 SMT 的名稱、我改 Before AOI 的容量」與 e2e 的 409 流程不變。
- 為了不覆寫而比原本嚴格的情況（都走「只能捨棄」，在目前的草稿上重做）：兩人移動同一步驟；我刪除的步驟（或隨它刪除的 flow、metric、constraint、位置）被別人改過；還沒有版面的模型兩人都做了第一次拓樸編輯或移動（兩邊都寫入整份版面）；兩人新增同一 ID 的步驟或連接。單元測試逐一列出這些情況與對應的不衝突情況。
- 伺服器端：`test` 不受 allowlist 限制（D-020），`TestEditorVectors` 以 ManualDraft 接受重產後的 22 組 vectors；伺服器確實比較舊值（mutant GO-A：把一組的舊容量改為 21 → `PATCH_TEST_FAILED`）。

**測試（先寫，先紅後綠）**

| 檔案 | 新增或改動 |
|---|---|
| `tests/editor.test.tsx` | 新增 3 個：審查者的情境（同欄位 → 只能捨棄、顯示別人的值、伺服器不變）；同一成員兩人都 add → 只能捨棄；同一步驟不同欄位 → 照常重新套用並儲存。既有 3 個的 patch 內容斷言加上舊值的 `test`（更嚴格，不是放寬） |
| `tests/editor-adapter.test.ts` | 新增 2 個：「每個 replace／remove 之前都有同一路徑的 test」（13 種編輯，逐 op 套用驗證）；18 種編輯 × 「別人改了同一處 → `reapplyPatch` 失敗／改了別處 → 成功／在自己的草稿上 → 成功」。既有的 move、rename、delete、properties 斷言改為含舊值 test |
| `tests/editor-patch.test.ts` | 新增 4 個 `reapplyPatch`：成員仍不存在時與 `applyPatch` 相同；add 會覆寫既有成員時失敗；append 的 ID 已存在時失敗；test 失敗時全有或全無 |
| `tests/editor-vectors.test.ts` | 未改；重產前執行失敗（`vectors-stale.log`，exit 1），重產後通過 |

紅：在未修改的程式上執行上述三個檔案，16 failed／41 passed（`red-t015.log`，exit 1）；審查者的情境與 add 的情境失敗的原因正是舊程式靜默重新套用（畫面為「Your changes are on draft revision 4 now. Check them, then save.」）；「同一步驟不同欄位」在舊程式上失敗只因第二次 PATCH 沒有舊值的 test（它的重新套用在舊程式上也成立）。綠：60／60（含 `editor-draft.test.ts`）。

**Mutant**（`mutants.py`、`mutants.json`、`mutant-*.log`；腳本逐一改一處、跑對應測試、從讀入的原文還原，結束後 `git status` 乾淨）

| ID | 改動 | 結果（擋下它的測試） |
|---|---|---|
| T015-A | `setAt` 不加舊值的 test | killed（11 個：adapter 6、元件 5，含審查者的情境） |
| T015-B | 重新套用改回以 `applyPatch` 判斷 | killed（元件：同一成員兩人都 add）；replace 的情境由 ops 中的舊值 test 擋下，兩道防線各自有效 |
| T015-C | `reapplyPatch` 允許 add 覆寫既有成員 | killed（patch、adapter、元件各 1） |
| T015-D | `reapplyPatch` 允許 append 重複的 ID | killed（patch、adapter） |
| T015-E | 409 期間畫布改回以 `applyPatch` 顯示 | killed（元件：套不上時欄位應顯示別人的 40） |
| T015-F | 刪除 catalog 元素前不 test 整個元素 | killed（adapter 3） |
| T015-G | 刪除步驟前不 test 整個 node | killed（adapter 3） |
| T015-H | 刪除步驟的位置前不 test | killed（adapter 3） |
| GO-A | vectors 中一組的舊容量改為 21 | killed（`TestEditorVectors`：`PATCH_TEST_FAILED /1/value`） |

**指令與結果**（Node 22.22.0、npm 10.9.4，`npm ci --engine-strict=false`；go1.27.3，`GOTOOLCHAIN=local`；共用機器 load average 約 15–20）

| 指令 | exit | 結果 |
|---|---|---|
| `npm ci --prefix apps/web --engine-strict=false`、`npm ci --prefix apps/web-e2e --engine-strict=false` | 0／0 | |
| `npx vitest run`（三個 T-015 測試檔，修正前） | 1 | 16 failed／41 passed（`red-t015.log`） |
| `npx vitest run tests/editor-vectors.test.ts`（重產前） | 1 | 檔案與今天的 ops 不同（`vectors-stale.log`） |
| `GEMINUS_WRITE_EDITOR_VECTORS=1 npx vitest run tests/editor-vectors.test.ts`，再不帶變數執行 | 0／0 | |
| `go test -count=1 -v -run TestEditorVectors ./internal/model/patch/` | 0 | 22 個 subtest 都 PASS（`go-editor-vectors.log`、`go-editor-vectors-final.log`） |
| `go test -count=1 ./internal/model/...` | 0 | model、calendar、hashing、patch、validate、coldstart 都 ok（`go-test-model.log`） |
| `make frontend-test`（lint、typecheck、vitest） | 0 | 34 個檔案、785 個 test（審查時 770；本輪新增 15：T-015 9、T-032 6；`frontend-test.log`） |
| `make frontend-build` | 0 | `frontend-build.log` |
| `make e2e-test`（單獨執行；先確認 3100／3101 沒有人用、沒有其他 Playwright） | 0 | 257 passed、32 skipped（全部是 `screenshots.spec.ts`，只在 CI 比對），12.4 分鐘；`editor.spec.ts` 的 409 與重新套用流程通過（`e2e-test.log`）。與審查時 coordinator 的 257／32 相同 |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | 每個檢查都 PASS（`spec-check.log`）；重產的 `checks/docs-check.json` 為 121 個 Markdown 檔、112 個相對連結：本分支沒有新增 Markdown，原本記錄的 115 已過時（tree 中有 121 個 tracked `.md`） |
| Mutant（`mutants.py`） | — | 9 個（含 GO-A）全部 killed |

`apps/web-e2e/tests/__screenshots__/` 沒有改動。

**風險**：每個欄位編輯多 1 個 op、每個刪除的元素多 1 個 op（方向鍵移動一次由 1 個變 2 個）；JSON Patch schema 的上限 1000 個 op（`schemas/json-patch.schema.json` 的 `maxItems`，既有）在一次儲存很多編輯時會較早碰到；超過時該次儲存會被伺服器的 patch schema 拒絕（本輪沒有實測這個情況），編輯仍保留在頁面上。比賽後可考慮合併同一路徑的連續編輯。

**NOT RUN**：截圖比對（只在 CI）；登入後以真 API 在瀏覽器操作編輯器（app 尚未掛 AuthProvider）；`make backend-test`、`backend-race`、`integration-test`（本輪沒有改 Go 程式，只重產 testdata，跑了 `go test ./internal/model/...`）；Node 24、Firefox／Safari、螢幕報讀器；沒有 live AI 呼叫，沒有讀任何 key 或 secret；沒有 push。

**延到比賽之後（未修）**：L1（畫布上 error／warning 只用邊框顏色）、L2（存活的 mutant F1 whole 欄位 2.5、F2 發布 Idempotency-Key 沿用、F7 發布失敗的 problem；報告對測試範圍的描述）、L3（簡潔規則的 unit 檢查不驗「看得見」）、L4（422 的欄位錯誤在改正後仍顯示）、L5（`planning/tasks.json` 的 T-015 進度紀錄，由 coordinator 更新）；Info I1–I6（I6 的 `checks/docs-check.json` 由 coordinator 在收錄 review 檔後重產）。

### Coordinator 驗證（review 第 1 輪修正，2026-10-26）

`t015-t032-m1` 的三個 commit（036f60b、702607e、17662fb）cherry-pick 到 T-051 的修正之後，沒有衝突以外的修改（只有生成的 `checks/*.json`）。Coordinator 先前代 owner 決定的「同一欄位以使用者自己的值為準」已撤回（違反 AGENTS.md §2 的 no silent overwrite；H-027 只委託介面的簡潔），改為每個欄位編輯先 `test` 舊值、重新套用失敗時只能捨棄。

指令與結果（evidence：`checks/evidence/2026-10-26-T-051-T-052-T-015-T-032-coordinator/`，`exit-codes.txt` 依執行順序；HEAD 312ea90；Node 22.22.0，go1.27.3）：

| 指令 | exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `make backend-test GOFLAGS=-count=1` | 0 | 79 個 package ok |
| `make architecture-check` | 0 | |
| `make backend-numeric` | 0 | |
| `make simulation-reference-test` | 0 | |
| `make ai-eval` | 0 | fake provider |
| `go run ./scripts/gen-electronics-line-data -check` | 0 | 檔案與 generator 的輸出相同 |
| `go test -race -count=1 -timeout 30m`（domainpack、results、import、audit、cmd/worker、generator） | 0 | 16 個 package ok |
| `make integration-test`（新的 dt_test_hkb3c，跑完刪除） | 0 | |
| `npm ci`（web、web-e2e） | 0／0 | |
| `make frontend-test` | 0 | 985／985 |
| `make frontend-build` | 0 | |
| `npm audit --prefix apps/web --omit=dev` | 0 | 0 vulnerabilities |
| `make e2e-test`（單獨執行） | 0 | 490 passed、32 skipped（全部是只在 CI 比對的截圖） |

NOT RUN：截圖比對（只在 CI）；完整的 `make backend-race`；live AI；Node 24。

M1 修正並經 coordinator 驗證，依 H-029 的比賽模式，**T-015 的 hackathon 切片完成**；Low（L1–L5）與 Info 為比賽後的後續。風險（後續）：每個欄位編輯多一個 `test` op，大量編輯的儲存較接近 JSON Patch 的 1000 op 上限，沒有測試上限附近的行為。


## CI run 209 的 e2e 失敗（2026-10-26，coordinator）

CI run 209（85a7750）的 browser checks 在 `apps/web-e2e/tests/editor.spec.ts:100` 失敗：刪除步驟後的 `expect(await focusIsVisible(page)).toBe(true)` 得到 false（其餘 521 個通過）。這段 editor 的程式在 run 208（通過）之後沒有改。

原因：`confirmDelete` 在下一個 animation frame 才把焦點移到 Add step（`model-editor.tsx` 的 `requestAnimationFrame`）。狀態文字在同一次 render 就出現，測試看到狀態文字後立刻讀一次焦點，沒有重試；如果那個 frame 還沒執行，焦點在 body 上。

重現：用暫時的 spec 複本，在按下 Delete 前把 `requestAnimationFrame` 延後 300 ms。commit 中的寫法在同一個 assertion 失敗，新寫法通過（`checks/evidence/2026-10-26-T-015-e2e-race/repro-delayed-focus.log`、兩個複本的 `.txt`；複本已刪除，沒有 commit）。

修正：先以會重試的 `toBeFocused()` 等焦點到 Add step，再檢查焦點看得見。檢查變得更嚴格（指定焦點的位置），沒有放寬。修正後的 `editor.spec.ts` 以 `--repeat-each=3` 執行：39 passed，exit 0（`editor-spec-fixed.log`）。完整的 e2e 由 CI 執行。
