# T-057 demo 路線修正：AI 加入步驟後的畫布排版、AI 文字不寫區塊名稱、回到情境與結果的路

**日期：2026-10-28｜範圍：T-057 live demo（`checks/T-057_AUTH0_VALIDATION.md` 的「需要 owner 知道的事」2–4）找到、評審一定會看到的三個問題；H-038 排定為產品完整度的第一步｜需求：REQ-004、REQ-008、REQ-014、REQ-022、REQ-023｜依據：TWIN_IR_SPEC §7、D-022、H-027、ADR-027**

## 1. 結論

| 問題（live 上看到的） | 原因 | 修正 |
|---|---|---|
| 照片 proposal 批准後，「Shipped」出現在產線中間 | AI edit 不能寫 view（D-022），新步驟沒有座標；layered layout 保留既有座標，sink 留在原位、新步驟排到它右邊 | 畫布的 `canvasLayout`：IR 只替部分步驟存了座標，而且存下的座標正好是「加入這些步驟之前那條線」的 layered 預設（沒有人放過）時，整條線依 flows 重新排版；第一次編輯把這個排版寫進 draft。有人放過的座標（範本、拖曳、方向鍵）一律保留（review r1 H1、M2 後的規則） |
| 「What the AI assumed」出現 `CURRENT_MODEL_IR`、`USER_INTENT` | prompt 只說區塊是資料，沒說使用者看不到區塊 | server rules `context-rules-1` → **`context-rules-2`**：prose 不寫區塊名稱，改用「目前的模型」「你的要求」「影像」等說法。server rules 在每個 role 的 system text 裡（modeler、scenario、photo、analyst、importer），一處涵蓋全部 |
| 離開結果頁後沒有路回去 | 模型頁沒有列出情境；情境頁只有執行表單 | 模型頁列出每個版本的情境（連到情境頁，review r1 M5）；情境頁另外載入「先前的結果」（不擋執行表單，review r1 M4），每一次情境執行配上與它一起開始的 baseline，連到結果頁 |

Server 的 patch、驗證、risk、approval 都沒有改。沒有新的 API：情境頁用既有的 `listSimulations`（`GET /v1/models/{id}/simulations`）與 `listScenarios`。

## 2. 設計

### 2.1 排版（`apps/web/features/model-editor/layout.ts`）

- **不在 server 做**：AI edit 的 patch profile 不允許 `/views`（D-022 只允許移除被刪除步驟的座標），approval 會以同一 profile 重新驗證；server 若替 AI 的 patch 補上座標，approval 就會拒絕，或者被批准的效果與審閱的 diff 不同。所以排版是畫布的投影規則，不是模型的改變。
- **規則**（review r1 後）：部分步驟沒有座標時，先重建「加入它們之前的那條線」：只留有座標的步驟，每一步連到它直接、或只經過沒有座標的步驟能到達的有座標步驟。存下的座標若**正好等於**這條線的 layered 預設（每一個座標都相同），代表沒有人放過，才忽略存下的座標、以 layered layout 重排整條線；否則照舊保留（`layeredLayout` 的「既有座標不動」，TWIN_IR_SPEC §7、`domainpack.go`）。
- 第一版的規則是「每個座標都在 240／120 的格點上」，review r1 指出兩個反例：出貨的電子產線範本 41 個座標都在格點上、卻是人排的（rework 迴圈往下繞，例如 `aoi_defect` 在 (1200,120)，layered 會放在 1440），AI 加一步就會整條重排（H1）；React Flow 的方向鍵一次 5 px、Shift 20 px，按 6 次 Shift 正好往下一列（M2）。新規則兩者都保留。
- **第一次編輯**：`materialize` 用同一個 `canvasLayout`，替沒有座標的步驟 `add`，替被重排的步驟先 `test` 舊值再 `replace`（T-015 review r1 M1 的規則），所以第一次編輯後畫面上沒有任何步驟跳動。
- 2D 與之後的 3D（`layout_mode: derived_from_2d`）應共用 `canvasLayout`。

