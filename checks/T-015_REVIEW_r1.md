# T-015 獨立審查第 1 輪（hackathon 切片：模型頁的 2D 編輯器）

**日期：2026-10-26｜審查者：獨立 reviewer（H-003；hackathon 模式 H-029：一輪），沒有參與撰寫｜方式：唯讀審查，除本檔與 `checks/T-032_REVIEW_r1.md` 外沒有改任何 tracked 檔，沒有 push｜結論：CHANGES REQUESTED（M1）**

## 審查範圍

- Commits（分支 `claude/lucid-meitner-vq1awo`）：df6a644、f70bec7、71ccdd8、0993814、74cb9ae、55d0703；coordinator 的 8eecee4（tokens test）與 492fae6（整合）中與 T-015 有關的部分。
- 報告：`checks/T-015_VALIDATION.md`（作者與 coordinator 兩節）；evidence：`checks/evidence/2026-10-26-T-015/`（含 `coordinator/`）。
- `planning/tasks.json` 的 T-015（scope 與 hackathon 切片一行）。
- 依據：AGENTS.md（§2 P0 invariants）、DESIGN.md（Simplicity 與其細化、Buttons／Disabled、Canvas、Simplifications in the app、tokens）、docs/TWIN_IR_SPEC.md（§1、§6 D-014／D-020、§7 views）、docs/PRD.md REQ-006、ADR-016、openapi/openapi.yaml 的 getDraft／patchDraft／publishVersion／getModelVersion、`apps/web/lib`（api client、etag、draft session、selection store）、`internal/models` 的 PatchDraft（只看語意是否與編輯器的假設一致）。

## 結論

切片的主要功能都在，而且做得紮實：IR → React Flow 是投影（ID 與端點都是 IR 的，沒有第二份模型）；每個編輯是 JSON Patch，D-020 的 identity `test` 都在，`TestEditorVectors` 用伺服器的 manual-draft policy 驗證；儲存帶 `If-Match`，回應的 ETag 必須等於 body 的 revision；409 保留所有編輯、畫布轉唯讀、從不自動 rebase；有錯誤、未存、過時或無變更時 Publish blocked 並寫出原因；字串都來自 catalog；gallery 預覽預設 404。報告中的數字與 evidence 相符。

需要修正的只有一項 Medium：409 之後的「重新套用」對同一欄位是 last-writer-wins 的 lost update，畫面不告訴使用者，而這個語意是 coordinator 代替 owner 決定的（M1）。其餘為 Low／Info。

## 發現

### High

無。

### Medium

**M1：409 後「重新套用」會覆寫別人在新 revision 對同一欄位的修改，畫面不說；這個語意由 coordinator 代 owner 決定**

- 位置：`apps/web/features/model-editor/model-editor.tsx:190-206`（`reapply` 只看 `applyPatch(r.data.ir, pending).ok`）；`apps/web/features/model-editor/edits.ts:69-78`、`:205-210`、`:212-218`（rename、機率、`setMember` 在 `replace`／`remove` 前只 `test` 元素的 ID，不 test 欄位原本的值）；`apps/web/lib/state/draft.ts:135-139`（`reapplyLocal`）。決定的出處：`checks/T-015_VALIDATION.md:155`（作者把它列為給 owner 的問題 5）、`:166`（coordinator 以「H-027 的簡潔原則之內」自行決定「同一欄位以使用者自己的值為準」）。
- 問題：REQ-006（`docs/PRD.md:30`：「不得覆寫…或偷偷 rebase」）與 AGENTS.md §2（「不 silent overwrite / auto-rebase」）要擋的正是 lost update；ADR-016（`docs/adr/ADR-016-frontend-server-state-boundary.md:15`）寫 conflict 只能由使用者明確 reload／discard 離開。T-015 加了第三個出口（reload 後保留編輯），這是 T-014 移交給 T-015 的 UX（`checks/T-014_VALIDATION.md:76`），本身沒問題；但它只靠 D-020 的 identity test，所以別人在新 revision 改過、而本機也改過的欄位，會被本機的值蓋掉，使用者從頭到尾看不到別人的值。H-027 委託 coordinator 的是介面簡潔（`docs/DECISIONS_AND_TBD.md` H-027：「agent 認為多餘的列給 owner 決定，不自行刪除」），並行語意不在其中；問題 5 本來就是給 owner 的。
- 失敗情境（我以臨時 vitest 重現，替身 API、真的 client 與 draft session；測試已刪除）：A 把「Before AOI」的 capacity 由 10 改為 12 並儲存（revision 4）。B 仍在 revision 3，把同一個 capacity 改為 25、儲存得到 409，按「載入目前的草稿並重新套用我的變更」→ 狀態「Unsaved changes. Draft revision 4.」、訊息「Your changes are on draft revision 4 now. Check them, then save.」、欄位顯示 25 → B 儲存 → revision 5 的 capacity 為 25。A 的 12 消失，B 沒有看過 12。
- 最小修正（擇一）：(a) 欄位編輯的 ops 在 `replace`／`remove` 前多一個舊值的 `test`（例如 `{op:"test", path:"/nodes/4/capacity", value:10}`；`add` 一個原本沒有的成員時 test 不適用，可改為重新套用前比較 base 與新 revision 該路徑的值）。同一欄位被別人改過時本機的 `applyPatch` 失敗，走既有的「套不上，只能捨棄」路徑（`reapplyFailed`）；加一個「同欄位衝突」的測試。D-020 允許 test 不受 allowlist 限制，`TestEditorVectors` 只需重產。(b) 若要保留 last-writer-wins，由 owner 以 H-記錄明確接受，並在重新套用後列出兩邊都改過的欄位（別人的值與自己的值）再讓使用者儲存。

