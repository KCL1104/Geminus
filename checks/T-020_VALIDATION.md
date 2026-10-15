# T-020 驗證報告：DES compiler、clock、heap、RNG

**日期：2026-10-15｜狀態：`done`（依 H-003 由 agent review 驗收；owner 可隨時重開）｜需求：REQ-011｜Gate：D-005、D-006、D-023、D-029、D-032、D-033（皆已批准）；本任務的實作語意另登記為 D-038（PROPOSED，gate 在 T-023）**

首輪輸出在 `checks/evidence/2026-10-15-T-020/`，review 修正後的輸出在 `checks/evidence/2026-10-15-T-020-review/`。

> **更正（2026-10-15 獨立 review 後）**：首版報告有五處說得太滿，已在下文改正，原句列在「首版說法更正」。

## 交付內容（`internal/simulation/engine`）

| 檔案 | 內容 |
|---|---|
| `rng.go` | SplitMix64、(0,1) uniform、replication seed、(node, purpose) stream seed（SIMULATION_SPEC §4） |
| `sample.go` | 四種分布的 sampler：constant 與 min=mode=max 的 triangular 不消耗 draw；exponential = -mean·ln(1-U)，至少 1 tick；triangular 標準 inverse CDF，量化前 clamp 回 [min,max]；empirical floor(U·N)。量化與 `model.Ticks` 相同，超過 365 天時改為「必晚於 horizon」的值。`ln` 是固定的對數演算法，不用平台的 `math.Log`（見下文 numerical profile） |
| `events.go` | 事件 heap，key = (tick, priority 0/10/20, sequence)；sequence 在 replication 內嚴格遞增 |
| `compile.go` | `Compile(RunInput)`：重新完整驗證 IR（schema、語意、以實際 dataset 樣本重跑 D-033）；核對 manifest（schema、logical_ref 排序且不重複、涵蓋每個 empirical、與 Datasets 完全一致）；檢查 RunConfig 與 ExecutionLimits（schema、365 天上限、0≤warmup<horizon，錯誤指到 `/config/horizon`、`/config/warmup`）；產生 immutable 的 `Program`。錯誤為 `*InputError`，issues 依 (path, code) 排序 |
| `replication.go` | 單一 replication 的 clock 與 event loop：source 依 ID 初始化、第一個 arrival 在一個 interarrival 之後、max_entities、horizon 同 tick 的事件會處理、晚於 horizon 不排程；entity ID `r<rep>/<source>/<n>`；每次 pop 檢查 cancellation 並扣共用的 event budget（`ErrEventBudget`），不變量破壞回 `ErrInternal` |
| `scripts/engine_reference.py` | 不共用 Go 程式碼的 Python 參考：SplitMix64、seed、量化、四種 sampler、事件順序、arrival 流程，以及逐運算移植的同一個 `ln` 演算法；產生 `testdata/simulation/engine-vectors.json` |
| `scripts/check_no_fma.sh`（`make backend-numeric`） | engine 程式在 amd64 v3、arm64、ppc64le、s390x、riscv64、loong64 的組語中不得有 fused multiply-add 或平台 transcendental 呼叫；並以 GOAMD64=v3 重跑數值測試。CI 的 backend job 會執行 |
| `docs/THIRD_PARTY_NOTICES.md` | `ln` 改寫自 Go `math.log`（BSD-3-Clause）與 FreeBSD e_log.c 的授權聲明 |

佇列、資源、服務與路由的 kernel 是 T-021；指標與 digest 是 T-022。`onArrival`／`onEvent` hook 是 T-021 接入點；未接 handler 的事件會回 `ErrInternal`。

## 獨立 review 結果與處理

Review 由另一個 agent 以對抗方式進行（H-003），它沒有修改 repo。

