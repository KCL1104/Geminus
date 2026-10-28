# T-057 demo 路線修正：AI 加入步驟後的畫布排版、AI 文字不寫區塊名稱、回到情境與結果的路

**日期：2026-10-28｜範圍：T-057 live demo（`checks/T-057_AUTH0_VALIDATION.md` 的「需要 owner 知道的事」2–4）找到、評審一定會看到的三個問題；H-038 排定為產品完整度的第一步｜需求：REQ-004、REQ-008、REQ-014、REQ-022、REQ-023｜依據：TWIN_IR_SPEC §7、D-022、H-027、ADR-027**

## 1. 結論

| 問題（live 上看到的） | 原因 | 修正 |
|---|---|---|
| 照片 proposal 批准後，「Shipped」出現在產線中間 | AI edit 不能寫 view（D-022），新步驟沒有座標；layered layout 保留既有座標，sink 留在原位、新步驟排到它右邊 | 畫布的 `canvasLayout`：IR 只替部分步驟存了座標、而且存的座標都在 layered 格點上（沒有人手動放過）時，整條線依 flows 重新排版；第一次編輯把這個排版寫進 draft。有手動放過的步驟時照舊 |
| 「What the AI assumed」出現 `CURRENT_MODEL_IR`、`USER_INTENT` | prompt 只說區塊是資料，沒說使用者看不到區塊 | server rules `context-rules-1` → **`context-rules-2`**：prose 不寫區塊名稱，改用「目前的模型」「你的要求」「影像」等說法。server rules 在每個 role 的 system text 裡（modeler、scenario、photo、analyst、importer），一處涵蓋全部 |
| 離開結果頁後沒有路回去 | 模型頁沒有列出情境；情境頁只有執行表單 | 模型頁列出最新版本的情境（連到情境頁）；情境頁列出「先前的結果」，每一次情境執行配上與它一起開始的 baseline，連到結果頁 |

Server 的 patch、驗證、risk、approval 都沒有改。沒有新的 API：情境頁用既有的 `listSimulations`（`GET /v1/models/{id}/simulations`）與 `listScenarios`。

## 2. 設計

### 2.1 排版（`apps/web/features/model-editor/layout.ts`）

- **不在 server 做**：AI edit 的 patch profile 不允許 `/views`（D-022 只允許移除被刪除步驟的座標），approval 會以同一 profile 重新驗證；server 若替 AI 的 patch 補上座標，approval 就會拒絕，或者被批准的效果與審閱的 diff 不同。所以排版是畫布的投影規則，不是模型的改變。
- **規則**：部分步驟沒有座標，而且每個存下的座標的 x 是 `LAYER_SPACING`（240）的倍數、y 是 `ROW_SPACING`（120）的倍數時，忽略存下的座標，以 layered layout 重排整條線。範本、空產線（`testdata/photos/bare-line.json`）與編輯器的「新增步驟」都放在格點上；拖曳或方向鍵移動的步驟幾乎不會剛好落在格點。
- **已知上限**（程式中的 `ponytail:` 註解）：剛好被拖到格點上的步驟會被當成沒有手動放過。若這變得重要，就在 view 存一個「手動放過」的標記。
- **第一次編輯**：`materialize` 用同一個 `canvasLayout`，替沒有座標的步驟 `add`，替被重排的步驟先 `test` 舊值再 `replace`（T-015 review r1 M1 的規則），所以第一次編輯後畫面上沒有任何步驟跳動。
- 2D 與之後的 3D（`layout_mode: derived_from_2d`）應共用 `canvasLayout`。

### 2.2 AI 的文字（`internal/ai/aicontext/aicontext.go`）

- 新增一條 server rule：「The person reads the prose fields but never sees these blocks: say "the current model", "your request", "the image" and the like, and never name a block (CURRENT_MODEL_IR, ENTITY_INDEX, USER_INTENT, CONTEXT_OMISSIONS, ID_ALLOCATION_POOL, SEGMENTS or any other).」
- `rulesVersion` 改為 `context-rules-2`。它是每個 prompt system text 的一部分，因此也在 InputHash 裡；ADR-027 的說明已同步。
- 各 role 的 template 沒有改，`prompt_template_version` 不變（modeler-0.2.0、photo-0.2.0、scenario-0.7.0）。

### 2.3 回到情境與結果（`apps/web/features/simulations/`）

- **模型頁**（`scenario-list.tsx`）：編輯器下方列出最新版本的情境，每個連到 `/scenarios/{id}`；沒有情境時什麼都不顯示（H-027）；超過一頁時顯示既有的「還有更多」說明（不會默默截斷）；讀取失敗時顯示 problem 與重試。檢視者也看得到。
  - 上限（`ponytail:`）：只列最新版本的情境；較舊版本的情境仍可從它們的 proposal 進入。
- **情境頁**（`load.ts`、`run-pair.ts`、`simulate-view.tsx`）：讀取模型的所有執行（API 依時間由舊到新，每頁 100），以 `earlierPairs` 配對：情境的每一次執行配上「同一版本、`scenario_id` 為 null、patch 為空、`config_hash` 相同、建立時間不晚於它」的最後一次執行（SimulateView 先 queue baseline），由新到舊列出，每項連到結果頁。找不到 baseline 的情境執行不列出。
  - 上限（`ponytail:`）：最多讀 10 頁（1000 次執行）；還有更多時，頁面說明較新的結果可能沒有列出。API 若提供由新到舊的列表就能解除。
- 結果頁的網址組法抽成 `resultsHref`，執行後的導向與「先前的結果」共用。
- 文字（en／zh-Hant）：`scenario.earlier.title`、`scenario.earlier.item`（複數）、`scenario.earlier.cut`、`model.scenarios.title`、`morePages.scenarios`、`loading.scenarios`。
- UI gallery 的兩個 stand-in 會回答情境列表：editor 的回空列表，所以截圖不變；proposal 的回它自己建立的情境。

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
