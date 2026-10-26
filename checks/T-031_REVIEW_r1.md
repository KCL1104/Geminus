# T-031 獨立審查第 1 輪（hackathon 切片：在模型頁請 AI 提出修改、在 `/proposals/<id>` 審閱與決定）

**日期：2026-10-26｜審查者：獨立 reviewer（H-003；hackathon 模式 H-029：一輪），沒有參與撰寫｜方式：唯讀審查，除本檔與 `checks/T-043_BOOTSTRAP_REVIEW_r1.md` 外沒有改任何 tracked 檔，沒有 push｜審查的 tree：HEAD 5b66648（含下列 commits）｜結論：CHANGES REQUESTED（M1）**

## 審查範圍

- Commits：fb19e95（client 方法與 `changes.ts`，先寫的測試）、e7d9d2c（畫面、gallery 審閱頁、測試）、f272ffc（報告與 evidence）、coordinator 的 f60fedf（整合驗證）。f60fedf 之後的 commit 沒有改 `features/ai-proposals`、`app/proposals` 或 `app/ui-gallery/proposal`。
- 報告：`checks/T-031_VALIDATION.md`（作者與 coordinator 兩節）；evidence：`checks/evidence/2026-10-26-T-031/`、`checks/evidence/2026-10-26-T-053-T-031-coordinator/`。
- 依據：AGENTS.md §2（AI 只建議、server 計算 risk 與 validation、人批准、stale base 409 不 rebase）、docs/AI_SPEC.md §4、§6、§7（狀態機、needs_input、approval、D-069 (b)）、docs/SECURITY.md §1、§5、DESIGN.md Simplicity（含「沒有原因就不停用」）與新增的 Simplifications 一點、openapi 的 createProposal／getProposal／approveProposal／rejectProposal、`schemas/proposal.schema.json`、`schemas/http.schema.json`（ProposalRequest、Approve、Reject、Approval）、`schemas/model-ir.schema.json`（名稱）、伺服器的 `internal/ai/review/risk.go` 與 `internal/model/patch/policy.go`（只為了確認 AI 可以寫出什麼樣的 ready proposal）。

## 結論

大部分做得對：畫面從不計算 risk（伺服器格是 `server_risk_level`，AI 格是 `provider_risk_claim` 與信心並標明是 AI 的估計，下方的「計入」是伺服器的 `risk_level`）；批准 edit 與批准 scenario 的按鈕、確認問句與結果各自分開，結果依 Approval 的 `mode`；base 已不是最新的 ready 提案不提供 Approve；409 `MODEL_VERSION_CONFLICT` 保留頁面、說明版本 N→M、不 rebase、不重送；needs_input 列出問題、只能 Reject；檢視者與未知角色不能決定；批准與請求的重試沿用 key；摘要、假設、預期、問題與 comment 一律是 React 的文字節點；字串都在兩個 catalog。報告中的數字與 evidence 大致相符（見 I2）。

需要修正的是 M1：人看得懂的差異中，名稱是 AI 在 patch 裡寫的，畫面卻把同名的不同 entity 顯示得一模一樣，也不顯示或隔離不可見與雙向控制字元，所以一個真的變更可以讀起來像什麼都沒變。這正是人做批准時所依據的內容（AGENTS §2 的人批准）。另有五個 Low（其中三個是我以 mutant 證明的測試缺口）。

## 逐項檢查