### Low

**L1：畫布上步驟的 error／warning 只用邊框顏色表示**

- 位置：`apps/web/features/model-editor/canvas.tsx:30-40`（`StepNodeView` 只畫名稱與類型）、`apps/web/features/model-editor/canvas.module.css:56-64`（`data-severity` 只改 `border-color`）。報告 `checks/T-015_VALIDATION.md:40` 的「名稱加『有錯誤』」只在 `aria-label`，畫面上沒有。
- 問題：看得見的步驟上，error、warning 與沒有 issue 的差別只有紅、琥珀、灰三種 1 px 邊框（WCAG 1.4.1 的關注點）。問題清單與屬性面板有文字，所以資訊不是只在顏色中，但在畫布上無法不靠顏色找到有錯的步驟或分辨錯誤與警告。
- 情境：色覺辨識困難的使用者在 41 步的產線（T-051 範本）上看不出哪幾步是紅框、哪幾步是琥珀框。
- 修正：在 `.kind` 那一行加 catalog 的嚴重度字（例如「Process · Error」）或圖示，或 warning 改用非顏色的線索（虛線）；editor.test 加一個看得見的文字斷言。

**L2：存活的 mutant，以及報告對測試範圍的描述過寬**

- F1：拿掉 `apps/web/features/model-editor/fields.tsx:70` 的 `(whole && !Number.isInteger(n))` → `tests/editor.test.tsx` 24/24 通過（佇列容量輸入 2.5 會被送出，靠伺服器 422 擋）。
- F2：`model-editor.tsx:218` 改為每次按 Publish 都換新的 Idempotency-Key → 24/24 通過。替身 API 的 `Call`（`apps/web/app/ui-gallery/editor/stand-in.ts:38`）沒有記錄 Idempotency-Key，`tests/editor.test.tsx:204-219` 無從斷言；報告 `checks/T-015_VALIDATION.md:91` 寫「發布（If-Match、base、key）」與實際不符（client 層的 `tests/api.test.ts` 只驗 header 有送，不驗同 revision 重試沿用）。
- F7：`model-editor.tsx:222` 不再顯示發布失敗的 problem → 24/24 通過：沒有任何發布 409／422／503 的測試。
- 情境：之後有人改壞「同 revision 重試沿用 key」或發布失敗的顯示，測試全綠。
- 修正：替身記錄 Idempotency-Key；加「發布失敗顯示 problem（含 request ID）、同 revision 重試沿用同一 key」與「whole 欄位輸入 2.5 不送出」兩個測試；或把報告的描述改成實際範圍。

**L3：簡潔規則細化後的 unit 檢查不驗「看得見」**

- 位置：`apps/web/tests/simplicity.test.tsx:55-60`、`apps/web/tests/editor.test.tsx:422-425`。
- 問題：DESIGN.md Simplicity 第 2 條寫「each blocked control names visible, non-empty text by `aria-describedby`」，unit 測試只檢查被指向元素的 `textContent` 非空；原因元素若 `hidden` 或在 `aria-hidden` 之下仍會通過。看得見只由 `apps/web-e2e/tests/simplicity.spec.ts` 檢查，而且只在 `/ui-gallery/editor`（已登入的真頁面在瀏覽器中尚未檢查，T-043）。
- 情境：把 Publish 的原因 `<p>` 改成 `hidden`（仍有文字），unit 測試照樣綠。
- 修正：unit 檢查排除 `hidden`、`aria-hidden` 祖先與 `display:none` 的 inline style；或在 DESIGN.md 寫明「visible」由瀏覽器檢查。

**L4：被拒儲存（422）的欄位錯誤在使用者改正後仍顯示**