### 2.2 AI 的文字（`internal/ai/aicontext/aicontext.go`）

- 新增一條 server rule：「The person reads the prose fields but never sees these blocks: say "the current model", "your request", "the image" and the like, and never name a block (CURRENT_MODEL_IR, ENTITY_INDEX, USER_INTENT, CONTEXT_OMISSIONS, ID_ALLOCATION_POOL, SEGMENTS or any other).」
- `rulesVersion` 改為 `context-rules-2`。它是每個 prompt system text 的一部分，因此也在 InputHash 裡；ADR-027 的說明已同步。
- 各 role 的 template 沒有改，`prompt_template_version` 不變（modeler-0.2.0、photo-0.2.0、scenario-0.7.0）。

### 2.3 回到情境與結果（`apps/web/features/simulations/`）

- **模型頁**（`scenario-list.tsx`）：編輯器下方列出每個版本的情境（版本由新到舊，每項標「以版本 N 為基礎」），每個連到 `/scenarios/{id}`；沒有情境時什麼都不顯示（H-027）；版本或某版本的情境超過一頁時顯示既有的「還有更多」說明（不會默默截斷）；讀取失敗時顯示 problem 與重試。檢視者也看得到。
  - 第一版只列最新版本的情境，並說舊版本的情境「可從 proposal 進入」；review r1 M5 指出 client 沒有列出 proposal 的方法，發布新版本後舊情境就找不到。
  - 上限（`ponytail:`）：版本的第一頁一次、每個版本一次請求；版本很多時，API 提供 model 層級的情境列表就能變成一次。
- **情境頁**（`load.ts`、`run-pair.ts`、`simulate-view.tsx`）：情境與版本先載入、執行表單立即可用；「先前的結果」另外載入（`loadEarlierRuns`，自己的 `useRemote`），讀取失敗只在這一區顯示 problem 與重試（review r1 M4）。讀取模型的所有執行（API 依時間由舊到新，每頁 100），以 `earlierPairs` 配對：情境的每一次執行配上「同一版本、`scenario_id` 為 null、patch 為空、`config_hash` 相同、建立時間不晚於它」的最後一次執行（SimulateView 先 queue baseline），由新到舊列出，每項連到結果頁。找不到 baseline 的情境執行不列出。
  - 上限（`ponytail:`）：最多讀 10 頁（1000 次執行）；還有更多時，頁面說明較新的結果可能沒有列出。API 若提供由新到舊的列表就能解除。
- 沒有完成的一組在連結旁標「仍在執行」或「未完成」（review r1 L7 的一部分）。
- 結果頁的網址組法抽成 `resultsHref`，執行後的導向與「先前的結果」共用。
- 文字（en／zh-Hant）：`scenario.earlier.title`、`scenario.earlier.item`（複數）、`scenario.earlier.cut`、`scenario.earlier.running`、`scenario.earlier.stopped`、`model.scenarios.title`、`model.scenarios.version`、`morePages.scenarios`、`loading.scenarios`。
- UI gallery 的兩個 stand-in 會回答版本與情境列表：editor 的回它的一個版本與空的情境列表，所以截圖不變；proposal 的回它自己的版本與建立的情境。

## 3. 測試

| 測試 | 內容 |
|---|---|
| `tests/editor-adapter.test.ts` 「the canvas's layout」（3 個） | AI 在 work 與 end 之間插入一步：`layeredLayout` 把 end 留在 480，`canvasLayout` 與 `toFlow` 排成 0／240／480／720；每個步驟都有座標，或有手動放過的步驟時照舊；第一次移動寫下的 ops（add、test＋replace）與套用後的座標 |
| `tests/results-view.test.tsx` 情境頁（2 個新增） | 兩頁執行，含另一個 config 的 baseline、另一個情境沒有 patch 的執行、找不到 baseline 的情境執行：只列兩組、由新到舊、href 正確、編輯者與檢視者都看得到、axe 無違規、第二次讀取帶 cursor 與 `limit=100`；10 頁都還有更多時顯示截斷說明 |
| `tests/results-view.test.tsx` 模型頁的情境（2 個） | 連結與名稱、axe；沒有情境時是空的；更多頁的說明；失敗後重試 |
| `internal/ai/aicontext/proposal_test.go` | system text 含 `Server rules (context-rules-2)` 與新規則 |
| 既有的情境頁、結果頁、simplicity、editor 測試 | 測試資料加上執行列表（空的），全部照舊通過 |