| 項目 | 結果 | 依據 |
|---|---|---|
| UI 不計算、不調高 risk，AI 的說法標明是 AI 的 | 通過 | `features/ai-proposals/proposal-view.tsx:241-255`；catalog「Checked by Geminus」「What the AI said」「The AI's own estimate; it does not lower the risk.」。`tests/proposal-view.test.tsx:88-106`、`:222-241`（AI claim 較高時計入 AI 的，D-027） |
| 批准 edit 與批准 scenario 分開 | 通過 | `proposal-view.tsx:171`、`:362-381`、`:326-344`（依 `approval.mode`）。`:222-241`：scenario 不建 version、連結為 API 的 `scenario_id` |
| stale／conflicted 不能批准；409 不 rebase、不重送 | 通過（測試缺口見 L2） | `proposal-view.tsx:168`、`:408-420`、`:133-135`、`:172`（有 outcome 後不可決定）；請求的 409 只在使用者按「Reload the model」時重讀（`ask-form.tsx:71-74`、`:117`） |
| AI 的文字只以文字呈現 | 散文通過；patch 中的名稱見 M1、L1 | `proposal-view.tsx:221`、`:229-231`、`:274-276`、`:287-289`、`:471`；`change-list.tsx` 沒有 `dangerouslySetInnerHTML`。我的暫時 probe：名稱 `<img src=x onerror=…><a href="javascript:…">approve</a>`、一個指向 `javascript:…` 的 markdown 連結與 `https://evil.example` 經 `ChangeList` 後沒有任何 img、a、script、iframe 或 `[onerror]`；10,000 字的名稱完整呈現（CSS `overflow-wrap: anywhere`） |
| idempotency key 在重試時沿用 | 批准與請求通過；拒絕見 L3 | `proposal-view.tsx:126`、`ask-form.tsx:59-61`；`tests/proposal-view.test.tsx:192-207`、`tests/proposal-ask.test.tsx:138-150`（同 key）與 `:152-170`（新版本換新 key） |
| needs_input 顯示問題、沒有 Approve | 通過 | `proposal-view.tsx:225-235`、`:408`；`tests/proposal-view.test.tsx:322-332` |
| 字串來自 catalog（en＋zh-Hant） | 通過 | `i18n` 測試、`strings.spec.ts`（coordinator 的 e2e log：`proposal.spec.ts:153` 兩語通過）；新字串沒有任務 ID |
| 簡潔規則 | 通過（見 L4） | `tests/proposal-simplicity.test.tsx` 33 個；Send 在有未存編輯時 blocked 並以 `aria-describedby` 指向原因（`ask-form.tsx:105-112`） |
| 報告數字 | 大致相符（I2） | 作者 vitest 40 檔 875（`frontend-test-3.log`）；coordinator 970、e2e 490 passed／32 skipped（`e2e-test.log`）、`exit-codes.txt`（backend-test 的 2 是 check-architecture 逾時，重跑 0，報告已寫）。我重跑六個 T-031 測試檔：9／10／19／33／27／7，共 105 |

## 發現

### High

無。

### Medium

**M1：差異中的名稱是 AI 寫的，同名（或只差不可見字元）的不同 entity 看起來一樣，雙向控制字元也沒有隔離；真的變更可以讀成「沒有變」**

- 位置：`apps/web/features/ai-proposals/changes.ts:101-121`（`namer` 把 ID 換成名稱：新 entity 的名稱取自 patch，也就是 AI 寫的）、`:201-216`（參照欄位 `usesResource`、`from`／`to`、`calendar`、`entity` 以名稱顯示）、`:8-9`（註解「Names and values come from the model and the patch, never from the AI's prose」）；`apps/web/features/ai-proposals/change-list.tsx:23-44`、`:86`、`:111`、`:125`（名稱直接插入 `t()` 的字串，沒有 ID、沒有 `<bdi>`／`dir`）；`proposal.module.css` 沒有任何 `unicode-bidi`。報告 `checks/T-031_VALIDATION.md:29`「名稱一律取自模型與 patch，不取自 AI 的文字」把 patch 中的名稱當成可信的。
- 為什麼伺服器擋不下：IR 的名稱只有 1–200 字元（`schemas/model-ir.schema.json` 的每個 `name`），沒有字元集或唯一性規則；AI edit 可以新增與修改 `resources`、`nodes`（`internal/model/patch/policy.go:20`）；不可見字元只在**送給** AI 的 context 中移除（`internal/ai/aicontext/sanitize.go:192-195`），AI 的**回答**沒有。伺服器 risk 對「新增 resource、process 改用另一組 resource」只給 at least medium（`internal/ai/review/risk.go:37-58`）。同一個 codebase 在 T-043 已對同名組織加上 ID（`apps/web/lib/auth/bootstrap.ts:73`、`apps/web/app/_components/account.tsx:18`），這裡沒有。
- 失敗情境（以暫時的 vitest probe 在 `ChangeList` 上重現，範例產線 `sample-line.ts`；probe 已刪除）：patch 為 `add /resources/-` `{id: "resource_01m4ydqk00e1ectr0n1c5x0009", name: "ICT tester", capacity: 9, …}`，再 `test`＋`replace /nodes/<ict>/requires/0/resource_id` 為新 ID。畫面的兩行是「Added Resource ICT tester」（其下一項「Capacity: 9」）與「ICT Resource it needs: “ICT tester” → “ICT tester”」：ICT 改用一組容量 9 的新機台（原本 2 台），差異卻讀成沒有變。名稱改成 `"ICT tester\u200B"`（結尾零寬空白）時兩行看起來也完全相同。伺服器會判 medium，審閱者看到「medium、所有檢查通過」與一行看似 no-op 的變更。雙向字元：把 Burn-in 改名為 `"Burn-in\u202E84 → 02"`，DOM 的文字是「Burn-in Name: “Burn-in” → “Burn-in\u202E84 → 02”」，沒有任何隔離；依 Unicode Bidi Algorithm，瀏覽器把 U+202E 之後到該列結束的字元由右向左排，畫面出現 patch 中沒有的「20 → 48」（這一點依演算法推得，沒有截圖）。JSON Patch 的 disclosure 有 ID，但 `JSON.stringify` 也原樣輸出 U+202E 與 U+200B。
- 最小修正：(1) 在 `change-list.tsx` 顯示名稱的地方，當一個名稱在 base 與套用後的模型中屬於兩個以上同類 entity，或該 entity 是這個 patch 新增的，加上 ID（同 `organization.named` 的「{name} ({id})」）；參照欄位（Resource it needs、連接的 from／to、班表、物件類型）也一樣。(2) 插入前把雙向控制字元（U+061C、U+200E–U+200F、U+202A–U+202E、U+2066–U+2069）與其他 `\p{Cf}`（U+200B–U+200D、U+2060、U+FEFF 等）換成看得見的記號（例如「⟨U+202E⟩」），並以 FSI／PDI（U+2068…U+2069）或 `<bdi>` 包住每個名稱與文字值；AI 的散文（摘要、假設、預期、問題）可套同一個函式。(3) 加 DOM 測試：上面的替換情境、零寬與 U+202E 名稱經 `ProposalRoute` 呈現，並把 `hostile` 範例的 patch 換成帶這些名稱。伺服器端拒絕 IR 名稱中的 `\p{Cf}` 是 contract 變更，屬於 owner 的決定（PROPOSED），不在本切片。