- 位置：`model-editor.tsx:100`、`:180-186`、`:210`：`refused` 只在儲存成功或捨棄時清空。
- 情境：capacity 輸入 0 → 儲存被拒（422）→ 欄位紅、步驟「with errors」；改成 5（尚未儲存）→ 欄位與步驟仍顯示 IR_SCHEMA_INVALID，直到下一次儲存。錯誤是針對已被拒的 candidate，不是目前畫面上的 IR。
- 修正：在 `edit()` 清掉 refused（或只清該步驟的），或把它標為「上次被拒的儲存」。

**L5：`planning/tasks.json` 的 T-015 進度紀錄過時**

- 位置：`planning/tasks.json:958` 一帶（T-015 `completion_evidence[0].summary`）。
- 問題：summary 寫「status 保持 not_started：check_decisions 要求依賴的 T-045 先到 review 或 done」，但 status 已是 `in_progress`；數字（frontend-test 600、e2e 143 passed）是整合前的，coordinator 整合後的 642／162（以及之後與 T-032 合併的 770／257）沒有記入。
- 修正：coordinator 更新該筆的 summary，或加一筆整合後的紀錄。

### Info

- **I1（已驗證）**：T-051 的正式範本現已整合（`internal/domainpack/electronicsline/templates/electronics_assembly_test_line.json`，41 node、47 flow、7 resource，含 domain_attributes、metadata、calendars）。我以臨時 vitest 對它產生 286 組編輯 patch（每個 node 的刪除、改名、移除名稱、移動；每個 queue 的容量設定與移除；每個時間參數；每個 flow 的斷開；決策機率；批次三欄；8 種新增；連接），再以臨時 Go 測試用 `patch.ManualDraft` 套用：全部被接受、schema-valid、model hash 與編輯器算的相同；`toFlow` 的 node／edge ID、端點與 positions 與 IR 完全相同。報告列為 NOT RUN 的「真正的範本」在 adapter／patch 層已補上；在瀏覽器中操作該範本仍未跑。
- **I2**：新步驟的預設值：process／delay 為 constant 1，source 為 exponential mean 1（`edits.ts:106`）；報告 `checks/T-015_VALIDATION.md:139` 只寫「constant」。這是編輯器的初值，不是指標或資料，屬可接受；建議在屬性面板標示「預設值，請修改」。
- **I3**：`apps/web/features/model-editor/properties.tsx:212` 在 resource 不存在時顯示寫死的 `"?"`，不是 catalog 字串（伺服器會另報 UNKNOWN_REFERENCE）。
- **I4**：第一次拓樸編輯或移動會把 layered 版面整份寫入 `views.2d.positions`（已記錄），所以「新增後再刪除」不會回到原本的 model hash，之後可能發布只差 positions 的版本。符合 TWIN_IR_SPEC §7（view 資料），列出供知悉。
- **I5**：每個步驟一個 Tab 停點；T-051 範本有 41 步，鍵盤使用者要經過全部才到屬性面板。coordinator 已暫時接受，owner 可再定（roving tabindex 或「跳到屬性」）。
- **I6**：`checks/docs-check.json` 的 `markdown_files` 會因本輪的兩個 review 檔在下一次 `make spec-check` 由 118 變為 120；我依指示沒有改 tracked 檔，請 coordinator 重產。

## 已核對、沒有問題的部分

