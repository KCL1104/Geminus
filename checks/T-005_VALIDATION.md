# T-005 驗證報告：JCS/hash 與 frozen dataset manifest

**日期：2026-10-15｜狀態：`review`（待 owner 驗收）｜需求：REQ-005、REQ-011、REQ-013｜Gate：D-006、D-019、D-034（皆已批准）；另用 D-029、D-033**

逐條輸出在 `checks/evidence/2026-10-15-T-005/`（`summary.tsv` 為 exit code 總表，`mutants.py` 為 mutation 清單）。

## 交付內容

| 項目 | 內容 |
|---|---|
| 契約 | `logical_ref`／`samples_ref` 收緊為 `^dataset:[a-z0-9_.-]+$`（D-034，3 處）；dataset snapshot 移除自身 `content_hash`（D-019）；新增 3 個反例 fixture；TS contracts 重新產生 |
| `internal/model/ticks.go` | `model.Ticks`：SIMULATION_SPEC §2 的 quantizer（×unit ms、round half up、正值 sub-tick→1、365 天上限）；validator 的 duration 上限改用同一函式 |
| `internal/model/hashing` | `Canonical`（gowebpki/jcs＋jsontext 獨立 parse、錯誤不引用輸入）、`Model`、`Patch`、`Config`／`NormalizeConfig`、`SortManifest`／`Manifest`、`Result`、`ContentHash`；定義見 TWIN_IR_SPEC §5.1 |
| `internal/model/validate` | `Bindings`：D-033 binding 階段（empirical 0 sample 的 interarrival 與零時間 cycle）；`Document`：任一 embedded schema 的 parse＋schema 階段 |
| `internal/storage/datasets` | `Resolve`：manifest shape／coverage → tenant-scoped `Store` 取物件 → 先驗 hash 再 parse → organization／version／logical_ref／unit／sample_count／365 天 → `validate.Bindings`；回傳排序後 manifest、manifest hash 與 samples |
| 獨立 oracle | `testdata/hashing/`：RFC 8785 原文向量、V8 數字向量（`scripts/gen_number_vectors.mjs`）、Python `rfc8785` 的 expected hashes 與 400 份隨機文件（`scripts/check_hashes.py`，`make spec-hashes` 已納入 `make spec-check`）；CI frontend job 以 Node 重新產生 V8 向量並 diff |
| Fixtures | `valid-model-version.json` 的 `hash`、`valid-scenario.json` 的 `patch_hash`、`valid-result.json` 的 `result_digest` 改為其內容的真實 hash；其餘 `sha256:000…` 仍是格式 placeholder |
| 文件 | TWIN_IR_SPEC §5.1／§5.2、SIMULATION_SPEC §2／§9、ADR-014（PROPOSED）、`testdata/hashing/README.md`、contracts README |

## 實際執行結果（最終一輪）

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | 含新的 `spec-hashes`：27 個 hash 與 Python 一致、fixture hash 欄位一致、文件語料未過期 |
| `go test -v ./internal/model/hashing/...` | 0 | 22 個 test／subtest PASS |
| `go test -v ./internal/storage/datasets/...` | 0 | 32 個 test／subtest PASS |
| `go test -v ./internal/model/...` | 0 | 125 個 test／subtest PASS，0 FAIL |
| `go test -race ./internal/model/... ./internal/storage/...` | 0 | 含並行 hash 與並行 Resolve |
| `make backend-test` / `backend-race` / `backend-build` | 0 / 0 / 0 | |
| V8 向量重新產生並 diff | 0 | 與入庫檔 byte 相同 |
| V8 擴充：1,000,000 個數字 | 0 | 全數一致（產生檔不入庫） |
| Python 擴充：100,000 份隨機文件 | 0 | 全數 byte 一致（產生檔不入庫） |
| Fuzz `FuzzCanonical` 60 s | 0 | 約 200 萬次；性質：不 panic、輸出為合法 JSON 且冪等 |
| Fuzz `FuzzStoredSnapshot` 30 s | 0 | 59 萬次；任意儲存 bytes 不 panic、資料問題不回 error、valid 時 samples 數正確 |
| Fuzz `FuzzIR` / `FuzzApply` 各 20 s | 0 / 0 | validator 重構後回歸 |
| `BenchmarkResolveMaxSnapshot`（100,000 samples） | 0 | 約 208 ms／次；**只是資料點**，見風險 |
| `BenchmarkLargeIR` | 0 | 約 68 ms／次（T-004 為 62–69 ms，重構未退化） |
| Mutation（`go test -overlay`） | 0 | 37/37 被擋；1 個 documented equivalent（見下） |
| `govulncheck v1.9.0` | 0 | No vulnerabilities found |
| `make frontend-test` | 0 | TS contracts 變更後 lint／typecheck／unit 通過 |
| `actionlint v1.7.12` | 0 | CI 新增 V8 向量檢查步驟 |

## 驗收測試對照