### Low

**L1：patch 中的名稱以文字呈現，沒有 DOM 層的測試（mutant 存活）**

- 位置：`apps/web/features/ai-proposals/change-list.tsx:111`；測試 `apps/web/tests/proposal-view.test.tsx:412-428` 用的 `hostile` 範例只有散文是惡意的，patch（`app/ui-gallery/proposal/samples.ts:69-77`、`:215-230`）的名稱都是 base 的；`tests/proposal-changes.test.ts:149-153` 只測純函式回傳原字串。
- 失敗情境（mutant MA）：把 `:111` 的 `{element(c.subject)}` 改成 `<span dangerouslySetInnerHTML={{ __html: element(c.subject) }} />`，`proposal-view`、`proposal-changes`、`proposal-simplicity`、`proposal-preview` 四個檔 89／89 通過；`npx eslint --max-warnings=0` 對這個 mutant 也是 exit 0（設定沒有 `react/no-danger`），所以 `make frontend-test` 會全綠。報告「惡意文字…不成為 img／script／iframe／連結」對 patch 名稱沒有被斷言。
- 修正：M1 (3) 的 DOM 測試加上帶 markup 與 `javascript:` 的名稱即可擋下；可另開 `react/no-danger`。

**L2：「409 之後不重送」沒有斷言（mutant 存活）**

- 位置：測試 `apps/web/tests/proposal-view.test.tsx:267-288`；程式 `apps/web/features/ai-proposals/proposal-view.tsx:133-135`（目前正確）。報告 `checks/T-031_VALIDATION.md:143` 寫「409：…沒有重送」。
- 失敗情境（mutant MB）：在 409 分支加一行 `void api.approveProposal(id, { expected_base_version_id: page.model.latest_version.id }, newIdempotencyKey())`（以新 base、新 key 自動再批准一次），`proposal-view`、`proposal-simplicity`、`proposal-ask` 69／69 通過：替身把它當成已決定的 proposal 回 409，沒有新 version，而測試只檢查 version 數與 createProposal 的次數。
- 修正：在該測試加 `expect(decisions(api)).toHaveLength(1)`（以及 `decisions(api)[0].body` 是原 base）。

**L3：拒絕的重試沿用 key 沒有測試（mutant 存活）**