| # | 發現 | 等級 | 處理 | 紅→綠證據 |
|---|---|---|---|---|
| 1 | `math.Log` 在 amd64 是 assembly、在其他 GOARCH 是可能被融合成 FMA 的 pure Go；Python 參考用 glibc log，因此「逐 tick 相同」只對 36 個儲存的 draw 成立 | 中 | 改用固定的 `ln`（Go pure-Go 演算法，每個乘積以 `float64()` 明確捨入）；Python 參考移植同一演算法；新增 10^6 個 ln 輸入與每個 sampler 10^5 次 draw 的 SHA-256 向量、常數 bit pattern 與邊界值；`make backend-numeric` 檢查六個 target 無 FMA | 未明確捨入的版本在 GOAMD64=v3（本機 CPU 支援 FMA）下 `TestLogProfile` 與 3 個 exponential digest 失敗；明確捨入後 v1／v3 皆通過（`red-before-fix.log`、`green-after-fix.log`） |
| 2 | triangular min>0 時可抽到 0 tick（min=mode=1e-300、U=2^-53） | 低 | 量化前 clamp 回 [min,max] | `TestTriangularStaysInRange` 先失敗（draw 0 ticks）後通過 |
| 3 | dataset issues 沒依 (path, code) 排序（D-023） | 低 | `validate.Sort` | `TestCompileDatasetIssuesSorted` 先失敗後通過 |
| 4 | config 錯誤只指到 `/config` | 低 | 指到 `/config/horizon`、`/config/warmup`、NaN 指到 `.../value` | `TestCompileRejects` 三個案例先失敗後通過 |
| 5 | 測試缺口（7 個有意義的存活 mutant） | — | 補測試：budget 恰好用完、dataset 單位、ARRIVAL 先排下一個再路由、requires 排序、max_entities 後不再抽樣（arrival 結束時比對 stream state）、log1p／拆開 sqrt（由 10^5 draw digest 抓） | 見 mutation 結果 |
| P1 | Compile 沒核對 `RunInput.Manifest` | 待決 | 採用：Compile 自行核對（模型未用到的 binding 允許，與 `datasets.Resolve` 相同） | `TestCompileChecksManifest` 6 個案例先失敗後通過 |
| P2 | entity 表保留所有建立過的 entity（10M entity 約 604 MB） | 待決 | T-021 kernel 改為只保留 active entity（記憶體受 max_active_entities 限制） | 見 T-021 報告 |
| P3 | route 保留 validator 的機率容差 | 待決 | T-021 對非 decision 不用 U、decision 用最後一條兜底；T-021 補測試 | 見 T-021 報告 |
| P4 | 「T-020 補充」語意寫進已批准的 spec 卻沒有 D 編號 | 待決 | 登記為 D-038（PROPOSED），gate 在 T-023；agent 不自行批准 | — |