- **沒有第二份模型**：`flow.ts` 的 node data 只有 label、kind、severity；`model-editor.tsx` 的工作中 IR 是 `applyPatch(remote.ir, pendingOps)`；selection store 只收 IR 的 node／resource／flow ID（`lib/state/selection.ts`）。
- **JSON Patch**：`patch.ts` 在副本上逐一套用、全有或全無、pointer 只解碼一次、`~` 的拼法檢查、`-` 只用於 add、test 以 JSON 值比較；ID 的 pattern（`^[A-Za-z]…`）排除 `__proto__`，其他地方都用 `Object.hasOwn`。刪除步驟依序（由後往前）先 test 再 remove flows、metrics、constraints、node 與 position（mutant F5 被擋）。
- **伺服器語意一致**：`internal/models/models.go` 的 PatchDraft：revision 不符為 409、patch issue 或 schema 不合為 422（不存）、semantic issue 隨草稿存下（D-028）；編輯器對 422 的 details 在它送出的 candidate 上定位，對已存草稿的 issues 在該草稿上定位。
- **409 與 stale base**：儲存帶 `If-Match: "draft-revision-N"`；`checkDraft` 要求 ETag 等於 body 的 revision；發布帶 `If-Match`、`expected_base_version_id` 與 Idempotency-Key；409 時畫布唯讀（F6 被擋）、Save blocked、不自動重送；重新載入本身不重新套用（F8、作者的 M5 被擋）；套不上時只能捨棄（F3 被擋）。
- **簡潔規則與其細化**：每個狀態至多一個 primary、按鈕不同名、連結不重複、沒有 `disabled`、每個 blocked 都以 `aria-describedby` 指向非空文字；coordinator 的 mutant（拿掉 Save 的描述）在 evidence 中為紅（`coordinator/simplicity-refined-red-mutant-no-reason.log`）。H-027 禁止的是佔位與永遠停用的控制項，Save／Publish 等待條件並寫出原因，細化合理，DESIGN.md 已同步。
- **a11y 與兩語**：Escape 取消選取且焦點留在步驟（F4 被擋）、Delete 先確認、方向鍵移動；React Flow 的英文 `node`／`edge`／`Edge from…` 換成 catalog；e2e 的 axe（en／zh-Hant × 淺／深 × 選取、選單、對話框、409、檢視者）與字串測試都通過（`coordinator/e2e-test-2.log`、`checks/evidence/2026-10-26-T-051-T-032-coordinator/e2e-test-rerun.log`，editor.spec 13 個皆 ✓）。
- **文字不會變成 markup**：`apps/web/features`、`app`、`ui`、`lib` 沒有 `dangerouslySetInnerHTML`、`innerHTML` 或 `eval`；伺服器訊息與 IR 的名稱都以 React 文字輸出，未知 code 的伺服器英文訊息標 `lang="en"`。
- **依賴**：`@xyflow/react` 精確釘在 12.12.0（MIT），lock 中 `@xyflow/system` 0.0.83、`zustand` 4.5.7、`classcat` 5.0.5 皆無 install script；ADR-011 與 THIRD_PARTY_NOTICES 已更新。
- **報告與 evidence**：作者的 600 tests／143 passed、coordinator 的 642／162 passed、mutation 13（M13 補測試後 killed）、Go 端兩個 mutant 為 FAIL，都與 log 相符；NOT RUN 的項目（截圖、登入後的真 API、Node 24、螢幕報讀器）都老實列出。

## 我執行的指令

環境：Node 22.22.0、npm 10.9.4（repo 要求 Node 24，以 `--engine-strict=false` 安裝）、go1.27.3；共用機器 load average 約 20。

| 指令 | exit | 結果 |
|---|---|---|
| `npm ci --prefix apps/web --engine-strict=false` | 0 | |
| `make frontend-test`（lint、typecheck、vitest） | 0 | 34 個檔案、770 個 test，與 coordinator 的 `frontend-test-rerun.log` 相同 |
| `make frontend-build` | 0 | 含 `/models/[modelId]`、`/ui-gallery/editor` |
| 臨時 vitest：T-051 範本的 286 組編輯 patch（產生到 scratchpad，測試檔已刪除） | 0 | toFlow 的 ID、端點、positions 相同 |
| 臨時 `go test -count=1 -run TestZZReviewTemplateVectors ./internal/model/patch/`（測試檔已刪除） | 0 | 286 組全部被 ManualDraft 接受、schema-valid、hash 相同 |
| 臨時 vitest：同欄位 409 後重新套用（M1 的重現，測試檔已刪除） | 0 | 伺服器最後的 capacity 為 25，別人的 12 被覆寫、畫面未提示 |
| `/home/user/Geminus/.venv/bin/python scripts/check_docs.py`（只做文件連結與 code fence 檢查；產生的 `checks/docs-check.json` 之後還原） | 0 | PASS，120 個 Markdown 檔（含本輪兩個 review） |
| 8 個 mutant（逐一改一行、跑一個測試檔、`git checkout --` 還原，結束時 `git status` 乾淨） | — | 被擋：F3 重新套用不檢查是否套得上、F4 Escape 交給 React Flow、F5 由前往後刪除、F6 conflict 中仍可編輯、F8 重新套用前不 dispatch remoteLoaded；存活：F1、F2、F7（L2） |

## 沒有執行的

- `make e2e-test`：依指示不執行（固定 port 與其他 agent 共用）；改讀 `coordinator/e2e-test-2.log` 與 `checks/evidence/2026-10-26-T-051-T-032-coordinator/e2e-test-rerun.log`。
- `make spec-check`：會重寫 tracked 的 `checks/*.json`，依「只改兩個 review 檔」沒有執行（見 I6）。
- `make backend-test`、`backend-race`、`integration-test`：本切片只改兩個 Go 測試檔與一個 testdata；只跑了上表的臨時 Go 測試。
- 截圖比對、登入後以真 API 在瀏覽器操作編輯器（app 尚未掛 AuthProvider）、Node 24、Firefox／Safari、螢幕報讀器：NOT RUN。
- 沒有任何 live AI 呼叫，沒有讀任何 API key 或 secret。

## 要 ACCEPT 需要修正

- M1：同欄位的重新套用不再靜默覆寫（加舊值 `test` 走「套不上」路徑並加測試），或由 owner 以 H-記錄明確接受 last-writer-wins 並在畫面列出兩邊都改過的欄位。