- 位置：`apps/web/features/ai-proposals/proposal-view.tsx:151`；`tests/proposal-view.test.tsx:243-264` 只有成功的拒絕。
- 失敗情境（mutant MC）：把 `:151` 改成每次都 `newIdempotencyKey()`，69／69 通過。伺服器的 D-030 會擋下第二個 effect，所以後果只是「已決定」的錯誤而不是第二次拒絕，但報告與 D-011 宣稱重試沿用 key。
- 修正：一個測試：`refuseNext` 503 → 再按 Reject proposal → 兩次的 key 相同；改了原因後 key 不同。

**L4：422 或風險改變的 409 之後仍提供 Approve，而伺服器會一直拒絕；說明也不提風險已變**

- 位置：`apps/web/features/ai-proposals/proposal-view.tsx:136-141`、`:408-420`；docs/AI_SPEC.md:103（D-069 (b) APPROVED：revalidation 失敗或 risk 改變的 proposal 維持 ready，只能 reject 後重新提出）；catalog `error.LIFECYCLE_CONFLICT`「This is not possible in its current state.」（`messages/en.ts:114`）。
- 失敗情境：pack 的規則變了，伺服器重算的 risk 高於存下的 → 批准回 409 `LIFECYCLE_CONFLICT`（`/risk_level`）→ 頁面重讀後仍顯示存下的較低 risk 與 primary 的 Approve，旁邊是一般的「目前的狀態不允許」；再按只會得到同樣的拒絕。本切片對 stale base 已因「永遠不可能成功」而不顯示 Approve（`checks/T-031_VALIDATION.md:125`），這裡的處理不一致。
- 修正：對 422 `MODEL_VALIDATION_FAILED` 與 `LIFECYCLE_CONFLICT` 在 `/risk_level`／`/server_risk_level` 的回答，換成一個 warning note（「伺服器重新檢查後不能批准，風險或檢查已改變；請拒絕並重新提出」）並只提供 Reject；503 仍可重試。

**L5：已登入的提案頁與模型頁的 Send 沒有在瀏覽器中走過 BFF**

- 位置：`apps/web-e2e/tests/proposal.spec.ts`（每個測試都開 `/ui-gallery/proposal`）；`apps/web-e2e/fake/servers.mjs` 沒有 ai-proposals 的 route；報告 `checks/T-031_VALIDATION.md:189` 的 NOT RUN 理由（「app 尚未掛 AuthProvider」）在 T-043 後續整合後已不成立，coordinator 一節沒有補上。
- 情境：H-029 的 demo 流程（模型頁 Send → `/proposals/<id>` → 批准）在真的 route 上經 `RequireSession`、bootstrap 的 organization 與 proxy 的 CSRF header；這些只在 vitest（替身）與 gallery 中測過。
- 修正：在 fake API 加 `POST /v1/models/{id}/ai-proposals`、`GET /v1/ai-proposals/{id}`、approve／reject（固定答案即可），並在 `bootstrap.spec.ts` 或 `proposal.spec.ts` 加一個以 editor 登入、送出、到提案頁、批准的測試；或在報告把它列為 NOT RUN 並寫明新的理由。

### Info

**I1：gallery 預覽的註解已過時**：`apps/web/app/ui-gallery/proposal/preview.tsx:2-4`「the app mounts no session yet, so the signed-in page cannot render in a browser」在 T-043 後續之後不正確。

**I2：報告的小誤差**：`checks/T-031_VALIDATION.md:144` 寫 `proposal-ask.test.tsx` 8 個 test，實際 9 個（含閉環）；`checks/evidence/2026-10-26-T-031/mutants-M6-rerun.txt` 只跑 M6，卻印「total 21: 1 killed, 0 survived」。

**I3：`failureProblem` 以 `in` 查 error code**：`apps/web/features/ai-proposals/proposal-view.tsx:60` 的 `code in errorCodeStatus` 對 `toString`、`constructor`、`__proto__` 也成立（與 T-045 r1 I2 同一類）。依 `lib/api/problem.ts:131-148` 推算最後會成為 protocol problem、不會丟出例外，而且 code 來自伺服器的 enum；改用 `Object.hasOwn` 即可。

**I4：輪詢只更新 proposal**：`proposal-view.tsx:89-105` 在 generating → ready 時沒有重讀 model；若生成期間別人發布了新版本，頁面仍提供 Approve（`:168` 的 `stale` 用的是開頁時的 model），批准得到 409 並顯示 conflicted 的說明。安全，只是多一次必然失敗的批准。