先寫了程式再寫測試，沒有照 AGENTS §5 先寫 red 測試；以下面的 mutation 證明測試會抓到錯誤。

### Mutation

| Mutant | 結果 |
|---|---|
| materialize 改回 `layeredLayout` | KILLED |
| `toFlow` 改回 `layeredLayout` | KILLED（加了 `toFlow` 的斷言之後；第一次 SURVIVED） |
| 不檢查座標是否在格點（M6） | KILLED |
| 配對不比 `config_hash`（M1） | KILLED |
| 由舊到新（M2） | KILLED |
| 不比建立時間（M3） | KILLED |
| 只讀第一頁（M4） | KILLED |
| 不顯示截斷說明（M5） | KILLED |
| baseline 不要求 `scenario_id` 為 null（M7） | KILLED（加了「另一個情境沒有 patch 的執行」之後；第一次 SURVIVED） |
| 移除新的 server rule | KILLED |

## 4. NOT RUN

- **Live**：新的 server rule 在 live 上的效果（AI 是否真的不再寫區塊名稱）還沒驗證。為了省花費，等 T-030 的 analyst 開 live 時一起跑一次文字 proposal 與照片 proposal。
- **部署**：還沒部署到 Railway。
- **截圖**：`make e2e-test` 在本機沒有跑截圖比對（只在 CI 的固定 Playwright 容器跑，`E2E_SCREENSHOTS=1`），由 CI 確認。

## 5. 命令與結果（2026-10-28，本機 WSL2；Go 1.27.3、Node 24.21.0）

| 命令 | 結果 |
|---|---|
| `make frontend-test` | exit 0：eslint `--max-warnings=0`、tsc、vitest **48 files、1069 tests passed** |
| `make e2e-test`（Playwright 1.56.1） | exit 0：**514 passed**、32 skipped（截圖，只在 CI 跑），17.6 分鐘 |
| `make backend-test` | exit 0：go vet、**84 個 package ok** |
| `go test ./internal/ai/... ./internal/proposals/... ./internal/aievals/...` | 全部 ok |
| `make spec-check` | exit 0 |

## 6. 獨立 review 第 1 輪（H-029）與修正

Reviewer（獨立 subagent，只讀 commit 15563cd 的程式，沒有 shell）：1 High、4 Medium、4 Low。H-029 的規則：High、Medium 修正並由 coordinator 驗證；Low 延後（L6 與 L7 的一部分很小，一起做了）。

