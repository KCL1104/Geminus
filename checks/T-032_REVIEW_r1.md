# T-032 獨立審查第 1 輪（hackathon 切片：從 scenario 執行、進度、Marey 圖與主要指標）

**日期：2026-10-26｜審查者：獨立 reviewer（H-003；hackathon 模式 H-029：一輪），沒有參與撰寫｜方式：唯讀審查，除本檔與 `checks/T-015_REVIEW_r1.md` 外沒有改任何 tracked 檔，沒有 push｜結論：CHANGES REQUESTED（M1）**

## 審查範圍

- Commits（分支 `claude/lucid-meitner-vq1awo`）：f21b02a、64b04f6、5c4746c、ee5850a、cae974b；coordinator 的 8eecee4（tokens test 的 package import 規則）與 492fae6（T-051＋T-032 整合）。
- 報告：`checks/T-032_VALIDATION.md`（作者與 coordinator 兩節）；evidence：`checks/evidence/2026-10-26-T-032/`、`checks/evidence/2026-10-26-T-051-T-032-coordinator/`。
- `planning/tasks.json` 的 T-032（scope、D-090 gate、hackathon 切片一行）。
- 依據：AGENTS.md（§2）、DESIGN.md（Marey Strip、Compare Table、Simplicity 與 T-032 的兩點簡化、Buttons Run）、docs/SIMULATION_SPEC.md（§5 統計、§7 比較與 D-088、§8 取消、§9 trace 與 D-039）、docs/DECISIONS_AND_TBD.md 的 D-088（含 (f) 負 baseline）、D-089、D-090、openapi/openapi.yaml 的 createSimulation、getSimulation、cancelSimulation、getSimulationResults、downloadSimulationTrace、compareSimulations、`apps/web/lib/api/client.ts`、`internal/simulation/trace`（trace 的寫法）。

## 結論

切片完成度高：兩個 run 共用一份明確的 config（§7 的可比條件）、各自的 Idempotency-Key 且同設定重試沿用；狀態依 `progress.revision` 只往前；失敗的 run 以 API 的 problem 顯示（含 request ID）；比較的值、delta、status、reason 都直接取自伺服器，沒有的值顯示「無資料」，每個數字以 `<data value>` 保留伺服器的原值；不可比（D-088）只並列、不給 delta；Marey 的幾何是純函式、決定性、不延伸截斷的 trace、放不下的事件結束該線而不猜。我另用真的 engine 跑 T-051 範本（有限 buffer 的阻塞、rework 迴圈、failure、abandon、批次）產生 trace，幾何沒有遺失任何線（I1）。報告的數字與 evidence 相符。

需要修正的是一項 Medium：取消在第一個 run 的請求失敗就停，另一個 run 不會被取消（M1），這是 baseline 剛完成時的常見時序。其餘為 Low／Info。

## 發現

### High

無。

### Medium

**M1：取消在第一個失敗就中止；剛完成的 baseline 回 409 時，scenario 不會被取消**

- 位置：`apps/web/features/simulations/use-run-pair.ts:71-88`（依序對每個仍在進行的 run 送 cancel，`if (!r.ok) { dispatch cancelFailed; return; }` 在 `:79-82`）；OpenAPI cancelSimulation：completed／failed 為 409（`LIFECYCLE_CONFLICT`）。
- 問題：頁面依「最後一次 poll」決定要取消哪些 run（最多 1 秒前的狀態）。baseline 先入列、通常先完成；在它完成與下一次 poll 之間按「Cancel the runs」，baseline 的 cancel 回 409，迴圈 `return`，scenario 的 cancel 沒有送出，畫面顯示「could not be cancelled」與 Conflict。使用者的取消只有在 poll 更新後再按一次才生效；若此時 poll 已因讀取失敗停下（等重試），再按仍會先送 baseline 並再得到 409。
- 失敗情境（我以臨時 vitest 重現，用與 `tests/results-view.test.tsx` 相同的 fake API，測試已刪除）：兩個 run 都 running → baseline 的 cancel 回 409 `LIFECYCLE_CONFLICT`、scenario 的 cancel 會回 200（cancellation_requested）→ 按一次 Cancel → 實際的 POST 只有 `/api/v1/simulations/sim_01M4YDQK00TA00000000T032BA/cancel`，畫面沒有「stopping」、有取消失敗的 problem。
- 最小修正：每個 run 各自送 cancel，一個失敗不跳過另一個；409 `LIFECYCLE_CONFLICT`（run 已結束）不當作失敗；結束後一律 `nextAttempt()` 重新讀取；加一個「一個 409、一個 200」的測試，並加一般取消失敗（503）時畫面顯示 problem 的測試。