## 實際執行結果（review 修正後）

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | 含 `engine_reference.py`：10 個 sampler 案例（各 10^5 draw digest）、10^6 個 ln 輸入、10 個事件、9 個 arrival |
| `make backend-test` / `backend-race` / `backend-build` | 0 / 0 / 0 | |
| `REQUIRE_GOAMD64_V3=1 make backend-numeric` | 0 | 六個 target 無 FMA、無平台 transcendental 呼叫；GOAMD64=v3 測試通過 |
| Fuzz `FuzzCompileAndArrive` 60 s | 0 | 1,289,879 次 |
| Mutation（`checks/evidence/2026-10-15-T-020-review/engine-mutants.py`） | 0 | **51/51 被擋，1 個 equivalent**：首輪 30 個（依現行程式碼改寫）＋ review 的存活 mutant（8 個；拆開 sqrt 的兩個分支各算一個）＋ 14 個針對修正的 mutant；`other-ref-ok` 在 manifest 核對後已無輸入可達，列為 equivalent 並在腳本中說明。腳本與 log 已提交 |
| `actionlint` | 0 | |
| GitHub Actions | 成功 | [run 38784747338](https://github.com/KCL1104/Geminus/actions/runs/38784747338)（5b625bb）：4/4 job 成功；backend job 的 `make backend-numeric` 在 `REQUIRE_GOAMD64_V3=1` 下實際跑了 GOAMD64=v3 測試（log：「PASS numeric tests as a GOAMD64=v3 build」）。首版 b8e19ad 的 run 38779234290 亦 4/4 成功 |

## 驗收對照

- **Numerical profile**：`ln` 與 Python 參考在 10^6 個輸入上逐 bit 相同；常數與邊界值（含 √½·2^32，唯一能分辨 `<` 與 `<=` 的輸入）逐 bit 相同；與 `math.Log` 相差不超過 1 ulp。每個 sampler 的 10^5 次 draw（tick 與量化前的 binary64 值）與參考的 digest 相同。
- **Golden vectors**：12-draw sampler 向量、事件順序、三個 source 的 arrival 序列與參考相同；sampler 與 arrival 結束時的 stream state 也相同，因此每個 source 的抽樣次數一致（max_entities 後不再抽，晚於 horizon 的那一次抽樣仍消耗）。
- **GOLDEN-001 的 arrival**：100 個 entity 於 10、20、…、1000 分鐘到達；horizon 恰在 1000 分時第 100 個仍處理，999.99999 分時只有 99 個。
- **決定性**：同一 program 與 replication index 結果相同，平行執行也相同；不同 index 不同；新增 source 不改變其他 source 的 arrival。同 tick 同 priority 的事件依排程順序：第一輪 arrival 依 source ID，之後依各自排程時間（a 每 10、b 每 20 分鐘時，t=20 的 b/1 先於 a/2，有測試）。
- **事件預算**：恰好 max 個事件可以跑完，max-1 失敗且 `Used()` 為 max-1。
- **D-033／溢位**：dataset 有 0 樣本且位於 rework loop 時回 ZERO_TIME_CYCLE；interarrival 可能為 0 時回 DISTRIBUTION_INVALID；triangular 不會抽出 0；delay 超出範圍回 `ErrInternal`。
- **拒絕輸入**：不合法 IR、manifest 問題（缺 binding、與 dataset 不一致、多出的 dataset、schema 不合法、重複、未排序）、缺 dataset、樣本數不符、樣本超過 365 天、warmup≥horizon、replications=0、horizon 或 warmup>365 天、NaN horizon、負 seed、未知 trace level、limits 超過或缺少——除「缺少 limits」逐欄回報外，測試都比對確切的 JSON Pointer 與 code。
- **不共用狀態**：Compile 後修改輸入 IR 不影響 program。

## 首版說法更正

| 首版 | 更正 |
|---|---|
| 需求 REQ-007、REQ-008、REQ-009 | 應為 REQ-011（`planning/tasks.json`） |
| 「與 Python 參考逐 tick 相同」 | 首版只對儲存的 36 個 exponential draw 成立；現在由 ln 與 sampler 的大樣本 digest 支撐 |
| 「stream 狀態也相同，證明消耗的 draw 數一致」 | 首版只檢查 sampler 向量；現在 arrival 也檢查 |
| 「Mutation 30/30」 | 首版 mutant 未提交、無法重現；review 另找出 7 個有意義的存活 mutant。現在腳本已提交（51/51＋1 equivalent） |
| 「同 tick 的不同 source 依 ID 順序」 | 只有第一輪如此；之後依排程順序（見上） |
| Fuzz「在 budget 內跑完」 | 只 fuzz IR bytes，config、limits、datasets 固定；ErrEventBudget 視為正常結束 |
| 「全部回帶 JSON Pointer」 | 首版 horizon>365 天的測試只檢查 `/config` 前綴且接受任何 code；現已改為確切比對 |

## 追溯

- TEST-019（RNG／aggregate order）：本任務涵蓋 seed、stream 與 arrival 的決定性；digest 層面在 T-022。
- TEST-017（clock／warmup／censoring）：只涵蓋 horizon 邊界；warmup 與 censoring 在 T-021／T-022。（2026-10-15 更正：首版把這兩個 ID 對調，T-021 review 指出。）
- 規劃輸出 `internal/simulation/compile/` 未建立：compile 放在 `internal/simulation/engine/`，因為 `Program` 的未匯出欄位由 replication 直接讀取，拆成兩個 package 需要匯出內部結構。

## 需 owner 知悉的實作解讀（D-038，PROPOSED）

1. ARRIVAL 事件先排下一個 arrival、再路由新 entity（決定同 tick 的 sequence）。
2. 晚於 horizon 的事件不放進 heap，但抽樣仍消耗 stream。
3. exponential 樣本至少 1 tick；超過 365 天的樣本視為「永不在 horizon 內」。
4. decision 的 0% flow 不列入 routes。
5. triangular 的值 clamp 回 [min,max]。
6. 固定的 `ln` 演算法，engine 不呼叫平台 transcendental。
7. Compile 自行核對 manifest。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| 在實體 arm64／其他非 amd64 機器上跑測試 | 本環境只有 amd64；以組語檢查（無 FMA、無平台 math 呼叫）與 GOAMD64=v3 測試替代。跨架構 byte-identical digest 不宣稱 |
| queue／resource／service／routing kernel、GOLDEN-001/002 的完整結果 | T-021 |
| 指標、warmup cohort、result digest、trace | T-022 |
| SimPy differential | T-037 |