| # | 發現 | 處理 |
|---|---|---|
| H1 | 「都在格點上」不等於「沒有人放過」：出貨的電子產線範本 41 個座標都在格點上、卻是人排的；AI 加一步就會丟掉全部座標，下一次編輯還會寫進 draft | **修正**：規則改為「存下的座標正好是加入步驟之前那條線的 layered 預設」（§2.1）。測試：範本本身不變；範本在 AOI 與其結果之間加一步，41 個座標都不變 |
| M2 | 方向鍵一次 5／20 px，按 6 次 Shift 正好落在下一列的格點 | **修正**（同一條規則）。測試：work 用方向鍵移到 (240,120) 之後，AI 加一步，座標照舊 |
| M3 | 「每個步驟都有座標」的測試座標不在格點上，拿掉 `some(...)` 也會通過；沒有「全部有座標、在格點上、但不是 layered」的情況 | **修正**：加上 check 在 (240,240)（在格點、不是 layered）的情況與範本本身。`some(...)` 的 mutant（L1）仍然存活，但它與原程式等價：全部有座標時，若座標等於 layered 預設，重排的結果也一樣 |
| M4 | 執行列表的任何一頁失敗（429／503），整個情境頁就變成錯誤；執行表單也要等全部讀完才出現 | **修正**：「先前的結果」另外載入（`loadEarlierRuns`），失敗只在那一區顯示 problem 與重試。測試：第一次 503 時表單仍在、重試後列表出現 |
| M5 | 只列最新版本的情境；client 沒有列出 proposal 的方法，發布後舊情境找不到 | **修正**：列出每個版本的情境（§2.3）。測試：兩個版本各一個情境，新版本在前；任一版本的列表有下一頁時顯示說明；失敗後重試 |
| L6 | `listSimulations` 不在 client 的 operation contract 測試裡 | **做了**：`tests/api.test.ts` 檢查 method、path、`cursor` 與 `limit=100`，以及其他種類的 ID 在送出前被拒 |
| L7 | 先前的結果沒有狀態；同一分鐘、同設定的兩次連結文字相同；分頁交錯時可能配到另一次的 baseline | **部分**：沒有完成的一組標「仍在執行」或「未完成」。文字相同與交錯配對延後（配到的 baseline 是同版本、同 config，結果相同；被取消時結果頁會說） |
| L8 | 由舊到新的列表加 1000 筆上限，藏起最新的執行 | 延後（已在頁面說明；需要 API 的排序選項） |
| L9 | 新 server rule 只有文字的測試，live 行為 NOT RUN；「the image」對 analyst／importer 沒有意義 | 延後（「and the like」涵蓋；live 與 T-030 一起驗證） |

### Mutation（第 1 輪修正後）

| Mutant | 結果 |
|---|---|
| L1 拿掉「每個步驟都有座標」的捷徑 | SURVIVED（等價，見 M3） |
| L2 不檢查是否為之前的 layered 預設 | KILLED |
| L3 不經過沒有座標的步驟連接（不重建之前那條線） | KILLED（加上「在 SMT 之前插入一步」的測試之後；只在 sink 前插入時兩者結果相同） |
| L4 一律視為預設 | KILLED |

### 第 1 輪修正後的命令與結果（2026-10-28）

| 命令 | 結果 |
|---|---|
| `make frontend-test` | exit 0：eslint、tsc、vitest **48 files、1071 tests passed** |
| `make e2e-test` | exit 0：**514 passed**、32 skipped（截圖，只在 CI 跑），16.8 分鐘 |
| Mutation（`canvasLayout`） | 見上表 |

Coordinator 驗證：H1 以出貨的範本實際讀檔測試（41 個座標、AOI 後插入一步後全部不變）；M2 以 React Flow 的鍵盤步長換算（Shift 20 px × 6 = 120）測試；M4、M5 各以失敗後重試的測試確認。

## 7. Live 確認（2026-10-28，評審帳號，Meta `muse-spark-1.3`）

證據：`checks/evidence/2026-10-28-live-analyst-3d/`。

- **文字提案的假設不再寫區塊名稱（`context-rules-2`）**：a383141 部署後的第一次文字提案，Meta 回 **404**（`not_found_error`，`AI_REQUEST_REJECTED`，花費 0），與 `checks/T-057_AUTH0_VALIDATION.md` 記錄的出口問題相同。8602c4b 重新部署（worker 換了執行個體）後再送同一個要求（「Add a buffer queue between ICT and FCT…」，edit）：AI 回 **needs input**，因為模型已經有 FCT 前的 buffer；「What the AI assumed」三條都是自然語言（例如「The current model already contains a buffer before FCT with capacity 10…」），沒有 `CURRENT_MODEL_IR`、`USER_INTENT`、`CONTEXT_OMISSIONS` 等區塊名稱。截圖 `D4-text-proposal.png`。
- **Demo 的風險仍在**：Meta 對部分 Railway 出口回 404，部署（或 worker 重啟）之後可能再出現。錄 demo 之前先送一個文字提案；404 時重新部署 worker。
- **照片提案**：這次沒有重跑（省花費）：NOT RUN。