### Low

**L1：結果頁的反向路徑沒有元件測試（mutant 存活）**

- R6：拿掉 `apps/web/features/results/load.ts:28` 的 `if (!comparison.ok) return comparison;` → `tests/results-view.test.tsx` 21/21 通過（比較 API 的 500 `compare.ErrInconsistent` 或 409 `RESULT_NOT_READY` 沒有任何測試；mutant 下頁面會以 undefined 的 comparison 繪製而拋錯，測試仍全綠）。
- R3：`load.ts:18` 把 trace 下載失敗改成「沒有 trace」→ 通過（`marey.failed.*` 與其 problem 沒被測到）。
- R2：`use-run-pair.ts:80` 不 dispatch `cancelFailed` → 通過（`runs.cancelFailed` 的畫面只有 reducer 單元測試）。
- 報告的範圍第 3 點寫元件測試涵蓋「失敗」，實際只有 run failed 與 poll 失敗。
- 修正：三個 fake API 案例（比較失敗→RemoteView 的 problem 與重試；trace 404／500→圖的失敗說明；取消 503→problem）。

**L2：結束時仍被擋的項目：位置是猜的、表格不計、沒有測試**

- 位置：`apps/web/features/results/marey.ts:234-239`（結束時 `held` 的線畫在 `[current.from, current.from + 1]` 之間）、`:462-479`（`traceSummary`）。
- 問題：trace 的 `blocked` 只有出發 node（`internal/simulation/trace/trace.go`，D-049），不含目的地，所以 `from + 1` 是猜下一列；目的地在上方（rework 回邊）或跨多列時，平線畫在錯的兩列之間，與 SIMULATION_SPEC §9「不能把缺失樣本補成虛構 trajectory」的精神不符（同檔中「服務中結束」的線就選擇停在已知處）。`traceSummary` 只數在列中的停留，所以這些項目不算在「Waiting at the end」，被擋的時間也不在「Longest wait」。Mutant R1（拿掉整個分支）與 R7（改成 `[from, from]`）都讓 `tests/marey.test.ts` 28/28 通過。
- 情境：T-051 範本中 ICT 之後的 `q_fct` 已滿時，ICT 被擋到 horizon 的電路板，其平線畫在 ICT 列與 ICT debug 列之間（下一列），但它要去的是 FCT。
- 修正：結束時仍被擋就停在 `service_end` 的點（與服務中結束一致），或至少把「結束時被擋」另計並加測試。

**L3：`checkPair` 不檢查 baseline 的版本**

- 位置：`apps/web/features/simulations/run-pair.ts:38-42`；`apps/web/features/results/load.ts:26`（只讀 scenario 的版本）。
- 問題：只檢查 scenario run 屬於此 scenario、baseline 的 patch 為 `[]`；不檢查 `baseline.model_version_id` 與 scenario run 相同。網址帶入另一個版本（或同組織另一個模型）的 baseline 時頁面接受，伺服器回 comparable=false（`MODEL_VERSION_MISMATCH`），但 Marey 以 scenario 版本的列畫 baseline 的 trace：node 不存在的線在第一個事件就結束，ID 碰巧相同的畫在錯的列；Facts 只列 scenario 的版本。
- 情境：手動把 `?baseline=` 換成同一模型 v2 的 baseline run。
- 修正：`checkPair` 加 `baseline.model_version_id === scenario.model_version_id`（不符為 not found，與現有的規則一致），或為 baseline 另讀自己的版本並各用各的列。

**L4：T-051 pack 的 ratio 指標以原始小數與英文 "(ratio)" 顯示**

- 位置：`apps/web/features/results/metrics.ts:62-66`（`unitOf` 只認 `minute`、`count`）、`apps/web/features/results/metrics-table.tsx:26`。
- 問題：T-051 已整合，其 domain 指標（`internal/domainpack/electronicsline/metrics.go` 的 `scrap_rate`、`rolled_throughput_yield_input`、各站首次良率等）的 unit 是 `ratio`；表格顯示為 `0.035 (ratio)`、Δ 為小數而不是百分點，zh-Hant 頁面也出現英文的「(ratio)」。值沒有被改（`<data value>` 正確），所以是 Low；但這是電子產線 demo 的主要品質指標。報告「T-032 還剩什麼」已列「domain pack 指標的名稱與單位」。
- 修正：`unitOf` 把 `ratio` 對到 MetricUnit `ratio`（%、Δ 為百分點），並加一個 domain ratio 的測試；名稱的 catalog 可之後再做。

**L5：coordinator 的紀錄與 `planning/tasks.json` 不符**