- **TEST-014**（empirical immutability）：hash 錯（bytes 多一個空白）、未綁定的 ref、他租戶 version、snapshot 的 version／logical_ref／unit／sample_count 與 binding 不符，皆在精確 path 失敗且不帶 datasets；hash 不符時不 parse 物件。新資料以新 dataset version 進來時，凍結在舊 version 的 manifest 仍解析為舊 samples、manifest hash 不變；只有明確的新 binding 會改用新 version。「既有 run 結果不變」的端到端部分需 engine 與 run API（T-020／T-021），**NOT RUN**。
- **TEST-019**（部分）：result_digest 的 payload 已定義並測試：run_id、status、trace、result_digest 自身不影響 digest；engine_version、metrics、replication、measurement window、config／manifest hash、seed 會影響；NaN metric 回錯而非產生 digest。「同一 frozen input 在不同 worker 並行下 digest 一致」需要 engine（T-011／T-020），**NOT RUN**。

## 開發中發現並修正的缺陷

1. **Fuzz 抓到 Canonical 非冪等**（第一輪 evidence 的 `fuzz-canonical` exit 1）：我原本額外拒絕超過 2^53−1 的整數 literal，但 `1e17`（exponent 寫法，被接受）的 JCS 輸出是 `100000000000000000`，再 canonicalize 就被拒。Go 重新 marshal 的 patch candidate 也會這樣寫，因此 validator 接受的 IR 可能算不出 effective_model_hash。已移除該規則，改為明確記載 RFC 8785 的 binary64 語意（精度外不同的文件同 hash，精確整數欄位由 schema 限制），並加入回歸測試與 fuzz corpus 檔 `internal/model/hashing/testdata/fuzz/FuzzCanonical/97537447d449a8c7`。
2. **錯誤訊息洩漏 member name**：第一版回傳 JSON Pointer，free-form metadata 的 key 可能是客戶資料；改為只帶 byte offset，並以 mutant 驗證測試會抓到。
3. **Mutation 抓到 4 個測試缺口**：binding schema 檢查、snapshot 大小上限、cancel 時的空 manifest 都被下一階段「碰巧回同一路徑」掩蓋；已改測試：要求 shape 失敗時 Store 讀取次數為 0、超限 snapshot 用合法 JSON、空 manifest 也要回 cancel。
4. **Differential fuzz（1.6M 次）**：沒有找到 jsontext 拒絕而 jcs 接受的輸入。jsontext 前置 parse 因此只是第二個獨立 parser 與錯誤分類來源，不是額外拒絕能力；程式註解與 ADR-014 已照實寫。

## Equivalent mutant

`math.Round(x)` → `math.Floor(x+0.5)`：在 365 天範圍內，兩者只在 x = 0.49999999999999994 不同，而正值 sub-tick 一律 clamp 為 1 tick，結果相同。推導見 `mutants.py` 註解，未為它加測試。

## 需 owner 知悉的實作解讀

TWIN_IR_SPEC §5.1／§5.2 已記錄，若不同意可另立決策：

1. **config_hash 的 payload** 為 `{horizon_ticks, warmup_ticks, replications, seed, trace_level}`，所以 `120 hour` 與 `5 day` 同 hash（D-019 的直接結果）。
2. **Model 未使用的 binding 允許存在**，但仍會驗證並計入 manifest hash：run 沿用 version manifest，scenario 可能把 empirical 改成其他分布。
3. **Binding 階段的 issue path** 指向 ModelVersion 形狀：`/ir/...` 與 `/dataset_bindings/<i>/...`。
4. **他租戶與不存在的 dataset version 回同一訊息**，不洩漏他租戶資料是否存在。
5. **`MaxSnapshotBytes` = 4 MiB** 是實作預設（schema 上限 100,000 samples 的 compact JSON 約 0.7–2.5 MB；benchmark 用的檔案為 0.69 MB），屬 D-007 的 benchmark 範圍。

## 剩餘風險

| 風險 | 說明 / 處置 |
|---|---|
| Binding 效能 | 單一 100,000-sample snapshot 約 208 ms，69% 花在 JSON Schema 逐數字的 `big.Rat` 範圍檢查。少量 binding 沒有問題；若 D-007 允許大量大型 binding，T-037 需 benchmark 後決定是否改用與 schema 等價的 typed 檢查（須有 parity test，不能另立第二份 source of truth） |
| 跨平台 digest | 只在 linux/amd64 驗證（D-006 已接受） |
| jcs 依賴 | 單一維護者的 fork；三組 oracle 與 CI 會擋住行為改變，升級時須全部重跑 |
| Go／Python 的大整數處理不同 | 超過 2^53−1 的整數 literal：Go 接受（binary64），Python `rfc8785` 拒絕。不會對同一輸入產生不同 hash；這類欄位已由 schema 限制 |

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| Postgres／object storage 的 `Store` 實作、RLS 下的讀取 | T-006（repository foundation）／T-012（import freeze）；本次只用 test double 驗 resolver 邏輯 |
| SQL `ORDER BY logical_ref COLLATE "C"`（D-034 的 SQL 部分） | T-006 scope |
| 端到端「已提交 run 不受新資料影響」、worker 並行下 digest 一致 | 需要 engine／run API（T-011、T-020、T-021） |
| `make db-test`（本機） | 本次未改 SQL；CI db-smoke job 仍會執行 |
| GitHub Actions | 推送後補記 |
