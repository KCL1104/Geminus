# T-004 驗證報告：IR／patch 語意驗證器

**日期：2026-10-15｜狀態：`review`（待 owner 驗收）｜需求：REQ-003、REQ-004、REQ-006、REQ-008、REQ-011｜Gate：D-005、D-014、D-020–D-023、D-028、D-029、D-033（皆已批准）**

逐條輸出在 `checks/evidence/2026-10-15-T-004/`（`summary.tsv` 為 exit code 總表）。

## 交付內容

| 項目 | 內容 |
|---|---|
| `schemas/embed.go` | 將 `schemas/*.schema.json` 嵌入 binary；Go 端驗證永遠使用同一份 source of truth |
| `internal/model/validate` | 階段：parse → JSON Schema → limits → IDs → references → routing／reachability／zero-time → distributions／resource demand → constraints；同階段收集、有 error 即停（D-023）；決定性排序 |
| `internal/model/patch` | RFC 6901 單次解碼、深度 ≤32；三種模式 allowlist（D-021、D-022）；test-before-modify（D-020）；在 copy 上原子套用後完整驗證 candidate |
| 依賴 | `santhosh-tekuri/jsonschema/v6` v6.0.3、`golang.org/x/text` v0.42.0（ADR-013）；CI 新增 govulncheck |
| 規格文件 | TWIN_IR_SPEC §4／§6 補錯誤碼、排序、D-020 規則與 T-004 解讀；SIMULATION_SPEC 依 D-032／D-033 改寫優先序與 zero-time |

## 實際執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `go test -v ./internal/model/...` | 0 | 98 個 test／subtest PASS，0 FAIL |
| `go test -race ./internal/model/...` | 0 | 含 schema 共用與 Apply 的並行測試 |
| `make backend-test` / `backend-race` / `backend-build` | 0 / 0 / 0 | |
| `make frontend-test` | 0 | 未變更前端，確認無回歸 |
| Fuzz `FuzzIR` 30 s | 0 | 681,674 次；性質：不 panic、結果決定性且已排序、valid 必有 IR |
| Fuzz `FuzzApply` 30 s | 0 | 1,074,708 次；性質：不 panic、base 不被修改、有 error 必無 candidate |
| `BenchmarkLargeIR`（500 nodes、約 750 flows） | 0 | 約 69 ms／次；**只是資料點，不是 T-037 效能驗收** |
| `govulncheck v1.9.0` | 0 | No vulnerabilities found（升級 x/text 後） |
| `actionlint v1.7.12` | 0 | |

Fuzz 以 `-fuzzminimizetime 0` 執行：預設的 interesting-input 最小化期間 execs 不累計，會被誤看成卡住；已以有／無最小化兩次比較確認不是 validator 卡住。

## 驗收測試對照

- **TEST-001**：Go 與 Python 對所有共用 IR fixtures 結論一致（正例皆 valid；schema 反例在 Python 釘住的精確路徑回 IR_SCHEMA_INVALID）。
- **TEST-002**：duplicate ID、unknown refs、probability sum、triangular 順序、no sink、zero-time cycle 皆在精確路徑失敗；另含 view reference、queue／source 路由、resource demand、constraint、限制與 warning 案例。
- **TEST-003**：root／identity／move／copy／非法 escape 被拒；解碼 token 繞過（`~1`、`~0`、前導零 index）被拒；第二個 op 失敗時第一個不生效且 base 不變。

## 證明測試會失敗（mutation）

| 對象 | Mutant | 結果 |
|---|---|---|
| patch | array 修改不寫回 parent（重現開發中修掉的 bug）、拿掉 D-020 檢查、scenario 全放行、非原子（失敗後繼續）、pointer 不解碼、陣列元素 test 永遠成立、物件 test 永遠成立 | 7/7 被擋（陣列元素 test 一項起初漏抓，補測試後才擋下） |
| validate | 由 fuzz 抓到 schema 錯誤訊息非決定性（函式庫依 map 順序列欄位），已修正 | — |

## 開發中發現並修正的缺陷

1. `Semantic()` 初版用 defer 累積 warning，`return` 先求值導致 warning 永遠遺失。
2. `walk()` 初版 map 層「複製後 defer 寫回」，陣列插入／刪除不會寫回 parent，刪除時會讓最後一個元素重複。改為逐層回傳的遞迴寫法並加回歸測試。
3. `golang.org/x/text` v0.14.0 有兩個已知模組漏洞，已升級。

## 需 owner 知悉的實作解讀

TWIN_IR_SPEC 已記錄，若不同意可另立決策調整：(1) source 不接收 incoming flow；(2) 任何可達但到不了 sink 的 node 都回 NO_REACHABLE_SINK；(3) constraints 以 `node_id` 作為 D-020 的識別欄位；(4) D-020 對三種模式都強制，包含人工 draft 編輯（2D editor 產生 patch 時需帶 test）。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| Dataset binding 階段（empirical 是否零時間、hash／unit） | T-005 |
| Risk classification | T-025（D-016） |
| RunConfig 驗證（0≤W<H≤365d） | 由 run API／engine（T-020／T-023）負責 |
| 效能 gate（validation <1 s） | T-037 依固定 reference 機器量測 |