- 位置：`checks/T-032_VALIDATION.md:200`（「狀態採 (a)：維持 `not_started`，切片紀錄在 completion_evidence」）；`planning/tasks.json:2116`（T-032 的 `completion_evidence` 為 `[]`）。
- 修正：加一筆 completion_evidence（report、evidence 目錄、`reviewer` 指向本檔），與 T-015、T-051 一致。

### Info

- **I1（已驗證）**：我以臨時 Go 測試用 `des.Engine.RunWithTrace`（des-go-0.5.0）跑 T-051 範本 3 天（seed 7、1 個 replication、sampled），trace 5573 個事件：queue_enter 1611、service_start 1593、service_end 1580、blocked／unblocked 各 104、failure 45、rework 41、complete 229、abandon 3，含 `batch_id`；未截斷。臨時 vitest 對它跑 `stepRows`／`rowResolver`／`entityPaths`／`mareyLayout`：263 條線＝232 條到 sink＋18 條結束時在等＋13 條結束時在服務（沒有一條因無法放置而中斷）；每個 `service_start` 都在線上的對應列；兩列之間的停留 104 段＝104 個發生在服務之後的 blocked（其中兩個是 `service_end → rework → blocked`，幾何照樣處理）；兩次計算的版面完全相同；所有點都在圖內。兩個臨時測試已刪除。
- **I2**：T-051 範本的列順序為 release_a、release_b、smt、aoi、aoi_touchup、ict、ict_debug、ict_retest、fct、burn_in、pack_ship、fct_debug、fct_retest、shipped、scrap：FCT 的 debug 與 retest 因 burn-in fail 的路徑較長而排在包裝之後，修理迴圈的線會跨過包裝列。規則（最長路徑、回邊不計）有記錄與測試，列出供 owner 判斷是否要讓修理站跟在自己的站後面。
- **I3**：取消請求進行中，Cancel 仍可按但沒有作用（`apps/web/features/simulations/run-status.tsx:93`），畫面沒有說明；連按只送一次有測試。
- **I4**：指標表的分組列用 `<th scope="colgroup" colSpan={5}>`（`metrics-table.tsx:117`），語意上是列群組的標籤；部分報讀器可能把「Run／Resources／Queues」當作其後所有格子的欄標題。axe 不報；可改為每組一個 `<tbody aria-labelledby>` 或把群組名併入列標題。
- **I5**：D-088 (f)（負 baseline 時 UI 須一併顯示 absolute）：表格在 Δ% 旁一律顯示 Δ，符合；沒有專門的測試。D-090 (b) 的宣告式指標標示未做（D-090 只說明位置，切片不要求）。
- **I6**：暖機期間在圖上沒有標示（目前預設暖機為 0）。`/ui-gallery/results` 以 `process.cwd()` 在 request 時讀 fixture，standalone 部署時檔案可能不在（gallery 預設關閉）。
- **I7**：demo 的缺口（app 尚未掛 AuthProvider；模型頁沒有到 `/scenarios/<id>` 的入口）報告已列，屬 T-043 後續與 T-031 切片。
- **I8**：`checks/docs-check.json` 的 `markdown_files` 會因本輪兩個 review 檔在下一次 `make spec-check` 由 118 變 120，請 coordinator 重產。

## 已核對、沒有問題的部分