**I5：決定時的 401 沒有登入連結**：`proposal-view.tsx:352` 只顯示 problem；T-043 的 `RemoteView` 對 401 會加 `SignInLink`。與 T-043 報告列的「草稿儲存時的 401 只顯示 problem」相同。

**I6：PROPOSED 與 APPROVED**：批准語意的依據 D-003、D-004、D-016、D-027、D-030、D-067–D-069 在 `docs/DECISIONS_AND_TBD.md` 為 APPROVED（D-069 (b) 的 B1 仍待 owner）。報告給 owner 的問題 2–4（獨立審閱頁、stale 不顯示 Approve、mode 用 Select）由 coordinator 在 hackathon 模式下接受，5、7、8、9 延後；這些是 coordinator 的決定，不是 owner 的 APPROVED，報告也是這樣寫；T-031 在 tasks.json 維持 `not_started`，只記 completion_evidence（`reviewer: null`）。AI edit 不能寫 `views`（T-056 的位置）由 coordinator 判定不需新決定。

**I7：generating 每 1.5 秒輪詢，沒有上限**（報告已列為殘餘風險）。

## 我執行了什麼（Node 22.22.0；repo 要求 24）

| 指令 | Exit | 結果 |
|---|---|---|
| `npm ci --prefix apps/web --engine-strict=false` | 0 | |
| `make frontend-test`（HEAD 5b66648） | 0 | lint 0 warning、typecheck、vitest 43 檔 985 個全過 |
| `make frontend-build` | 0 | `/proposals/[proposalId]`、`/ui-gallery/proposal` 為動態 route |
| `env -C apps/web npx vitest run tests/proposal-ask.test.tsx tests/proposal-view.test.tsx tests/proposal-changes.test.ts tests/proposals-api.test.ts tests/proposal-simplicity.test.tsx tests/proposal-preview.test.tsx --reporter=json` | 0 | 105 個（9、27、10、7、33、19） |
| 暫時的 `apps/web/tests/zz-review-probe.test.tsx`（`--silent=false`；已刪除） | 0 | 惡意 markup 與長名稱只是文字；同名與零寬的替換讀成 no-op；U+202E 與 U+200B 原樣進入 DOM，沒有 `bdi`／`[dir]`（M1） |
| Mutant MA（`change-list.tsx:111` 以 HTML 呈現）：`env -C apps/web npx vitest run tests/proposal-view.test.tsx tests/proposal-changes.test.ts tests/proposal-simplicity.test.tsx tests/proposal-preview.test.tsx` | 0 | 存活（89／89）→ L1 |
| `env -C apps/web npx eslint --max-warnings=0 features/ai-proposals/change-list.tsx`（MA 仍在時） | 0 | lint 也不擋 `dangerouslySetInnerHTML` → L1 |
| Mutant MB（409 後以新 base 與新 key 再批准）：`… tests/proposal-view.test.tsx tests/proposal-simplicity.test.tsx tests/proposal-ask.test.tsx` | 0 | 存活（69／69）→ L2 |
| Mutant MC（拒絕每次換 key）：同上三個檔 | 0 | 存活（69／69）→ L3 |

每個 mutant 之後以 `git checkout -- <file>` 還原並確認 `git status` 乾淨。

沒有執行：`make e2e-test`（共用的固定 port，依指示只讀 evidence 的 log）、`make spec-check`（會重產 tracked 的 `checks/*.json`）、Go 測試與 `make integration-test`（這一片沒有 Go、contract、schema 或 migration 的變更；作者暫時的 Go 測試只讀了 log）、`npm ci --prefix apps/web-e2e`、bidi 呈現的瀏覽器截圖、Node 24、任何 live AI 呼叫。沒有讀任何 key 或 secret。

結束前移除了我建立的 `apps/web/node_modules`、`apps/web/.next`、`apps/web/next-env.d.ts` 與 `apps/web/tsconfig.tsbuildinfo`（都在 .gitignore 中）；`git status --ignored` 除兩份審查檔外乾淨。

## 結論

**CHANGES REQUESTED**：M1（差異中的名稱需要以 ID 區分同名 entity，並讓不可見與雙向控制字元看得見、被隔離，加上 DOM 測試）。L1–L5 建議一併處理（L1 與 M1 的測試是同一個）；I1–I7 不阻擋。
