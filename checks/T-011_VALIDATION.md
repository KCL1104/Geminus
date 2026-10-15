# T-011 驗證報告：SoftwareDeliveryDomainPack／full template

**日期：2026-10-15｜狀態：`review`｜需求：REQ-003、REQ-004、REQ-015｜Gate：D-017（已批准）**

逐條輸出在 `checks/evidence/2026-10-15-T-011/`。

## 交付內容

| 項目 | 內容 |
|---|---|
| `internal/domainpack` | 原報告的 DomainPack 介面（ID、NodeCatalog、ResourceCatalog、Validate、InferFromEvents、ComputeMetrics、PromptContext、DefaultViews）；明確註冊的 `Registry`，未註冊 domain 回 `ErrUnsupportedDomain`（REQ-003 fail closed）；`CanonicalEvent` 型別（與 schema parity）；決定性的 `LayeredLayout` |
| `internal/domainpack/softwaredelivery` | 11 個 node kind（涵蓋全部 8 種 generic node type）與 4 個 resource kind；domain rule `DOMAIN_MISMATCH`；`Template`：D-017 synthetic full pipeline（Source→Review→CI→Test→Deploy→Sink），換上新的 model ID 與名稱後完整驗證；`ComputeMetrics`：五個 DORA 指標全部 unavailable 並附原因；`InferFromEvents` 回 `ErrUnavailable`（T-027）；`PromptContext` 只含靜態 domain 知識 |
| 文件 | TWIN_IR_SPEC §4 新增 `DOMAIN_MISMATCH`；ARCHITECTURE §3 記錄實作 |

## 實際執行結果

| 指令 | Exit | 說明 |
|---|---|---|
| `make spec-check` | 0 | |
| `go test -v ./internal/domainpack/...` | 0 | 11 個 test PASS |
| `make backend-test` / `backend-race` | 0 / 0 | |
| Mutation（`go test -overlay`） | 0 | 10/10 被擋 |
| `govulncheck v1.9.0` | 0 | No vulnerabilities found |
| GitHub Actions run [38763694574](https://github.com/KCL1104/Geminus/actions/runs/38763694574)（991005a） | success | spec-check、frontend、backend、db 4/4 |

## 驗收測試對照

- **TEST-001**：`CanonicalEvent` 與 `canonical-event.schema.json` 欄位 parity，fixture round-trip 不增減欄位。
- **TEST-002**：template 經完整 `validate.IR`（schema＋語意）為 valid；換名稱為空字串會被拒絕。
- **TEST-023**：五個 DORA 指標（change lead time、deployment frequency、change failure percentage、failed deployment recovery time、deployment rework rate）在 template 有 CI failure／rework 的情況下仍全部 `unavailable`，沒有任何數值，reason 指向 D-010 並說明 generic failure／rework 不是 DORA；輸出符合 `MetricSummary` schema；generic failure count 不被改動。
- **REQ-004**：template 存在 change_arrival→code_review→ci→test→deploy→deployed 的正機率路徑；metadata 標明 fixture、非客戶資料與三條 synthetic 假設。

## 需 owner 知悉的實作解讀

1. Domain rule 目前只有「domain 必須相符」。結構、引用、路由、分布由 generic validator 負責，不重複；我沒有自創其他 software-delivery 限制（例如 process 必須需要資源），因為來源沒有規定。
2. Node catalog 是編輯器 palette 與 AI prompt 用的「種類」，不寫進 IR；IR 仍只用 generic node type。
3. Template 是 `testdata/golden-models/default-delivery.json` 的嵌入複本（`go:embed` 不能讀上層目錄），由測試保證兩者內容相同。

## NOT RUN／延後

| 項目 | 原因 |
|---|---|
| Event inference | T-027 |
| 有 mapping 時的 DORA 計算 | 需要 pilot 資料與 mapping 定義（D-010），P0 不提供 |
| Generic test-only pack 與 architecture check | T-042 |