- **沒有捏造的值**：`metrics.ts` 的每一列的 baseline、scenario、absolute、relative、status、reason 都取自 Comparison；伺服器沒有的 run 層級指標為 missing（顯示原因、沒有數字）；CI 只取該 run 自己 Result 的 `confidence_interval_mean`（只有 replication population 會有，作者把「CI 用於中位數」判為等價 mutant，我同意）；百分比為 `relative × 100`（Intl percent），比例的差為百分點；baseline 為 0 時沒有百分比（TEST-022）；中位數的一句話只在可比、available、relative 不為 null 時出現。
- **數字保留原值**：每個數字（含 CI 上下界、Δ、Δ%、追蹤表的等待）都有 `<data value={String(v)}>`；作者在 e2e 抓到 bundled JSON 的數字被 minifier 改寫，改為 request 時讀檔並以掃描防止再 bundle，我確認 `app`、`features`、`lib`、`ui` 沒有 import `.json`。
- **D-088**：可比與否完全由伺服器決定；不可比時說明逐項原因、值並排、不給 delta（TEST-024 有測試）；engine_version 不同的案例有測試。
- **Run 的狀態**：queued、running（進度條與「n of m replications」）、running＋cancellation_requested（stopping）、completed、failed（API 的 problem，含 details 與 request ID）、cancelled 都有畫面；兩個都結束才停止 poll（mutant 被擋）；不屬於此 scenario 的 run 不顯示（R4 與作者的 mutant 被擋）；poll 失敗有重試。
- **Marey 的文法**：時間由左往右、步驟由上往下；baseline 1.4 px 石墨虛線、由列頂往下；scenario 1.75 px 深綠實線、由列底往上，lane 不共用；release 點空心／實心；sink 列一條 station line；主次時間格線；取樣說明（replication 1、約 1/8）、截斷與缺少 trace 的說明；`role="img"` 加 title、desc 與表格替代；圖中沒有可聚焦元素；手機上表格在可聚焦、具名的捲動區內（e2e 檢查 390 px 無橫向捲動）。trace 的事件依陣列順序處理（不依 sequence 排序，符合 §9），JS 的 Map 依插入順序。
- **文字不會變成 markup**：沒有 `dangerouslySetInnerHTML`、`innerHTML`；SVG 的 `<text>`、`<title>`、`<desc>` 與表格都以 React 文字輸出；`withNodes` 只切割 catalog 字串中的佔位符。
- **兩語與簡潔**：新字串都在 en 與 zh-Hant；results-simplicity 兩語 × 11 個狀態（至多一個 primary、沒有同名按鈕或重複連結、沒有停用的控制項、取消連按只送一次）；e2e 的 a11y（新 route × 主題 × 語言 × 寬度）、simplicity 與字串測試都通過（`checks/evidence/2026-10-26-T-051-T-032-coordinator/e2e-test-rerun.log`：results.spec 11 個皆 ✓，257 passed、32 skipped 全為截圖）。
- **coordinator 的 tokens test 修改（8eecee4）**：只允許列出的 package import，且被 import 的檔案本身不得有 `@font-face`、`@import`、`url(`，沒有放寬。
- **報告與 evidence**：713 tests（作者）、770 tests 與 257 passed（coordinator）、27 個 mutant（26 擋下、1 等價）、fixture 以真 engine 產生並通過 schema 檢查，都與 log 相符；沒有改 contract、OpenAPI、Go、migration 或依賴，與 diff 相符。

## 我執行的指令

環境：Node 22.22.0、npm 10.9.4（`--engine-strict=false`）、go1.27.3；共用機器 load average 約 20。

| 指令 | exit | 結果 |
|---|---|---|
| `npm ci --prefix apps/web --engine-strict=false` | 0 | |
| `make frontend-test` | 0 | 34 個檔案、770 個 test |
| `make frontend-build` | 0 | 含 `/scenarios/[scenarioId]`、`/scenarios/[scenarioId]/results`、`/ui-gallery/results` |
| 臨時 `go test -count=1 -run TestZZReviewTemplateTrace ./internal/simulation/des/`（測試檔已刪除） | 0 | T-051 範本的真 trace，5573 事件、未截斷 |
| 臨時 vitest：真 trace 上的 Marey 幾何（第一次以過窄的計數條件失敗，修正計數後通過；測試檔已刪除） | 0 | 見 I1 |
| 臨時 vitest：取消時 baseline 回 409（M1 的重現；測試檔已刪除） | 0 | 只送出 baseline 的 POST |
| `/home/user/Geminus/.venv/bin/python scripts/check_docs.py`（只做文件連結與 code fence 檢查；產生的 `checks/docs-check.json` 之後還原） | 0 | PASS，120 個 Markdown 檔（含本輪兩個 review） |
| 7 個 mutant（逐一改一行、跑一個測試檔、`git checkout --` 還原，結束時 `git status` 乾淨） | — | 被擋：R4 接受有 patch 的 baseline、R5 取樣說明寫 replication 0；存活：R1、R7（L2）、R2、R3、R6（L1） |

## 沒有執行的

- `make e2e-test`：依指示不執行；改讀 `checks/evidence/2026-10-26-T-032/e2e-test.log` 與 `checks/evidence/2026-10-26-T-051-T-032-coordinator/e2e-test-rerun.log`。
- `make spec-check`：會重寫 tracked 的 `checks/*.json`，沒有執行（見 I8）。
- `make backend-test`、`integration-test`、race：T-032 沒有改 Go；coordinator 的 log 已有。
- 與真 API／worker 的閉環（T-040）、已登入的瀏覽器頁面、截圖比對、SSE、Node 24、螢幕報讀器、Firefox／Safari：NOT RUN。
- 沒有任何 live AI 呼叫，沒有讀任何 API key 或 secret。

## 要 ACCEPT 需要修正

- M1：取消對每個仍在進行的 run 各自送出，已結束（409 `LIFECYCLE_CONFLICT`）不中止另一個，結束後重新讀取；加對應的測試。
